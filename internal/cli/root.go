package cli

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/iMithrellas/tarragon/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "tarragon",
	Short: "TarraGon launcher",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Load config once for all subcommands except config generate
		if cmd.Name() == "generate" && cmd.Parent() != nil && cmd.Parent().Name() == "config" {
			return nil
		}

		// Determine config dir (allow override via --config-dir)
		cfgDir := viper.GetString("config_dir")
		if cfgDir == "" {
			var err error
			cfgDir, err = os.UserConfigDir()
			if err != nil {
				log.Println("Error getting user config directory:", err)
				cfgDir = "/etc/tarragon"
			} else {
				cfgDir = filepath.Join(cfgDir, "tarragon")
			}
		}

		// Initialize / load config without regeneration
		if err := config.InitConfig(cfgDir, false, viper.GetString("config_format")); err != nil {
			if err == config.ErrConfigGenerated {
				return nil
			}
			return fmt.Errorf("config init: %w", err)
		}
		// Apply flag overrides: only flags explicitly set should override loaded config
		for _, opt := range config.GetConfigOptions() {
			// Look up in local or inherited flags
			fl := cmd.Flags().Lookup(opt.Key)
			if fl == nil {
				fl = cmd.InheritedFlags().Lookup(opt.Key)
			}
			if fl == nil || !fl.Changed {
				continue
			}
			switch opt.Value.(type) {
			case bool:
				if v, err := cmd.Flags().GetBool(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				} else if v, err := cmd.InheritedFlags().GetBool(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				}
			case string:
				if v, err := cmd.Flags().GetString(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				} else if v, err := cmd.InheritedFlags().GetString(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				}
			case int:
				if v, err := cmd.Flags().GetInt(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				} else if v, err := cmd.InheritedFlags().GetInt(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				}
			case int64:
				if v, err := cmd.Flags().GetInt64(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				} else if v, err := cmd.InheritedFlags().GetInt64(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				}
			case float64:
				if v, err := cmd.Flags().GetFloat64(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				} else if v, err := cmd.InheritedFlags().GetFloat64(opt.Key); err == nil {
					viper.Set(opt.Key, v)
				}
			}
		}
		return nil
	},
}

// Config command and subcommands
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration utilities",
}

var configGenCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate a configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := viper.GetString("config_dir")
		if dir == "" {
			var err error
			dir, err = os.UserConfigDir()
			if err != nil {
				dir = "/etc/tarragon"
			} else {
				dir = filepath.Join(dir, "tarragon")
			}
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		format := viper.GetString("config_format")
		cfgPath := filepath.Join(dir, fmt.Sprintf("tarragon.%s", format))
		if err := config.GenerateConfig(cfgPath, config.ConfigFormat(format)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Config generated at: %s\n", cfgPath); err != nil {
			return err
		}
		return nil
	},
}

func init() {
	// Disable Cobra's built-in completion command; we provide our own.
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	// Global persistent flags
	rootCmd.PersistentFlags().String("config-dir", "", "Config directory (default: XDG config/tarragon)")
	rootCmd.PersistentFlags().String("config-format", "toml", "Config format for generation")
	_ = viper.BindPFlag("config_dir", rootCmd.PersistentFlags().Lookup("config-dir"))
	_ = viper.BindPFlag("config_format", rootCmd.PersistentFlags().Lookup("config-format"))

	// Config option flags (as overrides)
	for _, opt := range config.GetConfigOptions() {
		switch v := opt.Value.(type) {
		case bool:
			rootCmd.PersistentFlags().Bool(opt.Key, v, opt.Comment)
		case string:
			rootCmd.PersistentFlags().String(opt.Key, v, opt.Comment)
		case int:
			rootCmd.PersistentFlags().Int(opt.Key, v, opt.Comment)
		case int64:
			rootCmd.PersistentFlags().Int64(opt.Key, v, opt.Comment)
		case float64:
			rootCmd.PersistentFlags().Float64(opt.Key, v, opt.Comment)
		default:
			// ignore unsupported types
		}
	}

	// Environment
	config.SetupEnvironment()

	// Register config commands
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configGenCmd)

	// Register plugin management commands
	rootCmd.AddCommand(installPluginCmd)
	rootCmd.AddCommand(uninstallPluginCmd)
	rootCmd.AddCommand(listPluginsCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
