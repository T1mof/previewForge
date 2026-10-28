package policy

import (
	"testing"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func TestResourceDefaultsBoundsAndIndependentCopy(t *testing.T) {
	defaults, err := NormalizeResources(nil)
	if err != nil || defaults.Requests.CPU != "100m" || defaults.Requests.Memory != "64Mi" || defaults.Limits.CPU != "500m" || defaults.Limits.Memory != "256Mi" {
		t.Fatalf("defaults: %+v %v", defaults, err)
	}
	input := &platformv1.ComputeResources{Requests: platformv1.ResourceRequests{CPU: "0.2"}}
	actual, err := NormalizeResources(input)
	if err != nil || actual.Requests.CPU != "0.2" || input.Requests.Memory != "" {
		t.Fatalf("normalization mutated caller or rejected equivalent quantity: %+v %v", input, err)
	}
	requirements := Requirements(actual)
	if requirements.Requests.Cpu().MilliValue() != 200 {
		t.Fatal("quantity conversion lost CPU units")
	}
	for _, test := range []struct {
		name   string
		change func(*platformv1.ComputeResources)
	}{
		{"invalid syntax", func(r *platformv1.ComputeResources) { r.Requests.CPU = "garbage" }},
		{"negative", func(r *platformv1.ComputeResources) { r.Requests.Memory = "-1Mi" }},
		{"tiny cpu", func(r *platformv1.ComputeResources) { r.Requests.CPU = "1u" }},
		{"tiny memory", func(r *platformv1.ComputeResources) { r.Limits.Memory = "1Mi" }},
		{"cpu cap", func(r *platformv1.ComputeResources) { r.Limits.CPU = "2" }},
		{"memory cap", func(r *platformv1.ComputeResources) { r.Limits.Memory = "2Gi" }},
		{"request cap", func(r *platformv1.ComputeResources) { r.Requests.CPU = "501m" }},
		{"request exceeds limit", func(r *platformv1.ComputeResources) { r.Requests.CPU, r.Limits.CPU = "300m", "200m" }},
		{"memory request exceeds limit", func(r *platformv1.ComputeResources) { r.Requests.Memory, r.Limits.Memory = "512Mi", "256Mi" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := *defaults
			test.change(&r)
			if _, err := NormalizeResources(&r); err == nil {
				t.Fatal("invalid resource envelope accepted")
			}
		})
	}
}

func TestProfileCompatibilityAndValidation(t *testing.T) {
	if p, err := NormalizeProfile(""); err != nil || p != platformv1.SecurityBaseline {
		t.Fatalf("legacy profile: %q %v", p, err)
	}
	if p, err := NormalizeProfile(platformv1.SecurityRestricted); err != nil || p != platformv1.SecurityRestricted {
		t.Fatalf("restricted: %q %v", p, err)
	}
	if _, err := NormalizeProfile("privileged"); err == nil {
		t.Fatal("unsupported profile accepted")
	}
}
