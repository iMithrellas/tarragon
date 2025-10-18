package config

import (
    "os"
    "path/filepath"
    "testing"
)

func TestGenerateAndLoadConfig(t *testing.T) {
    dir := t.TempDir()
    cfg := filepath.Join(dir, "tarragon", "config.toml")
    if err := GenerateConfig(cfg); err != nil {
        t.Fatalf("GenerateConfig: %v", err)
    }
    if _, err := os.Stat(cfg); err != nil {
        t.Fatalf("config file missing: %v", err)
    }
    if err := LoadConfig(cfg); err != nil {
        t.Fatalf("LoadConfig: %v", err)
    }
}

