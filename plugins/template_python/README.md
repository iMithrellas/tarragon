Template Python Plugin for Tarragon

Overview
- Logs initialization, startup, and request/response handling.
- Exposes `process(text)` that returns 3 randomly transposed variants of the input.
- CLI test: `make run` or `python3 template_plugin.py --once "Hello"`.

Install
- `make install` installs to `~/.local/lib/tarragon/plugins/template_python/`.

Runtime
- Lifecycle is set to `daemon` in `plugin.toml`; Tarragon may also invoke `--once` for query handling.
- Logs are written to stderr and `~/.cache/tarragon/plugins/template_python/plugin.log`.

