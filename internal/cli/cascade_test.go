package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// cascadeCLI is the fugaro.yaml a repository asking for the cascade writes:
// agent.auth: cascade and nothing else about it. What the cascade is *for* is
// the operator's answer (O2) and lives in the project policy, never here.
const cascadeCLI = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: cascade
  model: claude-sonnet-5-5
  models: { background: claude-haiku-4-5 }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`

// cascadePolicy is the published policy that turns the cascade on for aurora's
// cascade workflows: O3's spelling, routing.claude.via, which needs no provider
// route while tier C for Claude is off (C3.6). The version is the one this
// binary's reader expects, read from the package rather than restated.
func cascadePolicy() string {
	return fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}}}`, projectpolicy.Version)
}

// cascadePolicyWithMode is cascadePolicy with the policy's own budget node,
// whose mode is the one the runner overlays onto the installation's ceiling
// (runner.policyCeiling). A policy that carries a budget must name a mode
// (projectpolicy.validBudget), so this is the shape a mode arrives in when the
// project config's own ceiling names none.
func cascadePolicyWithMode(mode string) string {
	return fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}},"budget":{"mode":%q}}`, projectpolicy.Version, mode)
}

// cascadePolicyAllowList is the policy a project that really serves cascade
// runs publishes: O3's switch, and the model allow-list the credentials service
// holds every call of such a run to (the owner's ruling of 2026-10-22 — the
// service refuses a project that publishes none, `fugaro validate` reports it
// to the author (cascadeProblems), the launch refuses it before a job starts
// (cascadeLaunchRefusal) and the run refuses it in bootstrap (runner's
// applyRouting)). A policy that carries a budget block must
// name a mode (projectpolicy.validBudget), so the allow-list never arrives
// alone: the mode is the caller's, because the node's mode is a layer the
// gateway rule and the run's own merge judge, and a test about either has to
// spell the one it needs. The models are the ones cascadeLaunchYAML pins, which
// is what a project serving that repository would list.
func cascadePolicyAllowList(mode string) string {
	return fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}},`+
		`"budget":{"mode":%q,"allowed_models":["claude-sonnet-5-5"]}}`, projectpolicy.Version, mode)
}

