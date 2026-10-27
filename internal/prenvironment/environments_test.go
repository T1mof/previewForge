package prenvironment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func request() Request {
	return Request{RepositoryID: 123, Number: 42, Image: "ghcr.io/example/app@sha256:" + strings.Repeat("a", 64), Revision: strings.Repeat("b", 40), Port: 8080, Replicas: 1, TTLSeconds: 3600, Host: "pr-42.example.test", ClassName: "traefik"}
}

func fixture(t *testing.T, objects ...client.Object) (*Environments, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := platformv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	source := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: DefaultSourceNamespace, UID: "source-uid"}}
	objects = append(objects, source)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&platformv1.PreviewEnvironment{}).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetUID() == "" {
				obj.SetUID(types.UID("created-" + obj.GetName()))
			}
			created := obj.GetCreationTimestamp()
			if created.IsZero() {
				obj.SetCreationTimestamp(metav1.NewTime(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)))
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	return &Environments{Client: c, Source: Binding{Namespace: source.Name, UID: source.UID}}, c
}

func TestApplyPreservesIdentityTTLAndUnrelatedMetadata(t *testing.T) {
	ctx := context.Background()
	e, c := fixture(t)
	r := request()
	p, err := e.Apply(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	owner := metav1.GetControllerOf(p)
	if owner == nil || owner.UID != e.Source.UID || owner.Kind != "Namespace" || owner.BlockOwnerDeletion == nil || *owner.BlockOwnerDeletion {
		t.Fatalf("incorrect source owner: %+v", owner)
	}
	uid, created := p.UID, p.CreationTimestamp
	p.Annotations["unrelated"] = "preserve"
	p.Finalizers = []string{"test.previewforge.dev/hold"}
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Status.Message = "controller status"
	if err := c.Status().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	r.Image = "ghcr.io/example/app@sha256:" + strings.Repeat("c", 64)
	r.Revision = strings.Repeat("d", 40)
	p, err = e.Apply(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if p.UID != uid || !p.CreationTimestamp.Equal(&created) || p.Spec.TTLSeconds != 3600 || p.Status.Message != "controller status" || p.Annotations["unrelated"] != "preserve" || len(p.Finalizers) != 1 {
		t.Fatalf("apply lost identity or metadata: %+v", p)
	}
	version := p.ResourceVersion
	p, err = e.Apply(ctx, r)
	if err != nil || p.ResourceVersion != version {
		t.Fatalf("same apply must not write: %v", err)
	}
}

func TestForeignEnvironmentAndBindingAreProtected(t *testing.T) {
	ctx := context.Background()
	r := request()
	name, _ := Name(r.RepositoryID, r.Number)
	for _, test := range []struct {
		name  string
		owner *metav1.OwnerReference
	}{
		{"labels only", nil},
		{"wrong UID", &metav1.OwnerReference{APIVersion: "v1", Kind: "Namespace", Name: DefaultSourceNamespace, UID: "other", Controller: boolPtr(true)}},
		{"wrong kind", &metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: DefaultSourceNamespace, UID: "source-uid", Controller: boolPtr(true)}},
		{"not controller", &metav1.OwnerReference{APIVersion: "v1", Kind: "Namespace", Name: DefaultSourceNamespace, UID: "source-uid", Controller: boolPtr(false)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: name, UID: "foreign", Labels: map[string]string{"app.kubernetes.io/managed-by": "previewforge-pr", "platform.previewforge.dev/repository-id": "123", "platform.previewforge.dev/pr-number": "42"}}}
			if test.owner != nil {
				p.OwnerReferences = []metav1.OwnerReference{*test.owner}
			}
			e, c := fixture(t, p)
			if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
				t.Fatal(err)
			}
			version := p.ResourceVersion
			if _, err := e.Apply(ctx, r); !errors.Is(err, ErrForeign) {
				t.Fatalf("foreign apply: %v", err)
			}
			if _, err := e.Delete(ctx, r.RepositoryID, r.Number); !errors.Is(err, ErrForeign) {
				t.Fatalf("foreign delete: %v", err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil || p.ResourceVersion != version || !p.DeletionTimestamp.IsZero() {
				t.Fatalf("foreign object changed: %v", err)
			}
		})
	}
	e, _ := fixture(t)
	e.Source.UID = "replaced-source"
	if _, err := e.Apply(ctx, r); !errors.Is(err, ErrBinding) {
		t.Fatalf("wrong binding must fail: %v", err)
	}
	e.Source.Namespace = "missing-source"
	if _, err := e.Delete(ctx, r.RepositoryID, r.Number); !errors.Is(err, ErrBinding) {
		t.Fatalf("missing source must not masquerade as deleted environment: %v", err)
	}
}

