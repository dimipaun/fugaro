package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// update_selfflag_test.go holds D1553's other half: `fugaro update` on an
// installation that is not managed flags it itself when it is given the budget
// once, so the upgrade stays one command (the HARD RULE), and the flagging is
// refused in a coding agent's session even when --budget is passed (F20: the
// consent a flag gives is only as strong as the agent markers).
//
// The self-flag's write is the flag command's own write — managed.WriteFlag
// and managed.WriteManagedLabelForFlag — so what is asserted here is what the
// managed read answers afterwards, over the rig's fakes, and not what one fake
// map happens to hold.

// unmanagedUpdateRig is an installation that is there and converges, with the
// runs bucket its own marks would go into seeded as the write proves it (the
// fakes' project number and the bucket's label, managed.WriteFlag's own
// proof). It carries no marker and no fugaro_managed label: an installation
// fugaro created before it ever created one managed.
func unmanagedUpdateRig(t *testing.T) *initRig {
	t.Helper()
	noAgentSession(t)
	noRunSession(t)
	r := updateRig(t)
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	return r
}

// updateBudgetArgs is the owner's own command line: `fugaro update` given the
// budget once — the trio the doors take together (--budget,
// --budget-currency and --billing-account) — under --yes, which covers
// update's ordinary steps.
func updateBudgetArgs(extra ...string) []string {
	return append([]string{"update", "--yes", "--budget", "50", "--budget-currency", "USD", "--billing-account", billingAcc}, extra...)
}

// TestUpdateOnAnUnmanagedInstallationFlagsItAndNeedsNoSecondCommand is the
// HARD RULE's own test (D1553): one `fugaro update`, given the budget once,
// leaves the installation managed — both marks, the creation fact that records
// the amount and the label on the project itself — so the owner runs no second
// command and the upgrade is still one command. A run that recorded the amount
// and left the label to a command of ours that does not exist (or to a
// second invocation by hand) would be exactly the operator step the HARD RULE
// forbids.
func TestUpdateOnAnUnmanagedInstallationFlagsItAndNeedsNoSecondCommand(t *testing.T) {
	r := unmanagedUpdateRig(t)
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err != nil {
		t.Fatalf("fugaro update with the budget: %v\n%s\n%s", err, out, errOut)
	}
	// The half in the runs bucket: the creation fact, in the version that
	// records the amount the owner stated (a fact without one is refused by
	// every managed apply, so a flag that wrote one would buy nothing).
	got, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the self-flag wrote no gs://%s/%s:\n%s\n%s", initRunsBucket, managed.MarkerObject, out, errOut)
	}
	if !strings.Contains(got, `"amount":50`) || !strings.Contains(got, `"currency":"USD"`) {
		t.Errorf("gs://%s/%s = %s: it does not record the budget the run was given", initRunsBucket, managed.MarkerObject, got)
	}
	// And the half on the project itself, which the read requires beside it.
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != managed.ProjectLabelValue {
		t.Errorf("the project carries %s=%q, want %q", managed.ProjectLabelKey, l, managed.ProjectLabelValue)
	}
	// The read the apply-time gate takes is the one that settles it: a project
	// the read still refuses is not a flagged project, whatever the fakes hold.
	if !managedFlagIsManaged(t, r) {
		t.Error("the managed read does not believe the installation this run flagged")
	}
	// The run says what it did, and says that nothing follows it.
	said := out + errOut
	if !strings.Contains(said, "no second command") {
		t.Errorf("the run does not say the flagging needs no second command:\n%s", said)
	}
	// And it said what it was about to write before it wrote it: the flag
	// command's own statement (managedFlagStatement), which is the disclosure
	// the flag carries on its own command line and which no help text of this
	// door can carry — --managed, whose text used to be the only place any of
	// this was said, is declared here, hidden and refused by name.
	for _, want := range []string{"flagging GCP project", managed.MarkerObject, "it records: this project is managed", "it gives up: the typed confirmation"} {
		if !strings.Contains(said, want) {
			t.Errorf("the run does not state %q before it writes the flag:\n%s", want, said)
		}
	}
	// The other direction of the same property: a second run of the same
	// command finds it managed, flags nothing again, and still succeeds.
	again, againErr, err := executeStdin(t, "", updateBudgetArgs()...)
	if err != nil {
		t.Fatalf("the second fugaro update: %v\n%s\n%s", err, again, againErr)
	}
	if strings.Contains(again+againErr, "no second command") {
		t.Errorf("the second run claimed to have flagged the installation again:\n%s", again+againErr)
	}
	if !managedFlagIsManaged(t, r) {
		t.Error("the installation is not managed after a second run")
	}
}