// cascadePolicyViaDirect is a policy whose switch is written with the value
// D1b made the default: via: direct, an explicit "no route", which names the
// direct path rather than leaving the block out. It is a *written* switch that
// does not turn the cascade on, which is the shape a reader that asks only
// "is a via written here?" mistakes for the cascade's own.
func cascadePolicyViaDirect() string {
	return fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"direct"}}}`, projectpolicy.Version)
}

// cascadePolicyViaAuto is the other value O3 left exactly as it was: via: auto
// routes the Claude IDs the list names through the provider entry that claims
// them (D1a/D1b — the entry, its claim and its written allow_data_to are all
// here, so CheckRouting accepts the switch). It is a policy an installation may
// really publish, it is not the cascade, and every run of an aurora repository
// under it still has no credentials cascade.
func cascadePolicyViaAuto() string {
	return fmt.Sprintf(`{"version":%d,"allow_repos":["*"],`+
		`"providers":{"openrouter-claude":{"kind":"anthropic-compat","base_url":"https://openrouter.ai/api/v1",`+
		`"auth":"bearer","secret":"openrouter-claude-key","models":["anthropic/claude-sonnet-5.5"],"allow_data_to":["*"]}},`+
		`"routing":{"claude":{"via":"auto","models":["claude-sonnet-5-5"]}}}`, projectpolicy.Version)
}

// cascadeOtherProject points this machine at a *different* installation —
// writeProject's config for name, with its own database — and publishes policy
// in it. The selected project config is then real, readable, and publishes the
// cascade switch for its own runs, while the file being validated is aurora's:
// the state a repository's author is in when their laptop is set up for another
// project. Nothing about the other project's ceiling or switch is the file's.
func cascadeOtherProject(t *testing.T, name, policy string) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	path := writeProject(t, name, "proj-9")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("budget: { mode: observe, per_run_usd: 5, rtdb_url: " + db.URL + " }\n" +
		"endpoints: { no_auth: true }\n"); err != nil {
		t.Fatal(err)
	}
	db.Set("fugaro/project", name)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
	t.Setenv("FUGARO_CONFIG", path)
}

// TestValidateReadsTheGatewayModeFromThePolicyNode: the mode the gateway rule
// is judged over is the *run's*, and the run's ceiling is not the project
// config's alone. The runner overlays the published policy node's budget.mode
// onto the environment ceiling before the file is merged over the two
// (internal/runner/policy.go's policyCeiling, where the node replaces the
// ceiling's mode and not merely tightens it — "the mode is the key this
// matters most for"), and this command already reads that same node for O2.
//
// So a project whose owner sets the mode in the policy alone — the project
// config's ceiling names none, which is the state a policy published for its
// mode leaves behind — is an installation whose every cascade run has the
// gateway on: reading the ceiling without the node refused the file with
// "budget.mode is not set", for a run that observes.
//
// Mutation (run, restored): judge policy.Merge(ceilingLayer(lc),
// runner.FileLayer(cfg)) without the node's mode — this fails on that
// refusal, and the mode the node set is the only thing that changes.
func TestValidateReadsTheGatewayModeFromThePolicyNode(t *testing.T) {
	f := newLaunchBudget(t, "off")
	// The allow-list comes with the mode: what this test is about is the mode
	// the node overlays, and a node without the list would be refused for that
	// instead (TestValidateRefusesCascadeWithoutAllowList).
	publishPolicy(t, f, cascadePolicyAllowList("observe"))
	out, errOut, err := execute(t, "validate", writeConfig(t, cascadeCLI))
	if err != nil {
		t.Fatalf("a cascade file whose policy sets the mode was refused: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if strings.Contains(out+errOut, "needs the budget gateway") {
		t.Errorf("the gateway rule was judged without the policy node's mode:\n%s%s", out, errOut)
	}
}

// TestValidateRefusesCascadeWhenThePolicyNodeTurnsTheGatewayOff is the same
// overlay read the other way, and the reason it is the node's mode the ceiling
// takes rather than a second layer over it. The node *replaces* the ceiling's
// mode (runner.policyCeiling), so a policy that says off means every run of the
// project has the gateway off — even where the project config's own ceiling
// observes. Merging the two as tightening layers (the higher mode wins, which is
// what policy.Merge does between the ceiling and a file) would read this file as
// observing and admit it, for runs the gateway rule refuses.
//
// Mutation (run, restored): merge the node's mode through policy.Merge instead
// of taking it as the ceiling's own — the file is reported valid and this fails.
func TestValidateRefusesCascadeWhenThePolicyNodeTurnsTheGatewayOff(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	// The list is published with the mode, so that the refusal this test reads
	// is the gateway's and not the allow-list's.
	publishPolicy(t, f, cascadePolicyAllowList("off"))
	out, errOut, err := execute(t, "validate", writeConfig(t, cascadeCLI))
	if err == nil {
		t.Fatalf("a cascade file whose policy turns the budget off was admitted:\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if !strings.Contains(out+errOut, "needs the budget gateway") {
		t.Errorf("the file was refused, but not for the gateway the policy turns off:\n%s%s", out, errOut)
	}
}

// TestValidateRefusesCascadeWithoutPolicySwitch is O2 — the owner's ruling of
// 2026-10-17: a repository may *ask* for the cascade, and only the
// operator-written project policy enables it. `fugaro validate` is the surface
// that tells the repository's author so, before any run: a cascade file whose
// published policy does not carry the switch is a problem naming exactly what
// to set and that only the operator can set it. A policy the command cannot
// read at all (none published here, no database to read one from) is refused
// the same way rather than passed silently: a cascade workflow nobody has
// enabled is not a valid file, whichever side failed to answer.
//
// Mutation (run, restored), one per refusal this function makes: drop the
// `!readable` early return and the "no project config" subtest fails; judge
// the gateway rule from the file's own mode before readability is known
// (config.CheckCascadeGateway over policy.Merge(policy.Layer{}, …)) and the
// same subtest fails on the gateway line it may not carry; drop the `refuse`
// call from the `!found || p == nil` case and both the "no policy published"
// and the "no database" subtests fail (they would report the file valid);
// make the routing case unreachable and the "published policy without the
// switch" subtest fails. What is left is the case that must pass — the
// published switch — and the other auth, which never reaches any of them.
func TestValidateRefusesCascadeWithoutPolicySwitch(t *testing.T) {
	t.Run("a published policy without the switch", func(t *testing.T) {
		f := newLaunchBudget(t, "observe")
		publishPolicy(t, f, `{"version":`+fmt.Sprint(projectpolicy.Version)+`,"allow_repos":["*"]}`)
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (err %v, out %q)", ExitCode(err), ExitUserError, err, out)
		}
		for _, want := range []string{
			"agent.auth",
			"routing.claude: {via: cascade}",
			"only the operator",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, out)
			}
		}
	})

	t.Run("the policy's switch is published", func(t *testing.T) {
		f := newLaunchBudget(t, "observe")
		// The list as well as the switch: a policy that carries the switch and
		// no allow-list is refused below, by this rule, so the case that has to
		// pass publishes both.
		publishPolicy(t, f, cascadePolicyAllowList("observe"))
		out, errOut, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if err != nil {
			t.Fatalf("validate: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
		}
		if !strings.Contains(out, "is valid") {
			t.Fatalf("the switch was published and the file was not valid:\n%s\n%s", out, errOut)
		}
	})

	t.Run("no policy published", func(t *testing.T) {
		f := newLaunchBudget(t, "observe")
		markRules(t, f, projectpolicy.RulesVersion)
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError || !strings.Contains(out, "routing.claude: {via: cascade}") {
			t.Fatalf("an unpublished policy passed a cascade file: exit %d, out %q, err %v", ExitCode(err), out, err)
		}
	})

	t.Run("the policy's switch is another via value", func(t *testing.T) {
		// O3 (the owner's answer of 2026-10-17) settled the switch's spelling:
		// the cascade is turned on by routing.claude's via being *cascade*, and
		// by nothing else. via: direct and via: auto are the two other values
		// that block accepts, and each is a policy an installation really
		// publishes — auto with the route entry it names, every rule
		// CheckRouting holds it to met — so a rule that reads "a via is
		// written here" as the switch passes the file for a policy whose runs
		// have no credentials cascade at all. The refusal has to be O2's, not
		// "the policy could not be read": these nodes are exactly what a
		// published policy may be.
		for _, c := range []struct{ name, policy string }{
			{"via: direct", cascadePolicyViaDirect()},
			{"via: auto", cascadePolicyViaAuto()},
		} {
			t.Run(c.name, func(t *testing.T) {
				f := newLaunchBudget(t, "observe")
				publishPolicy(t, f, c.policy)
				out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
				if ExitCode(err) != ExitUserError {
					t.Fatalf("exit = %d, want %d (err %v, out %q)", ExitCode(err), ExitUserError, err, out)
				}
				if strings.Contains(out, "could not be read") {
					t.Fatalf("the policy was refused as unreadable rather than as the wrong switch, so this says nothing about the via rule:\n%s", out)
				}
				for _, want := range []string{"agent.auth", "routing.claude: {via: cascade}", "only the operator"} {
					if !strings.Contains(out, want) {
						t.Errorf("the refusal does not say %q:\n%s", want, out)
					}
				}
			})
		}
	})

	t.Run("another project's config is selected", func(t *testing.T) {
		// The selected project config is another installation's and publishes
		// the cascade switch — for *its* runs. Judging aurora's file by it
		// would read another project's ceiling for the gateway rule and another
		// project's policy for the switch, and report a cascade workflow valid
		// that no run of aurora would be admitted with: the policy a run reads
		// is its own project's, so the switch it can see is the one published
		// for aurora, and there is none.
		isolateProjects(t, t.TempDir())
		cascadeOtherProject(t, "borealis", cascadePolicy())
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError {
			t.Fatalf("another project's published switch admitted aurora's cascade file: exit = %d, want %d (err %v, out %q)", ExitCode(err), ExitUserError, err, out)
		}
		for _, want := range []string{"agent.auth", "aurora", "routing.claude: {via: cascade}", "only the operator"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, out)
			}
		}
	})

	t.Run("the policy could not be read", func(t *testing.T) {
		// A node the installation really published and this Fugaro cannot read:
		// the refusal has to be the read's own, not "this project has published
		// no policy" — which is what an unread node would be reported as if the
		// error arm were dropped, and it names a state that is false of a
		// project whose node is there and lists a key nobody reads.
		f := newLaunchBudget(t, "observe")
		markRules(t, f, projectpolicy.RulesVersion)
		f.db.Set(projectpolicy.PathShared(), json.RawMessage(
			`{"version":`+fmt.Sprint(projectpolicy.Version)+`,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}},"a_key_nobody_reads":1}`))
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (err %v, out %q)", ExitCode(err), ExitUserError, err, out)
		}
		if !strings.Contains(out, "agent.auth") || !strings.Contains(out, "could not be read") {
			t.Errorf("the refusal does not carry the read's own reason:\n%s", out)
		}
		if strings.Contains(out, "has published no policy") {
			t.Errorf("a policy node that is there and unreadable is reported as none published:\n%s", out)
		}
	})

	t.Run("no database to read one from", func(t *testing.T) {
		// The project config names no database URL at all: there is no
		// published policy this command could see, and it says *that* rather
		// than "this project has published no policy", which is false of an
		// installation whose config names no database — the second is a
		// statement about the node, the first about the operator's file, and
		// only the first sends them to `fugaro update --firebase-project`.
		path := budgetProject(t, "budget: { mode: observe, per_run_usd: 5 }\n", "  auth: cascade")
		out, _, err := execute(t, "validate", path)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
		}
		for _, want := range []string{"agent.auth", "routing.claude: {via: cascade}", "only the operator", "names no database"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "has published no policy") {
			t.Errorf("an installation with no database configured is reported as having published no policy:\n%s", out)
		}
	})

	t.Run("no project config for this project is selected", func(t *testing.T) {
		// The checkout names a project this machine has no config for: there
		// is no policy this command could read, and it refuses the file
		// naming the project whose config it would have read one from — the
		// state a repository's author is in on a machine that is not the
		// operator's.
		isolateProjects(t, t.TempDir())
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
		}
		for _, want := range []string{"agent.auth", "aurora", "routing.claude: {via: cascade}", "only the operator"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, out)
			}
		}
		// And the O2 refusal is the whole of it here: with no project config
		// selected this command cannot read the installation's ceiling either,
		// and the file's own mode is only a lower bound on the run's — a file
		// that writes no budget under a ceiling that enforces has the gateway
		// on. So the gateway rule is not judged from it: the line this state
		// used to carry was the file's own mode read as the run's, a problem no
		// run of this configuration necessarily has.
		if strings.Contains(out, "needs the budget gateway") {
			t.Errorf("the gateway rule was judged without the ceiling that decides the run's mode:\n%s", out)
		}
	})

	t.Run("the selected project config could not be read", func(t *testing.T) {
		// The other state selectedProjectConfigErr answers nil for: the config
		// is selected and was refused at load (its switch names a model no
		// entry claims), so there is no policy this command could read. The
		// refusal says that, and not "no project config for aurora is selected
		// here", which is false of it — the reason is the problem the loader's
		// own line carries.
		isolateProjects(t, t.TempDir())
		projectRouting(t, routedSwitchBlock)
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
		}
		if !strings.Contains(out, "agent.auth: is refused: the project config selected here could not be read") {
			t.Errorf("the refusal does not name the state it is in:\n%s", out)
		}
		if strings.Contains(out, "no project config for aurora is selected here") {
			t.Errorf("a config that is there and unreadable is reported as none selected:\n%s", out)
		}
	})

	t.Run("another auth is not this rule's business", func(t *testing.T) {
		f := newLaunchBudget(t, "observe")
		publishPolicy(t, f, `{"version":`+fmt.Sprint(projectpolicy.Version)+`,"allow_repos":["*"]}`)
		other := strings.Replace(cascadeCLI, "auth: cascade", "auth: api-key", 1)
		out, errOut, err := execute(t, "validate", writeConfig(t, other))
		if err != nil {
			t.Fatalf("an api-key file was refused for want of the cascade switch: %v\n%s\n%s", err, out, errOut)
		}
	})
}

// TestValidateRefusesCascadeWithoutAllowList (the owner's ruling of 2026-10-22,
// C1.11): the credentials service holds every call it carries to the project's
// published budget.allowed_models and refuses a project that publishes none, so
// a cascade run of such a project could make no call at all. The launch refuses
// that file before a job starts (cascadeLaunchRefusal) and the run refuses it in
// bootstrap (runner's applyRouting); this is the surface that tells the
// repository's author, and the reason it is a rule of its own here is the reason
// it is one everywhere: every other cascade rule is judged where the file is
// written *and* where it runs.
//
// The policy is the state an installation really reaches — the switch published,
// the budget mode on, and no allow-list, which projectpolicy.validBudget leaves
// as the absent key rather than an empty list — and what is asserted is that the
// refusal is *this* rule's: the switch is published, so nothing may answer with
// O2's sentence about setting it.
//
// Mutation (run, restored): drop the case from cascadeProblems and the first
// half reports the file valid, for a launch that refuses it.
func TestValidateRefusesCascadeWithoutAllowList(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	publishPolicy(t, f, cascadePolicyWithMode("observe"))
	out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a cascade file whose policy publishes no allow-list was admitted: exit = %d, want %d (err %v, out %q)", ExitCode(err), ExitUserError, err, out)
	}
	for _, want := range []string{"agent.auth", "budget.allowed_models", "fugaro project policy publish"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "routing.claude: {via: cascade}") {
		t.Errorf("the allow-list refusal also claimed the policy does not turn the cascade on, which it does:\n%s", out)
	}

	// The way out: the same file, the same installation, the node publishing
	// the allow-list the ruling requires.
	publishPolicy(t, f, cascadePolicyAllowList("observe"))
	out, errOut, err := execute(t, "validate", writeConfig(t, cascadeCLI))
	if err != nil {
		t.Fatalf("a cascade file whose policy publishes the allow-list was refused: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if !strings.Contains(out, "is valid") {
		t.Fatalf("the allow-list was published and the file was not valid:\n%s\n%s", out, errOut)
	}

	// The installation's ceiling is another layer and not the one the service
	// reads: a project config that carries the allow-list does not stand in for
	// the node, and the file is refused just the same.
	publishPolicy(t, f, cascadePolicyWithMode("observe"))
	ceilingAllowList(t, f.cloudFixture)
	out, _, err = execute(t, "validate", writeConfig(t, cascadeCLI))
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "budget.allowed_models") {
		t.Errorf("a cascade file whose ceiling publishes the allow-list and whose policy does not: exit = %d, out %q, err = %v, want the allow-list refusal (the ceiling never reaches the credentials service)", ExitCode(err), out, err)
	}
}

// TestValidateCascadeReadsNoPolicyOffline: `fugaro validate --offline` promises
// the command reads nothing over the network ("never read the runs bucket"), and
// the cascade rule is the one place validate would otherwise break that promise:
// the switch O2 turns the cascade on with lives in the project's *database*, so
// a cascade file sent this command with --offline would still open a connection
// — the thing a CI job with no Google credentials, or an operator looking at a
// file while their database is unreachable, asked it not to do. The rule cannot
// answer without the node (a cascade file nobody has proven is enabled is
// refused, never passed), so it refuses and says which flag it was that kept the
// policy out of reach.
//
// The counter is the fake database's own requests, which is what makes this a
// test of the call and not only of the sentence: the control run below — the
// same file, the same fixture, no --offline — has to move it, and the offline
// run has to leave it where it was.
//
// Mutation (run, restored): drop the offline arm (read the node whatever the
// flag says) — the offline run makes the request and the refusal is the read's
// own, so both the counter and the message fail.
func TestValidateCascadeReadsNoPolicyOffline(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	publishPolicy(t, f, cascadePolicyAllowList("observe"))

	before := len(f.db.Requests())
	out, _, err := execute(t, "validate", "--offline", writeConfig(t, cascadeCLI))
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a cascade file passed --offline validation: exit = %d, want %d (err %v, out %q)", ExitCode(err), ExitUserError, err, out)
	}
	for _, want := range []string{"agent.auth", "--offline", "routing.claude: {via: cascade}", "only the operator"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "could not be read") {
		t.Errorf("the refusal is the read's own, so the read happened:\n%s", out)
	}
	if got := len(f.db.Requests()); got != before {
		t.Errorf("--offline made %d request(s) to the project's database (%d before, %d after)", got-before, before, got)
	}

	// The control: without the flag the same file is judged against the same
	// node, which is a read — so the counter above is measuring the call.
	control := len(f.db.Requests())
	if _, _, err := execute(t, "validate", writeConfig(t, cascadeCLI)); err != nil {
		t.Fatalf("the control run (no --offline) refused the file: %v", err)
	}
	if got := len(f.db.Requests()); got == control {
		t.Fatalf("no request was made without --offline either, so the counter above proves nothing (still %d)", got)
	}
}

// TestValidateOfflineHelpNamesThePolicyRead: --offline's help promised the runs
// bucket alone ("never read the runs bucket: resolve against the cached project
// layer, if any") while the flag now keeps the published policy out of reach as
// well — and for a cascade file that is the difference between O2's switch being
// read and the file being refused unproven. The usage string is what `fugaro
// validate --help` prints and what the generated CLI reference's row repeats
// (website/content/cli-reference.md, rendered by internal/clidoc/cliref and written
// by `go run ./ee/clidoc`), so both are read here: a
// reader who takes the old sentence for the whole flag reads --offline as a way
// to skip the bucket and still get a verdict, not as the state that refuses
// their cascade workflow.
//
// Mutation (run, restored): restore the old usage string — the flag's own arm
// and the reference's row both fail, the second naming the row it read.
func TestValidateOfflineHelpNamesThePolicyRead(t *testing.T) {
	fl := newValidateCmd().Flags().Lookup("offline")
	if fl == nil {
		t.Fatal("validate carries no --offline flag: this scan no longer reads it")
	}
	if !strings.Contains(fl.Usage, "policy") {
		t.Errorf("validate's --offline help does not name the published policy the flag also keeps out of reach: %q", fl.Usage)
	}

	ref := readDoc(t, "website/content/cli-reference.md")
	section := between(ref, "## `fugaro validate`", "\n## `")
	if section == ref || strings.TrimSpace(section) == "" {
		t.Fatal("website/content/cli-reference.md carries no `fugaro validate` section: the reference moved and this scan no longer reads it")
	}
	row := between(section, "| `--offline` |", "\n")
	if row == section {
		t.Fatal("the reference's validate section carries no `--offline` row: the row moved and this scan no longer reads it")
	}
	if !strings.Contains(row, "policy") {
		t.Errorf("the generated reference's --offline row does not name the published policy (regenerate with `go run ./ee/clidoc` after the flag's help changes): %q", row)
	}
}

