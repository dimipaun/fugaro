package credsvc

// The cascade (C1.11): which credential a call goes to, what an answer means,
// and what the run is told when nothing can serve it.
//
// Nothing here decides anything creds does not: the marks, the leases, the
// stickiness, the wait-or-switch rule and the tier order are internal/creds'
// (C1.9). This file builds the candidate list the policy admits, hands it to
// creds.Select, and turns one answer into a mark or into the client's answer.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// candidates narrows the bundle to the credentials this call may use, and
// reports the ones it did not, with why.
//
// A tier-B credential needs no admission: it is the project's own paid key,
// and the policy that admits spending at all (`fallback: auto`) is the same
// policy that admits it. A tier-A credential needs O4's three-legged
// admission, and that is why the admission is read only when the bundle holds
// one: a repository with no shared token is never given tier A, and an
// installation whose bundle holds no tier-A credential never consults an
// admission at all (Global Constraint 4's drop, if it had applied).
func (s *Server) candidates(ctx context.Context, slug, canonical string, b *Bundle) ([]creds.Credential, []Tried, error) {
	// b.Credentials() and never b.Entries(): the bundle holds the GitHub App
	// key as well (C2.1), and it is not a candidate for anything. A cascade
	// that offered it would attach an App private key to an Anthropic call as
	// the caller's credential, so the filter is the accessor's rather than
	// this loop's.
	all := b.Credentials()
	hasA := false
	for _, c := range all {
		if c.Tier == creds.TierA {
			hasA = true
			break
		}
	}
	admitted := map[string]bool{}
	if hasA {
		var err error
		admitted, err = s.admittedAccounts(ctx, slug, canonical)
		if err != nil {
			return nil, nil, err
		}
	}
	out := make([]creds.Credential, 0, len(all))
	var skipped []Tried
	for _, c := range all {
		if c.Tier == creds.TierA && !admitted[c.Name] {
			skipped = append(skipped, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonNotAdmitted})
			continue
		}
		out = append(out, c)
	}
	return out, skipped, nil
}

// admittedAccounts is O4's admission, and it is an intersection of three
// published facts rather than a read of one (the review of C1.2, m-2 and
// m-3):
//
//   - the repository's own node names the accounts an administrator admitted
//     it to (C1.2). It is what was written once, by hand, and it can be wider
//     than the policy — two concurrent publishes are a multi-path patch with
//     no compare-and-set, and a node can be edited by hand;
//   - the shared node's accounts.<name>.allow_repos says the account itself is
//     offered to this repository;
//   - the shared node's allow_repos says the project admits the repository at
//     all. That is the leg that closes the `project repos rm` gap, which
//     removes a repository from the allow-list and does not touch its child.
//
// An absent child is an empty admission, never an error: a repository whose
// administrator has admitted it to nothing is admitted to nothing. An
// unreadable child or node is an error the caller refuses on — a reader that
// turned one into an empty set would hand tier A to nobody, which is the same
// answer by luck and the wrong one by construction (fail closed is a refusal,
// not a silent nothing).
func (s *Server) admittedAccounts(ctx context.Context, slug, canonical string) (map[string]bool, error) {
	child, found, err := s.cfg.Policy.Admissions(ctx, slug)
	if err != nil {
		return nil, err
	}
	if !found || child == nil || child.Credentials == nil {
		return map[string]bool{}, nil
	}
	shared, found, err := s.cfg.Policy.Shared(ctx)
	if err != nil {
		return nil, err
	}
	if !found || shared == nil {
		return map[string]bool{}, nil
	}
	if !projectpolicy.AdmitRepo(shared.AllowRepos, canonical) {
		return map[string]bool{}, nil
	}
	out := make(map[string]bool, len(child.Credentials.Accounts))
	for _, name := range child.Credentials.Accounts {
		acct, ok := shared.Accounts[name]
		if !ok {
			// An account the child names and the node does not define. The
			// intersection is empty for it, which is what an intersection
			// means; the publish path refuses to write such a child, and a
			// hand-edit that wrote one is admitted to nothing.
			continue
		}
		if projectpolicy.AdmitRepo(acct.AllowRepos, canonical) {
			out[name] = true
		}
	}
	return out, nil
}

