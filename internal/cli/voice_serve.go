// voice_serve.go is `fugaro voice serve`: the voice hop (P5.3.3), the third
// hidden in-container process of this binary, beside the credentials
// service's and the intake's.
//
// WHY A HIDDEN SUBCOMMAND and not a main of its own: this repository builds
// one image per process and one binary inside it, and both of its other
// in-container entry points are hidden subcommands of this binary (the
// credentials service, the intake). A Cloud Run *service* sets neither a
// command nor args -- the image's own entrypoint is what runs, and the
// Dockerfile's CMD is what names the subcommand -- and internal/infra/tf's
// cover case for google_cloud_run_v2_service refuses a plan whose container
// sets either. A second main would be a second binary for Cloud Build to
// build, publish and digest-pin, serving a process that shares this one's
// environment, signals, port and log (intake_serve.go's own reason).
//
// WHAT THIS PROCESS IS. It verifies the phone's Firebase ID token, checks that
// the device node is still there, enforces the project's voice policy and
// answers the project-status query. It forwards nothing: the provider call,
// the key and the register are P5.3.5's half, in the same directory at a later
// level. internal/voicesvc is the whole of the service, and this file is only
// the two things a process needs and a package cannot have: the deployment's
// environment, and a listener.
//
// THE ENVIRONMENT IS TWO NAMES, and voiceServiceConfig below is the one place
// they are read: FUGARO_FIREBASE_PROJECT (the audience and the issuer a
// device's ID token must carry, which is also the project whose database the
// two reads are) and FUGARO_RTDB_URL (that database, refused unless it is the
// Firebase project's own <project>-default-rtdb). Neither is a credential.
//
// WHAT IT DOES NOT READ is as much of the design as what it does. There is no
// job roster, no verifier or launcher list, no Firebase key file and no
// provider key: the hop serves no run and no person, it verifies a device
// against Google's published certificates, and it holds no provider credential
// until P5.3.5's forwarding half -- so the whole of what a compromised
// revision of this process brings with it is the database read below and
// nothing else. A missing name is a refusal by name and never a default: a
// process that does not know which Firebase project mints the identities it
// admits, or which database the device nodes live in, must not serve at all
// (D143's fail-closed half, credentialsServiceConfig's own rule). No refusal
// below names a value: this process's log is Cloud Logging and the
// environment is the deployment's.
//
// THE ONE GRANT, STATED AS THE GRANT IT IS. The revision runs as the service
// account voice.tf creates, and the whole of that account's ability is the
// one-permission reader fugaroStateRead -- firebasedatabase.instances.get,
// held in internal/infra/tf's RolePermissions pin -- which is a read of ANY
// node of the Realtime Database instance, because Realtime Database IAM has no
// path-level scope. So the account reaches every device node, every policy
// node, creds/ and the mailbox, and what keeps this process to the three nodes
// it uses is internal/voicesvc's own code and not the grant
// (internal/voicesvc/status.go, policy.go). SECURITY.md states it; the
// reviewer tier of P5.3.3 exists to weigh it.
//
// AND THE FENCE IS THIS PROCESS. Cloud Run's own invoker check cannot admit a
// phone: a device presents a *Firebase ID token*, which is not a Google IAM
// principal, so there is no principal to write an invoker binding for -- and
// the alternative, an installation-issued Google credential compiled into a
// phone, is the third credential item on a device that the owner's ruling of
// 2026-10-19 refused. The service is therefore reachable without an IAM
// principal and the check that admits a caller is VerifyDevice, inside this
// process. That is why the identity check is the whole fence rather than the
// second of two, and why it is the first thing the handler does
// (internal/voicesvc/server.go's status).

package cli

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/voicesvc"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// newVoiceCmd is the hidden `voice` group: the process, and nothing an
// operator is offered.
func newVoiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "voice",
		Short:  "Run the voice hop (internal; the hop's entrypoint)",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	cmd.AddCommand(newVoiceServeCmd())
	return cmd
}

// newVoiceServeCmd is `fugaro voice serve`. Like the other two processes' own
// entry points it has no flags at all, not even a --port: Cloud Run gives the
// container the port it must listen on, and a flag would be a second way to
// start a process whose whole configuration is the platform's.
func newVoiceServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the voice hop in its image (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Cloud Run stops a revision by sending SIGTERM and killing the
			// container seconds later, so the signal is a request to stop
			// serving rather than to die: the context below is what the host
			// watches, and it drains the calls in flight before it returns
			// (hostVoiceService). A call cut off mid-answer is a phone that
			// cannot tell a refusal from an outage.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveVoice(ctx, cmd, os.Getenv)
		},
	}
}