// TestValidateRefusesCascadeWithBudgetOff is the gateway rule at the surface
// that reports it: a cascade run's calls are made by the gateway, so the mode
// that reaches the run — the effective one, policy.Merge of the installation's
// ceiling and the file — has to be observe or enforce. Here the ceiling is off
// and the file says off too, which is the state the refusal names.
//
// Mutation (run, restored): judge runner.FileLayer(cfg).Mode alone, without
// the ceiling — the last part of this test fails, refusing a cascade file
// whose installation observes, which is the file's own mode read as the
// run's.
func TestValidateRefusesCascadeWithBudgetOff(t *testing.T) {
	f := newLaunchBudget(t, "off")
	// The allow-list is published with the switch: the node's mode is off, so
	// the refusal this test is about is the gateway's, and a policy that
	// publishes no allow-list would add this file's own refusal to the report.
	publishPolicy(t, f, cascadePolicyAllowList("off"))
	path := writeConfig(t, strings.Replace(cascadeCLI, "workflows:", "budget: { mode: off }\nworkflows:", 1))
	out, _, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
	}
	for _, want := range []string{"agent.auth", "budget.mode", "off"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "routing.claude: {via: cascade}") {
		t.Errorf("the gateway refusal also claimed the policy does not enable the cascade, which it does:\n%s", out)
	}

	// The mode judged is the run's effective one, not the file's: a file that
	// writes off under a ceiling that observes still runs with the gateway on
	// (policy.Merge keeps the stricter of the two), so its cascade workflow is
	// valid. A check reading the file's own mode would refuse it.
	observing := newLaunchBudget(t, "observe")
	publishPolicy(t, observing, cascadePolicyAllowList("observe"))
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("a cascade file whose installation observes was refused: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
}

