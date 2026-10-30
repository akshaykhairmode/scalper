SHELL := /bin/bash
export PATH := $(shell go env GOPATH)/bin:$(PATH)

GO        ?= go
BIN       ?= bin/scalper
CONFIG    ?= config.yaml
COVER_MIN ?= 55

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X github.com/akshaykhairmode/scalper/internal/version.Version=$(VERSION) \
             -X github.com/akshaykhairmode/scalper/internal/version.Commit=$(COMMIT) \
             -X github.com/akshaykhairmode/scalper/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all help fmt vet lint deadcode compress build test race cover cover-html check run clean

all: fmt vet lint test build

help:
	@echo "make fmt        gofmt + goimports if available"
	@echo "make vet        go vet ./..."
	@echo "make lint       golangci-lint run + deadcode check"
	@echo "make deadcode   check unreachable code with deadcode"
	@echo "make compress   pre-compress static assets with Brotli"
	@echo "make build      build $(BIN)"
	@echo "make test       go test ./..."
	@echo "make race       go test -race ./..."
	@echo "make cover      race tests + coverage gate ($(COVER_MIN)%)"
	@echo "make cover-html open the coverage report"
	@echo "make check      fmt check + vet + race + coverage"
	@echo "make run        run with -config $(CONFIG)"

fmt:
	gofmt -w cmd internal
	@if command -v goimports >/dev/null 2>&1; then goimports -local github.com/akshaykhairmode/scalper -w cmd internal; fi

vet:
	$(GO) vet ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "golangci-lint not installed; skipping"; fi
	@$(MAKE) deadcode

deadcode:
	@if command -v deadcode >/dev/null 2>&1; then \
		output=$$(deadcode ./cmd/scalper); \
		if [ -n "$$output" ]; then \
			echo "deadcode found:"; \
			echo "$$output"; \
			exit 1; \
		fi \
	else \
		echo "deadcode not installed; run: go install golang.org/x/tools/cmd/deadcode@latest"; \
	fi

compress:
	$(GO) run ./scripts/compress_assets.go

build: compress
	@mkdir -p "$(dir $(BIN))"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/scalper

test:
	$(GO) test ./...

race:
	$(GO) test -race -count=1 ./...

cover:
	$(GO) test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	./scripts/check-coverage.sh coverage.out $(COVER_MIN)

cover-html: cover
	$(GO) tool cover -html=coverage.out

check:
	@unformatted="$$(gofmt -l cmd internal)"; if [ -n "$$unformatted" ]; then echo "gofmt required for: $$unformatted"; exit 1; fi
	$(GO) vet ./...
	$(GO) test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	./scripts/check-coverage.sh coverage.out $(COVER_MIN)

run: build
	./$(BIN) -config $(CONFIG)

clean:
	rm -rf bin coverage.out
