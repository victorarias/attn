.PHONY: build install test test-v test-quick test-watch test-all test-frontend test-e2e test-harness clean generate-types check-types build-app install-app install-all dist

BINARY_NAME=attn
INSTALL_DIR=$(HOME)/.local/bin
BUILD_DIR=./cmd/attn

build:
	go build -o $(BINARY_NAME) $(BUILD_DIR)

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
	@if command -v fuser >/dev/null 2>&1; then \
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
	@# Use cat to avoid copying extended attributes that trigger Gatekeeper
	cat $(BINARY_NAME) > $(INSTALL_DIR)/$(BINARY_NAME)
	chmod +x $(INSTALL_DIR)/$(BINARY_NAME)
	@# macOS: remove quarantine attribute and ad-hoc sign binary
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		xattr -d com.apple.quarantine $(INSTALL_DIR)/$(BINARY_NAME) 2>/dev/null || true; \
		codesign -s - -f $(INSTALL_DIR)/$(BINARY_NAME); \
	else \
		echo "Skipping macOS quarantine removal and codesign on $(UNAME_S)"; \
	fi
	@# Kill running daemon and restart with new code
	-pkill -f "$(BINARY_NAME) daemon" 2>/dev/null || true
	@sleep 0.2
	@nohup $(INSTALL_DIR)/$(BINARY_NAME) daemon >/dev/null 2>&1 &
	@echo "Installed $(BINARY_NAME) to $(INSTALL_DIR) (daemon restarted)"

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
	cd app && pnpm tauri build --bundles app

# Install Tauri app to /Applications
install-app: build-app
	@rm -rf /Applications/attn.app
	cp -r app/src-tauri/target/release/bundle/macos/attn.app /Applications/
	@echo "Installed attn.app to /Applications"

# Install daemon and app
install-all: install install-app

# Create distributable DMG
dist: build-app
	cd app && pnpm tauri build --bundles dmg
	@echo "DMG created at app/src-tauri/target/release/bundle/dmg/"
