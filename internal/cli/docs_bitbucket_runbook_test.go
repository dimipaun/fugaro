package cli

import (
	"regexp"
	"strings"
	"testing"
)

// docs/credentials-service/runbook.md's twelfth section and the read mint it
// documents (T2.6 of docs/bitbucket-credentials/implementation-plan.md). The
// runbook is where an operator acts while something is already wrong, so its
// read-consumer section is held to three things a later edit can quietly
// break: the clause Check 145 has not settled stays marked UNVERIFIED (U14)
// while the one the reconciliation closed reads as closed (U15), the steps
// name real commands and a real credential entry, and
// the code the section documents is cited by the symbols that exist on this
// branch -- a runbook that cites a symbol the code has renamed sends the
// operator to a file that does not answer them.
const bitbucketRunbook = "docs/credentials-service/runbook.md"

// bitbucketRunbookReadConsumerHeading is the section's own heading. Its number
// is §12 because §11 is the proxy's token and the two are different
// credentials with different stop switches, and an operator who reads the
// wrong one rotates the wrong thing.
const bitbucketRunbookReadConsumerHeading = "## 12. The Bitbucket read consumer"

// bitbucketRunbookCitations is what the section cites: a file, and the
// symbols it names out of it. Every symbol here is read out of the real source
// below rather than trusted, so a rename on either side fails the test -- the
// section is the operator's only map from "the read consumer" to the code that
// spends it.
var bitbucketRunbookCitations = []struct {
	path    string
	symbols []string
}{
	{"internal/credsvc/bundle.go", []string{
		"BitbucketReadConsumerSecret",
		"KindBitbucketReadConsumer",
		"BitbucketReadConsumer",
	}},
	{"internal/credsvc/bitbucket.go", []string{
		"BitbucketReadPath",
		"bitbucketRead",
	}},
	{"internal/credsvc/auth.go", []string{
		"VerifyReadCaller",
		"readCallerClass",
		"PersonAccounts",
	}},
	{"internal/cli/creds.go", []string{
		"readConsumerShape",
		"readConsumerNode",
	}},
}

// readConsumerNoConsumerRE pulls the no-consumer refusal's own text out of
// internal/credsvc/bitbucket.go, so the section quotes the message the
// operator actually sees instead of one a later edit to the constant would
// leave behind.
var readConsumerNoConsumerRE = regexp.MustCompile(`ErrReadNoConsumer = errors\.New\("([^"]+)"\)`)

// TestDocsBitbucketRunbookReadConsumerIsMarkedUnverified: the section tells the
// open question from the closed one where the operator creates the consumer.
// U14 (whether creating a consumer needs workspace-admin rights) is the one
// Check 145 has not settled, and a runbook that picks an answer silently is
// worse than one that says it does not know. U15 (whether the consumer must be
// private) was closed on 2026-11-10 — Bitbucket's create form offers no such
// choice and the grant worked for the consumer it made — so the section says
// that rather than carrying a mark the reconciliation has already lifted.
func TestDocsBitbucketRunbookReadConsumerIsMarkedUnverified(t *testing.T) {
	section := flattenDoc(docSection(t, bitbucketRunbook, readDoc(t, bitbucketRunbook), bitbucketRunbookReadConsumerHeading))
	for _, want := range []string{
		// The open question, by its own number, with what it decides and the
		// word that keeps it from reading as a reading.
		"**U14**",
		"UNVERIFIED",
		"workspace-admin rights",
		// The closed one, with the reading that closed it rather than a mark.
		"**U15**",
		"private",
		"closed (2026-11-10)",
		"offers no such choice",
		// The check that settles what is left, named so a reader knows what to
		// wait for rather than who to ask.
		"check145.sh",
		"Check 145",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's %q section does not say %q: the clause Check 145 has not settled has to read as unsettled where the operator creates the consumer, and the one the reconciliation closed has to read as closed", bitbucketRunbook, bitbucketRunbookReadConsumerHeading, want)
		}
	}
	// U14's mark has to be there, not just U14: a later edit that dropped it
	// would leave the question reading as an answer.
	if n := strings.Count(section, "UNVERIFIED"); n < 1 {
		t.Errorf("%s's read-consumer section carries %d UNVERIFIED marks, want at least one for U14", bitbucketRunbook, n)
	}
	// And U15 carries none: the paragraph that states it is the paragraph that
	// says it is closed, and a mark left beside it would be the runbook telling
	// an operator that a settled question is open.
	i := strings.Index(section, "**U15**")
	if i < 0 {
		t.Fatalf("%s's read-consumer section no longer states U15", bitbucketRunbook)
	}
	u15 := section[i:]
	if j := strings.Index(u15, "**Write it with"); j >= 0 {
		u15 = u15[:j]
	}
	if strings.Contains(u15, "UNVERIFIED") {
		t.Errorf("%s's read-consumer section marks U15 UNVERIFIED: the reconciliation closed it on 2026-11-10 (the create form offers no such choice), so the mark belongs to U14 alone", bitbucketRunbook)
	}
}

