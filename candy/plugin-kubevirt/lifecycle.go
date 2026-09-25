package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/vmshared"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/shellquote"
	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/sshx"
)

// lifecycle.go — the host-side KubeVirt venue lifecycle, IMPLEMENTED in the plugin.
// The plugin runs ON the host (co-located) but out-of-process; it does the WHOLE venue
// lifecycle over GENERIC seams — sdk/kit for the ssh-config stanza + guest readiness
// waits + charly delivery, virtual-kubevirt client-go dynamic ops for the CRs, a managed
// `virtctl port-forward` for SSH reachability, and the reverse channel for guest ops.
// This is the deploy:vm lifecycle with the KubeVirt boot path substituted (lifecycle.go
// mirrors candy/plugin-deploy-vm/lifecycle.go where the venue is the same ssh guest).

// lifecycleParams are the params the host proxy ships for a kubevirt lifecycle Op. node
// is the canonical Deploy JSON; prepare is unused (the plugin self-resolves); opts is
// polymorphic, decoded per-op.
type lifecycleParams struct {
	Name      string          `json:"name"`
	Dir       string          `json:"dir"`
	Node      json.RawMessage `json:"node"`
	Opts      json.RawMessage `json:"opts"`
	KeepImage bool            `json:"keep_image"`
	Cmd       []string        `json:"cmd"`
}

// isLifecycleOp reports whether op is a substrate-lifecycle Op (vs. the OpExecute walk /
// OpPreresolve).
func isLifecycleOp(op string) bool {
	switch op {
	case sdk.OpPrepareVenue, sdk.OpArtifactKey, sdk.OpPostApply, sdk.OpTeardownExecutor,
		sdk.OpPostTeardown, sdk.OpStart, sdk.OpStop, sdk.OpStatus, sdk.OpLogs, sdk.OpShell,
		sdk.OpAttach, sdk.OpRebuild:
		return true
	}
	return false
}

// invokeLifecycle handles a kubevirt substrate-lifecycle Op over the reverse channel.
func invokeLifecycle(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt %s: executor: %w", req.GetOp(), err)
	}
	var p lifecycleParams
	if err := json.Unmarshal(req.GetParamsJson(), &p); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt %s: decode params: %w", req.GetOp(), err)
	}
	var host spec.HostEnv
	if err := json.Unmarshal(req.GetEnvJson(), &host); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt %s: decode host env: %w", req.GetOp(), err)
	}

	switch req.GetOp() {
	case sdk.OpPrepareVenue:
		return kvPrepareVenue(ctx, exec, p, host)
	case sdk.OpPostApply:
		return kvPostApply(ctx, exec, p, host)
	case sdk.OpArtifactKey:
		return marshalReply(map[string]string{"key": "kubevirt:" + vmNameForDeploy(p.Name), "entity": kvEntity(p)})
	case sdk.OpTeardownExecutor:
		return marshalReply(spec.VenueDescriptor{Kind: "ssh", Host: kit.VmSshAlias(sshAlias(p)), ConnectTimeout: 10})
	case sdk.OpPostTeardown:
		return kvPostTeardown(ctx, exec, p, host)
	case sdk.OpStart:
		return kvState(ctx, p, true)
	case sdk.OpStop:
		return kvState(ctx, p, false)
	case sdk.OpStatus:
		return kvStatus(ctx, p)
	case sdk.OpLogs:
		return marshalReply(struct{}{})
	case sdk.OpShell, sdk.OpAttach:
		return kvAttach(ctx, exec, p)
	case sdk.OpRebuild:
		return kvRebuild(ctx, exec, p, host)
	}
	return nil, fmt.Errorf("plugin-kubevirt: unhandled lifecycle op %q", req.GetOp())
}

// vmNameForDeploy is the per-deploy VirtualMachine CR name — the SANITIZED deploy name
// (the DOMAIN identity, not the shared entity), so sibling beds on one entity get
// distinct CRs.
func vmNameForDeploy(name string) string {
	return "charly-" + spec.VmDomainIdentity(name)
}

