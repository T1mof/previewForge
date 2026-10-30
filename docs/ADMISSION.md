# Admission boundaries

`config/hardened` adds four native `ValidatingAdmissionPolicy` resources and
`Deny, Audit` bindings to `config/default`. It requires the Kubernetes v1
ValidatingAdmissionPolicy API and the CEL quantity/string libraries used by
the manifests. A separate TLS ownership webhook performs live reads that
CEL cannot perform. The supported test target is Kubernetes 1.37; other
versions are not yet verified. Operator RBAC is unchanged. The webhook's
independent ServiceAccount has only `get` on Namespaces and PreviewEnvironments.

The hardened base needs a TLS Secret and a trusted webhook CA bundle before
it is usable. The disposable kind scripts create both automatically. For a
local installation, with administrator credentials and an available image:

```sh
mkdir -p bin/admission-install
bin/admissioncert --directory bin/admission-install
cat > bin/admission-install/kustomization.yaml <<'EOF'
resources:
  - ../../config/hardened
  - admission-tls.json
patches:
  - path: admission-ca.json
EOF
kubectl apply -k bin/admission-install
```

Kustomize policies deliberately match the exact default identity
`system:serviceaccount:previewforge-system:previewforge-controller-manager`.
Changing that installation namespace or ServiceAccount requires updating all
five identity match conditions and the webhook `--operator-identity` flag.
The handler validates a concrete ServiceAccount username and fails closed if
not configured; it never falls back from an invalid identity. The
[Helm chart](HELM.md) derives all five conditions and the server flag from the
release namespace. `config/default` alone
does not install these boundaries. Both disposable kind demo scripts use
`config/hardened`; external PR deployment remains disabled by default.

`admissioncert` is a demo helper: `--namespace` defaults to `previewforge-system`.
It creates a unique CA and serving certificate
valid for 48 hours, refuses to overwrite existing files, and stores the private
key only in the run directory/Secret. Never upload these files as artifacts.
A long-running installation must provision and rotate its own trusted TLS
certificate and update the CA bundle. Do not use the demo certificate as a
production certificate lifecycle solution.

## Enforced writes

The Namespace policy requires a `forge-*` name and a PreviewEnvironment
controller owner reference with API version, kind, name and UID. Existing
ownership cannot be adopted or changed. Diagnostic labels must agree with
the reference, and baseline/restricted Pod Security labels must remain at
v1.37 or newer. Restricted enforcement cannot be downgraded.

The ownership webhook checks the live PreviewEnvironment UID, recomputes
the Namespace name, and compares the live Namespace controller reference
with the resource's reference. A missing live owner, API read failure or
webhook outage denies operator writes (`failurePolicy: Fail`). It also checks
Namespace operations and prevents adopting or changing existing ownership.
The child CEL policy additionally freezes existing ownership and permits only the
operator's reserved Deployment, Service, Ingress, quota, limit range and
NetworkPolicy names. Labels alone never authorize a write. Operator-created
annotations and changes to existing annotations are forbidden, including
Pod template annotations. Existing administrator/controller annotations are
preserved.

The workload policy requires one app container, bounded resources and
replicas, the default ServiceAccount with token automount disabled, and
RuntimeDefault seccomp. It rejects privileged/capability additions, host
network/PID/IPC, injected containers, Secret volumes/environment, arbitrary
commands/hooks and remote kubelet probes. The optional `/tmp` emptyDir is
bounded to 64Mi. The Service policy permits internal ClusterIP routing to
the owned app and rejects external IPs, ExternalName and load balancers.

## Scope and limitations

These are additional restrictions on writes by one operator identity.
Cluster-wide read/list/watch permissions remain. RBAC access reviews still
report the underlying allowed verbs; actual admission determines whether a
particular write is accepted. Other identities, including administrators
and Kubernetes controllers, are outside these policies' identity match.

CEL's `namespaceObject` intentionally excludes owner references. The separate
webhook therefore uses uncached API reads; labels are not a substitute for
this check. Reads and the admitted write are not a cross-object transaction.
A compromised operator can still choose images/Ingress hosts, modify CRs
and change quota or NetworkPolicy contents
within owned Namespaces. Trusted admission webhooks/controllers and host
security remain separate concerns. This installation does not provide
hostile multi-tenant isolation or protection against a cluster administrator.
The webhook is part of the trusted computing base and runs independently
from the operator; compromising it bypasses the live ownership checks.
Its unavailability intentionally delays reconciliation and finalization
until service is restored. Broad operator read/list/watch and the PR deployer
CR-write boundaries remain separate limitations.

## Verification

`make test-integration` includes `TestAdmissionEnvtest`, using the real
manifest operator and read-only webhook ServiceAccount RBAC and a real API
server. It verifies policy installation/runtime evaluation, authorized writes,
forbidden foreign writes and unsafe Pod templates. Envtest has no policy
status controller, kubelet, namespace controller or garbage collector.

The full disposable Calico kind suite additionally runs
`scripts/e2e-admission.sh` and `scripts/check-admission.py`. It pauses the
operator, checks policy status/type warnings, proves RBAC allows the tested verbs, checks policy-specific API
denials and unchanged foreign UID/resourceVersion, permits normal owned
writes, fails closed during a real webhook outage, recovers after restart,
and waits for actual temporary Namespace deletion. Dangerous Pod
candidates use server dry-run so they are never scheduled. The artifact
contains policy/binding snapshots, individual denial messages and
`admission-checks.json`. The admission milestone required all 19 check IDs plus
verified cluster cleanup. [CI run 40938752311](https://github.com/T1mof/previewForge/actions/runs/40938752311)
passed all 19 checks and cleanup; the downloaded artifact was independently
verified. The local admission block passed, while the full local suite stopped
later on a monitoring image-pull timeout. See the
[verification report](verification/2026-10-30-admission.md) for exact evidence,
cleanup retry and limitations.

The configurable Helm namespace and exact server identity are additionally
verified by [CI 40975721575](https://github.com/T1mof/previewForge/actions/runs/40975721575):
20/20 including install, upgrade, rollback and uninstall from the packaged chart.
See the [Helm verification report](verification/2026-10-30-helm.md).

Reference: [Kubernetes ValidatingAdmissionPolicy](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/).

The current full Helm suite also includes load and recovery experiments: 22 check IDs, 21 with explicit Kustomize, plus `stress-report.json`. See [LOAD-TESTING.md](LOAD-TESTING.md). The historical admission 19/19 and Helm 20/20 proofs above remain scoped to their original commits.
