package kubevirt

import "testing"

// TestRunStrategyPatch pins the merge-patch body EnsureRunning sends. The PATCH (not a
// read-modify-write Update) is what makes the run-strategy change CONFLICT-FREE against
// the VM controller's concurrent reconciles; the body must set only spec.runStrategy.
func TestRunStrategyPatch(t *testing.T) {
	if got, want := string(runStrategyPatch("Always")), `{"spec":{"runStrategy":"Always"}}`; got != want {
		t.Errorf("runStrategyPatch(Always) = %s, want %s", got, want)
	}
	if got, want := string(runStrategyPatch("Halted")), `{"spec":{"runStrategy":"Halted"}}`; got != want {
		t.Errorf("runStrategyPatch(Halted) = %s, want %s", got, want)
	}
}
