package kubevirt

import (
	"strings"
	"testing"
)

// port_forward_supervisor_test.go pins opencharly/plugin-kubevirt#14's fix: the managed
// `virtctl port-forward` must be SUPERVISED (restarted while it keeps exiting), and its
// whole process group torn down on Stop — otherwise one exit leaves the ssh-stanza port
// with no listener for the rest of the 30-minute `wait-for-sshd` cap.

func TestPortForwardSupervisorCmd_RestartsAndWritesPid(t *testing.T) {
	script := portForwardSupervisorCmd("/usr/bin/virtctl",
		[]string{"port-forward", "--context", "vm-ctx", "vm/myvm/default", "45189:22"},
		"/state/port-forward.pid", "/state/port-forward.log")

	// It re-runs virtctl in a loop (the supervision) …
	if !strings.Contains(script, "while :") {
		t.Errorf("supervisor must loop; got: %s", script)
	}
	// … with the exact argv (#11's positional/type-prefixed form) …
	if !strings.Contains(script, "port-forward") || !strings.Contains(script, "vm/myvm/default") || !strings.Contains(script, "45189:22") {
		t.Errorf("supervisor must run the portForwardArgv; got: %s", script)
	}
	// … writes the pidfile so the plugin can find/Stop it …
	if !strings.Contains(script, ">") || !strings.Contains(script, "port-forward.pid") {
		t.Errorf("supervisor must write the pidfile; got: %s", script)
	}
	// … and detaches (setsid) so it outlives the plugin subprocess.
	if !strings.Contains(script, "setsid") {
		t.Errorf("supervisor must detach (setsid); got: %s", script)
	}
}
