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
	// TODO: periodically rescan pluginDir for new plugins/config changes.
	if err := mgr.Discover(); err != nil {
		log.Printf("Plugin discovery error: %v", err)
	}
	if err := mgr.StartPersistent(ctx, wire.SocketPlugins); err != nil {
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
	ui := newUIRegistry()

	// Start plugin listener and UI listener.
	reqOut, registry := startPluginListener(ctx, store, ui)
	if viper.GetBool("run_ipc") {
		go startUIServer(ctx, mgr, reqOut, registry, store, ui)
	}

	<-ctx.Done()
	log.Println("Daemon shutting down.")
}
