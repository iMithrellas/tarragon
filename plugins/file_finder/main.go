package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	defaultPluginName = "file_finder"
	maxResults        = 20
	batchSize         = 1000
	rescanInterval    = 5 * time.Minute
)

type pluginMessage struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	QueryID  string `json:"query_id,omitempty"`
	Text     string `json:"text,omitempty"`
	ResultID string `json:"result_id,omitempty"`
	Action   string `json:"action,omitempty"`
	Data     any    `json:"data,omitempty"`
}

type responseData struct {
	Results []resultItem `json:"results"`
}

type actionItem struct {
	Name        string `json:"name"`
	Default     bool   `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

type resultItem struct {
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	Description string       `json:"description,omitempty"`
	Icon        string       `json:"icon,omitempty"`
	Category    string       `json:"category,omitempty"`
	PreviewPath string       `json:"preview_path,omitempty"`
	Score       float64      `json:"score"`
	Actions     []actionItem `json:"actions,omitempty"`
}

type selectResponse struct {
	Type    string `json:"type"`
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type fileRow struct {
	Path     string
	Filename string
	Size     int64
}

type indexer struct {
	db   *sql.DB
	dirs []string
	mu   sync.RWMutex
}

func main() {
	var onceQuery string
	flag.StringVar(&onceQuery, "once", "", "run one query and exit")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
	}()

	pluginName := os.Getenv("TARRAGON_PLUGIN_NAME")
	if pluginName == "" {
		pluginName = defaultPluginName
	}

	userDirs := resolveUserDirs()
	dbPath := resolveDBPath()
	db, err := openDB(dbPath)
	if err != nil {
		logError(pluginName, "failed to open db: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	idx := &indexer{db: db, dirs: userDirs}

	if onceQuery != "" {
		logInfo(pluginName, "running one-shot index pass")
		if err := idx.runOnce(ctx); err != nil {
			logError(pluginName, "indexing failed in --once mode: %v", err)
			os.Exit(1)
		}
		results, err := idx.query(ctx, onceQuery)
		if err != nil {
			logError(pluginName, "query failed in --once mode: %v", err)
			os.Exit(1)
		}
		out := pluginMessage{
			Type: "response",
			Data: responseData{Results: results},
		}
		enc := json.NewEncoder(os.Stdout)
		if err := enc.Encode(out); err != nil {
			logError(pluginName, "failed to print JSON: %v", err)
			os.Exit(1)
		}
		return
	}

	go idx.runLoop(ctx, pluginName)

	endpoint := os.Getenv("TARRAGON_PLUGINS_ENDPOINT")
	if endpoint == "" {
		logInfo(pluginName, "no endpoint configured; entering idle mode")
		<-ctx.Done()
		logInfo(pluginName, "shutdown complete")
		return
	}

	if err := runDaemonMode(ctx, endpoint, pluginName, idx); err != nil && !errors.Is(err, context.Canceled) {
		logError(pluginName, "daemon mode failed: %v", err)
		os.Exit(1)
	}
}

func resolveUserDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	fallback := map[string]string{
		"XDG_DOCUMENTS_DIR": filepath.Join(home, "Documents"),
		"XDG_DOWNLOAD_DIR":  filepath.Join(home, "Downloads"),
		"XDG_PICTURES_DIR":  filepath.Join(home, "Pictures"),
		"XDG_VIDEOS_DIR":    filepath.Join(home, "Videos"),
		"XDG_MUSIC_DIR":     filepath.Join(home, "Music"),
		"XDG_DESKTOP_DIR":   filepath.Join(home, "Desktop"),
	}

	resolved := make(map[string]string, len(fallback))
	for k, v := range fallback {
		resolved[k] = v
	}

	configPath := filepath.Join(home, ".config", "user-dirs.dirs")
	b, err := os.ReadFile(configPath)
	if err == nil {
		lines := strings.Split(string(b), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			val = strings.Trim(val, `"`)
			if val == "" {
				continue
			}
			val = strings.ReplaceAll(val, "$HOME", home)
			if strings.HasPrefix(val, "~") {
				val = filepath.Join(home, strings.TrimPrefix(val, "~/"))
			}
			if _, ok := resolved[key]; ok {
				resolved[key] = val
			}
		}
	}

	order := []string{
		"XDG_DOCUMENTS_DIR",
		"XDG_DOWNLOAD_DIR",
		"XDG_PICTURES_DIR",
		"XDG_VIDEOS_DIR",
		"XDG_MUSIC_DIR",
		"XDG_DESKTOP_DIR",
	}

	seen := map[string]struct{}{}
	var dirs []string
	for _, key := range order {
		d := filepath.Clean(resolved[key])
		if _, ok := seen[d]; ok {
			continue
		}
		st, err := os.Stat(d)
		if err != nil || !st.IsDir() {
			continue
		}
		seen[d] = struct{}{}
		dirs = append(dirs, d)
	}

	return dirs
}

