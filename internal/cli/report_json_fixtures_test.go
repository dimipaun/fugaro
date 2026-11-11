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
			return []string{"backlog", "list", "--dir", backlogFixture(t)}
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
	"models ls": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"models", "ls"}
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
			return []string{"setup", "--yes"}
		},
	},
	"stats runs": {
		reads: true,
		run: func(t *testing.T) []string {
			jsonCloud(t)
			return []string{"stats", "runs", "--project", "aurora"}
		},
	},
	"update": {
		run: func(t *testing.T) []string {
			updateRig(t)
			return []string{"update", "--yes"}
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

// consoleReadExempt are the console reads this package has no run of, with the
// reason each has none: the Subscription Edition's five, and nothing else --
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
