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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// SecretKeySelector names a key of a Secret in the same namespace as the object referring to it.
type SecretKeySelector struct {
	// name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key in the Secret that holds the value. Defaults to apiKey.
	// +optional
	Key string `json:"key,omitempty"`
}

// NamespacedAccountSpec is the desired state of an Account.
type NamespacedAccountSpec struct {
	AccountSpec `json:",inline"`

	// apiKeySecretRef points at the Secret, in the Account's namespace, holding a Simply.com API key.
	// Create one under Account, API keys in the Simply.com control panel.
	APIKeySecretRef SecretKeySelector `json:"apiKeySecretRef"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=simplycom
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Account holds the credentials for one Simply.com account, usable by objects in its own namespace.
type Account struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NamespacedAccountSpec `json:"spec"`
	Status AccountStatus         `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AccountList contains a list of Account.
type AccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Account `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Account{}, &AccountList{})
		return nil
	})
}
