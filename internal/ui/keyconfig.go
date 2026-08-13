// The key-binding config file: a small YAML dotfile naming a preset and, on
// top of it, whatever the user wants moved. netdoc only ever reads it — no
// file is created, repaired, or rewritten on a user's behalf.

package ui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/heymaikol/network-doctor/internal/textsafe"
	"gopkg.in/yaml.v3"
)

// maxConfigBytes bounds the read. Nothing about a keymap needs more, and the
// path is a filename netdoc was pointed at rather than one it validated.
const maxConfigBytes = 256 << 10

// keyConfig is the file's shape. Unknown fields are rejected rather than
// ignored: a mistyped `binding:` that silently did nothing would look exactly
// like netdoc losing the user's keymap, and this file is hand-written.
type keyConfig struct {
	// Keys names a preset to start from. Empty means the default preset.
	Keys string `yaml:"keys"`
	// Bindings replaces individual actions' keys. Each value is the complete
	// key list for that action, so an empty list unbinds it.
	Bindings map[string][]string `yaml:"bindings"`
}

// LoadKeymap resolves the keymap for a run: the file at path (empty path, or
// no file there, means no user config), with preset overriding the file's own
// `keys:` when the CLI supplied one.
//
// It always returns a usable keymap. A broken config is reported, never fatal:
// the config is normally edited between runs, but netdoc is normally started
// during an outage, and refusing to start is the one response that helps
// nobody.
func LoadKeymap(path, preset string) (Keymap, []error) {
	cfg, err := readKeyConfig(path)
	if err != nil {
		km, _ := PresetKeymap(orElse(preset, "default"))
		return km, []error{err}
	}
	// The flag wins over the file: it is the more specific instruction, and
	// it is how a user gets a working keymap out of a broken file.
	return buildKeymap(orElse(preset, orElse(cfg.Keys, "default")), cfg.Bindings)
}

// orElse returns s, or fallback when s is empty.
func orElse(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func readKeyConfig(path string) (keyConfig, error) {
	var cfg keyConfig
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return cfg, nil // nothing configured is not a mistake
	case err != nil:
		return cfg, fmt.Errorf("%s", textsafe.Clean(err.Error()))
	case len(b) > maxConfigBytes:
		return cfg, fmt.Errorf("config file is larger than %d bytes", maxConfigBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		// yaml quotes the offending line back at you, and that line came from
		// a file netdoc did not write.
		return keyConfig{}, fmt.Errorf("%s", textsafe.Clean(err.Error()))
	}
	return cfg, nil
}
