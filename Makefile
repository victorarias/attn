.PHONY: build build-linux-amd64 build-linux-arm64 install test test-v test-quick test-watch test-all test-frontend test-e2e test-harness clean generate-types check-types build-app build-app-ui-automation install-app install-app-ui-automation install-all install-all-ui-automation app-screenshot dist release release-skip-tests

BINARY_NAME=attn
INSTALL_DIR=$(HOME)/.local/bin
BUILD_DIR=./cmd/attn
VERSION ?= $(shell bash ./scripts/version.sh)
OUTPUT ?= $(BINARY_NAME)
GO_LDFLAGS = -X main.version=$(VERSION)
ZIG ?= zig

build:
	go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

build-linux-amd64:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC='$(ZIG) cc -target x86_64-linux-gnu' CXX='$(ZIG) c++ -target x86_64-linux-gnu' go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

build-linux-arm64:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC='$(ZIG) cc -target aarch64-linux-gnu' CXX='$(ZIG) c++ -target aarch64-linux-gnu' go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

GOTESTSUM=$(HOME)/go/bin/gotestsum

$(GOTESTSUM):
	go install gotest.tools/gotestsum@latest

test: $(GOTESTSUM)
	$(GOTESTSUM) --format testdox -- ./...

# Verbose test output (shows all test names as they run)
test-v: $(GOTESTSUM)
	$(GOTESTSUM) --format standard-verbose -- ./...

# Quick test (dots only, fastest)
test-quick: $(GOTESTSUM)
	$(GOTESTSUM) --format dots -- ./...

# Watch mode - re-runs tests on file changes
test-watch: $(GOTESTSUM)
	$(GOTESTSUM) --watch --format testdox -- ./...

test-frontend:
	cd app && pnpm run test

test-e2e:
	@# Ensure stale Vite test server is not running
	@if [ "$(UNAME_S)" = "Darwin" ] && command -v lsof >/dev/null 2>&1; then \
		lsof -ti tcp:1421 | xargs -r kill 2>/dev/null || true; \
	elif command -v fuser >/dev/null 2>&1; then \
		fuser -k 1421/tcp 2>/dev/null || true; \
	elif command -v lsof >/dev/null 2>&1; then \
		lsof -ti tcp:1421 | xargs -r kill 2>/dev/null || true; \
	fi
	-pkill -f "vite.*1421" 2>/dev/null || true
	cd app && pnpm run e2e

test-harness: test test-frontend test-e2e

test-all: test test-frontend

UNAME_S := $(shell uname -s)

install: build
	@mkdir -p $(INSTALL_DIR)
	cp $(OUTPUT) $(INSTALL_DIR)/$(BINARY_NAME)
	@if [ "$(UNAME_S)" = "Darwin" ]; then codesign -s - -f $(INSTALL_DIR)/$(BINARY_NAME); fi
	@# Kill running daemon and restart with new local code.
	-pkill -f "$(BINARY_NAME) daemon" 2>/dev/null || true
	@sleep 0.2
	@nohup $(INSTALL_DIR)/$(BINARY_NAME) daemon >/dev/null 2>&1 &
	@sleep 0.2
	@pid=$$(pgrep -f -x "$(INSTALL_DIR)/$(BINARY_NAME) daemon" | head -n 1); \
	if [ -n "$$pid" ]; then \
		echo "Installed $(BINARY_NAME) to $(INSTALL_DIR) (daemon restarted: $$pid)"; \
	else \
		echo "Installed $(BINARY_NAME) to $(INSTALL_DIR) (daemon restart failed)"; \
		exit 1; \
	fi

clean:
	rm -f $(BINARY_NAME)
	rm -f $(INSTALL_DIR)/$(BINARY_NAME)

# Type generation pipeline: TypeSpec -> JSON Schema -> go-jsonschema/quicktype
generate-types:
	cd internal/protocol/schema && pnpm exec tsp compile .
	$(HOME)/go/bin/go-jsonschema -p protocol --only-models --tags json --resolve-extension json \
		--capitalization ID --capitalization URL --capitalization SHA --capitalization PR --capitalization CI \
		-o internal/protocol/generated.go \
		internal/protocol/schema/tsp-output/json-schema/*.json
	npx quicktype \
		--src internal/protocol/schema/tsp-output/json-schema/*.json \
		--src-lang schema --lang typescript \
		-o app/src/types/generated.ts

# CI check: verify generated files are up-to-date
check-types: generate-types
	git diff --exit-code internal/protocol/generated.go app/src/types/generated.ts

# Build Tauri app with bundled daemon (app bundle only, no DMG dialog)
build-app: build
	@mkdir -p app/src-tauri/binaries
	cp $(BINARY_NAME) app/src-tauri/binaries/$(BINARY_NAME)-aarch64-apple-darwin
	cd app && VITE_INSTALL_CHANNEL=source pnpm tauri build --bundles app
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign -s - -f app/src-tauri/target/release/bundle/macos/attn.app/Contents/MacOS/attn; \
	fi

build-app-ui-automation: build
	@mkdir -p app/src-tauri/binaries
	cp $(BINARY_NAME) app/src-tauri/binaries/$(BINARY_NAME)-aarch64-apple-darwin
	cd app && ATTN_UI_AUTOMATION=1 VITE_UI_AUTOMATION=1 VITE_INSTALL_CHANNEL=source pnpm tauri build --bundles app
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign -s - -f app/src-tauri/target/release/bundle/macos/attn.app/Contents/MacOS/attn; \
	fi

# Install Tauri app to ~/Applications
install-app: build-app
	@mkdir -p ~/Applications
	@rm -rf ~/Applications/attn.app
	cp -r app/src-tauri/target/release/bundle/macos/attn.app ~/Applications/
	@echo "Installed attn.app to ~/Applications"

install-app-ui-automation: build-app-ui-automation
	@mkdir -p ~/Applications
	@rm -rf ~/Applications/attn.app
	cp -r app/src-tauri/target/release/bundle/macos/attn.app ~/Applications/
	@echo "Installed attn.app to ~/Applications with UI automation bridge enabled"

# Install daemon and app
install-all: install install-app-ui-automation

install-all-ui-automation: install install-app-ui-automation

app-screenshot:
	cd app && node scripts/real-app-harness/capture-app-screenshot.mjs $(if $(SCREENSHOT_PATH),--path $(SCREENSHOT_PATH),) $(APP_SCREENSHOT_FLAGS)

# Ad-hoc sign the installed app binary so macOS doesn't SIGKILL it
# when invoked as a CLI subprocess (e.g. Claude Code hooks)
sign-app:
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign -s - ~/Applications/attn.app/Contents/MacOS/attn; \
		echo "Ad-hoc signed ~/Applications/attn.app/Contents/MacOS/attn"; \
	else \
		echo "sign-app is only needed on macOS"; \
	fi

# Create distributable DMG
dist: build-app
	cd app && VITE_INSTALL_CHANNEL=source pnpm tauri build --bundles dmg
	@echo "DMG created at app/src-tauri/target/release/bundle/dmg/"

release:
	./scripts/release.sh $(VERSION_TAG)

release-skip-tests:
	./scripts/release.sh $(VERSION_TAG) --skip-tests
