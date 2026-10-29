#!/usr/bin/env bash
# Sourced only inside the claimed fresh e2e cluster. GitHub events are fixtures;
# builds, deployer RBAC, CLI operations, Pods, HTTP and cleanup are real Kubernetes.
: "${previewctl:?}" "${directory:?}" "${context:?}"
pf_assert_owned
python_bin=''
for candidate in python python3; do
  if command -v "$candidate" >/dev/null; then python_bin=$candidate; break; fi
done
[[ -n "$python_bin" ]] || { echo 'Python 3 is required for PR e2e metadata' >&2; exit 1; }
first_revision=0000000000000000000000000000000000000001
next_revision=0000000000000000000000000000000000000002
build_example_image() {
  local stage=$1 revision=$2 image="previewforge-example:$PREVIEWFORGE_TEST_CLUSTER-$1" digest immutable
  docker build --file examples/hello/Dockerfile --build-arg "REVISION=$revision" -t "$image" . >&2 || return
  kind load docker-image "$image" --name "$PREVIEWFORGE_TEST_CLUSTER" >&2 || return
  # Docker archive imports may expose unusable import-* CRI repoDigests. Resolve
  # the actual OCI target of this exact tag and register its digest reference.
  docker exec "$PREVIEWFORGE_TEST_CLUSTER-control-plane" ctr -n k8s.io images list "name==docker.io/library/$image" > "$directory/artifacts/pr-image-$stage.txt" || return
  digest=$("$python_bin" -c 'import re,sys; rows=[line.split() for line in sys.stdin]; values=[row[2] for row in rows if len(row)>2 and row[0]==sys.argv[1]]; assert len(values)==1 and re.fullmatch(r"sha256:[0-9a-f]{64}", values[0]), "missing exact OCI image target"; print(values[0])' "docker.io/library/$image" < "$directory/artifacts/pr-image-$stage.txt") || return
  immutable="docker.io/library/previewforge-example@$digest"
  docker exec "$PREVIEWFORGE_TEST_CLUSTER-control-plane" ctr -n k8s.io images tag "docker.io/library/$image" "$immutable" >&2 || return
  docker exec "$PREVIEWFORGE_TEST_CLUSTER-control-plane" crictl inspecti "$immutable" > "$directory/artifacts/pr-image-$stage.json" || return
  printf '%s\n' "$immutable"
}
first_image=$(build_example_image first "$first_revision")
next_image=$(build_example_image next "$next_revision")

