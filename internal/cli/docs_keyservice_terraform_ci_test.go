package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The key service's Terraform suite, and the documents that say whether CI runs
// it — a claim no test held until this one.
//
// `ee/keyservice/terraform/infra_test.go` is a `//go:build terraform` file, so
// `go test ./...` compiles it to no test at all and only the job that hands the
// go tool the tag runs it. docs/pending-workflows/README.md's `## ci.yml` section
// says in as many words that no job runs it today and writes out the exact change
// that would — a format step, a second root loop, and one more package on the last
// step's command — and docs/design/key-signing-service.md's status line states the
// same fact about the suite. Both are claims about `.github/workflows/ci.yml`, and
// the section is what a reviewer reads before deciding whether the suite is
// reached, so each is held to the workflow in both directions: a document still
// calling the change outstanding once CI carries it fails, and so does one that has
// moved to the applied form while the workflow does not run the suite.
//
// The sibling section above the README's — the `internal/runner` test bound — is
// held the same way by docs_runner_timeout_test.go, over the three documents that
// state it, and that test is why this one exists: the first change went in without
// one and the claim it rests on could rot with every check green. The review of the
// second found the same gap one document further out, in the design doc.
//
// What this cannot do is run the job. Only the owner may write under
// `.github/workflows/`, so the change is staged in the README rather than made
// here, and the sentences below are the ones to write when it is applied.

const (
	// keyserviceTerraformPackage is what the change adds to the terraform job's
	// last step. It is the whole of the discriminator: `go vet -tags terraform
	// ./...` in the `test` job type-checks the package, and nothing else in the
	// workflow names it, so this string appearing at all is a step that would run
	// the suite.
	keyserviceTerraformPackage = "./ee/keyservice/..."

	// keyserviceCIPendingMarker opens the README's section while the change is
	// outstanding. It is the README's own sentence, quoted so that a rewrite of
	// it fails here rather than passing quietly.
	keyserviceCIPendingMarker = "**The change outstanding now, a third one: the key service's Terraform never runs in CI"

	// keyserviceCIAppliedMarker is what replaces it once the owner has applied
	// the change: the README's own `**Applied: ...**` form, which the sibling
	// section uses for the same move.
	keyserviceCIAppliedMarker = "**Applied: the key service's Terraform suite runs in CI"

	// keyserviceCILastStep is the line the README's section quotes as the last
	// step's command, which is what has to gain the package above.
	keyserviceCILastStep = "go test -tags terraform ./internal/infra/... ./deploy/terraform/..."
)

// keyserviceCIClaims are the documents that say whether CI runs the key service's
// Terraform suite, each with the two forms of its claim: the sentence it carries
// while the change is outstanding, and the one that replaces it once the owner has
// applied it. The README's section IS the change; the design doc's status line
// states the same fact about the service's own suite, and a reader of the design
// learns from it whether the suite that refuses a widened signer role is ever run,
// so it is held here rather than left as prose a diff can falsify.
var keyserviceCIClaims = []struct {
	path             string
	pending, applied string
}{
	{
		path:    "docs/pending-workflows/README.md",
		pending: keyserviceCIPendingMarker,
		applied: keyserviceCIAppliedMarker,
	},
	{
		path:    "docs/design/key-signing-service.md",
		pending: "No job runs that suite in CI",
		applied: "The suite runs in CI on every push to `main`",
	},
}

