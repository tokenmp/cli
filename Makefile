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

.PHONY: build install test lint fmt tidy clean

## build: compile the CLI into bin/ with version metadata stamped in
build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

## install: install the CLI into $GOBIN (or $GOPATH/bin)
install:
	go install -trimpath -ldflags '$(LDFLAGS)' .

## test: run the test suite
test:
	go test ./...

## lint: run go vet and golangci-lint when available
lint:
	go vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run || echo "golangci-lint not installed, skipped"

## fmt: format the source
fmt:
	gofmt -s -w .

## tidy: sync go.mod and go.sum
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf bin
