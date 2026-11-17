package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/task"
)

// changeSetRepos are the two repositories a change set launch names, in the
// order --repo names them: the first is the one whose run ID is the set's own.
var changeSetRepos = []string{"acme/app", "acme/other"}

// changeSetRunIDs are the run IDs the fixture's generator hands the two
// members, in launch order: fixed here because a change set's ID is its first
// member's run, so a test that reads the set back has to know which run that
// is.
var changeSetRunIDs = []string{"20261116-140000-a1b2", "20261116-140001-c3d4"}

// changeSetBlock is the block a member's task carries, as runChangeSet writes
// it: the set, named by its first member's run, and both of its members — this
// run's repository and the sibling's, which is another repository's run and so
// not one this fixture ever seeds.
func changeSetBlock(memberRun string) *task.ChangeSet {
	return &task.ChangeSet{ID: memberRun, Members: []task.ChangeSetMember{
		{Repo: changeSetRepos[0], RunID: memberRun},
		{Repo: changeSetRepos[1], RunID: changeSetRunIDs[1]},
	}}
}

// changeSetFixture is the launch fixture with a second repository enrolled: a
// change set launches one run per repository, so a fixture with one repository
// cannot tell a set from an ordinary launch.
func changeSetFixture(t *testing.T) *launchBudgetFixture {
	t.Helper()
	f := newLaunchBudget(t, "enforce")
	enrollLaunchRepo(t, "acme/other")
	slug := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(slug, "web"), "2", "4Gi")
	// The second repository's image is a current release's, as the first
	// repository's is (newLaunchBudget): the launch gates a member's task on the
	// job image (changeSetSince), and an image older than this release would
	// refuse the set rather than judge what this test is about.
	writeBuildRecord(t, f.cloudFixture, slug, "web", releasePolicy)
	return f
}

