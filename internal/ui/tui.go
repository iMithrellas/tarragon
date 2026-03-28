package ui

// tui.go — Bubble Tea + Lipgloss interactive TUI for the Tarragon launcher.
//
// Terminal input management (raw mode, alt-screen, cursor visibility) is
// handled entirely by Bubble Tea.  All raw ANSI manipulation has been removed.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/iMithrellas/tarragon/internal/wire"
	"github.com/spf13/viper"
)

// ─── Focus enum ──────────────────────────────────────────────────────────────

type focusArea int

const (
	listFocus   focusArea = iota // default: ↑↓ navigates result list
	detailFocus                  // ↑↓/j/k scrolls JSON viewport
)

// ─── Message types (Tea) ─────────────────────────────────────────────────────

type ackMsg struct{ queryID string }
type updateMsg struct {
	queryID string
	rows    []wire.ResultItem
	results map[string]tuiAggResult
}
type errMsg struct{ err error }
type debounceTickMsg struct{ seq int }
type statusMsg struct {
	connected []string
	total     int
}
type statusTickMsg struct{}

// selectResponseMsg carries the daemon's response to a select action.
type selectResponseMsg struct {
	success bool
	message string
}

// clearSelectStatusMsg is sent after the auto-clear timer fires.
type clearSelectStatusMsg struct{}

// ─── Data types ──────────────────────────────────────────────────────────────

// tuiAggResult extends the bench-only aggResult with the full plugin data payload.
type tuiAggResult struct {
	ElapsedMs float64         `json:"elapsed_ms"`
	Data      json.RawMessage `json:"data"`
}

// tuiAggView is the payload structure from daemon UpdateMessage for the TUI.
type tuiAggView struct {
	QueryID string                  `json:"query_id"`
	Input   string                  `json:"input"`
	Results map[string]tuiAggResult `json:"results"`
	List    []wire.ResultItem       `json:"list"`
}

// choice represents a selectable suggestion from a plugin.
type choice struct{ ID, Label string }

// connState holds connection-related fields that must not be copied by value.
// It is always accessed via a pointer so that the Tea value-copy semantics
// on Model do not trigger the sync.Mutex copy-lock check.
type connState struct {
	conn    net.Conn
	scanner *bufio.Scanner
	mu      sync.Mutex // serialises writes to conn
	program *tea.Program
}

func (cs *connState) writeMsg(v any) error {
	if cs == nil || cs.conn == nil {
		return nil
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return wire.WriteMsg(cs.conn, v)
}

// ─── Plugin color palette ─────────────────────────────────────────────────────

// pluginColors is a small palette of distinct terminal-friendly colors.
var pluginColors = []lipgloss.Color{
	"#FF6B6B", // coral-red
	"#4ECDC4", // teal
	"#45B7D1", // sky-blue
	"#96CEB4", // sage-green
	"#FFEAA7", // pale-yellow
	"#DDA0DD", // plum
	"#98D8C8", // mint
	"#F7DC6F", // golden
}

// pluginColor hashes a plugin name to a consistent color from the palette.
func pluginColor(name string) lipgloss.Color {
	var h int
	for _, r := range name {
		h += int(r)
	}
	return pluginColors[h%len(pluginColors)]
}

// ─── Lipgloss styles ─────────────────────────────────────────────────────────

var (
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))

	styleSelectedRow = lipgloss.NewStyle().
				Bold(true).
				Background(lipgloss.Color("236")).
				Foreground(lipgloss.Color("255"))

	styleLatency = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Italic(true)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	styleBorderFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205"))

	styleBorderNormal = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("238"))

	styleCursor = lipgloss.NewStyle().
			Background(lipgloss.Color("205")).
			Foreground(lipgloss.Color("0"))
)

// ─── Model ───────────────────────────────────────────────────────────────────

