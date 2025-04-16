package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/go-zeromq/zmq4"
	"github.com/spf13/viper"

	"github.com/iMithrellas/tarragon/pkg/models"
)

const endpointUI = "ipc:///tmp/tarragon-ui.ipc"

// queryResponseMsg carries the result of a ZeroMQ query.
type queryResponseMsg struct {
	resp        models.Payload
	took        time.Duration
	err         error
	query       string
	isDebounced bool
}

// Define application states
type appState int

const (
	stateInput appState = iota
	stateLoading
)

// spinner provides loading animation
type spinner struct {
	frames  []string
	current int
}

func newSpinner() spinner {
	return spinner{
		frames:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		current: 0,
	}
}

func (s *spinner) next() string {
	frame := s.frames[s.current]
	s.current = (s.current + 1) % len(s.frames)
	return frame
}

// Styles for TUI components
var (
	containerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Align(lipgloss.Center).
			PaddingBottom(1)

	inputStyle = lipgloss.NewStyle().
			PaddingTop(1).
			PaddingBottom(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	loadingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("208")).
			Bold(true).
			PaddingBottom(1)

	responseStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("63")).
			PaddingBottom(1)

	tableContainerStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("240")).
				Padding(1, 1).
				MarginBottom(1)

	footerStyle = lipgloss.NewStyle().
			PaddingTop(1).
			Foreground(lipgloss.Color("240")).
			Align(lipgloss.Center)
)

// model is our Bubbletea model
type model struct {
	state            appState
	input            textinput.Model
	table            table.Model
	responseData     models.Payload
	responseTime     time.Duration
	errorMsg         string
	width            int
	height           int
	debouncer        *time.Timer
	mutex            sync.Mutex
	debounceDuration time.Duration
	spinner          spinner
	queryCache       map[string]models.Payload
	cacheMutex       sync.RWMutex
}

// initialModel sets up our starting state
func initialModel(debounceDuration time.Duration) model {
	ti := textinput.New()
	ti.Placeholder = "Type your query..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("62")).Bold(true)
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	return model{
		state:            stateInput,
		input:            ti,
		mutex:            sync.Mutex{},
		debounceDuration: debounceDuration,
		spinner:          newSpinner(),
		queryCache:       make(map[string]models.Payload),
	}
}

func (m *model) debounceQuery(query string) tea.Cmd {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.debouncer != nil {
		m.debouncer.Stop()
	}

	return tea.Tick(m.debounceDuration, func(t time.Time) tea.Msg {
		return queryResponseMsg{
			query:       query,
			isDebounced: true,
		}
	})
}

