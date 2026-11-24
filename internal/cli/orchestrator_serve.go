// orchestrator_serve.go is `fugaro orchestrator serve`: the resident
// orchestrator (mobile plan task P5.1c; the design is
// docs/mobile/design/always-on-orchestrator.md, section 4.1), and the third
// hidden in-container process of this binary, beside the credentials
// service's (credentials_serve.go) and the intake's (intake_serve.go).
//
// WHY IT IS A HIDDEN SUBCOMMAND OF THIS BINARY and not a main of its own is
// the intake's own reason, unchanged: this repository builds one image and one
// binary per process, the Cloud Run service sets neither a command nor args, so
// the image's entrypoint is what runs and the Dockerfile's CMD is what names
// the subcommand — which is also why internal/infra/tf's cover case for
// google_cloud_run_v2_service refuses a plan whose container sets either. The
// service is hidden twice over, group and subcommand, and it takes no flag at
// all: Cloud Run gives the container the port it must listen on, and every
// other fact of the deployment is in its environment.
//
// WHAT THIS PROCESS IS, IN ONE PARAGRAPH. It is the resident half of Phase 2 —
// the process that lives in the installation's own project, is woken by the
// installation's Cloud Scheduler tick, and is what a phone's messages reach
// when no laptop is awake. It is kept to the least privilege section 4.3
// grants the `orch` account: it holds no model credential and no git
// credential, it reads no secret value, it changes no IAM, and the one database
// it can reach is this installation's own (never a second installation's
// nodes). The rules themselves are internal/orchestrator/never.go — section 6's
// eleven as refusals the code answers with — and this file is where two of them
// meet a real deployment fact at start-up: the installation it is told to serve
// is never `belong` (rule 6, F31), which is held to both names the deployment
// is made of, its own project and the Firebase project its database belongs to;
// and the database URL and that Firebase project must name one installation
// (rule 10).
//
// WHAT A PASS DOES. The tick's pass reads the node the previous pass left — the
// state document, and the lease node beside it — from the store and never from
// memory, and then reads the plan that record derives: the attempts it cannot
// see a start for (P5.1d's ResidentRequests) and the actions that are a write's
// — a first launch, a retry, an abandon. It writes no node of its own: a node's
// writes stay with `fugaro orchestrator patch` and the owner's own credential
// (D1223, D1241). What it does with the re-issues is start them, through the
// command's own launch (the paragraph below): each one is one job execution
// under the run id the node already recorded, and the pass reports how many it
// carried, how many it refused and how many steps it left. What the tick buys is
// what the owner-run Check 660 measures: the service exists, its autoscaling
// floor is 0, the scheduler job that wakes it is enabled, and a forced tick
// reads the store and starts nothing the node's own record did not already hold.
//
// THE SAME ONE TICK IS THE MAILBOX HALF (P5.1f). The pass that reads this
// installation's node also consumes its mailboxes: internal/orchestrator's
// Consume — the phase-1 consumer of `fugaro mailbox` (P4.1) run by this process
// instead of by a laptop, one pass per paired phone — takes or renews the lease
// on each phone's heartbeat, reads its inbox, verifies every record with the
// pairing's key, and answers the records it accepted into the outbox
// (consume.go's own header is the whole of it; the reader, the tag and the
// create-only write are the module's, so this is one pass in two places rather
// than two implementations of one). It carries nothing: the acting half is
// P5.1g's, so an accepted message is answered with this file's own sentence and
// no work is started for it — which is also why no ledger stands under the pass
// (an instance's /tmp is not a fence a tick that may land elsewhere can rest on,
// and the rules grant CONSUMER exactly two writes).
//
// THE CLAIM IT WRITES UNDER IS `mc`, and that is the whole difference between
// this consumer and the laptop's. `fugaro mailbox` writes as the operator's own
// admin credential, which bypasses the rules (D113b says so out loud); this
// process carries a service account, which the rules never fence either, so a
// write of those two nodes is admitted only under the rules' CONSUMER macro
// (`auth.token.mc == $mb`) — a claim internal/budget/token's MintConsumer mints
// and the process exchanges for an ID token — which makes this the first
// mailbox writer the rules can hold to "the outbox and the heartbeat and
// nothing else" (the design's section 4.3). The holder it takes the lease under
// is the installation's own orchestrator account and NOT a per-instance boot id,
// and the reason is the lease's own arithmetic: a holder that changed with every
// instance would renew nothing (Renew answers lost when the claim it reads names
// somebody else) and its take of that claim is refused to everyone while it is
// fresh, so a mailbox would go unconsumed for up to the lease TTL after every
// instance change — which for a service whose whole point is being woken is
// every tick.
//
// THE TWO HALVES OF THAT PASS ARE TWO IDENTITIES, AND NO ONE CLIENT IS BOTH.
// The `mc` claim is what the rules fence the WRITE with, and CONSUMER grants no
// read at all — "in particular it may read no mailbox, its own or another's" —
// so the inbox, the heartbeat and the outbox this pass reads are read by the
// deployment's own service-account client (the `rtdb.New(rawURL,
// rtdb.Auth{Source: ts})` below), which the rules never see, while the two
// writes go through a client built with the ID token the minted one is exchanged
// into. That is why orchestrator's Consume takes a ConsumerReader and, per
// mailbox, a ConsumerWriter (a `*rtdb.Client` satisfies the first and cannot
// satisfy the second): handing this client to both halves would be a consumer
// whose every write bypasses CONSUMER, which is the unfenced writer D113b
// describes and the thing the mailbox half exists to replace. What holds across
// the two is the ETag: it is the database's answer about the value at a
// location, so the ETag this process reads under IAM is the one the `mc` write
// presents as If-Match.
//
// WHAT THIS DEPLOYMENT STILL LACKS TO SERVE ONE, named rather than left to a
// tick that refuses: the pairing keys (the container is the module's, the grant
// is the secret row P5.1e added and the reader is keychain.Cloud.Keys, but this
// service builds none of it and no name in its environment points at the
// container); the Firebase web API key, which the minted custom token must be
// exchanged with before it is an identity the rules know, and which is
// therefore what keeps a mailbox's writer unbuildable — the service's own
// environment does not carry it (orchestrator.tf's six names are three of the
// node's and three of the launch's, and
// TestOrchestratorServiceEnvIsTheDeploymentsOwn holds that list in both
// directions); and the database read the inbox itself needs, which is an IAM
// read from an account section 4.3's table grants no firebasedatabase role —
// the P5.1c review's finding A, still open, and it fences the read half of this
// pass exactly as it fences the node read. So the consumer the tick is given is
// nil on this deployment, and orchestratorNoConsumer below is the line it says
// so in, once per tick, naming the missing half rather than reporting a mailbox
// pass that read nothing as if there had been nothing to read. A deployment that
// has those three facts wires one through orchestratorConfig, which is where
// every test of this file drives the mailbox half from.
//
// AND WHAT IT READS IS AN UPPER BOUND, NOT A LIST OF LAUNCHES OWED. The plan is
// read with an empty RunView (carry): this process reads no run records to
// judge "did this run start", so an attempt past its ten-minute ClaimTTL window
// is read as one it cannot see started — and a run that is simply longer than
// that window is one of them, because ClaimTTL is all this process has to tell
// "in flight" from "was never seen to go". So the field is named for the READING
// (Unreported) and not for a request the node is making, and no line is written
// per attempt (a warning once per tick per long run is a line nobody can act
// on). Acting on one is safe for a reason of its own, and it is the launcher's:
// the SAME run id and never a second one, and the command's launch reads the
// run's own launch record before it starts anything, so a run that already went
// is answered with the execution that record names rather than started again
// (existingLaunch). A caller that wants to know whether a run went asks
// `fugaro runs ls`.
//
// THE CALLER IS FENCED BY CLOUD RUN'S OWN IAM AND NOT BY A CHECK HERE. The
// service is deployed with invoker_iam_disabled = false and one binding —
// roles/run.invoker for the installation's scheduler service account — so a
// caller that is not that account is refused by the platform before this
// process sees the request. That is deliberately not repeated as an in-process
// class check the way the credentials service and the intake do it: those two
// grant invoker to a CLASS of callers (a run's job account, a launcher's
// person), so the platform's grant is wide and the process's check is the
// narrow half. Nothing in this tree verifies a Cloud Scheduler
// service-account caller (credsvc.NewLaunchers refuses a *.gserviceaccount.com
// address by design, and a second service-account class is a decision rather
// than a line of this task), and a second fence around a route that acts on
// nothing yet would be surface without a thing to protect. The task whose
// consumer half makes the route act — P5.1f — is where that decision belongs.
//
// The environment is six names, and orchestratorServiceConfig below is the one
// place they are read: FUGARO_PROJECT, FUGARO_FIREBASE_PROJECT and
// FUGARO_RTDB_URL, which are the node this process serves and the database it
// reads it out of, and FUGARO_GCP_PROJECT, FUGARO_REGION and FUGARO_BUCKET,
// which are the launch below. An installation whose deployment names one of them
// twice, or names a seventh, is the module's business (orchestrator.tf): nothing
// here reads a variable it does not need, and nothing here prints one.
//
// THE LAUNCH IS `fugaro run`'S ONE PATH, BUILT THE WAY A CONTAINER CAN BUILD IT.
// Both launchers this tree has live in the same file, and they are two bindings
// of ONE path rather than two paths (F11, D931, T2): the claim, the mint and the
// job execution are the command's own steps in each, `fugaro run --retry`'s
// reader of a stored task included, and what the daemon's launcher adds — the
// spec and the task it builds for an item, and the command's gate block — is
// exactly what a container has no hand in (orchestrator_launcher.go's
// residentLauncher names the two halves and what skipping the gates can cost,
// and its header is the argument for it). What this deployment builds over that
// path is the half a container can have: a
// *cloudEnv built from the three facts a run's own container already carries
// (FUGARO_BUCKET, FUGARO_GCP_PROJECT and FUGARO_REGION, which internal/cli's
// exec.go and imagecheck.go read inside a run; internal/infra's BucketEnv,
// GCPProjectEnv and RegionEnv are those three names), and residentLauncher,
// which reads the attempt's stored task with the command's own `--retry` reader
// and starts it with the command's own launchRun — the claim, the mint and the
// job execution the design names for this half (orchestrator_launcher.go's
// residentLauncher is the argument for what it does not run). The module's list
// is held to this process's reads in both directions by
// TestOrchestratorServiceEnvIsTheDeploymentsOwn and by internal/infra/tf's
// orchestratorServiceEnv allow-list, which refuses a container setting a name
// outside it.
//
// WHAT THE LAUNCH NEEDS THAT THIS DEPLOYMENT STILL DOES NOT HAVE, named rather
// than left to a tick that refuses: the credentials service's read of a
// repository's document at a ref, which is how the job of a project that
// published profiles is decided — the orch account never calls that service
// (Q13, section 4.3), so a re-issue of such a repository is refused every tick
// until a person launches it; the operator's own `claude_accounts`, which is
// where a stored task's named account is judged — a container is told six names
// and holds no project file, so the walk over a task naming a NAMED Claude
// account (the `default` account, the plain job and an axis non-account value
// are all answered without that key) comes out allowed for `default` alone and
// refuses it, every tick, with the command's own sentence
// (orchestrator_launcher.go's residentLauncher says what a person does about it,
// and TestResidentLauncherRefusesANamedAccountThisDeploymentCannotSee pins the
// behaviour); and every read of the database this process makes, which is an IAM
// read the account holds no permission for yet (the P5.1c review's finding A,
// orchestratorTest below). All three are the deployment's, and all three are
// reported with this task rather than papered over: every one of them is a
// refusal, written to the log as such, and none of them starts the wrong run.
//
// THE CONSUMER'S THREE ARE NOT IN THAT LIST, and deliberately: this process
// serves a tick with a nil consumer rather than one that refuses (the `mc`
// paragraph above has the whole argument), so there is no refusal here to name —
// the line is orchestratorNoConsumer, and the three facts a deployment would
// need to serve a mailbox are the pairing key container, the Firebase web API
// key and a database read this account may make.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// newOrchestratorServeCmd is `fugaro orchestrator serve`. Like the two other
// in-container entry points it has no flags at all, not even a --port: the
// whole configuration is the platform's, and a flag would be a second way to
// start a process whose deployment is the environment the module renders.
func newOrchestratorServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the resident orchestrator in its image (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Cloud Run stops a revision by sending SIGTERM and killing the
			// container seconds later, so the signal is a request to stop
			// serving rather than to die: the context below is what the host
			// watches, and it drains the calls in flight before it returns
			// (hostOrchestratorService). A pass already reading the node lands
			// or is abandoned; nothing is left half-written, because nothing in
			// this process writes.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveOrchestrator(ctx, cmd, os.Getenv)
		},
	}
}

