.PHONY: build build-linux build-windows build-all clean test run install help deps

# Binary names
BINARY_NAME=patris-export
BUILD_DIR=build

# Version information
VERSION?=1.0.0
BUILD_DATE=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildDate=$(BUILD_DATE)"

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build for current platform
	@echo "🔨 Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/patris-export
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

build-linux: ## Build for Linux
	@echo "🐧 Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/patris-export
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64"

build-windows: ## Build for Windows (requires pxlib DLL - see docs/WINDOWS_BUILD.md)
	@echo "🪟 Building for Windows..."
	@echo "⚠️  Note: Requires pxlib built for Windows from https://github.com/steinm/pxlib"
	@echo "⚠️  See docs/WINDOWS_BUILD.md for setup instructions"
	@mkdir -p $(BUILD_DIR)
	@# Compile Windows resource file if windres is available
	@if command -v x86_64-w64-mingw32-windres >/dev/null 2>&1; then \
		echo "📝 Compiling Windows resource file..."; \
		x86_64-w64-mingw32-windres -i cmd/patris-export/patris-export.rc \
			-o cmd/patris-export/patris-export_windows_amd64.syso -O coff --target=pe-x86-64 || \
			{ echo "❌ Resource compilation failed"; exit 1; }; \
		echo "✅ Resource file compiled"; \
	else \
		echo "⚠️  windres not found, skipping resource compilation"; \
	fi
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/patris-export
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe"
	@echo "⚠️  Remember to include pxlib.dll with the executable"

build-all: build-linux build-windows ## Build for all platforms

install: ## Install the binary to GOPATH/bin
	@echo "📦 Installing $(BINARY_NAME)..."
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/patris-export
	@echo "✅ Installed to $(shell go env GOPATH)/bin/$(BINARY_NAME)"

test: ## Run tests
	@echo "🧪 Running tests..."
	go test -v ./...

clean: ## Clean build artifacts
	@echo "🧹 Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f cmd/patris-export/*.syso
	@echo "✅ Clean complete"

run: build ## Build and run the application
	@./$(BUILD_DIR)/$(BINARY_NAME)

deps: ## Download dependencies
	@echo "📥 Downloading dependencies..."
	go mod download
	go mod tidy
	@echo "✅ Dependencies ready"
