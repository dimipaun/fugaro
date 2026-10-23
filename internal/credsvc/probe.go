package credsvc

// probe.go is C1.12: the probe.
//
// A credential that was backed off — a usage limit that reported no reset time
// — is not usable again by waiting. Its wait only says when the question may be
// asked, and the question is whether the credential works at all (C1.9's
// NeedsProbe): a token whose quota is gone and a token that was revoked look
// exactly alike when the upstream reports no reset. So before real work is
// routed to such a credential again, this file makes the minimal call that asks
// the question, and writes what the answer means: a success clears the mark
// (unless D142's first control holds the token for the rest of the window its
// exhaustion opened, which ProbeSucceeded decides), a failure doubles the wait
// to its cap, and a 401 is D139's broken mark, which only an operator clears
// (C1.21).
//
// The probe is called from the selection path and from nowhere else: it is due
// exactly when a selection answers that the earliest candidate is backed off
// with its wait passed (creds.ExhaustedUntil.Probe), which is the moment a run
// is about to be told to wait and might instead be served. Nothing here
// selects: after a probe the loop in forward selects again, and the state the
// probe just wrote is what the next selection reads.
//
// Four rules shape everything below.
//
//   - A probe runs only where the state says one is due: a backed-off mark
//     whose own wait has passed (Mark.NeedsProbe). Never on a broken credential
//     — a probe is not what clears a 401 — and never on an exhausted one, whose
//     reset is a time rather than a question. The credentials it can be run on
//     are the ones this call was offered AND the ones the policy admits: the
//     candidate list carries every credential in the bundle, and it is
//     creds.Select's tiers() that keeps the paid tier out of the cascade — a
//     probe decides for itself and so has to ask the same question
//     (probeCandidates). A probe that did not would spend the project's money
//     under `oauth-only` (D152) on a credential Select would never have chosen,
//     and — worse — would clear its mark, handing the next selection a usable
//     paid key under a policy that says never.
//   - One probe per credential at a time, and one per credential per call. The
//     prober first takes a lease under creds/, conditionally, so two instances
//     that notice the same backed-off credential at the same instant do not
//     both ask: a minimal call costs a fraction of a real one, but the answer
//     is written to a SHARED token's state, and two probers racing it would
//     double the wait twice over for one exhaustion. The per-call half is the
//     forward loop's own bound: a name this call already probed is never probed
//     again by the same call, so the loop cannot spin on a mark that keeps
//     answering "probe me".
//   - Every probe is audited (C1.13), and a probe whose line cannot be written
//     is not recorded: the line is written BEFORE the mark, and a line that
//     could not be written leaves the credential exactly as it was — backed
//     off, and still waiting. That order is D143's fail-closed rule where it
//     actually bites: the mark is the write that puts a credential back in
//     rotation, so it is the write the audit line must precede. The probe's
//     answer itself is already out of this service's hands by then (it was
//     made), so what is withheld is its EFFECT, which is the whole of what this
//     service decides.
//   - Nothing about the call reaches the probe but what the question needs: its
//     path and the model it names. The headers are this service's own, and the
//     body is built here, so a run cannot shape a credential-bearing call on a
//     shared token — a run that could set a header on the probe could make a
//     probe fail and push a donated token into a longer wait, and a run's own
//     headers are exactly the thing Check 90's allow-list exists to keep off
//     the subscription path. The model is the one exception and it is the
//     question itself: a probe asks whether the credential can serve THIS
//     run's work, and the model is what the work is.
//   - The probe is the service's, not the client's. It is detached from the
//     caller's context and bounded by the service (probeBudget and
//     probeRecordBudget, together below probeLeaseTTL): the call, the audit line
//     and the mark are facts about a credential, and a run that hangs up
//     mid-probe must not be able to discard them — a detached probe's answer is
//     still recorded, and an aborted one is not mistaken for a credential that
//     failed.
//
// Nothing here writes a credential's value anywhere: not to the client (the
// probe's answer is read for its status and closed, never relayed), not to the
// log (the lines name a credential and never carry one), and not to the state
// (a mark is a state and a time). The lease node carries the run that took it,
// which is two names.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/creds"
)

