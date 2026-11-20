package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// region_refusal_test.go pins LP8 of docs/location-preference: a typed region
// that is not the installation's is refused by the two commands that plan, with
// the fix (D3088 as amended, R6), and every other command keeps it as the
// per-invocation override it is (F4). The region is refused before anything is
// planned, which is why the tests run the commands and not the loaders: the
// property is that no plan, no apply and no write happens first.
//
// The two commands that plan are the loader pair's callers — loadInitConfig for
// `setup` and `update`, loadRepoConfig for `update --repo` — so both are run
// here, and `--plan-only` with them, because a run that applies nothing still
// plans in the region it was handed.
//
// The installation's own region is the rig's: us-east5 (initConfig). The region
// every row below types instead is us-west1.

// stubRunsBucketLocation makes the runs bucket's location read answer loc, or
// err, for the rows about a machine that has no project config yet: the config
// such a run builds carries no endpoints, so the real read has no fake to answer
// it.
func stubRunsBucketLocation(t *testing.T, loc string, err error) {
	t.Helper()
	old := runsBucketLocation
	runsBucketLocation = func(context.Context, *localcfg.Config, string) (string, error) { return loc, err }
	t.Cleanup(func() { runsBucketLocation = old })
}

// typedRegionOutput is what every row below reads: the two streams and the
// error, in one string, because a refusal reaches the terminal as an error and
// a note as a line on stderr.
func typedRegionOutput(out, errOut string, err error) string {
	if err == nil {
		return out + errOut
	}
	return out + errOut + ErrorText(err)
}

func TestAChangedRegionIsRefusedBySetupAndUpdate(t *testing.T) {
	refusal := []string{
		"--region us-west1 is not the installation's region us-east5",
		"cannot move",
		"Run the same command with --region us-east5",
	}
	for _, door := range []string{"setup", "update"} {
		t.Run(door, func(t *testing.T) {
			r := updateRig(t)
			out, errOut, err := executeStdin(t, "", door, "--region", "us-west1")
			got := typedRegionOutput(out, errOut, err)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("%s --region us-west1: exit %d, want %d\n%s", door, ExitCode(err), ExitUserError, got)
			}
			// The refusal names the installation's region, says it cannot move
			// and prints the fix: the same command with that region.
			for _, want := range refusal {
				if !strings.Contains(got, want) {
					t.Errorf("%s: the refusal lacks %q:\n%s", door, want, got)
				}
			}
			// And nothing was planned or applied: the region is refused before
			// anything is, which is the whole point of refusing it here.
			if n := len(r.ran(t, "plan")) + len(r.ran(t, "apply")); n != 0 {
				t.Errorf("%s planned or applied %d time(s) in the typed region: %q", door, n, r.calls(t))
			}
		})
	}
}

func TestARepoUpdateInAnotherRegionIsRefused(t *testing.T) {
	r := updateRig(t)
	t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", initProjectName, "")))

	out, errOut, err := executeStdin(t, "", "update", "--repo", "--no-build", "--region", "us-west1")
	got := typedRegionOutput(out, errOut, err)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("update --repo --region us-west1: exit %d, want %d\n%s", ExitCode(err), ExitUserError, got)
	}
	for _, want := range []string{
		"--region us-west1 is not the installation's region us-east5",
		"cannot move",
		"Run the same command with --region us-east5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, got)
		}
	}
	if n := len(r.ran(t, "plan")) + len(r.ran(t, "apply")); n != 0 {
		t.Errorf("update --repo planned or applied %d time(s) in the typed region: %q", n, r.calls(t))
	}
}

func TestPlanOnlyInAnotherRegionIsRefused(t *testing.T) {
	r := updateRig(t)
	out, errOut, err := executeStdin(t, "", "update", "--plan-only", "--region", "us-west1")
	got := typedRegionOutput(out, errOut, err)
	if ExitCode(err) != ExitUserError || !strings.Contains(got, "--region us-west1 is not the installation's region us-east5") {
		t.Fatalf("update --plan-only --region us-west1: exit %d, err %v\n%s", ExitCode(err), err, got)
	}
	if n := len(r.ran(t, "plan")); n != 0 {
		t.Errorf("a plan-only run planned in the typed region: %q", r.calls(t))
	}
}

// A machine with no project config has no region written down to compare a typed
// one with, so the runs bucket's own location is the installation's (R6, F12):
// the bucket was made in that region and cannot move either.
func TestANewMachinesRegionMustBeTheInstallations(t *testing.T) {
	r := updateRig(t)
	stubRunsBucketLocation(t, "europe-west1", nil)
	if err := os.Remove(r.cfg); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := executeStdin(t, "", "update", "--config-only", "--gcp-project", initProject, "--region", "us-east5")
	got := typedRegionOutput(out, errOut, err)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a new machine's update in another region: exit %d, want %d\n%s", ExitCode(err), ExitUserError, got)
	}
	for _, want := range []string{
		"--region us-east5 is not the installation's region europe-west1",
		"cannot move",
		"Run the same command with --region europe-west1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, got)
		}
	}
	// And nothing was written: the region is refused before the installation's
	// local config is created, not after.
	if _, err := os.Stat(r.cfg); err == nil {
		t.Errorf("the run wrote %s although it refused the region", r.cfg)
	}
}

