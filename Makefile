BUILD_DIR := $(CURDIR)/build
BUILD_OUTPUT := $(BUILD_DIR)/tarragon
BIN_SYMLINK := $(HOME)/.local/bin/tarragon
SERVICE_DIR := $(HOME)/.config/systemd/user
SERVICE_FILE := $(CURDIR)/systemd/tarragon.service

build:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_OUTPUT) ./cmd/


install-binary:
	@echo "Creating $(HOME)/.local/bin if it doesn't exist..."
	mkdir -p $(HOME)/.local/bin
	@echo "Symlinking binary from $(BUILD_OUTPUT) to $(BIN_SYMLINK)..."
	ln -sf $(BUILD_OUTPUT) $(BIN_SYMLINK)
	@echo "Binary symlinked to $(BIN_SYMLINK)"


install-service:
	@echo "Creating systemd user service directory..."
	mkdir -p $(SERVICE_DIR)
	@echo "Symlinking service file..."
	ln -sf $(SERVICE_FILE) $(SERVICE_DIR)/tarragon.service
	@echo "Service file symlinked to $(SERVICE_DIR)/tarragon.service"

reload-service:
	@echo "Reloading systemd user daemon..."
	systemctl --user daemon-reload
	@echo "Restarting tarragon service..."
	systemctl --user restart tarragon.service

run: build install-binary install-service reload-service

.PHONY: build install-binary install-service reload-service run

