package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEscapeJSONString(t *testing.T) {
	in := "a\"b\\c\n\t0"
	esc := escapeJSONString(in)
	if len(esc) == 0 || esc == in {
		t.Fatalf("expected escaped string to differ")
	}
}

func TestCleanupIPCRemovesStaleFile(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "sock.ipc")
	// Create a stale file
	if err := os.WriteFile(sock, []byte("x"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	cleanupIPC("ipc://" + sock)
	if _, err := os.Stat(sock); err == nil || !os.IsNotExist(err) {
		t.Fatalf("expected %s removed, err=%v", sock, err)
	}
}
