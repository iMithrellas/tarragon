# TarraGon justfile
# Run `just` to list all available recipes, or `just <recipe>` to run one.

set shell := ["bash", "-euo", "pipefail", "-c"]

# Paths
build_dir    := justfile_directory() / "build"
build_output := build_dir / "tarragon"
bin_symlink  := env("HOME") / ".local/bin/tarragon"
service_dir  := env("HOME") / ".config/systemd/user"
service_file := justfile_directory() / "systemd/tarragon.service"
plugin_root  := env("HOME") / ".local/lib/tarragon/plugins"

# List available recipes (default)
[doc("List all available recipes")]
default:
    @just --list --unsorted

# ─── Build & Install ──────────────────────────────────────────────

# Build the tarragon binary
[doc("Compile tarragon binary to build/tarragon")]
build:
    mkdir -p {{ build_dir }}
    go build -o {{ build_output }} ./cmd/

# Build with race detector enabled
[doc("Compile with -race for detecting data races")]
build-race:
    mkdir -p {{ build_dir }}
    go build -race -o {{ build_dir }}/tarragon-race ./cmd/

# Symlink binary into ~/.local/bin
[doc("Symlink build/tarragon to ~/.local/bin/tarragon")]
install-binary: build
    mkdir -p {{ env("HOME") }}/.local/bin
    ln -sf {{ build_output }} {{ bin_symlink }}
    @echo "Binary symlinked to {{ bin_symlink }}"

# ─── Systemd Service ─────────────────────────────────────────────

# Install the systemd user service
[doc("Symlink systemd unit to ~/.config/systemd/user/")]
install-service:
    mkdir -p {{ service_dir }}
    ln -sf {{ service_file }} {{ service_dir }}/tarragon.service
    @echo "Service symlinked to {{ service_dir }}/tarragon.service"

# Reload and restart the systemd user service
[doc("Reload systemd daemon and restart tarragon.service")]
reload-service:
    systemctl --user daemon-reload
    systemctl --user restart tarragon.service

# Show service status and recent logs
[doc("Show systemd service status")]
service-status:
    systemctl --user status tarragon.service || true

# Tail live service logs
[doc("Follow journal logs for tarragon.service")]
service-logs:
    journalctl --user -u tarragon.service -f

# Stop the service
[doc("Stop tarragon.service")]
stop-service:
    systemctl --user stop tarragon.service

# Enable service to start on login
[doc("Enable tarragon.service to start automatically on login")]
enable-service: install-service
    systemctl --user enable tarragon.service
    systemctl --user daemon-reload
    @echo "Service enabled; will start on login."

# ─── Run (full deploy cycle) ─────────────────────────────────────

# Build, install, and (re)start the service
[doc("Build -> install binary -> install service -> reload")]
run: build install-binary install-service reload-service

# ─── Testing ──────────────────────────────────────────────────────

# Run all Go tests
[doc("Run all Go tests")]
test *FLAGS:
    go test {{ FLAGS }} ./...

# Run tests with race detector
[doc("Run all Go tests with -race")]
test-race:
    go test -race ./...

# Run tests with verbose output
[doc("Run all Go tests with verbose output")]
test-verbose:
    go test -v ./...

# Run tests with coverage report
[doc("Run tests and generate coverage report")]
test-cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out
    @echo "HTML report: go tool cover -html=coverage.out"

# ─── Benchmarks ───────────────────────────────────────────────────

# Run the interactive benchmark TUI (requires daemon running)
[doc("Launch benchmark TUI against running daemon")]
bench *FLAGS:
    {{ build_output }} bench {{ FLAGS }}

# Run Go benchmarks (unit-level)
[doc("Run Go benchmark functions in all packages")]
go-bench *FLAGS:
    go test -bench=. -benchmem {{ FLAGS }} ./...

# ─── Linting & Formatting ────────────────────────────────────────

# Run all pre-commit hooks on all files
[doc("Run all pre-commit checks on the entire repo")]
lint:
    pre-commit run --all-files

# Run golangci-lint directly
[doc("Run golangci-lint on the codebase")]
golangci:
    golangci-lint run ./...

