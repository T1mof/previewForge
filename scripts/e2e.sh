#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/kind-lifecycle.sh
# A fresh cluster for every run; demo refuses existing clusters.
export PREVIEWFORGE_TEST_CLUSTER=${PREVIEWFORGE_TEST_CLUSTER:-previewforge-test-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')}
export PREVIEWFORGE_RUN_TOKEN=${PREVIEWFORGE_RUN_TOKEN:-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')}
pf_validate_cluster
context="kind-$PREVIEWFORGE_TEST_CLUSTER"
kubeconfig="$PWD/bin/runs/$PREVIEWFORGE_TEST_CLUSTER/kubeconfig"
directory=$(pf_run_directory)
[[ ! -e "$directory" ]] || { echo 'Refusing an existing run directory before installing cleanup traps' >&2; exit 1; }
ns=''
k() { kubectl --kubeconfig "$kubeconfig" --context "$context" --request-timeout=15s "$@"; }
record_check() { printf 'PASS: %s: %s\n' "$1" "$2" | tee -a "$directory/artifacts/checks.txt"; }
finish() {
  local code=$? cleanup_code=0 other_clusters remaining
  trap - EXIT INT TERM
  # This guard is required even for diagnostics: preflight may have rejected an
  # existing cluster with this name, or failed before claiming a new one.
  if [[ -e "$directory/.cluster-owner" ]]; then
    if pf_assert_owned; then
      mkdir -p "$directory/artifacts"
      k get namespaces,previewenvironments -o yaml > "$directory/artifacts/environments.yaml" 2>&1 || true
      k get deployments,pods,services -A -o wide > "$directory/artifacts/workloads.txt" 2>&1 || true
      k get pods -A -o yaml > "$directory/artifacts/pods.yaml" 2>&1 || true
      k get ingresses -A -o yaml > "$directory/artifacts/ingresses.yaml" 2>&1 || true
      k get events -A --sort-by=.lastTimestamp > "$directory/artifacts/events.txt" 2>&1 || true
      k -n previewforge-system logs deployment/previewforge-controller-manager --tail=300 > "$directory/artifacts/operator.log" 2>&1 || true
      k -n previewforge-ingress-system logs deployment/traefik --tail=200 > "$directory/artifacts/traefik.log" 2>&1 || true
      if command -v timeout >/dev/null; then
        timeout 60s kind export logs "$directory/artifacts/kind" --name "$PREVIEWFORGE_TEST_CLUSTER" || true
      fi
      if ! bash scripts/cleanup-kind.sh; then cleanup_code=1; fi
      if [[ $cleanup_code == 0 ]]; then
        remaining=$(kind get clusters) || cleanup_code=1
        other_clusters=$(printf '%s\n' "$remaining" | sed '/^$/d' | sort)
        if [[ "$other_clusters" != "$(sed '/^$/d' "$directory/.other-clusters" | sort)" ]]; then
          echo 'Other cluster names changed during the run' >&2
          cleanup_code=1
        else
          record_check cluster-isolation 'other cluster names unchanged; all kubectl commands used the run kubeconfig/context'
        fi
      fi
      [[ $cleanup_code == 0 ]] || code=1
      [[ ! -f "$directory/cleanup.txt" ]] || cp "$directory/cleanup.txt" "$directory/artifacts/cleanup.txt"
      printf 'E2E exit code: %s\nCleanup exit code: %s\nCluster: %s\nCommit: %s\n' "$code" "$cleanup_code" "$PREVIEWFORGE_TEST_CLUSTER" "${GITHUB_SHA:-local-unrecorded}" > "$directory/artifacts/result.txt"
    else
      code=1
    fi
  fi
  if [[ $code == 0 ]]; then echo 'E2E PASS, including test cluster cleanup.'; fi
  exit "$code"
}
# Install before demo: covers partial creation, image build, initial rollout,
# all assertions and ordinary interruption. CI supplies a second cleanup step.
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
bash scripts/kind-demo.sh
mkdir -p "$directory/artifacts"
ns=$(k get previewenvironment pr-42 -o jsonpath='{.status.namespace}')
k -n "$ns" rollout status deployment/app --timeout=180s
k -n "$ns" wait --for=condition=Ready pod -l app.kubernetes.io/name=app --timeout=180s
[[ $(k get previewenvironment pr-42 -o jsonpath='{.status.phase} {.status.readyReplicas}') == 'Ready 2' ]]
record_check creation 'PreviewEnvironment created; real application Pods and Deployment ready; CR Ready with two replicas'
deployment_uid=$(k -n "$ns" get deployment app -o jsonpath='{.metadata.uid}')
k create namespace e2e-unrelated
k -n e2e-unrelated create configmap canary --from-literal=sentinel=untouched
canary_uid=$(k -n e2e-unrelated get configmap canary -o jsonpath='{.metadata.uid}')
canary_rv=$(k -n e2e-unrelated get configmap canary -o jsonpath='{.metadata.resourceVersion}')
owner_uid=$(k get previewenvironment pr-42 -o jsonpath='{.metadata.uid}')
for kind in namespace deployment service; do
  if [[ $kind == namespace ]]; then args=(get namespace "$ns"); else args=(-n "$ns" get "$kind" app); fi
  actual_owner=$(k "${args[@]}" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}')
  [[ "$actual_owner" == "$owner_uid" ]] || { echo "Incorrect owner reference: $kind" >&2; exit 1; }
done
record_check owners 'Namespace, Deployment and Service controller owner UIDs match the CR'
wait_value() {
  local attempts=$1
  shift
  local i
  for ((i=0; i<attempts; i++)); do
    if "$@"; then return 0; fi
    sleep 2
  done
  echo "Timed out: $*" >&2
  return 1
}
new_deployment() {
  local uid
  uid=$(k -n "$ns" get deployment app -o jsonpath='{.metadata.uid}' 2>/dev/null) || return 1
  [[ -n "$uid" && "$uid" != "$deployment_uid" ]]
}
k -n "$ns" delete deployment app --wait=true --timeout=120s
wait_value 60 new_deployment
k -n "$ns" rollout status deployment/app --timeout=180s
k wait --for=condition=Ready previewenvironment/pr-42 --timeout=90s
record_check recreation 'deleted Deployment recreated with a new UID and real rollout ready'

k -n "$ns" scale deployment/app --replicas=7
replicas_restored() { [[ $(k -n "$ns" get deployment app -o jsonpath='{.spec.replicas}') == 2 ]]; }
wait_value 60 replicas_restored
record_check drift 'replica drift repaired'

stop_operator() {
  k -n previewforge-system scale deployment/previewforge-controller-manager --replicas=0
  k -n previewforge-system wait --for=delete pod -l app.kubernetes.io/name=previewforge-controller-manager --timeout=90s
}
start_operator() {
  k -n previewforge-system scale deployment/previewforge-controller-manager --replicas=1
  k -n previewforge-system rollout status deployment/previewforge-controller-manager --timeout=120s
}
old_operator_uid=$(k -n previewforge-system get pod -l app.kubernetes.io/name=previewforge-controller-manager -o jsonpath='{.items[0].metadata.uid}')
stop_operator
k patch previewenvironment pr-42 --type=merge -p '{"spec":{"replicas":3}}'
start_operator
restart_reconciled() { [[ $(k -n "$ns" get deployment app -o jsonpath='{.spec.replicas}') == 3 ]]; }
wait_value 60 restart_reconciled
k -n "$ns" rollout status deployment/app --timeout=180s
current_rollout_ready() {
  local values generation observed phase ready
  values=$(k get previewenvironment pr-42 -o jsonpath='{.metadata.generation} {.status.observedGeneration} {.status.phase} {.status.readyReplicas}') || return 1
  read -r generation observed phase ready <<< "$values"
  [[ "$observed" == "$generation" && "$phase" == Ready && "$ready" == 3 ]]
}
wait_value 60 current_rollout_ready
[[ $(k -n previewforge-system get pod -l app.kubernetes.io/name=previewforge-controller-manager -o jsonpath='{.items[0].metadata.uid}') != "$old_operator_uid" ]]
record_check restart 'new controller Pod after restart reconciled a spec change made during downtime; current generation Ready'

# HTTP request from a real pod checks Service selector, DNS, networking and the application.
service_ip=$(k -n "$ns" get service app -o jsonpath='{.spec.clusterIP}')
[[ -n $(k -n "$ns" get endpointslices -l kubernetes.io/service-name=app -o jsonpath='{.items[*].endpoints[?(@.conditions.ready==true)].addresses}') ]]
k -n "$ns" run smoke --restart=Never --image=busybox:1.37.0 --command -- sh -ec "wget -q -O- -T 30 http://app.$ns.svc.cluster.local; wget -q -O- -T 30 http://$service_ip"
smoke_succeeded() { [[ $(k -n "$ns" get pod smoke -o jsonpath='{.status.phase}') == Succeeded ]]; }
wait_value 60 smoke_succeeded
k -n "$ns" logs smoke | tee "$directory/artifacts/http.txt"
grep -q 'Welcome to nginx' "$directory/artifacts/http.txt"
record_check http 'real smoke Pod received application HTTP through Service DNS and ClusterIP; EndpointSlice ready'

# Optional API exposure is tested with a real Ingress controller in this run only.
source scripts/e2e-ingress.sh

create_preview() {
  local name=$1 ttl=$2 port=${3:-80}
  cat <<EOF | k apply -f -
apiVersion: platform.previewforge.dev/v1alpha1
kind: PreviewEnvironment
metadata:
  name: $name
spec:
  image: nginx:alpine
  port: $port
  ttlSeconds: $ttl
EOF
}
# A running container with a closed readiness port must not become CR Ready.
create_preview e2e-readiness 3600 81
readiness_ns=''
readiness_rejected() {
  readiness_ns=$(k get previewenvironment e2e-readiness -o jsonpath='{.status.namespace}') || return 1
  [[ -n "$readiness_ns" ]] || return 1
  [[ $(k -n "$readiness_ns" get pod -l app.kubernetes.io/name=app -o jsonpath='{.items[0].status.phase} {.items[0].status.conditions[?(@.type=="Ready")].status}') == 'Running False' && $(k get previewenvironment e2e-readiness -o jsonpath='{.status.phase} {.status.conditions[?(@.type=="Ready")].status}') == 'Deploying False' ]]
}
wait_value 60 readiness_rejected
k patch previewenvironment e2e-readiness --type=merge -p '{"spec":{"port":80}}'
k wait --for=condition=Ready previewenvironment/e2e-readiness --timeout=180s
k -n "$readiness_ns" rollout status deployment/app --timeout=180s
record_check readiness 'running but unready application blocked CR Ready; fixing readiness port completed a real rollout'
k delete previewenvironment e2e-readiness --wait=true --timeout=120s
k wait --for=delete namespace/"$readiness_ns" --timeout=120s
# Check finalizer behavior while another finalizer deliberately holds the Namespace.
create_preview e2e-finalizer 3600
k wait --for=condition=Ready previewenvironment/e2e-finalizer --timeout=180s
held_ns=$(k get previewenvironment e2e-finalizer -o jsonpath='{.status.namespace}')
k patch namespace "$held_ns" --type=merge -p '{"metadata":{"finalizers":["e2e.previewforge.dev/hold"]}}'
k delete previewenvironment e2e-finalizer --wait=false
namespace_terminating() { [[ -n $(k get namespace "$held_ns" -o jsonpath='{.metadata.deletionTimestamp}') ]]; }
wait_value 30 namespace_terminating
[[ $(k get previewenvironment e2e-finalizer -o jsonpath='{.metadata.finalizers}') == *platform.previewforge.dev/cleanup* ]]
[[ -n $(k get previewenvironment e2e-finalizer -o jsonpath='{.metadata.deletionTimestamp}') ]]
# Remove only the finalizer added by this test; the operator must finish cleanup.
k patch namespace "$held_ns" --type=json -p '[{"op":"test","path":"/metadata/finalizers/0","value":"e2e.previewforge.dev/hold"},{"op":"remove","path":"/metadata/finalizers/0"}]'
k wait --for=delete previewenvironment/e2e-finalizer --timeout=120s
k wait --for=delete namespace/"$held_ns" --timeout=120s
record_check finalizer 'CR cleanup finalizer held until Namespace deletion completed after releasing the test finalizer'

create_preview e2e-ttl-active 3600
k wait --for=condition=Ready previewenvironment/e2e-ttl-active --timeout=180s
active_ttl_ns=$(k get previewenvironment e2e-ttl-active -o jsonpath='{.status.namespace}')
k patch previewenvironment e2e-ttl-active --type=merge -p '{"spec":{"ttlSeconds":1}}'
k wait --for=delete previewenvironment/e2e-ttl-active --timeout=120s
k wait --for=delete namespace/"$active_ttl_ns" --timeout=120s
record_check ttl-active 'TTL with running controller removed the CR and fully deleted its Namespace'

# The TTL wakeup must survive operator downtime, including a passed deadline.
create_preview e2e-ttl 3600
k wait --for=condition=Ready previewenvironment/e2e-ttl --timeout=180s
ttl_ns=$(k get previewenvironment e2e-ttl -o jsonpath='{.status.namespace}')
stop_operator
k patch previewenvironment e2e-ttl --type=merge -p '{"spec":{"ttlSeconds":1}}'
sleep 2
start_operator
k wait --for=delete previewenvironment/e2e-ttl --timeout=120s
k wait --for=delete namespace/"$ttl_ns" --timeout=120s
record_check ttl-downtime 'TTL deadline passed during controller downtime; restart removed CR and fully deleted Namespace'

create_preview e2e-manual-delete 3600
k wait --for=condition=Ready previewenvironment/e2e-manual-delete --timeout=180s
manual_ns=$(k get previewenvironment e2e-manual-delete -o jsonpath='{.status.namespace}')
k delete previewenvironment e2e-manual-delete --wait=true --timeout=120s
k wait --for=delete namespace/"$manual_ns" --timeout=120s
k delete previewenvironment pr-42 --wait=true --timeout=120s
k wait --for=delete namespace/"$ns" --timeout=120s
record_check manual-delete 'manual deletion fully cleaned application Namespaces'

# Put a foreign Namespace at the exact calculated name while the operator is stopped.
# Forged labels still must not authorize adoption or deletion.
stop_operator
create_preview e2e-foreign 3600
foreign_cr_uid=$(k get previewenvironment e2e-foreign -o jsonpath='{.metadata.uid}')
uid_hash=$(printf '%s' "$foreign_cr_uid" | sha256sum)
foreign_ns="forge-e2e-foreign-${uid_hash:0:12}"
k create namespace "$foreign_ns"
k label namespace "$foreign_ns" app.kubernetes.io/managed-by=previewforge "platform.previewforge.dev/owner-uid=$foreign_cr_uid"
foreign_ns_uid=$(k get namespace "$foreign_ns" -o jsonpath='{.metadata.uid}')
foreign_ns_rv=$(k get namespace "$foreign_ns" -o jsonpath='{.metadata.resourceVersion}')
start_operator
foreign_conflict() { [[ $(k get previewenvironment e2e-foreign -o jsonpath='{.status.phase}') == Failed ]]; }
wait_value 30 foreign_conflict
k delete previewenvironment e2e-foreign --wait=false
foreign_cleanup_blocked() {
  [[ -n $(k get previewenvironment e2e-foreign -o jsonpath='{.metadata.deletionTimestamp}') && $(k get previewenvironment e2e-foreign -o jsonpath='{.metadata.finalizers}') == *platform.previewforge.dev/cleanup* ]]
}
wait_value 30 foreign_cleanup_blocked
sleep 3
[[ $(k get namespace "$foreign_ns" -o jsonpath='{.metadata.uid}') == "$foreign_ns_uid" ]]
[[ $(k get namespace "$foreign_ns" -o jsonpath='{.metadata.resourceVersion}') == "$foreign_ns_rv" ]]
[[ -z $(k get namespace "$foreign_ns" -o jsonpath='{.metadata.deletionTimestamp}') ]]
[[ $(k -n e2e-unrelated get configmap canary -o jsonpath='{.metadata.uid}') == "$canary_uid" ]]
[[ $(k -n e2e-unrelated get configmap canary -o jsonpath='{.metadata.resourceVersion}') == "$canary_rv" ]]
record_check foreign-resources 'foreign Namespace with forged labels refused adoption/deletion; unrelated ConfigMap UID/resourceVersion preserved'
echo 'Kubernetes scenarios completed; cluster cleanup must still succeed.'
echo "Diagnostics will be saved, then only $PREVIEWFORGE_TEST_CLUSTER will be deleted."
