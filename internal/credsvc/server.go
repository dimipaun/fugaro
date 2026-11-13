package credsvc

// The service's own HTTP surface (C1.11): the two Anthropic paths a run's
// gateway calls, the identity it verifies them with, and the answer it gives.
//
// It has no admin surface at all (D143): no route reads a mark, clears one,
// sets a switch or lists an account, because every one of those is the
// operator's and lives in internal/cli (C1.21). What is not routed cannot be
// reached by a caller who is not an operator, which is a stronger guarantee
// than a route that checks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/task"
)

// The two Anthropic paths the service serves, spelled as Anthropic spells them:
// the in-runner gateway forwards a run's request here instead of to
// api.anthropic.com, so the path is the one the SDK built. The mint's and the
// sign route's paths are this service's own contract and are declared beside
// the routes themselves (github.go, verdict.go).
const (
	MessagesPath    = "/v1/messages"
	CountTokensPath = "/v1/messages/count_tokens"
	// SignPath is the path a review run signs its artifact's digest on
	// (C3.9e). It is not an Anthropic path: the request and the answer are
	// this service's own contract.
	SignPath = "/v1/review/sign"
	// VerifyPath is the path a coordinator asks whether a verdict's signature
	// is one this service made (C3.9f). It is the sign route's counterpart and
	// is served to a caller class of its own: a verifier holds no run identity,
	// so this path is answered ahead of the shared request parse that every
	// other route is read by (ServeHTTP).
	VerifyPath = "/v1/review/verify"
)

// routeAct is the ONE rule for the two facts an audit line carries about the
// call rather than about what the service did with it: the purpose the call
// asked for, and the credential name a line for it may carry (C1.13).
//
// It exists so the rule is written once. The routes are four acts of the
// service — a proxied model call (C1.11), a mint (C2.1), a signature over a
// review run's digest (C3.9e) and an answer about one (C3.9f) — and every
// refusal a route answers has to be
// recorded as the act the run asked for and not as the branch it was answered
// in, whether that refusal is taken before the route is branched on (the
// binding and the stop switches in ServeHTTP) or inside the route's own code
// (the mint's own refusals, github.go's refuseMint and auditMint). Two
// spellings of one rule is how they drift: a mint's refusals named the App key
// while the stop-switch refusal named nothing until this rule replaced both.
//
// The credential answers for the mint because the App key is what the endpoint
// issues from: it is the endpoint's own name for it and not something read out
// of the bundle, so it is known before the bundle is read and after it alike,
// and a mint's line that named no credential would be a mint an operator
// cannot account for. A model call's line names none here, and so do a sign's
// and a verify's: which credential a model call used, or would have used, is the
// cascade's answer and not the route's, and a sign or a verify line's credential
// is an account's review-signing key — a name the route cannot read off a path,
// because it is the CALLER'S account (a sign's) or the account the caller NAMED
// (a verify's). Either way a refusal taken before one was chosen has no name to
// carry.
func routeAct(path string) (purpose, credential string) {
	switch path {
	case MintPath:
		return PurposeMint, config.GitHubAppKeySecret
	case VerifyPath:
		// A verify line's credential is the named account's review-signing key
		// — the key the answer was computed with — and, like the sign route's,
		// that name is not in the path. The handler fills it in once the
		// bundle has it; a refusal answered before then carries none.
		return PurposeReviewVerify, ""
	case BitbucketReadPath:
		// The read mint's credential is the read consumer, which is the
		// endpoint's own name for what it issues from and not a value read out
		// of the bundle (BB4, B1): a line for it is written before the bundle is
		// read and after it alike, and an operator reading one can tell a read
		// mint from the App key's mint without a second field.
		return PurposeBitbucketRead, BitbucketReadConsumerSecret
	case GitHubReadPath:
		// The GitHub read mint issues from the SAME credential a mint does —
		// the App key in the bundle — and names it here for the same reason: it
		// is the endpoint's own name for what it signs with, known before the
		// bundle is read and after it alike. The purpose is what tells the two
		// acts apart, and it is the field an operator greps: a person's read is
		// not a run's mint even though both are signed with the one key.
		return PurposeGitHubRead, config.GitHubAppKeySecret
	case MergeMintPath:
		// The merge mint issues from the SAME credential the two mints above
		// issue from — the App key in the bundle — and names it here for their
		// reason: it is the endpoint's own name for what it signs with, known
		// before the bundle is read and after it alike. The purpose is what tells
		// the three acts apart (E16, D2049): a daemon's merge token is not a
		// run's mint and not a person's read, even though all three are signed
		// with the one key.
		return PurposeMergeMint, config.GitHubAppKeySecret
	case SlipPath:
		// A grant's line names the launcher and no credential: the act is a
		// request for a run's grant and not an issuance, so there is no name of
		// a credential it could carry — the slip it writes holds names, and
		// which of them a run is later served is another route's answer
		// (DS1.6). Like the verify and read-mint purposes, its line is written
		// for a caller that is not a run of this installation at all
		// (callerWithoutJobPurpose).
		return PurposeSlipGrant, ""
	case SlipClaimPath:
		// A claim's line is an ordinary job-class one and names no credential,
		// for the grant's reason: the claim answers a claim secret and issues
		// no credential of the cascade, so there is no name to pair with a
		// tier — and naming one would be recording a fact the route does not
		// have.
		return PurposeSlipClaim, ""
	case SignPath:
		// The sign route's credential is the account's review-signing key
		// (verdict.go), and THAT NAME IS NOT IN THE PATH: it is the caller's
		// account, which the pairing resolves, so the route names the purpose
		// and leaves the credential to the handler — which fills it in from
		// the account once the key is in hand, the same way the cascade fills
		// in a model call's. A refusal answered before the handler has no
		// account to name and so carries none.
		return PurposeReviewSign, ""
	}
	if strings.HasPrefix(path, BitbucketProxyPrefix) {
		// The Bitbucket proxy route (B1.5), and the ONE route whose two facts
		// this rule cannot read off the path. Which of the two purposes the
		// call asked for — git or the Bitbucket API — is the classification's
		// answer (internal/gitproxy's Classify, the one reading of a path this
		// service has), and a rule that guessed between them from the segments
		// would be a second reading of a path, which is exactly what this
		// design refuses everywhere else (G13). The credential is the bundle's
		// Bitbucket token whichever way it goes, so it is known here and the
		// route's handler names it on every line it writes.
		//
		// What is left for the refusals answered ABOVE the classification —
		// the binding of the two identities and the operator's stop switches —
		// is therefore no purpose and no credential: the service had not yet
		// decided what the call was, and a line that named one of the two would
		// be recording a fact it did not have.
		return "", ""
	}
	return PurposeForward, ""
}

// servedRoute reports whether this service serves the call at all: the paths
// that are this service's own contract, and the run's Bitbucket traffic under
// its prefix (B1.5). A call it does not serve is a 404 and nothing else, which
// is how D143's "no admin surface" is kept — a route that does not exist cannot
// be reached by a caller who is not an operator.
//
// The methods are part of the rule and not a blanket, and they are the methods
// the calls under those paths actually use: POST for every path of this
// service's own contract (each of them a body-carrying call), and GET, POST and
// PUT under the prefix. The three are what the classified calls need — git's
// info/refs GET and its two POSTs, and the REST allow-list's GETs, its create
// and comment POSTs and its update PUT (G4 allows "update", and
// bitbucket.Provider's own ApplyReady and EnsurePR send it) — and nothing else
// is admitted, so a DELETE, a PATCH or a HEAD under the prefix is a route this
// service does not have.
//
// What a method is worth is decided once: the classification refuses a method a
// call does not take (call_not_allowed), so this gate is only about which
// requests have a route at all, and the two do not disagree — the prefix admits
// every method the classifier can name and no other.
func servedRoute(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, BitbucketProxyPrefix) {
		switch r.Method {
		case http.MethodGet, http.MethodPost, http.MethodPut:
			return true
		}
		return false
	}
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case MessagesPath, CountTokensPath, MintPath, SignPath, VerifyPath, BitbucketReadPath, GitHubReadPath, MergeMintPath, SlipPath, SlipClaimPath:
		return true
	}
	return false
}

