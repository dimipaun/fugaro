package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// managedFlagRig is the installation's own runs bucket, seeded as the write
// proves it: the fakes' project number, and the label the installation puts
// on it. WriteFlag proves both before it writes, so a rig that wants its
// marker recorded has to seed both.
func managedFlagRig(t *testing.T) *initRig {
	t.Helper()
	noAgentSession(t)
	noRunSession(t)
	r := newInitRig(t)
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	return r
}

// noRunSession clears the variables a Fugaro run's own process carries, so a
// test of this command passes when the suite itself runs inside one.
func noRunSession(t *testing.T) {
	t.Helper()
	for _, k := range managedFlagRunMarkers {
		t.Setenv(k, "")
	}
}

// managedFlagAt is the marker's bytes in bucket, and whether it is there.
func managedFlagAt(t *testing.T, g *gcpfake.GCS, bucket string) (string, bool) {
	t.Helper()
	b, err := g.Bucket(t, bucket).Bucket.ReadAll(context.Background(), managed.MarkerObject)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// managedFlagArgs is one flagging invocation of the rig's own project: the
// GCP project ID the installation is in, and the required budget pair.
func managedFlagArgs(extra ...string) []string {
	return append([]string{"managed", "flag", initProject, "--budget", "50", "--budget-currency", "USD"}, extra...)
}

// managedFlagIsManaged is the real read over the rig's fakes — the read the
// apply-time gate takes, project-number check, bucket label, label and all —
// so a test can ask what the command did to a PROJECT rather than what the
// fakes hold. A test that asserted only the fakes' label map would pass for a
// write the read still refuses.
func managedFlagIsManaged(t *testing.T, r *initRig) bool {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newInitClients(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	ok, reason, err := managed.IsManaged(t.Context(), c.Storage, c.CRM, lc.Name, lc.GCPProject)
	if err != nil {
		t.Fatalf("the managed read failed: %v", err)
	}
	if !ok {
		t.Logf("the managed read says not managed: %s", reason)
	}
	return ok
}

// TestManagedFlagWritesTheMarker is the write the command exists for: the
// exact project ID typed at a terminal writes gs://<runs bucket>/fugaro/
// managed.json, in the version that carries the amount the owner stated —
// the version the apply-time budget read opens the gate on (Task 4.4). A
// flag that wrote the bare creation fact would be one that lowers a guard
// and buys nothing.
func TestManagedFlagWritesTheMarker(t *testing.T) {
	r := managedFlagRig(t)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("no %s in gs://%s after the command:\n%s", managed.MarkerObject, initRunsBucket, out)
	}
	want := `{"version":2,"gcp_project":"proj-1234","budget":{"amount":50,"currency":"USD"}}`
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.MarkerObject, got, want)
	}
	// The output is the answer's other half: it states, before the write and
	// after it, what flagging gives up — the typed confirmation this project's
	// applies stop taking (D55) — and what stands in its place.
	for _, want := range []string{"gives up", "typed confirmation", "gate", "allow-delete", "budget"} {
		if !strings.Contains(out, want) {
			t.Errorf("the command does not say %q in what flagging gives up:\n%s", want, out)
		}
	}
	// And the write is read back through the managed read itself, with its
	// project-number check, before the command says it is done.
	if !strings.Contains(out, "read back") {
		t.Errorf("the command does not say the marker was read back:\n%s", out)
	}
}

