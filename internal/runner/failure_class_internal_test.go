package runner

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// statusSite is one statement that writes a record's ending, or one reference
// to the writer that writes it, as the guard below reads it off the parsed
// file. what says which of the two, in the words the failure is reported in.
type statusSite struct {
	file string
	line int
	pos  token.Pos
	what string
}

// recordEndFile is the one file allowed to write a record's ending. The rule
// the design states is that recordEnd is the only writer of a run's terminal
// status, outcome, reason and failure class, so a statement that writes any one
// of them, or a reference to the writer, anywhere else is a second writer
// whatever it intends.
const recordEndFile = "recordend.go"

// endingField reports whether a name is one of the four fields recordEnd writes
// together. The four are one fact — the class describes the status the record
// carries — so a guard over the status alone would leave the class, which is
// durable and which FC4 fills from the agent's own message, free to be written
// anywhere: `r.rec.FailureClass = …` after an ending would say a run failed a
// way its ending does not carry, and nothing reads the record again to see it.
func endingField(name string) bool {
	switch name {
	case "Status", "Outcome", "Reason", "FailureClass":
		return true
	}
	return false
}

// TestRecordEndIsTheOnlyWriterOfATerminalStatus is the guard over the tree
// that makes "one writer" a fact rather than an intention.
//
// It parses internal/runner rather than matching its text, because the text is
// not what the rule is about: `rec := *r.rec; rec.Status = st` in a fresh
// helper (the shape storeUnposted has) writes a status through a local record
// and reaches no `r.rec.Status` pattern at all, and so does a status carried
// in a variable or set in a runstore.Record literal. Every assignment to a
// record's ending is the same fact, so every one of them is reported by the
// file and line that makes it.
//
// The four fields are one fact and the guard reads all four: recordEnd writes
// the status, the outcome, the reason and the class together, so a statement
// that writes the class alone is as much a second writer as one that writes the
// status alone — and the class is the one a report counts runs by, which FC4
// will fill from the agent's own message.
//
// The field is not the only way in, so the guard does not stop at it either:
// writeEnd is the pair of assignments recordEnd is, and a second writer could
// as easily be a helper in another file calling writeEnd(r.rec, …), or taking
// it as a value (`w := writeEnd; w(r.rec, …)`, which is the same call one hop
// further from its name). Every reference to a writer is therefore reported,
// not only a call — which is what makes "one writer" a statement about
// recordEnd and not about the syntax a site happens to use.
//
// storeUnposted is accounted for rather than excepted: it renders a copy of
// the record into the stored report, and the copy is ended by recordend.go's
// own reportCopy, so no reference to writeEnd and no statement naming a field
// of the ending sits outside that file at all.
func TestRecordEndIsTheOnlyWriterOfATerminalStatus(t *testing.T) {
	// The reader first, on a file that is not in the tree: a guard that missed
	// a shape would pass on any tree, and the shapes it must not miss are the
	// copy through a local record that storeUnposted makes, the class written
	// on its own, and the writer reached through a variable.
	t.Run("the reader sees every shape", func(t *testing.T) {
		const src = `package runner

func f(r *run, rec *runstore.Record, st runstore.Status) {
	c := *rec
	c.Status = st
	c.FailureClass = failure.InfraError
	rec.Status, rec.Outcome = st, runstore.OutcomeNone
	_ = runstore.Record{Status: runstore.StatusFailed, Reason: "a reason"}
	_ = runstore.Record{Status: runstore.StatusRunning, Outcome: runstore.OutcomeNone}
	writeEnd(rec, runstore.StatusFailed, runstore.OutcomeNone, "", "")
	w := writeEnd
	g := r.recordEnd
}
`
		file, err := parser.ParseFile(token.NewFileSet(), "shape.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Nine: the copy's status and its class, the record's own status and
		// outcome, the failed literal's status and reason, the call to the
		// writer, the writer taken as a value and the method taken as a value.
		// The running literal is the record's construction — it too carries an
		// outcome, and neither is an ending.
		if got := len(statusSitesIn(file)); got != 9 {
			t.Errorf("the guard read %d of the 9 statements and references in a file that writes a copy's status and class, the record's own status and outcome, a literal's status and reason, calls writeEnd and takes writeEnd and recordEnd as values: a guard that does not see them passes on a tree that does", got)
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
		t.Errorf("%s:%d %s outside %s: every ending goes through recordEnd, so that the status, the outcome, the reason and the class describing it are written together and the last call is the only rule",
			site.file, site.line, site.what, recordEndFile)
	}
	if len(outside) > 0 {
		t.Errorf("%d statements write a record's ending, or reach its writer, outside %s: the count is the census, and one writer is the whole rule", len(outside), recordEndFile)
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

// TestNoFailureEndingCallsRecordEndWithoutAClass: a run that did not succeed
// is handed a class by the same decision that hands it its status, so the
// report can group it and a reader acts on a family and its remedy. An empty
// class is not `unclassified` — a record with no class at all is one the
// reader has to derive from the reason, which is what the records written
// before the field shipped need and what a new one must never need, because
// FC5's reader and this writer would then be answering the same question two
// ways.
//
// The guard is over the call site and not over the writer, because the writer
// already covers the other direction: writeEnd clears whatever class it is
// handed when the status is succeeded
// (TestNoClassIsWrittenWhereTheRunDidNotFail), so a failure ending can carry
// no class only if the site passes the empty literal itself. Two of the three
// mutations this guard was written for are exactly that — finalize's failed
// case, which passes failure.Unclassified, and endHalted, which passes
// haltClass(h.Reason) — and both redden here by the file and line that writes
// the ending. The third, endRefused, passes a class that is not empty and is
// the next test's.
//
// The design's §10 names this pin TestUnclassifiedIsWrittenWhereNoClassIsChosen,
// which cannot be written as named: it would assert a class on the records §3
// leaves it off (F35), and the half of it that is true — an unmatched reason
// reading `unclassified` — is the reader's, FC5's
// TestLegacyUnmatchedReasonIsUnclassified. This is the half that is the
// writer's: the class is there wherever the run failed.
func TestNoFailureEndingCallsRecordEndWithoutAClass(t *testing.T) {
	// The reader first, on a file that is not in the tree: a guard that read
	// the calls but applied the rule to the wrong argument, or that skipped
	// the calls of one shape, would pass on any tree.
	t.Run("the reader flags the classless shapes and no others", func(t *testing.T) {
		const src = `package runner

func f(rec *runstore.Record, h *runstore.Halt) {
	r.recordEnd(runstore.StatusFailed, runstore.OutcomeNone, "", "a reason")
	r.recordEnd(runstore.StatusHalted, runstore.OutcomeNone, "", "a reason")
	r.recordEnd(runstore.StatusSucceeded, runstore.OutcomeNone, "", "a reason")
	r.recordEnd(runstore.StatusFailed, runstore.OutcomeNone, haltClass(h.Reason), "a reason")
	writeEnd(rec, runstore.StatusCancelled, runstore.OutcomeNone, "", "a reason")
	reportCopy(rec, runstore.StatusInfraError, runstore.OutcomeNone, failure.InfraError, "a reason")
}
`
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "shape.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		calls := endingCallsIn(file, "shape.go", fset)
		if len(calls) != 6 {
			t.Fatalf("the walk read %d of the 6 endings of a file that ends a run six ways: a walk that misses calls says nothing about the ones that are wrong", len(calls))
		}
		var classless []endingCall
		for _, call := range calls {
			if classlessEnding(call) {
				classless = append(classless, call)
			}
		}
		// Three: the failed, the halted and the cancelled ending. The
		// succeeded one with an empty class is the shape the writer clears and
		// this guard must let through.
		if len(classless) != 3 {
			t.Errorf("the guard read %d of the 3 classless failure endings above (the failed, the halted and the cancelled one): a guard that reads none of them, or reads the succeeded one, is not the rule this test states", len(classless))
		}
	})

	for _, call := range endingCalls(t) {
		if !classlessEnding(call) {
			continue
		}
		t.Errorf("%s:%d (%s) ends the run %s with an empty class: the class is handed over by the same decision that hands over the status, so a failure names the family it is in — haltClass for a halt, and failure.Unclassified where the class is a later task's to choose, which is not the same as none at all", call.file, call.line, call.fn, call.status)
	}
}

// TestRecordEndSitesKeepTheClassTheyChose: the two endings whose class their
// status does not name still pass the class this task chose for them. A
// refused push ends infra_error — the status says which machinery stopped the
// run — with branch_moved, because the class says which family the cause is
// in, and the design's §3 names this call as that class's case; endUnchanged's
// fallback ends failed with branch_moved, because every reason it is called
// with is the branch or the pull request moving under the run (F34).
//
// The guard above cannot see either of them: a mutation from branch_moved to
// infra_error, or to unclassified, leaves a class in place, and both values
// are written elsewhere in the package, so only the site's own name says which
// one is right — and exactly that mutation was carried by an earlier revision
// of this branch, at endRefused, until a review of this pull request found it.
// The table stops at these two sites on purpose: every other class belongs to
// the task that chooses it (FC3's refusals, FC4's reading of the agent's own
// message), and a pin here would decide those classes in advance.
func TestRecordEndSitesKeepTheClassTheyChose(t *testing.T) {
	// The class is the vocabulary's constant for the family, written as the
	// site writes it: failure.BranchMoved, whose value is branch_moved, the
	// design's §3 row for the branch or the pull request moving under the run.
	want := []struct {
		fn     string
		status string
		class  string
	}{
		{"endRefused", "runstore.StatusInfraError", "failure.BranchMoved"},
		{"endUnchanged", "runstore.StatusFailed", "failure.BranchMoved"},
	}

	calls := endingCalls(t)
	for _, w := range want {
		matched := false
		for _, call := range calls {
			if call.fn != w.fn || call.status != w.status {
				continue
			}
			matched = true
			if call.class != w.class {
				t.Errorf("%s:%d (%s) ends %s with the class %s, want %s: the class is the cause's family and the remedy the report prints, and at this site it is not the name of the status beside it", call.file, call.line, call.fn, call.status, call.class, w.class)
			}
		}
		if !matched {
			t.Errorf("no ending of %s passes %s: the site this pin is about is gone or writes another status, and a pin that matches nothing holds nothing — the site and this table are corrected together", w.fn, w.status)
		}
	}
}

// TestRecordEndHaltAndCancelClasses: the two endings whose class the site does
// not choose — a halt reads its family from the halt's own reason, and a
// cancellation is `cancelled` and no other.
//
// A halt's family is the reason's: the seven endings that end a run halted all
// hand haltClass the reason the halt carries, and a site that passed a family
// constant instead would file every halt under one class while the record's own
// halt said another. A cancellation is its own family: the seven endings that
// end a run cancelled pass `cancelled`, and `infra_error` — the class beside it
// in the same record — would say the machinery stopped the run rather than a
// person, which is what endCancelled's own comment exists to tell apart. Both
// classes are durable — nothing rewrites one, and FC5's report and FC7's digest
// count runs by them — and neither guard above holds them: the class is not
// empty, so the classless guard lets it through, and
// TestRecordEndSitesKeepTheClassTheyChose names two other endings.
//
// The rule is a shape over the site, not a row per site, so a halt or a cancel
// added later is held by it too. Every other class is left alone on purpose:
// which family a failed or an infra_error ending is in belongs to the task that
// reads the cause — FC3's refusals and FC4's reading of the agent's own message
// — so those pass failure.Unclassified here, and a pin over them would decide
// those tasks in advance.
func TestRecordEndHaltAndCancelClasses(t *testing.T) {
	// The reader first, on a file that is not in the tree: the two shapes the
	// rule is about, the two ways a site chooses a class of its own for them,
	// and the endings the rule must not touch.
	t.Run("the reader flags the halts and cancels that choose their own class", func(t *testing.T) {
		const src = `package runner

func f(rec *runstore.Record, h *runstore.Halt) {
	r.recordEnd(runstore.StatusHalted, runstore.OutcomeNone, haltClass(h.Reason), "a reason")
	r.recordEnd(runstore.StatusCancelled, runstore.OutcomeNone, failure.Cancelled, "a reason")
	r.recordEnd(runstore.StatusHalted, runstore.OutcomeNone, failure.HaltedBudget, "a reason")
	r.recordEnd(runstore.StatusCancelled, runstore.OutcomeNone, failure.InfraError, "a reason")
	r.recordEnd(runstore.StatusHalted, runstore.OutcomeNone, haltClass(runstore.HaltRunCap), "a reason")
	r.recordEnd(runstore.StatusFailed, runstore.OutcomeNone, failure.Unclassified, "a reason")
	r.recordEnd(runstore.StatusInfraError, runstore.OutcomeNone, failure.BranchMoved, "a reason")
	r.recordEnd(runstore.StatusSucceeded, runstore.OutcomeNone, "", "a reason")
}
`
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "shape.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		calls := endingCallsIn(file, "shape.go", fset)
		if len(calls) != 8 {
			t.Fatalf("the walk read %d of the 8 endings of a file that ends a run eight ways: a walk that misses calls says nothing about the ones that choose a class of their own", len(calls))
		}
		var flagged []int
		for _, call := range calls {
			if choosesItsOwnClass(call) {
				flagged = append(flagged, call.line)
			}
		}
		// The three: a halt filed under one family, a cancel filed as an infra
		// error, and a haltClass handed a reason the halt does not carry. The
		// two above them are the shapes the rule lets through, and the three
		// below are endings of other statuses, whose class is not this rule's.
		if want := []int{6, 7, 8}; !slices.Equal(flagged, want) {
			t.Errorf("the reader flagged line(s) %v of a file whose halt and cancel endings choose a class at line(s) %v: a rule over the wrong argument, or over every ending, is not the rule this test states", flagged, want)
		}
	})

	halted, cancelled := 0, 0
	for _, call := range endingCalls(t) {
		switch call.status {
		case "runstore.StatusHalted":
			halted++
		case "runstore.StatusCancelled":
			cancelled++
		}
		if choosesItsOwnClass(call) {
			t.Errorf("%s:%d (%s) ends the run %s with the class %s: a halt's family is read from the halt's own reason (haltClass) and a cancellation is failure.Cancelled and no other — the class is durable, so a site that chooses one of its own files the run under a family its own record contradicts", call.file, call.line, call.fn, call.status, call.class)
		}
	}
	// The floor is what the tree has today (7 halts, 7 cancels): a walk that
	// reads a handful of either would hold nothing about the ones it missed.
	if halted < 7 || cancelled < 7 {
		t.Errorf("the tree ends a run halted 7 times and cancelled 7, and this walk read %d and %d: a walk that misses endings says nothing about the classes they choose", halted, cancelled)
	}
}

// choosesItsOwnClass reports whether a site picked the class for an ending
// whose class is not the site's to pick: a halt's family comes from the halt's
// own reason, and a cancellation is `cancelled` and nothing else. Every other
// status passes, so the rule leaves the classes FC3 and FC4 decide alone.
func choosesItsOwnClass(call endingCall) bool {
	switch call.status {
	case "runstore.StatusHalted":
		return !strings.HasPrefix(call.class, "haltClass(") || !strings.HasSuffix(call.class, ".Reason)")
	case "runstore.StatusCancelled":
		return call.class != "failure.Cancelled"
	}
	return false
}

// endingCall is one call to a writer, read off the parsed file: the function
// it sits in, and the status and class arguments it is handed as the source
// writes them. It is the fact a site ends a run with, and no assignment is
// one — which is why both guards above read calls and not fields.
type endingCall struct {
	file, fn string
	line     int
	status   string
	class    string
}

// classlessEnding reports whether a call ends a run that did not succeed with
// no class at all: the one shape
// TestNoFailureEndingCallsRecordEndWithoutAClass is about. A succeeded ending
// with an empty class is the writer's own rule (writeEnd), not a site's
// mistake, and a call that passes anything else — a class, a call to
// haltClass — is a site choosing one.
func classlessEnding(call endingCall) bool {
	return call.class == `""` && call.status != "runstore.StatusSucceeded"
}

// endingCalls is every call to a writer in the package's own code: a test may
// end a record to build the state it asserts on, and recordend.go's own two
// calls are the writer reaching itself.
func endingCalls(t *testing.T) []endingCall {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []endingCall
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s does not parse: %v", path, err)
		}
		out = append(out, endingCallsIn(file, path, fset)...)
	}
	// The floor is the guard's own reader check: the package ends a run from
	// every stage, so a walk that reads a handful is not reading the tree and
	// its silence would mean nothing.
	if len(out) < 20 {
		t.Fatalf("the walk read %d endings in internal/runner: the package ends a run from dozens of sites, so the guards above would pass against a tree they never looked at", len(out))
	}
	return out
}

// endingCallsIn is every call to a writer in one parsed file, each with the
// function it sits in.
func endingCallsIn(file *ast.File, path string, fset *token.FileSet) []endingCall {
	var out []endingCall
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			default:
				return true
			}
			status, class, ok := writerArgs(name)
			if !ok || len(call.Args) <= class {
				return true
			}
			out = append(out, endingCall{
				file: path, fn: fn.Name.Name, line: fset.Position(call.Pos()).Line,
				status: argText(fset, call.Args[status]), class: argText(fset, call.Args[class]),
			})
			return true
		})
	}
	return out
}

