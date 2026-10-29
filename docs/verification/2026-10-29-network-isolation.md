# NetworkPolicy: подтверждённый Calico enforcement

Проверено 2026-10-29. [Operator verification 40915413741](https://github.com/T1mof/previewForge/actions/runs/40915413741), source `cfb3946a588bf22aa514d2e4b6ae059606f5e965`: оба job **SUCCESS**, настоящий kind/Calico suite **18/18**, cleanup и report gate **PASS**. Новые NetworkPolicy протестированы поверх Kubernetes 1.37.0 и Calico 3.33.0, с одним Linux/amd64 control-plane node.

## Проверка artifact

| Поле | Значение |
| --- | --- |
| Artifact | `kind-e2e-40915413741-1` |
| ID | `12916698603` |
| SHA256 из API и скачанного ZIP | `0d1ab4a99b52fd6518d865a95f8f8281d0abefb50d3d6e135d7a24b60479a9f5` |
| Кластер | `previewforge-test-ci-40915413741-1` |
| Проверки | 18 уникальных check ID |
| E2E / cleanup exit | 0 / 0 |

Архив скачан отдельно. Независимо проверены digest, source SHA, все check ID, десять network подпроверок, **21 положительный и 17 отрицательных** probe JSON, конфигурация policy peers/ports, новые UID и точные controller owner UID, отсутствие обоих preview Namespace и exact-name cleanup kind. Проверены также все прежние сценарии: HTTP, PR revision create/update/delete без timeout retries, metrics HTTPS 401/403, inventory 2→3→3→2, Prometheus/rule tests и девять Grafana queries. Среди имён ZIP нет kubeconfig/claim файлов. Локальная копия — `bin/ci/github-artifact-12916698603.zip`, машинный итог — `.verified.json` рядом.

## Что доказал dataplane

- Отдельный Pod внутри preview обращался к приложению через Service DNS и Pod IP; само приложение разрешало DNS UDP и TCP к CoreDNS.
- Из другого preview запрещены обращения в обе стороны; проверены Pod IP и Service. Отдельный сервер вне preview был доступен снаружи до/после теста, но недоступен приложению через Pod IP и Service IP.
- HTTP на TCP 53 у постороннего сервера был доступен снаружи и заблокирован из preview: DNS allow не превратился в разрешение произвольного port-53 egress.
- Один и тот же публичный IPv4/TCP 443 target был доступен контрольному Pod до/после, но не приложению. Verifier проверил глобальность IP и совпадение target всех трёх запросов.
- Поддельный gateway label в другом Namespace и неправильный Pod label в правильном gateway Namespace не дали доступ. Совпадение обоих selectors разрешило только текущий app port; дополнительный реально работающий порт приложения оставался закрыт для gateway.
- Настоящий Traefik направлял HTTP по host. После изменения app port HTTP продолжил работать, а старый gateway port закрылся. Отключение Ingress отозвало прямой gateway-доступ, сохранив локальный HTTP.
- Watch восстановил обе удалённые политики с новыми UID и тем же CR owner UID, исправил port drift; allow/deny сохранились после рестарта оператора. После удаления CR оба Namespace полностью исчезли.

Negative probes требовали транспортную ошибку, а не HTTP 403. Для отрицательных запросов использовались literal IP и положительные контроли доступности, чтобы не принять DNS/приложение, которое не работает, за изоляцию. Calico DaemonSet/controller были Ready; проверены все три закреплённых image digest. Evidence — `network-*.json`, `network-checks.txt`, `network-cleanup.txt`, общий `checks.txt/result.txt/cleanup.txt`.

## Остальные проверки и ограничения

CI Linux race: controller 6,135 с, probe 1,029 с; envtest 10,58 с / пакет 11,674 с под manifest ServiceAccount RBAC. Локальные Windows generation/gofmt/vet/test/build и Linux race/envtest также прошли; envtest подтверждает API/watch/RBAC, а не dataplane. Подробности — в [TESTING.md](../TESTING.md).

Первый CI 40904321044 / b341df1: Linux SUCCESS, kind FAIL после десяти базовых Calico checks, cleanup PASS. Probe не собрался из-за исключённого `test` в `.dockerignore`; итоговый source исправляет build context. Этот первый run не считается успешным network e2e.

Локальный bootstrap `previewforge-test-calico-20261029-a` подтвердил Ready Calico/CoreDNS/operator/nginx и cleanup; он не выполнял проверки сетевых запретов. Полный локальный run b прервался до kubeconfig и не имеет результатов. Его endpoint-файл оказался заполнен нулевыми байтами; автоматический cleanup корректно отказался подтверждать Docker endpoint. После отдельного явного разрешения пользователя `previewforge-test-network-20261029-b` удалён вручную из проверенного локального Docker Desktop, с проверкой конкретного container ID. Подтверждены отсутствие кластера/контейнера и неизменность имён других кластеров; kind-кластеров не осталось. Claim снят, повреждённые записи сохранены; proof — `bin/runs/previewforge-test-network-20261029-b/artifacts/manual-cleanup.json` и `.txt`. **Ручная очистка PASS**, но это не успешный автоматический guard и не локальный полный 18/18. Удаление удалённого CI-кластера независимо подтверждено artifact.

Проверка не подтверждает IPv6/multi-node/provider matrix, защиту node/hostNetwork/API, DNS-name filtering, hostile multi-tenancy или внешний PR lifecycle. Дополнительные allow policies и admin deletion могут расширить доступ; CNI применяет изменения асинхронно. Внешний PR workflow остаётся выключенным. [Сетевой контракт](../NETWORK-ISOLATION.md), [модель доверия](../SECURITY.md).
