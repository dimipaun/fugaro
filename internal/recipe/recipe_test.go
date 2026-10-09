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
		{"version not an integer", "version: one\nname: a\nsteps:\n  - review: {}\n", "version", "whole number"},
		{"version a float", "version: 1.5\nname: a\nsteps:\n  - review: {}\n", "version", "whole number"},
		{"roles not a mapping", "version: 1\nname: a\nroles: coder\nsteps:\n  - review: {}\n", "roles", "must be a mapping"},
		{"steps not a list", "version: 1\nname: a\nsteps:\n  review: {}\n", "steps", "must be a list"},
		{"name 41 chars", "version: 1\nname: " + strings.Repeat("a", 41) + "\nsteps:\n  - review: {}\n", "name", "must be a recipe name"},
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
	if !strings.Contains(ProblemsText(ps), "the step type is check, not checks") {
		t.Errorf("a checks step: %v", ps)
	}
	for _, tc := range []struct{ roles, want string }{
		{"{ background: coder }", "only the reviewer and coder roles can be mapped"},
		{"{ reviewer: background }", "must be coder"},
		{"{ reviewer: claude-opus-5 }", modelMsg},
		{"{ reviewer: deepseek/deepseek-v4 }", modelMsg},
		{"{ reviewer: [coder] }", "must be coder"},
		{"{ reviewer: }", "must be coder"},
		{"{ reviewer: 5 }", "must be coder"},
	} {
		_, ps := Parse([]byte("version: 1\nname: a\nroles: " + tc.roles + "\nsteps:\n  - review: {}\n"))
		if !strings.Contains(ProblemsText(ps), tc.want) {
			t.Errorf("roles %s: problems %v lack %q", tc.roles, ps, tc.want)
		}
		if tc.want != modelMsg && strings.Contains(ProblemsText(ps), "never names a model") {
			t.Errorf("roles %s: a non-model value gets the model message: %v", tc.roles, ps)
		}
	}
}

// TestProblemPathEscapesUntrustedKeys: a recipe is untrusted input (it comes
// from the runs bucket), and Problem.Path echoes the offending key back so
// the author can find it. An ESC or newline in that key must never reach a
// terminal unescaped: a hostile key could otherwise plant ANSI sequences or
// forge extra output lines. This covers the four places recipe.go builds a
// Path from a raw yaml.Node key: a bad top-level key, a bad key under roles,
// a bad step-kind key (the "value" of the steps list) and a bad option key
// inside a step.
func TestProblemPathEscapesUntrustedKeys(t *testing.T) {
	const badKey = "bad\x1bkey\nend"
	escaped := func(path string) bool {
		return !strings.ContainsRune(path, 0x1b) && !strings.Contains(path, "\n") &&
			strings.Contains(path, `\u001b`) && strings.Contains(path, `\u000a`)
	}
	for _, tc := range []struct {
		name, text, wantPrefix string
	}{
		{"top-level key", "version: 1\nname: a\n\"bad\\x1bkey\\nend\": 1\nsteps:\n  - review: {}\n", ""},
		{"roles key", "version: 1\nname: a\nroles: { \"bad\\x1bkey\\nend\": coder }\nsteps:\n  - review: {}\n", "roles."},
		{"step-kind key", "version: 1\nname: a\nsteps:\n  - \"bad\\x1bkey\\nend\": {}\n  - review: {}\n", "steps[0]."},
		{"step-option key", "version: 1\nname: a\nsteps:\n  - review: {\"bad\\x1bkey\\nend\": 1}\n", "steps[0].review."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte(tc.text))
			for _, p := range ps {
				if strings.HasPrefix(p.Path, tc.wantPrefix) && strings.Contains(p.Path, "bad") {
					if !escaped(p.Path) {
						t.Fatalf("Path %q leaks the raw ESC/newline from %q", p.Path, badKey)
					}
					return
				}
			}
			t.Fatalf("no problem path echoing the bad key: %v", ps)
		})
	}
}

func TestExtendedFormatParses(t *testing.T) {
	for name, src := range map[string]string{
		"standard": "version: 1\nname: standard\nuse_when: typical work\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n",
		"premium":  "version: 1\nname: premium\nroles: { coder: reviewer }\nsteps:\n  - review: {}\n",
		"review":   "version: 1\nname: review-only\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n",
		"testfix":  "version: 1\nname: test-and-fix\nsteps:\n  - check: { command: test }\n  - first_line: {}\n  - review: { bounce: first_line }\n",
		"lintfix":  "version: 1\nname: lint-fix\nroles: { reviewer: coder }\nsteps:\n  - check: { command: lint, autofix: true }\n  - review: {}\n",
	} {
		r, ps := Parse([]byte(src))
		if len(ps) != 0 {
			t.Errorf("%s: %s", name, ProblemsText(ps))
			continue
		}
		if !UsesV07(r) {
			t.Errorf("%s: UsesV07 false", name)
		}
	}
}

func TestCheckCommandIsAnEnum(t *testing.T) {
	for _, bad := range []string{"make test", "rm -rf /", "fix", "Test", ""} {
		src := "version: 1\nname: x\nsteps:\n  - check: { command: \"" + bad + "\" }\n  - review: {}\n"
		_, ps := Parse([]byte(src))
		if !strings.Contains(ProblemsText(ps), "steps[0].check.command: must be build, test or lint (it names a commands.* key of fugaro.yaml; a recipe never holds a command)") {
			t.Errorf("command %q: %s", bad, ProblemsText(ps))
		}
	}
	_, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - check: { command: test, autofix: true }\n  - review: {}\n"))
	if !strings.Contains(ProblemsText(ps), "autofix is only for command: lint") {
		t.Errorf("autofix on test: %s", ProblemsText(ps))
	}
}

