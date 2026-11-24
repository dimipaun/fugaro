package runner_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

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
	"golang.org/x/oauth2"
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
	block := "rigor:\n  default: startup\n  areas:\n    - { name: app, match: feature.txt, level: pro }\n"
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

// triageLayer is a project layer whose defaults.rigor names Startup: the mode a
// run whose base branch cannot be read still resolves, which is what makes the
// second case above about the switch and not about the mode.
//
// It names the three roles a run behind a gateway must be priced by
// (CheckPins, D3020) because it is the whole of that run's configuration: the
// base branch's file is gone, and since S1 (D3237) the checkout's own
// agent.models block is dropped before the merge, so a layer that named only
// the rigor would leave the run with no model to price it by and it would be
// refused at bootstrap instead of reaching the switch this test is about.
const triageLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  agent:
    models: { coder: claude-sonnet-5-5, reviewer: claude-opus-5, background: claude-haiku-4-5 }
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
		{"the node turns it off", fmt.Sprintf(`{"version":%d,"triage":false}`, projectpolicy.Version), 0},
		{"the node leaves it on", fmt.Sprintf(`{"version":%d,"triage":true}`, projectpolicy.Version), 1},
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

// triageTextStep is a judge call that answered words and no verdict: the shape
// Claude Code leaves when the endpoint it was pointed at refused the run's own
// credential — "Invalid API key · Fix external API key" is the text, and the
// process ran, exited zero and reported a successful result, so this is a
// Result and not an error. It is the call run 20261115-194116-0846 bought
// twice, at one second each, with no verdict behind either.
func triageTextStep(text string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		return agent.Result{Text: text, Subtype: "success"}, nil
	}
}

// triageStructuredStep is a judge call that answered the structured output the
// schema should have produced, with something in it the reader refuses: a
// verdict word outside the two, a key that names no finding, a reason the
// model wrapped onto a second line.
func triageStructuredStep(raw string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		return agent.Result{Structured: json.RawMessage(raw), Subtype: "success"}, nil
	}
}

// triageLogEntry is the run's own log line about a round whose call bought no
// verdict: the first entry whose message is the runner's sentence about it,
// parsed out of the JSON the logger writes.
func triageLogEntry(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if msg, _ := entry["message"].(string); strings.Contains(msg, "produced no readable verdict") {
			return entry
		}
	}
	t.Fatalf("the run's log carries no line about the round's unreadable verdict:\n%s", raw)
	return nil
}

// TestTriageAuthFailureIsRecordedWithItsCause: a judge call an installation's
// own credential was refused for bought no verdict, and the run says so
// everywhere a person reads it rather than recording a stage that looks like
// any other one. The call is a *successful* result — the process ran — so
// nothing else in the run would have marked it; the round is failed with the
// call's own words as its cause (D1846), the log line is an ERROR naming that
// cause, and the pull request's status section carries both the marked stage
// row and the line above the table saying the judge did not answer and the
// fail-safe blocked what the mode's table blocks.
func TestTriageAuthFailureIsRecordedWithItsCause(t *testing.T) {
	const answer = "Invalid API key · Fix external API key"
	var logs bytes.Buffer
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	h.deps.Log = runner.NewLogger(&logs)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageTextStep(answer),
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, want the run the failed judge did not end", rec)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want the failed round kept beside the one that answered", tr)
	}
	failed := tr.Rounds[0]
	if !failed.Failed || failed.Round != 1 {
		t.Errorf("the failed round = %+v, want round 1 marked failed", failed)
	}
	if failed.Cause != answer {
		t.Errorf("the failed round's cause = %q, want the call's own words %q", failed.Cause, answer)
	}
	if e := failed.Entries[0]; e.Verdict != runstore.VerdictBlock || e.Source != runstore.TriageSourceFailSafe {
		t.Errorf("the failed round's entry = %+v, want the fail-safe's block and not a judge's placement", e)
	}
	if second := tr.Rounds[1]; second.Failed || second.Cause != "" {
		t.Errorf("the second round = %+v, want the round that answered and no cause", second)
	}
	entry := triageLogEntry(t, logs.Bytes())
	if entry["severity"] != "ERROR" || entry["cause"] != answer {
		t.Errorf("the run's log line about the round = %v, want an ERROR naming the cause", entry)
	}
	got := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil)
	for _, want := range []string{
		"**Triage:** the judge did not answer for round 1 of level 2: " + answer + ", so the fail-safe blocked every finding the mode's table blocks.",
		"| triage (failed: " + answer + ") |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report lacks %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "| triage"); n != 2 {
		t.Errorf("the report has %d triage rows, want the run's two rounds", n)
	}
}