// settingsOf is the published policy as creds.Select enforces it. Every
// member is read through a nil-safe accessor of the policy's own — including
// D142's two controls, which are pointers and are read through
// StopsAfterExhaustion and ConcurrencyCap rather than off the block — so an
// installation that publishes no credentials block reads the protective
// defaults (oauth-only, the 15-minute threshold, both controls at their
// protective values) rather than a zero no comparison matches, and a policy
// whose block is absent does not reach a nil *Credentials at all. That is the
// case Decode leaves for a document with no `credentials` key, and it is every
// call's case: an absent block is a policy, not a malformed one.
//
// The two controls are handed to creds as pointers to the values the accessors
// answer, which is what creds' own members mean: nil is the absent key and
// reads as the same default (stops, concurrency) the accessor answers, so
// spelling it out cannot change which control is in force — and it keeps the
// default in one place, the policy's, rather than in a second one here.
//
// Window and LeaseTTL are left zero: both are creds' own bounds (the
// subscription window D142's first control measures, and the lease TTL), they
// are not policy fields, and Settings.withDefaults fills them.
func settingsOf(p *projectpolicy.Policy) creds.Settings {
	var c *projectpolicy.Credentials
	if p != nil {
		c = p.Credentials
	}
	fallback := creds.OAuthOnly
	if c.FallbackValue() == projectpolicy.FallbackAuto {
		fallback = creds.Auto
	}
	stops := c.StopsAfterExhaustion()
	concurrency := c.ConcurrencyCap()
	return creds.Settings{
		Fallback:                     fallback,
		WaitThreshold:                c.WaitThresholdDuration(),
		StopAfterExhaustionPerWindow: &stops,
		ConcurrencyPerToken:          &concurrency,
		BackoffInitial:               c.BackoffInitialDuration(),
		BackoffCap:                   c.BackoffCapDuration(),
	}
}

// wireTier is a tier as the wire spells it. It answers "" for a tier this
// release does not select, which every writer of the contract refuses — so a
// tier the cascade invented would be a refusal and not a header.
func wireTier(t creds.Tier) string {
	switch t {
	case creds.TierA:
		return TierA
	case creds.TierB:
		return TierB
	case creds.TierC:
		return TierC
	}
	return ""
}

