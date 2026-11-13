package credsvc

// The credentials service's pools (docs/model-crafts/spec.md §2, reconciliation
// F7, F10, F21, F22, D154, D1622, D1626, D1627, D1634; plan task MC6): which of
// the bundle's model credentials a call may be offered once the project's crafts
// go out on a pool.
//
// **This file narrows the caller's candidate list; it does not order it.** The
// order it builds is the one the owner wrote — config.CraftNames' order for the
// crafts, the pool's listed order for its members — and what that order MEANS is
// creds.Select's own rule: tier A before tier B, and within a tier the offered
// order only breaks a tie (F7, and the sort in leastRecentlyExhausted). So a
// pool listed "key first, subscription second" is served by the subscription,
// which is the one thing a pool's listed order may not decide.
//
// **What the crafts can and cannot say to this service.** The wire carries no
// stage and no craft — a call arrives for a run and a job class, not for a
// stage (C1.10) — so the list is the UNION of the five crafts' accesses rather
// than the one craft this call is of: `implement` and `review_first` are served
// by the same loop, and a service that guessed which craft a call is of would
// answer a review stage from the coder's credential. Two consequences follow,
// and both are deliberate:
//
//   - a craft this path cannot serve (an inert pool, an OpenRouter access) is
//     skipped rather than refusing the whole call, because the union is what
//     every call of every craft is offered and one craft's refusal would take
//     the other three down with it. A narrowing that leaves NOTHING is the
//     other case and it is refused, naming every access that contributed
//     nothing (poolRefusal below);
//   - the list is narrower than the bundle for every caller, including a shared
//     job's: the crafts are the project's own declaration of which credentials
//     its calls go out on, and a caller offered more than that would be offered
//     a credential no craft names (F20, D1635's direction).
//
// **A node that writes no craft is not narrowed at all.** Every installation
// today publishes no crafts block, and its calls are served from the whole
// bundle exactly as before this task: the citing of a craft must not change
// what an installation that uses none is offered. The narrowing therefore
// starts at the crafts block and not at the pools — a pool no craft's access
// names serves nothing, however many members it lists.

