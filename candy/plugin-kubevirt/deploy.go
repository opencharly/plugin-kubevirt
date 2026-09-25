package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// deploy.go — the `deploy:kubevirt` SUBSTRATE provider: the PRERESOLVE leg (resolve
// the entity + cluster into a venue payload) and the OpExecute plan WALK. The walk is
// the SHARED kit.WalkPlans over the served guest SSH executor — the SAME walk the
// deploy:vm / deploy:local substrates use, so a kubevirt deploy applies the identical
// InstallPlan IR inside the cluster-scheduled guest (R3). The venue lifecycle itself
// lives in lifecycle.go.

// kubevirtDeployVenue is the preresolved payload the preresolver ships in
// DeployVenue.Substrate and the lifecycle/walk decode. The host carries it OPAQUELY (it
// never decodes it — the kernel reads only #ResolvedKubeVirt's Cluster/KubeContext), so
// the shape is this plugin's own.
type kubevirtDeployVenue struct {
	Cluster     string `json:"cluster,omitempty"`
	KubeContext string `json:"kube_context,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	DeployName  string `json:"deploy_name,omitempty"`
	VMName      string `json:"vm_name,omitempty"`
}

// kubevirtPreresolveParams decodes the host's marshalDeployOpParams envelope (name/dir/
// node/plans — the SAME ad-hoc shape every OpPreresolve dispatch carries).
type kubevirtPreresolveParams struct {
	Name string       `json:"name"`
	Dir  string       `json:"dir"`
	Node *spec.Deploy `json:"node"`
}

// invokeDeploy is the deploy-class dispatch: the lifecycle Ops (isLifecycleOp) route to
// lifecycle.go; OpPreresolve resolves the venue payload; every other deploy op
// (OpExecute) walks the InstallPlan views in the guest.
func invokeDeploy(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if isLifecycleOp(req.GetOp()) {
		return invokeLifecycle(ctx, req)
	}
	switch req.GetOp() {
	case sdk.OpPreresolve:
		return invokePreresolve(ctx, req)
	case sdk.OpExecute:
		return invokeExecute(ctx, req)
	}
	return nil, fmt.Errorf("kubevirt deploy: unsupported op %q", req.GetOp())
}

// invokePreresolve resolves the node's kind:kubevirt entity + its kind:kubernetes
// cluster template to a concrete kubeconfig context, and returns the venue payload.
func invokePreresolve(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("deploy:kubevirt preresolve: reach host reverse channel: %w", err)
	}
	var p kubevirtPreresolveParams
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &p); err != nil {
			return nil, fmt.Errorf("deploy:kubevirt preresolve: decode params: %w", err)
		}
	}
	node := p.Node
	if node == nil {
		tree, terr := loaderkit.ResolveMergedTreeViaExecutor(ctx, exec, p.Dir)
		if terr != nil {
			return nil, fmt.Errorf("deploy:kubevirt preresolve: resolve deploy tree: %w", terr)
		}
		n, ok := tree[p.Name]
		if !ok {
			return nil, fmt.Errorf("deploy:kubevirt preresolve: resolve deploy %q: no deploy entry", p.Name)
		}
		node = &n
	}
	entity := ""
	if node != nil {
		entity = node.From
	}
	if entity == "" {
		return nil, fmt.Errorf("deploy %q: target=kubevirt requires a `kubevirt:` (kind:kubevirt template) cross-ref — author `from: <template>` on the deployment entry", p.Name)
	}

	kv, err := resolveKubeVirtEntity(ctx, exec, p.Dir, entity)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving kubevirt entity %q: %w", p.Name, entity, err)
	}
	kubeContext, err := resolveKubeContext(ctx, exec, p.Dir, kv)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving kubevirt cluster: %w", p.Name, err)
	}

	venue := kubevirtDeployVenue{
		Cluster:     kv.Cluster,
		KubeContext: kubeContext,
		Namespace:   kv.Namespace,
		DeployName:  p.Name,
		VMName:      vmNameForDeploy(p.Name),
	}
	out, err := json.Marshal(venue)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal kubevirt venue: %w", p.Name, err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// invokeExecute walks the host's InstallPlan views inside the guest over the served SSH
// executor (the SAME kit.WalkPlans every machine-venue substrate uses) and returns the
// combined teardown ops the host records in the ledger.
func invokeExecute(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt: %w", err)
	}
	plans, err := sdk.DecodeInstallPlans(req.GetParamsJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt: decode plans: %w", err)
	}
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt: decode venue: %w", err)
	}
	reverseOps, err := kit.WalkPlans(ctx, exec, plans, kit.WalkOpts{})
	if err != nil {
		return nil, fmt.Errorf("plugin-kubevirt: %w", err)
	}
	candy := venue.DeployName
	if candy == "" {
		candy = "deploy-kubevirt"
	}
	return sdk.BuildDeployReply(reverseOps, candy, calver)
}

// resolveKubeVirtEntity self-loads the project PLUGIN-SIDE and decodes the named
// kind:kubevirt template body into a spec.KubeVirt. The body is read from
// uf.PluginKinds["kubevirt"] — the fold every standalone-substrate template lands in
// (the SAME map the kernel's fold writes). The template is already host-decoded
// (defaults applied) before it is folded, so a direct decode is faithful.
//
// GAP (reported): loaderkit exposes no ResolveKubeVirtEntityViaExecutor, and
// UnifiedFile.ProjectTemplates().ByKind omits "kubevirt" (fillNamespacedTemplates never
// copies the KubeVirt map). Reading PluginKinds directly closes that gap without
// reimplementing the loader.
func resolveKubeVirtEntity(ctx context.Context, exec *sdk.Executor, dir, name string) (*spec.KubeVirt, error) {
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, exec, dir)
	if err != nil {
		return nil, err
	}
	if !ok || uf == nil {
		return nil, fmt.Errorf("no charly.yml or no kind:kubevirt entities declared")
	}
	body := uf.PluginKinds["kubevirt"][name]
	if len(body) == 0 {
		// Namespace-aware fallback via the derived accessor (works once the projection
		// carries kubevirt; today it is populated for the root map, so this is a guard).
		if t := uf.ProjectTemplates(); t != nil {
			body = t.KubeVirt[name]
		}
	}
	if len(body) == 0 {
		if !loaderkit.ResolveEntityRef(uf, "kubevirt", name) {
			return nil, fmt.Errorf("not found")
		}
		return nil, fmt.Errorf("found but has no template body (a clone-base bed hop — resolve via the bed's own from:)")
	}
	var kv spec.KubeVirt
	if err := json.Unmarshal(body, &kv); err != nil {
		return nil, fmt.Errorf("decode kubevirt template: %w", err)
	}
	return &kv, nil
}

// resolveKubeContext resolves the concrete kubeconfig context for a #KubeVirt entity:
// an explicit kube_context wins; else the referenced kind:kubernetes cluster's
// KubeconfigContext, self-loaded plugin-side (loaderkit.ResolveKubernetesEntityViaExecutor
// — the SAME helper candy/plugin-kube uses, R3). An empty result is valid (the dynamic
// client then uses the kubeconfig current-context).
func resolveKubeContext(ctx context.Context, exec *sdk.Executor, dir string, kv *spec.KubeVirt) (string, error) {
	if kv.KubeContext != "" {
		return kv.KubeContext, nil
	}
	if kv.Cluster == "" {
		return "", nil
	}
	cluster, err := loaderkit.ResolveKubernetesEntityViaExecutor(ctx, exec, dir, kv.Cluster)
	if err != nil {
		return "", fmt.Errorf("resolving cluster %q: %w", kv.Cluster, err)
	}
	if cluster == nil {
		return "", fmt.Errorf("kind:kubernetes cluster %q resolved to an empty value", kv.Cluster)
	}
	return cluster.KubeconfigContext, nil
}
