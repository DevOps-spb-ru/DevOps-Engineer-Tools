# Makefile для Container image optimizer (cio).
# В Windows без установленного make используйте команды go напрямую (см. README.md).

BINARY_NAME := cio
ifeq ($(OS),Windows_NT)
BINARY := bin/$(BINARY_NAME).exe
else
BINARY := bin/$(BINARY_NAME)
endif

GO ?= go
GOLANGCI_LINT ?= golangci-lint
IMAGE ?= cio:local
SMOKE_IMAGE ?= postgres:15-alpine

.PHONY: all build test vet fmt fmt-check lint tidy docker smoke clean

all: fmt vet test build

build:
	$(GO) build -trimpath -o $(BINARY) ./cmd/cio

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

docker:
	docker build -t $(IMAGE) .

# Быстрая проверка на живом docker-демоне: только анализ слоёв, без Trivy.
smoke: build
	$(BINARY) analyze --no-trivy $(SMOKE_IMAGE)

clean:
	$(GO) clean
	rm -rf bin
