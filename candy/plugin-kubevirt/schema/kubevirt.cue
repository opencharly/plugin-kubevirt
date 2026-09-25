// This out-of-tree plugin's OWN CUE schema, served over the Describe channel — the
// typed plugin_input for the `kubevirt` VM/cluster-probe check verb. It is the SINGLE
// SOURCE for this plugin's params, used two ways (the same contract core `spec` and
// the other live-probe plugins use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (wrapped with
//     `package params` + `@go(params)`, then retagged with yaml tags) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the plugin serves this source over the
//     Describe channel; the host splices it onto the base (base ++ plugin) and
//     validates every authored `kubevirt:` step's plugin_input against #KubeVirtInput.
//
// The per-method fields live HERE (not in core #Op): a step's `kubevirt: <method>`
// sugar desugars to the internal plugin/plugin_input pair, the method name rides the
// input's `method` key (the verb's PRIMARY input field), and every kubevirt-exclusive
// modifier (cluster/kube_context/kubeconfig/namespace/name + the method-specific
// fields) lives here. Only the genuinely SHARED step modifiers (timeout, the
// exit_status/stdout/stderr matchers, context, …) stay on core #Op, read off the step
// Op by the provider.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (the SDK's
// serve-side check + gengotypes) AND splices onto the base (base ++ plugin is a
// def-name collision check, not a base-reference resolver).
//
// The plugin ALSO serves kind:kubevirt, deploy:kubevirt, and command:kubevirt. The
// kind capability keeps its authoring contract on core #KubeVirt (its value is
// validated HOST-SIDE against the kept #KubeVirtValue def — this plugin's deep
// OpValidate then restores the XOR rules CUE cannot express); the deploy substrate and
// the command carry no structured plugin_input, so no input def lives here for them.

// #KubeVirtInput is the `kubevirt` verb's plugin_input: the method name plus its
// method-exclusive modifiers.
#KubeVirtInput: {
	// method — the kubevirt method name; the verb's PRIMARY input field, so
	// `kubevirt: vm` desugars to {method: "vm"}.
	method: ("vm" | "vmi" | "wait-ready" | "printable-status" | "guest-info" | "migration" | "datavolume" | "snapshot" | "start" | "stop" | "restart" | "console" | "port-forward") @go(Method,type=string)
	// name / namespace — resource identity.
	name?:      string
	namespace?: string
	// cluster — a kind:kubernetes cluster template name; this PLUGIN self-resolves it
	// to a concrete kube_context (self-loading the project) and leaves the authored key
	// in place, so the input def admits both.
	cluster?: string
	// kubeconfig / kube_context — the cluster-selection pair (kubeconfig path +
	// context) an authored step may set explicitly.
	kubeconfig?:   string
	kube_context?: string @go(KubeContext)
	// wait / timeout seconds for the readiness-oriented methods (wait-ready,
	// guest-info, migration, datavolume, snapshot).
	wait_seconds?: int @go(WaitSeconds,type=int)
	// to_node — the migration target node (method: migration).
	to_node?: string @go(ToNode)
	// snapshot_class — the VolumeSnapshotClass (method: snapshot).
	snapshot_class?: string @go(SnapshotClass)
	// description — a human label for a created snapshot/datavolume.
	description?: string
	// size — the DataVolume size (method: datavolume).
	size?: string
	// storage_class — the DataVolume/snapshot storage class.
	storage_class?: string @go(StorageClass)
	// source_manifest — a multi-doc KubeVirt YAML manifest path the datavolume/vm
	// methods may apply verbatim.
	source_manifest?: string @go(SourceManifest)
	// local_port — the local TCP port for `virtctl port-forward` (method: port-forward);
	// 0 auto-allocates a free port.
	local_port?: int @go(LocalPort,type=int)
	// remote_port — the guest port to forward (method: port-forward; default 22).
	remote_port?: int @go(RemotePort,type=int)
	// json — machine-readable output toggle (methods: vm/vmi/datavolume/snapshot).
	json?: bool @go(JSON)
}
