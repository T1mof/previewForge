# Нагрузка и восстановление — 2026-10-30

Проверяемый source: `f91ace7210ccf6f6125a01c096becfeab678915f`. Методика и ограничения — в
[LOAD-TESTING.md](../LOAD-TESTING.md). Финальный полный
[CI run 41071421817](https://github.com/T1mof/previewForge/actions/runs/41071421817)
завершён: **22/22, cleanup PASS**, artifact и измерения проверены независимо.

## Финальный CI и опубликованные исходные данные

[Run 41071421817](https://github.com/T1mof/previewForge/actions/runs/41071421817),
source `f91ace7210ccf6f6125a01c096becfeab678915f`: оба job **SUCCESS**, полный Helm/Calico suite **22/22**,
report gate и guarded cleanup **PASS**. Linux job `124118388400`, kind job
`124121235541`. Проверены все прежние 20 check ID и новые `load`, `faults`.

Скачан `kind-e2e-41071421817-1`, artifact `12987182762`, 2312331 bytes.
SHA256 ZIP совпал с GitHub API и локальным Get-FileHash:
`d12c64623f3ff70011d834007122cea4128061387256501807e2e46969865377`.

Независимый checker проверил exact source в result, все 22 ID, HTTP, 21 allowed /
17 denied Calico probes, 10 admission denials / 8 RBAC reviews, Helm install /
upgrade / rollback, package digest и canonical assets, inventory 2/3/3/2 и девять
Grafana panels. Дополнительно проверены все load/faults observations, raw quantiles,
UID/поколения, HTTP, TTL и canary; неположительных задержек нет. Private keys,
kubeconfig и claim отсутствуют в ZIP.

[Исходный stress-report.json](2026-10-30-load-faults.json) опубликован без изменений
из artifact, SHA256 `471d92541b9399598c3187f7c2d2e31ad98fb0c52db545ae5399c9b3a48b9d61`.
Все 20 fixtures имеют исходные UID, записи readiness/Pod UID и подтверждение
NotFound CR/Namespace. JSON содержит временные метки, исходные задержки и ответы
Prometheus; хранение этих наблюдений не заменяет Kubernetes state оператора.

| Фаза | N | p50, с | p95, с | max, с |
|---|---:|---:|---:|---:|
| Создание → Ready | 16 | 11.660 | 12.711 | 12.711 |
| Увеличение 1 → 2 | 16 | 11.382 | 12.437 | 12.437 |
| Исправление drift 3 → 2 | 16 | 4.220 | 5.348 | 5.348 |
| Восстановление admission | 16 | 6.503 | 43.448 | 43.448 |
| Подготовка TTL fixtures | 4 | 8.236 | 8.237 | 8.237 |
| Восстановление оператора | 12 | 29.301 | 31.471 | 31.471 |
| Очистка manual/TTL после простоя | 8 | 31.597 | 32.615 | 32.615 |
| Удаление оставшихся окружений | 12 | 12.865 | 28.003 | 28.003 |

`backlog-cleanup` наблюдается после ожидания Ready выживших окружений; его
задержка — верхняя граница первого наблюдения NotFound, не точный момент GC.
Та же последовательность применяется в локальном прогоне ниже.

Сам runner занял 174.963 с. GitHub-hosted Ubuntu 24.04,
один kind node Linux/amd64, Kubernetes v1.37.0,
containerd://2.3.4; allocatable CPU `4`, memory
`16367524Ki`, Pod `110`. Оператор requests 100m/128Mi,
limits 500m/256Mi, один leader, четыре reconcile worker. Приложение warm/preloaded,
16 окружений и 4 TTL fixtures, четыре автора запросов; фоновый sample `pr-42`
имеет три реплики. Создание/обновление включает API и polling, не cold image pull.

Максимум выборки RSS — 61.40 MiB,
CPU rate за минуту — 0.04818 ядра,
глубина очереди — 5.
Ошибки reconcile при отказе admission: +217
в том же Pod; его UID и restart counters не изменились. После остановки оператора
UID его Pod изменился; 12 обновлений достигли Ready, 4 manual и 4 TTL CR/Namespace
исчезли, foreign canary сохранил UID/resourceVersion. Остальные 12 окружений
удалены отдельной волной. TTL пересёк дедлайны до возврата менеджера.

Интервал выборки метрик 5 с; эти максимумы не доказывают истинные пики.
Nearest-rank p95 при N ≤ 16 совпадает с max. Это один ограниченный профиль,
не оценка максимальной ёмкости или production SLO.

## Локальный прогон исправленных измерений

`previewforge-test-load-20261030-c`, source `f91ace7210ccf6f6125a01c096becfeab678915f`:
**focused load/faults PASS**, exit 0, cleanup 0, отсутствие кластера и удаление
claim подтверждены. Это отдельный прогон с Helm/Calico, Prometheus/Grafana и
настоящими Pod, не весь suite 22/22. Его четыре check ID — `helm`, `load`,
`faults`, `cluster-isolation`.

Независимый checker пересчитал процентили из исходных задержек, проверил
поколения и UID Pod/Deployment/Namespace/CR, HTTP, 4 + 4 запланированные замены
Deployment, 20 NotFound CR/Namespace, TTL дедлайны, отсутствие рестарта менеджера
при отказе admission, сохранение canary и числовые Prometheus samples.

| Фаза | N | p50, с | p95, с | max, с |
|---|---:|---:|---:|---:|
| Создание → Ready | 16 | 10.483 | 11.490 | 11.490 |
| Увеличение 1 → 2 | 16 | 10.975 | 11.015 | 11.015 |
| Исправление drift 3 → 2 | 16 | 3.402 | 4.430 | 4.430 |
| Восстановление admission | 16 | 10.295 | 43.088 | 43.088 |
| Подготовка TTL fixtures | 4 | 9.947 | 9.947 | 9.947 |
| Восстановление оператора | 12 | 26.677 | 27.699 | 27.699 |
| Очистка manual/TTL после простоя | 8 | 29.843 | 29.860 | 29.860 |
| Удаление оставшихся окружений | 12 | 13.977 | 14.981 | 14.981 |

Runner занял 155.175 с; подготовка kind, загрузка image,
установка Helm/monitoring и финальное удаление кластера в это время не входят.
Приложение заранее загружено в kind; профиль restricted, request 10m/16Mi,
limit 100m/64Mi, 16 окружений, 4 автора запросов, 4 отдельные TTL fixtures.
Дополнительно работали инфраструктура и sample `pr-42` с двумя репликами;
в полном CI sample имеет три реплики. Эти прогоны не идентичны по фоновому состоянию.

Локально один node Linux/amd64, Kubernetes 1.37.0, containerd 2.3.4,
kernel 6.6.87.2-microsoft-standard-WSL2; allocatable 16 CPU, 8089792Ki memory,
110 Pod. Оператор: requests 100m/128Mi, limits 500m/256Mi. Это ресурсы node
и контейнера, не выделенная вычислительная ёмкость самого теста.

Максимум выборки RSS — 60.05 MiB;
максимум CPU rate за минуту — 0.04673
ядра; максимум выборки глубины очереди — 5.
При отключённом admission счётчик ошибок reconcile вырос на
227 в том же Pod оператора.
Это ожидаемые ошибки управляемого отказа. Максимумы выборки каждые 5 с не равны
истинным пикам; CPU rate может охватывать соседние фазы и рестарт процесса.

Локальные доказательства: `bin/runs/previewforge-test-load-20261030-c/artifacts/`
(`stress-report.json`, `stress-report.verified.json`, `result.txt`, `cleanup.txt`),
log `bin/ci/previewforge-test-load-20261030-c-focused.log`. Private kubeconfig и
claim не входят в artifacts; пользовательский kubeconfig не использовался.

## Регрессии инструментов

Windows gofmt, vet, `go test ./...`, сборка runner — PASS; последний тестовый
пакет stress — 1,098 с. Linux race последнего runner — 2,858 с; Linux build
и `--help` — PASS. Полная локальная Linux suite до исправлений CLI/замеров также
прошла: race controller 4,490 с; реальные RBAC envtest admission 16,09 с и
lifecycle 12,21 с, пакет 29,552 с; chart 20,594 с.

Финальный CI Linux job `124118388400` на `f91ace7210ccf6f6125a01c096becfeab678915f` — SUCCESS:
race controller 4,330 с, admission 1,678 с, stress 2,489 с; реальные RBAC envtest
8,55 с / 9,74 с, пакет 19,336 с; chart 0,561 с. Actionlint, ShellCheck,
11 Bash isolation cases, 21 fake cleanup/demo scenario, 6 synthetic gate checks
и 8 PR metadata tests — PASS. Fakes проверяют утилиты и не являются Kubernetes
доказательством.

## Предварительные запуски

Локальный `previewforge-test-load-20261030-a` и
[CI 41065219723](https://github.com/T1mof/previewForge/actions/runs/41065219723),
source `2bcc1eb`, остановились после сетевого блока при конфликте глобального
флага `kubeconfig` с регистрацией controller-runtime. Нагрузочного отчёта нет;
cleanup PASS. Исправлено отдельным FlagSet и CLI regression test в `12b4761`.

Локальный `previewforge-test-load-20261030-b` и
[CI 41067731519](https://github.com/T1mof/previewForge/actions/runs/41067731519),
source `12b4761`, прошли функциональные 22/22, gate и cleanup. Независимый checker
отклонил задержки очистки: повторный poll обнулял уже полученное время NotFound.
Эти задержки не используются в опубликованных измерениях. `f91ace7` сохраняет
первое наблюдение; добавлены regression test с двумя наблюдениями и отказ runner
считать успешным отчёт с неположительной задержкой.

Профиль не подтверждает предельную ёмкость, production SLO, холодную загрузку
image, API server outage, partition, SIGKILL, 429 или смену лидера между живыми
кандидатами. Не закрывает version matrix. Внешний PR gate не активирован.
