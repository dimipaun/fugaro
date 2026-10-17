package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// The budget amount of a managed creation (Task 4.2 of
// docs/plans/2026-10-16-managed-projects.md, D64): required at creation, with
// a suggestion printed and a clear refusal otherwise, and recorded in the
// project's own creation fact so a later run and a restore can read what was
// chosen. The other half is what these tests exist as much for: a creation
// that is not managed keeps today's behaviour byte for byte — the amount is
// opt-in there, and the creation fact stays the object it is today.
//
// The harness is init_managed_test.go's case (c), the one run in the tree
// that really writes the fact: the project this run creates through
// projects.create, its own runs bucket (labelled and numbered as the
// installation builds it, because managed.WriteMarker proves both before it
// writes anything), and the installation's outputs naming that bucket. What
// is asserted is the refusal, the object's bytes, and what the run left
// undone.

// newManagedCreateRig is the create rig of a run that creates the project,
// with the created project's own runs bucket seeded as the installation
// leaves it: everything markCreated reads before it writes the fact.
func newManagedCreateRig(t *testing.T) (*projectRig, string) {
	t.Helper()
	r := newProjectRig(t)
	bucket := "fugaro-runs-" + newProj
	r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
	r.gcs.AddBucket(bucket, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
	roles := map[string]string{}
	for _, role := range []string{"launcher", "job_runner", "build_submitter", "tag_mover"} {
		roles[role] = "projects/" + newProj + "/roles/fugaro" + strings.ToUpper(role[:1]) + role[1:]
	}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"runs_bucket":               bucket,
		"registry_host":             "us-east5-docker.pkg.dev/" + newProj,
		"scheduler_service_account": "fugaro-scheduler@" + newProj + ".iam.gserviceaccount.com",
		"role_ids":                  roles,
	})}
	r.save(t)
	r.bill.AddAccount(billingAcc, true)
	return r, bucket
}

// createStdin is what the create rig's run types at its confirmations: the
// project's ID, the billing account, and the project's name for the
// installation's own review.
const createStdin = newProj + "\n" + billingAcc + "\n" + initProjectName + "\n"

// refusalText is everything a refused run said, whichever stream it said it
// on: the error cobra printed and the two buffers a person reads together.
func refusalText(out, errOut string, err error) string {
	s := out + errOut
	if err != nil {
		s += " " + err.Error()
	}
	return s
}

