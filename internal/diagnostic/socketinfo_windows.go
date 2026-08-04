package diagnostic

import (
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

func socketSendBuffer(conn net.Conn) (int, error) {
	return socketOption(conn, windows.SOL_SOCKET, windows.SO_SNDBUF)
}

func socketMSS(conn net.Conn) (int, error) {
	return socketOption(conn, windows.IPPROTO_TCP, windows.TCP_MAXSEG)
}

func socketOption(conn net.Conn, level, option int) (int, error) {
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
		value, sockErr = windows.GetsockoptInt(windows.Handle(fd), level, option)
	}); err != nil {
		return 0, err
	}
	return value, sockErr
}
