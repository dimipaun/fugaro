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
// reporter's half is held to the source and to the log rule from both sides.
//
// The reasons are printed on every run of the pin, the way CL10's are: an
// exemption a reviewer cannot read is a hole nobody sees.

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
// four constructors call. So the census is checked against the source rather
// than against a promise: every log name a constructor is called with is an
// entry, every entry is a name the source writes or one of the two builders
// that cannot (builtLogNames), the entry is the command's own name because that
// is what D1351 files a run under, and report.OpenLog accepts it -- the real
// rule, run for real, rather than a copy of its pattern.
func TestEveryMutatingCommandWritesALog(t *testing.T) {
	sources := packageSources(t)
	calls := reporterConstructorCalls(sources)
	if len(calls) == 0 {
		t.Fatal("no reporter constructor is called anywhere in this package, so a pass here would prove nothing")
	}

	// The chain the census rests on: every constructor ends in openReporter,
	// and openReporter is the only thing in this package that opens a log.
	for _, ctor := range []string{"reporterFor", "convergeReporter"} {
		if !mentionsCall(sources["report_flags.go"], ctor) || !mentionsCall(sources["report_flags.go"], "openReporter") {
			t.Errorf("%s is not built on openReporter in report_flags.go, so a reporter command may no longer reach report.OpenLog at all", ctor)
		}
	}
	if !strings.Contains(sources["report_step.go"], "reporterFor(") {
		t.Error("stepRunFor does not build its reporter through reporterFor in report_step.go, so the step family no longer reaches report.OpenLog")
	}
	logSites := 0
	for _, file := range slices.Sorted(maps.Keys(sources)) {
		if strings.Contains(sources[file], "report.OpenLog(") {
			logSites++
			if file != "report_flags.go" {
				t.Errorf("%s opens a log directly, and openReporter in report_flags.go is the one place a run's log is opened", file)
			}
		}
	}
	if logSites != 1 {
		t.Errorf("report.OpenLog is called from %d files, want exactly one (report_flags.go): a second call site is a run that opens a log no constructor made", logSites)
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
	// the call site, or built by the one function builtLogNames names.
	for _, name := range slices.Sorted(maps.Keys(calls)) {
		if _, ok := reporterPath(name); !ok {
			t.Errorf("a reporter is built at %s under the log name %q, which is not an entry in reporterCommands: a command that prints through the reporter has to be on the census", calls[name], name)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(reporterCommands)) {
		name := reporterCommands[path]
		if _, ok := calls[name]; ok {
			continue
		}
		builder, ok := builtLogNames[path]
		if !ok {
			t.Errorf("no reporter constructor in this package is called with %q, and builtLogNames does not name the function that builds it: `fugaro %s` is on the census with nothing in the source to hold it to", name, path)
			continue
		}
		src, ok := sources[builderFile(builder)]
		if !ok || !definesFunc(src, builder) {
			t.Errorf("builtLogNames says %s builds `fugaro %s`'s log name, and no such function is defined in this package", builder, path)
			continue
		}
		if !strings.Contains(src, `"`+name+`"`) {
			t.Errorf("%s does not name %q in %s, so the name `fugaro %s` files its log under is not the one the source builds", builder, name, builderFile(builder), path)
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

	// A listing is not a run: no exempt command may be one the reporter is
	// building, which is what the log names in the source would say.
	calls := reporterConstructorCalls(packageSources(t))
	for _, path := range slices.Sorted(maps.Keys(exemptListings)) {
		if name, onReporter := reporterCommands[path]; onReporter {
			if _, built := calls[name]; built {
				t.Errorf("`fugaro %s` is exempt as a listing and a reporter is built for it under %q: the two say different things about one command", path, name)
			}
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

// reporterConstructorCalls reads this package's own sources for the log names
// the reporter is built under, keyed by name with the file and line that names
// it. Only a literal at the call site can be read: the two commands built by
// builtLogNames are named at run time, which is why they are listed there.
func reporterConstructorCalls(sources map[string]string) map[string]string {
	calls := map[string]string{}
	for _, file := range slices.Sorted(maps.Keys(sources)) {
		for i, line := range strings.Split(sources[file], "\n") {
			for _, ctor := range reporterConstructors {
				at := strings.Index(line, ctor)
				if at < 0 {
					continue
				}
				rest := line[at+len(ctor):]
				open := strings.Index(rest, `"`)
				if open < 0 {
					continue // the name is built, not written: builtLogNames' business.
				}
				shut := strings.Index(rest[open+1:], `"`)
				if shut < 0 {
					continue
				}
				name := rest[open+1 : open+1+shut]
				if _, seen := calls[name]; !seen {
					calls[name] = fmt.Sprintf("%s:%d", file, i+1)
				}
			}
		}
	}
	return calls
}

// reporterConstructors are the calls that build a run through the reporter:
// reporterFor and convergeReporter in report_flags.go, stepRunFor in
// report_step.go, and openReporter, which is what all of them end in.
var reporterConstructors = []string{"reporterFor(", "convergeReporter(", "stepRunFor(", "openReporter("}

// packageSources is this package's non-test Go source, keyed by file name. The
// tests run in the package's own directory, so the names are the ones the
// census's reasons cite.
func packageSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	sources := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		sources[name] = string(b)
	}
	if len(sources) == 0 {
		t.Fatal("no sources were read, so every check below would pass on an empty map")
	}
	return sources
}

// builderFile is where the function builtLogNames names is defined, by the file
// the census's reasons cite. The two are the converge's door name and the kill
// switch's two names.
func builderFile(builder string) string {
	if builder == "doorName" {
		return "init.go"
	}
	return "budget.go"
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

// mentionsCall is whether src calls name at all, so that a helper whose whole
// job is one call is held to making it.
func mentionsCall(src, name string) bool {
	return strings.Contains(src, name+"(")
}

// definesFunc is whether src defines name, method or function: a method's
// receiver stands between `func` and the name, and the space in front is what
// both spellings share.
func definesFunc(src, name string) bool {
	return strings.Contains(src, " "+name+"(")
}
