// internal/daemon/daemon.go
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-zeromq/zmq4"
	"github.com/spf13/viper"
)

const ipcEndpoint = "ipc:///tmp/tarragon.ipc"

type Suggestion struct {
	IconPath string  `json:"iconPath"`
	Weight   float64 `json:"weight"`
	Command  string  `json:"command"`
}

type Payload struct {
	Action      string       `json:"action"`
	Value       string       `json:"value"`
	Suggestions []Suggestion `json:"suggestions"`
}

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

	go routerServer(ctx, "tcp://127.0.0.1:"+viper.GetString("port"), "TCP")
	go routerServer(ctx, ipcEndpoint, "IPC")

	<-ctx.Done()
	log.Println("Daemon shutting down.")
}

func routerServer(ctx context.Context, endpoint, label string) {
	if endpoint[:6] == "ipc://" {
		path := endpoint[6:]
		if _, err := os.Stat(path); err == nil {
			log.Printf("[%s] Removing stale IPC socket file: %s", label, path)
			os.Remove(path)
		}
	}
	router := zmq4.NewRouter(ctx)
	if err := router.Listen(endpoint); err != nil {
		log.Fatalf("[%s] Failed to bind: %v", label, err)
	}
	defer router.Close()

	log.Printf("[%s] Listening on %s\n", label, endpoint)

	for {
		msg, err := router.Recv()
		if err != nil {
			log.Printf("[%s] Error receiving: %v", label, err)
			continue
		}
		log.Printf("[%s] Received: %q", label, msg.Frames)

		// Echo back "ACK" to sender
		reply := zmq4.Msg{
			Frames: [][]byte{
				msg.Frames[0],
				[]byte("ACK"),
			},
		}

		if err := router.Send(reply); err != nil {
			log.Printf("[%s] Error sending ACK: %v", label, err)
		}
	}
}

func RunBenchmark() {
	const iterations = 100000

	ctx := context.Background()

	tcpEndpoint := "tcp://127.0.0.1:" + viper.GetString("port")
	tcpDealer := zmq4.NewDealer(ctx)
	if err := tcpDealer.Dial(tcpEndpoint); err != nil {
		log.Fatalf("TCP dial failed: %v", err)
	}
	defer tcpDealer.Close()

	ipcDealer := zmq4.NewDealer(ctx)
	if err := ipcDealer.Dial(ipcEndpoint); err != nil {
		log.Fatalf("IPC dial failed: %v", err)
	}
	defer ipcDealer.Close()

	payload := Payload{
		Action: "ping",
		Value:  "benchmark",
	}
	for i := 0; i < 10; i++ {
		payload.Suggestions = append(payload.Suggestions, Suggestion{
			IconPath: fmt.Sprintf("/icons/icon_%d.png", i),
			Weight:   float64(i) * 1.1,
			Command:  fmt.Sprintf("command_%d", i),
		})
	}
	raw, _ := json.Marshal(payload)

	var tcpAvg, ipcAvg time.Duration

	log.Printf("Benchmarking %d iterations...\n", iterations)

	for i := 1; i <= iterations; i++ {
		tcpDur := roundTrip(tcpDealer, raw)
		ipcDur := roundTrip(ipcDealer, raw)

		// Incremental averaging
		tcpAvg += (tcpDur - tcpAvg) / time.Duration(i)
		ipcAvg += (ipcDur - ipcAvg) / time.Duration(i)

		if i%(iterations/10) == 0 {
			log.Printf("Progress: %d%%", (i*100)/iterations)
		}
	}

	fmt.Printf("\nAveraged Results over %d iterations:\n", iterations)
	fmt.Printf("TCP: %v\n", tcpAvg)
	fmt.Printf("IPC: %v\n", ipcAvg)
}

func roundTrip(socket zmq4.Socket, data []byte) time.Duration {
	start := time.Now()

	if err := socket.Send(zmq4.NewMsg(data)); err != nil {
		log.Fatalf("Send failed: %v", err)
	}

	msg, err := socket.Recv()
	if err != nil {
		log.Fatalf("Recv failed: %v", err)
	}

	if len(msg.Frames) == 0 || string(msg.Frames[0]) != "ACK" {
		log.Fatalf("Unexpected reply: %q", msg.Frames)
	}

	return time.Since(start)
}
