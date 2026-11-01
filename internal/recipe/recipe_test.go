package recipe

import (
	"slices"
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
	// A recipe that never writes mode defaults to ModeImplement, not "".
	r = mustParse(t, "version: 1\nname: no-mode\nsteps:\n  - review: {}\n")
	if r.Mode != ModeImplement {
		t.Fatalf("Mode = %q, want %q", r.Mode, ModeImplement)
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
		// use_when is deliberately not restricted by mode: review (unlike
		// roles): it only describes when the routing skill should pick the
		// recipe, and a review-only recipe needs that as much as any other.
		"reviewdesc": "version: 1\nname: review-desc\nmode: review\nuse_when: a second opinion on an open PR\nsteps:\n  - review: { max_rounds: 1 }\n",
		"testfix":    "version: 1\nname: test-and-fix\nsteps:\n  - check: { command: test }\n  - first_line: {}\n  - review: { bounce: first_line }\n",
		"lintfix":    "version: 1\nname: lint-fix\nroles: { reviewer: coder }\nsteps:\n  - check: { command: lint, autofix: true }\n  - review: {}\n",
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
		want := "must be build, test or lint (it names a commands.* key of fugaro.yaml; a recipe never holds a command)"
		if !strings.Contains(ProblemsText(ps), want) {
			t.Errorf("command %q: %s", bad, ProblemsText(ps))
		}
		if ok := func() bool {
			for _, p := range ps {
				if p.Path == "steps[0].check.command" && p.Line > 0 {
					return true
				}
			}
			return false
		}(); !ok {
			t.Errorf("command %q: no steps[0].check.command problem with a line number: %v", bad, ps)
		}
	}
	_, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - check: { command: test, autofix: true }\n  - review: {}\n"))
	if !strings.Contains(ProblemsText(ps), "autofix is only for command: lint") {
		t.Errorf("autofix on test: %s", ProblemsText(ps))
	}
	if n := strings.Count(ProblemsText(ps), "autofix is only for command: lint"); n != 1 {
		t.Errorf("autofix on test reported %d times: %s", n, ProblemsText(ps))
	}
	for _, p := range ps {
		if p.Path == "steps[0].check.autofix" {
			if p.Line == 0 {
				t.Fatalf("autofix problem has no line number: %+v", p)
			}
			break
		}
	}
	// An invalid command is one problem, not two: autofix's own message adds
	// nothing once the command itself is already refused.
	_, ps = Parse([]byte("version: 1\nname: x\nsteps:\n  - check: { command: bogus, autofix: true }\n  - review: {}\n"))
	if n := len(ps); n != 1 {
		t.Errorf("bogus command with autofix: true produced %d problems, want 1: %v", n, ps)
	}
}

// TestCheckAutofixIsStrictlyBoolean: YAML 1.1 also resolves True, TRUE, False
// and FALSE to tag !!bool, but a recipe is untrusted input that Parse refuses
// rather than repairs, so only the literal true/false are accepted; anything
// else is refused, never silently reinterpreted as false.
func TestCheckAutofixIsStrictlyBoolean(t *testing.T) {
	for _, bad := range []string{"True", "TRUE", "False", "FALSE", "yes", "1"} {
		src := "version: 1\nname: x\nsteps:\n  - check: { command: lint, autofix: " + bad + " }\n  - review: {}\n"
		_, ps := Parse([]byte(src))
		if !strings.Contains(ProblemsText(ps), "must be true or false") {
			t.Errorf("autofix: %q: %s", bad, ProblemsText(ps))
		}
	}
}

// TestCheckStepsAtMostOne: the Go parser and schemas/recipe.schema.json's
// steps oneOf must agree that at most one check step is allowed.
func TestCheckStepsAtMostOne(t *testing.T) {
	_, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - check: { command: test }\n  - check: { command: lint }\n  - review: {}\n"))
	if !strings.Contains(ProblemsText(ps), "check may appear at most once") {
		t.Fatal(ProblemsText(ps))
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
	for _, p := range ps {
		if p.Path == "steps[1].review.bounce" {
			if p.Line == 0 {
				t.Fatalf("bounce problem has no line number: %+v", p)
			}
			return
		}
	}
	t.Fatalf("no steps[1].review.bounce problem: %v", ps)
}

// TestNewMessagesCarryLineNumbers: use_when, review.bounce and check.command
// point at the offending value's line, the same as their sibling checks
// (description, max_rounds).
func TestNewMessagesCarryLineNumbers(t *testing.T) {
	for _, tc := range []struct{ name, text, path string }{
		{"use_when", "version: 1\nname: x\nuse_when: " + strings.Repeat("x", 301) + "\nsteps:\n  - review: {}\n", "use_when"},
		{"check.command", "version: 1\nname: x\nsteps:\n  - check: { command: bogus }\n  - review: {}\n", "steps[0].check.command"},
		{"mode", "version: 1\nname: x\nmode: plan\nsteps:\n  - review: {}\n", "mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte(tc.text))
			for _, p := range ps {
				if p.Path == tc.path {
					if p.Line == 0 {
						t.Fatalf("%s problem has no line number: %+v", tc.name, p)
					}
					return
				}
			}
			t.Fatalf("no %s problem: %v", tc.path, ps)
		})
	}
}

