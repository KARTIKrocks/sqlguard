GOLANGCI_LINT_VERSION := v2.12.2
GOIMPORTS_VERSION := v0.45.0

MODULE_PATH := github.com/KARTIKrocks/sqlguard

# Sub-modules carry their own go.mod (heavy/opt-in deps kept out of the core
# import graph). `go test ./...` from root does NOT reach them, so every
# all-modules target loops over MODULES.
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

.PHONY: all help setup deps ci test test-v test-race coverage lint lint-fix fix fmt fmt-check vet tidy build cli install bench clean release-prep

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
	@echo "  release-prep  - Pin sub-modules to a release version (VERSION=vX.Y.Z)"

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

## Prepare sub-modules for release: drop the local replace and pin the parent
## version. Usage: make release-prep VERSION=v0.1.0
## Run this AFTER the root module tag for VERSION exists and is pushed, then
## commit and tag the sub-modules. Restore replaces afterwards for local dev
## (git checkout -- '**/go.mod') or develop against the published version.
release-prep:
ifndef VERSION
	$(error VERSION is required. Usage: make release-prep VERSION=v0.1.0)
endif
	@for mod in $(SUB_MODULES); do \
		echo "==> release-prep $$mod"; \
		(cd $$mod && go mod edit -dropreplace $(MODULE_PATH) -require $(MODULE_PATH)@$(VERSION)) || exit 1; \
	done
	@echo ""
	@echo "Done! Sub-modules now require $(MODULE_PATH)@$(VERSION) (replace dropped)."
	@echo "Next steps (root tag $(VERSION) must already be pushed):"
	@echo "  git add -A && git commit -m 'Prepare release $(VERSION)'"
	@echo "  git tag integrations/gormguard/$(VERSION)"
	@echo "  git tag integrations/sqlxguard/$(VERSION)"
	@echo "  git tag integrations/pgxguard/$(VERSION)"
	@echo "  git tag integrations/bunguard/$(VERSION)"
	@echo "  git tag integrations/xormguard/$(VERSION)"
	@echo "  git tag integrations/entguard/$(VERSION)"
	@echo "  git tag parsers/pgparser/$(VERSION)"
	@echo "  git tag parsers/mysqlparser/$(VERSION)"
	@echo "  git push origin main --tags"
