.PHONY: build build-linux-amd64 build-linux-arm64 install install-daemon install-dev install-daemon-dev dev test test-v test-quick test-watch test-all test-frontend test-e2e test-harness clean generate-types check-types build-app build-app-ui-automation build-app-dev app-screenshot dist release release-skip-tests

BINARY_NAME=attn
APP_BUNDLE=$(HOME)/Applications/attn.app
APP_BINARY=$(APP_BUNDLE)/Contents/MacOS/attn
# Dev sibling install. Separate bundle identifier (com.attn.manager.dev),
# separate data dir (~/.attn-dev), separate port (29849). See
# docs/plans or Phase 2 of the profiles feature for the full design.
APP_BUNDLE_DEV=$(HOME)/Applications/attn-dev.app
APP_BINARY_DEV=$(APP_BUNDLE_DEV)/Contents/MacOS/attn
BUILD_DIR=./cmd/attn
VERSION ?= $(shell bash ./scripts/version.sh)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
SOURCE_FINGERPRINT ?= $(shell bash ./scripts/source-fingerprint.sh --field fingerprint)
GIT_COMMIT ?= $(shell bash ./scripts/source-fingerprint.sh --field commit)
OUTPUT ?= $(BINARY_NAME)
GO_LDFLAGS = -X github.com/victorarias/attn/internal/buildinfo.Version=$(VERSION) -X github.com/victorarias/attn/internal/buildinfo.BuildTime=$(BUILD_TIME) -X github.com/victorarias/attn/internal/buildinfo.SourceFingerprint=$(SOURCE_FINGERPRINT) -X github.com/victorarias/attn/internal/buildinfo.GitCommit=$(GIT_COMMIT)
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

# Installing the PROD bundle while ATTN_PROFILE is set is almost always
# a mistake — it blows away the live install a developer is using. The
# guard fires during Make's parse phase so we fail *before* the expensive
# Tauri build, not after.
GUARDED_PROD_TARGETS := install install-daemon
ifneq (,$(filter $(GUARDED_PROD_TARGETS),$(MAKECMDGOALS)))
ifneq (,$(ATTN_PROFILE))
$(error ATTN_PROFILE=$(ATTN_PROFILE) is set. `make $(firstword $(MAKECMDGOALS))` targets the PROD bundle ($(APP_BUNDLE)). Did you mean `make dev`? To proceed anyway: env -u ATTN_PROFILE make $(firstword $(MAKECMDGOALS)))
endif
endif

install: build-app-ui-automation
	@echo ">>> Installing PROD: $(APP_BUNDLE) (profile=default, port=9849)"
	@mkdir -p ~/Applications
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
	@if [ "$(UNAME_S)" = "Darwin" ]; then codesign -s - -f $(APP_BINARY); fi
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

install-dev: build-app-dev
	@echo ">>> Installing DEV: $(APP_BUNDLE_DEV) (profile=dev, port=29849)"
	@mkdir -p ~/Applications
	@rm -rf $(APP_BUNDLE_DEV)
	cp -r app/src-tauri/target/release/bundle/macos/attn-dev.app ~/Applications/
	@ATTN_PROFILE=dev $(APP_BINARY_DEV) daemon ensure >/dev/null
	@echo "Installed $(APP_BUNDLE_DEV) — profile=dev, data=~/.attn-dev, port=29849"
	@open $(APP_BUNDLE_DEV)
	@echo "Launched $(APP_BUNDLE_DEV)"

install-daemon-dev: build
	@if [ ! -d "$(APP_BUNDLE_DEV)" ]; then \
		echo "No installed attn-dev.app found in ~/Applications; run make dev first"; \
		exit 1; \
	fi
	@echo ">>> Updating DEV daemon at $(APP_BINARY_DEV)"
	cp $(OUTPUT) $(APP_BINARY_DEV)
	@if [ "$(UNAME_S)" = "Darwin" ]; then codesign -s - -f $(APP_BINARY_DEV); fi
	@ATTN_PROFILE=dev $(APP_BINARY_DEV) daemon ensure >/dev/null
	@echo "Updated bundled dev daemon at $(APP_BINARY_DEV)"

