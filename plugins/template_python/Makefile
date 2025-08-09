PLUGIN_NAME := template_python
PLUGIN_DIR := $(CURDIR)
INSTALL_ROOT := $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps install uninstall run

check-deps:
    @command -v python3 >/dev/null 2>&1 || { echo "python3 not found" >&2; exit 1; }
    @python3 -c 'import sys, importlib; print("python", sys.version.split()[0]); importlib.import_module("zmq") or None' || { echo "pyzmq not installed. Try: pip install --user pyzmq" >&2; exit 1; }
    @echo "Dependencies OK"

install: check-deps
	@echo "Installing $(PLUGIN_NAME) plugin to $(INSTALL_ROOT)"
	@mkdir -p $(INSTALL_ROOT)
	@install -m 0644 plugin.toml $(INSTALL_ROOT)/plugin.toml
	@install -m 0755 template_plugin.py $(INSTALL_ROOT)/template_plugin.py
	@install -m 0644 README.md $(INSTALL_ROOT)/README.md
	@echo "Installed."

uninstall:
	@echo "Removing $(INSTALL_ROOT)"
	@rm -rf "$(INSTALL_ROOT)"

# Quick local test for the process() behavior
run:
	@python3 $(PLUGIN_DIR)/template_plugin.py --once "Hello Tarragon"
