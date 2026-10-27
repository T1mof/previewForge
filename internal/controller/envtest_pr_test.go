//go:build integration && !windows

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	"github.com/T1mof/previewForge/internal/prenvironment"
)

func verifyEnvtestPRSource(t *testing.T, environment *envtest.Environment, admin client.Client, scheme *runtime.Scheme) {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{"namespace.yaml", "service_account.yaml", "role.yaml", "role_binding.yaml"} {
		file, err := os.Open(filepath.Join("..", "..", "config", "pr-deployer", name))
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
			object, err := runtime.Decode(serializer.NewCodecFactory(scheme).UniversalDeserializer(), raw.Raw)
			if err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := admin.Create(ctx, object.(client.Object)); err != nil {
				file.Close()
				t.Fatal(err)
			}
		}
		file.Close()
	}
	user, err := environment.AddUser(envtest.User{Name: "system:serviceaccount:previewforge-pr-source:previewforge-pr-deployer", Groups: []string{"system:authenticated", "system:serviceaccounts", "system:serviceaccounts:previewforge-pr-source"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deployer, err := client.New(user.Config(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("PR deployer RBAC", func(t *testing.T) {
		for _, check := range []struct {
			group, resource, verb, name string
			allowed                     bool
		}{
			{"", "namespaces", "get", prenvironment.DefaultSourceNamespace, true},
			{"", "namespaces", "get", "other-namespace", false},
			{"", "namespaces", "create", "", false},
			{"", "secrets", "get", "", false},
			{"apps", "deployments", "create", "", false},
			{"platform.previewforge.dev", "previewenvironments", "create", "", true},
			{"platform.previewforge.dev", "previewenvironments", "patch", "", true},
			{"platform.previewforge.dev", "previewenvironments", "delete", "", true},
			{"platform.previewforge.dev", "previewenvironments/status", "update", "", false},
			{"platform.previewforge.dev", "previewenvironments/finalizers", "update", "", false},
		} {
			eventually(t, func() error {
				resource, subresource, _ := strings.Cut(check.resource, "/")
				review := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{Group: check.group, Resource: resource, Subresource: subresource, Verb: check.verb, Name: check.name}}}
				if err := deployer.Create(ctx, review); err != nil {
					return err
				}
				if review.Status.Allowed != check.allowed {
					return fmt.Errorf("PR RBAC %s %s/%s %s: allowed=%v", check.verb, check.group, check.resource, check.name, review.Status.Allowed)
				}
				return nil
			})
		}
	})
	source := &corev1.Namespace{}
	if err := admin.Get(ctx, client.ObjectKey{Name: prenvironment.DefaultSourceNamespace}, source); err != nil {
		t.Fatal(err)
	}
	e := &prenvironment.Environments{Client: deployer, Source: prenvironment.Binding{Namespace: source.Name, UID: source.UID}}
	request := prenvironment.Request{RepositoryID: 111, Number: 42, Image: "ghcr.io/example/app@sha256:" + strings.Repeat("a", 64), Revision: strings.Repeat("b", 40), Port: 8080, Replicas: 1, TTLSeconds: 3600}
	t.Run("PR source owner and optimistic patch", func(t *testing.T) {
		p, err := e.Apply(ctx, request)
		if err != nil {
			t.Fatalf("deployer must create Namespace-owned CR without Namespace finalizer permission: %v", err)
		}
		uid, created := p.UID, p.CreationTimestamp
		request.Image = "ghcr.io/example/app@sha256:" + strings.Repeat("c", 64)
		p, err = e.Apply(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if p.UID != uid || !p.CreationTimestamp.Equal(&created) || p.Generation != 2 {
			t.Fatalf("real apply reset identity or failed to update spec: %+v", p)
		}
		if _, err := e.Delete(ctx, request.RepositoryID, request.Number); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Delete(ctx, request.RepositoryID, request.Number); err != nil {
			t.Fatalf("delete must be idempotent: %v", err)
		}
	})
	t.Run("PR missing source is not successful deletion", func(t *testing.T) {
		wrong := *e
		wrong.Source.Namespace = "missing-source"
		if _, err := wrong.Delete(ctx, 111, 99); !errors.Is(err, prenvironment.ErrBinding) {
			t.Fatalf("wrong cluster/source must fail closed: %v", err)
		}
	})
	t.Run("PR delete UID precondition protects replacement", func(t *testing.T) {
		request.Number = 43
		original, err := e.Apply(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		observed := &observedPRDelete{Client: deployer}
		wrapper := &hookedClient{Client: observed}
		wrapper.onDelete = func(ctx context.Context, obj client.Object) error {
			if err := admin.Delete(ctx, obj); err != nil {
				return err
			}
			replacement := &platformv1.PreviewEnvironment{ObjectMeta: *original.ObjectMeta.DeepCopy(), Spec: original.Spec}
			replacement.UID = ""
			replacement.ResourceVersion = ""
			replacement.CreationTimestamp = original.CreationTimestamp
			return admin.Create(ctx, replacement)
		}
		protected := *e
		protected.Client = wrapper
		if _, err := protected.Delete(ctx, request.RepositoryID, request.Number); !errors.Is(err, prenvironment.ErrReplaced) || !apierrors.IsConflict(observed.result) {
			t.Fatalf("real precondition must return 409 and retry must refuse replacement: API=%v result=%v", observed.result, err)
		}
		current := &platformv1.PreviewEnvironment{}
		if err := admin.Get(ctx, client.ObjectKeyFromObject(original), current); err != nil {
			t.Fatal(err)
		}
		if current.UID == original.UID || !current.DeletionTimestamp.IsZero() {
			t.Fatal("replacement was deleted or not replaced")
		}
		if err := admin.Delete(ctx, current); err != nil {
			t.Fatal(err)
		}
	})
}

type observedPRDelete struct {
	client.Client
	result error
}

func (c *observedPRDelete) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.result = c.Client.Delete(ctx, obj, opts...)
	return c.result
}
