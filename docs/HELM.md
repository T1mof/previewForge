# Установка через Helm

Chart `charts/previewforge` упаковывает оператор, leader election, HTTPS metrics
и optional admission. Проверяемая версия Helm — 4.3.1, целевой Kubernetes —
1.37.0. Другие minor-версии пока не поддерживаются chart constraint; version
matrix остаётся отдельным этапом. Registry/image publish автоматически не включается.

## Контракт установки

Поддерживается один оператор на кластер: он наблюдает cluster-scoped CR и
создаёт динамические Namespace. ClusterRole, CRD и admission resource names
фиксированы; Helm ownership не позволяет молча установить второй release
или принять ресурсы существующего Kustomize install. Namespace установки
настраивается через `--namespace`; ServiceAccount names фиксированы.

Chart не создаёт user CR, не управляет установочным Namespace и существующими
TLS Secrets. CRD лежит в `crds/`, устанавливается до release и сохраняется при
uninstall. Helm не обновляет CRD при upgrade/rollback: совместимость и обновление
схемы проверяет администратор отдельно. [Документация Helm](https://docs.helm.sh/docs/chart_best_practices/custom_resource_definitions/).

## Базовый режим

Используйте отдельный demo-кластер, явные kubeconfig/context и доступный образ:

```sh
helm --kubeconfig "$CONFIG" --kube-context "$CONTEXT" install previewforge \
  charts/previewforge --namespace previewforge-system --create-namespace \
  --set image.repository=your-registry/previewforge --set image.tag=your-tag \
  --wait --timeout=180s
```

Образ содержит `/manager` и независимый `/admission`. Вместо tag можно передать
`image.digest=sha256:...`; digest имеет приоритет. Базовый режим соответствует
`config/default`: admission выключен явно в defaults. Metrics включены через
HTTPS authn/authz; `metrics.enabled=false` убирает endpoint/Service/reader role.
Пустой `metrics.tlsSecretName` использует ephemeral self-signed сертификат;
для постоянного scrape передайте собственный TLS Secret и доверенный CA клиенту.
Chart не устанавливает Prometheus Operator, ServiceMonitor или Grafana.

## Admission и произвольный namespace

Для admission передайте существующий TLS Secret и base64 PEM CA в values:

```yaml
admission:
  enabled: true
  tls:
    secretName: previewforge-admission-tls
    caBundle: <base64 PEM CA>
```

SAN должен включать `previewforge-admission.<release-namespace>.svc`. Private
key не передаётся в values и не попадает в release manifest. Его ротация и
обновление CA bundle — обязанность администратора. Helm согласует одну и ту же
identity в пяти matchConditions и флаге webhook `--operator-identity`.
Неисправная или пустая identity отклоняется; fallback на другой ServiceAccount
не используется. Kustomize install сохраняет прежнюю default identity.

Для одноразовой локальной демонстрации после `make build`:

```sh
# Только новый каталог; helper откажется перезаписывать сертификат.
mkdir -p bin/helm-demo-tls
bin/admissioncert --directory bin/helm-demo-tls --namespace forge-system
kubectl --kubeconfig "$CONFIG" --context "$CONTEXT" create namespace forge-system
kubectl --kubeconfig "$CONFIG" --context "$CONTEXT" create -f bin/helm-demo-tls/admission-tls.json
helm --kubeconfig "$CONFIG" --kube-context "$CONTEXT" install previewforge \
  charts/previewforge --namespace forge-system \
  -f bin/helm-demo-tls/admission-helm-values.json \
  --set image.repository=your-registry/previewforge --set image.tag=your-tag \
  --wait --timeout=180s
```

Сертификат helper действует 48 часов и годится для disposable demo. Каталог
с private Secret manifest не публикуется и не включается в test artifacts.
Для Ingress дополнительно задайте оба `network.ingressNamespace` и
`network.ingressPodSelector`. DNS/gateway values — настройки доверенного
администратора; CR не задаёт эти selectors. Подробнее: [границы admission](ADMISSION.md)
и [сетевая изоляция](NETWORK-ISOLATION.md).

## Upgrade, rollback и удаление

```sh
helm --kubeconfig "$CONFIG" --kube-context "$CONTEXT" upgrade previewforge \
  charts/previewforge --namespace forge-system --reuse-values \
  --set replicaCount=2 --wait --timeout=180s
helm --kubeconfig "$CONFIG" --kube-context "$CONTEXT" rollback previewforge 1 \
  --namespace forge-system --wait --timeout=180s
```

Leader election всегда включён, lease находится в release namespace. Upgrade
и rollback не должны менять UID CR, его Namespace и Deployment приложения.
Перед uninstall удалите свои конкретные PreviewEnvironment и дождитесь полного
cleanup Namespace; chart не удаляет пользовательские окружения за вас.
Затем выполните `helm ... uninstall previewforge --namespace forge-system --wait`.
CRD, установочный Namespace и внешний TLS Secret сохраняются. Без предварительного
cleanup активные CR/finalizers будут ждать повторной установки оператора.

## Воспроизведение проверок

```sh
bash scripts/install-helm.sh  # Linux amd64; официальный архив и закреплённый SHA256
export PATH="$PWD/bin/linux:$PATH"
python3 scripts/sync-chart-assets.py --check
make test-chart
make test-e2e
```

Полный Bash e2e по умолчанию использует Helm и требует **20** check ID. Он
устанавливает `.tgz` в нестандартный namespace, проверяет HTTP, отказ второй
установки и запись от custom SA, выполняет upgrade/rollback, устраняет drift,
удаляет fixture CR, затем проверяет сохранение CRD/Namespace canary после
uninstall. После этого пакет устанавливается в default namespace и выполняется
весь прежний suite 19/19. Всё происходит только в новом claimed kind с Calico;
EXIT/INT/TERM и CI `always()` cleanup сохранены.

Для отладки прежнего Kustomize suite можно явно указать
`PREVIEWFORGE_INSTALLER=kustomize`; это 19 проверок, без доказательства Helm
lifecycle. Standalone Bash/PowerShell demo сохраняют Kustomize по умолчанию.
Runtime подтверждён [CI 40975721575](https://github.com/T1mof/previewForge/actions/runs/40975721575)
на source `eb55483`: 20/20, cleanup PASS, artifact скачан и проверен независимо.
Локальный запуск остановился до Helm deployment на Docker Hub TLS timeout,
кластер очищен; локальный full 20/20 не заявляется.
[Проверенный пакет, SHA256 и границы доказательств](verification/2026-10-30-helm.md).
