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
	// A bare --repo (secrets set/ls, budget set/kill/resume) is rewritten at
	// the argv boundary before cobra parses anything: pflag cannot express an
	// optional-value string flag without swallowing the `--repo owner/name`
	// form (see NormalizeRepoArgs).
	root := cli.NewRootCmd()
	root.SetArgs(cli.NormalizeRepoArgs(os.Args[1:]))
	cmd, err := root.ExecuteContextC(ctx)
	stop()
	// Before the process can exit: give the background daily update check
	// (internal/cli/updatewarn.go) a short window to finish rather than
	// killing it mid-flight the instant the command's own work is done.
	// Bounded to ~200ms, not its own ~1s FetchTimeout: a command must
	// never visibly wait out a blackholed network just to exit, so a check
	// still running past the bound is abandoned instead (that day's check
	// is lost, not "the command slowed down").
	cli.WaitForUpdateCheck()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fugaro:", cli.ErrorText(err))
	}
	// Not from a PersistentPostRun: cobra skips it when RunE errors, which
	// most command failures do, so the nag is printed here instead,
	// unconditionally, after the command's own output (including the error
	// line just above, if any).
	cli.WarnUpdateAvailable(cmd)
	if err != nil {
		os.Exit(cli.ExitCode(err))
	}
}

// signalContext returns a context cancelled by the first SIGINT or SIGTERM.
// That first signal only cancels the context, so the runner can abandon the
// current stage and still finalize; signal handling is then reset, so a
// second signal gets the default action and force-quits the process.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
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
