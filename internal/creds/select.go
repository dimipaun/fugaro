package creds

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// Tier is a credential's cost class: A is a shared subscription OAuth token
// (someone's donated quota, free to spend and scarce), B is the project's
// Anthropic API key (paid, bounded by the caps) and C is a named provider key
// (paid, at a third party: the route D3000 gates for a Claude model). The
// cascade walks them in that order — cheapest usable first — and never the
// other way round.
type Tier string

const (
	TierA Tier = "a"
	TierB Tier = "b"
	// TierC is the third-party route (an OpenRouter key): a provider's own key,
	// held by the credentials service, that serves a model the provider runs.
	// It is selected only for a model that is not a Claude one, and that is the
	// whole of D3000: a CLAUDE model through a provider key waits for Check 50,
	// which measures Claude's prompt-cache markers through that route (D150,
	// C3.6, R3), while a non-Claude provider model is served without waiting —
	// and, the same fact read backwards, by this tier alone: a model that is not
	// Claude's cannot be run by a subscription token or the project's key, so
	// those candidates are dropped for it (checkCandidates, D3007).
	// Which models are Claude's is config.IsClaudeModel — the pin vocabulary
	// every reader already shares (R3), never a list of this package's.
	//
	// Since AK6b a bundle really can hold such a credential — the project's
	// provider key is in the document (F15, D1418), and KS1 adds one entry per
	// repository and provider beside it (R2, D3008) — so what keeps the Claude
	// case unserved is checkCandidates' refusal below and not the absence of a
	// credential, which is why a call whose only credential is one is answered
	// a typed refusal naming Check 50 rather than served by another tier.
	TierC Tier = "c"
)

// Credential is one named credential a call may be routed to. It carries the
// name, never a value: every name here is read, logged (C1.13) and shown in
// `fugaro accounts ls` (C1.21), and F27's rule is that only the name travels.
type Credential struct {
	Name string
	Tier Tier
	// Provider is the provider whose key this credential is, for a tier-C one,
	// named as the published policy's providers block names it (R2) — one
	// provider's key is the key of ONE provider, and a caller's list can hold
	// several of them (the offering offers one per provider the policy names).
	// It is what ties a key to the models it may serve beside the class rule
	// (Selector.Provider, checkCandidates): a key handed to another provider's
	// model is a bearer token sent to an endpoint it was not issued for.
	//
	// The tiers below carry nothing here: a shared subscription token and the
	// project's own Anthropic key belong to no provider and run a Claude model
	// alone.
	Provider string
}

// Run is the run a selection is made for: the repository slug and the run id
// the service verified from the run identity (C1.10). They are the run's nodes
// under creds/, so stickiness and the lease are per run without either of them
// being a name a caller invents.
type Run struct {
	Slug string
	ID   string
}

// Strategy is how a caller wants a tier's usable members shared out among the
// runs that arrive: the cascade's own rule (StrategyFailover, and the zero
// value), or a rotation through them (StrategyRoundRobin).
//
// It is the CALLER's word and not this package's policy. Which strategy a pool
// is served under is the owner's, written in the published node and read where
// the credentials come from (the credentials service maps it,
// internal/credsvc's roundrobin.go); a selection is handed the answer, because
// this package knows nothing of pools, crafts or projects.
//
// The two rules are the spec's, and their names are the ones the pool format
// spells (config.PoolStrategyFailover, .PoolStrategyRoundRobin), so a strategy
// written in a file and a strategy a selection is handed are one vocabulary.
type Strategy string

const (
	// StrategyFailover is the cascade's own within-tier rule: the credential
	// exhausted longest ago, with the round-robin counter breaking an exact tie
	// (leastRecentlyExhausted). It is the zero value, so a caller built without
	// a strategy selects exactly as it did before a strategy existed.
	StrategyFailover Strategy = "failover"
	// StrategyRoundRobin steps through the usable members of a tier in the
	// order the caller offered them, one run at a time, and skips the ones a
	// mark or the cap holds (rotate).
	StrategyRoundRobin Strategy = "round-robin"
)

// Selector selects the credential a run's next call goes to. It holds no state
// of its own: everything it decides is read from the Store and written back,
// because the service is stateless and several instances select at once.
type Selector struct {
	// Store is the cascade's shared state. It must not be nil.
	//
	// It is CascadeState and not Store deliberately: a selection reads marks,
	// leases, placements, the tie-breaker and nothing else, and the
	// per-repository auth record — which the ADMISSION around this selection
	// does read — is not the selection's state but a repository's own
	// configuration, read where that repository's own update wrote it (D2070
	// (a)). A selection that could reach RepoAuth through this handle would be
	// a second way to the record, and in a two-deployment world a way to the
	// wrong database.
	Store CascadeState
	// Settings are the policy's settings as this package enforces them.
	Settings Settings
	// Now is the clock. nil is time.Now.
	Now func() time.Time
	// Strategy is how this caller's candidates are shared out within a tier
	// (Strategy). It never outranks the tier: the strategy decides which member
	// of a tier a run goes to and never which tier (F7).
	Strategy Strategy
	// Model is the model this call is for, as the request names it, and it is
	// what tells a Claude model from any other: a tier-C candidate — a named
	// provider key — is served for any model that is not a Claude one, and a
	// Claude model through one waits for Check 50 (D3000, R3, TierC). It is the
	// same fact read the other way for the tiers below it: a model that is not
	// Claude's is served by a provider's key alone, and the Anthropic tiers a
	// provider's model cannot run are dropped from the list (D3007).
	//
	// It is the CALLER's answer and not this package's: a selection knows no
	// model ids and reads no request, so the side that parsed the call hands
	// the model it names (the credentials service's provider route and its
	// cascade route, KS4 and KS5), and "" means a caller that named none —
	// which reads as a Claude model, the fail-closed direction this gate has
	// always had. A caller that never sets it therefore selects exactly what it
	// selected before this field existed.
	Model string
	// Provider is the provider that runs this call's model — the name the
	// published policy's providers block gives it (R2) — and it is what a
	// tier-C candidate is tied to: a caller's list can hold several providers'
	// keys (one per provider the policy names), so the key of a provider this
	// call did not name is dropped (checkCandidates), because one provider's
	// key served for another's model is a bearer token sent to an endpoint it
	// was not issued for, on a route that cannot run the model at all.
	//
	// Which provider runs a model is a fact of the project's published policy
	// (config.ModelProvider.Matches over its `models` patterns), and this
	// package cannot read it: the side that resolved the call hands the answer
	// in, exactly as it hands in the model (the credentials service's provider
	// route, KS4 — a provider is named there, and the name IS the provider).
	//
	// "" means a caller that named none, and then NO tier-C candidate is served
	// at all. That is the fail-closed direction, and it is not the same as the
	// class rule: a list holding one provider's key is no evidence that that
	// provider runs this call's model — a model whose provider's key the bundle
	// does not hold would otherwise be sent to whichever one was left.
	Provider string
	// Exclusive names the candidate this call's own right serves OUTRIGHT while
	// it can serve: the named credential is selected and no other member of the
	// list is. It is the CALLER's word, per call, exactly as Strategy is — and
	// it is per call rather than a mark on a Credential because whether a token
	// is served outright is a fact about the call's entitlement and not about
	// the token: the same credential is one more member of the list in another
	// call, which has no slip naming it (internal/credsvc marks the person's own
	// token here, which the slip their run carries entitles it to, R7, D3144).
	//
	// It exists because the offered order cannot say this. Order breaks an exact
	// tie and says where a rotation starts (leastRecentlyExhausted, rotate), so
	// a list that offered such a candidate as one more member of its tier would
	// serve it to about one call in N — the tie-breaking counter's turn, or the
	// rotation's — while the whole content of the entitlement is that the calls
	// it names go out on that credential. The moment the candidate cannot serve
	// — exhausted, backed off, capped or broken — the rule stops holding it up
	// and the rest of the list is the answer, because "can serve now" is the
	// judgement every other candidate meets and not a rule of its own (free).
	//
	// "" means a caller that named none, and then no candidate is served
	// outright: a caller that never sets it selects exactly what it selected
	// before this field existed.
	Exclusive string
}

