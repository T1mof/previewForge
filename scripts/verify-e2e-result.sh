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
[[ $(grep -c '^PASS: ' "$directory/artifacts/checks.txt") == 13 ]] || {
  echo 'Expected all 13 Kubernetes and isolation checks; incomplete report' >&2; exit 1;
}
for check in creation owners recreation drift restart http readiness finalizer ttl-active ttl-downtime manual-delete foreign-resources cluster-isolation; do
  grep -q "^PASS: $check: " "$directory/artifacts/checks.txt" || {
    echo "Missing completed check: $check" >&2; exit 1;
  }
done
[[ ! -e "$directory/.cluster-owner" ]] || { echo 'Cluster claim remains after cleanup' >&2; exit 1; }
echo 'Complete e2e report and cleanup evidence verified.'