// TestManagedFlagWritesTheLabelToo is the other half of the statement this
// command makes (Task 3.0), and the half it used to leave out: a managed
// project carries BOTH marks — the object in its runs bucket and the label on
// the project itself — and the read requires both, so a flag that wrote the
// object alone left the project exactly as unmanaged as it was, holding a
// marker whose command can no longer be run again (this command replaces only
// the version-1 fact that records no amount, and it had just written the
// version that records one). The label is therefore written by the same
// command, at the same moment, under the same gate.
func TestManagedFlagWritesTheLabelToo(t *testing.T) {
	r := managedFlagRig(t)
	// Labels of the project's own, which the write has to keep: a Resource
	// Manager update replaces the whole map, so this is what a write that
	// sent only its own key back would drop.
	r.crm.SetLabels(initProject, map[string]string{"environment": "prod"})
	// And a display name, which the label write has to keep for the same
	// reason and by the same mechanism: an update replaces the project's
	// editable fields, so a body that carried the labels alone would take the
	// owner's name off the project (the round-2 review's should-fix, and
	// internal/infra/managed's own test of both writers).
	r.crm.SetDisplayName(initProject, "Belong production")
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := r.crm.Labels(initProject)[managed.ProjectLabelKey]; got != managed.ProjectLabelValue {
		t.Errorf("the project carries %s=%q, want %q", managed.ProjectLabelKey, got, managed.ProjectLabelValue)
	}
	if got := r.crm.Labels(initProject)["environment"]; got != "prod" {
		t.Errorf("the project's own environment label is %q after the flag, want it kept", got)
	}
	if got := r.crm.DisplayName(initProject); got != "Belong production" {
		t.Errorf("the project's display name is %q after the flag, want it kept", got)
	}
	// The confirmation says it before it happens: the operator typing the ID
	// is agreeing to both writes, so both are in the text they read.
	if !strings.Contains(out, managed.ProjectLabelKey+"="+managed.ProjectLabelValue) || !strings.Contains(out, "on the project itself") {
		t.Errorf("the confirmation does not say the label is written:\n%s", out)
	}
	// The assertion the contradiction was, and the only one that settles it:
	// the real read the apply gate takes. The marker alone does not make a
	// project managed, whatever the fakes hold.
	if !managedFlagIsManaged(t, r) {
		t.Error("the managed read does not believe a project this command flagged")
	}
	// And the undo is about both writes, because this command made both: the
	// object in the runs bucket and the label on the project. It said only
	// the first, which is half an undo for a command whose whole point is
	// that the two go together — and either one alone does make the project
	// unmanaged again, because the read requires both.
	if !strings.Contains(out, "undo:") {
		t.Fatalf("the command prints no undo for what it just wrote:\n%s", out)
	}
	for _, want := range []string{
		"delete the object gs://" + initRunsBucket + "/" + managed.MarkerObject,
		"no command of ours removes the marker in this release",
		"gcloud projects update " + initProject + " --remove-labels=" + managed.ProjectLabelKey,
		"UNVERIFIED",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the undo does not say %q:\n%s", want, out)
		}
	}
	// The one line of it no test can run live says so itself, rather than
	// reading like the half of the undo that is a command of ours.
	undo := out[strings.Index(out, "undo:"):]
	if line := strings.SplitN(undo, "\n", 2)[0]; !strings.Contains(line, "UNVERIFIED") {
		t.Errorf("the undo's live-unverified line is not marked as such: %q", line)
	}
}

