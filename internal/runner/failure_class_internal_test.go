package runner

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// statusSite is one statement that writes a record's Status field, as the
// guard below reads it off the parsed file.
type statusSite struct {
	file string
	line int
	pos  token.Pos
}

// recordEndFile is the one file allowed to write a record's Status. The rule
// the design states is that recordEnd is the only writer of a run's terminal
// status, outcome, reason and failure class, so an assignment anywhere else
// is a second writer whatever it intends.
const recordEndFile = "recordend.go"

// TestRecordEndIsTheOnlyWriterOfATerminalStatus is the guard over the tree
// that makes "one writer" a fact rather than an intention.
//
// It parses internal/runner rather than matching its text, because the text is
// not what the rule is about: `rec := *r.rec; rec.Status = st` in a fresh
// helper (the shape storeUnposted has) writes a status through a local record
// and reaches no `r.rec.Status` pattern at all, and so does a status carried
// in a variable or set in a runstore.Record literal. Every assignment to a
// record's Status is the same fact, so every one of them is reported by the
// file and line that makes it.
//
// storeUnposted is accounted for rather than excepted: it renders a copy of
// the record into the stored report, and the copy's status is written by
// recordend.go's own writeEnd like every other, which is why no statement
// outside that file names the field.
func TestRecordEndIsTheOnlyWriterOfATerminalStatus(t *testing.T) {
	// The reader first, on a file that is not in the tree: a guard that missed
	// a shape would pass on any tree, and the shape it must not miss is the
	// copy through a local record that storeUnposted makes.
	t.Run("the reader sees every shape", func(t *testing.T) {
		const src = `package runner

func f(rec *runstore.Record, st runstore.Status) {
	c := *rec
	c.Status = st
	rec.Status, rec.Outcome = st, runstore.OutcomeNone
	_ = runstore.Record{Status: runstore.StatusFailed}
	_ = runstore.Record{Status: runstore.StatusRunning}
}
`
		file, err := parser.ParseFile(token.NewFileSet(), "shape.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Three: the copy, the record itself and the literal. The running
		// literal is the record's construction and not an ending.
		if got := len(statusSitesIn(file)); got != 3 {
			t.Errorf("the guard read %d of the 3 statements in a file that assigns a local record's status, assigns the record's own and writes a terminal status in a literal: a guard that does not see them passes on a tree that does", got)
		}
	})

	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var outside []statusSite
	for _, path := range paths {
		// The guard is about the runner's own code: a test may set a record's
		// status to build the state it asserts on.
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s does not parse: %v", path, err)
		}
		for _, site := range statusSitesIn(file) {
			site.file, site.line = path, fset.Position(site.pos).Line
			if path != recordEndFile {
				outside = append(outside, site)
			}
		}
	}
	for _, site := range outside {
		t.Errorf("%s:%d writes a record's Status outside %s: every terminal status goes through recordEnd, so that the status, the outcome, the reason and the class describing it are written together and the last call is the only rule",
			site.file, site.line, recordEndFile)
	}
	if len(outside) > 0 {
		t.Errorf("%d statements write a record's Status outside %s: the count is the census, and one writer is the whole rule", len(outside), recordEndFile)
	}
}

// TestNoClassIsWrittenWhereTheRunDidNotFail: a run that ended succeeded
// carries no failure_class, whatever class its last writer was handed. The
// three shapes below are the three successes the record has a name for — a
// ready pull request, a review run that reviewed, an artifact run that
// delivered — and the plan's D1464 is the answer they share: there is no
// failure to name, and a class on any of them would be a classification of a
// success, which is the value the report's share of classified runs stops
// meaning anything with.
//
// Mutation (fill the class on a succeeded status in recordEnd): every shape
// below reddens, which is why the rule sits in the writer rather than at the
// three sites.
func TestNoClassIsWrittenWhereTheRunDidNotFail(t *testing.T) {
	for _, tt := range []struct {
		name    string
		outcome runstore.Outcome
	}{
		{"a ready pull request", runstore.OutcomeReady},
		{"a mode: review run that reviewed", runstore.OutcomeReviewed},
		{"an artifact run that delivered", runstore.OutcomeArtifacts},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &run{rec: &runstore.Record{}}
			r.recordEnd(runstore.StatusSucceeded, tt.outcome, failure.TestsFailed, "a reason the run did not fail for")
			if r.rec.FailureClass != "" {
				t.Errorf("a succeeded run carries the class %q: a class on a run with no failure names a cause it does not have, and the report counts it as one", r.rec.FailureClass)
			}
			// The field is absent rather than empty: a reader of an old record
			// and a writer of a new one agree by there being nothing to read.
			data, err := json.Marshal(r.rec)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "failure_class") {
				t.Errorf("a succeeded run's record.json carries failure_class: %s", data)
			}
		})
	}
}

// TestHaltedRunCarriesItsHaltsClass: a run that failed and was then halted
// ends halted with the halt's class, not the failure's. The last call wins and
// the four fields are written together, so the class always describes the
// status the record carries — which is the whole of the rule recordEnd's
// comment states, and the reason the class is not hung on the run's first
// failure reason (r.failure(), which the pull request's section reads and the
// record no longer holds).
func TestHaltedRunCarriesItsHaltsClass(t *testing.T) {
	r := &run{rec: &runstore.Record{}}
	r.recordEnd(runstore.StatusFailed, runstore.OutcomeDraft, failure.TestsFailed, "tests failing on the final commit")
	halt := &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run"}
	r.rec.Halt = halt
	r.recordEnd(runstore.StatusHalted, runstore.OutcomeDraft, haltClass(halt.Reason), "the per-run dollar cap was reached")
	if r.rec.Status != runstore.StatusHalted {
		t.Errorf("the record's status is %q, want halted: the halt's call is the last one", r.rec.Status)
	}
	if r.rec.FailureClass != failure.HaltedBudget {
		t.Errorf("the record's class is %q, want %q: the class describes the status the record carries, and a class left over from the failure before the halt describes a sentence the record no longer holds", r.rec.FailureClass, failure.HaltedBudget)
	}
}

// statusSitesIn is every statement of one parsed file that writes a Status
// field: an assignment to a selector named Status, and a Status entry of a
// runstore.Record literal. Both are how a record's status is set, and a guard
// that knows only the first would leave the second free.
func statusSitesIn(file *ast.File) []statusSite {
	var out []statusSite
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Status" {
					out = append(out, statusSite{pos: sel.Sel.Pos()})
				}
			}
		case *ast.CompositeLit:
			if !isRecordType(node.Type) {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Status" && !isRunningStatus(kv.Value) {
					out = append(out, statusSite{pos: key.Pos()})
				}
			}
		}
		return true
	})
	return out
}

// isRecordType reports whether a composite literal's type spells
// runstore.Record, the record whose Status this guard is about.
func isRecordType(expr ast.Expr) bool {
	return isSelector(expr, "runstore", "Record")
}

// isRunningStatus reports whether a literal's Status value is `running`: the
// status a record is built with (runner.go's Run), which says no ending has
// happened yet rather than deciding one. A literal is the one place a record
// exists before any writer has seen it, so `running` there is not the fact
// this guard is about; every other status a literal carries is an ending.
func isRunningStatus(expr ast.Expr) bool {
	return isSelector(expr, "runstore", "StatusRunning")
}

// isSelector reports whether expr is the qualified name pkg.name.
func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}
