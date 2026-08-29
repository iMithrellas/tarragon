# Building a Plugin for Tarragon

This guide explains how plugins integrate with the Tarragon daemon today.

Plugins declare a lifecycle in `plugin.toml`, and Tarragon runs them according to that lifecycle:

1. `daemon`: a persistent process speaking NDJSON over a Unix Domain Socket.
2. `on_demand_persistent`: a persistent process that Tarragon starts on first use, then keeps running until the daemon shuts down or the plugin is stopped.
3. `on_call`: an ephemeral CLI plugin invoked as `entrypoint tarragon <subcommand>`.

## Directory Layout and Install

Plugins are managed under `~/.local/lib/tarragon/plugins/<plugin-id>/` and loaded by the daemon at startup.

Repository layout note:

- Bundled first-party plugins live in `plugins/`.
- Authoring templates live in `plugin_templates/` and are intentionally separate from bundled plugins.
- Template directories use the same `plugin.toml` + `Makefile` contract as installable plugin repositories.

There are two supported plugin sources:

1. **Plugin Git repository** (installed with `tarragon plugin install <git-url>`): the repository root must contain `plugin.toml` and a `Makefile`; `entrypoint` is typically a path relative to the installed plugin directory.
2. **System plugin** (enabled with `tarragon plugin enable <name>`): Tarragon resolves the binary with `which <name>`, runs `<binary> tarragon manifest`, rewrites a relative `entrypoint` to the resolved absolute binary path, appends `source = "system"`, and stores the resulting manifest in Tarragon's plugin directory.

### System Plugin Enable + Reload Behavior

`tarragon plugin enable <name>` writes/updates plugin metadata on disk. A running daemon must reload or restart before it can use new manifests.

Current behavior note: `enable` itself does not trigger daemon reload. In practice, run `tarragon plugin restart`, or issue a reload-triggering config update (for example via `tarragon plugin config ...`).

Integration contract:

- enable writes the plugin manifest into Tarragon's plugin directory with system entrypoint normalization.
- reload re-reads plugin metadata/config and refreshes in-memory plugin state used for status + dispatch.
- after reload, newly enabled system plugins are eligible for status/dispatch according to their manifest fields (`enabled`, `lifecycle_mode`, `prefix`, `require_prefix`, `provides_general_suggestions`).

### Restarting Plugins

`tarragon plugin restart [plugin-id]` bounces plugin processes in the running daemon. With no argument every plugin is targeted.

Restart re-reads manifests and config overrides first, so it also picks up plugins installed or reconfigured since the daemon started. Reported status per plugin:

| Lifecycle | Status | Behavior |
| --- | --- | --- |
| `daemon` | `restarted` | process stopped and started again |
| `on_demand_persistent` | `stopped` | stopped only; starts again on the next matching query |
| `on_call` | `skipped` | no long-lived process to bounce |
| any, disabled | `stopped` | stopped, not started |
| unknown name | `error` | reported as not found |

## Plugin Shutdown Contract

Tarragon stops plugin processes gracefully. A plugin is sent `SIGTERM` and is only sent `SIGKILL` if it is still alive after the grace period, which is set by the `plugin_stop_timeout` config option (default `5s`).

This applies to daemon shutdown, `tarragon plugin restart`, and plugins stopped by a reload because they were disabled or had their lifecycle changed.

Plugin authors should handle `SIGTERM`: flush state, close sockets and exit. Persistent plugins that ignore it will still be killed, just later. Plugins are stopped concurrently, so total shutdown time is bounded by the slowest plugin rather than the sum of all of them.

### Plugin Source Metadata

System-enabled manifests include:

```toml
source = "system"
```

Status responses currently include source metadata when it is present in the manifest. Treat it as origin metadata (`system` vs local/default), and tolerate missing source metadata for local plugins or older manifests.

- Required files for plugin repositories installed with `tarragon plugin install`:
  - `plugin.toml` (configuration)
  - `entrypoint` executable (e.g., `my_plugin.py` or `my_plugin`)
  - `Makefile` with targets `check-deps`, `install`, `uninstall`, `run` (required)
    - The plugin manager will call `make check-deps` and `make install` to help users install your plugin.

Example `plugin.toml`:

```toml
id = "template_python"
name = "Template Python"
description = "Template Python plugin"
enabled = true
entrypoint = "template_plugin.py"
lifecycle_mode = "daemon"  # "daemon" | "on_demand_persistent" | "on_call"

provides_general_suggestions = true
prefix = "tpl"  # typed as @tpl with the default prefix_symbol
build_dependencies = ["python3"]
capabilities = ["suggest"]
```

