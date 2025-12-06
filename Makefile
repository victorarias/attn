.PHONY: build install test clean build-attn install-attn clean-attn

BINARY_NAME=cm
BINARY_NAME_ATTN=attn
INSTALL_DIR=$(HOME)/.local/bin
BUILD_DIR=./cmd/cm

build:
	go build -o $(BINARY_NAME) $(BUILD_DIR)

build-attn:
	go build -o $(BINARY_NAME_ATTN) $(BUILD_DIR)

test:
	go test ./...

install: build
	@mkdir -p $(INSTALL_DIR)
	@# Use cat to avoid copying extended attributes that trigger Gatekeeper
	cat $(BINARY_NAME) > $(INSTALL_DIR)/$(BINARY_NAME)
	chmod +x $(INSTALL_DIR)/$(BINARY_NAME)
	@# Remove quarantine attribute if present
	-xattr -d com.apple.quarantine $(INSTALL_DIR)/$(BINARY_NAME) 2>/dev/null || true
	@# Ad-hoc sign the binary to satisfy Gatekeeper
	codesign -s - -f $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Installed $(BINARY_NAME) to $(INSTALL_DIR)"

install-attn: build-attn
	@mkdir -p $(INSTALL_DIR)
	cat $(BINARY_NAME_ATTN) > $(INSTALL_DIR)/$(BINARY_NAME_ATTN)
	chmod +x $(INSTALL_DIR)/$(BINARY_NAME_ATTN)
	-xattr -d com.apple.quarantine $(INSTALL_DIR)/$(BINARY_NAME_ATTN) 2>/dev/null || true
	codesign -s - -f $(INSTALL_DIR)/$(BINARY_NAME_ATTN)
	@echo "Installed $(BINARY_NAME_ATTN) to $(INSTALL_DIR)"

clean:
	rm -f $(BINARY_NAME)
	rm -f $(INSTALL_DIR)/$(BINARY_NAME)

clean-attn:
	rm -f $(BINARY_NAME_ATTN)
	rm -f $(INSTALL_DIR)/$(BINARY_NAME_ATTN)
