package cli

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The docs/mobile-auth/ folder, pinned. The folder holds five documents and no
// code: the owner's spec (spec.md, verbatim), the reconciliation of that spec
// against this tree (reconciliation.md), the index (README.md), the plan that
// turns the owner's answers into ordered parts (implementation-plan.md), and
// the path D221 refused, written out and not built (provider-sign-in.md). No
// Go, no TypeScript, no rules change, no Firebase configuration and no live
// action follows from any of them.
//
// This pin is P0 of that plan, and the plan says what it holds. Each of the
// nine tests below is one claim, and every one of them is a sentence a later
// edit can quietly falsify:
//
//   - TestDocsMobileAuthDocumentsExist -- the five documents are in the tree,
//     the index carries the folder's current status line, and it keeps the
//     status as first written beside it, labelled as the record it is.
//   - TestDocsMobileAuthSpecIsTheOwnersText -- spec.md is the owner's text:
//     its title, its six heading lines (the title and the five sections under
//     it) and its date line, and none of this folder's apparatus in it -- no
//     decision number, no finding number, no part number, and none of the
//     words the reconciliation and the plan are written in. "The spec stops
//     being verbatim" is a thing a later edit can do silently, which is why it
//     is checked rather than asserted.
//   - TestDocsMobileAuthDecisionsAreNumbered -- D221-D236, contiguous and in
//     order, each written as `### D<n> — …`, each with its Recommendation
//     before any Alternative or cost, and each carrying the owner's recorded
//     answer of 2026-10-22.
//   - TestDocsMobileAuthPartsAreInOrder -- P0-P6, contiguous and in order: the
//     plan's parts are an order, not a list.
//   - TestDocsMobileAuthCitationsResolve -- every repository path the
//     reconciliation and the plan cite resolves on disk, under the filter and
//     the excuse list below.
//   - TestDocsMobileAuthCarriesNoSecretShape -- no line of the five documents
//     carries a Firebase API key shape, a PEM header or a reverse-DNS identity
//     literal. This folder is *about* Firebase configuration, so the next
//     person to edit it is the one most likely to paste a real one in.
//   - TestDocsMobileAuthAnswersAreRecorded -- the owner's answers of
//     2026-10-22 (D221-D236, each as recommended; P0 and P1 approved) are
//     recorded in the three places the folder states its own state, and the
//     parts that still wait are named beside them, so the answers are not read
//     as an approval of the whole plan.
//   - TestDocsMobileAuthProviderSymbolsResolve -- the symbols
//     provider-sign-in.md cites are in the files it names them in, and the
//     document still names them. P6 added this census because that document's
//     whole value is that a reader can check it: it prices a path nobody took,
//     and a path that resolved to nothing when the day came would be the price
//     of a decision re-derived from a citation that had rotted.
//   - TestDocsMobileAuthProviderIsRefusedNotPlanned -- provider-sign-in.md
//     carries its own statement of what it is (refused, not a part, a task or a
//     plan), states that D221's answer is not reopened by it, and carries none
//     of the folder's ruling vocabulary. The plan says why this one is a check
//     and not a hope: the document *"describes an alternative and must not read
//     as a ruling"*, and "the owner has decided" is a sentence a later edit can
//     add by accident -- in a folder whose other documents write decisions as
//     `### D<n> — …` with an answer under each.
//
// **What this guard does not hold, said rather than left to be found.** It
// checks paths, and symbols only where a document pairs each one with the path
// it lives in (mobileAuthProviderSymbols): a symbol named loosely in prose
// ("minted through `MintDeviceToken`" after a path) is outside it, for the
// reason `docs/coordinator/implementation-plan.md` gives of its own pair of
// checks in its P0 -- pairing an identifier with a citation needs a notion of
// scope the prose does not carry. The reconciliation and the plan cite symbols
// in that loose shape throughout, and the two documents are held to their paths
// and their numbering instead. It reads **this folder
// only**: it makes no claim about docs/mobile/, docs/central-orchestrator/,
// docs/coordinator/ or the check register, each of which its own pin holds --
// and it holds a citation into another folder's document to that path
// resolving, and to nothing about what the sentence there says.
const (
	mobileAuthReadme = "docs/mobile-auth/README.md"
	mobileAuthSpec   = "docs/mobile-auth/spec.md"
	mobileAuthRecon  = "docs/mobile-auth/reconciliation.md"
	mobileAuthPlan   = "docs/mobile-auth/implementation-plan.md"

	// mobileAuthProvider is P6's document: D221's alternative, written out in
	// full and not built. It is the one document in this folder that describes
	// a path the tree does not take, and that is why it is held to three things
	// the others are not: the symbols it cites, and the two checks below.
	mobileAuthProvider = "docs/mobile-auth/provider-sign-in.md"

	// mobileAuthPin is this file, by path: P0's own commit adds it, and the
	// excuse list below is where that fact has to be recorded -- see the
	// comment on mobileAuthWillCreate. TestDocsMobileAuthCitationsResolve
	// asserts both halves of that: the path is in the tree, and it is not an
	// entry on the list.
	mobileAuthPin = "internal/cli/docs_mobile_auth_test.go"

	// mobileAuthAnswered is the line every decision carries under it: the
	// owner's answer of 2026-10-22, and the fact that it took the
	// recommendation. The plan is written to the recommendations, so an answer
	// the other way rewrites the parts that depend on it, which is why the
	// sentence is pinned rather than left to a reader to find.
	mobileAuthAnswered = "**Answered 2026-10-22: as recommended"

	// The index's status line, and the record it keeps beside it. The line states
	// the folder's state and points at docs/status.md, which carries the current
	// table; the paragraph under it is the status as first written, kept, and it
	// is labelled as the record it is -- the tree has moved since (P0 and P1 are
	// merged), and the line is where a reader learns that.
	mobileAuthStatus     = "**Status as of 2026-11-10: parked, 2/7 merged; see [docs/status.md](../status.md) for the current table.**"
	mobileAuthKeptStatus = "No code is implemented"

	// mobileAuthNeverCarried is the one path these documents cite that is
	// excused **differently and permanently**, and it is not an entry in the
	// list below: mobile/node_modules is a dependency directory the repository
	// never carries, no pull request will ever land it, and the app's manifest
	// is the citation a reader should follow instead. The guard says so in its
	// own line rather than folding it into a list whose whole property is that
	// it empties.
	mobileAuthNeverCarried = "mobile/node_modules"
)

