//go:build linux

package simulation

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// resolvConf is the path a node's resolver configuration is bound over. The
// bind lives in the node's own mount namespace — the host's file is not
// writable from the director's user namespace, and would not be touched even
// if it were.
const resolvConf = "/etc/resolv.conf"

// RunNode is the node holder: the process that owns one simulated machine's
// network and mount namespaces. It is spawned by the director as
// `netdoc-sim __node <config.json>` with those namespaces already created by
// clone(2), so all it has to do is furnish them and stay alive.
//
// It never touches the network itself — the director does the wiring from
// outside via nsenter — which keeps the holder small enough to read in one go.
func RunNode(ctx context.Context, cfgPath string, stdin io.Reader, stdout, stderr io.Writer) error {
	blob, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	var cfg nodeConfig
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return fmt.Errorf("%s: %w", cfgPath, err)
	}
	if cfg.Resolver != "" {
		if err := bindResolver(filepath.Dir(cfgPath), cfg.Name, cfg.Resolver); err != nil {
			return err
		}
	}
	fmt.Fprintln(stdout, holderNSReady)

	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != holderStart {
		return errNoStart
	}
	closers, err := startServices(cfg.Services, cfg.Addresses)
	if err != nil {
		return err
	}
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	fmt.Fprintln(stdout, holderServicesReady)

	waitForShutdown(ctx, stdin)
	return nil
}

// bindResolver points this node at its own nameserver by bind-mounting a
// generated resolv.conf over the system one. The mount is confined to this
// process's mount namespace, which was created with clone(2) and made private,
// so it is invisible to the host and to every other node — that is what lets
// two simulated machines disagree about who their resolver is.
func bindResolver(dir, node, resolver string) error {
	src := filepath.Join(dir, node+"-resolv.conf")
	if err := os.WriteFile(src, []byte("nameserver "+resolver+"\n"), 0o644); err != nil {
		return err
	}
	if err := syscall.Mount(src, resolvConf, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind %s over %s: %w", src, resolvConf, err)
	}
	return nil
}
