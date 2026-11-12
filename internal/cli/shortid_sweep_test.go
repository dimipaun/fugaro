package cli

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// fullRunIDStamp is the launch stamp of a full run ID (task.NewRunID): the day
// and the second the run was minted, then the dash before its four random hex
// digits. A person reads runs by their ending and never by this (spec.md §1),
// so no human renderer's output carries one, and the sweep below drives each
// renderer over a world whose runs have one and looks for this.
var fullRunIDStamp = regexp.MustCompile(`\b\d{8}-\d{6}-`)

// sweepEndings are what the sweep world's four runs are shown as: two whose
// four characters already tell them apart, and two that share their last four
// and are therefore extended through the dash (SR1's rule, F1).
var sweepEndings = []string{"aaaa", "bbbb", "800-3bb2", "900-3bb2"}

// humanRenderer is one place the CLI shows runs to a person, as the sweep
// drives it.
type humanRenderer struct {
	// name is the command line a reader types, and the subtest's name.
	name string
	// drive runs the renderer over a world of its own and returns what each
	// stream carried. A world per renderer because a renderer that writes --
	// the digest writes a report -- must not decide what the next one finds.
	drive func(t *testing.T) (out, errOut string, err error)
	// shows are the strings the output must carry: the endings the world's
	// runs are shown as, so a renderer that printed nothing at all cannot
	// pass the sweep by saying nothing.
	shows []string
	// details is the reason this renderer names a run in full on purpose
	// (D1705). Empty means no full run ID may appear in its output.
	details string
}

// sweepRenderers is every human renderer the sweep drives: the CLI's listings
// of runs and the one-run views. Driving a command is what checks the whole of
// what it prints, the endings it does not build from a row among it, so this
// list is as wide as the sweep can reach by running things -- and a printer
// that is not here is caught by the source scan instead (assertNoPrintSiteNamesARunID).
var sweepRenderers = []humanRenderer{
	{
		name:  "runs ls",
		shows: sweepEndings,
		drive: func(t *testing.T) (string, string, error) {
			newSweepWorld(t)
			return execute(t, "runs", "ls", "--since", "0")
		},
	},
	{
		name:  "runs ls --group-by failure_class",
		shows: sweepEndings,
		drive: func(t *testing.T) (string, string, error) {
			newSweepWorld(t)
			return execute(t, "runs", "ls", "--since", "0", "--group-by", "failure_class")
		},
	},
	{
		name:  "watch --once --plain",
		shows: sweepEndings,
		drive: func(t *testing.T) (string, string, error) {
			newSweepWorld(t)
			return execute(t, "watch", "--once", "--plain")
		},
	},
	{
		name:  "budget show",
		shows: sweepEndings,
		drive: func(t *testing.T) (string, string, error) {
			newSweepWorld(t)
			return execute(t, "budget", "show")
		},
	},
	{
		// A run the bucket holds an unreadable object for is the one run a
		// listing names on its own: the row is an error row and the warning
		// above the table names the run. The world seeds one, so the line is
		// really printed and read here, not only read in the source
		// (assertNoPrintSiteNamesARunID).
		name:  "runs ls (a run whose objects are corrupt)",
		shows: []string{"aaaa", "cccc"},
		drive: func(t *testing.T) (string, string, error) {
			w := newSweepWorld(t)
			corruptSweepRun(t, w, "cccc")
			return execute(t, "runs", "ls", "--since", "0")
		},
	},
	{
		// The report's table is the digest's own listing: the rows are the
		// period's runs, and its ending rule is the same one (F14).
		name:  "runs digest",
		shows: []string{"aaaa", "800-3bb2", "900-3bb2"},
		drive: func(t *testing.T) (string, string, error) {
			newDigestSweepWorld(t)
			return execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", t.TempDir(), "--stdout")
		},
	},
	{
		// diagnose is the details view D1705 keeps whole: its head names the
		// run the person asked about, and that is the one place the full ID is
		// the point. It is driven here so the allow-list is a claim about what
		// the command really prints and not an exemption from looking.
		name:    "diagnose",
		shows:   []string{"Run:"},
		details: "the head of a run's details view names the run in full (D1705)",
		drive: func(t *testing.T) (string, string, error) {
			w := newSweepWorld(t)
			return execute(t, "diagnose", w.ids["aaaa"])
		},
	},
}

// sweepWorld is one fake installation holding the sweep's four runs.
type sweepWorld struct {
	*endingFixture
	// ids are the four run IDs, keyed by the ending each is shown as.
	ids map[string]string
}

