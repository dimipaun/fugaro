package cli

import (
	"errors"
	"os"

	"github.com/dimipaun/fugaro/internal/config"
)

// maxFugaroYAML is the most a fugaro.yaml may hold: a real one is a few
// kilobytes, and a checkout is somebody else's until it is trusted.
const maxFugaroYAML = config.MaxCheckoutFile

// missingFugaroYAML is the one sentence a command prints when the checkout it
// reads has no fugaro.yaml: the file the person has to write before the
// command can run, and the two ways to write it. It carries no syscall text —
// the raw os.ErrNotExist ("lstat <path>: no such file or directory") reads as
// a crash and hides the guidance it is appended to.
const missingFugaroYAML = "this checkout has no fugaro.yaml yet: write it with /fugaro:setup in Claude Code (or start from fugaro config example), merge it, then run fugaro setup --repo again"

// readFugaroYAML reads a fugaro.yaml (or any config file a checkout holds):
// only a regular file of at most maxFugaroYAML bytes (config.ReadRegular, the
// one reader of a checkout's files). A missing file is os.ErrNotExist.
func readFugaroYAML(path string) ([]byte, error) {
	return config.ReadRegular(path, maxFugaroYAML)
}

// requireFugaroYAML is readFugaroYAML for the commands that cannot go on
// without the file: a missing one is missingFugaroYAML, never the raw
// os.ErrNotExist, and every other refusal (a link, a device, an oversize
// file) is passed through unchanged.
func requireFugaroYAML(path string) ([]byte, error) {
	data, err := readFugaroYAML(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, userErr("%s", missingFugaroYAML)
	}
	return data, err
}
