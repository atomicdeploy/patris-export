.PHONY: build build-linux build-windows build-all build-lib build-lib-linux build-lib-windows clean test run install install-linux uninstall-linux help deps build-web assets

# Binary names
BINARY_NAME=patris-export
BUILD_DIR=build

# Version information. pkg/version is the canonical source so patch releases do
# not leave the Makefile build path reporting an older version.
SOURCE_VERSION=$(shell sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' pkg/version/version.go | head -n 1)
VERSION?=$(SOURCE_VERSION)
BUILD_DATE?=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
COMMIT?=$(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
VERSION_PKG=github.com/atomicdeploy/patris-export/pkg/version
LDFLAGS=-ldflags "-X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).BuildDate=$(BUILD_DATE) -X $(VERSION_PKG).Commit=$(COMMIT)"

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

assets: ## Rebuild generated assets (Windows icon, validate notification audio)
	@./scripts/rebuild-assets.sh

build-web: ## Build the web frontend
	@echo "🌐 Building web frontend..."
	@cd web && npm ci --silent && npm run build
	@echo "✅ Web frontend built"

build: build-web ## Build for current platform
	@echo "🔨 Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/patris-export
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

build-lib: build-web ## Build loadable library for the current platform
	@echo "Building patris-export loadable library..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -buildmode=c-shared $(LDFLAGS) -o $(BUILD_DIR)/patris-export-lib ./cmd/patris-export-lib
	@echo "Build complete: $(BUILD_DIR)/patris-export-lib"

build-linux: build-web ## Build for Linux
	@echo "🐧 Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/patris-export
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64"

build-lib-linux: build-web ## Build Linux loadable library (.so)
	@echo "Building Linux loadable library..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -buildmode=c-shared $(LDFLAGS) -o $(BUILD_DIR)/libpatris-export.so ./cmd/patris-export-lib
	@echo "Build complete: $(BUILD_DIR)/libpatris-export.so"

build-windows: assets build-web ## Build for Windows with CGO (builds pxlib from source)
	@echo "🪟 Building for Windows with full Paradox support..."
	@mkdir -p $(BUILD_DIR)
	@if command -v x86_64-w64-mingw32-windres >/dev/null 2>&1; then \
		echo "📝 Generating Windows resource file..."; \
		./scripts/generate-version-rc.sh cmd/patris-export/patris-export.rc || \
			{ echo "❌ Resource generation failed"; exit 1; }; \
		echo "📝 Compiling Windows resource file..."; \
		x86_64-w64-mingw32-windres -i cmd/patris-export/patris-export.rc \
			-o cmd/patris-export/patris-export_windows_amd64.syso -O coff --target=pe-x86-64 || \
			{ echo "❌ Resource compilation failed"; exit 1; }; \
		echo "✅ Resource file generated and compiled"; \
	else \
		echo "⚠️  windres not found, skipping resource compilation"; \
	fi
	@echo "📦 Building pxlib for Windows..."
	@PXLIB_MINGW_CROSS=1 ./scripts/build-pxlib-mingw.sh /tmp/patris-pxlib-windows
	@echo "🔨 Building Windows executable..."
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
		CGO_LDFLAGS="-L/tmp/patris-pxlib-windows/lib" \
		CGO_CFLAGS="-I/tmp/patris-pxlib-windows/include" \
		go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/patris-export
	@if find /tmp/patris-pxlib-windows/bin -name "*.dll" -type f 2>/dev/null | grep -q .; then \
		find /tmp/patris-pxlib-windows/bin -name "*.dll" -type f -exec cp {} $(BUILD_DIR)/ \; ; \
	fi
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe"

build-lib-windows: assets build-web ## Build Windows loadable library (.dll)
	@echo "Building Windows loadable library..."
	@mkdir -p $(BUILD_DIR)
	@if [ -n "$$PXLIB_ROOT" ]; then \
		export CGO_CFLAGS="-I$$PXLIB_ROOT/include $${CGO_CFLAGS}"; \
		export CGO_LDFLAGS="-L$$PXLIB_ROOT/lib $${CGO_LDFLAGS}"; \
	fi; \
	if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then \
		CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc go build -buildmode=c-shared $(LDFLAGS) -o $(BUILD_DIR)/patris-export.dll ./cmd/patris-export-lib; \
	else \
		CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -buildmode=c-shared $(LDFLAGS) -o $(BUILD_DIR)/patris-export.dll ./cmd/patris-export-lib; \
	fi
	@echo "Build complete: $(BUILD_DIR)/patris-export.dll"

build-all: build-linux build-windows ## Build for all platforms

install: build-web ## Install the binary to GOPATH/bin
	@echo "📦 Installing $(BINARY_NAME)..."
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/patris-export
	@echo "✅ Installed to $(shell go env GOPATH)/bin/$(BINARY_NAME)"

install-linux: ## Install Linux binary and systemd service (requires root)
	@sudo ./scripts/install-linux.sh install

uninstall-linux: ## Remove Linux binary and systemd service (requires root)
	@sudo ./scripts/install-linux.sh uninstall

test: ## Run tests
	@echo "🧪 Running tests..."
	go test -v ./...

clean: ## Clean build artifacts
	@echo "🧹 Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f cmd/patris-export/*.syso
	@rm -f cmd/patris-export/*.rc
	@rm -rf web/dist
	@echo "✅ Clean complete"

run: build ## Build and run the application
	@./$(BUILD_DIR)/$(BINARY_NAME)

deps: ## Download dependencies
	@echo "📥 Downloading dependencies..."
	go mod download
	go mod tidy
	@cd web && npm ci
	@echo "✅ Dependencies ready"
