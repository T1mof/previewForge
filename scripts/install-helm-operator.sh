#!/usr/bin/env bash
# Sourced by kind-demo.sh after image load in the freshly claimed cluster.
: "${cluster:?}" "${kubeconfig:?}" "${context:?}" "${overlay:?}" "${admissioncert:?}"
pf_assert_owned
helm_bin=${PREVIEWFORGE_HELM:-helm}
helm_home="$PWD/.cache/helm"
if command -v cygpath >/dev/null; then helm_home=$(cygpath -w "$helm_home"); fi
export HELM_CACHE_HOME="$helm_home/cache" HELM_CONFIG_HOME="$helm_home/config" HELM_DATA_HOME="$helm_home/data" HELM_PLUGINS="$helm_home/plugins"
python_bin=''
for candidate in python3 python; do
  if command -v "$candidate" >/dev/null; then python_bin=$candidate; break; fi
done
[[ -n "$python_bin" ]] || { echo 'Python 3 is required for the Helm kind lifecycle' >&2; exit 1; }
directory=$(pf_run_directory)
"$python_bin" scripts/check-helm-lifecycle.py "$kubeconfig" "$context" "$cluster" "$directory" "$helm_bin" "$admissioncert"
packages=("$directory"/artifacts/previewforge-*.tgz)
[[ ${#packages[@]} == 1 && -f ${packages[0]} ]] || { echo 'Expected exactly one verified chart package' >&2; exit 1; }
k create namespace previewforge-system
k create -f "$overlay/admission-tls.json"
"$helm_bin" --kubeconfig "$kubeconfig" --kube-context "$context" install previewforge "${packages[0]}" \
  --namespace previewforge-system -f "$overlay/admission-helm-values.json" \
  --set-string image.repository=previewforge --set-string "image.tag=$cluster" \
  --set-string network.ingressNamespace=previewforge-ingress-system \
  --set-string network.ingressPodSelector=platform.previewforge.dev/ingress-gateway=true \
  --wait --timeout=180s
