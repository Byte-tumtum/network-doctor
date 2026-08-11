package simulation

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Evidence is simulator-owned proof collected from services inside node
// namespaces. It complements netdoc's report; it is never used to manufacture
// a diagnostic result.
type Evidence struct {
	DNS              []DNSEvidence             `json:"dns"`
	DNSQueries       []DNSQueryEvidence        `json:"dns_queries"`
	SOCKSRequests    []SOCKSEvidence           `json:"socks_requests"`
	TLS              []TLSEvidence             `json:"tls"`
	ServiceStates    []ServiceStateEvidence    `json:"service_states"`
	ServiceReplies   []ServiceReplyEvidence    `json:"service_replies"`
	TCPResets        []TCPResetEvidence        `json:"tcp_resets"`
	PacketConditions []PacketConditionEvidence `json:"packet_conditions"`
	PacketDrops      []PacketDropEvidence      `json:"packet_drops"`
	Links            []LinkEvidence            `json:"links"`
	Routes           []RouteEvidence           `json:"routes"`
	Routers          []RouterEvidence          `json:"routers"`
	Reachability     []ReachabilityEvidence    `json:"reachability"`
	// FamilyReachability is measured, never derived. See its type comment.
	FamilyReachability []FamilyReachabilityEvidence `json:"family_reachability"`
}

type PacketConditionEvidence struct {
	Node           string        `json:"node"`
	Segment        string        `json:"segment"`
	Latency        time.Duration `json:"latency_ms,omitempty"`
	Jitter         time.Duration `json:"jitter_ms,omitempty"`
	LossPercent    float64       `json:"loss_percent,omitempty"`
	Seed           uint32        `json:"seed,omitempty"`
	Active         bool          `json:"active"`
	DroppedPackets uint64        `json:"dropped_packets"`
	ObservedMinRTT time.Duration `json:"observed_min_rtt_ms,omitempty"`
	ObservedMaxRTT time.Duration `json:"observed_max_rtt_ms,omitempty"`
	RTTSamples     int           `json:"rtt_samples"`
}

