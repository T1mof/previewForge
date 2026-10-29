# Проверки

Актуализировано: 2026-10-29. Среда: Windows/amd64, Go 1.26.1; Ubuntu WSL2/Linux amd64, Go 1.26.9 и gcc 13.3. История ниже разделяет локальные проверки и GitHub-hosted kind; этап 1 завершён.

## Admission — выполняется проверка (2026-10-29)

Подготовлены optional `config/hardened`, четыре CEL policy и отдельный TLS ownership webhook с manifest read-only ServiceAccount. Windows vet/test/build, сертификатная проверка, native fake CLI 7/7, Linux fake CLI 11/11, cleanup 21 и report gate 4 — PASS. Предварительная admission envtest suite прошла на настоящем API; после расширения проверок запущены полный Linux race/envtest и Calico kind suite. Новый 19-й check включает реальные отказы API при разрешающем RBAC, сохранение чужих UID/resourceVersion и отказ при остановке webhook. **Полный 19/19 и CI пока не подтверждены**. [Контракт, TLS и остаточные ограничения](ADMISSION.md).

## NetworkPolicy — подтверждённая проверка (2026-10-29)

Итоговый [CI run 40915413741](https://github.com/T1mof/previewForge/actions/runs/40915413741), source `cfb3946a588bf22aa514d2e4b6ae059606f5e965`: оба job **SUCCESS**, настоящий Calico suite **18/18**, cleanup/report gate **PASS**. Скачанный artifact `12916698603`, SHA256 `0d1ab4a99b52fd6518d865a95f8f8281d0abefb50d3d6e135d7a24b60479a9f5` совпал с API. Независимо проверены 21 allowed/17 denied probe JSON, десять network подпроверок, peers/ports/owner UID/new UID/revoke и Namespace/cluster cleanup. CI race controller 6,135 с, probe 1,029 с; envtest 10,58 с / пакет 11,674 с. [Отчёт и границы доказательств](verification/2026-10-29-network-isolation.md).

Windows generation/controller-gen 0.22.0, gofmt/vet/test/build — **PASS**, controller 92,7%. Linux vet/build/race — **PASS**; controller 4,374 с, probe 1,131 с. Envtest под manifest ServiceAccount RBAC — **PASS**, 18,31 с / пакет 19,542 с: реальные NetworkPolicy UID/watch/recreation, drift и create/patch allow, delete deny. Это проверка API, не dataplane.

Предварительный новый `previewforge-test-calico-20261029-a` подтвердил запуск Calico 3.33.0, CoreDNS, оператора и nginx Ready с двумя репликами. Кластер удалён, `cleanup.txt` PASS. PowerShell-обёртка с native stderr redirection вернула 1; полного e2e/enforcement в этом запуске не было. Дальнейший runner пишет UTF-8 log внутри Bash и сохраняет настоящий exit code suite.

Первый CI [run 40904321044](https://github.com/T1mof/previewForge/actions/runs/40904321044), source `b341df1`: Linux job **SUCCESS**, kind job **FAIL**, cleanup **PASS**. На настоящем Calico прошли lifecycle, HTTP, Ingress, PR, metrics и resource controls; сетевой check не начался, потому что root `.dockerignore` исключал `test/e2e/probe` из build context. Исключение удалено. Это не успешный enforcement suite; gate 18 остаётся обязательным.

Локальный `previewforge-test-network-20261029-b` прервался до появления kubeconfig, доступный log не содержит результатов. При разборе Docker Engine был недоступен; установленный Docker Desktop запущен в фоне без изменения настроек. Файлы `.docker-endpoint`/`.docker-host` заполнены NUL, поэтому автоматический cleanup отказался подтверждать совпадение endpoint. После явного разрешения пользователя выполнена отдельная ручная exact-name очистка с проверкой текущего локального Docker endpoint и конкретного container ID. Кластер и контейнер отсутствуют, имена других кластеров не изменились; kind-кластеров не осталось. Claim снят после проверки, повреждённые endpoint-файлы сохранены. Доказательство — `bin/runs/previewforge-test-network-20261029-b/artifacts/manual-cleanup.json` и `.txt`. **Ручная очистка PASS; локальный полный 18/18 не подтверждён**, автоматический guard не выдаётся за пройденный. Контракт — в [NETWORK-ISOLATION.md](NETWORK-ISOLATION.md). Внешний PR workflow выключен.

## Resource controls и Pod Security — текущая проверка (2026-10-29)

Итог 2026-10-29: [CI run 40890879045](https://github.com/T1mof/previewForge/actions/runs/40890879045), source `b5343c9845240d29ed828abcbe27f92c2051fdfb`: оба job **SUCCESS**, **17/17**, cleanup/report gate **PASS**. Artifact `12906427751`, SHA256 `2a436c2cdcb62e6e2799bd9eb9eb7e37d348b4a85348adb51ee26241d9d2eb88` скачан и независимо проверен, включая восемь guardrails подпроверок и все девять запросов Grafana. Linux race controller 3,601 с / policy 1,018 с / prenvironment 1,990 с; envtest 8,37 с / пакет 9,425 с. Тот же source прошёл локально **17/17**. [Подробный отчёт](verification/2026-10-29-resource-controls.md).

Windows `scripts/check.ps1 -Generate` — **PASS**: controller-gen 0.22.0, gofmt/vet/test/build/Kustomize; покрытие controller 91,4%, policy 100%, prenvironment 80,1%. Linux `scripts/linux-checks.sh` — **PASS**: vet/build/race, envtest 17,66 с / пакет 18,903 с под ServiceAccount RBAC. API server подтвердил вложенные resource defaults, CEL quantity/request≤limit/caps, запрет downgrade restricted, новые права и watch-восстановление/drift repair ResourceQuota/LimitRange. Envtest не доказывает реальную работу quota admission, Pod или GC.

Windows fake isolation 7/7, Bash fake isolation 9/9, cleanup/demo 21/21, synthetic gate 4/4, metadata unit 8/8, ShellCheck/Actionlint/Bash syntax — **PASS**. Первый отдельный вызов fake suite в WSL не нашёл Go в PATH; повтор с portable Go проекта прошёл. Это проверки CLI-изоляции, не Kubernetes e2e.

GitHub-hosted kind [run 40757615791](https://github.com/T1mof/previewForge/actions/runs/40757615791), source `714f02fc08669792b440c45395dc66e528086cbf`: оба job **SUCCESS**, **17/17**, cleanup/report gate **PASS**. Скачанный artifact `12848136039` проверен независимо: SHA256 совпал с API, все check ID/source SHA, restricted app Ready, LimitRange defaults/maximum, PSA и CPU quota admission, восстановление owner UID, Namespace и cluster cleanup. CI envtest 9,08 с / пакет 10,158 с. [Полный отчёт](verification/2026-10-29-resource-controls.md). Внешний PR workflow остаётся выключенным.

Первый локальный `previewforge-test-guardrails-20261028-a`: **FAIL**, cleanup **PASS**. После паузы машины прогон продолжился 2026-10-29 и прошёл lifecycle/Ingress/PR, запуск Prometheus/Grafana и inventory, но native `kubectl.exe` получил преобразованный Git Bash путь `C:/Program Files/Git/usr/bin/promtool` вместо пути внутри контейнера. Исправление `MSYS2_ARG_CONV_EXCL` исключает только `/bin/promtool` и `/etc/prometheus/`, сохраняя преобразование host kubeconfig. Прямая проверка аргументов native process в Git Bash — **PASS**. Полный повтор использует новый `previewforge-test-guardrails-20261029-b`; прежний кластер не переиспользуется.

Локальный b: **FAIL**, cleanup **PASS**. Promtool config и rule tests прошли; первый запрос панели Grafana получил HTTP 400 из-за завершающего CR в файле, созданном Windows Python. Исправлено удаление CR перед формированием URL; probe на всех девяти строках реального файла — **PASS**.

Итоговый локальный c `previewforge-test-guardrails-20261029-c`, source `b5343c9845240d29ed828abcbe27f92c2051fdfb`: **17/17 PASS**, E2E exit 0, cleanup exit 0, полный report gate PASS. Подтверждены restricted app, LimitRange defaults/maximum, PSA и CPU quota, новые UID восстановленных guardrails, cleanup Namespace, а также весь monitoring, HTTP/Ingress/PR/TTL suite. Кластер удалён; artifacts — `bin/runs/previewforge-test-guardrails-20261029-c/artifacts/`, лог — `bin/ci/previewforge-test-guardrails-20261029-c.log`.

## Метрики и monitoring demo (2026-10-28)

Итоговый CI [run 40745877197](https://github.com/T1mof/previewForge/actions/runs/40745877197), commit `743ec4ec3dac07ba175863023dfe0cd3a7c688a2`: оба job **SUCCESS**, настоящий kind **16/16**, cleanup/report gate PASS. Скачанный artifact независимо проверен по SHA256, source SHA, всем check ID, HTTPS 401/403, inventory 2→3→3→2, Prometheus query/target, Grafana provisioning и девяти запросам панелей. Linux race controller 2,874 с / monitoring 1,798 с; envtest 6,99 с / пакет 8,037 с. [Отчёт с artifact ID и digest](verification/2026-10-28-metrics.md). Локальные cold-pull отказы ниже не считаются успешным monitoring e2e.

Первый CI [run 40742463045](https://github.com/T1mof/previewForge/actions/runs/40742463045), `9ae0222`: Linux job **SUCCESS**, kind job **FAIL**, cleanup **PASS**. Prometheus/Grafana Pod запустились; HTTPS 401/403, разрешённый scrape и inventory create/restart/delete прошли. `promtool check config` завершился успешно, но rule unit tests потребовали writable `/tmp` при read-only root filesystem. Добавлен отдельный ограниченный emptyDir 64Mi; полный monitoring check этим первым CI не подтверждён.

Windows `scripts/check.ps1 -Generate`: **PASS**, controller-gen 0.22.0, gofmt/vet/test, сборка manager/CLI и Kustomize. Monitoring collector — 95,7% coverage, controller — 92,0%. Linux vet/build/race/envtest: **PASS**; race example 1,126 с, controller 3,244 с, monitoring 1,845 с, prenvironment 1,989 с; envtest 19,34 с / пакет 20,574 с. Envtest проверил обновлённый operator ServiceAccount RBAC, включая разрешения TokenReview/SubjectAccessReview; настоящую HTTPS auth проверяет отдельный kind-сценарий.

Metadata tests 8/8, Bash isolation 9/9, fake cleanup/demo 21/21, synthetic report gate 4/4 и ShellCheck/Actionlint — **PASS**. Gate расширен до 16 check ID; синтетические отчёты не считаются Kubernetes e2e. Kustomize monitoring demo — **PASS**.

После изменения initial Ready timeout повторены Windows isolation 7/7, Bash 9/9, cleanup/demo 21/21, gate 4/4 и lint — **PASS**. Третья локальная попытка c: **FAIL** до metrics check; nginx оставался в ContainerCreating/Pulling всё пятиминутное окно. **Cleanup PASS**. Локальный monitoring e2e на этом этапе не подтверждён; системные proxy/Docker/WSL настройки не менялись.

Первый kind `previewforge-test-metrics-20261028-a`: **FAIL** до metrics check, первоначальный CR не достиг Ready. В events — nginx ImagePullBackOff после TLS handshake timeout при запросе anonymous Docker Hub token; также были рестарты control plane во время cold build. Operator запустился и создал дочерние ресурсы. **Cleanup PASS**, отсутствие кластера подтверждено. Повтор b также **FAIL** до metrics check: nginx скачался за 2 минуты 53 секунды, контейнеры запустились за несколько секунд до исходного трёхминутного timeout. **Cleanup PASS**. Окно первого cold Ready в Bash/PowerShell увеличено до 300 секунд; критерий Ready сохранён. Эти запуски monitoring не подтверждают; итоговый прогон фиксируется отдельно.


## Подготовка PR-интеграции (2026-10-28)

После deadline demo c отдельно подтверждены NotFound CR `pr-42` и HTTP 404. Пустой `previewforge-test-demo-20261028-c` затем удалён через claim/token cleanup, отсутствие kind-кластера подтверждено. Это сохранённый результат предыдущей демонстрации; активной localhost-ссылки после TTL нет.

Итоговый CI [run 40640242526](https://github.com/T1mof/previewForge/actions/runs/40640242526), commit `ad728297b367d6316995d8ad6642fbe1c5cc6baa`: оба job **SUCCESS**, e2e **15/15**, cleanup/report gate PASS. Скачанный artifact проверен отдельно по SHA256, commit, всем check ID, обеим HTTP revision/health и удалению кластера. Linux race example 1,011 с, controller 3,059 с, prenvironment 1,810 с; envtest 8,80 с / пакет 9,851 с. [Отчёт с artifact ID и digest](verification/2026-10-28-pr-integration.md). Внешний PR lifecycle остаётся NOT RUN.

Свежее локальное demo `previewforge-test-demo-20261028-c`: **PASS**, настоящий HTTP с Windows-хоста через Traefik на `http://localhost:8080`; кластер оставлен для просмотра. Приложение создано 05:57:25 UTC, TTL deadline 06:57:25 UTC / 09:57:25 МСК 2026-10-28. Kubeconfig — `bin/runs/previewforge-test-demo-20261028-c/kubeconfig`, context — `kind-previewforge-test-demo-20261028-c`. Старое demo b после истечения TTL удалено штатным cleanup с сохранённым claim/token; пользовательский kubeconfig не менялся.

Итоговый локальный `previewforge-test-pr-20261028-d`: **15/15 PASS**, e2e exit 0, cleanup exit 0, report gate PASS; кластер удалён. PR check выполнил две реальные Docker-сборки, запуск immutable OCI digests, CLI под ограниченным SA, create/update с сохранением UID/creationTimestamp/Namespace, HTTP revision `000…001`/`000…002` и `/healthz`, отказ неверного source UID/чужого CR и повторное delete с ожиданием полного удаления Namespace. GitHub metadata — fixture, внешнего PR workflow этот результат не подтверждает. Артефакты: `.cache/worktrees/pr-integration/bin/runs/previewforge-test-pr-20261028-d/artifacts/`; лог рядом в `bin/ci/`. Запуск начат до commit исправления retry, поэтому GitHub commit SHA ему не присваивается; точный опубликованный commit проверяется CI отдельно.

Windows: controller-gen 0.22.0 (отдельная deployer роль), gofmt, vet, `go test ./...`, сборка CLI и Kustomize operator/deployer — **PASS**. Linux: vet/build — **PASS**, race example 1,061 с, controller 3,426 с, prenvironment 1,950 с; envtest 16,90 с / пакет 18,135 с — **PASS**. Envtest использовал настоящий RBAC deployer SA, проверил разрешённые/запрещённые операции, owner Namespace UID без finalizer permission, optimistic update и настоящий HTTP 409 при замене CR перед Delete; повтор не удалил замену.

Metadata helper: 8 unit-сценариев — **PASS** (актуальный head, устаревший build/close, fork, доверие, неверные repo/SHA/domain). Bash isolation 9/9, cleanup/demo 21/21, synthetic report gate 4/4 — **PASS**; это fake CLI, не Kubernetes. Итоговые Actionlint/ShellCheck — **PASS**. Внешний PR workflow **NOT RUN**, выключен по умолчанию согласно решению о выборе площадки позже.

Первый настоящий локальный PR e2e `previewforge-test-pr-20261028-a`: **FAIL** при разборе image digest после успешного Ingress. Containerd присвоил repository `import-*`; проверка ошибочно ожидала исходное Docker имя. **Cleanup PASS**. Повтор b: **FAIL** по timeout rollout, служебный repoDigest не разрешается kubelet локально; **cleanup PASS**. Итоговый скрипт получает OCI target digest точного загруженного tag из containerd и регистрирует явную `repository@sha256` ссылку; config ID не используется как manifest digest.

Прогон `previewforge-test-pr-20261028-c`: **FAIL** по timeout первоначального rollout оператора, до PR-сценария. Pod запустился лишь к завершению двухминутного ожидания; контроллер затем начал работу, но это не успешный suite. **Cleanup PASS**. После удаления истёкшего demo b запускается отдельная новая попытка; системные Docker/WSL настройки не менялись.

Первый CI PR-подготовки [run 40592109298](https://github.com/T1mof/previewForge/actions/runs/40592109298), `f5ed8c3`: Linux job **SUCCESS**, kind job **FAIL**, cleanup **PASS**. Реальные create и update завершились Ready с generation 1/2 и той же UID/Namespace; Pod получил правильную вторую revision, но следующий отдельный `/healthz` запрос завершился timeout. Retry охватывал только `/`, поэтому тест завершился на первом сбое health-запроса. Исправлено совместное ожидание обоих HTTP-ответов и добавлен отдельный failure log. Это не успешный e2e; итоговый прогон фиксируется отдельно.

## CI Ingress (2026-10-28)

[Run 40582139866](https://github.com/T1mof/previewForge/actions/runs/40582139866), commit `8082fab040df8ef79b62f19c82f877caf7e1027f`: оба job **SUCCESS**, e2e **14/14**, cleanup/report gate PASS. Скачанный artifact независимо проверен по digest, commit, всем check ID, HTTP и удалению кластера. Linux race 3,852 с; envtest 8,93 с / пакет 9,993 с. [Отчёт с artifact ID и SHA256](verification/2026-10-28-ingress.md).

## Ingress и локальная демонстрация (2026-10-27)

Windows `scripts/check.ps1 -Generate` — **PASS**, controller-gen 0.22.0, gofmt/vet/test/build/Kustomize, покрытие контроллера 92,0%. Linux `scripts/linux-checks.sh` — **PASS**: race 3,054 с, envtest 12,66 с / пакет 13,917 с под ServiceAccount RBAC. Envtest проверил CRD host/class, запрет IP/wildcard, создание Ingress, owner UID, настоящее watch-восстановление и отключение/очистку URL.

Локальный новый `previewforge-test-ingress-20261027-a`: **14/14 PASS**, E2E exit 0, cleanup exit 0, report gate PASS; кластер удалён. Новый check `ingress` выполнил настоящий HTTP через Traefik 3.7.16, восстановление маршрута, drift repair, смену host/URL и 404 после отключения. Ответы — `bin/runs/previewforge-test-ingress-20261027-a/artifacts/ingress-http.txt`; полный лог — `bin/ci/previewforge-test-ingress-20261027-a.log`. Локальный прогон выполнен до commit ветки; GitHub SHA для него не заявляется.

Demo `previewforge-test-demo-20261027-a` с портом 80: **FAIL** на Docker bind — Windows запретила использование порта; **cleanup PASS**. Default demo-порт изменён на 8080 без изменения системных настроек. Новый `previewforge-test-demo-20261027-b`: **PASS**, настоящий запрос с хоста через Ingress получил nginx на http://localhost:8080. Кластер намеренно оставлен для демонстрации, приложение подчиняется TTL; отчёт — `bin/runs/previewforge-test-demo-20261027-b/artifacts/demo.txt`, HTTP — `demo-http.txt`.

Регрессии скриптов: Windows 7/7 и Bash 9/9 isolation, 21/21 fake-CLI cleanup/demo, 4/4 synthetic report gate — **PASS**; ShellCheck/Actionlint — **PASS**. Новые проверки подтверждают loopback config, отказ внедрения через port, cleanup demo при HTTP ошибке, сохранение demo при успехе и отказ существующего run/claim до cleanup traps. Это отдельные fake-CLI проверки; настоящий HTTP подтверждён указанными kind-прогонами. TLS handshake не проверялся.

## GitHub CI этапа 1 (2026-10-27)

[Run 40576650171](https://github.com/T1mof/previewForge/actions/runs/40576650171), commit `7d0a1561d5f6b49c2c360907fa14e7be0e9390e7`: оба job **SUCCESS**. Linux race 3,285 с, envtest 7,44 с / пакет 8,501 с под ServiceAccount RBAC; настоящий kind e2e **13/13 PASS**, cleanup и report gate PASS. Скачанный artifact отдельно проверен по digest, commit, двум HTTP-ответам и всем check ID. Подробности — в [отчёте CI](verification/2026-10-27-stage1.md).

Перед публикацией с новым module path Windows Generate/gofmt/vet/test/build/Kustomize и Linux vet/build/race/envtest также прошли; локальный race 2,882 с, envtest 15,80 с / пакет 17,037 с.

## Настоящий локальный kind e2e (2026-10-27)

Docker Desktop 4.89.0 / Engine 29.7.2 доступен из Windows. WSL Integration для Ubuntu выключена, поэтому штатный `scripts/e2e.sh` запущен через Git Bash с Windows kind 0.33.0 и kubectl 1.36.1. Kubernetes в kind — 1.37.0. Настройки WSL/Docker Desktop и пользовательский kubeconfig не менялись.

Первый кластер `previewforge-test-local-20261027-a`: **13/13 PASS**, E2E exit code 0, cleanup exit code 0. `scripts/verify-e2e-result.sh` подтвердил полноту отчёта, отсутствие claim и доказательство удаления кластера.

**Итоговый прогон после исправления EXPIRES** — `previewforge-test-local-20261027-c`: **13/13 PASS**, E2E exit code 0, cleanup exit code 0. Проверка полноты отчёта прошла в том же запуске; кластер отсутствует, claim снят. Все результаты таблицы ниже повторно подтверждены итоговым кодом и сгенерированной CRD.

| Реальная проверка | Результат |
| --- | --- |
| Создание CR, Deployment и настоящих готовых Pod; controller owner UIDs | PASS |
| Восстановление удалённого Deployment с новой UID; исправление replicas | PASS |
| Новый Pod оператора после рестарта; изменение spec во время простоя | PASS; актуальная generation Ready |
| HTTP через Service DNS и ClusterIP из smoke Pod; ready EndpointSlice | PASS; ответ nginx сохранён в `http.txt` |
| Закрытый readiness-порт блокирует Ready; исправление порта завершает rollout | PASS |
| Cleanup finalizer удерживает CR до исчезновения Namespace | PASS; проверено с блокирующим тестовым finalizer |
| TTL при работающем операторе и после простоя; ручное удаление | PASS; CR и Namespace полностью исчезли |
| Чужой Namespace с поддельными labels и ConfigMap-canary | PASS; UID/resourceVersion сохранены, чужой Namespace не принят под управление и не удалён |
| Удаление тестового кластера и сохранение списка других kind-кластеров | PASS; `cleanup.txt`, claim удалён |

Первый прогон выявил дефект колонки `EXPIRES`: Kubernetes отображал будущую дату как `<invalid>`, поскольку `type=date` предназначен для относительного возраста. Маркер изменён на `type=string`, CRD перегенерирована controller-gen 0.22.0; колонка предназначена для абсолютного RFC3339 deadline. Поле `status.expiresAt` и расчёт TTL не изменены.

В новом кластере `previewforge-test-local-20261027-c` исправление проверено настоящим `kubectl get`: `EXPIRES` показывает `2026-10-27T20:52:14Z`. Вывод сохранён в `artifacts/expires.txt`. Там же сохранён `sources.sha256` с хешами Go-кода, manifests, скриптов и файлов сборки; соответствие текущим исходникам проверено. Git commit SHA этим не подменяется.

`scripts/check.ps1 -Generate` после исправления: **PASS** (генерация, gofmt, vet, `go test ./...`, сборка Windows manager, Kustomize). Покрытие контроллера — 91,4%. Повторные Linux vet/build/race/envtest после исправления: **PASS**, unit race 2,852 с, envtest 14,56 с / пакет 15,803 с; manager работал под ServiceAccount RBAC.

Повтор `previewforge-test-local-20261027-b` после изменения CRD: **FAIL** до установки оператора, `kubectl apply` не смог получить OpenAPI. В диагностике API server есть таймауты обращений к etcd и failed readiness probes; это не успешный Kubernetes suite. **Cleanup PASS**: кластер удалён, отсутствие проверено, claim снят. Отчёт ошибки сохранён в `bin/runs/previewforge-test-local-20261027-b/artifacts/`, лог — `bin/ci/previewforge-test-local-20261027-b.log`. Linux suite и успешная итоговая попытка c выполнены последовательно.

Артефакты первого прогона: `bin/runs/previewforge-test-local-20261027-a/artifacts/`; полный лог: `bin/ci/e2e-local-20261027-a.log`. Они локальные и исключены из Git. Эти локальные прогоны выполнены до инициализации Git и выбора remote, поэтому не имеют commit SHA. После выбора T1mof/previewForge module path изменён; опубликованный commit требует отдельного CI run. Настоящий cleanup подтверждён при успехе и ошибке установки; сигналы и ошибки самого удаления проверялись ранее с fake CLI, запуск CI `always()` пока не подтверждён.

Итоговые артефакты: `bin/runs/previewforge-test-local-20261027-c/artifacts/` (`checks.txt`, `result.txt`, `cleanup.txt`, `http.txt`, `expires.txt`, `sources.sha256`, диагностика kind). Полный лог: `bin/ci/previewforge-test-local-20261027-c.log`. Image оператора: `previewforge:previewforge-test-local-20261027-c`, digest `sha256:352909147bc7f10ce53615c5fe0262db85d0c28703449f213bcc36b61b4cb87d`.

## История проверок кода (2026-10-11)

Следующие результаты и ограничения зафиксированы до первого настоящего e2e; актуальный статус приведён выше.

| Проверка | Фактический результат |
| --- | --- |
| `go test ./... -coverprofile=coverage.out` | PASS; покрытие `internal/controller` 91,4% |
| `go vet ./...` | PASS |
| `go build -trimpath -o bin/manager.exe ./cmd/manager` | PASS |
| controller-gen 0.22.0: object, crd, rbac | PASS; генерируются DeepCopy, CRD и ClusterRole |
| `kubectl kustomize config/default` | PASS; итоговый manifest в `bin/install.yaml` |
| Linux/amd64 cross-build manager и `go test -c -tags=integration` | PASS; бинарники скомпилированы, не исполнялись |
| `bash -n scripts/kind-demo.sh scripts/e2e.sh` | PASS; проверен синтаксис |
| `scripts/check.ps1` | PASS; полный Windows workflow выполнен |
| Linux `go vet ./...` и build через `scripts/linux-checks.sh` | PASS; manager в `bin/linux/manager` |
| WSL2 `CGO_ENABLED=1 go test -race ./... -count=1` | PASS; контроллер 2,871 с, data races не обнаружены |
| WSL2 `go test -race -tags=integration ... -run TestEnvtest -count=1` | PASS; suite 8,33 с, итог пакета 9,551 с |
| `test-kind-scripts.py` в Windows и WSL2 | PASS; 7 случаев для PowerShell и 7 для Bash с подставными CLI |
| Kustomize overlay отдельного запуска | PASS; image tag заменён на previewforge:previewforge-test-check |
| `kind-demo.ps1 -ClusterName previewforge-test-audit` | BLOCKED; Docker Engine недоступен, отказ на preflight до создания кластера |

## Повторная проверка и CI e2e (2026-10-11)

| Проверка текущих изменений | Реальный результат |
| --- | --- |
| `scripts/linux-checks.sh` в Ubuntu WSL2 | PASS; vet/build, unit race 2,907 с, envtest suite 8,31 с / пакет 9,546 с под race detector |
| `test-kind-scripts.py` | PASS; 7 сценариев PowerShell и 7 Bash повторно, подставные CLI |
| `test-e2e-cleanup.py` | PASS; 17 сценариев с fake CLI: partial create/build/apply/setup/assertion/diagnostics failures, SIGTERM, успешный EXIT, отказ cleanup без владения и при смене endpoint, ошибка удаления и повторный cleanup |
| Проверки полноты отчёта в том же harness | PASS; 4 синтетических случая: отказ для неполного отчёта, дубликатов и отсутствующего cleanup; принятие полного набора. Это не Kubernetes результат |
| Actionlint 1.7.12 + ShellCheck 0.9.0 | PASS; итоговый workflow и Bash isolation/cleanup/e2e/report scripts |
| `bash -n` итоговых Bash-скриптов | PASS |
| `make LOCALBIN=.../bin/linux test-e2e` в Ubuntu | BLOCKED; exit 1 на Docker context preflight: Docker command недоступна без WSL Integration. Claim/кластер не создавались; Kubernetes assertions не выполнялись |
| GitHub `Operator verification` | NOT RUN; workflow подготовлен локально, папка не является Git-репозиторием, GitHub remote/целевой URL отсутствует |

Повторный Linux envtest подтвердил тот же набор настоящих API/RBAC проверок: разрешения ServiceAccount, конфликты Status/TTL Delete, UID precondition, foreign Namespace refusal и lifecycle с моделированным Deployment status. Эти результаты не подтверждают работу kubelet/HTTP/namespace controller.

Подготовлен [.github/workflows/operator-e2e.yml](../.github/workflows/operator-e2e.yml): только GitHub-hosted Ubuntu, pinned Go/Actions/kind/kubectl/node image, read-only GitHub permissions, отдельные cluster/context/kubeconfig/image для каждого запуска. Сначала Linux race/envtest и регрессии скриптов, затем настоящие Kubernetes проверки. Pipeline требует все 13 check IDs и подтверждённый cleanup. Инструкции запуска — в [VERIFICATION.md](VERIFICATION.md).

Ранний EXIT/INT/TERM trap теперь покрывает demo, image build, создание/rollout и assertions. Диагностика сохраняется до удаления; `cleanup-kind.sh` проверяет ownership token и тот же локальный Docker endpoint, удаляет только конкретное имя и проверяет отсутствие кластера. После успеха claim снимается; повторный `always()` cleanup ничего не удаляет. Ошибка cleanup делает suite/job неуспешным. Жёсткое завершение процесса/runner не гарантирует исполнение trap; это отдельно описано в SECURITY.md.

| Требование настоящего e2e | Подготовленная проверка | Подтверждение на 2026-10-11 |
| --- | --- | --- |
| Создание CR и настоящих Pod | Deployment rollout, Pod Ready, CR Ready/replicas | NOT RUN |
| Ready после готовности приложения | Running/Ready=False на закрытом порту, затем реальный rollout после исправления | NOT RUN |
| Service HTTP | Smoke Pod, DNS и ClusterIP, ready EndpointSlice, ответ nginx | NOT RUN |
| Восстановление Deployment | Новая UID после Delete, настоящий rollout | NOT RUN |
| Перезапуск контроллера | Новая UID Pod оператора, изменение spec во время простоя, актуальная generation Ready | NOT RUN |
| TTL и полный cleanup Namespace | TTL при работающем операторе и после downtime; ожидание исчезновения CR/Namespace | NOT RUN |
| Чужие ресурсы/кластеры | Foreign Namespace с forged labels, ConfigMap UID/RV, snapshot имён других кластеров, явный kubeconfig/context | NOT RUN; guards подтверждены только mock/envtest |
| Cleanup при ошибках | Claim/token, EXIT/INT/TERM, CI always(), отсутствие кластера и cleanup artifact | NOT RUN с настоящим Docker; fault-injection проверки отдельно |

На 2026-10-11 URL GitHub run, commit SHA и Kubernetes artifact отсутствовали. Локальные Kubernetes artifacts появились 2026-10-27; GitHub run по-прежнему не выполнен, этап 2 не начат.

Локальный лог mock/fault-injection прогонов сохранён в `bin/ci/cleanup-checks-local.log`; эта папка исключена из Git. В нём нет Kubernetes run.

Тесты проверяют создание ресурсов, Ready, идемпотентность без записи, обновление spec с сохранением Service IP, drift repair, recreation/restart, TTL и ожидание финализации, недоверенный status.namespace, ownership collisions, ошибки API и конфликт finalizer patch, независимую обработку восьми CR. Отдельно проверяются отказ от status patch при изменении UID/generation/deletionTimestamp и ожидание Terminating Namespace/Service.

Fake client не имеет namespace controller или GC. В TTL unit-тесте завершение удаления Namespace моделируется вручную; это не является доказательством настоящей очистки кластера.

## Что исправлено и дополнительно проверено

- Регрессионный тест сначала подтвердил потерю свежей Condition при копировании Status из кеша. Теперь Conditions берутся из uncached APIReader; сохраняются чужие Conditions и lastTransitionTime при неизменном Ready. Unit-тест прошёл после исправления.
- Actual optimistic-lock conflict на Status patch проверяется и fake client, и настоящим API: новый status не перезаписывается, retry проходит.
- Конкурентное продление TTL между status patch и Delete вызывает HTTP 409 и сохраняет CR. Отдельно проверен wrong UID на Namespace Delete — HTTP 409, Namespace остаётся без deletionTimestamp.
- Замена Namespace между read/delete проверена unit-тестом по resourceVersion. Fake client не реализует UID precondition; проверку UID поэтому подтверждает envtest.
- Cleanup сохраняет собственный и внешний finalizer при ошибке API; после успешной очистки снимается только собственный. Проверяется отказ удаления Namespace с чужой controller reference/без неё, даже с поддельными labels и status.namespace.
- Envtest загружает реальные RBAC manifests и запускает manager под ServiceAccount identity. Проверены разрешённые операции и запрет Secrets read, Pod delete, ClusterRole create, Lease/Event в других Namespace. Role в previewforge-system дополнена create/patch Event для стандартного leader election recorder.
- Envtest подтвердил CRD validation/defaulting, API defaults idempotence, simulated Ready, child delete watch/recreation, manager restart с leader election и TTL с удержанием finalizer.
- Первый envtest прогон выявил глобальную проверку повторного имени контроллера при создании второго manager в одном процессе. Только тестовому manager разрешена повторная регистрация после полного завершения первого; production validation не менялась. Повторный прогон прошёл под race detector.
- Demo-скрипты отказываются от существующего кластера/каталога запуска, недопустимого имени и удалённого Docker context/DOCKER_HOST. Подставные CLI подтвердили явные kubeconfig/context, отдельный image tag/overlay и сохранность пользовательского kubeconfig. Это не запуск workloads.

## Ограничения предыдущей среды (2026-10-11)

Состояние на дату прежней проверки; Docker в Windows стал доступен 2026-10-27, что позволило выполнить настоящий e2e через Git Bash.

- Docker CLI 29.7.2 установлен, но `docker version` не подключается к `dockerDesktopLinuxEngine`: named pipe отсутствует, Docker Engine остановлен.
- kubectl установлен; `kubectl config current-context` сообщает `current-context is not set`.
- На Windows kind, Kubebuilder CLI, make и gcc отсутствуют. controller-gen установлен в `bin/` проекта. В Ubuntu WSL2 установлены gcc/make/libc6-dev; Go 1.26.9 и setup-envtest находятся в проекте.
- Native Windows ограничения race/envtest обойдены через WSL2. Обе проверки реально выполнены в Linux. Envtest assets Kubernetes 1.37.0 (API server, etcd, kubectl) доступны в `bin/linux/envtest/k8s/1.37.0-linux-amd64`.
- Docker image, kind demo/e2e, настоящие Pods, HTTP и Kubernetes GC не проверены в этой среде.

## Как проверить в Linux

Нужны Go 1.26+ (желательно актуальный patch), make, gcc для race, kubectl, Bash, работающий Docker Engine и доступ к Go modules/GitHub/container registries.

```bash
make manifests
make vet test build
make test-race
make test-integration
make test-e2e
```

`scripts/linux-checks.sh` выполняет vet/build/race/envtest через Linux Go. `make test-integration` устанавливает setup-envtest 0.25.2, скачивает Kubernetes 1.37.0 assets и выполняет TestEnvtest под race detector. В envtest отсутствуют kubelet, Deployment controller, namespace controller и GC: Ready моделируется, фактическое исчезновение Namespace не подтверждается этим уровнем.

`make test-e2e` устанавливает kind 0.33.0 и создаёт только новый локальный `previewforge-test-*`. Проверяет настоящий HTTP/lifecycle, owners, held finalizer, активный TTL и TTL после downtime, manual cleanup, чужой Namespace с поддельными labels и canary. Сохраняет диагностику и удаляет только кластер с claim/token этого запуска, включая ошибки; чужие тестовые fixtures последнего сценария удаляются с тестовым кластером. Demo для ручной проверки сохраняет кластер.

В Windows первый сквозной запуск после старта Docker Desktop: `scripts/kind-demo.ps1`. Он не требует make/Bash и не меняет пользовательский kubeconfig. Полная инструкция, включая WSL Docker integration и PATH для Linux kubectl, — [VERIFICATION.md](VERIFICATION.md).

## Оставшиеся риски и неподтверждённые сценарии

Подробный анализ широких RBAC-прав (включая косвенный доступ через workload creation), сетевой изоляции/CNI и удаления всего содержимого управляемого Namespace — в [SECURITY.md](SECURITY.md). NetworkPolicy добавлены после этапа 1 и подтверждены CI 18/18 на Calico; [актуальный отчёт](verification/2026-10-29-network-isolation.md). Дополнительные admission-ограничения cluster-wide прав пока не реализованы.

- Локальный Bash e2e подтвердил Ready, HTTP/DNS/Service routing, восстановление Deployment, рестарт in-cluster manager и окончание Namespace GC. Запуск того же workflow на GitHub-hosted runner ещё не выполнен.
- PowerShell demo не выполнялся полностью в Docker/kind; настоящий lifecycle подтверждён Bash e2e через Git Bash.
- ClusterRole имеет cluster-wide доступ к Namespace/Deployment/Service; ownership guards не защищают от компрометации оператора. Envtest подтверждает текущие разрешения и guard logic, но не является нагрузочным или security audit.
- Namespace является единицей владения/очистки: ресурсы, вручную помещённые в принадлежащий preview Namespace, удаляются вместе с ним. Чужие Namespace защищены проверкой controller owner reference, UID и resourceVersion.
- Namespace изоляция не обеспечивает сетевую изоляцию; NetworkPolicy и дополнительные admission restrictions остаются вне этапа 1.
