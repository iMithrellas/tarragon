package daemon

import "github.com/iMithrellas/tarragon/internal/wire"

// publishToUI forwards an update to all connected UI clients.
func publishToUI(reg *uiRegistry, qid string, payload []byte) {
	if reg == nil {
		return
	}
	reg.publish(&wire.UpdateMessage{Type: "update", QueryID: qid, Payload: payload})
}