clean:
	rm -f $(BINARY_NAME)

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

# Build Tauri app with bundled daemon (app bundle only, no DMG dialog).
# Args:
#   $(1) — build-time env-var prefix (VITE_* for frontend, ATTN_* for cargo)
#   $(2) — productName (controls the .app bundle *folder* name — "attn" for
#          prod, "attn-dev" for the dev sibling). Matches the productName
#          in tauri.conf.json / tauri.dev.conf.json.
#   $(3) — extra args for `pnpm tauri build` (e.g. --config for overlays)
# The Go daemon is bundled as a sidecar under Contents/MacOS/attn regardless
# of productName, so the codesign step is the same for both builds.
define build_tauri_app
	@mkdir -p app/src-tauri/binaries
	cp $(BINARY_NAME) app/src-tauri/binaries/$(BINARY_NAME)-aarch64-apple-darwin
	cd app && $(1) pnpm tauri build --bundles app $(3)
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		mkdir -p app/src-tauri/target/release/bundle/macos/$(2).app/Contents/Resources; \
		printf '{\n  "version": "%s",\n  "sourceFingerprint": "%s",\n  "gitCommit": "%s",\n  "buildTime": "%s"\n}\n' '$(VERSION)' '$(SOURCE_FINGERPRINT)' '$(GIT_COMMIT)' '$(BUILD_TIME)' > app/src-tauri/target/release/bundle/macos/$(2).app/Contents/Resources/build-identity.json; \
	fi
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign -s - -f app/src-tauri/target/release/bundle/macos/$(2).app/Contents/MacOS/attn; \
	fi
endef

build-app: build
	$(call build_tauri_app,VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)',attn,)

build-app-ui-automation: build
	$(call build_tauri_app,ATTN_UI_AUTOMATION=1 VITE_UI_AUTOMATION=1 VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)',attn,)

# Dev build. Bakes ATTN_BUILD_PROFILE=dev into the Rust binary and
# VITE_ATTN_BUILD_PROFILE=dev + VITE_DAEMON_PORT=29849 into the frontend
# so the dev bundle can never accidentally point at the prod daemon.
# UI automation is enabled by default (matching `make install` / the
# prod install path) so real-app harness scenarios work against the
# dev bundle too. Set ATTN_DEV_UI_AUTOMATION=0 to disable.
# Output: app/src-tauri/target/release/bundle/macos/attn-dev.app
ATTN_DEV_UI_AUTOMATION ?= 1
build-app-dev: build
ifeq ($(ATTN_DEV_UI_AUTOMATION),1)
	$(call build_tauri_app,ATTN_UI_AUTOMATION=1 VITE_UI_AUTOMATION=1 ATTN_BUILD_PROFILE=dev VITE_ATTN_BUILD_PROFILE=dev VITE_DAEMON_PORT=29849 VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)',attn-dev,--config src-tauri/tauri.dev.conf.json)
else
	$(call build_tauri_app,ATTN_BUILD_PROFILE=dev VITE_ATTN_BUILD_PROFILE=dev VITE_DAEMON_PORT=29849 VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)',attn-dev,--config src-tauri/tauri.dev.conf.json)
endif

app-screenshot:
	cd app && node scripts/real-app-harness/capture-app-screenshot.mjs $(if $(SCREENSHOT_PATH),--path "$(SCREENSHOT_PATH)",) $(APP_SCREENSHOT_FLAGS)

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
	cd app && VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)' pnpm tauri build --bundles dmg
	@echo "DMG created at app/src-tauri/target/release/bundle/dmg/"

release:
	./scripts/release.sh $(VERSION_TAG)

release-skip-tests:
	./scripts/release.sh $(VERSION_TAG) --skip-tests
