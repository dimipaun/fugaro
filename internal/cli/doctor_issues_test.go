package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/spf13/cobra"
)

// savedIssue is one issue the tests here put in the store: a record the store's
// own shape accepts, with the run it came from (the store keys on it, so two
// issues of one directory need two stems), the status, and the uploaded flag
// the case is about.
func savedIssue(stem string, status issues.Status, uploaded bool) issues.Issue {
	run := "update-20261107183012-" + stem
	i := issues.Issue{
		Version:       issues.RecordVersion,
		LocalID:       "local-" + run,
		Run:           run,
		Reporter:      "ana@example.test",
		Created:       time.Date(2026, 11, 7, 18, 30, 12, 0, time.UTC),
		FailedCommand: "fugaro update",
		Class:         issues.ClassFugaro,
		FugaroVersion: "0.9.0",
		OS:            "linux",
		Status:        status,
		Uploaded:      uploaded,
		Summary:       "the update stops half way",
		Diagnosis:     "the provider cache is stale",
		Steps:         "run fugaro update",
	}
	if uploaded {
		i.Number = 7
		i.URL = "https://github.com/dimipaun/fugaro/issues/7"
	}
	return i
}

// savedStore is a store in a state directory of the caller's choosing, with
// the environment that names it: the same pair doctor reads through os.Getenv.
func savedStore(t *testing.T, state string) (*issues.Store, func(string) string) {
	t.Helper()
	env := func(name string) string {
		if name == "XDG_STATE_HOME" {
			return state
		}
		return ""
	}
	store, err := issues.OpenStore(env, "linux")
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	return store, env
}

// TestDoctorIssuesLineOnlyWhenUnsynced is doctor's one line for the issues this
// machine saved and has not filed: the "doctor is one screen" rule with an item
// in it. A machine with nothing saved prints no issues line at all, a saved
// issue that is waiting — pending, or never uploaded — prints exactly one line
// naming the commands that file it, and once it is filed the line is gone.
func TestDoctorIssuesLineOnlyWhenUnsynced(t *testing.T) {
	for _, tc := range []struct {
		name     string
		save     bool
		status   issues.Status
		uploaded bool
		want     bool
	}{
		{"nothing saved", false, "", false, false},
		{"pending", true, issues.StatusPending, false, true},
		{"dismissed but never uploaded", true, issues.StatusDismissed, false, true},
		{"submitted and uploaded", true, issues.StatusSubmitted, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, env := savedStore(t, t.TempDir())
			if tc.save {
				if err := store.Save(savedIssue("48213", tc.status, tc.uploaded)); err != nil {
					t.Fatalf("saving: %v", err)
				}
			}
			c := doctorIssuesCheck(env, "linux")
			if !tc.want {
				if c != nil {
					t.Fatalf("doctor emitted %+v, want no issues line", *c)
				}
				return
			}
			if c == nil {
				t.Fatal("a saved issue that is unfiled is not a line")
			}
			if c.OK || c.Severity != "info" {
				t.Errorf("the check is %+v, want an informational line", *c)
			}
			// The line is an item of the default view, and it never fails
			// doctor: an unfiled issue is not an installation fault.
			if got := doctorClassOf(*c); got != doctorClassActionable {
				t.Errorf("the check is %q, want %q", got, doctorClassActionable)
			}
			if checksFail([]doctorCheck{*c}, true) {
				t.Error("an unfiled issue fails doctor")
			}
			if !strings.Contains(c.Fix, "issue submit") || !strings.Contains(c.Fix, "issue list") {
				t.Errorf("the fix names no command that files one: %q", c.Fix)
			}
		})
	}

	// Two waiting issues are two counted, and the line says so: one line, and
	// it tells the reader how much is behind it.
	t.Run("two waiting issues", func(t *testing.T) {
		store, env := savedStore(t, t.TempDir())
		for _, stem := range []string{"48213", "48214"} {
			if err := store.Save(savedIssue(stem, issues.StatusPending, false)); err != nil {
				t.Fatalf("saving: %v", err)
			}
		}
		c := doctorIssuesCheck(env, "linux")
		if c == nil {
			t.Fatal("two waiting issues are not a line")
		}
		if !strings.HasPrefix(c.Problem, "2 saved issues are ") {
			t.Errorf("the line counts %q, want the two of them", c.Problem)
		}
	})

	// End to end: doctor reads the same store, so the line is one the reader
	// of the default view gets rather than a check nothing prints — once the
	// tree carries the command the fix names, which the assertion follows so
	// it holds on both sides of T-IS5.
	newDoctorRig(t)
	store, err := issues.OpenStore(os.Getenv, runtime.GOOS)
	if err != nil {
		t.Fatalf("opening the store doctor reads: %v", err)
	}
	if err := store.Save(savedIssue("48213", issues.StatusPending, false)); err != nil {
		t.Fatalf("saving: %v", err)
	}
	out, _, _ := execute(t, "doctor", "--dir", t.TempDir())
	want := 0
	if doctorIssuesLineExists(NewRootCmd()) {
		want = 1
	}
	if got := strings.Count(out, "issues-pending"); got != want {
		t.Errorf("the default view prints the issues line %d times, want %d:\n%s", got, want, out)
	}
}

