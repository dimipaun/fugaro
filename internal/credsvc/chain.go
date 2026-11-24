package credsvc

// chain.go is CS1: the credentials service's serving path for a call the
// project's routing table routes.
//
// # What this file decides, and what it does not
//
// The model a call is routed by is the one in its BODY and never a header
// (chain_wire.go's second rule): a body that names none is refused, and no
// header is read for it. The model is resolved to its entry (routing.Resolve,
// RT1) and the entry's chain is offered in the CHAIN'S OWN ORDER, which is what
// replaces the fixed tier order (R4, D3170): a pool by its rule, the launcher's
// pool through C2's lookup, the project's Anthropic key, a provider's key,
// Vertex. A source is skipped where it cannot serve this call — a key, a
// provider key or Vertex the repository is not admitted to (D3182), a pool
// member whose account does not admit the repository (R20), an empty
// launcher's pool, an exhausted source (D3180) — and a source the run's grant
// does not cover is never served at all.
//
// Skip is a decision about a source and never about the call: the walk moves on
// to the chain's next source, and only when the chain is spent does the call
// meet a refusal — exhausted with the earliest reset where something becomes
// usable, the selection's own refusals otherwise (refuseSelection). Nothing is
// downgraded silently: every CREDENTIAL that was dropped is named with its
// reason on the refusal the run and the operator read (Tried), and the answer
// names the source that served.
//
// # What a skip is recorded as, and what is not recorded
//
// Tried is the wire's list of CREDENTIALS (credsvc/wire.go): a name, a tier and
// one of the four reasons. A source dropped for a reason that is not about a
// credential has no credential to name and is therefore in no tried list — a
// source the run's grant does not cover, a launcher's pool nothing designated,
// a pool with no servable member, and a Vertex source this release cannot serve
// (CS4 lands that) all leave the chain quietly, and the CHAIN the call was
// served by is what the answer's own source member says. A credential-level
// skip — a key the repository is not admitted to, an account the repository is
// not admitted to — is named, because it is one of the four reasons and it is
// exactly what the run's halt message is built from.
//
// # The two halves this file does not own
//
// The TABLE arrives through PublishedTable below: it is RT4's member of the
// published policy, and until RT4 lands this seam answers "this project
// publishes no table" for every policy — so no call of this release is served
// by a chain, and every existing path is exactly what it was. RT4 assigns it
// from the node, which is the whole of what that task owes this file.
//
// The LAUNCHER'S POOL arrives through LauncherPools, C2's routing.LauncherPool:
// PL4 assigns the real lookup from its own init, and until then the stub
// answers "no pool designated" for every principal and every name, which is
// D3180's answer for a project nothing has designated yet.
//
// # R16: a placement is sticky for one step
//
// The chain is walked from its first source at EVERY call, and no placement this
// path writes is read back as a sticky choice: D154's stickiness held a run on
// one credential for the whole run, and R16 amends it to one step. Within a step
// the placement is stable for the reason the walk is deterministic — the same
// chain is offered in the same order, and the first source that can serve is the
// answer — and a source that becomes usable mid-step takes the call back, which
// is the walk's own rule and not a second one (this file never reads
// Store.Current; the chain is walked from the top on every call of the step).
// The placement is still written (creds.Selector.Place, .Serve): the license
// that caps a shared account (D142) and the run's own record of where it is are
// not stickiness, and both are still owed.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
)

// PublishedTable answers the routing table a project's published policy carries,
// and whether it carries one at all.
//
// It is a seam and not a reader, for the reason C1's own three stubs are (I1's
// table, MG3's bridge, PL3's rule): the contract ships before RT4 publishes the
// table, so the side that walks a chain has to be written and its tests have to
// run before a node can carry one. What it answers meanwhile is not a
// placeholder: "this project publishes no table" is the state every installation
// not yet migrated is in, and it is the answer that can never serve a call by
// accident — a project with no table is served by the paths it was served by
// before this contract existed.
//
// RT4 replaces this body with a read of the node's own member, and a test that
// serves a call by a chain installs its own table here (chain_test.go). The
// second answer is not a convenience: a project that publishes a table WITH NO
// ENTRY for a model is not the same situation as a project that publishes none,
// and the walk refuses the first (R22) where the second is left alone.
var PublishedTable = func(*projectpolicy.Policy) (routing.Table, bool) {
	return nil, false
}