// Select answers the credential the run's next call goes to, or the wait until
// one is usable.
//
// The answer is one of:
//
//   - (credential, 0, nil) — send the call there. The run's current credential
//     and its lease have been written, so the next call is sticky on the same
//     one (D154) and the credential's slot is held for the lease's TTL.
//   - ("", wait, *ExhaustedUntil) — nothing can be used now and something
//     becomes usable at `wait`: answer the typed refusal (C1.10) and let the
//     runner wait (C1.17), or, when Probe is set, probe first (C1.12).
//   - ("", 0, ErrNoCredential) — nothing can be used and nothing becomes usable:
//     every admitted candidate is broken (clearing one is the operator's,
//     C1.21), holds a mark with no time to wait for, or is refused by a
//     concurrency cap that no lapse frees. The caller refuses; it never falls
//     back to something else (D143).
//   - ("", 0, ErrFallbackForbidden) — the credentials that can serve the call —
//     now, or after the wait a mark names — are all of a tier this policy does
//     not admit (tier B under `oauth-only`, D152). A refusal, and a different
//     one from the above, because the fix is a policy and not a token. A
//     credential of a refused tier that could not serve the call anyway (a
//     broken one, one whose mark carries no time) is not this error: it is
//     ErrNoCredential, because a policy change would not make the call succeed.
//   - ("", 0, the Check 50 refusal) — every candidate left is a tier-C one and
//     this call is a Claude model's (or names no model): a Claude model through
//     a provider key waits for Check 50 (D3000, D150). A refusal, and the one
//     sentence that says which check and which decision: the caller named a
//     provider credential and this release does not select that route for a
//     Claude model yet.
//   - ("", 0, the model's own refusal) — every candidate left is of a tier this
//     call's model cannot be run on, the other way round: the two Anthropic
//     tiers offered for a model that is not Claude's (D3000, D3007). A refusal,
//     because the alternative is a provider's model sent to Anthropic on quota
//     that route cannot spend, and because a list of the wrong class is a
//     caller's mistake rather than an account's exhausted state.
//   - ("", 0, the provider's own refusal) — every candidate left is a named
//     provider's key and none of them belongs to the provider this call named
//     as the one that runs its model (or the call named none at all): one
//     provider's key never serves another's model, and the sentence names the
//     two providers it holds apart (D3000).
//   - another error — the state could not be read or written. Fail closed: an
//     unreadable state is not an empty one.
//
// candidates is the caller's list, and Select never widens it: exactly the
// credentials the project policy admits for this call, in the tier order
// (tiers: A, then B where the policy spends, then C). Nothing here reads an
// admission (that is C1.11's, from what C1.2 publishes) and nothing here
// invents a credential the caller did not offer. The one candidate that is
// served ahead of the tiers is the caller's own exclusive one, and only while
// it can serve (Selector.Exclusive); every other candidate is answered by the
// rule below. The tier decides which credential is taken and the order within
// a tier does not —
// except in a tie, which the offered order breaks, so a pool is listed the way
// its owner ordered it and the listed order never outranks the tier (F7). What
// picks among the usable members of one tier is the caller's Strategy — the
// rule above, or a rotation through them in that same order — and the tier is
// decided before it, so neither rule can move a call off the cheaper one. A
// candidate of a tier this release does not know is refused rather than
// skipped, and so is a list left with nothing but tier-C candidates this call's
// model may not be served by; a tier-C candidate that is merely one of several
// is dropped from the list instead, because the call would never have been
// served by it (checkCandidates, D3000). The tiers below it are dropped the
// other way round for a call whose model is not Claude's: a shared token or the
// project's key cannot run such a model either, and a list left with nothing
// but those is the model's own refusal (D3007). A tier B candidate offered under
// `oauth-only` is not refused — it carries no mistake — but it is never
// selected, and a call that nothing else can serve is answered with
// ErrFallbackForbidden when that candidate could actually serve it (see the
// list above: a broken one, or one held with no time to wait for, is
// ErrNoCredential).
//
// # What this does not re-check, and what that costs
//
// The marks and leases are read once, at the top, and the decision is taken from
// that reading (review finding m4). A mark written by another instance in
// between — the token this call is about to use answered 401 a moment ago — is
// therefore not seen here, and the call goes to a credential whose state has
// just changed.
//
// That is deliberate rather than overlooked, and its whole cost is one call: the
// credential is the one the reading said was usable, the call reaches the
// provider, and the provider's answer (a 401, a 429) is what records the new
// mark. Nothing durable is lost — the mark's write is a compare-and-set, so the
// two instances' marks do not overwrite each other — and nothing is served from
// a credential that the state will not correct a moment later. Reading the one
// mark again under a condition before returning would close the window at the
// price of a third round trip on every selection — the path taken by every call
// the project makes — to avoid one wasted call in a race that needs two
// instances to hit the same shared token in the same instant.
//
// The cap is the other read-then-write in this function, and its bound is
// Lease's: the cap can be exceeded by the number of instances selecting at once,
// for at most one lease TTL.
func (s *Selector) Select(ctx context.Context, run Run, candidates []Credential) (Credential, time.Duration, error) {
	return s.selectCredential(ctx, run, candidates, true)
}

