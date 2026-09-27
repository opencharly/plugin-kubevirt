package kubevirt

import "testing"

// TestCurrentRunStrategy pins the read helper the idempotent updateRunStrategy decision
// rests on. EnsureRunning consults it to SKIP a needless Update when the CR already
// carries the target runStrategy — the render sets the schema default, so that is the
// common path, and the skip avoids the "object has been modified" conflict the VM
// controller otherwise wins (observed live on the check-kubevirt-vm R10 bed).
func TestCurrentRunStrategy(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]any
		want string
	}{
		{"set", map[string]any{"spec": map[string]any{"runStrategy": "Always"}}, "Always"},
		{"unset", map[string]any{"spec": map[string]any{}}, ""},
		{"no spec", map[string]any{}, ""},
		{"wrong type", map[string]any{"spec": map[string]any{"runStrategy": 7}}, ""},
	}
	for _, c := range cases {
		if got := currentRunStrategy(c.obj); got != c.want {
			t.Errorf("%s: currentRunStrategy = %q, want %q", c.name, got, c.want)
		}
	}
}
