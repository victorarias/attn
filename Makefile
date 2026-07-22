.PHONY: run build build-linux-amd64 build-linux-arm64 install install-daemon install-dev install-daemon-dev dev test test-v test-quick test-watch test-all test-frontend test-e2e test-harness clean generate-types check-types build-app ensure-codesign-identity sign-app app-screenshot dist release release-skip-tests

# Bare `make` does the full prod inner loop: install + open the app.
# `make install` is install-only (for scripts/CI that drive the launch
# separately). Same pattern mirrored for dev: `make dev` = install + open,
# `make install-dev` = install only.
.DEFAULT_GOAL := run

BINARY_NAME=attn
# Prod bundle. Referenced by the parse-time guard message and `sign-app`; every
# other profile's app path / bundle id / port is derived at build+install time
# from the single authority (`attn profile resolve`), never hardcoded here.
APP_BUNDLE=$(HOME)/Applications/attn.app
APP_BINARY=$(APP_BUNDLE)/Contents/MacOS/attn
# Which profile to build/install. Empty = the default/prod bundle. A named
# profile (e.g. `make install PROFILE=agent7`) builds attn-agent7.app /
# com.attn.manager.agent7 with its own data dir + port. `make dev` ==
# `make install PROFILE=dev`. See docs/profiles.md.
PROFILE ?=
# Routing env that must NOT leak from a parent attn terminal into an isolated
# profile daemon we (re)start. Shared by every non-default profile install.
PROFILE_DAEMON_UNSET = -u ATTN_SOCKET_PATH -u ATTN_DB_PATH -u ATTN_CONFIG_PATH -u ATTN_WS_PORT -u ATTN_WRAPPER_PATH -u ATTN_INSIDE_APP -u ATTN_DAEMON_MANAGED -u ATTN_PTY_WORKER -u ATTN_SESSION_ID -u ATTN_AGENT
BUILD_DIR=./cmd/attn
VERSION ?= $(shell bash ./scripts/version.sh)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
SOURCE_FINGERPRINT ?= $(shell bash ./scripts/source-fingerprint.sh --field fingerprint)
GIT_COMMIT ?= $(shell bash ./scripts/source-fingerprint.sh --field commit)
OUTPUT ?= $(BINARY_NAME)
GO_LDFLAGS = -X github.com/victorarias/attn/internal/buildinfo.Version=$(VERSION) -X github.com/victorarias/attn/internal/buildinfo.BuildTime=$(BUILD_TIME) -X github.com/victorarias/attn/internal/buildinfo.SourceFingerprint=$(SOURCE_FINGERPRINT) -X github.com/victorarias/attn/internal/buildinfo.GitCommit=$(GIT_COMMIT)
ZIG ?= zig
# Prefer a stable local development identity so macOS privacy grants survive
# source rebuilds. Contributors without a certificate retain the ad-hoc
# fallback; callers may override this with a pinned identity or fingerprint.
MACOS_CODESIGN_IDENTITY ?=

# Native VT library (libghostty-vt) for the server-authoritative terminal.
# Only Darwin/arm64 links it via cgo (internal/ghosttyvt); every other platform
# compiles the pure-Go stub and needs nothing here. The archive lives under
# gitignored third_party/ (scripts/build-libghostty-vt.sh is the source of
# truth), so a fresh checkout has no archive and the first cgo link would fail.
# `build` therefore depends on the archive target on Darwin/arm64: it runs the
# build script — which requires zig 0.16.x and clones the pinned source — when
# the archive is missing or the pin moved, and is a no-op once it is present.
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
NATIVE_VT_LIB := third_party/ghostty-vt/lib/libghostty-vt.a
ifeq ($(UNAME_S)/$(UNAME_M),Darwin/arm64)
NATIVE_VT_DEP := $(NATIVE_VT_LIB)
else
NATIVE_VT_DEP :=
endif

