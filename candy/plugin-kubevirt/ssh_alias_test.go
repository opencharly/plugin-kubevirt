package kubevirt

import (
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// TestManagedSSHAliasIsCanonical pins the ONE managed-ssh alias a kubevirt deploy
// publishes and every consumer resolves: `kit.VmSshAlias(spec.VmDomainIdentity(deploy))`
// = "charly-<domain>". The former `sshAlias(p)` returned the already-namespaced CR name
// ("charly-<domain>"), which `kit.VmSshAlias` prefixed a SECOND time → the stanza + venue
// were written under "charly-charly-<domain>" — an alias plugin-check's readiness gate,
// plugin-deploy-vm, and `charly vm cp-box` can never resolve. This test fails if the
// double-prefix returns.
func TestManagedSSHAliasIsCanonical(t *testing.T) {
	for _, name := range []string{"check-kv", "vm:check-kv", "kubevirt:prod", "a.b"} {
		p := lifecycleParams{Name: name}
		want := spec.VmSshAlias(spec.VmDomainIdentity(name)) // charly-<domain> — the canonical form
		got := kit.VmSshAlias(deployDomain(p))               // what PrepareVenue/publish/teardown all use
		if got != want {
			t.Errorf("name %q: managed ssh alias = %q, want %q (single charly- prefix)", name, got, want)
		}
		if got != "charly-"+spec.VmDomainIdentity(name) {
			t.Errorf("name %q: alias %q is not charly-<domain>", name, got)
		}
	}
	// The double-prefix MUST be gone: no name yields charly-charly-.
	if alias := kit.VmSshAlias(deployDomain(lifecycleParams{Name: "check-kv"})); alias == "charly-charly-check-kv" {
		t.Fatal("managed ssh alias is double-prefixed (charly-charly-<domain>) — consumers cannot resolve it")
	}
}
