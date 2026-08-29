package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestUIAckRoundTrip(t *testing.T) {
	ack := AckMessage{Type: "ack", QueryID: "q-1"}
	b, err := json.Marshal(ack)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got AckMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != "ack" || got.QueryID != "q-1" {
		t.Fatalf("unexpected ack: %+v", got)
	}
}

func TestPluginReqRespRoundTrip(t *testing.T) {
	req := PluginRequest{Type: MsgRequest, QueryID: "q-2", Text: "hello"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	var req2 PluginRequest
	if err := json.Unmarshal(b, &req2); err != nil {
		t.Fatalf("unmarshal req: %v", err)
	}
	if req2.Type != MsgRequest || req2.QueryID != "q-2" || req2.Text != "hello" {
		t.Fatalf("unexpected req2: %+v", req2)
	}

	resp := PluginResponse{Type: MsgResponse, QueryID: "q-2", Data: json.RawMessage(`{"ok":true}`)}
	b2, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal resp: %v", err)
	}
	var resp2 PluginResponse
	if err := json.Unmarshal(b2, &resp2); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if resp2.Type != MsgResponse || resp2.QueryID != "q-2" || string(resp2.Data) != `{"ok":true}` {
		t.Fatalf("unexpected resp2: %+v", resp2)
	}
}

func TestWriteReadMsgRoundTrip(t *testing.T) {
	buf := &bytes.Buffer{}
	in := &UIRequest{Type: "query", ClientID: "c1", Text: "hello"}
	if err := WriteMsg(buf, in); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := NewScanner(buf)
	var out UIRequest
	if err := ReadMsg(s, &out); err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.Type != in.Type || out.ClientID != in.ClientID || out.Text != in.Text {
		t.Fatalf("unexpected roundtrip: %+v", out)
	}

	if err := ReadMsg(s, &out); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestQueryReplacementActionRoundTrip(t *testing.T) {
	in := &Action{Name: "Episodes", Type: ActionTypeQueryReplace, Query: "@anime episodes 154587"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}

	var out Action
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal action: %v", err)
	}
	if out != *in {
		t.Fatalf("action changed across wire round trip: got %+v, want %+v", out, *in)
	}
}

func TestKeepOpenActionRoundTrip(t *testing.T) {
	in := &Action{Name: "Copy", Type: ActionTypeKeepOpen, Default: true}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}

	var out Action
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal action: %v", err)
	}
	if out != *in {
		t.Fatalf("action changed across wire round trip: got %+v, want %+v", out, *in)
	}
}

func TestCleanupSocketRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.sock")
	ln, err := ListenUnix(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if err := CleanupSocket(path); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected removed, stat err=%v", err)
	}
}

func TestListenUnix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l.sock")
	ln, err := ListenUnix(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close()
}

func TestListenUnixCreatesDirWithSecureMode(t *testing.T) {
	dir := t.TempDir()
	sockDir := filepath.Join(dir, "tarragon")
	path := filepath.Join(sockDir, "ui.sock")

	ln, err := ListenUnix(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	fi, err := os.Stat(sockDir)
	if err != nil {
		t.Fatalf("stat socket dir: %v", err)
	}
	if !fi.IsDir() {
		t.Fatalf("expected %s to be a directory", sockDir)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("expected socket dir mode 0700, got %o", perm)
	}
}

func TestListenUnixRemovesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.sock")

	// Simulate a stale socket file left behind by a crashed daemon: a real
	// unix listener whose socket file is not unlinked on close.
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatalf("resolve addr: %v", err)
	}
	stale, err := net.ListenUnix("unix", addr)
	if err != nil {
		t.Fatalf("create stale listener: %v", err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatalf("close stale listener: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected stale socket file to remain, stat err=%v", err)
	}

	second, err := ListenUnix(path)
	if err != nil {
		t.Fatalf("second listen should remove stale socket and succeed: %v", err)
	}
	_ = second.Close()
}

func TestResolveUISocketPath(t *testing.T) {
	t.Setenv(envUISocket, "")
	t.Setenv(envXDGRuntimeDir, "")

	t.Run("env override wins", func(t *testing.T) {
		t.Setenv(envUISocket, "/custom/ui.sock")
		if got := ResolveUISocketPath(); got != "/custom/ui.sock" {
			t.Fatalf("expected override path, got %q", got)
		}
	})

	t.Run("xdg runtime dir when no override", func(t *testing.T) {
		t.Setenv(envUISocket, "")
		t.Setenv(envXDGRuntimeDir, "/run/user/1000")
		want := "/run/user/1000/tarragon/ui.sock"
		if got := ResolveUISocketPath(); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("fallback to euid tmp dir", func(t *testing.T) {
		t.Setenv(envUISocket, "")
		t.Setenv(envXDGRuntimeDir, "")
		want := fmt.Sprintf("/tmp/tarragon-%d/ui.sock", os.Geteuid())
		if got := ResolveUISocketPath(); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})
}

func TestResolvePluginsSocketPath(t *testing.T) {
	t.Setenv(envPluginsSocket, "")
	t.Setenv(envXDGRuntimeDir, "")

	t.Run("env override wins", func(t *testing.T) {
		t.Setenv(envPluginsSocket, "/custom/plugins.sock")
		if got := ResolvePluginsSocketPath(); got != "/custom/plugins.sock" {
			t.Fatalf("expected override path, got %q", got)
		}
	})

	t.Run("xdg runtime dir when no override", func(t *testing.T) {
		t.Setenv(envPluginsSocket, "")
		t.Setenv(envXDGRuntimeDir, "/run/user/1000")
		want := "/run/user/1000/tarragon/plugins.sock"
		if got := ResolvePluginsSocketPath(); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("fallback to euid tmp dir", func(t *testing.T) {
		t.Setenv(envPluginsSocket, "")
		t.Setenv(envXDGRuntimeDir, "")
		want := fmt.Sprintf("/tmp/tarragon-%d/plugins.sock", os.Geteuid())
		if got := ResolvePluginsSocketPath(); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})
}