# Rebuild the native archive when its pin, build script, or carried patch
# changes (or it is absent). The patch is listed via $(wildcard) so a fresh
# checkout without it still resolves the rule; once present, editing it forces a
# rebuild (the script applies it, so a stale archive would miss the change). The
# script installs into third_party/ghostty-vt/{lib,include}.
$(NATIVE_VT_LIB): ghostty-vt-native.pin scripts/build-libghostty-vt.sh $(wildcard ghostty-vt-native.patch)
	./scripts/build-libghostty-vt.sh

build: $(NATIVE_VT_DEP)
	go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

build-linux-amd64:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC='$(ZIG) cc -target x86_64-linux-gnu' CXX='$(ZIG) c++ -target x86_64-linux-gnu' go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

build-linux-arm64:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC='$(ZIG) cc -target aarch64-linux-gnu' CXX='$(ZIG) c++ -target aarch64-linux-gnu' go build -ldflags "$(GO_LDFLAGS)" -o $(OUTPUT) $(BUILD_DIR)

GOTESTSUM=$(HOME)/go/bin/gotestsum

$(GOTESTSUM):
	go install gotest.tools/gotestsum@latest

test:
	./scripts/test-go.sh

# Verbose test output (shows all test names as they run)
test-v:
	./scripts/test-go.sh -v

# Quick test (compact package output)
test-quick:
	./scripts/test-go.sh

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
ifeq (,$(PROFILE))
# Bare (prod) install while a profile is active in the shell — almost always a
# mistake that would clobber the live prod bundle.
$(error ATTN_PROFILE=$(ATTN_PROFILE) is set but `make $(firstword $(ACTIVE_GOALS))` targets the PROD bundle ($(APP_BUNDLE)). Build that profile with `make $(firstword $(ACTIVE_GOALS)) PROFILE=$(ATTN_PROFILE)`, or target prod explicitly with `env -u ATTN_PROFILE make $(firstword $(ACTIVE_GOALS))`)
else ifneq ($(ATTN_PROFILE),$(PROFILE))
# Build/runtime consistency (profiles design, safety #2): you cannot build
# agent7's app while your shell says you are agent8.
$(error build/runtime mismatch: ATTN_PROFILE=$(ATTN_PROFILE) but PROFILE=$(PROFILE). Refusing to build a different profile than your shell. Re-run with PROFILE=$(ATTN_PROFILE), or unset ATTN_PROFILE)
endif
endif
endif

# Build + install + open the PROFILE's app (bare `make` = prod). Every path is
# derived from the single authority so a named profile opens its own bundle.
run: install
	@app_name="$$($(CURDIR)/$(OUTPUT) profile resolve --profile "$(PROFILE)" --field appName)"; \
	open "$(HOME)/Applications/$$app_name.app"; \
	echo "Launched $(HOME)/Applications/$$app_name.app"

# Install-only (no open). `make install` = prod; `make install PROFILE=agent7`
# = the isolated agent7 bundle. Resources resolved from `attn profile resolve`.
install: build-app
	@set -e; \
	profile="$(PROFILE)"; \
	attn="$(CURDIR)/$(OUTPUT)"; \
	app_name="$$("$$attn" profile resolve --profile "$$profile" --field appName)"; \
	bundle_id="$$("$$attn" profile resolve --profile "$$profile" --field bundleId)"; \
	ws_port="$$("$$attn" profile resolve --profile "$$profile" --field wsPort)"; \
	label="$$("$$attn" profile resolve --profile "$$profile" --field label)"; \
	app_bundle="$(HOME)/Applications/$$app_name.app"; \
	app_binary="$$app_bundle/Contents/MacOS/attn"; \
	echo ">>> Installing $$label: $$app_bundle (port=$$ws_port)"; \
	mkdir -p ~/Applications; \
	: "Quit a running instance first. macOS keeps the running image via mmap,"; \
	: "so rm -rf + cp alone would leave an old process out of a deleted bundle."; \
	osascript -e "tell application id \"$$bundle_id\" to quit" 2>/dev/null || true; \
	rm -rf "$$app_bundle"; \
	cp -r "app/src-tauri/target/release/bundle/macos/$$app_name.app" ~/Applications/; \
	if [ -n "$$profile" ]; then \
		env $(PROFILE_DAEMON_UNSET) ATTN_PROFILE="$$profile" "$$app_binary" daemon ensure >/dev/null; \
	else \
		"$$app_binary" daemon ensure >/dev/null; \
	fi; \
	echo "Installed $$app_bundle (profile=$$label, port=$$ws_port)"

