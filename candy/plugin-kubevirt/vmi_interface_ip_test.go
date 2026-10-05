package kubevirt

import "testing"

// vmi_interface_ip_test.go pins opencharly/plugin-kubevirt#14's root-cause fix: the
// managed port-forward must not start until the VMI publishes a non-empty
// `status.interfaces[].ipAddress` (the field KubeVirt's virt-api dialer reads); an empty
// IP makes the proxy dial `:22` and tears the listener down.

func TestVmiInterfaceIP(t *testing.T) {
	// populated → the IP, true.
	u := map[string]any{"status": map[string]any{
		"interfaces": []any{map[string]any{"ipAddress": "10.42.0.26"}},
	}}
	if ip, ok := vmiInterfaceIP(u); !ok || ip != "10.42.0.26" {
		t.Errorf("vmiInterfaceIP = (%q,%v), want (10.42.0.26,true)", ip, ok)
	}
	// empty ipAddress → false (the race we must NOT start the forward on).
	u2 := map[string]any{"status": map[string]any{
		"interfaces": []any{map[string]any{"ipAddress": ""}},
	}}
	if _, ok := vmiInterfaceIP(u2); ok {
		t.Errorf("an empty ipAddress must report not-ready")
	}
	// no interfaces yet → false.
	if _, ok := vmiInterfaceIP(map[string]any{"status": map[string]any{}}); ok {
		t.Errorf("a VMI without interfaces must report not-ready")
	}
	// no status → false.
	if _, ok := vmiInterfaceIP(map[string]any{}); ok {
		t.Errorf("a VMI without status must report not-ready")
	}
}