// TestDoctorIssuesUnreadStoreIsNotZero is the fail-closed arm: when the store
// cannot be read, how many issues are unfiled is unknown, and an unknown that
// is filed as a measurement is the silent pass the classes exist to keep out of
// the view. The line is then an unread one — counted in the pointer line rather
// than printed as an item, because its fix is not a command this build is sure
// to have.
func TestDoctorIssuesUnreadStoreIsNotZero(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "fugaro")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("preparing the state directory: %v", err)
	}
	// A file where the issues directory belongs: listing it fails the way a
	// store the machine cannot read does, with no permission bit involved.
	if err := os.WriteFile(filepath.Join(dir, "issues"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("writing over the issues directory: %v", err)
	}
	_, env := savedStore(t, state)
	c := doctorIssuesCheck(env, "linux")
	if c == nil {
		t.Fatal("a store that cannot be read is reported as no unfiled issues")
	}
	if c.OK || c.Severity != "info" || !c.unread {
		t.Errorf("the check is %+v, want an informational unread line", *c)
	}
	if got := doctorClassOf(*c); got != doctorClassAudit {
		t.Errorf("the check is %q, want %q: an unknown is not an item the reader runs", got, doctorClassAudit)
	}
	if checksFail([]doctorCheck{*c}, true) {
		t.Error("an unreadable store fails doctor")
	}
	// It still reaches the reader: counted in the pointer line as the finding
	// it is, never dropped.
	view := doctorDefaultOutput(t, []doctorCheck{*c})
	if strings.Contains(view, "issues-pending") {
		t.Errorf("an unknown is printed as an item:\n%s", view)
	}
	if !strings.Contains(view, "1 more finding") {
		t.Errorf("an unknown is neither printed nor counted:\n%s", view)
	}
}

// TestDoctorIssuesLineWaitsForTheCommandItNames: the fix names `issue submit`,
// so the call site offers the line only when the tree can run it — doctor may
// not answer the reader with "unknown command".
func TestDoctorIssuesLineWaitsForTheCommandItNames(t *testing.T) {
	root := &cobra.Command{Use: "fugaro"}
	if doctorIssuesLineExists(root) {
		t.Error("a tree with no issue command is offered the line")
	}
	parent := &cobra.Command{Use: "issue"}
	root.AddCommand(parent)
	if doctorIssuesLineExists(root) {
		t.Error("a tree that cannot submit is offered the line")
	}
	parent.AddCommand(&cobra.Command{Use: "submit"})
	if !doctorIssuesLineExists(root) {
		t.Error("a tree that carries issue submit is not offered the line")
	}
}

// TestSetupPrintsGhHintOnce: setup recommends `gh` once and never requires it
// (spec 4b). The line is setup's — an update of an installation has nothing to
// do with filing an issue — and it stays out of the two modes whose stdout is
// a document rather than a transcript.
func TestSetupPrintsGhHintOnce(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	out, _, err := executeStdin(t, "", "setup", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.Count(out, "gh auth login"); got != 1 {
		t.Errorf("setup prints the gh hint %d times, want once:\n%s", got, out)
	}
	if !strings.Contains(out, "install `gh`") || !strings.Contains(out, "never required") {
		t.Errorf("setup does not recommend gh and gh auth login without requiring it:\n%s", out)
	}

	// --json's stdout is one object: the hint is not part of it.
	jsonOut, _, err := executeStdin(t, "", "setup", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, jsonOut)
	}
	if strings.Contains(jsonOut, "gh auth login") {
		t.Errorf("--json prints the gh hint into its own document:\n%s", jsonOut)
	}

	// And update is not setup: an installation that is already there gets no
	// hint about filing an issue.
	upOut, _, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, upOut)
	}
	if strings.Contains(upOut, "gh auth login") {
		t.Errorf("update prints setup's gh hint:\n%s", upOut)
	}
}
