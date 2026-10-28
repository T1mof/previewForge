SHELL := /bin/bash
CONTROLLER_GEN_VERSION := v0.22.0
ENVTEST_VERSION := v0.25.2
ENVTEST_K8S_VERSION := 1.37.0
KIND_VERSION := v0.33.0
IMG ?= previewforge:dev
LOCALBIN := $(CURDIR)/bin

.PHONY: generate manifests fmt vet test test-race test-integration build run install deploy docker-build kind-demo ingress-demo test-e2e

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

$(LOCALBIN)/controller-gen: | $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

$(LOCALBIN)/setup-envtest: | $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)

$(LOCALBIN)/kind: | $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/kind@$(KIND_VERSION)

generate: $(LOCALBIN)/controller-gen
	$(LOCALBIN)/controller-gen object paths=./api/v1alpha1

manifests: generate
	$(LOCALBIN)/controller-gen crd rbac:roleName=previewforge-manager-role 'paths=./api/v1alpha1;./internal/controller' output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
	$(LOCALBIN)/controller-gen rbac:roleName=previewforge-pr-deployer-role paths=./internal/prenvironment output:rbac:artifacts:config=config/pr-deployer
	$(LOCALBIN)/controller-gen rbac:roleName=previewforge-metrics-reader paths=./internal/monitoring output:rbac:artifacts:config=config/metrics-reader

fmt:
	gofmt -w api cmd internal examples

vet:
	go vet ./...

test:
	go test ./... -coverprofile=coverage.out

test-race:
	CGO_ENABLED=1 go test -race ./... -count=1

test-integration: $(LOCALBIN)/setup-envtest
	assets="$$($(LOCALBIN)/setup-envtest use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN)/envtest -p path)" && CGO_ENABLED=1 KUBEBUILDER_ASSETS="$$assets" go test -race -tags=integration ./internal/controller -run TestEnvtest -count=1 -v -timeout=3m

build: | $(LOCALBIN)
	go build -trimpath -o $(LOCALBIN)/manager ./cmd/manager
	go build -trimpath -o $(LOCALBIN)/previewctl ./cmd/previewctl

run:
	go run ./cmd/manager

install:
	kubectl apply -k config/crd

deploy:
	kubectl apply -k config/default

docker-build:
	docker build -t $(IMG) .

kind-demo: $(LOCALBIN)/kind
	PATH="$(LOCALBIN):$$PATH" bash scripts/kind-demo.sh

ingress-demo: $(LOCALBIN)/kind
	PATH="$(LOCALBIN):$$PATH" bash scripts/ingress-demo.sh

test-e2e: $(LOCALBIN)/kind build
	PATH="$(LOCALBIN):$$PATH" bash scripts/e2e.sh