// serveOrchestrator starts the resident orchestrator.
//
// The order is serveCredentials' and serveIntake's own, and for their reason:
// first the http2debug refusal, which every command of this repository's that
// opens or handles a credential makes (refuseHTTP2Debug: GODEBUG=http2debug
// makes Go print HTTP requests, headers included, to stderr), then the
// environment the module sets, and only then the service. A deployment that is
// wrong is told so by name rather than by a failure to start for a reason the
// operator cannot see; getenv is a parameter rather than os.Getenv so the
// refusals can be tested without a process environment.
func serveOrchestrator(ctx context.Context, cmd *cobra.Command, getenv func(string) string) error {
	if err := refuseHTTP2Debug(getenv); err != nil {
		return err
	}
	cfg, err := orchestratorServiceConfig(ctx, getenv, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	return hostOrchestratorService(ctx, cfg, getenv, cmd.ErrOrStderr())
}

// orchestratorServiceEnvNames is the whole of what this process reads from its
// environment. TestOrchestratorServiceEnvIsTheDeploymentsOwn holds it to
// orchestrator.tf's own env blocks in both directions, so a name added on one
// side and not the other fails rather than drifts: the direction that matters
// is the module setting a name this command does not read, which is a fact an
// operator would believe was in effect.
var orchestratorServiceEnvNames = []string{
	envProject, envFirebaseFP, envRTDBURL,
	envGCPProject, envRegion, envBucket,
}

// envBucket is FUGARO_BUCKET: the runs bucket's own URL, and the name a run's
// container, the check jobs and the catalog job all carry it under
// (internal/infra's BucketEnv, which is where the name comes from). It is not
// FUGARO_RUNS_BUCKET, which the history job carries because that job is given
// the bucket's name and not its URL: what this process opens is a bucket
// (blobx.Open, through the config's BucketURL), and the URL form is the one
// every container of a run already reads it under.
const envBucket = "FUGARO_BUCKET"

// orchestratorWiring points the configuration builder at stand-ins for the two
// things it reaches Google for as the process's own identity: the token source
// the installation's database is read with, and the environment a launch starts
// through. It is a test seam for credentialsWiring's reason exactly — without
// one, building the configuration would need Application Default Credentials
// and a live metadata server, and the test that matters most, that a full
// environment builds a service this file serves with, could not be written. It
// is never set outside a test, and it carries no value: both are constructors.
type orchestratorWiring struct {
	tokens func(ctx context.Context) (oauth2.TokenSource, error)
	// open is the launcher's environment: the deployment's facts as a
	// *cloudEnv, one per launch (residentLauncher's own reason). A test that
	// serves a tick and expects a run started replaces it, because the real one
	// opens the runs bucket and the backend of whatever installation the test
	// names.
	open func(ctx context.Context) (*cloudEnv, error)
}

// orchestratorTest is nil in production: the function above is then
// google.DefaultTokenSource over the database scopes, as the revision's own
// service account. This is an IAM read and not a rules read (rtdb sends a
// source as a Bearer header, and an IAM read bypasses the rules entirely), and
// Realtime Database IAM has no path scope, so it needs
// firebasedatabase.instances.get. Section 4.3's table has no row granting this
// account that permission and orchestrator.tf binds it to none, so on a real
// installation this read is refused: that is the P5.1c review's finding A, and
// the row is the design's to add rather than a line to slip into a fix.
var orchestratorTest *orchestratorWiring

// orchestratorConfig is one deployment's facts, as the builder below reads
// them: the orchestrator id this revision serves, the store it reads the node
// through, the launcher a re-issue is carried out by, the mailbox consumer the
// tick's other half runs as, and the log its lines go to.
//
// The launcher is orchestrator_launcher.go's residentLauncher and never a
// launcher of this file's making: a route here that took a claim, minted a token
// and called jobs.run of its own would be the second launch path F11, D931 and
// T2 rule out, and it would be one no gate and no claim discipline of the
// command's own had ever been compared against. What is built here is that
// launcher's environment — the deployment's facts as a *cloudEnv — which is the
// half a container has to supply.
type orchestratorConfig struct {
	id     string
	db     orchestratorStore
	launch orchestrator.Launcher
	// consumer is the tick's mailbox half (P5.1f), and it is the ONE optional
	// field here: the node half is the service's reason to exist and a
	// deployment without it serves nothing, while a mailbox half stands on three
	// facts the module does not put on this service yet (the header names them).
	// A nil consumer is therefore a real deployment and not a broken
	// configuration, and the tick says so in one line rather than consuming an
	// inbox it cannot reach (orchestratorNoConsumer).
	consumer *orchestratorConsumer
	log      *slog.Logger
}

// orchestratorConsumer is the mailbox half of a tick as a deployment holds it:
// the identity the lease is taken under, the pairings this installation has a
// key for, and the reader every read of the pass is made through.
//
// The reader is a ConsumerReader and not this file's orchestratorStore, because
// the half reads and only reads: the inbox, the ETag a compare-and-swap needs
// on the heartbeat and on the outbox, and the database's clock — the same
// *rtdb.Client in production (the one the builder below opens), declared as
// what the pass uses so that a test standing in for the database cannot hand
// the consumer a store wider than its own work. **The writes are not here at
// all**: each mailbox carries its own ConsumerWriter, built from the exchanged
// `mc` token of that phone's pairing, because a write made with this client
// would be a write the rules never fence (the header's paragraph). This
// deployment builds no writer, which is one of the facts that leave the
// consumer nil.
type orchestratorConsumer struct {
	holder    string
	mailboxes []orchestrator.ConsumerMailbox
	reader    orchestrator.ConsumerReader
}

// orchestratorConsumerHolder is the name this service holds a mailbox lease
// under: the installation's own orchestrator account, which is the identity that
// really writes these nodes.
//
// It is deliberately NOT a per-instance boot id, and the lease's own arithmetic
// is why: Renew answers lost when the claim it reads names another holder, and a
// take of a claim that is still fresh is refused to everyone (mailbox.LeaseTaken
// on a contender, AlreadyClaimed on a fresh one), so a holder that changed with
// every Cloud Run instance would leave every phone's mailbox unconsumed for up to
// ClaimTTL after each instance change — which, for a service the scheduler wakes,
// is every tick. The laptop's consumer is a daemon and may use its boot id; this
// one is the installation.
const orchestratorConsumerHolder = infra.OrchestratorAccountID

// orchestratorNoConsumer is the line a tick says when this deployment was wired
// with no mailbox consumer at all, and it names the three facts the deployment
// would need (the header's paragraph).
//
// It is said per tick rather than once at start-up because it is a fact about
// the tick: the numbers a pass reports — no mailbox read, no lease taken, no
// answer written — are the same numbers an empty inbox reports, so without this
// line an operator cannot tell "this phone had nothing to say" from "nothing on
// this deployment reads a phone at all", which is the same distinction carry
// makes its switch visible for.
const orchestratorNoConsumer = "tick consumed no mailbox: this deployment has no mailbox consumer (no pairing key container read, no Firebase web API key in its environment, and no database read the account may make), so a phone's message is read by no one here"

// orchestratorServiceConfig builds the resident orchestrator's configuration
// out of the deployment's environment, and it is the whole of this task's
// wiring.
//
// WHAT IT READS, AND WHY EACH NAME IS THERE. The set is three, and every one of
// them is a fact of the installation that cannot be derived from the others,
// and none of them is a credential:
//
//   - FUGARO_PROJECT is the Fugaro project this revision serves: the `<id>` of
//     the node `orchestrator/<id>/{state,lease,audit}` it reads, and the name
//     `fugaro ls` and the phone's messages call the installation. It is held to
//     PathState's own rule — a name this release cannot address is a node no
//     reader could open — and to section 6's rule 6 by name: `belong` is the
//     owner's own installation and the resident orchestrator is never it (F31).
//   - FUGARO_FIREBASE_PROJECT is the installation's Firebase project, which is
//     the project the realtime database belongs to. The database is named after
//     it (<project>-default-rtdb), so the two names must be one installation's —
//     and this one is held to rule 6 as well, because the database it names is
//     the installation's own: a deployment whose GCP project is outside the
//     `belong` family and whose database is inside it is still serving the
//     owner's own installation.
//   - FUGARO_RTDB_URL is the database itself, which is the only store this
//     process reads, and the only reach it has beyond Google's own IAM.
//
// THE LAUNCH'S THREE, and each is a fact of the installation the node's own
// three cannot answer (P5.1d; the names are internal/infra's BucketEnv,
// GCPProjectEnv and RegionEnv, which a run's container carries and which
// internal/cli's exec.go and imagecheck.go read inside one):
//
//   - FUGARO_GCP_PROJECT is the project the jobs are in, which is not always the
//     Firebase project the database belongs to (they are one only on a
//     same-project installation), and FUGARO_REGION is where the executions are
//     started. Together they are the backend: a launch that guessed either would
//     start nothing, or start it in another region's jobs.
//   - FUGARO_BUCKET is the runs bucket's URL — the claim, the task, the launch
//     record and the sealed budget token all live in it, and the launcher's own
//     grant is the bucket's (runs/, tokens/ and issues/ written, the whole
//     bucket read).
//
// WHAT IT DOES NOT READ is as much of the design as what it does. No URL of its
// own, which is the one place it differs from the other two processes: they
// verify their callers' Google ID tokens against their own URL as the audience,
// and this one is fenced by Cloud Run's IAM rather than by a check here (above),
// so the URL it is served on is the module's business and not this process's —
// a name nothing reads is a name an operator would believe was in effect. No
// repositories, no credentials service and no profiles: this process launches
// runs it is told the run id of and reads no repository's configuration of its
// own (the header's paragraph on what the launch still cannot do). And no
// budget.signature key of its own: the run's token is signed by the Firebase
// root's own signer account, whose name is derived from the Firebase project
// below rather than named a seventh time — internal/infra's SignerAccountID is
// the one spelling of it, and CheckFirebaseOutputsFor refuses any other.
//
// A REFUSAL NAMES A VARIABLE AND NEVER A VALUE. This process's log is Cloud
// Logging and the environment is the deployment's, so every message below names
// the variable and the shape it must have, never what it held (the no-echo
// rule, credentialsServiceConfig's own).
func orchestratorServiceConfig(ctx context.Context, getenv func(string) string, errw io.Writer) (orchestratorConfig, error) {
	id := getenv(envProject)
	if id == "" {
		return orchestratorConfig{}, userErr("the resident orchestrator needs %s in its environment (the installation module sets it on the service, and it is the project whose node this revision keeps: there is no flag, no config file and no default)", envProject)
	}
	if _, err := orchestrator.PathState(id); err != nil {
		return orchestratorConfig{}, userErr("%s is not a project this release can address: the node is orchestrator/<project>, whose key and paths are built from the name, so a deployment whose project is not one this release can address would read a node nothing else can open", envProject)
	}
	if err := orchestrator.NeverBelong(id); err != nil {
		return orchestratorConfig{}, userErr("the resident orchestrator will not serve this installation: %v", err)
	}
	fb := getenv(envFirebaseFP)
	if fb == "" {
		return orchestratorConfig{}, userErr("the resident orchestrator needs %s in its environment: it is the installation's Firebase project, which the database it reads belongs to, and a process that does not know which installation it is serving acts on no node at all", envFirebaseFP)
	}
	// Rule 6 is about the INSTALLATION and not about one of its names, so it is
	// held to both names this deployment is made of: the owner's own database
	// is whatever the installation's own project is called, and the database
	// URL below is named after the Firebase project (rule 10's check reads the
	// two back to each other), so the one name here covers the URL as well. A
	// deployment that named only its GCP project outside the rule and its
	// database inside it would otherwise read and act on the owner's own node —
	// the thing F31 fences. The names the rule is judged on are the owner's
	// installation's own and not only the word: `fugaro-belong` is the Firebase
	// project it reads and `edge-devel-dimi` the GCP project its resources are
	// in, and never.go refuses both.
	if err := orchestrator.NeverBelong(fb); err != nil {
		return orchestratorConfig{}, userErr("the resident orchestrator will not serve this installation: %v", err)
	}
	rawURL := getenv(envRTDBURL)
	if rawURL == "" {
		return orchestratorConfig{}, userErr("the resident orchestrator needs %s in its environment: the state node it reads lives in the installation's realtime database, and there is no other store this process may reach", envRTDBURL)
	}
	// The database client's own rule (rtdb.ValidateURL), with this command's
	// refusal: the client's error quotes the URL it was handed, and this
	// process's log is Cloud Logging (the no-echo rule).
	if err := rtdb.ValidateURL(rawURL, false); err != nil {
		return orchestratorConfig{}, userErr("%s is not a realtime database URL: it must be https://<database>.firebaseio.com or https://<database>.<region>.firebasedatabase.app, with no credentials, path, query or fragment", envRTDBURL)
	}
	// Section 6's rule 10, at the one place this deployment can get it wrong:
	// the database belongs to the installation's own Firebase project, and one
	// service account serves one installation's nodes. A process pointed at a
	// copied environment would otherwise read another installation's state and
	// act on its work items. The comparison is checkDatabaseHost's, which is
	// where history.go, the history job and the credentials service already
	// hold the same pair together; internal/orchestrator's NeverForeignInstallation
	// is the same rule as a typed refusal for the paths that reach it by value
	// (the launcher and the consumer), and not a second spelling of this check.
	if err := checkDatabaseHost(rawURL, fb); err != nil {
		return orchestratorConfig{}, userErr("%s and %s name different installations: the database is <Firebase project>-default-rtdb, so the URL must be the database of the Firebase project this revision serves, and the resident orchestrator is never a second installation's nodes (section 6, rule 10). Nothing was started", envRTDBURL, envFirebaseFP)
	}

	// The launch's three, and each is read and refused on its own: a
	// deployment that is missing one is told which, in this file's own words,
	// rather than failing at the first tick's launch with a message about a
	// resource this process never named.
	gcpProject := getenv(envGCPProject)
	if gcpProject == "" {
		return orchestratorConfig{}, userErr("the resident orchestrator needs %s in its environment: it is the GCP project the installation's jobs are in — the one a run's own container carries it under — and it is where this process starts a re-issued attempt: there is no flag, no config file and no default", envGCPProject)
	}
	// Held to the shape a project config's own gcp_project is held to
	// (localcfg's projectRE, the same regexp as infra.ValidProjectID): every
	// job, service account and image this launch names is derived from it
	// (`<account-id>@<gcp project>.iam.gserviceaccount.com`), so a value that
	// is no project ID is a launch that would build an address no installation
	// has.
	if !infra.ValidProjectID(gcpProject) {
		return orchestratorConfig{}, userErr("%s is not a GCP project ID, and every job, service account and image a launch names is derived from it: it must be 6 to 30 characters of lower-case letters, digits and hyphens, starting with a letter and ending with a letter or a digit", envGCPProject)
	}
	region := getenv(envRegion)
	if region == "" {
		return orchestratorConfig{}, userErr("the resident orchestrator needs %s in its environment: it is the region the installation's jobs are in (var.region of the installation root, which every job of this installation was deployed to), and a launch that guessed one would start nothing or start it where the job is not", envRegion)
	}
	// The runs bucket, held to the shape the repository's check job holds its
	// own to (jobEnv, imagecheck.go): a gs:// URL with a bucket and no path,
	// because it is what this process opens as a bucket (blobx.Open, through
	// the config's BucketURL below) and not an object of one. A value the
	// client cannot open is refused here rather than at the first tick, and
	// the refusal names the variable rather than the value (the no-echo rule
	// below) — the bucket's own name is not a secret, but this file does not
	// print what it was handed to the log.
	rawBucket := getenv(envBucket)
	u, err := url.Parse(rawBucket)
	if rawBucket == "" || err != nil || u.Scheme != "gs" || u.Host == "" || strings.Trim(u.Path, "/") != "" {
		return orchestratorConfig{}, userErr("%s must be the runs bucket's own URL, gs://<bucket> with no path, query or fragment: it is the bucket the claim, the task, the launch record and the run's sealed budget token are written to, and the whole of this process's reach to a launch beyond the backend", envBucket)
	}

	tokens := orchestratorProductionTokens
	if orchestratorTest != nil && orchestratorTest.tokens != nil {
		tokens = orchestratorTest.tokens
	}
	ts, err := tokens(ctx)
	if err != nil {
		return orchestratorConfig{}, remote(fmt.Errorf("no credentials for the installation's database: %w", err))
	}
	// One client, and it is the READ half of everything below: the node's two
	// reads and — when this deployment has a consumer — the consumer's inbox,
	// heartbeat and outbox reads, all over this revision's own service account
	// and therefore past the rules. A mailbox's WRITES are not this client's and
	// must never be: a service-account bearer is invisible to CONSUMER, so the
	// two nodes it wrote would be written unfenced (the header's paragraph on
	// the two identities, and orchestrator.ConsumerWriter).
	db, err := rtdb.New(rawURL, rtdb.Auth{Source: ts})
	if err != nil {
		// Unreachable past ValidateURL, and deliberately not reported with the
		// client's own error: that one quotes the URL.
		return orchestratorConfig{}, userErr("%s: the database client could not be built (it must be https, and reachable as this revision's own service account)", envRTDBURL)
	}
	// The launch's environment, as the config a launch reads its facts out of
	// (`fugaro run`'s own steps take a *cloudEnv, and openCloudFor builds one
	// from a config). The fields are the deployment's and nothing else's: no
	// local file, no checkout, no repository entry.
	//
	// The budget block is the one place this file states a fact the deployment
	// did not hand it, and the mode is deliberately the strict one. What the
	// mode decides HERE is one thing only: whether the launcher mints the run
	// its budget identity (budgetBackendOn: a mode that is not off with a
	// database configured). A resident launch must mint, and this process
	// cannot see the published policy to prove a run does not need one — a
	// config it built with the mode off would send it to read that policy over
	// an IAM read this account holds no permission for (finding A) and refuse
	// every launch. What the mode does NOT decide here is the run's own guard:
	// the mode the run enforces is the one its job was deployed with and the
	// one the published policy carries (internal/runner's Spend, merged from
	// the environment and the policy node), and the launcher's local mode
	// reaches no execution — so the draft the token's claims are minted under
	// this mode is exactly the draft a person's `fugaro run` mints under a
	// project whose mode is on.
	//
	// The ceilings are zero and are not a claim about the installation: they
	// are the gate block's (freshLaunchGates), which a resident launch does not
	// run (orchestrator_launcher.go's residentLauncher says why), and a
	// figure written here would be one no reader of this launch has. The
	// signer's name is derived rather than named a seventh time: it is the one
	// internal/infra's own check holds a Firebase root's output to
	// (CheckFirebaseOutputsFor: `fugaro-token-signer@<firebase project>`).
	base := &localcfg.Config{
		Version:    1,
		Name:       id,
		GCPProject: gcpProject,
		Region:     region,
		RunsBucket: u.Host,
		Bucket:     rawBucket,
		Budget: &localcfg.Budget{
			Mode:            localcfg.BudgetEnforce,
			FirebaseProject: fb,
			RTDBURL:         rawURL,
			TokenSigner:     fmt.Sprintf("%s@%s.iam.gserviceaccount.com", infra.SignerAccountID, fb),
		},
	}
	open := func(ctx context.Context) (*cloudEnv, error) {
		// A config per launch, and a budget block per launch inside it: a
		// launch writes the run's identity onto the config it starts through
		// (residentLauncher.Launch sets User), and a service Cloud Run may run
		// twice at once must not have two launches writing one config. The
		// copy is shallow on purpose and never deeper than this: the two levels
		// are the ones a launch mutates, and nothing else in the config is
		// written after it is built.
		c := *base
		b := *base.Budget
		c.Budget = &b
		return openCloudFor(ctx, &c)
	}
	if orchestratorTest != nil && orchestratorTest.open != nil {
		open = orchestratorTest.open
	}
	launch := &residentLauncher{open: open}
	// No consumer, and it is the environment that says so rather than this
	// line's taste: none of the six names is a pairing key, a Firebase web API
	// key or anything a database read could be taken with, so there is nothing
	// here to build one out of (the header names the three facts). The field is
	// left at its zero value and the tick reports it — a builder that filled it
	// with a consumer reading a key nobody loaded would be the same deployment
	// with a line that could not say why nothing was read.
	return orchestratorConfig{id: id, db: db, launch: launch, log: runner.NewLogger(errw)}, nil
}

// orchestratorProductionTokens is the one thing the builder needs that reaches
// Google as the service itself. It is a function rather than a value because a
// refusal before it must not have built a client.
func orchestratorProductionTokens(ctx context.Context) (oauth2.TokenSource, error) {
	// The same two scopes the history job and the credentials service read the
	// database with (budgetScopes): the same database, the same read.
	return google.DefaultTokenSource(ctx, budgetScopes...)
}

// hostOrchestratorService runs the resident orchestrator on the port the
// platform gives it (PORT) until ctx is cancelled, then drains the calls in
// flight.
//
// It is the other two processes' own shape: the listener first (a port already
// taken has to fail at start-up, not answer the platform's health check on a
// socket this process does not own), the same http.Server
// (credentialsHTTPServer), and the same drain. All three are one kind of thing
// — a Cloud Run service behind the same front end — so their transport bounds
// and the protocols they speak (credentialsServeProtocols' HTTP/1.1 and h2c)
// are facts about that front end rather than about what each one serves.
func hostOrchestratorService(ctx context.Context, cfg orchestratorConfig, getenv func(string) string, errw io.Writer) error {
	srv, err := newResidentOrchestrator(cfg)
	if err != nil {
		// newResidentOrchestrator names the missing piece; it is this process's
		// deployment that is wrong, so it is a user error and not a remote one.
		return userErr("the resident orchestrator was not started: %v", err)
	}
	port, err := servePort(getenv)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return userErr("the resident orchestrator cannot listen on PORT %s: %v", port, err)
	}
	return serveUntilStopAs(ctx, ln, credentialsHTTPServer(ctx, srv, errw), errw, orchestratorProcessName)
}