// serveVoice starts the hop.
//
// The order is serveCredentials' own, and for its reason: first the http2debug
// refusal, which is every command of this repository's that opens or handles a
// credential (refuseHTTP2Debug: GODEBUG=http2debug makes Go print HTTP
// requests, headers included, to stderr -- and this process handles a caller's
// ID token), then the environment the module sets, and only then the service.
// getenv is a parameter rather than os.Getenv so the refusals can be tested
// without a process environment.
func serveVoice(ctx context.Context, cmd *cobra.Command, getenv func(string) string) error {
	if err := refuseHTTP2Debug(getenv); err != nil {
		return err
	}
	cfg, err := voiceServiceConfig(ctx, getenv, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	return hostVoiceService(ctx, cfg, getenv, cmd.ErrOrStderr())
}

// voiceWiring points the configuration builder at a stand-in for the one
// thing it reaches Google for as the process's own identity: the OAuth2 token
// source the database is read with. It is a test seam for credentialsWiring's
// reason exactly: without one, building the configuration would need
// Application Default Credentials and a live metadata server, and the test
// that matters most -- that a full environment builds a configuration
// voicesvc.NewServer accepts -- could not be written. It is never set outside
// a test, and it carries no value: it is one constructor.
//
// The key source needs no seam: credsvc.NewGoogleKeys fetches nothing until a
// token is verified, and what it fetches then is Google's published
// certificates, which is a fact of the internet and not of this installation.
type voiceWiring struct {
	tokens func(ctx context.Context) (oauth2.TokenSource, error)
}

// voiceTest is nil in production: the function above is then
// google.DefaultTokenSource over the database scopes, over the service account
// voice.tf grants the instance read to.
var voiceTest *voiceWiring

// voiceServiceConfig builds the hop's configuration out of the deployment's
// environment, and it is the whole of this process's wiring.
//
// The two names are read in the order of what an operator has to be told, and
// each is refused by name before anything is built. The database URL is held
// to two things: that it is a Realtime Database URL at all (rtdb.ValidateURL,
// the history job's own check), and that it is THIS Firebase project's
// database (credentialsServiceDatabase, which is checkDatabaseHost's rule --
// the database is named after the project it belongs to). A pair that
// disagrees is a hop reading one installation's device nodes while verifying
// another's identities, which is what a copied deployment's environment looks
// like.
//
// What is built here is the three parts internal/voicesvc refuses to serve
// without: the verifier, the device reader and the enforcer. The verifier's
// key source is credsvc.NewGoogleKeys -- the credentials service's own cache
// of Google's published certificates for Firebase ID tokens -- through
// voiceKeySource below, and the reader and the enforcer share ONE rtdb.Client,
// because they are one database read as this process's one identity.
func voiceServiceConfig(ctx context.Context, getenv func(string) string, errw io.Writer) (voicesvc.Config, error) {
	firebaseProject := getenv(envFirebaseFP)
	if firebaseProject == "" {
		return voicesvc.Config{}, userErr("the voice hop needs %s in its environment (the installation module sets it on the service, and it is the project whose identities this hop verifies and whose database it reads: there is no flag, no config file and no default)", envFirebaseFP)
	}
	rawURL := getenv(envRTDBURL)
	if rawURL == "" {
		return voicesvc.Config{}, userErr("the voice hop needs %s in its environment: it is the database the device nodes and the project policy are read from, and there is no flag, no config file and no default", envRTDBURL)
	}
	if err := rtdb.ValidateURL(rawURL, false); err != nil {
		return voicesvc.Config{}, userErr("%s is not a realtime database URL: it must be https://<database>.firebaseio.com or https://<database>.<region>.firebasedatabase.app, with no credentials, path, query or fragment. Nothing was started and no credential was issued", envRTDBURL)
	}
	if err := credentialsServiceDatabase(rawURL, firebaseProject); err != nil {
		return voicesvc.Config{}, userErr("%v", err)
	}

	tokens := func(ctx context.Context) (oauth2.TokenSource, error) {
		// The same two scopes the credentials service and the history job read
		// the database with: this process reads the same nodes, as its own
		// service account rather than as a run.
		return google.DefaultTokenSource(ctx, budgetScopes...)
	}
	if voiceTest != nil && voiceTest.tokens != nil {
		tokens = voiceTest.tokens
	}
	ts, err := tokens(ctx)
	if err != nil {
		return voicesvc.Config{}, remote(fmt.Errorf("no credential for the installation's database, so the voice hop cannot read the device nodes or the policy: %w", err))
	}
	// The client's own refusal names the URL it was handed, which is what
	// FUGARO_RTDB_URL held -- credentialsServiceConfig's rule for the same
	// reason (this process's log is Cloud Logging), so it is rewritten here
	// rather than reported.
	db, err := rtdb.New(rawURL, rtdb.Auth{Source: ts})
	if err != nil {
		return voicesvc.Config{}, userErr("%s could not be used as the installation's database, as the voice hop's own service account", envRTDBURL)
	}

	auth, err := voicesvc.NewAuthenticator(voicesvc.AuthConfig{
		FirebaseProject: firebaseProject,
		Keys:            voiceKeySource{keys: credsvc.NewGoogleKeys()},
	})
	if err != nil {
		return voicesvc.Config{}, userErr("the voice hop cannot verify a device's identity, so it will not start: %v", err)
	}
	enforcer, err := voicesvc.NewEnforcer(voicesvc.RTDBVoice{DB: db})
	if err != nil {
		return voicesvc.Config{}, userErr("the voice hop cannot enforce the project policy, so it will not start: %v", err)
	}
	// The process's own log, which is the stream Cloud Logging reads: one line
	// per call, and no byte of any body (internal/voicesvc's callLog).
	return voicesvc.Config{
		Auth:     auth,
		Devices:  voicesvc.RTDBDevices{DB: db},
		Enforcer: enforcer,
		Log:      runner.NewLogger(errw),
	}, nil
}

// voiceKeySource is the device verifier's key source over the credentials
// service's own cache of Google's published certificates for Firebase ID
// tokens (credsvc.GoogleKeys). The two packages cannot share one type -- the
// hop depends on the SHAPE of the check and not on the credentials service
// (internal/voicesvc/auth.go's KeySource) -- so this is the one line where the
// two meet, and it exists for a reason that is more than plumbing.
//
// The two packages' sentinels are two errors, and the difference they carry is
// the difference between a caller's failure and this service's own: a key id
// Google does not publish is a REFUSAL OF THE TOKEN (401, and the app drops
// the token it holds), while a key set that could not be read is STATE (503,
// and the app serves the speech locally and carries on). credsvc's own
// sentinel is the first of those, and the hop's ErrUnknownKeyID is what its
// verifier switches on, so without this bridge a rotation would be answered as
// an outage for as long as the cache's life ran -- which is the failure the
// credentials service's re-fetch-on-unknown-kid is written to avoid. Nothing
// else is translated: every other error stays whatever it is, which is the
// hop's state direction.
type voiceKeySource struct {
	keys credsvc.KeySource
}

func (s voiceKeySource) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	key, err := s.keys.Key(ctx, kid)
	if err != nil && errors.Is(err, credsvc.ErrUnknownKeyID) {
		return nil, fmt.Errorf("%w: %w", voicesvc.ErrUnknownKeyID, err)
	}
	return key, err
}

