# Makefile for Sprinter Go project

# Variables
BINARY_NAME=sprinter
BUILD_DIR=bin
MAIN_PATH=./cmd/server
GO_VERSION=1.21.6
GO_ARCH=linux-arm64
OPENAPI_VERSION=v1.0.35

# Go parameters - check if go is available, otherwise use full path
GOCMD=$(shell if command -v go > /dev/null; then echo go; else echo /usr/local/go/bin/go; fi)
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod

.PHONY: all build clean test deps generate run help publish-openapi download-api install-go install-tools setup

# Default target
all: clean build

# Install Go if not present
install-go:
	@echo "Checking Go installation..."
	@if ! command -v go > /dev/null; then \
		echo "Installing Go $(GO_VERSION)..."; \
		cd /tmp; \
		wget https://go.dev/dl/go$(GO_VERSION).$(GO_ARCH).tar.gz; \
		sudo tar -C /usr/local -xzf go$(GO_VERSION).$(GO_ARCH).tar.gz; \
		rm go$(GO_VERSION).$(GO_ARCH).tar.gz; \
		cd -; \
		if ! grep -q "/usr/local/go/bin" ~/.bashrc; then \
			echo 'export PATH=$$PATH:/usr/local/go/bin' >> ~/.bashrc; \
		fi; \
		echo "Go installed successfully"; \
		echo "Please run: source ~/.bashrc or restart your terminal"; \
	else \
		echo "Go is already installed"; \
	fi

# Install required toolsd
install-tools: install-go
	@echo "Installing required tools..."
	@if command -v go > /dev/null; then \
		go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest; \
		go install github.com/swaggo/swag/cmd/swag@latest; \
		echo "Tools installed successfully"; \
	else \
		echo "Go not found in PATH. Please run: source ~/.bashrc"; \
		echo "Then run: make install-tools"; \
		exit 1; \
	fi

# Complete setup including Go installation
setup: install-tools download-api deps generate create-config
	@echo "Setup completed successfully!"
	@echo "If you get 'go: command not found' errors, run: source ~/.bashrc"

# Create configuration directory and files
create-config:
	@echo "Creating configuration files..."
	@mkdir -p config
	@mkdir -p data
	@mkdir -p logs
	@if [ ! -f config/config.yaml ]; then \
		echo "Creating default config.yaml..."; \
		cp config/config.yaml.example config/config.yaml 2>/dev/null || echo 'host_registration:\n  sprinter_url: "http://localhost:8081"' > config/config.yaml; \
	fi

# Build the application (includes code generation)
build: generate
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	@if command -v go > /dev/null; then \
		$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH); \
	else \
		echo "Go not found. Please run: source ~/.bashrc"; \
		exit 1; \
	fi

# Clean build artifacts and generated files
clean:
	@echo "Cleaning..."
	@if command -v go > /dev/null; then \
		$(GOCLEAN); \
	else \
		echo "Go not found, skipping clean"; \
	fi
	@rm -rf $(BUILD_DIR)
	@rm -rf internal/generated
	@rm -rf internal/client
	@rm -f api/openapi.yaml

# Run tests
test:
	@echo "Running tests..."
	@if command -v go > /dev/null; then \
		$(GOTEST) -v ./...; \
	else \
		echo "Go not found. Please run: source ~/.bashrc"; \
		exit 1; \
	fi

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@if command -v go > /dev/null; then \
		$(GOMOD) tidy; \
		$(GOMOD) download; \
		go get github.com/oapi-codegen/runtime; \
	else \
		echo "Go not found. Please run: source ~/.bashrc"; \
		exit 1; \
	fi

# Download OpenAPI specification from GitHub
download-api:
	@echo "Downloading OpenAPI specification from GitHub (version: $(OPENAPI_VERSION))..."
	@mkdir -p api
	@curl -L -o api/openapi.yaml https://github.com/miku-kookie/somana/releases/download/$(OPENAPI_VERSION)/openapi.yaml
	@echo "OpenAPI specification downloaded to api/openapi.yaml"

# Generate code from OpenAPI spec
generate: download-api
	@echo "Generating code from OpenAPI spec..."
	@mkdir -p internal/generated
	@mkdir -p internal/client
	@export PATH=$$PATH:/usr/local/go/bin:$$HOME/go/bin; \
	if command -v oapi-codegen > /dev/null || [ -f $$HOME/go/bin/oapi-codegen ]; then \
		OAPI_CODEGEN=$${OAPI_CODEGEN:-$$(command -v oapi-codegen || echo $$HOME/go/bin/oapi-codegen)}; \
		$$OAPI_CODEGEN -package generated -generate types api/openapi.yaml > internal/generated/types.go; \
		$$OAPI_CODEGEN -package generated -generate gin-server api/openapi.yaml > internal/generated/server.go; \
		$$OAPI_CODEGEN -package client -generate types,client api/openapi.yaml > internal/client/client.go; \
		echo "Code generation complete"; \
	else \
		echo "oapi-codegen not found. Installing..."; \
		go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest; \
		$$HOME/go/bin/oapi-codegen -package generated -generate types api/openapi.yaml > internal/generated/types.go; \
		$$HOME/go/bin/oapi-codegen -package generated -generate gin-server api/openapi.yaml > internal/generated/server.go; \
		$$HOME/go/bin/oapi-codegen -package client -generate types,client api/openapi.yaml > internal/client/client.go; \
		echo "Code generation complete"; \
	fi

# Generate Swagger documentation
generate-docs:
	@echo "Generating Swagger documentation..."
	@if command -v go > /dev/null; then \
		if command -v swag > /dev/null; then \
			swag init -g $(MAIN_PATH)/main.go -o ./docs; \
		else \
			echo "swag not found. Installing..."; \
			go install github.com/swaggo/swag/cmd/swag@latest; \
			swag init -g $(MAIN_PATH)/main.go -o ./docs; \
		fi; \
	else \
		echo "Go not found. Please run: source ~/.bashrc"; \
		exit 1; \
	fi

# Publish OpenAPI specification to GitHub releases
publish-openapi:
	@echo "Publishing OpenAPI specification..."
	@./scripts/publish-openapi.sh $(VERSION)

# Run the application
run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BUILD_DIR)/$(BINARY_NAME)

# Show help
help:
	@echo "Available targets:"
	@echo "  setup         - Complete setup (install Go, tools, deps, generate code, create config)"
	@echo "  install-go    - Install Go if not present"
	@echo "  install-tools - Install Go and required tools"
	@echo "  create-config - Create configuration files and directories"
	@echo "  build         - Generate code and build the application"
	@echo "  clean         - Clean build artifacts and generated files"
	@echo "  test          - Run tests"
	@echo "  deps          - Install dependencies"
	@echo "  download-api  - Download OpenAPI specification from GitHub"
	@echo "  generate      - Generate code from OpenAPI spec"
	@echo "  generate-docs - Generate Swagger documentation"
	@echo "  publish-openapi - Publish OpenAPI spec to GitHub releases"
	@echo "  run           - Generate, build and run the application"
	@echo "  help          - Show this help" 