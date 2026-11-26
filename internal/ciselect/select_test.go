package ciselect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// selectTinyGraph is a one-package listing: enough for a graph to parse, and
// nothing the tests' changed paths are.
const selectTinyGraph = `{"ImportPath":"example.com/m/internal/other","Name":"other",` +
	`"Dir":"/mod/internal/other","Module":{"Path":"example.com/m","Main":true,"Dir":"/mod"},` +
	`"GoFiles":["other.go"]}` + "\n"

func TestSelectIsFullForEveryDoubtOfR8(t *testing.T) {
	rows := []struct {
		name string
		want string
		// input builds the run's Input; the diff it carries is the row's one
		// difference where a doubt has to be reached through the change.
		input func(t *testing.T) Input
	}{
		{
			name: "a dispatch asked for everything",
			want: ReasonFullInput,
			input: func(t *testing.T) Input {
				in := selectRealInput(t, "internal/rtdb/client.go")
				in.Full = true
				return in
			},
		},
		{
			name: "the nightly takes no input",
			want: ReasonScheduled,
			input: func(t *testing.T) Input {
				in := selectRealInput(t, "internal/rtdb/client.go")
				in.Scheduled = true
				return in
			},
		},
		{
			name: "no full run on main to measure from",
			want: ReasonNoBase,
			input: func(t *testing.T) Input {
				in := selectRealInput(t, "internal/rtdb/client.go")
				in.Base = ""
				in.GitHub = &fakeGitHub{runErr: errors.New("gh: no runs")}
				return in
			},
		},
		{
			name: "the diff would not run",
			want: ReasonDiff,
			input: func(t *testing.T) Input {
				in := selectRealInput(t, "internal/rtdb/client.go")
				in.Command.(*fakeCommand).failCall = "git diff"
				return in
			},
		},
		{
			name: "the read map is missing",
			want: ReasonNoReadMap,
			input: func(t *testing.T) Input {
				in := selectRealInput(t, "internal/rtdb/client.go")
				in.Root = selectWriteRoot(t, map[string]string{"go.mod": "module example.com/m\n\ngo 1.24\n"})
				return in
			},
		},
		{
			name: "the graph would not list",
			want: ReasonNoGraph,
			input: func(t *testing.T) Input {
				in := selectRealInput(t, "internal/rtdb/client.go")
				in.Command.(*fakeCommand).failCall = "go list -deps"
				return in
			},
		},
		{
			// The real map, because this is the doubt a catch-all entry would
			// silently swallow: `**` and `*/*` are the scan's own record of a
			// path it could not name, and a test over a hand-written map could
			// not tell that they leave this one unable to fire.
			name: "a path no package owns and no entry names",
			want: ReasonUnmapped,
			input: func(t *testing.T) Input {
				return selectRealInput(t, "notes/random.txt")
			},
		},
		{
			name: "go.mod",
			want: ReasonAlwaysFull,
			input: func(t *testing.T) Input {
				return selectRealInput(t, "go.mod")
			},
		},
		{
			name: "go.sum",
			want: ReasonAlwaysFull,
			input: func(t *testing.T) Input {
				return selectRealInput(t, "go.sum")
			},
		},
		{
			name: "the workflow that calls the selector",
			want: ReasonAlwaysFull,
			input: func(t *testing.T) Input {
				return selectRealInput(t, ".github/workflows/ci.yml")
			},
		},
		{
			name: "the selector itself",
			want: ReasonAlwaysFull,
			input: func(t *testing.T) Input {
				return selectRealInput(t, "scripts/ci-select/main.go")
			},
		},
		{
			name: "the read map",
			want: ReasonAlwaysFull,
			input: func(t *testing.T) Input {
				return selectRealInput(t, readMapPath)
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			out := selectIn(t, row.input(t))
			if !out.Full {
				t.Fatalf("Full = false, want a full run for %s", row.want)
			}
			if !strings.Contains(out.Reason, row.want) {
				t.Errorf("Reason = %q, want it to name %q", out.Reason, row.want)
			}
			// A full run runs every job, whatever the doubt.
			if !out.Check || !out.Terraform || !out.Rules {
				t.Errorf("jobs = terraform %v, rules %v, check %v, want a full run to run every one",
					out.Terraform, out.Rules, out.Check)
			}
		})
	}
	// The two ways a path is accounted for, each against the real map and the
	// real graph: the doubt above is about a path nothing names, not about
	// every path no package of the graph happens to own.
	t.Run("a path the graph owns is not a doubt", func(t *testing.T) {
		in := selectRealInput(t, "internal/other/other.go")
		in.Root = selectWriteRoot(t, map[string]string{
			"go.mod":    "module example.com/m\n\ngo 1.24\n",
			readMapPath: `{"packages":[{"package":"./internal/cli","reads":["internal/cli/cli.go"]}]}` + "\n",
		})
		in.Command.(*fakeCommand).graph = []byte(selectTinyGraph)
		if out := selectIn(t, in); out.Full {
			t.Errorf("Reason = %q, want the owned path selected", out.Reason)
		}
	})
	t.Run("a path the real map names is not a doubt", func(t *testing.T) {
		// No package of the graph owns this file: the map names it with
		// "images/**", "images/base/**" and the path itself.
		if out := selectIn(t, selectRealInput(t, "images/base/Dockerfile")); out.Full {
			t.Errorf("Reason = %q, want the named path selected", out.Reason)
		}
	})
	t.Run("a catch-all entry makes a package run without naming a path", func(t *testing.T) {
		// ./internal/cli reads "**" among far more: that is R4's reason to run
		// it, not a reason to believe it knows where any particular file lives.
		// Reading the two questions as one is what left the doubt above unable
		// to fire on the committed map.
		m, err := LoadReadMap(filepath.Join(selectRoot(t), readMapPath))
		if err != nil {
			t.Fatalf("read map: %v", err)
		}
		g, err := ParseGraph(bytes.NewReader(selectListing(t, BuildTags)))
		if err != nil {
			t.Fatalf("graph: %v", err)
		}
		var cli PackageReads
		for _, p := range m.Packages {
			if p.Package == "./internal/cli" {
				cli = p
			}
		}
		if !readsAny(cli, []string{"notes/random.txt"}) {
			t.Fatal("./internal/cli no longer reads \"**\", so this subtest says nothing")
		}
		if mapped("notes/random.txt", m, g) {
			t.Error("mapped = true for a path only a catch-all matches")
		}
	})
}

