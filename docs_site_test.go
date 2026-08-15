package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The documentation site is published by GitHub Pages at an address derived
// entirely from the repository's owner and name. Nothing in the repository is
// told that address by GitHub, so several files hard-code it: the Jekyll config
// serves from it, cmd/docsite writes every internal link against it, and the
// package managers advertise it as the project's homepage. These tests derive
// it once from the module path and hold all of them to it, because a base path
// that is merely plausible produces a site of 404s that still deploys green.

// pagesURL is where a GitHub project page for this module is served.
func pagesURL(t *testing.T) (site, baseurl string) {
	t.Helper()
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^module\s+github\.com/([^/\s]+)/([^/\s]+)\s*$`).FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("go.mod has no github.com/<owner>/<repo> module path to derive the Pages URL from")
	}
	owner, repo := strings.ToLower(m[1]), m[2]
	return "https://" + owner + ".github.io/" + repo + "/", "/" + repo
}

func siteConfig(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("site/_config.yml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A project page is served under /<repo>/. Getting url or baseurl wrong does not
// fail any build: it produces canonical tags, a sitemap, and asset paths that
// all point somewhere that does not exist.
func TestSiteIsConfiguredForItsGitHubPagesAddress(t *testing.T) {
	site, baseurl := pagesURL(t)
	cfg := siteConfig(t)

	wantURL := strings.TrimSuffix(site, baseurl+"/")
	if got, _ := cfg["url"].(string); got != wantURL {
		t.Errorf("site/_config.yml url is %q, want %q", got, wantURL)
	}
	if got, _ := cfg["baseurl"].(string); got != baseurl {
		t.Errorf("site/_config.yml baseurl is %q, want %q: a project page is served under its repository name", got, baseurl)
	}
	for _, plugin := range []string{"jekyll-seo-tag", "jekyll-sitemap"} {
		plugins, _ := yaml.Marshal(cfg["plugins"])
		if !strings.Contains(string(plugins), plugin) {
			t.Errorf("site/_config.yml does not enable %s; the site would publish without %s", plugin,
				map[string]string{"jekyll-seo-tag": "titles, descriptions, or canonical URLs", "jekyll-sitemap": "a sitemap"}[plugin])
		}
	}
}

// The audit this change answers found the project's advertised homepage
// pointing at a download page. Every package manager's homepage is a place to
// learn the tool, so all of them point at the documentation site — and none of
// them may quietly go back to a releases URL.
func TestPackagedHomepagesPointAtTheDocumentationSite(t *testing.T) {
	site, _ := pagesURL(t)
	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	homepages := regexp.MustCompile(`(?m)^\s*homepage:\s*(\S+)\s*$`).FindAllStringSubmatch(string(data), -1)
	if len(homepages) < 4 {
		t.Fatalf("found %d homepage fields in .goreleaser.yaml; the test is not reaching the publish targets", len(homepages))
	}
	for _, m := range homepages {
		if m[1] != site {
			t.Errorf("a .goreleaser.yaml homepage is %q, want the documentation site %q", m[1], site)
		}
	}
}

// The RPM spec's URL is not a homepage advertisement: Source0 and Source1 are
// written as %{url}/releases/download/..., so repointing it at the site would
// break the source fetch COPR builds from. It stays the repository.
func TestRPMSpecURLStaysTheRepositoryItsSourcesComeFrom(t *testing.T) {
	data, err := os.ReadFile("packaging/network-doctor.spec")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(data)
	m := regexp.MustCompile(`(?m)^URL:\s+(\S+)\s*$`).FindStringSubmatch(spec)
	if m == nil {
		t.Fatal("the spec has no URL: field")
	}
	if !strings.HasPrefix(m[1], "https://github.com/") {
		t.Errorf("spec URL is %q; Source0 derives from it and must stay a github.com repository URL", m[1])
	}
	if !strings.Contains(spec, "Source0:        %{url}/releases/download/") {
		t.Error("the spec no longer derives Source0 from %{url}; this test's reason to exist changed")
	}
}

// Repointing the homepage is not a licence to rewrite every GitHub URL: the
// README's download links are supposed to point at the latest release, and a
// mechanical replacement would send people to documentation instead of files.
func TestREADMEKeepsDownloadLinksOnTheReleasesPage(t *testing.T) {
	site, _ := pagesURL(t)
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)

	if strings.Count(readme, "/releases/latest") < 3 {
		t.Error("README no longer sends downloads to /releases/latest; the .deb/.rpm/.apk and prebuilt-binary links need it")
	}
	if !strings.Contains(readme, site) {
		t.Errorf("README never links to the documentation site %s, which is how the site gets found", site)
	}
	if !strings.Contains(readme, "## Documentation") {
		t.Error("README has no Documentation section pointing at the site")
	}
}

// The site only exists if the workflow builds and deploys it, from both of its
// sources, with the supply-chain practice the rest of this repository uses.
func TestPagesWorkflowBuildsBothSourcesAndPinsItsActions(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/pages.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)

	for _, want := range []struct{ needle, why string }{
		{".wiki.git", "the wiki is never checked out, so the explanatory pages never reach the site"},
		{"go run ./cmd/docsite -wiki", "the site is never staged from docs/ and the wiki"},
		{"go run ./cmd/docsite -verify", "a site with broken links or base paths would deploy unnoticed"},
		{"actions/jekyll-build-pages@", "nothing renders the Markdown"},
		{"actions/upload-pages-artifact@", "the built site is never handed to Pages"},
		{"actions/deploy-pages@", "nothing deploys"},
		{"gollum:", "editing a wiki page would not republish the site"},
	} {
		if !strings.Contains(workflow, want.needle) {
			t.Errorf("pages.yml has no %q: %s", want.needle, want.why)
		}
	}

	// Same pinning rule the other workflows follow: a commit SHA, with the
	// human-readable version in a trailing comment.
	pinned := regexp.MustCompile(`uses: \S+@(\S+)`)
	uses := pinned.FindAllStringSubmatch(workflow, -1)
	if len(uses) < 4 {
		t.Fatalf("found %d action uses in pages.yml; the test is not reaching the workflow", len(uses))
	}
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, m := range uses {
		if !sha.MatchString(m[1]) {
			t.Errorf("pages.yml uses %q, which is not pinned to a commit SHA", m[0])
		}
	}

	// Least privilege: read-only by default, with write scopes only on the
	// job that deploys.
	before, after, ok := strings.Cut(workflow, "\njobs:")
	if !ok {
		t.Fatal("pages.yml has no jobs block")
	}
	if !strings.Contains(before, "permissions:\n  contents: read") {
		t.Error("pages.yml does not default to contents: read")
	}
	build, deploy, ok := strings.Cut(after, "\n  deploy:")
	if !ok {
		t.Fatal("pages.yml has no deploy job")
	}
	if strings.Contains(build, "write") {
		t.Error("the build job grants a write permission; only the deploy job needs one")
	}
	for _, scope := range []string{"pages: write", "id-token: write"} {
		if !strings.Contains(deploy, scope) {
			t.Errorf("the deploy job is missing %q, which Pages deployment requires", scope)
		}
	}
	if !strings.Contains(deploy, "github.ref == 'refs/heads/main'") {
		t.Error("the deploy job does not restrict itself to main")
	}
}
