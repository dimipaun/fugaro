package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// status_switch_lint_test.go is the static half of model-credentials-routing
// PR11's rule that every reader of a run's status meets `paused`. About three
// dozen files switch on a status, and the six that would miscount are fixed by
// hand; what keeps the rest honest is not a second sweep by hand but a read of
// the shapes a status is switched on with, which cannot miss one.
//
// The rule is about the SHAPE of a switch rather than about a list of sites. A
// switch is one over runstore's status vocabulary when every one of its
// non-default case arms is a `runstore.Status…` selector (under a `string(…)`
// conversion or not) or a string literal that spells one of runstore's own
// words, and when it has at least one such arm. Such a switch enumerates the
// vocabulary by construction, so one that names neither `paused` nor `default`
// is a reader that read the set before the word existed and answers wrong for a
// run that suspended itself (docs/model-credentials-routing/contract-pause.md,
// "The status word").
//
// What it deliberately does not flag is the shape the read side is full of: a
// switch whose arms mix runview's own launch words (StatusPending,
// StatusLaunching, StatusUnlaunched, StatusError) with one or two runstore
// words is a predicate over a row's derived status rather than an enumeration
// of the vocabulary, and the question the paused word raises there is answered
// beside it (a paused chain being in flight is pauseInFlight, PR12's). A
// `default` arm answers for every word the switch does not name, which is the
// other way this rule is satisfied, and a switch whose arms say nothing drawn
// from the vocabulary is a switch over something else that happens to hold
// status-shaped text (update_test.go's stage words).
//
// What it cannot see: a comparison (`if status == runstore.StatusFailed`)
// rather than an enumeration, a map or a table keyed by status, and a switch
// that spells words no runstore constant spells. Those are blind spots and not
// exemptions; the half that catches a status read written as any of them is the
// per-reader tests, one sub-test per reader, in the packages that own them
// (TestEveryStatusReaderHandlesPaused in internal/runview, internal/userstats
// and internal/watch, and the two CountsPausedApart tests in internal/budget
// and internal/digest).
func TestEverySwitchOverStatusKnowsPaused(t *testing.T) {
	// The guard's own premise, pinned by the compiler rather than by a string:
	// the constant whose name it looks for holds the word it looks for. A
	// rename of either is this failing rather than the lint reading nothing.
	if got := string(runstore.StatusPaused); got != "paused" {
		t.Fatalf("runstore.StatusPaused is %q, and this lint looks for %s, which holds the word paused", got, statusPausedConstant)
	}
	switches := statusSwitchesInTree(t, "../..")
	if len(switches) == 0 {
		t.Fatal("the walk found no switch over runstore's status vocabulary, so this lint is reading nothing")
	}
	for _, sw := range switches {
		if !sw.forgetsPaused() {
			continue
		}
		t.Errorf("%s: the switch in %s enumerates runstore's statuses (%s) and names neither paused nor a default, so a run that suspended itself is answered as something it is not",
			sw.where(), sw.fn, strings.Join(sw.arms, ", "))
	}
}

// TestTheStatusGuardCatchesAReaderThatForgetsPaused is the guard's own proof:
// the rows below are read the way the walk reads the tree, so a rule that
// stopped matching — a renamed constant, a shape it no longer recognises —
// fails here rather than reporting a clean tree it never read.
func TestTheStatusGuardCatchesAReaderThatForgetsPaused(t *testing.T) {
	words := statusWordsInRunstore()
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "an enumeration that forgets the word",
			src:  "func isFailure(s runstore.Status) bool {\n\tswitch s {\n\tcase runstore.StatusFailed, runstore.StatusHalted:\n\t\treturn true\n\t}\n\treturn false\n}",
			want: true,
		},
		{
			name: "the same switch with the word spelled out",
			src:  "func isFailure(s runstore.Status) bool {\n\tswitch s {\n\tcase runstore.StatusFailed, runstore.StatusHalted:\n\t\treturn true\n\tcase runstore.StatusPaused:\n\t\treturn false\n\t}\n\treturn false\n}",
		},
		{
			name: "the same switch over literals, which names it too",
			src:  "func isFailure(s string) bool {\n\tswitch s {\n\tcase \"failed\", \"halted\":\n\t\treturn true\n\tcase \"paused\":\n\t\treturn false\n\t}\n\treturn false\n}",
		},
		{
			name: "an enumeration with a default arm",
			src:  "func class(s runstore.Status) string {\n\tswitch s {\n\tcase runstore.StatusFailed:\n\t\treturn \"failed\"\n\tdefault:\n\t\treturn \"\"\n\t}\n}",
		},
		{
			name: "a predicate over a row's own launch words",
			src:  "func going(s string) bool {\n\tswitch s {\n\tcase runview.StatusPending, runview.StatusLaunching, string(runstore.StatusRunning):\n\t\treturn true\n\t}\n\treturn false\n}",
		},
		{
			name: "a switch over something else that holds such a word",
			src:  "func bad(word string) bool {\n\tswitch word {\n\tcase \"left for you\", \"waiting\", \"failed\":\n\t\treturn true\n\t}\n\treturn false\n}",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := statusSwitchesInSource(t, tc.src, words)
			if got != tc.want {
				t.Errorf("the guard read the row as a switch over the statuses: %v, want %v", got, tc.want)
			}
		})
	}
}

