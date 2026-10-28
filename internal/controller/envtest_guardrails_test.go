//go:build integration && !windows

package controller

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func verifyGuardrailsSchema(t *testing.T, c client.Client) {
	t.Helper()
	t.Run("resource schema defaults and security transition", func(t *testing.T) {
		ctx := context.Background()
		p := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: "resource-schema"}, Spec: platformv1.PreviewEnvironmentSpec{Image: "example:test", Resources: &platformv1.ComputeResources{}}}
		if err := c.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		if p.Spec.SecurityProfile != platformv1.SecurityBaseline || p.Spec.Resources.Requests.CPU != "100m" || p.Spec.Resources.Requests.Memory != "64Mi" || p.Spec.Resources.Limits.CPU != "500m" || p.Spec.Resources.Limits.Memory != "256Mi" {
			t.Fatalf("nested API defaults missing: %+v", p.Spec)
		}
		for _, resources := range []platformv1.ComputeResources{
			{Requests: platformv1.ResourceRequests{CPU: "invalid"}},
			{Requests: platformv1.ResourceRequests{CPU: "501m"}},
			{Limits: platformv1.ResourceLimits{Memory: "2Gi"}},
			{Requests: platformv1.ResourceRequests{CPU: "300m"}, Limits: platformv1.ResourceLimits{CPU: "200m"}},
			{Requests: platformv1.ResourceRequests{Memory: "512Mi"}, Limits: platformv1.ResourceLimits{Memory: "256Mi"}},
		} {
			bad := p.DeepCopy()
			bad.Spec.Resources = &resources
			if err := c.Update(ctx, bad); !apierrors.IsInvalid(err) {
				t.Fatalf("invalid resources accepted: %+v: %v", resources, err)
			}
		}
		p.Spec.SecurityProfile = platformv1.SecurityRestricted
		if err := c.Update(ctx, p); err != nil {
			t.Fatal(err)
		}
		p.Spec.SecurityProfile = platformv1.SecurityBaseline
		if err := c.Update(ctx, p); !apierrors.IsInvalid(err) {
			t.Fatalf("security downgrade accepted: %v", err)
		}
		// No manager/finalizer yet: do not leave a fixture for the lifecycle test.
		if err := c.Delete(ctx, p); err != nil {
			t.Fatal(err)
		}
	})
}

// These exercise watch/RBAC/patch behavior. Envtest has no quota controller and
// does not prove resource admission, running Pods or Namespace garbage collection.
func verifyGuardrailsWatches(t *testing.T, c client.Client, p *platformv1.PreviewEnvironment) {
	t.Helper()
	ctx := context.Background()
	q := &corev1.ResourceQuota{}
	l := &corev1.LimitRange{}
	qKey := client.ObjectKey{Name: QuotaName, Namespace: NamespaceName(p)}
	lKey := client.ObjectKey{Name: LimitRangeName, Namespace: NamespaceName(p)}
	eventually(t, func() error {
		if err := c.Get(ctx, qKey, q); err != nil {
			return err
		}
		return c.Get(ctx, lKey, l)
	})
	if !ownedBy(q, p) || !ownedBy(l, p) {
		t.Fatal("guardrail owner UID missing")
	}
	qUID, lUID := q.UID, l.UID
	if err := c.Delete(ctx, q); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, l); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() error {
		if err := c.Get(ctx, qKey, q); err != nil {
			return err
		}
		if err := c.Get(ctx, lKey, l); err != nil {
			return err
		}
		if q.UID == qUID || l.UID == lUID || !ownedBy(q, p) || !ownedBy(l, p) {
			return fmt.Errorf("guardrails not recreated with owner UID")
		}
		return nil
	})
	eventually(t, func() error {
		if err := c.Get(ctx, qKey, q); err != nil {
			return err
		}
		before := q.DeepCopy()
		q.Spec.Hard[corev1.ResourceRequestsCPU] = resource.MustParse("7")
		return c.Patch(ctx, q, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
	eventually(t, func() error {
		if err := c.Get(ctx, lKey, l); err != nil {
			return err
		}
		before := l.DeepCopy()
		l.Spec.Limits[0].Default[corev1.ResourceCPU] = resource.MustParse("900m")
		return c.Patch(ctx, l, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
	eventually(t, func() error {
		if err := c.Get(ctx, qKey, q); err != nil {
			return err
		}
		if err := c.Get(ctx, lKey, l); err != nil {
			return err
		}
		cpu := q.Spec.Hard[corev1.ResourceRequestsCPU]
		if cpu.Cmp(resource.MustParse("8")) != 0 || l.Spec.Limits[0].Default.Cpu().MilliValue() != 500 {
			return fmt.Errorf("guardrail drift remains")
		}
		return nil
	})
}