// sshAlias is the managed ssh-config alias for a deploy (the CR name).
func sshAlias(p lifecycleParams) string {
	return vmNameForDeploy(p.Name)
}

// kvEntity resolves the kind:kubevirt entity from the shipped node: node.From (the
// `kubevirt:` cross-ref) wins, else a legacy "kubevirt:<name>" prefix, else the deploy
// name.
func kvEntity(p lifecycleParams) string {
	var node spec.Deploy
	_ = json.Unmarshal(p.Node, &node)
	if node.From != "" {
		return string(node.From)
	}
	if strings.HasPrefix(p.Name, "kubevirt:") {
		return strings.TrimPrefix(strings.SplitN(p.Name, "/", 2)[0], "kubevirt:")
	}
	return p.Name
}

// kubevirtStateBase resolves the root directory for per-deploy host state.
func kubevirtStateBase(hostHome string) string {
	if raw := strings.TrimSpace(os.Getenv(vmshared.VmStateDirEnv)); raw != "" && filepath.IsAbs(raw) {
		return raw
	}
	return filepath.Join(hostHome, ".local", "share", "charly", "kubevirt")
}

// kvPrepareVenue runs the FULL preflight: resolve the entity + cluster, ensure the
// DataVolume, apply the VirtualMachine CR, ensure it runs, wait VMI Ready +
// AgentConnected, start the managed port-forward, publish the ssh stanza, wait for
// sshd + cloud-init, ensure charly is in the guest, and return the guest SSH venue
// descriptor + the KubeVirtDeployState patch.
func kvPrepareVenue(ctx context.Context, exec *sdk.Executor, p lifecycleParams, host spec.HostEnv) (*pb.InvokeReply, error) {
	var node spec.Deploy
	if err := json.Unmarshal(p.Node, &node); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: decode node: %w", err)
	}
	entity := kvEntity(p)
	vmName := vmNameForDeploy(p.Name)

	kv, err := resolveKubeVirtEntity(ctx, exec, p.Dir, entity)
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: resolve entity %q: %w", entity, err)
	}
	kubeContext, err := resolveKubeContext(ctx, exec, p.Dir, kv)
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: resolve cluster: %w", err)
	}
	namespace := kv.Namespace
	if namespace == "" {
		namespace = "default"
	}

	var opts spec.LifecycleOpts
	if len(p.Opts) > 0 {
		if err := json.Unmarshal(p.Opts, &opts); err != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: decode opts: %w", err)
		}
	}

	// Resolve the guest distro + ssh user from the boot medium's box metadata (the
	// runtime properties #KubeVirt does not carry — see render.go's SPEC GAP note).
	distro, sshUser, err := resolveGuestIdentity(kv, vmName)
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: %w", err)
	}

	stateDir := filepath.Join(kubevirtStateBase(host.Home), vmName)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: create state dir: %w", err)
	}
	sshKeyPath := filepath.Join(stateDir, "id_ed25519")
	sshPubKey, err := sshx.GenerateSSHKeypair(stateDir)
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: generate ssh keypair: %w", err)
	}
	_ = sshPubKey

	prior := resolvePriorKubeVirtState(ctx, exec, p.Name)

	port := 0
	if prior != nil {
		port = prior.SSHPort
	}
	if port == 0 {
		alloc, aerr := allocateLocalPort()
		if aerr != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: allocate port-forward port: %w", aerr)
		}
		port = alloc
	}

	conn := &clusterConn{context: kubeContext}
	cli, err := newClusterOps(conn)
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: %w", err)
	}

	// Render the CR and apply it. A data_volume / clone source is owned by the CR's own
	// `dataVolumeTemplates` (KubeVirt creates + deletes the DataVolume WITH the VM), so
	// no separate DataVolume apply happens here — one owner, one lifecycle. The
	// standalone RenderDataVolume is used only by the explicit verb/CLI paths.
	vmObj, err := RenderVirtualMachine(*kv, RenderOptions{
		Name:              vmName,
		Namespace:         namespace,
		Distro:            distro,
		SSHUser:           sshUser,
		SSHKey:            sshPubKey,
		RequiresExclusive: node.RequiredExclusive(),
	})
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: render VirtualMachine: %w", err)
	}
	if err := cli.ApplyVirtualMachine(ctx, namespace, vmObj); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: apply VirtualMachine: %w", err)
	}
	if !opts.DryRun {
		if err := cli.EnsureRunning(ctx, namespace, vmName); err != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: ensure running: %w", err)
		}
		if err := cli.WaitVMIReady(ctx, namespace, vmName, 10*time.Minute); err != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: %w", err)
		}
		if err := cli.WaitAgentConnected(ctx, namespace, vmName, 5*time.Minute); err != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: %w", err)
		}
	}

	// Managed port-forward on the auto-allocated local port. Started DETACHED (setsid +
	// pidfile under the deploy's state dir) so it survives this plugin subprocess's
	// lifetime; PostTeardown stops it by pidfile. Idempotent: a live forward on the
	// persisted port is reused.
	pf, err := newPortForwarder(ctx, kubeContext, namespace, vmName, port, stateDir)
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: start port-forward: %w", err)
	}
	_ = pf

	// Publish the managed ssh stanza + Include.
	if err := kit.WriteVmSshStanza(host.Home, kit.VmSshStanza{
		Alias:        kit.VmSshAlias(sshAlias(p)),
		Hostname:     "127.0.0.1",
		Port:         port,
		User:         sshUser,
		IdentityFile: sshKeyPath,
	}); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: publish ssh-config stanza: %w", err)
	}
	if err := kit.EnsureSshConfigInclude(host.Home); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt prepare-venue: ensure ssh-config include: %w", err)
	}

	ssh := kit.SSHArgs{Host: kit.VmSshAlias(sshAlias(p)), ConnectTimeout: 10}
	rr, _ := vmshared.ResolveReadiness(nil)
	poll := func(label string) kit.PollFunc {
		return func(pctx context.Context, cond vmshared.PollCondition) error {
			return vmshared.PollUntil(pctx, rr.WaitCapped(label, vmshared.PollRemote, 0), cond)
		}
	}
	var notes []string
	if !opts.DryRun {
		if err := kit.WaitForSSH(ctx, ssh, poll("ssh-ready")); err != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: wait-for-sshd: %w", err)
		}
		if err := kit.WaitForCloudInit(ctx, ssh, poll("cloud-init")); err != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: wait-for-cloud-init: %w", err)
		}
		if err := kit.WaitForPackageLock(ctx, ssh, poll("pkg-lock")); err != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: wait-for-package-lock: %w", err)
		}
		msg, err := kit.EnsureCharlyInGuest(ctx, ssh, host.CharlyBin, host.Version, charlyInstallStrategy(kv))
		if err != nil {
			return nil, fmt.Errorf("plugin-kubevirt prepare-venue: ensure charly in guest: %w", err)
		}
		notes = append(notes, msg)
	}

	state := spec.KubeVirtDeployState{
		Cluster:     kv.Cluster,
		KubeContext: kubeContext,
		Namespace:   namespace,
		VMName:      vmName,
		BootRef:     bootRef(kv),
		BootKind:    kv.Source.Kind,
		SSHPort:     port,
		SSHUser:     sshUser,
	}
	// SPEC/SDK GAP (reported, deliberately NOT worked around): the generic
	// PrepareVenue State patch is decoded host-side into spec.SaveDeployStateInput,
	// which carries NO KubeVirtState field — and deploykit.applyDeployState never
	// writes DeployNode.KubeVirtState. Shipping the state here would therefore be a
	// SILENT no-op (the exact phantom-persistence class the rulebook rejects), so the
	// plugin ships NO State patch (the same decision candy/plugin-deploy-vm's RCA #6
	// took) and recomputes the venue identity deterministically instead. The write
	// lands when spec's SaveDeployStateInput grows a kubevirt_state field; this
	// struct is ready to carry it.
	_ = state
	return marshalReply(spec.PrepareVenueReply{
		Venue: spec.VenueDescriptor{Kind: "ssh", Host: kit.VmSshAlias(sshAlias(p)), ConnectTimeout: 10},
		Notes: notes,
	})
}