// ProbeLeases is the conditional write the probe's one-at-a-time lease needs:
// the ETag discipline the state's own writes use (creds.DB, F17), on one node
// under creds/ that is not a mark and not a run's lease.
//
// It is separate from Config.State for the reason Config.DB is separate from
// it: creds.Store is a selection's state and has no delete and no raw node, so
// a lease that is not a credential's mark and not a run's placement has no
// place in it. *rtdb.Client is one, and so is creds.DB.
type ProbeLeases interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// The probe's node and its bounds.
const (
	// pathProbes is where the one-probe-at-a-time leases live: a sibling of the
	// marks, the leases and the stickiness records, under the subtree the
	// database rules deny every run token (O1, C1.3). A probe lease is keyed by
	// the credential's name, like a mark, because the shared thing it protects
	// is a credential and not a run.
	pathProbes = "creds/probes"

	// probeLeaseTTL is how long a claim stands without being refreshed. It is
	// the bound the subscription path puts on an answer's headers
	// (gateway's oauthResponseHeaderTimeout, ten minutes), which is the longest
	// a minimal non-streaming probe of either tier should take, and it is a
	// BOUND rather than a lock, exactly as creds' lease TTL is: an instance that
	// dies mid-probe stops holding the credential's probing for at most this,
	// and an instance that probes past it may find itself probing alongside
	// another. Two minimal calls cost two minimal calls; a credential that can
	// never be probed again because a dead instance's claim never lapsed costs
	// the run every call it makes.
	probeLeaseTTL = 10 * time.Minute

	// probeBudget is how long a probe's own upstream call may take once it is
	// detached from the client, and probeRecordBudget is how long the audit line
	// and the mark that record its answer may then take.
	//
	// The two together are BELOW probeLeaseTTL, and that is the point of the
	// numbers: a probe still in flight when its own claim lapses is a probe
	// another instance may start alongside (the claim is a bound, not a lock),
	// so the whole of the work is bounded under the claim it took. Five minutes
	// is also the longest a minimal, non-streaming probe is worth waiting for —
	// the subscription path's own header bound is ten
	// (oauthResponseHeaderTimeout), and a credential that has not answered one
	// token in half of it is not going to answer the run's work either — and the
	// recording gets a minute of its own rather than whatever the call left,
	// because a probe that used its whole budget must still be able to write the
	// answer it took five minutes to get. Two small writes under a minute is
	// generous on purpose: the cost of running out is a real answer discarded.
	probeBudget       = 5 * time.Minute
	probeRecordBudget = time.Minute

	// probeVersion is the API version a probe asks with: the one the client's
	// SDK sends, and the one Check 90 proved Anthropic-Version mandatory with
	// (gateway/oauth.go). Check 90's only measured fact about it is that
	// dropping it fails the call, so a probe that dropped it would fail for a
	// reason that has nothing to do with the credential.
	probeVersion = "2023-06-01"

	// probeContent and probeMaxTokens are the smallest Messages request this
	// service can make: one character in, one token out. The question a probe
	// asks is whether the credential is accepted and can serve this run's work
	// — not what it answers — so the answer is thrown away and its size is the
	// only thing worth keeping small.
	probeContent   = "."
	probeMaxTokens = 1

	// maxProbeModelBytes bounds the model a probe copies out of the run's own
	// body. A model id is a short name; the run's body is the caller's, bounded
	// only by what the gateway forwards, and this service must not build an
	// upstream body out of an unbounded run of it.
	maxProbeModelBytes = 200
)

