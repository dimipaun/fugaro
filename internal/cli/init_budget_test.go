package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// The budget of a creation: the amount and the currency, recorded in the
// project's own creation fact so a later run and a restore can read what was
// chosen. Since T2a of docs/managed-flag-ergonomics the fact records one for
// EVERY creating run, whether or not --managed was passed (D2212), and a run
// nobody chose a figure on records the defaults (D2211). What D64 asked of a
// --managed creation alone — an amount, its currency and the billing account,
// refused by name where any of the three is missing — is what
// TestManagedBudgetIsRequiredAtCreation pins, and T2c drops with the flag.
//
// The harness is init_managed_test.go's case (c), the one run in the tree
// that really writes the fact: the project this run creates through
// projects.create, its own runs bucket (labelled and numbered as the
// installation builds it, because managed.WriteMarker proves both before it
// writes anything), and the installation's outputs naming that bucket. What
// is asserted is the refusal, the object's bytes, the installation's own
// budget block, and what the run left undone.

// newManagedCreateRig is the create rig of a run that creates the project,
// with the created project's own runs bucket seeded as the installation
// leaves it: everything markCreated reads before it writes the fact.
func newManagedCreateRig(t *testing.T) (*projectRig, string) {
	t.Helper()
	r := newProjectRig(t)
	r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
	bucket := seedRunsBucket(t, r, gcpfake.FirstCreatedProjectNumber)
	r.bill.AddAccount(billingAcc, true)
	return r, bucket
}

// seedRunsBucket is the runs bucket of a project the run installs into, seeded
// as the installation leaves it: the bucket under the default name, labelled
// and numbered as managed.WriteMarker proves it, and the outputs that name it.
// number is the project's, which the marker is checked against — the fake's
// first for a project this run creates, the rig's own for one that is already
// there.
func seedRunsBucket(t *testing.T, r *projectRig, number uint64) string {
	t.Helper()
	bucket := "fugaro-runs-" + newProj
	r.gcs.AddProject(newProj, number)
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
	return bucket
}

// createBudgetBlock is the budget block the create rig's own run left in the
// installation's variables, as one JSON string. The work directory is keyed by
// the GCP project the run installs into (initRig.workdir is keyed by the
// project init installs into), which for this rig is the one it creates or
// adopts; an absent block reads as "null".
func createBudgetBlock(t *testing.T, r *projectRig) string {
	t.Helper()
	dir, err := infra.InstallationWorkdir(os.Getenv, newProj)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(infra.WorkdirVarsPath(dir, "installation"))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	block, err := json.Marshal(v["budget"])
	if err != nil {
		t.Fatal(err)
	}
	return string(block)
}

// creationFact is the object a creating run leaves in the project's runs
// bucket, as one JSON string: the fact at the version that records a budget
// (D1543), with the figures the run recorded. Since T2a of
// docs/managed-flag-ergonomics every creating run records one, whether or not
// --managed was passed, so this is the want of every test that creates a
// project — including the ones whose run chose no figures and therefore
// recorded the defaults (defaultCreationFact).
func creationFact(gcp string, amount int64, currency string) string {
	return fmt.Sprintf(`{"version":4,"gcp_project":%q,"budget":{"amount":%d,"currency":%q}}`, gcp, amount, currency)
}

// defaultCreationFact is that fact where nobody chose the figures: the defaults
// (D2211, D2212).
func defaultCreationFact(gcp string) string {
	return creationFact(gcp, managed.DefaultBudgetAmount, managed.DefaultBudgetCurrency)
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

	// --managed is about the project this run creates: a project setup
	// created earlier gets its budget from the owner's own command instead,
	// never from init (D58, Task 6.1), and update --firebase-project adds Firebase to such a project, which is
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
		if !strings.Contains(s, "first run of fugaro setup") {
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
		// Version 4 and no count: the amount alone is what the fact records,
		// and a creation writes it that way from the start rather than writing
		// the version a release behind wrote (D1543).
		want := `{"version":4,"gcp_project":"` + newProj + `","budget":{"amount":7,"currency":"USD"}}`
		if b != want {
			t.Errorf("gs://%s/%s = %s, want %s", bucket, managed.MarkerObject, b, want)
		}
	})

}

