
PLUGIN_NAME := system_control
PLUGIN_DIR := $(CURDIR)
INSTALL_ROOT := $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps install uninstall run test

check-deps:
	@command -v python3 >/dev/null 2>&1 || { echo "python3 not found" >&2; exit 1; }
	@command -v systemctl >/dev/null 2>&1 || { echo "systemctl not found" >&2; exit 1; }
	@command -v loginctl >/dev/null 2>&1 || { echo "loginctl not found" >&2; exit 1; }
	@echo "Dependencies OK"

install: check-deps
	@echo "Installing $(PLUGIN_NAME) plugin to $(INSTALL_ROOT)"
	@mkdir -p $(INSTALL_ROOT)
	@install -m 0644 plugin.toml $(INSTALL_ROOT)/plugin.toml
	@install -m 0755 system_control_plugin.py $(INSTALL_ROOT)/system_control_plugin.py
	@install -m 0644 README.md $(INSTALL_ROOT)/README.md
	@echo "Installed."

uninstall:
	@echo "Removing $(INSTALL_ROOT)"
	@rm -rf "$(INSTALL_ROOT)"

run:
	@python3 $(PLUGIN_DIR)/system_control_plugin.py --once "sleep"

test:
	@PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s $(PLUGIN_DIR) -p 'test_*.py'
