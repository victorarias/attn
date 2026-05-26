.PHONY: run build build-linux-amd64 build-linux-arm64 install install-daemon install-dev install-daemon-dev dev build-native-ghostty-kit stage-native-ghostty-kit build-native-app-dev dev-native test-native-swift test-native-automation test-native-terminal test-native-ci test test-v test-quick test-watch test-all test-frontend test-e2e test-harness clean generate-types check-types build-app build-app-dev app-screenshot dist release release-skip-tests

# Bare `make` does the full prod inner loop: install + open the app.
# `make install` is install-only (for scripts/CI that drive the launch
# separately). Same pattern mirrored for dev: `make dev` = install + open,
# `make install-dev` = install only.
.DEFAULT_GOAL := run

BINARY_NAME=attn
APP_BUNDLE=$(HOME)/Applications/attn.app
APP_BINARY=$(APP_BUNDLE)/Contents/MacOS/attn
# Dev sibling install. Separate bundle identifier (com.attn.manager.dev),
# separate data dir (~/.attn-dev), separate port (29849). See
# docs/plans or Phase 2 of the profiles feature for the full design.
APP_BUNDLE_DEV=$(HOME)/Applications/attn-dev.app
APP_BINARY_DEV=$(APP_BUNDLE_DEV)/Contents/MacOS/attn
# Dev targets may be invoked from a terminal hosted by the prod app, whose
# session env points back at the prod socket/wrapper. Do not let that routing
# state cross into the isolated dev daemon.
DEV_DAEMON_ENV=env -u ATTN_SOCKET_PATH -u ATTN_DB_PATH -u ATTN_CONFIG_PATH -u ATTN_WS_PORT -u ATTN_WRAPPER_PATH -u ATTN_INSIDE_APP -u ATTN_DAEMON_MANAGED -u ATTN_PTY_WORKER -u ATTN_SESSION_ID -u ATTN_AGENT ATTN_PROFILE=dev
BUILD_DIR=./cmd/attn
VERSION ?= $(shell bash ./scripts/version.sh)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
SOURCE_FINGERPRINT ?= $(shell bash ./scripts/source-fingerprint.sh --field fingerprint)
GIT_COMMIT ?= $(shell bash ./scripts/source-fingerprint.sh --field commit)
OUTPUT ?= $(BINARY_NAME)
GO_LDFLAGS = -X github.com/victorarias/attn/internal/buildinfo.Version=$(VERSION) -X github.com/victorarias/attn/internal/buildinfo.BuildTime=$(BUILD_TIME) -X github.com/victorarias/attn/internal/buildinfo.SourceFingerprint=$(SOURCE_FINGERPRINT) -X github.com/victorarias/attn/internal/buildinfo.GitCommit=$(GIT_COMMIT)
ZIG ?= zig
NATIVE_ZIG_PREFIX ?= $(shell HOMEBREW_NO_AUTO_UPDATE=1 brew --prefix zig@0.15 2>/dev/null)
NATIVE_GHOSTTY_DIR ?= $(abspath ../ghostty)
NATIVE_GHOSTTY_EXTERNAL_IO_REV ?= 853aa8cb89d1bb2bda17c867c0a0c88b5b286994
NATIVE_DEV_WS_URL ?= ws://localhost:29849/ws
NATIVE_APP_BUNDLE_DEV := $(abspath native-ui/.build/debug/attn-native-dev.app)
NATIVE_APP_BINARY_DEV := $(NATIVE_APP_BUNDLE_DEV)/Contents/MacOS/attn-native
NATIVE_SWIFT_BINARY_DEV := $(abspath native-ui/.build/debug/attn-native)
# Prefer a stable local development identity so macOS privacy grants survive
# source rebuilds. Sort by fingerprint because Keychain output ordering is
# not stable when more than one development identity is installed. CI and
# contributors without a certificate retain the ad-hoc fallback; callers may
# override this with a pinned fingerprint or Developer ID identity.
MACOS_CODESIGN_IDENTITY ?= $(shell security find-identity -v -p codesigning 2>/dev/null | awk '/"Apple Development:/ { print $$2 }' | LC_ALL=C sort | head -1)
ifeq ($(strip $(MACOS_CODESIGN_IDENTITY)),)
MACOS_CODESIGN_IDENTITY := -
endif
# Go tool binaries may be redirected by asdf or a local GOBIN setting.
GO_TOOLS_BIN ?= $(shell go env GOBIN)
ifeq ($(strip $(GO_TOOLS_BIN)),)
GO_TOOLS_BIN := $(shell go env GOPATH)/bin
endif

build:
	go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

build-linux-amd64:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC='$(ZIG) cc -target x86_64-linux-gnu' CXX='$(ZIG) c++ -target x86_64-linux-gnu' go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