func TestSelectBaseIsTheLastFullRunOnMain(t *testing.T) {
	cmd := &fakeCommand{
		graph: selectListing(t, BuildTags),
		pkgs:  selectTestPackages(t, BuildTags),
		diff:  "internal/rtdb/client.go\n",
		runList: `[{"databaseId":99,"headSha":"newest","displayTitle":"ci (selected)"},` +
			`{"databaseId":42,"headSha":"fullhead","displayTitle":"ci (full)"},` +
			`{"databaseId":7,"headSha":"olderhead","displayTitle":"ci (full)"}]`,
	}
	// The real GitHub seam, over the fake gh: the run list argv and the base it
	// yields are both what this test is about.
	gh := GHCommand{Command: cmd, Dir: selectRoot(t)}
	in := Input{Root: selectRoot(t), Head: "headsha", GitHub: gh, Command: cmd}
	out := selectIn(t, in)
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	if want := "gh run list --workflow ci --branch main --status success --limit 100 --json databaseId,headSha,displayTitle"; !selectCalled(cmd, want) {
		t.Errorf("calls = %v, want %q", cmd.calls, want)
	}
	// The base is the head of the last run titled `ci (full)`: not the newest
	// run, which is a selected one, and not the full run before it.
	if want := "git diff --name-only --no-renames fullhead headsha"; !selectCalled(cmd, want) {
		t.Errorf("calls = %v, want %q", cmd.calls, want)
	}
}

func TestSelectUsesATwoDotDiffWithoutRenames(t *testing.T) {
	dir := selectWriteRoot(t, map[string]string{
		"go.mod":    "module example.com/m\n\ngo 1.24\n",
		readMapPath: `{"packages":[{"package":"./olds","reads":["a.txt"]},{"package":"./news","reads":["b.txt"]}]}` + "\n",
		"a.txt":     "one\n",
	})
	selectGit(t, dir, "init", "-q")
	base := selectGitCommit(t, dir, "base")
	selectGit(t, dir, "mv", "a.txt", "b.txt")
	head := selectGitCommit(t, dir, "rename")
	out := selectIn(t, Input{
		Root: dir, Head: head, Base: base,
		GitHub:  &fakeGitHub{},
		Command: &fakeCommand{graph: []byte(selectTinyGraph), gitReal: true},
	})
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	// A two-dot diff without renames reports both names, so the package that
	// read the file where it was runs as well as the one that reads it now.
	for _, want := range []string{"./olds", "./news"} {
		if !selectHasPkg(out, want) {
			t.Errorf("legs = %v, want %s to run", selectLegPkgs(out), want)
		}
	}
}

func TestSelectCatalogFetchReachesPlugin(t *testing.T) {
	out := selectFixture(t, "internal/catalog/fetch.go")
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	// The catalog fetcher is read by the plugin's skill tests, which no import
	// edge reaches: only the read map does.
	for _, want := range []string{"./plugin", "./internal/cli"} {
		if !selectHasPkg(out, want) {
			t.Errorf("legs = %v, want %s to run", selectLegPkgs(out), want)
		}
	}
}

