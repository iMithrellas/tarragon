package cli

import (
    "bytes"
    "os"
    "path/filepath"
    "testing"
)

func TestCompletionGenerateWritesSingleFileAndPrintsHint(t *testing.T) {
    dir := t.TempDir()
    t.Setenv("XDG_DATA_HOME", dir)

    var out bytes.Buffer
    rootCmd.SetOut(&out)
    rootCmd.SetArgs([]string{"completion", "generate", "bash"})
    if err := rootCmd.Execute(); err != nil {
        t.Fatalf("execute: %v", err)
    }

    target := filepath.Join(dir, "tarragon", "completions")
    // Only bash should exist
    if _, err := os.Stat(filepath.Join(target, "tarragon.bash")); err != nil {
        t.Fatalf("missing bash completion: %v", err)
    }
    if _, err := os.Stat(filepath.Join(target, "tarragon.zsh")); err == nil {
        t.Fatalf("zsh completion should not be generated")
    }

    // Output should include a bash sourcing hint
    got := out.String()
    if !bytes.Contains([]byte(got), []byte("bash: add to")) {
        t.Fatalf("expected bash instruction in output, got: %s", got)
    }
}
