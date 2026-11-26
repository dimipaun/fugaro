package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The job environment variables that point the runner at the project's
// budget backend, set by fugaro update --firebase-project on every workflow job
// whenever the installation has a backend, whatever the job's mode (D11: the
// mode is policy now, and a run needs the URL and the key to read it — only
// the grace stays mode-gated, plan R10). Without RTDBURLEnv the runner is
// M9a's: a static cap in memory and no shared state.
const (
	RTDBURLEnv        = "FUGARO_RTDB_URL"
	FirebaseAPIKeyEnv = "FUGARO_FIREBASE_API_KEY"
	// BudgetGraceEnv shortens the D14 grace (a Go duration, at least 5 s and
	// at most the default 3 m): it exists for tests and can only tighten.
	BudgetGraceEnv = "FUGARO_BUDGET_GRACE"
	// BudgetTokenEnv is set in a job's environment by the launcher that minted
	// this run's budget token, and by nothing else: whether a token was minted
	// is a fact about one launch, so it travels as an execution override
	// (backend.LaunchSpec.Env) and never through fugaro update's deploy-time
	// environment. It closes a fail-open the security review of PR #379 found:
	// a run of the same repository could delete a queued run's token object
	// (before the seal moved it to tokens/, read-only for a job, D16), and a
	// token object a run cannot read is still possible (a job account without
	// its tokens/ grant reads it the way it reads a missing one: GCS answers
	// both with 403, since the grant carries no list permission), and a job whose own mode is off
	// read that as "this project has published no
	// policy" and ran with no session, no cap and no kill switch — while the
	// launcher had minted a token precisely because a policy exists. With the
	// marker set, a missing token is the refusal it is on a budget-on job,
	// whatever the job's mode; a job with no marker (every install that never
	// minted one, and every local run) keeps today's behaviour.
	//
	// Its *value* is the launch's seal key as one unpadded base64url string
	// (D15): the marker stopped being a one-bit marker when the launcher
	// started sealing, and became the carrier of a per-execution key. "1" is
	// still parsed — it is what a launcher from before the seal writes — and
	// the run then refuses at the take, naming the old launcher (D19's
	// mixed fleet), rather than reading the token that launcher left
	// unsealed. The parse is strict and never echoes the value: see
	// BackendFromEnv.
	//
	// The name itself is gitops.BudgetTokenEnv's, and this is an alias of it
	// rather than a second spelling: internal/gitops strips the variable from
	// every git call and cannot import this package (this package imports it),
	// so the one definition lives there and the two cannot drift.
	BudgetTokenEnv = gitops.BudgetTokenEnv
)

// SlipEnv is the variable a run reads its slip's id from: the launcher asks
// the credentials service for a slip and hands the job its id here (DS1.4,
// internal/cli's envSlip), and nothing else writes it. A job without it is
// the run of an installation whose service was not asked for one, and the run
// proceeds on the path it took before the slip existed.
//
// It is spelled here and aliased there (internal/cli's envSlip is
// runner.SlipEnv) rather than respelled, because the two halves are one name:
// the launcher writes it and this package reads it, so a second spelling is a
// launcher and a runner that disagree about it in silence.
const SlipEnv = "FUGARO_SLIP"

// slipHold is the slip this run took, and the beat that keeps the service
// that serves it warm while the run lives. id is the name the launcher was
// granted — public by design, it is in the job's environment and the agent can
// read it (F22, G3) — and secret is the claim's own credential, which lives
// here and in the service's memory and nowhere else: not in the environment,
// not in a log line, not in the record.
type slipHold struct {
	id     string
	secret string
	// service is the URL the slip was claimed at, kept with it so the beat
	// asks the same service the claim did without reading the environment
	// again: where the run claimed is a fact about the claim.
	service string
	// wire is the pair of header names this slip travels in: the project's own
	// for a slip this run claimed at its own service, the workspace's (C1's)
	// for the one the workspace granted it (WC8, R4). Kept with the hold
	// because it is a fact about the slip and not about the caller.
	wire slipWire
	// cancel ends the beat and done is closed when its goroutine has
	// returned, so a caller that waits on it knows nothing of the run is
	// still in flight. Both are written once, by startSlipBeat, and cleared
	// by stopSlipBeat; a hold that has not been stopped has both set.
	cancel context.CancelFunc
	done   chan struct{}
	// beatSaid is whether the run has written its one line about a beat that
	// did not land (beatSlip), so a service that stays unreachable is stated
	// once rather than every interval. Guarded by slipMu, which the beat's
	// goroutine and the run both hold.
	beatSaid bool
}

// slipAnswer is what one call to the claim route answered: the status, which
// is a decision whether or not the body was read, and the body itself.
type slipAnswer struct {
	status int
	body   []byte
}

// The retry of the run's own calls to the credentials service: the claim and,
// after it, every keep-warm beat. The spec's resilience rule is that a job
// retries its credential and proxy requests with backoff, and this is where
// the run's own are retried — the beat with them, since a beat is the same
// call. What the gateway makes of a model call is not retried here and never
// (internal/gateway's sendRetryingOnce says why: the service retries inside
// one answer, a repeated call is a second spend, and an unreachable service is
// D143's halt).
//
// The waits grow, and they are short on purpose: what they step over is a
// service that is cold-starting, restarting or briefly rate-limited, not an
// outage — an outage is the grace of the budget session and the run's own
// stage retry. They are vars so a test measures the backoff instead of
// spending it.
var (
	slipRetryDelays = []time.Duration{250 * time.Millisecond, 1 * time.Second, 4 * time.Second}
	slipRetryWait   = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
)

// The run's own calls have bounds of their own: a service that hangs must not
// hold the claim past the run's start, and one attempt of a beat must not hold
// the beat's goroutine. The ticker drops the ticks a slow beat did not take,
// so a service that answers slowly is beaten at less than one request per
// interval rather than at a queue of them, and the run's end cancels whatever
// attempt is in flight instead of waiting it out.
const (
	slipCallTimeout = 30 * time.Second
	// slipClaimBudget bounds the whole claim, the retries and the waits
	// between them together. The retry's own waits are short, so it is a
	// service that accepts the connection and never answers that would
	// otherwise hold the run's start for one client timeout per attempt; the
	// run's start is what this bound is for, and what it costs a service that
	// is merely slow is the run as it was before the slip existed.
	slipClaimBudget = 90 * time.Second
	// maxSlipAnswerBytes bounds what the claim route may answer, as the
	// launcher's grant call bounds its own answer (internal/cli's
	// maxSlipAnswerBytes): the service is another writer, and an unbounded
	// answer is one this run renders into memory.
	maxSlipAnswerBytes = 16 << 10
	// maxSlipExecutionBytes is internal/credsvc's own bound on the execution
	// name a claim may report (slip.go's maxSlipExecutionBytes, unexported
	// there): the service refuses the whole call over a longer value, so this
	// run reports none rather than a name the service will not read.
	maxSlipExecutionBytes = 128
	// maxSlipSecretBytes is the bound the service reads a presented claim
	// secret by (internal/credsvc's maxHeaderTokenBytes, unexported there):
	// the same bound as the token this run presents beside it.
	maxSlipSecretBytes = 512
	// maxIdentityTicketBytes bounds the identity ticket this run will carry
	// into a claim (identitywire.TicketEnv): the service draws it from
	// crypto/rand, exactly as it draws a slip's claim secret, so this is that
	// secret's own bound. A longer value is damage to the job's environment,
	// refused where it is parsed rather than put on the wire.
	maxIdentityTicketBytes = maxSlipSecretBytes
)

// slipBeatEvery is how often the beat asks the service to read the slip back
// (D1934): the budget session's own cadence, read off the session rather than
// respelled beside it, because one request per interval is the cadence Check
// 511 measured the service answering at, with no cold start, and the registry's
// beat is that same interval. A hold exists only under a session -- the claim
// is bound to the identity the session is -- so the default is the answer for
// a run that somehow has none.
func (r *run) slipBeatEvery() time.Duration {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return budget.DefaultHeartbeatEvery
	}
	return sess.HeartbeatEvery()
}

