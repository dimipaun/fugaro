package cli

import (
	"cmp"
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
	"github.com/dimipaun/fugaro/internal/task"
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
// are derived). Four more names are read beside those seven, each OPTIONAL
// because the module renders it only where it applies: FUGARO_VERIFIER_ACCOUNTS
// (C3.9g2), FUGARO_PERSON_ACCOUNTS and FUGARO_COORDINATOR_ACCOUNT (the read
// mint's two classes, BB4 and BB5) and FUGARO_REVIEW_JOB_ACCOUNTS (the
// review-only project jobs, C3.9a's class: {"profile","provider","account"}
// triples the service derives each review job's account from, the same document
// kind FUGARO_JOB_ACCOUNTS is). The names are held by a second thing:
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
	// envReviewJobAccounts is the installation's review-only project jobs --
	// the variant a `fugaro review` runs on (CSR7) -- as
	// {"profile","provider","account"} objects, one per job whose service
	// account may call the service as a review caller (C3.9a's class,
	// internal/credsvc's ClassReview). It is a document of the kind
	// FUGARO_JOB_ACCOUNTS is -- rows the service derives each account from --
	// over the variant's own axis: the accounts are DERIVED from the triples
	// (credsvc.ReviewJobAccountFor), because the variant's service account is
	// derived from the profile, the provider and the account by the same
	// function the deployer used, and a table of emails here would be a second
	// derivation that can drift from the accounts the deployer created
	// (internal/infra/projectjobs.go's reviewVariant derives them with the same
	// two functions).
	//
	// The three fields are the ones the variant's names are built from and not
	// a repository: a review job names no repository (a profile serves every
	// repository its policy admits), and the pairing resolves the repository
	// from the run identity's slug through the same table the per-repository
	// jobs are bound by (RepositoryTableFor, read below out of
	// FUGARO_JOB_ACCOUNTS). "account" is the axis's own spelling, in which the
	// empty string is the default account -- the mapping
	// credsvc.ReviewJobAccountFor applies, which is config.ReviewVariantAccount's
	// for the names the deployer spells.
	//
	// It is read as an OPTIONAL value, and the reason is the failure it would
	// otherwise cost: this name arrives with the deployment of the variant's
	// jobs, so an installation whose module does not render it yet -- every
	// module of a release before the entry lands -- must start the same service
	// it always did, with an empty review set, rather than refuse to bind a
	// port. An empty set is the class's own fail-closed statement and not a
	// gap: verifyJob answers no review class at all, every caller of it is
	// refused, and the launcher's own gate refuses the launch before a job is
	// started (its constant is `false` until the owner's live check). That is
	// the same shape and the same reason FUGARO_VERIFIER_ACCOUNTS,
	// FUGARO_PERSON_ACCOUNTS and FUGARO_COORDINATOR_ACCOUNT have, and it is why
	// this name is read outside credentialsServiceEnvNames: a name inside that
	// list is one a service REFUSES TO START without, which is the direction
	// that halts every run of an installation whose module has not moved yet
	// (D143). A value that is present and is not the document below is a
	// deployment that is wrong, and is refused here rather than read as empty.
	envReviewJobAccounts = "FUGARO_REVIEW_JOB_ACCOUNTS"
	// envVerifierAccounts is the identities that may ask the verify route
	// whether a verdict's signature is this service's: a JSON array of bare
	// addresses (the coordinator's account, and the owner's for a manual
	// check). It is read as an OPTIONAL value, outside credentialsServiceEnvNames
	// -- whose names are the seven this process REFUSES to start without --
	// because credentials.tf renders the entry only on an installation that
	// named a verifier (C3.9g2, D1068). Absent or empty is the empty list the
	// verify route already refuses every caller with.
	envVerifierAccounts = "FUGARO_VERIFIER_ACCOUNTS"
	// envPersonAccounts is the identities that may ask the read mint for a
	// read-scoped Bitbucket token: a JSON array of bare addresses, which is
	// what the module renders from the installation's operators, whose IAM
	// spellings it strips the "user:" from. Like the verifier list it is
	// read as OPTIONAL and lives outside credentialsServiceEnvNames, because
	// the module renders the entry only on an installation that named an
	// operator and an installation that named none must still start: absent
	// or empty is the empty list the read mint already refuses every caller
	// with (BB4).
	envPersonAccounts = "FUGARO_PERSON_ACCOUNTS"
	// envCoordinatorAccount is the installation's coordinator service account
	// (BB5): ONE bare address, which is the read mint's second class — the
	// identity the scheduled sweep calls with — and not a list, because an
	// installation has one coordinator. Like the verifier list and the person
	// list it is read as OPTIONAL and lives outside credentialsServiceEnvNames:
	// the module renders the entry only on an installation whose coordinator is
	// on, and one that never opted in must still start, with a read mint that
	// admits no coordinator at all. Absent or empty is that empty state, and a
	// value that is present and is not one address is a deployment that is
	// wrong (F24).
	envCoordinatorAccount = "FUGARO_COORDINATOR_ACCOUNT"
)

