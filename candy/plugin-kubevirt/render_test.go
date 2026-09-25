package kubevirt

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// render_test.go — table tests for the PURE renderers. These are the core of the
// plugin: every VirtualMachine / DataVolume shape decision is asserted here without a
// cluster.

func containerDiskKV() spec.KubeVirt {
	return spec.KubeVirt{
		Cluster:     "k3s",
		Namespace:   "charly",
		Description: "test",
		Source: spec.KubevirtSource{
			Kind:       "container_disk",
			Image:      "localhost/charly-vm:latest",
			PullPolicy: "IfNotPresent",
		},
		Memory:  "4G",
		CPU:     &spec.KubevirtCPU{Cores: 2, Sockets: 1, Threads: 1},
		Machine: "q35",
		Firmware: &spec.KubevirtFirmware{
			Bootloader:    "efi",
			EFISecureBoot: true,
		},
		Devices: &spec.KubevirtDevices{
			Rng: true,
			Inputs: []spec.KubevirtInput{
				{Type: "tablet", Bus: "usb"},
			},
			Watchdog: &spec.KubevirtWatchdog{Model: "i6300esb", Action: "reset"},
		},
		GPUs: []spec.KubevirtGPU{
			{ResourceName: "nvidia.com/gpu"},
		},
		Network:                       &spec.KubevirtNetwork{Interface: "masquerade", Model: "virtio"},
		RunStrategy:                   "Always",
		EvictionStrategy:              "LiveMigrate",
		TerminationGracePeriodSeconds: 30,
		NodeSelector:                  map[string]string{"kubernetes.io/os": "linux"},
		Instancetype:                  "u1.medium",
		Preference:                    "fedora",
		CloudInit:                     &spec.VmCloudInit{Hostname: "kv-test"},
	}
}

