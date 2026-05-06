package bench

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/iMithrellas/tarragon/internal/wire"
)

type Options struct {
	RandomInputs int
	Iterations   int
	Timeout      time.Duration
	Seed         int64
	WorstPct     float64
	Socket       string
}

type options struct {
	randomInputs int
	iterations   int
	timeout      time.Duration
	seed         int64
	worstPct     float64
	socket       string
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

type pluginState struct {
	State     string  `json:"state"`
	Count     int     `json:"count,omitempty"`
	ElapsedMs float64 `json:"elapsed_ms,omitempty"`
	Error     string  `json:"error,omitempty"`
}

type aggregateView struct {
	QueryID string                 `json:"query_id"`
	Plugins map[string]pluginState `json:"plugins"`
}

type queryOutcome struct {
	elapsedMs     map[string]float64
	errors        map[string]string
	missing       []string
	shouldRefresh bool
}

type client struct {
	socket   string
	clientID string
	conn     net.Conn
	scanner  *bufio.Scanner
}

func Run(cfg Options, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	return run(options{
		randomInputs: cfg.RandomInputs,
		iterations:   cfg.Iterations,
		timeout:      cfg.Timeout,
		seed:         cfg.Seed,
		worstPct:     cfg.WorstPct,
		socket:       cfg.Socket,
	}, out)
}

func run(opts options, out io.Writer) error {
	if opts.randomInputs < 0 {
		return fmt.Errorf("random must be >= 0")
	}
	if opts.iterations <= 0 {
		opts.iterations = 1
	}
	if opts.timeout <= 0 {
		opts.timeout = 2 * time.Second
	}
	if opts.seed == 0 {
		opts.seed = time.Now().UnixNano()
	}
	if opts.worstPct <= 0 || opts.worstPct >= 100 {
		opts.worstPct = 99
	}
	if opts.socket == "" {
		opts.socket = wire.SocketUI
	}

	c := &client{
		socket:   opts.socket,
		clientID: fmt.Sprintf("bench-%d", time.Now().UnixNano()),
	}
	if err := c.connect(); err != nil {
		return err
	}
	defer c.close()
	defer c.detach()

	status, err := c.requestStatus(opts.timeout)
	if err != nil {
		return err
	}

	stats, general, prefixOnly := classifyPlugins(status.Plugins)
	if len(stats) == 0 {
		return fmt.Errorf("no enabled plugins reported by daemon")
	}
	if len(general) == 0 && len(prefixOnly) == 0 {
		return fmt.Errorf("no benchmarkable plugins reported by daemon")
	}

	inputs := benchInputs(opts)
	for i := 0; i < opts.iterations; i++ {
		for _, input := range inputs {
			if len(general) > 0 {
				outcome, err := c.runQuery(input, general, opts.timeout)
				if err != nil {
					return err
				}
				recordOutcome(stats, input, outcome)
			}
			for _, plugin := range prefixOnly {
				query := strings.TrimSpace(plugin.Prefix + " " + input)
				outcome, err := c.runQuery(query, []string{plugin.Name}, opts.timeout)
				if err != nil {
					return err
				}
				recordOutcome(stats, input, outcome)
			}
		}
	}

	printSummary(out, stats, opts)
	return nil
}

func (c *client) connect() error {
	c.close()
	conn, err := net.Dial("unix", c.socket)
	if err != nil {
		return fmt.Errorf("connect %s: %w", c.socket, err)
	}
	c.conn = conn
	c.scanner = wire.NewScanner(conn)
	return nil
}

func (c *client) close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = nil
	c.scanner = nil
}

func (c *client) detach() {
	if c.conn == nil {
		return
	}
	_ = wire.WriteMsg(c.conn, &wire.UIRequest{Type: "detach", ClientID: c.clientID})
}

func (c *client) refresh() error {
	return c.connect()
}

func (c *client) requestStatus(timeout time.Duration) (wire.StatusResponse, error) {
	if err := wire.WriteMsg(c.conn, &wire.UIRequest{Type: "status", ClientID: c.clientID}); err != nil {
		return wire.StatusResponse{}, fmt.Errorf("write status request: %w", err)
	}
	if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return wire.StatusResponse{}, err
	}
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()

	for {
		var raw json.RawMessage
		if err := wire.ReadMsg(c.scanner, &raw); err != nil {
			if isTimeout(err) {
				return wire.StatusResponse{}, fmt.Errorf("status response timed out after %s", timeout)
			}
			return wire.StatusResponse{}, fmt.Errorf("read status response: %w", err)
		}
		kind, ok := messageType(raw)
		if !ok || kind != wire.MsgStatus {
			continue
		}
		var status wire.StatusResponse
		if err := json.Unmarshal(raw, &status); err != nil {
			return wire.StatusResponse{}, fmt.Errorf("decode status response: %w", err)
		}
		return status, nil
	}
}

