package runner

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
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
// The field is not the only way in, so the guard does not stop at it: writeEnd
// is the pair of assignments recordEnd is, and a second writer could as easily
// be a helper in another file calling writeEnd(r.rec, …). That call is the same
// fact through one more hop, so it is reported like an assignment — which is
// what makes "one writer" a statement about recordEnd and not about the syntax
// it happens to use.
//
// storeUnposted is accounted for rather than excepted: it renders a copy of
// the record into the stored report, and the copy is ended by recordend.go's
// own reportCopy, so no call to writeEnd and no statement naming the field
// sits outside that file at all.
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
	writeEnd(rec, runstore.StatusFailed, runstore.OutcomeNone, "", "")
}
`
		file, err := parser.ParseFile(token.NewFileSet(), "shape.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Four: the copy, the record itself, the literal and the call to the
		// writer. The running literal is the record's construction and not an
		// ending.
		if got := len(statusSitesIn(file)); got != 4 {
			t.Errorf("the guard read %d of the 4 statements in a file that assigns a local record's status, assigns the record's own, writes a terminal status in a literal and calls writeEnd: a guard that does not see them passes on a tree that does", got)
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
//
// Every reason of the closed set is walked, not the one that happens to be
// first: the class is durable (the report counts runs by it and nothing
// rewrites it), so a reason left on the wrong arm is a figure a reader acts on
// with the wrong remedy, and nothing else in the tree reads this switch.
func TestHaltedRunCarriesItsHaltsClass(t *testing.T) {
	// The family per reason, from the design's §3 rows: the dollar and token
	// caps are halted_budget; halted_credential is its own definition, "no
	// credential became usable, or the budget database refused", which is what
	// budget_unavailable (the backend never answered) and budget_token_expired
	// (the run waited past its own budget token) are — their remedy is an
	// account that answers again, never a cap raised; the governance tripwires
	// are halted_governance, the slice the checkpoint schema is pinned to; and
	// the project's switch is halted_kill_switch.
	families := map[runstore.HaltReason]failure.Class{
		runstore.HaltKillSwitch:         failure.HaltedKillSwitch,
		runstore.HaltRunCap:             failure.HaltedBudget,
		runstore.HaltRepoDailyCap:       failure.HaltedBudget,
		runstore.HaltGlobalDailyCap:     failure.HaltedBudget,
		runstore.HaltNoCap:              failure.HaltedBudget,
		runstore.HaltTokenCap:           failure.HaltedBudget,
		runstore.HaltBudgetUnavailable:  failure.HaltedCredential,
		runstore.HaltBudgetTokenExpired: failure.HaltedCredential,
		runstore.HaltNoCredential:       failure.HaltedCredential,
		runstore.HaltCredsRulesDenied:   failure.HaltedCredential,
		runstore.HaltStageBudget:        failure.HaltedGovernance,
		runstore.HaltReviewRatio:        failure.HaltedGovernance,
		runstore.HaltFanout:             failure.HaltedGovernance,
		runstore.HaltReviewScope:        failure.HaltedGovernance,
		runstore.HaltExpectedExceeded:   failure.HaltedGovernance,
	}

	reasons := haltReasonConstants(t)
	if got := len(reasons); got != len(families) {
		t.Errorf("runstore declares %d halt reasons and this walk places %d: a reason with no family is classed by nothing, and the count is how the two are held together", got, len(families))
	}
	for _, reason := range reasons {
		class, ok := families[reason]
		if !ok {
			t.Errorf("runstore's %q has no family here: a new reason is a class a report prints and a reader acts on, so it is placed deliberately and not left to the switch's default", reason)
			continue
		}
		t.Run(string(reason), func(t *testing.T) {
			if got := haltClass(reason); got != class {
				t.Errorf("haltClass(%q) is %q, want %q: the reason is a name, the class is the cause's family and the remedy the report prints for it", reason, got, class)
			}
			r := &run{rec: &runstore.Record{}}
			r.recordEnd(runstore.StatusFailed, runstore.OutcomeDraft, failure.TestsFailed, "tests failing on the final commit")
			r.rec.Halt = &runstore.Halt{Reason: reason, Scope: "run"}
			r.recordEnd(runstore.StatusHalted, runstore.OutcomeDraft, haltClass(reason), "a halt the runner recorded")
			if r.rec.Status != runstore.StatusHalted {
				t.Errorf("the record's status is %q, want halted: the halt's call is the last one", r.rec.Status)
			}
			if r.rec.FailureClass != class {
				t.Errorf("the record's class is %q, want %q: the class describes the status the record carries, and a class left over from the failure before the halt describes a sentence the record no longer holds", r.rec.FailureClass, class)
			}
		})
	}
	for reason := range families {
		if !slices.Contains(reasons, reason) {
			t.Errorf("this walk places a family for %q, which runstore no longer declares: a family for a reason no run can hold is a rule about a value that does not exist", reason)
		}
	}
}

// haltReasonConstants is the closed set of halt reasons, read from runstore's
// own source in the order it declares them: every constant of a block whose
// type is HaltReason, whether the spec spells the type or inherits it from the
// spec before it — which is Go's own rule for a const block, and the reason a
// reason added as `HaltProbe = "probe"` under a typed one is still walked here
// rather than passing unclassed.
//
// There is no slice to range over — GovernanceHaltReasons is the governance
// arm alone — so the source is the only complete list, and reading it is what
// makes the walk above cover a reason added later rather than the fifteen of
// today. A reason declared in a const block of its own, with no HaltReason
// type anywhere in it, is the one shape this cannot see: nothing in the source
// says the value is a halt reason at all.
func haltReasonConstants(t *testing.T) []runstore.HaltReason {
	t.Helper()
	const src = "../runstore/runstore.go"
	file, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
	if err != nil {
		t.Fatalf("%s does not parse: %v", src, err)
	}
	var out []runstore.HaltReason
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		// The block's type, carried the way the compiler carries it: a spec
		// that spells none repeats the last one.
		var blockType ast.Expr
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				blockType = vs.Type
			}
			if ident, ok := blockType.(*ast.Ident); !ok || ident.Name != "HaltReason" {
				continue
			}
			// A spec with no values repeats the one before it, so its value is
			// already in out.
			if len(vs.Values) == 0 {
				continue
			}
			if len(vs.Values) != len(vs.Names) {
				t.Fatalf("%s: a HaltReason spec names %d constants and gives %d values", src, len(vs.Names), len(vs.Values))
			}
			for _, value := range vs.Values {
				lit, ok := value.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: a HaltReason constant without a string value", src)
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %v", src, err)
				}
				out = append(out, runstore.HaltReason(unquoted))
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no HaltReason constants found in %s: the walk above would pass against a tree with no reasons in it", src)
	}
	return out
}

// statusSitesIn is every statement of one parsed file that writes a Status
// field, or reaches the writer that writes it: an assignment to a selector
// named Status, a Status entry of a runstore.Record literal, and a call to
// writeEnd. Each is how a record's status is set, and a guard that knows only
// the first would leave the others free.
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
		case *ast.CallExpr:
			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "writeEnd" {
				out = append(out, statusSite{pos: ident.Pos()})
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
