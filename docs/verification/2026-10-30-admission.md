# Admission: проверка границ записи оператора

Дата фиксации: 2026-10-30. Source:
[`b5a0af055519d0f35a83aa6358bf5702101808a6`](https://github.com/T1mof/previewForge/commit/b5a0af055519d0f35a83aa6358bf5702101808a6).
[CI run 40938752311](https://github.com/T1mof/previewForge/actions/runs/40938752311):
оба job **SUCCESS**, настоящий Calico kind **19/19**, report gate и cleanup **PASS**.

| Доказательство | Результат |
| --- | --- |
| Linux job `123678707610` | vet/build/race, обе envtest suite и script checks PASS |
| Kind job `123681388135` | настоящие Pods, HTTP, admission, сеть и GC PASS |
| Artifact `12927776379` | `kind-e2e-40938752311-1`, 1 225 375 байт, скачан и проверен отдельно |
| SHA256 ZIP | `c994cb5fc82b389671f29b3483b97bfdb49e6b90b25f9d24e1e0f00582973869` — совпал с GitHub API и локальным Get-FileHash |
| Кластер CI | `previewforge-test-ci-40938752311-1`, удалён; claim снят, отсутствие проверено |

## Независимая проверка скачанного artifact

- Source SHA, E2E/cleanup exit code 0, ровно 19 различных check ID и удаление точного кластера.
- Четыре CEL policy: `observedGeneration == generation`, expression warnings отсутствуют, `failurePolicy: Fail`, точная identity; четыре binding с `Deny, Audit`.
- Отдельный ownership webhook: точная identity, доверенный CA bundle и `failurePolicy: Fail`.
- Восемь настоящих SelfSubjectAccessReview: оператору разрешены четыре проверяемые операции записи; webhook SA разрешено читать Namespace/CR, запрещено patch Namespace и читать Secrets.
- Десять отказов с соответствующими policy/webhook: захват и удаление чужого Namespace, дочерний объект в чужом Namespace, захват чужого Service, hostNetwork, SA token, privileged, Secret volume, удалённый kubelet probe и external IP Service.
- Чужой Namespace сохранил UID/resourceVersion и отсутствие owner references. Захват Service внутри своего Namespace тоже отклонён; неизменность его UID/resourceVersion проверяет e2e.
- Обычные принадлежащие оператору записи разрешены. Остановка webhook закрыла API-запись; после рестарта нормальный запрос прошёл. В CI потребовалась одна recovery-попытка. Только transport errors повторяются в пределах 60 секунд; policy/ownership denial сразу проваливает тест.
- Прежние результаты: HTTP/Ingress и PR-fixture revision; inventory `2 → 3 → 3 → 2`; Prometheus/promtool и девять Grafana panels; guardrails; 21 разрешённая и 17 запрещённых сетевых probes, десять network подпроверок и Namespace cleanup.
- В ZIP нет kubeconfig, claim, TLS Secret manifest/key или PEM private key material. Demo TLS находится вне artifacts.

Это настоящая проверка API и dataplane в kind с Calico 3.33.0. Опасные
Deployment-кандидаты используют server dry-run и никогда не планируются на node.
`--validate=false` отключает клиентскую проверку kubectl, пытавшуюся list CRD;
валидация native API и admission выполняются сервером. RBAC ради теста не расширялся.

## Локальные проверки и границы доказательств

Windows `scripts/check.ps1`: gofmt/vet/test/build и рендер manifests — **PASS**.
Сертификатный unit test проверяет цепочку доверия, SAN, срок действия и отказ
перезаписать ключ. Reader ClusterRole сгенерирован controller-gen 0.22.0 из Go
markers. Native fake CLI **7/7 PASS**. Linux fake CLI 11, cleanup 21, synthetic
report gate 4, metadata 8 и shellcheck/actionlint — **PASS**, это не Kubernetes e2e.

Полный Linux `scripts/linux-checks.sh` **PASS**, exit code 0 записан внутри Bash:
race controller 4,155 с, probe 1,090 с; admission envtest 15,33 с, прежняя envtest
11,01 с, пакет 27,575 с. Первая PowerShell-обёртка вернула 1 из-за stderr
предупреждения WSL при успешных Go suite; повтор с Bash log/exit record снял
неоднозначность. В итоговом CI: race controller 3,802 с; admission envtest 8,60 с,
прежняя envtest 9,81 с, пакет 19,470 с.

Envtest использует настоящий API server и TLS webhook с manifest read-only
ServiceAccount; оператор также использует manifest RBAC. Проверены живой UID
CR, вычисленное имя Namespace, реальный owner Namespace, запреты захвата/смены
owner, Pod/Service ограничения и положительные записи. Envtest не имеет
kubelet, policy status controller или GC; type warnings, Pods и удаление
Namespace подтверждены в kind.

Локальный `previewforge-test-admission-20261029-c` на том же source подтвердил
весь admission-блок: десять запретов, восемь reviews, fail-closed, положительные
записи и удаление обоих fixture Namespace. Recovery потребовал две попытки:
transport error после Pod Ready, затем успех. **Полный локальный 19/19 не
заявляется**: дальнейший monitoring остановился на timeout загрузки образов
Prometheus/Grafana. Лог: `bin/ci/previewforge-test-admission-20261029-c.log`;
artifacts: `bin/runs/previewforge-test-admission-20261029-c/artifacts/`.

Первый cleanup `c` получил Docker error «did not receive an exit event»;
исходный `result.txt` сохраняет E2E 1 / cleanup 1. После перехода контейнера в
Exited повтор прошёл обычные проверки сохранённых claim/token и endpoint,
дополнительно проверил container ID
`04100e43b455127664520a6baa4683482a9088ec5ca21e783d7f12d5038008eb`
и удалил только этот кластер. `cleanup-retry.txt` и `cleanup.txt` подтверждают
отсутствие кластера и неизменность списка остальных кластеров. Claim снят;
identity records и пользовательский kubeconfig не переписывались.
На момент завершения работы локальных kind-кластеров не осталось.

## Исправления, найденные проверками

- [CI 40934626378](https://github.com/T1mof/previewForge/actions/runs/40934626378), `9e959cd`: Linux PASS, kind остановился до установки оператора из-за отсутствия сборки `admissioncert`; cleanup PASS. Исправлено в `03fd105`.
- CI `40936742711` / `03fd105` отменён после следующего исправления, когда выполнялся только Linux job и kind job отсутствовал. Запуски с работающим kind не отменялись.
- Локальный `a` / `9e959cd`: настоящий network enforcement PASS, затем client OpenAPI kubectl потребовал запрещённый CRD list; cleanup PASS. Исправление `75e09bf` не расширяет RBAC и переносит admission перед monitoring для раннего обнаружения ошибок.
- Локальный `b` и [CI 40937365785](https://github.com/T1mof/previewForge/actions/runs/40937365785) / `75e09bf`: все десять отказов сработали; немедленный запрос после рестарта webhook попал в ещё не обновившийся Service route. Оба кластера очищены. `b5a0af0` ожидает фактически успешный запрос, сохраняя промежуточные ответы; итоговый полный CI выше PASS.

## Эксплуатационный контракт

`config/hardened` опционален и включён в kind demo; `config/default` сам admission
не устанавливает. TLS demo действует 48 часов; постоянной установке нужны
собственная ротация TLS/CA и доступность webhook. Identity закреплена за default
namespace/ServiceAccount. Широкие reads, CR write, выбор image/host и изменение
quota/NetworkPolicy внутри принадлежащих окружений остаются ограничениями.
Webhook — отдельный доверенный компонент; reads не составляют транзакцию с
записью. Это не hostile multi-tenancy. [Полный контракт](../ADMISSION.md).

Внешний PR workflow/registry publish не активированы: repository gate
`PREVIEWFORGE_PR_ENABLED` отсутствует/false, Environment `preview` отсутствует.
Площадка и домен выбираются позже; PR-fixture не выдаётся за внешний workflow.
Следующие этапы — Helm, нагрузочные/отказные испытания и Kubernetes version matrix.
