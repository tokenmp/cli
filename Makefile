BINARY  := tokenmp
PACKAGE := github.com/tokenmp/cli
# A tagged build reports the tag (v0.1.0-3-gabc1234), an untagged one "dev".
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PACKAGE)/internal/version.Version=$(VERSION) \
	-X $(PACKAGE)/internal/version.Commit=$(COMMIT) \
	-X $(PACKAGE)/internal/version.Date=$(DATE)

GO            ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: build build-bin install test lint fmt fmt-check tidy clean check tools

## build: compile every package, no artifact left behind
build:
	$(GO) build ./...

## build-bin: compile the CLI into bin/ with version metadata stamped in
build-bin:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

## install: install the CLI into $GOBIN (or $GOPATH/bin)
install:
	$(GO) install -trimpath -ldflags '$(LDFLAGS)' .

## test: run the test suite
test:
	$(GO) test -race ./...

## lint: run go vet and golangci-lint
lint:
	$(GO) vet ./...
	$(GOLANGCI_LINT) run

## fmt: format the source
fmt:
	$(GOLANGCI_LINT) fmt

## fmt-check: report what fmt would change, fail if any
fmt-check:
	@out=$$($(GOLANGCI_LINT) fmt --diff); \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

## tidy: sync go.mod and go.sum
tidy:
	$(GO) mod tidy

## clean: remove build artifacts
clean:
	rm -rf bin

# 门禁：只依赖 Go 与 golangci-lint，不需要网络、上游服务或 node。
# CI 与容器里跑的是同一条，见 .github/workflows/ci.yml。
check: build test lint fmt-check

## tools: install the toolchain pinned in .mise.toml
tools:
	mise install
