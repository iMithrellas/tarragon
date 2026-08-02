PLUGIN_NAME := wallpaper
INSTALL_ROOT ?= $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps build install uninstall run info test clean

check-deps:
	@command -v go >/dev/null 2>&1 || { echo "go not found" >&2; exit 1; }
	@echo "Build dependencies OK"
	@# Runtime dependencies are optional and only warned about: the plugin
	@# auto-detects a backend at runtime and reports a clear error if none
	@# is present.
	@command -v swww >/dev/null 2>&1 \
		|| command -v hyprpaper >/dev/null 2>&1 \
		|| command -v swaybg >/dev/null 2>&1 \
		|| command -v wbg >/dev/null 2>&1 \
		|| echo "warning: no wallpaper backend found (install swww, hyprpaper, swaybg or wbg)" >&2
	@command -v matugen >/dev/null 2>&1 \
		|| echo "warning: matugen not found; theme generation will be skipped" >&2

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

run: build
	@./$(PLUGIN_NAME) tarragon query ""

info: build
	@./$(PLUGIN_NAME) --info

test:
	go test ./...

clean:
	@rm -f $(PLUGIN_NAME)
