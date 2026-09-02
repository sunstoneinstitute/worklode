.PHONY: build build-user build-all install test test-e2e vet clean graph-serve graph-query FORCE

# 053 §1: six executables from one module. The three user binaries are what
# Homebrew and Scoop install; the other three ship in the container images.
USER_BINARIES := lode lode-hook lode-statusline
ALL_BINARIES := $(USER_BINARIES) lode-server lode-watch lode-migrate

PREFIX := $(if $(shell test -w /usr/local/bin && echo yes),/usr/local,$(HOME))

# Focused build of any one executable: make bin/lode-hook, make bin/lode-server.
# FORCE keeps the decision to rebuild with the Go build cache, which knows the
# real inputs; make does not.
bin/%: FORCE
	go build -trimpath -o $@ ./cmd/$*

FORCE:

build: bin/lode ## Build the lode binary into bin/lode

build-user: $(addprefix bin/,$(USER_BINARIES)) ## Build the three end-user binaries

build-all: $(addprefix bin/,$(ALL_BINARIES)) ## Build all six executables

install: ## Build and install the three end-user binaries to $(PREFIX)/bin
	@for b in $(USER_BINARIES); do \
		go build -trimpath -o $(PREFIX)/bin/$$b ./cmd/$$b || exit 1; \
	done

test: ## Run the unit test suite
	go test -trimpath -race -count=1 ./...

test-e2e: ## Run the e2e suite (requires TEST_POSTGRES_DSN reachable)
	go test -trimpath -race -count=1 -tags e2e ./e2e/

vet: ## Run go vet
	go vet ./...

clean: ## Remove build output
	rm -rf bin

.DEFAULT_GOAL := build

graph-serve: build ## Export the task graph and serve it over SPARQL (HornDB on :3840/:3841)
	scripts/graph/serve.sh

graph-query: ## Run one query: make graph-query Q=gates [PORT=3840]
	scripts/graph/sparql.py $(or $(PORT),3840) scripts/graph/queries/$(Q).rq
