package cli

import (
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
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

// TestDoctorMemberChecksCarryThePersonalTokenLine: the member's line inside
// doctor's own run (WC11, D3141, F24). TestDoctorFlagsASwitchTheRouteShadows
// pins every state the line can report, on the facts it is built from; what it
// cannot see is whether the run carries the line at all, nor that saying it
// reaches neither the workspace's service nor its intake is a property of the
// run rather than of the line's own construction.
//
// The address the pin names is a server that records every request it is sent,
// so a call to the workspace from doctor is a request here and not something a
// reader has to take on trust (F24): the two documents the line is made of are
// this machine's own — the project's published policy, and the route derivation
// the line beside it had already made.
func TestDoctorMemberChecksCarryThePersonalTokenLine(t *testing.T) {
	var dialed atomic.Int64
	// The member's machine, with the workspace's service at an address that is
	// a server rather than a name: nothing doctor does may reach it. The address
	// has to be https (the pin's own rule), so the count is of whatever the
	// server answers — a request that got as far as the handler, and a handshake
	// it refused, which is what a caller using the wrong scheme would produce.
	memberRun := func(t *testing.T, extraRepo string) *routeRig {
		t.Helper()
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			dialed.Add(1)
			w.WriteHeader(http.StatusNotFound)
		}))
		srv.Config.ErrorLog = log.New(countingWriter{&dialed}, "", 0)
		t.Cleanup(srv.Close)
		sm := gcpfake.NewSecrets(t)
		pin := strings.Replace(routeMemberPin, routeAddress, srv.URL, 1)
		if pin == routeMemberPin {
			t.Fatal("routeMemberPin no longer carries the address this test replaces")
		}
		f := newProjectFixture(t, pin+routeCredsService)
		rewriteConfigLine(t, "no_auth: true", "no_auth: true, secret_manager: "+sm.URL+"/")
		if extraRepo != "" {
			rewriteConfigLine(t, "  acme/other: { provider: bitbucket, workflows: [web] }",
				"  acme/other: { provider: bitbucket, workflows: [web] }\n"+extraRepo)
		}
		return newRouteRigOn(t, f, sm)
	}

	t.Run("the switch over a route the project serves", func(t *testing.T) {
		r := memberRun(t, "")
		routeSeedBundle(t, r.sm, config.AnthropicSecret+": "+credsValue(t, "paid"))
		routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"github/acme/app"}},
			[]string{"bitbucket/acme/other", "github/acme/app"},
			map[string]any{creds.PersonSwitchKey: true})
		r.apply(t)

		o, out := wsDoctorRun(t)
		c, ok := doctorCheckByID(o.Checks, workspacePersonalCheckID)
		if !ok {
			t.Fatalf("doctor's run on a member's machine reports no %s check:\n%s", workspacePersonalCheckID, out)
		}
		// The switch is on in the node this run read, and acme/app's model route
		// is this project's own — the D3141 trap, said where the line is wired
		// in rather than only where it is built.
		if c.OK || c.Severity != "warning" {
			t.Errorf("%s = %+v, want the D3141 warning: the published node's switch is on and acme/app's model route is this project's own", workspacePersonalCheckID, c)
		}
		for _, want := range []string{"acme/app", "D3141", creds.PersonSwitchKey} {
			if !strings.Contains(c.Problem+c.Fix, want) {
				t.Errorf("%s does not carry %q:\nproblem: %s\nfix: %s", workspacePersonalCheckID, want, c.Problem, c.Fix)
			}
		}
	})

	// A repository whose route could not be derived is not a repository whose
	// model route is the workspace's: the line is unread over a project whose
	// repositories were only partly derived, rather than clearing the one the
	// switch is shadowed in (D3141's trap is exactly that credentials of the
	// project's own).
	t.Run("a repository whose route was not derived", func(t *testing.T) {
		r := memberRun(t, "  acme/noprov: { workflows: [web] }")
		routeSeedBundle(t, r.sm, config.AnthropicSecret+": "+credsValue(t, "paid"))
		routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"github/acme/app"}},
			[]string{"bitbucket/acme/other", "github/acme/app"},
			map[string]any{creds.PersonSwitchKey: true})
		r.apply(t)

		o, out := wsDoctorRun(t)
		c, ok := doctorCheckByID(o.Checks, workspacePersonalCheckID)
		if !ok {
			t.Fatalf("doctor's run on a member's machine reports no %s check:\n%s", workspacePersonalCheckID, out)
		}
		// The line is read back through --json, which carries no unread field, so
		// what a reader can tell it by is its own words.
		if c.OK || c.Severity != "info" {
			t.Errorf("%s = %+v, want the info line that answers nothing: one of this project's repositories has no derived route", workspacePersonalCheckID, c)
		}
		if !strings.Contains(c.Problem, "was not checked") {
			t.Errorf("%s does not say whether the switch is shadowed was not checked:\n%s", workspacePersonalCheckID, c.Problem)
		}
		if strings.Contains(c.Problem, "every repository") {
			t.Errorf("%s states every repository's route over one that was never derived:\n%s", workspacePersonalCheckID, c.Problem)
		}
	})

	if n := dialed.Load(); n != 0 {
		t.Errorf("doctor's run sent %d request(s) to the address the pin names, want none: the member's lines are this machine's own documents (F24)", n)
	}
}

// countingWriter counts whatever is written to it: the test server logs a
// handshake it refused here, so a dial that never reached the handler is counted
// beside the requests that did.
type countingWriter struct{ n *atomic.Int64 }

func (w countingWriter) Write(p []byte) (int, error) { w.n.Add(1); return len(p), nil }
