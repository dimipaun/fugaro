package cli

// docs/forge-labels/ — the owner's spec for the status labels the orchestrator's
// daemon keeps on its own pull requests, its reconciliation against the tree
// (D2430-D2440, F1-F26, R1-R8) and the plan that turns the owner's answers into
// slices (T6).
//
// What this file pins is the three documents T6 writes, and the pins are
// sentences rather than a shape: a label name, a namespace, a rate, the branch
// the service corroborates a pull request against, and the two facts a reader
// would otherwise take wrongly — that the labels are GitHub's alone and that a
// plain `fugaro run` is not labelled. Each of them is a claim about the code
// that a later edit can quietly falsify, which is what makes them worth pinning
// rather than asserting in prose.
//
// The code cross-checks are the second half, and they are the reason the
// documents are pinned to symbols and not only to substrings: the path the page
// names has to be the path the client and the service spell
// (creds.LabelPath and credsvc.LabelPath, which the service's own
// TestLabelRoutePathMatchesClient already holds to each other), the namespaces
// the page states have to be the ones gitprov.OwnedLabel answers for, and the
// Bitbucket sentence has to be the provider's own Supported. A sentence that
// keeps its words while the code under it moves fails here.
//
// What the file does NOT claim is that a pull request carries a label today,
// and the documents say why: the daemon's half is wired in this tree (T7's
// client and tick, #3096) and the identity the route is gated on landed with
// F19 and O2, so what is left is an installation's own update naming the
// account — which is what the release note's known limit states, and the last
// test below fails if it stops saying so.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
)

const (
	forgeLabelsProviders = "docs/git-providers.md"
	forgeLabelsSecurity  = "SECURITY.md"
	// forgeLabelsReleaseNotes is the notes of the next release, the file the
	// plan's T6 names as `docs/releases/vX.Y.Z.md`: the CLI stream is at
	// 0.25.0 (docs/release-streams.yaml) and 0.26.0's notes are written ahead
	// of the release, which is the order docs/releases/README.md asks for.
	forgeLabelsReleaseNotes = "docs/releases/v0.26.0.md"
	// forgeLabelsSection is the heading T6 adds to docs/git-providers.md.
	forgeLabelsSection = "## Status labels on Fugaro's pull requests"
	// forgeLabelsAdmitsTheDaemon is the one sentence all three documents carry
	// about the daemon's half of the feature having arrived: until F19 and O2
	// (2026-11-18) each of them said the daemon held no client for the route
	// and no installation named it, and the wiring that landed moved the three
	// together. It is written the same way in each, so they move together
	// again.
	forgeLabelsAdmitsTheDaemon = "the service admits the daemon as the installation's own orchestrator account"
)

// TestDocsForgeLabelsStatusSection: docs/git-providers.md carries the
// status-label section, with the labels, their namespaces, the two facts a
// reader would otherwise take wrongly (GitHub only; orchestrator-launched runs
// only, so a plain `fugaro run` is not labelled) and the one mechanism beside it
// that is unchanged (`git.pr.labels`). Its red state is this test: the section
// did not exist until T6 wrote it.
func TestDocsForgeLabelsStatusSection(t *testing.T) {
	doc := readDoc(t, forgeLabelsProviders)
	section := normSpace(docSection(t, forgeLabelsProviders, doc, forgeLabelsSection))

	for _, want := range []string{
		// The three labels and what each one means, with the colours the
		// service creates them with (D2437).
		"**`fugaro:running`** (blue `1D76DB`)",
		"**`fugaro:failed`** (red `D73A4A`)",
		"**`spec:<name>`** (grey `6E7781`)",
		// No success label, and why: ready already says it.
		"There is no success label",
		// GitHub only, and Bitbucket's own behaviour: a silent no-op rather
		// than a warning each tick (R7), which is a different thing from the
		// git.pr.labels warning the same document carries.
		"**GitHub only.**",
		"Bitbucket Cloud has no pull request labels, so every label operation there is a silent no-op",
		// Orchestrator-launched runs only, and the sentence a reader of a
		// plain `fugaro run` needs (D2432, F13).
		"**Orchestrator-launched runs only.**",
		"**a plain `fugaro run` is not labelled**",
		// The namespaces, and that a person's label is never touched (F23).
		"never touches a label outside the `fugaro:` and `spec:` namespaces",
		// git.pr.labels is a second mechanism and is unchanged (F1, R1).
		"**`git.pr.labels` is unchanged.**",
		"the status labels are written by the daemon after it sees the pull request",
		// The route: the service performs the act and no token leaves it.
		"**The credentials service performs the write, and no token leaves it.**",
		"`POST /v1/github/labels` (`internal/credsvc`)",
		"scoped to the one repository and to `{pull_requests: write, metadata: read}`",
		"caches it until near expiry and never returns it",
		// The name check, the corroboration and the rates, which are the
		// route's own rules rather than the daemon's (R3, F17, F22).
		"passes `^[a-z0-9][a-z0-9-]{0,43}$`, checked as ASCII before it is lowered",
		"its head branch is exactly `fugaro/<run id>`",
		"its author is Fugaro's own identity",
		"60 writes and 240 reads an hour per instance of the service, counted apart",
		// The labels are created on first use, so no second command
		// pre-creates them (D2440, F11).
		"The service creates its labels on first use, so nothing pre-creates them",
		// And the daemon's half, which arrived with F19 and O2.
		"**The daemon's half is wired, and the installation is what admits it.**",
		forgeLabelsAdmitsTheDaemon,
		"the daemon holds a client for the route",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's %q does not say %q: this is the section an operator reads to find out what the labels are and what writes them", forgeLabelsProviders, forgeLabelsSection, want)
		}
	}

	// The section is a second mechanism beside git.pr.labels and must not be
	// read as a replacement for it: the static list is still applied by the
	// run at ready, and a draft still carries none of it.
	if !strings.Contains(doc, "a draft carries no reviewers and no labels") {
		t.Errorf("%s no longer says %q: the draft rule is the other half of the label story, and the new section is written to leave it standing", forgeLabelsProviders, "a draft carries no reviewers and no labels")
	}
}

