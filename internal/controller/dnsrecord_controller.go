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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/recorder"

	simplycomv1alpha1 "github.com/PeterSR/simply-com-operator/api/v1alpha1"
	"github.com/PeterSR/simply-com-operator/internal/simply"
)

// Finalizer keeps a DNSRecord until its record has been removed from the zone.
const Finalizer = "simplycom.petersr.github.io/finalizer"

const accountRefIndex = "spec.accountRef"

// DNSRecordReconciler keeps one record in a Simply.com zone in line with each DNSRecord.
type DNSRecordReconciler struct {
	client.Client
	Access   *SimplyAccess
	Recorder recorder.EventRecorder
	// OwnerID tells this operator's records apart from those of another installation sharing a zone.
	OwnerID string
	// ResyncInterval is how often a record in sync is checked again, to undo changes made outside
	// Kubernetes.
	ResyncInterval time.Duration
	// RetryInterval is how long to wait after a problem only the user or Simply.com can fix.
	RetryInterval time.Duration
}

// +kubebuilder:rbac:groups=simplycom.petersr.github.io,resources=dnsrecords,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=simplycom.petersr.github.io,resources=dnsrecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=simplycom.petersr.github.io,resources=dnsrecords/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var obj simplycomv1alpha1.DNSRecord
	if err := r.Get(ctx, req.NamespacedName, &obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	ref := obj.Namespace + "/" + obj.Name

	if !obj.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &obj, ref)
	}
	if controllerutil.AddFinalizer(&obj, Finalizer) {
		if err := r.Update(ctx, &obj); err != nil {
			return ctrl.Result{}, err
		}
	}

	zone := obj.Spec.Zone
	obj.Status.FQDN = fqdn(zone, relativeName(zone, obj.Spec.Name))

	if err := validateSpec(&obj.Spec); err != nil {
		return r.notReady(ctx, &obj, simplycomv1alpha1.ReasonInvalidSpec, err.Error(), 0)
	}
	acc, err := r.Access.Resolve(ctx, obj.Namespace, obj.Spec.AccountRef)
	if err != nil {
		return r.notReady(ctx, &obj, reasonFor(err), err.Error(), r.RetryInterval)
	}
	if !acc.allowsZone(zone) {
		return r.notReady(ctx, &obj, simplycomv1alpha1.ReasonZoneNotAllowed,
			fmt.Sprintf("the account does not allow zone %q", zone), 0)
	}

	records, cached, err := r.Access.ListZone(ctx, acc, zone)
	if err != nil {
		return r.apiError(ctx, &obj, "listing the zone", err)
	}
	want := desiredRecord(&obj.Spec, r.OwnerID, ref)
	p := planRecord(records, want, r.OwnerID, ref, obj.Status.RecordID, obj.Spec.Adopt)
	if cached && (p.action != actionNone || len(p.duplicates) > 0) {
		// Never write on the strength of a cached listing: a record made in the control panel since
		// would be missed, and the operator would create a duplicate of it.
		r.Access.ForgetZone(acc, zone)
		if records, _, err = r.Access.ListZone(ctx, acc, zone); err != nil {
			return r.apiError(ctx, &obj, "listing the zone", err)
		}
		p = planRecord(records, want, r.OwnerID, ref, obj.Status.RecordID, obj.Spec.Adopt)
	}

	if p.action == actionConflict {
		r.Recorder.Eventf(&obj, nil, corev1.EventTypeWarning, simplycomv1alpha1.ReasonConflict, "Sync", "%s", p.conflict)
		return r.notReady(ctx, &obj, simplycomv1alpha1.ReasonConflict, p.conflict, r.RetryInterval)
	}

	wrote := false
	// Duplicates go first: an update to a CNAME is refused while another record has its name.
	for _, id := range p.duplicates {
		if err := acc.client.DeleteDNSRecord(ctx, zone, id); err != nil && !simply.IsNotFound(err) {
			return r.apiError(ctx, &obj, "deleting a duplicate record", err)
		}
		wrote = true
		log.Info("deleted duplicate record", "recordID", id)
	}

	recordID := p.recordID
	switch p.action {
	case actionCreate:
		recordID, err = acc.client.CreateDNSRecord(ctx, zone, want)
		if err != nil {
			return r.apiError(ctx, &obj, "creating the record", err)
		}
		wrote = true
		log.Info("created record", "recordID", recordID, "fqdn", obj.Status.FQDN, "type", want.Type)
		r.Recorder.Eventf(&obj, nil, corev1.EventTypeNormal, "Created", "Sync", "created %s record %d", want.Type, recordID)
	case actionUpdate:
		current := findRecord(records, recordID)
		if err := acc.client.UpdateDNSRecord(ctx, zone, recordID, updatePayload(current, want)); err != nil {
			return r.apiError(ctx, &obj, "updating the record", err)
		}
		wrote = true
		verb := "updated"
		if p.adopting {
			verb = "adopted"
		}
		log.Info(verb+" record", "recordID", recordID, "fqdn", obj.Status.FQDN, "type", want.Type)
		r.Recorder.Eventf(&obj, nil, corev1.EventTypeNormal, "Updated", "Sync", "%s %s record %d", verb, want.Type, recordID)
	}

	if wrote {
		r.Access.ForgetZone(acc, zone)
		if err := acc.client.ReloadDNSZone(ctx, zone); err != nil {
			// The record is stored; the next sync reloads again. Record what was done meanwhile.
			obj.Status.RecordID = &recordID
			return r.apiError(ctx, &obj, "reloading the zone", err)
		}
	}

	now := metav1.Now()
	obj.Status.RecordID = &recordID
	obj.Status.LastSyncedTime = &now
	r.setReady(&obj, metav1.ConditionTrue, simplycomv1alpha1.ReasonSynced, "the record matches the spec")
	if err := r.Status().Update(ctx, &obj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.ResyncInterval}, nil
}

