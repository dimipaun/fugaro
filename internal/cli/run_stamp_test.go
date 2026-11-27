package cli

// run_stamp_test.go is VS2 of docs/ground-rules: the launcher stamps every task
// it writes with the release it ran as (task.Spec.CLIVersion, D3441), so the
// runner can upgrade itself in place to exactly that release before it runs the
// task (spec.md §6, VS5). A development build has no release to name and stamps
// nothing (D3449).
//
// The stamp is written in createTask, the one place any launch path stores a
// task, so what these cases pin is that each path really goes through it: the
// command, a --pr follow-up, a review, the orchestrator and a cloud diagnosis
// are driven here through their own entry points, and the task each one stored
// is read back from the bucket. The repeat case is the other half of D3441: a
// repeated --run-id judged on the caller's request alone, never on which
// release happened to run the repeat.

import (
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// stampRelease is the release every case below launches as: a plain X.Y.Z, the
// shape task.releaseVersionRE and releaseRE both carry. No case asserts it
// anywhere the CLI's own version reaches a wire format, so the number is
// arbitrary.
const stampRelease = "0.128.0"

// stampLaunchFixture is the installation a plain `fugaro run` launches into:
// the one TestRunEmbedsTheProjectLayer uses, minus the layer-generation patch
// that test is about. Every gate a launch passes is satisfied here (the project
// layer is published and read from the checkout, the job image is a release
// whose runner reads it), so a case below is about the stamp and not about a
// refusal.
func stampLaunchFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	registerOtherLocally(t, f)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, releaseSeal)
	return f
}

// TestEveryLaunchStampsCLIVersion drives every launch path that stores a task
// and holds the stored task to the release the launch ran as. A path that built
// its own task.json, or that wrote the field after the store, fails here with
// the empty cli_version it left.
func TestEveryLaunchStampsCLIVersion(t *testing.T) {
	t.Run("run", func(t *testing.T) {
		useVersion(t, stampRelease)
		f := stampLaunchFixture(t)
		const run = "20261008-100000-abcd"
		if _, _, err := execute(t, "run", "--run-id", run, "A task"); err != nil {
			t.Fatalf("the launch was refused: %v", err)
		}
		if got := readSpecOf(t, f, mustSlug("github", "acme/other"), run).CLIVersion; got != stampRelease {
			t.Fatalf("the run's task carries cli_version %q, want %q: this launch ran as %s", got, stampRelease, stampRelease)
		}
	})

	t.Run("follow-up", func(t *testing.T) {
		useVersion(t, stampRelease)
		f := newCloudFixture(t)
		seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x"); err != nil {
			t.Fatalf("the follow-up was refused: %v", err)
		}
		if got := readSpecOf(t, f, appSlug, fuID).CLIVersion; got != stampRelease {
			t.Fatalf("the follow-up's task carries cli_version %q, want %q", got, stampRelease)
		}
	})

	t.Run("review", func(t *testing.T) {
		useVersion(t, stampRelease)
		f := newProjectJobFixture(t)
		projectJobCheckout(t)
		const run = "20261110-110001-bbbb"
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "--recipe", "review-only", "--pr", "7"); err != nil {
			t.Fatalf("the review was refused: %v", err)
		}
		if got := readSpecOf(t, f.cloudFixture, appSlug, run).CLIVersion; got != stampRelease {
			t.Fatalf("the review's task carries cli_version %q, want %q", got, stampRelease)
		}
	})

	t.Run("orchestrator", func(t *testing.T) {
		useVersion(t, stampRelease)
		f := newLauncherFixture(t)
		const run = "20261127-050000-abcd"
		spec := orchestrator.Spec{ItemID: "vs2", Title: "the launcher stamps the task", Repo: "acme/app",
			Recipe: "standard", RigorTier: "medium", Brief: "Stamp the task."}
		if _, err := f.launcher(t).Launch(t.Context(), spec, run); err != nil {
			t.Fatalf("the daemon's launch was refused: %v", err)
		}
		stored, err := f.runStore(t, run).ReadTask(t.Context())
		if err != nil {
			t.Fatalf("the run the daemon started stored no task this build can read: %v", err)
		}
		if stored.CLIVersion != stampRelease {
			t.Fatalf("the daemon's task carries cli_version %q, want %q", stored.CLIVersion, stampRelease)
		}
	})

	t.Run("troubleshoot", func(t *testing.T) {
		useVersion(t, stampRelease)
		f, job := tcLaunchFixture(t)
		// The platform's answer that the diagnosis's job ran: without it the
		// fake leaves the execution pending and the launch waits for a state
		// that never comes (troubleshootAwaitExecution).
		tcLaunchAnswers(t, f)
		if _, err := tcLaunch(t, job); err != nil {
			t.Fatalf("the diagnosis's launch was refused: %v", err)
		}
		if got := tcStoredTask(t, f.cloudFixture, job.slug, job.spec.RunID).CLIVersion; got != stampRelease {
			t.Fatalf("the diagnosis's task carries cli_version %q, want %q", got, stampRelease)
		}
	})
}

