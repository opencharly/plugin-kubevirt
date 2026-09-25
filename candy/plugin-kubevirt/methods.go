package kubevirt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// methods.go is the kubevirt verb method dispatcher: the 13-method surface over the
// KubeVirt dynamic clients, returning the captured output string (so provider.go can
// feed it through the shared sdk matcher pipeline — a host-side matcher step does not
// run for an out-of-process verb). `console` / `port-forward` shell out to virtctl (the
// only sane way to reach the guest console / a forwarded port).

// requiredModifiers mirrors the per-method required-field specs. The host's
// validate-time + runtime required-modifier check keyed off the in-proc live-verb seam,
// which an external verb is not — so the check moves HERE, at dispatch, preserving the
// "missing required modifier(s): X" failure. The names are plugin-INPUT keys
// (#KubeVirtInput wire names, plus the shared `name`/`namespace`).
var requiredModifiers = map[string][]string{
	"vm":               {"name"},
	"vmi":              {"name"},
	"wait-ready":       {"name"},
	"printable-status": {"name"},
	"guest-info":       {"name"},
	"migration":        {"name"},
	"datavolume":       {"name"},
	"snapshot":         {"name"},
	"start":            {"name"},
	"stop":             {"name"},
	"restart":          {"name"},
	"console":          {"name"},
	"port-forward":     {"name"},
}

// dispatch runs one kubevirt method against the resolved cluster and returns its
// captured output. A returned error is the verb FAILING (the in-tree CLI Run()
// returning an error → exit 1); provider.go maps it through the exit_status / stderr
// matchers.
//
//nolint:gocyclo // a flat method switch over the 13-method allowlist; splitting would scatter the contract.
func dispatch(conn *clusterConn, op *spec.Op, in *params.KubeVirtInput) (string, error) {
	method := in.Method
	if err := sdk.RequireModifiers(method, op, requiredModifiers); err != nil {
		return "", err
	}
	switch method {
	case "vm":
		return runVM(conn, op, in)
	case "vmi":
		return runVMI(conn, op, in)
	case "wait-ready":
		return runWaitReady(conn, op, in)
	case "printable-status":
		return runPrintableStatus(conn, op, in)
	case "guest-info":
		return runGuestInfo(conn, op, in)
	case "migration":
		return runMigration(conn, op, in)
	case "datavolume":
		return runDataVolume(conn, op, in)
	case "snapshot":
		return runSnapshot(conn, op, in)
	case "start":
		return runVMState(conn, op, in, "start")
	case "stop":
		return runVMState(conn, op, in, "stop")
	case "restart":
		return runVMState(conn, op, in, "restart")
	case "console":
		return runVirtctlConsole(conn, in)
	case "port-forward":
		return runVirtctlPortForward(conn, op, in)
	}
	return "", fmt.Errorf("unknown kubevirt method %q", method)
}

// namespaceOr returns the input namespace, else "default".
func namespaceOr(in *params.KubeVirtInput) string {
	if in.Namespace != "" {
		return in.Namespace
	}
	return "default"
}

// timeoutFor bounds a readiness method from the step timeout, else def.
func timeoutFor(op *spec.Op, def time.Duration) time.Duration {
	if op == nil || op.Timeout == "" {
		return def
	}
	d, err := time.ParseDuration(op.Timeout)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// waitSecondsFor resolves the wait bound: the input's wait_seconds wins, else the step
// timeout, else def.
func waitSecondsFor(op *spec.Op, in *params.KubeVirtInput, def time.Duration) time.Duration {
	if in.WaitSeconds > 0 {
		return time.Duration(in.WaitSeconds) * time.Second
	}
	return timeoutFor(op, def)
}

// ---------------------------------------------------------------------------
// vm / vmi
// ---------------------------------------------------------------------------

func runVM(conn *clusterConn, _ *spec.Op, in *params.KubeVirtInput) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := namespaceOr(in)
	u, err := client.Resource(gvrVirtualMachines).Namespace(ns).Get(context.TODO(), in.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting VirtualMachine %s/%s: %w", ns, in.Name, err)
	}
	if in.JSON {
		return marshalJSON(u.Object)
	}
	return describeTarget(u.Object) + "\n", nil
}