func TestOrderWithChecksAndModes(t *testing.T) {
	for src, want := range map[string]string{
		"steps:\n  - first_line: {}\n  - check: { command: test }\n  - review: {}\n": "check steps must come first",
		"mode: review\nsteps:\n  - first_line: {}\n  - review: {}\n":                 "mode: review allows exactly one review step and nothing else",
		"mode: review\nsteps:\n  - review: { max_rounds: 2 }\n":                      "mode: review reviews once: max_rounds must be 1",
		"mode: review\nroles: { coder: reviewer }\nsteps:\n  - review: {}\n":         "mode: review has no coder step to map roles onto",
		"mode: review\nroles: { reviewer: coder }\nsteps:\n  - review: {}\n":         "mode: review has no coder step to map roles onto",
		"mode: plan\nsteps:\n  - review: {}\n":                                       "must be implement, review or artifact",
		"use_when: " + strings.Repeat("x", 301) + "\nsteps:\n  - review: {}\n":       "must be at most 300 bytes",
	} {
		_, ps := Parse([]byte("version: 1\nname: x\n" + src))
		if !strings.Contains(ProblemsText(ps), want) {
			t.Errorf("%q: got %s", src, ProblemsText(ps))
		}
	}
}

// TestDigestStepFirstOnly: digest is the report run's step, a deterministic
// runner-side stage that writes the period's report before any model runs
// (T3.1), so it is legal only as the first step of a mode: implement recipe,
// and at most once.
func TestDigestStepFirstOnly(t *testing.T) {
	r := mustParse(t, "version: 1\nname: x\nsteps:\n  - digest: {}\n  - review: {}\n")
	if len(r.Steps) != 2 || r.Steps[0] != (Step{Kind: StepDigest}) || r.Steps[1].Kind != StepReview {
		t.Fatalf("steps = %+v", r.Steps)
	}
	for _, tc := range []struct{ name, steps, path, want string }{
		{"after a check", "steps:\n  - check: { command: test }\n  - digest: {}\n  - review: {}\n", "steps[1]", "digest must be the first step"},
		{"twice", "steps:\n  - digest: {}\n  - digest: {}\n  - review: {}\n", "steps[1]", "digest may appear at most once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte("version: 1\nname: x\n" + tc.steps))
			if !problemsHave(ps, tc.path, tc.want) {
				t.Fatalf("problems = %v", ps)
			}
		})
	}
}

