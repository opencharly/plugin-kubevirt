package kubevirt

import (
	"net"
	"testing"
	"time"
)

// A detached supervisor whose VMI is gone still ACCEPTS a connection to its local port
// and then tunnels nowhere, so `readLivePid` — which only asks "is the process alive" —
// happily reuses it. The next run then spends an entire wait-for-sshd cap talking to a
// dead VM (measured: plugin-kubevirt#32, a 30m0s cap burned on a supervisor ten hours
// old). These tests pin the property that makes the reuse safe: the port must SPEAK
// ssh, not merely be open.
func TestForwardCarriesSSH(t *testing.T) {
	t.Run("a silent listener is NOT a live tunnel", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c // accept and say nothing: exactly what an orphaned supervisor does
			}
		}()
		port := ln.Addr().(*net.TCPAddr).Port
		if forwardCarriesSSH("127.0.0.1", port, 300*time.Millisecond) {
			t.Fatal("a listener that never sends a banner was accepted as a live tunnel: this is the defect that burns the whole ssh wait")
		}
	})

	t.Run("an ssh banner IS a live tunnel", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = c.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n")) // unbuffered: a buffered write here is what made this test lie the first time
				_ = c
			}
		}()
		port := ln.Addr().(*net.TCPAddr).Port
		if !forwardCarriesSSH("127.0.0.1", port, 2*time.Second) {
			t.Fatal("a listener that presents an ssh banner was rejected: the healthy idempotent reuse would break")
		}
	})

	t.Run("nothing listening is NOT a live tunnel", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		if forwardCarriesSSH("127.0.0.1", port, 200*time.Millisecond) {
			t.Fatal("a closed port was accepted as a live tunnel")
		}
	})
}