// TestManagedFlagSaysWhichHalfIsMissingWhenTheLabelIsRefused: the two writes
// are one statement, and a credential that may write the object but not the
// project's labels (resourcemanager.projects.update is the owner's, design
// finding 14) can leave half of it written. The command must not report that
// as done — the read requires both marks, so the project is not managed — must
// not take the marker back (the object is the authority and it is there), and
// must say which half is missing and where the owner's one line is, because
// here the operator runs this command themselves and the recovery is theirs.
// It also must not be mistaken for a state a second flag finishes: the marker
// is now the version this command refuses to replace.
func TestManagedFlagSaysWhichHalfIsMissingWhenTheLabelIsRefused(t *testing.T) {
	r := managedFlagRig(t)
	r.crm.DenyUpdate(initProject)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err == nil {
		t.Fatalf("the flag reported success with no label written:\n%s", out)
	}
	// A remote failure and not the operator's error: they typed what they were
	// asked to type, and half of the statement was written. The exit code says
	// the command did not do what it was asked, without blaming the input.
	if ExitCode(err) != ExitRemoteError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitRemoteError)
	}
	// The marker is there — WriteFlag wrote it before the label was attempted,
	// in markCreated's order — and the command says so rather than claiming
	// nothing happened.
	if _, ok := managedFlagAt(t, r.gcs, initRunsBucket); !ok {
		t.Errorf("the marker was taken back after the label was refused (out:\n%s)", out)
	} else if !strings.Contains(err.Error(), "wrote gs://"+initRunsBucket+"/"+managed.MarkerObject) {
		t.Errorf("the refusal does not say the object was written: %v", err)
	}
	// Which half is missing, and that the read requires both: without it the
	// project is not managed and nothing is skipped for it.
	for _, want := range []string{managed.ProjectLabelKey, "requires both", "is not managed yet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The recovery: the owner's own terminal, and the doc with the line.
	for _, want := range []string{"owner's own terminal", "docs/gcp-setup.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if !strings.Contains(err.Error(), "not a second run of this command") {
		t.Errorf("the refusal does not say a second flag cannot finish it: %v", err)
	}
	// And the truth of it: the project is not managed, whatever the object
	// says. This is the fail-closed direction, and it is what makes the
	// refusal an error rather than a note.
	if managedFlagIsManaged(t, r) {
		t.Error("the managed read believed a project whose label was never written")
	}
	// A second run of the command is refused by the marker itself, so the
	// state is not one this command can finish.
	out, _, err = executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err == nil {
		t.Fatalf("the command replaced the v2 marker it had just written:\n%s", out)
	}
	if !strings.Contains(err.Error(), "already there") {
		t.Errorf("the second run's refusal is not the marker's: %v", err)
	}
}

// TestManagedFlagFailsClosedOnAnUnreadableProjectNumber: the number is what
// proves the runs bucket is the project's own, and it comes from the cloud. A
// project the caller cannot read is refused before the confirmation is asked
// for, and nothing is written — an unreadable number is never a match.
func TestManagedFlagFailsClosedOnAnUnreadableProjectNumber(t *testing.T) {
	r := managedFlagRig(t)
	r.crm.DenyGet(initProject)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err == nil {
		t.Fatalf("the flag was written with no project number read:\n%s", out)
	}
	if strings.Contains(out, "Type "+initProject) {
		t.Errorf("the confirmation was asked for with no project number read:\n%s", out)
	}
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
	}
}

// TestManagedFlagRefusesInsideAFugaroRun: the plan refuses the command from a
// run credential as well as from a coding agent's session. A run's process
// carries its own variables whether or not the agent's markers survive into
// the shell a command runs in, so each of them refuses on its own, before
// anything is read, and nothing is written.
func TestManagedFlagRefusesInsideAFugaroRun(t *testing.T) {
	for _, marker := range managedFlagRunMarkers {
		t.Run(marker, func(t *testing.T) {
			r := managedFlagRig(t)
			terminal(t, true)
			t.Setenv(marker, "x")
			out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
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
			if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
			}
			// Neither half, and this is the assertion that covers the new
			// one: a run's process holds the credentials the label needs, so
			// a refusal that happened after the label write would leave the
			// project labelled by a run — the write D58 and design finding 14
			// keep to the owner's own terminal.
			if got := r.crm.Labels(initProject)[managed.ProjectLabelKey]; got != "" {
				t.Errorf("a refused run wrote %s=%q on the project", managed.ProjectLabelKey, got)
			}
		})
	}
}

