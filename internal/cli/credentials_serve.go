package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/credsvc"
)

// `fugaro credentials serve` is the credentials service's own entry point
// (plan task C1.14): the process the service's image runs, and the only thing
// that runs in it. It is the counterpart of the history job's
// `fugaro budget history --sweep` (history.go) -- a hidden command whose job
// is to be a Cloud Run process rather than something an operator types -- with
// one difference that decides its shape: a Cloud Run *job* is started with a
// command and its arguments (modules/workflow/job.tf sets both, and
// internal/infra/tf's knownCommands holds the pairs), while the service is a
// Cloud Run *service*, whose container sets neither, so the image's own
// entrypoint is the service and the command below is what the Dockerfile's
// CMD names (images/credentials/Dockerfile). internal/infra/tf's cover case
// for google_cloud_run_v2_service is written against exactly that: it refuses
// a plan whose container sets a command or args, because the module sets
// neither and the image's entrypoint is what runs.
//
// It is hidden twice -- the group and the subcommand -- because it is not an
// operator's command: it takes no argument, no flag and no config file, and
// everything it can be given is the environment the installation module sets
// on the service -- which is not yet enough to serve (below). `fugaro --help`
// is where that is checked (TestCredentialsServeHidden).
//
// The environment is credentials.tf's, and it is exactly three names:
// FUGARO_PROJECT, FUGARO_GCP_PROJECT and FUGARO_REGION. They are the history
// job's own three constants (history.go) read the same way, and they are held
// by a second thing: internal/infra/tf's credentialsServiceEnv is the
// allow-list init's cover applies to the service's container, so a fourth
// name is refused at plan time and an environment variable is not a path a
// credential reaches this process by. The bundle secret is not one of them:
// the service derives its name from FUGARO_PROJECT (C1.11's loader, the
// project-wide container of the logical name "creds-bundle") and reads it as
// its own service account, which is the one grant it holds (Q6, Global
// Constraint 12). Nothing here reads a credential and nothing here prints one:
// the command has no flag that could carry a value, no output of its own, and
// every refusal below names a variable or a missing piece and never a value.
//
// WHAT THIS BUILD CANNOT DO, AND WHY THIS COMMAND DOES NOT SERVE.
//
// The service itself is plan task C1.11's, and it is in this build:
// internal/credsvc's server.go defines Config and NewServer, and the routes,
// the forward, the cascade and the bundle loader are there with it. So the
// host below is real -- credsvc.NewServer over a configuration, the platform's
// PORT, the bounds a long-lived streamed call allows, and a SIGTERM that
// drains -- and it is the whole of what this image runs.
//
// What is missing is the credsvc.Config that host is handed, and that is a
// fact about the deployment rather than about this build. The installation
// module puts exactly three names on the service's container
// (FUGARO_PROJECT, FUGARO_GCP_PROJECT, FUGARO_REGION; credentials.tf), and a
// Config needs four more facts of the installation: the realtime database URL
// the policy, kill, state, probe and outcome readers are all built on, the
// Firebase project a run's identity is verified against, the service's own
// URL (the audience its callers' ID tokens are minted for), and the job
// service accounts, one per (repository, workflow). None is derivable from
// the three -- a Cloud Run URL is not a function of a name and a region, and
// a repository's slug is a hash (task.Slug) -- and guessing any of them is
// what this command must not do: a guessed audience or account table is a
// service that admits a caller it should not, which is the boundary D135/Q6
// exist to keep.
//
// So credentialsServiceConfig refuses, by name, listing all four and naming
// the change that lifts it, and no port is bound: that is the fail-closed
// direction this whole work is held to (D143: an unreachable service halts a
// run, and no code path falls back to a key in the job). A revision that
// fails to start is what tells an owner the image they deployed cannot serve,
// while a service that answered every call with a refusal would look healthy
// and halt every run that reached it.
//
// The change that lifts it is not a variable an operator can add to the
// container: the cover's credentialsServiceEnv allow-list admits those three
// names and refuses a fourth at plan time, which is the rule that keeps an
// environment variable from being a path a credential reaches this process
// by. It is this command's own config builder, that allow-list and
// credentials.tf moving together -- and no task in the plan records that
// change (C1.18, which owns the container, has landed; its allow-list is the
// boundary above). The day it lands this command serves with no change to its
// own contract: the three names stay required, the refusals stay by name, and
// hostCredentialsService below is already the host those facts feed.
func newCredentialsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "credentials",
		Short:  "Run the credentials service (internal; the service image's entrypoint)",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	cmd.AddCommand(newCredentialsServeCmd())
	return cmd
}

