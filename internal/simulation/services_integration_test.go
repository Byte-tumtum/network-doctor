//go:build integration

package simulation

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestTCPResetServiceAcceptsThenResets(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := startTCPResetServer([]net.Listener{ln}, "reset", nil)
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp4", ln.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var one [1]byte
		_, err = conn.Read(one[:])
		_ = conn.Close()
		if !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("read error = %v, want ECONNRESET", err)
		}
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp4", ln.Addr().String(), 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("closed listener still accepted a connection")
	}
}
