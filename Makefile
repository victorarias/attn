.PHONY: build install test test-all test-frontend clean generate-types check-types build-app install-app install-all dist

BINARY_NAME=attn
INSTALL_DIR=$(HOME)/.local/bin
BUILD_DIR=./cmd/attn

build:
	go build -o $(BINARY_NAME) $(BUILD_DIR)

test:
	go test ./...

test-frontend:
	cd app && pnpm run test

test-all: test test-frontend

install: build
	@mkdir -p $(INSTALL_DIR)
	@# Use cat to avoid copying extended attributes that trigger Gatekeeper
	cat $(BINARY_NAME) > $(INSTALL_DIR)/$(BINARY_NAME)
	chmod +x $(INSTALL_DIR)/$(BINARY_NAME)
	@# Remove quarantine attribute if present
	-xattr -d com.apple.quarantine $(INSTALL_DIR)/$(BINARY_NAME) 2>/dev/null || true
	@# Ad-hoc sign the binary to satisfy Gatekeeper
	codesign -s - -f $(INSTALL_DIR)/$(BINARY_NAME)
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
