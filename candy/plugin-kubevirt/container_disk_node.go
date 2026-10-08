package kubevirt

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/container"
	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// container_disk_node.go — the containerDisk delivery for a kind:kubevirt member, expressed
// through the VENUE-GENERIC verified transfer.
//
// A `container_disk.image` may be a locally-built charly VM box that exists only in the host
// engine store; the k3s NODE's containerd has never seen it, so a `WaitVMIReady` with
// `imagePullPolicy: Never`/`IfNotPresent` would fail. Before the member applies its
// VirtualMachine CR, this ensures the image is present in the node's containerd.
//
// This used to be a SELF-CONTAINED probe+`StreamLoad` pair (its own `ctr images ls -q` and
// `podman save | ssh ... ctr images import -`). It is now a thin CONSUMER of the one shared
// path — `deploykit.TransferImageToVenue` over `deploykit.NewNodeVenue` (sdk v0.2026280.1341)
// — the SAME verified idempotency, torn-overlay recovery and tag that `charly box load` and
// `charly vm cp-box` run. R3: one delivery implementation for every node/venue, not a third.
//
// The node is the member's PARENT (a peer/group member): `node.MemberOf` carries the folded
// owner key (the loader stamps it), so the managed ssh alias is `charly-<domain>`. When the
// member has no charly-managed node parent (an external cluster), the image must come from a
// registry and this delivery is SKIPPED cleanly.

// containerDiskNodeAlias names the k3s node's managed ssh alias for a kubevirt member. It
// returns "" when the node is not a charly-managed VM.
func containerDiskNodeAlias(node *spec.Deploy, name, kubeContext string) string {
	owner := ""
	if node != nil && node.MemberOf != "" {
		owner = node.MemberOf
	} else if i := strings.LastIndexByte(name, '.'); i >= 0 {
		// An in-substrate member (a dotted name) is owned by the name before the
		// last dot; a dotted deploy key of the form deploy.member mirrors the same
		// owner (the plugin-adb androidParentContainer precedent).
		owner = name[:i]
	}
	if owner != "" {
		return kit.VmSshAlias(spec.VmDomainIdentity(owner))
	}
	// A charly-managed k3s VM publishes its kubeconfig context as vm-<domain>
	// (plugin-kube's k3s post-provision). Its ssh alias is charly-<domain>.
	if strings.HasPrefix(kubeContext, "vm-") {
		return kit.VmSshAlias(strings.TrimPrefix(kubeContext, "vm-"))
	}
	return ""
}

// containerDiskDeliveryTarget decides whether prepare-venue must stream a locally-built
// containerDisk into the node before the CR is applied, and to which ssh alias. PURE (no
// I/O) so the prepare-venue call-site gate is unit-testable: deliver is false when
// dry-run (no cluster touched), when there is no container_disk source, or when the node
// is not charly-reachable (an external cluster pulls from its own registry).
func containerDiskDeliveryTarget(kv *spec.KubeVirt, node *spec.Deploy, name, kubeContext string, dryRun bool) (alias string, deliver bool) {
	if dryRun || kv == nil || kv.Source.Kind != "container_disk" || kv.Source.Image == "" {
		return "", false
	}
	alias = containerDiskNodeAlias(node, name, kubeContext)
	if alias == "" {
		return "", false
	}
	return alias, true
}

// containerDiskInNodeCtr is the in-node containerd verb prefix. k3s ships its OWN ctr, so the
// node's CRI store is reached with `sudo k3s ctr`; `-n k8s.io` is the namespace the kubelet
// reads (plain `ctr` defaults to `default` and would be invisible to the kubelet). It is the
// ONE place this venue's store scope is decided — the probe, tag, removal and load all go
// through it, so they can never address a different store.
const containerDiskInNodeCtr = "sudo k3s ctr -n k8s.io"

// containerDiskNodeExecutor builds the venue transport: an SSH DeployExecutor to the
// charly-managed k3s node, reading the managed ssh_config fragment (the `-F` the retired
// bespoke path passed by hand). Package var so a test can substitute a recorder without
// spawning ssh.
var containerDiskNodeExecutor = func(home, alias string) spec.DeployExecutor {
	return &specexec.SSHExecutor{Host: alias, Args: []string{"-F", kit.SshConfigPath(home)}}
}

// ensureContainerDiskOnNode streams ref into the node's containerd via alias (the target
// decided by containerDiskDeliveryTarget), through the venue-generic verified transfer: a
// deploykit.NewNodeVenue whose ctrOps probes with `... ctr -n k8s.io images ls -q` (verified
// idempotency) and whose load streams `podman save <ref>` over SSH into
// `... ctr -n k8s.io images import -` — no fork of TransferImageToVenue.
func ensureContainerDiskOnNode(ctx context.Context, host spec.HostEnv, alias, ref string) error {
	nodeExec := containerDiskNodeExecutor(host.Home, alias)
	hostEngine := "podman"
	if rt, rerr := kit.ResolveRuntime(); rerr == nil && rt.RunEngine != "" {
		hostEngine = rt.RunEngine
	}
	venue := deploykit.NewNodeVenue(
		nodeExec,
		containerDiskInNodeCtr,
		func() *exec.Cmd {
			// The HOST-side load reader: a real ssh process that feeds the `save` stream on
			// stdin to the node's `… ctr … images import -`. Its store scope is DERIVED from
			// containerDiskInNodeCtr — the SAME constant the venue's probe/tag/remove use — so
			// the load and the verification cannot address different stores (a future change to
			// the constant moves both together; the invariant the comment states is true).
			argv := append([]string{"-F", kit.SshConfigPath(host.Home), alias}, strings.Fields(containerDiskInNodeCtr)...)
			argv = append(argv, "images", "import", "-")
			return exec.CommandContext(ctx, "ssh", argv...)
		},
		"containerDisk",
	)
	if err := deploykit.TransferImageToVenue(ctx, venue, container.EngineBinary(hostEngine), ref, "", deploykit.EmitOpts{}); err != nil {
		return fmt.Errorf("delivering %s into node %q containerd: %w", ref, alias, err)
	}
	return nil
}
