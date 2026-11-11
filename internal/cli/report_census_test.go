package cli

// report_census_test.go is CL9: the pin that says which commands print through
// the reporter and which do not (D1354).
//
// The migration moved the mutating commands onto the reporter one family at a
// time (CL5a, CL5b, CL6, CL7a-c), and a migration nobody pins is a migration
// that stops halfway and says so nowhere: the next command added to the tree
// would print its own account by hand, and no test would notice. So the tree is
// walked, every visible runnable command has to be accounted for in
// report_exempt.go -- on the reporter, or exempt with a reason -- and the
// reporter's half is held to the code: not to a file that contains the words,
// which prose satisfies, but to the syntax tree. A constructor has to call
// openReporter, openReporter has to call report.OpenLog, the call site has to
// pass the log name the census claims, and a name built at run time has to be
// one the builder returns or the door spec writes down.
//
// The reasons are printed on every run of the pin, the way CL10's are: an
// exemption a reviewer cannot read is a hole nobody sees.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/report"
)

// TestEveryCommandIsOnTheReporterOrExempt walks the tree and holds every
// command a person can type to the census: on the reporter, or exempt with a
// reason that says what it prints instead.
//
// The walk is the tree's rather than a list's, which is the whole point: a
// command added later is in the walk the day it is registered, and a command
// that prints an account of its own is a failure here until someone either
// moves it onto the reporter or writes down why it is not one. Hidden commands
// and the parents that run nothing are left out, because a command nobody can
// type prints nothing a person reads.
func TestEveryCommandIsOnTheReporterOrExempt(t *testing.T) {
	tree := censusCommands(t)
	for _, path := range slices.Sorted(maps.Keys(tree)) {
		sets := 0
		for _, in := range []bool{
			hasKey(reporterCommands, path),
			hasKey(exemptListings, path),
			hasKey(exemptAnswers, path),
		} {
			if in {
				sets++
			}
		}
		switch sets {
		case 1:
		case 0:
			t.Errorf("`fugaro %s` is on neither the reporter nor exempt: a command that prints anything has to say which it is. Print its run through the reporter and add it to reporterCommands, or add it to exemptListings for a reading, or to exemptAnswers with the reason it prints no account of a run", path)
		default:
			t.Errorf("`fugaro %s` is in more than one of reporterCommands, exemptListings and exemptAnswers: the three are exclusive, and two entries for one command are two answers to one question", path)
		}
	}

	// A stale entry is a hole nobody reads: it excuses a command the tree no
	// longer has, and the command it was written for is unexcused.
	for _, set := range []struct {
		what  string
		paths []string
	}{
		{"reporterCommands", slices.Sorted(maps.Keys(reporterCommands))},
		{"exemptListings", slices.Sorted(maps.Keys(exemptListings))},
		{"exemptAnswers", slices.Sorted(maps.Keys(exemptAnswers))},
	} {
		for _, path := range set.paths {
			if _, ok := tree[path]; !ok {
				t.Errorf("%s names `fugaro %s`, which the tree does not have as a visible runnable command: a renamed or deleted command leaves a stale entry, and a hidden one is not a command a person reads", set.what, path)
			}
		}
	}

	// The reason is the deliverable: an exemption of six words is a shrug.
	for _, path := range slices.Sorted(maps.Keys(exemptListings)) {
		if n := len(strings.Fields(exemptListings[path].why)); n < 8 {
			t.Errorf("exemptListings' reason for `fugaro %s` is %d words (%q), and it has to say what the command prints instead of an account of a run", path, n, exemptListings[path].why)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(exemptAnswers)) {
		if n := len(strings.Fields(exemptAnswers[path])); n < 8 {
			t.Errorf("exemptAnswers' reason for `fugaro %s` is %d words (%q), and it has to say what the command prints instead of an account of a run", path, n, exemptAnswers[path])
		}
	}

	// The list a reviewer reads, on every run.
	for _, path := range slices.Sorted(maps.Keys(exemptAnswers)) {
		fmt.Printf("`fugaro %s`, off the reporter: %s\n", path, exemptAnswers[path])
	}
}

// TestEveryMutatingCommandWritesALog holds the reporter's half of the census to
// the code, from both sides.
//
// A command on the reporter is a command whose run opens a log (D1351), and the
// log is opened in one place: report.OpenLog, called by openReporter, which the
// constructors call. Every step of that is read off the syntax tree rather than
// off the text of a file -- a definition is not a call, and a word in a comment
// is not a name -- so the census is held to what the code does: the call sites
// must pass the name the census claims, a name built at run time must be one the
// builder returns or the door spec writes down, the name must be the command's
// own, and report.OpenLog must accept it -- the real rule, run for real, rather
// than a copy of its pattern.
func TestEveryMutatingCommandWritesALog(t *testing.T) {
	src := parseCensusSource(t)
	calls := reporterConstructorCalls(t, src)
	if len(calls.literal) == 0 {
		t.Fatal("no reporter constructor is called with a log name anywhere in this package, so a pass here would prove nothing")
	}

	// The chain the census rests on: a constructor calls openReporter,
	// stepRunFor calls a constructor, openReporter calls report.OpenLog, and
	// nothing else in the package opens a log.
	for _, ctor := range []string{"reporterFor", "convergeReporter"} {
		file, body := src.funcBody(t, ctor)
		switch {
		case body == nil:
			t.Errorf("%s is not defined in this package, so a command built on it reaches no log at all", ctor)
		case len(callsTo(body, "openReporter")) == 0:
			t.Errorf("%s does not call openReporter in %s: a command built on it reaches no log at all", ctor, file)
		}
	}
	if _, body := src.funcBody(t, "stepRunFor"); body == nil || len(callsTo(body, "reporterFor")) == 0 {
		t.Error("stepRunFor does not call reporterFor, so the step family no longer reaches a log through the one path the rest of the census is checked against")
	}
	logFile, logBody := src.funcBody(t, "openReporter")
	if logBody == nil || len(callsTo(logBody, "report.OpenLog")) == 0 {
		t.Errorf("openReporter does not call report.OpenLog in %s: the constructors' logs are not report's", logFile)
	}
	src.eachFunc(func(file, name string, body *ast.BlockStmt) {
		if name == "openReporter" {
			return
		}
		if len(callsTo(body, "report.OpenLog")) > 0 {
			t.Errorf("%s opens a log directly in %s, and openReporter in report_flags.go is the one place a run's log is opened", name, file)
		}
	})

	// The door's name reaches doorName through the field the converge is
	// given: a door spec nothing assigns is a name doorName never returns,
	// and a doorName that reads another field is a name no door wrote.
	if _, body := src.funcBody(t, "doorName"); body == nil || !readsField(body, "door") {
		t.Error("doorName does not read the door's own name field, so the door specs builtLogNames names are not what it returns")
	}
	if !src.assignsFieldFromField("door", "name") {
		t.Error("nothing assigns a door's name field from the door spec's own name, so no door's name ever reaches doorName")
	}

	isolateState(t)
	for _, path := range slices.Sorted(maps.Keys(reporterCommands)) {
		name := reporterCommands[path]
		if want := strings.ReplaceAll(path, " ", "-"); name != want {
			t.Errorf("`fugaro %s` files its log under %q, want %q: D1351 files a run under the name a person types", path, name, want)
		}
		sink, err := report.OpenLog(name, os.Getenv, runtime.GOOS, time.Now, nil)
		if err != nil {
			t.Errorf("`fugaro %s` files its log under %q, which report.OpenLog refuses: %v", path, name, err)
			continue
		}
		if got := sink.Path(); got != "" && !strings.Contains(filepath.Base(got), name+"-") {
			t.Errorf("`fugaro %s`'s log is %q, which is not named after the command", path, got)
		}
		_ = sink.Close()
	}

	// Both directions: a name the source builds a reporter with is an entry,
	// and an entry is a name the source builds a reporter with -- written at
	// the call site, or built by the function builtLogNames names, whose name
	// the source then has to write down where the census says it does.
	for _, name := range slices.Sorted(maps.Keys(calls.literal)) {
		if _, ok := reporterPath(name); !ok {
			t.Errorf("a reporter is built at %s under the log name %q, which is not an entry in reporterCommands: a command that prints through the reporter has to be on the census", calls.literal[name], name)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(reporterCommands)) {
		name := reporterCommands[path]
		if _, ok := calls.literal[name]; ok {
			continue
		}
		built, ok := builtLogNames[path]
		if !ok {
			t.Errorf("no reporter constructor in this package is called with %q, and builtLogNames does not say where `fugaro %s` builds it: the entry is held to nothing", name, path)
			continue
		}
		if _, called := calls.built[built.builder]; !called {
			t.Errorf("builtLogNames says %s builds `fugaro %s`'s log name, and no reporter constructor is called with %s(...): the census names a builder nothing reads", built.builder, path, built.builder)
		}
		if built.door != "" {
			file, expr := src.varValue(t, built.door)
			lit, isLit := expr.(*ast.CompositeLit)
			if !isLit {
				t.Errorf("%s is not a composite literal in %s, so it is not the door spec builtLogNames names for `fugaro %s`", built.door, file, path)
				continue
			}
			if got, ok := fieldString(lit, "name"); !ok || got != path {
				t.Errorf("%s's name field is %q in %s, and `fugaro %s` files its log under %q: a renamed door is a run filed under a name no command has", built.door, got, file, path, name)
			}
			continue
		}
		file, body := src.funcBody(t, built.builder)
		if body == nil {
			t.Errorf("builtLogNames says %s builds `fugaro %s`'s log name, and no such function is defined in this package", built.builder, path)
			continue
		}
		if !slices.Contains(stringLiterals(body), name) {
			t.Errorf("%s does not return %q in %s, so the name `fugaro %s` files its log under is not one the source builds", built.builder, name, file, path)
		}
	}
}

// TestExemptCommandsAreListings is the half of the census D1354 decides: an
// exempt command whose stdout is a reading is a listing, and a listing's
// --json document is a contract (G2), so which commands carry one is checked
// against the tree rather than taken on trust. A listing with no document of
// its own prints its reading whole, and its reason has to say so.
func TestExemptCommandsAreListings(t *testing.T) {
	tree := censusCommands(t)
	if len(exemptListings) == 0 {
		t.Fatal("no listing is exempt, so a pass here would prove nothing")
	}
	for _, path := range slices.Sorted(maps.Keys(exemptListings)) {
		l := exemptListings[path]
		cmd, ok := tree[path]
		if !ok {
			continue // the coverage test reports the stale entry.
		}
		hasJSON := cmd.Flags().Lookup("json") != nil
		if l.json != hasJSON {
			t.Errorf("exemptListings says `fugaro %s` %s a --json document, and the tree says it %s: the entry is what the console and the plugin read", path, yesNo(l.json), yesNo(hasJSON))
		}
		if !l.json && !strings.Contains(l.why, "printed whole") {
			t.Errorf("`fugaro %s` is exempt as a listing with no --json document, so its reason has to say its reading is printed whole, and it says %q", path, l.why)
		}
	}

	for _, path := range slices.Sorted(maps.Keys(exemptListings)) {
		fmt.Printf("`fugaro %s`, exempt: %s\n", path, exemptListings[path].why)
	}
}

// censusCommands is the tree this pin is about: every command a person can
// type. Hidden commands are out -- a command the reference leaves out is not
// one a person reads -- and so is a parent that runs nothing, whose subcommands
// are the commands.
func censusCommands(t *testing.T) map[string]*cobra.Command {
	t.Helper()
	tree := map[string]*cobra.Command{}
	for path, cmd := range cliRefEveryCommand(cliRefRoot()) {
		if cmd.Runnable() && !cmd.Hidden && path != "fugaro" {
			tree[strings.TrimPrefix(path, "fugaro ")] = cmd
		}
	}
	if len(tree) < 100 {
		t.Fatalf("the walk found %d commands, and the tree has well over a hundred: a census that walks nothing passes everything", len(tree))
	}
	return tree
}

// censusSource is this package's non-test sources, parsed once. Every claim
// below is a claim about a declaration, a call or a name the source writes, and
// each of those is a node in this tree rather than a word in a file: a comment
// that says "update", or a function that is defined but never called, satisfies
// a search through the text and satisfies nothing here.
type censusSource struct {
	fset  *token.FileSet
	files map[string]*ast.File
}

func parseCensusSource(t *testing.T) *censusSource {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	src := &censusSource{fset: token.NewFileSet(), files: map[string]*ast.File{}}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(src.fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		src.files[name] = file
	}
	if len(src.files) == 0 {
		t.Fatal("no sources were parsed, so every check below would pass on an empty tree")
	}
	return src
}

// funcBody is the declaration of name -- a function or a method -- and the file
// it is written in, or a nil body when this package has no such declaration.
func (s *censusSource) funcBody(t *testing.T, name string) (string, *ast.BlockStmt) {
	t.Helper()
	for _, file := range slices.Sorted(maps.Keys(s.files)) {
		for _, decl := range s.files[file].Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name.Name != name || fd.Body == nil {
				continue
			}
			return file, fd.Body
		}
	}
	return "", nil
}

// eachFunc visits every declaration with a body, which is how a rule that has
// to hold of the whole package -- no other function opens a log -- is checked
// without a list of files to keep up to date.
func (s *censusSource) eachFunc(fn func(file, name string, body *ast.BlockStmt)) {
	for _, file := range slices.Sorted(maps.Keys(s.files)) {
		for _, decl := range s.files[file].Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
				fn(file, fd.Name.Name, fd.Body)
			}
		}
	}
}

