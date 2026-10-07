package recipe

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, text string) *Recipe {
	t.Helper()
	r, ps := Parse([]byte(text))
	if len(ps) > 0 {
		t.Fatalf("problems: %v\n%s", ps, text)
	}
	return r
}

func TestParseValid(t *testing.T) {
	r := mustParse(t, `version: 1
name: cheap-loop-senior
description: Cheap first-line review/fix loop, then one senior review
roles:
  reviewer: coder
steps:
  - first_line: { max_rounds: 2 }
  - review:     { max_rounds: 3 }
`)
	if r.Name != "cheap-loop-senior" || !r.ReviewerIsCoder || len(r.Steps) != 2 ||
		r.Steps[0] != (Step{Kind: StepFirstLine, MaxRounds: 2}) || r.Steps[1] != (Step{Kind: StepReview, MaxRounds: 3}) {
		t.Fatalf("recipe = %+v", r)
	}
	r = mustParse(t, "version: 1\nname: bare\nsteps:\n  - review:\n")
	if len(r.Steps) != 1 || r.Steps[0] != (Step{Kind: StepReview}) || r.ReviewerIsCoder {
		t.Fatalf("bare = %+v", r)
	}
	r = mustParse(t, "version: 1\nname: empty-body\nsteps:\n  - first_line: {}\n  - review: {}\n")
	if r.Steps[0].MaxRounds != 0 || r.Steps[1].MaxRounds != 0 {
		t.Fatalf("empty bodies = %+v", r.Steps)
	}
}

func TestParseInvalid(t *testing.T) {
	for _, tc := range []struct{ name, text, path, want string }{
		{"no version", "name: a\nsteps:\n  - review: {}\n", "version", "is required"},
		{"version 2", "version: 2\nname: a\nsteps:\n  - review: {}\n", "version", "must be 1"},
		{"bad name", "version: 1\nname: Bad_Name\nsteps:\n  - review: {}\n", "name", "must be a recipe name"},
		{"no steps", "version: 1\nname: a\n", "steps", "is required"},
		{"no review", "version: 1\nname: a\nsteps:\n  - first_line: {}\n", "steps", "must end with a review step"},
		{"review not last", "version: 1\nname: a\nsteps:\n  - review: {}\n  - first_line: {}\n", "steps[1]", "first_line must come before review"},
		{"two reviews", "version: 1\nname: a\nsteps:\n  - review: {}\n  - review: {}\n", "steps[1]", "review must appear exactly once"},
		{"two first lines", "version: 1\nname: a\nsteps:\n  - first_line: {}\n  - first_line: {}\n  - review: {}\n", "steps[1]", "at most once"},
		{"first rounds 4", "version: 1\nname: a\nsteps:\n  - first_line: { max_rounds: 4 }\n  - review: {}\n", "steps[0].first_line.max_rounds", "between 1 and 3"},
		{"review rounds 11", "version: 1\nname: a\nsteps:\n  - review: { max_rounds: 11 }\n", "steps[0].review.max_rounds", "between 1 and 10"},
		{"explicit zero", "version: 1\nname: a\nsteps:\n  - review: { max_rounds: 0 }\n", "steps[0].review.max_rounds", "between 1 and 10"},
		{"rounds string", "version: 1\nname: a\nsteps:\n  - review: { max_rounds: two }\n", "steps[0].review.max_rounds", "whole number"},
		{"unknown step", "version: 1\nname: a\nsteps:\n  - lint: {}\n  - review: {}\n", "steps[0].lint", "is not a step type"},
		{"two keys in a step", "version: 1\nname: a\nsteps:\n  - { first_line: {}, review: {} }\n", "steps[0]", "must be one step"},
		{"unknown top key", "version: 1\nname: a\ncolour: red\nsteps:\n  - review: {}\n", "colour", "is not a recipe key"},
		{"unknown step key", "version: 1\nname: a\nsteps:\n  - review: { rounds: 2 }\n", "steps[0].review.rounds", "is not a recipe key"},
		{"long description", "version: 1\nname: a\ndescription: " + strings.Repeat("x", 201) + "\nsteps:\n  - review: {}\n", "description", "at most 200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, ps := Parse([]byte(tc.text))
			if r != nil {
				t.Fatalf("parsed: %+v", r)
			}
			for _, p := range ps {
				if p.Path == tc.path && strings.Contains(p.Message, tc.want) {
					return
				}
			}
			t.Fatalf("problems %v lack %s: %q", ps, tc.path, tc.want)
		})
	}
}