func resolveDBPath() string {
	home, _ := os.UserHomeDir()
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "tarragon", "file_finder.db")
}

func openDB(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir state dir: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}

	schema := `
CREATE TABLE IF NOT EXISTS files (
	path TEXT PRIMARY KEY,
	filename TEXT NOT NULL,
	mtime REAL NOT NULL,
	size INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_filename ON files(filename);
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return db, nil
}

func (i *indexer) runLoop(ctx context.Context, pluginName string) {
	if err := i.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logError(pluginName, "initial indexing pass failed: %v", err)
	}

	ticker := time.NewTicker(rescanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := i.runOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logError(pluginName, "periodic indexing pass failed: %v", err)
			}
		}
	}
}

func (i *indexer) runOnce(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO files(path, filename, mtime, size)
VALUES(?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	filename=excluded.filename,
	mtime=excluded.mtime,
	size=excluded.size
`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare upsert: %w", err)
	}

	commitBatch := func() error {
		if err := stmt.Close(); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		newTx, err := i.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		tx = newTx
		newStmt, err := tx.PrepareContext(ctx, `
INSERT INTO files(path, filename, mtime, size)
VALUES(?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	filename=excluded.filename,
	mtime=excluded.mtime,
	size=excluded.size
`)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		stmt = newStmt
		return nil
	}

	count := 0
	for _, root := range i.dirs {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if _, err := stmt.ExecContext(ctx, path, d.Name(), float64(info.ModTime().UnixNano())/1e9, info.Size()); err != nil {
				return nil
			}
			count++
			if count%batchSize == 0 {
				if err := commitBatch(); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}

	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("close stmt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	if err := i.pruneStale(ctx); err != nil {
		return fmt.Errorf("prune stale: %w", err)
	}

	return nil
}

