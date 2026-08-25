// Local-device service discovery: what the bounded connect sweep reports, and
// how a service found on a device turns back into a target.

package diagnostic

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// stubDial answers each port from a table: nil error means the port accepted,
// and anything else is how it declined. Ports absent from the table time out
// the way a silent host does.
func stubDial(t *testing.T, byPort map[int]error) func(context.Context, string, string) (net.Conn, error) {
	t.Helper()
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		_, portText, err := net.SplitHostPort(addr)
		if err != nil {
			t.Errorf("dialed %q, which is not host:port", addr)
			return nil, err
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			t.Errorf("dialed non-numeric port %q", portText)
			return nil, err
		}
		reply, ok := byPort[port]
		if !ok {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if reply != nil {
			return nil, reply
		}
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}
}

func openPorts(scan ServiceScan) []int {
	ports := make([]int, len(scan.Open))
	for i, svc := range scan.Open {
		ports[i] = svc.Port
	}
	return ports
}

func TestDiscoverServices(t *testing.T) {
	services := []LocalService{
		{Port: 22, Name: "SSH", Scheme: "ssh"},
		{Port: 80, Name: "HTTP", Scheme: "http"},
		{Port: 443, Name: "HTTPS", Scheme: "https"},
		{Port: 631, Name: "IPP"},
	}
	refused := &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}
	cases := []struct {
		name         string
		byPort       map[int]error
		wantOpen     []int
		wantRefused  int
		wantSilent   int
		wantAnswered bool // Refused > 0: the device proved it is still there
	}{
		{
			name:     "several services answer",
			byPort:   map[int]error{22: nil, 80: nil, 443: refused, 631: nil},
			wantOpen: []int{22, 80, 631}, wantRefused: 1, wantAnswered: true,
		},
		{
			name:     "one service answers",
			byPort:   map[int]error{631: nil, 22: refused, 80: refused, 443: refused},
			wantOpen: []int{631}, wantRefused: 3, wantAnswered: true,
		},
		{
			// The device is on the network and simply offers nothing on this
			// list, which is a different answer from having vanished.
			name:   "nothing listening but the device answers",
			byPort: map[int]error{22: refused, 80: refused, 443: refused, 631: refused},
			// no open ports
			wantRefused: 4, wantAnswered: true,
		},
		{
			// Every port silent: a device that went away and one that drops
			// unsolicited traffic are indistinguishable from here.
			name:       "device says nothing at all",
			byPort:     map[int]error{},
			wantSilent: 4,
		},
		{
			name:   "unreachable is not refused",
			byPort: map[int]error{22: errors.New("network is unreachable"), 80: errors.New("no route to host"), 443: errors.New("x"), 631: errors.New("x")},
			// A non-refusal is not evidence the device answered.
			wantSilent: 4,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := &netops{dialContext: stubDial(t, c.byPort)}
			scan := o.discoverServices(context.Background(), "192.0.2.10", services, 200*time.Millisecond)
			if got := openPorts(scan); !slices.Equal(got, c.wantOpen) {
				t.Errorf("open ports = %v, want %v", got, c.wantOpen)
			}
			if scan.Refused != c.wantRefused || scan.Silent != c.wantSilent {
				t.Errorf("refused/silent = %d/%d, want %d/%d", scan.Refused, scan.Silent, c.wantRefused, c.wantSilent)
			}
			if scan.Checked() != len(services) {
				t.Errorf("checked = %d, want every one of the %d ports accounted for", scan.Checked(), len(services))
			}
			if answered := scan.Refused > 0; answered != c.wantAnswered {
				t.Errorf("device answered = %v, want %v", answered, c.wantAnswered)
			}
		})
	}
}

// The open list is a list a cursor sits on, so two runs of the same scan have
// to produce the same order however the network staggers its replies.
func TestDiscoverServicesSortsOpenPortsAscending(t *testing.T) {
	services := []LocalService{{Port: 9100, Name: "JetDirect"}, {Port: 80, Name: "HTTP"}, {Port: 631, Name: "IPP"}}
	o := &netops{dialContext: stubDial(t, map[int]error{80: nil, 631: nil, 9100: nil})}
	scan := o.discoverServices(context.Background(), "192.0.2.10", services, time.Second)
	if got := openPorts(scan); !slices.Equal(got, []int{80, 631, 9100}) {
		t.Errorf("open ports = %v, want them in ascending port order", got)
	}
}

