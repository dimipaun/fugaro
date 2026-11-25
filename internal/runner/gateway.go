package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// VertexBudgetRefusal is what a Vertex workflow under a budget in enforce
// mode is told: the gateway can't yet be relied on to cap Vertex spend.
// fugaro validate, fugaro update and the runner's bootstrap all say it.
//
// It names observe alone (AB1b, F11): the word that was the third mode is not
// one to send a reader to any more (D3009).
const VertexBudgetRefusal = "Vertex budgets are not supported yet: use budget.mode observe for this repository (see docs/gcp-live-checklist.md, check 20)"

// VertexPoolRefusal is what a pool holding a Vertex member is told under
// budget.mode: enforce (D1626, F8). It is VertexBudgetRefusal with the reason
// the pool is refused added in front, so the remedy — the same one — stays
// spelled once: a pool is admitted where a Vertex run is, and refused where a
// Vertex run is refused.
//
// It is a function and not a constant because the sentence names the member
// the pool lists, which is the operator's own key: a reader told only that
// "the pool is refused" would have to find which of its members did it.
func VertexPoolRefusal(pool, member string) string {
	return fmt.Sprintf("the pool %s lists the Vertex credential %s, and %s", pool, member, VertexBudgetRefusal)
}

// The exhaustion signals, one row per credential type (F5, D1634): what a
// credential of each type says when it has nothing left to serve with, and the
// one table "this credential is spent" is read from. A failover moves on
// nothing else — an attempt that ended on a signal here was refused by the
// credential that served it and not by anything about the work, so the next
// member is what a failover would serve the stage with — and this table is
// therefore what a pool's failover stands on, wherever one is built.
//
// Where one is built today is not this path. A pool on the runner path cannot
// serve one stage's model with one member and then the next (poolchoice.go's
// head works that out from the routes the gateway accepts), so ordered
// failover belongs to the credentials service (MC6); what this tree reads the
// table for is the OAuth subscription allowance's own sentence, by way of
// signalPhrases and oauthLimitPhrases (subscriptionUsageLimit). The reading
// helpers below are the table's Phrase half; the status and the error type a
// failover would ask of a row are recorded beside it, and their callers are
// the tests that pin every row until one is built.
//
// F5 is unverified (docs/model-crafts/reconciliation.md §5): the exact set of
// statuses and bodies each provider returns on exhaustion is not something
// this tree can observe, and the table is deliberately narrow for that reason,
// leaning the safe way the phrases it inherited from oauthLimitPhrases do. A
// signal it does not know leaves the stage failing with the agent's own words
// quoted, while a signal it wrongly knows would move the owner's run off a
// credential that was answering.
//
// Two halves of one signal sit in a row because they arrive by different roads:
//
//   - Status and Type are what the upstream answered with, and they are what
//     the gateway relays — a provider route's error as the status and the API
//     error type (internal/gateway's providerError), a direct Anthropic answer
//     verbatim. No code here reads them: the runner never holds an upstream
//     answer, only the agent's result text. They are the row's own statement
//     of the signal, and TestPoolExhaustionSignals is what consumes them — it
//     builds each row's upstream answer from them and asserts that the text
//     the gateway makes of it is one this table reads. A status a row claims
//     and a phrase that answer does not produce is a row the test refuses.
//   - Phrase is what the attempt's own text says about it, matched as a
//     substring of the lowercased text, and it is the half a reader of the
//     table takes: the OAuth row's phrases are read by the runner through
//     signalPhrases (oauthLimitPhrases, subscriptionUsageLimit), and the rows
//     of every type are read by credentialExhaustedText where a failover is
//     built. A row with no Phrase is a signal this table records and does not
//     claim to read: Vertex answers with Google's own error body, which
//     reaches the agent verbatim, and no phrase here guesses
//     its wording — the stage then fails with those words quoted, which is the
//     direction to fail in. A row with no Status is a phrase no status
//     carries: the OAuth subscription allowance is spent inside a session that
//     is answering normally, and the only witness is the CLI's own sentence
//     about it.
//
// A credential type absent from the table has no signal at all, and an answer
// from it is never read as exhaustion.
var exhaustionSignals = map[string][]exhaustionSignal{
	config.CredentialTypeClaudeOAuth: {
		{Status: 429, Type: "rate_limit_error", Phrase: `"type":"rate_limit_error"`,
			Why: "the subscription's rate limit"},
		{Phrase: "claude ai usage limit", Why: "the subscription's usage limit"},
		{Phrase: "hit your limit", Why: "the subscription's usage limit"},
	},
	config.CredentialTypeAnthropic: {
		{Status: 429, Type: "rate_limit_error", Phrase: `"type":"rate_limit_error"`,
			Why: "the key's rate limit"},
		{Status: 529, Type: "overloaded_error", Phrase: `"type":"overloaded_error"`,
			Why: "Anthropic is overloaded"},
	},
	config.CredentialTypeOpenRouter: {
		{Status: 429, Type: "rate_limit_error", Phrase: `"type":"rate_limit_error"`,
			Why: "the provider's rate limit"},
		{Status: 402, Type: "invalid_request_error", Phrase: "the provider answered with status 402",
			Why: "the provider's account is out of credit"},
	},
	config.CredentialTypeVertex: {
		{Status: 429, Type: "rate_limit_error", Why: "the project's quota"},
		{Status: 503, Type: "overloaded_error", Why: "Vertex is overloaded"},
	},
}

// exhaustionSignal is one way one credential type says it is spent.
type exhaustionSignal struct {
	// Status is the HTTP status the upstream answers with, 0 for a signal
	// that is not a status at all.
	Status int
	// Type is the API error type that status arrives under, "" when the
	// status has none of its own.
	Type string
	// Phrase is matched as a substring of the lowercased attempt text, "" for
	// a signal whose wording this table does not claim to know.
	Phrase string
	// Why names the signal as a person reads it, for the line that says which
	// signal moved a run.
	Why string
}

// credentialExhaustedText reports whether an attempt's own text says the
// credential of this type is spent. It is the table's Phrase half as a yes or
// no, asked by whoever is deciding whether to move an attempt off a credential
// — a failover, where one is built (the runner path's is not: see the table's
// head) — and it is closed on purpose: the phrases are shapes rather than
// English, for the reason the phrases it inherited record.
func credentialExhaustedText(typ, text string) bool {
	return exhaustionWhy(typ, text) != ""
}

// exhaustionWhy names the signal an attempt's text carries, "" when the text
// carries none: credentialExhaustedText's answer as a clause, so a caller that
// has to say which signal moved an attempt off a credential has the table's
// own words for it rather than a second reading of the text.
func exhaustionWhy(typ, text string) string {
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	for _, s := range exhaustionSignals[typ] {
		if s.Phrase != "" && strings.Contains(lower, s.Phrase) {
			return s.Why
		}
	}
	return ""
}

// signalPhrases are the text shapes the credential type's row records, in the
// order it records them: the table read as a list of phrases, so a reader that
// has to name the shapes themselves (oauthLimitPhrases) takes them from here
// rather than restating them.
func signalPhrases(typ string) []string {
	var out []string
	for _, s := range exhaustionSignals[typ] {
		if s.Phrase != "" {
			out = append(out, s.Phrase)
		}
	}
	return out
}

// coderPinPath is where config's own checks point a problem with the coder
// pin: the first of internal/config's pinPaths, the string every
// agent.models.* problem carries in its Path. Restated here because config
// keeps those paths unexported, and pinned by the arm tests that name a coder
// the run's policy refuses (shadow_test.go).
const coderPinPath = "agent.models.coder"

// reviewerPinPath is where config's own checks point a problem with the
// reviewer, restated for the same reason as coderPinPath: a review level's
// own model is judged in the slot its stage pins, and the level checks below
// have to tell that problem from the ones about the run's other pins.
const reviewerPinPath = "agent.models.reviewer"

// diagnoseModelPath is where a problem with the diagnose stage's model is
// named: the key internal/config's own validator and settings registry read it
// under (agent.models.diagnose, validateAgentModels). The diagnose stage's
// model is judged in the coder's slot, which is the slot config.StageRole
// resolves the stage to and so the one its output limit is read from
// (applyRecipeOutputLimit), and a problem about it is renamed here.
const diagnoseModelPath = "agent.models.diagnose"

// modelRefusal is the class of checkBudget failure the run's model policy
// raises — a model this run may not run, or cannot price — rather than
// infrastructure failing under it, and it carries whether the coder pin was
// one of the models refused. The coder is the one pin an A/B arm moves
// (D555): a challenger arm's own model refused is the arm's own refusal
// (D556), while the pins both arms share, and every failure that is no model
// refusal at all (a Vertex budget, a managed settings directory), are the
// champion's conditions too and are never recorded as the arm's decision.
type modelRefusal struct {
	msg      string
	coderPin bool
}

func (e *modelRefusal) Error() string { return e.msg }

// namesCoderPin reports whether the coder pin is one of the models a
// model-policy check refused.
func namesCoderPin(ps []config.Problem) bool {
	return slices.ContainsFunc(ps, func(p config.Problem) bool { return p.Path == coderPinPath })
}

// refusal joins a model-policy check's problems into checkBudget's error, of
// the class above. what is the sentence the run reports them under, exactly
// as checkBudget has always reported them.
func refusal(what string, ps []config.Problem) *modelRefusal {
	msgs := make([]string, len(ps))
	for i, p := range ps {
		msgs[i] = p.String()
	}
	return &modelRefusal{msg: what + ": " + strings.Join(msgs, "; "), coderPin: namesCoderPin(ps)}
}

// haltGrace is how long a halted stage's agent gets to exit on its own
// before it is stopped. A var so a test can shorten it.
var haltGrace = 60 * time.Second

// gatewayCloseTimeout bounds closing the gateway after the last stage.
const gatewayCloseTimeout = 10 * time.Second

// allowRoutingSettingsEnv is the live test's knob: it skips the refusal of
// routing settings, so check 20 can measure what Claude Code does with
// them. It works only on a local run against the real API.
const allowRoutingSettingsEnv = "FUGARO_TEST_ALLOW_ROUTING_SETTINGS"

