// cmd/main.go
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/iMithrellas/tarragon/internal/config"
	"github.com/iMithrellas/tarragon/internal/daemon"
	"github.com/iMithrellas/tarragon/internal/ui"
	"github.com/spf13/viper"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Get config directory
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Println("Error getting user config directory:", err)
		configDir = "/etc/tarragon"
	} else {
		configDir = filepath.Join(configDir, "tarragon")
	}

	configRegenerate := viper.GetBool("config-generate")
	configFormat := viper.GetString("config-format")

	// Initialize config
	if err := config.InitConfig(configDir, configRegenerate, configFormat); err != nil {
		if err == config.ErrConfigGenerated {
			return
		}
		log.Fatalf("Config initialization failed: %v", err)
	}

	// Run the application
	switch {
	case viper.GetBool("daemon"):
		daemon.RunDaemon()
	case viper.GetBool("tui"):
		ui.RunTUI()
	default:
		log.Println("No mode selected. Use --daemon or -t")
	}
}
