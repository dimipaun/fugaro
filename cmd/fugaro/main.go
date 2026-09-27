// Command fugaro is the Fugaro CLI and in-container runner.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dimipaun/fugaro/internal/cli"
)

func main() {
	// SIGTERM is how Cloud Run asks a task to stop; cancelling the context lets
	// the runner abandon the current stage and still finalize.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := cli.NewRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fugaro:", err)
		os.Exit(cli.ExitCode(err))
	}
}