// A managed creation with no amount is refused, and the refusal is one a
// person can act on: it names the three flags and prints a value to start
// from, and the run does nothing at all — no project, no Firebase, no
// billing link, and no creation fact (a run that created nothing has nothing
// to record).
func TestManagedBudgetIsRequiredAtCreation(t *testing.T) {
	t.Run("--create-project --managed with no amount", func(t *testing.T) {
		r, bucket := newManagedCreateRig(t)
		out, errOut, err := r.run(t, true, createStdin,
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New", "--managed")
		if err == nil {
			t.Fatalf("the run created the project with no amount (D64 requires one):\n%s", refusalText(out, errOut, nil))
		}
		s := refusalText(out, errOut, err)
		if code := ExitCode(err); code != ExitUserError {
			t.Errorf("exit %d, want %d: a missing amount is the caller's to fix:\n%s", code, ExitUserError, s)
		}
		if !strings.Contains(s, "--managed") {
			t.Errorf("the refusal does not say which flag requires the amount:\n%s", s)
		}
		for _, want := range []string{"--budget", "--budget-currency", "--billing-account"} {
			if !strings.Contains(s, want) {
				t.Errorf("the refusal does not name %s:\n%s", want, s)
			}
		}
		// The suggestion is the point of the refusal: D64 asks for a value a
		// person can act on rather than a bare "missing flag". It is a
		// suggestion and not a default — the run records nothing (below), and
		// no constant carries it (TestManagedBudgetHasNoDefault).
		if !strings.Contains(s, "--budget 50 --budget-currency USD") {
			t.Errorf("the refusal prints no suggested amount to start from:\n%s", s)
		}
		r.nothingDone(t, "a managed creation refused for its missing amount")
		noMarker(t, r.gcs, bucket)
	})

	// One of the three flags without the other two is the same refusal: the
	// amount is a triple (--budget, --budget-currency, --billing-account) or
	// it is nothing, and a managed creation never has half of one.
	t.Run("--create-project --managed with half an amount", func(t *testing.T) {
		r, bucket := newManagedCreateRig(t)
		out, errOut, err := r.run(t, true, createStdin,
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New", "--managed", "--budget", "7")
		if err == nil {
			t.Fatalf("the run created the project with an amount and no currency:\n%s", refusalText(out, errOut, nil))
		}
		s := refusalText(out, errOut, err)
		if code := ExitCode(err); code != ExitUserError {
			t.Errorf("exit %d, want %d:\n%s", code, ExitUserError, s)
		}
		for _, want := range []string{"--budget-currency", "--billing-account"} {
			if !strings.Contains(s, want) {
				t.Errorf("the refusal does not name %s:\n%s", want, s)
			}
		}
		r.nothingDone(t, "a managed creation refused for half an amount")
		noMarker(t, r.gcs, bucket)
	})

	// --managed is about the project this run creates: a project that already
	// exists is flagged managed by an owner-run command of its own (D58), and
	// init --firebase adds Firebase to such a project, which is never what
	// makes one managed (D73).
	t.Run("--managed without --create-project", func(t *testing.T) {
		r := newProjectRig(t)
		r.existing(true)
		out, errOut, err := r.run(t, true, "", "--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc)
		if err == nil {
			t.Fatalf("--managed was accepted without --create-project:\n%s", refusalText(out, errOut, nil))
		}
		s := refusalText(out, errOut, err)
		if code := ExitCode(err); code != ExitUserError {
			t.Errorf("exit %d, want %d:\n%s", code, ExitUserError, s)
		}
		if !strings.Contains(s, "--create-project") {
			t.Errorf("the refusal does not say what --managed goes with:\n%s", s)
		}
		r.nothingDone(t, "--managed without --create-project")
	})
}

// The positive half: the amount and the currency a run was given are the ones
// the project's creation fact carries, and the fact is the marker's new
// version — the object Task 0.1 writes extended, not a second object
// (Task 4.2's own step: the read of Task 0.2 is taught the new version, and
// the two live side by side because a project created before this task keeps
// reading as managed).
func TestManagedCreationRecordsTheAmount(t *testing.T) {
	t.Run("--managed records the amount and the currency in the fact", func(t *testing.T) {
		r, bucket := newManagedCreateRig(t)
		out, errOut, err := r.run(t, true, createStdin,
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
			"--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc)
		if err != nil {
			t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
		}
		if n := len(r.crm.Creates()); n != 1 {
			t.Fatalf("%d project(s) created:\n%s", n, refusalText(out, errOut, nil))
		}
		b, ok := markerAt(t, r.gcs, bucket)
		if !ok {
			t.Fatalf("no %s in gs://%s after the run that created the project:\n%s", managed.MarkerObject, bucket, refusalText(out, errOut, nil))
		}
		want := `{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":7,"currency":"USD"}}`
		if b != want {
			t.Errorf("gs://%s/%s = %s, want %s", bucket, managed.MarkerObject, b, want)
		}
	})

	// A budget an ordinary creation asks for is exactly what it is today: the
	// Terraform budget, and a creation fact that records none. This is the
	// half of Task 4.2 that must not reach anyone else — the change is for a
	// managed creation, and everything else is byte for byte what it was.
	t.Run("--create-project with a budget and no --managed records none", func(t *testing.T) {
		r, bucket := newManagedCreateRig(t)
		out, errOut, err := r.run(t, true, createStdin,
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
			"--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc)
		if err != nil {
			t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
		}
		b, ok := markerAt(t, r.gcs, bucket)
		if !ok {
			t.Fatalf("no %s in gs://%s after the run that created the project:\n%s", managed.MarkerObject, bucket, refusalText(out, errOut, nil))
		}
		want := `{"version":1,"gcp_project":"` + newProj + `"}`
		if b != want {
			t.Errorf("gs://%s/%s = %s, want %s: a creation that is not managed records no amount", bucket, managed.MarkerObject, b, want)
		}
	})

	// And without --managed and without a budget — the run every existing
	// test and every existing installation makes — the fact is the object it
	// is today, byte for byte.
	t.Run("--create-project without --managed is unchanged", func(t *testing.T) {
		r, bucket := newManagedCreateRig(t)
		out, errOut, err := r.run(t, true, createStdin,
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New")
		if err != nil {
			t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
		}
		b, ok := markerAt(t, r.gcs, bucket)
		if !ok {
			t.Fatalf("no %s in gs://%s after the run that created the project:\n%s", managed.MarkerObject, bucket, refusalText(out, errOut, nil))
		}
		want := `{"version":1,"gcp_project":"` + newProj + `"}`
		if b != want {
			t.Errorf("gs://%s/%s = %s, want %s", bucket, managed.MarkerObject, b, want)
		}
	})
}

// There is no amount anything can fall back on — D64's answer, and the test
// the task's red step is written against ("the 'no default' test against a
// constant default of 50"). A default could live in exactly two places, so
// both are pinned: the flag's own default value (an absent flag is not an
// amount), and the refusal path (a run given no amount records nothing, so a
// fallback like 50 has nowhere to be written from). A constant such as
// `defaultManagedBudget = 50`, wired into either, fails here.
func TestManagedBudgetHasNoDefault(t *testing.T) {
	f := newInitCmd().Flags().Lookup("budget")
	if f == nil {
		t.Fatal("fugaro init has no --budget flag")
	}
	if f.DefValue != "0" {
		t.Errorf("fugaro init --budget defaults to %q, want 0: a managed creation has no amount to fall back on (D64), and 0 is what an absent flag means", f.DefValue)
	}

	r, bucket := newManagedCreateRig(t)
	out, errOut, err := r.run(t, true, createStdin,
		"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New", "--managed")
	if err == nil {
		t.Fatalf("a managed creation chose an amount by itself:\n%s", refusalText(out, errOut, nil))
	}
	if s := refusalText(out, errOut, err); !strings.Contains(s, "--budget") {
		t.Fatalf("a managed creation with no amount was not refused for the missing amount:\n%s", s)
	}
	noMarker(t, r.gcs, bucket)
	r.nothingDone(t, "a managed creation refused for its missing amount")
}

// --create-project --managed on a project that already exists: the project
// stage adopts it and creates nothing, so there is no creation to record and
// markCreated writes no fact. The owner asked for a managed project, and
// getting an unmarked one with no word about it is the failure this pins:
// the run must say --managed was not applied and that no fact was written.
// The bucket is seeded as the managed creation's is, so the absence of the
// fact is about the gate and not about an unwritable bucket.
func TestManagedOnAnExistingProjectSaysItWasNotApplied(t *testing.T) {
	r := newProjectRig(t)
	const number = 424242
	bucket := "fugaro-runs-" + newProj
	r.crm.AddProject(newProj, number)
	r.gcs.AddProject(newProj, number)
	r.bill.SetBilling(newProj, true)
	r.bill.AddAccount(billingAcc, true)
	r.gcs.AddBucket(bucket, number, managedBucketLabels)
	roles := map[string]string{}
	for _, role := range []string{"launcher", "job_runner", "build_submitter", "tag_mover"} {
		roles[role] = "projects/" + newProj + "/roles/fugaro" + strings.ToUpper(role[:1]) + role[1:]
	}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"runs_bucket":               bucket,
		"registry_host":             "us-east5-docker.pkg.dev/" + newProj,
		"scheduler_service_account": "fugaro-scheduler@" + newProj + ".iam.gserviceaccount.com",
		"role_ids":                  roles,
	})}
	r.save(t)
	out, errOut, err := r.run(t, true, newProj+"\n"+initProjectName+"\n"+initProjectName+"\n",
		"--create-project", "--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc)
	all := refusalText(out, errOut, err)
	if err != nil {
		t.Fatalf("the run failed, so the warning would be about a failure rather than about adoption: %v\n%s", err, all)
	}
	if n := len(r.crm.Creates()); n != 0 {
		t.Fatalf("%d project(s) created: the project already existed:\n%s", n, all)
	}
	noMarker(t, r.gcs, bucket)
	if !strings.Contains(all, "--managed was not applied") {
		t.Errorf("the run adopted an existing project with --managed and did not say --managed was not applied:\n%s", all)
	}
	if !strings.Contains(all, "no creation fact was written") {
		t.Errorf("the run did not say that no creation fact was written:\n%s", all)
	}
}