// MayServe answers whether ONE credential may serve this run's call now, and it
// writes NOTHING: no placement, no lease, no mark, no counter.
//
// It is the judgement Select makes over a list of that one credential
// (selectCredential), and the one thing it leaves out is the recording of the
// decision. Every rule is therefore the same rule and every refusal the same
// error: the class rule and the provider tie (checkCandidates, D3000), the
// credential's own mark, the room the cap leaves, and the wait a mark that will
// lift answers with (ExhaustedUntil) — so a caller renders one sentence for one
// state, and no credential is ever judged twice by two spellings of a rule.
//
// # Why a caller wants the judgement without the placement
//
// A call whose credential is DERIVED has no decision to record. The credentials
// service's provider route builds its entry from the run's verified repository
// and the provider's name the call carries, and it is the same entry for every
// call that run makes to that provider: there is no pool for the run to be moved
// around, no other credential this call could have been served by, and so
// neither D154's stickiness nor D142's slot means anything for it.
//
// What placing it would do is the harm. A run's current credential and its lease
// are ONE fact — where this run is (SetPlacement, take) — and a run that mixes
// Claude calls with routed ones is on a shared subscription token for the first.
// The routed call would move the run onto the provider's entry, which takes the
// lease the run still holds on the account with it: the account's concurrency
// slot (D142) is freed while the run is using it, and the next Claude call finds
// no current credential to stay on (D154) and either re-selects off the prompt
// cache's credential or waits on the cap. A run's placement is the cascade's to
// write, and only for a call the cascade served.
//
// The judgement itself is not optional, and that is the other half: a caller
// that skipped it and attached the credential anyway would serve an entry a mark
// holds, or one the class rule and the provider tie refuse.
func (s *Selector) MayServe(ctx context.Context, run Run, c Credential) error {
	_, _, err := s.selectCredential(ctx, run, []Credential{c}, false)
	return err
}

