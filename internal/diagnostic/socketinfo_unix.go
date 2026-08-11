//go:build linux || darwin

package diagnostic

import (
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func socketSendBuffer(conn net.Conn) (int, error) {
	return socketOption(conn, unix.SOL_SOCKET, unix.SO_SNDBUF)
}

func socketMSS(conn net.Conn) (int, error) {
	return socketOption(conn, unix.IPPROTO_TCP, unix.TCP_MAXSEG)
}

func socketOption(conn net.Conn, level, option int) (int, error) {
	return socketValue(conn, func(fd uintptr) (int, error) {
		return unix.GetsockoptInt(int(fd), level, option)
	})
}

// socketValue runs read against the connection's descriptor while the runtime
// holds it open. Getsockopt is not the only question worth asking of a socket:
// the send queue is an ioctl on Linux and a socket option on Darwin.
func socketValue(conn net.Conn, read func(fd uintptr) (int, error)) (int, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("connection does not expose its socket")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var value int
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		value, sockErr = read(fd)
	}); err != nil {
		return 0, err
	}
	return value, sockErr
}
