#!/usr/bin/env bash
# Shared ownership checks for the disposable Bash e2e cluster.
# Keep creation, log export and deletion on the same local container provider.
export KIND_EXPERIMENTAL_PROVIDER=docker
pf_validate_cluster() {
  [[ ${PREVIEWFORGE_TEST_CLUSTER:-} =~ ^previewforge-test-[a-z0-9][a-z0-9-]{0,31}$ ]] || {
    echo 'Expected a fresh previewforge-test-* cluster name' >&2; return 1;
  }
  [[ ${PREVIEWFORGE_RUN_TOKEN:-} =~ ^[a-f0-9]{32}$ ]] || {
    echo 'Expected a per-run ownership token' >&2; return 1;
  }
}

pf_local_docker() {
  [[ -z ${DOCKER_HOST:-} || "$DOCKER_HOST" =~ ^(unix|npipe):// ]] || {
    echo 'Remote DOCKER_HOST is refused' >&2; return 1;
  }
  if ! PF_DOCKER_ENDPOINT=$(docker context inspect --format '{{.Endpoints.docker.Host}}'); then
    printf 'Docker context inspection failed: %s\n' "$PF_DOCKER_ENDPOINT" >&2
    return 1
  fi
  [[ "$PF_DOCKER_ENDPOINT" =~ ^(unix|npipe):// ]] || {
    echo 'A local Docker context is required' >&2; return 1;
  }
}

pf_run_directory() {
  printf '%s/bin/runs/%s' "$PWD" "$PREVIEWFORGE_TEST_CLUSTER"
}

# Called only after checking that the cluster and run directory do not exist.
# Atomic mkdir prevents another invocation from claiming the same directory.
pf_claim_cluster() {
  local directory
  pf_validate_cluster || return 1
  directory=$(pf_run_directory)
  mkdir -p "$PWD/bin/runs" || return 1
  (umask 077; mkdir "$directory") || return 1
  printf '%s\n' "$PF_DOCKER_ENDPOINT" > "$directory/.docker-endpoint" || return 1
  printf '%s\n' "${DOCKER_HOST:-}" > "$directory/.docker-host" || return 1
  printf '%s\n' "$PREVIEWFORGE_RUN_TOKEN" > "$directory/.cluster-owner" || return 1
}

pf_assert_owned() {
  local directory
  pf_validate_cluster || return 1
  directory=$(pf_run_directory)
  [[ -f "$directory/.cluster-owner" && ! -L "$directory" && ! -L "$directory/.cluster-owner" ]] || {
    echo 'No ownership claim for this run; refusing cluster access/cleanup' >&2; return 1;
  }
  [[ $(cat "$directory/.cluster-owner") == "$PREVIEWFORGE_RUN_TOKEN" ]] || {
    echo 'Ownership token mismatch; refusing cluster access/cleanup' >&2; return 1;
  }
  pf_local_docker || return 1
  [[ $(cat "$directory/.docker-endpoint") == "$PF_DOCKER_ENDPOINT" && $(cat "$directory/.docker-host") == "${DOCKER_HOST:-}" ]] || {
    echo 'Docker endpoint changed; refusing cluster access/cleanup' >&2; return 1;
  }
}

pf_cleanup_cluster() {
  local directory clusters existing
  pf_assert_owned || return 1
  directory=$(pf_run_directory)
  # A failed create may leave nodes without a kubeconfig. The claim is written
  # before create, so partial creation is also covered by this exact-name delete.
  if command -v timeout >/dev/null; then
    timeout 90s kind delete cluster --name "$PREVIEWFORGE_TEST_CLUSTER" || return 1
  else
    kind delete cluster --name "$PREVIEWFORGE_TEST_CLUSTER" || return 1
  fi
  clusters=$(kind get clusters) || return 1
  while IFS= read -r existing; do
    [[ "$existing" != "$PREVIEWFORGE_TEST_CLUSTER" ]] || {
      echo 'Test cluster still exists after cleanup' >&2; return 1;
    }
  done <<< "$clusters"
  printf 'PASS: deleted %s; absence verified\n' "$PREVIEWFORGE_TEST_CLUSTER" > "$directory/cleanup.txt"
  rm -- "$directory/.cluster-owner" || return 1
  echo "Cleaned only $PREVIEWFORGE_TEST_CLUSTER"
}