// orchestratorProcessName is what this process's log lines call themselves: the
// name Cloud Logging shows beside them, and the one thing that tells the
// resident orchestrator's stream apart from the credentials service's and the
// intake's, which are revisions of the same installation.
const orchestratorProcessName = "resident orchestrator"

// orchestratorTickPath is the route the installation's scheduler job wakes this
// service on (orchestrator.tf's http_target). It is one route and a POST: a GET
// on it is a person or a probe, and neither is a tick.
const orchestratorTickPath = "/tick"

// orchestratorStore is what this process reads the installation's node through,
// and it is two of internal/orchestrator's interfaces rather than a new one of
// this file's making: a pass makes the two reads that package's two readers
// exist for — ReadState takes a Reader (one node read, no write reachable) and
// ReadLeaseNode takes a CAS (the ETag a listing reads) — and the real client
// (internal/rtdb's *Client) satisfies both. Naming the pair here is what lets a
// test serve the pass from a store of its own, and it is why the fake beside
// this file has a PutIfMatch that fails the test: the pass must not reach the
// write half of the pair it holds.
//
// ServerNow is the third of the store's methods a pass reads, and it is the
// database's own clock (rtdb.Client.ServerNow): the pass asks
// internal/orchestrator's Plan whether an attempt's claim window has passed,
// and D1230's rule is that no judgment under this subtree is made against this
// machine's clock. ok is false before the store has answered anything, which
// after the two reads above it never is.
type orchestratorStore interface {
	orchestrator.CAS
	orchestrator.Reader
	ServerNow() (time.Time, bool)
}