// credentialsServiceEnvNames is the whole of this command's REQUIRED
// environment: the seven names credentials.tf sets on the service's container,
// the ones whose absence is a service that cannot say which installation it is
// serving. The class-specific entries it reads beside them are OPTIONAL and
// live outside this list, because the module renders each of those only where
// it applies (envReviewJobAccounts, envVerifierAccounts, envPersonAccounts and
// envCoordinatorAccount). internal/infra/tf's credentialsServiceEnv is init's
// cover's allow-list, which is these seven plus those four: a name outside it is
// a container the cover refuses, which is what makes the module's list a
// boundary rather than a convention. The lists are held together by tests
// rather than by this comment: TestCredentialsServiceEnvIsTheDeploymentsOwn
// reads credentials.tf's own env blocks and requires every name this command
// reads to be one the module sets, so a name added on one side and not the
// other fails rather than drifts -- the direction that matters is the one where
// the module sets a name this command does not read, which is a fact an
// operator would believe was in effect.
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
// The set left empty on purpose is the shared project jobs' one
// (AuthConfig.SharedJobAccounts): under Global Constraint 6 this service admits
// runs in per-repository jobs only until the phase-3 shared-job rules land, and
// an empty set is that statement rather than a fact the deployment has to make.
// The day those rules land it is a read of the policy this service already
// reads (credsvc.SharedJobAccountsFor), not a variable.
//
// The review-only variant's accounts (AuthConfig.ReviewJobAccounts and
// Config.ReviewAccounts, which are the same list) come from the deployment
// instead: FUGARO_REVIEW_JOB_ACCOUNTS, the class's own variable beside the
// seven, whose rows are the (profile, provider, account) triples the variant's
// jobs were deployed over, and credsvc.ReviewJobAccountFor derives the one
// service account each of them runs as — the same derivation the policy read
// would make
// (credsvc.ReviewJobAccountsFor), which is why this command still reads no
// policy: it builds its configuration from its own environment and never
// touches the database, the property that keeps the service's start-up
// independent of the installation's state (D143, and the reason a policy read
// here would make the configuration fail for a reason no operator could see in
// it). An empty set — the value absent, or an empty document — is the
// fail-closed statement it is for the shared set: this installation admits no
// review job, and every caller of that class is refused, which is the state of
// every installation whose module does not render the entry yet.
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
	// deployment that is wrong rather than a deployment with a default. The
	// names read outside this loop are the class-specific ones the module
	// renders only where they apply, and the rest are not.
	env := map[string]string{}
	for _, k := range credentialsServiceEnvNames {
		if env[k] = getenv(k); env[k] == "" {
			return credsvc.Config{}, userErr("the credentials service needs %s in its environment (the installation module sets it on the service, and it is the whole of how this process is configured: there is no flag, no config file and no default)", k)
		}
	}
	// Outside that loop, because the module renders this entry only on an
	// installation that named a verifier: an absent value is the empty list
	// the verify route already refuses every caller with, not a service that
	// will not start (C3.9g2, D1068). A value that is present and is not the
	// list the module renders is a deployment that is wrong, and is refused
	// here rather than silently read as empty.
	verifiers, err := credentialsServiceVerifiers(getenv(envVerifierAccounts))
	if err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	// The read mint's own class, read the same way and for the same reason:
	// the entry is rendered only on an installation that named an operator,
	// and an installation that named none gets a route that admits nobody
	// rather than a service that will not bind a port (BB4).
	persons, err := credentialsServicePersons(getenv(envPersonAccounts))
	if err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	// The read mint's other class (BB5), read the same way and for the same
	// reason: the entry is rendered only on an installation whose coordinator is
	// on, and one that never opted in gets a route that admits no coordinator
	// rather than a service that will not bind a port (F24).
	coordinator, err := credentialsServiceCoordinator(getenv(envCoordinatorAccount))
	if err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	// The review-only class (C3.9a), read the same way and for the same reason:
	// the entry arrives with the deployment of the variant's jobs, so an
	// installation whose module does not render it starts with an empty set and
	// refuses every caller of the class rather than refusing to bind a port
	// (`envReviewJobAccounts` states the direction and why it matters). The
	// derived accounts are the rows the deployer created, and nothing here is
	// invented: a row whose provider is not a provider kind is refused rather
	// than derived into an account nobody deployed.
	reviewAccounts, err := credentialsServiceReviewJobAccounts(getenv(envReviewJobAccounts), env[envGCPProject])
	if err != nil {
		return credsvc.Config{}, userErr("%v", err)
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
	accounts, repos, err := credentialsServiceJobAccounts(env[envJobAccounts], env[envProject], env[envGCPProject])
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
		// The verify route's own class (C3.9f, D1068): the accounts that may
		// ask whether a verdict's signature is this service's. Empty is the
		// state every installation starts in, and the route answers every
		// caller with the class's refusal rather than the process failing.
		VerifierAccounts: verifiers,
		// The read mint's class (BB4): the installation's operators, by the
		// bare address a Google ID token's email claim carries. Empty is the
		// state an installation that named no operator starts in, and the
		// route answers every caller with the class's refusal.
		PersonAccounts: persons,
		// ... and its second class (BB5): the coordinator's own service
		// account, which is the identity the scheduled sweep calls with. Empty
		// is the state an installation that runs no coordinator starts in.
		CoordinatorAccount: coordinator,
		// The review-only class (C3.9a): the jobs `fugaro review` runs on, by
		// the service account each one was deployed as, with the Claude account
		// its calls are served by. Empty is the state an installation whose
		// module renders no review-job document starts in, and a caller of the
		// class is then no class at all rather than a class with no route.
		ReviewJobAccounts: reviewAccounts,
		Keys:              credsvc.NewGoogleKeys(),
		JobKeys:           credsvc.NewGoogleKeys(credsvc.WithKeysEndpoint(credsvc.GoogleOAuthCertsURL)),
		Outcomes:          credsvc.RTDBOutcomes{DB: db},
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
		// The same client is the create-only write the sign route's
		// one-signature mark is taken with (C3.9e, D1074): the mark is a node
		// under creds/ like the probe's lease, read for its ETag and written
		// back under it, over the service's own identity — which is the only
		// writer of that subtree (the rules deny every run token it).
		Signs: db,
		// Every audited act goes to the process's log, which is the stream
		// Cloud Logging reads; the one auditor that redacts is built inside
		// NewServer (NewProbeAuditor arms it with this service's loader).
		AuditLog:    log.Handler(),
		JobAccounts: accounts,
		// The review class's own table (C3.9a): the same rows
		// AuthConfig.ReviewJobAccounts holds, because the pairing needs the
		// account NAME — the one the published admissions speak — where the
		// verifier needs the class. NewServer refuses a configuration whose
		// review table is non-empty and whose repository table is empty, which
		// is the one pair the two are read as (server.go).
		ReviewAccounts: reviewAccounts,
		// The repository table, which is the pairing's second step for a
		// review caller (C3.9a) and is read from this variable's own input
		// above rather than from the published policy: the table must be the
		// one the per-repository bindings are made from.
		Repos: repos,
		Log:   log,
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

// credentialsServiceVerifiers turns FUGARO_VERIFIER_ACCOUNTS into the verify
// route's class (C3.9f, D1068): a JSON array of bare addresses, which is what
// the module renders from the installation's verifier_accounts.
//
// An absent or empty value is an EMPTY list and not a refusal, because the
// module renders the entry only on an installation that named a verifier: a
// service that would not start without it would be one every installation that
// never opted in could not run. A value that is present and is not that array
// is a deployment that is wrong, so it is refused by name — and the refusal
// names the variable and never an entry, because the entries are accounts of
// people and the environment is this process's own. Whether an entry is an
// address at all is credsvc.NewAuthenticator's to say (a verifier is matched
// against a Google ID token's email claim, and that fold is the one that
// decides), so this function only holds the document to its shape.
func credentialsServiceVerifiers(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var verifiers []string
	if err := json.Unmarshal([]byte(raw), &verifiers); err != nil {
		return nil, fmt.Errorf("%s is not the installation's verifier list: it must be a JSON array of the bare email addresses a Google ID token carries, one per identity that may ask the service to verify a signature. Nothing was started and no credential was issued", envVerifierAccounts)
	}
	return verifiers, nil
}

// credentialsServicePersons turns FUGARO_PERSON_ACCOUNTS into the read mint's
// class (BB4): a JSON array of bare addresses, which is what the module renders
// from the installation's operators.
//
// It is credentialsServiceVerifiers' rule word for word, and for the same
// reason: an absent or empty value is an EMPTY list and not a refusal, because
// the module renders the entry only on an installation that named an operator,
// while a value that is present and is not that array is a deployment that is
// wrong. The refusal names the variable and never an entry, because the entries
// are accounts of people.
func credentialsServicePersons(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var persons []string
	if err := json.Unmarshal([]byte(raw), &persons); err != nil {
		return nil, fmt.Errorf("%s is not the installation's person list: it must be a JSON array of the bare email addresses a Google ID token carries, one per person who may ask the service to mint a read-scoped Bitbucket token. Nothing was started and no credential was issued", envPersonAccounts)
	}
	return persons, nil
}

// credentialsServiceCoordinator turns FUGARO_COORDINATOR_ACCOUNT into the read
// mint's second class (BB5): the coordinator Cloud Run job's own service
// account, as the bare address a Google ID token's email claim carries.
//
// The two rules the lists above are read by hold here too, and this one adds a
// third because a single value is not a document:
//
//   - absent or empty is the empty account and not a refusal, because the
//     module renders the entry only on an installation whose coordinator is on
//     (F24), and a service that would not start without it is one every
//     installation that never opted in could not run;
//   - what is present is held to the shape the module renders — one address,
//     with no space in it and nothing around it — because the value reaches an
//     authenticator that matches it against a claim, and a value carrying a
//     quote or a newline is a deployment that is wrong rather than an account
//     that admits nobody. Whether it is an ADDRESS at all is
//     credsvc.NewAuthenticator's to say, the rule the verifier list is left to;
//   - the refusal names the variable and never the value. It is an account, and
//     the environment is this process's own (the no-echo rule).
func credentialsServiceCoordinator(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, " \t\r\n") {
		return "", fmt.Errorf("%s is not the installation's coordinator account: it must be one bare email address, the one a Google ID token's email claim carries, with no space and nothing else in the value. Nothing was started and no credential was issued", envCoordinatorAccount)
	}
	return raw, nil
}