// keyserviceCIStateProblems answers what disagrees between
// `.github/workflows/ci.yml` and each of the documents above about the key
// service's Terraform suite, one problem per disagreement. It is a function of the
// texts rather than of the files — the workflow, and a reader for a document's path
// — so that the rules can be pinned against the states this repository cannot be in
// (TestKeyserviceCIStateRulesSeeEveryState).
func keyserviceCIStateProblems(ci string, doc func(path string) string) []string {
	var problems []string
	runs := strings.Contains(ci, keyserviceTerraformPackage)
	for _, c := range keyserviceCIClaims {
		text := doc(c.path)
		pending := strings.Contains(text, c.pending)
		applied := strings.Contains(text, c.applied)
		switch {
		case pending && applied:
			problems = append(problems, fmt.Sprintf("%s carries both %q and %q: they are the same claim in two directions, so a reader is told no job runs the key service's Terraform suite and that one does", c.path, c.pending, c.applied))
		case !pending && !applied:
			problems = append(problems, fmt.Sprintf("%s carries neither form of the key service's Terraform claim: the sentence it carries while the change is outstanding is %q, and the one that replaces it once the owner applies the change is %q", c.path, c.pending, c.applied))
		case runs && pending:
			problems = append(problems, fmt.Sprintf(".github/workflows/ci.yml now runs the key service's Terraform suite (a step names %s) while %s still says %q: the change has been applied without this document, which is what this fails on — write %q in the same commit", keyserviceTerraformPackage, c.path, c.pending, c.applied))
		case !runs && applied:
			problems = append(problems, fmt.Sprintf("%s says the key service's Terraform suite runs in CI (%q) while no job in .github/workflows/ci.yml names %s: the document has moved ahead of the workflow, so a reader is told the suite runs on every push and it does not", c.path, c.applied, keyserviceTerraformPackage))
		}
	}
	// The README section's own reasoning, held while it is outstanding: the `test`
	// job type-checks the tagged tests, and the terraform job's last step is the
	// line the change names. Either moving leaves the section arguing about a
	// workflow that is no longer there.
	if !strings.Contains(ci, "go vet -tags terraform ./...") {
		problems = append(problems, ".github/workflows/ci.yml no longer type-checks the terraform-tagged tests with `go vet -tags terraform ./...`: docs/pending-workflows/README.md says that step is what keeps the key service's suite from rotting into not compiling, and without it the two facts the section rests on are one")
	}
	if !runs && !strings.Contains(ci, keyserviceCILastStep) {
		problems = append(problems, ".github/workflows/ci.yml's terraform job no longer ends in `"+keyserviceCILastStep+"`: docs/pending-workflows/README.md names that line as the one the key service's package is added to, so either the section or the workflow has to move")
	}
	return problems
}

// TestKeyserviceCIDocsMatchTheWorkflow holds every document that says whether CI
// runs the key service's Terraform suite to the workflow it describes.
func TestKeyserviceCIDocsMatchTheWorkflow(t *testing.T) {
	ci := readDoc(t, ".github/workflows/ci.yml")
	for _, p := range keyserviceCIStateProblems(ci, func(path string) string { return readDoc(t, path) }) {
		t.Error(p)
	}
	// The other half of the claim is the suite's own build tag — an untagged file
	// would be run by the `test` job's `go test ./...` — and it is not asserted
	// here on purpose: a core test reading the real ee/ has to go through
	// readRealEE and be named in eeReadingTests, which the core-without-ee
	// workflow skips by a list this run may not edit. That half also fails loudly
	// rather than rotting: the tag is what keeps the suite out of the test job,
	// and that job has no terraform binary for it to run with.
	//
	// The census row this pin moved is held the same way as its siblings' are: the
	// amendment naming it has to be in the ledger, because a row that does not
	// record the pin that moved it is a merge that did not.
	ledger := readDoc(t, "docs/rigor-modes/reconciliation.md")
	if !strings.Contains(flattenDoc(ledger), "docs_keyservice_terraform_ci_test.go`, the key service's pin") {
		t.Errorf("docs/rigor-modes/reconciliation.md carries no amendment naming internal/cli/docs_keyservice_terraform_ci_test.go: the census row's own text says a folder that writes a pin moves the row in its own commit and records it there")
	}
}