// unsent is the candidates this call has not already sent to. The mark a
// usage limit writes does not always hold its credential — a reset already in
// the past, on a tier whose D142 window is off, leaves a mark that has
// expired — so the retry loop tracks what it sent rather than trusting the
// state to refuse it. Without this, one call could send to the same credential
// again and again.
func unsent(cands []creds.Credential, sent map[string]bool) []creds.Credential {
	out := make([]creds.Credential, 0, len(cands))
	for _, c := range cands {
		if !sent[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// auditCall writes this service's one line for one call (C1.13): the issuance
// a credential answered, or the refusal this service answered in its place. The
// event is completed from the identity the service verified (Identity.Audit —
// the run, the repository slug, the job the run reported and the account that
// called, and none of them from anything a caller typed) and carries the
// credential's NAME and the tier it is of, never a value: an AuditEvent has no
// field a value could be written into (audit.go).
//
// It is written through the auditor the SERVICE built — NewServer's
// NewProbeAuditor over the bundle loader, the one the probe writes through too
// — and not through a configured one, for exactly the reason C1.13 gives: the
// loader is the one place this service reads a credential, so it is the one
// place that can answer what a value is, and it arms this line's redaction.
// The arming is therefore a fact of the service's construction and not
// something a caller can forget (WithAuditScrub, NewAuditor's refusal).
//
// purpose is what the call asked for and not what the helper writes: the two
// routes this service serves are two acts — a proxied model call (C1.11) and a
// mint (C2.1) — and a refusal taken before the route could be served (the
// operator's stop switch, a stop switch that could not be read) is still a
// refusal of one of them. A line that said "forward" for a mint would be the
// audit telling an operator that a model call was refused when the run asked
// for an installation token.
//
// status is the wire's own vocabulary: StatusOK for an issuance, and one of
// the contract's error types for a refusal. The credential and the tier are
// passed together or not at all — an issuance and a refusal that names the
// credential it would have used both carry them, a refusal taken before one
// was chosen carries neither (the auditor refuses a line that names one
// without the other: a reader that guessed the missing half would be reading
// an answer this service did not give).
func (s *Server) auditCall(ctx context.Context, id Identity, purpose, credential, tier, status string) error {
	return s.audit.Write(ctx, id.Audit(AuditEvent{
		Credential: credential,
		Tier:       tier,
		Purpose:    purpose,
		Status:     status,
	}))
}

// refuseCall answers one refusal and records it: one typed refusal to the run,
// one line to the operator, and the run's answer is the same whether or not the
// line lands — an act this service will not record is not made, and a call it
// refuses is not made either way (D143, the rule refuseMint already keeps for
// the refusal half of the mint). A line that could not be written is the
// service's own log line, which is where an operator reads it.
//
// The audit status is the refusal's own wire error type, so the run's halt
// message and the operator's line read the same word for the same refusal.
func (s *Server) refuseCall(ctx context.Context, w http.ResponseWriter, id Identity, purpose, credential, tier string, status int, typ, message string) {
	if err := s.auditCall(ctx, id, purpose, credential, tier, typ); err != nil {
		s.log.Error("credsvc: a refused call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// forward answers the call: it selects, sends, and acts on what came back.
//
// The retry is the whole of the loop and it has exactly two rules. It happens
// only for an answer that says the credential could not serve the call — a
// usage limit (429) or a 401 — and it happens only once per credential and
// only while nothing has been streamed to the client. Both rules are why this
// is a loop over a `sent` set rather than a retry inside `send`: the moment
// the answer is the client's, the call is over.
func (s *Server) forward(ctx context.Context, w http.ResponseWriter, cl *call, sel *creds.Selector, id Identity, b *Bundle, cands []creds.Credential, notAdmitted []Tried) {
	tried := append([]Tried(nil), notAdmitted...)
	sent := make(map[string]bool, len(cands))
	// probed is what THIS call has already probed (C1.12): a name in it is
	// never probed twice by the same call, which is what bounds the loop below
	// when a probe leaves a credential the next selection still wants probed.
	probed := make(map[string]bool, len(cands))
	started, reason := "", ""
	// The run is the identity's: the slug and the run id the service verified
	// (C1.10) are what the cascade's nodes are keyed by, and taking them from
	// anywhere else would be naming a run the service did not verify.
	run := creds.Run{Slug: id.Slug, ID: id.Run}

	pool := cands
	for {
		c, _, err := sel.Select(ctx, run, pool)
		if err != nil {
			// A probe being owed on the earliest candidate is the one answer
			// this loop acts on rather than relays (C1.12): the credential is
			// backed off with its wait passed, so it is asked whether it works
			// and the selection is made again. Anything else — a real wait, a
			// broken mark, an unreadable state — is the run's answer and is
			// refused below.
			if s.probeDue(ctx, sel, id, cl, b, pool, probed, err) {
				continue
			}
			s.refuseSelection(ctx, w, id, err, tried)
			return
		}
		if sent[c.Name] {
			// Select handed back a credential this call has already sent to,
			// which means its mark does not hold it (a usage limit whose reset
			// had already passed, on a tier whose window is off). Sending
			// again is the one thing this loop must not do, so the call is
			// refused rather than retried.
			//
			// It is the one refusal here that names a credential the call has
			// already spent: the line carries it, because the audit exists so
			// that an operator can read what the service did with which
			// credential — and what it did with this one is send to it twice.
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"no credential is usable now and none of the ones this call used may be tried again")
			return
		}
		e, ok := b.Lookup(c.Name)
		if !ok {
			// Unreachable: the candidates came from this bundle. A refusal
			// rather than a panic, because the alternative is a service that
			// crashes on a state it cannot name.
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"the selected credential is not in the bundle")
			return
		}
		if started == "" {
			started = c.Name
		}
		ans, err := s.send(ctx, cl, e)
		if err != nil {
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable, err.Error())
			return
		}

		switch status := ans.resp.StatusCode; {
		case unauthorizedStatus(status):
			// D139: a 401 marks the credential broken, and only an operator
			// clears that (C1.21). The upstream's own answer is not forwarded:
			// it is about a credential the run does not hold, and a run shown
			// an authentication error it cannot act on is a run that stops for
			// the wrong reason.
			ans.resp.Body.Close()
			if _, err := sel.Broken(ctx, c.Name); err != nil {
				s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable, markRefusal)
				return
			}
			tried = append(tried, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonBroken})
			sent[c.Name], reason = true, ReasonBroken
			pool = retryPool(cands, sent)
			continue

		case usageLimitStatus(status):
			// A usage limit. Nothing has been streamed, so the call may be
			// retried — on another credential, never on this one.
			reset := usageLimitReset(ans.resp.Header, s.now())
			ans.resp.Body.Close()
			if _, err := sel.Exhausted(ctx, c.Name, c.Tier, reset); err != nil {
				s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable, markRefusal)
				return
			}
			tried = append(tried, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonExhausted})
			sent[c.Name], reason = true, ReasonExhausted
			pool = retryPool(cands, sent)
			continue
		}

		// Everything else is the client's answer, exactly as it came: a 2xx, a
		// 400 for a request Anthropic refused, an overloaded_error, a 500. The
		// service retries none of them — a retry is for a credential that
		// cannot serve the call, and none of these says that.
		var sw *Switch
		if len(sent) > 0 && c.Name != started {
			sw = &Switch{From: started, To: c.Name, Reason: reason}
		}
		scan := &usageLimitScanner{}
		s.answer(ctx, w, id, cl, ans, c, sw, scan)
		if scan.Found() {
			// A usage limit inside a 200 stream: the bytes are the client's
			// already, so this call is not retried and the credential is
			// marked with no reset — C1.9's backoff, which is the shape U3
			// leaves open until a subscription usage limit is observed.
			//
			// The context is stripped of its cancellation: the exhaustion is a
			// fact about the credential whether or not the client stayed to
			// read the answer, and a client that hung up must not be the
			// reason the next call walks into the same limit.
			if _, err := sel.Exhausted(context.WithoutCancel(ctx), c.Name, c.Tier, time.Time{}); err != nil {
				s.log.Warn("credsvc: a usage limit seen inside a stream could not be recorded", "error", err)
			}
		}
		return
	}
}