// LauncherPools is C2's lookup for "the launcher's pool" (routing.LauncherPool,
// D3176): the pool one run is served from, resolved from the principal the run's
// slip carries and the pool a launch named with `--pool` (CS12).
//
// PL4 assigns the real lookup from its own init; until then it is nil and
// launcherPool answers the stub, which designates nothing (launcherpool_stub.go,
// D3180). It is nil-and-not-the-stub at declaration so that PL4's init — which
// runs after every package-level declaration — is the only writer that matters,
// the same ordering rule C1's stubs state.
var LauncherPools routing.LauncherPool

// launcherPool is the lookup this walk resolves a launcher's pool through.
func launcherPool() routing.LauncherPool {
	if LauncherPools == nil {
		return StubLauncherPool{}
	}
	return LauncherPools
}

// ---------------------------------------------------------------------------
// A call's chain, as the walk offers it
// ---------------------------------------------------------------------------

// chainSource is one source of a call's chain: what the table said about it,
// what the answer carries for it, and the credential material this service may
// serve it with.
type chainSource struct {
	// name is the source as the answer and the run's record carry it (C1's
	// SourceName): the kind, and the name of the one that kind names. It is
	// never a value.
	name routing.SourceName
	// first is whether this is the CHAIN'S first source, which is what the
	// answer's fallback member is decided by: a call served by any other source
	// fell back, whatever the reason the ones before it were skipped for.
	first bool
	// model is the CANONICAL Claude id of the model this source serves: the
	// source's own Model where it names one — a written downgrade, which
	// spec.md requires to be written — and the model the call asked for
	// otherwise (CS3's sourceModel). It is what the answer's HeaderModel
	// carries, because it is what the gateway prices the call by.
	model string
	// spelling is how that model is written on THIS source, which is what the
	// request's `model` member becomes on the way out (CS3's rewriteModel): the
	// Claude API takes the pin's own id, a provider takes its route's, Vertex
	// its dated one. A source with no spelling for the model is never sent it
	// (D3175) and never reaches a chain at all.
	spelling string
	// members are the credentials this source may serve with: one for each of
	// the kinds that is a single credential of the project, and a pool's
	// members in the pool's owned order for the two pool kinds. An empty list
	// is a source with nothing usable, which the walk skips (D3180).
	members []creds.Credential
	// pool is set exactly for the two pool kinds, and it is what C2's picker
	// needs: the reference whose rule and threshold travel with it, so the
	// picker reads the pool's configuration no second time.
	pool routing.PoolRef
	// pooled is whether this source's member is chosen by C2's picker rather
	// than being the one credential of a single-credential source. It is not
	// pool.Pool != "": a launcher's pool with a reference but no members is
	// still a pooled source, and answering it as a single-credential one would
	// send the call to whichever member happened to be first with no rule
	// behind it.
	pooled bool
}

// chainPlan is a call's chain: the sources the walk offers, in the chain's own
// order, and what became of the credentials that were dropped.
type chainPlan struct {
	// model is the model the call names, read from its body.
	model string
	// sources are the sources the walk offers, in the chain's own order.
	sources []chainSource
	// skipped is every credential dropped on the way, in the chain's own order:
	// a refusal's sentence and the run's own halt message are built from it
	// (Tried), so a dropped credential is never silent.
	skipped []Tried
}

// chainServed is what the answer carries about a chain call's source (C1's
// three members): the source that served, whether it was the chain's first, and
// the model that served. Only a caller that sent the stage header ever sees it
// (chainAnswer, SentStage).
type chainServed struct {
	source   routing.SourceName
	fallback bool
	model    string
}

// chainRefusal is a refusal the walk answers with before any credential is
// touched: the status, the wire type and the sentence, all three of which the
// caller writes (refuseCall) so that every refusal of this path is audited by
// the one path that audits refusals — exactly one line, and no value in it.
type chainRefusal struct {
	status int
	typ    string
	what   string
}

