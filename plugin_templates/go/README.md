# Template Go Plugin for Tarragon

This template implements a persistent Tarragon plugin using only the Go standard library.

Commands:

- `make run`: run a local `tarragon query` smoke test.
- `make build`: build the plugin binary.
- `make install`: install to `~/.local/lib/tarragon/plugins/template_go/`.

Runtime:

- Persistent mode connects to `TARRAGON_PLUGINS_ENDPOINT` and speaks NDJSON over a Unix socket.
- CLI smoke tests use `template_go tarragon query "TEXT"`.
