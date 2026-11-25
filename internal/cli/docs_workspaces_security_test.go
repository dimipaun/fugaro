package cli

// The workspaces capability's security claims, pinned.
//
// W0-W8 built the thing: a workspace's own project and database, the record
// `fugaro update` publishes, the signer endpoint and the member-bound
// assertion, the member's admission, the workspace's credentials service and
// its bundle, the cap's meter and alarm, and the stop. W9 is the documentation
// those tasks make true, and this file is the half of it that rots silently:
// SECURITY.md's own section and the workspace paragraph in docs/design/v1.md.
// A release that shipped the capability without SECURITY.md's residual risks
// would be a documentation failure of a security feature, so every risk
// docs/workspaces/spec.md §8 lists is pinned here as a sentence, and the
// withdrawn one is pinned as withdrawn rather than dropped.
//
// Two things about how the pins are read, both borrowed from
// docs_security_test.go, whose shape this file follows:
//
//   - **The claims are read out of the section** (docSection), never out of the
//     whole document. A claim that lives only in a bullet pointing at the
//     section, or in the credentials service's own section beside it, must not
//     satisfy a pin on its own: the workspace's limits are stated in one place,
//     and that is where a reader finds them.
//   - **The overclaims are read out of the whole document**, because a bullet
//     that points at the section is as wrong as the section is. Each banned
//     sentence is one a rewrite drifts into: that the workspace is a boundary
//     between members, that a member's database cannot be read, that the
//     workspace holds no credential, that the cap cannot be passed, that
//     revocation is immediate, and that the cap binds on every
//     shared-credential call.
//
// The red step is this file's first run: the sentences it pins are not written
// yet, so it fails on the section's own absence.

import (
	"regexp"
	"strings"
	"testing"
)

// wsSecurityHeading is the section SECURITY.md states the workspace's trust
// model in. It is a "###" of "Trust model and known limits", beside the
// credentials service's and the Bitbucket proxy's own sections.
const wsSecurityHeading = "### Fugaro workspaces"

// wsAuthorityHeading is the section the addendum's own threat model is stated
// in: the workspace-projects-users work gives a workspace IAM authority over
// its member projects (D2800), which reverses three of the rules
// docs/workspaces/spec.md §9 states as prohibitions, so SECURITY.md carries
// the supersession and, per credential or actor that authority puts in reach,
// what it reaches. It is a section of its own rather than a paragraph of the
// one above because the two answer different questions: the section above is
// what a member's people and a member's run reach of a workspace, and this one
// is what the workspace's authority reaches of a member.
const wsAuthorityHeading = "### Workspace authority over member projects"

