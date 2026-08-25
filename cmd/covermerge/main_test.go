package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeUnionsOverlappingProfiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, profile string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(profile), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	one := write("one.out", "mode: set\nexample/a.go:2.1,3.2 1 1\nexample/a.go:5.1,6.2 2 0\n")
	two := write("two.out", "mode: set\nexample/a.go:2.1,3.2 1 0\nexample/a.go:5.1,6.2 2 1\nexample/b.go:1.1,2.2 1 1\n")

	var got bytes.Buffer
	if err := merge(&got, []string{one, two}); err != nil {
		t.Fatal(err)
	}
	want := "mode: set\nexample/a.go:2.1,3.2 1 1\nexample/a.go:5.1,6.2 2 1\nexample/b.go:1.1,2.2 1 1\n"
	if got.String() != want {
		t.Fatalf("merged profile:\n%s\nwant:\n%s", got.String(), want)
	}
}

func TestMergeRejectsIncompatibleProfiles(t *testing.T) {
	dir := t.TempDir()
	for name, profile := range map[string]string{
		"mode":       "mode: count\nexample/a.go:2.1,3.2 1 1\n",
		"statements": "mode: set\nexample/a.go:2.1,3.2 2 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			base := filepath.Join(dir, name+"-base.out")
			other := filepath.Join(dir, name+"-other.out")
			if err := os.WriteFile(base, []byte("mode: set\nexample/a.go:2.1,3.2 1 0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(other, []byte(profile), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := merge(new(bytes.Buffer), []string{base, other}); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("merge error = %v, want %q", err, name)
			}
		})
	}
}
