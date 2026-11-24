package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// launchSpecOf resolves the spec of a launched review of prs off the fixture's
// bucket, the way reviewSpec does, and returns the one warning line it wrote —
// "" when it wrote none.
func launchSpecOf(t *testing.T, f *cloudFixture, prs ...int) *task.ReviewSpec {
	t.Helper()
	rs, warn, err := resolveSpecOn(t, f, prs...)
	if err != nil {
		t.Fatalf("resolving the spec of %v: %v", prs, err)
	}
	if warn != "" {
		t.Fatalf("resolving the spec of %v warned: %s", prs, warn)
	}
	return rs
}

func resolveSpecOn(t *testing.T, f *cloudFixture, prs ...int) (*task.ReviewSpec, string, error) {
	t.Helper()
	env := fileEnv(t, f)
	var warn bytes.Buffer
	rs, err := resolveLaunchSpec(t.Context(), env, appSlug, launchSpecAsk{PRs: prs}, &warn)
	return rs, warn.String(), err
}

// seedLaunchRoot stores a first run id on PR pr that was asked for text,
// started at.
func seedLaunchRoot(t *testing.T, f *cloudFixture, id, text string, pr int, at time.Time) {
	t.Helper()
	spec := firstRunSpec(id)
	spec.Task = text
	seedLaunchRootSpec(t, f, spec, pr, at)
}

// seedLaunchRootSpec is seedLaunchRoot for a first run's task the caller
// builds itself: task.json is written once, so a spec carrying more than a
// text — a plan task, a recipe — is seeded in one call.
func seedLaunchRootSpec(t *testing.T, f *cloudFixture, spec *task.Spec, pr int, at time.Time) {
	t.Helper()
	seedSpec(t, f, spec, false)
	rec := prRecord(spec.RunID, "", pr, 1)
	rec.StartedAt = at
	writeRecord(t, f, spec.RunID, rec)
}

// seedLaunchFollowUp stores a follow-up of root on PR 7 whose own text is
// text, started at.
func seedLaunchFollowUp(t *testing.T, f *cloudFixture, id, root, text string, at time.Time) {
	t.Helper()
	spec := followUpSpec(id, root, root, 7)
	spec.Task = text
	seedLaunchFollowUpSpec(t, f, spec, at)
}

// seedLaunchFollowUpSpec is seedLaunchFollowUp for a follow-up's task the
// caller builds itself, so one carrying from_findings and own_text is seeded
// in one call.
func seedLaunchFollowUpSpec(t *testing.T, f *cloudFixture, spec *task.Spec, at time.Time) {
	t.Helper()
	seedSpec(t, f, spec, false)
	rec := prRecord(spec.RunID, "", 7, 0.5)
	rec.Branch, rec.StartedAt = "fugaro/"+spec.PreviousRun, at
	rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: spec.PreviousRun}
	writeRecord(t, f, spec.RunID, rec)
}

// wantLaunchSpecNone wants rs to be a spec of no spec with reason.
func wantLaunchSpecNone(t *testing.T, rs *task.ReviewSpec, reason string) {
	t.Helper()
	if rs == nil || rs.Source != task.ReviewSpecNone || rs.Reason != reason {
		t.Fatalf("review spec = %+v, want source none with reason %q", rs, reason)
	}
	if rs.Text != "" || rs.Followup != "" || rs.Run != "" {
		t.Fatalf("review spec = %+v, want a none source to carry no text, follow-up or run", rs)
	}
}

