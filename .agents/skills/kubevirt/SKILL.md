---
name: kubevirt
description: 'The `kubevirt:` deploy SUBSTRATE (7th): `kind: kubevirt` templates +
  `kubevirt:`/`target: kubevirt` deploys, the VirtualMachine CR lifecycle (DataVolume,
  runStrategy, VMI Ready + AgentConnected, managed `virtctl port-forward` SSH), instancetype,
  snapshots, live migration, GPU host-device passthrough, and nested virtualisation via a
  host-passthrough CPU. Served out-of-process
  by candy/plugin-kubevirt; driven by the `charly kubevirt` CLI family. Use before
  authoring or troubleshooting any `kind: kubevirt` entity/deploy.'
---

# KubeVirt — the 7th charly deploy substrate

`kubevirt:` is a **deploy SUBSTRATE**: it is the venue a deploy runs on, exactly
like `pod:`, `vm:`, `kubernetes:`, `local:`, `android:`, and `kindcluster:`. It is
**NOT** a check verb. A `kind: kubevirt` entity is a template; a `kubevirt:`
(or `target: kubevirt`) node is the deploy of it.

**Served out-of-process, no core code.** All KubeVirt interaction lives in
`candy/plugin-kubevirt` (opencharly/plugin-kubevirt), a standalone Go module the
host builds and serves over go-plugin gRPC — so the `k8s.io/client-go` +
`k8s.io/apimachinery` dependency stays out of charly's core `go.mod`. charly's
`externalDeploySubstratePlugins` map routes the `kubevirt` word to
`@github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt`. There is **no
host `charly vm` path for kubevirt** — a kubevirt deploy is brought up by the
plugin's deploy lifecycle, not `charly vm build`.

## Two ways to see it

1. **As a VM-shaped venue** — a `kind: kubevirt` entity is a `#KubeVirt` template
   (source, machine, guest, network, instancetype, migration, snapshot, GPU); the
   plugin renders it to a KubeVirt `VirtualMachine` CR, applies it, waits for the
   VMI `Ready` + the guest agent `Connected`, opens a managed `virtctl
   port-forward` to guest SSH on an auto-allocated local port, and records the
   venue identity as `kubevirt_state` in the per-host overlay. Guest-side plan
   walks then run over that SSH executor — the SAME `sdk/kit.WalkPlans` seam
   `deploy:vm` uses. The declared `DeployTraits` are
   `{Venue:"ssh", ImageBacked:true, BedTarget:true, SupportsEphemeral:true,
   SupportsFromSnapshot:true}` with **no** `ExclusiveVenue` (a KubeVirt VM is a
   cluster-managed workload, so no host arbiter).
2. **As a `charly kubevirt` CLI family** — `build`/`create` (render vs apply a
   VirtualMachine from a `kind: kubevirt` entity), `start`/`stop`/`restart`/
   `destroy`, `console` (virtctl serial), `ssh` (print the managed alias),
   `snapshot` (VirtualMachineSnapshot CR), `migrate`
   (VirtualMachineInstanceMigration), `gpu` (show the devices an entity would
   request), `status` (live VMs). The plugin owns the whole Kong grammar AND the
   CR operations, built on the SAME cluster_ops surface the verb uses (R3).

## Authoring (sketch)

```yaml
# The kind:kubevirt TEMPLATE — a VM definition. A deploy names it with `from:`.
# `distro` + `ssh_user` are the guest identity: they are required unless the
# source is a containerDisk whose VM box carries them in its OCI labels.
runner-vmi:
    kubevirt:
        cluster: prod                # a kind:kubernetes cluster template name
        namespace: vms
        distro: ubuntu
        ssh_user: ubuntu
        memory: 4G
        cpu: { cores: 2 }
        machine: q35
        firmware: { bootloader: efi }   # an Ubuntu cloud image is UEFI/GPT
        run_strategy: Always
        source:
            kind: data_volume         # container_disk | data_volume | pvc | clone
            size: 30Gi
            storage_class: csi-cinder-high-speed-gen2
            data_volume:
                http: { url: https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img }
        cloud_init: { timezone: UTC, charly_install: { strategy: auto } }

# The DEPLOY — a `kubevirt:` node carrying `from:`.
runner:
    kubevirt:
        from: runner-vmi
```

## Host devices and nested virtualisation

These are two DIFFERENT things, and the first is not the route to the second.

**`devices.host_devices`** is KubeVirt host-device passthrough
(`spec.domain.devices.hostDevices`) for PCI/MDEV/USB devices a cluster device
plugin advertises. The cluster must allowlist the resource in the KubeVirt CR's
`permittedHostDevices` and enable the Alpha `HostDevices` feature gate; a VMI
carrying the field without the gate is rejected at admission
(`spec.HostDevices: Host Devices feature gate is not enabled in kubevirt-config`,
MEASURED). It cannot carry a character device.

```yaml
vmi:
    kubevirt:
        devices:
            host_devices:
                - { device_name: intel.com/qat, name: quickaccess1 }
```

`device_name` is the resource the cluster's device plugin advertises; `name` is
the guest-visible label and defaults to `device_name`.

**Nested virtualisation** — a guest that itself runs VMs — needs a guest
`/dev/kvm`, and that comes from a **`host-passthrough` CPU**, not from host
devices: the guest's own kernel then loads `kvm_amd`/`kvm_intel` and creates the
device. MEASURED on an AMD host: `host-model` gave neither `svm` nor `/dev/kvm`;
after `cpu.model: host-passthrough` the guest had `svm`, `/dev/kvm`
(`root:kvm 0660`), `kvm_amd` loaded, and a nested
`qemu-system-x86_64 -accel kvm` ran with exit 0.

```yaml
runner-vmi:
    kubevirt:
        cpu: { cores: 2, model: host-passthrough }   # the guest then has svm + /dev/kvm
```

## The guest identity and the cluster it lands on

A `container_disk` source keeps no guest facts of its own: `distro` and `ssh_user`
are read from the VM box's `ai.opencharly.vm.box` OCI labels. Every OTHER arm
(`data_volume` / `pvc` / `clone`) is a bare disk, so it declares the pair on the
entity — an explicit value always wins over box labels. The id is validated against
the `#DistroID` vocabulary in this plugin's `OpValidate`, which also enforces the
presence rule (`schema/kubevirt.cue` documents both and delegates them here).

The cluster is named by `cluster:` (a `kind:kubernetes` template) or pinned with
`kube_context:`. The template's `kubeconfig` FILE is resolved beside its context, so
an externally-managed cluster is addressed by its own per-cluster kubeconfig rather
than by the ambient `$KUBECONFIG` / `~/.kube/config` — including by the managed
`virtctl port-forward`, which is the only route into the guest.

## The verb and the beds

The declarative `kubevirt:` check verb (see `/charly-kubevirt:check-kubevirt`)
probes a running cluster (vm/vmi/wait-ready/guest-info/datavolume/snapshot/
migration). The platform install is `candy/kubevirt-operator`
(`/charly-kubevirt:kubevirt-operator`); the client is `candy/kubevirt` (virtctl).
The R10 beds are `check-kubevirt-operator` (KubeVirt + CDI onto a k3s VM) and
`check-kubevirt-vm` (a charly-built VM box booted as a `containerDisk`, guest SSH
over the port-forward, then a fresh `charly update`).
