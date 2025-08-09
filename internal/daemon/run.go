package daemon

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/iMithrellas/tarragon/internal/plugins"
	"github.com/iMithrellas/tarragon/internal/wire"
	"github.com/spf13/viper"
)

// RunDaemon composes the daemon services and blocks until shutdown.
func RunDaemon() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pluginDir := plugins.DefaultDir()
	mgr := plugins.NewManager(pluginDir)
	if err := mgr.Discover(); err != nil {
		log.Printf("Plugin discovery error: %v", err)
	}
	if err := mgr.StartPersistent(ctx, wire.EndpointPlugins); err != nil {
		log.Printf("Plugin start error: %v", err)
	}
	defer mgr.StopAll()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Println("Shutdown signal received.")
		cancel()
	}()

	// Aggregates store with configurable limit
	maxAgg := viper.GetInt("max_aggregates")
	if maxAgg <= 0 {
		maxAgg = 64
	}
	store := newAggregateStore(maxAgg)

	// Start plugin ROUTER and get channels/registry
	reqOut, registry := startPluginRouter(ctx, store)

	// Start IPC server(s)
	if viper.GetBool("run_tcp") {
		go reqServer(ctx, "tcp://127.0.0.1:"+viper.GetString("port"), "TCP", mgr, reqOut, registry, store)
	}
	if viper.GetBool("run_ipc") {
		go reqServer(ctx, wire.EndpointUIReq, "IPC", mgr, reqOut, registry, store)
	}

	<-ctx.Done()
	log.Println("Daemon shutting down.")
}
