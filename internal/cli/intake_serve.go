// intake_serve.go is `fugaro intake serve`: the intake process (DS2.1), the
// second hidden in-container process of this binary, beside the credentials
// service's.
//
// Why a hidden subcommand of this binary and not the `cmd/fugaro-intake` main
// the plan names first: this repository builds ONE image per process and one
// binary inside it, and both of its other in-container entry points are hidden
// subcommands of this binary (the history sweep, the credentials service). A
// Cloud Run *service* sets neither a command nor args, so the image's own
// entrypoint is what runs and the Dockerfile's CMD is what names the
// subcommand; internal/infra/tf's cover case for google_cloud_run_v2_service
// refuses a plan whose container sets either, which is the same shape the
// credentials service's command is hidden for. A second main would be a second
// binary for Cloud Build to build, publish and digest-pin, serving one process
// that shares this one's environment, signals, port and log — and the plan's
// own line admits this route ("or the existing credentials serve pattern in
// internal/cli/credentials_serve.go, which this task reads first and follows").
//
// WHAT THIS PROCESS IS, IN ONE PARAGRAPH. It is the only writer of the
// per-user and project-shared secret containers, and it verifies its callers
// itself: Cloud Run grants a launcher invoker on the whole service (F36), so
// the launcher class's check at the route is the fence, and this process holds
// neither the installation's job accounts nor its Firebase key set nor any
// database role (D1940). Everything it can do is in internal/intake.
//
// The environment is four names, and intakeServiceConfig below is the one
// place they are read: FUGARO_GCP_PROJECT, FUGARO_INTAKE_URL,
// FUGARO_LAUNCHER_ACCOUNTS and FUGARO_RUNS_BUCKET. An installation whose
// deployment names one of them twice, or names a fifth, is DS2.2's business
// (the Terraform half of this feature): nothing here reads a variable it does
// not need, and nothing here prints one.

package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/runner"

	"github.com/spf13/cobra"
)

// newIntakeCmd is the hidden `intake` group: the process, and nothing an
// operator is offered.
func newIntakeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "intake",
		Short:  "Run the secrets intake (internal; the intake image's entrypoint)",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	cmd.AddCommand(newIntakeServeCmd())
	return cmd
}

// newIntakeServeCmd is `fugaro intake serve`. Like the credentials service's
// own entry point it has no flags at all, not even a --port: Cloud Run gives
// the container the port it must listen on, and a flag would be a second way
// to start a process whose whole configuration is the platform's.
func newIntakeServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the secrets intake in its image (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Cloud Run stops a revision by sending SIGTERM and killing the
			// container seconds later, so the signal is a request to stop
			// serving rather than to die: the context below is what the host
			// watches, and it drains the calls in flight before it returns
			// (hostIntakeService). A credential being written when the revision
			// is replaced must land or be refused, never half-write.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveIntake(ctx, cmd, os.Getenv)
		},
	}
}

