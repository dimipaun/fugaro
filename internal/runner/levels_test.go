package runner_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// levelsRecipe is a repository recipe shaped like the design's "standard"
// catalog recipe with level 3's step after it (docs/review-levels-ab-jobmax
// L4.2): the first line, the senior review that bounces back to it, and the
// adversarial step, which bounces the same way.
const levelsRecipe = "version: 1\nname: levels\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - adversarial: { bounce: first_line }\n"

// levelsFiles is the repository file map for levelsRecipe.
var levelsFiles = map[string]string{".fugaro/recipes/levels.yaml": levelsRecipe}

// modeCfg is the fixture config with extra agent lines and the repository's
// rigor mode set to mode. The block is what makes the repository opt in
// (rigor.Config.Configured) and the first layer the level resolver reads, so a
// case that wants a mode's own level row has to write it.
func modeCfg(t *testing.T, mode string, rounds int, extra string) string {
	t.Helper()
	cfg := firstLineCfg(t, rounds, extra)
	out := strings.Replace(cfg, "agent:\n", "rigor:\n  default: "+mode+"\nagent:\n", 1)
	if out == cfg {
		t.Fatal("the fixture has no agent: block to put rigor: before, so the repository would not opt in")
	}
	return out
}

// criticalCfg is modeCfg at Critical, the one mode whose row runs level 3
// (internal/rigor's defaultLevels).
func criticalCfg(t *testing.T, rounds int, extra string) string {
	t.Helper()
	return modeCfg(t, "critical", rounds, extra)
}

// levelsCfg is the Critical fixture with the levels recipe and level 3's own
// round count: what every case below that runs all three levels needs.
func levelsCfg(t *testing.T, extra string) string {
	t.Helper()
	return criticalCfg(t, 2, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 2\n"+extra)
}

// lastTier is the tier's last round in the record, read the way the runner's
// own lastRoundOf reads a level.
func lastTier(t *testing.T, rec *runstore.Record, tier string) runstore.ReviewSummary {
	t.Helper()
	for i := len(rec.Reviews) - 1; i >= 0; i-- {
		if rec.Reviews[i].Tier == tier {
			return rec.Reviews[i]
		}
	}
	t.Fatalf("the record holds no %s review: %v", tier, tiers(rec))
	return runstore.ReviewSummary{}
}

// firstTier is lastTier's first round instead of its last.
func firstTier(t *testing.T, rec *runstore.Record, tier string) runstore.ReviewSummary {
	t.Helper()
	for _, rs := range rec.Reviews {
		if rs.Tier == tier {
			return rs
		}
	}
	t.Fatalf("the record holds no %s review: %v", tier, tiers(rec))
	return runstore.ReviewSummary{}
}

// TestLevelsL3BounceSkipsL2: an L3 rejection runs the fix, then the L1 loop,
// then L3 again — never L2 (D727). The senior review shipped the head it read;
// asking it again about a head it never saw would be a second review of the
// change level 3 already rejected, and the level that decides readiness is
// level 3 from then on.
func TestLevelsL3BounceSkipsL2(t *testing.T) {
	h := newHarnessFiles(t, levelsCfg(t, ""), nil, levelsFiles)
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0),    // review_first
		review("ship", 0),    // review (senior)
		review("changes", 1), // adversarial round 1
		fixStep,
		review("ship", 0), // review_first, the bounce-back
		review("ship", 0), // adversarial round 2
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial", "fix", "review_first", "adversarial") {
		t.Fatalf("stages = %v", got)
	}
	if n := countStage(rec, "review"); n != 1 {
		t.Fatalf("senior reviews = %d, want 1: a level 3 rejection never goes back to level 2 (D727)", n)
	}
	if n := countStage(rec, "adversarial"); n != 2 {
		t.Fatalf("adversarial reviews = %d, want 2", n)
	}
	if got := tiers(rec); !eq(got, "first:ship", "senior:ship", "adversarial:changes", "first:ship", "adversarial:ship") {
		t.Fatalf("reviews = %v", got)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s, want ready (reason %q): every enabled level's last verdict is ship", rec.Outcome, rec.Reason)
	}
}