// TestTriageUnreadableVerdictIsRecordedWithTheReadersCause: a judge that ran
// and answered something shaped like a verdict the reader refused is a
// different fact from one that never answered at all, and the record tells the
// two apart: the round failed with the reader's own cause rather than the
// answer's text, which is what a reader seeing the two records side by side
// needs to tell "the judge did not answer" from "the judge's answer could not
// be read" (runstore.TriageCauseUnreadable, runstore.TriageFailureClause).
func TestTriageUnreadableVerdictIsRecordedWithTheReadersCause(t *testing.T) {
	// Shaped like the schema's own object and refused by it: the word is
	// neither of the two the round answers with.
	const answer = `{"version":1,"findings":[{"index":0,"key":"9f2a1c4d5e6a7b8c","verdict":"maybe","reason":"judged for this test"}]}`
	var logs bytes.Buffer
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	h.deps.Log = runner.NewLogger(&logs)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageStructuredStep(answer),
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want the failed round kept beside the one that answered", tr)
	}
	failed := tr.Rounds[0]
	if !failed.Failed || failed.Cause != runstore.TriageCauseUnreadable {
		t.Errorf("the failed round = %+v, want round 1 failed with %q", failed, runstore.TriageCauseUnreadable)
	}
	if e := failed.Entries[0]; e.Source != runstore.TriageSourceFailSafe {
		t.Errorf("the failed round's entry = %+v, want the fail-safe's block", e)
	}
	if entry := triageLogEntry(t, logs.Bytes()); entry["cause"] != runstore.TriageCauseUnreadable {
		t.Errorf("the run's log line = %v, want the reader's own cause", entry)
	}
	got := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil)
	for _, want := range []string{
		"**Triage:** the judge's answer for round 1 of level 2 could not be read,",
		"| triage (failed: " + runstore.TriageCauseUnreadable + ") |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report lacks %q:\n%s", want, got)
		}
	}
}

// triageTableRows is the stage table's triage rows out of a report, in the
// record's own order: a case reads the marks off them, which is where a reader
// of the pull request's body sees them.
func triageTableRows(t *testing.T, report string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "| triage") {
			out = append(out, line)
		}
	}
	return out
}

// triageRowKeys is what each of the record's triage rows carries, as
// "<level>:<round>:<ordinal>" from the three members the row keeps
// (runstore.StageTiming's Level, Round and Ordinal). The ordinal is the key the
// report pairs a row with its round's failure by, and the last field here is what
// makes a case about that pairing readable: the round and the level before it are
// the description a reader follows, and both halves of it repeat — two levels of
// one run both number a round 1 (D1847), and a bounce numbers a level's rounds
// from 1 again (D727).
func triageRowKeys(rec *runstore.Record) []string {
	var out []string
	for _, s := range rec.Stages {
		if s.Name == "triage" {
			out = append(out, string(s.Level)+":"+strconv.Itoa(s.Round)+":"+strconv.Itoa(s.Ordinal))
		}
	}
	return out
}

// triageCutStream is a judge attempt whose model stream closed before its
// response finished: the retry the runner gives that failure and no other
// (transientStagePhrases), which runs the stage once more and records the ended
// attempt's own timing row.
func triageCutStream(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	return agent.Result{Text: streamClosedText, IsError: true, Subtype: "success"}, nil
}

