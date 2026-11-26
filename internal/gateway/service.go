package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// C1.15. Everything in this file exists for one thing: a run's model calls
// going to the installation's credentials service instead of to Anthropic,
// with the job's own Google identity and the run's, and the answer's tier
// read back for the ledger and the runner (D135, D143). It runs in the job,
// in the in-runner gateway; the service's own half of the contract is
// internal/credsvc, and the OAuth upstream this kind reaches through the
// service is oauth.go.
//
// Deleting the cascade means deleting this file and the case that names
// KindService; no shared code is unpicked.
//
// # Why the wire contract is mirrored here
//
// The names, the closed sets and the reader of a refusal below are a mirror
// of C1.10's contract in internal/credsvc, and not that package itself:
// internal/credsvc imports this one (C1.8's exported helpers are what the
// service forwards with), so this package cannot import it without a cycle.
// The mirror is held to the contract by TestServiceContractMirrorsCredsvc,
// an external test file (package gateway_test, which may import
// internal/credsvc) that runs both readers over one table of answers, and by
// TestServiceConstantsMirrorCredsvc, which compares the names one by one.
// This is the shape the repository already uses for a value two packages
// share (MaxFeePct, pinned by a test that the two are equal).
//
// # What never reaches the agent, and what never reaches a log
//
// The tier, the credential's name and the switch are the service's answer to
// this gateway and to nobody else: they are dropped from the answer before
// it is copied on (dropResponseHeaders), so the agent never learns the set
// of credentials its project holds — which is what D143's accepted single
// point of compromise rests on. A refusal the service wrote is fugaro's own
// text and is never relayed either: the agent gets a sentence this gateway
// builds from validated fields.
//
// No value is ever logged or rendered here. The credential's *name* is
// carried (it is what the run's record displays, C1.19) and it is never a
// value; the two tokens this file attaches are attached and nothing else —
// they are not logged, not written into an error, and not put in a halt's
// detail, and every sentence below is one this file spells.

// KindService is the C1.15 upstream kind: the installation's credentials
// service, which answers the Messages API's own paths with a credential it
// holds and this job does not (D135). It serves the paths the anthropic
// upstream serves, with the same gateway token in front, so the agent's
// configuration is the same one byte for byte: only the destination and the
// credential differ.
const KindService = "service"

// The request headers of the contract (credsvc.HeaderRunIdentity,
// credsvc.HeaderRunJob). The job service account's Google ID token travels
// in Authorization, which Cloud Run IAM consumes for the invoker check (F30),
// so the run's own identity needs a header of its own.
const (
	// HeaderRunIdentity carries the run's Firebase ID token.
	HeaderRunIdentity = "X-Fugaro-Run-Identity"
	// HeaderRunJob names the Cloud Run job the run reports it is in. It is
	// the caller's claim and never evidence (C1.10).
	HeaderRunJob = "X-Fugaro-Run-Job"
	// HeaderSlip names the slip this run's calls are bound to: the id the
	// launcher was granted and the run claimed at its own start (D1941,
	// DS1.5's runner half).
	HeaderSlip = "X-Fugaro-Slip"
	// HeaderSlipClaim carries the secret that claim answered. It is the one
	// credential of this contract, it lives in the runner's memory, and the
	// service keeps only its hash — so it is attached here, on the call the
	// service decides by, and never written to a log, an error or a record.
	HeaderSlipClaim = "X-Fugaro-Slip-Claim"
	// WorkspaceSlipHeader carries the id of the workspace's own slip: the one
	// the workspace's service granted a run (C1's PersonSlipEnv), presented on
	// every call this run makes to that service and on none it makes to its
	// own. It is the first of the two names C1 leaves for WC8 to declare a
	// second time, and this package is the second declaration: internal/creds
	// imports internal/budget, which imports this package, so the gateway
	// cannot spell them through that package and does not (C1's own header
	// comment, F26's shape for the provider wire). TestWorkspaceSlipMirrorsC1
	// holds the two spellings to each other name by name.
	//
	// Two names rather than HeaderSlip's is R4 stated as a spelling: a run
	// holds two slips, they are granted by two services a member's admins
	// control differently (F13), and the header a call carries is what tells
	// the two apart at a service. It is not a secret.
	WorkspaceSlipHeader = "X-Fugaro-Workspace-Slip"
	// WorkspaceSlipClaimHeader carries the claim secret the workspace's claim
	// answered (C1's PersonSlipClaimHeader), read exactly as HeaderSlipClaim
	// is: it is attached to a call to the workspace's service and to no other,
	// it is never logged and never written into an error, a halt's detail or a
	// record, and the workspace keeps only its hash.
	WorkspaceSlipClaimHeader = "X-Fugaro-Workspace-Slip-Claim"
	// HeaderAuthMode is the auth the run's own repository goes out on, as the
	// runner declares it (B2.2, credsvc.HeaderAuthMode). It is a statement
	// and never evidence: the service decides from the record `fugaro update`
	// wrote for that repository and reads this only to check the two agree
	// (D2070 (a), R7), so a value the agent chose would be refused rather
	// than served.
	HeaderAuthMode = "X-Fugaro-Auth-Mode"
	// HeaderAuthAccount names the Claude account an oauth run goes out on,
	// spelled as config.ClaudeAccountNameRE spells one. It is read for one
	// thing only, whether the repository's record names that account.
	HeaderAuthAccount = "X-Fugaro-Auth-Account"
)

// The answer headers of the contract (credsvc.HeaderTier,
// credsvc.HeaderCredential, credsvc.HeaderCredentialOwner, credsvc.HeaderSwitch,
// credsvc.HeaderProbe).
const (
	// HeaderTier is the credential tier that answered: TierA, TierB or
	// TierC. A Messages call settles by it (C1.16).
	HeaderTier = "X-Fugaro-Tier"
	// HeaderCredential is the name of the credential that answered, never
	// its value.
	HeaderCredential = "X-Fugaro-Credential"
	// HeaderCredentialOwner is the owner of the credential that answered,
	// never its value (credsvc.HeaderCredentialOwner, F16): the account id of
	// the person whose credential it is, which the run records beside the
	// credential's name. It is written only where the credential served is
	// the launcher's own per-user one, so an answer that carries none — a
	// bundle entry's, a pool's, a slip-less call's — is the ordinary case.
	HeaderCredentialOwner = "X-Fugaro-Credential-Owner"
	// HeaderSwitch, when present, is the JSON of a switch: this call moved
	// from one credential to another inside the service.
	HeaderSwitch = "X-Fugaro-Switch"
	// HeaderProbe, when present, names the tiers the service probed while
	// answering this call, comma separated and once each. A probe is a call
	// the run pays for, and this is the only place the run can learn of it
	// (C1.12, C1.16): it settles as notional on tier A and as money on any
	// other tier.
	HeaderProbe = "X-Fugaro-Probed"
)

// The credential tiers, as they are spelled on the wire (credsvc.TierA and
// its neighbours). Tier C is reserved.
const (
	TierA = "A"
	TierB = "B"
	TierC = "C"
)

// The error types of a refusal body (credsvc.ErrorExhaustedUntil and its
// neighbours). The runner switches on these, not on a status: two of them
// share one status.
const (
	// ErrorExhaustedUntil is the typed answer: no credential is usable
	// before the answer's instant. It is the one refusal that does not halt
	// the run: the runner waits for the instant (C1.17).
	ErrorExhaustedUntil = "exhausted_until"
	// ErrorUnverifiedRunIdentity is the run's own identity being refused.
	ErrorUnverifiedRunIdentity = "unverified_run_identity"
	// ErrorUnverifiedJobIdentity is the caller's Google identity being
	// refused: the ID token Cloud Run IAM admitted is not one of the
	// installation's job service accounts.
	ErrorUnverifiedJobIdentity = "unverified_job_identity"
	// ErrorStateUnavailable is the service being unable to read the state it
	// must read: a refusal, so the run halts (D143), never a fallback.
	ErrorStateUnavailable = "state_unavailable"
	// ErrorRequestRefused is the call itself being refused by the service's
	// own request check (credsvc.ErrorRequestRefused, C1.11's follow-up): a
	// model the project's published allow-list does not name, a server tool,
	// a shape the budget cannot bound, a max_tokens above the model's own
	// bound — and, since the owner's ruling of 2026-10-22, a project that
	// publishes no budget.allowed_models at all, which the service refuses
	// fail-closed rather than pinning no model. A refusal, so the run halts
	// (D143), and the halt's detail names what to look at.
	ErrorRequestRefused = "request_refused"
	// ErrorNoCandidateCredentials is a call the service had no credential to
	// serve: every credential the policy offered this repository was dropped
	// before the selection was made, so there was nothing to select from,
	// nothing to wait for and no other tier to reach. That is not
	// ErrorStateUnavailable, whose name would send the operator to the
	// database: the service read the state, and what it read is that this
	// repository's admission covers none of the project's model credentials.
	// A refusal, so the run halts (D143). The names the service considered
	// reach this job's log and the service's own line, and never the halt's
	// detail, which the agent reads: the set of the project's credentials is
	// not the agent's business (D143).
	ErrorNoCandidateCredentials = "no_candidate_credentials"
	// ErrorRepositoryNotListed is the workspace's own refusal of a call from a
	// repository its member row does not name (R19): the workspace's record of
	// a job account carries the repositories that account serves (D2992, R6),
	// and a run whose repository is not in that list cannot be served from the
	// workspace's credentials at all. A refusal, so the run halts (D143), and
	// the halt's detail names the workspace owner's own fix.
	//
	// It is written by the workspace's scope — the Scope C1 declares and
	// internal/credsvc (WS2) implements, told apart from the member's own
	// service by WorkspaceModeEnv and nothing else — and it is mirrored here
	// the way every other type of this contract is, because this package
	// cannot import the service's half (the header comment above says why). A
	// scope that spelled it otherwise would halt a run with the
	// unreadable-answer sentence instead of the one that names the fix, so
	// TestAnUnlistedRepositoryHaltsNamingTheFix holds the spelling.
	ErrorRepositoryNotListed = "repository_not_listed"
	// ErrorSlipOwnerRemoved is the workspace's refusal of a call under a slip
	// whose owner has left the workspace's roster (R3, D3146): the workspace's
	// service re-reads its roster as it re-reads its published node, so a
	// person the roster no longer holds stops being served within a minute
	// whether or not the sweep of their containers succeeded, and the call is
	// refused rather than continued on the shared entries. A refusal, so the
	// run halts (D143) with the removal named — and it moves to no other
	// credential, which is what makes it a halt rather than a switch.
	//
	// The word is the workspace's scope's to write — it is mirrored here the
	// way every other type of this contract is, because this package cannot
	// import the service's half — and the landed scope (WC4) writes it at the
	// one point it may: credsvc's verifyPersonCallSlip asks the roster after
	// the slip's claim secret has been matched, so only the run that holds
	// that secret is ever answered this word, and it is answered the removal
	// by name (slipOwnerRemoved). The claim route's own reading of the same
	// fact keeps the record's one sentence instead, since a caller there names
	// a slip before it holds any secret — so a call is answered here and a
	// claim is not.
	ErrorSlipOwnerRemoved = "slip_owner_removed"
)

