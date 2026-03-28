package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/iMithrellas/tarragon/internal/wire"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"
)

// choice represents a selectable suggestion from a plugin.
type choice struct{ ID, Label string }

// item is a flattened selectable row tagged with plugin source.
type item struct {
	Plugin string
	Choice choice
}

// RunTUI launches a minimal interactive TUI without external deps.
func RunTUI() {
	t := &tui{
		ctx:      context.Background(),
		clientID: fmt.Sprintf("cli-%d", time.Now().UnixNano()),
		debounce: func() int {
			if d := viper.GetInt("tuidebounce"); d > 0 {
				return d
			}
			return 200
		}(),
		winch: make(chan os.Signal, 1),
		done:  make(chan struct{}),
	}
	if err := t.initConn(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer t.closeConn()
	if err := t.initTerminal(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer t.restoreTerminal()
	signal.Notify(t.winch, unix.SIGWINCH)
	t.loop()
}

type tui struct {
	ctx             context.Context
	conn            net.Conn
	scanner         *bufio.Scanner
	writeMu         sync.Mutex
	renderMu        sync.Mutex
	clientID        string
	input, lastSent string
	lastQID         string
	rows            []item
	sel, top        int
	pendingAt       time.Time
	debounce        int
	winch           chan os.Signal
	done            chan struct{}
	orig            *unix.Termios
}

func (t *tui) initConn() error {
	conn, err := net.Dial("unix", wire.SocketUI)
	if err != nil {
		return fmt.Errorf("connect UI socket: %w", err)
	}
	t.conn = conn
	t.scanner = wire.NewScanner(conn)
	return nil
}

func (t *tui) closeConn() {
	if t.conn != nil {
		_ = t.conn.Close()
	}
}

func (t *tui) initTerminal() error {
	var err error
	if t.orig, err = enableRaw(); err != nil {
		return fmt.Errorf("enable raw: %w", err)
	}
	enterAltScreen()
	hideCursor()
	return nil
}
func (t *tui) restoreTerminal() { showCursor(); exitAltScreen(); restoreTerm(t.orig) }

func (t *tui) loop() {
	// updates
	go t.recvUpdates()
	// debounced sender
	go t.debounceSender()
	// initial frame
	t.render()

	// key loop
	rd := os.Stdin
	buf := make([]byte, 8)
	for {
		select {
		case <-t.winch:
			t.render()
		default:
		}
		n, err := rd.Read(buf[:1])
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "read: %v\n", err)
			}
			break
		}
		if n == 0 {
			continue
		}
		b := buf[0]
		if b == 0x1b { // ESC sequences
			if _, err := rd.Read(buf[:1]); err == nil && buf[0] == '[' {
				if _, err := rd.Read(buf[:1]); err == nil {
					switch buf[0] {
					case 'A':
						t.moveSel(-1)
					case 'B':
						t.moveSel(1)
					}
				}
			}
			continue
		}
		switch b {
		case 3, 4: // Ctrl-C/D
			t.detach()
			close(t.done)
			fmt.Println("\r")
			return
		case 13, 10: // Enter
			var selected item
			doSelect := false
			t.renderMu.Lock()
			if t.sel >= 0 && t.sel < len(t.rows) && t.lastQID != "" {
				selected = t.rows[t.sel]
				doSelect = true
			} else if t.input != t.lastSent {
				t.pendingAt = time.Now().Add(-time.Hour)
			}
			t.renderMu.Unlock()
			if doSelect {
				t.sendSelect(selected)
			}
			t.render()
		case 127: // Backspace
			t.renderMu.Lock()
			if len(t.input) > 0 {
				t.input = t.input[:len(t.input)-1]
				t.pendingAt = time.Now()
			}
			t.sel, t.top = 0, 0
			t.renderMu.Unlock()
			t.render()
		default:
			if b >= 32 && b <= 126 {
				t.renderMu.Lock()
				t.input += string(b)
				t.pendingAt = time.Now()
				t.sel, t.top = 0, 0
				t.renderMu.Unlock()
				t.render()
			}
			t.renderMu.Lock()
			emptyInput := t.input == ""
			t.renderMu.Unlock()
			if b == 'q' && emptyInput {
				t.detach()
				close(t.done)
				fmt.Println("\r")
				return
			}
		}
	}
}

func (t *tui) render() {
	t.renderMu.Lock()
	defer t.renderMu.Unlock()
	redraw(t.input, t.rows, t.sel, t.top, t.lastQID)
}
func (t *tui) moveSel(delta int) {
	t.renderMu.Lock()
	if delta < 0 && t.sel > 0 {
		t.sel--
	} else if delta > 0 && t.sel+1 < len(t.rows) {
		t.sel++
	}
	adjustViewport(&t.top, t.sel, t.rows)
	t.renderMu.Unlock()
	t.render()
}

