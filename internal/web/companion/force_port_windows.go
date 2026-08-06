//go:build windows

package companion

// listenerBusy is best-effort on Windows; Restart relies on graceful stop.
func listenerBusy(port int) bool { return false }

// forceFreePort is a no-op on Windows; graceful companion stop covers the
// supported lifecycle. Orphan listeners must be stopped with `mm web stop`.
func forceFreePort(port int) {}
