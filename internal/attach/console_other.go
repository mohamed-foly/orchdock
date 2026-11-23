//go:build !windows

package attach

import "os"

// Terminals outside Windows already speak terminal sequences.
func enableVTInput(*os.File) func()  { return func() {} }
func enableVTOutput(*os.File) func() { return func() {} }