// varValue is a package-level var's value, by the name it is bound to: the door
// specs are composite literals, and what the census claims about them is what
// the literal's own fields say.
func (s *censusSource) varValue(t *testing.T, name string) (string, ast.Expr) {
	t.Helper()
	for _, file := range slices.Sorted(maps.Keys(s.files)) {
		for _, decl := range s.files[file].Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, n := range vs.Names {
					if n.Name == name && i < len(vs.Values) {
						return file, vs.Values[i]
					}
				}
			}
		}
	}
	return "", nil
}

// reporterCalls are the places a reporter is built: the log names written at
// the call site, and the builders the names that cannot be written there are
// read from, each with the file and line of the call.
type reporterCalls struct {
	literal map[string]string
	built   map[string]string
}

// reporterConstructorCalls reads the second argument of every constructor call
// in this package: a string literal is the log name, and anything else is the
// call whose own name is the builder the census has to name.
func reporterConstructorCalls(t *testing.T, src *censusSource) reporterCalls {
	t.Helper()
	out := reporterCalls{literal: map[string]string{}, built: map[string]string{}}
	record := func(m map[string]string, key, at string) {
		if _, seen := m[key]; !seen {
			m[key] = at
		}
	}
	for _, file := range slices.Sorted(maps.Keys(src.files)) {
		ast.Inspect(src.files[file], func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 || !slices.Contains(reporterConstructors, calleeName(call.Fun)) {
				return true
			}
			at := fmt.Sprintf("%s:%d", file, src.fset.Position(call.Pos()).Line)
			if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if name, err := strconv.Unquote(lit.Value); err == nil {
					record(out.literal, name, at)
				}
				return true
			}
			if name := builderName(call.Args[1]); name != "" {
				record(out.built, name, at)
			}
			return true
		})
	}
	return out
}