// TestSecurityMdStatesTheWorkspaceThreatModel is W9's headline pin: the
// workspace's four reachable surfaces, what a stolen workspace token reaches
// and what it does not, the unchanged member token, the rotation and
// revocation the design does not claim, the pool's intersection, the cap and
// its meter, the cross-project read, the stop, the injection rules, the
// unattended agent, the six prohibitions of spec §9, and all seven residual
// risks — with the fifth pinned as withdrawn, because a number reused is a
// reader sent to the wrong place.
func TestSecurityMdStatesTheWorkspaceThreatModel(t *testing.T) {
	s := readDoc(t, "SECURITY.md")
	if !strings.Contains(s, "\n"+wsSecurityHeading+"\n") {
		t.Fatalf("SECURITY.md has no `%s` section: a workspace adds a sign-in surface, a credential pool and a spend ceiling that no member had, and the section is where the trust model states them", wsSecurityHeading)
	}
	section := flattenDoc(docSection(t, "SECURITY.md", s, wsSecurityHeading))
	for _, want := range []string{
		// The four surfaces. An earlier draft of the design named only the
		// first, which is the omission a threat model cannot afford.
		"**Four surfaces are reachable",
		"the member's exchange",
		"the workspace's signer endpoint",
		"the workspace's credentials service",
		"the workspace's own Firebase",
		"a public sign-in surface",
		// No member reads the workspace's database, and the workspace adds no
		// route to a member's own API.
		"no member reads it",
		// What a stolen workspace token reaches, and what it reaches that the
		// person did not have: nothing about a member and nothing about the
		// group.
		"**What a stolen workspace token reaches.**",
		"never a bearer for a member's API",
		"**What a stolen workspace token reaches that the person did not have: nothing about a member, and nothing about the group.**",
		"**member list**",
		"**shape and timing**",
		"no member enumeration",
		// The timing half is an owner-run check, said rather than left to be
		// assumed: no hermetic test can hold a running server's timing.
		"**The timing half of that claim has no hermetic test**",
		"`jti`",
		"`aud`",
		// The member token itself is unchanged by the workspace.
		"**A stolen member token is unchanged.**",
		// No rotation, stated as the absence of a claim, and revocation bounded
		// by two lifetimes rather than instantaneous.
		"**There is no rotation here, and the design does not claim one.**",
		"not detectable by rotation",
		"**Revocation at the workspace ends the person's ability to exchange, and nothing else**",
		"neither revocation is instantaneous across members",
		// WS6 gave the member its one grant in the workspace's project when the
		// workspace root started deploying the workspace's own credentials
		// service, so the closer half of that sentence has to name it rather
		// than say no member holds a grant there (F29): what a member's job
		// accounts hold is the front door to that service and no node of the
		// workspace's database.
		"**The one grant a member holds in the workspace's project is the invoke grant on the workspace's own credentials service**",
		"`roles/run.invoker` per member job account",
		"no node of the workspace's database",
		// WC2 makes that sentence's implication false: from the task that
		// renders the roster's people into the workspace's own root, a
		// member's job accounts are no longer the only holders of that
		// grant. So the sentence names the other two principals — one
		// invoke grant per address for each `human` of the roster, and one
		// accessor for the workspace's own account, conditioned to the
		// person's prefix AND to the one name the workspace serves (D2971,
		// R6) — and the width of that condition is pinned, because a
		// condition that dropped either half would read the project's other
		// secrets.
		"**A person the workspace's roster names holds the same invoke grant, and the workspace's own account holds one conditioned accessor beside it.**",
		"`roles/run.invoker` per address",
		"conditioned to the `fugaro_u_` prefix",
		"the one personal name the workspace serves",
		// The pool, the intersection, and what a compromised member reaches.
		"**A compromised member reaches the pool, and the intersection is what bounds it.**",
		"cannot read a credential's value",
		// The cap: metered where the money is spent, bounded by the reservation
		// in flight, and refusing on a failed settle.
		"reserves the room for a call before it makes it and settles on the response's usage",
		"**the reservation in flight**",
		"**a failed settle refuses**",
		// The wiring, and what it reaches: the reservation is taken before
		// every paid call the workspace's own service answers — the probe
		// included, since a probe is a call the workspace pays for — the
		// settle's failure refuses the call, a subscription token's call
		// spends nothing of the workspace's money, and an INSTALLATION's scope
		// is the one with no meter, unchanged in every behavior. The sentence
		// has to name which of the two deployments that scope is, because this
		// tree builds both.
		"the probe a cascade owes a backed-off credential included",
		"refusing the call when that settle cannot be written",
		"serving a call on a subscription token unmetered because it spends none of the workspace's money",
		"it reserves nothing, settles nothing and writes no header",
		// The headroom's delivery, narrowed to the answers that really carry it:
		// answer() and refuseMeter write it, and a refusal that never reached the
		// meter — refuseSelection's wait, the call a credential was already sent
		// to — writes none, which nothing reads as a ceiling of zero.
		"the headroom rides the answers the service writes itself",
		"a refusal that never reached the meter",
		// And the one path where the fail-closed rule above does not hold. A
		// streamed answer has already reached the run when its usage is known,
		// so a settle that cannot be written there is a log line and the
		// reservation ages out unrecorded. Without this the pin above --
		// "refusing the call when that settle cannot be written" -- reads as a
		// rule about every path, while the path most calls take is the one it
		// does not cover.
		"**A streamed answer is the one path where a failed settle does not refuse the call, and it is stated rather than hidden**",
		"released with its amount never reaching the month's settled spend",
		// And the limits this release does not hide. Both deployments are built
		// here and the meter binds over the workspace's own, so the sentence
		// has to say what the cap DOES reach -- the deployment, the route that
		// leads a member's run to it -- and leave the limits that are real (the
		// stream, the alarm, the missing per-member share) as the limits.
		"**And the limit this release does not hide.**",
		"**What the wiring reaches, and what it does not, is worth stating exactly.**",
		"the workspace owner's `fugaro update` applies it with no other step",
		"reaches that service where the route its own `fugaro update` published says a kind is served from the workspace",
		// The alarm, which is the cap's second half and is still hosted
		// nowhere: the section states the design's alarm above and has to say
		// that nothing runs it, or a reader takes it as live while residual
		// risk 1 counts it as a mitigation. `budget.ReconcileWorkspace` is
		// called from its own test file and from nothing else on this tree
		// (D2991).
		"**The reconciliation alarm still runs nowhere**",
		"`budget.ReconcileWorkspace` is reached from its own tests and from nothing that runs",
		"**And one member can use the cap up for the others**",
		"no per-member share of the workspace's cap",
		// The alarm, and the unbound member that fails closed rather than
		// counting as zero.
		"**The reconciliation alarm.**",
		"A member the per-member table does not bind is reported as an alarm rather than counted as zero",
		// The cross-project read: a whole-database read that bypasses the rules,
		// the write that is gone, and what the service cannot reach at all.
		"**What the workspace's service can read, and what it cannot.**",
		"IAM on a Realtime Database is whole-database and bypasses the rules",
		"`firebasedatabase.instances.get`",
		"**A compromised workspace service therefore reads a member's whole database**",
		"**It cannot write any of it**",
		// And the one write that is in reach, because the sentence above is
		// weaker than it reads without it: the run's own Firebase token, which
		// a run's call to the service carries (R18, F30), writes the run's own
		// nodes in the member's database. F30 says so in as many words, and O4
		// is the review this sentence is written for -- a service that verifies
		// the run by that token holds it.
		"**The one write this puts in reach is the run's own token.**",
		"the run's own nodes in the member's database",
		"**a write the service's own grant does not carry**",
		"**cannot delete or disable a member's database**",
		"**The runs bucket is not read at all**",
		// The stop, in the workspace's own database, and its scope.
		"**The stop, and what it does not reach.**",
		"`fugaro budget kill --workspace <name>`",
		"**workspace's own database**",
		"`fugaro budget resume --workspace <name>`",
		"not a member's own credentials, not a member's own budget or caps, not a member's sign-in",
		// Injection, and an agent as a caller.
		"**Injection into the workspace service.**",
		"a token's content is never echoed",
		"**An unattended agent as a caller.**",
		"no workspace-level token limit to get wrong",
		// The residual risks, every one of spec §8's seven, the withdrawn
		// fifth included -- and each carrying the number spec §8 gives it, so a
		// reader can put the two lists beside each other. The numbers are part
		// of the pinned phrase rather than a second loop, because the section is
		// read flattened and a line-anchored check cannot survive the wrap.
		"**What this design accepts, as residual risk.**",
		"1. **A member's operator can spend the workspace's pool**",
		"2. **A workspace revocation does not reach a member token already minted.**",
		"3. **The workspace's identity provider is a single point of sign-in**",
		"4. **The workspace service is on the critical path of every shared-credential call.**",
		"5. **Withdrawn on 2026-11-04.**",
		"the number is kept rather than reused",
		"6. **Anyone who can mint a custom token for the workspace's project can set `email` and `email_verified` to anything, and reach every member as anybody.**",
		"`signBlob`",
		"`TokenCreator`",
		"**refuse `custom` and `anonymous`**",
		"`google.com`",
		"`password`",
		"7. **A shared credential is served where the member's own service would refuse it: the run's repository is now held to the calling job account, and the account's class is not.**",
		"**The pairing of that repository with the account that called is asked too",
		"closes the gap the workspaces design recorded as O3",
		"refused at the pairing with the typed reason `unverified_run_identity`",
		"**The second way this risk is reachable is the account's class**",
		"**this release ships it stated and not enforced**",
		// Spec §9 as prohibitions, which is how the spec writes them.
		"**What a workspace never does.**",
		"**never widens a member**",
		"**never hands out a credential**",
		"**never owns a repository, a runs bucket, a job or an image**",
		"**never writes a member's database**",
		"**never becomes a second permission model**",
		"**carries no workspace-wide repository allow-list**",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the `%s` section never says %q: a workspace adds an identity surface, a shared credential pool and a spend ceiling, and this section is where SECURITY.md states what each one reaches", wsSecurityHeading, want)
		}
	}

	// The addendum's half, and the red step of WU0: the workspace-projects-users
	// work hands the workspace IAM authority over its member projects, which is
	// the rule the section above states as a prohibition, so the supersession
	// and every credential or actor that authority reaches are stated here. The
	// heading is read out of the document by name, so this block fails on the
	// section's absence -- which is what it did before the section was written.
	authority := flattenDoc(docSection(t, "SECURITY.md", s, wsAuthorityHeading))
	for _, want := range []string{
		// The supersession itself: three of §9's six rules, named, and the one
		// consent that supersedes them.
		"**The addendum gives a workspace IAM authority over its member projects, and it supersedes three of the six rules `docs/workspaces/spec.md` §9 states as prohibitions.**",
		"**never widens a member**, **never writes a member's database** and **never becomes a second permission model**",
		"for people and roles only",
		"on one consent, and it is the member owner's own act",
		"`fugaro workspace project link`",
		"an unlinked member — every existing one — keeps `fugaro user …` unchanged",
		// The three that stand: a rewrite that superseded all six would read as a
		// workspace that may now do anything to a member.
		"**The other three rules stand**",
		"**never hands out a credential**",
		"never owns a repository, a runs bucket, a job or an image",
		"**carries no workspace-wide repository allow-list**",
		// The actors and credentials, each with its own lead, so a reader can
		// find the one they are asking about.
		"**A stolen workspace-admin credential reaches every linked member, and that is the consent's real size.**",
		"it reads every secret of that member",
		"**every personal Claude OAuth token**",
		// The account's own credential paths, which the credential-widest reading
		// of "no key" hid: an out-of-band key and actAs outlive the binding that
		// made them, so the lead names the key rather than denying it.
		"**A compromised workspace IAM account is the same reach held as an account, and a key made on it outlives the binding that made it.**",
		"`iam.serviceAccountKeys.create`",
		"`iam.serviceAccounts.actAs`",
		"`rm --admin` removes the token-creator binding and revokes no key",
		"**The workspace credentials service's account can rewrite the roster, `Admins` included.**",
		"`workspaceRosters/<name>`",
		"**What it cannot do is become an admin by writing the list**",
		"**The workspace project's owners, editors and budget admins are workspace admins de facto.**",
		"**the workspace project's own IAM is the workspace's root of trust**",
		// A project admin's two tamperable nodes, named: the mirror the grant gate
		// reads and the audit trail itself.
		"**A hostile project admin rewrites the mirror and the member's audit node.**",
		"**the audit trail is as tamperable as the roster**",
		// The member owner, whose reach stops at their own member -- except for
		// the one record of theirs the workspace reads.
		"**A hostile member owner reaches their own member and nothing else.**",
		"`config/admin/workspace-link`",
		"refuses a recorded parent inside any workspace's folder",
		// The disclosure the mirror makes: the group's addresses are readable by
		// every member's own people, which the design above said they were not.
		"**Every member's launchers and viewers read the mirror: the workspace's member addresses and external marks.**",
		// And the lag, which is the half a removed admin's reach outlives -- with
		// the other half named beside it: what they made while they held the
		// authority is not taken back by the command that removes them, and the
		// owner ruled on 2026-11-19 that it is accepted with that said.
		"**The revocation lag is the member's own update, and removing an admin does not take back what they made while they held the authority.**",
		"the workspace's stored grants stay in the member's roster",
		"the owner's ruling of 2026-11-19",
		// The document the whole statement lives in, so the two are read together.
		"docs/workspace-projects-users/threat-model.md",
	} {
		if !strings.Contains(authority, want) {
			t.Errorf("the `%s` section never says %q: a workspace that holds IAM authority over its members is a widening the workspaces section above denies, and this section is where SECURITY.md states who that authority puts in reach of what", wsAuthorityHeading, want)
		}
	}

	// The overclaims a rewrite drifts into, read out of the whole document.
	for _, banned := range []string{
		// The workspace as a boundary: it groups projects, and a member's own
		// lists stay the only thing that admits anyone.
		"the workspace is a boundary between its members",
		"the workspace's cap cannot be passed",
		"the workspace holds no credential",
		// The cross-project read: the read permission reaches every node, and
		// no IAM policy narrows it to the nodes the service uses.
		"the workspace service reads only the ledger, the policy and the outcomes",
		"the workspace's grant is limited to the nodes it reads",
		// Revocation, which the design buys back with token lifetime. The
		// banned spelling is the overclaim, not the honest sentence beside it:
		// "neither revocation is instantaneous across members" is what the
		// section has to say, so the pin cannot be a substring of it.
		"a workspace revocation reaches a member token already minted",
		"a workspace revocation is immediate",
		// The pre-WS6 spelling of the grant claim, which the workspace's own
		// service makes false: a member's job account does hold one grant in
		// the workspace's project, and a section that kept the older wording
		// would tell a reader no member's account reaches the workspace at all.
		"because no member holds a grant on the workspace's project",
		// And WC2's own: the roster's people hold that grant too, so a
		// sentence that left a member's job accounts as its only holders
		// would send a reader past the grant a person of the workspace
		// holds and past the accessor the workspace's own account holds
		// beside it.
		"only member job accounts hold a grant",
		// The meter, which is machinery on this tree and wired to the paid-call
		// path of the workspace's own deployment -- which is built here and
		// which `fugaro update` applies -- so the cap IS a fence over a
		// member's metered call. What is wider than the tree is the word
		// "every": a kind served from the project's own copy is not metered,
		// and a subscription token's call reserves nothing.
		"the workspace's cap is enforced on every shared-credential call",
		"every shared-credential call is metered",
		// The stale spellings this section carried while the meter was unwired
		// or the deployment unbuilt. All three are now false in the other
		// direction, and a section that kept one would tell a reader the cap
		// is not a fence at all when it is.
		"**a workspace's cap binds nothing today**",
		"still not a fence over any call a member makes today",
		"does not exist on this tree yet",
		"answers every call as an **installation**",
		"no code path takes them on this tree",
	} {
		if strings.Contains(flattenDoc(s), banned) {
			t.Errorf("SECURITY.md says %q: that is wider than the design and wider than the tree", banned)
		}
	}
}