// TestDocsForgeLabelsSecurityStatesTheRoutesReach: SECURITY.md names the label
// route among what the service does and states its real reach in the words
// T3's code is written in — the service performs the write, no token leaves it,
// a stolen daemon identity can relabel only Fugaro's own pull requests with the
// allowed names, and an agent's session on the operator's machine can present
// that identity as it can for the merge route (F18).
func TestDocsForgeLabelsSecurityStatesTheRoutesReach(t *testing.T) {
	doc := readDoc(t, forgeLabelsSecurity)
	section := normSpace(docSection(t, forgeLabelsSecurity, doc,
		"### The GitHub App's key, when the credentials service holds it"))

	for _, want := range []string{
		// The route, its caller class and the reason it exists at all: a
		// label-only GitHub credential does not exist (F5, D2439).
		"**A label is read and written by the service too, for the installation's daemon and for nothing else (D2439, F5, F18).**",
		"`POST /v1/github/labels`",
		"GitHub has no label-only permission",
		"**the service performs the write itself and no token leaves it**",
		// The token it mints for that act, and that the caller cannot widen
		// it (U2).
		"scoped to the one repository and to `{pull_requests: write, metadata: read}`",
		// The names: the two status labels, R3's spec shape, ASCII before
		// lowering, and remove's two names alone (F26, R3).
		"`remove` takes `fugaro:running` and `fugaro:failed` alone",
		"checked as ASCII before it is lowered",
		// The corroboration, which is what bounds a hostile run's own record
		// (F17, O3).
		"its head branch must be exactly `fugaro/<run id>`",
		"its author must be Fugaro's own identity",
		// The rates, stated as the same per-instance best-effort bound the
		// merge mint's own paragraph states (F22).
		"60 writes and 240 reads an hour",
		"per instance of this service and no more",
		// One audit line per call, served or refused (C1.13).
		"writes exactly one audit line",
		// The reach, in T3's own words: the fence does not stop a session on
		// the daemon's own machine, and what bounds it is the acts (F18).
		"**What bounds a stolen daemon identity is the acts themselves",
		"a coding agent's session on the operator's machine can present the daemon's identity exactly as it can for the merge route above",
		"relabel **Fugaro's own pull requests**",
		"cannot write label text",
		"cannot reach a pull request Fugaro did not open",
		// And the daemon's half: the installation names it, the daemon mints
		// its own identity and the route is reached (F19, O2).
		forgeLabelsAdmitsTheDaemon,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's credentials-service section does not say %q: the label route is a write path on the App key, and its reach is what a reader of this document has to be able to check", forgeLabelsSecurity, want)
		}
	}

	// The route's paragraph belongs beside the merge mint's, which is the
	// other act the daemon's class reaches: a reader who finds one and not
	// the other has an incomplete picture of what that class may do.
	mint := strings.Index(doc, "A merge is minted by the service too")
	label := strings.Index(doc, "A label is read and written by the service too")
	if mint == -1 || label == -1 {
		t.Fatalf("%s no longer carries both the merge mint's paragraph and the label route's", forgeLabelsSecurity)
	}
	if label < mint {
		t.Errorf("%s states the label route before the merge mint: the label paragraph is written as the same class's second act and reads after the first", forgeLabelsSecurity)
	}
}