// vertexRegionPrefix starts Claude Code's per-model Vertex region overrides.
const vertexRegionPrefix = "VERTEX_REGION_"

// credentialsServiceURLEnv is where a job learns the URL of its
// installation's credentials service (C1.15, D146/D147): the installation's
// own Cloud Run service, which the owner opts into and which the job is
// given an invoker grant on (C1.18). It is set on the job only when the
// installation has the service on, so its presence is the "the service is
// on" fact a cascade run is built on, and its absence is what refuses one
// (rather than a run reaching for a service that is not there).
const credentialsServiceURLEnv = "FUGARO_CREDENTIALS_SERVICE_URL"

// metadataHostEnv and the metadata path below are the job's own identity:
// the Google ID token the metadata server mints for the credentials
// service's URL, which Cloud Run IAM admits for run.invoker (D135). The host
// variable is the platform's own knob (google.golang.org's client libraries
// read the same one for the same reason, and the job's env is where the
// platform puts it), so a test can point this at a stand-in without any
// test-only switch in this file.
const (
	metadataHostEnv      = "GCE_METADATA_HOST"
	defaultMetadataHost  = "metadata.google.internal"
	metadataIdentityPath = "/computeMetadata/v1/instance/service-accounts/default/identity"
	// maxIDTokenBytes bounds a token this job will present, and is
	// internal/credsvc's own bound on one it will read (auth.go's
	// maxTokenBytes): a token longer than it, or one that is not a single
	// clean value, is refused here rather than sent to be refused there.
	maxIDTokenBytes = 8 << 10
)

// metadataClient is the client the metadata request is made with: a bound on
// the whole exchange, because the metadata server is the platform's own
// link-local address and an answer that takes longer than this is not one
// this run waits for. (Nothing is retried here: the gateway's one retry is
// for an upstream whose answer never began (internal/gateway,
// sendRetryingOnce), and a call that cannot present its identity fails like
// any other upstream error -- or halts the run, D143.)
var metadataClient = &http.Client{Timeout: 10 * time.Second}

// modelAuth is the run's answer to which auth its model path takes: the
// credentials service's review class when the run is a review class run
// (reviewClassRun), and its own configuration's auth otherwise (O1, D143,
// D893 (c)).
//
// It is an accessor and not a rewrite of r.cfg.Agent.Auth: the configuration is
// what the repository wrote, and the record's cost basis (runstore.ModelBasis)
// and the account walk (accountWalkApplies) read the field, so a class that
// moved it would move those with it.
//
// The class is spelled cascade because every arm it needs already exists for
// that auth, and none of them is a credential of the job's: a cascade run
// carries none (D143), the gateway holds the per-run token Claude Code presents
// (GatewayVars), the gateway's upstream is the credentials service
// (serviceUpstream), and agent.BuildEnv's cascade arm requires no secret and
// refuses a run with no gateway. The oauth arm the review job's own
// configuration would otherwise take is the one that demands a mounted
// CLAUDE_CODE_OAUTH_TOKEN (agent.BuildEnv) — the token CSR7's variant
// deliberately does not mount. So no fourth auth name is invented and
// internal/agent is not touched: GatewayOn already answers true for cascade,
// and the readers that share it (validate's prices read) keep answering for a
// coding run, which is the only question a repository's configuration can be
// asked.
func (r *run) modelAuth() string {
	if r.cfg == nil {
		return ""
	}
	if r.reviewClassRun() {
		return config.AuthCascade
	}
	return r.cfg.Agent.Auth
}

// reviewClassRun reports whether this run's model path is the credentials
// service's review class: the run is of that class (r.runClass — the task's
// mode: review, or the review class a diagnosis's launch writes) and the job it
// was admitted to is the review-only variant (jobTriple.Class, set where the
// candidate was matched).
//
// Both halves are facts of the run and of the job the platform put it in, and
// neither is read from the container's environment. r.reviewJob() reads
// FUGARO_REVIEW_JOB, which the launcher writes into the execution's environment
// (backend.LaunchSpec.Env) and which any job definition can carry: keyed on
// that marker, a run in the shared coding job whose environment carries it
// would take the review class's path — O2's arm skipped, the gateway's upstream
// the credentials service, its calls served by the review class's credential —
// while the service holds it to the allow-list alone. A coding run holds the
// App key and a Claude token, and a container must not be able to trade them
// for another class's credential by writing one variable. The marker's own job
// is a permission the caller states (reviewMayComment), never a credential.
//
// The conjunction fails closed. r.job is the triple the run was admitted to,
// and a per-repository job adopts none at all (jobTriple's zero value), so a
// run whose mode is review but which was not placed in the variant keeps its
// own configuration's auth — for the review job, oauth with a token its
// container does not mount, which is a refusal and never a call on a credential
// nobody chose.
// A run with no task at all is not one: r.runClass reads the task's own
// markers, and a caller that has no task is asking about a run that never
// started.
func (r *run) reviewClassRun() bool {
	return r.spec != nil && r.runClass() == classReview && r.job.Class == classReview
}

// gatewayOn reports whether the agent's calls go through the gateway. It is
// GatewayOn — the same predicate for the readers that hold a mode and an auth
// rather than a run — read off the run, and it is the only place that happens:
// the routing switch's own question (RoutingInertReason) and the gateway's
// rules all ask one function, so no reader can answer it differently. The auth
// it asks about is the run's model path's (modelAuth), never the file's own:
// the class is what decides where a review container's calls go.
//
// An oauth run on the switch is the one addition (B2.2, F30): GatewayOn cannot
// answer for it — an oauth auth is not one the gateway ever carried — so the
// run's own switch is asked beside it. The mode is no longer a fact of either
// answer (AB1b, D3009): GatewayOn carries every credential the gateway can
// carry in every mode, and the budget is accounted for by every run (Spend.On),
// so what is left here is the credential and the switch.
func (r *run) gatewayOn() bool {
	return GatewayOn(r.modelAuth(), r.spend.Mode) || r.serviceSwitched()
}

// serviceSwitched reports whether this run's model calls go through the
// installation's credentials service because its repository is on the switch
// (B2.2, F30): its auth is one the service serves (ServiceSwitchedAuth) and
// this job's environment carries the service's URL
// (credentialsServiceURLEnv). The URL is the whole of it — it is what the
// installation's own root hands a switched repository's job (B2.3, D2072), and
// the flip list (config.ServiceServedAuths) is not read here on purpose: the
// URL is what the rollout orders first — B2.3 handed it out while every mount
// was still there — so a run keyed on the list would ignore the URL that
// arrived first, and the flip that filled the list (B2.7) left this answer
// exactly what it was.
//
// A local run is never switched (D149, as for cascade): `fugaro exec` runs on
// the developer's own credentials, has no installation identity and no path to
// a private service, so a developer whose environment happens to carry the
// variable is not sent through one.
func (r *run) serviceSwitched() bool {
	return !r.d.Local && ServiceSwitchedAuth(r.modelAuth()) && envLookup(r.d.Env, credentialsServiceURLEnv) != ""
}

// serviceModelPath reports whether this run's model calls reach the
// installation's credentials service at all: the two arms gatewayUpstream
// builds a service upstream for — a switched repository's run (serviceSwitched)
// and a cascade run, which a review class run's model path is spelled as
// (modelAuth) — and never a local invocation, which serviceUpstream refuses
// (D149).
//
// It is the runner's own answer to "which upstream does this run's gateway
// build", asked by the readers that have to know before a gateway exists: R12's
// fact decides a run's routed calls are the service's only where the service is
// reached at all (providerKeysThroughService).
func (r *run) serviceModelPath() bool {
	return !r.d.Local && (r.serviceSwitched() || r.modelAuth() == config.AuthCascade)
}

// vertexServedByTheService reports whether this run's Vertex calls are the
// credentials service's — made on the service's own identity and priced and
// capped like a key's (D3174, CS4) — rather than this job's own GCP identity,
// which nothing in this gateway can cap (D1626).
//
// The fact is this run's model path (serviceModelPath), the predicate
// gatewayUpstream builds the upstream from, and never the service's address
// alone: the two cannot then disagree about where a Vertex call of this run
// goes. The address is not that fact — a launch hands it to a job whose
// document it could not read, judged on the repository's record or the deploy
// root's tfvars (internal/cli's credentialsServiceLaunchEnv) — so a `vertex`
// job can carry it while its Vertex calls are still the job's own identity
// (gatewayUpstream's vertex arm), and reading the address here would lift both
// refusals over exactly the uncapped path they exist for.
//
// A `vertex` file's run is the third shape the predicate answers (CS6): its
// job carries the service's address where the project's routing table reaches
// the repository (ServiceSwitchedAuth admits vertex, CS10 hands the address
// over), and its Vertex calls are then the service's, so both refusals stand
// down. Where the address is not there — an unmigrated repository, which no
// chain of the table serves and whose launch hands nothing over — the run's
// Vertex calls are still the job's own GCP identity and both refusals hold
// (D1626). A local run is never the service's (D149).
func (r *run) vertexServedByTheService() bool {
	return r.serviceModelPath()
}

// servedAuth is the auth this run's model path is spelled as for the arms that
// know the four auths and nothing about the switch: agent.BuildEnv and
// agent.GatewayVars build the agent's environment from it, and the record's
// per-stage ServedBy is named from it.
//
// A switched run is spelled cascade, for the reason a review class run is
// (modelAuth): every arm the switch needs already exists for that auth — no
// credential of the job's is mounted or handed over, the gateway holds the
// per-run token Claude Code presents, and the upstream is the credentials
// service — and the auths it is switched *from* name the opposite in
// internal/agent: an oauth auth with a gateway is refused there
// ("auth: oauth never goes through the gateway"), and an api-key auth with a
// gateway keeps ANTHROPIC_API_KEY's mount as the credential the agent's calls
// are made with, which is the one thing a switched run does not have. So
// internal/agent keeps every refusal and fence it has, and the switch is
// spelled where the run's own auth is read for the environment, exactly as the
// class is.
func (r *run) servedAuth() string {
	if r.serviceSwitched() {
		return config.AuthCascade
	}
	return r.modelAuth()
}

