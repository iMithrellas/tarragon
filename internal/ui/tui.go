package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/go-zeromq/zmq4"
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
		active: make(map[string]struct{}),
		winch:  make(chan os.Signal, 1),
		done:   make(chan struct{}),
	}
	if err := t.initSockets(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer t.closeSockets()
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
	req, sub        zmq4.Socket
	clientID        string
	input, lastSent string
	lastQID         string
	rows            []item
	sel, top        int
	pendingAt       time.Time
	debounce        int
	active          map[string]struct{}
	subMu, renderMu sync.Mutex
	winch           chan os.Signal
	done            chan struct{}
	orig            *unix.Termios
}

func (t *tui) initSockets() error {
	t.req = zmq4.NewReq(t.ctx)
	if err := t.req.Dial(wire.EndpointUIReq); err != nil {
		return fmt.Errorf("connect REQ: %w", err)
	}
	t.sub = zmq4.NewSub(t.ctx)
	if err := t.sub.Dial(wire.EndpointUISub); err != nil {
		return fmt.Errorf("connect SUB: %w", err)
	}
	return nil
}
func (t *tui) closeSockets() {
	if t.req != nil {
		_ = t.req.Close()
	}
	if t.sub != nil {
		_ = t.sub.Close()
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
	t.renderMu.Lock()
	redraw(t.input, t.rows, t.sel, t.top, t.lastQID)
	t.renderMu.Unlock()

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
			if t.sel >= 0 && t.sel < len(t.rows) && t.lastQID != "" {
				t.sendSelect(t.rows[t.sel])
			} else if t.input != t.lastSent {
				t.pendingAt = time.Now().Add(-time.Hour)
			}
			t.render()
		case 127: // Backspace
			if len(t.input) > 0 {
				t.input = t.input[:len(t.input)-1]
				t.pendingAt = time.Now()
			}
			t.sel, t.top = 0, 0
			t.render()
		default:
			if b >= 32 && b <= 126 {
				t.input += string(b)
				t.pendingAt = time.Now()
				t.sel, t.top = 0, 0
				t.render()
			}
			if b == 'q' && t.input == "" {
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
	redraw(t.input, t.rows, t.sel, t.top, t.lastQID)
	t.renderMu.Unlock()
}
func (t *tui) moveSel(delta int) {
	if delta < 0 && t.sel > 0 {
		t.sel--
	} else if delta > 0 && t.sel+1 < len(t.rows) {
		t.sel++
	}
	adjustViewport(&t.top, t.sel, t.rows)
	t.render()
}

func (t *tui) recvUpdates() {
	type aggView struct {
		QueryID string            `json:"query_id"`
		List    []wire.ResultItem `json:"list"`
	}
	for {
		select {
		case <-t.done:
			return
		default:
		}
		msg, err := t.sub.Recv()
		if err != nil || len(msg.Frames) < 2 {
			if err != nil {
				fmt.Fprintf(os.Stderr, "recv: %v\n", err)
			}
			return
		}
		t.subMu.Lock()
		_, ok := t.active[string(msg.Frames[0])]
		t.subMu.Unlock()
		if !ok {
			continue
		}
		var upd wire.UpdateMessage
		if json.Unmarshal(msg.Frames[1], &upd) != nil {
			continue
		}
		var view aggView
		if json.Unmarshal(upd.Payload, &view) != nil || view.QueryID == "" {
			continue
		}
		t.lastQID = view.QueryID
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
		t.render()
	}
}

func (t *tui) debounceSender() {
	for {
		time.Sleep(25 * time.Millisecond)
		if t.pendingAt.IsZero() || time.Since(t.pendingAt) < time.Duration(t.debounce)*time.Millisecond || t.input == t.lastSent {
			if !t.pendingAt.IsZero() && t.input == t.lastSent {
				t.pendingAt = time.Time{}
			}
			continue
		}
		// send query
		body, _ := json.Marshal(&wire.UIRequest{Type: "query", ClientID: t.clientID, Text: t.input})
		if err := t.req.Send(zmq4.NewMsg(body)); err != nil {
			continue
		}
		reply, err := t.req.Recv()
		if err != nil || len(reply.Frames) == 0 {
			continue
		}
		var ack wire.AckMessage
		if json.Unmarshal(reply.Frames[0], &ack) != nil || ack.QueryID == "" {
			continue
		}
		t.subMu.Lock()
		for topic := range t.active {
			_ = t.sub.SetOption(zmq4.OptionUnsubscribe, topic)
		}
		t.active = make(map[string]struct{})
		_ = t.sub.SetOption(zmq4.OptionSubscribe, ack.QueryID)
		t.active[ack.QueryID] = struct{}{}
		t.subMu.Unlock()
		t.lastSent, t.rows, t.sel, t.top = t.input, nil, 0, 0
		t.render()
		t.pendingAt = time.Time{}
	}
}

func (t *tui) sendSelect(it item) {
	body, _ := json.Marshal(&wire.UIRequest{Type: "select", ClientID: t.clientID, QueryID: t.lastQID, Plugin: it.Plugin, Text: it.Choice.ID})
	if err := t.req.Send(zmq4.NewMsg(body)); err == nil {
		_, _ = t.req.Recv()
	}
}
func (t *tui) detach() {
	body, _ := json.Marshal(&wire.UIRequest{Type: "detach", ClientID: t.clientID})
	_ = t.req.Send(zmq4.NewMsg(body))
	_, _ = t.req.Recv()
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
