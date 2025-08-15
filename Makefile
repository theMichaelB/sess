.PHONY: build install clean test

BINARY_NAME=sess
GO=go
# Semantic version, can be overridden: make VERSION=v1.2.3 build
VERSION?=v1.0.0
# Inject version into main.version and strip symbols
GOFLAGS=-ldflags="-s -w -X main.version=$(VERSION)"
PREFIX=/usr/local

build:
	$(GO) build $(GOFLAGS) -o $(BINARY_NAME) ./cmd/main.go

install:
	install -m 755 $(BINARY_NAME) $(PREFIX)/bin/$(BINARY_NAME)

uninstall:
	rm -f $(PREFIX)/bin/$(BINARY_NAME)

clean:
	rm -f $(BINARY_NAME)
	$(GO) clean

test:
	$(GO) test -v ./...

usability-test: build
	./test_usability.sh

deps:
	$(GO) mod download
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

.DEFAULT_GOAL := build
