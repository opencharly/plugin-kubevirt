package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// provider.go is the out-of-process provider for ALL of plugin-kubevirt's
// capabilities. Invoke branches on the request CLASS:
//
//   - "kind"    — OpLoad (echo the host-pre-decoded canonical node) + OpValidate (the
//     deep XOR check).
//   - "deploy"  — OpPreresolve / OpPrepareVenue+the lifecycle Ops / OpExecute (the plan
//     walk) / OpArtifactKey / OpPostApply / OpPostTeardown / OpStart/Stop/Status/Logs/
//     Shell/Attach/Rebuild.
//   - "verb"    — the `kubevirt:` check verb: decode the full #Op + CheckEnv, resolve
//     any `cluster:` convenience to a concrete kube_context plugin-side, dispatch the
//     method, and self-evaluate the matchers via sdk.VerbVerdict (the out-of-process
//     verb path does not run the host matcher pipeline, so this Invoke owns the whole
//     verdict — R3, the same sdk pipeline every live verb uses).
//   - "command" — the `charly kubevirt` CLI (OpRun, pass-through args).
type provider struct{ pb.UnimplementedProviderServer }

func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetClass() {
	case "kind":
		switch req.GetOp() {
		case sdk.OpLoad:
			return kindLoad(req)
		case sdk.OpValidate:
			diags, err := validateKubeVirtDeep(req.GetParamsJson())
			if err != nil {
				return nil, err
			}
			out, merr := json.Marshal(diags)
			if merr != nil {
				return nil, fmt.Errorf("kubevirt: marshal diagnostics: %w", merr)
			}
			return &pb.InvokeReply{ResultJson: out}, nil
		default:
			return nil, fmt.Errorf("kubevirt kind: unsupported op %q (only %q, %q)", req.GetOp(), sdk.OpLoad, sdk.OpValidate)
		}
	case "deploy":
		return invokeDeploy(ctx, req)
	case "command":
		return invokeCommand(ctx, req)
	case "verb":
		return invokeVerb(ctx, req)
	default:
		return nil, fmt.Errorf("kubevirt: unsupported class %q", req.GetClass())
	}
}

// kubevirtEnv is the plugin-side decode of the CheckEnv the host ships as
// Operation.Env for a `kubevirt:` check step — only Mode matters here (the verb
// probes a cluster, not a container, so it needs no container resolution).
type kubevirtEnv struct {
	Box  string `json:"box"`
	Mode string `json:"mode"` // "live" | "box"
}

// invokeVerb runs one `kubevirt:` check operation. The host dispatches a `kubevirt:`
// check step through the registry (ResolveVerb("kubevirt") → this grpcProvider →
// Provider.Invoke) with the FULL #Op marshaled as params_json and a CheckEnv snapshot as
// env; every kubevirt-exclusive field rides the desugared plugin input
// (params.KubeVirtInput). Because the out-of-process verb path does NOT run the host-side
// matcher pipeline, this Invoke OWNS the whole verdict.
func invokeVerb(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "kubevirt: decode op: "+err.Error())
		}
	}
	var env kubevirtEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	var in params.KubeVirtInput
	kit.DecodeInput(op.PluginInput, &in)
	method := in.Method

	// A cluster probe needs a reachable cluster — skip under `charly check box`
	// (mirrors the host's RunModeBox skip).
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("kubevirt: %s requires a running cluster (skip under charly check box)", method))
	}

	// Resolve the `cluster: <profile>` convenience to a concrete kubeconfig context,
	// PLUGIN-SIDE (self-loading the project over the reverse channel — R3, the SAME
	// loaderkit.ResolveKubernetesEntityViaExecutor candy/plugin-kube uses). A miss is a
	// valid result — the verb then falls back to the kubeconfig current-context.
	if in.Cluster != "" && in.KubeContext == "" {
		exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
		if err != nil {
			return sdk.ResultJSON("fail", fmt.Sprintf("kubevirt: %s: %v", method, err))
		}
		resolveClusterContext(ctx, exec, &in)
	}

	conn := connFromInput(&in)
	out, runErr := dispatch(conn, &op, &in)

	// The shared exit/stdout/stderr verdict pipeline. kubevirt produces no artifact.
	return sdk.VerbVerdict("kubevirt", method, out, runErr, &op, false)
}
