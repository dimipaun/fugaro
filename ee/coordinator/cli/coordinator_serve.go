// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

// newCoordinatorServeCmd is `fugaro coordinator serve`: the resident service's
// process, and the only thing that runs on the front door's Cloud Run revision
// (mcp-server plan task M4; the module names this pair as the container's
// command and args, coordinator.tf).
//
// It is hidden, like the sweep and like the credentials service's own serve,
// because it is not a command of the coordinator's surface: it takes no
// argument, no flag and no config file, and everything it can be given is the
// environment the installation module sets on the service. `fugaro coordinator
// --help` is where that is checked (TestCoordinatorServeIsHidden), and the
// generated CLI reference skips a hidden command, so the coordinator's
// documented surface does not change.
//
// The image is the history image's (internal/infra's MCPServer names it), whose
// default CMD is `version` and whose entrypoint is the fugaro binary: the
// module's explicit command and args are what make the revision the front door
// rather than a container that prints a version and exits.
//
// What the process refuses, and in which order, is the credentials service's
// order and for the same reasons: GODEBUG=http2debug makes Go print the
// requests it handles, headers included, and the front door's headers carry a
// caller's bearer token, so that refusal comes first, before anything is read;
// then the environment, because a revision that mounted a model credential is
// the one thing spec.md §9 forbids and it must fail to start rather than serve
// (coordinator.RefuseModelCredential); and only then a listener. os.Interrupt
// is beside SIGTERM because the same binary is what a developer runs by hand,
// and the context below is what drains the requests in flight before the
// process returns. That context is derived from the command's own, so it is
// done on either: a signal, or a cancellation from whatever ran this command
// (cobra's ExecuteContext), which reaches the same drain by the same path and
// needs no second switch of its own.
func newCoordinatorServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the coordinator's front door in its image (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := fcli.RefuseHTTP2Debug(os.Getenv); err != nil {
				return err
			}
			if err := coordinator.RefuseModelCredential(os.Environ); err != nil {
				return err
			}
			return coordinator.Serve(ctx, coordinator.NewService(), os.Getenv, cmd.ErrOrStderr())
		},
	}
}
