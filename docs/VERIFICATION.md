# Финальная проверка этапа 1

Этап 1 закрывается после подтверждённого запуска e2e с реальными Pod, HTTP через Service, восстановлением Deployment, рестартом оператора, TTL и завершением удаления Namespace. Envtest проверяет настоящий API server/RBAC, но не запускает приложения и не заменяет этот критерий.

## Windows: проверки кода и скрипта

```powershell
cd E:\PreviewForge
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\check.ps1
$env:GOMODCACHE = Join-Path $PWD '.cache\gomod'
$env:GOCACHE = Join-Path $PWD '.cache\go-build'
python .\scripts\test-kind-scripts.py
```

Тест скриптов использует подставные исполняемые CLI и временные файлы внутри `.cache/script-guards`. Он проверяет выбор кластера, отказ от существующего имени/kubeconfig, удалённого Docker context/DOCKER_HOST и сохранность пользовательского kubeconfig. Это проверка маршрутизации команд, а не Kubernetes e2e.

## WSL2: envtest и race без Docker

Ubuntu WSL2 уже обнаружена и подготовлена в текущем сеансе. В неё установлены gcc, make, libc6-dev и сертификаты. Linux Go 1.26.9 находится только в `.cache/gomod/golang.org/toolchain@v0.0.1-go1.26.9.linux-amd64`; системная Go-установка Windows не менялась.

Из PowerShell:

```powershell
wsl --list --verbose
wsl --distribution Ubuntu --exec bash /mnt/e/PreviewForge/scripts/linux-checks.sh
```

Скрипт выбирает Linux Go, использует отдельный build cache `.cache/linux`, собирает менеджер в `bin/linux`, затем выполняет unit-тесты и envtest под race detector. Kubernetes 1.37.0 assets загружаются setup-envtest 0.25.2 в `bin/linux/envtest`. Ошибка загрузки assets останавливает запуск; успешного skip нет.

Для новой Ubuntu WSL2:

```powershell
# Проверка доступных дистрибутивов; установка новой WSL требуется только при её отсутствии.
wsl --list --verbose
# В окне PowerShell с правами администратора, если WSL отсутствует:
wsl --install -d Ubuntu
```

В Ubuntu установите необходимые пакеты:

```bash
sudo apt-get update
sudo apt-get install -y --no-install-recommends gcc make libc6-dev ca-certificates
```

Можно использовать установленный Linux Go 1.26+; либо скачать закреплённый Linux toolchain в проект из PowerShell:

```powershell
cd E:\PreviewForge
$env:GOMODCACHE = Join-Path $PWD '.cache\gomod'
go mod download golang.org/toolchain@v0.0.1-go1.26.9.linux-amd64
wsl --distribution Ubuntu --exec bash /mnt/e/PreviewForge/scripts/linux-checks.sh
```

Эквивалент для обычного Linux с установленным Go и gcc:

```bash
bash scripts/linux-checks.sh
python3 scripts/test-kind-scripts.py
python3 scripts/test-e2e-cleanup.py
```

Текущий harness выполняет 21 сценарий cleanup/demo с fake CLI и 6 проверок синтетического отчёта; Bash isolation проверяет 11 случаев, Windows — 7. Это регрессии защиты скриптов, не приложение в Kubernetes. Helm milestone подтверждён настоящим CI kind 20/20: [отчёт](verification/2026-10-30-helm.md). `scripts/linux-checks.sh` также устанавливает проверенный Helm 4.3.1 в `bin/linux` и запускает chart lint/render/schema/package tests. Новые `load`/`faults` и методика измерений — в [LOAD-TESTING.md](LOAD-TESTING.md); текущий полный gate требует 22 ID с Helm или 21 с явным Kustomize и файл `stress-report.json`.

Envtest запускает менеджер под идентичностью ServiceAccount из поставляемого RBAC; отдельно проверяет ограниченный PR deployer SA. Проверяются positive/negative SelfSubjectAccessReview, leader election, status subresource, HTTP 409 при конфликте Status/TTL Delete, UID precondition и отказ удаления чужого Namespace. Ready моделируется записью Deployment status: Deployment controller/kubelet/GC в envtest отсутствуют.

## Настоящий Kubernetes: Docker Desktop и kind