func (c *client) runQuery(text string, targets []string, timeout time.Duration) (queryOutcome, error) {
	outcome, err := runQuery(c.conn, c.scanner, c.clientID, text, targets, timeout)
	if err == nil {
		if outcome.shouldRefresh {
			if refreshErr := c.refresh(); refreshErr != nil {
				return outcome, refreshErr
			}
		}
		return outcome, nil
	}

	if !isConnectionFailure(err) {
		return outcome, err
	}

	if refreshErr := c.refresh(); refreshErr != nil {
		return outcome, refreshErr
	}
	outcome, err = runQuery(c.conn, c.scanner, c.clientID, text, targets, timeout)
	if err == nil && outcome.shouldRefresh {
		err = c.refresh()
	}
	return outcome, err
}

func classifyPlugins(infos []wire.PluginInfo) (map[string]*benchStats, []string, []wire.PluginInfo) {
	stats := make(map[string]*benchStats)
	generalSet := make(map[string]struct{})
	prefixByName := make(map[string]wire.PluginInfo)

	for _, info := range infos {
		if !info.Enabled {
			continue
		}
		stats[info.Name] = &benchStats{name: info.Name}
		if !info.RequirePrefix && info.ProvidesGeneral {
			generalSet[info.Name] = struct{}{}
			continue
		}
		if strings.TrimSpace(info.Prefix) != "" {
			prefixByName[info.Name] = info
		}
	}

	general := make([]string, 0, len(generalSet))
	for name := range generalSet {
		general = append(general, name)
	}
	sort.Strings(general)

	prefixOnly := make([]wire.PluginInfo, 0, len(prefixByName))
	for _, info := range prefixByName {
		prefixOnly = append(prefixOnly, info)
	}
	sort.Slice(prefixOnly, func(i, j int) bool { return prefixOnly[i].Name < prefixOnly[j].Name })

	return stats, general, prefixOnly
}

func runQuery(conn net.Conn, scanner *bufio.Scanner, clientID, text string, targets []string, timeout time.Duration) (queryOutcome, error) {
	expect := make(map[string]struct{}, len(targets))
	for _, name := range targets {
		expect[name] = struct{}{}
	}
	outcome := queryOutcome{
		elapsedMs: make(map[string]float64),
		errors:    make(map[string]string),
	}

	if err := wire.WriteMsg(conn, &wire.UIRequest{Type: "query", ClientID: clientID, Text: text}); err != nil {
		return outcome, fmt.Errorf("write query %q: %w", text, err)
	}

	deadline := time.Now().Add(timeout)
	var qid string
	for len(outcome.elapsedMs)+len(outcome.errors) < len(expect) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return outcome, err
		}
		var raw json.RawMessage
		if err := wire.ReadMsg(scanner, &raw); err != nil {
			if isTimeout(err) {
				outcome.shouldRefresh = true
				break
			}
			if errors.Is(err, io.EOF) {
				return outcome, fmt.Errorf("daemon closed UI socket")
			}
			return outcome, err
		}

		kind, ok := messageType(raw)
		if !ok {
			continue
		}
		switch kind {
		case "ack":
			if qid != "" {
				continue
			}
			var ack wire.AckMessage
			if json.Unmarshal(raw, &ack) == nil {
				qid = ack.QueryID
			}
		case "update":
			var upd wire.UpdateMessage
			if json.Unmarshal(raw, &upd) != nil || upd.QueryID == "" {
				continue
			}
			if qid == "" || upd.QueryID != qid {
				continue
			}
			applyUpdate(upd.Payload, expect, outcome)
		}
	}
	_ = conn.SetReadDeadline(time.Time{})

	for name := range expect {
		if _, ok := outcome.elapsedMs[name]; ok {
			continue
		}
		if _, ok := outcome.errors[name]; ok {
			continue
		}
		outcome.missing = append(outcome.missing, name)
	}
	sort.Strings(outcome.missing)
	return outcome, nil
}