// TestTriageRowCarriesItsOwnRoundsFailure: where two triage rounds of one run
// are each recorded and one of them failed, the stage row that says so is the
// row of that round's own call — never one picked by position. The second
// round's judge loses its stream first, so that round has two rows of its own,
// which is the shape a table read back from the end gets wrong: it marks round
// 2's first row with round 1's failure and leaves round 1's own row looking
// like a judge that ran (runstore.StageTiming.Round, D1846).
func TestTriageRowCarriesItsOwnRoundsFailure(t *testing.T) {
	const answer = "Invalid API key · Fix external API key"
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageTextStep(answer), // round 1: the judge bought no verdict
		fixStep,
		review("ship", 0),
		triageCutStream,     // round 2, first attempt: the stream closed
		triageStep("defer"), // round 2, second attempt: a verdict
	)
	if err != nil {
		t.Fatal(err)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 || !tr.Rounds[0].Failed || tr.Rounds[1].Failed {
		t.Fatalf("the record's triage block = %+v, want round 1 failed and round 2 answered", tr)
	}
	if got := triageRowKeys(rec); !eq(got, "l2:1:1", "l2:2:2", "l2:2:2") {
		t.Fatalf("the triage rows carry the keys %v, want round 1's row and round 2's retried pair", got)
	}
	// The new fields are additive: a record carrying them is one the published
	// schema accepts.
	assertStoredRecordValidates(t, h.store)
	rows := triageTableRows(t, runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil))
	if len(rows) != 3 {
		t.Fatalf("the report has %d triage rows, want the run's three attempts:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[0], "(failed: "+answer+")") {
		t.Errorf("round 1's own row = %q, want it marked with the failure its round recorded", rows[0])
	}
	for i, row := range rows[1:] {
		if strings.Contains(row, "(failed") {
			t.Errorf("round 2's row %d = %q, want no mark: the failure is round 1's", i+1, row)
		}
	}
}

// TestTriageRowCarriesItsOwnRoundsFailureWhenARetryHalts: a retried triage
// attempt records its timing row and then the run halts on it, so the record's
// last triage rows belong to a round it never recorded at all. The failure line
// and the table must still agree about the rounds the record does carry: the
// round 1 row is marked, the two rows of the halted round 2 are not, and the
// line above the table names round 1 alone. Counting the rows back from the end
// puts round 1's mark on the halted attempt's row instead.
func TestTriageRowCarriesItsOwnRoundsFailureWhenARetryHalts(t *testing.T) {
	const answer = "Invalid API key · Fix external API key"
	b := newHandleBox(t)
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageTextStep(answer), // round 1: the judge bought no verdict
		fixStep,
		review("ship", 0),
		triageCutStream,                   // round 2, first attempt: the stream closed
		haltAfter(b, triageStep("defer")), // round 2, second attempt: a row, then the halt
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("rec = %+v, halt = %+v, want the run halted on the attempt that recorded a row", rec, rec.Halt)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 1 || !tr.Rounds[0].Failed {
		t.Fatalf("the record's triage block = %+v, want round 1's failure alone: the halted round was never recorded", tr)
	}
	if got := triageRowKeys(rec); !eq(got, "l2:1:1", "l2:2:2", "l2:2:2") {
		t.Fatalf("the triage rows carry the keys %v, want round 1's row and the halted round 2's pair", got)
	}
	assertStoredRecordValidates(t, h.store)
	report := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil)
	line := "**Triage:** the judge did not answer for round 1 of level 2: " + answer + ", so the fail-safe blocked every finding the mode's table blocks."
	if !strings.Contains(report, line) {
		t.Errorf("the report lacks %q:\n%s", line, report)
	}
	rows := triageTableRows(t, report)
	if len(rows) != 3 {
		t.Fatalf("the report has %d triage rows, want the run's three attempts:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[0], "(failed: "+answer+")") {
		t.Errorf("round 1's own row = %q, want it marked with the failure its round recorded", rows[0])
	}
	for i, row := range rows[1:] {
		if strings.Contains(row, "(failed") {
			t.Errorf("row %d = %q, want no mark: it belongs to the round the run halted in, which the record never recorded", i+1, row)
		}
	}
}

