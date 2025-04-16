// internal/daemon/daemon.go
package daemon

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-zeromq/zmq4"
	"github.com/iMithrellas/tarragon/pkg/models"
	"github.com/spf13/viper"
)

const (
	ipcEndpointUi      = "ipc:///tmp/tarragon-ui.ipc"
	ipcEndpointPlugins = "ipc:///tmp/tarragon-plugins.ipc"
)

func RunDaemon() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Println("Shutdown signal received.")
		cancel()
	}()

	if viper.GetBool("run_tcp") {
		go routerServer(ctx, "tcp://127.0.0.1:"+viper.GetString("port"), "TCP")
	}
	if viper.GetBool("run_ipc") {
		go routerServer(ctx, ipcEndpointUi, "IPC")
	}

	<-ctx.Done()
	log.Println("Daemon shutting down.")
}

// lookupPayload searches the trie for the given key (exact match) and returns the corresponding payload.
func lookupPayload(trie *models.Trie, key string) *models.Payload {
	current := trie.Root
	for _, r := range key {
		node, ok := current.Children[r]
		if !ok {
			return nil
		}
		current = node
	}
	return current.Value
}

func routerServer(ctx context.Context, endpoint, label string) {
	// Remove stale IPC socket file if necessary.
	if len(endpoint) >= 6 && endpoint[:6] == "ipc://" {
		path := endpoint[6:]
		if _, err := os.Stat(path); err == nil {
			log.Printf("[%s] Removing stale IPC socket file: %s", label, path)
			os.Remove(path)
		}
	}

	rep := zmq4.NewRep(ctx)
	if err := rep.Listen(endpoint); err != nil {
		log.Fatalf("[%s] Failed to bind: %v", label, err)
	}
	defer rep.Close()
	log.Printf("[%s] Listening on %s", label, endpoint)

	// Create the trie with test data.
	words := []string{"banana", "zebra", "mountain", "quartz", "hyper", "echo", "lunar", "binary", "delta", "forest"}
	trie := models.CreateTestTrie(words)

	for {
		msg, err := rep.Recv()
		if err != nil {
			log.Printf("[%s] Error receiving: %v", label, err)
			continue
		}
		log.Printf("[%s] Received frames: %q", label, msg.Frames)

		input := string(msg.Frames[0])
		log.Printf("[%s] Looking up input: %s", label, input)

		payload := lookupPayload(trie, input)
		var jsonPayload []byte
		if payload != nil {
			jsonPayload, err = json.Marshal(payload)
			if err != nil {
				log.Printf("[%s] Error marshalling payload: %v", label, err)
				jsonPayload = []byte(`{"error": "failed to marshall payload"}`)
			}
		} else {
			jsonPayload = []byte(`{"error": "payload not found"}`)
		}

		// Send a single frame reply with the JSON payload.
		reply := zmq4.Msg{
			Frames: [][]byte{jsonPayload},
		}

		if err := rep.Send(reply); err != nil {
			log.Printf("[%s] Error sending reply: %v", label, err)
		}
	}
}