func findRecord(records []simply.DNSRecord, id int64) simply.DNSRecord {
	for _, rec := range records {
		if rec.ID == id {
			return rec
		}
	}
	return simply.DNSRecord{}
}

func (r *DNSRecordReconciler) finalize(ctx context.Context, obj *simplycomv1alpha1.DNSRecord, ref string) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(obj, Finalizer) {
		return ctrl.Result{}, nil
	}
	if obj.Spec.DeletionPolicy != simplycomv1alpha1.DeletionPolicyOrphan {
		acc, err := r.Access.Resolve(ctx, obj.Namespace, obj.Spec.AccountRef)
		if err != nil {
			msg := err.Error() + "; the record cannot be removed until the account is back, " +
				"or set spec.deletionPolicy to Orphan to leave it in the zone"
			return r.notReady(ctx, obj, reasonFor(err), msg, r.RetryInterval)
		}
		zone := obj.Spec.Zone
		if !acc.allowsZone(zone) {
			// The account no longer lets the operator touch this zone, so not even to clean up.
			logf.FromContext(ctx).Info("zone not allowed by the account; leaving any record in place", "zone", zone)
			controllerutil.RemoveFinalizer(obj, Finalizer)
			return ctrl.Result{}, r.Update(ctx, obj)
		}
		r.Access.ForgetZone(acc, zone)
		records, _, err := r.Access.ListZone(ctx, acc, zone)
		if err != nil && !simply.IsNotFound(err) {
			return r.apiError(ctx, obj, "listing the zone", err)
		}
		deleted := 0
		for _, rec := range records {
			if o, rf, ok := parseMarker(rec.Comment); ok && o == r.OwnerID && rf == ref {
				if err := acc.client.DeleteDNSRecord(ctx, zone, rec.ID); err != nil && !simply.IsNotFound(err) {
					return r.apiError(ctx, obj, "deleting the record", err)
				}
				deleted++
			}
		}
		if deleted > 0 {
			r.Access.ForgetZone(acc, zone)
			if err := acc.client.ReloadDNSZone(ctx, zone); err != nil {
				return r.apiError(ctx, obj, "reloading the zone", err)
			}
			logf.FromContext(ctx).Info("deleted record", "fqdn", obj.Status.FQDN, "count", deleted)
		}
	}
	controllerutil.RemoveFinalizer(obj, Finalizer)
	return ctrl.Result{}, r.Update(ctx, obj)
}