// TestManagedFlagFailsWhenTheMarkerDoesNotReadBack: the plan's step 3 reads
// the written marker back with the project-number check. A write the managed
// read does not believe — not managed, or another amount than the one typed —
// is not reported as a flagged project.
func TestManagedFlagFailsWhenTheMarkerDoesNotReadBack(t *testing.T) {
	for name, back := range map[string]struct {
		b  managed.Budget
		ok bool
	}{
		"not managed":      {managed.Budget{}, false},
		"another amount":   {managed.Budget{Amount: 51, Currency: "USD"}, true},
		"another currency": {managed.Budget{Amount: 50, Currency: "EUR"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			managedFlagRig(t)
			terminal(t, true)
			old := managedFlagReadBack
			managedFlagReadBack = func(context.Context, *storage.Service, *crm.Service, string, string) (managed.Budget, bool, string, error) {
				return back.b, back.ok, "planted", nil
			}
			t.Cleanup(func() { managedFlagReadBack = old })
			out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
			if err == nil {
				t.Fatalf("a marker that does not read back was reported as flagged:\n%s", out)
			}
			if !strings.Contains(err.Error(), "read back") {
				t.Errorf("the error does not say the read-back failed: %v", err)
			}
			if strings.Contains(out, "is a managed project") {
				t.Errorf("the command claimed success:\n%s", out)
			}
		})
	}
}

// TestManagedFlagRefusesEveryCodingAgentMarker is the property D58's answer
// rests on: an agent cannot flag a project managed by its own action. The
// command writes the marker with the operator's credentials, and a coding
// agent's session holds them — so nothing is written from one, under any of
// the six markers, whatever the arguments (there are none but a project ID
// and the budget pair, and both are given here).
func TestManagedFlagRefusesEveryCodingAgentMarker(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			r := managedFlagRig(t)
			// A terminal, and the right ID ready to be typed: the refusal is
			// about who is running and not about what is or is not there.
			terminal(t, true)
			t.Setenv(marker, "1")
			out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
			if err == nil {
				t.Fatalf("the command ran in a coding agent's session:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name the marker %s: %v", marker, err)
			}
			if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
			}
		})
	}
	// The whole surface is the owner's, so the argument checks below never
	// run either: an agent gets the same refusal for a project ID no
	// installation has.
	r := managedFlagRig(t)
	t.Setenv("CLAUDECODE", "1")
	if _, _, err := execute(t, "managed", "flag", "*", "--budget", "50", "--budget-currency", "USD"); err == nil || !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("an agent's session got past the marker check with a wildcard: %v", err)
	}
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
	}
}

// TestManagedFlagNeedsTheExactProjectIDTyped: the confirmation is a typed
// string and the string is the GCP project ID — the whole boundary D58's
// answer has, since the marker is what turns the confirmation off. Without a
// terminal there is no confirmation at all, and anything but the exact ID —
// another project's, a prefix of this one, the same ID in another case — is
// not it. Nothing is written by any of them.
func TestManagedFlagNeedsTheExactProjectIDTyped(t *testing.T) {
	r := managedFlagRig(t)
	// No terminal: refused, and the refusal names no flag that would stand in
	// for the typed ID.
	out, _, err := execute(t, managedFlagArgs()...)
	if err == nil {
		t.Fatalf("the flag ran with no terminal:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "needs a real terminal") {
		t.Errorf("the refusal does not say a terminal is needed: %v", err)
	}
	assertNamesNoFlagItLacks(t, newManagedFlagCmd(), err)
	terminal(t, true)
	for name, typed := range map[string]string{
		"nothing typed":            "\n",
		"another project":          "proj-9999\n",
		"a prefix of this project": "proj-123\n",
		"the same ID uppercased":   "PROJ-1234\n",
		"the ID with a space":      " proj-1234 extra\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := executeStdin(t, typed, managedFlagArgs()...)
			if err == nil {
				t.Fatalf("the flag was written with %q typed:\n%s", typed, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "not confirmed") {
				t.Errorf("the refusal does not say the confirmation was not given: %v", err)
			}
			if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
			}
		})
	}
}

