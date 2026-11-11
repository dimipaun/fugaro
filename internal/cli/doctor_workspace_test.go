package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestDoctorWorkspaceSplitCheck: doctor's line for the one workspace state W3's
// root cannot converge (doctorWorkspaceSplitCheck), on the configs that decide
// it. The check is built by hand because it reads the local config and nothing
// else — the plane a cloudless test can state.
func TestDoctorWorkspaceSplitCheck(t *testing.T) {
	owner := func(firebase string) *localcfg.Config {
		members := map[string]localcfg.WorkspaceMember{"member-b": {FP: "member-b"}}
		return &localcfg.Config{Name: initProjectName, GCPProject: initProject, WorkspaceMembers: &members,
			Budget: &localcfg.Budget{FirebaseProject: firebase}}
	}

	// A workspace's own project whose Firebase is its own is every installation
	// this root converges: no line.
	if c := doctorWorkspaceSplitCheck(owner(initProject)); c != nil {
		t.Errorf("doctor reports a check for a workspace's own project whose Firebase is its own: %+v", c)
	}
	// And an installation that is not a workspace's own is not this state
	// whatever its Firebase says: the workspace's own half is what declares it.
	member := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Workspace: "alpha",
		Budget: &localcfg.Budget{FirebaseProject: wsSplitFirebaseProject}}
	if c := doctorWorkspaceSplitCheck(member); c != nil {
		t.Errorf("doctor reports a check for a member installation: %+v", c)
	}

	c := doctorWorkspaceSplitCheck(owner(wsSplitFirebaseProject))
	if c == nil {
		t.Fatalf("doctor reports no check for a split workspace owner")
	}
	if c.ID != workspaceSplitCheckID || c.OK || c.Severity != "warning" {
		t.Errorf("check = %+v, want the %s warning", c, workspaceSplitCheckID)
	}
	for _, want := range []string{initProject, wsSplitFirebaseProject, "budget.firebase_project", "no service account is created", "fugaro update --firebase-project " + initProject} {
		if !strings.Contains(c.Problem+c.Fix, want) {
			t.Errorf("the check does not carry %q:\nproblem: %s\nfix: %s", want, c.Problem, c.Fix)
		}
	}
}
