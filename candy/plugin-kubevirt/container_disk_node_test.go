package kubevirt

import (
	"context"
	"strings"
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

// TestContainerDiskInNodeCtr pins the in-node store verb: `sudo k3s ctr -n k8s.io`. The k8s.io
// namespace is what the kubelet reads (plain `ctr` would be invisible to it), and k3s ships
// its own ctr, so the sudo+k3s prefix is required. Losing either breaks the delivery.
func TestContainerDiskInNodeCtr(t *testing.T) {
	if !strings.Contains(containerDiskInNodeCtr, "k3s ctr") {
		t.Errorf("containerDiskInNodeCtr = %q, want it to use k3s's own ctr", containerDiskInNodeCtr)
	}
	if !strings.Contains(containerDiskInNodeCtr, "-n k8s.io") {
		t.Errorf("containerDiskInNodeCtr = %q, want the k8s.io namespace the kubelet reads", containerDiskInNodeCtr)
	}
}

// TestEnsureContainerDiskOnNodeUsesVenue proves ensureContainerDiskOnNode drives the
// venue-generic verified transfer rather than a bespoke probe/import pair: with the node
// executor substituted, the venue's ctrOps probe runs `… images ls -q` in the k8s.io
// namespace and a present image is NOT re-streamed (the verified idempotency TransferImageToVenue
// owns). No ssh process is spawned.
func TestEnsureContainerDiskOnNodeUsesVenue(t *testing.T) {
	orig := containerDiskNodeExecutor
	t.Cleanup(func() { containerDiskNodeExecutor = orig })

	rec := &recExecutor{stdout: "localhost/charly-check-kubevirt-vm-box:stable\n"}
	containerDiskNodeExecutor = func(_, _ string) spec.DeployExecutor { return rec }

	host := spec.HostEnv{Home: "/home/tester"}
	ref := "localhost/charly-check-kubevirt-vm-box:stable"
	if err := ensureContainerDiskOnNode(context.Background(), host, "charly-check-kubevirt-vm", ref); err != nil {
		t.Fatalf("ensureContainerDiskOnNode: %v", err)
	}
	// The venue probed via the ctr verb (HasImage) — so a present image is a verified skip.
	if !rec.sawContains("images ls -q") {
		t.Errorf("venue did not probe via ctr images ls -q; calls=%v", rec.calls)
	}
	if rec.sawContains("import -") {
		t.Errorf("a present image must be a verified skip, not re-imported; calls=%v", rec.calls)
	}
}

// recExecutor is a DeployExecutor that records every command and answers RunCapture from a
// canned stdout — enough to drive the venue's HasImage probe without spawning ssh.
type recExecutor struct {
	calls  []string
	stdout string
}

func (e *recExecutor) Venue() string { return "rec://test" }
func (e *recExecutor) RunCapture(_ context.Context, script string) (string, string, int, error) {
	e.calls = append(e.calls, script)
	return e.stdout, "", 0, nil
}
func (e *recExecutor) RunSystem(_ context.Context, script string, _ spec.EmitOpts) error {
	e.calls = append(e.calls, "SYSTEM "+script)
	return nil
}
func (e *recExecutor) RunUser(_ context.Context, script string, _ spec.EmitOpts) error {
	e.calls = append(e.calls, "USER "+script)
	return nil
}
func (e *recExecutor) RunBuilder(context.Context, spec.BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *recExecutor) PutFile(context.Context, string, string, uint32, bool, spec.EmitOpts) error {
	return nil
}
func (e *recExecutor) GetFile(context.Context, string, bool, spec.EmitOpts) ([]byte, error) {
	return nil, nil
}
func (e *recExecutor) RunInteractive(context.Context, string) (int, error) { return -1, nil }
func (e *recExecutor) RunStream(context.Context, string) (int, error)      { return -1, nil }
func (e *recExecutor) Kind() string                                        { return "rec" }
func (e *recExecutor) ResolveHome(context.Context, string) (string, error) { return "/home/guest", nil }
func (e *recExecutor) sawContains(sub string) bool {
	for _, c := range e.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}
