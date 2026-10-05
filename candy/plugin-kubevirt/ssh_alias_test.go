package kubevirt

import "testing"

// ssh_alias_test.go pins opencharly/plugin-kubevirt#14's alias fix: `sshAlias` returns the
// managed ssh-config alias (the CR name, already `charly-`-prefixed). The call sites used
// to WRAP it in `kit.VmSshAlias` again, producing a DOUBLE `charly-charly-<domain>` alias
// that no consumer resolved — the bed's check-live ssh (`Could not resolve hostname
// charly-check-kubevirt-vm-guest`) failed on it after the port-forward layer was fixed.

func TestSshAlias_SingleCharlyPrefix(t *testing.T) {
	p := lifecycleParams{Name: "check-kubevirt-vm-guest"}
	got := sshAlias(p)
	if got != "charly-check-kubevirt-vm-guest" {
		t.Errorf("sshAlias = %q, want charly-check-kubevirt-vm-guest (exactly one charly- prefix)", got)
	}
	// Guard against the double-prefix regression explicitly.
	if s := "charly-" + got; got == s {
		t.Errorf("sshAlias must not itself carry the charly- prefix for kit.VmSshAlias to add")
	}
}
