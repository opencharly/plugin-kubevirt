package kubevirt

import (
	"encoding/json"
	"errors"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// vmshared_seams.go wires the shared VM package's injection seams for THIS plugin
// process. The shared cloud-init renderer (vmshared.RenderCloudInit) calls
// vmshared.ValidateEgress before emitting bytes; the seam has a DIFFERENT
// implementation per consumer (charly core wires the real CUE validator; an
// out-of-process plugin wires the verb:egress peer dispatch). Without a wiring the
// seam is nil and the renderer nil-derefs — so this init() is required (the same
// pattern candy/plugin-vm's vmshared_aliases.go uses).
func init() {
	vmshared.ValidateEgress = validateEgressPlugin
}

// validateEgressPlugin validates a rendered cloud-init artifact by Invoking verb:egress
// (OpValidate) over the reverse channel — the SAME provider charly core's own
// ValidateEgress* shims reach. Best-effort graceful-degrade with no reverse channel
// (e.g. the pure render call in a unit test): the HOST validates what the plugin
// returns, and a non-command context skips validation rather than crashing.
func validateEgressPlugin(kind, label string, data []byte) error {
	if commandExec == nil {
		return nil
	}
	params, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		Mode  string `json:"mode"`
		Data  string `json:"data"`
	}{Kind: kind, Label: label, Mode: "bytes", Data: string(data)})
	if err != nil {
		return err
	}
	out, err := commandExec.InvokeProvider(commandCtx, "verb", "egress", sdk.OpValidate, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	if len(out) > 0 {
		var reply struct {
			Error string `json:"error"`
		}
		if uerr := json.Unmarshal(out, &reply); uerr != nil {
			return errors.New("decode egress validate reply: " + uerr.Error())
		}
		if reply.Error != "" {
			return errors.New(reply.Error)
		}
	}
	return nil
}

// Compile-time proof the seam signature matches (guards against an sdk change).
var _ func(kind, label string, data []byte) error = validateEgressPlugin
var _ = spec.ScopeUser
