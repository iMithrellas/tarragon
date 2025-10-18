// internal/config/config.go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iMithrellas/tarragon/internal/db"
	"github.com/spf13/viper"
)

type ConfigFormat string

const (
	FormatTOML ConfigFormat = "toml"
	FormatYAML ConfigFormat = "yaml"
	FormatJSON ConfigFormat = "json"
	FormatINI  ConfigFormat = "ini"
)

type ConfigOption struct {
	Key     string
	Value   interface{}
	Comment string
}

// GetConfigOptions returns the default configuration options
func GetConfigOptions() []ConfigOption {
	return []ConfigOption{
		{
			Key:     "run_tcp",
			Value:   false,
			Comment: "Enable TCP server",
		},
		{
			Key:     "run_ipc",
			Value:   true,
			Comment: "Enable IPC server",
		},
		{
			Key:     "port",
			Value:   "5555",
			Comment: "TCP port to listen on",
		},
		{
			Key:     "tuidebounce",
			Value:   200,
			Comment: "TUI debounce interval in milliseconds",
		},
		{
			Key:     "max_aggregates",
			Value:   64,
			Comment: "Maximum number of aggregates to maintain",
		},
		{
			Key:     "db_path",
			Value:   db.DefaultPath(),
			Comment: "Path to the database file",
		},
	}
}

// Flag binding moved to CLI layer (internal/cli). This package no longer binds flags.

func SetupEnvironment() {
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
}

func FindConfigFile(dir string) (string, error) {
	formats := []string{"toml", "yaml", "yml", "json", "ini"}
	baseName := "tarragon"

	var found []string
	for _, ext := range formats {
		path := filepath.Join(dir, fmt.Sprintf("%s.%s", baseName, ext))
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		}
	}

	if len(found) == 0 {
		return "", fmt.Errorf("no config file found in %s", dir)
	}

	if len(found) > 1 {
		fmt.Fprintf(os.Stderr, "Warning: Multiple config files found:\n")
		for _, f := range found {
			fmt.Fprintf(os.Stderr, "  - %s\n", f)
		}
		fmt.Fprintf(os.Stderr, "Using: %s\n\n", found[0])
	}

	return found[0], nil
}

func LoadConfig(configDir string) error {
	path, err := FindConfigFile(configDir)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating config directory: %w", err)
	}

	viper.SetConfigFile(path)

	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext != "" {
		viper.SetConfigType(ext)
	} else {
		viper.SetConfigType("toml")
	}

	if err := viper.ReadInConfig(); err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	// Remove JSON comments key if present
	viper.Set("_comments", nil)

	SetupEnvironment()

	return nil
}

// InitConfig handles config initialization: generation if requested,
// loading existing config, or auto-generating default if none exists
func InitConfig(configDir string, regenerate bool, format string) error {
	// Handle explicit config generation request
	if regenerate {
		configFormat := ConfigFormat(strings.ToLower(format))
		configPath := filepath.Join(
			configDir,
			fmt.Sprintf("tarragon.%s", configFormat),
		)
		if err := GenerateConfig(configPath, configFormat); err != nil {
			return fmt.Errorf("failed to generate config: %w", err)
		}
		fmt.Printf("Config generated at: %s\n", configPath)
		// Return a special sentinel error to indicate we should exit
		return ErrConfigGenerated
	}

	// Try to load existing config
	if err := LoadConfig(configDir); err != nil {
		// If no config exists, auto-generate a default one
		if strings.Contains(err.Error(), "no config file found") {
			fmt.Fprintln(os.Stderr,
				"No config file found, generating default...")
			configPath := filepath.Join(configDir, "tarragon.toml")
			if err := GenerateConfig(configPath, FormatTOML); err != nil {
				return fmt.Errorf(
					"failed to generate default config: %w", err)
			}
			// Try loading the newly generated config
			if err := LoadConfig(configDir); err != nil {
				return fmt.Errorf("failed to load generated config: %w", err)
			}
		} else {
			return fmt.Errorf("failed to load config: %w", err)
		}
	}

	return nil
}

// ErrConfigGenerated is returned when config is generated on user request
var ErrConfigGenerated = fmt.Errorf("config generated successfully")

func GenerateConfig(path string, format ConfigFormat) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking config file: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating config directory: %w", err)
	}

	options := GetConfigOptions()

	var content string
	var err error

	switch format {
	case FormatTOML:
		content = generateTOML(options)
	case FormatYAML:
		content = generateYAML(options)
	case FormatJSON:
		content, err = generateJSON(options)
		if err != nil {
			return fmt.Errorf("error generating JSON: %w", err)
		}
	case FormatINI:
		content = generateINI(options)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}

	return nil
}

func generateTOML(options []ConfigOption) string {
	var sb strings.Builder
	for _, opt := range options {
		sb.WriteString(fmt.Sprintf("# %s\n", opt.Comment))
		sb.WriteString(formatTOMLValue(opt.Key, opt.Value))
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func generateYAML(options []ConfigOption) string {
	var sb strings.Builder
	for _, opt := range options {
		sb.WriteString(fmt.Sprintf("# %s\n", opt.Comment))
		sb.WriteString(formatYAMLValue(opt.Key, opt.Value))
		sb.WriteString("\n")
	}
	return sb.String()
}

func generateJSON(options []ConfigOption) (string, error) {
	config := make(map[string]interface{})
	comments := make(map[string]string)

	for _, opt := range options {
		config[opt.Key] = opt.Value
		comments[opt.Key] = opt.Comment
	}

	// JSON doesn't support comments natively, so we use a _comments field
	result := map[string]interface{}{
		"_comments": comments,
	}
	for k, v := range config {
		result[k] = v
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func generateINI(options []ConfigOption) string {
	var sb strings.Builder
	sb.WriteString("[default]\n")
	for _, opt := range options {
		sb.WriteString(fmt.Sprintf("; %s\n", opt.Comment))
		sb.WriteString(formatINIValue(opt.Key, opt.Value))
		sb.WriteString("\n")
	}
	return sb.String()
}

func formatTOMLValue(key string, value interface{}) string {
	switch v := value.(type) {
	case string:
		return fmt.Sprintf("%s = \"%s\"", key, v)
	case bool, int, int64, float64:
		return fmt.Sprintf("%s = %v", key, v)
	default:
		return fmt.Sprintf("%s = \"%v\"", key, v)
	}
}

func formatYAMLValue(key string, value interface{}) string {
	switch v := value.(type) {
	case string:
		return fmt.Sprintf("%s: \"%s\"", key, v)
	case bool, int, int64, float64:
		return fmt.Sprintf("%s: %v", key, v)
	default:
		return fmt.Sprintf("%s: \"%v\"", key, v)
	}
}

func formatINIValue(key string, value interface{}) string {
	return fmt.Sprintf("%s = %v", key, value)
}
