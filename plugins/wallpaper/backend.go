package main

// Backend selection rationale
// ---------------------------
// This plugin deliberately does NOT open its own wlr-layer-shell surface.
//
// A background surface must be owned by a process that outlives everything
// else on the desktop. A Tarragon plugin is the opposite of that: the plugin
// manager starts it as a child of the daemon and terminates it whenever the
// daemon shuts down, reloads a lifecycle change, or the plugin is restarted.
// Shutdown is graceful (SIGTERM first), but graceful or not the process still
// exits, and with it the surface: every daemon restart would leave the user
// staring at a black screen. Painting the background from a launcher
// plugin also means reimplementing wl_output hotplug, per-output modes,
// fractional scaling, viewporter, shm buffer pools and image decoding, in Go,
// where the Wayland bindings are third-party and unproven.
//
// Delegating to a dedicated wallpaper daemon gives all of that for free and
// keeps the background alive independently of Tarragon. matugen is the default
// because it already runs its own daemon, handles multi-output and hotplug,
// caches decoded images and supports transitions, and exposes a trivial CLI.
// The other backends exist so the plugin is not a hard awww or matugen dependency.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Backend applies a wallpaper to the Wayland background layer.
type Backend interface {
	Name() string
	// Available reports whether this backend can be used right now.
	Available() bool
	// Apply sets path as the wallpaper. It may mutate st (e.g. to record a
	// spawned child PID) and is responsible for persisting nothing itself.
	Apply(ctx context.Context, path string, cfg *Config, st *State) error
}

// backendOrder is the auto-detection preference, best first.
var backendOrder = []string{"matugen", "awww", "hyprpaper", "swaybg", "wbg"}

func newBackend(name string) (Backend, error) {
	switch name {
	case "matugen":
		return &matugenBackend{}, nil
	case "awww":
		return &awwwBackend{}, nil
	case "hyprpaper":
		return &hyprpaperBackend{}, nil
	case "swaybg":
		return &spawnBackend{bin: "swaybg", args: []string{"-m", "fill", "-i", "{path}"}}, nil
	case "wbg":
		return &spawnBackend{bin: "wbg", args: []string{"{path}"}}, nil
	case "custom":
		return &customBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q", name)
	}
}

// resolveBackend picks the configured backend, or auto-detects one.
func resolveBackend(cfg *Config) (Backend, error) {
	if cfg.Backend != "auto" {
		b, err := newBackend(cfg.Backend)
		if err != nil {
			return nil, err
		}
		if cfg.Backend == "custom" && len(cfg.CustomCommand) == 0 {
			return nil, fmt.Errorf("backend = \"custom\" requires custom_command")
		}
		if !b.Available() {
			return nil, fmt.Errorf("backend %q is configured but not available on PATH", cfg.Backend)
		}
		return b, nil
	}

	if len(cfg.CustomCommand) > 0 {
		b := &customBackend{}
		if b.Available() {
			return b, nil
		}
	}
	for _, name := range backendOrder {
		b, err := newBackend(name)
		if err != nil || !b.Available() {
			continue
		}
		return b, nil
	}
	return nil, fmt.Errorf("no wallpaper backend found; install matugen or one of %s, or set custom_command in %s",
		strings.Join(backendOrder, ", "), configPath())
}

// matugenBackend delegates both theme generation and wallpaper application to
// matugen. Its [config.wallpaper] section is responsible for invoking awww (or
// another configured wallpaper command), so the plugin does not apply the
// image a second time.
type matugenBackend struct{}

func (m *matugenBackend) Name() string { return "matugen" }

func (m *matugenBackend) Available() bool {
	_, err := exec.LookPath("matugen")
	return err == nil
}

func (m *matugenBackend) Apply(ctx context.Context, path string, cfg *Config, _ *State) error {
	// Matugen owns the wallpaper command, but it does not manage the awww
	// daemon. Ensure the default Wayland wallpaper daemon exists before
	// matugen executes its [config.wallpaper] hook.
	if err := ensureAwwwDaemon(ctx); err != nil {
		return err
	}

	args := matugenArgs(path, cfg)

	return runCommand(ctx, "matugen", args...)
}