// TestRenderVirtualMachine_ContainerDisk asserts the full CR shape for a containerDisk
// source: apiVersion/kind/identity, domain devices, storage, cloud-init, run strategy,
// GPU, instancetype matcher.
func TestRenderVirtualMachine_ContainerDisk(t *testing.T) {
	kv := containerDiskKV()
	obj, err := RenderVirtualMachine(kv, RenderOptions{
		Name:      "charly-kv-test",
		Namespace: "charly",
		Distro:    "arch",
		SSHUser:   "arch",
		SSHKey:    "ssh-ed25519 AAAATEST",
	})
	if err != nil {
		t.Fatalf("RenderVirtualMachine: %v", err)
	}

	if obj["apiVersion"] != "kubevirt.io/v1" || obj["kind"] != "VirtualMachine" {
		t.Fatalf("bad GVK: %v", obj)
	}
	meta := obj["metadata"].(map[string]any)
	if meta["name"] != "charly-kv-test" || meta["namespace"] != "charly" {
		t.Fatalf("bad metadata: %v", meta)
	}

	specBody := obj["spec"].(map[string]any)
	if specBody["runStrategy"] != "Always" {
		t.Errorf("runStrategy = %v, want Always", specBody["runStrategy"])
	}
	if specBody["evictionStrategy"] != "LiveMigrate" {
		t.Errorf("evictionStrategy = %v", specBody["evictionStrategy"])
	}
	if specBody["terminationGracePeriodSeconds"] != int64(30) {
		t.Errorf("terminationGracePeriodSeconds = %v", specBody["terminationGracePeriodSeconds"])
	}

	tmpl := specBody["template"].(map[string]any)
	tmplSpec := tmpl["spec"].(map[string]any)

	// instancetype matcher
	it, ok := tmplSpec["instancetype"].(map[string]any)
	if !ok {
		t.Fatalf("missing instancetype matcher: %v", tmplSpec)
	}
	if it["kind"] != "VirtualMachineInstancetype" || it["name"] != "u1.medium" {
		t.Errorf("instancetype matcher = %v", it)
	}
	pref, ok := it["preference"].(map[string]any)
	if !ok || pref["name"] != "fedora" {
		t.Errorf("preference matcher = %v", it["preference"])
	}

	// domain
	domain := tmplSpec["domain"].(map[string]any)
	if domain["machine"].(map[string]any)["type"] != "q35" {
		t.Errorf("machine = %v", domain["machine"])
	}
	fw := domain["firmware"].(map[string]any)
	efi := fw["bootloader"].(map[string]any)["efi"].(map[string]any)
	if efi["secureBoot"] != true {
		t.Errorf("secureBoot = %v", efi)
	}
	cpu := domain["cpu"].(map[string]any)
	topo := cpu["topology"].(map[string]any)
	if topo["cores"] != int64(2) || topo["sockets"] != int64(1) || topo["threads"] != int64(1) {
		t.Errorf("cpu topology = %v", topo)
	}
	if domain["resources"].(map[string]any)["requests"].(map[string]any)["memory"] != "4G" {
		t.Errorf("memory = %v", domain["resources"])
	}
	devices := domain["devices"].(map[string]any)
	if devices["rng"] == nil {
		t.Errorf("missing rng: %v", devices)
	}
	if devices["watchdog"].(map[string]any)["action"] != "reset" {
		t.Errorf("watchdog = %v", devices["watchdog"])
	}
	inputs := devices["inputs"].([]any)
	if len(inputs) != 1 || inputs[0].(map[string]any)["type"] != "tablet" {
		t.Errorf("inputs = %v", inputs)
	}
	gpus := devices["gpus"].([]any)
	if len(gpus) != 1 || gpus[0].(map[string]any)["resourceName"] != "nvidia.com/gpu" {
		t.Errorf("gpus = %v", gpus)
	}
	disks := devices["disks"].([]any)
	if len(disks) != 1 || disks[0].(map[string]any)["name"] != "boot" {
		t.Errorf("disks = %v", disks)
	}
	ifaces := devices["interfaces"].([]any)
	if len(ifaces) != 1 || ifaces[0].(map[string]any)["masquerade"] == nil {
		t.Errorf("interfaces = %v", ifaces)
	}

	// volumes: boot containerDisk + cloud-init
	volumes := tmplSpec["volumes"].([]any)
	if len(volumes) != 2 {
		t.Fatalf("volumes = %v", volumes)
	}
	boot := volumes[0].(map[string]any)
	cd := boot["containerDisk"].(map[string]any)
	if cd["image"] != "localhost/charly-vm:latest" || cd["imagePullPolicy"] != "IfNotPresent" {
		t.Errorf("containerDisk = %v", cd)
	}
	ciVol := volumes[1].(map[string]any)
	if ciVol["name"] != "cloudinitdisk" {
		t.Errorf("cloud-init volume name = %v", ciVol)
	}
	noCloud := ciVol["cloudInitNoCloud"].(map[string]any)
	ud, _ := noCloud["userData"].(string)
	if !strings.Contains(ud, "#cloud-config") {
		t.Errorf("userData missing #cloud-config header: %q", ud)
	}
	if !strings.Contains(ud, "kv-test") {
		t.Errorf("userData missing hostname: %q", ud)
	}
	if !strings.Contains(ud, "arch") {
		t.Errorf("userData missing ssh user: %q", ud)
	}
	if !strings.Contains(ud, "AAAATEST") {
		t.Errorf("userData missing ssh key: %q", ud)
	}

	// networks
	networks := tmplSpec["networks"].([]any)
	if len(networks) != 1 || networks[0].(map[string]any)["pod"] == nil {
		t.Errorf("networks = %v", networks)
	}

	// nodeSelector
	if tmplSpec["nodeSelector"].(map[string]any)["kubernetes.io/os"] != "linux" {
		t.Errorf("nodeSelector = %v", tmplSpec["nodeSelector"])
	}
}

