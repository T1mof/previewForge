#!/usr/bin/env bash
# Sourced only after kind-demo claims and creates a fresh disposable cluster.
: "${cluster:?}" "${kubeconfig:?}" "${context:?}"
pf_assert_owned
calico_directory="$(pf_run_directory)/calico"
mkdir "$calico_directory"
calico_source="$calico_directory/calico.yaml"
curl --fail --location --retry 3 --connect-timeout 15 --max-time 120 \
  https://raw.githubusercontent.com/projectcalico/calico/v3.33.0/manifests/calico.yaml -o "$calico_source"
printf '%s  %s\n' 2de8f47595fb9c41b3f47d7b767a1f8e72ecf84057af834738ff12689a234da5 "$calico_source" | sha256sum --check
cp config/network-demo/calico-patch.yaml "$calico_directory/patch.yaml"
cat > "$calico_directory/kustomization.yaml" <<'EOF'
resources: [calico.yaml]
images:
  - name: quay.io/calico/calico
    newTag: v3.33.0
    digest: sha256:4ebc20730127bc9e8bf1db5f5a2d8b58cf37fe0e891b3c7b5471992221d213e5
  - name: quay.io/calico/node
    newTag: v3.33.0
    digest: sha256:565d8db3200e443955557f8f28ff44ab8a4a456754bc74a19c4f2f3444af9b1c
  - name: quay.io/calico/third-party-cni-plugins
    newTag: v3.33.0
    digest: sha256:0f32f48279484c0d6f0e0d0e767b2d0e1a6c6ca34cb3421a61b989d992f3df41
patches:
  - path: patch.yaml
EOF
k apply --server-side --field-manager=previewforge-calico -k "$calico_directory"
k -n kube-system rollout status daemonset/calico-node --timeout=600s
k -n kube-system rollout status deployment/calico-kube-controllers --timeout=240s
k wait --for=condition=Ready nodes --all --timeout=180s
k -n kube-system rollout status deployment/coredns --timeout=180s
