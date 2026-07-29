package diagnostic

import (
	"bufio"
	"bytes"
	"net"
	"strings"

	"github.com/heymaikol/network-doctor/internal/textsafe"
)

type advertisedName struct {
	name  string
	score int
}

func parseAvahiNames(out []byte, ips []string) map[string]string {
	wanted := make(map[string]bool, len(ips))
	for _, ip := range ips {
		wanted[ip] = true
	}

	best := make(map[string]advertisedName)
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(nil, 64<<10)
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), ";", 10)
		if len(fields) < 9 || fields[0] != "=" || !wanted[fields[7]] || net.ParseIP(fields[7]) == nil {
			continue
		}

		host := strings.TrimSuffix(unescapeAvahi(fields[6]), ".local")
		name, score := host, 1
		if instance := unescapeAvahi(fields[3]); instance != "" {
			name, score = instance, 2
			if strings.HasPrefix(instance, host+" - ") {
				name = host
			}
		}
		if len(fields) == 10 {
			switch {
			case fields[4] == "_googlecast._tcp" && avahiTXT(fields[9], "fn") != "":
				name, score = avahiTXT(fields[9], "fn"), 5
			case avahiTXT(fields[9], "fn") != "":
				name, score = avahiTXT(fields[9], "fn"), 4
			case avahiTXT(fields[9], "ty") != "":
				name, score = avahiTXT(fields[9], "ty"), 3
			}
		}
		name = strings.TrimSpace(textsafe.Clean(strings.ToValidUTF8(name, "?")))
		if name == "" || len(name) > 100 {
			continue
		}
		if old := best[fields[7]]; score > old.score || score == old.score && len(name) < len(old.name) {
			best[fields[7]] = advertisedName{name, score}
		}
	}

	names := make(map[string]string, len(best))
	for ip, candidate := range best {
		names[ip] = candidate.name
	}
	return names
}

func avahiTXT(txt, key string) string {
	start := strings.Index(txt, `"`+key+`=`)
	if start < 0 {
		return ""
	}
	value := txt[start+len(key)+2:]
	var ok bool
	value, _, ok = strings.Cut(value, `"`)
	if !ok {
		return ""
	}
	return unescapeAvahi(value)
}

func unescapeAvahi(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i+3 < len(s) && s[i] == '\\' &&
			s[i+1] >= '0' && s[i+1] <= '9' &&
			s[i+2] >= '0' && s[i+2] <= '9' &&
			s[i+3] >= '0' && s[i+3] <= '9' {
			b.WriteByte((s[i+1]-'0')*100 + (s[i+2]-'0')*10 + s[i+3] - '0')
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