// JobAccount is one job service account and the repository whose runs are
// launched as it. It is the binding the service checks a run token against
// (review finding N1): a run token names a repository slug, a job token names
// the service account it was minted for, and only a pair that a deployment
// published together is one run.
//
// Repo is the canonical repository name (projectpolicy.Canonical) the
// account's runs belong to. It is what the tier-A admission is checked
// against, because the published policy speaks canonical names and the run
// token speaks a slug.
type JobAccount struct {
	Email string
	Slug  string
	Repo  string
}

// RepoJob is one repository's job service account, as the deployment knows
// it. It is the input of JobAccountsFor, which turns a deployment's shape into
// the flat table the service reads.
type RepoJob struct {
	// Repo is the repository, spelled as the operator spells it ("acme/app");
	// JobAccountsFor canonicalises it.
	Repo string
	// Workflow is the workflow whose job runs as this account. A repository
	// has one job per workflow, each with its own service account.
	Workflow string
}

// JobAccountsFor builds the binding table for an installation's
// per-repository jobs: every repository's every workflow's job service
// account, paired with the repository whose runs are launched as it.
//
// The account's ID is derived from the slug and the workflow
// (gcp.ServiceAccountID), which is the one derivation the deployer uses to
// create the account, and the slug is derived from the provider and the
// repository (task.Slug), which is the one derivation the runner uses to name
// a run's nodes — so the slug in this table is the slug a run token carries
// and not a second spelling of it. Both derivations happen here rather than
// being taken as arguments, because a table an operator typed is a table that
// can disagree with the accounts that exist.
//
// The build account is deliberately not in the table: it never calls the
// service. A caller whose email is in no row is refused, so an installation
// that passes nothing here serves nobody (NewServer refuses the empty table).
func JobAccountsFor(fugaroProject, gcpProject, provider string, jobs []RepoJob) ([]JobAccount, error) {
	out := make([]JobAccount, 0, len(jobs))
	for _, j := range jobs {
		if j.Workflow == "" {
			return nil, fmt.Errorf("credsvc: job accounts for %q: a job with no workflow", j.Repo)
		}
		slug, err := task.Slug(provider, j.Repo)
		if err != nil {
			return nil, fmt.Errorf("credsvc: job accounts for %q: %w", j.Repo, err)
		}
		canonical, err := projectpolicy.Canonical(provider, j.Repo)
		if err != nil {
			return nil, fmt.Errorf("credsvc: job accounts for %q: %w", j.Repo, err)
		}
		out = append(out, JobAccount{
			Email: serviceAccountEmail(gcp.ServiceAccountID(slug, j.Workflow), gcpProject),
			Slug:  slug,
			Repo:  canonical,
		})
	}
	return out, nil
}

