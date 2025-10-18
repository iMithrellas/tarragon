package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/ini.v1"
	"gopkg.in/yaml.v3"
)

// resetFlags clears both viper and pflag state
func resetFlags() {
	viper.Reset()
	pflag.CommandLine = pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
}

func TestGenerateAndLoadConfig(t *testing.T) {
	resetFlags()
	defer resetFlags()

	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")
	cfg := filepath.Join(configDir, "tarragon.toml")

	if err := GenerateConfig(cfg, FormatTOML); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("config file missing: %v", err)
	}

	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if !viper.IsSet("run_tcp") {
		t.Error("run_tcp not set after loading config")
	}
	if !viper.IsSet("port") {
		t.Error("port not set after loading config")
	}
}

func TestGenerateConfigMultipleFormats(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")

	formats := []ConfigFormat{FormatTOML, FormatYAML, FormatJSON, FormatINI}

	for _, format := range formats {
		t.Run(string(format), func(t *testing.T) {
			resetFlags()
			defer resetFlags()

			cfg := filepath.Join(configDir, "tarragon."+string(format))

			if err := GenerateConfig(cfg, format); err != nil {
				t.Fatalf("GenerateConfig(%s): %v", format, err)
			}
			if _, err := os.Stat(cfg); err != nil {
				t.Fatalf("config file missing for %s: %v", format, err)
			}
		})
	}
}

func TestGeneratedConfigValidity(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")

	tests := []struct {
		format   ConfigFormat
		validate func(string) error
	}{
		{
			format: FormatTOML,
			validate: func(path string) error {
				v := viper.New()
				v.SetConfigFile(path)
				v.SetConfigType("toml")
				return v.ReadInConfig()
			},
		},
		{
			format: FormatYAML,
			validate: func(path string) error {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				var config map[string]interface{}
				return yaml.Unmarshal(data, &config)
			},
		},
		{
			format: FormatJSON,
			validate: func(path string) error {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				var config map[string]interface{}
				return json.Unmarshal(data, &config)
			},
		},
		{
			format: FormatINI,
			validate: func(path string) error {
				_, err := ini.Load(path)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			cfg := filepath.Join(configDir, "test."+string(tt.format))

			// Generate config
			if err := GenerateConfig(cfg, tt.format); err != nil {
				t.Fatalf("GenerateConfig failed: %v", err)
			}

			// Validate syntax
			if err := tt.validate(cfg); err != nil {
				t.Fatalf("Generated %s config is invalid: %v",
					tt.format, err)
			}
		})
	}
}

func TestGeneratedConfigValues(t *testing.T) {
	resetFlags()
	defer resetFlags()

	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")
	cfg := filepath.Join(configDir, "tarragon.toml")

	if err := GenerateConfig(cfg, FormatTOML); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}

	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Verify expected default values
	tests := []struct {
		key      string
		getValue func() interface{}
		expected interface{}
	}{
		{"run_tcp", func() interface{} { return viper.GetBool("run_tcp") }, false},
		{"run_ipc", func() interface{} { return viper.GetBool("run_ipc") }, true},
		{"port", func() interface{} { return viper.GetString("port") }, "5555"},
		{"tuidebounce", func() interface{} { return viper.GetInt("tuidebounce") }, 200},
		{"max_aggregates", func() interface{} { return viper.GetInt("max_aggregates") }, 64},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			actual := tt.getValue()
			if actual != tt.expected {
				t.Errorf("Expected %s=%v (type %T), got %v (type %T)",
					tt.key, tt.expected, tt.expected, actual, actual)
			}
		})
	}
}

func TestInitConfigAutoGenerate(t *testing.T) {
	resetFlags()
	defer resetFlags()

	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")

	if err := InitConfig(configDir, false, "toml"); err != nil {
		t.Fatalf("InitConfig: %v", err)
	}

	cfg := filepath.Join(configDir, "tarragon.toml")
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("auto-generated config file missing: %v", err)
	}
}

func TestInitConfigRegenerate(t *testing.T) {
	resetFlags()
	defer resetFlags()

	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")

	err := InitConfig(configDir, true, "yaml")
	if err != ErrConfigGenerated {
		t.Fatalf("Expected ErrConfigGenerated, got: %v", err)
	}

	cfg := filepath.Join(configDir, "tarragon.yaml")
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("regenerated config file missing: %v", err)
	}
}

func TestFindConfigFile(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	tomlPath := filepath.Join(configDir, "tarragon.toml")
	if err := os.WriteFile(tomlPath, []byte("run_tcp = true"), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	found, err := FindConfigFile(configDir)
	if err != nil {
		t.Fatalf("FindConfigFile: %v", err)
	}
	if found != tomlPath {
		t.Errorf("Expected %s, got %s", tomlPath, found)
	}
}

func TestFindConfigFileNotFound(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")

	_, err := FindConfigFile(configDir)
	if err == nil {
		t.Error("Expected error when no config file exists")
	}
}
