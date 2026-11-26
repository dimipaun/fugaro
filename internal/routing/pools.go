// The pools contract (C2): what a pool is made of now, who designates one for a
// project, and the two lookups the credentials service calls to serve a run's
// calls from one.
//
// A pool is a set of credentials a call may be served from. What this file fixes
// is the handful of names every side of that has to agree on, and nothing that
// computes — no picker, no store, no policy read, no command:
//
//   - the rule a pool is served by (R5, D3177): `ordered`, `round-robin`, or
//     `failover`, which is what a pool that names neither a rule nor a strategy
//     is served by, as it is today;
//   - the principal a pool belongs to (D3181): a person, a service account, or
//     the project itself;
//   - the optional threshold a pool may carry (D3178, PL8);
//   - the designation record (R26, PL4): one principal's answer to "which pool
//     serves my runs in this project";
//   - the lookup the service calls for "the launcher's pool" (`LauncherPool`,
//     PL4) and the picker it calls inside a pool (`creds.PickInPool`, PL3), in
//     that order, off the answer to the first (CS1);
//   - the JSON of `fugaro accounts pool ls --json` (PL2, PL10, VS2).
//
// The sides that compute are written against these names: PL1 puts a rule, a
// principal and a threshold in the config, PL2 and PL10 print the document
// below, PL3 picks inside a pool by its rule, PL4 answers the lookup, CS1 walks
// a chain, CS14 records the grant, MG1 migrates an installation and RT7 reports
// doctor's findings. A side that finds this contract wrong or incomplete raises
// a change request rather than working around it
// ([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)).
//
// The document this file belongs to is
// [docs/model-credentials-routing/contract-pools.md](../../docs/model-credentials-routing/contract-pools.md),
// and it is where the rules a reader outside Go needs — who may write a
// designation, what a pool's figures mean, what a personal pool holds — are
// stated.
//
// # Why these names live here, and why nothing of this tree is imported
//
// A pool's rule and principal are read by the config that decodes them
// (internal/config, PL1), the selector that serves them (internal/creds, PL3),
// the service that resolves them (internal/credsvc, PL4, CS1) and the commands
// that print them (internal/cli, PL2, PL5, PL9), so one package has to hold them
// and every one of those can import it. What that package must not do is import
// any of them back: a cycle is one line away otherwise — internal/creds already
// imports both this package and internal/config, and internal/config is where a
// pool's own members are written (PL1) — so this file imports no package of
// this tree at all, and a later member that needs one belongs in the package
// that reads it rather than here.
package routing

import (
	"context"
	"encoding/json"
	"time"
)

// Rule is how the service picks among the usable members of one pool (R5).
//
// It is a string and the three values below are the whole vocabulary, because a
// rule is published in the installation's policy and read back by a release that
// may be older than the writer: `rule` is a NEW member of a pool (R21), so a
// pool that carries none is served by the `strategy` it was written with, and by
// `failover` only where it names neither (RuleOrDefault) — a value never changes
// meaning, and no pool written before a rule existed moves because one arrived.
//
// `round-robin` and `failover` keep their meanings (D3177): the second is the
// credential exhausted longest ago, the round-robin counter breaking an exact
// tie, which is what `internal/creds`' Selection already does within a tier.
// `ordered` is the new one: the first listed member that is usable, and no
// preference beyond the order its owner wrote the pool in.
//
// `failover` is NOT the zero value by accident: like Strategy in internal/creds
// it is the default a caller gets without saying anything, and RuleOrDefault is
// the one place that reads "" as it — after the pool's own `strategy`, which
// keeps its meaning (PL1, R21).
type Rule string

const (
	// RuleOrdered is the first listed member that is usable: the pool is walked
	// in the order its owner wrote it and no member is preferred over an
	// earlier one (R5).
	RuleOrdered Rule = "ordered"
	// RuleRoundRobin steps through the usable members in the listed order, one
	// run at a time, and skips the ones a mark or the cap holds.
	RuleRoundRobin Rule = "round-robin"
	// RuleFailover takes the member used least recently — the one exhausted
	// longest ago — with the round-robin counter breaking an exact tie, which is
	// what internal/creds' leastRecentlyExhausted already does. It is what a pool
	// that names neither a rule nor a strategy does, and what every pool did
	// before rules existed.
	RuleFailover Rule = "failover"
)

// Rules are the rules a pool may be written with, so that a validator renders
// one message from one list (internal/config's PoolStrategies is the same idea
// for the member that carried the rule until now).
var Rules = []Rule{RuleOrdered, RuleRoundRobin, RuleFailover}

// Valid reports whether r is a rule this release serves pools by: one of Rules,
// or "" (which is read as failover rather than refused).
func (r Rule) Valid() bool {
	switch r {
	case "", RuleOrdered, RuleRoundRobin, RuleFailover:
		return true
	}
	return false
}

