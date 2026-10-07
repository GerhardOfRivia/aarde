OUTPUT ?= bin
SEMVER ?= 1.5.2
VERSION ?= $(SEMVER)-dev
RELEASE_TAG ?= v$(SEMVER)
LDFLAGS = -ldflags "-X main.version=$(VERSION)"
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

.PHONY: all activate web build integration fmt vet test race release clean

all: build

activate:
	source activate

web:
	# npm ci && npm run build
	docker run --rm --user $$(id -u):$$(id -g) --mount type=bind,src=$(CURDIR),dst=/workspace -w /workspace/web node:22-bookworm-slim /bin/sh -lc 'npm ci && npm run build'

build:
	go build -trimpath $(LDFLAGS) -o $(OUTPUT)/aarde ./cmd/aarde

integration:
	@trap 'docker compose -p aarde-test -f docker-compose.test.yml down' EXIT; \
	docker compose -p aarde-test -f docker-compose.test.yml up --build --abort-on-container-exit --exit-code-from tests

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

check: fmt vet test race

release:
	@test -z "$$(git status --porcelain)" || { echo "Refusing to release with a dirty working tree."; exit 1; }
	@if git rev-parse --verify --quiet "refs/tags/$(RELEASE_TAG)" >/dev/null; then echo "Tag $(RELEASE_TAG) already exists."; exit 1; fi
	git tag "$(RELEASE_TAG)"
	git push origin "$(RELEASE_TAG)"

clean:
	rm -f ./bin/aarde
	rm -rf ./web/node_modules ./web/tsconfig.app.tsbuildinfo ./web/tsconfig.node.tsbuildinfo

