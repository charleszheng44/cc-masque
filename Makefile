GO ?= go
BINARY := cc-crew
PKGS := ./...

IMAGE_REPO := ghcr.io/charleszheng44
IMAGE_TAG  ?= latest

.PHONY: all build test fmt vet lint cover clean docker-build docker-build-sandbox docker-publish docker-publish-sandbox

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

docker-build:
	docker build -t $(IMAGE_REPO)/cc-crew:$(IMAGE_TAG) .

docker-build-sandbox:
	docker build -f Dockerfile.ubuntu -t $(IMAGE_REPO)/cc-crew-sandbox:$(IMAGE_TAG) .

docker-publish: docker-build
	docker push $(IMAGE_REPO)/cc-crew:$(IMAGE_TAG)

docker-publish-sandbox: docker-build-sandbox
	docker push $(IMAGE_REPO)/cc-crew-sandbox:$(IMAGE_TAG)

clean:
	rm -f $(BINARY) cover.out
