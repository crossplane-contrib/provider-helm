/*
Copyright 2020 The Crossplane Authors.

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
	runtimev1alpha1 "github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"helm.sh/helm/v3/pkg/release"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A ChartSpec defines the chart spec for a Release
type ChartSpec struct {
	Repository    string                          `json:"repository"`
	Name          string                          `json:"name"`
	Version       string                          `json:"version"`
	PullSecretRef runtimev1alpha1.SecretReference `json:"pullSecretRef,omitempty"`
}

// NamespacedName represents a namespaced object name
type NamespacedName struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// DataKeySelector defines required spec to access a key of a configmap or secret
type DataKeySelector struct {
	NamespacedName `json:",inline,omitempty"`
	Key            string `json:"key,omitempty"`
	Optional       bool   `json:"optional,omitempty"`
}

// ValueFromSource represents source of a value
type ValueFromSource struct {
	ConfigMapKeyRef *DataKeySelector `json:"configMapKeyRef,omitempty"`
	SecretKeyRef    *DataKeySelector `json:"secretKeyRef,omitempty"`
}

// SetVal represents a "set" value override in a Release
type SetVal struct {
	Name      string           `json:"name"`
	Value     string           `json:"value,omitempty"`
	ValueFrom *ValueFromSource `json:"valueFrom,omitempty"`
}

// ValuesSpec defines the Helm value overrides spec for a Release
type ValuesSpec struct {
	// TODO: investigate using map[string]interface{} instead
	Values     string            `json:"values,omitempty"`
	ValuesFrom []ValueFromSource `json:"valuesFrom,omitempty"`
	Set        []SetVal          `json:"set,omitempty"`
}

// ReleaseParameters are the configurable fields of a Release.
type ReleaseParameters struct {
	Chart       ChartSpec         `json:"chart"`
	Namespace   string            `json:"namespace"`
	PatchesFrom []ValueFromSource `json:"patchesFrom,omitempty"`
	ValuesSpec  `json:",inline"`
}

// ReleaseObservation are the observable fields of a Release.
type ReleaseObservation struct {
	State              release.Status `json:"state,omitempty"`
	ReleaseDescription string         `json:"releaseDescription,omitempty"`
	Revision           int            `json:"revision,omitempty"`
}

// A ReleaseSpec defines the desired state of a Release.
type ReleaseSpec struct {
	runtimev1alpha1.ResourceSpec `json:",inline"`
	ForProvider                  ReleaseParameters `json:"forProvider"`
	// RollbackRetriesLimit is max number of attempts to retry Helm deployment by rolling back the release.
	RollbackRetriesLimit *int32 `json:"rollbackLimit,omitempty"`
}

// A ReleaseStatus represents the observed state of a Release.
type ReleaseStatus struct {
	runtimev1alpha1.ResourceStatus `json:",inline"`
	AtProvider                     ReleaseObservation `json:"atProvider,omitempty"`
	PatchesSha                     string             `json:"patchesSha,omitempty"`
	Failed                         int32              `json:"failed,omitempty"`
	Synced                         bool               `json:"synced,omitempty"`
}

// +kubebuilder:object:root=true

// A Release is an example API type
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="STATE",type="string",JSONPath=".status.atProvider.state"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster
type Release struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReleaseSpec   `json:"spec"`
	Status ReleaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReleaseList contains a list of Release
type ReleaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Release `json:"items"`
}
