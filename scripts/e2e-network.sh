#!/usr/bin/env bash
# Real dataplane checks, sourced only inside the claimed disposable cluster.
: "${directory:?}" "${python_bin:?}"
pf_assert_owned
[[ ${PREVIEWFORGE_KIND_CNI:-} == calico ]] || { echo 'Network enforcement requires Calico; kindnet is not proof' >&2; exit 1; }
k -n kube-system get daemonset/calico-node deployment/calico-kube-controllers -o json > "$directory/artifacts/network-calico.json"
net_image="previewforge-netprobe:$PREVIEWFORGE_TEST_CLUSTER"
docker build --file test/e2e/probe/Dockerfile -t "$net_image" .
kind load docker-image "$net_image" --name "$PREVIEWFORGE_TEST_CLUSTER"
for net_name in e2e-net-a e2e-net-b; do
  net_ingress=''
  if [[ "$net_name" == e2e-net-a ]]; then net_ingress='  ingress: {host: network.example.test, className: previewforge-demo}'; fi
  cat <<EOF | k create -f -
apiVersion: platform.previewforge.dev/v1alpha1
kind: PreviewEnvironment
metadata:
  name: $net_name
spec:
  image: $net_image
  port: 8080
  securityProfile: restricted
  resources:
    requests: {cpu: 10m, memory: 32Mi}
    limits: {cpu: 250m, memory: 128Mi}
  ttlSeconds: 3600
$net_ingress
EOF
  k wait --for=condition=Ready "previewenvironment/$net_name" --timeout=180s
done
net_a_ns=$(k get previewenvironment e2e-net-a -o jsonpath='{.status.namespace}')
net_b_ns=$(k get previewenvironment e2e-net-b -o jsonpath='{.status.namespace}')
net_owner=$(k get previewenvironment e2e-net-a -o jsonpath='{.metadata.uid}')
net_service_a=$(k -n "$net_a_ns" get service app -o jsonpath='{.spec.clusterIP}')
net_service_b=$(k -n "$net_b_ns" get service app -o jsonpath='{.spec.clusterIP}')
net_dns=$(k -n kube-system get service kube-dns -o jsonpath='{.spec.clusterIP}')
net_pod_ip() {
  k -n "$1" get pods -l app.kubernetes.io/name=app -o json > "$directory/artifacts/network-app-$2-pods.json"
  "$python_bin" -c 'import json,sys; p=[p for p in json.load(open(sys.argv[1]))["items"] if not p["metadata"].get("deletionTimestamp") and p["status"].get("phase")=="Running"]; assert len(p)==1; sys.stdout.write(p[0]["status"]["podIP"])' "$directory/artifacts/network-app-$2-pods.json"
}
net_ip_a=$(net_pod_ip "$net_a_ns" a)
net_ip_b=$(net_pod_ip "$net_b_ns" b)
net_client() {
  local name=$1 namespace=$2 gateway=$3
  cat <<EOF | k create -f -
apiVersion: v1
kind: Pod
metadata:
  name: $name
  namespace: $namespace
  labels:
    app.kubernetes.io/name: network-probe
    platform.previewforge.dev/ingress-gateway: "$gateway"
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: probe
      image: $net_image
      command: [/netprobe, hold]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities: {drop: [ALL]}
      resources:
        requests: {cpu: 10m, memory: 16Mi}
        limits: {cpu: 100m, memory: 64Mi}
EOF
  k -n "$namespace" wait --for=condition=Ready "pod/$name" --timeout=120s
}
net_client net-local "$net_a_ns" false
net_client net-outsider previewforge-e2e-clients true
net_client net-trusted previewforge-ingress-system true
net_client net-wrong-pod previewforge-ingress-system false
# The unrestricted witness also serves HTTP on 53: allowing arbitrary port-53
# egress must fail, while real CoreDNS UDP and TCP must work.
cat <<EOF | k create -f -
apiVersion: v1
kind: Pod
metadata:
  name: net-witness
  namespace: previewforge-e2e-clients
  labels:
    app.kubernetes.io/name: network-witness
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: server
      image: $net_image
      args: [serve, "8080", "53"]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities: {drop: [ALL], add: [NET_BIND_SERVICE]}
      readinessProbe:
        tcpSocket: {port: 8080}
      resources:
        requests: {cpu: 10m, memory: 16Mi}
        limits: {cpu: 100m, memory: 64Mi}
