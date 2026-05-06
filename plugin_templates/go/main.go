package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const defaultName = "template_go"

type request struct {
	Type     string `json:"type"`
	QueryID  string `json:"query_id"`
	Text     string `json:"text"`
	ResultID string `json:"result_id"`
	Action   string `json:"action"`
}

type response struct {
	Type    string `json:"type"`
	QueryID string `json:"query_id"`
	Data    any    `json:"data"`
}

type result struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Actions []action `json:"actions,omitempty"`
}

type action struct {
	Name        string `json:"name"`
	Default     bool   `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

func main() {
	args := os.Args[1:]
	if len(args) >= 3 && args[0] == "tarragon" && args[1] == "query" {
		writeQueryResult(strings.Join(args[2:], " "))
		return
	}
	if len(args) >= 2 && args[0] == "--once" {
		writeQueryResult(strings.Join(args[1:], " "))
		return
	}

	endpoint := os.Getenv("TARRAGON_PLUGINS_ENDPOINT")
	if endpoint == "" {
		log("idle mode; TARRAGON_PLUGINS_ENDPOINT is not set")
		select {}
	}
	if err := runDaemon(endpoint, pluginName()); err != nil {
		log("daemon error: %v", err)
		os.Exit(1)
	}
}

func runDaemon(endpoint, name string) error {
	conn, err := dialWithRetry(endpoint, 20, 100*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	if err := enc.Encode(map[string]string{"type": "hello", "name": name}); err != nil {
		return err
	}
	log("connected to %s", endpoint)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			log("invalid request: %v", err)
			continue
		}
		switch req.Type {
		case "request":
			data := map[string]any{"input": req.Text, "variants": variants(req.Text)}
			if err := enc.Encode(response{Type: "response", QueryID: req.QueryID, Data: data}); err != nil {
				return err
			}
		case "select":
			_ = enc.Encode(map[string]any{"type": "select_response", "success": true, "message": "selected " + req.ResultID})
		}
	}
	return scanner.Err()
}

func dialWithRetry(endpoint string, attempts int, delay time.Duration) (net.Conn, error) {
	var last error
	for i := 0; i < attempts; i++ {
		conn, err := net.Dial("unix", endpoint)
		if err == nil {
			return conn, nil
		}
		last = err
		time.Sleep(delay)
	}
	return nil, last
}

func writeQueryResult(text string) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"input": text, "variants": variants(text)})
}

func variants(text string) []result {
	transforms := []string{reverse(text), strings.ToUpper(text), titleWords(text)}
	out := make([]result, 0, len(transforms))
	for i, value := range transforms {
		out = append(out, result{
			ID:    fmt.Sprintf("%d", i+1),
			Label: value,
			Actions: []action{{
				Name:        "select",
				Default:     true,
				Description: "Acknowledge selection",
			}},
		})
	}
	return out
}

func reverse(text string) string {
	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func titleWords(text string) string {
	fields := strings.Fields(text)
	for i, field := range fields {
		runes := []rune(field)
		if len(runes) == 0 {
			continue
		}
		runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
		fields[i] = string(runes)
	}
	return strings.Join(fields, " ")
}

func pluginName() string {
	if name := os.Getenv("TARRAGON_PLUGIN_NAME"); name != "" {
		return name
	}
	return defaultName
}

func log(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[PLUGIN: %s] ", pluginName())
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