// slipClient is the client the run's own calls to the credentials service are
// made with: it never follows a redirect, because the call carries the job's
// own Google ID token and a followed 307 or 308 would carry it to whatever
// host the Location named (internal/cli's slipClient and internal/gateway's
// upstreamClient refuse redirects for the same reason), it keeps no cookie
// jar, and it does not wait on an answer forever.
var slipClient = &http.Client{
	Timeout:       slipCallTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Backend is how the run reaches the project's budget database. The zero
// value is no backend.
type Backend struct {
	RTDBURL string
	APIKey  string
	// Grace is the D14 window; zero is budget.DefaultGrace.
	Grace time.Duration
	// TokenMarked is true when the job's environment carries BudgetTokenEnv:
	// the launcher that started this execution minted it a budget token, so
	// the token's absence from the runs bucket is a refusal and never "this
	// project has published no policy" (see BudgetTokenEnv).
	TokenMarked bool
	// TokenKey is the launch's seal key, when the marker's value carried one
	// (BackendFromEnv): the token object is sealed under it and nothing else
	// is read. It is nil for a marker of "1", a launcher from before the
	// seal, and the run then refuses at the take (D19). The key is
	// a secret: it is registered with the run's redactor as soon as the run
	// starts (runner.go's addSealKey), and nothing that formats the marker's
	// value or this field may print it.
	TokenKey []byte
	// Ticket is the identity ticket the launcher that started this execution
	// was issued for it, read out of the job's environment
	// (identitywire.TicketEnv, LM6): the run claims its identity from the
	// installation's credentials service with it, instead of opening the
	// sealed object a signing launcher left (LM7, R7). It is a secret of the
	// service's, handed to one execution and dying into a token at the claim:
	// it is registered with the run's redactor before its first use, and
	// nothing that formats this field may print it.
	//
	// Only one of the two is ever set by a launch -- the flag and the ticket
	// are the two mechanisms of one bridge -- and the ticket wins where both
	// are, because it is what the launcher of this release asked the issuer
	// for.
	Ticket string
	// MalformedEnv is set, beside BackendErr, when the job's environment
	// carries the backend URL or the launcher's marker and the value could not
	// be parsed: the fields above are then only as far as the parse got, and
	// the run must not read the zero value as "this job has no backend" (L-b
	// of the security review of PR #379). Losing the marker that way is what
	// would let a job skip the read of the project's policy without a word,
	// which is the one place the marker did not fail closed. A malformed value
	// that describes no backend at all (a stray grace, an API key with no URL)
	// leaves it false: such a value is damage to the ceiling's environment
	// like any other, and from AB1a on (D3009, R3) it stops the run too, but it
	// is not a state in which the run lost something it was told it had.
	MalformedEnv bool
	// Tune, for tests only, adjusts the session's configuration (the fakes'
	// URLs, short intervals).
	Tune func(*budget.Config)
	// HTTP, for tests only, serves the backend's calls.
	HTTP *http.Client
}

// On reports whether a backend is configured.
func (b Backend) On() bool { return b.RTDBURL != "" }

// Issued reports whether the launcher that started this execution provisioned
// this run's identity for it: the old marker, whose value is the seal key of
// the object it left in the runs bucket, or the ticket it asked the issuer for
// (LM6, LM7). Either one says the same thing about the job's environment --
// this run has an identity it must be accounted for with, so a job that carries
// neither a database nor a database key to read is a job that cannot use what
// it was given, and the run refuses rather than going on outside the shared
// caps (M2). One predicate for the two mechanisms, because the two answer every
// question the run asks of them the same way (R7: both live here for one
// release).
func (b Backend) Issued() bool { return b.TokenMarked || b.Ticket != "" }

// BackendFromEnv reads the backend from the job's environment. loopbackOK
// admits a database on a loopback address (a local run against a fake or an
// emulator). A malformed value is an error, which the runner reports as an
// infra_error at bootstrap: never a silent "no backend". The Backend it returns
// beside an error carries MalformedEnv, which says whether the environment
// named a backend it could not parse — the two variables that matter are the
// URL and the launcher's token marker, so the variables are read first and
// validated after, which is the only order that can report it.
func BackendFromEnv(lookup func(string) (string, bool), loopbackOK bool) (Backend, error) {
	var b Backend
	url, hasURL := lookup(RTDBURLEnv)
	key, hasKey := lookup(FirebaseAPIKeyEnv)
	marker, hasMarker := lookup(BudgetTokenEnv)
	ticket, hasTicket := lookup(identitywire.TicketEnv)
	grace, hasGrace := lookup(BudgetGraceEnv)
	// Everything but the ones below describes no backend on its own: a stray
	// grace or an API key with no URL is damage to the ceiling's environment
	// rather than a backend this job carries. The three that do are the URL and
	// the two things a launcher writes to say it provisioned this run's
	// identity (the seal-key marker and the ticket): each is something only a
	// launcher that saw a budget writes, so each must not be lost to a parse
	// that failed (L-b).
	b.MalformedEnv = hasURL || hasMarker || hasTicket
	if hasURL {
		if err := rtdb.ValidateURL(url, loopbackOK); err != nil {
			return b, fmt.Errorf("%s: %v", RTDBURLEnv, err)
		}
		b.RTDBURL = url
	}
	if hasKey {
		if strings.TrimSpace(key) != key || key == "" || strings.ContainsAny(key, " \t\r\n&?#/") {
			return b, fmt.Errorf("%s: it must be a Firebase web API key", FirebaseAPIKeyEnv)
		}
		b.APIKey = key
	}
	if hasMarker {
		// Only the launcher writes this: the launch's seal key as one
		// unpadded base64url string, or "1" from a launcher that predates the
		// seal (refused later, at the take, with that said).
		// Anything else is damage to the job's environment, which the runner
		// reports as an infra_error rather than reading as "no marker": the
		// marker is what makes a missing token fail closed, so a value nobody
		// can interpret must not be the quiet one.
		//
		// The value is a secret once the launcher seals, and this parse runs
		// before any redactor exists: internal/cli's runExec calls this at
		// bootstrap, and the failure reaches the record only later, as the
		// run's infra_error, when there is no redactor to have registered the
		// key with. So neither error below carries the value — they name the
		// variable and the shape it wants instead. The redactor is the second,
		// weaker half and belongs to the runner (addSealKey).
		switch {
		case marker == "1":
			b.TokenMarked = true
		default:
			key, err := token.ParseKey(marker)
			if err != nil {
				return b, fmt.Errorf("%s: it must be 1, or the launch's seal key as one unpadded base64url string decoding to %d bytes, and only a launcher that minted this run's budget token sets it", BudgetTokenEnv, token.KeySize)
			}
			b.TokenMarked, b.TokenKey = true, key
		}
	}
	if hasTicket {
		// Only a launcher that was issued a ticket for this run writes it, and
		// it is a value this run presents to the issuer in a claim (LM6, LM7).
		// Anything this run cannot carry there is damage to the job's
		// environment, reported as an infra_error rather than read as "no
		// ticket": a run that lost the ticket would look for the sealed object
		// of a launch that minted none, and stop over the wrong cause.
		//
		// The value is a secret and this parse runs before any redactor
		// exists, so the refusal names the variable and the shape it wants and
		// never the value (the marker's own rule, above). The floor is the one
		// every other registered value holds (minSecretLen): a ticket shorter
		// than that cannot be redacted without redacting text that is not a
		// secret, and no service draws one that short.
		if len(ticket) < minSecretLen || len(ticket) > maxIdentityTicketBytes || !printableToken(ticket) {
			return b, fmt.Errorf("%s: it must be the identity ticket a launcher was issued for this run, one printable value of at least %d and at most %d bytes, and only such a launcher sets it", identitywire.TicketEnv, minSecretLen, maxIdentityTicketBytes)
		}
		b.Ticket = ticket
	}
	if hasGrace {
		d, err := time.ParseDuration(grace)
		if err != nil || d < budget.MinGrace || d > budget.MaxGrace {
			return b, fmt.Errorf("%s %q: it must be a duration between %s and %s", BudgetGraceEnv, grace, budget.MinGrace, budget.MaxGrace)
		}
		b.Grace = d
	}
	return b, nil
}

// backendOn reports whether this run reads the budget backend: the job's
// environment carries the database. The mode is not asked — from AB1a on every
// run is accounted for (D3009, R3), so no mode decides whether the backend is
// used.
func (r *run) backendOn() bool { return r.d.Backend.On() }

// budgetHalt records a halt the session found and stops the run: the
// running stage is cancelled and the gateway refuses every call and cuts the
// calls in flight. It is ignored once the agent loop is over (a kill during
// finalize or writeback changes nothing), and loses to an earlier cancel or
// halt.
func (r *run) onBudgetHalt(h budget.Halt) {
	rh := runstore.Halt{Reason: runstore.HaltReason(h.Reason), Scope: string(h.Scope), At: r.d.Now().UTC(), Detail: h.Detail}
	if !r.haltExternal(rh) {
		r.d.Log.Info("budget: a halt arrived too late or after another cause; ignoring it", "reason", string(h.Reason))
		return
	}
	got := r.haltValue()
	r.mu.Lock()
	gw := r.gw
	r.mu.Unlock()
	if gw != nil {
		gw.HaltExternal(gateway.Halt{Reason: string(got.Reason), Scope: got.Scope, Detail: got.Detail, At: got.At})
	}
	r.cancelHaltedStage(*got)
	r.d.Log.Warn("budget: the run is halted", "reason", string(got.Reason), "scope", got.Scope)
}

// haltExternal is haltNow for a cause outside the stage, ignored once the
// agent loop is over.
func (r *run) haltExternal(h runstore.Halt) bool {
	h.Detail = r.redact(h.Detail)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return false
	}
	return r.recordHaltLocked(h)
}

