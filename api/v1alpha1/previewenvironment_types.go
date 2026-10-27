package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type Phase string

const (
	PhasePending   Phase = "Pending"
	PhaseDeploying Phase = "Deploying"
	PhaseReady     Phase = "Ready"
	PhaseFailed    Phase = "Failed"
	PhaseExpired   Phase = "Expired"
)

type PreviewEnvironmentSpec struct {
	// Image is a container image reference accessible from the cluster.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^\S+$`
	Image string `json:"image"`
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	Replicas *int32 `json:"replicas,omitempty"`
	// Port is used by the container, Service and TCP readiness probe.
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`
	// TTLSeconds is measured from metadata.creationTimestamp, including downtime.
	// +kubebuilder:default=3600
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=604800
	TTLSeconds int64 `json:"ttlSeconds,omitempty"`
	// Ingress exposes the application through an installed Ingress controller.
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`
}

type IngressSpec struct {
	// Host is an exact DNS hostname; wildcard hosts and IP addresses are not supported.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?(\.[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)*$`
	// +kubebuilder:validation:XValidation:rule="!isIP(self)",message="host must be a DNS name rather than an IP address"
	Host string `json:"host"`
	// ClassName explicitly selects an IngressClass installed by the cluster administrator.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?(\.[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)*$`
	ClassName string `json:"className"`
	// TLSSecretName references a TLS Secret provisioned separately in the preview Namespace.
	// The operator does not read, copy or create Secrets.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?(\.[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)*$`
	TLSSecretName string `json:"tlsSecretName,omitempty"`
}

type PreviewEnvironmentStatus struct {
	// +kubebuilder:validation:Enum=Pending;Deploying;Ready;Failed;Expired
	Phase           Phase  `json:"phase,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	ServiceEndpoint string `json:"serviceEndpoint,omitempty"`
	// URL is the configured Ingress route, not proof of DNS, TLS or external reachability.
	URL                string       `json:"url,omitempty"`
	ObservedGeneration int64        `json:"observedGeneration,omitempty"`
	ReadyReplicas      int32        `json:"readyReplicas,omitempty"`
	ExpiresAt          *metav1.Time `json:"expiresAt,omitempty"`
	Message            string       `json:"message,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=preview
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.status.namespace`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// Expires is an absolute deadline; date columns display an age and reject future times.
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=`.status.expiresAt`
type PreviewEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PreviewEnvironmentSpec   `json:"spec"`
	Status            PreviewEnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type PreviewEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PreviewEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PreviewEnvironment{}, &PreviewEnvironmentList{})
}