// bootRef resolves the boot medium ref for the state record.
func bootRef(kv *spec.KubeVirt) string {
	switch kv.Source.Kind {
	case "container_disk":
		return kv.Source.Image
	case "data_volume", "clone":
		return ""
	case "pvc":
		return kv.Source.PVC
	}
	return ""
}

// charlyInstallStrategy extracts cloud_init.charly_install.strategy ("" → auto).
func charlyInstallStrategy(kv *spec.KubeVirt) string {
	if kv != nil && kv.CloudInit != nil && kv.CloudInit.CharlyInstall != nil {
		return kv.CloudInit.CharlyInstall.Strategy
	}
	return ""
}

// resolveGuestIdentity resolves the guest distro + ssh user for the entity. For a
// containerDisk the distro comes from the VM box's OCI metadata
// (deploykit.VmCapabilitiesFromLabels); the ssh user from the box metadata's SSHUser.
// Other source kinds must carry their own cloud_init and are rejected when neither the
// box metadata nor an explicit value resolves.
func resolveGuestIdentity(kv *spec.KubeVirt, _ string) (distro, sshUser string, err error) {
	if kv.Source.Kind == "container_disk" {
		meta, merr := resolveVmBoxMetadata(kv.Source.Image)
		if merr == nil && meta != nil {
			return meta.Distro, meta.SSHUser, nil
		}
	}
	// Non-containerDisk, or unreadable box metadata: require an explicit cloud_init.
	if kv.CloudInit == nil {
		return "", "", fmt.Errorf("the kubevirt source %q has no VM-box metadata; a deploy needs a containerDisk VM box (distro + ssh user) or an explicit cloud_init", kv.Source.Kind)
	}
	return "", "", fmt.Errorf("the kubevirt source %q needs an explicit guest distro + ssh user (not yet carried by #KubeVirt); use a containerDisk VM box", kv.Source.Kind)
}

