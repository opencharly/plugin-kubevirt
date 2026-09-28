package kubevirt

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

// container_disk_node.go — the SELF-CONTAINED containerDisk delivery for a
// kind:kubevirt member.
//
// A `container_disk.image` may be a locally-built charly VM box that exists only
// in the host engine store; the k3s NODE's containerd has never seen it, so a
// `WaitVMIReady` with `imagePullPolicy: Never`/`IfNotPresent` would fail. Before
// the member applies its VirtualMachine CR, this ensures the image is present in
// the node's containerd by STREAMING it host-side — the SAME `save | import`
// discipline every other delivery path uses (spec/container.StreamLoad):
//
//	podman save <ref> | ssh <node-alias> 'sudo k3s ctr -n k8s.io images import -'
//
// The namespace is `k8s.io` — the CRI namespace the kubelet reads; plain `ctr`
// defaults to `default` and would be invisible to the kubelet.
//
// The node is the member's PARENT (a peer/group member): `node.MemberOf` carries
// the folded owner key (the loader stamps it), so the managed ssh alias is
// `charly-<domain>`. When the member has no charly-managed node parent (an
// external cluster), the image must come from a registry and this delivery is
// SKIPPED cleanly.

// containerDiskNodeAlias names the k3s node's managed ssh alias for a kubevirt
// member. It returns "" when the node is not a charly-managed VM.
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

// ensureContainerDiskOnNode delivers kv.Source.Image into the node's containerd
// when it is a container_disk source and the node is charly-reachable; otherwise
// it is a no-op. Idempotent: a present image is not re-streamed.
func ensureContainerDiskOnNode(ctx context.Context, host spec.HostEnv, node *spec.Deploy, name, kubeContext string, kv *spec.KubeVirt) error {
	if kv == nil || kv.Source.Kind != "container_disk" || kv.Source.Image == "" {
		return nil
	}
	alias := containerDiskNodeAlias(node, name, kubeContext)
	if alias == "" {
		return nil // external cluster: the node pulls the image from its registry
	}
	present, err := containerDiskNodeImagePresent(host.Home, alias, kv.Source.Image)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	return containerDiskNodeImageImport(ctx, host.Home, alias, kv.Source.Image)
}

// containerDiskNodeImagePresent reports whether the node's containerd already
// holds ref (in the kubelet's k8s.io namespace). Package var (test seam).
var containerDiskNodeImagePresent = func(home, alias, ref string) (bool, error) {
	out, err := exec.Command("ssh", "-F", kit.SshConfigPath(home), alias,
		"sudo", "k3s", "ctr", "-n", "k8s.io", "images", "ls", "-q").Output()
	if err != nil {
		return false, fmt.Errorf("probing node %q containerd: %w", alias, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == ref {
			return true, nil
		}
	}
	return false, nil
}

// containerDiskNodeImageImport streams ref from the host engine store into the
// node's containerd. Package var (test seam).
var containerDiskNodeImageImport = func(ctx context.Context, home, alias, ref string) error {
	engine := "podman"
	if rt, rerr := kit.ResolveRuntime(); rerr == nil && rt.RunEngine != "" {
		engine = rt.RunEngine
	}
	save := exec.CommandContext(ctx, container.EngineBinary(engine), "save", ref)
	load := exec.CommandContext(ctx, "ssh", "-F", kit.SshConfigPath(home), alias,
		"sudo", "k3s", "ctr", "-n", "k8s.io", "images", "import", "-")
	if err := container.StreamLoad(save, load); err != nil {
		return fmt.Errorf("delivering %s into node %q containerd: %w", ref, alias, err)
	}
	return nil
}