// SharedJobAccountsFor computes the service accounts of the installation's
// shared project jobs: one per (profile, provider, account) the policy
// publishes, derived exactly as internal/infra/projectjobs.go deploys them
// (gcp.ProjectServiceAccountID over config.ProjectJobAccounts' axis, the two
// functions the deployer, the launcher and the runner share). It is what
// C1.10's verifier refuses a run in a shared project job with — Global
// Constraint 6's gate, which holds until C3.9 — and it is computed from the
// policy rather than from a list, so the accounts the service refuses are the
// accounts the deployer created and not a second list that can drift.
// TestSharedJobAccountsAreTheNamesProjectJobsDeploys builds one installation
// both ways and requires the two sets to be equal, which is what makes the
// claim a test rather than a comment.
//
// The account's ID is the profile's own triple and holds no Fugaro project
// name: a project job's service account is named after the job it runs, which
// is what makes it the signal a caller cannot forge (the account a run is in
// is the account Cloud Run minted its ID token for). The email's domain is the
// GCP project, which is the only place the installation's name enters.
//
// A policy that publishes no profile yields nothing: an installation with no
// shared job has no account to refuse.
func SharedJobAccountsFor(p *projectpolicy.Policy, gcpProject string) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, name := range p.ProfileNames() {
		pr, err := p.ProfileFor(name)
		if err != nil {
			// A profile the policy itself cannot read is not a shared job's
			// account. Decode refuses one, so this is unreachable through a
			// published node, and a reader that cannot be given a bad profile
			// must not invent an account name from it.
			continue
		}
		for _, account := range config.ProjectJobAccounts(pr.JobProfile) {
			out = append(out, serviceAccountEmail(gcp.ProjectServiceAccountID(name, pr.Provider, account), gcpProject))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// serviceAccountEmail is a service account ID as the email a Google ID token
// names it by.
func serviceAccountEmail(id, gcpProject string) string {
	return id + "@" + gcpProject + ".iam.gserviceaccount.com"
}

// Config is everything one service needs. Everything but Log and Now is
// required, and NewServer refuses a configuration that is missing one: a
// service that starts without a verifier, a bundle or a policy is a service
// that answers every call with something, and none of those somethings is
// what its caller must be told.
type Config struct {
	// Auth verifies the two identities a call carries (C1.10).
	Auth Verifier
	// Bundles loads the one bundle secret (C1.10's Q6 answer).
	Bundles *BundleLoader
	// Policy reads the published policy: the shared node and a repository's
	// own (C1.2).
	Policy PolicyReader
	// State is the cascade's shared state under creds/.
	State creds.Store
	// Kills reads the operator's two stop switches.
	Kills KillReader
	// DB is the one database write the service makes outside the cascade: the
	// removal of an ended run's placement (the N4 cleanup). It is separate from
	// State because it is not a selection's business — creds.Store has no
	// delete, deliberately, so that no selection can drop another call's mark.
	DB Patcher
	// Probes is the conditional write the probe's one-probe-at-a-time lease
	// needs (C1.12). It is separate from DB for the same reason DB is separate
	// from State: it is a different node and a different fact — a claim on
	// probing a credential, which lapses on its own and is never deleted.
	// *rtdb.Client is one.
	Probes ProbeLeases
	// Slips is the grant a launcher asks for and the claim a run makes once
	// (DS1.1, DS1.3): its own node, its own lifetime and its own store, which
	// is why it is neither State's nor DB's business — a slip is written before
	// a run exists and read on every call the run makes, and no selection has
	// anything to say about it. *creds.RTDBStore is one.
	//
	// It is required at construction, like State and Probes and for a stronger
	// reason than either: a service that cannot write a slip cannot grant a run
	// its credentials at all, so it is not a service this build starts.
	Slips creds.SlipStore
	// Signs is the create-only write the one-signature mark is taken with
	// (C3.9e, D1074): the sign route's own node, which is why it is neither
	// DB's business nor State's. It may be nil, and a nil one is a service
	// that signs nothing — the route refuses rather than answering a
	// signature the mark would have bounded, and the refusal says so. It is
	// not required at construction because an installation that deploys no
	// review job signs nothing and is a service this build starts.
	// *rtdb.Client is one.
	Signs SignMarks
	// AuditLog is where the service's audited acts are written (C1.13): every
	// line this service writes, the mint's (C2.1) and the probe's (C1.12)
	// included. There is one writer and one handler — Server.audit is the ONLY
	// auditor this service holds — so no act of it has a log of its own and an
	// operator reads one stream. The service builds its auditor from this
	// handler, armed with the bundle loader's own redaction (NewProbeAuditor),
	// so a credential's value cannot reach a line: the arming is not a caller's
	// to forget.
	//
	// It is a handler and not a logger, deliberately — see NewAuditor: a logger
	// carries whatever it was built with, and an attr or a group on an audit
	// line is a field the contract does not have.
	AuditLog slog.Handler
	// JobAccounts binds a job service account to the repository whose runs
	// are launched as it. It is the table the run-to-job binding is checked
	// against (N1), so an empty one refuses every call.
	JobAccounts []JobAccount
	// ReviewAccounts are the installation's review jobs (C3.9a): the same rows
	// AuthConfig.ReviewJobAccounts holds, and the reason they are here as well
	// is that the pairing needs the account NAME — the name the published
	// admissions speak — where the verifier needs the class. A caller the
	// verifier answered as a review one whose email is not in this table is
	// refused (pairReview): the two lists are configured together, so a
	// disagreement is a deployment that cannot serve the class at all.
	//
	// It may be empty, and an empty one is the same statement an empty
	// SharedJobAccounts makes: this installation has no review job, so no
	// caller of that class exists.
	ReviewAccounts []ReviewJobAccount
	// Repos is the installation's repository table (RepositoryTableFor): the
	// slug a run identity carries to the canonical repository name the
	// published policy speaks. It is what pairs a review caller with a run,
	// because a review job's account names no repository (review.go), and it
	// is deliberately the same derivation the per-repository accounts are
	// bound by rather than a second spelling of it. A slug it does not hold is
	// refused, never guessed (D1066).
	Repos map[string]string
	// Client is the HTTP client the upstream call is made with. nil is a client
	// with the service's own bounds. Either way it is put through
	// upstreamClient, so the client that carries a credential never follows a
	// redirect: a caller cannot hand this service a client that would.
	Client *http.Client
	// Log is where the service's own lines go. nil is slog.Default().
	Log *slog.Logger
	// Now is the clock. nil is time.Now.
	Now func() time.Time
}

// Verifier is the part of C1.10's authenticator the service calls.
type Verifier interface {
	Verify(ctx context.Context, req Request) (Identity, error)
	// VerifyVerifier admits the caller of the verify route, which presents a
	// token and no Request at all (C3.9f): the path is answered ahead of
	// RequestFromHTTP, so the two identity headers a Request is built from are
	// not there to read.
	VerifyVerifier(ctx context.Context, tok string) (string, error)
	// VerifyReadCaller admits the caller of the Bitbucket read mint, which
	// presents a token and no Request for VerifyVerifier's reason and no new one
	// (BB4, B2): the path is answered ahead of RequestFromHTTP. It is the same
	// check against two more lists — the installation's people and its
	// coordinator (BB5) — and the classes are told apart by the list their
	// caller is in rather than by a flag on a route.
	VerifyReadCaller(ctx context.Context, tok string) (string, error)
	// VerifyPersonCaller admits the caller of the GitHub read mint
	// (githubread.go). It is VerifyReadCaller's mechanism against ONE list —
	// the installation's people — and that is the whole of the difference: the
	// GitHub read is the person's alone, because the coordinator job holds the
	// App credential itself and mints its own read-only token with it.
	VerifyPersonCaller(ctx context.Context, tok string) (string, error)
	// VerifyOrchestrator admits the caller of the merge mint (mergemint.go,
	// E16): the installation's orchestrator daemon, and nothing else. It is a
	// class of its own against one more list — the orchestrator accounts — and
	// its audience is this service's own URL alone: the daemon mints for the
	// service with --audiences, and the token a person's own credentials produce
	// (GcloudAudience) is refused here whatever list it was matched against.
	VerifyOrchestrator(ctx context.Context, tok string) (string, error)
	// VerifyLauncher admits the caller of the grant route (DS1.3, D1938), which
	// presents a token and no Request for the same reason as the three above:
	// the path is answered ahead of RequestFromHTTP. A launcher is a person on
	// the installation's roster, and what the grant needs of the token beyond
	// the address is the numeric `sub` — the owner a per-user credential's name
	// is checked against (R7) and the identity the slip records (DS1.1).
	VerifyLauncher(ctx context.Context, tok string) (LauncherCaller, error)
}

// PolicyReader is the published policy as the service reads it: the project's
// shared node, and one repository's own node by slug.
type PolicyReader interface {
	Shared(ctx context.Context) (*projectpolicy.Policy, bool, error)
	Admissions(ctx context.Context, slug string) (*projectpolicy.RepoPolicy, bool, error)
}

// KillReader is the operator's stop switches as the service reads them.
//
// Stop reads the switches its caller honours, and the slug is what says which:
// a route that names a repository passes it and gets budget's pair (the global
// switch and that repository's own), and a route that names a person and no
// repository passes "" and gets the global switch alone. The empty slug means
// exactly that and is not a wildcard: a caller that has a repository always
// passes it, because the two reads are two, and the reader that builds a
// repository's path refuses an empty slug rather than answer "is this
// repository stopped" by leaving the repository's switch out of the read
// (budget.ReadKills).
type KillReader interface {
	Stop(ctx context.Context, slug string) (on bool, which string, err error)
}

// Patcher is the one database write the ended-run cleanup makes: several
// locations under one root in one atomic request, with nil deleting.
// *rtdb.Client is one.
type Patcher interface {
	Patch(ctx context.Context, root string, updates map[string]any) error
}

// RTDBPolicy is the published policy read from the database as the service's
// own account: the shared node, and one repository's node by slug. Decode is
// C1.2's reader, so a node this service reads is a node the publish path
// wrote.
type RTDBPolicy struct {
	// DB is the database; *rtdb.Client is one.
	DB Getter
}

func (p RTDBPolicy) Shared(ctx context.Context) (*projectpolicy.Policy, bool, error) {
	var raw json.RawMessage
	found, err := p.DB.Get(ctx, projectpolicy.PathShared(), &raw)
	if err != nil || !found {
		return nil, false, err
	}
	pol, _, err := projectpolicy.Decode(raw)
	return pol, true, err
}

func (p RTDBPolicy) Admissions(ctx context.Context, slug string) (*projectpolicy.RepoPolicy, bool, error) {
	var raw json.RawMessage
	found, err := p.DB.Get(ctx, projectpolicy.PathRepo(slug), &raw)
	if err != nil || !found {
		return nil, false, err
	}
	rp, _, err := projectpolicy.DecodeRepo(raw)
	return rp, true, err
}

// RTDBKills is the operator's stop flag read from the database: budget.ReadKills
// for a route that names a repository (the pair a run honours and `fugaro budget
// kill` writes) and budget.ReadGlobalKill for one that names a person and no
// repository (KillReader.Stop's empty slug). Either way the service's stop flag
// is the switch an operator already has, and an unreadable switch is ON in both
// readers because the rule is one rule in one place (budget's fail-closed
// decode).
type RTDBKills struct {
	// DB is the database; *rtdb.Client is one.
	DB budget.KillGetter
}

func (k RTDBKills) Stop(ctx context.Context, slug string) (bool, string, error) {
	var (
		kills budget.Kills
		err   error
	)
	if slug == "" {
		// A person-facing route: the two git reads and the read mint name a
		// person and no repository, and the global switch alone stops them.
		kills, err = budget.ReadGlobalKill(ctx, k.DB)
	} else {
		kills, err = budget.ReadKills(ctx, k.DB, slug)
	}
	if err != nil {
		return false, "", err
	}
	on, which := kills.On()
	return on, which, nil
}

// Server is the credentials service: one http.Handler.
type Server struct {
	cfg  Config
	log  *slog.Logger
	now  func() time.Time
	jobs map[string]JobAccount
	// review is Config.ReviewAccounts by folded email: the same lookup the
	// verifier makes (NewAuthenticator folds the same way), held here because
	// the pairing and the cascade's offering rule both need the account name
	// rather than the class.
	review map[string]ReviewJobAccount
	audit  *Auditor
	// slips holds the slip bodies this service has verified, for the five
	// minutes R4 gives them (slipCache). It is this service's own memory and
	// not the store's: the store is what the record IS, and this is what one
	// process last read of it.
	slips *slipCache
	// mergeMints is the merge mint's per-repository rate limit (E16, D2049). It
	// is this service's own memory, like the slip cache, and for a stronger
	// reason: what it bounds is a daemon looping on one endpoint, and a node
	// under creds/ would be a second writer of a fact nothing else reads.
	mergeMints *mergeMintRate
}

// NewServer validates cfg and returns the service.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Auth == nil {
		return nil, errors.New("credsvc: the service has no verifier")
	}
	if cfg.Bundles == nil {
		return nil, errors.New("credsvc: the service has no bundle loader")
	}
	if cfg.Policy == nil {
		return nil, errors.New("credsvc: the service has no policy reader")
	}
	if cfg.State == nil {
		return nil, errors.New("credsvc: the service has no credential state")
	}
	// The state is read through this service's own reader (stateReads), which
	// labels the read that failed as it fails: a selection makes three reads of
	// the store — every credential's mark, every lease and the run's own
	// placement — and answers a failure with one type and one sentence, so
	// without the label the operator's line cannot say which node to look at
	// (refusallog.go). It is wrapped here rather than at each reader so that no
	// path can hold the store unlabelled, and it changes nothing else about it:
	// every method answers what it answered before.
	cfg.State = &stateReads{Store: cfg.State}
	if cfg.Kills == nil {
		return nil, errors.New("credsvc: the service cannot read the stop switches")
	}
	if cfg.DB == nil {
		return nil, errors.New("credsvc: the service cannot remove an ended run's placement")
	}
	if cfg.Probes == nil {
		// A probe lease that cannot be written is a credential that can never
		// be probed, and a backed-off credential is cleared by nothing else: it
		// would be out of service for ever (C1.9's ProbeSucceeded is the only
		// transition off that mark). A service that cannot take the lease is
		// therefore not a service this build starts.
		return nil, errors.New("credsvc: the service cannot take the probe's one-at-a-time lease, so a backed-off credential could never come back")
	}
	if cfg.Slips == nil {
		// A service that cannot write a slip cannot grant a run anything: the
		// grant is the only way a run is ever handed the id its calls are bound
		// to (D1932), so a nil store is a service whose whole purpose is
		// unreachable rather than one whose slip route refuses — which is the
		// same statement NewServer makes about State and Probes, and the reason
		// a deployment has no flag to leave it out.
		return nil, errors.New("credsvc: the service cannot write a slip, so it can grant no run its credentials")
	}
	if cfg.AuditLog == nil {
		// The same refusal for the same reason one step further along: a probe
		// this service could not audit must not be made (D143), and one that
		// cannot be audited at all is one that cannot be made at all — which is
		// again a credential that never comes back. It is also the whole of the
		// service's audit: the auditor built from this handler is the one the
		// answer path and the mint write through, so a service without it is a
		// service whose every issuance and every mint would refuse.
		return nil, errors.New("credsvc: the service has no audit handler, so it cannot record a probe and a backed-off credential could never come back")
	}
	if len(cfg.JobAccounts) == 0 {
		return nil, errors.New("credsvc: the service has no job accounts, so it can bind no run to a repository")
	}
	if len(cfg.ReviewAccounts) > 0 && len(cfg.Repos) == 0 {
		// The review class's pairing is the account's admission to the
		// repository the run identity names, and the table is the only thing
		// that can name it: a service with review accounts and no table would
		// answer every review call with a refusal, which is fail-closed but is
		// not the service the deployment configured.
		return nil, errors.New("credsvc: the service has review job accounts and no repository table, so it can pair no review call with a run")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	// The client is hardened whichever one it is: a client this service did not
	// build is still the client that carries the credential, so the rule that it
	// never follows a redirect is applied to it here rather than trusted to its
	// caller.
	cfg.Client = upstreamClient(cfg.Client)
	// The auditor is built here and not taken from the configuration: it must
	// be armed with this service's own bundle loader (NewProbeAuditor), which
	// is the one place the values it redacts live.
	audit, err := NewProbeAuditor(cfg.AuditLog, cfg.Bundles)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, log: cfg.Log, now: cfg.Now, jobs: make(map[string]JobAccount, len(cfg.JobAccounts)), review: make(map[string]ReviewJobAccount, len(cfg.ReviewAccounts)), audit: audit, slips: newSlipCache(), mergeMints: newMergeMintRate(cfg.Now)}
	for _, ja := range cfg.JobAccounts {
		if ja.Email == "" || ja.Slug == "" || ja.Repo == "" {
			return nil, errors.New("credsvc: a job account with no email, slug or repository binds nothing")
		}
		if _, dup := s.jobs[ja.Email]; dup {
			return nil, errors.New("credsvc: the same job account is bound to two repositories")
		}
		s.jobs[ja.Email] = ja
	}
	for _, ra := range cfg.ReviewAccounts {
		if ra.Email == "" || ra.Account == "" {
			return nil, errors.New("credsvc: a review job account with no email or no Claude account binds nothing")
		}
		// Keyed by the folded email, exactly as the verifier keys its own
		// review table: the account Verify answers with is the CONFIGURED
		// spelling of the email it matched, so a deployment whose two lists
		// spell the same address in different cases would have the verifier
		// admit a caller this table could not look up — a miss on case alone,
		// and a pairing refused for a reason no operator could see.
		folded, ok := foldEmail(ra.Email)
		if !ok {
			return nil, errors.New("credsvc: a review job service account is not a printable ASCII email address")
		}
		if prev, dup := s.review[folded]; dup {
			if prev.Account != ra.Account {
				return nil, errors.New("credsvc: the same review job account is bound to two Claude accounts")
			}
			continue
		}
		s.review[folded] = ra
	}
	return s, nil
}

