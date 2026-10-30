#!/usr/bin/env bash
# Sourced after the network witnesses and monitoring are ready in the same run.
: "${directory:?}" "${kubeconfig:?}" "${context:?}"
pf_assert_owned
stressbench=${PREVIEWFORGE_STRESSBENCH:-stressbench}
command -v "$stressbench" >/dev/null || { echo 'Build stressbench first (make build or scripts/check.ps1)' >&2; exit 1; }
source_uid=$(k get namespace previewforge-system -o jsonpath='{.metadata.uid}')
"$stressbench" --kubeconfig "$kubeconfig" --context "$context" \
  --cluster "$PREVIEWFORGE_TEST_CLUSTER" --run-dir "$directory" \
  --source-namespace-uid "$source_uid" --operator-namespace previewforge-system \
  --image "previewforge-netprobe:$PREVIEWFORGE_TEST_CLUSTER" --environments 16 --workers 4
record_check load '16 warm-image environments; bounded four-writer burst, scale and drift; actual Pods/HTTP; raw latencies and sampled Prometheus metrics in stress-report.json'
record_check faults 'admission fail-closed/recovery without manager restart; manager downtime, queued updates/deletes, four elapsed TTLs, foreign canary preserved; all 20 fixture CRs/Namespaces absent'
