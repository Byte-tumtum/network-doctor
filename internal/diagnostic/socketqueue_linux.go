//go:build linux

package diagnostic

import (
	"net"

	"golang.org/x/sys/unix"
)

// socketQueued reports what this socket still owes the peer: bytes the
// application has written that the peer has not acknowledged.
//
// SIOCOUTQ counts the unsent queue and the sent-but-unacknowledged window
// together, which is the quantity the path-MTU probe needs — either state
// means the bytes have not arrived. SIOCOUTQNSD would count only the unsent
// half, and a black hole parks its payload in the unacknowledged half.
//
// SO_SNDBUF cannot answer this. Linux treats it as an accounting hint rather
// than a hard ceiling, so a socket reporting an 8 KiB send buffer will still
// take a 24 KiB write in one go and report success without a byte leaving the
// machine.
func socketQueued(conn net.Conn) (int, error) {
	return socketValue(conn, func(fd uintptr) (int, error) {
		return unix.IoctlGetInt(int(fd), unix.SIOCOUTQ)
	})
}
