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

// ClusterSecretKeySelector names a key of a Secret in any namespace.
type ClusterSecretKeySelector struct {
	// name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace of the Secret. Defaults to the namespace the operator runs in.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// key in the Secret that holds the value. Defaults to apiKey.
	// +optional
	Key string `json:"key,omitempty"`
}

// ClusterAccountSpec is the desired state of a ClusterAccount.
type ClusterAccountSpec struct {
	AccountSpec `json:",inline"`

	// apiKeySecretRef points at the Secret holding a Simply.com API key.
	APIKeySecretRef ClusterSecretKeySelector `json:"apiKeySecretRef"`

	// allowedNamespaces are namespaces whose objects may use this account. With neither this nor
	// namespaceSelector set, every namespace may, which gives everyone allowed to create DNSRecords
	// anywhere in the cluster the power to change the account's zones.
	// +optional
	// +listType=set
	AllowedNamespaces []string `json:"allowedNamespaces,omitempty"`

	// namespaceSelector selects, by their labels, further namespaces whose objects may use this
	// account. A namespace is allowed when allowedNamespaces lists it or this selector matches it.
	// An empty selector ({}) matches every namespace.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories=simplycom
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterAccount holds the credentials for one Simply.com account, usable by objects in every namespace.
type ClusterAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterAccountSpec `json:"spec"`
	Status AccountStatus      `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterAccountList contains a list of ClusterAccount.
type ClusterAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ClusterAccount{}, &ClusterAccountList{})
		return nil
	})
}
