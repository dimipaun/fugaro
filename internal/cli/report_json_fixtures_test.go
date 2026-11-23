package cli

// report_json_fixtures_test.go is CL10's half of the `--json` pin: one
// hermetic run per command that carries a `--json` flag, so
// report_json_test.go can put every one of them under the environment a
// person's own terminal is — colour forced on, a terminal on the command's
// output, `--verbose` — and still find stdout one JSON document and not a byte
// of the human view (G2, D1352).
//
// A command with no fixture is listed with the reason it has none, and the
// reason names the blocker rather than the command: a stream that owns the
// terminal until it is interrupted, a read that goes to a host this package
// cannot fake, a job the fake backend cannot launch. TestJSONFixtureCoverage
// walks the real tree, so a command that gains `--json` is either given a run
// here or given a reason; nothing is silently outside the pin.
//
// Every fixture is a run the package can already make: the rigs are the ones
// the command's own tests use (newCloudFixture, newBudgetFixture, the checkout
// and workspace homes), reached through the helpers in this file, so the pin
// asks the same question of the same fixture the command's behaviour is
// written against.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/diagnoseagent"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// jsonCase is one command's place in the pin.
type jsonCase struct {
	// stdin is what the run is handed on its input, for the commands that read
	// a value rather than take one as an argument. Empty is a run with no
	// input at all.
	stdin string
	// lines marks a run whose --json is one document per line rather than one
	// document: the log reader prints an entry at a time, so its pin is per
	// line and the test reads it that way.
	lines bool
	// why is why this command has no fixture here: the one thing that keeps a
	// hermetic run from reaching its own JSON outcome. A command with a
	// fixture leaves it empty.
	why string
	// reads marks a run that changes nothing: no `--json` read and no read
	// the console spawns writes a log (G7), which TestJSONReadsWriteNoLog
	// holds this run to.
	reads bool
	// run prepares the fixture and returns the command's argv, without
	// `--json` and without `--verbose`: the test appends both, so every
	// fixture is asked the same question.
	run func(t *testing.T) []string
}

