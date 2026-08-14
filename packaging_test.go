package main

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Two independent paths build the Linux packages: GoReleaser's nfpm section
// produces the deb/rpm/apk attached to the release, and packaging/network-doctor.spec
// is rebuilt from source by COPR. A user installing from either one has to end
// up with the same executables, so these tests read both and compare them
// rather than trusting the two files to be edited together.

type goreleaserBuild struct {
	ID     string   `yaml:"id"`
	Binary string   `yaml:"binary"`
	Main   string   `yaml:"main"`
	Goos   []string `yaml:"goos"`
}

type goreleaserConfig struct {
	Builds   []goreleaserBuild `yaml:"builds"`
	Archives []struct {
		ID  string   `yaml:"id"`
		IDs []string `yaml:"ids"`
	} `yaml:"archives"`
	NFPMs []struct {
		IDs    []string `yaml:"ids"`
		Bindir string   `yaml:"bindir"`
	} `yaml:"nfpms"`
}

func loadGoreleaserConfig(t *testing.T) goreleaserConfig {
	t.Helper()
	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Builds) == 0 || len(cfg.NFPMs) != 1 {
		t.Fatalf("unexpected config shape: %d builds, %d nfpms", len(cfg.Builds), len(cfg.NFPMs))
	}
	return cfg
}

// linuxBinaries is the contract every Linux install path owes the user.
func (c goreleaserConfig) linuxBinaries() []string {
	var names []string
	for _, b := range c.Builds {
		if slices.Contains(b.Goos, "linux") {
			names = append(names, b.Binary)
		}
	}
	slices.Sort(names)
	return names
}

func TestReleaseBuildsNetdocSim(t *testing.T) {
	cfg := loadGoreleaserConfig(t)

	i := slices.IndexFunc(cfg.Builds, func(b goreleaserBuild) bool { return b.Binary == "netdoc-sim" })
	if i < 0 {
		t.Fatal("no release build produces netdoc-sim")
	}
	sim := cfg.Builds[i]
	if sim.Main != "./cmd/netdoc-sim" {
		t.Errorf("netdoc-sim built from %q, want ./cmd/netdoc-sim", sim.Main)
	}
	// The backend is Linux namespaces. A darwin or windows artifact would be a
	// binary whose every real command returns ErrUnsupported, so shipping one
	// would advertise a simulator that cannot simulate. See docs/simulation.md.
	if !slices.Equal(sim.Goos, []string{"linux"}) {
		t.Errorf("netdoc-sim targets %v; it has no non-Linux backend, so keep it linux-only", sim.Goos)
	}
}

// Every build needs exactly one archive, or a binary either never reaches the
// release page or two of them race for the same filename.
func TestEveryBuildHasOneArchive(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	for _, b := range cfg.Builds {
		var archives []string
		for _, a := range cfg.Archives {
			if slices.Contains(a.IDs, b.ID) {
				archives = append(archives, a.ID)
			}
		}
		if len(archives) != 1 {
			t.Errorf("build %q is in archives %v, want exactly one", b.ID, archives)
		}
	}
}

// No ids filter on nfpms means every Linux build is packaged, which is how one
// `dnf install network-doctor` lands both executables.
func TestLinuxPackagesShipEveryLinuxBuild(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if ids := cfg.NFPMs[0].IDs; len(ids) != 0 {
		t.Errorf("nfpms filters builds to %v; drop the filter so every Linux build is packaged", ids)
	}
	if want := []string{"netdoc", "netdoc-sim"}; !slices.Equal(cfg.linuxBinaries(), want) {
		t.Errorf("Linux builds are %v, want %v", cfg.linuxBinaries(), want)
	}
}

// The container image is the third install path, and it owes the user the same
// pair of executables the packages do — from one build, at one version, in one
// directory, because that is what makes netdoc-sim find the netdoc beside it.
// container_test.go proves this against a built image; this proves it against
// the file, in the ordinary gate, where nobody needs a container engine.
func TestDockerfileShipsTheSameBinariesAsTheLinuxPackages(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)

	for _, binary := range loadGoreleaserConfig(t).linuxBinaries() {
		if !strings.Contains(dockerfile, "-o /out/"+binary+" ") {
			t.Errorf("the image never builds %s, which the Linux packages ship", binary)
		}
		if !strings.Contains(dockerfile, "COPY --from=build /out/"+binary+" /usr/bin/"+binary) {
			t.Errorf("the image never installs %s into /usr/bin, where netdoc-sim looks for its sibling", binary)
		}
	}
	// One version string for both, injected the same way GoReleaser injects it.
	// An image whose netdoc and netdoc-sim disagreed would make every recorded
	// challenge result ambiguous about which build it graded.
	if got := strings.Count(dockerfile, "-X main.version=${VERSION}"); got != 2 {
		t.Errorf("%d builds stamp -X main.version, want 2", got)
	}
	// Digest-pinned bases: a floating tag would make two builds of one commit
	// different images, and dependabot keeps the pins current.
	for _, line := range strings.Split(dockerfile, "\n") {
		if !strings.HasPrefix(line, "FROM ") {
			continue
		}
		if !strings.Contains(line, "@sha256:") {
			t.Errorf("base image is not pinned by digest: %s", line)
		}
	}
	// netdoc-sim owns the argument parsing. An entrypoint that re-parsed
	// anything would be a second CLI to keep in step with this one.
	if !strings.Contains(dockerfile, `ENTRYPOINT ["/usr/bin/netdoc-sim"]`) {
		t.Error("the image entrypoint is not netdoc-sim")
	}
}