// newCredentialsServeCmd is `fugaro credentials serve`: the service's process,
// and nothing else. It has no flags at all -- not even a --port: Cloud Run
// gives the container the port it must listen on, and a flag would be a second
// way to start a process whose whole configuration is the platform's.
func newCredentialsServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the credentials service in its image (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Cloud Run stops a revision by sending SIGTERM and killing the
			// container ten seconds later, so the signal is a request to stop
			// serving rather than to die: the context below is what the host
			// watches, and it drains the calls in flight before it returns
			// (hostCredentialsService). os.Interrupt is here because the same
			// binary is what a developer runs by hand; neither signal is a
			// failure, and a run that reaches this process must not be cut off
			// mid-answer.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveCredentials(ctx, cmd, os.Getenv)
		},
	}
}

// serveCredentials starts the service.
//
// The order below is the order of what an operator has to be told: first the
// http2debug refusal, which is every command of this repository's that opens
// or handles a credential (refuseHTTP2Debug: GODEBUG=http2debug makes Go
// print HTTP requests, headers included, to stderr), then the environment the
// module sets, and only then the service itself. A service whose deployment is
// wrong must be told so by name rather than by a failure to start for a reason
// the operator cannot see.
//
// getenv is a parameter rather than os.Getenv so the refusals can be tested
// without a process environment (history.go's runHistorySweep takes it the
// same way).
func serveCredentials(ctx context.Context, cmd *cobra.Command, getenv func(string) string) error {
	if err := refuseHTTP2Debug(getenv); err != nil {
		return err
	}
	cfg, err := credentialsServiceConfig(getenv)
	if err != nil {
		return err
	}
	return hostCredentialsService(ctx, cfg, getenv, cmd.ErrOrStderr())
}