// newSweepWorld builds the world the listing renderers are driven over: the
// ending fixture's installation (a runs bucket, a budget database, and the
// clock the seven-day window is measured from pinned) holding four runs of the
// fixture's repository -- two with endings that tell them apart on their own
// and two that share their last four characters -- each of them both a run the
// bucket holds (task.json) and a run the registry says is live.
func newSweepWorld(t *testing.T) *sweepWorld {
	t.Helper()
	pinSweepClocks(t)
	f := newEndingFixture(t)
	// The database names the installation's project, as an operator's update
	// wrote it: every read of the budget feed checks it first.
	f.db.Set(budget.PathProject, "aurora")
	w := &sweepWorld{endingFixture: f, ids: map[string]string{}}
	// Two runs a minute and two minutes back, plus the colliding pair: the
	// extension crosses the dash, so the two that share `3bb2` are shown as
	// `800-3bb2` and `900-3bb2` (SR1, F1).
	for _, r := range []struct {
		ending string
		at     time.Duration
		title  string
	}{
		{"aaaa", time.Minute, "the newest run"},
		{"bbbb", 2 * time.Minute, "the older run"},
		{"900-3bb2", time.Minute, "the newer colliding run"},
		{"800-3bb2", 2 * time.Minute, "the older colliding run"},
	} {
		id := recentRunID(t, r.at, task.ShortRun(r.ending))
		w.ids[r.ending] = id
		seedTitledRun(t, f.cloudFixture, id, r.title)
		seedSweepAgent(t, f, id, r.title)
	}
	return w
}

