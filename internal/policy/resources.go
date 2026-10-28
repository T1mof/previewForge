// Package policy shares resource validation between reconciliation and the PR CLI.
package policy

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func NormalizeProfile(profile platformv1.SecurityProfile) (platformv1.SecurityProfile, error) {
	if profile == "" {
		return platformv1.SecurityBaseline, nil
	}
	if profile != platformv1.SecurityBaseline && profile != platformv1.SecurityRestricted {
		return "", fmt.Errorf("unsupported security profile %q", profile)
	}
	return profile, nil
}

func NormalizeResources(input *platformv1.ComputeResources) (*platformv1.ComputeResources, error) {
	r := &platformv1.ComputeResources{}
	if input != nil {
		*r = *input
	}
	if r.Requests.CPU == "" {
		r.Requests.CPU = "100m"
	}
	if r.Requests.Memory == "" {
		r.Requests.Memory = "64Mi"
	}
	if r.Limits.CPU == "" {
		r.Limits.CPU = "500m"
	}
	if r.Limits.Memory == "" {
		r.Limits.Memory = "256Mi"
	}
	for _, field := range []struct{ name, value, low, high string }{
		{"requests.cpu", r.Requests.CPU, "1m", "500m"}, {"requests.memory", r.Requests.Memory, "16Mi", "512Mi"},
		{"limits.cpu", r.Limits.CPU, "1m", "1"}, {"limits.memory", r.Limits.Memory, "16Mi", "1Gi"},
	} {
		q, err := resource.ParseQuantity(field.value)
		if err != nil || len(field.value) > 32 || q.Cmp(resource.MustParse(field.low)) < 0 || q.Cmp(resource.MustParse(field.high)) > 0 {
			return nil, fmt.Errorf("resources.%s must be a quantity between %s and %s", field.name, field.low, field.high)
		}
	}
	cpuRequest, memoryRequest := resource.MustParse(r.Requests.CPU), resource.MustParse(r.Requests.Memory)
	if cpuRequest.Cmp(resource.MustParse(r.Limits.CPU)) > 0 || memoryRequest.Cmp(resource.MustParse(r.Limits.Memory)) > 0 {
		return nil, fmt.Errorf("resource requests must not exceed limits")
	}
	return r, nil
}

// Requirements expects the output of NormalizeResources.
func Requirements(r *platformv1.ComputeResources) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(r.Requests.CPU), corev1.ResourceMemory: resource.MustParse(r.Requests.Memory)},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(r.Limits.CPU), corev1.ResourceMemory: resource.MustParse(r.Limits.Memory)},
	}
}
