package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is the plugin's own configuration.
//
// Tarragon does not forward per-plugin settings to plugin processes (the
// daemon only sets TARRAGON_PLUGINS_ENDPOINT and TARRAGON_PLUGIN_NAME, and
// `tarragon plugin config` only understands enabled/prefix/lifecycle_mode).
// Plugins therefore own their own configuration file, the same way
// file_finder owns its XDG user-dirs resolution.
type Config struct {
	// Library
	Directories    []string `toml:"directories"`
	Recursive      bool     `toml:"recursive"`
	Extensions     []string `toml:"extensions"`
	MaxResults     int      `toml:"max_results"`
	RescanInterval string   `toml:"rescan_interval"`

	// Backend
	Backend       string   `toml:"backend"`
	CustomCommand []string `toml:"custom_command"`

	// awww tuning
	AwwwTransitionType     string  `toml:"awww_transition_type"`
	AwwwTransitionFPS      int     `toml:"awww_transition_fps"`
	AwwwTransitionDuration float64 `toml:"awww_transition_duration"`
	AwwwResizeMode         string  `toml:"awww_resize"`
	AwwwFillColor          string  `toml:"awww_fill_color"`

	// Theming
	MatugenMode string `toml:"matugen_mode"`
	MatugenType string `toml:"matugen_type"`
	// MatugenPrefer maps to matugen's --prefer. It is mandatory in practice:
	// matugen >= 4 refuses to guess a source colour when several candidates
	// exist and stdout is not a TTY, which is always the case for a plugin
	// started by the daemon.
	MatugenPrefer    string   `toml:"matugen_prefer"`
	MatugenExtraArgs []string `toml:"matugen_extra_args"`

	// Post hook, runs after a successful set. {path} is substituted.
	PostCommand []string `toml:"post_command"`

	// Startup
	RestoreOnStart bool   `toml:"restore_on_start"`
	RestoreTimeout string `toml:"restore_timeout"`
}

func defaultConfig() *Config {
	return &Config{
		Directories:            nil, // filled by defaultDirectories() when empty
		Recursive:              true,
		Extensions:             []string{"jpg", "jpeg", "png", "webp", "bmp", "gif", "tif", "tiff", "pnm", "tga", "farbfeld"},
		MaxResults:             40,
		RescanInterval:         "2m",
		Backend:                "matugen",
		AwwwTransitionType:     "outer",
		AwwwTransitionFPS:      60,
		AwwwTransitionDuration: 1.5,
		AwwwResizeMode:         "crop",
		MatugenMode:            "dark",
		MatugenType:            "scheme-tonal-spot",
		MatugenPrefer:          "saturation",
		RestoreOnStart:         true,
		RestoreTimeout:         "60s",
	}
}

// configPath returns $XDG_CONFIG_HOME/tarragon/wallpaper.toml.
// This sits next to tarragon.toml without colliding with it: the core only
// looks for tarragon.{toml,yaml,yml,json}.
func configPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "tarragon", "wallpaper.toml")
}

// loadConfig reads the config file, falling back to defaults for any missing
// key. A commented template is written on first run so the plugin is
// discoverable without documentation.
func loadConfig() (*Config, string, error) {
	path := configPath()
	cfg := defaultConfig()

	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := toml.Unmarshal(b, cfg); err != nil {
			return cfg, path, fmt.Errorf("parse %s: %w", path, err)
		}
	case os.IsNotExist(err):
		if werr := writeDefaultConfig(path); werr != nil {
			return cfg, path, fmt.Errorf("write default config: %w", werr)
		}
	default:
		return cfg, path, fmt.Errorf("read %s: %w", path, err)
	}

	cfg.normalize()
	return cfg, path, nil
}