// TestValidateCascadeSwitchReasonNeverBlamesTheGateway: a cascade run's
// gateway is the credentials service's, not a model API's credential, and the
// sentence validate prints for a cascade run's gateway must not be the
// model-API one: "the gateway needs a model API's credential" is false of a
// cascade run, whose whole point is a gateway carrying a credential that is
// not the job's own (D133, D143).
//
// C1.19 taught runner.GatewayOn the value, so the state this is asked in is
// the other one now: with an effective mode that is off, a cascade run has no
// gateway at all (the gateway is the budget's to start, D11), the switch
// reaches no run of the configuration, and the file is refused for the mode
// beside the note — which is what the two lines below are about. With the mode
// on, the same file reaches the switch instead, and what validate says about a
// switch that names a model this checkout carries no route for is the halt
// TestValidateRefusesCascadeWithBudgetOff's sibling cases cover.
//
// The state is the one the switch's reach is asked about: the project config's
// switch names a Claude model this checkout carries no route for, so the pin
// has no fall-back (D1b) and the line says what the run does with it.
//
// Mutation (run, restored): drop the cascade arm from routingInertWhy's
// RoutingInertNoGateway case — the line then blames the gateway's credential
// rule and this test fails on that sentence.
func TestValidateCascadeSwitchReasonNeverBlamesTheGateway(t *testing.T) {
	f := newLaunchBudget(t, "off")
	// With the allow-list, so that the report this test reads is the gateway's
	// refusal and the note beside it, and not the allow-list's refusal too.
	publishPolicy(t, f, cascadePolicyAllowList("off"))
	// The switch is the operator's own file, and it names a model only a route
	// this repository may not send code to claims (routedProjectBlock admits
	// acme/sandbox; the checkout is acme/other).
	f.appendConfig(t, routedProjectBlock+routedSwitchBlock)
	inCheckoutOf(t, "acme/other")
	file := strings.Replace(cascadeCLI, "workflows:", "budget: { mode: off }\nworkflows:", 1)
	out, errOut, err := execute(t, "validate", writeConfig(t, file))
	// The gateway rule refuses the file: a cascade run with the budget off has
	// no gateway, and a cascade run has no credential of its own to fall back
	// to (C1.19's launch carries the same rule; config.CheckCascadeGateway).
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
	}
	if !strings.Contains(errOut, "routing.claude") {
		t.Fatalf("want the note about the switch's reach:\n%s%s", out, errOut)
	}
	if strings.Contains(out+errOut, "the gateway needs a model API's credential") {
		t.Fatalf("the reason blames a gateway rule that is false of a cascade run:\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "credentials service") {
		t.Fatalf("the reason does not name the gateway a cascade run's calls are carried by:\n%s%s", out, errOut)
	}
	if strings.Contains(out, "the project's routing switch is not usable") {
		t.Fatalf("the halt is asserted for a run that does not reach the switch:\n%s", out)
	}
}

// shiftRelease returns rel with a release number moved by delta: a release
// below cascadeSince is the image the gate has to refuse, and one above it the
// image it has to pass. Written as a shift of the constant rather than as a
// literal so the boundary this test pins is the constant's own: a row written
// as "0.9.5" beside a constant of "0.9.6" is the same number typed twice, and
// it stays green when the constant moves — which is the skew the test exists
// to catch.
//
// The patch is the component that moves whenever it can, so a constant with a
// nonzero patch has both neighbours in its own patch series (0.9.6's are 0.9.5
// and 0.9.7). A constant that opens its minor — a patch of 0, as 0.10.0 does —
// has no patch below it, so the lower neighbour moves the minor and keeps the
// patch (0.9.0) while the one above it still moves the patch (0.10.1): the
// patch is tried first, and only the direction that would go below zero falls
// through to the minor. Both are releases the gate's own numeric compare puts
// on the right side of the constant, which is all this fixture has to be; a
// constant whose patch cannot move down and whose minor cannot either is not a
// release this gate can judge, and that is what the fatal says.
func shiftRelease(t *testing.T, rel string, delta int) string {
	t.Helper()
	parts := strings.Split(rel, ".")
	if len(parts) != 3 {
		t.Fatalf("cascadeSince = %q is not a X.Y.Z release, so no boundary can be read from it", rel)
	}
	major, minor, patch := 0, 0, 0
	var err error
	if major, err = strconv.Atoi(parts[0]); err != nil {
		t.Fatalf("cascadeSince = %q is not a X.Y.Z release, so no boundary can be read from it", rel)
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		t.Fatalf("cascadeSince = %q is not a X.Y.Z release, so no boundary can be read from it", rel)
	}
	if patch, err = strconv.Atoi(parts[2]); err != nil {
		t.Fatalf("cascadeSince = %q is not a X.Y.Z release, so no boundary can be read from it", rel)
	}
	switch {
	case patch+delta >= 0:
		return fmt.Sprintf("%d.%d.%d", major, minor, patch+delta)
	case minor+delta >= 0:
		return fmt.Sprintf("%d.%d.%d", major, minor+delta, patch)
	}
	t.Fatalf("cascadeSince = %q has no release %d below/above it", rel, delta)
	return ""
}

// TestShiftReleaseMovesThePatchFirst pins the fixture's own precedence rule,
// because the gate tests' boundary rows rest on it and it is easy to misread
// from the comment alone: the patch is tried first, so 0.10.0's neighbour above
// is 0.10.1, and only a direction with no patch left falls through to the minor
// (0.9.0 below). A constant with a nonzero patch moves no minor at all. The
// comment above shiftRelease said the opposite — 0.11.0 above — until this
// round's review, which is the drift this test exists to stop.
//
// Mutation (run, restored): swap the switch's two cases (minor first) and the
// 0.10.0/+1 row becomes 0.11.0 and this fails.
func TestShiftReleaseMovesThePatchFirst(t *testing.T) {
	for _, c := range []struct {
		rel   string
		delta int
		want  string
	}{
		{"0.10.0", 1, "0.10.1"},
		{"0.10.0", -1, "0.9.0"},
		{"0.9.6", 1, "0.9.7"},
		{"0.9.6", -1, "0.9.5"},
	} {
		if got := shiftRelease(t, c.rel, c.delta); got != c.want {
			t.Errorf("shiftRelease(%q, %d) = %q, want %q", c.rel, c.delta, got, c.want)
		}
	}
}