// serveIntake starts the intake.
//
// The order is serveCredentials' own, and for its reason: first the
// http2debug refusal, which is every command of this repository's that opens
// or handles a credential (refuseHTTP2Debug: GODEBUG=http2debug makes Go print
// HTTP requests, headers included, to stderr), then the environment the module
// sets, and only then the service. A deployment that is wrong must be told so
// by name rather than by a failure to start for a reason the operator cannot
// see. getenv is a parameter rather than os.Getenv so the refusals can be
// tested without a process environment.
func serveIntake(ctx context.Context, cmd *cobra.Command, getenv func(string) string) error {
	if err := refuseHTTP2Debug(getenv); err != nil {
		return err
	}
	cfg, err := intakeServiceConfig(ctx, getenv, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	return hostIntakeService(ctx, cfg, getenv, cmd.ErrOrStderr())
}

// envIntakeURL is the intake's own URL: the audience every caller's Google ID
// token must carry, and the one name the intake does not share with the
// credentials service (this is a second Cloud Run service, so its URL is its
// own).
const envIntakeURL = "FUGARO_INTAKE_URL"

// intakeWiring points the configuration builder at stand-ins for the two
// things it reaches Google for as the process's own identity: the Secret
// Manager client the containers are written through, and the runs bucket the
// derived admin list is read from. It is a test seam for credentialsWiring's
// reason exactly: without one, building the configuration would need
// Application Default Credentials and a live metadata server, and the test
// that matters most — that a full environment builds a configuration
// intake.New accepts — could not be written. It is never set outside a test,
// and it carries no value: it is two constructors.
type intakeWiring struct {
	secrets func(ctx context.Context, project string) (intake.SecretStore, error)
	bucket  func(ctx context.Context, name string) (*blobx.Bucket, error)
}

// intakeTest is nil in production: the two functions above are then Secret
// Manager's own client and the runs bucket, both over the identity the
// revision runs as.
var intakeTest *intakeWiring

// intakeServiceConfig builds the intake's configuration out of the
// deployment's environment, and it is the whole of this task's wiring.
//
// WHAT IT READS, AND WHY EACH NAME IS THERE.
//
// The set is four, and every one of them is a fact of the installation that
// cannot be derived from the others, and none of them is a credential:
//
//   - FUGARO_GCP_PROJECT is the GCP project the containers live in: the Secret
//     Manager project, which is the installation's.
//   - FUGARO_INTAKE_URL is this service's own URL, which is the audience a
//     caller's Google ID token is minted for — the one thing that binds such a
//     token to *this* service. Cloud Run assigns it, so it is passed rather
//     than derived, and a service that guessed one would admit nobody, or
//     worse, admit a token minted for another service.
//   - FUGARO_LAUNCHER_ACCOUNTS is the launcher class's list (D1938, F25): the
//     roster's people, as the bare addresses a Google ID token's email claim
//     carries. It is READ AS OPTIONAL, by credentialsServiceLaunchers' rule
//     and for its reason — the module renders the entry only on an
//     installation whose roster names a launcher, and one that names none must
//     still start, with a class that admits nobody (fail closed), rather than
//     be a service that will not bind a port.
//   - FUGARO_RUNS_BUCKET is the runs bucket holding the derived admin list
//     (access.PathAdminList, DS2.0). It is the intake's only reach beyond
//     Secret Manager and it is an object read: the intake holds no database
//     role (D1940), so the roster reaches it as the document `fugaro update`
//     publishes rather than over the database.
//
// WHAT IT DOES NOT READ is as much of the design as what it does. The job
// accounts, the Firebase project and key set, the database URL and the policy
// are what the credentials service needs to check a RUN's identity and serve
// it a bundle; the intake serves no run, admits one class over Google's own
// ID-token key set and writes containers. An absent FUGARO_GCP_PROJECT or
// FUGARO_RUNS_BUCKET is a refusal rather than a default: a process that does
// not know which project it writes in, or cannot read who the admins are, must
// not serve at all (D143's fail-closed half).
//
// A REFUSAL NAMES A VARIABLE AND NEVER A VALUE. This process's log is Cloud
// Logging, and the environment is the deployment's: every message below names
// the variable and the shape it must have, never what it held (the no-echo
// rule, credentialsServiceConfig's own).
func intakeServiceConfig(ctx context.Context, getenv func(string) string, errw io.Writer) (intake.Config, error) {
	project := getenv(envGCPProject)
	if project == "" {
		return intake.Config{}, userErr("the secrets intake needs %s in its environment (the installation module sets it on the service, and it is the project the callers' containers live in: there is no flag, no config file and no default)", envGCPProject)
	}
	audience := getenv(envIntakeURL)
	if audience == "" {
		return intake.Config{}, userErr("the secrets intake needs %s in its environment (the installation module sets it to this service's own URL, which is the audience a caller's Google ID token is minted for: there is no flag, no config file and no default)", envIntakeURL)
	}
	if err := intakeServiceAudience(audience); err != nil {
		return intake.Config{}, userErr("%v", err)
	}
	launchers, err := credentialsServiceLaunchers(getenv(envLauncherAccounts))
	if err != nil {
		return intake.Config{}, userErr("%v", err)
	}
	bucketName := getenv(envRunsBucket)
	if bucketName == "" {
		return intake.Config{}, userErr("the secrets intake needs %s in its environment: the derived admin list (the roster's project admins) is a bucket object, and the intake holds no database role to read the roster itself with", envRunsBucket)
	}
	// The bucket is addressed the way this repository addresses it: the module
	// passes the bucket's own name and the scheme is GCS's (openBkt,
	// history_rollover.go). A name carrying a separator or a space is a
	// deployment that would reach somewhere else, and it is refused here
	// rather than resolved into a URL.
	if strings.ContainsAny(bucketName, "/: ") {
		return intake.Config{}, userErr("%s is not the runs bucket's name: the installation module passes the bucket's own name (fugaro-runs-<gcp-project>), never a URL", envRunsBucket)
	}

	secrets := func(ctx context.Context, project string) (intake.SecretStore, error) {
		return intake.NewSecretManager(ctx, project)
	}
	bucket := func(ctx context.Context, name string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "gs://"+name)
	}
	if intakeTest != nil {
		if intakeTest.secrets != nil {
			secrets = intakeTest.secrets
		}
		if intakeTest.bucket != nil {
			bucket = intakeTest.bucket
		}
	}

	callers, err := credsvc.NewLaunchers(credsvc.LauncherConfig{
		Audience: audience,
		Accounts: launchers,
		// Google's published ID-token key set and not the Firebase set: this
		// class is a person's own `gcloud` identity, and the Firebase keys sign
		// the run identities this process does not hold.
		Keys: credsvc.NewGoogleKeys(credsvc.WithKeysEndpoint(credsvc.GoogleOAuthCertsURL)),
	})
	if err != nil {
		return intake.Config{}, userErr("the secrets intake cannot verify its callers, so it will not start: %v", err)
	}
	store, err := secrets(ctx, project)
	if err != nil {
		return intake.Config{}, remote(fmt.Errorf("no Secret Manager client for the installation's containers: %w", err))
	}
	objects, err := bucket(ctx, bucketName)
	if err != nil {
		return intake.Config{}, remote(fmt.Errorf("the runs bucket holding the derived admin list could not be opened: %w", err))
	}
	list, err := intake.NewBucketAdminList(objects)
	if err != nil {
		return intake.Config{}, userErr("the secrets intake cannot read the derived admin list, so it will not start: %v", err)
	}
	// The process's own log, which is the stream Cloud Logging reads: the
	// auditor writes one line per call onto it, and nothing else in this
	// process writes a request, a header or a body.
	log := runner.NewLogger(errw)
	audit, err := intake.NewAuditor(log.Handler())
	if err != nil {
		return intake.Config{}, userErr("the secrets intake cannot audit its calls, so it will not start: %v", err)
	}
	return intake.Config{Callers: callers, Admins: list, Secrets: store, Audit: audit}, nil
}

