#!/usr/bin/env bash
# Sourced inside the claimed fresh cluster, after the immutable example is loaded.
: "${first_image:?}" "${directory:?}" "${python_bin:?}"
pf_assert_owned
guard_name=e2e-guardrails
cat <<EOF | k create -f -
apiVersion: platform.previewforge.dev/v1alpha1
kind: PreviewEnvironment
metadata:
  name: $guard_name
spec:
  image: $first_image
  port: 8080
  securityProfile: restricted
  resources:
    requests: {cpu: 200m, memory: 128Mi}
    limits: {cpu: 750m, memory: 512Mi}
  ttlSeconds: 3600
EOF
k wait --for=condition=Ready "previewenvironment/$guard_name" --timeout=180s
guard_ns=$(k get previewenvironment "$guard_name" -o jsonpath='{.status.namespace}')
guard_uid=$(k get previewenvironment "$guard_name" -o jsonpath='{.metadata.uid}')
k -n "$guard_ns" rollout status deployment/app --timeout=120s
k get namespace "$guard_ns" -o json > "$directory/artifacts/guardrails-namespace.json"
k -n "$guard_ns" get deployment app -o json > "$directory/artifacts/guardrails-deployment.json"
"$python_bin" - "$directory/artifacts" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
namespace = json.loads((root / 'guardrails-namespace.json').read_text())
assert namespace['metadata']['labels']['pod-security.kubernetes.io/enforce'] == 'restricted'
pod = json.loads((root / 'guardrails-deployment.json').read_text())['spec']['template']['spec']
assert not pod['automountServiceAccountToken'] and not pod['enableServiceLinks']
assert pod['securityContext']['runAsNonRoot'] and pod['securityContext']['runAsUser'] == 65532
assert pod['securityContext']['seccompProfile']['type'] == 'RuntimeDefault'
container = pod['containers'][0]
assert container['resources'] == {'requests': {'cpu': '200m', 'memory': '128Mi'}, 'limits': {'cpu': '750m', 'memory': '512Mi'}}
assert container['securityContext']['readOnlyRootFilesystem']
assert not container['securityContext']['allowPrivilegeEscalation']
assert container['securityContext']['capabilities']['drop'] == ['ALL']
PY

guard_quota_ready() { [[ $(k -n "$guard_ns" get resourcequota previewforge-budget -o jsonpath='{.status.hard.requests\.cpu}') == 8 ]]; }
wait_value 60 guard_quota_ready
for guard_resource in resourcequota/previewforge-budget limitrange/previewforge-defaults; do
  [[ $(k -n "$guard_ns" get "$guard_resource" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}') == "$guard_uid" ]]
done

guard_pod() {
  local name=$1 resources=${2:-default} privileged=${3:-false}
  cat <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: $name
  namespace: $guard_ns
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 65534
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: test
      image: busybox:1.37.0
      command: [sh, -c, 'sleep 1000']
      securityContext:
        privileged: $privileged
        allowPrivilegeEscalation: $privileged
        capabilities: {drop: [ALL]}
EOF
  case "$resources" in
    default) ;;
    quota) printf '      resources:\n        requests: {cpu: 750m, memory: 16Mi}\n        limits: {cpu: "1", memory: 16Mi}\n' ;;
    oversized) printf '      resources:\n        requests: {cpu: 100m, memory: 2Gi}\n        limits: {cpu: 500m, memory: 2Gi}\n' ;;
    *) echo 'Unknown test Pod resource configuration' >&2; return 1 ;;
  esac
}
guard_pod guard-defaults | k create -f -
k -n "$guard_ns" get pod guard-defaults -o json > "$directory/artifacts/guardrails-defaults.json"
"$python_bin" - "$directory/artifacts/guardrails-defaults.json" <<'PY'
import json, sys
resources = json.load(open(sys.argv[1]))['spec']['containers'][0]['resources']
assert resources == {'requests': {'cpu': '100m', 'memory': '64Mi'}, 'limits': {'cpu': '500m', 'memory': '256Mi'}}
PY
k -n "$guard_ns" delete pod guard-defaults --wait=true --timeout=90s
if guard_pod guard-privileged default true | k create -f - > "$directory/artifacts/guardrails-psa-rejected.txt" 2>&1; then
  echo 'Privileged Pod was admitted' >&2; exit 1
