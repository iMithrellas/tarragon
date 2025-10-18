Template Rust Plugin for Tarragon

Overview
- Logs initialization, startup, request/response handling, and selection events.
- `--once "TEXT"` prints JSON with 3 random transpositions of the input.
- No external crates; builds fully offline.

Build and Test
- make run         # cargo run --release -- --once "Hello"
- make install     # installs to ~/.local/lib/tarragon/plugins/template_rust

Runtime
- Name: taken from `plugin.toml` (`name = ...`) and passed via `TARRAGON_PLUGIN_NAME` env var.
- Lifecycle is set to `daemon` in `plugin.toml`; Tarragon may also invoke `--once` for quick testing.
- Logs go to stderr (journalctl under the tarragon unit) and `~/.cache/tarragon/plugins/template_rust/plugin.log`.
- Selection handling: when `{\"type\":\"select\",\"query_id\":\"...\",\"text\":\"<token>\"}` is received, the plugin logs the event and sends a lightweight ack response so the UI can reflect the selection.