// jsonCases is the pin's list, keyed by the command's path in the tree as
// jsonCommandPaths spells it.
var jsonCases = map[string]jsonCase{
	"accounts ls": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"accounts", "ls", "--repo", "acme/app"}
		},
	},
	// apply is a write verb whose run changes nothing here — this world's
	// record owes no step — so what the fixture holds to the contract is the
	// document an owed-nothing run prints. The command's other run arm, a step
	// that fails, prints the same document (apply.go) and is driven through
	// the command by apply_test.go, where the failure is a configuration the
	// test builds rather than one this world happens to have.
	"apply": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"apply"}
		},
	},
	"artifacts ls": {
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			id := "20260927-100000-abcd"
			seedRun(t, f, id, "", "", true)
			body := []byte("hello")
			seedArtifactRun(t, f, id, []runstore.ArtifactFile{
				{Name: "sizzle.png", Bytes: int64(len(body)), SHA256: sha256Hex(body), ContentType: "image/png"},
			}, map[string][]byte{"sizzle.png": body})
			return []string{"artifacts", "ls", appSlug + "/" + id}
		},
	},
	"backends ls": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"backends", "ls"}
		},
	},
	"backlog list": {
		reads: true,
		run: func(t *testing.T) []string {
			newOrchestratorFixture(t)
			return []string{"backlog", "list"}
		},
	},
	"budget preview": {
		reads: true,
		run: func(t *testing.T) []string {
			newBudgetFixture(t, "")
			return []string{"budget", "preview", "--repo", "acme/app", "--recipe", "standard", "--runs", "6"}
		},
	},
	"budget prices": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"budget", "prices"}
		},
	},
	"budget reconcile": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"budget", "reconcile", "--from", "2026-10-12", "--to", "2026-11-16", "--repo", "acme/app"}
		},
	},
	"budget show": {
		reads: true,
		run: func(t *testing.T) []string {
			newBudgetFixture(t, "")
			return []string{"budget", "show", "--all"}
		},
	},
	"cancel": {
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			id := "20260927-100000-abcd"
			f.run.SetState(seedRun(t, f, id, "", "", true), backend.StateRunning)
			return []string{"cancel", "--now", id}
		},
	},
	"config layer": {
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			publishedLayer(t, f, testProjectLayer)
			return []string{"config", "layer"}
		},
	},
	"config list": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"config", "list"}
		},
	},
	"config show": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCheckout(t)
			return []string{"config", "show"}
		},
	},
	"craft ls": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"craft", "ls"}
		},
	},
	"creds ls": {
		reads: true,
		run: func(t *testing.T) []string {
			credsFixtureFor(t).seed(t, []byte(config.ClaudeOAuthToken+": "+tokenValue+"\n"))
			return []string{"creds", "ls"}
		},
	},
	"creds set": {
		stdin: credsValuePrefix + "-JSON\n",
		run: func(t *testing.T) []string {
			f := credsFixtureFor(t)
			// The container the installation's Terraform creates, with no
			// version yet: the state the first credential is set from.
			f.seed(t, nil)
			return []string{"creds", "set", config.ClaudeOAuthToken}
		},
	},
	"diagnose": {
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			id := "20260927-100000-abcd"
			exec := seedRun(t, f, id, "", "", true)
			writeStage(t, f, id, exec, "implement")
			return []string{"diagnose", id}
		},
	},
	"doctor": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"doctor"}
		},
	},
	"estimate": {
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			id := "20260927-100000-abcd"
			seedEstimateRun(t, f, id, estimateSeed{
				tokens:   map[string]runstore.ModelTokenCounts{"claude-sonnet-5": {Input: 1_000_000, Output: 10_000}},
				modelBy:  map[string]float64{"claude-sonnet-5": 4},
				modelUSD: 4,
			})
			return []string{"estimate", "--run", appSlug + "/" + id, "--model", "claude-opus-5-5"}
		},
	},
	"followups": {
		// The plan and not a launch: the fixture's parent is merged, so
		// --launch here would file the tasks rather than print them.
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			launchable(t, f)
			return []string{"followups", mergedParent(t, f, allDeferred(t))}
		},
	},
	"image add": {
		// The dry run is the one arm a fixture here can reach and the one the
		// console spawns (O4): the real add builds, redeploys and publishes,
		// and writes its own document under a name that is one command's. It
		// is a read — the run writes nothing and files no log — and the
		// catalog is seeded so the document carries an entry rather than the
		// note about a file that names none.
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			f.appendConfig(t, catalogYAML)
			return []string{"image", "add", "app", "--tags", "node", "--dockerfile", "acme/app:.fugaro/app.Dockerfile", "--no-apply", "--dry-run"}
		},
	},
	"image build": {
		why: "the run's confirmation is typed at a terminal, and --json never gives one, so the command refuses before it prints any document",
	},
	"image check": {
		why: "the check clones the repository's main branch from its host, and nothing here stands in for that host: the command's own tests build a bare remote and rewrite git's url.insteadOf for it",
	},
	"image ls": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"image", "ls"}
		},
	},
	"image status": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"image", "status"}
		},
	},
	"image survey": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			// A repository is read through its host's own file API, and this
			// package has no host: the command's opener is the seam
			// (openSurveyProvider), and surveyStub — the one the survey's own
			// tests install — hands it the project's repository file-backed.
			surveyStub(t, map[string]map[string]string{
				"acme/app": {"package.json": `{"name":"app"}`},
			})
			return []string{"image", "survey"}
		},
	},
	"issue list": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"issue", "list"}
		},
	},
	"issue mine": {
		// No project and no store here, which is the run this fixture wants:
		// the notes that say so go to stderr and stdout is the one document.
		// Not a read: the listing uploads this machine's own pending records
		// before it prints them (F10 keeps it to the local files in a coding
		// agent's session, which is not the session this pin runs).
		run: func(t *testing.T) []string {
			return []string{"issue", "mine"}
		},
	},
	"issue show": {
		why: "the read goes to the repository's host, and this package has no fake for a GitHub issue",
	},
	"logs": {
		reads: true,
		lines: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			id := "20260927-100000-abcd"
			exec := seedRun(t, f, id, "", "", true)
			// Two entries, because this is the one run whose --json is a
			// document a line: one value for the whole output is the reading
			// the case exists to rule out, so the fixture has to be one a
			// whole-output check would fail.
			f.logging.AddJSONLines(exec, []byte(
				`{"time":"`+logTime(time.Second)+`","severity":"INFO","message":"stage started","stage":"implement"}`+"\n"+
					`{"time":"`+logTime(2*time.Second)+`","severity":"INFO","message":"stage finished","stage":"implement"}`+"\n"))
			return []string{"logs", id}
		},
	},
	"managed status": {
		reads: true,
		run: func(t *testing.T) []string {
			newManagedStatusRig(t, managedAuditOpts{})
			return []string{"managed", "status", "--project", "aurora"}
		},
	},
	// The mailbox consumer's --json is one document per pass, and --once is the
	// one pass this pin can hold it to. The fixture is the world a pass needs:
	// a pairing the config names and a keychain of the test's holding its key,
	// and one message in its inbox so the document it prints is a pass over
	// something rather than an empty one.
	"mailbox": {
		run: func(t *testing.T) []string {
			f := newMailboxFixture(t, "")
			f.writeAction(t, "msg-1", mailboxNowMs())
			return []string{"mailbox", "--once"}
		},
	},
	// The sweeper's --json is one document for the run, and a different document
	// from the consumer's pass: the mailbox it walked, what it deleted and what
	// it kept. --dry-run is the write-free run of that same body, so this pin
	// takes it: a prompt a day past D118's TTL is named among the deleted, which
	// is what says the run reached its own work rather than a refusal before it.
	"mailbox sweep": {
		reads: true,
		run: func(t *testing.T) []string {
			f := newMailboxFixture(t, "")
			f.announce(t, "p-old", time.Now().Add(-73*time.Hour).UnixMilli())
			return []string{"mailbox", "sweep", "--dry-run"}
		},
	},
	// models add carries --json only beside --dry-run (MD1): the document is
	// what one add would do, so the run this pin takes is the write-free one,
	// which is a read and leaves no log. The route comes from
	// models_add_test.go's own fixture, because the cloud fixture's config has
	// no provider for a model to be claimed on.
	"models add": {
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			f.appendConfig(t, modelsAddCloudExtra)
			return []string{"models", "add", "vendor/model-z", "--price-in", "1", "--price-out", "2", "--dry-run"}
		},
	},
	"models ls": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"models", "ls"}
		},
	},
	"escalation ls": {
		reads: true,
		run: func(t *testing.T) []string {
			escalationRig(t)
			return []string{"escalation", "ls"}
		},
	},
	"escalation show": {
		reads: true,
		run: func(t *testing.T) []string {
			escalationRig(t)
			return []string{"escalation", "show", escalationItemID}
		},
	},
	"models search": {
		reads: true,
		run: func(t *testing.T) []string {
			return []string{"models", "search", "deepseek"}
		},
	},
	"orchestrator events": {
		reads: true,
		run: func(t *testing.T) []string {
			orchestratorStateRig(t)
			return []string{"orchestrator", "events"}
		},
	},
	"orchestrator ls": {
		reads: true,
		run: func(t *testing.T) []string {
			f := lsFixture(t)
			seedOrchestratorNode(t, f, "aurora")
			seedOrchestratorLease(t, f, "aurora", "macbook", orchestratorTestNow)
			seedBriefCache(t, "aurora", orchestratorTestNow)
			return []string{"orchestrator", "ls"}
		},
	},
	"orchestrator import": {
		run: func(t *testing.T) []string {
			newOrchestratorFixture(t)
			// The plan path reads a graph in a checkout and the plan itself at
			// HEAD, so the rig is a real checkout with a real commit rather
			// than a fixture directory: the graph the import answers about is
			// the one committed there.
			t.Chdir(planImportCheckout(t, planImportGraph, planImportPlanMD))
			return []string{"orchestrator", "import", "--plan", "docs/demo"}
		},
	},
	"orchestrator patch": {
		run: func(t *testing.T) []string {
			f := newOrchestratorFixture(t)
			s := seedOrchestratorState(t, f)
			path := patchFile(t, patchDoc(t, s.Rev, orchestrator.Op{Op: orchestrator.OpFactSet,
				Fact: &orchestrator.FactOp{Key: "branch", Value: "release/1.2"}}))
			return []string{"orchestrator", "patch", "-f", path}
		},
	},
	"orchestrator state": {
		reads: true,
		run: func(t *testing.T) []string {
			orchestratorStateRig(t)
			return []string{"orchestrator", "state"}
		},
	},
	"pair": {
		why: "the pairing flow waits for a device to claim its code, and a run that waits owns the terminal until it is interrupted",
	},
	"pair ls": {
		reads: true,
		run: func(t *testing.T) []string {
			newBudgetFixture(t, "")
			return []string{"pair", "ls"}
		},
	},
	"pair revoke": {
		why: "a revocation names a device the fixture has no way to pair, so the run refuses before it prints a document",
	},
	"plan graph": {
		reads: true,
		run: func(t *testing.T) []string {
			_, planPath := planGraphFixture(t, planGraphDoc)
			return []string{"plan", "graph", "--plan", planPath}
		},
	},
	"plan status": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"plan", "status"}
		},
	},
	"project clone-url ls": {
		reads: true,
		run: func(t *testing.T) []string {
			f := newBudgetFixture(t, "")
			f.appendConfig(t, "clone_urls:\n  github/acme/app: https://github.com/acme/app\n")
			return []string{"project", "clone-url", "ls"}
		},
	},
	"project policy show": {
		reads: true,
		run: func(t *testing.T) []string {
			newBudgetFixture(t, "")
			return []string{"project", "policy", "show"}
		},
	},
	"project profile ls": {
		reads: true,
		run: func(t *testing.T) []string {
			newBudgetFixture(t, "")
			return []string{"project", "profile", "ls"}
		},
	},
	"project profile show": {
		reads: true,
		run: func(t *testing.T) []string {
			// The world's own config defines no profiles, and the read refuses a
			// name it has none of rather than printing an empty document, so
			// this run needs the block a project with a profile carries.
			modelsWriteHomeWith(t, projectProfileExtra)
			return []string{"project", "profile", "show"}
		},
	},
	"project repos ls": {
		reads: true,
		run: func(t *testing.T) []string {
			// The enrolment view's own reads: this machine's config, the
			// published node, and the two durable places a run is recorded in —
			// the runs bucket and spendDaily, which the seen fixture stands up
			// empty. A project that admits nothing and has seen nothing still
			// prints a document, and that is what this pin asks about: the shape
			// the console reads, not the rows in it.
			newSeenFixture(t)
			return []string{"project", "repos", "ls"}
		},
	},
	"project repos status": {
		reads: true,
		run: func(t *testing.T) []string {
			// The enrolment's own fixture: a published allow-list admitting one
			// repository and the container its run mounts. The command names
			// the repository rather than walking every one, so the pin reads
			// the nine rows of one screen.
			_, sm := statusFixture(t)
			seedGitHubAppKey(t, sm)
			return []string{"project", "repos", "status", "github/acme/app"}
		},
	},
	"project routing show": {
		reads: true,
		run: func(t *testing.T) []string {
			newBudgetFixture(t, "")
			return []string{"project", "routing", "show"}
		},
	},
	"project show": {
		reads: true,
		run: func(t *testing.T) []string {
			newBudgetFixture(t, "")
			return []string{"project", "show"}
		},
	},
	"recipes ls": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"recipes", "ls"}
		},
	},
	"recipes show": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"recipes", "show", "default"}
		},
	},
	"recipes validate": {
		reads: true,
		run: func(t *testing.T) []string {
			path := writeRecipeFile(t, t.TempDir(), "solo.yaml", projectSoloCatalogName)
			return []string{"recipes", "validate", path}
		},
	},
	"review": {
		why: "the launch needs an installation whose coordinator job the fake backend cannot run, so the run refuses before it prints a document",
	},
	"review show": {
		why: "the read needs a review run's artifact, which the fixture would have to fake a coordinator job to produce",
	},
	"rigor floor show": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"rigor", "floor", "show"}
		},
	},
	"run": {
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "Add a feature"}
		},
	},
	"runs digest": {
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			seedRun(t, f, "20260927-100000-abcd", "", "", true)
			return []string{"runs", "digest", "--repo", "acme/app"}
		},
	},
	"runs ls": {
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			seedRun(t, f, "20260927-100000-abcd", "", "", true)
			return []string{"runs", "ls"}
		},
	},
	"runs show": {
		reads: true,
		run: func(t *testing.T) []string {
			// The chain rig: three runs of one chain with the budget on, which
			// is what gives the document its limit, its spend and its layers.
			showRig(t)
			return []string{"runs", "show", showTip}
		},
	},
	"runs stats": {
		reads: true,
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			seedRun(t, f, "20260927-100000-abcd", "", "", true)
			return []string{"runs", "stats"}
		},
	},
	"runs summary": {
		reads: true,
		run: func(t *testing.T) []string {
			newSummaryFixture(t)
			return []string{"runs", "summary"}
		},
	},
	"runs watch": {
		reads: true,
		run: func(t *testing.T) []string {
			// The daemon's own rig with this command's host seam pointed at the
			// same fake host, so the document is the triage's real answer rather
			// than the fail-closed one a window with no host would give.
			newWatchFixture(t, green())
			return []string{"runs", "watch"}
		},
	},
	"seats": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"seats"}
		},
	},
	"secrets ls": {
		reads: true,
		run: func(t *testing.T) []string {
			secretsFixture(t)
			return []string{"secrets", "ls", "--repo", "acme/app"}
		},
	},
	"secrets rm": {
		run: func(t *testing.T) []string {
			secretsRmFixture(t)
			return []string{"secrets", "rm", "bitbucket-token", "--dry-run"}
		},
	},
	"secrets set": {
		stdin: tokenValue + "\n",
		run: func(t *testing.T) []string {
			secretsFixture(t)
			return []string{"secrets", "set", "claude-oauth-token", "--repo", "acme/app"}
		},
	},
	// The intake's two families print the intake's own answer as the document
	// (printIntakeAnswer), so what --json carries is the service's own wire
	// shape and no line of the human answer. The rig and the seam are
	// secrets_intake_test.go's: intakeUserJSONCase and intakeSharedJSONCase.
	"secrets shared ls":  intakeSharedJSONCase("ls", "", true),
	"secrets shared rm":  intakeSharedJSONCase("rm", "", false),
	"secrets shared set": intakeSharedJSONCase("set", intakeTestValue+"\n", false),
	"secrets user ls":    intakeUserJSONCase("ls", "", true),
	"secrets user rm":    intakeUserJSONCase("rm", "", false),
	"secrets user set":   intakeUserJSONCase("set", intakeTestValue+"\n", false),
	"security paths": {
		reads: true,
		run: func(t *testing.T) []string {
			baseBranchCheckout(t, "acme/app", securityNoKeyYAML, "main")
			return []string{"security", "paths"}
		},
	},
	"settings": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"settings"}
		},
	},
	"settings get": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"settings", "get", "default_claude_account"}
		},
	},
	"settings set": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"settings", "set", "max_parallel", "4", "--dry-run"}
		},
	},
	"settings unset": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"settings", "unset", "max_parallel", "--dry-run"}
		},
	},
	"setup": {
		run: func(t *testing.T) []string {
			newInitRig(t)
			return []string{"setup"}
		},
	},
	"stats runs": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"stats", "runs", "--project", "aurora"}
		},
	},
	"tell": {
		run: func(t *testing.T) []string {
			f := jsonCloud(t)
			id := "20260927-100000-abcd"
			seedRun(t, f, id, "", "someone@example.com", true)
			writeRecord(t, f, id, tellRecord(id, tellAccepting()))
			return []string{"tell", id, "check the flaky test before the fix"}
		},
	},
	"troubleshoot": {
		run: func(t *testing.T) []string {
			// The command's own rig (troubleshoot_test.go): a failed
			// command's log, a source tree, a credential and the agent's
			// answer, none of them this machine's -- the run reaches its own
			// document without an agent, a network or a change to anything of
			// the person's.
			x := tsSetup(t)
			x.id = tsFailedLog(t, time.Now().UTC(), "error: the account is not allowed here", "Failed in 1.2s")
			x.diag = diagnoseagent.Diagnosis{
				Outcome:   diagnoseagent.OutcomeConfiguration,
				Signature: "update configuration",
				Text:      "The account is not allowed for this repository.",
			}
			return []string{"troubleshoot", x.id.String()}
		},
	},
	"update": {
		run: func(t *testing.T) []string {
			updateRig(t)
			return []string{"update"}
		},
	},
	"update-skills": {
		reads: true,
		run: func(t *testing.T) []string {
			releaseBuild(t, "0.3.0")
			wiringCheckout(t, "{}")
			return []string{"update-skills", "--check"}
		},
	},
	"user ls": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"user", "ls"}
		},
	},
	"validate": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCheckout(t)
			return []string{"validate"}
		},
	},
	"watch": {
		why: "the screen owns the terminal until it is interrupted, so a run of it never returns an exit to assert on",
	},
	"workspace login": {
		run: func(t *testing.T) []string {
			workspaceFullHome(t)
			return []string{"workspace", "login", "workspace-alpha"}
		},
	},
	"workspace ls": {
		reads: true,
		run: func(t *testing.T) []string {
			workspaceFullHome(t)
			return []string{"workspace", "ls"}
		},
	},
	"workspace show": {
		reads: true,
		run: func(t *testing.T) []string {
			workspaceFullHome(t)
			return []string{"workspace", "show", "workspace-alpha"}
		},
	},
	// The three reads below are the family's own (docs/workspace-projects-users),
	// and each one opens the workspace's own Google Cloud project: its roster
	// node over the Realtime Database, its mechanism's IAM, and -- for every
	// member the workspace holds -- that member's own project, where the grants
	// `user ls` reports are stored (R11, R14: the workspace reads a member's own
	// roster only once that member is linked). A hermetic run of one through the
	// tree would have to fake all of that where the tree's own wiring opens it
	// (workspace_wiring.go), and this package has no rig that reaches that far:
	// the family's own tests build the command beside the tree with sides of
	// their own (wu7New, wu11New), which is how they reach a member's project at
	// all. `workspace project ls` and `status` are also the two reads D2821
	// puts on the workspace owner's machine, with the workspace's own project
	// selected, which is a home no other fixture here builds.
	"workspace project ls": {
		why: "the read opens the workspace's own Google Cloud project for its roster node and its mechanism, and each member's own project for that member's rows, which no rig here fakes where the tree's wiring opens them",
	},
	"workspace project status": {
		why: "the read measures every member of the workspace across the member's own Google Cloud project -- its database, its service accounts, its stored grants -- and the mechanism the workspace holds it by, none of which a run through the tree can reach hermetically",
	},
	"workspace user ls": {
		why: "the read opens the workspace's own roster node and then each linked member's own project for the grants stored there, so a hermetic run would need both the workspace's and every member's Google Cloud project faked where the tree's wiring opens them",
	},
}