```toml
id = "calculator"
name = "Calculator"
description = "Evaluate basic math expressions"
enabled = true
entrypoint = "calc_plugin_executable"  # Relative for local plugins; may be absolute for system-enabled plugins
lifecycle_mode = "on_demand_persistent"  # Options: "daemon", "on_demand_persistent", "on_call"
provides_general_suggestions = true  # Responds to input without a prefix?
prefix = "calc"  # Optional: bare token; the prefix_symbol is prepended
build_dependencies = ["make", "go"]  # Optional: List of tools checked by 'make check-deps'
capabilities = ["suggest", "icon"]  # Optional: Extra features
icon = "calc.png"  # Optional: Icon path
```

## Plugin Identity

Every plugin has two distinct names:

- `id` is the stable identifier. It is used for the install directory, the `[plugins.<id>]` config section, `tarragon plugin config <id>`, the `plugin` field on results, and the plugin's IPC routing name.
- `name` is a human-readable display name with no routing meaning. It may contain spaces and capitals.

`id` must consist of lowercase letters, digits, `_` and `-`. When `id` is omitted it defaults to the plugin's install directory name; invalid characters are normalized (`"System Control"` becomes `system_control`). Declaring `id` explicitly is recommended.

The daemon passes the id to plugins as `TARRAGON_PLUGIN_NAME` and `TARRAGON_PLUGIN_ID`, and the display name as `TARRAGON_PLUGIN_DISPLAY_NAME`. A persistent plugin must send its id in the `hello` message, or the daemon cannot route requests to it.

## Configuration Overrides

Tarragon-level plugin settings are overridden in the configuration stack,
keyed by plugin id:

```toml
[plugins.system_control]
enabled = true
prefix = "@sys"
lifecycle_mode = "on_call"
```

Only `enabled`, `prefix` and `lifecycle_mode` are supported. Any other key, or a section that matches no installed plugin id, is ignored and reported as a warning in the daemon log. Sections keyed by display name still apply for backwards compatibility, but are deprecated and warn.

Overrides can also be written with `tarragon plugin config <id> --prefix sys`,
which updates the primary `tarragon.toml` and asks a running daemon to reload.
Settings in a higher-precedence `tarragon.d/*.toml` file still win. See the
main README for the complete configuration precedence order. Plugin-specific
settings that Tarragon does not understand belong in the plugin's own config
file instead.

Lifecycle modes:
- `daemon`: started by the daemon at startup and kept running.
- `on_demand_persistent`: started when a matching query needs the plugin and kept running afterward.
- `on_call`: executed per request by running `entrypoint tarragon query <text>`.

## Dispatch Eligibility and Prefix Targeting

Tarragon supports both global (unprefixed) and explicit prefix-targeted dispatch.

- **Global/unprefixed query**:
  - `require_prefix = true` excludes a plugin from unprefixed dispatch.
  - `provides_general_suggestions` is the contract field for general-suggestion eligibility. Current dispatch requires it to be true for unprefixed queries. For compatibility, discovered plugin manifests that omit the field default to true, but plugin authors should set it explicitly.
- **Prefix-targeted query**: when input starts with a plugin prefix, Tarragon dispatches only to the matched plugin and forwards query text with the prefix removed.
  - Prefix-targeted dispatch does not depend on `provides_general_suggestions`.
  - If prefixes overlap, the longest matching prefix wins. Equal-length matches are a configuration error; dispatch falls back to the lowest plugin id and the daemon logs a collision warning.
- **Incomplete or unknown prefix**: any input beginning with the configured global prefix symbol is treated as an explicit routing attempt. If it does not match an enabled plugin prefix, no plugin is dispatched, including general-suggestion plugins.

Use `require_prefix` for strict prefix-only plugins. Use `provides_general_suggestions` as the explicit global-eligibility signal.

### The Prefix Symbol

Manifests declare a bare prefix token, not the leading symbol:

```toml
prefix = "calc"
```

The symbol comes from the top-level `prefix_symbol` config option, which defaults to `@`. The example above is typed as `@calc`, and changing `prefix_symbol` to `:` changes it to `:calc` for every plugin at once, without editing any manifest.

A prefix that starts with a non-alphanumeric character is treated as literal and is used exactly as written, so `prefix = "@calc"` and `prefix = "="` keep working regardless of the configured symbol. Prefix overrides in `[plugins.<id>]` follow the same rule.

The effective prefix is what `tarragon plugin list`, `tarragon plugin config` and the status response report. The configured symbol is passed to plugins as `TARRAGON_PREFIX_SYMBOL` for plugins that expose several prefixes of their own.

## Lifecycle-Aware Status Expectations

- `daemon` and `on_demand_persistent` plugins may be persistently connected (`connected=true`) while running.
- `on_call` plugins are ephemeral and usually not connected between requests.

Treat `connected` as transport state, not total availability. An enabled `on_call` plugin can be dispatchable even while not persistently connected.

