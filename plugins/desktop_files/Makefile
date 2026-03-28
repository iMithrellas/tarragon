
PLUGIN_NAME := desktop_files
PLUGIN_DIR := $(CURDIR)
INSTALL_ROOT := $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps install uninstall run

check-deps:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 not found" >&2; exit 1; }
	@python3 -c 'import sys; print("python", sys.version.split()[0])' || { echo "python3 check failed" >&2; exit 1; }
	@echo "Dependencies OK"

install: check-deps
	@echo "Installing $(PLUGIN_NAME) plugin to $(INSTALL_ROOT)"
	@mkdir -p $(INSTALL_ROOT)
	@install -m 0644 plugin.toml $(INSTALL_ROOT)/plugin.toml
	@install -m 0755 desktop_files_plugin.py $(INSTALL_ROOT)/desktop_files_plugin.py
	@install -m 0644 README.md $(INSTALL_ROOT)/README.md
	@echo "Installed."

uninstall:
	@echo "Removing $(INSTALL_ROOT)"
	@rm -rf "$(INSTALL_ROOT)"

run:
	@python3 $(PLUGIN_DIR)/desktop_files_plugin.py --once "firefox"
