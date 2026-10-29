/*
Copyright 2026 Peter Severin Rasmussen.

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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	simplycomv1alpha1 "github.com/PeterSR/simply-com-operator/api/v1alpha1"
	"github.com/PeterSR/simply-com-operator/internal/simply"
)

const (
	timeout  = 10 * time.Second
	interval = 100 * time.Millisecond
)

var recordCounter int

func newRecord(spec simplycomv1alpha1.DNSRecordSpec) *simplycomv1alpha1.DNSRecord {
	recordCounter++
	if spec.AccountRef.Name == "" {
		spec.AccountRef = simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "main"}
	}
	if spec.Zone == "" {
		spec.Zone = testZone
	}
	return &simplycomv1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rec-%d", recordCounter), Namespace: "default"},
		Spec:       spec,
	}
}

func readyCondition(name types.NamespacedName) func() *metav1.Condition {
	return func() *metav1.Condition {
		var obj simplycomv1alpha1.DNSRecord
		if err := k8sClient.Get(ctx, name, &obj); err != nil {
			return nil
		}
		return meta.FindStatusCondition(obj.Status.Conditions, simplycomv1alpha1.ConditionReady)
	}
}

func haveReason(status metav1.ConditionStatus, reason string) OmegaMatcher {
	return And(Not(BeNil()), WithTransform(func(c *metav1.Condition) string { return string(c.Status) + "/" + c.Reason },
		Equal(string(status)+"/"+reason)))
}

func recordsNamed(name string) func() []simply.DNSRecord {
	return func() []simply.DNSRecord {
		var out []simply.DNSRecord
		for _, r := range fake.Records(testZone) {
			if r.Name == name {
				out = append(out, r)
			}
		}
		return out
	}
}

func deleteAndWait(obj *simplycomv1alpha1.DNSRecord) {
	Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}, obj))
	}, timeout, interval).Should(BeTrue())
}

var _ = Describe("DNSRecord", Ordered, func() {
	BeforeAll(func() {
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "simply", Namespace: operatorNamespace},
			StringData: map[string]string{"apiKey": testAPIKey},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &simplycomv1alpha1.ClusterAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "main"},
			Spec: simplycomv1alpha1.ClusterAccountSpec{
				AccountSpec:     simplycomv1alpha1.AccountSpec{AllowAdopt: true},
				APIKeySecretRef: simplycomv1alpha1.ClusterSecretKeySelector{Name: "simply"},
			},
		})).To(Succeed())
	})

	It("reports the account as ready with its products", func() {
		Eventually(func() []string {
			var acc simplycomv1alpha1.ClusterAccount
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "main"}, &acc); err != nil ||
				!meta.IsStatusConditionTrue(acc.Status.Conditions, simplycomv1alpha1.ConditionReady) {
				return nil
			}
			return acc.Status.Products
		}, timeout, interval).Should(Equal([]string{testZone}))
	})

	It("creates, updates and deletes a record", func() {
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			Name: "www", Type: "A", Data: "192.0.2.1", TTL: new(int32(600)), Comment: "web",
		})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}

		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionTrue, simplycomv1alpha1.ReasonSynced))
		recs := recordsNamed("www")()
		Expect(recs).To(HaveLen(1))
		Expect(recs[0].Data).To(Equal("192.0.2.1"))
		Expect(*recs[0].Comment).To(Equal("web [simply-com-operator owner=envtest ref=default/" + obj.Name + "]"))
		Expect(fake.Reloads(testZone)).To(BeNumerically(">", 0))

		var got simplycomv1alpha1.DNSRecord
		Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
		Expect(got.Status.FQDN).To(Equal("www.example.com"))
		Expect(got.Status.RecordID).To(Equal(&recs[0].ID))

		got.Spec.Data = "192.0.2.2"
		Expect(k8sClient.Update(ctx, &got)).To(Succeed())
		Eventually(func() string {
			r := recordsNamed("www")()
			if len(r) != 1 {
				return ""
			}
			return r[0].Data
		}, timeout, interval).Should(Equal("192.0.2.2"))
		Expect(recordsNamed("www")()[0].ID).To(Equal(recs[0].ID))

		deleteAndWait(&got)
		Expect(recordsNamed("www")()).To(BeEmpty())
	})

	It("undoes a change made outside Kubernetes", func() {
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "drift", Type: "TXT", Data: "hello"})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		Eventually(recordsNamed("drift"), timeout, interval).Should(HaveLen(1))

		r := recordsNamed("drift")()[0]
		r.Data = "changed in the control panel"
		fake.Set(testZone, r)
		Eventually(func() string { return recordsNamed("drift")()[0].Data }, timeout, interval).Should(Equal("hello"))
		deleteAndWait(obj)
	})

	It("leaves an unmanaged record alone and reports a conflict", func() {
		id := fake.Seed(testZone, simply.DNSRecord{Name: "mail", Type: "A", Data: "192.0.2.5", Comment: new("made by hand")})
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "mail", Type: "A", Data: "192.0.2.5"})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonConflict))
		Consistently(recordsNamed("mail"), time.Second, interval).Should(
			ConsistOf(simply.DNSRecord{ID: id, Name: "mail", Type: "A", Data: "192.0.2.5", TTL: new(int32(3600)), Comment: new("made by hand")}))

		deleteAndWait(obj)
		Expect(recordsNamed("mail")()).To(HaveLen(1), "deleting a DNSRecord that never owned the record must not delete it")
	})

	It("does not trust a cached listing when it is about to write", func() {
		acc, err := testAccess.Resolve(ctx, "default",
			simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "main"})
		Expect(err).NotTo(HaveOccurred())
		_, _, err = testAccess.ListZone(ctx, acc, testZone)
		Expect(err).NotTo(HaveOccurred())

		// Made in the control panel after the operator last listed the zone.
		fake.Seed(testZone, simply.DNSRecord{Name: "late", Type: "TXT", Data: "by hand"})
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "late", Type: "TXT", Data: "by hand"})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonConflict))
		Expect(recordsNamed("late")()).To(HaveLen(1))
		deleteAndWait(obj)
	})

	It("adopts an exact match when asked", func() {
		id := fake.Seed(testZone, simply.DNSRecord{Name: "adopt", Type: "TXT", Data: "keep me", Comment: new("old")})
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "adopt", Type: "TXT", Data: "keep me", Adopt: true})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		Eventually(func() string {
			r := recordsNamed("adopt")()
			if len(r) != 1 || r[0].Comment == nil {
				return ""
			}
			return *r[0].Comment
		}, timeout, interval).Should(HavePrefix("[simply-com-operator owner=envtest"))
		Expect(recordsNamed("adopt")()[0].ID).To(Equal(id))
		deleteAndWait(obj)
		Expect(recordsNamed("adopt")()).To(BeEmpty())
	})

	It("refuses a CNAME next to an unmanaged record", func() {
		fake.Seed(testZone, simply.DNSRecord{Name: "clash", Type: "TXT", Data: "verification"})
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "clash", Type: "CNAME", Data: "lb.example.net"})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonConflict))
		Expect(recordsNamed("clash")()).To(HaveLen(1))
		deleteAndWait(obj)
	})

	It("keeps the record with deletionPolicy Orphan, without the marker", func() {
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			Name: "orphan", Type: "A", Data: "192.0.2.7", Comment: "keep", DeletionPolicy: simplycomv1alpha1.DeletionPolicyOrphan,
		})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		Eventually(recordsNamed("orphan"), timeout, interval).Should(HaveLen(1))
		deleteAndWait(obj)
		recs := recordsNamed("orphan")()
		Expect(recs).To(HaveLen(1))
		Expect(recs[0].Comment).To(Equal(new("keep")))

		// A later object of the same name does not take the orphan over unasked.
		again := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "orphan", Type: "A", Data: "192.0.2.7"})
		again.Name = obj.Name
		Expect(k8sClient.Create(ctx, again)).To(Succeed())
		key := types.NamespacedName{Namespace: again.Namespace, Name: again.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonConflict))
		deleteAndWait(again)
		Expect(recordsNamed("orphan")()).To(HaveLen(1))
	})

	It("lets an Orphan object go when Simply.com cannot publish the marker's removal", func() {
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			Name: "orphan-down", Type: "A", Data: "192.0.2.13", DeletionPolicy: simplycomv1alpha1.DeletionPolicyOrphan,
		})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionTrue, simplycomv1alpha1.ReasonSynced))
		fake.FailReloads(100)
		deleteAndWait(obj)
		fake.FailReloads(0)
		Expect(recordsNamed("orphan-down")()).To(HaveLen(1))
	})

	It("publishes a write whose reload failed at the next sync", func() {
		fake.FailReloads(1)
		before := fake.Reloads(testZone)
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "reload", Type: "A", Data: "192.0.2.11"})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionTrue, simplycomv1alpha1.ReasonSynced))
		Expect(fake.Reloads(testZone)).To(BeNumerically(">", before))
		Expect(recordsNamed("reload")()).To(HaveLen(1))
		deleteAndWait(obj)
	})

	It("refuses adoption when the account does not allow it", func() {
		Expect(k8sClient.Create(ctx, &simplycomv1alpha1.ClusterAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "strict"},
			Spec: simplycomv1alpha1.ClusterAccountSpec{
				APIKeySecretRef:   simplycomv1alpha1.ClusterSecretKeySelector{Name: "simply"},
				AllowedNamespaces: []string{"default"},
			},
		})).To(Succeed())
		id := fake.Seed(testZone, simply.DNSRecord{Name: "handmade", Type: "TXT", Data: "mine", Comment: new("by hand")})
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			AccountRef: simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "strict"},
			Name:       "handmade", Type: "TXT", Data: "mine", Adopt: true,
		})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonConflict))
		var got simplycomv1alpha1.DNSRecord
		Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())
		Expect(got.Status.Conditions[0].Message).To(ContainSubstring("allowAdopt"))
		deleteAndWait(obj)
		Expect(recordsNamed("handmade")()).To(ConsistOf(HaveField("ID", id)))
		Expect(*recordsNamed("handmade")()[0].Comment).To(Equal("by hand"))
	})

	It("keeps a ClusterAccount to its allowed namespaces", func() {
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "outsider"}})).To(Succeed())
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			AccountRef: simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "strict"},
			Name:       "outsider", Type: "A", Data: "192.0.2.12",
		})
		obj.Namespace = "outsider"
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonNotPermitted))
		Expect(recordsNamed("outsider")()).To(BeEmpty())
		// Delete policy: a namespace the account does not allow is let go without touching the zone.
		deleteAndWait(obj)
	})

	It("lets a ClusterAccount allow namespaces by label", func() {
		Expect(k8sClient.Create(ctx, &simplycomv1alpha1.ClusterAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "by-label"},
			Spec: simplycomv1alpha1.ClusterAccountSpec{
				APIKeySecretRef:   simplycomv1alpha1.ClusterSecretKeySelector{Name: "simply"},
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"dns": "allowed"}},
			},
		})).To(Succeed())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "labelled", Labels: map[string]string{"dns": "allowed"}}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			AccountRef: simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "by-label"},
			Name:       "labelled", Type: "A", Data: "192.0.2.14",
		})
		obj.Namespace = ns.Name
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionTrue, simplycomv1alpha1.ReasonSynced))

		// The suite resyncs every few seconds, so this does not prove the Namespace watch; the kind e2e does.
		setLabel := func(value string) {
			var current corev1.Namespace
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ns.Name}, &current)).To(Succeed())
			current.Labels["dns"] = value
			Expect(k8sClient.Update(ctx, &current)).To(Succeed())
		}
		setLabel("no")
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonNotPermitted))
		Expect(recordsNamed("labelled")()).To(HaveLen(1))

		setLabel("allowed")
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionTrue, simplycomv1alpha1.ReasonSynced))
		deleteAndWait(obj)
		Expect(recordsNamed("labelled")()).To(BeEmpty())
	})

	It("keeps allowing the listed namespaces next to a selector", func() {
		Expect(k8sClient.Create(ctx, &simplycomv1alpha1.ClusterAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "list-and-label"},
			Spec: simplycomv1alpha1.ClusterAccountSpec{
				APIKeySecretRef:   simplycomv1alpha1.ClusterSecretKeySelector{Name: "simply"},
				AllowedNamespaces: []string{"default"},
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"dns": "nowhere"}},
			},
		})).To(Succeed())
		listed := newRecord(simplycomv1alpha1.DNSRecordSpec{
			AccountRef: simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "list-and-label"},
			Name:       "listed", Type: "A", Data: "192.0.2.15",
		})
		Expect(k8sClient.Create(ctx, listed)).To(Succeed())
		Eventually(readyCondition(client.ObjectKeyFromObject(listed)), timeout, interval).
			Should(haveReason(metav1.ConditionTrue, simplycomv1alpha1.ReasonSynced))

		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "unlabelled"}})).To(Succeed())
		other := newRecord(simplycomv1alpha1.DNSRecordSpec{
			AccountRef: simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "list-and-label"},
			Name:       "unlabelled", Type: "A", Data: "192.0.2.16",
		})
		other.Namespace = "unlabelled"
		Expect(k8sClient.Create(ctx, other)).To(Succeed())
		Eventually(readyCondition(client.ObjectKeyFromObject(other)), timeout, interval).
			Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonNotPermitted))
		Expect(recordsNamed("unlabelled")()).To(BeEmpty())
		deleteAndWait(other)
		deleteAndWait(listed)
	})

	It("reports an invalid namespaceSelector on the account and the record", func() {
		Expect(k8sClient.Create(ctx, &simplycomv1alpha1.ClusterAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-selector"},
			Spec: simplycomv1alpha1.ClusterAccountSpec{
				APIKeySecretRef: simplycomv1alpha1.ClusterSecretKeySelector{Name: "simply"},
				NamespaceSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "dns", Operator: "Sometimes"},
				}},
			},
		})).To(Succeed())
		Eventually(func() *metav1.Condition {
			var acc simplycomv1alpha1.ClusterAccount
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "bad-selector"}, &acc); err != nil {
				return nil
			}
			return meta.FindStatusCondition(acc.Status.Conditions, simplycomv1alpha1.ConditionReady)
		}, timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonInvalidSpec))

		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			AccountRef: simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "bad-selector"},
			Name:       "bad-selector", Type: "A", Data: "192.0.2.17",
		})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		Eventually(readyCondition(client.ObjectKeyFromObject(obj)), timeout, interval).
			Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonInvalidSpec))
		Expect(recordsNamed("bad-selector")()).To(BeEmpty())

		// Deletion waits for the account to be fixed rather than guessing whether the namespace may.
		Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
		Consistently(func() error {
			return k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), &simplycomv1alpha1.DNSRecord{})
		}, 2*time.Second, interval).Should(Succeed())
		var acc simplycomv1alpha1.ClusterAccount
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "bad-selector"}, &acc)).To(Succeed())
		acc.Spec.NamespaceSelector = &metav1.LabelSelector{}
		Expect(k8sClient.Update(ctx, &acc)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), &simplycomv1alpha1.DNSRecord{}))
		}, timeout, interval).Should(BeTrue())
	})

	It("finds its record again when the status is lost", func() {
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "restore", Type: "A", Data: "192.0.2.8"})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		Eventually(recordsNamed("restore"), timeout, interval).Should(HaveLen(1))
		posts := fake.Calls("POST")

		// As after a backup restore: the object comes back without status.
		var current simplycomv1alpha1.DNSRecord
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}, &current)).To(Succeed())
		current.Status = simplycomv1alpha1.DNSRecordStatus{}
		Expect(k8sClient.Status().Update(ctx, &current)).To(Succeed())
		current.Spec.Comment = "touched"
		Expect(k8sClient.Update(ctx, &current)).To(Succeed())

		Eventually(func() string {
			r := recordsNamed("restore")()
			if len(r) != 1 || r[0].Comment == nil {
				return ""
			}
			return *r[0].Comment
		}, timeout, interval).Should(HavePrefix("touched "))
		Expect(recordsNamed("restore")()).To(HaveLen(1))
		Expect(fake.Calls("POST")-posts).To(BeNumerically("<=", 2), "only reloads, no second create")
		deleteAndWait(&current)
	})

	It("rejects a zone the account does not allow", func() {
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "narrow", Namespace: "default"},
			StringData: map[string]string{"token": testAPIKey},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &simplycomv1alpha1.Account{
			ObjectMeta: metav1.ObjectMeta{Name: "narrow", Namespace: "default"},
			Spec: simplycomv1alpha1.NamespacedAccountSpec{
				AccountSpec:     simplycomv1alpha1.AccountSpec{Zones: []string{"example.org"}},
				APIKeySecretRef: simplycomv1alpha1.SecretKeySelector{Name: "narrow", Key: "token"},
			},
		})).To(Succeed())
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			AccountRef: simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindNamespaced, Name: "narrow"},
			Name:       "nope", Type: "A", Data: "192.0.2.9",
		})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonZoneNotAllowed))
		Expect(recordsNamed("nope")()).To(BeEmpty())
		deleteAndWait(obj)
	})

	It("reports a missing account and catches up once it exists", func() {
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{
			AccountRef: simplycomv1alpha1.AccountReference{Kind: simplycomv1alpha1.AccountKindCluster, Name: "later"},
			Name:       "later", Type: "A", Data: "192.0.2.10",
		})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonAccountNotFound))

		Expect(k8sClient.Create(ctx, &simplycomv1alpha1.ClusterAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "later"},
			Spec:       simplycomv1alpha1.ClusterAccountSpec{APIKeySecretRef: simplycomv1alpha1.ClusterSecretKeySelector{Name: "simply"}},
		})).To(Succeed())
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionTrue, simplycomv1alpha1.ReasonSynced))
		deleteAndWait(obj)
	})

	It("rejects an invalid spec at admission", func() {
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "mx", Type: "MX", Data: "mail.example.com"})
		Expect(k8sClient.Create(ctx, obj)).NotTo(Succeed())
		obj = newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "a", Type: "A", Data: "192.0.2.1", Priority: new(int32(1))})
		Expect(k8sClient.Create(ctx, obj)).NotTo(Succeed())
	})

	It("reports an invalid spec the schema lets through", func() {
		obj := newRecord(simplycomv1alpha1.DNSRecordSpec{Name: "bad", Type: "A", Data: "not-an-ip"})
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		key := types.NamespacedName{Namespace: obj.Namespace, Name: obj.Name}
		Eventually(readyCondition(key), timeout, interval).Should(haveReason(metav1.ConditionFalse, simplycomv1alpha1.ReasonInvalidSpec))
		deleteAndWait(obj)
	})
})