// reporterConstructors are the calls that build a run through the reporter:
// reporterFor and convergeReporter in report_flags.go, stepRunFor in
// report_step.go, and openReporter, which is what all of them end in.
var reporterConstructors = []string{"reporterFor", "convergeReporter", "stepRunFor", "openReporter"}

// callsTo is every call to name inside n, where name is spelled the way the
// census spells it: `openReporter` for a call this package makes, and
// `report.OpenLog` for one it makes into another package. A declaration of the
// same name is not a call, and is not found here. The callee is named by
// calleeName, the package's one reader of a call's name (it is defined beside
// the compute-backend docs test, which reads calls the same way).
func callsTo(n ast.Node, name string) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(n, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && calleeName(call.Fun) == name {
			out = append(out, call)
		}
		return true
	})
	return out
}

// builderName is the function a constructor call reads a built log name from:
// the last segment of the argument's own callee, so `o.doorName()` is doorName
// and `budgetKillLogName(kill)` is budgetKillLogName, which is how
// builtLogNames names them. An argument that is not a call builds no name.
func builderName(arg ast.Expr) string {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return ""
	}
	name := calleeName(call.Fun)
	if at := strings.LastIndex(name, "."); at >= 0 {
		name = name[at+1:]
	}
	return name
}

// stringLiterals is every string literal written inside n, unquoted: a name
// returned by a builder is one of these, and nothing else counts.
func stringLiterals(n ast.Node) []string {
	var out []string
	ast.Inspect(n, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil {
			out = append(out, v)
		}
		return true
	})
	return out
}