1. Запустить Docker Desktop с Linux containers и дождаться рабочего `docker info`.
2. Для выполнения Bash e2e из Ubuntu включить Docker Desktop → Settings → Resources → WSL Integration → Ubuntu. Проверить `docker info` в Ubuntu. Linux kubectl уже загружен вместе с envtest assets; его можно добавить в PATH командами ниже. Наличие `kubectl.exe` на Windows не заменяет Linux CLI.
3. Выбрать локальный Docker context с unix/npipe endpoint; удалённые DOCKER_HOST/context запрещены скриптами. Создать только новый тестовый кластер. Оба demo-скрипта отказываются использовать существующие кластеры, включая одноимённый `previewforge-test-*`, и не изменяют пользовательский kubeconfig. Каждый запуск хранит конфигурацию отдельно в `bin/runs/<cluster>/kubeconfig`; `bin/kind.kubeconfig` — удобная копия последнего запуска.

### Полный e2e в Git Bash без WSL Integration

Если Docker Engine работает в Windows, полный Bash-сценарий можно выполнить из **Git Bash**, используя Windows Go, Docker и kubectl. Это отдельный вариант запуска; Linux race/envtest по-прежнему выполняются в Ubuntu WSL2. В текущей среде этот путь проверен с kind 0.33.0, kubectl 1.36.1 и Kubernetes 1.37.0.

