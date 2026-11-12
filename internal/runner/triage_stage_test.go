package runner_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/triage"
)

// The triage stage in shadow (T-TR7a): every case below runs a real loop with
// the stage on and reads what the run recorded and what the stage was handed.
// The fixture writes agent.triage: false (testdata/fixture-repo), so a case
// about the stage turns it on itself and no other test's run buys a judge.

// triageOn flips the fixture's own agent.triage: false to true. The fixture
// writes the key so that the mode-opt-in configurations the other tests build
// from it run no triage call; a case that wants the stage says so here.
func triageOn(t *testing.T, cfg string) string {
	t.Helper()
	out := strings.Replace(cfg, "  triage: false\n", "  triage: true\n", 1)
	if out == cfg {
		t.Fatal("the configuration carries no `  triage: false` line to flip, so the stage would not run")
	}
	return out
}

// triageOff is triageOn's inverse, for the cases whose point is a file that
// writes the switch off: the fixture's own key, on a configuration a case
// built with triageOn.
func triageOff(t *testing.T, cfg string) string {
	t.Helper()
	out := strings.Replace(cfg, "  triage: true\n", "  triage: false\n", 1)
	if out == cfg {
		t.Fatal("the configuration carries no `  triage: true` line to flip")
	}
	return out
}

// triageCfg is the fixture configuration with block as the repository's own
// rigor: block and the triage switch on: the mode decides whether the stage
// runs at all (D1851, D1852), so a case names the block it is about.
func triageCfg(t *testing.T, block string, rounds int, extra string) string {
	t.Helper()
	cfg := strings.Replace(firstLineCfg(t, rounds, extra), "agent:\n", block+"agent:\n", 1)
	return triageOn(t, cfg)
}

// startupCfg is triageCfg at Startup, the cheapest mode that runs the stage.
func startupCfg(t *testing.T, rounds int, extra string) string {
	t.Helper()
	return triageCfg(t, "rigor:\n  default: startup\n", rounds, extra)
}

// triageBK is oauthBK with a configuration of the case's own and the owner's
// policy node published: the walk the stage's Claude account comes from runs
// only where a node and the switch are (autoRun's world), and the account the
// record names for the stage is what it decides.
func triageBK(t *testing.T, cfg, node string) *bk {
	t.Helper()
	g := newGW(t, strings.Replace(cfg, "auth: api-key", "auth: oauth", 1), "off", "")
	g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN=oauth-token-abcdef")
	b := attachBackend(t, g, "off")
	b.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	b.db.Set(projectpolicy.PathShared(), json.RawMessage(node))
	return b
}

// briefFindingRE reads one finding's header out of a triage brief: the index
// and the key a verdict must name it by (D1850). It is how the answer below
// says "one entry per finding the brief numbered" without a test having to
// know what the keys are.
var briefFindingRE = regexp.MustCompile(`(?m)^    \[(\d+)\] ([0-9a-f]{16,64}) — `)

// triageStep answers the judge's brief: one entry per finding the brief
// numbered, every one of them word. A round with no findings earns the empty
// array, which is a verdict that answers nothing and is read as one.
func triageStep(word string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		findings := []map[string]any{}
		for _, m := range briefFindingRE.FindAllStringSubmatch(req.Prompt, -1) {
			i, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("the brief's finding header %q does not parse: %v", m[0], err)
			}
			findings = append(findings, map[string]any{
				"index": i, "key": m[2], "verdict": word, "reason": "judged for this test",
			})
		}
		b, err := json.Marshal(map[string]any{"version": 1, "findings": findings})
		if err != nil {
			t.Fatal(err)
		}
		return agent.Result{Structured: b, CostUSD: 0.25}, nil
	}
}