// residentOrchestrator is the resident orchestrator as the host serves it: the
// node it serves and the lines a pass writes.
type residentOrchestrator struct {
	id string
	db orchestratorStore
	// launch starts a re-issued attempt, over the command's own launch path
	// (orchestrator_launcher.go's residentLauncher, built by the configuration
	// builder: the claim, the mint and the job execution are that launcher's
	// and the command's): this file hands a plan's re-issue to it and writes
	// no step of its own — no claim, no mint, no jobs.run.
	launch orchestrator.Launcher
	// consumer is the tick's mailbox half, nil when this deployment has none
	// (orchestratorConfig says why that is a deployment and not a mistake).
	consumer *orchestratorConsumer
	log      *slog.Logger
}

// newResidentOrchestrator is the service the host serves with. It refuses a
// configuration that is missing a piece rather than serving one that reads
// nothing: the builder above is the only caller, and a nil store here would be
// a deployment whose every tick answered with a panic.
func newResidentOrchestrator(cfg orchestratorConfig) (*residentOrchestrator, error) {
	if cfg.id == "" {
		return nil, fmt.Errorf("no orchestrator id")
	}
	if cfg.db == nil {
		return nil, fmt.Errorf("no store to read %s's node through", orchestrator.SubtreeRoot)
	}
	if cfg.launch == nil {
		return nil, fmt.Errorf("no launcher for the attempts this node records")
	}
	if cfg.log == nil {
		return nil, fmt.Errorf("no log for this process's one line per pass")
	}
	// The consumer is NOT refused when it is absent — see orchestratorConfig —
	// and it is refused when it is half-built, because a consumer with no reader
	// or no holder is one whose every mailbox pass would fail on a nil the
	// deployment could have told apart from no consumer at all. A mailbox's own
	// writer is checked where it is used (orchestrator's consumeMailbox refuses
	// one that names another mailbox or is missing), because it is per pairing
	// and not per deployment, and that refusal is a mailbox's rather than the
	// whole tick's.
	if cfg.consumer != nil && (cfg.consumer.reader == nil || cfg.consumer.holder == "") {
		return nil, fmt.Errorf("a mailbox consumer with no holder or no reader: a deployment either has a consumer it can run or none at all")
	}
	return &residentOrchestrator{id: cfg.id, db: cfg.db, launch: cfg.launch, consumer: cfg.consumer, log: cfg.log}, nil
}