// --- injectable seams (test stubs) -------------------------------------------

// resolveVmBoxMetadata reads a VM box's OCI metadata (distro/ssh user). Package var so
// tests stub it instead of needing a live podman.
var resolveVmBoxMetadata = func(imageRef string) (*spec.VmBoxMetadata, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, err
	}
	return deploykit.VmCapabilitiesFromLabels(rt.RunEngine, imageRef)
}

// newClusterOps builds the live cluster CR client. Package var (test seam).
var newClusterOps = func(conn *clusterConn) (clusterOps, error) { return newDynamicCluster(conn) }

// newPortForwarder starts a managed `virtctl port-forward`. Package var (test seam).
var newPortForwarder = func(ctx context.Context, kubeContext, namespace, vmName string, localPort int, stateDir string) (portForwarder, error) {
	return startVirtctlPortForward(ctx, kubeContext, namespace, vmName, localPort, stateDir)
}

// resolvePriorKubeVirtState reads a deploy's persisted KubeVirtDeployState.
var resolvePriorKubeVirtState = func(ctx context.Context, exec *sdk.Executor, deployName string) *spec.KubeVirtDeployState {
	return loadPriorKubeVirtState(ctx, exec, deployName)
}

// portForwarder is the managed `virtctl port-forward` handle.
type portForwarder interface {
	Stop() error
}

