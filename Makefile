# Knowhere Nexus — developer and CI entry points. See README.md.
SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X github.com/ntsrihari/knowhere-nexus/internal/buildinfo.Version=$(VERSION) \
	-X github.com/ntsrihari/knowhere-nexus/internal/buildinfo.Commit=$(COMMIT)
DIST    := internal/webui/dist
IMAGE   ?= knowhere-nexus:local

STATICCHECK_VERSION := v0.8.2
GOVULNCHECK_VERSION := v1.9.0
# Build analysis tools with the project toolchain (go.mod), not the tool's
# own minimum Go version, so they understand the project's language version.
GO_TOOLCHAIN := $(shell sed -n 's/^toolchain //p' go.mod)

.PHONY: all frontend-deps frontend frontend-check build test test-race fuzz lint vuln check e2e image container-test

all: check build

frontend-deps:
	cd frontend && npm ci

## Build the SvelteKit app and copy it into the Go embed directory.
## Only files inside internal/webui/dist (except .gitkeep) are replaced.
frontend:
	cd frontend && npm run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R frontend/build/. internal/webui/dist/

frontend-check:
	cd frontend && npm run check && npm test

## Static binary with the frontend embedded (run `make frontend` first).
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/nexus ./cmd/nexus

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

## Short fuzzing pass over the contract parsers (ST-8).
fuzz:
	go test -run '^$$' -fuzz FuzzParseCatalog -fuzztime 20s ./internal/contract
	go test -run '^$$' -fuzz FuzzParseTheme -fuzztime 20s ./internal/contract

lint:
	@out="$$(gofmt -l cmd internal contracts)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	GOTOOLCHAIN=$(GO_TOOLCHAIN) go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

vuln:
	GOTOOLCHAIN=$(GO_TOOLCHAIN) go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	cd frontend && npm audit --audit-level=high

check: lint test-race frontend-check

## Browser check: no CSP violations or console errors (acceptance criterion 6).
e2e: frontend build
	./scripts/e2e.sh

image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

## Hardened run of the image (ST-12).
container-test:
	./scripts/container-test.sh $(IMAGE)