func TestSelectFakeTerraformReachesCli(t *testing.T) {
	out := selectFixture(t, "internal/infra/tf/faketerraform/main.go")
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	// internal/cli runs the fake terraform binary as a process, so no import
	// edge reaches it; the read map names the tree its tests exec.
	if !selectHasPkg(out, "./internal/cli") {
		t.Errorf("legs = %v, want ./internal/cli to run", selectLegPkgs(out))
	}
	if !out.Terraform {
		t.Error("Terraform = false, want the job for a change under internal/infra")
	}
}

func TestSelectAnEmbeddedTemplateReachesItsDocsReaders(t *testing.T) {
	out := selectFixture(t, "internal/budget/rules/rules.json.tmpl")
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	// The template is embedded by the rules package and read by every test that
	// builds a project against the rules: both halves of the selection, and the
	// rules job.
	for _, want := range []string{"./internal/budget/rules", "./internal/cli"} {
		if !selectHasPkg(out, want) {
			t.Errorf("legs = %v, want %s to run", selectLegPkgs(out), want)
		}
	}
	if !out.Rules {
		t.Error("Rules = false, want the job for a change under internal/budget/rules")
	}
}

func TestSelectDocsOnlyRunsTheDocsReaders(t *testing.T) {
	out := selectFixture(t, "docs/ci-affected-tests/reconciliation.md")
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	// R7: a documents-only change runs what the map says reads a changed file,
	// as whole families -- not the packages that read the tree at run time,
	// which is every other change.
	if !selectHasPkg(out, "./internal/cli") {
		t.Errorf("legs = %v, want ./internal/cli to run", selectLegPkgs(out))
	}
	if !selectHasLeg(out, "cli-update") {
		t.Errorf("legs = %v, want the cli families whole", selectLegNames(out))
	}
	if selectHasPkg(out, "./internal/bbtokens") {
		t.Errorf("legs = %v, want a package the map does not name for this file left out", selectLegPkgs(out))
	}
	if out.Terraform || out.Rules {
		t.Errorf("jobs = terraform %v, rules %v, want neither for documents", out.Terraform, out.Rules)
	}
	if len(out.Legs) > DefaultMaxLegs {
		t.Errorf("legs = %d, want no more than the full run's %d", len(out.Legs), DefaultMaxLegs)
	}
}

func TestSelectFixtureRepoReachesFixtureFilesCallers(t *testing.T) {
	out := selectFixture(t, "testdata/fixture-repo/fugaro.yaml")
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	// The fixture repository is a directory a test copies and runs the CLI
	// against: the map names the subtree, the graph can only own its own files.
	for _, want := range []string{"./internal/cli", "./internal/runner"} {
		if !selectHasPkg(out, want) {
			t.Errorf("legs = %v, want %s to run", selectLegPkgs(out), want)
		}
	}
}

func TestSelectSourcedShellLibraryReachesItsCallers(t *testing.T) {
	out := selectFixture(t, "scripts/retry.sh")
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	// scripts/retry.sh is sourced by scripts/release-gate.sh, which is the only
	// place it is named: the scan follows the source line to the caller.
	if !selectHasPkg(out, "./internal/cli") {
		t.Errorf("legs = %v, want ./internal/cli to run", selectLegPkgs(out))
	}
}