// credentialsServiceConfig builds the service's configuration out of the
// deployment's environment, and it is the whole of this task's wiring.
//
// WHAT IT CAN BUILD, AND WHAT IT REFUSES TO INVENT.
//
// The three names the installation module sets on the service
// (credentialsServiceEnv, credentials.tf) are the Fugaro project, the GCP
// project and the region, and an empty one is a deployment that is wrong
// rather than a deployment with a default -- Terraform writes each of them
// from a required input -- so each is refused by name.
//
// They are not enough to build a credsvc.Config, and that is a fact about the
// deployment rather than about this command. Of the things NewServer requires,
// exactly one is derivable from them: the bundle loader reads the one
// project-wide container of the logical name creds-bundle
// (credsvc.BundleSecretID(FUGARO_PROJECT)) in FUGARO_GCP_PROJECT, as the
// service's own account, which is the one Secret Manager grant it holds (Q6).
// The rest are facts of the installation that the module does not put in this
// container's environment:
//
//   - the installation's realtime database URL, which the policy reader, the
//     cascade's state, the operator stop switches, the probe leases and the
//     write-once outcome reader are all built on (the history job's
//     FUGARO_RTDB_URL, history.go);
//   - the Firebase project, which is the aud and iss a run's identity is
//     verified against (FUGARO_FIREBASE_PROJECT; it is not the Fugaro
//     project's name and it is not the GCP project's);
//   - the service's own URL, which is the audience every caller's Google ID
//     token is minted for (the runner's FUGARO_CREDENTIALS_SERVICE_URL) --
//     and a Cloud Run service's URL is not a function of its name, region and
//     the Fugaro project's name;
//   - the installation's job service accounts, one per (repository, workflow)
//     (credsvc.JobAccountsFor over the repositories and workflows the
//     installation deployed). A slug is a hash of the repository
//     (task.Slug), so this table cannot be recovered from the database's
//     published slugs, and an empty one refuses every caller.
//
// Guessing any of them is what this command must not do. A wrong database
// URL, a wrong audience or an invented account table is a service that either
// refuses the runs it exists to serve or -- worse, and the reason every
// refusal here fails closed -- admits one it should not: the audience is what
// binds a caller's ID token to *this* service, and the account table is the
// only part of "which job is this run in" a caller cannot write. So this
// refuses, by name, listing all four and the change that has to carry them,
// and nothing is started: a revision that fails to start is what tells an
// owner the image they deployed cannot serve, while a service that answered
// every call with a refusal would look healthy and halt every run that
// reached it (D143).
//
// It is a named refusal and not a comment because what lifts it is a small,
// nameable change -- but not a deployment setting an operator can add. The
// cover's credentialsServiceEnv allow-list admits exactly the three names
// above and refuses a fourth at plan time (internal/infra/tf/cover.go; the
// plan records the rule as a security boundary), and credentials.tf sets
// exactly those three, so the four facts cannot arrive on the container
// without this command's own builder, that allow-list and credentials.tf
// moving together. No task in the plan records that change: C1.18, which owns
// the container, has landed with its three names and its allow-list. This
// command's contract does not change when the change lands: the three names
// stay required, the refusals stay by name, and hostCredentialsService below
// is already the host those facts feed.
func credentialsServiceConfig(getenv func(string) string) (credsvc.Config, error) {
	// The three names credentials.tf sets on the service, and the allow-list
	// init's cover holds its container to (credentialsServiceEnv). An empty
	// variable is not a value the module can set: Terraform writes each of
	// these from a required input, so an empty one is a deployment that is
	// wrong and not a deployment with a default.
	env := map[string]string{}
	for _, k := range []string{envProject, envGCPProject, envRegion} {
		if env[k] = getenv(k); env[k] == "" {
			return credsvc.Config{}, userErr("the credentials service needs %s in its environment (the installation module sets it on the service)", k)
		}
	}
	return credsvc.Config{}, userErr("the credentials service cannot be configured from this deployment: the installation module sets %s, %s and %s on the service and nothing else, and the service needs four more facts of the installation -- its realtime database URL, its Firebase project, its own URL (the audience its callers' ID tokens are minted for) and the job service accounts of its repositories and workflows. None is derivable from the three: a Cloud Run URL is not a function of a name and a region, a repository's slug is a hash, and a guessed audience or account table is a service that admits a caller it should not. Nothing was started and no credential was issued. This is not a variable an operator can add to the container: internal/infra/tf's credentialsServiceEnv allow-list admits exactly these three names and refuses a fourth at plan time, so the change is this command's own config builder, that allow-list and credentials.tf together -- and no task in the plan records it",
		envProject, envGCPProject, envRegion)
}

// The host's own bounds. A proxied model call is long-lived -- it streams a
// completion that can take minutes, and Cloud Run's request timeout is set to
// the hour the module allows (credentials.tf, Check 91 measures what its front
// end does with one) -- so there is deliberately no WriteTimeout and no
// ReadTimeout: either would cut a call in flight, which is a run that fails
// for a reason nothing in its record explains. What is bounded is the part
// that cannot be long-lived: a client that has opened a connection and not
// sent its request, and a connection left idle between calls.
const (
	// serveReadHeaderTimeout bounds a caller that connects and then says
	// nothing. Every request the service answers is a small JSON POST or a
	// streamed one, and the headers of both arrive at once.
	serveReadHeaderTimeout = 30 * time.Second
	// serveIdleTimeout is how long a kept-alive connection with no request on
	// it is held. The runner's gateway calls the service repeatedly over one
	// job, so it is generous.
	serveIdleTimeout = 2 * time.Minute
	// serveShutdownGrace is how long a SIGTERM lets the calls in flight finish
	// before the process returns. Cloud Run sends SIGTERM and then kills the
	// container ten seconds later, so this is under that: a stream cut off by
	// the platform is a run that halts with a reason it cannot see, and one
	// that ends inside the grace is a run that reads a whole answer.
	serveShutdownGrace = 8 * time.Second
	// serveMaxHeaderBytes bounds what a caller may send before the body. The
	// wire contract's own headers are small and bounded (internal/credsvc's
	// request reader holds them to their sizes); this is the outer bound, and
	// it is half Go's default because nothing this service answers has a
	// header near it.
	serveMaxHeaderBytes = 1 << 19
	// defaultServePort is what PORT falls back to when it is unset. Cloud Run
	// always sets PORT; the fallback is for a developer running the binary by
	// hand, and it is the port the platform's own documentation uses.
	defaultServePort = "8080"
)