// TestLevelsL3BounceNeverDecidesWithoutL2: level 2 can be off while level 3 is
// on — a task writes `l2: none` under a repository that added level 3 — and
// then level 3 is the deciding level even though no level 2 round exists. The
// bounce level 3 sends the first line into must not become the round the mode's
// findings policy is read off: it would split level 1's findings against a
// level 3 round the mode never tolerated, and the run would report level 1's
// finding as the one it left open.
//
// The run here stops inside the bounce — its fix stage fails, which ends the
// loop before level 3 is asked for another round — so the last round left
// behind is the bounce's own. Stopping there is the point: a level 3 round
// after the bounce would set the deciding round itself, whatever the first line
// did, and a stop before the bounce would leave level 3's own round last. Only
// a stop in between reads the first line's round, and only the run's own
// condition decides what that round does to readiness.
//
// A run that stops is a draft either way — finalize reports ready for no halt,
// failure or cancel — so the record's tolerated findings are where the wrong
// round shows: level 1's finding is one Startup tolerates, and level 3's
// unscored one blocks in every mode (D4).
func TestLevelsL3BounceNeverDecidesWithoutL2(t *testing.T) {
	cfg := modeCfg(t, "startup", 1, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 2\n")
	spec := &task.Spec{
		Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature",
		ReviewLevels: &config.ReviewLevels{
			L2: &config.LevelSetting{Off: true},
			L3: &config.LevelSetting{Model: "senior-model"},
		},
	}
	h := newHarnessFiles(t, cfg, spec, levelsFiles)
	// Startup's table tolerates this finding and blocks the unscored one
	// (TestDecideToleratedRoundOpensAReadyPR, D4).
	tolerated := map[string]any{"summary": "fix it", "likelihood": 2, "impact": 2, "fix_cost": 1, "class": "correctness"}
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0),                  // review_first: level 1's own round
		review("changes", 1),               // adversarial round 1: an unscored finding, which blocks in every mode (D4)
		fixStep,                            // its fix
		reviewScored("changes", tolerated), // review_first: the bounce, the round that must not decide
		failStep,                           // the bounce's fix fails: the run stops inside the bounce
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "adversarial", "fix", "review_first", "fix") {
		t.Fatalf("stages = %v", got)
	}
	if n := countStage(rec, "adversarial"); n != 1 {
		t.Fatalf("adversarial reviews = %d, want 1: the run stops inside the bounce, before level 3 runs again", n)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %s, want draft (reason %q)", rec.Outcome, rec.Reason)
	}
	if rec.Tolerated != nil {
		t.Errorf("the record's tolerated findings = %+v, want none: level 3 is the deciding level, and the finding it left blocks in every mode (D4)", rec.Tolerated)
	}
}

