package watch

import (
	"fmt"
	"os"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestMain prepares the fake gh the PR-state tests link to, once, before any
// test can fork (testutil.FakeCommand says why no test writes its own).
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
