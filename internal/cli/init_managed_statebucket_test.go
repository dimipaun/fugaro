package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The installation's state bucket of a managed creation (Task 4.5 of
// docs/managed-projects/implementation-plan.md, D59): a managed project's state
// must live in the bucket the managed apply reads it from, and the read takes
// the name from the GCP project and from nowhere else.
//
// Why this is a refusal at creation rather than a read of the bucket later: the
// managed apply's accounted side lists gs://fugaro-tfstate-<gcp_project> and
// gs://fugaro-managed-tfstate-<gcp_project> by those derived names
// (managedAccounted, and Inventory.installationCreated on the other half of
// the comparison), because a bucket name a caller could choose is a bucket the
// apply would believe instead of the project's own. A managed project whose
// installation state sits in some other bucket is therefore a project whose
// state that read cannot find: CauseStateUnread, and every managed apply of it
// falls back to the typed confirmation the project was created to be free of —
// in an agent's session, a plan that is never applied. The bucket a creation
// would use is resolved from two places (--state-bucket and
// terraform.state_bucket in the project config), so the check reads the
// resolved spec and covers both; and it runs before the project stage, so a
// refused creation creates nothing.

// stateBucketOf is the bucket a managed creation's installation state must
// live in, spelled the way the run's refusal spells it.
func managedStateBucket() string { return "fugaro-tfstate-" + newProj }

// A managed creation that would put the installation's state anywhere but the
// derived bucket is refused: nothing is created, no creation fact is written,
// and the refusal names both buckets so the operator can see what to change.
func TestManagedCreationRefusesAForeignStateBucket(t *testing.T) {
	// Both spellings of the same decision: the flag, and the project config's
	// own terraform.state_bucket (which is what a creation that passes no flag
	// would otherwise use, since infra.Installation prefers the flag, then the
	// config, then the derived name).
	cases := map[string]struct {
		flags  []string
		config string
	}{
		"--state-bucket on the command line": {flags: []string{"--state-bucket", "aurora-state"}},
		"terraform.state_bucket in the project config": {
			config: "terraform: { state_bucket: aurora-state }\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, bucket := newManagedCreateRig(t)
			if tc.config != "" {
				r.appendConfig(t, tc.config)
			}
			args := append([]string{"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
				"--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc}, tc.flags...)
			out, errOut, err := r.run(t, true, createStdin, args...)
			if err == nil {
				t.Fatalf("a managed creation put its installation state outside gs://%s and was accepted:\n%s",
					managedStateBucket(), refusalText(out, errOut, nil))
			}
			s := refusalText(out, errOut, err)
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit %d, want %d: the state bucket is the caller's to fix:\n%s", code, ExitUserError, s)
			}
			// The refusal has to say both what is required and what was asked
			// for: "the state bucket is wrong" without the two names is a
			// refusal a person cannot act on.
			for _, want := range []string{managedStateBucket(), "aurora-state"} {
				if !strings.Contains(s, want) {
					t.Errorf("the refusal does not name %q:\n%s", want, s)
				}
			}
			// And the advice has to be sound. The bucket comes from
			// --state-bucket or from terraform.state_bucket, so the fix is to
			// stop recording it in either place — and NOT "flag it managed
			// later": there is no unmanaged creation to fall back on any more
			// (D2214), and `fugaro managed flag` refuses a project whose config
			// records another bucket for this same reason
			// (TestManagedFlagNeedsTheDerivedStateBucket). Advice that sends an
			// operator round that way costs a created project and arrives at
			// the same refusal.
			if !strings.Contains(s, "terraform.state_bucket") {
				t.Errorf("the refusal does not name the config field that holds the value:\n%s", s)
			}
			for _, advice := range []string{"flag it later", "--gcp-project"} {
				if strings.Contains(s, advice) {
					t.Errorf("the refusal recommends %q, which leads to the same state:\n%s", advice, s)
				}
			}
			// And it fires before anything is created: no project, no
			// Firebase, no billing link, no creation fact. A check that ran
			// after the project stage would leave a managed project that the
			// managed apply always falls back on.
			r.nothingDone(t, "a managed creation refused for its state bucket")
			noMarker(t, r.gcs, bucket)
		})
	}
}

