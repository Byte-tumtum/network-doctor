package diagnostic

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// sshAliasRe keeps aliases that render safely inside the map's "ip (name)"
// format; ssh allows almost anything in a Host pattern, but a name with
// spaces or parens would corrupt the display and the parse behind it.
var sshAliasRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// SSHHostAliases maps IP literals from ~/.ssh/config HostName lines to their
// Host alias — the name the user actually calls the machine, which often
// exists in no DNS at all.
// ponytail: top-level config only; Include directives can wait for someone
// who uses them.
func SSHHostAliases() map[string]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	f, err := os.Open(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseSSHAliases(f)
}

func parseSSHAliases(r io.Reader) map[string]string {
	names := make(map[string]string)
	var aliases []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "host":
			aliases = fields[1:]
		case "hostname":
			// ssh honors only the first HostName per block; so do we,
			// even when it isn't an IP literal.
			blockAliases := aliases
			aliases = nil
			if net.ParseIP(fields[1]) == nil {
				continue
			}
			for _, a := range blockAliases {
				if sshAliasRe.MatchString(a) {
					names[fields[1]] = a
					break
				}
			}
		}
	}
	return names
}
