.PHONY: build build-user build-all install test test-scripts test-e2e vet openapi clean graph-serve graph-query web web-gen web-clean up dev-up dev-down dev-db dev-embeddings dev-server dev-server-watch dev-web seed FORCE

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

# When .env holds op:// references, run compose and lode-server under op run so
# 1Password resolves them. Compose interpolates the resolved values, which take
# precedence over its own read of .env.
ENV_RUN := $(if $(shell grep -qs '^[^\#]*op://' .env && echo y),op run --env-file=.env --)

up: ## Start the compose stack, resolving .env's op:// references
	$(ENV_RUN) docker compose up -d

# Host-side development: Postgres in compose, lode-server and Vite on the host.
# Exported so docker compose publishes Postgres on the same port. Make does not
# read .env, so env_or_default takes the value from there before the default.
env_or_default = $(or $(shell sed -n 's/^$(1)=//p' .env 2>/dev/null | tail -1),$(2))
export LODE_POSTGRES_PORT ?= $(call env_or_default,LODE_POSTGRES_PORT,5432)
DEV_DSN ?= postgres://postgres:postgres@localhost:$(LODE_POSTGRES_PORT)/postgres?sslmode=disable

dev-db: ## Start compose's Postgres and migrate it from the host
	$(ENV_RUN) docker compose up -d --wait postgres
	go run -trimpath ./cmd/lode-migrate --dsn '$(DEV_DSN)' --migrations-path deploy/base/migrations

seed: ## Replace compose's database with a pg_dump -Fc file, then migrate: make seed DUMP=<file>
	@test -f "$(DUMP)" || { echo "usage: make seed DUMP=<pg_dump -Fc file>"; exit 1; }
	$(ENV_RUN) docker compose up -d --wait postgres
	docker compose exec -T -e PGOPTIONS='-c client_min_messages=warning' postgres psql -q -U postgres -v ON_ERROR_STOP=1 \
		-c 'DROP SCHEMA public CASCADE' -c 'CREATE SCHEMA public'
	@# Docker's 64 MB /dev/shm cannot hold a parallel HNSW index build.
	docker compose exec -T -e PGOPTIONS='-c max_parallel_maintenance_workers=0' postgres pg_restore -U postgres -d postgres --no-owner --no-acl --exit-on-error < '$(DUMP)'
	go run -trimpath ./cmd/lode-migrate --dsn '$(DEV_DSN)' --migrations-path deploy/base/migrations

export LODE_EMBEDDINGS_PORT ?= $(call env_or_default,LODE_EMBEDDINGS_PORT,8081)
EMBEDDINGS_URL := http://localhost:$(LODE_EMBEDDINGS_PORT)

dev-embeddings: ## Start compose's embedding server (amd64 image; emulated on Apple Silicon)
	$(ENV_RUN) docker compose --profile embeddings up -d embeddings

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
	LODE_DSN='$(DEV_DSN)' LODE_WEB_OPEN=$${LODE_WEB_OPEN:-1} LODE_INSTANCE_ENV=$${LODE_INSTANCE_ENV:-dev} exec $(ENV_RUN) ./bin/lode-server

dev-server-watch: ## Run dev-server, rebuilding and restarting it on source changes (.air.toml)
	go tool air

dev-web: ## Run the SPA's Vite dev server against dev-server (http://localhost:5173)
	cd web/app && pnpm install --frozen-lockfile && VITE_API_MOCKS=off pnpm dev

# EMBEDDINGS=1 also starts dev-embeddings and waits up to 3 minutes for it to
# answer, since dev-server only checks for it once, at startup.
dev-up: dev-db $(if $(EMBEDDINGS),dev-embeddings) ## Run dev-db, then dev-server-watch and dev-web together (EMBEDDINGS=1 adds dev-embeddings)
	@$(if $(EMBEDDINGS),for i in $$(seq 90); do curl -sf -o /dev/null $(EMBEDDINGS_URL)/health && break; sleep 2; done)
	$(MAKE) -j2 dev-server-watch dev-web

# No -v: the Postgres volume, and with it the dev database, survives.
dev-down: ## Stop and remove the compose containers, keeping the database volume
	docker compose --profile embeddings down

.DEFAULT_GOAL := build

graph-serve: build ## Export the task graph and serve it over SPARQL (HornDB on :3840/:3841)
	scripts/graph/serve.sh

graph-query: ## Run one query: make graph-query Q=gates [PORT=3840]
	scripts/graph/sparql.py $(or $(PORT),3840) scripts/graph/queries/$(Q).rq
