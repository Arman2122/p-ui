# Canonical task runner. Mirrors .github/workflows/ci.yml so `make verify`
# reproduces the PR gate locally. Run `make help` for the list.

SHELL := bash
GO_PKGS = $(shell go list ./... | grep -v '/frontend/node_modules/')
FRONTEND = frontend
NODE_CORE = .cache/node-tools/nodecore

.PHONY: nodecore
nodecore:
	@mkdir -p .cache/node-tools
	CGO_ENABLED=0 GOOS="$$(go env GOHOSTOS)" GOARCH="$$(go env GOHOSTARCH)" go build -o $(NODE_CORE) ./tools/nodecore

# Dropping go-sqlite3 removed the last cgo dependency, so the panel builds as
# pure Go and links statically. CI sets the same value.
export CGO_ENABLED = 0

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

# go:embed of internal/web/dist needs the dir to exist even when the
# frontend bundle has not been built. CI stubs it the same way.
.PHONY: dist-stub
dist-stub:
	@mkdir -p internal/web/dist && touch internal/web/dist/.gitkeep

.PHONY: gen
gen: ## Regenerate Zod schemas + OpenAPI from Go sources
	cd $(FRONTEND) && npm run gen

.PHONY: gen-check
gen-check: gen ## Fail if generated files are stale
	git diff --exit-code -- frontend/src/generated frontend/public/openapi.json

.PHONY: lint-go
lint-go: dist-stub nodecore ## golangci-lint on Go sources
	$(NODE_CORE) -core xray -lint

.PHONY: lint-fe
lint-fe: ## ESLint on frontend sources
	cd $(FRONTEND) && npm run lint

.PHONY: lint
lint: lint-go lint-fe ## All linters

.PHONY: typecheck
typecheck: ## tsc --noEmit
	cd $(FRONTEND) && npm run typecheck

.PHONY: msw-worker-check
msw-worker-check: ## Verify the Storybook worker matches its installed MSW runtime
	cmp $(FRONTEND)/public/mockServiceWorker.js $(FRONTEND)/node_modules/msw/lib/mockServiceWorker.js

.PHONY: test-go
# -timeout for the same reason `race` carries one: Go's default is 10 minutes
# PER PACKAGE and internal/web/service already measures ~600s. Ledger L16.
test-go: dist-stub nodecore ## Go tests (shuffle, no cache)
	$(NODE_CORE) -core xray -go test -- -shuffle=on -count=1 -timeout 30m $(GO_PKGS)

# The race detector is the one thing that still needs cgo, so this target opts
# back in. It is a local-only tool; the PR gate (`verify`) does not run it.
.PHONY: race
race: dist-stub nodecore ## Go tests with the race detector (needs cgo + a C compiler)
	CGO_ENABLED=1 $(NODE_CORE) -core xray -go test -- -race -shuffle=on -count=1 -timeout 30m $(GO_PKGS)

.PHONY: test-fe
test-fe: ## Frontend tests (vitest)
	cd $(FRONTEND) && npm test

.PHONY: test
test: test-go test-fe ## All tests

.PHONY: vulncheck
vulncheck: dist-stub nodecore ## govulncheck
	GOBIN="$(abspath .cache/node-tools)" go install golang.org/x/vuln/cmd/govulncheck@latest
	PATH="$(abspath .cache/node-tools):$$PATH" $(NODE_CORE) -core xray -tool govulncheck

.PHONY: build-fe
build-fe: ## Build the Vite bundles into internal/web/dist
	cd $(FRONTEND) && npm run build

.PHONY: build
build: build-fe nodecore ## Build the frontend then the Go binary
	$(NODE_CORE) -core xray -go build -- ./...

.PHONY: build-storybook
build-storybook: ## Build the static Storybook (compile-checks all stories)
	cd $(FRONTEND) && npm run build-storybook

# The PR gate. Matches ci.yml: codegen freshness, both linters, typecheck,
# both test suites, a full build, and the Storybook compile-check.
.PHONY: verify
verify: gen-check lint typecheck msw-worker-check test build build-storybook ## Full local gate (mirrors CI)
	@echo "verify: OK"
