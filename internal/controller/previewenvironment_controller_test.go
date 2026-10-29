package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

var testNow = time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := platformv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func preview(name string) *platformv1.PreviewEnvironment {
	return &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{
		Name: name, UID: types.UID("uid-" + name), Generation: 1, CreationTimestamp: metav1.NewTime(testNow),
	}, Spec: platformv1.PreviewEnvironmentSpec{Image: "nginx:alpine", Replicas: int32Ptr(2), Port: 80, TTLSeconds: 3600}}
}

func fixture(t *testing.T, objects ...client.Object) (*PreviewEnvironmentReconciler, client.Client) {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&platformv1.PreviewEnvironment{}, &appsv1.Deployment{}).WithObjects(objects...).Build()
	return &PreviewEnvironmentReconciler{Client: c, APIReader: c, Scheme: s, Now: func() time.Time { return testNow }, Network: testNetworkConfig()}, c
}

func reconcile(t *testing.T, r *PreviewEnvironmentReconciler, name string) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProvisionReadyAndIdempotence(t *testing.T) {
	ctx := context.Background()
	p := preview("pr-42")
	r, c := fixture(t, p)
	reconcile(t, r, p.Name) // persist finalizer before creating resources
	result := reconcile(t, r, p.Name)
	if result.RequeueAfter != pollInterval {
		t.Fatalf("unexpected poll interval: %v", result)
	}
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: NamespaceName(p)}, ns); err != nil {
		t.Fatal(err)
	}
	if !ownedBy(ns, p) {
		t.Fatal("namespace must have controller owner")
	}
	d := &appsv1.Deployment{}
	key := types.NamespacedName{Name: AppName, Namespace: ns.Name}
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	if *d.Spec.Replicas != 2 || d.Spec.Template.Spec.Containers[0].Image != p.Spec.Image || !ownedBy(d, p) {
		t.Fatal("deployment differs from desired state")
	}
	container := d.Spec.Template.Spec.Containers[0]
	if container.Resources.Limits.Memory().String() != "256Mi" || container.ReadinessProbe.TCPSocket == nil {
		t.Fatal("resource limits and readiness probe must be set")
	}
	svc := &corev1.Service{}
	if err := c.Get(ctx, key, svc); err != nil {
		t.Fatal(err)
	}
	if svc.Spec.Ports[0].Port != 80 || !ownedBy(svc, p) {
		t.Fatal("invalid service")
	}
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != platformv1.PhaseDeploying {
		t.Fatalf("phase: %s", p.Status.Phase)
	}
	d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2}
	if err := c.Status().Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != platformv1.PhaseReady || !meta.IsStatusConditionTrue(p.Status.Conditions, "Ready") {
		t.Fatalf("not ready: %+v", p.Status)
	}
	objects := []client.Object{ns, d, svc, p}
	versions := make([]string, len(objects))
	for i, obj := range objects {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			t.Fatal(err)
		}
		versions[i] = obj.GetResourceVersion()
	}
	reconcile(t, r, p.Name)
	for i, obj := range objects {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			t.Fatal(err)
		}
		if obj.GetResourceVersion() != versions[i] {
			t.Fatalf("idempotent reconcile modified %T", obj)
		}
	}
}

func TestRepairAndSpecUpdate(t *testing.T) {
	ctx := context.Background()
	p := preview("repair")
	r, c := fixture(t, p)
	reconcile(t, r, p.Name)
	reconcile(t, r, p.Name)
	key := types.NamespacedName{Name: AppName, Namespace: NamespaceName(p)}
	d := &appsv1.Deployment{}
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	d.Spec.Replicas = int32Ptr(9)
	d.Spec.Template.Spec.Containers[0].Image = "foreign:drift"
	if err := c.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	if *d.Spec.Replicas != 2 || d.Spec.Template.Spec.Containers[0].Image != p.Spec.Image {
		t.Fatal("drift was not repaired")
	}
	if err := c.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	// A new reconciler has no per-environment state and recreates missing resources.
	restarted := &PreviewEnvironmentReconciler{Client: c, APIReader: c, Scheme: r.Scheme, Now: r.Now}
	reconcile(t, restarted, p.Name)
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Image = "nginx:stable-alpine"
	p.Spec.Port = 8080
	p.Spec.Replicas = int32Ptr(3)
	p.Generation++
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	svc := &corev1.Service{}
	if err := c.Get(ctx, key, svc); err != nil {
		t.Fatal(err)
	}
	svc.Spec.ClusterIP = "10.0.0.10"
	if err := c.Update(ctx, svc); err != nil {
		t.Fatal(err)
	}
	reconcile(t, restarted, p.Name)
	if err := c.Get(ctx, key, d); err != nil {
		t.Fatal(err)
	}
	if *d.Spec.Replicas != 3 || d.Spec.Template.Spec.Containers[0].Image != p.Spec.Image {
		t.Fatal("spec changes were not applied")
	}
	if err := c.Get(ctx, key, svc); err != nil {
		t.Fatal(err)
	}
	if svc.Spec.ClusterIP != "10.0.0.10" || svc.Spec.Ports[0].Port != 8080 {
		t.Fatal("service update must preserve allocated cluster IP")
	}
}

