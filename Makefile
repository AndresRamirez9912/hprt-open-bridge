# photo-print-bridge: common tasks. Run `make help` to list them.

BINARY  := bridge
PKG     := ./cmd/bridge
OUT     := bin
LDFLAGS := -s -w
IMAGE   := photo-print-bridge:latest

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

.PHONY: run
run: ## Run the bridge with config.yaml and the optional .env
	go run $(PKG) -config .

.PHONY: build
build: ## Build the binary for this machine into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT)/$(BINARY) $(PKG)

.PHONY: build-all
build-all: ## Cross compile for Linux amd64, arm64, armv7 and riscv64 (Raspberry Pi and RISC-V boards)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT)/$(BINARY)-linux-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT)/$(BINARY)-linux-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT)/$(BINARY)-linux-armv7 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -trimpath -ldflags="$(LDFLAGS)" -o $(OUT)/$(BINARY)-linux-riscv64 $(PKG)

.PHONY: test
test: ## Run the tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run the tests and open the coverage report
	go test -coverprofile=$(OUT)/coverage.out ./...
	go tool cover -html=$(OUT)/coverage.out

.PHONY: fmt
fmt: ## Format the code
	gofmt -w .

.PHONY: lint
lint: ## Check formatting and run go vet
	@test -z "$$(gofmt -l .)" || (echo "Not formatted:"; gofmt -l .; exit 1)
	go vet ./...

.PHONY: docker-build
docker-build: ## Build the Docker image
	docker build -t $(IMAGE) .

.PHONY: docker-up
docker-up: ## Start the bridge with docker compose
	docker compose up -d --build

.PHONY: docker-down
docker-down: ## Stop the bridge started with docker compose
	docker compose down

.PHONY: clean
clean: ## Remove the build output
	rm -rf $(OUT)
