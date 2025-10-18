package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection.
type DB struct {
	SQL *sql.DB
}

// DefaultPath returns the default SQLite DB path under XDG data dir.
// Example: ~/.local/share/tarragon/tarragon.db
func DefaultPath() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "tarragon", "tarragon.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "tarragon.db"
	}
	return filepath.Join(home, ".local", "share", "tarragon", "tarragon.db")
}

// Open opens (and initializes) the SQLite database at path.
// If path is empty, it uses DefaultPath().
func Open(path string) (*DB, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating db dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Pragmas to improve robustness for an app DB.
	if _, err := sqlDB.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA synchronous=NORMAL;`); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	d := &DB{SQL: sqlDB}
	if err := d.Init(context.Background()); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// Init applies the minimal schema used by Tarragon.
// It creates tables for frecency and JSON metrics.
func (d *DB) Init(ctx context.Context) error {
	if d == nil || d.SQL == nil {
		return errors.New("nil DB")
	}
	stmts := []string{
		// Optional schema versioning table for future migrations.
		`CREATE TABLE IF NOT EXISTS schema_version (
            id INTEGER PRIMARY KEY CHECK (id = 1),
            version INTEGER NOT NULL
        );`,
		`INSERT INTO schema_version (id, version)
            SELECT 1, 1 WHERE NOT EXISTS (SELECT 1 FROM schema_version WHERE id = 1);`,

		// Frecency storage skeleton. Exact scoring policy left to caller.
		`CREATE TABLE IF NOT EXISTS frecency (
            key TEXT PRIMARY KEY,
            category TEXT NOT NULL DEFAULT '',
            count INTEGER NOT NULL DEFAULT 0,
            last_seen_ts INTEGER NOT NULL DEFAULT 0,
            last_exec_ts INTEGER NOT NULL DEFAULT 0,
            score REAL NOT NULL DEFAULT 0.0
        );`,
		`CREATE INDEX IF NOT EXISTS idx_frecency_score ON frecency(score DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_frecency_category ON frecency(category);`,

		// JSON metrics sink: arbitrary JSON payloads with a type tag and timestamp.
		`CREATE TABLE IF NOT EXISTS metrics (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            ts INTEGER NOT NULL,
            type TEXT NOT NULL,
            data TEXT NOT NULL
        );`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_type_ts ON metrics(type, ts DESC);`,
	}
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		// In case of panic, rollback.
		_ = tx.Rollback()
	}()
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Close closes the underlying DB.
func (d *DB) Close() error {
	if d == nil || d.SQL == nil {
		return nil
	}
	return d.SQL.Close()
}

// InsertMetric stores a JSON metric with the provided type tag and current timestamp.
// Data must be a JSON string; use json.Marshal for structured values.
func (d *DB) InsertMetric(ctx context.Context, typ string, jsonData string) error {
	if d == nil || d.SQL == nil {
		return errors.New("nil DB")
	}
	_, err := d.SQL.ExecContext(ctx,
		`INSERT INTO metrics(ts, type, data) VALUES(?, ?, ?);`,
		time.Now().Unix(), typ, jsonData,
	)
	if err != nil {
		return fmt.Errorf("insert metric: %w", err)
	}
	return nil
}
