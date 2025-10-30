# --------------------------------------------------------------------
# 	Makefile
# --------------------------------------------------------------------

# Targets

# Default target
.PHONY: all
all: build

# Build render-diff
.PHONY: build
build:
	@echo "Building render-diff "
	go build -o render-diff

# Run golangci-lint
.PHONY: lint
lint:
	@echo "Running golangci-lint"
	golangci-lint run

# Run go vet
.PHONY: vet
vet:
	go vet

# Run go fmt
.PHONY: fmt
fmt:
	go fmt .

# Run go tests if they exist
.PHONY: test
test:
	go test ./... -v