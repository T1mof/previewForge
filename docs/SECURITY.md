# Безопасность и остаточные риски

Сейчас PreviewForge следует использовать в отдельном тестовом кластере без production credentials и ценных данных. Ownership-проверки reconcile защищают от ошибок приложения. Optional `config/hardened` дополнительно ограничивает фактические записи ServiceAccount через CEL и отдельный read-only ownership webhook; [контракт, TLS и остаточные риски](ADMISSION.md). Успешный lifecycle e2e не доказывает безопасность для недоверенных tenants.

## Широкий RBAC

Оператор может изменять Namespace, Deployment и Service во всём кластере. Права нужны текущей реализации с динамическими Namespace и общим watch. Проверка ownerReferences/UID выполняется приложением; Kubernetes RBAC не умеет ограничивать права по labels, ownerReferences или префиксу имени. Отсутствие прямого чтения Secrets недостаточно: право создавать Deployment позволяет создать Pod с Secret volume или более привилегированным ServiceAccount. Это ограничение описано в [Kubernetes RBAC good practices](https://kubernetes.io/docs/concepts/security/rbac-good-practices/#workload-creation).

Минимальные меры сейчас:

- Запускать проверку на одноразовом GitHub-hosted runner и в свежем kind-кластере; не передавать пользовательский kubeconfig, cloud credentials или production Secrets. Workflow уже использует `contents: read`, checkout без сохранения credentials и закреплённые SHA Actions. Он не использует `pull_request_target`, внешние кластеры или registry push.
- В тестовом кластере выдавать create/update/delete PreviewEnvironment только доверенным пользователям. Не выдавать им управление ServiceAccount/RBAC и произвольную запись в управляемые Namespace. Сам оператор не получает права на изменение RBAC; envtest проверяет отрицательные разрешения.
- Сохранять securityContext оператора: non-root, read-only root filesystem, запрет privilege escalation, seccomp и сброс capabilities. Для приложений отключён автоматический mount ServiceAccount token. Каждый preview Namespace получает PSA enforce baseline/restricted, ResourceQuota и LimitRange; для совместимых образов используйте `spec.securityProfile: restricted`. Точные defaults, бюджеты и ограничения — в [RESOURCE-CONTROLS.md](RESOURCE-CONTROLS.md).

`config/hardened` запрещает записи оператора вне вычисленного Namespace живого CR, сверяет owner references/UID через API и ограничивает workload ServiceAccount, Secret/hostPath mounts, hostNetwork и privileged Pods. Один префикс имени — лишь дополнительный фильтр. `config/default` этих admission-ограничений не устанавливает. Более сильное сокращение RBAC требует разделить доверенный bootstrap Namespace/RoleBinding и контроллер с namespaced Roles; динамическая выдача RBAC сама по себе привилегированна и требует отдельного проектирования. Broad read/list/watch и запись CR/NetworkPolicy внутри владений остаются отдельными рисками; общий кластер пока не является подтверждённой моделью эксплуатации.

## Сетевая изоляция и её границы

Сам Namespace не блокирует трафик. Оператор создаёт две NetworkPolicy до Deployment: isolation всего preview Namespace с внутрисредовым доступом и ограниченным DNS, затем отдельный gateway allow только к app Pod и TCP `spec.port` при включённом Ingress. Namespace/Pod selectors DNS и gateway задаёт администратор; они соединены AND. CR не может разрешить произвольный публичный egress. [Полный контракт](NETWORK-ISOLATION.md).

Субъекты с правом записи дополнительных NetworkPolicy могут расширить доступ: правила аддитивны. Удаление базовой политики создаёт окно до watch-восстановления, а применение правил CNI асинхронно. Назначение labels и запись Pod в доверенный gateway/DNS Namespace должны контролироваться администратором. Нет гарантии нулевого окна старта и немедленного разрыва уже установленных соединений.

**NetworkPolicy действует только при реальном enforcement сетевым плагином.** Наличие YAML/API объектов и Ready по TCP не доказывают изоляцию. Полный Bash e2e устанавливает закреплённый Calico в новый kind с отключённым default CNI и проверяет allow/deny с положительными контролями Pod IP/Service/public TCP. Native PowerShell/kindnet demo исполнения правил не обеспечивает. Выполненные результаты — в [TESTING.md](TESTING.md); envtest не заменяет dataplane proof. См. [Kubernetes NetworkPolicy](https://kubernetes.io/docs/concepts/services-networking/network-policies/) и [kind CNI configuration](https://kind.sigs.k8s.io/docs/user/configuration/#disable-default-cni).

NetworkPolicy не является полным firewall узла/API, не ограничивает DNS names/exfiltration или скомпрометированный cluster-wide оператор. Для недоверенных workloads нужны дополнительные admission/RBAC и host-level ограничения. Сохраняйте отдельный кластер без чувствительных сервисов и production-маршрутов; эта реализация не заявляет hostile multi-tenancy.

## Namespace удаляется целиком

Namespace является единицей lifecycle. При TTL или ручном удалении CR namespace controller удаляет **все** namespaced ресурсы внутри него, включая вручную созданные ConfigMap, Secret, PVC, чужие Deployment и ресурсы без ownerReferences PreviewForge. Проверка владельца самого Namespace не делает исключений для его содержимого. Финализация PVC/внешнего хранилища может дополнительно задержать cleanup.

Минимальные меры: не размещать общие/долговечные данные в `forge-*`; не выдавать посторонним субъектам запись в эти Namespace; разрешать запись только оператору и необходимым контроллерам через RBAC/admission. Пользователю CR должно быть известно, что TTL уничтожает окружение целиком. Сохранять UID/resourceVersion Delete preconditions и finalizer до NotFound — эти защиты уже реализованы и проверены на API.

Если требуется сохранять вручную добавленные ресурсы, текущий контракт Namespace cleanup использовать нельзя. Нужно отдельно изменить модель удаления на управляемые дочерние объекты или блокировать посторонние записи admission-правилами. Проверка содержимого перед Delete без admission не устраняет гонку с поздней записью. Сейчас такого поведения нет.

## Доступ через Ingress

Необязательный Ingress добавляет оператору cluster-wide get/list/watch/create/patch/delete только для `networking.k8s.io/ingresses`; запись Ingress status и чтение Secrets не разрешены. DNS, уникальность host и разрешённые домены остаются ответственностью администратора. В общем кластере доверенным авторам CR следует выделять отдельный домен; проверка глобальной уникальности hostname и domain admission пока не реализованы.

Ссылка TLS указывает на Secret в preview Namespace; его создаёт администратор или отдельный certificate controller. Оператор не переносит сертификаты между Namespace. Тестовый Traefik имеет отдельный ServiceAccount с Secrets read для TLS и устанавливается только в новом локальном kind. Demo публикует HTTP listener только на 127.0.0.1; это локальная демонстрация доверенного приложения.

## Границы очистки CI

Metrics Service в default install использует HTTPS и штатные TokenReview/SubjectAccessReview. Отдельная metrics-reader роль выдаёт только GET `/metrics`; default install не создаёт reader binding. Optional monitoring demo связывает её с Prometheus SA. Self-signed certificate/пропуск проверки CA и anonymous Grafana Viewer предназначены только для нового локального demo; внешнее развёртывание требует serving CA/SAN и отдельной схемы UI-доступа. Пароль admin demo создаётся случайным Secret запуска и не сохраняется в artifacts. Подробности и точная семантика inventory — в [OBSERVABILITY.md](OBSERVABILITY.md).

PR-интеграция по умолчанию выключена и предназначена для отдельного demo-кластера. CLI проверяет source Namespace UID и controller owner UID; RBAC deployer ограничен типами ресурсов, но не owner UID или полями metadata. В частности, patch основного CR может менять finalizers; CLI этого не делает. Fork PR пропускаются, build отделён от Environment credentials, deploy использует trusted base commit и повторно проверяет live PR state. Полная модель и порядок включения — в [PR-INTEGRATION.md](PR-INTEGRATION.md). Правила API/subresource описаны в [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/#referring-to-resources).

Bash e2e проверяет отсутствие кластера до создания, резервирует новый каталог и сохраняет token владения запуском вместе с локальным Docker endpoint. EXIT/INT/TERM обработчики собирают диагностику и удаляют только точное имя, соответствующее claim. После успешного удаления claim снимается; повторный cleanup ничего не удаляет. CI дополнительно выполняет cleanup с `always()`; ошибка или оставшийся кластер делают job неуспешным. Ни одна команда не выполняет `docker system prune`, удаление других кластеров или изменение пользовательского kubeconfig.

SIGKILL, потеря Docker daemon/runner и жёсткое завершение job могут не дать исполниться обработчику. На GitHub-hosted runner VM одноразовая; локально при такой аварии может остаться только тестовый кластер. Восстановление cleanup требует сохранённых имени/token и того же Docker endpoint; нельзя удалять все кластеры ради очистки одного запуска. Отдельный CI artifact `cleanup.txt` подтверждает успешное exact-name удаление и проверку отсутствия; его отсутствие нельзя считать успешной очисткой. Snapshot имён остальных кластеров и проверки маршрутизации CLI не являются проверкой содержимого чужих кластеров: к ним не подключаемся.