func runVMI(conn *clusterConn, _ *spec.Op, in *params.KubeVirtInput) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := namespaceOr(in)
	u, err := client.Resource(gvrVMIs).Namespace(ns).Get(context.TODO(), in.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting VirtualMachineInstance %s/%s: %w", ns, in.Name, err)
	}
	if in.JSON {
		return marshalJSON(u.Object)
	}
	return describeTarget(u.Object) + "\n", nil
}

// ---------------------------------------------------------------------------
// wait-ready / printable-status
// ---------------------------------------------------------------------------

// vmiReady reports whether a VMI has the Ready condition True.
func vmiReady(u map[string]any) bool {
	conds, ok := u["status"].(map[string]any)
	if !ok {
		return false
	}
	list, ok := conds["conditions"].([]any)
	if !ok {
		return false
	}
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "Ready" && m["status"] == "True" {
			return true
		}
	}
	return false
}

// vmiAgentConnected reports whether a VMI has the AgentConnected condition True.
func vmiAgentConnected(u map[string]any) bool {
	status, ok := u["status"].(map[string]any)
	if !ok {
		return false
	}
	list, ok := status["conditions"].([]any)
	if !ok {
		return false
	}
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "AgentConnected" && m["status"] == "True" {
			return true
		}
	}
	return false
}