func matugenArgs(path string, cfg *Config) []string {
	args := []string{"image", path}
	if mode := strings.TrimSpace(cfg.MatugenMode); mode != "" {
		args = append(args, "--mode", mode)
	}
	if scheme := strings.TrimSpace(cfg.MatugenType); scheme != "" {
		args = append(args, "--type", scheme)
	}
	if prefer := strings.TrimSpace(cfg.MatugenPrefer); prefer != "" {
		args = append(args, "--prefer", prefer)
	}
	if cfg.MatugenOLED {
		args = append(args, "--lightness-dark", "-0.2")
	}
	args = append(args, cfg.MatugenExtraArgs...)
	return args
}

func ensureAwwwDaemon(ctx context.Context) error {
	if _, err := exec.LookPath("awww"); err != nil {
		return fmt.Errorf("matugen backend requires awww: %w", err)
	}
	if runCommand(ctx, "awww", "query") == nil {
		return nil
	}

	daemon, err := exec.LookPath("awww-daemon")
	if err != nil {
		return fmt.Errorf("awww daemon is not running and awww-daemon is unavailable: %w", err)
	}
	cmd := exec.Command(daemon)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start awww-daemon: %w", err)
	}
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if runCommand(ctx, "awww", "query") == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("awww-daemon did not become ready; check WAYLAND_DISPLAY=%q and its namespace", os.Getenv("WAYLAND_DISPLAY"))
}

// ─── awww ────────────────────────────────────────────────────────────────

type awwwBackend struct{}

func (s *awwwBackend) Name() string { return "awww" }

func (s *awwwBackend) Available() bool {
	_, err := exec.LookPath("awww")
	return err == nil
}

func (s *awwwBackend) Apply(ctx context.Context, path string, cfg *Config, _ *State) error {
	if err := s.ensureDaemon(ctx); err != nil {
		return err
	}

	args := []string{"img", path}
	if t := strings.TrimSpace(cfg.AwwwTransitionType); t != "" {
		args = append(args, "--transition-type", t)
	}
	if cfg.AwwwTransitionFPS > 0 {
		args = append(args, "--transition-fps", strconv.Itoa(cfg.AwwwTransitionFPS))
	}
	if cfg.AwwwTransitionDuration > 0 {
		args = append(args, "--transition-duration", strconv.FormatFloat(cfg.AwwwTransitionDuration, 'f', -1, 64))
	}
	if r := strings.TrimSpace(cfg.AwwwResizeMode); r != "" {
		args = append(args, "--resize", r)
	}
	if c := strings.TrimSpace(cfg.AwwwFillColor); c != "" {
		args = append(args, "--fill-color", c)
	}
	return runCommand(ctx, "awww", args...)
}

// ensureDaemon starts awww-daemon if it is not already answering.
// The daemon is detached (setsid) so it survives this plugin being killed.
func (s *awwwBackend) ensureDaemon(ctx context.Context) error {
	if runCommand(ctx, "awww", "query") == nil {
		return nil
	}

	var start *exec.Cmd
	if bin, err := exec.LookPath("awww-daemon"); err == nil {
		start = exec.Command(bin)
	} else {
		// awww < 0.9 spawns its daemon via `swww init`.
		start = exec.Command("awww", "init")
	}
	detach(start)
	start.Stdout = nil
	start.Stderr = nil
	if err := start.Start(); err != nil {
		return fmt.Errorf("start awww daemon: %w", err)
	}
	go func() { _ = start.Wait() }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if runCommand(ctx, "awww", "query") == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("awww daemon did not become ready")
}

// ─── hyprpaper ───────────────────────────────────────────────────────────

type hyprpaperBackend struct{}

func (h *hyprpaperBackend) Name() string { return "hyprpaper" }

func (h *hyprpaperBackend) Available() bool {
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		return false
	}
	if _, err := exec.LookPath("hyprctl"); err != nil {
		return false
	}
	_, err := exec.LookPath("hyprpaper")
	return err == nil
}

func (h *hyprpaperBackend) Apply(ctx context.Context, path string, _ *Config, _ *State) error {
	if err := h.ensureRunning(ctx); err != nil {
		return err
	}
	if err := runCommand(ctx, "hyprctl", "hyprpaper", "preload", path); err != nil {
		return fmt.Errorf("hyprpaper preload: %w", err)
	}
	// Empty monitor field means "all outputs".
	if err := runCommand(ctx, "hyprctl", "hyprpaper", "wallpaper", ","+path); err != nil {
		return fmt.Errorf("hyprpaper wallpaper: %w", err)
	}
	// Best effort; frees the previous image from VRAM.
	_ = runCommand(ctx, "hyprctl", "hyprpaper", "unload", "unused")
	return nil
}