// seedFlagFact leaves gs://<runs bucket>/fugaro/managed.json holding m and
// nothing else, as an earlier run left it: the marker object without the label
// on the project is exactly the half-written state this step can produce — its
// object write succeeds and its label write then fails — and it is sealed here
// rather than injected, because a GCS answer that fails once has no hook in
// gcpfake. The marker is built by the package's own writer, so the seed is a
// marker this build would have written and not a literal that could drift from
// one.
func seedFlagFact(t *testing.T, r *initRig, m managed.Marker) {
	t.Helper()
	data, err := m.Bytes()
	if err != nil {
		t.Fatalf("building the marker to seed: %v", err)
	}
	r.gcs.Put(initRunsBucket, managed.MarkerObject, data)
}

// TestUpdateFinishesAFlagWhoseLabelWriteFailed: the state the self-flag itself
// leaves when its label write fails — the object recording the amount, the
// project without the label. The read the apply-time gate makes requires both
// marks and answers not managed, so a second `fugaro update` with the same
// budget reaches the flag writer and meets the object the first run wrote:
// managed.WriteFlag refuses it (ErrAlreadyMarked) and `fugaro managed flag`
// refuses it for the same reason, so a warning naming a hand-written label
// would leave the owner a manual repair — the one thing the `fugaro update`
// rule forbids. The run reads the object WITHOUT the label (managed.ReadFlagFact,
// which the read that requires the label cannot do: it drops the object), tells
// it is this project's fact recording the amount it was given, and writes the
// half that is missing.
func TestUpdateFinishesAFlagWhoseLabelWriteFailed(t *testing.T) {
	r := unmanagedUpdateRig(t)
	seedFlagFact(t, r, managed.Marker{
		Version:    managed.MarkerVersionBuilds,
		GCPProject: initProject,
		Budget:     &managed.Budget{Amount: 50, Currency: "USD"},
		// The object's own default bound: the seed is what a flag writes.
		MaxImageBuilds: managed.DefaultImageBuilds,
	})
	// The premise, so the test cannot pass on a state that was already managed:
	// the label is what is missing, and the read that requires both refuses.
	if managedFlagIsManaged(t, r) {
		t.Fatal("the seeded state already reads as managed, so this test proves nothing about finishing it")
	}
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err != nil {
		t.Fatalf("fugaro update with the budget over a half-written flag: %v\n%s\n%s", err, out, errOut)
	}
	// The label was written, the object was left alone, and the read believes
	// the project: the flag is finished, and no second command was needed.
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != managed.ProjectLabelValue {
		t.Errorf("the project carries %s=%q, want %q: the run did not finish the flag", managed.ProjectLabelKey, l, managed.ProjectLabelValue)
	}
	if !managedFlagIsManaged(t, r) {
		t.Error("the managed read does not believe the installation the run finished flagging")
	}
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); !ok || !strings.Contains(got, `"amount":50`) {
		t.Errorf("gs://%s/%s = %q, want the fact the earlier run wrote, recording the amount", initRunsBucket, managed.MarkerObject, got)
	}
	said := out + errOut
	for _, want := range []string{"left the creation fact", "no second command"} {
		if !strings.Contains(said, want) {
			t.Errorf("the run does not say %q about the flag it finished:\n%s", want, said)
		}
	}
}