// TestSetupCreatingRunRecordsDefaultBudget: a run that creates the project
// records a budget whether or not --managed was passed (T2a of
// docs/managed-flag-ergonomics; D2212) — the creation fact is what a later run
// and the carry read (R5, R7), and a fact that records no amount leaves the
// alert with nothing to keep it by. With nothing chosen on the flags or at the
// question the figures are the defaults (D2211), and the same figures are the
// installation's alert, on the account the run links: a project this run
// creates has no billing account of its own, so the link is the only one there
// is (D2216).
//
// This is the task's red step: before it, a creation without --managed wrote
// the version-1 fact and created no alert at all.
func TestSetupCreatingRunRecordsDefaultBudget(t *testing.T) {
	r, bucket := newManagedCreateRig(t)
	out, errOut, err := r.run(t, true, createStdin,
		"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New")
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
	if want := `{"version":4,"gcp_project":"` + newProj + `","budget":{"amount":100,"currency":"USD"}}`; b != want {
		t.Errorf("gs://%s/%s = %s, want %s: a creating run records its budget, 100 USD where nobody chose one", bucket, managed.MarkerObject, b, want)
	}
	got := createBudgetBlock(t, r)
	if want := `{"amount":100,"billing_account":"` + billingAcc + `","currency_code":"USD"}`; got != want {
		t.Errorf("the installation's budget = %s, want %s: the alert is created with the same figures, on the account the run links", got, want)
	}
}

// TestSetupBillingAccountDefaultsToLinked: the account a creating run's alert
// is on is the one the run links, where --billing-account is not given
// (D2216). The owner names the account once, on --link-billing, and the link
// and the alert take it: a run that named an amount and no account used to be
// refused at the door, which is the second half of this test.
func TestSetupBillingAccountDefaultsToLinked(t *testing.T) {
	r, bucket := newManagedCreateRig(t)
	out, errOut, err := r.run(t, true, createStdin,
		"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
		"--budget", "7", "--budget-currency", "EUR")
	if err != nil {
		t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
	}
	got := createBudgetBlock(t, r)
	if want := `{"amount":7,"billing_account":"` + billingAcc + `","currency_code":"EUR"}`; got != want {
		t.Errorf("the installation's budget = %s, want %s: the alert is on the account the run links", got, want)
	}
	if b, ok := markerAt(t, r.gcs, bucket); !ok || b != `{"version":4,"gcp_project":"`+newProj+`","budget":{"amount":7,"currency":"EUR"}}` {
		t.Errorf("gs://%s/%s = %q (present %v): the fact records the figures the alert was created with", bucket, managed.MarkerObject, b, ok)
	}
}

// TestSetupExplicitBudgetWins: the flags are what a run was told, and the
// defaults are only what it takes where nobody chose (D2212) — an explicit
// amount and currency are the ones recorded and the ones the alert is created
// with, and an explicit --billing-account wins over the account the run links
// (D2216).
func TestSetupExplicitBudgetWins(t *testing.T) {
	const other = "111111-222222-333333"
	r, bucket := newManagedCreateRig(t)
	out, errOut, err := r.run(t, true, createStdin,
		"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
		"--budget", "250", "--budget-currency", "EUR", "--billing-account", other)
	if err != nil {
		t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
	}
	got := createBudgetBlock(t, r)
	if want := `{"amount":250,"billing_account":"` + other + `","currency_code":"EUR"}`; got != want {
		t.Errorf("the installation's budget = %s, want %s: the flags are the run's own figures", got, want)
	}
	if b, ok := markerAt(t, r.gcs, bucket); !ok || b != `{"version":4,"gcp_project":"`+newProj+`","budget":{"amount":250,"currency":"EUR"}}` {
		t.Errorf("gs://%s/%s = %q (present %v), want the flags' figures, not the defaults", bucket, managed.MarkerObject, b, ok)
	}
}

