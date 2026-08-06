package simulation

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
)

// Evidence is simulator-owned proof collected from services inside node
// namespaces. It complements netdoc's report; it is never used to manufacture
// a diagnostic result.
type Evidence struct {
	DNS           []DNSEvidence   `json:"dns"`
	SOCKSRequests []SOCKSEvidence `json:"socks_requests"`
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

type evidenceEvent struct {
	Kind        string `json:"kind"`
	Node        string `json:"node"`
	Service     string `json:"service,omitempty"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	QueryType   string `json:"query_type,omitempty"`
	Event       string `json:"event,omitempty"`
	AddressType string `json:"address_type,omitempty"`
	Destination string `json:"destination,omitempty"`
	Port        int    `json:"port,omitempty"`
	Result      string `json:"result"`
}

// evidenceRecorder serializes events from concurrent service goroutines into
// one JSONL file owned by the node holder. The director only reads it after a
// netdoc process has exited.
type evidenceRecorder struct {
	mu   sync.Mutex
	node string
	file *os.File
}

func openEvidenceRecorder(path, node string) (*evidenceRecorder, error) {
	if path == "" {
		return &evidenceRecorder{node: node}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &evidenceRecorder{node: node, file: f}, nil
}

func (r *evidenceRecorder) record(event evidenceEvent) {
	if r == nil || r.file == nil {
		return
	}
	event.Node = r.node
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = json.NewEncoder(r.file).Encode(event)
}

func (r *evidenceRecorder) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
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
		}
	}
	var out Evidence
	for _, item := range dns {
		out.DNS = append(out.DNS, item)
	}
	for _, item := range socks {
		out.SOCKSRequests = append(out.SOCKSRequests, item)
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
	if out.DNS == nil {
		out.DNS = []DNSEvidence{}
	}
	if out.SOCKSRequests == nil {
		out.SOCKSRequests = []SOCKSEvidence{}
	}
	return out
}