// probeLease is one credential's probe claim: which run took it, and until
// when it stands. The run is recorded so an operator reading the subtree can
// see who is probing, and because a claim with no holder is a lock rather than
// a lease — this one lapses on its own and nothing deletes it.
type probeLease struct {
	Slug  string    `json:"slug"`
	Run   string    `json:"run"`
	Until time.Time `json:"until"`
}

// probeLeasePath is one credential's probe-lease node. The name is escaped the
// one way every node under creds/ escapes one (budget.Key, creds' own seg), so
// a name and the node it names cannot disagree with the marks' spelling. A name
// that escapes to nothing is impossible: the names come from the bundle, which
// holds no empty one.
func probeLeasePath(name string) string { return pathProbes + "/" + budget.Key(name) }

// probeCall is the minimal call a probe makes, built from the run's own call:
// the same path — the endpoint the credential is being asked about is the one
// the run is about to use — and the model the run's body names, with this
// service's own headers and its own body.
//
// ok is false when the run's body names no model this service will put in a
// probe: the call is not a model call this service can make, so there is
// nothing to ask. The caller then answers the run the wait it was going to be
// answered anyway, which is the fail-closed direction — a probe of "does this
// token work" that asked something else would be answering a question nobody
// asked.
func probeCall(cl *call) (*call, bool) {
	model, ok := callModel(cl.body)
	if !ok {
		return nil, false
	}
	switch cl.path {
	case MessagesPath, CountTokensPath:
	default:
		// Unreachable through ServeHTTP, which serves those two paths and
		// nothing else. A refusal rather than a panic, like every other
		// unreachable branch the service answers.
		return nil, false
	}
	return &call{
		path: cl.path,
		body: probeBody(cl.path, model),
		// The caller's own tokens travel with the probe as the scrubs they are.
		// Nothing in a probe's header set carries one — the headers are this
		// file's own — and a change that ever copied one of the caller's would
		// be scrubbed by the same call the forward path makes.
		scrubs: cl.scrubs,
		header: probeHeaders(),
	}, true
}

// probeBody is the body a probe sends: a Messages request asking for one token,
// or — on the count-tokens path, whose API refuses every field it does not
// take — the same body without max_tokens.
//
// The model is re-marshalled rather than pasted: it came out of a JSON
// document, and a probe body is built by this service and not assembled from a
// caller's text.
func probeBody(path, model string) []byte {
	req := probeRequest{
		Model:    model,
		Messages: []probeMessage{{Role: "user", Content: probeContent}},
	}
	if path == MessagesPath {
		req.MaxTokens = probeMaxTokens
	}
	b, err := json.Marshal(req)
	if err != nil {
		// A struct of two strings and an int cannot fail to marshal. An empty
		// body rather than a panic: send would refuse it, and a service that
		// crashes on its own bug is a service that stops answering every run.
		return nil
	}
	return b
}

// probeRequest and probeMessage are the body above. Their field names are
// Anthropic's, and they are written out rather than built as a map so the probe
// body is one shape and cannot gain a field by accident.
type probeRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens,omitempty"`
	Messages  []probeMessage `json:"messages"`
}

type probeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// probeHeaders is the header set a probe carries: the content types the API
// needs, and the one header Check 90 proved mandatory for a subscription call —
// and nothing from the call the probe stands beside. See the file's fourth rule
// for why the caller's headers are not copied: on the subscription path they
// are exactly what Check 90's allow-list refuses, and on either tier a run must
// not be able to make a probe fail.
func probeHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("Anthropic-Version", probeVersion)
	return h
}

// callModel is the model the run's own body names, and whether it named one
// this service will probe with.
func callModel(body []byte) (string, bool) {
	var head struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return "", false
	}
	if head.Model == "" || len(head.Model) > maxProbeModelBytes {
		return "", false
	}
	return head.Model, true
}

// probeOutcome is what one probe's answer means for the credential's mark. The
// four are Credential's own algebra (C1.9), named here so the audit status and
// the mark are read off one value instead of two switches that can disagree.
type probeOutcome int