// TestInitRepoRefusesCascadeOnOlderBaseImage is Task C1.1's skew gate: a
// fugaro.yaml value is a version-skew break in a strictly decoded config, so
// `fugaro update --repo` refuses to deploy a cascade repository against an
// installation whose base image predates the release that reads the value —
// before any cloud effect. cascadeSince is the release that ships it: an image
// at it (or past it) passes, a ref that is not a release image is only warned
// about (nothing can tell what its runner knows), and an installation with no
// base image at all passes, since its job is built from this release's own
// image.
//
// The boundary is the constant's own (shiftRelease): the image refused is one
// release below cascadeSince and the images that pass are cascadeSince itself
// and one release above it, so a constant moved to a release no base image
// could be — or one that no longer sits where the number in docs/release.md
// and the schemas says — is what this test's rows are written to catch rather
// than to follow.
//
// Mutation (run, restored): move cascadeSince below the base image's release
// (0.9.4) and both this test's refusal and the repoEngine one fail — the gate
// passes an image whose runner has never heard of the value; refuse the empty
// base image and the "" row fails; drop the warning in the `!ok` case and the
// dev-ref row fails, with an installation whose base image cannot be judged
// passing in silence.
func TestInitRepoRefusesCascadeOnOlderBaseImage(t *testing.T) {
	cascade := &config.Config{Agent: config.Agent{Auth: config.AuthCascade}}
	below, above := shiftRelease(t, cascadeSince, -1), shiftRelease(t, cascadeSince, 1)
	lc := func(ref string) *localcfg.Config {
		if ref == "" {
			return &localcfg.Config{}
		}
		return &localcfg.Config{BaseImages: map[string]string{"base": ref}}
	}
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := &initRun{o: &initOptions{}, cmd: cmd, w: &out}

	older := "ghcr.io/dimipaun/fugaro-base:" + below
	err := r.gateCascadeAuth(r.o, "acme/app", lc(older), cascade, config.SourceRepo)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want refused", err)
	}
	for _, want := range []string{
		"fugaro " + cascadeSince + " or later",
		older + ", release " + below,
		"fugaro update --base base",
		"agent.auth: cascade",
		// The subject names where this row's value came from (SourceRepo):
		// the same gate under a layer's default says so instead (see
		// TestCascadeSkewNamesTheLayerTheValueCameFrom).
		"fugaro.yaml's agent.auth: cascade",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
	}

	// A base image at cascadeSince or past it passes, and one that is not a
	// release image only warns.
	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + cascadeSince, "ghcr.io/dimipaun/fugaro-base:" + above} {
		var warn bytes.Buffer
		if err := refuseCascadeSkew("acme/app", config.AuthCascade, config.SourceRepo, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%s: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	for _, ref := range []string{"", "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c"} {
		var warn bytes.Buffer
		if err := refuseCascadeSkew("acme/app", config.AuthCascade, config.SourceRepo, ref, &warn); err != nil {
			t.Errorf("%q: err = %v, want none", ref, err)
		}
		if ref != "" && (!strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked")) {
			t.Errorf("%q: warn = %q", ref, warn.String())
		}
	}

	// Another auth asks nothing of the image: the gate is about the value the
	// image has to read, and a vertex file is read by every release.
	for _, auth := range []string{"vertex", "api-key", "oauth"} {
		var warn bytes.Buffer
		err := refuseCascadeSkew("acme/app", auth, config.SourceRepo, older, &warn)
		if err != nil || warn.Len() != 0 {
			t.Errorf("auth %q: err = %v, warn = %q, want neither", auth, err, warn.String())
		}
	}

	// --forget tears the repository down and --plan-only shows the plan
	// without applying it: neither deploys the value, so neither is refused
	// (the same escape the route-switch gate leaves). --print-vars is judged
	// like a deploy would be — the values it prints are the ones a deploy
	// would use — and nothing of it lands on stdout.
	old := lc(older)
	if err := r.gateCascadeAuth(r.o, "acme/app", old, cascade, config.SourceRepo); err == nil {
		t.Fatal("a plain deploy was not refused")
	}
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateCascadeAuth(o, "acme/app", old, cascade, config.SourceRepo); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateCascadeAuth(pv, "acme/app", old, cascade, config.SourceRepo); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestCascadeSkewNamesTheLayerTheValueCameFrom: the subject of both messages
// names the thing that put the cascade in effect, and a resolved value is not
// proof that the repository's own file asked for it. agent.auth resolves like
// every other key — the repository's value, else the workflow's profile's,
// else the project layer's defaults (config.Resolve) — so an installation
// whose layer defaults agent.auth to cascade runs every repository it covers
// through the cascade with no fugaro.yaml saying so, and a subject that
// always said "fugaro.yaml's" would send that operator to a line their file
// does not have (the misattribution annotate() avoids on a problem, and
// doctor's oauth-token-allowed check avoids in its message).
//
// src is config.Resolution.SourceOf("agent.auth"), which is what repoEngine
// passes; a source this cannot name is named rather than guessed at, so a
// value that reaches the config some new way is still not attributed to a
// file. The pair of messages is checked through refuseCascadeSkew, not just
// cascadeSubject: the warning is the other place the subject is printed.
//
// Mutation (run, restored): make cascadeSubject ignore src and always return
// the fugaro.yaml wording — the layer rows fail, and the refusal tells the
// operator of a layered installation to edit a line their file does not
// have. Mutation (pass config.SourceRepo where repoEngine has
// rf.Res.SourceOf("agent.auth")): TestRepoEngineNamesTheProjectLayerThatAsksForTheCascade
// fails, which is the call site this test cannot see.
func TestCascadeSkewNamesTheLayerTheValueCameFrom(t *testing.T) {
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, cascadeSince, -1)
	for _, c := range []struct{ src, subject string }{
		{config.SourceRepo, "fugaro.yaml's agent.auth: " + config.AuthCascade + " of acme/app"},
		{config.SourceProject, "the project layer's defaults.agent.auth: " + config.AuthCascade + " of acme/app"},
	} {
		if got := cascadeSubject("acme/app", c.src); got != c.subject {
			t.Errorf("cascadeSubject(%q) = %q, want %q", c.src, got, c.subject)
		}
		err := refuseCascadeSkew("acme/app", config.AuthCascade, c.src, older, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), c.subject) {
			t.Errorf("src %q: the refusal does not name %q: %v", c.src, c.subject, err)
		}
		var warn bytes.Buffer
		if err := refuseCascadeSkew("acme/app", config.AuthCascade, c.src, "not-a-release-ref", &warn); err != nil || !strings.Contains(warn.String(), c.subject) {
			t.Errorf("src %q: the warning does not name %q (err = %v, warn = %q)", c.src, c.subject, err, warn.String())
		}
	}
	// The value the layer set is never blamed on the repository's file, and a
	// source this cannot name says so instead of picking one.
	for _, src := range []string{config.SourceProject, config.SourceProfile("shared"), config.SourceDefault} {
		got := cascadeSubject("acme/app", src)
		if strings.Contains(got, "fugaro.yaml") {
			t.Errorf("cascadeSubject(%q) = %q: a value this source set is attributed to the repository's own file", src, got)
		}
	}
	if got := cascadeSubject("acme/app", config.SourceProfile("shared")); !strings.Contains(got, "shared") {
		t.Errorf("cascadeSubject(SourceProfile) = %q: an unnamed source is not named at all", got)
	}
}

// TestCascadeSinceIsTheReleaseTheDocsAndSchemasName: the constant's number is
// stated to three other readers, and each of them acts on it. docs/release.md's
// "Before you tag" bullet is what a release verifies the constant against
// ("currently `\"0.10.0\"`"), and the two schemas' agent.auth descriptions tell
// an author editing a fugaro.yaml which release stops refusing the value
// ("Fugaro before 0.10.0 refuses the value"). A constant that moves alone leaves
// one of those readers a number that is not the one the gate judges with — the
// release checklist verifying a release that does not read the value, or a
// repository author told their file is fine on an image whose runner refuses it
// — and nothing else in this package reads any of the three.
//
// The boundary the gate itself judges is pinned in the test above, off the
// constant; this is the other half: the number a person reads is the number the
// gate uses, so moving the constant to a release that no base image can be, or
// to one the docs do not name, fails here rather than silently refusing every
// cascade install.
//
// Mutation (run, restored): change cascadeSince to "0.10.1" and nothing else —
// this fails naming the release checklist and both schemas, which still say
// 0.10.0.
func TestCascadeSinceIsTheReleaseTheDocsAndSchemasName(t *testing.T) {
	// The bullet itself, by its own opening, and not the first line that
	// mentions the constant: the managedDeployerShipped bullet above it cites
	// this constant's comment for the 2026-10-20 renumbering, and a
	// cross-reference is not the bullet a release verifies the constant
	// against. Reading by mention made the checklist checkable only while
	// nothing else in the file named the constant.
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `cascadeSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no cascadeSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+cascadeSince+"\"`") {
		t.Errorf("docs/release.md's cascadeSince bullet does not carry the constant's release (%q): the checklist would verify a release the gate does not judge with", cascadeSince)
	}
	// The schemas' own line, not the file: the enum line is the one that
	// carries the description an editor of fugaro.yaml or a project layer reads.
	for _, path := range []string{"../../schemas/fugaro.schema.json", "../../schemas/project-layer.schema.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var line string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, `"enum"`) && strings.Contains(l, `"cascade"`) {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("%s: no agent.auth enum line names cascade", path)
		}
		if !strings.Contains(line, "before "+cascadeSince) {
			t.Errorf("%s's agent.auth description does not say Fugaro before %s refuses the value: the schema tells an author a release the gate does not judge with", path, cascadeSince)
		}
	}
}

// TestCascadeSinceIsTheReleaseTheCredentialsServiceShipsIn: the four readers
// above are held to the constant, and this one holds the constant to the
// release the credentials service ships in — 0.10.0, which is not tagged yet
// and is where the owner's ruling of 2026-10-20 places the whole service (this
// value included; the line named 0.9.6 while the next release was expected to
// be 0.9.6, and the owner renumbered it to 0.10.0).
//
// It is written out rather than read from anything because nothing else in the
// tree knows which release this is: the constant, docs/release.md's bullet and
// both schemas' descriptions can move together to a number that is not the
// ruled one and stay green in TestCascadeSinceIsTheReleaseTheDocsAndSchemasName,
// which compares them with each other. What a wrong number costs is the gate
// itself: a cascade repository on an installation whose base image is 0.9.5 or
// older would be refused by a constant naming a release that ships later than
// the one that actually reads the value, and admitted by one naming an earlier
// release, in both cases against the release the owner ruled for.
//
// Mutation (run, restored): change cascadeSince to "0.11.0" — this fails, and
// the docs/schemas test above stays green with the labels moved to match.
func TestCascadeSinceIsTheReleaseTheCredentialsServiceShipsIn(t *testing.T) {
	if cascadeSince != "0.10.0" {
		t.Errorf("cascadeSince = %q: the credentials service ships in 0.10.0 (the owner's ruling of 2026-10-20), so the gate would judge the base image against a release the value does not ship in", cascadeSince)
	}
}

