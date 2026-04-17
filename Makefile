GO ?= go
BINARY := cc-crew
PKGS := ./...

.PHONY: all build test fmt vet lint cover clean

all: fmt vet test build

build:
	$(GO) build -o $(BINARY) ./cmd/cc-crew

test:
	$(GO) test $(PKGS)

fmt:
	gofmt -w .

vet:
	$(GO) vet $(PKGS)

cover:
	$(GO) test -coverprofile=cover.out $(PKGS)
	$(GO) tool cover -func=cover.out | tail -n 1

clean:
	rm -f $(BINARY) cover.out
