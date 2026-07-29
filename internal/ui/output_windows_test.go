package ui

import "testing"

func TestDecodeWindowsCodePage(t *testing.T) {
	got, err := decodeWindowsCodePage(437, "M\x81nchen")
	if err != nil {
		t.Fatal(err)
	}
	if got != "München" {
		t.Errorf("decoded = %q, want München", got)
	}
}

func TestWindowsOEMToolSelection(t *testing.T) {
	for _, name := range []string{"route", "NETSTAT.EXE", `C:\Windows\System32\ping.exe`, "nslookup", "tracert", "pathping"} {
		if !isWindowsOEMTool(name) {
			t.Errorf("%q should use OEM decoding", name)
		}
	}
	for _, name := range []string{"curl.exe", "nmap", "ssh", "openssl"} {
		if isWindowsOEMTool(name) {
			t.Errorf("%q should stay UTF-8", name)
		}
		if got := decodeToolOutput(name, "München"); got != "München" {
			t.Errorf("%q UTF-8 output decoded as %q", name, got)
		}
	}
}