// enrollLaunchRepo writes a repository into the fixture's own repos block. It
// inserts rather than appends: the block is not the file's last key -- the
// budget's is, which newLaunchBudget appends after it -- so an appended entry
// would land inside the budget block and the config would not decode.
func enrollLaunchRepo(t *testing.T, repo string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const key = "repos:\n"
	at := strings.Index(string(data), key)
	if at < 0 {
		t.Fatal("the fixture's config has no repos block to enroll a repository in")
	}
	at += len(key)
	entry := "  " + repo + ": { provider: github, workflows: [web] }\n"
	if err := os.WriteFile(path, []byte(string(data)[:at]+entry+string(data)[at:]), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixedRunIDs makes the fixture's launch mint changeSetRunIDs in order, and
// fails the test on a third call: a change set mints exactly one run ID per
// member, and a launcher that asked for another would be launching something
// this test does not know about.
func fixedRunIDs(t *testing.T) {
	t.Helper()
	orig := runIDGenerator
	calls := 0
	runIDGenerator = func() (string, error) {
		if calls >= len(changeSetRunIDs) {
			return orig()
		}
		id := changeSetRunIDs[calls]
		calls++
		return id, nil
	}
	t.Cleanup(func() { runIDGenerator = orig })
}

// launchChangeSet runs the change-set launch of changeSetFixture in this
// process, below the refusal every build makes at the flag itself
// (changeSetRefusal): the launch shape is what this file judges, and it is what
// the release that lifts the refusal reaches unchanged.
func launchChangeSet(t *testing.T, f *launchBudgetFixture, repos ...string) error {
	t.Helper()
	env, err := openCloud(context.Background(), cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Close)
	cmd := newRunCmd()
	return runChangeSet(context.Background(), cmd, env, &runOptions{repos: repos}, "Add a feature", 0)
}

// TestChangeSetMembersFixedAtLaunch: membership is fixed by the one launch that
// created the members (D642), so the whole member list is written into every
// member's task at launch and no later launch can join a set: the block is what
// a settle step reads to know which runs are the set, and a member that could
// add one would be a member widening a set nobody agreed to. The set's own ID
// is its first member's run, so a set is named by a run that exists.
func TestChangeSetMembersFixedAtLaunch(t *testing.T) {
	f := changeSetFixture(t)
	setMaxParallel(t, 5)
	fixedRunIDs(t)
	var calls []string
	f.run.OnRun = func(c gcpfake.RunCall) { calls = append(calls, c.Job) }

	if err := launchChangeSet(t, f, changeSetRepos...); err != nil {
		t.Fatalf("change set launch: %v", err)
	}
	if len(calls) != len(changeSetRepos) {
		t.Fatalf("%d executions were started, want %d (one run per repository)", len(calls), len(changeSetRepos))
	}
	specs := make([]*task.Spec, len(changeSetRepos))
	for i, repo := range changeSetRepos {
		specs[i] = readSpecOf(t, f.cloudFixture, mustSlug("github", repo), changeSetRunIDs[i])
		if specs[i].Repo != repo {
			t.Fatalf("member %d's task is of %s, want %s", i, specs[i].Repo, repo)
		}
		if specs[i].RunID != changeSetRunIDs[i] {
			t.Fatalf("member %d's task is run %s, want %s", i, specs[i].RunID, changeSetRunIDs[i])
		}
	}
	want := &task.ChangeSet{ID: changeSetRunIDs[0], Members: []task.ChangeSetMember{
		{Repo: changeSetRepos[0], RunID: changeSetRunIDs[0]},
		{Repo: changeSetRepos[1], RunID: changeSetRunIDs[1]},
	}}
	for i, spec := range specs {
		cs := spec.ChangeSet
		if cs == nil {
			t.Fatalf("member %d's task carries no change set: a member that names none is a run no settle step can find", i)
		}
		if cs.ID != want.ID || len(cs.Members) != len(want.Members) {
			t.Fatalf("member %d's change set = %+v, want %+v", i, cs, want)
		}
		for j, m := range want.Members {
			if cs.Members[j] != m {
				t.Fatalf("member %d's change set names %+v at %d, want %+v", i, cs.Members[j], j, m)
			}
		}
		if m, ok := cs.Member(spec.Repo); !ok || m.RunID != spec.RunID {
			t.Fatalf("member %d's own repository is not in its set's member list as its own run: %+v", i, cs.Members)
		}
	}
}

// TestChangeSetFollowUpCarriesTheBlock: a follow-up on a member's pull request
// resolves from the run it continues and carries that run's change set into the
// task it writes, members and all (resolvePR, newFollowUpSpec) — nothing on the
// launch names the set, since a follow-up's only door to a set is the run it
// follows. The block is what holds the follow-up's pull request a draft while
// the set is unfinished: a follow-up that dropped it would be the way to flip a
// held member ready on its own, from `fugaro run --pr N` (F15). The copy is
// what `fugaro run --pr N` must make without any --change-set flag, which this
// build refuses outright.
//
// Mutation (drop ChangeSet from the spec literal in newFollowUpSpec, or the
// `c.ChangeSet = prev.ChangeSet` that fills it): the follow-up's stored task
// carries no set and this test fails.
func TestChangeSetFollowUpCarriesTheBlock(t *testing.T) {
	f := newCloudFixture(t)
	// The image must be one whose runner reads the field: the inherited block
	// is what the launch is gated on (changeSetSince).
	writeBuildRecord(t, f, appSlug, "web", releasePolicy)
	root := firstRunSpec(rootID)
	root.ChangeSet = changeSetBlock(rootID)
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "tidy up"); err != nil {
		t.Fatalf("follow-up on a member's pull request: %v", err)
	}
	got := readSpec(t, f, fuID).ChangeSet
	want := changeSetBlock(rootID)
	if got == nil {
		t.Fatal("the follow-up's task carries no change set: the hold its member is under would not bind it, and the follow-up would flip the pull request ready")
	}
	if got.ID != want.ID || len(got.Members) != len(want.Members) {
		t.Fatalf("follow-up change set = %+v, want %+v", got, want)
	}
	for i, m := range want.Members {
		if got.Members[i] != m {
			t.Fatalf("follow-up change set names %+v at %d, want %+v", got.Members[i], i, m)
		}
	}
}

// TestChangeSetRefusesOneRepositoryTwice: a change set is one run per
// repository, and two --repo values that reduce to one repository are one
// member named twice — which would write a task whose own member list repeats
// it (task.Validate refuses that), after the members before it had already
// launched. The refusal is the launcher's, before anything is launched.
func TestChangeSetRefusesOneRepositoryTwice(t *testing.T) {
	f := changeSetFixture(t)
	setMaxParallel(t, 5)
	fixedRunIDs(t)
	err := launchChangeSet(t, f, "acme/app", "Acme/App.git")
	if err == nil {
		t.Fatal("a change set naming one repository twice was launched")
	}
	for _, want := range []string{"acme/app", "twice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions were started, want none: the duplicate is judged before the first member launches", n)
	}
	if storedTaskFor(t, f.cloudFixture, appSlug) {
		t.Error("a refused change set stored a task")
	}
}