// TestRepoEngineRefusesCascadeOnAnOlderBaseImage is the gate at its call site:
// the checks above test gateCascadeAuth in isolation, and this one pins that
// repoEngine reaches it at all — before the plan, the apply or any other cloud
// call — from a checkout that asks for the cascade against an installation
// whose base image predates cascadeSince. A gate nothing calls is a comment.
//
// Mutation (run, restored): drop the gateCascadeAuth call from repoEngine —
// this test then fails on the refusal's own words, and what it is left
// holding is the state bucket the run never reaches (the check above would
// not have noticed: it calls the gate itself).
func TestRepoEngineRefusesCascadeOnAnOlderBaseImage(t *testing.T) {
	below := shiftRelease(t, cascadeSince, -1)
	r := newInitRig(t)
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:"+below+"}\n")
	r.save(t)
	isolateCache(t)
	noProjectLayerBucket(t)
	yaml := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n" +
		"agent: { auth: cascade }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", yaml)
	t.Chdir(dir)
	e := rigEngine(t, r, &initOptions{})
	err := e.r.repoEngine(t.Context(), ".", "", true)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit = %d, err = %v: want the cascade refused on this installation's base image", ExitCode(err), err)
	}
	for _, want := range []string{cascadeSince, below, "fugaro update --base base", "agent.auth: cascade", "fugaro.yaml's agent.auth: cascade"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if applies := r.ran(t, "apply"); len(applies) != 0 {
		t.Errorf("the refusal applied anyway: %v", applies)
	}
}

