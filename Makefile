SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
LDFLAGS := -X github.com/pulumi/pulumi/sdk/v3/go/common/version.Version=3.246.0
.PHONY: dev-init build build-binaries build-test image cli migrate bootstrap smoke dev-world test-unit test-contract test-integration test-e2e test-security test-web test-fault test-restore bench verify

dev-init:
	python3 scripts/dev-init.py
build-binaries:
	mkdir -p bin test-results
	go build -ldflags '$(LDFLAGS)' -o bin/backend ./cmd/backend
	go build -o bin/backendctl ./cmd/backendctl
	go build -o bin/pulumi-resource-backendtest ./tests/provider
	go build -o bin/world ./tests/world
cli:
	scripts/build-cli.sh
image:
	docker compose build api
build: build-binaries cli image
build-test:
	mkdir -p bin test-results
	go build -race -tags faulttest -ldflags '$(LDFLAGS)' -o bin/backend ./cmd/backend
	go build -race -o bin/backendctl ./cmd/backendctl
	go build -race -o bin/pulumi-resource-backendtest ./tests/provider
	go build -race -o bin/world ./tests/world
migrate:
	docker compose run --rm api migrate
bootstrap:
	scripts/bootstrap.sh
smoke:
	python3 scripts/smoke.py
dev-world: build-binaries
	bin/world --listen 127.0.0.1:7071 --dir .dev/world
test-unit:
	go test -race ./internal/...
test-contract: build-test
	python3 scripts/test.py contract
test-integration: build-test
	python3 scripts/test.py integration
test-e2e: build-test cli
	python3 scripts/test.py e2e
test-security: build-test
	python3 scripts/test.py security
test-web: build-test
	python3 scripts/test.py web
test-fault: build-test cli
	python3 scripts/test.py fault
test-restore: build-test
	python3 scripts/test.py restore
bench: build-binaries
	python3 scripts/test.py bench
verify:
	$(MAKE) test-unit
	$(MAKE) build-test cli
	python3 scripts/test.py all
web-deps:
	scripts/web-deps.sh
