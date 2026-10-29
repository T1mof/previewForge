#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/kind-lifecycle.sh
pf_validate_cluster
directory=$(pf_run_directory)
grep -Fx 'E2E exit code: 0' "$directory/artifacts/result.txt"
grep -Fx 'Cleanup exit code: 0' "$directory/artifacts/result.txt"
grep -Fx "Cluster: $PREVIEWFORGE_TEST_CLUSTER" "$directory/artifacts/result.txt"
grep -Fx "PASS: deleted $PREVIEWFORGE_TEST_CLUSTER; absence verified" "$directory/artifacts/cleanup.txt"
expected=19
if [[ ${PREVIEWFORGE_INSTALLER:-helm} == helm ]]; then expected=20; fi
[[ $(grep -c '^PASS: ' "$directory/artifacts/checks.txt") == "$expected" ]] || {
  echo "Expected all $expected Kubernetes and isolation checks; incomplete report" >&2; exit 1;
}
if [[ $expected == 20 ]]; then
  grep -q '^PASS: helm: ' "$directory/artifacts/checks.txt"
  [[ -s "$directory/artifacts/helm-checks.json" ]] || { echo 'Missing Helm runtime evidence' >&2; exit 1; }
fi
for check in creation owners recreation drift restart http ingress pr-integration metrics guardrails network admission readiness finalizer ttl-active ttl-downtime manual-delete foreign-resources cluster-isolation; do
  grep -q "^PASS: $check: " "$directory/artifacts/checks.txt" || {
    echo "Missing completed check: $check" >&2; exit 1;
  }
done
[[ ! -e "$directory/.cluster-owner" ]] || { echo 'Cluster claim remains after cleanup' >&2; exit 1; }
echo 'Complete e2e report and cleanup evidence verified.'