// mobileAuthDocuments is the folder's five documents: what
// TestDocsMobileAuthDocumentsExist holds to the tree and what the secret-shape
// scan reads. The index lists them in its own order (README.md is its own last
// row); this is the list the two checks below walk, and a document added to the
// folder and not to it is a document nothing pins.
var mobileAuthDocuments = []string{mobileAuthReadme, mobileAuthSpec, mobileAuthRecon, mobileAuthPlan, mobileAuthProvider}

// TestDocsMobileAuthDocumentsExist: the folder is five documents, and the index
// still describes them as what they are. The status line is the index's own
// claim about the folder (its state, and the pointer to docs/status.md), and
// the index is an index: it names the other four documents, so a reader who
// opens this folder first is sent to all of it.
//
// Mutation (verified, run and restored): delete a document and this test fails
// naming it; delete the status line from the index and it fails naming the
// sentence the plan pins.
func TestDocsMobileAuthDocumentsExist(t *testing.T) {
	for _, doc := range mobileAuthDocuments {
		if _, err := os.Stat("../../" + doc); err != nil {
			t.Errorf("%s is not in the tree: %v", doc, err)
		}
	}
	readme := readDoc(t, mobileAuthReadme)
	if !strings.Contains(readme, mobileAuthStatus) {
		t.Errorf("%s no longer carries the status line %q: the folder says of itself what it is, and a reader who lost that line would take the plan for code", mobileAuthReadme, mobileAuthStatus)
	}
	if !strings.Contains(readme, mobileAuthKeptStatus) {
		t.Errorf("%s no longer keeps the status as first written, %q: the index is where a reader learns what this folder was, and a record deleted rather than kept leaves the folder looking as though it had always been where the tree is now", mobileAuthReadme, mobileAuthKeptStatus)
	}
	for _, name := range []string{"spec.md", "reconciliation.md", "implementation-plan.md", "provider-sign-in.md"} {
		if !strings.Contains(readme, name) {
			t.Errorf("%s no longer names %s: the index is the one place a reader is sent from, and a document it drops is a document nobody opens", mobileAuthReadme, name)
		}
	}
}

// mobileAuthSpecHeadings is spec.md's headings, line for line: the title and
// the five sections under it, which is the six the plan counts. The assertion
// is the whole list rather than a membership test, so a heading added, removed,
// renamed or reordered fails.
var mobileAuthSpecHeadings = []string{
	"# Fugaro Mobile App Auth Spec",
	"## Flow",
	"## Providers",
	"## Per-project Firebase sign-in",
	"## Allowlist and roles",
	"## Open questions",
}

var mobileAuthSpecHeadingRE = regexp.MustCompile(`(?m)^#{1,6} .*$`)

// mobileAuthSpecProvenance is the owner's own date and name line, which the
// index promises is reproduced with the text.
const mobileAuthSpecProvenance = "Oct 20, 2026 · @Dimi"

// mobileAuthSpecForeignRE are the folder's own numbering schemes: a decision
// number, a finding number, a part number. The spec the owner wrote has none of
// them, and one appearing in it is the two documents mixed.
var mobileAuthSpecForeignRE = []*regexp.Regexp{
	regexp.MustCompile(`\bD\d+\b`),
	regexp.MustCompile(`\bF\d+\b`),
	regexp.MustCompile(`\bP\d\b`),
}

// mobileAuthSpecForeignWords is the folder's own vocabulary for its apparatus.
// These are words the reconciliation and the plan cannot be written without, so
// one of them in the owner's text is an edit to the text rather than a copy of
// it.
var mobileAuthSpecForeignWords = []string{
	"Recommendation",
	"Alternative",
	"reconcil",
	"implementation-plan",
	"implementation plan",
	"Finding",
}

// TestDocsMobileAuthSpecIsTheOwnersText: spec.md is the owner's spec, copied
// alone. The folder's whole reading rests on it -- the reconciliation is only
// meaningful because it disagrees with the owner's words rather than with a
// paraphrase -- and the ways it stops being a copy are silent: a heading
// reworded, a decision number pasted in from the reconciliation, a paragraph
// annotated in place.
//
// Mutation (verified, run and restored): insert a decision number into the
// spec (or reword a heading) and this test fails naming it.
func TestDocsMobileAuthSpecIsTheOwnersText(t *testing.T) {
	spec := readDoc(t, mobileAuthSpec)
	if got := mobileAuthSpecHeadingRE.FindAllString(spec, -1); !slices.Equal(got, mobileAuthSpecHeadings) {
		t.Errorf("%s's headings are %q; the owner's spec is its title and the five sections under it, and a heading that moved or multiplied means the document was edited rather than copied", mobileAuthSpec, got)
	}
	if !strings.Contains(spec, mobileAuthSpecProvenance) {
		t.Errorf("%s no longer carries %q: the spec is the owner's text, its date and its name included, and the index promises exactly that", mobileAuthSpec, mobileAuthSpecProvenance)
	}
	for _, re := range mobileAuthSpecForeignRE {
		if got := re.FindString(spec); got != "" {
			t.Errorf("%s carries %q, which is this folder's own numbering rather than the owner's words: the spec is reproduced alone, and a number pasted into it is the reconciliation leaking into the text it disagrees with", mobileAuthSpec, got)
		}
	}
	for _, word := range mobileAuthSpecForeignWords {
		if strings.Contains(spec, word) {
			t.Errorf("%s carries %q, which is this folder's own vocabulary: the spec is the text as it was written, with nothing of ours in it", mobileAuthSpec, word)
		}
	}
}

