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
// WHAT A PASS DOES TODAY, WHICH IS DELIBERATELY LITTLE. The tick's pass reads
// the node the previous pass left — the state document, and the lease node
// beside it — from the store and never from memory, and writes one line. It
// starts no execution, writes no message and writes no node: the launcher half
// is P5.1d and the consumer half is P5.1f, and a pass that started an execution
// today would be a run nobody asked for on a service that has no mailbox to
// read it from. What the tick buys at this step is exactly what the owner-run
// Check 660 measures: the service exists, its autoscaling floor is 0, the
// scheduler job that wakes it is enabled, and a forced tick over an empty
// mailbox reads the store and starts nothing.
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
// The environment is three names, and orchestratorServiceConfig below is the
// one place they are read: FUGARO_PROJECT, FUGARO_FIREBASE_PROJECT and
// FUGARO_RTDB_URL. An installation whose deployment names one of them twice, or
// names a fourth, is the module's business (orchestrator.tf): nothing here
// reads a variable it does not need, and nothing here prints one.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
var orchestratorServiceEnvNames = []string{envProject, envFirebaseFP, envRTDBURL}

// orchestratorWiring points the configuration builder at a stand-in for the one
// thing it reaches Google for as the process's own identity: the token source
// the installation's database is read with. It is a test seam for
// credentialsWiring's reason exactly — without one, building the configuration
// would need Application Default Credentials and a live metadata server, and
// the test that matters most, that a full environment builds a service this
// file serves with, could not be written. It is never set outside a test, and
// it carries no value: it is a constructor.
type orchestratorWiring struct {
	tokens func(ctx context.Context) (oauth2.TokenSource, error)
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
// through, and the log its lines go to.
type orchestratorConfig struct {
	id  string
	db  orchestratorStore
	log *slog.Logger
}

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
// WHAT IT DOES NOT READ is as much of the design as what it does. No region, no
// GCP project, no bucket: the credentials service needs all three because it
// serves runs and reads the installation's containers, and this process reads
// one node of one database as its own service account. No URL of its own
// either, which is the one place it differs from the other two processes: they
// verify their callers' Google ID tokens against their own URL as the audience,
// and this one is fenced by Cloud Run's IAM rather than by a check here
// (above), so the URL it is served on is the module's business and not this
// process's — a name nothing reads is a name an operator would believe was in
// effect.
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

	tokens := orchestratorProductionTokens
	if orchestratorTest != nil && orchestratorTest.tokens != nil {
		tokens = orchestratorTest.tokens
	}
	ts, err := tokens(ctx)
	if err != nil {
		return orchestratorConfig{}, remote(fmt.Errorf("no credentials for the installation's database: %w", err))
	}
	db, err := rtdb.New(rawURL, rtdb.Auth{Source: ts})
	if err != nil {
		// Unreachable past ValidateURL, and deliberately not reported with the
		// client's own error: that one quotes the URL.
		return orchestratorConfig{}, userErr("%s: the database client could not be built (it must be https, and reachable as this revision's own service account)", envRTDBURL)
	}
	return orchestratorConfig{id: id, db: db, log: runner.NewLogger(errw)}, nil
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
type orchestratorStore interface {
	orchestrator.CAS
	orchestrator.Reader
}

// residentOrchestrator is the resident orchestrator as the host serves it: the
// node it serves and the one line a pass writes.
type residentOrchestrator struct {
	id  string
	db  orchestratorStore
	log *slog.Logger
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
	if cfg.log == nil {
		return nil, fmt.Errorf("no log for this process's one line per pass")
	}
	return &residentOrchestrator{id: cfg.id, db: cfg.db, log: cfg.log}, nil
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
	s.log.Info("tick read the node and ran no step",
		"process", orchestratorProcessName, "orchestrator", pass.Orchestrator,
		"rev", pass.Rev, "items", pass.Items, "intents", pass.Intents,
		"lease_holder", pass.LeaseHolder, "lease_at", pass.LeaseAt)
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
}

// tick is one pass: read the node the previous pass left, read the lease beside
// it, and run no step.
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
// WHAT IT DOES NOT DO is the rest of section 6's list as this step can hold it:
// no write to any node (there is no PutIfMatch on this path), no message read or
// written (P5.1f), no execution started (P5.1d), no secret, no IAM, no deploy.
// The test beside this file holds the first of those the only way it can be
// held: the store it serves with refuses a write, so a pass that reached for
// one fails rather than passes.
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
	return pass, nil
}
