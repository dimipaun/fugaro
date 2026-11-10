package runner_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
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
func TestTriageRunsAfterEachReviewRound(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageStep("defer"),
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
	if e.Verdict != runstore.VerdictDefer || e.Source != runstore.TriageSourceAgent || e.Reason != "judged for this test" {
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

// TestShadowTriageDoesNotChangeTheFixPrompt is the shadow slice's own red
// step: the fixer is handed the round exactly as it was before the stage
// existed (FixPrompt of the review's verdict), whatever the judge answered.
func TestShadowTriageDoesNotChangeTheFixPrompt(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	_, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageStep("defer"),
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	finding, err := json.Marshal(map[string]any{"verdict": "changes", "findings": []any{blockingFinding()}})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := runner.TryVerdict(agent.Result{Structured: finding})
	if !ok {
		t.Fatal("the review's own answer does not read back as a verdict")
	}
	if len(h.agent.calls) < 4 {
		t.Fatalf("%d agent calls, want the implement, review, triage and fix of this run", len(h.agent.calls))
	}
	fix := h.agent.calls[3]
	if fix.Prompt != runner.FixPrompt(v) {
		t.Errorf("the fixer was handed something other than the review's own verdict:\n--- got ---\n%s\n--- want ---\n%s",
			fix.Prompt, runner.FixPrompt(v))
	}
	if strings.Contains(fix.Prompt, "judged for this test") || strings.Contains(fix.Prompt, "fugaro-triage") {
		t.Errorf("the judge's own text reached the fixer:\n%s", fix.Prompt)
	}
}

// TestShadowTriageWritesNoDeferred: what the judge deferred is in the record's
// triage block and nowhere else. The record's deferred set is empty — the
// stage T-TR7b is the one that acts on a verdict — and the pull request's body
// carries no deferred section.
func TestShadowTriageWritesNoDeferred(t *testing.T) {
	h := newHarness(t, startupCfg(t, 2, ""), nil)
	rec, err := h.run(t,
		implement("feature"),
		reviewScored("changes", blockingFinding()),
		triageStep("defer"),
		fixStep,
		review("ship", 0),
		triageStep("defer"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Triage == nil || len(rec.Triage.Rounds) != 2 {
		t.Fatalf("the record's triage block = %+v, want the two rounds this run judged", rec.Triage)
	}
	if rec.Deferred != nil {
		t.Errorf("the record's deferred set = %+v, want it untouched by the shadow stage", rec.Deferred)
	}
	pr := onlyPR(t, h.provider)
	if strings.Contains(pr.Body, "Deferred") || strings.Contains(pr.Body, "follow-up") {
		t.Errorf("the pull request's body carries a deferral the stage never acted on:\n%s", pr.Body)
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
