SHELL := /bin/bash
GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X mineagent/internal/version.Version=$(VERSION)

.PHONY: build test vet fmt plugin run clean

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/mineagent ./cmd/mineagent

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

plugin:
	cd paper-plugin && ./gradlew --no-daemon jar

run: build
	./bin/mineagent --config config.json

clean:
	rm -rf bin
	cd paper-plugin && ./gradlew --no-daemon clean