// refuseStrayToken ends a budget-on run that has no backend in its job
// environment and no marker, but whose token object the job can read: a
// launcher minted for it and the job cannot use the token, so the run would
// otherwise go on outside the shared caps without a word. One bucket read,
// only for budget-on runs without a backend (startBudgetBackend has already
// refused a marked one, before any read).
//
// The read's answers, and what each means here:
//
//   - the object (200): a stray token, refused;
//   - 404: no object, the run goes on;
//   - 403 (blobx.ErrForbidden): the run goes on. The job account's grant on
//     tokens/ is a conditioned objectViewer with no storage.objects.list, so
//     GCS answers its read of a missing object with 403
//     (docs/bucket-iam/spec.md §2.3, live check 7): that is what every run
//     of an installation without Firebase meets, since no launcher minted
//     for it. A job with no tokens/ grant gets the same 403 for a present
//     object, and no answer of the bucket tells the two apart, so the
//     bucket is not the evidence of a mint here: the marker is
//     (budget-token-channel.md §4 principle 3). Every launcher that writes
//     under tokens/ seals the object and hands the key to the execution as
//     the marker, so a minted run reaches this function only with no
//     object to find;
//   - anything else (a 401, a 400, a 5xx after the client's retries, a
//     driver's fs.ErrPermission, an oversized object): a check that cannot
//     be made, refused.
func (r *run) refuseStrayToken(ctx context.Context) error {
	slug, runID := r.d.Store.Slug(), r.d.Store.RunID()
	if r.d.Bucket == nil || budget.Key(slug) != slug || budget.Key(runID) != runID || slug == "" || runID == "" {
		return nil
	}
	// Presence, not a take: the object is the launcher's evidence that it
	// minted, and this run has no backend to use it with. The run's grant on
	// tokens/ is read-only, so the object stays for the lifecycle rule.
	present, err := token.ObjectPresent(ctx, r.d.Bucket, slug, runID)
	if errors.Is(err, blobx.ErrForbidden) {
		// A missing object's answer to a caller without list permission:
		// see the doc comment. Logged, so an operator can see the check met
		// it; no object name and nothing of the error's text.
		r.d.Log.Info("budget: the token check was answered 403, which is how GCS answers this job for a missing object; no marker, so no token was minted for this run")
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for a budget token in the runs bucket: %s", r.redact(err.Error()))
	}
	if !present {
		return nil
	}
	return fmt.Errorf("the run was launched with a budget token but this job has no budget backend (%s is not set in its environment): run fugaro update for this repository, then launch the run again", RTDBURLEnv)
}

// freezeHalts is called when the agent loop is over: nothing the backend says
// can stop a stage any more.
func (r *run) freezeHalts() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

// bootstrapHalt records a policy halt before anything was locked and returns
// the error that ends bootstrap (outcome none, exit 0).
func (r *run) bootstrapHalt(reason runstore.HaltReason, scope, detail string) error {
	r.haltNow(runstore.Halt{Reason: reason, Scope: scope, At: r.d.Now().UTC(), Detail: detail})
	return &HaltError{*r.haltValue()}
}

// strayTokenNoBackend is M2's refusal for a job whose launcher minted a budget
// token and whose environment carries no budget backend: the marker is written
// only by a launcher that saw a shared budget or a published policy, so this
// run has a ceiling it cannot read. It is worded once because it has two
// readers and they must agree: budgetClient, which meets the state at the
// policy read — before admissionCheck, before any git credential, before the
// clone — and startBudgetBackend, which met it first and is where a run that
// answered "no policy" earlier would otherwise stop.
func strayTokenNoBackend() error {
	return fmt.Errorf("this run was launched with a budget token, so this repository has a shared budget or a published policy, but its job carries no budget backend (%s is not set in the job's environment): run fugaro update for this repository, then launch the run again", RTDBURLEnv)
}

