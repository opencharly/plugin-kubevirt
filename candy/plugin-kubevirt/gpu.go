package kubevirt

import "github.com/opencharly/spec/spec"

// gpu.go — the KubeVirt host-device (GPU) mapping. #KubeVirt.gpus already models the
// two KubeVirt forms directly (resource_name: a cluster device-plugin resource, e.g.
// "nvidia.com/gpu"; device_name: a specific host device), so the renderer emits
// spec.domain.devices.gpus from them verbatim (render.go). This file adds the second
// input path: a deploy's `requires_exclusive:` GPU declaration, mapped onto the same
// KubeVirt GPU entries.
//
// Unlike the vm substrate, NO host arbiter is involved (kubevirt declares no
// ExclusiveVenue — the cluster device plugin owns the physical card), so this mapping
// is pure and the resource name is taken straight from the declared token.

// GPUsFromRequiresExclusive maps a deploy's `requires_exclusive:` tokens onto KubeVirt
// GPU entries. A KubeVirt device-plugin resource token ("nvidia.com/gpu") maps to
// resource_name; any other token is treated as a specific host device (device_name).
// Empty tokens are skipped.
func GPUsFromRequiresExclusive(requiresExclusive []string) []spec.KubevirtGPU {
	var out []spec.KubevirtGPU
	for _, tok := range requiresExclusive {
		if tok == "" {
			continue
		}
		if isDevicePluginResource(tok) {
			out = append(out, spec.KubevirtGPU{ResourceName: tok})
			continue
		}
		out = append(out, spec.KubevirtGPU{DeviceName: tok})
	}
	return out
}

// isDevicePluginResource reports whether a token names a Kubernetes extended resource
// (a "vendor/name" form like "nvidia.com/gpu") rather than a raw host device.
func isDevicePluginResource(tok string) bool {
	for i := 0; i < len(tok); i++ {
		if tok[i] == '/' {
			return true
		}
	}
	return false
}

// RenderVirtualMachineGPUs returns the `resourceName=`/`deviceName=` entries a #KubeVirt
// entity requests (the `charly kubevirt gpu` output).
func RenderVirtualMachineGPUs(kv spec.KubeVirt) []string {
	var out []string
	for _, g := range kv.GPUs {
		switch {
		case g.ResourceName != "":
			out = append(out, "resourceName="+g.ResourceName)
		case g.DeviceName != "":
			out = append(out, "deviceName="+g.DeviceName)
		}
	}
	return out
}