// TestUpdateDoesNotFinishAFactThatRecordsAnotherAmount: the completion above is
// for ONE object — this project's fact recording the amount this run was given.
// A fact recording another amount is close to it and is not it: a flag records
// an amount, it does not edit one, so writing the label there would make the
// project managed at a figure its owner never chose for this run, and no
// command of ours would take it back to the amount the run meant. The run says
// what it found, names both amounts, and names the one way to finish it.
func TestUpdateDoesNotFinishAFactThatRecordsAnotherAmount(t *testing.T) {
	r := unmanagedUpdateRig(t)
	seedFlagFact(t, r, managed.Marker{
		Version:        managed.MarkerVersionBuilds,
		GCPProject:     initProject,
		Budget:         &managed.Budget{Amount: 75, Currency: "EUR"},
		MaxImageBuilds: managed.DefaultImageBuilds,
	})
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err != nil {
		t.Fatalf("fugaro update with the budget over a fact recording another amount: %v\n%s\n%s", err, out, errOut)
	}
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != "" {
		t.Errorf("the run wrote %s=%q on a fact recording 75 EUR, and the amount this run was given is the one a flag records", managed.ProjectLabelKey, l)
	}
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read believes a project the run did not flag")
	}
	said := out + errOut
	for _, want := range []string{"it was not flagged", "records the budget amount 75 EUR", "--budget 75 --budget-currency EUR", "if that is the amount meant"} {
		if !strings.Contains(said, want) {
			t.Errorf("the warning does not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, "left the creation fact") {
		t.Errorf("the run claims to have finished a fact recording another amount:\n%s", said)
	}
	if strings.Contains(said, "no second command") {
		t.Errorf("the run claimed to have flagged the installation:\n%s", said)
	}
}

// TestUpdateDoesNotFinishAFactThatRecordsAnotherBuildBound: the amount is the
// one this run was given and the fact is not one the flag writes, for the other
// figure it carries. The bound is what the statement printed above the write
// promises — one billable build per invocation (D1013) — and a fact holding the
// project's applies to a larger count is an object recorded by hand rather than
// by any run of ours: completing it with the label would make the project
// managed at a bound this run never stated, so the figure that took effect
// would not be the one the owner read. The run writes neither mark and says
// what is there.
func TestUpdateDoesNotFinishAFactThatRecordsAnotherBuildBound(t *testing.T) {
	r := unmanagedUpdateRig(t)
	seedFlagFact(t, r, managed.Marker{
		Version:    managed.MarkerVersionBuilds,
		GCPProject: initProject,
		Budget:     &managed.Budget{Amount: 50, Currency: "USD"},
		// In the marker's own range, so the object is one the read believes:
		// the bound is a figure this build accepts and not the default.
		MaxImageBuilds: 1000,
	})
	before, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the seed wrote no gs://%s/%s", initRunsBucket, managed.MarkerObject)
	}
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err != nil {
		t.Fatalf("fugaro update with the budget over a fact recording another bound: %v\n%s\n%s", err, out, errOut)
	}
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != "" {
		t.Errorf("the run wrote %s=%q on a fact holding the project's applies to another bound", managed.ProjectLabelKey, l)
	}
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read believes a project the run did not flag")
	}
	// The object is untouched: neither half of this run's write happened.
	if after, _ := managedFlagAt(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("gs://%s/%s = %q, want the fact the earlier run wrote (%q)", initRunsBucket, managed.MarkerObject, after, before)
	}
	said := out + errOut
	// Both figures are named, and so is the one the statement above the write
	// promised — the statement this run printed before it asked for either mark.
	for _, want := range []string{
		"it was not flagged",
		"one build per invocation",
		"records the budget amount 50 USD",
		"holds this project's applies to 1000 billable builds per invocation",
		"where the flag this step would finish records 1",
		"neither mark was written",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the warning does not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, "left the creation fact") {
		t.Errorf("the run claims to have finished a fact recording another bound:\n%s", said)
	}
	if strings.Contains(said, "no second command") {
		t.Errorf("the run claimed to have flagged the installation:\n%s", said)
	}
}