// gateway is where the agent's model calls go, nil until the gateway runs.
func (r *run) gateway() *agent.Gateway { return r.gwAgent }

// GatewayStageLimitFunc builds the gateway's per-stage governor for one run
// (plan T7.2, D331): the governance the run resolved, the tier it was
// dispatched at and the run's own cap, which is the figure a stage budget is a
// fraction of. The limits themselves are governance's and live in
// ee/governance (D527, D530); the gateway knows only the hook.
type GatewayStageLimitFunc func(g policy.Governance, t tier.Tier, cap pricing.Micros) gateway.StageLimit

// gatewayStageLimitFunc is the installed builder. The core ships none, and a
// gateway started by an untagged build has no StageLimit at all: it enforces
// the run's spend cap, the lease's sub-cap and the stage's pinned models and no
// per-stage budget, exactly as it did before the hook existed (D522, G10).
var gatewayStageLimitFunc GatewayStageLimitFunc

// SetGatewayStageLimit installs the gateway's stage-limit builder, refusing a
// second call: the edition of a build is decided once, by ee/register, and a
// builder that could be replaced after it would be a way to drop a tagged
// binary's stage budgets. A nil builder is refused for the same reason a nil
// function is in SetStage: it would silently mean "this build enforces
// nothing", which is what not calling this at all already says.
func SetGatewayStageLimit(f GatewayStageLimitFunc) error {
	if gatewayStageLimitFunc != nil {
		return errors.New("runner: the gateway stage limit is already set")
	}
	if f == nil {
		return errors.New("runner: the gateway stage limit needs a builder")
	}
	gatewayStageLimitFunc = f
	return nil
}

// gatewayStageLimit is what startGateway hands the gateway: the governor the
// run's own governance makes, or nil in a build that carries no builder.
//
// It is read once, at start, and never replaced: the limits are the ones the
// run resolved and the tier it was dispatched at, both of which are settled by
// then (resolvePolicy runs before the gateway starts), and the cap is the run's
// own — a stage budget is a fraction of it, so a run whose cap is 0 (observe
// with no cap at all) has no stage budget to enforce either.
func (r *run) gatewayStageLimit() gateway.StageLimit {
	f := gatewayStageLimitFunc
	if f == nil {
		return nil
	}
	return f(r.policy.Governance, r.spec.Tier, r.spend.Cap)
}

// startGateway starts the run's gateway and makes the agent's environment
// point at it. The real credential stays here.
func (r *run) startGateway(ctx context.Context) error {
	s := r.spend
	o := gateway.Options{Prices: s.Prices, Cap: s.Cap, Log: r.d.Log, EndStageWait: r.d.GatewayStageWait}
	// The run's own governance, resolved a stage before this: the stage
	// budgets, the review ratio and the fan-out stop the gateway enforces per
	// stage (T7.2). It is nil in a build that carries no limits.
	o.StageLimit = r.gatewayStageLimit()
	o.Mode = gateway.Observe
	if s.Mode == "enforce" {
		o.Mode = gateway.Enforce
	}
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess != nil {
		// The lease is the judge: it refuses a cap only when the project's
		// mode, or this run's own, enforces it, and it never refuses one in
		// observe. The gateway enforces whatever the lease refuses, so a
		// project switched to enforce mid-run stops this run too, rather
		// than leaving its calls waiting.
		o.Mode, o.Lease = gateway.Enforce, sess.Lease()
	}
	up, err := r.gatewayUpstream(ctx)
	if err != nil {
		return err
	}
	o.Upstream = up
	// The service's answer reaches the run here (C1.15): the typed
	// `exhausted_until` is what ends the stage in flight and starts the wait
	// (C1.17, D138). The same answers carry the tier, the credential's name
	// and the switch, which C1.16's ledger and C1.19's record consume; this
	// is the one hand-off C1.15 owns, and an answer that is not the exhausted
	// one is left alone rather than guessed at here.
	o.OnServiceAnswer = r.serviceAnswer
	// And the credentials service is what serves this run's model calls, which
	// is the whole of what this run's model path is (serviceModelPath, CS6):
	// the gateway then sends the run's current stage on every call it makes to
	// the service and reads the chain's own answer back — the source that
	// served the call, whether it was the chain's first and which model served
	// — through the hook below. A run whose model path is its own repository's
	// keeps both off, sends no stage header and is answered exactly as it was
	// before this contract: that is what lets the migration land one repository
	// at a time (R25, chain_names.go).
	o.ChainServed = gateway.ChainServed(r.serviceModelPath())
	o.OnChainAnswer = r.chainAnswered
	// Where each kind of this repository's credentials is served from, read
	// once here, at the run's start (C2, D2982): a record that moves a kind to
	// the workspace is a second upstream for that kind, and the record's answer
	// for the model kind is what the gateway serves its model calls from.
	if err := r.routeGateway(ctx, &o); err != nil {
		return err
	}
	routes, err := r.gatewayRoutes()
	if err != nil {
		return errors.New(r.redact(err.Error()))
	}
	o.Routes = routes
	r.routed = len(routes) > 0
	gw, err := gateway.Start(ctx, o)
	if err != nil {
		return fmt.Errorf("starting the gateway: %s", r.redact(err.Error()))
	}
	r.addSecret(gw.Token())
	if sess != nil {
		sess.SetUsage(func() pricing.Micros { return gw.Ledger().Used })
	}
	r.mu.Lock()
	r.gw = gw
	r.gwAgent = &agent.Gateway{BaseURL: gw.URL(), Token: gw.Token()}
	r.modelBy = map[string]pricing.Micros{}
	r.routeBy = map[string]pricing.Micros{}
	r.stageExtra = r.endGatewayStage
	r.mu.Unlock()
	return nil
}

// gatewayUpstream is where this run's gateway sends its calls and with what
// credential: the auth this run's model path takes (modelAuth), spelled once.
// startGateway builds its options from it, and the two are separate so that
// what a run does with each auth is one function a reader -- and a test -- can
// ask without starting a gateway.
//
// The auths are the three the gateway serves. api-key and vertex are the
// job's own credential (the run's key, or the job's own Vertex identity);
// cascade is the credentials service (C1.15, service.go), which holds the
// credential and attaches it per call, so the job has none and must never
// reach for one (D143). An auth with no arm is refused rather than guessed
// at: the gateway is the only thing between the agent and a model, and an
// upstream nobody chose is a credential nobody chose.
//
// A review class run is the cascade arm (modelAuth): the credential is the
// service's review class's and the job holds none, so an upstream built from
// the review job's own file — oauth, whose arm is refused here — is exactly
// what must not happen.
//
// A run on the switch is the service arm too, and it is asked first (B2.2,
// F30; a `vertex` run with CS6): the switch replaces this run's own credential
// with the service's, so the api-key arm below — which is the mounted key, the
// very credential the switch takes the repository off (F31, D2072) — and the
// vertex arm — which is the job's own uncapped GCP identity (D1626, D3174) —
// must not be reached for it. What is left of the run's own auth is the
// declaration serviceUpstream makes, which is the whole reason the auth is
// still read here.
func (r *run) gatewayUpstream(ctx context.Context) (gateway.Upstream, error) {
	if r.serviceSwitched() {
		return r.serviceUpstream()
	}
	up := gateway.Upstream{BaseURL: r.d.GatewayUpstream}
	switch r.modelAuth() {
	case "api-key":
		up.Kind, up.APIKey = "anthropic", envLookup(r.d.Env, "ANTHROPIC_API_KEY")
		r.addSecret(up.APIKey)
	case "vertex":
		up.Kind = "vertex"
		up.VertexProject = envLookup(r.d.Env, "ANTHROPIC_VERTEX_PROJECT_ID")
		up.VertexLocations = vertexLocations(r.d.Env)
		switch {
		case r.d.VertexTokens != nil:
			up.Token = r.d.VertexTokens
		case r.d.GatewayUpstream != "":
			// A test upstream must never be paired with real credentials.
			return gateway.Upstream{}, errors.New("starting the gateway: a test upstream needs a test token source")
		default:
			ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
			if err != nil {
				return gateway.Upstream{}, fmt.Errorf("starting the gateway: Vertex credentials: %w", err)
			}
			up.Token = ts
		}
	case config.AuthCascade:
		return r.serviceUpstream()
	default:
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: auth %q never goes through it", r.modelAuth())
	}
	return up, nil
}

// cascadeLocalRefusal is D149's refusal of a cascade auth in a local run, kept
// in one place because two doers say it: the service upstream below, and the
// routing switch's own arm in bootstrap (routing.go's applyRouting), which
// judges the auth before a gateway is ever started.
//
// The second caller is why this is not just a line in serviceUpstream: that
// function is reached only through the gateway, and only for a run whose mode
// turns the gateway on — while a local `fugaro exec` is usually mode-off or
// mode-unset, so its cascade file was answered by agent.BuildEnv instead
// ("agent.auth cascade needs..."), and a local run whose mode *is* on was
// refused for the switch, with the operator's remedy (publish the policy),
// which no local invocation can be fixed by: `fugaro exec` never cascades.
// The local invocation's own sentence is the one that names the fix — use
// api-key or oauth locally — so applyRouting says this before it judges the
// switch at all.
func cascadeLocalRefusal() error {
	return fmt.Errorf("agent.auth is %s and this is a local run: %s runs on the developer's own credentials and never through the credentials service (D149). Use agent.auth api-key or oauth locally", config.AuthCascade, execName)
}