// corruptSweepRun adds a run of the fixture's repository whose launch.json the
// bucket holds undecodable, so the listing that reads it lists an error row and
// warns about it. The warning is the one line of a listing that names a run
// with no table around it, and it is where a whole ID reached a person before
// this test drove it (runs.go's readRunVia).
func corruptSweepRun(t *testing.T, w *sweepWorld, ending string) string {
	t.Helper()
	id := recentRunID(t, 3*time.Minute, task.ShortRun(ending))
	w.ids[ending] = id
	seedTitledRun(t, w.cloudFixture, id, "the corrupt run")
	b, err := blobx.Open(context.Background(), w.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := runstore.Open(b.Bucket, appSlug, id).PutFile(context.Background(), "launch.json", []byte("{not json"), "application/json"); err != nil {
		t.Fatal(err)
	}
	return id
}

// newDigestSweepWorld builds the world the digest is driven over: the fixture's
// repository checked out, and four runs whose records finished in the closed
// day the report is asked for, two of them sharing their last four characters
// so the report's own collision rule has something to extend.
func newDigestSweepWorld(t *testing.T) {
	t.Helper()
	digestCheckout(t)
	digestClock(t, "2026-10-27")
	f := newCloudFixture(t)
	fin := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	// Three rows and no more: the report's table shows three (digest's own
	// cap), and the two that share `3bb2` are extended to `800-3bb2` and
	// `900-3bb2` over exactly the rows printed (F15).
	for _, id := range []string{
		"20261025-115900-aaaa", "20261025-115900-3bb2", "20261025-115800-3bb2",
	} {
		seedDigestRun(t, f, id, fin, "acme/app")
	}
}

// pinSweepClocks pins every clock a swept renderer measures a window from to
// the instant the ending fixture mints its run IDs against, so all of them list
// the same four runs whatever day the test happens to run on.
func pinSweepClocks(t *testing.T) {
	t.Helper()
	oldWatch, oldBudget := watchNow, budgetNow
	watchNow = func() time.Time { return endingNow }
	budgetNow = func() time.Time { return endingNow }
	t.Cleanup(func() { watchNow, budgetNow = oldWatch, oldBudget })
}

// seedSweepAgent puts one run in the budget database's /agents registry, the
// feed watch and budget show read their live rows from. The entry is a claim
// and not a run -- resolveRun confirms one against the bucket (F6) -- but a
// listing draws what the feed says, and the sweep needs rows to draw.
func seedSweepAgent(t *testing.T, f *endingFixture, id, title string) {
	t.Helper()
	f.db.Set(budget.PathAgent(appSlug, id), map[string]any{
		"repo": "acme/app", "title": title, "stage": "code", "auth": "api_key",
		"startedAt": endingNow.Add(-time.Minute).UnixMilli(), "updatedAt": endingNow.UnixMilli(),
		"requestedBy": "someone@example.com"})
}

// fullRunIDSites is the allow-list of print sites that may name a run ID in
// full, keyed file:function with the reason each may (D1705). A print call
// whose arguments carry a run ID that no shortener wrapped is a site, and a
// site that is not here fails the sweep: adding a printer means either passing
// the ID through task.ShortRun or saying here why it stays whole. The list is
// read both ways, so an entry whose site stopped printing a run ID fails too.
var fullRunIDSites = map[string]string{
	"cancel.go:printCancel":                   "the ID is replaced by its short form at the head of the function, so every sentence under it names the run the way the person typed it",
	"cloud.go:fullRows":                       "cancel's list is the one place a person copies a whole ID from: cancel acts on nothing else (D1710)",
	"diagnose.go:followUpLine":                "the details view names, in full, the run a follow-up continues (D1705)",
	"diagnose.go:printDiagnosis":              "the head of the details view is where a person copies the whole ID (D1705)",
	"plan_status.go:printPlanStatusPlan":      "plan.State.Run is the short suffix already, so the cell carries no full ID",
	"troubleshoot.go:printTroubleshoot":       "the ID is a command log's own name, not a cloud run's (D1609), and it is the argument the person types back into `fugaro troubleshoot` to explain this failure again: shortened, what is left is the pid, which identifies nothing",
	"troubleshoot.go:troubleshootWriteReport": "the report is read beside the evidence folder it sits in, where the log it is about is named by that same ID (D1605, D1609), so a reader can match the two",
}

// runIDNames are the fields an expression names a run ID by at a print site: a
// row's Run and RunID, a follow-up's PreviousRun, and the lowercase forms the
// lookups' own structs use.
var runIDNames = map[string]bool{"Run": true, "RunID": true, "PreviousRun": true, "runID": true, "previousRun": true}

// shortenerNames are the calls a print site shows an ending through. Any of
// them between the ID and the call that prints is the shortening the rule asks
// for (task.ShortRun is the one rule, shortRef its runstore-shaped caller).
var shortenerNames = map[string]bool{"ShortRun": true, "shortRef": true}

// printNames are the calls that write text a person reads.
var printNames = map[string]bool{
	"Fprintf": true, "Printf": true, "Sprintf": true, "Fprintln": true, "Fprint": true,
	"Println": true, "Print": true, "WriteString": true,
	// The listing's own warning writer: `runs ls` tells a person about a run it
	// could not read through it, and that line names the run.
	"warnf": true,
}

// readerNames are the calls that build a person's message but are not print
// sites everywhere. A `userErr` or a `fmt.Errorf` mostly carries the run ID the
// person themselves supplied — `--run-id`, `--attach`, and `run --retry`, which
// the plan keeps on the whole ID — and a message about the person's own input
// keeps it whole. Inside one of the package's readers (runReader) the signature
// says which value is the run, so there they are read like any other printer.
var readerNames = map[string]bool{"userErr": true, "Errorf": true}

// TestHumanOutputHasNoFullRunID is the sweep: every human renderer is driven
// over a fake installation holding runs with distinct and colliding endings,
// and no output of one carries a full run ID's launch stamp. The details views
// are the allow-list (D1705) and are asserted to carry the full ID, so the list
// is a claim about what they print rather than a hole in the sweep.
//
// The registry of renderers only covers the printers it names, so the test
// then reads this package's own production sources and holds every print site
// that names a run ID to the same allow-list (fullRunIDSites): a printer nobody
// registered fails there instead of passing the sweep by not running.
func TestHumanOutputHasNoFullRunID(t *testing.T) {
	for _, r := range sweepRenderers {
		t.Run(r.name, func(t *testing.T) {
			out, errOut, err := r.drive(t)
			if err != nil {
				t.Fatalf("%s = %v (%s)", r.name, err, errOut)
			}
			text := out + errOut
			for _, want := range r.shows {
				if !strings.Contains(text, want) {
					t.Errorf("%s does not show %q:\n%s", r.name, want, text)
				}
			}
			if r.details != "" {
				if !fullRunIDStamp.MatchString(text) {
					t.Errorf("%s is a details view (%s) and shows no full run ID:\n%s", r.name, r.details, text)
				}
				return
			}
			for _, stamp := range fullRunIDStamp.FindAllString(text, -1) {
				t.Errorf("%s prints the full run ID stamp %q: a person reads runs by their ending (spec.md §1), and the full ID belongs to --json and the details views (D1705):\n%s",
					r.name, stamp, text)
			}
		})
	}
	assertNoPrintSiteNamesARunID(t)
}

// assertNoPrintSiteNamesARunID reads this package's production sources and
// fails a print call whose arguments carry a run ID that no shortener wrapped
// and no allow-list entry owns. A run ID reaches a call through a field of the
// row, run or task it is read off, so the scan looks for those fields
// (runIDNames) among the arguments of every print call (printNames), skips the
// ones a shortener sits above (shortenerNames) or that are only a map key or a
// field name, and holds the rest to fullRunIDSites.
//
// Two things a printer may be written with are read only where the signature
// says what they hold: inside a reader — a function handed the run's slug
// beside the run itself (runReader) — a bare `id` or `root` is the run
// (readRunVia and readTask say `id`, checkRoot says `root`, and a message that
// names one of them there names a run), and the message builders (readerNames)
// count as print sites. Everywhere else `id` is whatever else a function calls
// an id (a model, a secret, a build), `root` is a directory, and a `userErr`
// about a `--run-id` the person supplied names their own input.
//
// It is a reading of the source and not a proof: an ID a format string prints
// as a value the scanner cannot see (a struct marshalled, an ID carried in a
// variable through three functions, a run read off a store's own `Slug()`) is
// not a site. The sweep's driven renderers are what covers those, and this is
// what covers a printer the registry does not know.
func assertNoPrintSiteNamesARunID(t *testing.T) {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no production sources found: this guard would pass over nothing")
	}
	sites := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			reader := runReader(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				called := lastName(call.Fun)
				if !printNames[called] && !(len(reader) > 0 && readerNames[called]) {
					return true
				}
				for _, a := range call.Args {
					if carriesRunID(a, reader) {
						sites[name+":"+fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}
	if len(sites) == 0 {
		t.Fatal("no print site in this package names a run ID: the scan read nothing, so every allowed site below is a claim about code it did not look at")
	}
	for site := range sites {
		why, ok := fullRunIDSites[site]
		switch {
		case !ok:
			t.Errorf("%s prints a run ID that no shortener wrapped: pass it through task.ShortRun (a person reads runs by their ending, spec.md §1), or say in fullRunIDSites why that ID stays whole (a details view or a machine's document, D1705)", site)
		case why == "":
			t.Errorf("%s is allowed with no reason: an exemption nobody can read is an omission", site)
		}
	}
	for site := range fullRunIDSites {
		if !sites[site] {
			t.Errorf("%s is allowed and no longer prints a run ID: remove the entry, or the list holds a claim about code that is not there", site)
		}
	}
}

// lastName is the trailing identifier of a call's function: the Fprintf of
// fmt.Fprintf, the WriteString of a strings.Builder's.
func lastName(fun ast.Expr) string {
	switch v := fun.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.IndexExpr: // a generic instantiation: fmt.Fprintf[T](...)
		return lastName(v.X)
	}
	return ""
}

// carriesRunID reports whether e carries a run ID into the call it is an
// argument of: the ID reaches the output through calls and operators, and not
// through a shortener, a map key (the key names the run, the value is what is
// printed) or a field name. reader is the enclosing function's run-naming
// parameters (runReader), the names a bare ident has to be to be the run.
func carriesRunID(e ast.Expr, reader map[string]bool) bool {
	var stack []ast.Node
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if namesRunID(n, reader) && !shortens(stack) {
			found = true
		}
		stack = append(stack, n)
		return true
	})
	return found
}

