package main

import (
	"os"
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
