/*
Copyright 2025.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WebAppSpec defines the desired state of WebApp
type WebAppSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName,omitempty"`

	// ADDED: The UUID from the build request
	ContainerID string `json:"containerID,omitempty"`

	RepoURL     string `json:"repoURL,omitempty"`
	Branch      string `json:"branch,omitempty"`
	
	// +kubebuilder:validation:Required
	Image string `json:"image,omitempty"`
	
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=8080
	Port int `json:"port,omitempty"`
	
	// +optional
	EnvVariables map[string]string `json:"envVariables,omitempty"`
}

// WebAppStatus defines the observed state of WebApp.
type WebAppStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// WebApp is the Schema for the webapps API
type WebApp struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec WebAppSpec `json:"spec"`

	// +optional
	Status WebAppStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// WebAppList contains a list of WebApp
type WebAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []WebApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebApp{}, &WebAppList{})
}