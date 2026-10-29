"""Install, upgrade, rollback and uninstall the packaged chart in disposable kind."""
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time


def main():
    kubeconfig, context, cluster, directory, helm, admissioncert = sys.argv[1:]
    if context != "kind-" + cluster or not cluster.startswith("previewforge-test-"):
        raise SystemExit("Helm lifecycle requires a disposable previewforge-test context")
    directory = Path(directory)
    root = Path(__file__).resolve().parent.parent
    if directory.resolve() != (root / "bin" / "runs" / cluster).resolve() or Path(kubeconfig).resolve() != (directory / "kubeconfig").resolve():
        raise SystemExit("Run directory/kubeconfig must belong to this project invocation")
    token = os.environ.get("PREVIEWFORGE_RUN_TOKEN", "")
    if not token or (directory / ".cluster-owner").read_text().strip() != token:
        raise SystemExit("Missing or mismatched invocation claim")
    tool_env = dict(os.environ, **{key: str(root / ".cache" / "helm" / leaf) for key, leaf in [
        ("HELM_CACHE_HOME", "cache"), ("HELM_CONFIG_HOME", "config"), ("HELM_DATA_HOME", "data"), ("HELM_PLUGINS", "plugins"),
    ]})
    artifacts = directory / "artifacts"
    artifacts.mkdir(exist_ok=True)
    certs = directory / "helm-custom"
    certs.mkdir()  # A new run directory is mandatory; never reuse TLS.
    namespace, release = "previewforge-helm-system", "forge-custom"
    kubectl = ["kubectl", "--kubeconfig", kubeconfig, "--context", context, "--request-timeout=15s"]
    helm_args = [helm, "--kubeconfig", kubeconfig, "--kube-context", context]

    def run(args, obj=None, tool="kubectl"):
        base = kubectl if tool == "kubectl" else helm_args
        if obj is not None:
            args = args + ["--validate=false"]
        return subprocess.run(base + args, input=json.dumps(obj) if obj is not None else None,
                              capture_output=True, text=True, timeout=300, check=False, env=tool_env)

    def ok(args, obj=None, tool="kubectl"):
        result = run(args, obj, tool)
        if result.returncode:
            raise AssertionError(f"{tool} {args}: {result.stderr}")
        return result.stdout

    def get(kind, name, ns=None):
        return json.loads(ok((["-n", ns] if ns else []) + ["get", kind, name, "-o", "json"]))

    def save(name, value):
        (artifacts / name).write_text(json.dumps(value, indent=2), encoding="utf-8")

    def absent(kind, name):
        result = run(["get", kind, name])
        assert result.returncode and "NotFound" in result.stderr, result.stderr

    def stable_objects():
        current = get("previewenvironment", "helm-smoke")
        ns_obj = get("namespace", app_namespace)
        app = get("deployment", "app", app_namespace)
        assert current["metadata"]["uid"] == preview["metadata"]["uid"]
        assert ns_obj["metadata"]["uid"] == app_ns["metadata"]["uid"]
        assert app["metadata"]["uid"] == baseline_app["metadata"]["uid"]
        ok(["-n", app_namespace, "rollout", "status", "deployment/app", "--timeout=120s"])
        return {"crUID": current["metadata"]["uid"], "namespaceUID": ns_obj["metadata"]["uid"], "deploymentUID": app["metadata"]["uid"]}

    ok(["package", "charts/previewforge", "--destination", str(artifacts)], tool="helm")
    packages = list(artifacts.glob("previewforge-*.tgz"))
    assert len(packages) == 1
    package = str(packages[0])
    package_hash = hashlib.sha256(packages[0].read_bytes()).hexdigest()
    ok(["create", "namespace", namespace])
    initial_namespace = get("namespace", namespace)
    ok(["-n", namespace, "create", "configmap", "helm-canary", "--from-literal=preserve=yes"])
    canary = get("configmap", "helm-canary", namespace)
    subprocess.run([admissioncert, "--directory", str(certs), "--namespace", namespace], check=True)
    ok(["create", "-f", str(certs / "admission-tls.json")])
    values = json.loads((certs / "admission-helm-values.json").read_text())
    values["image"] = {"repository": "previewforge", "tag": cluster, "pullPolicy": "IfNotPresent"}
    values_path = certs / "values.json"
    values_path.write_text(json.dumps(values), encoding="utf-8")
    ok(["install", release, package, "--namespace", namespace, "-f", str(values_path), "--wait", "--timeout=180s"], tool="helm")
    crd = get("crd", "previewenvironments.platform.previewforge.dev")
    tls_uid = ok(["-n", namespace, "get", "secret", "previewforge-admission-tls", "-o", "jsonpath={.metadata.uid}"])
    role_before = get("clusterrole", "previewforge-manager-role")
    conflict = run(["install", "forge-collision", package, "--namespace", namespace, "-f", str(values_path)], tool="helm")
    assert conflict.returncode and "invalid ownership metadata" in conflict.stderr, conflict.stderr
    (artifacts / "helm-conflict.txt").write_text(conflict.stderr, encoding="utf-8")
    role_after = get("clusterrole", "previewforge-manager-role")
    assert all(role_before["metadata"][k] == role_after["metadata"][k] for k in ("uid", "resourceVersion"))

    preview = json.loads(ok(["create", "-f", "-", "-o", "json"], {
        "apiVersion": "platform.previewforge.dev/v1alpha1", "kind": "PreviewEnvironment",
        "metadata": {"name": "helm-smoke"}, "spec": {"image": "nginx:alpine", "ttlSeconds": 3600},
    }))
    ok(["wait", "--for=condition=Ready", "previewenvironment/helm-smoke", "--timeout=180s"])
    ready = get("previewenvironment", "helm-smoke")
    app_namespace = "forge-helm-smoke-" + hashlib.sha256(preview["metadata"]["uid"].encode()).hexdigest()[:12]
    assert ready["status"]["namespace"] == app_namespace
    app_ns = get("namespace", app_namespace)
    refs = [r for r in app_ns["metadata"]["ownerReferences"] if r.get("controller")]
    assert len(refs) == 1 and refs[0]["uid"] == preview["metadata"]["uid"] and refs[0]["name"] == "helm-smoke"
    assert refs[0]["apiVersion"] == preview["apiVersion"] and refs[0]["kind"] == "PreviewEnvironment"
    baseline_app = get("deployment", "app", app_namespace)
    baseline = stable_objects()
    ok(["-n", app_namespace, "run", "helm-http", "--image=busybox:1.37.0", "--restart=Never", "--command", "--", "sh", "-ec", f"wget -q -O- -T 30 http://app.{app_namespace}.svc.cluster.local"])
    for _ in range(90):
        if get("pod", "helm-http", app_namespace).get("status", {}).get("phase") == "Succeeded":
            break
        time.sleep(1)
    else:
        raise AssertionError("Helm-installed application did not return HTTP")
    body = ok(["-n", app_namespace, "logs", "helm-http"])
    assert "Welcome to nginx" in body
    (artifacts / "helm-http.txt").write_text(body, encoding="utf-8")

    identity = "system:serviceaccount:" + namespace + ":previewforge-controller-manager"
    service = get("service", "app", app_namespace)
    ok(["--as=" + identity, "replace", "--dry-run=server", "-f", "-"], service)
    foreign_name = "forge-helm-foreign"
    ok(["create", "namespace", foreign_name])
    candidate = copy.deepcopy(service)
    candidate["metadata"] = {k: service["metadata"][k] for k in ("name", "labels", "ownerReferences")}
    candidate["metadata"]["namespace"] = foreign_name
    candidate.pop("status", None)
    for field in ("clusterIP", "clusterIPs", "ipFamilies", "ipFamilyPolicy"):
        candidate["spec"].pop(field, None)
    denied = run(["--as=" + identity, "create", "-f", "-"], candidate)
    assert denied.returncode and "Forbidden" in denied.stderr and "ownership.platform.previewforge.dev" in denied.stderr, denied.stderr
    (artifacts / "helm-custom-identity-denied.txt").write_text(denied.stderr, encoding="utf-8")

    ok(["upgrade", release, package, "--namespace", namespace, "--reuse-values", "--set", "replicaCount=2", "--set", "resources.requests.cpu=120m", "--wait", "--timeout=180s"], tool="helm")
    assert get("deployment", "previewforge-controller-manager", namespace)["spec"]["replicas"] == 2
    ok(["-n", app_namespace, "scale", "deployment/app", "--replicas=3"])
    for _ in range(60):
        if get("deployment", "app", app_namespace)["spec"]["replicas"] == 1:
            break
        time.sleep(1)
    else:
        raise AssertionError("Upgraded operator did not repair drift")
    upgraded = stable_objects()
    ok(["rollback", release, "1", "--namespace", namespace, "--wait", "--timeout=180s"], tool="helm")
    assert get("deployment", "previewforge-controller-manager", namespace)["spec"]["replicas"] == 1
    rolled_back = stable_objects()
    assert ok(["-n", namespace, "get", "secret", "previewforge-admission-tls", "-o", "jsonpath={.metadata.uid}"]) == tls_uid
    save("helm-history.json", json.loads(ok(["history", release, "--namespace", namespace, "-o", "json"], tool="helm")))
    (artifacts / "helm-manifest.yaml").write_text(ok(["get", "manifest", release, "--namespace", namespace], tool="helm"), encoding="utf-8")

    ok(["delete", "previewenvironment", "helm-smoke", "--wait=true", "--timeout=120s"])
    absent("namespace", app_namespace)
    ok(["uninstall", release, "--namespace", namespace, "--wait", "--timeout=180s"], tool="helm")
    retained_crd = get("crd", "previewenvironments.platform.previewforge.dev")
    assert retained_crd["metadata"]["uid"] == crd["metadata"]["uid"] and retained_crd["spec"] == crd["spec"]
    assert get("namespace", namespace)["metadata"]["uid"] == initial_namespace["metadata"]["uid"]
    retained_canary = get("configmap", "helm-canary", namespace)
    assert all(retained_canary["metadata"][k] == canary["metadata"][k] for k in ("uid", "resourceVersion"))
    absent("clusterrole", "previewforge-manager-role")
    absent("validatingwebhookconfiguration", "previewforge-ownership")
    ok(["delete", "namespace", namespace, foreign_name, "--wait=true", "--timeout=120s"])
    absent("namespace", namespace)
    absent("namespace", foreign_name)
    save("helm-checks.json", {
        "namespace": namespace, "release": release, "baseline": baseline, "upgraded": upgraded, "rolledBack": rolled_back,
        "crdUID": crd["metadata"]["uid"], "crdRetained": True, "namespaceCanaryRetained": True,
        "conflictRefused": True, "customIdentityDenied": True, "fixtureCleanup": True, "tlsUIDStable": True,
        "package": packages[0].name, "packageSHA256": package_hash, "helmVersion": ok(["version", "--short"], tool="helm").strip(),
    })
    with (artifacts / "checks.txt").open("a", encoding="utf-8") as log:
        log.write("PASS: helm: packaged chart installed in a custom Namespace; upgrade/rollback preserved owner UIDs; custom admission denied foreign writes; uninstall retained CRD and Namespace canary\n")
    print("PASS: Helm install/upgrade/rollback/uninstall with live application, ownership and retention checks")


if __name__ == "__main__":
    main()