// TestUpdateDoesNotFinishAFactAboutAnotherProject: the other side of the same
// check, and the one that decides whether the completion writes anything at
// all. The object is in this project's runs bucket — proved by number and
// label, as the writer proves it — and it says it is a fact about a different
// project, so it is not this project's: the label goes on THIS project, and
// writing it on the strength of an object about someone else would flag a
// project whose own fact was never read. The run says what is there instead.
func TestUpdateDoesNotFinishAFactAboutAnotherProject(t *testing.T) {
	r := unmanagedUpdateRig(t)
	seedFlagFact(t, r, managed.Marker{
		Version:        managed.MarkerVersionBuilds,
		GCPProject:     "someone-elses-project",
		Budget:         &managed.Budget{Amount: 50, Currency: "USD"},
		MaxImageBuilds: managed.DefaultImageBuilds,
	})
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err != nil {
		t.Fatalf("fugaro update with the budget over a fact about another project: %v\n%s\n%s", err, out, errOut)
	}
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != "" {
		t.Errorf("the run wrote %s=%q on the strength of an object about another project", managed.ProjectLabelKey, l)
	}
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read believes a project the run did not flag")
	}
	// The read's own sentence about what is there, in this step's warning and
	// not only in the writer's error: the branch that has nothing to write says
	// what it found, and it is not the branch that records another amount — a
	// fact that is not this project's is not a fact whose amount this run could
	// adopt either.
	said := out + errOut
	for _, want := range []string{"it was not flagged", "there is nothing for this step to write", "names project someone-elses-project, not " + initProject} {
		if !strings.Contains(said, want) {
			t.Errorf("the warning does not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, "if that is the amount meant") {
		t.Errorf("the run read an object about another project as this project's fact recording an amount:\n%s", said)
	}
	if strings.Contains(said, "no second command") {
		t.Errorf("the run claimed to have flagged the installation:\n%s", said)
	}
}

// TestUpdateBudgetHelpNamesTheFlaggingItTriggers is the disclosure half of D1553
// on the update door. What a run given the budget does besides recording it —
// flag an installation that is not managed, which takes that installation's
// typed confirmation away — used to be said only in --managed's help, and
// --managed is declared on this door, hidden from its help and refused by name,
// so a reader of `fugaro update --help` was told nothing about it. --budget is
// the flag that triggers the write, so --budget is where it is said; setup's
// own --budget is the budget alone, because setup flags nothing it did not
// create.
func TestUpdateBudgetHelpNamesTheFlaggingItTriggers(t *testing.T) {
	out, _, err := execute(t, "update", "--help")
	if err != nil {
		t.Fatalf("fugaro update --help: %v", err)
	}
	said := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"--budget", "flags that installation as managed", "fugaro_managed=true", "typed confirmation", "coding agent's session"} {
		if !strings.Contains(said, want) {
			t.Errorf("fugaro update --help's --budget does not say %q:\n%s", want, out)
		}
	}
	sout, _, err := execute(t, "setup", "--help")
	if err != nil {
		t.Fatalf("fugaro setup --help: %v", err)
	}
	if ssaid := strings.Join(strings.Fields(sout), " "); strings.Contains(ssaid, "flags that installation as managed") {
		t.Errorf("fugaro setup --help claims setup flags an installation that already exists:\n%s", sout)
	}
}

