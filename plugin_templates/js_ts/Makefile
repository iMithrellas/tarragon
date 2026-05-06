PLUGIN_NAME := template_js_ts
INSTALL_ROOT ?= $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps install uninstall run

check-deps:
	@command -v node >/dev/null 2>&1 || { echo "node not found" >&2; exit 1; }
	@echo "Dependencies OK"

install: check-deps
	@echo "Installing $(PLUGIN_NAME) plugin to $(INSTALL_ROOT)"
	@mkdir -p $(INSTALL_ROOT)
	@install -m 0644 plugin.toml $(INSTALL_ROOT)/plugin.toml
	@install -m 0755 template_plugin.js $(INSTALL_ROOT)/template_plugin.js
	@install -m 0644 template_plugin.ts $(INSTALL_ROOT)/template_plugin.ts
	@install -m 0644 README.md $(INSTALL_ROOT)/README.md
	@echo "Installed."

uninstall:
	@echo "Removing $(INSTALL_ROOT)"
	@rm -rf "$(INSTALL_ROOT)"

run:
	@node template_plugin.js tarragon query "Hello Tarragon"
