//go:build !linux && !darwin

package diagnostic

import (
	"errors"
	"net"
	"runtime"
)

// socketQueued has nothing to call here. Windows is the platform this costs.
// Winsock's SO_SNDBUF reports the buffer's size and never its occupancy, and
// ioctlsocket offers FIONREAD, which counts the receive side. SIO_TCP_INFO is
// the one documented call that comes close, but it only counts what TCP has
// already put on the wire: BytesInFlight is sent-but-unacknowledged, and no
// field reports the unsent queue behind it. Acknowledgement progress could only
// be derived as BytesOut less BytesRetrans less BytesInFlight, which rests on
// undocumented behavior of BytesOut across retransmissions and on a struct
// layout hand-declared here that nothing in this repository can execute. A
// wrong offset would misclassify silently, which is worse than a limitation
// that announces itself in the row.
//
// The path-MTU probe handles the error rather than being handed a zero, and
// falls back to its send-buffer inference. See pmtuProbe for what that costs.
func socketQueued(net.Conn) (int, error) {
	return 0, errors.New("no TCP send-queue accounting on " + runtime.GOOS)
}
