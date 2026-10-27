package controller

import (
	"context"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func TestIngressLifecycle(t *testing.T) {
	ctx := context.Background()
	p := preview("ingress")
	p.Spec.Ingress = &platformv1.IngressSpec{Host: "pr-42.example.test", ClassName: "traefik"}
	r, c := fixture(t, p)
	get := func(obj client.Object) {
		t.Helper()
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			t.Fatal(err)
		}
	}
	update := func(obj client.Object) {
		t.Helper()
		if err := c.Update(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	reconcile(t, r, p.Name)
	reconcile(t, r, p.Name)
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: NamespaceName(p)}}
	get(ingress)
	get(p)
	if !ownedBy(ingress, p) || ingress.Spec.Rules[0].Host != p.Spec.Ingress.Host || *ingress.Spec.IngressClassName != "traefik" || p.Status.URL != "http://pr-42.example.test" {
		t.Fatalf("wrong route, owner or URL: %+v, %+v", ingress.Spec, p.Status)
	}
	// Port changes retain the named backend; hostname and TLS update together.
	p.Spec.Port = 8080
	p.Spec.Ingress.Host = "changed.example.test"
	p.Spec.Ingress.TLSSecretName = "preview-tls"
	update(p)
	reconcile(t, r, p.Name)
	get(ingress)
	get(p)
	backend := ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service
	if backend.Name != AppName || backend.Port.Name != "http" || ingress.Spec.TLS[0].Hosts[0] != "changed.example.test" || p.Status.URL != "https://changed.example.test" {
		t.Fatalf("incorrect TLS route: %+v, %s", ingress.Spec, p.Status.URL)
	}
	ingress.Spec.Rules[0].Host = "drift.example.test"
	update(ingress)
	reconcile(t, r, p.Name)
	get(ingress)
	if ingress.Spec.Rules[0].Host != "changed.example.test" {
		t.Fatal("route drift was not repaired")
	}
	version := ingress.ResourceVersion
	reconcile(t, r, p.Name)
	get(ingress)
	if ingress.ResourceVersion != version {
		t.Fatal("unchanged route must be idempotent")
	}
	if err := c.Delete(ctx, ingress); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	get(ingress)
	// A third-party finalizer must block Ready while the route is being disabled.
	ingress.Finalizers = []string{"test.previewforge.dev/hold"}
	update(ingress)
	get(p)
	p.Spec.Ingress = nil
	update(p)
	reconcile(t, r, p.Name)
	get(ingress)
	get(p)
	if ingress.DeletionTimestamp.IsZero() || p.Status.URL != "" || p.Status.Phase != platformv1.PhaseDeploying {
		t.Fatalf("pending deletion must clear URL and block Ready: %+v", p.Status)
	}
	ingress.Finalizers = nil
	update(ingress)
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(ingress), ingress); !apierrors.IsNotFound(err) {
		t.Fatalf("disabled route still exists: %v", err)
	}
}

func TestForeignIngressIsNotAdoptedOrDeleted(t *testing.T) {
	ctx := context.Background()
	p := preview("foreign-ingress")
	p.Spec.Ingress = &platformv1.IngressSpec{Host: "preview.example.test", ClassName: "traefik"}
	foreign := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{
		Name: AppName, Namespace: NamespaceName(p), UID: "foreign-uid",
		Labels: map[string]string{ManagedByLabel: "previewforge", OwnerUIDLabel: string(p.UID)},
	}}
	r, c := fixture(t, p, foreign)
	reconcile(t, r, p.Name)
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); err == nil {
		t.Fatal("enabled ingress must refuse a foreign route")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(foreign), foreign); err != nil {
		t.Fatal(err)
	}
	version := foreign.ResourceVersion
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	if p.Status.URL != "" {
		t.Fatal("ownership conflict must not advertise a URL")
	}
	p.Spec.Ingress = nil
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(foreign), foreign); err != nil {
		t.Fatal(err)
	}
	if foreign.ResourceVersion != version || !foreign.DeletionTimestamp.IsZero() {
		t.Fatal("disabling ingress changed a foreign object")
	}
}

func TestIngressDeleteConflictsWithConcurrentChange(t *testing.T) {
	ctx := context.Background()
	p := preview("ingress-delete-race")
	p.Spec.Ingress = &platformv1.IngressSpec{Host: "preview.example.test", ClassName: "traefik"}
	r, c := fixture(t, p)
	reconcile(t, r, p.Name)
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Ingress = nil
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	wrapper := &hookedClient{Client: c}
	wrapper.onDelete = func(ctx context.Context, obj client.Object) error {
		current := &networkingv1.Ingress{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
			return err
		}
		current.Annotations = map[string]string{"concurrent": "change"}
		return c.Update(ctx, current)
	}
	r.Client = wrapper
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); !apierrors.IsConflict(err) {
		t.Fatalf("delete must enforce resourceVersion: %v", err)
	}
}