// TestAdversarialStepOrder: review level 3 is one step kind of its own,
// adversarial, legal at most once and only after the review step — the shape
// is ... review, adversarial, with nothing after it. Its knobs are its own
// max_rounds (1 to 10, the review step's range) and an optional bounce.
func TestAdversarialStepOrder(t *testing.T) {
	r := mustParse(t, "version: 1\nname: x\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - adversarial: { max_rounds: 3, bounce: first_line }\n")
	want := []Step{{Kind: StepFirstLine}, {Kind: StepReview, Bounce: true}, {Kind: StepAdversarial, MaxRounds: 3, Bounce: true}}
	if !slices.Equal(r.Steps, want) {
		t.Fatalf("steps = %+v", r.Steps)
	}
	if !UsesAdversarial(r) {
		t.Error("UsesAdversarial = false, want true")
	}
	// A bounce is optional, and the step is present without one.
	r = mustParse(t, "version: 1\nname: x\nsteps:\n  - review: {}\n  - adversarial: {}\n")
	if !slices.Equal(r.Steps, []Step{{Kind: StepReview}, {Kind: StepAdversarial}}) {
		t.Fatalf("steps = %+v", r.Steps)
	}
	if !UsesAdversarial(r) {
		t.Error("UsesAdversarial = false for a bare adversarial step, want true")
	}
	none := mustParse(t, "version: 1\nname: x\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n")
	if UsesAdversarial(none) {
		t.Error("UsesAdversarial = true for a recipe without the step, want false")
	}
	for _, tc := range []struct{ name, steps, path, want string }{
		{"before review", "steps:\n  - adversarial: {}\n  - review: {}\n", "steps[0]", "adversarial must come after review"},
		{"twice", "steps:\n  - review: {}\n  - adversarial: {}\n  - adversarial: {}\n", "steps[2]", "adversarial may appear at most once"},
		{"rounds 11", "steps:\n  - review: {}\n  - adversarial: { max_rounds: 11 }\n", "steps[1].adversarial.max_rounds", "between 1 and 10"},
		{"rounds 0", "steps:\n  - review: {}\n  - adversarial: { max_rounds: 0 }\n", "steps[1].adversarial.max_rounds", "between 1 and 10"},
		{"unknown knob", "steps:\n  - review: {}\n  - adversarial: { rounds: 2 }\n", "steps[1].adversarial.rounds", "is not a recipe key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte("version: 1\nname: x\n" + tc.steps))
			if !problemsHave(ps, tc.path, tc.want) {
				t.Fatalf("problems = %v", ps)
			}
		})
	}
	// The step is not a step of the two modes that pin their own steps.
	_, ps := Parse([]byte("version: 1\nname: x\nmode: review\nsteps:\n  - review: {}\n  - adversarial: {}\n"))
	if !problemsHave(ps, "steps", "mode: review allows exactly one review step and nothing else") {
		t.Fatalf("mode: review problems = %v", ps)
	}
	_, ps = Parse([]byte(artifactHead + artifactOutputs + "steps:\n  - adversarial: {}\n  - render: {}\n"))
	if !problemsHave(ps, "steps[0].adversarial", "is not a step of mode: artifact") {
		t.Fatalf("mode: artifact problems = %v", ps)
	}
	// The report run's shape stays exactly digest then review: a third step
	// would loop over a report no model wrote.
	_, ps = Parse([]byte("version: 1\nname: x\nsteps:\n  - digest: {}\n  - review: {}\n  - adversarial: {}\n"))
	if !problemsHave(ps, "steps[0]", "digest must be followed by one review step") {
		t.Fatalf("digest problems = %v", ps)
	}
}

// TestAdversarialBounceTarget: adversarial's bounce is first_line only, the
// same single target review.bounce has, and it needs a first_line step to
// bounce back to.
func TestAdversarialBounceTarget(t *testing.T) {
	_, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - review: {}\n  - adversarial: { bounce: first_line }\n"))
	if !problemsHave(ps, "steps[1]", "bounce: first_line needs a first_line step before the adversarial step") {
		t.Fatalf("no first_line problems = %v", ps)
	}
	_, ps = Parse([]byte("version: 1\nname: x\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - adversarial: { bounce: review }\n"))
	if !problemsHave(ps, "steps[2].adversarial.bounce", "bounce can only be first_line") {
		t.Fatalf("bad target problems = %v", ps)
	}
	_, ps = Parse([]byte("version: 1\nname: x\nsteps:\n  - review: {}\n  - adversarial: { bounce: true }\n"))
	if !problemsHave(ps, "steps[1].adversarial.bounce", "must be a string") {
		t.Fatalf("non-string bounce problems = %v", ps)
	}
}

// TestAdversarialRoundsDefault: a step without max_rounds is left at 0 by the
// parser, so the reader applies the rounds a step that writes none takes, as
// it does for a first_line or review step. That reader is the rigor mode's own
// senior row (rigor.AdversarialDefault, the owner's ruling of 2026-10-31):
// this package holds no default of its own to alias, which is why
// StageBound takes the number from its caller.
func TestAdversarialRoundsDefault(t *testing.T) {
	r := mustParse(t, "version: 1\nname: x\nsteps:\n  - review: {}\n  - adversarial: {}\n")
	if r.Steps[1].MaxRounds != 0 {
		t.Fatalf("MaxRounds = %d, want 0 (the default is the reader's)", r.Steps[1].MaxRounds)
	}
}

