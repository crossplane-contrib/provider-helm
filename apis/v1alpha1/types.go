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
	Repository string `json:"repository"`
	Name       string `json:"name"`
	Version    string `json:"version"`
}

// HelmValues represent inline value overrides in the CR.
// This type definition is a workaround to https://github.com/kubernetes-sigs/kubebuilder/issues/528
//type HelmValues json.RawMessage

// ReleaseParameters are the configurable fields of a Release.
type ReleaseParameters struct {
	Chart     ChartSpec `json:"chart"`
	Namespace string    `json:"namespace"`
	Values    string    `json:"values,omitempty"`
	// Set
}

// ReleaseObservation are the observable fields of a Release.
type ReleaseObservation struct {
	Status             release.Status `json:"status,omitempty"`
	ReleaseDescription string         `json:"releaseDescription,omitempty"`
}

// A ReleaseSpec defines the desired state of a Release.
type ReleaseSpec struct {
	runtimev1alpha1.ResourceSpec `json:",inline"`
	ForProvider                  ReleaseParameters `json:"forProvider"`
}

// A ReleaseStatus represents the observed state of a Release.
type ReleaseStatus struct {
	runtimev1alpha1.ResourceStatus `json:",inline"`
	AtProvider                     ReleaseObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Release is an example API type
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="STATUS",type="string",JSONPath=".status.bindingPhase"
// +kubebuilder:printcolumn:name="STATE",type="string",JSONPath=".status.atProvider.state"
// +kubebuilder:printcolumn:name="CLASS",type="string",JSONPath=".spec.classRef.name"
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
