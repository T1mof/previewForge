package controller

import (
	"context"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	"github.com/T1mof/previewForge/internal/policy"
)

const (
	QuotaName      = "previewforge-budget"
	LimitRangeName = "previewforge-defaults"
	policyVersion  = "v1.37"
	policyPrefix   = "pod-security.kubernetes.io/"
)

// Never relax a Namespace that an administrator has already made restricted.
// Keep newer policy versions rather than silently downgrading their checks.
func configureNamespace(ns *corev1.Namespace, p *platformv1.PreviewEnvironment, profile platformv1.SecurityProfile) {
	setLabels(ns, p)
	labels := ns.Labels
	if labels[policyPrefix+"enforce"] != "restricted" {
		labels[policyPrefix+"enforce"] = string(profile)
	}
	for _, mode := range []string{"enforce", "warn", "audit"} {
		if mode != "enforce" {
			labels[policyPrefix+mode] = "restricted"
		}
		labels[policyPrefix+mode+"-version"] = atLeastPolicyVersion(labels[policyPrefix+mode+"-version"])
	}
}

func atLeastPolicyVersion(version string) string {
	if version == "latest" {
		return version
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) == 2 && strings.HasPrefix(version, "v") {
		major, e1 := strconv.Atoi(parts[0])
		minor, e2 := strconv.Atoi(parts[1])
		if e1 == nil && e2 == nil && (major > 1 || major == 1 && minor > 37) {
			return version
		}
	}
	return policyVersion
}

// Fixed budgets avoid quota shrink deadlocks during resource changes and rollout.
// Up to ten app replicas, one surge Pod and four default-sized debug Pods fit.
func configureQuota(q *corev1.ResourceQuota) {
	q.Spec = corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{
		corev1.ResourceRequestsCPU: resource.MustParse("8"), corev1.ResourceLimitsCPU: resource.MustParse("16"),
		corev1.ResourceRequestsMemory: resource.MustParse("8Gi"), corev1.ResourceLimitsMemory: resource.MustParse("16Gi"),
		corev1.ResourcePods: resource.MustParse("15"),
		"count/pods":        resource.MustParse("32"), "count/services": resource.MustParse("2"),
		"count/deployments.apps": resource.MustParse("1"), "count/replicasets.apps": resource.MustParse("16"),
		"count/secrets": resource.MustParse("8"), "count/configmaps": resource.MustParse("8"),
	}}
}

func configureLimitRange(l *corev1.LimitRange) {
	defaults, _ := policy.NormalizeResources(nil)
	resources := policy.Requirements(defaults)
	l.Spec = corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
		Type: corev1.LimitTypeContainer, Default: resources.Limits, DefaultRequest: resources.Requests,
		Min: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
		Max: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
	}}}
}

func (r *PreviewEnvironmentReconciler) reconcileGuardrails(ctx context.Context, p *platformv1.PreviewEnvironment, namespace string) (bool, error) {
	q := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: QuotaName, Namespace: namespace}}
	if err := r.ensure(ctx, p, q, func() { configureQuota(q) }); err != nil {
		return false, err
	}
	l := &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: LimitRangeName, Namespace: namespace}}
	if err := r.ensure(ctx, p, l, func() { configureLimitRange(l) }); err != nil {
		return false, err
	}
	return !q.DeletionTimestamp.IsZero() || !l.DeletionTimestamp.IsZero(), nil
}

func applicationPodSpec(spec platformv1.PreviewEnvironmentSpec) corev1.PodSpec {
	no, yes := false, true
	grace := int64(30)
	container := corev1.Container{
		Name: AppName, Image: spec.Image, ImagePullPolicy: corev1.PullIfNotPresent,
		Resources:              policy.Requirements(spec.Resources),
		SecurityContext:        &corev1.SecurityContext{AllowPrivilegeEscalation: &no, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"NET_RAW"}}},
		TerminationMessagePath: "/dev/termination-log", TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}
	pod := corev1.PodSpec{
		Containers: []corev1.Container{container}, RestartPolicy: corev1.RestartPolicyAlways,
		DNSPolicy: corev1.DNSClusterFirst, SchedulerName: corev1.DefaultSchedulerName,
		ServiceAccountName: "default", AutomountServiceAccountToken: &no, EnableServiceLinks: &no,
		TerminationGracePeriodSeconds: &grace,
		SecurityContext:               &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
	}
	if spec.SecurityProfile == platformv1.SecurityRestricted {
		uid, group := int64(65532), int64(65532)
		pod.SecurityContext.RunAsNonRoot, pod.SecurityContext.RunAsUser, pod.SecurityContext.RunAsGroup = &yes, &uid, &group
		pod.SecurityContext.FSGroup = &group
		pod.Containers[0].SecurityContext.ReadOnlyRootFilesystem = &yes
		pod.Containers[0].SecurityContext.Capabilities.Drop = []corev1.Capability{"ALL"}
		size := resource.MustParse("64Mi")
		pod.Volumes = []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &size}}}}
		pod.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}}
	}
	return pod
}
