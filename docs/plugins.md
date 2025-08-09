# Building a Plugin for Tarragon

Disclaimer: This documentation was created with the help of an AI assistant. It may contain inaccuracies or omissions; please validate details and test in your environment.

This guide explains how to implement a plugin that integrates with the Tarragon daemon. Two modes are supported:

1) Preferred (fast): persistent process speaking ZeroMQ DEALER to the daemon ROUTER.
2) Fallback (simple): `--once <text>` CLI that prints a single JSON response to stdout.

The daemon can use either path; it prefers ZMQ if your plugin connects and falls back to `--once` if not.

## Directory Layout and Install

Plugins are installed under `~/.local/lib/tarragon/plugins/<plugin_name>/` and discovered by the daemon at startup.

- Required files:
  - `plugin.toml` (configuration)
  - `entrypoint` executable (e.g., `my_plugin.py` or `my_plugin`)
  - `Makefile` with targets `check-deps`, `install`, `uninstall`, `run` (required)
    - The plugin manager will call `make check-deps` and `make install` to help users install your plugin.

Example `plugin.toml`:

```
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

Lifecycle modes:
- `daemon`: started and managed persistently by the launcher; should speak ZMQ.
- `on_demand_persistent`: started when first needed and kept while the UI is attached (future flow).
- `on_call`: executed per request via `--once` (no persistent connection required).

## ZMQ Protocol (Recommended)

Endpoint: `ipc:///tmp/tarragon-plugins.ipc` (ROUTER on daemon side)

Handshake:
1) Connect a DEALER socket; set identity to your plugin name (optional but helpful).
2) Send: `{ "type": "hello", "name": "<plugin-name>" }`
3) Loop:
   - Receive: `{ "type": "request", "query_id": "<id>", "text": "<input>" }`
   - Process and send: `{ "type": "response", "query_id": "<id>", "data": <your JSON> }`

The daemon measures latency and merges your response into the UI snapshot.

Environment variables passed to daemon‑mode plugins:
- `TARRAGON_PLUGINS_ENDPOINT`: ROUTER endpoint to connect to.
- `TARRAGON_PLUGIN_NAME`: The configured plugin name.

### Python (pyzmq) skeleton

```
import json, os, zmq

endpoint = os.environ.get("TARRAGON_PLUGINS_ENDPOINT", "ipc:///tmp/tarragon-plugins.ipc")
name = os.environ.get("TARRAGON_PLUGIN_NAME", "my_plugin")

ctx = zmq.Context.instance()
sock = ctx.socket(zmq.DEALER)
sock.setsockopt(zmq.IDENTITY, name.encode())
sock.connect(endpoint)
sock.send_json({"type":"hello","name":name})

while True:
    msg = sock.recv_json()
    if msg.get("type") != "request":
        continue
    qid, text = msg["query_id"], msg["text"]
    data = {"echo": text}
    sock.send_json({"type":"response","query_id":qid,"data":data})
```

### Rust (zmq crate) skeleton

```
let ctx = zmq::Context::new();
let sock = ctx.socket(zmq::DEALER)?;
sock.set_identity(b"my_plugin")?;
sock.connect("ipc:///tmp/tarragon-plugins.ipc")?;
sock.send(r#"{"type":"hello","name":"my_plugin"}"#, 0)?;
loop {
    let msg = sock.recv_msg(0)?;
    // parse JSON {type:"request", query_id, text}
    // build response {type:"response", query_id, data}
    sock.send(response_json, 0)?;
}
```

## Fallback CLI (`--once`)

If your plugin does not use ZMQ, support a simple command-line path:

```
$ my_plugin --once "hello world"
{"input":"hello world","variants":["HELLO WORLD","Hello World","world hello"]}
```

- The daemon runs `entrypoint --once <text>` and expects a single JSON object on stdout.
- Stderr is captured for logging; non-zero exit will be surfaced as an error payload.

## Makefile Specification (Required)

Your Makefile must define the following targets:

```
check-deps:   # verify required toolchain (e.g., python3/pyzmq or cargo/rustc)
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
	@python3 -c 'import zmq' || { echo "pyzmq missing (pip install --user pyzmq)" >&2; exit 1; }

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

- Log initialization and readiness clearly (e.g., after sending HELLO for ZMQ).
- Log each request/response pair with the `query_id` to aid tracing.
- For long-running plugins, add periodic heartbeat logs.

## Security & Resource Notes

- Treat input as untrusted; validate/escape as needed in shell calls.
- Avoid expensive initialization in per-request paths; prefer the ZMQ persistent connection for performance.
- Keep stdout strictly for protocol JSON in `--once` mode.