// TestDocsBitbucketRunbookReadConsumerIsOwnerRunOnly: the section's steps are
// the owner's, in the owner's own terminal, and it says so rather than leaving
// an agent to infer it. The other half of the same claim is the answer a
// reader of the whole file starts from ("the owner runs every step in this
// file", pinned by docs_credentials_runbook_test.go), so this test pins the
// section's own closing sentence.
func TestDocsBitbucketRunbookReadConsumerIsOwnerRunOnly(t *testing.T) {
	section := flattenDoc(docSection(t, bitbucketRunbook, readDoc(t, bitbucketRunbook), bitbucketRunbookReadConsumerHeading))
	if !strings.Contains(section, "No agent runs any step of it") {
		t.Errorf("%s's read-consumer section does not say no agent runs any step of its check", bitbucketRunbook)
	}
	if !strings.Contains(section, "the create, the stop, the rotation and the transfer are yours") {
		t.Errorf("%s's read-consumer section does not put its own four steps on the owner", bitbucketRunbook)
	}
	// The banned shape, as in the file's own guard: a sentence that hands a
	// step of this to a run. A run has no reason to reach the read mint --
	// the mint refuses a run identity by name -- so a runbook that offers one
	// is wrong as well as unsafe.
	for _, banned := range []string{
		"the agent can set a credential",
		"a run rotates the bundle",
		"fugaro creds set bitbucket-read-consumer --",
	} {
		if strings.Contains(section, banned) {
			t.Errorf("%s's read-consumer section carries %q, which reads as a step a run or an agent may take", bitbucketRunbook, banned)
		}
	}
}

// TestDocsBitbucketRunbookReadConsumerNamesTheRealEntryAndSteps: the section
// names the bundle entry by its real document key, writes it with the CLI's
// real command and stdin shape, and states the three facts about the
// credential itself that decide what its token is (created under the
// installing person's account, read scopes only; the token acts as that
// person; it stops when the person leaves). Each is a sentence the code
// already implements, so none of them is a promise this PR is making.
func TestDocsBitbucketRunbookReadConsumerNamesTheRealEntryAndSteps(t *testing.T) {
	section := flattenDoc(docSection(t, bitbucketRunbook, readDoc(t, bitbucketRunbook), bitbucketRunbookReadConsumerHeading))
	for _, want := range []string{
		// The entry's real document key, so `fugaro creds ls` and the runbook
		// agree on what the operator is looking at.
		"`bitbucket-read-consumer`",
		// The write: the CLI's own command, stdin only, and the three fields
		// of the document it reads.
		"fugaro creds set bitbucket-read-consumer",
		"stdin only",
		"`key`, `secret` and `workspace`",
		// The value's meaning, as the runbook's own text rather than a table a
		// reader has to find.
		"installing person's own account",
		"read scopes only",
		"acts as **that person**",
		"stops working when that person leaves",
		// The read mint's route, so the section is about a real mint.
		"/v1/bitbucket/read-token",
		// The rule that makes the write order-safe: the loader refuses the
		// whole bundle on an unknown key, so an older service reading a bundle
		// that carries this entry halts every model call and every GitHub mint.
		"fugaro update",
		"refuses the **whole** bundle",
		// And the kind that must never be written by hand while S3 is parked.
		"bitbucket-run-consumer",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's read-consumer section does not name %q", bitbucketRunbook, want)
		}
	}
}

