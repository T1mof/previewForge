#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/kind-lifecycle.sh
installer=${PREVIEWFORGE_INSTALLER:-kustomize}
[[ "$installer" == helm || "$installer" == kustomize ]] || { echo 'PREVIEWFORGE_INSTALLER must be helm or kustomize' >&2; exit 1; }
if [[ "$installer" == helm ]]; then
  command -v "${PREVIEWFORGE_HELM:-helm}" >/dev/null || { echo 'Helm 4.3.1 is required; see scripts/install-helm.sh' >&2; exit 1; }
fi
for tool in docker kind kubectl; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
kind_cni=${PREVIEWFORGE_KIND_CNI:-kindnet}
[[ "$kind_cni" == kindnet || "$kind_cni" == calico ]] || { echo 'PREVIEWFORGE_KIND_CNI must be kindnet or calico' >&2; exit 1; }
if [[ "$kind_cni" == calico ]]; then
  for tool in curl sha256sum; do command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }; done
fi
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
kind_args=(--name "$cluster" --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5 --kubeconfig "$kubeconfig")
if [[ "$kind_cni" == kindnet ]]; then kind_args+=(--wait 120s); fi
if [[ -n ${PREVIEWFORGE_DEMO_HTTP_PORT:-} || "$kind_cni" == calico ]]; then
  cat > "$(dirname "$kubeconfig")/kind.yaml" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
EOF
  if [[ "$kind_cni" == calico ]]; then
    printf 'networking:\n  disableDefaultCNI: true\n  podSubnet: "10.244.0.0/16"\n' >> "$(dirname "$kubeconfig")/kind.yaml"
  fi
  cat >> "$(dirname "$kubeconfig")/kind.yaml" <<'EOF'
nodes:
  - role: control-plane
EOF
  if [[ -n ${PREVIEWFORGE_DEMO_HTTP_PORT:-} ]]; then
    cat >> "$(dirname "$kubeconfig")/kind.yaml" <<EOF
    extraPortMappings:
      - containerPort: 30080
        hostPort: $PREVIEWFORGE_DEMO_HTTP_PORT
        listenAddress: "127.0.0.1"
        protocol: TCP
EOF
  fi
  kind_args+=(--config "$(dirname "$kubeconfig")/kind.yaml")
fi
kind create cluster "${kind_args[@]}"
k() { kubectl --kubeconfig "$kubeconfig" --context "$context" --request-timeout=15s "$@"; }
if [[ "$kind_cni" == calico ]]; then source scripts/install-calico.sh; fi
cp "$kubeconfig" bin/kind.kubeconfig
image="previewforge:$cluster"
overlay=$(dirname "$kubeconfig")
admissioncert=${PREVIEWFORGE_ADMISSIONCERT:-admissioncert}
"$admissioncert" --directory "$overlay"
cat > "$overlay/kustomization.yaml" <<EOF
resources:
  - ../../../config/hardened
  - admission-tls.json
images:
  - name: previewforge
    newTag: $cluster
patches:
  - path: operator-network.yaml
  - path: admission-ca.json
EOF
cp config/network-demo/operator-patch.yaml "$overlay/operator-network.yaml"
docker build -t "$image" .
kind load docker-image "$image" --name "$cluster"
if [[ "$installer" == helm ]]; then
  source scripts/install-helm-operator.sh
else
  k apply -k "$overlay"
fi
k wait --for=condition=Established crd/previewenvironments.platform.previewforge.dev --timeout=60s
k -n previewforge-system rollout status deployment/previewforge-admission --timeout=120s
k -n previewforge-system rollout status deployment/previewforge-controller-manager --timeout=120s
k apply -f config/samples/platform_v1alpha1_previewenvironment.yaml
k wait --for=condition=Ready previewenvironment/pr-42 --timeout=300s
k get previewenvironment/pr-42
echo "Cluster: $cluster; context: $context; kubeconfig: $kubeconfig"
echo "Remove only this test cluster with: kind delete cluster --name $cluster"
