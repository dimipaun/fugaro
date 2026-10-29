package cli

import (
	"strings"
	"testing"
	"time"
)

// release0120 is a build record's base_ref at exactly compressionSince: a
// runner built from it knows task.Spec.CompressionOff.
const release0120 = "ghcr.io/dimipaun/fugaro-base:0.12.0"

// compressionOnForTest makes the launcher resolve agent.compression as on for
// one test, and restores it after. It is the CLI's counterpart of the
// runner's Deps.CompressionForTest: config.Validate refuses the key while it
// is reserved (the plan's P11), so no committed fugaro.yaml can carry it and
// no flag sets it — nothing a repository or a launcher controls reaches this.
func compressionOnForTest(t *testing.T) {
	t.Helper()
	old := compressionForTest
	compressionForTest = "headroom"
	t.Cleanup(func() { compressionForTest = old })
}

// TestRunNoCompressionSetsSpecField: --no-compression writes the task's own
// compression_off when the resolved configuration turns compression on, and a
// launch that does not give the flag writes no field at all. The override is
// one-directional, so there is nothing to write when compression is already
// off.
func TestRunNoCompressionSetsSpecField(t *testing.T) {
	t.Run("the flag writes the field", func(t *testing.T) {
		f := newCloudFixture(t)
		compressionOnForTest(t)
		writeBuildRecord(t, f, appSlug, "web", release0120)
		runID := wallRunID(t, time.Minute, "c0a1")
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", runID, "--no-compression", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := readSpec(t, f, runID); !got.CompressionOff {
			t.Fatal("CompressionOff = false, want true")
		}
	})
	t.Run("no flag, no field", func(t *testing.T) {
		f := newCloudFixture(t)
		compressionOnForTest(t)
		writeBuildRecord(t, f, appSlug, "web", release0120)
		runID := wallRunID(t, time.Minute, "c0a2")
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", runID, "A task"); err != nil {
			t.Fatal(err)
		}
		if got := readSpec(t, f, runID); got.CompressionOff {
			t.Fatal("CompressionOff = true with no --no-compression, want absent")
		}
	})
}

// TestNoCompressionFlagExistsButNoForceOnFlagDoes is the structural half of
// the design's scope ruling (docs/design/headroom-compression.md §4): a
// launcher may turn a repository's reviewed choice off for one run and never
// on, so the flag set carries --no-compression and no --compression. A future
// edit that added a force-on flag fails here rather than only in review.
func TestNoCompressionFlagExistsButNoForceOnFlagDoes(t *testing.T) {
	flags := newRunCmd().Flags()
	if flags.Lookup("no-compression") == nil {
		t.Error("fugaro run has no --no-compression flag")
	}
	if flags.Lookup("compression") != nil {
		t.Error("fugaro run has a --compression flag: turning compression on is a reviewed fugaro.yaml change, never a flag (docs/design/headroom-compression.md §4)")
	}
}

// TestNoCompressionWhenConfigUnreadable: --no-compression is refused when the
// launcher cannot read the configuration at all — a --repo launch from outside
// a checkout of that repository. Neither guess is safe: writing the field
// would put compression_off on a task whose repository may never turn
// compression on, and a runner older than compressionSince refuses the whole
// task for a field it does not know (task.Parse decodes with
// DisallowUnknownFields), so a launch that worked before the flag existed
// would fail at bootstrap — the one thing a flag that only ever turns
// compression off may not cause; skipping the image gate instead would let
// that same run start on such a runner. The launch says what it could not read
// and starts nothing.
func TestNoCompressionWhenConfigUnreadable(t *testing.T) {
	// unreadable is a launch made from outside any checkout of acme/app, so
	// neither the ref's document (cascadeRefConfig) nor the checkout's
	// (checkoutConfig) resolves. record is the workflow's build record: an old
	// one, so a launch that got through here would be one the image gate exists
	// to stop.
	unreadable := func(t *testing.T) *cloudFixture {
		t.Helper()
		f := newCloudFixture(t)
		t.Chdir(t.TempDir())
		writeBuildRecord(t, f, appSlug, "web", release060)
		return f
	}
	// refuse runs a launch that must be refused for the unreadable
	// configuration, and must start nothing.
	refuse := func(t *testing.T, f *cloudFixture, err error) {
		t.Helper()
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--no-compression: the launcher cannot read acme/app's fugaro.yaml at ") ||
			!strings.Contains(err.Error(), "to know whether compression is on; run from a checkout that has the ref, or drop the flag") {
			t.Fatalf("err = %v, want a user error saying --no-compression cannot tell whether compression is on", err)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	}
	t.Run("a first run", func(t *testing.T) {
		f := unreadable(t)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "c3a1"), "--no-compression", "A task")
		refuse(t, f, err)
	})
	// The follow-up case is the one that used to launch: the stored task
	// already carries compression_off, so the field was written over a
	// configuration the launcher could not read and the image gate was skipped
	// on it, and the run started on a 0.6.0 image that refuses the field at
	// bootstrap. The flag is refused here whatever the inherited field says.
	t.Run("a follow-up inheriting the field", func(t *testing.T) {
		f := unreadable(t)
		root := firstRunSpec(rootID)
		root.CompressionOff = true
		seedSpec(t, f, root, false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-time.Hour)
		writeRecord(t, f, rootID, rec)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--no-compression", "more work")
		refuse(t, f, err)
	})
	// An unreadable configuration is not itself a reason to write the field:
	// only the flag is. A launch without one stays byte-identical to what the
	// previous release wrote, and starts.
	t.Run("no flag, no field", func(t *testing.T) {
		f := unreadable(t)
		runID := wallRunID(t, time.Minute, "c3a2")
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", runID, "A task"); err != nil {
			t.Fatal(err)
		}
		if got := readSpec(t, f, runID); got.CompressionOff {
			t.Fatal("CompressionOff = true with no --no-compression, want absent")
		}
	})
}