// TestDocsBitbucketRunbookReadConsumerCitesTheMergedCode: every file the
// section cites exists and still carries the symbols the section names out of
// it, and the no-consumer refusal is quoted from the code rather than retyped.
// The section documents behavior, so the alternative to this guard is an
// operator following a citation to a file that no longer explains what they
// are looking at.
func TestDocsBitbucketRunbookReadConsumerCitesTheMergedCode(t *testing.T) {
	section := flattenDoc(docSection(t, bitbucketRunbook, readDoc(t, bitbucketRunbook), bitbucketRunbookReadConsumerHeading))
	for _, cite := range bitbucketRunbookCitations {
		if !strings.Contains(section, cite.path) {
			t.Errorf("%s's read-consumer section does not cite %s, the file that implements what it documents", bitbucketRunbook, cite.path)
		}
		src := readDoc(t, cite.path)
		for _, symbol := range cite.symbols {
			if !strings.Contains(section, symbol) {
				t.Errorf("%s's read-consumer section does not name %q out of %s: a citation that does not name the symbol sends the operator to a file that does not answer them", bitbucketRunbook, symbol, cite.path)
			}
			if !strings.Contains(src, symbol) {
				t.Errorf("%s no longer holds %q, which %s's read-consumer section cites: the doc and the code have drifted", cite.path, symbol, bitbucketRunbook)
			}
		}
	}

	// The refusal's own text, read out of bitbucket.go: "a bundle with no read
	// consumer is refused, and never falls back to another entry" is the one
	// claim in the section an operator checks against a log line.
	src := readDoc(t, "internal/credsvc/bitbucket.go")
	m := readConsumerNoConsumerRE.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("internal/credsvc/bitbucket.go no longer defines ErrReadNoConsumer as an errors.New literal; update this test's regexp")
	}
	msg := strings.TrimPrefix(m[1], "credsvc: ")
	if !strings.Contains(section, msg) {
		t.Errorf("%s's read-consumer section does not quote the no-consumer refusal %q: the operator reads that line in the log and this is where it is explained", bitbucketRunbook, msg)
	}
	// And the state it answers with, so the two halves of the refusal are in
	// the same place.
	if !strings.Contains(section, "state_unavailable") {
		t.Errorf("%s's read-consumer section does not name state_unavailable, the state a bundle with no read consumer answers with", bitbucketRunbook)
	}
}

// TestDocsBitbucketRunbookReadConsumerNamesBothCallerClasses: who the consumer
// is minted to, one class per command. G4 is the split -- a person's own
// identity token for `coordinator show` and for that person's local
// `coordinator status`, and the coordinator account alone for the sweep -- and
// the two are the whole of the read mint's admission. A section that named
// only one of them would leave the reader thinking the other cannot happen,
// which is the reading the classes exist to prevent.
func TestDocsBitbucketRunbookReadConsumerNamesBothCallerClasses(t *testing.T) {
	section := flattenDoc(docSection(t, bitbucketRunbook, readDoc(t, bitbucketRunbook), bitbucketRunbookReadConsumerHeading))
	for _, want := range []string{
		"**person class**",
		"**coordinator class**",
		// What each class is, and the local command the person class is for.
		"own local",
		"coordinator status",
		"coordinator show",
		// The coordinator's half: the sweep, run by the job's own account.
		"sweep",
		"the coordinator account alone",
		// And the split itself, stated rather than implied.
		"one caller class per command",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's read-consumer section does not say %q: the read mint admits exactly two classes and the section has to name both where the consumer is written", bitbucketRunbook, want)
		}
	}
}