// TestTriageRowCarriesItsOwnLevelsFailure: a run that reviewed at two levels
// records a triage round for each of them, and each level numbers its own
// rounds from 1 (D1847) — so level 1's round 1 and level 2's round 1 are two
// rounds whose own numbers agree, and a mark keyed on the number alone lands on
// both. Here level 1's judge buys no verdict and level 2's answers: the table
// must mark the level 1 row alone, because telling a reader that level 2's judge
// failed when it answered is the false failure claim the mark exists to avoid.
func TestTriageRowCarriesItsOwnLevelsFailure(t *testing.T) {
	const answer = "Invalid API key · Fix external API key"
	h := newHarness(t, startupCfg(t, 1, "  first_line_review: on\n"), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageTextStep(answer), // level 1, round 1: the judge bought no verdict
		fixStep,
		review("ship", 0),
		triageStep("defer"), // level 2, round 1: a verdict
	)
	if err != nil {
		t.Fatal(err)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want both levels' first rounds", tr)
	}
	if tr.Rounds[0].Round != 1 || tr.Rounds[0].Level != policy.LevelL1 || !tr.Rounds[0].Failed {
		t.Fatalf("the first round = %+v, want level 1's round 1 failed", tr.Rounds[0])
	}
	if tr.Rounds[1].Round != 1 || tr.Rounds[1].Level != policy.LevelL2 || tr.Rounds[1].Failed {
		t.Fatalf("the second round = %+v, want level 2's round 1 answered", tr.Rounds[1])
	}
	if got := triageRowKeys(rec); !eq(got, "l1:1:1", "l2:1:2") {
		t.Fatalf("the triage rows carry the keys %v, want one row per level's round 1", got)
	}
	assertStoredRecordValidates(t, h.store)
	report := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil)
	// The one line above the table names the round with its level: the number
	// alone names both of this run's rounds, and a reader who stops at the line
	// has nothing else to tell them apart (runstore.TriageFailureClause).
	line := "**Triage:** the judge did not answer for round 1 of level 1: " + answer + ", so the fail-safe blocked every finding the mode's table blocks."
	if !strings.Contains(report, line) {
		t.Errorf("the report lacks %q:\n%s", line, report)
	}
	rows := triageTableRows(t, report)
	if len(rows) != 2 {
		t.Fatalf("the report has %d triage rows, want one per level:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[0], "(failed: "+answer+")") {
		t.Errorf("level 1's row = %q, want it marked with the failure its round recorded", rows[0])
	}
	if strings.Contains(rows[1], "(failed") {
		t.Errorf("level 2's row = %q, want no mark: that level's judge answered, and only the number its round carries says otherwise", rows[1])
	}
}

// TestTriageRowCarriesItsOwnRoundsFailureAfterABounce: a bounce runs the cheap
// loop again from its round 1 (D727), so one run records two triage rounds that
// agree on both halves of the pair -- level 1, round 1 -- and a mark keyed on
// that pair lands on the row of the round whose judge answered. Here the first
// level 1 round buys no verdict and the bounced one answers: only the first
// round's row may carry the failure, and the line above the table names that
// round, while the second one is the bounce's own judge having answered.
func TestTriageRowCarriesItsOwnRoundsFailureAfterABounce(t *testing.T) {
	const answer = "Invalid API key · Fix external API key"
	cfg := startupCfg(t, 2, "  first_line_review: on\n  recipe: standard-bounce\n")
	h := newHarnessFiles(t, cfg, nil, bounceFiles)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageTextStep(answer), // level 1, round 1: the judge bought no verdict
		fixStep,
		reviewScored("changes", blockingFinding()),
		triageStep("block"), // level 2, round 1: a verdict
		fixStep,
		review("ship", 0),
		triageStep("defer"), // the bounce's level 1, round 1 again: a verdict
		review("ship", 0),
		triageStep("defer"), // level 2, round 2: a verdict
	)
	if err != nil {
		t.Fatal(err)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 4 {
		t.Fatalf("the record's triage block = %+v, want the run's four rounds", tr)
	}
	for i, rnd := range tr.Rounds {
		if rnd.Ordinal != i+1 {
			t.Errorf("round %d = %+v, want the ordinal %d", i, rnd, i+1)
		}
		if rnd.Failed != (i == 0) {
			t.Errorf("round %d = %+v, want only the first round failed", i, rnd)
		}
	}
	// The pair is not a key: the first round and the bounce's own level 1 round
	// agree on both halves of it, and the record's own order is what tells them
	// apart.
	if tr.Rounds[0].Level != tr.Rounds[2].Level || tr.Rounds[0].Round != tr.Rounds[2].Round {
		t.Fatalf("rounds 0 and 2 = %+v and %+v, want the level 1 round 1 a bounce records twice", tr.Rounds[0], tr.Rounds[2])
	}
	if got := triageRowKeys(rec); !eq(got, "l1:1:1", "l2:1:2", "l1:1:3", "l2:2:4") {
		t.Fatalf("the triage rows carry the keys %v, want the run's four rounds in its own order", got)
	}
	assertStoredRecordValidates(t, h.store)
	report := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil)
	line := "**Triage:** the judge did not answer for round 1 of level 1: " + answer + ", so the fail-safe blocked every finding the mode's table blocks."
	if !strings.Contains(report, line) {
		t.Errorf("the report lacks %q:\n%s", line, report)
	}
	rows := triageTableRows(t, report)
	if len(rows) != 4 {
		t.Fatalf("the report has %d triage rows, want the run's four rounds:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[0], "(failed: "+answer+")") {
		t.Errorf("the first round's row = %q, want it marked with the failure its round recorded", rows[0])
	}
	for i, row := range rows[1:] {
		if strings.Contains(row, "(failed") {
			t.Errorf("row %d = %q, want no mark: it belongs to a round whose judge answered, whatever round and level it judged", i+1, row)
		}
	}
}