func TestRecipeNamesNoModelEvenAsRole(t *testing.T) {
	for src, want := range map[string]string{
		"roles: { coder: claude-opus-4-1 }":           "a recipe never names a model",
		"roles: { coder: reviewer, reviewer: coder }": "roles: coder: reviewer and reviewer: coder exclude each other",
		"roles: { background: coder }":                "only the reviewer and coder roles can be mapped",
	} {
		_, ps := Parse([]byte("version: 1\nname: x\n" + src + "\nsteps:\n  - review: {}\n"))
		if !strings.Contains(ProblemsText(ps), want) {
			t.Errorf("%s: %s", src, ProblemsText(ps))
		}
	}
}

func TestBounceNeedsFirstLine(t *testing.T) {
	_, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - review: { bounce: first_line }\n"))
	if !strings.Contains(ProblemsText(ps), "bounce: first_line needs a first_line step before the review") {
		t.Fatal(ProblemsText(ps))
	}
	_, ps = Parse([]byte("version: 1\nname: x\nsteps:\n  - first_line: {}\n  - review: { bounce: review }\n"))
	if !strings.Contains(ProblemsText(ps), "bounce can only be first_line") {
		t.Fatal(ProblemsText(ps))
	}
}

func TestOrderWithChecksAndModes(t *testing.T) {
	for src, want := range map[string]string{
		"steps:\n  - first_line: {}\n  - check: { command: test }\n  - review: {}\n": "check steps must come first",
		"mode: review\nsteps:\n  - first_line: {}\n  - review: {}\n":                 "mode: review allows exactly one review step and nothing else",
		"mode: review\nsteps:\n  - review: { max_rounds: 2 }\n":                      "mode: review reviews once: max_rounds must be 1",
		"mode: plan\nsteps:\n  - review: {}\n":                                       "mode must be implement or review",
		"use_when: " + strings.Repeat("x", 301) + "\nsteps:\n  - review: {}\n":       "use_when: must be at most 300 bytes",
	} {
		_, ps := Parse([]byte("version: 1\nname: x\n" + src))
		if !strings.Contains(ProblemsText(ps), want) {
			t.Errorf("%q: got %s", src, ProblemsText(ps))
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

// TestParseBoundaries: the limits are inclusive at the documented value.
func TestParseBoundaries(t *testing.T) {
	const tail = "\nsteps:\n  - review: {}\n"
	for _, tc := range []struct {
		name, text string
		ok         bool
	}{
		{"name 40", "version: 1\nname: " + strings.Repeat("a", 40) + tail, true},
		{"name 41", "version: 1\nname: " + strings.Repeat("a", 41) + tail, false},
		{"description 200 bytes", "version: 1\nname: a\ndescription: " + strings.Repeat("x", 200) + tail, true},
		{"description 201 bytes", "version: 1\nname: a\ndescription: " + strings.Repeat("x", 201) + tail, false},
		// 100 two-byte runes are 100 characters but 200 bytes; 101 are 202.
		{"description 100 two-byte runes", "version: 1\nname: a\ndescription: " + strings.Repeat("é", 100) + tail, true},
		{"description 101 two-byte runes", "version: 1\nname: a\ndescription: " + strings.Repeat("é", 101) + tail, false},
		{"BOM", "\ufeffversion: 1\nname: a" + tail, true},
		{"CRLF", "version: 1\r\nname: a\r\nsteps:\r\n  - review: {}\r\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, ps := Parse([]byte(tc.text))
			if (r != nil) != tc.ok {
				t.Fatalf("recipe %+v, problems %v", r, ps)
			}
		})
	}
	base := "version: 1\nname: a\nsteps:\n  - review: {}\n#"
	for n, ok := range map[int]bool{MaxBytes: true, MaxBytes + 1: false} {
		text := base + strings.Repeat("x", n-len(base))
		if len(text) != n {
			t.Fatalf("built %d bytes, want %d", len(text), n)
		}
		r, ps := Parse([]byte(text))
		if (r != nil) != ok {
			t.Errorf("%d bytes: recipe %+v, problems %v", n, r, ps)
		}
	}
}

// TestOrderUsesSourcePositions: a dropped invalid step keeps its place in the
// numbering, and says nothing more than its own problem.
func TestOrderUsesSourcePositions(t *testing.T) {
	_, ps := Parse([]byte("version: 1\nname: a\nsteps:\n  - lint: {}\n  - review: {}\n  - first_line: {}\n"))
	got := map[string][]string{}
	for _, p := range ps {
		got[p.Path] = append(got[p.Path], p.Message)
	}
	if len(got["steps[0].lint"]) != 1 || !strings.Contains(got["steps[0].lint"][0], "is not a step type") {
		t.Errorf("lint: %v", ps)
	}
	if len(got["steps[2]"]) != 1 || !strings.Contains(got["steps[2]"][0], "first_line must come before review") {
		t.Errorf("first_line: %v", ps)
	}
	if len(got["steps"]) != 1 || !strings.Contains(got["steps"][0], "review must be the last step") || len(ps) != 3 {
		t.Errorf("problems = %v", ps)
	}
	// An invalid last step is not a review that is out of place.
	_, ps = Parse([]byte("version: 1\nname: a\nsteps:\n  - review: {}\n  - lint: {}\n"))
	if len(ps) != 1 || ps[0].Path != "steps[1].lint" {
		t.Errorf("lint after review: %v", ps)
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