const (
	// probeWorked: the credential served the minimal call. Probed.
	probeWorked probeOutcome = iota
	// probeRefused: a 401 — the token is dead (D139). Broken, and out until an
	// operator clears it (C1.21).
	probeRefused
	// probeLimited: a 429 — the credential is exhausted again, with whatever
	// reset the answer named and none if it named none. Exhausted, which is the
	// same wait-doubling as a failure when no reset was named, and Since moves
	// because the credential was exhausted again.
	probeLimited
	// probeUnproven: the credential did not serve the call and nothing says the
	// token is dead: a 5xx, a 403 for a model this credential may not use, a
	// 400 for a request the service built, or a connection that never reached
	// Anthropic at all. The wait doubles. It is the fail-closed direction — an
	// unproven credential is not put back in rotation — and it is the plan's
	// own rule read literally ("a failed probe doubles the wait to the 5 h
	// cap"): only an answer that says the credential WORKS is a success.
	probeUnproven
)

// classifyProbe reads one probe's answer as a mark's worth of meaning, and the
// reset the answer named — read only for probeLimited, and zero when the answer
// named none.
func classifyProbe(resp *http.Response, err error, now time.Time) (probeOutcome, time.Time) {
	if err != nil || resp == nil {
		return probeUnproven, time.Time{}
	}
	switch status := resp.StatusCode; {
	case unauthorizedStatus(status):
		return probeRefused, time.Time{}
	case usageLimitStatus(status):
		// The same reader the forward path uses, and the same rule: the latest
		// reset the answer named, or none, which is the backed-off case.
		return probeLimited, usageLimitReset(resp.Header, now)
	default:
		if status >= 200 && status < 300 {
			return probeWorked, time.Time{}
		}
		return probeUnproven, time.Time{}
	}
}

// probeDue is the probe the selection path runs when one is owed, and it
// answers whether the call should select again. It is called with the error a
// selection answered, and it does nothing at all unless that answer is a probe
// being due.
//
// probed is this call's own record of what it has already probed: a name in it
// is never probed again by the same call, which is what bounds the forward loop
// (each probe either changes the state or is not made, and a probe that changes
// nothing would otherwise be asked for again and again).
func (s *Server) probeDue(ctx context.Context, sel *creds.Selector, id Identity, cl *call, b *Bundle, cands []creds.Credential, probed map[string]bool, err error) bool {
	var until *creds.ExhaustedUntil
	if !errors.As(err, &until) || !until.Probe {
		// Nothing a probe can answer. A refusal with no time to wait for is the
		// operator's business (a broken mark, C1.21), and a real wait is a wait:
		// the credential's reset is a time, not a question.
		return false
	}
	now := s.now()
	marks, merr := s.cfg.State.Marks(ctx)
	if merr != nil {
		// The state could not be read, so which credential the probe is due on
		// cannot be known. The selection's own refusal (an unreadable state,
		// which it has already answered with) is what the run is told.
		return false
	}
	c, ok := firstDue(probeCandidates(cands, sel.Settings), marks, now, probed)
	if !ok {
		// A selection said a probe is due and the marks say none is: the state
		// moved between the two reads (another instance probed the credential,
		// which is what the answer wanted). Refusing is the fail-closed
		// direction and the run's answer is the wait it was given.
		return false
	}
	probed[c.Name] = true
	e, ok := b.Lookup(c.Name)
	if !ok {
		// Unreachable: the candidates came from this bundle. A refusal rather
		// than a panic, because the alternative is a service that crashes on a
		// state it cannot name.
		return false
	}
	return s.probeOne(ctx, sel, id, creds.Run{Slug: id.Slug, ID: id.Run}, c, e, cl)
}