// TestTriageCauseIsOneRedactedBoundedLine: the cause is a service's own
// sentence about the call, it is one line, and it reaches a record, a report
// and a terminal — so it is the call's first line, with this run's secrets and
// the credential shapes no value list can name taken out of it, clipped to the
// record's own bound (runstore.MaxTriageLine). A service's auth error quotes
// the credential it refused, which is exactly the key this run does not hold a
// copy of, and an agent's transcript carries its own account of itself over
// several lines.
func TestTriageCauseIsOneRedactedBoundedLine(t *testing.T) {
	token := "sk-" + strings.Repeat("Q", 32)
	text := "the upstream refused this run's key: " + token + " " + strings.Repeat("x", 300) + "\nand the trace below it is not the cause"
	h := newHarness(t, startupCfg(t, 1, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageTextStep(text),
		fixStep,
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 1 || !tr.Rounds[0].Failed {
		t.Fatalf("the record's triage block = %+v, want the one failed round", tr)
	}
	cause := tr.Rounds[0].Cause
	if strings.Contains(cause, token) || strings.Contains(cause, "sk-") {
		t.Errorf("the cause carries the credential the call quoted: %q", cause)
	}
	if !strings.Contains(cause, "[REDACTED]") {
		t.Errorf("the cause = %q, want the credential's shape replaced rather than the line dropped", cause)
	}
	if strings.Contains(cause, "trace below") || strings.Contains(cause, "\n") {
		t.Errorf("the cause = %q, want the call's first line alone", cause)
	}
	if got := utf8.RuneCountInString(cause); got != runstore.MaxTriageLine {
		t.Errorf("the cause is %d runes, want it clipped to %d", got, runstore.MaxTriageLine)
	}
}

// budgetedTriageCfg is the startup triage configuration of a run that has a
// gateway: gwRolesConfig's priced models and the fixture's own switches, with
// the triage stage on. The reviewer is on opus — a Claude model only Anthropic
// serves — which is the shape the skip exists for: a run whose gateway holds no
// Anthropic key can reach such a model through a stage account and through
// nothing else (config.IsClaudeModel).
func budgetedTriageCfg(t *testing.T, rounds int, extra string) string {
	t.Helper()
	cfg := gwRolesConfig(t)
	if !strings.Contains(cfg, "  review_rounds: 2\n") {
		t.Fatal("the configuration has no review_rounds line")
	}
	out := strings.Replace(cfg, "  review_rounds: 2\n", "  review_rounds: "+strconv.Itoa(rounds)+"\n"+extra, 1)
	return triageOn(t, "rigor:\n  default: startup\n"+out)
}

// triageAccountNode is a published policy that names an account for the triage
// stage's own key: rung 2 of the walk the stage's account comes from
// (resolveMixedStages). The key is the operator's (the published node is the
// runner's own copy of his project file), so a run reads it as the account the
// stage is to be served by.
func triageAccountNode(account string) string {
	return fmt.Sprintf(`{"version":%d,"claude_account_stages":{"triage":%q}}`, projectpolicy.Version, account)
}

// TestTriageWithNoAccountAndNoKeyIsSkippedNotAsked: the call this round would
// make is one the run already knows it cannot serve — the stage resolved to no
// Claude account, its model is a Claude one no route claims, and the run is the
// api-key one whose gateway holds no Anthropic key — so the stage is not started
// at all (triageUnserved).
//
// Three things follow, and each is a place a reader looks:
//
//   - No judge call is made. There is no step for it in the script because a
//     scripted agent fails a call no step answers, which is what the run used to
//     do here in a different way: the gateway refuses the call for the key the
//     run does not hold, and that refusal is a failed stage that ended the run
//     (TestTriageStageFailureEndsTheRun).
//   - The record says so: the round is Skipped, is not Failed, and carries the
//     one cause that names the fix (runstore.TriageCauseNoAccount). The
//     fail-safe settles the round — every finding the mode's table blocks blocks
//     — which is what makes the fix round that follows it the one it always was
//     (D1846).
//   - The pull request's status section, the stage table and the run's log say
//     it: the line above the table in the skip's own words, that round's row
//     marked rather than showing a duration that reads as a judge that ran, and
//     an ERROR naming the cause.
//
// Mutation (drop the skip branch in triageRound, or make triageUnserved answer
// false): the run makes the call after all, the scripted agent has no step for
// a call it never expected, and this case fails on the error the run returns —
// the fact it pins being that no such call is made.
func TestTriageWithNoAccountAndNoKeyIsSkippedNotAsked(t *testing.T) {
	var logs bytes.Buffer
	g := newGW(t, budgetedTriageCfg(t, 2, ""), "enforce", "50", out4k())
	// The run's gateway holds the provider key and no Anthropic one: the
	// upstream an unclaimed model reaches is the one with nothing behind it
	// (gatewayUpstream reads this variable and internal/gateway refuses the
	// call on it).
	g.deps.Env = filterEnv(g.deps.Env, "ANTHROPIC_API_KEY")
	g.deps.Log = runner.NewLogger(&logs)
	b := attachBackend(t, g, "enforce")
	// No step for the judge: a call the run does make has nothing to answer it,
	// and the scripted agent fails on it.
	rec, err := b.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		fixStep,
		review("ship", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, want the run the skipped judge did not end", rec)
	}
	if calls := triageCalls(b.agent.calls); len(calls) != 0 {
		t.Fatalf("the fake claude was asked %d triage questions, want none: the call is the one the run cannot serve", len(calls))
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want one round per review round", tr)
	}
	for _, rnd := range tr.Rounds {
		if !rnd.Skipped || rnd.Failed {
			t.Errorf("the round = %+v, want it skipped and not failed", rnd)
		}
		if rnd.Cause != runstore.TriageCauseNoAccount {
			t.Errorf("the round's cause = %q, want %q", rnd.Cause, runstore.TriageCauseNoAccount)
		}
	}
	first := tr.Rounds[0]
	if len(first.Entries) != 1 {
		t.Fatalf("the skipped round's entries = %+v, want the fail-safe's placement of the round's finding", first.Entries)
	}
	if e := first.Entries[0]; e.Verdict != runstore.VerdictBlock || e.Source != runstore.TriageSourceFailSafe {
		t.Errorf("the skipped round's entry = %+v, want the fail-safe's block and not a judge's placement", e)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "triage", "fix", "review", "triage") {
		t.Fatalf("stages = %v, want the skipped rounds recorded as the rows they stand in for", got)
	}
	// The rows and the rounds stay paired by the round's own ordinal, which is
	// what the table's marks are keyed on (report.go's triageRowStates): a
	// skipped round with no row would leave the mark of the round after it
	// nowhere to land.
	if got := triageRowKeys(rec); !eq(got, "l2:1:1", "l2:2:2") {
		t.Errorf("the triage rows carry the keys %v, want the two skipped rounds' own ordinals", got)
	}
	var entry map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if msg, _ := e["message"].(string); strings.Contains(msg, "triage round was skipped") {
			entry = e
			break
		}
	}
	if entry == nil {
		t.Fatalf("the run's log carries no line about the skipped round:\n%s", logs.String())
	}
	if entry["severity"] != "ERROR" || entry["cause"] != runstore.TriageCauseNoAccount {
		t.Errorf("the run's log line about the round = %v, want an ERROR naming the cause", entry)
	}
	got := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil)
	// The report is markdown, so the cause's own underscores are escaped in it
	// (inlineText): the two steps the cause names are the part a reader must see
	// intact, and the escape is what keeps the sentence from reading as
	// emphasis.
	escaped := strings.ReplaceAll(runstore.TriageCauseNoAccount, "_", `\_`)
	for _, want := range []string{
		"**Triage:** triage did not run for round 1 of level 2: " + escaped + "; triage did not run for round 2 of level 2: " + escaped + ", so the fail-safe blocked every finding the mode's table blocks.",
		"| triage (skipped: no account) |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report lacks %q:\n%s", want, got)
		}
	}
	rows := triageTableRows(t, got)
	if len(rows) != 2 {
		t.Fatalf("the report has %d triage rows, want the run's two rounds:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	for i, row := range rows {
		if !strings.Contains(row, "(skipped: no account)") {
			t.Errorf("row %d = %q, want it marked with the skip its round recorded", i, row)
		}
		// No call ran for the round, so the row shows no duration: a "0s" cell
		// beside the mark would read as a judge that answered instantly.
		if !strings.HasSuffix(strings.TrimSpace(row), "— |") {
			t.Errorf("row %d = %q, want the duration cell empty of a time", i, row)
		}
	}
	// The new fields are additive: a record carrying them is one the published
	// schema accepts.
	assertStoredRecordValidates(t, b.store)
}