build-linux-arm64:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC='$(ZIG) cc -target aarch64-linux-gnu' CXX='$(ZIG) c++ -target aarch64-linux-gnu' go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

GOTESTSUM=$(GO_TOOLS_BIN)/gotestsum

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

# Installing the PROD bundle while ATTN_PROFILE is set is almost always
# a mistake — it blows away the live install a developer is using. The
# guard fires during Make's parse phase so we fail *before* the expensive
# Tauri build, not after.
#
# Includes the default goal (`run`, invoked as bare `make`) — without it,
# `ATTN_PROFILE=dev make` would silently reinstall the prod bundle.
# The empty-goal case (bare `make`) is represented by the current
# DEFAULT_GOAL ("run"); we substitute that in so the check catches it.
GUARDED_PROD_TARGETS := run install install-daemon
ACTIVE_GOALS := $(if $(MAKECMDGOALS),$(MAKECMDGOALS),$(.DEFAULT_GOAL))
GUARDED_INVOCATION := $(filter $(GUARDED_PROD_TARGETS),$(ACTIVE_GOALS))
ifneq (,$(GUARDED_INVOCATION))
ifneq (,$(ATTN_PROFILE))
$(error ATTN_PROFILE=$(ATTN_PROFILE) is set. `make $(firstword $(ACTIVE_GOALS))` targets the PROD bundle ($(APP_BUNDLE)). Did you mean `make dev`? To proceed anyway: env -u ATTN_PROFILE make $(firstword $(ACTIVE_GOALS)))
endif
endif

run: install
	@open $(APP_BUNDLE)
	@echo "Launched $(APP_BUNDLE)"

install: build-app
	@echo ">>> Installing PROD: $(APP_BUNDLE) (profile=default, port=9849)"
	@mkdir -p ~/Applications
	@# Quit a running instance first. macOS keeps the running image
	@# via mmap, so rm -rf + cp alone would leave an old process running
	@# out of a deleted bundle while disk has the new code. Quiet
	@# no-op if nothing is running.
	@osascript -e 'tell application id "com.attn.manager" to quit' 2>/dev/null || true
	@rm -rf ~/Applications/attn.app
	cp -r app/src-tauri/target/release/bundle/macos/attn.app ~/Applications/
	@$(APP_BINARY) daemon ensure >/dev/null
	@echo "Installed attn.app to ~/Applications"

install-daemon: build
	@if [ ! -d "$(APP_BUNDLE)" ]; then \
		echo "No installed attn.app found in ~/Applications; run make install first"; \
		exit 1; \
	fi
	@echo ">>> Updating PROD daemon at $(APP_BINARY)"
	cp $(OUTPUT) $(APP_BINARY)
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" $(APP_BINARY); \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" $(APP_BUNDLE); \
	fi
	@$(APP_BINARY) daemon ensure >/dev/null
	@echo "Updated bundled daemon at $(APP_BINARY)"

# Dev-sibling install. `make dev` is THE command for the attn-on-attn
# inner loop: rebuild, reinstall, restart dev daemon — without ever
# touching the prod install. Uses a separate bundle identifier
# (com.attn.manager.dev), productName (attn-dev), data dir (~/.attn-dev),
# and port (29849). Prod daemon and app keep running.
#
# Run `eval "$(attn profile-env dev)"` (or `attn profile-env --fish dev | source`)
# in your shell if you want `attn ...` commands to target the dev daemon
# by default.
dev: install-dev
	@open $(APP_BUNDLE_DEV)
	@echo "Launched $(APP_BUNDLE_DEV)"

install-dev: build-app-dev
	@echo ">>> Installing DEV: $(APP_BUNDLE_DEV) (profile=dev, port=29849)"
	@mkdir -p ~/Applications
	@# Quit a running dev instance first; same mmap reasoning as `install`.
	@osascript -e 'tell application id "com.attn.manager.dev" to quit' 2>/dev/null || true
	@rm -rf $(APP_BUNDLE_DEV)
	cp -r app/src-tauri/target/release/bundle/macos/attn-dev.app ~/Applications/
	@$(DEV_DAEMON_ENV) $(APP_BINARY_DEV) daemon ensure >/dev/null
	@echo "Installed $(APP_BUNDLE_DEV) — profile=dev, data=~/.attn-dev, port=29849"

install-daemon-dev: build
	@if [ ! -d "$(APP_BUNDLE_DEV)" ]; then \
		echo "No installed attn-dev.app found in ~/Applications; run make dev first"; \
		exit 1; \
	fi
	@echo ">>> Updating DEV daemon at $(APP_BINARY_DEV)"
	cp $(OUTPUT) $(APP_BINARY_DEV)
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" $(APP_BINARY_DEV); \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" $(APP_BUNDLE_DEV); \
	fi
	@$(DEV_DAEMON_ENV) $(APP_BINARY_DEV) daemon ensure >/dev/null
	@echo "Updated bundled dev daemon at $(APP_BINARY_DEV)"