// TestLaunchSpecFromBucketRows is R1's core: a review launched on a pull
// request a run opened carries the task that run was asked for, the latest
// follow-up's text beside it and the plan task the root names — resolved by
// the launcher, through the runs bucket, and written into task.json for the
// review job, which reads no other run.
//
// Mutation (drop the resolveLaunchSpec call from reviewSpec): task.json
// carries no review_spec at all and this test fails on its first assertion.
func TestLaunchSpecFromBucketRows(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+reviewSpecSince)
	plan := firstRunSpec(rootID)
	plan.Task = "Rename the gadget\nand update its docs"
	plan.PlanTask = "review-against-task/RA5a"
	seedLaunchRootSpec(t, f, plan, 7, time.Now().Add(-72*time.Hour))
	wait := "20261116-100000-bbbb"
	seedLaunchFollowUp(t, f, wait, rootID, "Also rename the widget", time.Now().Add(-48*time.Hour))

	// The launch itself, not the resolver: what the job reads is task.json.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "review-only", "--pr", "7", "--run-id", "20261119-100000-abcd"); err != nil {
		t.Fatalf("launching a review of PR 7: %v", err)
	}
	spec := readSpec(t, f, "20261119-100000-abcd")
	rs := spec.ReviewSpec
	if rs == nil {
		t.Fatal("the launched review carries no review_spec")
	}
	if rs.Source != task.ReviewSpecRun || rs.Run != rootID {
		t.Fatalf("review spec = %+v, want the root run %s", rs, rootID)
	}
	if rs.Text != "Rename the gadget\nand update its docs" {
		t.Fatalf("review spec text = %q, want the root run's task", rs.Text)
	}
	if rs.Followup != "Also rename the widget" {
		t.Fatalf("review spec follow-up = %q, want the latest follow-up's text", rs.Followup)
	}
	if rs.PlanTask != "review-against-task/RA5a" {
		t.Fatalf("review spec plan task = %q, want the root task's", rs.PlanTask)
	}
}

// TestLaunchSpecIgnoresSpoofedHeadBranch is F22: a head ref name is whoever
// opened the pull request's choice, so a pull request from a branch named
// after another run must not make the reviewer judge against that run's task.
// The root comes from the rows alone, and the fake forge below answers with a
// branch naming a run that really exists and really wrote something else: the
// answer the resolver must never act on.
//
// Mutation (resolve the root from the forge's head ref instead of the rows'
// branch, as the pre-R1 reading did): the spec names the other run's id and
// this test fails.
func TestLaunchSpecIgnoresSpoofedHeadBranch(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+reviewSpecSince)
	seedLaunchRoot(t, f, rootID, "Rename the gadget", 7, time.Now().Add(-72*time.Hour))
	// The run whose name the pull request's opener borrowed: a real run, of
	// this repository, that opened another pull request and was asked for
	// something else. Nothing but the head ref name connects it to PR 7.
	spoofed := wallRunIDAt(2, "080000", "dead")
	seedLaunchRoot(t, f, spoofed, "Clean the widget", 9, time.Now().Add(-48*time.Hour))

	rs := launchSpecOf(t, f, 7)
	if rs.Source != task.ReviewSpecRun || rs.Run != rootID {
		t.Fatalf("review spec = %+v, want the root the rows recorded (%s) and never %s", rs, rootID, spoofed)
	}
	if rs.Text != "Rename the gadget" {
		t.Fatalf("review spec text = %q, want the task PR 7 was written for", rs.Text)
	}
}

