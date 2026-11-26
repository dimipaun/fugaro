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

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/workspace"
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
// are derived). Eight more names are read beside those seven, each OPTIONAL
// because the module renders it only where it applies: FUGARO_VERIFIER_ACCOUNTS
// (C3.9g2), FUGARO_PERSON_ACCOUNTS and FUGARO_COORDINATOR_ACCOUNT (the read
// mint's two classes, BB4 and BB5), FUGARO_REVIEW_JOB_ACCOUNTS (the
// review-only project jobs, C3.9a's class: {"profile","provider","account"}
// triples the service derives each review job's account from, the same document
// kind FUGARO_JOB_ACCOUNTS is), FUGARO_LAUNCHER_ACCOUNTS (the launcher class
// of D1938: the addresses that may ask the service for a slip and
// reach the intake, which is a class of those two routes and of none the seven
// above serve), FUGARO_SHARED_JOB_ACCOUNTS (the shared project jobs the coding
// half of the review class runs in, B5.1: the same document kind again, over
// the coding axis), FUGARO_BUILD_ACCOUNTS (the build class,
// B5: the addresses of the installation's build identities, one per project,
// which are the one caller class of the build route and of nothing else) and
// FUGARO_ORCHESTRATOR_ACCOUNTS (the resident daemon's own class,
// E16/D2049 and F19/O2: the one address that may ask the label route for a
// label write and the merge mint for a merge token, rendered only on an
// installation that runs a daemon). The
// names are held by a second thing:
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
// work is held to (D143: an unreachable service is waited for, and then halts
// the run, and no code path falls back to a key in the job). A revision that fails to start is what
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
	// refused, and a launch that reached such a service is refused before a job
	// is started (checkReviewAdmission judges the release the installation's own
	// tfvars state for its service, and warns and admits only where that record
	// cannot be read). That is the same shape and the same reason
	// FUGARO_VERIFIER_ACCOUNTS,
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
	// envLauncherAccounts is the identities that may ask this service for a
	// slip and reach the intake (D1938, F25, F36): a JSON array of bare
	// addresses, which is what the module renders from the installation's
	// roster. It is the person class's shape and its rule -- the launcher class
	// is admitted on the grant route and the intake, and the read mints stay
	// operators-only -- and like the lists above it is read as OPTIONAL and
	// lives outside credentialsServiceEnvNames, because the module renders the
	// entry only on an installation whose roster names a launcher: one that
	// names none must still start, with a launcher class that admits nobody,
	// rather than be a service that will not bind a port.
	envLauncherAccounts = "FUGARO_LAUNCHER_ACCOUNTS"
	// envSharedJobAccounts is the installation's shared project jobs -- the
	// job a coding run of a profile's repositories runs in -- as
	// {"profile","provider","account","repos"} objects, one per job whose
	// service account may call the service as a shared caller (C3.9's coding
	// half, internal/credsvc's ClassShared). It is the review document's own
	// shape over the coding axis, and it differs from it in the one member
	// the class's binding needs: the repositories the profile is bound to,
	// which is what G21 holds a shared caller's identity to. They are the
	// profile's own allow_repos, and they are the narrower of the binding's two
	// gates: the pairing holds the caller to this list AND to the project's own
	// answer for the profile (projectpolicy.Admits, read live, because a
	// profile's list may be wider than the project's spell for spell and this
	// list alone is not a gate).
	//
	// The accounts are DERIVED from the first three fields
	// (credsvc.SharedJobAccountFor), for the reason the review document gives:
	// the service account is a derivation of the profile, the provider and the
	// account, so a table of emails would be a second spelling that can drift
	// from the accounts the deployer created. "account" is the axis's own
	// spelling, in which the empty string is the default account, and it is
	// config.ReviewVariantAccount's mapping for the names the deployer spells.
	//
	// It is read as an OPTIONAL value by the rule envReviewJobAccounts states:
	// the name arrives with the deployment of the shared jobs, so an
	// installation whose module does not render it yet must start the service
	// it always did, with an empty shared set, rather than refuse to bind a
	// port. An empty set is this class's fail-closed statement: verifyJob
	// answers no shared class at all and every caller of it is refused whole,
	// which is exactly the state Global Constraint 6 held this class in before
	// this task. A value that is present and is not the document below is a
	// deployment that is wrong, and is refused here rather than read as empty.
	envSharedJobAccounts = "FUGARO_SHARED_JOB_ACCOUNTS"
	// envBuildAccounts is the installation's build identities (B5, D2301): the
	// service accounts a project's catalog builds run as, one per project, as a
	// JSON array of bare addresses, which is what the module renders from the
	// account it creates (modules/installation/build_identity.tf). It is the
	// build route's one caller class (credsvc's AuthConfig.BuildAccounts) and
	// it is the class of that route and of no other: a build is neither a run
	// of this installation nor an operator of it, and credsvc.NewAuthenticator
	// refuses an address that is on this list and on any other.
	//
	// It is read as an OPTIONAL value, the verifier list's own rule and for the
	// same reason: the module renders the entry whenever the credentials
	// service itself is deployed, so an installation whose module does not
	// render it yet -- every release before this one -- must start the service
	// it always did, with a build class that admits nobody, rather than refuse
	// to bind a port. That empty class is fail-closed and not a gap: the build
	// route answers every caller with the class's own refusal, and no run of
	// this installation is a build (D143 is about a run halting, and a run
	// never asks this route).
	envBuildAccounts = "FUGARO_BUILD_ACCOUNTS"
	// envOrchestratorAccounts is the resident orchestrator daemon's own class
	// (E16, D2049; F19 and O2 of docs/forge-labels): the accounts that may ask
	// the label route for a pull request's label write and the merge mint for a
	// merge token, as a JSON array of bare addresses — one address, the daemon's
	// own, which is what the module renders from the account its own
	// orchestrator.tf creates (modules/installation/credentials.tf). It is
	// credsvc's AuthConfig.OrchestratorAccounts.
	//
	// It is read as an OPTIONAL value, the build class's own rule and for the
	// same reason: the module renders the entry only on an installation that
	// runs a resident orchestrator, so a service deployed by a release before
	// this one -- every installation today -- must start with a class that
	// admits nobody and answer both routes with the class's own refusal, rather
	// than refuse to bind a port. A deployment that carries the entry and not
	// the address shape is refused by name.
	//
	// The class is one of the two the daemon's token reaches: Cloud Run's front
	// end admits it by the invoker grant orchestrator.tf makes on this service,
	// and this list is what the container then admits. The audience is the other
	// half of the rule and the reason a person's ambient credentials are not
	// this class whatever list they would match (credsvc.VerifyOrchestrator):
	// the token has to be minted FOR THIS SERVICE'S URL, which no plain
	// `gcloud auth print-identity-token` does.
	envOrchestratorAccounts = "FUGARO_ORCHESTRATOR_ACCOUNTS"
	// envSignerAccount is the account a run's identity is signed as (D3121,
	// LM11): ONE bare address, the runs' signer the installation's Firebase root
	// creates (its own output token_signer, whose account ID
	// internal/infra's SignerAccountID holds) and the account LM11 puts the
	// issuer's single minter row on. It is the address the launcher's signJwt
	// call named before this work. It is the one value that says whether this
	// installation HAS an issuer at all -- an installation whose block names no
	// signer has none, and its identity routes answer the contract's "not
	// served" (R7, I1) -- and it is read here as an OPTIONAL value for exactly
	// that reason: a service the installation root rendered before LM12 set the
	// entry, and one an update has not yet handed a signer to, must keep
	// starting rather than refuse to bind a port.
	//
	// It is not a credential: the service is granted the minter ROLE on this
	// account through IAM (LM11's row), and the account's own keys are nobody's
	// (modules/firebase/iam.tf grants it no role at all). A value present and
	// not one address is a deployment that is wrong, refused by name.
	envSignerAccount = "FUGARO_SIGNER_ACCOUNT"
	// envRosterServiceAccounts is the roster's service accounts (D3123): a JSON
	// array of bare addresses, the `serviceAccount:` entries of the
	// installation's roster -- the launcher class a person's list cannot hold,
	// because the address presents a service account's token rather than a
	// person's. `fugaro update` names such entries in its own lines (R10) and
	// renders the ones this installation holds here; an installation whose
	// roster names none renders no entry and must start, which is why it is read
	// as an OPTIONAL value like every other class list. It is credsvc's
	// IdentityConfig.ServiceAccounts, the roster the ticket route admits the
	// launcher class from (LM3).
	envRosterServiceAccounts = "FUGARO_ROSTER_SERVICE_ACCOUNTS"
	// envImplicitAdmins is the implicit admins of this installation (F29, R16):
	// a JSON array of bare addresses, which is what the module renders from the
	// machine's terraform.budget_admins -- the same list the CLI's own launch
	// gate reads out of its local configuration (userImplicitAdmins,
	// internal/cli's access_run_gate.go), so that the issuer's scope check
	// admits an owner the gate admits today. A `group:` or `domain:` member is
	// not on it, for the gate's own reason: a member the gate cannot match
	// against a caller is not one it can place. It is credsvc's
	// IdentityConfig.ImplicitAdmins, read as an OPTIONAL value like the lists
	// above, and the launch scope is built from it (LM23, LM24).
	envImplicitAdmins = "FUGARO_IMPLICIT_ADMINS"
)

