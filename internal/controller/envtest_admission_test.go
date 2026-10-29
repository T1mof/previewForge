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

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	webadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	boundary "github.com/T1mof/previewForge/internal/admission"
)

// This suite exercises actual admission and manifest RBAC without a kubelet or GC.
func TestAdmissionEnvtest(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{Paths: []string{filepath.Join("..", "..", "config", "admission", "server.yaml")}}}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := testScheme(t)
	if err := admissionv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	op, err := client.New(envtestOperatorConfig(t, env, admin, scheme), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reader := envtestAdmissionReader(t, env, admin, scheme)
	server := webhook.NewServer(webhook.Options{Host: env.WebhookInstallOptions.LocalServingHost, Port: env.WebhookInstallOptions.LocalServingPort, CertDir: env.WebhookInstallOptions.LocalServingCertDir})
	ownership, err := boundary.NewOwnership(reader, NamespaceName, boundary.OperatorIdentity)
	if err != nil {
		t.Fatal(err)
	}
	server.Register(boundary.Path, &webadmission.Webhook{Handler: ownership})
	serverCtx, cancelServer := context.WithCancel(ctx)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Start(serverCtx) }()
	t.Cleanup(func() {
		cancelServer()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("admission server did not stop")
		}
	})
	for _, name := range []string{"namespaces.yaml", "children.yaml", "deployments.yaml", "services.yaml", "bindings.yaml"} {
		f, err := os.Open(filepath.Join("..", "..", "config", "admission", name))
		if err != nil {
			t.Fatal(err)
		}
		decoder := yamlutil.NewYAMLOrJSONDecoder(f, 4096)
		for {
			var raw runtime.RawExtension
			if err := decoder.Decode(&raw); err != nil {
				if err == io.EOF {
					break
				}
				f.Close()
				t.Fatal(err)
			}
			if len(raw.Raw) == 0 {
				continue
			}
			obj, err := runtime.Decode(serializer.NewCodecFactory(scheme).UniversalDeserializer(), raw.Raw)
			if err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err := admin.Create(ctx, obj.(client.Object)); err != nil {
				f.Close()
				t.Fatalf("install %s: %v", name, err)
			}
		}
		f.Close()
	}
	// Envtest has no policy status controller. The kind gate checks observed
	// generation and type warnings; this suite evaluates the actual policies.
	// Wait for the binding cache using a request that RBAC permits.
	eventually(t, func() error {
		err := op.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "admission-cache-probe"}}, client.DryRunAll)
		if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "previewforge-namespaces") {
			return fmt.Errorf("admission not active: %v", err)
		}
		return nil
	})
	p := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: "admission"}, Spec: platformv1.PreviewEnvironmentSpec{Image: "nginx:alpine"}}
	if err := admin.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	spec, err := normalizedSpec(p.Spec)
	if err != nil {
		t.Fatal(err)
	}
	r := &PreviewEnvironmentReconciler{Scheme: scheme}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: NamespaceName(p)}}
	configureNamespace(ns, p, spec.SecurityProfile)
	if err := r.attachOwner(ns, p); err != nil {
		t.Fatal(err)
	}
	if err := op.Create(ctx, ns); err != nil {
		t.Fatalf("owned Namespace: %v", err)
	}
	denied := func(t *testing.T, policy string, err error) {
		t.Helper()
		if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "previewforge-"+policy+".platform.previewforge.dev") {
			t.Fatalf("expected %s admission denial, got %v", policy, err)
		}
	}
	deniedWebhook := func(t *testing.T, err error) {
		t.Helper()
		if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), boundary.WebhookName) {
			t.Fatalf("expected live ownership denial: %v", err)
		}
	}
	t.Run("live UID and computed Namespace name", func(t *testing.T) {
		for _, wrongUID := range []bool{false, true} {
			candidate := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "forge-impostor", Labels: ns.DeepCopy().Labels, OwnerReferences: ns.DeepCopy().OwnerReferences}}
			if wrongUID {
				candidate.OwnerReferences[0].UID = "stale-uid"
				candidate.Labels[OwnerUIDLabel] = "stale-uid"
			}
			deniedWebhook(t, op.Create(ctx, candidate, client.DryRunAll))
		}
	})
	t.Run("foreign namespace and adoption", func(t *testing.T) {
		foreign := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "forge-foreign", Labels: ns.Labels}}
		if err := admin.Create(ctx, foreign); err != nil {
			t.Fatal(err)
		}
		denied(t, "namespaces", op.Delete(ctx, foreign, client.DryRunAll))
		foreign.OwnerReferences = ns.OwnerReferences
		denied(t, "namespaces", op.Update(ctx, foreign, client.DryRunAll))
		denied(t, "namespaces", op.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "forge-labels-only", Labels: ns.Labels}}, client.DryRunAll))
	})
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: ns.Name}}
	configureDeployment(d, p, spec)
	if err := r.attachOwner(d, p); err != nil {
		t.Fatal(err)
	}
	// namespaceObject comes from the admission namespace cache.
	eventually(t, func() error { return op.Create(ctx, d, client.DryRunAll) })
	if err := op.Create(ctx, d); err != nil {
		t.Fatalf("owned Deployment: %v", err)
	}
	t.Run("dangerous deployment fields", func(t *testing.T) {
		for _, check := range []struct {
			name   string
			mutate func(*appsv1.Deployment)
		}{
			{"host network", func(d *appsv1.Deployment) { d.Spec.Template.Spec.HostNetwork = true }},
			{"serviceaccount token", func(d *appsv1.Deployment) { b := true; d.Spec.Template.Spec.AutomountServiceAccountToken = &b }},
			{"privileged", func(d *appsv1.Deployment) {
				b := true
				d.Spec.Template.Spec.Containers[0].SecurityContext.Privileged = &b
				d.Spec.Template.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = &b
			}},
			{"secret volume", func(d *appsv1.Deployment) {
				d.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: "secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "credential"}}}}
			}},
			{"secret env", func(d *appsv1.Deployment) {
				d.Spec.Template.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "credential"}}}}
			}},
			{"kubelet probe host", func(d *appsv1.Deployment) {
				d.Spec.Template.Spec.Containers[0].ReadinessProbe.TCPSocket.Host = "127.0.0.1"
			}},
			{"pod annotation", func(d *appsv1.Deployment) { d.Spec.Template.Annotations = map[string]string{"inject": "yes"} }},
		} {
			t.Run(check.name, func(t *testing.T) {
				candidate := d.DeepCopy()
				check.mutate(candidate)
				denied(t, "deployments", op.Update(ctx, candidate, client.DryRunAll))
			})
		}
	})
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: ns.Name}}
	configureService(svc, p, spec)
	if err := r.attachOwner(svc, p); err != nil {
		t.Fatal(err)
	}
	if err := op.Create(ctx, svc); err != nil {
		t.Fatalf("owned Service: %v", err)
	}
	t.Run("external service", func(t *testing.T) {
		candidate := svc.DeepCopy()
		candidate.Spec.ExternalIPs = []string{"192.0.2.1"}
		denied(t, "services", op.Update(ctx, candidate, client.DryRunAll))
	})
	t.Run("wrong child owner", func(t *testing.T) {
		candidate := svc.DeepCopy()
		candidate.OwnerReferences[0].UID = "different-uid"
		candidate.Labels[OwnerUIDLabel] = "different-uid"
		candidate.Spec.Selector[OwnerUIDLabel] = "different-uid"
		denied(t, "children", op.Update(ctx, candidate, client.DryRunAll))
	})
	t.Run("child in foreign namespace", func(t *testing.T) {
		candidate := svc.DeepCopy()
		candidate.Namespace = "forge-foreign"
		candidate.ResourceVersion = ""
		candidate.UID = ""
		candidate.Spec.ClusterIP = ""
		candidate.Spec.ClusterIPs = nil
		deniedWebhook(t, op.Create(ctx, candidate, client.DryRunAll))
	})
	t.Run("live Namespace owner is checked", func(t *testing.T) {
		candidate := ns.DeepCopy()
		candidate.OwnerReferences[0].UID = "another-owner"
		if err := admin.Update(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		deniedWebhook(t, op.Update(ctx, svc.DeepCopy(), client.DryRunAll))
		candidate.OwnerReferences = ns.OwnerReferences
		if err := admin.Update(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		ns = candidate
	})
	t.Run("restricted cannot downgrade", func(t *testing.T) {
		candidate := ns.DeepCopy()
		candidate.Labels[policyPrefix+"enforce"] = "restricted"
		if err := admin.Update(ctx, candidate); err != nil {
			t.Fatal(err)
		}
		candidate.Labels[policyPrefix+"enforce"] = "baseline"
		denied(t, "namespaces", op.Update(ctx, candidate, client.DryRunAll))
	})
	// Delete authorization uses oldObject and must permit owned Namespace cleanup.
	if err := op.Delete(ctx, ns, client.DryRunAll); err != nil {
		t.Fatalf("owned Namespace delete: %v", err)
	}
}

func envtestAdmissionReader(t *testing.T, env *envtest.Environment, admin client.Client, scheme *runtime.Scheme) client.Client {
	t.Helper()
	for _, name := range []string{"role.yaml", "server.yaml"} {
		f, err := os.Open(filepath.Join("..", "..", "config", "admission", name))
		if err != nil {
			t.Fatal(err)
		}
		decoder := yamlutil.NewYAMLOrJSONDecoder(f, 4096)
		for {
			var raw runtime.RawExtension
			if err := decoder.Decode(&raw); err != nil {
				if err == io.EOF {
					break
				}
				f.Close()
				t.Fatal(err)
			}
			if len(raw.Raw) == 0 {
				continue
			}
			obj, err := runtime.Decode(serializer.NewCodecFactory(scheme).UniversalDeserializer(), raw.Raw)
			if err != nil {
				f.Close()
				t.Fatal(err)
			}
			switch obj.GetObjectKind().GroupVersionKind().Kind {
			case "ServiceAccount", "ClusterRole", "ClusterRoleBinding":
				if err := admin.Create(context.Background(), obj.(client.Object)); err != nil {
					f.Close()
					t.Fatal(err)
				}
			}
		}
		f.Close()
	}
	user, err := env.AddUser(envtest.User{Name: "system:serviceaccount:previewforge-system:previewforge-admission", Groups: []string{"system:authenticated", "system:serviceaccounts", "system:serviceaccounts:previewforge-system"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := client.New(user.Config(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}