// mobileAuthDecisionRE matches a decision heading: `### D221 — …`. The em dash
// is the separator the reconciliation's §4 uses for every one of the sixteen.
var mobileAuthDecisionRE = regexp.MustCompile(`(?m)^### (D\d+) — `)

// mobileAuthPartRE matches a part heading: `### P0 — …`.
var mobileAuthPartRE = regexp.MustCompile(`(?m)^### (P\d) — `)

// mobileAuthDecision is one decision: its number, and its text from its heading
// to the next decision's — the last one's runs to the end of section 4, where
// the next `## ` heading is.
type mobileAuthDecision struct{ number, body string }

// mobileAuthDecisions splits the reconciliation's §4 into its decisions, in the
// order they are written. §4 is the one section written as decisions, and the
// rest of the document uses no `### D<n> — …` heading, so the headings are the
// section.
func mobileAuthDecisions(doc string) []mobileAuthDecision {
	idx := mobileAuthDecisionRE.FindAllStringSubmatchIndex(doc, -1)
	var out []mobileAuthDecision
	for i, m := range idx {
		end := len(doc)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		} else if j := strings.Index(doc[m[1]:], "\n## "); j >= 0 {
			end = m[1] + j
		}
		out = append(out, mobileAuthDecision{number: doc[m[2]:m[3]], body: doc[m[0]:end]})
	}
	return out
}

// mobileAuthNoAlternative is the one decision that states no Alternative, and
// it is named rather than left to the check below to skip: D235 asks what v1 is
// and what is later, so it answers with a v1 list and a later list rather than
// with a choice between two things -- there is no second option to price. Every
// other decision states one, and a decision that loses its Alternative is a
// recording that stopped being the weighing the owner answered: the plan exposes
// the alternative it displaced so an answer against the recommendation is taken
// with its cost in front of the owner.
const mobileAuthNoAlternative = "D235"

// TestDocsMobileAuthDecisionsAreNumbered: the decisions are D221-D236,
// contiguous and in order, each recommendation-first with the alternative and
// its cost after it (D235 states none, and is the one named exception), and each
// carrying the owner's answer of 2026-10-22.
//
// A decision number is an allocation the owner made, so a number that moves or
// a gap that opens is not a formatting slip: it is this work claiming a number
// nobody gave it, or dropping one it was given. Recommendation-first is the
// same kind of claim from the other side: the plan is written to the
// recommendation, so a decision whose Alternative comes first is one a reader
// takes for a plan they do not have.
//
// Mutation (verified, run and restored): renumber one decision (D236 to D237)
// and this test fails naming the number and the position; delete a decision's
// Alternative -- any but D235's, which has none -- and it fails naming the
// decision that stopped pricing the option it displaced.
func TestDocsMobileAuthDecisionsAreNumbered(t *testing.T) {
	recon := readDoc(t, mobileAuthRecon)
	decisions := mobileAuthDecisions(recon)
	if len(decisions) != 16 {
		t.Fatalf("%s carries %d decisions written as `### D<n> — …`, and D221–D236 is sixteen: the headings, or the section, is what changed", mobileAuthRecon, len(decisions))
	}
	for i, d := range decisions {
		want := fmt.Sprintf("D%d", 221+i)
		if d.number != want {
			t.Errorf("%s's decision %d is %s, where D221–D236 contiguous and in order means %s: the number is an allocation the owner made, and one that moved is an allocation this work did not receive", mobileAuthRecon, i+1, d.number, want)
		}
		rec := strings.Index(d.body, "Recommendation")
		if rec < 0 {
			t.Errorf("%s's %s states no Recommendation: every decision is written recommendation first, because the plan is written to the recommendation and a part whose answer went the other way is rewritten", mobileAuthRecon, d.number)
			continue
		}
		alt := strings.Index(d.body, "Alternative")
		switch {
		case alt >= 0 && alt < rec:
			t.Errorf("%s's %s states its Alternative before its Recommendation: the recommendation is the half the plan follows, so it comes first and the alternative and its cost follow it", mobileAuthRecon, d.number)
		case alt < 0 && d.number != mobileAuthNoAlternative:
			t.Errorf("%s's %s states no Alternative, and %s is the only decision that states none: an answer against the recommendation is taken with its cost in front of the owner, and a decision that dropped the option it displaced dropped that cost with it", mobileAuthRecon, d.number, mobileAuthNoAlternative)
		}
		if !strings.Contains(d.body, mobileAuthAnswered) {
			t.Errorf("%s's %s carries no %q line: the owner answered all sixteen on 2026-10-22, and which answer each took is the one thing a reader of this section cannot infer from the text", mobileAuthRecon, d.number, mobileAuthAnswered)
		}
	}
}

// TestDocsMobileAuthPartsAreInOrder: the plan's parts are P0-P6, contiguous and
// in order. This is the same claim as the decisions' numbering in the other
// document: the parts are a dependency order (§2 and §6 of the plan argue for
// it), and a part renumbered or dropped turns the order into a list a reader
// picks from.
//
// Mutation (verified, run and restored): delete a part heading and this test
// fails naming the position.
func TestDocsMobileAuthPartsAreInOrder(t *testing.T) {
	plan := readDoc(t, mobileAuthPlan)
	parts := mobileAuthPartRE.FindAllStringSubmatch(plan, -1)
	if len(parts) != 7 {
		t.Fatalf("%s writes %d parts as `### P<n> — …`, and P0–P6 is seven: the headings, or the plan, is what changed", mobileAuthPlan, len(parts))
	}
	for i, m := range parts {
		want := fmt.Sprintf("P%d", i)
		if m[1] != want {
			t.Errorf("%s's part %d is %s, where P0–P6 contiguous and in order means %s: the parts are a dependency order, and one that moved is a reader planning work the tree cannot carry yet", mobileAuthPlan, i+1, m[1], want)
		}
	}
}