// ServeHTTP is the whole of what this service answers.
//
// Nothing here verifies a caller (the file's own header says why: Cloud Run's
// IAM is this route's fence) and nothing here reads a request body, a header or
// a query: a tick carries no argument, so a caller has nothing to say beyond
// the request line, and a route that read a body would be one an attacker could
// put text into.
func (s *residentOrchestrator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != orchestratorTickPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "a tick is a POST", http.StatusMethodNotAllowed)
		return
	}
	pass, err := s.tick(r.Context())
	if err != nil {
		// The log line carries the store's own words, on one line: this
		// process's log is Cloud Logging, which is the operator's, and an rtdb
		// error quotes the path it read, so a newline in it would be a line
		// nobody wrote (oneLine). The answer carries a fixed sentence instead,
		// which is all a scheduler needs to record the tick as failed --
		// retry_config is retry_count = 0, so nothing is retried behind it.
		s.log.Warn("the tick could not read this installation's node",
			"process", orchestratorProcessName, "orchestrator", s.id, "error", oneLine(err.Error()))
		http.Error(w, "the tick could not read this installation's node; see the service's log", http.StatusServiceUnavailable)
		return
	}
	attrs := []any{
		"process", orchestratorProcessName, "orchestrator", pass.Orchestrator,
		"rev", pass.Rev, "items", pass.Items, "intents", pass.Intents,
		"lease_holder", pass.LeaseHolder, "lease_at", pass.LeaseAt,
		"unreported", pass.Unreported, "carried", pass.Carried, "left", pass.Left,
	}
	// The mailbox half's counts ride this same line when the deployment has one:
	// an operator reading a tick wants one line per tick and not two, and the
	// attributes are absent — rather than zero, which is what an empty inbox
	// reports — when there is no consumer to have read one.
	if m := pass.Mailbox; m != nil {
		attrs = append(attrs,
			"mailboxes", m.Mailboxes, "mail_leases", m.Leases, "mail_lost", m.Lost,
			"mail_read", m.Read, "mail_verified", m.Verified, "mail_refused", m.Refused,
			"mail_answered", m.Answered, "mail_present", m.Present, "mail_refusals", m.Refusals)
	}
	s.log.Info(orchestratorPassLine, attrs...)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(pass); err != nil {
		// The body is a convenience for whoever forced the tick by hand; the
		// log line above is the evidence. A client that hung up mid-answer is
		// not a failed tick, and there is nothing to do about it here.
		s.log.Warn("the tick's answer was not written", "process", orchestratorProcessName, "error", oneLine(err.Error()))
	}
}

