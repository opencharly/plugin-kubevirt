package kubevirt

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt/params"
	"github.com/opencharly/spec/spec"
)

// migrate.go — the VirtualMachineInstanceMigration surface: `charly kubevirt migrate <vm>
// [--to-node]` and the `kubevirt: migration` verb method. It creates a
// VirtualMachineInstanceMigration CR (kubevirt.io/v1) targeting the named VMI and, when
// the caller asks for a wait, polls it to a terminal phase.

// migrateVMI creates a VirtualMachineInstanceMigration for vmi and returns the created
// CR's name. toNode pins the migration target (empty → the scheduler picks).
func migrateVMI(ctx context.Context, conn *clusterConn, namespace, vmi, toNode string) (string, error) {
	client, err := conn.dynamicClient()
	if err != nil {
		return "", err
	}
	migName := vmi + "-migration"
	obj := map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachineInstanceMigration",
		"metadata":   map[string]any{"name": migName, "namespace": namespace},
		"spec":       map[string]any{"vmiName": vmi},
	}
	if toNode != "" {
		obj["spec"].(map[string]any)["nodeName"] = toNode
	}
	if _, err := client.Resource(gvrMigrations).Namespace(namespace).Create(ctx, toUnstructured(obj), metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("creating VirtualMachineInstanceMigration for %s/%s: %w", namespace, vmi, err)
	}
	return migName, nil
}

// waitMigration polls a migration CR to a terminal phase, bounded by timeout.
func waitMigration(ctx context.Context, conn *clusterConn, namespace, migName string, timeout time.Duration) error {
	client, err := conn.dynamicClient()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		u, err := client.Resource(gvrMigrations).Namespace(namespace).Get(ctx, migName, metav1.GetOptions{})
		if err == nil {
			switch nestedMapString(u.Object, "status", "phase") {
			case "Succeeded":
				return nil
			case "Failed":
				return fmt.Errorf("migration %s/%s Failed", namespace, migName)
			}
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting migration %s/%s: %w", namespace, migName, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for migration %s/%s", namespace, migName)
		}
		time.Sleep(2 * time.Second)
	}
}

// runMigration is the `kubevirt: migration` verb method.
func runMigration(conn *clusterConn, op *spec.Op, in *params.KubeVirtInput) (string, error) {
	ns := namespaceOr(in)
	migName, err := migrateVMI(context.TODO(), conn, ns, in.Name, in.ToNode)
	if err != nil {
		return "", err
	}
	// No explicit wait → fire-and-report (the created migration's name). A wait_seconds
	// or step timeout polls to a terminal phase.
	if in.WaitSeconds <= 0 && (op == nil || op.Timeout == "") {
		return fmt.Sprintf("%s/%s Pending\n", ns, migName), nil
	}
	if err := waitMigration(context.TODO(), conn, ns, migName, waitSecondsFor(op, in, 300*time.Second)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s Succeeded\n", ns, migName), nil
}
