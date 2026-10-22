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
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// The two paths the service serves, spelled as Anthropic spells them: the
// in-runner gateway forwards a run's request here instead of to
// api.anthropic.com, so the path is the one the SDK built.
const (
	MessagesPath    = "/v1/messages"
	CountTokensPath = "/v1/messages/count_tokens"
)

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
	// Audit is C1.13's auditor: the one writer of the service's audit lines,
	// and the seam the service's own redaction is armed at (WithAuditScrub).
	// Every act of this service that touches a credential is recorded through
	// it — a mint (C2.1) writes its line here — and an act that cannot be
	// audited is not made (D143), so NewServer refuses a service without one
	// rather than building one whose mints could only refuse.
	Audit *Auditor
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
	// AuditLog is where the service's audited acts are written (C1.13): the
	// probe today (C1.12), and every act the service audits as the rest of this
	// build wires one. The service builds its auditor from it, armed with the
	// bundle loader's own redaction (NewProbeAuditor), so a credential's value
	// cannot reach a line: the arming is not a caller's to forget.
	//
	// It is a handler and not a logger, deliberately — see NewAuditor: a logger
	// carries whatever it was built with, and an attr or a group on an audit
	// line is a field the contract does not have.
	AuditLog slog.Handler
	// JobAccounts binds a job service account to the repository whose runs
	// are launched as it. It is the table the run-to-job binding is checked
	// against (N1), so an empty one refuses every call.
	JobAccounts []JobAccount
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
}

// PolicyReader is the published policy as the service reads it: the project's
// shared node, and one repository's own node by slug.
type PolicyReader interface {
	Shared(ctx context.Context) (*projectpolicy.Policy, bool, error)
	Admissions(ctx context.Context, slug string) (*projectpolicy.RepoPolicy, bool, error)
}

// KillReader is the operator's two stop switches as the service reads them.
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

// RTDBKills is the operator's stop flag read from the database. It is
// budget.ReadKills — the same pair a run honours and `fugaro budget kill`
// writes — so the service's stop flag is the switch an operator already has,
// and an unreadable switch is ON in both readers because the rule is one rule
// in one place (budget's fail-closed decode).
type RTDBKills struct {
	// DB is the database; *rtdb.Client is one.
	DB budget.KillGetter
}

func (k RTDBKills) Stop(ctx context.Context, slug string) (bool, string, error) {
	kills, err := budget.ReadKills(ctx, k.DB, slug)
	if err != nil {
		return false, "", err
	}
	on, which := kills.On()
	return on, which, nil
}

