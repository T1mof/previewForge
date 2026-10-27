package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func TestStatusPreservesFreshConditions(t *testing.T) {
	ctx := context.Background()
	p := preview("conditions")
	r, c := fixture(t, p)
	current := p.DeepCopy()
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), current); err != nil {
		t.Fatal(err)
	}
	transition := metav1.NewTime(testNow.Add(-time.Minute))
	current.Status.Conditions = []metav1.Condition{
		{Type: "Ready", Status: metav1.ConditionTrue, Reason: "DeploymentReady", Message: "already ready", ObservedGeneration: p.Generation, LastTransitionTime: transition},
		{Type: "ExternalCheck", Status: metav1.ConditionTrue, Reason: "Passed", Message: "written after cached read", ObservedGeneration: p.Generation, LastTransitionTime: transition},
	}
	if err := c.Status().Update(ctx, current); err != nil {
		t.Fatal(err)
	}
	// p simulates an older cache snapshot with no conditions at the same generation.
	if err := r.updateStatus(ctx, p, platformv1.PhaseReady, "DeploymentReady", "all ready", 2); err != nil {
		t.Fatal(err)
	}
	if !meta.IsStatusConditionTrue(p.Status.Conditions, "ExternalCheck") {
		t.Fatal("fresh external condition was lost")
	}
	ready := meta.FindStatusCondition(p.Status.Conditions, "Ready")
	if ready == nil || !ready.LastTransitionTime.Equal(&transition) {
		t.Fatal("unchanged Ready must preserve the latest transition time")
	}
}

type hookedClient struct {
	client.Client
	onStatusPatch func(context.Context) error
	onDelete      func(context.Context, client.Object) error
}

type hookedStatusWriter struct {
	client.SubResourceWriter
	owner *hookedClient
}

func (c *hookedClient) Status() client.SubResourceWriter {
	return &hookedStatusWriter{SubResourceWriter: c.Client.Status(), owner: c}
}

func (w *hookedStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	if hook := w.owner.onStatusPatch; hook != nil {
		w.owner.onStatusPatch = nil
		if err := hook(ctx); err != nil {
			return err
		}
	}
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func (c *hookedClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if hook := c.onDelete; hook != nil {
		c.onDelete = nil
		if err := hook(ctx, obj); err != nil {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func TestStatusPatchConflictRetriesWithoutOverwriting(t *testing.T) {
	ctx := context.Background()
	p := preview("status-conflict")
	r, c := fixture(t, p)
	wrapper := &hookedClient{Client: c}
	wrapper.onStatusPatch = func(ctx context.Context) error {
		latest := p.DeepCopy()
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), latest); err != nil {
			return err
		}
		latest.Status.Message = "concurrent writer"
		return c.Status().Update(ctx, latest)
	}
	r.Client = wrapper
	err := r.updateStatus(ctx, p, platformv1.PhaseReady, "DeploymentReady", "ready", 2)
	if !apierrors.IsConflict(err) {
		t.Fatalf("expected actual optimistic-lock conflict, got %v", err)
	}
	latest := p.DeepCopy()
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), latest); err != nil {
		t.Fatal(err)
	}
	if latest.Status.Message != "concurrent writer" || latest.Status.Phase == platformv1.PhaseReady {
		t.Fatal("conflicting patch overwrote newer status")
	}
	if err := r.updateStatus(ctx, p, platformv1.PhaseReady, "DeploymentReady", "ready", 2); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase != platformv1.PhaseReady {
		t.Fatal("status did not recover on retry")
	}
}

