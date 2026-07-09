GOLANGCI_LINT_VERSION := v2.12.2
GOIMPORTS_VERSION := v0.45.0

# Sub-modules carry their own go.mod (heavy/opt-in deps kept out of the core
# import graph). `go test ./...` from root does NOT reach them, so every
# all-modules target loops over MODULES. They compile against this tree rather
# than the published core because go.work lists them all.
SUB_MODULES = \
	./integrations/gormguard \
	./integrations/sqlxguard \
	./integrations/pgxguard \
	./integrations/bunguard \
	./integrations/xormguard \
	./integrations/entguard \
	./parsers/pgparser \
	./parsers/mysqlparser
MODULES = . $(SUB_MODULES)

# The integration module is never released, so it is not in SUB_MODULES. Its
# tests sit behind the `integration` build tag and need the compose stack
# below, so it is also excluded from the plain vet/test loops.
INTEGRATION_MODULE := ./test/integration
COMPOSE_FILE := test/integration/docker-compose.yml

# Override to point the integration tests at your own servers. An unset DSN
# skips that database's tests rather than failing them.
SQLGUARD_TEST_PG_DSN ?= postgres://sqlguard:sqlguard@localhost:55432/sqlguard?sslmode=disable
SQLGUARD_TEST_MYSQL_DSN ?= root:sqlguard@tcp(localhost:53306)/sqlguard
SQLGUARD_TEST_MARIADB_DSN ?= root:sqlguard@tcp(localhost:53307)/sqlguard

.PHONY: all help setup deps ci test test-v test-race coverage lint lint-fix fix fmt fmt-check vet tidy build cli install bench clean db-up db-down test-integration vet-integration

all: tidy fmt vet lint build test

## Show available targets
help:
	@echo "Available targets:"
	@echo "  all           - Tidy, format, vet, lint, build, test (all modules)"
	@echo "  setup         - Install development tools"
	@echo "  deps          - Download module dependencies (all modules)"
	@echo "  ci            - CI pipeline (fmt-check, vet, lint, test-race)"
	@echo "  test          - Run tests across all modules"
	@echo "  test-v        - Run tests with verbose output (all modules)"
	@echo "  test-race     - Run tests with race detector (all modules)"
	@echo "  db-up         - Start the integration-test databases (docker compose)"
	@echo "  db-down       - Stop the integration-test databases and drop volumes"
	@echo "  test-integration - Run explain/ tests against the live databases"
	@echo "  coverage      - Run tests with merged coverage report (all modules)"
	@echo "  vet           - Run go vet (all modules)"
	@echo "  lint          - Run golangci-lint (all modules)"
	@echo "  lint-fix      - Run golangci-lint with --fix (root module)"
	@echo "  fix           - fmt + lint-fix"
	@echo "  fmt           - Format code (gofmt -s + goimports)"
	@echo "  fmt-check     - Verify formatting without modifying files"
	@echo "  tidy          - Run go mod tidy (all modules)"
	@echo "  build         - Build all packages (all modules)"
	@echo "  cli           - Build the sqlguard CLI to bin/sqlguard"
	@echo "  install       - Install the CLI to \$$GOPATH/bin"
	@echo "  bench         - Run benchmarks (all modules)"
	@echo "  clean         - Remove build/coverage artifacts"

## Install development tools (skips if already present)
setup:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	}
	@command -v goimports >/dev/null 2>&1 || { \
		echo "Installing goimports $(GOIMPORTS_VERSION)..."; \
		go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION); \
	}

## Download module dependencies across all modules
deps:
	@for mod in $(MODULES); do \
		echo "==> Downloading deps $$mod"; \
		(cd $$mod && go mod download) || exit 1; \
	done

## CI: run formatting check, vet, lint and tests with race detector
ci: fmt-check vet lint test-race

## Build all packages across all modules (compile check)
build:
	@for mod in $(MODULES); do \
		echo "==> Building $$mod"; \
		(cd $$mod && go build ./...) || exit 1; \
	done

## Build the CLI binary
cli:
	@echo "==> Building bin/sqlguard"
	@go build -o bin/sqlguard ./cmd/sqlguard

## Install the CLI to $GOPATH/bin
install:
	go install ./cmd/sqlguard

