// Package kubevirt is the charly plugin owning ALL KubeVirt interaction: the 6th
// deploy SUBSTRATE (`kind:kubevirt` / `deploy:kubevirt` / `target: kubevirt`), the
// declarative `kubevirt:` cluster-probe check VERB, and the `charly kubevirt` CLI
// family — an importable root package + its own go.mod. It exists to keep the heavy
// k8s.io/client-go + k8s.io/apimachinery stack OUT of charly's core go.mod: the host
// go-builds this binary and serves it OUT-OF-PROCESS over go-plugin gRPC via the
// charly plugin SDK, so every capability dispatches through the provider registry
// exactly like a built-in.
//
// It serves FOUR capabilities:
//
//   - kind:kubevirt (Structural:true, Validates:true) — the 6th substrate kind. The
//     host pre-decodes the CANONICAL node and threads it in op.Env; OpLoad ECHOES it
//     (the candy/plugin-substrate contract, R3), and OpValidate runs the deep XOR
//     check CUE's closed struct cannot express.
//   - deploy:kubevirt (Lifecycle:true, Preresolve:true) — the venue lifecycle
//     (lifecycle.go) + the plan walk (deploy.go, kit.WalkPlans over the served guest
//     SSH executor), the same generic seams deploy:vm uses.
//   - verb:kubevirt (#KubeVirtInput, Primary:"method") — the check verb (methods.go),
//     CRUD over the KubeVirt/CDI/instancetype/snapshot dynamic clients.
//   - command:kubevirt — the `charly kubevirt` CLI (command.go).
//
// Dual-placement by construction: the SAME NewProvider()/NewMeta() compile INTO charly
// in-process when listed in compiled_plugins, or cmd/serve serves them OUT-OF-PROCESS
// over go-plugin gRPC when they are not — placement is invisible above the registry.
//
// Boundary law: this plugin MAY import concrete kinds (plugins may); it reaches the
// host only via the reverse channel / HostBuild / InvokeProvider seams.
package kubevirt

import (
	"embed"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// calver is this candy's CalVer, advertised over Describe. It MUST match the
// `version:` in candy/plugin-kubevirt/charly.yml — the host reports it when a provider
// resolves.
const calver = "2026.269.0001"

// NewProvider returns the kubevirt provider (all four capabilities).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the plugin's FOUR capabilities + its self-contained CUE schema via
// sdk.NewMeta → BuildCapabilities.
//
//   - kind:kubevirt is STRUCTURAL (its OpLoad returns the host-pre-decoded canonical
//     node, the candy/plugin-substrate contract) and declares Validates:true (the deep
//     OpValidate in kind.go restores the source-arm XOR / GPU resource⊕device /
//     instancetype⊕cpu rules a CUE closed struct cannot express). Its DeployTraits
//     mirror plugin-substrate's kubevirt entry (ssh venue, image-backed, a bed target,
//     ephemeral + snapshot capable, NO exclusive_venue — the host arbiter is skipped).
//   - deploy:kubevirt declares Lifecycle:true (its own venue lifecycle) +
//     Preresolve:true (the cluster/entity pre-resolution).
//   - verb:kubevirt validates its plugin_input against the served #KubeVirtInput.
//   - command:kubevirt is pass-through CLI args (no InputDef).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{
			{
				Class:      "kind",
				Word:       "kubevirt",
				Structural: true,
				Validates:  true,
				DeployTraits: &spec.DeployTraits{
					Venue:                "ssh",
					ImageBacked:          true,
					BedTarget:            true,
					SupportsEphemeral:    true,
					SupportsFromSnapshot: true,
				},
			},
			{Class: "deploy", Word: "kubevirt", Lifecycle: true, Preresolve: true},
			{Class: "verb", Word: "kubevirt", InputDef: "#KubeVirtInput", Primary: "method"},
			{Class: "command", Word: "kubevirt"},
		},
		schemaFS)
}
