package runner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The level's persona reaches the prompt (LD1, LV2.2): the coder's text leads
// the brief the implement and fix stages read, the reviewer's follows the head
// of every review stage's, and the level both come from is the one the run has
// reached — not the one it was launched with, and not the other role's or
// another level's.
//
// The word "persona" is deliberately absent from internal/runner/outcome.go:
// readiness is one deterministic rule over scored findings (D7, LD1), the
// persona shapes behaviour and decides nothing, and the guard at the bottom of
// this file holds the two apart by the structure of the source rather than by
// an assertion some later edit could quietly satisfy.

// promptDataForPersona is the brief every level's text is injected into here:
// the run contract as a first run has it.
func promptDataForPersona() PromptData {
	return PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/s"}
}

// TestPromptsCarryTheirLevelsPersona is the coverage half: every level's coder
// text is in the briefs the coder stages read, every level's reviewer text is
// in the prompts the review stages read, and neither carries the other role's.
//
// Mutation (drop the personaBlock call from SystemPrompt, FixPrompt or
// reviewBrief, or hand the reviewer's text to a coder stage): the level's own
// text is missing from the prompt, or the other role's is in it.
func TestPromptsCarryTheirLevelsPersona(t *testing.T) {
	for _, level := range rigor.Rigors {
		coder := rigor.Persona(level, rigor.Coder)
		reviewer := rigor.Persona(level, rigor.Reviewer)
		if strings.TrimSpace(coder) == "" || strings.TrimSpace(reviewer) == "" {
			t.Fatalf("rigor.Persona(%s, ...) answers an empty text; the table this test leans on is broken", level)
		}
		for name, prompt := range map[string]string{
			"the implement and fix system prompt": SystemPrompt(promptDataForPersona(), "", coder),
			"the fix prompt":                      FixPrompt([]Finding{{Severity: "blocker", Summary: "the handler ignores the error"}}, "", coder),
		} {
			if !strings.Contains(prompt, coder) {
				t.Errorf("%s at %s does not carry the level's coder persona:\n%s", name, level, prompt)
			}
			if strings.Contains(prompt, reviewer) {
				t.Errorf("%s at %s carries the reviewer persona too: a coder told how to judge its own work grades it:\n%s", name, level, prompt)
			}
		}
		for name, prompt := range map[string]string{
			"the default review prompt":  ReviewPrompt("", "", "origin/main", DiffSize{}, false, reviewer),
			"a skill review prompt":      ReviewPrompt("/review", "", "origin/main", DiffSize{}, false, reviewer),
			"a prompt-file review":       ReviewPrompt("", "Check the SQL.", "origin/main", DiffSize{}, false, reviewer),
			"the artifact review prompt": reviewArtifactPrompt("", "", "origin/main", DiffSize{}, false, reviewer),
			"a pick prompt":              PickPrompt("", "", []int{9, 7}, []string{"/review/9", "/review/7"}, reviewer),
		} {
			if !strings.Contains(prompt, reviewer) {
				t.Errorf("%s at %s does not carry the level's reviewer persona:\n%s", name, level, prompt)
			}
			if strings.Contains(prompt, coder) {
				t.Errorf("%s at %s carries the coder persona too:\n%s", name, level, prompt)
			}
		}
	}
	// A configured review may be a slash command, and the reviewer's skill only
	// loads when the prompt starts with it: the persona follows that command
	// rather than leading the prompt.
	if got := ReviewPrompt("/review", "", "origin/main", DiffSize{}, false, rigor.Persona(rigor.Pro, rigor.Reviewer)); !strings.HasPrefix(got, "/review ") {
		t.Errorf("a skill review prompt does not start with its command:\n%.120s", got)
	}
}

// TestPromptsReadTheirPersonaThroughTheSeam: personaFor is the one seam, and a
// stub stands where rigor.Persona would be — so a prompt built through the
// run's own accessors carries what the seam answers and not what the table
// holds. It is the seam rather than the text that this pins: an accessor that
// called rigor.Persona itself, or that read another level's row, would answer
// the table's text here and fail.
//
// Mutation (call rigor.Persona from coderPersona or reviewerPersona instead of
// personaFor): both prompts carry the table's text and not the stub's.
func TestPromptsReadTheirPersonaThroughTheSeam(t *testing.T) {
	defer func(prev func(rigor.Rigor, rigor.Role) string) { personaFor = prev }(personaFor)
	personaFor = func(level rigor.Rigor, role rigor.Role) string {
		return "STUB PERSONA FOR " + string(level) + "/" + string(role)
	}
	r := &run{rec: &runstore.Record{Rigor: &runstore.RigorRecord{Level: rigor.Critical}}}
	const want = "STUB PERSONA FOR critical/coder"
	if got := r.coderPersona(); got != want {
		t.Fatalf("coderPersona() = %q, want %q", got, want)
	}
	if got := r.reviewerPersona(); got != "STUB PERSONA FOR critical/reviewer" {
		t.Errorf("reviewerPersona() = %q, want the reviewer half of the same seam", got)
	}
	if got := SystemPrompt(promptDataForPersona(), "", r.coderPersona()); !strings.Contains(got, want) {
		t.Errorf("the system prompt does not carry what the seam answers:\n%s", got)
	}
	if got := FixPrompt(nil, "", r.coderPersona()); !strings.Contains(got, want) {
		t.Errorf("the fix prompt does not carry what the seam answers:\n%s", got)
	}
	if got := ReviewPrompt("", "", "origin/main", DiffSize{}, false, r.reviewerPersona()); !strings.Contains(got, "STUB PERSONA FOR critical/reviewer") {
		t.Errorf("the review prompt does not carry what the seam answers:\n%s", got)
	}
	// A run that has resolved no level yet asks for no stance at all rather
	// than for another level's: rigor.Persona answers "" for a pair the table
	// does not hold, and only the stub makes that visible here.
	if got := (&run{rec: &runstore.Record{}}).coderPersona(); got != "STUB PERSONA FOR /coder" {
		t.Errorf("coderPersona() with no level resolved = %q, want the empty level's own answer", got)
	}
}