// writerArgs is where a call to one of the writers keeps its status and its
// class, and whether the call is one at all: recordEnd is a method, so the
// record is its receiver and its arguments start at the status, while writeEnd
// and reportCopy take the record first and reach the status after it.
func writerArgs(name string) (status, class int, ok bool) {
	switch name {
	case "recordEnd":
		return 0, 2, true
	case "writeEnd", "reportCopy":
		return 1, 3, true
	}
	return 0, 0, false
}

// argText is an argument as the source writes it: `runstore.StatusHalted`,
// `haltClass(h.Reason)`, `""`. The guards above compare what a site hands the
// writer, which is a value's name and not a field write, so the written form
// is the fact — there is no second spelling of a class argument to catch.
func argText(fset *token.FileSet, expr ast.Expr) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, expr); err != nil {
		return "?"
	}
	return b.String()
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
// with the wrong remedy, and nothing else in the tree reads this switch. The
// families themselves, and the walk's own coverage of the closed set, are
// haltFamilies' and TestHaltReasonsMapToTheirFamily's: this test reads the one
// table and holds what a halt leaves on the record.
func TestHaltedRunCarriesItsHaltsClass(t *testing.T) {
	for reason, class := range haltFamilies() {
		t.Run(string(reason), func(t *testing.T) {
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
// makes the walk above cover a reason added later rather than the sixteen of
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

// statusSitesIn is every statement of one parsed file that writes a record's
// ending, or reaches the writer that writes it: an assignment to a selector
// named for one of the four fields, such a field as a key of a runstore.Record
// literal, a reference to writeEnd, and a reference to recordEnd that is not
// the call itself. Each is how a record's ending is set, and a guard that knows
// only the first would leave the others free.
func statusSitesIn(file *ast.File) []statusSite {
	var out []statusSite
	// The call to recordEnd is the site; the method taken as a value is the
	// same ending one hop away, so the calls are collected first and every
	// other reference to the name is reported.
	called := map[token.Pos]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			called[call.Fun.Pos()] = true
		}
		return true
	})
	// The declaration of a writer is its name once, and not a site.
	declared := map[token.Pos]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			declared[node.Name.Pos()] = true
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && endingField(sel.Sel.Name) {
					out = append(out, statusSite{pos: sel.Sel.Pos(), what: "writes a record's " + sel.Sel.Name})
				}
			}
		case *ast.CompositeLit:
			if !isRecordType(node.Type) || isRecordConstruction(node) {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && endingField(key.Name) {
					out = append(out, statusSite{pos: key.Pos(), what: "writes a record's " + key.Name})
				}
			}
		case *ast.Ident:
			if node.Name == "writeEnd" && !declared[node.Pos()] {
				out = append(out, statusSite{pos: node.Pos(), what: "reaches writeEnd"})
			}
		case *ast.SelectorExpr:
			if node.Sel.Name == "recordEnd" && !called[node.Pos()] {
				out = append(out, statusSite{pos: node.Pos(), what: "reaches recordEnd"})
			}
		}
		return true
	})
	return out
}

// isRecordConstruction reports whether a runstore.Record literal is the record
// being built — the one whose Status is `running` (runner.go's Run). A literal
// is the one place a record exists before any writer has seen it, so the fields
// it carries there say what the run started as and not how it ended; every
// other record literal is an ending written by hand.
func isRecordConstruction(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Status" {
			return isRunningStatus(kv.Value)
		}
	}
	return false
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