// TestRenderVirtualMachine_DataVolume asserts the data_volume arm: a boot dataVolume
// reference + dataVolumeTemplates, and the standalone DataVolume CR.
func TestRenderVirtualMachine_DataVolume(t *testing.T) {
	kv := spec.KubeVirt{
		Source: spec.KubevirtSource{
			Kind: "data_volume",
			DataVolume: map[string]any{
				"http": map[string]any{"url": "https://example.com/disk.qcow2"},
			},
			Size:         "20Gi",
			StorageClass: "local-path",
			ContentType:  "kubevirt",
		},
		Memory: "2G",
	}
	obj, err := RenderVirtualMachine(kv, RenderOptions{Name: "dvvm", Namespace: "default"})
	if err != nil {
		t.Fatalf("RenderVirtualMachine: %v", err)
	}
	specBody := obj["spec"].(map[string]any)
	dts, ok := specBody["dataVolumeTemplates"].([]any)
	if !ok || len(dts) != 1 {
		t.Fatalf("dataVolumeTemplates = %v", specBody["dataVolumeTemplates"])
	}
	dt := dts[0].(map[string]any)
	dvSpec := dt["spec"].(map[string]any)
	if dvSpec["source"].(map[string]any)["http"] == nil {
		t.Errorf("dataVolumeTemplate source = %v", dvSpec["source"])
	}
	storage := dvSpec["storage"].(map[string]any)
	if storage["storageClassName"] != "local-path" || storage["contentType"] != "kubevirt" {
		t.Errorf("storage = %v", storage)
	}
	if storage["resources"].(map[string]any)["requests"].(map[string]any)["storage"] != "20Gi" {
		t.Errorf("storage size = %v", storage["resources"])
	}

	volumes := specBody["template"].(map[string]any)["spec"].(map[string]any)["volumes"].([]any)
	boot := volumes[0].(map[string]any)
	dv := boot["dataVolume"].(map[string]any)
	if dv["name"] != "boot" {
		t.Errorf("boot dataVolume ref = %v", dv)
	}

	// Standalone DataVolume CR
	standalone := RenderDataVolume("dvvm", "default", kv)
	if standalone == nil {
		t.Fatal("RenderDataVolume returned nil for a data_volume source")
	}
	if standalone["apiVersion"] != "cdi.kubevirt.io/v1beta1" || standalone["kind"] != "DataVolume" {
		t.Errorf("DataVolume GVK = %v", standalone)
	}
	if standalone["metadata"].(map[string]any)["name"] != "dvvm-dv" {
		t.Errorf("DataVolume name = %v", standalone["metadata"])
	}
}

// TestRenderDataVolume_NonDataVolumeSource asserts nil for a containerDisk/PVC source.
func TestRenderDataVolume_NonDataVolumeSource(t *testing.T) {
	for _, kind := range []string{"container_disk", "pvc"} {
		kv := spec.KubeVirt{Source: spec.KubevirtSource{Kind: kind, Image: "x", PVC: "y"}}
		if got := RenderDataVolume("n", "ns", kv); got != nil {
			t.Errorf("kind %s: RenderDataVolume = %v, want nil", kind, got)
		}
	}
}

// TestRenderVirtualMachine_PVC asserts the pvc arm references the claim name.
func TestRenderVirtualMachine_PVC(t *testing.T) {
	kv := spec.KubeVirt{Source: spec.KubevirtSource{Kind: "pvc", PVC: "my-disk"}}
	obj, err := RenderVirtualMachine(kv, RenderOptions{Name: "pvcvm"})
	if err != nil {
		t.Fatalf("RenderVirtualMachine: %v", err)
	}
	volumes := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["volumes"].([]any)
	claim := volumes[0].(map[string]any)["persistentVolumeClaim"].(map[string]any)
	if claim["claimName"] != "my-disk" {
		t.Errorf("pvc claim = %v", claim)
	}
}

// TestRenderVirtualMachine_CloudInitRequiresIdentity asserts the loud failure when a
// cloud_init is declared but the distro/ssh user could not be resolved (the SPEC GAP).
func TestRenderVirtualMachine_CloudInitRequiresIdentity(t *testing.T) {
	kv := spec.KubeVirt{
		Source:    spec.KubevirtSource{Kind: "data_volume", Size: "1Gi", DataVolume: map[string]any{"blank": map[string]any{}}},
		CloudInit: &spec.VmCloudInit{Hostname: "x"},
	}
	if _, err := RenderVirtualMachine(kv, RenderOptions{Name: "x"}); err == nil {
		t.Fatal("expected error for cloud_init with no distro, got nil")
	}
	// No cloud_init → no identity needed.
	kv.CloudInit = nil
	if _, err := RenderVirtualMachine(kv, RenderOptions{Name: "x"}); err != nil {
		t.Fatalf("no cloud_init should render without identity: %v", err)
	}
}

