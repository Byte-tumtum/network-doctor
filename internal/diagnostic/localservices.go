// Local-device service discovery: the step between "this device answered on
// the network" and "diagnose this endpoint", so a device can be picked without
// the person picking it having to already know which port to name.

package diagnostic

import (
	"cmp"
	"context"
	"net"
	"slices"
	"strconv"
	"time"
)

// LocalService is one TCP port worth offering as a diagnosis target on a
// device found on the local network.
//
// Name is the registered name of the port and nothing else. It says which
// service conventionally listens there, never what the device is: a printer,
// a router and a laptop all answer on 80, and a connect tells them apart not
// at all, so nothing here tries to.
//
// Scheme is the target scheme to hand ParseTarget when this service is the
// one chosen, and it is set only for the protocols netdoc has a real probe
// for. Empty means the diagnosis stops at the TCP rung, which is the honest
// place to stop for a protocol netdoc cannot speak.
type LocalService struct {
	Port   int
	Name   string
	Scheme string
}

// Target renders this service on host the way a person would type it, so the
// choice goes through the same ParseTarget every other target does. The scheme
// is carried where there is one, which is what makes an admin page on 8080 an
// HTTP target instead of an anonymous port.
func (s LocalService) Target(host string) string {
	endpoint := net.JoinHostPort(host, strconv.Itoa(s.Port))
	if s.Scheme == "" {
		return endpoint
	}
	return s.Scheme + "://" + endpoint
}

// localServices is the fixed list DiscoverServices dials: the services a
// person actually tries to reach on a device on their own network, one
// connection each. It is deliberately short and deliberately fixed. Widening
// it turns a two-second question about one device into a port scan, which is a
// different operation with different consequences, and netdoc already offers
// one of those behind its own confirmation gate.
var localServices = []LocalService{
	{Port: 21, Name: "FTP"},
	{Port: 22, Name: "SSH", Scheme: "ssh"},
	{Port: 23, Name: "Telnet"},
	{Port: 53, Name: "DNS"},
	{Port: 80, Name: "HTTP", Scheme: "http"},
	{Port: 443, Name: "HTTPS", Scheme: "https"},
	{Port: 445, Name: "SMB"},
	{Port: 515, Name: "LPD"},
	{Port: 631, Name: "IPP"},
	{Port: 2049, Name: "NFS"},
	{Port: 3389, Name: "RDP"},
	{Port: 5900, Name: "VNC"},
	{Port: 8080, Name: "HTTP (alt)", Scheme: "http"},
	{Port: 8443, Name: "HTTPS (alt)", Scheme: "https"},
	{Port: 9100, Name: "JetDirect"},
}

// ServiceScan is what one device said when its common service ports were
// tried: the ports that accepted a connection, and a count of how the rest
// declined.
//
// The two ways of declining are kept apart because they answer different
// questions. Refused means something answered for that address, which is
// evidence the device is switched on and reachable even when nothing useful is
// listening. Silent means nothing came back inside the budget, and a device
// that is powered off, gone from the network, or simply dropping unsolicited
// traffic all look identical from here, so nothing downstream may claim to
// tell them apart.
type ServiceScan struct {
	Open    []LocalService
	Refused int
	Silent  int
}

// Checked is how many ports the scan tried.
func (s ServiceScan) Checked() int { return len(s.Open) + s.Refused + s.Silent }

// DiscoverServices reports which of a short fixed list of common service ports
// host accepts connections on, bounded by timeout (DefaultProbeTimeout when
// non-positive). Every port on the list is one an ordinary client would dial
// anyway; each is dialed once, all of them at once, and a socket that opens is
// closed without a byte being sent or read.
//
// It is not a port scan and must not become one: one host, one pass, one
// connection per port.
func DiscoverServices(ctx context.Context, sources *SourceAddresses, host string, timeout time.Duration) ServiceScan {
	o := defaultOps
	if sources != nil {
		o = opsFromSources(sources)
	}
	return o.discoverServices(ctx, host, localServices, timeout)
}

func (o *netops) discoverServices(ctx context.Context, host string, services []LocalService, timeout time.Duration) ServiceScan {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type reply struct {
		svc     LocalService
		open    bool
		refused bool
	}
	// Buffered for every dial: a scan abandoned by its context must not leave
	// goroutines parked on a channel nobody is reading any more.
	replies := make(chan reply, len(services))
	for _, svc := range services {
		go func(svc LocalService) {
			conn, err := o.dialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(svc.Port)))
			if err != nil {
				replies <- reply{svc: svc, refused: isConnectionRefused(err)}
				return
			}
			_ = conn.Close()
			replies <- reply{svc: svc, open: true}
		}(svc)
	}

	var scan ServiceScan
	for range services {
		switch r := <-replies; {
		case r.open:
			scan.Open = append(scan.Open, r.svc)
		case r.refused:
			scan.Refused++
		default:
			scan.Silent++
		}
	}
	// Replies arrive in whatever order the network answers, and a list that
	// reorders itself between two runs of the same scan is not a list anyone
	// can point a cursor at.
	slices.SortFunc(scan.Open, func(a, b LocalService) int { return cmp.Compare(a.Port, b.Port) })
	return scan
}