# Build the full Ghostty Metal renderer from attn's upstream-based fork.
build-native-ghostty-kit:
	@if [ ! -d "$(NATIVE_GHOSTTY_DIR)/.git" ]; then \
		echo "Missing attn Ghostty fork clone at $(NATIVE_GHOSTTY_DIR)"; \
		echo "Clone https://github.com/victorarias/ghostty and checkout attn/external-io."; \
		exit 1; \
	fi
	@if ! git -C "$(NATIVE_GHOSTTY_DIR)" merge-base --is-ancestor "$(NATIVE_GHOSTTY_EXTERNAL_IO_REV)" HEAD; then \
		echo "Ghostty checkout does not contain attn external-I/O commit $(NATIVE_GHOSTTY_EXTERNAL_IO_REV)."; \
		echo "Checkout the attn/external-io branch in $(NATIVE_GHOSTTY_DIR)."; \
		exit 1; \
	fi
	@if [ ! -x "$(NATIVE_ZIG_PREFIX)/bin/zig" ]; then \
		echo "Missing patched Zig toolchain; install it with: brew install zig@0.15"; \
		exit 1; \
	fi
	@if ! xcrun -find metal >/dev/null 2>&1; then \
		echo "Missing Metal Toolchain; install it with: xcodebuild -downloadComponent MetalToolchain"; \
		exit 1; \
	fi
	cd "$(NATIVE_GHOSTTY_DIR)" && PATH="$(NATIVE_ZIG_PREFIX)/bin:$$PATH" zig build -Dapp-runtime=none -Demit-xcframework=true -Dxcframework-target=native -Demit-macos-app=false -Dsentry=false -Di18n=false
	@test -f "$(NATIVE_GHOSTTY_DIR)/macos/GhosttyKit.xcframework/macos-arm64/libghostty-internal-fat.a"
	@echo "Built GhosttyKit from $(NATIVE_GHOSTTY_DIR)"

# Stage the locally-built renderer as a SwiftPM binary target input.
stage-native-ghostty-kit: build-native-ghostty-kit
	@mkdir -p native-ui/Vendor
	@rm -rf native-ui/Vendor/GhosttyKit.xcframework
	cp -R "$(NATIVE_GHOSTTY_DIR)/macos/GhosttyKit.xcframework" native-ui/Vendor/

# Build a signed macOS bundle for the Swift native client. A stable bundle
# identifier is required for Screen Recording permission to survive rebuilds.
build-native-app-dev: stage-native-ghostty-kit
	cd native-ui && swift build --product attn-native -c debug
	@mkdir -p "$(NATIVE_APP_BUNDLE_DEV)/Contents/MacOS"
	cp native-ui/Resources/Info.plist "$(NATIVE_APP_BUNDLE_DEV)/Contents/Info.plist"
	cp "$(NATIVE_SWIFT_BINARY_DEV)" "$(NATIVE_APP_BINARY_DEV)"
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" "$(NATIVE_APP_BINARY_DEV)" >/dev/null; \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" "$(NATIVE_APP_BUNDLE_DEV)" >/dev/null; \
	fi

# Run the workspace-first native client against the isolated dev daemon.
# The native Ghostty build currently requires Homebrew's Zig 0.15 patch for
# macOS SDK 26.4, so callers should not need to alter their asdf selection.
dev-native: build build-native-app-dev
	@echo ">>> Ensuring native dev daemon (profile=dev, port=29849, data=~/.attn-dev)"
	@if [ "$(UNAME_S)" = "Darwin" ]; then codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" $(OUTPUT) >/dev/null; fi
	@$(DEV_DAEMON_ENV) ./$(OUTPUT) daemon ensure >/dev/null
	@echo ">>> Launching $(NATIVE_APP_BUNDLE_DEV) (profile=dev, daemon=$(NATIVE_DEV_WS_URL)); Ctrl-C stops a client launched here"
	$(DEV_DAEMON_ENV) ATTN_NATIVE_WS_URL="$(NATIVE_DEV_WS_URL)" \
		ATTN_NATIVE_APP_BUNDLE="$(NATIVE_APP_BUNDLE_DEV)" \
		sh scripts/run-native-dev.sh

# Swift native unit tests. Agent-driven terminal smoke tests return once the
# forked native surface host is connected to the automation bridge.
test-native-swift: stage-native-ghostty-kit
	cd native-ui && swift test

# Signed app smoke test for the focus-safe automation foundation. It launches
# an isolated no-daemon instance and never attaches to live terminal state.
test-native-automation: build-native-app-dev
	node app/scripts/real-app-harness/scenario-swift-native-automation-foundation.mjs