// TestRenderVirtualMachine_BiosDefault asserts an unset/nil firmware renders BIOS.
func TestRenderVirtualMachine_BiosDefault(t *testing.T) {
	kv := spec.KubeVirt{
		Source:   spec.KubevirtSource{Kind: "container_disk", Image: "img"},
		Firmware: &spec.KubevirtFirmware{},
	}
	obj, err := RenderVirtualMachine(kv, RenderOptions{Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	fw := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["domain"].(map[string]any)["firmware"].(map[string]any)
	if fw["bootloader"].(map[string]any)["bios"] == nil {
		t.Errorf("expected bios bootloader, got %v", fw)
	}
}

// TestRenderVirtualMachine_GpuDeviceName asserts device_name GPU rendering.
func TestRenderVirtualMachine_GpuDeviceName(t *testing.T) {
	kv := spec.KubeVirt{
		Source: spec.KubevirtSource{Kind: "container_disk", Image: "img"},
		GPUs:   []spec.KubevirtGPU{{DeviceName: "0000:01:00.0"}},
	}
	obj, err := RenderVirtualMachine(kv, RenderOptions{Name: "g"})
	if err != nil {
		t.Fatal(err)
	}
	gpus := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["domain"].(map[string]any)["devices"].(map[string]any)["gpus"].([]any)
	if gpus[0].(map[string]any)["deviceName"] != "0000:01:00.0" {
		t.Errorf("gpus = %v", gpus)
	}
}

// TestRenderVirtualMachine_Clone asserts the clone arm emits a DataVolume template
// sourced from the clone PVC.
func TestRenderVirtualMachine_Clone(t *testing.T) {
	kv := spec.KubeVirt{
		Source: spec.KubevirtSource{Kind: "clone", Clone: &spec.KubevirtCloneSource{From: "golden-vm"}, Size: "30Gi"},
	}
	obj, err := RenderVirtualMachine(kv, RenderOptions{Name: "clonevm"})
	if err != nil {
		t.Fatal(err)
	}
	dts := obj["spec"].(map[string]any)["dataVolumeTemplates"].([]any)
	src := dts[0].(map[string]any)["spec"].(map[string]any)["source"].(map[string]any)
	if src["PVC"].(map[string]any)["name"] != "golden-vm" {
		t.Errorf("clone source = %v", src)
	}
}

// TestRenderVirtualMachineGPUs asserts the CLI/output helper.
func TestRenderVirtualMachineGPUs(t *testing.T) {
	kv := spec.KubeVirt{GPUs: []spec.KubevirtGPU{{ResourceName: "nvidia.com/gpu"}, {DeviceName: "0000:01:00.0"}}}
	got := RenderVirtualMachineGPUs(kv)
	want := []string{"resourceName=nvidia.com/gpu", "deviceName=0000:01:00.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestGPUsFromRequiresExclusive asserts a `vendor/name` token maps to resource_name and
// any other token to device_name.
func TestGPUsFromRequiresExclusive(t *testing.T) {
	got := GPUsFromRequiresExclusive([]string{"nvidia.com/gpu", "0000:01:00.0", ""})
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 entries", got)
	}
	if got[0].ResourceName != "nvidia.com/gpu" {
		t.Errorf("entry[0] = %+v, want resource_name", got[0])
	}
	if got[1].DeviceName != "0000:01:00.0" {
		t.Errorf("entry[1] = %+v, want device_name", got[1])
	}
}

// TestRenderVirtualMachine_RequiresExclusiveGPU asserts a deploy-level requires_exclusive
// reaches spec.domain.devices.gpus when the entity declares no explicit gpus.
func TestRenderVirtualMachine_RequiresExclusiveGPU(t *testing.T) {
	kv := spec.KubeVirt{Source: spec.KubevirtSource{Kind: "container_disk", Image: "img"}}
	obj, err := RenderVirtualMachine(kv, RenderOptions{Name: "rg", RequiresExclusive: []string{"nvidia.com/gpu"}})
	if err != nil {
		t.Fatal(err)
	}
	gpus := obj["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["domain"].(map[string]any)["devices"].(map[string]any)["gpus"].([]any)
	if gpus[0].(map[string]any)["resourceName"] != "nvidia.com/gpu" {
		t.Errorf("gpus = %v", gpus)
	}
}
