package simulation

import (
	"strings"
	"testing"
)

// A scenario file is data, not a script. Nothing in it may become a command, an
// executable path, a raw nftables expression, an interface name, or a mount
// path. These tests pin that down at the validation boundary — the only place
// scenario strings are allowed to turn into values this package will use.
//
// The structural half of the guarantee lives in the backend: every command is
// built as an argv slice and handed to exec.CommandContext, so no scenario
// string is ever parsed by a shell even if one slipped through here.

// hostile is the payload shape that would matter if any of these fields were
// ever concatenated into a command line.
const hostile = "; touch /tmp/pwned #"

func TestScenarioCannotSupplyCommandsOrPaths(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		// Node names become file names in the workspace and appear in argv-
		// adjacent positions; the charset is the guard.
		{"node name with a shell metacharacter",
			strings.Replace(minimalScenario, "name: server", "name: \"server"+hostile+"\"", 1),
			"letters, digits"},
		{"node name with a path separator",
			strings.Replace(minimalScenario, "name: server", "name: ../../etc/passwd", 1),
			"letters, digits"},
		{"node name with a newline",
			strings.Replace(minimalScenario, "name: server", "name: \"a\\nb\"", 1),
			"letters, digits"},

		// Addresses reach `ip addr add`, `ip route add`, nftables rules and the
		// generated resolv.conf.
		{"address that is not an address",
			strings.Replace(minimalScenario, "address: 10.77.0.10", "address: \"10.77.0.10 "+hostile+"\"", 1),
			"address"},
		{"gateway that is not an address",
			strings.Replace(minimalScenario, "gateway: 10.77.0.1", "gateway: \"$(id)\"", 1),
			"gateway"},
		{"alias that is not an address",
			strings.Replace(minimalScenario, "address: 10.77.0.1}", "address: 10.77.0.1, aliases: ['1.1.1.1 -j ACCEPT']}", 1),
			"alias"},

		// Ports and durations reach argv as numbers; they must be numbers.
		{"port out of range",
			strings.Replace(minimalScenario, "address: 10.77.0.1}", "address: 10.77.0.1, services: [{type: tcp, port: 99999}]}", 1),
			"out of range"},
		{"negative port",
			strings.Replace(minimalScenario, "address: 10.77.0.1}", "address: 10.77.0.1, services: [{type: tcp, port: -1}]}", 1),
			"out of range"},

		// Fault fields are the ones that become nftables and tc expressions.
		{"drop protocol is not a protocol",
			minimalScenario + "\nfaults: [{type: drop, node: client, protocol: 'udp drop; ip saddr 0.0.0.0/0'}]\n",
			"unknown protocol"},
		{"drop destination is not an address",
			minimalScenario + "\nfaults: [{type: drop, node: client, to: 'accept; drop'}]\n",
			"to:"},
		{"netem delay is not a duration",
			minimalScenario + "\nfaults: [{type: netem, node: client, delay: '10ms root handle 1:'}]\n",
			"delay"},
		{"netem loss is not a percentage",
			minimalScenario + "\nfaults: [{type: netem, node: client, delay: 10ms, loss: '1% ecn'}]\n",
			"not a percentage"},
		{"negative delay",
			minimalScenario + "\nfaults: [{type: netem, node: client, delay: -5ms}]\n",
			"must not be negative"},

		// The target is the one scenario string that reaches netdoc's own argv.
		{"target is not a target",
			strings.Replace(minimalScenario, "example.test:80", "'--json --iface eth0'", 1),
			"invalid target"},

		// Zone names are compared against queries and written into DNS answers.
		{"zone name with a metacharacter",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{type: dns, zone: {'a"+hostile+"': 10.77.0.1}}]}", 1),
			"not a hostname"},

		// There is no field for a command or an executable, and adding one to a
		// file must not be silently accepted.
		{"an invented command field", minimalScenario + "\ncommand: /bin/sh\n", "command"},
		{"an invented node exec field",
			strings.Replace(minimalScenario, "address: 10.77.0.1}", "address: 10.77.0.1, exec: /bin/sh}", 1),
			"exec"},
		{"an invented service binary field",
			strings.Replace(minimalScenario, "address: 10.77.0.1}",
				"address: 10.77.0.1, services: [{type: http, binary: /bin/sh}]}", 1),
			"binary"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseScenario(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("want a validation error, got none")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestScopedAddressesRejected covers the one hole netip.ParseAddr leaves open:
// it accepts an essentially arbitrary string after "%", which would then reach
// an `ip` argv and a generated resolv.conf.
func TestScopedAddressesRejected(t *testing.T) {
	if _, _, err := parseAddr("fe80::1%" + hostile); err == nil {
		t.Error("a scoped address must be rejected")
	}
	if _, _, err := parseAddr("fe80::1%eth0"); err == nil {
		t.Error("even a plausible zone must be rejected — nothing here supports one")
	}
}

// TestAddressesAreCanonicalised proves what reaches a command is the kernel's
// spelling of the address, not the bytes that were in the file.
func TestAddressesAreCanonicalised(t *testing.T) {
	s, err := ParseScenario(strings.NewReader(strings.Replace(minimalScenario,
		"address: 10.77.0.1}", "address: 10.77.0.1, aliases: ['0:0:0:0:0:0:0:1', '::FFFF:1.2.3.4']}", 1)))
	if err != nil {
		t.Fatal(err)
	}
	got := s.Topology.Nodes[1].Aliases
	want := []string{"::1", "::ffff:1.2.3.4"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alias[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestValidatedScenarioHasNoSurprisingStrings walks a fully validated scenario
// and asserts that every string that can reach a command is drawn from a
// charset with no shell, nftables or tc syntax in it. It is the backstop for a
// field added later without a validator.
func TestValidatedScenarioHasNoSurprisingStrings(t *testing.T) {
	for _, name := range LibraryNames() {
		s, err := LibraryScenario(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Run(name, func(t *testing.T) {
			for _, n := range s.Topology.Nodes {
				assertTame(t, "node name", n.Name)
				assertTame(t, "address", n.Address)
				assertTame(t, "gateway", n.Gateway)
				assertTame(t, "resolver", n.Resolver)
				for _, a := range n.Aliases {
					assertTame(t, "alias", a)
				}
				for _, svc := range n.Services {
					assertTame(t, "service type", svc.Type)
					for zn, zv := range svc.Zone {
						assertTame(t, "zone name", zn)
						assertTame(t, "zone address", zv)
					}
				}
			}
			for _, f := range s.Faults {
				assertTame(t, "fault type", f.Type)
				assertTame(t, "fault node", f.Node)
				assertTame(t, "fault to", f.To)
				assertTame(t, "fault protocol", f.Protocol)
				assertTame(t, "fault direction", f.Direction)
				assertTame(t, "fault loss", f.Loss)
			}
		})
	}
}

// assertTame rejects anything outside the charset the validators permit.
func assertTame(t *testing.T, what, s string) {
	t.Helper()
	const allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.:-_%"
	for _, r := range s {
		if !strings.ContainsRune(allowed, r) {
			t.Errorf("%s %q contains %q, which no validator should have let through", what, s, r)
		}
	}
}