// startVirtctlPortForward spawns a DETACHED `virtctl port-forward` bound to the deploy's
// state dir (setsid; pidfile `port-forward.pid`; log `port-forward.log`). Detaching
// matters: the plugin runs as a host subprocess per Invoke and exits after PrepareVenue,
// so a child of the plugin process would be reaped when it returns. Idempotent: when the
// pidfile names a live process, it is reused rather than a second forward started.
func startVirtctlPortForward(ctx context.Context, kubeContext, namespace, vmName string, localPort int, stateDir string) (portForwarder, error) {
	if stateDir == "" {
		return nil, fmt.Errorf("port-forward: no state dir")
	}
	pidFile := filepath.Join(stateDir, "port-forward.pid")
	if pid, ok := readLivePid(pidFile); ok {
		return &virtctlPortForward{pid: pid, pidFile: pidFile}, nil
	}
	argv := []string{"port-forward"}
	if kubeContext != "" {
		argv = append(argv, "--context", kubeContext)
	}
	if namespace != "" {
		argv = append(argv, "--namespace", namespace)
	}
	argv = append(argv, vmName, "--local-port", strconv.Itoa(localPort), "--port", "22")
	logFile := filepath.Join(stateDir, "port-forward.log")
	// setsid detaches into a new session so it outlives the plugin subprocess.
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, shellquote.ShellQuote(a))
	}
	script := fmt.Sprintf("setsid virtctl %s >>%s 2>&1 & echo $! >%s",
		strings.Join(quoted, " "), shellquote.ShellQuote(logFile), shellquote.ShellQuote(pidFile))
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	pid, ok := readLivePid(pidFile)
	if !ok {
		return nil, fmt.Errorf("port-forward: process did not start (no live pid in %s)", pidFile)
	}
	return &virtctlPortForward{pid: pid, pidFile: pidFile}, nil
}

// readLivePid reads a pidfile and reports whether it names a live process.
func readLivePid(pidFile string) (int, bool) {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return 0, false
	}
	return pid, true
}

type virtctlPortForward struct {
	pid     int
	pidFile string
}

func (p *virtctlPortForward) Stop() error {
	if p.pid > 0 {
		_ = syscall.Kill(p.pid, syscall.SIGTERM)
	}
	if p.pidFile != "" {
		_ = os.Remove(p.pidFile)
	}
	return nil
}

// kvState starts/stops the VM via its runStrategy.
func kvState(ctx context.Context, p lifecycleParams, start bool) (*pb.InvokeReply, error) {
	state := resolvePriorKubeVirtState(ctx, nil, p.Name)
	conn := &clusterConn{}
	if state != nil {
		conn.context = state.KubeContext
	}
	cli, err := newClusterOps(conn)
	if err != nil {
		return nil, err
	}
	ns := "default"
	vm := vmNameForDeploy(p.Name)
	if state != nil && state.Namespace != "" {
		ns = state.Namespace
	}
	if start {
		err = cli.EnsureRunning(ctx, ns, vm)
	} else {
		err = cli.Stop(ctx, ns, vm)
	}
	if err != nil {
		return nil, err
	}
	return marshalReply(struct{}{})
}

// kvStatus reports the VM's live state.
func kvStatus(ctx context.Context, p lifecycleParams) (*pb.InvokeReply, error) {
	state := resolvePriorKubeVirtState(ctx, nil, p.Name)
	conn := &clusterConn{}
	ns := "default"
	vm := vmNameForDeploy(p.Name)
	if state != nil {
		conn.context = state.KubeContext
		if state.Namespace != "" {
			ns = state.Namespace
		}
	}
	cli, err := newClusterOps(conn)
	if err != nil {
		return marshalReply(map[string]any{"State": "unknown"})
	}
	st, healthy, serr := cli.Status(ctx, ns, vm)
	if serr != nil {
		return marshalReply(map[string]any{"State": "unknown", "Healthy": false})
	}
	return marshalReply(map[string]any{"State": st, "Healthy": healthy})
}