// TestSelectAnAnyEntryRunsUnlessEveryChangedPathIsIgnored pins R4: a package
// the map marks `any` reads a path it builds at run time, so it runs for a
// change its own list does not name — unless every changed path is one its walk
// states it steps out of, which is the only way such a package stands down.
// That is the direction the selector may not fail in (the plan's first global
// constraint), and the committed map marks 119 of its 135 packages `any`, so a
// rule that stopped holding would silently run fewer tests than a full run
// would have failed on.
func TestSelectAnAnyEntryRunsUnlessEveryChangedPathIsIgnored(t *testing.T) {
	// Hand-written, because selectFixture clears the committed map's `any`
	// flags: nothing else here exercises the rule at all.
	root := selectWriteRoot(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.24\n",
		readMapPath: `{"packages":[` +
			`{"package":"./internal/alpha","reads":["internal/alpha/one.go"],"any":true},` +
			`{"package":"./internal/beta","reads":["internal/beta/one.go"],"any":true,"ignores":["internal/beta/out"]},` +
			`{"package":"./internal/named","reads":["internal/named/thing.go"]},` +
			`{"package":"./internal/quiet","reads":["internal/quiet/other.go"]}]}` + "\n",
	})
	rows := []struct {
		name  string
		files []string
		want  []string
		not   []string
	}{
		{
			name:  "an any entry whose list does not name the path",
			files: []string{"internal/named/thing.go"},
			want:  []string{"./internal/alpha", "./internal/beta", "./internal/named"},
			not:   []string{"./internal/quiet"},
		},
		{
			name:  "an any entry the only changed path is ignored by",
			files: []string{"internal/beta/out/x.go"},
			want:  []string{"./internal/alpha"},
			not:   []string{"./internal/beta", "./internal/named", "./internal/quiet"},
		},
		{
			name:  "an any entry only one of the changed paths is ignored by",
			files: []string{"internal/beta/out/x.go", "internal/named/thing.go"},
			want:  []string{"./internal/alpha", "./internal/beta", "./internal/named"},
			not:   []string{"./internal/quiet"},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			out := selectIn(t, selectInput(root, []byte(selectTinyGraph), row.files...))
			if out.Full {
				t.Fatalf("Full = true: %s", out.Reason)
			}
			for _, pkg := range row.want {
				if !selectHasPkg(out, pkg) {
					t.Errorf("legs = %v, want %s to run", selectLegPkgs(out), pkg)
				}
			}
			for _, pkg := range row.not {
				if selectHasPkg(out, pkg) {
					t.Errorf("legs = %v, want %s not to run", selectLegPkgs(out), pkg)
				}
			}
		})
	}
	t.Run("every any entry of the committed map runs", func(t *testing.T) {
		// The same rule against the real map: each entry it marks `any` that
		// neither names this path nor steps out of it is one the run must hold.
		const changed = "internal/localcfg/localcfg.go"
		m, err := LoadReadMap(filepath.Join(selectRoot(t), readMapPath))
		if err != nil {
			t.Fatalf("read map: %v", err)
		}
		out := selectIn(t, selectRealInput(t, changed))
		if out.Full {
			t.Fatalf("Full = true: %s", out.Reason)
		}
		held := 0
		buildable := map[string]bool{}
		for _, line := range strings.Fields(*selectBuildable(t)) {
			if pkg, ok := repoPath(selectModule(t), line); ok {
				buildable[pkg] = true
			}
		}
		for _, p := range m.Packages {
			// A package with no file under the default tags has no leg to run in.
			if !buildable[p.Package] {
				continue
			}
			if !p.Any || readsAny(p, []string{changed}) || ignoredBy(p, changed) {
				continue
			}
			held++
			if !selectHasPkg(out, p.Package) {
				t.Errorf("legs = %v, want %s to run: the map marks it `any`", selectLegPkgs(out), p.Package)
			}
		}
		if held == 0 {
			t.Fatal("the committed map marks no package `any` outside this path, so this subtest says nothing")
		}
	})
}