// hostCredentialsService runs the service on the port the platform gives it
// (PORT) until ctx is cancelled, then drains the calls in flight.
//
// It is a function of the built configuration rather than of the environment,
// so it is the same host whatever the deployment's variables turn out to be,
// and so a test can start it with a server of its own and stop it again
// (TestCredentialsServeHostStopsOnSignal).
func hostCredentialsService(ctx context.Context, cfg credsvc.Config, getenv func(string) string, errw io.Writer) error {
	srv, err := credsvc.NewServer(cfg)
	if err != nil {
		// NewServer names the missing piece; it is this process's deployment
		// that is wrong, so it is a user error and not a remote one.
		return userErr("the credentials service was not started: %v", err)
	}
	port, err := servePort(getenv)
	if err != nil {
		return err
	}
	// The listener is bound here rather than by ListenAndServe so that the
	// port is known to be this process's before anything is served: a
	// deployment whose PORT is taken by another process has to fail at
	// startup, not answer the platform's health check on a socket it does not
	// own.
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return userErr("the credentials service cannot listen on PORT %s: %v", port, err)
	}
	return serveUntilStop(ctx, ln, credentialsHTTPServer(ctx, srv, errw), errw)
}

// credentialsHTTPServer is the host's own shape, and a function of the handler
// rather than of the service so that the bounds below are the same object
// whoever is served -- and so that a test can drive a real listener, request
// and shutdown through them without a service's worth of fakes
// (TestCredentialsServeHostStopsOnSignal).
func credentialsHTTPServer(ctx context.Context, h http.Handler, errw io.Writer) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: serveReadHeaderTimeout,
		IdleTimeout:       serveIdleTimeout,
		MaxHeaderBytes:    serveMaxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		// Nothing here prints a request, a header or a body: this logger
		// carries Go's own connection-level lines (a client that hung up
		// mid-request, a TLS handshake failure) and nothing this service
		// decided.
		ErrorLog: slog.NewLogLogger(slog.NewTextHandler(errw, nil), slog.LevelWarn),
	}
}

// serveUntilStop serves ln until ctx is cancelled, then drains the calls in
// flight.
//
// The cancellation is the platform's SIGTERM (the command's own context) or a
// test's, and the drain is the whole point of it: a proxied model call can be
// streaming a minute of completion when the revision is replaced, and a
// process that returned at once would cut every one of them. What it does not
// do is report a drain that timed out as a failure: the platform kills the
// container seconds after the signal whatever this says, the run whose call
// was cut off reads its own refusal from the closed connection, and a
// non-zero exit from a revision the platform is replacing anyway is a log
// line that says a healthy shutdown failed.
func serveUntilStop(ctx context.Context, ln net.Listener, srv *http.Server, errw io.Writer) error {
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	fmt.Fprintf(errw, "credentials service: listening on %s\n", ln.Addr())
	select {
	case err := <-done:
		// Serve returned on its own: the listener failed or was closed under
		// it. Either way the service is not serving, and the process must not
		// pretend otherwise.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return remote(fmt.Errorf("the credentials service stopped: %w", err))
		}
		return nil
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), serveShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		fmt.Fprintf(errw, "credentials service: shutdown did not drain every call in %s: %v\n", serveShutdownGrace, err)
	}
	return nil
}

// servePort reads PORT -- the port Cloud Run gives the container, and the one
// the platform's health check and its front end reach this process on. A value
// that is not a port is a deployment that is wrong, so it is refused by name
// and never defaulted: a service listening somewhere the front end does not
// reach is a service that looks started and answers nothing.
func servePort(getenv func(string) string) (string, error) {
	raw := getenv("PORT")
	if raw == "" {
		return defaultServePort, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 65535 {
		return "", userErr("PORT is %q, which is not a TCP port", oneLine(raw))
	}
	return strconv.Itoa(n), nil
}
