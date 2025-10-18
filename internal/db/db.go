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

// DefaultPath returns the default SQLite DB path under XDG *state* dir.
// Example: ~/.local/state/tarragon/tarragon.db
func DefaultPath() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "tarragon", "tarragon.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "tarragon.db"
	}
	return filepath.Join(home, ".local", "state", "tarragon", "tarragon.db")
}

// Open opens (and initializes) the SQLite database at path.
// If path is empty (or explicitly ""), it uses DefaultPath().
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
	// Pragmas tuned for app DB use.
	if _, err := sqlDB.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA foreign_keys=ON;
		PRAGMA busy_timeout=5000;
		PRAGMA synchronous=NORMAL;
	`); err != nil {
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

// Init applies/updates the schema used by Tarragon.
// - Uses a schema_version table for simple migrations.
// - Frecency table uses (plugin,value,context) composite PK with nullable wildcards.
func (d *DB) Init(ctx context.Context) error {
	if d == nil || d.SQL == nil {
		return errors.New("nil DB")
	}

	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Ensure schema_version row exists
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_version (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL
		);
		INSERT INTO schema_version (id, version)
		SELECT 1, 0
		WHERE NOT EXISTS (SELECT 1 FROM schema_version WHERE id = 1);
	`); err != nil {
		return fmt.Errorf("schema_version init: %w", err)
	}

	// Read current version
	var ver int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_version WHERE id=1`).Scan(&ver); err != nil {
		return fmt.Errorf("read schema_version: %w", err)
	}

	// Create (or ensure) current tables (idempotent)
	stmts := []string{
		// Frecency (multi-parameter, nullable wildcards).
		`CREATE TABLE IF NOT EXISTS frecency (
			plugin        TEXT,                   -- NULL = wildcard
			value         TEXT,                   -- NULL = wildcard
			context       TEXT,                   -- NULL = wildcard
			score         REAL    NOT NULL DEFAULT 0.0, -- lazily decayed score
			updated_at    INTEGER NOT NULL DEFAULT 0,   -- unix seconds when score last decayed
			last_seen_ts  INTEGER NOT NULL DEFAULT 0,
			last_exec_ts  INTEGER NOT NULL DEFAULT 0,
			exec_count    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (plugin, value, context)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_frecency_plugin ON frecency(plugin);`,
		`CREATE INDEX IF NOT EXISTS idx_frecency_value  ON frecency(value);`,
		`CREATE INDEX IF NOT EXISTS idx_frecency_ctx    ON frecency(context);`,

		// Metrics sink
		`CREATE TABLE IF NOT EXISTS metrics (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			ts   INTEGER NOT NULL,
			type TEXT    NOT NULL,
			data TEXT    NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_type_ts ON metrics(type, ts DESC);`,
	}
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
