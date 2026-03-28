package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
)

type BenchOptions struct {
	RandomInputs int
	Iterations   int
	Timeout      time.Duration
	Seed         int64
	WorstPct     float64
}

type benchStats struct {
	name     string
	count    int
	sumMs    float64
	minMs    float64
	maxMs    float64
	timesMs  []float64
	inputs   []string
	timeouts int
	errors   int
}

type benchMsg struct {
	plugin string
	input  string
	ms     float64
	err    error
	done   bool
}

type benchModel struct {
	table    table.Model
	stats    map[string]*benchStats
	order    []string
	total    int
	done     bool
	worstPct float64
	fatalErr string
}

func RunBenchTUI(opts BenchOptions) error {
	if opts.RandomInputs <= 0 {
		opts.RandomInputs = 100
	}
	if opts.Iterations <= 0 {
		opts.Iterations = 1
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Second
	}
	if opts.Seed == 0 {
		opts.Seed = time.Now().UnixNano()
	}
	if opts.WorstPct <= 0 || opts.WorstPct >= 100 {
		opts.WorstPct = 99
	}

	msgs := make(chan benchMsg, 128)
	mgr := plugins.NewManager(plugins.DefaultDir())
	if err := mgr.Discover(); err != nil {
		return err
	}

	stats := make(map[string]*benchStats)
	var order []string
	for name, p := range mgr.Plugins {
		if !p.Config.Enabled {
			continue
		}
		stats[name] = &benchStats{name: name}
		order = append(order, name)
	}
	sort.Strings(order)

	go runBench(context.Background(), mgr, opts, msgs)

	columns := []table.Column{
		{Title: "Plugin", Width: 22},
		{Title: "Runs", Width: 6},
		{Title: "Avg ms", Width: 10},
		{Title: "Min", Width: 8},
		{Title: "Max", Width: 8},
		{Title: "Timeouts", Width: 9},
	}

	t := table.New(table.WithColumns(columns), table.WithRows(make([]table.Row, 0)))
	t.SetStyles(defaultTableStyles())

	m := &benchModel{
		table:    t,
		stats:    stats,
		order:    order,
		total:    0,
		done:     false,
		worstPct: opts.WorstPct,
	}

	perPlugin := (opts.RandomInputs + len(interestingInputs)) * opts.Iterations
	m.total = perPlugin * len(order)

	p := tea.NewProgram(m, tea.WithAltScreen())
	go func() {
		for msg := range msgs {
			p.Send(msg)
		}
	}()
	_, err := p.Run()
	return err
}

func (m *benchModel) Init() tea.Cmd {
	return nil
}

func (m *benchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case benchMsg:
		if msg.plugin == "" && msg.err != nil {
			m.fatalErr = msg.err.Error()
			m.done = true
			return m, nil
		}
		if msg.done {
			m.done = true
			return m, nil
		}
		st := m.stats[msg.plugin]
		if st == nil {
			st = &benchStats{name: msg.plugin}
			m.stats[msg.plugin] = st
			m.order = append(m.order, msg.plugin)
			sort.Strings(m.order)
		}
		switch msg.err {
		case nil:
			st.count++
			st.sumMs += msg.ms
			if st.count == 1 || msg.ms < st.minMs {
				st.minMs = msg.ms
			}
			if msg.ms > st.maxMs {
				st.maxMs = msg.ms
			}
			st.timesMs = append(st.timesMs, msg.ms)
			st.inputs = append(st.inputs, msg.input)
		case context.DeadlineExceeded:
			st.timeouts++
		default:
			st.errors++
		}
		m.refreshRows()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		default:
			var cmd tea.Cmd
			m.table, cmd = m.table.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *benchModel) View() string {
	var b strings.Builder
	b.WriteString(m.table.View())
	b.WriteString("\n")

	doneCount := 0
	for _, st := range m.stats {
		doneCount += st.count + st.timeouts + st.errors
	}

	if m.done {
		fmt.Fprintf(&b, "Benchmark complete (%d/%d). Press q to quit.\n", doneCount, m.total)
	} else {
		fmt.Fprintf(&b, "Benchmark running (%d/%d). Press q to quit.\n", doneCount, m.total)
	}
	if m.fatalErr != "" {
		b.WriteString("Error: " + m.fatalErr + "\n")
	}

	selected := m.table.SelectedRow()
	if len(selected) > 0 {
		name := selected[0]
		if st := m.stats[name]; st != nil {
			worstLines := worstInputs(st, m.worstPct)
			if len(worstLines) > 0 {
				fmt.Fprintf(&b, "Worst p%.0f inputs:\n", m.worstPct)
				for _, line := range worstLines {
					b.WriteString("  " + line + "\n")
				}
			}
		}
	}
	return b.String()
}

