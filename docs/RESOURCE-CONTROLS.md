# Ресурсы и Pod Security

Каждое окружение получает `ResourceQuota/previewforge-budget`, `LimitRange/previewforge-defaults` и Pod Security labels своего Namespace. Эти объекты принадлежат точному UID `PreviewEnvironment`; чужие объекты с теми же именами блокируют reconcile. Watch восстанавливает удалённые объекты и исправляет drift с optimistic locking. Finalizer сохраняется до NotFound принадлежащего CR Namespace.

## API

```yaml
apiVersion: platform.previewforge.dev/v1alpha1
kind: PreviewEnvironment
metadata:
  name: restricted-example
spec:
  image: ghcr.io/your-org/your-app@sha256:<digest>
  port: 8080
  securityProfile: restricted
  resources:
    requests: {cpu: 200m, memory: 128Mi}
    limits: {cpu: 750m, memory: 512Mi}
  ttlSeconds: 3600
```

Замените image на существующий образ. Ресурсы задаются Kubernetes quantities; CEL отклоняет некорректные значения, request выше limit и превышение диапазонов. CLI и reconciler используют ту же проверку в Go. При отсутствии `resources` reconciler применяет defaults; при `resources: {}` CRD заполняет вложенные поля.

| Поле контейнера | Default | Допустимый диапазон |
| --- | --- | --- |
| requests.cpu | 100m | 1m–500m |
| requests.memory | 64Mi | 16Mi–512Mi |
| limits.cpu | 500m | 1m–1 |
| limits.memory | 256Mi | 16Mi–1Gi |

`previewctl apply` принимает `--security-profile restricted`, `--cpu-request`, `--memory-request`, `--cpu-limit`, `--memory-limit`. Если задан один resource flag, остальные получают defaults. При повторном apply указывайте нужный профиль и ресурсы: CLI согласует полную спецификацию. PR workflow явно выбирает `restricted` для совместимого example image и остаётся выключенным до выбора отдельного кластера.

## Бюджет Namespace

| ResourceQuota | Значение |
| --- | --- |
| requests.cpu / limits.cpu | 8 / 16 |
| requests.memory / limits.memory | 8Gi / 16Gi |
| pods (незавершённые) / count/pods (все) | 15 / 32 |
| count/deployments.apps / count/replicasets.apps | 1 / 16 |
| count/services | 2 |
| count/secrets / count/configmaps | 8 / 8 |

Бюджет фиксирован, чтобы изменение размера приложения не требовало сначала уменьшить quota ниже ресурсов уже работающих Pod. Десять максимальных реплик, один surge Pod и четыре Pod с defaults укладываются в него. Deployment использует `maxSurge: 1`, `maxUnavailable: 0`, хранит три старых ReplicaSet. Завершающиеся Pod тоже могут временно занимать ресурсы: quota вправе задержать rollout до их удаления. Вручную добавленные Pod расходуют тот же бюджет. Завершённые debug Pod учитываются в `count/pods`; удаляйте их после диагностики.

LimitRange задаёт container defaults из таблицы, минимум 1m/16Mi и максимум 1 CPU/1Gi. Вручную созданный Pod получает requests/limits, превышение container maximum отклоняется admission. Новые quota/LimitRange не выселяют и не изменяют ранее созданные вручную Pod; управляемое приложение обновляется через Deployment rollout. ResourceQuota ограничивает сумму объявленных ресурсов и число объектов, а не резервирует физическую мощность и не ограничивает общее количество PreviewEnvironment. Это не механизм глобальной справедливости или защиты от node disk pressure. См. [ResourceQuota](https://kubernetes.io/docs/concepts/policy/resource-quotas/) и [LimitRange](https://kubernetes.io/docs/concepts/policy/limit-range/).

## Профили безопасности

Default `baseline` сохраняет совместимость с обычным nginx, которому нужны root и отдельные capabilities при старте. Namespace enforce использует baseline, warn/audit — restricted. Управляемый Pod отключает ServiceAccount token и service links, использует seccomp RuntimeDefault, запрещает privilege escalation и сбрасывает NET_RAW.

`restricted` дополнительно запускает приложение с UID/GID/FSGroup 65532, `runAsNonRoot`, drop ALL и read-only root filesystem; `/tmp` доступен через emptyDir с sizeLimit 64Mi. Образ должен поддерживать этот UID, read-only filesystem и выбранный порт. Namespace admission проверяет все новые Pod, включая созданные вручную; требования описаны в [Pod Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/). Read-only root filesystem — дополнительная настройка управляемого приложения, а не требование PSA ко всем Pod.

Профиль restricted нельзя понизить через API. Уже установленный restricted enforce Namespace также не понижается: baseline CR получает `SecurityProfileConflict` до явного перехода на restricted. Версия политики закреплена на v1.37; более новая версия или `latest`, заданная администратором, сохраняется. Обновление labels не исправляет ранее созданные вручную Pod; operator обновляет только свой Deployment.

Operator управляет всей `Deployment.spec.template.spec`: исправляет hostNetwork/privileged/ServiceAccount drift и убирает вручную добавленные init containers, mounts, sidecars, env и другие поля, которых нет в API. Для расширений сначала нужна явная модель в CRD. Annotations и посторонние labels сохраняются.

## Границы защиты и проверок

Авторы CR и администраторы должны оставаться доверенными. Оператор имеет cluster-wide права создания workloads и изменения Namespace labels; скомпрометированный оператор может обойти собственные настройки. Удаление администратором quota/LimitRange создаёт окно до восстановления; watch не заменяет admission/RBAC-запрет на такие изменения. Сетевая изоляция задаётся [NetworkPolicy](NETWORK-ISOLATION.md), исполнение требует поддерживающего CNI. Дополнительный анализ — в [SECURITY.md](SECURITY.md).

Unit-тесты проверяют границы ресурсов, ownership, drift и бюджет rollout. Linux envtest проверяет CEL, defaults, переход профиля, RBAC и настоящие watch. Он не содержит quota controller и не подтверждает resource admission или запуск Pod. `scripts/e2e-guardrails.sh` проверяет restricted app Ready, defaulting, отказ privileged/oversized/CPU-overquota Pod, восстановление объектов и cleanup Namespace. Сценарий прошёл в GitHub-hosted kind: [17/17 и проверенный artifact](verification/2026-10-29-resource-controls.md). Остальные результаты — в [TESTING.md](TESTING.md).
