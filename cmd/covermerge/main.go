// covermerge unions Go set-mode coverage profiles.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type block struct {
	location   string
	statements int
	covered    bool
}

func main() {
	out := flag.String("o", "coverage.out", "merged coverage profile")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: covermerge [-o profile] profile...")
		os.Exit(2)
	}
	var merged bytes.Buffer
	if err := merge(&merged, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "covermerge:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, merged.Bytes(), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "covermerge:", err)
		os.Exit(1)
	}
}

func merge(out io.Writer, paths []string) error {
	blocks := map[string]block{}
	for _, path := range paths {
		// #nosec G304 -- paths are explicit local coverage profile arguments.
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		if !scanner.Scan() || scanner.Text() != "mode: set" {
			_ = file.Close()
			return fmt.Errorf("%s: coverage mode is not set", path)
		}
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) != 3 {
				_ = file.Close()
				return fmt.Errorf("%s: malformed coverage row %q", path, scanner.Text())
			}
			statements, err := strconv.Atoi(fields[1])
			if err != nil || statements < 0 {
				_ = file.Close()
				return fmt.Errorf("%s: invalid statement count %q", path, fields[1])
			}
			count, err := strconv.ParseUint(fields[2], 10, 64)
			if err != nil {
				_ = file.Close()
				return fmt.Errorf("%s: invalid coverage count %q", path, fields[2])
			}
			current, exists := blocks[fields[0]]
			if exists && current.statements != statements {
				_ = file.Close()
				return fmt.Errorf("%s: block %s has inconsistent statement counts", path, fields[0])
			}
			blocks[fields[0]] = block{location: fields[0], statements: statements, covered: current.covered || count > 0}
		}
		err = scanner.Err()
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}

	ordered := make([]block, 0, len(blocks))
	for _, item := range blocks {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].location < ordered[j].location })
	if _, err := fmt.Fprintln(out, "mode: set"); err != nil {
		return err
	}
	for _, item := range ordered {
		count := 0
		if item.covered {
			count = 1
		}
		if _, err := fmt.Fprintf(out, "%s %d %d\n", item.location, item.statements, count); err != nil {
			return err
		}
	}
	return nil
}
