package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// managed_require_test.go is D1541's fence's own test: a mutating command on a
// project that is not managed refuses, says which project it read as unmanaged
// and why, and prints the one command that flags it — with the GCP project ID
// as that command's argument, so nothing is typed at a prompt and no flag
// stands in for it.
//
// The mutating command driven here is `fugaro restore`, the one in T6's files
// that reads a project's creation fact anyway: an installation whose old
// project is not managed is refused by the read the restart already makes. The
// flag and the unflag are not driven: they are the marks' own command, and a
// project that is not managed is what they are run on.

func TestUnmanagedProjectRefusesAndNamesTheFlagCommand(t *testing.T) {
	noAgentSession(t)
	noRunSession(t)
	// The binary's own name, so the sentence the owner reads is pinned here
	// rather than whatever path the test binary was started from.
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })

	r, _ := newRestoreRig(t)
	// The old installation answers and is not managed: its runs bucket is there
	// (so this is a project the run can read, and not a lost one), and neither
	// half of the mark is — no creation fact in the bucket and no
	// fugaro_managed label on the project itself. It is the shape of a project
	// fugaro created before it created one managed.
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	_, root := restoreCheckout(t)
	// The creating path the restore delegates to needs a real terminal; the
	// refusal below comes before any of its steps.
	terminal(t, true)
	out, errOut, err := execute(t, "restore", restoreNewProj, root, "--config", r.cfg, "--billing-account", billingAcc)
	if err == nil {
		t.Fatalf("a mutating command ran on a project that is not managed:\n%s\n%s", out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	said := out + errOut + err.Error()
	for _, want := range []string{
		// What the command declines, from its own annotation.
		"nothing was restored: a restore rebuilds a managed project",
		// Which project, and what the managed read answered about it.
		initProject, "is not managed",
		// The one command that flags it, with the project as its argument.
		"fugaro managed flag " + initProject + " --budget N --budget-currency CCC",
		// The argument is the whole consent, so nothing is typed at a prompt.
		"the argument is the declaration itself and nothing is typed at a prompt",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, said)
		}
	}
	// The reason the read gave is in the sentence too: which half of the mark is
	// the one that is missing, rather than only that something is.
	if !strings.Contains(said, managed.ProjectLabelKey) && !strings.Contains(said, managed.MarkerObject) {
		t.Errorf("the refusal does not say what the managed read found:\n%s", said)
	}
	// The refusal is the whole of the run: nothing created, nothing written.
	if n := len(r.crm.Creates()); n != 0 {
		t.Errorf("the refused run created %d project(s)", n)
	}
	for _, req := range r.gcs.Requests() {
		if req.Method != http.MethodGet {
			t.Errorf("the refused run wrote to GCS: %s %s", req.Method, req.Path)
		}
	}
	// And the sentence is the tree's own declaration for this command:
	// root.go's managedMutatingCommands becomes an annotation on the command, so
	// which commands refuse this way — and what each one says it declines — is
	// one table rather than a search.
	restore, _, err := NewRootCmd().Find([]string{"restore"})
	if err != nil {
		t.Fatalf("finding restore in the tree: %v", err)
	}
	if got := restore.Annotations[managedRequiresAnnotation]; got != "nothing was restored: a restore rebuilds a managed project" {
		t.Errorf("the restore carries the annotation %q, want the clause its refusal prints", got)
	}
	// The other direction: a command the table does not name carries none, so
	// the annotation is not on every command by accident.
	show, _, err := NewRootCmd().Find([]string{"version"})
	if err != nil {
		t.Fatalf("finding version in the tree: %v", err)
	}
	if got := show.Annotations[managedRequiresAnnotation]; got != "" {
		t.Errorf("fugaro version carries the managed annotation %q", got)
	}
}

// TestEveryManagedMutatingCommandResolves: root.go's table says the annotation
// is applied by cobra path and that "the table is checked by a test" — this is
// that check. A path that does not resolve is skipped silently (annotateManaged-
// Mutating's own `continue`), so an entry that is misspelled or that names a
// command the tree no longer has would leave the command refusing an unmanaged
// project in managedRequired's generic words rather than in the clause the table
// wrote for it, and the table would say a command is declared when it is not.
// Every entry has to resolve, carry its own clause, and be a command a person
// runs rather than a group of them.
func TestEveryManagedMutatingCommandResolves(t *testing.T) {
	if len(managedMutatingCommands) == 0 {
		t.Fatal("the table is empty: a command that should refuse an unmanaged project carries no clause")
	}
	for path, clause := range managedMutatingCommands {
		if clause == "" {
			t.Errorf("%s is declared with no clause: its refusal would say nothing about what it declines", path)
		}
		c, _, err := NewRootCmd().Find(strings.Fields(strings.TrimPrefix(path, "fugaro ")))
		if err != nil || c == nil {
			t.Errorf("%s does not resolve in the tree (%v): annotateManagedMutating skips it, so it carries no clause", path, err)
			continue
		}
		if got := c.Annotations[managedRequiresAnnotation]; got != clause {
			t.Errorf("%s carries the annotation %q, want the table's clause %q", path, got, clause)
		}
		if !c.Runnable() {
			t.Errorf("%s is not a runnable command: the table names the commands a person runs", path)
		}
	}
}