// ServeHTTP answers one call. Every path below either forwards the call or
// refuses it typed; there is no path that answers a call with a credential
// the policy did not admit.
//
// The order of the checks is the order of their cost and of their authority:
// the route, then the request as the contract reads it, then the two
// identities, then the binding between them, then the operator's stop flag,
// then the policy and the request itself, and only then the bundle and the
// cascade.
//
// The request's own check is deliberately before the bundle (the C1.11
// follow-up): the bundle is the one document that holds every credential, and a
// request the gateway would refuse — a model the project does not allow, a
// server tool, a shape the budget cannot bound — must be refused without a
// credential ever being read. The policy is read first because the check needs
// it: it is a policy read, not a credential, and a policy that cannot be read
// refuses the call either way.
//
// Two routes share that order, and the mint leaves it after the stop flag: the
// mint's whole admission is the two identities, the binding and the switch
// (C2.1, D145), and the policy, the cascade and the shape of a model call are
// none of its business.
//
// The verify route (C3.9f) does not share it at all, and it is the ONE route
// answered ahead of the shared parse. Every route above is admitted by
// RequestFromHTTP, which reads the two identity headers, and by Verify, which
// refuses a call that carries no run identity — and a verifier holds none: it is
// not a run of this installation and not a job of it. Left behind that parse, the
// route would refuse every verifier before the class was ever consulted, and the
// whole route would be dead configuration (spec §5.4). So the path is recognised
// first, the one token it carries is verified on its own by its own class, and
// its refusal is that class's own typed refusal — after which everything the
// verifier's handler needs is the call's own body and the key the account it
// names would be at.
//
// What this path answers is recorded (C1.13), one line per call: the issuance
// the cascade's answer is (cascade.go's answer), and every refusal from the
// binding on — the pairing of the two identities, the stop switches, the
// policy, the request's own reading, the bundle, the admissions and the
// cascade's own refusals. The refusals BEFORE that are not: a route this
// service does not serve, a request the contract cannot read and an identity it
// will not verify are answered before there is a run, a repository or a job this
// service could name, and the audit line's "who" is the identity Verify returned
// (Identity.Audit) and nothing a caller typed — which is also why the binding
// refusal, the first one that has a whole identity to name, is the first one
// recorded.
//
// The refusals with no line are the ones above that point, and only those: no
// refusal this service answers once it has an identity in hand goes
// unrecorded, the request-shape refusals included — they are answered before
// the bundle is read, which is why their line names no credential, but they are
// answered with the identity in hand and the status they carry is the
// contract's own word for them (wire.go's request_refused, audit.go's
// validErrorType). So there is no refusal on this path this log cannot say.
//
// A line carries the purpose and the credential of the ROUTE the call asked
// for and not the branch it was answered in, which is why the refusals above
// the mint's branch (the binding and the stop switches) read both off the URL:
// a stopped service mints nothing and proxies nothing, and which of the two was
// asked for is the one thing those refusals know — including that a mint's
// credential is the App key, the only one that endpoint has.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !servedRoute(r) {
		// There is no other route, and in particular no admin route: D143's
		// "no admin endpoint" is kept by there being nothing to reach rather
		// than by a check inside a handler.
		s.refuse(w, http.StatusNotFound, "not_found",
			"this service serves POST "+MessagesPath+", "+CountTokensPath+", "+MintPath+", "+SignPath+", "+VerifyPath+", "+BitbucketReadPath+", "+GitHubReadPath+", "+MergeMintPath+", "+SlipPath+" and "+SlipClaimPath+", and the run's own Bitbucket traffic under "+BitbucketProxyPrefix)
		return
	}
	if r.URL.Path == VerifyPath {
		// The one route that is answered before the shared parse, and it has to
		// be: RequestFromHTTP reads the two identity headers below and Verify
		// refuses a call with no run identity, while the caller of this route
		// holds none and never will (C3.9f, spec §5.4).
		s.verify(ctx, w, r)
		return
	}
	if r.URL.Path == BitbucketReadPath {
		// The second route answered before the shared parse, for the verifier
		// class's reason and not a new one (BB4, B2): its caller is a person,
		// who holds no run identity either, so RequestFromHTTP would refuse the
		// call before its own class ever read the token. What replaces the parse
		// is this route's own read of the body (readReadRequest), and the
		// run-identity headers are refused by name inside the handler.
		s.bitbucketRead(ctx, w, r)
		return
	}
	if r.URL.Path == GitHubReadPath {
		// The third route answered before the shared parse, and for the same
		// reason as the other two (BB4, B2): its caller is a person, who holds
		// no run identity, so RequestFromHTTP would refuse the call before the
		// person class ever read the token. Its own read of the body is
		// readGitHubReadRequest, and the run-identity headers are refused by
		// name inside the handler.
		s.githubRead(ctx, w, r)
		return
	}
	if r.URL.Path == MergeMintPath {
		// The fifth route answered before the shared parse, and the orchestrator
		// daemon's reason is the other four's (E16, D2049): the daemon is not a
		// run of this installation and holds no run identity, so RequestFromHTTP
		// would refuse the call — for the missing identity headers — before the
		// daemon class ever read the token. Its own read of the body is
		// readMergeMintRequest, and the run-identity headers are refused by name
		// inside the handler.
		s.mergeMint(ctx, w, r)
		return
	}
	if r.URL.Path == SlipPath {
		// The fourth route answered before the shared parse, and the launcher
		// class's reason is the other three's: a launcher holds no run identity,
		// and the run it is asking for does not exist yet (D1937, D1938), so
		// RequestFromHTTP would refuse the call — for the missing identity
		// headers — before the class ever read the token. What replaces the
		// parse is this route's own read of the body (readGrantRequest), and the
		// run and the repository the call is for are its fields and not headers.
		//
		// The CLAIM route is deliberately not here: a claim is the run's own
		// first call and presents the two identities every other run call does,
		// so it goes through the shared parse below and is branched on after the
		// stop switches, exactly as the mint is.
		s.slipGrant(ctx, w, r)
		return
	}
	req, err := RequestFromHTTP(r)
	if err != nil {
		// The contract's own reading of the headers, never this server's
		// (review finding m7). Its errors name a header and never a value.
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity, err.Error())
		return
	}
	id, err := s.cfg.Auth.Verify(ctx, req)
	if err != nil {
		s.refuseIdentity(ctx, w, r, err)
		return
	}
	// The purpose the call asks for and the credential a line for it names, and
	// what every refusal below is recorded as (C1.13): the route decides both
	// (routeAct, the one rule), and they are read here because every refusal
	// that follows — the binding, the stop switches and an unreadable switch —
	// is answered before the route is branched on, and because every refusal
	// below the mint's branch is recorded with THIS purpose as well: a line that
	// hard-coded one route's purpose would be a line an operator reads as
	// another call (a mint refused by the kill switch recorded as a proxied
	// model call was exactly that, before the purpose was read off the route).
	// A mint's line names the App key for the same reason
	// (routeAct): the credential the endpoint issues from is the endpoint's own
	// name for it, not a value read out of a bundle, which is why the mint
	// itself (auditMint, refuseMint) takes both from the same rule.
	purpose, credential := routeAct(r.URL.Path)
	// The pairing, which is where the two classes differ (spec §3.2), and the
	// canonical repository every check below is made against.
	//
	// A per-repository job's account names a repository, so the pairing is one
	// comparison: a run identity is honoured only by the job whose repository
	// it belongs to, which is what stops an admitted job account from
	// presenting a run token of another repository (review finding N1). A
	// review job's account names none, so that comparison cannot be made and
	// must not be replaced by "trust the run token": the repository is the run
	// identity's own slug, resolved through the installation's table and
	// admitted to by the calling account (pairReview). Either way the call
	// below reads the repository from the pairing and not from the job
	// account, because a review caller has no job account to read it from.
	var (
		ja        JobAccount
		canonical string
	)
	switch id.JobClass {
	case ClassReview:
		repo, err := s.pairReview(ctx, id)
		if err != nil {
			s.refusePairing(ctx, w, id, purpose, credential, err)
			return
		}
		canonical = repo
		// What the mint is handed in place of a job account: the same account,
		// the run identity's slug and the repository the pairing resolved. The
		// mint's own shape is unchanged — C3.9b is the task that chooses its
		// permission set from the class — and what it is scoped to is already
		// the pairing's answer here.
		ja = JobAccount{Email: id.JobAccount, Slug: id.Slug, Repo: canonical}
	default:
		bound := false
		ja, bound = s.jobs[id.JobAccount]
		if !bound || ja.Slug != id.Slug {
			// The refusal is recorded like the ones below it: the identity
			// Verify returned is whole, so the line names the run, the
			// repository the run belongs to, the job and the account — and the
			// route's credential and no other, which for a model call is none
			// (the bundle has not been read) and for a mint is the App key's
			// name.
			s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnverified, ErrorUnverifiedRunIdentity,
				"the run identity is not a run of the calling job's repository")
			return
		}
		canonical = ja.Repo
	}
	on, which, err := s.cfg.Kills.Stop(ctx, id.Slug)
	if err != nil {
		// An unreadable switch is not an off switch (budget's own rule), so
		// the call is refused rather than served. The read is named on the
		// refusal's own line, because a switch that could not be read and a
		// switch that is ON are the same refusal type and the same status to a
		// run and two different incidents to the operator.
		s.refuseCallCause(ctx, w, id, purpose, credential, "", StatusUnavailable, ErrorStateUnavailable,
			"the operator's stop switches could not be read", labelRead(readKills, err))
		return
	}
	if on {
		// The operator's stop flag is the kill switch they already have
		// (the owner's answer of 2026-10-19): with either on, the service
		// issues no credential at all and answers a refusal the run halts on.
		// It is read before the bundle, so a stopped service reads no
		// credential at all.
		s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	if r.URL.Path != SlipClaimPath {
		// The slip the call presents, if it presents one (DS1.6, G3, G4, G12).
		// It is read after the stop switches — a stopped service reads no slip
		// either — and on every route but the claim's: a claim is not a call
		// that carries a slip, it is the call that TAKES one, and its own read
		// of the two headers is the once-only fence of D1941 (slipClaim). A
		// call that presents neither header is the slip-less path, which is
		// what this service served before the slips existed and what it goes on
		// serving (R9).
		binding, refused := s.verifyCallSlip(ctx, r, id)
		if refused != nil {
			// The refusal names the read it was taken on when it was taken on
			// one (slipRefusal.cause): the slip's own record could not be read
			// is the one of these that is a failed read rather than an answer
			// about what the call said, and the two are one type and one status
			// to the run.
			s.refuseCallCause(ctx, w, id, purpose, credential, "", refused.status, refused.typ, refused.message, refused.cause)
			return
		}
		if binding != nil {
			// The rest of the call is answered under the slip it was admitted
			// by: the audit line reads the two fields from here (Auditor.Write,
			// R5), the offering is fenced by it (slipOffering, G12) and the
			// claim secret is registered with the call's scrub (callScrubs), so
			// a value the run pasted into its own body reaches no upstream log.
			ctx = withCallSlip(ctx, binding)
		}
	}
	if r.URL.Path == SlipClaimPath {
		// The claim (DS1.3, D1941). Everything above it is its admission and
		// nothing below it is: the run identity and the job's own token have
		// been verified, the pairing has fixed the repository the run belongs
		// to, and the operator's stop flag has been read, so a stopped service
		// claims nothing. It reads the slip the run names and decides from the
		// record alone — the run it binds, the account it expects and the claim
		// secret, if it was already claimed — and it consults no policy, no
		// bundle and no cascade: a slip is a grant, not a credential, and no
		// tier or admission has anything to say about it. Which of those names a
		// run is later served is DS1.6's call.
		s.slipClaim(ctx, w, r, id, req)
		return
	}
	if r.URL.Path == MintPath {
		// The mint (C2.1, D145). Everything above it is its admission and
		// nothing below it is: the run identity and the job's own token have
		// been verified, the binding between them has been made — which is what
		// fixes the repository the token is scoped to — and the operator's stop
		// flag has been read, so a stopped service mints nothing. It reads the
		// bundle itself, because the App key is in it, and it reads no policy
		// and consults no cascade: an installation token is not a model
		// credential and no tier or admission has anything to say about it.
		//
		// The review class reaches this endpoint too, and what it is served
		// here is the class's own decision (mintPermissions, D1065): CSR1's
		// read-only set, never the run's write set over the repository the
		// review is about. The pairing above already refused a review identity
		// of any other shape, so a coding run launched into the review job never
		// reaches a mint at all.
		s.mint(ctx, w, r, id, ja)
		return
	}
	if r.URL.Path == SignPath {
		// The sign route (C3.9e). Everything above it is its admission and
		// nothing below it is: the two identities have been verified, the
		// pairing — for a review caller, the class's own, with the claim rule
		// of §3.3 — has fixed the repository the frame binds, and the
		// operator's stop flag has been read, so a stopped service signs
		// nothing. It reads the bundle itself, because the account's
		// review-signing key is in it, and it reads no policy and consults no
		// cascade: a signature is not a model credential and no tier or
		// admission has anything to say about it. The class is the route's own
		// gate and the handler makes it, because the key is the CALLER'S
		// account's.
		s.sign(ctx, w, r, id, req, purpose)
		return
	}
	if strings.HasPrefix(r.URL.Path, BitbucketProxyPrefix) {
		// The Bitbucket proxy route (B1.5). Everything above it is its
		// admission and nothing below it is: the two identities have been
		// verified, the pairing has fixed the repository the run's own job
		// account is (canonical, G3), and the operator's stop flag has been
		// read, so a stopped service proxies nothing. It reads the project's
		// published block itself — which is what admits the repository, and
		// whether the installation proxies it at all (G9) — and the bundle,
		// for the one token it forwards with; and it consults no cascade,
		// because a Bitbucket token is not a model credential and no tier,
		// admission or budget has anything to say about it.
		s.bitbucketProxy(ctx, w, r, id, canonical)
		return
	}
	// The project's policy, and the request itself, are read before the bundle:
	// the request's check needs the policy's allow-list, and neither is a
	// credential. A request the gateway's own rules refuse is answered here —
	// before the one document that holds every credential is read at all.
	shared, found, err := s.cfg.Policy.Shared(ctx)
	if err != nil || !found {
		// A node that is not there is not a read that failed, and labelRead
		// answers a nil err as nil: the two share one sentence and the line
		// still tells them apart, by the read it names and by the class.
		s.refuseCallCause(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
			"the project's policy could not be read", labelRead(readPolicyShared, err))
		return
	}
	cl, err := readCall(r, callScrubs(ctx, req))
	if err != nil {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	if r.URL.Path == MessagesPath {
		// Only the Messages path is held to these rules. The token count's body
		// is not a Messages body — it carries no max_tokens — and the gateway
		// does not hold it to them either (its own forwardFree), so a check
		// applied to it would refuse calls the gateway forwards.
		//
		// The project's own allow-list is read first, and a project that
		// publishes none is refused here: with no list this service has no
		// model rule to pin the call to, and the ceiling the run is held to
		// instead is the job's own configuration, which the service cannot read
		// (the owner's ruling of 2026-10-22 — fail closed, never "no rule").
		// The refusal is the same typed 400 as the check's own, and like it,
		// it is answered before the bundle is read: the sentence names the way
		// out and no credential value is anywhere in it, because none was
		// touched.
		if noAllowList := requestNoAllowListRefusal(shared); noAllowList != "" {
			s.refuseCall(ctx, w, id, purpose, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+noAllowList)
			return
		}
		refusal, err := s.requestRefusal(shared, cl.body)
		if err != nil {
			// The project's own prices could not be read: a state the caller
			// cannot fix, and never a refusal of what it sent. The read is
			// named on the refusal's own line like every other one of this
			// path's: the sentence says which block it was, and the label and
			// the class say that it was the build of the table and not the
			// policy node that failed.
			s.refuseCallCause(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
				"the project's model prices could not be read", labelRead(readPolicyPrices, err))
			return
		}
		if refusal != "" {
			// The gateway's own sentence, and the one thing this service's
			// answer adds to it: the refusal happened before any credential was
			// read. The sentence does quote the request's own model ID and the
			// tool type or field it objects to — that is what makes it
			// actionable — but no credential value is anywhere in it: none was
			// touched.
			s.refuseCall(ctx, w, id, purpose, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+refusal)
			return
		}
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		// Fail closed, and name the CLASS of what is wrong without ever
		// carrying a value, a key or another package's text (BundleRefusal):
		// the loader's own error carries the key it is about for the operator,
		// and the run reading this refusal is not the operator. What the run
		// cannot be told and the operator can is on the service's own line: the
		// loader's error reaches it as its Go type and never as its text.
		s.refuseCallCause(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
			BundleRefusal(err), labelRead(readBundle, err))
		return
	}
	cands, notAdmitted, err := s.candidates(ctx, id, canonical, b)
	if err != nil {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable, "the repository's admissions could not be read")
		return
	}
	// The slip's own fence on the offering (G12, R9), and the last thing
	// between the candidates and the cascade: a credential that belongs to a
	// person or to the project is served only under a slip that names it.
	binding, _ := callSlipOf(ctx)
	cands, notAdmitted = slipOffering(binding, cands, notAdmitted)
	sel := &creds.Selector{Store: s.cfg.State, Settings: settingsOf(shared), Now: s.now}
	s.forward(ctx, w, cl, sel, id, b, cands, notAdmitted)
}

