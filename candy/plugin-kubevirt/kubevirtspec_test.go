package kubevirt

// kubevirtspec_test.go — the view's OWN decode test, which is what makes "the body moved and the
// behaviour did not" checkable (opencharly/spec#304 unit 6).
//
// The renderer's tests build the view in Go; this one decodes a REPRESENTATIVE authored body
// through the same json.Unmarshal the deploy path performs, asserts every field the plugin reads,
// and then renders from the decoded value — so a wrong tag drops data loudly instead of painting
// an empty domain.

import (
	"encoding/json"
	"testing"
)

func TestKubevirtSpec_DecodesAuthoredBody(t *testing.T) {
	body := []byte(`{
	  "cluster": "prod-cluster",
	  "kube_context": "ctx-1",
	  "namespace": "vms",
	  "storage_class": "fast",
	  "description": "a vm",
	  "source": {"kind": "container_disk", "image": "registry/img:v1", "pull_policy": "Always", "pull_secret": "regcred", "disk_path_in_image": "/disk.img", "data_volume": {"http": {"url": "http://x"}}, "size": "10Gi", "storage_class": "fast", "content_type": "kubevirt", "pvc": "claim", "clone": {"from": "src"}},
	  "distro": "fedora",
	  "ssh_user": "dev",
	  "memory": "4Gi",
	  "cpu": {"cores": 4, "sockets": 1, "threads": 2, "model": "host-passthrough", "dedicated_cpu_placement": true},
	  "machine": "q35",
	  "firmware": {"bootloader": "efi", "efi_secure_boot": true},
	  "devices": {"autoattach_pod_interface": true, "rng": true, "disks": [{"name": "extra", "bus": "virtio"}], "host_devices": [{"device_name": "pci_0000_01_00_0"}], "inputs": [{"type": "tablet", "bus": "virtio"}], "watchdog": {"model": "i6300esb", "action": "reset"}},
	  "plan": [{"run": "echo hi"}] ,
	  "gpus": [{"resource_name": "nvidia.com/gpu", "device_name": ""}],
	  "cloud_init": {"hostname": "vm1"},
	  "network": {"interface": "masquerade", "model": "virtio", "ports": [{"name": "http", "port": 8080, "protocol": "TCP"}]},
	  "instancetype": "large",
	  "preference": "fedora",
	  "run_strategy": "Always",
	  "eviction_strategy": "LiveMigrate",
	  "termination_grace_period_seconds": 30,
	  "node_selector": {"pool": "kv"},
	  "affinity": {"nodeAffinity": {}},
	  "migration": {"allow_auto_converge": true, "bandwidth_per_migration": "64Mi"},
	  "snapshot": [{"name": "pre-update", "description": "before", "snapshot_class": "std"}]
	}`)

	var kv kubevirtSpec
	if err := json.Unmarshal(body, &kv); err != nil {
		t.Fatalf("decode authored body: %v", err)
	}

	// identity + scalars
	if kv.Cluster != "prod-cluster" || kv.KubeContext != "ctx-1" || kv.Namespace != "vms" || kv.StorageClass != "fast" || kv.Description != "a vm" {
		t.Fatalf("identity fields not decoded: %+v", kv)
	}
	if kv.Distro != "fedora" || kv.SSHUser != "dev" || kv.Machine != "q35" || kv.Instancetype != "large" || kv.Preference != "fedora" {
		t.Errorf("scalar fields not decoded: %+v", kv)
	}
	if kv.RunStrategy != "Always" || kv.EvictionStrategy != "LiveMigrate" || kv.TerminationGracePeriodSeconds != 30 {
		t.Errorf("runtime policy fields not decoded: %+v", kv)
	}
	if string(kv.Memory) != "4Gi" {
		t.Errorf("memory not decoded: %q", kv.Memory)
	}

	// the hand-written source union
	if kv.Source.Kind != "container_disk" || kv.Source.Image != "registry/img:v1" || kv.Source.PullPolicy != "Always" || kv.Source.PullSecret != "regcred" || kv.Source.DiskPathInImage != "/disk.img" {
		t.Errorf("source container_disk arm not decoded: %+v", kv.Source)
	}
	if kv.Source.Size != "10Gi" || kv.Source.StorageClass != "fast" || kv.Source.ContentType != "kubevirt" || kv.Source.PVC != "claim" {
		t.Errorf("source data_volume/pvc arms not decoded: %+v", kv.Source)
	}
	if len(kv.Source.DataVolume) == 0 {
		t.Errorf("source data_volume map not decoded: %+v", kv.Source)
	}
	if kv.Source.Clone == nil || kv.Source.Clone.From != "src" {
		t.Errorf("source clone arm not decoded: %+v", kv.Source)
	}

	// the sub-blocks the renderer reads
	if kv.CPU == nil || kv.CPU.Cores != 4 || kv.CPU.Threads != 2 || kv.CPU.Model != "host-passthrough" || !kv.CPU.DedicatedCPUPlacement {
		t.Errorf("cpu not decoded: %+v", kv.CPU)
	}
	if kv.Firmware == nil || kv.Firmware.Bootloader != "efi" || !kv.Firmware.EFISecureBoot {
		t.Errorf("firmware not decoded: %+v", kv.Firmware)
	}
	if kv.Devices == nil || !kv.Devices.Rng || kv.Devices.Watchdog == nil || kv.Devices.Watchdog.Model != "i6300esb" {
		t.Errorf("devices not decoded: %+v", kv.Devices)
	}
	if kv.Devices == nil || len(kv.Devices.Disks) != 1 || len(kv.Devices.Host_devices) != 1 || kv.Devices.Host_devices[0].Device_name != "pci_0000_01_00_0" {
		t.Errorf("devices disks/host_devices not decoded: %+v", kv.Devices)
	}
	if kv.Devices == nil || len(kv.Devices.Inputs) != 1 || kv.Devices.Inputs[0].Type != "tablet" {
		t.Errorf("devices inputs not decoded: %+v", kv.Devices)
	}
	if len(kv.Plan) != 1 {
		t.Errorf("plan not decoded: %+v", kv.Plan)
	}
	if len(kv.GPUs) != 1 || kv.GPUs[0].ResourceName != "nvidia.com/gpu" {
		t.Errorf("gpus not decoded: %+v", kv.GPUs)
	}
	if kv.CloudInit == nil || kv.CloudInit.Hostname != "vm1" {
		t.Errorf("cloud_init not decoded: %+v", kv.CloudInit)
	}
	if kv.Network == nil || kv.Network.Interface != "masquerade" || len(kv.Network.Ports) != 1 || kv.Network.Ports[0].Port != 8080 {
		t.Errorf("network not decoded: %+v", kv.Network)
	}
	if kv.NodeSelector["pool"] != "kv" || kv.Migration == nil || !kv.Migration.AllowAutoConverge {
		t.Errorf("placement/migration not decoded: %+v %+v", kv.NodeSelector, kv.Migration)
	}
	if len(kv.Snapshots) != 1 || kv.Snapshots[0].Name != "pre-update" {
		t.Errorf("snapshots not decoded: %+v", kv.Snapshots)
	}

	// and the decoded view DRIVES the pure render helpers (the full renderer's 34 assertions
	// live in render_test.go; this test's job is the DECODE, so it drives a helper that reads
	// only the decoded GPUs).
	if gpus := RenderVirtualMachineGPUs(kv); len(gpus) != 1 {
		t.Errorf("renderer saw %d GPUs, decoded %d", len(gpus), len(kv.GPUs))
	}
}

