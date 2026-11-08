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
	if said := out + errOut; !strings.Contains(said, "no second command") {
		t.Errorf("the run does not say the flagging needs no second command:\n%s", said)
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