// probeCandidates is the candidates a probe may run on, which is the candidates
// the policy admits and not the ones the bundle holds.
//
// The two lists are not the same, and that is the whole reason this exists: the
// bundle's candidates are every credential the caller offered, and the tier
// filter that keeps the paid key out of the cascade under `oauth-only` lives
// inside creds.Select (Settings.tiers, D152). A probe is made on a credential
// the selection did NOT hand back — it is made because none could be handed
// back — so it cannot inherit that filter and has to apply it itself. Asking
// creds' own accessor rather than restating the rule keeps the two in step:
// Fallback.Spends is what tiers() reads, and its zero value (no written
// fallback) does not spend, which is the fail-closed direction for a Settings
// this service did not build.
//
// The tier-A half is what is left when the policy does not spend, which is
// reachableOnly's rule on the probe's side: a tier-B credential is a
// possibility only under a written `fallback: auto`.
func probeCandidates(cands []creds.Credential, set creds.Settings) []creds.Credential {
	if set.Fallback.Spends() {
		return cands
	}
	out := make([]creds.Credential, 0, len(cands))
	for _, c := range cands {
		if c.Tier == creds.TierA {
			out = append(out, c)
		}
	}
	return out
}

// firstDue is the candidate a probe is due on: the most overdue of the ones
// whose backed-off wait has passed. It is creds' own choice, taken here rather
// than guessed at — `earliest` picks the soonest-usable candidate the same way
// — and the most overdue one is the credential a successful probe helps soonest.
//
// Ties go to the caller's order, which is the bundle's, so a call is
// deterministic about which of two equally overdue credentials it asks about
// first.
func firstDue(cands []creds.Credential, marks map[string]creds.Mark, now time.Time, probed map[string]bool) (creds.Credential, bool) {
	var out creds.Credential
	var at time.Time
	found := false
	for _, c := range cands {
		if probed[c.Name] {
			continue
		}
		m, ok := marks[c.Name]
		if !ok || !m.NeedsProbe(now) {
			continue
		}
		if !found || m.Until.Before(at) {
			out, at, found = c, m.Until, true
		}
	}
	return out, found
}

// probeCallContext is the context a probe's upstream call runs on, and
// probeRecordContext the one its audit line and its mark run on: both detached
// from the caller — a run that hangs up mid-probe must not be the reason a
// credential's question goes unanswered — and each bounded by its own budget.
//
// They are two functions rather than two inline calls so that the bound each
// phase is given is a thing a test can read off the context it gets back,
// instead of a constant it can only add up: a recording that took the call's
// budget would leave the whole of the work at probeLeaseTTL, i.e. exactly the
// point at which another instance may probe alongside this one.
func probeCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), probeBudget)
}

func probeRecordContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), probeRecordBudget)
}

