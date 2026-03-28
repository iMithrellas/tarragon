PLUGIN_NAME := file_finder
INSTALL_ROOT := $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps build install uninstall run clean

check-deps:
	@command -v go >/dev/null 2>&1 || { echo "go not found" >&2; exit 1; }
	@echo "Dependencies OK"

build: check-deps
	go build -o $(PLUGIN_NAME) .

install: build
	@echo "Installing $(PLUGIN_NAME) to $(INSTALL_ROOT)"
	@mkdir -p $(INSTALL_ROOT)
	@install -m 0755 $(PLUGIN_NAME) $(INSTALL_ROOT)/$(PLUGIN_NAME)
	@install -m 0644 plugin.toml $(INSTALL_ROOT)/plugin.toml
	@install -m 0644 README.md $(INSTALL_ROOT)/README.md
	@echo "Installed."

uninstall:
	@echo "Removing $(INSTALL_ROOT)"
	@rm -rf "$(INSTALL_ROOT)"

run:
	go run . --once "report"

clean:
	@rm -f $(PLUGIN_NAME)
