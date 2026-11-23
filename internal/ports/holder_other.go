//go:build !windows

package ports

// Holder names the process listening on the port; this platform cannot
// tell yet, so it returns "".
func Holder(int) string { return "" }
