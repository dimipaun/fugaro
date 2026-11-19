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
// the device node is still there, enforces the project's voice policy, answers
// the project-status query and — since P5.3.5, the forwarding half — makes the
// call to the provider the register admits, with the key it reads out of the
// installation's provider-key container. internal/voicesvc is the whole of the
// service, and this file is only the things a process needs and a package
// cannot have: the deployment's environment, the clients, and a listener.
//
// THE ENVIRONMENT IS THREE NAMES, and voiceServiceConfig below is the one place
// they are read: FUGARO_FIREBASE_PROJECT (the audience and the issuer a
// device's ID token must carry, which is also the project whose database the
// two reads are), FUGARO_RTDB_URL (that database, refused unless it is the
// Firebase project's own <project>-default-rtdb) and FUGARO_GCP_PROJECT (the
// installation's GCP project, which is where the provider-key container
// lives). None is a credential, and the third names a PROJECT rather than a
// container: the container's ID is a constant of this binary
// (internal/backend/gcp's VoiceProviderKeysSecret, the same literal
// modules/installation/secrets.tf writes), so no secret ID is an environment
// value anywhere in this tree.
//
// WHAT IT DOES NOT READ is as much of the design as what it does. There is no
// job roster, no verifier or launcher list and no Firebase key file: the hop
// serves no run and no person, and it verifies a device against Google's
// published certificates. The ONE credential it holds is the provider keys
// (P5.3.5): a provider key reaches this process out of Secret Manager as this
// service's own account and is presented to the provider and to nobody else —
// never a log line, never a response, never a caller. So the whole of what a
// compromised revision of this process brings with it is the database read
// below and the ability to spend at the providers the register admits, which
// is the spend SECURITY.md discloses as outside every cap. A missing name is a
// refusal by name and never a default: a
// process that does not know which Firebase project mints the identities it
// admits, or which database the device nodes live in, must not serve at all
// (D143's fail-closed half, credentialsServiceConfig's own rule). No refusal
// below names a value: this process's log is Cloud Logging and the
// environment is the deployment's.
//
// THE TWO GRANTS, STATED AS THE GRANTS THEY ARE. The revision runs as the
// service account voice.tf creates, and it holds two. The first is the
// one-permission reader fugaroStateRead -- firebasedatabase.instances.get,
// held in internal/infra/tf's RolePermissions pin -- which is a read of ANY
// node of the Realtime Database instance, because Realtime Database IAM has no
// path-level scope: the account reaches every device node, every policy node,
// creds/ and the mailbox, and what keeps this process to the three nodes it
// uses is internal/voicesvc's own code and not the grant
// (internal/voicesvc/status.go, policy.go). The second is
// roles/secretmanager.secretAccessor on the ONE provider-key container
// (P5.3.5, secrets.tf) and on nothing else of the project, which is the key
// every forwarded call presents. SECURITY.md states both; the reviewer tier of
// P5.3.3 exists to weigh the read, and P5.3.5's carries the key.
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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/voicesvc"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	secretmanager "google.golang.org/api/secretmanager/v1"
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

// voiceWiring points the configuration builder at stand-ins for the three
// things it reaches Google for as the process's own identity: the OAuth2 token
// source the database is read with, the Secret Manager client the provider
// keys are read through, and this service's own access-token source for the
// engine called as the installation rather than with a key. It is a test seam
// for credentialsWiring's reason exactly: without one, building the
// configuration would need Application Default Credentials and a live metadata
// server, and the test that matters most -- that a full environment builds a
// configuration voicesvc.NewServer accepts -- could not be written. It is
// never set outside a test, and it carries no value: three constructors.
//
// The device verifier's key source needs no seam: credsvc.NewGoogleKeys
// fetches nothing until a token is verified, and what it fetches then is
// Google's published certificates, which is a fact of the internet and not of
// this installation.
type voiceWiring struct {
	tokens  func(ctx context.Context) (oauth2.TokenSource, error)
	secrets func(ctx context.Context) (*secretmanager.Service, error)
	mint    func(ctx context.Context) (voicesvc.TokenSource, error)
}

