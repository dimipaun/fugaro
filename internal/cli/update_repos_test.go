package cli

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// C1's tests are about the contract around the reader: the order of the list
// (and its memo), the hint, and the line. What reads the repositories is
// enrolledReposImpl, which a test stands in for here -- it is git and, after
// I1, the code host's API.

// stubEnrolledRepos stands impl in for the command's reader and restores the
// real one when the test ends.
func stubEnrolledRepos(t *testing.T, impl func(context.Context, enrolledIn) ([]repoView, []string)) {
	t.Helper()
	old := enrolledReposImpl
	t.Cleanup(func() { enrolledReposImpl = old })
	enrolledReposImpl = impl
}

// reposOf is the views' repositories, in the order they came.
func reposOf(views []repoView) []string {
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.Repo)
	}
	return out
}

// TestRepoViewOrderIsCanonical: whatever order the reader listed the
// repositories in, every stage and both output members see them in canonical
// owner/name order ("Zeta/app" sorts after "beta/x", which byte order would
// put last), and one run reads the list once (the memo). The caller's copy is
// its own: a stage that reordered it would reorder the run's.
func TestRepoViewOrderIsCanonical(t *testing.T) {
	calls := 0
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		calls++
		return []repoView{{Repo: "Zeta/app"}, {Repo: "beta/x"}, {Repo: "Acme/Web"}}, []string{"github/acme/*"}
	})
	want := []string{"Acme/Web", "beta/x", "Zeta/app"}

	state := &reposState{}
	first, patterns := enrolledRepos(context.Background(), enrolledIn{Memo: state})
	if got := reposOf(first); !slices.Equal(got, want) {
		t.Fatalf("the list came back as %v, want %v: the views are in canonical owner/name order whatever order the reader listed them in", got, want)
	}
	if !slices.Equal(patterns, []string{"github/acme/*"}) {
		t.Errorf("the admit patterns came back as %v, want the one the reader could not list: a pattern is reported, never expanded", patterns)
	}

	first[0].Repo = "mutated/by the caller"
	second, _ := enrolledRepos(context.Background(), enrolledIn{Memo: state})
	if got := reposOf(second); !slices.Equal(got, want) {
		t.Errorf("the second read of the run came back as %v, want %v: the run's list is its own, and a caller's edit must not reach it", got, want)
	}
	if calls != 1 {
		t.Errorf("the repositories were read %d times in one run, want 1: the list is memoized on the engine's state (e.repos)", calls)
	}

	// A caller with no memo (doctor, a test) reads fresh, and still in
	// canonical order.
	fresh, _ := enrolledRepos(context.Background(), enrolledIn{})
	if got := reposOf(fresh); !slices.Equal(got, want) {
		t.Errorf("a read with no memo came back as %v, want %v", got, want)
	}
	if calls != 2 {
		t.Errorf("a read with no memo made %d reads in all, want 2: without a memo nothing is remembered between calls", calls)
	}
}

// TestHintFirstMovesOnlyHumanLines: the hint puts the working directory's
// repository at the top of the human lines and moves nothing else -- the rest
// keep their order -- and it leaves the caller's slice alone, which is the
// one the --json document and the event stream are built from (they are never
// reordered). A hint no line names, or one already first, changes nothing.
func TestHintFirstMovesOnlyHumanLines(t *testing.T) {
	lines := []initflow.RepoLine{{Repo: "acme/app"}, {Repo: "beta/web"}, {Repo: "gamma/x"}}
	before := slices.Clone(lines)

	if got := hintFirst("Beta/Web", lines); !slices.Equal(got, []initflow.RepoLine{lines[1], lines[0], lines[2]}) {
		t.Errorf("hintFirst(beta/web) came back as %v, want the hinted line first and the rest in the order they came", got)
	}
	if !slices.Equal(lines, before) {
		t.Errorf("hintFirst changed the caller's lines: the --json document and the event stream are built from the same slice and keep the canonical order")
	}
	for _, hint := range []string{"", "delta/other", "acme/app"} {
		if got := hintFirst(hint, lines); !slices.Equal(got, lines) {
			t.Errorf("hintFirst(%q) came back as %v, want the lines as they stand: a hint that names no line, and one already first, move nothing", hint, got)
		}
	}
}

