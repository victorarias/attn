.PHONY: build install test clean

BINARY_NAME=cm
INSTALL_DIR=$(HOME)/.local/bin
BUILD_DIR=./cmd/cm

build:
	go build -o $(BINARY_NAME) $(BUILD_DIR)

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

clean:
	rm -f $(BINARY_NAME)
	rm -f $(INSTALL_DIR)/$(BINARY_NAME)