// voiceTest is nil in production: the three functions above are then
// google.DefaultTokenSource over the database scopes, secretmanager.NewService,
// and voicesvc.GoogleTokenSource -- all three over the service account voice.tf
// grants the instance read and the one provider-key container to.
var voiceTest *voiceWiring

// voiceProviderClient is the client every provider call is made with, and it
// is built in the open here because voicesvc.Config refuses a server without
// one (its own comment says why: the TLS configuration and the timeouts of a
// call that carries the installation's provider key are decisions this process
// has to make rather than inherit).
//
// Three decisions, each of them about a credential or about a stream:
//
//   - NO REDIRECT IS FOLLOWED, buildTokenHTTPClient's rule for the same
//     reason one credential over: the request carries the provider's key in an
//     Authorization header, and a followed 307 or 308 would carry that key to
//     whatever host the Location named.
//   - NO WHOLE-CALL TIMEOUT. The answer is a stream that ends when the caller
//     stops speaking (provider.go's engine contract), so a client-level Timeout
//     would cut a long utterance at a fixed second with no way for the phone to
//     tell that from a provider's failure. What bounds the call is the caller's
//     own context -- cancelled the moment the phone hangs up -- and Cloud Run's
//     300s request timeout on the revision.
//   - BOUNDED HANDSHAKES. A provider that accepts the TCP connection and then
//     says nothing would otherwise hold a request open until that 300s. The
//     dial, the TLS handshake and the response header are each given a
//     deadline, and nothing after the header is: the body is the stream above.
//     Compression is off because the body is forwarded as it arrives and this
//     process has no business decompressing a provider's answer.
func voiceProviderClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	t.DialContext = (&net.Dialer{Timeout: voiceProviderDialTimeout, KeepAlive: voiceProviderKeepAlive}).DialContext
	t.TLSHandshakeTimeout = voiceProviderDialTimeout
	t.ResponseHeaderTimeout = voiceProviderHeaderTimeout
	return &http.Client{
		Transport:     t,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

const (
	// voiceProviderDialTimeout bounds reaching a provider and the TLS
	// handshake with it. Fifteen seconds is an order of magnitude over the
	// worst handshake a phone's turn can wait for, and the point is that a
	// black hole is a failure rather than a request held open.
	voiceProviderDialTimeout = 15 * time.Second
	// voiceProviderKeepAlive is the TCP keepalive on a pooled connection: a
	// provider that goes away between turns must not leave a socket this
	// process believes in.
	voiceProviderKeepAlive = 30 * time.Second
	// voiceProviderHeaderTimeout bounds the wait for the provider's response
	// header. Past it the body is streamed and nothing here bounds it (above).
	voiceProviderHeaderTimeout = 30 * time.Second
)

// voiceServiceConfig builds the hop's configuration out of the deployment's
// environment, and it is the whole of this process's wiring.
//
// The three names are read in the order of what an operator has to be told,
// and each is refused by name before anything is built. The database URL is
// held to two things: that it is a Realtime Database URL at all
// (rtdb.ValidateURL, the history job's own check), and that it is THIS Firebase
// project's database (credentialsServiceDatabase, which is checkDatabaseHost's
// rule -- the database is named after the project it belongs to). A pair that
// disagrees is a hop reading one installation's device nodes while verifying
// another's identities, which is what a copied deployment's environment looks
// like. The third name is a GCP project ID and nothing else is checked about
// it: the container inside it is this binary's own constant, so a value naming
// the wrong project is a Secret Manager refusal on the first forwarded call
// rather than a process that would not start. It is refused here when it is
// MISSING, because a hop with no project to read its provider keys from would
// answer every forwarding call with ErrKeyUnavailable and read as a broken
// register.
//
// What is built here is everything internal/voicesvc refuses to serve without:
// the verifier, the device reader, the enforcer, the provider-key source, the
// service's own token source and the HTTP client its provider calls are made
// with. The verifier's key source is credsvc.NewGoogleKeys -- the credentials
// service's own cache of Google's published certificates for Firebase ID
// tokens -- through voiceKeySource below, and the reader and the enforcer share
// ONE rtdb.Client, because they are one database read as this process's one
// identity.
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
	// The project the provider-key container lives in, and never the
	// container: its ID is this binary's constant (gcp.VoiceProviderKeysSecret,
	// the literal modules/installation/secrets.tf writes), which is what keeps
	// a secret ID out of a container's environment (voice.tf's env block, and
	// internal/infra/tf's voiceServiceEnv, which admits exactly this name).
	gcpProject := getenv(envGCPProject)
	if gcpProject == "" {
		return voicesvc.Config{}, userErr("the voice hop needs %s in its environment: it is the installation's GCP project, where the provider-key container every forwarded call is credentialed by lives (%s). Without it no call can be credentialed, so nothing was started and no credential was issued", envGCPProject, gcp.VoiceProviderKeysSecret)
	}

	tokens := func(ctx context.Context) (oauth2.TokenSource, error) {
		// The same two scopes the credentials service and the history job read
		// the database with: this process reads the same nodes, as its own
		// service account rather than as a run.
		return google.DefaultTokenSource(ctx, budgetScopes...)
	}
	// The provider keys and this service's own access tokens. Both are built
	// for the forwarding half (P5.3.5): the first is where a provider's key
	// comes from, and the second is what an engine called as the installation's
	// identity is reached with instead of a key — provider.go's register says
	// which engine is which, and a call to an engine with no key of either kind
	// is refused rather than made half-credentialed.
	secrets := func(ctx context.Context) (*secretmanager.Service, error) {
		return secretmanager.NewService(ctx)
	}
	mint := func(ctx context.Context) (voicesvc.TokenSource, error) {
		return voicesvc.GoogleTokenSource(ctx)
	}
	if voiceTest != nil {
		if voiceTest.tokens != nil {
			tokens = voiceTest.tokens
		}
		if voiceTest.secrets != nil {
			secrets = voiceTest.secrets
		}
		if voiceTest.mint != nil {
			mint = voiceTest.mint
		}
	}
	ts, err := tokens(ctx)
	if err != nil {
		return voicesvc.Config{}, remote(fmt.Errorf("no credential for the installation's database, so the voice hop cannot read the device nodes or the policy: %w", err))
	}
	sm, err := secrets(ctx)
	if err != nil {
		return voicesvc.Config{}, remote(fmt.Errorf("no Secret Manager client, so the voice hop cannot read the provider keys every forwarded call is credentialed by: %w", err))
	}
	minted, err := mint(ctx)
	if err != nil {
		return voicesvc.Config{}, remote(fmt.Errorf("no token source for this service's own identity, so the voice hop cannot reach the engine that is called as the installation rather than with a key: %w", err))
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
		// The provider-key source, and it holds the container's ID from this
		// binary rather than from the environment: Secret is a constant, so a
		// deployment cannot point this process at another container of the
		// project even if it wrote a different FUGARO_GCP_PROJECT -- what it
		// could do is name another project, in which case the read is refused
		// by IAM (the account holds its accessor on one container of one
		// project, secrets.tf).
		Keys: &voicesvc.SecretManagerKeys{
			Project: gcpProject,
			Secret:  gcp.VoiceProviderKeysSecret,
			Service: sm,
		},
		Tokens: minted,
		// What the provider calls are made with, built here rather than
		// defaulted: voiceProviderClient's comment is the three decisions.
		Client: voiceProviderClient(),
		Log:    runner.NewLogger(errw),
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