func (t *tui) recvUpdates() {
	type aggView struct {
		QueryID string            `json:"query_id"`
		List    []wire.ResultItem `json:"list"`
	}
	for {
		var raw json.RawMessage
		err := wire.ReadMsg(t.scanner, &raw)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "recv: %v\n", err)
			}
			return
		}
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &kind) != nil {
			continue
		}
		switch kind.Type {
		case "ack":
			var ack wire.AckMessage
			if json.Unmarshal(raw, &ack) != nil || ack.QueryID == "" {
				continue
			}
			t.renderMu.Lock()
			t.lastQID = ack.QueryID
			t.renderMu.Unlock()
			t.render()
		case "update":
			var upd wire.UpdateMessage
			if json.Unmarshal(raw, &upd) != nil {
				continue
			}
			var view aggView
			if json.Unmarshal(upd.Payload, &view) != nil || view.QueryID == "" {
				continue
			}
			t.renderMu.Lock()
			if t.lastQID == "" || view.QueryID != t.lastQID {
				t.renderMu.Unlock()
				continue
			}
			var newRows []item
			for _, it := range view.List {
				id := it.ID
				label := it.Label
				if label == "" {
					label = id
				}
				if id == "" {
					id = label
				}
				if id == "" && label == "" {
					continue
				}
				newRows = append(newRows, item{Plugin: it.Plugin, Choice: choice{ID: id, Label: label}})
			}
			t.rows = newRows
			if t.sel >= len(t.rows) {
				t.sel = len(t.rows) - 1
				if t.sel < 0 {
					t.sel = 0
				}
			}
			adjustViewport(&t.top, t.sel, t.rows)
			t.renderMu.Unlock()
			t.render()
		default:
			continue
		}
	}
}

func (t *tui) debounceSender() {
	for {
		time.Sleep(25 * time.Millisecond)
		t.renderMu.Lock()
		pendingAt := t.pendingAt
		debounce := t.debounce
		input := t.input
		lastSent := t.lastSent
		t.renderMu.Unlock()
		if pendingAt.IsZero() || time.Since(pendingAt) < time.Duration(debounce)*time.Millisecond || input == lastSent {
			if !pendingAt.IsZero() && input == lastSent {
				t.renderMu.Lock()
				t.pendingAt = time.Time{}
				t.renderMu.Unlock()
			}
			continue
		}
		if t.conn == nil {
			continue
		}
		t.writeMu.Lock()
		err := wire.WriteMsg(t.conn, &wire.UIRequest{Type: "query", ClientID: t.clientID, Text: input})
		t.writeMu.Unlock()
		if err != nil {
			continue
		}
		t.renderMu.Lock()
		t.lastSent, t.rows, t.sel, t.top = input, nil, 0, 0
		t.pendingAt = time.Time{}
		t.renderMu.Unlock()
		t.render()
	}
}

func (t *tui) sendSelect(it item) {
	if t.conn == nil {
		return
	}
	t.writeMu.Lock()
	t.renderMu.Lock()
	qid := t.lastQID
	t.renderMu.Unlock()
	_ = wire.WriteMsg(t.conn, &wire.UIRequest{Type: "select", ClientID: t.clientID, QueryID: qid, Plugin: it.Plugin, Text: it.Choice.ID})
	t.writeMu.Unlock()
}

func (t *tui) detach() {
	if t.conn == nil {
		return
	}
	t.writeMu.Lock()
	_ = wire.WriteMsg(t.conn, &wire.UIRequest{Type: "detach", ClientID: t.clientID})
	t.writeMu.Unlock()
}

// redraw prints the UI using alternate buffer with a responsive layout.
func redraw(input string, rows []item, sel int, top int, qid string) {
	// Clear and home
	fmt.Print("\033[H\033[2J")
	// Get terminal size
	h, w := termSize()
	if h < 5 {
		h = 5
	}
	if w < 20 {
		w = 20
	}
	// Header
	title := "Tarragon TUI"
	printLine(fmt.Sprintf("\033[1m%s\033[0m", title), w)
	// Input line
	qline := fmt.Sprintf("Query: %s", input)
	printLine(truncate(qline, w), w)
	printLine("", w)
	// Results area height (reserve 1 line for status)
	area := h - 4
	if area < 1 {
		area = 1
	}
	// Render rows within viewport
	end := top + area
	if end > len(rows) {
		end = len(rows)
	}
	for i := top; i < end; i++ {
		it := rows[i]
		content := truncate(fmt.Sprintf("[%s] %s", it.Plugin, it.Choice.Label), w)
		if i == sel {
			printLine("\033[7m"+content+"\033[0m", w)
		} else {
			printLine(content, w)
		}
	}
	// Fill blank lines if fewer than area
	for i := end; i < top+area; i++ {
		printLine("", w)
	}
	// Status line
	more := 0
	if len(rows) > end {
		more = len(rows) - end
	}
	status := fmt.Sprintf("%d results%s • ↑/↓ move • Enter select • Ctrl-C quit", len(rows), func() string {
		if more > 0 {
			return fmt.Sprintf(" (+%d)", more)
		}
		return ""
	}())
	if qid != "" {
		status = fmt.Sprintf("%s • id=%s", status, qid)
	}
	printLine(truncate(status, w), w)
	// Move cursor to input end (row 2, col after prefix+input)
	col := len("Query: ") + len(input) + 1
	if col < 1 {
		col = 1
	}
	if col > w {
		col = w
	}
	fmt.Printf("\033[2;%dH", col)
}