// startBudgetBackend takes the run's Firebase identity and joins the shared
// budget (plan T6): it reads and unseals the token object (never deleting it), exchanges it,
// reads the caps and the kill switches, halts the run if it may not begin,
// creates the registry entry and starts the heartbeat and the kill watch.
// A halt here has outcome none and exit 0: nothing was locked or pushed.
// startBudgetBackend opens the run's budget session, and from AB1a on it opens
// it for every run: every mode is accounted for — a mode left unset is observe
// (C1, D3009, R3) — so nothing here asks the mode before it takes the run's
// identity. What a run can still meet is a job it has nothing to open a session
// against: a marked one with no database, which is the refusal below naming its
// fix, and a markless one whose token object is not there and whose own
// environment names no live mode — a launch the launcher minted nothing for,
// which opens no session and runs on the environment ceiling exactly as it did
// before AB1a (budgetClient's markless arm; AB4 is what makes every launch
// mint). That arm is a Cloud Run launch's alone: a Batch task that reached here
// with nothing a launcher left would already have been refused at the policy
// read, because on Batch a launcher that minted nothing launched nothing
// (budgetClient, BR1, R18). Everything else it can meet is a refusal.
// It is reached only after admissionCheck has admitted this run — bootstrap
// calls the check before the lock and before this, and nothing else calls
// either — so the slug the session is keyed by (r.d.Store.Slug, through the
// identity the policy read built) is the one task.Slug of the job's provider
// and the task's repository computes, for the profile the job's own name pins
// (plan task 3.3: the ordering is the security property, and this is the lease
// it protects).
func (r *run) startBudgetBackend(ctx context.Context) error {
	// The launcher's marker says it minted this run a budget token, and the
	// ticket says it asked an issuer for this run's identity: either way it did
	// so only because the project has a shared budget or a published policy.
	// A job with no backend in its environment then cannot read that policy,
	// and the run would go on with the environment ceiling, no session and no
	// kill switch (M2; refuseStrayToken below is the same refusal for a job
	// that can read the bucket, found there). What the launcher left is proof
	// enough: nothing else writes either value.
	if r.d.Backend.Issued() && !r.d.Backend.On() {
		return noBackendForWhatTheLauncherLeft(r.d.Backend)
	}
	// The run's identity, taken out of the runs bucket once and shared with
	// the project policy's read (projectpolicy.go): one take, so the policy
	// read and the session are one identity. The take removes nothing (D16:
	// the job's grant on tokens/ is read-only, and the object stays until the
	// lifecycle rule or the launcher removes it), so what makes it
	// consume-once is the seal — the launcher delivers the key to the one
	// execution it starts — and never a missing object. Its refusals are this
	// function's own, shipped ones: a job whose backend is damaged, or whose
	// budget is on with no token in the bucket, stops here (B1).
	if _, err := r.budgetClient(ctx); err != nil {
		return err
	}
	ident := &r.ident
	if ident.db == nil {
		return r.refuseStrayToken(ctx)
	}
	// The generation of the database's rules this installation has deployed,
	// which the session needs before it may write the day counter's
	// spend-accuracy leaves: Realtime Database applies a stage report's
	// multi-path update atomically, so one leaf the deployed document does not
	// grant refuses the whole report and the stage fails (spend-accuracy F10,
	// SA5.1). The marker is the same node the policy read above gates on
	// (projectpolicy.PathRulesMarker), and an absent one is generation 0: the
	// pre-policy rules, under which the session writes the leaves it always
	// wrote.
	//
	// A marker this run cannot read is one warning and generation 0, not a
	// halt, and the direction is the reason: the four leaves are figures for
	// the report's drift alarm and nothing decides on them, while the safe
	// side of the gate is the one that writes fewer leaves -- the ones every
	// deployed document grants. A cloud run meets the marker in this state
	// only if admissionCheck's own read of it succeeded a moment earlier
	// (projectpolicy.Read refuses a hand-edited one there, and an outage is
	// left to the session's own grace), and a local run against a backend,
	// which reads no policy at all, then writes today's leaves rather than
	// failing.
	deployed, rerr := projectpolicy.RulesGeneration(ctx, ident.db)
	if rerr != nil {
		r.d.Log.Warn("budget: the deployed rules generation could not be read, so this run writes only the day-counter leaves every document carries (run 'fugaro update' to deploy the rules that carry the spend-accuracy ones)",
			"node", projectpolicy.PathRulesMarker(), "err", r.redact(rerr.Error()))
	}

	// The mode the run is accounted for, read through C1: a mode no layer set —
	// and the word that was a mode — is observe, which is what result.json
	// records and what the session enforces at (D3009, R3; the record's enum
	// holds the two live modes and nothing else).
	mode := policy.Mode(r.spend.Mode).Effective()
	// The configuration the policy read already built and Tuned; only the
	// fields that depend on the merged policy are set here.
	cfg := ident.cfg
	cfg.PolicyCap, cfg.CommittedDaily, cfg.LocalEnforce = r.spend.Cap, r.committedDaily(), mode == policy.ModeEnforce
	cfg.RulesGeneration = deployed
	if r.rec.Deadline != nil {
		cfg.Deadline = *r.rec.Deadline
	}
	sess, err := budget.Open(ctx, cfg, ident.custom)
	if err != nil {
		// The policy read classified the same failures; this is the one place
		// the two classifications are written, so they cannot drift (R2-3).
		return r.budgetIdentityError(err)
	}
	r.mu.Lock()
	r.sess = sess
	r.mu.Unlock()

	// Admit is told whether this run's model calls are leased, and it asks the
	// run's own predicate rather than spelling the auths again: a cascade run
	// has a gateway and a lease exactly like an api-key one (C1.19), so a
	// second spelling here read it as an unleased run and admitted it into an
	// enforcing installation with no caps — to fail call by call instead of
	// halting no_cap as every other gatewayed run does.
	h, err := sess.Admit(ctx, budget.AdmitOptions{Gateway: r.gatewayOn(), LocalEnforce: cfg.LocalEnforce, PolicyCap: r.spend.Cap})
	switch {
	case errors.Is(err, budget.ErrGraceExpired):
		return r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run", "the budget backend could not be reached before the run began: "+r.redact(err.Error()))
	case err != nil:
		return fmt.Errorf("reading the project's budget: %s", r.redact(err.Error()))
	case h != nil:
		return r.bootstrapHalt(runstore.HaltReason(h.Reason), string(h.Scope), h.Detail)
	}
	if err := sess.Start(ctx, r.registryEntry()); err != nil {
		if errors.Is(err, budget.ErrGraceExpired) {
			return r.bootstrapHalt(runstore.HaltBudgetUnavailable, "run", "the budget backend could not be reached before the run began: "+r.redact(err.Error()))
		}
		return fmt.Errorf("registering the run: %s", r.redact(err.Error()))
	}
	r.rec.Budget = &runstore.BudgetRecord{Day: budget.Day(sess.Now()), Mode: mode, Backend: "rtdb"}
	return nil
}

const policyEnforce = "enforce"

// committedDaily is the repository's committed per_day_usd in micro-dollars.
func (r *run) committedDaily() budget.Micros {
	if r.policy.PerDayUSD <= 0 {
		return 0
	}
	m, err := pricing.FromUSD(r.policy.PerDayUSD)
	if err != nil || m < 1 {
		return 0
	}
	return m
}

// registryEntry is what the run says about itself at bootstrap. The title
// (task.Spec.TitleLine: the launcher's own --title, else the task's first
// line) is untrusted text: redacted and clipped by the session.
func (r *run) registryEntry() budget.AgentEntry {
	title := r.redact(r.spec.TitleLine())
	if runes := []rune(title); len(runes) > 80 {
		title = string(runes[:80])
	}
	// Auth is the credential this run's model calls go out on, and a run the
	// routing table serves has none to write: what such a run goes out on is
	// the table's own answer and arrives per stage as `source` and `fallback`
	// (chainAnswered), so the runner chooses no whole-run upstream for it
	// (model-credentials-routing CS6, D3070 — every view prints the dash for an
	// absent member, CS9). An unmigrated repository keeps its file's auth:
	// nothing of the table reaches it and its calls are its own, as its file
	// says (R3).
	auth := r.cfg.Agent.Auth
	if r.serviceModelPath() {
		auth = ""
	}
	e := budget.AgentEntry{
		Repo: r.spec.Repo, Workflow: r.rec.Workflow, Title: title, Stage: "bootstrap", Auth: auth,
		Coder: r.cfg.Agent.ModelFor(config.RoleCoder), Reviewer: r.cfg.Agent.ModelFor(config.RoleReviewer),
	}
	if r.rec.Deadline != nil {
		e.StageDeadline = r.rec.Deadline.UnixMilli()
	}
	if rr := r.rec.Recipe; rr != nil && rr.Name != recipe.DefaultName {
		e.Recipe = rr.Name
	}
	// The rigor mode the run resolved at bootstrap, which is the moment this
	// entry is built at (runner.go: bootstrapRigor precedes
	// startBudgetBackend): the mode a live run shows in `fugaro watch` is the
	// record's own, and recordRigor keeps this pair in step with a later
	// moment that raises it.
	if rr := r.rec.Rigor; rr != nil {
		e.Mode, e.From = string(rr.LevelOf()), string(rr.From)
	}
	return e
}

// beginBudgetStage shows the stage in the registry.
func (r *run) beginBudgetStage(name string, n int, deadline time.Time) {
	now := r.d.Now().UnixMilli()
	r.noteRegistry(func(e *budget.AgentEntry) {
		e.Stage, e.StageStartedAt, e.StageDeadline, e.Action = name, now, deadline.UnixMilli(), ""
		if name == "review" || name == "review_first" {
			e.Round = n
		}
	})
}

// noteRegistry changes the run's registry entry when a session exists; the
// next heartbeat sends it.
func (r *run) noteRegistry(fn func(*budget.AgentEntry)) {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess != nil {
		sess.Update(fn)
	}
}

// noteRegistryPRURL shows the pull request's URL in the registry: for a
// first run, once notePR records its number; for a follow-up, once
// checkPullRequest confirms it (a follow-up's PR is known from bootstrap on,
// not just from a later stage boundary).
func (r *run) noteRegistryPRURL(url string) {
	r.noteRegistry(func(e *budget.AgentEntry) { e.PRURL = url })
}

// verifySummaryRunes bounds the registry's verify summary before it becomes
// AgentEntry.Verify. It does not guarantee staying under the 200-byte cap
// clipEntry enforces on every registry string: 120 runes of multi-byte text
// (an escaped \uXXXX from Printable, say) can well exceed 200 bytes, in
// which case clipEntry's clipString clips it again, byte-wise. That second
// clip is harmless (it drops any partial trailing rune the byte cut leaves),
// just possibly shorter than 120 runes.
const verifySummaryRunes = 120

// verifySummaryLine is rec's one-line summary as it goes into the registry:
// redacted, sanitized through pluginwire.Printable (the text can hold an
// agent-chosen test name, and clipEntry's own clipString strips control
// characters but not bidi overrides and other Cf formatting code points),
// and clipped to verifySummaryRunes.
func (r *run) verifySummaryLine(rec verify.Record) string {
	summary := pluginwire.Printable(r.redact(rec.Summary()))
	if runes := []rune(summary); len(runes) > verifySummaryRunes {
		summary = string(runes[:verifySummaryRunes])
	}
	return summary
}

// noteVerify reads the latest verify record and shows its one-line summary
// in the registry. A failed read only warns: the registry simply keeps its
// last known verify line.
func (r *run) noteVerify() {
	records, err := verify.Records(r.d.StateDir)
	if err != nil {
		r.d.Log.Warn("reading verify records for the registry failed", "err", r.redact(err.Error()))
		return
	}
	if len(records) == 0 {
		return
	}
	summary := r.verifySummaryLine(records[len(records)-1])
	r.noteRegistry(func(e *budget.AgentEntry) { e.Verify = summary })
}

