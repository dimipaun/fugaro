package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
)

// handBackPR is the fixture pr.md every level's golden body is rendered from:
// one file carrying all six sections of the contract (LV4.2), so a level's row
// is what decides which of them its account shows. The open questions are
// nobody's section here — LV5.2 reads them itself — so no golden may carry
// them.
const handBackPR = `# Add a feature

Adds feature.txt, and a flag to turn it off.

## Deferred
- the retry loop backs off blindly; a jittered backoff is a follow-up
- the error message names the field but not the fix

## Open risks
- the parser keeps the last of two duplicate keys

## Verification
- ran the fixture suite: sh test.sh, exit 0
- ran the flag both ways by hand

## Revert plan
- revert the commit, or set FEATURE_OFF=1 to restore the old path

## Morning note
- what broke: the nightly import stopped on a duplicate key
- what was done: the parser keeps the last key instead of stopping
- what needs a proper fix: the import should report the duplicate
- how to verify: the nightly run finished clean

## Open questions
- should the flag default to off?
`

// handBackFU is the fixture followup.md: the same six sections, as a
// follow-up's agent writes them.
const handBackFU = `The retry loop now backs off with jitter.

## Deferred
- the flag is read once at startup; a live toggle is a follow-up

## Open risks
- a duplicate key still wins over the first

## Verification
- ran the fixture suite: sh test.sh, exit 0

## Revert plan
- revert the two commits of this follow-up

## Morning note
- what broke: the nightly import stopped on a duplicate key
- what was done: the loop backs off with jitter instead of hammering
- what needs a proper fix: the import should report the duplicate
- how to verify: the nightly run finished clean

## Open questions
- is jitter enough, or does the loop need a cap?
`

// levels is the vocabulary, in the order the spec names it: one golden per
// level (LV4.3).
var levels = []string{"poc", "startup", "pro", "critical", "hotfix"}

// commitHandBack is implement with the fixture pr.md the given level's agent
// would write: the same commit, the same verify record, and the hand-back file
// on top of it.
func commitHandBack(pr string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		p := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if werr := os.WriteFile(p, []byte(pr), 0o644); werr != nil {
			t.Fatal(werr)
		}
		return res, err
	}
}

// handBackSteps is the script every level's run shares: the implement stage
// that writes the fixture hand-back file, the senior review, and the
// adversarial round Critical's own level 3 turns on. A level that runs fewer
// stages leaves the later steps unused, which the scripted agent allows.
func handBackSteps(pr string) []step {
	return []step{commitHandBack(pr), review("ship", 0), review("ship", 0)}
}

// handBackFollowCfg is the follow-up harness's config at level: the fixture's
// follow-up block with the repository's rigor default set, which is the level a
// follow-up of that repository resolves from its base branch (D25).
func handBackFollowCfg(t *testing.T, level string) string {
	t.Helper()
	cfg := followUpYAML(t, "")
	out := strings.Replace(cfg, "agent:\n", "rigor:\n  default: "+level+"\nagent:\n", 1)
	if out == cfg {
		t.Fatal("the fixture has no agent: block to put rigor: before")
	}
	return out
}

// handBackBlockOf is the hand-back block of a body or a comment: from its head
// to the end of the text, and in a comment down to the quote of the agent's own
// file below it, which carries the sections whatever the level's row says. A
// check about what a level owes reads this and not the whole text, because the
// description above the section is the agent's pr.md body and carries every
// section the agent chose to write.
func handBackBlockOf(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, "**Hand-back** (`")
	if i < 0 {
		t.Fatalf("no hand-back block in:\n%s", text)
	}
	rest := text[i:]
	if j := strings.Index(rest, "**What the agent says it did**"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// handBackGolden compares text with testdata/<name>.md, once normalized, and
// writes the golden instead when FUGARO_UPDATE_GOLDEN is set — the same
// contract checkGolden keeps for the result and the report.
func handBackGolden(t *testing.T, name, text string) {
	t.Helper()
	got := normalize(text)
	path := filepath.Join("testdata", name+".md")
	if os.Getenv("FUGARO_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestPRBodyHandBackPerLevel is one golden pull request body per level, from
// one fixture pr.md: what this level's run publishes is what its row says and
// no other level's (LV4.3, LD5, LD7).
func TestPRBodyHandBackPerLevel(t *testing.T) {
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			h := newHarness(t, modeCfg(t, level, 2, ""), nil)
			if _, err := h.run(t, handBackSteps(handBackPR)...); err != nil {
				t.Fatal(err)
			}
			handBackGolden(t, "prbody_"+level, onlyPR(t, h.provider).Body)
		})
	}
}