// TestLevelsReadinessHighestLevel: readiness is decided by the highest enabled
// level that ran, and only when every enabled outer level's last verdict is
// ship (D727, G4). At Critical all three levels run, so the rule can end a run
// two ways: a lower level that did not ship holds a shipped level 3 back, and
// level 3's own verdict is the one readiness reads.
func TestLevelsReadinessHighestLevel(t *testing.T) {
	t.Run("a lower level that did not ship holds the pull request back", func(t *testing.T) {
		h := newHarnessFiles(t, levelsCfg(t, ""), nil, levelsFiles)
		rec, err := h.run(t,
			implement("feature"),
			review("ship", 0),    // review_first
			review("changes", 1), // review, round 1
			fixStep,
			review("ship", 0),    // review_first, the bounce
			review("changes", 1), // review, round 2: the senior rounds are spent
			review("ship", 0),    // adversarial: level 3 ships
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := tiers(rec); !eq(got, "first:ship", "senior:changes", "first:ship", "senior:changes", "adversarial:ship") {
			t.Fatalf("reviews = %v", got)
		}
		if rec.Outcome != runstore.OutcomeDraft {
			t.Fatalf("outcome = %s, want draft: level 3 shipped and level 2 did not", rec.Outcome)
		}
		for _, want := range []string{"review level 2 last shipped nothing", "round 2 left changes", "until every enabled review level ships"} {
			if !strings.Contains(rec.Reason, want) {
				t.Errorf("reason = %q, want it to say %q", rec.Reason, want)
			}
		}
	})
	t.Run("the highest level's own verdict decides", func(t *testing.T) {
		h := newHarnessFiles(t, levelsCfg(t, ""), nil, levelsFiles)
		rec, err := h.run(t,
			implement("feature"),
			review("ship", 0),    // review_first
			review("ship", 0),    // review: level 2 ships
			review("changes", 1), // adversarial round 1
			fixStep,
			review("ship", 0),    // review_first, the bounce
			review("changes", 1), // adversarial round 2: level 3's last verdict
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := tiers(rec); !eq(got, "first:ship", "senior:ship", "adversarial:changes", "first:ship", "adversarial:changes") {
			t.Fatalf("reviews = %v", got)
		}
		if rec.Outcome != runstore.OutcomeDraft {
			t.Fatalf("outcome = %s, want draft: level 3 is the deciding level and it did not ship", rec.Outcome)
		}
		if want := "review round 2 still has 1 finding"; !strings.Contains(rec.Reason, want) {
			t.Errorf("reason = %q, want %q: level 2 shipped, and readiness reads the level above it", rec.Reason, want)
		}
	})
}

// TestLevelsPOCL1Decides: POC's own row turns level 2 off and leaves level 1
// on (rigor.defaultLevels), so the cheap loop is the highest level the run
// holds and its verdict is the one readiness reads (D723). It is the one mode
// where a first-line review decides on its own; everywhere else level 1 only
// advises.
func TestLevelsPOCL1Decides(t *testing.T) {
	t.Run("a shipped first line is ready", func(t *testing.T) {
		cfg := modeCfg(t, "poc", 1, "  first_line_review: on\n  recipe: levels\n")
		h := newHarnessFiles(t, cfg, nil, levelsFiles)
		rec, err := h.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatal(err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review_first") {
			t.Fatalf("stages = %v: level 2 does not run at POC", got)
		}
		if n := countStage(rec, "review"); n != 0 {
			t.Fatalf("senior reviews = %d, want none", n)
		}
		if rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
			t.Fatalf("outcome = %s (reason %q), want ready: the first line is the decider", rec.Outcome, rec.Reason)
		}
	})
	t.Run("a first line that asks for changes is a draft", func(t *testing.T) {
		cfg := modeCfg(t, "poc", 1, "  first_line_review: on\n  recipe: levels\n")
		h := newHarnessFiles(t, cfg, nil, levelsFiles)
		rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep)
		if err != nil {
			t.Fatal(err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review_first", "fix") {
			t.Fatalf("stages = %v", got)
		}
		if rec.Outcome != runstore.OutcomeDraft {
			t.Fatalf("outcome = %s, want draft: the deciding level asked for changes", rec.Outcome)
		}
		if want := "review round 1 still has 1 finding"; !strings.Contains(rec.Reason, want) {
			t.Errorf("reason = %q, want %q", rec.Reason, want)
		}
	})
}

// TestLevelsBootstrapRefusesBelowFloor: the owner's floor is a requirement and
// not a veto, so a task that writes `none` for a level the floor of the run's
// mode requires is refused rather than quietly kept on (G1, D722). A launcher
// writes the task, so the refusal is the whole point: the runner resolves the
// levels again at bootstrap, and a task written straight to the runs bucket
// meets the same check the launcher's own does (L3.2).
func TestLevelsBootstrapRefusesBelowFloor(t *testing.T) {
	b := policyRun(t, criticalCfg(t, 2, "  first_line_review: on\n"), "enforce", "5",
		`{"version":4,"review_floor":{"critical":["l2"]}}`)
	spec := &task.Spec{
		Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature",
		ReviewLevels: &config.ReviewLevels{L2: &config.LevelSetting{Off: true}},
	}
	if err := b.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	_, err := b.run(t)
	if err == nil {
		t.Fatal("the task turned level 2 off under an owner's floor that requires it, and the run was accepted")
	}
	for _, want := range []string{"bootstrap", "review levels: l2: none is refused", "critical"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to say %q", err, want)
		}
	}
	if n := len(b.agent.calls); n != 0 {
		t.Errorf("the run made %d agent call(s): the refusal comes before any stage", n)
	}
	rec, rerr := b.store.ReadRecord(context.Background())
	if rerr != nil {
		t.Fatal(rerr)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Errorf("record = %s/%s, want infra_error/none", rec.Status, rec.Outcome)
	}
}

