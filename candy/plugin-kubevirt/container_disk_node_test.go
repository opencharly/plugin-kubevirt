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

// TestEnsureContainerDiskOnNode pins the delivery decision table: a container_disk
// source with a reachable node imports only when the image is absent; every other
// shape is a no-op.
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
	node := &spec.Deploy{MemberOf: "check-kubevirt-vm"}
	ref := "localhost/charly-check-kubevirt-vm-box:stable"
	kvCD := func() *spec.KubeVirt {
		return &spec.KubeVirt{Source: spec.KubevirtSource{Kind: "container_disk", Image: ref}}
	}

	// Absent → import.
	containerDiskNodeImagePresent = func(_, alias, r string) (bool, error) {
		probed = append(probed, alias+"|"+r)
		return false, nil
	}
	imports = nil
	if err := ensureContainerDiskOnNode(context.Background(), host, node, "check-kubevirt-vm-guest", "", kvCD()); err != nil {
		t.Fatalf("absent case: %v", err)
	}
	if len(imports) != 1 || imports[0].alias != "charly-check-kubevirt-vm" || imports[0].ref != ref {
		t.Errorf("absent case imports = %v, want one import of %s to charly-check-kubevirt-vm", imports, ref)
	}

	// Present → no import.
	containerDiskNodeImagePresent = func(_, _, _ string) (bool, error) { return true, nil }
	imports = nil
	if err := ensureContainerDiskOnNode(context.Background(), host, node, "check-kubevirt-vm-guest", "", kvCD()); err != nil {
		t.Fatalf("present case: %v", err)
	}
	if len(imports) != 0 {
		t.Errorf("present case must not import; got %v", imports)
	}

	// Non-container_disk source → no-op (no probe, no import).
	containerDiskNodeImagePresent = func(_, _, _ string) (bool, error) {
		t.Error("a non-container_disk source must not probe the node")
		return false, nil
	}
	imports = nil
	other := &spec.KubeVirt{Source: spec.KubevirtSource{Kind: "data_volume"}}
	if err := ensureContainerDiskOnNode(context.Background(), host, node, "check-kubevirt-vm-guest", "", other); err != nil {
		t.Fatalf("data_volume case: %v", err)
	}
	if len(imports) != 0 {
		t.Errorf("data_volume case must not import; got %v", imports)
	}

	// No reachable node (external cluster) → no-op (no probe, no import).
	containerDiskNodeImagePresent = func(_, _, _ string) (bool, error) {
		t.Error("an external cluster must not probe a node")
		return false, nil
	}
	if err := ensureContainerDiskOnNode(context.Background(), host, nil, "theguest", "prod", kvCD()); err != nil {
		t.Fatalf("external case: %v", err)
	}

	// nil kv → no-op.
	if err := ensureContainerDiskOnNode(context.Background(), host, node, "x", "", nil); err != nil {
		t.Fatalf("nil kv case: %v", err)
	}

	_ = probed
}
