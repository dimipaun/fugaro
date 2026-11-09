package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// managedUnflagMarker is a marker a managed project holds: the version that
// records the amount and the image build bound (M5, D1013). A project is
// managed only while BOTH marks are there — this object and the project's own
// label — which is what makes the removal a two-half statement.
var managedUnflagMarker = `{"version":3,"gcp_project":"` + initProject + `","budget":{"amount":50,"currency":"USD"},"max_image_builds":1}`

// managedUnflagRig is a managed project: the runs bucket the read proves, the
// creation fact in it, and the label on the project itself.
func managedUnflagRig(t *testing.T) *initRig {
	t.Helper()
	r := managedFlagRig(t)
	markManaged(t, r)
	return r
}

// markManaged puts both halves of the mark on the rig's project, the state
// every later command has to be judged against.
func markManaged(t *testing.T, r *initRig) {
	t.Helper()
	r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(managedUnflagMarker))
	labelled(r.crm, initProject)
}

// managedUnflagArgs is one unflagging invocation of the rig's own project.
func managedUnflagArgs(extra ...string) []string {
	return append([]string{"managed", "unflag", initProject}, extra...)
}

// managedUnflagSaysHalf reports the line the command printed about one half of
// the mark.
func managedUnflagSaysHalf(out, what string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, what) {
			return line
		}
	}
	return ""
}

// TestManagedUnflagRefusesAnAgentSession is the property D1020's answer rests
// on: the mark is what turns the apply-time confirmation off, so the command
// that REMOVES it is the owner's by construction — a session that could remove
// the mark could remove it mid-command and keep applying ungated. Nothing is
// removed from a coding agent's session, under any of the five markers that
// name one, whatever the arguments. The sixth, the IDE extension's own, is
// decided by the terminal the way agentenv decides it: with no terminal the
// same refusal (below), and at a terminal it is the owner's own, who names the
// project as the argument and removes the mark like any other person at a
// terminal (the accepted half is TestManagedFlagAcceptsTheIDETerminal's, over
// the flag: both commands ask agentMarker the same question, so the same
// classification reaches both).
func TestManagedUnflagRefusesAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.AgentMarkers {
		t.Run(marker, func(t *testing.T) {
			r := managedUnflagRig(t)
			terminal(t, true)
			t.Setenv(marker, "1")
			out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
			if err == nil {
				t.Fatalf("the command ran in a coding agent's session:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name the marker %s: %v", marker, err)
			}
			assertBothMarksStillThere(t, r, "a coding agent's session")
			if len(r.crm.Requests()) != 0 {
				t.Errorf("a refused session read the project: %v", r.crm.Requests())
			}
		})
	}
	// The IDE marker alone with no terminal: an agent's tool call has none.
	t.Run(agentenv.IDEMarker+" with no terminal", func(t *testing.T) {
		r := managedUnflagRig(t)
		noTerminal(t)
		t.Setenv(agentenv.IDEMarker, "1")
		out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
		if err == nil {
			t.Fatalf("the command ran with no terminal:\n%s", out)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
		}
		if !strings.Contains(err.Error(), agentenv.IDEMarker) {
			t.Errorf("the refusal does not name the marker %s: %v", agentenv.IDEMarker, err)
		}
		assertBothMarksStillThere(t, r, "a session with no terminal")
		if len(r.crm.Requests()) != 0 {
			t.Errorf("a refused session read the project: %v", r.crm.Requests())
		}
	})
}