// reportNotional hands a stage's own cost figure to the backend: notional
// dollars, advisory only, by model (plan R7).
//
// The stage is the judge of whether there is anything to report, and it is
// asked per stage rather than per run (P13): a gateway stage's dollars are the
// ledger's, and the session reports the gateway's settled usage itself, while a
// stage the run's stage-scoped Claude OAuth account serves is outside the
// gateway by construction — nobody else can report it, and its figure reaches
// the repository's and the project's notional counters here alone
// (docs/mixed-credentials/spec.md §5, §11). Asked about the run instead
// (r.gatewayOn(), true for every mixed run) the account stage's dollars are
// dropped in silence.
//
// Silence is the gateway's, and only for the stages it serves — which is why
// the gate is the two conditions and not either alone. Under a run with no
// gateway at all (an oauth run: GatewayOn never sends an oauth token through
// one) every stage is Claude Code's own and every figure is notional, exactly
// as before a mixed run existed; a gate that admitted only the account stage
// would leave an oauth installation's notional counters at zero, in silence.
//
// A write that fails is kept and retried by the heartbeat; the grace decides
// about an outage.
func (r *run) reportNotional(ctx context.Context, name string, res agent.Result) {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil || (r.gatewayOn() && !r.stageDirect(name)) {
		return
	}
	notional := r.usdMicros(res.CostUSD)
	by := map[string]budget.ModelUse{}
	for model, u := range res.ModelUsage {
		use := budget.ModelUse{In: u.Input, Out: u.Output, CR: u.CacheRead, CW: u.CacheCreation}
		if m, ok := r.spend.Prices.Lookup(model); ok {
			use.Micros = m.Rates.Cost(pricing.Usage{Input: u.Input, CacheWrite5m: u.CacheCreation, CacheRead: u.CacheRead, Output: u.Output})
		}
		by[model] = use
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := sess.AddNotional(wctx, notional, by); err != nil {
		r.d.Log.Warn("reporting a stage's notional spend failed; it will be retried", "err", r.redact(err.Error()))
	}
}

// finishBudget ends the session: the last report, the outcome, the registry
// entry gone. It runs on a context of its own, so a cancelled run still
// tells the backend how it ended; every step is bounded.
func (r *run) finishBudget(ctx context.Context) {
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budgetFinishTimeout)
	defer cancel()
	sess.Finish(fctx, string(r.rec.Status))
	if b := r.rec.Budget; b != nil {
		b.GrantedMicros, b.ReleasedMicros = int64(sess.GrantedMicros()), int64(sess.ReleasedMicros())
	}
}

// budgetFinishTimeout bounds everything the session does at the end, inside
// Cloud Run's ~10 s SIGTERM window. Finish orders its writes by importance:
// the release first, then the last usage, the outcome, the entry; what a
// kill cuts short stays for the sweeper.
const budgetFinishTimeout = 8 * time.Second

// redactHandler redacts every string the session logs, so a credential can
// never reach the job's log through an error text.
type redactHandler struct {
	h      slog.Handler
	redact func(string) string
}

func (h redactHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.h.Enabled(ctx, l) }

func (h redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool { out.AddAttrs(h.attr(a)); return true })
	return h.h.Handle(ctx, out)
}

func (h redactHandler) attr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(h.redact(a.Value.String()))
	case slog.KindGroup:
		g := a.Value.Group()
		out := make([]slog.Attr, len(g))
		for i, c := range g {
			out[i] = h.attr(c)
		}
		a.Value = slog.GroupValue(out...)
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(h.redact(err.Error()))
		} else {
			a.Value = slog.StringValue(h.redact(fmt.Sprint(a.Value.Any())))
		}
	}
	return a
}

func (h redactHandler) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = h.attr(a)
	}
	return redactHandler{h: h.h.WithAttrs(out), redact: h.redact}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{h: h.h.WithGroup(name), redact: h.redact}
}

// claimSlip takes this run's slip, once, at its own start, and starts the beat
// that keeps the instance serving it warm (D1933, D1934). Everything the run
// does at the service afterwards — every model call the gateway proxies, every
// beat — presents the claim's secret beside the slip's id, and the service
// keeps only the hash of it, so the secret lives in this process and nowhere
// else: not in the job's environment, not in a record, not in a line.
//
// The two answers of that route are what a run reads apart: the service
// answers the call that CLAIMS the slip with the secret, and a later call that
// presents it with the claim's own reading (D1941). That is why a run claims
// once and a second claim is not made at all — the service refuses the replay,
// and a refusal here is a run that holds a slip and cannot use it.
//
// Nothing here fails the run. A run whose launcher was granted no slip (no
// SlipEnv), whose installation has no service to claim at, or whose service
// refused the call, runs the path it ran before the slip existed, and no more
// than that: R9's fence is the service's own, which serves a slip-bound
// credential only under a live claim and fails closed without one, so failing
// the run here would break runs that work today over a credential they may
// never ask for (the launcher's own reading of the same state,
// internal/cli's runSlipEnv).
func (r *run) claimSlip(ctx context.Context) {
	if r.slipHold() != nil {
		return
	}
	slipID := envLookup(r.d.Env, SlipEnv)
	if slipID == "" {
		// No slip was granted to this launch: an installation whose service
		// has no slip routes, a launcher that could not reach it, a job that
		// was started before this release. All of them are the run as it was.
		return
	}
	serviceURL := envLookup(r.d.Env, credentialsServiceURLEnv)
	if serviceURL == "" {
		r.d.Log.Warn("this run holds no slip: its job carries " + SlipEnv + " and no " + credentialsServiceURLEnv + ", so the slip cannot be claimed and a credential that only a slip serves is refused at the call (D1933)")
		return
	}
	if err := checkSlipServiceURL(serviceURL); err != nil {
		// The rule the run's other service calls hold their URLs to, and the
		// launcher's own grant call before this one: the claim carries the
		// job's own Google ID token, and over plain http to a host that is
		// not this machine it would travel in cleartext. The run proceeds
		// without a slip, and the gateway refuses the same URL with its own
		// refusal before any model call is made.
		r.d.Log.Warn("this run holds no slip: " + err.Error())
		return
	}
	body, err := json.Marshal(credsvc.SlipClaimRequest{Execution: slipExecution(r.d.Execution)})
	if err != nil {
		r.d.Log.Warn("this run holds no slip: the claim call could not be built, so a credential that only a slip serves is refused at the call", "err", r.redact(err.Error()))
		return
	}
	cctx, cancel := context.WithTimeout(ctx, slipClaimBudget)
	defer cancel()
	ans, err := r.slipRetryCall(cctx, projectSlipWire, serviceURL, slipID, "", body)
	if slipRouteAbsent(ans.status) {
		// The service does not serve the claim route at all — one release
		// behind this runner — which is not a refusal of this run: it is the
		// version skew DS1.4 keeps, and it is quiet because the run proceeds
		// on the path it took before the slip existed.
		return
	}
	if err != nil {
		r.d.Log.Warn("this run holds no slip: the installation's credentials service could not be asked for the claim, so a credential that only a slip serves is refused at the call", "err", r.redact(err.Error()))
		return
	}
	if ans.status != http.StatusOK {
		// A refusal: the service decided, and asking it again answers the
		// same thing.
		r.d.Log.Warn("this run holds no slip: the installation's credentials service refused the claim, so a credential that only a slip serves is refused at the call (R9: a slip-bound credential is served under a live claim and never without one)", "status", ans.status)
		return
	}
	var claim credsvc.SlipClaimResponse
	if err := json.Unmarshal(ans.body, &claim); err != nil {
		r.d.Log.Warn("this run holds no slip: the credentials service's answer to the claim is not a claim's")
		return
	}
	if !slipSecretUsable(claim.ClaimSecret) {
		r.d.Log.Warn("this run holds no slip: the credentials service answered the claim with no usable claim secret")
		return
	}
	r.setSlipHold(slipID, claim.ClaimSecret, serviceURL)
	// Registered before anything can print it: the answer that carried it is
	// already out of this function, and the beat, the gateway and any failure
	// after it are redacted of it from here on.
	r.addSecret(claim.ClaimSecret)
	r.startSlipBeat(ctx)
	r.d.Log.Info("this run claimed its slip: the credentials service serves it the credentials the slip names, and this run keeps the service warm for as long as it lives by asking it to read the slip back every interval (D1933, D1934)")
}

