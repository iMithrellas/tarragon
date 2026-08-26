package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
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

	formats := []ConfigFormat{FormatTOML, FormatYAML, FormatJSON}

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
		{"max_aggregates", func() interface{} { return viper.GetInt("max_aggregates") }, 64},
		{"frecency_weight", func() interface{} { return viper.GetFloat64("frecency_weight") }, 0.3},
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

func TestLoadConfigMergesTOMLDropInsLexically(t *testing.T) {
	resetFlags()
	defer resetFlags()

	configDir := filepath.Join(t.TempDir(), "tarragon")
	dropInDir := filepath.Join(configDir, "tarragon.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("mkdir drop-in dir: %v", err)
	}
	primaryPath := filepath.Join(configDir, "tarragon.toml")
	if err := os.WriteFile(primaryPath, []byte(strings.Join([]string{
		"result_ordering = \"primary\"",
		"prefix_symbol = \"@\"",
		"[plugins.alpha]",
		"enabled = true",
		"prefix = \"primary\"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write primary config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropInDir, "10-disable.toml"), []byte(strings.Join([]string{
		"result_ordering = \"first\"",
		"[plugins.alpha]",
		"enabled = false",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write first drop-in: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropInDir, "20-prefix.toml"), []byte(strings.Join([]string{
		"result_ordering = \"second\"",
		"[plugins.alpha]",
		"prefix = \"later\"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write second drop-in: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropInDir, "99-ignored.yaml"), []byte("result_ordering: ignored\n"), 0o644); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}

	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := viper.GetString("result_ordering"); got != "second" {
		t.Fatalf("result_ordering = %q, want lexical last value", got)
	}
	if got := viper.GetBool("plugins.alpha.enabled"); got {
		t.Fatal("nested enabled value from first drop-in was lost")
	}
	if got := viper.GetString("plugins.alpha.prefix"); got != "later" {
		t.Fatalf("plugin prefix = %q, want later", got)
	}
	if got := viper.GetString("prefix_symbol"); got != "@" {
		t.Fatalf("primary-only value = %q, want @", got)
	}
	if got := viper.ConfigFileUsed(); got != primaryPath {
		t.Fatalf("ConfigFileUsed = %q, want primary %q", got, primaryPath)
	}
}

func TestLoadConfigPrecedenceEnvironmentThenExplicitOverride(t *testing.T) {
	resetFlags()
	defer resetFlags()
	t.Setenv("RESULT_ORDERING", "environment")
	t.Setenv("PLUGINS_ALPHA_ENABLED", "false")

	configDir := filepath.Join(t.TempDir(), "tarragon")
	dropInDir := filepath.Join(configDir, "tarragon.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("mkdir drop-in dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "tarragon.toml"), []byte("result_ordering = \"primary\"\n[plugins.alpha]\nenabled = true\n"), 0o644); err != nil {
		t.Fatalf("write primary config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropInDir, "10-local.toml"), []byte("result_ordering = \"drop-in\"\n"), 0o644); err != nil {
		t.Fatalf("write drop-in: %v", err)
	}

	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := viper.GetString("result_ordering"); got != "environment" {
		t.Fatalf("environment value = %q, want environment", got)
	}
	if got := viper.GetBool("plugins.alpha.enabled"); got {
		t.Fatal("nested plugin environment override was not applied")
	}

	// The CLI applies explicitly changed flags through viper.Set.
	viper.Set("result_ordering", "flag")
	if err := ReloadConfig(); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	if got := viper.GetString("result_ordering"); got != "flag" {
		t.Fatalf("explicit override = %q, want flag", got)
	}
}

func TestReloadConfigReappliesDropIns(t *testing.T) {
	resetFlags()
	defer resetFlags()

	configDir := filepath.Join(t.TempDir(), "tarragon")
	dropInDir := filepath.Join(configDir, "tarragon.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("mkdir drop-in dir: %v", err)
	}
	primaryPath := filepath.Join(configDir, "tarragon.toml")
	dropInPath := filepath.Join(dropInDir, "10-local.toml")
	if err := os.WriteFile(primaryPath, []byte("prefix_symbol = \"@\"\nresult_ordering = \"primary\"\n"), 0o644); err != nil {
		t.Fatalf("write primary config: %v", err)
	}
	if err := os.WriteFile(dropInPath, []byte("prefix_symbol = \":\"\nresult_ordering = \"old\"\n"), 0o644); err != nil {
		t.Fatalf("write drop-in: %v", err)
	}
	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if err := os.WriteFile(dropInPath, []byte("result_ordering = \"new\"\n"), 0o644); err != nil {
		t.Fatalf("replace drop-in: %v", err)
	}
	if err := ReloadConfig(); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}

	if got := viper.GetString("result_ordering"); got != "new" {
		t.Fatalf("reloaded value = %q, want new", got)
	}
	if got := viper.GetString("prefix_symbol"); got != "@" {
		t.Fatalf("removed drop-in key = %q, want primary fallback", got)
	}
	if got := viper.ConfigFileUsed(); got != primaryPath {
		t.Fatalf("ConfigFileUsed = %q, want primary %q", got, primaryPath)
	}
}

func TestLoadConfigReportsMalformedDropIn(t *testing.T) {
	resetFlags()
	defer resetFlags()

	configDir := filepath.Join(t.TempDir(), "tarragon")
	dropInDir := filepath.Join(configDir, "tarragon.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("mkdir drop-in dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "tarragon.toml"), []byte("run_ipc = true\n"), 0o644); err != nil {
		t.Fatalf("write primary config: %v", err)
	}
	badPath := filepath.Join(dropInDir, "20-bad.toml")
	if err := os.WriteFile(badPath, []byte("prefix_symbol = [\n"), 0o644); err != nil {
		t.Fatalf("write malformed drop-in: %v", err)
	}

	err := LoadConfig(configDir)
	if err == nil {
		t.Fatal("expected malformed drop-in to fail loading")
	}
	if !strings.Contains(err.Error(), badPath) {
		t.Fatalf("error %q does not identify malformed drop-in %q", err, badPath)
	}
}

func TestReloadConfigKeepsPreviousValuesWhenDropInIsMalformed(t *testing.T) {
	resetFlags()
	defer resetFlags()

	configDir := filepath.Join(t.TempDir(), "tarragon")
	dropInDir := filepath.Join(configDir, "tarragon.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("mkdir drop-in dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "tarragon.toml"), []byte("result_ordering = \"primary\"\n"), 0o644); err != nil {
		t.Fatalf("write primary config: %v", err)
	}
	dropInPath := filepath.Join(dropInDir, "10-local.toml")
	if err := os.WriteFile(dropInPath, []byte("result_ordering = \"valid\"\n"), 0o644); err != nil {
		t.Fatalf("write drop-in: %v", err)
	}
	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if err := os.WriteFile(dropInPath, []byte("result_ordering = [\n"), 0o644); err != nil {
		t.Fatalf("corrupt drop-in: %v", err)
	}
	if err := ReloadConfig(); err == nil {
		t.Fatal("expected malformed drop-in to fail reload")
	}
	if got := viper.GetString("result_ordering"); got != "valid" {
		t.Fatalf("failed reload changed effective value to %q, want valid", got)
	}
}

func TestWritePluginOverrideUpdatesPrimaryBelowDropIn(t *testing.T) {
	resetFlags()
	defer resetFlags()

	configDir := filepath.Join(t.TempDir(), "tarragon")
	dropInDir := filepath.Join(configDir, "tarragon.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("mkdir drop-in dir: %v", err)
	}
	primaryPath := filepath.Join(configDir, "tarragon.toml")
	if err := os.WriteFile(primaryPath, []byte("[plugins.alpha]\nprefix = \"primary\"\n"), 0o644); err != nil {
		t.Fatalf("write primary config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dropInDir, "10-local.toml"), []byte("[plugins.alpha]\nprefix = \"drop-in\"\n"), 0o644); err != nil {
		t.Fatalf("write drop-in: %v", err)
	}
	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if err := WritePluginOverride("alpha", map[string]any{"prefix": "command"}); err != nil {
		t.Fatalf("WritePluginOverride: %v", err)
	}
	primary, err := os.ReadFile(primaryPath)
	if err != nil {
		t.Fatalf("read primary config: %v", err)
	}
	if !strings.Contains(string(primary), "prefix = \"command\"") {
		t.Fatalf("primary config was not updated:\n%s", primary)
	}
	if got := viper.GetString("plugins.alpha.prefix"); got != "drop-in" {
		t.Fatalf("effective prefix = %q, want higher-precedence drop-in", got)
	}
}

func TestWritePluginOverridePreservesCommentsAndUpdatesSection(t *testing.T) {
	resetFlags()
	defer resetFlags()

	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	cfgPath := filepath.Join(configDir, "tarragon.toml")
	content := strings.Join([]string{
		"# top comment",
		"run_tcp = false",
		"",
		"[plugins.calculator]",
		"enabled = true",
		"prefix = \"@calc\"",
		"",
		"[plugins.other]",
		"enabled = true",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if err := WritePluginOverride("calculator", map[string]any{
		"enabled":        false,
		"prefix":         "=",
		"lifecycle_mode": "on_call",
	}); err != nil {
		t.Fatalf("WritePluginOverride: %v", err)
	}

	updated, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	txt := string(updated)
	if !strings.Contains(txt, "# top comment") {
		t.Fatalf("expected original comment to be preserved, got:\n%s", txt)
	}
	if !strings.Contains(txt, "[plugins.calculator]") ||
		!strings.Contains(txt, "enabled = false") ||
		!strings.Contains(txt, "prefix = \"=\"") ||
		!strings.Contains(txt, "lifecycle_mode = \"on_call\"") {
		t.Fatalf("expected calculator overrides section to be updated, got:\n%s", txt)
	}
	if !strings.Contains(txt, "[plugins.other]") {
		t.Fatalf("expected unrelated plugin section to remain, got:\n%s", txt)
	}
}

func TestResetPluginOverrideRemovesSection(t *testing.T) {
	resetFlags()
	defer resetFlags()

	dir := t.TempDir()
	configDir := filepath.Join(dir, "tarragon")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	cfgPath := filepath.Join(configDir, "tarragon.toml")
	content := strings.Join([]string{
		"run_tcp = false",
		"",
		"[plugins.calculator]",
		"enabled = false",
		"prefix = \"=\"",
		"",
		"[plugins.other]",
		"enabled = true",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if err := ResetPluginOverride("calculator"); err != nil {
		t.Fatalf("ResetPluginOverride: %v", err)
	}

	updated, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	txt := string(updated)
	if strings.Contains(txt, "[plugins.calculator]") {
		t.Fatalf("expected calculator section removed, got:\n%s", txt)
	}
	if !strings.Contains(txt, "[plugins.other]") {
		t.Fatalf("expected other section to remain, got:\n%s", txt)
	}
}

func TestWritePluginOverrideQuotesNonBareKeys(t *testing.T) {
	resetFlags()
	defer resetFlags()

	configDir := filepath.Join(t.TempDir(), "tarragon")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfgPath := filepath.Join(configDir, "tarragon.toml")
	if err := os.WriteFile(cfgPath, []byte("run_tcp = false\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if err := WritePluginOverride("System Control", map[string]any{"prefix": "@sys"}); err != nil {
		t.Fatalf("WritePluginOverride: %v", err)
	}

	updated, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	if !strings.Contains(string(updated), "[plugins.\"System Control\"]") {
		t.Fatalf("expected quoted section header, got:\n%s", string(updated))
	}
}

func TestResetPluginOverrideRemovesQuotedSection(t *testing.T) {
	resetFlags()
	defer resetFlags()

	configDir := filepath.Join(t.TempDir(), "tarragon")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfgPath := filepath.Join(configDir, "tarragon.toml")
	content := strings.Join([]string{
		"run_tcp = false",
		"",
		"[plugins.\"System Control\"]",
		"prefix = \"@sys\"",
		"",
		"[plugins.other]",
		"enabled = true",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := LoadConfig(configDir); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if err := ResetPluginOverride("System Control"); err != nil {
		t.Fatalf("ResetPluginOverride: %v", err)
	}

	updated, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	txt := string(updated)
	if strings.Contains(txt, "System Control") {
		t.Fatalf("expected quoted section to be removed, got:\n%s", txt)
	}
	if !strings.Contains(txt, "[plugins.other]") {
		t.Fatalf("expected unrelated section to remain, got:\n%s", txt)
	}
}
