package daemon

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/iMithrellas/tarragon/internal/db"
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
	mgr.ApplyOverrides()
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
	orderingMode := viper.GetString("result_ordering")
	if orderingMode == "" {
		orderingMode = "global"
	}
	frecencyWeight := viper.GetFloat64("frecency_weight")
	if frecencyWeight == 0 {
		frecencyWeight = 0.3
	}
	database, err := db.Open(viper.GetString("db_path"))
	if err != nil {
		log.Printf("DB open error (frecency disabled): %v", err)
	}
	if database != nil {
		defer func() {
			if err := database.Close(); err != nil {
				log.Printf("DB close error: %v", err)
			}
		}()
	}
	store := newAggregateStore(maxAgg, orderingMode, database, frecencyWeight)
	ui := newUIRegistry()

	// Start plugin listener BEFORE spawning plugin processes, so the
	// socket is ready to accept connections when plugins start.
	reqOut, registry := startPluginListener(ctx, store, ui)
	if viper.GetBool("run_ipc") {
		go startUIServer(ctx, mgr, reqOut, registry, store, ui, database)
	}

	// Now start persistent plugin processes; the listener is already up.
	if err := mgr.StartPersistent(ctx, wire.SocketPlugins); err != nil {
		log.Printf("Plugin start error: %v", err)
	}

	<-ctx.Done()
	log.Println("Daemon shutting down.")
}