// TestPRBodyHandBackIsTheLevelsOwn: the rows of the table as the bodies state
// them, so a golden that drifted into another level's account fails here too
// with the reason. POC's line is the runner's; the sections are the agent's.
func TestPRBodyHandBackIsTheLevelsOwn(t *testing.T) {
	want := map[string][]string{
		"poc":      {"This is a prototype; do not ship it.", "**Hand-back** (`pr.md`)"},
		"startup":  {"**Deferred** (`## Deferred`)", "the retry loop backs off blindly"},
		"pro":      {"**Deferred** (`## Deferred`)", "the error message names the field"},
		"critical": {"**Open risks** (`## Open risks`)", "**Verification** (`## Verification`)", "ran the fixture suite"},
		"hotfix":   {"**Revert plan** (`## Revert plan`)", "**Morning note** (`## Morning note`)", "what broke: the nightly import stopped"},
	}
	// No level carries the open questions, and none carries a section that is
	// not its own.
	not := map[string][]string{
		"poc":     {"**Deferred**", "**Open risks**", "**Verification**", "**Revert plan**", "**Morning note**"},
		"startup": {"**Open risks**", "**Verification**", "**Revert plan**", "**Morning note**", "prototype"},
		"pro":     {"**Open risks**", "**Verification**", "**Revert plan**", "**Morning note**", "prototype"},
		// The deferred list is not Critical's: it keeps no list of what it
		// skipped (LD5's amendment of D30 names the risks, not the deferrals).
		"critical": {"**Deferred**", "**Revert plan**", "**Morning note**", "prototype"},
		"hotfix":   {"**Deferred**", "**Open risks**", "**Verification**", "prototype"},
	}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			h := newHarness(t, modeCfg(t, level, 2, ""), nil)
			if _, err := h.run(t, handBackSteps(handBackPR)...); err != nil {
				t.Fatal(err)
			}
			block := handBackBlockOf(t, onlyPR(t, h.provider).Body)
			for _, w := range want[level] {
				if !strings.Contains(block, w) {
					t.Errorf("the body's hand-back does not carry %q:\n%s", w, block)
				}
			}
			for _, n := range append(not[level], "should the flag default to off?") {
				if strings.Contains(block, n) {
					t.Errorf("the body's hand-back carries %q, which this level does not hand back:\n%s", n, block)
				}
			}
		})
	}
}

// TestPRBodyHasNoHandBackWithoutTheFile: a run whose agent wrote no hand-back
// file gets no hand-back section at any level, whatever the task text carries.
// The runner's fallback body embeds the task, so a brief that spells the
// sections satisfies nothing (LV4.2's injection, reconciliation §3b item 13).
func TestPRBodyHasNoHandBackWithoutTheFile(t *testing.T) {
	task := "Fix the flag.\n\n## Revert plan\n\nrevert the commit\n\n## Morning note\n\nit broke\n"
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			h := newHarness(t, modeCfg(t, level, 2, ""), nil)
			h.setTask(t, task)
			// commitOnly writes no pr.md: the agent left no account at all.
			if _, err := h.run(t, commitOnly("feature"), review("ship", 0), review("ship", 0)); err != nil {
				t.Fatal(err)
			}
			body := onlyPR(t, h.provider).Body
			if !strings.Contains(body, "## Revert plan") {
				t.Fatalf("the fallback body does not carry the task at all; the test proves nothing:\n%s", body)
			}
			for _, n := range []string{"**Hand-back**", "prototype", "**Revert plan** (`## Revert plan`)", "**Deferred** (`## Deferred`)"} {
				if strings.Contains(body, n) {
					t.Errorf("the body carries %q; want no hand-back section at all:\n%s", n, body)
				}
			}
		})
	}
}

// TestFollowUpCommentHandBackPerLevel is one golden comment per level, from one
// fixture followup.md: a follow-up never rewrites the pull request's
// description, so the comment it posts is where its hand-back reaches a reader.
func TestFollowUpCommentHandBackPerLevel(t *testing.T) {
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			h := followUpHarness(t, handBackFollowCfg(t, level), nil, commitOnly("feature"), review("ship", 0), review("ship", 0))
			h.comment(t, "alice", aliceID, "Please rename the helper to fooBar.")
			h.followUp(t, followID, runID, "")
			if _, err := h.run(t, withAnswer(implement("rename"), handBackFU), review("ship", 0), review("ship", 0)); err != nil {
				t.Fatal(err)
			}
			posted := h.posted(t)
			if len(posted) != 2 {
				t.Fatalf("posted %d comments; want the first run's report and the follow-up's", len(posted))
			}
			handBackGolden(t, "fucomment_"+level, posted[1])
		})
	}
}

