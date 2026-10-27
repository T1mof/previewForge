package controller

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func TestNamespaceName(t *testing.T) {
	p := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("long.name-", 20), UID: "uid-one"}}
	name := NamespaceName(p)
	if problems := validation.IsDNS1123Label(name); len(problems) != 0 {
		t.Fatalf("invalid namespace %q: %v", name, problems)
	}
	if NamespaceName(p) != name {
		t.Fatal("name must be deterministic")
	}
	p.UID = types.UID("uid-two")
	if NamespaceName(p) == name {
		t.Fatal("recreated CR must get a different namespace")
	}
}

func TestNormalizedSpec(t *testing.T) {
	spec, err := normalizedSpec(platformv1.PreviewEnvironmentSpec{Image: "nginx:alpine"})
	if err != nil || *spec.Replicas != 1 || spec.Port != 80 || spec.TTLSeconds != 3600 {
		t.Fatalf("unexpected defaults: %+v, %v", spec, err)
	}
	for _, test := range []struct {
		name string
		spec platformv1.PreviewEnvironmentSpec
	}{
		{"empty image", platformv1.PreviewEnvironmentSpec{}},
		{"whitespace", platformv1.PreviewEnvironmentSpec{Image: "nginx\nlatest"}},
		{"port", platformv1.PreviewEnvironmentSpec{Image: "nginx", Port: 65536}},
		{"ttl", platformv1.PreviewEnvironmentSpec{Image: "nginx", TTLSeconds: -1}},
		{"too many replicas", platformv1.PreviewEnvironmentSpec{Image: "nginx", Replicas: int32Ptr(11)}},
		{"zero replicas", platformv1.PreviewEnvironmentSpec{Image: "nginx", Replicas: int32Ptr(0)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizedSpec(test.spec); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestReadiness(t *testing.T) {
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Status: appsv1.DeploymentStatus{
		ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2,
	}}
	phase, _, _ := readiness(d, 2)
	if phase != platformv1.PhaseReady {
		t.Fatal("complete rollout should be ready")
	}
	d.Status.ObservedGeneration = 1
	phase, _, _ = readiness(d, 2)
	if phase != platformv1.PhaseDeploying {
		t.Fatal("stale status must not be ready")
	}
	d.Status.ObservedGeneration = 2
	d.Status.UpdatedReplicas = 1
	phase, _, _ = readiness(d, 2)
	if phase != platformv1.PhaseDeploying {
		t.Fatal("old ready replicas must not imply rollout completion")
	}
	d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded", Message: "image cannot be pulled"}}
	phase, reason, message := readiness(d, 2)
	if phase != platformv1.PhaseFailed || reason != "ProgressDeadlineExceeded" || message != "image cannot be pulled" {
		t.Fatal("failed rollout must expose diagnostics")
	}
}

func int32Ptr(n int32) *int32 { return &n }
