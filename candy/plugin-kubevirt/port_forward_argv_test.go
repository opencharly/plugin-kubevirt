package kubevirt

import (
	"strings"
	"testing"
)

// port_forward_argv_test.go pins opencharly/plugin-kubevirt#11's SECOND bug: virtctl
// v1.9.0's `port-forward` takes POSITIONAL args, not the old `--local-port`/`--port`
// flags (`unknown flag: --local-port`), which made the managed forward die instantly and
// surface only as a 30-minute `wait-for-sshd … :0`.

func TestPortForwardArgv_PositionalSyntax(t *testing.T) {
	got := portForwardArgv("ctx-1", "default", "myvm", 45189)
	want := []string{"port-forward", "--context", "ctx-1", "myvm/default", "45189:22"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("portForwardArgv = %v, want %v", got, want)
	}
	// The retired flags must NOT appear — they are the bug.
	for _, a := range got {
		if a == "--local-port" || a == "--port" || a == "--namespace" {
			t.Errorf("argv must not carry the retired flag %q: %v", a, got)
		}
	}
}

func TestPortForwardArgv_NoNamespaceOrContext(t *testing.T) {
	got := portForwardArgv("", "", "myvm", 1234)
	want := []string{"port-forward", "myvm", "1234:22"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("portForwardArgv = %v, want %v", got, want)
	}
}