// serviceUpstream is the credentials service's upstream (C1.15, D135): the
// service's URL from the job's environment, and the two identities every call
// carries -- the job's Google ID token, minted from the metadata server for the
// service's own URL, and the run's own Firebase ID token. No credential of the
// job's is anywhere in it: the whole point of the cascade is that the job holds
// none (D143).
//
// Two runs reach it: a cascade run (and, through the same arm, a review class
// run, whose model path is spelled cascade), and an api-key or oauth run whose
// repository is on the switch (B2.2, F30). The second declares what it is —
// its auth, and for oauth the account it was launched on — because the service
// holds the call to the record `fugaro update` wrote for that repository and
// refuses a declaration the record does not name (D2070 (a), B2.1). The
// declaration is a statement and never evidence, and it is the runner's: the
// gateway drops any the agent sent (service.go's serviceUpstream).
//
// Everything it refuses, it refuses before a call is made, because each of
// these is a run that cannot be served at all:
//
//   - a local run. `fugaro exec` is unchanged by the cascade (D149): it uses
//     the developer's own environment, has no installation identity and no
//     network path to a private service, and gains nothing by being routed
//     through one. A cascade auth locally is a mistake in the file, said so
//     rather than reached for, and a switched run is not a local one at all
//     (serviceSwitched).
//   - a job with no credentials service. The installation has not opted in
//     (D147), so there is no service to call and no credential anywhere this
//     run may use.
//   - a run with no budget identity. The run's own token is what the service
//     verifies the run with (C1.10); without it the service can only refuse,
//     and a run on this upstream is always admitted by the budget backend
//     (C1.19), so this is a fact about how the run was started and not about
//     the service.
//   - a test upstream. Deps.GatewayUpstream redirects the api-key and vertex
//     kinds to a stand-in; this upstream's destination is the installation's
//     own service and nothing else, so a test that set both is told rather
//     than silently sent to the service.
//
// The job's name is read the way the rest of the run reads it (jobName:
// from the platform's own report of the execution, never from an environment
// a launcher can write). It is a claim the service holds to the job-name
// rule and decides nothing from -- which job called is decided from the
// verified service-account email (C1.10) -- and the gateway refuses a
// service upstream without a valid one, so a run that cannot name its job
// stops here.
func (r *run) serviceUpstream() (gateway.Upstream, error) {
	if r.d.Local {
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: %w", cascadeLocalRefusal())
	}
	url := envLookup(r.d.Env, credentialsServiceURLEnv)
	if url == "" {
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: agent.auth is %s and this job has no credentials service: %s is not set, so the installation has not opted into one (D147) and this run has no credential of its own to fall back to (D143)", r.modelAuth(), credentialsServiceURLEnv)
	}
	if r.d.GatewayUpstream != "" {
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: a run whose auth is %s has the installation's own credentials service (%s) as its upstream, not a test upstream", r.modelAuth(), credentialsServiceURLEnv)
	}
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: a run whose auth is %s has no run identity: the budget backend is what admitted it, and the credentials service verifies the run by the identity that admitted it", r.modelAuth())
	}
	job, err := r.jobName()
	if err != nil {
		return gateway.Upstream{}, errors.New(r.redact(err.Error()))
	}
	if job == "" {
		// Belt and braces: jobName's own contract means this cannot fire
		// today -- the only run it answers "" for is a local one, refused
		// above -- but an upstream that reports no job is one the gateway
		// refuses anyway (validate), and a run that got here with none
		// should stop where the reason is known rather than there.
		return gateway.Upstream{}, fmt.Errorf("starting the gateway: a run whose auth is %s has no Cloud Run job name to report", r.modelAuth())
	}
	up := gateway.Upstream{Kind: gateway.KindService, BaseURL: url, Service: r.serviceIdentities(url, job, sess.IDToken())}
	if auth := r.modelAuth(); ServiceSwitchedAuth(auth) {
		// What this run's own repository goes out on, declared so the service
		// can hold the call to that repository's record (D2070 (a), B2.1). A
		// cascade run declares nothing: the record the service narrows it to
		// is its own repository's already, and "" is the contract's "no
		// declaration" (credsvc.RequestFromHTTP).
		up.AuthMode = auth
		if auth == "oauth" {
			// The account this run was launched on, spelled as a message
			// spells one: the run's own selection, "" being the account a run
			// that named none uses (config.DefaultClaudeAccount). It is what
			// the service reads the repository's accounts against, so a run
			// launched on an account that repository's record does not name is
			// refused rather than served another's.
			up.Account = selectedName(r.launchAccount())
		}
	}
	return up, nil
}

// serviceIdentities is how every call this run makes to the credentials service
// identifies itself: the job's Google ID token, minted per call from the
// metadata server for the service's own URL (which is the audience Cloud Run
// IAM checks, D135), the run's own Firebase ID token, the job's name, and the
// slip the run claimed at its start (DS1.5).
//
// The slip is presented on every call, which is how a cascade run is served a
// per-user, pool-owned or shared credential at all (D1933, R9). A run that
// holds none — an installation whose service has no slip routes, a claim that
// did not land — sends none, and the service decides.
//
// The model path (serviceUpstream) and the runner's own fetch of a workflow
// secret its job does not mount (workflowsecrets.go, B3.4) both read it from
// here, so one run cannot present two different identities to one service.
func (r *run) serviceIdentities(url, job string, runToken func() string) *gateway.Service {
	svc := &gateway.Service{
		Job:        job,
		RunIDToken: runToken,
		IDToken:    func(ctx context.Context) (string, error) { return metadataIDToken(ctx, r.d.Env, url) },
	}
	if hold := r.slipHold(); hold != nil {
		svc.SlipID, svc.SlipClaim = hold.id, hold.secret
	}
	return svc
}

// routeGateway is where this run's credential route reaches its gateway (C2,
// WS11): the record is read once, here, and the workspace's second upstream the
// gateway holds is built from it.
//
// A kind the record serves from the workspace is one this run reaches the
// workspace's service for, and the address is where that call goes (R3): it is
// carried by the record and by nothing else, and this run never takes an
// address from its own environment — F32's point, because an address a launcher
// supplies is an address a launcher can move, and every call to it presents
// this run's own token.
//
// The record's answer for the model kind is handed over as the one fact the
// gateway acts on (gateway.Options.ModelFromWorkspace), because the record's
// types are internal/creds's and the gateway cannot name them: internal/creds
// imports internal/budget, which imports internal/gateway, so the import is a
// cycle. There is no second reading of the record in the gateway and none here
// beyond this one: creds.Route.Holder decides, once, per kind.
func (r *run) routeGateway(ctx context.Context, o *gateway.Options) error {
	if r.d.Local {
		// D149 for the workspace's service as it is for the member's own
		// (serviceSwitched, cascadeLocalRefusal): `fugaro exec` runs on the
		// developer's own credentials, has no run identity for a service to
		// verify it by and no path to a private service, so it is not routed
		// anywhere. The record is not even read — a local run that stopped on a
		// record it could not read would be stopped by a document that says
		// nothing about it.
		return nil
	}
	route, err := r.credentialRoute(ctx)
	if err != nil {
		return err
	}
	if !servedFromWorkspace(route) {
		// Nothing is called at the record's address for a kind the project
		// serves (C2), so a run in that state holds no second upstream at all —
		// which is also every run with no record.
		return nil
	}
	// The workspace's own slip is claimed here, and here only: this is where
	// the run learns it has a kind served there, and a claim belongs to the
	// run's bootstrap (WC8, R3, R4). It is the same call against the other
	// service, with the id the launcher wrote for it (creds.PersonSlipEnv) and
	// the address the record carries, and a run the workspace granted no slip
	// asks for none.
	r.claimWorkspaceSlip(ctx, route.Address)
	ws, err := r.workspaceUpstream(route.Address)
	if err != nil {
		return fmt.Errorf("starting the gateway: %w", err)
	}
	o.Workspace = ws
	o.ModelFromWorkspace = route.Holder(creds.KindModel) == creds.HolderWorkspace
	// And the beat that keeps the service this upstream calls warm (WS13, R10,
	// D2985). It is started here because here is where the run learns it has a
	// kind served there, and it is given the upstream rather than the address,
	// so the beat cannot call an address or present an identity the run's own
	// calls do not (contract-wire.md's "on Holder and not on Address").
	r.startWorkspaceBeat(ctx, ws)
	return nil
}

// credentialRoute is the run's route record, read once for the whole run (C2,
// R4's "read once at the run's start").
//
// It is read at the first question that needs it, and there are two: where the
// run's forge credentials of a kind are served, which is the first thing
// bootstrap asks because the git forwarder starts before the clone (WS18), and
// which upstream the gateway holds (WS11). Both read the same answer, so a run
// cannot be of two minds about the service a kind comes from, and the record is
// read once and not once per question.
//
// The read is not held under the lock: it reaches the database, and the lock
// guards the whole run's state. Both callers are on the run's own bootstrap
// path, one after the other, so what this memoizes is the read and not a race
// between two of them.
func (r *run) credentialRoute(ctx context.Context) (creds.Route, error) {
	r.mu.Lock()
	if r.routeRead {
		route, err := r.credRoute, r.routeReadErr
		r.mu.Unlock()
		return route, err
	}
	r.mu.Unlock()
	route, err := r.readRoute(ctx)
	r.mu.Lock()
	r.credRoute, r.routeReadErr, r.routeRead = route, err, true
	r.mu.Unlock()
	return route, err
}

// forgeService is the credentials service this run's forge credentials of one
// kind are served by (WS18, D2979): the workspace's, at the address the run's
// own route record carries, or this project's own, whose address is the one the
// job's environment names (C1.15, D146) — which is every run with no record and
// every run whose record serves that kind from its project.
//
// fromWorkspace is the whole of the difference, and the two callers act on it
// rather than on the address: git traffic goes to whichever service this names
// (the forwarder is pointed at it), while the opener a run uses for that kind
// depends on there being a service at all — a forge credential served remotely
// is opened without a credential of the job's own, and one the project serves is
// opened exactly as it always was.
//
// The address is the record's and is not looked for anywhere else (R3, F32):
// the call a routed run makes to it presents the run's own token, so an address
// from the job's environment would be a token sent where a launcher chose. A
// record that serves a kind from the workspace always carries one (DecodeRoute
// refuses one that does not), and a record that carries none is a shape this
// release could not have read and stops the run rather than naming "".
func (r *run) forgeService(ctx context.Context, kind creds.Kind) (address string, fromWorkspace bool, err error) {
	if r.d.Local {
		// D149, as routeGateway's own: `fugaro exec` runs on the developer's
		// own credentials, has no run identity for either service to verify it
		// by and no path to a private one, so it is routed nowhere and the
		// record is not read.
		return envLookup(r.d.Env, providers.EnvCredentialsServiceURL), false, nil
	}
	route, err := r.credentialRoute(ctx)
	if err != nil {
		return "", false, err
	}
	if route.Holder(kind) != creds.HolderWorkspace {
		return envLookup(r.d.Env, providers.EnvCredentialsServiceURL), false, nil
	}
	if route.Address == "" {
		return "", false, fmt.Errorf("this run's credential route serves the %s kind from the workspace's credentials service and carries no address, which is a record this release could not have read: run 'fugaro update' in this repository's own terminal — its route stage publishes that record — and launch the run again", kind)
	}
	return route.Address, true, nil
}

