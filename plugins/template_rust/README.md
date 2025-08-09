Template Rust Plugin for Tarragon

Overview
- Logs initialization, startup, and request/response handling.
- `--once "TEXT"` prints JSON with 3 random transpositions of the input.
- No external crates; builds fully offline.

Build and Test
- make run         # cargo run --release -- --once "Hello"
- make install     # installs to ~/.local/lib/tarragon/plugins/template_rust

Runtime
- Lifecycle is set to `daemon` in `plugin.toml`; Tarragon may also invoke `--once` for query handling.
- Logs go to stderr and ~/.cache/tarragon/plugins/template_rust/plugin.log.