// TestManagedUnflagRefusesARunProcess is the other half of the same boundary,
// and the one `fugaro managed flag` carries for the same reason: a Fugaro
// run's own process holds the operator's credentials, so a run credential
// could remove the marks its own project is managed by. Every variable a run's
// process carries refuses on its own, before anything is read.
func TestManagedUnflagRefusesARunProcess(t *testing.T) {
	for _, marker := range managedFlagRunMarkers {
		t.Run(marker, func(t *testing.T) {
			r := managedUnflagRig(t)
			terminal(t, true)
			t.Setenv(marker, "x")
			out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
			if err == nil {
				t.Fatalf("the command ran inside a Fugaro run:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s: %v", marker, err)
			}
			if len(r.crm.Requests()) != 0 {
				t.Errorf("a refused run read the project: %v", r.crm.Requests())
			}
			assertBothMarksStillThere(t, r, "a Fugaro run's process")
		})
	}
}

// TestManagedUnflagRemovesBothMarks is what the command exists for: one
// invocation, the exact project ID as the argument, and BOTH marks a managed
// carries are gone — the object in the runs bucket and the label on the
// project itself. It is asserted through the managed read the apply gate
// takes, with its project-number check, because a test that looked only at the
// fakes' maps would pass for a removal the read still believes.
func TestManagedUnflagRemovesBothMarks(t *testing.T) {
	r := managedUnflagRig(t)
	if !managedFlagIsManaged(t, r) {
		t.Fatal("the rig's project does not read as managed before the command")
	}
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("gs://%s/%s is still there after the command:\n%s", initRunsBucket, managed.MarkerObject, out)
	}
	if got := r.crm.Labels(initProject)[managed.ProjectLabelKey]; got != "" {
		t.Errorf("the project still carries %s=%q after the command", managed.ProjectLabelKey, got)
	}
	// The read is the assertion that settles it: the object alone is not what
	// makes a project managed, and the label alone is not either.
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read still believes a project this command unflagged")
	}
	// And the command says what it took off — the one sentence an owner reads
	// to know what changed — and reads the result back before it claims it.
	for _, want := range []string{"removed", "read back", "is not managed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the command does not say %q:\n%s", want, out)
		}
	}
}

// TestManagedUnflagConvergesAHalfRemovedProject: either mark alone leaves the
// project unmanaged, so a project whose object is gone and whose label is
// there — or the reverse, the state a manual removal leaves — is a project
// that converges in one command rather than needing a second. What was already
// gone is said rather than failing on it.
func TestManagedUnflagConvergesAHalfRemovedProject(t *testing.T) {
	for name, tc := range map[string]struct {
		object, label bool
		want          string
	}{
		"the object is already gone": {false, true, "was not there"},
		"the label is already gone":  {true, false, "was not the managed mark"},
		"neither mark is there":      {false, false, "was not there"},
	} {
		t.Run(name, func(t *testing.T) {
			r := managedFlagRig(t)
			if tc.object {
				r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(managedUnflagMarker))
			}
			if tc.label {
				labelled(r.crm, initProject)
			}
			terminal(t, true)
			out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if _, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("gs://%s/%s is still there after the command", initRunsBucket, managed.MarkerObject)
			}
			if got := r.crm.Labels(initProject)[managed.ProjectLabelKey]; got != "" {
				t.Errorf("the project still carries %s=%q after the command", managed.ProjectLabelKey, got)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the command does not say %q about the half that was already gone:\n%s", tc.want, out)
			}
			if managedFlagIsManaged(t, r) {
				t.Error("the managed read still believes the project")
			}
		})
	}
}

// TestManagedUnflagKeepsTheProjectsOwnLabels: the mark is one label among
// whatever else the project carries, and a Resource Manager update replaces
// the whole map and the display name — so a removal that sent only its own key
// back would take the owner's other labels and the project's name with it.
func TestManagedUnflagKeepsTheProjectsOwnLabels(t *testing.T) {
	r := managedUnflagRig(t)
	r.crm.SetLabels(initProject, map[string]string{"environment": "prod"})
	r.crm.SetDisplayName(initProject, "Belong production")
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := r.crm.Labels(initProject)["environment"]; got != "prod" {
		t.Errorf("the project's own environment label is %q after the unflag, want it kept", got)
	}
	if got := r.crm.DisplayName(initProject); got != "Belong production" {
		t.Errorf("the project's display name is %q after the unflag, want it kept", got)
	}
}

