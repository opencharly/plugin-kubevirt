package kubevirt

import (
	"strings"
	"testing"
)

// ssh_alias_test.go pins opencharly/plugin-kubevirt#14's alias fix at the ACTUAL call sites.
// The bug was wrapping the already-prefixed CR name in `spec.VmSshAlias` again →
// `charly-charly-<domain>`, which broke the bed's check-live ssh. These drive the REAL
// call-site seams (`venueDescriptor`, `sshStanza`) so a regression that re-wraps the alias
// fails the test.

const wantAlias = "charly-check-kubevirt-vm-guest"

func TestVenueDescriptor_SinglePrefixedHost(t *testing.T) {
	p := lifecycleParams{Name: "check-kubevirt-vm-guest"}
	got := venueDescriptor(p).Host
	if got != wantAlias {
		t.Errorf("venueDescriptor Host = %q, want %q", got, wantAlias)
	}
	if strings.HasPrefix(strings.TrimPrefix(got, "charly-"), "charly-") {
		t.Errorf("venueDescriptor Host is DOUBLE-prefixed: %q", got)
	}
}

func TestSshStanza_SinglePrefixedAlias(t *testing.T) {
	p := lifecycleParams{Name: "check-kubevirt-vm-guest"}
	got := sshStanza(p, 41234, "arch", "/key").Alias
	if got != wantAlias {
		t.Errorf("sshStanza Alias = %q, want %q", got, wantAlias)
	}
	if strings.HasPrefix(strings.TrimPrefix(got, "charly-"), "charly-") {
		t.Errorf("sshStanza Alias is DOUBLE-prefixed: %q", got)
	}
}

func TestVenueSSHHost_SinglePrefixed(t *testing.T) {
	p := lifecycleParams{Name: "check-kubevirt-vm-guest"}
	if got := venueSSHHost(p); got != wantAlias {
		t.Errorf("venueSSHHost = %q, want %q", got, wantAlias)
	}
}