func (h *hyprpaperBackend) ensureRunning(ctx context.Context) error {
	if runCommand(ctx, "hyprctl", "hyprpaper", "listloaded") == nil {
		return nil
	}
	cmd := exec.Command("hyprpaper")
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hyprpaper: %w", err)
	}
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if runCommand(ctx, "hyprctl", "hyprpaper", "listloaded") == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("hyprpaper did not become ready")
}

// ─── swaybg / wbg (respawn model) ────────────────────────────────────────

// spawnBackend drives daemons that have no IPC: the only way to change the
// wallpaper is to start a new instance and kill the old one. The new instance
// is detached so it outlives this plugin, and its PID is recorded in the state
// file so it can still be replaced after a plugin restart.
type spawnBackend struct {
	bin  string
	args []string
}

func (s *spawnBackend) Name() string { return s.bin }

func (s *spawnBackend) Available() bool {
	_, err := exec.LookPath(s.bin)
	return err == nil
}

func (s *spawnBackend) Apply(ctx context.Context, path string, _ *Config, st *State) error {
	args := substituteArgs(s.args, path)
	cmd := exec.Command(s.bin, args...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", s.bin, err)
	}
	go func() { _ = cmd.Wait() }()

	old := st.SwapSpawnPID(cmd.Process.Pid)

	if old > 0 && old != cmd.Process.Pid {
		// Let the new surface map before tearing the old one down, otherwise
		// the background flashes. This must be synchronous: under the on_call
		// lifecycle the process exits as soon as Apply returns, so a
		// background goroutine would simply never run.
		select {
		case <-ctx.Done():
		case <-time.After(400 * time.Millisecond):
		}
		// The recorded PID may be stale (e.g. it survived a reboot and has
		// since been recycled), so confirm it is still our backend before
		// signalling it.
		if processIsBinary(old, s.bin) {
			_ = syscall.Kill(old, syscall.SIGTERM)
		}
	}
	return nil
}

// processIsBinary reports whether pid is currently running the named binary.
// It guards against killing an unrelated process that inherited a recycled
// PID from a previous boot.
func processIsBinary(pid int, bin string) bool {
	// /proc/<pid>/comm carries the executable name, truncated to 15 chars.
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		comm := strings.TrimSpace(string(raw))
		if comm == bin || (len(comm) == 15 && strings.HasPrefix(bin, comm)) {
			return true
		}
	}
	// Fall back to argv, which also covers interpreted wrappers where comm
	// is the interpreter rather than the script.
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	for _, a := range strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00") {
		if a != "" && filepath.Base(a) == bin {
			return true
		}
	}
	return false
}

// ─── custom ──────────────────────────────────────────────────────────────

type customBackend struct{}

func (c *customBackend) Name() string { return "custom" }

func (c *customBackend) Available() bool { return true }

func (c *customBackend) Apply(ctx context.Context, path string, cfg *Config, _ *State) error {
	if len(cfg.CustomCommand) == 0 {
		return fmt.Errorf("custom_command is empty")
	}
	args := substituteArgs(cfg.CustomCommand, path)
	return runCommand(ctx, args[0], args[1:]...)
}

// ─── helpers ─────────────────────────────────────────────────────────────

// substituteArgs replaces the {path} placeholder in an argv template.
// If no placeholder is present the path is appended.
func substituteArgs(template []string, path string) []string {
	out := make([]string, 0, len(template)+1)
	replaced := false
	for _, a := range template {
		if strings.Contains(a, "{path}") {
			replaced = true
			a = strings.ReplaceAll(a, "{path}", path)
		}
		out = append(out, a)
	}
	if !replaced {
		out = append(out, path)
	}
	return out
}

func runCommand(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = nil
	if err := cmd.Run(); err != nil {
		msg := cleanOutput(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", bin, err, truncate(msg, 200))
		}
		return fmt.Errorf("%s: %w", bin, err)
	}
	return nil
}

// ansiRE matches SGR/CSI escape sequences. Tools like matugen colourise their
// errors even when stderr is a pipe, and those bytes would otherwise end up
// verbatim in the select_response message shown by the UI.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// cleanOutput flattens command output into a single readable line.
func cleanOutput(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, "; ")
}

// detach puts the child in its own session so it is not killed together with
// the plugin's process group when the daemon SIGKILLs us.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
