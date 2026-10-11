# Regix development tasks. Only Docker is needed for `make image` and `make demo`;
# the other targets use a local Go and Node.js.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE   ?= regix:$(VERSION)

.PHONY: ui build test check image demo demo-down

ui: ## Build the user interface into internal/web/dist
	cd ui && npm ci && npm run build

build: ui ## Build the regix binary
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o regix ./cmd/regix

test: ## Run the Go tests
	go test ./...

check: test ## Everything CI runs
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	cd ui && npm ci && npm run typecheck

image: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

demo: ## Start a registry with sample images and Regix on http://localhost:8080
	docker compose -f examples/demo/docker-compose.yml up --build

demo-down:
	docker compose -f examples/demo/docker-compose.yml down --volumes