// TestManagedFlagNamesOneProjectAndNoOther: the command is per project with
// the exact project ID — no bulk form, no wildcard, no prefix, no list — and
// the ID it names must be the selected installation's own. A wildcard, a
// second project ID and a project ID that is a valid ID of another project
// are each refused before the cloud is opened.
func TestManagedFlagNamesOneProjectAndNoOther(t *testing.T) {
	r := managedFlagRig(t)
	terminal(t, true)
	for name, args := range map[string][]string{
		"no project ID":           {"managed", "flag", "--budget", "50", "--budget-currency", "USD"},
		"a wildcard":              {"managed", "flag", "*", "--budget", "50", "--budget-currency", "USD"},
		"a prefix wildcard":       {"managed", "flag", "proj-*", "--budget", "50", "--budget-currency", "USD"},
		"a list of two":           {"managed", "flag", initProject, "other-proj", "--budget", "50", "--budget-currency", "USD"},
		"a prefix of the ID":      {"managed", "flag", "proj-12", "--budget", "50", "--budget-currency", "USD"},
		"the ID and a suffix":     {"managed", "flag", initProject + "5", "--budget", "50", "--budget-currency", "USD"},
		"the project number":      {"managed", "flag", "424242424242", "--budget", "50", "--budget-currency", "USD"},
		"a URL":                   {"managed", "flag", "https://console.cloud.google.com/home?project=" + initProject, "--budget", "50", "--budget-currency", "USD"},
		"the ID with a space":     {"managed", "flag", initProject + " ", "--budget", "50", "--budget-currency", "USD"},
		"a Cyrillic lookalike":    {"managed", "flag", "pr\u043ej-1234", "--budget", "50", "--budget-currency", "USD"},
		"an uppercase ID":         {"managed", "flag", "PROJ-1234", "--budget", "50", "--budget-currency", "USD"},
		"a too-short ID":          {"managed", "flag", "abc", "--budget", "50", "--budget-currency", "USD"},
		"another project's ID":    {"managed", "flag", "another-proj", "--budget", "50", "--budget-currency", "USD"},
		"the Fugaro project name": {"managed", "flag", initProjectName, "--budget", "50", "--budget-currency", "USD"},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := executeStdin(t, initProject+"\n", args...)
			if err == nil {
				t.Fatalf("the command accepted %v:\n%s", args, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
			}
		})
	}
	// The one case that works, so the table above is not passing for the
	// wrong reason.
	if _, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...); err != nil {
		t.Fatalf("the exact project ID was refused: %v", err)
	}
}

// TestManagedFlagRefusesABudgetItCannotRecord: the amount is required with no
// default (D64), and it is the marker's own rule — the pair the write of a
// managed creation applies and the read believes. Every row here is refused
// before the confirmation is even asked for, and nothing is written.
func TestManagedFlagRefusesABudgetItCannotRecord(t *testing.T) {
	r := managedFlagRig(t)
	terminal(t, true)
	for name, args := range map[string][]string{
		"neither flag":                {"managed", "flag", initProject},
		"no currency":                 {"managed", "flag", initProject, "--budget", "50"},
		"no amount":                   {"managed", "flag", initProject, "--budget-currency", "USD"},
		"a zero amount":               {"managed", "flag", initProject, "--budget", "0", "--budget-currency", "USD"},
		"a negative amount":           {"managed", "flag", initProject, "--budget", "-1", "--budget-currency", "USD"},
		"an amount above the ceiling": {"managed", "flag", initProject, "--budget", "10000001", "--budget-currency", "USD"},
		"a lower-case currency":       {"managed", "flag", initProject, "--budget", "50", "--budget-currency", "usd"},
		"a four-letter currency":      {"managed", "flag", initProject, "--budget", "50", "--budget-currency", "USDX"},
		"a currency with a newline":   {"managed", "flag", initProject, "--budget", "50", "--budget-currency", "US\n"},
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := executeStdin(t, initProject+"\n", args...)
			if err == nil {
				t.Fatalf("the command accepted %v:\n%s", args, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "budget") {
				t.Errorf("the refusal does not name the budget: %v", err)
			}
			// The budget is checked before the cloud is opened: no read of
			// the project's number, and nothing written anywhere.
			if len(r.crm.Requests()) != 0 {
				t.Errorf("a flag with a budget it cannot record read the project: %v", r.crm.Requests())
			}
			if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
			}
		})
	}
	// And the refusal offers the two flags the command defines, which is what
	// a person needs to act on: every --token in it is a flag of this command.
	_, _, err := execute(t, "managed", "flag", initProject)
	assertNamesNoFlagItLacks(t, newManagedFlagCmd(), err)
}