// hostVoiceService runs the hop on the port the platform gives it (PORT) until
// ctx is cancelled, then drains the calls in flight.
//
// It is hostIntakeService's own shape over this process's handler: the same
// listener-first ordering (a port already taken has to fail at start-up, not
// answer the platform's health check on a socket this process does not own),
// and the same http.Server (credentialsHTTPServer), because the three
// processes are one kind of thing -- a Cloud Run service behind the same front
// end -- and their transport bounds are a fact about that front end rather
// than about what each one serves.
func hostVoiceService(ctx context.Context, cfg voicesvc.Config, getenv func(string) string, errw io.Writer) error {
	srv, err := voicesvc.NewServer(cfg)
	if err != nil {
		// NewServer names the missing piece; it is this process's deployment
		// that is wrong, so it is a user error and not a remote one.
		return userErr("the voice hop was not started: %v", err)
	}
	port, err := servePort(getenv)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return userErr("the voice hop cannot listen on PORT %s: %v", port, err)
	}
	return serveUntilStopAs(ctx, ln, credentialsHTTPServer(ctx, srv, errw), errw, voiceProcessName)
}

// voiceProcessName is what this process's two log lines call themselves: the
// name Cloud Logging shows beside them, and the one thing that tells the hop's
// stream apart from the credentials service's and the intake's when an
// operator reads one installation's logs.
const voiceProcessName = "voice hop"