func (r *chainRefusal) Error() string { return r.what }

// chainWalk is one call's walk over its chain. It is built per call and holds
// nothing across calls: the chain is walked from its first source at every call
// (R16), and the `sent` set the loop below hands it is this call's own.
type chainWalk struct {
	plan *chainPlan
	sel  *creds.Selector
	// pick is C2's picker, asked once per pool source and never for a
	// single-credential one (PL3's PoolRule, which is what the service wires).
	pick creds.PoolPicker
	run  creds.Run
	// cl is the call being walked, held for exactly one thing: the request's
	// `model` member becomes the served source's spelling on the way out
	// (rewriteModel), and it is written from asked rather than from the body as
	// it stands, so a retry to another source rewrites the AGENT'S model and
	// never the previous source's spelling of it.
	cl *call
	// asked is the body as the agent wrote it.
	asked []byte
	// now is the service's clock, and it is the walk's own copy rather than a
	// second reading of a wait: a wait the selector or the picker answered is
	// turned back into the instant it was measured from (an ExhaustedUntil
	// carries both), so the earliest reset of the whole chain is one instant and
	// never two clocks' worth of arithmetic.
	now func() time.Time
	// served is the source that answered, written by next and read by the
	// answer (answer, chainAnswer).
	served *chainServed
}

// next answers the credential this call's next attempt goes to, and it is the
// whole of the chain's order: the sources are offered in the order the table
// wrote them, a source that cannot serve is skipped, and the earliest reset of
// anything that is only exhausted is what the call waits for when the chain is
// spent.
//
// sent is what this call has already attempted, by credential name: a retry
// after a 401 or a usage limit is a fresh walk that may not re-send to a
// credential this call has already spent, exactly as retryPool is for the
// cascade's own path.
func (w *chainWalk) next(ctx context.Context, sent map[string]bool) (creds.Credential, time.Duration, error) {
	var (
		earliestAt   time.Time
		haveEarliest bool
		probe        bool
	)
	for i := range w.plan.sources {
		src := &w.plan.sources[i]
		c, wait, err := w.offer(ctx, src, sent)
		if err != nil {
			var until *creds.ExhaustedUntil
			if errors.As(err, &until) {
				if !haveEarliest || until.At.Before(earliestAt) {
					earliestAt, haveEarliest, probe = until.At, true, until.Probe
				}
				continue
			}
			// ErrNoCredential, and anything the state could not answer: this
			// source serves nothing now and its own wait was not a time. The
			// next source of the chain is still the call's, and the state's own
			// error is not swallowed — a store that could not be read is
			// answered by the last source's refusal rather than silently
			// turned into "exhausted".
			if !isSelectionRefusal(err) {
				return creds.Credential{}, 0, err
			}
			continue
		}
		if c.Name == "" {
			continue
		}
		if wait > 0 {
			if at := w.now().Add(wait); !haveEarliest || at.Before(earliestAt) {
				earliestAt, haveEarliest, probe = at, true, false
			}
			continue
		}
		if src.spelling != "" && w.cl != nil {
			body, err := rewriteModel(w.asked, src.spelling)
			if err != nil {
				// A body this service cannot read one model out of — the walk
				// already read one out of it to resolve the chain, so this is
				// a body that changed shape under the walk, and it is the
				// call's refusal rather than a skip.
				return creds.Credential{}, 0, err
			}
			w.cl.body = body
		}
		w.served = &chainServed{source: src.name, fallback: !src.first, model: src.model}
		return c, 0, nil
	}
	if haveEarliest {
		wait := earliestAt.Sub(w.now())
		if wait < 0 {
			wait = 0
		}
		return creds.Credential{}, wait, &creds.ExhaustedUntil{At: earliestAt, Wait: wait, Probe: probe}
	}
	return creds.Credential{}, 0, creds.ErrNoCredential
}

