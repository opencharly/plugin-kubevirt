package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/alecthomas/kong"

	"github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt/params"
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// command.go is the command:kubevirt leg — the `charly kubevirt <verb>` CLI family
// (build/create/start/stop/restart/destroy/console/ssh/snapshot/migrate/gpu/status).
// The plugin owns the ENTIRE logic: the Kong grammar AND the KubeVirt CR operations,
// built on the same cluster_ops / methods surface the verb uses (R3 — one protocol
// surface covers the CLI and every bed).
//
// Placement: compiled-in, `charly kubevirt` dispatches IN-PROC via Invoke(OpRun)
// (runKubeVirtCommand) with the reverse channel stashed, so the handlers inherit
// charly's real stdio/TTY; out-of-process the host fork/execs cmd/serve → CliMain
// running the SAME runCLI. The entity-based subcommands (create/build/gpu) need the
// project-self-load reverse channel and therefore error clearly when it is absent
// (matching candy/plugin-vm's CliMain contract for a host-coupled command).

// commandCtx / commandExec carry the host reverse channel stashed during Invoke(OpRun)
// so the Kong handlers (which take no ctx) can reach host-side self-load seams.
var (
	commandCtx  = context.Background()
	commandExec *sdk.Executor
)

func setCommandContext(ctx context.Context, exec *sdk.Executor) {
	commandCtx = ctx
	commandExec = exec
}

// runKubeVirtCommand serves command:kubevirt's Invoke(OpRun): recover the executor,
// decode the pass-through args, and run the Kong tree.
func runKubeVirtCommand(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("kubevirt command: reach host reverse channel: %w", err)
	}
	setCommandContext(ctx, exec)
	var in struct {
		Args []string `json:"args"`
	}
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("kubevirt command: decode args: %w", err)
		}
	}
	if rerr := runCLI(in.Args); rerr != nil {
		return nil, rerr
	}
	return &pb.InvokeReply{}, nil
}