// Why a credential was not used, in a refusal's tried list
// (credsvc.ReasonExhausted and its neighbours). A closed set.
const (
	ReasonExhausted   = "exhausted"
	ReasonBroken      = "broken"
	ReasonNotAdmitted = "not_admitted"
	ReasonStopped     = "stopped"
)

// The actions an exhausted answer may carry (runstore.PauseAction's three
// values, mirrored here as the rest of this contract is: this package cannot
// import internal/credsvc, which imports it). The credentials service decides
// which one a run takes and the job carries it out (R19); the one rule the
// runner owns is a hotfix run treating wait and pause as halt (R10). A test
// pins the three spellings against the runstore ones.
const (
	// ActionWait is the in-job wait: the reset is nearer than the service's
	// own pause.wait_in_job, and the run waits for it as it always has
	// (D3184).
	ActionWait = "wait"
	// ActionPause is the suspension: the run saves itself, tells the service
	// and ends paused, and the service resumes it later as a new run in the
	// same chain (D3185).
	ActionPause = "pause"
	// ActionHalt is the end: nothing is worth waiting for, and the run stops
	// with HaltNoCredential.
	ActionHalt = "halt"
)

// ReasonBudgetUnavailable is the halt a run that cannot reach the service is
// recorded with (D143: "the run halts like budget_unavailable"), the same
// spelling runstore.HaltBudgetUnavailable and budget.ReasonBudgetUnavailable
// use. A test pins the three.
const ReasonBudgetUnavailable = "budget_unavailable"

const (
	// maxCredentialNameBytes is the contract's bound on a credential's name
	// (credsvc's maxCredentialNameBytes: the rules' MaxString).
	maxCredentialNameBytes = 200
	// maxJobNameBytes is internal/backend/gcp's maxJobName: Cloud Run's own
	// 63 less room for the execution token the execute page adds
	// (credsvc's maxJobNameBytes).
	maxJobNameBytes = 49
	// maxRefusalMessageBytes bounds the free-text message of a refusal body
	// (credsvc's maxRefusalMessageBytes). The message is never read back —
	// the sentence is rebuilt from the fields — but a body this contract
	// could not have written is not read as one it did.
	maxRefusalMessageBytes = 8 << 10
	// maxTriedEntries bounds a refusal's tried list (credsvc's
	// maxTriedEntries; C1.11 must refuse a bundle larger than it).
	maxTriedEntries = 64
	// maxServiceHeaderBytes is the contract's bound on a token-shaped header
	// value (credsvc's maxHeaderTokenBytes).
	maxServiceHeaderBytes = 512
	// maxServiceRefusalBytes is more than any legal refusal body can be: the
	// message's own bound plus maxTriedEntries entries of names at their own
	// bound. A longer answer is not one this contract wrote, and is relayed
	// rather than read (a model error's body is another writer's and may be
	// any size).
	maxServiceRefusalBytes = maxRefusalMessageBytes + maxTriedEntries*(maxCredentialNameBytes+64)
)

// jobNameRE is the shape of a Cloud Run job name (credsvc's jobNameRE): what
// internal/backend/gcp sanitizes to, and what Cloud Run itself requires of a
// name.
var jobNameRE = regexp.MustCompile(`^[a-z](?:[a-z0-9-]*[a-z0-9])?$`)

// Service is how a service upstream identifies itself to the credentials
// service (D135): the job's Google identity, which Cloud Run IAM admits for
// run.invoker, and the run's own.
//
// Every field is required, and validate refuses a service upstream without
// one: the service verifies both identities on every call (C1.10), so an
// upstream that cannot present them cannot be served, and there is nothing
// else it could fall back to (D143).
type Service struct {
	// IDToken mints the job service account's Google ID token for the
	// service's URL (Upstream.BaseURL): the audience Cloud Run IAM checks.
	// It is called once per request — see metadataIDToken in
	// internal/runner for why the job does not cache it — and an error
	// means the call is not sent.
	//
	// It is called from concurrent calls, so it must be safe for that.
	IDToken func(ctx context.Context) (string, error)
	// RunIDToken is the run's own Firebase ID token, the budget identity
	// the launcher minted (C1.10's HeaderRunIdentity). It is read per
	// request — the session's token changes under it — and an empty value
	// means the call is not sent: an unauthenticated call to the service
	// under this run's other identity is not a call this gateway makes.
	//
	// It is called from concurrent calls, so it must be safe for that.
	RunIDToken func() string
	// Job is the Cloud Run job name the run reports it is in
	// (HeaderRunJob). The service holds it to the job-name rule and decides
	// nothing from it — which job called is decided from the verified
	// service-account email — so this is a claim, and validate refuses one
	// that could not be a job name.
	Job string
	// SlipID and SlipClaim are the claim the run holds: the slip's id and
	// the secret the claim route answered it (DS1.5). Both are presented on
	// every call (HeaderSlip, HeaderSlipClaim), which is what the service
	// serves a run's own credentials by (R9), and they are all or nothing:
	// validate refuses a service upstream carrying one without the other,
	// because a call with a slip and no secret is a request the service
	// does not read. A run that holds no slip carries neither, and is the
	// run of an installation that has not deployed the slip routes —
	// internal/cli's launcher hands it none to claim (DS1.4's skew).
	//
	// The secret is a credential and nothing here logs it: unlike IDToken it
	// is a value rather than a function because the run takes its slip once
	// before the gateway starts, and the claim does not change under a
	// running gateway.
	SlipID    string
	SlipClaim string
	// WorkspaceSlipID and WorkspaceSlipClaim are the workspace's own slip,
	// held exactly as the pair above is and presented under C1's names
	// (WorkspaceSlipHeader, WorkspaceSlipClaimHeader) to the workspace's
	// service and to no other (R4). They are a second pair rather than the
	// same one because the two slips have two owners and two grantors: a
	// member's slip is its own project's, and the workspace's is one only the
	// workspace grants (F13, R3).
	//
	// Which pair a Service carries is which service it calls, and a Service
	// carries one pair at most: Options.validate refuses a service upstream
	// holding both, a call to one service being made under one slip, and
	// Workspace.upstream is the only place the pair below is ever set. Both
	// pairs are all or nothing for the reason above — a call presenting an id
	// and no claim secret is a request the service does not read.
	WorkspaceSlipID    string
	WorkspaceSlipClaim string
}

// ServiceAnswer is what the credentials service said about one call: the
// tier that answered it, the credential's name, and the switch it made
// (C1.10's answer headers; C1.16 settles the call by the tier, C1.19 records
// the name, C1.16 logs the switch).
type ServiceAnswer struct {
	// Tier is the tier the answer was served from: TierA, TierB or TierC.
	Tier string
	// Credential is the name of the credential that answered, never its
	// value.
	Credential string
	// CredentialOwner is the owner of the credential that answered, never its
	// value (F16, credsvc.HeaderCredentialOwner): the account id of the
	// person whose credential it is, which the run records beside the name.
	// It is empty on an answer that carries none — a credential the launcher
	// does not own, a call the service admitted under no slip, or one a
	// service that predates the field answered — and the run then records no
	// owner.
	CredentialOwner string
	// Switch is the contract's switch header as it arrived: the JSON of a
	// Switch record, unread here. A switch is what the run's own log carries
	// (C1.16), and the runner reads it with the contract's own reader
	// (credsvc.SwitchHeader) rather than this package keeping a second
	// reading of it.
	Switch string
	// SwitchCost is what this answer's switch is estimated to cost (C1.16,
	// F24), computed here from the last call's cache reads and the tier
	// that answered, and zero when the answer carries no switch. It is the
	// record's "estimated cache-miss cost", and the service cannot supply
	// it: what the run has cached is the gateway's own knowledge.
	SwitchCost SwitchCost
	// Probed is every tier the service made a probe on while answering this
	// call (C1.12's HeaderProbe), once each and in the order it made them.
	// A probe is a call, so it is the run's to account for (C1.16), and the
	// service is the only place that knows it happened: its own answer is
	// closed unread, and its audit line is the service's record, not the
	// run's. The tiers are what the ledger needs and the names are not
	// carried -- which credential was probed is C1.13's line, and the run
	// learns the name of the credential that ANSWERED from Credential.
	Probed []string
	// Exhausted, when set, is the service's typed answer that no credential
	// is usable before its instant: this call was not served, and the one
	// thing to do about it is wait (C1.17). Every other field is then the
	// zero value: an exhausted answer carries no tier, and the credentials
	// it names are in Tried.
	Exhausted *Exhausted
	// Unreachable, when set, is a call this gateway could not reach the
	// service with at all (D2983): the call was not sent, and the one thing to
	// do about it is wait for the service until the instant it names. Every
	// other field is then the zero value.
	Unreachable *Unreachable
}

// Exhausted is the service's typed exhausted answer (credsvc's
// ExhaustedUntil): no credential is usable before ResetAt, and Tried is every
// credential the service considered, in the order it considered them, with
// why each was skipped.
//
// It is the one refusal that does not halt the run: a wait is what the runner
// does with it (C1.17), and D143's halt is for a service the run cannot reach
// or cannot be served by at all. Until the runner acts on it the call is
// answered 503 with no retry-after of its own, so the client backs off by its
// own schedule rather than sleeping for an instant the run has not agreed to
// wait for.
type Exhausted struct {
	ResetAt time.Time
	Tried   []Tried
	// Action is the action the service decided for this run (C3, R19): one of
	// ActionWait, ActionPause and ActionHalt, and empty from a service of a
	// release behind, which the runner waits out as it always has (D3184).
	Action string
	// ResumeAt is the instant a pause will be resumed at, zero for the other
	// two actions. It is the service's own figure and the run only reads it
	// (R8): pause.json carries no time, and the node the service writes is the
	// only record of it.
	ResumeAt time.Time
}

// Tried is one credential the service considered and did not use
// (credsvc's Tried).
type Tried struct {
	Name   string
	Tier   string
	Reason string
}