# Fast path: swap just the Go daemon sidecar into the already-installed PROFILE
# bundle, re-sign, and restart its daemon. `make install-daemon PROFILE=agent7`.
install-daemon: ensure-codesign-identity build
	@set -e; \
	profile="$(PROFILE)"; \
	attn="$(CURDIR)/$(OUTPUT)"; \
	app_name="$$("$$attn" profile resolve --profile "$$profile" --field appName)"; \
	label="$$("$$attn" profile resolve --profile "$$profile" --field label)"; \
	app_bundle="$(HOME)/Applications/$$app_name.app"; \
	app_binary="$$app_bundle/Contents/MacOS/attn"; \
	if [ ! -d "$$app_bundle" ]; then \
		echo "No installed $$app_name.app in ~/Applications; run make install$$([ -n "$$profile" ] && echo " PROFILE=$$profile") first"; \
		exit 1; \
	fi; \
	echo ">>> Updating $$label daemon at $$app_binary"; \
	cp $(OUTPUT) "$$app_binary"; \
	if [ "$(UNAME_S)" = "Darwin" ]; then \
		identity="$(MACOS_CODESIGN_IDENTITY)"; \
		if [ -z "$$identity" ]; then identity="$$(bash ./scripts/macos-codesign-identity.sh find)"; fi; \
		if [ -z "$$identity" ]; then identity="-"; fi; \
		codesign --force --sign "$$identity" "$$app_binary"; \
		codesign --force --sign "$$identity" "$$app_bundle"; \
	fi; \
	if [ -n "$$profile" ]; then \
		env $(PROFILE_DAEMON_UNSET) ATTN_PROFILE="$$profile" "$$app_binary" daemon ensure >/dev/null; \
	else \
		"$$app_binary" daemon ensure >/dev/null; \
	fi; \
	echo "Updated bundled daemon at $$app_binary"

# `make dev` is THE command for the attn-on-attn inner loop: rebuild, reinstall,
# restart the dev daemon — without ever touching the prod install. It is now a
# thin alias for the profile-parameterized targets (PROFILE=dev → attn-dev.app /
# com.attn.manager.dev / ~/.attn-dev / port 29849). `make install PROFILE=<name>`
# is the general form; these aliases preserve the documented dev commands.
#
# These aliases target the dev profile UNCONDITIONALLY, so they clear the
# inherited ATTN_PROFILE before recursing. Without that, an agent whose shell is
# scoped to its own profile (e.g. ATTN_PROFILE=agent7 — the documented per-agent
# setup) would trip the build/runtime-mismatch guard (PROFILE=dev != agent7) on
# the very command the docs hand them as the dev escape hatch. `make dev` means
# "I want the dev sibling, period", independent of the shell's profile.
#
# Run `eval "$(attn profile-env dev)"` (or `attn profile-env --fish dev | source`)
# in your shell if you want `attn ...` commands to target the dev daemon
# by default.
dev:
	@env -u ATTN_PROFILE $(MAKE) run PROFILE=dev

install-dev:
	@env -u ATTN_PROFILE $(MAKE) install PROFILE=dev

install-daemon-dev:
	@env -u ATTN_PROFILE $(MAKE) install-daemon PROFILE=dev

clean:
	rm -f $(BINARY_NAME)

