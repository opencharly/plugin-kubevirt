package kubevirt

import (
	"fmt"

	"github.com/opencharly/sdk/vmshared"
	"github.com/opencharly/spec/spec"
)

// render.go — the PURE renderers from the authored #KubeVirt model onto KubeVirt CR
// maps. No client, no cluster, no disk I/O: everything here is a deterministic function
// of the spec value, so the whole VM shape is unit-testable (render_test.go).
//
// The cloud-init volume is produced by the SHARED renderer
// (vmshared.RenderCloudInit, R3) — the SAME implementation the vm substrate uses — so
// the #VmCloudInit contract has ONE owner.
//
// SPEC GAP (reported, not worked around): #KubeVirt carries no `source.distro` /
// `source.ssh` fields. The shared cloud-init renderer REQUIRES a distro (to emit the
// correct sshd unit + init-system arm) and an ssh user (to authorise the deploy key —
// without one the guest boots with no account charly can log in as). Both are runtime
// properties of the boot disk, resolved by the LIFECYCLE from the VM box's OCI metadata
// (deploykit.VmCapabilitiesFromLabels) for a containerDisk, and passed in via
// RenderOptions. A cloud_init on a source whose box metadata cannot be read FAILS LOUD
// rather than shipping a broken guest.

// RenderOptions carries the runtime-resolved values the pure renderer needs but that
// the authored #KubeVirt model does not itself hold (see the SPEC GAP note): the guest
// distro (for the cloud-init sshd unit), the ssh login user, and the public key to
// authorise. Name/Namespace/InstanceID are the CR identity.
type RenderOptions struct {
	Name       string
	Namespace  string
	Distro     string
	SSHUser    string
	SSHKey     string
	InstanceID string
	// RequiresExclusive is the deploy's `requires_exclusive:` token list. When the
	// entity declares no explicit `gpus:`, these tokens are mapped onto KubeVirt GPU
	// entries (gpu.go) so a deploy-level GPU declaration reaches the CR.
	RequiresExclusive []string
}

// RenderVirtualMachine renders a #KubeVirt entity into a KubeVirt `VirtualMachine` CR
// (apiVersion kubevirt.io/v1). The returned map is a plain `map[string]any` ready to
// hand to the dynamic client as an Unstructured.
func RenderVirtualMachine(kv spec.KubeVirt, opts RenderOptions) (map[string]any, error) {
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}

	templateSpec := map[string]any{}

	// --- domain ---
	domain := renderDomain(kv, opts)

	// --- storage: volumes + networks + the boot disk ---
	bootVolumeName := "boot"
	volumes, networks, disks := renderStorage(kv, bootVolumeName)

	// --- cloud-init volume ---
	if kv.CloudInit != nil {
		ci, err := renderCloudInitVolume(kv, opts)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, ci)
	}
	devices, _ := domain["devices"].(map[string]any)
	if devices == nil {
		devices = map[string]any{}
	}
	existingDisks, _ := devices["disks"].([]any)
	devices["disks"] = append(existingDisks, disks...)
	devices["interfaces"] = renderInterfaces(kv)
	domain["devices"] = devices
	templateSpec["domain"] = domain

	if len(volumes) > 0 {
		templateSpec["volumes"] = volumes
	}
	if len(networks) > 0 {
		templateSpec["networks"] = networks
	}
	if kv.Migration != nil {
		if m := renderMigration(kv.Migration); len(m) > 0 {
			templateSpec["migration"] = m
		}
	}
	if len(kv.NodeSelector) > 0 {
		templateSpec["nodeSelector"] = stringMap(kv.NodeSelector)
	}
	if len(kv.Affinity) > 0 {
		templateSpec["affinity"] = kv.Affinity
	}

	// --- instancetype / preference matchers ---
	if kv.Instancetype != "" || kv.Preference != "" {
		matchers := map[string]any{}
		if kv.Instancetype != "" {
			matchers["kind"] = "VirtualMachineInstancetype"
			matchers["name"] = kv.Instancetype
		}
		if kv.Preference != "" {
			matchers["preference"] = map[string]any{"kind": "VirtualMachinePreference", "name": kv.Preference}
		}
		templateSpec["instancetype"] = matchers
	}

	specBody := map[string]any{
		"template": map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"kubevirt.io/domain": opts.Name},
			},
			"spec": templateSpec,
		},
	}
	if kv.RunStrategy != "" {
		specBody["runStrategy"] = kv.RunStrategy
	}
	if kv.EvictionStrategy != "" {
		specBody["evictionStrategy"] = kv.EvictionStrategy
	}
	if kv.TerminationGracePeriodSeconds > 0 {
		specBody["terminationGracePeriodSeconds"] = int64(kv.TerminationGracePeriodSeconds)
	}
	if dt := renderDataVolumeTemplates(kv); len(dt) > 0 {
		specBody["dataVolumeTemplates"] = dt
	}

	return map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata": map[string]any{
			"name":      opts.Name,
			"namespace": opts.Namespace,
		},
		"spec": specBody,
	}, nil
}

