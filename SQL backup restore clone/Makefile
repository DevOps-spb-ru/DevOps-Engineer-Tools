# Makefile для SQL backup, restore and clone (sqlbrc).
# В Windows без установленного make используйте команды go напрямую (см. README.md).

BINARY_NAME := sqlbrc
ifeq ($(OS),Windows_NT)
BINARY := bin/$(BINARY_NAME).exe
else
BINARY := bin/$(BINARY_NAME)
endif

GO ?= go
GOLANGCI_LINT ?= golangci-lint

# Версия подставляется в бинарь через ldflags: sqlbrc --version покажет её вместе с коммитом и датой.
# Переопределить можно так: make build VERSION=0.1.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: all build version test vet fmt fmt-check lint tidy clean

all: fmt vet test build

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/sqlbrc

version: build
	$(BINARY) --version

test:
	$(GO) test ./... -count=1

vet:
	$(GO) vet ./...

fmt:
	gofmt -l -w cmd internal

fmt-check:
	gofmt -l cmd internal

lint:
	$(GOLANGCI_LINT) run ./...

tidy:
	$(GO) mod tidy

clean:
	$(GO) clean
	rm -rf bin