// TestLaunchSpecOrphanIsNone: a pull request no run of this repository
// produced — a person's own, or one whose runs fell out of the lookback — is
// reviewed code-only, with the reason (R1, D2933's shape for an orphan). So is
// a pull request whose rows name no one branch, or name a run whose own record
// places it elsewhere: the bucket cannot name one root run, and a review is
// never refused over that — one line says which rows or which record it was.
//
// Mutation (treat a row whose branch is not a Fugaro branch as a root): the
// second case resolves a spec instead of none, and this test fails.
func TestLaunchSpecOrphanIsNone(t *testing.T) {
	t.Run("no runs at all", func(t *testing.T) {
		f := newCloudFixture(t)
		wantLaunchSpecNone(t, launchSpecOf(t, f, 7), launchSpecNoRun)
	})
	t.Run("a pull request of the repository's own, not Fugaro's", func(t *testing.T) {
		f := newCloudFixture(t)
		// A run that continues PR 7 on a branch Fugaro never minted: the
		// shape a person's pull request has when a follow-up was launched at
		// it, and no run of the repository opened it.
		spec := followUpSpec("20261116-100000-bbbb", "somebody", "somebody", 7)
		spec.Branch = "feature/gadget"
		seedSpec(t, f, spec, false)
		rec := prRecord("20261116-100000-bbbb", "", 7, 0.5)
		rec.Branch = "feature/gadget"
		writeRecord(t, f, "20261116-100000-bbbb", rec)
		wantLaunchSpecNone(t, launchSpecOf(t, f, 7), launchSpecNoRun)
	})
	t.Run("rows that name different branches", func(t *testing.T) {
		f := newCloudFixture(t)
		// Two runs with records of their own, each naming PR 7 and a branch
		// of its own: the state a forged row or a stale record leaves behind.
		seedLaunchRoot(t, f, wallRunIDAt(3, "090000", "aaaa"), "Rename the gadget", 7, time.Now().Add(-72*time.Hour))
		seedLaunchRoot(t, f, wallRunIDAt(2, "100000", "bbbb"), "Rename the gadget", 7, time.Now().Add(-48*time.Hour))
		rs, warn, err := resolveSpecOn(t, f, 7)
		if err != nil {
			t.Fatal(err)
		}
		wantLaunchSpecNone(t, rs, launchSpecNoRun)
		if !strings.Contains(warn, "different branches") {
			t.Fatalf("stderr = %q, want one line saying the rows disagree on the branch", warn)
		}
	})
	t.Run("a run whose own record is on another branch", func(t *testing.T) {
		f := newCloudFixture(t)
		// root opened PR 9 on a branch naming another run; a run that never
		// launched continues PR 7 on the branch that names root, so PR 7's
		// rows point at a run whose record says it is not the run that opened
		// this pull request (checkRoot's own comparison).
		root := wallRunIDAt(3, "090000", "aaaa")
		seedLaunchRoot(t, f, root, "Rename the gadget", 9, time.Now().Add(-90*time.Hour))
		rec := prRecord(root, "", 9, 1)
		rec.Branch, rec.StartedAt = "fugaro/20261115-090000-cccc", time.Now().Add(-90*time.Hour)
		writeRecord(t, f, root, rec)
		seedSpec(t, f, followUpSpec("20261118-100000-bbbb", root, root, 7), false)
		rs, warn, err := resolveSpecOn(t, f, 7)
		if err != nil {
			t.Fatal(err)
		}
		wantLaunchSpecNone(t, rs, launchSpecNoRun)
		if !strings.Contains(warn, "records branch") {
			t.Fatalf("stderr = %q, want one line naming the branch the run's record holds", warn)
		}
	})
}

// TestLaunchSpecFollowUpText: the latest follow-up's text is what the pull
// request was last asked to do, and it is added beside the root task (R1). A
// follow-up whose task the bucket has lost contributes nothing and says so.
func TestLaunchSpecFollowUpText(t *testing.T) {
	f := newCloudFixture(t)
	seedLaunchRoot(t, f, rootID, "Rename the gadget", 7, time.Now().Add(-72*time.Hour))
	first := wallRunIDAt(3, "100000", "1111")
	latest := wallRunIDAt(2, "110000", "2222")
	seedLaunchFollowUp(t, f, first, rootID, "the first follow-up", time.Now().Add(-72*time.Hour))
	seedLaunchFollowUp(t, f, latest, rootID, "the latest follow-up", time.Now().Add(-48*time.Hour))
	rs := launchSpecOf(t, f, 7)
	if rs.Followup != "the latest follow-up" {
		t.Fatalf("follow-up text = %q, want the latest follow-up's", rs.Followup)
	}

	// The same fixture with the latest follow-up's task.json gone: the review
	// still runs, against the root and the older follow-up's text.
	removeTaskObject(t, f, latest)
	rs, warn, err := resolveSpecOn(t, f, 7)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Followup != "the first follow-up" {
		t.Fatalf("follow-up text = %q, want the older follow-up's once the latest has no task", rs.Followup)
	}
	if !strings.Contains(warn, "warning:") || !strings.Contains(warn, task.ShortRun(latest)) {
		t.Fatalf("stderr = %q, want one line naming %s", warn, task.ShortRun(latest))
	}
}

