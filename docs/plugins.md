# Building a Plugin for Tarragon

Disclaimer: This documentation was created with the help of an AI assistant. It may contain inaccuracies or omissions; please validate details and test in your environment.

This guide explains how to implement a plugin that integrates with the Tarragon daemon. Two modes are supported:

1) Preferred (fast): persistent process speaking NDJSON over a Unix Domain Socket to the daemon.
2) Fallback (simple): `--once <text>` CLI that prints a single JSON response to stdout.

The daemon can use either path; it prefers the persistent socket mode when your plugin connects and falls back to `--once` if not.

## Directory Layout and Install

Plugins are installed under `~/.local/lib/tarragon/plugins/<plugin_name>/` and discovered by the daemon at startup.

- Required files:
  - `plugin.toml` (configuration)
  - `entrypoint` executable (e.g., `my_plugin.py` or `my_plugin`)
  - `Makefile` with targets `check-deps`, `install`, `uninstall`, `run` (required)
    - The plugin manager will call `make check-deps` and `make install` to help users install your plugin.

Example `plugin.toml`:

```toml
name = "template_python"
description = "Template Python plugin"
enabled = true
entrypoint = "template_plugin.py"
lifecycle_mode = "daemon"  # "daemon" | "on_demand_persistent" | "on_call"

provides_general_suggestions = true
prefix = "@tpl"
build_dependencies = ["python3"]
capabilities = ["suggest"]
```

```toml
name = "Calculator"
description = "Evaluate basic math expressions"
enabled = true
entrypoint = "calc_plugin_executable"  # Path to the executable/script relative to plugin dir
lifecycle_mode = "on_demand_persistent"  # Options: "daemon", "on_demand_persistent", "on_call"
provides_general_suggestions = true  # Responds to input without a prefix?
prefix = "@calc"  # Optional: Prefix to force this plugin
build_dependencies = ["make", "go"]  # Optional: List of tools checked by 'make check-deps'
capabilities = ["suggest", "icon"]  # Optional: Extra features
icon = "calc.png"  # Optional: Icon path
```

Lifecycle modes:
- `daemon`: started and managed persistently by the launcher; should speak NDJSON over a Unix Domain Socket.
- `on_demand_persistent`: started when first needed and kept while the UI is attached (future flow).
- `on_call`: executed per request via `--once` (no persistent connection required).

## Unix Domain Socket + NDJSON Protocol (Recommended)

Endpoint: `/tmp/tarragon-plugins.sock` (daemon listener)

Protocol:
1) Connect to the Unix socket path from `TARRAGON_PLUGINS_ENDPOINT`.
2) Send hello (one NDJSON line):
   - `{ "type": "hello", "name": "<plugin-name>" }\n`
3) Loop:
   - Receive request (one NDJSON line):
     - `{ "type": "request", "query_id": "<id>", "text": "<input>" }\n`
   - Process and send response (one NDJSON line):
     - `{ "type": "response", "query_id": "<id>", "data": <your JSON> }\n`

NDJSON framing means each message is exactly one JSON object on one line, terminated by `\n`.

The daemon measures latency and merges your response into the UI snapshot.

Environment variables passed to daemon‑mode plugins:
- `TARRAGON_PLUGINS_ENDPOINT`: Unix socket path to connect to (for example, `/tmp/tarragon-plugins.sock`).
- `TARRAGON_PLUGIN_NAME`: The configured plugin name.

### Python skeleton

```python
import json, os, socket

endpoint = os.environ.get("TARRAGON_PLUGINS_ENDPOINT", "/tmp/tarragon-plugins.sock")
name = os.environ.get("TARRAGON_PLUGIN_NAME", "my_plugin")

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(endpoint)
s.sendall(json.dumps({"type": "hello", "name": name}).encode() + b"\n")

f = s.makefile('r')
while True:
    line = f.readline()
    if not line:
        break
    msg = json.loads(line)
    if msg.get("type") != "request":
        continue
    qid, text = msg["query_id"], msg["text"]
    data = {"echo": text}
    s.sendall(json.dumps({"type": "response", "query_id": qid, "data": data}).encode() + b"\n")
```

### Rust skeleton

```rust
use std::os::unix::net::UnixStream;
use std::io::{BufRead, BufReader, Write};

let endpoint = std::env::var("TARRAGON_PLUGINS_ENDPOINT")
    .unwrap_or("/tmp/tarragon-plugins.sock".into());
let stream = UnixStream::connect(&endpoint)?;
let mut writer = stream.try_clone()?;
let reader = BufReader::new(stream);

writeln!(writer, r#"{{"type":"hello","name":"my_plugin"}}"#)?;

for line in reader.lines() {
    let line = line?;
    // parse JSON {type:"request", query_id, text}
    // build response {type:"response", query_id, data}
    writeln!(writer, "{}", response_json)?;
}
```

## Fallback CLI (`--once`)

If your plugin does not use persistent socket mode, support a simple command-line path:

```
$ my_plugin --once "hello world"
{"input":"hello world","variants":["HELLO WORLD","Hello World","world hello"]}
```

- The daemon runs `entrypoint --once <text>` and expects a single JSON object on stdout.
- Stderr is captured for logging; non-zero exit will be surfaced as an error payload.

## Makefile Specification (Required)

Your Makefile must define the following targets:

```
check-deps:   # verify required toolchain (e.g., python3 or cargo/rustc)
install:      # build and copy files into ~/.local/lib/tarragon/plugins/<name>/
uninstall:    # remove installed files from the plugin directory
run:          # local quick test (e.g., --once "Hello")
```

Example (Python):

```
PLUGIN_NAME := my_plugin
INSTALL_ROOT := $(HOME)/.local/lib/tarragon/plugins/$(PLUGIN_NAME)

.PHONY: check-deps install uninstall run

check-deps:
	@command -v python3 >/dev/null || { echo "python3 missing" >&2; exit 1; }

install: check-deps
	@mkdir -p $(INSTALL_ROOT)
	@install -m 0644 plugin.toml $(INSTALL_ROOT)/plugin.toml
	@install -m 0755 my_plugin.py $(INSTALL_ROOT)/my_plugin.py

uninstall:
	@rm -rf $(INSTALL_ROOT)

run:
	@python3 my_plugin.py --once "Hello Tarragon"
```

## Logging

- Log initialization and readiness clearly (e.g., after sending the hello message over the socket).
- Log each request/response pair with the `query_id` to aid tracing.
- For long-running plugins, add periodic heartbeat logs.

## Security & Resource Notes

- Treat input as untrusted; validate/escape as needed in shell calls.
- Avoid expensive initialization in per-request paths; prefer the persistent Unix socket connection for performance.
- Keep stdout strictly for protocol JSON in `--once` mode.
