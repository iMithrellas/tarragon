package cli

import (
	"strings"
	"testing"
)

func TestRewriteManifestForSystem_RewritesRelativeEntrypointAndPreservesOtherKeys(t *testing.T) {
	manifest := []byte(`name = "demo"
description = "demo plugin"
entrypoint = "demo-plugin"
lifecycle_mode = "on_call"
custom_flag = "preserve-me"
`)

	out, name, err := rewriteManifestForSystem(manifest, "/usr/bin/demo", "demo")
	if err != nil {
		t.Fatalf("rewriteManifestForSystem error: %v", err)
	}
	if name != "demo" {
		t.Fatalf("plugin name = %q, want demo", name)
	}

	got := string(out)
	if !strings.Contains(got, `entrypoint = "/usr/bin/demo"`) {
		t.Fatalf("expected rewritten absolute entrypoint, got:\n%s", got)
	}
	if !strings.Contains(got, `source = "system"`) {
		t.Fatalf("expected source marker, got:\n%s", got)
	}
	if !strings.Contains(got, `custom_flag = "preserve-me"`) {
		t.Fatalf("expected custom key to be preserved, got:\n%s", got)
	}
}

func TestRewriteManifestForSystem_KeepsAbsoluteEntrypoint(t *testing.T) {
	manifest := []byte(`name = "demo"
entrypoint = "/opt/bin/demo"
lifecycle_mode = "on_call"
source = "custom"
`)

	out, _, err := rewriteManifestForSystem(manifest, "/usr/bin/demo", "demo")
	if err != nil {
		t.Fatalf("rewriteManifestForSystem error: %v", err)
	}

	got := string(out)
	if !strings.Contains(got, `entrypoint = "/opt/bin/demo"`) {
		t.Fatalf("expected absolute entrypoint to stay unchanged, got:\n%s", got)
	}
	if !strings.Contains(got, `source = "system"`) {
		t.Fatalf("expected source to be overridden to system, got:\n%s", got)
	}
}
