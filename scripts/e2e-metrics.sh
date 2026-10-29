#!/usr/bin/env bash
# Sourced within the claimed, fresh Kubernetes e2e cluster only.
# Expressions in single-quoted scripts expand inside the Pod, never on the host.
# shellcheck disable=SC2016
: "${directory:?}" "${python_bin:?}"
pf_assert_owned
k create namespace previewforge-monitoring
# Random local-demo admin credential, never written to artifacts or printed.
k -n previewforge-monitoring create secret generic previewforge-grafana-admin --from-literal="password=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
k apply -k config/monitoring-demo
for app in prometheus grafana; do
  k -n previewforge-monitoring rollout status deployment/"$app" --timeout=240s
done
for account in prometheus default; do
  cat <<EOF | k apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: metrics-$account
  namespace: previewforge-monitoring
spec:
  restartPolicy: Never
  serviceAccountName: $account
  securityContext:
    runAsNonRoot: true
    runAsUser: 65534
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: client
      image: busybox:1.37.0
      command: [sh, -c, 'sleep 1200']
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities: {drop: [ALL]}
      resources:
        requests: {cpu: 10m, memory: 8Mi}
        limits: {cpu: 100m, memory: 32Mi}
      volumeMounts: [{name: tmp, mountPath: /tmp}]
  volumes:
    - name: tmp
      emptyDir: {sizeLimit: 8Mi}
EOF
  k -n previewforge-monitoring wait --for=condition=Ready pod/"metrics-$account" --timeout=120s
done

# TLS is real; certificate verification is skipped only for the ephemeral,
# self-signed serving certificate in this disposable local test cluster.
metrics_url=https://previewforge-metrics.previewforge-system.svc.cluster.local:8443/metrics
k -n previewforge-monitoring exec metrics-prometheus -- sh -ec '
  if wget --no-check-certificate -S -O /tmp/body -T 10 "$1" 2>/tmp/headers; then exit 1; fi
  grep -E "HTTP/[0-9.]+ 401" /tmp/headers
' sh "$metrics_url" > "$directory/artifacts/metrics-unauthenticated.txt"
k -n previewforge-monitoring exec metrics-default -- sh -ec '
  token=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
  if wget --no-check-certificate -S -O /tmp/body -T 10 --header="Authorization: Bearer $token" "$1" 2>/tmp/headers; then exit 1; fi
  grep -E "HTTP/[0-9.]+ 403" /tmp/headers
' sh "$metrics_url" > "$directory/artifacts/metrics-forbidden.txt"

metrics_scrape() {
  k -n previewforge-monitoring exec metrics-prometheus -- sh -ec '
    token=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
    wget -q --no-check-certificate -O- -T 10 --header="Authorization: Bearer $token" "$1"
  ' sh "$metrics_url" > "$1"
}
metrics_scrape "$directory/artifacts/metrics-before.txt"
baseline_count=$("$python_bin" -c 'import re,sys; text=open(sys.argv[1]).read(); assert re.search(r"^previewforge_inventory_up 1$",text,re.M); print(int(sum(float(v) for v in re.findall(r"^previewforge_environments\{phase=\"[^\"]+\"\} ([0-9.e+]+)$",text,re.M))))' "$directory/artifacts/metrics-before.txt")
inventory_count() {
  metrics_scrape "$directory/artifacts/metrics-$2.txt" || return 1
  "$python_bin" -c 'import re,sys; text=open(sys.argv[2]).read(); assert re.search(r"^previewforge_inventory_up 1$",text,re.M); values=re.findall(r"^previewforge_environments\{phase=\"[^\"]+\"\} ([0-9.e+]+)$",text,re.M); assert len(values)==7 and sum(map(float,values))==int(sys.argv[1])' "$1" "$directory/artifacts/metrics-$2.txt"
}
create_preview e2e-metrics 3600
k wait --for=condition=Ready previewenvironment/e2e-metrics --timeout=180s
metrics_namespace=$(k get previewenvironment e2e-metrics -o jsonpath='{.status.namespace}')
wait_value 30 inventory_count "$((baseline_count + 1))" created
stop_operator
start_operator
wait_value 30 inventory_count "$((baseline_count + 1))" restarted
k delete previewenvironment e2e-metrics --wait=true --timeout=120s
k wait --for=delete namespace/"$metrics_namespace" --timeout=120s
wait_value 30 inventory_count "$baseline_count" deleted