// renderDomain renders spec.template.spec.domain (without the boot disk; the caller
// attaches disks).
func renderDomain(kv spec.KubeVirt, opts RenderOptions) map[string]any {
	domain := map[string]any{}
	devices := map[string]any{}

	if kv.Machine != "" {
		domain["machine"] = map[string]any{"type": kv.Machine}
	}
	if kv.Firmware != nil {
		domain["firmware"] = renderFirmware(kv.Firmware)
	}
	if kv.CPU != nil {
		domain["cpu"] = renderCPU(kv.CPU)
	}
	if kv.Memory != "" {
		domain["resources"] = map[string]any{"requests": map[string]any{"memory": kv.Memory}}
	}

	if d := kv.Devices; d != nil {
		if d.AutoattachPodInterface {
			devices["autoattachPodInterface"] = true
		}
		if d.AutoattachGraphicsDevice {
			devices["autoattachGraphicsDevice"] = true
		}
		if d.AutoattachSerialConsole {
			devices["autoattachSerialConsole"] = true
		}
		if d.Rng {
			devices["rng"] = map[string]any{}
		}
		for _, in := range d.Inputs {
			entry := map[string]any{"type": in.Type}
			if in.Bus != "" {
				entry["bus"] = in.Bus
			}
			devices["inputs"] = appendSlice(devices["inputs"], entry)
		}
		if d.Watchdog != nil {
			wd := map[string]any{"name": "watchdog", "model": d.Watchdog.Model}
			if d.Watchdog.Action != "" {
				wd["action"] = d.Watchdog.Action
			}
			devices["watchdog"] = wd
		}
		for _, extra := range d.Disks {
			devices["disks"] = appendSlice(devices["disks"], extra)
		}
	}

	// GPUs: an explicit #KubeVirt `gpus:` list wins; otherwise a deploy-level
	// `requires_exclusive:` declaration is mapped onto the same GPU entries (gpu.go).
	gpus := kv.GPUs
	if len(gpus) == 0 && len(opts.RequiresExclusive) > 0 {
		gpus = GPUsFromRequiresExclusive(opts.RequiresExclusive)
	}
	if len(gpus) > 0 {
		entries := make([]any, 0, len(gpus))
		for _, g := range gpus {
			entry := map[string]any{}
			if g.ResourceName != "" {
				entry["resourceName"] = g.ResourceName
			}
			if g.DeviceName != "" {
				entry["deviceName"] = g.DeviceName
			}
			entries = append(entries, entry)
		}
		devices["gpus"] = entries
	}

	if len(devices) > 0 {
		domain["devices"] = devices
	}
	return domain
}

func renderCPU(cpu *spec.KubevirtCPU) map[string]any {
	out := map[string]any{}
	if cpu.Cores > 0 || cpu.Sockets > 0 || cpu.Threads > 0 {
		topology := map[string]any{}
		if cpu.Cores > 0 {
			topology["cores"] = int64(cpu.Cores)
		}
		if cpu.Sockets > 0 {
			topology["sockets"] = int64(cpu.Sockets)
		}
		if cpu.Threads > 0 {
			topology["threads"] = int64(cpu.Threads)
		}
		out["topology"] = topology
	}
	if cpu.Model != "" {
		out["model"] = cpu.Model
	}
	if cpu.DedicatedCPUPlacement {
		out["dedicatedCpuPlacement"] = true
	}
	return out
}

func renderFirmware(fw *spec.KubevirtFirmware) map[string]any {
	switch fw.Bootloader {
	case "efi":
		efi := map[string]any{}
		if fw.EFISecureBoot {
			efi["secureBoot"] = true
		}
		return map[string]any{"bootloader": map[string]any{"efi": efi}}
	default: // "bios" (or unset)
		return map[string]any{"bootloader": map[string]any{"bios": map[string]any{}}}
	}
}

