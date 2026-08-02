package daemon

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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
	mgr.SetStopTimeout(pluginStopTimeout())
	// TODO: periodically rescan pluginDir for new plugins/config changes.
	if err := mgr.Discover(); err != nil {
		log.Printf("Plugin discovery error: %v", err)
	}
	if err := mgr.ApplyOverrides(); err != nil {
		log.Printf("Plugin override error: %v", err)
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

// pluginStopTimeout resolves the configured plugin shutdown grace period,
// falling back to the default when it is unset or unparseable.
func pluginStopTimeout() time.Duration {
	raw := strings.TrimSpace(viper.GetString("plugin_stop_timeout"))
	if raw == "" {
		return plugins.DefaultStopTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("Invalid plugin_stop_timeout %q; using %s", raw, plugins.DefaultStopTimeout)
		return plugins.DefaultStopTimeout
	}
	return d
}
