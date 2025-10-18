package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaAtPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.db")
	d, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("db file missing: %v", err)
	}

	// Verify tables exist via sqlite_master
	var n int
	if err := d.SQL.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name IN ('frecency','metrics')`).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if n < 2 {
		t.Fatalf("expected both tables present, got %d", n)
	}

	// Insert a metric and check it lands
	if err := d.InsertMetric(context.Background(), "test", `{"ok":true}`); err != nil {
		t.Fatalf("InsertMetric: %v", err)
	}
	if err := d.SQL.QueryRow(`SELECT COUNT(1) FROM metrics WHERE type='test'`).Scan(&n); err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 metric, got %d", n)
	}
}

func TestOpenDefaultPathUsesXDG(t *testing.T) {
	dir := t.TempDir()
	// Ensure XDG_DATA_HOME directs DefaultPath()
	t.Setenv("XDG_STATE_HOME", dir)
	def := DefaultPath()
	// Sanity: must live under our temp dir
	if got, want := filepath.Dir(filepath.Dir(def)), dir; got != want && filepath.Dir(def) != dir {
		t.Fatalf("DefaultPath not under temp XDG: %q", def)
	}

	d, err := Open("")
	if err != nil {
		t.Fatalf("Open with XDG default path: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, err := os.Stat(def); err != nil {
		t.Fatalf("db file missing at default path: %v (path=%s)", err, def)
	}
}