func TestSelectAnEmptyLegSetSkipsTheShardJob(t *testing.T) {
	dir := selectWriteRoot(t, map[string]string{
		"go.mod":    "module example.com/m\n\ngo 1.24\n",
		readMapPath: `{"packages":[{"package":"./olds","reads":["a.txt"]}]}` + "\n",
		"a.txt":     "one\n",
	})
	selectGit(t, dir, "init", "-q")
	head := selectGitCommit(t, dir, "base")
	// Nothing changed since the full run: it already ran every test of this
	// tree, so this run has no legs to hand the shard job.
	out := selectIn(t, Input{
		Root: dir, Head: head, Base: head,
		GitHub:  &fakeGitHub{},
		Command: &fakeCommand{graph: []byte(selectTinyGraph), gitReal: true},
	})
	if out.Full {
		t.Fatalf("Full = true: %s", out.Reason)
	}
	if out.Legs == nil || len(out.Legs) != 0 {
		t.Fatalf("legs = %#v, want an empty, non-null set", out.Legs)
	}
	if !out.Check {
		t.Error("Check = false, want vet to run whatever the legs are")
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// GitHub fails a job whose matrix is empty, so the workflow reads this and
	// skips the shard job; the test job already counts a skipped shard as
	// success.
	if !strings.Contains(string(data), `"legs":[]`) {
		t.Errorf("json = %s, want legs to print as []", data)
	}
	if summary := Summary(out); !strings.Contains(summary, "skipped") {
		t.Errorf("summary = %q, want it to say the shard job is skipped", summary)
	}
}

func TestSelectTimingsDownloadFailureIsEqualWeights(t *testing.T) {
	files := "internal/rtdb/client.go"
	// The timings weigh the legs: a package far heavier than the rest stands
	// alone, which is how a test sees that they arrived.
	heavy := "10000\tgithub.com/dimipaun/fugaro/internal/rtdb\tTestTree\n"
	withTimes := selectFixtureGithub(t, &fakeGitHub{run: selectRun0(), times: heavy}, files)
	if !selectHasLegPkg(withTimes, "./internal/rtdb") {
		t.Errorf("legs = %v, want the heavy package alone in a leg", selectLegPkgs(withTimes))
	}
	equal := selectFixtureGithub(t, &fakeGitHub{run: selectRun0()}, files)
	failed := selectFixtureGithub(t, &fakeGitHub{run: selectRun0(), timesErr: errors.New("gh: no artifact")}, files)
	if failed.Full {
		t.Fatalf("Full = true: %s", failed.Reason)
	}
	if selectJSON(t, equal) != selectJSON(t, failed) {
		t.Errorf("a failed download packed differently:\n%s\n%s", selectJSON(t, equal), selectJSON(t, failed))
	}
}

func TestSelectTerraformAndRulesFromTheTaggedGraph(t *testing.T) {
	rows := []struct {
		name      string
		file      string
		terraform bool
		rules     bool
	}{
		{"a rules file", "internal/budget/rules/rules.json.tmpl", true, true},
		{"a root outside the module's own tests", "deploy/terraform/gcp/main.tf", true, true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			out := selectFixture(t, row.file)
			if out.Full {
				t.Fatalf("Full = true: %s", out.Reason)
			}
			if out.Terraform != row.terraform || out.Rules != row.rules {
				t.Errorf("jobs = terraform %v, rules %v, want %v and %v",
					out.Terraform, out.Rules, row.terraform, row.rules)
			}
		})
	}
	t.Run("a package only the build tags compile", func(t *testing.T) {
		// Its test file is behind -tags terraform, so the untagged listing does
		// not hold the package at all: the tagged graph is what makes a change
		// to the file reach it.
		const file = "ee/keyservice/terraform/infra_test.go"
		root := selectWriteRoot(t, map[string]string{
			"go.mod":    "module github.com/dimipaun/fugaro\n\ngo 1.24\n",
			readMapPath: `{"packages":[{"package":"./internal/other","reads":["` + file + `"]}]}` + "\n",
		})
		tagged := selectIn(t, selectInput(root, selectListing(t, BuildTags), file))
		if !selectHasPkg(tagged, "./ee/keyservice/terraform") {
			t.Errorf("legs = %v, want the tagged graph's package to run", selectLegPkgs(tagged))
		}
		plain := selectIn(t, selectInput(root, selectListing(t, ""), file))
		if selectHasPkg(plain, "./ee/keyservice/terraform") {
			t.Errorf("legs = %v, want the untagged graph to miss it", selectLegPkgs(plain))
		}
		if plain.Full || tagged.Full {
			t.Errorf("reasons = %q, %q, want both selected", tagged.Reason, plain.Reason)
		}
	})
}

func TestSelectAFullRunIsTheWholeMatrix(t *testing.T) {
	in := selectRealInput(t, "internal/rtdb/client.go")
	in.Full = true
	out := selectIn(t, in)
	if !out.Full {
		t.Fatalf("Full = false: %s", out.Reason)
	}
	// The workflow builds its matrix from this in both modes, so a full run has
	// to print ci.yml's whole one: the families whole, and as many packed legs
	// as the ceiling leaves.
	if got := len(out.Legs); got != DefaultMaxLegs {
		t.Errorf("legs = %d, want the full matrix's %d", got, DefaultMaxLegs)
	}
	for _, want := range []string{"cli-update", "cli-0", "cli-7", "scripts-0", "scripts-1", "scripts-live-5"} {
		if !selectHasLeg(out, want) {
			t.Errorf("legs = %v, want %s", selectLegNames(out), want)
		}
	}
}

func TestSelectWorkflowChangeIsFull(t *testing.T) {
	out := selectIn(t, selectRealInput(t, ".github/workflows/ci.yml"))
	if !out.Full {
		t.Fatalf("Full = false: %s", out.Reason)
	}
	if !strings.Contains(out.Reason, ".github/workflows/ci.yml") {
		t.Errorf("Reason = %q, want it to name the workflow", out.Reason)
	}
	// The matrix the workflow builds is the selector's own answer, so a full
	// run has to print one: the whole of it, families and all.
	if !selectHasLeg(out, "cli-update") || !selectHasLeg(out, "scripts-live-0") {
		t.Errorf("legs = %v, want the full matrix", selectLegNames(out))
	}
}