// requestRefused is what every request refusal begins with: the gateway's own
// sentence follows it. A caller reading it learns the two things that are not
// in the gateway's sentence — that this is the request being refused and that
// the refusal came before any credential was read — and no credential value is
// in either.
const requestRefused = "the request is refused before any credential is read: "

// refuse writes one typed refusal. It is the only way this service answers a
// call it does not forward, so every refusal carries a type the runner
// switches on and a sentence that names no credential value.
func (s *Server) refuse(w http.ResponseWriter, status int, typ, message string) {
	WriteWireError(w, status, WireError{Error: typ, Message: message})
}

// refuseIdentity answers a Verify refusal, and does the one piece of work a
// refusal can require.
//
// When the refusal is that the run has ended, the run's placement under
// creds/ is removed (re-review of C1.9, finding N4). C1.9 never deletes a
// run's creds/current node and deletes a lease only once it has lapsed and the
// residue above its own bound has been passed, so without this the nodes of
// every run the project has ever had accumulate — and this call path, whose
// liveness check is what learns a run has ended, is where the deletion
// belongs.
//
// The removal is one atomic Patch of both nodes, because the pair is one fact
// about the run (creds' SetPlacement writes them the same way), and it is
// best-effort in exactly one sense: the refusal is answered whatever it does,
// since a caller whose run has ended must be refused whether or not the
// cleanup landed, and the next call for that run will try again.
//
// A run that has ended is also where its slip ends (endEndedRunsSlip): the two
// are the same fact seen twice, and this refusal is the only place the service
// learns it.
func (s *Server) refuseIdentity(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	var ended *RunEnded
	switch {
	case errors.As(err, &ended):
		s.forgetRun(ctx, ended.Slug, ended.Run)
		s.endEndedRunsSlip(ctx, r, ended)
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity, err.Error())
	case errors.Is(err, ErrUnverifiedJob):
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
	case errors.Is(err, ErrUnverified):
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity, err.Error())
	default:
		// The state the identity must be checked against could not be read:
		// a refusal the run halts on, never a fallback (D143).
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, err.Error())
	}
}

