package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var GroupVersion = schema.GroupVersion{Group: "deploypilot.io", Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &DeploymentRequest{}, &DeploymentRequestList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
})
var AddToScheme = SchemeBuilder.AddToScheme

// Keep these aliases referenced here to make schema evolution imports explicit.
var _ = resource.Quantity{}
var _ = serializer.CodecFactory{}
var _ = intstr.IntOrString{}
var _ types.UID

type ProbeSpec struct {
	Path                string `json:"path"`
	InitialDelaySeconds int32  `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int32  `json:"periodSeconds,omitempty"`
}
type ResourceSpec struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}
type EnvVar struct {
	Name       string `json:"name"`
	Value      string `json:"value,omitempty"`
	SecretName string `json:"secretName,omitempty"`
	SecretKey  string `json:"secretKey,omitempty"`
}
type DeploymentRequestSpec struct {
	Image          string       `json:"image"`
	Replicas       *int32       `json:"replicas,omitempty"`
	Port           int32        `json:"port,omitempty"`
	Resources      ResourceSpec `json:"resources,omitempty"`
	ReadinessProbe *ProbeSpec   `json:"readinessProbe,omitempty"`
	LivenessProbe  *ProbeSpec   `json:"livenessProbe,omitempty"`
	Env            []EnvVar     `json:"env,omitempty"`
}
type DeploymentCondition struct {
	Type               string                 `json:"type"`
	Status             metav1.ConditionStatus `json:"status"`
	Reason             string                 `json:"reason,omitempty"`
	Message            string                 `json:"message,omitempty"`
	LastTransitionTime metav1.Time            `json:"lastTransitionTime,omitempty"`
}
type DeploymentRequestStatus struct {
	ObservedGeneration int64                 `json:"observedGeneration,omitempty"`
	ReadyReplicas      int32                 `json:"readyReplicas,omitempty"`
	DesiredReplicas    int32                 `json:"desiredReplicas,omitempty"`
	Conditions         []DeploymentCondition `json:"conditions,omitempty"`
	DeploymentName     string                `json:"deploymentName,omitempty"`
	ServiceName        string                `json:"serviceName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.image"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type DeploymentRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DeploymentRequestSpec   `json:"spec"`
	Status            DeploymentRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type DeploymentRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeploymentRequest `json:"items"`
}

func (in *DeploymentRequest) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.Replicas != nil {
		v := *in.Spec.Replicas
		out.Spec.Replicas = &v
	}
	out.Spec.Env = append([]EnvVar(nil), in.Spec.Env...)
	out.Spec.Resources.Requests = copyMap(in.Spec.Resources.Requests)
	out.Spec.Resources.Limits = copyMap(in.Spec.Resources.Limits)
	if in.Spec.ReadinessProbe != nil {
		v := *in.Spec.ReadinessProbe
		out.Spec.ReadinessProbe = &v
	}
	if in.Spec.LivenessProbe != nil {
		v := *in.Spec.LivenessProbe
		out.Spec.LivenessProbe = &v
	}
	out.Status.Conditions = append([]DeploymentCondition(nil), in.Status.Conditions...)
	return &out
}
func (in *DeploymentRequestList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := *in
	out.Items = make([]DeploymentRequest, len(in.Items))
	for i := range in.Items {
		out.Items[i] = *in.Items[i].DeepCopyObject().(*DeploymentRequest)
	}
	return &out
}
func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
