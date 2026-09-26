package kubevirt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// TestManagedSSHStanzaPublishAndResolve exercises the CHANGED path live via the REAL
// kit writer/reader against a real temp HOME — the exact call the deploy lifecycle's
// PrepareVenue makes to publish the guest's ssh stanza (kit.WriteVmSshStanza with
// kit.VmSshAlias(deployDomain(p))). It then asserts a CONSUMER (plugin-check's readiness
// gate / plugin-deploy-vm / `charly vm cp-box`, all of which derive
// kit.VmSshAlias(spec.VmDomainIdentity(deploy))) resolves the alias the lifecycle wrote.
// Before the fix the write went to "charly-charly-<domain>" and this resolve missed.
func TestManagedSSHStanzaPublishAndResolve(t *testing.T) {
	home := t.TempDir()
	p := lifecycleParams{Name: "check-kv"}

	// The lifecycle's publish call, verbatim (lifecycle.go kvPrepareVenue).
	alias := kit.VmSshAlias(deployDomain(p))
	if err := kit.WriteVmSshStanza(home, kit.VmSshStanza{
		Alias:        alias,
		Hostname:     "127.0.0.1",
		Port:         2224,
		User:         "arch",
		IdentityFile: filepath.Join(home, ".ssh", "id_kv"),
	}); err != nil {
		t.Fatalf("publish stanza: %v", err)
	}

	// Read the fragment the real writer produced.
	frag := kit.SshFragmentPath(home)
	b, err := os.ReadFile(frag)
	if err != nil {
		t.Fatalf("read stanza fragment %s: %v", frag, err)
	}
	got := string(b)
	t.Logf("published fragment %s:\n%s", frag, strings.TrimRight(got, "\n"))

	// The consumer's derivation (plugin-check bed readiness + cp-box) must match.
	consumerAlias := kit.VmSshAlias(spec.VmDomainIdentity(p.Name))
	if consumerAlias != alias {
		t.Fatalf("consumer alias %q != published alias %q", consumerAlias, alias)
	}
	if !strings.Contains(got, "Host "+alias) {
		t.Fatalf("fragment does not declare the published Host %q:\n%s", alias, got)
	}
	if strings.Contains(got, "charly-charly-") {
		t.Fatalf("fragment carries a double-prefixed alias:\n%s", got)
	}
}
