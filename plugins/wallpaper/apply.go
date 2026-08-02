package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Applier serialises wallpaper changes and owns the backend + state.
type Applier struct {
	mu      sync.Mutex
	cfg     *Config
	state   *State
	backend Backend
	log     *Logger
}

func NewApplier(cfg *Config, state *State, log *Logger) *Applier {
	return &Applier{cfg: cfg, state: state, log: log}
}

// backendLocked resolves (and caches) the backend. Resolution is lazy so the
// plugin still starts on a machine where no wallpaper daemon is installed yet;
// the error surfaces on the first attempt instead of killing the process.
// Callers must hold a.mu.
func (a *Applier) backendLocked() (Backend, error) {
	if a.backend != nil {
		return a.backend, nil
	}
	b, err := resolveBackend(a.cfg)
	if err != nil {
		return nil, err
	}
	a.backend = b
	a.log.Info("using backend %q", b.Name())
	return b, nil
}

func (a *Applier) resetBackend() {
	a.mu.Lock()
	a.backend = nil
	a.mu.Unlock()
}

// BackendName returns the active backend name, resolving it if needed.
func (a *Applier) BackendName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, err := a.backendLocked()
	if err != nil {
		return "unavailable"
	}
	return b.Name()
}

// Apply sets the wallpaper, persists it, and optionally regenerates matugen
// templates. withTheme=false skips matugen for the "set only" action.
func (a *Applier) Apply(ctx context.Context, path string, withTheme bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.applyLocked(ctx, path, withTheme)
}

func (a *Applier) applyLocked(ctx context.Context, path string, withTheme bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("wallpaper %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("wallpaper %s is a directory", path)
	}

	backend, err := a.backendLocked()
	if err != nil {
		return err
	}

	setCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := backend.Apply(setCtx, path, a.cfg, a.state); err != nil {
		return fmt.Errorf("%s: %w", backend.Name(), err)
	}

	a.state.Record(path, backend.Name())
	if err := a.state.Save(); err != nil {
		// Non-fatal: the wallpaper is set, only persistence failed.
		a.log.Error("failed to persist state: %v", err)
	}

	if withTheme && a.cfg.Matugen {
		if err := a.runMatugen(ctx, path); err != nil {
			// Also non-fatal: a broken matugen config should not make the
			// wallpaper change look like a failure.
			a.log.Error("matugen failed: %v", err)
			return fmt.Errorf("wallpaper set, but matugen failed: %w", err)
		}
	}

	if len(a.cfg.PostCommand) > 0 {
		if err := a.runPostCommand(ctx, path); err != nil {
			a.log.Error("post_command failed: %v", err)
		}
	}

	return nil
}

// Restore re-applies the persisted wallpaper. It retries because at login the
// Tarragon user service can easily win the race against the compositor, in
// which case no backend is reachable yet.
func (a *Applier) Restore(ctx context.Context) error {
	current := a.state.Current()
	if current == "" {
		a.log.Info("no persisted wallpaper to restore")
		return nil
	}
	if _, err := os.Stat(current); err != nil {
		a.log.Error("persisted wallpaper %s is gone: %v", current, err)
		return err
	}

	deadline := time.Now().Add(a.cfg.restoreTimeout())
	delay := 250 * time.Millisecond
	var lastErr error

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Theming is skipped on restore: matugen templates were already
		// written when the wallpaper was originally chosen, and rerunning
		// it on every login would needlessly rewrite dotfiles.
		err := a.Apply(ctx, current, false)
		if err == nil {
			a.log.Info("restored wallpaper %s (attempt %d)", current, attempt)
			return nil
		}
		lastErr = err
		a.resetBackend() // re-detect; the compositor may not be up yet

		if time.Now().After(deadline) {
			break
		}
		a.log.Info("restore attempt %d failed (%v), retrying in %s", attempt, err, delay)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
	return fmt.Errorf("restore gave up after %s: %w", a.cfg.restoreTimeout(), lastErr)
}

// runMatugen regenerates the user's matugen templates from the wallpaper.
func (a *Applier) runMatugen(ctx context.Context, path string) error {
	if _, err := exec.LookPath("matugen"); err != nil {
		return fmt.Errorf("matugen not found on PATH")
	}

	args := []string{"image", path}
	if m := strings.TrimSpace(a.cfg.MatugenMode); m != "" {
		args = append(args, "--mode", m)
	}
	if t := strings.TrimSpace(a.cfg.MatugenType); t != "" {
		args = append(args, "--type", t)
	}
	if pref := strings.TrimSpace(a.cfg.MatugenPrefer); pref != "" {
		args = append(args, "--prefer", pref)
	}
	args = append(args, a.cfg.MatugenExtraArgs...)

	mctx, cancel := context.WithTimeout(ctx, a.cfg.matugenTimeout())
	defer cancel()

	start := time.Now()
	if err := runCommand(mctx, "matugen", args...); err != nil {
		return err
	}
	a.log.Info("matugen regenerated templates in %s", time.Since(start).Round(time.Millisecond))
	return nil
}

func (a *Applier) runPostCommand(ctx context.Context, path string) error {
	args := substituteArgs(a.cfg.PostCommand, path)
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return runCommand(pctx, args[0], args[1:]...)
}
