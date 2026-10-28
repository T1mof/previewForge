package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	"github.com/T1mof/previewForge/internal/policy"
)

func TestGuardrailsOwnershipDriftAndRecreation(t *testing.T) {
	ctx := context.Background()
	p := preview("guardrails")
	p.Spec.SecurityProfile = platformv1.SecurityRestricted
	p.Spec.Resources = &platformv1.ComputeResources{Requests: platformv1.ResourceRequests{CPU: "200m", Memory: "128Mi"}, Limits: platformv1.ResourceLimits{CPU: "750m", Memory: "512Mi"}}
	r, c := fixture(t, p)
	reconcile(t, r, p.Name)
	reconcile(t, r, p.Name)
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, client.ObjectKey{Name: NamespaceName(p)}, ns); err != nil {
		t.Fatal(err)
	}
	if ns.Labels[policyPrefix+"enforce"] != "restricted" || ns.Labels[policyPrefix+"enforce-version"] != policyVersion {
		t.Fatal("Namespace admission profile missing")
	}
	q := &corev1.ResourceQuota{}
	l := &corev1.LimitRange{}
	if err := c.Get(ctx, client.ObjectKey{Name: QuotaName, Namespace: ns.Name}, q); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: LimitRangeName, Namespace: ns.Name}, l); err != nil {
		t.Fatal(err)
	}
	if !ownedBy(q, p) || !ownedBy(l, p) {
		t.Fatal("guardrails lack controller owner UID")
	}
	d := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Name: AppName, Namespace: ns.Name}, d); err != nil {
		t.Fatal(err)
	}
	if d.Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().MilliValue() != 200 || !*d.Spec.Template.Spec.SecurityContext.RunAsNonRoot || !*d.Spec.Template.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem {
		t.Fatal("application resource/profile settings missing")
	}
	q.Spec.Hard[corev1.ResourceRequestsCPU] = resource.MustParse("1")
	if err := c.Update(ctx, q); err != nil {
		t.Fatal(err)
	}
	l.Spec.Limits[0].Default[corev1.ResourceCPU] = resource.MustParse("900m")
	if err := c.Update(ctx, l); err != nil {
		t.Fatal(err)
	}
	ns.Labels[policyPrefix+"enforce"], ns.Labels["unrelated"] = "privileged", "keep"
	if err := c.Update(ctx, ns); err != nil {
		t.Fatal(err)
	}
	yes := true
	d.Spec.Template.Spec.HostNetwork = true
	d.Spec.Template.Spec.ServiceAccountName = "foreign-service-account"
	d.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "injected", Image: "busybox"}}
	d.Spec.Template.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: &yes}
	if err := c.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
		t.Fatal(err)
	}
	if ns.Labels[policyPrefix+"enforce"] != "restricted" || ns.Labels["unrelated"] != "keep" {
		t.Fatal("Namespace policy drift not repaired or unrelated label lost")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(q), q); err != nil {
		t.Fatal(err)
	}
	cpu := q.Spec.Hard[corev1.ResourceRequestsCPU]
	if cpu.Cmp(resource.MustParse("8")) != 0 {
		t.Fatal("quota drift not repaired")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(l), l); err != nil {
		t.Fatal(err)
	}
	if l.Spec.Limits[0].Default.Cpu().MilliValue() != 500 {
		t.Fatal("LimitRange drift not repaired")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(d), d); err != nil {
		t.Fatal(err)
	}
	pod := d.Spec.Template.Spec
	if pod.HostNetwork || pod.ServiceAccountName != "default" || len(pod.InitContainers) != 0 || pod.Containers[0].SecurityContext.Privileged != nil || *pod.SecurityContext.RunAsUser != 65532 || pod.Containers[0].SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatal("unsafe managed Pod drift survived")
	}
	if err := c.Delete(ctx, q); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, l); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(q), q); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(l), l); err != nil {
		t.Fatal(err)
	}
	if !ownedBy(q, p) || !ownedBy(l, p) {
		t.Fatal("recreated guardrails lost ownership")
	}
	qVersion, lVersion := q.ResourceVersion, l.ResourceVersion
	reconcile(t, r, p.Name)
	_ = c.Get(ctx, client.ObjectKeyFromObject(q), q)
	_ = c.Get(ctx, client.ObjectKeyFromObject(l), l)
	if q.ResourceVersion != qVersion || l.ResourceVersion != lVersion {
		t.Fatal("idempotent reconciliation rewrote guardrails")
	}
}

