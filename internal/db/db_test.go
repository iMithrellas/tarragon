package db

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestRecordSelectionAndGetFrecencyScores(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.db")
	d, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	ctx := context.Background()
	if err := d.RecordSelection(ctx, "plug", "item"); err != nil {
		t.Fatalf("RecordSelection first: %v", err)
	}
	if err := d.RecordSelection(ctx, "plug", "item"); err != nil {
		t.Fatalf("RecordSelection second: %v", err)
	}

	var count int
	if err := d.SQL.QueryRowContext(ctx, `SELECT exec_count FROM frecency WHERE plugin=? AND value=? AND context=''`, "plug", "item").Scan(&count); err != nil {
		t.Fatalf("query exec_count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected exec_count=2 got=%d", count)
	}

	scores, err := d.GetFrecencyScores(ctx)
	if err != nil {
		t.Fatalf("GetFrecencyScores: %v", err)
	}
	if scores["plug:item"] <= 0 {
		t.Fatalf("expected positive frecency score, got %v", scores["plug:item"])
	}
}

func TestGetFrecencyScoresAppliesDecay(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.db")
	d, err := Open(p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	now := time.Now().Unix()
	twoDaysAgo := now - 2*24*60*60
	if _, err := d.SQL.ExecContext(context.Background(), `
		INSERT INTO frecency(plugin, value, context, exec_count, last_exec_ts)
		VALUES(?, ?, '', ?, ?)
	`, "plug", "aged", 3, twoDaysAgo); err != nil {
		t.Fatalf("seed frecency row: %v", err)
	}

	scores, err := d.GetFrecencyScores(context.Background())
	if err != nil {
		t.Fatalf("GetFrecencyScores: %v", err)
	}

	got := scores["plug:aged"]
	want := 3.0 * math.Exp(-0.1*2.0)
	if math.Abs(got-want) > 0.05 {
		t.Fatalf("unexpected decayed score: got=%v want~=%v", got, want)
	}
}
