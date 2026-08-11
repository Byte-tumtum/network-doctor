//go:build darwin

package diagnostic

import (
	"net"

	"golang.org/x/sys/unix"
)

// socketQueued reports what this socket still owes the peer: bytes the
// application has written that the peer has not acknowledged.
//
// SO_NWRITE reads the send socket buffer's occupancy, which for a TCP socket
// is the unsent data plus the unacknowledged window — the same quantity Linux
// answers SIOCOUTQ with. It is a plain getsockopt returning an int, so it needs
// no separate accessor.
func socketQueued(conn net.Conn) (int, error) {
	return socketOption(conn, unix.SOL_SOCKET, unix.SO_NWRITE)
}
