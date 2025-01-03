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

package v1beta1

import (
	"helm.sh/helm/v3/pkg/release"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// A ChartSpec defines the chart spec for a Release
type ChartSpec struct {
	// Repository: Helm repository URL, required if ChartSpec.URL not set
	Repository string `json:"repository,omitempty"`
	// Name of Helm chart, required if ChartSpec.URL not set
	Name string `json:"name,omitempty"`
	// Version of Helm chart, late initialized with latest version if not set
	Version string `json:"version,omitempty"`
	// URL to chart package (typically .tgz), optional and overrides others fields in the spec
	URL string `json:"url,omitempty"`
	// PullSecretRef is reference to the secret containing credentials to helm repository
	PullSecretRef xpv1.SecretReference `json:"pullSecretRef,omitempty"`
}

// NamespacedName represents a namespaced object name
type NamespacedName struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// DataKeySelector defines required spec to access a key of a configmap or secret
type DataKeySelector struct {
	NamespacedName `json:",inline"`
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
	// +kubebuilder:pruning:PreserveUnknownFields
	Values     runtime.RawExtension `json:"values,omitempty"`
	ValuesFrom []ValueFromSource    `json:"valuesFrom,omitempty"`
	Set        []SetVal             `json:"set,omitempty"`
}

// ReleaseParameters are the configurable fields of a Release.
type ReleaseParameters struct {
	Chart ChartSpec `json:"chart"`
	// Namespace to install the release into.
	Namespace string `json:"namespace"`
	// SkipCreateNamespace won't create the namespace for the release. This requires the namespace to already exist.
	SkipCreateNamespace bool `json:"skipCreateNamespace,omitempty"`
	// Wait for the release to become ready.
	Wait bool `json:"wait,omitempty"`
	// WaitTimeout is the duration Helm will wait for the release to become
	// ready. Only applies if wait is also set. Defaults to 5m.
	WaitTimeout *metav1.Duration `json:"waitTimeout,omitempty"`
	// PatchesFrom describe patches to be applied to the rendered manifests.
	PatchesFrom []ValueFromSource `json:"patchesFrom,omitempty"`
	// ValuesSpec defines the Helm value overrides spec for a Release.
	ValuesSpec `json:",inline"`
	// SkipCRDs skips installation of CRDs for the release.
	SkipCRDs bool `json:"skipCRDs,omitempty"`
	// InsecureSkipTLSVerify skips tls certificate checks for the chart download
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`
}

// ReleaseObservation are the observable fields of a Release.
type ReleaseObservation struct {
	State              release.Status `json:"state,omitempty"`
	ReleaseDescription string         `json:"releaseDescription,omitempty"`
	Revision           int            `json:"revision,omitempty"`
}

// A ReleaseSpec defines the desired state of a Release.
type ReleaseSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ConnectionDetails []ConnectionDetail `json:"connectionDetails,omitempty"`
	ForProvider       ReleaseParameters  `json:"forProvider"`
	// RollbackRetriesLimit is max number of attempts to retry Helm deployment by rolling back the release.
	RollbackRetriesLimit *int32 `json:"rollbackLimit,omitempty"`
}

// A ReleaseStatus represents the observed state of a Release.
type ReleaseStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          ReleaseObservation `json:"atProvider,omitempty"`
	PatchesSha          string             `json:"patchesSha,omitempty"`
	Failed              int32              `json:"failed,omitempty"`
	Synced              bool               `json:"synced,omitempty"`
}

// ConnectionDetail todo
type ConnectionDetail struct {
	v1.ObjectReference    `json:",inline"`
	ToConnectionSecretKey string `json:"toConnectionSecretKey,omitempty"`
	// SkipPartOfReleaseCheck skips check for meta.helm.sh/release-name annotation.
	SkipPartOfReleaseCheck bool `json:"skipPartOfReleaseCheck,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion

// A Release is an example API type
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="CHART",type="string",JSONPath=".spec.forProvider.chart.name"
// +kubebuilder:printcolumn:name="VERSION",type="string",JSONPath=".spec.forProvider.chart.version"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="STATE",type="string",JSONPath=".status.atProvider.state"
// +kubebuilder:printcolumn:name="REVISION",type="string",JSONPath=".status.atProvider.revision"
// +kubebuilder:printcolumn:name="DESCRIPTION",type="string",JSONPath=".status.atProvider.releaseDescription"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,helm}
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
