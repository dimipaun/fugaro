package cli

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The docs/mobile-auth/ folder, pinned. The folder holds four documents and no
// code: the owner's spec (spec.md, verbatim), the reconciliation of that spec
// against this tree (reconciliation.md), the index (README.md) and the plan
// that turns the owner's answers into ordered parts (implementation-plan.md).
// No Go, no TypeScript, no rules change, no Firebase configuration and no live
// action follows from any of them.
//
// This pin is P0 of that plan, and the plan says what it holds. Each of the
// seven tests below is one claim, and every one of them is a sentence a later
// edit can quietly falsify:
//
//   - TestDocsMobileAuthDocumentsExist -- the four documents are in the tree,
//     and the index's status line still says "plan only; no code is
//     implemented".
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
//   - TestDocsMobileAuthCarriesNoSecretShape -- no line of the four documents
//     carries a Firebase API key shape, a PEM header or a reverse-DNS identity
//     literal. This folder is *about* Firebase configuration, so the next
//     person to edit it is the one most likely to paste a real one in.
//   - TestDocsMobileAuthAnswersAreRecorded -- the owner's answers of
//     2026-10-22 (D221-D236, each as recommended; P0 and P1 approved) are
//     recorded in the three places the folder states its own state, and the
//     parts that still wait are named beside them, so the answers are not read
//     as an approval of the whole plan.
//
// **What this guard does not hold, said rather than left to be found.** It
// checks paths, not symbols: a symbol named beside a citation without the
// possessive form ("minted through `MintDeviceToken`" after a path) is outside
// it, for the reason `docs/coordinator/implementation-plan.md` gives of its own
// pair of checks in its P0 -- pairing an identifier with a citation needs a
// notion of scope the prose does not carry. It reads **this folder
// only**: it makes no claim about docs/mobile/, docs/central-orchestrator/,
// docs/coordinator/ or the check register, each of which its own pin holds --
// and it holds a citation into another folder's document to that path
// resolving, and to nothing about what the sentence there says.
const (
	mobileAuthReadme = "docs/mobile-auth/README.md"
	mobileAuthSpec   = "docs/mobile-auth/spec.md"
	mobileAuthRecon  = "docs/mobile-auth/reconciliation.md"
	mobileAuthPlan   = "docs/mobile-auth/implementation-plan.md"

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

	// The index's status line, in the two pieces the plan names it by: the
	// folder's own account of itself, whose second half says nothing here is
	// built yet.
	mobileAuthStatus      = "**Status: plan only.**"
	mobileAuthStatusBuilt = "No code is implemented"

	// mobileAuthNeverCarried is the one path these documents cite that is
	// excused **differently and permanently**, and it is not an entry in the
	// list below: mobile/node_modules is a dependency directory the repository
	// never carries, no pull request will ever land it, and the app's manifest
	// is the citation a reader should follow instead. The guard says so in its
	// own line rather than folding it into a list whose whole property is that
	// it empties.
	mobileAuthNeverCarried = "mobile/node_modules"
)

// mobileAuthDocuments is the folder's four documents, in the order the index
// lists them.
var mobileAuthDocuments = []string{mobileAuthReadme, mobileAuthSpec, mobileAuthRecon, mobileAuthPlan}

// TestDocsMobileAuthDocumentsExist: the folder is four documents, and the index
// still describes them as what they are. The status line is the index's own
// claim about the folder ("plan only; no code is implemented"), and the index
// is an index: it names the other three documents, so a reader who opens this
// folder first is sent to all of it.
//
// Mutation (verified, run and restored): delete a document and this test fails
// naming it; delete "no code is implemented" from the index's status line and
// it fails naming the sentence the plan pins.
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
	if !strings.Contains(readme, mobileAuthStatusBuilt) {
		t.Errorf("%s's status line no longer says %q: nothing in this folder is built, and the index is where a reader learns it first", mobileAuthReadme, mobileAuthStatusBuilt)
	}
	for _, name := range []string{"spec.md", "reconciliation.md", "implementation-plan.md"} {
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
// mobile Phase 6 tasks' (P6.8's, approved 2026-10-18 and unbuilt), and the
// reconciliation's §F23 names the borrowed ones at once. P6.6's
// `internal/budget/token/device.go` left the list in the pull request that
// landed it — the pairing plan's T2, "The phone's key, the typed fingerprint
// and the device token" (`docs/plans/2026-10-22-mobile-pairing-cli.md`,
// 2026-10-29) — which is the deletion every entry owes its own pull request.
// The list is held here rather than re-derived, so an implementer who finds
// another adds it visibly instead of loosening the rule.
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
var mobileAuthWillCreate = map[string]string{
	"docs/mobile-auth/provider-sign-in.md": "P6's provider path, written out and refused",
	"mobile/src/pairing":                   "mobile Task P6.8, approved 2026-10-18 and unbuilt",
	"mobile/src/overview":                  "mobile Task P6.8's overview screen, approved 2026-10-18 and unbuilt",
}

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
// (`rules.go:433`) and it fails on the same token; create
// `internal/budget/token/device.go` and it fails naming the entry and the pull
// request that owes its deletion.
func TestDocsMobileAuthCitationsResolve(t *testing.T) {
	dirs := mobileAuthTopLevelDirs(t)
	named := map[string]bool{}
	for _, doc := range []string{mobileAuthRecon, mobileAuthPlan} {
		paths := mobileAuthCitedPaths(readDoc(t, doc), dirs)
		// A filter that stopped matching would make every assertion below
		// vacuous: the reconciliation and the plan cite far more than this.
		if len(paths) < 40 {
			t.Fatalf("%s: mobileAuthCitedPaths found %d paths, and these two documents cite far more: the filter, not the documents, is what broke", doc, len(paths))
		}
		for _, p := range paths {
			named[p] = true
			switch {
			case p == mobileAuthNeverCarried:
				// Excused in its own line, permanently: the dependency
				// directory the repository never carries, whose citation is the
				// app's manifest.
				if _, err := os.Stat("../../mobile/package.json"); err != nil {
					t.Errorf("%s cites %s as the dependency directory this repository never carries, and the manifest that is the citation instead is not in the tree: %v", doc, p, err)
				}
			case mobileAuthWillCreate[p] != "":
				// Absence is asserted below, with the reason and the deletion
				// the entry owes.
			default:
				if _, err := os.Stat("../../" + p); err != nil {
					t.Errorf("%s cites %s, which is not in the tree: a path a reader cannot open is a dead end, and a path these documents expect to exist must not be excused", doc, p)
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
			t.Errorf("mobileAuthWillCreate excuses %s (%s), which neither the reconciliation nor the plan cites any more: the list must only excuse a path the documents actually name", p, why)
		}
		if _, err := os.Stat("../../" + p); err == nil {
			t.Errorf("%s is in the tree now, and mobileAuthWillCreate still excuses it (%s): the entry is stale and must be deleted by the pull request that lands it, because a path is excused only while it is missing", p, why)
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

// TestDocsMobileAuthCarriesNoSecretShape: no line of the four documents carries
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
