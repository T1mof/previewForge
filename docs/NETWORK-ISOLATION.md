# Сетевая изоляция

Operator создаёт две стандартные `networking.k8s.io/v1` NetworkPolicy **до Deployment**. Они принадлежат точному controller owner UID PreviewEnvironment, восстанавливаются через watch и исправляют drift с optimistic locking. Совпадение имени/labels не разрешает принятие чужой политики. При конфликте Deployment не создаётся. У оператора нет прямого NetworkPolicy delete/status permission; cleanup выполняется через принадлежащий CR Namespace.

## Контракт трафика

| Трафик | Правило |
| --- | --- |
| Внутри одного preview Namespace | Разрешён между Pod на любых портах |
| DNS | UDP и TCP 53 только к настроенным DNS Pod в настроенном Namespace |
| Между разными preview Namespace | Закрыт |
| Из preview к другим Pod/Service и публичным адресам | Закрыт, кроме DNS |
| В preview из других Namespace | Закрыт, кроме доверенного Ingress gateway |
| От gateway к приложению | Только TCP `spec.port`, только когда `spec.ingress` задан |

`previewforge-isolation` выбирает все Pod Namespace и включает ingress/egress isolation. Он разрешает внутрисредовое взаимодействие и DNS. `previewforge-ingress` выбирает app Pod по selector с UID CR и добавляет разрешение gateway. При отключении Ingress политика остаётся с пустым ingress allow list; при изменении `spec.port` старый gateway port закрывается. Политики аддитивны: пустой allow list второй политики не отменяет разрешённый локальный трафик первой.

DNS и gateway peers используют **один** peer с `namespaceSelector` и `podSelector`: оба условия должны выполняться. Namespace определяется стандартным `kubernetes.io/metadata.name`, а не произвольным label `trusted=true`. Скопированный gateway label в другом Namespace не даёт доступ. Метки Pod определяют сетевую идентичность и требуют контроля записи в доверенный Namespace; для владения объектами по-прежнему используется controller reference/UID.

## Настройка администратором

Defaults DNS: Namespace `kube-system`, Pod selector `k8s-app=kube-dns`. При другом Pod-based DNS передайте `--dns-namespace` и `--dns-pod-selector`. NodeLocal DNSCache, hostNetwork DNS и DNS forwarding через отдельный адрес этим контрактом не поддерживаются.

Для Ingress администратор добавляет к существующим manager args, например:

```yaml
- --ingress-controller-namespace=ingress-nginx
- --ingress-controller-pod-selector=app.kubernetes.io/name=ingress-nginx,app.kubernetes.io/component=controller
```

Оба значения обязательны вместе; selector не может быть пустым. CR не может выбрать доверенный Namespace, произвольный gateway selector или разрешить публичный egress. Если Ingress запрошен без admin config, operator устанавливает базовую изоляцию и пустое gateway-разрешение, затем сообщает `NetworkPolicyFailed`; Deployment не создаётся. В `config/default` gateway не задан, поскольку его размещение зависит от кластера.

Локальный operator overlay использует Namespace `previewforge-ingress-system` и selector `platform.previewforge.dev/ingress-gateway=true`. Этот дополнительный label имеет только Pod template тестового Traefik; Service/ReplicaSet selectors остаются отдельными. CNI, Ingress controller и DNS конфигурацию устанавливает администратор, а не автор PreviewEnvironment.

## Воспроизведение

Для настоящего enforcement используйте Bash/Git Bash:

```bash
PREVIEWFORGE_KIND_CNI=calico bash scripts/kind-demo.sh
# Или полный suite с автоматической очисткой:
make test-e2e
```

Полный e2e по умолчанию выбирает Calico. `kind-demo.sh` без переменной и native PowerShell demo сохраняют простой kindnet-вариант; **kindnet здесь не доказывает и не обеспечивает исполнение NetworkPolicy**. Полный сетевой check откажется считать такой запуск PASS. `make kind-demo`/`make ingress-demo` выбирают Calico, если CNI не указан явно. Применение обычных manifests в произвольном кластере само по себе не устанавливает CNI.

Calico 3.33.0 выбран по [проверяемой совместимости с Kubernetes 1.37](https://docs.tigera.io/calico/latest/getting-started/kubernetes/requirements). Скрипт принимает только новый `previewforge-test-*`, отключает default CNI, сохраняет kubeconfig запуска и проверяет claim перед установкой. Upstream manifest закреплён SHA256; три image закреплены manifest digest. VXLAN использует Pod CIDR `10.244.0.0/16`; BGP/IPIP отключены. Установка выполняется только внутри нового kind; не меняет CNI существующих кластеров, Docker/WSL settings или пользовательский kubeconfig. [kind CNI configuration](https://kind.sigs.k8s.io/docs/user/configuration/#disable-default-cni), [официальные Calico manifests](https://github.com/projectcalico/calico/tree/v3.33.0/manifests).

## Доказательства и ограничения

Unit-тесты проверяют AND peers, порты, конфликты владельцев, drift/recreation и отзыв gateway. Envtest проверяет настоящий API/watch/RBAC, но не запускает сетевой dataplane. `scripts/e2e-network.sh` использует отдельный Go probe image и настоящие Pod. HTTP 403 не считается сетевой блокировкой. Отрицательные проверки идут через literal Pod IP/Service IP, с положительными контролями доступности сервера; публичный TCP target проверяется снаружи до и после отказа из приложения. DNS UDP/TCP проверяются отдельно; HTTP-сервер на 53 в другом Namespace должен оставаться недоступным. Проверяются gateway Namespace **и** Pod identity, порт, restart, новые UID политик и Namespace cleanup. Enforcement подтверждён [CI 40915413741, 18/18 и независимо проверенный artifact](verification/2026-10-29-network-isolation.md): 21 positive и 17 denied probes. Локальный полный 18/18 не заявляется; остальные результаты — в [TESTING.md](TESTING.md).

Наличие объектов в API и Ready по TCP probe **не доказывают enforcement CNI**. Программирование правил асинхронно; API create перед Deployment не гарантирует нулевое окно сети при запуске/обновлении. Удаление администратором базовой политики открывает окно до восстановления; существующие соединения могут обрабатываться иначе, чем новые. Дополнительная allow policy расширяет доступ. Не выдавайте недоверенным субъектам запись NetworkPolicy, Namespace labels или доверенных Pod labels.

Обычная NetworkPolicy не является полным firewall узла/API, не определяет HTTP authorization, не фильтрует DNS names, не предотвращает DNS exfiltration и не защищает от скомпрометированного cluster-wide оператора. HostNetwork gateways, IPv6/multi-node/provider matrix и публичный внешний PR lifecycle требуют отдельных проверок. Кластер остаётся выделенной средой для доверенных приложений; [модель доверия](SECURITY.md).
