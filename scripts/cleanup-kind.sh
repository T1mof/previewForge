#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/kind-lifecycle.sh
pf_validate_cluster
directory=$(pf_run_directory)
# CI's always() fallback also runs when preflight rejected an existing cluster.
# Without a claim this invocation has no permission to delete anything.
if [[ ! -e "$directory/.cluster-owner" ]]; then
  echo 'No cluster was claimed by this run; cleanup skipped.'
  exit 0
fi
pf_cleanup_cluster