func TestForeignGuardrailsWithForgedLabelsAreNotAdopted(t *testing.T) {
	for _, kind := range []string{"quota", "limit-range"} {
		t.Run(kind, func(t *testing.T) {
			p := preview("foreign-" + kind)
			meta := metav1.ObjectMeta{Name: QuotaName, Namespace: NamespaceName(p), UID: "foreign-uid", Labels: map[string]string{ManagedByLabel: "previewforge", OwnerUIDLabel: string(p.UID)}}
			var foreign client.Object = &corev1.ResourceQuota{ObjectMeta: meta}
			if kind == "limit-range" {
				meta.Name = LimitRangeName
				foreign = &corev1.LimitRange{ObjectMeta: meta}
			}
			r, c := fixture(t, p, foreign)
			before := foreign.DeepCopyObject().(client.Object)
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(foreign), before); err != nil {
				t.Fatal(err)
			}
			reconcile(t, r, p.Name)
			_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: p.Name}})
			if err == nil || !strings.Contains(err.Error(), "ownership conflict") {
				t.Fatalf("foreign %s accepted: %v", kind, err)
			}
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(foreign), foreign); err != nil {
				t.Fatal(err)
			}
			if foreign.GetUID() != before.GetUID() || foreign.GetResourceVersion() != before.GetResourceVersion() || len(foreign.GetOwnerReferences()) != 0 {
				t.Fatal("foreign guardrail changed")
			}
		})
	}
}

func TestStricterNamespaceAdmissionIsNotWeakened(t *testing.T) {
	p := preview("already-restricted")
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: NamespaceName(p), Labels: map[string]string{policyPrefix + "enforce": "restricted", policyPrefix + "enforce-version": "latest"}}}
	r, c := fixture(t, p)
	if err := r.attachOwner(ns, p); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: p.Name}}); err == nil {
		t.Fatal("baseline must not silently weaken an existing restricted Namespace")
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(ns), ns); err != nil {
		t.Fatal(err)
	}
	if ns.Labels[policyPrefix+"enforce"] != "restricted" || ns.Labels[policyPrefix+"enforce-version"] != "latest" {
		t.Fatal("admission weakened")
	}
}

func TestNamespaceBudgetFitsSupportedRollingUpdate(t *testing.T) {
	q := &corev1.ResourceQuota{}
	configureQuota(q)
	maxResources, err := policy.NormalizeResources(&platformv1.ComputeResources{Requests: platformv1.ResourceRequests{CPU: "500m", Memory: "512Mi"}, Limits: platformv1.ResourceLimits{CPU: "1", Memory: "1Gi"}})
	if err != nil {
		t.Fatal(err)
	}
	defaults, _ := policy.NormalizeResources(nil)
	maximum, debug := policy.Requirements(maxResources), policy.Requirements(defaults)
	for _, field := range []struct {
		key        corev1.ResourceName
		app, debug corev1.ResourceList
	}{
		{corev1.ResourceRequestsCPU, maximum.Requests, debug.Requests}, {corev1.ResourceLimitsCPU, maximum.Limits, debug.Limits},
		{corev1.ResourceRequestsMemory, maximum.Requests, debug.Requests}, {corev1.ResourceLimitsMemory, maximum.Limits, debug.Limits},
	} {
		name := corev1.ResourceCPU
		if strings.Contains(string(field.key), "memory") {
			name = corev1.ResourceMemory
		}
		total := resource.MustParse("0")
		for range 11 {
			total.Add(field.app[name])
		}
		for range 4 {
			total.Add(field.debug[name])
		}
		if total.Cmp(q.Spec.Hard[field.key]) > 0 {
			t.Fatalf("ten replicas + one surge + four default debug Pods exceed %s quota", field.key)
		}
	}
}
