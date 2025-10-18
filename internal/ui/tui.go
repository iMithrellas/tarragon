package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-zeromq/zmq4"
	"github.com/iMithrellas/tarragon/internal/wire"
)

// choice represents a selectable suggestion from a plugin.
type choice struct{ ID, Label string }

func RunTUI() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// session client ID (time component is sufficient)
	clientID := fmt.Sprintf("cli-%d", time.Now().UnixNano())

	// REQ socket for starting queries and detach
	req := zmq4.NewReq(ctx)
	if err := req.Dial(wire.EndpointUIReq); err != nil {
		fmt.Fprintf(os.Stderr, "connect error (REQ): %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = req.Close() }()

	// SUB socket for receiving updates.
	sub := zmq4.NewSub(ctx)
	if err := sub.Dial(wire.EndpointUISub); err != nil {
		fmt.Fprintf(os.Stderr, "connect error (SUB): %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = sub.Close() }()

	// background receiver
	var subMu sync.Mutex
	active := make(map[string]struct{})
	lastLines := 0
	doneCh := make(chan struct{})
	// track last ack id and last plugin list for selection
	var lastQID string
	var lastPlugins []string
	// choices extracted per plugin from latest snapshot
	lastChoices := make(map[string][]choice)
	go func() {
		for {
			select {
			case <-doneCh:
				return
			default:
			}
			msg, err := sub.Recv()
			if err != nil {
				fmt.Fprintf(os.Stderr, "update recv error: %v\n", err)
				return
			}
			if len(msg.Frames) < 2 {
				continue
			}
			subMu.Lock()
			_, ok := active[string(msg.Frames[0])]
			subMu.Unlock()
			if !ok {
				continue
			}
			var upd wire.UpdateMessage
			if err := json.Unmarshal(msg.Frames[1], &upd); err != nil {
				fmt.Fprintf(os.Stderr, "update parse error: %v\n", err)
				continue
			}
			// Try to extract plugin names from aggregate payload
			type aggView struct {
				QueryID string                     `json:"query_id"`
				Results map[string]json.RawMessage `json:"results"`
			}
			var view aggView
			_ = json.Unmarshal(upd.Payload, &view)
			if view.QueryID != "" {
				lastQID = view.QueryID
				lastPlugins = lastPlugins[:0]
				for name, raw := range view.Results {
					lastPlugins = append(lastPlugins, name)
					lastChoices[name] = extractChoices(raw)
				}
			}
			pretty, _ := json.MarshalIndent(json.RawMessage(upd.Payload), "", "  ")
			// Replace previous snapshot with the new one using ANSI.
			s := string(pretty)
			lines := 1
			for i := 0; i < len(s); i++ {
				if s[i] == '\n' {
					lines++
				}
			}
			// Save cursor, move to start of block, clear, print, restore.
			// Best-effort: may interfere with the prompt while typing.
			fmt.Print("\0337") // save cursor
			if lastLines > 0 {
				// Move to beginning of the block located lastLines above.
				fmt.Printf("\r\033[%dA", lastLines)
				// Clear lastLines lines.
				for i := 0; i < lastLines; i++ {
					fmt.Print("\033[2K") // clear line
					if i < lastLines-1 {
						fmt.Print("\033[1B")
					} // move down
				}
				// Move back to top of block.
				if lastLines > 1 {
					fmt.Printf("\033[%dA", lastLines-1)
				}
			}
			fmt.Print(s)
			if !strings.HasSuffix(s, "\n") {
				fmt.Print("\n")
			}
			if len(lastPlugins) > 0 {
				fmt.Printf("plugins: %s\n", strings.Join(lastPlugins, ", "))
				for _, p := range lastPlugins {
					chs := lastChoices[p]
					if len(chs) == 0 {
						continue
					}
					fmt.Printf("  %s:\n", p)
					for i, c := range chs {
						if c.Label == "" {
							c.Label = c.ID
						}
						fmt.Printf("    [%d] %s (id=%s)\n", i, c.Label, c.ID)
					}
				}
			}
			lastLines = lines
			fmt.Print("\0338") // restore cursor
		}
	}()

	in := bufio.NewReader(os.Stdin)
	fmt.Println("tarragon-cli: type a query and press Enter. Commands: :q quit, :sel <plugin>")
	for {
		fmt.Print("> ")
		line, err := in.ReadString('\n')
		if err != nil {
			fmt.Println()
			break
		}
		query := strings.TrimSpace(line)
		if query == "" {
			continue
		}
		switch strings.ToLower(query) {
		case ":q", "q", "quit", "exit":
			// best-effort detach
			reqBody, _ := json.Marshal(&wire.UIRequest{Type: "detach", ClientID: clientID})
			_ = req.Send(zmq4.NewMsg(reqBody))
			_, _ = req.Recv()
			close(doneCh)
			return
		}

		if strings.HasPrefix(strings.ToLower(query), ":sel ") {
			args := strings.Fields(query)
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: :sel <plugin> [index|id]")
				continue
			}
			pname := args[1]
			if pname == "" || lastQID == "" {
				fmt.Fprintln(os.Stderr, "no selection available or missing plugin")
				continue
			}
			// Determine selection token to send (id preferred)
			var token string
			if len(args) >= 3 {
				idxOrID := args[2]
				// try index
				if i, err := atoi(idxOrID); err == nil {
					chs := lastChoices[pname]
					if i >= 0 && i < len(chs) {
						token = chs[i].ID
					}
				}
				if token == "" {
					token = idxOrID
				}
			}
			reqBody, _ := json.Marshal(&wire.UIRequest{Type: "select", ClientID: clientID, QueryID: lastQID, Plugin: pname, Text: token})
			if err := req.Send(zmq4.NewMsg(reqBody)); err != nil {
				fmt.Fprintf(os.Stderr, "send select error: %v\n", err)
				continue
			}
			_, _ = req.Recv() // ack ok
			fmt.Printf("sent selection to %s for %s (token=%s)\n", pname, lastQID, token)
			continue
		}

		// send JSON query
		reqBody, _ := json.Marshal(&wire.UIRequest{Type: "query", ClientID: clientID, Text: query})
		if err := req.Send(zmq4.NewMsg(reqBody)); err != nil {
			fmt.Fprintf(os.Stderr, "send error: %v\n", err)
			continue
		}

		// expect ack and subscribe to topic
		reply, err := req.Recv()
		if err != nil || len(reply.Frames) == 0 {
			fmt.Fprintf(os.Stderr, "ack error: %v\n", err)
			continue
		}
		var ack wire.AckMessage
		if err := json.Unmarshal(reply.Frames[0], &ack); err != nil || ack.QueryID == "" {
			fmt.Fprintf(os.Stderr, "ack parse error: %v\n", err)
			continue
		}
		subMu.Lock()
		for t := range active {
			_ = sub.SetOption(zmq4.OptionUnsubscribe, t)
		}
		active = make(map[string]struct{})
		lastLines = 0
		_ = sub.SetOption(zmq4.OptionSubscribe, ack.QueryID)
		active[ack.QueryID] = struct{}{}
		subMu.Unlock()
		fmt.Printf("listening for updates id=%s...\n", ack.QueryID)
	}
}

// atoi is a tiny helper that parses an int without importing strconv at top-level
func atoi(s string) (int, error) { return strconv.Atoi(s) }

// extractChoices tries to parse a plugin payload into a list of selectable items.
// It supports a few common shapes to keep the testing UI useful:
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
	// Try object with suggestions field
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		if arr, ok := obj["suggestions"]; ok {
			if chs := parseArrayChoices(arr); len(chs) > 0 {
				return chs
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
