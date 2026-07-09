.PHONY: build build-linux build-windows build-all clean test run install help deps build-web

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

build-web: ## Build the web frontend
	@echo "🌐 Building web frontend..."
	@cd web && npm install --silent && npm run build
	@echo "✅ Web frontend built"

build: build-web ## Build for current platform
	@echo "🔨 Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/patris-export
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

build-linux: build-web ## Build for Linux
	@echo "🐧 Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/patris-export
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64"

build-windows: build-web ## Build for Windows with CGO (builds pxlib from source)
	@echo "🪟 Building for Windows with full Paradox support..."
	@mkdir -p $(BUILD_DIR)
	@echo "📦 Building pxlib for Windows..."
	@bash -c 'cd /tmp && \
		rm -rf pxlib && \
		git clone --quiet https://github.com/steinm/pxlib.git && \
		cd pxlib && \
		sed -i "s/#include <Windows\.h>/#include <windows.h>/g" src/paradox.c && \
		sed -i "s/#include <Winbase\.h>/#include <winbase.h>/g" src/paradox.c && \
		if grep -q "target_sources(pxlib PRIVATE.*pxlib.rc)" CMakeLists.txt; then \
			sed -i "/target_sources(pxlib PRIVATE.*pxlib.rc)/d" CMakeLists.txt; \
		fi && \
		printf "set(CMAKE_SYSTEM_NAME Windows)\nset(CMAKE_SYSTEM_PROCESSOR x86_64)\nset(CMAKE_C_COMPILER x86_64-w64-mingw32-gcc)\nset(CMAKE_CXX_COMPILER x86_64-w64-mingw32-g++)\nset(CMAKE_RC_COMPILER x86_64-w64-mingw32-windres)\nset(CMAKE_FIND_ROOT_PATH /usr/x86_64-w64-mingw32)\nset(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)\nset(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)\nset(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)\n" > mingw-toolchain.cmake && \
		mkdir build && cd build && \
		cmake .. -DCMAKE_TOOLCHAIN_FILE=../mingw-toolchain.cmake -DCMAKE_BUILD_TYPE=Release -DENABLE_GSF=OFF && \
		cmake --build . --config Release -- -j$$(nproc) && \
		sudo mkdir -p /usr/x86_64-w64-mingw32/include && \
		if [ -f include/paradox.h ]; then \
			sudo cp include/paradox.h /usr/x86_64-w64-mingw32/include/; \
		elif [ -f ../include/paradox.h.in ]; then \
			sed "s/@PX_HAVE_ICONV@/1/g; s/@PX_HAVE_RECODE@/0/g; s/@PX_HAVE_GSF@/0/g" ../include/paradox.h.in | sudo tee /usr/x86_64-w64-mingw32/include/paradox.h > /dev/null; \
		fi && \
		[ -f ../include/paradox-mp.h ] && sudo cp ../include/paradox-mp.h /usr/x86_64-w64-mingw32/include/ || true && \
		LIBFILE=$$(find . -name "libpxlib*.a" -o -name "pxlib*.a" | head -1) && \
		if [ -n "$$LIBFILE" ]; then \
			sudo cp "$$LIBFILE" /usr/x86_64-w64-mingw32/lib/libpx.a; \
		fi'
	@echo "🔨 Building Windows executable..."
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
		CGO_LDFLAGS="-L/usr/x86_64-w64-mingw32/lib" \
		CGO_CFLAGS="-I/usr/x86_64-w64-mingw32/include" \
		go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/patris-export
	@if find /tmp/pxlib/build -name "*.dll" -type f 2>/dev/null | grep -q .; then \
		find /tmp/pxlib/build -name "*.dll" -type f -exec cp {} $(BUILD_DIR)/ \; ; \
	fi
	@echo "✅ Build complete: $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe"

build-all: build-linux build-windows ## Build for all platforms

install: build-web ## Install the binary to GOPATH/bin
	@echo "📦 Installing $(BINARY_NAME)..."
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/patris-export
	@echo "✅ Installed to $(shell go env GOPATH)/bin/$(BINARY_NAME)"

test: ## Run tests
	@echo "🧪 Running tests..."
	go test -v ./...

clean: ## Clean build artifacts
	@echo "🧹 Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -rf web/dist
	@echo "✅ Clean complete"

run: build ## Build and run the application
	@./$(BUILD_DIR)/$(BINARY_NAME)

deps: ## Download dependencies
	@echo "📥 Downloading dependencies..."
	go mod download
	go mod tidy
	@cd web && npm install
	@echo "✅ Dependencies ready"
