package wire

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}

func TestWriteReadMsgRoundTripAck(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	want := AckMessage{Type: "ack", QueryID: "q-1"}
	if err := WriteMsg(&b, want); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}

	scanner := NewScanner(strings.NewReader(b.String()))
	var got AckMessage
	if err := ReadMsg(scanner, &got); err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	if got != want {
		t.Fatalf("unexpected ack: got %+v want %+v", got, want)
	}
}

func TestWriteReadMsgRoundTripPluginResponse(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	want := PluginResponse{Type: MsgResponse, QueryID: "q-2", Data: json.RawMessage(`{"ok":true}`)}
	if err := WriteMsg(&b, want); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}

	scanner := NewScanner(strings.NewReader(b.String()))
	var got PluginResponse
	if err := ReadMsg(scanner, &got); err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	if got.Type != want.Type || got.QueryID != want.QueryID || string(got.Data) != string(want.Data) {
		t.Fatalf("unexpected response: got %+v want %+v", got, want)
	}
}

func TestWriteMsgConcurrent(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	w := &lockedWriter{w: &b}
	const n = 20

	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			if err := WriteMsg(w, AckMessage{Type: "ack", QueryID: string(rune('a' + i))}); err != nil {
				t.Errorf("WriteMsg(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != n {
		t.Fatalf("expected %d lines, got %d", n, len(lines))
	}
	for i, line := range lines {
		var ack AckMessage
		if err := json.Unmarshal([]byte(line), &ack); err != nil {
			t.Fatalf("line %d not valid json: %v", i, err)
		}
	}
}

func TestReadMsgEmptyJSONLine(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(strings.NewReader("{}\n"))
	var got map[string]any
	if err := ReadMsg(scanner, &got); err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %#v", got)
	}
}

func TestNewScannerLargePayload(t *testing.T) {
	t.Parallel()

	// Keep the encoded line just below 1MB scanner limit.
	raw := strings.Repeat("a", 700*1024)
	msg := map[string]string{"payload": base64.StdEncoding.EncodeToString([]byte(raw))}

	var b strings.Builder
	if err := WriteMsg(&b, msg); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}

	scanner := NewScanner(strings.NewReader(b.String()))
	var got map[string]string
	if err := ReadMsg(scanner, &got); err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if got["payload"] != msg["payload"] {
		t.Fatalf("payload mismatch")
	}
}

func TestReadMsgEOF(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(strings.NewReader(""))
	var got AckMessage
	err := ReadMsg(scanner, &got)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestCleanupSocketRemovesExistingSocket(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sock")

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	if err := CleanupSocket(path); err != nil {
		t.Fatalf("CleanupSocket: %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected socket file removed, stat err: %v", err)
	}
}

func TestCleanupSocketRejectsNonSocket(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-socket")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := CleanupSocket(path); err == nil {
		t.Fatal("expected error for non-socket path")
	}
}

func TestListenUnixCreatesListenerAndCleansStaleSocket(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "wire.sock")

	ln1, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("create stale listener: %v", err)
	}
	if err := ln1.Close(); err != nil {
		t.Fatalf("close stale listener: %v", err)
	}

	ln2, err := ListenUnix(path)
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	t.Cleanup(func() { _ = ln2.Close() })

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected socket to exist: %v", err)
	}
}
