//go:build integration

package peer

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPeerSessionLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listener, err := NewListener(ctx, []string{"127.0.0.1:0"}, Options{Name: "machine-a", Version: "test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	pairingCode := listener.PairingCode()

	type outcome struct {
		result Result
		err    error
	}
	listenerDone := make(chan outcome, 1)
	go func() {
		result, err := listener.Run(ctx)
		listenerDone <- outcome{result, err}
	}()
	connector, err := Connect(ctx, pairingCode, Options{Name: "machine-b", Version: "test", Timeout: time.Second})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	listening := <-listenerDone
	if listening.err != nil {
		t.Fatalf("listen: %v", listening.err)
	}
	for role, result := range map[string]Result{"listener": listening.result, "connector": connector} {
		if !result.OK || result.Diagnosis.ID != DiagnosisBidirectionalOK {
			t.Errorf("%s result = %+v", role, result)
		}
		passes := 0
		for _, observation := range result.Observations {
			if observation.Status == "PASS" {
				passes++
				if !observation.TCPConnected || !observation.TLSAuthenticated || !observation.ApplicationTraffic || observation.PayloadBytes != payloadSize {
					t.Errorf("%s incomplete pass evidence: %+v", role, observation)
				}
			}
		}
		if passes != 2 {
			t.Errorf("%s pass count = %d, want two IPv4 directions", role, passes)
		}
	}
	if connector.Local.Name != "machine-b" || connector.Remote.Name != "machine-a" || listening.result.Local.Name != "machine-a" {
		t.Errorf("endpoint identities: listener %+v, connector %+v", listening.result, connector)
	}

	for role, result := range map[string]Result{"listener": listening.result, "connector": connector} {
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte(pairingCode)) || strings.Contains(string(encoded), strings.TrimPrefix(pairingCode, pairingPrefix)) {
			t.Errorf("%s result leaked pairing credential", role)
		}
	}
}

func TestPeerSessionDualStackLoopback(t *testing.T) {
	preflight, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	_ = preflight.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listener, err := NewListener(ctx, []string{"127.0.0.1:0", "[::1]:0"}, Options{Name: "dual-a", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := listener.Run(ctx)
		done <- err
	}()
	result, err := Connect(ctx, listener.PairingCode(), Options{Name: "dual-b", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !result.OK || len(result.Observations) != 4 {
		t.Fatalf("dual-stack result = %+v", result)
	}
	for _, observation := range result.Observations {
		if observation.Status != "PASS" {
			t.Errorf("dual-stack observation = %+v", observation)
		}
	}
}

func TestWrongCredentialDoesNotConsumeListenerSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listener, err := NewListener(ctx, []string{"127.0.0.1:0"}, Options{Name: "listener", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	wrong, err := decodePairing(listener.PairingCode(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wrong.Token[0] ^= 0xff
	wrongCode, err := encodePairing(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(ctx, wrongCode, Options{Name: "wrong", Timeout: 500 * time.Millisecond}); err == nil {
		t.Fatal("wrong credential connected")
	} else if strings.Contains(err.Error(), encodeSecret(wrong.Token[:])) {
		t.Fatalf("error leaked credential: %v", err)
	}

	listenerDone := make(chan error, 1)
	go func() {
		_, err := listener.Run(ctx)
		listenerDone <- err
	}()
	if _, err := Connect(ctx, listener.PairingCode(), Options{Name: "right", Timeout: time.Second}); err != nil {
		t.Fatalf("valid credential after rejection: %v", err)
	}
	if err := <-listenerDone; err != nil {
		t.Fatalf("listener after rejection: %v", err)
	}
}

func TestListenerCancellationClosesWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := NewListener(ctx, []string{"127.0.0.1:0"}, Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := listener.Run(context.Background()); err == nil {
		t.Fatal("canceled listener returned no error")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionCapClosesListener(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, err := NewListener(ctx, []string{"127.0.0.1:0"}, Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := listener.Run(ctx)
		done <- err
	}()
	for range maxConnections + 1 {
		conn, err := net.DialTimeout("tcp4", listener.Endpoints()[0], time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
	if err := <-done; err == nil {
		t.Fatal("listener stayed open after the connection cap")
	}
}