// The control the refusal must not break: the derived name, written out
// explicitly, is the bucket the creation wants and is accepted — the check is
// an equality with the derived name and not a refusal of the flag.
func TestManagedCreationAcceptsTheDerivedStateBucket(t *testing.T) {
	r, bucket := newManagedCreateRig(t)
	out, errOut, err := r.run(t, true, createStdin,
		"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
		"--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc,
		"--state-bucket", managedStateBucket())
	if err != nil {
		t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
	}
	if n := len(r.crm.Creates()); n != 1 {
		t.Fatalf("%d project(s) created:\n%s", n, refusalText(out, errOut, nil))
	}
	if _, ok := markerAt(t, r.gcs, bucket); !ok {
		t.Errorf("the creation wrote no %s in gs://%s:\n%s", "fugaro/managed.json", bucket, refusalText(out, errOut, nil))
	}
}

// And the same on the run itself: a first run of `fugaro setup` creates its
// project without --create-project saying so, and it is held to the rule like
// any other creating run — the gate is the settled creation and not the flag's
// spelling of it (D2214, T2c). `--managed` used to be what this case passed,
// and it is the flag this release retired.
func TestAFlaglessFirstRunIsRefusedAForeignStateBucket(t *testing.T) {
	r, bucket := newManagedCreateRig(t)
	// A first run on this machine: no project config to select.
	if err := os.Remove(r.cfg); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := r.run(t, true, "yes\n",
		"--name", "aurora", "--rigor", "startup", "--region", "us-east5",
		"--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc,
		"--state-bucket", "aurora-state")
	if err == nil {
		t.Fatalf("a flagless first run put its installation state outside gs://%s and was accepted:\n%s",
			managedStateBucket(), refusalText(out, errOut, nil))
	}
	s := refusalText(out, errOut, err)
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("exit %d, want %d:\n%s", code, ExitUserError, s)
	}
	for _, want := range []string{managedStateBucket(), "aurora-state"} {
		if !strings.Contains(s, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, s)
		}
	}
	r.nothingDone(t, "a flagless creation refused for its state bucket")
	noMarker(t, r.gcs, bucket)
}

// The gate follows the run's creation of the project, not the flag's spelling
// of it (D2214, T2c). settleCreation sets --create-project's own field on every
// first run of `setup`, whose creation no flag names, and every project a
// creating run makes Fugaro's is managed — so a first run that would put the
// installation's state outside the derived bucket is refused even though it
// passed no --create-project (and no --managed exists to pass any more, which
// is what left this case to the settled field).
func TestTheSettledCreationIsHeldToTheStateBucketRule(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	lc, err := localcfg.Parse([]byte(catalogProducerLocal + "terraform: { state_bucket: aurora-state }\n"))
	if err != nil {
		t.Fatal(err)
	}
	o := &initOptions{door: setupDoor.name, cloud: cloudOptions{gcpProject: lc.GCPProject}}
	if err := (&initRun{o: o}).settleCreation(true); err != nil {
		t.Fatalf("settleCreation: %v", err)
	}
	if !o.createProject {
		t.Fatal("a first run of setup settles no creation: this test's whole point is the field it sets")
	}
	_, err = installOptions(o, lc)
	if err == nil {
		t.Fatal("a settled creation put the installation's state outside the derived bucket and was accepted")
	}
	for _, want := range []string{"fugaro-tfstate-" + lc.GCPProject, "aurora-state"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// And the line the gate does not cross: a run that creates no project installs
// its state where its own config says, so a bucket of its own is its answer and
// not a refusal.
func TestARunThatCreatesNoProjectKeepsItsStateBucket(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	lc, err := localcfg.Parse([]byte(catalogProducerLocal + "terraform: { state_bucket: aurora-state }\n"))
	if err != nil {
		t.Fatal(err)
	}
	o := &initOptions{door: updateDoor.name, cloud: cloudOptions{gcpProject: lc.GCPProject}}
	if err := (&initRun{o: o}).settleCreation(true); err != nil {
		t.Fatalf("settleCreation: %v", err)
	}
	if o.createProject {
		t.Fatal("a run of the update door settled a creation")
	}
	spec, err := installOptions(o, lc)
	if err != nil {
		t.Fatalf("a run that creates no project was refused for its state bucket: %v", err)
	}
	if spec.StateBucket != "aurora-state" {
		t.Errorf("the state bucket this run would use = %q, want aurora-state", spec.StateBucket)
	}
}
