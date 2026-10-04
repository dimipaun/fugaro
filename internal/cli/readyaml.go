package cli

import "github.com/dimipaun/fugaro/internal/config"

// maxFugaroYAML is the most a fugaro.yaml may hold: a real one is a few
// kilobytes, and a checkout is somebody else's until it is trusted.
const maxFugaroYAML = config.MaxCheckoutFile

// readFugaroYAML reads a fugaro.yaml (or any config file a checkout holds):
// only a regular file of at most maxFugaroYAML bytes (config.ReadRegular, the
// one reader of a checkout's files). A missing file is os.ErrNotExist.
func readFugaroYAML(path string) ([]byte, error) {
	return config.ReadRegular(path, maxFugaroYAML)
}