// kvAttach runs the interactive session IN the guest over the served executor.
func kvAttach(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	exit, err := exec.RunInteractive(ctx, strings.Join(p.Cmd, " "))
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt attach: %w", err)
	}
	return marshalReply(spec.PodExecReply{ExitCode: exit})
}

// kvPostApply deploys each nested target:pod child as a persistent in-guest quadlet —
// the SAME three-seam interleave the vm substrate uses.
func kvPostApply(ctx context.Context, exec *sdk.Executor, p lifecycleParams, host spec.HostEnv) (*pb.InvokeReply, error) {
	var node spec.Deploy
	if err := json.Unmarshal(p.Node, &node); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt post-apply: decode node: %w", err)
	}
	if !node.HasMembers() {
		return marshalReply(struct{}{})
	}
	domain := sshAlias(p)
	charlyCmd := "/tmp/charly-" + host.Version
	content, err := os.ReadFile(host.CharlyBin)
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt post-apply: read host charly %s: %w", host.CharlyBin, err)
	}
	if err := exec.PutFile(ctx, charlyCmd, content, 0o755, false); err != nil {
		return nil, fmt.Errorf("plugin-kubevirt post-apply: deliver host charly into guest: %w", err)
	}
	for _, child := range inGuestPodMembers(&node) {
		asRef := "localhost/charly-" + child.Name + ":latest"
		if err := cliBoxBuild(ctx, exec, child.Node.Image); err != nil {
			return nil, fmt.Errorf("build nested image %s (%s): %w", child.Name, child.Node.Image, err)
		}
		if err := cliCpBox(ctx, exec, domain, child.Node.Image, asRef); err != nil {
			return nil, fmt.Errorf("cp-box nested %s -> guest: %w", child.Name, err)
		}
		script := fmt.Sprintf(
			"sudo loginctl enable-linger \"$(id -un)\" >/dev/null 2>&1 || true\n"+
				"export XDG_RUNTIME_DIR=\"/run/user/$(id -u)\"\n"+
				"%s deploy from-box %s %s",
			charlyCmd, asRef, child.Name)
		if err := exec.RunUser(ctx, script, nil); err != nil {
			return nil, fmt.Errorf("deploy nested pod %s in guest: %w", child.Name, err)
		}
	}
	return marshalReply(struct{}{})
}

// inGuestPodMembers filters the node's IN-SUBSTRATE members down to nested target:pod
// children kvPostApply deploys as in-guest quadlets.
func inGuestPodMembers(node *spec.Deploy) []*spec.Member {
	var out []*spec.Member
	for _, m := range node.InSubstrateMembers() {
		if m.Node == nil || m.Node.Image == "" {
			continue
		}
		switch m.Node.Target {
		case "", "pod", "container":
		default:
			continue
		}
		out = append(out, m)
	}
	return out
}

// cliBoxBuild asks the host to run `charly box build <image>` over the cli seam.
func cliBoxBuild(ctx context.Context, exec *sdk.Executor, image string) error {
	_, err := cliCall(ctx, exec, "box", "build", image)
	return err
}

// cliCpBox asks the host to run `charly vm cp-box`; the KubeVirt guest is reached over
// the managed ssh alias, and the image lands in the guest's rootless podman store.
func cliCpBox(ctx context.Context, exec *sdk.Executor, domain, image, asRef string) error {
	_, err := cliCall(ctx, exec, "vm", "cp-box", domain, image, "--as", asRef, "--rootless")
	return err
}

// cliCall runs a `charly <argv>` subcommand on the HOST via the generic "cli"
// host-builder.
func cliCall(ctx context.Context, exec *sdk.Executor, argv ...string) (spec.CliReply, error) {
	reqJSON, err := json.Marshal(spec.CliRequest{Argv: argv})
	if err != nil {
		return spec.CliReply{}, err
	}
	resJSON, err := exec.HostBuild(ctx, "cli", reqJSON)
	if err != nil {
		return spec.CliReply{}, err
	}
	var r spec.CliReply
	if uerr := json.Unmarshal(resJSON, &r); uerr != nil {
		return spec.CliReply{}, uerr
	}
	if r.Error != "" {
		return r, fmt.Errorf("charly %s: %s", strings.Join(argv, " "), r.Error)
	}
	return r, nil
}

