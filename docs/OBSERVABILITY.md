# Метрики и локальный monitoring demo

Оператор экспортирует состояние PreviewEnvironment и стандартные controller-runtime/process metrics. Collector читает Kubernetes API на каждый scrape с timeout 5 секунд, не хранит локальный реестр окружений и восстанавливает inventory после рестарта без replay событий. Метрики не меняют reconcile, владение ресурсами или finalizer.

## Смысл метрик

| Метрика | Тип | Смысл |
| --- | --- | --- |
| `previewforge_inventory_up` | Gauge | Успешен ли текущий List CR через API; при ошибке — 0, остальные inventory samples отсутствуют |
| `previewforge_environments{phase}` | Gauge | Число существующих CR в каждой фазе |
| `previewforge_environment_oldest_age_seconds{phase}` | Gauge | Наибольший возраст существующего CR от creationTimestamp в каждой фазе |
| `previewforge_environments_ttl_overdue` | Gauge | Число ещё существующих CR с истёкшим TTL, включая terminating |
| `previewforge_cleanup_oldest_age_seconds` | Gauge | Наибольшее время от deletionTimestamp среди удаляемых CR |

Фазы ограничены `Pending`, `Deploying`, `Ready`, `Failed`, `Expired`, `Terminating`, `Unknown`. Удаляемый CR всегда `Terminating`; устаревшая generation status не считается Ready. Для Ready также требуется актуальная Ready=True condition. Произвольное неизвестное значение status попадает в `Unknown`. Счётчики и возраст пустых фаз равны 0.

В labels нет имён PR/CR, UID, Namespace, image, host или сообщений ошибок. Это фиксированная cardinality, независимо от количества окружений. При ошибке API отсутствие inventory нельзя считать нулём окружений: проверяйте `previewforge_inventory_up`. Возраст — возраст объекта, а не точная длительность rollout. TTL рассчитывается от creationTimestamp; обновление image не начинает его заново.

Ошибки/время reconcile, очередь и память процесса доступны через стандартные `controller_runtime_*`, `workqueue_*`, `process_*`. Inventory gauges отражают текущее состояние; они не являются историей create/delete. Историю хранит Prometheus с момента успешного scrape.

## HTTPS и права