// TestLaunchSpecFindingsFollowUpOwnTextOnly: a follow-up launched from a
// review's findings has a reviewer's report in its text, and the review of
// the pull request it wrote must quote the person's own words alone (F25,
// F30). The task of such a follow-up is the findings followed by them.
//
// Mutation (quote Spec.Task instead of Spec.OwnText when FromFindings is
// set): the reviewer's report reaches the spec and this test fails.
func TestLaunchSpecFindingsFollowUpOwnTextOnly(t *testing.T) {
	f := newCloudFixture(t)
	seedLaunchRoot(t, f, rootID, "Rename the gadget", 7, time.Now().Add(-72*time.Hour))
	id := wallRunIDAt(2, "110000", "2222")
	fu := followUpSpec(id, rootID, rootID, 7)
	fu.Task = "the reviewer said the lock is released early\n\nalso rename the widget"
	fu.FromFindings = true
	fu.OwnText = "also rename the widget"
	seedLaunchFollowUpSpec(t, f, fu, time.Now().Add(-48*time.Hour))

	rs := launchSpecOf(t, f, 7)
	if rs.Followup != "also rename the widget" {
		t.Fatalf("follow-up text = %q, want the person's own words alone", rs.Followup)
	}
}

// TestLaunchSpecOwnStepRootIsNone: a report run's task text is a launcher
// label rather than a brief ("periodic run review"), so a review of the pull
// request it opened judges the code alone and says why (R10, F13).
//
// Mutation (drop the ownStepRecipe call): the spec quotes the label and this
// test fails.
func TestLaunchSpecOwnStepRootIsNone(t *testing.T) {
	for _, name := range []string{"run-digest", "commit-reports"} {
		t.Run(name, func(t *testing.T) {
			f := newCloudFixture(t)
			yaml, ok := recipe.CatalogText(name)
			if !ok {
				t.Fatalf("the catalog has no recipe %q", name)
			}
			spec := firstRunSpec(rootID)
			spec.Task = "periodic run review"
			spec.Recipe = &task.Recipe{Name: name, Source: string(recipe.SourceCatalog), SHA256: recipe.Sum(yaml), YAML: string(yaml)}
			seedLaunchRootSpec(t, f, spec, 7, time.Now().Add(-72*time.Hour))
			wantLaunchSpecNone(t, launchSpecOf(t, f, 7), launchSpecOwnStep)
		})
	}
}

// TestLaunchSpecPickSharedAndDiffering is R9: a pick run reviews several pull
// requests with one spec, so several that were written for the same task give
// that one spec, and several that were not give none with the reason.
//
// Mutation (write the first pull request's spec without comparing the
// others): the differing case resolves a spec and this test fails.
func TestLaunchSpecPickSharedAndDiffering(t *testing.T) {
	const shared = "Rename the gadget"
	t.Run("the same task", func(t *testing.T) {
		f := newCloudFixture(t)
		seedLaunchRoot(t, f, rootID, shared, 7, time.Now().Add(-72*time.Hour))
		other := wallRunIDAt(3, "091000", "bbbb")
		seedLaunchRoot(t, f, other, shared, 8, time.Now().Add(-72*time.Hour))
		rs := launchSpecOf(t, f, 7, 8)
		if rs.Source != task.ReviewSpecRun || rs.Text != shared {
			t.Fatalf("review spec = %+v, want the one task both pull requests were written for", rs)
		}
	})
	t.Run("different tasks", func(t *testing.T) {
		f := newCloudFixture(t)
		seedLaunchRoot(t, f, rootID, shared, 7, time.Now().Add(-72*time.Hour))
		other := wallRunIDAt(3, "091000", "bbbb")
		seedLaunchRoot(t, f, other, "Fix the flaky test", 8, time.Now().Add(-72*time.Hour))
		wantLaunchSpecNone(t, launchSpecOf(t, f, 7, 8), launchSpecPick)
		// The same two pull requests through the launch that reviews several
		// at once: what the job reads is one task carrying the none, so the
		// rule holds at the launch and not only in the resolver.
		writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+reviewSpecSince)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "pick", "--pr", "7", "--pr", "8", "--run-id", "20261119-100000-abcd"); err != nil {
			t.Fatalf("launching a pick review of PRs 7 and 8: %v", err)
		}
		spec := readSpec(t, f, "20261119-100000-abcd").ReviewSpec
		wantLaunchSpecNone(t, spec, launchSpecPick)
	})
}

