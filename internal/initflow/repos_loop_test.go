package initflow

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// reporting is a stage that does per-repository work: it hands the loop the
// lines C1's contract gives it (RepoReporter), which is the whole of what the
// loop has to read.
type reporting struct {
	*fake
	lines []RepoLine
}

func (s *reporting) RepoLines() []RepoLine { return s.lines }

// threeRepos is the spec's own example: three repositories, one of each state,
// which is the sentence a stage's detail becomes ("3 repositories: 1 done,
// 1 needs you, 1 unread", spec §Output).
func threeRepos() []RepoLine {
	return []RepoLine{
		{Repo: "acme/app", State: RepoDone, Detail: "stored 1 (git credential)"},
		{Repo: "acme/web", State: RepoNeedsYou, Detail: "<secret> is missing"},
		{Repo: "acme/old", State: RepoUnread, Detail: "the repository is gone (404)"},
	}
}

// TestLoopReadsRepoReporter: a stage that reports one line per repository has
// them carried into its result — the lines themselves, and the summary they
// make as the stage's own detail, which is the same sentence from every
// directory — and handed to the caller's OnRepos once, in the pass the run
// keeps. A stage that reports nothing is the run it always was.
func TestLoopReadsRepoReporter(t *testing.T) {
	lines := threeRepos()
	want := "3 repositories: 1 done, 1 needs you, 1 unread"

	t.Run("an applying run", func(t *testing.T) {
		var log []string
		s := &reporting{fake: &fake{name: "secrets", log: &log}, lines: lines}
		var handed [][]RepoLine
		opts := yesOpts()
		opts.OnRepos = func(l []RepoLine) { handed = append(handed, l) }
		res, err := Run(context.Background(), []Stage{s}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Stages) != 1 {
			t.Fatalf("the run settled %d stages, want 1", len(res.Stages))
		}
		got := res.Stages[0]
		if len(got.Repos) != len(lines) || got.Repos[0] != lines[0] || got.Repos[2] != lines[2] {
			t.Errorf("the stage's result carries %+v, want the lines the stage reported, in the order it reported them", got.Repos)
		}
		if got.Detail != want {
			t.Errorf("the stage's detail is %q, want %q: a stage that reports lines says what the lines make of it, and not the working directory's own reason", got.Detail, want)
		}
		if len(handed) != 1 {
			t.Fatalf("OnRepos was handed the lines %d times, want 1: the run's own account prints them once, and an applying run's review screen prints none of them", len(handed))
		}
		if len(handed[0]) != len(lines) || handed[0][0].Repo != "acme/app" {
			t.Errorf("OnRepos was handed %+v, want the stage's own lines", handed[0])
		}
	})

	t.Run("a plan-only run", func(t *testing.T) {
		var log []string
		s := &reporting{fake: &fake{name: "secrets", log: &log}, lines: lines}
		var handed int
		opts := yesOpts()
		opts.PlanOnly = true
		opts.OnRepos = func([]RepoLine) { handed++ }
		res, err := Run(context.Background(), []Stage{s}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if res.Stages[0].Detail != want || len(res.Stages[0].Repos) != 3 {
			t.Errorf("the plan-only run's row is %+v, want the lines and their summary like the applying run's", res.Stages[0])
		}
		if handed != 1 {
			t.Errorf("OnRepos was handed the lines %d times, want 1: a plan-only run's stages are its account, so its lines print there too", handed)
		}
	})

	t.Run("a stage that reports none", func(t *testing.T) {
		var log []string
		s := &fake{name: "preflight", log: &log, outDetail: "checked"}
		var handed int
		opts := yesOpts()
		opts.OnRepos = func([]RepoLine) { handed++ }
		res, err := Run(context.Background(), []Stage{s}, opts)
		if err != nil {
			t.Fatal(err)
		}
		if res.Stages[0].Repos != nil {
			t.Errorf("a stage that implements no RepoReporter came back with %+v, want no lines", res.Stages[0].Repos)
		}
		if res.Stages[0].Detail != "checked" {
			t.Errorf("the stage's detail came back as %q, want its own: only a stage that reported lines has it replaced by their summary", res.Stages[0].Detail)
		}
		if handed != 0 {
			t.Errorf("OnRepos was handed %d times for a stage that reports nothing, want none", handed)
		}
	})
}

// TestLoopRedactsRepoLines: a repository name and a detail can quote a value
// the run holds (F6), and a stage is not trusted to have redacted them: what
// reaches the document and the screen is redacted by the loop, as every other
// line of the run is.
func TestLoopRedactsRepoLines(t *testing.T) {
	const held = "AGENTSECRETVALUE"
	lines := []RepoLine{{Repo: "acme/app", State: RepoDone, Detail: "wrote " + held}}
	var log []string
	s := &reporting{fake: &fake{name: "secrets", log: &log}, lines: lines}
	opts := yesOpts()
	opts.Redact = []string{held}
	res, err := Run(context.Background(), []Stage{s}, opts)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Stages[0].Repos[0].Detail
	if strings.Contains(got, held) || !strings.Contains(got, "[redacted]") {
		t.Errorf("the line came back as %q, want the value the run holds redacted out of it", got)
	}
	if strings.Contains(res.Stages[0].Detail, held) {
		t.Errorf("the summary came back as %q, want it built from the redacted lines", res.Stages[0].Detail)
	}
}

// TestRepoSummaryCountsEveryLine: the summary is the lines' own count, spelled
// in the contract's state order, and the number always adds up to the lines —
// a state the contract does not have is counted rather than dropped, which is
// what keeps a stage's sentence honest about what it read.
func TestRepoSummaryCountsEveryLine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []RepoLine
		want  string
	}{
		{"the spec's own example", threeRepos(), "3 repositories: 1 done, 1 needs you, 1 unread"},
		{"one repository", []RepoLine{{Repo: "acme/app", State: RepoDone}}, "1 repository: 1 done"},
		{"the state order is the contract's", []RepoLine{
			{Repo: "a", State: RepoSkipped}, {Repo: "b", State: RepoUnread}, {Repo: "c", State: RepoNeedsYou}, {Repo: "d", State: RepoDone},
		}, "4 repositories: 1 done, 1 needs you, 1 unread, 1 skipped"},
		{"a state the contract does not have still counts", []RepoLine{
			{Repo: "a", State: RepoDone}, {Repo: "b", State: RepoState("half-way")},
		}, "2 repositories: 1 done, 1 skipped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := repoSummary(tc.lines); got != tc.want {
				t.Errorf("repoSummary(%+v) = %q, want %q", tc.lines, got, tc.want)
			}
		})
	}
}

// TestResultCarriesNotices: the steps after the stage loop report under their
// own name, and what carries them out is the document's `notices` member
// (R12, R19). It is absent for a run that reported none, so every run that
// wrote no notice writes the document it always wrote.
func TestResultCarriesNotices(t *testing.T) {
	none := &Result{}
	b, err := none.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("notices")) {
		t.Errorf("a run with no notices printed the member anyway:\n%s", b)
	}
	with := &Result{Notices: []Notice{{Step: "notices", Repo: "github/acme/*", State: RepoNeedsYou, Detail: "admitted by pattern, not listed"}}}
	b, err = with.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"notices"`, `"step": "notices"`, `"repo": "github/acme/*"`, `"state": "needs you"`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("the document is missing %s:\n%s", want, b)
		}
	}
}
