// cmd/main.go
package main

import (
	"log"
	"os"

	"github.com/iMithrellas/tarragon/internal/config"
	"github.com/iMithrellas/tarragon/internal/daemon"
	"github.com/iMithrellas/tarragon/internal/ui"
	"github.com/spf13/viper"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Println("Error getting user config directory:", err)
		configDir = "/etc/tarragon"
	}
	configDir += "/tarragon/config.toml"
	err = config.GenerateConfig(configDir)
	if err != nil {
		log.Println("Error generating config file:", err)
	}
	config.LoadConfig(configDir)

	switch {
	case viper.GetBool("daemon"):
		daemon.RunDaemon()
	case viper.GetBool("tui"):
		ui.RunTUI()
	default:
		log.Println("No mode selected. Use --daemon or --bench")
	}
}
