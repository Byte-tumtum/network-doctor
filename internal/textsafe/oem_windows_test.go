package textsafe

import "testing"

// 437 rather than the machine's OEM page, so the expectation is stable.
func TestDecodeCodePage(t *testing.T) {
	got, err := decodeCodePage(437, "M\x81nchen")
	if err != nil {
		t.Fatal(err)
	}
	if got != "München" {
		t.Errorf("decoded = %q, want München", got)
	}
}