// TestManagedFlagNeedsTheProjectsOwnRunsBucket is the marker's own discipline
// on the flag's path: the object may only be written into the runs bucket
// that is the project's own, by project number and by the installation's
// label. A bucket name is global, so without both a mark in a stranger's
// bucket would read as a fact about this project — and the flag that could
// not prove it writes nothing.
func TestManagedFlagNeedsTheProjectsOwnRunsBucket(t *testing.T) {
	for name, tc := range map[string]struct {
		bucket string
		number uint64
		labels map[string]string
	}{
		"another project's bucket": {initRunsBucket, 999, managedBucketLabels},
		"an unlabelled bucket":     {initRunsBucket, initProjectNumber, nil},
		"a label someone else set": {initRunsBucket, initProjectNumber, map[string]string{"fugaro": "managed-by-someone"}},
		"no runs bucket at all":    {"fugaro-runs-somewhere", initProjectNumber, managedBucketLabels},
	} {
		t.Run(name, func(t *testing.T) {
			noAgentSession(t)
			noRunSession(t)
			r := newInitRig(t)
			terminal(t, true)
			// Only the row's bucket is seeded. The installation's runs bucket
			// is the name the write looks in (fugaro-runs-<gcp_project>), so
			// the last row seeds one of another name instead and the write
			// finds no bucket at all.
			r.gcs.AddBucket(tc.bucket, tc.number, tc.labels)
			out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
			if err == nil {
				t.Fatalf("the flag was written into a bucket that is not the project's own:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
			}
		})
	}
}

// TestManagedFlagNeedsTheDefaultRunsBucket: the marker is only ever read from
// the project's runs bucket under its default name — the rule the managed
// apply and fugaro allow-delete already apply — so a marker written into a
// bucket the config names instead would be read by nobody, which is the fail-
// open direction of a command whose whole point is the object it writes.
func TestManagedFlagNeedsTheDefaultRunsBucket(t *testing.T) {
	r := managedFlagRig(t)
	// The config names another bucket, which the installation otherwise has.
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
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err == nil {
		t.Fatalf("the marker was written into a bucket nothing reads:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), initRunsBucket) {
		t.Errorf("the refusal does not name the bucket the marker has to be in: %v", err)
	}
	for _, bucket := range []string{initRunsBucket, other} {
		if got, ok := managedFlagAt(t, r.gcs, bucket); ok {
			t.Errorf("a refused run wrote gs://%s/%s = %s", bucket, managed.MarkerObject, got)
		}
	}
}

// TestManagedFlagNeedsTheDerivedStateBucket: the runs bucket's rule, applied to
// the other bucket a managed project's objects live in, and the reason a
// creation refused for --state-bucket cannot be answered with "create it
// unmanaged and flag it later". An unmanaged creation resolves
// terraform.state_bucket out of this same config (infra.Installation's firstOf),
// so flagging that project would be flagging one whose installation state the
// managed read cannot find — managedAccounted reads gs://fugaro-tfstate-
// <gcp_project> by that derived name and Inventory.installationCreated
// recognises the same name — leaving every apply of it on the typed
// confirmation flagging exists to take away. A project whose state is in
// another bucket is refused here rather than flagged into that state.
func TestManagedFlagNeedsTheDerivedStateBucket(t *testing.T) {
	r := managedFlagRig(t)
	const other = "fugaro-tfstate-elsewhere"
	r.appendConfig(t, "terraform: { state_bucket: "+other+" }\n")
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err == nil {
		t.Fatalf("a project whose installation state nothing reads was flagged:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	all := out + err.Error()
	for _, want := range []string{initStateBucket, other} {
		if !strings.Contains(all, want) {
			t.Errorf("the refusal does not name %s: %s", want, all)
		}
	}
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
	}
}

// The control beside it: the derived name written out in the config is the
// value a creation that passes no flag resolves to anyway, so recording it
// explicitly is not a reason to refuse — the check is about which bucket the
// state is in, not about the field being set.
func TestManagedFlagAcceptsTheDerivedStateBucketRecordedExplicitly(t *testing.T) {
	r := managedFlagRig(t)
	r.appendConfig(t, "terraform: { state_bucket: "+initStateBucket+" }\n")
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, ok := managedFlagAt(t, r.gcs, initRunsBucket); !ok {
		t.Errorf("no %s in gs://%s for a config that records the derived state bucket:\n%s", managed.MarkerObject, initRunsBucket, out)
	}
}

// TestManagedFlagReplacesTheCreationFactThatRecordsNoAmount: a project Fugaro
// created without --managed holds the version-1 creation fact and no amount,
// and the apply's gate refuses it for exactly that (internal/managed's
// refusal names this command as the way out). So flagging it replaces that one
// object — and says so, because "created the marker" and "replaced the fact
// that recorded no amount" are different things to have done.
func TestManagedFlagReplacesTheCreationFactThatRecordsNoAmount(t *testing.T) {
	r := managedFlagRig(t)
	const existing = `{"version":1,"gcp_project":"proj-1234"}`
	r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(existing))
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := `{"version":2,"gcp_project":"proj-1234","budget":{"amount":50,"currency":"USD"}}`
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); !ok || got != want {
		t.Errorf("gs://%s/%s = %s (present %v), want %s", initRunsBucket, managed.MarkerObject, got, ok, want)
	}
	if !strings.Contains(out, "replaced the creation fact") || !strings.Contains(out, "recorded no budget amount") {
		t.Errorf("the command did not say it replaced the creation fact:\n%s", out)
	}
	// The replacement writes the label too, and the read believes the project
	// afterwards: a project Fugaro created without --managed had the object
	// and no label (0.8.5's state), and the replacement is what makes it
	// managed rather than half-marked. The owner's ruling allows exactly this
	// direction — a version-1 fact to version 2, never a version 2 again.
	if got := r.crm.Labels(initProject)[managed.ProjectLabelKey]; got != managed.ProjectLabelValue {
		t.Errorf("the project carries %s=%q after the replacement, want %q", managed.ProjectLabelKey, got, managed.ProjectLabelValue)
	}
	if !managedFlagIsManaged(t, r) {
		t.Error("the managed read does not believe a project whose version-1 fact this command replaced")
	}
}

// TestManagedFlagRefusesAProjectThatAlreadyRecordsAnAmount: the marker is a
// fact, and the amount in it is the owner's decision at the time it was taken.
// A project that already records one — created by a managed run, or flagged
// before — is refused rather than flagged again: this command adds a budget,
// it does not edit one. So is every object that is not this project's
// version-1 fact: a marker about another project, a version this build does
// not know, and an object that is not a marker at all.
func TestManagedFlagRefusesAProjectThatAlreadyRecordsAnAmount(t *testing.T) {
	for name, existing := range map[string]string{
		"a version-2 marker":                 `{"version":2,"gcp_project":"proj-1234","budget":{"amount":50,"currency":"USD"}}`,
		"a version this build does not know": `{"version":3,"gcp_project":"proj-1234"}`,
		"a marker about another project":     `{"version":1,"gcp_project":"another-proj"}`,
		"an object that is not a marker":     "not json at all",
	} {
		t.Run(name, func(t *testing.T) {
			r := managedFlagRig(t)
			r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(existing))
			terminal(t, true)
			out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
			if err == nil {
				t.Fatalf("an object the command may not replace was flagged over:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "already there") {
				t.Errorf("the refusal does not say the object is already there: %v", err)
			}
			if got, _ := managedFlagAt(t, r.gcs, initRunsBucket); got != existing {
				t.Errorf("a refused flag changed the object: %s → %s", existing, got)
			}
		})
	}
}

// TestManagedFlagOffersNoWayAroundTheTypedID: the confirmation is the whole
// boundary D58's answer has, so nothing stands in for it — no --yes, no
// --non-interactive, and no flag naming more than one project. This is the
// assertion a later change that added one would break.
func TestManagedFlagOffersNoWayAroundTheTypedID(t *testing.T) {
	cmd := newManagedFlagCmd()
	for _, name := range []string{"yes", "non-interactive", "all", "projects", "force", "allow-delete"} {
		if cmd.Flags().Lookup(name) != nil || cmd.PersistentFlags().Lookup(name) != nil {
			t.Errorf("fugaro managed flag defines --%s: the typed GCP project ID is the only confirmation it takes", name)
		}
	}
	// What it does define: the cloud flags, and the required budget pair.
	for _, name := range []string{"budget", "budget-currency", "project", "config", "gcp-project", "region"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("fugaro managed flag does not define --%s", name)
		}
	}
	// It takes exactly one project ID and nothing else.
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("fugaro managed flag accepts no project ID")
	}
	if err := cmd.Args(cmd, []string{initProject, "another-proj"}); err == nil {
		t.Error("fugaro managed flag accepts more than one project ID")
	}
	if err := cmd.Args(cmd, []string{initProject}); err != nil {
		t.Errorf("fugaro managed flag refuses one project ID: %v", err)
	}
}