// TestPromptAfterARiseCarriesTheRaisedLevelsPersona: D22's second moment can
// raise a run's level mid-flight — an area rule that matches the diff the fix
// round just wrote — and a prompt built after that moment carries the level the
// run is at now. The pair below is one repository's configuration, one task and
// one diff, resolved with and without the round moment: the same inputs answer
// two different stances, which is the whole of what "the level reached when the
// prompt is built" means.
//
// Mutation (read the bootstrap level, or the spec's requested level, instead of
// reachedRigor): the raised run's prompts carry POC's text.
func TestPromptAfterARiseCarriesTheRaisedLevelsPersona(t *testing.T) {
	const block = "rigor:\n  default: poc\n  areas:\n    - { name: payments, match: \"internal/payments/**\", level: critical }\n"
	raised := raisedRun(t, raisedCase{block: block, moments: []string{runstore.RigorAtRound}})
	stayed := raisedRun(t, raisedCase{block: block})
	if got := raised.reachedRigor(); got != rigor.Critical {
		t.Fatalf("the raised run resolved %q; want critical, or the case proves nothing", got)
	}
	if got := stayed.reachedRigor(); got != rigor.POC {
		t.Fatalf("the run with no round moment resolved %q; want poc, or the pair is not a rise", got)
	}
	pocCoder, criticalCoder := rigor.Persona(rigor.POC, rigor.Coder), rigor.Persona(rigor.Critical, rigor.Coder)
	if pocCoder == criticalCoder {
		t.Fatal("both levels answer the same coder text; the pair below would prove nothing")
	}
	after := FixPrompt([]Finding{{Severity: "blocker", Summary: "the handler ignores the error"}}, "", raised.coderPersona())
	if !strings.Contains(after, criticalCoder) {
		t.Errorf("the fix prompt built after the rise does not carry critical's coder persona:\n%s", after)
	}
	if strings.Contains(after, pocCoder) {
		t.Errorf("the fix prompt built after the rise still carries poc's coder persona:\n%s", after)
	}
	if before := FixPrompt(nil, "", stayed.coderPersona()); !strings.Contains(before, pocCoder) {
		t.Errorf("the fix prompt of the run that did not rise does not carry poc's coder persona:\n%s", before)
	}
	rev := ReviewPrompt("", "", "origin/main", DiffSize{}, false, raised.reviewerPersona())
	if !strings.Contains(rev, rigor.Persona(rigor.Critical, rigor.Reviewer)) {
		t.Errorf("the review prompt built after the rise does not carry critical's reviewer persona:\n%s", rev)
	}
	if strings.Contains(rev, rigor.Persona(rigor.POC, rigor.Reviewer)) {
		t.Errorf("the review prompt built after the rise still carries poc's reviewer persona:\n%s", rev)
	}
}

