package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"slices"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The managed gate as `fugaro project policy publish` reads it (M3, D1012,
// spec §4.2).
//
// Why this command is in the set, and why it is the command whose rules have
// to be written rather than assumed. The node it writes is what every run of
// the project is admitted by: allow_repos decides which repositories may run
// at all, a profile's allow_repos decides which repositories the job it
// selects serves, the accounts block decides which repositories may hold which
// token, the routing block decides where a Claude call goes, and the budget
// block is the ceiling a run is held to. So the step is the shape the gate
// exists for — a document the command builds before it writes, whose whole
// effect is visible in that document — and it is also the shape that can
// WIDEN something, which is why §4.2's rule for it is a list of things that
// must not change rather than a plan a person reads: repos unchanged or
// narrower, routing unchanged, the credentials block unchanged, and --delete,
// --seed-repos and --remove-routing never admitted at all.
//
// The rules are read over the node the publish WOULD write and the node it
// replaces, never over the command line or the flags (spec §4.1 rule 2): a
// rule that read the arguments could be satisfied by spelling one change
// another way, and --seed-repos is in the list precisely because "the change,
// spelled as a flag" is what a widening is.
//
// What this file does NOT do is decide whether the session may go on. That is
// initflow's own rule — CanConfirmManaged, asked with the answer these rules
// produced — because a command that restated D55's rule would be a second copy
// of it to drift from, and the second copy is the one that would widen a
// confirmation instead of narrowing it (D1026). Nor does it read the marks:
// that is the one helper every command in the set calls (managed_gate.go,
// M6, D1015/D1017/D1024/D1025), and a command that re-implemented it would be
// the drift that helper exists to prevent.

// policyPublishClass is the class this step's confirmation is. It is
// YesOrTyped and not Typed: a Typed step is money or permanent, neither of
// which a policy node is — the deletion is --yes-coverable and the publish
// itself asks nothing at all — and a Typed class is one no plan gate may admit
// (D1026). It is one constant for both doors of this command so the class
// cannot differ between a publish and a deletion.
const policyPublishClass = initflow.YesOrTyped

// The rules, in the words the refusal and the audit record carry. Each is a
// constant because the two readers grep for the same text: the session reads
// the rule that refused (D1023) and the owner reads it again, months later, in
// the record of a run they did not watch (D1014). Each names where it is
// written down, so an owner comparing a refusal against the design finds the
// table it came from.
const (
	// policyGenerationRule is §4.2's "a node under the deployed rules
	// generation". The deployed half is requirePolicyRules, asked before the
	// gate and unchanged today; what this rule adds is the node's OWN
	// declaration, so a publish that would write a node of another generation
	// is refused rather than judged by this one's rules.
	policyGenerationRule = "the node states the generation this build writes (spec §4.2)"
	// policyReposRule is §4.2's "repos unchanged or narrower".
	policyReposRule = "repos is unchanged or narrower (spec §4.2)"
	// policyProfileRule, policyAccountsRule and policyBitbucketRule are §4.1
	// rule 5's "no widening of an allow list", once per list the node carries.
	// They are separate rules rather than one because the refusal has to say
	// WHICH list grew: a profile's scope, an account's repositories and the
	// bitbucket token's reach are three different things a widening grants.
	policyProfileRule   = "no profile admits a repository it did not admit before (spec §4.1 rule 5)"
	policyAccountsRule  = "no account admits a repository it did not admit before (spec §4.1 rule 5)"
	policyBitbucketRule = "no bitbucket token entry admits a repository it did not admit before (spec §4.1 rule 5)"
	// policyRoutingRule and policyCredentialsRule are §4.2's other two
	// unchangeable members: where a model call goes, and how the cascade that
	// may pay for one behaves.
	policyRoutingRule     = "routing is unchanged (spec §4.2)"
	policyCredentialsRule = "the credentials block is unchanged (spec §4.2)"
	// policyCapRule is §4.1 rule 6: no increase of a budget cap, whichever
	// command it is.
	policyCapRule = "no budget cap is raised (spec §4.1 rule 6)"
	// The three flags §4.2 names as never admitted. They are rules and not a
	// check on the arguments for the reason the rules above are read off the
	// node: what is refused is the effect, and the effect of --seed-repos is an
	// allow-list that grows.
	policyDeleteRule      = "--delete is never admitted (spec §4.2)"
	policySeedReposRule   = "--seed-repos is never admitted (spec §4.2)"
	policyRoutingFlagRule = "--remove-routing is never admitted (spec §4.2)"
)

// ruleSaid is one rule's refusal: the rule first, in its own words, and then
// what fired. One function so the sentence the session reads and the record
// the owner keeps carry the rule in the same position, which is what lets an
// owner grep a refusal out of a day of records.
func ruleSaid(rule, detail string) string { return rule + ": " + detail }

// policyPublishVerdict is the gate's verdict over the node a publish would
// write: every rule read over the exact bytes that would be written, against
// the node those bytes replace. It is built here rather than in project_cmd.go
// so the rules sit beside their own words, and it is built BEFORE the first
// write and before any argument of a write is prepared (D1021).
//
// An unreadable or undecodable document never reaches this function: the
// command's own reads refuse it first (readPolicyNode, refuseNewerPolicy,
// publishedPolicyOf), which is the fail-closed direction the gate's own type
// documents — "no verdict" must never read as "admitted".
func policyPublishVerdict(old, pol *projectpolicy.Policy, body []byte, seedRepos, removeRouting bool) managedmode.Converge {
	var passed, refused []string
	judge := func(rule, refusal string) {
		if refusal == "" {
			passed = append(passed, rule)
			return
		}
		refused = append(refused, refusal)
	}
	judge(policyGenerationRule, policyGenerationRefusal(pol))
	judge(policyReposRule, policyReposRefusal(old, pol))
	judge(policyProfileRule, policyProfileRefusal(old, pol))
	judge(policyAccountsRule, policyAccountsRefusal(old, pol))
	judge(policyBitbucketRule, policyBitbucketRefusal(old, pol))
	judge(policyRoutingRule, policyRoutingRefusal(old, pol))
	judge(policyCredentialsRule, policyCredentialsRefusal(old, pol))
	judge(policyCapRule, policyCapRefusal(old, pol))
	if seedRepos {
		refused = append(refused, ruleSaid(policySeedReposRule, "the flag re-seeds allow_repos from this machine's own repositories and the shared config, so the list it writes is the list this machine happens to hold: a publish never re-adds a name an operator removed"))
	}
	if removeRouting {
		refused = append(refused, ruleSaid(policyRoutingFlagRule, "the flag drops the published routing block, and that block is where a project's Claude calls are sent"))
	}
	if len(refused) > 0 {
		return managedmode.ConvergeRefused(body, policyPublishClass, refused)
	}
	return managedmode.ConvergeAdmitted(body, policyPublishClass, passed)
}

// policyDeleteVerdict is the gate's verdict over a removal. It is refused by
// construction: a removal changes what every run of the project is admitted by
// exactly as much as a publish does, and it is the owner's own decision. The
// document it names is the node AS IT WAS READ — the one the deletion would
// remove — because that is the document an owner comparing a record against
// the database is looking at (D1022's digest is over the bytes the gate read).
func policyDeleteVerdict(raw []byte) managedmode.Converge {
	return managedmode.ConvergeRefused(raw, policyPublishClass, []string{
		ruleSaid(policyDeleteRule, "the published node is what every run of the project is admitted by, so removing it is the owner's own decision and not one a plan can stand in for"),
	})
}

// policyGenerationRefusal is the node's own generation, which is the half of
// §4.2's rule this side can see.
func policyGenerationRefusal(pol *projectpolicy.Policy) string {
	if pol.Version == projectpolicy.Version {
		return ""
	}
	return ruleSaid(policyGenerationRule, fmt.Sprintf("the node is version %d and this build writes version %d", pol.Version, projectpolicy.Version))
}

// policyReposRefusal is §4.2's rule over the node's own allow-list. A publish
// with no node to replace has no "narrower" answer at all: nothing admitted
// before, everything the list names admitted after, so it is refused — the
// same fail-closed answer --seed-repos gets, and for the same reason, since a
// first publish IS a seed.
func policyReposRefusal(old, pol *projectpolicy.Policy) string {
	if old == nil {
		return ruleSaid(policyReposRule, "there is no published node for this list to be unchanged or narrower than, so every repository it names would be admitted where none was")
	}
	if e := widenedAllowList(old.AllowRepos, pol.AllowRepos); e != "" {
		return ruleSaid(policyReposRule, fmt.Sprintf("%s is admitted by no entry of the published list", e))
	}
	return ""
}

// policyProfileRefusal is §4.1 rule 5 over the profiles block, which is the
// example the rule itself names: a profile that admits one more repository
// than it did is one more repository whose runs hold whatever that profile
// mounts. A profile the published node does not define at all is refused by
// the same answer — everything it admits is newly admitted.
func policyProfileRefusal(old, pol *projectpolicy.Policy) string {
	for _, name := range slices.Sorted(maps.Keys(pol.Profiles)) {
		if e := widenedAllowList(profileAllowRepos(old, name), pol.Profiles[name].AllowRepos); e != "" {
			return ruleSaid(policyProfileRule, fmt.Sprintf("profiles.%s admits %s, which the published block did not", name, e))
		}
	}
	return ""
}

// policyAccountsRefusal is the same rule over the accounts block: it is what
// bounds the token a run of that account's job holds, so one more repository
// in an account's list is one more repository that token may be used by.
func policyAccountsRefusal(old, pol *projectpolicy.Policy) string {
	for _, name := range slices.Sorted(maps.Keys(pol.Accounts)) {
		if e := widenedAllowList(accountAllowRepos(old, name), pol.Accounts[name].AllowRepos); e != "" {
			return ruleSaid(policyAccountsRule, fmt.Sprintf("accounts.%s admits %s, which the published block did not", name, e))
		}
	}
	return ""
}

// policyBitbucketRefusal is the same rule over the bitbucket token's block:
// the block is the token's whole reach, so an entry that grew is a credential
// that reaches one more workspace or repository.
func policyBitbucketRefusal(old, pol *projectpolicy.Policy) string {
	if e := widenedAllowList(bitbucketAllowRepos(old), bitbucketAllowRepos(pol)); e != "" {
		return ruleSaid(policyBitbucketRule, fmt.Sprintf("%s is admitted by no entry of the published block", e))
	}
	return ""
}

// policyRoutingRefusal is §4.2's routing rule: the block decides where a
// Claude call goes, so any difference at all — a destination added, a switch
// turned off, a model list that moved — is refused. --remove-routing is the
// flag that would do one of those on purpose, and it has its own rule.
func policyRoutingRefusal(old, pol *projectpolicy.Policy) string {
	if reflect.DeepEqual(routingOf(old), routingOf(pol)) {
		return ""
	}
	return ruleSaid(policyRoutingRule, fmt.Sprintf("the published block is %s and this publish writes %s", routingText(routingOf(old)), routingText(routingOf(pol))))
}

// policyCredentialsRefusal is §4.2's credentials rule: the block says how the
// cascade behaves once routing turns it on, so a publish that changed it would
// change how a run reaches for another credential — and whether it may spend a
// paid tier — without touching a route.
func policyCredentialsRefusal(old, pol *projectpolicy.Policy) string {
	if reflect.DeepEqual(credentialsOf(old), credentialsOf(pol)) {
		return ""
	}
	return ruleSaid(policyCredentialsRule, "the block this publish would write is not the block the published node carries")
}

// policyCapRefusal is §4.1 rule 6, decided by policyLoosening: the same
// comparison `settings set` decides its own typed confirmation with, so a
// widening this door refuses is a widening that door refuses too — and the
// refusal names the member, because "a cap was raised" is not an answer an
// owner can act on.
func policyCapRefusal(old, pol *projectpolicy.Policy) string {
	loosening := policyLoosening(old, pol)
	if len(loosening) == 0 {
		return ""
	}
	what := fmt.Sprintf("%s from %s to %s", loosening[0].Member, loosening[0].Was, loosening[0].Now)
	if len(loosening) > 1 {
		what += fmt.Sprintf(", and %d more member(s)", len(loosening)-1)
	}
	return ruleSaid(policyCapRule, what)
}

// widenedAllowList is the one predicate behind every "no widening of an allow
// list" rule (spec §4.1 rule 5): the first entry of now that no entry of old
// admits, or "" when the list did not grow. It is asked with the project's own
// matcher rather than compared as text, so an entry spelled as a pattern that
// admits more than the pattern it replaces — a segment wildcard, or the "*"
// that admits every repository — is caught rather than read as a rename. The
// answer is the entry itself so the refusal can name what would have been
// admitted.
func widenedAllowList(old, now []string) string {
	for _, entry := range now {
		if !projectpolicy.AdmitRepo(old, entry) {
			return entry
		}
	}
	return ""
}

// profileAllowRepos, accountAllowRepos and bitbucketAllowRepos read one entry
// of each list off a node that may not have it: a policy that is not
// published at all is nil, and a block the node does not carry is the empty
// list — which is what makes "the published node admitted nothing here" the
// answer for a member that appeared.
func profileAllowRepos(p *projectpolicy.Policy, name string) []string {
	if p == nil {
		return nil
	}
	return p.Profiles[name].AllowRepos
}

func accountAllowRepos(p *projectpolicy.Policy, name string) []string {
	if p == nil {
		return nil
	}
	return p.Accounts[name].AllowRepos
}

func bitbucketAllowRepos(p *projectpolicy.Policy) []string {
	if p == nil || p.BitbucketToken == nil {
		return nil
	}
	return p.BitbucketToken.AllowRepos
}

func routingOf(p *projectpolicy.Policy) *projectpolicy.Routing {
	if p == nil {
		return nil
	}
	return p.Routing
}

func credentialsOf(p *projectpolicy.Policy) *projectpolicy.Credentials {
	if p == nil {
		return nil
	}
	return p.Credentials
}

// policyPublishGate is the gate this command reads: the project's own for a
// coding agent's session, and the zero gate — which refuses nothing and reads
// nothing — for anybody else's.
//
// Why a person's session reads nothing at all. The gate exists to replace the
// confirmation a coding agent cannot give; a person at a terminal gives it
// themselves, and their own decision is the record of it. So their run builds
// no client and makes no call: a transient storage or Resource Manager failure
// must not stop a publish that is not the gate's business, and an ordinary
// installation must not start paying for a property that is false there
// (D1025).
//
// Why the marks are read AFTER openProjectBudget: the ownership question
// (D1017) is asked by that call with the command's own words for what it
// declined to do, and a database this installation may not write has no
// business clearing anything. The helper asks it again for a managed project
// and writes nothing either way.
func policyPublishGate(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, agent string) (managedGate, error) {
	if agent == "" {
		return managedGate{}, nil
	}
	clients, err := newInitClients(ctx, lc)
	if err != nil {
		return managedGate{}, err
	}
	return readManagedGate(ctx, managedGateEnv{lc: lc, s: clients.Storage, crm: clients.CRM, db: db}, managedGateFacts{
		Subcommand:  "project policy publish",
		Argv:        os.Args[1:],
		Declined:    "nothing was published",
		AgentMarker: agent,
	})
}

// publishAgentRefusal is today's refusal, byte for byte, for a project this
// program has not proved is managed. It is written once because two places
// read it: the command refuses with it before it reads the node, so an
// unmanaged project's session reads exactly what it read before this design
// existed and nothing else happens; and the loop refuses with it when the
// second mark read found the project is not managed any more (D1024). Two
// copies of one refusal are two chances for them to drift.
func publishAgentRefusal(agent string) error {
	return userErr("nothing was written: fugaro project policy publish writes the policy every run of the project is admitted by: %s", initflow.AgentRefusal(agent))
}

// policyPublishProceed is whether the session may go on and what it reads
// about it: initflow's own class rule, asked with the gate's answer, and
// initflow's own sentence for the one case in which a step in a coding agent's
// session proceeds. A person's session is touched by neither.
func policyPublishProceed(agent string, out io.Writer, m initflow.Managed) error {
	if agent == "" {
		return nil
	}
	if !initflow.CanConfirmManaged(policyPublishClass, initflow.Conditions{Agent: agent}, m) {
		return publishAgentRefusal(agent)
	}
	fmt.Fprintln(out, initflow.AgentProceeded(agent))
	return nil
}