// statusSwitchesInSource is the same read and the same verdict as the walk's,
// over one source the test wrote: the rows are parsed and never compiled, which
// is what the rule reads.
func statusSwitchesInSource(t *testing.T, src string, words []string) bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "row.go", "package row\n"+src, 0)
	if err != nil {
		t.Fatalf("parsing the row: %v", err)
	}
	for _, sw := range statusSwitchesInFile(fset, file, "row.go", words) {
		if sw.forgetsPaused() {
			return true
		}
	}
	return false
}

// statusPausedConstant is the name of the constant a switch over runstore's
// type spells paused with: an arm is read by the words it uses, and this is the
// one the rule looks for.
const statusPausedConstant = "StatusPaused"

// statusSwitch is one switch over runstore's status vocabulary as the walk
// reads it: where it is, the declaration it sits in, its arms as written, and
// the two answers the rule turns on.
type statusSwitch struct {
	file, fn string
	line     int
	// arms are the case arms, each as its constant's name or the word its
	// literal spells, so a failure says what the switch does enumerate.
	arms []string
	// hasDefault is a `default:` arm, which answers for every word the switch
	// does not name.
	hasDefault bool
	// knowsPaused is an arm naming the paused word.
	knowsPaused bool
}

func (s statusSwitch) where() string { return s.file + ":" + strconv.Itoa(s.line) }

// forgetsPaused is the verdict the rule reaches: an enumeration of the
// vocabulary that answers for a word it never names.
func (s statusSwitch) forgetsPaused() bool { return !s.hasDefault && !s.knowsPaused }

// statusSwitchesInTree walks root and reports every switch over the vocabulary.
// Test sources are walked too: a test that enumerates the statuses is a reader
// like any other, and one that skips paused is a gap in what the suite covers.
// Nothing is type-checked — a switch is read by the words its arms use — so the
// walk needs no build tag, no dependency order and no compiled package.
func statusSwitchesInTree(t *testing.T, root string) []statusSwitch {
	t.Helper()
	words := statusWordsInRunstore()
	fset := token.NewFileSet()
	var out []statusSwitch
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return perr
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			rel = p
		}
		out = append(out, statusSwitchesInFile(fset, file, rel, words)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}

// statusWordsInRunstore is runstore's status vocabulary as the wire spells it,
// read from runstore.Statuses so a word added there is matched here without a
// second spelling of the set.
func statusWordsInRunstore() []string {
	words := make([]string, len(runstore.Statuses))
	for i, s := range runstore.Statuses {
		words[i] = string(s)
	}
	return words
}

// statusSwitchesInFile reports the switches of one parsed file, each under the
// declaration it sits in: a function or method by its own name, and a
// package-level var by the name its function literal is bound to, which is how
// a reader plumbed as a var is declared here (noprompt_lint_test.go walks
// declarations the same way).
func statusSwitchesInFile(fset *token.FileSet, file *ast.File, name string, words []string) []statusSwitch {
	var out []statusSwitch
	collect := func(body *ast.BlockStmt, fn string) {
		if body == nil {
			return
		}
		ast.Inspect(body, func(n ast.Node) bool {
			if sw, ok := n.(*ast.SwitchStmt); ok {
				if s, ok := readStatusSwitch(fset, sw, name, fn, words); ok {
					out = append(out, s)
				}
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			collect(d.Body, d.Name.Name)
		case *ast.GenDecl:
			if d.Tok != token.VAR {
				continue
			}
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, v := range vs.Values {
					lit, ok := v.(*ast.FuncLit)
					if !ok {
						continue
					}
					fn := "var"
					if i < len(vs.Names) {
						fn = vs.Names[i].Name
					}
					collect(lit.Body, fn)
				}
			}
		}
	}
	return out
}

// readStatusSwitch reads one switch: ok is false unless every non-default arm
// is drawn from runstore's status vocabulary, which is what makes the switch an
// enumeration of the set rather than a predicate over a row's own words.
func readStatusSwitch(fset *token.FileSet, sw *ast.SwitchStmt, name, fn string, words []string) (statusSwitch, bool) {
	out := statusSwitch{file: name, fn: fn, line: fset.Position(sw.Pos()).Line}
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		if cc.List == nil {
			out.hasDefault = true
			continue
		}
		for _, e := range cc.List {
			word, isConstant, ok := statusArm(e, words)
			if !ok {
				return statusSwitch{}, false
			}
			out.arms = append(out.arms, word)
			switch {
			case isConstant && word == statusPausedConstant:
				out.knowsPaused = true
			case !isConstant && word == string(runstore.StatusPaused):
				out.knowsPaused = true
			}
		}
	}
	if len(out.arms) == 0 {
		return statusSwitch{}, false
	}
	return out, true
}

// statusArm resolves a case arm: a `runstore.Status…` selector, with or without
// a `string(…)` conversion around it, or a string literal spelling one of
// runstore's words. isConstant tells the two apart, and only a constant naming
// the paused word is what the rule looks for: a literal is matched against the
// vocabulary, so a switch over some other string is left alone.
func statusArm(e ast.Expr, words []string) (word string, isConstant, ok bool) {
	for {
		call, is := e.(*ast.CallExpr)
		if !is || len(call.Args) != 1 {
			break
		}
		e = call.Args[0]
	}
	switch v := e.(type) {
	case *ast.SelectorExpr:
		id, is := v.X.(*ast.Ident)
		if !is || id.Name != "runstore" || !strings.HasPrefix(v.Sel.Name, "Status") {
			return "", false, false
		}
		return v.Sel.Name, true, true
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false, false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil || !slices.Contains(words, s) {
			return "", false, false
		}
		return s, false, true
	}
	return "", false, false
}
