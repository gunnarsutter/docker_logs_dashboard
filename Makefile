.PHONY: build build-config-builder build-config-validator validate-config run clean test install help

# Binary name
BINARY_NAME=docker-logs-dashboard
CONFIG_BUILDER_NAME=config-builder
CONFIG_VALIDATOR_NAME=config-validator
BUILD_DIR=build

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build the application
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) -v

build-config-builder: ## Build the config-builder tool
	@echo "Building $(CONFIG_BUILDER_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(CONFIG_BUILDER_NAME) -v ./cmd/config-builder/...

build-config-validator: ## Build the config-validator tool
	@echo "Building $(CONFIG_VALIDATOR_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(CONFIG_VALIDATOR_NAME) -v ./cmd/config-validator/...

validate-config: ## Validate a config file (use CONFIG=... and optional CONFIGS_DIR=...)
	@CONFIG_PATH=$${CONFIG:-config.yaml}; \
	CONFIGS_PATH=$${CONFIGS_DIR:-configs}; \
	QUIET_FLAG=$${QUIET:+-quiet}; \
	echo "Validating $$CONFIG_PATH..."; \
	$(GOCMD) run ./cmd/config-validator $$QUIET_FLAG -config "$$CONFIG_PATH" -configs-dir "$$CONFIGS_PATH"

run: build ## Build and run the application
	@echo "Running $(BINARY_NAME)..."
	@./$(BUILD_DIR)/$(BINARY_NAME)

run-example: build ## Build and run with example config
	@echo "Running $(BINARY_NAME) with example config..."
	@if [ ! -f config.yaml ]; then cp config.example.yaml config.yaml; fi
	@./$(BUILD_DIR)/$(BINARY_NAME) -config config.yaml

clean: ## Clean build files
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf $(BUILD_DIR)

test: ## Run tests
	@echo "Running tests..."
	$(GOTEST) -v ./...

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	$(GOTEST) -cover -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GOMOD) download

tidy: ## Tidy dependencies
	@echo "Tidying dependencies..."
	$(GOMOD) tidy

install: build ## Install the binary to $GOPATH/bin
	@echo "Installing $(BINARY_NAME)..."
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)
	@echo "Installed to $(GOPATH)/bin/$(BINARY_NAME)"

docker-check: ## Check Docker connection
	@echo "Checking Docker connection..."
	@docker ps > /dev/null 2>&1 && echo "✓ Docker is accessible" || echo "✗ Docker is not accessible"

all: clean deps build ## Clean, download deps, and build
