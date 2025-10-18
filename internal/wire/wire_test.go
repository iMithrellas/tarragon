package wire

import (
    "encoding/json"
    "testing"
)

func TestUIAckRoundTrip(t *testing.T) {
    ack := AckMessage{Type: "ack", QueryID: "q-1"}
    b, err := json.Marshal(ack)
    if err != nil { t.Fatalf("marshal: %v", err) }
    var got AckMessage
    if err := json.Unmarshal(b, &got); err != nil { t.Fatalf("unmarshal: %v", err) }
    if got.Type != "ack" || got.QueryID != "q-1" {
        t.Fatalf("unexpected ack: %+v", got)
    }
}

func TestPluginReqRespRoundTrip(t *testing.T) {
    req := PluginRequest{Type: MsgRequest, QueryID: "q-2", Text: "hello"}
    b, err := json.Marshal(req)
    if err != nil { t.Fatalf("marshal req: %v", err) }
    var req2 PluginRequest
    if err := json.Unmarshal(b, &req2); err != nil { t.Fatalf("unmarshal req: %v", err) }
    if req2.Type != MsgRequest || req2.QueryID != "q-2" || req2.Text != "hello" {
        t.Fatalf("unexpected req2: %+v", req2)
    }

    resp := PluginResponse{Type: MsgResponse, QueryID: "q-2", Data: json.RawMessage(`{"ok":true}`)}
    b2, err := json.Marshal(resp)
    if err != nil { t.Fatalf("marshal resp: %v", err) }
    var resp2 PluginResponse
    if err := json.Unmarshal(b2, &resp2); err != nil { t.Fatalf("unmarshal resp: %v", err) }
    if resp2.Type != MsgResponse || resp2.QueryID != "q-2" || string(resp2.Data) != `{"ok":true}` {
        t.Fatalf("unexpected resp2: %+v", resp2)
    }
}

