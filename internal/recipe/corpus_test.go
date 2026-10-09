package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invalidWhy is the problem each testdata/recipe/invalid file must produce,
// so a file that is invalid for the wrong reason fails. Every file there must
// be listed.
var invalidWhy = map[string]string{
	"autofix-not-lint.yaml":     "autofix is only for command: lint",
	"bounce-bad-value.yaml":     "bounce can only be first_line",
	"bounce-no-first-line.yaml": "bounce: first_line needs a first_line step before the review",
	"check-command-bad.yaml":    "must be build, test or lint",
	"check-not-first.yaml":      "check steps must come first",
	"check-twice.yaml":          "check may appear at most once",
	"checks-step.yaml":          "the step type is check, not checks",
	"description-201.yaml":      "at most 200 bytes",
	"extends.yaml":              "extends is reserved",
	"first-line-4.yaml":         "between 1 and 3",
	"first-line-twice.yaml":     "first_line may appear at most once",
	"mode-bad.yaml":             "mode must be implement or review",
	"mode-review-extra.yaml":    "mode: review allows exactly one review step and nothing else",
	"mode-review-rounds.yaml":   "mode: review reviews once: max_rounds must be 1",
	"model-key.yaml":            "a recipe never names a model",
	"no-review.yaml":            "must end with a review step",
	"review-first.yaml":         "first_line must come before review",
	"roles-both-mapped.yaml":    "roles: coder: reviewer and reviewer: coder exclude each other",
	"roles-unknown-role.yaml":   "only the reviewer and coder roles can be mapped",
	"rounds-0.yaml":             "between 1 and 10",
	"rounds-11.yaml":            "between 1 and 10",
	"shape-anchor.yaml":         "anchor or alias",
	"use-when-long.yaml":        "must be at most 300 bytes",
}

func TestCorpus(t *testing.T) {
	valid, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "recipe", "valid", "*.yaml"))
	if len(valid) == 0 {
		t.Fatal("no valid corpus")
	}
	for _, f := range valid {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if r, ps := Parse(data); r == nil {
			t.Errorf("%s: refused: %v", f, ps)
		}
	}
	invalid, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "recipe", "invalid", "*.yaml"))
	if len(invalid) == 0 {
		t.Fatal("no invalid corpus")
	}
	for _, f := range invalid {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		want, ok := invalidWhy[filepath.Base(f)]
		if !ok {
			t.Errorf("%s: add the expected problem to invalidWhy", f)
			continue
		}
		r, ps := Parse(data)
		if r != nil || !strings.Contains(ProblemsText(ps), want) {
			t.Errorf("%s: recipe %+v, problems %v, want %q", f, r, ps, want)
		}
	}
	if len(invalid) != len(invalidWhy) {
		t.Errorf("invalidWhy lists %d files, the corpus has %d", len(invalidWhy), len(invalid))
	}
}