# Format all Go files
[doc("Format all Go source files")]
fmt:
    go fmt ./...
    @echo "All Go files formatted."

# Check for formatting issues without modifying
[doc("Check Go formatting without writing changes")]
fmt-check:
    @test -z "$(gofmt -l .)" || { echo "Unformatted files:"; gofmt -l .; exit 1; }

# ─── Pre-commit Setup ────────────────────────────────────────────

# Install pre-commit and set up git hooks
[doc("Install pre-commit tool and register git hooks")]
setup-precommit:
    #!/usr/bin/env bash
    set -euo pipefail
    if command -v pre-commit &>/dev/null; then
        echo "pre-commit already installed."
    elif command -v pacman &>/dev/null; then
        echo "Installing pre-commit via pacman..."
        sudo pacman -S --needed --noconfirm pre-commit
    elif command -v apt-get &>/dev/null; then
        echo "Installing pre-commit via apt..."
        sudo apt-get update && sudo apt-get install -y pre-commit
    elif command -v brew &>/dev/null; then
        echo "Installing pre-commit via Homebrew..."
        brew install pre-commit
    else
        echo "Falling back to pip..."
        pip install --user pre-commit
    fi
    pre-commit install

# ─── Plugins ──────────────────────────────────────────────────────

# Install a plugin by directory name (e.g., just plugin-install calc_python)
[doc("Install a plugin: just plugin-install <name>")]
plugin-install name:
    make -C plugins/{{ name }} install

# Uninstall a plugin by directory name
[doc("Uninstall a plugin: just plugin-uninstall <name>")]
plugin-uninstall name:
    make -C plugins/{{ name }} uninstall

# Quick-run a plugin locally (e.g., just plugin-run calc_python)
[doc("Run a plugin in test mode: just plugin-run <name>")]
plugin-run name:
    make -C plugins/{{ name }} run

# Check dependencies for a plugin
[doc("Check a plugin's build dependencies")]
plugin-check name:
    make -C plugins/{{ name }} check-deps

# Install all plugins
[doc("Install all plugins from plugins/")]
plugin-install-all:
    #!/usr/bin/env bash
    set -euo pipefail
    for dir in plugins/*/; do
        name=$(basename "$dir")
        echo "=== Installing plugin: $name ==="
        make -C "$dir" install
    done

# Install a remote plugin from a git URL
[doc("Install remote plugin: just install-remote-plugin <url>")]
install-remote-plugin url: build
    {{ build_output }} install-plugin {{ url }}

# Uninstall a previously installed remote plugin by name
[doc("Uninstall remote plugin: just uninstall-remote-plugin <name>")]
uninstall-remote-plugin name: build
    {{ build_output }} uninstall-plugin {{ name }}

# ─── Shell Completions ───────────────────────────────────────────

# Generate shell completions (bash, zsh, fish, powershell)
[doc("Generate shell completions: just completions <shell>")]
completions shell:
    {{ build_output }} completion generate {{ shell }}

# ─── Config ───────────────────────────────────────────────────────

# Generate a default config file
[doc("Generate default config: just config-generate [--config-format toml]")]
config-generate *FLAGS:
    {{ build_output }} config generate {{ FLAGS }}

# ─── Utilities ────────────────────────────────────────────────────

# Clean build artifacts
[doc("Remove build directory and coverage output")]
clean:
    rm -rf {{ build_dir }}
    rm -f coverage.out
    @echo "Cleaned."

# Tidy Go modules
[doc("Run go mod tidy")]
tidy:
    go mod tidy

# Run the TUI (requires daemon running)
[doc("Launch the TUI client")]
tui:
    {{ build_output }} tui

# Run the daemon in the foreground (useful for development)
[doc("Run daemon in foreground for development")]
daemon *FLAGS:
    {{ build_output }} daemon {{ FLAGS }}

# Show the binary version / help
[doc("Print tarragon --help")]
help:
    {{ build_output }} --help

# Full development check: fmt, lint, test, build
[doc("Run fmt + lint + test + build (full CI-like check)")]
check: fmt lint test build
    @echo "All checks passed."
