package kubevirt

import (
	"context"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestContainerDiskNodeAlias pins the node-alias derivation: a peer member's
// parent (node.MemberOf), an in-substrate dotted member name, and the
// charly-managed k3s-VM kubeconfig context all resolve to charly-<domain>; an
// external cluster yields "" (delivery skipped).
func TestContainerDiskNodeAlias(t *testing.T) {
	cases := []struct {
		name        string
		node        *spec.Deploy
		deployName  string
		kubeContext string
		want        string
	}{
		{"peer member via MemberOf", &spec.Deploy{MemberOf: "check-kubevirt-vm"}, "check-kubevirt-vm-guest", "", "charly-check-kubevirt-vm"},
		{"in-substrate dotted name", nil, "check-kubevirt-vm.guest", "", "charly-check-kubevirt-vm"},
		{"k3s vm kubeconfig context", nil, "theguest", "vm-check-kubevirt-vm", "charly-check-kubevirt-vm"},
		{"external cluster", nil, "theguest", "prod-cluster", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := containerDiskNodeAlias(c.node, c.deployName, c.kubeContext); got != c.want {
				t.Errorf("containerDiskNodeAlias(%v, %q, %q) = %q, want %q", c.node, c.deployName, c.kubeContext, got, c.want)
			}
		})
	}
}

// TestContainerDiskDeliveryTarget pins the PURE prepare-venue call-site gate (finding B12):
// the delivery must run ONLY for a container_disk source with a reachable node, NOT on a
// dry-run, NOT for another source kind, NOT for an external cluster, NOT for a nil entity.
// This is the condition at lifecycle.go's `kvPrepareVenue` call site, unit-tested without a
// cluster.
func TestContainerDiskDeliveryTarget(t *testing.T) {
	node := &spec.Deploy{MemberOf: "check-kubevirt-vm"}
	ref := "localhost/charly-check-kubevirt-vm-box:stable"
	cd := func() *spec.KubeVirt {
		return &spec.KubeVirt{Source: spec.KubevirtSource{Kind: "container_disk", Image: ref}}
	}
	cases := []struct {
		name        string
		kv          *spec.KubeVirt
		node        *spec.Deploy
		kubeContext string
		dryRun      bool
		wantDeliver bool
		wantAlias   string
	}{
		{"container_disk + reachable node", cd(), node, "", false, true, "charly-check-kubevirt-vm"},
		{"dry-run never delivers", cd(), node, "", true, false, ""},
		{"non-container_disk source", &spec.KubeVirt{Source: spec.KubevirtSource{Kind: "data_volume"}}, node, "", false, false, ""},
		{"empty image", &spec.KubeVirt{Source: spec.KubevirtSource{Kind: "container_disk", Image: ""}}, node, "", false, false, ""},
		{"nil entity", nil, node, "", false, false, ""},
		{"external cluster (no reachable node)", cd(), nil, "prod", false, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			alias, deliver := containerDiskDeliveryTarget(c.kv, c.node, "check-kubevirt-vm-guest", c.kubeContext, c.dryRun)
			if deliver != c.wantDeliver || alias != c.wantAlias {
				t.Errorf("containerDiskDeliveryTarget = (%q, %v), want (%q, %v)", alias, deliver, c.wantAlias, c.wantDeliver)
			}
		})
	}
}

// TestEnsureContainerDiskOnNode pins the streaming delivery: given a resolved target, it
// probes the node and imports ONLY when the image is absent (idempotent).
func TestEnsureContainerDiskOnNode(t *testing.T) {
	origPresent, origImport := containerDiskNodeImagePresent, containerDiskNodeImageImport
	t.Cleanup(func() { containerDiskNodeImagePresent, containerDiskNodeImageImport = origPresent, origImport })

	type imp struct{ alias, ref string }
	var imports []imp
	var probed []string
	containerDiskNodeImageImport = func(_ context.Context, _, alias, ref string) error {
		imports = append(imports, imp{alias, ref})
		return nil
	}

	host := spec.HostEnv{Home: "/home/tester"}
	ref := "localhost/charly-check-kubevirt-vm-box:stable"

	// Absent → import.
	containerDiskNodeImagePresent = func(_, alias, r string) (bool, error) {
		probed = append(probed, alias+"|"+r)
		return false, nil
	}
	if err := ensureContainerDiskOnNode(context.Background(), host, "charly-check-kubevirt-vm", ref); err != nil {
		t.Fatalf("absent case: %v", err)
	}
	if len(imports) != 1 || imports[0].alias != "charly-check-kubevirt-vm" || imports[0].ref != ref {
		t.Errorf("absent case imports = %v, want one import of %s to charly-check-kubevirt-vm", imports, ref)
	}
	if len(probed) != 1 || probed[0] != "charly-check-kubevirt-vm|"+ref {
		t.Errorf("absent case probed = %v, want exactly one probe of charly-check-kubevirt-vm|%s", probed, ref)
	}

	// Present → no import.
	containerDiskNodeImagePresent = func(_, _, _ string) (bool, error) { return true, nil }
	imports = nil
	if err := ensureContainerDiskOnNode(context.Background(), host, "charly-check-kubevirt-vm", ref); err != nil {
		t.Fatalf("present case: %v", err)
	}
	if len(imports) != 0 {
		t.Errorf("present case must not import; got %v", imports)
	}
}
