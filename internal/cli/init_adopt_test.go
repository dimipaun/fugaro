package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The owner's ruling of 2026-11-22: `fugaro setup --gcp-project` may name a
// project that is already there, as long as it holds nothing of Fugaro's, and
// setup then uses it as if it had created it. init_adopt.go is the read that
// decides and the label the adopting run writes; these are the cases the
// ruling asks to be pinned.

// TestSetupAdoptsAnExistingEmptyProject: a project that is there, holds
// nothing of Fugaro's and is not being deleted is adopted — nothing is
// created, the run says so in one line, and the project carries
// fugaro_created=adopted afterwards. The value is "adopted" and not "true"
// because the label states the project's origin, and it is what a later run
// resumes the project by (TestResumedAdoptionIsMarkedManaged).
func TestSetupAdoptsAnExistingEmptyProject(t *testing.T) {
	t.Run("Firebase and billing already there", func(t *testing.T) {
		r := newProjectRig(t)
		r.foreign(true)
		out, _, err := r.run(t, true, newProj+"\n", "--create-project")
		if n := len(r.crm.Creates()); n != 0 {
			t.Fatalf("%d project(s) created: an existing project is adopted, not created (err %v):\n%s", n, err, out)
		}
		if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != infra.AdoptedLabelValue {
			t.Errorf("%s on the adopted project = %q, want %q (err %v):\n%s", infra.CreatedLabelKey, got, infra.AdoptedLabelValue, err, out)
		}
		if !strings.Contains(out, "adopted existing project "+newProj) {
			t.Errorf("the run does not say it adopted the project (err %v):\n%s", err, out)
		}
		if n := len(r.fb.Adds()); n != 0 {
			t.Errorf("Firebase added %d time(s): the project already has it", n)
		}
		if n := len(r.bill.Links()); n != 0 {
			t.Errorf("billing linked %d time(s): the project already has it", n)
		}
	})

	t.Run("no Firebase, billing linked", func(t *testing.T) {
		r := newProjectRig(t)
		r.crm.AddProject(newProj, 424242)
		r.gcs.AddProject(newProj, 424242)
		r.bill.SetBilling(newProj, false)
		r.bill.AddAccount(billingAcc, true)
		out, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
		if n := len(r.crm.Creates()); n != 0 {
			t.Fatalf("%d project(s) created (err %v):\n%s", n, err, out)
		}
		if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != infra.AdoptedLabelValue {
			t.Errorf("%s on the adopted project = %q, want %q (err %v):\n%s", infra.CreatedLabelKey, got, infra.AdoptedLabelValue, err, out)
		}
		if n := len(r.fb.Adds()); n != 1 {
			t.Errorf("Firebase added %d time(s), want once: the project has none", n)
		}
		if n := len(r.bill.Links()); n != 1 {
			t.Errorf("billing linked %d time(s), want once: the project has no link", n)
		}
	})
}

// TestSetupAdoptsWithBillingAlreadyLinked: a project that already has billing
// is not relinked, the billing question is not asked, and the run says so in
// one line — naming no account, which no mode of this stage prints.
func TestSetupAdoptsWithBillingAlreadyLinked(t *testing.T) {
	r := newProjectRig(t)
	r.foreign(true)
	r.bill.AddAccount(billingAcc, true)
	out, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", billingAcc)
	if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != infra.AdoptedLabelValue {
		t.Fatalf("%s on the project = %q, want %q (err %v):\n%s", infra.CreatedLabelKey, got, infra.AdoptedLabelValue, err, out)
	}
	if n := len(r.bill.Links()); n != 0 {
		t.Errorf("billing linked %d time(s): an existing link is never replaced", n)
	}
	if !strings.Contains(out, "billing is already linked to project "+newProj) {
		t.Errorf("the run does not say the billing link is already there (err %v):\n%s", err, out)
	}
	if strings.Contains(out, "link billing account "+billingAcc+" to project "+newProj) {
		t.Errorf("the billing link was confirmed: an existing link is never replaced:\n%s", out)
	}
}

