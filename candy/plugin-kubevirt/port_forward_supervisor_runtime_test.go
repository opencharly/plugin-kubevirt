package kubevirt

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/spec/shellquote"
)

// port_forward_supervisor_runtime_test.go pins opencharly/plugin-kubevirt#14 by RUNNING the
// PRODUCTION supervisor (`portForwardSupervisorCmd`) — not a hand-written replica — against
// a fake "virtctl" whose listener dies while the process stays alive, and asserting the
// generated loop actually RESTARTS it and that Stop's group-kill leaves the supervisor dead.

func TestPortForwardSupervisorCmd_HealthChecksAndRestarts(t *testing.T) {
	script := portForwardSupervisorCmd("/usr/bin/virtctl",
		[]string{"port-forward", "--context", "vm-ctx", "vm/myvm/default", "45189:22"},
		"/state/port-forward.pid", "/state/port-forward.log", 45189, 20)
	if !strings.Contains(script, "45189") || !strings.Contains(script, "LISTEN") {
		t.Errorf("supervisor must health-check the local port; got: %s", script)
	}
	if !strings.Contains(script, "vm/myvm/default") || !strings.Contains(script, "45189:22") {
		t.Errorf("supervisor must run the portForwardArgv; got: %s", script)
	}
	if !strings.Contains(script, "port-forward.pid") || !strings.Contains(script, "setsid") {
		t.Errorf("supervisor must write the pidfile and setsid-detach; got: %s", script)
	}
}

// TestPortForwardSupervisorCmd_RuntimeRestartAndGroupKill runs the REAL production script.
func TestPortForwardSupervisorCmd_RuntimeRestartAndGroupKill(t *testing.T) {
	for _, b := range []string{"sh", "ss", "python3"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("no %s", b)
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "port-forward.pid")
	logFile := filepath.Join(dir, "port-forward.log")
	starts := filepath.Join(dir, "starts")
	fake := filepath.Join(dir, "fake-virtctl")

	// the fake "virtctl": record each start, bind the port, drop the listener while staying alive
	py := "import socket,time\n" +
		"open(" + pyStr(starts) + ",'a').write('start\\n')\n" +
		"s=socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)\n" +
		"s.bind((\"127.0.0.1\", " + strconv.Itoa(port) + ")); s.listen(1)\n" +
		"time.sleep(3)\n" +
		"s.close()\n" +
		"time.sleep(120)\n"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec python3 -c "+shellquote.ShellQuote(py)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// the PRODUCTION supervisor, with a short listen timeout for the test
	script := portForwardSupervisorCmd(fake, []string{}, pidFile, logFile, port, 3)
	if err := exec.Command("sh", "-c", script).Run(); err != nil {
		t.Fatalf("start supervisor: %v", err)
	}
	time.Sleep(14 * time.Second)
	raw, _ := os.ReadFile(starts)
	if n := len(strings.Fields(string(raw))); n < 2 {
		t.Errorf("the production supervisor must RESTART a dead listener; starts=%d (want >=2)", n)
	}

	pidRaw, _ := os.ReadFile(pidFile)
	spid := strings.TrimSpace(string(pidRaw))
	if spid == "" {
		t.Fatal("no supervisor pid written")
	}
	// Stop semantics: the group-kill must take the supervisor down
	_ = exec.Command("sh", "-c", "kill -TERM -"+spid+" 2>/dev/null; kill -TERM "+spid+" 2>/dev/null").Run()
	time.Sleep(3 * time.Second)
	if exec.Command("sh", "-c", "kill -0 "+spid+" 2>/dev/null").Run() == nil {
		t.Errorf("Stop must kill the supervisor %s", spid)
	}
	_ = exec.Command("sh", "-c", "pkill -f "+shellquote.ShellQuote(fake)+" 2>/dev/null").Run()
}

func pyStr(s string) string { return strconv.Quote(s) }