func (c *Config) normalize() {
	if len(c.Directories) == 0 {
		c.Directories = defaultDirectories()
	}
	expanded := make([]string, 0, len(c.Directories))
	seen := map[string]struct{}{}
	for _, d := range c.Directories {
		d = expandPath(d)
		if d == "" {
			continue
		}
		if _, dup := seen[d]; dup {
			continue
		}
		seen[d] = struct{}{}
		expanded = append(expanded, d)
	}
	c.Directories = expanded

	norm := make([]string, 0, len(c.Extensions))
	for _, e := range c.Extensions {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e != "" {
			norm = append(norm, e)
		}
	}
	if len(norm) == 0 {
		norm = defaultConfig().Extensions
	}
	c.Extensions = norm

	if c.MaxResults <= 0 {
		c.MaxResults = 40
	}
	c.Backend = strings.ToLower(strings.TrimSpace(c.Backend))
	if c.Backend == "" {
		c.Backend = "auto"
	}
	if c.MatugenMode == "" {
		c.MatugenMode = "dark"
	}
}

func (c *Config) allowedExt(name string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return false
	}
	for _, e := range c.Extensions {
		if e == ext {
			return true
		}
	}
	return false
}

func (c *Config) rescanInterval() time.Duration {
	return parseDuration(c.RescanInterval, 2*time.Minute)
}

func (c *Config) restoreTimeout() time.Duration {
	return parseDuration(c.RestoreTimeout, 60*time.Second)
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func defaultDirectories() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return []string{
		filepath.Join(home, "Pictures", "Wallpapers"),
		filepath.Join(home, "Pictures", "wallpapers"),
		filepath.Join(home, ".local", "share", "wallpapers"),
		"/usr/share/backgrounds",
		"/usr/share/wallpapers",
	}
}

func expandPath(p string) string {
	p = strings.TrimSpace(os.ExpandEnv(p))
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return filepath.Clean(p)
}

const defaultConfigTemplate = `# Tarragon wallpaper plugin configuration.
#
# Tarragon's core config (tarragon.toml) only carries enabled/prefix/
# lifecycle_mode overrides, so this plugin keeps its own settings here.

# Wallpaper library. Directories that do not exist are skipped silently.
# directories = ["~/Pictures/Wallpapers", "/usr/share/backgrounds"]
recursive = true
# extensions = ["jpg", "jpeg", "png", "webp"]
max_results = 40
rescan_interval = "2m"

# Backend used to paint the Wayland background layer.
#   matugen   generate templates and let matugen's config apply the wallpaper
#   auto      pick the first available of: matugen, awww, hyprpaper, swaybg, wbg
#   awww      preferred; own daemon, multi-output, hotplug, transitions
#   hyprpaper Hyprland only, driven through hyprctl
#   swaybg    respawned per change (wlr-layer-shell)
#   wbg       respawned per change (wlr-layer-shell)
#   custom    use custom_command below
backend = "matugen"

# Only used with backend = "custom". Argv, not a shell line. {path} is
# replaced with the absolute wallpaper path.
# custom_command = ["my-wallpaper-tool", "--set", "{path}"]

# awww tuning (ignored by other backends).
awww_transition_type = "outer"
awww_transition_fps = 60
awww_transition_duration = 1.0
awww_resize = "crop"

# Matugen owns both template generation and wallpaper application. Its
# [config.wallpaper] section in ~/.config/matugen/config.toml should invoke
# awww (or another wallpaper daemon).
matugen_mode = "dark"
matugen_type = "scheme-tonal-spot"
# Which candidate source colour to pick. Required for matugen >= 4 when run
# without a TTY (i.e. always, from the daemon). One of: darkness, lightness,
# saturation, less-saturation, value, closest-to-fallback. Set to "" to omit.
matugen_prefer = "saturation"
# matugen_extra_args = ["--contrast", "0.2"]

# Optional hook run after a successful change. Argv, {path} substituted.
# post_command = ["makoctl", "reload"]

# Re-apply the last wallpaper when the plugin starts. This is what makes the
# wallpaper survive reboots and daemon restarts.
restore_on_start = true
# How long to keep retrying at startup while waiting for the compositor.
restore_timeout = "60s"
`

func writeDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(defaultConfigTemplate), 0o644)
}