// forgeKind is a provider kind as the credential route spells it. The contract
// names the forge kinds their forge's own name (creds.KindBitbucket,
// creds.KindGitHub; creds' TestRouteNamesArePinned holds the two spellings to
// each other), so the conversion is the whole of the mapping — and a provider
// the contract does not name reads as a kind no record serves, which is the
// project's own, exactly as an unknown kind reads everywhere else.
func forgeKind(kind string) creds.Kind { return creds.Kind(kind) }

// servedFromWorkspace reports whether the record serves any kind of this
// repository's credentials from the workspace's service. Every kind is asked
// through the contract's own Holder, so a kind the record leaves out — and
// every kind of the zero Route a repository with no record reads — answers
// HolderProject.
func servedFromWorkspace(route creds.Route) bool {
	for _, kind := range creds.Kinds {
		if route.Holder(kind) == creds.HolderWorkspace {
			return true
		}
	}
	return false
}

// readRoute is the credential route this repository's own `fugaro update`
// published, read once at the run's start (C2, R4): where each kind of its
// credentials is served from, and the workspace's address when a kind is served
// there.
//
// A repository with no record reads the zero Route and no error, and so does a
// run with no database at all: both are today's run — the project's own service
// for every kind — which is what a member that never pinned an address gets.
//
// A node this run's token may not read reads the same way and for a reason of
// the same kind: the grant that admits a run token to config/policy/route/<slug>
// belongs to the rules WS22 deploys, and until those rules are on the project
// the node answers a denial exactly as a node that is not there does. Reading
// the denial as a refusal would stop every run of every project that has not
// yet taken the rule, for a feature none of them uses, and what is lost by
// reading it as "no record" is nothing this run can be told apart from: the
// project's own service is where every kind was served before the record
// existed. Neither case is a fallback from an answer this run read: a record
// that IS readable and cannot be decoded is refused below, where the contract
// refuses it, rather than served as the project's.
//
// Every other failure stops the run. A record that exists and this release
// cannot read is the one thing a run may not carry on from: the record is what
// says where a credential is served, and a run that read it half-way would send
// a member's token to a service nobody chose (contract-wire.md).
func (r *run) readRoute(ctx context.Context) (creds.Route, error) {
	db, err := r.budgetClient(ctx)
	if err != nil {
		return creds.Route{}, err
	}
	if db == nil {
		return creds.Route{}, nil
	}
	path := creds.RoutePath(r.d.Store.Slug())
	var raw json.RawMessage
	found, err := db.Get(ctx, path, &raw)
	switch {
	case err == nil:
	case errors.Is(err, rtdb.ErrPermission):
		return creds.Route{}, nil
	default:
		return creds.Route{}, fmt.Errorf("the credential route this repository's own 'fugaro update' publishes at %s could not be read: %s. A run does not start on a route it could not read, because the record is what says which service each kind of its credentials comes from", path, r.redact(err.Error()))
	}
	if !found {
		return creds.Route{}, nil
	}
	route, _, err := creds.DecodeRoute(raw)
	if err != nil {
		return creds.Route{}, fmt.Errorf("the credential route at %s is not one this release reads: %s. Run 'fugaro update' in this repository's own terminal: its route stage publishes that record and rebuilds this run's image, so the record and the run are one release. A run does not start on a record it cannot read, because a record half-read is a credential sent to a service nobody chose", path, r.redact(err.Error()))
	}
	return route, nil
}

// workspaceUpstream is the run's second upstream (D2981): the workspace's
// credentials service, at the address the run's own route record carries, with
// the job's Google identity minted for that address's audience and the run's own
// token.
//
// The address is the record's and is not looked for anywhere else (R3): in
// particular the job's environment is not read for one, which is F32's whole
// point — this call presents the run's own Firebase token, and an address a
// launcher can move is a token sent to a launcher's destination. The identity
// is the run's own on this path exactly as it is against the member's own
// service (F30, O4), which SECURITY.md states: the workspace's service verifies
// the run by the token that admitted it.
//
// What travels with it is the workspace's own slip and never the member's
// (R4, WC8): the claim this run took at bootstrap under C1's headers, and
// nothing else. gateway.Workspace has no field for a member's slip and none for
// an auth declaration, so this is a property of the shape rather than of a rule
// this function keeps (R18).
//
// The slip is read from the hold rather than passed in, so this function cannot
// be called with one that disagrees with the secret the run holds: routeGateway
// claims it just above, and a run that holds none hands over none.
func (r *run) workspaceUpstream(address string) (*gateway.Workspace, error) {
	if address == "" {
		// DecodeRoute refuses a record without one, so a route that moves a
		// kind always carries an address: reaching this is a shape this
		// release could not have read, and no call is made to "".
		return nil, errors.New("this run's route serves a kind from the workspace and carries no address")
	}
	job, err := r.jobName()
	if err != nil {
		return nil, errors.New(r.redact(err.Error()))
	}
	if job == "" {
		// Belt and braces, as in serviceUpstream's own: jobName answers "" for
		// a local run alone, and a local run is not routed at all.
		return nil, errors.New("a run routed to the workspace has no Cloud Run job name to report")
	}
	// The token is the run's own as it is now (runIDToken) rather than the
	// budget session's, because the first caller of this is not the gateway:
	// the git forwarder starts before the clone, and the budget session does not
	// exist yet there (WS18). Both answer the same identity — runIDToken's own
	// comment has the two — and a run that holds neither still cannot be served.
	if r.runIDToken() == "" {
		// The workspace's service verifies the run by the identity that
		// admitted it (F30, O4), so a run that holds none cannot be served
		// there: there is no second identity to present and none to fall back
		// to. No launcher produces this (a local run is not routed at all, and
		// a job run is admitted by the launcher's token before it starts), and
		// a run that reported no identity to a service that verifies one would
		// be a call refused one at a time instead of a run stopped once.
		return nil, errors.New("a run routed to the workspace has no run identity, and the workspace's service verifies the run by the identity that admitted it")
	}
	ws := &gateway.Workspace{
		Address:    address,
		Job:        job,
		RunIDToken: r.runIDToken,
		IDToken: func(ctx context.Context, audience string) (string, error) {
			return metadataIDToken(ctx, r.d.Env, audience)
		},
	}
	if hold := r.workspaceSlipHold(); hold != nil {
		ws.SlipID, ws.SlipClaim = hold.id, hold.secret
	}
	return ws, nil
}

// execName is what a local invocation is called, for the sentences that have
// to name it.
const execName = "fugaro exec"

// serviceAnswer is where the credentials service's answer reaches the run
// (C1.15's hand-off to the runner, C1.17's reaction to it). The gateway calls
// it from its request path, once per answer, from the calls of every stage
// that is in flight, so it takes no lock of its own beyond noteExhausted's
// and does nothing a caller could block on.
//
// Three answers are acted on. The exhausted one is a decision about the stage in
// flight: the stage ends -- it did not fail -- and the runner waits for the
// reset and resumes it (C1.17, D138). An unreachable one is that same decision
// for the same reason (D2983): a call could not reach the service at all, the
// stage in flight ends, and the run waits for the instant the gateway's bound
// names and resumes the stage -- the gateway halts the run instead once that
// bound is spent, naming the service. The third is the name of the credential
// that answered (C1.19): a cascade run is the one run whose credential is not
// its own, so it is the one run that has to record which one it spent on --
// the record `/runs/<id>/result.json` carries (D140) and the registry entry
// the dashboard reads (C1.3). Both are written by r.recordCredential
// (runner.go, with the run's other record writers) and decided here, where the
// answer arrives, because a second reader of the same contract is how two
// records come to disagree. The owner of that credential's token is the same
// answer's second fact and part of the same write (F16): the two are the
// record's pair, one call to recordCredential hands over both, and an answer
// that names a credential the launcher does not own clears the owner rather
// than leaving the one an earlier answer gave. The tier a call settled by and
// the switch are C1.16's ledger.
//
// The typed answer is turned back into the contract's own type
// (credsvc.ExhaustedUntil) rather than a runner type of its own: the wire
// contract is the only spelling of it, and noteExhausted's caller in a test
// hands it the same shape.
func (r *run) serviceAnswer(a gateway.ServiceAnswer) {
	if a.Credential != "" {
		// The credential that answered and the owner of its token go over
		// together, as ONE write: the stages of a cascade call at the same
		// time, so two answers reach this point together, and handing the two
		// fields to two writers would let their writes interleave -- the
		// record pairing one answer's credential with another answer's owner.
		// recordCredential takes both under one lock for that reason, and an
		// empty owner is a write that CLEARS it (F16). An answer that names no
		// credential is a refusal or a call the service did not serve, and it
		// writes neither, so the record stands as it does.
		r.recordCredential(a.Credential, a.CredentialOwner)
	}
	if a.Switch != "" {
		// The switch the answer carries (C1.16, D140): the run's log of what
		// the cascade moved and why, with the cache-miss estimate the gateway
		// computed for that move.
		r.recordSwitch(a.Switch, a.SwitchCost)
	}
	if a.Exhausted != nil {
		tried := make([]credsvc.Tried, 0, len(a.Exhausted.Tried))
		for _, tr := range a.Exhausted.Tried {
			tried = append(tried, credsvc.Tried{Name: tr.Name, Tier: tr.Tier, Reason: tr.Reason})
		}
		r.noteExhausted(credsvc.ExhaustedUntil{ResetAt: a.Exhausted.ResetAt, Tried: tried})
	}
	if a.Unreachable != nil {
		// The gateway's own answer and not the service's (D2983): no service
		// answered this call, so it carries no tier, no credential name and no
		// switch, and the wait it starts is the wait an exhausted answer starts.
		r.noteUnreachable(*a.Unreachable)
	}
}

