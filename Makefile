GO ?= go
FRONTEND_DIR := cmd/dockstar-ui/frontend
WAILS_BASE_TAGS := production,desktop
WAILS_WEBKIT_TAG := $(shell pkg-config --exists webkit2gtk-4.1 2>/dev/null && printf ',webkit2_41')
WAILS_TAGS := $(WAILS_BASE_TAGS)$(WAILS_WEBKIT_TAG)
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X github.com/redundercover/dockstar/internal/version.Version=$(VERSION) -X github.com/redundercover/dockstar/internal/version.Commit=$(COMMIT) -X github.com/redundercover/dockstar/internal/version.Date=$(DATE)

.PHONY: all build build-ui-frontend generate test race lint clean install-user uninstall-user package release

all: build

build-ui-frontend:
	npm --prefix $(FRONTEND_DIR) ci
	npm --prefix $(FRONTEND_DIR) run build

build: build-ui-frontend
	mkdir -p build
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o build/dockstar ./cmd/dockstar
	$(GO) build -trimpath -tags '$(WAILS_TAGS)' -ldflags '$(LDFLAGS)' -o build/dockstar-ui ./cmd/dockstar-ui
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o build/dockstar-wake-helper ./cmd/dockstar-wake-helper
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o build/dockstar-startup-helper ./cmd/dockstar-startup-helper

generate:
	$(GO) generate ./internal/protocol

test: build-ui-frontend
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
