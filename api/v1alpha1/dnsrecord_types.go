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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AccountKind is the kind of account an object uses.
// +kubebuilder:validation:Enum=Account;ClusterAccount
type AccountKind string

const (
	AccountKindNamespaced AccountKind = "Account"
	AccountKindCluster    AccountKind = "ClusterAccount"
)

// AccountReference points at the Account or ClusterAccount whose credentials an object uses.
type AccountReference struct {
	// kind is Account (in the object's own namespace) or ClusterAccount. Defaults to ClusterAccount.
	// +kubebuilder:default=ClusterAccount
	// +optional
	Kind AccountKind `json:"kind,omitempty"`

	// name of the Account or ClusterAccount.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// DNSRecordType is a DNS record type that Simply.com accepts.
// +kubebuilder:validation:Enum=A;AAAA;ALIAS;CAA;CNAME;HTTPS;LOC;MX;NS;SRV;SSHFP;TLSA;TXT
type DNSRecordType string

// DeletionPolicy says what happens at Simply.com when the Kubernetes object is deleted.
// +kubebuilder:validation:Enum=Delete;Orphan
type DeletionPolicy string

const (
	// DeletionPolicyDelete removes the record from the zone.
	DeletionPolicyDelete DeletionPolicy = "Delete"
	// DeletionPolicyOrphan leaves the record in the zone and takes the ownership marker off it.
	DeletionPolicyOrphan DeletionPolicy = "Orphan"
)

// DNSRecordSpec is the desired state of one record in a Simply.com DNS zone.
// +kubebuilder:validation:XValidation:rule="self.type != 'MX' || has(self.priority)",message="priority is required for MX records"
// +kubebuilder:validation:XValidation:rule="self.type != 'SRV' || has(self.priority)",message="priority is required for SRV records"
// +kubebuilder:validation:XValidation:rule="!has(self.priority) || self.type in ['MX', 'SRV']",message="priority is only for MX and SRV records"
type DNSRecordSpec struct {
	// accountRef names the account whose API key is used.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="accountRef is immutable"
	AccountRef AccountReference `json:"accountRef"`

	// zone is the Simply.com product holding the DNS zone, which is the domain name, such as example.com.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="zone is immutable"
	Zone string `json:"zone"`

	// name of the record relative to the zone: "www", "_dmarc", "@" for the zone apex, "*" for a
	// wildcard. A fully qualified name inside the zone, such as www.example.com, is accepted too.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// type of the record.
	Type DNSRecordType `json:"type"`

	// data is the record's value as Simply.com stores it: an address for A and AAAA, a host name
	// for CNAME, MX and NS (no trailing dot needed), the text for TXT (without quotes), and
	// "weight port target" for SRV.
	// +kubebuilder:validation:MinLength=1
	Data string `json:"data"`

	// ttl in seconds. Left out, the zone's default applies and any TTL set at Simply.com is kept.
	// +kubebuilder:validation:Minimum=60
	// +optional
	TTL *int32 `json:"ttl,omitempty"`

	// priority for MX and SRV records.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Priority *int32 `json:"priority,omitempty"`

	// comment shown next to the record in the Simply.com control panel. The operator appends its
	// ownership marker to it.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Comment string `json:"comment,omitempty"`

	// deletionPolicy says whether deleting this object removes the record (Delete, the default) or
	// leaves it in the zone (Orphan). An orphaned record loses the ownership marker, so no later
	// object takes it over unless it adopts it.
	// +kubebuilder:default=Delete
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// adopt lets the operator take over an existing record that is not marked as managed but has
	// exactly this name, type and data. Without it such a record is reported as a conflict and left
	// alone. The account must have allowAdopt. The operator never changes or deletes a record it
	// does not manage.
	// +optional
	Adopt bool `json:"adopt,omitempty"`
}

// DNSRecordStatus is the observed state of a DNSRecord.
type DNSRecordStatus struct {
	// conditions; Ready is True when the record at Simply.com matches the spec.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// recordID is Simply.com's id of the managed record.
	// +optional
	RecordID *int64 `json:"recordID,omitempty"`

	// fqdn is the fully qualified name of the record.
	// +optional
	FQDN string `json:"fqdn,omitempty"`

	// observedGeneration is the generation the status was computed from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// lastSyncedTime is when the record was last confirmed to match the spec.
	// +optional
	LastSyncedTime *metav1.Time `json:"lastSyncedTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sdns,categories=simplycom
// +kubebuilder:printcolumn:name="FQDN",type=string,JSONPath=`.status.fqdn`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Data",type=string,JSONPath=`.spec.data`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DNSRecord is one record in a DNS zone hosted at Simply.com.
type DNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DNSRecordSpec   `json:"spec"`
	Status DNSRecordStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DNSRecordList contains a list of DNSRecord.
type DNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DNSRecord{}, &DNSRecordList{})
		return nil
	})
}