// namesRunID reports whether n is an expression a run ID is read off: a field
// of the row, run or task it belongs to (runIDNames), or a name the enclosing
// function gives the run — `runID` anywhere, and the bare ident a reader's own
// signature says is the run (runReader).
func namesRunID(n ast.Node, reader map[string]bool) bool {
	switch v := n.(type) {
	case *ast.SelectorExpr:
		return runIDNames[v.Sel.Name]
	case *ast.Ident:
		return v.Name == "runID" || reader[v.Name]
	}
	return false
}

// runReader is what fn calls the run it is handed, for one of the package's
// readers: a function given the run's slug beside the run itself names it,
// either `id` (readRun, readRunVia, readTask, scanRow) or `root`, the run a
// branch names (checkRoot). A helper handed one without the other names no run
// this way — a repository, a project layer, a spec and a directory all come
// under some `id` or `root` elsewhere in the package — so only the fields of
// runIDNames count there.
func runReader(fn *ast.FuncDecl) map[string]bool {
	if fn.Type.Params == nil {
		return nil
	}
	named := map[string]bool{}
	for _, p := range fn.Type.Params.List {
		for _, n := range p.Names {
			named[n.Name] = true
		}
	}
	if !named["slug"] {
		return nil
	}
	runs := map[string]bool{}
	for _, name := range []string{"id", "root"} {
		if named[name] {
			runs[name] = true
		}
	}
	return runs
}