func renderMigration(m *spec.KubevirtMigration) map[string]any {
	out := map[string]any{}
	if m.AllowAutoConverge {
		out["allowAutoConverge"] = true
	}
	if m.AllowPostCopy {
		out["allowPostCopy"] = true
	}
	if m.BandwidthPerMigration != "" {
		out["bandwidthPerMigration"] = m.BandwidthPerMigration
	}
	return out
}

// renderStorage renders the boot volume(s) + networks + boot disk from the source arm.
// It returns the volumes list, the networks list, and the disk entries (each a
// `{name, disk:{bus}}` map referencing a volume name).
func renderStorage(kv spec.KubeVirt, bootName string) (volumes []any, networks []any, disks []any) {
	src := kv.Source
	switch src.Kind {
	case "container_disk":
		cd := map[string]any{"image": src.Image}
		if src.PullPolicy != "" {
			cd["imagePullPolicy"] = src.PullPolicy
		}
		if src.PullSecret != "" {
			cd["imagePullSecret"] = src.PullSecret
		}
		volumes = append(volumes, map[string]any{"name": bootName, "containerDisk": cd})
	case "data_volume", "clone":
		volumes = append(volumes, map[string]any{"name": bootName, "dataVolume": map[string]any{"name": bootName}})
	case "pvc":
		volumes = append(volumes, map[string]any{"name": bootName, "persistentVolumeClaim": map[string]any{"claimName": src.PVC}})
	}

	disks = append(disks, map[string]any{"name": bootName, "disk": map[string]any{"bus": "virtio"}})

	// The networks list is a single pod network (KubeVirt's masquerade default binds to
	// it); the interface TYPE is declared on the domain's interfaces.
	networks = append(networks, map[string]any{"name": "default", "pod": map[string]any{}})
	return volumes, networks, disks
}

// renderInterfaces renders spec.template.spec.domain.devices.interfaces from the
// authored network block (a single masquerade/bridge/pod interface over the pod net).
func renderInterfaces(kv spec.KubeVirt) []any {
	iface := "masquerade"
	model := "virtio"
	if kv.Network != nil {
		if kv.Network.Interface != "" {
			iface = kv.Network.Interface
		}
		if kv.Network.Model != "" {
			model = kv.Network.Model
		}
	}
	entry := map[string]any{"name": "default", "model": model}
	switch iface {
	case "bridge":
		entry["bridge"] = map[string]any{}
	case "pod":
		// a pod interface carries no binding block
	default:
		entry["masquerade"] = map[string]any{}
	}
	return []any{entry}
}

// renderCloudInitVolume builds the cloudInitNoCloud volume from the shared renderer.
func renderCloudInitVolume(kv spec.KubeVirt, opts RenderOptions) (map[string]any, error) {
	if opts.Distro == "" {
		return nil, fmt.Errorf("kubevirt: cloud_init is set but the guest distro could not be resolved (the shared cloud-init renderer needs a distro id to emit the sshd unit; declare it on the containerDisk VM box, or drop cloud_init on a source whose distro cannot be read)")
	}
	if opts.SSHUser == "" {
		return nil, fmt.Errorf("kubevirt: cloud_init is set but the guest ssh user could not be resolved (cloud-init would authorise no key and the guest would be unreachable; declare ssh_user on the containerDisk VM box)")
	}
	userData, metaData, networkConfig, err := renderCloudInit(kv, opts)
	if err != nil {
		return nil, err
	}
	noCloud := map[string]any{}
	if userData != "" {
		noCloud["userData"] = userData
	}
	noCloud["metaData"] = metaData
	if networkConfig != "" {
		noCloud["networkData"] = networkConfig
	}
	return map[string]any{"name": "cloudinitdisk", "cloudInitNoCloud": noCloud}, nil
}

