//go:build charttest

// Package chart verifies the packaged manifests without contacting a cluster.
package chart

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

func helm(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	binary := os.Getenv("HELM_BINARY")
	if binary == "" {
		binary = "helm"
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HELM_CACHE_HOME="+filepath.Join(root, ".cache", "helm", "cache"), "HELM_CONFIG_HOME="+filepath.Join(root, ".cache", "helm", "config"), "HELM_DATA_HOME="+filepath.Join(root, ".cache", "helm", "data"), "HELM_PLUGINS="+filepath.Join(root, ".cache", "helm", "plugins"))
	return cmd.CombinedOutput()
}

func documents(t *testing.T, raw []byte) map[string]*unstructured.Unstructured {
	t.Helper()
	objects := map[string]*unstructured.Unstructured{}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	for {
		obj := &unstructured.Unstructured{}
		if err := decoder.Decode(obj); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if len(obj.Object) == 0 {
			continue
		}
		key := obj.GetKind() + "/" + obj.GetName()
		if objects[key] != nil {
			t.Fatalf("duplicate %s", key)
		}
		objects[key] = obj
	}
	return objects
}

func render(t *testing.T, namespace string, extra ...string) map[string]*unstructured.Unstructured {
	t.Helper()
	args := []string{"template", "forge", "charts/previewforge", "--namespace", namespace, "--kube-version", "1.37.0", "--include-crds"}
	args = append(args, extra...)
	raw, err := helm(t, args...)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, raw)
	}
	return documents(t, raw)
}

func field(t *testing.T, obj *unstructured.Unstructured, path ...string) any {
	t.Helper()
	if obj == nil {
		t.Fatal("required rendered object missing")
	}
	value, found, err := unstructured.NestedFieldNoCopy(obj.Object, path...)
	if err != nil || !found {
		t.Fatalf("missing %v: %v", path, err)
	}
	return value
}

func assertRBAC(t *testing.T, objects map[string]*unstructured.Unstructured) {
	t.Helper()
	for _, source := range []string{"rbac/role.yaml", "admission/role.yaml", "metrics-reader/role.yaml"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "config", source))
		if err != nil {
			t.Fatal(err)
		}
		for key, obj := range documents(t, raw) {
			if rendered := objects[key]; rendered != nil && !reflect.DeepEqual(obj.Object["rules"], rendered.Object["rules"]) {
				t.Fatalf("chart changes canonical RBAC: %s", key)
			}
		}
	}
}

func TestDefaultChart(t *testing.T) {
	if raw, err := helm(t, "lint", "charts/previewforge", "--strict"); err != nil {
		t.Fatalf("lint: %v\n%s", err, raw)
	}
	objects := render(t, "custom-system")
	assertRBAC(t, objects)
	if objects["CustomResourceDefinition/previewenvironments.platform.previewforge.dev"] == nil {
		t.Fatal("CRD missing")
	}
	for _, obj := range objects {
		switch obj.GetKind() {
		case "Namespace", "Secret", "PreviewEnvironment", "ValidatingAdmissionPolicy", "ValidatingWebhookConfiguration":
			t.Fatalf("unexpected default resource %s", obj.GetKind())
		}
		if obj.GetNamespace() != "" && obj.GetNamespace() != "custom-system" {
			t.Fatal("wrong namespace")
		}
	}
	raw, err := helm(t, "template", "forge", "charts/previewforge", "--namespace", "custom-system", "--kube-version", "1.37.0")
	if err != nil {
		t.Fatal(err)
	}
	if documents(t, raw)["CustomResourceDefinition/previewenvironments.platform.previewforge.dev"] != nil {
		t.Fatal("CRD must live in crds/, outside uninstall templates")
	}
}

