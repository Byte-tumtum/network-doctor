//go:build linux

package simulation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
func RunNode(ctx context.Context, cfgPath string, stdin io.Reader, stdout, stderr io.Writer) (err error) {
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
	if cfg.EnableIPv6 {
		if err := enableIPv6(); err != nil {
			return err
		}
	}
	if cfg.ForwardIPv4 {
		if err := enableForwarding(ipv4ForwardPath, "IPv4"); err != nil {
			return err
		}
	}
	if cfg.ForwardIPv6 {
		if err := enableForwarding(ipv6ForwardPath, "IPv6"); err != nil {
			return err
		}
	}
	if cfg.ForwardingStatus != "" && (cfg.ForwardIPv4 || cfg.ForwardIPv6) {
		status, err := json.Marshal(forwardingStatus{IPv4: cfg.ForwardIPv4, IPv6: cfg.ForwardIPv6})
		if err != nil {
			return err
		}
		if err := os.WriteFile(cfg.ForwardingStatus, status, 0o600); err != nil {
			return fmt.Errorf("record namespace forwarding: %w", err)
		}
	}
	fmt.Fprintln(stdout, holderNSReady)

	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != holderStart {
		return errNoStart
	}
	recorder, err := openEvidenceRecorder(cfg.Evidence, cfg.Name)
	if err != nil {
		return fmt.Errorf("open evidence recording for node %q: %w", cfg.Name, err)
	}
	defer func() { err = finishEvidenceRecording(err, recorder) }()
	closers, dns, err := startServices(ctx, cfg.Services, cfg.Addresses, cfg.Resolver, cfg.TrustDir, recorder)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeServices(closers); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("stop services for node %q: %w", cfg.Name, closeErr))
		}
	}()
	fmt.Fprintln(stdout, holderServicesReady)

	return serveHolderCommands(ctx, stdin, stdout, dns, recorder)
}

func finishEvidenceRecording(err error, recorder *evidenceRecorder) error {
	closeErr := recorder.Close()
	if closeErr == nil {
		return err
	}
	if err == nil || errors.Is(closeErr, err) {
		return closeErr
	}
	return errors.Join(err, closeErr)
}

const ipv4ForwardPath = "/proc/sys/net/ipv4/ip_forward"
const ipv6ForwardPath = "/proc/sys/net/ipv6/conf/all/forwarding"

var ipv6DisablePaths = []string{
	"/proc/sys/net/ipv6/conf/all/disable_ipv6",
	"/proc/sys/net/ipv6/conf/default/disable_ipv6",
}

type forwardingStatus struct {
	IPv4 bool `json:"ipv4"`
	IPv6 bool `json:"ipv6"`
}

// enableIPv4Forwarding runs in the router holder's network namespace. Linux
// virtualizes this sysctl per network namespace; writing it here cannot alter
// the director or host value. The status file lets the director report the
// value read back from this exact namespace without invoking a shell.
func enableForwarding(path, family string) error {
	if err := os.WriteFile(path, []byte("1\n"), 0o600); err != nil {
		return fmt.Errorf("enable namespace %s forwarding: %w", family, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("verify namespace %s forwarding: %w", family, err)
	}
	if strings.TrimSpace(string(raw)) != "1" {
		return fmt.Errorf("namespace %s forwarding remained %q", family, strings.TrimSpace(string(raw)))
	}
	return nil
}

func enableIPv6() error {
	for _, path := range ipv6DisablePaths {
		if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
			return fmt.Errorf("enable IPv6 in simulator namespace through %s: %w", path, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("verify IPv6 in simulator namespace through %s: %w", path, err)
		}
		if strings.TrimSpace(string(raw)) != "0" {
			return fmt.Errorf("simulator namespace IPv6 remained disabled at %s", path)
		}
	}
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