// TestCompressionImageGateRefusesOldBuild: a launch whose task carries
// compression_off, or whose resolved configuration turns compression on, is
// refused when the job's build record predates compressionSince — on every
// launch path (a first run, a follow-up, a retry, an explicit --workflow, a
// --batch label). The gate fires whether or not --no-compression is given: the
// run would still read a fugaro.yaml an old runner's own config parser refuses,
// so the override is no way past it.
//
// The last two cases are the field's half of that condition, which the
// configuration's half does not imply: a follow-up and a retry inherit the
// field from the run they continue, so they carry it even once the repository's
// key is gone, and an old runner refuses the whole task on the field alone.
//
// The IDs are minted from the wall clock rather than fixed: the follow-up case
// drives run --pr, whose lookback resolvePR measures back from time.Now()
// itself, so a fixed date would age out of it and the case would stop reaching
// the gate by the calendar alone.
func TestCompressionImageGateRefusesOldBuild(t *testing.T) {
	refusal := func(t *testing.T, err error) {
		t.Helper()
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "agent.compression") || !strings.Contains(err.Error(), "fugaro 0.12.0 or later") {
			t.Fatalf("err = %v, want a user error naming agent.compression and fugaro 0.12.0 or later", err)
		}
	}
	t.Run("first run", func(t *testing.T) {
		f := newCloudFixture(t)
		compressionOnForTest(t)
		writeBuildRecord(t, f, appSlug, "web", release060)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "c1a1"), "A task")
		refusal(t, err)
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	})
	t.Run("follow-up", func(t *testing.T) {
		f := newCloudFixture(t)
		compressionOnForTest(t)
		writeBuildRecord(t, f, appSlug, "web", release060)
		seedSpec(t, f, firstRunSpec(rootID), false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-time.Hour)
		writeRecord(t, f, rootID, rec)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "more work")
		refusal(t, err)
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	})
	t.Run("--workflow", func(t *testing.T) {
		f := newCloudFixture(t)
		compressionOnForTest(t)
		writeBuildRecord(t, f, appSlug, "web", release060)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "c1a2"), "--workflow", "web", "A task")
		refusal(t, err)
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	})
	t.Run("--batch", func(t *testing.T) {
		f := newCloudFixture(t)
		compressionOnForTest(t)
		writeBuildRecord(t, f, appSlug, "web", release060)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "c1a3"), "--batch", "nightly", "A task")
		refusal(t, err)
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	})
	t.Run("--no-compression does not skip it", func(t *testing.T) {
		f := newCloudFixture(t)
		compressionOnForTest(t)
		writeBuildRecord(t, f, appSlug, "web", release060)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", wallRunID(t, time.Minute, "c1a4"), "--no-compression", "A task")
		refusal(t, err)
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	})
	// No compressionOnForTest here: the repository turns compression off, and
	// the field the follow-up inherits is the whole reason the launch is gated.
	t.Run("a follow-up inheriting the field after the key was removed", func(t *testing.T) {
		f := newCloudFixture(t)
		writeBuildRecord(t, f, appSlug, "web", release060)
		root := firstRunSpec(rootID)
		root.CompressionOff = true
		seedSpec(t, f, root, false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-time.Hour)
		writeRecord(t, f, rootID, rec)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "more work")
		refusal(t, err)
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	})
	t.Run("a retry of a task carrying the field", func(t *testing.T) {
		f := newCloudFixture(t)
		writeBuildRecord(t, f, appSlug, "web", release060)
		spec := firstRunSpec(wallRunID(t, time.Minute, "c1a5"))
		spec.CompressionOff = true
		seedSpec(t, f, spec, false)
		_, _, err := execute(t, "run", "--retry", spec.RunID)
		refusal(t, err)
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want none", n)
		}
	})
}