// TestDigestStepNeedsReview: the report run hands its pull request to the one
// senior review, so a digest step is followed by exactly one review step and
// nothing else, and only a mode: implement recipe may hold it (T3.1).
func TestDigestStepNeedsReview(t *testing.T) {
	for _, tc := range []struct{ name, steps string }{
		{"alone", "steps:\n  - digest: {}\n"},
		{"then a first line", "steps:\n  - digest: {}\n  - first_line: {}\n  - review: {}\n"},
		{"then two reviews", "steps:\n  - digest: {}\n  - review: {}\n  - review: {}\n"},
		{"then a check", "steps:\n  - digest: {}\n  - check: { command: test }\n  - review: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte("version: 1\nname: x\n" + tc.steps))
			if !problemsHave(ps, "steps[0]", "digest must be followed by one review step") {
				t.Fatalf("problems = %v", ps)
			}
		})
	}
	_, ps := Parse([]byte("version: 1\nname: x\nmode: review\nsteps:\n  - digest: {}\n  - review: {}\n"))
	if !problemsHave(ps, "steps[0]", "digest is only for mode: implement") {
		t.Fatalf("mode: review problems = %v", ps)
	}
	_, ps = Parse([]byte(artifactHead + artifactOutputs + "steps:\n  - digest: {}\n  - review: {}\n"))
	if !problemsHave(ps, "steps[0].digest", "is not a step of mode: artifact") {
		t.Fatalf("mode: artifact problems = %v", ps)
	}
	// The step takes no options, like plan and render: any key under it is
	// an unknown recipe key rather than a silently ignored one.
	_, ps = Parse([]byte("version: 1\nname: x\nsteps:\n  - digest: { max_rounds: 2 }\n  - review: {}\n"))
	if !problemsHave(ps, "steps[0].digest.max_rounds", "is not a recipe key") {
		t.Fatalf("options problems = %v", ps)
	}
}

// TestRunDigestInCatalog: the report run's recipe is a catalog recipe whose
// shape is exactly a digest step and one senior round, and its use_when keeps
// the routing skill off it (T3.1).
func TestRunDigestInCatalog(t *testing.T) {
	text, ok := CatalogText("run-digest")
	if !ok {
		t.Fatal("run-digest is not in the catalog")
	}
	r, ps := Parse(text)
	if len(ps) != 0 {
		t.Fatalf("problems = %s", ProblemsText(ps))
	}
	if len(r.Steps) != 2 || r.Steps[0] != (Step{Kind: StepDigest}) || r.Steps[1] != (Step{Kind: StepReview, MaxRounds: 1}) {
		t.Fatalf("steps = %+v", r.Steps)
	}
	if !strings.Contains(r.UseWhen, "scheduled") {
		t.Errorf("use_when = %q, want it to say the recipe is for scheduled run reviews", r.UseWhen)
	}
}

// TestUseWhenRefusesControlChars: use_when is shown verbatim by `recipes ls`
// and the routing skill, and a project recipe comes from the runs bucket
// (untrusted), so a control character or newline must be refused, not
// echoed to a terminal or a skill prompt.
func TestUseWhenRefusesControlChars(t *testing.T) {
	_, ps := Parse([]byte("version: 1\nname: x\nuse_when: \"a\\e[2Jb\\nc\"\nsteps:\n  - review: {}\n"))
	if !strings.Contains(ProblemsText(ps), unsafeCharMsg) {
		t.Fatal(ProblemsText(ps))
	}
	_, ps = Parse([]byte("version: 1\nname: x\nuse_when: \"a\\tb\"\nsteps:\n  - review: {}\n"))
	if !strings.Contains(ProblemsText(ps), unsafeCharMsg) {
		t.Fatalf("tab: %s", ProblemsText(ps))
	}
}

// TestHasControlCharClasses: every class hasControlChar refuses, each alone
// so a mutation that drops one class cannot hide behind another still
// tripping the function on the same input. A string with none of them is
// not flagged.
func TestHasControlCharClasses(t *testing.T) {
	for name, r := range map[string]rune{
		"C0 (tab)":            '\t',
		"C0 (newline)":        '\n',
		"DEL":                 0x7f,
		"C1 (0x80)":           0x80,
		"C1 (NEL, 0x85)":      0x85,
		"C1 (CSI, 0x9b)":      0x9b,
		"C1 (0x9f)":           0x9f,
		"Cf (RLO bidi)":       '‮',
		"Cf (zero-width sp)":  '​',
		"line separator":      ' ',
		"paragraph separator": ' ',
	} {
		t.Run(name, func(t *testing.T) {
			if !hasControlChar("a" + string(r) + "b") {
				t.Fatalf("hasControlChar(%q) = false, want true", string(r))
			}
		})
	}
	if hasControlChar("plain ascii, no funny business") {
		t.Fatal("hasControlChar(plain) = true, want false")
	}
	if hasControlChar("unicode café, 日本語, emoji 🎉 are all fine") {
		t.Fatal("hasControlChar(ordinary unicode) = true, want false")
	}
}