// Server is the credentials service: one http.Handler.
type Server struct {
	cfg   Config
	log   *slog.Logger
	now   func() time.Time
	jobs  map[string]JobAccount
	audit *Auditor
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
	if cfg.Kills == nil {
		return nil, errors.New("credsvc: the service cannot read the stop switches")
	}
	if cfg.Audit == nil {
		// C1.13: an issuance that cannot be audited is not made. A service with
		// no auditor is a service whose every mint would refuse, so it is
		// refused here — where an operator sees it at startup — rather than
		// discovered as a run that halts.
		return nil, errors.New("credsvc: the service has no auditor, so an issuance it made could not be audited")
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
	if cfg.AuditLog == nil {
		// The same refusal for the same reason one step further along: a probe
		// this service could not audit must not be made (D143), and one that
		// cannot be audited at all is one that cannot be made at all — which is
		// again a credential that never comes back.
		return nil, errors.New("credsvc: the service has no audit handler, so it cannot record a probe and a backed-off credential could never come back")
	}
	if len(cfg.JobAccounts) == 0 {
		return nil, errors.New("credsvc: the service has no job accounts, so it can bind no run to a repository")
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
	s := &Server{cfg: cfg, log: cfg.Log, now: cfg.Now, jobs: make(map[string]JobAccount, len(cfg.JobAccounts)), audit: audit}
	for _, ja := range cfg.JobAccounts {
		if ja.Email == "" || ja.Slug == "" || ja.Repo == "" {
			return nil, errors.New("credsvc: a job account with no email, slug or repository binds nothing")
		}
		if _, dup := s.jobs[ja.Email]; dup {
			return nil, errors.New("credsvc: the same job account is bound to two repositories")
		}
		s.jobs[ja.Email] = ja
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
// What this path answers is recorded (C1.13), one line per call: the issuance
// the cascade's answer is (cascade.go's answer), and every refusal from the
// point the caller is admitted on — the stop switches, the policy, the request's
// own reading, the bundle, the admissions and the cascade's own refusals. The
// refusals BEFORE admission are not: a route this service does not serve, a
// request the contract cannot read and an identity it will not verify are
// answered before there is a run, a repository or a job this service could name,
// and the audit line's "who" is the identity Verify returned and nothing a
// caller typed (Identity.Audit).
//
// The request-shape refusals (request_refused) are the one refusal after
// admission this path does not record, and the reason is the contract's own:
// those are answered before the bundle is read — no credential is touched at all
// — and the audit line's status is the wire's vocabulary (audit.go's
// ValidStatus), which has no word for them. Writing one would need a status this
// service invented.
//
// A line carries the purpose of the ROUTE the call asked for and not the
// branch it was answered in, which is why the two refusals above the mint's
// branch (the stop switches) read it off the URL: a stopped service mints
// nothing and proxies nothing, and which of the two was asked for is the one
// thing those refusals know.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost || (r.URL.Path != MessagesPath && r.URL.Path != CountTokensPath && r.URL.Path != MintPath) {
		// There is no other route, and in particular no admin route: D143's
		// "no admin endpoint" is kept by there being nothing to reach rather
		// than by a check inside a handler.
		s.refuse(w, http.StatusNotFound, "not_found",
			"this service serves POST "+MessagesPath+", "+CountTokensPath+" and "+MintPath)
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
		s.refuseIdentity(ctx, w, err)
		return
	}
	ja, bound := s.jobs[id.JobAccount]
	if !bound || ja.Slug != id.Slug {
		// Review finding N1: the two identities a call carries are verified
		// separately and nothing in C1.10 pairs them, so the pairing is an
		// admission question and it is asked here. A run identity is honoured
		// only by the job whose repository it belongs to, which is what stops
		// an admitted job account from presenting a run token of another
		// repository.
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity,
			"the run identity is not a run of the calling job's repository")
		return
	}
	// The purpose the call asks for, and what every refusal below is recorded
	// as (C1.13): the route decides it, and it is read here because the two
	// refusals that follow — the stop switches and an unreadable switch — are
	// answered before the route is branched on. A refusal of a mint recorded as
	// "forward" would be the audit telling an operator that a model call was
	// refused when the run asked for an installation token.
	purpose := PurposeForward
	if r.URL.Path == MintPath {
		purpose = PurposeMint
	}
	on, which, err := s.cfg.Kills.Stop(ctx, id.Slug)
	if err != nil {
		// An unreadable switch is not an off switch (budget's own rule), so
		// the call is refused rather than served.
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		// The operator's stop flag is the kill switch they already have
		// (the owner's answer of 2026-10-19): with either on, the service
		// issues no credential at all and answers a refusal the run halts on.
		// It is read before the bundle, so a stopped service reads no
		// credential at all.
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
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
		s.mint(ctx, w, r, id, ja)
		return
	}
	// The project's policy, and the request itself, are read before the bundle:
	// the request's check needs the policy's allow-list, and neither is a
	// credential. A request the gateway's own rules refuse is answered here —
	// before the one document that holds every credential is read at all.
	shared, found, err := s.cfg.Policy.Shared(ctx)
	if err != nil || !found {
		s.refuseCall(ctx, w, id, PurposeForward, "", "", StatusUnavailable, ErrorStateUnavailable, "the project's policy could not be read")
		return
	}
	cl, err := readCall(r, []string{req.RunIDToken, req.JobIDToken})
	if err != nil {
		s.refuseCall(ctx, w, id, PurposeForward, "", "", StatusUnavailable, ErrorStateUnavailable, err.Error())
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
			s.refuse(w, StatusRequestRefused, ErrorRequestRefused, requestRefused+noAllowList)
			return
		}
		refusal, err := s.requestRefusal(shared, cl.body)
		if err != nil {
			// The project's own prices could not be read: a state the caller
			// cannot fix, and never a refusal of what it sent.
			s.refuseCall(ctx, w, id, PurposeForward, "", "", StatusUnavailable, ErrorStateUnavailable, "the project's model prices could not be read")
			return
		}
		if refusal != "" {
			// The gateway's own sentence, and the one thing this service's
			// answer adds to it: the refusal happened before any credential was
			// read. The sentence does quote the request's own model ID and the
			// tool type or field it objects to — that is what makes it
			// actionable — but no credential value is anywhere in it: none was
			// touched.
			s.refuse(w, StatusRequestRefused, ErrorRequestRefused, requestRefused+refusal)
			return
		}
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		// Fail closed, and name the CLASS of what is wrong without ever
		// carrying a value, a key or another package's text (BundleRefusal):
		// the loader's own error carries the key it is about for the operator,
		// and the run reading this refusal is not the operator.
		s.refuseCall(ctx, w, id, PurposeForward, "", "", StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	cands, notAdmitted, err := s.candidates(ctx, id.Slug, ja.Repo, b)
	if err != nil {
		s.refuseCall(ctx, w, id, PurposeForward, "", "", StatusUnavailable, ErrorStateUnavailable, "the repository's admissions could not be read")
		return
	}
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
func (s *Server) refuseIdentity(ctx context.Context, w http.ResponseWriter, err error) {
	var ended *RunEnded
	switch {
	case errors.As(err, &ended):
		s.forgetRun(ctx, ended.Slug, ended.Run)
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
