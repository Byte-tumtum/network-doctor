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
	done := make(chan struct{})
	go func() {
		serveTCPReset(ln, "reset", nil)
		close(done)
	}()
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
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reset listener did not shut down")
	}
	if conn, err := net.DialTimeout("tcp4", ln.Addr().String(), 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("closed listener still accepted a connection")
	}
}