// selectCredential is the selection Select and MayServe share, and `place` is
// the whole difference between them: Select records the decision (take), which
// is D154's stickiness and D142's lease written as one placement, and MayServe
// answers it and writes nothing.
func (s *Selector) selectCredential(ctx context.Context, run Run, candidates []Credential, place bool) (Credential, time.Duration, error) {
	set := s.Settings.withDefaults()
	now := s.now()
	if err := run.valid(); err != nil {
		return Credential{}, 0, err
	}
	cs, err := checkCandidates(candidates, s.Model, s.Provider)
	if err != nil {
		return Credential{}, 0, err
	}
	marks, err := s.Store.Marks(ctx)
	if err != nil {
		return Credential{}, 0, err
	}
	leases, err := s.Store.Leases(ctx, now)
	if err != nil {
		return Credential{}, 0, err
	}

	// The caller's exclusive candidate, before the placement and before any
	// tier: the credential the caller named here is SERVED while it can serve,
	// and the list's other members are not. Both halves of that are the whole
	// rule, and each is why the position is where it is (Selector.Exclusive).
	//
	// Before the placement, because D154's stickiness is how a pool is shared
	// among runs and this candidate is not a member of one: a token that a call
	// is entitled to is the token that call goes out on, and a run that was
	// moved onto a shared entry by an exhaustion of its own — the one state
	// that puts one there — does not stay there once its own token can serve
	// again. Before the tiers, because the within-tier rule is the other half
	// of the sharing (the counter's turn, a pool's rotation) and the offered
	// order says only where it starts.
	//
	// The one judgement it meets is the same one every other candidate meets
	// (free): a candidate that cannot serve now — exhausted, backed off, capped
	// or broken — is not held up, and the rest of the list is the answer, with
	// the placement, the tier loop and the wait-or-switch rule exactly what
	// they already were. A name the caller's list does not hold is no candidate
	// at all, which is the same rule read the other way and the reason a caller
	// whose list was narrowed by an admission keeps its ordinary selection.
	for _, c := range cs {
		if s.Exclusive == "" || c.Name != s.Exclusive || !reachable(c, set) || !free(c, marks, leases, run, now, set) {
			continue
		}
		if place {
			if err := s.take(ctx, run, c, now, set); err != nil {
				return Credential{}, 0, err
			}
		}
		return c, 0, nil
	}

	// D154's stickiness next, and before every rule below it: a run keeps the
	// credential it is on for as long as that credential is offered, usable and
	// not held by the cap — the reason is the prompt cache, and every rule below
	// this one would move the run off it for a reason the cache does not care
	// about.
	cur, found, err := s.Store.Current(ctx, run)
	if err != nil {
		return Credential{}, 0, err
	}
	if found {
		if c, ok := lookup(cs, cur); ok && reachable(c, set) && free(c, marks, leases, run, now, set) {
			if place {
				if err := s.take(ctx, run, c, now, set); err != nil {
					return Credential{}, 0, err
				}
			}
			return c, 0, nil
		}
	}

	// The cascade, cheapest tier first, and only the tiers this policy admits
	// (tiers): under oauth-only the paid tier is not in the cascade at all, so
	// no path below — not the cascade, not the fallback at the end — can return
	// a tier B credential, and a call that only the paid key could serve is
	// refused with ErrFallbackForbidden rather than served (D152).
	for _, tier := range set.tiers() {
		group := groupOf(cs, tier)
		if len(group) == 0 {
			continue
		}
		usable := make([]Credential, 0, len(group))
		for _, c := range group {
			if free(c, marks, leases, run, now, set) {
				usable = append(usable, c)
			}
		}
		if len(usable) > 0 {
			c, err := s.within(ctx, usable, marks)
			if err != nil {
				return Credential{}, 0, err
			}
			if place {
				if err := s.take(ctx, run, c, now, set); err != nil {
					return Credential{}, 0, err
				}
			}
			return c, 0, nil
		}
		// Nothing in this tier can be used now. Whether the run waits or moves
		// to the next tier is D152's wait-or-switch rule, and it is here because
		// this is where the wait is known: it waits while the wait is inside the
		// threshold and moves on beyond it. Under oauth-only there is no next
		// tier in this loop — and so nothing to switch to — which is the whole
		// of the threshold's being "ignored" under that policy: the wait is
		// returned whatever its length.
		//
		// A wait exactly equal to the threshold WAITS, and the comparison below
		// is the one place that says so. The two documents are not the same word
		// at the boundary: spec.md's rule is that a credential which "resets
		// within a threshold (default 15 minutes)" is waited for — within
		// including the bound — while C1.11 says the typed answer is for a wait
		// "under the threshold". The spec's reading is the one this code takes,
		// because the threshold is a bound on how long a run may idle and a wait
		// of exactly the threshold idles no longer than a run is allowed to, and
		// because the other reading makes the threshold's own value a wait the
		// run refuses to wait out. It is stated here, and pinned by
		// TestWaitOrSwitchAtTheThresholdExactly, so that a later reader does not
		// have to infer which word "under" was meant to be.
		at, probe, ok := earliest(group, marks, leases, run, now, set)
		if !ok {
			// No credential of this tier has a time it could serve work at:
			// there is no wait to answer with, and the next tier may still serve
			// the call.
			continue
		}
		if wait := waitUntil(now, at); wait <= set.WaitThreshold {
			return Credential{}, wait, &ExhaustedUntil{At: at, Wait: wait, Probe: probe}
		}
	}

	// Neither admitted tier could serve the call and neither answered with a
	// wait inside the threshold. A credential of an admitted tier is still
	// coming back somewhere — the run waits for the earliest one rather than
	// fail: D143's halt is for a service that cannot be reached, not for
	// accounts that are exhausted.
	// A wait among the credentials this policy admits is still a wait, whatever
	// the threshold said: the threshold decides whether to move on to another
	// tier, and there is no other tier left to move to.
	allowed := reachableOnly(cs, set)
	if at, probe, ok := earliest(allowed, marks, leases, run, now, set); ok {
		wait := waitUntil(now, at)
		return Credential{}, wait, &ExhaustedUntil{At: at, Wait: wait, Probe: probe}
	}
	if len(allowed) < len(cs) {
		// Nothing this policy admits can serve the call — every one of them is
		// broken, has a mark with no time, or there are none — and the caller
		// offered a tier it does not admit. When that credential could serve the
		// call — or would, if it were waited for — the run is refused with the
		// error that names the policy and the word that would change it (D152),
		// rather than with the one that says a token needs an operator: a
		// subscription token that has died is exactly when the two answers
		// differ.
		//
		// It is a question about the OTHER credentials, though, and it has to be
		// asked: a paid key that is broken, or held by a mark carrying no time,
		// cannot serve the call and no `fallback: auto` would make it. The error
		// that names the policy says "the credentials that could serve the call
		// are all of a tier this policy does not admit", and saying that of a
		// key that could not serve it sends the operator to a policy change that
		// changes nothing; ErrNoCredential is the honest answer, and it is the
		// one that names what actually stands in the way.
		if _, _, ok := earliest(cs, marks, leases, run, now, set); ok {
			return Credential{}, 0, ErrFallbackForbidden
		}
	}
	return Credential{}, 0, ErrNoCredential
}

// tiers is the cascade's order under this policy: tier A always, tier B only
// when the policy spends, and tier C — a named provider key — whichever the
// fallback says. It is the one place the paid tier is admitted, so "oauth-only
// never spends" is a property of this function rather than of a check below it
// — the earlier shape of this code gated only the wait-vs-switch decision and
// fell through to the paid key whenever every shared token was broken, which is
// exactly when the platform must refuse (D152).
//
// Tier C is not part of that decision, and the reason is what a tier-C
// candidate is (R3, D3000): it is in the list only because the call's own
// published policy names that provider credential for the calling repository —
// an entry the credentials service reads out of the bundle and offers, and one
// no policy that names no provider can produce (internal/credsvc's offering).
// So the owner has already decided to spend there, and `fallback` is about
// something else: it is the rule for moving a CLAUDE call off a shared
// subscription token onto the project's own paid ANTHROPIC key, and a
// non-Claude provider model was never on a subscription token to be moved off
// one. D3007 reads the same way from the owner: a repository's auth mode is
// there for the models and not the other way round, so a project whose Claude
// calls run on donated tokens (`oauth-only`, the protective default) can still
// have its DeepSeek calls served by its own OpenRouter key.
func (s Settings) tiers() []Tier {
	if s.Fallback.Spends() {
		return []Tier{TierA, TierB, TierC}
	}
	return []Tier{TierA, TierC}
}

// reachable reports whether a credential is of a tier this policy admits. It is
// what the sticky path is checked against as well: a run placed on the paid key
// while the policy was `auto` does not keep spending after the operator turns
// the policy back to `oauth-only` — its call waits for a shared credential, or
// is refused, and the run is moved when one is taken.
//
// Tier C is admitted the way tiers admits it and for the same reason: a run
// placed on a named provider key stays on it, and a call whose model is not a
// Claude one keeps being served, whatever the fallback says.
func reachable(c Credential, set Settings) bool {
	return c.Tier == TierA || c.Tier == TierC || set.Fallback.Spends()
}

// reachableOnly is the candidates this policy admits, in the order offered.
func reachableOnly(cs []Credential, set Settings) []Credential {
	if set.Fallback.Spends() {
		return cs
	}
	return append(groupOf(cs, TierA), groupOf(cs, TierC)...)
}

