package peer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

type memoryConn struct {
	bytes.Buffer
	deadline time.Time
}

func (conn *memoryConn) Close() error                         { return nil }
func (conn *memoryConn) LocalAddr() net.Addr                  { return testAddr("127.0.0.1:1") }
func (conn *memoryConn) RemoteAddr() net.Addr                 { return testAddr("127.0.0.1:2") }
func (conn *memoryConn) SetDeadline(deadline time.Time) error { conn.deadline = deadline; return nil }
func (conn *memoryConn) SetReadDeadline(deadline time.Time) error {
	conn.deadline = deadline
	return nil
}
func (conn *memoryConn) SetWriteDeadline(deadline time.Time) error {
	conn.deadline = deadline
	return nil
}

type testAddr string

func (address testAddr) Network() string { return "tcp" }
func (address testAddr) String() string  { return string(address) }

func encodedByte(size int, value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, size))
}

func validProbeMessage() wireMessage {
	return wireMessage{
		Version: ProtocolVersion, Type: "probe", Token: encodedByte(tokenSize, 1),
		Nonce: encodedByte(nonceSize, 2), Challenge: encodedByte(payloadSize, 3),
	}
}

func framedJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 4+len(payload))
	// #nosec G115 -- json.Marshal of this fixed test value is far below uint32.
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	return frame
}

func TestProtocolMessageRoundTrip(t *testing.T) {
	want := validProbeMessage()
	conn := &memoryConn{}
	if err := writeMessage(context.Background(), conn, time.Second, want); err != nil {
		t.Fatal(err)
	}
	got, err := readMessage(context.Background(), conn, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("message = %+v, want %+v", got, want)
	}
}