// TestUpdateDoesNotFlagAProjectWhoseStateIsElsewhere: the other half of what
// `fugaro managed flag` refuses before it writes (managed_flag.go, and
// checkManagedStateBucket on the creating path). The managed apply reads the
// installation's Terraform state out of gs://fugaro-tfstate-<gcp_project> BY
// THAT DERIVED NAME, so a project whose config keeps it in another bucket is
// one whose every managed apply reads CauseStateUnread and keeps the typed
// confirmation the marks would take away. Writing them anyway would leave an
// installation the managed read believes and the apply does not, and the trap
// is permanent: a second `fugaro update --budget` finds a fact that records an
// amount and leaves it, so the repair would be a hand-run unflag and a state
// move — the operator step the HARD RULE forbids. So this step warns and
// writes neither half.
func TestUpdateDoesNotFlagAProjectWhoseStateIsElsewhere(t *testing.T) {
	r := unmanagedUpdateRig(t)
	// The bucket the config names, seeded as this project's own — a bucket name
	// is global, and the run proves the one it resolves. Without it the run's
	// own preflight refuses earlier and this test would prove nothing about the
	// self-flag.
	r.appendConfig(t, "terraform: { state_bucket: fugaro-tfstate-elsewhere }\n")
	r.gcs.AddBucket("fugaro-tfstate-elsewhere", initProjectNumber, map[string]string{"fugaro": "tfstate"})
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err != nil {
		t.Fatalf("fugaro update with the budget and a state bucket the config names: %v\n%s\n%s", err, out, errOut)
	}
	// Neither half of the mark, and the read the apply-time gate makes still
	// refuses the project.
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("the self-flag wrote gs://%s/%s = %s on an installation whose state is elsewhere", initRunsBucket, managed.MarkerObject, got)
	}
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != "" {
		t.Errorf("the self-flag wrote %s=%q on an installation whose state is elsewhere", managed.ProjectLabelKey, l)
	}
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read believes a project the self-flag did not flag")
	}
	// It says why, names the bucket the config records and the one the state
	// would have to move to, and does not claim to have flagged anything.
	said := out + errOut
	for _, want := range []string{"nothing was flagged", "fugaro-tfstate-elsewhere", initStateBucket} {
		if !strings.Contains(said, want) {
			t.Errorf("the warning does not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, "no second command") {
		t.Errorf("the run claimed to have flagged the installation:\n%s", said)
	}
}

// TestSetupDoesNotFlagAnUnmanagedInstallation: D1553's self-flag is the
// upgrade's, and the hook it hangs on is the one both doors share, so the door
// is the step's first question. `fugaro setup` on a project that already exists
// flags nothing — its own --managed help says flagging one that already exists
// is not setup's, and it stays that way: what a wizard door would take away
// here is the installation's typed confirmation, on a run whose job is to
// converge what is there. The command line is the HARD RULE's test with the
// other door in front of it, which is the only difference between the two runs.
func TestSetupDoesNotFlagAnUnmanagedInstallation(t *testing.T) {
	r := unmanagedUpdateRig(t)
	args := append([]string{"setup"}, updateBudgetArgs()[1:]...)
	out, errOut, err := executeStdin(t, "", args...)
	if err != nil {
		t.Fatalf("fugaro setup with the budget on an existing installation: %v\n%s\n%s", err, out, errOut)
	}
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("fugaro setup wrote gs://%s/%s = %s on a project it did not create", initRunsBucket, managed.MarkerObject, got)
	}
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != "" {
		t.Errorf("fugaro setup wrote %s=%q on a project it did not create", managed.ProjectLabelKey, l)
	}
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read believes a project fugaro setup did not flag")
	}
	if said := out + errOut; strings.Contains(said, "no second command") {
		t.Errorf("fugaro setup claimed to have flagged the installation:\n%s", said)
	}
}