// consoleReadCases are the console reads whose command carries no `--json`
// flag, keyed the way consoleReadPaths spells it. G7's second half is about the
// read the console spawns and not about the flag -- a read that filed a log
// would turn the console's own polling into the churn that evicts the log a
// person wants after a failure -- so each of these is run the way the console
// spawns it, the path's own words and no flag, and held to writing no log. A
// read that needs a rig of its own belongs in jsonCases instead, which is where
// the fixtures that prepare one live.
var consoleReadCases = []string{"fugaro version"}

// consoleReadEE is the reason the console reads whose code this build does not
// carry have no run here: the Subscription Edition's commands are the core
// tree's paid stubs, and a stub parses no flag and refuses before it does
// anything (extend.go), so there is no outcome of one to hold to the rule.
const consoleReadEE = "the command is the Subscription Edition's, and this package builds the core tree, where the name answers with the paid stub (extend.go), a command that parses no flag and refuses before it does anything: there is no run of it here"

// consoleReadEESubcommand is the same fact about a name the core tree does not
// stub: `orchestrator map` is registered **under a family the released tree
// builds** (`RegisterSubcommand`), so a core build carries no stub for it
// (extend.go's paidSubcommands says why) and cobra answers for the name as it
// does for one no build has. There is therefore no run of it here either, and
// the console's own floor argues the same way from the other side: the console
// refuses the read before it spawns anything until a release carries the
// command (the console's `ORCHESTRATOR_MAP_CLI_FLOOR`).
const consoleReadEESubcommand = "the command is the Subscription Edition's, registered under the core tree's own `orchestrator` family (RegisterSubcommand, ee/orchestrator/cli), so this package's core build holds no stub for it and no run of the name exists here"

