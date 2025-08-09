package ui

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    "github.com/go-zeromq/zmq4"
)

// Endpoints used to talk to the daemon.
const (
    endpointUIReq  = "ipc:///tmp/tarragon-ui.ipc"      // REQ/REP for initiating a query
    endpointUISub  = "ipc:///tmp/tarragon-updates.ipc" // PUB/SUB for async updates
)

// ackMessage is received after sending a query to the daemon.
type ackMessage struct {
    Type    string `json:"type"`     // "ack"
    QueryID string `json:"query_id"`
}

// updateMessage is streamed over PUB/SUB with incremental results.
type updateMessage struct {
    Type    string          `json:"type"`     // "update"
    QueryID string          `json:"query_id"`
    Payload json.RawMessage `json:"payload"`  // Arbitrary payload (daemon uses models.Payload)
    Done    bool            `json:"done"`
}

func RunTUI() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Handle Ctrl-C (SIGINT) and SIGTERM.
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
    go func() {
        <-sigCh
        fmt.Println("\nbye")
        cancel()
    }()

    // REQ socket for starting queries.
    req := zmq4.NewReq(ctx)
    if err := req.Dial(endpointUIReq); err != nil {
        fmt.Fprintf(os.Stderr, "connect error (REQ): %v\n", err)
        os.Exit(1)
    }
    defer req.Close()

    // SUB socket for receiving updates.
    sub := zmq4.NewSub(ctx)
    if err := sub.Dial(endpointUISub); err != nil {
        fmt.Fprintf(os.Stderr, "connect error (SUB): %v\n", err)
        os.Exit(1)
    }
    defer sub.Close()
    // We will set subscription per-query after receiving an ACK (to the query ID topic).

    in := bufio.NewReader(os.Stdin)
    fmt.Println("tarragon-cli: type a query and press Enter. (Ctrl-C to quit)")
    for {
        fmt.Print("> ")
        line, err := in.ReadString('\n')
        if err != nil {
            fmt.Println()
            return
        }
        query := strings.TrimSpace(line)
        if query == "" {
            continue
        }
        switch strings.ToLower(query) {
        case ":q", "q", "quit", "exit":
            return
        }

        start := time.Now()
        // Send the raw query as a single frame for now.
        if err := req.Send(zmq4.NewMsg([]byte(query))); err != nil {
            fmt.Fprintf(os.Stderr, "send error: %v\n", err)
            continue
        }

        // Expect an ACK containing a query ID.
        reply, err := req.Recv()
        if err != nil {
            fmt.Fprintf(os.Stderr, "recv error: %v\n", err)
            continue
        }
        if len(reply.Frames) == 0 {
            fmt.Fprintln(os.Stderr, "empty ack reply")
            continue
        }
        var ack ackMessage
        if err := json.Unmarshal(reply.Frames[0], &ack); err != nil {
            fmt.Fprintf(os.Stderr, "ack parse error: %v\n", err)
            continue
        }
        if ack.QueryID == "" {
            fmt.Fprintln(os.Stderr, "missing query id in ack")
            continue
        }

        // Subscribe to updates for this query ID (topic = queryID).
        if err := sub.SetOption(zmq4.OptionSubscribe, ack.QueryID); err != nil {
            fmt.Fprintf(os.Stderr, "subscribe error: %v\n", err)
            continue
        }

        fmt.Printf("listening for updates id=%s...\n", ack.QueryID)

        // Receive updates until Done=true.
        for {
            msg, err := sub.Recv()
            if err != nil {
                fmt.Fprintf(os.Stderr, "update recv error: %v\n", err)
                break
            }
            if len(msg.Frames) < 2 {
                // Expect [topic][payload]
                continue
            }
            topic := string(msg.Frames[0])
            if topic != ack.QueryID {
                // Not our topic; ignore (shouldn't happen due to subscription filter).
                continue
            }
            var upd updateMessage
            if err := json.Unmarshal(msg.Frames[1], &upd); err != nil {
                fmt.Fprintf(os.Stderr, "update parse error: %v\n", err)
                continue
            }

            // Print the payload as-is (pretty JSON). The payload conforms to models.Payload on daemon side.
            pretty, _ := json.MarshalIndent(json.RawMessage(upd.Payload), "", "  ")
            fmt.Printf("%s\n", string(pretty))

            if upd.Done {
                fmt.Printf("(done in %s)\n", time.Since(start).Truncate(time.Microsecond))
                break
            }
        }

        // Unsubscribe from this query's topic before next loop.
        _ = sub.SetOption(zmq4.OptionUnsubscribe, ack.QueryID)
    }
}
