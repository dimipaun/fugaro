package cli

import (
	"slices"
	"strings"
	"testing"
)

// The key service's Terraform suite, and the one claim
// docs/pending-workflows/README.md makes about it that no test held.
//
// `ee/keyservice/terraform/infra_test.go` is a `//go:build terraform` file, so
// `go test ./...` compiles it to no test at all and only the job that hands the
// go tool the tag runs it. The README's `## ci.yml` section says in as many
// words that no job runs it today, and writes out the exact change that would —
// a format step, a second root loop, and one more package on the last step's
// command. That is a claim about `.github/workflows/ci.yml`, and the section is
// what a reviewer reads before deciding whether the suite is reached, so the two
// are held to each other here in both directions: the section still calling the
// change outstanding once CI carries it fails, and so does a section that has
// moved to the applied form while the workflow does not run the suite.
//
// The sibling section above it — the `internal/runner` test bound — is held the
// same way by docs_runner_timeout_test.go, and that test is why this one exists:
// the second change went in without one and the claim it rests on could rot with
// every check green.
//
// What this cannot do is run the job. Only the owner may write under
// `.github/workflows/`, so the change is staged in the README rather than made
// here, and the sentence below is the one to write when it is applied.

const (
	// keyserviceTerraformPackage is what the change adds to the terraform job's
	// last step. It is the whole of the discriminator: `go vet -tags terraform
	// ./...` in the `test` job type-checks the package, and nothing else in the
	// workflow names it, so this string appearing at all is a step that would run
	// the suite.
	keyserviceTerraformPackage = "./ee/keyservice/..."

	// keyserviceCIPendingMarker opens the section while the change is
	// outstanding. It is the README's own sentence, quoted so that a rewrite of
	// it fails here rather than passing quietly.
	keyserviceCIPendingMarker = "**The change outstanding now, a third one: the key service's Terraform never runs in CI"

	// keyserviceCIAppliedMarker is what replaces it once the owner has applied
	// the change: the README's own `**Applied: ...**` form, which the sibling
	// section uses for the same move.
	keyserviceCIAppliedMarker = "**Applied: the key service's Terraform suite runs in CI"

	// keyserviceCILastStep is the line the section quotes as the last step's
	// command, which is what has to gain the package above.
	keyserviceCILastStep = "go test -tags terraform ./internal/infra/... ./deploy/terraform/..."
)

// keyserviceCIStateProblems answers what disagrees between
// `.github/workflows/ci.yml` and docs/pending-workflows/README.md's `## ci.yml`
// section about the key service's Terraform suite. It is a function of the two
// texts rather than of the files so that the rules can be pinned against the
// states this repository cannot be in (TestKeyserviceCIStateRulesSeeEveryState).
func keyserviceCIStateProblems(ci, readme string) []string {
	var problems []string
	outstanding := strings.Contains(readme, keyserviceCIPendingMarker)
	applied := strings.Contains(readme, keyserviceCIAppliedMarker)
	runs := strings.Contains(ci, keyserviceTerraformPackage)

	switch {
	case outstanding && applied:
		problems = append(problems, "docs/pending-workflows/README.md carries both forms of the key service's Terraform change ("+keyserviceCIPendingMarker+"... and "+keyserviceCIAppliedMarker+"...): the section says in one place that no job runs the suite and in another that one does")
	case !outstanding && !applied:
		problems = append(problems, "docs/pending-workflows/README.md carries neither form of the key service's Terraform change: the sentence opening it while the change is outstanding is "+keyserviceCIPendingMarker+"..., and the one that replaces it once the owner applies the change is "+keyserviceCIAppliedMarker+"...")
	}
	if runs && outstanding {
		problems = append(problems, ".github/workflows/ci.yml now runs the key service's Terraform suite (a step names "+keyserviceTerraformPackage+") while docs/pending-workflows/README.md still says "+keyserviceCIPendingMarker+"...: the change has been applied without the section, which is what this fails on — rewrite it as "+keyserviceCIAppliedMarker+"... in the same commit")
	}
	if !runs && applied {
		problems = append(problems, "docs/pending-workflows/README.md says the key service's Terraform change is applied while no job in .github/workflows/ci.yml names "+keyserviceTerraformPackage+": the section has moved ahead of the workflow, so a reader is told the suite runs on every push and it does not")
	}
	// The section's own reasoning, held while it is outstanding: the `test` job
	// type-checks the tagged tests, and the terraform job's last step is the line
	// the change names. Either moving leaves the section arguing about a workflow
	// that is no longer there.
	if !strings.Contains(ci, "go vet -tags terraform ./...") {
		problems = append(problems, ".github/workflows/ci.yml no longer type-checks the terraform-tagged tests with `go vet -tags terraform ./...`: docs/pending-workflows/README.md says that step is what keeps the key service's suite from rotting into not compiling, and without it the two facts the section rests on are one")
	}
	if !runs && !strings.Contains(ci, keyserviceCILastStep) {
		problems = append(problems, ".github/workflows/ci.yml's terraform job no longer ends in `"+keyserviceCILastStep+"`: docs/pending-workflows/README.md names that line as the one the key service's package is added to, so either the section or the workflow has to move")
	}
	return problems
}

