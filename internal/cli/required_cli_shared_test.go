package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// The fence's second call site (R4). A command with no local config resolves
// its project through selectProject's fallback to the shared config published
// to the runs bucket, and the root hook could not have fenced it: at that
// moment the config did not exist on this machine. The hook records its
// verdict in the command's context instead, and selectProject's shared-config
// branch is where the fence asks.
//
// So the tests here are about that branch, and they are the branch's own: the
// fixture is a real shared config fetched from the fixture's bucket, and what
// varies is the verdict a command's pre-run would have recorded — which
// TestEveryCommandIsFencedOrExempt pins for every command, doctor and runs ls
// among them.

// fenceShared is the shared-config world: a fixture whose project has no local
// config at all, only the one published to its runs bucket, a checkout naming
// it, the stamp source faked and the binary at a released version.
func fenceShared(t *testing.T, min string) (*fenceRig, *cloudFixture) {
	t.Helper()
	f := sharedEnd2End(t)
	t.Chdir(gitCheckout(t, filepath.Join(f.dir, "app"), "version: 1\nproject: aurora\ngcp_project: proj-1234\n"))
	withVersion(t, fenceVersion)
	return newFenceRig(t).above(min), f
}

// fenceSharedFetched wraps the fixture's own fetch seam and hands back the last
// config it produced: the published installation, whose database is the one the
// fence's shared-config check must have read. The assertion it exists for is
// that the fence asked about the config the fetch returned and not some other
// machine's.
func fenceSharedFetched(t *testing.T) func() *localcfg.Config {
	t.Helper()
	inner := sharedFetch
	var last *localcfg.Config
	sharedFetch = func(ctx context.Context, getenv func(string) string, now time.Time, name, gcp string) (*localcfg.Config, string, error) {
		c, note, err := inner(ctx, getenv, now, name, gcp)
		if c != nil {
			last = c
		}
		return c, note, err
	}
	t.Cleanup(func() { sharedFetch = inner })
	return func() *localcfg.Config { return last }
}

// fenceNotes is the fence's own lines in s: the notes this package prints
// beside a command's output, and the one thing a console read adds to stderr.
func fenceNotes(s string) []string {
	var notes []string
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		if strings.HasPrefix(line, "fugaro: note: ") {
			notes = append(notes, line)
		}
	}
	return notes
}

// fenceStampNotes is the notes that are the fence's own line, told from the
// other notes a shared-config read prints beside its output.
func fenceStampNotes(s string) []string {
	var lines []string
	for _, note := range fenceNotes(s) {
		if strings.Contains(note, "requires fugaro") {
			lines = append(lines, note)
		}
	}
	return lines
}

// TestFenceCoversTheSharedConfigFallback: a project this machine has no local
// config for is fenced all the same. The command's own resolver fetched the
// published config, so the fence asks there — and it asks about the database
// that config names, not about this machine's.
func TestFenceCoversTheSharedConfigFallback(t *testing.T) {
	rig, _ := fenceShared(t, fenceAbove)
	fetched := fenceSharedFetched(t)

	out, _, err := execute(t, "tell", "--json", "abcd1234", "hello")
	fenceRefusal(t, out, err)
	if len(rig.queries) != 1 {
		t.Fatalf("the fence read the stamp %v, want one read of the published installation", rig.queries)
	}
	c := fetched()
	if c == nil {
		t.Fatal("no shared config was fetched: the command did not go through the fallback this test is about")
	}
	if got, want := rig.queries[0].URL, c.Budget.RTDBURL; got != want {
		t.Errorf("the fence asked about %s, want the published config's database %s", got, want)
	}
}

// TestDoctorOnTheSharedConfigPassesBelowTheStamp: doctor is exempt because it
// reports the stamp as a check of its own instead of refusing, and that
// exemption has to hold on the shared-config path too — the one path where the
// fence is not the hook's. The fence does not read the node for it: an exemption
// is the fence not asking, and the one read is doctor's own check's.
func TestDoctorOnTheSharedConfigPassesBelowTheStamp(t *testing.T) {
	rig, _ := fenceShared(t, fenceAbove)

	out, errOut, err := execute(t, "doctor", "--json")
	var below *versionstamp.BelowError
	if errors.As(err, &below) {
		t.Fatalf("doctor was refused below the stamp on the shared config: %v", err)
	}
	// Doctor's own required-version check reads the published node once; a
	// second read would be the fence asking as well.
	if len(rig.queries) > 1 {
		t.Errorf("doctor read the stamp %v, want its own one read and no fence read: the exemption has to reach the shared-config site", rig.queries)
	}
	if !strings.Contains(errOut, "shared config") {
		t.Errorf("doctor's stderr does not name the shared config it read:\n%s", errOut)
	}
	if out != "" && !json.Valid([]byte(out)) {
		t.Errorf("stdout = %q, want a JSON document or nothing", out)
	}
}

// TestRunsLsOnTheSharedConfigPassesBelowTheStamp: runs ls is a read the console
// spawns, so it passes below the stamp with one line beside its answer — here
// through the shared config, where the line comes from selectProject's own
// check rather than the hook's.
func TestRunsLsOnTheSharedConfigPassesBelowTheStamp(t *testing.T) {
	rig, _ := fenceShared(t, fenceAbove)

	out, errOut, err := execute(t, "runs", "ls")
	if err != nil {
		t.Fatalf("runs ls below the stamp: %v\n%s", err, errOut)
	}
	if !strings.Contains(out, "0 runs") {
		t.Errorf("stdout = %q, want the run list: a console read's answer must survive the fence", out)
	}
	notes := fenceNotes(errOut)
	if len(notes) != 2 {
		t.Errorf("stderr carries %d notes, want the fence's and the shared config's:\n%s", len(notes), errOut)
	}
	stamps := fenceStampNotes(errOut)
	if len(stamps) != 1 || !strings.Contains(stamps[0], fenceAbove) {
		t.Errorf("the fence's lines in stderr = %q, want exactly one naming %s:\n%s", stamps, fenceAbove, errOut)
	}
	if len(rig.queries) != 1 {
		t.Errorf("the fence read the stamp %v, want one read", rig.queries)
	}
}

// TestSharedConfigWithABareContextIsNotChecked is R4's last sentence: a
// selectProject that no command's pre-run went through carries no verdict, and
// a fence that guessed would refuse a command its own table exempts — so it
// asks nothing at all.
func TestSharedConfigWithABareContextIsNotChecked(t *testing.T) {
	rig := newFenceRig(t).above(fenceAbove)
	withVersion(t, fenceVersion)
	sharedEnd2End(t)

	var errOut bytes.Buffer
	sel, lc, err := selectProject(context.Background(), cloudOptions{
		project: "aurora", gcpProject: "proj-1234",
		stderr: func() io.Writer { return &errOut },
	})
	if err != nil {
		t.Fatalf("selectProject with no verdict: %v", err)
	}
	if sel.From != "shared config" || lc == nil {
		t.Fatalf("selection = %+v, want the shared config: this test is about the branch that fetched it", sel)
	}
	if len(rig.queries) != 0 {
		t.Errorf("a context with no verdict was checked: the fence asked %v", rig.queries)
	}
	if _, ok := fenceVerdictFrom(context.Background()); ok {
		t.Error("a bare context carries a fence verdict")
	}
}