// consoleReadExempt are the console reads this package has no run of, with the
// reason each has none: the Subscription Edition's five and its one such
// subcommand, and nothing else --
// `watch` is the one other read the pin cannot run, and its reason is the one
// its jsonCases entry already carries, so that one table stays the only place a
// reader has to look. What keeps this list honest is
// assertEveryConsoleReadIsHeld, which walks the design's own consoleReadPaths
// and fails on a read that is in none of the three, on an entry here that is
// not a console read, and on exemptions that became most of the list.
var consoleReadExempt = map[string]string{
	"fugaro coordinator intents": consoleReadEE,
	"fugaro coordinator show":    consoleReadEE,
	"fugaro coordinator status":  consoleReadEE,
	"fugaro graph":               consoleReadEE,
	"fugaro report":              consoleReadEE,
	// The console-fixes plan's CG7a: the Coordinator tab's map read, the one
	// entry here that is a subcommand of a family the released tree builds
	// rather than a paid name at the root.
	"fugaro orchestrator map": consoleReadEESubcommand,
}

// jsonCommandPaths walks the tree the released binary builds and returns every
// command that carries a `--json` flag, as the space-joined path a person
// types. It is the list the pin's coverage is measured against, so it is read
// from the tree rather than written down: a command added tomorrow is in it
// without anyone remembering to add it.
func jsonCommandPaths() []string {
	root := NewRootCmd()
	var paths []string
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		if c.Flags().Lookup("json") != nil {
			paths = append(paths, path)
		}
		for _, sub := range c.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	for _, c := range root.Commands() {
		walk(c, c.Name())
	}
	return paths
}

