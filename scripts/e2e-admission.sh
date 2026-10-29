#!/usr/bin/env bash
# The operator is paused so repair cannot hide an accidentally allowed mutation.
: "${directory:?}" "${python_bin:?}" "${ns:?}" "${kubeconfig:?}" "${context:?}"
pf_assert_owned
stop_operator
"$python_bin" scripts/check-admission.py "$kubeconfig" "$context" "$ns" "$directory/artifacts"
start_operator
k -n "$ns" rollout status deployment/app --timeout=180s
record_check admission 'actual SA requests denied despite RBAC allow; foreign resources unchanged; normal owned writes and Namespace cleanup allowed'