# Type generation pipeline: TypeSpec -> JSON Schema -> go-jsonschema/quicktype
generate-types:
	rm -rf internal/protocol/schema/tsp-output/json-schema
	cd internal/protocol/schema && pnpm exec tsp compile .
	$(HOME)/go/bin/go-jsonschema -p protocol --only-models --tags json --resolve-extension json \
		--capitalization ID --capitalization URL --capitalization SHA --capitalization PR --capitalization CI \
		-o internal/protocol/generated.go \
		internal/protocol/schema/tsp-output/json-schema/*.json
	# The @26.0.0 pin is the guarantee: npx otherwise resolves whatever's
	# latest on npm on every run, so an unpinned quicktype release can change
	# generated.ts out from under everyone with no signal beyond a
	# check-types diff (or, if flag defaults shift again, no diff at all).
	# --no-prefer-unions/--no-prefer-unknown are a style choice pinned
	# alongside it, and are what the rest of the app is written against: real
	# enums (usable as values, not just string literals) and `any` index
	# signatures. Those were quicktype's defaults until 26, which flipped both
	# preferences on — so the flags are only redundant with the version pin
	# for as long as both stay put. Keep them explicit.
	npx quicktype@26.0.0 \
		--src internal/protocol/schema/tsp-output/json-schema/*.json \
		--src-lang schema --lang typescript \
		--no-prefer-unions --no-prefer-unknown \
		-o app/src/types/generated.ts

# CI check: verify generated files are up-to-date
check-types: generate-types
	git diff --exit-code internal/protocol/generated.go app/src/types/generated.ts

# Build the packaged app for $(PROFILE) (empty = prod). All bundle metadata is
# derived from `attn profile resolve` by scripts/build-app-profile.sh: the
# default build uses the committed tauri.conf.json; a named profile gets a
# generated tauri.<name>.gen.conf.json overlay and bakes ATTN_BUILD_PROFILE /
# ATTN_BUILD_WS_PORT / ATTN_BUILD_BUNDLE_ID into the binary so it can never
# point at another profile's daemon. UI automation is gated at runtime (see
# profile::automation_enabled) — a profiled build sees its ATTN_PROFILE and
# enables automation for any named profile (dev, ticketqa, agent7, …); prod (the
# empty-profile bundle) stays off unless ATTN_AUTOMATION=1.
build-app: ensure-codesign-identity build
	@PROFILE="$(PROFILE)" ATTN_BIN="$(CURDIR)/$(OUTPUT)" \
		VERSION='$(VERSION)' SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' \
		GIT_COMMIT='$(GIT_COMMIT)' BUILD_TIME='$(BUILD_TIME)' \
		MACOS_CODESIGN_IDENTITY='$(MACOS_CODESIGN_IDENTITY)' \
		bash ./scripts/build-app-profile.sh

ensure-codesign-identity:
	@identity="$$(bash ./scripts/macos-codesign-identity.sh ensure)"; \
	if [ "$$identity" = "-" ]; then \
		echo "No stable macOS code-signing identity available; falling back to ad-hoc signing."; \
	else \
		echo "Using macOS code-signing identity $$identity"; \
	fi

app-screenshot:
	cd app && node scripts/real-app-harness/capture-app-screenshot.mjs $(if $(SCREENSHOT_PATH),--path "$(SCREENSHOT_PATH)",) $(APP_SCREENSHOT_FLAGS)

# Sign the installed app and bundled CLI/daemon sidecar using the configured
# stable identity when available; falls back to ad-hoc without a certificate.
sign-app: ensure-codesign-identity
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		identity="$(MACOS_CODESIGN_IDENTITY)"; \
		if [ -z "$$identity" ]; then identity="$$(bash ./scripts/macos-codesign-identity.sh find)"; fi; \
		if [ -z "$$identity" ]; then identity="-"; fi; \
		codesign --force --sign "$$identity" ~/Applications/attn.app/Contents/MacOS/attn; \
		codesign --force --sign "$$identity" ~/Applications/attn.app; \
		echo "Signed ~/Applications/attn.app with identity $$identity"; \
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
