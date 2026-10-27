#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/kind-lifecycle.sh
export PREVIEWFORGE_TEST_CLUSTER=${PREVIEWFORGE_TEST_CLUSTER:-previewforge-test-demo-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')}
export PREVIEWFORGE_RUN_TOKEN=${PREVIEWFORGE_RUN_TOKEN:-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')}
export PREVIEWFORGE_DEMO_HTTP_PORT=${PREVIEWFORGE_DEMO_HTTP_PORT:-8080}
pf_validate_cluster
command -v curl >/dev/null || { echo 'Missing prerequisite: curl' >&2; exit 1; }
directory=$(pf_run_directory)
[[ ! -e "$directory" ]] || { echo 'Refusing an existing run directory before installing cleanup traps' >&2; exit 1; }
context="kind-$PREVIEWFORGE_TEST_CLUSTER"
kubeconfig="$directory/kubeconfig"
k() { kubectl --kubeconfig "$kubeconfig" --context "$context" --request-timeout=15s "$@"; }
finish() {
  local code=$?
  trap - EXIT INT TERM
  if [[ $code != 0 && -e "$directory/.cluster-owner" ]]; then
    # Only the exact fresh cluster claimed by this run may be accessed or removed.
    if pf_assert_owned; then
      mkdir -p "$directory/artifacts"
      k get pods,ingresses -A -o wide > "$directory/artifacts/demo-failure.txt" 2>&1 || true
      k -n previewforge-ingress-system logs deployment/traefik --tail=100 > "$directory/artifacts/traefik.log" 2>&1 || true
      bash scripts/cleanup-kind.sh || code=1
    else
      code=1
    fi
  fi
  exit "$code"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
bash scripts/kind-demo.sh
pf_assert_owned
k apply -f config/ingress-demo/traefik.yaml
k -n previewforge-ingress-system rollout status deployment/traefik --timeout=180s
k patch previewenvironment pr-42 --type=merge -p '{"spec":{"ingress":{"host":"localhost","className":"previewforge-demo"}}}'
configured=false
for ((i=0; i<60; i++)); do
  if [[ $(k get previewenvironment pr-42 -o jsonpath='{.status.url}') == http://localhost ]]; then configured=true; break; fi
  sleep 2
done
[[ $configured == true ]] || { echo 'Timed out waiting for the configured Ingress URL' >&2; exit 1; }
mkdir -p "$directory/artifacts"
curl --fail --silent --show-error --retry 30 --retry-all-errors --retry-delay 2 --max-time 5 \
  --header 'Host: localhost' "http://127.0.0.1:$PREVIEWFORGE_DEMO_HTTP_PORT/" -o "$directory/artifacts/demo-http.txt"
grep -q 'Welcome to nginx' "$directory/artifacts/demo-http.txt"
url=http://localhost
if [[ $PREVIEWFORGE_DEMO_HTTP_PORT != 80 ]]; then url="$url:$PREVIEWFORGE_DEMO_HTTP_PORT"; fi
printf 'PASS: real HTTP through Ingress\nURL: %s\nCluster: %s\nContext: %s\nKubeconfig: %s\n' \
  "$url" "$PREVIEWFORGE_TEST_CLUSTER" "$context" "$kubeconfig" | tee "$directory/artifacts/demo.txt"
k get previewenvironment pr-42
echo 'Demo cluster is retained; the application still follows its TTL.'
echo "Cleanup: PREVIEWFORGE_TEST_CLUSTER=$PREVIEWFORGE_TEST_CLUSTER PREVIEWFORGE_RUN_TOKEN=\$(cat bin/runs/$PREVIEWFORGE_TEST_CLUSTER/.cluster-owner) bash scripts/cleanup-kind.sh"