func TestProtocolRejectsMalformedFrames(t *testing.T) {
	invalidUTF8 := framedJSON(t, wireMessage{
		Version: ProtocolVersion, Type: "hello_ok", Nonce: encodedByte(nonceSize, 2),
		Challenge: encodedByte(payloadSize, 3), Name: "x",
	})
	invalidUTF8[bytes.LastIndex(invalidUTF8, []byte(`"x"`))+1] = 0xff
	tests := []struct {
		name  string
		frame []byte
		want  error
	}{
		{"truncated header", []byte{0, 0}, io.ErrUnexpectedEOF},
		{"empty", []byte{0, 0, 0, 0}, errInvalidMessage},
		{"oversized", []byte{0, 1, 0, 0}, errInvalidMessage},
		{"truncated payload", []byte{0, 0, 0, 2, '{'}, io.ErrUnexpectedEOF},
		{"unknown field", framedJSON(t, map[string]any{"version": 1, "type": "done", "surprise": true}), errInvalidMessage},
		{"invalid UTF-8", invalidUTF8, errInvalidMessage},
		{"wrong version", framedJSON(t, map[string]any{"version": 2, "type": "done"}), errVersionMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := &memoryConn{}
			_, _ = conn.Write(test.frame)
			_, err := readMessage(context.Background(), conn, time.Second)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestProtocolReadObeysCanceledContext(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readMessage(ctx, client, time.Second); err == nil {
		t.Fatal("read on canceled context returned no error")
	}
}

func TestProtocolReadObeysExpiredDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	if _, err := readMessage(ctx, client, time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read error = %v, want deadline exceeded", err)
	}
}

func TestPairingRoundTripAndVersionHandling(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	want := pairing{
		Expires: now.Add(time.Minute),
		Endpoints: []wireEndpoint{
			{Address: "192.0.2.10:4242", Family: FamilyIPv4},
			{Address: "[2001:db8::10]:4242", Family: FamilyIPv6},
		},
	}
	copy(want.Pin[:], bytes.Repeat([]byte{4}, sha256.Size))
	copy(want.Token[:], bytes.Repeat([]byte{5}, tokenSize))
	code, err := encodePairing(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePairing(code, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Expires != want.Expires || !bytes.Equal(got.Pin[:], want.Pin[:]) || !bytes.Equal(got.Token[:], want.Token[:]) || len(got.Endpoints) != 2 {
		t.Fatalf("decoded pairing = %+v", got)
	}
	if _, err := decodePairing(strings.Replace(code, pairingPrefix, "ndp2.", 1), now); !errors.Is(err, errInvalidPairing) {
		t.Fatalf("new protocol prefix error = %v, want invalid pairing", err)
	}
	if _, err := decodePairing(code, want.Expires); !errors.Is(err, errExpiredPairing) {
		t.Fatalf("stale pairing error = %v, want expired", err)
	}
}

func TestPairingRejectsInvalidEndpoints(t *testing.T) {
	for _, address := range []string{"", ":0", "0.0.0.0:1", "255.255.255.255:1", "example.com:1", "127.0.0.1", "127.0.0.1:65536"} {
		if _, _, err := NormalizeListenAddress(address); err == nil {
			t.Errorf("NormalizeListenAddress(%q) returned no error", address)
		}
	}
	if address, family, err := NormalizeListenAddress("[2001:db8::1]:0"); err != nil || address != "[2001:db8::1]:0" || family != FamilyIPv6 {
		t.Fatalf("IPv6 listen address = %q, %q, %v", address, family, err)
	}
}

func TestAuthenticatorRejectsWrongAndReplayedCredentials(t *testing.T) {
	var token [tokenSize]byte
	copy(token[:], bytes.Repeat([]byte{7}, tokenSize))
	auth := authenticator{token: token}
	nonce := encodedByte(nonceSize, 8)
	if err := auth.authenticate(encodedByte(tokenSize, 9), nonce); !errors.Is(err, errAuthentication) {
		t.Fatalf("wrong token error = %v", err)
	}
	if err := auth.authenticate(encodedByte(tokenSize, 7), nonce); err != nil {
		t.Fatalf("valid auth: %v", err)
	}
	if err := auth.authenticate(encodedByte(tokenSize, 7), nonce); !errors.Is(err, errReplay) {
		t.Fatalf("replay error = %v, want replay", err)
	}
}

func TestAuthenticatorBoundsExchanges(t *testing.T) {
	var token [tokenSize]byte
	auth := authenticator{token: token}
	encodedToken := encodeSecret(token[:])
	for i := range maxConnections {
		if err := auth.authenticate(encodedToken, encodedByte(nonceSize, byte(i))); err != nil {
			t.Fatalf("exchange %d: %v", i, err)
		}
	}
	if err := auth.authenticate(encodedToken, encodedByte(nonceSize, 0xff)); !errors.Is(err, errTooManyExchanges) {
		t.Fatalf("exchange beyond cap = %v", err)
	}
}

func TestCertificatePinAuthenticatesTheExpectedPeerOnly(t *testing.T) {
	certificate := &x509.Certificate{Raw: []byte("one ephemeral certificate")}
	pin := sha256.Sum256(certificate.Raw)
	verify := clientTLSConfig(pin).VerifyConnection
	if err := verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}); err != nil {
		t.Fatalf("correct pin: %v", err)
	}
	wrong := sha256.Sum256([]byte("another certificate"))
	if err := clientTLSConfig(wrong).VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}); !errors.Is(err, errAuthentication) {
		t.Fatalf("wrong pin error = %v", err)
	}
}

func TestErrorsDoNotContainPairingSecrets(t *testing.T) {
	secret := encodedByte(tokenSize, 0x5a)
	message := validProbeMessage()
	message.Token = secret
	message.Nonce = "bad"
	if err := validateMessage(message); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("validation error = %q, want safe error", err)
	}
	observation := Observation{
		Direction: DirectionConnectorToListener, Family: FamilyIPv4,
		Status: "FAIL", Cause: "\x1b]52;c;aGk=\a", Destination: "127.0.0.1:1",
	}
	if err := validateObservation(observation); err == nil {
		t.Fatal("peer-controlled cause outside the fixed vocabulary was accepted")
	}
}

func FuzzDecodeMessage(f *testing.F) {
	payload, _ := json.Marshal(map[string]any{"version": 1, "type": "done"})
	frame := make([]byte, 4+len(payload))
	// #nosec G115 -- json.Marshal of this fixed fuzz seed is far below uint32.
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	f.Add(frame)
	f.Add([]byte{0, 0, 0, 1, '{'})
	pairingCode, _ := encodePairing(pairing{
		Expires:   time.Unix(1_800_000_060, 0),
		Endpoints: []wireEndpoint{{Address: "127.0.0.1:1", Family: FamilyIPv4}},
	})
	f.Add([]byte(pairingCode))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxMessageSize+4 {
			data = data[:maxMessageSize+4]
		}
		conn := &memoryConn{}
		_, _ = conn.Write(data)
		_, _ = readMessage(context.Background(), conn, time.Second)
		_, _ = decodePairing(string(data), time.Unix(1_800_000_000, 0))
	})
}