// offer is one source's answer: the credential it serves with, or the reason it
// does not.
//
// A credential this call has ALREADY used is never used again (forward's sent
// set), but a source left with nothing unsent is not an empty one: it is still
// judged, and never served from. The difference is a whole behaviour — a
// credential the call spent and left a mark on is one the call can wait for,
// and a walk that answered "this source serves nothing" there would tell a run
// at a usage limit that no credential becomes usable by waiting
// (refuseSelection's state_unavailable) where the mark holds the reset it may
// wait out. It is retryPool's rule, which is what the cascade's own retry does:
// ask over the whole set again, and never send to what was already sent.
func (w *chainWalk) offer(ctx context.Context, src *chainSource, sent map[string]bool) (creds.Credential, time.Duration, error) {
	if src.pooled {
		members := chainRemaining(src.members, sent)
		// Every member this source could serve with has been attempted: the
		// pool is asked over the WHOLE set all the same, and never served from.
		// What comes back is then the state's own answer — the earliest reset
		// of a pool whose members are all exhausted — and a member the picker
		// hands back is one this call has already sent to, which is the one
		// thing this loop must not do.
		spent := len(members) == 0 && len(src.members) > 0
		if spent {
			members = src.members
		}
		// The pool's own rule judges and picks in one answer (PickInPool), so
		// the member it answers is already free: the walk records the placement
		// (Place) and never judges it a second time. The one rule it does not
		// apply is the policy's tier gate, which the chain replaces (R4).
		c, wait, err := w.pick.PickInPool(ctx, src.pool, w.run, members)
		if err != nil {
			return creds.Credential{}, wait, err
		}
		if spent {
			return creds.Credential{}, 0, creds.ErrNoCredential
		}
		if err := w.sel.Place(ctx, w.run, c); err != nil {
			return creds.Credential{}, 0, err
		}
		return c, 0, nil
	}
	var spent []creds.Credential
	for _, c := range src.members {
		if sent[c.Name] {
			spent = append(spent, c)
			continue
		}
		wait, err := w.sel.Serve(ctx, w.run, c)
		if err != nil {
			return creds.Credential{}, wait, err
		}
		return c, 0, nil
	}
	// Nothing in this source is left to serve with, so the credentials it was
	// spent on are judged once more and never served (Judge): a mark among them
	// is the reset the call waits for, and it is judged after the unsent ones
	// so that a source which can still serve does.
	for _, c := range spent {
		wait, err := w.sel.Judge(ctx, w.run, c)
		if err != nil {
			return creds.Credential{}, wait, err
		}
	}
	return creds.Credential{}, 0, creds.ErrNoCredential
}

// chainMembers is every credential a chain may serve with, in the chain's own
// order and with the pool kinds flattened. It is what forward's C1.12 probe is
// handed (probeDue reads a candidate list), and nothing else reads it: the walk
// itself never consults it, because a pool's members are the picker's to choose
// from and a flattened list would lose the rule that chose them.
func chainMembers(plan *chainPlan) []creds.Credential {
	var out []creds.Credential
	for _, src := range plan.sources {
		out = append(out, src.members...)
	}
	return out
}

// isSelectionRefusal reports whether err is one of the two the walk answers by
// moving to the chain's next source: a credential that serves nothing now
// (ErrNoCredential) and a pool handed no members at all. Everything else —
// a state that could not be read — is the call's answer and not a skip, because
// a store that failed is not a chain that is spent.
func isSelectionRefusal(err error) bool {
	return errors.Is(err, creds.ErrNoCredential) || errors.Is(err, creds.ErrNoCandidates)
}