// Model is the Bubble Tea model for the Tarragon TUI.
// All fields that contain a sync.Mutex are stored via *connState so that the
// model can safely be passed by value through Bubble Tea's interface.
type Model struct {
	// connection (pointer — contains mutex, must not be value-copied)
	cs *connState

	// identity / query state
	clientID    string
	lastQID     string
	lastSent    string
	debounceMs  int
	debounceSeq int // monotonically increasing; used to ignore stale ticks

	// inline text input (managed manually — textinput requires atotto/clipboard)
	inputValue string
	inputPos   int // cursor position in runes

	// result state
	rows    []wire.ResultItem
	results map[string]tuiAggResult
	cursor  int

	// UI state
	focus   focusArea
	loading bool
	spinner spinner.Model
	detail  viewport.Model

	// transient action feedback (cleared after 3s)
	selectStatus  string // empty means no message
	selectSuccess bool   // true = success, false = error

	// plugin status
	pluginNames []string // connected plugin names
	pluginTotal int      // total enabled plugins

	// dimensions
	width  int
	height int
}

// NewModel constructs an initial Model.  Call RunTUI() for the normal entry point.
func NewModel(clientID string, debounceMs int) Model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return Model{
		clientID:   clientID,
		debounceMs: debounceMs,
		spinner:    sp,
		results:    make(map[string]tuiAggResult),
		width:      80,
		height:     24,
		detail:     viewport.New(60, 20),
	}
}

