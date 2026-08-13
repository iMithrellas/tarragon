package cli

import (
	"bytes"
	"testing"
)

func TestCompletionGenerateWritesScriptToStdout(t *testing.T) {
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"completion", "generate", "zsh"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !bytes.HasPrefix([]byte(got), []byte("#compdef tarragon")) {
		t.Fatalf("expected zsh completion script, got: %s", got)
	}
}