func (r *DNSRecordReconciler) setReady(obj *simplycomv1alpha1.DNSRecord, status metav1.ConditionStatus, reason, msg string) {
	obj.Status.ObservedGeneration = obj.Generation
	meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
		Type: simplycomv1alpha1.ConditionReady, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: obj.Generation,
	})
}

// notReady records a problem and waits retry before trying again; with retry 0 it waits for a
// change to the object or its account instead.
func (r *DNSRecordReconciler) notReady(ctx context.Context, obj *simplycomv1alpha1.DNSRecord, reason, msg string, retry time.Duration) (ctrl.Result, error) {
	r.setReady(obj, metav1.ConditionFalse, reason, msg)
	if err := r.Status().Update(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: retry}, nil
}

// apiError reports a failed Simply.com call. A refused request waits for the retry interval, since
// sending it again unchanged fails again; anything else is returned for the usual backoff.
func (r *DNSRecordReconciler) apiError(ctx context.Context, obj *simplycomv1alpha1.DNSRecord, doing string, err error) (ctrl.Result, error) {
	msg := doing + ": " + err.Error()
	r.Recorder.Eventf(obj, nil, corev1.EventTypeWarning, simplycomv1alpha1.ReasonAPIError, "Sync", "%s", msg)
	if simply.IsClientError(err) {
		return r.notReady(ctx, obj, simplycomv1alpha1.ReasonAPIError, msg, r.RetryInterval)
	}
	r.setReady(obj, metav1.ConditionFalse, simplycomv1alpha1.ReasonAPIError, msg)
	if uerr := r.Status().Update(ctx, obj); uerr != nil && !apierrors.IsNotFound(uerr) {
		logf.FromContext(ctx).Error(uerr, "updating status")
	}
	return ctrl.Result{}, fmt.Errorf("%s: %w", doing, err)
}

func accountRefKey(kind simplycomv1alpha1.AccountKind, namespace, name string) string {
	if kind == simplycomv1alpha1.AccountKindNamespaced {
		return "Account/" + namespace + "/" + name
	}
	return "ClusterAccount/" + name
}

// SetupWithManager sets up the controller with the Manager.
func (r *DNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &simplycomv1alpha1.DNSRecord{}, accountRefIndex,
		func(o client.Object) []string {
			rec := o.(*simplycomv1alpha1.DNSRecord)
			return []string{accountRefKey(rec.Spec.AccountRef.Kind, rec.Namespace, rec.Spec.AccountRef.Name)}
		}); err != nil {
		return err
	}
	recordsUsing := func(key func(o client.Object) string) handler.EventHandler {
		return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
			var list simplycomv1alpha1.DNSRecordList
			if err := r.List(ctx, &list, client.MatchingFields{accountRefIndex: key(o)}); err != nil {
				return nil
			}
			reqs := make([]reconcile.Request, 0, len(list.Items))
			for _, item := range list.Items {
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: item.Namespace, Name: item.Name}})
			}
			return reqs
		})
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&simplycomv1alpha1.DNSRecord{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&simplycomv1alpha1.Account{}, recordsUsing(func(o client.Object) string {
			return accountRefKey(simplycomv1alpha1.AccountKindNamespaced, o.GetNamespace(), o.GetName())
		}), builder.WithPredicates(readyChanged{})).
		Watches(&simplycomv1alpha1.ClusterAccount{}, recordsUsing(func(o client.Object) string {
			return accountRefKey(simplycomv1alpha1.AccountKindCluster, "", o.GetName())
		}), builder.WithPredicates(readyChanged{})).
		Named("dnsrecord").
		Complete(r)
}
