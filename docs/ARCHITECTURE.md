# Архитектура PreviewForge

## Источник истины

Kubernetes API хранит spec, status, owner references, finalizers и время создания. Внешняя БД и in-memory registry окружений не нужны. После рестарта informer перечисляет CR, очередь заново запускает reconcile, а deadline выводится из metadata и spec. `Now` внедряется только для тестирования времени.

## Scope и владение

`PreviewEnvironment` имеет scope Cluster. Namespace — cluster-scoped dependent; namespaced CR не может быть его владельцем. Namespace, Deployment, Service и необязательный Ingress имеют прямую controller owner reference на CR с UID. Это допустимо для cluster-scoped владельца и позволяет `Owns` отправлять события этих типов в очередь CR.

Имя Namespace: `forge-<нормализованное имя до 40 символов>-<12 hex SHA-256 UID>`. Оно укладывается в DNS label, стабильно после рестарта, отличается при пересоздании одноимённого CR. Хеш не заменяет проверку владельца: даже при коллизии существующий чужой ресурс не принимается под управление.

Labels `app.kubernetes.io/managed-by=previewforge` и `platform.previewforge.dev/owner-uid` служат диагностике/селекторам. Авторизация изменений требует controller reference с правильными APIVersion, Kind, Name и UID. Изменённый `status.namespace` не влияет на адрес удаления.

## Reconcile

1. Прочитать CR; NotFound завершает обработку, deletionTimestamp переводит в cleanup.
2. Сохранить finalizer до создания ресурсов.
3. Нормализовать/проверить spec, вычислить Namespace и deadline.
4. При истёкшем TTL записать `Expired` и запросить удаление CR с UID/resourceVersion preconditions.
5. Создать или согласовать Namespace и PSA labels; при Terminating дождаться удаления, более строгую политику не понижать.
6. Создать/согласовать ResourceQuota, LimitRange и две NetworkPolicy с проверкой owner UID; при их удалении дождаться восстановления. Gateway peer задаёт администратор; CR не может разрешить произвольный egress.
7. Создать/согласовать Deployment, Service и необязательный Ingress; при отключении дождаться удаления принадлежащего CR маршрута.
8. Определить готовность rollout, записать status при изменении.
9. Перепроверить через минимум из 30 секунд и оставшегося TTL. Watch обычно реагирует быстрее.

Ошибки возвращаются controller-runtime; workqueue выполняет rate-limited retry. Частично созданное окружение допустимо: следующий вызов продолжит согласование. `AlreadyExists` после cache miss и resourceVersion conflicts также возвращаются для retry, без опасного принятия чужих ресурсов.

Оператор контролирует Namespace labels, quota/LimitRange spec, Deployment replicas/selector/strategy, labels Pod template и всю PodSpec: единственный app container, resources/readiness, ServiceAccount и securityContext. Service type/selector/ports также согласуются. Сторонние annotations/labels сохраняются, API defaults учитываются для идемпотентности. Произвольные env, volumes, sidecars и securityContext не входят в API и удаляются из управляемой PodSpec при drift repair. Во время редактирования CR старый reconcile может кратко применить прежнюю конфигурацию; generation guard запрещает опубликовать такой status как актуальный, следующее событие согласует новый spec.

## Status

| Phase | Значение |
| --- | --- |
| Pending | Начало создания или ожидание удаления Namespace |
| Deploying | Ресурсы созданы, актуальный rollout ещё не готов |
| Ready | Deployment наблюдает текущую generation; desired, updated, ready, available и total replicas совпадают |
| Failed | Ошибка API/ownership/validation, ReplicaFailure или ProgressDeadlineExceeded |
| Expired | TTL истёк; очистка запрошена |

`Ready` condition включает reason, message, observedGeneration и lastTransitionTime. Status содержит namespace, внутренний Service endpoint, expiresAt и readyReplicas. Диагностика image pull/scheduling дополнительно доступна через Kubernetes Pod events. `Failed` повторно проверяется; устранение ошибки может вернуть окружение в Deploying/Ready.

## Удаление

Finalizer читает рассчитанный Namespace через uncached APIReader и проверяет владельца. Запрос Delete содержит UID и resourceVersion. Namespace controller удаляет всё содержимое Namespace; finalizer CR удаляется только после NotFound Namespace. Garbage collection по owner references является дополнительной гарантией, но normal cleanup не зависит только от GC.

