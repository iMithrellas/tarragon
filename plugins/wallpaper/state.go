package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is the persisted wallpaper selection.
//
// This file is what makes the wallpaper survive a reboot: on start the plugin
// reads it back and re-applies. It lives under XDG_STATE_HOME rather than the
// config dir because it is machine state, not user configuration.
type State struct {
	Path      string    `json:"path"`
	Backend   string    `json:"backend,omitempty"`
	AppliedAt time.Time `json:"applied_at,omitempty"`
	// SpawnPID tracks the detached child of respawn-style backends
	// (swaybg/wbg) so a later change can replace it even across restarts.
	SpawnPID int `json:"spawn_pid,omitempty"`
	// History is a small MRU list, newest first.
	History []string `json:"history,omitempty"`

	mu   sync.Mutex
	path string
}

const historyLimit = 20

func statePath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "tarragon", "wallpaper", "state.json")
}

func loadState() *State {
	p := statePath()
	st := &State{path: p}
	b, err := os.ReadFile(p)
	if err != nil {
		return st
	}
	// A corrupt state file must never stop the plugin from starting.
	_ = json.Unmarshal(b, st)
	st.path = p
	return st
}

// Save writes the state atomically so a crash mid-write cannot corrupt it.
func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Record updates the current selection and pushes it onto the MRU history.
func (s *State) Record(path, backend string) {
	s.mu.Lock()
	s.Path = path
	s.Backend = backend
	s.AppliedAt = time.Now()

	history := make([]string, 0, historyLimit)
	history = append(history, path)
	for _, h := range s.History {
		if h == path {
			continue
		}
		history = append(history, h)
		if len(history) >= historyLimit {
			break
		}
	}
	s.History = history
	s.mu.Unlock()
}

func (s *State) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Path
}

// SwapSpawnPID stores the PID of a newly spawned backend process and returns
// the one it replaces.
func (s *State) SwapSpawnPID(pid int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.SpawnPID
	s.SpawnPID = pid
	return old
}