func TestOwnershipCollisions(t *testing.T) {
	for _, kind := range []string{"namespace", "deployment", "service"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			p := preview("collision")
			r, c := fixture(t, p)
			reconcile(t, r, p.Name)
			var obj client.Object
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: NamespaceName(p)}}
			if kind == "namespace" {
				obj = ns
			} else {
				if err := r.attachOwner(ns, p); err != nil {
					t.Fatal(err)
				}
				if err := c.Create(ctx, ns); err != nil {
					t.Fatal(err)
				}
				metadata := metav1.ObjectMeta{Name: AppName, Namespace: ns.Name}
				if kind == "deployment" {
					obj = &appsv1.Deployment{ObjectMeta: metadata}
				} else {
					obj = &corev1.Service{ObjectMeta: metadata}
				}
			}
			// Even forged PreviewForge labels do not authorize adoption.
			setLabels(obj, p)
			if err := c.Create(ctx, obj); err != nil {
				t.Fatal(err)
			}
			version := obj.GetResourceVersion()
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: p.Name}})
			if err == nil || !strings.Contains(err.Error(), "ownership conflict") {
				t.Fatalf("expected collision: %v", err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				t.Fatal(err)
			}
			if obj.GetResourceVersion() != version || ownedBy(obj, p) {
				t.Fatal("foreign resource was changed")
			}
			if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
				t.Fatal(err)
			}
			if p.Status.Phase != platformv1.PhaseFailed {
				t.Fatal("collision must be diagnosable")
			}
		})
	}
}

func TestTTLAndFinalizer(t *testing.T) {
	ctx := context.Background()
	p := preview("ttl")
	p.Spec.TTLSeconds = 10
	r, c := fixture(t, p)
	reconcile(t, r, p.Name)
	if got := reconcile(t, r, p.Name).RequeueAfter; got != 10*time.Second {
		t.Fatalf("TTL wakeup: %s", got)
	}
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: NamespaceName(p)}, ns); err != nil {
		t.Fatal(err)
	}
	ns.Finalizers = []string{"test.previewforge.dev/hold"}
	if err := c.Update(ctx, ns); err != nil {
		t.Fatal(err)
	}
	// TTL must survive a restart; time does not come from in-memory timers.
	r = &PreviewEnvironmentReconciler{Client: c, APIReader: c, Scheme: r.Scheme, Now: func() time.Time { return testNow.Add(10 * time.Second) }}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != platformv1.PhaseExpired || p.DeletionTimestamp.IsZero() {
		t.Fatal("TTL must expire and delete CR")
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
		t.Fatal(err)
	}
	if ns.DeletionTimestamp.IsZero() {
		t.Fatal("namespace deletion was not requested")
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(p, Finalizer) {
		t.Fatal("finalizer removed while namespace still exists")
	}
	// Fake client has no namespace controller; emulate completion of namespace deletion.
	ns.Finalizers = nil
	if err := c.Update(ctx, ns); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); !apierrors.IsNotFound(err) {
		t.Fatalf("CR should be gone: %v", err)
	}
}

func TestDeletionNeverUsesStatusNamespace(t *testing.T) {
	ctx := context.Background()
	p := preview("delete")
	r, c := fixture(t, p)
	reconcile(t, r, p.Name)
	reconcile(t, r, p.Name)
	foreign := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "foreign"}}
	if err := c.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	p.Status.Namespace = foreign.Name
	if err := c.Status().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, types.NamespacedName{Name: foreign.Name}, foreign); err != nil {
		t.Fatalf("foreign namespace was affected: %v", err)
	}
}

