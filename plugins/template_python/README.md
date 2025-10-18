Template Python Plugin for Tarragon

Overview
- Logs initialization, startup, request/response handling, and selection events.
- Exposes `process(text)` that returns 3 randomly transposed variants of the input.
- CLI test: `make run` or `python3 template_plugin.py --once "Hello"`.

Install
 - `make install` installs to `~/.local/lib/tarragon/plugins/template_python/`.

Runtime
- Name: taken from `plugin.toml` (`name = ...`) and passed via `TARRAGON_PLUGIN_NAME` env var.
- Lifecycle is set to `daemon` in `plugin.toml`; Tarragon may also invoke `--once` for quick testing.
- Logs are written to stderr (journalctl under the tarragon unit) and to `~/.cache/tarragon/plugins/template_python/plugin.log`.
- Selection handling: when a `{"type":"select","query_id":"...","text":"<id>"}` is received, the plugin logs the event, resolves the id to a value from the last response for that query, and sends a lightweight ack so the UI can reflect the selection.
