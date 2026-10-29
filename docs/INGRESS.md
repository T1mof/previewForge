# Демонстрация Ingress

Этап 1 подтверждён [GitHub run 40576650171](https://github.com/T1mof/previewForge/actions/runs/40576650171). Следующий согласованный шаг — локальный демонстрационный кластер с Ingress. PR integration следует после проверки этой демонстрации.

## API

Для доступа к приложению настройте operator flags `--ingress-controller-namespace` и `--ingress-controller-pod-selector`: NetworkPolicy разрешает только этот gateway и только TCP `spec.port`. IngressClass сам по себе не определяет сетевую идентичность контроллера. Без admin config CR получает `NetworkPolicyFailed`. Локальные overlays уже задают identity тестового Traefik; правила и CNI — в [NETWORK-ISOLATION.md](NETWORK-ISOLATION.md).

```yaml
spec:
  image: nginx:alpine
  ingress:
    host: preview.example.test
    className: previewforge-demo
    # tlsSecretName: preview-tls
```

`ingress` необязателен. При его наличии обязательны точный DNS host и явный IngressClass. IP-адреса и wildcard hosts отклоняются CRD; operator также проверяет поля перед созданием ресурсов. Пример целиком: [sample](../config/samples/platform_v1alpha1_previewenvironment_ingress.yaml).

Оператор создаёт стандартный `networking.k8s.io/v1` Ingress `app` в preview Namespace. Маршрут `/` с `pathType=Prefix` направлен в Service `app` на именованный порт `http`; изменение порта приложения не оставляет устаревший backend. IngressClass, DNS и уникальность hostname обеспечивает администратор кластера. Для маршрутизации нужен работающий [Ingress controller](https://kubernetes.io/docs/concepts/services-networking/ingress/).

`status.url` содержит настроенный маршрут `http://<host>`, либо `https://<host>` при указании TLS Secret. Это адрес конфигурации; Ready по-прежнему означает готовность Deployment и согласование ресурсов. DNS, доверие сертификату и внешняя доступность этим статусом не проверяются.

TLS Secret создаётся отдельно в Namespace окружения. PreviewForge не читает, не копирует и не создаёт Secrets. Настройка ссылки TLS и HTTPS URL проверяются unit-тестом. Envtest проверяет API, владение и watch; HTTP-маршрутизация проверяется e2e. Полноценный TLS handshake этим HTTP-сценарием не подтверждается.

## Владение и отключение

Ingress получает controller owner reference с UID PreviewEnvironment. Оператор исправляет drift и восстанавливает удалённый Ingress через Owns watch. Чужой Ingress с тем же именем не принимается под управление; при включённом Ingress CR получает Failed без URL.

Удаление `spec.ingress` запрашивает удаление только принадлежащего CR маршрута. Перед Delete используется свежий APIReader и UID/resourceVersion preconditions. До исчезновения объекта URL очищен, Ready блокируется. При отключении чужой Ingress остаётся нетронутым. Полное удаление PreviewEnvironment по-прежнему удаляет весь принадлежащий ему Namespace, включая его содержимое.

## Локальный demo

Требуются работающий локальный Docker Engine, kind, kubectl, Bash и curl. Подготовка инструментов Git Bash/WSL — в [VERIFICATION.md](VERIFICATION.md). Git Bash использует Windows Docker и не требует WSL Integration.

```bash
# Linux/macOS, с доступными make и Linux инструментами:
make ingress-demo

# Git Bash после добавления bin/ в PATH:
bash scripts/ingress-demo.sh
```

Скрипт создаёт только новый `previewforge-test-demo-*`, отдельные kubeconfig/context/image и локальный port mapping `127.0.0.1:8080 -> NodePort 30080`. Затем устанавливает оператор, приложение и тестовый Traefik, включает Ingress для `localhost` и проверяет настоящий HTTP с хоста. Приложение доступно по `http://localhost:8080` без редактирования DNS или hosts.

Если порт 8080 занят, выберите другой до запуска:

```bash
PREVIEWFORGE_DEMO_HTTP_PORT=18080 bash scripts/ingress-demo.sh
```

В этом случае адрес хоста — `http://localhost:18080`; `status.url` хранит URL стандартного Ingress listener, а дополнительный локальный port mapping показывает demo-скрипт. Порт валидируется до создания кластера; listener привязан только к loopback. Порт 8080 выбран по умолчанию после реального отказа Windows разрешить bind порта 80; системные настройки портов не меняются.

После успеха кластер остаётся для демонстрации. Приложение подчиняется TTL от creationTimestamp и будет удалено по истечении срока. Скрипт печатает точную команду cleanup с именем и claim своего запуска. После ошибки или обычного прерывания demo удаляет только свой кластер; прежние run directories отвергаются до установки cleanup traps. Пользовательский kubeconfig не меняется.

`config/ingress-demo/traefik.yaml` — отдельная инфраструктура для свежего тестового кластера. Используется [Traefik 3.7.16](https://github.com/traefik/traefik/releases/tag/v3.7.16), image закреплён digest. У Traefik собственный ServiceAccount с read-доступом к Services/EndpointSlices/Secrets и доступом к Ingress; оператор сохраняет запрет чтения Secrets. Настройки provider соответствуют [документации Traefik](https://doc.traefik.io/traefik/v3.7/reference/install-configuration/providers/kubernetes/kubernetes-ingress/). Эта demo-конфигурация не устанавливается `config/default`.

## E2e

`make test-e2e` выполняет прежний lifecycle и дополнительный check `ingress`: HTTP через настоящий Traefik, проверка owner UID, восстановление удалённого маршрута, исправление drift, смена host/URL, отключение и настоящий 404 на удалённом маршруте. Report gate требует 14 различных check ID. Ответы сохранены в `artifacts/ingress-http.txt`, логи controller — в `artifacts/traefik.log`. Тестовый e2e-кластер удаляется после успеха и ошибки; постоянный demo запускается отдельно.
