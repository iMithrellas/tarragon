# Template JavaScript/TypeScript Plugin for Tarragon

This template provides a runnable JavaScript plugin plus a TypeScript source reference with the same shape.

Commands:

- `make run`: run a local `tarragon query` smoke test with Node.js.
- `make install`: install to `~/.local/lib/tarragon/plugins/template_js_ts/`.

Runtime:

- Persistent mode connects to `TARRAGON_PLUGINS_ENDPOINT` and speaks NDJSON over a Unix socket.
- CLI smoke tests use `node template_plugin.js tarragon query "TEXT"`.
- `template_plugin.ts` is included as a typed reference; compile it with your TypeScript setup if you want a TS-first plugin.