// TestUnreadRepoViewCarriesTheReasonAlone: the reader's two shapes are whole
// ones. A repository it could not read comes back with its reason and nothing
// else -- no branch, no head commit, no bytes, no parse, no extra files -- so
// Read() alone tells a stage whether there is anything to plan from, and a
// stage can never be handed a file the update must not act on, and the reason
// is one printable line whatever the repository's file held. The read shape is
// the complement: everything the reader read, and no reason.
func TestUnreadRepoViewCarriesTheReasonAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want string
		long bool
	}{
		{name: "no fugaro.yaml on the default branch", want: "no fugaro.yaml on the default branch"},
		{name: "a fugaro.yaml that does not validate",
			yaml: "version: 1\nproject: aurora\nnope: 1\n", want: "its fugaro.yaml is not valid"},
		// The parse's sentence about the file is the repository's text: it
		// quotes the field it refused, so a name the repository chose can be
		// as long as the file allows.
		{name: "a fugaro.yaml whose problem quotes more than a line may hold",
			yaml: "version: 1\nproject: aurora\n" + strings.Repeat("x", pluginwire.MaxPrintable+50) + ": 1\n",
			want: "its fugaro.yaml is not valid", long: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(repoCheckout(t, "https://github.com/acme/app.git", tc.yaml))

			views, patterns := enrolledReposImpl(context.Background(), enrolledIn{})
			if len(views) != 1 || len(patterns) != 0 {
				t.Fatalf("the reader answered %d views and %d patterns, want the one checkout and none", len(views), len(patterns))
			}
			v := views[0]
			if v.Read() {
				t.Fatalf("the view reads as read, want unread: %q", tc.want)
			}
			if !strings.Contains(v.Unread, tc.want) {
				t.Errorf("the reason came back as %q, want it to name %q", v.Unread, tc.want)
			}
			if v.Branch != "" || v.Head != "" {
				t.Errorf("the unread view carries the branch %q at %q, want neither: an unread view carries the reason alone", v.Branch, v.Head)
			}
			if v.YAML != nil || v.Cfg != nil || len(v.Files) != 0 {
				t.Errorf("the unread view carries bytes %q, a parse %+v and %d files, want none of them: a stage must not plan from a repository update cannot read", v.YAML, v.Cfg, len(v.Files))
			}
			// The reason is one printable line whatever the file held: a
			// stage prints it without newRepoLine (a notice, doctor's row),
			// and the parse's own sentence is not this program's text.
			for _, r := range v.Unread {
				if r < 0x20 || r == 0x7f || r == 0x202e {
					t.Errorf("the reason came back holding %q, want one printable line: %q", r, v.Unread)
				}
			}
			if n, max := len([]rune(v.Unread)), pluginwire.MaxPrintable+len("... (truncated)"); n > max {
				t.Errorf("the reason came back %d runes long, want at most %d: it is bounded", n, max)
			}
			if tc.long && !strings.HasSuffix(v.Unread, "(truncated)") {
				t.Errorf("a reason too long for a line came back as %q, want it cut and saying so", v.Unread)
			}
		})
	}

	t.Run("a repository that reads", func(t *testing.T) {
		t.Chdir(repoCheckout(t, "https://github.com/acme/app.git", noBaseBranchYAML))

		views, _ := enrolledReposImpl(context.Background(), enrolledIn{})
		if len(views) != 1 {
			t.Fatalf("the reader answered %d views, want the one checkout", len(views))
		}
		v := views[0]
		if !v.Read() {
			t.Fatalf("the view reads as unread (%q), want read", v.Unread)
		}
		if v.Branch != "main" || v.Head == "" {
			t.Errorf("the read view carries the branch %q at %q, want main and its commit", v.Branch, v.Head)
		}
		if len(v.YAML) == 0 || v.Cfg == nil {
			t.Errorf("the read view carries %d bytes of fugaro.yaml and the parse %+v, want both", len(v.YAML), v.Cfg)
		}
	})
}