// metadataIDToken mints the job service account's Google ID token for
// audience from the metadata server: the credential Cloud Run IAM admits for
// run.invoker on the credentials service (D135, F30). The audience is the
// service's own URL, which is the whole of the check Cloud Run makes.
//
// It is minted per call rather than cached. The metadata server is the
// platform's own link-local endpoint, so the round trip is a millisecond
// against a model call's seconds; what a cache would buy is a token this run
// keeps using after the platform stopped issuing it -- a revoked service
// account, a job whose identity changed -- and what the run needs is the
// shortest-lived credential the platform will give it.
//
// The body is never read into an error and never logged: on success it is
// the token, and on failure it is the metadata server's own text about a
// request this code made. Only the status is reported.
func metadataIDToken(ctx context.Context, env []string, audience string) (string, error) {
	return metadataToken(ctx, metadataIdentityURL(env, audience))
}

// metadataToken is the read both of the metadata server's identity tokens are
// made with: this job's own, short format, and the attestation a Batch run's
// claim carries, in the full format (batch_attest.go). They are one function
// because the read is one — the same header, the same bound, the same refusal
// of anything that is not one clean token — and only the URL they are asked at
// tells them apart.
func metadataToken(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", errors.New("the metadata request could not be built")
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := metadataClient.Do(req)
	if err != nil {
		return "", errors.New("the metadata server could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the metadata server answered %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxIDTokenBytes+1))
	if err != nil {
		return "", errors.New("the metadata server's answer could not be read")
	}
	tok := strings.TrimSpace(string(b))
	// One clean token, at the bound the service reads a presented one by: a
	// token with a newline or a space in it, or one longer than that bound, is
	// refused here rather than sent to be refused there. The job's own token
	// is presented as a header value and could carry neither anyway; the
	// attestation travels in a body, and this refuses the same shapes rather
	// than reading a second rule.
	if len(tok) > maxIDTokenBytes || !printableToken(tok) {
		return "", errors.New("the metadata server answered with no usable identity token")
	}
	return tok, nil
}

// metadataIdentityURL is the metadata server's own endpoint for this job's
// identity: the link-local name the platform serves it at unless the job's
// environment says otherwise (GCE_METADATA_HOST, which the platform sets on
// its own hosts and which is therefore the only thing that may move it), the
// instance's default service account, and the audience Cloud Run IAM checks.
// It is separate from the request so that where the token is asked for is one
// expression a reader -- and a test -- can see without a call.
func metadataIdentityURL(env []string, audience string) string {
	host := envLookup(env, metadataHostEnv)
	if host == "" {
		host = defaultMetadataHost
	}
	return "http://" + host + metadataIdentityPath + "?audience=" + url.QueryEscape(audience)
}

// printableToken reports whether tok is one clean header value: non-empty and
// printable ASCII with no space (internal/credsvc's rule for a presented
// token, auth.go's maxTokenBytes and wire.go's printableHeaderValue).
func printableToken(tok string) bool {
	if tok == "" {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < 0x21 || tok[i] > 0x7e {
			return false
		}
	}
	return true
}

// vertexLocations are the locations a Vertex call may name: the job's
// region and every per-model region override.
func vertexLocations(env []string) []string {
	var out []string
	add := func(l string) {
		if l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	add(envLookup(env, "CLOUD_ML_REGION"))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, vertexRegionPrefix) {
			add(v)
		}
	}
	return out
}

// beginGatewayStage tells the gateway which models the stage may use.
func (r *run) beginGatewayStage(name string) {
	r.gw.BeginStage(r.stageSpec(name))
}

// stageSpec is the set the gateway admits while a stage runs: the model the
// stage's own pins name (stageModel — a review level's own model is its
// stage's, L4.5), the background model, the run's vision slot and the stage's
// output limit per call. A set that named the role slot where the pins name a
// level's own model would refuse every call of the stage as not pinned for it.
// The vision slot is carried for the stages a coder-role pin does not cover —
// the reviewer's, whose model is never moved (D2004) — where an image the
// stage's own model cannot reserve is still refused, and what the slot changes
// is the sentence the owner reads (the gateway's image refusal, V4, D2009). A
// coder-role stage runs the vision model outright (applyVision, V7), so its
// slot is the answer to a question the stage no longer asks.
func (r *run) stageSpec(name string) gateway.Stage {
	return gateway.Stage{
		Name:            name,
		Model:           r.stageModel(name),
		Background:      r.backgroundModel(),
		Vision:          r.cfg.Agent.VisionModel(),
		MaxOutputTokens: r.stageMaxOutput(name),
	}
}

// endGatewayStage closes the stage in the gateway, takes its figures into
// the run's, and says what it refused: violations first, then tokens.
func (r *run) endGatewayStage(stage string) ([]string, int64) {
	rep := r.gw.EndStage()
	r.mu.Lock()
	r.gwUsed += rep.Used
	r.gwNotional += rep.Notional
	r.lastStage = &rep
	for m, v := range rep.ByModel {
		r.modelBy[m] += v
	}
	for m, v := range rep.ByRoute {
		r.routeBy[m] += v
	}
	r.reported += rep.Reported
	r.unreconciled += rep.Unreconciled
	r.unparsed += rep.UsageUnparsed
	r.serviceTiers.Merge(rep.ServiceTiers)
	r.mu.Unlock()
	log := r.d.Log.With("stage", stage)
	if rep.UsageUnparsed > 0 {
		log.Warn(fmt.Sprintf("usage_unparsed: %d", rep.UsageUnparsed))
	}
	if rep.WouldHalt > 0 {
		log.Warn("budget: calls the cap would have refused", "calls", rep.WouldHalt)
	}
	// A response that named a service tier the table has no multiplier for
	// was charged the conservative multiple rather than failing the stage:
	// the run says which tiers, so an operator reading the log can tell a
	// provider's own tier from a price that is wrong (gateway.StageReport's
	// ServiceTiers, which the line prints as a record carries it).
	if tiers := rep.ServiceTiers.List(); len(tiers) > 0 {
		log.Warn("budget: service tiers charged conservatively", "tiers", strings.Join(tiers, ", "))
	}
	return rep.Violations, rep.Tokens
}

// lastWaited is how many calls the gateway told to retry in the stage
// that just ended.
func (r *run) lastWaited() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastStage == nil {
		return 0
	}
	return r.lastStage.Waited
}

// recordGatewayStageCost is the run's own account of a gateway stage that has
// just ended: the gateway's settled figure is the run's model cost and Claude
// Code's own is only a cross-check against it (runner.go calls this where the
// stage's agent returned), and a halt the gateway raised after the stage's
// watcher looked is recorded now.
//
// The figure is the money and the notional part together, which is what
// record.CostUSD and cost.model_usd mean (C1.16, P12): the run's whole model
// spend, of which cost.ModelNotionalUSD says how much was the subscription's.
// The billed side is cost.TotalUSD, which WithNotional keeps money-only --
// a cascade run's tier-A dollars are in the model figure and in no total and
// against no cap (D140).
//
// It is the ledger's figures that overwrite the run's, so a stage the run's
// stage-scoped Claude OAuth account served is added back in (r.direct): the
// ledger never saw its calls, and without the third term the next gateway
// stage would erase the figure from the run's own record (P11). Nothing of
// this stage is asked of the gateway, which is why runner.go does not call
// this for one.
func (r *run) recordGatewayStageCost(ctx context.Context, stage string, claudeUSD float64, stopped bool) {
	r.drainHalt(ctx, stage)
	r.crossCheckCost(stage, claudeUSD, stopped)
	r.mu.Lock()
	r.rec.CostUSD = (r.gwUsed + r.gwNotional + r.direct).USD()
	r.mu.Unlock()
}

// crossCheckCost warns when Claude Code's own figure for a stage and the
// gateway's differ by more than 5%. A stage that was stopped (by a halt or
// a cancel or its deadline) never got to report its cost, so its figure
// says nothing and nothing is compared.
//
// The gateway's side is its whole model figure -- the money and the notional
// part together -- because that is what Claude Code's own total_cost_usd
// counts: it prices every call the stage made, whatever credential paid for
// it, and a cascade stage's tier-A calls are in it (C1.16, F25). Comparing the
// billed part alone against it would read every cascade stage as a 100%
// disagreement and bury the cross-check's real finding.
func (r *run) crossCheckCost(stage string, claude float64, stopped bool) {
	r.mu.Lock()
	rep := r.lastStage
	r.mu.Unlock()
	if rep == nil || stopped {
		return
	}
	gw := (rep.Used + rep.Notional).USD()
	if larger := max(claude, gw); larger > 0 && abs(claude-gw) > 0.05*larger {
		r.d.Log.Warn("cost cross-check", "stage", stage, "claude_code_usd", claude, "gateway_usd", gw)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// watchHalt waits for the gateway's halt while a stage runs. On a halt it
// records it at once, then gives the agent haltGrace to exit by itself
// (done closes) before stopping the stage.
//
// What it records is the halt the stage hook made of the stop when the hook
// leaves one — the gateway's stage budgets, the review ratio and the fan-out
// are governance stops, and they leave the checkpoint report a governance stop
// leaves (T7.3) — and the gateway's own halt otherwise (gatewayStop). It is
// called on a goroutine of its own, which governanceHalt allows because the
// stage's own stopWatch joins this one before the stage's spend and history
// are written (halt.go).
func (r *run) watchHalt(stageCtx context.Context, done <-chan struct{}, stage string) {
	select {
	case gh, ok := <-r.gw.Halted():
		if !ok || gh.Reason == "" {
			return // delivered before
		}
		if !r.gatewayStop(stageCtx, stage, gh) {
			return // a cancel came first, and stops the stage itself
		}
		h := r.haltValue()
		if h == nil {
			return
		}
		t := time.NewTimer(haltGrace)
		defer t.Stop()
		select {
		case <-done:
		case <-stageCtx.Done():
		case <-t.C:
			r.cancelHaltedStage(*h)
		}
	case <-done:
	case <-stageCtx.Done():
	}
}

// drainHalt records a halt the watcher did not get to, once the stage's
// agent has returned.
func (r *run) drainHalt(ctx context.Context, stage string) {
	select {
	case gh, ok := <-r.gw.Halted():
		if ok && gh.Reason != "" {
			r.gatewayStop(ctx, stage, gh)
		}
	default:
	}
}

func gatewayHalt(gh gateway.Halt) runstore.Halt {
	scope := gh.Scope
	if scope == "" {
		scope = "run"
	}
	return runstore.Halt{Reason: runstore.HaltReason(gh.Reason), Scope: scope, At: gh.At.UTC(), Detail: gh.Detail,
		// The workspace's own marker travels with the halt and nowhere else
		// (gateway.Halt.WorkspaceRefusal): the gateway read it off the
		// refusal the workspace's credentials service answered, so this is
		// the service's statement about itself and not something the run
		// inferred. It is carried into the record verbatim, and the runner
		// decides nothing with it.
		WorkspaceRefusal: gh.WorkspaceRefusal}
}

// closeGateway stops the gateway, once, and takes the run's model cost from
// its ledger: the figure includes a call that settled after its stage's
// wait ran out. It also removes the managed settings file the run wrote.
func (r *run) closeGateway() {
	defer r.removeManagedSettings()
	r.mu.Lock()
	gw, closed := r.gw, r.gwClosed
	r.gwClosed = true
	r.mu.Unlock()
	if gw == nil || closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), gatewayCloseTimeout)
	defer cancel()
	if err := gw.Close(ctx); err != nil {
		r.d.Log.Warn("closing the gateway failed", "err", r.redact(err.Error()))
	}
	r.recordLedgerCost(gw.Ledger())
	r.mu.Lock()
	// The run's own model figure is the ledger's totals plus the stages the
	// ledger never saw, an account stage's own among them (P11, P12): the
	// ledger's figure alone would drop them from the record here, at the last
	// gateway write of the run, however carefully the stages kept them.
	spent := r.gwUsed + r.gwNotional + r.direct
	r.mu.Unlock()
	r.rec.CostUSD = spent.USD()
	r.updateCost()
	r.warnReportedGap()
}

