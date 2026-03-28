# file_finder plugin (Go)

`file_finder` is a Go plugin for Tarragon that indexes files under common user directories and serves fuzzy filename matches over the Tarragon UDS/NDJSON plugin protocol.

## Features

- Resolves XDG user dirs from `~/.config/user-dirs.dirs` with fallback to:
  - `~/Documents`, `~/Downloads`, `~/Pictures`, `~/Videos`, `~/Music`, `~/Desktop`
- Maintains a SQLite index at:
  - `$XDG_STATE_HOME/tarragon/file_finder.db`, or
  - `~/.local/state/tarragon/file_finder.db`
- Background indexer:
  - initial scan on startup
  - rescans every 5 minutes
  - batched upserts + stale-file pruning
- Query matching:
  - tokenized case-insensitive `LIKE` matching on filename
  - relevance score based on filename/query length distance
  - returns top 20 sorted by score

## Build

```bash
make check-deps
make build
```

## Run (one-shot)

```bash
make run
# or
go run . --once "invoice"
```

## Install for Tarragon

```bash
make install
```

This installs:

- `file_finder` (binary)
- `plugin.toml`
- `README.md`

to `~/.local/lib/tarragon/plugins/file_finder/`.
