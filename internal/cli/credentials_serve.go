package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
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
// on the service (credentials.tf), which is the whole of its configuration.
// `fugaro --help` is where that is checked (TestCredentialsServeHidden).
//
// The environment is the seven names credentials.tf sets, and
// credentialsServiceConfig below is the one place they are read:
// FUGARO_PROJECT, FUGARO_GCP_PROJECT and FUGARO_REGION (the history job's own
// three constants, history.go, read the same way), FUGARO_RTDB_URL and
// FUGARO_FIREBASE_PROJECT (the installation's database and Firebase project,
// which the history job reads too), FUGARO_CREDENTIALS_SERVICE_URL (the
// audience its callers' ID tokens are minted for -- the service resource's own
// uri) and FUGARO_JOB_ACCOUNTS (the installation's per-repository jobs as
// {"provider","repo","workflow"} triples, from which the job service accounts
// are derived). The names are held by a second thing:
// internal/infra/tf's credentialsServiceEnv is the allow-list init's cover
// applies to the service's container, so a name outside it is refused at plan
// time, and the two lists move together -- an environment variable is not a
// path a credential reaches this process by. The bundle secret is deliberately
// not one of them: the service derives its name from FUGARO_PROJECT (C1.11's
// loader, the project-wide container of the logical name "creds-bundle") and
// reads it as its own service account, which is the one grant it holds (Q6,
// Global Constraint 12). Nothing here reads a credential and nothing here
// prints one: the command has no flag that could carry a value, no output of
// its own, and every refusal below names a variable or a missing piece and
// never a value.
//
// A deployment whose environment is incomplete does not serve: the refusal is
// by name, and no port is bound. That is the fail-closed direction this whole
// work is held to (D143: an unreachable service halts a run, and no code path
// falls back to a key in the job). A revision that fails to start is what
// tells an owner the image they deployed cannot serve, while a service that
// answered every call with a refusal would look healthy and halt every run
// that reached it. Guessing a fact is what this command must not do either: a
// guessed audience or account table is a service that admits a caller it
// should not, which is the boundary D135/Q6 exist to keep.
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
	cfg, err := credentialsServiceConfig(ctx, getenv, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	return hostCredentialsService(ctx, cfg, getenv, cmd.ErrOrStderr())
}

// The two names credentials.tf sets that the history job does not have, and
// the two the two commands share (envRTDBURL, envFirebaseFP, envProject,
// envGCPProject, envRegion: history.go).
const (
	// envCredentialsServiceURL is the service's own URL: the audience every
	// caller's Google ID token is minted for. It is not a function of the
	// service's name, the region and the Fugaro project -- Cloud Run assigns
	// it -- so credentials.tf passes the service resource's own uri, and a
	// service that guessed one would accept a token minted for another
	// service (or refuse every caller).
	envCredentialsServiceURL = "FUGARO_CREDENTIALS_SERVICE_URL"
	// envJobAccounts is the installation's per-repository jobs: a JSON array
	// of {"provider","repo","workflow"} objects, one per job whose service
	// account may call the service. The accounts themselves are derived from
	// the triples (credsvc.JobAccountsFor) rather than passed, because the
	// account's name and the repository's slug are both hashes of them and a
	// table of emails would be a second derivation that can drift from the
	// accounts the deployer created.
	envJobAccounts = "FUGARO_JOB_ACCOUNTS"
)

// credentialsServiceEnvNames is the whole of this command's environment: the
// seven names credentials.tf sets on the service's container, and the same set
// as internal/infra/tf's credentialsServiceEnv allow-list, which is the
// module's side of the same rule (init's cover refuses a container that sets a
// name outside it). The two are held together by a test rather than by a
// comment: TestCredentialsServiceEnvIsTheDeploymentsOwn reads credentials.tf's
// own env blocks and requires this list to be exactly what the module sets, so
// a name added on one side and not the other fails rather than drifts -- the
// direction that matters is the one where the module sets a name this command
// does not read, which is a fact an operator would believe was in effect.
var credentialsServiceEnvNames = []string{
	envProject, envGCPProject, envRegion,
	envRTDBURL, envFirebaseFP,
	envCredentialsServiceURL, envJobAccounts,
}