// A device that never answers must still finish inside the budget rather than
// leaving the caller with a pane that spins forever.
func TestDiscoverServicesIsBounded(t *testing.T) {
	services := []LocalService{{Port: 22, Name: "SSH"}, {Port: 80, Name: "HTTP"}}
	o := &netops{dialContext: stubDial(t, map[int]error{})}
	start := time.Now()
	scan := o.discoverServices(context.Background(), "192.0.2.10", services, 150*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("scan took %v, want it bounded by its own timeout", elapsed)
	}
	if scan.Silent != len(services) {
		t.Errorf("silent = %d, want all %d ports", scan.Silent, len(services))
	}
}

// The list is the whole safety argument for this step: short, fixed, and made
// only of ports an ordinary client would dial on its own.
func TestLocalServicesTableStaysNarrow(t *testing.T) {
	if len(localServices) > 20 {
		t.Errorf("localServices has %d ports; this is a service chooser, not a port scanner", len(localServices))
	}
	seen := map[int]bool{}
	last := 0
	for _, svc := range localServices {
		switch {
		case svc.Port < 1 || svc.Port > 65535:
			t.Errorf("port %d is not a port", svc.Port)
		case seen[svc.Port]:
			t.Errorf("port %d is listed twice", svc.Port)
		case svc.Port < last:
			t.Errorf("port %d is out of order after %d; the list is read top to bottom", svc.Port, last)
		case svc.Name == "":
			t.Errorf("port %d has no service name", svc.Port)
		}
		seen[svc.Port], last = true, svc.Port
		// A scheme is a promise that netdoc has probes for that protocol.
		switch svc.Scheme {
		case "", "http", "https", "ssh":
		default:
			t.Errorf("port %d carries scheme %q, which ParseTarget does not accept", svc.Port, svc.Scheme)
		}
	}
}

// Every service has to survive the round trip back into a target, since that
// is how a chosen service reaches the probe DAG at all.
func TestLocalServiceTargetParsesBackToTheSameEndpoint(t *testing.T) {
	cases := []struct {
		host      string
		svc       LocalService
		wantRaw   string
		wantProto Proto
	}{
		{"192.168.1.23", LocalService{Port: 631, Name: "IPP"}, "192.168.1.23:631", ProtoNone},
		{"192.168.1.23", LocalService{Port: 9100, Name: "JetDirect"}, "192.168.1.23:9100", ProtoNone},
		{"192.168.1.1", LocalService{Port: 80, Name: "HTTP", Scheme: "http"}, "http://192.168.1.1:80", ProtoHTTP},
		{"192.168.1.1", LocalService{Port: 8080, Name: "HTTP (alt)", Scheme: "http"}, "http://192.168.1.1:8080", ProtoHTTP},
		{"192.168.1.1", LocalService{Port: 8443, Name: "HTTPS (alt)", Scheme: "https"}, "https://192.168.1.1:8443", ProtoTLSHTTP},
		{"192.168.1.5", LocalService{Port: 22, Name: "SSH", Scheme: "ssh"}, "ssh://192.168.1.5:22", ProtoSSH},
		{"fd00::5", LocalService{Port: 445, Name: "SMB"}, "[fd00::5]:445", ProtoNone},
		{"fd00::5", LocalService{Port: 443, Name: "HTTPS", Scheme: "https"}, "https://[fd00::5]:443", ProtoTLSHTTP},
	}
	for _, c := range cases {
		t.Run(c.wantRaw, func(t *testing.T) {
			raw := c.svc.Target(c.host)
			if raw != c.wantRaw {
				t.Fatalf("Target = %q, want %q", raw, c.wantRaw)
			}
			target, err := ParseTarget(raw)
			if err != nil {
				t.Fatalf("ParseTarget(%q): %v", raw, err)
			}
			if target.Host != c.host || target.Port != c.svc.Port || !target.PortExplicit {
				t.Errorf("target = %s:%d (explicit %v), want %s:%d explicit", target.Host, target.Port, target.PortExplicit, c.host, c.svc.Port)
			}
			if target.Proto != c.wantProto {
				t.Errorf("proto = %s, want %s; the service found is what picks the protocol rows", target.Proto, c.wantProto)
			}
		})
	}
}
