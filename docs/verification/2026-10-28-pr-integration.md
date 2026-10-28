# Подтверждение подготовки PR-интеграции

[GitHub run 40640242526](https://github.com/T1mof/previewForge/actions/runs/40640242526) — **SUCCESS**, оба job. Проверенный commit: `ad728297b367d6316995d8ad6642fbe1c5cc6baa`.

## Linux и Kubernetes

Linux vet/build/race/envtest, metadata unit tests и регрессии скриптов — **PASS**. Race: example 1,011 с, controller 3,059 с, prenvironment 1,810 с. Envtest 8,80 с / пакет 9,851 с; настоящий API server и ограниченный deployer ServiceAccount RBAC, optimistic patch, HTTP 409 и сохранение новой UID при замене CR перед Delete.

Kind `previewforge-test-ci-40640242526-1`: **15/15 PASS**, e2e exit 0, cleanup exit 0; обязательные report gate и `always()` cleanup — SUCCESS. Сохранены прежние проверки lifecycle/Ingress; новый check `pr-integration` выполнил:

- Две реальные Docker-сборки example Go HTTP server и запуск immutable OCI digests.
- CLI create/update через ограниченный SA с сохранением UID, creationTimestamp и Namespace; текущая generation Ready.
- HTTP через Traefik с revision `0000000000000000000000000000000000000001` и `0000000000000000000000000000000000000002`, оба `/healthz` — `ok`.
- Отказ неверного source Namespace UID и чужого CR с поддельными labels.
- Delete с ожиданием удаления CR/Namespace и успешный повторный delete.

В HTTP логе есть один `wget: download timed out` перед успешным повтором второй версии. Retry охватывает revision и health вместе; обе успешные пары ответов проверены отдельно. Это устраняет обнаруженный в первом CI дефект теста, не снижая требования к HTTP-результату.

## Проверенный artifact

[kind-e2e-40640242526-1](https://github.com/T1mof/previewForge/actions/runs/40640242526/artifacts/12795632577), ID `12795632577`, SHA256:

```text
2b0351b49a66aeae64d1b4b4e281e2871977ab7c4366cec30f5ab85ec4c39ca8
```

Архив скачан; SHA совпал с GitHub API, commit совпал с ожидаемым. Проверены все 15 различных check ID, два обычных nginx ответа, Ingress ответы/404, JSON create/update с одной UID/Namespace и generation 1/2, обе HTTP revision и health, delete исходной UID и повторный delete без UID, а также подтверждение отсутствия кластера. Файлы kubeconfig/claim в архив не включены. Локальная копия — `bin/ci/github-artifact-12795632577.zip`, отчёт проверки — соседний `.verified.json`. GitHub хранит artifact 7 дней.

## Локальная проверка и предел результата

Новый локальный `previewforge-test-pr-20261028-d` также прошёл **15/15**, cleanup и report gate; кластер удалён. Локальные artifacts находятся в `.cache/worktrees/pr-integration/bin/runs/previewforge-test-pr-20261028-d/artifacts/`. Неуспешные промежуточные попытки и первый CI записаны в [TESTING.md](../TESTING.md).

GitHub PR metadata в kind — **fixture**. Настоящие GitHub PR события с GHCR publish и внешним кластером — **NOT RUN**. По решению пользователя подготовлена интеграция для отдельного кластера, размещение и домен выбираются позже. Read-only проверка GitHub подтвердила отсутствие `PREVIEWFORGE_PR_ENABLED` и Environment `preview`; внешний workflow выключен. [Порядок включения](../PR-INTEGRATION.md).

Свежее demo `previewforge-test-demo-20261028-c` отдельно проверено HTTP с Windows-хоста: `http://localhost:8080`, HTTP 200. Оно использует ранее проверенный Ingress operator; production Go-код оператора в PR-подготовке не менялся. Кластер сохранён, приложение подчиняется TTL до 09:57:25 МСК 2026-10-28. Публичный DNS, TLS handshake и provider OIDC этим результатом не подтверждаются.