// credentialsWiring points the configuration builder at stand-ins for the two
// things it needs that reach Google with the service's own identity: the token
// source the database is read with, and the Secret Manager client the bundle
// is read through. It is a test seam for the same reason historyTest is
// (history.go): without one, building the configuration would need Application
// Default Credentials and a live metadata server, and the test that matters
// most here -- that a full environment builds a configuration NewServer
// accepts -- could not be written. It is never set outside a test, and it
// carries no value: it is two constructors.
type credentialsWiring struct {
	tokens  func(ctx context.Context) (oauth2.TokenSource, error)
	secrets func(ctx context.Context) (*secretmanager.Service, error)
}

// credentialsTest is nil in production: the two functions above are then
// google.DefaultTokenSource over the database scopes and Secret Manager's own
// client, both over the service's identity (the service account the revision
// runs as, which is the one grant it holds).
var credentialsTest *credentialsWiring

// credentialsServiceConfig builds the service's configuration out of the
// deployment's environment, and it is the whole of this task's wiring.
//
// WHAT IT READS, AND WHY EACH NAME IS THERE.
//
// The set is the seven names credentials.tf sets, and it is minimal in the
// sense that matters: every one of them is a fact of the installation that
// cannot be derived from the others, and none of them is a credential.
//
//   - FUGARO_PROJECT is the Fugaro project: the fp claim a run's identity
//     carries, and the name the bundle container's ID is derived from
//     (credsvc.BundleSecretID).
//   - FUGARO_GCP_PROJECT is the GCP project the container lives in: the
//     Secret Manager project, and the domain of every job account's email.
//   - FUGARO_REGION is the installation's region, which the history job needs
//     and this service keeps for the same reason -- the two processes read
//     one environment, and a name that went missing between them is a
//     deployment that drifted.
//   - FUGARO_RTDB_URL is the realtime database URL the policy reader, the
//     cascade's state, the operator stop switches, the probe leases and the
//     write-once outcome reader are all built on. It is refused unless it is
//     the Firebase project's own database (below).
//   - FUGARO_FIREBASE_PROJECT is the Firebase project a run's identity is
//     verified against: the aud and the iss of its ID token. It is neither of
//     the two projects above, and nothing derives it.
//   - FUGARO_CREDENTIALS_SERVICE_URL is the service's own URL, which is the
//     audience a caller's Google ID token is minted for -- the one thing that
//     binds such a token to *this* service. Cloud Run assigns it, so it is
//     passed rather than derived.
//   - FUGARO_JOB_ACCOUNTS is the installation's per-repository jobs as
//     {"provider","repo","workflow"} triples. It is the only table here that
//     is a list rather than a name, and it cannot be recovered from anything
//     else: an account's ID is a hash of the slug and the workflow
//     (gcp.ServiceAccountID) and the slug is a hash of the provider and the
//     repository (task.Slug). An empty table is refused rather than defaulted
//     -- a service that admits nobody is one an operator would read as
//     healthy while every run halted (D143).
//
// WHAT IT BUILDS, AND WHAT IT REFUSES TO INVENT.
//
// Everything else NewServer requires is built here from those names: the
// database client (as the service's own identity, so every read and write is
// one the revision's service account is allowed), the bundle loader over the
// project-wide creds-bundle container, the verifier with Google's two
// published key sets (Firebase ID tokens for the run, Google ID tokens for the
// caller), the auditor armed with the loader's own redaction, and the
// process's log handler. Nothing is guessed and nothing is defaulted: a
// missing or malformed name is a refusal that names it.
//
// The one set left empty on purpose is the shared project jobs' accounts
// (AuthConfig.SharedJobAccounts): under Global Constraint 6 this service
// admits runs in per-repository jobs only until the phase-3 shared-job rules
// land, and an empty set is that statement rather than a fact the deployment
// has to make. The day those rules land it is a read of the policy this
// service already reads (credsvc.SharedJobAccountsFor), not a variable.
//
// A REFUSAL NAMES A VARIABLE, NEVER A VALUE.
//
// The environment is the deployment's, and this process is the one that holds
// the bundle's values in memory: an error string here is a line in a Cloud
// Logging log an operator reads, so it names the variable and the shape it
// must have and never what it held. That is why the database's own host check
// is rewritten below rather than reported (checkDatabaseHost's message quotes
// the database's name) and why a job triple that cannot be derived is refused
// without the derivation's own error (which quotes the repository).
//
// getenv is a parameter rather than os.Getenv so every refusal can be tested
// without a process environment (history.go's runHistorySweep takes it the
// same way), and errw is where the service's own log lines go: the same writer
// hostCredentialsService logs to, so the auditor and the process share one
// stream.
func credentialsServiceConfig(ctx context.Context, getenv func(string) string, errw io.Writer) (credsvc.Config, error) {
	// The seven names credentials.tf sets on the service, and the allow-list
	// init's cover holds its container to (credentialsServiceEnv). An empty
	// variable is not a value the module can set: Terraform writes each of
	// these from an input the installation has, so an empty one is a
	// deployment that is wrong rather than a deployment with a default.
	env := map[string]string{}
	for _, k := range credentialsServiceEnvNames {
		if env[k] = getenv(k); env[k] == "" {
			return credsvc.Config{}, userErr("the credentials service needs %s in its environment (the installation module sets it on the service, and it is the whole of how this process is configured: there is no flag, no config file and no default)", k)
		}
	}
	// The database's shape, then the database itself: a URL that is not a
	// Firebase Realtime Database at all is a different refusal from one that
	// names another installation's database.
	if err := rtdb.ValidateURL(env[envRTDBURL], false); err != nil {
		return credsvc.Config{}, userErr("%s is not a realtime database URL: it must be https://<database>.firebaseio.com or https://<database>.<region>.firebasedatabase.app, with no credentials, path, query or fragment. Nothing was started and no credential was issued", envRTDBURL)
	}
	if err := credentialsServiceDatabase(env[envRTDBURL], env[envFirebaseFP]); err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	if err := credentialsServiceAudience(env[envCredentialsServiceURL]); err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	accounts, err := credentialsServiceJobAccounts(env[envJobAccounts], env[envProject], env[envGCPProject])
	if err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	emails := make([]string, 0, len(accounts))
	for _, a := range accounts {
		emails = append(emails, a.Email)
	}

	tokens, secrets := credentialsProductionWiring()
	if credentialsTest != nil {
		if credentialsTest.tokens != nil {
			tokens = credentialsTest.tokens
		}
		if credentialsTest.secrets != nil {
			secrets = credentialsTest.secrets
		}
	}
	ts, err := tokens(ctx)
	if err != nil {
		return credsvc.Config{}, remote(fmt.Errorf("no credentials for the installation's database: %w", err))
	}
	db, err := rtdb.New(env[envRTDBURL], rtdb.Auth{Source: ts})
	if err != nil {
		// Unreachable past ValidateURL, and deliberately not reported with the
		// client's own error: that one quotes the URL.
		return credsvc.Config{}, userErr("%s: the database client could not be built (it must be https, and reachable as the service's own service account)", envRTDBURL)
	}
	sm, err := secrets(ctx)
	if err != nil {
		return credsvc.Config{}, remote(fmt.Errorf("no Secret Manager client for the bundle container: %w", err))
	}

	log := runner.NewLogger(errw)
	loader := &credsvc.BundleLoader{Source: credsvc.SecretManagerSource{
		Project: env[envGCPProject],
		Secret:  credsvc.BundleSecretID(env[envProject]),
		Service: sm,
	}}
	auth, err := credsvc.NewAuthenticator(credsvc.AuthConfig{
		Project:         env[envProject],
		FirebaseProject: env[envFirebaseFP],
		Audience:        env[envCredentialsServiceURL],
		JobAccounts:     emails,
		Keys:            credsvc.NewGoogleKeys(),
		JobKeys:         credsvc.NewGoogleKeys(credsvc.WithKeysEndpoint(credsvc.GoogleOAuthCertsURL)),
		Outcomes:        credsvc.RTDBOutcomes{DB: db},
	})
	if err != nil {
		return credsvc.Config{}, userErr("the credentials service cannot verify its callers, so it will not start: %v", err)
	}
	return credsvc.Config{
		Auth:    auth,
		Bundles: loader,
		Policy:  credsvc.RTDBPolicy{DB: db},
		State:   &creds.RTDBStore{DB: db},
		Kills:   credsvc.RTDBKills{DB: db},
		DB:      db,
		Probes:  db,
		// Every audited act goes to the process's log, which is the stream
		// Cloud Logging reads; the one auditor that redacts is built inside
		// NewServer (NewProbeAuditor arms it with this service's loader).
		AuditLog:    log.Handler(),
		JobAccounts: accounts,
		Log:         log,
	}, nil
}