EOF
k -n previewforge-e2e-clients wait --for=condition=Ready pod/net-witness --timeout=120s
k -n previewforge-e2e-clients expose pod net-witness --name=net-witness --port=8080
net_witness=$(k -n previewforge-e2e-clients get pod net-witness -o jsonpath='{.status.podIP}')
net_witness_service=$(k -n previewforge-e2e-clients get service net-witness -o jsonpath='{.spec.clusterIP}')
k -n previewforge-e2e-clients get pod/net-witness service/net-witness -o json > "$directory/artifacts/network-witness.json"
k get namespaces "$net_a_ns" "$net_b_ns" -o json > "$directory/artifacts/network-namespaces.json"
k -n "$net_b_ns" get networkpolicies -o json > "$directory/artifacts/network-b-policies.json"
net_probe() {
  local namespace=$1 pod=$2
  shift 2
  MSYS2_ARG_CONV_EXCL='/netprobe' k -n "$namespace" exec "$pod" -- /netprobe "$@"
}
net_check() {
  local id=$1 namespace=$2 pod=$3
  shift 3
  net_probe "$namespace" "$pod" "$@" > "$directory/artifacts/network-$id.json"
}
net_eventually() { wait_value 30 net_check "$@"; }
net_eventually same-service "$net_a_ns" net-local http "http://app.$net_a_ns.svc.cluster.local:8080" previewforge-network-witness
net_eventually same-pod "$net_a_ns" net-local http "http://$net_ip_a:8081" previewforge-network-witness
net_check b-local "$net_b_ns" deployment/app http "http://$net_ip_b:8080" previewforge-network-witness
net_check dns-udp "$net_a_ns" deployment/app dns udp "$net_dns" "app.$net_a_ns.svc.cluster.local."
net_check dns-tcp "$net_a_ns" deployment/app dns tcp "$net_dns" "app.$net_a_ns.svc.cluster.local."
net_eventually witness-before previewforge-e2e-clients net-outsider http "http://$net_witness:8080" previewforge-network-witness
net_check witness-dns-port previewforge-e2e-clients net-outsider http "http://$net_witness:53" previewforge-network-witness
net_eventually witness-service-before previewforge-e2e-clients net-outsider http "http://$net_witness_service:8080" previewforge-network-witness
net_eventually ingress-pod-denied previewforge-e2e-clients net-outsider http-denied "http://$net_ip_a:8080"
net_eventually ingress-service-denied previewforge-e2e-clients net-outsider http-denied "http://$net_service_a:8080"
net_eventually cross-a-b "$net_a_ns" deployment/app http-denied "http://$net_ip_b:8080"
net_eventually cross-b-a "$net_b_ns" deployment/app http-denied "http://$net_ip_a:8080"
net_eventually cross-service "$net_a_ns" deployment/app http-denied "http://$net_service_b:8080"
net_eventually egress-witness "$net_a_ns" deployment/app http-denied "http://$net_witness:8080"
net_eventually egress-witness-service "$net_a_ns" deployment/app http-denied "http://$net_witness_service:8080"
net_eventually rogue-dns-port "$net_a_ns" deployment/app http-denied "http://$net_witness:53"
net_eventually wrong-gateway-pod previewforge-ingress-system net-wrong-pod http-denied "http://$net_ip_a:8080"
net_eventually gateway-direct previewforge-ingress-system net-trusted http "http://$net_ip_a:8080" previewforge-network-witness
net_eventually gateway-wrong-port previewforge-ingress-system net-trusted http-denied "http://$net_ip_a:8081"
net_eventually gateway-no-ingress previewforge-ingress-system net-trusted http-denied "http://$net_ip_b:8080"
net_eventually ingress-http previewforge-e2e-clients net-outsider http http://traefik.previewforge-ingress-system.svc.cluster.local previewforge-network-witness network.example.test
net_check public-resolve previewforge-e2e-clients net-outsider resolve "${PREVIEWFORGE_E2E_PUBLIC_HOST:-api.github.com}"
net_public=$("$python_bin" -c 'import json,sys; sys.stdout.write(json.load(open(sys.argv[1]))["address"])' "$directory/artifacts/network-public-resolve.json")
net_eventually public-before previewforge-e2e-clients net-outsider tcp "$net_public:443"
net_eventually public-denied "$net_a_ns" deployment/app tcp-denied "$net_public:443"
net_eventually public-after previewforge-e2e-clients net-outsider tcp "$net_public:443"
net_check witness-after previewforge-e2e-clients net-outsider http "http://$net_witness:8080" previewforge-network-witness
net_check witness-service-after previewforge-e2e-clients net-outsider http "http://$net_witness_service:8080" previewforge-network-witness
net_old_isolation=$(k -n "$net_a_ns" get networkpolicy previewforge-isolation -o jsonpath='{.metadata.uid}')
net_old_gateway=$(k -n "$net_a_ns" get networkpolicy previewforge-ingress -o jsonpath='{.metadata.uid}')
k -n "$net_a_ns" get networkpolicies -o json > "$directory/artifacts/network-policies-before.json"
k -n "$net_a_ns" delete networkpolicy/previewforge-isolation networkpolicy/previewforge-ingress --wait=true --timeout=90s
net_recreated() {
  local isolation gateway owner
  isolation=$(k -n "$net_a_ns" get networkpolicy previewforge-isolation -o jsonpath='{.metadata.uid}') || return 1
  gateway=$(k -n "$net_a_ns" get networkpolicy previewforge-ingress -o jsonpath='{.metadata.uid}') || return 1
  owner=$(k -n "$net_a_ns" get networkpolicy previewforge-isolation -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}') || return 1
  [[ -n "$isolation" && -n "$gateway" && "$isolation" != "$net_old_isolation" && "$gateway" != "$net_old_gateway" && "$owner" == "$net_owner" ]]
}
wait_value 60 net_recreated
k -n "$net_a_ns" patch networkpolicy previewforge-ingress --type=json -p '[{"op":"replace","path":"/spec/ingress/0/ports/0/port","value":8081}]'
net_gateway_port() { [[ $(k -n "$net_a_ns" get networkpolicy previewforge-ingress -o jsonpath='{.spec.ingress[0].ports[0].port}') == "$1" ]]; }
wait_value 60 net_gateway_port 8080
net_eventually recreated-denied "$net_a_ns" deployment/app tcp-denied "$net_public:443"
net_eventually repaired-gateway previewforge-ingress-system net-trusted http "http://$net_ip_a:8080" previewforge-network-witness
net_eventually repaired-wrong-port previewforge-ingress-system net-trusted http-denied "http://$net_ip_a:8081"
k -n "$net_a_ns" get networkpolicies -o json > "$directory/artifacts/network-policies-restored.json"
stop_operator
start_operator
net_check restarted-dns "$net_a_ns" deployment/app dns tcp "$net_dns" "app.$net_a_ns.svc.cluster.local."
net_eventually restarted-denied "$net_a_ns" deployment/app tcp-denied "$net_public:443"
net_eventually restarted-ingress previewforge-e2e-clients net-outsider http http://traefik.previewforge-ingress-system.svc.cluster.local previewforge-network-witness network.example.test
k patch previewenvironment e2e-net-a --type=merge -p '{"spec":{"port":8081}}'
net_port_current() {
  local generation observed phase
  read -r generation observed phase <<< "$(k get previewenvironment e2e-net-a -o jsonpath='{.metadata.generation} {.status.observedGeneration} {.status.phase}')"
  [[ "$observed" == "$generation" && "$phase" == Ready ]] && net_gateway_port 8081
}
wait_value 90 net_port_current
net_ip_a=$(net_pod_ip "$net_a_ns" a-updated)
net_eventually new-port previewforge-ingress-system net-trusted http "http://$net_ip_a:8081" previewforge-network-witness
net_eventually old-port-denied previewforge-ingress-system net-trusted http-denied "http://$net_ip_a:8080"
net_eventually updated-ingress previewforge-e2e-clients net-outsider http http://traefik.previewforge-ingress-system.svc.cluster.local previewforge-network-witness network.example.test
k patch previewenvironment e2e-net-a --type=merge -p '{"spec":{"ingress":null}}'
k -n "$net_a_ns" wait --for=delete ingress/app --timeout=90s
net_eventually revoked-gateway previewforge-ingress-system net-trusted http-denied "http://$net_ip_a:8081"
net_check still-local "$net_a_ns" net-local http "http://$net_ip_a:8081" previewforge-network-witness
k -n "$net_a_ns" get networkpolicies -o json > "$directory/artifacts/network-policies-revoked.json"
k delete previewenvironment e2e-net-a e2e-net-b --wait=true --timeout=120s
k wait --for=delete "namespace/$net_a_ns" "namespace/$net_b_ns" --timeout=120s
[[ -z $(k get namespaces "$net_a_ns" "$net_b_ns" --ignore-not-found -o name) ]]
printf 'PASS: deleted %s %s; absence verified\n' "$net_a_ns" "$net_b_ns" > "$directory/artifacts/network-cleanup.txt"
k -n previewforge-e2e-clients delete pod/net-outsider pod/net-witness --wait=true --timeout=90s
k -n previewforge-e2e-clients delete service/net-witness --wait=true --timeout=90s
k -n previewforge-ingress-system delete pod/net-trusted pod/net-wrong-pod --wait=true --timeout=90s
printf '%s\n' 'PASS: same namespace HTTP and UDP/TCP DNS' 'PASS: cross environment Pod IP and Service blocked' 'PASS: outside ingress and spoofed gateway blocked' 'PASS: witness egress and rogue DNS port blocked' 'PASS: public TCP reachable before/after and blocked inside' 'PASS: actual Ingress and exact trusted gateway port' 'PASS: policy UID recreation and drift repair' 'PASS: restart retained allow and deny' 'PASS: port update and Ingress revocation' 'PASS: both Namespace cleanup' > "$directory/artifacts/network-checks.txt"
record_check network 'Calico enforced same-namespace/DNS/gateway allows and Pod-IP/Service/public-egress denies; positive controls, UID recreation/drift/restart, port update/revocation and both Namespace cleanup passed'