func adjustViewport(top *int, sel int, rows []item) {
	h, _ := termSize()
	area := h - 4
	if area < 1 {
		area = 1
	}
	if sel < *top {
		*top = sel
	}
	if sel >= *top+area {
		*top = sel - area + 1
	}
	if *top < 0 {
		*top = 0
	}
	if *top > len(rows) {
		*top = len(rows)
	}
}

func termSize() (h, w int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws == nil || ws.Row == 0 || ws.Col == 0 {
		return 24, 80
	}
	return int(ws.Row), int(ws.Col)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if len(s) <= w {
		return s
	}
	if w <= 1 {
		return s[:w]
	}
	if w == 2 {
		return s[:1] + "…"
	}
	return s[:w-1] + "…"
}

// (no table helpers in this variant)

func enterAltScreen() { fmt.Print("\033[?1049h\033[H\033[2J") }
func exitAltScreen()  { fmt.Print("\033[?1049l\033[0m") }
func hideCursor()     { fmt.Print("\033[?25l") }
func showCursor()     { fmt.Print("\033[?25h") }

// Ensure each printed line starts at column 1 and ends with CRLF
func printLine(s string, w int) {
	if w > 0 {
		s = truncate(s, w)
	}
	fmt.Print("\r")
	fmt.Print(s)
	fmt.Print("\r\n")
}

// enableRaw switches TTY to raw mode (Unix).
func enableRaw() (*unix.Termios, error) {
	fd := int(os.Stdin.Fd())
	orig, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	raw := *orig
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	// Keep OPOST so that newlines and carriage returns behave consistently
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return orig, nil
}

func restoreTerm(orig *unix.Termios) {
	if orig == nil {
		return
	}
	_ = unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, orig)
}

// extractChoices tries to parse a plugin payload into a list of selectable items.
// It supports a few common shapes:
// - { "suggestions": [ {"id":"...", "label"|"title"|"text":"..."}, ... ] }
// - [ {"id":"...", ...}, ... ]
// - [ "string", ... ] (id and label are the string)
func extractChoices(raw json.RawMessage) []choice {
	// The raw is an aggResult JSON; unwrap if necessary
	type resultWrap struct {
		ElapsedMs float64         `json:"elapsed_ms"`
		Data      json.RawMessage `json:"data"`
	}
	var wrap resultWrap
	if err := json.Unmarshal(raw, &wrap); err == nil && len(wrap.Data) > 0 {
		raw = wrap.Data
	}
	// Try object with suggestions/variants/items/choices fields
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		for _, k := range []string{"suggestions", "variants", "items", "choices"} {
			if arr, ok := obj[k]; ok {
				if chs := parseArrayChoices(arr); len(chs) > 0 {
					return chs
				}
			}
		}
	}
	// Try array at top level
	if chs := parseArrayChoices(raw); len(chs) > 0 {
		return chs
	}
	return nil
}

func parseArrayChoices(raw json.RawMessage) []choice {
	// array of objects
	var objs []map[string]any
	if json.Unmarshal(raw, &objs) == nil && len(objs) > 0 {
		out := make([]choice, 0, len(objs))
		for _, o := range objs {
			var id, label string
			if v, ok := o["id"].(string); ok {
				id = v
			}
			if v, ok := o["label"].(string); ok {
				label = v
			}
			if label == "" {
				if v, ok := o["title"].(string); ok {
					label = v
				}
			}
			if label == "" {
				if v, ok := o["text"].(string); ok {
					label = v
				}
			}
			if id == "" && label == "" {
				continue
			}
			if id == "" {
				id = label
			}
			out = append(out, choice{ID: id, Label: label})
		}
		if len(out) > 0 {
			return out
		}
	}
	// array of strings
	var strs []string
	if json.Unmarshal(raw, &strs) == nil && len(strs) > 0 {
		out := make([]choice, 0, len(strs))
		for _, s := range strs {
			out = append(out, choice{ID: s, Label: s})
		}
		return out
	}
	return nil
}