// TestSetupNoLinkedAccountRecordsAmountAndPrintsCommand: with no account
// linked the run creates no alert — a budget block with no billing account is
// one the installation module's own validation refuses, and an alert on an
// account nobody chose is the other answer this does not take — and it records
// the amount in the creation fact anyway, printing the one command that adds
// the alert (D2216). The order matters twice over: the amount in the fact is
// what the next run's carry keeps the alert by (R5, R7), and the line is the
// door's rather than the question's, because this run asked nothing (a project
// that is already there keeps the budget its own, askBudget).
//
// The project is one an earlier run of fugaro setup made Fugaro's, with billing
// already linked and nothing under --link-billing here: the run resumes it, and
// no account is named anywhere — neither the flag nor the wizard, which asks
// nothing on a run with a project config.
func TestSetupNoLinkedAccountRecordsAmountAndPrintsCommand(t *testing.T) {
	r := newProjectRig(t)
	const number = 424242
	// The project is one an earlier run made Fugaro's, so this run resumes it:
	// a project this run created itself would pause at the billing question
	// before any installation existed, and the fact is written at the end of
	// the installation's apply.
	r.existing(true)
	bucket := seedRunsBucket(t, r, number)
	out, errOut, err := r.run(t, true, newProj+"\n"+initProjectName+"\n", "--create-project")
	all := refusalText(out, errOut, err)
	if err != nil {
		t.Fatalf("the adopting run: %v\n%s", err, all)
	}
	if n := len(r.crm.Creates()); n != 0 {
		t.Fatalf("%d project(s) created: the project is adopted\n%s", n, all)
	}
	if b, ok := markerAt(t, r.gcs, bucket); !ok || b != `{"version":4,"gcp_project":"`+newProj+`","budget":{"amount":100,"currency":"USD"}}` {
		t.Errorf("gs://%s/%s = %q (present %v): the run records the amount with no account to put an alert on", bucket, managed.MarkerObject, b, ok)
	}
	if b := createBudgetBlock(t, r); b != "null" {
		t.Errorf("the installation's budget = %s, want none: a budget with no billing account is one the module refuses", b)
	}
	for _, want := range []string{"no alert was created", "fugaro update --budget 100 --budget-currency USD --billing-account"} {
		if !strings.Contains(all, want) {
			t.Errorf("the run does not say %q:\n%s", want, all)
		}
	}
}

