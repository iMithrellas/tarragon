# Building a Plugin for Tarragon

This guide explains how plugins integrate with the Tarragon daemon today.

Plugins declare a lifecycle in `plugin.toml`, and Tarragon runs them according to that lifecycle:

1. `daemon`: a persistent process speaking NDJSON over a Unix Domain Socket.
2. `on_demand_persistent`: a persistent process that Tarragon starts on first use, then keeps running until the daemon shuts down or the plugin is stopped.
3. `on_call`: an ephemeral CLI plugin invoked as `entrypoint tarragon <subcommand>`.

## Directory Layout and Install

Plugins are managed under `~/.local/lib/tarragon/plugins/<plugin_name>/` and loaded by the daemon at startup.

There are two supported plugin sources:

1. **Local plugin directory** (installed with `tarragon plugin install`): `entrypoint` is typically a path relative to the plugin directory.
2. **System plugin** (enabled with `tarragon plugin enable <name>`): Tarragon resolves the binary with `which <name>`, runs `<binary> tarragon manifest`, rewrites a relative `entrypoint` to the resolved absolute binary path, appends `source = "system"`, and stores the resulting manifest in Tarragon's plugin directory.

### System Plugin Enable + Reload Behavior

`tarragon plugin enable <name>` writes/updates plugin metadata on disk. A running daemon must reload or restart before it can use new manifests.

Current behavior note: `enable` itself does not trigger daemon reload. In practice, restart the daemon or issue a reload-triggering config update (for example via `tarragon plugin config ...`).

Integration contract:

- enable writes the plugin manifest into Tarragon's plugin directory with system entrypoint normalization.
- reload re-reads plugin metadata/config and refreshes in-memory plugin state used for status + dispatch.
- after reload, newly enabled system plugins are eligible for status/dispatch according to their manifest fields (`enabled`, `lifecycle_mode`, `prefix`, `require_prefix`, `provides_general_suggestions`).

### Plugin Source Metadata

System-enabled manifests include:

```toml
source = "system"
```

If source metadata is surfaced in status/runtime payloads, treat it as origin metadata (`system` vs local/default). Clients should tolerate missing source metadata for compatibility with older runtimes.

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
entrypoint = "calc_plugin_executable"  # Relative for local plugins; may be absolute for system-enabled plugins
lifecycle_mode = "on_demand_persistent"  # Options: "daemon", "on_demand_persistent", "on_call"
provides_general_suggestions = true  # Responds to input without a prefix?
prefix = "@calc"  # Optional: Prefix to force this plugin
build_dependencies = ["make", "go"]  # Optional: List of tools checked by 'make check-deps'
capabilities = ["suggest", "icon"]  # Optional: Extra features
icon = "calc.png"  # Optional: Icon path
```

Lifecycle modes:
- `daemon`: started by the daemon at startup and kept running.
- `on_demand_persistent`: started when a matching query needs the plugin and kept running afterward.
- `on_call`: executed per request via `tarragon query <text>`.

## Dispatch Eligibility and Prefix Targeting

Tarragon supports both global (unprefixed) and explicit prefix-targeted dispatch.

- **Global/unprefixed query**:
  - `require_prefix = true` excludes a plugin from unprefixed dispatch.
  - `provides_general_suggestions` is the contract field for general-suggestion eligibility. Older runtimes may still fan out to all non-`require_prefix` plugins.
- **Prefix-targeted query**: when input starts with a plugin prefix, Tarragon dispatches only to the matched plugin and forwards query text with the prefix removed.
  - Prefix-targeted dispatch does not depend on `provides_general_suggestions`.
  - If prefixes overlap, the longest matching prefix wins.

Use `require_prefix` for strict prefix-only plugins. Use `provides_general_suggestions` as the explicit global-eligibility signal; clients should tolerate legacy runtimes where this flag is not yet enforced in dispatch.

## Lifecycle-Aware Status Expectations

- `daemon` and `on_demand_persistent` plugins may be persistently connected (`connected=true`) while running.
- `on_call` plugins are ephemeral and usually not connected between requests.

Treat `connected` as transport state, not total availability. An enabled `on_call` plugin can be dispatchable even while not persistently connected.

Entrypoint path rules:
- Relative `entrypoint`: resolved from the plugin directory (`~/.local/lib/tarragon/plugins/<name>/...`).
- Absolute `entrypoint`: executed directly as-is (used by system-enabled plugins).

## Unix Domain Socket + NDJSON Protocol

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

The daemon measures latency and merges your response into the shared result snapshot. UIs also receive per-plugin query state metadata derived from dispatch/response events so they can show pending, empty, and error states while a query is still in flight.

Environment variables passed to persistent plugins:
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

## On-Call CLI Contract

If your plugin uses `lifecycle_mode = "on_call"`, Tarragon invokes CLI subcommands under `tarragon`:

```
$ my_plugin tarragon query "hello world"
{"input":"hello world","variants":["HELLO WORLD","Hello World","world hello"]}
```

- Query command: `entrypoint tarragon query <text>`
  - stdout: one JSON payload (same shape rules as persistent plugin `data`)
  - non-zero exit: treated as plugin error
- Select command: `entrypoint tarragon select <result-id> [action]`
  - used when user executes a result action for this plugin
  - plugin should act based on `result-id` (on-call plugins are ephemeral and should not rely on in-memory query state)
  - exit status controls success/failure
  - optional stdout JSON: `{ "success": true|false, "message": "..." }`

## Result Shape

Plugin payloads are normalized into Tarragon result items. The daemon currently understands these common shapes:

- `{ "results": [...] }`
- `{ "suggestions": [...] }`
- `{ "items": [...] }`
- `{ "choices": [...] }`
- `{ "variants": [...] }`
- `[ ... ]`
- a single object with `id` plus one of `label`, `title`, or `text`

Array items can be objects or strings.

Recognized object fields:

- `id`
- `label`, `title`, or `text`
- `description`
- `icon`
- `category`
- `score`
- `preview_path`
- `actions`

Example:

```json
{
  "results": [
    {
      "id": "firefox.desktop",
      "label": "Firefox",
      "description": "Web browser",
      "score": 0.97,
      "icon": "firefox",
      "category": "apps",
      "actions": [
        { "name": "open", "default": true }
      ]
    }
  ]
}
```

## Makefile Specification (Required)

Your Makefile must define the following targets:

```
check-deps:   # verify required toolchain (e.g., python3 or cargo/rustc)
install:      # build and copy files into ~/.local/lib/tarragon/plugins/<name>/
uninstall:    # remove installed files from the plugin directory
run:          # local quick test (e.g., tarragon query "Hello")
```

Tarragon's install flow currently:

1. clones the plugin repository,
2. validates `plugin.toml`,
3. runs `make check-deps`,
4. runs `make install`, passing `INSTALL_ROOT` and `PLUGIN_DIR`,
5. verifies the installed plugin directory exists.

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
	@python3 my_plugin.py tarragon query "Hello Tarragon"
```

## Logging

- Log initialization and readiness clearly (for persistent plugins, after sending the hello message over the socket).
- Log each request/response pair with the `query_id` to aid tracing.
- For long-running plugins, add periodic heartbeat logs.

## Security & Resource Notes

- Treat input as untrusted; validate/escape as needed in shell calls.
- Avoid expensive initialization in per-request paths; prefer a persistent lifecycle when the plugin is queried often.
- For `on_call` plugins, keep stdout strictly for JSON responses from Tarragon subcommands.
