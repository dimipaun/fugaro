package cli

// The workspace's own cap (F10, D1299/D1303): `fugaro workspace cap set
// --workspace <name> --monthly <amount>` records it in the config of the
// project the workspace is named after, and only there — `fugaro update`
// publishes it with the record, and the workspace's own service meters and
// enforces it (W2's stage is what writes the cloud; nothing here does).
//
// Why it has a file of its own rather than a place in workspace_test.go: the
// cap is one of the two things W8 owns, and the word it is spelled with is a
// decision (F10) rather than a detail of this family's plumbing.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestWorkspaceCapSetWritesTheLocalConfigOnly: W8's own test, in the file this
// task's plan gives `cap set` -- the cap is recorded in the local config of the
// workspace's own project, the member's config is untouched, and the cloud is
// written by nobody here (W2's publish is the stage that does).
func TestWorkspaceCapSetWritesTheLocalConfigOnly(t *testing.T) {
	_, aurora, owner := workspaceOwnerHome(t)
	auroraBefore := readFileString(t, aurora)
	out, _, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50", "--yes")
	if err != nil {
		t.Fatalf("workspace cap set: %v\n%s", err, out)
	}
	if got := readFileString(t, aurora); got != auroraBefore {
		t.Errorf("cap set wrote another project's config:\n%s", got)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, owner)))
	if err != nil {
		t.Fatal(err)
	}
	if lc.WorkspaceCap == nil || lc.WorkspaceCap.Monthly != 50 {
		t.Errorf("the cap is %+v, want 50 a month", lc.WorkspaceCap)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not hand off to fugaro update:\n%s", out)
	}
}

// TestWorkspaceCapIsNeverSpelledGlobal: F10's consequence, stated as a test so
// the two surfaces cannot drift into meaning the same thing. `--global` is the
// project's word (F9: the caps at config/caps/global, merged so only tightening
// wins), and an operator who read "the workspace's cap" where the command meant
// "this project's cap" would state a ceiling on the wrong thing. So a
// workspace's cap is named by a workspace every time — there is no scope it
// falls back to, and the words the project's own surface uses are not this
// command's — while `--global` on the project's surface keeps writing the
// project's caps into the database and no config's workspace_cap.
func TestWorkspaceCapIsNeverSpelledGlobal(t *testing.T) {
	h := newWSUpdateHome(t, wsOwnerTable)
	owner := h.project(t, "workspace-alpha").path
	before := readFileString(t, owner)

	// Left out, and stated empty: neither is a scope. The refusal is the same
	// one, and it says why a workspace is the only way to state this cap.
	for _, args := range [][]string{
		{"workspace", "cap", "set", "--monthly", "50", "--yes"},
		{"workspace", "cap", "set", "--workspace", "", "--monthly", "50", "--yes"},
	} {
		if _, _, err := execute(t, args...); err == nil || !strings.Contains(err.Error(), "never spelled globally") {
			t.Errorf("%v: %v, want the refusal that a cap belongs to one workspace", args, err)
		}
	}
	// The words the other two scopes are spelled with are unknown here rather
	// than read as one of them.
	for _, word := range []string{"--global", "--all", "--defaults"} {
		if _, _, err := execute(t, "workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50", word, "--yes"); err == nil ||
			!strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("workspace cap set %s: %v, want an unknown flag: this command has no scope but the named workspace", word, err)
		}
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("a refused cap set wrote the file:\n%s", after)
	}

	// The other direction, on one installation: the project's own `--global`
	// writes its caps into the database, and no config on this machine gains a
	// workspace cap from it.
	db := h.project(t, "workspace-alpha").db
	db.Set(budget.PathProject, "workspace-alpha")
	if _, _, err := execute(t, "budget", "set", "--global", "--daily", "5", "--yes"); err != nil {
		t.Fatalf("budget set --global: %v", err)
	}
	if db.Value(budget.PathCapsGlobal) == nil {
		t.Error("budget set --global wrote no project cap, so this half proves nothing")
	}
	if after := readFileString(t, owner); after != before {
		t.Errorf("the project's --global cap wrote a workspace cap into a config:\n%s", after)
	}
}