func TestSelectTestTimesComeFromTheLastFullRun(t *testing.T) {
	cmd := &fakeCommand{
		runList: `[{"databaseId":42,"headSha":"fullhead","displayTitle":"ci (full)"}]`,
		artifacts: map[string]string{
			"test-times-cli-0":  "3\tgithub.com/dimipaun/fugaro/internal/cli\tTestDocs\n",
			"test-times-rest-0": "12\tgithub.com/dimipaun/fugaro/internal/rtdb\tTestTree\n",
		},
	}
	gh := GHCommand{Command: cmd, Dir: "/checkout"}
	run, err := gh.LastFullRun(context.Background())
	if err != nil {
		t.Fatalf("LastFullRun: %v", err)
	}
	if run.ID != 42 {
		t.Fatalf("run = %+v, want the full one", run)
	}
	times, err := gh.TestTimes(context.Background(), run)
	if err != nil {
		t.Fatalf("TestTimes: %v", err)
	}
	for _, want := range []string{"TestDocs", "TestTree"} {
		if !strings.Contains(times, want) {
			t.Errorf("times = %q, want %s in them", times, want)
		}
	}
	if want := "gh run download 42 --pattern test-times-*"; !selectCalled(cmd, want) {
		t.Errorf("calls = %v, want %q", cmd.calls, want)
	}
}

func TestSelectSummary(t *testing.T) {
	full := Summary(Output{Full: true, Reason: ReasonScheduled, Legs: []Leg{{Name: "pack-0"}},
		Check: true, Terraform: true, Rules: true})
	for _, want := range []string{"full run", ReasonScheduled, "pack-0", "check", "terraform", "rules"} {
		if !strings.Contains(full, want) {
			t.Errorf("summary = %q, want %q", full, want)
		}
	}
	selected := Summary(selectFixture(t, "scripts/retry.sh"))
	for _, want := range []string{"selected run", "scripts-0"} {
		if !strings.Contains(selected, want) {
			t.Errorf("summary = %q, want %q", selected, want)
		}
	}
	if strings.Contains(selected, "terraform") {
		t.Errorf("summary = %q, want the jobs it does not run left out", selected)
	}
}

// --- the fixtures the tests above are built from ---

// fakeGitHub stands in for the gh CLI at the interface the selector holds.
type fakeGitHub struct {
	run      Run
	runErr   error
	times    string
	timesErr error
}

func (f *fakeGitHub) LastFullRun(context.Context) (Run, error) { return f.run, f.runErr }

func (f *fakeGitHub) TestTimes(context.Context, Run) (string, error) { return f.times, f.timesErr }

// fakeCommand answers the three programs the selector runs, and records every
// call: which base the diff names, and which argv the gh calls carry, are what
// the tests pin.
type fakeCommand struct {
	t *testing.T
	// graph is the `go list -deps -test -json` output and pkgs the `-f` one.
	graph []byte
	pkgs  string
	// buildable answers the default-tag `go list -f` as its lines; nil makes
	// every package of the graph buildable.
	buildable *string
	// diff answers git, unless gitReal runs the real git in the checkout the
	// input's Root names.
	diff    string
	gitReal bool
	// runList answers `gh run list`, artifacts `gh run download`: the
	// GHCommand is under test through the same commands it runs for real.
	runList   string
	artifacts map[string]string
	// failCall is a prefix of a call the command answers with an error.
	failCall string
	calls    []string
}

func (f *fakeCommand) Output(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if f.failCall != "" && strings.HasPrefix(call, f.failCall) {
		return nil, errors.New("command failed")
	}
	switch {
	case name == "go" && selectArg(args, 0) == "list" && selectArg(args, 1) == "-deps":
		return f.graph, nil
	case name == "go" && selectArg(args, 0) == "list" && selectArg(args, 1) == "-f" && !slices.Contains(args, "-tags"):
		if f.buildable != nil {
			return []byte(*f.buildable), nil
		}
		return graphImportPaths(f.graph), nil
	case name == "go" && selectArg(args, 0) == "list" && selectArg(args, 1) == "-f":
		return []byte(f.pkgs), nil
	case name == "git" && f.gitReal:
		return ExecCommand{}.Output(ctx, dir, name, args...)
	case name == "git":
		return []byte(f.diff), nil
	case name == "gh" && selectArg(args, 0) == "run" && selectArg(args, 1) == "list":
		return []byte(f.runList), nil
	case name == "gh" && selectArg(args, 0) == "run" && selectArg(args, 1) == "download":
		return nil, f.download(args)
	}
	f.t.Fatalf("unexpected command %s", call)
	return nil, nil
}

// download writes the artifacts into the --dir gh was given, one directory per
// artifact as gh lays them out.
func (f *fakeCommand) download(args []string) error {
	dir := ""
	for i, arg := range args {
		if arg == "--dir" && i+1 < len(args) {
			dir = args[i+1]
		}
	}
	if dir == "" {
		return errors.New("gh run download without --dir")
	}
	for name, body := range f.artifacts {
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sub, "test-times.tsv"), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func selectArg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// selectRoot is the checkout the goldens are measured against: this package's
// own module.
func selectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above this package")
		}
		dir = parent
	}
}