// forgetRun removes a run's placement from the cascade's state: the credential
// it was on, and the lease it held.
//
// It is written here rather than in internal/creds because it is not a
// selection's business — creds.Store deliberately has no delete, so that no
// selection can drop another call's mark — and because the paths it names are
// creds' own exported builders, so the deletion cannot drift from the writes.
// The failure is logged and not raised: a caller whose run has ended is
// refused either way.
func (s *Server) forgetRun(ctx context.Context, slug, run string) {
	r := creds.Run{Slug: slug, ID: run}
	if err := s.cfg.DB.Patch(ctx, "", map[string]any{
		creds.PathCurrent(r): nil,
		creds.PathLease(r):   nil,
	}); err != nil {
		s.log.Warn("credsvc: an ended run's placement could not be removed", "error", err)
	}
}

// slipBodyTTL is how long the body of a slip this service verified is held in
// memory (R4, F14): five minutes, which is the plan's number and not a tuning
// knob.
//
// What the window buys is a model call decided without a read of the database:
// a slip is read on EVERY call a run makes, and its body is written twice in a
// run's life — the grant and the claim — so a read per call is a read the
// record does not need. What the window costs is that a change to the slip's
// OWN fields (an ended record, an edited Allowed set) is seen up to five
// minutes late, and that is the trade R4 and F14 accept.
//
// What it must never answer is the run's own liveness and the operator's stop
// switches, and neither is here: the outcome is read per call by auth.go's
// checkLive as it always was, the switches by ServeHTTP, and the runtime is a
// field of the body the decision is recomputed from on every call — so an
// expiry that arrives inside the window still ends the record at that call.
const slipBodyTTL = 5 * time.Minute