func TestDeletionRefusesForeignNamespace(t *testing.T) {
	ctx := context.Background()
	p := preview("foreign-delete")
	p.Finalizers = []string{Finalizer}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: NamespaceName(p)}}
	r, c := fixture(t, p, ns)
	if err := c.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: p.Name}})
	if err == nil || !strings.Contains(err.Error(), "refusing to delete") {
		t.Fatalf("unsafe cleanup: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
		t.Fatal(err)
	}
	if !ns.DeletionTimestamp.IsZero() {
		t.Fatal("foreign namespace deletion was requested")
	}
}

type failingClient struct {
	client.Client
	failGet   bool
	failPatch bool
}

func TestStaleStatusIsRejected(t *testing.T) {
	for _, change := range []string{"generation", "uid", "deletion"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			p := preview("stale")
			p.Finalizers = []string{Finalizer}
			r, c := fixture(t, p)
			current := &platformv1.PreviewEnvironment{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(p), current); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "generation":
				current.Spec.Image = "new:image"
				current.Generation++
			case "uid":
				current.UID = "replacement-uid"
			case "deletion":
				if err := c.Delete(ctx, current); err != nil {
					t.Fatal(err)
				}
			}
			if change != "deletion" {
				if err := c.Update(ctx, current); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.updateStatus(ctx, p, platformv1.PhaseReady, "DeploymentReady", "stale result", 2); !apierrors.IsConflict(err) {
				t.Fatalf("stale status must conflict: %v", err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(p), current); err != nil {
				t.Fatal(err)
			}
			if current.Status.Phase == platformv1.PhaseReady {
				t.Fatal("stale readiness was published")
			}
		})
	}
}

func TestTerminatingResourcesWait(t *testing.T) {
	for _, kind := range []string{"namespace", "service"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			p := preview("terminating")
			r, c := fixture(t, p)
			reconcile(t, r, p.Name)
			reconcile(t, r, p.Name)
			var obj client.Object
			if kind == "namespace" {
				obj = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: NamespaceName(p)}}
			} else {
				obj = &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: NamespaceName(p)}}
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				t.Fatal(err)
			}
			obj.SetFinalizers([]string{"test.previewforge.dev/hold"})
			if err := c.Update(ctx, obj); err != nil {
				t.Fatal(err)
			}
			if err := c.Delete(ctx, obj); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				t.Fatal(err)
			}
			version := obj.GetResourceVersion()
			reconcile(t, r, p.Name)
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				t.Fatal(err)
			}
			if obj.GetResourceVersion() != version {
				t.Fatal("terminating resource must not be updated")
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
				t.Fatal(err)
			}
			if p.Status.Phase == platformv1.PhaseReady {
				t.Fatal("terminating resource cannot imply Ready")
			}
		})
	}
}

func (c *failingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Namespace); ok && c.failGet {
		c.failGet = false
		return apierrors.NewServiceUnavailable("temporary API outage")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *failingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.failPatch {
		c.failPatch = false
		return apierrors.NewConflict(platformv1.GroupVersion.WithResource("previewenvironments").GroupResource(), obj.GetName(), fmt.Errorf("concurrent update"))
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestAPIErrorsAndRecovery(t *testing.T) {
	ctx := context.Background()
	p := preview("api-errors")
	r, c := fixture(t, p)
	wrapper := &failingClient{Client: c, failPatch: true}
	r.Client = wrapper
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: p.Name}}); !apierrors.IsConflict(err) {
		t.Fatalf("conflict must be returned for retry: %v", err)
	}
	reconcile(t, r, p.Name)
	wrapper.failGet = true
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: p.Name}}); err == nil {
		t.Fatal("API outage must be returned for retry")
	}
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != platformv1.PhaseFailed || !strings.Contains(p.Status.Message, "temporary API outage") {
		t.Fatalf("missing diagnostics: %+v", p.Status)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, types.NamespacedName{Name: p.Name}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != platformv1.PhaseDeploying {
		t.Fatal("transient failure should recover")
	}
}

func TestConcurrentEnvironments(t *testing.T) {
	objects := make([]client.Object, 8)
	for i := range objects {
		objects[i] = preview(fmt.Sprintf("pr-%d", i))
	}
	r, c := fixture(t, objects...)
	var wg sync.WaitGroup
	errors := make(chan error, len(objects))
	for _, obj := range objects {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}); err != nil {
					errors <- err
					return
				}
			}
		}(obj.GetName())
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	namespaces := &corev1.NamespaceList{}
	if err := c.List(context.Background(), namespaces); err != nil {
		t.Fatal(err)
	}
	if len(namespaces.Items) != len(objects) {
		t.Fatalf("expected isolated namespaces, got %d", len(namespaces.Items))
	}
}
