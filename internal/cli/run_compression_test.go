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

// TestCompressionImageGateRefusesOldBuild: a launch whose resolved
// configuration turns compression on is refused when the job's build record
// predates compressionSince, on every launch path (a first run, a follow-up,
// an explicit --workflow, a --batch label). The gate fires whether or not
// --no-compression is given: the run would still read a fugaro.yaml an old
// runner's own config parser refuses, so the override is no way past it.
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
}

// TestCompressionImageGateNotTriggeredWithoutCompression: an installation
// that never turns compression on is unaffected in full. --no-compression
// alone has nothing to turn off, so the launch writes no compression_off
// field (an old runner would refuse the task outright) and is never judged on
// the build record the gate would need.
func TestCompressionImageGateNotTriggeredWithoutCompression(t *testing.T) {
	f := newCloudFixture(t)
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
// repeated on every one of them.
func TestFollowUpInheritsCompressionOff(t *testing.T) {
	f := newCloudFixture(t)
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