fi
grep -q 'violates PodSecurity' "$directory/artifacts/guardrails-psa-rejected.txt"
grep -q 'privileged' "$directory/artifacts/guardrails-psa-rejected.txt"
if guard_pod guard-oversized oversized | k create -f - > "$directory/artifacts/guardrails-limit-rejected.txt" 2>&1; then
  echo 'Pod over LimitRange maximum was admitted' >&2; exit 1
fi
grep -qi 'maximum memory usage per Container is 1Gi' "$directory/artifacts/guardrails-limit-rejected.txt"
for guard_i in {1..10}; do guard_pod "guard-budget-$guard_i" quota | k create -f -; done
if guard_pod guard-budget-11 quota | k create -f - > "$directory/artifacts/guardrails-quota-rejected.txt" 2>&1; then
  echo 'Pod over Namespace CPU quota was admitted' >&2; exit 1
fi
grep -q 'exceeded quota: previewforge-budget' "$directory/artifacts/guardrails-quota-rejected.txt"
grep -q 'requests.cpu' "$directory/artifacts/guardrails-quota-rejected.txt"
for guard_rejected in guard-privileged guard-oversized guard-budget-11; do
  [[ -z $(k -n "$guard_ns" get pod "$guard_rejected" --ignore-not-found -o name) ]]
done
k -n "$guard_ns" get resourcequota previewforge-budget -o json > "$directory/artifacts/guardrails-quota.json"
k -n "$guard_ns" get limitrange previewforge-defaults -o json > "$directory/artifacts/guardrails-limits.json"
guard_quota_uid=$(k -n "$guard_ns" get resourcequota previewforge-budget -o jsonpath='{.metadata.uid}')
guard_limit_uid=$(k -n "$guard_ns" get limitrange previewforge-defaults -o jsonpath='{.metadata.uid}')
# Test-admin deletion is confined to this fresh namespace. Controller watches
# must recreate the deleted objects with the same CR owner and new object UIDs.
k -n "$guard_ns" delete resourcequota/previewforge-budget limitrange/previewforge-defaults --wait=true --timeout=90s
guard_recreated() {
  local quota limit owner
  quota=$(k -n "$guard_ns" get resourcequota previewforge-budget -o jsonpath='{.metadata.uid}') || return 1
  limit=$(k -n "$guard_ns" get limitrange previewforge-defaults -o jsonpath='{.metadata.uid}') || return 1
  owner=$(k -n "$guard_ns" get resourcequota previewforge-budget -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}') || return 1
  [[ -n "$quota" && -n "$limit" && "$quota" != "$guard_quota_uid" && "$limit" != "$guard_limit_uid" && "$owner" == "$guard_uid" ]]
}
wait_value 60 guard_recreated
k -n "$guard_ns" patch resourcequota previewforge-budget --type=merge -p '{"spec":{"hard":{"requests.cpu":"7"}}}'
k -n "$guard_ns" patch limitrange previewforge-defaults --type=merge -p '{"spec":{"limits":[{"type":"Container","default":{"cpu":"900m","memory":"256Mi"},"defaultRequest":{"cpu":"100m","memory":"64Mi"},"min":{"cpu":"1m","memory":"16Mi"},"max":{"cpu":"1","memory":"1Gi"}}]}}'
guard_drift_repaired() {
  [[ $(k -n "$guard_ns" get resourcequota previewforge-budget -o jsonpath='{.spec.hard.requests\.cpu}') == 8 && $(k -n "$guard_ns" get limitrange previewforge-defaults -o jsonpath='{.spec.limits[0].default.cpu}') == 500m ]]
}
wait_value 60 guard_drift_repaired
k -n "$guard_ns" get resourcequota/previewforge-budget limitrange/previewforge-defaults -o json > "$directory/artifacts/guardrails-restored.json"
k delete previewenvironment "$guard_name" --wait=true --timeout=120s
k wait --for=delete namespace/"$guard_ns" --timeout=120s
printf '%s\n' 'PASS: restricted application Ready' 'PASS: LimitRange defaulting' 'PASS: privileged Pod rejected by PSA' 'PASS: oversized Pod rejected by LimitRange' 'PASS: eleventh CPU Pod rejected by ResourceQuota' 'PASS: rejected Pods absent' 'PASS: owned guardrails recreated and drift repaired' 'PASS: Namespace cleanup' > "$directory/artifacts/guardrails-checks.txt"
record_check guardrails 'real restricted application ready; LimitRange defaults/maximum, Pod Security and CPU quota enforced by admission; owned guardrails recreated and repaired; Namespace deleted'