// invokeCommand handles the command class in Invoke — the in-proc OpRun path.
func invokeCommand(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("kubevirt command: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	return runKubeVirtCommand(ctx, req)
}

// CliMain is the OUT-OF-PROCESS command entry (the host fork/execs the serve binary with
// the pass-through tokens). It runs the SAME effect as the compiled-in OpRun path.
func CliMain(args []string) int {
	if err := runCLI(args); err != nil {
		fmt.Fprintf(os.Stderr, "charly kubevirt: %v\n", err)
		return 1
	}
	return 0
}

// runCLI parses the pass-through args into the KubeVirtCmd tree and runs the selected
// leaf. sdk.RunInProcCLI is the house helper (kong's default Exit would os.Exit charly).
func runCLI(args []string) error {
	var cli KubeVirtCmd
	return sdk.RunInProcCLI("kubevirt", &cli, args,
		kong.Description("Manage KubeVirt VirtualMachines on a Kubernetes cluster (create/start/stop/restart/destroy/console/ssh/snapshot/migrate/gpu/status)"),
		kong.Bind(&cli))
}

// commandModel reflects the Kong grammar into a CLIModel (errors propagate to Describe,
// never panic).
func commandModel() (*spec.CLIModel, error) {
	return sdk.BuildCLIModel(&KubeVirtCmd{}, "kubevirt", calver, "kubevirt")
}

// Globals are the targeting flags every subcommand accepts (global flags).
type Globals struct {
	Cluster     string `name:"cluster" help:"kind:kubernetes cluster template name (resolves the kube_context)"`
	KubeContext string `name:"kube-context" help:"Kubeconfig context to target"`
	Kubeconfig  string `name:"kubeconfig" help:"Kubeconfig path (default: $KUBECONFIG then ~/.kube/config)"`
	Namespace   string `name:"namespace" short:"n" help:"Kubernetes namespace (default: default)"`
}

func (g Globals) conn() *clusterConn {
	return &clusterConn{kubeconfig: g.Kubeconfig, context: g.KubeContext}
}

func (g Globals) ops() (clusterOps, error) {
	conn := g.conn()
	if g.Cluster != "" && conn.context == "" && commandExec != nil {
		in := &params.KubeVirtInput{Cluster: g.Cluster}
		resolveClusterContext(commandCtx, commandExec, in)
		conn.context = in.KubeContext
	}
	return newClusterOps(conn)
}

// KubeVirtCmd is the `charly kubevirt` command tree.
type KubeVirtCmd struct {
	Globals

	Status   StatusCmd   `cmd:"" help:"Show the live state of the VirtualMachines in the cluster"`
	Create   CreateCmd   `cmd:"" help:"Create (apply) a VirtualMachine from a kind:kubevirt entity"`
	Build    BuildCmd    `cmd:"" help:"Render the VirtualMachine CR for a kind:kubevirt entity without applying it"`
	Start    StartCmd    `cmd:"" help:"Start a VirtualMachine"`
	Stop     StopCmd     `cmd:"" help:"Stop a VirtualMachine"`
	Restart  RestartCmd  `cmd:"" help:"Restart a VirtualMachine"`
	Destroy  DestroyCmd  `cmd:"" help:"Delete a VirtualMachine"`
	Console  ConsoleCmd  `cmd:"" help:"Open the VM serial console (virtctl)"`
	SSH      SSHCmd      `cmd:"" help:"Print the managed ssh alias for a VM"`
	Snapshot SnapshotCmd `cmd:"" help:"Create a VirtualMachineSnapshot"`
	Migrate  MigrateCmd  `cmd:"" help:"Migrate a VirtualMachineInstance to another node"`
	GPU      GPUCmd      `cmd:"" help:"Show the GPU devices a kind:kubevirt entity would request"`
}

// ---------------------------------------------------------------------------
// status / create / build
// ---------------------------------------------------------------------------

type StatusCmd struct{}

func (c StatusCmd) Run(root *KubeVirtCmd) error {
	cli, err := root.ops()
	if err != nil {
		return err
	}
	return runStatus(commandCtx, cli, root.Namespace)
}

type CreateCmd struct {
	Entity string `arg:"" help:"kind:kubevirt entity name"`
}

func (c CreateCmd) Run(root *KubeVirtCmd) error {
	return runCreateApply(root, c.Entity, true)
}

type BuildCmd struct {
	Entity string `arg:"" help:"kind:kubevirt entity name"`
}

func (c BuildCmd) Run(root *KubeVirtCmd) error {
	return runCreateApply(root, c.Entity, false)
}

// ---------------------------------------------------------------------------
// start / stop / restart / destroy
// ---------------------------------------------------------------------------

type StartCmd struct {
	VM string `arg:"" help:"VirtualMachine name"`
}

func (c StartCmd) Run(root *KubeVirtCmd) error {
	cli, err := root.ops()
	if err != nil {
		return err
	}
	if err := cli.EnsureRunning(commandCtx, namespaceOrStr(root.Namespace), c.VM); err != nil {
		return err
	}
	fmt.Printf("%s/%s started\n", namespaceOrStr(root.Namespace), c.VM)
	return nil
}

type StopCmd struct {
	VM string `arg:"" help:"VirtualMachine name"`
}

func (c StopCmd) Run(root *KubeVirtCmd) error {
	cli, err := root.ops()
	if err != nil {
		return err
	}
	if err := cli.Stop(commandCtx, namespaceOrStr(root.Namespace), c.VM); err != nil {
		return err
	}
	fmt.Printf("%s/%s stopped\n", namespaceOrStr(root.Namespace), c.VM)
	return nil
}

type RestartCmd struct {
	VM string `arg:"" help:"VirtualMachine name"`
}

func (c RestartCmd) Run(root *KubeVirtCmd) error {
	in := &params.KubeVirtInput{Name: c.VM, Namespace: root.Namespace, KubeContext: root.KubeContext, Kubeconfig: root.Kubeconfig}
	if _, err := runVMState(root.conn(), nil, in, "restart"); err != nil {
		return err
	}
	fmt.Printf("%s restarted\n", c.VM)
	return nil
}

type DestroyCmd struct {
	VM string `arg:"" help:"VirtualMachine name"`
}

func (c DestroyCmd) Run(root *KubeVirtCmd) error {
	cli, err := root.ops()
	if err != nil {
		return err
	}
	if err := cli.DeleteVirtualMachine(commandCtx, namespaceOrStr(root.Namespace), c.VM); err != nil {
		return err
	}
	fmt.Printf("%s/%s destroyed\n", namespaceOrStr(root.Namespace), c.VM)
	return nil
}

// ---------------------------------------------------------------------------
// console / ssh
// ---------------------------------------------------------------------------

type ConsoleCmd struct {
	VM string `arg:"" help:"VirtualMachine name"`
}

func (c ConsoleCmd) Run(root *KubeVirtCmd) error {
	in := &params.KubeVirtInput{Name: c.VM, Namespace: root.Namespace, KubeContext: root.KubeContext, Kubeconfig: root.Kubeconfig}
	out, err := runVirtctlConsole(root.conn(), in)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

type SSHCmd struct {
	VM string `arg:"" help:"VirtualMachine name (or deploy name)"`
}

func (c SSHCmd) Run(root *KubeVirtCmd) error {
	alias := c.VM
	if !hasPrefix(alias, "charly-") {
		alias = vmNameForDeploy(alias)
	}
	fmt.Printf("ssh %s\n", alias)
	fmt.Println("(the managed alias is published by the deploy lifecycle; the port-forward is started by prepare-venue)")
	return nil
}

// ---------------------------------------------------------------------------
// snapshot / migrate / gpu
// ---------------------------------------------------------------------------

type SnapshotCmd struct {
	VM string `arg:"" help:"VirtualMachine name"`
}

func (c SnapshotCmd) Run(root *KubeVirtCmd) error {
	in := &params.KubeVirtInput{Name: c.VM, Namespace: root.Namespace, KubeContext: root.KubeContext, Kubeconfig: root.Kubeconfig}
	out, err := runSnapshot(root.conn(), nil, in)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

type MigrateCmd struct {
	VM     string `arg:"" help:"VirtualMachineInstance name"`
	ToNode string `name:"to-node" help:"Target node name (optional; the scheduler picks when empty)"`
}

func (c MigrateCmd) Run(root *KubeVirtCmd) error {
	in := &params.KubeVirtInput{Name: c.VM, Namespace: root.Namespace, KubeContext: root.KubeContext, Kubeconfig: root.Kubeconfig, ToNode: c.ToNode, WaitSeconds: 300}
	out, err := runMigration(root.conn(), nil, in)
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

type GPUCmd struct {
	Entity string `arg:"" help:"kind:kubevirt entity name"`
}

func (c GPUCmd) Run(root *KubeVirtCmd) error {
	kv, err := resolveEntityForCLI(c.Entity)
	if err != nil {
		return err
	}
	gpus := RenderVirtualMachineGPUs(*kv)
	if len(gpus) == 0 {
		fmt.Println("(no GPU declared)")
		return nil
	}
	for _, g := range gpus {
		fmt.Println(g)
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func runStatus(ctx context.Context, cli clusterOps, namespace string) error {
	ns := namespaceOrStr(namespace)
	l, ok := cli.(lister)
	if !ok {
		return fmt.Errorf("status: listing unsupported")
	}
	rows, err := l.ListVirtualMachines(ctx, ns)
	if err != nil {
		return err
	}
	sort.Strings(rows)
	for _, r := range rows {
		fmt.Println(r)
	}
	return nil
}

// lister is the optional listing surface (used by status).
type lister interface {
	ListVirtualMachines(ctx context.Context, namespace string) ([]string, error)
}

func runCreateApply(root *KubeVirtCmd, entity string, apply bool) error {
	kv, err := resolveEntityForCLI(entity)
	if err != nil {
		return err
	}
	distro, sshUser, err := resolveGuestIdentity(kv, entity)
	if err != nil {
		return err
	}
	ns := namespaceOrStr(root.Namespace)
	obj, err := RenderVirtualMachine(*kv, RenderOptions{Name: entity, Namespace: ns, Distro: distro, SSHUser: sshUser})
	if err != nil {
		return err
	}
	if !apply {
		out, err := marshalJSON(obj)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}
	cli, err := root.ops()
	if err != nil {
		return err
	}
	if dv := RenderDataVolume(entity, ns, *kv); dv != nil {
		if err := cli.EnsureDataVolume(commandCtx, ns, dv); err != nil {
			return err
		}
	}
	if err := cli.ApplyVirtualMachine(commandCtx, ns, obj); err != nil {
		return err
	}
	fmt.Printf("applied VirtualMachine %s/%s\n", ns, entity)
	return nil
}

// resolveEntityForCLI resolves a kind:kubevirt entity for a CLI subcommand, which needs
// the project-self-load reverse channel. Out-of-process CLI mode (no channel) errors
// clearly, matching candy/plugin-vm's host-coupled CliMain contract.
func resolveEntityForCLI(entity string) (*spec.KubeVirt, error) {
	if commandExec == nil {
		return nil, fmt.Errorf("resolving kind:kubevirt entity %q requires the compiled-in placement (no host reverse channel out-of-process)", entity)
	}
	dir, err := hostProjectDir(commandCtx, commandExec, "")
	if err != nil {
		return nil, err
	}
	return resolveKubeVirtEntity(commandCtx, commandExec, dir, entity)
}

func namespaceOrStr(ns string) string {
	if ns == "" {
		return "default"
	}
	return ns
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
