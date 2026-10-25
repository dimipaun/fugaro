package runner

import (
	"fmt"
	"os"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestMain(m *testing.M) {
	cleanup, err := testutil.PrepareFakeCommands()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