// credentialsServiceEnvNames is the whole of this command's REQUIRED
// environment: the seven names credentials.tf sets on the service's container,
// the ones whose absence is a service that cannot say which installation it is
// serving. The class-specific entries it reads beside them are OPTIONAL and
// live outside this list, because the module renders each of those only where
// it applies (envReviewJobAccounts, envSharedJobAccounts, envVerifierAccounts,
// envPersonAccounts, envCoordinatorAccount, envLauncherAccounts,
// envBuildAccounts and envOrchestratorAccounts).
// internal/infra/tf's credentialsServiceEnv is init's cover's allow-list, which
// is these seven plus those seven: a name outside it is a container the cover
// refuses, which is what makes the module's list a boundary rather than a
// convention. The lists are held together by tests
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

// credentialsServiceWorkspaceEnvNames is the whole of what the WORKSPACE's
// process requires: the six names the workspace root sets on the service it
// deploys (deploy/terraform/gcp/roots/workspace/main.tf, planned against
// internal/infra/tf's WorkspaceServiceEnv, WS6), which is that list less the
// mode's own name (creds.WorkspaceModeEnv, read before this list). The six are
// the installation's own first six because a workspace is a Fugaro project like
// any other and this process reads the SAME things about it: its own database,
// its own Firebase project, its own URL.
//
// **FUGARO_JOB_ACCOUNTS is deliberately not here**, and it is the one name the
// two lists differ by: that document is an installation's per-repository jobs,
// and a workspace's deployment has none — a member's job is admitted by the
// published credentials node and the invoke grant, not by an environment
// (WorkspaceServiceEnv's own comment). A process that required it would refuse
// to start on the container the workspace root creates, which is the whole
// deployment this list exists for.
var credentialsServiceWorkspaceEnvNames = []string{
	envProject, envGCPProject, envRegion,
	envRTDBURL, envFirebaseFP,
	envCredentialsServiceURL,
}

// credentialsServiceMissing is the refusal for a name the process needs and its
// deployment did not set, and it says which deployment that is: the installation
// module's service for the seven names, the workspace root's for the six a
// workspace's process needs. An operator reads this line with the root they
// applied in hand, so the sentence names the variable, the deployment that sets
// it and nothing that was in it.
func credentialsServiceMissing(workspace, name string) string {
	set := "the installation module sets it on the service"
	if workspace != "" {
		set = "the workspace mode is on (" + creds.WorkspaceModeEnv + " is set), so the workspace root sets it on the service"
	}
	return fmt.Sprintf("the credentials service needs %s in its environment (%s, and it is the whole of how this process is configured: there is no flag, no config file and no default)", name, set)
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
	// database builds the client the process reaches its own database with, at
	// the URL it was configured with. nil is rtdb.New over the token source
	// above, and a test sets it for the reason the other two are here and one
	// more: the workspace mode READS its published credentials node at startup
	// (credentialsWorkspaceService), so a test of that read has to reach a fake
	// rather than a Firebase host.
	database func(ctx context.Context, url string, ts oauth2.TokenSource) (*rtdb.Client, error)
}

// credentialsTest is nil in production: the three functions above are then
// google.DefaultTokenSource over the database scopes, Secret Manager's own
// client and rtdb's own constructor, all over the service's identity (the
// service account the revision runs as, which is the one grant it holds).
var credentialsTest *credentialsWiring

// credentialsWorkspaceTest is nil in production: C1's constructor for the
// workspace's resolver, which is NewWorkspaceScopeResolver -- a VARIABLE in
// another package, assigned by that package's own file (scope_workspace.go) and
// by nothing outside it.
//
// A test of this package sets it to record the configuration the process built
// -- the whole of what WS5 is accepted for, and unobservable through the
// resolver's own answers -- and delegates to the real constructor, so that the
// resolver the process serves under is C1's and not the test's. It is never set
// outside a test and carries no value.
var credentialsWorkspaceTest func(cfg credsvc.WorkspaceScopeConfig) (credsvc.ScopeResolver, error)