// Unreachable is a call this gateway could not reach the service with at all
// (D2983): no answer arrived, so nothing was refused and nothing was served.
// It carries nothing the service said, because the service said nothing.
//
// It is not a halt: what the run does with it is wait, which is the mechanism
// an exhausted answer uses (the runner ends the stage in flight, waits, and
// resumes it). Its one figure is the instant the wait ends, which is this
// package's own bound measured from the first call that could not reach the
// service (serviceUnreachableBound); a call that meets the spent bound is what
// halts the run, naming the service (stateDetailUnreachable or
// stateDetailUnreachableWorkspace).
type Unreachable struct {
	// RetryAt is when this run stops waiting for the service: the first call
	// that could not reach it, plus the bound. A call that meets a spent bound
	// is the halt.
	RetryAt time.Time
	// Service is which credentials service could not be reached: "project's"
	// or "workspace's". It is a fact about the route the run read at its start
	// and not an address, and it is here so the runner's own log line and the
	// run's record name the same service the halt names.
	Service string
}

// The two services a call can be sent to, as the run's log and its halt name
// them: which one a call went to is the route the run read at its start
// (Options.ModelFromWorkspace), never an address.
const (
	serviceNameProject   = "project's credentials service"
	serviceNameWorkspace = "workspace's credentials service"
)

// serviceCall reports whether this call is the credentials service's own, and
// so is read, answered and halted the way a service call is: the service's
// Messages path (no route at all beside a service upstream), and a SERVED
// provider route, whose key is the service's too (Served, KS5, R1/R12).
//
// A route the job holds the key for beside a service upstream is not one of
// these even though its kind's upstream is: it went to the provider's own
// endpoint with the job's key (a switched api-key run, R6/D1634), so its
// answer is the provider's, it is retried by this gateway, and a service
// refusal is not something it can be given.
func (s *Server) serviceCall(rt route) bool {
	return s.o.Upstream.Kind == KindService && (rt.provider == nil || rt.provider.Served)
}

// serviceUpstream builds a service call's headers and credential: the model
// request's own headers as the anthropic upstream would forward them, the
// job service account's Google ID token as the bearer credential Cloud Run
// IAM checks, the run's identity and the job name in the contract's headers,
// and nothing else. Both tokens are attached here and in no other place in
// the gateway.
//
// The invariant D143 rests on is that this call carries no credential of the
// job's own: validate refuses a service upstream that has one, and this
// function writes only the two identities above — the agent's own headers,
// the gateway token and every credential header Claude Code sent are already
// gone from what forwardHeaders returns. A route the job holds the key for is
// refused as well, on oauthUpstream's reasoning: such a route is a provider's
// own endpoint and is reached with that key, which a cascade run does not
// have. That call never reaches this function at all — upstreamRequest returns
// the provider's own request before the kind switch — and the check is kept so
// a route handed to it in code cannot have a service call built for it either.
//
// A SERVED route is the opposite case and does reach this function (KS5):
// its key is the service's, so the call this builds is the call a routed
// served call is made of — the two identities, and then the provider's name
// on top, which upstreamRequest sets from the route (HeaderProvider).
func (s *Server) serviceUpstream(ctx context.Context, rt route, in http.Header) (http.Header, error) {
	if rt.provider != nil && !rt.provider.Served {
		return nil, errServiceOnRoute
	}
	sv := s.o.Upstream.Service
	if sv == nil {
		return nil, fmt.Errorf("%w: the credentials service's identity isn't available", errServiceUnidentified)
	}
	id, run, slip, err := sv.serviceCallIdentity(ctx)
	if err != nil {
		return nil, err
	}
	h := forwardHeaders(in)
	s.scrubToken(h)
	// The service's own answer headers mean nothing in a request, and a
	// client that sent them must not have them arrive as if the service had
	// answered with them. The slip's two headers — and the workspace's own
	// pair beside them — are dropped for that reason and for one more: they
	// are the run's claim, taken by the runner and held here, so an agent
	// that sent a slip of its own must not have it reach either service —
	// what travels below is the claim the run took, and never a value the
	// agent chose. The run's own auth declaration is
	// dropped for that same second reason: the agent runs inside the
	// repository whose auth it is, so a mode or an account it wrote would
	// read as the runner's statement about a repository it may not be in —
	// what travels below is what the runner resolved for this run. The
	// provider's name (HeaderProvider) is dropped for it too: it is what
	// decides which of the repository's keys the service serves a routed
	// call with (R2, KS5), so it is set here from the route the gateway
	// routed to and never taken from a header the agent wrote.
	//
	// The chain wire's four names go the same way (chainwire.go). The stage
	// header is dropped whatever this gateway's ChainServed says and is then
	// set from the run's own stage: it is the whole of R24's guard against
	// answering in full a caller that is not a gateway of this contract, and
	// the service cannot tell the gateway's own from one the agent wrote, so
	// the drop is what makes it the gateway's (contract-table.md, "What the
	// guard is not": a gateway built before this rule has no drop, and no
	// later task may assume the window is closed for one). The three answer
	// members are dropped for the reason every answer header above is, and
	// the served model (HeaderModel) with them: they are the service's own
	// answer and mean nothing in a request, so a client that sent them must
	// not have them arrive as if the service had answered with them. No
	// header naming a model is ever SET here: the service reads the model a
	// call is for from the request's body (chainwire.go).
	for _, k := range []string{HeaderTier, HeaderCredential, HeaderCredentialOwner, HeaderSwitch, HeaderProbe, HeaderSlip, HeaderSlipClaim, WorkspaceSlipHeader, WorkspaceSlipClaimHeader, HeaderAuthMode, HeaderAuthAccount, HeaderProvider, HeaderStage, HeaderSource, HeaderFallback, HeaderModel} {
		h.Del(k)
	}
	h.Set("Authorization", "Bearer "+id)
	h.Set(HeaderRunIdentity, run)
	h.Set(HeaderRunJob, sv.Job)
	if mode := s.o.Upstream.AuthMode; mode != "" {
		h.Set(HeaderAuthMode, mode)
	}
	if account := s.o.Upstream.Account; account != "" {
		h.Set(HeaderAuthAccount, account)
	}
	if slip.id != "" {
		h.Set(slip.header, slip.id)
		h.Set(slip.claimHeader, slip.claim)
	}
	if s.o.ChainServed {
		// The run's current stage, from the run's own stage and never from a
		// header (the drop above): a caller that sends it is answered with the
		// chain's own accounting, which is what lets a step's record name the
		// source that served it (ChainAnswerFunc, CS8). A call with no stage
		// running carries the header not at all — the service then reads the
		// caller as one of this contract answering for no step, which is the
		// pre-contract answer — and a Messages call of a run is never one:
		// handleMessages refuses a call with no stage before it is forwarded.
		// A token count needs no stage (forwardFree), so one made with none
		// running meets that same pre-contract answer and records nothing.
		if stage := s.stageName(); stage != "" {
			h.Set(HeaderStage, stage)
		}
	}
	return h, nil
}

// serviceCallIdentity mints the identity every call this package makes to the
// credentials service presents (C1.10, D135): the job service account's Google
// ID token, which Cloud Run IAM consumes from Authorization, the run's own
// Firebase ID token, and the slip the run claimed, if it holds one. It is the
// one place the two identities are read, so the model path (serviceUpstream)
// and the runner's own call (Post, B3.4's workflow-secret fetch) cannot come to
// present different identities to the one service.
//
// An identity that cannot be assembled is errServiceUnidentified and nothing is
// sent: there is no credential of the job's to fall back to (D143), and a call
// the service would refuse for want of an identity is not one this package
// makes. Each slip is all or nothing for Upstream.validate's reason: a call
// presenting an id and no claim secret is a request the service does not read,
// and a Service carrying both slips is refused here too, a call to one service
// being made under one slip (R4).
func (s Service) serviceCallIdentity(ctx context.Context) (bearer, run string, slip serviceSlip, err error) {
	if s.IDToken == nil || s.RunIDToken == nil {
		return "", "", serviceSlip{}, fmt.Errorf("%w: the credentials service's identity isn't available", errServiceUnidentified)
	}
	run = s.RunIDToken()
	if run == "" {
		// An unauthenticated call to the service under the job's own Google
		// identity is not a call this package makes (D143): the run's identity
		// is what the service verifies the run with, and there is no second
		// way to be admitted.
		return "", "", serviceSlip{}, fmt.Errorf("%w: the run's identity token isn't available", errServiceUnidentified)
	}
	id, err := s.IDToken(ctx)
	if err != nil {
		return "", "", serviceSlip{}, fmt.Errorf("%w: the job's identity token isn't available: %v", errServiceUnidentified, err)
	}
	if id == "" {
		return "", "", serviceSlip{}, fmt.Errorf("%w: the job's identity token isn't available", errServiceUnidentified)
	}
	switch {
	case (s.SlipID == "") != (s.SlipClaim == ""):
		return "", "", serviceSlip{}, fmt.Errorf("%w: the run holds half a slip", errServiceUnidentified)
	case (s.WorkspaceSlipID == "") != (s.WorkspaceSlipClaim == ""):
		return "", "", serviceSlip{}, fmt.Errorf("%w: the run holds half the workspace's slip", errServiceUnidentified)
	case s.SlipID != "" && s.WorkspaceSlipID != "":
		// Two slips on one upstream: a call to one service is made under one
		// slip, and the header a call carries is what tells the two services
		// apart (R4). Nothing decides which of the two this call would be
		// under, so it is not sent.
		return "", "", serviceSlip{}, fmt.Errorf("%w: the service upstream carries a member's slip and the workspace's", errServiceUnidentified)
	case s.WorkspaceSlipID != "":
		slip = serviceSlip{id: s.WorkspaceSlipID, claim: s.WorkspaceSlipClaim, header: WorkspaceSlipHeader, claimHeader: WorkspaceSlipClaimHeader}
	default:
		slip = serviceSlip{id: s.SlipID, claim: s.SlipClaim, header: HeaderSlip, claimHeader: HeaderSlipClaim}
	}
	return id, run, slip, nil
}

// serviceSlip is the slip one call to a credentials service travels under: the
// id the launcher was granted, the claim secret the claim answered, and the two
// header names it travels in. header and claimHeader are empty for a run that
// holds no slip at all, and id is empty then too: the call carries neither name
// and is the run of an installation whose service serves no slip route.
//
// The names are C1's own (creds.PersonSlipHeader for the workspace's slip,
// credsvc.HeaderSlip for this project's), declared here a second time for the
// reason the constants are, and held to them by TestWorkspaceSlipMirrorsC1.
type serviceSlip struct {
	id          string
	claim       string
	header      string
	claimHeader string
}

