# Building a UI for Tarragon

Disclaimer: This documentation was created with the help of an AI assistant. It may contain inaccuracies or omissions; please validate details and test in your environment.

This guide explains how to build a UI that talks to the Tarragon daemon over a Unix Domain Socket using NDJSON framing.

## Responsibilities of a UI

- Create a single persistent bidirectional Unix Domain Socket connection to the daemon.
- Send queries and read acknowledgements/updates on that same connection.
- Render the latest aggregate snapshot per active query.
- Optionally support a `detach` control to ask the daemon to purge your aggregates when the UI exits.

## Endpoints

- UI ↔ Daemon (single bidirectional connection): `/tmp/tarragon-ui.sock`

## Minimal Flow

1) Connect to Unix socket at `/tmp/tarragon-ui.sock`.
2) For each input:
   - Write NDJSON line: `{ "type": "query", "client_id": "<id>", "text": "<input>" }\n`
   - Read NDJSON line: `{ "type": "ack", "query_id": "..." }\n`
   - Read NDJSON update lines: `{ "type": "update", "query_id": "...", "payload": <snapshot> }\n`
   - Replace your displayed snapshot with the latest one for that `query_id`.
3) On exit, write NDJSON line `{ "type": "detach", "client_id": "<id>" }\n` (best‑effort) to let the daemon purge memory.

NDJSON framing means each message is exactly one JSON object on one line, terminated by `\n`.

## Tips

- Concurrency: Multiple queries can run concurrently on the same connection.
- Snapshots: Each update contains the full current aggregate (no delta merging needed).

## Message Shapes

- Query (UI → Daemon, NDJSON line):
  - `{ "type": "query", "client_id": "<string>", "text": "<input>" }`
- Detach (UI → Daemon, NDJSON line):
  - `{ "type": "detach", "client_id": "<string>" }`
- Ack (Daemon → UI, NDJSON line):
  - `{ "type": "ack", "query_id": "<id>" }`
- Update (Daemon → UI, NDJSON line):
  - `{ "type": "update", "query_id": "<id>", "payload": <AggregateSnapshot JSON> }`
- AggregateSnapshot:
  - `{"query_id":"<id>","input":"<input>","results":{"<plugin>":{"elapsed_ms":<float>,"data":<plugin JSON>}, ...}}`

## Example: Python

```python
import json, socket, uuid

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect("/tmp/tarragon-ui.sock")
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
        print("snapshot:", json.dumps(json.loads(msg["payload"]), indent=2))
```

## Example: Go

```
conn, _ := net.Dial("unix", "/tmp/tarragon-ui.sock")
scanner := bufio.NewScanner(conn)

query, _ := json.Marshal(map[string]any{"type":"query","client_id":"cli-1","text":"hello"})
conn.Write(append(query, '\n'))

scanner.Scan()
var a struct{ QueryID string `json:"query_id"` }
json.Unmarshal(scanner.Bytes(), &a)

scanner.Scan()
var upd struct{ Payload json.RawMessage `json:"payload"` }
json.Unmarshal(scanner.Bytes(), &upd)
```
