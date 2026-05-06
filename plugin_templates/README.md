# Plugin Templates

Executable plugin templates live here so they do not get mixed into Tarragon's bundled plugin set under `plugins/`.

Each template is a small plugin repository shape with:

- `plugin.toml`
- `Makefile`
- a local `make run` smoke test
- a `make install` target that installs to `~/.local/lib/tarragon/plugins/<name>/`

Available templates:

- `python`
- `rust`
- `go`
- `c`
- `cpp`
- `js_ts`
