
PLUGIN_NAME := mithshell
PLUGIN_DIR := $(CURDIR)
INSTALL_ROOT := $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps install uninstall run

check-deps:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 not found" >&2; exit 1; }
	@python3 -c 'import sys; print("python", sys.version.split()[0])' || { echo "python3 check failed" >&2; exit 1; }
	@command -v mithshell >/dev/null 2>&1 || echo "warning: mithshell not found on PATH; install it before using this plugin" >&2
	@echo "Dependencies OK"

install: check-deps
	@echo "Installing $(PLUGIN_NAME) plugin to $(INSTALL_ROOT)"
	@mkdir -p $(INSTALL_ROOT)
	@install -m 0644 plugin.toml $(INSTALL_ROOT)/plugin.toml
	@install -m 0755 mithshell_plugin.py $(INSTALL_ROOT)/mithshell_plugin.py
	@install -m 0644 README.md $(INSTALL_ROOT)/README.md
	@echo "Installed."

uninstall:
	@echo "Removing $(INSTALL_ROOT)"
	@rm -rf "$(INSTALL_ROOT)"

# Quick local test for the mithshell plugin
run:
	@python3 $(PLUGIN_DIR)/mithshell_plugin.py --once "lock"
