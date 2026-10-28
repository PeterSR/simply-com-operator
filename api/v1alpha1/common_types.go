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
)

// Condition types and reasons shared by every kind in this group.
const (
	// ConditionReady is True when the object's desired state has been applied at Simply.com.
	ConditionReady = "Ready"

	ReasonSynced          = "Synced"
	ReasonAccountNotFound = "AccountNotFound"
	ReasonSecretNotFound  = "SecretNotFound"
	ReasonInvalidSpec     = "InvalidSpec"
	ReasonZoneNotAllowed  = "ZoneNotAllowed"
	ReasonConflict        = "Conflict"
	ReasonAPIError        = "APIError"
	ReasonNotPermitted    = "NotPermitted"
)

// DefaultAPIKeySecretKey is the Secret key read when a secret reference names none.
const DefaultAPIKeySecretKey = "apiKey"

// AccountSpec is the part of Account and ClusterAccount that is the same for both.
type AccountSpec struct {
	// zones, when set, are the only Simply.com products (domain names, such as example.com) that
	// objects using this account may change. Empty means every product the API key can reach.
	// +optional
	// +listType=set
	Zones []string `json:"zones,omitempty"`

	// allowAdopt lets objects using this account take over existing records that the operator did
	// not create (DNSRecord spec.adopt). Off by default, so that whoever may create objects cannot
	// take over, and later delete, records made by hand.
	// +optional
	AllowAdopt bool `json:"allowAdopt,omitempty"`
}

// AccountStatus is the observed state of an Account or ClusterAccount.
type AccountStatus struct {
	// conditions; Ready is True once the API key has been accepted by Simply.com.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// products are the product handles (domain names) the API key can reach, as of lastCheckedTime.
	// +optional
	// +listType=set
	Products []string `json:"products,omitempty"`

	// lastCheckedTime is when the API key was last checked against Simply.com.
	// +optional
	LastCheckedTime *metav1.Time `json:"lastCheckedTime,omitempty"`

	// observedGeneration is the generation the status was computed from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}
