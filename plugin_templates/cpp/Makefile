PLUGIN_NAME := template_cpp
INSTALL_ROOT ?= $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)
CXX ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra

.PHONY: check-deps build install uninstall run clean

check-deps:
	@command -v $(CXX) >/dev/null 2>&1 || { echo "$(CXX) not found" >&2; exit 1; }
	@echo "Dependencies OK"

build: check-deps
	$(CXX) $(CXXFLAGS) -o $(PLUGIN_NAME) template_plugin.cpp

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
	@$(CXX) $(CXXFLAGS) -o $(PLUGIN_NAME) template_plugin.cpp
	@./$(PLUGIN_NAME) tarragon query "Hello Tarragon"

clean:
	@rm -f $(PLUGIN_NAME)