// TestLevelsBootstrapRefusesSameModelTask: the same-model rule spans two levels
// (D721) and the run resolves the task's own block at bootstrap, so a task that
// names one model for level 1 and level 2 is refused before a stage runs. The
// launcher refuses it too (L3.2); this is the half that keeps a task written
// straight to the runs bucket from running the senior review on the model that
// wrote the change.
func TestLevelsBootstrapRefusesSameModelTask(t *testing.T) {
	const shared = "coder-model"
	spec := &task.Spec{
		Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature",
		ReviewLevels: &config.ReviewLevels{
			L1: &config.LevelSetting{Model: shared},
			L2: &config.LevelSetting{Model: shared},
		},
	}
	h := newHarnessFiles(t, criticalCfg(t, 2, "  first_line_review: on\n"), spec, nil)
	_, err := h.run(t)
	if err == nil {
		t.Fatalf("the task ran level 1 and level 2 both on %s and was accepted", shared)
	}
	for _, want := range []string{"bootstrap", "l2.model and l1.model are both " + shared, "D721"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to say %q", err, want)
		}
	}
	if n := len(h.agent.calls); n != 0 {
		t.Errorf("the run made %d agent call(s): the refusal comes before any stage", n)
	}
}

// TestLevelsFocusIsQuotedAndRecorded: a level's focus is data a launcher wrote,
// and it reaches the reviewer as quoted text under a heading of the run's own,
// through safetext like every other untrusted string the run puts in front of a
// model (FocusPrompt). It is the adversarial level's own, so no other stage's
// prompt carries it, and the pull request's levels line records it beside the
// verdict it belongs to.
func TestLevelsFocusIsQuotedAndRecorded(t *testing.T) {
	const focus = "the retry path and the backoff on the second attempt"
	h := newHarnessFiles(t, levelsCfg(t, "  levels:\n    l3: {focus: \""+focus+"\"}\n"), nil, levelsFiles)
	rec, err := h.run(t, implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial") {
		t.Fatalf("stages = %v", got)
	}
	var focused []int
	for i, c := range h.agent.calls {
		if strings.Contains(c.Prompt, "## Review focus") {
			focused = append(focused, i)
		}
	}
	if len(focused) != 1 || focused[0] != 3 {
		t.Fatalf("calls carrying the focus heading = %v, want the adversarial stage's alone (call 3)", focused)
	}
	prompt := h.agent.calls[3].Prompt
	for _, want := range []string{"## Review focus", "> " + focus} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the adversarial prompt does not carry %q:\n%s", want, prompt)
		}
	}
	// The heading is the run's own text and the focus is the line under it: a
	// focus that read as an instruction is quoted rather than joined into the
	// prompt's own sentences.
	if !strings.Contains(prompt, "## Review focus\n\n> ") {
		t.Errorf("the focus does not sit under the heading as a quoted line:\n%s", prompt)
	}
	if got := lastTier(t, rec, runstore.TierAdversarial).Verdict; got != "ship" {
		t.Fatalf("the adversarial round's verdict = %q, want ship", got)
	}
	if want := "(focus: " + focus + ")"; !strings.Contains(onlyPR(t, h.provider).Body, want) {
		t.Errorf("the pull request's levels line does not record the focus (%q):\n%s", want, onlyPR(t, h.provider).Body)
	}
}

// TestLevelsFocusShownForLevel3Only: a focus is level 3's alone — only its
// reviewer is given one (AdversarialPrompt, levelFocus) — so the pull request's
// levels line shows the focus beside the level whose reviewer received it and
// no other. A focus a layer wrote for level 1 or level 2 reaches no model, and
// the line must not claim the round was asked for it.
func TestLevelsFocusShownForLevel3Only(t *testing.T) {
	h := newHarnessFiles(t, levelsCfg(t, "  levels:\n    l1: {focus: \"the cheap path\"}\n    l2: {focus: \"the contract\"}\n"), nil, levelsFiles)
	rec, err := h.run(t, implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	body := onlyPR(t, h.provider).Body
	if strings.Contains(body, "(focus:") {
		t.Errorf("the levels line shows a focus no reviewer was given:\n%s", body)
	}
	for _, want := range []string{"level 1: ship in round 1", "level 2: ship in round 1", "level 3: ship in round 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("the levels line does not name %q:\n%s", want, body)
		}
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial") {
		t.Fatalf("stages = %v", got)
	}
}