// TestChangeSetGateTheJobImage: a member's task carries the set (change_set),
// a field an older runner refuses outright (task.Parse decodes with
// DisallowUnknownFields), so the launch is judged against the job image it
// would start on rather than started on a job that dies at bootstrap
// (changeSetSince). The gate is the field's and never the flag's: a launch that
// is in no set carries none, and is judged by nothing here.
func TestChangeSetGateTheJobImage(t *testing.T) {
	f := changeSetFixture(t)
	setMaxParallel(t, 5)
	fixedRunIDs(t)
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:"+shiftRelease(t, changeSetSince, -1))
	err := launchChangeSet(t, f, changeSetRepos...)
	if err == nil {
		t.Fatal("a change set was launched onto a job image older than the release that reads the field")
	}
	for _, want := range []string{"change set", changeSetSince, "acme/app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions were started, want none: the gate is judged before the task is written", n)
	}
	if storedTaskFor(t, f.cloudFixture, appSlug) {
		t.Error("a gated change set stored a task")
	}
}

// TestChangeSetLaunchesOnceSettleExists: the release that ships the settle step
// is the one that lifts D1's refusal. A set whose members' pull requests nothing
// could flip ready would stay in draft for good — the draft hold (D614) is the
// whole point of creating one — so a build without the step still refuses the
// flag, in D522's sentence, and a build with it launches the set (D616).
//
// The two answers are judged here together because only one of them is
// reachable through the command in any one build: a test binary of this package
// never loads ee/register (only cmd/fugaro does), so Edition() is core whatever
// tag the binary carries. The tagged launch is driven with the marker set the
// way ee/register's init sets it, so what the command does with the flag is
// judged and not just the sentence it prints.
func TestChangeSetLaunchesOnceSettleExists(t *testing.T) {
	core := changeSetRefusal(EditionCore)
	if !strings.Contains(core, "Subscription Edition") {
		t.Fatalf("the refusal an untagged build prints = %q, want D522's paid-feature sentence", core)
	}
	if tagged := changeSetRefusal(EditionEE); tagged != "" {
		t.Fatalf("a build that has the settle step still refuses --change-set: %q", tagged)
	}

	f := changeSetFixture(t)
	setMaxParallel(t, 5)
	fixedRunIDs(t)
	old := editionEE
	editionEE = true
	t.Cleanup(func() { editionEE = old })
	if _, _, err := execute(t, "run", "--change-set", "--repo", changeSetRepos[0], "--repo", changeSetRepos[1], "Add a feature"); err != nil {
		t.Fatalf("a build with the settle step refused the change set launch: %v", err)
	}
	for i, repo := range changeSetRepos {
		spec := readSpecOf(t, f.cloudFixture, mustSlug("github", repo), changeSetRunIDs[i])
		if spec.ChangeSet == nil || spec.ChangeSet.ID != changeSetRunIDs[0] {
			t.Fatalf("%s's task carries %+v, want the set named by the first member's run %s", repo, spec.ChangeSet, changeSetRunIDs[0])
		}
	}
}