// blockingFinding is a finding the mode's table blocks and the floor does not
// name: risk 9 against Startup's floor of 8, class correctness, and nothing
// only the owner can settle — the one kind of finding a judge's own verdict
// may move (D1857).
func blockingFinding() map[string]any {
	return map[string]any{
		"summary": "unbounded retry loop in the fetch path", "file": "feature.txt",
		"likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	}
}

// findingKeyOf is the key internal/runner derives for a finding: the digest of
// its file and summary (findingKey, D1850). It is restated here so a case can
// say what the record's key must be rather than only that it is non-empty.
func findingKeyOf(file, summary string) string {
	sum := sha256.Sum256([]byte(file + "\x00" + summary))
	return hex.EncodeToString(sum[:])
}

// triageCalls are the agent calls of the run that are the judge's, in order:
// a stage whose brief carries the goal block is the triage stage and no other.
func triageCalls(calls []agent.Request) []agent.Request {
	var out []agent.Request
	for _, req := range calls {
		if strings.Contains(req.Prompt, "<<<fugaro-triage-goal-") {
			out = append(out, req)
		}
	}
	return out
}

// personaOf is the persona half of a triage brief: the text before the floor
// sentence, which is the only part of it that is the mode's own file
// (internal/triage's Prompt).
func personaOf(brief string) string {
	persona, _, _ := strings.Cut(brief, "\nThe floor is binding")
	return persona
}

// TestTriageRunsAfterEachReviewRound: the stage follows every review round the
// run runs, on the round's own findings, and the record's triage block says so
// — one round per review round, with the index, key and word the judge
// answered (D1850). The second review round ships, so the run has a round with
// no findings and the block keeps it as one.
//
// The judge answers `block` here, which is what buys the second review round:
// an answer that deferred the round whole ends it (T-TR7b), and this case is
// about the round-by-round log and not about that.
func TestTriageRunsAfterEachReviewRound(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageStep("block"),
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage", "fix", "review", "triage") {
		t.Fatalf("stages = %v, want a triage round after each of the two review rounds", got)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want one round per review round", tr)
	}
	first := tr.Rounds[0]
	if first.Round != 1 || first.Mode != rigor.Startup || first.Level != policy.LevelL2 || first.Failed {
		t.Errorf("the first triage round = %+v, want round 1 of the startup run at level 2 and not failed", first)
	}
	if first.Model != rec.Reviews[0].Model {
		t.Errorf("the judge ran on %q, want the senior review's own model %q (D1848)", first.Model, rec.Reviews[0].Model)
	}
	if len(first.Entries) != 1 {
		t.Fatalf("the first round's entries = %+v, want one per finding the round read", first.Entries)
	}
	e := first.Entries[0]
	if e.Index != 0 || e.Key != findingKeyOf("feature.txt", "unbounded retry loop in the fetch path") {
		t.Errorf("the entry = %+v, want the round's first finding under the digest of its file and summary", e)
	}
	// The word and its source are the judge's own: the mode's table blocks this
	// finding, so a placement with source table would be the table overriding
	// the answer rather than the answer standing.
	if e.Verdict != runstore.VerdictBlock || e.Source != runstore.TriageSourceAgent || e.Reason != "judged for this test" {
		t.Errorf("the entry = %+v, want the judge's own defer with its reason", e)
	}
	second := tr.Rounds[1]
	if second.Round != 2 || second.Failed || len(second.Entries) != 0 {
		t.Errorf("the second round = %+v, want the round that read no findings", second)
	}
}

