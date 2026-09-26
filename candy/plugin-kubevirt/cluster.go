package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// cluster.go holds the cluster connection + the KubeVirt GVR table. This is where the
// heavy k8s.io/client-go + k8s.io/apimachinery dependency lives — entirely out of
// charly's core go.mod. clusterConn + restConfig mirror candy/plugin-kube's proven
// resolution (R3: the SAME kubeconfig/context precedence).

// clusterConn carries the resolved cluster-selection inputs the plugin builds a
// rest.Config from. A `cluster: <profile>` is resolved to a concrete kube_context by
// resolveClusterContext BEFORE the connection is built, so this struct only ever sees a
// kubeconfig path + context.
type clusterConn struct {
	kubeconfig string // input kubeconfig — host path (empty → $KUBECONFIG then ~/.kube/config)
	context    string // input kube_context — kubeconfig context (empty → current-context)
}

func connFromInput(in *params.KubeVirtInput) *clusterConn {
	return &clusterConn{kubeconfig: in.Kubeconfig, context: in.KubeContext}
}

// restConfig resolves the cluster connection to a rest.Config. Resolution:
//  1. kubeconfig path: the input's kubeconfig, else $KUBECONFIG, else ~/.kube/config.
//  2. context: the input's kube_context, else the kubeconfig current-context.
//
// The resolved context is validated against the loaded kubeconfig BEFORE handing off to
// the deferred client, so an empty or STALE context fails fast with an actionable
// message instead of a cryptic dial / TLS error at the first API call.
func (c *clusterConn) restConfig() (*rest.Config, error) {
	ctxName := c.context

	kubeconfigPath := c.kubeconfig
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}
	if kubeconfigPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}

	raw, err := loadingRules.Load()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	if ctxName == "" {
		ctxName = raw.CurrentContext
	}
	if ctxName == "" {
		return nil, fmt.Errorf("no kubeconfig context selected (no cluster/context resolved, and the kubeconfig has no current-context); set cluster: <name> or kube_context: <ctx>")
	}
	if _, ok := raw.Contexts[ctxName]; !ok {
		known := make([]string, 0, len(raw.Contexts))
		for name := range raw.Contexts {
			known = append(known, name)
		}
		sort.Strings(known)
		avail := "none"
		if len(known) > 0 {
			avail = strings.Join(known, ", ")
		}
		return nil, fmt.Errorf("kubeconfig context %q does not exist (known: %s); set cluster: <name> or kube_context: <ctx> to select a valid one", ctxName, avail)
	}

	overrides := &clientcmd.ConfigOverrides{CurrentContext: ctxName}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
}

func (c *clusterConn) dynamicClient() (dynamic.Interface, error) {
	cfg, err := c.restConfig()
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(cfg)
}

// Canonical KubeVirt-family GVRs. Listed here so adding a resource kind to probe is a
// one-line addition and the dynamic-client calls stay legible.
var (
	gvrVirtualMachines = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachines"}
	gvrVMIs            = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachineinstances"}
	gvrMigrations      = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachineinstancemigrations"}
	gvrSnapshots       = schema.GroupVersionResource{Group: "snapshot.kubevirt.io", Version: "v1beta1", Resource: "virtualmachinesnapshots"}
	gvrDataVolumes     = schema.GroupVersionResource{Group: "cdi.kubevirt.io", Version: "v1beta1", Resource: "datavolumes"}
	gvrInstancetypes   = schema.GroupVersionResource{Group: "instancetype.kubevirt.io", Version: "v1beta1", Resource: "virtualmachineinstancetypes"}
	gvrKubeVirtCRs     = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "kubevirts"}
	gvrCDICRs          = schema.GroupVersionResource{Group: "cdi.kubevirt.io", Version: "v1beta1", Resource: "cdis"}
)

// resolveClusterContext resolves a `cluster: <profile>` to a concrete kubeconfig context
// by self-loading the project PLUGIN-SIDE (loaderkit.ResolveKubernetesEntityViaExecutor —
// the SAME helper candy/plugin-kube uses). A miss / empty context is a valid result: the
// caller falls back to the kubeconfig current-context.
func resolveClusterContext(ctx context.Context, exec *sdk.Executor, in *params.KubeVirtInput) {
	if exec == nil {
		return
	}
	dir, derr := hostProjectDir(ctx, exec, "")
	if derr != nil {
		return
	}
	view, verr := loaderkit.ResolveKubernetesEntityViaExecutor(ctx, exec, dir, in.Cluster)
	if verr == nil && view != nil {
		in.KubeContext = view.KubeconfigContext
	}
}

// hostProjectDir resolves the project directory via the "deploy-plugins-connect" host
// seam (the SAME preamble candy/plugin-kube's hostProjectDir runs) — used by a leg that
// has no dispatch-threaded dir of its own (the `kubevirt:` check verb).
func hostProjectDir(ctx context.Context, exec *sdk.Executor, deployName string) (string, error) {
	reqJSON, err := json.Marshal(spec.DeployPluginsConnectRequest{Path: deployName})
	if err != nil {
		return "", err
	}
	resJSON, err := exec.HostBuild(ctx, "deploy-plugins-connect", reqJSON)
	if err != nil {
		return "", err
	}
	var reply spec.DeployPluginsConnectReply
	if err := json.Unmarshal(resJSON, &reply); err != nil {
		return "", err
	}
	return reply.Dir, nil
}

// nestedString is a thin legibility wrapper over the unstructured accessor (a
// missing/!found path is the empty string).
func nestedString(u *unstructured.Unstructured, fields ...string) string {
	v, _, _ := unstructured.NestedString(u.Object, fields...)
	return v
}