// credentialsDatabase builds the client the process reads and writes the
// database at url with: the service's own token source, which is the identity
// every read and write is made as. The URL is the only thing that says which
// database a call reaches, and it is never built here -- an installation's comes
// from FUGARO_RTDB_URL and a member's from the member's own Firebase project
// (credsvc's workspaceDatabase, which this function is handed the answer of).
func credentialsDatabase(ctx context.Context, url string, ts oauth2.TokenSource) (*rtdb.Client, error) {
	if credentialsTest != nil && credentialsTest.database != nil {
		return credentialsTest.database(ctx, url, ts)
	}
	return rtdb.New(url, rtdb.Auth{Source: ts})
}

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
// The shared project jobs' class (AuthConfig.SharedJobAccounts and
// Config.SharedAccounts, which are the same rows) comes from the deployment
// too: FUGARO_SHARED_JOB_ACCOUNTS, the coding half of the class the review
// entry below carries, whose rows are the (profile, provider, account) triples
// the shared jobs were deployed over plus the repositories each profile is
// bound to — the member G21 needs and the policy read would have supplied
// (credsvc.SharedJobAccountsFor derives the same rows from a published policy,
// which is what a caller holding the document can compare against). An empty
// set is this class's fail-closed statement: no shared caller is admitted at
// all, which is the state of every installation whose module does not render
// the entry yet.
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
	// WHICH SERVICE THIS PROCESS IS (R2, C2). FUGARO_WORKSPACE_MODE is the
	// mode's own name: it is set only on the workspace's service and its value
	// is the workspace this process is the credentials service OF. It is read
	// before anything else because it decides which environment the rest of
	// this function reads: an installation's container carries no such name
	// (credentials.tf), and the workspace's carries it and none of the
	// installation's class lists (internal/infra/tf's WorkspaceServiceEnv,
	// which the workspace root sets and that cover holds it to, WS6). With the
	// name absent the process below is exactly the one it was before the mode
	// existed: same names, same tables, same Server, and no resolver of its
	// own.
	workspace := getenv(creds.WorkspaceModeEnv)
	// The names this process requires: the installation's seven, or a
	// workspace's six (credentialsServiceWorkspaceEnvNames). An empty variable
	// is not a value either deployment can set: Terraform writes each of these
	// from an input the root has, so an empty one is a deployment that is wrong
	// rather than a deployment with a default. The names read outside this loop
	// are the class-specific ones the module renders only where they apply, and
	// the rest are not.
	required := credentialsServiceEnvNames
	if workspace != "" {
		required = credentialsServiceWorkspaceEnvNames
	}
	env := map[string]string{}
	for _, k := range required {
		if env[k] = getenv(k); env[k] == "" {
			return credsvc.Config{}, userErr("%s", credentialsServiceMissing(workspace, k))
		}
	}
	// The mode's value IS the workspace's Fugaro project name: the name its
	// bundle container is derived from, the name its published node is read
	// under and the name its own record is written at. The workspace root sets
	// both from one input (roots/workspace's var.workspace, WS6), so a pair that
	// disagrees is a deployment no plan of this repository produces — and
	// refusing it here rather than serving it is what keeps this process from
	// reading the node of the workspace the mode names while holding the bundle
	// of the project FUGARO_PROJECT names, which is a deployment handing out one
	// project's credentials under another's name. The refusal names the two
	// variables and neither value.
	if workspace != "" && env[envProject] != workspace {
		return credsvc.Config{}, userErr("%s and %s disagree: the mode's value is the workspace this service is the credentials service OF, which is the workspace's own Fugaro project name, and the workspace root sets the two from one input. Nothing was started and no credential was issued", creds.WorkspaceModeEnv, envProject)
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
	// The launcher class's list (D1938, F25, F36), read by the person list's own
	// rule and for the same reason: the entry is rendered only on an
	// installation whose roster names a launcher, and one that names none gets a
	// class that admits nobody rather than a service that will not bind a port.
	launchers, err := credentialsServiceLaunchers(getenv(envLauncherAccounts))
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
	// The shared coding class (B5.1), read by the review class's own rule and
	// for the same reason: the entry arrives with the deployment of the shared
	// jobs, so an installation whose module does not render it starts with an
	// empty set and refuses every caller of the class whole -- the state this
	// class was in under Global Constraint 6 -- rather than refuse to bind a
	// port (`envSharedJobAccounts` states the direction and why it matters).
	sharedAccounts, err := credentialsServiceSharedJobAccounts(getenv(envSharedJobAccounts), env[envGCPProject])
	if err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	// The build class (B5, D2301), read by the verifier list's own rule and for
	// the same reason: the entry arrives with the deployment of the build
	// identity the module creates beside the service, so a service deployed by
	// a release that renders none starts with an empty class and refuses every
	// caller of the build route rather than refusing to bind a port
	// (`envBuildAccounts` states the direction and why it matters).
	builds, err := credentialsServiceBuildAccounts(getenv(envBuildAccounts))
	if err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	// The resident daemon's own class (E16, D2049; F19, O2), read by the build
	// class's rule and for the same reason: the entry arrives with the daemon
	// account the installation module creates, so a service deployed by a
	// release that renders none starts with an empty class and answers the
	// label route and the merge mint with the class's own refusal rather than
	// refusing to bind a port (`envOrchestratorAccounts` states the direction
	// and why it matters, and the audience is the second half of the gate).
	orchestrators, err := credentialsServiceOrchestrators(getenv(envOrchestratorAccounts))
	if err != nil {
		return credsvc.Config{}, userErr("%v", err)
	}
	// The identity half's three values (LM22): the signer a run's identity is
	// signed as (D3121), the roster's service accounts (D3123) and the implicit
	// admins the CLI's own gate reads (F29, R16). They are read here into C1's
	// configuration, and the issuer's own four parts are built from them below:
	// the store (LM1), the scope check (LM23, LM24) and the ticket's bound on a
	// run's runtime. Each is read as an OPTIONAL value by the rules
	// above -- the module that renders them is LM12, so an installation whose
	// module does not render one yet must start the service it always did --
	// while a value that is present and is not the document the module renders
	// is a deployment that is wrong, refused by name and never by value.
	var (
		signer         string
		rosterAccounts []string
		implicitAdmins []string
	)
	if workspace == "" {
		if signer, err = credentialsServiceSigner(getenv(envSignerAccount)); err != nil {
			return credsvc.Config{}, userErr("%v", err)
		}
		if rosterAccounts, err = credentialsServiceRosterServiceAccounts(getenv(envRosterServiceAccounts)); err != nil {
			return credsvc.Config{}, userErr("%v", err)
		}
		if implicitAdmins, err = credentialsServiceImplicitAdmins(getenv(envImplicitAdmins)); err != nil {
			return credsvc.Config{}, userErr("%v", err)
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
	// The installation's own job document, and the three tables derived from it.
	// A workspace's process reads none of them and its container carries no such
	// name (credentialsServiceWorkspaceEnvNames): the identities it admits are
	// the members' job accounts the published node names, and the repositories a
	// call is bound to are the node's own per-account lists (C3's
	// JobAccountRepos), so a table built from an environment here would be a
	// second answer to a question the node has already answered. The three
	// values stay empty, and cfg.JobAccounts with them: a workspace's process is
	// configured with no job document at all, its resolver binds its runs
	// through the node, and NewServer accepts an empty table from a deployment
	// that hands in a resolver of its own (server.go's check).
	var (
		accounts   []credsvc.JobAccount
		repos      map[string]string
		repoBuilds []credsvc.RepoBuildAccount
	)
	emails := []string{}
	if workspace == "" {
		accounts, repos, repoBuilds, err = credentialsServiceJobAccounts(env[envJobAccounts], env[envProject], env[envGCPProject])
		if err != nil {
			return credsvc.Config{}, userErr("%v", err)
		}
		for _, a := range accounts {
			emails = append(emails, a.Email)
		}
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
	db, err := credentialsDatabase(ctx, env[envRTDBURL], ts)
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
	// The workspace's own half of the process (WS5), and nothing for an
	// installation: the node this deployment reads its callers out of, the
	// verified front door built over it, the meter over the workspace's own
	// database, and C1's resolver, which is what the Server serves under. It is
	// built BEFORE the verifier because the member table is this deployment's
	// caller list: a workspace's process has no job document to read one from
	// (above), and the accounts the node names are the identities its auth is
	// built with.
	var (
		members  []credsvc.WorkspaceMember
		resolver credsvc.ScopeResolver
	)
	if workspace != "" {
		half, err := credentialsWorkspaceService(ctx, workspace, env, db, ts)
		if err != nil {
			return credsvc.Config{}, err
		}
		members, resolver = half.members, half.resolver
		for _, m := range members {
			emails = append(emails, m.JobAccounts...)
		}
	}
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
		// The launcher class (D1938, F25): the people who may ask the service
		// for a slip and reach the intake, by the bare address a Google ID
		// token's email claim carries -- the same shape PersonAccounts has, and
		// a class of other routes rather than a widening of that one, so an
		// installation whose roster names no launcher starts with a class that
		// admits nobody and its read mints unchanged.
		LauncherAccounts: launchers,
		// ... and its second class (BB5): the coordinator's own service
		// account, which is the identity the scheduled sweep calls with. Empty
		// is the state an installation that runs no coordinator starts in.
		CoordinatorAccount: coordinator,
		// The review-only class (C3.9a): the jobs `fugaro review` runs on, by
		// the service account each one was deployed as, with the value of the
		// account axis its calls are served by. Empty is the state an installation whose
		// module renders no review-job document starts in, and a caller of the
		// class is then no class at all rather than a class with no route.
		ReviewJobAccounts: reviewAccounts,
		// The shared coding class (B5.1): the jobs a coding run in a shared
		// project job runs in, by the service account each one was deployed
		// as, with the profile it belongs to and the repositories that
		// profile is bound to. Empty is the state an installation whose module
		// renders no shared-job document starts in, and a caller of the class
		// is then a caller no route is served for.
		SharedJobAccounts: sharedAccounts,
		// The build class (B1, B5): the project's build identity, which is the
		// account a catalog build runs as and the one caller class of the build
		// route. Empty is the state a service deployed before the module
		// rendered the entry starts in, and the route then answers every caller
		// with the class's refusal.
		BuildAccounts: builds,
		// The resident daemon's class (E16, D2049; F19, O2): the one account
		// that may ask this service for a pull request's label write and for a
		// merge token, and the class of those two routes and of nothing else.
		// Empty is the state an installation that runs no daemon starts in —
		// and the state every installation is in until the module renders the
		// entry — and both routes then answer every caller with the class's
		// refusal.
		OrchestratorAccounts: orchestrators,
		// The repositories' build class (B4.3, R5, G20): one row per repository
		// this deployment holds jobs for, bound to that one repository, its slug
		// and its provider. It is the one caller class of the build-credentials
		// route, and the binding is the whole of what that route serves: a
		// build of one repository is answered that repository's credentials and
		// never a name its call carries. Empty is the state an installation
		// whose job document is empty starts in — which the reader above
		// refuses — and a caller of the class is then no class at all.
		RepoBuildAccounts: repoBuilds,
		Keys:              credsvc.NewGoogleKeys(),
		JobKeys:           credsvc.NewGoogleKeys(credsvc.WithKeysEndpoint(credsvc.GoogleOAuthCertsURL)),
		Outcomes:          credsvc.RTDBOutcomes{DB: db},
	})
	if err != nil {
		return credsvc.Config{}, userErr("the credentials service cannot verify its callers, so it will not start: %v", err)
	}
	// MG3's release-1 bridge, which the chain walk (CS1) consults for a run
	// whose image predates the routing table: the old access record read as the
	// chain it stands for, over this deployment's own database client —
	// therefore the same service identity every node beside it is read with —
	// and bounded by the published policy's paid fallback, which is what an
	// `api-key` or `cascade` record was bound by before the table existed
	// (legacy_auth.go). It is assigned because this is the one place a client
	// exists — the bridge takes creds.DB so that a test can drive it against a
	// fake, and the seam's own default is the stub that answers "no old record"
	// (chain_stub.go) — and it is assigned rather than left to that default
	// because the default is the answer that refuses a run whose image predates
	// the table, which is the one thing the migration's first release promises
	// not to do.
	//
	// An INSTALLATION assigns it, and a workspace does not. `fugaro update`
	// writes the record into the database of the repository's OWN project, so a
	// workspace's process serves members whose records live in the MEMBERS'
	// databases and reads them there, through the scope's own reader, per call
	// (scope.go's RepoAuth): the member's own service is what authored it. This
	// bridge holds one client and holds it for the whole process — the seam's
	// answer has no caller in it (chain_wire.go) — so a workspace that assigned
	// one would answer a member's older run out of the WORKSPACE's database:
	// an absent record where it holds no such slug, and, where it holds one,
	// another installation's record, which would serve a member's call off a
	// reach nobody recorded for it. Both directions are worse than the stub's
	// refusal, which is what a workspace keeps here: a member's run whose image
	// predates the table is refused rather than served a stranger's reach, and
	// the bridge it needs is the walk's to assign, because the walk is what
	// holds the caller's scope and therefore the member's database. X4 removes
	// the assignment with the bridge.
	if workspace == "" {
		credsvc.LegacyChain = credsvc.NewLegacyAuth(db, credsvc.RTDBPolicy{DB: db})
	}
	// The issuer's last three parts (I1): the issuance record's store, the
	// launch-scope check with the orchestrator's class composed behind it, and
	// the readers those two need. They are built here and not in credsvc
	// because every one of them is a read of THIS deployment's database — the
	// same client the routes beside them use and therefore the same service
	// identity — and because the scope's own inputs are the deployment's: the
	// repository table, the records, the published policy and the implicit
	// admins, exactly as the CLI's own launch gate reads them (R16, F29).
	//
	// A workspace builds one too and never uses it: its container carries no
	// signer (the block below is read only for an installation), so its
	// identity routes answer the contract's "not served" whatever this holds,
	// and the orchestrator's record is not a workspace's business at all.
	scope, err := credentialsIdentityScope(db, repos, implicitAdmins, env[envProject])
	if err != nil {
		return credsvc.Config{}, userErr("the credentials service cannot judge a launch scope, so it will not start: %v", err)
	}
	return credsvc.Config{
		Auth:    auth,
		Bundles: loader,
		// Resolver is the front door's scope resolver (C1): nil for an
		// installation, which is its own — NewServer builds
		// installationResolver over this same configuration
		// (scope_installation.go), so an installation's deployment is served by
		// the one front door it always was — and the workspace's own for the
		// mode above (WS5), which is the only deployment whose runs its
		// configuration could otherwise not bind: a workspace's container
		// carries no job document (credentialsServiceWorkspaceEnvNames), so
		// this table is empty there and the node the resolver re-reads is what
		// binds a call.
		Resolver: resolver,
		Policy:   credsvc.RTDBPolicy{DB: db},
		State:    &creds.RTDBStore{DB: db},
		Kills:    credsvc.RTDBKills{DB: db},
		DB:       db,
		Probes:   db,
		// The same client is the create-only write the sign route's
		// one-signature mark is taken with (C3.9e, D1074): the mark is a node
		// under creds/ like the probe's lease, read for its ETag and written
		// back under it, over the service's own identity — which is the only
		// writer of that subtree (the rules deny every run token it).
		Signs: db,
		// The slips (DS1.1, DS1.3): the grant a launcher asks for and the claim
		// a run makes once, over the same client and therefore the same service
		// identity. It is a second store of its own rather than State's, because
		// a slip is not a selection's business — it is written before the run
		// exists and read on every call the run makes — and it is not optional:
		// NewServer refuses a service that cannot write one, because a service
		// that cannot grant a run its credentials has no purpose at all.
		Slips: &creds.RTDBStore{DB: db},
		// The workflow-secrets record (B3.1, B3.2): what `fugaro update` wrote
		// for each repository's jobs, read over the same client and therefore
		// the same service identity. It is the ONE node the workflow-secrets
		// route serves a call out of, so a deployment that wired nothing here
		// would refuse every one of those calls rather than answer an empty
		// value — which is why the reader is wired with the route's own field
		// and not left to the database the other routes write through.
		WorkflowSecrets: credsvc.RTDBWorkflowSecrets{DB: db},
		// The project record (IS3, D3328, D3333): which repositories may fetch
		// a project-shared workflow secret by name. It is the fence a
		// version-2 call and a build's read of a `fugaro_p_` container are
		// admitted by, read over the same client and the same service identity
		// as the record above.
		ProjectSecrets: credsvc.RTDBProjectSecrets{DB: db},
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
		// The shared coding class's own table (B5.1): the same rows
		// AuthConfig.SharedJobAccounts holds, because the pairing needs the
		// profile and the repositories the row is bound to where the verifier
		// needs only the class.
		SharedAccounts: sharedAccounts,
		// The repository table, which is the pairing's second step for a
		// review caller (C3.9a) and for a shared one (B5.1), and is read from
		// this variable's own input above rather than from the published
		// policy: the table must be the one the per-repository bindings are
		// made from.
		Repos: repos,
		// The service's own URL, which is already read and checked as the
		// audience (credentialsServiceAudience above): a build's Bitbucket
		// grant names the proxy its git will reach, and the URL in that answer
		// is this service's and never the Host header of the call that asked
		// for it -- a grant built from a caller's own text would send a
		// build's clone wherever its caller said (B1, D2302). The check above
		// is what makes this the same value a caller's ID token is minted for,
		// so the grant names a URL the build can actually reach.
		BaseURL: env[envCredentialsServiceURL],
		// The build pairings (B1, rule 9): what a Bitbucket build's grant
		// records and what the proxy's own admission reads back on every call
		// that build's git makes, over the same client and therefore the same
		// service identity as the slips above. It is a store of its own
		// because the act is its own — a pairing is neither a selection's
		// business nor a run's, it is written as a grant is answered and read
		// by a caller that is not a run at all — and it is wired here rather
		// than left to the database the other routes write through: a
		// deployment that wired nothing would refuse every Bitbucket build
		// rather than answer a grant naming a proxy that admits nothing.
		BuildPairs: &creds.RTDBStore{DB: db},
		Log:        log,
		// The identity half (LM22, C1, I1): the values this command read out of
		// the deployment's environment above, and the rest of what the issuer
		// is built from — the issuance record's store, the launch-scope check
		// with the orchestrator's class composed behind it, and the bound on a
		// run's runtime. Every one of them is wired here rather than left to a
		// default, because each answers a question the routes ASK: a store the
		// service did not wire is a wiring fault the ticket route answers as
		// one, and a maximum of zero refuses every ask there is.
		//
		// An empty Signer is the state all of that is mute about: an
		// installation whose Firebase root carries no issuer row yet, and one
		// deployed by a release before this work, has no account to sign a run's
		// identity as, and its six identity routes answer the contract's "not
		// served" (R7) whatever this holds — the bridge a launcher of this
		// release signs a run's identity locally across, as long as the people's
		// minter row is still there.
		//
		// VMIdentity is deliberately left nil, and it is the one field of this
		// configuration a deployment cannot yet fill: it is the link from a
		// claiming VM to the Batch job the record names (BR3 of
		// docs/batch-run-identity), whose reader that plan builds. Nil is the
		// state C1 states for it and not an oversight — a Cloud Run launch
		// names no job at all and claims exactly as it did (R18), and a claim
		// on a record that DOES name a job is answered the contract's "not
		// served" rather than served on the account check alone (D3114).
		Identity: credsvc.IdentityConfig{
			Signer:          signer,
			ServiceAccounts: rosterAccounts,
			// The roster's operators (R2, A2): the read mint's admitted people,
			// the addresses `fugaro update` may be run by, and the only class
			// the probe admits. They are this installation's
			// FUGARO_PERSON_ACCOUNTS — the module renders them under that one
			// name (credentials.tf's credentials_read_people) because the two
			// are one list of one set of people — and a second variable for
			// this second reader would be a second list to keep in step.
			Operators: persons,
			// The implicit admins, as the gate reads them (F29, R16): the
			// machine's own terraform.budget_admins `user:` entries, which the
			// scope check adds to the records' members so that an owner is not
			// refused a launch that works today.
			ImplicitAdmins: implicitAdmins,
			// The ceiling on a run's identity (R4): a launch's ask is its
			// repository's own timeouts.total plus the queue allowance, and one
			// beyond the ceiling is refused rather than clamped — a shorter
			// identity than the run needs is a run that halts in the middle of
			// its work. A day is the longest a job's execution is held at all
			// on either platform a run runs on (Cloud Run jobs and Cloud Batch
			// both cap a task at 24 hours), so the ceiling is above every ask an
			// installation could honour.
			MaxRuntime: identityMaxRuntime,
			// The issuance record's store (LM1) and the launch-scope check
			// (LM23 with LM24 behind it), both built over this deployment's own
			// database client above.
			Store: credsvc.NewIssuanceStore(db),
			Scope: scope,
		},
	}, nil
}

// credentialsWorkspaceDeployment is the workspace's own half of the process: what
// the mode builds that an installation's deployment has no counterpart for
// (WS5).
type credentialsWorkspaceDeployment struct {
	// members is the published node's member table, read by credsvc's own
	// conversion (WorkspaceMembersOf) so that the node's reader and the service
	// cannot come to different answers about what a row says. It is this
	// deployment's whole caller list: the accounts in it are the ones its
	// verifier admits.
	members []credsvc.WorkspaceMember
	// resolver is C1's workspace resolver, built over the front door, the node
	// reader and the meter below. It is what the Server serves every call of
	// this deployment under, and it is the same Server an installation is
	// served by.
	resolver credsvc.ScopeResolver
}

// credentialsWorkspaceService builds the workspace's half: the node reader, the
// verified front door, the meter, the personal half and the resolver, in that
// order.
//
// WHAT IT READS AT STARTUP, AND WHY IT READS IT HERE. The workspace's members
// are not an environment: they are the workspace's own published credentials
// node (projectpolicy.PathWorkspaceCredentials of this workspace, C3's node),
// which the workspace owner's `fugaro update` writes (WS4), and this process
// reads it ONCE, at startup, out of the workspace's OWN database (the one
// FUGARO_RTDB_URL names; no member's database is reached before a caller has
// been verified). Two things follow from that read being here rather than in a
// call:
//
//   - the *Workspace built from it is the deployment's verified front door: it
//     answers which member a job account belongs to, resolves the run identity
//     beside it and holds that member's own outcome reader and database. It is
//     the VERIFIER and not the membership — who is admitted is what the node the
//     resolver re-reads says (C1's WorkspaceScopeConfig comment, R11/R13), and
//     nothing in this function freezes that.
//   - a workspace that has published no node, or one that cannot be read or
//     decoded, REFUSES here rather than starting: a revision that fails to start
//     is what tells an owner which step is left (their `fugaro update`), while a
//     service that answered every call with a refusal would look healthy and
//     halt every run that reached it (D143's fail-closed direction).
//
// It builds the meter over the same client and the same database (budget's
// WorkspaceSpendDB, which *rtdb.Client is): the cap it enforces is the
// WORKSPACE's own figure, and the meter holds no write on any member. The
// resolver is C1's constructor's answer — a workspace whose resolution could not
// be built is a refusal and not a nil, which is what keeps a call from being
// served by something nobody checked.
//
// The PERSONAL half (C2, WC14) is the other read of the workspace's own
// database this function makes, and it is built by credentialsWorkspacePerson
// below: the roster node's reader, the launcher class it admits its people with
// and the slip store its own grants live in. It is set on the package variable
// the workspace's scope reads at construction and set BEFORE the resolver is
// built, because the resolver is what reads it (C2's two variables). Its own
// refusals, and why a workspace with no roster node starts anyway, are that
// function's.
func credentialsWorkspaceService(ctx context.Context, workspace string, env map[string]string, db *rtdb.Client, ts oauth2.TokenSource) (credentialsWorkspaceDeployment, error) {
	node := credentialsWorkspaceNode{db: db, name: workspace}
	published, found, err := node.WorkspaceNode(ctx)
	if err != nil {
		return credentialsWorkspaceDeployment{}, remote(fmt.Errorf("the workspace's published credentials node could not be read: %w", err))
	}
	if !found {
		return credentialsWorkspaceDeployment{}, userErr("%s is set and the workspace it names has published no credentials node: the node is what this service binds its callers and its pool to, and the workspace owner's `fugaro update` is what publishes it. Nothing was started and no credential was issued", creds.WorkspaceModeEnv)
	}
	members := credsvc.WorkspaceMembersOf(published)
	w, err := credsvc.NewWorkspace(credsvc.WorkspaceConfig{
		Project:  workspace,
		Audience: env[envCredentialsServiceURL],
		Members:  members,
		// No owner person is named, and none can be: the workspace-wide routes
		// that class is for are not among the routes this mode serves (R14: the
		// model routes, a run's forge routes and the keep-warm route, and the
		// workspace owner's own read mint stays the installation's, D2988). The
		// roster WC14 hands the personal half below is the LAUNCHER class's and
		// not this one's (C2's rule 2, R3). An empty list is the fail-closed
		// answer for a class no route here admits.
		Keys:    credsvc.NewGoogleKeys(),
		JobKeys: credsvc.NewGoogleKeys(credsvc.WithKeysEndpoint(credsvc.GoogleOAuthCertsURL)),
		// A member's database client, at the URL the service derived from that
		// member's own Firebase project: this function opens exactly the URL it
		// is handed and chooses nothing about which member's state a call may
		// read.
		Open: func(databaseURL string) (credsvc.Getter, error) {
			return credentialsDatabase(ctx, databaseURL, ts)
		},
		// The pool's state and the stop switch are the workspace's own
		// database's, the one FUGARO_RTDB_URL names; no member's database is
		// ever handed to either (R8, D1302).
		State: &creds.RTDBStore{DB: db},
		Kills: db,
	})
	if err != nil {
		return credentialsWorkspaceDeployment{}, userErr("the workspace's service cannot verify its callers, so it will not start: %v", err)
	}
	meter, err := credsvc.NewWorkspaceMeter(credsvc.WorkspaceMeterConfig{Name: workspace, DB: db})
	if err != nil {
		return credentialsWorkspaceDeployment{}, userErr("the workspace's service cannot keep the workspace's own cap, so it will not start: %v", err)
	}
	// The workspace's personal half (C2, WC14), set on the package's one
	// configuration variable and set BEFORE the resolver is built: the
	// workspace's scope reads it once, at construction, and never per call
	// (C2's "Two package-level variables are how the sides meet"). The
	// assignment is unconditional, so a workspace with no roster node writes the
	// ZERO configuration there rather than leaving whatever another deployment
	// of this process had put in it.
	person, err := credentialsWorkspacePerson(ctx, workspace, env, db)
	if err != nil {
		return credentialsWorkspaceDeployment{}, err
	}
	credsvc.PersonScopeConfigForServe = person
	// The switch this process reads a member's personal half with (R9, C1's
	// PersonSwitchReader): WC6's reading of the MEMBER the call is under, taken
	// from the call's own scope at the moment it is asked (credsvc.ScopePersonSwitch).
	// It is assigned here and nowhere else, and it is the line that makes the
	// personal half serve a token at all: the variable's own default answers OFF
	// for every project, so a workspace whose half IS wired would refuse every
	// call whose slip names a personal token with the sentence R11 reserves for a
	// member who turned the switch off.
	credsvc.PersonSwitchForServe = credsvc.ScopePersonSwitch()
	build := credsvc.NewWorkspaceScopeResolver
	if credentialsWorkspaceTest != nil {
		build = credentialsWorkspaceTest
	}
	resolver, err := build(credsvc.WorkspaceScopeConfig{Workspace: w, Node: node, Meter: meter})
	if err != nil {
		return credentialsWorkspaceDeployment{}, userErr("the workspace's service cannot serve a call, so it will not start: %v", err)
	}
	return credentialsWorkspaceDeployment{members: members, resolver: resolver}, nil
}

// credentialsWorkspacePerson builds the workspace's personal half (C2, WC14):
// the roster node's reader, the launcher verifier the workspace's own roster
// admits its people with, and the slip store its own grants live in — all three
// over the workspace's OWN database, the one this process was handed, and none
// of them over a member's (R3, R8 of workspace-service).
//
// THE ROSTER IS READ HERE ONCE, FOR THE CLASS, AND THE READER IS HANDED ON
// UNREAD. What a person may LAUNCH with is the workspace owner's published
// roster (D2970, R3), and an addition to it takes effect at the owner's next
// `fugaro update` (D2971), which is why the class's account list is fixed at
// startup. What the reader answers beside it is not fixed at all: the scope
// re-reads the node through it on the sixty-second rule the published node has
// (C2's rule 3, WC4's clock), so a person REMOVED from the roster stops being
// served within a minute without a restart. The reader is therefore the store
// itself and never a snapshot of it.
//
// The class is PEOPLE ONLY, and the roster's machines are not on it (F12, C2's
// rule 2): a `sa` person of the roster is a service account, and a service
// account is never a launcher of this service — which is why no second list is
// handed to it (a workspace's grant is asked for by a person, on a run they
// launch; the roster's machines are the ones the WORKSPACE's own project runs
// things as, WC3).
//
// AN ADDRESS OF THAT SHAPE IS DROPPED BEFORE THE CLASS IS BUILT, WHATEVER THE
// ROSTER CALLS IT. The class's own construction refuses a
// `*.gserviceaccount.com` entry, and nothing upstream stops one from being
// written as a HUMAN: a roster address is held to the address grammar and not
// to its domain, `workspace user add` records a person as a human unless told
// otherwise, and adopting a member project's grants records every address it
// finds as one — a member's own roster among them, which that member's admins
// can write (D1943). A class built straight off the people would then refuse
// this whole deployment at its next cold start, every member's model and forge
// routes with it, over an entry that could never be verified as a person
// anyway. So the entry is left out and the process starts: C2 fixes a roster
// this service can serve nobody from as a state and not a refusal ("What each
// side may rely on", WC14's paragraph).
//
// A WORKSPACE WITH NO ROSTER NODE IS NOT A FAILURE TO START; A ROSTER THIS
// BUILD CANNOT READ IS. The first is the state of every workspace whose owner
// has published no roster — the state an installation's deployment is in, where
// the personal half is not wired at all — and C2 fixes it as a state rather
// than a refusal: the scope serves no grant and no personal candidate, which is
// today's behaviour ("What each side may rely on", WC14's paragraph). The
// second is a deployment whose owner HAS published one, and a process that
// started with the half silently unwired would refuse every grant of every
// person the owner just added while looking healthy — the reading this file
// already takes of the published node above (D143's fail-closed direction),
// which is read out of the same database a moment earlier and so cannot be
// excused as an outage of something else.
func credentialsWorkspacePerson(ctx context.Context, name string, env map[string]string, db *rtdb.Client) (credsvc.PersonScopeConfig, error) {
	roster, err := workspace.NewRosterStore(db, name)
	if err != nil {
		return credsvc.PersonScopeConfig{}, userErr("%s names a workspace whose roster node cannot be addressed, so its service will not start: %v", creds.WorkspaceModeEnv, err)
	}
	node, found, err := roster.Read(ctx)
	if err != nil {
		return credsvc.PersonScopeConfig{}, remote(fmt.Errorf("the workspace's roster could not be read: %w", err))
	}
	if !found {
		return credsvc.PersonScopeConfig{}, nil
	}
	// Decoded here and not by the scope, because the CLASS is built from it and
	// the class is this function's: what the scope reads through the reader
	// above is C1's business and stays C1's (C2's "the shape of the document is
	// internal/workspace/roster.go's").
	document, err := workspace.DecodeRoster(node.Raw)
	if err != nil {
		return credsvc.PersonScopeConfig{}, userErr("%s names a workspace whose roster is not a document this build reads, so its service will not start: %v", creds.WorkspaceModeEnv, err)
	}
	launchers, err := credsvc.NewLaunchers(credsvc.LauncherConfig{
		Audience: env[envCredentialsServiceURL],
		Accounts: workspaceRosterLaunchers(document),
		// Google's published ID-token key set and not the Firebase set: this
		// class is a person's own `gcloud` identity, and the Firebase keys sign
		// the run identities this process does not hold (the intake's own
		// construction, intake_serve.go).
		Keys: credsvc.NewGoogleKeys(credsvc.WithKeysEndpoint(credsvc.GoogleOAuthCertsURL)),
	})
	if err != nil {
		return credsvc.PersonScopeConfig{}, userErr("%s names a workspace whose roster does not name a launcher class this service can verify its callers with, so it will not start: %v", creds.WorkspaceModeEnv, err)
	}
	return credsvc.PersonScopeConfig{
		Roster:    roster,
		Launchers: launchers,
		Slips:     &creds.RTDBStore{DB: db},
	}, nil
}

// workspaceRosterLaunchers is the roster's people as the launcher class's
// account list (R3, D2970): the address of every `human` and no machine's, in
// the roster's own spelling, sorted and each once — and no address of the
// service-account shape, whatever kind the roster gave it.
//
// The shape is dropped rather than handed on because the class refuses it and
// this deployment cannot afford that refusal: one roster entry mis-kinded as a
// human — a forgotten `--kind sa`, or a service account a member's own roster
// granted a role to and this workspace adopted — would otherwise stop the whole
// service on its next cold start, every member's routes with it. An address of
// that shape can never be a caller of the launcher class (credsvc's own
// foldLauncherAccounts refuses it for the same reason), so leaving it out costs
// this class nothing it could have admitted. A roster whose people are ALL of
// that shape therefore yields an empty list and not a refusal: the class admits
// nobody, the scope serves no grant, and the process starts (C2).
//
// The roster's addresses are already canonical and unique within it
// (workspace.Roster.Validate), so the sort and the compact are the class's own
// shape rather than a repair of it (LauncherConfig.Accounts). An installation's
// launcher list is derived from a `user:`-prefixed member list instead
// (internal/infra's launcherAccounts, which drops every entry that is not one);
// this one is read off the roster's people, which are bare addresses and carry
// their kind in a field of their own (F11).
func workspaceRosterLaunchers(document *workspace.Roster) []string {
	out := []string{}
	for _, p := range document.People {
		if p.Kind != workspace.KindHuman {
			continue
		}
		// Folded, as the class folds it: the domain is lowercased by the
		// canonical spelling a roster is held to, and this is the same
		// comparison credsvc makes of the address it is handed.
		if strings.HasSuffix(strings.ToLower(p.Address), saAddressSuffix) {
			continue
		}
		out = append(out, p.Address)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// credentialsWorkspaceNode reads the workspace's own published credentials node
// out of the workspace's own database: the one read of this deployment that is
// not made for a call, and C1's WorkspaceNodeReader.
//
// It holds no cache and no clock of its own. The rule the resolver is held to —
// a copy older than sixty seconds is re-read, and a re-read that fails refuses
// the call rather than serving from the stale copy (R13, R17) — is the
// resolver's, with the node's caching and its clock beside it (C1's comment on
// the interface): this reader answers what is at the path NOW, and a caller that
// wants a freshness rule wraps it.
type credentialsWorkspaceNode struct {
	db   *rtdb.Client
	name string
}

// WorkspaceNode answers the node, false where none is published. The document is
// decoded by C3's own reader, which reads the version it finds, refuses a
// version no release wrote, and validates what it read; absent is false and not
// an error, because a workspace that published none is a state of the
// deployment and not a failure of the read (C1's WorkspaceNodeReader).
func (n credentialsWorkspaceNode) WorkspaceNode(ctx context.Context) (*projectpolicy.WorkspaceCredentials, bool, error) {
	var raw json.RawMessage
	found, err := n.db.Get(ctx, projectpolicy.PathWorkspaceCredentials(n.name), &raw)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	w, found, err := projectpolicy.DecodeWorkspaceCredentials(raw)
	if err != nil {
		return nil, false, err
	}
	return w, found, nil
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

// credentialsServiceLaunchers turns FUGARO_LAUNCHER_ACCOUNTS into the launcher
// class's list (D1938, F36): a JSON array of bare addresses, which is what the
// module renders from the installation's roster.
//
// It is credentialsServicePersons' rule word for word, and for the same reason:
// an absent or empty value is an EMPTY list and not a refusal, because the
// module renders the entry only on an installation whose roster names a
// launcher, while a value that is present and is not that array is a deployment
// that is wrong. The refusal names the variable and never an entry, because the
// entries are accounts of people.
//
// The list is the person list's shape and not its content: which of the two a
// given address is on is what makes it a class (D1938), so neither list is
// derived from the other here. An operator who launches is on both, which is
// the roster's doing and not this reader's.
func credentialsServiceLaunchers(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var launchers []string
	if err := json.Unmarshal([]byte(raw), &launchers); err != nil {
		return nil, fmt.Errorf("%s is not the installation's launcher list: it must be a JSON array of the bare email addresses a Google ID token carries, one per person who may ask the service to grant a run its secrets. Nothing was started and no credential was issued", envLauncherAccounts)
	}
	return launchers, nil
}

// credentialsServiceBuildAccounts turns FUGARO_BUILD_ACCOUNTS into the build
// class (B5, D2301): a JSON array of bare addresses, which is what the module
// renders from the build identity it creates — one address, because a project
// has one build identity, in the same array shape every list of the class is
// read in so that a project with more than one is a deployment the module can
// state rather than a shape this process has to grow for.
//
// It is credentialsServiceVerifiers' rule word for word, and for the same
// reason: an absent or empty value is an EMPTY class and not a refusal,
// because a release before this one renders no such entry and must still
// start — the build route then refuses every caller, which is the fail-closed
// state rather than a service that will not bind a port — while a value that
// is present and is not that array is a deployment that is wrong. The refusal
// names the variable and never an entry, because the entries are service
// accounts of the installation's own project.
func credentialsServiceBuildAccounts(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var builds []string
	if err := json.Unmarshal([]byte(raw), &builds); err != nil {
		return nil, fmt.Errorf("%s is not the installation's build identity list: it must be a JSON array of the bare email addresses a Google ID token carries, one per project identity that may ask the service for a build's credential. Nothing was started and no credential was issued", envBuildAccounts)
	}
	return builds, nil
}

// credentialsServiceOrchestrators turns FUGARO_ORCHESTRATOR_ACCOUNTS into the
// resident daemon's class (E16, D2049; F19 and O2 of docs/forge-labels): a
// JSON array of bare addresses, which is what the module renders from the
// account its own orchestrator.tf creates — one address, in the same array
// shape every list of a class is read in so that a second daemon is a
// deployment the module can state rather than a shape this process has to grow
// for.
//
// credentialsServiceBuildAccounts' rule word for word, and for the same
// reason: an absent or empty value is an EMPTY class and not a refusal,
// because the module renders the entry only on an installation that runs a
// resident orchestrator, and a release before this one renders none — a
// service deployed by it must still start, with both routes answering every
// caller with the class's own refusal, which is the fail-closed state rather
// than a service that will not bind a port. A value that is present and is not
// that array is a deployment that is wrong, and the refusal names the variable
// and never an entry: the entries are service accounts of the installation's
// own project (the no-echo rule).
//
// The class is not a widening of what a list of addresses usually admits: the
// address is matched against a token minted FOR THIS SERVICE'S URL alone
// (credsvc.VerifyOrchestrator), so a person's ambient credentials are refused
// whatever list they would have matched, and NewAuthenticator refuses an
// address that is also a person's, a launcher's, a job's, a shared job's or a
// review job's — the daemon is an automation of its own, and a run's own token
// admitted here would be exactly the credential C2.2 and F28 keep out of the
// mint.
func credentialsServiceOrchestrators(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var orchestrators []string
	if err := json.Unmarshal([]byte(raw), &orchestrators); err != nil {
		return nil, fmt.Errorf("%s is not the installation's orchestrator list: it must be a JSON array of the bare email addresses a Google ID token carries, one per resident orchestrator daemon that may ask the service for a label write and for a merge token. Nothing was started and no credential was issued", envOrchestratorAccounts)
	}
	return orchestrators, nil
}

// credentialsServiceSigner turns FUGARO_SIGNER_ACCOUNT into the identity
// half's signer (LM22, D3121): the account a run's identity is signed as, as
// the one bare address its Google ID token's email claim carries — the runs'
// signer modules/firebase/iam.tf creates and LM11 grants the issuer its single
// minter row on.
//
// It is credentialsServiceCoordinator's rule word for word and for the same
// reason: an absent or empty value is the EMPTY account and not a refusal,
// because the entry arrives with the module LM12 renders next, so a service
// deployed by a release before it must start the same service it always did —
// with no signer, which is an installation that has no issuer and whose
// identity routes answer the contract's "not served" (R7) — while a value
// that is present and is not one address is a deployment that is wrong. And
// whether the value is an ADDRESS at all is left to whoever consumes it,
// which is the coordinator's own rule: this reader holds it to the shape the
// module renders and decides nothing about the account.
//
// The refusal names the variable and never the value: it is an account, and the
// environment is this process's own (the no-echo rule).
func credentialsServiceSigner(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, " \t\r\n") {
		return "", fmt.Errorf("%s is not the installation's signer account: it must be one bare email address, the one a Google ID token's email claim carries, with no space and nothing else in the value. Nothing was started and no credential was issued", envSignerAccount)
	}
	return raw, nil
}

// credentialsServiceRosterServiceAccounts turns FUGARO_ROSTER_SERVICE_ACCOUNTS
// into the identity half's service-account class (LM22, D3123): a JSON array of
// bare addresses, which is what the module renders from the `serviceAccount:`
// entries of the installation's roster.
//
// It is credentialsServiceBuildAccounts' rule word for word, and for the same
// reason: an absent or empty value is an EMPTY list and not a refusal, because
// the entry arrives with the module LM12 renders next and an installation whose
// roster names no service account — or whose module does not render the entry
// at all — must start the same service it always did rather than refuse to bind
// a port; while a value that is present and is not that array is a deployment
// that is wrong. The refusal names the variable and never an entry, because the
// entries are service accounts of the installation's own project.
//
// It is read here into credsvc.IdentityConfig.ServiceAccounts, the roster the
// ticket route admits the launcher class from (LM3). A `group:` or
// `domain:` roster entry is deliberately not on it — an address that holds no
// token to present is one no caller can be — which is LM12's and the update's
// to say, not this reader's.
func credentialsServiceRosterServiceAccounts(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var accounts []string
	if err := json.Unmarshal([]byte(raw), &accounts); err != nil {
		return nil, fmt.Errorf("%s is not the installation's roster service accounts: it must be a JSON array of the bare email addresses a Google ID token carries, one per serviceAccount: entry of the installation's roster. Nothing was started and no credential was issued", envRosterServiceAccounts)
	}
	return accounts, nil
}

// credentialsServiceImplicitAdmins turns FUGARO_IMPLICIT_ADMINS into the
// identity half's implicit admins (LM22, F29, R16): a JSON array of bare
// addresses, which is what the module renders from the machine's
// terraform.budget_admins — the same list the CLI's own launch gate reads out
// of its local configuration (userImplicitAdmins, access_run_gate.go), so that
// the issuer's scope check gives the gate's answer and an owner is not refused
// a launch that works today.
//
// It is credentialsServiceRosterServiceAccounts' rule word for word, and for
// the same reason: an absent or empty value is an EMPTY list and not a refusal
// — the entry is the deployment's, and an installation whose local
// configuration names no admin must start the service it always did — while a
// value that is present and is not that array is a deployment that is wrong.
// The refusal names the variable and never an entry, because the entries are
// accounts of people.
//
// A `group:` or `domain:` member is not on the list by the gate's own rule: a
// member the gate cannot match against a caller is not one it can place. The
// value is read into credsvc.IdentityConfig.ImplicitAdmins, and the launch
// scope is built from it (LM23, LM24).
func credentialsServiceImplicitAdmins(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var admins []string
	if err := json.Unmarshal([]byte(raw), &admins); err != nil {
		return nil, fmt.Errorf("%s is not the installation's implicit admins: it must be a JSON array of the bare email addresses a Google ID token carries, one per terraform.budget_admins member the installation's own configuration names. Nothing was started and no credential was issued", envImplicitAdmins)
	}
	return admins, nil
}

// identityMaxRuntime is the ceiling on the runtime a launch may ask a run's
// identity for (credsvc.IdentityConfig.MaxRuntime, R4). The ticket route
// refuses an ask beyond it rather than clamping it, so this is the one place a
// deployment's answer to "how long may a run's identity live" is stated, and
// it is a constant of the process rather than a variable of the installation:
// every installation asks for the same thing, and it is the greater of the
// deployed job's own task timeout — the repository's timeouts.total plus
// backend.TaskTimeoutSlack, which internal/infra deploys — and --total-timeout
// plus the same slack, and both are held at or below Cloud Run's ceiling, by
// internal/projectpolicy for the class and by parseTotalTimeout for the flag
// (launchIdentityWindow). So the platform's own ceiling is the only bound that
// admits every ask a launch may legitimately make, and a smaller one is not a
// tighter policy but a launch that cannot work: the route refuses rather than
// clamps, so a repository whose timeouts.total is longer would be refused at
// the ticket route, with advice to launch a run the service refuses again.
// A maximum of zero, which is the fail-closed reading of an installation that
// stated none, would refuse every launch there is.
const identityMaxRuntime = gcp.MaxTaskTimeout

// credentialsIdentityScope builds the ticket route's launch-scope check (R16):
// the CLI gate's own judgement recomputed on the service's side of the wire,
// with the orchestrator's class composed behind it (LM23 and LM24).
//
// Every input is this deployment's: the repository table the route already
// binds a call to (the same `repos` map the rest of this configuration holds),
// the roles and scopes records and the published policy, both read over the
// database client above and therefore with this service's own account, and the
// implicit admins the machine's configuration names.
//
// The implicit admins are converted to MEMBERS here and not carried as the
// bare addresses the environment holds, because that is the spelling both
// readers of them use: internal/access matches a member against a member
// (access.Member, which is what the CLI's own gate builds its input from), and
// the addresses this deployment is rendered are the same ones with the prefix
// taken off (the module's own reader, credentialsServiceImplicitAdmins). The
// conversion is access.Member and not a `"user:" + addr`, so an entry the
// resolver cannot hold as a member places nobody — the fail-closed direction,
// and the one the gate takes for the same entry (userImplicitAdmins).
//
// An error is a wiring fault and not a deployment's typo: the only two ways to
// reach it are a nil database and a nil check, and a service that served an
// ask with no scope judged would issue a launcher a run's identity from a
// repository nothing admitted.
func credentialsIdentityScope(db *rtdb.Client, repos map[string]string, implicitAdmins []string, orchestratorID string) (credsvc.ScopeCheck, error) {
	members := make([]string, 0, len(implicitAdmins))
	for _, addr := range implicitAdmins {
		if member, err := accesslib.Member(addr); err == nil {
			members = append(members, member)
		}
	}
	return credsvc.NewOrchestratedScope(
		credsvc.NewLaunchScope(credsvc.LaunchScopeConfig{
			Repos:          repos,
			Records:        credsvc.RTDBAccess{DB: db},
			Policy:         credsvc.RTDBPolicy{DB: db},
			ImplicitAdmins: members,
		}),
		identityOrchestratorRecord{db: db, id: orchestratorID},
		identityPauses{db: db},
	)
}

// identityOrchestratorRecord is the resident orchestrator's own state node as
// the issuer reads it (credsvc.OrchestratorRecord, LM24/D3136): the attempts
// the daemon committed before it started anything, which is the whole of what
// the orchestrator's class may be issued a ticket for.
//
// It is read over this deployment's own database client — the same one every
// other node of this service is read with, and therefore the same service
// account — and the id it reads is the project's own orchestrator, which is the
// only one a project has: an id is `<project>` (orchestrator.PathState admits no
// other shape in this release), and the project is the name this process is
// configured with, not a value any caller can name.
type identityOrchestratorRecord struct {
	db *rtdb.Client
	id string
}

func (r identityOrchestratorRecord) State(ctx context.Context) (orchestrator.State, error) {
	return orchestrator.ReadState(ctx, r.db, r.id, orchestrator.RoleOrchestrator)
}

// identityPauses is this service's own record of the pauses it wrote
// (credsvc.PausedNodes, D3189): the nodes under runstore.PauseNodeRoot, which
// this service alone writes and which are the only evidence a resume is served
// on.
//
// A node this release cannot read is answered ErrNoPause as the interface says
// — a pause the issuer cannot read is not one it serves a resume of, and the
// ask is refused rather than guessed at — while a node the database could not
// be asked for at all is returned as it stands, because that is this service's
// own failure and not a verdict about the caller (R16's ScopeFault rule).
type identityPauses struct {
	db *rtdb.Client
}

func (p identityPauses) Paused(ctx context.Context, slug, run string) error {
	var raw json.RawMessage
	found, err := p.db.Get(ctx, runstore.PauseNodePath(slug, run), &raw)
	if err != nil {
		return err
	}
	if !found {
		return credsvc.ErrNoPause
	}
	if _, err := runstore.ParsePauseNode(raw); err != nil {
		return credsvc.ErrNoPause
	}
	return nil
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

// credentialsServiceJobAccounts turns FUGARO_JOB_ACCOUNTS into the three things
// NewServer is given that this variable holds: the binding table, one account
// per (repository, workflow), derived from the triple by the one derivation
// the deployer used (credsvc.JobAccountsFor over task.Slug and
// gcp.ServiceAccountID); the repository table, which is those same rows
// read the other way round (credsvc.RepositoryTableFor: the slug a run
// identity carries to the canonical repository name the published policy
// speaks); and the repositories' build class, one row per repository, derived
// from the same per-provider list by the derivation the repository root deploys
// the account with (credsvc.RepoBuildAccountsFor over gcp.BuildServiceAccountID,
// B4.3). The third is the same input read a third way rather than a second
// document: a repository's build exists exactly where its jobs do, so the class
// the build-credentials route admits cannot drift from the repositories this
// deployment holds.
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
func credentialsServiceJobAccounts(raw, fugaroProject, gcpProject string) ([]credsvc.JobAccount, map[string]string, []credsvc.RepoBuildAccount, error) {
	var jobs []struct {
		Provider string `json:"provider"`
		Repo     string `json:"repo"`
		Workflow string `json:"workflow"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&jobs); err != nil {
		return nil, nil, nil, fmt.Errorf("%s is not the installation's job accounts: it must be a JSON array of {\"provider\": ..., \"repo\": ..., \"workflow\": ...} objects, one per job whose service account may call the service. Nothing was started and no credential was issued", envJobAccounts)
	}
	if len(jobs) == 0 {
		return nil, nil, nil, fmt.Errorf("%s names no job, so the service would bind no run to a repository: an empty table refuses every caller, and a service that admits nobody is one an operator would read as healthy while every run halted. Nothing was started and no credential was issued", envJobAccounts)
	}
	byProvider := map[string][]credsvc.RepoJob{}
	for i, j := range jobs {
		if j.Provider == "" || j.Repo == "" || j.Workflow == "" {
			// The index and the field names, not the entry: a triple is the
			// one value in this environment that can hold a name an operator
			// would not want in a log, and the refusal has to be actionable
			// without it.
			return nil, nil, nil, fmt.Errorf("%s: entry %d has no provider, repository or workflow (all three are required: the account's name is derived from the repository and the workflow, and the repository's slug from the provider and the repository)", envJobAccounts, i)
		}
		byProvider[j.Provider] = append(byProvider[j.Provider], credsvc.RepoJob{Repo: j.Repo, Workflow: j.Workflow})
	}
	out := make([]credsvc.JobAccount, 0, len(jobs))
	builds := make([]credsvc.RepoBuildAccount, 0, len(jobs))
	repos := make(map[string]string, len(jobs))
	for _, provider := range slices.Sorted(maps.Keys(byProvider)) {
		accts, err := credsvc.JobAccountsFor(fugaroProject, gcpProject, provider, byProvider[provider])
		if err != nil {
			// Not the derivation's own error: it quotes the repository. What
			// is left is actionable because the entry above has been checked
			// for its three fields, so what can be wrong here is the provider
			// or the repository's shape.
			return nil, nil, nil, fmt.Errorf("%s names a job whose service account cannot be derived: the provider must be a provider kind (lower case, starting with a letter) and the repository a canonical path this build can name, because the account's name is derived from the two rather than configured. Nothing was started and no credential was issued", envJobAccounts)
		}
		table, err := credsvc.RepositoryTableFor(fugaroProject, gcpProject, provider, byProvider[provider])
		if err != nil {
			// Unreachable: the same input has just been derived above. The
			// branch is here rather than a second copy of the table's rule,
			// because a table spelled anywhere but RepositoryTableFor would be
			// a slug resolved one way for a binding and another for a review.
			return nil, nil, nil, fmt.Errorf("%s names a job whose repository cannot be derived: the provider must be a provider kind (lower case, starting with a letter) and the repository a canonical path this build can name. Nothing was started and no credential was issued", envJobAccounts)
		}
		// A repository's build class is derived from the SAME document and the
		// same per-provider list (B4.3, G20): the repositories whose jobs this
		// deployment holds are exactly the repositories whose builds it runs,
		// so the class the build-credentials route admits is the set the
		// deployer created rather than a second list that can drift from it.
		// The derivation's own error quotes the repository, so it is respelled
		// for the same reason JobAccountsFor's is.
		repoBuilds, err := credsvc.RepoBuildAccountsFor(gcpProject, provider, byProvider[provider])
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s names a job whose repository's build service account cannot be derived: the provider must be a provider kind (lower case, starting with a letter) and the repository a canonical path this build can name, because the account's name is derived from the two rather than configured. Nothing was started and no credential was issued", envJobAccounts)
		}
		out = append(out, accts...)
		builds = append(builds, repoBuilds...)
		maps.Copy(repos, table)
	}
	return out, repos, builds, nil
}

// credentialsServiceReviewJobAccounts turns FUGARO_REVIEW_JOB_ACCOUNTS into the
// review-only class's table (C3.9a): one row per review job this installation
// deployed, keyed by the service account the caller is verified as and carrying
// the value of the account axis that call is served by.
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
	// row, and rows that name one account under two values of the account axis
	// are left for credsvc.NewAuthenticator to refuse, which is the deployment that
	// disagrees with itself rather than a document this function can repair.
	slices.SortFunc(out, func(a, b credsvc.ReviewJobAccount) int { return cmp.Compare(a.Email, b.Email) })
	return slices.Compact(out), nil
}

// credentialsServiceSharedJobAccounts turns FUGARO_SHARED_JOB_ACCOUNTS into
// the shared class's table (C3.9's coding half, B5.1): one row per shared
// project job this installation deployed, keyed by the service account the
// caller is verified as, carrying the profile it belongs to and the
// repositories that profile is bound to.
//
// The document is a JSON array of {"profile": ..., "provider": ...,
// "account": ..., "repos": [...]} objects — the review document's own axis,
// which is the axis one shared job per (profile, provider, account) is
// deployed over (config.ProjectJobAccounts over a profile's job profile), plus
// the one member the coding class needs that the review class does not: the
// repositories G21 holds a shared caller's run identity to. They are the
// profile's own allow_repos as the installation published them, and they
// travel here so that the row says what the job was deployed for without a
// read of its own. They are not the binding's verdict, which the pairing takes
// from the project's own answer for the profile (projectpolicy.Admits): a
// profile's list may be wider than the project's, and it is the project's
// enrollment that says a repository may be served. The review class needs no
// such member because it resolves its repository from the run identity's slug
// alone (RepositoryTableFor).
//
// The account may be absent, which is the axis's spelling of the default
// account, and each row's service account is derived from the first three
// fields by credsvc.SharedJobAccountFor — the same derivation the policy read
// makes (credsvc.SharedJobAccountsFor), so there is one spelling of these names
// and not two.
//
// The two rules the class-specific entries are read by hold here
// (`envSharedJobAccounts` states them): an absent or empty value is the EMPTY
// set, the fail-closed statement that this installation admits no shared
// project job — every caller of the class is then refused whole, which is the
// state of an installation whose module does not render the entry yet — while
// a value that is present and is not this document is refused here, because it
// is a deployment that is wrong and would otherwise be read as a service that
// admits nobody. An empty array is the empty set and not a refusal, for the
// review table's reason: an installation that deploys no shared job still
// starts and refuses the class.
//
// An entry's `repos` must be PRESENT — an empty array is the deny-all profile
// and not a refusal, and a missing one is a document nobody could have written
// on purpose, refused by name rather than read as the same thing.
//
// A REFUSAL NAMES A VARIABLE AND AN ENTRY, NEVER A VALUE.
func credentialsServiceSharedJobAccounts(raw, gcpProject string) ([]credsvc.SharedJobAccount, error) {
	if raw == "" {
		return nil, nil
	}
	var rows []struct {
		Profile  string    `json:"profile"`
		Provider string    `json:"provider"`
		Account  string    `json:"account"`
		Repos    *[]string `json:"repos"`
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("%s is not the installation's shared project job accounts: it must be a JSON array of {\"profile\": ..., \"provider\": ..., \"account\": ..., \"repos\": [...]} objects, one per shared project job this installation deployed. Nothing was started and no credential was issued", envSharedJobAccounts)
	}
	out := make([]credsvc.SharedJobAccount, 0, len(rows))
	for i, r := range rows {
		if r.Profile == "" || r.Provider == "" {
			// The index and the field names, not the entry, by the review
			// table's own rule: the row is a value of this environment, and
			// the refusal has to be actionable without it.
			return nil, fmt.Errorf("%s: entry %d has no profile or no provider (both are required: the shared job's service account is derived from the profile, the provider and the account, and a row without them names an account nobody deployed)", envSharedJobAccounts, i)
		}
		if !task.ValidProvider(r.Provider) {
			return nil, fmt.Errorf("%s: entry %d names a provider that is not a provider kind (lower case, starting with a letter), so the shared job's service account cannot be derived from it and would be one this installation cannot have deployed", envSharedJobAccounts, i)
		}
		if r.Repos == nil {
			return nil, fmt.Errorf("%s: entry %d names no repositories at all (the member is required, and an empty array is how a profile bound to none is written): a shared caller is served for the identity's repository only when the profile admits it, so a row read as deny-all is a job this service refuses every call of", envSharedJobAccounts, i)
		}
		for _, repo := range *r.Repos {
			if repo == "" {
				return nil, fmt.Errorf("%s: entry %d has an empty entry in repos (every entry is one canonical \"<provider>/<owner>/<name>\" repository, and an empty one admits nothing)", envSharedJobAccounts, i)
			}
		}
		out = append(out, credsvc.SharedJobAccountFor(r.Profile, r.Provider, r.Account, *r.Repos, gcpProject))
	}
	// The same order the policy read makes (credsvc.SharedJobAccountsFor), and
	// the repeats are NOT dropped here, which is the one difference from the
	// review table: a row carries its profile and its repositories, so two rows
	// that name one account are either the same row twice — which
	// credsvc.NewAuthenticator and NewServer both read as one — or a deployment
	// that binds one account to two profiles, which both refuse by name. A
	// document this function collapsed on the email alone would hide the second
	// from them.
	slices.SortFunc(out, func(a, b credsvc.SharedJobAccount) int { return cmp.Compare(a.Email, b.Email) })
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
	return serveUntilStopAs(ctx, ln, srv, errw, "credentials service")
}

// serveUntilStopAs is serveUntilStop with the process's own name in the lines
// it writes, for the second process that serves over the same host shape (the
// intake, intake_serve.go): the two revisions' logs are one stream in Cloud
// Logging, and a line that called both of them the credentials service would
// be an operator reading the wrong process.
func serveUntilStopAs(ctx context.Context, ln net.Listener, srv *http.Server, errw io.Writer, name string) error {
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	fmt.Fprintf(errw, "%s: listening on %s\n", name, ln.Addr())
	select {
	case err := <-done:
		// Serve returned on its own: the listener failed or was closed under
		// it. Either way the service is not serving, and the process must not
		// pretend otherwise.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return remote(fmt.Errorf("the %s stopped: %w", name, err))
		}
		return nil
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), serveShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		fmt.Fprintf(errw, "%s: shutdown did not drain every call in %s: %v\n", name, serveShutdownGrace, err)
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
