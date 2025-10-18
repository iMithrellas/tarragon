Contributing to Tarragon

Thanks for considering contributing! This guide shows how to build, test, lint, and submit changes.

Quick start
- Prereqs: Go 1.24+, pre-commit, git, systemd (for the user service).
- Fork and clone the repo, then create a feature branch.
- Install hooks: pre-commit install; run all checks once: pre-commit run -a.
- Build: make build. Run TUI/daemon: tarragon tui or tarragon daemon.

Makefile targets
- build: Compiles the binary to build/tarragon.
- install-binary: Symlinks build/tarragon to ~/.local/bin/tarragon.
- install-service: Symlinks systemd unit to ~/.config/systemd/user/tarragon.service.
- reload-service: Reloads the user systemd daemon and restarts the service.
- run: build → install-binary → install-service → reload-service.

Notes
- run will restart your user service; ensure your unit in systemd/tarragon.service is as you expect.
- PATH: ~/.local/bin must be in your PATH to run tarragon directly.

Pre-commit hooks
This project uses pre-commit to enforce formatting, linting, and tests before commits.

Install
- pipx install pre-commit or pip install --user pre-commit
- Optional: golangci-lint (if not auto-installed by the hook):
  - macOS: brew install golangci-lint
  - Linux: curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b ~/.local/bin

Enable and run
- make setup-precommit        # install pre-commit (pacman/apt/brew/pip) and install hooks
- pre-commit run -a           # run all checks over the repo

Configured checks (.pre-commit-config.yaml)
- Pre-commit hooks: trailing-whitespace, end-of-file-fixer, check-yaml, check-added-large-files.
- Go hooks (pre-commit-golang): go-fmt, go-imports, no-go-testing, golangci-lint, go-unit-tests.

Testing
- Unit tests: go test ./...
- You can also rely on the pre-commit go-unit-tests hook; it will run tests on commit.

Project build/run tips
- Config: tarragon config generate [--config-dir DIR] [--config-format toml|yaml|json|ini].
- DB: SQLite file path defaults to XDG_DATA_HOME/tarragon/tarragon.db (or ~/.local/share/tarragon/tarragon.db).
- Completions: tarragon completion generate [bash|zsh|fish|powershell].

Contribution workflow
- Branch: create a descriptive branch name (e.g., feature/frecency-db).
- Small, focused PRs: keep diffs tight and scoped to the change.
- Tests: add/adjust tests when you change behavior or add features.
- Passing checks: ensure pre-commit passes locally before opening a PR.

Reporting issues and proposing changes
- Open an issue describing the problem/feature and context.
- Link to relevant code and logs where possible.

License
- By contributing, you agree your contributions are licensed under the repository’s license.