k apply -k config/pr-deployer
source_namespace=previewforge-pr-source
source_uid=$(k get namespace "$source_namespace" -o jsonpath='{.metadata.uid}')
server=$(k config view --minify --raw -o jsonpath='{.clusters[0].cluster.server}')
ca_data=$(k config view --minify --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')
token=$(k -n "$source_namespace" create token previewforge-pr-deployer --duration=15m)
deployer_config="$directory/deployer.kubeconfig"
umask 077
cat > "$deployer_config" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: $context
    cluster:
      server: $server
      certificate-authority-data: $ca_data
users:
  - name: previewforge-pr-deployer
    user:
      token: $token
contexts:
  - name: $context
    context:
      cluster: $context
      user: previewforge-pr-deployer
current-context: $context
EOF
unset token

pr_name=pr-424242-42
pr_host=pr-424242-42.pr.example.test
common=(--kubeconfig "$deployer_config" --context "$context" --repository-id 424242 --pr-number 42 --wait --timeout 3m)
spec=(--host "$pr_host" --ingress-class previewforge-demo --port 8080 --ttl-seconds 3600 --security-profile restricted --cpu-request 150m --memory-request 96Mi --cpu-limit 500m --memory-limit 256Mi)
if "$previewctl" apply "${common[@]}" --source-uid wrong-source --image "$first_image" --revision "$first_revision" "${spec[@]}"; then
  echo 'Wrong source UID was accepted' >&2; exit 1
fi
if k get previewenvironment "$pr_name" >/dev/null 2>&1; then echo 'Wrong source UID created an environment' >&2; exit 1; fi

"$previewctl" apply "${common[@]}" --source-uid "$source_uid" --image "$first_image" --revision "$first_revision" "${spec[@]}" > "$directory/artifacts/pr-create.json"
[[ $(k get previewenvironment "$pr_name" -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}') == "$source_uid" ]]
pr_namespace=$(k get previewenvironment "$pr_name" -o jsonpath='{.status.namespace}')
pr_created=$(k get previewenvironment "$pr_name" -o jsonpath='{.metadata.creationTimestamp}')
pr_namespace_uid=$(k get namespace "$pr_namespace" -o jsonpath='{.metadata.uid}')
pr_http() {
  local name=$1 revision=$2 overrides
  printf -v overrides '{"spec":{"automountServiceAccountToken":false,"securityContext":{"runAsNonRoot":true,"runAsUser":65534,"seccompProfile":{"type":"RuntimeDefault"}},"containers":[{"name":"%s","securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}}}]}}' "$name"
  k -n previewforge-e2e-clients run "$name" --restart=Never --image=busybox:1.37.0 \
    --override-type=strategic --overrides="$overrides" \
    --command -- sh -ec \
    "for i in \$(seq 1 30); do if wget -q -O /tmp/response -T 5 --header='Host: $pr_host' http://traefik.previewforge-ingress-system.svc.cluster.local && grep -q '$revision' /tmp/response && wget -q -O /tmp/health -T 5 --header='Host: $pr_host' http://traefik.previewforge-ingress-system.svc.cluster.local/healthz && grep -qx ok /tmp/health; then cat /tmp/response /tmp/health; exit 0; fi; sleep 2; done; exit 1"
  if ! k -n previewforge-e2e-clients wait --for=jsonpath='{.status.phase}'=Succeeded pod/"$name" --timeout=180s; then
    k -n previewforge-e2e-clients logs "$name" > "$directory/artifacts/pr-http-failure-$name.txt" 2>&1 || true
    return 1
  fi
  k -n previewforge-e2e-clients logs "$name" >> "$directory/artifacts/pr-http.txt"
}
pr_http pr-first "$first_revision"
"$previewctl" apply "${common[@]}" --source-uid "$source_uid" --image "$next_image" --revision "$next_revision" --replicas 2 "${spec[@]}" > "$directory/artifacts/pr-update.json"
[[ $(k get previewenvironment "$pr_name" -o jsonpath='{.metadata.creationTimestamp}') == "$pr_created" ]]
[[ $(k get namespace "$pr_namespace" -o jsonpath='{.metadata.uid}') == "$pr_namespace_uid" ]]
[[ $(k -n "$pr_namespace" get deployment app -o jsonpath='{.spec.template.spec.containers[0].image}') == "$next_image" ]]
pr_http pr-updated "$next_revision"

# Stop the operator so any metadata changes would be attributable to this test,
# then prove forged labels do not authorize the CLI to delete a foreign CR.
stop_operator
cat <<EOF | k apply -f -
apiVersion: platform.previewforge.dev/v1alpha1
kind: PreviewEnvironment
metadata:
  name: pr-424242-43
  labels:
    app.kubernetes.io/managed-by: previewforge-pr
    platform.previewforge.dev/repository-id: "424242"
    platform.previewforge.dev/pr-number: "43"
spec:
  image: $first_image
  port: 8080
  ttlSeconds: 3600
EOF
foreign_pr_version=$(k get previewenvironment pr-424242-43 -o jsonpath='{.metadata.uid} {.metadata.resourceVersion}')
if "$previewctl" delete --kubeconfig "$deployer_config" --context "$context" --source-uid "$source_uid" --repository-id 424242 --pr-number 43; then
  echo 'Foreign PR environment deletion was accepted' >&2; exit 1
fi
[[ $(k get previewenvironment pr-424242-43 -o jsonpath='{.metadata.uid} {.metadata.resourceVersion}') == "$foreign_pr_version" ]]
[[ -z $(k get previewenvironment pr-424242-43 -o jsonpath='{.metadata.deletionTimestamp}') ]]
start_operator
"$previewctl" delete "${common[@]}" --source-uid "$source_uid" > "$directory/artifacts/pr-delete.json"
k wait --for=delete namespace/"$pr_namespace" --timeout=120s
"$previewctl" delete "${common[@]}" --source-uid "$source_uid" > "$directory/artifacts/pr-delete-again.json"
[[ $(k get namespace "$source_namespace" -o jsonpath='{.metadata.uid}') == "$source_uid" ]]
record_check pr-integration 'restricted deployer CLI created and updated immutable example images with real HTTP revisions, preserved TTL origin, refused wrong source/foreign CR, and completed idempotent close cleanup'
