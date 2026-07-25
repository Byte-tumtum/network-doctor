package diagnostic

import (
	"maps"
	"strings"
	"testing"
)

func TestParseSSHAliases(t *testing.T) {
	config := `
# comment
Host github.com
  HostName github.com
  User git

Host pihole
  HostName 192.168.1.1
  User pi

Host pi-nas backup-nas
	HostName 192.168.1.2

Host *.example !prod
  HostName 192.168.1.3

Host stray
  # HostName commented out

Host v6box
  HostName fd00::7

Host myserver
  HostName myserver.example.com
  HostName 10.0.0.5
`
	got := parseSSHAliases(strings.NewReader(config))
	want := map[string]string{
		"192.168.1.1": "pihole",
		"192.168.1.2": "pi-nas",
		"fd00::7":     "v6box",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("parseSSHAliases = %v, want %v (non-IP hostnames and pattern aliases skipped)", got, want)
	}
}