// ─── Tea interface ───────────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	m.doSendStatus()
	return tea.Batch(
		m.spinner.Tick,
		m.startReaderCmd(),
		tea.Tick(5*time.Second, func(_ time.Time) tea.Msg { return statusTickMsg{} }),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ── Window resize ──────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeViewport()

	// ── Spinner tick ───────────────────────────────────────────────────────
	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	// ── Background reader messages ─────────────────────────────────────────
	case ackMsg:
		m.lastQID = msg.queryID
		m.loading = true
		cmds = append(cmds, m.spinner.Tick)

	case updateMsg:
		if m.lastQID == "" || msg.queryID != m.lastQID {
			break
		}
		m.loading = false
		m.rows = msg.rows
		m.results = msg.results
		// clamp cursor
		if m.cursor >= len(m.rows) {
			m.cursor = max(0, len(m.rows)-1)
		}
		m.updateDetailContent()

	case errMsg:
		// connection error — quit gracefully
		return m, tea.Quit

	// ── Plugin status ──────────────────────────────────────────────────────
	case statusMsg:
		m.pluginNames = msg.connected
		m.pluginTotal = msg.total

	case statusTickMsg:
		m.doSendStatus()
		cmds = append(cmds, tea.Tick(5*time.Second, func(_ time.Time) tea.Msg { return statusTickMsg{} }))

	// ── Select action feedback ──────────────────────────────────────────────
	case selectResponseMsg:
		if msg.message != "" {
			m.selectStatus = msg.message
		} else if msg.success {
			m.selectStatus = "✓ Action executed"
		} else {
			m.selectStatus = "✗ Action failed"
		}
		m.selectSuccess = msg.success
		// Auto-clear after 3 seconds
		cmds = append(cmds, tea.Tick(3*time.Second, func(_ time.Time) tea.Msg {
			return clearSelectStatusMsg{}
		}))

	case clearSelectStatusMsg:
		m.selectStatus = ""

	// ── Debounce tick ──────────────────────────────────────────────────────
	case debounceTickMsg:
		if msg.seq != m.debounceSeq {
			break // stale tick — ignore
		}
		if m.inputValue != m.lastSent {
			cmd := m.doSendQuery(m.inputValue)
			cmds = append(cmds, cmd)
		}

	// ── Keyboard ───────────────────────────────────────────────────────────
	case tea.KeyMsg:
		switch msg.String() {

		case "ctrl+c":
			m.doSendDetach()
			return m, tea.Quit

		case "esc":
			if m.focus == detailFocus {
				m.focus = listFocus
			} else if m.inputValue != "" {
				m.inputValue = ""
				m.inputPos = 0
				m.rows = nil
				m.results = make(map[string]tuiAggResult)
				m.cursor = 0
				m.lastQID = ""
				m.loading = false
				m.updateDetailContent()
			}

		case "tab":
			if m.showDetail() {
				if m.focus == listFocus {
					m.focus = detailFocus
				} else {
					m.focus = listFocus
				}
			}

		case "enter":
			if m.focus == listFocus && len(m.rows) > 0 && m.lastQID != "" {
				m.doSendSelect()
			}

		case "q":
			if m.inputValue == "" {
				m.doSendDetach()
				return m, tea.Quit
			}
			// 'q' is a printable char — insert it
			m.inputInsert('q')
			cmds = append(cmds, m.scheduleDebounce())

		case "up":
			if m.focus == detailFocus {
				var cmd tea.Cmd
				m.detail, cmd = m.detail.Update(msg)
				cmds = append(cmds, cmd)
			} else {
				if m.cursor > 0 {
					m.cursor--
					m.updateDetailContent()
				}
			}

		case "down":
			if m.focus == detailFocus {
				var cmd tea.Cmd
				m.detail, cmd = m.detail.Update(msg)
				cmds = append(cmds, cmd)
			} else {
				if m.cursor < len(m.rows)-1 {
					m.cursor++
					m.updateDetailContent()
				}
			}

		case "j":
			if m.focus == detailFocus {
				var cmd tea.Cmd
				m.detail, cmd = m.detail.Update(tea.KeyMsg{Type: tea.KeyDown})
				cmds = append(cmds, cmd)
			} else {
				m.inputInsert('j')
				cmds = append(cmds, m.scheduleDebounce())
			}

		case "k":
			if m.focus == detailFocus {
				var cmd tea.Cmd
				m.detail, cmd = m.detail.Update(tea.KeyMsg{Type: tea.KeyUp})
				cmds = append(cmds, cmd)
			} else {
				m.inputInsert('k')
				cmds = append(cmds, m.scheduleDebounce())
			}

		case "backspace", "ctrl+h":
			if m.focus != detailFocus {
				if m.inputPos > 0 {
					runes := []rune(m.inputValue)
					m.inputValue = string(append(runes[:m.inputPos-1], runes[m.inputPos:]...))
					m.inputPos--
					m.cursor = 0
					cmds = append(cmds, m.scheduleDebounce())
				}
			}

		case "left":
			if m.focus != detailFocus && m.inputPos > 0 {
				m.inputPos--
			}

		case "right":
			if m.focus != detailFocus {
				runes := []rune(m.inputValue)
				if m.inputPos < len(runes) {
					m.inputPos++
				}
			}

		case "home", "ctrl+a":
			if m.focus != detailFocus {
				m.inputPos = 0
			}

		case "end", "ctrl+e":
			if m.focus != detailFocus {
				m.inputPos = utf8.RuneCountInString(m.inputValue)
			}

		default:
			// Printable rune input (skip 'q' which is handled above)
			if msg.Type == tea.KeyRunes && msg.String() != "q" {
				for _, r := range msg.Runes {
					m.inputInsert(r)
				}
				cmds = append(cmds, m.scheduleDebounce())
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	// ── Header bar ──────────────────────────────────────────────────────────
	header := styleTitle.Render("Tarragon") + "  " + m.renderInput()
	if m.loading {
		header += "  " + m.spinner.View()
	}

	// ── Status bar ──────────────────────────────────────────────────────────
	var statusParts []string

	// Plugin status with colored dots
	if m.pluginTotal > 0 {
		pluginStatus := fmt.Sprintf("Plugins %d/%d", len(m.pluginNames), m.pluginTotal)
		var dots []string
		for _, name := range m.pluginNames {
			col := pluginColor(name)
			dot := lipgloss.NewStyle().Foreground(col).Render("●")
			dots = append(dots, dot)
		}
		if len(dots) > 0 {
			pluginStatus += " " + strings.Join(dots, "")
		}
		statusParts = append(statusParts, pluginStatus)
	}

	statusParts = append(statusParts, fmt.Sprintf("%d result(s)", len(m.rows)))
	statusParts = append(statusParts, "↑↓ navigate")
	if m.showDetail() {
		statusParts = append(statusParts, "Tab detail")
	}
	statusParts = append(statusParts, "Enter select")
	statusParts = append(statusParts, "q quit")
	status := styleStatusBar.Render(strings.Join(statusParts, " • "))

	// Action feedback overlay (shown above the status bar when non-empty)
	if m.selectStatus != "" {
		var feedbackStyle lipgloss.Style
		if m.selectSuccess {
			feedbackStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true) // green
		} else {
			feedbackStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true) // red
		}
		feedback := feedbackStyle.Render(m.selectStatus)
		status = feedback + "  " + status
	}

	// ── Panels ──────────────────────────────────────────────────────────────
	// Available height for panels (subtract header + status bar + 2 border lines each)
	panelH := m.height - 4 // header(1) + status(1) + top-border(1) + bot-border(1)
	if panelH < 3 {
		panelH = 3
	}

	left := m.renderList(panelH)
	if m.showDetail() {
		right := m.renderDetail(panelH)
		panels := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
		return lipgloss.JoinVertical(lipgloss.Left, header, panels, status)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, left, status)
}

