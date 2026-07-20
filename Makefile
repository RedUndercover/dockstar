GO ?= go
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X github.com/redundercover/dockstar/internal/version.Version=$(VERSION) -X github.com/redundercover/dockstar/internal/version.Commit=$(COMMIT) -X github.com/redundercover/dockstar/internal/version.Date=$(DATE)

.PHONY: all build generate test race lint clean install-user uninstall-user package release

all: build

build:
	mkdir -p build
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o build/dockstar ./cmd/dockstar
	$(GO) build -trimpath -tags migrated_fynedo -ldflags '$(LDFLAGS)' -o build/dockstar-ui ./cmd/dockstar-ui
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o build/dockstar-wake-helper ./cmd/dockstar-wake-helper

generate:
	$(GO) generate ./internal/protocol

test:
	$(GO) test ./...

race:
	$(GO) test -race ./internal/...

lint:
	test -z "$$($(GO) fmt ./...)"
	$(GO) vet ./...
	staticcheck $$(go list ./... | grep -v '/internal/protocol$$')
	govulncheck ./...

clean:
	rm -rf build dist

install-user: build
	./packaging/scripts/install-user.sh

uninstall-user:
	./packaging/scripts/uninstall-user.sh

package:
	goreleaser release --snapshot --clean

release:
	goreleaser release --clean