// jsonCloud is the rig most of these runs use: the fixture's fake cloud
// (endpoints pointed at the fakes, a file:// runs bucket, the project config
// isolated to a directory of the test's own) with the working directory moved
// aside, so a command that reads the checkout it stands in reads an empty one
// and never the repository this test itself lives in.
func jsonCloud(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	return f
}

// jsonCheckout is the rig for the commands that answer about the checkout they
// stand in: an installation that enrolls the repository, and a git checkout of
// it whose fugaro.yaml validates.
func jsonCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	isolateProjects(t, dir)
	writeProject(t, "aurora", "proj-1234")
	enrollRepo(t, "aurora", "acme/app")
	co := checkoutsAt(t, filepath.Join(dir, "src", "app"), checkoutsYAML, "https://github.com/acme/app.git")
	t.Chdir(co)
	return co
}

// orchestratorStateRig is the installation the orchestrator's reads answer
// about: the budget fixture's Firebase database with a state node the
// daemon's own encoder wrote, so the read has a document to take apart.
func orchestratorStateRig(t *testing.T) *budgetFixture {
	t.Helper()
	f := newOrchestratorFixture(t)
	seedOrchestratorState(t, f, workItemUpsert("web-fix", "fix the flaky test"))
	return f
}

// jsonHarness is the environment every fixture is run under: a terminal on the
// command's output, so the reporter believes a person is watching, FORCE_COLOR
// and CLICOLOR_FORCE set, which termenv would honour if the reporter did not
// decide for itself (T4(a)), no NO_COLOR, and a state directory of the test's
// own, so the log a run may keep is this test's to read.
func jsonHarness(t *testing.T) {
	t.Helper()
	isolateState(t)
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	old := writerIsTerminal
	writerIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { writerIsTerminal = old })
}

// jsonRun runs one fixture's command the way the pin asks it: with --json and
// --verbose, in a fresh rig, with the input the case carries.
func jsonRun(t *testing.T, c jsonCase) (string, string, error) {
	t.Helper()
	args := append(c.run(t), "--json", "--verbose")
	if c.stdin != "" {
		return executeStdin(t, c.stdin, args...)
	}
	return execute(t, args...)
}

// jsonLogs is every log the run left, whatever command filed it: a read has to
// leave none at all, not merely none under its own name.
func jsonLogs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(logDir(t))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// jsonNonEmptyLines is the lines of s that carry something, the way reportLines
// reads a run's account.
func jsonNonEmptyLines(s string) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