Текущий полный suite требует Helm 4.3.1. Поместите `helm.exe` из
[официального Windows amd64 архива](https://get.helm.sh/helm-v4.3.1-windows-amd64.zip)
в `bin/`, предварительно сравнив SHA256 архива:
`fcfd7b796dcf0f225abc40b5636a3b01f55f3de2fb1dfb18475df94e62c14112`.
Проверьте `bin/helm.exe version --short`. Это установка инструмента в проект,
системные PATH/настройки Helm менять не требуется. Первый Helm milestone 20/20
проверен в CI; его ранний локальный прогон остановился на Docker Hub TLS timeout.
Новый локальный full suite 22/22 на `12b4761` подтвердил поведение и cleanup,
но его задержки очистки отклонены независимым checker. Исправленный `f91ace7`
прошёл локальный focused load/faults; [измерения и история проверок](verification/2026-10-30-load-faults.md).

```bash
cd /e/PreviewForge
set -euo pipefail
export GOMODCACHE="$(cygpath -w "$PWD/.cache/gomod")"
export GOCACHE="$(cygpath -w "$PWD/.cache/go-build")"
export GOBIN="$(cygpath -w "$PWD/bin")"
export PATH="$PWD/bin:$PATH"
go install sigs.k8s.io/kind@v0.33.0
go build -trimpath -o bin/previewctl.exe ./cmd/previewctl
go build -trimpath -o bin/admissioncert.exe ./cmd/admissioncert
go build -trimpath -o bin/stressbench.exe ./test/e2e/stress
export PREVIEWFORGE_PREVIEWCTL=previewctl.exe PREVIEWFORGE_ADMISSIONCERT=admissioncert.exe PREVIEWFORGE_HELM=helm.exe
export PREVIEWFORGE_TEST_CLUSTER="previewforge-test-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
export PREVIEWFORGE_RUN_TOKEN="$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
mkdir -p bin/ci
bash scripts/e2e.sh 2>&1 | tee "bin/ci/$PREVIEWFORGE_TEST_CLUSTER.log"
bash scripts/verify-e2e-result.sh
```

`cygpath -w` задаёт пути кешей и установки для Windows Go. Имя кластера и token экспортируются в родительской оболочке, чтобы e2e и проверка отчёта использовали одну идентичность запуска. `pipefail` сохраняет ошибку e2e при записи через `tee`. Сценарий использует прежние ownership guards, отдельный kubeconfig/context и обязательный cleanup; включать WSL Integration для этого варианта не требуется.

### Demo в PowerShell и полный e2e в Linux

Первый запуск из PowerShell без make/Bash:

```powershell
cd E:\PreviewForge
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\kind-demo.ps1
$taskConfig = Join-Path $PWD 'bin\kind.kubeconfig'
$taskContext = kubectl --kubeconfig $taskConfig config current-context
$taskNamespace = kubectl --kubeconfig $taskConfig --context $taskContext get previewenvironment pr-42 -o 'jsonpath={.status.namespace}'
kubectl --kubeconfig $taskConfig --context $taskContext -n $taskNamespace port-forward svc/app 8080:80
# В другом терминале открыть http://localhost:8080
```

Полная проверка из Linux/Ubuntu с Go, gcc, make, Docker и kubectl:

```bash
cd /mnt/e/PreviewForge
# Если Linux Go ещё не установлен в PATH:
export PATH="$PWD/.cache/gomod/golang.org/toolchain@v0.0.1-go1.26.9.linux-amd64/bin:$PATH"
export GOMODCACHE="$PWD/.cache/gomod"
export GOCACHE="$PWD/.cache/linux/go-build"
ASSETS=$(bin/linux/setup-envtest use 1.37.0 --bin-dir "$PWD/bin/linux/envtest" -p path)
export PATH="$ASSETS:$PATH"
make LOCALBIN="$PWD/bin/linux" test-e2e
```

E2e создаёт новый `previewforge-test-<random>` и использует явные `--kubeconfig`/`--context` во всех kubectl-командах. Image имеет отдельный tag `previewforge:<cluster>`; Kustomize overlay конкретного запуска не меняет исходные manifests и не перезаписывает общий `previewforge:dev` image. Проверяются:

- Создание CR, Namespace/Deployment/Service, controller ownerReferences и Ready.
- Настоящие готовые Pod; при закрытом readiness-порте работающий Pod не переводит CR в Ready, исправление порта завершает rollout.
- Restricted example с настраиваемыми ресурсами; LimitRange defaults, admission-отказ privileged/oversized/CPU-overquota Pod, восстановление ResourceQuota/LimitRange и cleanup Namespace. На этапе resource controls gate требовал 17 check ID; результаты прежних suite сохраняют свои исторические counts.
- Сетевой этап расширил suite до 18 check ID; текущий полный suite с admission, Helm и нагрузкой требует 22. Устанавливается Calico 3.33.0 в новый kind с отключённым default CNI. Проверяются реальные allow/deny через Pod IP/Service, CoreDNS UDP/TCP, публичный TCP с внешним положительным контролем, точные gateway peers/port, восстановление/restart и отзыв доступа. Контракт — в [NETWORK-ISOLATION.md](NETWORK-ISOLATION.md); наличие политики на kindnet не считается enforcement.
- Новая UID Deployment после его удаления; исправление drift replicas.
- Изменение spec при остановленном операторе, актуальная generation и Ready после рестарта.
- HTTP запросы к Service DNS и ClusterIP из настоящего Pod, ready EndpointSlice и содержимое ответа nginx.
- Удержание cleanup finalizer до исчезновения Namespace, когда другой finalizer блокирует удаление.
- TTL, истёкший во время простоя оператора, и исчезновение CR/Namespace после рестарта.
- TTL при работающем операторе после подтверждённого Ready, с полным исчезновением Namespace.
- Ручное удаление окружения с завершением Namespace cleanup.
- Чужой Namespace с рассчитанным именем и поддельными labels: отказ adoption/delete, сохранение UID/resourceVersion и блокировка finalizer CR.
- Посторонний Namespace и ConfigMap-canary остаются неизменными.

**Bash e2e удаляет свой кластер после успеха и ошибки.** До удаления сохраняет CR/Namespace, Pods (включая image IDs), workloads, events, operator logs и kind logs в `bin/runs/<cluster>/artifacts`. Чужие fixtures последнего сценария существуют только внутри этого одноразового кластера и удаляются вместе с ним. Ни один чужой кластер не удаляется. Ошибка cleanup сохраняет claim и делает тест неуспешным; отсутствие `cleanup.txt` нельзя считать успешной очисткой.

Demo-скрипты для ручной работы по-прежнему оставляют свой кластер. Для аварийного cleanup Bash e2e после потери процесса используйте сохранённые имя/token только этого запуска и тот же локальный Docker endpoint:

```bash
export PATH="$PWD/bin/linux:$PATH"
export PREVIEWFORGE_TEST_CLUSTER=previewforge-test-<имя-из-вывода>
export PREVIEWFORGE_RUN_TOKEN=$(cat "bin/runs/$PREVIEWFORGE_TEST_CLUSTER/.cluster-owner")
bash scripts/cleanup-kind.sh
```

Для повторной проверки создаётся новый кластер; предыдущий никогда не переиспользуется автоматически. Результаты фиксируются в [TESTING.md](TESTING.md), статус этапа — в [ROADMAP.md](ROADMAP.md).

## GitHub Actions при недоступном локальном Docker

Workflow: [operator-e2e.yml](../.github/workflows/operator-e2e.yml). Срабатывает на `push`, `pull_request` и `workflow_dispatch`. Это проверка оператора этапа 1; автоматического создания preview для PR, публикации image и доступа к внешнему кластеру здесь нет.

Оба job запускаются на GitHub-hosted `ubuntu-24.04` с Go 1.26.9. Первый выполняет vet/build/race/envtest под RBAC оператора и регрессии скриптов. Второй устанавливает kind 0.33.0 и kubectl 1.37.0 с проверкой checksum, создаёт новый `previewforge-test-ci-<run_id>-<attempt>` с отдельным kubeconfig, собирает и загружает отдельный image и запускает `scripts/e2e.sh`. Pipeline с `tee` сохраняет исходный exit code через Bash pipefail.

Очистка: ранний EXIT/INT/TERM trap плюс отдельный `always()` шаг. Удаление разрешается только с claim/token текущего запуска и неизменным локальным Docker endpoint; при preflight отказе использовать существующий кластер claim не появляется и cleanup ничего не удаляет. Затем CI требует нулевые коды e2e/cleanup, все 22 различных check IDs с Helm (21 с явным Kustomize), `stress-report.json` с исходными задержками и `cleanup.txt` с подтверждённым отсутствием кластера. Diagnostics загружаются даже при ошибке; kubeconfig, token claim и credentials не входят в artifact paths. SIGKILL/потеря runner может помешать cleanup-скрипту; GitHub-hosted VM одноразовая, self-hosted runner здесь не используется.

Как запустить:

1. Поместить исходники и workflow в ветку [T1mof/previewForge](https://github.com/T1mof/previewForge) и выполнить push. Этот репозиторий выбран пользователем 2026-10-27; module path приведён к его URL. Push запускает workflow автоматически.
2. Открыть репозиторий → Actions → **Operator verification**. Push запускает проверку автоматически. Для ручного запуска workflow должен присутствовать на default branch; выбрать **Run workflow** и ветку с проверяемым кодом. При наличии GitHub CLI и авторизации: `gh workflow run operator-e2e.yml --repo OWNER/REPO --ref BRANCH`.
3. Дождаться успешного **Linux race and envtest**, затем **Real Pods and cleanup in kind**. Скачать artifact `kind-e2e-<run_id>-<attempt>` и проверить `checks.txt`, `result.txt`, `cleanup.txt`, `http.txt`, `pods.yaml`, `ingress-http.txt`, `traefik.log` и `e2e.log`. Успех статического lint или одной Linux suite не закрывает этап 1.
4. Записать URL run, commit SHA (в `result.txt`), версии инструментов и итог всех восьми требований в TESTING.md. Отметить e2e/cleanup в ROADMAP только после настоящего успешного job с этими доказательствами.

Подтверждённый статус на 2026-10-27: локальный Docker доступен из Windows; настоящий `scripts/e2e.sh` прошёл через Git Bash все 13 проверок и удалил свой кластер. WSL Integration для Ubuntu выключена; прежний отказ Linux e2e на Docker preflight не мешает варианту Git Bash. Linux vet/build/race/envtest выполнены отдельно. Результаты и artifacts — в [TESTING.md](TESTING.md). Удалённый CI подтверждён: [run 40576650171](https://github.com/T1mof/previewForge/actions/runs/40576650171), commit 7d0a156, оба job SUCCESS. Скачанный artifact и cleanup проверены отдельно; см. [отчёт этапа 1](verification/2026-10-27-stage1.md). Риски RBAC, сети и удаления всего Namespace, включая условия применимости NetworkPolicy, — в [SECURITY.md](SECURITY.md).

Источники по настройке среды: [Microsoft WSL](https://learn.microsoft.com/en-us/windows/wsl/install), [Kubebuilder envtest](https://book.kubebuilder.io/reference/envtest), [Docker Desktop WSL](https://docs.docker.com/desktop/features/wsl/), [установка kubectl в Linux](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/).

Актуальный полный CI на `f91ace7`: [41071421817](https://github.com/T1mof/previewForge/actions/runs/41071421817), 22/22, gate/cleanup и downloaded artifact 12987182762 проверены. [Измерения и исходные данные](verification/2026-10-30-load-faults.md); локально та же ревизия прошла focused load/faults с независимой проверкой положительных задержек.
