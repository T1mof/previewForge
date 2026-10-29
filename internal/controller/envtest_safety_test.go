//go:build integration && !windows

package controller

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func envtestOperatorConfig(t *testing.T, env *envtest.Environment, admin client.Client, scheme *runtime.Scheme) *rest.Config {
	t.Helper()
	ctx := context.Background()
	if err := admin.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "previewforge-system"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"service_account.yaml", "role.yaml", "role_binding.yaml", "leader_election.yaml"} {
		file, err := os.Open(filepath.Join("..", "..", "config", "rbac", name))
		if err != nil {
			t.Fatal(err)
		}
		decoder := yamlutil.NewYAMLOrJSONDecoder(file, 4096)
		for {
			var raw runtime.RawExtension
			if err := decoder.Decode(&raw); err != nil {
				if err == io.EOF {
					break
				}
				file.Close()
				t.Fatal(err)
			}
			if len(raw.Raw) == 0 {
				continue
			}
			obj, err := runtime.Decode(serializer.NewCodecFactory(scheme).UniversalDeserializer(), raw.Raw)
			if err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := admin.Create(ctx, obj.(client.Object)); err != nil {
				file.Close()
				t.Fatal(err)
			}
		}
		file.Close()
	}
	// The client certificate authenticates as exactly the manifest ServiceAccount.
	user, err := env.AddUser(envtest.User{
		Name:   "system:serviceaccount:previewforge-system:previewforge-controller-manager",
		Groups: []string{"system:authenticated", "system:serviceaccounts", "system:serviceaccounts:previewforge-system"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	op, err := client.New(user.Config(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("RBAC allow and deny", func(t *testing.T) {
		for _, check := range []struct {
			group, resource, verb, namespace string
			allowed                          bool
		}{
			{"platform.previewforge.dev", "previewenvironments", "watch", "", true},
			{"platform.previewforge.dev", "previewenvironments", "delete", "", true},
			{"", "namespaces", "delete", "", true},
			{"apps", "deployments", "patch", "arbitrary-preview", true},
			{"", "services", "create", "arbitrary-preview", true},
			{"", "resourcequotas", "create", "arbitrary-preview", true},
			{"networking.k8s.io", "networkpolicies", "create", "arbitrary-preview", true},
			{"networking.k8s.io", "networkpolicies", "patch", "arbitrary-preview", true},
			{"networking.k8s.io", "networkpolicies", "delete", "arbitrary-preview", false},
			{"", "resourcequotas", "patch", "arbitrary-preview", true},
			{"", "limitranges", "watch", "arbitrary-preview", true},
			{"", "resourcequotas/status", "update", "arbitrary-preview", false},
			{"networking.k8s.io", "ingresses", "create", "arbitrary-preview", true},
			{"networking.k8s.io", "ingresses", "delete", "arbitrary-preview", true},
			{"networking.k8s.io", "ingresses/status", "update", "arbitrary-preview", false},
			{"coordination.k8s.io", "leases", "create", "previewforge-system", true},
			{"", "events", "create", "previewforge-system", true},
			{"", "events", "create", "arbitrary-preview", false},
			{"coordination.k8s.io", "leases", "create", "arbitrary-preview", false},
			{"", "secrets", "get", "previewforge-system", false},
			{"", "pods", "delete", "arbitrary-preview", false},
			{"rbac.authorization.k8s.io", "clusterroles", "create", "", false},
			{"authentication.k8s.io", "tokenreviews", "create", "", true},
			{"authorization.k8s.io", "subjectaccessreviews", "create", "", true},
		} {
			eventually(t, func() error {
				resource, subresource, _ := strings.Cut(check.resource, "/")
				review := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
					Group: check.group, Resource: resource, Subresource: subresource, Verb: check.verb, Namespace: check.namespace,
				}}}
				if err := op.Create(ctx, review); err != nil {
					return err
				}
				if review.Status.Allowed != check.allowed {
					return fmt.Errorf("RBAC %s %s/%s in %s: allowed=%v, want %v", check.verb, check.group, check.resource, check.namespace, review.Status.Allowed, check.allowed)
				}
				return nil
			})
		}
	})
	return user.Config()
}

func verifyEnvtestSafety(t *testing.T, ctx context.Context, admin, operator client.Client, scheme *runtime.Scheme) {
	newPreview := func(name string) *platformv1.PreviewEnvironment {
		p := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: name, Finalizers: []string{Finalizer}}, Spec: platformv1.PreviewEnvironmentSpec{Image: "nginx:alpine"}}
		if err := admin.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("real status conflict", func(t *testing.T) {
		p := newPreview("real-status-conflict")
		wrapper := &hookedClient{Client: operator}
		wrapper.onStatusPatch = func(ctx context.Context) error {
			latest := &platformv1.PreviewEnvironment{}
			if err := admin.Get(ctx, client.ObjectKeyFromObject(p), latest); err != nil {
				return err
			}
			latest.Status.Message = "concurrent API writer"
			return admin.Status().Update(ctx, latest)
		}
		r := &PreviewEnvironmentReconciler{Client: wrapper, APIReader: operator, Scheme: scheme}
		if err := r.updateStatus(ctx, p, platformv1.PhaseReady, "DeploymentReady", "stale status", 1); !apierrors.IsConflict(err) {
			t.Fatalf("expected HTTP 409: %v", err)
		}
		latest := &platformv1.PreviewEnvironment{}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(p), latest); err != nil {
			t.Fatal(err)
		}
		if latest.Status.Message != "concurrent API writer" {
			t.Fatal("concurrent status was overwritten")
		}
		if err := r.updateStatus(ctx, p, platformv1.PhaseReady, "DeploymentReady", "fresh status", 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("real TTL extension conflicts with delete", func(t *testing.T) {
		p := newPreview("real-ttl-extension")
		p.Spec.TTLSeconds = 1
		if err := admin.Update(ctx, p); err != nil {
			t.Fatal(err)
		}
		wrapper := &hookedClient{Client: operator}
		wrapper.onDelete = func(ctx context.Context, obj client.Object) error {
			latest := &platformv1.PreviewEnvironment{}
			if err := admin.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
				return err
			}
			latest.Spec.TTLSeconds = 3600
			return admin.Update(ctx, latest)
		}
		r := &PreviewEnvironmentReconciler{Client: wrapper, APIReader: operator, Scheme: scheme, Now: func() time.Time { return p.CreationTimestamp.Add(2 * time.Second) }}
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); !apierrors.IsConflict(err) {
			t.Fatalf("stale TTL delete should get HTTP 409: %v", err)
		}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			t.Fatal(err)
		}
		if !p.DeletionTimestamp.IsZero() || p.Spec.TTLSeconds != 3600 {
			t.Fatal("TTL extension did not protect CR")
		}
	})
	t.Run("real UID precondition prevents namespace delete", func(t *testing.T) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "uid-precondition"}}
		if err := admin.Create(ctx, ns); err != nil {
			t.Fatal(err)
		}
		wrongUID := types.UID("a-different-namespace-uid")
		if err := operator.Delete(ctx, ns, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &wrongUID}}); !apierrors.IsConflict(err) {
			t.Fatalf("wrong UID must get HTTP 409: %v", err)
		}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
			t.Fatal(err)
		}
		if !ns.DeletionTimestamp.IsZero() {
			t.Fatal("wrong-UID delete touched namespace")
		}
	})
	t.Run("real foreign namespace cleanup is refused", func(t *testing.T) {
		p := newPreview("real-foreign")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: NamespaceName(p)}}
		setLabels(ns, p) // forged labels must not authorize deletion
		if err := admin.Create(ctx, ns); err != nil {
			t.Fatal(err)
		}
		if err := admin.Delete(ctx, p); err != nil {
			t.Fatal(err)
		}
		r := &PreviewEnvironmentReconciler{Client: operator, APIReader: operator, Scheme: scheme}
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(p)}); err == nil {
			t.Fatal("foreign namespace deletion was accepted")
		}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
			t.Fatal(err)
		}
		if !ns.DeletionTimestamp.IsZero() {
			t.Fatal("foreign namespace was touched")
		}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
			t.Fatal(err)
		}
		if !controllerutil.ContainsFinalizer(p, Finalizer) {
			t.Fatal("blocked cleanup released finalizer")
		}
	})
}