// renderCloudInit feeds the shared vmshared.RenderCloudInit with a synthetic VmSpec
// built from the KubeVirt entity (the cloud_init block + the runtime-resolved distro +
// ssh user), so the #VmCloudInit contract has ONE renderer (R3).
func renderCloudInit(kv spec.KubeVirt, opts RenderOptions) (userData, metaData, networkConfig string, err error) {
	vm := &spec.ResolvedVm{
		Source:    spec.VmSource{Kind: "cloud_image", Distro: opts.Distro},
		SSH:       &spec.VmSsh{User: opts.SSHUser},
		CloudInit: kv.CloudInit,
	}
	return vmshared.RenderCloudInit(vm, vmshared.CloudInitRuntimeParams{
		Hostname:              opts.Name,
		InstanceID:            opts.InstanceID,
		SSHPublicKey:          opts.SSHKey,
		InjectKeyViaCloudInit: opts.SSHKey != "",
	})
}

// renderDataVolumeTemplates emits the KubeVirt `dataVolumeTemplates` for a data_volume /
// clone source so the VM owns the DV lifecycle (deleted with the VM). Returns nil for a
// containerDisk / PVC source.
func renderDataVolumeTemplates(kv spec.KubeVirt) []any {
	src := kv.Source
	if src.Kind != "data_volume" && src.Kind != "clone" {
		return nil
	}
	dvSpec := map[string]any{}
	switch {
	case src.Kind == "data_volume" && len(src.DataVolume) > 0:
		dvSpec["source"] = src.DataVolume
	case src.Kind == "clone" && src.Clone != nil:
		dvSpec["source"] = map[string]any{"PVC": map[string]any{"name": src.Clone.From}}
	}
	storage := map[string]any{}
	if src.Size != "" {
		storage["resources"] = map[string]any{"requests": map[string]any{"storage": src.Size}}
	}
	if src.StorageClass != "" {
		storage["storageClassName"] = src.StorageClass
	}
	if src.ContentType != "" {
		storage["contentType"] = src.ContentType
	}
	if len(storage) > 0 {
		dvSpec["storage"] = storage
	}
	return []any{
		map[string]any{
			"metadata": map[string]any{"name": "boot"},
			"spec":     dvSpec,
		},
	}
}

// RenderDataVolume renders a standalone CDI `DataVolume` CR for a data_volume / clone
// source arm (used by the ensure-datavolume step for a source that is NOT owned by the
// VM's dataVolumeTemplates). Returns nil when the entity's source is not data-volume-backed.
func RenderDataVolume(name, namespace string, kv spec.KubeVirt) map[string]any {
	src := kv.Source
	if namespace == "" {
		namespace = "default"
	}
	if src.Kind != "data_volume" && src.Kind != "clone" {
		return nil
	}
	specBody := map[string]any{}
	switch {
	case src.Kind == "data_volume" && len(src.DataVolume) > 0:
		specBody["source"] = src.DataVolume
	case src.Kind == "clone" && src.Clone != nil:
		specBody["source"] = map[string]any{"PVC": map[string]any{"name": src.Clone.From}}
	}
	storage := map[string]any{}
	if src.Size != "" {
		storage["resources"] = map[string]any{"requests": map[string]any{"storage": src.Size}}
	}
	if src.StorageClass != "" {
		storage["storageClassName"] = src.StorageClass
	}
	if src.ContentType != "" {
		storage["contentType"] = src.ContentType
	}
	if len(storage) > 0 {
		specBody["storage"] = storage
	}
	return map[string]any{
		"apiVersion": "cdi.kubevirt.io/v1beta1",
		"kind":       "DataVolume",
		"metadata": map[string]any{
			"name":      name + "-dv",
			"namespace": namespace,
		},
		"spec": specBody,
	}
}

// --- small map helpers (pure) -------------------------------------------------

func stringMap(in map[string]string) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func appendSlice(existing any, v any) []any {
	if s, ok := existing.([]any); ok {
		return append(s, v)
	}
	return []any{v}
}

// describeTarget renders a one-line human summary of a KubeVirt CR (used by the
// `vm`/`vmi` methods' non-JSON output).
func describeTarget(u map[string]any) string {
	kind := nestedMapString(u, "kind")
	ns := nestedMapString(u, "metadata", "namespace")
	name := nestedMapString(u, "metadata", "name")
	state := nestedMapString(u, "status", "printableStatus")
	if state == "" {
		state = nestedMapString(u, "status", "phase")
	}
	if state == "" {
		state = "unknown"
	}
	return fmt.Sprintf("%s %s/%s %s", kind, ns, name, state)
}

func nestedMapString(m map[string]any, fields ...string) string {
	cur := any(m)
	for _, f := range fields {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[f]
	}
	s, _ := cur.(string)
	return s
}
