PLUGIN_NAME := template_rust
INSTALL_ROOT := $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps build install uninstall run clean

check-deps:
	@command -v cargo >/dev/null 2>&1 || { echo "cargo not found" >&2; exit 1; }
	@command -v rustc >/dev/null 2>&1 || { echo "rustc not found" >&2; exit 1; }
	@echo "Dependencies OK"

build: check-deps
	cargo build --release

install: build
	@echo "Installing $(PLUGIN_NAME) to $(INSTALL_ROOT)"
	@mkdir -p $(INSTALL_ROOT)
	@install -m 0755 target/release/template_rust $(INSTALL_ROOT)/template_rust
	@install -m 0644 plugin.toml $(INSTALL_ROOT)/plugin.toml
	@install -m 0644 README.md $(INSTALL_ROOT)/README.md
	@echo "Installed."

uninstall:
	@echo "Removing $(INSTALL_ROOT)"
	@rm -rf "$(INSTALL_ROOT)"

run:
	cargo run --release -- --once "Hello Tarragon"

clean:
	cargo clean
