.PHONY: build build-user build-all install test test-scripts test-e2e vet openapi clean graph-serve graph-query web web-gen web-clean dev-db dev-embeddings dev-server dev-web FORCE

# WL-REQ-15: seven executables from one module. The three user binaries are what
# Homebrew installs; three more ship in the container images, and
# lode-gate-check runs only in CI.
USER_BINARIES := lode lode-hook lode-statusline
ALL_BINARIES := $(USER_BINARIES) lode-server lode-watch lode-migrate lode-gate-check

PREFIX := $(if $(shell test -w /usr/local/bin && echo yes),/usr/local,$(HOME))

# Focused build of any one executable: make bin/lode-hook, make bin/lode-server.
# FORCE keeps the decision to rebuild with the Go build cache, which knows the
# real inputs; make does not.
bin/%: FORCE
	go build -trimpath -o $@ ./cmd/$*

FORCE:

# -count=1 bypasses Go's test result cache. PR CI clears it to reuse cached
# results; the merge queue and local runs keep it, since a cached pass cannot
# see changes in Postgres or other external services.
TESTCOUNT ?= -count=1

build: bin/lode ## Build the lode binary into bin/lode

build-user: $(addprefix bin/,$(USER_BINARIES)) ## Build the three end-user binaries

build-all: $(addprefix bin/,$(ALL_BINARIES)) ## Build all seven executables

install: ## Build and install the three end-user binaries to $(PREFIX)/bin
	@for b in $(USER_BINARIES); do \
		go build -trimpath -o $(PREFIX)/bin/$$b ./cmd/$$b || exit 1; \
	done

test: ## Run the unit test suite
	go test -trimpath -race $(TESTCOUNT) ./...

# The tests scripts/ keeps for its own tooling. Stdlib python and plain
# shell, so there is nothing to install and no Go build. _lint.yml runs this;
# it is out of `test` because that target is the slow -race Go suite.
test-scripts: ## Run the tests for scripts/ (python + shell)
	@set -e; for t in scripts/*_test.py; do echo "==> $$t"; python3 "$$t"; done
	@set -e; for t in scripts/*_test.sh; do echo "==> $$t"; "$$t"; done

test-e2e: ## Run the e2e suite (requires TEST_POSTGRES_DSN reachable)
	go test -trimpath -race $(TESTCOUNT) -tags e2e ./e2e/

vet: ## Run go vet
	go vet ./...

openapi: ## Write the generated OpenAPI 3.1 document to bin/openapi.json
	@mkdir -p bin
	LODE_OPENAPI_OUT=$(CURDIR)/bin/openapi.json go test -trimpath -count=1 ./internal/api -run '^TestOpenAPIWrite$$'

clean: ## Remove build output
	rm -rf bin

web: ## Build the SPA into internal/webapp/dist (needs Node.js and pnpm)
	cd web/app && pnpm install --frozen-lockfile && pnpm build

web-gen: openapi ## Regenerate the SPA's typed API client from bin/openapi.json
	cd web/app && pnpm gen:api

web-clean: ## Put the committed placeholder back after a local SPA build
	git clean -fdx internal/webapp/dist
	git checkout -- internal/webapp/dist/index.html

# Host-side development: Postgres in compose, lode-server and Vite on the host.
# Exported so docker compose publishes Postgres on the same port.
export LODE_POSTGRES_PORT ?= 5432
DEV_DSN ?= postgres://postgres:postgres@localhost:$(LODE_POSTGRES_PORT)/postgres?sslmode=disable

dev-db: ## Start compose's Postgres and migrate it from the host
	docker compose up -d --wait postgres
	go run -trimpath ./cmd/lode-migrate --dsn '$(DEV_DSN)' --migrations-path deploy/base/migrations

export LODE_EMBEDDINGS_PORT ?= 8081
EMBEDDINGS_URL := http://localhost:$(LODE_EMBEDDINGS_PORT)

dev-embeddings: ## Start compose's embedding server (amd64 image; emulated on Apple Silicon)
	docker compose --profile embeddings up -d embeddings

# Embedding settings mirror deploy/base/configmap.yaml, used only when
# dev-embeddings answers.
dev-server: bin/lode-server ## Run lode-server on the host against dev-db (stop compose's worklode first)
	@if curl -sf -o /dev/null $(EMBEDDINGS_URL)/health; then \
		echo "using embeddings at $(EMBEDDINGS_URL)"; \
		export LODE_EMBEDDING_URL=$(EMBEDDINGS_URL)/v1/embeddings LODE_EMBEDDING_MODEL=/model \
			LODE_EMBEDDING_CONTEXT_TOKENS=2048 \
			LODE_EMBEDDING_QUERY_PREFIX='task: search result | query: ' \
			LODE_EMBEDDING_DOCUMENT_PREFIX='title: none | text: '; \
	fi; \
	LODE_DSN='$(DEV_DSN)' LODE_WEB_OPEN=$${LODE_WEB_OPEN:-1} LODE_INSTANCE_ENV=$${LODE_INSTANCE_ENV:-dev} exec ./bin/lode-server

dev-web: ## Run the SPA's Vite dev server against dev-server (http://localhost:5173)
	cd web/app && pnpm install --frozen-lockfile && VITE_API_MOCKS=off pnpm dev

.DEFAULT_GOAL := build

graph-serve: build ## Export the task graph and serve it over SPARQL (HornDB on :3840/:3841)
	scripts/graph/serve.sh

graph-query: ## Run one query: make graph-query Q=gates [PORT=3840]
	scripts/graph/sparql.py $(or $(PORT),3840) scripts/graph/queries/$(Q).rq