Entrypoint path rules:
- Relative `entrypoint`: resolved from the plugin directory (`~/.local/lib/tarragon/plugins/<plugin-id>/...`).
- Absolute `entrypoint`: executed directly as-is (used by system-enabled plugins).
- If an absolute entrypoint for a `source = "system"` on-call plugin no longer
  exists, Tarragon resolves its basename through the daemon's `PATH`. This lets
  system plugins continue working after their executable moves between standard
  user/system bin directories. Local plugin manifests do not use this fallback.

## Unix Domain Socket + NDJSON Protocol

Endpoint: resolved by the daemon at startup (see "Socket Locations" below) and
passed to persistent plugins via `TARRAGON_PLUGINS_ENDPOINT`.

Protocol:
1) Connect to the Unix socket path from `TARRAGON_PLUGINS_ENDPOINT`.
2) Send hello (one NDJSON line):
   - `{ "type": "hello", "name": "<plugin-id>" }\n`
3) Loop:
   - Receive request (one NDJSON line):
     - `{ "type": "request", "query_id": "<id>", "text": "<input>" }\n`
   - Process and send response (one NDJSON line):
     - `{ "type": "response", "query_id": "<id>", "data": <your JSON> }\n`

NDJSON framing means each message is exactly one JSON object on one line, terminated by `\n`.

The daemon measures latency and merges your response into the shared result snapshot. UIs also receive per-plugin query state metadata derived from dispatch/response events so they can show pending, empty, and error states while a query is still in flight.

Environment variables passed to plugins:
- `TARRAGON_PLUGINS_ENDPOINT`: Unix socket path to connect to (for example, `/run/user/1000/tarragon/plugins.sock`).
- `TARRAGON_PLUGIN_NAME`: The stable plugin id used for IPC routing. This is set for persistent and on-call plugins.
- `TARRAGON_PLUGIN_ID`: The stable plugin id. This is set for persistent and on-call plugins.
- `TARRAGON_PLUGIN_DISPLAY_NAME`: The human-readable display name. This is set for persistent and on-call plugins.
- `TARRAGON_PLUGIN_PREFIX`: The plugin's resolved user-facing prefix, or empty
  when it has no single prefix. This is set for persistent and on-call plugins.
- `TARRAGON_PREFIX_SYMBOL`: The configured global prefix symbol.

`TARRAGON_PLUGINS_ENDPOINT` is set only for persistent plugins. On-call plugins
receive the identity and prefix variables above when invoked for a query or
selection.

### Socket Locations

The daemon resolves the plugins socket path at startup, in order:

1. `TARRAGON_PLUGINS_SOCKET`, if set and non-empty, is used verbatim.
2. Otherwise, if `XDG_RUNTIME_DIR` is set and non-empty:
   `$XDG_RUNTIME_DIR/tarragon/plugins.sock`.
3. Otherwise: `/tmp/tarragon-<euid>/plugins.sock` (`<euid>` is the daemon's
   effective UID).

The daemon creates the parent directory with mode `0700` before listening, and
removes a stale leftover socket file if one is present. Persistent plugins
should not hardcode a socket path; always read `TARRAGON_PLUGINS_ENDPOINT`.

### Python skeleton

```python
import json, os, socket

endpoint = os.environ["TARRAGON_PLUGINS_ENDPOINT"]  # always set by the daemon for persistent plugins
plugin_id = os.environ.get("TARRAGON_PLUGIN_ID", "my_plugin")

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(endpoint)
s.sendall(json.dumps({"type": "hello", "name": plugin_id}).encode() + b"\n")

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

// Always set by the daemon for persistent plugins.
let endpoint = std::env::var("TARRAGON_PLUGINS_ENDPOINT")?;
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

If your plugin uses `lifecycle_mode = "on_call"`, Tarragon invokes CLI subcommands on the plugin executable under the `tarragon` namespace:

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

An action can request query replacement instead of plugin selection:

```json
{
  "name": "Episodes",
  "type": "query_replace",
  "query": "@anime episodes 154587"
}
```

The UI replaces its input with `query`, remains open, and submits a normal
query request. It must not send a select request for this action. Actions
without `type: "query_replace"` retain the existing select behavior.

An action can also keep the UI open after a successful normal selection:

```json
{
  "name": "Copy",
  "type": "keep_open"
}
```

Action types have these semantics:

- no `type`: send a normal select request; the UI dismisses after success.
- `type: "keep_open"`: send a normal select request; the UI remains open after success.
- `type: "query_replace"`: replace the query and submit it normally; the UI remains open.

The UI should remain open when an action fails so the user can retry. The
`keep_open` type is a UI hint, and the UI may apply its own user-level policy.

## Makefile Specification (Required)

Your Makefile must define the following targets:

```
check-deps:   # verify required toolchain (e.g., python3 or cargo/rustc)
install:      # build and copy files into ~/.local/lib/tarragon/plugins/<plugin-id>/
uninstall:    # remove installed files from the plugin directory
run:          # local quick test (e.g., ./my_plugin tarragon query "Hello")
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