// buildRows creates table rows from the response data
func buildRows(resp models.Payload) []table.Row {
	var rows []table.Row
	for _, grp := range resp.Groups {
		for _, sug := range grp.Suggestions {
			row := table.Row{
				grp.Plugin,
				sug.Command,
				sug.IconPath,
				sug.Value,
				fmt.Sprintf("%.3f", sug.Weight),
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// rebuildTable rebuilds the table using the current terminal width
func rebuildTable(m model) table.Model {
	totalWidth := m.width - 10

	widthRatios := []float64{0.15, 0.25, 0.20, 0.30, 0.10}

	columns := []table.Column{
		{Title: "Plugin", Width: int(float64(totalWidth) * widthRatios[0])},
		{Title: "Command", Width: int(float64(totalWidth) * widthRatios[1])},
		{Title: "Icon Path", Width: int(float64(totalWidth) * widthRatios[2])},
		{Title: "Value", Width: int(float64(totalWidth) * widthRatios[3])},
		{Title: "Weight", Width: int(float64(totalWidth) * widthRatios[4])},
	}

	rows := buildRows(m.responseData)

	tableHeight := m.height - 13
	if tableHeight < 3 {
		tableHeight = 3
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithHeight(tableHeight),
		table.WithWidth(totalWidth),
	)

	styles := table.DefaultStyles()
	styles.Header = styles.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Align(lipgloss.Center)

	styles.Selected = styles.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true)

	t.SetStyles(styles)
	return t
}

// queryCommand sends the query over ZeroMQ
func queryCommand(query string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(query) == "" {
			return queryResponseMsg{
				resp: models.Payload{},
				took: 0,
			}
		}

		ctx := context.Background()
		req := zmq4.NewReq(ctx)
		if err := req.Dial(endpointUI); err != nil {
			return queryResponseMsg{err: fmt.Errorf("failed to connect: %w", err)}
		}
		defer req.Close()

		start := time.Now()
		msg := zmq4.NewMsg([]byte(query))
		if err := req.Send(msg); err != nil {
			return queryResponseMsg{err: fmt.Errorf("error sending message: %w", err)}
		}

		replyMsg, err := req.Recv()
		if err != nil {
			return queryResponseMsg{err: fmt.Errorf("error receiving reply: %w", err)}
		}
		took := time.Since(start)

		if len(replyMsg.Frames) == 0 {
			return queryResponseMsg{err: fmt.Errorf("empty reply")}
		}

		var res models.Payload
		if err := json.Unmarshal(replyMsg.Frames[0], &res); err != nil {
			return queryResponseMsg{err: fmt.Errorf("error parsing JSON: %w", err)}
		}

		return queryResponseMsg{
			resp: res,
			took: took,
		}
	}
}

// Init implements tea.Model
func (m model) Init() tea.Cmd {
	return textinput.Blink
}

// Update implements tea.Model
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if len(m.table.Rows()) > 0 {
			m.table = rebuildTable(m)
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)

			if strings.TrimSpace(m.input.Value()) != "" {
				cmds = append(cmds, m.debounceQuery(m.input.Value()))
			}
		}

	case queryResponseMsg:
		if msg.isDebounced {
			m.state = stateLoading
			return m, queryCommand(msg.query)
		}

		if msg.err != nil {
			m.errorMsg = msg.err.Error()
			m.state = stateInput
			return m, nil
		}

		m.responseData = msg.resp
		m.responseTime = msg.took
		m.table = rebuildTable(m)
		m.state = stateInput

		if len(m.table.Rows()) > 0 {
			var tableCmd tea.Cmd
			m.table, tableCmd = m.table.Update(msg)
			cmds = append(cmds, tableCmd)
		}
	}

	return m, tea.Batch(cmds...)
}

// View implements tea.Model
func (m model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Search Interface"))
	b.WriteString("\n")

	b.WriteString(inputStyle.Render(m.input.View()))
	b.WriteString("\n")

	if m.errorMsg != "" {
		b.WriteString(errorStyle.Render("Error: " + m.errorMsg))
		b.WriteString("\n\n")
	}

	if m.state == stateLoading {
		b.WriteString(loadingStyle.Render(
			fmt.Sprintf("%s Loading...", m.spinner.next())))
	} else if len(m.table.Rows()) > 0 {
		b.WriteString(responseStyle.Render(
			fmt.Sprintf("Response time: %s | Value: %s",
				m.responseTime,
				m.responseData.Value)))
		b.WriteString("\n")
		b.WriteString(tableContainerStyle.Render(m.table.View()))
	}

	b.WriteString(footerStyle.Render("Press 'ctrl+c' or 'ctrl+q' to quit"))

	return containerStyle.Render(b.String())
}

// RunTUI starts the Bubbletea program
func RunTUI() error {
	debounceMs := viper.GetInt("tuidebounce")

	if debounceMs < 50 {
		debounceMs = 50
	} else if debounceMs > 2000 {
		debounceMs = 2000
	}

	debounceDuration := time.Duration(debounceMs) * time.Millisecond

	p := tea.NewProgram(
		initialModel(debounceDuration),
		tea.WithAltScreen(),
	)
	return p.Start()
}
