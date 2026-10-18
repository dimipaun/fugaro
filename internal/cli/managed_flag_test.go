package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// managedFlagRig is the installation's own runs bucket, seeded as the write
// proves it: the fakes' project number, and the label the installation puts
// on it. WriteFlag proves both before it writes, so a rig that wants its
// marker recorded has to seed both.
func managedFlagRig(t *testing.T) *initRig {
	t.Helper()
	noAgentSession(t)
	r := newInitRig(t)
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	return r
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

// TestManagedFlagRefusesAProjectThatAlreadyRecordsOne: the marker is a fact
// written once. A project that already records one — created by a run, or
// flagged by an owner before — is refused rather than rewritten, so this
// command never replaces a record with another one.
func TestManagedFlagRefusesAProjectThatAlreadyRecordsOne(t *testing.T) {
	r := managedFlagRig(t)
	const existing = `{"version":1,"gcp_project":"proj-1234"}`
	r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte(existing))
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err == nil {
		t.Fatalf("an already-marked project was flagged again:\n%s", out)
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
	if got, ok := managedFlagAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, got)
	}
}
