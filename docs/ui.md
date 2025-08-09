# Building a UI for Tarragon

Disclaimer: This documentation was created with the help of an AI assistant. It may contain inaccuracies or omissions; please validate details and test in your environment.

This guide explains how to build a UI that talks to the Tarragon daemon over ZeroMQ.

## Responsibilities of a UI

- Create a persistent REQ socket to send queries and receive an ACK with a `query_id`.
- Create a persistent SUB socket to receive updates. Subscribe to the `query_id` topic from the ACK.
- Render the latest aggregate snapshot per active query.
- Optionally support a `detach` control to ask the daemon to purge your aggregates when the UI exits.

## Endpoints

- REQ/REP (UI → Daemon): `ipc:///tmp/tarragon-ui.ipc`
- PUB/SUB (Daemon → UI): `ipc:///tmp/tarragon-updates.ipc`

## Minimal Flow

1) Connect REQ to UI endpoint.
2) Connect SUB to updates endpoint.
3) For each input:
   - Send `{ "type": "query", "client_id": "<id>", "text": "<input>" }` over REQ.
   - Receive `{ "type": "ack", "query_id": "..." }`.
   - Subscribe SUB to the `query_id` topic.
   - In a background loop, read SUB frames: `[topic, payload]` where `payload` decodes to `{ "type": "update", "query_id": "...", "payload": <AggregateSnapshot JSON> }`.
   - Replace your displayed snapshot with the latest one for that `query_id`.
4) On exit, send `{ "type": "detach", "client_id": "<id>" }` to REQ (best‑effort) to let the daemon purge memory.

## Tips

- Concurrency: Multiple queries can run concurrently; you can subscribe to multiple `query_id`s or focus on one.
- Slow-joiner: The daemon delays briefly after ACK to allow SUB to attach.
- Snapshots: Each update contains the full current aggregate (no delta merging needed).

## Message Shapes

- Query (UI → Daemon, REQ):
  - `{ "type": "query", "client_id": "<string>", "text": "<input>" }`
- Detach (UI → Daemon, REQ):
  - `{ "type": "detach", "client_id": "<string>" }`
- Ack (Daemon → UI, REP):
  - `{ "type": "ack", "query_id": "<id>" }`
- Update (Daemon → UI, PUB payload):
  - `{ "type": "update", "query_id": "<id>", "payload": <AggregateSnapshot JSON> }`
- AggregateSnapshot:
  - `{"query_id":"<id>","input":"<input>","results":{"<plugin>":{"elapsed_ms":<float>,"data":<plugin JSON>}, ...}}`

## Example: Python (pyzmq)

```
import json, time, zmq, uuid

CTX = zmq.Context.instance()
req = CTX.socket(zmq.REQ)
req.connect("ipc:///tmp/tarragon-ui.ipc")
sub = CTX.socket(zmq.SUB)
sub.connect("ipc:///tmp/tarragon-updates.ipc")

client_id = f"cli-{uuid.uuid4()}"

def query(text: str):
    req.send_json({"type":"query","client_id":client_id,"text":text})
    ack = req.recv_json()
    qid = ack["query_id"]
    sub.setsockopt_string(zmq.SUBSCRIBE, qid)
    return qid

qid = query("hello")
poller = zmq.Poller(); poller.register(sub, zmq.POLLIN)
while True:
    evts = dict(poller.poll(1000))
    if sub in evts:
        topic, payload = sub.recv_multipart()
        upd = json.loads(payload)
        snap = json.loads(upd["payload"])  # AggregateSnapshot
        print("snapshot:", json.dumps(snap, indent=2))
```

## Example: Go (go-zeromq/zmq4)

```
ctx := context.Background()
req := zmq4.NewReq(ctx); _ = req.Dial("ipc:///tmp/tarragon-ui.ipc")
sub := zmq4.NewSub(ctx); _ = sub.Dial("ipc:///tmp/tarragon-updates.ipc")

body, _ := json.Marshal(map[string]any{"type":"query","client_id":"cli-1","text":"hello"})
_ = req.Send(zmq4.NewMsg(body))
ack, _ := req.Recv()
var a struct{ QueryID string `json:"query_id"` }
_ = json.Unmarshal(ack.Frames[0], &a)
_ = sub.SetOption(zmq4.OptionSubscribe, a.QueryID)

msg, _ := sub.Recv()
var upd struct{ Payload json.RawMessage `json:"payload"` }
_ = json.Unmarshal(msg.Frames[1], &upd)
```
