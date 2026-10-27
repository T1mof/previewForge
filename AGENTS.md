# PreviewForge: постоянные инструкции

- Сначала прочитать README.md, docs/ROADMAP.md и docs/TESTING.md. Исходное ТЗ: docs/SPECIFICATION.md.
- Lifecycle этапа 1 подтверждён локальным kind e2e. Порядок пользователя: сначала подтвердить GitHub CI и локальный kind, затем демонстрационный кластер с Ingress, после него PR integration. Не перескакивать к сложным PR workflows, Helm или observability раньше этих проверок.
- Целевой репозиторий: https://github.com/T1mof/previewForge.git; Go module path github.com/T1mof/previewForge. Пользователь разрешил продолжать разработку самостоятельно; уточнять существенные решения архитектуры и необходимые внешние данные.
- Следовать Kubebuilder Go v4 layout. API: api/v1alpha1; reconcile: internal/controller; entrypoint: cmd/manager.
- PreviewEnvironment cluster-scoped. Никогда не принимать под управление/удалять ресурсы только по labels, имени или status.namespace; проверять controller owner reference и UID.
- Finalizer сохраняется до NotFound Namespace. Delete использует UID/resourceVersion preconditions; patch — optimistic locking.
- TTL считается от creationTimestamp; state хранится в Kubernetes. Не добавлять внешние БД и локальный registry окружений.
- API/RBAC менять в Go markers; затем перегенерировать controller-gen 0.22.0 через make manifests или scripts/check.ps1 -Generate. Не редактировать generated файлы вручную.
- После изменения Go запускать gofmt, go vet и go test ./...; для изменения API/defaulting/watch дополнительно envtest в Linux, для настоящих Pod/GC — e2e в выделенном kind-кластере.
- Для Windows есть scripts/check.ps1. Кеши и инструменты держать в .cache/ и bin/ проекта; эти каталоги исключены из git.
- Указывать реально выполненные проверки. Fake client, компиляция Linux suite и envtest не заменяют e2e; отсутствие среды не считать PASS.
- Не изменять внешний кластер или пользовательский kubeconfig. Скрипты kind создают только новый previewforge-test-* и отказываются переиспользовать существующий. Использовать kubeconfig конкретного запуска bin/runs/<cluster>/kubeconfig и явный context kind-<cluster>.
- Для Linux/race/envtest есть scripts/linux-checks.sh и docs/VERIFICATION.md. WSL2 Ubuntu настроена; Linux Go 1.26.9 в .cache/gomod, Linux build cache отдельный .cache/linux. Envtest работает под ServiceAccount RBAC; не заменять его admin credentials.
- .github/workflows/operator-e2e.yml проверяет этап 1 на GitHub-hosted Linux/kind. Не подключать CI к пользовательским кластерам и не использовать production credentials. Bash e2e удаляет только кластер с claim/token текущего запуска; сохранять EXIT/INT/TERM и always() cleanup. Проверки cleanup/изоляции с fake CLI не считать Kubernetes e2e.
- Обновлять документацию и ROADMAP при изменении поведения. Кратко объяснять важные решения пользователю по-русски.
