GO ?= go
XDG_CACHE_HOME ?= $(if $(HOME),$(HOME)/.cache,$(CURDIR)/.cache)
GOCACHE ?= $(XDG_CACHE_HOME)/abx/go-build
GOMODCACHE ?= $(XDG_CACHE_HOME)/abx/gomod
VERSION_FILE ?= VERSION
VERSION ?= $(shell cat $(VERSION_FILE) 2>/dev/null || echo dev)
BUILD_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X main.version=$(VERSION) -X main.buildCommit=$(BUILD_COMMIT) -X main.buildDate=$(BUILD_DATE)

.PHONY: build test fmt release-artifacts

build:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) build -ldflags "$(LDFLAGS)" -o abx ./cmd/abx

test:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test ./...

fmt:
	$(GO) fmt ./...

release-artifacts:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) build -ldflags "$(LDFLAGS)" -o dist/abx_$(VERSION)_darwin_arm64 ./cmd/abx
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) build -ldflags "$(LDFLAGS)" -o dist/abx_$(VERSION)_darwin_amd64 ./cmd/abx
	shasum -a 256 dist/abx_$(VERSION)_darwin_arm64 dist/abx_$(VERSION)_darwin_amd64 > dist/abx_$(VERSION)_SHA256SUMS