// TestDocsBitbucketRunbookReadConsumerStatesStopRotationAndTransfer: the three
// drills, each with the honest step rather than the one a reader would assume.
// Today no Fugaro command removes an entry from the bundle, so the stop switch
// is a hand-written version **and the disabling of the version it replaced** --
// a bundle write that leaves the previous version enabled keeps the consumer
// readable, which is the one thing removing it is for. The same care is why
// the section names the file's mode and its deletion: the bundle document
// holds every credential the installation has.
func TestDocsBitbucketRunbookReadConsumerStatesStopRotationAndTransfer(t *testing.T) {
	section := flattenDoc(docSection(t, bitbucketRunbook, readDoc(t, bitbucketRunbook), bitbucketRunbookReadConsumerHeading))
	for _, want := range []string{
		// The stop, honestly: no command removes an entry, so the version is
		// written by hand and the replaced one is disabled by hand.
		"No Fugaro command removes an entry from the bundle",
		"gcloud secrets versions add",
		"gcloud secrets versions disable",
		"the-version-you-replaced",
		// The document's own hazards while it exists on disk.
		"0600",
		"rm -f <bundle-file>",
		// The wider switch, which is §7's and stops the read mint with the
		// rest of the service, because the route reads the switches first.
		"fugaro budget kill --all",
		// The rotation: regenerate the secret at Bitbucket, write the bundle,
		// confirm a mint. U13 -- whether a token already minted survives the
		// rotation -- stays unsettled and is Check 148's to measure.
		"regenerate the consumer's secret",
		"confirm a mint",
		"**U13**",
		"check148.sh",
		// The transfer: a new consumer under the new owner, and the old one
		// deleted at Bitbucket, which is what actually ends it.
		"Ownership transfer",
		"new owner",
		"Delete the old consumer at Bitbucket",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's read-consumer section does not say %q", bitbucketRunbook, want)
		}
	}
}

// TestDocsBitbucketRunbookReadConsumerSectionIsLast stops the section being
// renumbered into the proxy's. §11 is the proxied token's rotation and the two
// credentials' stop switches differ in every step -- one is `fugaro creds set`
// of a different key, the other is a hand-written bundle version -- so a
// section merged into §11, or inserted before it, is an operator rotating the
// wrong secret while git traffic is already failing.
func TestDocsBitbucketRunbookReadConsumerSectionIsLast(t *testing.T) {
	doc := readDoc(t, bitbucketRunbook)
	proxy := strings.Index(doc, "## 11. The Bitbucket proxy")
	read := strings.Index(doc, bitbucketRunbookReadConsumerHeading)
	if proxy == -1 || read == -1 {
		t.Fatalf("%s no longer carries both the proxy's section (§11) and the read consumer's (§12)", bitbucketRunbook)
	}
	if read < proxy {
		t.Errorf("%s puts the read consumer's section before the proxy's: the numbers would then name the wrong secret", bitbucketRunbook)
	}
}

