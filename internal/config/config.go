// internal/config/config.go
package config

import (
	"fmt"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"os"
	"path/filepath"
	"strings"
)

func BindFlags() {
	pflag.Bool("run_tcp", false, "Run with TCP")
	pflag.Bool("run_ipc", false, "Run with IPC")
	pflag.String("port", "", "Port number")
	pflag.Int("tuidebounce", 0, "Debounce duration for TUI in ms(e.g., 300)")
	pflag.BoolP("daemon", "d", false, "Run in daemon mode")
	pflag.BoolP("bench", "b", false, "Run in benchmark mode")
	pflag.BoolP("tui", "t", false, "Run with text-based user interface")
	pflag.BoolP("gui", "g", false, "Run with graphical user interface")

	pflag.Parse()

	pflag.Visit(func(f *pflag.Flag) {
		viper.BindPFlag(f.Name, f)
	})

}

func SetupEnvironment() {
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
}

func LoadConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating config directory: %w", err)
	}

	viper.SetConfigFile(path)
	viper.SetConfigType("toml")

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	SetupEnvironment()
	BindFlags()

	return nil
}

func GenerateConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking config file: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating config directory: %w", err)
	}

	viper.SetConfigFile(path)
	viper.SetConfigType("toml")

	viper.Set("run_tcp", false)
	viper.Set("run_ipc", true)
	viper.Set("port", "5555")
    viper.Set("tuidebounce", 200)
    viper.Set("max_aggregates", 64)

	if err := viper.WriteConfigAs(path); err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}

	viper.Reset()
	return nil
}
