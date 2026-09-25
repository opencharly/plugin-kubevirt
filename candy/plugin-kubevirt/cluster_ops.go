package kubevirt

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// cluster_ops.go — the live CR operations the venue lifecycle drives: apply the
// VirtualMachine + DataVolume, ensure-running, wait VMI Ready / AgentConnected, status,
// delete. Implemented over the dynamic client (R3 — the SAME client the verb methods
// use); the lifecycle talks to this narrow interface so it is unit-testable with a stub.

// clusterOps is the narrow CR surface the lifecycle needs.
type clusterOps interface {
	ApplyVirtualMachine(ctx context.Context, namespace string, obj map[string]any) error
	EnsureDataVolume(ctx context.Context, namespace string, obj map[string]any) error
	EnsureRunning(ctx context.Context, namespace, vmName string) error
	Stop(ctx context.Context, namespace, vmName string) error
	WaitVMIReady(ctx context.Context, namespace, vmName string, timeout time.Duration) error
	WaitAgentConnected(ctx context.Context, namespace, vmName string, timeout time.Duration) error
	Status(ctx context.Context, namespace, vmName string) (string, bool, error)
	DeleteVirtualMachine(ctx context.Context, namespace, vmName string) error
}

// dynamicCluster is the client-go-backed clusterOps.
type dynamicCluster struct {
	dyn dynamic.Interface
}

// newDynamicCluster builds a dynamicCluster from the resolved connection.
func newDynamicCluster(conn *clusterConn) (clusterOps, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return nil, err
	}
	return &dynamicCluster{dyn: client}, nil
}

// ApplyVirtualMachine creates or updates the VirtualMachine CR (idempotent apply).
func (c *dynamicCluster) ApplyVirtualMachine(ctx context.Context, namespace string, obj map[string]any) error {
	return applyObject(ctx, c.dyn, gvrVirtualMachines, namespace, obj)
}

// EnsureDataVolume creates the DataVolume CR when absent (idempotent).
func (c *dynamicCluster) EnsureDataVolume(ctx context.Context, namespace string, obj map[string]any) error {
	name := nestedMapString(obj, "metadata", "name")
	_, err := c.dyn.Resource(gvrDataVolumes).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = c.dyn.Resource(gvrDataVolumes).Namespace(namespace).Create(ctx, toUnstructured(obj), metav1.CreateOptions{})
	return err
}

// EnsureRunning sets spec.runStrategy=Always on the VirtualMachine.
func (c *dynamicCluster) EnsureRunning(ctx context.Context, namespace, vmName string) error {
	return c.updateRunStrategy(ctx, namespace, vmName, "Always")
}

// Stop sets spec.runStrategy=Halted on the VirtualMachine.
func (c *dynamicCluster) Stop(ctx context.Context, namespace, vmName string) error {
	return c.updateRunStrategy(ctx, namespace, vmName, "Halted")
}

func (c *dynamicCluster) updateRunStrategy(ctx context.Context, namespace, vmName, strategy string) error {
	u, err := c.dyn.Resource(gvrVirtualMachines).Namespace(namespace).Get(ctx, vmName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if err := setRunStrategy(u.Object, strategy); err != nil {
		return err
	}
	_, err = c.dyn.Resource(gvrVirtualMachines).Namespace(namespace).Update(ctx, u, metav1.UpdateOptions{})
	return err
}

// WaitVMIReady polls the VirtualMachineInstance's Ready condition.
func (c *dynamicCluster) WaitVMIReady(ctx context.Context, namespace, vmName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		u, err := c.dyn.Resource(gvrVMIs).Namespace(namespace).Get(ctx, vmName, metav1.GetOptions{})
		if err == nil && vmiReady(u.Object) {
			return nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting VMI %s/%s: %w", namespace, vmName, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for VirtualMachineInstance %s/%s Ready", namespace, vmName)
		}
		time.Sleep(2 * time.Second)
	}
}

// WaitAgentConnected polls the VirtualMachineInstance's AgentConnected condition.
func (c *dynamicCluster) WaitAgentConnected(ctx context.Context, namespace, vmName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		u, err := c.dyn.Resource(gvrVMIs).Namespace(namespace).Get(ctx, vmName, metav1.GetOptions{})
		if err == nil && vmiAgentConnected(u.Object) {
			return nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting VMI %s/%s: %w", namespace, vmName, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for the guest agent on %s/%s (AgentConnected)", namespace, vmName)
		}
		time.Sleep(2 * time.Second)
	}
}

// Status reports the VM's printable status + a healthy bool.
func (c *dynamicCluster) Status(ctx context.Context, namespace, vmName string) (string, bool, error) {
	u, err := c.dyn.Resource(gvrVirtualMachines).Namespace(namespace).Get(ctx, vmName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "absent", false, nil
		}
		return "unknown", false, err
	}
	st := nestedString(u, "status", "printableStatus")
	return st, st == "Running", nil
}

// DeleteVirtualMachine deletes the VirtualMachine CR (idempotent: a missing CR is fine).
func (c *dynamicCluster) DeleteVirtualMachine(ctx context.Context, namespace, vmName string) error {
	err := c.dyn.Resource(gvrVirtualMachines).Namespace(namespace).Delete(ctx, vmName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// ListVirtualMachines returns one `name state` row per VirtualMachine in the namespace.
func (c *dynamicCluster) ListVirtualMachines(ctx context.Context, namespace string) ([]string, error) {
	list, err := c.dyn.Resource(gvrVirtualMachines).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var rows []string
	for i := range list.Items {
		u := &list.Items[i]
		st := nestedString(u, "status", "printableStatus")
		if st == "" {
			st = "unknown"
		}
		rows = append(rows, fmt.Sprintf("%s %s", u.GetName(), st))
	}
	return rows, nil
}

// applyObject creates the object, or updates it when it already exists (merge by
// resourceVersion — the same idempotent apply shape the kubernetes deploy uses).
func applyObject(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace string, obj map[string]any) error {
	name := nestedMapString(obj, "metadata", "name")
	iface := dyn.Resource(gvr).Namespace(namespace)
	existing, err := iface.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = iface.Create(ctx, toUnstructured(obj), metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	u := toUnstructured(obj)
	u.SetResourceVersion(existing.GetResourceVersion())
	_, err = iface.Update(ctx, u, metav1.UpdateOptions{})
	return err
}

// loadPriorKubeVirtState reads a deploy's persisted KubeVirtDeployState from the per-host
// overlay PLUGIN-SIDE.
//
// SPEC/SDK GAP (reported, not worked around): spec.SaveDeployStateInput carries no
// KubeVirtState field and deploykit.applyDeployState never writes DeployNode.KubeVirtState
// — so the generic PrepareVenue State patch cannot persist the KubeVirt state today, and
// this read returns whatever a future writer leaves. The read is correct + harmless: a
// miss degrades to nil, and the port/CR identity is recomputed deterministically.
func loadPriorKubeVirtState(ctx context.Context, exec *sdk.Executor, deployName string) *spec.KubeVirtDeployState {
	if exec == nil {
		return nil
	}
	dc, err := loaderkit.LoadHostDeployConfigViaExecutor(ctx, exec)
	if err != nil || dc == nil {
		return nil
	}
	boxKey, instKey := spec.ParseDeployKey(deployName)
	if node, ok := dc.Deploy[spec.DeployKey(boxKey, instKey)]; ok {
		return node.KubeVirtState
	}
	return nil
}