// credentialsServiceJobAccounts turns FUGARO_JOB_ACCOUNTS into the two things
// NewServer is given that this variable holds: the binding table, one account
// per (repository, workflow), derived from the triple by the one derivation
// the deployer used (credsvc.JobAccountsFor over task.Slug and
// gcp.ServiceAccountID), and the repository table, which is those same rows
// read the other way round (credsvc.RepositoryTableFor: the slug a run
// identity carries to the canonical repository name the published policy
// speaks).
//
// The repository table is what pairs a review caller with a run (C3.9a): a
// review job's account names no repository, so the run identity's own slug is
// the only repository the call can be about, and it is resolved here rather
// than guessed from the slug's own text. Its input is this variable and not
// the published policy, because the table must be the one the per-repository
// bindings are made from — a service that resolved a slug another way could
// pair an account with a repository its own binding table disagrees about.
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
func credentialsServiceJobAccounts(raw, fugaroProject, gcpProject string) ([]credsvc.JobAccount, map[string]string, error) {
	var jobs []struct {
		Provider string `json:"provider"`
		Repo     string `json:"repo"`
		Workflow string `json:"workflow"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&jobs); err != nil {
		return nil, nil, fmt.Errorf("%s is not the installation's job accounts: it must be a JSON array of {\"provider\": ..., \"repo\": ..., \"workflow\": ...} objects, one per job whose service account may call the service. Nothing was started and no credential was issued", envJobAccounts)
	}
	if len(jobs) == 0 {
		return nil, nil, fmt.Errorf("%s names no job, so the service would bind no run to a repository: an empty table refuses every caller, and a service that admits nobody is one an operator would read as healthy while every run halted. Nothing was started and no credential was issued", envJobAccounts)
	}
	byProvider := map[string][]credsvc.RepoJob{}
	for i, j := range jobs {
		if j.Provider == "" || j.Repo == "" || j.Workflow == "" {
			// The index and the field names, not the entry: a triple is the
			// one value in this environment that can hold a name an operator
			// would not want in a log, and the refusal has to be actionable
			// without it.
			return nil, nil, fmt.Errorf("%s: entry %d has no provider, repository or workflow (all three are required: the account's name is derived from the repository and the workflow, and the repository's slug from the provider and the repository)", envJobAccounts, i)
		}
		byProvider[j.Provider] = append(byProvider[j.Provider], credsvc.RepoJob{Repo: j.Repo, Workflow: j.Workflow})
	}
	out := make([]credsvc.JobAccount, 0, len(jobs))
	repos := make(map[string]string, len(jobs))
	for _, provider := range slices.Sorted(maps.Keys(byProvider)) {
		accts, err := credsvc.JobAccountsFor(fugaroProject, gcpProject, provider, byProvider[provider])
		if err != nil {
			// Not the derivation's own error: it quotes the repository. What
			// is left is actionable because the entry above has been checked
			// for its three fields, so what can be wrong here is the provider
			// or the repository's shape.
			return nil, nil, fmt.Errorf("%s names a job whose service account cannot be derived: the provider must be a provider kind (lower case, starting with a letter) and the repository a canonical path this build can name, because the account's name is derived from the two rather than configured. Nothing was started and no credential was issued", envJobAccounts)
		}
		table, err := credsvc.RepositoryTableFor(fugaroProject, gcpProject, provider, byProvider[provider])
		if err != nil {
			// Unreachable: the same input has just been derived above. The
			// branch is here rather than a second copy of the table's rule,
			// because a table spelled anywhere but RepositoryTableFor would be
			// a slug resolved one way for a binding and another for a review.
			return nil, nil, fmt.Errorf("%s names a job whose repository cannot be derived: the provider must be a provider kind (lower case, starting with a letter) and the repository a canonical path this build can name. Nothing was started and no credential was issued", envJobAccounts)
		}
		out = append(out, accts...)
		maps.Copy(repos, table)
	}
	return out, repos, nil
}

// credentialsServiceReviewJobAccounts turns FUGARO_REVIEW_JOB_ACCOUNTS into the
// review-only class's table (C3.9a): one row per review job this installation
// deployed, keyed by the service account the caller is verified as and carrying
// the Claude account that call is served by.
//
// The document is a JSON array of {"profile": ..., "provider": ...,
// "account": ...} objects — the variant's own axis, which is the axis
// CSR7 deploys one review job over (config.ProjectJobAccounts over a profile's
// job profile), and NOT FUGARO_JOB_ACCOUNTS' triple: a review job is not bound
// to a repository (its account names none, spec §2.1), so the row names no
// repository and no workflow, and the repository a call is about is the run
// identity's own slug, resolved through the table FUGARO_JOB_ACCOUNTS builds.
// The account may be absent, which is the axis's spelling of the default
// account, and each row's service account is derived from the three fields by
// credsvc.ReviewJobAccountFor — the same derivation the policy read makes, so
// there is one spelling of these names and not two.
//
// The two rules the class-specific entries are read by hold here
// (`envReviewJobAccounts` states them): an absent or empty value is the EMPTY
// set, which is the fail-closed statement that this installation admits no
// review job — every caller of the class is then refused, which is the state of
// an installation whose module does not render the entry yet — while a value
// that is present and is not this document is refused here, because it is a
// deployment that is wrong and would otherwise be read as a service that admits
// nobody.
//
// An empty array is the empty set and not a refusal: the module renders the
// entry from the review jobs it deploys, and an installation with none is one
// whose service must still start and refuse the class. That is the other
// direction from FUGARO_JOB_ACCOUNTS, whose empty table is refused by name
// because an empty one there is a service that binds no run at all.
//
// A REFUSAL NAMES A VARIABLE AND AN ENTRY, NEVER A VALUE.
func credentialsServiceReviewJobAccounts(raw, gcpProject string) ([]credsvc.ReviewJobAccount, error) {
	if raw == "" {
		return nil, nil
	}
	var rows []struct {
		Profile  string `json:"profile"`
		Provider string `json:"provider"`
		Account  string `json:"account"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("%s is not the installation's review job accounts: it must be a JSON array of {\"profile\": ..., \"provider\": ..., \"account\": ...} objects, one per review-only job this installation deployed. Nothing was started and no credential was issued", envReviewJobAccounts)
	}
	out := make([]credsvc.ReviewJobAccount, 0, len(rows))
	for i, r := range rows {
		if r.Profile == "" || r.Provider == "" {
			// The index and the field names, not the entry: a row is a value
			// of this environment, and the refusal has to be actionable
			// without it.
			return nil, fmt.Errorf("%s: entry %d has no profile or no provider (both are required: the review job's service account is derived from the profile, the provider and the account, and a row without them names an account nobody deployed)", envReviewJobAccounts, i)
		}
		if !task.ValidProvider(r.Provider) {
			return nil, fmt.Errorf("%s: entry %d names a provider that is not a provider kind (lower case, starting with a letter), so the review job's service account cannot be derived from it and would be one this installation cannot have deployed", envReviewJobAccounts, i)
		}
		out = append(out, credsvc.ReviewJobAccountFor(r.Profile, r.Provider, r.Account, gcpProject))
	}
	// The same order and the same de-duplication the policy read makes
	// (credsvc.ReviewJobAccountsFor): two rows that name one account are one
	// row, and rows that name one account under two Claude accounts are left
	// for credsvc.NewAuthenticator to refuse, which is the deployment that
	// disagrees with itself rather than a document this function can repair.
	slices.SortFunc(out, func(a, b credsvc.ReviewJobAccount) int { return cmp.Compare(a.Email, b.Email) })
	return slices.Compact(out), nil
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
// B0.6 recording one). That record is in, and it is the FAIL: a 33 MiB pack on
// HTTP/1.1 was answered 413 by the front end, which the run read as the four
// bytes its proxy received. So the first half of the belief below is measured
// -- the hop in front of this process refuses an HTTP/1 request body a large
// git pack exceeds. The second half is not: the reconciliation still marks it
// (U2: that HTTP/2's streaming keeps the pack off that buffer) unverified, and
// the run above never reached a listener that speaks h2c, so no live step has
// read it. What is stated here is therefore what the port's name
// (credentials.tf) turns on and not a result -- an HTTP/2 body is streamed
// past that buffer.
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