// TestTriageWithAnAccountStillCalls: the skip is the account's absence and
// nothing else. A published node that names the account for the triage stage's
// own key gives the stage one, the run holds the token for it, and the judge is
// asked exactly as it was before the skip existed — the same two calls, the same
// answer read, and a round with neither flag.
//
// The run is keyless on purpose (ANTHROPIC_API_KEY filtered out, as the case
// above does it): the stage's account is then the *only* fact that keeps this
// call alive, since the gateway's key is the other condition the skip reads.
// Deleting the account guard in triageUnserved (stageAccount) turns both rounds
// into skips and fails here; leaving the key in the environment would keep the
// call alive through that guard instead, and this test would pass with the
// account guard gone.
func TestTriageWithAnAccountStillCalls(t *testing.T) {
	g := newGW(t, budgetedTriageCfg(t, 2, ""), "enforce", "50", out4k())
	g.deps.Env = append(filterEnv(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN"), "CLAUDE_CODE_OAUTH_TOKEN="+accountToken)
	b := publishPolicy(t, g, triageAccountNode("acme-overflow"))
	rec, err := b.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		// `block`, so the round's finding is the fix round's work and the loop
		// buys the second review: a deferral of every finding the round names
		// ends the loop there (T-TR7b), and this case is about the account and
		// not about that rule.
		triageStep("block"),
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls := triageCalls(b.agent.calls); len(calls) != 2 {
		t.Fatalf("the fake claude was asked %d triage questions, want both rounds: the account serves the stage", len(calls))
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want one round per review round", tr)
	}
	for _, rnd := range tr.Rounds {
		if rnd.Skipped || rnd.Failed {
			t.Errorf("the round = %+v, want an answered round", rnd)
		}
		if rnd.Account != "acme-overflow" {
			t.Errorf("the round's account = %q, want the account the node named", rnd.Account)
		}
	}
	if e := tr.Rounds[0].Entries[0]; e.Source != runstore.TriageSourceAgent {
		t.Errorf("the answered round's entry = %+v, want the judge's own placement", e)
	}
	if got := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil); strings.Contains(got, "skipped") {
		t.Errorf("the report calls a served round skipped:\n%s", got)
	}
}

// TestTriageOnARoutedClaudeModelStillCalls: the skip is about a call only an
// Anthropic key can serve, so a Claude model a provider route claims is not one
// — the model is the route's own ID ("anthropic/<id>", the one spelling a route
// may claim, config.CheckClaudeClaim), the gateway sends the call to the route
// on the route's credential, and the judge is asked as it was before the skip
// existed.
//
// The run is keyless and names no triage account, which is exactly the shape the
// case above skips on: the route is the one fact that keeps this call alive.
// Deleting the route guard in triageUnserved (config.ProviderFor) turns both
// rounds into skips and fails here.
func TestTriageOnARoutedClaudeModelStillCalls(t *testing.T) {
	cfg := budgetedTriageCfg(t, 2, "")
	// The triage stage runs on the senior reviewer's model, so the claim has to
	// be that role's pin: the claimed ID is a Claude one by IsClaudeModel and one
	// the route names by ProviderFor, which is the crossing this case is.
	cfg = strings.Replace(cfg, "reviewer: "+opus, "reviewer: "+claimedSenior, 1)
	if !strings.Contains(cfg, "reviewer: "+claimedSenior) {
		t.Fatal("the fixture has no reviewer line to put on the claimed ID")
	}
	g := newGW(t, cfg, "enforce", "50", out4k())
	wireRoutes(t, g, claimedRoutes(), claimPricesJSON, nil)
	g.deps.Env = filterEnv(g.deps.Env, "ANTHROPIC_API_KEY")
	b := attachBackend(t, g, "enforce")
	rec, err := b.run(t,
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
	if calls := triageCalls(b.agent.calls); len(calls) != 2 {
		t.Fatalf("the fake claude was asked %d triage questions, want both rounds: the route serves the stage", len(calls))
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want one round per review round", tr)
	}
	for _, rnd := range tr.Rounds {
		if rnd.Skipped || rnd.Failed {
			t.Errorf("the round = %+v, want an answered round: the route claims the model", rnd)
		}
	}
	if e := tr.Rounds[0].Entries[0]; e.Source != runstore.TriageSourceAgent {
		t.Errorf("the answered round's entry = %+v, want the judge's own placement", e)
	}
	if got := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil); strings.Contains(got, "skipped") {
		t.Errorf("the report calls a routed round skipped:\n%s", got)
	}
}

// TestTriageOnAVertexRunIsAsked: the key the skip looks for is the api-key
// auth's own, the one the gateway sends in the call's header. A Vertex run sends
// the job's Vertex identity instead — agent.BuildEnv's vertex arm sets
// CLAUDE_CODE_USE_VERTEX, ANTHROPIC_VERTEX_PROJECT_ID and
// CLAUDE_CODE_SKIP_VERTEX_AUTH and sets no ANTHROPIC_API_KEY — so the key's
// absence says nothing about whether its judge can be asked: it can, on every
// round, and no round of it is Skipped. The auth guard in triageUnserved
// (servedAuth) is what keeps those apart; without it every Vertex — and every
// cascade or switched — run with the stage on and no stage account would skip
// its judge on every round.
func TestTriageOnAVertexRunIsAsked(t *testing.T) {
	cfg := strings.Replace(budgetedTriageCfg(t, 2, ""), "auth: api-key", "auth: vertex", 1)
	if !strings.Contains(cfg, "auth: vertex") {
		t.Fatal("the fixture has no auth line to put the run on Vertex")
	}
	g := newGW(t, cfg, "observe", "", out4k())
	g.deps.Env = append(filterEnv(g.deps.Env, "ANTHROPIC_API_KEY"),
		"CLOUD_ML_REGION=us-east5", "ANTHROPIC_VERTEX_PROJECT_ID=proj-1234")
	g.deps.VertexTokens = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.TEST", TokenType: "Bearer"})
	b := attachBackend(t, g, "observe")
	rec, err := b.run(t,
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
	if calls := triageCalls(b.agent.calls); len(calls) != 2 {
		t.Fatalf("the fake claude was asked %d triage questions, want both rounds: the run's own Vertex credential serves the stage", len(calls))
	}
	tr := rec.Triage
	if tr == nil || len(tr.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want one round per review round", tr)
	}
	for _, rnd := range tr.Rounds {
		if rnd.Skipped || rnd.Failed {
			t.Errorf("the round = %+v, want an answered round: the key's absence on a Vertex run is not a stage nothing serves", rnd)
		}
	}
	if got := runner.Report(rec, "runs/acme-app/"+rec.RunID+"/", nil); strings.Contains(got, "skipped") {
		t.Errorf("the report calls a Vertex run's round skipped:\n%s", got)
	}
}
