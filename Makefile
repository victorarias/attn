.PHONY: build install install-bin stop-daemon test test-v test-quick test-watch test-all test-frontend test-e2e test-harness clean generate-types check-types build-app install-app install-all dist release release-skip-tests

BINARY_NAME=attn
INSTALL_DIR=$(HOME)/.local/bin
SOURCE_BIN_DIR=app/src-tauri/target/release
SOURCE_BIN_PATH=$(SOURCE_BIN_DIR)/attn
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

install-bin:
	@mkdir -p $(INSTALL_DIR)
	@mkdir -p $(SOURCE_BIN_DIR)
	go build -o $(SOURCE_BIN_PATH) $(BUILD_DIR)
	@# Source installs use a symlink named `attn` that points at a stable binary
	@# path. On this machine, copied binaries named exactly `attn` in install
	@# locations have been unstable, while the Tauri release output path is stable.
	ln -sfn $(abspath $(SOURCE_BIN_PATH)) $(INSTALL_DIR)/$(BINARY_NAME)

stop-daemon:
	-pkill -f "$(BINARY_NAME) daemon" 2>/dev/null || true
	@sleep 0.2

install: install-bin stop-daemon
	@# Kill running daemon and restart with new local code.
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
	@tmp_go=$$(mktemp); \
	tmp_ts=$$(mktemp); \
	cp internal/protocol/generated.go "$$tmp_go"; \
	cp app/src/types/generated.ts "$$tmp_ts"; \
	trap 'rm -f "$$tmp_go" "$$tmp_ts"' EXIT; \
	$(MAKE) generate-types >/dev/null; \
	cmp -s internal/protocol/generated.go "$$tmp_go" || { \
		echo "internal/protocol/generated.go is out of date with internal/protocol/schema/main.tsp"; \
		exit 1; \
	}; \
	cmp -s app/src/types/generated.ts "$$tmp_ts" || { \
		echo "app/src/types/generated.ts is out of date with internal/protocol/schema/main.tsp"; \
		exit 1; \
	}

# Build Tauri app with bundled daemon (app bundle only, no DMG dialog)
build-app: build
	@mkdir -p app/src-tauri/binaries
	cp $(BINARY_NAME) app/src-tauri/binaries/$(BINARY_NAME)-aarch64-apple-darwin
	cd app && VITE_INSTALL_CHANNEL=source pnpm tauri build --bundles app

# Install Tauri app to /Applications
install-app: build-app
	-pkill -f "/Applications/attn.app/Contents/MacOS/app" 2>/dev/null || true
	-pkill -f "/Applications/attn.app/Contents/MacOS/attn daemon" 2>/dev/null || true
	@sleep 0.5
	@rm -rf /Applications/attn.app
	ditto app/src-tauri/target/release/bundle/macos/attn.app /Applications/attn.app
	rm -f /Applications/attn.app/Contents/MacOS/attn
	ln -sfn $(abspath $(SOURCE_BIN_PATH)) /Applications/attn.app/Contents/MacOS/attn
	@echo "Installed attn.app to /Applications"

# Install daemon and app
install-all: install-bin stop-daemon install-app

# Create distributable DMG
dist: build-app
	cd app && VITE_INSTALL_CHANNEL=source pnpm tauri build --bundles dmg
	@echo "DMG created at app/src-tauri/target/release/bundle/dmg/"

release:
	./scripts/release.sh $(VERSION_TAG)

release-skip-tests:
	./scripts/release.sh $(VERSION_TAG) --skip-tests