func applyUpdate(payload json.RawMessage, expect map[string]struct{}, outcome queryOutcome) {
	var view aggregateView
	if json.Unmarshal(payload, &view) != nil {
		return
	}
	for name, state := range view.Plugins {
		if _, ok := expect[name]; !ok {
			continue
		}
		if _, seen := outcome.elapsedMs[name]; seen {
			continue
		}
		if _, seen := outcome.errors[name]; seen {
			continue
		}
		switch state.State {
		case "done", "empty":
			outcome.elapsedMs[name] = state.ElapsedMs
		case "error":
			if state.Error == "" {
				state.Error = "plugin returned error"
			}
			outcome.errors[name] = state.Error
		}
	}
}

func recordOutcome(stats map[string]*benchStats, input string, outcome queryOutcome) {
	for name, ms := range outcome.elapsedMs {
		st := stats[name]
		if st == nil {
			continue
		}
		st.count++
		st.sumMs += ms
		if st.count == 1 || ms < st.minMs {
			st.minMs = ms
		}
		if ms > st.maxMs {
			st.maxMs = ms
		}
		st.timesMs = append(st.timesMs, ms)
		st.inputs = append(st.inputs, input)
	}
	for name := range outcome.errors {
		if st := stats[name]; st != nil {
			st.errors++
		}
	}
	for _, name := range outcome.missing {
		if st := stats[name]; st != nil {
			st.timeouts++
		}
	}
}

func printSummary(w io.Writer, stats map[string]*benchStats, opts options) {
	names := make([]string, 0, len(stats))
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintf(w, "Tarragon benchmark\n")
	fmt.Fprintf(w, "seed=%d random=%d iterations=%d timeout=%s\n\n", opts.seed, opts.randomInputs, opts.iterations, opts.timeout)
	printTable(w, names, stats, opts.worstPct)
}

func printTable(w io.Writer, names []string, stats map[string]*benchStats, worstPct float64) {
	headers := []string{"Plugin", "Runs", "Avg ms", "Min ms", "Max ms", fmt.Sprintf("P%.0f ms", worstPct), "Timeouts", "Errors"}
	rows := make([][]string, 0, len(names))
	for _, name := range names {
		st := stats[name]
		rows = append(rows, []string{
			st.name,
			fmt.Sprintf("%d", st.count),
			formatMs(avgMs(st)),
			formatMs(st.minMs),
			formatMs(st.maxMs),
			formatMs(percentile(st.timesMs, worstPct)),
			fmt.Sprintf("%d", st.timeouts),
			fmt.Sprintf("%d", st.errors),
		})
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	printRow(w, headers, widths)
	printSeparator(w, widths)
	for _, row := range rows {
		printRow(w, row, widths)
	}
}

func printRow(w io.Writer, cells []string, widths []int) {
	for i, cell := range cells {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		if i == 0 {
			fmt.Fprintf(w, "%-*s", widths[i], cell)
			continue
		}
		fmt.Fprintf(w, "%*s", widths[i], cell)
	}
	fmt.Fprintln(w)
}

func printSeparator(w io.Writer, widths []int) {
	for i, width := range widths {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		fmt.Fprint(w, strings.Repeat("-", width))
	}
	fmt.Fprintln(w)
}

func avgMs(st *benchStats) float64 {
	if st == nil || st.count == 0 {
		return 0
	}
	return st.sumMs / float64(st.count)
}

func percentile(values []float64, pct float64) float64 {
	if len(values) == 0 {
		return 0
	}
	vals := append([]float64(nil), values...)
	sort.Float64s(vals)
	idx := int(math.Ceil((pct/100.0)*float64(len(vals)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx]
}

func formatMs(ms float64) string {
	if ms <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", ms)
}

func messageType(raw json.RawMessage) (string, bool) {
	var kind struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil || kind.Type == "" {
		return "", false
	}
	return kind.Type, true
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isConnectionFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "broken pipe") ||
		strings.Contains(errText, "connection reset") ||
		strings.Contains(errText, "daemon closed ui socket")
}

func benchInputs(opts options) []string {
	inputs := append([]string{}, interestingInputs...)
	rng := rand.New(rand.NewSource(opts.seed))
	for i := 0; i < opts.randomInputs; i++ {
		inputs = append(inputs, randomExpr(rng))
	}
	return inputs
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