// TestDocsForgeLabelsReleaseNotes: the next release's notes carry the operator
// step the plan asks for — run `fugaro update`, which redeploys the service
// that performs every label read and write — and they carry the half that is
// not in the release as a known limit rather than leaving a reader to find out
// that nothing labels a pull request yet.
func TestDocsForgeLabelsReleaseNotes(t *testing.T) {
	doc := readDoc(t, forgeLabelsReleaseNotes)
	flat := normSpace(doc)

	// The notes say what the labels are, so the operator step below is about
	// something a reader has read about.
	for _, want := range []string{
		"`fugaro:running`",
		"`fugaro:failed`",
		"`spec:<name>`",
		"no credential leaves it",
		"GitHub only",
		"`git.pr.labels`",
		"a plain `fugaro run` is not labelled",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q: the notes are where a user reads what the labels are", forgeLabelsReleaseNotes, want)
		}
	}

	// The operator step, under the heading docs/releases/README.md names and
	// the website's changelog check recognises.
	operators := normSpace(docSection(t, forgeLabelsReleaseNotes, doc, "### For operators"))
	if !strings.Contains(operators, "run `fugaro update`") {
		t.Errorf("%s's `### For operators` does not say %q: the service that performs every label read and write is redeployed by that one command, and it is the whole of the step", forgeLabelsReleaseNotes, "run `fugaro update`")
	}
	if !strings.Contains(operators, "redeploys the credentials service") {
		t.Errorf("%s's `### For operators` does not say what the update does for the labels: %q", forgeLabelsReleaseNotes, "redeploys the credentials service")
	}
	if !strings.Contains(operators, "the labels are created by the service on first use") {
		t.Errorf("%s's `### For operators` does not say that nothing pre-creates the labels: a reader who expects a setup step goes looking for a command that does not exist", forgeLabelsReleaseNotes)
	}

	// The known limit, which is the same fact the two other documents state.
	// It names the identity rather than the client, because the client and the
	// tick are in the tree this release's notes are written for (T7, #3096):
	// what is missing on the next release's notes is the half F19 and O2 land,
	// and until an installation's update carries it nothing labels a pull
	// request.
	limits := normSpace(docSection(t, forgeLabelsReleaseNotes, doc, "### Known limits"))
	if !strings.Contains(limits, "no pull request carries") {
		t.Errorf("%s's `### Known limits` does not say that no pull request carries these labels yet: the notes must not read as a feature a user can see", forgeLabelsReleaseNotes)
	}
	if !strings.Contains(limits, "the class the service matches a caller against is empty until an installation's own Terraform names the account the daemon runs as") {
		t.Errorf("%s's `### Known limits` does not name the half that is missing: the identity the service admits the daemon as (F19, O2), which is the empty orchestrator class and not the client", forgeLabelsReleaseNotes)
	}
}

// TestDocsForgeLabelsClaimsMatchTheCode: every fact the three documents state
// about the tree is read back out of the tree. The path is the one the client
// and the service spell, the namespaces are the ones gitprov.OwnedLabel answers
// for, the Bitbucket sentence is the provider's own Supported, and the label
// names are the ones the service's constants hold. A document that keeps its
// words while the code under it moves fails here rather than shipping.
func TestDocsForgeLabelsClaimsMatchTheCode(t *testing.T) {
	docs := map[string]string{
		forgeLabelsProviders:    readDoc(t, forgeLabelsProviders),
		forgeLabelsSecurity:     readDoc(t, forgeLabelsSecurity),
		forgeLabelsReleaseNotes: readDoc(t, forgeLabelsReleaseNotes),
	}

	// The path: the documents name it, and the two spellings of it are the
	// one value (the service's own TestLabelRoutePathMatchesClient holds the
	// pair to each other; this is the documents' side of it).
	if creds.LabelPath != credsvc.LabelPath {
		t.Fatalf("the client spells the label route %q and the service %q: the documents name one path", creds.LabelPath, credsvc.LabelPath)
	}
	if creds.LabelPath != "/v1/github/labels" {
		t.Fatalf("creds.LabelPath is %q: the documents name /v1/github/labels, and a rename has to move them with it", creds.LabelPath)
	}
	for path, doc := range docs {
		if !strings.Contains(normSpace(doc), "`POST /v1/github/labels`") {
			t.Errorf("%s does not name `POST /v1/github/labels`, which is creds.LabelPath and credsvc.LabelPath", path)
		}
	}

	// The namespaces the documents state are the ones the code answers for,
	// and the two labels are the two the route's own constants hold.
	for _, name := range []string{"fugaro:running", "fugaro:failed", "spec:forge-labels"} {
		if !gitprov.OwnedLabel(name) {
			t.Errorf("gitprov.OwnedLabel(%q) is false, and the documents call it a namespace Fugaro writes", name)
		}
	}
	if gitprov.OwnedLabel("kind/bug") {
		t.Errorf("gitprov.OwnedLabel(%q) is true, and the documents say a label outside the two namespaces is never touched", "kind/bug")
	}

	// The Bitbucket sentence: the host has no pull request labels, so every
	// operation there is a silent no-op (R7).
	if (&bitbucket.Provider{}).Supported() {
		t.Errorf("%s says Bitbucket Cloud has no pull request labels, and bitbucket.Provider.Supported() is true", forgeLabelsProviders)
	}
}