// Mark applies a state change to one credential's mark, under the store's
// compare-and-set: f is handed the mark as it is now and answers what it should
// become. It is how the service records an exhaustion, a failed probe, a 401
// and a clear, and it is the only way this package changes a mark — a caller
// never computes a mark from a value it read earlier, because that is how one
// call's mark would erase another's.
func (s *Selector) Mark(ctx context.Context, name string, f func(prev Mark, found bool) (Mark, error)) (Mark, error) {
	if name == "" {
		return Mark{}, fmt.Errorf("creds: a mark needs the credential's name")
	}
	if f == nil {
		return Mark{}, fmt.Errorf("creds: a mark of %q was asked for with no change to make", name)
	}
	return s.Store.UpdateMark(ctx, name, f)
}

// Record is Mark with the settings this selector enforces, for the two
// transitions that need them: a usage limit (which resets, or backs off) and a
// failed probe (which doubles). Both are D153's schedule, and both are here so
// that a caller does not have to hold the settings and the mark algebra apart.
func (s *Selector) Record(ctx context.Context, name string, f func(prev Mark, found bool, set Settings) (Mark, error)) (Mark, error) {
	// The same guard Mark has, and here rather than left to Mark: f is wrapped
	// below, so the closure Mark receives is never nil and Mark's own check
	// cannot fire — the nil call would happen inside the store's compare-and-set
	// callback instead, as a nil-pointer panic where Mark returns an error. The
	// two entry points are one mistake apart, so they answer it the same way.
	if f == nil {
		return Mark{}, fmt.Errorf("creds: a record of %q was asked for with no change to make", name)
	}
	set := s.Settings.withDefaults()
	return s.Mark(ctx, name, func(prev Mark, found bool) (Mark, error) { return f(prev, found, set) })
}

// Exhausted records a usage limit on one credential: the reset the response
// reported, or, with none, a backoff (see Exhaust). reset is the zero time when
// the response reported no reset.
//
// The tier is the caller's, for the same reason Probed's is, and here it decides
// what "a hold is already in force" means. Exhaust asks Mark.holdInForce, whose
// answer for an exhausted mark is Ready — a time measured with D142's window —
// so a raw Settings answers a tier B mark with the shared tier's five-hour
// window. That would make the writer and the reader disagree about the same
// mark: free and earliest read a tier B candidate through forTier, with the
// controls off, so they consider an exhaustion whose reset has passed usable and
// send calls to it, while this write would call every subsequent 429 an
// in-flight call and never back the credential off. The key would then be
// selected again and again, denied again and again, for the rest of a window
// nobody wrote for it — the retry storm a backoff exists to stop, on the tier
// that spends money. set.forTier(tier) is what keeps the two readers of the same
// mark in agreement.
func (s *Selector) Exhausted(ctx context.Context, name string, tier Tier, reset time.Time) (Mark, error) {
	now := s.now()
	return s.Record(ctx, name, func(prev Mark, _ bool, set Settings) (Mark, error) {
		return Exhaust(prev, now, reset, set.forTier(tier)), nil
	})
}

// ProbeFailed records a failed probe on one credential: the wait doubles, to
// its cap (C1.12).
//
// The tier is the caller's for the reason Exhausted's is: whether the mark the
// probe landed on is still held is answered with Ready, and Ready measures
// D142's window — a shared tier's — so a tier B mark must be read with
// set.forTier(tier) or a failed probe would be swallowed by a window the reader
// (free, earliest) does not apply to that credential either.
func (s *Selector) ProbeFailed(ctx context.Context, name string, tier Tier) (Mark, error) {
	now := s.now()
	return s.Record(ctx, name, func(prev Mark, _ bool, set Settings) (Mark, error) {
		return ProbeFailed(prev, now, set.forTier(tier)), nil
	})
}

// Probed records a successful probe of one credential (C1.12): the wait is
// over, and the credential is Cleared — unless D142's first control holds it for
// the rest of the window its exhaustion opened, which ProbeSucceeded decides.
//
// The tier is the caller's, because this is a write and the mark it writes does
// not carry one: C1.12's probe runs on a credential it read from the candidates
// it was given, and only the caller knows which tier that is. Tier B is the case
// that matters — its controls are off (forTier).
//
// The tier is TRUSTED, and getting it wrong is not harmless: passing TierB for a
// shared token turns D142's window off for it, so a successful probe inside the
// window returns Cleared and the token is back in rotation immediately — the
// exact thing the window control exists to prevent. There is no check here that
// the name is of the tier the caller claims, and there cannot be: a mark is a
// name and a state, and the tier of a name lives in the candidate list the
// caller holds. What a caller gets for passing the right one is that the probe
// reads the same window the reader (free, earliest) reads for that credential.
// (An earlier version of this comment claimed the opposite — that a wrong tier
// could only ever shorten a hold harmlessly — which the review was right to
// call out: it described behaviour the code does not have.)
func (s *Selector) Probed(ctx context.Context, name string, tier Tier) (Mark, error) {
	now := s.now()
	return s.Record(ctx, name, func(prev Mark, _ bool, set Settings) (Mark, error) {
		return ProbeSucceeded(prev, now, set.forTier(tier)), nil
	})
}

// Cleared records the operator's clear (C1.21): the transition that takes a
// broken or exhausted mark off, after the operator has replaced the token or
// judged the account fit to serve again. It is the operator's write and the only
// one that takes a broken mark off: a probe answers through Probed, which is a
// no-op on anything but a backed-off mark (ProbeSucceeded), so a probe's success
// cannot clear a 401.
func (s *Selector) Cleared(ctx context.Context, name string) (Mark, error) {
	return s.Mark(ctx, name, func(Mark, bool) (Mark, error) { return Cleared(), nil })
}

// Broken records a 401 on one credential (D139). It is the one mark this
// package will not undo on its own: only the operator's Clear (C1.21) takes it
// off — a probe never runs on a broken credential (C1.12), and ProbeSucceeded
// refuses one that arrives anyway.
func (s *Selector) Broken(ctx context.Context, name string) (Mark, error) {
	now := s.now()
	return s.Mark(ctx, name, func(Mark, bool) (Mark, error) { return Break(now), nil })
}