// ─── View helpers ─────────────────────────────────────────────────────────────

func (m Model) showDetail() bool {
	return m.width >= 80
}

// renderInput renders the text input with an inline block cursor.
func (m Model) renderInput() string {
	runes := []rune(m.inputValue)
	var b strings.Builder
	b.WriteString("> ")
	for i, r := range runes {
		if i == m.inputPos {
			b.WriteString(styleCursor.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	// Block cursor at end of input
	if m.inputPos == len(runes) {
		b.WriteString(styleCursor.Render(" "))
	}
	return b.String()
}

// renderList renders the result list panel with Lipgloss rounded borders.
func (m Model) renderList(height int) string {
	var panelW int
	if m.showDetail() {
		panelW = m.width * 40 / 100 // 40% for left panel
	} else {
		panelW = m.width // full width (border accounts for 2 chars)
	}
	if panelW < 12 {
		panelW = 12
	}
	innerW := panelW - 2 // subtract left+right border

	// Title
	title := fmt.Sprintf("Results (%d)", len(m.rows))

	// Build row lines
	contentHeight := height - 2 // border top+bottom
	if contentHeight < 1 {
		contentHeight = 1
	}
	lines := make([]string, 0, len(m.rows))
	for i, row := range m.rows {
		label := row.Label
		if label == "" {
			label = row.ID
		}

		latency := ""
		if res, ok := m.results[row.Plugin]; ok && res.ElapsedMs > 0 {
			latency = styleLatency.Render(fmt.Sprintf("%.1fms", res.ElapsedMs))
		}

		col := pluginColor(row.Plugin)
		dot := lipgloss.NewStyle().Foreground(col).Render("●")

		// Frecency indicator: show a flame icon for items with notable frecency
		frecIndicator := ""
		if row.FrecencyScore > 1.0 {
			frecIndicator = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("🔥")
		}

		if i == m.cursor {
			lines = append(lines, styleSelectedRow.Width(innerW).Render(
				fmt.Sprintf(" → %s %-*s  %s %s",
					dot,
					innerW/2,
					truncate(label, innerW/2),
					truncate(row.Plugin, innerW/5),
					frecIndicator,
				),
			))
		} else {
			lines = append(lines, fmt.Sprintf("   %s %-*s  %s %s %s",
				dot,
				innerW/2,
				truncate(label, innerW/2),
				truncate(row.Plugin, innerW/5),
				frecIndicator,
				latency,
			))
		}
	}

	// Pad to fill available height
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}

	content := styleTitle.Render(title) + "\n" + strings.Join(lines[:min(len(lines), contentHeight)], "\n")

	st := styleBorderNormal
	if m.focus == listFocus {
		st = styleBorderFocused
	}
	return st.Width(innerW).Height(height - 2).Render(content)
}

// renderDetail renders the JSON detail panel for the selected result.
func (m Model) renderDetail(height int) string {
	leftW := m.width * 40 / 100
	rightW := m.width - leftW
	if rightW < 12 {
		rightW = 12
	}
	innerW := rightW - 2 // subtract border

	// Plugin for title / border color
	plugin := ""
	if len(m.rows) > 0 && m.cursor < len(m.rows) {
		plugin = m.rows[m.cursor].Plugin
	}

	col := pluginColor(plugin)
	titleText := "Detail"
	if plugin != "" {
		titleText = "Detail: " + plugin
	}
	title := lipgloss.NewStyle().Foreground(col).Bold(true).Render(titleText)

	// Viewport occupies height minus title line and borders
	m.detail.Width = innerW
	m.detail.Height = height - 4

	st := styleBorderNormal
	if m.focus == detailFocus {
		st = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(col)
	}

	return st.Width(innerW).Height(height - 2).Render(title + "\n" + m.detail.View())
}

// ─── State helpers ─────────────────────────────────────────────────────────────

// resizeViewport recalculates viewport dimensions after a window resize.
func (m *Model) resizeViewport() {
	if m.width < 80 {
		return
	}
	leftW := m.width * 40 / 100
	rightW := m.width - leftW - 4
	panelH := m.height - 6
	if rightW < 10 {
		rightW = 10
	}
	if panelH < 3 {
		panelH = 3
	}
	m.detail.Width = rightW
	m.detail.Height = panelH
}

// updateDetailContent pretty-prints the selected result's JSON into the viewport.
// It also prepends a summary line with score / frecency metadata.
func (m *Model) updateDetailContent() {
	if len(m.rows) == 0 || m.cursor >= len(m.rows) {
		m.detail.SetContent("(no selection)")
		return
	}
	row := m.rows[m.cursor]
	res, ok := m.results[row.Plugin]
	if !ok {
		m.detail.SetContent("(no data)")
		return
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		m.detail.SetContent(fmt.Sprintf("(marshal error: %v)", err))
		return
	}

	// Build a metadata header above the raw JSON
	var header strings.Builder
	scoreStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	header.WriteString(scoreStyle.Render(fmt.Sprintf("Plugin: %s", row.Plugin)))
	if row.Score > 0 {
		header.WriteString(scoreStyle.Render(fmt.Sprintf("  Score: %.4f", row.Score)))
	}
	if row.FrecencyScore > 0 {
		frecStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
		header.WriteString("  " + frecStyle.Render(fmt.Sprintf("Frecency: %.2f", row.FrecencyScore)))
	}
	if len(row.Actions) > 0 {
		names := make([]string, 0, len(row.Actions))
		for _, a := range row.Actions {
			n := a.Name
			if a.Default {
				n = "[" + n + "]"
			}
			names = append(names, n)
		}
		header.WriteString(scoreStyle.Render("  Actions: " + strings.Join(names, ", ")))
	}

	m.detail.GotoTop()
	m.detail.SetContent(header.String() + "\n\n" + string(b))
}

// inputInsert inserts a rune at the current cursor position.
func (m *Model) inputInsert(r rune) {
	runes := []rune(m.inputValue)
	runes = append(runes[:m.inputPos], append([]rune{r}, runes[m.inputPos:]...)...)
	m.inputValue = string(runes)
	m.inputPos++
	m.cursor = 0
}

// scheduleDebounce returns a command that fires a debounceTickMsg after the
// configured debounce duration.  It increments the sequence counter so that
// any previous pending tick is rendered stale.
func (m *Model) scheduleDebounce() tea.Cmd {
	m.debounceSeq++
	seq := m.debounceSeq
	delay := time.Duration(m.debounceMs) * time.Millisecond
	return tea.Tick(delay, func(_ time.Time) tea.Msg {
		return debounceTickMsg{seq: seq}
	})
}

// doSendQuery sends a query to the daemon and returns a spinner tick cmd.
func (m *Model) doSendQuery(text string) tea.Cmd {
	if m.cs == nil {
		return nil
	}
	if err := m.cs.writeMsg(&wire.UIRequest{Type: "query", ClientID: m.clientID, Text: text}); err != nil {
		return nil
	}
	m.lastSent = text
	m.rows = nil
	m.results = make(map[string]tuiAggResult)
	m.cursor = 0
	m.loading = true
	m.updateDetailContent()
	return m.spinner.Tick
}

// doSendSelect sends a select message for the currently highlighted row.
// It picks the action marked Default=true, or "execute" as a fallback.
func (m *Model) doSendSelect() {
	if m.cs == nil || len(m.rows) == 0 || m.cursor >= len(m.rows) {
		return
	}
	row := m.rows[m.cursor]

	// Determine the action to send
	action := "execute"
	for _, a := range row.Actions {
		if a.Default {
			action = a.Name
			break
		}
	}
	// If there's exactly one action and no explicit default, use it
	if action == "execute" && len(row.Actions) == 1 {
		action = row.Actions[0].Name
	}

	_ = m.cs.writeMsg(&wire.UIRequest{
		Type:     wire.MsgSelect,
		ClientID: m.clientID,
		QueryID:  m.lastQID,
		Plugin:   row.Plugin,
		ResultID: row.ID,
		Action:   action,
	})
}

// doSendDetach sends a detach message to the daemon.
func (m *Model) doSendDetach() {
	if m.cs == nil {
		return
	}
	_ = m.cs.writeMsg(&wire.UIRequest{Type: "detach", ClientID: m.clientID})
}

// doSendStatus sends a status request to the daemon.
func (m *Model) doSendStatus() {
	if m.cs == nil {
		return
	}
	_ = m.cs.writeMsg(&wire.UIRequest{Type: "status", ClientID: m.clientID})
}

// startReaderCmd returns a tea.Cmd that spawns a goroutine to read messages
// from the daemon socket and forward them to Tea via program.Send.
func (m Model) startReaderCmd() tea.Cmd {
	cs := m.cs // capture pointer — safe across value copies
	return func() tea.Msg {
		go func() {
			if cs == nil || cs.scanner == nil || cs.program == nil {
				return
			}
			for {
				var raw json.RawMessage
				if err := wire.ReadMsg(cs.scanner, &raw); err != nil {
					if err != io.EOF {
						cs.program.Send(errMsg{err: err})
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
					cs.program.Send(ackMsg{queryID: ack.QueryID})

				case "status":
					var status wire.StatusResponse
					if json.Unmarshal(raw, &status) != nil {
						continue
					}
					cs.program.Send(statusMsg{
						connected: status.Connected,
						total:     status.Total,
					})

				case wire.MsgSelectResponse:
					var resp wire.SelectResponse
					if json.Unmarshal(raw, &resp) != nil {
						continue
					}
					cs.program.Send(selectResponseMsg{
						success: resp.Success,
						message: resp.Message,
					})

				case "update":
					var upd wire.UpdateMessage
					if json.Unmarshal(raw, &upd) != nil {
						continue
					}
					var view tuiAggView
					if json.Unmarshal(upd.Payload, &view) != nil || view.QueryID == "" {
						continue
					}
					// Normalise list rows
					rows := make([]wire.ResultItem, 0, len(view.List))
					for _, it := range view.List {
						id := it.ID
						label := it.Label
						if id == "" {
							id = label
						}
						if label == "" {
							label = id
						}
						if id == "" && label == "" {
							continue
						}
						rows = append(rows, wire.ResultItem{
							ID:            id,
							Label:         label,
							Plugin:        it.Plugin,
							Score:         it.Score,
							FrecencyScore: it.FrecencyScore,
							Actions:       it.Actions,
						})
					}
					cs.program.Send(updateMsg{
						queryID: view.QueryID,
						rows:    rows,
						results: view.Results,
					})
				}
			}
		}()
		return nil // initial return is a no-op message
	}
}

// ─── Public entry point ───────────────────────────────────────────────────────

// RunTUI dials the daemon socket and runs the interactive Bubble Tea TUI.
func RunTUI() {
	debounceMs := viper.GetInt("tuidebounce")
	if debounceMs <= 0 {
		debounceMs = 200
	}

	clientID := fmt.Sprintf("cli-%d", time.Now().UnixNano())
	conn, err := net.Dial("unix", wire.SocketUI)
	if err != nil {
		fmt.Println("connect UI socket:", err)
		return
	}
	defer func() { _ = conn.Close() }()

	cs := &connState{
		conn:    conn,
		scanner: wire.NewScanner(conn),
	}

	m := NewModel(clientID, debounceMs)
	m.cs = cs

	p := tea.NewProgram(m, tea.WithAltScreen())
	cs.program = p

	if _, err := p.Run(); err != nil {
		fmt.Println("TUI error:", err)
	}
}

// ─── Helpers (kept for backward-compat / tests) ───────────────────────────────

// truncate shortens s to at most w visible characters, adding "…" if truncated.
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

// extractChoices tries to parse a plugin payload into a list of selectable items.
// It supports a few common shapes:
//   - { "suggestions": [ {"id":"...", "label"|"title"|"text":"..."}, ... ] }
//   - [ {"id":"...", ...}, ... ]
//   - [ "string", ... ] (id and label are the string)
func extractChoices(raw json.RawMessage) []choice {
	type resultWrap struct {
		ElapsedMs float64         `json:"elapsed_ms"`
		Data      json.RawMessage `json:"data"`
	}
	var wrap resultWrap
	if err := json.Unmarshal(raw, &wrap); err == nil && len(wrap.Data) > 0 {
		raw = wrap.Data
	}
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
	if chs := parseArrayChoices(raw); len(chs) > 0 {
		return chs
	}
	return nil
}

func parseArrayChoices(raw json.RawMessage) []choice {
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
