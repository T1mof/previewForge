package controller

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

const (
	Finalizer      = "platform.previewforge.dev/cleanup"
	ManagedByLabel = "app.kubernetes.io/managed-by"
	OwnerUIDLabel  = "platform.previewforge.dev/owner-uid"
	AppName        = "app"
)

// NamespaceName is stable across restarts and changes when a CR is recreated.
func NamespaceName(p *platformv1.PreviewEnvironment) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, p.Name)
	if len(name) > 40 {
		name = name[:40]
	}
	name = strings.Trim(name, "-")
	if name == "" {
		name = "preview"
	}
	hash := sha256.Sum256([]byte(p.UID))
	return fmt.Sprintf("forge-%s-%x", name, hash[:6])
}

func normalizedSpec(spec platformv1.PreviewEnvironmentSpec) (platformv1.PreviewEnvironmentSpec, error) {
	if spec.Image == "" || len(spec.Image) > 512 || strings.ContainsFunc(spec.Image, unicode.IsSpace) {
		return spec, fmt.Errorf("image must be a non-empty image reference without whitespace (max 512 bytes)")
	}
	if spec.Replicas == nil {
		n := int32(1)
		spec.Replicas = &n
	}
	if spec.Port == 0 {
		spec.Port = 80
	}
	if spec.TTLSeconds == 0 {
		spec.TTLSeconds = 3600
	}
	if *spec.Replicas < 1 || *spec.Replicas > 10 {
		return spec, fmt.Errorf("replicas must be between 1 and 10")
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return spec, fmt.Errorf("port must be between 1 and 65535")
	}
	if spec.TTLSeconds < 1 || spec.TTLSeconds > 604800 {
		return spec, fmt.Errorf("ttlSeconds must be between 1 and 604800")
	}
	return spec, nil
}

// Labels are selectors and diagnostics; only the controller owner reference proves ownership.
func ownedBy(obj client.Object, p *platformv1.PreviewEnvironment) bool {
	ref := metav1.GetControllerOf(obj)
	return p.UID != "" && ref != nil && ref.UID == p.UID && ref.Name == p.Name &&
		ref.Kind == "PreviewEnvironment" && ref.APIVersion == platformv1.GroupVersion.String()
}

func setLabels(obj client.Object, p *platformv1.PreviewEnvironment) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[ManagedByLabel] = "previewforge"
	labels[OwnerUIDLabel] = string(p.UID)
	obj.SetLabels(labels)
}

func selector(p *platformv1.PreviewEnvironment) map[string]string {
	return map[string]string{OwnerUIDLabel: string(p.UID), "app.kubernetes.io/name": AppName}
}

func configureDeployment(d *appsv1.Deployment, p *platformv1.PreviewEnvironment, spec platformv1.PreviewEnvironmentSpec) {
	d.Spec.Replicas = spec.Replicas
	d.Spec.Selector = &metav1.LabelSelector{MatchLabels: selector(p)}
	labels := d.Spec.Template.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range selector(p) {
		labels[k] = v
	}
	d.Spec.Template.Labels = labels
	no := false
	d.Spec.Template.Spec.AutomountServiceAccountToken = &no
	var container corev1.Container
	// Retain API-defaulted fields so reconciling does not cause perpetual rollouts.
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Name == AppName {
			container = c
			break
		}
	}
	container.Name = AppName
	container.Image = spec.Image
	container.ImagePullPolicy = corev1.PullIfNotPresent
	container.Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: spec.Port, Protocol: corev1.ProtocolTCP}}
	container.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("256Mi")},
	}
	container.ReadinessProbe = &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("http")}},
		InitialDelaySeconds: 2, PeriodSeconds: 5, TimeoutSeconds: 1, SuccessThreshold: 1, FailureThreshold: 3,
	}
	d.Spec.Template.Spec.Containers = []corev1.Container{container}
	deadline := int32(120)
	d.Spec.ProgressDeadlineSeconds = &deadline
}

func configureService(s *corev1.Service, p *platformv1.PreviewEnvironment, spec platformv1.PreviewEnvironmentSpec) {
	s.Spec.Type = corev1.ServiceTypeClusterIP
	s.Spec.Selector = selector(p)
	s.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: spec.Port, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP}}
}

func readiness(d *appsv1.Deployment, replicas int32) (platformv1.Phase, string, string) {
	if d.Status.ObservedGeneration < d.Generation {
		return platformv1.PhaseDeploying, "RolloutPending", "waiting for Deployment controller to observe the current generation"
	}
	for _, condition := range d.Status.Conditions {
		if condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionFalse && condition.Reason == "ProgressDeadlineExceeded" {
			return platformv1.PhaseFailed, "ProgressDeadlineExceeded", condition.Message
		}
		if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
			return platformv1.PhaseFailed, "ReplicaFailure", condition.Message
		}
	}
	if d.Status.UpdatedReplicas == replicas && d.Status.ReadyReplicas == replicas && d.Status.AvailableReplicas == replicas && d.Status.Replicas == replicas {
		return platformv1.PhaseReady, "DeploymentReady", "all desired replicas are updated, ready and available"
	}
	return platformv1.PhaseDeploying, "RolloutPending", fmt.Sprintf("waiting for rollout: %d/%d ready, %d updated, %d available", d.Status.ReadyReplicas, replicas, d.Status.UpdatedReplicas, d.Status.AvailableReplicas)
}

// ensure ownership is attached only to newly created resources, never to collisions.
func (r *PreviewEnvironmentReconciler) attachOwner(obj client.Object, p *platformv1.PreviewEnvironment) error {
	setLabels(obj, p)
	return controllerutil.SetControllerReference(p, obj, r.Scheme)
}