// take records a decision: the run's current credential, which is D154's
// stickiness, and its lease, which is the slot it holds under D142's cap.
//
// The two are one fact — where this run is — and they are written as one
// placement (Store.SetPlacement), atomically. They used to be two writes, which
// left a window in which a failure took one and not the other and the run's
// stickiness named a credential it held no slot on (or the reverse) until its
// next selection; there is no order of two writes that avoids that, which is
// why the fix is one write and not a different order.
//
// The error is returned rather than swallowed, so the caller fails closed: a
// call is never sent on a decision the service could not record.
func (s *Selector) take(ctx context.Context, run Run, c Credential, now time.Time, set Settings) error {
	return s.Store.SetPlacement(ctx, run, c.Name, Lease{Credential: c.Name, Until: now.Add(set.LeaseTTL)})
}

// within picks the one credential of a tier that serves this call out of the
// set a mark or the cap does not hold, by the rule the caller asked for. It is
// the only place a strategy is read, and it is read once per tier — the tier
// itself has already been decided by the loop above, which is what "a strategy
// never outranks the tier" means in code.
//
// The usable set is the same one both rules are handed, and that is the point:
// a member a mark holds is not in it, so neither rule can reach a backed-off,
// broken or capped credential, and the "skip any that fail" the spec asks of a
// round robin is this set and not a rule of its own.
func (s *Selector) within(ctx context.Context, usable []Credential, marks map[string]Mark) (Credential, error) {
	if s.Strategy == StrategyRoundRobin {
		return s.rotate(ctx, usable)
	}
	return s.leastRecentlyExhausted(ctx, usable, marks)
}

// rotate takes the next usable member of a tier, in the order the caller
// offered them, and consumes one turn of the project's counter (creds/
// roundrobin, F6, D1625). The offered order is the caller's and, for a pool,
// the owner's: the pool's listed order is where a rotation starts and the order
// it turns in (F7).
//
// A turn is consumed by a selection that had to CHOOSE, which is why a usable
// set of one is answered without touching the counter: a pool whose other
// members are held is not a rotation, and spending a project-wide turn on it
// would move every other pool's rotation along for nothing. What a turn buys is
// what the counter is for: several service instances selecting at once each
// take a different member rather than the same one, because the read and the
// write of the counter are one compare-and-set (Store.RoundRobin) and a lost
// race re-reads rather than overwrites.
//
// It is a rotation per selection and not per call to the upstream in the sense
// D1625 is about, and the property comes from the caller: the credential this
// answers is written onto the run by take, and a run that has one is answered
// by the sticky path before any rule here is reached (D154, D874). So a run
// takes one turn and keeps it.
//
// A count below zero is a node a hand-edit wrote and is read as zero rather
// than as an index into the middle of the set, exactly as the tie-break reads
// it (leastRecentlyExhausted).
func (s *Selector) rotate(ctx context.Context, usable []Credential) (Credential, error) {
	if len(usable) == 1 {
		return usable[0], nil
	}
	n, err := s.Store.RoundRobin(ctx)
	if err != nil {
		return Credential{}, err
	}
	if n < 0 {
		n = 0
	}
	return usable[int(n%int64(len(usable)))], nil
}

// leastRecentlyExhausted picks from a usable set: the credential exhausted
// longest ago (D154), and among credentials exhausted equally recently — of
// which the commonest case is a set that has never been exhausted at all, where
// every `Since` is zero — a round robin through the counter in creds/.
//
// The order is by the exhaustion time and never by "who was used more": the
// state does not record uses, and it does not need to. A credential that has
// been idle for a week and one that has just come back from a reset are the
// same thing to a token's owner — quota they can spend — and the round robin is
// what spreads new runs over them evenly.
//
// It reads no Settings, and it takes none: the tie-break's policy is the
// round-robin counter in creds/ and the marks it orders by, both of which it
// has, and a setting it consulted would be a policy input this signature
// promised and the body never read — the shape a later reader takes for
// "wired, and the round robin is where a per-tier rule would go" and then
// looks for a caller that sets it. The settings the selection does turn on are
// read where the tier is known (free, and the mark rules beside it).
func (s *Selector) leastRecentlyExhausted(ctx context.Context, usable []Credential, marks map[string]Mark) (Credential, error) {
	ordered := make([]Credential, len(usable))
	copy(ordered, usable)
	since := func(c Credential) time.Time { return recency(marks[c.Name]) }
	// The order it is sorted into is the OFFERED order, held stable: a tie is
	// left as the caller listed the candidates, so the tied set has one order
	// and it is the caller's — for a pool, the order its owner wrote it in
	// (F7: within a tier the pool's listed order breaks the tie). A round robin
	// over a set that arrived in whatever order a document read produced would
	// not be a round robin, and sorting the tie by name would be this package
	// re-ordering a pool the owner ordered deliberately.
	sort.SliceStable(ordered, func(i, j int) bool {
		return since(ordered[i]).Before(since(ordered[j]))
	})
	tied := []Credential{ordered[0]}
	for _, c := range ordered[1:] {
		if !since(c).Equal(since(ordered[0])) {
			break
		}
		tied = append(tied, c)
	}
	if len(tied) == 1 {
		return tied[0], nil
	}
	n, err := s.Store.RoundRobin(ctx)
	if err != nil {
		return Credential{}, err
	}
	if n < 0 {
		// A counter a hand-edit made negative must not index out of the set.
		n = 0
	}
	return tied[int(n%int64(len(tied)))], nil
}

// recency is how long ago a credential was last exhausted: the mark's Since,
// and the zero time for a credential that never was — which is the oldest of
// all, and so the first a new run takes.
func recency(m Mark) time.Time {
	switch m.State {
	case StateExhausted, StateBackedOff:
		return m.Since
	}
	return time.Time{}
}

// free reports whether the credential can be used by run at now: its mark holds
// nothing, and the per-token cap has room (D142's second control).
//
// The settings are read per tier here, at the one place a candidate's tier is
// known, and that is where review findings M1 and M2 are settled: both of D142's
// controls are a shared tier's (Settings.forTier), so this is the only line that
// needs to know it. A tier B candidate is then held by its mark's own time — a
// reset, or a backoff — and never by a window or a cap written for donated
// subscription quota.
func free(c Credential, marks map[string]Mark, leases []Lease, run Run, now time.Time, set Settings) bool {
	set = set.forTier(c.Tier)
	if !marks[c.Name].Usable(now, set) {
		return false
	}
	return Free(leases, c.Name, now, run, set.concurrency())
}

