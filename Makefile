.PHONY: build install test test-e2e vet clean

BIN := bin/lode
PREFIX := $(if $(shell test -w /usr/local/bin && echo yes),/usr/local,$(HOME))

build: ## Build the lode binary into bin/lode
	go build -trimpath -o $(BIN) ./cmd/lode

install: ## Build lode and install it to $(PREFIX)/bin
	go build -trimpath -o $(PREFIX)/bin/lode ./cmd/lode

test: ## Run the unit test suite
	go test -trimpath -race -count=1 ./...

test-e2e: ## Run the e2e suite (requires TEST_POSTGRES_DSN reachable)
	go test -trimpath -race -count=1 -tags e2e ./e2e/

vet: ## Run go vet
	go vet ./...

clean: ## Remove build output
	rm -rf bin

.DEFAULT_GOAL := build
