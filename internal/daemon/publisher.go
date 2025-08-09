package daemon

// PUB socket serialization and enqueue helpers

type pubFrame struct {
	topic   string
	payload []byte
}

// Global publisher enqueue channel (initialized in reqServer)
var pubEnqueue chan pubFrame

func enqueuePub(topic string, payload []byte) {
	if pubEnqueue != nil {
		pubEnqueue <- pubFrame{topic: topic, payload: payload}
	}
}
