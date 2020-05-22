/*
Copyright 2019 Suraj Banakar.

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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// EnvironmentSpec defines the desired state of Environment
type EnvironmentSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Required
	// Source are parameters to define the main application
	Source AppSrc `json:"source"`

	// Dependencies are the dependencies required for the main application
	Dependencies []DependencySrc `json:"dependencies,omitempty"`

	// ClusterClassLabel is used to select the crossplane cluster class for provisioning the cluster
	ClusterClassLabel string `json:"clusterClassLabel,omitempty"`

	// ClusterName is the name of the cluster to provision in the cloud provider
	ClusterName string `json:"clusterName,omitempty"`

	// TTL (Time to Live) is the time duration for which the cluster should live.
	// Once the TTL is exceeded, the cluster is automatically deleted.
	// Optional parameter with no default value.
	// +kubebuilder:validation:Pattern=^(([0-9]+)m|([0-9]+)h|([0-9]+)d|([0-9]+)y)$
	TTL string `json:"ttl,omitempty"`
}

// AppSrc defines fields related to the source repository/location of the application
// AppSrc overlaps with DependencySrc but they're kept as two different structs
// to accomodate validation (e.g., path is required in app but not in dependencies)
// AppSrc and DependencySrc might get merged in the future
type AppSrc struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	Namespace string `json:"namespace,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Revision string `json:"revision"`

	// +kubebuilder:validation:MinLength=1
	ChartName string `json:"chartName,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	RepoURL string `json:"repoURL"`
}

// DependencySrc defines fields related to the source repository/location of the application
// DependencySrc overlaps with AppSrc but they're kept as two different structs (check AppSrc for more info)
type DependencySrc struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	Namespace string `json:"namespace,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Revision string `json:"revision"`

	// +kubebuilder:validation:MinLength=1
	ChartName string `json:"chartName,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	RepoURL string `json:"repoURL"`
}

// EnvironmentStatus defines the observed state of Environment
type EnvironmentStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	ClusterStatus     metav1.Status `json:"clusterStatus,omitempty"`
	ApplicationStatus metav1.Status `json:"applicationStatus,omitempty"`
	DependencyStatus  metav1.Status `json:"dependencyStatus,omitempty"`

	Ready             bool         `json:"ready,omitempty"`
	TTLStartTimestamp *metav1.Time `json:"ttlStartTimestamp,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=`.status.ready`
// Environment is the Schema for the environments API
type Environment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EnvironmentSpec   `json:"spec"`
	Status EnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EnvironmentList contains a list of Environment
type EnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Environment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Environment{}, &EnvironmentList{})
}
