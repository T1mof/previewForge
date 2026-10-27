//go:build integration && !windows

package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

// Envtest has an API server and etcd, but no kubelet, Deployment controller,
// namespace controller or garbage collector. Ready is simulated explicitly.
func TestEnvtest(t *testing.T) {
	environment := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	cfg, err := environment.Start()
	if err != nil {
		t.Fatalf("start envtest (set KUBEBUILDER_ASSETS): %v", err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})
	scheme := testScheme(t)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	operatorConfig := envtestOperatorConfig(t, environment, c, scheme)
	operatorClient, err := client.New(operatorConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	verifyEnvtestSafety(t, ctx, c, operatorClient, scheme)
	verifyEnvtestPRSource(t, environment, c, scheme)

	t.Run("schema and defaults", func(t *testing.T) {
		bad := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: "invalid"}, Spec: platformv1.PreviewEnvironmentSpec{Image: "nginx", Replicas: int32Ptr(0)}}
		if err := c.Create(ctx, bad); !apierrors.IsInvalid(err) {
			t.Fatalf("schema must reject zero replicas: %v", err)
		}
		bad.Spec.Replicas = nil
		bad.Spec.Image = "nginx bad"
		if err := c.Create(ctx, bad); !apierrors.IsInvalid(err) {
			t.Fatalf("schema must reject image whitespace: %v", err)
		}
		bad.Spec.Image = "nginx"
		for _, ingress := range []*platformv1.IngressSpec{
			{Host: "127.0.0.1", ClassName: "traefik"},
			{Host: "*.example.test", ClassName: "traefik"},
			{Host: "preview.example.test"},
		} {
			bad.Spec.Ingress = ingress
			if err := c.Create(ctx, bad); !apierrors.IsInvalid(err) {
				t.Fatalf("schema must reject ingress %+v: %v", ingress, err)
			}
		}
	})

	startManager := func() func() {
		t.Helper()
		// Production restarts create a new process; this test creates a second
		// manager in one process after the first is fully stopped. The controller
		// name registry is process-global, so permit that reuse only in this test.
		skipDuplicateNameValidation := true
		mgr, err := ctrl.NewManager(operatorConfig, ctrl.Options{
			Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0",
			LeaderElection: true, LeaderElectionNamespace: "previewforge-system",
			LeaderElectionID: "envtest.previewforge.dev", LeaderElectionReleaseOnCancel: true,
			Controller: config.Controller{SkipNameValidation: &skipDuplicateNameValidation},
		})
		if err != nil {
			t.Fatal(err)
		}
		r := &PreviewEnvironmentReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Scheme: scheme}
		if err := r.SetupWithManager(mgr); err != nil {
			t.Fatal(err)
		}
		managerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- mgr.Start(managerCtx) }()
		return func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("manager stopped with error: %v", err)
				}
			case <-time.After(15 * time.Second):
				t.Error("manager did not shut down")
			}
		}
	}
	stop := startManager()
	t.Cleanup(func() {
		if stop != nil {
			stop()
		}
	})
	p := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: "integration"}, Spec: platformv1.PreviewEnvironmentSpec{Image: "nginx:alpine"}}
	p.Spec.Ingress = &platformv1.IngressSpec{Host: "integration.example.test", ClassName: "traefik"}
	if err := c.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	if p.Spec.Replicas == nil || *p.Spec.Replicas != 1 || p.Spec.Port != 80 || p.Spec.TTLSeconds != 3600 {
		t.Fatalf("CRD defaults missing: %+v", p.Spec)
	}
	key := types.NamespacedName{Name: AppName, Namespace: NamespaceName(p)}
	d := &appsv1.Deployment{}
	svc := &corev1.Service{}
	ingress := &networkingv1.Ingress{}
	eventually(t, func() error {
		if err := c.Get(ctx, key, d); err != nil {
			return err
		}
		if err := c.Get(ctx, key, svc); err != nil {
			return err
		}
		if err := c.Get(ctx, key, ingress); err != nil {
			return err
		}
		return nil
	})
	if svc.Spec.ClusterIP == "" || !ownedBy(d, p) || !ownedBy(svc, p) {
		t.Fatal("real API must allocate Service IP and preserve ownership")
	}
	if !ownedBy(ingress, p) || ingress.Spec.Rules[0].Host != "integration.example.test" {
		t.Fatal("real API must preserve Ingress route and ownership")
	}
	// Verify API defaulting does not make reconciliation continually write a Deployment.
	r := &PreviewEnvironmentReconciler{Client: c, APIReader: c, Scheme: scheme}
	eventually(t, func() error {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: p.Name}})
		return err
	})
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	version := d.ResourceVersion
	eventually(t, func() error {
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: p.Name}})
		return err
	})
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	if d.ResourceVersion != version {
		t.Fatal("API defaulting broke idempotence")
	}
	d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	if err := c.Status().Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() error {
		if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
			return err
		}
		if p.Status.Phase != platformv1.PhaseReady {
			return fmt.Errorf("phase is %s", p.Status.Phase)
		}
		return nil
	})
	if p.Status.URL != "http://integration.example.test" {
		t.Fatalf("configured route URL missing: %s", p.Status.URL)
	}
	// A real Ingress watch must recreate a deleted route under ServiceAccount RBAC.
	oldIngressUID := ingress.UID
	if err := c.Delete(ctx, ingress); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() error {
		if err := c.Get(ctx, key, ingress); err != nil {
			return err
		}
		if ingress.UID == oldIngressUID {
			return fmt.Errorf("old ingress still exists")
		}
		return nil
	})
	// Disabling ingress must remove only its owned route and clear the advertised URL.
	eventually(t, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		before := p.DeepCopy()
		p.Spec.Ingress = nil
		return c.Patch(ctx, p, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
	eventually(t, func() error {
		if err := c.Get(ctx, key, ingress); !apierrors.IsNotFound(err) {
			return fmt.Errorf("ingress not removed: %v", err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		if p.Status.URL != "" {
			return fmt.Errorf("disabled route URL remains")
		}
		return nil
	})
	// Delete event must recreate a managed Deployment without polling it manually.
	oldUID := d.UID
	if err := c.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() error {
		if err := c.Get(ctx, key, d); err != nil {
			return err
		}
		if d.UID == oldUID {
			return fmt.Errorf("old deployment still exists")
		}
		return nil
	})
	stop()
	stop = nil
	// Change state while operator is stopped; a fresh manager must observe the CR.
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Image = "nginx:stable-alpine"
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	stop = startManager()
	eventually(t, func() error {
		if err := c.Get(ctx, key, d); err != nil {
			return err
		}
		if d.Spec.Template.Spec.Containers[0].Image != p.Spec.Image {
			return fmt.Errorf("restart has not reconciled new image")
		}
		return nil
	})
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	// Concurrent status writes may change resourceVersion; use a fresh optimistic patch.
	eventually(t, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		before := p.DeepCopy()
		p.Spec.TTLSeconds = 1
		return c.Patch(ctx, p, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
	eventually(t, func() error {
		ns := &corev1.Namespace{}
		if err := c.Get(ctx, types.NamespacedName{Name: key.Namespace}, ns); err != nil {
			return err
		}
		if ns.DeletionTimestamp.IsZero() {
			return fmt.Errorf("namespace deletion not requested")
		}
		return nil
	})
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != platformv1.PhaseExpired || len(p.Finalizers) == 0 {
		t.Fatal("expired CR must wait for namespace finalization")
	}
}

func eventually(t *testing.T, check func() error) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if last = check(); last == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("condition did not converge: %v", last)
}
