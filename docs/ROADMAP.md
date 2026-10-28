# Roadmap

Обновлено: 2026-10-28. Исходное [ТЗ](SPECIFICATION.md) сохранено без изменений. Этап 1 завершён: локальный kind и GitHub run 40576650171 подтверждены. Проверенный commit, все 13 результатов и cleanup зафиксированы в [отчёте CI](verification/2026-10-27-stage1.md). Ingress/demo также подтверждены; подготовлена выключенная PR-интеграция для будущего отдельного кластера.

## Этап 1 — Рабочий Kubernetes Operator

- [x] Go module, Kubebuilder v4 layout, PROJECT, manager и Dockerfile.
- [x] Cluster-scoped PreviewEnvironment CRD, defaults, validation, status subresource.
- [x] Создание Namespace → Deployment → Service.
- [x] Reconcile управляемых полей и восстановление отсутствующих ресурсов.
- [x] Проверка владения и отказ при коллизиях.
- [x] Ready по актуальному rollout, пять фаз и диагностические Conditions.
- [x] TTL, Delete preconditions, cleanup finalizer.
- [x] Модульные тесты lifecycle, ownership, ошибок API и нескольких CR.
- [x] Локальные unit-тесты, vet, сборка Windows manager, рендер Kustomize.
- [x] Компиляция manager и envtest suite для Linux/amd64; проверка синтаксиса Bash-скриптов.
- [x] Выполнен race detector в Ubuntu WSL2, Go 1.26.9/gcc 13.3; PASS.
- [x] Выполнен envtest Kubernetes 1.37.0 под race detector в WSL2; PASS. Manager использует ServiceAccount RBAC и leader election.
- [x] Добавлены проверки Status conflict/Conditions, concurrent TTL extension, UID Delete precondition, чужих владельцев и сохранения finalizers при API outage.
- [x] Исправлена потеря Conditions из-за устаревшего cache snapshot; дополнен namespaced RBAC для leader election Event.
- [x] Скрипты создают только новый локальный previewforge-test-*; тесты границ PowerShell/Bash прошли по 7 сценариев. Image и Kustomize overlay отдельные для каждого запуска.
- [x] E2e расширен: owners, held finalizer, TTL после downtime, чужой Namespace с поддельными labels и ConfigMap-canary.
- [x] Подготовлен GitHub Actions workflow этапа 1: hosted Ubuntu, race/envtest, отдельный kind/kubeconfig, диагностика, EXIT/always() cleanup и обязательные 13 результатов сценариев. Реальный запуск этого исходного набора из 13 check ID подтверждён ниже.
- [x] Добавлены проверки настоящих Pod/readiness, Service DNS/ClusterIP, активного TTL; cleanup ограничен claim/token/локальным Docker endpoint текущего запуска. Остаточные риски и меры защиты описаны в SECURITY.md.
- [x] На 2026-10-11: Linux vet/build/race/envtest, Actionlint/ShellCheck, 17 mock cleanup сценариев и 4 синтетических report gate проверки — PASS. Тогда настоящий e2e остановился на Docker preflight; успешный локальный Kubernetes run выполнен позже, 2026-10-27.
- [x] Выполнен workflow в T1mof/previewForge: run 40576650171, commit 7d0a156, оба job SUCCESS. Скачанный kind artifact проверен по digest, commit, всем 13 check ID и cleanup; см. отчёт этапа 1.
- [x] Собран Docker image; штатный Bash kind demo/e2e выполнен через Git Bash с локальным Docker Desktop. Все 13 проверок — PASS, cleanup тестового кластера подтверждён.
- [x] Подтверждены реальные Pod/readiness, HTTP через Service DNS/ClusterIP, рестарт, TTL при работе/простое, finalizer до исчезновения Namespace и отказ изменения чужих ресурсов.
- [x] Исправлена колонка EXPIRES: абсолютный RFC3339 deadline вместо `<invalid>` для будущей даты; CRD перегенерирована controller-gen 0.22.0.
- [x] После исправления: Windows Generate/gofmt/vet/test/build/Kustomize и Linux vet/build/race/envtest — PASS. Итоговый новый kind-кластер c: 13/13 PASS, report gate и cleanup PASS. Промежуточный отказ API server в кластере b записан отдельно; cleanup при этой ошибке также подтверждён.

