//go:build integration && !windows

package controller

import (
	"context"
	"fmt"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

// API/watch/owner validation only: envtest does not run a policy dataplane.
func verifyNetworkWatches(t *testing.T, c client.Client, p *platformv1.PreviewEnvironment) {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{IsolationPolicyName, GatewayPolicyName} {
		q := &networkingv1.NetworkPolicy{}
		key := client.ObjectKey{Name: name, Namespace: NamespaceName(p)}
		eventually(t, func() error { return c.Get(ctx, key, q) })
		if !ownedBy(q, p) {
			t.Fatal("network policy owner UID missing")
		}
		uid := q.UID
		if err := c.Delete(ctx, q); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() error {
			if err := c.Get(ctx, key, q); err != nil {
				return err
			}
			if q.UID == uid || !ownedBy(q, p) {
				return fmt.Errorf("network watch has not recreated owned policy")
			}
			return nil
		})
		eventually(t, func() error {
			if err := c.Get(ctx, key, q); err != nil {
				return err
			}
			before := q.DeepCopy()
			q.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
			return c.Patch(ctx, q, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
		})
		eventually(t, func() error {
			if err := c.Get(ctx, key, q); err != nil {
				return err
			}
			if len(q.Spec.Ingress) == 0 || len(q.Spec.Ingress[0].From) == 0 {
				return fmt.Errorf("allow-all network drift survives")
			}
			return nil
		})
	}
}