// slipCache holds the slip bodies this service has verified, one entry per slip
// id, each good for slipBodyTTL from the read that put it there.
//
// What it holds is a BODY and never a DECISION: every call re-asks the record's
// own questions of the body it finds — is this the call's run and account, is
// this the claim secret, is the runtime still running — so the cache can never
// serve a call the record would refuse, and what it hides is a change to the
// fields and nothing else.
//
// Only a record this service read back and admitted a call by is ever written
// into it (verifyCallSlip). That is what keeps the map the installation's own
// slips: the key is an id a CALLER chose, so a cache that remembered whatever
// an id turned up would be a place for one caller to plant a record another
// call is decided from.
type slipCache struct {
	mu      sync.Mutex
	entries map[string]slipEntry
}

// slipEntry is one cached body and the instant it was read.
type slipEntry struct {
	sl creds.Slip
	at time.Time
}

func newSlipCache() *slipCache { return &slipCache{entries: map[string]slipEntry{}} }

// get answers the body held for id, if one is held and its window has not
// passed at now. An entry past its window is dropped where it is found rather
// than by a sweeper, because a sweep is the eviction the spec says this cache
// has none of: a slip whose calls stop leaves its entry behind until the
// instance goes away, and a cache that is per instance and lost on scale-to-zero
// is what the spec accepts in exchange for reading the record once in five
// minutes.
func (c *slipCache) get(id string, now time.Time) (creds.Slip, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok {
		return creds.Slip{}, false
	}
	if !now.Before(e.at.Add(slipBodyTTL)) {
		delete(c.entries, id)
		return creds.Slip{}, false
	}
	return e.sl, true
}

// put holds one verified body for its window.
func (c *slipCache) put(id string, sl creds.Slip, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = slipEntry{sl: sl, at: now}
}