// TestNewRepoLineSanitises: a line about a repository can quote a branch name,
// a path or a provider's own sentence, so the repository and the detail are
// made safe for one line of a terminal and bounded in length before anything
// prints them -- while the state, which is this program's own word, is not
// touched.
func TestNewRepoLineSanitises(t *testing.T) {
	line := newRepoLine("acme/\x1b[31mweb\r\n", initflow.RepoNeedsYou,
		"secret \x07 ‮gpj.txt is missing\nrun `fugaro secrets set`")
	if got, want := line.Repo, "acme/web??"; got != want {
		t.Errorf("the repository came back as %q, want %q: an escape sequence is dropped, and every other control becomes a printable one", got, want)
	}
	if got, want := line.Detail, "secret ? ?gpj.txt is missing?run `fugaro secrets set`"; got != want {
		t.Errorf("the detail came back as %q, want %q", got, want)
	}
	if line.State != initflow.RepoNeedsYou {
		t.Errorf("the state came back as %q, want %q: it is this program's own word and is not sanitised", line.State, initflow.RepoNeedsYou)
	}

	long := newRepoLine("acme/app", initflow.RepoDone, strings.Repeat("x", pluginwire.MaxPrintable+100))
	if got, want := len([]rune(long.Detail)), pluginwire.MaxPrintable+len("... (truncated)"); got != want {
		t.Errorf("a detail of %d runes came back %d runes long, want %d: the line is bounded", pluginwire.MaxPrintable+100, got, want)
	}
	if !strings.HasSuffix(long.Detail, "(truncated)") {
		t.Errorf("a long detail came back as %q, and a cut one says so", long.Detail)
	}
}

// TestReposNoticesAreTheStepsAlone: a stage and a step after the loop report
// through the same entry point (C1), and only the step's lines are the run's
// notices -- the document's `notices` member and the stream's `notice` lines.
// A stage's lines are on the stage's own result (`repos`) and printed under
// its header, so a reader that took them from both would count every
// repository twice (U13, R12).
func TestReposNoticesAreTheStepsAlone(t *testing.T) {
	e := &initEngine{r: &initRun{}}
	lines := []initflow.RepoLine{{Repo: "acme/app", State: initflow.RepoNeedsYou, Detail: "the pin"}}

	e.r.inStages = true
	e.reportRepos("plugin", lines)
	if got := e.notices(); len(got) != 0 {
		t.Fatalf("a stage's lines came back as the notices %+v, want none: a stage's lines are its own, and the loop reads them off the stage", got)
	}
	if len(e.reposState.stages) != 1 {
		t.Errorf("the stage's report was not recorded: the run keeps what a stage reported")
	}

	e.r.inStages = false
	e.reportRepos("backlog", lines)
	got := e.notices()
	if len(got) != 1 {
		t.Fatalf("the notices are %+v, want the one line the step reported: a step after the loop has no stage of its own", got)
	}
	if got[0].Step != "backlog" || got[0].Repo != "acme/app" || got[0].State != initflow.RepoNeedsYou || got[0].Detail != "the pin" {
		t.Errorf("the notice is %+v, want the step's own name on the line it reported", got[0])
	}
}

// TestTheLoopsGoThroughRunStages: a stage reports its per-repository lines
// through the same entry point a step does (C1), and the run tells the two
// apart by whether the stage loop is running (initRun.inStages, reportRepos).
// That is right only while every loop the run drives marks it — the converge,
// the wizard's questions, `apply`, `project enroll`, the accounts and the
// after-write door — so this pins the one place that may call the loop.
func TestTheLoopsGoThroughRunStages(t *testing.T) {
	var loops []string
	for _, path := range mustGlob(t, "*.go") {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "initflow.Run(") {
			loops = append(loops, path)
		}
	}
	if len(loops) != 1 || loops[0] != "init.go" {
		t.Errorf("the stage loop is called from %v, want init.go alone (runStages): a loop that does not mark the run as running files its stages' lines as a step's notices, and the document would carry them twice", loops)
	}
}
