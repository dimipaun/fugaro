package cli

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/ciselect"
)

// TestCISelectorLegKeysAreTheMatrixKeys holds the keys `ci.yml`'s shard job
// reads off its matrix to the field names of ciselect.Leg. The selector's
// output is the matrix, so a renamed field that the workflow still reads under
// the old name hands every runner an empty value, and the run fails or, worse,
// runs the wrong tests; the packer's own tests cannot see it because the text
// that reads the keys is a workflow file.
func TestCISelectorLegKeysAreTheMatrixKeys(t *testing.T) {
	var legKeys []string
	typ := reflect.TypeOf(ciselect.Leg{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		legKeys = append(legKeys, name)
	}
	slices.Sort(legKeys)

	ci := readDoc(t, ".github/workflows/ci.yml")
	var used []string
	for _, m := range regexp.MustCompile(`\bmatrix\.(\w+)`).FindAllStringSubmatch(ci, -1) {
		if !slices.Contains(used, m[1]) {
			used = append(used, m[1])
		}
	}
	slices.Sort(used)

	if !slices.Equal(legKeys, used) {
		t.Errorf(".github/workflows/ci.yml reads the matrix keys %v and ciselect.Leg's JSON keys are %v: the selector's output is the matrix, so the two lists are one list", used, legKeys)
	}
}

// TestCISelectorOutputKeysAreTheSelectJobKeys holds the other half of that
// contract, the keys the `select` step reads out of the selector's JSON, to
// `ciselect.Output`'s own field names. A rename here is worse than a leg's:
// `jq` prints `null` for a key that is not there rather than failing, so
// `.legs` hands the shard job an empty matrix — which is skipped, and a skipped
// shard counts as success — while `.terraform` and `.rules` drop the two jobs
// the release gate requires without a word.
func TestCISelectorOutputKeysAreTheSelectJobKeys(t *testing.T) {
	var outputKeys []string
	typ := reflect.TypeOf(ciselect.Output{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		outputKeys = append(outputKeys, name)
	}

	// `select.json` is written by the `select` step and named nowhere else, so
	// the lines that name it are the reads of the selector's output.
	ci := readDoc(t, ".github/workflows/ci.yml")
	var read []string
	for _, line := range strings.Split(ci, "\n") {
		if !strings.Contains(line, "select.json") {
			continue
		}
		// The filename's own dot is not a key read.
		line = strings.ReplaceAll(line, "select.json", "")
		for _, m := range regexp.MustCompile(`\.([A-Za-z_]\w*)`).FindAllStringSubmatch(line, -1) {
			if !slices.Contains(read, m[1]) {
				read = append(read, m[1])
			}
		}
	}
	for _, key := range []string{"legs", "terraform", "rules"} {
		if !slices.Contains(read, key) {
			t.Errorf(".github/workflows/ci.yml no longer reads `.%s` out of select.json: the shard matrix and the two job switches come from the selector, and the jobs they decide run only where the workflow asks", key)
		}
	}
	for _, key := range read {
		if !slices.Contains(outputKeys, key) {
			t.Errorf(".github/workflows/ci.yml reads `.%s` out of select.json and ciselect.Output's JSON keys are %v: a renamed field reads as null, so the job it decides is skipped with nothing failing", key, outputKeys)
		}
	}
}

// TestCIDocsSayASelectedRunIsTheDefaultAndAFullRunPrecedesATag (CS8): the two
// documents a change is sized from and a release is cut from, plus the plan
// folder's own record. A selected run is the default and is not a gate, so a CI
// section that still promises a full run on every dispatch sends a release to
// the gate's refusal, and a release checklist that never dispatches one leaves
// the tag unvouched-for. The read-map has one door and it is not CI's, so the
// refresh command is pinned in both documents that give it.
func TestCIDocsSayASelectedRunIsTheDefaultAndAFullRunPrecedesATag(t *testing.T) {
	const dispatch = "gh workflow run ci --ref main -f full=true"

	contributing := flattenDoc(docSection(t, "CONTRIBUTING.md", readDoc(t, "CONTRIBUTING.md"), "## What CI runs"))
	for _, want := range []string{
		"A dispatch is selected by default",
		dispatch,
		"nightly full run",
		"scripts/ci-readmap --from-artifact",
	} {
		if !strings.Contains(contributing, want) {
			t.Errorf("CONTRIBUTING.md's `## What CI runs` no longer says %q: a contributor reads this section to know what a dispatch runs, what a tag requires and how the read-map is refreshed", want)
		}
	}

	// The dispatch is a pre-tag step, so it is pinned inside the checklist the
	// release reads in order rather than anywhere in the section it sits in.
	release := readDoc(t, "docs/release.md")
	start := strings.Index(release, "**Before you tag")
	const checklistEnd = "\n`scripts/release.sh X.Y.Z` does the whole thing"
	if start < 0 {
		t.Fatalf("docs/release.md has no %q checklist: it is where a release reads the CI a tag needs", "**Before you tag")
	}
	checklist := release[start:]
	if end := strings.Index(checklist, checklistEnd); end >= 0 {
		checklist = checklist[:end]
	} else {
		t.Fatalf("docs/release.md's \"Before you tag\" checklist no longer ends at %q, so this test is reading past it", checklistEnd)
	}
	checklist = flattenDoc(checklist)
	for _, want := range []string{dispatch, "selected", "full run", "nightly", "scripts/ci-readmap --from-artifact"} {
		if !strings.Contains(checklist, want) {
			t.Errorf("docs/release.md's \"Before you tag\" checklist no longer says %q: a tag is cut only after a full CI run on its commit, a selected run must not be mistaken for one, and the map's refresh has one door", want)
		}
	}

	readme := flattenDoc(readDoc(t, "docs/ci-affected-tests/README.md"))
	for _, want := range []string{
		"one exception to the 2026-11-07 ruling",
		"github.event_name == 'schedule'",
		"scripts/ci-readmap --from-artifact",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("docs/ci-affected-tests/README.md no longer says %q: the nightly is the plan's one exception to the dispatch-only ruling, and the README is where that exception is recorded", want)
		}
	}
}