В `config/default` включён HTTPS listener 8443 и ClusterIP Service `previewforge-metrics`. Процесс при ручном запуске по-прежнему имеет default `--metrics-bind-address=0`; при включении listener всегда HTTPS с authentication/authorization. Штатный [controller-runtime filter](https://book.kubebuilder.io/reference/metrics) проверяет Bearer token через TokenReview, затем право GET `/metrics` через SubjectAccessReview. Права на оба review API добавлены в Go markers и сгенерированы controller-gen 0.22.0.

[config/metrics-reader](../config/metrics-reader/kustomization.yaml) содержит отдельно сгенерированную роль только `get /metrics`, без Kubernetes resource permissions. Default install её ни к кому не привязывает. Optional demo связывает роль только с Prometheus SA. Operator и PR deployer не получают metrics-reader binding автоматически. Filter кеширует результаты authn/authz; изменение разрешений не обязано вступать в силу мгновенно.

Без `--metrics-cert-path` сервер генерирует временный self-signed certificate в памяти. Это работает с read-only root filesystem. Локальный demo явно использует `insecure_skip_verify: true` для этого сертификата; это не подтверждение доверия к CA/SAN. Для выбранного отдельного кластера нужно смонтировать serving certificate `tls.crt`/`tls.key`, передать `--metrics-cert-path`, настроить Prometheus `ca_file`/`server_name` и включить проверку сертификата. Оператор не создаёт certificate issuer и не читает Secrets через API ради этого mount.

## Prometheus и Grafana

[config/monitoring-demo](../config/monitoring-demo/kustomization.yaml) — отдельный optional install для нового локального kind, не часть default operator install. Используются [Prometheus 3.15.1](https://github.com/prometheus/prometheus/releases/tag/v3.15.1) и [Grafana 13.3.0](https://github.com/grafana/grafana/releases/tag/v13.3.0), оба image закреплены SHA256 index digest.

Prometheus использует projected ServiceAccount token из файла, статический HTTPS target и scrape interval 5 секунд. RBAC не разрешает ему читать Secrets или CR. Demo рассчитан на один Pod оператора; для HA нужны отдельные scrape targets Pod, а не балансировка одного Service поверх нескольких process counters. Dashboard использует max по inventory, чтобы не складывать копии одного cluster inventory.

Все Services — ClusterIP. Grafana позволяет anonymous **Viewer** для просмотра локального demo; admin password создаётся случайным Secret текущего запуска и не входит в Git/artifacts. Для внешнего развёртывания anonymous access и схема пользовательского доступа требуют отдельной настройки. Prometheus TSDB и внутренние данные Grafana лежат в ограниченных emptyDir: это одноразовая демонстрация, а не долговременное хранилище. Нет связи с external telemetry, alert delivery или пользовательским кластером.

[Dashboard JSON](../config/monitoring-demo/grafana/previewforge.json) содержит девять панелей: доступность inventory API, число окружений, TTL overdue, время cleanup, фазы, возраст unready CR, ошибки reconcile, p95 reconcile time, память. Datasource UID — `previewforge-prometheus`; provisioning files входят в тот же Kustomize install. [Alert rules](../config/monitoring-demo/prometheus/rules.yml) проверяют недоступность inventory, задержку cleanup и повторяющиеся ошибки reconcile; Alertmanager/внешняя отправка не подключены. [Правила provisioning Grafana](https://grafana.com/docs/grafana/latest/administration/provisioning/) и [конфигурация Prometheus](https://prometheus.io/docs/prometheus/latest/configuration/configuration/) описаны в первичной документации.

## Воспроизведение

`make test-e2e` создаёт новый `previewforge-test-*`, проверяет metrics auth, create/restart/delete inventory, настоящий Prometheus scrape, promtool config/rule tests, provisioning dashboard и запрос live data через Grafana. Затем удаляет только кластер этого запуска. Check `metrics` дополняет прежние 15 проверок; fake CLI/gate tests не заменяют этот Kubernetes сценарий.

Для ручного просмотра сначала создайте **новый** кластер через `make kind-demo` или штатный Git Bash demo. Из корня проекта, в Bash:

```bash
set -euo pipefail
task_context=$(kubectl --kubeconfig "$PWD/bin/kind.kubeconfig" config current-context)
case "$task_context" in kind-previewforge-test-*) ;; *) exit 1 ;; esac
task_cluster=${task_context#kind-}
task_config="$PWD/bin/runs/$task_cluster/kubeconfig"
k() { kubectl --kubeconfig "$task_config" --context "$task_context" "$@"; }
k create namespace previewforge-monitoring
k -n previewforge-monitoring create secret generic previewforge-grafana-admin \
  --from-literal="password=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
k apply -k config/monitoring-demo
k -n previewforge-monitoring rollout status deployment/prometheus --timeout=240s
k -n previewforge-monitoring rollout status deployment/grafana --timeout=240s
k -n previewforge-monitoring port-forward --address 127.0.0.1 svc/grafana 3000:3000
# http://localhost:3000/d/previewforge
```

Второй install в неизвестный существующий Namespace не является частью этого сценария. Сохранённый demo-кластер и его monitoring остаются до cleanup; приложение по-прежнему ограничено TTL. Остановка port-forward не удаляет кластер. Cleanup с claim/token и тем же Docker endpoint:

```bash
PREVIEWFORGE_TEST_CLUSTER="$task_cluster" \
PREVIEWFORGE_RUN_TOKEN=$(cat "bin/runs/$task_cluster/.cluster-owner") \
bash scripts/cleanup-kind.sh
```
