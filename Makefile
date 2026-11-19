SHELL := /usr/bin/env bash
IMG ?= elasticsearch-queue-autoscaler:dev
CONTROLLER_GEN := go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2
ENVTEST := go run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20250517180713-32e5e9e948a5
.PHONY: generate manifests fmt vet test integration build docker-build helm-check verify install

generate:
	$(CONTROLLER_GEN) object paths="./api/..."
manifests:
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd/bases
	cp config/crd/bases/*.yaml charts/elasticsearch-queue-autoscaler/crds/
fmt:
	gofmt -w api cmd internal test
vet:
	go vet ./...
test:
	go test ./api/...
	go test -race -coverprofile=coverage.out ./internal/...
integration:
	KUBEBUILDER_ASSETS="$$( $(ENVTEST) use 1.32.0 -p path )" go test -tags=integration -v ./test/integration/...
build:
	CGO_ENABLED=0 go build -trimpath -o bin/manager ./cmd
docker-build:
	docker build -t $(IMG) .
helm-check:
	helm lint charts/elasticsearch-queue-autoscaler
	helm template eqa charts/elasticsearch-queue-autoscaler --namespace elastic-system --include-crds > config/install.yaml
verify: generate manifests fmt vet test integration build helm-check
install:
	kubectl apply -f config/crd/bases

.PHONY: local-up local-test local-status local-down local-check
local-up:
	python3 hack/local/lab.py up
local-test:
	python3 hack/local/lab.py test
local-status:
	python3 hack/local/lab.py status
local-down:
	python3 hack/local/lab.py down
local-check:
	python3 -m unittest discover -s hack/local -p 'test_*.py' -v
