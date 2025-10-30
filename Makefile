# --------------------------------------------------------------------
# 	Makefile
# --------------------------------------------------------------------

# --- Configuration ---
APP_NAME        := render-diff
IMAGE_NAME      := ghcr.io/dlactin/$(APP_NAME)
TAG             := latest
CONTAINER_NAME  := $(APP_NAME)

# Targets

# Default target
.PHONY: all
all: build

# Build render-diff
.PHONY: build
build:
	@echo "Building render-diff "
	go build -o render-diff

.PHONY: lint
lint:
	@echo "Running golangci-lint"
	golangci-lint run

# Clean up any build artifacts
.PHONY: clean
clean:
	@echo "Cleaning up..."
	docker rmi $(IMAGE_NAME):$(TAG) 2>/dev/null || true
	go clean
	@echo "Clean complete"

# Run go tests
.PHONY: test
test:
	go test ./... -v