# Building a UI for Tarragon

Disclaimer: This documentation was created with the help of an AI assistant. It may contain inaccuracies or omissions; please validate details and test in your environment.

This guide explains how to build a UI that talks to the Tarragon daemon over a Unix Domain Socket using NDJSON framing.

## Responsibilities of a UI

- Create a single persistent bidirectional Unix Domain Socket connection to the daemon.
- Send queries and read acknowledgements/updates on that same connection.
- Render the latest aggregate snapshot per active query.
- Use per-plugin query state metadata to show which plugins are still pending, returned no entries, or failed.
- Optionally support a `detach` control to ask the daemon to purge your aggregates when the UI exits.

## Endpoints

- UI ↔ Daemon (single bidirectional connection): resolved by the daemon at
  startup (see "Socket Location" below).

### Socket Location

The daemon resolves the UI socket path at startup, in order:

1. `TARRAGON_UI_SOCKET`, if set and non-empty, is used verbatim.
2. Otherwise, if `XDG_RUNTIME_DIR` is set and non-empty:
   `$XDG_RUNTIME_DIR/tarragon/ui.sock`.
3. Otherwise: `/tmp/tarragon-<euid>/ui.sock` (`<euid>` is the daemon's
   effective UID).

The daemon creates the parent directory with mode `0700` before listening, and
removes a stale leftover socket file if one is present. UI clients should
implement the same resolution order (or read `TARRAGON_UI_SOCKET` directly)
rather than hardcoding a path.

## Minimal Flow

1) Connect to the resolved UI Unix socket path (see above).
2) For each input:
   - Write NDJSON line: `{ "type": "query", "client_id": "<id>", "text": "<input>" }\n`
   - Read NDJSON line: `{ "type": "ack", "query_id": "..." }\n`
   - Read NDJSON update lines: `{ "type": "update", "query_id": "...", "payload": "<base64>" }\n`
   - Base64-decode `payload` and parse the result as JSON to get the aggregate snapshot.
   - Replace your displayed snapshot with the latest one for that `query_id`.
3) On exit, write NDJSON line `{ "type": "detach", "client_id": "<id>" }\n` (best-effort) to let the daemon purge memory.

### Query Replacement Actions

An action may use `type: "query_replace"` and provide a `query`:

```json
{
  "name": "Episodes",
  "type": "query_replace",
  "query": "@anime episodes 154587"
}
```

When the user invokes this action, replace the input text with `query` and
send it as a normal query request. Keep the socket and UI open; process the
acknowledgement and updates exactly as for any other query. Do not send a
select request for this action. Actions without this type retain the existing
select behavior.

NDJSON framing means each message is exactly one JSON object on one line, terminated by `\n`.

## Tips

- Concurrency: Multiple queries can run concurrently on the same connection.
- Snapshots: Each update contains the full current aggregate (no delta merging needed).
- Plugin progress: the aggregate snapshot includes a `plugins` map keyed by plugin name. UIs can render pending/empty/error states and compute pending elapsed time from `started_at_unix_ms`.

### Lifecycle-aware availability

When consuming daemon status metadata:

- treat `connected` as current IPC attachment state (primarily relevant for persistent lifecycles),
- do not treat `connected=false` as unavailable for enabled `on_call` plugins.

`on_call` plugins are expected to be dispatchable without a persistent connection.

## Message Shapes

- Query (UI → Daemon, NDJSON line):
  - `{ "type": "query", "client_id": "<string>", "text": "<input>" }`
- Detach (UI → Daemon, NDJSON line):
  - `{ "type": "detach", "client_id": "<string>" }`
- Ack (Daemon → UI, NDJSON line):
  - `{ "type": "ack", "query_id": "<id>" }`
- Update (Daemon → UI, NDJSON line):
  - `{ "type": "update", "query_id": "<id>", "payload": "<base64-encoded AggregateSnapshot JSON>" }`
  - `payload` is Tarragon's Go `[]byte` field, which `encoding/json` marshals as a
    standard base64 string, **not** an inline JSON object. Base64-decode it
    first, then parse the decoded bytes as JSON to get the AggregateSnapshot.
- AggregateSnapshot (after base64-decoding `payload`):
  - `{"query_id":"<id>","input":"<input>","started_at_unix_ms":<epoch-ms>,"results":{"<plugin>":{"elapsed_ms":<float>,"data":<plugin JSON>}, ...},"plugins":{"<plugin>":{"state":"pending|done|empty|error","count":<int>,"elapsed_ms":<float>,"error":"<message>"}},"list":[...]}`

Plugin state meanings:
- `pending`: the plugin was selected for this query and has not responded yet.
- `done`: the plugin responded with one or more normalized result entries.
- `empty`: the plugin responded successfully but produced no normalized result entries.
- `error`: the plugin failed, timed out, or returned an error payload.

Result actions may also include `type` and `query`. The `query_replace` type
uses `query` as the replacement input and starts a normal query.

## Example: Python

```python
import base64, json, os, socket, uuid


def resolve_ui_socket_path() -> str:
    if override := os.environ.get("TARRAGON_UI_SOCKET"):
        return override
    if runtime_dir := os.environ.get("XDG_RUNTIME_DIR"):
        return os.path.join(runtime_dir, "tarragon", "ui.sock")
    return f"/tmp/tarragon-{os.geteuid()}/ui.sock"


s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(resolve_ui_socket_path())
f = s.makefile('r')

client_id = f"cli-{uuid.uuid4()}"

query = {"type": "query", "client_id": client_id, "text": "hello"}
s.sendall(json.dumps(query).encode() + b"\n")

while True:
    line = f.readline()
    if not line:
        break
    msg = json.loads(line)
    if msg["type"] == "ack":
        print("query_id:", msg["query_id"])
    elif msg["type"] == "update":
        # payload is base64-encoded JSON; decode before parsing.
        snapshot = json.loads(base64.b64decode(msg["payload"]))
        print("snapshot:", json.dumps(snapshot, indent=2))
```

## Example: Go

```
func resolveUISocketPath() string {
	if v := os.Getenv("TARRAGON_UI_SOCKET"); v != "" {
		return v
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "tarragon", "ui.sock")
	}
	return fmt.Sprintf("/tmp/tarragon-%d/ui.sock", os.Geteuid())
}

conn, _ := net.Dial("unix", resolveUISocketPath())
scanner := bufio.NewScanner(conn)

query, _ := json.Marshal(map[string]any{"type":"query","client_id":"cli-1","text":"hello"})
conn.Write(append(query, '\n'))

scanner.Scan()
var a struct{ QueryID string `json:"query_id"` }
json.Unmarshal(scanner.Bytes(), &a)

scanner.Scan()
// Payload is []byte: encoding/json base64-decodes it automatically because
// the wire value is a base64 string, not an inline JSON object.
var upd struct{ Payload []byte `json:"payload"` }
json.Unmarshal(scanner.Bytes(), &upd)

var snapshot map[string]any
json.Unmarshal(upd.Payload, &snapshot)
```