// TestKubevirtSpec_DecodesSourceArms covers the boot-medium union arm by arm: a single fixture
// can only carry one `kind`, so each arm is decoded on its own and asserted — the arm that
// `renderStorage`/`renderDataVolumeTemplates` branch on.
func TestKubevirtSpec_DecodesSourceArms(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		check func(*testing.T, kubevirtSpec)
	}{
		{"container_disk", `{"source":{"kind":"container_disk","image":"registry/img:v1","disk_path_in_image":"/d.img"}}`,
			func(t *testing.T, kv kubevirtSpec) {
				if kv.Source.Kind != "container_disk" || kv.Source.Image == "" || kv.Source.DiskPathInImage != "/d.img" {
					t.Errorf("container_disk arm: %+v", kv.Source)
				}
			}},
		{"data_volume", `{"source":{"kind":"data_volume","data_volume":{"http":{"url":"http://x"}},"size":"10Gi","storage_class":"fast","content_type":"kubevirt"}}`,
			func(t *testing.T, kv kubevirtSpec) {
				if kv.Source.Kind != "data_volume" || len(kv.Source.DataVolume) == 0 || kv.Source.Size != "10Gi" || kv.Source.ContentType != "kubevirt" {
					t.Errorf("data_volume arm: %+v", kv.Source)
				}
			}},
		{"pvc", `{"source":{"kind":"pvc","pvc":"existing-claim"}}`,
			func(t *testing.T, kv kubevirtSpec) {
				if kv.Source.Kind != "pvc" || kv.Source.PVC != "existing-claim" {
					t.Errorf("pvc arm: %+v", kv.Source)
				}
			}},
		{"clone", `{"source":{"kind":"data_volume","clone":{"from":"source-dv"}}}`,
			func(t *testing.T, kv kubevirtSpec) {
				if kv.Source.Clone == nil || kv.Source.Clone.From != "source-dv" {
					t.Errorf("clone arm: %+v", kv.Source)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var kv kubevirtSpec
			if err := json.Unmarshal([]byte(tc.body), &kv); err != nil {
				t.Fatalf("decode %s arm: %v", tc.name, err)
			}
			tc.check(t, kv)
		})
	}
}