// TestKeyserviceCIStateRulesSeeEveryState pins the rules above against the
// combinations that matter, including the ones this repository cannot be in:
// applied in the workflow with the documents updated, and applied with one or both
// documents left behind. Each case asserts the problems *and*, for the cases about
// one document, that the other one produced none — a rule that failed everything
// would fail here rather than look strict.
func TestKeyserviceCIStateRulesSeeEveryState(t *testing.T) {
	const (
		// The workflow as it is today.
		ciPending = "  terraform:\n    steps:\n" +
			"      - run: terraform fmt -check -recursive deploy/terraform\n" +
			"      - run: " + keyserviceCILastStep + "\n" +
			"  test:\n    steps:\n      - run: go vet -tags terraform ./...\n"
		// ...and with the README's change applied.
		ciApplied = "  terraform:\n    steps:\n" +
			"      - run: terraform fmt -check -recursive deploy/terraform\n" +
			"      - run: terraform fmt -check -recursive ee/keyservice/terraform\n" +
			"      - run: " + keyserviceCILastStep + " " + keyserviceTerraformPackage + "\n" +
			"  test:\n    steps:\n      - run: go vet -tags terraform ./...\n"
		// ...and with the step written but the package never added, which is
		// the way the section goes false without the workflow running anything.
		ciStepOnly = "  terraform:\n    steps:\n" +
			"      - run: terraform fmt -check -recursive ee/keyservice/terraform\n" +
			"      - run: " + keyserviceCILastStep + "\n" +
			"  test:\n    steps:\n      - run: go vet -tags terraform ./...\n"
	)
	// claim is the text of the claim at index i in the given form, with the
	// README's own section prose around it so that the markers are read where the
	// section carries them.
	claim := func(i int, form string) string {
		c := keyserviceCIClaims[i]
		switch form {
		case "pending":
			return "prose. " + c.pending + " (2026-11-09, S3.5).** prose " + keyserviceCILastStep + "\n"
		case "applied":
			return "prose. " + c.applied + " (2026-11-09, S3.5).** prose " + keyserviceCILastStep + "\n"
		}
		return "prose about something else\n"
	}
	// docs answers each document's text from the form its entry holds: pending,
	// applied, "both", or "" for a document that says neither.
	docs := func(forms ...string) func(string) string {
		return func(path string) string {
			for i, c := range keyserviceCIClaims {
				if c.path != path {
					continue
				}
				if forms[i] == "both" {
					return claim(i, "pending") + claim(i, "applied")
				}
				return claim(i, forms[i])
			}
			return ""
		}
	}
	readme, design := keyserviceCIClaims[0].path, keyserviceCIClaims[1].path
	for _, tc := range []struct {
		name  string
		ci    string
		forms []string
		want  []string // one substring per expected problem
		not   []string // documents a problem must not name
	}{
		{name: "the state this branch is in: the change is outstanding in both documents and in the workflow", ci: ciPending, forms: []string{"pending", "pending"}},
		{name: "applied, with both documents rewritten", ci: ciApplied, forms: []string{"applied", "applied"}},
		{name: "applied, both documents left behind", ci: ciApplied, forms: []string{"pending", "pending"}, want: []string{readme + " still says", design + " still says"}},
		{name: "both documents rewritten, the workflow left behind", ci: ciPending, forms: []string{"applied", "applied"}, want: []string{readme + " says", design + " says"}},
		{name: "the README moved with the workflow and the design doc left behind", ci: ciApplied, forms: []string{"applied", "pending"}, want: []string{design + " still says"}, not: []string{readme + " still says"}},
		{name: "the design doc moved and the README left behind", ci: ciPending, forms: []string{"pending", "applied"}, want: []string{design + " says"}, not: []string{readme + " says"}},
		{name: "the format step written, the package never added", ci: ciStepOnly, forms: []string{"pending", "pending"}},
		{name: "the format step written while both documents claim a job runs the suite", ci: ciStepOnly, forms: []string{"applied", "applied"}, want: []string{readme + " says", design + " says"}},
		{name: "a document that carries neither form", ci: ciPending, forms: []string{"", "pending"}, want: []string{readme + " carries neither form"}, not: []string{design + " carries neither form"}},
		{name: "a document that carries both forms", ci: ciPending, forms: []string{"pending", "both"}, want: []string{design + " carries both"}, not: []string{readme + " carries both"}},
		{name: "the terraform-tagged tests no longer type-checked", ci: "  test:\n    steps:\n      - run: go vet ./...\n", forms: []string{"pending", "pending"}, want: []string{"no longer type-checks the terraform-tagged tests"}},
		{name: "the last step renamed on the workflow's side", ci: "  test:\n    steps:\n      - run: go vet -tags terraform ./...\n", forms: []string{"pending", "pending"}, want: []string{"no longer ends in"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := keyserviceCIStateProblems(tc.ci, docs(tc.forms...))
			if len(tc.want) == 0 {
				if len(problems) != 0 {
					t.Fatalf("problems = %q, want none", problems)
				}
				return
			}
			if len(problems) == 0 {
				t.Fatalf("no problems at all, want %d (%q)", len(tc.want), tc.want)
			}
			for _, want := range tc.want {
				if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
					t.Errorf("no problem names %q; got %q", want, problems)
				}
			}
			for _, not := range tc.not {
				if slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, not) }) {
					t.Errorf("a problem names %q, which this case leaves correct; got %q", not, problems)
				}
			}
		})
	}
}