// PacketDropEvidence is the kernel's own count of the packets one drop fault's
// rule matched, read back from that rule's nftables counter once the run ended.
// A rule that was installed but never matched anything reports zero: a fault
// that was injected and a fault that took effect are different claims, and only
// the counter can tell them apart.
type PacketDropEvidence struct {
	Node      string `json:"node"`
	Family    string `json:"family,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Port      int    `json:"port,omitempty"`
	To        string `json:"to,omitempty"`
	Direction string `json:"direction"`
	Packets   uint64 `json:"packets"`
}

// LinkEvidence describes one actual namespace interface using its logical
// segment name. Kernel implementation names are intentionally absent.
type LinkEvidence struct {
	Node    string `json:"node"`
	Segment string `json:"segment"`
	Address string `json:"address"`
	IPv4    string `json:"ipv4,omitempty"`
	IPv6    string `json:"ipv6,omitempty"`
	Up      bool   `json:"up"`
}

// RouteEvidence combines the validated route with the kernel's selected path.
// GatewayReachable is omitted when no neighbor observation was available.
type RouteEvidence struct {
	Node             string `json:"node"`
	Destination      string `json:"destination"`
	Via              string `json:"via,omitempty"`
	Segment          string `json:"segment"`
	Metric           int    `json:"metric"`
	Family           string `json:"family,omitempty"`
	Selected         bool   `json:"selected"`
	Source           string `json:"source,omitempty"`
	GatewayReachable *bool  `json:"gateway_reachable,omitempty"`
}

type RouterEvidence struct {
	Node           string `json:"node"`
	IPv4Forwarding bool   `json:"ipv4_forwarding"`
	IPv6Forwarding bool   `json:"ipv6_forwarding"`
}

type ReachabilityEvidence struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Family    string   `json:"family,omitempty"`
	Via       []string `json:"via"`
	Reachable bool     `json:"reachable"`
}

// Address family states a FamilyReachabilityEvidence can carry. Unavailable and
// unreachable are deliberately distinct: a family the node was never given an
// address in was not tested, which is not the same claim as a family that was
// dialed and did not answer.
const (
	FamilyStateReachable   = "reachable"
	FamilyStateUnreachable = "unreachable"
	FamilyStateUnavailable = "unavailable"
)

// FamilyReachabilityEvidence is the simulator's own point-in-time answer to one
// question: from inside this node's namespace, does a TCP connection to the
// controlled endpoints of this address family complete?
//
// It is a state rather than a bool because there are three outcomes, and it is
// its own type rather than a ReachabilityEvidence so that the only way to fill
// it in is to dial. The single producer is the node holder, which never sees
// netdoc's report; anything derived from a diagnosis, a scenario expectation or
// a fault record belongs somewhere else. Absence of a record for a family is
// not "unavailable" — it means no observation was taken at all.
type FamilyReachabilityEvidence struct {
	Node   string   `json:"node"`
	Family string   `json:"family"`
	Target string   `json:"target,omitempty"`
	Via    []string `json:"via,omitempty"`
	State  string   `json:"state"`
}

// DNSEvidence aggregates identical queries observed by a DNS service.
type DNSEvidence struct {
	Node      string `json:"node"`
	Service   string `json:"service,omitempty"`
	Source    string `json:"source"`
	Name      string `json:"name"`
	QueryType string `json:"query_type"`
	Result    string `json:"result"`
	Count     int    `json:"count"`
}

// DNSQueryEvidence preserves scheduled query order rather than aggregating it.
// Sequence is scoped to service, queried name, and query type.
type DNSQueryEvidence struct {
	Node             string `json:"node"`
	Service          string `json:"service"`
	Source           string `json:"source"`
	Name             string `json:"name"`
	QueryType        string `json:"query_type"`
	Sequence         int    `json:"sequence"`
	ScheduledOutcome string `json:"scheduled_outcome"`
	ActualOutcome    string `json:"actual_outcome"`
	// Offset places the query on the fault timeline, relative to T0. It is
	// filled in by the director once the run's epoch is known.
	Offset  time.Duration `json:"offset_ms"`
	DelayMs int64         `json:"delay_ms,omitempty"`
	at      time.Time
}

type TCPResetEvidence struct {
	Node    string `json:"node"`
	Service string `json:"service,omitempty"`
	Event   string `json:"event"`
	Result  string `json:"result"`
	Count   int    `json:"count"`
}

// SOCKSEvidence aggregates protocol events observed by a SOCKS service.
// A greeting proves proxy reachability even when local DNS fails before the
// client can send a CONNECT request.
type SOCKSEvidence struct {
	Node        string `json:"node"`
	Service     string `json:"service,omitempty"`
	Event       string `json:"event"`
	AddressType string `json:"address_type,omitempty"`
	Destination string `json:"destination,omitempty"`
	Port        int    `json:"port,omitempty"`
	Result      string `json:"result"`
	Count       int    `json:"count"`
}

// TLSEvidence aggregates handshakes observed by a simulator TLS service. It
// contains certificate metadata only; private keys never enter the recorder.
type TLSEvidence struct {
	Node                 string    `json:"node"`
	Service              string    `json:"service"`
	CertificateMode      string    `json:"certificate_mode"`
	RequestedServer      string    `json:"requested_server,omitempty"`
	CertificateDNS       []string  `json:"certificate_dns"`
	NotBefore            time.Time `json:"not_before"`
	NotAfter             time.Time `json:"not_after"`
	CertificatePresented bool      `json:"certificate_presented"`
	Result               string    `json:"result"`
	Count                int       `json:"count"`
}

// ServiceStateEvidence records the mode of a successfully started controlled
// service. It is emitted by the node holder, not copied into the report from a
// hunt manifest or diagnosis.
type ServiceStateEvidence struct {
	Node    string `json:"node"`
	Service string `json:"service,omitempty"`
	Type    string `json:"type"`
	Port    int    `json:"port"`
	Mode    string `json:"mode,omitempty"`
	Status  int    `json:"status,omitempty"`
}

// ServiceReplyEvidence counts the replies a controlled service actually sent,
// in the shape it sent them. It is the companion to ServiceStateEvidence and
// deliberately not the same record: a service that came up in a faulty mode has
// a state, but until a client reaches it and it answers, nothing was done to
// anyone. Only a reply proves the fault reached the wire.
type ServiceReplyEvidence struct {
	Node    string `json:"node"`
	Service string `json:"service,omitempty"`
	Type    string `json:"type"`
	Port    int    `json:"port"`
	Status  int    `json:"status,omitempty"`
	Result  string `json:"result"`
	Count   int    `json:"count"`
}

const (
	evidenceServiceState = "service_state"
	evidenceServiceReply = "service_reply"
	// replyResponded is the reply result of a service that answered normally,
	// whatever the answer said. A faulty mode names itself instead.
	replyResponded = "responded"
)

type evidenceEvent struct {
	Kind             string `json:"kind"`
	Node             string `json:"node"`
	Service          string `json:"service,omitempty"`
	Name             string `json:"name,omitempty"`
	Source           string `json:"source,omitempty"`
	QueryType        string `json:"query_type,omitempty"`
	Event            string `json:"event,omitempty"`
	AddressType      string `json:"address_type,omitempty"`
	Destination      string `json:"destination,omitempty"`
	Port             int    `json:"port,omitempty"`
	Result           string `json:"result"`
	Sequence         int    `json:"sequence,omitempty"`
	ScheduledOutcome string `json:"scheduled_outcome,omitempty"`
	ActualOutcome    string `json:"actual_outcome,omitempty"`
	DelayMs          int64  `json:"delay_ms,omitempty"`
	// At is the wall clock the holder observed the event at. Holders and the
	// director share one machine's clock, which is the only thing they can
	// correlate across processes; it never orders the fault scheduler, which
	// runs off a single monotonic epoch.
	At time.Time `json:"at"`

	CertificateMode      string    `json:"certificate_mode,omitempty"`
	RequestedServer      string    `json:"requested_server,omitempty"`
	CertificateDNS       []string  `json:"certificate_dns,omitempty"`
	NotBefore            time.Time `json:"not_before,omitempty"`
	NotAfter             time.Time `json:"not_after,omitempty"`
	CertificatePresented bool      `json:"certificate_presented,omitempty"`
	ServiceType          string    `json:"service_type,omitempty"`
	ServicePort          int       `json:"service_port,omitempty"`
	ServiceMode          string    `json:"service_mode,omitempty"`
	ServiceStatus        int       `json:"service_status,omitempty"`
}

// evidenceRecorder serializes events from concurrent service goroutines into
// one JSONL file owned by the node holder. The director only reads it after a
// netdoc process has exited.
type evidenceRecorder struct {
	mu     sync.Mutex
	node   string
	file   *os.File
	err    error
	failed chan error
}

func openEvidenceRecorder(path, node string) (*evidenceRecorder, error) {
	if path == "" {
		return &evidenceRecorder{node: node}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &evidenceRecorder{node: node, file: f, failed: make(chan error, 1)}, nil
}

func (r *evidenceRecorder) record(event evidenceEvent) error {
	if r == nil || r.file == nil {
		return nil
	}
	event.Node = r.node
	if event.At.IsZero() {
		event.At = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if err := json.NewEncoder(r.file).Encode(event); err != nil {
		r.err = fmt.Errorf("record evidence for node %q: %w", r.node, err)
		select {
		case r.failed <- r.err:
		default:
		}
	}
	return r.err
}

func (r *evidenceRecorder) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *evidenceRecorder) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	err := r.err
	if closeErr := r.file.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("finalize evidence recording for node %q: %w", r.node, closeErr))
	}
	return err
}

func readEvidence(paths []string) (Evidence, error) {
	var events []evidenceEvent
	for _, path := range paths {
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Evidence{}, err
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var event evidenceEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				f.Close()
				return Evidence{}, fmt.Errorf("%s: %w", path, err)
			}
			events = append(events, event)
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			return Evidence{}, err
		}
	}
	return aggregateEvidence(events), nil
}

func aggregateEvidence(events []evidenceEvent) Evidence {
	dns := make(map[string]DNSEvidence)
	socks := make(map[string]SOCKSEvidence)
	tlsEvents := make(map[string]TLSEvidence)
	resets := make(map[string]TCPResetEvidence)
	replies := make(map[string]ServiceReplyEvidence)
	for _, event := range events {
		switch event.Kind {
		case ServiceDNS:
			key := event.Node + "\x00" + event.Service + "\x00" + event.Source + "\x00" + event.Name + "\x00" + event.QueryType + "\x00" + event.Result
			item := dns[key]
			item.Node, item.Service, item.Name = event.Node, event.Service, event.Name
			item.Source = event.Source
			item.QueryType, item.Result, item.Count = event.QueryType, event.Result, item.Count+1
			dns[key] = item
		case ServiceSOCKS5:
			key := event.Node + "\x00" + event.Service + "\x00" + event.Event + "\x00" + event.AddressType + "\x00" + event.Destination + "\x00" + strconv.Itoa(event.Port) + "\x00" + event.Result
			item := socks[key]
			item.Node, item.Service, item.Event = event.Node, event.Service, event.Event
			item.AddressType, item.Destination, item.Port = event.AddressType, event.Destination, event.Port
			item.Result, item.Count = event.Result, item.Count+1
			socks[key] = item
		case ServiceTLS:
			key := event.Node + "\x00" + event.Service + "\x00" + event.CertificateMode + "\x00" +
				event.RequestedServer + "\x00" + strings.Join(event.CertificateDNS, "\x00") + "\x00" +
				event.NotBefore.UTC().Format(time.RFC3339Nano) + "\x00" + event.NotAfter.UTC().Format(time.RFC3339Nano) +
				"\x00" + strconv.FormatBool(event.CertificatePresented) + "\x00" + event.Result
			item := tlsEvents[key]
			item.Node, item.Service = event.Node, event.Service
			item.CertificateMode, item.RequestedServer = event.CertificateMode, event.RequestedServer
			item.CertificateDNS = append([]string(nil), event.CertificateDNS...)
			item.NotBefore, item.NotAfter = event.NotBefore, event.NotAfter
			item.CertificatePresented, item.Result = event.CertificatePresented, event.Result
			item.Count++
			tlsEvents[key] = item
		case ServiceTCPReset:
			key := event.Node + "\x00" + event.Service + "\x00" + event.Event + "\x00" + event.Result
			item := resets[key]
			item.Node, item.Service, item.Event, item.Result = event.Node, event.Service, event.Event, event.Result
			item.Count++
			resets[key] = item
		case evidenceServiceReply:
			key := strings.Join([]string{event.Node, event.Service, event.ServiceType,
				strconv.Itoa(event.ServicePort), strconv.Itoa(event.ServiceStatus), event.Result}, "\x00")
			item := replies[key]
			item.Node, item.Service, item.Type = event.Node, event.Service, event.ServiceType
			item.Port, item.Status, item.Result = event.ServicePort, event.ServiceStatus, event.Result
			item.Count++
			replies[key] = item
		}
	}
	var out Evidence
	for _, item := range dns {
		out.DNS = append(out.DNS, item)
	}
	for _, item := range socks {
		out.SOCKSRequests = append(out.SOCKSRequests, item)
	}
	for _, item := range tlsEvents {
		out.TLS = append(out.TLS, item)
	}
	for _, event := range events {
		if event.Kind == evidenceServiceState {
			out.ServiceStates = append(out.ServiceStates, ServiceStateEvidence{Node: event.Node, Service: event.Service,
				Type: event.ServiceType, Port: event.ServicePort, Mode: event.ServiceMode, Status: event.ServiceStatus})
		}
		if event.Kind == ServiceDNS && event.Sequence > 0 {
			out.DNSQueries = append(out.DNSQueries, DNSQueryEvidence{Node: event.Node, Service: event.Service,
				Source: event.Source, Name: event.Name, QueryType: event.QueryType, Sequence: event.Sequence,
				ScheduledOutcome: event.ScheduledOutcome, ActualOutcome: event.ActualOutcome,
				DelayMs: event.DelayMs, at: event.At})
		}
	}
	for _, item := range resets {
		out.TCPResets = append(out.TCPResets, item)
	}
	for _, item := range replies {
		out.ServiceReplies = append(out.ServiceReplies, item)
	}
	sort.Slice(out.DNS, func(i, j int) bool {
		a, b := out.DNS[i], out.DNS[j]
		return a.Node+a.Service+a.Source+a.Name+a.QueryType+a.Result < b.Node+b.Service+b.Source+b.Name+b.QueryType+b.Result
	})
	sort.Slice(out.SOCKSRequests, func(i, j int) bool {
		a, b := out.SOCKSRequests[i], out.SOCKSRequests[j]
		return a.Node+a.Service+a.Event+a.AddressType+a.Destination+strconv.Itoa(a.Port)+a.Result <
			b.Node+b.Service+b.Event+b.AddressType+b.Destination+strconv.Itoa(b.Port)+b.Result
	})
	sort.Slice(out.TLS, func(i, j int) bool {
		a, b := out.TLS[i], out.TLS[j]
		return a.Node+a.Service+a.CertificateMode+a.RequestedServer+strings.Join(a.CertificateDNS, "\x00")+a.Result <
			b.Node+b.Service+b.CertificateMode+b.RequestedServer+strings.Join(b.CertificateDNS, "\x00")+b.Result
	})
	sort.Slice(out.ServiceStates, func(i, j int) bool {
		a, b := out.ServiceStates[i], out.ServiceStates[j]
		return strings.Join([]string{a.Node, a.Service, a.Type, strconv.Itoa(a.Port), a.Mode, strconv.Itoa(a.Status)}, "\x00") <
			strings.Join([]string{b.Node, b.Service, b.Type, strconv.Itoa(b.Port), b.Mode, strconv.Itoa(b.Status)}, "\x00")
	})
	sort.Slice(out.ServiceReplies, func(i, j int) bool {
		a, b := out.ServiceReplies[i], out.ServiceReplies[j]
		return strings.Join([]string{a.Node, a.Service, a.Type, strconv.Itoa(a.Port), strconv.Itoa(a.Status), a.Result}, "\x00") <
			strings.Join([]string{b.Node, b.Service, b.Type, strconv.Itoa(b.Port), strconv.Itoa(b.Status), b.Result}, "\x00")
	})
	sort.Slice(out.DNSQueries, func(i, j int) bool {
		a, b := out.DNSQueries[i], out.DNSQueries[j]
		if a.Node+a.Service+a.Name+a.QueryType != b.Node+b.Service+b.Name+b.QueryType {
			return a.Node+a.Service+a.Name+a.QueryType < b.Node+b.Service+b.Name+b.QueryType
		}
		return a.Sequence < b.Sequence
	})
	sort.Slice(out.TCPResets, func(i, j int) bool {
		a, b := out.TCPResets[i], out.TCPResets[j]
		return a.Node+a.Service+a.Event+a.Result < b.Node+b.Service+b.Event+b.Result
	})
	if out.DNS == nil {
		out.DNS = []DNSEvidence{}
	}
	if out.SOCKSRequests == nil {
		out.SOCKSRequests = []SOCKSEvidence{}
	}
	if out.TLS == nil {
		out.TLS = []TLSEvidence{}
	}
	if out.ServiceStates == nil {
		out.ServiceStates = []ServiceStateEvidence{}
	}
	if out.ServiceReplies == nil {
		out.ServiceReplies = []ServiceReplyEvidence{}
	}
	if out.DNSQueries == nil {
		out.DNSQueries = []DNSQueryEvidence{}
	}
	if out.TCPResets == nil {
		out.TCPResets = []TCPResetEvidence{}
	}
	out.PacketConditions = []PacketConditionEvidence{}
	out.PacketDrops = []PacketDropEvidence{}
	out.Links = []LinkEvidence{}
	out.Routes = []RouteEvidence{}
	out.Routers = []RouterEvidence{}
	out.Reachability = []ReachabilityEvidence{}
	out.FamilyReachability = []FamilyReachabilityEvidence{}
	return out
}