func TestDeletePreconditionsAndRecreatedIdentity(t *testing.T) {
	ctx := context.Background()
	e, c := fixture(t)
	r := request()
	p, err := e.Apply(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	calls, sawConflict := 0, false
	e.Client = interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		calls++
		options := &client.DeleteOptions{}
		for _, option := range opts {
			option.ApplyToDelete(options)
		}
		current := p.DeepCopy()
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), current); err != nil {
			return err
		}
		if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != p.UID || options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion != current.ResourceVersion {
			t.Fatal("delete omitted fresh UID/resourceVersion")
		}
		if calls == 1 {
			current.Annotations["concurrent"] = "change"
			if err := c.Update(ctx, current); err != nil {
				return err
			}
		}
		err := c.Delete(ctx, obj, opts...)
		if calls == 1 {
			sawConflict = apierrors.IsConflict(err)
		}
		return err
	}})
	if _, err := e.Delete(ctx, r.RepositoryID, r.Number); err != nil || calls != 2 || !sawConflict {
		t.Fatalf("same UID conflict must safely retry: calls=%d conflict=%v err=%v", calls, sawConflict, err)
	}
	e.Client = c
	if _, err := e.Delete(ctx, r.RepositoryID, r.Number); err != nil {
		t.Fatal(err)
	}
	replacement, err := e.Apply(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// Fake UID allocation is deterministic; model a genuine replacement UID explicitly.
	replacement.UID = "replacement-uid"
	if err := c.Update(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	deleted, err := e.Deleted(ctx, r.RepositoryID, r.Number, p.UID)
	if err != nil || !deleted {
		t.Fatalf("old UID must be considered gone without touching replacement: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(replacement), replacement); err != nil || !replacement.DeletionTimestamp.IsZero() {
		t.Fatalf("replacement changed: %v", err)
	}
}

func TestReadyRequiresCurrentGenerationAndRoute(t *testing.T) {
	p := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Generation: 2}, Status: platformv1.PreviewEnvironmentStatus{Phase: platformv1.PhaseReady, ObservedGeneration: 2, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
	if Ready(p) {
		t.Fatal("stale Ready condition was accepted")
	}
	p.Status.Conditions[0].ObservedGeneration = 2
	if !Ready(p) {
		t.Fatal("current internal rollout should be ready")
	}
	p.Spec.Ingress = &platformv1.IngressSpec{Host: "pr.example.test", ClassName: "traefik"}
	if Ready(p) {
		t.Fatal("missing current route was accepted")
	}
	p.Status.URL = "http://pr.example.test"
	if !Ready(p) {
		t.Fatal("current route should be ready")
	}
	p.Status.ObservedGeneration = 1
	if Ready(p) {
		t.Fatal("stale observed generation was accepted")
	}
}

func TestMutableImageAndInvalidIdentityAreRejected(t *testing.T) {
	e, _ := fixture(t)
	r := request()
	r.Image = "nginx:latest"
	if _, err := e.Apply(context.Background(), r); err == nil {
		t.Fatal("mutable image was accepted")
	}
	if _, err := Name(0, 42); err == nil {
		t.Fatal("missing repository identity was accepted")
	}
}

func boolPtr(value bool) *bool { return &value }