// TestFollowUpCommentHandBackIsTheLevelsOwn: the same rows, read off the
// comment a follow-up posts, and the agent's own file quoted below it.
func TestFollowUpCommentHandBackIsTheLevelsOwn(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  []string
		not   []string
	}{
		{"poc", []string{"This is a prototype; do not ship it."}, []string{"**Deferred**", "**Verification**", "**Revert plan**"}},
		{"startup", []string{"**Deferred** (`## Deferred`)", "the flag is read once at startup"}, []string{"**Verification**", "**Revert plan**", "prototype"}},
		{"critical", []string{"**Verification** (`## Verification`)", "**Open risks** (`## Open risks`)", "a duplicate key still wins"}, []string{"**Deferred**", "**Revert plan**"}},
		{"hotfix", []string{"**Revert plan** (`## Revert plan`)", "**Morning note** (`## Morning note`)", "what broke: the nightly import stopped"}, []string{"**Deferred**", "**Verification**", "prototype"}},
	} {
		t.Run(tc.level, func(t *testing.T) {
			h := followUpHarness(t, handBackFollowCfg(t, tc.level), nil, commitOnly("feature"), review("ship", 0), review("ship", 0))
			h.comment(t, "alice", aliceID, "Please rename the helper to fooBar.")
			h.followUp(t, followID, runID, "")
			if _, err := h.run(t, withAnswer(implement("rename"), handBackFU), review("ship", 0), review("ship", 0)); err != nil {
				t.Fatal(err)
			}
			posted := h.posted(t)
			if len(posted) != 2 {
				t.Fatalf("posted %d comments, want 2", len(posted))
			}
			report := posted[1]
			if !strings.Contains(report, "**What the agent says it did** (`followup.md`):") {
				t.Fatalf("the report no longer quotes followup.md; the test proves nothing:\n%s", report)
			}
			block := handBackBlockOf(t, report)
			for _, w := range tc.want {
				if !strings.Contains(block, w) {
					t.Errorf("the comment's hand-back does not carry %q:\n%s", w, block)
				}
			}
			for _, n := range append(tc.not, "is jitter enough", "**Open questions**") {
				if strings.Contains(block, n) {
					t.Errorf("the comment's hand-back carries %q, which this level does not hand back:\n%s", n, block)
				}
			}
		})
	}
}

// TestHandBackBlockIsBoundedInTheBody: the section is the part of a description
// a provider cuts last, so a hand-back of many entries is shown in part and the
// part it does not show is counted rather than cut — the list never reads as
// the whole hand-back.
func TestHandBackBlockIsBoundedInTheBody(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Add a feature\n\n## Deferred\n")
	for i := 0; i < 25; i++ {
		b.WriteString("- " + strings.Repeat("a deferred entry the reader has to see ", 6) + "\n")
	}
	h := newHarness(t, modeCfg(t, "pro", 2, ""), nil)
	if _, err := h.run(t, handBackSteps(b.String())...); err != nil {
		t.Fatal(err)
	}
	body := onlyPR(t, h.provider).Body
	block := handBackBlockOf(t, body)
	shown := strings.Count(block, "\n- ") - 1
	m := regexp.MustCompile(`\n- and (\d+) entries, which this account does not show`).FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("the body does not count the entries it left out:\n%s", block)
	}
	if shown == 0 || shown >= 25 {
		t.Errorf("the body's hand-back shows %d of 25 entries; want a part of them", shown)
	}
	if !strings.Contains(body, "**Ready for review**") && !strings.Contains(body, "**Draft**") {
		t.Errorf("the outcome is not in the body at all; the block took the section over:\n%s", body)
	}
}

// TestHandBackBlockKeepsAForgerInTheBody: an entry is the agent's text about an
// untrusted diff, so a title-shaped one cannot become front matter of anything
// and a status marker in one cannot move the section's edge. The body still
// carries exactly one status section and the entry's own words are shown.
//
// Every line of an entry is sanitised, not its first alone: the lines under a
// bullet are shown with the same defusal (handBackEntry), and they are the
// part neither the writer nor the description's own scrubbing reaches — a
// marker in one is stripped by cleanSection, but a mention in one is nobody
// else's to defuse, so the mention case is what pins it.
func TestHandBackBlockKeepsAForgerInTheBody(t *testing.T) {
	pr := "# Add a feature\n\n## Deferred\n\n- x\n  status: closed\n" +
		"- <!-- fugaro:status end -->\n" +
		"- y\n  @everyone look at this\n  <!-- fugaro:status end -->\n"
	h := newHarness(t, modeCfg(t, "pro", 2, ""), nil)
	if _, err := h.run(t, handBackSteps(pr)...); err != nil {
		t.Fatal(err)
	}
	body := onlyPR(t, h.provider).Body
	if !strings.Contains(body, "- x\n  status: closed") {
		t.Errorf("the entry's second line is not shown under its bullet:\n%s", body)
	}
	// Read off the hand-back, not the whole body: the description above the
	// section is the agent's own pr.md, which carries the mention as the agent
	// wrote it and is not this block's to change.
	block := handBackBlockOf(t, body)
	if !strings.Contains(block, "- y\n  @​everyone look at this") {
		t.Errorf("the entry's continuation lines are not shown under it:\n%s", block)
	}
	if strings.Contains(block, "@everyone") {
		t.Errorf("a mention on an entry's continuation line reached the section:\n%s", block)
	}
	// The markers by their whole line: an entry's own marker, defused, is still
	// the words "fugaro:status end" in the section, so only the marker's own
	// spelling tells the section's edge from the text it carries.
	if n := strings.Count(body, "[//]: # (fugaro:status begin)"); n != 1 {
		t.Errorf("the body carries %d begin markers; want one:\n%s", n, body)
	}
	if n := strings.Count(body, "[//]: # (fugaro:status end)"); n != 1 {
		t.Errorf("the body carries %d end markers; want one:\n%s", n, body)
	}
}