var (
	selectListingsMu sync.Mutex
	selectListings   = map[string][]byte{}
	selectPkgLists   = map[string]string{}
)

// selectListing is this module as the go tool lists it under a tag set. It is
// 18 MB and takes seconds, so each test binary captures it once and hands it to
// the fake command, which is the seam the selector really reads it through.
func selectListing(t *testing.T, tags string) []byte {
	t.Helper()
	selectListingsMu.Lock()
	defer selectListingsMu.Unlock()
	if out, ok := selectListings[tags]; ok {
		return out
	}
	args := []string{"list", "-deps", "-test", "-json"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./...")
	out, err := selectRun(t, selectRoot(t), "go", args...)
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	selectListings[tags] = out
	return out
}

// selectTestPackages is the `go list -f` answer a full run's matrix is built
// from: the packages of the module that have tests.
func selectTestPackages(t *testing.T, tags string) string {
	t.Helper()
	selectListingsMu.Lock()
	defer selectListingsMu.Unlock()
	if out, ok := selectPkgLists[tags]; ok {
		return out
	}
	out, err := selectRun(t, selectRoot(t), "go", "list", "-f", testPackagesFormat, "-tags", tags, "./...")
	if err != nil {
		t.Fatalf("go list -f: %v", err)
	}
	selectPkgLists[tags] = string(out)
	return string(out)
}

func selectRun(t *testing.T, dir, name string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.New(err.Error() + ": " + strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// selectRealInput is a run over the real checkout with a canned diff: the
// always-full paths, the doubts that need no map, and the full matrix.
func selectRealInput(t *testing.T, files ...string) Input {
	t.Helper()
	return Input{
		Root:    selectRoot(t),
		Head:    "headsha",
		Base:    "basesha",
		GitHub:  &fakeGitHub{run: selectRun0()},
		Command: &fakeCommand{graph: selectListing(t, BuildTags), pkgs: selectTestPackages(t, BuildTags), buildable: selectBuildable(t), diff: selectDiff(files...)},
	}
}

func selectBuildable(t *testing.T) *string {
	t.Helper()
	out, err := selectRun(t, selectRoot(t), "go", "list", "-f", "{{.ImportPath}}", "./...")
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	s := string(out)
	return &s
}

// selectFixture is a selection over this module's real map and graph, with the
// map's `any` flags cleared: what a change reaches is then the map's own
// answer, and not the "runs for anything" a walker's entry says.
func selectFixture(t *testing.T, files ...string) Output {
	t.Helper()
	return selectFixtureGithub(t, &fakeGitHub{run: selectRun0()}, files...)
}

func selectFixtureGithub(t *testing.T, gh GitHub, files ...string) Output {
	t.Helper()
	in := selectInput(selectMapRoot(t), selectListing(t, BuildTags), files...)
	in.GitHub = gh
	return selectIn(t, in)
}

func selectInput(root string, graph []byte, files ...string) Input {
	return Input{
		Root:    root,
		Head:    "headsha",
		Base:    "basesha",
		GitHub:  &fakeGitHub{run: selectRun0()},
		Command: &fakeCommand{graph: graph, pkgs: "", diff: selectDiff(files...)},
	}
}

func selectRun0() Run { return Run{ID: 1, Head: "basesha"} }

// selectMapRoot is a throwaway checkout of this module holding the committed
// read map with every `any` cleared. The graph and the diff still come from the
// real repository, so the test is about what the map reaches and nothing else.
func selectMapRoot(t *testing.T) string {
	t.Helper()
	root := selectRoot(t)
	module, err := modulePath(root)
	if err != nil {
		t.Fatalf("module path: %v", err)
	}
	m, err := LoadReadMap(filepath.Join(root, readMapPath))
	if err != nil {
		t.Fatalf("read map: %v", err)
	}
	for i := range m.Packages {
		m.Packages[i].Any = false
	}
	data, err := MarshalReadMap(m)
	if err != nil {
		t.Fatalf("marshal read map: %v", err)
	}
	return selectWriteRoot(t, map[string]string{
		"go.mod":    "module " + module + "\n\ngo 1.24\n",
		readMapPath: string(data),
	})
}

// selectWriteRoot writes a throwaway checkout.
func selectWriteRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// selectGit runs git in a throwaway repository, with the identity a commit
// needs spelled out so no test reads the machine's configuration.
func selectGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=ci-select", "-c", "user.email=ci-select@example.com",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func selectGitCommit(t *testing.T, dir, message string) string {
	t.Helper()
	selectGit(t, dir, "add", "-A")
	selectGit(t, dir, "commit", "-q", "-m", message)
	return selectGit(t, dir, "rev-parse", "HEAD")
}

func selectIn(t *testing.T, in Input) Output {
	t.Helper()
	out, err := Select(context.Background(), in)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	return out
}

func selectJSON(t *testing.T, out Output) string {
	t.Helper()
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func selectLegPkgs(out Output) []string {
	var pkgs []string
	for _, leg := range out.Legs {
		pkgs = append(pkgs, strings.Fields(leg.Pkg)...)
	}
	return pkgs
}

func selectLegNames(out Output) []string {
	names := make([]string, 0, len(out.Legs))
	for _, leg := range out.Legs {
		names = append(names, leg.Name)
	}
	return names
}

func selectHasPkg(out Output, pkg string) bool {
	for _, got := range selectLegPkgs(out) {
		if got == pkg {
			return true
		}
	}
	return false
}

// selectHasLegPkg reports whether a leg holds this package and nothing else.
func selectHasLegPkg(out Output, pkg string) bool {
	for _, leg := range out.Legs {
		if leg.Pkg == pkg {
			return true
		}
	}
	return false
}

func selectHasLeg(out Output, name string) bool {
	for _, leg := range out.Legs {
		if leg.Name == name {
			return true
		}
	}
	return false
}

func selectCalled(cmd *fakeCommand, prefix string) bool {
	for _, call := range cmd.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

func selectDiff(files ...string) string {
	if len(files) == 0 {
		return ""
	}
	return strings.Join(files, "\n") + "\n"
}

// graphImportPaths is the packages of the main module a listing names, one per
// line, test variants left out.
func graphImportPaths(listing []byte) []byte {
	var out strings.Builder
	dec := json.NewDecoder(bytes.NewReader(listing))
	for {
		var lp listedPkg
		if dec.Decode(&lp) != nil {
			break
		}
		if lp.Module == nil || !lp.Module.Main || lp.ForTest != "" || strings.Contains(lp.ImportPath, " [") || strings.HasSuffix(lp.ImportPath, ".test") {
			continue
		}
		out.WriteString(lp.ImportPath + "\n")
	}
	return []byte(out.String())
}

const tagOnlyModule = "example.com/tagonly"

// tagOnlyRepo is a module with a package whose only Go file, a test, is behind
// the terraform tag, beside one with a default test.
func tagOnlyRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":              "module " + tagOnlyModule + "\n\ngo 1.21\n",
		"plain/plain.go":      "package plain\n",
		"plain/plain_test.go": "package plain\n\nimport \"testing\"\n\nfunc TestPlain(t *testing.T) {}\n",
		"gated/gated_test.go": "//go:build terraform\n\npackage gated\n\nimport \"testing\"\n\nfunc TestGated(t *testing.T) {}\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSelectAFullRunLeavesATagGatedOnlyPackageOutOfTheLegs(t *testing.T) {
	root := tagOnlyRepo(t)
	legs, err := fullLegs(context.Background(), Input{Root: root}, root)
	if err != nil {
		t.Fatal(err)
	}
	var pkgs []string
	for _, l := range legs {
		pkgs = append(pkgs, strings.Fields(l.Pkg)...)
	}
	if want := []string{"./plain"}; !slices.Equal(pkgs, want) {
		t.Errorf("packages in legs = %q, want %q: go test names a package with no buildable file as a setup failure", pkgs, want)
	}
}

func TestSelectATagGatedFileStillSelectsTheJobsButNoDefaultLeg(t *testing.T) {
	const gatedListing = `{"ImportPath":"example.com/m/internal/infra/x","Name":"x",` +
		`"Dir":"/mod/internal/infra/x","Module":{"Path":"example.com/m","Main":true,"Dir":"/mod"},` +
		`"TestGoFiles":["x_test.go"]}` + "\n"
	root := selectWriteRoot(t, map[string]string{
		"go.mod":    "module example.com/m\n\ngo 1.24\n",
		readMapPath: `{"packages":[]}` + "\n",
	})
	empty := ""
	in := selectInput(root, []byte(gatedListing), "internal/infra/x/x_test.go")
	in.Command.(*fakeCommand).buildable = &empty
	out := selectIn(t, in)
	if out.Full {
		t.Fatalf("Full: %s", out.Reason)
	}
	if len(out.Legs) != 0 {
		t.Errorf("legs = %v, want none: the package has no default-tag file", selectLegNames(out))
	}
	if !out.Terraform {
		t.Error("Terraform = false, want the job its tree belongs to still selected")
	}
}

func selectModule(t *testing.T) string {
	t.Helper()
	module, err := modulePath(selectRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	return module
}