// kvRebuild destroys the VM CR, re-ensures the DataVolume, recreates + starts the CR,
// then re-applies the deploy's candies via `charly deploy add <name>` — the path
// `charly update <kubevirt-bed>` routes through (the disposable bed's fresh-rebuild R10
// gate).
func kvRebuild(ctx context.Context, exec *sdk.Executor, p lifecycleParams, host spec.HostEnv) (*pb.InvokeReply, error) {
	var ropts spec.DeployTargetRebuildOpts
	if len(p.Opts) > 0 {
		if err := json.Unmarshal(p.Opts, &ropts); err != nil {
			return nil, fmt.Errorf("plugin-kubevirt rebuild: decode opts: %w", err)
		}
	}
	if ropts.DryRun {
		return marshalReply(struct{}{})
	}
	state := resolvePriorKubeVirtState(ctx, exec, p.Name)
	ns := "default"
	conn := &clusterConn{}
	if state != nil {
		conn.context = state.KubeContext
		if state.Namespace != "" {
			ns = state.Namespace
		}
	}
	cli, err := newClusterOps(conn)
	if err != nil {
		return nil, err
	}
	vm := vmNameForDeploy(p.Name)
	_ = cli.DeleteVirtualMachine(ctx, ns, vm)
	if _, err := cliCall(ctx, exec, "deploy", "add", p.Name); err != nil {
		return nil, err
	}
	// Re-run the FULL preflight so the recreated VM is booted + reachable again.
	return kvPrepareVenue(ctx, exec, p, host)
}

// kvPostTeardown deletes the VirtualMachine CR, stops the managed port-forward, removes
// the managed ssh-config stanza, and ships the charly.yml entry keys for the host to
// remove.
func kvPostTeardown(ctx context.Context, exec *sdk.Executor, p lifecycleParams, host spec.HostEnv) (*pb.InvokeReply, error) {
	state := resolvePriorKubeVirtState(ctx, exec, p.Name)
	ns := "default"
	conn := &clusterConn{}
	if state != nil {
		conn.context = state.KubeContext
		if state.Namespace != "" {
			ns = state.Namespace
		}
	}
	vm := vmNameForDeploy(p.Name)
	if cli, err := newClusterOps(conn); err == nil {
		_ = cli.DeleteVirtualMachine(ctx, ns, vm)
	}
	// Stop the managed port-forward (by pidfile under this deploy's state dir).
	stopPortForwardByPidfile(filepath.Join(kubevirtStateBase(host.Home), vm))
	if remaining, err := kit.RemoveVmSshStanza(host.Home, kit.VmSshAlias(sshAlias(p))); err != nil {
		fmt.Fprintf(os.Stderr, "note: ssh-config stanza cleanup: %v\n", err)
	} else if remaining == 0 {
		if err := kit.RemoveSshConfigInclude(host.Home); err != nil {
			fmt.Fprintf(os.Stderr, "note: ssh-config include cleanup: %v\n", err)
		}
	}
	entries := []string{p.Name}
	return marshalReply(spec.PostTeardownReply{RemoveEntries: entries})
}

// stopPortForwardByPidfile terminates the detached `virtctl port-forward` recorded in
// stateDir/port-forward.pid, then removes the pidfile. Best-effort.
func stopPortForwardByPidfile(stateDir string) {
	pidFile := filepath.Join(stateDir, "port-forward.pid")
	if pid, ok := readLivePid(pidFile); ok {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	_ = os.Remove(pidFile)
}

// marshalReply marshals v into a *pb.InvokeReply.ResultJson.
func marshalReply(v any) (*pb.InvokeReply, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: b}, nil
}