// TestLaunchSpecClippedAndRedacted: every text a review's spec carries is
// bounded by MaxReviewSpecBytes and redacted with redact.Text, so what the
// launcher writes is what Validate accepts and no credential a brief happens
// to quote reaches the reviewer (R4, R13). A stored task cannot be trimmed,
// so a long one is clipped at a line with a note rather than refused.
//
// Mutation (write the task text as it is): the spec is longer than the cap
// and this test fails; mutation (drop the redact.Text call): the token is in
// the spec and its subtest fails.
func TestLaunchSpecClippedAndRedacted(t *testing.T) {
	// Built here rather than written as a literal: no secret-shaped string
	// belongs in a test's source.
	token := "ghp_" + strings.Repeat("a1B", 12)
	long := strings.Repeat("a line of the brief\n", 2000) // 40000 bytes, over the 16 KiB cap
	f := newCloudFixture(t)
	// The token stands at the front of a brief that is then clipped: what is
	// redacted is the whole text, and what is dropped is its end, so a marker
	// near the end would be clipped away with the rest and prove nothing.
	seedLaunchRoot(t, f, rootID, "the token is "+token+"\n"+long, 7, time.Now().Add(-72*time.Hour))
	id := wallRunIDAt(2, "110000", "2222")
	seedLaunchFollowUp(t, f, id, rootID, "the token is "+token+"\n"+long, time.Now().Add(-48*time.Hour))

	rs := launchSpecOf(t, f, 7)
	for what, text := range map[string]string{"text": rs.Text, "followup": rs.Followup} {
		if len(text) > task.MaxReviewSpecBytes {
			t.Errorf("%s is %d bytes, over the cap of %d", what, len(text), task.MaxReviewSpecBytes)
		}
		if !strings.Contains(text, "was dropped]") {
			t.Errorf("%s carries no note that it was clipped:\n%s", what, text)
		}
	}
	if strings.Contains(rs.Text+rs.Followup, token) {
		t.Error("the spec carries the token the brief quotes")
	}
	if !strings.Contains(rs.Text, redactedMarker) {
		t.Error("the spec carries no [REDACTED] where the token was")
	}
}

// redactedMarker is what redact.Text substitutes for a credential. It is
// asserted here rather than imported, so a test that wants the shape reads
// the same string a person reading the spec would see.
const redactedMarker = "[REDACTED]"