// TestSetupAdoptsRefusesAProjectThatHoldsSomethingOfFugaros: the read that
// decides (init_adopt.go) is what stops the adoption, and the refusals are the
// ones the stage has always had. The runs bucket and the installation's state
// bucket are each enough, and so is the origin label's key carrying a value
// this program does not write — what the refusal itself says of "a label
// fugaro setup writes", and what init_adopt.go's list makes of that key. The
// two values this program does write are not a refusal: they are the resume
// (the last subtest here).
func TestSetupAdoptsRefusesAProjectThatHoldsSomethingOfFugaros(t *testing.T) {
	t.Run("the runs bucket", func(t *testing.T) {
		r := newProjectRig(t)
		r.foreign(true)
		r.installed()
		out, _, err := r.run(t, true, newProj+"\n", "--create-project")
		if ExitCode(err) != ExitUserError || !strings.Contains(out+errString(err), "holds something of Fugaro's") {
			t.Errorf("exit %d (%v), want the refusal:\n%s", ExitCode(err), err, out)
		}
		if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != "" {
			t.Errorf("the refused project was labelled %s=%s", infra.CreatedLabelKey, got)
		}
		r.nothingDone(t, "a project holding a runs bucket")
	})

	t.Run("the installation state bucket", func(t *testing.T) {
		r := newProjectRig(t)
		r.foreign(true)
		r.gcs.AddBucket("fugaro-tfstate-"+newProj, 424242, map[string]string{"fugaro": "tfstate"})
		out, _, err := r.run(t, true, newProj+"\n", "--create-project")
		if ExitCode(err) != ExitUserError || !strings.Contains(out+errString(err), "holds something of Fugaro's") {
			t.Errorf("exit %d (%v), want the refusal:\n%s", ExitCode(err), err, out)
		}
		if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != "" {
			t.Errorf("the refused project was labelled %s=%s", infra.CreatedLabelKey, got)
		}
	})

	t.Run("the origin label with a value fugaro setup never writes", func(t *testing.T) {
		r := newProjectRig(t)
		r.foreign(true)
		// Anyone who may update the project can set the label, so its value
		// can be one this program never writes. The key alone still says the
		// project is Fugaro's, so it is refused rather than adopted
		// (infra.ProjectInfo.CreatedLabel, init_adopt.go): a run must not
		// adopt a project whose origin it cannot read.
		r.crm.SetLabels(newProj, map[string]string{infra.CreatedLabelKey: "made-by-hand"})
		for _, args := range [][]string{{"--create-project", "--plan-only"}, {"--create-project"}} {
			out, _, err := r.run(t, true, newProj+"\n", args...)
			if ExitCode(err) != ExitUserError || !strings.Contains(out+errString(err), "holds something of Fugaro's") {
				t.Errorf("%v: exit %d (%v), want the refusal:\n%s", args, ExitCode(err), err, out)
			}
			if strings.Contains(out, "adopted existing project") {
				t.Errorf("%v: a project whose origin label this program never wrote was adopted:\n%s", args, out)
			}
		}
		if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != "made-by-hand" {
			t.Errorf("%s on the refused project = %q, want the value it carried, %q", infra.CreatedLabelKey, got, "made-by-hand")
		}
		r.nothingDone(t, "a project carrying the origin label with an unwritten value")
	})

	t.Run("the origin label with a value this program does not write", func(t *testing.T) {
		r := newProjectRig(t)
		r.foreign(true)
		// The key is the one fugaro setup writes, and "managed" is not a value
		// it writes: the project is not Fugaro's by this label, and the refusal
		// is the answer, exactly as it is for a bucket.
		r.crm.SetLabels(newProj, map[string]string{infra.CreatedLabelKey: "managed"})
		out, _, err := r.run(t, true, newProj+"\n", "--create-project")
		if ExitCode(err) != ExitUserError || !strings.Contains(out+errString(err), "holds something of Fugaro's") {
			t.Errorf("exit %d (%v), want the refusal:\n%s", ExitCode(err), err, out)
		}
		if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != "managed" {
			t.Errorf("the refused project's %s = %q, want the value it already carried, %q", infra.CreatedLabelKey, got, "managed")
		}
		r.nothingDone(t, "a project carrying the origin label with a value this program does not write")
	})

	t.Run("the origin label a run of fugaro setup wrote", func(t *testing.T) {
		r := newProjectRig(t)
		r.foreign(true)
		r.crm.SetLabels(newProj, map[string]string{infra.CreatedLabelKey: infra.CreatedLabelValue})
		// The creation an earlier run made is resumed, not adopted, and it is
		// not adopted twice: the label the run reads is the resume path.
		out, _, err := r.run(t, true, newProj+"\n", "--create-project")
		if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != infra.CreatedLabelValue {
			t.Errorf("%s on the project = %q, want the value the earlier run wrote, %q (err %v):\n%s", infra.CreatedLabelKey, got, infra.CreatedLabelValue, err, out)
		}
		if strings.Contains(out, "adopted existing project") {
			t.Errorf("a project an earlier run created was adopted:\n%s", out)
		}
	})
}