// TestReservedKeysEachSaySo: every reserved key, at the top level and in a
// step, is refused with its own message, never a generic "unknown key".
func TestReservedKeysEachSaySo(t *testing.T) {
	for key, msg := range reserved {
		for _, text := range []string{
			"version: 1\nname: a\n" + key + ": x\nsteps:\n  - review: {}\n",
			"version: 1\nname: a\nsteps:\n  - review: { " + key + ": x }\n",
		} {
			_, ps := Parse([]byte(text))
			if len(ps) == 0 || !strings.Contains(ProblemsText(ps), msg) {
				t.Errorf("%s: problems %v lack %q", key, ps, msg)
			}
		}
	}
	_, ps := Parse([]byte("version: 1\nname: a\nsteps:\n  - checks: {}\n  - review: {}\n"))
	if !strings.Contains(ProblemsText(ps), reserved["checks"]) {
		t.Errorf("a checks step: %v", ps)
	}
	for _, tc := range []struct{ roles, want string }{
		{"{ coder: reviewer }", "only the reviewer role can be mapped"},
		{"{ background: coder }", "only the reviewer role can be mapped"},
		{"{ reviewer: background }", "can only be coder"},
		{"{ reviewer: claude-opus-5 }", modelMsg},
		{"{ reviewer: deepseek/deepseek-v4 }", modelMsg},
	} {
		_, ps := Parse([]byte("version: 1\nname: a\nroles: " + tc.roles + "\nsteps:\n  - review: {}\n"))
		if !strings.Contains(ProblemsText(ps), tc.want) {
			t.Errorf("roles %s: problems %v lack %q", tc.roles, ps, tc.want)
		}
	}
}

func TestParseShapeRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"anchor":    {"version: 1\nname: a\nsteps:\n  - review: &r { max_rounds: 2 }\n", "anchor or alias"},
		"alias":     {"version: 1\nname: &n a\ndescription: *n\nsteps:\n  - review: {}\n", "anchor or alias"},
		"tag":       {"version: !!int 1\nname: a\nsteps:\n  - review: {}\n", "explicit YAML tag"},
		"duplicate": {"version: 1\nname: a\nname: b\nsteps:\n  - review: {}\n", `repeats the key "name"`},
		"merge":     {"version: 1\nname: a\n<<: { description: x }\nsteps:\n  - review: {}\n", "merge key"},
		"two docs":  {"version: 1\nname: a\nsteps:\n  - review: {}\n---\nversion: 1\n", "more than one YAML document"},
		"not a map": {"- review: {}\n", "not a YAML mapping"},
		"empty":     {"", "is empty"},
	} {
		_, ps := Parse([]byte(tc.text))
		if !strings.Contains(ProblemsText(ps), tc.want) {
			t.Errorf("%s: problems %v lack %q", name, ps, tc.want)
		}
	}
}

func TestParseSizeLimit(t *testing.T) {
	text := "version: 1\nname: a\ndescription: x\nsteps:\n  - review: {}\n" + "#" + strings.Repeat("x", MaxBytes)
	_, ps := Parse([]byte(text))
	if !strings.Contains(ProblemsText(ps), "16 KiB") {
		t.Fatalf("problems = %v", ps)
	}
}

func TestSumAndPaths(t *testing.T) {
	if got := Sum([]byte("version: 1\nname: solo\nsteps:\n  - review: {}\n")); got != "8a9f3fd7432aabb347069c3108bdeddd8b834a55ebbeb3ba4af8ff8dd54754e0" {
		t.Fatalf("Sum = %s", got)
	}
	if RepoPath("x") != ".fugaro/recipes/x.yaml" || ObjectKey("x") != "fugaro/recipes/x.yaml" {
		t.Fatal(RepoPath("x"), ObjectKey("x"))
	}
}
