package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-zeromq/zmq4"
	"github.com/iMithrellas/tarragon/internal/wire"
)

// Endpoints used to talk to the daemon.
const (
	endpointUIReq = "ipc:///tmp/tarragon-ui.ipc"      // REQ/REP for initiating a query
	endpointUISub = "ipc:///tmp/tarragon-updates.ipc" // PUB/SUB for async updates
)

// ackMessage is received after sending a query to the daemon.
type ackMessage struct {
	Type    string `json:"type"` // "ack"
	QueryID string `json:"query_id"`
}

// updateMessage is streamed over PUB/SUB with incremental results.
type updateMessage struct {
	Type    string          `json:"type"` // "update"
	QueryID string          `json:"query_id"`
	Payload json.RawMessage `json:"payload"` // Arbitrary payload (daemon uses models.Payload)
	Done    bool            `json:"done"`
}

func RunTUI() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// session client ID
	rand.Seed(time.Now().UnixNano())
	clientID := fmt.Sprintf("cli-%d-%d", time.Now().UnixNano(), rand.Intn(1_000_000))

	// REQ socket for starting queries and detach
	req := zmq4.NewReq(ctx)
	if err := req.Dial(wire.EndpointUIReq); err != nil {
		fmt.Fprintf(os.Stderr, "connect error (REQ): %v\n", err)
		os.Exit(1)
	}
	defer req.Close()

	// SUB socket for receiving updates.
	sub := zmq4.NewSub(ctx)
	if err := sub.Dial(wire.EndpointUISub); err != nil {
		fmt.Fprintf(os.Stderr, "connect error (SUB): %v\n", err)
		os.Exit(1)
	}
	defer sub.Close()

	// background receiver
	var subMu sync.Mutex
	active := make(map[string]struct{})
	lastLines := 0
	doneCh := make(chan struct{})
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
			topic := string(msg.Frames[0])
			subMu.Lock()
			_, ok := active[topic]
			subMu.Unlock()
			if !ok {
				continue
			}
			var upd wire.UpdateMessage
			if err := json.Unmarshal(msg.Frames[1], &upd); err != nil {
				fmt.Fprintf(os.Stderr, "update parse error: %v\n", err)
				continue
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
			lastLines = lines
			fmt.Print("\0338") // restore cursor
		}
	}()

	in := bufio.NewReader(os.Stdin)
	fmt.Println("tarragon-cli: type a query and press Enter. (:q to quit)")
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