func (m *benchModel) refreshRows() {
	rows := make([]table.Row, 0, len(m.order))
	for _, name := range m.order {
		st := m.stats[name]
		if st == nil {
			continue
		}
		avg := 0.0
		if st.count > 0 {
			avg = st.sumMs / float64(st.count)
		}
		row := table.Row{
			name,
			fmt.Sprintf("%d", st.count),
			formatMs(avg),
			formatMs(st.minMs),
			formatMs(st.maxMs),
			fmt.Sprintf("%d", st.timeouts),
		}
		rows = append(rows, row)
	}
	m.table.SetRows(rows)
}

func runBench(ctx context.Context, mgr *plugins.Manager, opts BenchOptions, out chan<- benchMsg) {
	defer func() { out <- benchMsg{done: true} }()

	conn, err := net.Dial("unix", wire.SocketUI)
	if err != nil {
		out <- benchMsg{err: err}
		return
	}
	defer func() { _ = conn.Close() }()
	scanner := wire.NewScanner(conn)
	var writeMu sync.Mutex

	inputs := benchInputs(opts)

	var general []string
	var prefixOnly []string
	for name, p := range mgr.Plugins {
		if !p.Config.Enabled {
			continue
		}
		if p.Config.RequirePrefix && strings.TrimSpace(p.Config.Prefix) == "" {
			out <- benchMsg{plugin: name, err: fmt.Errorf("missing prefix")}
			continue
		}
		if p.Config.RequirePrefix {
			prefixOnly = append(prefixOnly, name)
		} else {
			general = append(general, name)
		}
	}
	sort.Strings(general)
	sort.Strings(prefixOnly)

	for _, input := range inputs {
		for i := 0; i < opts.Iterations; i++ {
			if ctx.Err() != nil {
				return
			}
			if len(general) > 0 {
				results, missing, err := runQueryAll(conn, scanner, &writeMu, input, general, opts.Timeout)
				if err != nil {
					out <- benchMsg{err: err}
					return
				}
				for name, ms := range results {
					out <- benchMsg{plugin: name, input: input, ms: ms, err: nil}
				}
				for _, name := range missing {
					out <- benchMsg{plugin: name, input: input, err: context.DeadlineExceeded}
				}
			}
			for _, name := range prefixOnly {
				p := mgr.Plugins[name]
				prefix := strings.TrimSpace(p.Config.Prefix)
				query := prefix + " " + input
				results, missing, err := runQueryAll(conn, scanner, &writeMu, query, []string{name}, opts.Timeout)
				if err != nil {
					out <- benchMsg{err: err}
					return
				}
				for plugin, ms := range results {
					out <- benchMsg{plugin: plugin, input: input, ms: ms, err: nil}
				}
				for _, plugin := range missing {
					out <- benchMsg{plugin: plugin, input: input, err: context.DeadlineExceeded}
				}
			}
		}
	}
}