// TestTriagePersonaFollowsTheReachedMode: the judge is asked from the persona
// of the mode the run had reached at that round, not from the mode it started
// at (F14). The repository is Startup's and the file the task touches is an
// area set to Pro, so the fix between the two rounds resolves the mode again
// (D22's second moment) and the second round's brief is Pro's.
func TestTriagePersonaFollowsTheReachedMode(t *testing.T) {
	block := "rigor:\n  default: startup\n  areas:\n    - { name: app, match: feature.txt, mode: pro }\n"
	h := newHarness(t, triageCfg(t, block, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageStep("block"),
		fixStep,
		reviewScored("changes", blockingFinding()),
		triageStep("block"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Rigor == nil || rec.Rigor.Mode != rigor.Pro {
		t.Fatalf("the run ended at %+v, want the area's Pro: the two briefs below would then be the same persona", rec.Rigor)
	}
	briefs := triageCalls(h.agent.calls)
	if len(briefs) != 2 {
		t.Fatalf("%d triage calls, want one per review round", len(briefs))
	}
	if got, want := personaOf(briefs[0].Prompt), personaOf(triage.Prompt(rigor.Startup, "", nil)); got != want {
		t.Errorf("the first round was asked from another persona than Startup's:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if got, want := personaOf(briefs[1].Prompt), personaOf(triage.Prompt(rigor.Pro, "", nil)); got != want {
		t.Errorf("the second round was asked from another persona than the reached mode's Pro:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestTriageSkippedInPOCAndCritical: the two modes whose own table decides
// everything run no judge at all (D1851, D1855), and the switch the repository
// wrote cannot put the stage back into them — the run is configured, the file
// says triage: true, and no call is made.
func TestTriageSkippedInPOCAndCritical(t *testing.T) {
	// Critical's own row turns level 3 on for every run (internal/rigor's
	// defaultLevels), so its script answers the adversarial round too; POC runs
	// the senior review alone and ends the run after the fix.
	cases := []struct {
		mode  string
		steps []step
	}{
		{"poc", []step{implement("feature"), reviewScored("changes", blockingFinding()), fixStep}},
		{"critical", []step{implement("feature"), reviewScored("changes", blockingFinding()), review("ship", 0)}},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			h := newHarness(t, triageCfg(t, "rigor:\n  default: "+c.mode+"\n", 1, ""), nil)
			rec, err := h.run(t, c.steps...)
			if err != nil {
				t.Fatal(err)
			}
			if n := countStage(rec, "triage"); n != 0 {
				t.Errorf("%d triage stages ran in %s, want none", n, c.mode)
			}
			if rec.Triage != nil {
				t.Errorf("the record carries a triage block in %s: %+v", c.mode, rec.Triage)
			}
		})
	}
}

// TestTriageSkippedForAnUnconfiguredRepository: a repository with no rigor:
// block resolves Pro by default (D19) and never triages (D1852) — the answer
// is the base branch's, and a file that writes the switch on cannot make an
// unconfigured repository defer anything.
func TestTriageSkippedForAnUnconfiguredRepository(t *testing.T) {
	h := newHarness(t, triageOn(t, testutil.FixtureFiles(t)["fugaro.yaml"]), nil)
	rec, err := h.run(t, implement("feature"), reviewScored("changes", blockingFinding()), fixStep, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Rigor == nil || rec.Rigor.Mode != rigor.Pro {
		t.Fatalf("the run resolved %+v, want the Pro an unconfigured repository gets (D19)", rec.Rigor)
	}
	if n := countStage(rec, "triage"); n != 0 {
		t.Errorf("%d triage stages ran for a repository that configured no rigor: block, want none", n)
	}
	if rec.Triage != nil {
		t.Errorf("the record carries a triage block: %+v", rec.Triage)
	}
}

// TestTriageFailureIsRecordedAndHarmless: a judge call that fails is recorded
// as a failed round with the fail-safe placement — every finding the table
// blocks blocks — and changes nothing else about the run (D1846). The fix
// round is still bought and still gets the round's findings.
func TestTriageFailureIsRecordedAndHarmless(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		failStep,
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, want a run the failed judge did not end", rec)
	}
	if n := countStage(rec, "fix"); n != 1 {
		t.Errorf("%d fix stages, want the round's fix bought as it always was", n)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want the failed round kept beside the one that answered", tr)
	}
	failed := tr.Rounds[0]
	if !failed.Failed || failed.Round != 1 {
		t.Errorf("the failed round = %+v, want round 1 marked failed", failed)
	}
	if len(failed.Entries) != 1 {
		t.Fatalf("the failed round's entries = %+v, want the table's placement for the finding it read", failed.Entries)
	}
	if e := failed.Entries[0]; e.Verdict != runstore.VerdictBlock || e.Source != runstore.TriageSourceFailSafe {
		t.Errorf("the failed round's entry = %+v, want a block the table placed, not the judge", e)
	}
	if second := tr.Rounds[1]; second.Failed {
		t.Errorf("the second round = %+v, want the round that answered", second)
	}
}

// TestTriageWorkingDirectoryFailureIsRecordedAndHarmless: the judge is asked
// from a directory that holds nothing of the run, and a run that cannot make
// one does not ask the judge from the checkout instead — the round is recorded
// as failed with the fail-safe placement, the same record a round whose answer
// could not be read leaves (D1846), and the run goes on: the fix round is still
// bought and the round after it is judged as usual.
func TestTriageWorkingDirectoryFailureIsRecordedAndHarmless(t *testing.T) {
	orig, had := os.LookupEnv("TMPDIR")
	t.Cleanup(func() {
		if had {
			os.Setenv("TMPDIR", orig)
			return
		}
		os.Unsetenv("TMPDIR")
	})
	// MkdirTemp reads TMPDIR on every call, so the window is the round's own:
	// it opens as the review step answers — the review stage's own call is
	// behind it — and closes at the fix step, which is the next call the run
	// makes. The run's only other MkdirTemp is writeStampHooks', at bootstrap,
	// before the window opens.
	missing := filepath.Join(t.TempDir(), "not-a-directory")
	reviewWithoutTemp := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := reviewScored("changes", blockingFinding())(t, ctx, req)
		if err == nil {
			if err := os.Setenv("TMPDIR", missing); err != nil {
				t.Fatal(err)
			}
		}
		return res, err
	}
	fixWithTemp := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if had {
			if err := os.Setenv("TMPDIR", orig); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Unsetenv("TMPDIR"); err != nil {
			t.Fatal(err)
		}
		return fixStep(t, ctx, req)
	}
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewWithoutTemp,
		fixWithTemp,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, want a run the round with no working directory did not end", rec)
	}
	if n := countStage(rec, "fix"); n != 1 {
		t.Errorf("%d fix stages, want the round's fix bought as it always was", n)
	}
	if n := len(triageCalls(h.agent.calls)); n != 1 {
		t.Errorf("%d judge calls, want the round that had a working directory alone: a run with nowhere to ask the judge does not ask it from the checkout", n)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want the failed round kept beside the one that answered", tr)
	}
	failed := tr.Rounds[0]
	if !failed.Failed || failed.Round != 1 || failed.Level != policy.LevelL2 {
		t.Errorf("the failed round = %+v, want round 1 of the senior level marked failed", failed)
	}
	if len(failed.Entries) != 1 {
		t.Fatalf("the failed round's entries = %+v, want the table's placement for the finding it read", failed.Entries)
	}
	if e := failed.Entries[0]; e.Verdict != runstore.VerdictBlock || e.Source != runstore.TriageSourceFailSafe {
		t.Errorf("the failed round's entry = %+v, want a block the table placed and not a round with no entries at all", e)
	}
	if second := tr.Rounds[1]; second.Failed {
		t.Errorf("the second round = %+v, want the round whose judge had a directory to be asked from", second)
	}
}

// TestTriageStageFailureEndsTheRun: a judge call the run could not make — a
// model the gateway refused for the stage, a cancel, a halt, a spent budget —
// is a stage failure like every other one, and the loop that called the judge
// stops on it. The shadow rule is about what the run does with a *verdict*: it
// says a verdict moves nothing, not that a failure is swallowed, and a run its
// shadow stage already failed buys no fix round, no further review and no
// second judge — the stage a run ends on is recorded where it failed and not
// papered over by the stages a failed run would otherwise go on dispatching.
func TestTriageStageFailureEndsTheRun(t *testing.T) {
	b := newHandleBox(t)
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	const violation = "model claude-x is not pinned for stage triage"
	arm := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		b.h.SetStageExtra(func(stage string) ([]string, int64) {
			if stage == "triage" {
				return []string{violation}, 0
			}
			return nil, 0
		})
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t,
		arm,
		reviewScored("changes", blockingFinding()),
		triageStep("block"),
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Reason != "stage triage: "+violation {
		t.Fatalf("rec = %+v, want the run failed by the judge's own stage", rec)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage") {
		t.Fatalf("stages = %v, want the run to stop at the judge that failed it", got)
	}
	if len(h.agent.calls) != 3 {
		t.Errorf("%d agent calls, want the implement, the review and the judge: a run the judge failed runs no further stage", len(h.agent.calls))
	}
	if rec.Triage != nil {
		t.Errorf("the record's triage block = %+v, want none: the round a stage failure ended is the run's failure, not a failed round", rec.Triage)
	}
}

// TestTriageRequestHasNoToolsAndNoSettingsSources is F25/F29's invariant: the
// judge reads text and touches nothing, so its request carries the empty tool
// list and the empty setting-source list — both non-nil, because a nil list is
// the CLI's own default set and would hand the stage every tool and the user's
// own settings — and it runs in a directory of its own rather than in the
// checkout under review. The managed settings it is launched under are the
// review posture, which is the third of F29's parts and the one that keeps a
// hook of the checkout out of a stage that reads its text.
func TestTriageRequestHasNoToolsAndNoSettingsSources(t *testing.T) {
	// A gateway harness, because the managed settings file is what a stage
	// launched without one never writes at all (writeManagedSettings returns
	// before it writes anything when there is no gateway and no stage
	// account), and this test reads that file as the judge starts.
	h := newGW(t, triageOn(t, gwConfig(t, "rigor:\n  default: startup\n")), "observe", "").harness
	var posture string
	read := func(inner step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			_, _, raw := settingsEnv(t, h)
			posture = raw
			return inner(t, ctx, req)
		}
	}
	if _, err := h.run(t, implement("feature"), reviewScored("ship", blockingFinding()), read(triageStep("block"))); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"disableAllHooks", "allowManagedHooksOnly", "allowManagedMcpServersOnly", "allowedMcpServers"} {
		if !strings.Contains(posture, key) {
			t.Errorf("the judge's managed settings carry no %s: %s", key, posture)
		}
	}
	calls := triageCalls(h.agent.calls)
	if len(calls) != 1 {
		t.Fatalf("%d triage calls, want one", len(calls))
	}
	req := calls[0]
	if req.Tools == nil || len(req.Tools) != 0 {
		t.Errorf("Tools = %#v, want a non-nil empty list: a nil one is the CLI's default set", req.Tools)
	}
	if req.SettingSources == nil || len(req.SettingSources) != 0 {
		t.Errorf("SettingSources = %#v, want a non-nil empty list: a nil one is the user's own settings", req.SettingSources)
	}
	if !req.StrictMCPConfig {
		t.Error("the judge's request does not carry --strict-mcp-config")
	}
	if req.JSONSchema != runner.TriageSchema {
		t.Errorf("JSONSchema = %q, want the triage stage's own", req.JSONSchema)
	}
	if req.Dir == "" || req.Dir == h.deps.WorkDir {
		t.Errorf("Dir = %q, want a directory of its own and not the checkout %q", req.Dir, h.deps.WorkDir)
	}
	if _, err := os.Stat(req.Dir); !os.IsNotExist(err) {
		t.Errorf("the judge's directory %q is still there after the run: %v", req.Dir, err)
	}
	for _, other := range h.agent.calls {
		if other.Prompt == req.Prompt {
			continue
		}
		if other.SessionID == req.SessionID {
			t.Errorf("the judge shares a session with another stage: %q", other.SessionID)
		}
	}
	if !strings.Contains(req.Prompt, "Add a feature") {
		t.Errorf("the brief does not carry the task's goal:\n%s", req.Prompt)
	}
}

// TestTriageStageAccountIsRecorded: the judge is a stage of the run like any
// other, so the account that served it is in the record's per-stage map — and
// a run that triages nothing has no entry for it, which is what keeps the map
// from claiming a stage nobody ran (D876, D1848).
func TestTriageStageAccountIsRecorded(t *testing.T) {
	node := autoNode(t, "true", accountA, accountB)
	steps := []step{
		implement("feature"),
		reviewScored("ship", blockingFinding()),
		triageStep("defer"),
	}
	t.Run("the judge's own account", func(t *testing.T) {
		b := triageBK(t, startupCfg(t, 2, ""), node)
		rec, err := b.run(t, steps...)
		if err != nil {
			t.Fatal(err)
		}
		if got := rec.StageAccounts["triage"]; got != config.DefaultClaudeAccount {
			t.Errorf("stage_accounts = %+v, want the account that served the judge", rec.StageAccounts)
		}
	})
	t.Run("no entry for a stage that did not run", func(t *testing.T) {
		b := triageBK(t, modeCfg(t, "startup", 2, ""), node)
		rec, err := b.run(t, implement("feature"), reviewScored("ship", blockingFinding()))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := rec.StageAccounts["triage"]; ok {
			t.Errorf("stage_accounts = %+v, want no entry for a stage this run never ran", rec.StageAccounts)
		}
	})
}

// TestTriageSwitchIsReadFromTheBaseBranch: the switch is the base branch's
// answer and never the checkout's (D25, D1854). The branch under review is the
// artifact being reviewed: a branch that could turn the judge on for its own
// pull request would be choosing who judges it, and one that could turn it off
// would be choosing not to be judged.
//
// The two cases below are the two ways the checkout's answer differs from the
// base's. In the first the base reads fugaro.yaml and writes the switch off
// while the branch writes it on; in the second there is no base file to read at
// all — baseCfg is nil — and the mode the run is judged at comes from the
// project layer the task carried, which is the one part of a configuration that
// is no branch's. Neither run calls a judge, and a run that read r.cfg instead
// would call one in both.
func TestTriageSwitchIsReadFromTheBaseBranch(t *testing.T) {
	t.Run("the checkout turns it on over a base that does not", func(t *testing.T) {
		// main, the base, is the fixture with a rigor block and the switch off;
		// the branch under review is the same file with the switch on.
		h := newHarness(t, triageOff(t, startupCfg(t, 1, "")), nil)
		pushBranch(t, h, "feature", startupCfg(t, 1, ""))
		setRef(t, h, task.Spec{Ref: "feature"})
		// The judge's answer is scripted so that a run which asked for one is
		// answered rather than failing on the unscripted call: what this case
		// reads is whether the call was made at all.
		rec, err := h.run(t, implement("feature"), review("ship", 0), triageStep("defer"))
		mustReady(t, rec, err)
		if rec.Rigor == nil || rec.Rigor.Mode != rigor.Startup {
			t.Fatalf("the run resolved %+v, want the Startup both files name: the mode is not what this case is about", rec.Rigor)
		}
		if n := countStage(rec, "triage"); n != 0 {
			t.Errorf("%d triage stages, want none: the branch under review's own switch is not the base's", n)
		}
		if rec.Triage != nil {
			t.Errorf("the record carries a triage block: %+v", rec.Triage)
		}
	})
	t.Run("a base that cannot be read", func(t *testing.T) {
		// The repository's default branch has no fugaro.yaml: the base is
		// unreadable, baseCfg is nil, and the mode comes from the project layer
		// (rigor: startup under defaults). The checkout asks for the stage, and
		// the run makes no call: with no base file to read a switch from, the
		// strict direction is no triage, and the checkout's own answer is the
		// one this must never fall back to.
		checkout := strings.Replace(triageOn(t, firstLineCfg(t, 1, "")), "project: aurora\n", "project: aurora\ngcp_project: proj-1234\n", 1)
		layer := layerSpec(triageLayer).ProjectLayer
		h := newHarness(t, checkout, layerSpec(triageLayer))
		pushBranch(t, h, "feature", checkout)
		commitToRemote(t, h, func(dir string) {
			if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
				t.Fatal(err)
			}
		})
		setRef(t, h, task.Spec{Ref: "feature", ProjectLayer: layer})
		rec, err := h.run(t, implement("feature"), review("ship", 0), triageStep("defer"))
		// mustReady as well as the two below: a switch read off a nil baseCfg
		// does not answer false, it panics, and a stage the runner recovers is
		// a run that ends failed with no triage stage either — the assertion
		// that tells the two apart is that this run finished its work.
		mustReady(t, rec, err)
		if rec.Rigor == nil || rec.Rigor.Mode != rigor.Startup {
			t.Fatalf("the run resolved %+v, want the layer's Startup: the mode gate is not what this case is about", rec.Rigor)
		}
		if n := countStage(rec, "triage"); n != 0 {
			t.Errorf("%d triage stages, want none: a run whose base file cannot be read triages nothing", n)
		}
		if rec.Triage != nil {
			t.Errorf("the record carries a triage block: %+v", rec.Triage)
		}
	})
}

