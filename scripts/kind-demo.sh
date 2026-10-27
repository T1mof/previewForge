#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/kind-lifecycle.sh
for tool in docker kind kubectl; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
pf_local_docker
docker info >/dev/null
if [[ -n ${PREVIEWFORGE_DEMO_HTTP_PORT:-} ]]; then
  [[ $PREVIEWFORGE_DEMO_HTTP_PORT =~ ^[1-9][0-9]{0,4}$ && $PREVIEWFORGE_DEMO_HTTP_PORT -le 65535 ]] || {
    echo 'PREVIEWFORGE_DEMO_HTTP_PORT must be a decimal port from 1 to 65535' >&2; exit 1;
  }
fi
cluster=${PREVIEWFORGE_TEST_CLUSTER:-previewforge-test-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')}
export PREVIEWFORGE_TEST_CLUSTER="$cluster"
export PREVIEWFORGE_RUN_TOKEN=${PREVIEWFORGE_RUN_TOKEN:-$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')}
pf_validate_cluster
context="kind-$cluster"
kubeconfig="$PWD/bin/runs/$cluster/kubeconfig"
clusters=$(kind get clusters)
while IFS= read -r existing; do
  [[ "$existing" != "$cluster" ]] || { echo "Refusing to reuse existing cluster $cluster" >&2; exit 1; }
done <<< "$clusters"
[[ ! -e "$(dirname "$kubeconfig")" ]] || { echo 'Refusing to overwrite an existing run directory' >&2; exit 1; }
pf_claim_cluster
printf '%s\n' "$clusters" > "$(dirname "$kubeconfig")/.other-clusters"
kind_args=(--name "$cluster" --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5 --kubeconfig "$kubeconfig" --wait 120s)
if [[ -n ${PREVIEWFORGE_DEMO_HTTP_PORT:-} ]]; then
  cat > "$(dirname "$kubeconfig")/kind.yaml" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30080
        hostPort: $PREVIEWFORGE_DEMO_HTTP_PORT
        listenAddress: "127.0.0.1"
        protocol: TCP
EOF
  kind_args+=(--config "$(dirname "$kubeconfig")/kind.yaml")
fi
kind create cluster "${kind_args[@]}"
cp "$kubeconfig" bin/kind.kubeconfig
image="previewforge:$cluster"
overlay=$(dirname "$kubeconfig")
cat > "$overlay/kustomization.yaml" <<EOF
resources:
  - ../../../config/default
images:
  - name: previewforge
    newTag: $cluster
EOF
docker build -t "$image" .
kind load docker-image "$image" --name "$cluster"
k() { kubectl --kubeconfig "$kubeconfig" --context "$context" --request-timeout=15s "$@"; }
k apply -k "$overlay"
k wait --for=condition=Established crd/previewenvironments.platform.previewforge.dev --timeout=60s
k -n previewforge-system rollout status deployment/previewforge-controller-manager --timeout=120s
k apply -f config/samples/platform_v1alpha1_previewenvironment.yaml
k wait --for=condition=Ready previewenvironment/pr-42 --timeout=180s
k get previewenvironment/pr-42
echo "Cluster: $cluster; context: $context; kubeconfig: $kubeconfig"
echo "Remove only this test cluster with: kind delete cluster --name $cluster"