// orchestratorPass is what one pass read, and it is the whole of what this
// service acts on: the state the previous pass left, and the lease beside it.
//
// It is a deliberate subset of what the node holds — counts rather than the
// items themselves — because the response body is a return value and the log
// line is one line, and neither is a way to read a project's plan out of Cloud
// Logging. Nothing here is a wire format a reader outside this binary consumes:
// the caller that forces a tick is a scheduler, which ignores the body, or a
// person reading it once.
type orchestratorPass struct {
	Orchestrator string `json:"orchestrator"`
	// Rev is the state node's revision, which is 0 for the node a project has
	// not written yet: ReadState answers the empty state rather than an error,
	// so the revision is how a reader tells "nothing here yet" from "here is
	// what the previous pass left".
	Rev           int64 `json:"rev"`
	SchemaVersion int   `json:"schema_version"`
	Items         int   `json:"items"`
	Intents       int   `json:"intents"`
	// LeaseHolder is the daemon holding this id, empty when nobody does, and
	// LeaseAt is the server clock of its last beat as RFC 3339: both are read
	// from the store, never remembered, because the lease is the fence two
	// daemons meet on and a process that cached it would answer about a claim
	// that moved.
	//
	// What this pass does NOT say is whether the claim is FRESH, which
	// orchestrator.LeaseClaim.Fresh decides against the reading's own stamp
	// (server time, never a revision's own clock). Nothing here acts on the
	// lease yet, so the judgment has no caller: it is P5.1d's launcher and
	// P5.1f's consumer that fence their work on it, and they read the lease
	// themselves rather than a fact this line remembered.
	LeaseHolder string `json:"lease_holder"`
	LeaseAt     string `json:"lease_at,omitempty"`
	// Unreported is what this pass READ of the node and NOT what the runs bucket
	// would say, and the name is the whole of that distinction: an attempt the
	// node records, past its ten-minute ClaimTTL window, for which this pass
	// cannot see a start. A run that is simply longer than that window is one of
	// these too — nothing here reads the runs bucket (the header says why), and
	// ClaimTTL is all this process has to tell "in flight" from "was never seen
	// to go" — so the number is an upper bound and never a count of launches
	// owed. It is a wire field, and this name is the one it keeps: "requests"
	// would have said the node was asking for a launch, which is a claim about
	// the bucket this pass does not read.
	//
	// Carried is how many of them this pass STARTED, and it is a different
	// number from Unreported on purpose: an attempt whose launch is refused
	// (the runs bucket does not hold its task, its repository's document cannot
	// be read) is one this pass read and did not carry, and every refusal is a
	// line of its own below the pass's own (carry). One that was already going
	// is counted here too, and correctly: the launcher answers the execution
	// the run's own launch record names (existingLaunch), so what this counts
	// is attempts a launcher answered a start for, in the same run id.
	//
	// Left is the actions the plan derived that ARE a write's: a first launch, a
	// retry, an abandon. They are read and reported and never acted on: this
	// pass writes no node (D1223, D1241) and the starter it holds has no
	// commit, so a first launch reaches nothing at all.
	Unreported int `json:"unreported"`
	Carried    int `json:"carried"`
	Left       int `json:"left"`
	// Mailbox is the tick's other half (P5.1f): what the pass over this
	// installation's paired phones came to, and nil when this deployment was
	// wired with no consumer at all. It is a pointer for exactly that
	// distinction — an absent half and a half that read nothing are the same
	// counts, and the tick's warning line is not the only place a person reads a
	// pass (orchestratorNoConsumer says the rest).
	Mailbox *orchestratorMailboxPass `json:"mailbox,omitempty"`
}