// retryPool is what a retry selects from: the candidates this call has not
// sent to, or the whole set when there are none left.
//
// The whole set, because the answer then is not "try one" but "tell the run
// what the state says" — Select over the whole set reads the marks this call
// just wrote and answers the earliest reset (or that nothing is usable at
// all). A credential it hands back is one this call has already sent to, and
// the loop refuses on that rather than sending again.
func retryPool(cands []creds.Credential, sent map[string]bool) []creds.Credential {
	if left := unsent(cands, sent); len(left) > 0 {
		return left
	}
	return cands
}

// answerUnaudited is what a call whose issuance line could not be written is
// refused with. It is this file's own sentence and carries no value: nothing of
// the upstream's answer reaches the run, because the answer is what fail-closed
// withholds (D143).
const answerUnaudited = "the call could not be audited, so the credential's answer was not given"

// markRefusal is what a mark that could not be written means. It is a refusal
// and not a detail to log past: the next call's selection is made from the
// state, so a credential this call found unusable and could not record is a
// credential the next call is sent to again.
const markRefusal = "the credential's state could not be recorded"

// answer writes the upstream's answer to the client, with the headers of this
// contract on it: the tier, the credential's name, and the switch when this
// call moved.
//
// It is also where the call is recorded (C1.13): the credential answered, so
// this is the issuance, and the line is written before a byte of the answer is
// — an issuance this service could not audit is not made (D143), which here
// means the run is refused rather than handed an answer no line accounts for.
// The credential was spent by then (the upstream call is out of this service's
// hands), so what fail-closed withholds is its effect: no byte of the answer
// reaches the run and the run halts on the refusal.
//
// The headers are built and validated BEFORE the line is written, so a call
// that cannot carry them is a refusal with no issuance line beside it: a line
// saying a credential answered a run this service then refused would be a line
// an operator could not act on, and the refusal is the one act that happened.
//
// The headers are built and validated before any of them is written, so an
// answer that cannot carry them is a typed refusal with no upstream header
// leaking onto it.
//
// The body is scrubbed of the credential as the headers are, and for every
// tier rather than the subscription one alone: an upstream that echoes the
// credential it was handed — in a 200 body, in a 400's error text, in a
// request id — must not have it echoed on to the run, and every credential
// this service attaches is a secret value (tier B's paid key as much as tier
// A's token). gateway.NewRedactWriter is the gateway's own no-echo rule over a
// body, and it is used here rather than re-implemented for the reason C1.8
// exists: the two forwarders must not drift. It also holds back a tail that
// could still become the credential, so a credential split across two reads is
// redacted too, and its Close writes that tail once the stream ends.
func (s *Server) answer(ctx context.Context, w http.ResponseWriter, id Identity, cl *call, ans *answer, c creds.Credential, sw *Switch, scan *usageLimitScanner) {
	defer ans.resp.Body.Close()
	hdr := http.Header{}
	copyAnswerHeaders(hdr, ans.resp.Header, ans.credential)
	if err := SetCredentialHeaders(hdr, wireTier(c.Tier), c.Name); err != nil {
		s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
			"the answer could not carry the credential that served it")
		return
	}
	if sw != nil {
		if err := SetSwitchHeader(hdr, *sw); err != nil {
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"the answer could not carry the switch that served it")
			return
		}
	}
	if len(cl.probed) > 0 {
		// The probes this call made before it was served (C1.12): the run
		// accounts for them by the tier each was made on (C1.16), and the
		// answer is the only place it can learn them. An answer that cannot
		// carry them is refused rather than sent without them — a model
		// answer that hid the probes it was served after is a run paying for
		// calls its ledger never saw.
		if err := SetProbeHeader(hdr, cl.probed); err != nil {
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"the answer could not carry the probes this call made")
			return
		}
	}
	// The issuance's own line (C1.13), written here and not after the answer:
	// an issuance this service cannot audit is not made, and the credential is
	// spent by now — what is withheld is the answer.
	if err := s.auditCall(ctx, id, PurposeForward, c.Name, wireTier(c.Tier), StatusOK); err != nil {
		s.log.Error("credsvc: the issuance's audit line was not written", "error", err)
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, answerUnaudited)
		return
	}
	for k, vs := range hdr {
		w.Header()[k] = vs
	}
	w.WriteHeader(ans.resp.StatusCode)
	tee := func([]byte) {}
	if scan != nil && answerIsStream(ans.resp, cl.requested) {
		tee = scan.scan
	}
	// The credential is scrubbed out of the body on its way to the client, and
	// so is every token the caller presented: the run identity this service
	// verified and the job's ID token. An upstream that echoed one of them —
	// in a message's text, in an error, in a request id — must not have it
	// echoed on to the run, and the two tokens are as much this service's to
	// keep out of an answer as the credential is.
	//
	// The writers chain, one per value, and each holds back a tail that could
	// still become its value, so a value split across two reads is redacted
	// too. They wrap the client's own ResponseWriter, and the
	// ResponseController stays on that ResponseWriter rather than on the
	// outermost writer, so flushing is still the flush of the client's
	// connection (C1.8's pump flushes after every write, and a stream must not
	// be delayed by the redaction).
	out := io.Writer(w)
	var rw []io.WriteCloser
	for _, key := range append([]string{ans.credential}, cl.scrubs...) {
		if key == "" {
			continue
		}
		r := gateway.NewRedactWriter(out, key)
		rw = append(rw, r)
		out = r
	}
	// Pump never buffers and flushes after every write, which is what makes
	// the answer reach the run as it arrives (C1.8). Its bool says the stream
	// ended rather than failed; nothing here acts on it, because the client's
	// connection is the client's.
	gateway.Pump(out, http.NewResponseController(w), ans.resp.Body, tee)
	// Innermost last: a writer's tail is not its value after all, so it is the
	// tail of the answer and it is written — into the writer below it, which
	// then has the same chance to find a value in what it is handed.
	for i := len(rw) - 1; i >= 0; i-- {
		_ = rw[i].Close()
	}
}