// intakeServiceAudience checks the service's own URL, which is the audience
// every caller's Google ID token carries. It is credentialsServiceAudience's
// rule — an https URL with no path, query, fragment or userinfo — because both
// processes are Cloud Run revisions whose audience is exactly their own URL,
// and an audience with a path in it is one no token this platform mints would
// match, which reads as a broken deployment rather than as the
// misconfiguration it is.
func intakeServiceAudience(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "https" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%s is not the intake's own URL: it must be the https URL Cloud Run serves this revision on, with no path, query or fragment (a caller's Google ID token is minted for exactly that audience, so anything else admits nobody). Nothing was started and no credential was issued", envIntakeURL)
	}
	return nil
}

// hostIntakeService runs the intake on the port the platform gives it (PORT)
// until ctx is cancelled, then drains the calls in flight.
//
// It is hostCredentialsService's own shape, over this process's handler: the
// same listener-first ordering (a port already taken has to fail at start-up,
// not answer the platform's health check on a socket this process does not
// own), and the same http.Server (credentialsHTTPServer), because the two
// processes are one kind of thing — a Cloud Run service behind the same front
// end — and their transport bounds are a fact about that front end rather than
// about what each one serves. What each one SPEAKS is the same too:
// credentialsServeProtocols' HTTP/1.1 and h2c.
func hostIntakeService(ctx context.Context, cfg intake.Config, getenv func(string) string, errw io.Writer) error {
	srv, err := intake.New(cfg)
	if err != nil {
		// New names the missing piece; it is this process's deployment that is
		// wrong, so it is a user error and not a remote one.
		return userErr("the secrets intake was not started: %v", err)
	}
	port, err := servePort(getenv)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return userErr("the secrets intake cannot listen on PORT %s: %v", port, err)
	}
	return serveUntilStopAs(ctx, ln, credentialsHTTPServer(ctx, srv, errw), errw, intakeProcessName)
}

// intakeProcessName is what this process's two log lines call themselves: the
// name Cloud Logging shows beside them, and the one thing that tells the
// intake's stream apart from the credentials service's when an operator reads
// the two installations' logs.
const intakeProcessName = "secrets intake"