// earliest is the time the first credential of a group becomes usable — by
// waiting out its mark, or by enough other runs' leases lapsing — and whether
// one ever does (false when every credential of the group is broken, or holds a
// mark no amount of waiting clears). probe reports that the credential that time
// belongs to is backed off and its wait has passed, which is C1.12's probe
// (NeedsProbe): the probe decides whether the wait is over, and the time is when
// the credential could serve work once it is.
func earliest(cs []Credential, marks map[string]Mark, leases []Lease, run Run, now time.Time, set Settings) (time.Time, bool, bool) {
	var (
		best  time.Time
		probe bool
		found bool
	)
	for _, c := range cs {
		m := marks[c.Name]
		// The tier's own settings, for the same reason free reads them: the
		// window a mark is held for is the shared tier's (M1/M2).
		tierSet := set.forTier(c.Tier)
		at, ok := m.Ready(now, tierSet)
		if !ok {
			continue
		}
		// Probe is what the MARK needs — its own wait has passed, C1.12 — and it
		// is decided before a lease, or D142's window, has a chance to push the
		// time later: a caller told to sleep instead would never probe the
		// credential at all. The two facts are both reported and neither is a
		// lie: `at` is the earliest this credential could serve work (the window
		// the control holds it for, or the lease that does), and Probe says the
		// probe C1.12 owes it is due now.
		needs := m.NeedsProbe(now)
		if !Free(leases, c.Name, now, run, tierSet.concurrency()) {
			// The cap is the other reason a credential is not usable, and it
			// lifts when the holding leases lapse — which lapse, for a cap above
			// one, is SlotFrees' answer and not the first one's. It can only push
			// the time later, never earlier: a credential both exhausted and held
			// waits for the later of the two. So with both in play the answer
			// carries both facts — probe it, and it could be used at best at `at`
			// — and neither is a lie.
			exp, ok := SlotFrees(leases, c.Name, now, run, tierSet.concurrency())
			if !ok {
				// The cap refuses the credential and no lapse frees it: a limit
				// of 0 or less, which Free reads as "no room at all" for a
				// negative and as "no cap" for a zero — and a zero cannot reach
				// here, since Free returns true for it (review finding: a
				// hand-built negative cap failed closed in Free and answered
				// "usable now" here). There is no time to wait for, and
				// answering one would be telling the caller to retry
				// immediately against a credential the cap will refuse again.
				// So it is not a candidate for "when", exactly as a broken one
				// is not: skip it, and let the tier's other credentials — or the
				// next tier, or ErrNoCredential — answer.
				continue
			}
			if exp.After(at) {
				at = exp
			}
		}
		if !found || at.Before(best) {
			best, probe, found = at, needs, true
		}
	}
	return best, probe, found
}

// waitUntil is at minus now, never negative: a time in the past is a wait of
// nothing, and a negative duration in an answer is a caller's bug waiting to be
// read as "the reset is behind us, so try again" forever.
func waitUntil(now, at time.Time) time.Duration {
	if d := at.Sub(now); d > 0 {
		return d
	}
	return 0
}

// lookup finds a credential in a candidate list by name.
func lookup(cs []Credential, name string) (Credential, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return Credential{}, false
}

// groupOf is the candidates of one tier, in the order they were offered.
func groupOf(cs []Credential, tier Tier) []Credential {
	var out []Credential
	for _, c := range cs {
		if c.Tier == tier {
			out = append(out, c)
		}
	}
	return out
}

