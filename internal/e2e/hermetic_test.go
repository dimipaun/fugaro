package e2e

import "slices"

// hermeticEnv is base for a fugaro subprocess that must not reach real
// Google or read the developer's credentials: no application default
// credentials (a file that doesn't exist), no metadata server, and every
// https request sent to a proxy nothing listens on (Go sends loopback
// traffic, which is all the fakes use, around any proxy). It applies to the
// fugaro child only: helper processes (docker builds) keep the real
// environment, so nothing is changed process-wide.
func hermeticEnv(base []string) []string {
	out := slices.Clone(base)
	return append(out,
		"GOOGLE_APPLICATION_CREDENTIALS=/nonexistent/fugaro-tests-have-no-credentials.json",
		"GCE_METADATA_HOST=127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1", "https_proxy=http://127.0.0.1:1",
		"STORAGE_EMULATOR_HOST=",
	)
}
