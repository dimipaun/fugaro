package runner

// The pause's own refusal, from the inside: the runs a pause may not take at
// all. The pause's push is public in a way no other push of a run is — it
// carries whatever the agent left in the tree, to a branch a later run cuts a
// checkout from — so the modes whose branch is not the run's to advance must
// refuse it, and a run that took one would publish a state of the branch no run
// of the recipe meant to (checkpoint.go, digest.go T3.3, reports_commit.go R9,
// shadow.go A2.1).

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// pauseRefusalRun is the least a run needs to be refused a pause it may not
// take: a real checkout on a branch of its own, a record, and the run's own
// context. Nothing else is reached — a refusal happens before the pause writes,
// pushes or asks anything — which is what makes this a test of the refusal and
// not of the pause.
func pauseRefusalRun(t *testing.T) (*run, *gitops.Repo) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/"+refusalRunID); err != nil {
		t.Fatal(err)
	}
	r := &run{
		repo: repo,
		cfg:  &config.Config{},
		rec:  &runstore.Record{RunID: refusalRunID, Branch: "fugaro/" + refusalRunID},
		d:    Deps{Log: slog.New(slog.DiscardHandler), Now: time.Now},
	}
	return r, repo
}

const refusalRunID = "20260101-010101-1a2b"

// TestSuspendRefusesARunWithNoBranchOfItsOwn: every mode whose branch is not
// the run's to advance refuses the pause, and the run ends as a run whose
// credential ran out rather than publishing a checkpoint none of those modes
// meant to publish. The work session is a real one and the stage a stage a
// resume could enter, so the refusal each case reads is the branch's and no
// other.
func TestSuspendRefusesARunWithNoBranchOfItsOwn(t *testing.T) {
	const session = "4a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	for _, tc := range []struct {
		name     string
		noBranch func(*run)
	}{
		{"no checkout", func(r *run) { r.repo = nil }},
		{"an artifact run", func(r *run) { r.artifact = true }},
		{"a digest run", func(r *run) { r.digest = true }},
		{"a reports run", func(r *run) { r.commitReports = true }},
		{"a challenger arm", func(r *run) { r.shadow = true }},
		{"a review job", func(r *run) { r.review = true }},
		{"an evaluate run", func(r *run) { r.evaluate = true }},
		{"a rebase run", func(r *run) { r.rebase = true }},
		{"a replay run", func(r *run) { r.replay = refusalRunID }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, repo := pauseRefusalRun(t)
			tc.noBranch(r)
			work, why := r.pauseSession("implement", session)
			if why == "" {
				t.Fatalf("pauseSession took a pause for %s, naming session %q: the run would push a checkpoint it must not", tc.name, work)
			}
			if !strings.Contains(why, "no branch") {
				t.Errorf("the refusal does not say the run has no branch of its own: %q", why)
			}
			if r.repo == nil {
				// There is no checkout at all, so there is no push to watch:
				// the refusal above is the whole of what this case can show,
				// and Suspend reaches it through this same call.
				return
			}
			head := strings.TrimSpace(testutil.Git(t, repo.Dir, "rev-parse", "HEAD"))
			ex := exhaustedError{Until: time.Now().Add(time.Hour), Action: runstore.ActionPause, ResumeAt: time.Now().Add(2 * time.Hour)}
			err := Suspend(context.Background(), r, "implement", work, ex)
			var halt *HaltError
			if !errors.As(err, &halt) || halt.Halt.Reason != runstore.HaltNoCredential || halt.Halt.Scope != "run" {
				t.Fatalf("err = %v; want the halt a run whose credential ran out ends with", err)
			}
			if r.rec.PushedHead != "" {
				t.Errorf("the run pushed %s: a pause took a checkpoint for %s", r.rec.PushedHead, tc.name)
			}
			if after := strings.TrimSpace(testutil.Git(t, repo.Dir, "rev-parse", "HEAD")); after != head {
				t.Errorf("the run's HEAD moved to %s: the pause committed for %s", after, tc.name)
			}
		})
	}
}
