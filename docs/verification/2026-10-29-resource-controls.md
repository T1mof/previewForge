# Resource controls и Pod Security: подтверждённый CI

Проверка выполнена 2026-10-29. [Operator verification 40890879045](https://github.com/T1mof/previewForge/actions/runs/40890879045) проверил итоговый source commit `b5343c9845240d29ed828abcbe27f92c2051fdfb`, включая исправления Git Bash. Оба job **SUCCESS**: Linux race/envtest и настоящий kind lifecycle/admission с обязательной очисткой. Тот же source прошёл локальный полный suite 17/17.

## Независимая проверка artifact

| Поле | Значение |
| --- | --- |
| Artifact | `kind-e2e-40890879045-1` |
| ID | `12906427751` |
| SHA256 из GitHub API и скачанного ZIP | `2a436c2cdcb62e6e2799bd9eb9eb7e37d348b4a85348adb51ee26241d9d2eb88` |
| Кластер | `previewforge-test-ci-40890879045-1` |
| Проверки | 17 уникальных check ID, все PASS |
| E2E / cleanup exit | 0 / 0 |

ZIP скачан отдельно. Проверены source SHA, точный набор 17 check ID, HTTP, immutable PR create/update/delete с той же UID, metrics HTTPS 401/403, inventory 2→3→3→2, Prometheus target/query, rule tests и девять Grafana panel queries. Повторных PR HTTP timeout в этом прогоне не было. Подтверждены exact-name удаление тестового кластера и отсутствие kubeconfig/claim файлов среди имён ZIP. Локальная копия — `bin/ci/github-artifact-12906427751.zip`, машинный результат — `.verified.json` рядом.

## Что доказал новый сценарий

- Настоящий example Deployment стал Ready с requests 200m/128Mi, limits 750m/512Mi, UID 65532, seccomp RuntimeDefault, drop ALL, read-only root filesystem и без ServiceAccount token. Namespace enforce — restricted v1.37.
- Pod без resources получил LimitRange defaults 100m/64Mi и 500m/256Mi.
- Privileged Pod отклонён API с `violates PodSecurity` и указанием privileged/privilege escalation.
- Контейнер с memory request/limit 2Gi отклонён LimitRange: максимум 1Gi.
- После десяти разрешённых тестовых Pod запрос ещё одного на 750m CPU отклонён ResourceQuota: использовано 7700m, лимит 8 CPU. Все три отклонённых Pod отсутствовали в API.
- Принадлежащие CR ResourceQuota и LimitRange восстановились после удаления с новыми UID, сохранили controller owner UID и исправили drift бюджета/defaults. После удаления CR Namespace полностью исчез.
- PR CLI также работал с restricted-профилем и resource flags; fixture PR metadata не считается внешним GitHub PR lifecycle.

Evidence: `guardrails-{namespace,deployment,defaults,quota,limits,restored}.json`, три `guardrails-*-rejected.txt`, восемь подпроверок `guardrails-checks.txt`, общий `checks.txt`, `result.txt`, `cleanup.txt` в artifact. Это настоящий API admission, Pods и GC в одноразовом GitHub-hosted kind, а не fake CLI или envtest.

## Остальные проверки и границы

CI Linux race: controller 3,601 с, policy 1,018 с, prenvironment 1,990 с; envtest 8,37 с / пакет 9,425 с. Envtest использовал manifest RBAC ServiceAccount, подтвердил CEL/defaults, запрет downgrade и quota/LimitRange watch. Windows generation/gofmt/vet/test/build и Linux локальные vet/build/race/envtest также прошли; подробности — в [TESTING.md](../TESTING.md).

Локальный полный Git Bash/kind `previewforge-test-guardrails-20261029-c`, source `b5343c9845240d29ed828abcbe27f92c2051fdfb`: **17/17 PASS**, E2E/cleanup exit 0/0, report gate PASS. Подтверждены и все monitoring-проверки, включая promtool и девять запросов Grafana. Кластер удалён. Evidence — `bin/runs/previewforge-test-guardrails-20261029-c/artifacts/`, лог — `bin/ci/previewforge-test-guardrails-20261029-c.log`.

Предшествующие локальные a/b — **FAIL**, cleanup обоих **PASS**. Первый обнаружил преобразование container path `/bin/promtool` в Windows-путь Git Bash; второй после успешного promtool — CRLF в сгенерированных Windows Python строках URL панелей. Итоговый скрипт выборочно исключает container paths из MSYS conversion и убирает завершающий CR при чтении запросов. Native argument/file probes, полный новый кластер c и итоговый CI подтвердили исправления. Кластеры a/b не переиспользовались, системные настройки не менялись. Первый успешный CI до этих исправлений — run 40757615791, source 714f02f; его artifact 12848136039 также был независимо проверен.

Внешний кластер/PR deploy не активированы. NetworkPolicy/CNI, глобальная cluster quota/fairness, дополнительные ограничения cluster-wide прав оператора, load/fault испытания и Kubernetes version matrix этим прогоном не проверены. Контракт и модель доверия — в [RESOURCE-CONTROLS.md](../RESOURCE-CONTROLS.md) и [SECURITY.md](../SECURITY.md).