// RuleOrDefault is the rule a pool is served by: the `rule` it names, or, where
// it names none, the `strategy` it was written with, and failover where it names
// neither.
//
// The middle step is what keeps `strategy` meaning what it means (PL1, R21): a
// pool published with `strategy: round-robin` and no `rule` is served
// round-robin, exactly as it is today. So `strategy` is read rather than
// reinterpreted, and a pool moves because a rule arrived only where its owner
// wrote one.
//
// `strategy` is a plain string rather than config's own type, because this file
// imports no package of this tree; the caller holding the pool passes the member
// it read. The two values it carries are config's own PoolStrategies
// (`failover`, `round-robin`), named in the switch below for the same reason.
// `ordered` is not among them: it arrived with `rule`, so a pool that wants it
// writes `rule`, and a strategy of anything else reads as failover — which is
// config's own StrategyOrDefault read one step further.
//
// Its answer is never "": whoever fills a PoolRef.Rule reads it through this, so
// a picker acts on one resolved rule and never re-reads the pool's
// configuration.
func (r Rule) RuleOrDefault(strategy string) Rule {
	if r != "" {
		return r
	}
	switch s := Rule(strategy); s {
	case RuleRoundRobin, RuleFailover:
		return s
	}
	return RuleFailover
}

// PrincipalKind is what a pool's principal is (D3181, D3179): a person, a
// service account, or the project itself (a shared pool, D3183).
type PrincipalKind string

const (
	// PrincipalPerson is one person: an id is their Google subject, the stable
	// numeric account id (`sub`) the secret-name grammar and the intake are
	// already keyed on. It is what a slip records as its owner, so a person's
	// pool is resolved from their own runs and nobody else's (R7, D3179).
	PrincipalPerson PrincipalKind = "person"
	// PrincipalServiceAccount is a verified service account: an id is the
	// account's own address, the one its runs' tokens carry
	// (credsvc.VerifyServiceAccountLauncher). Such a run's pool is a project
	// pool an admin fills (D3181), and it never draws on a person's pool
	// (D2976), except for the one case D3191 adds: a run that resumes a person's
	// paused run draws on that person's pool.
	PrincipalServiceAccount PrincipalKind = "service-account"
	// PrincipalProject is the project itself: the shared pool, which every
	// account the migration wrapped became (MG1, D3183) and which takes the
	// tokens people contribute to it deliberately (D3194).
	PrincipalProject PrincipalKind = "project"
)

// PrincipalKinds are the kinds a pool's principal may be written with, so that a
// validator renders one message from one list.
var PrincipalKinds = []PrincipalKind{PrincipalPerson, PrincipalServiceAccount, PrincipalProject}

// Principal is who a pool belongs to.
//
// It is an IDENTITY and never display text: a person's is the Google subject the
// service verified, not the text a launch wrote about itself, which a caller
// writes and which no reader may trust as an identity (R7). The pool a run is
// served from follows the verified principal on the run's slip, so a forged
// requester gets nothing (F51's accepted risk is the project's own database
// admin, who can forge the record itself — not a job's word about itself).
// The launcher's own text — the field R7 names beside the principal, and the
// one of the two with a guard test of its own — is named in
// [contract-pools.md](../../docs/model-credentials-routing/contract-pools.md)
// and deliberately not here: that guard fails a Go file of this module which
// names it, which is what keeps it out of every decision.
// Its json tags are the wire's own (the slip a launcher and a service exchange,
// internal/creds): the config's `pools:` block decodes config.PoolPrincipal, not
// this type (PL1), so nothing here is written to or read from a yaml document.
type Principal struct {
	// Kind is one of PrincipalKinds.
	Kind PrincipalKind `json:"kind"`
	// ID is the principal's own identifier: a person's Google subject, a service
	// account's address, or the project's name. It is a name and never a value:
	// no credential's secret is ever here, and none ever may be.
	ID string `json:"id"`
}

// Valid reports whether p names a principal: a kind this release knows and a
// non-empty id. An empty id is refused rather than read as "no principal": a
// pool whose principal is missing is a pool nothing can resolve, which is a
// written mistake (PL1 refuses it) and never a state a serve path may meet.
func (p Principal) Valid() bool {
	for _, k := range PrincipalKinds {
		if p.Kind == k {
			return p.ID != ""
		}
	}
	return false
}