// TestLevelsFocusPromptSanitises: FocusPrompt is where a level's focus becomes
// text a model reads, and it does two things to it — it runs it through
// safetext, so an escape sequence, a bidi control or a zero-width character
// cannot make the text read as something other than what it is, and it quotes
// every line under a heading of the run's own. The resolver refuses those
// characters in a layer's focus (config.LevelSetting.validate), so the
// sanitising half is unreachable through a run and is pinned here.
func TestLevelsFocusPromptSanitises(t *testing.T) {
	if got := runner.FocusPrompt(""); got != "" {
		t.Errorf("FocusPrompt(\"\") = %q, want \"\": a level with no focus adds nothing to the prompt", got)
	}
	got := runner.FocusPrompt("watch\x1b[31mthe​retry path")
	if !strings.Contains(got, "## Review focus") {
		t.Errorf("the focus is not under the fixed heading:\n%q", got)
	}
	if !strings.Contains(got, "> watchthe?retry path") {
		t.Errorf("the sanitised focus is not quoted as one line:\n%q", got)
	}
	for _, c := range got {
		if c == 0x1b || c == 0x200b {
			t.Errorf("the prompt still carries U+%04X: the focus did not go through safetext:\n%q", c, got)
		}
	}
	// A multi-line focus is quoted line by line rather than joined into one
	// line: the resolver refuses a line break in a layer's focus too, so this
	// is the builder's own shape rather than a value a run reaches it with.
	if got := runner.FocusPrompt("first\nsecond"); !strings.Contains(got, "> first\n> second") {
		t.Errorf("a two-line focus is not quoted line by line:\n%q", got)
	}
	// The adversarial prompt is the level's own question with the focus quoted
	// under it and the senior brief after both: the focus is data the reviewer
	// reads and never a replacement for the run's own prompt.
	prompt := runner.AdversarialPrompt("SENIOR BRIEF", "the retry path")
	if !strings.Contains(prompt, "## Review focus\n\n> the retry path") || !strings.Contains(prompt, "SENIOR BRIEF") {
		t.Errorf("AdversarialPrompt = %q, want the focus quoted under its heading and the senior brief kept", prompt)
	}
}

// TestLevelsPRBodyShowsHeads: the pull request lists each level's verdict and
// the head it read (D727). After a level 3 rejection the fix moves the branch
// and level 2 is never asked again, so its line names an earlier commit than
// level 3's — which is the whole reason the head is recorded, and the run is
// still ready because every enabled level's last verdict is ship.
func TestLevelsPRBodyShowsHeads(t *testing.T) {
	h := newHarnessFiles(t, levelsCfg(t, ""), nil, levelsFiles)
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0),    // review_first
		review("ship", 0),    // review: level 2 ships the head it read
		review("changes", 1), // adversarial round 1
		fixStep,
		review("ship", 0), // review_first, the bounce
		review("ship", 0), // adversarial round 2
	)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s, want ready (reason %q)", rec.Outcome, rec.Reason)
	}
	senior, first3, last3 := lastTier(t, rec, runstore.TierSenior), firstTier(t, rec, runstore.TierAdversarial), lastTier(t, rec, runstore.TierAdversarial)
	if senior.Head == "" || first3.Head == "" || last3.Head == "" {
		t.Fatalf("a review round recorded no head: %v", tiers(rec))
	}
	if senior.Head != first3.Head {
		t.Fatalf("level 2 read %s and level 3's first round read %s: nothing changed the branch between them", senior.Head, first3.Head)
	}
	if last3.Head == senior.Head {
		t.Fatalf("the fix did not move the head (%s), so this case cannot tell the two levels apart", senior.Head)
	}
	first := lastTier(t, rec, runstore.TierFirst)
	body := onlyPR(t, h.provider).Body
	for _, want := range []string{
		"review levels:",
		"level 1: ship in round " + strconv.Itoa(first.Round) + " on `" + first.Head[:7] + "`",
		"level 2: ship in round " + strconv.Itoa(senior.Round) + " on `" + senior.Head[:7] + "`",
		"level 3: ship in round " + strconv.Itoa(last3.Round) + " on `" + last3.Head[:7] + "`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the pull request does not say %q:\n%s", want, body)
		}
	}
}