// waitFloor is the shortest wait this service answers a run with, and it is a
// floor rather than a value: a wait the selection answered with is relayed as
// it stands whenever it is really in the future, and an instant that has
// already passed is replaced by now plus this.
//
// An instant in the past is not a wait at all. The runner acts on the typed
// answer by resuming at reset_at (C1.17), so a reset_at behind the run is an
// instruction to resume at once, and a run that resumes at once against a state
// that has not moved is a stage re-run in a tight loop — burning a run's own
// budget on a wait that was never going to end.
//
// Two states reach it, and both are ordinary. A probe owed on a backed-off
// credential is due exactly BECAUSE the credential's own wait has passed, so the
// instant the selection answers with (At) is behind the run by construction: the
// probe is what would move it, and a probe that was not made — the slot is
// another instance's, the lease cannot be read, the claim keeps losing its
// compare-and-set, the run's body names no model — leaves nothing but that
// instant to answer with. And a usage limit whose reset had already passed when
// the answer was read writes a mark that holds nothing (C1.9), so the next
// selection answers with a past instant the same way.
//
// Fifteen seconds is a value, not a measurement: a probe takes seconds (a
// one-token call), and the wait has to be long enough that a runner resuming
// under it does not become the loop this exists to prevent. It is nowhere near
// the policy's own threshold, so no wait this service invents can be mistaken
// for the credential's own reset.
const waitFloor = 15 * time.Second