// TestDescriptionRefusesUnsafeChars: description is shown verbatim too
// (recipes ls, --json), and is just as untrusted as use_when for a project
// or repository recipe, so it gets the same refusal.
func TestDescriptionRefusesUnsafeChars(t *testing.T) {
	for name, desc := range map[string]string{
		"C1 CSI":         "a\\u009b31mb",
		"bidi override":  "evil ‮ txt",
		"line separator": "a b",
	} {
		t.Run(name, func(t *testing.T) {
			_, ps := Parse([]byte("version: 1\nname: x\ndescription: \"" + desc + "\"\nsteps:\n  - review: {}\n"))
			if !strings.Contains(ProblemsText(ps), unsafeCharMsg) {
				t.Fatalf("problems: %s", ProblemsText(ps))
			}
		})
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

// TestReviewPRRange: review-only needs exactly one pull request, pick needs
// 2 to 4, and any other name (a custom mode: review recipe) is
// unconstrained.
//
// Mutation (swap the "pick" entry's {2, 4} for {1, 4}): this test fails,
// finding ok but lo == 1 for "pick".
func TestReviewPRRange(t *testing.T) {
	if lo, hi, ok := ReviewPRRange("review-only"); !ok || lo != 1 || hi != 1 {
		t.Fatalf("review-only: lo=%d hi=%d ok=%v, want 1,1,true", lo, hi, ok)
	}
	if lo, hi, ok := ReviewPRRange("pick"); !ok || lo != 2 || hi != 4 {
		t.Fatalf("pick: lo=%d hi=%d ok=%v, want 2,4,true", lo, hi, ok)
	}
	if _, _, ok := ReviewPRRange("my-custom-review"); ok {
		t.Fatal("my-custom-review: ok = true, want false (unconstrained)")
	}
}

// --- mode: artifact (0.8.0, docs/design/artifact-runs.md §3) ---------------
//
// The third Recipe.Mode: a run that renders files into the run's bucket and
// opens no pull request. Parse takes only bytes, so nothing here knows where
// a recipe came from, and there is deliberately no source-based refusal: the
// catalog may ship an artifact recipe exactly as it ships mode: review ones
// (plan P2).

// artifactHead is the shortest artifact recipe's head; every case below adds
// its own outputs block and steps.
const artifactHead = "version: 1\nname: art\nmode: artifact\n"

// artifactOutputs is a legal outputs block, as compact as YAML allows.
const artifactOutputs = "outputs: { globs: [out/*.png] }\n"

// problemsHave reports whether ps holds a problem at path whose message
// contains msg.
func problemsHave(ps []Problem, path, msg string) bool {
	for _, p := range ps {
		if p.Path == path && strings.Contains(p.Message, msg) {
			return true
		}
	}
	return false
}

// TestArtifactOutputsParse: the outputs block's keys, the defaults behind the
// three caps and the probe (which the accessors apply, never Parse), an
// artifact recipe's two legal step shapes, and Outputs being non-nil exactly
// when the mode is artifact.
func TestArtifactOutputsParse(t *testing.T) {
	r := mustParse(t, artifactHead+"description: Renders the sizzle\n"+
		"outputs:\n  globs: [\"out/*.mp4\", \"out/*.png\"]\n  max_files: 4\n"+
		"  max_bytes: 1048576\n  max_total_bytes: 4194304\n  probe: probe\n"+
		"steps:\n  - plan: {}\n  - render: {}\n")
	out := r.Outputs
	if out == nil {
		t.Fatal("an artifact recipe's outputs block parsed to nothing")
	}
	if !slices.Equal(out.Globs, []string{"out/*.mp4", "out/*.png"}) || out.MaxFiles != 4 ||
		out.MaxBytes != 1048576 || out.MaxTotalBytes != 4194304 || out.Probe != "probe" {
		t.Fatalf("outputs = %+v", out)
	}
	if out.FileCount() != 4 || out.MaxFileBytes() != 1048576 || out.MaxSetBytes() != 4194304 {
		t.Fatalf("caps = %d %d %d", out.FileCount(), out.MaxFileBytes(), out.MaxSetBytes())
	}
	if r.Mode != ModeArtifact || len(r.Steps) != 2 || r.Steps[0].Kind != StepPlan || r.Steps[1].Kind != StepRender {
		t.Fatalf("recipe = %+v", r)
	}

	// A block that names only its globs leaves all three caps at their
	// default and probes nothing (P6: max_bytes is per artifact,
	// max_total_bytes is the whole set).
	r = mustParse(t, artifactHead+artifactOutputs+"steps:\n  - render: {}\n")
	out = r.Outputs
	if out.FileCount() != DefaultOutputFiles || out.MaxFileBytes() != DefaultOutputBytes ||
		out.MaxSetBytes() != DefaultOutputTotalBytes || out.Probe != "none" {
		t.Fatalf("defaults = %+v (%d, %d, %d)", out, out.FileCount(), out.MaxFileBytes(), out.MaxSetBytes())
	}
	const want = 8
	if DefaultOutputFiles != want || DefaultOutputBytes != 256<<20 || DefaultOutputTotalBytes != 2<<30 {
		t.Fatalf("defaults changed: %d files, %d and %d bytes", DefaultOutputFiles, DefaultOutputBytes, DefaultOutputTotalBytes)
	}

	// Outputs is non-nil exactly when Mode is ModeArtifact.
	if other := mustParse(t, "version: 1\nname: a\nsteps:\n  - review: {}\n"); other.Outputs != nil {
		t.Fatalf("Outputs = %+v on a mode: implement recipe", other.Outputs)
	}
	// An empty block is refused (it names nothing to deliver), so the zero
	// Outputs of a hand-built recipe can never come from Parse.
	if _, ps := Parse([]byte(artifactHead + "outputs: {}\nsteps:\n  - render: {}\n")); !problemsHave(ps, "outputs.globs", "is required") {
		t.Fatalf("outputs: {}: %v", ps)
	}
}

func TestArtifactModeRequiresOutputs(t *testing.T) {
	// A mode: artifact recipe without outputs: declares no deliverable, so
	// there is nothing to upload and nothing to cap.
	_, ps := Parse([]byte(artifactHead + "steps:\n  - render: {}\n"))
	if !problemsHave(ps, "outputs", "is required for mode: artifact") {
		t.Fatalf("problems = %v", ps)
	}
	// outputs is refused in the other two modes: a run that opens a pull
	// request delivers its diff, not a file set, and the caps would never be
	// read by anything.
	for _, tc := range []struct{ name, text string }{
		{"implement, no mode key", "version: 1\nname: art\n" + artifactOutputs + "steps:\n  - review: {}\n"},
		{"implement, written out", "version: 1\nname: art\nmode: implement\n" + artifactOutputs + "steps:\n  - review: {}\n"},
		{"review", "version: 1\nname: art\nmode: review\n" + artifactOutputs + "steps:\n  - review: { max_rounds: 1 }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ps := Parse([]byte(tc.text)); !problemsHave(ps, "outputs", "is only for mode: artifact") {
				t.Fatalf("problems = %v", ps)
			}
		})
	}
}