// TestLevelsAdversarialEffortOnAClaudeStageOnly: a level's effort reaches its
// stage only where that stage is Claude's (L4.3's EffortVars). A stage on a
// provider route runs with thinking off (MAX_THINKING_TOKENS=0), so the
// variable is dropped rather than sent as a setting that would look applied and
// do nothing (D725). The route is read off the stage's own model and the run's
// background one (stageOnProviderRoute), and the resolver refuses an effort on
// a model a provider serves, so the background is the one way a run can reach
// this with a level effort set at all.
func TestLevelsAdversarialEffortOnAClaudeStageOnly(t *testing.T) {
	const effort = "high"
	// The models are priced IDs and level 2 has one of its own: a budgeted run
	// prices what it pins, and the same-model rule refuses the reviewer's own
	// model twice (D721). Level 3 takes the reviewer's, which level 1 does not.
	cfg := gwConfig(t, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 2\n"+
		"  levels:\n    l2: {model: "+opus+"}\n    l3: {effort: "+effort+"}\n")
	cfg = strings.Replace(cfg, "agent:\n", "rigor:\n  default: critical\nagent:\n", 1)

	t.Run("a Claude stage takes it", func(t *testing.T) {
		g := newGWFiles(t, cfg, "observe", "50", levelsFiles)
		rec, err := g.run(t, implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
		if err != nil || rec.Outcome != runstore.OutcomeReady {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial") {
			t.Fatalf("stages = %v", got)
		}
		if got := envValue(g.agent.calls[3].Env, "CLAUDE_CODE_EFFORT_LEVEL"); got != effort {
			t.Errorf("the adversarial stage's CLAUDE_CODE_EFFORT_LEVEL = %q, want the level's own %q", got, effort)
		}
		// Level 2 names no effort of its own, and a level's effort is its own:
		// neither the senior review nor the first line carries it.
		for _, i := range []int{1, 2} {
			if got := envValue(g.agent.calls[i].Env, "CLAUDE_CODE_EFFORT_LEVEL"); got != "" {
				t.Errorf("call %d's CLAUDE_CODE_EFFORT_LEVEL = %q, want it unset", i, got)
			}
		}
	})
	t.Run("a provider stage drops it", func(t *testing.T) {
		routed := strings.Replace(cfg, "background: "+haiku, "background: "+deepseek, 1)
		if routed == cfg {
			t.Fatal("the fixture's background model moved, so the run's background is not the provider's")
		}
		g := newGWFiles(t, routed, "enforce", "50", levelsFiles)
		wireProviders(t, g, nil, "acme/app")
		rec, err := g.run(t, implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
		if err != nil || rec.Outcome != runstore.OutcomeReady {
			t.Fatalf("rec = %+v, err = %v: a provider-served background is not a refusal", rec, err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial") {
			t.Fatalf("stages = %v", got)
		}
		if got := envValue(g.agent.calls[3].Env, "CLAUDE_CODE_EFFORT_LEVEL"); got != "" {
			t.Errorf("the adversarial stage's CLAUDE_CODE_EFFORT_LEVEL = %q, want it unset: a provider serves %s, and a provider stage runs with thinking off", got, deepseek)
		}
	})
}

// TestLevelsRecordNoEffortWhenTheStageIsOnAProviderRoute: a level that names a
// reasoning effort records it only when that effort reached the stage. A stage
// on a provider route raises no thinking at all (G5, D725) — its pins carry the
// route's variables, not the effort — so EffortVars answers nothing for it and
// the round records the empty effort, the same statement the stage's own
// environment makes, while the model it did run on is still the level's own.
//
// The route here is the *background* model's, which is what makes the case
// reachable at all: a level whose own model a provider serves and which names an
// effort is refused at bootstrap (config's D725 check), but a level on a Claude
// model in a run whose background model the route serves is an ordinary
// configuration, and its stage is on the route like any other (stagePins). This
// is the whole reason reviewFacts asks EffortVars instead of printing the
// level's effort back: without the route check the record would claim a
// reasoning level the provider's calls never ran at.
func TestLevelsRecordNoEffortWhenTheStageIsOnAProviderRoute(t *testing.T) {
	const effort = "high"
	cfg := gwConfig(t, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 2\n"+
		"  levels:\n    l2: {model: "+opus+"}\n    l3: {effort: "+effort+"}\n")
	cfg = strings.Replace(cfg, "agent:\n", "rigor:\n  default: critical\nagent:\n", 1)
	// The same configuration TestLevelsRecordTheReviewersModelAndEffort runs,
	// but for one line: the background model is the provider's, so every stage
	// of the run is on the route. That pair is the case — one line apart, the
	// level's own effort is recorded or is not.
	cfg = strings.Replace(cfg, "background: "+haiku, "background: "+deepseek, 1)
	if !strings.Contains(cfg, "background: "+deepseek) {
		t.Fatal("the fixture has no background model to put on the route")
	}
	// enforce, not observe: a provider's calls are real dollars, so the run
	// refuses to make them with a cap that only watches (checkProviderModels).
	g := newGWFiles(t, cfg, "enforce", "50", levelsFiles)
	// The owner's provider claims deepseek/*, which is the background's model.
	wireProviders(t, g, nil, "acme/app")
	rec, err := g.run(t, implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	// Level 2 names a model of its own and no effort, so what it records about
	// the route is the model it ran on and not an effort.
	if senior := lastTier(t, rec, runstore.TierSenior); senior.Model != opus || senior.Effort != "" {
		t.Errorf("the senior review recorded model %q, effort %q, want %q and none", senior.Model, senior.Effort, opus)
	}
	adv := lastTier(t, rec, runstore.TierAdversarial)
	if adv.Model != sonnet {
		t.Errorf("the adversarial review recorded model %q, want the reviewer slot's %q", adv.Model, sonnet)
	}
	if adv.Effort != "" {
		t.Errorf("the adversarial review recorded effort %q, want none: the level named %q and the stage's route runs with thinking off", adv.Effort, effort)
	}
	// The line the pull request carries says the same: the model, no effort at
	// all, and the head the round read.
	body := onlyPR(t, g.provider).Body
	want := "adversarial review: ship in round " + strconv.Itoa(adv.Round) + " (recorded: model " + sonnet + ", at " + adv.Head[:7] + ")"
	if !strings.Contains(body, want) {
		t.Errorf("the pull request does not say %q:\n%s", want, body)
	}
}

// TestLevelsRecordTheReviewersModelAndEffort: each review round's summary keeps
// what reviewed it — the model the stage was pinned to and the reasoning effort
// that reached it (CLI 0.16.0) — beside the head it read, and the pull request's
// review line prints the three. The two are read off the stage's own pins, so a
// level with a model of its own records that one, a level that names no effort
// records none, and the effort a level names on a Claude stage is the value the
// stage's environment carries.
func TestLevelsRecordTheReviewersModelAndEffort(t *testing.T) {
	const effort = "high"
	cfg := gwConfig(t, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 2\n"+
		"  levels:\n    l2: {model: "+opus+"}\n    l3: {effort: "+effort+"}\n")
	cfg = strings.Replace(cfg, "agent:\n", "rigor:\n  default: critical\nagent:\n", 1)
	g := newGWFiles(t, cfg, "observe", "50", levelsFiles)
	rec, err := g.run(t, implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	first, senior, adv := lastTier(t, rec, runstore.TierFirst), lastTier(t, rec, runstore.TierSenior), lastTier(t, rec, runstore.TierAdversarial)
	for _, tc := range []struct {
		who    string
		got    runstore.ReviewSummary
		model  string
		effort string
	}{
		// Level 1 runs the coder's model through the coder's slot, and names
		// no effort of its own.
		{"first-line", first, sonnet, ""},
		// Level 2 names a model of its own and no effort.
		{"senior", senior, opus, ""},
		// Level 3 takes the reviewer's slot and the level's effort.
		{"adversarial", adv, sonnet, effort},
	} {
		if tc.got.Model != tc.model || tc.got.Effort != tc.effort {
			t.Errorf("the %s review recorded model %q, effort %q, want %q and %q", tc.who, tc.got.Model, tc.got.Effort, tc.model, tc.effort)
		}
		if tc.got.Head == "" {
			t.Errorf("the %s review recorded no head, so this case cannot say which commit it read", tc.who)
		}
	}
	if first.Head != senior.Head || senior.Head != adv.Head {
		t.Fatalf("nothing moved the branch between the three rounds, yet they read %q, %q and %q", first.Head, senior.Head, adv.Head)
	}
	// The plan's last round is level 3's, so its facts are the ones the pull
	// request's review line carries.
	body := onlyPR(t, g.provider).Body
	want := "adversarial review: ship in round " + strconv.Itoa(adv.Round) + " (recorded: model " + sonnet + ", effort " + effort + ", at " + adv.Head[:7] + ")"
	if !strings.Contains(body, want) {
		t.Errorf("the pull request does not say %q:\n%s", want, body)
	}
}
