.PHONY: build build-linux-amd64 build-linux-arm64 install install-daemon test test-v test-quick test-watch test-all test-frontend test-e2e test-harness clean generate-types check-types build-app build-app-ui-automation app-screenshot dist release release-skip-tests

BINARY_NAME=attn
APP_BUNDLE=$(HOME)/Applications/attn.app
APP_BINARY=$(APP_BUNDLE)/Contents/MacOS/attn
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

install: build-app-ui-automation
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
	cp $(OUTPUT) $(APP_BINARY)
	@if [ "$(UNAME_S)" = "Darwin" ]; then codesign -s - -f $(APP_BINARY); fi
	@$(APP_BINARY) daemon ensure >/dev/null
	@echo "Updated bundled daemon at $(APP_BINARY)"

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

# Build Tauri app with bundled daemon (app bundle only, no DMG dialog)
define build_tauri_app
	@mkdir -p app/src-tauri/binaries
	cp $(BINARY_NAME) app/src-tauri/binaries/$(BINARY_NAME)-aarch64-apple-darwin
	cd app && $(1) pnpm tauri build --bundles app
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		mkdir -p app/src-tauri/target/release/bundle/macos/attn.app/Contents/Resources; \
		printf '{\n  "version": "%s",\n  "sourceFingerprint": "%s",\n  "gitCommit": "%s",\n  "buildTime": "%s"\n}\n' '$(VERSION)' '$(SOURCE_FINGERPRINT)' '$(GIT_COMMIT)' '$(BUILD_TIME)' > app/src-tauri/target/release/bundle/macos/attn.app/Contents/Resources/build-identity.json; \
	fi
	@if [ "$(UNAME_S)" = "Darwin" ]; then \
		codesign -s - -f app/src-tauri/target/release/bundle/macos/attn.app/Contents/MacOS/attn; \
	fi
endef

build-app: build
	$(call build_tauri_app,VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)')

build-app-ui-automation: build
	$(call build_tauri_app,ATTN_UI_AUTOMATION=1 VITE_UI_AUTOMATION=1 VITE_INSTALL_CHANNEL=source VITE_ATTN_BUILD_VERSION='$(VERSION)' VITE_ATTN_SOURCE_FINGERPRINT='$(SOURCE_FINGERPRINT)' VITE_ATTN_GIT_COMMIT='$(GIT_COMMIT)' VITE_ATTN_BUILD_TIME='$(BUILD_TIME)')

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