// TestSecurityMdStatesTheWorkspacesOwnSlip is WC4's half of the sentence this
// task makes false, and it is the reason the plan's shared-files table takes
// `SECURITY.md` as its one exception: the slip paragraph states, of every slip,
// that the roster which decided its credential set "is read at issue time and
// never remembered" and that "a slip already issued keeps serving the run it was
// granted for" — and the workspace's own slip is narrower than that on every
// count. The service re-reads the roster as it re-reads the node, so a person
// taken off the roster is refused within the minute, a slip a person holds is
// refused on every call once they are gone, the slip records the owner's
// verified address case-folded at grant time, and a grant nobody claimed is
// over at its claim deadline rather than claimable for weeks. The pin reads the
// section the sentence lives in, because a claim stated anywhere else in the
// document is not a claim the reader of that paragraph has.
func TestSecurityMdStatesTheWorkspacesOwnSlip(t *testing.T) {
	s := readDoc(t, "SECURITY.md")
	const slipsHeading = "### The intake, and the slips a run holds"
	if !strings.Contains(s, "\n"+slipsHeading+"\n") {
		t.Fatalf("SECURITY.md has no `%s` section: the workspace's own slip is narrower than the installation's, and the paragraph that states the wider rule is where the narrowing has to be read", slipsHeading)
	}
	section := flattenDoc(docSection(t, "SECURITY.md", s, slipsHeading))
	for _, want := range []string{
		// The narrowing, named as one: a reader who takes the paragraph above
		// at face value has to meet this sentence in the same place.
		"A workspace's own slip is narrower than that on every count",
		// The roster IS re-read, as the node is, and a removed person is
		// refused within the minute -- both their next grant and the calls of
		// the runs they already launched (R3, D3146).
		"re-reads the roster as it re-reads the node",
		"every 60 seconds",
		"is refused their next grant",
		"every call under a slip they already hold within that minute",
		// The check every call makes needs the verified address, and it is
		// case-folded when the grant writes it (F11, finding 7).
		"records its owner's verified address, case-folded at grant time",
		"the check every call reads",
		// And the claim deadline: the number is WC4's to choose and is chosen
		// here, so the document states it rather than leaving a reader to
		// guess how long an unclaimed grant is worth.
		"a claim deadline counted from the grant — 30 minutes",
		"can no longer be claimed",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SECURITY.md's slips paragraph never says %q: the paragraph states the wider rule, and WC4's narrower one has to be read beside it or the document is false about the workspace's own slip", want)
		}
	}
}

