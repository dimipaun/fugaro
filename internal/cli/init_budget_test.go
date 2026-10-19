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

	// --managed is about the project this run creates: an existing project is
	// flagged by the owner's own command instead, never by init (D58, Task
	// 6.1), and init --firebase adds Firebase to such a project, which is
	// never what makes one managed (D73).
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

	// The same property where a fallback has room to live: the amount is
	// absent while the other two flags are there, so a refusal that only
	// counts flags sees a complete trio. Nothing else in this file reaches
	// that state — the subtest above passes none of the three and the one in
	// TestManagedBudgetIsRequiredAtCreation passes an amount without its
	// currency — and it is the state a constant such as
	// `if o.budget <= 0 { o.budget = 50 }` turns into a managed project whose
	// monthly line nobody chose. An absent amount and `--budget 0` are the
	// same run (the flag defaults to 0), and D64 refuses both: the refusal is
	// the amount's, not the other two flags'.
	t.Run("--managed with a currency and a billing account and no amount", func(t *testing.T) {
		r, bucket := newManagedCreateRig(t)
		out, errOut, err := r.run(t, true, createStdin,
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
			"--managed", "--budget-currency", "USD", "--billing-account", billingAcc)
		if err == nil {
			t.Fatalf("a managed creation with no amount was accepted because the other two flags were there:\n%s", refusalText(out, errOut, nil))
		}
		s := refusalText(out, errOut, err)
		if code := ExitCode(err); code != ExitUserError {
			t.Errorf("exit %d, want %d: a missing amount is the caller's to fix:\n%s", code, ExitUserError, s)
		}
		if !strings.Contains(s, "--budget ") {
			t.Errorf("the refusal does not name the missing amount:\n%s", s)
		}
		r.nothingDone(t, "a managed creation refused for its missing amount")
		noMarker(t, r.gcs, bucket)
	})
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
	// And the owner is not left with "not managed" as the end of it: since
	// Task 6.1 there is a command for exactly this project, so the warning
	// names it with the project ID the owner would type.
	if want := "fugaro managed flag " + newProj; !strings.Contains(all, want) {
		t.Errorf("the run did not say how such a project becomes managed (want %q):\n%s", want, all)
	}
}

// The other half of that warning, and the reason it is two: an empty
// r.res.Created is what adoption leaves behind and also what every other way
// a --managed run creates nothing leaves behind, so the message may only say
// "already existed" when the project stage actually read a project that was
// already there. Here it does not exist and the typed confirmation is
// declined, so nothing was adopted and nothing was created: the warning says
// the creation did not happen, and a reader is not sent to look for a project
// that is not there.
func TestManagedWarningDoesNotClaimAdoptionWithoutOne(t *testing.T) {
	r := newProjectRig(t)
	r.bill.AddAccount(billingAcc, true)
	out, errOut, err := r.run(t, true, "not-the-project-id\n",
		"--create-project", "--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc)
	all := refusalText(out, errOut, err)
	if err == nil {
		t.Fatalf("a declined creation returned no error:\n%s", all)
	}
	// The run really did reach the project stage's confirmation and stop
	// there, so the warning below is about a creation that did not happen
	// rather than about an earlier refusal.
	r.nothingDone(t, "a managed creation whose confirmation was declined")
	if !strings.Contains(all, "not confirmed") {
		t.Fatalf("the run did not stop at the project's typed confirmation:\n%s", all)
	}
	if !strings.Contains(all, "--managed was not applied") {
		t.Errorf("the run created no project under --managed and did not say --managed was not applied:\n%s", all)
	}
	if strings.Contains(all, "already existed") {
		t.Errorf("the run says the project already existed, and it does not (nothing was adopted and nothing was created):\n%s", all)
	}
	if !strings.Contains(all, "no creation fact was written") {
		t.Errorf("the run did not say that no creation fact was written:\n%s", all)
	}
}

// The figures a managed creation records are held, before anything is
// created, to the rule the creation fact's write and read share
// (managed.Budget.Check): a currency that is not three ASCII uppercase
// letters and an amount outside 1 to 10,000,000 whole units are refused, the
// run does nothing, and no fact is written. Without the check the run created
// the project and only the marker's write refused the figures — a managed
// project left unmarked by a typo.
func TestManagedBudgetFiguresAreChecked(t *testing.T) {
	for name, flags := range map[string][]string{
		"a lower-case currency":     {"--budget", "7", "--budget-currency", "usd"},
		"a four-letter currency":    {"--budget", "7", "--budget-currency", "USDX"},
		"an amount above the range": {"--budget", "10000001", "--budget-currency", "USD"},
		"the largest int64 amount":  {"--budget", "9223372036854775807", "--budget-currency", "USD"},
	} {
		t.Run(name, func(t *testing.T) {
			r, bucket := newManagedCreateRig(t)
			args := append([]string{"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
				"--managed", "--billing-account", billingAcc}, flags...)
			out, errOut, err := r.run(t, true, createStdin, args...)
			if err == nil {
				t.Fatalf("a managed creation with %s was accepted:\n%s", name, refusalText(out, errOut, nil))
			}
			s := refusalText(out, errOut, err)
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit %d, want %d:\n%s", code, ExitUserError, s)
			}
			if !strings.Contains(s, "ISO 4217") {
				t.Errorf("the refusal does not say what a budget must be:\n%s", s)
			}
			r.nothingDone(t, "a managed creation refused for its budget figures")
			noMarker(t, r.gcs, bucket)
		})
	}

	// The bound is a bound: the largest amount is a creation like any other.
	t.Run("the largest amount in the range", func(t *testing.T) {
		r, bucket := newManagedCreateRig(t)
		out, errOut, err := r.run(t, true, createStdin,
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
			"--managed", "--budget", "10000000", "--budget-currency", "EUR", "--billing-account", billingAcc)
		if err != nil {
			t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
		}
		b, ok := markerAt(t, r.gcs, bucket)
		want := `{"version":2,"gcp_project":"` + newProj + `","budget":{"amount":10000000,"currency":"EUR"}}`
		if !ok || b != want {
			t.Errorf("gs://%s/%s = %s (present %v), want %s", bucket, managed.MarkerObject, b, ok, want)
		}
	})
}

// A budget Terraform (or the Budget API behind it) rejects is never recorded:
// the budget is a resource of the installation root's apply, and markCreated
// writes the fact only once that apply has succeeded — it needs the runs
// bucket from r.res.Outputs, which writeConfig sets after a successful
// installation apply and nothing else sets in a creation run. So a creation
// whose installation apply fails (a currency that is not the billing
// account's, say) created the project and records nothing: unmarked, the
// fail-closed direction, and the warning says so. Pinned because the order is
// what makes the fact true, and a refactor that read the bucket name from
// the spec instead would record an amount no budget carries.
func TestManagedFactIsNotWrittenWhenTheInstallationApplyFails(t *testing.T) {
	r, bucket := newManagedCreateRig(t)
	r.script["apply"] = map[string]any{"exit": 1}
	r.save(t)
	out, errOut, err := r.run(t, true, createStdin,
		"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
		"--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc)
	if err == nil {
		t.Fatalf("the installation apply failed, yet init succeeded:\n%s", refusalText(out, errOut, nil))
	}
	if n := len(r.crm.Creates()); n != 1 {
		t.Fatalf("%d project(s) created, want the one this run created before the apply failed:\n%s", n, refusalText(out, errOut, err))
	}
	noMarker(t, r.gcs, bucket)
	if s := refusalText(out, errOut, err); !strings.Contains(s, "not marked") {
		t.Errorf("the run does not say the created project is not marked:\n%s", s)
	}
}