// The release publishes the image from the tag it is building, with that tag as
// the version the binaries report. Reading it from the workflow keeps the two
// halves of "the image tag names the build inside it" from drifting apart.
func TestReleaseWorkflowPublishesTheImageAtTheTag(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, want := range []string{
		"VERSION=${{ github.ref_name }}",
		"ghcr.io/${{ github.repository_owner }}/netdoc-sim:${{ github.ref_name }}",
		"platforms: linux/amd64,linux/arm64",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("the release workflow does not contain %q", want)
		}
	}
}

// The man page and the three completion files are hand-maintained copies of the
// flag list in main.go, so a new flag silently ships undocumented and
// uncompletable. Read the flags back out of the real usage output and require
// each shipped surface to mention every one, in that surface's own spelling.
func TestShippedSurfacesDocumentEveryFlag(t *testing.T) {
	var usage bytes.Buffer
	if code := run([]string{"--help"}, &usage, io.Discard); code != 0 {
		t.Fatalf("run(--help) = %d, want 0", code)
	}
	// PrintDefaults writes "  -name value", then the usage text on its own
	// indented line.
	flags := regexp.MustCompile(`(?m)^  -(\S+)`).FindAllStringSubmatch(usage.String(), -1)
	if len(flags) < 2 {
		t.Fatalf("parsed %d flags out of the usage output:\n%s", len(flags), usage.String())
	}

	// How each file spells a flag, so "documented" means the real declaration
	// and not a passing mention in a comment or an example.
	surfaces := map[string]func(name string) string{
		"packaging/netdoc.1":                func(n string) string { return `\-` + strings.ReplaceAll(n, "-", `\-`) },
		"packaging/completions/netdoc.bash": func(n string) string { return "-" + n + " --" + n },
		"packaging/completions/netdoc.zsh":  func(n string) string { return "{--" + n + ",-" + n + "}" },
		"packaging/completions/netdoc.fish": func(n string) string { return "-o " + n + " -l " + n },
	}
	for path, spelling := range surfaces {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range flags {
			if want := spelling(f[1]); !strings.Contains(string(data), want) {
				t.Errorf("%s never declares -%s (looked for %q)", path, f[1], want)
			}
		}
	}
}

// specSection returns the lines of one %section of an RPM spec, so an assertion
// can tell "installed and listed" from "mentioned in a comment somewhere".
func specSection(t *testing.T, spec, name string) []string {
	t.Helper()
	// An allowlist, not "any line starting with %": %files' own body is made of
	// directives like %license and %doc, which are content rather than a new
	// section.
	sections := []string{"%description", "%prep", "%build", "%install", "%files", "%changelog"}
	var lines []string
	in := false
	for _, line := range strings.Split(spec, "\n") {
		if head := strings.Fields(line); len(head) > 0 && slices.Contains(sections, head[0]) {
			in = head[0] == name
			continue
		}
		if in && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	if len(lines) == 0 {
		t.Fatalf("spec section %s not found", name)
	}
	return lines
}

// The invariant that keeps a GitHub .deb and a COPR .rpm from disagreeing.
func TestRPMSpecShipsTheSameBinariesAsNFPM(t *testing.T) {
	data, err := os.ReadFile("packaging/network-doctor.spec")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(data)

	var packaged []string
	for _, line := range specSection(t, spec, "%files") {
		if name, ok := strings.CutPrefix(line, "%{_bindir}/"); ok {
			packaged = append(packaged, name)
		}
	}
	slices.Sort(packaged)

	want := loadGoreleaserConfig(t).linuxBinaries()
	if !slices.Equal(packaged, want) {
		t.Errorf("spec %%files ships %v, GoReleaser's Linux packages ship %v", packaged, want)
	}

	// %files can list a path %install never wrote; rpmbuild catches that, but
	// only on a Fedora host with the tarballs. Catch it in the ordinary gate.
	install := strings.Join(specSection(t, spec, "%install"), "\n")
	for _, name := range want {
		if !strings.Contains(install, "%{buildroot}%{_bindir}/"+name) {
			t.Errorf("spec %%install never installs %s", name)
		}
	}
	if !strings.Contains(strings.Join(specSection(t, spec, "%build"), "\n"), "./cmd/netdoc-sim") {
		t.Error("the spec's build section never compiles ./cmd/netdoc-sim")
	}
}