# Git Bash must pass container paths literally to native kubectl.exe. Scope this
# setting to exec so host kubeconfig paths retain normal MSYS conversion.
MSYS2_ARG_CONV_EXCL='/bin/promtool;/etc/prometheus/' k -n previewforge-monitoring exec deployment/prometheus -- /bin/promtool check config /etc/prometheus/prometheus.yml > "$directory/artifacts/metrics-promtool.txt"
k -n previewforge-monitoring exec deployment/prometheus -- sh -ec 'cd /etc/prometheus && /bin/promtool test rules rules.test.yml' >> "$directory/artifacts/metrics-promtool.txt"
printf '\nRules unit test: PASS\n' >> "$directory/artifacts/metrics-promtool.txt"
metrics_queries() {
  local target=$1 path=$2 output=$3
  k -n previewforge-monitoring exec metrics-prometheus -- wget -q -O- -T 10 "http://$target/$path" > "$directory/artifacts/$output"
}
prometheus_inventory() {
  metrics_queries prometheus:9090 'api/v1/query?query=sum%28previewforge_environments%29' metrics-prometheus-query.json || return 1
  "$python_bin" -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["status"]=="success" and len(d["data"]["result"])==1 and float(d["data"]["result"][0]["value"][1])==int(sys.argv[2])' "$directory/artifacts/metrics-prometheus-query.json" "$baseline_count"
}
wait_value 30 prometheus_inventory
metrics_queries prometheus:9090 api/v1/targets metrics-prometheus-targets.json
metrics_queries grafana:3000 api/dashboards/uid/previewforge metrics-grafana-dashboard.json
metrics_queries grafana:3000 'api/datasources/proxy/uid/previewforge-prometheus/api/v1/query?query=previewforge_inventory_up' metrics-grafana-query.json
"$python_bin" -c 'import json,sys; dash=json.load(open(sys.argv[1]))["dashboard"]; assert dash["uid"]=="previewforge" and len(dash["panels"])==9; d=json.load(open(sys.argv[2])); assert d["status"]=="success" and float(d["data"]["result"][0]["value"][1])==1; t=json.load(open(sys.argv[3])); assert t["status"]=="success" and any(x["health"]=="up" and x["labels"]["job"]=="previewforge" for x in t["data"]["activeTargets"])' "$directory/artifacts/metrics-grafana-dashboard.json" "$directory/artifacts/metrics-grafana-query.json" "$directory/artifacts/metrics-prometheus-targets.json"
"$python_bin" -c 'import json,sys,urllib.parse; panels=json.load(open(sys.argv[1]))["dashboard"]["panels"]; print("\n".join(urllib.parse.quote(p["targets"][0]["expr"].replace("$__rate_interval","1m"),safe="") for p in panels))' "$directory/artifacts/metrics-grafana-dashboard.json" > "$directory/artifacts/metrics-dashboard-queries.txt"
query_index=0
while IFS= read -r query; do
  query_index=$((query_index + 1))
  metrics_queries grafana:3000 "api/datasources/proxy/uid/previewforge-prometheus/api/v1/query?query=$query" "metrics-panel-$query_index.json"
  "$python_bin" -c 'import json,sys; assert json.load(open(sys.argv[1]))["status"]=="success"' "$directory/artifacts/metrics-panel-$query_index.json"
done < "$directory/artifacts/metrics-dashboard-queries.txt"
[[ $query_index == 9 ]]
record_check metrics 'HTTPS rejected unauthenticated and unauthorized clients; authorized inventory tracked create/restart/delete; real Prometheus scrape and promtool rules passed; Grafana provisioned nine panels and queried live data'