// claimWorkspaceSlip is the run's second claim (WC8, R3, R4): the workspace's
// slip, claimed at the workspace's own credentials service at bootstrap, whose
// secret is then presented to that service and to no other.
//
// It is claimSlip one service over, and it differs from it in three things and
// no more: the id is read from C1's own environment entry for it
// (creds.PersonSlipEnv, which a launcher writes on a workspace route whose
// switch is on), the address is the one the run's own route record carries and
// never one from the job's environment (R3, F32), and the id and the secret
// travel in C1's two headers. There is no keep-warm beat of its own: the
// workspace's service is already beaten by WS13's own, for as long as a run
// routed a kind there lives.
//
// Nothing here fails the run, exactly as nothing in the first claim does. A run
// the workspace granted no slip — a member whose launcher holds no personal
// token, an installation with no workspace service, a grant that failed — is
// the run of an installation with no personal half at all, and R3's fence is
// the workspace's service's own: it serves a slip-bound credential under a live
// claim and fails closed without one. A slip the run took and cannot use is the
// service's answer at the call, and never this function's.
//
// The claim route is the same path the project's service serves (credsvc's
// SlipClaimPath) and its answer is the same shape: one route of workspace mode
// is the claim, and the two services answer one contract about it (R3, C1).
func (r *run) claimWorkspaceSlip(ctx context.Context, serviceURL string) {
	if r.workspaceSlipHold() != nil {
		// Already taken, as claimSlip's own guard says: a second claim of one
		// slip is a call the service refuses, and the run would hold nothing
		// more for it.
		return
	}
	slipID := envLookup(r.d.Env, creds.PersonSlipEnv)
	if slipID == "" {
		// The workspace granted this launch no slip: a member whose launcher
		// holds no personal token, a launch that asked for none, a grant that
		// did not land (D3147). All of them are the run as it was.
		return
	}
	if serviceURL == "" {
		r.d.Log.Warn("this run holds no workspace slip: its job carries " + creds.PersonSlipEnv + " and the run's own route record serves a kind from the workspace without an address, which is a record this release could not have read")
		return
	}
	if err := checkSlipServiceURL(serviceURL); err != nil {
		// The rule the run's own claim holds its URL to, and for the same
		// reason: the claim carries the job's own Google ID token, and over
		// plain http to a host that is not this machine it would travel in
		// cleartext. The run proceeds without the workspace's slip.
		r.d.Log.Warn("this run holds no workspace slip: " + err.Error())
		return
	}
	body, err := json.Marshal(credsvc.SlipClaimRequest{Execution: slipExecution(r.d.Execution)})
	if err != nil {
		r.d.Log.Warn("this run holds no workspace slip: the claim call could not be built, so a credential the workspace serves under one is refused at the call")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, slipClaimBudget)
	defer cancel()
	ans, err := r.slipRetryCall(cctx, workspaceSlipWire, serviceURL, slipID, "", body)
	if slipRouteAbsent(ans.status) {
		// The workspace's service does not serve the claim route at all — one
		// release behind this runner, which is the version skew DS1.4 keeps
		// for the project's own service and keeps here too: it is quiet, and
		// the run proceeds on the path it took before the slip existed.
		return
	}
	if err != nil {
		r.d.Log.Warn("this run holds no workspace slip: the workspace's credentials service could not be asked for the claim, so a credential it serves under a slip is refused at the call", "err", r.redact(err.Error()))
		return
	}
	if ans.status != http.StatusOK {
		// A refusal: the workspace's service decided, and asking it again
		// answers the same thing. It is not a halt — the run has not been
		// refused a credential yet, and a refusal at the claim is the same
		// class as a claim that never landed (WC7's reading of a failed
		// grant, internal/cli's runSlipEnv).
		r.d.Log.Warn("this run holds no workspace slip: the workspace's credentials service refused the claim, so a credential it serves under a slip is refused at the call", "status", ans.status)
		return
	}
	var claim credsvc.SlipClaimResponse
	if err := json.Unmarshal(ans.body, &claim); err != nil {
		r.d.Log.Warn("this run holds no workspace slip: the workspace's answer to the claim is not a claim's")
		return
	}
	if !slipSecretUsable(claim.ClaimSecret) {
		r.d.Log.Warn("this run holds no workspace slip: the workspace's credentials service answered the claim with no usable claim secret")
		return
	}
	r.setWorkspaceSlipHold(slipID, claim.ClaimSecret, serviceURL)
	// Registered before anything can print it: the answer that carried it is
	// already out of this function, and every call, log line and failure after
	// it is redacted of it from here on.
	r.addSecret(claim.ClaimSecret)
	r.d.Log.Info("this run claimed the workspace's slip: the workspace's credentials service serves it the credentials the slip names, and every call this run makes to that service travels under it (R4)")
}

// checkSlipServiceURL refuses a URL this run will not send its own identity
// to: https, or http on a loopback address, which is a test's own service. The
// rule is the run's other service calls' and the launcher's grant call's,
// respelled because none of those helpers is reachable from here with a
// sentence fit for this call — this package's own checkReviewServiceURL wraps
// its refusals in the review signature's family, and a slip warning that named
// the review artifact would be a sentence about the wrong call.
//
// Both of this run's own claims hold the address to it: a slip's, which is
// given up over a refusal here, and the identity claim's (identity_claim.go),
// which is not — the job's own identity token is on that call, and a run with a
// ticket has no other way to reach an identity.
func checkSlipServiceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("the credentials service URL in this job's environment is not a scheme and a host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return errors.New("the credentials service URL in this job's environment is not https: the claim would carry the job's own identity token in cleartext")
}

// slipSecretUsable reports whether s is one claim secret this run will carry:
// non-empty, and one printable header value at the bound the service reads a
// presented one by (internal/credsvc's maxHeaderTokenBytes, which is
// unexported there and respelled here rather than guessed at). A secret this
// run cannot present is worse than none: every call would be refused at the
// service, where no secret at all is the run that never had a slip.
func slipSecretUsable(s string) bool {
	return len(s) <= maxSlipSecretBytes && printableToken(s)
}

// slipExecution is the execution name a claim reports: the short name of the
// execution this process is in, or nothing when this run cannot read one back
// (a local invocation has none). The service records it for an operator and
// decides nothing by it (F7, D1937), and it refuses a call whose execution is
// not one printable value of its own bound (internal/credsvc's
// maxSlipExecutionBytes), so a name this run cannot reduce to one is left out
// of the call rather than sent to be refused.
func slipExecution(execution string) string {
	id, ok := backend.ParseExecution(execution)
	if !ok || id.Name == "" || len(id.Name) > maxSlipExecutionBytes || !printableToken(id.Name) {
		return ""
	}
	return id.Name
}

// slipWire is the pair of header names one slip travels in: the project's own
// (credsvc's) or the workspace's (C1's, which the gateway declares a second time
// inside its own package because it cannot import this one). The two are never
// mixed on one call: which pair a claim or a beat is sent under is which service
// it is sent to, and that is R4 said as a spelling.
type slipWire struct {
	id    string
	claim string
}

// projectSlipWire is FUGARO_SLIP and credsvc's two names: the slip a member's
// own credentials service granted, which is DS1's whole wire.
var projectSlipWire = slipWire{id: credsvc.HeaderSlip, claim: credsvc.HeaderSlipClaim}

// workspaceSlipWire is C1's names for the workspace's own slip (WC8): the same
// call shape against the other service, presented under the two headers that
// service reads and never under a member's.
var workspaceSlipWire = slipWire{id: creds.PersonSlipHeader, claim: creds.PersonSlipClaimHeader}