// shortens reports whether any ancestor in stack takes the ID out of the
// printed value: a shortener call, a map index (the ID is a key), or a key of a
// composite literal (the ID is a field name).
func shortens(stack []ast.Node) bool {
	for _, n := range stack {
		switch v := n.(type) {
		case *ast.CallExpr:
			if shortenerNames[lastName(v.Fun)] {
				return true
			}
		case *ast.IndexExpr, *ast.KeyValueExpr:
			return true
		}
	}
	return false
}

// TestMachineSurfacesKeepFullRunID is the sweep's other half: the places a
// machine reads a run ID keep it whole, whatever a person's frame shows
// (D1705). Each is driven through what really produces it -- the launch's own
// branch builder, the marker a pull request's body carries, the environment a
// run's commits are stamped from, the backlog file a run files and the stored
// task -- and not through a copy of the shape, so a surface that starts
// shortening fails here rather than in a machine's own reading.
func TestMachineSurfacesKeepFullRunID(t *testing.T) {
	w := newSweepWorld(t)
	ctx := context.Background()
	id := w.ids["aaaa"]

	t.Run("--json", func(t *testing.T) {
		out, errOut, err := execute(t, "runs", "ls", "--since", "0", "--json")
		if err != nil {
			t.Fatalf("runs ls --json = %v (%s)", err, errOut)
		}
		for _, want := range w.ids {
			if !strings.Contains(out, want) {
				t.Errorf("runs ls --json does not carry the full run ID %s: --json is a machine's document, and the console, the paging cursor and --after read the ID whole (D1705):\n%s", want, out)
			}
		}
	})

	t.Run("branch", func(t *testing.T) {
		branch, err := budgetBranch(&task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web"})
		if err != nil {
			t.Fatal(err)
		}
		if branch != "fugaro/"+id {
			t.Fatalf("the branch of %s = %q, want fugaro/%s: a follow-up continues a run through the branch's own ID (F9)", id, branch, id)
		}
		if root, ok := task.BranchRunID(branch); !ok || root != id {
			t.Errorf("task.BranchRunID(%q) = %q, %v, want the whole ID: the branch is an identifier a machine reads, never a display of one", branch, root, ok)
		}
	})

	t.Run("marker", func(t *testing.T) {
		marker := gitprov.ReportMarker(id)
		if !strings.Contains(marker, id) {
			t.Fatalf("gitprov.ReportMarker(%s) = %q, want the whole ID: the hidden marker is how the next run finds the PR's run (F9)", id, marker)
		}
		if got, ok := gitprov.FugaroRun("a body\n" + marker + "\n"); !ok || got != id {
			t.Errorf("gitprov.FugaroRun of the marker = %q, %v, want %q", got, ok, id)
		}
	})

	t.Run("commit trailer", func(t *testing.T) {
		env := gitops.StampEnv(gitops.Stamp{Trailers: []gitops.Trailer{{Key: gitops.TrailerRun, Value: id}}}, t.TempDir(), nil)
		if want := "FUGARO_STAMP_RUN=" + id; !slices.Contains(env, want) {
			t.Fatalf("the commit stamp carries no %q: a commit's trailer names the run that made it, whole (F9):\n%v", want, env)
		}
	})

	t.Run("filed_in", func(t *testing.T) {
		dir := t.TempDir()
		item, err := backlog.Write(dir, backlog.New{
			Title: "the sweep's item", Mode: "none", Area: "project",
			FiledBy: "dana", FiledIn: "run:" + id, Date: "2026-11-09",
			Body: backlog.Body("what was left open", "why it was left open", "", nil),
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.Base(item.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if want := "filed-in: run:" + id; !strings.Contains(string(data), want) {
			t.Errorf("the filed item carries no %q: the field is what a reader resolves the item's run by, whole (F9, F14):\n%s", want, data)
		}
	})

	t.Run("task.json", func(t *testing.T) {
		env := w.env(t)
		store := runstore.Open(env.bucket.Bucket, appSlug, id)
		if !strings.Contains(store.Prefix(), id) {
			t.Errorf("the run's bucket prefix %q does not carry %q: a run's objects live under its whole ID (F9)", store.Prefix(), id)
		}
		spec, err := store.ReadTask(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if spec.RunID != id {
			t.Errorf("task.json's run_id = %q, want %q: the stored task is an identifier, not a display (D1705)", spec.RunID, id)
		}
	})
}
