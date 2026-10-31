IMG ?= deploypilot:dev
.PHONY: fmt vet test build generate manifests docker kind-up kind-load install run up down
fmt:
	gofmt -w $$(find api cmd internal -name '*.go')
vet:
	go vet ./...
test:
	go test ./...
build:
	go build -o bin/deploypilot ./cmd/deploypilot
generate:
	@echo 'API types are handwritten; update manifests/crd.yaml and api/openapi.yaml with schema changes.'
manifests:
	kubectl kustomize config
docker:
	docker build -t $(IMG) .
kind-up:
	kind create cluster --name deploypilot
kind-load:
	kind load docker-image $(IMG) --name deploypilot
install:
	kubectl apply -k config
run:
	go run ./cmd/deploypilot
up:
	./scripts/dev-up.sh
down:
	kind delete cluster --name deploypilot