// Threshold is a pool's optional usage threshold (D3178, PL8): the fraction of a
// member's limit at which the pool moves off that member, before the limit is
// hit rather than after it.
//
// It is OPTIONAL, and the absence of one is a nil *Threshold wherever a pool is
// carried (the config's member is optional and so is the pool document's): a
// pool with no threshold does nothing beyond its rule, and no default is
// invented for it.
//
// The bounds are the contract's answer to what the plan left as "between 0 and
// 1": greater than 0, which refuses a pool that would move off every member
// always — that pool serves nothing and is a written mistake rather than a rule
// — and at most 1, which admits a threshold that fires exactly at the limit. The
// validation (PL1) and the picker (PL8) read this one answer, so a value the
// config accepts is a value the picker acts on.
//
// What a threshold needs to work is a utilisation figure the provider reported
// for the member: with none the rule does nothing and says so once in the audit
// line (PL8). It is never guessed (D3178, U1).
type Threshold float64

// Valid reports whether t is a threshold this release admits: greater than 0 and
// at most 1, which is the bound PL1's validation and PL8's picker share.
func (t Threshold) Valid() bool { return t > 0 && t <= 1 }

// Designation is one principal's designation for one project (R26): the pool
// that principal's own runs are served from, and the record the service's lookup
// reads (PL4).
//
// It is the principal's own statement about itself, keyed by the principal — a
// person's Google subject, a service account's address — and it is written only
// through a route of the service that verifies the caller and refuses a pool the
// caller does not own (PL4, PL5). The database rules cannot hold that (F61):
// people reach the database by IAM, which bypasses the rules, so the route is
// the only writer that matters and the store is otherwise closed.
//
// What the record holds is display data: every launcher of the project can read
// the designations (and the pool figures beside them) whether the rules admit
// them or not, so nothing here is ever read as an authorization input by
// anything but the service — and the service reads it against the principal it
// verified on the run's slip, never against what a job said about itself (R7,
// R26).
type Designation struct {
	// Principal is whose runs this designation serves. It is the principal's own
	// verified id: a designation is never written in another principal's name.
	Principal Principal `json:"principal"`
	// Project is the project the designated pool belongs to. A pool is one
	// project's — a personal pool's members are that project's per-user
	// containers (D3179 as amended, F55) — so a designation answers for one
	// project and no other.
	Project string `json:"project"`
	// Pool is the pool's name, the one its owner wrote and the one a launch may
	// name with `--pool`.
	Pool string `json:"pool"`
}

// PoolRef is a pool a run may be served from, as the lookup answers it.
type PoolRef struct {
	// Project is the project the pool belongs to.
	Project string `json:"project"`
	// Pool is the pool's name.
	Pool string `json:"pool"`
	// Rule is how the service picks among the pool's usable members, RESOLVED:
	// never "". Whoever fills this reference reads the pool's own `rule` and,
	// where it names none, the `strategy` it was written with (Rule.RuleOrDefault),
	// so the picker acts on one answer and reads the pool's configuration no
	// second time (PL3, PL4).
	Rule Rule `json:"rule"`
	// Threshold is the pool's usage threshold, null when it names none: the
	// same optional member the pool carries in the config, read back out of it
	// by whoever resolves this reference (PL4). It is here rather than left to
	// the picker to fetch because the two settings a rule acts on — how the
	// members are picked and when one is moved off — travel with the pool the
	// lookup answers, so one read of the pool's config serves the whole serve
	// path (PL3, PL8).
	Threshold *Threshold `json:"threshold"`
	// Principal is whose members this pool holds: a person's per-user containers
	// in the project (D3179 as amended), a service account's project pool
	// (D3181), or the project's own shared pool (D3183, D3194).
	//
	// It is the POOL's principal and not necessarily the caller's, and the two
	// are separate facts on purpose: a run launched with `--pool` may name a
	// shared pool that belongs to the project, and a run that resumes a paused
	// one draws on the pool its original launch named (D3191). Who may be served
	// from a pool is the lookup's decision (PL4, D3181) and never a property of
	// this record.
	Principal Principal `json:"principal"`
}

// LauncherPool answers the pool one run's launcher is served from for one
// project: the pool that launcher's designation names, or, where the launch
// named a pool with `--pool`, that pool (D3176).
//
// principal is the VERIFIED principal on the run's slip (R7): a person's Google
// subject, or a verified service account (PL6). It is never the text a launch
// wrote about itself, and a caller that passed one would be trusting a job's
// word about itself.
//
// named is the pool a run asked for with `--pool`, or "" when the launch named
// none (D3176). The implementation checks the launcher's entitlement to a named
// pool and answers `found` false where the launcher owns it and is allowed on it
// neither, so naming a pool never widens what a run may be served from. It never
// changes the chain: the flag says WHICH pool "the launcher's pool" resolves to
// for this run, not that the chain's order moves.
//
// found false is "no pool designated" and it is not an error: no designation, a
// pool that is empty, and a named pool the launcher is not entitled to all
// answer it (D3180, PL4), and the walk moves on to the chain's next source with
// the step flagged a fallback. An error is a store that could not be read and is
// never "none": a caller that read it as an absent designation would serve a
// fallback source where a pool exists.
//
// The stub this contract ships is internal/credsvc/launcherpool_stub.go, whose
// every answer is found false. PL4 pointed the service at the real lookup — it
// assigns it in credsvc.NewServer, not from an init — and the stub stays for a
// process that has built no server; its deletion is I1's (launcherpool_stub.go).
type LauncherPool interface {
	LauncherPool(ctx context.Context, principal Principal, named string) (PoolRef, bool, error)
}

