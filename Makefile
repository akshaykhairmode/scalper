SHELL := /bin/bash

GO        ?= go
BIN       ?= bin/scalper
CONFIG    ?= config.yaml
COVER_MIN ?= 85

.PHONY: all help fmt vet lint build test race cover cover-html check run clean

all: fmt vet lint test build

help:
	@echo "make fmt        gofmt + goimports if available"
	@echo "make vet        go vet ./..."
	@echo "make lint       golangci-lint run (if installed)"
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

build:
	@mkdir -p "$(dir $(BIN))"
	$(GO) build -trimpath -o $(BIN) ./cmd/scalper

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