// fieldString is the string a composite literal writes under key, which is how
// a door spec states its own name.
func fieldString(lit *ast.CompositeLit, key string) (string, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		k, ok := kv.Key.(*ast.Ident)
		if !ok || k.Name != key {
			continue
		}
		v, ok := kv.Value.(*ast.BasicLit)
		if !ok || v.Kind != token.STRING {
			continue
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	}
	return "", false
}

// readsField is whether n reads a field of that name at all: doorName returns
// the door's own name field, and a doorName that reads something else is a
// name no door wrote.
func readsField(n ast.Node, field string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
			found = true
		}
		return true
	})
	return found
}

// assignsFieldFromField is whether any declaration assigns a field named from
// another field named to, which is the one hop between a door spec's own name
// and what doorName returns. It is read structurally rather than by variable
// name, because what matters is that the field is filled from the spec and not
// what the two receivers are called.
func (s *censusSource) assignsFieldFromField(from, to string) bool {
	found := false
	s.eachFunc(func(_, _ string, body *ast.BlockStmt) {
		ast.Inspect(body, func(node ast.Node) bool {
			as, ok := node.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			if fieldName(as.Lhs[0]) == from && fieldName(as.Rhs[0]) == to {
				found = true
			}
			return true
		})
	})
	return found
}

func fieldName(e ast.Expr) string {
	if sel, ok := e.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}

// reporterPath maps a log name back to the command it is filed under, for the
// commands whose own name holds a dash of its own (`allow-delete` is one word
// of the path, not two).
func reporterPath(name string) (string, bool) {
	for _, cand := range []string{strings.ReplaceAll(name, "-", " "), name} {
		if hasKey(reporterCommands, cand) {
			return cand, true
		}
	}
	return "", false
}

func hasKey[V any](m map[string]V, key string) bool {
	_, ok := m[key]
	return ok
}

func yesNo(b bool) string {
	if b {
		return "carries"
	}
	return "carries no"
}
