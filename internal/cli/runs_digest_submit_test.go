package cli

// fugaro runs digest --submit-draft: a redacted learning draft a person can
// file as a "Skill learning" issue (D327/D328). The command is the terminal
// half of the submission channel: it prints, and it sends nothing.
//
// The draft leaves the repository -- a person pastes it into a public issue --
// so it is redacted harder than a committed report: no repository name and no
// run ID, because both name this repository and its runs. The figures are the
// report's own, which carry no person and no task text (G1, D323).
//
// Every fixture here is of the placeholder repository owner/repo-a, which the
// fixture adds to the synthetic installation the cloud fixture builds.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// submitRepo is the invented repository every fixture here is of. The draft
// must carry neither it nor its name.
const submitRepo = "owner/repo-a"

// submitFixture makes the working directory a checkout of submitRepo, so that
// "this repository" resolves to a name the draft must leave out, and returns
// the fixture and the slug its bucket keeps submitRepo's runs under.
func submitFixture(t *testing.T) (*cloudFixture, string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/"+submitRepo+".git")
	t.Chdir(dir)
	f := newCloudFixture(t)
	f.appendConfig(t, "  "+submitRepo+": { provider: github, base_branch: main, workflows: [web] }\n")
	return f, mustSlug("github", submitRepo)
}

// seedSubmitRun writes one finished run of submitRepo into the fixture's
// bucket, under the slug submitFixture returned.
func seedSubmitRun(t *testing.T, f *cloudFixture, slug, id string, fin time.Time) {
	t.Helper()
	rec := &runstore.Record{
		Version: 1, RunID: id, Repo: submitRepo, Workflow: "web",
		Status:    runstore.StatusSucceeded,
		StartedAt: fin.Add(-time.Hour), FinishedAt: &fin,
	}
	if err := runstore.Open(statsBucket(t, f), slug, id).WriteRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

// TestSubmitDraftHasNoRepoNameOrRunID is the headline redaction: a draft
// leaves the repository for a public issue, so neither the repository's name
// nor a run ID is in it. The runs are renumbered instead, so the figures the
// lesson is about are still there to read.
func TestSubmitDraftHasNoRepoNameOrRunID(t *testing.T) {
	f, slug := submitFixture(t)
	digestClock(t, "2026-10-27")
	fin := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	seedSubmitRun(t, f, slug, "20261025-120000-abcd", fin)
	seedSubmitRun(t, f, slug, "20261025-130000-abcd", fin)

	out, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--submit-draft")
	if err != nil {
		t.Fatalf("runs digest --submit-draft: %v (%s)", err, errOut)
	}
	for _, absent := range []string{submitRepo, "repo-a", "20261025-120000-abcd", "20261025-130000-abcd"} {
		if strings.Contains(out, absent) {
			t.Errorf("the draft carries %q:\n%s", absent, out)
		}
	}
	// And no person: a record is untrusted input and the issue the draft is
	// pasted into is public, so an address or an account name reaching it is
	// the case this redaction exists for (G1, D323).
	for _, absent := range []string{"@", "person-1", "someone"} {
		if strings.Contains(out, absent) {
			t.Errorf("the draft carries %q:\n%s", absent, out)
		}
	}
	for _, want := range []string{
		"run-1",
		"run-2",
		// The one command that opens the issue form, run by the person, in a
		// browser (D327) -- against Fugaro's repository, because the form lives
		// there and `gh` would otherwise resolve the user's own from the
		// checkout this command read.
		"gh issue create --web -R " + updatecheck.Repo,
		// The form the person picks in the chooser --web opens: it is not a
		// --template flag, because --web ignores one, so the draft has to say
		// which form to select and where it lives.
		".github/ISSUE_TEMPLATE/skill_learning.yml",
		// The line the person replaces with the lesson.
		"<write the lesson here",
		"2026-10-25",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the draft does not carry %q:\n%s", want, out)
		}
	}
	// And no --template: `gh issue create --web` ignores it and opens the
	// chooser instead, so a draft carrying one would hand the person a flag
	// the browser drops and a form it never selected.
	if strings.Contains(out, "--template") {
		t.Errorf("the draft carries --template, which `gh issue create --web` ignores:\n%s", out)
	}
}

// TestSubmitDraftIsDeterministic is G3 for the draft: nothing in the renderer
// reads a clock, and every map it walks is walked in sorted key order, so the
// same records give byte-identical bytes. A person who prints a draft twice
// reads the same draft.
func TestSubmitDraftIsDeterministic(t *testing.T) {
	f, slug := submitFixture(t)
	digestClock(t, "2026-10-27")
	fin := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	seedSubmitRun(t, f, slug, "20261025-120000-abcd", fin)
	seedSubmitRun(t, f, slug, "20261025-130000-abcd", fin)

	print := func() string {
		out, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--submit-draft")
		if err != nil {
			t.Fatalf("runs digest --submit-draft: %v (%s)", err, errOut)
		}
		return out
	}
	if first, second := print(), print(); first != second {
		t.Errorf("the draft is not byte-identical when printed twice:\n%s\nwant:\n%s", second, first)
	}
}

// TestSubmitDraftSendsNothing is the other half of D327: the command reads the
// period's records like any report, and then prints -- it has no seam that
// sends a learning, no file to write it into, and no job to start. The one
// command that could send it is `gh`, so the proof is that `gh` is never run
// and that nothing outside stdout is touched.
func TestSubmitDraftSendsNothing(t *testing.T) {
	f, slug := submitFixture(t)
	dir := t.TempDir()
	digestClock(t, "2026-10-27")
	seedSubmitRun(t, f, slug, "20261025-120000-abcd", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))

	// A `gh` first on PATH that records every call: the file is absent until
	// the first invocation, so its absence is the proof that nothing ran.
	calls := testutil.FakeCommand(t, "gh", "exit 0\n")

	out, errOut, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--dir", dir, "--submit-draft")
	if err != nil {
		t.Fatalf("runs digest --submit-draft: %v (%s)", err, errOut)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Errorf("--submit-draft ran gh:\n%s", out)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("--submit-draft wrote %v (err %v), want nothing", entries, err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("--submit-draft started %d executions, want none", n)
	}
	if !strings.Contains(out, "Nothing has been sent") {
		t.Errorf("the draft does not say nothing was sent:\n%s", out)
	}
}

// TestSubmitDraftRefusesReportFlags: --submit-draft prints the draft instead of
// the report, so the flags that choose a report's shape or its destination have
// nothing to act on and are refused rather than silently ignored.
func TestSubmitDraftRefusesReportFlags(t *testing.T) {
	submitFixture(t)
	digestClock(t, "2026-10-27")

	for _, flag := range []string{"--stdout", "--json"} {
		_, _, err := execute(t, "runs", "digest", "--period", "day", "--date", "2026-10-25", "--submit-draft", flag)
		if err == nil {
			t.Errorf("--submit-draft accepted %s", flag)
			continue
		}
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("--submit-draft %s refused with %q, want the flag named", flag, err)
		}
	}
}