// probeOne makes the probe on one credential, audits it and records what it
// means, answering whether the call should select again.
//
// The order is the whole of the fail-closed rule (the file's third): the call
// is made, the answer is classified, the line is written, and only then is the
// mark. A line that could not be written leaves the mark where it was — the
// credential is still backed off and the run still waits — and the service logs
// why, in a line that names the credential and never a value. The probe itself
// cannot be un-made (it is out of this service's hands by then), so what is
// withheld is its effect, which is the part this service decides.
func (s *Server) probeOne(ctx context.Context, sel *creds.Selector, id Identity, run creds.Run, c creds.Credential, e Entry, cl *call) bool {
	probe, ok := probeCall(cl)
	if !ok {
		return false
	}
	claimed, cerr := s.claimProbe(ctx, e.Name, run)
	if cerr != nil {
		// The lease could not be read. No probe is made: whether one is in
		// flight is exactly what could not be established, and probing without
		// knowing is what the lease is for.
		s.log.Warn("credsvc: the probe lease could not be read", "credential", e.Name, "error", cerr)
		return false
	}
	if !claimed {
		return false
	}
	// The slot is let go as soon as this probe is done with it. The claim is
	// held against a CONCURRENT probe and not against the next one, and it is
	// the only thing that must be true while the call is in flight — so holding
	// it for its whole TTL after the answer landed would refuse the next probe
	// to whoever asks first, this instance included. That is not a theoretical
	// case: an installation whose policy sets `backoff.initial` below the claim's
	// TTL (projectpolicy accepts one minute) has a credential due for its second
	// probe long before the claim written by its first one has lapsed, and a run
	// refused there is answered a wait that is already in the past — a runner
	// re-running the stage in a loop until the claim expires. Releasing it on the
	// way out, on every path, is the fix; the TTL stays what it is, which is the
	// bound on an instance that dies mid-probe and never reaches this line.
	// WithoutCancel: letting the slot go is bookkeeping about a call that was
	// already made, so a client that hung up while the probe was in flight must
	// not be the reason the next run's probe is refused.
	defer s.releaseProbe(context.WithoutCancel(ctx), e.Name, run)
	// The probe's own work is detached from the client's context, and bounded
	// (cascade.go does the same for the one mark it writes from an answer that
	// was already streamed). A run that hangs up mid-probe must not be the
	// reason a credential's question goes unanswered: the call has already been
	// made and charged, and everything below it — the answer being read, the
	// audit line, the mark — is a fact about the credential rather than about
	// the client. Left on the client's context, a disconnect turns a successful
	// probe into a discarded one whose audit line reads "exhausted": the
	// credential keeps a wait nobody asked for, twice, if the client's own
	// abandonment is counted as the credential failing.
	//
	// Two phases, two budgets: the call, then the recording. The recording gets
	// its own budget afresh rather than inheriting whatever the call left, because
	// a probe that used its whole budget must still be able to write the answer it
	// took five minutes to get. Neither budget is the other's, and neither is
	// taken from the caller: both are probeCallContext/probeRecordContext.
	callCtx, callDone := probeCallContext(ctx)
	ans, err := s.send(callCtx, probe, e)
	callDone()
	if ans != nil {
		// The call was made, so it is the run's to account for (C1.16): the tier
		// it was made on is recorded on the call the run is waiting for, and the
		// answer carries it (HeaderProbe). It is recorded here, where the call
		// happened, and not after the audit line below: the call is made either
		// way, and an audit line that could not be written withholds the probe's
		// EFFECT (the mark) and never the fact that a call was spent.
		cl.probed = append(cl.probed, wireTier(c.Tier))
	}
	recordCtx, recordDone := probeRecordContext(ctx)
	defer recordDone()
	reset := time.Time{}
	var outcome probeOutcome
	if ans != nil {
		// The answer is read for its status and closed: it is a probe's answer,
		// not the run's, and not one byte of it is relayed anywhere. It is not
		// scanned for a usage limit inside a stream either — a probe asks for a
		// non-streaming answer, and a stream here would be an answer to a
		// request nobody made.
		defer ans.resp.Body.Close()
		outcome, reset = classifyProbe(ans.resp, nil, s.now())
	} else {
		outcome, reset = classifyProbe(nil, err, s.now())
	}
	status := probeStatus(outcome)
	if aerr := s.audit.Write(recordCtx, id.Audit(AuditEvent{
		Credential: e.Name,
		Tier:       wireTier(c.Tier),
		Purpose:    PurposeProbe,
		Status:     status,
	})); aerr != nil {
		s.log.Warn("credsvc: a probe could not be audited, so its result was not recorded",
			"credential", e.Name, "tier", wireTier(c.Tier), "status", status, "error", aerr)
		return false
	}
	var werr error
	switch outcome {
	case probeWorked:
		_, werr = sel.Probed(recordCtx, c.Name, c.Tier)
	case probeRefused:
		_, werr = sel.Broken(recordCtx, c.Name)
	case probeLimited:
		_, werr = sel.Exhausted(recordCtx, c.Name, c.Tier, reset)
	default:
		_, werr = sel.ProbeFailed(recordCtx, c.Name, c.Tier)
	}
	if werr != nil {
		// The mark could not be written, which is the forward path's own
		// refusal (markRefusal) and here is the same failure one step earlier:
		// the next call's selection is made from the state, so a probe whose
		// result could not be recorded is a probe whose result does not exist.
		// The run is answered the wait it was going to be answered.
		s.log.Warn("credsvc: a probe's result could not be recorded",
			"credential", e.Name, "tier", wireTier(c.Tier), "error", werr)
		return false
	}
	return true
}