// credentialsProductionWiring is the two things the builder needs that reach
// Google as the service itself. They are built lazily -- both are functions
// rather than values -- because a refusal before them must not have built a
// client, and because Secret Manager's own constructor is the one call in this
// command that can fail for a reason the deployment cannot see.
func credentialsProductionWiring() (func(context.Context) (oauth2.TokenSource, error), func(context.Context) (*secretmanager.Service, error)) {
	return func(ctx context.Context) (oauth2.TokenSource, error) {
			// The same two scopes the history job reads the database with
			// (budgetScopes): this service reads and writes the same nodes.
			return google.DefaultTokenSource(ctx, budgetScopes...)
		}, func(ctx context.Context) (*secretmanager.Service, error) {
			return secretmanager.NewService(ctx)
		}
}

// credentialsServiceDatabase holds the database URL and the Firebase project
// together: the database is named after the Firebase project it belongs to
// (<project>-default-rtdb), so the two names must be the same installation's.
// A pair that disagrees is a service reading one installation's state while
// verifying another's runs -- which is what a copied deployment's environment
// looks like, and the reason this is a refusal rather than a warning.
//
// The rule is checkDatabaseHost's (history.go), which is where it is defined
// once for both processes; the message is this command's own because that one
// quotes the database's name and this one may not quote a value (above).
func credentialsServiceDatabase(rawURL, firebaseProject string) error {
	if err := checkDatabaseHost(rawURL, firebaseProject); err != nil {
		return fmt.Errorf("%s and %s name different installations: the database is <Firebase project>-default-rtdb, so the URL must be the database of the Firebase project a run's identity is verified against. Nothing was started and no credential was issued", envRTDBURL, envFirebaseFP)
	}
	return nil
}

