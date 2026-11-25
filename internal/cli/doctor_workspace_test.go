package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
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

// TestDoctorFlagsASwitchTheRouteShadows: the member's line for the personal
// half of the workspace's credentials (WC11, D3141, F24). The line is built
// from its three facts rather than read, so every state it can report is
// stated here without a database — which is also what proves doctor calls
// neither the workspace's service nor its intake to say it (F24): the entry
// point's own reads are the project's published policy and the route
// derivation beside the line.
func TestDoctorFlagsASwitchTheRouteShadows(t *testing.T) {
	lc := func(address string) *localcfg.Config {
		return &localcfg.Config{Name: initProjectName, GCPProject: initProject, Workspace: "alpha",
			WorkspacePin: localcfg.WorkspacePin{ServiceAddress: address}}
	}
	workspace := creds.Route{Model: creds.HolderWorkspace}
	project := creds.Route{Model: creds.HolderProject}

	// The switch off is a measurement and not a defect: every run is served
	// the workspace's shared entries, and the fix names the one command that
	// turns it on plus the update that publishes it.
	off := workspacePersonalLine(lc("https://svc.example"), false, routeConfigOf(lc("https://svc.example")), map[string]creds.Route{"repo-a": workspace}, true)
	if off.ID != workspacePersonalCheckID || !off.OK || off.Severity != "info" {
		t.Errorf("off = %+v, want the %s info line", off, workspacePersonalCheckID)
	}
	for _, want := range []string{creds.PersonSwitchKey, "off", selfCommand() + " settings set " + creds.PersonSwitchKey + " true", selfCommand() + " update", "R9"} {
		if !strings.Contains(off.Problem+off.Fix, want) {
			t.Errorf("the off line does not carry %q:\nproblem: %s\nfix: %s", want, off.Problem, off.Fix)
		}
	}

	// On and every repository's model route is the workspace's: the switch is
	// served, and there is nothing to run.
	served := workspacePersonalLine(lc("https://svc.example"), true, routeConfigOf(lc("https://svc.example")), map[string]creds.Route{"repo-a": workspace}, true)
	if !served.OK || served.Severity != "info" {
		t.Errorf("served = %+v, want an info line with nothing to run for it", served)
	}

	// On where the model route is this project's is the state D3141 makes a
	// trap, and it is a warning: the switch is on and reaches nothing. It
	// names the repositories it is shadowed in, so a person can act on it
	// without asking the workspace anything.
	shadowed := workspacePersonalLine(lc("https://svc.example"), true, routeConfigOf(lc("https://svc.example")),
		map[string]creds.Route{"repo-a": workspace, "repo-b": project}, true)
	if shadowed.OK || shadowed.Severity != "warning" {
		t.Errorf("shadowed = %+v, want a warning", shadowed)
	}
	for _, want := range []string{"repo-b", "D3141", creds.PersonSwitchKey} {
		if !strings.Contains(shadowed.Problem+shadowed.Fix, want) {
			t.Errorf("the shadowed line does not carry %q:\nproblem: %s\nfix: %s", want, shadowed.Problem, shadowed.Fix)
		}
	}
	if strings.Contains(shadowed.Problem, "repo-a") {
		t.Errorf("the shadowed line names a repository whose route is the workspace's: %s", shadowed.Problem)
	}

	// A pin that names no service address is the same shadow reached another
	// way: the project publishes no route at all, so no repository of it can
	// be served a personal token whatever the switch says.
	nopin := lc("")
	unpinned := workspacePersonalLine(nopin, true, routeConfigOf(nopin), map[string]creds.Route{"repo-a": project}, true)
	if unpinned.OK || unpinned.Severity != "warning" {
		t.Errorf("unpinned = %+v, want a warning", unpinned)
	}
	for _, want := range []string{"--service-address", creds.PersonSwitchKey} {
		if !strings.Contains(unpinned.Problem+unpinned.Fix, want) {
			t.Errorf("the unpinned line does not carry %q:\nproblem: %s\nfix: %s", want, unpinned.Problem, unpinned.Fix)
		}
	}

	// A derivation that could not be made is a read that answers nothing, and
	// the line is unread rather than a measurement: "the switch is served"
	// would be an answer nobody checked (R9: a policy that could not be read
	// is an error, never off).
	unread := workspacePersonalLine(lc("https://svc.example"), true, routeConfigOf(lc("https://svc.example")), nil, false)
	if !unread.unread || unread.OK {
		t.Errorf("unread = %+v, want an unread line", unread)
	}
}
