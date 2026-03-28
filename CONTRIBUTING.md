Contributing to Tarragon

Thanks for considering contributing! This guide shows how to build, test, lint, and submit changes.

Quick start
- Prereqs: Go 1.24+, [just](https://just.systems), pre-commit, git, systemd (for the user service).
- Fork and clone the repo, then create a feature branch.
- Install hooks: `just setup-precommit`; run all checks once: `just lint`.
- Build: `just build`. Run TUI/daemon: `just tui` or `just daemon`.
- See all recipes: `just` (with no arguments).

Justfile recipes
Run `just` to list everything. Key recipes:

Build & install
- `just build`: Compile the binary to build/tarragon.
- `just install-binary`: Symlink build/tarragon to ~/.local/bin/tarragon.
- `just run`: build -> install binary -> install service -> reload.
- `just clean`: Remove build artifacts.

Service management
- `just install-service`: Symlink systemd unit to ~/.config/systemd/user/tarragon.service.
- `just reload-service`: Reload the user systemd daemon and restart the service.
- `just enable-service`: Enable the service to start on login.
- `just service-status`: Show current service status.
- `just service-logs`: Tail live journal logs.
- `just stop-service`: Stop the running service.

Testing & benchmarks
- `just test`: Run all Go tests.
- `just test-race`: Run tests with the race detector.
- `just test-cover`: Generate a coverage report.
- `just bench`: Launch the interactive benchmark TUI (daemon must be running).
- `just go-bench`: Run Go-level benchmark functions.

Linting & formatting
- `just lint`: Run all pre-commit checks on the repo.
- `just golangci`: Run golangci-lint directly.
- `just fmt`: Format all Go files.
- `just fmt-check`: Check formatting without modifying files.
- `just check`: Full CI-like check (fmt + lint + test + build).

Plugins
- `just plugin-install <name>`: Install a plugin (e.g., `just plugin-install calc_python`).
- `just plugin-uninstall <name>`: Uninstall a plugin.
- `just plugin-run <name>`: Quick-test a plugin locally.
- `just plugin-install-all`: Install every plugin in plugins/.

Notes
- `just run` will restart your user service; ensure your unit in systemd/tarragon.service is as you expect.
- PATH: ~/.local/bin must be in your PATH to run tarragon directly.

Pre-commit hooks
This project uses pre-commit to enforce formatting, linting, and tests before commits.

Install
- pipx install pre-commit or pip install --user pre-commit
- Optional: golangci-lint (if not auto-installed by the hook):
  - macOS: brew install golangci-lint
  - Linux: curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b ~/.local/bin

Enable and run
- `just setup-precommit`       # install pre-commit (pacman/apt/brew/pip) and register hooks
- `just lint`                  # run all checks over the repo

Configured checks (.pre-commit-config.yaml)
- Pre-commit hooks: trailing-whitespace, end-of-file-fixer, check-yaml, check-added-large-files.
- Go hooks (pre-commit-golang): go-fmt, go-imports, no-go-testing, golangci-lint, go-unit-tests.

Testing
- Unit tests: `just test`
- Verbose: `just test-verbose`
- Coverage: `just test-cover`
- You can also rely on the pre-commit go-unit-tests hook; it will run tests on commit.

Project build/run tips
- Config: `just config-generate --config-format toml` (or yaml, json, ini).
- DB: SQLite file path defaults to XDG_DATA_HOME/tarragon/tarragon.db (or ~/.local/share/tarragon/tarragon.db).
- Completions: `just completions bash` (or zsh, fish, powershell).

Contribution workflow
- Branch: create a descriptive branch name (e.g., feature/frecency-db).
- Small, focused PRs: keep diffs tight and scoped to the change.
- Tests: add/adjust tests when you change behavior or add features.
- Passing checks: run `just check` locally before opening a PR.

Reporting issues and proposing changes
- Open an issue describing the problem/feature and context.
- Link to relevant code and logs where possible.

License
- By contributing, you agree your contributions are licensed under the repository's license.