func runWaitReady(conn *clusterConn, op *spec.Op, in *params.KubeVirtInput) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := namespaceOr(in)
	deadline := time.Now().Add(waitSecondsFor(op, in, 300*time.Second))
	for {
		u, err := client.Resource(gvrVMIs).Namespace(ns).Get(context.TODO(), in.Name, metav1.GetOptions{})
		if err == nil && vmiReady(u.Object) {
			return fmt.Sprintf("%s/%s Ready\n", ns, in.Name), nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("getting VMI %s/%s: %w", ns, in.Name, err)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for VirtualMachineInstance %s/%s Ready", ns, in.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

func runPrintableStatus(conn *clusterConn, _ *spec.Op, in *params.KubeVirtInput) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := namespaceOr(in)
	u, err := client.Resource(gvrVirtualMachines).Namespace(ns).Get(context.TODO(), in.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting VirtualMachine %s/%s: %w", ns, in.Name, err)
	}
	st := nestedString(u, "status", "printableStatus")
	if st == "" {
		st = nestedString(u, "status", "created")
	}
	if st == "" {
		st = "unknown"
	}
	return st + "\n", nil
}

// ---------------------------------------------------------------------------
// guest-info
// ---------------------------------------------------------------------------

func runGuestInfo(conn *clusterConn, op *spec.Op, in *params.KubeVirtInput) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := namespaceOr(in)
	deadline := time.Now().Add(waitSecondsFor(op, in, 300*time.Second))
	for {
		u, err := client.Resource(gvrVMIs).Namespace(ns).Get(context.TODO(), in.Name, metav1.GetOptions{})
		if err == nil && vmiAgentConnected(u.Object) {
			var b strings.Builder
			fmt.Fprintf(&b, "agent-connected=true\n")
			if id := nestedString(u, "status", "guestOSInfo", "id"); id != "" {
				fmt.Fprintf(&b, "guest-id=%s\n", id)
			}
			if name := nestedString(u, "status", "guestOSInfo", "name"); name != "" {
				fmt.Fprintf(&b, "guest-name=%s\n", name)
			}
			if ver := nestedString(u, "status", "guestOSInfo", "version"); ver != "" {
				fmt.Fprintf(&b, "guest-version=%s\n", ver)
			}
			return b.String(), nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("getting VMI %s/%s: %w", ns, in.Name, err)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for the guest agent on %s/%s (AgentConnected)", ns, in.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// migration — see migrate.go
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// datavolume
// ---------------------------------------------------------------------------

func runDataVolume(conn *clusterConn, op *spec.Op, in *params.KubeVirtInput) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := namespaceOr(in)
	deadline := time.Now().Add(waitSecondsFor(op, in, 300*time.Second))
	for {
		u, err := client.Resource(gvrDataVolumes).Namespace(ns).Get(context.TODO(), in.Name, metav1.GetOptions{})
		if err == nil {
			phase := nestedMapString(u.Object, "status", "phase")
			switch phase {
			case "Succeeded":
				return fmt.Sprintf("%s/%s Succeeded\n", ns, in.Name), nil
			case "Failed":
				return "", fmt.Errorf("DataVolume %s/%s Failed", ns, in.Name)
			}
			if in.JSON {
				return marshalJSON(u.Object)
			}
		} else if !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("getting DataVolume %s/%s: %w", ns, in.Name, err)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for DataVolume %s/%s to succeed", ns, in.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// snapshot
// ---------------------------------------------------------------------------

func runSnapshot(conn *clusterConn, op *spec.Op, in *params.KubeVirtInput) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := namespaceOr(in)
	snapName := in.Name + "-snapshot"
	specBody := map[string]any{
		"source": map[string]any{"apiGroup": "kubevirt.io", "kind": "VirtualMachine", "name": in.Name},
	}
	if in.SnapshotClass != "" {
		specBody["volumeSnapshotClassName"] = in.SnapshotClass
	}
	obj := map[string]any{
		"apiVersion": "snapshot.kubevirt.io/v1beta1",
		"kind":       "VirtualMachineSnapshot",
		"metadata": map[string]any{
			"name":      snapName,
			"namespace": ns,
		},
		"spec": specBody,
	}
	if in.Description != "" {
		obj["metadata"].(map[string]any)["annotations"] = map[string]any{"charly.io/description": in.Description}
	}
	if _, err := client.Resource(gvrSnapshots).Namespace(ns).Create(context.TODO(), toUnstructured(obj), metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("creating VirtualMachineSnapshot for %s/%s: %w", ns, in.Name, err)
	}
	deadline := time.Now().Add(waitSecondsFor(op, in, 300*time.Second))
	for {
		u, err := client.Resource(gvrSnapshots).Namespace(ns).Get(context.TODO(), snapName, metav1.GetOptions{})
		if err == nil {
			phase := nestedMapString(u.Object, "status", "phase")
			switch phase {
			case "Succeeded":
				return fmt.Sprintf("%s/%s Succeeded\n", ns, snapName), nil
			case "Failed":
				return "", fmt.Errorf("snapshot %s/%s Failed", ns, snapName)
			}
			if in.JSON {
				return marshalJSON(u.Object)
			}
		} else if !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("getting snapshot %s/%s: %w", ns, snapName, err)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for snapshot %s/%s", ns, snapName)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// start / stop / restart
// ---------------------------------------------------------------------------

// runVMState patches the VirtualMachine's runStrategy to start/stop it, or triggers a
// restart by first halting then re-running. It uses the KubeVirt `runStrategy` field
// (the supported dynamic-client path — virtctl is only needed for console/port-forward).
func runVMState(conn *clusterConn, op *spec.Op, in *params.KubeVirtInput, action string) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	ns := namespaceOr(in)
	u, err := client.Resource(gvrVirtualMachines).Namespace(ns).Get(context.TODO(), in.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting VirtualMachine %s/%s: %w", ns, in.Name, err)
	}
	switch action {
	case "stop":
		if err := setRunStrategy(u.Object, "Halted"); err != nil {
			return "", err
		}
		if _, err := client.Resource(gvrVirtualMachines).Namespace(ns).Update(context.TODO(), u, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("stopping VirtualMachine %s/%s: %w", ns, in.Name, err)
		}
		return fmt.Sprintf("%s/%s stopped\n", ns, in.Name), nil
	case "start":
		if err := setRunStrategy(u.Object, "Always"); err != nil {
			return "", err
		}
		if _, err := client.Resource(gvrVirtualMachines).Namespace(ns).Update(context.TODO(), u, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("starting VirtualMachine %s/%s: %w", ns, in.Name, err)
		}
		return fmt.Sprintf("%s/%s started\n", ns, in.Name), nil
	case "restart":
		// A restart is a stop followed by a start on the runStrategy field. Run the
		// halt update, then the run update, from the SAME latest object.
		if err := setRunStrategy(u.Object, "Halted"); err != nil {
			return "", err
		}
		halted, err := client.Resource(gvrVirtualMachines).Namespace(ns).Update(context.TODO(), u, metav1.UpdateOptions{})
		if err != nil {
			return "", fmt.Errorf("restarting VirtualMachine %s/%s (halt): %w", ns, in.Name, err)
		}
		if err := setRunStrategy(halted.Object, "Always"); err != nil {
			return "", err
		}
		if _, err := client.Resource(gvrVirtualMachines).Namespace(ns).Update(context.TODO(), halted, metav1.UpdateOptions{}); err != nil {
			return "", fmt.Errorf("restarting VirtualMachine %s/%s (run): %w", ns, in.Name, err)
		}
		return fmt.Sprintf("%s/%s restarted\n", ns, in.Name), nil
	}
	_ = op
	return "", fmt.Errorf("unknown state action %q", action)
}

// setRunStrategy writes spec.runStrategy on a VirtualMachine object map.
func setRunStrategy(obj map[string]any, strategy string) error {
	specMap, ok := obj["spec"].(map[string]any)
	if !ok {
		specMap = map[string]any{}
		obj["spec"] = specMap
	}
	specMap["runStrategy"] = strategy
	return nil
}

// ---------------------------------------------------------------------------
// console / port-forward (virtctl)
// ---------------------------------------------------------------------------

func runVirtctlConsole(conn *clusterConn, in *params.KubeVirtInput) (string, error) {
	return runVirtctl(conn, in, "console", in.Name, "--no-stdin")
}

func runVirtctlPortForward(conn *clusterConn, _ *spec.Op, in *params.KubeVirtInput) (string, error) {
	remotePort := in.RemotePort
	if remotePort <= 0 {
		remotePort = 22
	}
	localPort := in.LocalPort
	if localPort <= 0 {
		p, err := allocateLocalPort()
		if err != nil {
			return "", fmt.Errorf("allocating local port: %w", err)
		}
		localPort = p
	}
	return runVirtctl(conn, in, "port-forward", in.Name, fmt.Sprintf("--local-port=%d", localPort), fmt.Sprintf("--port=%d", remotePort))
}

// runVirtctl shells out to virtctl (the KubeVirt CLI) — the only supported way to reach
// the guest console / a forwarded port. The cluster context/kubeconfig are passed
// explicitly so it never uses the ambient-current-context by accident.
func runVirtctl(conn *clusterConn, in *params.KubeVirtInput, subcommand string, args ...string) (string, error) {
	argv := []string{subcommand}
	if conn.kubeconfig != "" {
		argv = append(argv, "--kubeconfig", conn.kubeconfig)
	}
	if conn.context != "" {
		argv = append(argv, "--context", conn.context)
	}
	if in.Namespace != "" {
		argv = append(argv, "--namespace", in.Namespace)
	}
	argv = append(argv, args...)
	cmd := exec.Command("virtctl", argv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("virtctl %s: %w", strings.Join(argv, " "), err)
	}
	return string(out), nil
}

// allocateLocalPort asks the kernel for a free TCP port on the loopback interface. It
// closes the socket immediately and returns the number — the standard auto-allocation
// (the window between close and virtctl's bind is the same every such allocation has).
func allocateLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ---------------------------------------------------------------------------
// output / map helpers
// ---------------------------------------------------------------------------

// marshalJSON renders a value as indented JSON text.
func marshalJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// toUnstructured wraps a plain object map as an *unstructured.Unstructured.
func toUnstructured(obj map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: obj}
}