// TestManagedFlagRefusesAGcpProjectFlagThatContradictsTheArgument: --project,
// --config and the checkout select the installation, the argument names the
// GCP project, and --gcp-project is a cloud flag that may not contradict the
// selected config. The command checks the argument against the config's own
// gcp_project after the selection, so none of the three can point the write
// at a project whose credentials it is not using.
func TestManagedFlagRefusesAGcpProjectFlagThatContradictsTheArgument(t *testing.T) {
	r := managedFlagRig(t)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", "managed", "flag", initProject, "--gcp-project", "another-proj", "--budget", "50", "--budget-currency", "USD")
	if err == nil {
		t.Fatalf("--gcp-project named another project and the flag went ahead:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
	}
}

// TestManagedFlagRefusesWhenTheRunsBucketRootRefusesTheWrite: the marker sits
// at the runs bucket's root, where only an operator's own credential may
// write. A write the bucket refuses is reported as what it is — the root, and
// who may write there — rather than as an unknown remote failure.
func TestManagedFlagRefusesWhenTheRunsBucketRootRefusesTheWrite(t *testing.T) {
	r := managedFlagRig(t)
	// The fake refuses object writes into the bucket's root, the way the
	// installation's own grants do for every account a run holds.
	r.gcs.DenyWrites(initRunsBucket, managed.MarkerObject)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err == nil {
		t.Fatalf("the flag was written where the bucket refuses writes:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: run this as an operator)", ExitCode(err), ExitUserError)
	}
	// The reach it names is the launcher condition's (gcp.LauncherBucketCondition):
	// runs/ and, since 0.9.0's sealed budget token, tokens/.
	if !strings.Contains(err.Error(), "a launcher writes only runs/ and tokens/") {
		t.Errorf("the refusal does not name the launcher's write reach as runs/ and tokens/: %v", err)
	}
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
	}
}