// orchestratorMailboxPass is what the tick's mailbox half came to: one pass per
// paired phone, and the counts internal/orchestrator's ConsumerOutcome reports,
// named the same so a reader moves between the two without translating.
//
// The refusals themselves are LINES and not this field: an error names the
// mailbox or quotes a path, which is what a person needs to know which phone is
// stuck, and a log line is where this process's own carry puts them too. What
// the field carries is that there was one, so a pass line with a refusal in it
// does not look like a pass that read nothing.
type orchestratorMailboxPass struct {
	Mailboxes int `json:"mailboxes"`
	// Leases is the mailboxes this pass holds after it, and Lost is the ones it
	// does not; the two add up to Mailboxes, which is the whole of the first
	// decision a pass makes about a phone.
	Leases int `json:"leases"`
	Lost   int `json:"lost"`
	// Read is the records the inbox held, Verified the ones the pairing's tag
	// accepted, Refused the ones it did not (which are answered in no way at all
	// — consume.go says why), Answered the replies this pass wrote, and Present
	// the ones a previous pass had already written and this one found in place.
	// Consume carries nothing, so a record it verified is answered and no attempt
	// is started for it.
	Read     int `json:"read"`
	Verified int `json:"verified"`
	Refused  int `json:"refused"`
	Answered int `json:"answered"`
	Present  int `json:"present"`
	// Refusals is how many lines this pass wrote below its own, which are the
	// only thing about a refusal that is not visible in the counts above.
	Refusals int `json:"refusals"`
}

// orchestratorPassLine is the one line an operator reads to see what a tick came
// to, and it is one sentence for every pass: what the tick read of the node and
// what it did with the plan that record derives. What it read and what it
// carried ride this line's own fields — items, intents, unreported, carried,
// left — and no attempt is named here: a run id is not this line's, and the
// count is the whole of what a person needs to know whether the service is
// doing its work (the per-attempt lines are carry's failures, below this one).
const orchestratorPassLine = "tick read the node and carried the re-issues it read"

// tick is one pass: read the node the previous pass left, read the lease beside
// it, and read the launch requests that record already holds.
//
// Everything it reads comes from the store. There is no state in this process
// between passes — Cloud Run keeps an instance alive between ticks and gives it
// to nobody else, so a pass that held what it read would answer from memory
// after the node had moved, which is exactly the shadow of the node the
// orchestrator's whole design refuses (the node is the only truth, D1232). The
// reads are two: ReadState is a reader's read of the state node (the absent
// node is this orchestrator's empty state, and a document another id or role
// wrote is ErrNotThisNode rather than something to act on), and ReadLeaseNode
// is a listing's read of the lease, taken without a claim and without a clock
// of this process's own.
//
// THE PLAN IS READ AND ITS RE-ISSUES ARE CARRIED (P5.1d): ResidentCarry splits
// the attempts this pass cannot see started from the actions that are a write's,
// hands the first half to the launcher the configuration builder built, and
// reports what it read, what it started and what it left (carry). The plan is
// read under caps whose ONE field this process reads and whose others are zero:
// the budget switch is read out of the same database the node is in (carry), so
// `fugaro budget kill` stops this pass as it stops a run and a switch that could
// not be read is ON — Caps' own rule, and the half of the zero Caps that is a
// reading. The ceilings stay zero, which is the fail-closed direction for them
// rather than an omission: the guardrails live in the installation's published
// policy, which this revision does not read (the header's six names are not
// where it lives), and a pass that cannot read a cap plans no first launch —
// while the re-issues still come back, an attempt that was committed and counted
// spending nothing a cap is there to bound.
//
// AND THEN THE SAME TICK IS THE MAILBOX HALF (P5.1f): consume reads this
// installation's paired mailboxes — the lease, the inbox, the tag, the answer —
// through internal/orchestrator's Consume, which is the phase-1 consumer this
// command already runs on a laptop. It comes AFTER the node's half and not
// before: the node is what this service exists for, a launch the plan owed is
// already going by the time a mailbox is read, and a phone's message changes no
// node here (the acting half is P5.1g's). The two halves share one store and one
// line, and neither can stop the other: a mailbox pass that refuses is a warning
// line and a nil Mailbox in the pass, while the node half has already been read
// and carried.
//
// WHAT IT DOES NOT DO is the rest of section 6's list as this step can hold it:
// no write to any NODE of the orchestrator subtree (there is no PutIfMatch on
// this path) and no message carried out — a mailbox write goes through the
// mailbox module's own signer and create-only reply, and an accepted record is
// answered and not acted on — plus no secret, no IAM and no deploy: the header's
// paragraph says why the one launch path is built where it is. The test beside
// this file holds the first of those the only way it can be held: the store it
// serves with refuses a write, so a pass that reached for one fails rather than
// passes.
func (s *residentOrchestrator) tick(ctx context.Context) (orchestratorPass, error) {
	state, err := orchestrator.ReadState(ctx, s.db, s.id, orchestrator.RoleOrchestrator)
	if err != nil {
		return orchestratorPass{}, err
	}
	claim, err := orchestrator.ReadLeaseNode(ctx, s.db, s.id)
	if err != nil {
		return orchestratorPass{}, err
	}
	pass := orchestratorPass{
		Orchestrator:  s.id,
		Rev:           state.Rev,
		SchemaVersion: state.SchemaVersion,
		Items:         len(state.WorkItems),
		Intents:       len(state.Intents),
		LeaseHolder:   claim.Holder,
	}
	if !claim.At.IsZero() {
		// The store's own stamp, as RFC 3339 in UTC. It is not this process's
		// clock: a laptop's clock is what the lease must not be read with
		// (D1230), and neither is a revision's.
		pass.LeaseAt = claim.At.UTC().Format(time.RFC3339)
	}
	s.carry(ctx, state, &pass)
	s.consume(ctx, &pass)
	return pass, nil
}

