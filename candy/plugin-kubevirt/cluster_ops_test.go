package kubevirt

import "testing"

// TestVmRunStrategySatisfied pins the idempotent EnsureRunning decision: a VM already
// at the target strategy is left untouched (no Update → no optimistic-concurrency
// conflict with the controller). Crucially, the "Always" target is ALSO satisfied by
// spec.running==true — the controller's own normalization of a continuously-running VM
// — so a VM the controller already started is recognized as satisfied, not Updated.
func TestVmRunStrategySatisfied(t *testing.T) {
	cases := []struct {
		name     string
		obj      map[string]any
		strategy string
		want     bool
	}{
		{"explicit match", map[string]any{"spec": map[string]any{"runStrategy": "Always"}}, "Always", true},
		{"running true satisfies Always", map[string]any{"spec": map[string]any{"running": true}}, "Always", true},
		{"running false does not satisfy Always", map[string]any{"spec": map[string]any{"running": false}}, "Always", false},
		{"running true does not satisfy Halted", map[string]any{"spec": map[string]any{"running": true}}, "Halted", false},
		{"explicit mismatch", map[string]any{"spec": map[string]any{"runStrategy": "Halted"}}, "Always", false},
		{"no spec", map[string]any{}, "Always", false},
	}
	for _, c := range cases {
		if got := vmRunStrategySatisfied(c.obj, c.strategy); got != c.want {
			t.Errorf("%s: vmRunStrategySatisfied = %v, want %v", c.name, got, c.want)
		}
	}
}