func TestArtifactStepsArePlanThenRenderOrRenderAlone(t *testing.T) {
	for _, tc := range []struct{ name, steps string }{
		{"no steps", "steps: []\n"},
		{"plan alone", "steps:\n  - plan: {}\n"},
		{"two renders", "steps:\n  - render: {}\n  - render: {}\n"},
		{"two plans", "steps:\n  - plan: {}\n  - plan: {}\n  - render: {}\n"},
		{"render before plan", "steps:\n  - render: {}\n  - plan: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte(artifactHead + artifactOutputs + tc.steps))
			if !problemsHave(ps, "steps", "one render step, optionally after one plan step") {
				t.Fatalf("problems = %v", ps)
			}
		})
	}
	for _, steps := range []string{
		"steps:\n  - render: {}\n",
		"steps:\n  - plan: {}\n  - render: {}\n",
	} {
		if r, ps := Parse([]byte(artifactHead + artifactOutputs + steps)); len(ps) != 0 {
			t.Errorf("%q: %s", steps, ProblemsText(ps))
		} else if r.Mode != ModeArtifact {
			t.Errorf("%q: mode = %q", steps, r.Mode)
		}
	}
}

func TestArtifactModeRefusesTheAgentSteps(t *testing.T) {
	// check, first_line and review are the agent loop's steps: an artifact
	// recipe has no implement to gate and one optional plan stage.
	for _, tc := range []struct{ kind, step string }{
		{"check", "check: { command: test }"},
		{"first_line", "first_line: {}"},
		{"review", "review: {}"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			_, ps := Parse([]byte(artifactHead + artifactOutputs + "steps:\n  - " + tc.step + "\n  - render: {}\n"))
			if !problemsHave(ps, "steps[0]."+tc.kind, "is not a step of mode: artifact") {
				t.Fatalf("problems = %v", ps)
			}
		})
	}
	// review.bounce is refused with review itself, so nothing separate
	// refuses it: there is no review step for a bounce to belong to.
	_, ps := Parse([]byte(artifactHead + artifactOutputs + "steps:\n  - review: { bounce: first_line }\n"))
	if !problemsHave(ps, "steps[0].review", "is not a step of mode: artifact") {
		t.Fatalf("problems = %v", ps)
	}
	// roles map the coder and reviewer onto each other: an artifact recipe
	// has neither role.
	_, ps = Parse([]byte(artifactHead + "roles: { reviewer: coder }\n" + artifactOutputs + "steps:\n  - render: {}\n"))
	if !problemsHave(ps, "roles", "no agent steps to map roles onto") {
		t.Fatalf("problems = %v", ps)
	}
	// The mirror: plan and render are refused in the other two modes, and
	// each is its own message, not "is not a step type".
	for _, tc := range []struct{ mode, steps string }{
		{"", "steps:\n  - render: {}\n  - review: {}\n"},
		{"mode: review\n", "steps:\n  - render: {}\n"},
	} {
		_, ps := Parse([]byte("version: 1\nname: a\n" + tc.mode + tc.steps))
		if !problemsHave(ps, "steps[0]", "is only for mode: artifact") {
			t.Fatalf("%q: problems = %v", tc.steps, ps)
		}
		if strings.Contains(ProblemsText(ps), "is not a step type") {
			t.Fatalf("%q: an artifact step gets the unknown-step message: %v", tc.steps, ps)
		}
	}
}

