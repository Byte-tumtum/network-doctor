//go:build !linux

package simulation

// No backend runs off Linux, so no simulation can be recorded and none can be
// alive. These keep the shared state code compiling everywhere.

func processStamp(int) string { return "" }

func stopProcess(int, bool) error { return ErrUnsupported }
