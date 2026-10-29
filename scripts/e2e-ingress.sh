#!/usr/bin/env bash
# Sourced by e2e.sh after its ownership guard and cleanup traps are installed.
: "${ns:?}" "${directory:?}" "${owner_uid:?}"
pf_assert_owned
k create namespace previewforge-e2e-clients
k apply -f config/ingress-demo/traefik.yaml
k -n previewforge-ingress-system rollout status deployment/traefik --timeout=180s
k patch previewenvironment pr-42 --type=merge -p '{"spec":{"ingress":{"host":"pr-42.example.test","className":"previewforge-demo"}}}'
ingress_configured() {
  [[ $(k get previewenvironment pr-42 -o jsonpath='{.status.url}') == http://pr-42.example.test && $(k -n "$ns" get ingress app -o jsonpath='{.spec.rules[0].host}') == pr-42.example.test ]]
}
wait_value 60 ingress_configured
[[ $(k -n "$ns" get ingress app -o jsonpath='{.metadata.ownerReferences[?(@.controller==true)].uid}') == "$owner_uid" ]]

ingress_http() {
  local name=$1 host=$2
  k -n previewforge-e2e-clients run "$name" --restart=Never --image=busybox:1.37.0 --command -- sh -ec \
    "for i in \$(seq 1 30); do if wget -q -O /tmp/response -T 5 --header='Host: $host' http://traefik.previewforge-ingress-system.svc.cluster.local && grep -q 'Welcome to nginx' /tmp/response; then cat /tmp/response; exit 0; fi; sleep 2; done; exit 1"
  k -n previewforge-e2e-clients wait --for=jsonpath='{.status.phase}'=Succeeded pod/"$name" --timeout=120s
  k -n previewforge-e2e-clients logs "$name" >> "$directory/artifacts/ingress-http.txt"
}
ingress_http ingress-smoke pr-42.example.test

old_ingress_uid=$(k -n "$ns" get ingress app -o jsonpath='{.metadata.uid}')
k -n "$ns" delete ingress app --wait=true --timeout=60s
ingress_recreated() {
  local uid
  uid=$(k -n "$ns" get ingress app -o jsonpath='{.metadata.uid}' 2>/dev/null) || return 1
  [[ -n "$uid" && "$uid" != "$old_ingress_uid" ]]
}
wait_value 60 ingress_recreated
k -n "$ns" patch ingress app --type=json -p '[{"op":"replace","path":"/spec/rules/0/host","value":"drift.example.test"}]'
wait_value 60 ingress_configured
k patch previewenvironment pr-42 --type=merge -p '{"spec":{"ingress":{"host":"updated.example.test","className":"previewforge-demo"}}}'
ingress_updated() { [[ $(k get previewenvironment pr-42 -o jsonpath='{.status.url}') == http://updated.example.test && $(k -n "$ns" get ingress app -o jsonpath='{.spec.rules[0].host}') == updated.example.test ]]; }
wait_value 60 ingress_updated
ingress_http ingress-updated updated.example.test

k patch previewenvironment pr-42 --type=merge -p '{"spec":{"ingress":null}}'
k -n "$ns" wait --for=delete ingress/app --timeout=60s
ingress_disabled() { [[ -z $(k get previewenvironment pr-42 -o jsonpath='{.status.url}') ]]; }
wait_value 60 ingress_disabled
# A successful TCP connection to the controller must now receive its 404, not nginx.
k -n previewforge-e2e-clients run ingress-disabled --restart=Never --image=busybox:1.37.0 --command -- sh -ec \
  "for i in \$(seq 1 30); do response=\$(wget -S -O /tmp/response -T 5 --header='Host: updated.example.test' http://traefik.previewforge-ingress-system.svc.cluster.local 2>&1) && { sleep 2; continue; }; if echo \"\$response\" | grep -q '404 Not Found'; then echo \"\$response\"; exit 0; fi; sleep 2; done; exit 1"
k -n previewforge-e2e-clients wait --for=jsonpath='{.status.phase}'=Succeeded pod/ingress-disabled --timeout=120s
k -n previewforge-e2e-clients logs ingress-disabled >> "$directory/artifacts/ingress-http.txt"
record_check ingress 'real HTTP through Traefik, owned route recreated and drift repaired, hostname/URL updated, disabled route removed and returned 404'