// TestManagedUnflagSaysTheFenceStays: revocation does not tear down the
// deployer fence (D75). The seven scoped roles and their conditioned bindings
// stay in the project, and the command says so BEFORE it writes — the argument
// is the whole of the consent (D1541), so the statement is all the owner gets
// and it comes before the first removal.
func TestManagedUnflagSaysTheFenceStays(t *testing.T) {
	r := managedUnflagRig(t)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	fence := managedUnflagSaysHalf(out, "deployer fence")
	if fence == "" {
		t.Fatalf("the command does not say the deployer fence stays:\n%s", out)
	}
	if !strings.Contains(fence, "does not remove") {
		t.Errorf("the fence line does not say the fence is left in place: %q", fence)
	}
	// Before the first removal, not after: nothing is asked and nothing is
	// typed, so the statement is the only thing that tells the owner what they
	// have just agreed to.
	if at := strings.Index(out, managed.MarkerObject+": "); at < 0 {
		t.Fatalf("the command reported no removal:\n%s", out)
	} else if strings.Index(out, fence) > at {
		t.Errorf("the fence is said after the removal was made:\n%s", out)
	}
	// And the command makes no IAM call at all — the fence is not this
	// command's to touch, and it does not try.
	for _, rq := range r.crm.Requests() {
		if strings.Contains(rq.Path, "setIamPolicy") {
			t.Errorf("the command made an IAM call: %s %s", rq.Method, rq.Path)
		}
	}
}

// TestUnflagTakesTheProjectIDAsAnArgument: the argument is the whole boundary
// this command has, and the same rule the flag keeps (D1541): the ID names the
// one project whose marks come off, nothing stands in for it — and nothing has
// to stand in for it, so a session with no terminal at all that names the right
// project removes the marks. An ID that is not this installation's own is
// refused, at a terminal or not.
func TestUnflagTakesTheProjectIDAsAnArgument(t *testing.T) {
	r := managedUnflagRig(t)
	noTerminal(t)
	out, _, err := executeStdin(t, "", managedUnflagArgs()...)
	if err != nil {
		t.Fatalf("the exact project ID as the argument was refused: %v\n%s", err, out)
	}
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read still believes a project this command unflagged")
	}
	for name, id := range map[string]string{
		"another project":          "proj-9999",
		"a prefix of this project": "proj-123",
	} {
		t.Run(name, func(t *testing.T) {
			r := managedUnflagRig(t)
			noTerminal(t)
			out, _, err := executeStdin(t, id+"\n", "managed", "unflag", id)
			if err == nil {
				t.Fatalf("the command unflagged with %s as the argument:\n%s", id, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), id) {
				t.Errorf("the refusal does not name the project it was given: %v", err)
			}
			assertBothMarksStillThere(t, r, "a refused argument")
		})
	}
}

// TestManagedUnflagNamesOneProjectAndNoOther: one invocation names one project
// by its exact ID — no bulk form, no wildcard, no prefix, no list — and it
// must be the selected installation's own GCP project.
func TestManagedUnflagNamesOneProjectAndNoOther(t *testing.T) {
	r := managedUnflagRig(t)
	terminal(t, true)
	for name, args := range map[string][]string{
		"no project ID":           {"managed", "unflag"},
		"a wildcard":              {"managed", "unflag", "*"},
		"a prefix wildcard":       {"managed", "unflag", "proj-*"},
		"a list of two":           {"managed", "unflag", initProject, "other-proj"},
		"a prefix of the ID":      {"managed", "unflag", "proj-12"},
		"the ID with a suffix":    {"managed", "unflag", initProject + "5"},
		"a Cyrillic lookalike":    {"managed", "unflag", "pr\u043ej-1234"},
		"another project's ID":    {"managed", "unflag", "another-proj"},
		"the Fugaro project name": {"managed", "unflag", initProjectName},
		"a --gcp-project flag":    {"managed", "unflag", initProject, "--gcp-project", "another-proj"},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := executeStdin(t, initProject+"\n", args...)
			if err == nil {
				t.Fatalf("the command accepted %v:\n%s", args, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			assertBothMarksStillThere(t, r, "a refused argument")
		})
	}
	// The one case that works, so the table above is not passing for the wrong
	// reason.
	if _, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...); err != nil {
		t.Fatalf("the exact project ID was refused: %v", err)
	}
}