func TestOutputsGlobsRefuseEscapes(t *testing.T) {
	for _, tc := range []struct{ name, globs, want string }{
		{"absolute", "[/etc/passwd]", "must be relative to the checkout root"},
		{"dot dot element", "[out/../../etc/passwd]", "must not contain a .. path element"},
		{"dot dot alone", "[\"..\"]", "must not contain a .. path element"},
		{"dot dot trailing", "[out/..]", "must not contain a .. path element"},
		{"empty list", "[]", "must name at least one glob"},
		{"empty glob", "[\"\"]", "must not be empty"},
		{"not a list", "out/*.png", "must be a list of globs"},
		{"not strings", "[1]", "must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte(artifactHead + "outputs: { globs: " + tc.globs + " }\nsteps:\n  - render: {}\n"))
			if !strings.Contains(ProblemsText(ps), tc.want) {
				t.Fatalf("globs %s: problems %v lack %q", tc.globs, ps, tc.want)
			}
		})
	}
	// A name that merely begins with dots, and a dot inside a name, are not
	// escapes: only a whole .. path element is.
	r := mustParse(t, artifactHead+"outputs: { globs: [\"out/..hidden/*.png\", \"out/a..png\"] }\nsteps:\n  - render: {}\n")
	if !slices.Equal(r.Outputs.Globs, []string{"out/..hidden/*.png", "out/a..png"}) {
		t.Fatalf("globs = %v", r.Outputs.Globs)
	}
}