// TestDesignV1NamesTheWorkspace: docs/design/v1.md is the design the whole
// repository is written against, its project section defines the unit every
// other document names, and W9 is the task the plan lets add the workspace
// paragraph there. A workspace has to be defined beside the project rather than
// only in its own folder, because the two are the pair a reader meets first --
// and the paragraph has to carry the rule the workspace exists to keep, which
// is that it never widens a member.
func TestDesignV1NamesTheWorkspace(t *testing.T) {
	doc := readDoc(t, "docs/design/v1.md")
	for _, want := range []string{
		"**Workspace.**",
		"../workspaces/spec.md",
		"a project of its own",
		"membership takes both sides",
		"holds no repository, no runs bucket, no job and no image",
		"never widens a member",
		// The addendum's half: the design the whole repository is written against
		// has to say that a workspace holds IAM authority over its member
		// projects, which supersedes three of the rules its own §9 states, and
		// that the consent is the member owner's own. A design that left the
		// "never widens a member" sentence standing unqualified beside a
		// workspace that does widen one is the contradiction this pin exists for.
		"**supersedes three of `docs/workspaces/spec.md` §9's six prohibitions",
		"never writes a member's database",
		"never becomes a second permission model",
		"for people and roles only",
		"on the member owner's own consent at `fugaro workspace project link`",
		"D2800",
		"../workspace-projects-users/threat-model.md",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/design/v1.md never says %q: the project section is where the repository's unit is defined, and a workspace the design does not define beside it is a workspace the rest of the tree reads as a second project layer", want)
		}
	}
	// The paragraph is a term of the same list, not a section of its own: it is
	// read beside `project`, and a "##" heading would put the workspace above
	// the terminology it is defined against.
	if !regexp.MustCompile(`(?m)^- \*\*Workspace\.\*\* `).MatchString(doc) {
		t.Error("docs/design/v1.md does not carry the workspace as a bullet of the terminology list: the project section defines its units as bullets, and a paragraph anywhere else is a unit a reader does not meet where the others are")
	}
}

