package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The installation's runs bucket names its project in fugaro/project.json;
// a project config whose name differs is refused, exit 1, before the
// command does anything.
func TestOpenCloudRefusesNameMismatch(t *testing.T) {
	f := newCloudFixture(t)
	f.writeMarker(t, "borealis", "proj-1234")
	_, stderr, err := execute(t, "ls")
	if ExitCode(err) != ExitUserError || err == nil {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	for _, want := range []string{"project config aurora", "whose Fugaro project is borealis"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q lacks %q", err, want)
		}
	}
	if !strings.Contains(stderr, "project: aurora") {
		t.Errorf("the header came after the refusal, or not at all: %q", stderr)
	}
}

func TestOpenCloudRefusesGCPProjectMismatch(t *testing.T) {
	f := newCloudFixture(t)
	f.writeMarker(t, "aurora", "other-proj")
	_, _, err := execute(t, "ls")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "other-proj") || !strings.Contains(err.Error(), "proj-1234") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// An installation applied before M9a has no marker: the operator names it.
func TestOpenCloudMissingMarker(t *testing.T) {
	f := newCloudFixture(t)
	if err := os.Remove(f.markerPath()); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "ls")
	if ExitCode(err) != ExitUserError || err == nil {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	for _, want := range []string{"project aurora's installation has no project name yet", "fugaro init --name aurora"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q lacks %q", err, want)
		}
	}
}

func TestOpenCloudMarkerGarbageIsRefused(t *testing.T) {
	f := newCloudFixture(t)
	if err := os.WriteFile(f.markerPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "ls")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "project.json") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// A marker that can't be read (as opposed to one that isn't there) is a
// remote failure, not a verdict on the config.
func TestOpenCloudMarkerReadErrorIsRemote(t *testing.T) {
	f := newCloudFixture(t)
	if err := os.Chmod(f.markerPath(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.markerPath(), 0o644) })
	if f, err := os.Open(f.markerPath()); err == nil {
		f.Close()
		t.Skip("the file is readable despite mode 0 (running as root?)")
	}
	_, _, err := execute(t, "ls")
	if ExitCode(err) != ExitRemoteError || err == nil {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// One check a day: a second command finds the answer cached and reads no
// object.
func TestOpenCloudUsesCache(t *testing.T) {
	f := newCloudFixture(t)
	if _, _, err := execute(t, "ls"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.markerPath()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "ls"); err != nil {
		t.Fatalf("the second command read the marker again: %v", err)
	}
	cached := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "fugaro", "project-check", "aurora.json")
	if _, err := os.Stat(cached); err != nil {
		t.Fatal(err)
	}
}

// A failed check is never cached, and a cached check for another runs
// bucket or GCP project doesn't count.
func TestOpenCloudCacheIgnoresOtherBucket(t *testing.T) {
	f := newCloudFixture(t)
	if _, _, err := execute(t, "ls"); err != nil {
		t.Fatal(err)
	}
	// Point the config at another bucket (an empty one, so no marker): the
	// cached check was for the old one.
	other := filepath.Join(f.dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("FUGARO_CONFIG"), []byte(strings.Replace(string(data), "bucket_url: "+f.bucket, "bucket_url: file://"+other, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = execute(t, "ls")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "no project name yet") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

func TestOpenCloudFailedCheckIsNotCached(t *testing.T) {
	f := newCloudFixture(t)
	f.writeMarker(t, "borealis", "proj-1234")
	if _, _, err := execute(t, "ls"); err == nil {
		t.Fatal("a mismatch passed")
	}
	f.writeMarker(t, "aurora", "proj-1234")
	if _, _, err := execute(t, "ls"); err != nil {
		t.Fatalf("after the fix: %v", err)
	}
}