// consume is the tick's mailbox half (P5.1f): the phase-1 consumer of `fugaro
// mailbox` (internal/orchestrator's Consume, which is P4.1's pass and this
// process's — one pass in two places rather than two implementations of one),
// run once per tick over every mailbox this installation has a pairing key for.
//
// What it does per mailbox is the module's and is not repeated here: it takes or
// renews the lease under the holder, reads the inbox, verifies every record with
// the pairing's key and answers the ones it accepted — under the minted `mc`
// claim, which is the whole difference between this consumer and the laptop's
// (the header's paragraph). What it carries is nothing, because the acting half
// of the consumer is P5.1g's: an accepted message is answered with the pass's
// own sentence saying it was read and carried nothing out, so a phone learns
// that the message arrived and is not presented as though something had happened.
//
// A REFUSAL IS ONE WARNING LINE AND THE PASS GOES ON, which is carry's own rule
// for its own half: a mailbox this pass could not read — the store refused the
// heartbeat, the name is not one the module will build a path from, the
// database's clock could not be read — is a phone whose messages are going
// nowhere until a person looks, and the pass names it. What is NOT one line per
// mailbox is the reading itself: a mailbox that was simply empty is a count in
// the pass line, because there is nothing a person could do about it.
//
// THE BUDGET SWITCH DOES NOT STOP THIS HALF, deliberately: `fugaro budget kill`
// stops what SPENDS (carry, above, and the runs it would start), and a consumer
// that carries nothing spends nothing — it reads a phone's message and answers
// it with "nothing was performed", which is what the phone is owed whether the
// switch is on or not. The day the acting half lands (P5.1g) that changes, and
// that half is the one that has to read the switch: it is the half that starts
// a run.
//
// A deployment with no consumer is the third case and is said in one line rather
// than passed over in silence: the counts of an unwired tick and of an empty
// inbox are the same counts, and orchestratorNoConsumer says the rest. The
// Mailbox field stays nil there and on a refusal, so a pass line an operator
// reads never shows a consumer's numbers that no consumer reported.
func (s *residentOrchestrator) consume(ctx context.Context, pass *orchestratorPass) {
	if s.consumer == nil {
		s.log.Warn(orchestratorNoConsumer, "process", orchestratorProcessName, "orchestrator", s.id)
		return
	}
	out, err := orchestrator.Consume(ctx, s.consumer.reader, orchestrator.ConsumerOptions{
		Holder:    s.consumer.holder,
		Mailboxes: s.consumer.mailboxes,
	})
	if err != nil {
		// Consume answers this way before it reads or writes anything at all —
		// the database's clock, which no judgment in this subtree is made
		// against a container's clock without (D1230) — so nothing is owed to a
		// phone behind this line and the pass reports no mailbox.
		s.log.Warn("the tick read no mailbox", "process", orchestratorProcessName, "orchestrator", s.id, "error", oneLine(err.Error()))
		return
	}
	pass.Mailbox = &orchestratorMailboxPass{
		Mailboxes: out.Mailboxes,
		Leases:    out.Leases,
		Lost:      out.Lost,
		Read:      out.Read,
		Verified:  out.Verified,
		Refused:   out.Refused,
		Answered:  out.Replied,
		Present:   out.Present,
		Refusals:  len(out.Refusals),
	}
	for _, err := range out.Refusals {
		// One line per mailbox this pass could not consume. The error's own
		// words name the mailbox or the node the store refused (oneLine because
		// an rtdb error quotes the path it read, and this log is Cloud Logging).
		s.log.Warn("the tick could not consume a mailbox",
			"process", orchestratorProcessName, "orchestrator", s.id, "error", oneLine(err.Error()))
	}
}

// carry reads what the node's own record derives — the attempts this pass
// cannot see a start for, and the actions beside them that are a write's — and
// starts the first of those two through the launcher, which is the whole of what
// this process does besides reading.
//
// The clock it judges the ten-minute claim window with is the STORE's, not this
// machine's (D1230): ok is false only before the store has answered anything,
// and the two reads above are what made it answer — a pass that could not read
// it reads nothing rather than deciding against a container's clock, which is
// the direction every refusal in this file takes.
//
// A RE-ISSUE IT READ IS STARTED, under the run id the node recorded and through
// the command's own launch (ResidentCarry, and orchestrator_launcher.go's
// residentLauncher behind it): the claim, the mint and the job execution are
// that launcher's, and this file writes no step of them. A run that is longer
// than the claim window is read as one this pass cannot see started and reaches
// the launcher too — safely, because the launcher reads the run's own launch
// record before it starts anything and answers a run that already went with the
// execution that record names (existingLaunch), so the same run id is never a
// second execution.
//
// A REFUSAL IS ONE WARNING LINE AND THE PASS GOES ON. What a launcher refuses
// here is an attempt of this installation that cannot be started from this
// container — a record whose task the runs bucket does not hold, a repository
// whose document cannot be read — and it is a thing a person has to see: the
// attempt will not start on its own, and the node holds it open. So each one is
// a line naming the run and the launcher's own words, on one line (the log is
// Cloud Logging, and an error may quote a path), and the pass still carries
// every other attempt it read (internal/orchestrator's resident_launch.go says
// why one failure must not stop the rest). What is NOT written per attempt is
// the reading itself: a long run is counted in the pass's own line
// (Unreported) and never warned about once per tick, because there is nothing a
// person could do about a run that is simply still going.
//
// The RunView is empty because this process reads no run record to judge "did
// this run start" (the header says why), which is what makes the count an upper
// bound (internal/orchestrator's resident_launch.go says what the empty view can
// and cannot tell a caller). Nothing else about the read is apart: the items,
// the caps and the breaker are Plan's own.
func (s *residentOrchestrator) carry(ctx context.Context, state orchestrator.State, pass *orchestratorPass) {
	now, ok := s.db.ServerNow()
	if !ok {
		s.log.Warn("the database's clock could not be read, so this tick read nothing of the plan",
			"process", orchestratorProcessName, "orchestrator", s.id)
		return
	}
	// The budget switch is read, not assumed, and it is the one guardrail this
	// process can reach: `config/kill/global` is a node of the same database the
	// state node is in, so `fugaro budget kill` stops this pass exactly as it
	// stops a run. A read that FAILED is ON and reads nothing, which is Caps'
	// own rule — "a switch it could not read is on" — and the daemon's own
	// reading of the same node (orchestrator_run.go's caps) answers the same way.
	// The zero value of the field is the permissive one, so it is never what a
	// pass hands in unwatched.
	kills, err := budget.ReadGlobalKill(ctx, s.db)
	if err != nil {
		s.log.Warn("the budget switch could not be read, and a switch this process cannot read is on, so this tick read nothing of the plan",
			"process", orchestratorProcessName, "orchestrator", s.id)
		return
	}
	on, which := kills.On()
	if on {
		// One line, because "there was nothing to read" and "the switch is on"
		// are the same numbers in the pass and an operator reading the log has
		// to be able to tell them apart.
		s.log.Warn("the budget switch is on, so this tick read nothing of the plan",
			"process", orchestratorProcessName, "orchestrator", s.id, "switch", which)
		return
	}
	// The caps the plan is read under: the switch as it was READ — which is the
	// only field with a reading behind it, and the reason it is written from the
	// variable rather than left to the zero value, which is the permissive one —
	// and every ceiling at its zero, which is the fail-closed half: a zero cap is
	// no room at all, so a first launch and a retry plan nothing while the
	// re-issues still come back, because a re-issue spends nothing a cap bounds
	// (planNode's own reason).
	caps := orchestrator.Caps{KillSwitch: on}
	// The starter is one with a LAUNCHER and nothing else: an ActionReissue is
	// carried out through Starter.Launcher, and every other action a write's
	// would reach a commit this resident pass has none of (resident_launch.go's
	// own argument). ResidentCarry hands it only the re-issues.
	//
	// The launcher is the pass's OWN (residentPass): the service's launcher is
	// built once, and the attempt each re-issue asks the installation's
	// credentials service for is a fact of the record this tick read (D3136), so
	// the record travels with the launcher for the one call the carrying makes
	// and nowhere else — the service holds no state a second tick of one
	// instance could write, and nothing here outlives the pass.
	out := orchestrator.ResidentCarry(ctx, orchestrator.Starter{Launcher: residentPass{launch: s.launch, state: state}}, state, orchestrator.RunView{}, caps, now)
	pass.Unreported, pass.Carried, pass.Left = out.Read, out.Carried, out.Left
	for _, err := range out.Refused {
		// One line per attempt a launcher refused, and it names the run: the
		// error beside these attributes is the launcher's own sentence, and
		// the launcher's sentences name the record they were refused for
		// (retrySpec's "run acme/app/<id> has no task.json"), which is what a
		// person needs to know WHICH attempt is stuck — the pass's own line
		// carries counts and quotes no item, and it cannot say this. A run id
		// is not this process's secret: the operator wrote it on the node, and
		// `fugaro runs ls` prints it. oneLine because an error may quote a path
		// this process read, and this log is Cloud Logging.
		s.log.Warn("the tick could not start an attempt the node records",
			"process", orchestratorProcessName, "orchestrator", s.id, "error", oneLine(err.Error()))
	}
}