// slipsSecurityHeading is the section of SECURITY.md that states what a slip is
// and what a hostile launcher reaches of one. It is the section a workspace's
// slip changes: the roster sentence it carried said a slip already issued keeps
// serving the run it was granted for, which is true of the pool a grant is read
// from and false of a workspace's roster, which its credentials service re-reads
// under a live slip (D3146).
const slipsSecurityHeading = "### The intake, and the slips a run holds"

// TestSecurityMdStatesTheRostersOneException is WC8's pin on the sentence it
// adds to the slips paragraph, reconciled with WC4's when the two tasks met on
// `main`. WC4 narrowed the paragraph for a workspace's own slip -- the roster
// re-read as the node is, the case-folded address, the claim deadline -- and
// left the pool's own rule standing, because "a slip already issued keeps
// serving the run it was granted for" is true of the publisher's pool a grant's
// credential set is read from; WC8 adds what the run does when the roster its
// slip is re-read against drops its person. So the paragraph states both, and
// this pin holds them together: a rewrite that dropped either half would leave
// a reader with the wrong rule.
//
// What the halt's reason says is pinned nowhere, deliberately: the reason a run
// refused under a removed owner's slip carries is the open half of D3146 (the
// landed service answers the one sentence it answers every refusal of a run's
// identity, so the halt does not name the removal), and the document states the
// halt itself rather than a sentence about its wording.
func TestSecurityMdStatesTheRostersOneException(t *testing.T) {
	doc := readDoc(t, "SECURITY.md")
	section := flattenDoc(docSection(t, "SECURITY.md", doc, slipsSecurityHeading))
	for _, want := range []string{
		// The pool's own rule, kept: the roster a grant's credential set is
		// read from is the publisher's, and a person taken off it is refused at
		// the next grant.
		"the roster that decided a slip's credential set is read at issue time and never remembered",
		"a member the publisher takes off a pool is refused at the next grant",
		"while a slip already issued keeps serving the run it was granted for",
		// WC4's narrowing, which is what makes the sentence above safe to keep.
		"A workspace's own slip is narrower than that on every count",
		"every call under a slip they already hold within that minute",
		// And WC8's own fact, which neither side of the paragraph stated: the
		// run under such a slip ends rather than being served another
		// credential (D3146).
		"the run under such a slip halts rather than being served another credential",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the `%s` section does not state %q: a slip that keeps serving a run whose person the workspace has dropped is the guarantee D3146 refuses", slipsSecurityHeading, want)
		}
	}
}
