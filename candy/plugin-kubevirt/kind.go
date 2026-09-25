package kubevirt

import (
	"encoding/json"
	"fmt"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// kind.go — the `kind:kubevirt` capability: the 6th substrate structural kind. It is a
// PURE-ECHO seam (the candy/plugin-substrate contract, R3): the host pre-decodes the
// CANONICAL node via the core loader, validates its value against the kept
// #KubeVirtValue def, and threads the result in op.Env
// (spec.StructuralKindLoadEnv.Standalone). This OpLoad RETURNS it — a deploy echo
// (spec.Deploy) the host folds into uf.Deploy, or a template echo (the typed
// spec.KubeVirt JSON) the host folds into the kubevirt template map. A substrate value
// is RICH + core-referencing (#KubeVirt + #Deploy + #VmCloudInit), so it cannot be
// soundly re-decoded from the raw op.Params.
//
// The deep OpValidate (Validates:true) restores the XOR rules a CUE closed struct cannot
// express — the SAME rule set candy/plugin-substrate's validate_kubevirt.go enforces (the
// host may dispatch OpValidate to either provider; the two are deliberately kept in step).

// kindLoad echoes the host-pre-decoded canonical node (the candy/plugin-substrate
// OpLoad contract).
func kindLoad(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var env spec.StructuralKindLoadEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("kubevirt kind: decode load env: %w", err)
		}
	}
	if env.Standalone == nil {
		return nil, fmt.Errorf("kubevirt kind: host threaded no pre-decoded node (op.Env.standalone missing)")
	}
	switch env.Standalone.Shape {
	case "template":
		if len(env.Standalone.Template) == 0 {
			return nil, fmt.Errorf("kubevirt kind: template shape carries no template")
		}
		// Echo the host-pre-decoded typed template value verbatim (raw JSON) — the host
		// folds it into its kubevirt template map.
		return &pb.InvokeReply{ResultJson: env.Standalone.Template}, nil
	case "deploy":
		if env.Standalone.Deploy == nil {
			return nil, fmt.Errorf("kubevirt kind: deploy shape carries no deploy node")
		}
		out, err := json.Marshal(env.Standalone.Deploy)
		if err != nil {
			return nil, fmt.Errorf("kubevirt kind: marshal deploy: %w", err)
		}
		return &pb.InvokeReply{ResultJson: out}, nil
	default:
		return nil, fmt.Errorf("kubevirt kind: unknown load shape %q (want deploy|template)", env.Standalone.Shape)
	}
}

// kubevirtValidateBody is the minimal decode of the authored #KubeVirt body the XOR rules
// read. It mirrors candy/plugin-substrate's kubevirtValidateBody exactly.
type kubevirtValidateBody struct {
	Source struct {
		Kind         string `json:"kind"`
		Image        string `json:"image"`
		StorageClass string `json:"storage_class"`
		DataVolume   any    `json:"data_volume"`
		PVC          string `json:"pvc"`
		Clone        any    `json:"clone"`
	} `json:"source"`
	CPU struct {
		Cores   int `json:"cores"`
		Sockets int `json:"sockets"`
		Threads int `json:"threads"`
	} `json:"cpu"`
	Instancetype string `json:"instancetype"`
	GPUs         []struct {
		ResourceName string `json:"resource_name"`
		DeviceName   string `json:"device_name"`
	} `json:"gpus"`
}

var kubevirtSourceKinds = map[string]bool{
	"container_disk": true,
	"data_volume":    true,
	"pvc":            true,
	"clone":          true,
}

// validateKubeVirtDeep runs the deep OpValidate check against the raw authored entity
// body the host threads via op.Params. Enforced:
//
//   - source.kind is exactly one of the four arms (container_disk / data_volume / pvc /
//     clone) — a Go decode catches an EMPTY or unknown kind the closedness gate sees no
//     concrete value to check.
//   - source.kind == container_disk forbids source.storage_class (a containerDisk is not
//     a PVC-backed volume).
//   - each gpus[] entry sets EXACTLY ONE of resource_name / device_name.
//   - instancetype XOR explicit cpu (a matcher + an inline domain CPU conflict).
func validateKubeVirtDeep(paramsJSON json.RawMessage) (spec.Diagnostics, error) {
	var body kubevirtValidateBody
	if len(paramsJSON) > 0 {
		if err := json.Unmarshal(paramsJSON, &body); err != nil {
			return spec.Diagnostics{}, fmt.Errorf("kubevirt kind: OpValidate: decode entity: %w", err)
		}
	}

	var diags spec.Diagnostics
	if body.Source.Kind == "" {
		diags.Items = append(diags.Items, spec.Diagnostic{
			Severity: "error",
			Path:     "source.kind",
			Message:  "must be one of container_disk | data_volume | pvc | clone",
		})
	} else if !kubevirtSourceKinds[body.Source.Kind] {
		diags.Items = append(diags.Items, spec.Diagnostic{
			Severity: "error",
			Path:     "source.kind",
			Message:  fmt.Sprintf("%q is not a known kubevirt source kind (one of container_disk | data_volume | pvc | clone)", body.Source.Kind),
		})
	}
	if body.Source.Kind == "container_disk" && body.Source.StorageClass != "" {
		diags.Items = append(diags.Items, spec.Diagnostic{
			Severity: "error",
			Path:     "source.storage_class",
			Message:  "is not valid on a container_disk source (a containerDisk is not a PVC-backed volume)",
		})
	}

	for i, g := range body.GPUs {
		hasRes := g.ResourceName != ""
		hasDev := g.DeviceName != ""
		if hasRes == hasDev {
			diags.Items = append(diags.Items, spec.Diagnostic{
				Severity: "error",
				Path:     fmt.Sprintf("gpus[%d]", i),
				Message:  "must set exactly one of resource_name (a cluster device-plugin resource) or device_name (a specific host device)",
			})
		}
	}

	if body.Instancetype != "" && (body.CPU.Cores != 0 || body.CPU.Sockets != 0 || body.CPU.Threads != 0) {
		diags.Items = append(diags.Items, spec.Diagnostic{
			Severity: "error",
			Path:     "instancetype",
			Message:  "is mutually exclusive with an explicit cpu block (the instancetype owns the domain CPU)",
		})
	}
	return diags, nil
}