// TestSetupAdoptsIgnoresABucketOfAnotherProject: a bucket name is global, so a
// bucket under the name this installation would use that belongs to someone
// else's project is not this project's, and it does not stop the adoption.
func TestSetupAdoptsIgnoresABucketOfAnotherProject(t *testing.T) {
	r := newProjectRig(t)
	r.foreign(true)
	r.gcs.AddBucket("fugaro-runs-"+newProj, 999999, managedBucketLabels)
	r.gcs.AddBucket("fugaro-tfstate-"+newProj, 999999, map[string]string{"fugaro": "tfstate"})
	out, _, err := r.run(t, true, newProj+"\n", "--create-project")
	if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != infra.AdoptedLabelValue {
		t.Errorf("%s on the project = %q, want %q: another project's bucket under the name is not this project's (err %v):\n%s", infra.CreatedLabelKey, got, infra.AdoptedLabelValue, err, out)
	}
}

// TestAdoptionRefusesAProjectBeingDeleted: lifecycle has to be ACTIVE. A
// project being deleted is neither created nor adopted, and it is refused with
// its own message — not with the "holds something of Fugaro's" one — whether
// or not it carries an origin label.
func TestAdoptionRefusesAProjectBeingDeleted(t *testing.T) {
	r := newProjectRig(t)
	r.foreign(true)
	r.crm.SetLifecycle(newProj, "DELETE_REQUESTED")
	out, _, err := r.run(t, true, newProj+"\n", "--create-project")
	if err == nil || !strings.Contains(out+errString(err), "DELETE_REQUESTED") || !strings.Contains(out, "undelete") {
		t.Errorf("a project pending deletion: err %v\n%s", err, out)
	}
	if strings.Contains(out+errString(err), "holds something of Fugaro's") {
		t.Errorf("a project being deleted is refused as one holding something of Fugaro's:\n%s\n%v", out, err)
	}
	if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != "" {
		t.Errorf("a project being deleted was labelled %s=%s", infra.CreatedLabelKey, got)
	}
	r.nothingDone(t, "a project pending deletion")
}