// The flag carries no amount of its own, on a managed creation or any other:
// an absent --budget is 0, and D2212's 100 is a figure a creating run's own
// code takes (budgetFigures), never one the flag's default puts there. What
// D64 keeps while the flag lives is the other half: a --managed creation with
// no amount is still refused, and the refusal is the amount's rather than the
// three flags' — so a constant such as `defaultManagedBudget = 50` wired into
// the flag or into the refusal (a run given no amount creates nothing, so a
// fallback has nowhere to be written from) fails here. T2c drops the flag and
// with it the refusal; the flag's own default is pinned by no other test.
func TestManagedBudgetHasNoDefault(t *testing.T) {
	f := newSetupCmd().Flags().Lookup("budget")
	if f == nil {
		t.Fatal("fugaro setup has no --budget flag")
	}
	if f.DefValue != "0" {
		t.Errorf("fugaro setup --budget defaults to %q, want 0: a managed creation has no amount to fall back on (D64), and 0 is what an absent flag means", f.DefValue)
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

// --create-project --managed on a project an earlier run of fugaro setup
// created (it carries the creation label fugaro_created=true, which only the
// creating run's create request writes): the project stage resumes it and
// creates nothing in this run, and the project is still Fugaro's creation, so
// markCreated records the fact — the object with the budget this run was
// given, and the fugaro_managed label — exactly as the creating run would have
// (the owner's ruling of 2026-10-23: a resumed setup is managed by default). The owner asked for a managed project, and
// getting an unmarked one with no word about it is the failure this pins:
// the run must say --managed was not applied and that no fact was written.
// The bucket is seeded as the managed creation's is, so the absence of the
// fact is about the gate and not about an unwritable bucket.
func TestAResumedCreationIsMarkedManaged(t *testing.T) {
	r := newProjectRig(t)
	const number = 424242
	bucket := "fugaro-runs-" + newProj
	r.crm.AddProject(newProj, number)
	r.crm.SetLabels(newProj, map[string]string{infra.CreatedLabelKey: infra.CreatedLabelValue})
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
	out, errOut, err := r.run(t, true, newProj+"\n"+initProjectName+"\n"+initProjectName+"\n"+newProj+"\n",
		"--create-project", "--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc)
	all := refusalText(out, errOut, err)
	if err != nil {
		t.Fatalf("the run failed, so the warning would be about a failure rather than about the resume: %v\n%s", err, all)
	}
	if !strings.Contains(all, "this MARKS project "+newProj+" as Fugaro-managed") || !strings.Contains(all, "this run resumed the creation of project "+newProj) {
		t.Errorf("the mark was not behind the typed ID, or not said to be a resume:\n%s", all)
	}
	if n := len(r.crm.Creates()); n != 0 {
		t.Fatalf("%d project(s) created: the project already existed:\n%s", n, all)
	}
	got, ok := markerAt(t, r.gcs, bucket)
	if !ok {
		t.Fatalf("a resumed creation wrote no gs://%s/%s:\n%s", bucket, managed.MarkerObject, all)
	}
	if want := `{"version":4,"gcp_project":"` + newProj + `","budget":{"amount":7,"currency":"USD"}}`; got != want {
		t.Errorf("gs://%s/%s = %s, want %s", bucket, managed.MarkerObject, got, want)
	}
	if v := r.crm.Labels(newProj)[managed.ProjectLabelKey]; v != managed.ProjectLabelValue {
		t.Errorf("a resumed creation is not labelled %s=%s (got %q):\n%s", managed.ProjectLabelKey, managed.ProjectLabelValue, v, all)
	}
	if strings.Contains(all, "--managed was not applied") {
		t.Errorf("the run said --managed was not applied, and it marked the resumed creation:\n%s", all)
	}
}

// The other half of that warning, and the reason it is two: an empty
// r.res.Created is what a resumed creation leaves behind and also what every other way
// a --managed run creates nothing leaves behind, so the message may only say
// "already existed" when the project stage actually read a project that was
// already there. Here it does not exist and no person is at the run: the
// confirmation the run stops at is one nothing but a person at a terminal
// gives (T7, docs/no-prompt-policy), and the run leaves the step for them, so
// nothing was adopted and nothing was created: the warning says the creation
// did not happen, and a reader is not sent to look for a project that is not
// there.
func TestManagedWarningDoesNotClaimAdoptionWithoutOne(t *testing.T) {
	r := newProjectRig(t)
	r.bill.AddAccount(billingAcc, true)
	noTerminal(t)
	out, errOut, err := r.run(t, false, "",
		"--create-project", "--managed", "--budget", "7", "--budget-currency", "USD", "--billing-account", billingAcc)
	all := refusalText(out, errOut, err)
	if err == nil {
		t.Fatalf("a creation nobody was at to confirm returned no error:\n%s", all)
	}
	// The run really did reach a step that wanted a person and stop there
	// rather than refuse earlier, so the warning below is about a creation
	// that did not happen.
	r.nothingDone(t, "a managed creation whose confirmation nobody was at to give")
	if !strings.Contains(all, "needs your own confirmation, which no flag gives") {
		t.Fatalf("the run did not stop at the project's own confirmation, which no flag gives:\n%s", all)
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

	// The amount's own range is the whole of what is judged here: a managed
	// creation records the amount and nothing else, whatever the figure.
	t.Run("the largest amount in the range", func(t *testing.T) {
		r, bucket := newManagedCreateRig(t)
		out, errOut, err := r.run(t, true, createStdin,
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
			"--managed", "--budget", "10000000", "--budget-currency", "EUR", "--billing-account", billingAcc)
		if err != nil {
			t.Fatalf("%v\n%s", err, refusalText(out, errOut, nil))
		}
		b, ok := markerAt(t, r.gcs, bucket)
		want := `{"version":4,"gcp_project":"` + newProj + `","budget":{"amount":10000000,"currency":"EUR"}}`
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

// A resumed creation is never marked without a person at the run's terminal:
// fugaro_created=true is a label anyone with resourcemanager.projects.update
// can set, so it only says which project to ask about, and the mark is written
// by confirmResumedMark, whose bar is that person alone — no --json, no coding
// agent's session, and nothing typed (T7). A run without one writes neither
// half of the fact (the object and the fugaro_managed label), and none of them
// announces a mark it did not make.
//
// What such a run cannot do is say how to mark the project: the advice ("run
// fugaro managed flag ...") comes from the resumed-mark step, which is reached
// once the installation's runs bucket is known, and a run nobody is at never
// gets past the installation's own apply
// (TestAResumedCreationIsMarkedManaged is the half that does).
func TestAResumedCreationIsNeverMarkedWithoutAPersonAtTheTerminal(t *testing.T) {
	cases := map[string]struct {
		json  bool
		agent bool
	}{
		"no terminal":              {},
		"--json":                   {json: true},
		"a coding agent's session": {agent: true},
	}
	for name, tc := range cases {
		agent := tc.agent
		t.Run(name, func(t *testing.T) {
			r := newProjectRig(t)
			const number = 424242
			bucket := "fugaro-runs-" + newProj
			r.crm.AddProject(newProj, number)
			r.crm.SetLabels(newProj, map[string]string{infra.CreatedLabelKey: infra.CreatedLabelValue})
			r.gcs.AddProject(newProj, number)
			r.fb.AddFirebase(newProj)
			r.bill.SetBilling(newProj, true)
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
			args := []string{"--create-project"}
			if tc.json {
				args = append(args, "--json")
			}
			if agent {
				t.Setenv("CLAUDECODE", "1")
			} else {
				noTerminal(t)
			}
			out, errOut, err := r.run(t, false, "", args...)
			all := refusalText(out, errOut, err)
			if got, ok := markerAt(t, r.gcs, bucket); ok {
				t.Errorf("a resumed creation was marked with nobody at the terminal: gs://%s/%s = %s\n%s", bucket, managed.MarkerObject, got, all)
			}
			if v := r.crm.Labels(newProj)[managed.ProjectLabelKey]; v != "" {
				t.Errorf("a resumed creation was labelled %s=%s with nobody at the terminal\n%s", managed.ProjectLabelKey, v, all)
			}
			if strings.Contains(all, "this MARKS project "+newProj+" as Fugaro-managed") {
				t.Errorf("the run announced a mark no person at the terminal confirmed:\n%s", all)
			}
		})
	}
}

// --- The carry a plain update makes (T0 of docs/managed-flag-ergonomics) ---

// budgetAlertAccount is the billing account the rig's alert sits on, as the
// installation root's state records it: the one figure of the alert that no
// local document holds.
const budgetAlertAccount = "0123AB-4567CD-89EF01"

// budgetAlertState is the installation root's state object for an installation
// that holds the alert: format version 4, with the module's resource and the
// account it was applied to.
func budgetAlertState() []byte {
	return []byte(`{"version":4,"terraform_version":"1.9.8","serial":1,"lineage":"x","outputs":{},"resources":[` +
		`{"mode":"managed","type":"google_billing_budget","name":"this","instances":[{"attributes":{"billing_account":"` +
		budgetAlertAccount + `","amount":{"units":"250","nanos":0}}}]}]}`)
}

// budgetAlertRig is an installation that holds an alert, as the two records a
// machine without a work directory can reach describe it: the state bucket with
// the root's state object, and the runs bucket with the creation fact the amount
// was recorded in — the fact marked as this installation's own, with the number,
// the bucket label and the project label the managed read proves before it
// believes one (managed.ReadBudget).
func budgetAlertRig(t *testing.T) *initRig {
	t.Helper()
	r := updateRig(t)
	fakeTerminal(t)
	labelled(r.crm, initProject)
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	r.gcs.Put(initRunsBucket, managed.MarkerObject,
		[]byte(`{"version":4,"gcp_project":"`+initProject+`","budget":{"amount":250,"currency":"EUR"}}`))
	r.gcs.Put(initStateBucket, infra.StatePrefixInstallation+"/default.tfstate", budgetAlertState())
	return r
}

// carriedBudgetIs says what is wrong with the budget the run wrote, or "" when
// it is the alert the rig's cloud records: 250 EUR on the account the state
// names, and no fourth key — a block that carried anything else would be a
// figure of this program's own rather than the installation's.
func carriedBudgetIs(b map[string]any) string {
	if len(b) != 3 {
		return "it does not hold exactly the three figures of an alert"
	}
	if b["billing_account"] != budgetAlertAccount {
		return "the account is not the one the installation's state records"
	}
	if b["amount"] != float64(250) {
		return "the amount is not the one the project's creation fact records"
	}
	if b["currency_code"] != "EUR" {
		return "the currency is not the one the project's creation fact records"
	}
	return ""
}

// budgetOf is the budget the run wrote into the installation's tfvars, and
// whether it is there at all. The document is the assertion rather than the
// plan, because the plan is a function of it: a document without the key is the
// plan that deletes module.installation.google_billing_budget.this[0], which the
// guard refuses by name (TestInitRefusesDeletePlan) — so a run that stops there
// is the same failure seen one step later.
func budgetOf(t *testing.T, r *initRig) (map[string]any, bool) {
	t.Helper()
	v := r.tfvars(t)
	b, ok := v["budget"].(map[string]any)
	return b, ok
}

// TestUpdateCarriesTheBudget: an installation that holds a budget alert keeps it
// through a plain `fugaro update`. The run answers nothing about the budget —
// no flag, no member of the local config — and the alert survives it, which is
// CLAUDE.md's HARD RULE that `fugaro update` is the whole upgrade and R5's
// requirement that the alert survives any machine.
//
// The figures come from the cloud and not from the work directory, and the
// assertion says so: the work directory here holds a previous document that
// names a DIFFERENT account and amount, as an installation whose alert moved
// would, and what lands is what the state and the creation fact record (R7).
func TestUpdateCarriesTheBudget(t *testing.T) {
	r := budgetAlertRig(t)
	// The installation as the last apply left it: a document of its own, naming
	// an account that is not the one the state holds.
	root := r.root()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := `{"project": "` + initProject + `", "region": "us-east5", ` +
		`"budget": {"billing_account": "ZZZZZZ-ZZZZZZ-ZZZZZZ", "amount": 1, "currency_code": "USD"}}`
	if err := os.WriteFile(filepath.Join(root, infra.VarsFile), []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("a plain update of an installation that holds an alert: %v\n%s%s", err, out, errOut)
	}
	b, ok := budgetOf(t, r)
	if !ok {
		t.Fatalf("the update wrote no budget into the installation's tfvars, so its plan deletes the alert:\n%s%s", out, errOut)
	}
	if why := carriedBudgetIs(b); why != "" {
		t.Errorf("the carried budget = %v: %s. The amount and the currency are the project's creation fact's, and the account is the alert's own resource in the installation's state — the work directory's own figures are not a source (R7)", b, why)
	}
}

// TestUpdateCarriesTheBudgetWithAnEmptyWorkdir: the same installation, and a
// machine that has never run its apply — no work directory, no tfvars, nothing
// under XDG_STATE_HOME at all. The carry is what makes the alert survive a
// fresh machine, a cleaned one, or a console on another host (R5, R7), so the
// two records it reads are the only ones it may need, and this is the test that
// says nothing local is one of them.
func TestUpdateCarriesTheBudgetWithAnEmptyWorkdir(t *testing.T) {
	r := budgetAlertRig(t)
	if _, err := os.Stat(r.workdir()); !os.IsNotExist(err) {
		t.Fatalf("the rig has a work directory before the run (%v), so this test would not be about an empty one", err)
	}

	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("a plain update from a machine with no work directory: %v\n%s%s", err, out, errOut)
	}
	b, ok := budgetOf(t, r)
	if !ok {
		t.Fatalf("the update wrote no budget into the installation's tfvars, so its plan deletes the alert:\n%s%s", out, errOut)
	}
	if why := carriedBudgetIs(b); why != "" {
		t.Errorf("the carried budget = %v: %s. A machine with no work directory reads the amount from the creation fact and the account from the state, and needs nothing else", b, why)
	}
}

// carriedBudgetRig is an installation that is managed and holds a budget alert:
// the two marks of a managed project (the creation fact that records the amount
// and the label on the project itself, which the read requires together), and
// the installation root's own state object, whose budget resource records the
// billing account. It is updateRig — an installation that is there and whose
// plan reads back empty — plus those records, with the terminal a plain
// `fugaro update` confirms its ordinary steps at.
func carriedBudgetRig(t *testing.T, amount int64, currency string) *initRig {
	t.Helper()
	noAgentSession(t)
	noRunSession(t)
	r := updateRig(t)
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	labelled(r.crm, initProject)
	seedFlagFact(t, r, managed.Marker{
		Version:    managed.MarkerVersionNoBuilds,
		GCPProject: initProject,
		Budget:     &managed.Budget{Amount: amount, Currency: currency},
	})
	r.gcs.Put(initStateBucket, infra.StatePrefixInstallation+"/default.tfstate", installationStateWithBudget(billingAcc))
	return r
}

// installationStateWithBudget is a version-4 state document as terraform's GCS
// backend writes it, holding the installation module's budget resource with the
// billing account given. Written out here rather than shared with
// internal/infra's own test: the shape is terraform's, and a helper shared with
// the package under test would pin the two to each other instead of pinning the
// document terraform really writes.
func installationStateWithBudget(account string) []byte {
	return []byte(`{"version":4,"terraform_version":"1.9.8","serial":3,"lineage":"a1","outputs":{},` +
		`"resources":[{"mode":"managed","module":"module.installation","type":"google_billing_budget","name":"this",` +
		`"instances":[{"schema_version":0,"attributes":{"billing_account":` + fmt.Sprintf("%q", account) +
		`,"display_name":"fugaro monthly budget"}}]}]}`)
}

// seedWorkdirBudget writes a budget block into the installation workdir's tfvars
// before a run, as a machine that planned this installation before would hold
// it. The figures are the caller's, and every caller passes ones the
// installation's own records do not hold: what a run writes from the file is
// then visibly wrong rather than merely missing.
func seedWorkdirBudget(t *testing.T, r *initRig, account string, amount int64, currency string) {
	t.Helper()
	dir, err := infra.InstallationWorkdir(os.Getenv, initProject)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"project": initProject, "budget": map[string]any{
		"billing_account": account, "amount": amount, "currency_code": currency}})
	if err != nil {
		t.Fatal(err)
	}
	path := infra.WorkdirVarsPath(dir, "installation")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// budgetBlockOf is the budget block the run left in the installation's
// variables, as one JSON string, so a test pins the three fields and nothing
// else: an absent block reads as "null" and fails every comparison here.
func budgetBlockOf(t *testing.T, r *initRig) string {
	t.Helper()
	b, err := json.Marshal(r.tfvars(t)["budget"])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestCreatingSetupRefusesABudgetWithoutAnAccount: the carry that lets an
// explicit --budget take the account from the installation's state (R7) has
// nothing to take it from on a run that creates the project — the state is
// what the run is about to make. The refusal is at the door, before the
// project stage, and not at the installation stage's own state read: by then
// the project, its Firebase link, its billing and its APIs are all made, and a
// run that ended there would have changed cloud for nothing.
func TestCreatingSetupRefusesABudgetWithoutAnAccount(t *testing.T) {
	r := newProjectRig(t)
	out, errOut, err := r.run(t, true, "", "--create-project", "--budget", "7", "--budget-currency", "USD")
	if err == nil {
		t.Fatalf("a creating run took a budget with no account to carry it from:\n%s", refusalText(out, errOut, nil))
	}
	said := refusalText(out, errOut, err)
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("exit %d, want %d:\n%s", code, ExitUserError, said)
	}
	if !strings.Contains(said, "--billing-account") {
		t.Errorf("the refusal does not name the flag that completes the block:\n%s", said)
	}
	r.nothingDone(t, "a creating run refused for a budget with no account")
	if n := r.posts(t); n != 0 {
		t.Errorf("%d write call(s) were sent", n)
	}
}

// TestUpdateExplicitBudgetTakesTheCarriedAccount: the explicit flags replace
// what is carried (R5), and a --budget with no --billing-account takes the account the
// installation's state records (R7), refused by name where there is none.
func TestUpdateExplicitBudgetTakesTheCarriedAccount(t *testing.T) {
	// An explicit --budget with no --billing-account takes the carried account
	// (R7): the amount and the currency are the flags', the account is the
	// installation's, and the run plans no delete of the alert for the missing
	// flag.
	t.Run("--budget with no --billing-account takes the carried account", func(t *testing.T) {
		r := carriedBudgetRig(t, 250, "USD")
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, "", "update", "--budget", "500", "--budget-currency", "EUR")
		if err != nil {
			t.Fatalf("fugaro update --budget: %v\n%s\n%s", err, out, errOut)
		}
		if want := `{"amount":500,"billing_account":"` + billingAcc + `","currency_code":"EUR"}`; budgetBlockOf(t, r) != want {
			t.Errorf("the variables' budget = %s, want the flags' amount and currency with the carried account %s", budgetBlockOf(t, r), want)
		}
	})

	// The whole trio replaces what is carried: nothing of the records is left in
	// the block, so an owner who names all three gets exactly what they asked
	// for.
	t.Run("the trio replaces what is carried", func(t *testing.T) {
		r := carriedBudgetRig(t, 250, "USD")
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, "", "update", "--budget", "500", "--budget-currency", "EUR",
			"--billing-account", "111111-222222-333333")
		if err != nil {
			t.Fatalf("fugaro update with the trio: %v\n%s\n%s", err, out, errOut)
		}
		if want := `{"amount":500,"billing_account":"111111-222222-333333","currency_code":"EUR"}`; budgetBlockOf(t, r) != want {
			t.Errorf("the variables' budget = %s, want the flags' own block %s", budgetBlockOf(t, r), want)
		}
	})

	// And with nothing to take an account from, the flag is refused by name
	// rather than planned with an empty account: the module cannot create an
	// alert without one, and a run that wrote the block anyway would be the
	// alert's deletion in a plan that looks complete.
	t.Run("--budget with nothing carried names the missing flag", func(t *testing.T) {
		noAgentSession(t)
		noRunSession(t)
		r := updateRig(t) // no creation fact, no state object: nothing to carry
		fakeTerminal(t)
		out, errOut, err := executeStdin(t, "", "update", "--budget", "500", "--budget-currency", "USD")
		if err == nil {
			t.Fatalf("fugaro update --budget with nothing to carry was accepted:\n%s\n%s", out, errOut)
		}
		said := refusalText(out, errOut, err)
		if code := ExitCode(err); code != ExitUserError {
			t.Errorf("exit %d, want %d:\n%s", code, ExitUserError, said)
		}
		if !strings.Contains(said, "--billing-account") {
			t.Errorf("the refusal does not name the flag that completes the block:\n%s", said)
		}
		// The refusal stands before any plan exists: nothing was planned with a
		// half block, which is the plan that would delete the alert.
		if n := len(r.ran(t, "plan")); n != 0 {
			t.Errorf("the refused run planned %d root(s) anyway:\n%s", n, said)
		}
	})
}

// TestPrintVarsSaysItCarriesNoBudget: --print-vars reads no cloud by design, so
// it carries no budget block, and it says so rather than printing an
// installation's values that read as an installation without an alert — the
// document is what an owner applies by hand, and the absent key there is the
// alert's deletion. Given the trio, the block is the flags' and there is
// nothing to say.
func TestPrintVarsSaysItCarriesNoBudget(t *testing.T) {
	newInitRig(t)
	out, stderr, err := executeStdin(t, "", "setup", "--print-vars", "--no-log-isolation")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "budget alert this installation holds is not in these values") {
		t.Errorf("--print-vars prints no budget block and does not say so:\n%s", stderr)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if v["budget"] != nil {
		t.Errorf("--print-vars printed a budget block it read no cloud for: %v", v["budget"])
	}

	newInitRig(t)
	out, stderr, err = executeStdin(t, "", "setup", "--print-vars", "--no-log-isolation",
		"--budget", "50", "--budget-currency", "USD", "--billing-account", billingAcc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "budget alert this installation holds is not in these values") {
		t.Errorf("--print-vars warned about a budget it was given:\n%s", stderr)
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if b, err := json.Marshal(v["budget"]); err != nil || string(b) != `{"amount":50,"billing_account":"`+billingAcc+`","currency_code":"USD"}` {
		t.Errorf("the printed budget = %s, %v, want the flags' own block", b, err)
	}
}

// budgetUpdateChange is the change a run that moved its alert's amount plans: an
// in-place update of the installation module's budget resource, on the billing
// account given. It is what R10's drift plans on the next run and what an
// explicit --budget plans once the carry has completed the block with the
// installation's own account.
func budgetUpdateChange(account, amount string) planChange {
	return planChange{"address": "module.installation.google_billing_budget.this[0]", "type": "google_billing_budget",
		"change": map[string]any{"actions": []string{"update"}, "before": map[string]any{"amount": "200"},
			"after": map[string]any{"billing_account": account, "amount": amount, "currency_code": "USD"}}}
}

// TestCarriedBudgetCoversThePlansBudgetChange: the account the carry took out
// of the installation's state is the account the review announced, so the plan
// of the run's own carried block is covered by the run's one confirmation. The
// cover admits a create or update of google_billing_budget only on an account
// it was shown (tf.Cover.Billing, cover.go's own rule), so a cover built from
// --billing-account alone makes this plan one the review never announced — the
// refusal that stops every run that cannot ask (--json, the console, a chained
// run) and, at the owner's terminal, the banner over an update the run itself
// wrote the account for.
func TestCarriedBudgetCoversThePlansBudgetChange(t *testing.T) {
	r := carriedBudgetRig(t, 250, "USD")
	// The installation root's plan holds the alert's own update, and the root's
	// plan REPORTS it: a plan with nothing in it is never shown to the cover at
	// all (init.go's cycle reads the change off TF.Plan's exit code, and a run
	// whose plan is empty says "No changes" and applies nothing), so a rig left
	// at updateRig's empty plan would pass this test with the cover built from
	// the flags alone — which is the bug.
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", budgetUpdateChange(billingAcc, "250"))
	// The installation's project carries both halves of the managed fact (the
	// carry's rig reads its marker and label), so the step this plan reaches is
	// admitted by the gate and the admission is written down — and the record
	// names the account the run acts as, which is the credentials isolated here
	// and no network call.
	isolateGateCredentials(t)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	said := out + errOut
	if err != nil {
		said += ErrorText(err)
	}
	// The refusal's own words are looked for beside the banner's: which of the
	// two a run prints is the door it came through, and what this test is about
	// is the fact behind either — the account the carry took out of the state
	// is the one the review announced.
	if strings.Contains(said, "refused by cover") || strings.Contains(said, "outside what the review announced") {
		t.Errorf("the plan of the run's own carried budget is not covered by the run's confirmation:\n%s", said)
	}
	if err != nil {
		t.Fatalf("fugaro update with the alert's own update in its plan: %v\n%s", err, said)
	}
}
