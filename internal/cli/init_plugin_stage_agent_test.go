package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// TestInitPluginStageMovesInAgentSessionAheadOfARepositoryStop is U0 of
// docs/plugin-user-scope/implementation-plan.md (reconciliation.md F13): the
// one claim in that design that could ship as a bug if it is wrong. A coding
// agent's session runs with the agent marker set (the way
// internal/initflow/loop.go applies LocalUnderAgent), in a repository whose
// repository stage returns needs-you — here the checkout's own fugaro.yaml is
// the invalid v0.7.0 shape convergeLegacyYAML carries, so the repository stage
// is the user's to fix and the checkout's cloud step may not be built from it.
// In that session the plugin step still reaches the machine: the plugin is
// installed in the user's own Claude Code, exactly as it does when the
// repository stage is done. The cloud half is the step this session leaves for
// the user — its job images are built from the fugaro.yaml the run did not
// onboard — so the stage is done with what it may do and the run exits 1.
//
// What the test decides is the plugin step's position. The claim is not true
// yet, so the body is behind the tree's guard for exactly that — the FUTURE
// skip of managed_test.go's TestManagedConfigClaimingManagedIsNotManaged — and
// that guard may not come off here: a red main is fixed before anything else
// merges (docs/dogfooding.md), and every concurrent run's own verify runs this
// package (fugaro.yaml), so an unguarded red would end other runs on a test
// they did not touch. U1 is what moves the step into the plugin stage and makes
// that stage LocalUnderAgent; it is also the task that removes this skip, and
// until it does the skip is the only thing keeping the claim out of the test
// run.
//
// With the skip removed today the first failure names the reason:
//
//	Claude Code lists the plugin as ... after the run, want ...: the plugin
//	step was left for the user in a coding agent's session, though its write is
//	on this machine, and the run never reached it
//
// Today the plugin step lives in the checkout stage, which a coding agent's
// session never reaches here: the plugin stage — a stage neither
// LocalUnderAgent nor SelfConfirming — is left whole for the user, the loop
// stops there, and everything behind it, the repository stage and the
// checkout's own plugin step included, is blocked. So the plugin stays on the
// previous release and nothing local moves. After U1 the assertions below hold:
// the plugin installs, the repository stage stops needs-you, and the checkout
// runs past that stop to park its cloud half.
func TestInitPluginStageMovesInAgentSessionAheadOfARepositoryStop(t *testing.T) {
	t.Skip("FUTURE (U1, docs/plugin-user-scope/implementation-plan.md): no skip-free red may land — docs/dogfooding.md fixes a red main before anything else merges, and every run's own verify runs this package (fugaro.yaml). The claim below is false until U1 moves the plugin step into the plugin stage and makes that stage LocalUnderAgent; U1 removes this skip and changes nothing else in this file.")
	r := newConvergeRigYAML(t, v4Node(), convergeLegacyYAML)
	currentRelease(t)
	// The staleness line is once per process; an earlier test's run may have
	// spent it.
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	// The coding agent's session: the marker internal/initflow/loop.go reads to
	// leave an applying stage for the user.
	t.Setenv("CLAUDECODE", "1")

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err == nil {
		t.Fatalf("the run reported success while the checkout's cloud half is parked:\n%s", out)
	}
	var res convergeJSON
	if uerr := json.Unmarshal([]byte(runJSONObject(t, out)), &res); uerr != nil {
		t.Fatalf("%v:\n%s\n%s", uerr, out, errOut)
	}

	// The plugin step reached the machine: the plugin is installed in the
	// user's own Claude Code, which is the step that clears the staleness line
	// every command prints. This is the assertion the design is decided by, so
	// it comes first and its failure names the step being left for the user.
	if got := claudeListVersion(t, r); got != convergeRelease {
		t.Fatalf("Claude Code lists the plugin as %q after the run, want %q: the plugin step was left for the user in a coding agent's session, though its write is on this machine, and the run never reached it\n%s", got, convergeRelease, out)
	}
	// And it is not left for the user: the plugin's own route (`fugaro
	// update-skills`) is not what this run hands back.
	for _, l := range res.Left {
		if l["stage"] == initflow.Plugin && strings.Contains(l["text"], selfCommand()+" update-skills") {
			t.Errorf("the plugin step is left for the user (%q), want it applied in a coding agent's session\n%s", l["text"], out)
		}
	}

	// The fixture is the design's: the repository stage stops for the user, and
	// the file it stopped over is not one this run may build from.
	if got := res.state(initflow.Repository); got != "needs-you" {
		t.Errorf("the repository stage is %q, want needs-you: the invalid fugaro.yaml is the user's to fix, and the plugin step is ahead of it\n%s", got, out)
	}

	// The cloud half is parked and stays parked: no job image reaches Cloud
	// Build, and the user is left the command that runs it in their own
	// terminal, beside the repository line they are to fix.
	if n := len(r.builder.specs); n != 0 {
		t.Errorf("%d job image(s) reached Cloud Build from a fugaro.yaml the repository stage did not onboard: %+v\n%s", n, r.builder.specs, out)
	}
	if !strings.Contains(out+errOut, "cloud: skipped: a coding agent's session") {
		t.Errorf("the cloud step was not parked by name:\n%s\n%s", out, errOut)
	}
}