// Post sends one signed call to a route of the credentials service and hands
// back the status and the answer, for a caller whose call is not a model call
// (B3.4: the runner fetches a workflow secret its job does not mount). It is
// the client of the same service the model path reaches, signed the same way:
// the two identities serviceCallIdentity assembles, the slip the run holds, and
// nothing else — no header of the caller's own travels, and this package writes
// no other one.
//
// The path and the body are the caller's, spelled from the contract's own file
// (credsvc's WorkflowSecretsPath, its request and its answer): this package
// cannot import internal/credsvc — the service imports this one — so the
// contract's text lives once, in that file, and never a second time here. What
// this method owns is the wire the contract travels on.
//
// The answer is bounded and the client follows no redirect: a followed 307 or
// 308 would carry the job's ID token to whatever host the Location named. The
// credential the answer may carry is returned to the caller and appears nowhere
// else — not in an error, not in a log line, and not in this package's own
// sentences, all of which name the call and never what it answered.
func (s Service) Post(ctx context.Context, baseURL, path string, body []byte) (status int, answer []byte, err error) {
	if err := checkBaseURL(baseURL); err != nil {
		return 0, nil, err
	}
	if !validJobName(s.Job) {
		// The service holds the reported job to the job-name rule (and decides
		// nothing from it, C1.10), so a call that could not report one is
		// refused here rather than sent to be refused there.
		return 0, nil, fmt.Errorf("%w: this call cannot report the job it is in", errServiceUnidentified)
	}
	if !strings.HasPrefix(path, "/") {
		return 0, nil, fmt.Errorf("gateway: %q isn't a path of the credentials service", path)
	}
	bearer, run, slip, err := s.serviceCallIdentity(ctx)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("gateway: the credentials service call could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set(HeaderRunIdentity, run)
	req.Header.Set(HeaderRunJob, s.Job)
	if slip.id != "" {
		req.Header.Set(slip.header, slip.id)
		req.Header.Set(slip.claimHeader, slip.claim)
	}
	resp, err := serviceCallClient.Do(req)
	if err != nil {
		// The cause is the transport's own and names no value: nothing of the
		// answer exists yet.
		return 0, nil, fmt.Errorf("gateway: the credentials service could not be reached: %v", err)
	}
	defer resp.Body.Close()
	answer, err = io.ReadAll(io.LimitReader(resp.Body, maxServiceAnswerBytes+1))
	if err != nil {
		return resp.StatusCode, nil, errors.New("gateway: the credentials service's answer could not be read")
	}
	if len(answer) > maxServiceAnswerBytes {
		// A bound rather than a read to the end: the answer of a route that
		// serves one value is small, and the service is another writer.
		return resp.StatusCode, nil, fmt.Errorf("gateway: the credentials service's answer is larger than the %d bytes this call reads", maxServiceAnswerBytes)
	}
	return resp.StatusCode, answer, nil
}

// maxServiceAnswerBytes bounds the answer Service.Post reads. It is far above
// the two fields of the contract's answer (credsvc.WorkflowSecretsResponse) and
// above Secret Manager's own 64 KiB value bound, so no honest answer of the
// route this client serves meets it, and an unbounded one never reaches the
// caller.
const maxServiceAnswerBytes = 128 << 10

// serviceCallClient is the client a Service.Post call is made with: the
// gateway's own upstream client — no redirect followed, no cookie jar kept —
// with the response-header bound serviceUpstreamClient states, so a service
// that accepts the connection and never answers is cut the same way on this
// route as on the model one. The caller's context bounds the call itself (a
// bootstrap fetch holds the whole thing under its own budget), and it is a var
// so a test can answer that question directly.
var serviceCallClient = serviceUpstreamClient(upstreamClient(nil))

// errServiceOnRoute is the refusal a service call gives a route the job holds
// the key for, from validateRoutes and from serviceUpstream. There is no cycle
// to break here (such a route's key is the job's own credential, which a
// cascade run does not have): it is refused by name so a misconfiguration says
// what it is, and in both places so a Route built in code past validation
// cannot reach a provider through a service run either. The one service
// upstream that does hold provider keys is a switched api-key run's, whose
// routes validateRoutes admits (B2.2, R6/D1634) — and whose routed calls the
// upstream path still sends to the provider's own address.
//
// It is about the UNSERVED route, and that is what its sentence says (KS5): a
// served route (Served) carries no key of the job's, and its call goes to the
// service's provider route by design (R1, R12) — validateRoutes admits it
// beside the service upstream and upstreamRequest builds its call there.
var errServiceOnRoute = errors.New("a provider route the job holds a key for is never reached through the credentials service: such a route is a provider's own endpoint and is called with the job's own key, which a cascade run does not have (D143)")

// errServiceUnidentified is every way a service call fails before it is sent
// for want of an identity: the run's own token, or the job's Google ID token,
// which only the metadata server mints. They are one error because the run's
// reaction to all of them is one -- there is no credential of the job's to
// fall back to (D143), so a call that cannot present an identity is a call
// that will never be admitted, and the run halts budget_unavailable-style
// rather than being answered 502 once per call until its deadline.
//
// Nothing is ever sent without both tokens: serviceUpstream attaches them and
// returns this error before any request exists, so a caller can only see it
// with nothing on the wire.
var errServiceUnidentified = errors.New("this job cannot present its identity to the credentials service")

// serviceResponseHeaderTimeout bounds how long a service call waits for the
// service's response headers: a connection that never answers at all is one
// this run cannot be served by, and without a bound it would hang until the
// stage's own deadline instead of halting (D143). It covers the wait for the
// headers only, never the body: the service streams a model answer, and the
// gateway never buffers a stream. The bound is generous on purpose -- the
// service may be cold-starting, and it may retry the same request on another
// credential before its first byte (C1.11) -- so it is a backstop for a
// connection that is gone, not a per-call limit. A var, like endStageWait and
// oauthResponseHeaderTimeout, so a test can shorten it.
var serviceResponseHeaderTimeout = 10 * time.Minute

// serviceUpstreamClient is the client a service call is made with: the
// gateway's own, with the response-header bound above. It states the bound
// here rather than inheriting it from the order Start happens to build its
// client in, exactly as oauthUpstreamClient does.
func serviceUpstreamClient(c *http.Client) *http.Client {
	out := *c
	tr := c.Transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	if t, ok := tr.(*http.Transport); ok {
		nt := t.Clone()
		nt.ResponseHeaderTimeout = serviceResponseHeaderTimeout
		out.Transport = nt
	}
	return &out
}

// validTier reports whether tier is one of the three of the contract.
func validTier(t string) bool { return t == TierA || t == TierB || t == TierC }

// validReason reports whether reason is one of the four of the contract's
// closed set.
func validReason(r string) bool {
	switch r {
	case ReasonExhausted, ReasonBroken, ReasonNotAdmitted, ReasonStopped:
		return true
	}
	return false
}

// validCredentialName reports whether name may be a credential's name: one
// clean header value at the contract's bound (credsvc.ValidCredentialName).
func validCredentialName(name string) bool {
	return len(name) <= maxCredentialNameBytes && printableValue(name)
}

// validJobName reports whether s may be the Cloud Run job a call reports it
// is in (credsvc.ValidJobName): a name this installation could have, bounded
// so that the two rules are one rule each.
func validJobName(s string) bool {
	return len(s) <= maxJobNameBytes && jobNameRE.MatchString(s)
}

// printableValue reports whether s is one clean header value: non-empty,
// bounded, printable ASCII with no space (credsvc's validHeaderToken), so it
// can be displayed and logged without a newline or a control character
// hiding in it.
func printableValue(s string) bool {
	if s == "" || len(s) > maxServiceHeaderBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// responseHeader returns the single value of an answer's header, present
// false when the answer carries none. An answer that carries it more than
// once is an error: the tier settles a call against the caps and the name is
// what the run records, so neither may be decided by header order (C1.10's
// responseHeader, which this mirrors).
func responseHeader(h http.Header, name string) (value string, present bool, err error) {
	values := h.Values(name)
	switch len(values) {
	case 1:
		return values[0], true, nil
	case 0:
		return "", false, nil
	}
	return "", false, fmt.Errorf("the answer carries the %s header more than once", name)
}

// credentialHeaders reads the tier and the credential's name back, holding
// them to the rules they were written by (C1.10's CredentialHeaders). ok is
// false for an answer that carries neither, which is most answers that are
// not a credential's; an answer carrying one without the other, an unknown
// tier or a name that is not one clean value is an error rather than a pair.
//
// No error here repeats a value: both arrive as header text from another
// process.
func credentialHeaders(h http.Header) (tier, name string, ok bool, err error) {
	tier, haveTier, err := responseHeader(h, HeaderTier)
	if err != nil {
		return "", "", false, err
	}
	name, haveName, err := responseHeader(h, HeaderCredential)
	if err != nil {
		return "", "", false, err
	}
	if !haveTier && !haveName {
		return "", "", false, nil
	}
	if !validTier(tier) {
		return "", "", false, errors.New("the answer's tier isn't one this contract carries")
	}
	if !validCredentialName(name) {
		return "", "", false, errors.New("the answer's credential name isn't one clean header value")
	}
	return tier, name, true, nil
}

// MaxCredentialOwnerDigits bounds the owner a credential's answer may name: the
// width of a Google account id — a number above 2^53, which is why
// internal/infra/tf's identity handling already carries the same number, a
// service account's unique_id, as text rather than rendering it. Twenty digits
// would be the width of a uint64, and these ids are wider than one, so a bound
// derived from a 64-bit number would refuse every launcher there is.
//
// It is THE definition of that width, and internal/credsvc imports it (its
// maxLauncherSubDigits, the launcher class's own spelling of the same bound):
// the service is the writer of the owner header and this package is its reader,
// the two ends of one contract are this repository's code, and a bound spelled
// twice is a bound that can drift with nothing red — the shape of the drift
// being an owner the run records that the service could not have written. The
// bound is a SHAPE check rather than a fence against a stranger's text: the
// owner is the launcher's `sub` claim, inside a token Google signed, so a
// caller cannot choose it — what the bound keeps out of the service's own two
// sinks (the slip's owner and the secret name fugaro_u_<id>_<name>, whose total
// length Secret Manager bounds) is an id Google has never issued. A value the
// issuer widens past this one day is refused rather than carried, which is the
// fail-closed answer, and widening it is this constant's change and the test
// that pins it.
const MaxCredentialOwnerDigits = 21

// ValidCredentialOwner reports whether s may be a credential's owner: the
// launcher's account id the contract writes it as, digits only and at most
// MaxCredentialOwnerDigits of them. The run records it (F16), so a value the
// service could not have written is refused rather than handed on. It is the
// one definition of the rule, and internal/credsvc's validSlipOwner delegates
// to it for the same reason the bound is here: one rule, both ends of the wire.
func ValidCredentialOwner(s string) bool {
	if s == "" || len(s) > MaxCredentialOwnerDigits {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// credentialOwner reads the owner of the credential that answered, present
// false for an answer that carries none — every answer that is not a
// slip-bound call's, and every answer a service that predates the field wrote.
// An owner that is not one launcher's account id is an error rather than a
// value: the run records it, so a caller that guessed at one would record a
// fact the service did not state. The error names the field and never the
// value.
func credentialOwner(h http.Header) (owner string, present bool, err error) {
	owner, present, err = responseHeader(h, HeaderCredentialOwner)
	if err != nil || !present {
		return "", present, err
	}
	if !ValidCredentialOwner(owner) {
		return "", false, errors.New("the answer's credential owner isn't one launcher's account id")
	}
	return owner, true, nil
}

// switchValue reads the contract's switch header, unread. An answer that
// carries it twice is an error for the same reason as above: which of two
// records is logged must not be decided by header order.
func switchValue(h http.Header) (string, error) {
	v, _, err := responseHeader(h, HeaderSwitch)
	return v, err
}

// maxProbedTiers is how many tiers the probe header can name: the tiers are a
// closed set of three and a call probes at most one credential per tier
// (C1.12). It is credsvc's own bound (maxProbedTiers), respelled because the
// package exports it only as the writer's rule; a list longer than it is not
// this contract's, and is refused rather than read.
const maxProbedTiers = 3

// probedTiers reads the tiers the service probed while answering this call
// (C1.12's HeaderProbe). No answer carrying none is the common case and is
// not an error: a call the service could serve without asking a backed-off
// credential anything probed nothing.
//
// Every refusal here is the reader's own sentence: the tiers arrive as header
// text from another process, and a tier that is not one of the contract's
// three is a value this ledger must not be asked to charge by -- the by-tier
// split has a leaf for B and C and none for anything else (C1.16).
func probedTiers(h http.Header) ([]string, error) {
	value, present, err := responseHeader(h, HeaderProbe)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > maxProbedTiers {
		return nil, errors.New("the answer's probe header names more tiers than this contract carries")
	}
	seen := make(map[string]bool, len(parts))
	for _, tier := range parts {
		if !validTier(tier) {
			return nil, errors.New("the answer's probed tier isn't one this contract carries")
		}
		if seen[tier] {
			return nil, errors.New("the answer's probe header names one tier twice")
		}
		seen[tier] = true
	}
	return parts, nil
}

// serviceRefusal is the JSON body of a refusal, the mirror of credsvc's
// WireError. Message is decoded and never read: the contract's own reader
// keeps the sentence out of the caller's hands, and this one does not even
// hand it on — a sentence built from the validated fields is what a halt
// says.
type serviceRefusal struct {
	Error   string         `json:"error"`
	Message string         `json:"message,omitempty"`
	ResetAt int64          `json:"reset_at,omitempty"`
	Tried   []serviceTried `json:"tried,omitempty"`
	// Action is the action the service decided for the run (C3), one of
	// ActionWait, ActionPause and ActionHalt, and ResumeAt is the instant a
	// pause will be resumed at in epoch milliseconds UTC. Both are written
	// only for a caller that sent the stage header (R24, F57), and a service
	// of a release behind writes neither: an answer without them is one this
	// gateway reads as it always has.
	Action   string `json:"action,omitempty"`
	ResumeAt int64  `json:"resume_at,omitempty"`
}

// serviceTried is one entry of a refusal's tried list (credsvc's Tried).
type serviceTried struct {
	Name   string `json:"name"`
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

// readServiceRefusal reads a non-2xx answer as the service's own refusal.
// ok is false when the answer is not one — a 2xx, or a body that does not say
// it is a refusal (claimsRefusal) — which is how a *model* error the service
// relayed is told from the service's own answer: that one is relayed to the
// agent as it always was, and a refusal never is.
//
// An error means the answer says it is a refusal and is not one this contract
// could have written: a body over the contract's bound, one that does not
// decode strictly, one with something after its JSON value, a message past
// its bound, or a tried list that breaks its rules. The caller must not act
// on it as a refusal — and must not relay it either, which is the whole point
// of asking here rather than at the relay: a refusal names the project's
// credentials, and a body that claims to be one is never put in front of the
// agent, whether or not this release can read it.
//
// The validating half is the contract's own (validateTriedList,
// ReadWireError, ReadExhaustedUntil), mirrored here; the two are held
// together by TestServiceContractMirrorsCredsvc.
func readServiceRefusal(status int, body []byte) (serviceRefusal, bool, error) {
	if status/100 == 2 {
		return serviceRefusal{}, false, nil
	}
	if !claimsRefusal(body) {
		return serviceRefusal{}, false, nil
	}
	// From here the body says it is this service's own refusal: it is acted
	// on as one or refused as one, and never relayed to the agent -- a body
	// this contract could not have written names the project's credentials,
	// and the agent has no business seeing the set of them (D143).
	if len(body) >= maxServiceRefusalBytes {
		// Longer than any refusal this contract can write.
		return serviceRefusal{}, true, errors.New("the refusal is longer than this contract's")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var e serviceRefusal
	if dec.Decode(&e) != nil {
		return serviceRefusal{}, true, errors.New("the refusal isn't a body this contract could have written")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return serviceRefusal{}, true, errors.New("the refusal carries something after its JSON value")
	}
	if len(e.Message) > maxRefusalMessageBytes {
		return serviceRefusal{}, true, errors.New("the refusal's message is longer than this contract's")
	}
	if err := validTriedList(e.Tried); err != nil {
		return serviceRefusal{}, true, err
	}
	return e, true, nil
}

// claimsRefusal reports whether a body says it is this service's own refusal:
// a JSON object whose "error" member is a non-empty string, and which is not
// the model API's own error envelope — {"type":"error","error":{...}} — which
// the service relays from the credential's upstream and which is another
// writer's body.
//
// It is deliberately loose where the reader above is strict. The question
// here is not "can this release read the refusal" but "is this the service's
// own text": a refusal this release cannot read halts the run rather than
// being relayed (readServiceRefusal's error), so a field a later service adds
// to a refusal body cannot put a sentence naming the project's credentials in
// front of the agent. No error is returned and no part of the body is echoed.
func claimsRefusal(body []byte) bool {
	var probe struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	// The first JSON value alone: a body with something after it still says
	// what it is, and the strict reader below is what refuses the trailer.
	if json.NewDecoder(bytes.NewReader(body)).Decode(&probe) != nil || probe.Type == "error" {
		return false
	}
	var typ string
	return json.Unmarshal(probe.Error, &typ) == nil && typ != ""
}

// modelErrorEnvelope reports whether a body is the model API's own error
// envelope -- {"type":"error","error":{"type":...,"message":...}} -- which the
// service relays from the credential's upstream unchanged (C1.11). It is the
// one non-2xx body that is another writer's and is meant for the agent, and it
// is what separates a relayed model error from the platform's own front door:
// both arrive as 401 or 403, and the two must not be treated alike.
//
// It is as strict as the relay allows, because the two mistakes are not equal.
// Failing to recognise a relayed model error stops a run that another
// credential could serve and hides the model's own sentence from the agent;
// failing to recognise the platform's front door relays a body that names the
// project and the caller, which is the one thing D143 refuses. So the envelope
// has to be there in full: the type that identifies it, and the error object
// inside it with a type of its own, which is the shape the model API writes and
// the platform's own JSON ({"error":{"code":...,"status":...}}) does not.
func modelErrorEnvelope(body []byte) bool {
	var probe struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	// The first JSON value alone, as claimsRefusal reads it: a body with
	// something after it still says what it is, and a relayed body is passed
	// through whole whatever follows.
	if json.NewDecoder(bytes.NewReader(body)).Decode(&probe) != nil {
		return false
	}
	return probe.Type == "error" && probe.Error.Type != ""
}

// validTriedList holds a tried list to the contract's rules: the count bound
// first, then every entry by name, tier and reason. It is what the contract's
// own reader asks of every refusal body (credsvc.validateTriedList): the
// names reach a run's halt message, which is text a human reads.
func validTriedList(tried []serviceTried) error {
	if len(tried) > maxTriedEntries {
		return fmt.Errorf("the refusal carries %d tried credentials, over this contract's bound of %d", len(tried), maxTriedEntries)
	}
	for _, tr := range tried {
		if !validCredentialName(tr.Name) {
			return errors.New("a tried credential's name isn't one credential name")
		}
		if !validTier(tr.Tier) {
			return errors.New("a tried credential's tier isn't one this contract carries")
		}
		if !validReason(tr.Reason) {
			return errors.New("a tried credential's reason isn't one this contract carries")
		}
	}
	return nil
}

// exhaustedAnswer is the typed exhausted answer as this package hands it on,
// or an error when the answer is not one this contract could have written:
// the instant is the one thing the runner waits for, so an answer without it,
// or with a list the contract does not allow, is not a wait.
func exhaustedAnswer(e serviceRefusal) (Exhausted, error) {
	if e.ResetAt <= 0 {
		return Exhausted{}, errors.New("the exhausted answer carries no reset instant")
	}
	tried := make([]Tried, 0, len(e.Tried))
	for _, tr := range e.Tried {
		tried = append(tried, Tried{Name: tr.Name, Tier: tr.Tier, Reason: tr.Reason})
	}
	a := Exhausted{ResetAt: time.UnixMilli(e.ResetAt).UTC(), Tried: tried, Action: e.Action}
	switch e.Action {
	case "":
		// A service of a release behind decides no action, and the answer is
		// the one this contract had before the pause existed: the run waits.
	case ActionWait, ActionHalt:
		// Neither is a pause, so neither may carry a resume instant (C3): an
		// answer that names one is not one this contract wrote, and half of a
		// pause is not acted on.
		if e.ResumeAt != 0 {
			return Exhausted{}, errors.New("only a pause carries a resume instant")
		}
	case ActionPause:
		// The instant the service will resume the run at is the whole of what
		// a pause tells the job about the future, and it is the service's own
		// figure: a pause without it names nothing the run could act on.
		if e.ResumeAt <= 0 {
			return Exhausted{}, errors.New("the pause answer carries no resume instant")
		}
		a.ResumeAt = time.UnixMilli(e.ResumeAt).UTC()
	default:
		// An action outside the closed set is a value neither side of this
		// contract wrote. The run follows the action it is given (R19), so an
		// answer naming one this gateway cannot place is one it refuses rather
		// than waits on a guess.
		return Exhausted{}, errors.New("the exhausted answer names no action this gateway knows")
	}
	return a, nil
}

// serviceAnswer reads what the credentials service said about a call and acts
// on it, before any of the answer reaches the agent. It reports the status
// the agent's answer was written with and done true when the answer was dealt
// with here — a refusal, a halt, or an answer this gateway cannot read — and
// done false when the answer is the model's own and the caller relays it.
//
// needTier is a Messages call's answer: it settles by the tier C1.16 charges
// it at, so an answer that carries none (or a pair the contract would not
// write) halts the run rather than settling by a guess. A token count is free
// and settles nothing, and asks for nothing.
//
// servedRoute is an answer the service's provider route relayed from a
// provider (KS5, R1/R12): this run's route is served, so the call went to the
// service with the provider's name on it and what came back is the provider's
// own answer — not the service's text about this job. Nothing in it is acted
// on here except the contract's own typed refusals, which are the closed set
// the switch below decides by; any other non-2xx answer is done false, so the
// caller relays it exactly as it relays a provider's error on the path where
// the job holds the key (providerError: the error type, and a sentence this
// gateway writes — never a byte of the body, and an allow-list of headers).
// The readings that are a Messages call's alone are skipped for it: the
// platform's front-door 401/403 (stateDetailUnadmitted) would otherwise halt
// the run for the provider's own moderation refusal or exhausted key, which
// the mount path relays and another model or another call continues after.
//
// The probes the answer reports (HeaderProbe, C1.12) come back with the tier:
// each one is a call the run paid for and settles the same way the call does,
// by the tier it was made on (C1.16).
//
// A non-2xx answer is read as a refusal only when it is one; a model error
// the service relayed is its own writer's text, reaches the agent as it does
// today, and its body is put back on the response for the relay to stream. A
// refusal is never relayed: it is fugaro's own answer, it names the project's
// credentials, and the agent has no business seeing the set of them (D143).
func (s *Server) serviceAnswer(w http.ResponseWriter, resp *http.Response, needTier, servedRoute bool) (status int, done bool, tier string, probed []string) {
	// What this answer cleared, because an answer of the service's own is not
	// always the service being back: a workspace's service that REFUSES a call
	// as unable to read its roster answered and is still not serving this run,
	// and that refusal is the outage the wait is for (workspaceUnavailable,
	// keepUnreachable).
	outage := s.noteServiceReached()
	if resp.StatusCode/100 == 2 {
		// The room the workspace's service has left arrives on the answer
		// itself, so it is read here, before any of it reaches the agent
		// (O1). It is advisory and the gateway decides nothing with it: the
		// service refuses the call at the ceiling whatever this gateway
		// believes, and a call the service did not pay for carries no
		// number and changes nothing.
		s.noteWorkspaceHeadroom(resp.Header)
		if s.o.ChainServed {
			// The answer's chain members (chainwire.go), read here where the
			// service's other answer headers are and before any of it reaches
			// the agent: which source served this call, whether it was the
			// chain's first, and which model served. They are read for every
			// served call of this gateway — a token count, a provider route's
			// relayed answer and a Messages call alike — because the step's
			// record folds the fallback flag over the whole stage (CS8), and
			// an answer that names one and not the others is a halt rather
			// than a value the run records a guess from.
			a, present, err := chainAnswerHeaders(resp.Header)
			if err != nil {
				return s.haltService(w, stateDetailUnreadable), true, "", nil
			}
			if present {
				// An answer of a service built before this contract carries
				// none of the three and is not an error: this gateway sent the
				// stage header, and a service that does not read it answers in
				// the shape it always did.
				s.answerChain(a)
			}
		}
		if !needTier {
			return resp.StatusCode, false, "", nil
		}
		tier, name, ok, err := credentialHeaders(resp.Header)
		if err == nil && !ok {
			err = errors.New("the answer carries no tier")
		}
		if err != nil {
			return s.haltService(w, stateDetailUnreadable), true, "", nil
		}
		owner, _, err := credentialOwner(resp.Header)
		if err != nil {
			// The owner is what the run records beside the credential's name
			// (F16), so an answer whose owner cannot be read is one the run
			// must not record a guess from: it halts like an answer whose
			// tier cannot be read. An answer that carries no owner is the
			// ordinary case and not an error.
			return s.haltService(w, stateDetailUnreadable), true, "", nil
		}
		sw, err := switchValue(resp.Header)
		if err != nil {
			return s.haltService(w, stateDetailUnreadable), true, "", nil
		}
		if probed, err = probedTiers(resp.Header); err != nil {
			// An answer whose probes cannot be read is a halt rather than a
			// ledger input dropped: the call was made and somebody paid for
			// it, and a run that cannot say so would report a spend it did
			// not make.
			return s.haltService(w, stateDetailUnreadable), true, "", nil
		}
		a := ServiceAnswer{Tier: tier, Credential: name, CredentialOwner: owner, Switch: sw, Probed: probed}
		if sw != "" {
			// A switch's cache-miss cost is the gateway's to estimate
			// (F24): it is computed here, where the last call's usage is,
			// and handed to the run with the record the service wrote.
			a.SwitchCost = s.switchCost(tier)
		}
		s.answer(a)
		return resp.StatusCode, false, tier, probed
	}
	head, rerr := io.ReadAll(io.LimitReader(resp.Body, maxServiceRefusalBytes))
	// Whatever this answer turns out to be, the reader above has consumed
	// its first bytes: they go back in front of the rest, so a relayed body
	// arrives whole and the pump streams it as it always did.
	resp.Body = replayBody{Reader: io.MultiReader(bytes.NewReader(head), resp.Body), Closer: resp.Body}
	// The workspace's own marker, if this refusal carried one: which of the
	// workspace's refusals the service answered (workspace_refusal.go). It is
	// read here, off the refusal itself, and from nowhere else -- no reason,
	// scope or detail is ever read as a marker, because a member's own
	// credentials service writes all three the same way. An answer that
	// carries no marker, or one this release does not know, marks nothing.
	wsRefusal, _ := WorkspaceRefusal(resp.Header)
	// haltOn is the halt this refusal forces, with whatever the service said
	// about itself recorded on it. Every halt below goes through it, so a
	// refusal the workspace marked is marked wherever it was answered.
	haltOn := func(detail string) int { return s.haltServiceMarked(w, detail, wsRefusal) }
	if rerr != nil {
		// A body that broke mid-read, before any of it reached the agent:
		// not a refusal this code can read, and not one to relay. A served
		// call's answer is the provider's whatever state it arrived in, and
		// the relay below writes a sentence of this gateway's and the type
		// the status implies, so nothing of it is lost by handing it on.
		if servedRoute {
			return resp.StatusCode, false, "", nil
		}
		return haltOn(stateDetailUnreadable), true, "", nil
	}
	e, ok, err := readServiceRefusal(resp.StatusCode, head)
	if servedRoute && (!ok || err != nil) {
		// Not this contract's refusal, on a call whose answer is the
		// provider's: it is relayed rather than read. The two readings that
		// follow this branch — the platform's front door on a 401 or 403,
		// and a body that claims to be a refusal and is not one this release
		// can read — belong to a Messages call, where the body really is the
		// service's own about this job. A provider writes both shapes: a 403
		// whose error is OpenRouter's own object is its moderation refusal,
		// and a string error is its own text as well, and the mount path
		// relays both without stopping the run.
		return resp.StatusCode, false, "", nil
	}
	if !ok {
		// Not this contract's refusal. A 401 or 403 that is not the model
		// API's own envelope either is the platform's layer answering: Cloud
		// Run IAM refusing this job's invoker grant, or a proxy in front of
		// the service. The contract's own 403 (unverified_job_identity) was
		// read above, so what is left here is a caller the service never
		// admitted. It is not relayed -- the body is the platform's, and it
		// names the project and the caller -- and it is not a model error the
		// service passed on: every call this run makes will be refused the
		// same way, so the run halts budget_unavailable-style (D143) rather
		// than being answered 401 once per call.
		//
		// A credential's own 401 or 403 is the other thing that arrives with
		// these statuses, and it is the model's answer about the credential,
		// not the platform's about the caller. C1.11 acts on two answers
		// itself -- a usage limit, and a 401 that marks the credential broken
		// (D139) -- and everything else it relays as it found it, so a 403
		// from the credential's upstream is the model's and reaches the agent
		// as it always did. The run is not stopped by it either: another
		// credential, or a person, may be what fixes it.
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && !modelErrorEnvelope(head) {
			// Which of the two services answered decides which grant the
			// detail names: the member's own job on its own project's service,
			// or the same job on the workspace's, where only the workspace
			// owner's own `fugaro update` can grant it (R16).
			if s.wsModel {
				return haltOn(stateDetailUnadmittedWorkspace), true, "", nil
			}
			return haltOn(stateDetailUnadmitted), true, "", nil
		}
		return resp.StatusCode, false, "", nil // a model error the service relayed
	}
	if err != nil {
		return haltOn(stateDetailUnreadable), true, "", nil
	}
	switch e.Error {
	case ErrorExhaustedUntil:
		ex, err := exhaustedAnswer(e)
		if err != nil {
			return haltOn(stateDetailUnreadable), true, "", nil
		}
		// The wait is not a halt: the runner decides what to do about it
		// (C1.17), and until it does the call is answered retryably and
		// without a retry-after of its own, so the client backs off by its
		// own schedule.
		s.answer(ServiceAnswer{Exhausted: &ex})
		writeError(w, http.StatusServiceUnavailable, "api_error",
			"fugaro: the credentials service has no credential available before "+ex.ResetAt.UTC().Format(time.RFC3339))
		return http.StatusServiceUnavailable, true, "", nil
	case ErrorStateUnavailable:
		if s.wsModel && s.workspaceSlipHeld() && wsRefusal == "" {
			// R3, D2983: the workspace's service re-reads its roster on every
			// call under its slip, and a roster it could not read is refused as
			// the service being unable to read the state it must read — never as
			// a verdict about this run — so this run waits for it exactly as it
			// waits for a workspace service it cannot reach, and moves to no
			// other credential meanwhile. The run's own project's service is
			// unchanged: its "cannot read the state" is D143's halt, as it was.
			//
			// The re-read is the slip's (F13, R4): a run routed to the
			// workspace that holds no slip made no roster read for this call to
			// fail, so its refusal is the state the service could not read, the
			// halt it always was, and the wait is not extended to it.
			//
			// An answer the workspace MARKED is not this refusal at all, and it
			// halts below as it always did (workspace_refusal.go): the
			// workspace's own cap, stop and settle reach a member as this same
			// 503 state_unavailable, and each is a verdict about the call and
			// not an outage. Waited for, one would spend the bound on a service
			// that answered and then halt naming the roster with no marker on
			// it — losing the fact the halt is read for (D2022's O19).
			//
			// The clock is put back, because this refusal is the outage itself
			// and not an answer that ends it: a bound re-armed by every refusal
			// is a wait that never spends it, and the halt below — the roster,
			// named — would never be reached (noteUnreachable, R3).
			s.keepUnreachable(outage)
			s.workspaceUnavailable(w)
			return http.StatusServiceUnavailable, true, "", nil
		}
		return haltOn(refusalDetail(e.Error)), true, "", nil
	case ErrorNoCandidateCredentials:
		// The one refusal whose detail is built from the answer rather than
		// from the type alone (noCredentialDetail): which of the filters
		// removed the candidates is the whole diagnosis, and a run told only
		// "no credential" is a run whose operator greps a log with nothing in
		// it. The names stay out of the detail and go to this job's own log
		// (triedLog), which is where the run's operator reads them and where
		// the agent cannot.
		s.log.Warn("budget: the credentials service had no credential to offer a call",
			"stage", s.stageName(), "tried", triedLog(e.Tried))
		return haltOn(noCredentialDetail(e.Tried)), true, "", nil
	case ErrorRequestRefused:
		// The service's request check refused the call itself, and the one
		// cause a forwarded call can really have is a project that publishes
		// no allow-list: the gateway pins a call to the stage's models, and
		// the pins were held to the project's list when the run started, so
		// the service refuses what this gateway forwarded only when that list
		// is absent (the owner's ruling of 2026-10-22) or has moved under a
		// running run. The detail says so, because the service's own sentence
		// never reaches the agent (D143): without it the operator reads a
		// detail about a broken service instead of the missing allow-list.
		return haltOn(refusalDetail(e.Error)), true, "", nil
	case ErrorSlipOwnerRemoved:
		// D3146, R3: the workspace's scope, refusing a call under the slip of
		// someone its roster no longer holds. The detail names the removal and
		// the halt is the run's end — it moves to no other credential, which is
		// the whole of what makes this a halt and not a switch.
		return haltOn(refusalDetail(e.Error)), true, "", nil
	case ErrorRepositoryNotListed:
		// R19: the workspace's scope, refusing a repository its own record of
		// the calling job account does not name. The detail names the
		// workspace owner's own step, as the platform's 403 above does and for
		// the same reason: nothing on this side of the workspace can add a
		// repository to that list.
		return haltOn(refusalDetail(e.Error)), true, "", nil
	case ErrorUnverifiedRunIdentity, ErrorUnverifiedJobIdentity:
		return haltOn(refusalDetail(e.Error)), true, "", nil
	case ErrorProviderNotServed, ErrorProviderNotAllowed:
		// The provider route's own two refusals (C1, KS5): the service holds
		// no key for this repository and this provider, or the published
		// policy does not allow the call. Both halt the run (D143): this job
		// carries R12's fact, which is the owner's own record that the keys
		// are served, so an absent key there is a snapshot of the
		// installation and not a state a run papers over with a mounted key
		// -- and there is none mounted for a served route in any case.
		return haltOn(refusalDetail(e.Error)), true, "", nil
	}
	if servedRoute {
		// A body that says it is a refusal and carries a type outside the
		// closed set the switch above decides by: on a served call that is
		// the provider's own answer — its error member is a string, or a
		// shape of its own — and it is relayed as the mount path relays one,
		// which keeps the type it reads and writes a sentence of this
		// gateway's in place of the body.
		return resp.StatusCode, false, "", nil
	}
	// A refusal whose type this release does not know. It is the service's
	// own answer and is not relayed, and the call cannot be accounted for
	// either: stop rather than improvise (D143's posture). The type is from
	// a closed set the service writes, and is not echoed into the detail.
	return haltOn(stateDetailUnreadable), true, "", nil
}

// replayBody is a response body whose first bytes were read and put back.
type replayBody struct {
	io.Reader
	io.Closer
}

// The details a service halt is recorded with. They are constants: a halt's
// Detail is displayed to the agent in every later call's 403 and stored in
// the run's record, and nothing the service sent is ever echoed into it.
const (
	// stateDetailUnreachable is the project's credentials service, which this
	// job could not reach for the whole of the bound (D2983). It is the
	// sentence a run of a repository whose model kind is served by its own
	// project reads, and the one the live checklist reads for that case; the
	// workspace's own is stateDetailUnreachableWorkspace below.
	stateDetailUnreachable = "the credentials service could not be reached"
	// stateDetailUnreadable is an answer this gateway could not read as the
	// service's own, or could not account a call by.
	stateDetailUnreadable = "the credentials service answered with something this gateway cannot read"
	// stateDetailUnadmitted is the service's own front door refusing this
	// job: an answer that is neither a model answer nor the contract's typed
	// refusal, on the two statuses the platform's IAM layer uses.
	stateDetailUnadmitted = "the credentials service refused this job's call (check the job's invoker grant on the service)"
	// stateDetailUnidentified is a call this job could not present its
	// identity on: the run's own token, or the Google ID token the metadata
	// server mints. Nothing was sent.
	stateDetailUnidentified = "this job could not present its identity to the credentials service (the metadata server or the run's own identity was not available)"
	// stateDetailUnadmittedWorkspace is stateDetailUnadmitted for a run whose
	// model calls go to the workspace's credentials service (R16): the
	// platform's own front door refuses every call this job makes, which is a
	// job the workspace has not granted run.invoker yet — the expected state of
	// a member that has just added a repository, because its own `fugaro update`
	// publishes the route that sends the repository here and the workspace
	// owner's has not admitted the repository's job (F17). It is not waited for:
	// every later call would be refused the same way. The sentence names the
	// owner's step rather than the operator's own, because the member's operator
	// cannot grant it: the workspace's project is not theirs.
	stateDetailUnadmittedWorkspace = "the workspace's credentials service refused this job's call: this repository's job is not granted on it yet, and the workspace owner's own 'fugaro update' in the workspace's project is what grants it (nothing this run or its own repository's operator does reaches that project)"
	// stateDetailUnreachableWorkspace is stateDetailUnreachable for a run whose
	// model calls go to the workspace's credentials service (D2983, R16): the
	// bound was spent without one answer from it. It names that service rather
	// than "the credentials service" because the two are different projects
	// with different owners, and a member's operator told only that "the
	// credentials service" could not be reached would look at their own.
	stateDetailUnreachableWorkspace = "the workspace's credentials service could not be reached"
	// workspaceNotListedDetail is R19's halt: the workspace's scope refuses a
	// repository its own record of the calling job account does not name. Like
	// stateDetailUnadmittedWorkspace it names the workspace owner's own step,
	// which is the one thing that adds a repository to that list.
	workspaceNotListedDetail = "the workspace's credentials service does not serve this repository (repository_not_listed): the workspace's record of this job account does not name it, and the workspace owner's own 'fugaro update' in the workspace's project is what adds it"
	// slipOwnerRemovedDetail is D3146's halt: the workspace's service re-reads
	// the workspace's roster as it re-reads its published node, and this call's
	// slip belongs to someone that re-read no longer holds. It names the
	// removal, which is the whole of what the run's operator is owed — the
	// person's own admin is the one who restores a role — and it says what the
	// run does not do: it moves to no other credential, since a slip refused
	// this way is not a credential that failed but a person who left.
	slipOwnerRemovedDetail = "the workspace's credentials service refused this call because the slip's owner has left the workspace's roster (slip_owner_removed): the run halts and is served no other credential (D3146)"
	// stateDetailWorkspaceRoster is what a run that waited out the bound for a
	// workspace's service which answers "I cannot read the state I must read" is
	// halted with (R3, D2983). It is not stateDetailUnreachableWorkspace: that
	// one says the service could not be reached, and this one answered. What it
	// could not do is read the workspace's roster, and the two sentences are
	// apart so an operator looks at the workspace's own state rather than at
	// whether the revision is up.
	stateDetailWorkspaceRoster = "the workspace's credentials service cannot read the workspace's roster"
)

// refusalDetail is the detail for a typed refusal the run cannot be served
// by: the type, which is one of the contract's own constants, and what the
// operator is to look at — never anything the service sent, and never a
// credential. Every sentence here is a constant, so a refusal's Detail is the
// same text whatever the service's own body said.
func refusalDetail(typ string) string {
	switch typ {
	case ErrorStateUnavailable:
		return "the credentials service cannot read the state it must read (state_unavailable)"
	case ErrorUnverifiedRunIdentity:
		return "the credentials service refused this run's identity (unverified_run_identity)"
	case ErrorUnverifiedJobIdentity:
		return "the credentials service refused this job's identity (unverified_job_identity)"
	case ErrorRequestRefused:
		// The service's request check refused the call, and its own sentence
		// is never relayed (D143): this is the sentence the agent and the
		// run's record get instead. It names the way out first, because the
		// cause a forwarded call really has is a project that publishes no
		// budget.allowed_models (the owner's ruling of 2026-10-22) — and it
		// does not claim that is the only one, because the same type answers
		// for a server tool, an unbounded shape and a max_tokens bound.
		return "the credentials service refused this request (request_refused: check the project's published budget.allowed_models and this call's model, tools and max_tokens)"
	case ErrorProviderNotServed:
		// The provider route's own refusal (C1, KS5), and the operator's two
		// documents are named because the cause is one of two: an entry the
		// bundle does not hold for this repository and provider, or one whose
		// value the copy never wrote. Neither is a key's value and neither is
		// a credential's name.
		return "the credentials service holds no key for this repository's provider (provider_not_served: check the installation's bundle for this repository's provider entry and the copy's record, then run fugaro update)"
	case ErrorProviderNotAllowed:
		// The other one: the key is held and the published policy is what
		// refuses the call (R11, D3003), so the reader is sent to the
		// allowance and never to a key.
		return "the credentials service refused this provider call (provider_not_allowed: check the project's published allowance and this repository's own admission)"
	case ErrorRepositoryNotListed:
		return workspaceNotListedDetail
	case ErrorSlipOwnerRemoved:
		return slipOwnerRemovedDetail
	}
	return stateDetailUnreadable
}

// noCredentialDetail is the detail for a call the service had no credential to
// offer. Unlike every other detail here it is built from the answer, because
// the cause IS the answer: which filter removed the credentials is what tells
// the operator whether to look at the project's published policy, the
// installation's bundle or the repository's own local config, and a run told
// only that no credential existed is a run whose operator greps a log that
// says nothing.
//
// What is built from the answer is only the set of reasons, and each one is
// rendered by noCredentialCause, whose every sentence is a constant of this
// package: no free text, no credential NAME and nothing else the service sent
// reaches a Detail, which the agent is shown and the run's record keeps (D143).
// The names are in this job's own log instead (triedLog), which the agent
// never reads.
func noCredentialDetail(tried []serviceTried) string {
	if len(tried) == 0 {
		// Nothing was dropped because nothing was there: the bundle the
		// service loads holds no model credential at all, which is a fact
		// about the installation and not about the policy.
		return "the credentials service had no credential to offer this call (no_candidate_credentials): the installation's bundle holds no model credential, so there was nothing this policy could admit (check the bundle with the credentials service's own log line for this run)"
	}
	causes := noCredentialCauses(tried)
	return "the credentials service had no credential to offer this call (no_candidate_credentials): " +
		strings.Join(causes, "; ") +
		" — check the project policy's credentials.accounts, each account's allow_repos and this repository's own claude_accounts, and the credentials service's own log line for this run, which names every credential it considered"
}

// noCredentialCause is one tried reason as a sentence of this package's own.
// Every value it answers is a constant here, and the reason reaches it only
// after validTriedList held the body to the contract's own closed set: a reason
// this release does not carry is a detail that says the answer could not be
// read rather than a word the service chose.
func noCredentialCause(reason string) string {
	switch reason {
	case ReasonNotAdmitted:
		return "not one of the credentials this repository's policy admits to it (not_admitted)"
	case ReasonExhausted:
		return "the credentials it may use are all marked exhausted (exhausted)"
	case ReasonBroken:
		return "the credentials it may use are all marked broken (broken)"
	case ReasonStopped:
		return "the credentials it may use are stopped by an operator's switch (stopped)"
	}
	return stateDetailUnreadable
}

// noCredentialCauses is the distinct reasons a tried list carries, rendered in
// this package's own order and never in the order the answer happened to list
// them, so the same refusal is the same detail whatever order it was built in.
func noCredentialCauses(tried []serviceTried) []string {
	present := make(map[string]bool, len(tried))
	for _, tr := range tried {
		present[tr.Reason] = true
	}
	out := make([]string, 0, len(present))
	for _, r := range []string{ReasonNotAdmitted, ReasonExhausted, ReasonBroken, ReasonStopped} {
		if present[r] {
			out = append(out, noCredentialCause(r))
		}
	}
	return out
}

// triedLog renders a refusal's tried list for this job's own log: the names the
// service considered and the reason each was not used, one line. It is the
// diagnosis that stays out of the halt's detail (noCredentialDetail) because
// the Detail is displayed to the agent, and it is what an operator greps the
// run's log for — the service's own line for the same refusal carries the same
// list, so the two can be read against each other.
func triedLog(tried []serviceTried) string {
	parts := make([]string, 0, len(tried))
	for _, tr := range tried {
		parts = append(parts, logValue(tr.Name)+" ("+tr.Reason+")")
	}
	return strings.Join(parts, ", ")
}

// haltService records a halt the service's own answer forces, writes the
// refusal the agent's call is answered with -- the first halt's message, which
// is this one's unless a halt came first -- and returns its status. The first
// halt of any kind stands, and a halt the gateway decided itself does not
// cancel the calls in flight, exactly as a run-cap halt does not: each of them
// settles by its own answer.
func (s *Server) haltService(w http.ResponseWriter, detail string) int {
	return s.haltServiceMarked(w, detail, "")
}

// haltServiceMarked is haltService for an answer that carried the workspace's
// own marker: the same halt, with the refusal the workspace's credentials
// service named recorded on it (workspace_refusal.go). workspace is "" for
// every answer that carried no marker, which is every refusal that is not the
// workspace's and every answer from a release before the marker existed, and
// the halt is then the one haltService writes.
//
// The marker changes nothing about the halt but the fact it carries: the
// reason, the scope, the detail and the message are the ones a refusal the
// service did not mark gets, so a run refused by a workspace stops exactly
// where it stopped before and only its record says who refused it.
func (s *Server) haltServiceMarked(w http.ResponseWriter, detail, workspace string) int {
	s.mu.Lock()
	if s.halt == nil {
		h := Halt{Reason: ReasonBudgetUnavailable, Scope: "run", Detail: detail, WorkspaceRefusal: workspace, At: time.Now().UTC()}
		s.haltLocked(h, "fugaro: budget halted: "+haltText(h), s.stageNameLocked(), true)
	}
	msg := s.haltMsg
	s.mu.Unlock()
	writeHalt(w, msg)
	return http.StatusForbidden
}

// serviceUnreachable is what a call the gateway could not reach the service
// with is answered with (D2983): the run waits for the service and retries, for
// at most serviceUnreachableBound from the first call that could not reach it,
// and a call that meets the spent bound halts the run with a reason that names
// which service it was. It never reaches for a credential of the job's own —
// there is none to reach for, and D143 is why there is none.
//
// The wait is the runner's, and its mechanism is the one an exhausted answer
// already uses (C1.17): the Unreachable answer below ends the stage in flight
// through the run's own hook, stage waits where the answer says and resumes the
// stage's session, and that retry — not a second send here — is what makes the
// outage survivable. Nothing in this package sends the call again: a service
// that is not answering is not a failure a second send fixes, and holding the
// agent's call for the whole bound would hold the run's own stage in a request
// nobody can cancel.
//
// So the call itself is answered the way an exhausted one is — 503, retryable,
// with no retry-after of its own, so the agent backs off by its own schedule
// for the moment it takes the run to end the stage — and the run learns of the
// wait on the same hook its typed answers arrive on.
//
// Which service is named is which upstream this gateway calls: a run whose
// model kind is routed to the workspace has the workspace's upstream IN
// Options.Upstream (Start, Options.ModelFromWorkspace), so wsModel is the whole
// of the question and no address is compared here.
//
// It reports whether this call's answer is the gateway's own — the wait's 503
// or the halt, both written here — which is what the caller returns on: an
// external halt or a cancelled request came first, and then the agent's answer
// is the one that cause wrote. The wait itself is answerServiceWait, which
// serviceAnswer reaches too: a workspace service that REFUSED a call as
// unavailable is waited for the same way (workspaceUnavailable, R3).
func (s *Server) serviceUnreachable(w http.ResponseWriter, base, req context.Context) (answered bool) {
	if req.Err() != nil || base.Err() != nil {
		return false
	}
	detail, service := stateDetailUnreachable, serviceNameProject
	if s.wsModel {
		detail, service = stateDetailUnreachableWorkspace, serviceNameWorkspace
	}
	s.answerServiceWait(w, detail, service, "could not be reached")
	return true
}

// workspaceUnavailable is what a call the workspace's service REFUSED as
// unavailable is answered with (R3, D2983): the workspace's service re-reads
// the workspace's roster as it re-reads its published node, and a roster it
// could not read is refused as the service being unable to read the state it
// must read — never as a verdict about this run. So the run waits for it
// exactly as it waits for a workspace service it cannot reach, and moves to no
// other credential meanwhile, which is the whole of R3's "it waits for a
// service it cannot reach instead of halting on a verdict nobody made".
//
// The service answered, so the sentence says what it could not do rather than
// that it could not be reached, and the halt the spent bound reaches names the
// roster rather than the connection.
func (s *Server) workspaceUnavailable(w http.ResponseWriter) {
	s.answerServiceWait(w, stateDetailWorkspaceRoster, serviceNameWorkspace, "cannot read the workspace's roster")
}

// answerServiceWait is the wait both of the callers above answer with: the
// answer that ends the stage in flight, and the halt of the run once the bound
// from the first such call is spent. cause is what went wrong, as the log line
// and the agent's own error sentence say it — the two callers differ in what
// the service did or failed to do, and in nothing else.
func (s *Server) answerServiceWait(w http.ResponseWriter, detail, service, cause string) {
	until := s.noteUnreachable()
	if !time.Now().Before(until) {
		s.haltService(w, detail)
		return
	}
	s.answer(ServiceAnswer{Unreachable: &Unreachable{RetryAt: until, Service: service}})
	s.log.Warn("the "+service+" "+cause+"; the run waits for it",
		"stage", s.stageName(), "service", service, "retry_at", until.UTC().Format(time.RFC3339))
	writeError(w, http.StatusServiceUnavailable, "api_error",
		"fugaro: the "+service+" "+cause+"; the run waits for it until "+until.UTC().Format(time.RFC3339))
}

// noteUnreachable records a call that could not reach the service and answers
// when this run stops waiting for it: the first such call, plus
// serviceUnreachableBound (D2983). The clock is the run's and not the call's,
// so a stage that meets the same outage again — and a second call in the stage
// that met it — waits for the outage to be over rather than for a fresh bound
// of its own, and every answer that arrives clears it (serviceAnswer): one
// answer means the service is back.
//
// The first call of an outage is the one that sets the bound, and the bound is
// this run's own: it is not persisted and not written to the record, because a
// run that is stopped and relanced is a run that starts its own.
func (s *Server) noteUnreachable() time.Time {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachableSince.IsZero() {
		s.unreachableSince = now
	}
	return s.unreachableSince.Add(serviceUnreachableBound)
}

// noteServiceReached clears the outage clock: an answer of any kind — a served
// call, a typed refusal, a model error the service relayed — is the service
// being reachable, and the next call that cannot reach it starts a bound of its
// own (D2983). It is called from serviceAnswer, the one place an answer of the
// service's own is read.
//
// It answers the clock it cleared, because one answer is not always the service
// being back: a workspace's service that refuses a call as unable to read its
// roster answered and is still not serving this run, and that refusal puts the
// clock back where it was (keepUnreachable, R3).
func (s *Server) noteServiceReached() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	since := s.unreachableSince
	s.unreachableSince = time.Time{}
	return since
}

// keepUnreachable puts an outage clock back where an answer found it: the
// workspace's service REFUSED a call as unable to read its roster rather than
// serving one, and R3's wait is measured from the first call of the outage, so
// a bound re-armed by every such refusal is a wait that never spends it and a
// halt (stateDetailWorkspaceRoster) that is never reached. A clock that was not
// running stays cleared: the outage of these refusals begins with the one that
// meets it.
func (s *Server) keepUnreachable(since time.Time) {
	if since.IsZero() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachableSince.IsZero() {
		s.unreachableSince = since
	}
}

// workspaceSlipHeld reports whether the calls this gateway makes to the
// workspace's service travel under the workspace's own slip: the roster R3's
// refusal is about is re-read under that slip and nowhere else (F13, R4), so a
// call holding none has no roster read to wait for.
func (s *Server) workspaceSlipHeld() bool {
	sv := s.o.Upstream.Service
	return sv != nil && sv.WorkspaceSlipID != ""
}

// serviceUnreachableBound is how long a run waits for a credentials service it
// cannot reach before it halts, naming the service (D2983). One constant covers
// both services — an outage is a fact about the service and not about the route
// that met it — and it is the bound gitforward.go's own retry is to reuse when
// WS18 lands, because the git paths have no stage to wait in and would otherwise
// spend a second one.
//
// What it is chosen for: it rides out what a service actually does (a revision
// rollout, a cold start, a scale-to-zero revision waking, IAM propagation on a
// grant that just landed), which is seconds to a couple of minutes, and it is
// short enough that a service that is really gone stops the run inside the job
// rather than at its deadline. It is a var, like endStageWait and
// upstreamRetryDelay, so the tests do not spend it.
var serviceUnreachableBound = 5 * time.Minute

// answer hands one service answer to the run's hook, if it has one.
func (s *Server) answer(a ServiceAnswer) {
	if s.o.OnServiceAnswer != nil {
		s.o.OnServiceAnswer(a)
	}
}

// answerChain hands one served call's chain answer to the step's record hook,
// if the run has one (chain_names.go): the source that served the call, the
// fallback flag and the model that served. Like answer it is called with no
// lock of this package's held, and a nil hook is not an error — a run whose
// repository has not migrated has no record member to fill.
func (s *Server) answerChain(a ChainAnswer) {
	if s.o.OnChainAnswer != nil {
		s.o.OnChainAnswer(a)
	}
}
