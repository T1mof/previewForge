# Журнал технических решений

## 2026-10-11 — Структура Kubebuilder и зависимости

Используем Go v4 layout (`api`, `cmd/manager`, `internal/controller`, `config`, `PROJECT`) и controller-runtime. Kubebuilder CLI отсутствовал, поэтому scaffold написан в этой структуре вручную; CRD, RBAC и DeepCopy генерирует настоящий controller-gen. Это сохраняет привычный workflow без зависимости сборки от CLI.

Go minimum 1.26.0 соответствует controller-runtime 0.25.2; Kubernetes Go modules 0.37.0 соответствуют его go.mod. controller-gen 0.22.0 использует ту же minor Kubernetes. Версии проверены через Go module proxy; конфигурация kind соответствует версии 0.33.0/Kubernetes 1.37.0. Docker builder использует исправленный Go 1.26.9, локально установлен Go 1.26.1.

Источники: [controller-runtime go.mod](https://github.com/kubernetes-sigs/controller-runtime/blob/v0.25.2/go.mod), [controller-tools go.mod](https://github.com/kubernetes-sigs/controller-tools/blob/v0.22.0/go.mod), [kind 0.33.0](https://github.com/kubernetes-sigs/kind/releases/tag/v0.33.0), [Go releases](https://go.dev/doc/devel/release).

## 2026-10-11 — Cluster-scoped CR

CR владеет cluster-scoped Namespace и namespaced Deployment/Service. Cluster scope устраняет недопустимую owner reference от Namespace к namespaced объекту. Прямые owner references всех управляемых ресурсов дают естественные Owns watches и GC.

Источник: [Kubernetes owners/dependents](https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/).

## 2026-10-11 — Finalizer до завершения удаления Namespace

GC не даёт удобной гарантии «окружение действительно удалено» в момент исчезновения CR. Finalizer явно запрашивает Delete Namespace и ожидает NotFound. UID/resourceVersion preconditions предотвращают удаление объекта, подменённого или изменённого между чтением и Delete. Namespace берётся из функции имени, а не из редактируемого status.

## 2026-10-11 — TTL от creationTimestamp

Срок не хранится только в таймере процесса и не сбрасывается при рестарте. `expiresAt = creationTimestamp + ttlSeconds`; spec TTL можно изменить до начала удаления. Это предсказуемо, воспроизводимо и не требует отдельного scheduler. `Expired` сохраняется перед Delete и существует лишь до окончания cleanup.

## 2026-10-11 — Readiness по актуальному rollout

Одного ReadyReplicas недостаточно: старые реплики могут оставаться готовыми после смены image. Проверяем observedGeneration, updated/ready/available/total replicas. TCP probe подходит для минимального API с произвольным портом; HTTP path можно добавить позднее.

## 2026-10-11 — Optimistic-lock patch

Сохраняем неизменённые API defaults и чужие annotations, патчим управляемые поля с resourceVersion, status — отдельным subresource. Конфликт возвращается в workqueue. Нет локальной карты окружений и дополнительных mutex: Kubernetes — источник истины, очередь сериализует один ключ, разные ключи независимы.

## 2026-10-11 — Проверки по уровням

Fake client позволяет быстро проверить время и ошибки, но не моделирует API validation/defaulting/GC. Envtest добавляет настоящий API и watches; kind необходим для реальных Pod, HTTP и cleanup. Результаты этих уровней фиксируются отдельно, отсутствие среды не считается успешным тестом.

Источник: [Kubebuilder envtest](https://book.kubebuilder.io/reference/envtest).

## 2026-10-11 — Сохранение Conditions при устаревшем cache snapshot

Повторная ревизия обнаружила, что копирование всего `p.Status` в свежий объект из APIReader стирает Conditions, изменённые после чтения из informer при той же generation. Регрессионный тест подтвердил потерю ExternalCheck и неверный lastTransitionTime Ready. Теперь согласуются только принадлежащие оператору status fields, а Conditions обновляются поверх свежего списка из APIReader; resourceVersion остаётся optimistic-lock precondition. Конфликт между свежим чтением и patch возвращается в очередь, без перезаписи нового status.

## 2026-10-11 — Новый локальный кластер для каждого теста

Одно имя `previewforge` не доказывает, что существующий кластер разрешено изменять. Поэтому обе реализации demo создают только новый `previewforge-test-*`, отказываются принимать существующие кластеры/kubeconfig запуска и remote Docker endpoints. Явные context и kubeconfig конкретного запуска исключают зависимость от текущего пользовательского Kubernetes context. Кластеры сохраняются для диагностики; удаление выполняется вручную по имени, выведенному этим запуском.

## 2026-10-11 — Реальный RBAC в envtest и проверка UID

Envtest manager теперь запускается с удостоверением ServiceAccount и RBAC из поставляемых manifests, с leader election. Role для operator Namespace дополнена create/patch Event, которые использует стандартный leader election recorder. Positive/negative SelfSubjectAccessReview проверяют достаточность прав и отсутствие доступа к Secrets, удалению Pod и Lease в других Namespace. Fake client игнорирует UID precondition на Delete, поэтому проверка UID и HTTP 409 выполняются отдельно на настоящем API server.

## 2026-10-11 — Linux проверки в WSL2

На машине обнаружена Ubuntu WSL2. gcc/make/libc headers установлены в ней; Go 1.26.9 скачан в кеш проекта без смены системного Go Windows. Отдельный Linux build cache и `bin/linux` исключают смешивание бинарников платформ. Доступен `scripts/linux-checks.sh` для воспроизведения vet/build/race/envtest без Docker. Наличие этих проверок не закрывает этап 1 без Kubernetes e2e.

## CI e2e и обязательный cleanup

GitHub Actions operator-e2e workflow относится к подтверждению этапа 1. Он использует только hosted Ubuntu и свежий локальный kind, без внешних kubeconfig/credentials. Bash e2e больше не оставляет кластер: ранний trap покрывает ошибки demo и assertions, claim/token и Docker endpoint ограничивают удаление текущим запуском, always() шаг страхует обычное прерывание. Диагностика сохраняется до cleanup; job требует все check IDs и доказательство удаления. Demo для ручной работы сохраняет кластер. Подготовка и lint workflow не равны выполненному GitHub run; этап остаётся открытым до настоящего e2e.

## 2026-10-27 — Настоящий e2e через Git Bash и абсолютный deadline

Docker Engine стал доступен из Windows, но WSL Integration для Ubuntu остаётся выключенной. Штатный Bash e2e запущен через Git Bash с Windows kind/kubectl: все 13 проверок и удаление одноразового кластера подтверждены настоящим Kubernetes. Это позволяет проверить lifecycle без изменения настроек Docker/WSL; Linux race/envtest продолжают выполняться отдельно под ServiceAccount RBAC. Запуск GitHub workflow остаётся самостоятельной неподтверждённой задачей.

В реальном `kubectl get` обнаружено `<invalid>` в EXPIRES: колонка `type=date` интерпретируется как возраст и не подходит для будущего deadline. Маркер переведён в `type=string`, чтобы выводить абсолютное RFC3339 время. Схема поля `status.expiresAt`, TTL от `creationTimestamp` и логика reconcile сохраняются. Generated CRD получена controller-gen 0.22.0 через `scripts/check.ps1 -Generate`.

## 2026-10-27 — Порядок публикации и дальнейшей разработки

Пользователь выбрал репозиторий `https://github.com/T1mof/previewForge.git` и последовательность: сначала CI и локальный kind, затем демонстрационный кластер с Ingress, после него PR integration. Первый опубликованный вариант содержит проверенный lifecycle этапа 1; начатые изменения Ingress сохранены отдельно и не включены в базовый CI. Module path и Go imports приведены к фактическому репозиторию. Локальные kind artifacts относятся к прежнему module path; итоговый опубликованный commit должен получить отдельное подтверждение GitHub-hosted Linux/kind.

## 2026-10-27 — Приёмка CI этапа 1

GitHub run 40576650171 для commit 7d0a156 завершил оба job успешно. Скачанный kind artifact проверен независимо: digest GitHub API совпал, все 13 check ID и два HTTP-ответа присутствуют, result содержит ожидаемый commit, cleanup подтверждает отсутствие кластера. Это закрывает этап 1 и разрешает следующий согласованный шаг — демонстрационный кластер с Ingress. Подробности и artifact ID сохранены в [отчёте](verification/2026-10-27-stage1.md).

## 2026-10-27 — Необязательный Ingress и локальное demo

После успешной приёмки CI этапа 1 добавлен стандартный networking/v1 Ingress с явным классом и точным host. URL описывает настроенный маршрут; Ready не подменяет проверку DNS/TLS/внешнего HTTP. Отключение удаляет только принадлежащий CR Ingress с UID/RV preconditions и свежим APIReader; чужой маршрут остаётся нетронутым.

Реальная маршрутизация проверена отдельным Traefik 3.7.16 в свежем kind: e2e 14/14 с cleanup. Сохраняемое demo проверяет HTTP с хоста и использует loopback 8080; Windows реально отказала bind порта 80, неуспешный кластер удалён. Настройки ОС и пользовательский kubeconfig не изменялись. Для CI и demo добавлен ранний отказ от прежнего run directory до установки cleanup traps.