// TestResumedAdoptionIsMarkedManaged: the project an earlier run adopted and
// did not get to mark (it died before its installation was applied) is
// resumed by the label it carries, and the rerun marks it managed exactly as a
// creating run marks the project it created — behind the one confirmation that
// is the person at the terminal, whose banner says which of the two labels it
// carries.
func TestResumedAdoptionIsMarkedManaged(t *testing.T) {
	r := newProjectRig(t)
	const number = 424242
	bucket := "fugaro-runs-" + newProj
	r.crm.AddProject(newProj, number)
	r.crm.SetLabels(newProj, map[string]string{infra.CreatedLabelKey: infra.AdoptedLabelValue})
	r.gcs.AddProject(newProj, number)
	r.gcs.AddBucket(bucket, number, managedBucketLabels)
	r.fb.AddFirebase(newProj)
	r.bill.SetBilling(newProj, true)
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
	out, _, err := r.run(t, true, newProj+"\n"+initProjectName+"\n", "--create-project")
	if n := len(r.crm.Creates()); n != 0 {
		t.Fatalf("%d project(s) created (err %v):\n%s", n, err, out)
	}
	if !strings.Contains(out, "says fugaro setup adopted it") {
		t.Errorf("the mark's banner does not say how the project became Fugaro's (err %v):\n%s", err, out)
	}
	if b, ok := markerAt(t, r.gcs, bucket); !ok {
		t.Errorf("no %s in gs://%s after the run that resumed the adoption (err %v):\n%s", managed.MarkerObject, bucket, err, out)
	} else if want := `{"version":1,"gcp_project":"` + newProj + `"}`; b != want {
		t.Errorf("gs://%s/%s = %s, want %s", bucket, managed.MarkerObject, b, want)
	}
	if got := r.crm.Labels(newProj)[managed.ProjectLabelKey]; got != managed.ProjectLabelValue {
		t.Errorf("%s on the resumed project = %q, want %q: the run that resumes an adoption writes both halves of the fact (err %v):\n%s",
			managed.ProjectLabelKey, got, managed.ProjectLabelValue, err, out)
	}
	if got := r.crm.Labels(newProj)[infra.CreatedLabelKey]; got != infra.AdoptedLabelValue {
		t.Errorf("%s on the resumed project = %q, want %q: the label states the origin, and a resume does not restate it", infra.CreatedLabelKey, got, infra.AdoptedLabelValue)
	}
}

// TestMarkCreatedRecordsAnAdoptedProject: the fact a run records is the same
// one a creating run records — the project this installation's, and the runs
// bucket its mark goes in — and the sentences around it say the project was
// adopted rather than created. The rig cannot reach the write itself (its
// installation is faked, so the runs bucket only exists when a test seeds it,
// and a seeded bucket is what makes the project non-empty), so what is pinned
// here is the branch: creation() is the adopted project, and the warning names
// the adoption.
func TestMarkCreatedRecordsAnAdoptedProject(t *testing.T) {
	r := &initRun{o: &initOptions{asJSON: true}, gcpProject: newProj}
	if got := r.creation(); got != "" {
		t.Fatalf("creation() = %q before any project was made Fugaro's", got)
	}
	r.res.Adopted = newProj
	if got := r.creation(); got != newProj {
		t.Errorf("creation() = %q, want %q: an adopted project is the one the run marks", got, newProj)
	}

	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r = &initRun{cmd: cmd, w: &errOut, o: &initOptions{asJSON: true}, gcpProject: newProj}
	r.res.Adopted = newProj
	r.res.Outputs = &infra.InstallationOutputs{RunsBucket: "fugaro-runs-someone-else"}
	e := &initEngine{r: r, c: &infra.Clients{}}
	if err := e.outcome(context.Background(), &initflow.Result{}, nil); err != nil {
		t.Fatalf("%v", err)
	}
	var doc struct {
		Warnings []string `json:"warnings"`
	}
	if jerr := json.Unmarshal(out.Bytes(), &doc); jerr != nil {
		t.Fatalf("stdout is not the result object (%v):\n%s", jerr, out.String())
	}
	want := "project " + newProj + " was adopted but its runs bucket is fugaro-runs-someone-else"
	var found bool
	for _, w := range doc.Warnings {
		if strings.Contains(w, want) {
			found = true
		}
		if strings.Contains(w, "was created but") {
			t.Errorf("a run that adopted a project says it created one: %s", w)
		}
	}
	if !found {
		t.Errorf("no warning says the project was adopted (%q):\n%s", want, out.String())
	}
}