Воспроизведение: `scripts/check.ps1` для Windows; `wsl --distribution Ubuntu --exec bash /mnt/e/PreviewForge/scripts/linux-checks.sh` для race/envtest. Полный локальный e2e подтверждён 2026-10-27 через Git Bash, без изменения настроек WSL Integration. Итоговые отчёты находятся в `bin/runs/previewforge-test-local-20261027-c/artifacts/`; кластер удалён. Первый опубликованный commit 7d0a156 подтверждён GitHub Linux/kind job; artifact и cleanup проверены независимо. Точные команды и запуск CI — в [VERIFICATION.md](VERIFICATION.md), реальные результаты — в [TESTING.md](TESTING.md), меры защиты — в [SECURITY.md](SECURITY.md). Ingress/demo подтверждены отдельно; PR-интеграция подготовлена без внешней активации.

## Этап 2 — Надёжность и PR integration

- [ ] Расширить fault/concurrency tests: stale cache, API outage, конфликт spec/status/delete, смена лидера.
- [x] Подготовлен выключенный PR workflow: same-repository trusted PR, image digest, base tooling, повторная проверка live head/state, per-PR concurrency. Внешняя активация отложена.
- [ ] Создание окружений из PR, привязка к image build, удаление при закрытии PR.
- [x] Определена модель доступа: отдельный кластер, ограниченный deployer SA, source Namespace UID; fork PR пропускаются, build не получает кластерный Environment Secret. OIDC зависит от выбранной площадки.
- [x] Необязательный Ingress с host/class/TLS Secret, URL в status, ownership, drift/recreation и безопасное отключение. Linux envtest и локальный e2e 14/14 PASS.
- [x] Локальный демонстрационный kind с Traefik и настоящим HTTP на http://localhost:8080; приложение demo b затем удалено по часовому TTL (CR и Namespace NotFound).
- [x] CI Ingress подтверждён: run 40582139866, commit 8082fab, оба job SUCCESS, 14/14 e2e, cleanup и скачанный artifact проверены; [отчёт](verification/2026-10-28-ingress.md).
- [x] Подготовлена выключенная по умолчанию PR-интеграция, CLI и установочные RBAC manifests. [Инструкция](PR-INTEGRATION.md). Площадка/домен и включение живых PR-ссылок выбираются позже; пользовательский или production кластер не используется.
- [x] PR-компоненты проверены в новом локальном kind: 15/15 e2e, cleanup/report gate PASS; две image revision по HTTP, ограниченный SA, update и idempotent close. GitHub metadata здесь fixture.
- [x] CI PR-подготовки подтверждён: run 40640242526, commit ad72829, оба job SUCCESS, 15/15 и cleanup; artifact скачан и проверен независимо. [Отчёт](verification/2026-10-28-pr-integration.md).
- [ ] После выбора площадки: активировать workflow и подтвердить настоящий GitHub PR create/update/HTTP/close. Fixture metadata в локальном kind этот пункт не закрывает.
- [ ] Политики сетевой изоляции и ограничения доступа к API.

Базовые drift repair, ownership и optimistic locking включены в этап 1, поскольку нужны для корректного reconcile.

## Этап 3 — Наблюдаемость и эксплуатация

- [ ] Предметные Prometheus metrics, защищённый scrape endpoint, Grafana dashboard.
- [ ] ResourceQuota/LimitRange, настраиваемые ресурсы, Pod Security и admission restrictions.
- [ ] Helm Chart.
- [ ] Нагрузочные и отказоустойчивые испытания с опубликованными измерениями.
- [ ] Больше интеграционных сценариев и поддерживаемая Kubernetes version matrix.

Requests/limits контейнера уже заданы в этапе 1. Реализованы inventory collector из Kubernetes и защищённый HTTPS endpoint в default manifests; optional Prometheus/Grafana demo и e2e описаны в [OBSERVABILITY.md](OBSERVABILITY.md). Приёмка metrics/monitoring отмечается после настоящего kind и CI; запуск процесса вне manifests оставляет metrics выключенными по умолчанию.