import (
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// craftNodeKeyPrefix is where the published node writes a craft's keys: the
// repository's own fugaro.yaml writes agent.crafts, the node writes crafts, and
// the prefix is where a problem about an access is reported (craft.Layer).
// It is the same constant the runner resolves the node with.
const craftNodeKeyPrefix = "crafts"

// poolRefusal is why the project's crafts leave a call nothing this service can
// serve it by. It is a type of its own rather than one more error string for
// the handler's single sentence: the refusal's own sentence is what the run
// needs to read — the pool, its members, and the switch or the path that would
// let it serve — and the caller's error branch has no other case it could be
// confused with.
//
// The sentence names credentials and pools by NAME and never by value, the way
// every other refusal on this path does: it is built from the published node,
// which is the document an operator wrote, and from the names of the bundle's
// entries, which is all this service ever says about them (F27, C1.13).
type poolRefusal struct{ message string }

func (e *poolRefusal) Error() string { return e.message }

// poolOffering is the credentials the published node's crafts go out on: the
// candidate list of every caller that is not a review one, in the order the
// owner wrote it, with the bundle's tier for each name.
//
// narrowed is false when the node writes no crafts block at all, and the caller
// then offers the whole bundle. A true narrowed with an empty list and a nil
// error cannot happen: that case is the refusal.
//
// strategy is how this list is shared out among the runs that arrive, which is
// the one thing about a pool the list itself does not carry: round-robin when
// every craft that contributed goes out on a pool asking for the rotation, and
// the cascade's own rule otherwise (roundrobin.go, MC7). It is answered from
// this walk rather than from a second one, so the list and the rule it is
// served under cannot be about two different sets of crafts.
func poolOffering(p *projectpolicy.Policy, b *Bundle) (offering []creds.Credential, strategy creds.Strategy, narrowed bool, err error) {
	if p == nil || p.Crafts == nil {
		return nil, creds.StrategyFailover, false, nil
	}
	eff, _ := craft.Resolve([]craft.Layer{{
		Name:        craft.SourceProject,
		KeyPrefix:   craftNodeKeyPrefix,
		Crafts:      *p.Crafts,
		Credentials: p.CraftCredentials,
		Pools:       p.CraftPools,
	}}, subscriptionSpreadOf(p))

	held := bundleTiers(b)
	var out []creds.Credential
	var gaps []string
	var rotating, pinned int
	seen := map[string]bool{}
	for _, name := range config.CraftNames {
		e, ok := eff[name]
		if !ok {
			continue
		}
		own, gap := craftOffering(name, e, p, held)
		if gap != "" {
			gaps = append(gaps, gap)
		} else if pool, ok := namedPool(e.Access, p); ok && poolStrategy(pool) == creds.StrategyRoundRobin {
			// A craft that put a candidate in through a pool asking for the
			// rotation, counted beside the ones that put one in any other way:
			// offeringStrategy reads the two together (roundrobin.go). A craft
			// with a gap contributed nothing and is in neither count — what it
			// would have asked for is not what this call is served by.
			rotating++
		} else {
			pinned++
		}
		for _, c := range own {
			// A credential two crafts both name is one candidate: the list is a
			// set of what may serve, and checkCandidates refuses a name offered
			// twice rather than deduplicating it (a caller that listed one twice
			// would be a caller that meant something by it).
			if seen[c.Name] {
				continue
			}
			seen[c.Name] = true
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		// Every craft put nothing in, so this call has no candidate at all. The
		// refusal carries what each access was and what became of it, which is
		// the whole of what an operator needs to fix it — and it is refused
		// here rather than answered as "no credential is offered", which would
		// send the operator looking in the bundle for a fault that is in the
		// crafts.
		return nil, creds.StrategyFailover, true, &poolRefusal{message: poolRefusalHead + strings.Join(gaps, "; ")}
	}
	return out, offeringStrategy(rotating, pinned), true, nil
}

// poolRefusalHead is what every crafts refusal begins with.
const poolRefusalHead = "the project's crafts leave this call nothing the credentials service can serve it by: "

// poolSkipSubscription is what a pool's Claude OAuth member is recorded as
// while the project's subscription_spread switch is off (D1626). It is the
// runner's own sentence for the same skip, word for word
// (internal/runner's poolSkipSubscription), because the two paths answer one
// pool: a run whose credential this service serves reads the same reason in the
// run's log and in a refusal, and an operator who meets one of them meets the
// same words.
const poolSkipSubscription = "the project's subscription_spread switch is off, so this subscription is not spread"

// craftOffering is ONE craft's contribution to the list: the credentials its
// access names, in the pool's listed order, and — when it contributes nothing —
// the clause that says why. The two are exclusive.
//
// A craft's access names a pool, a credential, or nothing any layer holds; the
// first of the three decides, and the third is a node the publish path refuses
// and a hand-edit can still write, so it is answered rather than assumed away.
func craftOffering(name string, e craft.Effective, p *projectpolicy.Policy, held map[string]creds.Tier) ([]creds.Credential, string) {
	if e.Inert {
		// D1622: a pool of two or more Claude OAuth subscriptions serves nothing
		// while the owner's subscription-spread switch is off, and this is the
		// path's half of the rule the runner's checkStagePool states on its own.
		pool, _ := namedPool(e.Access, p)
		return nil, fmt.Sprintf("the %s craft's access names the pool %q, which serves nothing yet: it lists %s, and Fugaro spreads a run across subscriptions only once the project's subscription_spread switch is on (it is off)",
			name, e.Access, oauthMemberText(pool, p.CraftCredentials))
	}
	if pool, ok := namedPool(e.Access, p); ok {
		out := poolOfferingOf(pool, p, held)
		if len(out) == 0 {
			return nil, fmt.Sprintf("the %s craft's access names the pool %q, and this service can serve none of its members: %s",
				name, pool.Name, memberClauses(pool, p, held))
		}
		return out, ""
	}
	typ := nodeCredentialType(e.Access, p)
	if typ == "" {
		return nil, fmt.Sprintf("the %s craft's access names %q, which no layer this service reads holds", name, e.Access)
	}
	if config.CredentialServingPath(typ) != config.CredentialPathCredsvc {
		return nil, fmt.Sprintf("the %s craft's access names the credential %q (%s), which the runner path serves", name, e.Access, runnerPathWhy(typ))
	}
	tier, ok := held[e.Access]
	if !ok {
		return nil, fmt.Sprintf("the %s craft's access names the credential %q (%s), which this service's bundle does not hold", name, e.Access, typ)
	}
	if tier != creds.TierA && tier != creds.TierB {
		// A name the bundle holds as something this path does not select as a
		// model credential — the provider key is the one that exists (AK6b).
		// Offering it to a coding caller would refuse the caller's whole call
		// (checkCandidates refuses tier C), which is the one outcome a
		// configuration mistake must not reach.
		return nil, fmt.Sprintf("the %s craft's access names %q, which this service holds as a credential it does not select for a craft's calls", name, e.Access)
	}
	return []creds.Credential{{Name: e.Access, Tier: tier}}, ""
}

// poolOfferingOf is one pool's members that this service can serve, in the
// pool's listed order: the members whose credential this path serves (tier A or
// B, D1634) and whose value this service's bundle holds.
//
// A member the bundle does not hold is a member this service cannot serve,
// whatever the pool meant by it: the OAuth accounts and the Anthropic key are
// the bundle's (bundle.go), and a name that is not in it is a credential the
// job that would spend it holds instead — the runner path's shape.
//
// **A Claude OAuth member is served only once the subscription switch is on**
// (D1622, D1626, D1636). The resolver calls a pool of two or more such members
// inert, and this is the other half of the same ruling on this path: inside a
// pool that also holds another kind each subscription member is stepped over
// while the switch is off, which is the skip the runner's poolMember makes from
// its side (poolSkipSubscription) — the two paths answer one pool, so a mixed
// pool must not be spread by this one alone.
func poolOfferingOf(pool config.Pool, p *projectpolicy.Policy, held map[string]creds.Tier) []creds.Credential {
	spread := subscriptionSpreadOf(p)
	var out []creds.Credential
	for _, m := range config.PoolMembers(pool, p.CraftCredentials) {
		if m.Path != config.CredentialPathCredsvc {
			continue
		}
		if m.Type == config.CredentialTypeClaudeOAuth && !spread {
			continue
		}
		tier, ok := held[m.Name]
		if !ok || (tier != creds.TierA && tier != creds.TierB) {
			continue
		}
		out = append(out, creds.Credential{Name: m.Name, Tier: tier})
	}
	return out
}

// memberClauses is what became of each of a pool's members, one clause each, in
// the pool's listed order: the order the owner reads his own pool in.
func memberClauses(pool config.Pool, p *projectpolicy.Policy, held map[string]creds.Tier) string {
	parts := make([]string, 0, len(pool.Members))
	for _, m := range config.PoolMembers(pool, p.CraftCredentials) {
		switch {
		case m.Path == config.CredentialPathRunner:
			parts = append(parts, fmt.Sprintf("%s (%s)", m.Name, runnerPathWhy(m.Type)))
		case m.Path == "":
			parts = append(parts, fmt.Sprintf("%s (no path this release serves a credential of type %q on)", m.Name, m.Type))
		default:
			if m.Type == config.CredentialTypeClaudeOAuth && !subscriptionSpreadOf(p) {
				parts = append(parts, fmt.Sprintf("%s (%s)", m.Name, poolSkipSubscription))
				continue
			}
			if _, ok := held[m.Name]; !ok {
				parts = append(parts, fmt.Sprintf("%s (%s, which this service's bundle does not hold)", m.Name, m.Type))
				continue
			}
			parts = append(parts, fmt.Sprintf("%s (this service holds it as a credential it does not select for a craft's calls)", m.Name))
		}
	}
	if len(parts) == 0 {
		return "the pool holds no member"
	}
	return strings.Join(parts, ", ")
}

// runnerPathWhy is why a member or an access of that credential type is not
// this path's, in one clause: the same two reasons the runner's own pool walk
// states from its side (F7, D1634, D1624).
func runnerPathWhy(typ string) string {
	if typ == config.CredentialTypeVertex {
		return "the built-in Vertex credential, whose calls are the project's own GCP service and are served where the run authenticates as vertex"
	}
	return "its key is mounted in the job that spends it, so the run's own path serves it"
}

// oauthMemberText is how many of a pool's members are Claude OAuth
// subscriptions, which is what makes it inert (D1622). It counts the pool's own
// layer's types, the rule craft.Resolve's inert applies.
func oauthMemberText(pool config.Pool, creds []config.Credential) string {
	n := 0
	for _, m := range config.PoolMembers(pool, creds) {
		if m.Type == config.CredentialTypeClaudeOAuth {
			n++
		}
	}
	return fmt.Sprintf("%d Claude OAuth subscription credentials", n)
}

// namedPool is the pool of that name the published node holds, which is the
// only document this service reads credentials and pools from: a repository
// cannot create one (F12, D1628) and this service has no other layer.
func namedPool(name string, p *projectpolicy.Policy) (config.Pool, bool) {
	for _, pool := range p.CraftPools {
		if pool.Name == name {
			return pool, true
		}
	}
	return config.Pool{}, false
}

// nodeCredentialType is the type of the credential of that name the node holds,
// then the built-ins: config.PoolMembers answers this for a pool's members, and
// a craft that names a credential directly needs the same answer.
func nodeCredentialType(name string, p *projectpolicy.Policy) string {
	for _, c := range p.CraftCredentials {
		if c.Name == name {
			return c.Type
		}
	}
	for _, c := range config.BuiltinCredentials() {
		if c.Name == name {
			return c.Type
		}
	}
	return ""
}

// bundleTiers is the bundle's credentials by name: the tiers this service's own
// accessor answers (b.Credentials, which excludes the App key, the git tokens
// and the signing keys), so a name is offered with the tier the candidate list
// must carry and a name the bundle does not hold is absent.
//
// The provider key is added beside Credentials rather than through it, and this
// map is the only place the two meet: a craft may name that credential (AK6b),
// and the answer to it must be what this service HOLDS it as — a tier-C
// credential this path does not select for a craft's calls — rather than that
// the bundle holds nothing under the name. Reading it here is not offering it:
// every candidate this file builds refuses a tier outside A and B, which is the
// exclusion Credentials makes for the sharper reason that a tier-C candidate
// would refuse the caller's whole call (checkCandidates).
func bundleTiers(b *Bundle) map[string]creds.Tier {
	all := b.Credentials()
	out := make(map[string]creds.Tier, len(all)+1)
	for _, c := range all {
		out[c.Name] = c.Tier
	}
	if e, ok := b.ProviderKey(); ok {
		// A name a model credential already answers keeps that credential: an
		// account may legally be named "provider-key"
		// (claude-oauth-token-provider-key), and the name a craft's access
		// spells is then the account's — the value the cascade would select,
		// Lookup answering model credentials — rather than the provider
		// entry's, which is never selected by a caller of this list.
		if _, taken := out[e.Name]; !taken {
			out[e.Name] = e.Tier
		}
	}
	return out
}

// subscriptionSpreadOf is the published node's subscription-spread switch
// (D1622, D1636): off unless the node says true.
func subscriptionSpreadOf(p *projectpolicy.Policy) bool {
	return p.SubscriptionSpread != nil && *p.SubscriptionSpread
}

// named is whether the offering holds a credential of that name, which is what
// tells a bundle credential the pool names from one it does not.
func named(offer []creds.Credential, name string) bool {
	for _, c := range offer {
		if c.Name == name {
			return true
		}
	}
	return false
}
