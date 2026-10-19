package cli

import (
	"strings"
	"testing"
)

// The installation's state bucket of a managed creation (Task 4.5 of
// docs/plans/2026-10-16-managed-projects.md, D59): a managed project's state
// must live in the bucket the managed apply reads it from, and the read takes
// the name from the GCP project and from nowhere else.
//
// Why this is a refusal at creation and not a read that follows the flag: the
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
				"--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc}, tc.flags...)
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
		"--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc,
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

// And the other control: a creation that is NOT managed keeps --state-bucket
// exactly as it was. The rule is about the managed read's derived names, and
// an ordinary installation reads its state from whatever bucket it was given,
// so nothing here may reach it.
func TestOrdinaryCreationKeepsItsStateBucket(t *testing.T) {
	r, _ := newManagedCreateRig(t)
	out, errOut, err := r.run(t, true, createStdin,
		"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
		"--state-bucket", "aurora-state")
	if err != nil {
		t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
	}
	if n := len(r.crm.Creates()); n != 1 {
		t.Fatalf("%d project(s) created:\n%s", n, refusalText(out, errOut, nil))
	}
}
