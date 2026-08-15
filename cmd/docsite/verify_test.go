package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// builtSite writes a minimal rendered site: a landing page and one document,
// both at the project base path, plus the files a crawler needs.
func builtFixture(t *testing.T, pages map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	all := map[string]string{
		"index.html":                      `<a href="/network-doctor/wiki/Getting-Started/">Start</a>`,
		"wiki/Getting-Started/index.html": `<h1 id="getting-started">Start</h1><a href="/network-doctor/">Home</a>`,
		"sitemap.xml":                     `<urlset/>`,
		"robots.txt":                      "User-agent: *\n",
	}
	for path, content := range pages {
		all[path] = content
	}
	for path, content := range all {
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(path)), content); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestVerifyAcceptsAWorkingSite(t *testing.T) {
	if err := verify(builtFixture(t, nil), "/network-doctor"); err != nil {
		t.Fatalf("a working site failed verification: %v", err)
	}
}

// Each case is breakage that only exists once the site is rendered, which is
// why it is checked against the built bytes rather than the sources.
func TestVerifyRejectsASiteThatWouldNotWork(t *testing.T) {
	for _, tc := range []struct {
		name, path, content, want string
	}{
		{
			// The failure mode of a project page: an absolute link
			// without the base path goes to the account's root site.
			name: "link that dropped the project base path",
			path: "index.html", content: `<a href="/wiki/Getting-Started/">Start</a>`,
			want: "missing the /network-doctor base path",
		},
		{
			name: "asset that dropped the project base path",
			path: "index.html", content: `<link href="/assets/style.css">`,
			want: "missing the /network-doctor base path",
		},
		{
			name: "link to a page the site never built",
			path: "index.html", content: `<a href="/network-doctor/wiki/Retired/">Gone</a>`,
			want: "which the site does not serve",
		},
		{
			name: "link to an anchor that moved",
			path: "index.html", content: `<a href="/network-doctor/wiki/Getting-Started/#renamed">Gone</a>`,
			want: "not an id on that page",
		},
		{
			name: "same-page anchor that does not exist",
			path: "index.html", content: `<a href="#nowhere">Skip</a>`,
			want: "not an id on this page",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verify(builtFixture(t, map[string]string{tc.path: tc.content}), "/network-doctor")
			if err == nil {
				t.Fatal("verification passed; the site would have deployed broken")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain %q", err, tc.want)
			}
		})
	}
}

func TestVerifyRejectsAnIncompleteBuild(t *testing.T) {
	for _, missing := range []string{"sitemap.xml", "robots.txt", "index.html"} {
		t.Run("no "+missing, func(t *testing.T) {
			dir := builtFixture(t, nil)
			if err := os.Remove(filepath.Join(dir, missing)); err != nil {
				t.Fatal(err)
			}
			err := verify(dir, "/network-doctor")
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("verify without %s returned %v, want a failure naming it", missing, err)
			}
		})
	}
}