## Run tests across all modules
test:
	@for mod in $(MODULES); do \
		echo "==> Testing $$mod"; \
		(cd $$mod && go test -count=1 ./...) || exit 1; \
	done

## Run tests with verbose output across all modules
test-v:
	@for mod in $(MODULES); do \
		echo "==> Testing (verbose) $$mod"; \
		(cd $$mod && go test -v -count=1 ./...) || exit 1; \
	done

## Run tests with race detector across all modules
test-race:
	@for mod in $(MODULES); do \
		echo "==> Testing (race) $$mod"; \
		(cd $$mod && go test -race -count=1 ./...) || exit 1; \
	done

## Start the integration-test databases and wait for them to become healthy
db-up:
	docker compose -f $(COMPOSE_FILE) up -d --wait

## Stop the integration-test databases and remove their volumes
db-down:
	docker compose -f $(COMPOSE_FILE) down -v

## Run the explain/ integration tests against live Postgres, MySQL and MariaDB.
## Requires `make db-up` first. Each database's tests skip if its DSN is unset.
test-integration:
	@echo "==> Integration testing $(INTEGRATION_MODULE)"
	@cd $(INTEGRATION_MODULE) && \
		SQLGUARD_TEST_PG_DSN='$(SQLGUARD_TEST_PG_DSN)' \
		SQLGUARD_TEST_MYSQL_DSN='$(SQLGUARD_TEST_MYSQL_DSN)' \
		SQLGUARD_TEST_MARIADB_DSN='$(SQLGUARD_TEST_MARIADB_DSN)' \
		go test -tags=integration -count=1 -race ./...

## Vet the integration module (needs the build tag to see any files)
vet-integration:
	@echo "==> Vetting $(INTEGRATION_MODULE)"
	@cd $(INTEGRATION_MODULE) && go vet -tags=integration ./...

## Run tests with coverage and generate a merged report across all modules
coverage:
	@echo "mode: atomic" > coverage.out
	@for mod in $(MODULES); do \
		echo "==> Coverage $$mod"; \
		(cd $$mod && go test -race -covermode=atomic -coverprofile=cover.tmp ./...) || exit 1; \
		if [ -f $$mod/cover.tmp ]; then tail -n +2 $$mod/cover.tmp >> coverage.out && rm $$mod/cover.tmp; fi; \
	done
	@go tool cover -func=coverage.out | tail -1
	@echo "Full report: go tool cover -html=coverage.out"

## Run linter across all modules
lint: setup
	@for mod in $(MODULES); do \
		echo "==> Linting $$mod"; \
		(cd $$mod && golangci-lint run --timeout=5m ./...) || exit 1; \
	done

## Run golangci-lint with auto-fix (root module)
lint-fix: setup
	golangci-lint run --fix ./...

## Fix code formatting and linting issues
fix: fmt lint-fix

## Format code (recurses the whole tree, all modules)
fmt: setup
	@gofmt -s -w .
	@goimports -w .

## Check formatting without modifying files (used in CI)
fmt-check: setup
	@test -z "$$(gofmt -s -l . | tee /dev/stderr)" || { echo "Unformatted files found. Run 'make fmt'."; exit 1; }
	@test -z "$$(goimports -l . | tee /dev/stderr)" || { echo "Unordered imports found. Run 'make fmt'."; exit 1; }

## Run go vet across all modules
vet:
	@for mod in $(MODULES); do \
		echo "==> Vetting $$mod"; \
		(cd $$mod && go vet ./...) || exit 1; \
	done

## Run go mod tidy across all modules
tidy:
	@for mod in $(MODULES); do \
		echo "==> Tidying $$mod"; \
		(cd $$mod && go mod tidy) || exit 1; \
	done

## Run benchmarks across all modules
bench:
	@for mod in $(MODULES); do \
		echo "==> Benchmarking $$mod"; \
		(cd $$mod && go test -bench=. -benchmem -run='^$$' ./...) || exit 1; \
	done

## Remove build and coverage artifacts
clean:
	@rm -f coverage*.out cover.tmp coverage.txt coverage.html
	@find . -name cover.tmp -delete 2>/dev/null || true
	@rm -rf dist/ build/ bin/

