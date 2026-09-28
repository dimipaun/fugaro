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
	quietGoogleSDK()
	// SIGTERM is how Cloud Run asks a task to stop; cancelling the context lets
	// the runner abandon the current stage and still finalize.
	ctx, stop := signalContext()
	err := cli.NewRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fugaro:", err)
		os.Exit(cli.ExitCode(err))
	}
}

// signalContext returns a context cancelled by the first SIGINT or SIGTERM.
// That first signal only cancels the context, so the runner can abandon the
// current stage and still finalize; signal handling is then reset, so a
// second signal gets the default action and force-quits the process.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(ctx, stop)
	return ctx, stop
}

// sdkLogEnv makes a Google Cloud Go client built without a logger of its
// own log every request, headers and body, to stderr. Fugaro's own clients
// pass a discarding logger (internal/backend/gcp), but the storage client
// drops it (cloud.google.com/go/storage builds its raw service with only an
// HTTP client), and a secret or a task must never reach stderr because a
// user once exported this for debugging something else.
const sdkLogEnv = "GOOGLE_SDK_GO_LOGGING_LEVEL"

// quietGoogleSDK unsets sdkLogEnv before any client exists; the Go
// clients read it when they are built.
func quietGoogleSDK() { _ = os.Unsetenv(sdkLogEnv) }
