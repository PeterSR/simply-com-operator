/*
Copyright 2026 The simply-com-operator Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simplycomv1alpha1 "github.com/PeterSR/simply-com-operator/api/v1alpha1"
	"github.com/PeterSR/simply-com-operator/internal/simply"
)

// ClientFactory makes a Simply.com client for an API key.
type ClientFactory func(apiKey string) *simply.Client

// SimplyAccess turns account references into Simply.com clients, and caches zone listings.
type SimplyAccess struct {
	// Reader reads Accounts and ClusterAccounts, normally from the cache.
	Reader client.Reader
	// SecretReader reads Secrets. It is meant to be uncached, so the operator does not keep every
	// Secret in the cluster in memory, and a rotated key is used at once.
	SecretReader client.Reader
	// ClusterResourceNamespace is where a ClusterAccount's secret is looked up when its reference
	// names no namespace: the namespace the operator runs in.
	ClusterResourceNamespace string
	NewClient                ClientFactory
	// ZoneCacheTTL is how long a zone listing is reused. Simply.com can only list a whole zone, so
	// without it every DNSRecord in a burst would fetch the same zone again.
	ZoneCacheTTL time.Duration

	mu    sync.Mutex
	zones map[string]zoneEntry
	// unpublished holds zones written to since their last successful reload; published holds
	// zones reloaded since the process started. A zone in neither is reloaded once, in case a
	// write before a restart was never published.
	unpublished map[string]bool
	published   map[string]bool
}

type zoneEntry struct {
	records []simply.DNSRecord
	fetched time.Time
}

// errAccount is an account problem the user has to fix, with the condition reason to report.
type errAccount struct {
	reason string
	err    error
}

func (e *errAccount) Error() string { return e.err.Error() }
func (e *errAccount) Unwrap() error { return e.err }

func accountError(reason, format string, args ...any) error {
	return &errAccount{reason: reason, err: fmt.Errorf(format, args...)}
}

// reasonFor is the condition reason for err.
func reasonFor(err error) string {
	if ae, ok := errors.AsType[*errAccount](err); ok {
		return ae.reason
	}
	return simplycomv1alpha1.ReasonAPIError
}

// account is a resolved Account or ClusterAccount.
type account struct {
	// ref names the Account or ClusterAccount.
	ref        string
	client     *simply.Client
	zones      []string
	allowAdopt bool
	// cacheKey identifies the API key without holding it.
	cacheKey string
}

func (a *account) allowsZone(zone string) bool {
	if len(a.zones) == 0 {
		return true
	}
	return slices.ContainsFunc(a.zones, func(z string) bool { return strings.EqualFold(z, zone) })
}

// Resolve finds the account ref names for an object in namespace and reads its API key. An empty
// namespace skips the ClusterAccount's namespace check, for the account's own controller.
func (s *SimplyAccess) Resolve(ctx context.Context, namespace string, ref simplycomv1alpha1.AccountReference) (*account, error) {
	var spec simplycomv1alpha1.AccountSpec
	var secretKey types.NamespacedName
	var key string

	switch ref.Kind {
	case simplycomv1alpha1.AccountKindNamespaced:
		var acc simplycomv1alpha1.Account
		if err := s.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &acc); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, accountError(simplycomv1alpha1.ReasonAccountNotFound, "Account %s/%s not found", namespace, ref.Name)
			}
			return nil, err
		}
		spec = acc.Spec.AccountSpec
		secretKey = types.NamespacedName{Namespace: namespace, Name: acc.Spec.APIKeySecretRef.Name}
		key = acc.Spec.APIKeySecretRef.Key
	default:
		var acc simplycomv1alpha1.ClusterAccount
		if err := s.Reader.Get(ctx, types.NamespacedName{Name: ref.Name}, &acc); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, accountError(simplycomv1alpha1.ReasonAccountNotFound, "ClusterAccount %s not found", ref.Name)
			}
			return nil, err
		}
		allowed, err := s.namespaceAllowed(ctx, &acc, namespace)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, accountError(simplycomv1alpha1.ReasonNotPermitted,
				"ClusterAccount %s does not allow namespace %s", ref.Name, namespace)
		}
		spec = acc.Spec.AccountSpec
		ns := acc.Spec.APIKeySecretRef.Namespace
		if ns == "" {
			ns = s.ClusterResourceNamespace
		}
		secretKey = types.NamespacedName{Namespace: ns, Name: acc.Spec.APIKeySecretRef.Name}
		key = acc.Spec.APIKeySecretRef.Key
	}

	apiKey, err := s.readSecret(ctx, secretKey, key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(apiKey))
	return &account{
		ref:        accountRefKey(ref.Kind, namespace, ref.Name),
		client:     s.NewClient(apiKey),
		zones:      spec.Zones,
		allowAdopt: spec.AllowAdopt,
		cacheKey:   hex.EncodeToString(sum[:8]),
	}, nil
}

// namespaceAllowed says whether objects in namespace may use acc. An empty namespace only has the
// selector checked, so the account's own controller reports a broken one.
func (s *SimplyAccess) namespaceAllowed(ctx context.Context, acc *simplycomv1alpha1.ClusterAccount, namespace string) (bool, error) {
	var selector labels.Selector
	if acc.Spec.NamespaceSelector != nil {
		var err error
		if selector, err = metav1.LabelSelectorAsSelector(acc.Spec.NamespaceSelector); err != nil {
			return false, accountError(simplycomv1alpha1.ReasonInvalidSpec,
				"ClusterAccount %s has an invalid namespaceSelector: %v", acc.Name, err)
		}
	}
	if namespace == "" || (selector == nil && len(acc.Spec.AllowedNamespaces) == 0) {
		return true, nil
	}
	if slices.Contains(acc.Spec.AllowedNamespaces, namespace) {
		return true, nil
	}
	if selector == nil {
		return false, nil
	}
	var ns corev1.Namespace
	if err := s.Reader.Get(ctx, types.NamespacedName{Name: namespace}, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return selector.Matches(labels.Set(ns.Labels)), nil
}

func (s *SimplyAccess) readSecret(ctx context.Context, name types.NamespacedName, key string) (string, error) {
	if key == "" {
		key = simplycomv1alpha1.DefaultAPIKeySecretKey
	}
	var secret corev1.Secret
	if err := s.SecretReader.Get(ctx, name, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", accountError(simplycomv1alpha1.ReasonSecretNotFound, "Secret %s not found", name)
		}
		return "", err
	}
	value := strings.TrimSpace(string(secret.Data[key]))
	if value == "" {
		return "", accountError(simplycomv1alpha1.ReasonSecretNotFound, "Secret %s has no key %q", name, key)
	}
	return value, nil
}

// ListZone lists zone, reusing a recent listing, and says whether it did. A listing from the cache
// may miss a change made outside the operator, so it is only fit to conclude there is nothing to do.
func (s *SimplyAccess) ListZone(ctx context.Context, acc *account, zone string) ([]simply.DNSRecord, bool, error) {
	k := acc.cacheKey + "/" + strings.ToLower(zone)
	s.mu.Lock()
	if e, ok := s.zones[k]; ok && time.Since(e.fetched) < s.ZoneCacheTTL {
		s.mu.Unlock()
		return e.records, true, nil
	}
	s.mu.Unlock()

	records, err := acc.client.ListDNSRecords(ctx, zone)
	if err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	if s.zones == nil {
		s.zones = map[string]zoneEntry{}
	}
	s.zones[k] = zoneEntry{records: records, fetched: time.Now()}
	s.mu.Unlock()
	return records, false, nil
}

// ForgetZone drops the cached listing of zone, after a write.
func (s *SimplyAccess) ForgetZone(acc *account, zone string) {
	s.mu.Lock()
	delete(s.zones, acc.cacheKey+"/"+strings.ToLower(zone))
	s.mu.Unlock()
}

// zoneKey identifies a zone of an account. It is the account's object, not its key, so a rotated
// key keeps the zone's pending reload.
func zoneKey(acc *account, zone string) string {
	return acc.ref + "/" + strings.ToLower(zone)
}

// Wrote records a write to zone: its listing is stale, and it must be reloaded to be published.
func (s *SimplyAccess) Wrote(acc *account, zone string) {
	s.mu.Lock()
	delete(s.zones, acc.cacheKey+"/"+strings.ToLower(zone))
	if s.unpublished == nil {
		s.unpublished = map[string]bool{}
	}
	s.unpublished[zoneKey(acc, zone)] = true
	s.mu.Unlock()
}

// PublishIfNeeded reloads zone if it was written to since its last successful reload, or has not
// been reloaded since the process started. A failed reload stays pending, so the next sync of any
// record in the zone tries again.
func (s *SimplyAccess) PublishIfNeeded(ctx context.Context, acc *account, zone string) error {
	// Check, reload, then clear without holding the lock: a write in between would be cleared too.
	// That cannot happen while each controller runs one reconcile at a time, as it does.
	k := zoneKey(acc, zone)
	s.mu.Lock()
	pending := s.unpublished[k] || !s.published[k]
	s.mu.Unlock()
	if !pending {
		return nil
	}
	if err := acc.client.ReloadDNSZone(ctx, zone); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.unpublished, k)
	if s.published == nil {
		s.published = map[string]bool{}
	}
	s.published[k] = true
	s.mu.Unlock()
	return nil
}