func (i *indexer) pruneStale(ctx context.Context) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT path FROM files`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer rows.Close()

	del, err := tx.PrepareContext(ctx, `DELETE FROM files WHERE path = ?`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer del.Close()

	for rows.Next() {
		select {
		case <-ctx.Done():
			_ = tx.Rollback()
			return ctx.Err()
		default:
		}

		var path string
		if err := rows.Scan(&path); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := os.Stat(path); err != nil {
			if _, err := del.ExecContext(ctx, path); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (i *indexer) query(ctx context.Context, q string) ([]resultItem, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	q = strings.TrimSpace(q)
	if q == "" {
		return []resultItem{}, nil
	}

	tokens := strings.Fields(strings.ToLower(q))
	if len(tokens) == 0 {
		return []resultItem{}, nil
	}

	var sb strings.Builder
	sb.WriteString("SELECT path, filename, size FROM files WHERE ")
	args := make([]any, 0, len(tokens)+1)
	for idx, t := range tokens {
		if idx > 0 {
			sb.WriteString(" AND ")
		}
		sb.WriteString("lower(filename) LIKE ?")
		args = append(args, "%"+t+"%")
	}
	sb.WriteString(" LIMIT ?")
	args = append(args, maxResults)

	rows, err := i.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []fileRow
	for rows.Next() {
		var r fileRow
		if err := rows.Scan(&r.Path, &r.Filename, &r.Size); err != nil {
			return nil, err
		}
		hits = append(hits, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make([]resultItem, 0, len(hits))
	for _, h := range hits {
		parent := filepath.Dir(h.Path)
		if parent == "" {
			parent = "/"
		}
		results = append(results, resultItem{
			ID:          h.Path,
			Label:       h.Filename,
			Description: parent,
			Icon:        "text-x-generic",
			Category:    "Files",
			PreviewPath: h.Path,
			Score:       scoreFilename(h.Filename, q),
			Actions: []actionItem{
				{Name: "open", Default: true, Description: "Open file"},
				{Name: "open_folder", Description: "Open containing folder"},
			},
		})
	}

	sort.SliceStable(results, func(a, b int) bool {
		if results[a].Score == results[b].Score {
			return results[a].ID < results[b].ID
		}
		return results[a].Score > results[b].Score
	})

	if len(results) > maxResults {
		results = results[:maxResults]
	}

	return results, nil
}

func scoreFilename(filename, query string) float64 {
	lf := utf8.RuneCountInString(filename)
	lq := utf8.RuneCountInString(query)
	maxLen := maxInt(lf, lq, 1)
	diff := absInt(lf - lq)
	score := 1.0 - float64(diff)/float64(maxLen)
	if score < 0.1 {
		return 0.1
	}
	return math.Min(score, 1.0)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func maxInt(vals ...int) int {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func runDaemonMode(ctx context.Context, endpoint, pluginName string, idx *indexer) error {
	conn, err := connectWithRetry(ctx, endpoint, 20, 100*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := writeJSONLine(conn, pluginMessage{Type: "hello", Name: pluginName}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	logInfo(pluginName, "connected to daemon")

	reader := bufio.NewScanner(conn)
	reader.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !reader.Scan() {
			if err := reader.Err(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			return nil
		}

		line := reader.Bytes()
		var msg pluginMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			logError(pluginName, "invalid message: %v", err)
			continue
		}

		switch msg.Type {
		case "request":
			results, err := idx.query(ctx, msg.Text)
			if err != nil {
				logError(pluginName, "query failed: %v", err)
				results = []resultItem{}
			}
			resp := pluginMessage{
				Type:    "response",
				QueryID: msg.QueryID,
				Data:    responseData{Results: results},
			}
			if err := writeJSONLine(conn, resp); err != nil {
				return fmt.Errorf("write response: %w", err)
			}
		case "select":
			resultID := msg.ResultID
			action := msg.Action
			logInfo(pluginName, "select qid=%s result_id=%s action=%s", msg.QueryID, resultID, action)

			var success bool
			var message string
			switch action {
			case "open", "":
				cmd := exec.Command("xdg-open", resultID)
				cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				cmd.Stdout = nil
				cmd.Stderr = nil
				if err := cmd.Start(); err != nil {
					success, message = false, fmt.Sprintf("failed to open: %v", err)
				} else {
					success, message = true, fmt.Sprintf("Opened %s", filepath.Base(resultID))
				}
			case "open_folder":
				dir := filepath.Dir(resultID)
				cmd := exec.Command("xdg-open", dir)
				cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				cmd.Stdout = nil
				cmd.Stderr = nil
				if err := cmd.Start(); err != nil {
					success, message = false, fmt.Sprintf("failed to open folder: %v", err)
				} else {
					success, message = true, fmt.Sprintf("Opened %s", dir)
				}
			default:
				success, message = false, fmt.Sprintf("unknown action: %s", action)
			}

			resp := selectResponse{Type: "select_response", Success: success, Message: message}
			if err := writeJSONLine(conn, resp); err != nil {
				return fmt.Errorf("write select_response: %w", err)
			}
		default:
			logInfo(pluginName, "ignoring message type=%s", msg.Type)
		}
	}
}

func connectWithRetry(ctx context.Context, endpoint string, attempts int, delay time.Duration) (net.Conn, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		conn, err := net.Dial("unix", endpoint)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
	return nil, fmt.Errorf("connect failed after %d attempts: %w", attempts, lastErr)
}

func writeJSONLine(w io.Writer, msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func logInfo(pluginName, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[PLUGIN: %s] INFO %s\n", pluginName, fmt.Sprintf(format, args...))
}

func logError(pluginName, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[PLUGIN: %s] ERROR %s\n", pluginName, fmt.Sprintf(format, args...))
}