// mobileAuthCitationRE matches a backticked token. The folder writes a path as
// `internal/cli/pair.go`, a directory as `mobile/src/pairing/` and a package
// directory as `internal/localcfg`.
var mobileAuthCitationRE = regexp.MustCompile("`([^`\n]+)`")

// mobileAuthProseChars are the characters a backticked token carries when it is
// prose rather than a citation: whitespace (a command line, a sentence), a
// hedge, an em dash, a glob. The plan states the rule, and it is what keeps
// `fugaro pair --email` and a node path with a pattern in it out of the census.
const mobileAuthProseChars = " \t|—*?[]{}<>"

// mobileAuthTopLevelDirs is the set of the repository's top-level directories,
// **read from the tree at run time** rather than written into this file. That is
// the plan's rule and it is the whole reason the census can tell a file from the
// two things these documents cite constantly and no guard can open: a Realtime
// Database node path (`config/admin/devices`) and an IAM role
// (`roles/firebasedatabase.admin`) start with no repository directory, so they
// are not citations. A directory added later is covered without an edit here,
// and a directory renamed breaks the check loudly.
func mobileAuthTopLevelDirs(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir("../..")
	if err != nil {
		t.Fatalf("reading the repository root: %v", err)
	}
	dirs := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			dirs[e.Name()] = true
		}
	}
	// A walk that found nothing would make every citation below pass.
	for _, want := range []string{"docs", "internal", "mobile"} {
		if !dirs[want] {
			t.Fatalf("the repository root, read from internal/cli, has no %s/ directory: the walk, not the tree, is what broke", want)
		}
	}
	return dirs
}