// probeStatus is what the audit line says happened, in the contract's own
// vocabulary (C1.13): StatusOK for a credential that answered, and for
// everything else the status of the mark the probe leaves — ReasonExhausted for
// a credential that stays held, for longer, and ReasonBroken for a token that
// answered 401. Both of the latter are reasons a credential was not used, which
// is what ValidStatus's set is.
func probeStatus(o probeOutcome) string {
	switch o {
	case probeWorked:
		return StatusOK
	case probeRefused:
		return ReasonBroken
	default:
		return ReasonExhausted
	}
}

// claimProbe takes this run's claim on one credential's probe slot, and answers
// whether it got it.
//
// The write is conditional on the node it read, the way every write under
// creds/ is (F17): two instances reading the same lapsed lease at once must not
// both write over it, and the loser must not overwrite the winner's claim with
// its own — an unconditional write would put a second prober on the credential
// while the first one's call is still in flight. A node that moved under the
// write is read again, up to the number of attempts the state's own writes use;
// a node this service cannot read at all is not claimed either (GetETag's error
// is returned), because an unknown node under creds/ is the operator's to clear
// and guessing at it would be guessing at whether a probe is in flight.
//
// ok=false with a nil error is the answer "someone else is probing, or the
// database would not take the write": either way this call makes no probe and
// the run is answered the wait it was given. The error says only that the lease
// could not be read, which is the caller's to log and no probe is made on
// either answer.
func (s *Server) claimProbe(ctx context.Context, name string, run creds.Run) (ok bool, err error) {
	now := s.now()
	path := probeLeasePath(name)
	claim := probeLease{Slug: run.Slug, Run: run.ID, Until: now.Add(probeLeaseTTL)}
	var last error
	for i := 0; i < creds.DefaultAttempts; i++ {
		var cur probeLease
		etag, found, err := s.cfg.Probes.GetETag(ctx, path, &cur)
		if err != nil {
			return false, err
		}
		if found && cur.Until.After(now) {
			// A live claim: another instance is probing this credential now.
			// Nothing is logged — this is the mechanism working — and the run
			// waits as it would have.
			return false, nil
		}
		if err := s.cfg.Probes.PutIfMatch(ctx, path, etag, claim); err == nil {
			return true, nil
		} else {
			last = err
		}
	}
	// The node kept moving under the claim (or the database kept refusing it).
	// No probe is made, which is the same answer as a lost race, and the last
	// error is logged because a database that will not take this write is worth
	// an operator's attention while a lost race is not.
	s.log.Warn("credsvc: the probe lease could not be taken", "credential", name, "error", last)
	return false, nil
}