// TestUnflagOffersNoWayAroundTheArgument: the argument is the whole boundary,
// so nothing stands in for it — no --yes, no --non-interactive, no bulk or
// wildcard form, and no second project. This is the assertion a later change
// that added one would break.
func TestUnflagOffersNoWayAroundTheArgument(t *testing.T) {
	cmd := newManagedUnflagCmd()
	for _, name := range []string{"yes", "non-interactive", "all", "projects", "force", "prefix", "allow-delete"} {
		if cmd.Flags().Lookup(name) != nil || cmd.PersistentFlags().Lookup(name) != nil {
			t.Errorf("fugaro managed unflag defines --%s: the GCP project ID as the argument is the only consent it takes", name)
		}
	}
	for _, name := range []string{"project", "config", "gcp-project", "region"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("fugaro managed unflag does not define --%s", name)
		}
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("fugaro managed unflag accepts no project ID")
	}
	if err := cmd.Args(cmd, []string{initProject, "another-proj"}); err == nil {
		t.Error("fugaro managed unflag accepts more than one project ID")
	}
	if err := cmd.Args(cmd, []string{initProject}); err != nil {
		t.Errorf("fugaro managed unflag refuses one project ID: %v", err)
	}
}

// TestManagedUnflagNeedsTheDefaultRunsBucket: the marker is read from the
// project's runs bucket under its default name — the name the managed read
// derives and nothing else — so a config that names another bucket is a
// project whose marker the read would never look at, and the removal would be
// aimed at a bucket the installation does not use. It is the rule the flag
// keeps, from the other side.
func TestManagedUnflagNeedsTheDefaultRunsBucket(t *testing.T) {
	r := managedUnflagRig(t)
	const other = "fugaro-runs-elsewhere"
	r.gcs.AddBucket(other, initProjectNumber, managedBucketLabels)
	cfg, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	custom := strings.Replace(string(cfg), "runs_bucket: "+initRunsBucket, "runs_bucket: "+other, 1)
	if custom == string(cfg) {
		t.Fatal("the rig's config does not name the default runs bucket")
	}
	if err := os.WriteFile(r.cfg, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err == nil {
		t.Fatalf("the command ran against a bucket the read does not use:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), initRunsBucket) {
		t.Errorf("the refusal does not name the bucket the marker has to be in: %v", err)
	}
	assertBothMarksStillThere(t, r, "a refused config")
}

// TestManagedUnflagFailsClosedOnAnUnreadableProjectNumber: the number is what
// proves the runs bucket is the project's own, and it comes from the cloud. A
// project the caller cannot read is refused before the statement is printed and
// before anything is removed.
func TestManagedUnflagFailsClosedOnAnUnreadableProjectNumber(t *testing.T) {
	r := managedUnflagRig(t)
	r.crm.DenyGet(initProject)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err == nil {
		t.Fatalf("the command ran with no project number read:\n%s", out)
	}
	if strings.Contains(out, "unflagging GCP project") {
		t.Errorf("the command stated what it was about to remove with no project number read:\n%s", out)
	}
	assertBothMarksStillThere(t, r, "a refused run")
}

// TestManagedUnflagNeedsTheProjectsOwnRunsBucket is the marker's own
// discipline on the removal's path: the object is only ever looked at in the
// runs bucket proved to be that project's own, by project number and by the
// installation's label. A bucket name is global, so without the proof a
// removal could take an object out of a stranger's bucket — and a removal that
// cannot prove the bucket takes nothing out of it.
func TestManagedUnflagNeedsTheProjectsOwnRunsBucket(t *testing.T) {
	for name, tc := range map[string]struct {
		number uint64
		labels map[string]string
	}{
		"another project's bucket": {999, managedBucketLabels},
		"an unlabelled bucket":     {initProjectNumber, nil},
	} {
		t.Run(name, func(t *testing.T) {
			noAgentSession(t)
			noRunSession(t)
			r := newInitRig(t)
			r.gcs.AddBucket(initRunsBucket, tc.number, tc.labels)
			markManaged(t, r)
			terminal(t, true)
			out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
			if err == nil {
				t.Fatalf("the removal went ahead in a bucket that is not the project's own:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			assertBothMarksStillThere(t, r, "a refused run")
		})
	}
}

// TestManagedUnflagRefusesWhenTheRunsBucketRootRefusesTheDelete: the marker
// sits at the runs bucket's root, where only an operator's own credential may
// write. A delete the bucket refuses is reported as what it is — the root, and
// who may write there — rather than as an unknown remote failure.
func TestManagedUnflagRefusesWhenTheRunsBucketRootRefusesTheDelete(t *testing.T) {
	r := managedUnflagRig(t)
	r.gcs.DenyWrites(initRunsBucket, managed.MarkerObject)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err == nil {
		t.Fatalf("the removal happened where the bucket refuses writes:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: run this as an operator)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "a launcher writes only runs/, tokens/ and issues/") {
		t.Errorf("the refusal does not name the launcher's write reach as runs/, tokens/ and issues/: %v", err)
	}
	assertBothMarksStillThere(t, r, "a refused run")
}

// TestManagedUnflagSaysWhichHalfIsLeftWhenTheLabelIsRefused: the two removals
// are one statement, and a credential that may write the object but not the
// project's labels can leave half of it done. The object is already gone, so
// the project is NOT managed — the read requires both — and the command must
// say that rather than report as if nothing had happened, and must not put the
// marker back.
func TestManagedUnflagSaysWhichHalfIsLeftWhenTheLabelIsRefused(t *testing.T) {
	r := managedUnflagRig(t)
	r.crm.DenyUpdate(initProject)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err == nil {
		t.Fatalf("the unflag reported success with the label still on the project:\n%s", out)
	}
	if ExitCode(err) != ExitRemoteError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitRemoteError)
	}
	if _, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Error("the marker was put back after the label was refused")
	}
	if got := r.crm.Labels(initProject)[managed.ProjectLabelKey]; got != managed.ProjectLabelValue {
		t.Errorf("the label is %q after the refused update, want it still there", got)
	}
	for _, want := range []string{managed.MarkerObject, managed.ProjectLabelKey, "is not managed any more"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The truth of it, and the reason this is a report and not a rollback: the
	// project is already unmanaged, because either mark alone is enough.
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read still believed the project")
	}
}

// assertBothMarksStillThere is the assertion every refusal above needs: a
// command that refused removed neither half. It uses the real read, so a
// refusal that left a half-removed project behind is caught.
func assertBothMarksStillThere(t *testing.T, r *initRig, who string) {
	t.Helper()
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); !ok {
		t.Errorf("%s removed gs://%s/%s", who, initRunsBucket, managed.MarkerObject)
	} else if got != managedUnflagMarker {
		t.Errorf("%s changed gs://%s/%s: %s", who, initRunsBucket, managed.MarkerObject, got)
	}
	if got := r.crm.Labels(initProject)[managed.ProjectLabelKey]; got != managed.ProjectLabelValue {
		t.Errorf("%s removed the %s label (it is %q now)", who, managed.ProjectLabelKey, got)
	}
}