// recordLedgerCost takes the run's cost figures from the gateway's ledger:
// the settled totals, the prompt-cache totals, the token counts by model, and
// the provider reports with the price table's band behind them. Every one of
// them is the ledger's and none is a sum of the stage reports, because the
// ledger holds calls no report does: a call that settled after its stage's
// wait ran out is in the ledger's figures and in no stage report at all, so a
// figure built by adding a report's to the ledger's would count that call
// twice (F24, G1.2, SA1.5).
func (r *run) recordLedgerCost(led gateway.Ledger) {
	r.mu.Lock()
	// The ledger's settled totals are the run's, and they are the ones
	// r.gwUsed and r.gwNotional keep: the money its calls paid and the
	// subscription part of them (C1.16, D140). The stages' own reports are
	// what the splits (model_by, route_by) are built from.
	r.gwUsed, r.gwNotional = led.Used, led.Notional
	// And the part of that money a fallback bought (C1, CS8, D140), which is
	// the ledger's alone for the same reason: a fallback call that settled
	// after its stage's report was taken is in the run's figure and in no
	// stage's.
	r.gwFallback = led.FallbackUSD
	r.gwCacheRead, r.gwCacheWrite = led.CacheReadTokens, led.CacheWriteTokens
	// The run's token counts by model are the ledger's, like the totals
	// beside them and for the same reason: a call that settled after its
	// stage's wait ran out is in them (G1.2, F8). The two are the same calls'
	// tokens, so a reader that sums these entries gets exactly those totals.
	r.modelTokens = modelTokenCounts(led.ModelTokens)
	// The provider reports and the table band behind them, by serving model,
	// with the calls that carried no report counted beside them (F24, D2650).
	// A Ledger is a snapshot, so these are the run's until it reads another.
	r.reportedBy = led.ReportedBy
	r.tableLoBy = led.TableLoBy
	r.tableHiBy = led.TableHiBy
	r.unreportedCalls = led.UnreportedCalls
	r.mu.Unlock()
}

// routingKnob reports whether the live test's knob skips the
// routing refusal. In the cloud, or against a test upstream, it never
// does: Deps.Local is the CLI's answer and its zero value is the cloud
// (design compute-backend.md §2.5), so a platform no backend claims
// refuses the knob too. The warning still says Cloud Run, the only cloud
// platform that can reach it today.
func (r *run) routingKnob() bool {
	if envLookup(r.d.Env, allowRoutingSettingsEnv) != "1" {
		return false
	}
	if !r.d.Local {
		if !r.warnedKnob {
			r.warnedKnob = true
			r.d.Log.Warn(allowRoutingSettingsEnv + " is ignored on Cloud Run")
		}
		return false
	}
	return r.d.GatewayUpstream == ""
}