// TestUpdateSelfFlagRefusesInsideAFugaroRun: the second fence beside the
// agent's, which the flag command and the unflag both keep and this step must
// too — a run's variables are set by the platform rather than by the agent, so
// the two refusals are not the same test. A run credential may not write the
// marks that take the confirmation away, least of all on its own installation,
// where the applies that would then skip the confirmation are the ones its own
// verdict is judged by.
func TestUpdateSelfFlagRefusesInsideAFugaroRun(t *testing.T) {
	for _, marker := range managedFlagRunMarkers {
		t.Run(marker, func(t *testing.T) {
			r := unmanagedUpdateRig(t)
			t.Setenv(marker, "x")
			out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
			if err == nil {
				t.Fatalf("a Fugaro run's process flagged the installation:\n%s\n%s", out, errOut)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			said := out + errOut + err.Error()
			for _, want := range []string{"nothing was flagged", marker, "is not managed"} {
				if !strings.Contains(said, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, said)
				}
			}
			// Neither half of the mark, as in the agent fence's test: the refusal
			// has to stand where the write would be, and the label is the half a
			// run credential's write would leave behind.
			if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
			}
			if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != "" {
				t.Errorf("a refused run wrote %s=%q on the project", managed.ProjectLabelKey, l)
			}
		})
	}
}

// TestUpdatePlanOnlyFlagsNothing: --plan-only creates and changes nothing, and
// the self-flag's whole output is the two marks that take the installation's
// typed confirmation away. A preview that wrote them would turn a consent gate
// off on a read-only flag's word — and the update's own help says, in as many
// words, that this door never takes --yes because it changes nothing on a
// flag's word. So the same command line as the HARD RULE's test, with
// --plan-only, leaves the installation exactly as unmanaged as it found it,
// and says nothing about flagging it.
func TestUpdatePlanOnlyFlagsNothing(t *testing.T) {
	r := unmanagedUpdateRig(t)
	out, errOut, err := executeStdin(t, "", updateBudgetArgs("--plan-only")...)
	if err != nil {
		t.Fatalf("fugaro update --plan-only with the budget: %v\n%s\n%s", err, out, errOut)
	}
	// Neither half: the creation fact, and the label on the project itself. One
	// without the other leaves the project unmanaged, and a preview may write
	// neither.
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("fugaro update --plan-only wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
	}
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != "" {
		t.Errorf("fugaro update --plan-only wrote %s=%q on the project", managed.ProjectLabelKey, l)
	}
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read believes a project --plan-only did not flag")
	}
	// And it did not claim to have flagged anything: the sentence the read-only
	// door must never print is the one the real flag prints.
	if said := out + errOut; strings.Contains(said, "no second command") {
		t.Errorf("fugaro update --plan-only claimed to have flagged the installation:\n%s", said)
	}
}

// TestUpdateSelfFlagRefusesInAnAgentSession is F20's test for this fence: a
// coding agent's session that passes --budget does not get the flag by asking
// for it, because what the flag does is write the marks that take the owner's
// own confirmations away. The fence stands where the write would be, so the
// refusal is the run's outcome and not a warning an agent could report as a
// finished update: it names the marker, the state the read answered with, and
// the command the owner runs at their own terminal.
func TestUpdateSelfFlagRefusesInAnAgentSession(t *testing.T) {
	r := unmanagedUpdateRig(t)
	t.Setenv(agentenv.Markers[0], "1")
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err == nil {
		t.Fatalf("an agent's session flagged the installation by passing --budget:\n%s\n%s", out, errOut)
	}
	said := out + errOut + err.Error()
	for _, want := range []string{"nothing was flagged", agentenv.Markers[0], "is not managed", "no second command"} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, said)
		}
	}
	// The owner's own command is the one it names, with the budget.
	if !strings.Contains(said, "fugaro update --budget N --budget-currency CCC") {
		t.Errorf("the refusal does not name the owner's own command:\n%s", said)
	}
	// And neither half of the mark was written: a refusal that wrote one would
	// be the whole fence gone, since the read requires both.
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
	}
	if l := r.crm.Labels(initProject)[managed.ProjectLabelKey]; l != "" {
		t.Errorf("a refused run wrote %s=%q on the project", managed.ProjectLabelKey, l)
	}
}