// TestKeyserviceCIDocsMatchTheWorkflow holds the README's `## ci.yml` section
// and the workflow it describes to each other.
func TestKeyserviceCIDocsMatchTheWorkflow(t *testing.T) {
	ci := readDoc(t, ".github/workflows/ci.yml")
	readme := readDoc(t, "docs/pending-workflows/README.md")
	for _, p := range keyserviceCIStateProblems(ci, readme) {
		t.Error(p)
	}
	// The suite the section is about has to be the tagged file it says it is, or
	// the claim is about nothing: an untagged file would be compiled by the
	// `test` job's `go test ./...` and run there, whichever job runs what.
	suite := readDoc(t, "ee/keyservice/terraform/infra_test.go")
	if !strings.Contains(suite, "//go:build terraform") {
		t.Errorf("ee/keyservice/terraform/infra_test.go no longer carries `//go:build terraform`: docs/pending-workflows/README.md's `## ci.yml` section rests on its being a tagged file that untagged `go test ./...` compiles to no test at all")
	}
}

// TestKeyserviceCIStateRulesSeeEveryState pins the rules above against the
// combinations that matter, including the ones this repository cannot be in:
// applied in the workflow with the section updated, and applied with the section
// left behind. Each case asserts the problems *and* that a correct pair produces
// none, so a rule that failed everything would fail here rather than look
// strict.
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
	section := func(form string) string {
		switch form {
		case "pending":
			return "prose. " + keyserviceCIPendingMarker + " (2026-11-09, S3.5).** prose " + keyserviceCILastStep + "\n"
		case "applied":
			return "prose. " + keyserviceCIAppliedMarker + " (2026-11-09, S3.5).** prose " + keyserviceCILastStep + "\n"
		}
		return "prose about something else\n"
	}
	for _, tc := range []struct {
		name  string
		ci    string
		form  string
		want  []string // one substring per expected problem
		green bool
	}{
		{"the state this branch is in: the change is outstanding in the section and in the workflow", ciPending, "pending", nil, true},
		{"applied, with the section rewritten", ciApplied, "applied", nil, true},
		{"applied, the section left behind", ciApplied, "pending", []string{"has been applied without the section"}, false},
		{"the section rewritten, the workflow left behind", ciPending, "applied", []string{"moved ahead of the workflow"}, false},
		{"the format step written, the package never added", ciStepOnly, "pending", nil, true},
		{"the format step written while the section claims a job runs the suite", ciStepOnly, "applied", []string{"moved ahead of the workflow"}, false},
		{"neither form of the sentence", ciPending, "", []string{"carries neither form"}, false},
		{"both forms of the sentence", ciPending, "both", []string{"carries both forms"}, false},
		{"the terraform-tagged tests no longer type-checked", "  test:\n    steps:\n      - run: go vet ./...\n", "pending", []string{"no longer type-checks the terraform-tagged tests"}, false},
		{"the last step renamed on the workflow's side", "  test:\n    steps:\n      - run: go vet -tags terraform ./...\n", "pending", []string{"no longer ends in"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readme := section(tc.form)
			if tc.form == "both" {
				readme = section("pending") + section("applied")
			}
			problems := keyserviceCIStateProblems(tc.ci, readme)
			if tc.green {
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
		})
	}
}