// remaining is the members this call has not already attempted, in the pool's
// owned order.
func chainRemaining(members []creds.Credential, sent map[string]bool) []creds.Credential {
	out := make([]creds.Credential, 0, len(members))
	for _, c := range members {
		if sent[c.Name] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ---------------------------------------------------------------------------
// The chain of one call
// ---------------------------------------------------------------------------

// chainFor answers the chain this call is served by, and whether the routing
// table decides it at all.
//
// served=false is "this call goes by the path it went by before this contract":
// the project publishes no table (PublishedTable). Every other answer is a plan
// or a refusal, and the refusal is a *chainRefusal the caller writes itself.
func (s *Server) chainFor(ctx context.Context, cl *call, id Identity, canonical string, shared *projectpolicy.Policy, b *Bundle, binding *slipBinding) (*chainPlan, bool, error) {
	if shared == nil {
		return nil, false, nil
	}
	table, published := PublishedTable(shared)
	if !published {
		return nil, false, nil
	}
	grant := grantOf(binding)
	if grant != nil && len(grant.Sources) == 0 {
		// A grant that covers no source of the new kinds: the launcher that
		// built it is a release behind the table, and its run is served by the
		// release-1 bridge rather than by a chain — which is the whole of what
		// the bridge is for (chain_wire.go's release-1 bridge). The bridge
		// answers whether that repository has an old record at all, and the stub
		// this release ships answers "no record" for everything, so such a run
		// is refused with the fix rather than served by whatever the record
		// happened to hold.
		sources, found := LegacyChain.OldSources(id.Slug)
		if !found {
			return nil, true, &chainRefusal{
				status: StatusUnavailable,
				typ:    ErrorStateUnavailable,
				what: "this run's grant covers no source of the project's routing table: it was launched by a release " +
					"that predates the table, and this project holds no record of what that repository's runs go out " +
					"on — relaunch the run with a current launcher, whose grant covers the table's sources",
			}
		}
		return s.planSources(ctx, sources, "", id, canonical, shared, b, binding)
	}
	model, named := callModel(cl.body)
	if !named {
		return nil, true, &chainRefusal{
			status: StatusRequestRefused,
			typ:    ErrorRequestRefused,
			what: requestRefused + "this project publishes a routing table, and a table is resolved against the model " +
				"a call names in its BODY: this call's body names none, and a model is never read from a header",
		}
	}
	entry, err := routing.Resolve(table, model)
	if err != nil {
		if errors.Is(err, routing.ErrNoEntry) {
			// R22: a model no entry matches is a model this project has not said
			// how to reach, and the fail-closed direction is the point of the
			// table. Falling back to "whatever credential is around" is exactly
			// the behaviour the table replaces.
			return nil, true, &chainRefusal{
				status: StatusRequestRefused,
				typ:    ErrorRequestRefused,
				what: requestRefused + "this project's routing table holds no entry for the model this call names, " +
					"and a model with no entry is a model the project has not said how to reach: name the model the " +
					"table routes, or publish an entry for it",
			}
		}
		return nil, true, err
	}
	return s.planSources(ctx, entry.Chain, model, id, canonical, shared, b, binding)
}

// planSources builds a call's chain from a list of sources: the table's entry,
// or the release-1 bridge's own answer about an old record.
func (s *Server) planSources(ctx context.Context, sources []routing.Source, model string, id Identity, canonical string, shared *projectpolicy.Policy, b *Bundle, binding *slipBinding) (*chainPlan, bool, error) {
	plan := &chainPlan{model: model}
	grant := grantOf(binding)
	held := bundleTiers(b)
	var admitted map[string]bool
	for i, src := range sources {
		if !grantCovers(grant, src) {
			// A source the run's grant does not cover is never served — not
			// skipped-and-then-served, never served at all. There is no
			// credential to name here, so nothing is written to the tried list:
			// the grant is the launch's own reading of the table, and a source
			// outside it was never this call's to consider.
			continue
		}
		out, skipped, err := s.planSource(ctx, src, model, i == 0, id, canonical, shared, b, binding, held, &admitted)
		if err != nil {
			return nil, true, err
		}
		plan.skipped = append(plan.skipped, skipped...)
		if len(out.members) == 0 {
			continue
		}
		plan.sources = append(plan.sources, out)
	}
	return plan, true, nil
}

// planSource is one source of a chain, resolved: the credential material this
// service may serve it with, or a credential-level skip (or nothing at all).
//
// admitted is filled at most once per call, and only where a pool member's
// account has to be checked against the repository's own node (R20): the read
// is a policy read, and a call whose chain holds no pool never makes it.
func (s *Server) planSource(ctx context.Context, src routing.Source, model string, first bool, id Identity, canonical string, shared *projectpolicy.Policy, b *Bundle, binding *slipBinding, held map[string]creds.Tier, admitted *map[string]bool) (chainSource, []Tried, error) {
	name := routing.SourceName{Kind: src.Kind, Name: sourceNameOf(src)}
	out := chainSource{name: name, first: first, spelling: ""}
	if model != "" {
		// D3175: the map is the only authority for a spelling, and a source the
		// map does not spell this model for is skipped by the walk rather than
		// sent the id the agent asked with — a call to a model nobody measured
		// on that source. It is skipped here, before the credential is even
		// looked up, so no chain holds a source that could not be sent the call.
		served, spelling, ok := sourceModel(src, model)
		if !ok {
			return out, nil, nil
		}
		out.model, out.spelling = served, spelling
	}

	switch src.Kind {
	case routing.KindLauncherPool:
		// A launcher's pool names no pool in the table: which pool serves is
		// decided per run, from the run's verified principal and the pool its
		// launch named (D3176, D3180). With no designation the source is
		// skipped and the call moves on with the step flagged a fallback.
		if binding == nil || binding.slip == nil {
			return out, nil, nil
		}
		ref, found, err := launcherPool().LauncherPool(ctx, binding.slip.Launcher, namedPoolOf(binding))
		if err != nil {
			return out, nil, err
		}
		if !found {
			return out, nil, nil
		}
		out.pool, out.pooled = ref, true
		out.name.Name = ref.Pool
		pool, ok := namedPool(ref.Pool, shared)
		if !ok {
			return out, nil, nil
		}
		out.members = poolOfferingOf(pool, shared, held)

	case routing.KindPool:
		pool, ok := namedPool(src.Pool, shared)
		if !ok {
			return out, nil, nil
		}
		out.pool, out.pooled = routing.PoolRef{
			Project:   id.Project,
			Pool:      pool.Name,
			Rule:      poolRule(pool),
			Principal: principalOf(binding),
		}, true
		out.members = poolOfferingOf(pool, shared, held)

	case routing.KindAnthropic:
		c, ok := ownNamedEntry(b, id.Slug, config.AnthropicKeyAccount, creds.TierB)
		if !ok {
			return out, nil, nil
		}
		if !sourceAdmits(src, canonical) {
			return out, []Tried{{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonNotAdmitted}}, nil
		}
		out.members = []creds.Credential{c}

	case routing.KindProvider:
		c, ok := ownNamedEntry(b, id.Slug, src.Provider, creds.TierC)
		if !ok {
			return out, nil, nil
		}
		c.Provider = src.Provider
		if !sourceAdmits(src, canonical) {
			return out, []Tried{{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonNotAdmitted}}, nil
		}
		out.members = []creds.Credential{c}

	case routing.KindVertex:
		// Vertex is reached through the installation's service account and the
		// run's own GCP project, and neither the translation nor the serving
		// path exists in this release: CS4 lands it, and until then a Vertex
		// source serves nothing. It is skipped here rather than recorded, for
		// the reason this file's header states — there is no credential of the
		// service's bundle to name in a tried entry, and a name invented to fill
		// one would be a fact nobody checked.
		return out, nil, nil

	default:
		// A sixth kind is RT2's decoder to refuse and RT3's checks to catch, so
		// a chain that carries one is a table no side stored: it serves nothing
		// rather than widening the chain.
		return out, nil, nil
	}

	if out.pooled {
		if len(out.members) == 0 {
			return out, nil, nil
		}
		// R20: a pool's admission is its members' own. A member the repository
		// is not admitted to is skipped for it, so two repositories admitted to
		// two different accounts can share one pool and neither moves.
		//
		// The admission is the repository's own node intersected with the
		// project's (admittedAccounts), which is the same read the cascade's own
		// path makes — and it is made only here, at the first pool that has a
		// member to check.
		if *admitted == nil {
			a, err := s.admittedAccounts(ctx, id, canonical)
			if err != nil {
				return out, nil, err
			}
			*admitted = a
		}
		kept := make([]creds.Credential, 0, len(out.members))
		var skipped []Tried
		for _, c := range out.members {
			if c.Tier == creds.TierA && !(*admitted)[c.Name] {
				skipped = append(skipped, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonNotAdmitted})
				continue
			}
			kept = append(kept, c)
		}
		out.members = kept
		return out, skipped, nil
	}
	return out, nil, nil
}

// grantOf is the chain half of a call's slip, and nil for a run that carries no
// slip at all — an orchestrator's launch, or a call of a scope that never
// granted one. An empty and a nil grant are different answers (CS14): the first
// is a launcher whose grant covers nothing of the new kinds, the second is a run
// nothing granted.
func grantOf(binding *slipBinding) *creds.SlipChain {
	if binding == nil || binding.slip == nil {
		return nil
	}
	return &binding.slip.SlipChain
}

// namedPoolOf is the pool the run's launch asked for, "" when it named none.
func namedPoolOf(binding *slipBinding) string {
	if g := grantOf(binding); g != nil {
		return g.NamedPool
	}
	return ""
}

// principalOf is the run's verified launcher, as the slip recorded it (R7). A
// pool's own principal is the lookup's answer and not this one (PL4), so this
// is read only where the table's own source has to carry something and never as
// an entitlement.
func principalOf(binding *slipBinding) routing.Principal {
	if binding == nil || binding.slip == nil {
		return routing.Principal{}
	}
	return binding.slip.Launcher
}

// grantCovers reports whether the run's grant covers this source (C2's rule):
// a source is covered by its KIND and, where the kind names one, its POOL.
//
// A run with NO slip is the case the plan's own rule names — "a run with no slip
// is served the project's sources its repository is admitted to and no
// launcher's pool" — so a nil grant covers every kind but the launcher's pool,
// whose whole content is a per-run designation a run with no grant does not
// have. That is also what makes an orchestrator's launch, which carries no slip
// at all, work at all.
func grantCovers(grant *creds.SlipChain, src routing.Source) bool {
	if grant == nil {
		return src.Kind != routing.KindLauncherPool
	}
	for _, g := range grant.Sources {
		if g.Kind != string(src.Kind) {
			continue
		}
		if src.Kind == routing.KindPool {
			if g.Pool != src.Pool {
				continue
			}
		}
		return true
	}
	return false
}

// sourceNameOf is the name a source carries in the answer and in the run's
// record: the one its kind names, or "" where the kind names nobody (C1's
// SourceName, which is never a value).
func sourceNameOf(src routing.Source) string {
	switch src.Kind {
	case routing.KindPool:
		return src.Pool
	case routing.KindProvider:
		return src.Provider
	}
	return ""
}

// sourceAdmits reports whether a source of one of the three single-credential
// kinds may serve this repository (D3182, R20).
//
// An EMPTY list admits nobody, which is the fail-closed direction and the same
// rule a Claude account's own allow_data_to already has: a source with no
// repository admitted is inert rather than an error, so a project can publish
// the shape of a table before it decides its admissions. A pool carries no list:
// its admission is its members' own.
//
// The repository a table admits is spelled the way every other repository in
// the published node is (projectpolicy.Canonical, `provider/owner/name`) and not
// as the run's storage slug: a table is a document a person writes and reads,
// and the slug is derived from the identity a run token carries. It is the same
// spelling the member half of R20 compares with (projectpolicy.AdmitRepo over
// the canonical name), so one repository has one spelling across the whole of a
// call's admission.
func sourceAdmits(src routing.Source, canonical string) bool {
	for _, repo := range src.Admit {
		if repo == canonical {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// C1's answer side
// ---------------------------------------------------------------------------

// chainAnswer is C1's answer side: the three members this contract adds, written
// ONLY to a caller that sent the stage header (R24, F57) — a gateway built
// before this contract passes an answer header it does not know straight to the
// agent and decodes a refusal body strictly, so a member it has never heard of
// is either the agent reading the name of a credential source or the gateway
// failing on its own service's answer.
//
// It is one function so that the rule is stated once for every member: a later
// member added under this rule asks SentStage here and nowhere else.
func chainAnswer(hdr http.Header, cl *call, served *chainServed) {
	if served == nil || !cl.stage {
		return
	}
	if v, err := json.Marshal(served.source); err == nil {
		hdr.Set(HeaderSource, string(v))
	}
	if served.fallback {
		hdr.Set(HeaderFallback, FallbackYes)
	} else {
		hdr.Set(HeaderFallback, FallbackNo)
	}
	if served.model != "" {
		hdr.Set(HeaderModel, served.model)
	}
}