// checkCandidates refuses a candidate list this package cannot select from, and
// copies the ones it can. Every refusal is a caller's mistake reaching the
// wrong place, and none of them is skipped quietly:
//
//   - an empty list is ErrNoCandidates, because a caller with no credential to
//     offer (an admission that dropped every account, a bundle read that came
//     back empty) must see that, not an exhausted account;
//   - a candidate of an unknown tier is refused, and an empty name too;
//   - the same name twice is refused: it would be one credential counted twice
//     in a tie, which is a worse round robin for a reason nobody could see;
//   - a tier C candidate — a named provider key — is refused unless this call's
//     model is one it may be served by, and a list that leaves nothing but such
//     candidates is refused with the sentence naming Check 50 (D3000, D150);
//   - a tier A or tier B candidate is refused when this call's model is NOT a
//     Claude one, which is the same rule read the other way (D3000, D3007): a
//     provider's key runs a model that is not Claude's, and a shared
//     subscription token or the project's key cannot. A list that leaves
//     nothing but those is refused rather than run on one;
//   - a tier C candidate is refused unless it is the key of the provider this
//     call named as the one that runs its model (D3000, R2): a list can hold
//     several providers' keys, and one provider's key handed to another's model
//     is a bearer token sent to an endpoint it was not issued for. A list left
//     with nothing but such keys is refused with a sentence naming the caller's
//     provider and the candidate's, or the call's own silence about it.
//
// # Which tier-C candidates this call may be served by, and why the list is the
// # answer (D3000, R3, F5)
//
// A tier C credential is a third-party provider's key, and this release serves
// it for every model but a Claude one: a CLAUDE model through a provider key
// waits for Check 50, which measures Claude's prompt-cache markers on that route
// and nothing about another model (D150, D3000). So the model decides, and the
// model is the caller's (Selector.Model) — an empty one reads as a Claude model
// and keeps this gate exactly as wide as it was before the model arrived.
//
// The rule cuts the other way for the tiers below it, and by the same fact: a
// call whose model is not Claude's is a call neither a subscription token nor
// the project's own Anthropic key can run, so those candidates are dropped too
// (D3007). Without that, the offering would be worse than useless: the cascade
// walks tier A first, so a provider's model would be served by whatever shared
// token happened to be in the list — the wrong route entirely, on donated quota
// — and the provider key the caller offered for it would never be reached. This
// is the one place the two directions are decided, so a caller cannot be served
// a credential its model's route cannot use.
//
// The class is not the whole of the tier-C question, and the second half is the
// tie the same sentence asks for: WHICH provider. A list of a project's named
// entries holds one key per provider the published policy names, so several of
// them are ordinary and not a mistake, and the model's own name does not tell
// them apart (the patterns of the policy's `models` are what do, and OpenRouter
// runs models other vendors made). The caller therefore names the provider that
// runs this model (Selector.Provider) and a key of any other is dropped; a
// call that names none gets no tier-C credential at all, since a key of one
// provider is no evidence that the model is its own. Without the tie the list's
// first provider key would be handed the model — at best a route that cannot
// run it, at worst one provider's bearer token at another's endpoint.
//
// A refused candidate is DROPPED from the list, and the rest of the call is not
// its business. That is the difference a bundle of named entries makes (KS1
// offers a coding caller the provider entries of its own repository), and
// without it the gate would be far wider than the decision it implements: a
// repository that holds an OpenRouter key would be refused EVERY Claude call it
// makes — the cascade's own model entries are offered beside the provider entry,
// and the whole call would fail on a candidate that call would never have been
// served by. What the refusal is for is the call whose only credential is a
// provider's: a Claude model routed through one, which is exactly what the
// gateway pins and what the service's provider route builds a list of one for.
// Such a call reads the sentence, and it is the sentence that names the check,
// the reason and the decision.
func checkCandidates(cs []Credential, model, provider string) ([]Credential, error) {
	if len(cs) == 0 {
		return nil, ErrNoCandidates
	}
	// Which classes of credential can run this call at all is the model's
	// question, and it is asked before the policy's (D3000, D3007). A provider's
	// key runs every model but a Claude one, and the two Anthropic tiers run a
	// Claude one alone: the same fact in both directions, so a candidate of the
	// wrong class is dropped here rather than offered to the cascade — a model a
	// provider runs sent to Anthropic is a call that route cannot serve, made on
	// quota somebody donated for a Claude model.
	byProvider := ProviderServes(model)
	out := make([]Credential, 0, len(cs))
	seen := make(map[string]bool, len(cs))
	var refused, wrongProvider []Credential
	for _, c := range cs {
		if c.Name == "" {
			return nil, fmt.Errorf("creds: a candidate of tier %q has no name", c.Tier)
		}
		switch c.Tier {
		case TierA, TierB:
			if byProvider {
				refused = append(refused, c)
				continue
			}
		case TierC:
			if !byProvider {
				refused = append(refused, c)
				continue
			}
			// The second half of the tier-C question, and the one the model
			// cannot answer on its own: which provider's key this is against
			// which provider runs this call's model (Selector.Provider). A
			// candidate with no provider of its own is never served either:
			// an unnamed key is no more evidence that it is this call's than
			// an unnamed call is evidence that it is the key's.
			if provider == "" || c.Provider != provider {
				wrongProvider = append(wrongProvider, c)
				continue
			}
		default:
			return nil, fmt.Errorf("creds: %q has tier %q, which is not a tier this Fugaro knows: it selects tier a (a shared subscription token), tier b (the project's API key) and tier c (a named provider's key, for a model that is not Claude's)", c.Name, c.Tier)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("creds: %q is offered twice, and one credential offered twice would be counted twice", c.Name)
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		if len(wrongProvider) > 0 {
			// Every candidate left is a provider's key and none of them is the
			// provider's this call is for — the other half of the tier-C
			// question, and a refusal of its own because its fix is not the
			// policy's or the account's but the CALLER's: the provider that
			// runs the model it named, or that provider's key in the list. The
			// alternative is the harm the tie exists for — one provider's
			// bearer token sent to another's endpoint.
			switch p := wrongProvider[0]; {
			case provider == "":
				return nil, fmt.Errorf("creds: %q is a provider's key, and this call names no provider: a credential of tier C is served only to a call that says which provider runs its model (D3000)", p.Name)
			case p.Provider == "":
				return nil, fmt.Errorf("creds: %q is offered with no provider, and this call's model is run by %q: a provider's key is served only for the provider it belongs to (D3000)", p.Name, provider)
			default:
				return nil, fmt.Errorf("creds: %q is the key of the provider %q, and this call's model is run by %q: one provider's key never serves another provider's model (D3000)", p.Name, p.Provider, provider)
			}
		}
		if byProvider {
			// Nothing is left but the tiers a Claude call runs on, and this
			// call's model is not Claude's. As above, the answer is not "no
			// credential is offered" — the caller offered some, and the mistake
			// is which — and it is not the Check 50 sentence, which is about the
			// other direction.
			return nil, fmt.Errorf("creds: %q is tier %s, and a call whose model is not Claude's is served by a provider's own key: neither a shared subscription token nor the project's key can run it (D3000, D3007)", refused[0].Name, refused[0].Tier)
		}
		// Nothing is left but provider keys this call's model may not be served
		// by, and that is the refusal D150's gate is: the answer is not "no
		// credential is offered" (which would send the caller to the bundle for
		// a call the bundle answered) and not "everything is exhausted" (which
		// would idle a run on a credential that is not coming).
		return nil, fmt.Errorf("creds: %q is tier C, and this release does not select a third-party route for a Claude model: it waits for Check 50 (D150, C3.6)", refused[0].Name)
	}
	return out, nil
}

// ProviderServes reports whether a call for this model may be served by a
// provider's own key: every model but a Claude one (D3000, R3). A caller that
// named no model is not one of them — the fail-closed direction, and the one
// every caller had before a selection could be told the model at all.
//
// It is the whole of the class question, which is why it answers both
// directions of it: a call it answers true for is a call the Anthropic tiers
// cannot run, so checkCandidates drops them, and a call it answers false for is
// one a provider's key cannot (D3007). It says nothing about WHICH provider's
// key: a call may be served by a provider's and still not by the one offered,
// which is the tie Selector.Provider decides beside this one (checkCandidates).
//
// The vocabulary is config's own (IsClaudeModel over ClaudePatterns): which
// models are Claude's is the pin ladder the config, the mount and the launch
// already share (R3), and a second list here would be a second answer to a
// question that has one.
//
// It is exported because the rule has a second reader outside this package: the
// credentials service decides which candidates a probe may run on, and a probe
// sends the call's OWN model through the credential it asks about — so a probe
// on a provider key for a Claude model would be exactly the route Check 50
// gates, made by the service rather than by the agent
// (internal/credsvc's probeCandidates). A caller that restated the rule instead
// would be a second answer to it.
func ProviderServes(model string) bool {
	return model != "" && !config.IsClaudeModel(model)
}

func (s *Selector) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (r Run) valid() error {
	if r.Slug == "" || r.ID == "" {
		return fmt.Errorf("creds: a selection needs the run identity's repository and run id, and this one is slug %q, run %q", r.Slug, r.ID)
	}
	return nil
}