// releaseProbe lets one credential's probe slot go, by writing the claim that
// stands on it back with a time that is not in the future. The store has no
// delete, deliberately (creds' own rule for everything under creds/), so a
// claim is released by lapsing it rather than by removing it — which is also
// what an operator reading the subtree wants to see: the node stays, and it
// says who probed last and that they are done.
//
// Only this run's own claim is released. If the node no longer names this run,
// another instance's claim replaced this one (this probe outlasted the TTL, and
// the slot was taken again while it was still in flight), and writing over that
// would be letting go of a claim that is holding somebody else's probe — the
// one thing the claim exists to prevent.
//
// A release that cannot be written is logged and nothing else: the claim then
// lapses by its own TTL, which is the bound it already had, and the probe's own
// result is unaffected — this runs after the mark is written.
func (s *Server) releaseProbe(ctx context.Context, name string, run creds.Run) {
	now := s.now()
	path := probeLeasePath(name)
	var last error
	for i := 0; i < creds.DefaultAttempts; i++ {
		var cur probeLease
		etag, found, err := s.cfg.Probes.GetETag(ctx, path, &cur)
		if err != nil {
			last = err
			break
		}
		if !found || cur.Slug != run.Slug || cur.Run != run.ID {
			return
		}
		// Until is now, which is not "after now": claimProbe reads a claim as
		// live only while its Until is in the future, so this is the released
		// form of the same node.
		if err := s.cfg.Probes.PutIfMatch(ctx, path, etag, probeLease{Slug: run.Slug, Run: run.ID, Until: now}); err == nil {
			return
		} else {
			last = err
		}
	}
	s.log.Warn("credsvc: a probe's claim could not be released, so it stands until it lapses",
		"credential", name, "error", last)
}

// NewProbeAuditor returns the auditor this service's probed acts are written
// with (C1.13), armed with the redaction the bundle loader answers.
//
// It is the one place the two are put together, and it is why the service does
// not take an Auditor in its configuration: audit.go's own rule is that the
// code holding the values is the code that removes them, and the loader is that
// code (C1.11, F32). A service whose auditor was armed from something else — or
// not armed at all, which NewAuditor refuses — would be a service whose audit
// lines could carry a credential's value, so the arming is not a caller's
// choice to make.
//
// handler is the handler and not a logger, deliberately: see NewAuditor's own
// note on why a logger's Handler carries attrs and groups that must not reach
// this line.
func NewProbeAuditor(handler slog.Handler, l *BundleLoader) (*Auditor, error) {
	if l == nil {
		return nil, errors.New("credsvc: the audit log has no bundle loader to redact credential values with")
	}
	return NewAuditor(handler, WithAuditScrub(l.Scrub))
}

// Scrub removes every credential value the loader currently holds from s. It is
// the service's redaction as audit.go's WithAuditScrub wants it: the loader is
// the one place the service reads a credential (Q6), so it is the one place
// that can answer what a value is.
//
// It scrubs the document the loader has READ — the cache it serves calls from
// — and nothing else, which is exactly the set the service can hand out: a
// document that was replaced is not in memory any more, and the loader never
// serves one it refused. The replacement itself is the same string the
// gateway's own redaction writes into a body (redactedValue), so a value
// removed here and a value removed there read the same way.
//
// The read is under the loader's own lock, because a rotation replaces the
// cached document while calls are being served. It takes no lock of its own
// beyond that one and never calls out, so a scrub can never deadlock a Load
// (which holds the lock only to read or replace the cache, never while reading
// a source).
func (l *BundleLoader) Scrub(s string) string {
	if l == nil || s == "" {
		return s
	}
	l.mu.Lock()
	b := l.cached
	l.mu.Unlock()
	if b == nil {
		// Nothing has been read yet, so this loader holds no value to remove.
		//
		// This is reachable for an audit line, and not only for a probe's: the
		// auditor the service writes EVERY line through is armed with this
		// (NewProbeAuditor), so every refusal answered between the identity
		// check and the first bundle read — the binding, the stop switches, an
		// unreadable policy, a body this endpoint cannot read, an unreadable
		// bundle — is scrubbed here, against no values at all. That is safe
		// rather than lucky: no field of such a line can hold a credential's
		// value — the credential field is empty (no credential was chosen), the
		// purpose and the status are this package's own constants, and the
		// identity's four fields are the ones auth.go has already held to
		// token.ValidSegment, ValidJobName and the account rule — and a scrubber
		// that invented a redaction for a value it does not hold would be
		// redacting text that is not a credential. A probe's own lines are
		// written after its bundle read, which is what the loader's order
		// guarantees for them.
		return s
	}
	for _, e := range b.entries {
		if v := e.Value(); v != "" && strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, redactedValue)
		}
	}
	return s
}
