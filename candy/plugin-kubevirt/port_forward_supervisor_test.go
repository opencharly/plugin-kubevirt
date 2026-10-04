package kubevirt

import (
	"strings"
	"testing"
)

// port_forward_supervisor_test.go pins opencharly/plugin-kubevirt#14's fix: the managed
// `virtctl port-forward` must be SUPERVISED with a HEALTH CHECK — a forward started early
// can HANG without binding its port (measured live: the process is alive, `ss` shows no
// listener, and a fresh virtctl on the same VM binds + ssh works). Restart-on-exit alone
// misses the hang; the supervisor must restart when the port is not LISTENING.

func TestPortForwardSupervisorCmd_HealthChecksAndRestarts(t *testing.T) {
	script := portForwardSupervisorCmd("/usr/bin/virtctl",
		[]string{"port-forward", "--context", "vm-ctx", "vm/myvm/default", "45189:22"},
		"/state/port-forward.pid", "/state/port-forward.log", 45189)

	// It must HEALTH-CHECK the exact stanza port (restart a hang, not only an exit).
	if !strings.Contains(script, "45189") || !strings.Contains(script, "LISTEN") {
		t.Errorf("supervisor must health-check the local port; got: %s", script)
	}
	// It uses the exact argv (#11's positional/type-prefixed form).
	if !strings.Contains(script, "port-forward") || !strings.Contains(script, "vm/myvm/default") || !strings.Contains(script, "45189:22") {
		t.Errorf("supervisor must run the portForwardArgv; got: %s", script)
	}
	// It writes the pidfile (find/Stop) and detaches (outlives the plugin subprocess).
	if !strings.Contains(script, "port-forward.pid") || !strings.Contains(script, "setsid") {
		t.Errorf("supervisor must write the pidfile and setsid-detach; got: %s", script)
	}
}
