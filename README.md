# plugin-kubevirt

The `plugin-kubevirt` plugin candy of the [opencharly/charly](https://github.com/opencharly/charly)
candy library, as a standalone repo (the candy de-submodule cutover, plugin
kind). The Go module lives at `candy/plugin-kubevirt/` with module path
`github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt`.

It owns ALL KubeVirt interaction, out-of-process:

- **`kind:kubevirt`** — the 6th deploy substrate kind (structural, `Validates:true`).
  A KubeVirt `VirtualMachine` on a Kubernetes cluster: both a standalone template
  (`kubevirt:` block) and a deploy (`from:`/`target: kubevirt`).
- **`deploy:kubevirt`** — the venue lifecycle (`Lifecycle:true`, `Preresolve:true`):
  ensure the DataVolume, apply the `VirtualMachine` CR, run it, wait VMI `Ready` +
  `AgentConnected`, start a managed `virtctl port-forward` on an auto-allocated local
  port, publish the managed ssh stanza, wait for sshd/cloud-init, ensure charly in the
  guest, then walk the `InstallPlan` via the shared `sdk/kit.WalkPlans` over the served
  guest SSH executor.
- **`verb:kubevirt`** — the declarative `kubevirt:` cluster-probe check verb (13
  methods: `vm`, `vmi`, `wait-ready`, `printable-status`, `guest-info`, `migration`,
  `datavolume`, `snapshot`, `start`, `stop`, `restart`, `console`, `port-forward`).
  `wait-ready` has two arms: it waits for a VirtualMachineInstance `Ready` in a VM
  namespace, and — selected by the CR's canonical install namespace — for the KubeVirt
  CR (`namespace: kubevirt`) or the CDI CR (`namespace: cdi`) to report phase
  `Deployed`, so a platform bed asserts the operator platform itself.
- **`command:kubevirt`** — the `charly kubevirt` CLI family (`build`, `create`,
  `start`, `stop`, `restart`, `destroy`, `console`, `ssh`, `snapshot`, `migrate`,
  `gpu`, `status`).

The plugin reuses the shared cloud-init renderer (`sdk/vmshared.RenderCloudInit`, R3)
and the shared deploy walk (`sdk/kit.WalkPlans`) — the same generic seams the `vm`
substrate uses; only the venue's boot path (the KubeVirt CR + `virtctl`) differs.

## Layout

```
candy/plugin-kubevirt/
  charly.yml              # plugin: providers [kind, deploy, verb, command] + ADE plan
  go.mod / go.sum
  plugin.go               # NewProvider / NewMeta
  provider.go             # Invoke dispatch by class
  kind.go                 # OpLoad echo + deep OpValidate
  cluster.go              # kubeconfig/context resolution + GVRs
  cluster_ops.go          # the live CR operations (apply/wait/status/delete)
  render.go               # PURE #KubeVirt -> VirtualMachine/DataVolume CR (unit-tested)
  lifecycle.go            # the venue lifecycle (prepare/rebuild/postapply/teardown)
  deploy.go               # the plan walk (kit.WalkPlans)
  methods.go              # the 13 verb methods
  migrate.go              # VirtualMachineInstanceMigration
  gpu.go                  # requires_exclusive -> spec.domain.devices.gpus
  command.go              # the `charly kubevirt` CLI (Kong)
  vmshared_seams.go       # wires vmshared.ValidateEgress to verb:egress
  schema/kubevirt.cue     # #KubeVirtInput (the verb's self-contained schema)
  params/cue_types_gen.go # generated (cue exp gengotypes)
  cmd/serve/main.go       # sdk.Main(NewProvider(), NewMeta(), CliMain)
```

## Verification

```
GOWORK=<dev go.work> go build ./...
GOWORK=<dev go.work> go vet ./...
GOWORK=<dev go.work> go test ./...
gofmt -l .
```
