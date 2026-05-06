# Template C Plugin for Tarragon

This template uses C11 plus POSIX Unix sockets and no third-party libraries.

Commands:

- `make run`: compile and run a local `tarragon query` smoke test.
- `make build`: build the plugin binary.
- `make install`: install to `~/.local/lib/tarragon/plugins/template_c/`.

Runtime:

- Persistent mode connects to `TARRAGON_PLUGINS_ENDPOINT` and speaks NDJSON over a Unix socket.
- CLI smoke tests use `template_c tarragon query "TEXT"`.