func runQueryAll(conn net.Conn, scanner *bufio.Scanner, writeMu *sync.Mutex, text string, targets []string, timeout time.Duration) (map[string]float64, []string, error) {
	if len(targets) == 0 {
		return nil, nil, nil
	}
	expect := make(map[string]struct{}, len(targets))
	for _, name := range targets {
		expect[name] = struct{}{}
	}

	writeMu.Lock()
	err := wire.WriteMsg(conn, &wire.UIRequest{Type: "query", ClientID: "bench", Text: text})
	writeMu.Unlock()
	if err != nil {
		return nil, nil, err
	}

	received := make(map[string]float64)
	deadline := time.Now().Add(timeout)
	var qid string

	for len(received) < len(expect) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return received, nil, err
		}
		var raw json.RawMessage
		if err := wire.ReadMsg(scanner, &raw); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			if err == io.EOF {
				return received, nil, fmt.Errorf("bench updates closed")
			}
			return received, nil, err
		}
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &kind) != nil {
			continue
		}
		switch kind.Type {
		case "ack":
			if qid != "" {
				continue
			}
			var ack wire.AckMessage
			if json.Unmarshal(raw, &ack) != nil || ack.QueryID == "" {
				continue
			}
			qid = ack.QueryID
		case "update":
			var upd wire.UpdateMessage
			if json.Unmarshal(raw, &upd) != nil || upd.QueryID == "" {
				continue
			}
			if qid == "" || upd.QueryID != qid {
				continue
			}
			var view aggView
			if json.Unmarshal(upd.Payload, &view) != nil || view.QueryID == "" {
				continue
			}
			for name, res := range view.Results {
				if _, ok := expect[name]; !ok {
					continue
				}
				if _, seen := received[name]; seen {
					continue
				}
				received[name] = res.ElapsedMs
			}
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
	var missing []string
	for name := range expect {
		if _, ok := received[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return received, missing, nil
}

type aggView struct {
	QueryID string               `json:"query_id"`
	Results map[string]aggResult `json:"results"`
}

type aggResult struct {
	ElapsedMs float64 `json:"elapsed_ms"`
}

func benchInputs(opts BenchOptions) []string {
	inputs := append([]string{}, interestingInputs...)
	rng := rand.New(rand.NewSource(opts.Seed))
	for i := 0; i < opts.RandomInputs; i++ {
		inputs = append(inputs, randomExpr(rng))
	}
	return inputs
}

func worstInputs(st *benchStats, pct float64) []string {
	if st == nil || len(st.timesMs) == 0 {
		return nil
	}
	if pct <= 0 || pct >= 100 {
		pct = 99
	}
	type entry struct {
		idx int
		ms  float64
	}
	entries := make([]entry, 0, len(st.timesMs))
	for i, ms := range st.timesMs {
		entries = append(entries, entry{idx: i, ms: ms})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ms < entries[j].ms })
	cut := int(math.Ceil((pct/100.0)*float64(len(entries)))) - 1
	if cut < 0 {
		cut = 0
	}
	threshold := entries[cut].ms
	var out []string
	for _, e := range entries {
		if e.ms >= threshold {
			inp := ""
			if e.idx < len(st.inputs) {
				inp = st.inputs[e.idx]
			}
			out = append(out, fmt.Sprintf("%.2f ms\t%s", e.ms, inp))
		}
	}
	return out
}

func formatMs(ms float64) string {
	if ms <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", ms)
}

func defaultTableStyles() table.Styles {
	s := table.DefaultStyles()
	return s
}

func randomExpr(r *rand.Rand) string {
	funcs := []string{"sqrt", "sin", "cos", "tan", "abs", "log"}
	ops := []string{"+", "-", "*", "/", "%", "**"}

	pickNum := func() string {
		n := r.Intn(1000)
		if r.Float64() < 0.2 {
			return fmt.Sprintf("%d.%d", n, r.Intn(100))
		}
		return fmt.Sprintf("%d", n)
	}

	switch r.Intn(4) {
	case 0:
		return fmt.Sprintf("%s%s%s", pickNum(), ops[r.Intn(len(ops))], pickNum())
	case 1:
		return fmt.Sprintf("(%s%s%s)%s%s", pickNum(), ops[r.Intn(len(ops))], pickNum(), ops[r.Intn(len(ops))], pickNum())
	case 2:
		fn := funcs[r.Intn(len(funcs))]
		return fmt.Sprintf("%s(%s)", fn, pickNum())
	default:
		return fmt.Sprintf("%s %s %s", pickNum(), ops[r.Intn(len(ops))], pickNum())
	}
}

var interestingInputs = []string{
	"2+2",
	"3*7-4",
	"1/3",
	"sqrt(9)",
	"sin(pi/2)",
	"log(10)",
	"2**8",
	"5%2",
	"floor(2.9)",
	"ceil(2.1)",
	"abs(-42)",
	"cos(0)",
	"tan(pi/4)",
	"((2+3)*4)/5",
	"7//2",
	"3*(2+(5-1))",
	"0.1+0.2",
	"pi*2",
	"e+1",
	"(8-3)^(2)",
	"12/(2+4)",
	"1000-999.5",
	"hello world",
	"sum(1,2)",
}