# Isolated-daemon terminal test. The scenario creates workspaces via the
# daemon protocol, then verifies the native client through Ghostty surfaces.
# Set ATTN_NATIVE_REAL_AGENTS=1 to additionally render local Claude/Codex UIs.
test-native-terminal: build build-native-app-dev
	ATTN_NATIVE_DAEMON_BINARY="$(abspath $(OUTPUT))" node app/scripts/real-app-harness/scenario-swift-native-terminal.mjs

# CI-safe native coverage. The harness scenarios operate against isolated
# profiles and avoid hardware keyboard input or real locally installed agents.
test-native-ci: test-native-swift test-native-automation test-native-terminal

clean:
	rm -f $(BINARY_NAME)

# Type generation pipeline: TypeSpec -> JSON Schema -> go-jsonschema/quicktype
generate-types:
	cd internal/protocol/schema && pnpm exec tsp compile .
	$(GO_TOOLS_BIN)/go-jsonschema -p protocol --only-models --tags json --resolve-extension json \
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

# Build Tauri app with bundled daemon (app bundle only, no DMG dialog).
# Args:
#   $(1) — build-time env-var prefix (VITE_* for frontend, ATTN_* for cargo)
#   $(2) — productName (controls the .app bundle *folder* name — "attn" for
#          prod, "attn-dev" for the dev sibling). Matches the productName
#          in tauri.conf.json / tauri.dev.conf.json.
#   $(3) — extra args for `pnpm tauri build` (e.g. --config for overlays)
# The Go daemon is bundled as a sidecar under Contents/MacOS/attn regardless
# of productName. Sign the sidecar first, then the enclosing app bundle, so
# the installed app and the CLI/PTY host share the configured stable identity.
define build_tauri_app
	@mkdir -p app/src-tauri/binaries
	cp $(BINARY_NAME) app/src-tauri/binaries/$(BINARY_NAME)-aarch64-apple-darwin
	cd app && $(1) pnpm tauri build --bundles app $(3)
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		mkdir -p app/src-tauri/target/release/bundle/macos/$(2).app/Contents/Resources; \
		printf '{\n  "version": "%s",\n  "sourceFingerprint": "%s",\n  "gitCommit": "%s",\n  "buildTime": "%s"\n}\n' '$(VERSION)' '$(SOURCE_FINGERPRINT)' '$(GIT_COMMIT)' '$(BUILD_TIME)' > app/src-tauri/target/release/bundle/macos/$(2).app/Contents/Resources/build-identity.json; \
	fi
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" app/src-tauri/target/release/bundle/macos/$(2).app/Contents/MacOS/attn; \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" app/src-tauri/target/release/bundle/macos/$(2).app; \
	fi
endef

build-app: build
	$(call build_tauri_app,VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)',attn,)

# Dev build. Bakes ATTN_BUILD_PROFILE=dev into the Rust binary and
# VITE_ATTN_BUILD_PROFILE=dev + VITE_DAEMON_PORT=29849 into the frontend
# so the dev bundle can never accidentally point at the prod daemon.
# UI automation is gated at *runtime* now (see profile::automation_enabled
# in app/src-tauri/src/profile.rs) — dev builds get it on by default
# because the runtime sees ATTN_PROFILE=dev; prod builds get it off
# unless the user sets ATTN_AUTOMATION=1.
# Output: app/src-tauri/target/release/bundle/macos/attn-dev.app
build-app-dev: build
	$(call build_tauri_app,ATTN_BUILD_PROFILE=dev VITE_ATTN_BUILD_PROFILE=dev VITE_DAEMON_PORT=29849 VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)',attn-dev,--config src-tauri/tauri.dev.conf.json)

app-screenshot:
	cd app && node scripts/real-app-harness/capture-app-screenshot.mjs $(if $(SCREENSHOT_PATH),--path "$(SCREENSHOT_PATH)",) $(APP_SCREENSHOT_FLAGS)

# Sign the installed app and bundled CLI/daemon sidecar using the configured
# stable identity when available; falls back to ad-hoc without a certificate.
sign-app:
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" ~/Applications/attn.app/Contents/MacOS/attn; \
		codesign --force --sign "$(MACOS_CODESIGN_IDENTITY)" ~/Applications/attn.app; \
		echo "Signed ~/Applications/attn.app with identity $(MACOS_CODESIGN_IDENTITY)"; \
	else \
		echo "sign-app is only needed on macOS"; \
	fi

# Create distributable DMG
dist: build-app
	cd app && VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)' pnpm tauri build --bundles dmg
	@echo "DMG created at app/src-tauri/target/release/bundle/dmg/"

release:
	./scripts/release.sh $(VERSION_TAG)

release-skip-tests:
	./scripts/release.sh $(VERSION_TAG) --skip-tests