func TestHardenedChartNamespaceAndCanonicalPolicies(t *testing.T) {
	for _, namespace := range []string{"previewforge-system", "custom-helm-system"} {
		t.Run(namespace, func(t *testing.T) {
			objects := render(t, namespace, "--set", "admission.enabled=true", "--set-string", "admission.tls.caBundle=ZmFrZQ==")
			assertRBAC(t, objects)
			identity := "system:serviceaccount:" + namespace + ":previewforge-controller-manager"
			for _, name := range []string{"namespaces", "children", "deployments", "services"} {
				raw, err := os.ReadFile(filepath.Join("..", "..", "config", "admission", name+".yaml"))
				if err != nil {
					t.Fatal(err)
				}
				key := "ValidatingAdmissionPolicy/previewforge-" + name + ".platform.previewforge.dev"
				source := documents(t, raw)[key]
				conditions := field(t, source, "spec", "matchConditions").([]any)
				conditions[0].(map[string]any)["expression"] = "request.userInfo.username == '" + identity + "'"
				if !reflect.DeepEqual(source.Object["spec"], objects[key].Object["spec"]) {
					t.Fatalf("canonical policy changed: %s", name)
				}
			}
			for _, obj := range objects {
				if obj.GetNamespace() != "" && obj.GetNamespace() != namespace {
					t.Fatal("wrong object namespace")
				}
				if obj.GetKind() == "Secret" || obj.GetKind() == "Namespace" {
					t.Fatal("chart must not own TLS or installation Namespace")
				}
			}
			config := objects["ValidatingWebhookConfiguration/previewforge-ownership"]
			hooks := field(t, config, "webhooks").([]any)
			hook := hooks[0].(map[string]any)
			if hook["clientConfig"].(map[string]any)["service"].(map[string]any)["namespace"] != namespace {
				t.Fatal("webhook Service namespace mismatch")
			}
			if hook["matchConditions"].([]any)[0].(map[string]any)["expression"] != "request.userInfo.username == '"+identity+"'" {
				t.Fatal("webhook identity mismatch")
			}
			deployment := objects["Deployment/previewforge-admission"]
			for _, name := range []string{"previewforge-admission", "previewforge-controller-manager"} {
				pod := field(t, objects["Deployment/"+name], "spec", "template", "spec").(map[string]any)
				security := pod["securityContext"].(map[string]any)
				if security["runAsNonRoot"] != true || security["seccompProfile"].(map[string]any)["type"] != "RuntimeDefault" {
					t.Fatal("Pod hardening lost")
				}
				container := pod["containers"].([]any)[0].(map[string]any)["securityContext"].(map[string]any)
				if container["allowPrivilegeEscalation"] != false || container["readOnlyRootFilesystem"] != true || !reflect.DeepEqual(container["capabilities"].(map[string]any)["drop"], []any{"ALL"}) {
					t.Fatal("container hardening lost")
				}
			}
			containers := field(t, deployment, "spec", "template", "spec", "containers").([]any)
			args := containers[0].(map[string]any)["args"].([]any)
			if !strings.Contains(strings.TrimSpace(toStrings(args)), "--operator-identity="+identity) {
				t.Fatal("server identity mismatch")
			}
		})
	}
}

func toStrings(values []any) string {
	var result []string
	for _, value := range values {
		result = append(result, value.(string))
	}
	return strings.Join(result, "\n")
}

func TestChartOptions(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	objects := render(t, "custom-system", "--set", "metrics.enabled=false", "--set-string", "image.digest="+digest)
	if objects["Service/previewforge-metrics"] != nil || objects["ClusterRole/previewforge-metrics-reader"] != nil {
		t.Fatal("disabled metrics still exposed")
	}
	containers := field(t, objects["Deployment/previewforge-controller-manager"], "spec", "template", "spec", "containers").([]any)
	if containers[0].(map[string]any)["image"] != "previewforge@"+digest {
		t.Fatal("digest ignored")
	}
	if !strings.Contains(toStrings(containers[0].(map[string]any)["args"].([]any)), "--metrics-bind-address=0") {
		t.Fatal("metrics server not disabled")
	}
	objects = render(t, "custom-system", "--set", "metrics.tlsSecretName=metrics-cert")
	if _, found, _ := unstructured.NestedFieldNoCopy(objects["Deployment/previewforge-controller-manager"].Object, "spec", "template", "spec", "volumes"); !found {
		t.Fatal("external metrics TLS not mounted")
	}
}

func TestRejectInvalidChartConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"--set", "replicaCount=0"}, {"--set", "admission.enabled=true"}, {"--set", "network.ingressNamespace=gateway"},
		{"--set", "network.ingressPodSelector=app=router"}, {"--set", "image.digest=latest"}, {"--set", "unexpected=true"},
		{"--set-string", "network.dnsPodSelector=bad\nselector"}, {"--kube-version", "1.36.0"}, {"--kube-version", "1.38.0"}, {"--namespace", "Invalid.ns"},
	} {
		base := []string{"template", "forge", "charts/previewforge", "--kube-version", "1.37.0"}
		base = append(base, args...)
		if raw, err := helm(t, base...); err == nil {
			t.Fatalf("accepted %v: %s", args, raw)
		}
	}
}

func TestPackagedChart(t *testing.T) {
	dir := t.TempDir()
	if raw, err := helm(t, "package", "charts/previewforge", "--destination", dir); err != nil {
		t.Fatalf("package: %v\n%s", err, raw)
	}
	packages, err := filepath.Glob(filepath.Join(dir, "previewforge-*.tgz"))
	if err != nil || len(packages) != 1 {
		t.Fatal("missing chart package")
	}
	f, err := os.Open(packages[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	archive := tar.NewReader(z)
	files := map[string][]byte{}
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(header.Name, "..") || strings.HasPrefix(header.Name, "/") {
			t.Fatal("unsafe package path")
		}
		if header.Typeflag == tar.TypeReg {
			raw, err := io.ReadAll(archive)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte("BEGIN PRIVATE KEY")) {
				t.Fatal("private key in package")
			}
			files[header.Name] = raw
		}
	}
	for _, path := range []string{"previewforge/Chart.yaml", "previewforge/values.schema.json", "previewforge/templates/admission.yaml", "previewforge/crds/platform.previewforge.dev_previewenvironments.yaml", "previewforge/files/generated/admission/children.yaml"} {
		if len(files[path]) == 0 {
			t.Fatalf("packaged chart missing %s", path)
		}
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "config/crd/bases/platform.previewforge.dev_previewenvironments.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n")), files["previewforge/crds/platform.previewforge.dev_previewenvironments.yaml"]) {
		t.Fatal("packaged CRD differs from canonical generated CRD")
	}
}
