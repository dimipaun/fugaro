//go:build !unix

package claudeplugin

import "os/exec"

// isolate is a no-op where process groups are not available: only the
// direct child is killed at a timeout. Fugaro releases for darwin and linux.
func isolate(cmd *exec.Cmd) {}