// mobileAuthCitedPaths is every repository path a document cites: a backticked
// token, with no prose characters in it, that contains a `/` and whose first
// segment is a top-level directory of this repository. The token is used as
// written -- a trailing slash is a spelling of a directory and is stripped, but
// a line number (`rules.go:433`) is not: it is a citation that does not resolve,
// which is the mutation the plan names.
func mobileAuthCitedPaths(doc string, dirs map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range mobileAuthCitationRE.FindAllStringSubmatch(doc, -1) {
		token := m[1]
		if strings.ContainsAny(token, mobileAuthProseChars) {
			continue
		}
		if !strings.Contains(token, "/") {
			continue
		}
		if !dirs[strings.SplitN(token, "/", 2)[0]] {
			continue
		}
		token = strings.TrimSuffix(token, "/")
		if seen[token] {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	slices.Sort(out)
	return out
}

// mobileAuthWillCreate is the excuse list, by name, each entry with the part
// that owes its deletion: the paths these documents cite that are not in the
// tree yet. Each is excused **only while it does not exist**, and the guard
// fails on an entry that resolves -- which is the property that makes the list
// self-cleaning: the pull request that lands P1 or Phase 6 deletes its entry in
// the same change, and a reader can see from the list alone what is still
// missing.
//
// The plan writes three of these as this work's own new paths and two as the
// mobile Phase 6 tasks', and the reconciliation's §F23 names the borrowed ones
// at once. P6.6's `internal/budget/token/device.go` left the list in the pull
// request that landed it — the pairing plan's T2, "The phone's key, the typed
// fingerprint and the device token" (`docs/plans/2026-10-22-mobile-pairing-cli.md`,
// 2026-10-29) — and P6.8's two, `mobile/src/pairing` and `mobile/src/overview`,
// left it in the pull request that landed that task, which is the deletion
// every entry owes its own pull request. The list is held here rather than
// re-derived, so an implementer who finds another adds it visibly instead of
// loosening the rule.
//
// **The third of this work's own three is not here, and that is the rule
// working**: `internal/cli/docs_mobile_auth_test.go` is the file this pin is
// written in, P0's own commit is the one that landed it, and its entry left the
// list in that commit -- the same deletion every other entry owes its own pull
// request. `internal/cli/pair.go`'s entry left the same way, in the pull request
// that landed the command's surface (T1a of the pairing plan), while the wait
// that makes `fugaro pair` a whole act has not. mobile/node_modules is excused
// differently and permanently, which is why it is not an entry here either: see
// mobileAuthNeverCarried.
//
// **The list is empty now, and P6 is the part that emptied it.** Its last entry
// was `docs/mobile-auth/provider-sign-in.md`, and the pull request that landed
// that document -- P6's own -- is the one that deleted the entry, which is the
// same rule read for the last time. What the map is kept for is the other
// direction of it: an implementer who finds a path these documents cite and the
// tree does not have adds it **here**, visibly and with the part that owes its
// deletion, rather than loosening the path rule above.
var mobileAuthWillCreate = map[string]string{}

// TestDocsMobileAuthCitationsResolve: every repository path the reconciliation
// and the plan cite resolves on disk, and every entry on the excuse list is
// still excused for a reason. A citation is how these documents carry their
// whole value -- the reconciliation is a reading of the tree, and the plan is a
// set of parts against it -- so a path that stops resolving is a claim nobody
// can check, and an excuse that has gone stale is a path that resolved while
// the guard still looked past it.
//
// Mutation (verified, run and restored): rename a cited path in a scratch tree
// and this test fails naming it; append a line number to a citation
// (`rules.go:433`) and it fails on the same token; name a path the tree does not
// have in the provider document (`internal/cli/no-such-file.go`) and it fails
// naming it, which is the red step P6's own part writes; put a path back on
// mobileAuthWillCreate and it fails on the entry's staleness.
func TestDocsMobileAuthCitationsResolve(t *testing.T) {
	dirs := mobileAuthTopLevelDirs(t)
	named := map[string]bool{}
	// The floor is per document, and it is what keeps a filter that stopped
	// matching from making every assertion below vacuous. The provider document
	// cites fewer paths than the other two -- it is one path written out, not a
	// reading of the whole tree -- so its floor is its own.
	for _, doc := range []struct {
		path string
		min  int
	}{{mobileAuthRecon, 40}, {mobileAuthPlan, 40}, {mobileAuthProvider, 20}} {
		paths := mobileAuthCitedPaths(readDoc(t, doc.path), dirs)
		if len(paths) < doc.min {
			t.Fatalf("%s: mobileAuthCitedPaths found %d paths, and this document cites at least %d: the filter, not the document, is what broke", doc.path, len(paths), doc.min)
		}
		for _, p := range paths {
			named[p] = true
			switch {
			case p == mobileAuthNeverCarried:
				// Excused in its own line, permanently: the dependency
				// directory the repository never carries, whose citation is the
				// app's manifest.
				if _, err := os.Stat("../../mobile/package.json"); err != nil {
					t.Errorf("%s cites %s as the dependency directory this repository never carries, and the manifest that is the citation instead is not in the tree: %v", doc.path, p, err)
				}
			case mobileAuthWillCreate[p] != "":
				// Absence is asserted below, with the reason and the deletion
				// the entry owes.
			default:
				if _, err := os.Stat("../../" + p); err != nil {
					t.Errorf("%s cites %s, which is not in the tree: a path a reader cannot open is a dead end, and a path these documents expect to exist must not be excused", doc.path, p)
				}
			}
		}
	}
	// This file is the third of this work's own three new paths, and it is
	// deliberately not on the list: P0's own pull request is the one that landed
	// it, so it was the one that owed the entry's deletion. That is the same
	// rule as every entry below, read the other way, and it is asserted rather
	// than left to the comment that says it.
	if _, excused := mobileAuthWillCreate[mobileAuthPin]; excused {
		t.Errorf("mobileAuthWillCreate excuses %s, which is this file: it was excused only while it did not exist, and the pull request that landed it -- the one this file is in -- owed its deletion", mobileAuthPin)
	}
	if _, err := os.Stat("../../" + mobileAuthPin); err != nil {
		t.Errorf("%s is not in the tree: %v", mobileAuthPin, err)
	}

	for p, why := range mobileAuthWillCreate {
		if !named[p] {
			t.Errorf("mobileAuthWillCreate excuses %s (%s), which none of these documents cites any more: the list must only excuse a path the documents actually name", p, why)
		}
		if _, err := os.Stat("../../" + p); err == nil {
			t.Errorf("%s is in the tree now, and mobileAuthWillCreate still excuses it (%s): the entry is stale and must be deleted by the pull request that lands it, because a path is excused only while it is missing", p, why)
		}
	}
}

// mobileAuthProviderSymbols is the provider document's citations of code: each
// symbol with the file it lives in. P6's red step is a citation -- "move a cited
// symbol and the folder's guard must fail, naming it" -- and this is the census
// that makes it true. It is a list rather than a derivation, and deliberately:
// the folder's other two documents name symbols loosely in prose ("`USER` is a
// minted `mu` claim and the device node"), where pairing an identifier with a
// citation needs a notion of scope the prose does not carry
// (`docs/coordinator/implementation-plan.md`'s P0 says the same of its own pair
// of checks). The provider document is short and its whole subject is code a
// reader can walk, so every symbol it names is paired with its file here -- and
// the reverse list below is the other half, which is what keeps the pairing from
// being decorative.
var mobileAuthProviderSymbols = map[string][]string{
	"internal/cli/init_firebase.go": {
		"func refusePublicSignUp(", "func workspaceSignInPath(",
		"func (r *initRun) identityPlatform(", "func (s *idpStep) initialize(",
		"FirebaseAPIKey",
	},
	"internal/infra/identityplatform.go": {
		"func PublicSignUp(", "func (p *IdentityPlatform) Initialize(", "func (p *IdentityPlatform) Config(",
		"defaultSupportedIdpConfigs", "oauthIdpConfigs", "signIn.email.enabled",
		"client.permissions.disabledUserSignup is false",
	},
	// The macros the document names as what a provider session would reach, and
	// the device node it would keep reading (D221's recommendation).
	"internal/budget/rules/rules.go": {"PathDevices", "userClaim", "\"USER\":", "\"TOKEN\":", "\"PAIRING\":", "\"MAILBOX\":"},
	// The one verified-email check the tree has, and the userinfo read it is
	// over -- a precedent for the shape, not an implementation of it.
	"internal/cli/init_members.go": {"func userEmail(", "var authenticatedUser = func(", "is not verified"},
	// The custom-token path, which is what the public API key buys today.
	"internal/budget/token/mint.go":     {"func Mint("},
	"internal/budget/token/exchange.go": {"func Exchange(", "accounts:signInWithCustomToken"},
	// The generation a first rule reading a provider claim would have to bump,
	// and the two things the fields would move.
	"internal/projectpolicy/version.go": {"const RulesVersion = 4"},
	// The proof vehicles of section 6: the denial table, the synthetic token
	// builder it drives, and the node's own gate.
	"internal/budget/rules/emulator_test.go":         {"func TestDenialTable("},
	"internal/budget/rules/harness_test.go":          {"func unsignedToken("},
	"internal/budget/rules/emulator_pairing_test.go": {"func TestDeviceNodeGatesEverythingUserReaches("},
	// What a client meets on the device node, either way.
	"internal/budget/rules/rules.json.tmpl": {"\"$uid\": { \".read\": false, \".write\": false }"},
	// The app's half of D221: two items, an identity the repository does not
	// commit, and the config resolver the identity is built in.
	"mobile/src/accounts/secret.js": {"export const SECRET_ITEMS = Object.freeze(['idToken', 'macKey']);"},
	"mobile/src/app-config.test.js": {"no reverse-DNS identity is committed here"},
	"mobile/app.config.js":          {"export function resolveConfig(", "export const ID_VARIABLES = Object.freeze({"},
	// The Firebase packages the app carries today -- which is why the fence is
	// "no provider and no Auth package" rather than "no Firebase".
	"mobile/package.json": {
		"\"@react-native-firebase/app\"", "\"@react-native-firebase/analytics\"",
		"\"@react-native-firebase/crashlytics\"",
	},
	// Identity Platform is not a resource, and the workspace's own project is the
	// one where the pin lives rather than the refusal.
	"deploy/terraform/gcp/modules/firebase/firebase.tf": {"Identity Platform is deliberately not a resource here"},
	"docs/workspaces/spec.md":                           {"A member's Firebase refuses public sign-up, and the workspace's is the one that does not"},
	// The live reading of the workspace's pin, which is the shape a per-project
	// check for the provider path would have to copy. It names no symbol of this
	// repository's own -- it reads the API -- so what it is held to is the
	// refusal and the two lists it holds the pin against.
	"scripts/live-checks/check361.sh": {"refusePublicSignUp", "defaultSupportedIdpConfigs", "oauthIdpConfigs"},
}

// mobileAuthProviderNames is the reverse direction: the symbols the document
// must still name. A symbol renamed in the tree and left in the document is the
// same dead end as one that left the tree, and a document that quietly dropped
// one is a claim a reader can no longer follow -- which is the failure this half
// is written for.
var mobileAuthProviderNames = []string{
	"refusePublicSignUp", "workspaceSignInPath", "identityPlatform", "idpStep.initialize",
	"PublicSignUp", "Initialize", "Config",
	"defaultSupportedIdpConfigs", "oauthIdpConfigs", "signIn.email.enabled",
	"signIn.anonymous.enabled", "signIn.phoneNumber.enabled", "disabledUserSignup",
	"PathDevices", "USER", "TOKEN", "RUN", "PAIRING", "MAILBOX",
	"userEmail", "authenticatedUser", "Mint", "Exchange", "accounts:signInWithCustomToken",
	"RulesVersion", "unsignedToken", "TestDenialTable", "TestDeviceNodeGatesEverythingUserReaches",
	"SECRET_ITEMS", "resolveConfig", "ID_VARIABLES", "@react-native-firebase",
	"@react-native-firebase/auth", "email", "email_verified", "auth.uid",
	"assetlinks.json", "apple-app-site-association", "google-services.json",
	"GoogleService-Info.plist", "@privaterelay.appleid.com", "signInWithCredential",
	"config/admin/devices/$uid", "mobile/.gitignore", "firebase.json",
	"scripts/live-checks/check361.sh", "docs/design/project-centric.md",
	"docs/workspaces/spec.md", "docs/pending-workflows/",
}

// TestDocsMobileAuthProviderSymbolsResolve: the symbols provider-sign-in.md
// cites are still in the files it names them in, and the document still names
// them. This is the census P6's own part writes, and it exists because the
// document's whole value is that a reader can check it: it prices a path nobody
// took, and a price that no longer resolves is a decision re-derived from a
// citation that had rotted.
//
// Mutation (verified, run and restored): rename a cited symbol in a scratch tree
// (`refusePublicSignUp` to `refuseProviderSignIn`) and this test fails naming the
// symbol and its file; delete a symbol from the document and the reverse half
// fails naming it.
func TestDocsMobileAuthProviderSymbolsResolve(t *testing.T) {
	doc := readDoc(t, mobileAuthProvider)
	for path, symbols := range mobileAuthProviderSymbols {
		data, err := os.ReadFile("../../" + path)
		if err != nil {
			t.Errorf("reading %s: %v", path, err)
			continue
		}
		for _, symbol := range symbols {
			if !strings.Contains(string(data), symbol) {
				t.Errorf("%s cites %s's %s, which is no longer there: the document prices a path of the tree as it is, and a citation that has moved is a price nobody can check", mobileAuthProvider, path, symbol)
			}
		}
	}
	for _, name := range mobileAuthProviderNames {
		if !strings.Contains(doc, name) {
			t.Errorf("%s no longer names %s: a citation the document drops is a reader who cannot find what it points at", mobileAuthProvider, name)
		}
	}
}

const (
	// mobileAuthProviderStatus is the document's own statement of what it is.
	// The one thing a reader must not take it for is a plan: D221's alternative
	// is refused, `docs/pending-workflows/` is the model, and a document that
	// lost this line would look like work waiting to be done.
	mobileAuthProviderStatus = "**Status: refused. Nothing below is built, planned or approved, and this document is not a part, a task or a plan.**"

	// mobileAuthProviderPurpose is the sentence the plan writes for this
	// document's purpose, and it is the reason the folder carries a document
	// instead of a paragraph.
	mobileAuthProviderPurpose = "the next person who wants this should read a document, not re-derive a decision"

	// mobileAuthProviderNoRuling is the document saying out loud what the checks
	// below hold it to: it is a description of an alternative to a decision that
	// is answered, and it does not reopen it.
	mobileAuthProviderNoRuling = "No decision is taken here, and D221's answer is not reopened by it."
)

// mobileAuthProviderRulings is the folder's ruling vocabulary, and the
// provider document may carry none of it. `**Answered` is the shape
// reconciliation.md §4 writes each of D221-D236's answers in; "the owner has
// decided" is the sentence the plan's P6 names in its own red step -- *"write a
// decision as if it were answered ... and the guard's verbatim/decision checks
// must fail, because this document describes an alternative and must not read as
// a ruling"*; and the two "we will" phrases are what turns a description into a
// commitment. A reader who meets any of them here has met a document claiming a
// decision nobody took.
var mobileAuthProviderRulings = []string{
	"the owner has decided",
	"the owner decided",
	"has been decided",
	"**Answered",
	"we will build",
	"we will implement",
	"this document plans",
}

// TestDocsMobileAuthProviderIsRefusedNotPlanned: the provider document says what
// it is, and says what it is not. Three sentences are pinned -- its status line,
// its purpose, and the line that refuses to be a ruling -- because each is a
// sentence a later edit can drop while leaving the rest of the document reading
// like a plan. And its own claim about the app's half of D221 is checked against
// the tree rather than taken on its word: `mobile/package.json` carries the
// three `@react-native-firebase` packages and **no auth package**, which is
// exactly the fence the document says the path would cross.
//
// Mutation (verified, run and restored): write "the owner has decided" into the
// document and this test fails naming the phrase; add
// `@react-native-firebase/auth` to `mobile/package.json` and it fails naming the
// dependency, because the document would then be describing a tree that no
// longer matches it.
func TestDocsMobileAuthProviderIsRefusedNotPlanned(t *testing.T) {
	// Sentences here are prose and wrap, so both sides are compared with their
	// whitespace flattened, the way the neighbouring pins read a paragraph: a
	// sentence moved across a line break is the same sentence, and a check that
	// failed on the wrapping would be fixed by rewrapping it rather than by
	// restoring what it says. The ruling scan below reads the flattened text for
	// the same reason, and it flattens the other way too: a phrase broken across
	// a line is a phrase the document carries.
	doc := normSpace(readDoc(t, mobileAuthProvider))
	for _, want := range []struct{ sentence, why string }{
		{mobileAuthProviderStatus, "the document states what it is, and the one thing a reader must not take it for is a plan or a part"},
		{mobileAuthProviderPurpose, "the plan writes this document's purpose, and it is why a reader is sent here rather than to a decision that was re-derived"},
		{mobileAuthProviderNoRuling, "the document describes an alternative to an answered decision, and saying so is what keeps it from reading as a reopening"},
	} {
		if !strings.Contains(doc, normSpace(want.sentence)) {
			t.Errorf("%s no longer carries %q: %s", mobileAuthProvider, want.sentence, want.why)
		}
	}
	for _, phrase := range mobileAuthProviderRulings {
		if strings.Contains(doc, phrase) {
			t.Errorf("%s carries %q, which is this folder's ruling vocabulary: the provider path was refused, nothing here is a decision, and a document that read as one would send a reader to build what D221 answered against", mobileAuthProvider, phrase)
		}
	}
	// The scan above is only meaningful if the vocabulary exists somewhere: the
	// reconciliation writes an answer under every one of the sixteen decisions,
	// and this is that control.
	if !strings.Contains(readDoc(t, mobileAuthRecon), mobileAuthAnswered) {
		t.Fatalf("%s no longer carries %q, so the scan above proves nothing: the control, not the provider document, is what broke", mobileAuthRecon, mobileAuthAnswered)
	}

	// The document's own claim about the app, held to the tree: the three
	// packages are the control (the file is really read), and the auth package
	// is the claim.
	pkg := readDoc(t, "mobile/package.json")
	if !strings.Contains(pkg, "@react-native-firebase/app") {
		t.Errorf("mobile/package.json no longer carries @react-native-firebase/app, which %s names as what the app does carry: the check below would pass on a file that was not read", mobileAuthProvider)
	}
	if strings.Contains(pkg, "firebase/auth") {
		t.Errorf("mobile/package.json carries a Firebase Auth package, and %s states that it lists none: that is the app's half of D221 -- no provider session and no Auth package -- and a path that took D221's alternative changes this document in the same change", mobileAuthProvider)
	}

	// ... and the configuration half of the same claim: an `auth` block in
	// either manifest is where Identity Platform's providers are enabled from a
	// project's own repository, which is the first line of the path. The key
	// beside each file is the control, so a check that read nothing fails here
	// rather than passing quietly.
	for _, f := range []struct{ path, control string }{
		{"firebase.json", "\"hosting\""},
		{"mobile/firebase.json", "\"react-native\""},
	} {
		text := readDoc(t, f.path)
		if !strings.Contains(text, f.control) {
			t.Errorf("%s no longer carries %s, so the check beside it proves nothing: the control, not the document, is what broke", f.path, f.control)
			continue
		}
		if strings.Contains(text, "\"auth\"") {
			t.Errorf("%s carries an `auth` block, and %s states that neither manifest does: an `auth` block is where Identity Platform's providers are configured from a project's own repository, and a file that has grown one has started the path this document describes", f.path, mobileAuthProvider)
		}
	}
}

// mobileAuthSecretShapes are the three shapes the plan names, in the shapes this
// repository already refuses elsewhere: a Firebase web API key
// (`mobile/src/accounts/settings.js`'s own pattern, `internal/config`'s token
// list), a PEM header, and a reverse-DNS identity literal -- the shape of the
// app's store identity, which D125 keeps out of this repository.
var mobileAuthSecretShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a Firebase API key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{10,}`)},
	{"a PEM header", regexp.MustCompile(`-----BEGIN[A-Z ]*-----`)},
	{"a reverse-DNS identity literal", regexp.MustCompile(`\b(?:com|org|net|io|ai|dev|co)\.[A-Za-z0-9][A-Za-z0-9.-]*`)},
}

// mobileAuthSecretShape returns the name of the first shape line carries, or ""
// if it carries none.
func mobileAuthSecretShape(line string) string {
	for _, s := range mobileAuthSecretShapes {
		if s.re.MatchString(line) {
			return s.name
		}
	}
	return ""
}

// TestDocsMobileAuthCarriesNoSecretShape: no line of the five documents carries
// a credential-shaped literal. The plan says why this one is unusual: the
// folder is *about* Firebase configuration, so the next person to edit it is
// the one most likely to paste a real key in -- and a document is where a key
// pasted once stays.
//
// The scan is walked with planted values first, so a green run means the walk
// can fail, and with the two clean strings the documents actually carry, so a
// check that fails on everything fails here rather than passing quietly.
//
// Mutation (verified, run and restored): paste an `AIza…`-shaped string into
// the reconciliation and this test fails naming the document and the line.
func TestDocsMobileAuthCarriesNoSecretShape(t *testing.T) {
	for _, planted := range []struct{ line, shape string }{
		{"    firebase_api_key: AIzaSyA0123456789abcdefghijklmnopqrstu", "a Firebase API key"},
		{"-----BEGIN PRIVATE KEY-----", "a PEM header"},
		{"  ios.bundleIdentifier: com.fugaro.app", "a reverse-DNS identity literal"},
	} {
		if got := mobileAuthSecretShape(planted.line); got != planted.shape {
			t.Fatalf("%q is reported as %q, and it is %s: the scan is what broke, not the documents", planted.line, got, planted.shape)
		}
	}
	// The plan writes the key's shape as `AIza…`, whose ellipsis is not key
	// material, and the documents name real things that are not identities. A
	// scan that flagged these would be turned off rather than fixed.
	for _, clean := range []string{
		"shape (`AIza…`), a PEM header, or a reverse-DNS identity literal",
		"`mobile/app.json` carries no `ios.bundleIdentifier`",
		"`signIn.email.enabled` is the first field `PublicSignUp` reports",
		"holding `roles/firebasedatabase.admin` can do to an entry",
	} {
		if got := mobileAuthSecretShape(clean); got != "" {
			t.Errorf("%q is reported as %s: the scan must tell a document talking about a shape from the shape itself", clean, got)
		}
	}
	for _, doc := range mobileAuthDocuments {
		for i, line := range strings.Split(readDoc(t, doc), "\n") {
			if got := mobileAuthSecretShape(line); got != "" {
				t.Errorf("%s:%d carries %s: this folder is about Firebase configuration, and a value pasted here is a value in a public repository", doc, i+1, got)
			}
		}
	}
}

// TestDocsMobileAuthAnswersAreRecorded: the folder is not only a plan any more
// -- the owner answered it -- and the three places it states its own state say
// so, each with the same facts. The decisions themselves carry their answers
// (TestDocsMobileAuthDecisionsAreNumbered); what this test holds is that the
// folder's headers agree with them, that the sentences which said nothing was
// answered are gone rather than contradicted in place, and that the parts which
// still wait are named beside the approval, so "the owner answered" is not read
// as "the plan is open".
//
// Mutation (verified, run and restored): put "nothing here is answered" back in
// the reconciliation's §4 and this test fails naming the sentence; drop the
// index's answer line and it fails naming the index.
func TestDocsMobileAuthAnswersAreRecorded(t *testing.T) {
	readme := readDoc(t, mobileAuthReadme)
	recon := readDoc(t, mobileAuthRecon)
	plan := readDoc(t, mobileAuthPlan)

	for _, want := range []struct{ path, text, sentence string }{
		{mobileAuthReadme, readme, "The owner's ruling of **2026-10-22**"},
		{mobileAuthRecon, recon, "answered by the owner on 2026-10-22, each as recommended"},
		{mobileAuthPlan, plan, "**Amended 2026-10-22:**"},
		{mobileAuthPlan, plan, "**Passed 2026-10-22.**"},
	} {
		if !strings.Contains(want.text, want.sentence) {
			t.Errorf("%s no longer carries %q: the owner answered D221–D236 as recommended on 2026-10-22, and the documents a reader meets first have to say so where they say what this folder is", want.path, want.sentence)
		}
	}

	// The sentences that said the opposite, and which no later reader may find
	// again: each was true when it was written and is false of the folder now.
	// They are checked in the documents they were written in rather than left
	// to a reader to reconcile with the answers above them. The status lines of
	// 2026-10-21 are the exception: main keeps them as written under a dated
	// amendment, so they are history and not checked here.
	for _, gone := range []struct{ path, text, sentence string }{
		{mobileAuthRecon, recon, "Nothing here is answered"},
		{mobileAuthRecon, recon, "nothing in this folder is approved"},
	} {
		if strings.Contains(gone.text, gone.sentence) {
			t.Errorf("%s still says %q: the owner answered all sixteen decisions on 2026-10-22, so a folder that says otherwise sends a reader to ask for an answer it already has", gone.path, gone.sentence)
		}
	}

	// The approval opened two parts and named the ones it did not open: an
	// answer is not an approval of the plan, and the parts that wait on the
	// mobile Phase 6 tasks wait on them still.
	for _, want := range []struct{ path, text, sentence string }{
		{mobileAuthReadme, readme, "P0 and P1"},
		{mobileAuthPlan, plan, "P0 and P1"},
		{mobileAuthPlan, plan, "P3, P4 and P5"},
	} {
		if !strings.Contains(want.text, want.sentence) {
			t.Errorf("%s no longer says %q: the answers opened P0 and P1 and nothing else, and a reader who cannot tell which parts wait will plan work the Phase 6 tasks have to land first", want.path, want.sentence)
		}
	}
}