// TestDocsBitbucketRunbookTokenTypes: §11 of the runbook is what the owner
// reads to store, rotate and recover a proxy token, so it carries the order the
// service and the write have to be taken in, the two ways out of a broken mark,
// and the fact that a rotation is one write or two depending on which
// credential is being rotated.
//
// Mutations (verified, run and restored): the rollback bullet's "a deployed
// service older than the release that knows the family refuses the whole
// bundle" deleted -> FAIL on that pin, which is T1's fact and the reason the
// order is stated before the command rather than after it; the two ways out of a
// broken mark reduced to "no Fugaro command clears it" -> FAIL, which is the
// state that sends an operator looking for a command that does not exist; the
// rotation paragraph's "**A typed** token (`bitbucket-token--<name>`) is the
// **proxy's alone**" clause deleted -> FAIL, because the section then tells an
// operator that every rotation is two writes, which is true of the legacy entry
// alone. From the review of this round, the not-landed paragraphs came with the
// section: the third step of the order publishes nothing on this tree, because
// the node's member is the publisher's (T12) and no non-test code carries it,
// so the section states the step, what it does not do yet and what still serves
// -- the legacy entry, unchanged. Its pins fail alone when any one of those
// three halves is dropped.
//
// Mutation (verified, run and restored), from the second review, which blocked
// the pull request once T12 had landed: the bullet put back as "**nothing of
// the token reaches a run until the policy is published — and on this tree
// there is nothing to publish yet** ... **That third step publishes nothing on
// this release** ... which **no non-test code here carries**", with the
// token-stored-today sentence and the "do not read a stored typed token as live
// until T12 is in the release you run" tail in place of the update's own name
// and of the CLI upgrade, fails this on five pins -- the two headings, the
// stage that writes the key, the token that serves nothing between the two
// updates, the CLI upgrade and the release notes' line. That is the reading
// that is false now, and it is the reading an operator acts on: the section
// sends them to store a token the update they just ran does not publish.
func TestDocsBitbucketRunbookTokenTypes(t *testing.T) {
	doc := flattenDoc(readDoc(t, bitbucketRunbook))
	const heading = "## 11. The Bitbucket proxy"
	if !strings.Contains(doc, heading) {
		t.Fatalf("%s has no `%s` section: it is the operator's page for the proxy's tokens, and the section is where they are rotated and recovered", bitbucketRunbook, heading)
	}
	section := flattenDoc(docSection(t, bitbucketRunbook, doc, heading))
	for _, want := range []string{
		// The types, the command, and the order around it.
		"The proxy accepts any mix of the three token types — repository, project and workspace — and several at once, each stored under its own name",
		"`fugaro creds set bitbucket-token--<name>` asks for the type, the coverage and an optional expiry, checks the token against Bitbucket, and writes nothing at all when the check fails",
		"Because the token is chosen per request, one run can carry **more than one Bitbucket identity**, one per token that acted (D472)",
		"the audit line's `credential` is what says which token made one",
		"**The service moves first: `fugaro update`, then the write, then `fugaro update` again.**",
		// T1: the whole-bundle refusal a rolled-back service brings, and the
		// guard in the command that is not the whole of the care.
		"a deployed service older than the release that knows the family refuses the whole bundle",
		"the loader refuses a document carrying a key it does not know **whole** — every credential in it, the model credentials included",
		"fugaro creds set bitbucket-token--<name>` refuses that write itself where the deployed service is too old to read it",
		"**run `fugaro update` before the write**",
		// The third step of the order is the publish itself: the node's member
		// is the publisher's (T12), and T12 is in this release, so the section
		// says which stage writes the key, that a token stored between the two
		// updates serves nothing until the second one, what the publish does to
		// a run already going, and what an operator runs about the skew.
		"**nothing of the token reaches a run until the policy is published.**",
		"That update's policy stage is the publisher",
		"`projectpolicy.DeriveBitbucketTokens` derives the node's `bitbucket_tokens` key from each record of your local project config's typed tokens and from the identity and verification time the creds state store holds for it (R5, R12)",
		"A typed token stored **between** the two updates is in the bundle and in your local config and **serves no request** until the second one",
		"the legacy `bitbucket-token` above goes on serving exactly as it does now",
		"A project that records no typed token derives no key at all, so an installation holding the legacy entry alone converges with no step and no read of the store (R3)",
		"`docs/design/update-in-flight.md:62`",
		"**upgrade the CLI**",
		"the release notes' `### For operators` line, `docs/releases/v0.25.0.md`",
		// The mark: where it lives, what shows it, and the two ways out of one
		// no command clears.
		"A 401 that a read of a repository the token covers confirms marks **that one token** `broken`; a 429 marks it `exhausted` until `Retry-After`; a 403 or a 404 marks nothing.",
		"a **different node from the model half's** `creds/marks/<name>`, so `fugaro accounts clear` clears a model credential and never a Bitbucket token",
		"`credential` names the token that served the call and `status` carries the reaction it took",
		"**a token a 401 marked broken stays out of service, and no Fugaro command clears a Bitbucket token's mark in this release**",
		"**store the replacement under a name of its own**",
		"**clear the node by hand** — remove `creds/tokens/<name>` from the database",
		// What that node holds besides the hold, and what puts it back: the
		// identity and the verification time are the publisher's input (R12),
		// so an operator who clears the node by hand takes them with it and
		// gets them back by storing the token again.
		"That node is also where the token's **Identity** (the account UUID the add-time check proved) and **VerifiedAt** live, so removing it drops those two as well",
		"running `fugaro creds set bitbucket-token--<name>` again on that token records them again",
		"a token that is merely throttled comes back by itself and one that is broken does not",
		// F21/R5 read the other way round: the node is readable by every run,
		// and this page is where the operator sees that saying so is not a
		// promise that no run may read a token's reach.
		"**A rotation writes one copy per credential the proxy and a build both hold, while S4 is open.**",
		"A **typed** token (`bitbucket-token--<name>`) is the **proxy's alone**",
		"so rotating it is two writes of one new value and a rotation that misses the second leaves every build of the repository cloning with the token you just revoked",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("`%s` of %s does not say %q: the owner rotates and recovers the proxy's tokens from this section, and the order and the recovery are the two things a missing sentence costs them", heading, bitbucketRunbook, want)
		}
	}
}
