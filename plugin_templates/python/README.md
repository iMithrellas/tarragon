# Template Python Plugin for Tarragon

Overview
- Logs initialization, startup, request/response handling, and selection events.
- Exposes `process(text)` that returns 3 randomly transposed variants of the input.
- CLI test: `make run` or `python3 template_plugin.py tarragon query "Hello"`.

Install
- `make install` installs to `~/.local/lib/tarragon/plugins/template_python/`.

Runtime
- Identity: the stable ID from `plugin.toml` is passed via `TARRAGON_PLUGIN_NAME` and `TARRAGON_PLUGIN_ID`; the display name is passed via `TARRAGON_PLUGIN_DISPLAY_NAME`.
- Lifecycle is set to `daemon` in `plugin.toml`; quick CLI testing can use the plugin script's `tarragon query` subcommand.
- Logs are written to stderr (journalctl under the tarragon unit) and to `~/.cache/tarragon/plugins/template_python/plugin.log`.
- Selection handling: when a `{"type":"select","query_id":"...","text":"<id>"}` is received, the plugin logs the event, resolves the id to a value from the last response for that query, and sends a lightweight ack so the UI can reflect the selection.
