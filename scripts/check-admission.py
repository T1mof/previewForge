"""Real API admission checks, invoked only by the claimed disposable kind e2e."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import time


def main():
    kubeconfig, context, namespace, output = sys.argv[1:]
    if not context.startswith("kind-previewforge-test-"):
        raise SystemExit("Admission checks require a disposable previewforge-test context")
    output = Path(output)
    base = ["kubectl", "--kubeconfig", kubeconfig, "--context", context, "--request-timeout=15s"]
    operator = ["--as=system:serviceaccount:previewforge-system:previewforge-controller-manager"]
    results = []

    def run(args, obj=None, op=False):
        return subprocess.run(base + (operator if op else []) + args,
                              input=json.dumps(obj) if obj is not None else None,
                              text=True, capture_output=True, check=False)

    def ok(args, obj=None, op=False):
        result = run(args, obj, op)
        if result.returncode:
            raise AssertionError(f"{args}: {result.stderr}")
        return result.stdout

    def get(kind, name, ns=None):
        return json.loads(ok((["-n", ns] if ns else []) + ["get", kind, name, "-o", "json"]))

    def deny(name, policy, args, obj=None):
        result = run(args, obj, op=True)
        (output / f"admission-denied-{name}.txt").write_text(result.stderr, encoding="utf-8")
        assert result.returncode != 0 and "Forbidden" in result.stderr, (name, result.stderr)
        expected = "ownership.platform.previewforge.dev" if policy == "ownership" else f"previewforge-{policy}.platform.previewforge.dev"
        assert expected in result.stderr, (name, result.stderr)
        results.append({"check": name, "policy": policy, "denied": True})

    policies = json.loads(ok(["get", "validatingadmissionpolicies", "-o", "json"]))
    own_policies = [p for p in policies["items"] if p["metadata"]["name"].startswith("previewforge-")]
    assert len(own_policies) == 4
    for p in own_policies:
        assert p["status"]["observedGeneration"] == p["metadata"]["generation"]
        assert not p["status"].get("typeChecking", {}).get("expressionWarnings"), p["status"]
    (output / "admission-policies.json").write_text(json.dumps(policies, indent=2), encoding="utf-8")
    (output / "admission-bindings.json").write_text(ok(["get", "validatingadmissionpolicybindings", "-o", "json"]), encoding="utf-8")
    (output / "admission-webhook.json").write_text(ok(["get", "validatingwebhookconfiguration", "previewforge-ownership", "-o", "json"]), encoding="utf-8")
    # Store real access-review responses, not just an assertion in our report.
    reviews = []
    for identity, verb, resource, group, ns, allowed in [
        ("previewforge-controller-manager", "patch", "namespaces", "", "", True),
        ("previewforge-controller-manager", "delete", "namespaces", "", "", True),
        ("previewforge-controller-manager", "create", "services", "", namespace, True),
        ("previewforge-controller-manager", "patch", "deployments", "apps", namespace, True),
        ("previewforge-admission", "get", "namespaces", "", "", True),
        ("previewforge-admission", "get", "previewenvironments", "platform.previewforge.dev", "", True),
        ("previewforge-admission", "patch", "namespaces", "", "", False),
        ("previewforge-admission", "get", "secrets", "", "previewforge-system", False),
    ]:
        review = {"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": {"resourceAttributes": {"verb": verb, "resource": resource, "group": group, "namespace": ns}}}
        username = "system:serviceaccount:previewforge-system:" + identity
        response = json.loads(ok(["--as=" + username, "create", "-f", "-", "-o", "json"], review))
        assert response["status"]["allowed"] == allowed, response
        reviews.append({"identity": username, "review": response})
    (output / "admission-rbac.json").write_text(json.dumps(reviews, indent=2), encoding="utf-8")
    owned = get("namespace", namespace)
    deployment = get("deployment", "app", namespace)
    service = get("service", "app", namespace)
    foreign_name = "forge-admission-foreign"
    foreign = {"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": foreign_name, "labels": owned["metadata"]["labels"]}}
    foreign["metadata"]["labels"] = {k: v for k, v in foreign["metadata"]["labels"].items() if k != "kubernetes.io/metadata.name"}
    ok(["create", "-f", "-"], foreign)
    before = get("namespace", foreign_name)
    forged = copy.deepcopy(before)
    forged["metadata"]["ownerReferences"] = owned["metadata"]["ownerReferences"]
    deny("namespace-adoption", "namespaces", ["replace", "-f", "-"], forged)
    deny("namespace-delete", "namespaces", ["delete", "namespace", foreign_name, "--wait=false"])
    after = get("namespace", foreign_name)
    assert before["metadata"]["uid"] == after["metadata"]["uid"]
    assert before["metadata"]["resourceVersion"] == after["metadata"]["resourceVersion"]
    (output / "admission-foreign-before.json").write_text(json.dumps(before), encoding="utf-8")
    (output / "admission-foreign-after.json").write_text(json.dumps(after), encoding="utf-8")

    def fresh(obj, ns):
        obj = copy.deepcopy(obj)
        obj["metadata"] = {k: obj["metadata"][k] for k in ("name", "labels", "ownerReferences")}
        obj["metadata"]["namespace"] = ns
        obj.pop("status", None)
        return obj

    foreign_service = fresh(service, foreign_name)
    for field in ("clusterIP", "clusterIPs", "ipFamilies", "ipFamilyPolicy"):
        foreign_service["spec"].pop(field, None)
    deny("foreign-child", "ownership", ["create", "-f", "-"], foreign_service)
    # A valid owned Namespace can be created and deleted by the operator.
    fixture_name = "e2e-admission"
    ok(["create", "-f", "-"], {"apiVersion": "platform.previewforge.dev/v1alpha1", "kind": "PreviewEnvironment", "metadata": {"name": fixture_name}, "spec": {"image": "nginx:alpine", "ttlSeconds": 3600}})
    fixture = get("previewenvironment", fixture_name)
    fixture_uid = fixture["metadata"]["uid"]
    fixture_owner = [{"apiVersion": fixture["apiVersion"], "kind": fixture["kind"], "name": fixture_name, "uid": fixture_uid, "controller": True, "blockOwnerDeletion": True}]
    temp_name = "forge-" + fixture_name + "-" + hashlib.sha256(fixture_uid.encode()).hexdigest()[:12]
    temp = fresh(owned, "")
    temp["metadata"].pop("namespace", None)
    temp["metadata"]["name"] = temp_name
    temp["metadata"]["labels"].pop("kubernetes.io/metadata.name", None)
    temp["metadata"]["labels"]["platform.previewforge.dev/owner-uid"] = fixture_uid
    temp["metadata"]["ownerReferences"] = fixture_owner
    ok(["create", "-f", "-"], temp, op=True)
    adopted = fresh(foreign_service, temp_name)
    adopted["metadata"]["labels"]["platform.previewforge.dev/owner-uid"] = fixture_uid
    adopted["spec"]["selector"]["platform.previewforge.dev/owner-uid"] = fixture_uid
    adopted["metadata"].pop("ownerReferences")
    ok(["create", "-f", "-"], adopted)
    existing = get("service", "app", temp_name)
    candidate = copy.deepcopy(existing)
    candidate["metadata"]["ownerReferences"] = fixture_owner
    deny("child-adoption", "children", ["replace", "-f", "-"], candidate)
    unchanged = get("service", "app", temp_name)
    assert existing["metadata"]["uid"] == unchanged["metadata"]["uid"]
    assert existing["metadata"]["resourceVersion"] == unchanged["metadata"]["resourceVersion"]
    # Dry-run evaluates admission without ever scheduling a dangerous candidate.
    for name, mutate in [
        ("host-network", lambda p: p.update(hostNetwork=True)),
        ("sa-token", lambda p: p.update(automountServiceAccountToken=True)),
        ("privileged", lambda p: p["containers"][0]["securityContext"].update(privileged=True, allowPrivilegeEscalation=True)),
        ("secret-volume", lambda p: p.update(volumes=[{"name": "credential", "secret": {"secretName": "credential"}}])),
        ("probe-host", lambda p: p["containers"][0]["readinessProbe"]["tcpSocket"].update(host="127.0.0.1")),
    ]:
        candidate = copy.deepcopy(deployment)
        mutate(candidate["spec"]["template"]["spec"])
        deny(name, "deployments", ["replace", "--dry-run=server", "-f", "-"], candidate)
    candidate = copy.deepcopy(service)
    candidate["spec"]["externalIPs"] = ["192.0.2.1"]
    deny("external-service", "services", ["replace", "--dry-run=server", "-f", "-"], candidate)
    # A normal reconcile-shaped workload remains admissible.
    ok(["replace", "--dry-run=server", "-f", "-"], deployment, op=True)
    ok(["replace", "--dry-run=server", "-f", "-"], service, op=True)
    # The independent webhook must fail closed while the operator is paused.
    ok(["-n", "previewforge-system", "scale", "deployment/previewforge-admission", "--replicas=0"])
    try:
        ok(["-n", "previewforge-system", "wait", "--for=delete", "pod", "-l", "app.kubernetes.io/name=previewforge-admission", "--timeout=90s"])
        result = run(["replace", "--dry-run=server", "-f", "-"], service, op=True)
        (output / "admission-fail-closed.txt").write_text(result.stderr, encoding="utf-8")
        assert result.returncode != 0 and 'failed calling webhook "ownership.platform.previewforge.dev"' in result.stderr, result.stderr
    finally:
        ok(["-n", "previewforge-system", "scale", "deployment/previewforge-admission", "--replicas=1"])
        ok(["-n", "previewforge-system", "rollout", "status", "deployment/previewforge-admission", "--timeout=120s"])
    ok(["replace", "--dry-run=server", "-f", "-"], service, op=True)
    ok(["delete", "namespace", temp_name, "--wait=false"], op=True)
    ok(["delete", "namespace", foreign_name, "--wait=false"])
    for name in (temp_name, foreign_name):
        for _ in range(90):
            result = run(["get", "namespace", name])
            if result.returncode and "NotFound" in result.stderr:
                break
            time.sleep(1)
        else:
            raise AssertionError(f"Namespace cleanup did not finish: {name}")
    ok(["delete", "previewenvironment", fixture_name, "--wait=true", "--timeout=30s"])
    (output / "admission-checks.json").write_text(json.dumps({"checks": results, "rbacAllows": True, "positiveWorkloads": True, "failClosed": True, "cleanup": True}, indent=2), encoding="utf-8")


if __name__ == "__main__":
    main()