Чужой Namespace, недоступный API или блокирующие finalizers оставляют CR в Terminating с повторными попытками и ошибкой в логах. Нельзя безусловно освобождать finalizer в этих случаях: это скрыло бы незавершённую очистку. Весь Namespace принадлежит окружению, поэтому ручные ресурсы, помещённые туда, тоже удаляются.

## Конкурентность

Manager запускает четыре worker; workqueue сериализует обработку одного ключа внутри процесса. Несколько CR имеют разные Namespace и selectors. Optimistic-lock merge patch защищает запись от устаревшего resourceVersion. Перед status patch APIReader проверяет актуальные UID, generation и отсутствие deletionTimestamp. Conditions берутся из этого свежего чтения: старый cache snapshot не должен удалять более новые Conditions или сбрасывать lastTransitionTime без смены статуса. Deployment оператора использует namespaced Lease для leader election; одновременно действует один лидер.

## Границы изоляции

Оператор не читает Secrets и не управляет Pod напрямую. ClusterRole ограничена PreviewEnvironment, Namespace, Deployment, Service и Ingress; Lease и запись Event для leader election доступны через Role только в `previewforge-system`. Динамические Namespace требуют cluster-wide разрешений на перечисление и создание ресурсов: RBAC не умеет ограничивать права произвольными owner labels. Ownership guards являются логикой приложения, а не защитой от компрометации самого оператора.

Requests по умолчанию 100m/64Mi, limits 500m/256Mi; API допускает настройку в ограниченном диапазоне. Поддерживаются 1–10 реплик, один surge Pod и TCP readiness. ServiceAccount token отключён. До Deployment создаются принадлежащие CR ResourceQuota/LimitRange/NetworkPolicy и согласуются Namespace Pod Security labels. Профили и бюджеты — в [RESOURCE-CONTROLS.md](RESOURCE-CONTROLS.md), DNS/gateway peers и необходимость реального CNI enforcement — в [NETWORK-ISOLATION.md](NETWORK-ISOLATION.md). Ready по TCP не доказывает изоляцию CNI. Дополнительные ограничения cluster-wide прав оператора остаются будущей работой; проект предназначен для доверенных приложений в отдельном тестовом кластере.

## Необязательный Ingress

`spec.ingress` задаёт точный DNS host, IngressClass и необязательную ссылку на TLS Secret в preview Namespace. Стандартный networking/v1 Ingress направляет `/` в именованный Service port. Он использует общий ownership/optimistic-lock reconcile и Owns watch. При отключении APIReader заново проверяет owner UID перед Delete с UID/RV preconditions; Ready ждёт исчезновения удаляемого маршрута. `status.url` описывает конфигурацию маршрута, а внешняя доступность проверяется отдельным HTTP e2e. PreviewForge не получает Secrets RBAC; тестовый Traefik использует отдельный ServiceAccount. Подробности — [INGRESS.md](INGRESS.md).

## Проверяемость

Unit-тесты проверяют логику с fake client и управляемыми часами. Fake client данной версии проверяет resourceVersion на Delete, но не UID precondition; UID дополнительно проверяется через настоящий API в envtest. Envtest запускает manager под ServiceAccount RBAC с leader election, проверяет CRD/defaulting/status, HTTP 409, настоящий watch/restart manager, но явно симулирует Deployment status. Полный lifecycle, HTTP, rollout и GC требуют kind. Тестовые scripts создают только новый локальный previewforge-test-* и отказываются использовать существующий кластер или удалённый Docker endpoint. См. [TESTING.md](TESTING.md) и [VERIFICATION.md](VERIFICATION.md).

## Helm как слой установки

Chart `charts/previewforge` упаковывает существующий оператор, leader-election RBAC, HTTPS metrics и optional admission. API и состояние окружений по-прежнему находятся в Kubernetes. Canonical CRD/RBAC/CEL синхронизируются в package из `config/`; проверки сравнивают копии и отрендеренные правила, чтобы упаковка не меняла права и политику владения. Namespace установки задаётся release; одна identity согласована между пятью matchConditions и отдельным webhook через validated `--operator-identity`.

Поддерживается один cluster-wide controller installation на кластер с фиксированными global resource names. Helm проверяет ownership release при конфликте. CRD хранится в `crds/`, сохраняется после uninstall и обновляется отдельно администратором. Chart не создаёт пользовательские CR, Namespace установки или private TLS Secret. Cleanup конкретных окружений завершается до uninstall; release upgrade/rollback меняет Deployment оператора, сохраняя UID окружения. [Установка](HELM.md), [проверенный lifecycle 20/20](verification/2026-10-30-helm.md).