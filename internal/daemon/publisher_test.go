package daemon

import "testing"

func TestEnqueuePub(t *testing.T) {
	// Case 1: nil channel should not panic
	pubEnqueue = nil
	enqueuePub("topic", []byte("data"))

	// Case 2: valid channel receives a frame
	ch := make(chan pubFrame, 1)
	old := pubEnqueue
	pubEnqueue = ch
	defer func() { pubEnqueue = old }()

	enqueuePub("t1", []byte("p1"))
	select {
	case f := <-ch:
		if f.topic != "t1" || string(f.payload) != "p1" {
			t.Fatalf("unexpected frame: %+v", f)
		}
	default:
		t.Fatalf("expected frame enqueued")
	}
}