func TestTTLConcurrentExtensionPreventsDelete(t *testing.T) {
	ctx := context.Background()
	p := preview("ttl-extension")
	p.Spec.TTLSeconds = 1
	p.Finalizers = []string{Finalizer}
	r, c := fixture(t, p)
	r.Now = func() time.Time { return testNow.Add(2 * time.Second) }
	wrapper := &hookedClient{Client: c}
	wrapper.onDelete = func(ctx context.Context, obj client.Object) error {
		latest := &platformv1.PreviewEnvironment{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
			return err
		}
		latest.Spec.TTLSeconds = 3600
		latest.Generation++
		return c.Update(ctx, latest)
	}
	r.Client = wrapper
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
	if !apierrors.IsConflict(err) {
		t.Fatalf("concurrent TTL extension must prevent stale delete: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	if !p.DeletionTimestamp.IsZero() || p.Spec.TTLSeconds != 3600 {
		t.Fatal("extended environment was deleted")
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	if p.Status.Phase == platformv1.PhaseExpired {
		t.Fatal("TTL extension should recover from Expired after conflict")
	}
}

func TestNamespaceReplacementBetweenReadAndDeleteIsSafe(t *testing.T) {
	ctx := context.Background()
	p := preview("replacement")
	p.Finalizers = []string{Finalizer}
	r, c := fixture(t, p)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: NamespaceName(p), UID: "original-namespace"}}
	if err := r.attachOwner(ns, p); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	wrapper := &hookedClient{Client: c}
	wrapper.onDelete = func(ctx context.Context, obj client.Object) error {
		if err := c.Delete(ctx, obj); err != nil {
			return err
		}
		replacement := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: obj.GetName(), UID: "foreign-replacement"}}
		if err := c.Create(ctx, replacement); err != nil {
			return err
		}
		// Fake client checks resourceVersion but does not implement UID delete
		// preconditions. Give the replacement a distinct version; envtest covers UID.
		replacement.Annotations = map[string]string{"test": "replacement"}
		return c.Update(ctx, replacement)
	}
	r.Client = wrapper
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
	if !apierrors.IsConflict(err) {
		t.Fatalf("stale namespace delete must conflict: %v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: ns.Name}, ns); err != nil {
		t.Fatal(err)
	}
	if ns.UID != "foreign-replacement" || !ns.DeletionTimestamp.IsZero() {
		t.Fatal("foreign replacement namespace was affected")
	}
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)})
	if err == nil {
		t.Fatal("subsequent cleanup must refuse foreign namespace")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(p, Finalizer) {
		t.Fatal("unsafe cleanup released finalizer")
	}
}

func TestOwnerReferenceIdentity(t *testing.T) {
	p := preview("owner-reference")
	r, _ := fixture(t, p)
	for _, mutation := range []string{"uid", "name", "kind", "apiVersion", "controller"} {
		t.Run(mutation, func(t *testing.T) {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: NamespaceName(p)}}
			if err := r.attachOwner(ns, p); err != nil {
				t.Fatal(err)
			}
			if !ownedBy(ns, p) || ns.OwnerReferences[0].BlockOwnerDeletion == nil || !*ns.OwnerReferences[0].BlockOwnerDeletion {
				t.Fatal("missing correct controller reference")
			}
			ref := &ns.OwnerReferences[0]
			switch mutation {
			case "uid":
				ref.UID = "other"
			case "name":
				ref.Name = "other"
			case "kind":
				ref.Kind = "Other"
			case "apiVersion":
				ref.APIVersion = "other/v1"
			case "controller":
				no := false
				ref.Controller = &no
			}
			if ownedBy(ns, p) {
				t.Fatalf("ownership accepted mismatched %s", mutation)
			}
		})
	}
}

func TestCleanupAPIFailurePreservesAllFinalizers(t *testing.T) {
	ctx := context.Background()
	p := preview("cleanup-api")
	p.Finalizers = []string{Finalizer, "external.example/cleanup"}
	r, c := fixture(t, p)
	if err := c.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	r.APIReader = &failingClient{Client: c, failGet: true}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); !apierrors.IsServiceUnavailable(err) {
		t.Fatalf("cleanup must retry API errors: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	if len(p.Finalizers) != 2 {
		t.Fatal("API failure released a finalizer")
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	if controllerutil.ContainsFinalizer(p, Finalizer) || !controllerutil.ContainsFinalizer(p, "external.example/cleanup") {
		t.Fatal("successful cleanup must remove only our finalizer")
	}
}