// checkManagedDirWritable is the gateway's precondition: Claude Code's
// managed settings are what keep a repository's own settings from
// rerouting the agent, so a directory that can't hold them fails the run.
//
// It is the image's precondition, and a local invocation has no image: the
// path is Claude Code's own (agent.ManagedSettingsPath), a developer's
// machine may hold no such directory and may not be the developer's to
// write, and --managed-settings cannot be pointed elsewhere there
// (checkTestHooks). What the file is for still holds without it: the
// gateway's address and token reach the stage in its environment
// (agent.BuildEnv), which is the runner's own, and checkSettingsRouting
// below refuses any settings file that would send the agent elsewhere. So
// the directory is the cloud's to require, and a local run goes on
// without it — equally true of the write in writeManagedSettings, which
// warns rather than refusing.
func (r *run) checkManagedDirWritable() error {
	if r.d.Local {
		return nil
	}
	dir := filepath.Dir(r.managedPath())
	f, err := os.CreateTemp(dir, agent.ManagedSettingsTmpPattern)
	if err != nil {
		return fmt.Errorf("the image can't hold Claude Code's managed settings (%v); rebuild it on an M9a base image", err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// checkBudget is bootstrap's budget gate, after the project check and
// before the lock: a Vertex run that enforces is refused (a misconfiguration,
// so an infra_error), an api-key run that enforces without a cap halts (a
// policy, so a halt), and a run through the gateway needs prices for its
// models and — in the image, where Claude Code reads them — a managed
// settings directory it can write.
func (r *run) checkBudget(ctx context.Context) error {
	auth, s := r.cfg.Agent.Auth, r.spend
	// D3174: a run whose model path is the credentials service's is not
	// refused here — the service calls Vertex on its own identity, and the
	// gateway prices and settles the call as it does a key's — while a run
	// that authenticates as vertex itself spends the project's own GCP quota,
	// which nothing caps (vertexServedByTheService).
	if auth == "vertex" && s.Mode == "enforce" && !r.vertexServedByTheService() {
		return errors.New(VertexBudgetRefusal)
	}
	// A pool one of the run's crafts goes out on that lists the built-in
	// Vertex credential is refused here for VertexBudgetRefusal's own reason:
	// those calls are the project's own GCP service, so the run above this
	// line's own case cannot cap them either (D1626, F8) — unless this run's
	// Vertex is the service's, which is the same lift and is read in
	// vertexPoolRefusal.
	if s.Mode == "enforce" {
		if reason := r.vertexPoolRefusal(ctx); reason != "" {
			return errors.New(reason)
		}
	}
	// With the backend the caps are the database's too, and the session has
	// already judged them (Admit): an enforcing run with none halted there.
	if auth == "api-key" && s.Mode == "enforce" && s.Cap <= 0 && r.sess == nil {
		r.haltNow(runstore.Halt{Reason: runstore.HaltNoCap, Scope: "run", At: r.d.Now().UTC(),
			Detail: r.noCapDetail()})
		return &HaltError{*r.haltValue()}
	}
	// A model another provider serves needs the owner's approval of this
	// repository and api-key auth, whatever the mode: refused before any
	// call, naming the rule.
	if err := r.checkProviderModels(); err != nil {
		return err
	}
	// A pool every member of which serves the stage's model as written
	// (F23, D1637): judged here, before any stage, and not at the stage that
	// would find out.
	if err := r.checkPoolModels(ctx); err != nil {
		return err
	}
	// The allow-list bounds the models whatever the credential or the mode. The
	// switch and the routes are the run's own: a route ID is admitted only
	// where the switch routes the model to it, which is the resolution
	// applyRouting performed a step ago (resolvePolicy). A review level's own
	// model is judged here as the checks above judge it, and for the same
	// reason: the list bounds the models a run may use, and a level model is a
	// model the run would use.
	if ps := r.checkModels(func(a config.Agent) []config.Problem {
		return config.CheckAllowed(a, r.policy, r.ceilingAllowed(), r.routingSwitch(), r.modelProviders())
	}); len(ps) > 0 {
		return refusal("the models are not all allowed", ps)
	}
	if !r.gatewayOn() {
		return nil
	}
	priced := func(a config.Agent) []config.Problem { return config.CheckPins(a, s.Prices) }
	if ps := r.checkModels(priced); len(ps) > 0 {
		return refusal("the models can't be priced under the budget", ps)
	}
	// The run's images, judged with the pins above: a coder that can't read
	// one is refused here, before any stage spends, when the task names a
	// picture — and warned about when the repository alone tracks one (V5).
	if err := r.checkImages(ctx); err != nil {
		return err
	}
	for _, w := range config.PinWarnings(r.cfg.Agent, s.Prices) {
		r.d.Log.Warn("model pin: "+w.String(), "code", w.Code)
	}
	if catalogStale(s.Prices, r.cfg.Agent.PinnedModels()) {
		// One line per run, whatever the mode: the models this run is pinned
		// to are charged the dearest end of their tier's range, and
		// result.json says what was charged once the run is over (design §7,
		// P7). A stale row no pin names is not this run's business: the routes
		// may have materialized one, and a run that never calls it, or prices
		// it by hand, over-charges nothing.
		r.d.Log.Warn("budget: price catalog stale", "code", "catalog_stale")
	}
	if err := r.checkManagedDirWritable(); err != nil {
		return err
	}
	if reason := r.checkSettingsRouting(); reason != "" {
		return errors.New(reason)
	}
	return nil
}

// warnReportedGap logs, once, when the providers' own cost figures leave the
// price table's band by more than reportedGapPct, which is the comparison
// `fugaro diagnose` makes on the same run (F16, D2640): a complete call on a
// provider route settles on the report, so a report read against the charge
// agrees with it by construction, and the band -- what the table would have
// charged the calls that reported, at its selected tier's two ends, with no
// route fee, which providers don't report -- is what a gap is real against.
// Above the band the table's prices are low, or a catalog-priced route served
// an endpoint dearer than the tier's range. Below it the provider charged less
// than the tier's cheapest end, where the call is floored (D2651), so the
// charge the caps see is the table's figure rather than the provider's: the
// gap is the table or an override priced above the endpoints actually served,
// or a provider under-reporting a bill the floor bounds. A run whose calls
// carried no band at all keeps the older comparison against the charge without
// the fee, and only above it: with no band a low report may be partial
// reporting (a cut stream, a call without a cost).
func (r *run) warnReportedGap() {
	r.mu.Lock()
	reported := r.reported
	lo, hi := sumMicros(r.tableLoBy), sumMicros(r.tableHiBy)
	var charged pricing.Micros
	for name, v := range r.routeBy {
		// The provider reports the model's price, not Fugaro's route fee.
		charged += pricing.WithoutFee(v, r.modelProviders()[name].RouteFeePct)
	}
	r.mu.Unlock()
	if reported <= 0 {
		return
	}
	switch {
	case hi > 0 && float64(reported) > float64(hi)*(1+float64(reportedGapPct)/100):
		r.d.Log.Warn(fmt.Sprintf("reported cost: the providers reported $%.4f, more than %d%% above the $%.4f the table would have charged the calls they reported at the tier's dearest end: check model_prices and the account's provider settings", reported.USD(), reportedGapPct, hi.USD()), "code", "reported_gap")
	case lo > 0 && float64(reported) < float64(lo)*(1-float64(reportedGapPct)/100):
		r.d.Log.Warn(fmt.Sprintf("reported cost: the providers reported $%.4f, more than %d%% below the $%.4f the table would have charged those calls at the tier's cheapest end: check model_prices and the account's provider settings", reported.USD(), reportedGapPct, lo.USD()), "code", "reported_gap")
	case lo == 0 && hi == 0 && float64(reported) > float64(charged)*(1+float64(reportedGapPct)/100):
		r.d.Log.Warn(fmt.Sprintf("reported cost: the providers reported $%.4f, more than %d%% over the $%.4f charged: check model_prices and the account's provider settings", reported.USD(), reportedGapPct, charged.USD()), "code", "reported_gap")
	}
}

// reportedGapPct is the margin (percent) a provider's reported cost may leave
// the table's band -- or, for a run whose calls carried none, exceed the
// charge without the route fee -- by before the run warns.
const reportedGapPct = 10

// modelAgent is one model the run carries that is not one of its four pins, as
// the model checks see it: a copy of the run's agent whose role slot carries
// the model, plus the two keys a problem about it is named by — the key the
// owner wrote it under, and the slot the check read it from.
type modelAgent struct {
	agent config.Agent
	path  string
	slot  string
}

// levelAgents are the review levels' own models as the model checks see them
// (modelAgent): one per level whose own model is not already one of the run's
// pins. A level that runs its role's model has nothing of its own to judge, and
// a level model that is a pin is judged as that pin.
func (r *run) levelAgents() []modelAgent {
	a := r.cfg.Agent
	pinned := a.PinnedModels()
	var out []modelAgent
	for _, s := range reviewStages {
		m := r.levelModel(s.level)
		if m == "" || slices.Contains(pinned, m) {
			continue
		}
		la := modelAgent{agent: a, path: levelModelPath(s.level)}
		if s.role == config.RoleReviewer {
			la.agent.Models.Reviewer, la.slot = m, reviewerPinPath
		} else {
			la.agent.Models.Coder, la.slot = m, coderPinPath
		}
		out = append(out, la)
	}
	return out
}

// diagnoseAgents is the diagnose stage's own model as the model checks see it
// (modelAgent): one entry when this run executes the diagnose step, under the
// key the owner wrote it or, when no layer wrote one, the built-in default this
// run would call (diagnoseModel).
//
// It is judged exactly as a pin is, and for the same reason the level models
// are: the list bounds the models a run may use, and the diagnose model is a
// model this run calls. What the stage sends it is the evidence a failure left
// behind — a log, a doctor report, the changed files' names — so where that may
// go is the owner's decision and not a decision about role slots: a project
// whose allow-list leaves the diagnose model off, or whose provider does not
// name this repository in allow_data_to, refuses the run at bootstrap rather
// than sending a person's failure logs to an endpoint the policy never
// approved. A model that is already one of the run's pins is left out, as a
// level's is: its own pin's judgement is the same one.
func (r *run) diagnoseAgents() []modelAgent {
	if !r.diagnose {
		return nil
	}
	a := r.cfg.Agent
	m := r.stageModel(stageDiagnose)
	if slices.Contains(a.PinnedModels(), m) {
		return nil
	}
	ma := modelAgent{agent: a, path: diagnoseModelPath, slot: coderPinPath}
	ma.agent.Models.Coder = m
	return []modelAgent{ma}
}

// modelAgents are every model the model checks judge beyond the run's own
// pins: each review level's own model (levelAgents), and the diagnose stage's
// when this run is a cloud diagnosis (diagnoseAgents).
func (r *run) modelAgents() []modelAgent {
	return append(r.levelAgents(), r.diagnoseAgents()...)
}

// checkModels runs one of the model checks over the run's own pins and then
// over every other model the run carries (modelAgents), renaming a problem
// about one of those to the key the owner wrote it under
// (agent.levels.l3.model, agent.models.diagnose) rather than the slot the
// check read it from. A problem under any other path is the run's own, which
// the first call has already reported: the copy of the agent differs from the
// run's in that one slot and nothing else.
func (r *run) checkModels(check func(config.Agent) []config.Problem) []config.Problem {
	ps := check(r.cfg.Agent)
	for _, ma := range r.modelAgents() {
		for _, p := range check(ma.agent) {
			if p.Path != ma.slot {
				continue
			}
			p.Path = ma.path
			ps = append(ps, p)
		}
	}
	return ps
}

// levelModelPath is the configuration key a level's own model is written
// under, which a problem about it names: policy's level names (l1, l2, l3)
// are that key's own middle word.
func levelModelPath(level policy.Level) string {
	return "agent.levels." + string(level) + ".model"
}

// checkProviderModels is the owner's data policy for provider models
// (config.CheckProviderPolicy, config.CheckProviderAuth). The run's own
// allowed_models layer is the file's here; the merged list is CheckAllowed's.
func (r *run) checkProviderModels() error {
	var allowed []string
	if b := r.cfg.Budget; b != nil {
		allowed = b.AllowedModels
	}
	prices := r.spend.Prices
	if prices == nil {
		// The budget is off, so the ceiling loaded no table; the checks
		// below still judge the prices the run would charge, built the
		// same way.
		var err error
		if prices, err = r.priceTable(nil); err != nil {
			return fmt.Errorf("pricing the models: %w", err)
		}
	}
	// The four checks below judge an Agent's pins, and a review level's own
	// model is a pin of its stage: checkModels runs each of them over the
	// level models too, so a model a level 3 names passes the same data
	// policy, the same auth rule, the same gateway rule and the same price
	// rule the coder's does. Without it a provider whose allow_data_to does
	// not name this repository could serve a level, and the repository's code
	// would be sent there (L4.5).
	ps := r.checkModels(func(a config.Agent) []config.Problem {
		return config.CheckProviderPolicy(a, allowed, r.spec.Repo, r.modelProviders(), prices)
	})
	ps = append(ps, r.checkModels(func(a config.Agent) []config.Problem {
		return config.CheckProviderAuth(a, r.modelProviders())
	})...)
	if len(ps) > 0 {
		return refusal("the models are not approved for this repository", ps)
	}
	// From here the pins are the run's: an unset background model becomes
	// the provider coder's, so that CheckPins, CheckAllowed, the gateway's
	// stage pins and Claude Code's haiku role all see the same one.
	bg := r.backgroundModel()
	mode := policy.ModeOff
	if r.gatewayOn() {
		mode = r.spend.Mode
	}
	if ps := r.checkModels(func(a config.Agent) []config.Problem {
		return config.CheckProviderGateway(a, bg, r.modelProviders(), mode)
	}); len(ps) > 0 {
		// The first problem's own sentence, as this check has always
		// reported; the class is what the caller reads.
		return &modelRefusal{msg: ps[0].Message, coderPin: namesCoderPin(ps)}
	}
	r.cfg.Agent.Models.Background = bg
	return nil
}

// noCapDetail says how to give an enforcing run a cap. Where the owner's
// ceiling asked for enforce, the cap belongs in the project config; where a
// repository's fugaro.yaml escalated the mode, either place will do.
func (r *run) noCapDetail() string {
	const base = "budget.mode is enforce but no per-run cap is set: set budget.per_run_usd in the project config and run fugaro update"
	switch r.policy.Sources[policy.KeyMode] {
	case policy.SourceCeiling:
		return base
	case policy.SourcePolicy:
		// The ceiling is the project's published policy: fugaro update copies
		// the local config's value into the jobs, and would not change what
		// this run read.
		return "budget.mode is enforce but no per-run cap is set: set budget.per_run_usd in the project's policy (" + projectpolicy.PathShared() + "), which is what the run enforces"
	}
	return "budget.mode is enforce (set in fugaro.yaml) but no per-run cap is set: set budget.per_run_usd in fugaro.yaml on the default branch, or in the project config and run fugaro update"
}

// ceilingAllowed is the owner's allow-list from the run's ceiling, nil when
// none was set: error messages name it next to the merged one. The ceiling is
// the job environment's copy of the config, or the project's published policy
// when it has one (resolvePolicy).
func (r *run) ceilingAllowed() []string { return r.ceiling.AllowedModels }