// TestCompressionImageGateNotTriggeredWithoutCompression: an installation
// that never turns compression on is unaffected in full. --no-compression
// alone has nothing to turn off, so the launch writes no compression_off
// field (an old runner would refuse the task outright) and is never judged on
// the build record the gate would need.
//
// The launch is made from a checkout of the repository because the case this
// test is about is a configuration the launcher read and that leaves
// compression off, where the flag is a no-op. A launch from outside a checkout
// is the other case — the launcher read nothing there, so the flag cannot be
// honoured and the launch is refused (TestNoCompressionWhenConfigUnreadable).
func TestCompressionImageGateNotTriggeredWithoutCompression(t *testing.T) {
	f := newCloudFixture(t)
	appRepoCheckout(t, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
		"workflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"})
	writeBuildRecord(t, f, appSlug, "web", release060)
	runID := wallRunID(t, time.Minute, "c2a1")
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", runID, "--no-compression", "A task"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, runID); got.CompressionOff {
		t.Fatal("CompressionOff = true with no compression in the configuration, want absent")
	}
}

// TestFollowUpInheritsCompressionOff: a follow-up keeps the previous run's
// override the way it keeps its account and title, so a chain of follow-ups
// on a run launched with --no-compression stays off without the flag being
// repeated on every one of them. The image is one that can read the field:
// the inherited field is what the launch is gated on (see
// TestCompressionImageGateRefusesOldBuild), even though nothing in this
// fixture's configuration turns compression on.
func TestFollowUpInheritsCompressionOff(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release0120)
	root := firstRunSpec(rootID)
	root.CompressionOff = true
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "more work"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); !got.CompressionOff {
		t.Fatal("follow-up CompressionOff = false, want true (inherited)")
	}
}

// TestCompressionImageGateRefusesOldCloneBuild is the clone branch of the
// compression gate: a checkout: clone workflow has no derived image and so no
// build record to judge (checkImageSince's half), and the floor is judged
// against base_images.base itself instead (checkCloneImageSince). 0.7.5 is a
// release that predates compressionSince, so the launch is refused there —
// before any execution — exactly as the built-image branch refuses its own old
// build record.
func TestCompressionImageGateRefusesOldCloneBuild(t *testing.T) {
	f := newCloudFixture(t)
	compressionOnForTest(t)
	cloneCheckout(t, f, release075, "", nil)
	_, _, err := execute(t, "run", "--run-id", wallRunID(t, time.Minute, "c5a1"), "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "agent.compression") ||
		!strings.Contains(err.Error(), "fugaro 0.12.0 or later") ||
		!strings.Contains(err.Error(), "the checkout: clone job image of acme/other workflow svc is ghcr.io/dimipaun/fugaro-base:0.7.5, release 0.7.5") {
		t.Fatalf("err = %v, want a user error naming agent.compression, fugaro 0.12.0 or later and the base image the clone job would run", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestRetryKeepsNoCompression: --retry replays a run's stored task as it is —
// --no-compression is in taskFlags for that reason, so the stored task is the
// only thing that can carry the override into a retry — and the field is what
// the retried launch's own image gate is judged on. A retry that dropped it
// would not merely lose the override: createTask compares the stored task with
// the one the launch just built, so a dropped field is a different task and
// the retry is refused outright.
func TestRetryKeepsNoCompression(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release0120)
	t.Chdir(t.TempDir())
	spec := firstRunSpec(wallRunID(t, time.Minute, "c6a1"))
	spec.CompressionOff = true
	seedSpec(t, f, spec, false)
	wantRefused(t, f, "it takes no --no-compression", "run", "--retry", spec.RunID, "--no-compression")
	if _, _, err := execute(t, "run", "--retry", spec.RunID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, spec.RunID); !got.CompressionOff {
		t.Fatal("retried task CompressionOff = false, want the stored task's true")
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}