// TestPromptsGrowByTheirPersonaAloneWithinTheBudget: the persona is prepended
// to a brief and inserted once into a review prompt, and it is the only thing
// either change adds — a builder that wrote anything else beside the level's
// stance (a heading, a second copy, a repetition of a rule) would be a prompt
// nobody reviewed. The persona's own size is bounded too: it is the room the
// prompt spends before it reaches the change, and internal/rigor's
// persona_test.go holds every text to the same bound (reconciliation U1).
//
// Mutation (append the persona rather than prepend it, or write it twice): the
// prompt is no longer the persona-free prompt with one occurrence of the text
// in it.
func TestPromptsGrowByTheirPersonaAloneWithinTheBudget(t *testing.T) {
	// promptPersonaBudget is internal/rigor's personaBudget (4096 bytes): that
	// package's own test is where the texts are held to it, and this is the
	// half the runner owes — a prompt it hands a model grows by no more than
	// the stance is allowed to take.
	const promptPersonaBudget = 4096
	d := promptDataForPersona()
	for _, level := range rigor.Rigors {
		for _, role := range []rigor.Role{rigor.Coder, rigor.Reviewer} {
			if p := rigor.Persona(level, role); len(p) > promptPersonaBudget {
				t.Errorf("rigor.Persona(%s, %s) is %d bytes, over the %d a prompt may spend on a stance", level, role, len(p), promptPersonaBudget)
			}
		}
		coder, reviewer := rigor.Persona(level, rigor.Coder), rigor.Persona(level, rigor.Reviewer)

		bare, with := SystemPrompt(d, "", ""), SystemPrompt(d, "", coder)
		if with != coder+"\n\n"+bare {
			t.Errorf("the system prompt at %s is not the persona-free one with the coder text in front of it:\n%q", level, with)
		}
		bareFix, withFix := FixPrompt(nil, "", ""), FixPrompt(nil, "", coder)
		if withFix != coder+"\n\n"+bareFix {
			t.Errorf("the fix prompt at %s is not the persona-free one with the coder text in front of it:\n%q", level, withFix)
		}
		bareRev := ReviewPrompt("", "", "origin/main", DiffSize{}, false, "")
		withRev := ReviewPrompt("", "", "origin/main", DiffSize{}, false, reviewer)
		if got := strings.Count(withRev, reviewer); got != 1 {
			t.Errorf("the review prompt at %s carries the reviewer text %d times, want once", level, got)
		}
		if strings.Replace(withRev, reviewer+"\n\n", "", 1) != bareRev {
			t.Errorf("the review prompt at %s differs from the persona-free one by more than the reviewer text:\n%q", level, withRev)
		}
	}
}

// TestTheReadinessRuleCannotSeeThePersona is the global constraint, pinned by
// structure (LV2.2): readiness is one deterministic rule inside Decide (D7),
// Decide takes the resolved level and never the persona text, and a persona
// reaching outcome.go would not be a bug any behavioural test could see — every
// persona is non-empty and every prompt is a string, so nothing about a run's
// ending changes until a person reads the sentence.
//
// Three things are held, each over the parsed source and not over its text:
//
//   - outcome.go names nothing persona-shaped. The scan is over identifiers, so
//     prose that mentions a persona is not a reference, and it matches on the
//     name case-insensitively, so a helper spelled PersonaTexts is the same
//     reference as personaFor.
//   - the only non-test file in the package that reaches for rigor.Persona or
//     personaFor is persona.go, the one seam (LV2.2). Without this the first
//     clause would be a rule about one file that any new helper beside it could
//     walk around.
//   - Decide's own parameters carry no persona: no parameter's name or type is
//     persona-shaped, and the only string parameter is the commit the
//     verification is read on. A persona is a string, so a string parameter is
//     the one shape a persona text could arrive in; a later task that adds one
//     says so in decideStringParams rather than in a diff nobody reads.
//
// Mutation (add a `persona string` parameter to Decide, or a helper in
// outcome.go that reads personaFor): the guard fails with the file and line.
func TestTheReadinessRuleCannotSeeThePersona(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "outcome.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing outcome.go: %v", err)
	}
	ast.Inspect(parsed, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || !strings.Contains(strings.ToLower(id.Name), "persona") {
			return true
		}
		t.Errorf("outcome.go:%d names %s: the readiness rule is one deterministic rule over scored findings and never reads a persona (D7, LD1)", fset.Position(id.Pos()).Line, id.Name)
		return true
	})

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "persona.go" {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "rigor" && x.Sel.Name == "Persona" {
					t.Errorf("%s:%d calls rigor.Persona directly: the package reads a level's text through personaFor alone, so that one file is where a prompt's persona comes from (LV2.2)", name, fset.Position(x.Pos()).Line)
				}
			case *ast.Ident:
				if x.Name == "personaFor" {
					t.Errorf("%s:%d names personaFor: the one seam is persona.go", name, fset.Position(x.Pos()).Line)
				}
			}
			return true
		})
	}

	decide := funcDecl(parsed, "Decide")
	if decide == nil || decide.Type.Params == nil {
		t.Fatal("outcome.go has no Decide with a parameter list: this guard is over the signature readiness is decided by")
	}
	for _, field := range decide.Type.Params.List {
		typ := types.ExprString(field.Type)
		if strings.Contains(strings.ToLower(typ), "persona") {
			t.Errorf("Decide takes a %s: the persona decides nothing, and a parameter is the one way it could reach readiness (D7, LD1)", typ)
		}
		for _, name := range field.Names {
			if strings.Contains(strings.ToLower(name.Name), "persona") {
				t.Errorf("Decide takes a parameter named %s: the persona decides nothing (D7, LD1)", name.Name)
			}
			if typ == "string" && !slices.Contains(decideStringParams, name.Name) {
				t.Errorf("Decide takes the string parameter %s beside %v: a persona is a string, so a string parameter is the shape a persona text could arrive in — add it to decideStringParams only when it carries something else (D7, LD1)", name.Name, decideStringParams)
			}
		}
	}
}

// decideStringParams are the string parameters Decide may take: the commit the
// verification is read on, and nothing else.
var decideStringParams = []string{"finalSHA"}

// funcDecl is the named function declaration of a parsed file, nil when the
// file has none by that name.
func funcDecl(f *ast.File, name string) *ast.FuncDecl {
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}