// TestRepoEngineNamesTheProjectLayerThatAsksForTheCascade is the subject's
// call site: the repository's fugaro.yaml here sets no agent.auth at all, and
// the project layer it resolves over defaults it to cascade, so the cascade
// is in effect without the file asking for it. repoEngine has to hand the
// gate the source of the value it read (rf.Res.SourceOf), not the fact that
// the value exists: a gate told "the file" would send this operator to edit a
// line their fugaro.yaml does not have, when the fix is in the layer or in
// an agent.auth line they would add to their own file.
//
// The layer is the one findLayer reads (the local config's own bucket_url,
// written as a file:// bucket with config.LayerKey in it), the same seam
// doctor's layer tests use, so the resolution really runs over it.
//
// Mutation (run, restored): pass config.SourceRepo instead of
// rf.Res.SourceOf("agent.auth") in repoEngine — this test fails on the
// refusal naming fugaro.yaml for a value the file does not carry, and
// cascadeSubject's own test cannot see it (it calls the function directly).
func TestRepoEngineNamesTheProjectLayerThatAsksForTheCascade(t *testing.T) {
	below := shiftRelease(t, cascadeSince, -1)
	r := newInitRig(t)
	isolateCache(t)
	runs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	layer := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  agent: { auth: cascade }\n"
	if err := os.WriteFile(filepath.Join(runs, config.LayerKey), []byte(layer), 0o644); err != nil {
		t.Fatal(err)
	}
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:"+below+"}\nbucket_url: file://"+runs+"\n")
	r.save(t)
	yaml := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", yaml)
	t.Chdir(dir)
	e := rigEngine(t, r, &initOptions{})
	err := e.r.repoEngine(t.Context(), ".", "", true)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit = %d, err = %v: want the layer's cascade refused on this installation's base image", ExitCode(err), err)
	}
	got := err.Error()
	for _, want := range []string{cascadeSince, "fugaro update --base base", "agent.auth: cascade", "the project layer's defaults.agent.auth: cascade"} {
		if !strings.Contains(got, want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if strings.Contains(got, "fugaro.yaml's agent.auth") {
		t.Errorf("the refusal blames the repository's file for a value the layer set: %v", err)
	}
	if applies := r.ran(t, "apply"); len(applies) != 0 {
		t.Errorf("the refusal applied anyway: %v", applies)
	}
}

// cascadeLaunchYAML is cascadeCLI shaped for a launch fixture: the workflow is
// the one the cloud fixture's repository admits (its config lists `web`), so a
// launch resolves a spec and reaches the gate this file is about rather than
// refusing the workflow first.
const cascadeLaunchYAML = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: cascade
  model: claude-sonnet-5-5
workflows:
  web: { commands: { build: sh build.sh, test: sh test.sh } }
`

// TestCascadeRefusedWithoutService (C1.19): a repository that asks for the
// cascade on an installation whose config names no database is refused at
// launch. Neither the policy switch that turns the cascade on nor the
// credentials rules marker lives anywhere this launcher can read, and the job
// of such an installation mounts no model credential at all (C1.20), so the
// run would have nothing to fall back to (D143). The mutation: reading the
// database only where a policy was found, which launches the run on an
// installation that has no service at all.
func TestCascadeRefusedWithoutService(t *testing.T) {
	f := newCloudFixture(t)
	projectJobCheckoutYAML(t, cascadeLaunchYAML)
	const run = "20261022-090000-ca5c"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a cascade launch without a service: err = %v, want a refusal", err)
	}
	for _, want := range []string{"cascade", "credentials service", "--provision-credentials"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none: nothing of a refused cascade launch may start", n)
	}
}

// TestCascadeRefusedWithoutCredsRulesMarker (C1.19): the installation has a
// database and its mode runs the gateway, but the deployed rules predate the
// credentials family (C1.3's marker, C1.17's named halt). The run is refused
// here, before anything is written, rather than in the job where the first
// credential it records is denied.
//
// The policy is published with the switch and the allow-list
// (cascadePolicyAllowList), so neither the O2 refusal nor the allow-list
// refusal is the one that answers first: what this test is about is the state
// the operator's own deploy leaves behind — the switch published, the
// installation's rules still predating the credentials family.
//
// Hand mutations this fails on: dropping the marker read (the launch goes on
// and the job halts on HaltCredsRulesDenied); reading the marker as "found is
// enough" (a generation below this Fugaro's passes, and the deployed document
// is not the one this binary reads); refusing on the marker for every auth
// (every api-key launch of the installation stops); judging both directions
// with one `gen != rules.CredsRulesVersion` check, which is what the two
// generation rows below were added for: a marker above this Fugaro's is then
// reported as an older deploy and its owner is sent at the init that refuses
// it.
func TestCascadeRefusedWithoutCredsRulesMarker(t *testing.T) {
	f := newProjectJobFixtureMode(t, "enforce")
	publishPolicy(t, f.launchBudgetFixture, cascadePolicyAllowList("enforce"))
	projectJobCheckoutYAML(t, cascadeLaunchYAML)
	const run = "20261022-090000-ca5c"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a cascade launch without the rules marker: err = %v, want a refusal", err)
	}
	for _, want := range []string{"credsRules", "update --firebase-project", "halt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
	if tok := readToken(t, f.cloudFixture, run); tok != "" {
		t.Error("a refused cascade launch minted a budget token")
	}
	// The same installation with the marker at this Fugaro's generation is not
	// refused for the marker: the refusal above is the marker's, and the same
	// gate must let the launch past it once the deploy is current. What is
	// asserted is the gate's own answer, since the rest of a launch needs a
	// whole fixture (a build record, an image) that has nothing to do with it.
	f.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
	if err != nil {
		t.Fatalf("opening the fixture's cloud environment: %v", err)
	}
	if err := cascadeLaunchRefusal(context.Background(), env, &task.Spec{Repo: "acme/app"}); err != nil {
		t.Errorf("a cascade launch on an installation whose marker is current: %v", err)
	}

	// The two ways the generation can miss this binary's are two problems with
	// two fixes: a marker below it is an old deploy of this Fugaro's own rules,
	// and the launch sends its owner back to init from this binary; a marker
	// above it is a *newer* Fugaro's document, which this release refuses to
	// deploy over (internal/infra's DB.Plan) and whose owner has to upgrade
	// first. The direction is the whole point: the old single check told the
	// owner of a newer marker to re-run init from this Fugaro, which is the one
	// command that refuses them.
	for _, c := range []struct {
		what    string
		gen     int
		wants   []string
		refuses []string
	}{
		{
			what:  "below this Fugaro's",
			gen:   rules.CredsRulesVersion - 1,
			wants: []string{"update --firebase-project", "in your own terminal from this Fugaro", "below"},
		},
		{
			what:    "above this Fugaro's",
			gen:     rules.CredsRulesVersion + 1,
			wants:   []string{"brew upgrade dimipaun/tap/fugaro", "newer binary", "above"},
			refuses: []string{"from this Fugaro", "fugaro upgrade"},
		},
	} {
		f.db.Set(rules.PathCredsRulesMarker(), c.gen)
		env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
		if err != nil {
			t.Fatalf("opening the fixture's cloud environment: %v", err)
		}
		err = cascadeLaunchRefusal(context.Background(), env, &task.Spec{Repo: "acme/app"})
		if err == nil {
			t.Fatalf("a cascade launch on an installation whose marker is %s was admitted", c.what)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("a marker %s: err = %v, want a user refusal", c.what, err)
		}
		for _, want := range c.wants {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal for a marker %s does not say %q: %v", c.what, want, err)
			}
		}
		for _, refuse := range c.refuses {
			if strings.Contains(err.Error(), refuse) {
				t.Errorf("the refusal for a marker %s says %q, which is the other direction's remedy: %v", c.what, refuse, err)
			}
		}
	}
}

// TestCascadeLaunchRefusesWithoutPolicySwitch is O2 at the launch (C1.19): the
// plan puts the switch's refusal on the launch's own side, fugaro validate's
// cascadeProblems is the author's half of it, and validate.go says the launch
// "refuses the same pair again on its own side" — so it has to be refused
// before the claim is taken, before the budget token is minted and before an
// execution starts, not left to the runner's bootstrap to refuse a run already
// paid for.
//
// The read fails closed in every direction: no policy published at all, a
// policy with no routing block, or a written switch whose via is another value
// (`direct` names the direct path and `auto` keeps today's routing meaning,
// O3) are each a cascade workflow no run is admitted with. The fixture's own
// policy (projectJobPolicy) has no routing block, which is the "published and
// not switched on" column; via: direct is the one a reader that only asks
// "is a via written here?" reads as the cascade's own.
//
// Mutation (run, restored): drop the O2 switch from cascadeLaunchRefusal and
// every subtest below launches (executions, a stored task and a minted token);
// accept any written via (`p.Routing.Claude != nil`) and the via: direct case
// fails.
func TestCascadeLaunchRefusesWithoutPolicySwitch(t *testing.T) {
	for _, c := range []struct {
		what   string
		policy string
	}{
		{what: "no switch in the policy", policy: projectJobPolicy},
		{what: "the switch written with another via", policy: cascadePolicyViaDirect()},
	} {
		t.Run(c.what, func(t *testing.T) {
			f := newProjectJobFixtureMode(t, "enforce")
			publishPolicy(t, f.launchBudgetFixture, c.policy)
			projectJobCheckoutYAML(t, cascadeLaunchYAML)
			const run = "20261022-090000-ca5c"
			_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
			if ExitCode(err) != ExitUserError {
				t.Fatalf("a cascade launch under %s: err = %v, want a refusal", c.what, err)
			}
			for _, want := range []string{"cascade", "routing.claude: {via: cascade}", "project policy publish", "only the operator"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Errorf("%d executions, want none: nothing of a refused cascade launch may start", n)
			}
			if storedTask(t, f.cloudFixture) {
				t.Error("a refused cascade launch stored a task")
			}
			if tok := readToken(t, f.cloudFixture, run); tok != "" {
				t.Error("a refused cascade launch minted a budget token")
			}
		})
	}

	// And the third direction, without a launch: a project that has published
	// nothing at all. The launch's gate is called directly, because the rest of
	// a launch needs a build record and a job this state has nothing to do
	// with; what is asserted is the gate's own answer.
	newLaunchBudget(t, "observe")
	projectJobCheckoutYAML(t, cascadeLaunchYAML)
	env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
	if err != nil {
		t.Fatalf("opening the fixture's cloud environment: %v", err)
	}
	err = cascadeLaunchRefusal(context.Background(), env, &task.Spec{Repo: "acme/app"})
	if err == nil {
		t.Fatal("a cascade launch on an installation that has published no policy was admitted")
	}
	for _, want := range []string{"has published no policy", projectpolicy.PathShared(), "routing.claude: {via: cascade}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestCascadeLaunchRefusedWhenTheFileTurnsTheGatewayOff and its sibling below
// are the launch's gateway rule (config.CheckCascadeGateway, C1.19): a cascade
// run has no credential of its own, so the gateway must be on — the mode has to
// be observe or enforce — and a launch that would produce a run with nothing to
// carry its calls is refused here, before the claim is taken, before a task is
// stored and before a budget token is minted, rather than left to the job's
// first call to fail.
//
// The two tests are the two layers the run's mode can come from, because the
// launch's merge is `policy.Merge(ceiling, file)` with the *published policy
// node's* mode put on the ceiling first (runner.policyCeiling: the node
// replaces the ceiling's mode rather than tightening it), and each layer is a
// different code path in cascadeLaunchRefusal: `runner.FileLayer(co)` for the
// file, the `p.Budget.Mode` override for the node.
//
// What each layer can *do* is not the same, and that decides how each test has
// to be built. A file layer can only tighten, so a file that writes off under a
// ceiling that observes is ignored, not obeyed (policy.Merge's Ignored — the
// runner uses the ceiling's mode): the file's own off is the run's only where
// the ceiling names no mode at all, which is the state the first test sets up.
// The node's mode is the ceiling's, replaced key by key, so a node that says
// off under a ceiling that enforces really is off — the second test, and the
// one that fails if the override is dropped.
//
// The refusal names the way out in the mode's own words: CheckCascadeGateway's
// problem ("cascade needs the budget gateway: budget.mode is off … Set
// budget.mode to observe or enforce — in the project config, or in fugaro.yaml")
// and this gate's sentence about the credentials service.
func TestCascadeLaunchRefusedWhenTheFileTurnsTheGatewayOff(t *testing.T) {
	f := newProjectJobFixtureMode(t, "observe")
	publishPolicy(t, f.launchBudgetFixture, cascadePolicy())
	// The ceiling names no mode, so the file's own is the run's: a repository
	// that asks for the cascade and writes its budget off.
	withoutBudgetMode(t, f.cloudFixture)
	projectJobCheckoutYAML(t, strings.Replace(cascadeLaunchYAML, "workflows:", "budget: { mode: off }\nworkflows:", 1))
	requireCascadeGatewayRefusal(t, f, "the file's own budget.mode: off")
}

// withoutBudgetMode takes the mode out of the fixture's project config: the
// ceiling then names none (localcfg's own off is the absence of a mode, not a
// ceiling that says nothing may be accounted), and the file's mode is the one
// the run gets.
func withoutBudgetMode(t *testing.T, f *cloudFixture) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const withMode = "budget: { mode: observe,"
	if !strings.Contains(string(data), withMode) {
		t.Fatalf("the fixture's project config no longer carries %q:\n%s", withMode, data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), withMode, "budget: {", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCascadeLaunchRefusedWhenThePolicyTurnsTheGatewayOff is the same rule over
// the other layer, and the reason the node's mode is the *ceiling's* rather than
// a third layer: `policy.Merge` between two modes takes the tighter, so a node
// that says off under a ceiling that enforces would be read as enforcing — the
// run would launch with the gateway the policy had just turned off. The launch
// reads the node as the ceiling's own mode (runner.policyCeiling), so off is
// off.
func TestCascadeLaunchRefusedWhenThePolicyTurnsTheGatewayOff(t *testing.T) {
	f := newProjectJobFixtureMode(t, "enforce")
	publishPolicy(t, f.launchBudgetFixture, cascadePolicyWithMode("off"))
	projectJobCheckoutYAML(t, cascadeLaunchYAML)
	requireCascadeGatewayRefusal(t, f, "the published policy's budget.mode: off")
}

// TestCascadeLaunchJudgesTheRefsModeNotTheWorkingTrees (C1.19): the gateway
// rule judges the run's document, not the launcher's. The run clones spec.Ref
// and merges *that* file, so a working tree left on another branch — or
// carrying an uncommitted edit — is a document the run never reads, and a mode
// read from it is a mode no run has. Here the working tree writes
// budget.mode: observe while origin/main, the ref this launch names, says off:
// judged from the working tree the launch is admitted, takes the claim, mints a
// budget token and starts an execution whose run refuses in agent.BuildEnv on
// its first call — the outcome this gate exists to prevent; judged from the ref
// it is refused here, before anything is written.
//
// Mutation (run, restored): read the working tree again
// (runner.FileLayer(checkoutConfig(...))) and this launch goes through.
func TestCascadeLaunchJudgesTheRefsModeNotTheWorkingTrees(t *testing.T) {
	f := newProjectJobFixtureMode(t, "observe")
	publishPolicy(t, f.launchBudgetFixture, cascadePolicy())
	// The ceiling names no mode, so the merge is decided between the two
	// documents' budget.mode alone (the policy node carries none either).
	withoutBudgetMode(t, f.cloudFixture)
	projectJobCheckoutYAMLAt(t,
		strings.Replace(cascadeLaunchYAML, "workflows:", "budget: { mode: observe }\nworkflows:", 1),
		strings.Replace(cascadeLaunchYAML, "workflows:", "budget: { mode: off }\nworkflows:", 1))
	requireCascadeGatewayRefusal(t, f, "the ref's budget.mode: off under a working tree that says observe")
}

// TestCascadeLaunchMergesTheDefaultBranchesPolicy (C1.19): the launch judges
// the same three layers the run merges (runner's resolvePolicy: the ceiling, the
// default branch's policy and the ref's own file), because a layer can only
// tighten and a mode raised by the default branch is a mode the run has. Here
// the launch's ref says nothing, the ceiling names off — the published node's
// own mode, the loosest value there is — and origin/main, the file the *run*
// reads as its middle layer, says observe: the run's gateway is on, and the gate
// must not refuse it.
//
// The working tree agrees with the ref (both say nothing) so that the default
// branch is the only layer that can answer: with it, the mode is observe and the
// gate passes; without it the merge is off and CheckCascadeGateway refuses what
// the run would carry.
//
// Mutation (run, restored): drop defaultBranchPolicyLayer from the merge and
// this launch is refused ("budget.mode is off") although its run has a gateway.
func TestCascadeLaunchMergesTheDefaultBranchesPolicy(t *testing.T) {
	f := newProjectJobFixtureMode(t, "observe")
	// The node's mode is off, so the default branch's observe is the layer that
	// decides — and the allow-list is published with it, because a policy that
	// carries a budget block must name a mode (projectpolicy.validBudget) and
	// this gate refuses a cascade whose policy publishes no allow-list.
	publishPolicy(t, f.launchBudgetFixture, cascadePolicyAllowList("off"))
	withoutBudgetMode(t, f.cloudFixture)
	f.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	cascadeCheckoutAt(t, "feature",
		strings.Replace(cascadeLaunchYAML, "workflows:", "budget: { mode: observe }\nworkflows:", 1),
		cascadeLaunchYAML, cascadeLaunchYAML)
	env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
	if err != nil {
		t.Fatalf("opening the fixture's cloud environment: %v", err)
	}
	if err := cascadeLaunchRefusal(context.Background(), env, &task.Spec{Repo: "acme/app", Ref: "feature"}); err != nil {
		t.Errorf("a cascade launch whose gateway the default branch's policy turns on: %v", err)
	}
}

// TestCascadeLaunchRefusedWithoutAllowList (the owner's ruling of 2026-10-22):
// the credentials service holds every call it carries to the project's
// published budget.allowed_models and refuses a project that publishes none
// (C1.11's request check, internal/credsvc's requestNoAllowList), so a cascade
// run of such a project has every call refused and halts on the first (D143).
// The launch refuses it here, one step earlier and for the same reason the O2
// switch and the gateway rule are judged on this side: before the claim is
// taken, before a task is stored and before a budget token is minted, so the
// person at the terminal hears the fix rather than the job halting call by call.
//
// The policy is the state an installation really reaches: the switch published,
// the budget mode on, and the budget block naming a mode and no allow-list —
// projectpolicy.validBudget refuses an *empty* list, so "publishes none" is the
// key left out, which is the node that keeps the allow-list the job's
// environment was deployed with and is exactly the list the service cannot read
// (H1).
//
// What is judged is that node's list and not the run's merged one, and the
// third launch below is what says so: the same project with the allow-list in
// the *installation's* own config (the ceiling) and none in the policy is
// refused just the same, because the ceiling never reaches the service. A gate
// reading the run's merged list would admit a run whose every call the service
// refuses.
//
// Mutation (run, restored): drop the refusal and the launch below goes through
// — the gate answers nil for the same fixture, and the run is left to be refused
// call by call in the job.
func TestCascadeLaunchRefusedWithoutAllowList(t *testing.T) {
	f := newProjectJobFixtureMode(t, "enforce")
	// The node's mode is the gateway rule's (observe), so the refusal that
	// answers the cascade launch below is the allow-list's and not the mode's.
	publishPolicy(t, f.launchBudgetFixture, cascadePolicyWithMode("observe"))
	f.db.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion)
	projectJobCheckoutYAML(t, cascadeLaunchYAML)
	const run = "20261022-090000-ca5c"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a cascade launch whose policy publishes no allow-list: err = %v, want a refusal", err)
	}
	for _, want := range []string{
		"cascade",
		"budget.allowed_models",
		"add the allow-list to the project layer",
		"fugaro project policy publish",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none: a refused cascade launch may start nothing", n)
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("a refused cascade launch stored a task")
	}
	if tok := readToken(t, f.cloudFixture, run); tok != "" {
		t.Error("a refused cascade launch minted a budget token")
	}

	// The other auth, on the same installation and the same policy: an api-key
	// workflow is not held to the project's allow-list by this gate, so the
	// rule is the cascade's and not a launch's.
	projectJobCheckoutYAML(t, projectJobRepoYAML)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261022-090001-ca5d", "Add a feature"); err != nil {
		t.Errorf("an api-key launch of an installation whose policy publishes no allow-list: %v", err)
	}

	// The ceiling's list is not the node's, and it is the node's the service
	// reads: with the allow-list in the fixture's own config and none in the
	// policy, the launch is refused for the same reason.
	ceilingAllowList(t, f.cloudFixture)
	projectJobCheckoutYAML(t, cascadeLaunchYAML)
	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20261022-090002-ca5e", "Add a feature")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "budget.allowed_models") {
		t.Errorf("a cascade launch whose ceiling publishes the allow-list and whose policy does not: exit = %d, err = %v, want the allow-list refusal (the ceiling never reaches the credentials service)", ExitCode(err), err)
	}

	// The way out, and the same cascade admitted: the project publishes the
	// allow-list the ruling requires, and nothing else about the fixture
	// changes. What is asserted is the gate's own answer, since the rest of a
	// launch needs a whole fixture (a claim, an image, a build record) that has
	// nothing to do with the rule.
	publishPolicy(t, f.launchBudgetFixture, cascadePolicyAllowList("enforce"))
	env, err := openCloud(context.Background(), cloudOptions{stderr: discard})
	if err != nil {
		t.Fatalf("opening the fixture's cloud environment: %v", err)
	}
	if err := cascadeLaunchRefusal(context.Background(), env, &task.Spec{Repo: "acme/app"}); err != nil {
		t.Errorf("a cascade launch whose policy publishes the allow-list: %v", err)
	}
}

// ceilingAllowList adds the model allow-list to the fixture's *local* config —
// the ceiling a job's environment is deployed with — and leaves the published
// policy's node as it is. The two are different layers, and the credentials
// service reads the node alone (internal/credsvc's requestModels), which is what
// tells a gate that reads the node from one that reads the run's merged list.
func ceilingAllowList(t *testing.T, f *cloudFixture) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const budget = "budget: { mode: "
	if !strings.Contains(string(data), budget) {
		t.Fatalf("the fixture's project config no longer carries %q:\n%s", budget, data)
	}
	added := strings.Replace(string(data), budget, "budget: { allowed_models: [claude-sonnet-5-5], mode: ", 1)
	if err := os.WriteFile(path, []byte(added), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cascadeCheckoutAt lays down a checkout whose three documents are spelled
// apart: the default branch's fugaro.yaml (origin/main), the ref a launch names
// (origin/<ref>) and the working tree's. projectJobCheckoutYAMLAt is the same
// fixture with two of them.
func cascadeCheckoutAt(t *testing.T, ref, mainYAML, refYAML, workingYAML string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": mainYAML})
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the default branch's fugaro.yaml")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": refYAML})
	testutil.Git(t, dir, "add", "fugaro.yaml")
	testutil.Git(t, dir, "commit", "-q", "-m", "the ref's fugaro.yaml")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/"+ref, "HEAD")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": workingYAML})
	t.Chdir(dir)
}

// requireCascadeGatewayRefusal runs the launch of the fixture's cascade
// workflow and requires the gateway rule to be the refusal: the user error, the
// mode it found and the way out, with nothing of the launch written — no
// execution, no task, no token.
func requireCascadeGatewayRefusal(t *testing.T, f *projectJobFixture, what string) {
	t.Helper()
	const run = "20261022-090000-ca5c"
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", run, "Add a feature")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a cascade launch whose gateway is off (%s): err = %v, want a refusal", what, err)
	}
	for _, want := range []string{
		"needs the budget gateway", "budget.mode is off",
		"Set budget.mode to observe or enforce",
		// This gate's own sentence, which is what tells a cascade's owner why a
		// mode they may have set for other repositories stops this run.
		"credentials service", "cascade run holds no model credential",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal for %s does not say %q: %v", what, want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none: a refused cascade launch may start nothing", n)
	}
	if storedTask(t, f.cloudFixture) {
		t.Error("a refused cascade launch stored a task")
	}
	if tok := readToken(t, f.cloudFixture, run); tok != "" {
		t.Error("a refused cascade launch minted a budget token")
	}
}
