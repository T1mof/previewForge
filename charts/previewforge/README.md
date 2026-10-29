# PreviewForge chart

Installs one cluster-wide PreviewForge operator, namespaced leader election,
HTTPS metrics, and optional CEL/ownership admission boundaries. Tested target:
Kubernetes 1.37.0 and Helm 4.3.1. This is a development chart; no operator image
is published automatically. Supply an available image tag or immutable digest.

```sh
helm --kubeconfig "$KUBECONFIG" --kube-context "$CONTEXT" install previewforge \
  ./charts/previewforge --namespace previewforge-system --create-namespace \
  --set image.repository=your-registry/previewforge --set image.tag=your-tag --wait
```

Admission is disabled by default, matching `config/default`. Enable it with
`admission.enabled=true`, an existing `admission.tls.secretName` in the release
namespace, and `admission.tls.caBundle` containing base64 PEM CA. The serving
certificate must cover `previewforge-admission.<namespace>.svc`. The chart
does not generate, own, or rotate private keys. The project `admissioncert`
helper creates a 48-hour certificate for disposable demos only.

| Value | Purpose |
| --- | --- |
| `image.repository/tag/digest/pullPolicy` | Operator and independent admission server image; digest takes precedence |
| `replicaCount`, `resources` | Manager replicas (1–3) and resources; leader election is always enabled |
| `network.dnsNamespace/dnsPodSelector` | Non-empty trusted DNS namespace and Pod selector |
| `network.ingressNamespace/ingressPodSelector` | Trusted gateway pair; configure both before creating preview Ingress |
| `metrics.enabled/tlsSecretName` | Authenticated HTTPS endpoint; optional existing TLS Secret |
| `admission.enabled/replicaCount/resources` | Independent ownership webhook and write policies |
| `admission.tls.secretName/caBundle` | Externally managed serving TLS and CA |

Release namespace may vary; ServiceAccount/resource names are fixed to prevent
accidental parallel cluster-wide controllers. A second installation conflicts
with Helm ownership metadata. Existing Kustomize resources are not adopted.
Do not bypass that check using ownership takeover options.

The chart does not own the installation Namespace, TLS Secrets, or user
PreviewEnvironments. Delete your named previews and wait for Namespace cleanup
before uninstalling. Helm retains the CRD; schema updates are an explicit
administrator operation before an operator upgrade. Uninstalling with active
previews leaves them and their finalizers waiting for an operator to be restored.

Prometheus/Grafana are separate optional monitoring components, not dependencies
of this chart. `config/monitoring` remains the disposable project demo.

Generated CRD/RBAC/policy assets are synchronized by
`python3 scripts/sync-chart-assets.py`; `--check` and chart tests prevent drift.
Do not edit the copies. From the repository root run `make test-chart` with
Helm on PATH, or use `scripts/linux-checks.sh` for the complete suite.
Full verification and installation examples: `docs/HELM.md` in the repository.
