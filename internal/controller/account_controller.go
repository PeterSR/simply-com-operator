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
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	simplycomv1alpha1 "github.com/PeterSR/simply-com-operator/api/v1alpha1"
	"github.com/PeterSR/simply-com-operator/internal/simply"
)

// AccountReconciler checks the API key of each Account and ClusterAccount against Simply.com and
// reports what it can reach.
type AccountReconciler struct {
	client.Client
	Access *SimplyAccess
	// CheckInterval is how often a working API key is checked again.
	CheckInterval time.Duration
	// RetryInterval is how long to wait after a failed check.
	RetryInterval time.Duration
}

// +kubebuilder:rbac:groups=simplycom.petersr.github.io,resources=accounts;clusteraccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=simplycom.petersr.github.io,resources=accounts/status;clusteraccounts/status,verbs=get;update;patch

// Reconcile handles both kinds: a request with a namespace is an Account, one without a ClusterAccount.
func (r *AccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Namespace != "" {
		var acc simplycomv1alpha1.Account
		if err := r.Get(ctx, req.NamespacedName, &acc); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		ref := simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindNamespaced, Name: acc.Name}
		retry := r.check(ctx, acc.Namespace, ref, acc.Generation, &acc.Status)
		return ctrl.Result{RequeueAfter: retry}, r.Status().Update(ctx, &acc)
	}
	var acc simplycomv1alpha1.ClusterAccount
	if err := r.Get(ctx, req.NamespacedName, &acc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ref := simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: acc.Name}
	retry := r.check(ctx, "", ref, acc.Generation, &acc.Status)
	return ctrl.Result{RequeueAfter: retry}, r.Status().Update(ctx, &acc)
}

func (r *AccountReconciler) check(ctx context.Context, namespace string, ref simplycomv1alpha1.AccountReference,
	generation int64, status *simplycomv1alpha1.AccountStatus) time.Duration {
	now := metav1.Now()
	status.LastCheckedTime = &now
	status.ObservedGeneration = generation
	set := func(s metav1.ConditionStatus, reason, msg string) {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: simplycomv1alpha1.ConditionReady, Status: s, Reason: reason, Message: msg, ObservedGeneration: generation,
		})
	}

	acc, err := r.Access.Resolve(ctx, namespace, ref)
	if err != nil {
		set(metav1.ConditionFalse, reasonFor(err), err.Error())
		return r.RetryInterval
	}
	products, err := acc.client.ListProducts(ctx)
	if err != nil {
		reason := simplycomv1alpha1.ReasonAPIError
		if simply.IsUnauthorized(err) {
			reason = "Unauthorized"
		}
		set(metav1.ConditionFalse, reason, err.Error())
		return r.RetryInterval
	}
	names := make([]string, 0, len(products))
	for _, p := range products {
		names = append(names, p.Object)
	}
	slices.Sort(names)
	status.Products = names
	set(metav1.ConditionTrue, "Authenticated", "Simply.com accepted the API key")
	return r.CheckInterval
}

// SetupWithManager sets up one controller for Accounts and ClusterAccounts. Secrets are not
// watched, to keep them out of the cache; a changed key shows in the status at the next check.
func (r *AccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&simplycomv1alpha1.Account{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&simplycomv1alpha1.ClusterAccount{}, &handler.EnqueueRequestForObject{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("account").
		Complete(r)
}

// readyChanged passes an account event when its spec or its Ready condition changed, which is when
// the DNSRecords using it should be looked at again.
type readyChanged struct {
	predicate.Funcs
}

func (readyChanged) Update(e event.UpdateEvent) bool {
	if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
		return true
	}
	return readyStatus(e.ObjectOld) != readyStatus(e.ObjectNew)
}

func readyStatus(o client.Object) metav1.ConditionStatus {
	var conds []metav1.Condition
	switch a := o.(type) {
	case *simplycomv1alpha1.Account:
		conds = a.Status.Conditions
	case *simplycomv1alpha1.ClusterAccount:
		conds = a.Status.Conditions
	}
	if c := meta.FindStatusCondition(conds, simplycomv1alpha1.ConditionReady); c != nil {
		return c.Status
	}
	return metav1.ConditionUnknown
}
