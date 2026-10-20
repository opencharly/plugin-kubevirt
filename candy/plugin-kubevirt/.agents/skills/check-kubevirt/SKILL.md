---
name: check-kubevirt
description: 'The declarative `kubevirt:` check verb (served out-of-process by candy/plugin-kubevirt):
  vm / vmi / wait-ready / printable-status / guest-info / datavolume / snapshot /
  migration / start-stop-restart / console / port-forward, over the KubeVirt + CDI
  + instancetype + snapshot APIs. Use before authoring `kubevirt:` steps in a candy/box
  plan or probing a live KubeVirt deploy.'
---

# The `kubevirt:` check verb

`kubevirt:` is a DECLARATIVE check verb served out-of-process by
`candy/plugin-kubevirt`. There is **no host `charly check kubevirt`
subcommand** — you author a `kubevirt:` step inside a candy/box plan and run it
with `charly check live <image> --filter kubevirt` or `charly check run <bed>`.
It is the cluster-probe twin of the `kube:` verb, for the KubeVirt + CDI CRs.
Full detail: references/live-probe-verbs.md.

## Method surface

The method name is the scalar value (`kubevirt: vm`) or the `method:` key of the
`kubevirt:` map. All modifiers live INSIDE the `kubevirt:` map.

| `kubevirt:` value | Purpose |
|---|---|
| `vm` | VirtualMachine list/state |
| `vmi` | VirtualMachineInstance list/state (phase, node, IP, ready) |
| `wait-ready` | block until a VM/VMI is `Ready` + the guest agent `Connected` |
| `printable-status` | the human-readable status roll-up |
| `guest-info` | guest OS/user info via the guest agent |
| `datavolume` | CDI DataVolume state |
| `snapshot` | VirtualMachineSnapshot state |
| `migration` | VirtualMachineInstanceMigration state |
| `start` / `stop` / `restart` | mutate the VM runStrategy |
| `console` | serial console (virtctl) |
| `port-forward` | managed `virtctl port-forward` to guest SSH |

Cluster selection mirrors `kube:`: `cluster` (a `kind: kubernetes` template →
resolved `kube_context`), `kube_context`, `kubeconfig`, `namespace`.
