package cli

import (
    "os"
    "path/filepath"
    "testing"
)

func TestConfigGenerateCreatesFile(t *testing.T) {
    // Prepare a temp config dir via flag
    dir := t.TempDir()
    rootCmd.SetArgs([]string{"--config-dir", dir, "config", "generate", "--config-format", "toml"})
    if err := rootCmd.Execute(); err != nil {
        t.Fatalf("execute: %v", err)
    }
    cfg := filepath.Join(dir, "tarragon.toml")
    if _, err := os.Stat(cfg); err != nil {
        t.Fatalf("expected config at %s: %v", cfg, err)
    }
}