// The reader those rows replace, against the fake: a bucket the rig made in a
// region answers it, and one that is not there is reported as missing rather
// than as a location.
func TestTheRunsBucketsLocationIsReadFromTheBucket(t *testing.T) {
	r := updateRig(t)
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, nil)
	r.gcs.SetBucketLocation(initRunsBucket, "europe-west1")
	if got, err := runsBucketLocation(context.Background(), lc, initRunsBucket); err != nil || got != "europe-west1" {
		t.Fatalf("the runs bucket's location = %q, %v; want europe-west1", got, err)
	}
	if _, err := runsBucketLocation(context.Background(), lc, "fugaro-runs-nothing"); !errors.Is(err, errRunsBucketMissing) {
		t.Fatalf("a bucket that is not there: %v, want %v", err, errRunsBucketMissing)
	}
}

func TestTheSameRegionIsAccepted(t *testing.T) {
	// The installation's own region, with the config there: nothing is refused,
	// nothing is said about it, and the run plans as it always did. It is
	// --plan-only because that is all this row needs: an ordinary update of the
	// rig's config writes it, and the write is the one step of that run which
	// wants a terminal.
	r := updateRig(t)
	out, errOut, err := executeStdin(t, "", "update", "--plan-only", "--region", "us-east5")
	got := typedRegionOutput(out, errOut, err)
	if err != nil {
		t.Fatalf("update --plan-only --region us-east5 (the installation's own): %v\n%s", err, got)
	}
	if strings.Contains(got, "is not the installation's region") || strings.Contains(got, "was not checked") {
		t.Errorf("a region the installation is already in was refused or questioned:\n%s", got)
	}

	// And on a machine with no config yet, where the region that is accepted is
	// the runs bucket's own. --print-vars is the one path of the two planning
	// doors that reaches the loader and then stops before the first cloud call:
	// the config such a run builds carries no endpoints, so anything further
	// would leave the fakes and reach the real Google.
	r = updateRig(t)
	stubRunsBucketLocation(t, "us-east5", nil)
	if err := os.Remove(r.cfg); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = executeStdin(t, "", "update", "--print-vars", "--name", initProjectName, "--gcp-project", initProject, "--region", "us-east5")
	got = typedRegionOutput(out, errOut, err)
	if err != nil {
		t.Fatalf("a new machine's update in the bucket's own region: %v\n%s", err, got)
	}
	if strings.Contains(got, "is not the installation's region") || strings.Contains(got, "was not checked") {
		t.Errorf("the bucket's own region was refused or questioned:\n%s", got)
	}
}

// A launcher may lack storage.buckets.get (F21): a location that cannot be read
// is not a refusal, and the one line saying it was not checked is the whole of
// what the person is told about it (R10, D3096).
func TestAnUnreadableBucketIsSaidNotRefused(t *testing.T) {
	r := updateRig(t)
	stubRunsBucketLocation(t, "", errors.New("no storage.buckets.get"))
	if err := os.Remove(r.cfg); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := executeStdin(t, "", "update", "--print-vars", "--name", initProjectName, "--gcp-project", initProject, "--region", "us-east5")
	got := typedRegionOutput(out, errOut, err)
	if err != nil {
		t.Fatalf("an unreadable runs bucket stopped a new machine's update: %v\n%s", err, got)
	}
	const note = "fugaro: note: --region us-east5 was not checked against the runs bucket gs://" + initRunsBucket + ", whose location could not be read\n"
	if n := strings.Count(errOut, note); n != 1 {
		t.Errorf("the note %q appears %d time(s) in stderr:\n%s", note, n, errOut)
	}
	if strings.Contains(got, "is not the installation's region") {
		t.Errorf("a location that could not be read was refused:\n%s", got)
	}
}

// Every other command keeps --region as the per-invocation override it is (F4,
// D3088's amendment): doctor is one, and the region of the project it reads is
// the one it was handed. `runs ls` and `update --check` are the other two, each
// pinned by its own test (TestLsInAnotherRegionNamesTheRunsRegion, and
// update_check_test.go's row).
func TestReadCommandsKeepTheRegionOverride(t *testing.T) {
	newDoctorRig(t)
	noProjectLayerBucket(t)
	t.Chdir(gitCheckout(t, t.TempDir(), "version: 1\nproject: "+initProjectName+"\ngcp_project: "+initProject+"\n"))

	o := doctorJSON(t, "--region", "europe-west1")
	if o.Error != "" {
		t.Fatalf("doctor refused a region override: %s", o.Error)
	}
	if o.Project == nil || o.Project.Region != "europe-west1" {
		t.Fatalf("doctor did not read the project in the typed region: %+v", o.Project)
	}
}