// forget drops any body held for id. It is called where the service ENDS a
// record itself — the max runtime, the run's outcome — because a record that is
// gone needs no window, and one that is ended and then re-granted under the
// same id (which only a collision could do) must not be read from a body read
// before it.
func (c *slipCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// slipBinding is the slip a call was ADMITTED under: the id, the owner the
// grant recorded and the names it allows, all taken from a record verified as
// this call's, this account's, claimed with the secret it presented and still
// inside its runtime (verifyCallSlip).
//
// verifyCallSlip is the only thing that builds one, and that is the point: the
// fence below (slipOffering) and the audit line's two fields (Auditor.Write)
// read a binding as "this call was checked", and a value that could be built
// anywhere would be a claim the service had not verified.
type slipBinding struct {
	id      string
	owner   string
	allowed []string
	// secret is the claim secret the call presented. It is held for ONE thing:
	// registering it with the call's scrub (callScrubs), so a value the run
	// pasted into its own body reaches no upstream log. Nothing writes it
	// anywhere else.
	secret string
}

// names reports whether the slip allows a credential name. A nil binding — a
// call with no slip — allows none, which is the fail-closed half of G12 read at
// its narrowest: the receiver is nil-safe so the caller does not have to be.
func (b *slipBinding) names(name string) bool {
	return b != nil && slices.Contains(b.allowed, name)
}

// ctxCallSlip is the key a verified slip travels under for the rest of its
// call.
//
// The line it is for is written by the code that ANSWERS the call — cascade.go's
// auditCall, refuseCall and answer — and that code is not the code that
// verified the slip. The request's context is the one thing every one of those
// writers already holds, and it is the call's own: a value put here is read by
// the lines of this call and by no other, which is what R5's two fields are.
// The alternative — a parameter threaded through the cascade — is the interface
// of a task that is not this one.
type ctxCallSlip struct{}

// withCallSlip hangs a verified slip on the call's context.
func withCallSlip(ctx context.Context, b *slipBinding) context.Context {
	return context.WithValue(ctx, ctxCallSlip{}, b)
}

// callSlipOf answers the verified slip of the call ctx belongs to, if it was
// admitted under one.
func callSlipOf(ctx context.Context) (*slipBinding, bool) {
	b, ok := ctx.Value(ctxCallSlip{}).(*slipBinding)
	return b, ok && b != nil
}

// callScrubs is what readCall must keep out of the forwarded request: the
// caller's own two tokens, and — when the call was admitted under a slip — the
// claim secret it presented.
//
// The secret is a credential the run holds (D1941) and the id beside it is
// public (G5); neither belongs upstream. The headers go by prefix
// (readCall) and this is what covers the BODY, which is where a run that pasted
// one would put it — and where an upstream's logs would keep it.
func callScrubs(ctx context.Context, req Request) []string {
	scrubs := []string{req.RunIDToken, req.JobIDToken}
	if b, ok := callSlipOf(ctx); ok {
		scrubs = append(scrubs, b.secret)
	}
	return scrubs
}

// slipRefusal is a refused call that carried a slip: the typed refusal
// ServeHTTP answers it with, in the shape every refusal on that path has.
//
// cause is the read the refusal was taken on, for the one refusal here that is
// a read that FAILED rather than a request this service does not read — the
// store's read of the record — and it is nil for the rest, which are answers
// about what the call itself said. It reaches the refusal's own log line and
// nothing else (refusallog.go), so the run is answered the same class sentence
// it was before this field existed.
type slipRefusal struct {
	status  int
	typ     string
	message string
	cause   error
}

// verifyCallSlip reads the slip a call presents and verifies it (DS1.6, G3, G4,
// G12, R10). It answers the binding the call was admitted under, or the refusal
// it is answered with, or neither for a call that presents no slip at all.
//
// The reading is this contract's own (oneHeader, slipSecretFromHTTP): a value
// sent twice, an id this service could not have minted, an id with no claim
// secret and a secret with no id are requests this service does not read, and
// they are refused before the database is asked anything. A call that presents
// NEITHER header is the slip-less path — not a refusal — and what that path is
// served is the same as it was before the slips existed (R9's second sentence).
//
// What a record read back must be is what the claim's own reading form asks of
// it (slipClaim, DS1.3), in the same order: THIS run's and THIS account's,
// claimed with the secret the call presented, and still inside its runtime. The
// three are ONE sentence and one status, so a caller cannot use this route to
// learn which slips exist — and only a record that passes all three is ever
// cached, because a body admitted from a refused call is a body another call
// could be decided from.
//
// The runtime is the one refusal that also ACTS: a slip whose runtime has passed
// is ENDED here (creds.SlipStore.End), so issuance stops for every later call
// and for every other reader of the database rather than only for the call that
// found it expired. A record whose secret does NOT match ends nothing,
// deliberately: the id is public and every run of the installation holds a job
// token, so ending the record of a call that presented a bad secret would be a
// way to stop a working run by knowing its id.
func (s *Server) verifyCallSlip(ctx context.Context, r *http.Request, id Identity) (*slipBinding, *slipRefusal) {
	secret, presented, err := slipSecretFromHTTP(r)
	if err != nil {
		return nil, &slipRefusal{StatusRequestRefused, ErrorRequestRefused, requestRefused + err.Error(), nil}
	}
	if len(r.Header.Values(HeaderSlip)) == 0 {
		if presented {
			// A claim secret with no slip names the secret of nothing: there is
			// no record this call could be asking about.
			return nil, &slipRefusal{StatusRequestRefused, ErrorRequestRefused,
				requestRefused + "the call carries a " + HeaderSlipClaim + " header and no " + HeaderSlip + " header", nil}
		}
		return nil, nil
	}
	slipID, err := oneHeader(r, HeaderSlip)
	if err != nil {
		return nil, &slipRefusal{StatusRequestRefused, ErrorRequestRefused, requestRefused + err.Error(), nil}
	}
	if !printableHeaderValue(slipID, maxSlipIDBytes) {
		// A slip id this service could not have minted is a request it does not
		// read, refused BEFORE the database is asked — the claim route's own
		// rule (slipClaim), for its own reason: a lookup of every string a
		// caller sends is a lookup an operator's database should not have to
		// answer.
		return nil, &slipRefusal{StatusRequestRefused, ErrorRequestRefused,
			requestRefused + "the " + HeaderSlip + " header is not one slip id", nil}
	}
	if !presented {
		// Every call after the claim presents the secret the claim answered
		// (D1941): an id alone is never enough (G3), and a call that has none
		// is a call of a shape this route does not have rather than one to
		// serve.
		return nil, &slipRefusal{StatusRequestRefused, ErrorRequestRefused,
			requestRefused + "the call carries a " + HeaderSlip + " header and no " + HeaderSlipClaim + " header", nil}
	}

	now := s.now()
	sl, held := s.slips.get(slipID, now)
	if !held {
		read, found, err := s.cfg.Slips.Slip(ctx, slipID)
		if err != nil {
			// The state the slip must be read from could not be read: a refusal
			// the run halts on, never a fallback (D143).
			return nil, &slipRefusal{StatusUnavailable, ErrorStateUnavailable, "the slip could not be read", labelRead(readSlip, err)}
		}
		if !found {
			return nil, slipNotThisRunsRefusal()
		}
		sl = read
	}
	if !slipIsThisRuns(sl, id) || !sl.ClaimMatches(secret) {
		return nil, slipNotThisRunsRefusal()
	}
	if !sl.Live(now) {
		s.endSlip(ctx, slipID, "its runtime has passed")
		return nil, slipNotThisRunsRefusal()
	}
	if !held {
		// The window is measured from the READ that just happened and never
		// from the call: a put on every call would slide it, and a run that
		// calls this service as its runs do — every model call — would then
		// hold a body the record could have changed under for as long as it
		// keeps calling. Five minutes is the whole of what the spec accepts a
		// finished or revoked job keeps access for (F14), so the body is
		// re-read from the record once the window has passed.
		s.slips.put(slipID, sl, now)
	}
	return &slipBinding{id: slipID, owner: sl.Owner, allowed: slices.Clone(sl.Allowed), secret: secret}, nil
}

// slipNotThisRunsRefusal is the one answer every refusal the RECORD makes is
// given (slipNotThisRuns), in this path's shape.
func slipNotThisRunsRefusal() *slipRefusal {
	return &slipRefusal{StatusUnverified, ErrorUnverifiedRunIdentity, slipNotThisRuns, nil}
}

// endSlip ends one slip and forgets the body held for it. It is the service's
// own act and not a caller's — creds.SlipStore.End is reached from nowhere else
// (G10's no end route) — and it is best-effort in the way forgetRun is: a
// record that could not be ended is one the runtime cap reaches anyway, and the
// call this was decided on is refused either way.
func (s *Server) endSlip(ctx context.Context, id, why string) {
	s.slips.forget(id)
	if err := s.cfg.Slips.End(ctx, id); err != nil {
		s.log.Warn("credsvc: a slip could not be ended", "slip", id, "why", why, "error", err)
	}
}

// endEndedRunsSlip ends the slip a call presented when the record is the ENDED
// run's own: the outcome half of "issuance ends" (DS1.1's End names the run's
// outcome and the max runtime as its two callers, R10).
//
// It is the half nothing else reaches. A run that has ended makes no call this
// service would serve, so no other path ever sees its record again, and without
// this the installation would keep one node per slipped run for ever — the same
// accumulation the placement cleanup beside it exists to prevent (forgetRun).
// The runtime cap would reach the record one day; the outcome reaches it now.
//
// The RECORD decides, and not the caller: the id the call presented is read
// back, and it is ended only if it names this very run. A call cannot end
// anyone's slip by naming an id (G3, the id alone is never enough) — and a
// record whose secret this call did not present is untouched, because this path
// has verified the run's identity and not the slip's.
func (s *Server) endEndedRunsSlip(ctx context.Context, r *http.Request, ended *RunEnded) {
	ids := r.Header.Values(HeaderSlip)
	if len(ids) != 1 || !printableHeaderValue(ids[0], maxSlipIDBytes) {
		return
	}
	sl, found, err := s.cfg.Slips.Slip(ctx, ids[0])
	if err != nil {
		s.log.Warn("credsvc: an ended run's slip could not be read", "slip", ids[0], "error", err)
		return
	}
	if !found || sl.Slug != ended.Slug || sl.Run != ended.Run {
		return
	}
	s.endSlip(ctx, ids[0], "the run has ended")
}

// slipOffering narrows a call's offering to what it may be served (G12, R9).
//
// The rule is about NAMES, and it is one sentence: a credential of the secret
// grammar (secretname) belongs to a person or to the project, so it is served
// only under a slip that names it — and a call with no slip is offered none of
// them, which is the fail-closed half. Every other name a caller's list holds
// is a platform credential of the bundle's own closed key set (CredentialKey),
// which is what this service served before the slips existed and what it goes
// on serving, so the slip-less path is unchanged (R9's second sentence).
//
// A name dropped here is reported in the tried list as ReasonNotAdmitted — the
// reason the cascade already has for "this credential is not yours" — so a run
// reads a slip that does not cover it exactly as it reads a repository the
// policy does not admit it to. And when nothing is left, the refusal is the
// cascade's own: the offering arrives empty, the selection has no candidates,
// and refuseSelection answers with the tried list (one line, one status).
func slipOffering(b *slipBinding, cands []creds.Credential, notAdmitted []Tried) ([]creds.Credential, []Tried) {
	out := make([]creds.Credential, 0, len(cands))
	for _, c := range cands {
		if slipRequiresSlip(c.Name) && !b.names(c.Name) {
			notAdmitted = append(notAdmitted, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonNotAdmitted})
			continue
		}
		out = append(out, c)
	}
	return out, notAdmitted
}

// slipRequiresSlip reports whether a credential name is one only a slip may
// serve: a per-user or project-shared name of the grammar, which is the whole
// of what "belongs to a person or to the project" means to this service (R9).
// A name outside the grammar is a bundle entry's, and the bundle's keys are a
// closed set this service already decided about.
//
// The PREFIX is the test, which is the reading the grant already holds a name
// to (checkGrantedNames, fail-closed for the same case): a name that carries
// fugaro_u_ or fugaro_p_ and does not parse — an upper-case letter, an
// ambiguous separator, no name half at all — is a name of the grammar's kind
// that this service cannot read, and one it cannot read as somebody else's is
// not thereby nobody's. Only a name with neither prefix is a bundle entry's.
func slipRequiresSlip(name string) bool {
	if _, err := secretname.Parse(name); err == nil {
		return true
	}
	return strings.HasPrefix(name, secretname.UserPrefix) || strings.HasPrefix(name, secretname.ProjectPrefix)
}