// The `fugaro accounts pool ls --json` document (PL2), which the console's pools
// screen draws (VS2) and whose figures PL10 counts. Its members are named here
// rather than in the command, because three sides print, fill and read it and
// the command lands before the figures do: PL2 prints the document with no
// figure in it, PL10 fills the usage and the headroom, and VS2 draws whichever
// are there.
//
// It carries NAMES AND FIGURES AND NEVER A VALUE (F19): a member is the name of
// a container — a project's, or a person's own — and its state, its usage and
// what is left of it. No token, no key and no secret appears anywhere in it, for
// anyone, an admin included.
type (
	// PoolListDoc is the whole document: the project's pools and their members.
	PoolListDoc struct {
		// Project is the project whose pools these are. Every pool of the
		// document belongs to it.
		Project string `json:"project"`
		// Pools are the project's pools. An empty list is a fact — the project
		// has no pool — and never a null.
		Pools []PoolEntry `json:"pools"`
	}

	// PoolEntry is one pool and who has designated it.
	PoolEntry struct {
		// Name is the pool's name.
		Name string `json:"name"`
		// Rule is how the service picks among the pool's members (R5), as the
		// pool's owner wrote it: "" where the pool names none, which is read as
		// the `strategy` the pool was written with and as failover only where it
		// names neither (Rule.RuleOrDefault). That is the one resolution every
		// pool's rule gets, so a reader of this member never invents a rule of
		// its own and a pool is never read as one its owner did not write. This
		// document carries the pool and not its strategy, so the resolution
		// happens where a pool is read with its config (PL1) and never here.
		Rule Rule `json:"rule"`
		// Principal is whose pool it is (D3181).
		Principal Principal `json:"principal"`
		// Threshold is the pool's usage threshold, null when it names none
		// (D3178).
		Threshold *Threshold `json:"threshold"`
		// Members are the pool's members in the order its owner listed them,
		// which is the order `ordered` walks and the order a round robin turns
		// in.
		Members []PoolMember `json:"members"`
	}

	// PoolMember is one member of a pool: what holds it, what it has spent, and
	// what is left of it.
	PoolMember struct {
		// Name is the member's name — the container's, in the project's or the
		// principal's own namespace — and never its value (F19).
		Name string `json:"name"`
		// State is what holds the member, in internal/creds' own words, which
		// `fugaro accounts ls --json` already prints: "available" for a member
		// nothing holds, and otherwise the mark's state (`creds.State`).
		State string `json:"state"`
		// NextReset is when the member becomes usable again — a mark's own
		// instant, or the time its leases lapse — and null when nothing holds
		// it. It is the same instant a chain's exhausted answer is built from.
		NextReset *time.Time `json:"next_reset"`
		// Usage is what the member has spent in the window the service counts
		// (PL10), and null where the service counts nothing for it.
		Usage *PoolUsage `json:"usage"`
		// Headroom is the fraction of the member's limit that is left, where the
		// provider reported a utilisation figure, and null otherwise: it is
		// never guessed (D3178, PL10). It is the figure the threshold rule moves
		// a pool off a member by (PL8), read back as display.
		Headroom *float64 `json:"headroom"`
	}

	// PoolUsage is what one member has spent in the current window (PL10).
	PoolUsage struct {
		// Calls is how many calls the member has served in the window.
		Calls int64 `json:"calls"`
		// Tokens is how many tokens those calls spent. It is a count and not a
		// price: what a call cost is the ledger's, and a subscription token's
		// calls cost dollars to nobody.
		Tokens int64 `json:"tokens"`
	}
)

// MarshalJSON writes the document with its pools a list whatever the caller
// built in memory: a PoolListDoc whose Pools was never filled is the project
// with no pool, and the document says that with an empty array and never with a
// null — a reader that met a null would have to decide for itself whether the
// project has no pool or the writer forgot to say, which is the one thing this
// member's own comment promises it never has to. It is the same invariant the
// documents of this tree keep at their construction sites, kept here instead
// because three sides build this one and the first of them (PL2) prints it
// before any of its figures exist.
func (d PoolListDoc) MarshalJSON() ([]byte, error) {
	type plain PoolListDoc // no methods: every field and tag is written as declared
	out := plain(d)
	if out.Pools == nil {
		out.Pools = []PoolEntry{}
	}
	return json.Marshal(out)
}
