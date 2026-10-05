package kubevirt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// virtctl_resolve_test.go pins opencharly/plugin-kubevirt#11's fix: the plugin shells
// out to a HOST-side virtctl for the managed port-forward (and the verb/CLI arms), and
// must resolve it explicitly — and FAIL LOUDLY when it is absent — instead of spawning a
// bare `virtctl` that dies with a message buried in port-forward.log (which surfaced only
// as a misleading 30-minute `wait-for-sshd … :0` timeout).

// TestResolveVirtctl_EnvOverrideWins proves $CHARLY_VIRTCTL is honoured first.
func TestResolveVirtctl_EnvOverrideWins(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "virtctl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHARLY_VIRTCTL", bin)
	got, err := resolveVirtctl()
	if err != nil {
		t.Fatalf("resolveVirtctl: %v", err)
	}
	if got != bin {
		t.Errorf("resolveVirtctl = %q, want the $CHARLY_VIRTCTL override %q", got, bin)
	}
}

// TestResolveVirtctl_BadEnvOverrideErrors proves a bad override fails CLOSED (never a
// silent fallthrough to a different binary).
func TestResolveVirtctl_BadEnvOverrideErrors(t *testing.T) {
	t.Setenv("CHARLY_VIRTCTL", filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := resolveVirtctl(); err == nil {
		t.Fatal("resolveVirtctl with a non-existent $CHARLY_VIRTCTL: want an error, got nil")
	} else if !strings.Contains(err.Error(), "CHARLY_VIRTCTL") {
		t.Errorf("error must name $CHARLY_VIRTCTL; got: %v", err)
	}
}

// TestResolveVirtctl_NotFoundIsLoudAndNamed pins the contract the RCA asks for: when no
// virtctl resolves, the error names virtctl (not a downstream sshd timeout). Skips when
// the environment genuinely has /usr/bin/virtctl or a PATH virtctl.
func TestResolveVirtctl_NotFoundIsLoudAndNamed(t *testing.T) {
	t.Setenv("CHARLY_VIRTCTL", "")
	if _, err := os.Stat("/usr/bin/virtctl"); err == nil {
		t.Skip("/usr/bin/virtctl exists on this host")
	}
	if _, err := exec.LookPath("virtctl"); err == nil {
		t.Skip("virtctl on PATH on this host")
	}
	_, err := resolveVirtctl()
	if err == nil {
		t.Fatal("resolveVirtctl with no virtctl anywhere: want a loud error, got nil")
	}
	if !strings.Contains(err.Error(), "virtctl not found") {
		t.Errorf("error must name the missing virtctl; got: %v", err)
	}
}