// slipRetryCall makes one of this run's own calls to the claim route — the
// claim, or a beat after it — retrying the answers that are worth asking again
// about (spec §Resilience: a job retries its credential and proxy requests
// with backoff).
//
// An answer about the call is not retried: a refusal, a slip already claimed,
// a service that does not serve the route, all answer the same thing to a
// second call, and the retries would only spend the run's start on it. What the
// backoff steps over is an attempt this run got no answer to: a service that
// cannot be reached (a cold start, a refused connection, a reset), one that
// answers 5xx or its rate limit, and an attempt that never left -- a call this
// run could not build because the metadata server or its own identity was not
// there yet, and an answer whose body broke mid-read. The last two are the same
// class as the rest: nothing the service decided, and nothing this run can act
// on but to ask once more.
//
// The ctx is the call's own budget: a cancelled wait, and an in-flight request
// whose ctx is cancelled, both end it at once, which is what lets the beat be
// stopped mid-retry.
func (r *run) slipRetryCall(ctx context.Context, wire slipWire, serviceURL, slipID, secret string, body []byte) (slipAnswer, error) {
	for attempt := 0; ; attempt++ {
		ans, err := r.slipPost(ctx, wire, serviceURL, slipID, secret, body)
		if !slipRetryable(ans.status, err) || attempt >= len(slipRetryDelays) {
			return ans, err
		}
		if werr := slipRetryWait(ctx, slipRetryDelays[attempt]); werr != nil {
			return ans, werr
		}
	}
}

// slipRetryable reports whether an answer, or the failure to get one, is one
// the run asks again about. A call that got no answer at all is the service
// being unreachable — a cold start, a refused connection, a reset — and is the
// reason this retry exists.
func slipRetryable(status int, err error) bool {
	if err != nil {
		return true
	}
	return status == http.StatusTooManyRequests || status >= 500
}

// slipPost makes one POST to the claim route and reads the answer back, with
// the identities the service decides a call by: the job's own Google ID token,
// which Cloud Run IAM admits (D1935), the run's Firebase identity, which is
// what the claim is bound to (F23), and the job's name, which is recorded and
// decides nothing. The claim secret travels only when secret is set — the
// first call presents none, and every call after it presents the one the claim
// answered.
//
// The answer is bounded and the client never follows a redirect, and neither
// value is read into an error: the secret's own call is the one place it is on
// any wire but the service's, and a failure here is this run's sentence and
// not the service's text.
func (r *run) slipPost(ctx context.Context, wire slipWire, serviceURL, slipID, secret string, body []byte) (slipAnswer, error) {
	job, err := r.jobName()
	if err != nil {
		return slipAnswer{}, err
	}
	token, err := metadataIDToken(ctx, r.d.Env, serviceURL)
	if err != nil {
		return slipAnswer{}, err
	}
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		return slipAnswer{}, errors.New("this run has no budget identity, which is what the credentials service holds a claim to")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(serviceURL, "/")+credsvc.SlipClaimPath, bytes.NewReader(body))
	if err != nil {
		return slipAnswer{}, errors.New("the claim call's request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(gateway.HeaderRunIdentity, sess.IDToken()())
	req.Header.Set(gateway.HeaderRunJob, job)
	req.Header.Set(wire.id, slipID)
	if secret != "" {
		req.Header.Set(wire.claim, secret)
	}
	resp, err := slipClient.Do(req)
	if err != nil {
		// A cancelled call is not the service's fault and is not retried:
		// slipRetryable reads the ctx below.
		if ctx.Err() != nil {
			return slipAnswer{}, ctx.Err()
		}
		return slipAnswer{}, errors.New("the installation's credentials service could not be reached")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSlipAnswerBytes+1))
	if err != nil {
		return slipAnswer{status: resp.StatusCode}, errors.New("the credentials service's answer could not be read")
	}
	if len(data) > maxSlipAnswerBytes {
		return slipAnswer{status: resp.StatusCode}, errors.New("the credentials service's answer is larger than a claim's")
	}
	return slipAnswer{status: resp.StatusCode, body: data}, nil
}

// slipRouteAbsent reports whether status is the answer of a service that does
// not serve the claim route: the 404 its own router gives a path it does not
// serve (internal/credsvc's servedRoute, which answers 404 rather than 405 for
// a route asked with another method), and the 405 an intermediary may give a
// route it holds for another method. It is internal/cli's rule for the grant
// route, respelled because that package's helper is the launcher's half and
// this one cannot import it: the two sides of the skew have to read it the
// same way, and neither is the other's to own.
func slipRouteAbsent(status int) bool {
	return status == http.StatusNotFound || status == http.StatusMethodNotAllowed
}

// startSlipBeat sends the keep-warm request while the run lives: the same call
// that claimed the slip, presenting the claim secret instead of nothing, which
// the service answers from the slip it holds without a credential behind it
// (D1934). One request per interval is the cadence Check 511 measured the
// service answering at, with no cold start, so a run holds its own instance of
// the service warm rather than paying a cold start on the call that needs a
// credential.
//
// It is a no-op for a run that holds no slip, which is every run of an
// installation without the slip routes and every run whose claim did not land:
// there is nothing to keep warm with, and the run proceeds without it.
//
// The beat's own context is detached from the run's: the run's end is what
// ends the beat, and that is stopSlipBeat -- the one call that both cancels
// this context and waits for the goroutine -- rather than a cancellation
// reaching it from anywhere else. A beat that outlived the run would be a
// request of a finished run, and the service counts what reaches it.
func (r *run) startSlipBeat(ctx context.Context) {
	r.slipMu.Lock()
	hold := r.slip
	if hold == nil || hold.done != nil {
		r.slipMu.Unlock()
		return
	}
	beatCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	hold.cancel, hold.done = cancel, done
	r.slipMu.Unlock()
	go func() {
		defer close(done)
		t := time.NewTicker(r.slipBeatEvery())
		defer t.Stop()
		for {
			select {
			case <-beatCtx.Done():
				return
			case <-t.C:
				r.beatSlip(beatCtx, hold)
			}
		}
	}()
}

// stopSlipBeat ends the beat and waits for its goroutine: a beat still in
// flight when the run ends would be a request of a finished run, and the
// service counts what reaches it. It is safe to call on a run that never held
// a slip, and safe to call twice.
func (r *run) stopSlipBeat() {
	r.slipMu.Lock()
	hold := r.slip
	if hold == nil || hold.done == nil {
		r.slipMu.Unlock()
		return
	}
	cancel, done := hold.cancel, hold.done
	hold.cancel, hold.done = nil, nil
	r.slipMu.Unlock()
	cancel()
	<-done
}

// beatSlip asks the service to read the slip back. A failure here is not a
// run's failure — the run's own calls still present the claim — so it is one
// warning, once: a beat that cannot land usually cannot land for the rest of
// the run, and a line per interval would say the same thing every 15 s.
//
// The one failure that is not the service's is the run's own end: this
// context is cancelled by stopSlipBeat and by nothing else (startSlipBeat
// detaches it from the run's), so an attempt that ends with it cancelled is
// this run giving up a request of a run that is over. There is no next call to
// pay a cold start, and the warning below would report the run's own end as
// the service going away.
func (r *run) beatSlip(ctx context.Context, hold *slipHold) {
	body, err := json.Marshal(credsvc.SlipClaimRequest{Execution: slipExecution(r.d.Execution)})
	if err != nil {
		return
	}
	ans, err := r.slipRetryCall(ctx, hold.wire, hold.service, hold.id, hold.secret, body)
	switch {
	case slipRouteAbsent(ans.status):
		// The service stopped serving the claim route under a run that holds
		// a slip: a version skew in the middle of a run, which is the
		// update happening under it (docs/design/update-in-flight.md). There
		// is nothing to keep warm and no call of the run's is refused by it,
		// because the route that serves a credential is not this one.
		return
	case err != nil:
		if ctx.Err() != nil {
			return
		}
		r.slipWarnOnce(hold, "the keep-warm beat could not reach the installation's credentials service, so the instance serving this run may go cold and the next call pays a cold start (D1934)", err)
	case ans.status != http.StatusOK:
		r.slipWarnOnce(hold, "the installation's credentials service refused the keep-warm beat, so the instance serving this run may go cold and the next call pays a cold start (D1934)", fmt.Errorf("the service answered %d", ans.status))
	}
}

// slipWarnOnce writes the run's one line about a beat that did not land.
func (r *run) slipWarnOnce(hold *slipHold, msg string, err error) {
	r.slipMu.Lock()
	said := hold.beatSaid
	hold.beatSaid = true
	r.slipMu.Unlock()
	if said {
		return
	}
	r.d.Log.Warn(msg, "err", r.redact(err.Error()))
}

// slipHold is the slip this run took, or nil for a run that holds none.
func (r *run) slipHold() *slipHold {
	r.slipMu.Lock()
	defer r.slipMu.Unlock()
	return r.slip
}

// setSlipHold takes the claim. It is written once, by the claim.
func (r *run) setSlipHold(id, secret, service string) {
	r.slipMu.Lock()
	defer r.slipMu.Unlock()
	r.slip = &slipHold{id: id, secret: secret, service: service, wire: projectSlipWire}
}