// credentialsServiceAudience checks the service's own URL, which is the
// audience a caller's Google ID token carries. It must be an https URL with no
// path, query or fragment: Cloud Run serves the revision at the origin, and an
// audience with a path in it is one no token this platform mints would match
// -- a service that could never admit a caller, which reads as a broken
// deployment rather than as the misconfiguration it is.
func credentialsServiceAudience(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "https" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%s is not the service's own URL: it must be the https URL Cloud Run serves this revision on, with no path, query or fragment (a caller's Google ID token is minted for exactly that audience, so anything else admits nobody). Nothing was started and no credential was issued", envCredentialsServiceURL)
	}
	return nil
}

// credentialsServiceJobAccounts turns FUGARO_JOB_ACCOUNTS into the binding
// table NewServer is given: one account per (repository, workflow), derived
// from the triple by the one derivation the deployer used
// (credsvc.JobAccountsFor over task.Slug and gcp.ServiceAccountID).
//
// The document is strict: a JSON array of objects with exactly those three
// fields, all of them non-empty. Terraform writes it from an input the owner
// supplies (credentials.tf), so a malformed one is a deployment that is wrong;
// an unknown field is refused rather than ignored because a triple that names
// a fact this command does not read (say "project") is an owner expecting a
// binding that would not be made.
//
// An empty array is refused here rather than left to NewServer: the two agree
// (NewServer refuses a table that binds nothing), and refusing it by name says
// which variable was empty, which is the thing an operator has to fix.
func credentialsServiceJobAccounts(raw, fugaroProject, gcpProject string) ([]credsvc.JobAccount, error) {
	var jobs []struct {
		Provider string `json:"provider"`
		Repo     string `json:"repo"`
		Workflow string `json:"workflow"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&jobs); err != nil {
		return nil, fmt.Errorf("%s is not the installation's job accounts: it must be a JSON array of {\"provider\": ..., \"repo\": ..., \"workflow\": ...} objects, one per job whose service account may call the service. Nothing was started and no credential was issued", envJobAccounts)
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("%s names no job, so the service would bind no run to a repository: an empty table refuses every caller, and a service that admits nobody is one an operator would read as healthy while every run halted. Nothing was started and no credential was issued", envJobAccounts)
	}
	byProvider := map[string][]credsvc.RepoJob{}
	for i, j := range jobs {
		if j.Provider == "" || j.Repo == "" || j.Workflow == "" {
			// The index and the field names, not the entry: a triple is the
			// one value in this environment that can hold a name an operator
			// would not want in a log, and the refusal has to be actionable
			// without it.
			return nil, fmt.Errorf("%s: entry %d has no provider, repository or workflow (all three are required: the account's name is derived from the repository and the workflow, and the repository's slug from the provider and the repository)", envJobAccounts, i)
		}
		byProvider[j.Provider] = append(byProvider[j.Provider], credsvc.RepoJob{Repo: j.Repo, Workflow: j.Workflow})
	}
	out := make([]credsvc.JobAccount, 0, len(jobs))
	for _, provider := range slices.Sorted(maps.Keys(byProvider)) {
		accts, err := credsvc.JobAccountsFor(fugaroProject, gcpProject, provider, byProvider[provider])
		if err != nil {
			// Not the derivation's own error: it quotes the repository. What
			// is left is actionable because the entry above has been checked
			// for its three fields, so what can be wrong here is the provider
			// or the repository's shape.
			return nil, fmt.Errorf("%s names a job whose service account cannot be derived: the provider must be a provider kind (lower case, starting with a letter) and the repository a canonical path this build can name, because the account's name is derived from the two rather than configured. Nothing was started and no credential was issued", envJobAccounts)
		}
		out = append(out, accts...)
	}
	return out, nil
}

// The host's own bounds. A proxied model call is long-lived -- it streams a
// completion that can take minutes, and Cloud Run's request timeout is set to
// the hour the module allows (credentials.tf, Check 91 measures what its front
// end does with one) -- and so is a git fetch or push through the same host
// (B1.5), so there is deliberately no WriteTimeout and no ReadTimeout: either
// would cut a call in flight, which is a run that fails for a reason nothing
// in its record explains. What is bounded is the part that cannot be
// long-lived: a client that has opened a connection and not sent its request,
// a connection left idle between calls, and a peer that has stopped reading.
const (
	// serveReadHeaderTimeout bounds a caller that connects and then says
	// nothing. Every request the service answers is a small JSON POST or a
	// streamed one, and the headers of both arrive at once.
	serveReadHeaderTimeout = 30 * time.Second
	// serveIdleTimeout is how long a kept-alive connection with no request on
	// it is held. The runner's gateway calls the service repeatedly over one
	// job, so it is generous. It is also what bounds an HTTP/2 connection that
	// has been opened and has no stream on it -- h2 has no header timeout of
	// its own, and net/http gives an h2 connection this same field as its idle
	// timer (server.go: `if idle := sc.hs.IdleTimeout()`), started as soon as
	// the preface is read.
	serveIdleTimeout = 2 * time.Minute
	// serveMaxConcurrentStreams is how many streams one HTTP/2 connection may
	// hold open at once. It is a bound on how much work one connection may ask
	// this process for -- each stream is a handler goroutine, and for this
	// service an upstream call carrying a credential behind it -- and not a
	// throughput decision: Go's own default is 250, with a TODO in its source
	// to make it 100 "as the GFE seems to", and the hop in front of this
	// process is that front end. So 100 is the number a caller already handles
	// from a peer it cannot tell apart from this one, which means it is never
	// the limit a legitimate caller hits first, and a caller that wants more
	// in flight opens another connection.
	serveMaxConcurrentStreams = 100
	// serveWriteByteTimeout is the one write bound HTTP/2 offers that a long
	// streamed push still survives, and it is why it is set where a
	// WriteTimeout is not: it is not a deadline for the stream but for a
	// single write attempt with no progress, restarted by every byte written
	// (net/http's writeWithByteTimeout). Two minutes of a connection that
	// cannot accept one byte is a peer that has stopped reading -- a runner
	// that died, a git client on a link that is gone -- and it is holding a
	// goroutine and an upstream connection that carries a model credential;
	// without this, the only thing that ends it is Cloud Run's own request
	// timeout an hour out (credentials.tf). A pack or a model stream that is
	// still moving is never cut: progress restarts the clock.
	serveWriteByteTimeout = 2 * time.Minute
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

// credentialsServeProtocols is what the host speaks on its one listener:
// HTTP/1.1 and HTTP/2 cleartext (h2c), the second of them with prior
// knowledge, and no TLS of this process's own -- Cloud Run's front end
// terminates TLS and then talks to the container, and with the service's port
// named `h2c` (credentials.tf) it talks HTTP/2 cleartext.
//
// h2c is what B1.0 exists for, and B1.0 is conditional on the owner's Check
// 190 failing a large push (implementation-plan.md, whose entry depends on
// B0.6 recording one). Nothing in this file rests on a measurement of that
// check: docs/gcp-live-checklist.md still records it as not run on this tree,
// and the reconciliation marks the reading it rests on (U2: that HTTP/2's
// streaming keeps the pack off the HTTP/1 path's buffer) unverified. So what
// is stated here is the belief the task was planned on and not a result — the
// hop in front of this process buffers an HTTP/1 request body and refuses one
// a large git pack exceeds, and an HTTP/2 body is streamed past that buffer,
// which is the hop the port's name (credentials.tf) turns h2c on for. Check
// 190's live re-run is the owner's, and it is what would prove it.
//
// HTTP/1.1 stays on, and not as a courtesy: it is what every caller this
// service has today speaks. The runner's gateway is a plain net/http client
// (internal/gateway, internal/credsvc's own callers), and its requests go out
// over HTTP/1.1 unless it asks for something else; a server that spoke only
// h2c would fail every model call this installation makes, which is a whole
// installation halted by a transport choice.
//
// The GODEBUG=http2debug refusal is not weakened by this and does not depend
// on it: it is the first thing serveCredentials does, before the environment
// is read and long before a listener exists, and a process that refused under
// it cannot have started an h2c server (TestCredentialsServeRefusesHTTP2DebugStill).
func credentialsServeProtocols() *http.Protocols {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	// TLS-based HTTP/2 is deliberately not enabled: this process serves no
	// TLS. Cloud Run's front end owns the listener's TLS, and an h2 ALPN
	// negotiation is not something that can happen on a cleartext socket.
	protocols.SetHTTP2(false)
	return &protocols
}

// credentialsHTTPServer is the host's own shape, and a function of the handler
// rather than of the service so that the bounds below are the same object
// whoever is served -- and so that a test can drive a real listener, request
// and shutdown through them without a service's worth of fakes
// (TestCredentialsServeHostStopsOnSignal).
//
// The HTTP/2 half of it is stated rather than left to the library's defaults,
// because every default here is a decision about this service that nobody
// made. Two of them are load-bearing in the direction this task is about:
//
//   - Server.ReadTimeout and Server.WriteTimeout stay zero, and on HTTP/2 that
//     is not the same statement as on HTTP/1.1. net/http's h2 server arms a
//     per-stream timer from ReadTimeout when the stream is created, closing
//     the request body at that deadline whatever progress the body is making
//     (internal/http2: `if sc.hs.ReadTimeout() > 0 { st.readDeadline =
//     time.AfterFunc(...)}`), and another from WriteTimeout for the response.
//     A non-zero value there would cut a 33 MiB push or a minute-long model
//     stream mid-flight; the same fields also carry h2's send-ping
//     health-check interval, which nothing here needs.
//   - Server.IdleTimeout and Server.MaxHeaderBytes are reused by the h2
//     server as they are -- the idle timer of a connection with no stream on
//     it, and MAX_HEADER_LIST_SIZE -- so the bounds above cover both
//     protocols rather than only the one they were first written for.
//
// What is stated is the two the h2 server would otherwise choose for this
// process: the concurrent-stream limit (serveMaxConcurrentStreams) and the
// no-progress write bound (serveWriteByteTimeout). Left at its default is the
// frame size, which is a transport detail neither limit depends on.
func credentialsHTTPServer(ctx context.Context, h http.Handler, errw io.Writer) *http.Server {
	return &http.Server{
		Handler:           h,
		Protocols:         credentialsServeProtocols(),
		ReadHeaderTimeout: serveReadHeaderTimeout,
		IdleTimeout:       serveIdleTimeout,
		MaxHeaderBytes:    serveMaxHeaderBytes,
		HTTP2: &http.HTTP2Config{
			MaxConcurrentStreams: serveMaxConcurrentStreams,
			WriteByteTimeout:     serveWriteByteTimeout,
		},
		BaseContext: func(net.Listener) context.Context { return ctx },
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
