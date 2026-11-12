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
//     revocation is immediate, and that the meter is wired on this tree.
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
		// The pool, the intersection, and what a compromised member reaches.
		"**A compromised member reaches the pool, and the intersection is what bounds it.**",
		"cannot read a credential's value",
		// The cap: metered where the money is spent, bounded by the reservation
		// in flight, and refusing on a failed settle.
		"reserves the room for a call before it makes it and settles on the response's usage",
		"**the reservation in flight**",
		"**a failed settle refuses**",
		// And the limit this release does not hide: the meter is not wired, so
		// the cap binds nothing on this tree.
		"**And the limit this release does not hide.**",
		"no code path takes them on this tree",
		"**a workspace's cap binds nothing today**",
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
		"7. **A shared credential is served where the member's own service would refuse it: the run's repository is not held to the calling job account.**",
		"**The second way this risk is reachable is the account's class**",
		"**this release ships both stated and not enforced**",
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
		// The meter, which is machinery on this tree and not yet a fence.
		"the workspace's cap is enforced on every shared-credential call",
		"every shared-credential call is metered",
	} {
		if strings.Contains(flattenDoc(s), banned) {
			t.Errorf("SECURITY.md says %q: that is wider than the design and wider than the tree", banned)
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