// TestDevBuildDoesNotStamp is D3449's half: a development build is not a
// release, so it has nothing to stamp and the runner upgrades nothing for it —
// the release the image already holds is what runs. The path is the plain
// `fugaro run` above, with the build version left at its development default.
func TestDevBuildDoesNotStamp(t *testing.T) {
	if releaseVersion() != "" {
		t.Fatalf("this build's version is %q, which releaseVersion reads as a release: the case below would stamp it", Version)
	}
	f := stampLaunchFixture(t)
	const run = "20261008-100000-abcd"
	if _, _, err := execute(t, "run", "--run-id", run, "A task"); err != nil {
		t.Fatalf("the launch was refused: %v", err)
	}
	spec := readSpecOf(t, f, mustSlug("github", "acme/other"), run)
	if spec.CLIVersion != "" {
		t.Fatalf("a development build's task carries cli_version %q, and a dev build names no release to upgrade to (D3449)", spec.CLIVersion)
	}
	// The key itself is absent, not merely empty: cli_version is omitempty, and
	// an older runner reading this task is the state the field was added not to
	// disturb.
	data, err := spec.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cli_version") {
		t.Errorf("the stored task carries the cli_version key though the build stamped nothing:\n%s", data)
	}
}

// TestRepeatIgnoresStamp: a repeated --run-id is judged on the request the
// caller made, never on the release that happens to run the repeat (D3441). The
// stored task keeps the stamp of the launch that really started the run, so a
// teammate on a newer CLI re-running an id is the same run and not "a different
// task" — and a genuinely different request is still refused.
func TestRepeatIgnoresStamp(t *testing.T) {
	ctx := t.Context()
	b := memblob.OpenBucket(nil)
	defer b.Close()
	s := runstore.Open(b, appSlug, "20261127-120000-abcd")
	// A launch's spec as its own caller builds it: no stamp of its own, and the
	// one createTask adds.
	stored := func() *task.Spec {
		return &task.Spec{Version: 1, RunID: s.RunID(), Repo: "acme/app", Ref: "main",
			Workflow: "web", Title: "A task", Task: "A task"}
	}

	useVersion(t, "0.128.0")
	if err := createTask(ctx, s, stored()); err != nil {
		t.Fatalf("the first launch was refused: %v", err)
	}

	useVersion(t, "0.129.0")
	if err := createTask(ctx, s, stored()); err != nil {
		t.Fatalf("a repeated --run-id launched from another release was refused as a different task: %v", err)
	}
	have, err := s.ReadTask(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if have.CLIVersion != "0.128.0" {
		t.Fatalf("the stored task carries cli_version %q, want the 0.128.0 of the launch that started the run", have.CLIVersion)
	}

	// The comparison the stamp is left out of is still a comparison: a repeat
	// carrying a different request is refused.
	other := stored()
	other.Task, other.Title = "Something else", "Something else"
	if err := createTask(ctx, s, other); err == nil || !strings.Contains(err.Error(), "different task") {
		t.Fatalf("a repeated --run-id carrying another task: err = %v, want the different-task refusal", err)
	}
}
