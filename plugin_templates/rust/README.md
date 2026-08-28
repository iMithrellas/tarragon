# Template Rust Plugin for Tarragon

Overview
- Logs initialization, startup, request/response handling, and selection events.
- `template_rust tarragon query "TEXT"` prints JSON with 3 random transpositions of the input.
- No external crates; builds fully offline.

Build and Test
- make run         # cargo run --release -- tarragon query "Hello"
- make install     # installs to ~/.local/lib/tarragon/plugins/template_rust

Runtime
- Identity: the stable ID from `plugin.toml` is passed via `TARRAGON_PLUGIN_NAME` and `TARRAGON_PLUGIN_ID`; the display name is passed via `TARRAGON_PLUGIN_DISPLAY_NAME`.
- Lifecycle is set to `daemon` in `plugin.toml`; quick CLI testing can use the plugin executable's `tarragon query` subcommand.
- Logs go to stderr (journalctl under the tarragon unit) and `~/.cache/tarragon/plugins/template_rust/plugin.log`.
- Selection handling: when `{\"type\":\"select\",\"query_id\":\"...\",\"text\":\"<token>\"}` is received, the plugin logs the event and sends a lightweight ack response so the UI can reflect the selection.
