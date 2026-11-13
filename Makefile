SHELL := /bin/bash
export PATH := $(shell go env GOPATH)/bin:$(PATH)

GO        ?= go
BIN       ?= bin/scalper
CONFIG    ?= config.yaml
COVER_MIN ?= 55

VERSION   ?= $(shell V=$$(git describe --tags --abbrev=0 2>/dev/null || git tag -l "v*" --sort=-v:refname 2>/dev/null | head -n 1); echo "$${V:-v0.7.1}")
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X github.com/akshaykhairmode/scalper/internal/version.Version=$(VERSION) \
             -X github.com/akshaykhairmode/scalper/internal/version.Commit=$(COMMIT) \
             -X github.com/akshaykhairmode/scalper/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: all help fmt vet lint deadcode web-install web-build compress build test race cover cover-html check run serve deploy clean mobile-install mobile-bundle mobile-sync mobile-android mobile-ios

all: fmt vet lint test build

help:
	@echo "make fmt        gofmt + goimports if available"
	@echo "make vet        go vet ./..."
	@echo "make lint       gofmt check + golangci-lint run + deadcode check"
	@echo "make deadcode   check unreachable code with deadcode"
	@echo "make compress   pre-compress static assets with Brotli"
	@echo "make build      build $(BIN)"
	@echo "make test       go test ./..."
	@echo "make race       go test -race ./..."
	@echo "make cover      race tests + coverage gate ($(COVER_MIN)%)"
	@echo "make cover-html open the coverage report"
	@echo "make check      fmt check + vet + race + coverage"
	@echo "make run        run with -config $(CONFIG)"
	@echo "make serve      build and run in serve mode (0.0.0.0, no browser)"
	@echo "make deploy     cross-compile and deploy to remote server (SERVER_IP, SSH_KEY)"
	@echo "make mobile-bundle sync web assets for Capacitor mobile app"
	@echo "make mobile-sync   sync web assets and run Capacitor sync"

fmt:
	gofmt -w .
	@if command -v goimports >/dev/null 2>&1; then goimports -local github.com/akshaykhairmode/scalper -w cmd internal; fi

vet:
	$(GO) vet ./...

lint:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then \
		echo "gofmt required for:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
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

web-install:
	cd web && npm install

web-build:
	@if [ -d "web/node_modules" ]; then \
		node web/build.js; \
	fi

compress:
	$(GO) run ./scripts/compress_assets.go

build: web-build compress
	@mkdir -p "$(dir $(BIN))"
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/scalper

test:
	$(GO) test ./...

web-test:
	@if [ -d "web/node_modules" ]; then \
		npm --prefix web test; \
	else \
		echo "skip web tests: run npm --prefix web install first"; \
	fi

race:
	$(GO) test -race -count=1 ./...

cover:
	$(GO) test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	./scripts/check-coverage.sh coverage.out $(COVER_MIN)

cover-html: cover
	$(GO) tool cover -html=coverage.out

check:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "gofmt required for: $$unformatted"; exit 1; fi
	$(GO) vet ./...
	$(GO) test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	./scripts/check-coverage.sh coverage.out $(COVER_MIN)

run: build
	./$(BIN) -config $(CONFIG)

serve: run

deploy:
	./scripts/deploy.sh $(SERVER_IP) $(SSH_KEY) $(DEPLOY_USER)

clean:
	rm -rf bin coverage.out

mobile-install:
	cd mobile && npm install

mobile-bundle: web-build
	node mobile/scripts/sync-assets.js

mobile-sync: mobile-bundle
	cd mobile && npx cap sync

mobile-android: mobile-bundle
	cd mobile/android && ./gradlew assembleRelease || ./gradlew assembleDebug

mobile-ios: mobile-bundle
	cd mobile && npx cap sync ios