// refuseSelection turns creds.Select's answer into the client's, and records
// it (C1.13): one line per refused call, with the refusal's own error type as
// its status and no credential named, because no single credential was chosen —
// what was considered, and why each one was skipped, is the run's own answer
// (the tried list) and not this line's.
//
// The typed answer is the one case the runner acts on rather than halts for:
// nothing is usable now and something becomes usable at an instant, so the run
// waits (C1.17). Everything else is a refusal it halts on (D143) — the state
// could not be read, every candidate is broken or held with no time to wait
// for, or the only credentials that could serve the call are of a tier this
// policy does not admit (D152, which only a written `fallback: auto` changes).
//
// The tried list is carried on every refusal this path writes, so the run's
// halt message names what was considered and why each one was skipped. The
// typed wait carries it structurally (ExhaustedUntil), and a refusal that
// halts carries it in the same field of the same body (WriteWireError), so a
// run reading either finds the same list in the same place.
func (s *Server) refuseSelection(ctx context.Context, w http.ResponseWriter, id Identity, err error, tried []Tried) {
	var until *creds.ExhaustedUntil
	if errors.As(err, &until) {
		now := s.now()
		reset := until.At
		if !reset.After(now) {
			// The instant has passed, which is a state the run cannot act on
			// (waitFloor). It is answered as a short wait ahead of the run
			// instead, so the next stage it runs has something to wait for and
			// the loop is bounded by the floor rather than by the state.
			reset = now.Add(waitFloor)
		}
		if aerr := s.auditCall(ctx, id, PurposeForward, "", "", ErrorExhaustedUntil); aerr != nil {
			s.log.Error("credsvc: a refused call could not be audited", "error", aerr)
		}
		WriteExhaustedUntil(w, ExhaustedUntil{ResetAt: reset, Tried: tried}, now)
		return
	}
	if aerr := s.auditCall(ctx, id, PurposeForward, "", "", ErrorStateUnavailable); aerr != nil {
		s.log.Error("credsvc: a refused call could not be audited", "error", aerr)
	}
	WriteWireError(w, StatusUnavailable, WireError{
		Error:   ErrorStateUnavailable,
		Message: err.Error(),
		Tried:   tried,
	})
}