// TestLaunchSpecOldImageWarnsAndOmits is D2933: an image older than
// reviewSpecSince cannot read the fields, so the launcher writes none of them
// and says so in one line naming `fugaro update` — the whole upgrade that
// rebuilds the image — and the review runs code-only. An image that is not
// older writes them, which is what makes this a gate on the image rather than
// a launch that never carries a spec.
//
// Mutation (write the fields whatever the image is): the first subtest fails,
// finding a review_spec the image cannot read; mutation (refuse the launch
// instead of dropping the fields): the first subtest fails on the launch's
// own error.
func TestLaunchSpecOldImageWarnsAndOmits(t *testing.T) {
	launch := func(t *testing.T, base string) (*task.Spec, string) {
		t.Helper()
		f := newCloudFixture(t)
		writeBuildRecord(t, f, appSlug, "web", base)
		seedLaunchRoot(t, f, rootID, "Rename the gadget", 7, time.Now().Add(-72*time.Hour))
		_, errOut, err := execute(t, "run", "--repo", "acme/app", "--recipe", "review-only", "--pr", "7", "--run-id", "20261119-100000-abcd")
		if err != nil {
			t.Fatalf("launching a review on %s: %v", base, err)
		}
		return readSpec(t, f, "20261119-100000-abcd"), errOut
	}
	t.Run("an image older than reviewSpecSince", func(t *testing.T) {
		spec, errOut := launch(t, "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, reviewSpecSince, -1))
		if spec.ReviewSpec != nil || spec.FromFindings || spec.OwnText != "" {
			t.Fatalf("spec carries %+v, want none of the three fields on an image that cannot read them", spec.ReviewSpec)
		}
		lines := 0
		for _, line := range strings.Split(errOut, "\n") {
			if strings.Contains(line, "judges the code alone") {
				lines++
			}
		}
		if lines != 1 || !strings.Contains(errOut, reviewSpecSince) || !strings.Contains(errOut, "fugaro update") {
			t.Fatalf("stderr = %q, want one line naming %s and fugaro update", errOut, reviewSpecSince)
		}
	})
	t.Run("an image at reviewSpecSince", func(t *testing.T) {
		spec, errOut := launch(t, "ghcr.io/dimipaun/fugaro-base:"+reviewSpecSince)
		if spec.ReviewSpec == nil || spec.ReviewSpec.Source != task.ReviewSpecRun || spec.ReviewSpec.Run != rootID {
			t.Fatalf("review spec = %+v, want the root task", spec.ReviewSpec)
		}
		if strings.Contains(errOut, "judges the code alone") {
			t.Fatalf("stderr = %q, want no drop note on an image that reads the fields", errOut)
		}
	})
}

// TestFollowUpFromReviewRunMarksFindings: a follow-up launched from a
// review's findings carries the two fields that say so — from_findings and
// own_text, the person's own TEXT — because the task's text is the findings
// followed by it, and a review of the pull request it opens must quote the
// person's words alone (R2, F30).
//
// Mutation (drop the from_findings/own_text write): this test fails on its
// first assertion.
func TestFollowUpFromReviewRunMarksFindings(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	// The follow-up carries its chain's title, so the image has to be one that
	// reads titles; it also has to read the review's spec, or the launch drops it.
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+reviewSpecSince)
	putReviewArtifact(t, f, reviewID, fixFirstArtifact(reviewID))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--review-run", reviewID, "also rename x"); err != nil {
		t.Fatalf("a fix round carrying the findings of %s: %v", reviewID, err)
	}
	spec := readSpec(t, f, fuID)
	if !spec.FromFindings {
		t.Error("the follow-up does not record that it was launched from a review's findings")
	}
	if spec.OwnText != "also rename x" {
		t.Errorf("own_text = %q, want the person's own text", spec.OwnText)
	}
	if !strings.Contains(spec.Task, "the lock is released before the write") {
		t.Errorf("the follow-up's brief does not carry the findings it was launched from:\n%s", spec.Task)
	}
	// What a review of the pull request this follow-up opens is handed: the
	// person's words, and none of the reviewer's report.
	if got := followUpSpecText(spec); got != "also rename x" {
		t.Errorf("the follow-up contributes %q, want the person's own text alone", got)
	}
}

// TestFollowUpFromReviewRunOldImageOmits is D2933 at the other writer: a
// follow-up whose job image predates the fields carries neither, and the one
// line names `fugaro update`. The launch is not refused, and the findings —
// which are the task's own text — still reach the brief.
//
// Mutation (write the fields whatever the image is): this test fails, finding
// from_findings on a task the image refuses at bootstrap.
func TestFollowUpFromReviewRunOldImageOmits(t *testing.T) {
	f, reviewID := fixBriefSeed(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, reviewSpecSince, -1))
	putReviewArtifact(t, f, reviewID, fixFirstArtifact(reviewID))
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--review-run", reviewID, "also rename x")
	if err != nil {
		t.Fatalf("a fix round carrying the findings of %s: %v", reviewID, err)
	}
	spec := readSpec(t, f, fuID)
	if spec.FromFindings || spec.OwnText != "" {
		t.Fatalf("from_findings = %v, own_text = %q; want neither on an image that cannot read them", spec.FromFindings, spec.OwnText)
	}
	if !strings.Contains(spec.Task, "the lock is released before the write") {
		t.Errorf("the brief does not carry the findings:\n%s", spec.Task)
	}
	if !strings.Contains(errOut, "judges the code alone") || !strings.Contains(errOut, "fugaro update") {
		t.Fatalf("stderr = %q, want one line naming fugaro update", errOut)
	}
}

// removeTaskObject deletes run id's task.json, the object a row's branch
// names: the state a bucket holds for a run whose task was never written, or
// is no longer there.
func removeTaskObject(t *testing.T, f *cloudFixture, id string) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Delete(ctx, "runs/"+appSlug+"/"+id+"/task.json"); err != nil {
		t.Fatal(err)
	}
}