// triageLayer is a project layer whose defaults.rigor names Startup and nothing
// else: the mode a run whose base branch cannot be read still resolves, which is
// what makes the second case above about the switch and not about the mode.
const triageLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  rigor:
    default: startup
`

// TestTriageSwitchIsReadFromThePublishedNode: the owner's published node is
// where the off switch lives (D1854), read beside the base branch's own key
// and merged tighter-only, so a node that turns the stage off takes it away
// from a repository whose file asks for it — and a node that leaves it on
// leaves the repository's own answer standing.
func TestTriageSwitchIsReadFromThePublishedNode(t *testing.T) {
	cases := []struct {
		name string
		node string
		want int
	}{
		{"the node turns it off", `{"version":4,"triage":false}`, 0},
		{"the node leaves it on", `{"version":4,"triage":true}`, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := triageOn(t, gwConfig(t, "rigor:\n  default: pro\n"))
			b := policyRun(t, cfg, "off", "", c.node)
			rec, err := b.run(t, implement("feature"), reviewScored("ship", blockingFinding()), triageStep("defer"))
			if err != nil {
				t.Fatal(err)
			}
			if n := countStage(rec, "triage"); n != c.want {
				t.Errorf("%d triage stages, want %d", n, c.want)
			}
		})
	}
}