// workspaceSlipHold is the workspace's slip this run took, or nil for a run the
// workspace granted none.
func (r *run) workspaceSlipHold() *slipHold {
	r.slipMu.Lock()
	defer r.slipMu.Unlock()
	return r.wsSlip
}

// setWorkspaceSlipHold takes the workspace's claim. It is written once, by the
// claim, and it never carries a beat: the workspace's service is kept warm by
// WS13's own beat, whose call presents the job's identity and the run's token
// and no slip at all (R18, F33).
func (r *run) setWorkspaceSlipHold(id, secret, service string) {
	r.slipMu.Lock()
	defer r.slipMu.Unlock()
	r.wsSlip = &slipHold{id: id, secret: secret, service: service, wire: workspaceSlipWire}
}

// workspaceBeat is the run's second beat (WS13, R10, R14, F33, D2985): the
// keep-warm request a run sends to its workspace's credentials service while it
// lives, once its own route record serves a kind of its credentials there.
//
// It is the slip beat's shape against the other service, and it exists for the
// same reason: the workspace pays the same cold start the installation's own
// service does, and a run that routed a paid call there should not pay it
// inside that call. What it is NOT is the slip beat's call: the workspace's
// service takes the job's identity and the run's own token and nothing else of
// the member's (R18, F40), so this call carries no slip id and no claim secret
// — the route it posts to is the one route of that service which answers
// nothing about a credential (R14).
//
// upstream is the gateway's own second upstream for this run, built where the
// record was read: the address is the record's (R3), and the two identities are
// the ones that upstream presents, read per request so a token that turns over
// under a long run is the fresh one. The beat therefore cannot call a different
// address, and cannot present a different identity, than the calls it keeps the
// service warm for.
type workspaceBeat struct {
	upstream *gateway.Workspace
	// cancel ends the beat and done is closed when its goroutine has returned,
	// as the slip hold's are (startWorkspaceBeat writes both, stopWorkspaceBeat
	// clears them).
	cancel context.CancelFunc
	done   chan struct{}
	// beatSaid is whether the run has written its one line about a beat that
	// did not land, so an unreachable service is stated once rather than every
	// interval. Guarded by workspaceMu, which the beat's goroutine and the run
	// both hold.
	beatSaid bool
}

// startWorkspaceBeat sends the keep-warm request to the workspace's credentials
// service for as long as the run lives (R10, R14, D2985). It is called where the
// run's route record is read and the second upstream is built, because that is
// the one place the run knows it has a kind served there: a record that moves no
// kind is a run that never calls the workspace at all, and it keeps nothing
// warm (contract-wire.md, "it builds the second upstream, and starts the beat,
// on Holder and not on Address").
//
// The switch is the run's own (agent.workspace_beat, creds.WorkspaceBeatSetting,
// C2): off means this run sends no beat, which costs a cold start at a call and
// never a credential — the owner's doubt of D2985 is why the switch exists and
// is why it is not a safety setting. The default is on, and it is read here
// rather than at the call site so the one place a beat is decided is one
// function.
//
// The cadence is the slip beat's (slipBeatEvery): one run keeps one interval,
// and both beats are the same kind of request to a service that pays the same
// cold start. The context is detached from the run's for the slip beat's reason
// — the run's end is stopWorkspaceBeat, and nothing else ends this beat — so a
// request of the beat is never made on behalf of a finished run.
func (r *run) startWorkspaceBeat(ctx context.Context, ws *gateway.Workspace) {
	if ws == nil || ws.Address == "" {
		return
	}
	if !r.workspaceBeatOn() {
		return
	}
	r.workspaceMu.Lock()
	if r.workspaceBeat != nil {
		r.workspaceMu.Unlock()
		return
	}
	beatCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	beat := &workspaceBeat{upstream: ws, cancel: cancel, done: done}
	r.workspaceBeat = beat
	r.workspaceMu.Unlock()
	go func() {
		defer close(done)
		t := time.NewTicker(r.slipBeatEvery())
		defer t.Stop()
		for {
			select {
			case <-beatCtx.Done():
				return
			case <-t.C:
				r.beatWorkspace(beatCtx, beat)
			}
		}
	}()
}

// workspaceBeatOn is whether this run sends the keep-warm beat to its
// workspace's credentials service: the run's own switch, on unless
// agent.workspace_beat says otherwise (R10, D2985, creds.WorkspaceBeatSetting).
// A run with no resolved configuration at all — which no run of this release
// is, the config being what selected its workflows — sends none rather than
// panicking on a nil dereference: the beat is an optimisation, and a run that
// cannot read its own switch does not guess one.
func (r *run) workspaceBeatOn() bool {
	if r.cfg == nil {
		return false
	}
	return r.cfg.Agent.WorkspaceBeatOn()
}

// stopWorkspaceBeat ends the beat and waits for its goroutine, as stopSlipBeat
// does for the slip's: a beat still in flight when the run ends would be a
// request of a finished run, and the workspace's service counts what reaches it
// (R14). It is safe to call on a run that never had a beat, and safe to call
// twice.
func (r *run) stopWorkspaceBeat() {
	r.workspaceMu.Lock()
	beat := r.workspaceBeat
	if beat == nil || beat.done == nil {
		r.workspaceMu.Unlock()
		return
	}
	cancel, done := beat.cancel, beat.done
	beat.cancel, beat.done = nil, nil
	r.workspaceMu.Unlock()
	cancel()
	<-done
}

// beatWorkspace posts one keep-warm request to the workspace's credentials
// service. The call is made through gateway.Service, which is the one client of
// that service's contract this run has: it presents the job's Google ID token,
// the run's own token and the job's name, and it presents the slip only if the
// Service holds one — this beat's carries none, so no slip id and no claim
// secret travel (R18, F33), which gateway.Workspace's own upstream states as a
// property of the shape rather than a rule a later edit could forget.
//
// A failure here is not a run's failure — the calls this beats for are the
// gateway's, and a beat that did not land costs a cold start and nothing else —
// so it is one warning, once, as the slip beat's is. The one failure that is
// not the service's is the run's own end: this context is cancelled by
// stopWorkspaceBeat and by nothing else, so an attempt that ends with it
// cancelled is this run giving up a request of a run that is over, and there is
// no next call to warn about.
func (r *run) beatWorkspace(ctx context.Context, beat *workspaceBeat) {
	ws := beat.upstream
	svc := gateway.Service{
		Job:        ws.Job,
		RunIDToken: ws.RunIDToken,
		IDToken:    func(ctx context.Context) (string, error) { return ws.IDToken(ctx, ws.Address) },
	}
	status, _, err := svc.Post(ctx, ws.Address, credsvc.WorkspaceKeepWarmPath, nil)
	switch {
	case workspaceKeepWarmAbsent(status):
		// The workspace's service does not serve the keep-warm route: a
		// version skew, and the shape C1 states before the route is deployed
		// (WS2 serves it). Nothing of the run's is refused by it — the route
		// that serves the run's calls is another one — so this is not a
		// warning, and it is the same reading slipRouteAbsent gives the claim
		// route.
		return
	case err != nil:
		if ctx.Err() != nil {
			return
		}
		r.workspaceWarnOnce(beat, "the keep-warm beat could not reach this run's workspace credentials service, so the instance serving it may go cold and the next call pays a cold start (R14, D2985)", err)
	case status != http.StatusOK:
		r.workspaceWarnOnce(beat, "this run's workspace credentials service refused the keep-warm beat, so the instance serving it may go cold and the next call pays a cold start (R14, D2985)", fmt.Errorf("the service answered %d", status))
	}
}

// workspaceKeepWarmAbsent reports whether status is the answer of a workspace
// service that does not serve the keep-warm route: the same two the claim
// route's own reading takes (slipRouteAbsent), which is the same skew seen from
// the other service.
func workspaceKeepWarmAbsent(status int) bool { return slipRouteAbsent(status) }

// workspaceWarnOnce writes the run's one line about the workspace beat not
// landing.
func (r *run) workspaceWarnOnce(beat *workspaceBeat, msg string, err error) {
	r.workspaceMu.Lock()
	said := beat.beatSaid
	beat.beatSaid = true
	r.workspaceMu.Unlock()
	if said {
		return
	}
	r.d.Log.Warn(msg, "err", r.redact(err.Error()))
}
