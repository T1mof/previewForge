package admission

// The independent webhook cannot mutate the operator or its policies.
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get
// +kubebuilder:rbac:groups=platform.previewforge.dev,resources=previewenvironments,verbs=get