func TestOutputsCapsHaveCeilings(t *testing.T) {
	for _, tc := range []struct{ name, block, path, want string }{
		{"33 files", "max_files: 33", "outputs.max_files", "must be between 1 and 32"},
		{"0 files", "max_files: 0", "outputs.max_files", "must be between 1 and 32"},
		{"files not a number", "max_files: many", "outputs.max_files", "must be a whole number"},
		{"2 GiB plus one", "max_bytes: 2147483649", "outputs.max_bytes", "must be between 1 and 2147483648 bytes (2 GiB)"},
		{"0 bytes", "max_bytes: 0", "outputs.max_bytes", "must be between 1 and"},
		{"negative bytes", "max_bytes: -1", "outputs.max_bytes", "must be between 1 and"},
		{"16 GiB plus one", "max_total_bytes: 17179869185", "outputs.max_total_bytes", "must be between 1 and 17179869184 bytes (16 GiB)"},
		{"0 total", "max_total_bytes: 0", "outputs.max_total_bytes", "must be between 1 and"},
		{"unknown probe", "probe: lint", "outputs.probe", "must be probe or none"},
		{"probe not a string", "probe: 5", "outputs.probe", "must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := Parse([]byte(artifactHead + "outputs: { globs: [out/*.png], " + tc.block + " }\nsteps:\n  - render: {}\n"))
			if !problemsHave(ps, tc.path, tc.want) {
				t.Fatalf("%s: problems %v lack %s: %q", tc.block, ps, tc.path, tc.want)
			}
		})
	}
	// The ceilings themselves are legal, and both probe values are.
	r := mustParse(t, artifactHead+"outputs:\n  globs: [out/*.png]\n  max_files: 32\n  max_bytes: 2147483648\n  max_total_bytes: 17179869184\n  probe: none\nsteps:\n  - render: {}\n")
	if r.Outputs.MaxFiles != 32 || r.Outputs.MaxBytes != MaxOutputBytes || r.Outputs.MaxTotalBytes != MaxOutputTotalBytes {
		t.Fatalf("outputs = %+v", r.Outputs)
	}
}

// TestOutputTotalCapHasACeiling is the set cap's own pair of rules: above its
// ceiling it is refused like the other two caps, and a max_total_bytes below
// max_bytes with max_files of 1 is refused too — the set is that one
// artifact, so the smaller cap silently governs and one legal artifact
// already breaks the set (plan Task 1's outputs rules; the plan lists this
// test under internal/config, but an outputs block's caps are only ever
// parsed here, by Parse).
func TestOutputTotalCapHasACeiling(t *testing.T) {
	_, ps := Parse([]byte(artifactHead + "outputs: { globs: [out/*.png], max_total_bytes: 34259738368 }\nsteps:\n  - render: {}\n"))
	if !strings.Contains(ProblemsText(ps), "must be between 1 and 17179869184 bytes (16 GiB)") {
		t.Fatalf("problems = %v", ps)
	}
	one := "outputs: { globs: [out/*.png], max_files: 1, max_bytes: 1048576, max_total_bytes: 262144 }\n"
	_, ps = Parse([]byte(artifactHead + one + "steps:\n  - render: {}\n"))
	if !problemsHave(ps, "outputs.max_total_bytes", "must be at least outputs.max_bytes") {
		t.Fatalf("problems = %v", ps)
	}
	// With room for more than one artifact the pair is a set cap doing its
	// job (a set of files each under max_bytes may still be over the total),
	// so it is legal.
	if _, ps := Parse([]byte(artifactHead + "outputs: { globs: [out/*.png], max_files: 2, max_bytes: 1048576, max_total_bytes: 262144 }\nsteps:\n  - render: {}\n")); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
	// Only the two written caps are compared: a defaulted one is not a
	// relation the author wrote.
	if _, ps := Parse([]byte(artifactHead + "outputs: { globs: [out/*.png], max_files: 1, max_total_bytes: 262144 }\nsteps:\n  - render: {}\n")); len(ps) != 0 {
		t.Fatalf("problems = %v", ps)
	}
}

// TestUsesV08IsPresenceNotValue: UsesV08 decides from the presence of a
// 0.8.0 construct, never from a parsed value, the rule UsesV07 already
// follows: a 0.7 parser refuses mode: artifact, the plan and render step
// kinds and the outputs key outright, so any one of them means the run needs
// a 0.8 runner even when everything around it looks like a no-op.
//
// (The plan's own example — "mode: implement plus an outputs: {} block" —
// cannot exist: an outputs block is refused outright in the other two modes,
// by the same task. The presence case here is therefore the artifact recipe
// whose block holds nothing but its globs, every cap and the probe at their
// defaults.)
func TestUsesV08IsPresenceNotValue(t *testing.T) {
	// Not one cap written, and it still counts.
	r := mustParse(t, artifactHead+artifactOutputs+"steps:\n  - render: {}\n")
	if !UsesV08(r) {
		t.Fatal("an artifact recipe whose outputs block holds only its globs is not V08")
	}
	// Each construct on its own counts, whatever it sits beside.
	for _, text := range []string{
		"version: 1\nname: a\nmode: artifact\n" + artifactOutputs + "steps:\n  - plan: {}\n  - render: {}\n",
		"version: 1\nname: a\nmode: artifact\noutputs: { globs: [x], max_files: 8, probe: none }\nsteps:\n  - render: {}\n",
	} {
		if r := mustParse(t, text); !UsesV08(r) {
			t.Errorf("not V08: %s", text)
		}
	}
	// A recipe using only 0.7 keys, and one using none, do not.
	old := mustParse(t, "version: 1\nname: a\nuse_when: typical work\nmode: implement\nsteps:\n  - check: { command: test }\n  - first_line: {}\n  - review: { bounce: first_line }\n")
	if UsesV08(old) {
		t.Fatal("a mode: implement recipe with 0.7 keys only is V08")
	}
	if !UsesV07(old) {
		t.Fatal("a mode: implement recipe with 0.7 keys only is not V07")
	}
	if r := mustParse(t, "version: 1\nname: a\nsteps:\n  - review: {}\n"); UsesV08(r) || UsesV07(r) {
		t.Fatal("a bare recipe uses a 0.7 or 0.8 key")
	}
}
