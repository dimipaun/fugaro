package runner_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// championRunID is the champion of a reviewer pair (B1.2): a run ID of the
// shape run_id has, whose patch object is the one thing this arm reads of it.
const championRunID = "20260926-221530-beef"

// championLine is what the champion's patch adds to the fixture's README: the
// one change both reviewers of the pair read.
const championLine = "the champion's change"

// replayTask is a pair at a review slot: a challenger whose one difference is
// the slot level's reviewer, over the champion's own recorded patch (D728,
// B1.2). The champion's base is the fixture's main, which is where the
// launcher of a reviewer arm points the task's ref (B1.4).
func replayTask(slot task.ABSlot) *task.Spec {
	spec := armTask(challengerModel)
	spec.AB.Slot, spec.AB.ChampionRun = slot, championRunID
	return spec
}

// championChange is the champion's work: a line appended to the fixture's own
// README, so the patch is a change to a file the checkout already has.
func championChange(h *harness) map[string]string {
	return map[string]string{"README.md": h.files["README.md"] + championLine + "\n"}
}

// championPatch is a real diff of a change to the fixture's files, made in a
// clone of the fixture's own remote: the shape the champion's own upload
// writes (git diff between the champion's start and its head) and one git
// apply accepts.
func championPatch(t *testing.T, h *harness, change map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "champion")
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", h.remote, dir)
	testutil.WriteFiles(t, dir, change)
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "the champion's work")
	return testutil.Git(t, dir, "diff", "HEAD~1", "HEAD") + "\n"
}

// touchPatch is a patch that adds path with one line: the shape a patch takes
// when it writes a file, and what the fence reads its paths out of.
func touchPatch(path string) string {
	return "diff --git a/" + path + " b/" + path + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + path +
		"\n@@ -0,0 +1 @@\n+one line\n"
}

// putPatch stores patch as runID's own recorded patch object
// (runs/<slug>/<run-id>/ab/patch.diff), the object a reviewer arm reads under
// the champion's prefix and nothing else.
func putPatch(t *testing.T, h *harness, runID, patch string) {
	t.Helper()
	ctx := context.Background()
	w, err := h.bucket.NewWriter(ctx, runstore.PatchKey(runstore.Open(h.bucket, repoSlug, runID).Prefix()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(patch)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// putChampionPatch stores patch under the champion's own prefix.
func putChampionPatch(t *testing.T, h *harness, patch string) {
	t.Helper()
	putPatch(t, h, championRunID, patch)
}

// replayReview is the review round a reviewer arm's stage answers with: the
// verdict the pair's judge reads, and a line in the transcript it reads the
// findings out of (B1.3).
func replayReview(verdict string, findings int) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		fmt.Fprintf(req.Transcript, "{\"stage\":\"review\",\"verdict\":%q}\n", verdict)
		return review(verdict, findings)(t, ctx, req)
	}
}

// TestABReplayReviewsChampionPatch is B1.2's red step: a `slot: l2` challenger
// used to run the whole coding loop. It runs one review of the champion's
// recorded patch with the challenger model instead — no implement, no fix, no
// test — and files that round as the champion's own level-2 round, which is
// what the pair's judge matches the two finding sets by (D728).
func TestABReplayReviewsChampionPatch(t *testing.T) {
	g, _ := armGWSpec(t, armCfg(t), "50", replayTask(task.ABSlotL2), armLayer)
	withBucket(g.harness)
	putChampionPatch(t, g.harness, championPatch(t, g.harness, championChange(g.harness)))
	rec, err := g.run(t, replayReview("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.agent.calls) != 1 {
		t.Fatalf("the arm made %d agent call(s), want exactly one review of the champion's patch: a pair at a review slot runs no coding loop", len(g.agent.calls))
	}
	call := g.agent.calls[0]
	if call.Model != challengerModel {
		t.Fatalf("the review ran on %q, want the challenger %q: the slot level's reviewer is the arm's one difference (D728)", call.Model, challengerModel)
	}
	if !strings.Contains(call.Prompt, championLine) {
		t.Fatalf("the reviewer was not handed the champion's patch:\n%s", call.Prompt)
	}
	if !strings.Contains(call.Prompt, "Do not modify any files.") {
		t.Fatalf("the reviewer was not handed the review brief:\n%s", call.Prompt)
	}
	if len(rec.Reviews) != 1 {
		t.Fatalf("the record carries %d review(s), want the arm's one round: %+v", len(rec.Reviews), rec.Reviews)
	}
	sum := rec.Reviews[0]
	if sum.Tier != runstore.TierSenior || sum.Verdict != "changes" || sum.Findings != 1 || sum.Model != challengerModel {
		t.Fatalf("the round was recorded as %+v, want the senior tier's verdict on the challenger", sum)
	}
	// The round is filed under the stage the level itself runs, so a reader of
	// the pair finds the two finding sets under one name (B1.3).
	data, err := g.bucket.ReadAll(context.Background(), g.store.Prefix()+"transcripts/review-1.jsonl")
	if err != nil || !strings.Contains(string(data), `"verdict":"changes"`) {
		t.Fatalf("the round is not in transcripts/review-1.jsonl: %q, %v", data, err)
	}
}

// TestABReplayPublishesNothing: the replay is a challenger arm in everything
// the pair block promises (D554) — no push, no pull request, no comment, no
// credential that could push — and it reads the champion's patch with a
// read-only token alone.
func TestABReplayPublishesNothing(t *testing.T) {
	g, p := armGWSpec(t, armCfg(t), "50", replayTask(task.ABSlotL2), armLayer)
	withBucket(g.harness)
	putChampionPatch(t, g.harness, championPatch(t, g.harness, championChange(g.harness)))
	pushes := countPushes(t, g.harness)
	rec, err := g.run(t, replayReview("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if n := pushes(); n != 0 {
		t.Fatalf("the remote accepted %d push(es) from a reviewer arm", n)
	}
	if got := publishes(g.harness); len(got) != 0 {
		t.Fatalf("a reviewer arm called the provider: %v", got)
	}
	if remoteHasBranch(t, g.harness) {
		t.Fatal("the run branch reached the remote")
	}
	if rec.Outcome != runstore.OutcomeNone || rec.PushedHead != "" {
		t.Fatalf("record outcome = %q, pushed head = %q, want none and no tip", rec.Outcome, rec.PushedHead)
	}
	if rec.AB == nil || rec.AB.Arm != task.ABArmChallenger || rec.AB.Slot != task.ABSlotL2 || rec.AB.ChampionRun != championRunID {
		t.Fatalf("the record's pair block is %+v, want the challenger at l2 over %s", rec.AB, championRunID)
	}
	if writes, reads := p.counts(); writes != 0 || reads == 0 {
		t.Fatalf("the arm minted %d write credential(s) and %d read-only one(s), want none and at least one (A2.1)", writes, reads)
	}
}

// TestABReplayRefusesPatchFromOtherRun: the arm reads the patch under the
// champion's own prefix and nothing else (B1.2). A champion whose patch object
// is not there leaves the arm nothing to review, and a patch object under
// another run's prefix — this arm's own, which every launcher can write — is
// not read in its place.
func TestABReplayRefusesPatchFromOtherRun(t *testing.T) {
	cases := []struct {
		name  string
		patch func(t *testing.T, h *harness)
		want  string
	}{
		{
			name:  "the champion's patch is not there",
			patch: func(*testing.T, *harness) {},
			want:  "is not in the runs bucket",
		},
		{
			name: "another run's patch is",
			patch: func(t *testing.T, h *harness) {
				// The arm's own prefix: the object a run writes about itself,
				// which no reviewer arm may review in the champion's place.
				putPatch(t, h, runID, championPatch(t, h, championChange(h)))
			},
			want: "is not in the runs bucket",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := armGWSpec(t, armCfg(t), "50", replayTask(task.ABSlotL2), armLayer)
			withBucket(g.harness)
			tc.patch(t, g.harness)
			pushes := countPushes(t, g.harness)
			rec, err := g.run(t, replayReview("ship", 0))
			if err != nil {
				t.Fatalf("a refused replay returned %v, want nil: it is the arm's own ending", err)
			}
			if len(g.agent.calls) != 0 {
				t.Fatalf("a refused replay ran %d stage(s): nothing may be reviewed before the champion's patch is read", len(g.agent.calls))
			}
			if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone || rec.FailureClass != failure.ConfigInvalid {
				t.Fatalf("record = %s/%s/%s, want failed/none/%s", rec.Status, rec.Outcome, rec.FailureClass, failure.ConfigInvalid)
			}
			if !strings.Contains(rec.Reason, tc.want) || !strings.Contains(rec.Reason, championRunID) {
				t.Fatalf("reason = %q, want it to name %q and the champion %s", rec.Reason, tc.want, championRunID)
			}
			if n := pushes(); n != 0 {
				t.Fatalf("the remote accepted %d push(es) from a refused reviewer arm", n)
			}
		})
	}
}

// TestABReplayRefusesPatchTouchingAgentConfig: the patch is untrusted — any
// launcher, and the champion's own job agent, can write that object — so a
// patch that reaches a hook, an instruction file or a tool setting is refused
// before anything is applied or run (B1.2, D728). A path that merely reads
// like one is not refused: the fence is a set of path components, not a
// substring match.
//
// The fence reads each path the way git apply reads it, because the patch is
// what git is handed and a spelling git decodes is a path this run would carry:
// the quoted headers below name a refused file in bytes that hold none of its
// name (the review's finding of 2026-11-15), while an unquoted escape is the
// file's own name and not a refused one.
func TestABReplayRefusesPatchTouchingAgentConfig(t *testing.T) {
	cases := []struct {
		name string
		// path is the path the patch touches, as the patch spells it.
		path string
		// patch, when set, is the whole champion's patch — the header shape
		// of a rename or a copy, and the quoting and escaping of a path git's
		// own writer spells that way. touchPatch writes the rest.
		patch string
		// comp is the path component the fence refuses, "" when it refuses
		// nothing: the refusal names the component and not the file, so a
		// .claude/settings.json is refused as .claude.
		comp string
	}{
		{name: "the agent's own settings", path: ".claude/settings.json", comp: ".claude"},
		{name: "a plugin directory", path: ".claude-plugin/plugin.json", comp: ".claude-plugin"},
		{name: "a repository recipe", path: ".fugaro/recipes/deep.yaml", comp: ".fugaro"},
		{name: "a workflow", path: ".github/workflows/ci.yml", comp: ".github"},
		{name: "an ignore rule", path: ".gitignore", comp: ".gitignore"},
		{name: "a filter", path: ".gitattributes", comp: ".gitattributes"},
		{name: "the run's own configuration", path: "fugaro.yaml", comp: "fugaro.yaml"},
		{name: "an instruction file", path: "CLAUDE.md", comp: "CLAUDE.md"},
		{name: "a local instruction file", path: "CLAUDE.local.md", comp: "CLAUDE.local.md"},
		{name: "an MCP configuration", path: ".mcp.json", comp: ".mcp.json"},
		{name: "a nested instruction file", path: "nested/deep/CLAUDE.md", comp: "CLAUDE.md"},
		{name: "a path that merely reads like one", path: "docs/claude-notes.md"},
		{name: "a file beside a refused one", path: "src/clauderc.go"},
		{
			// The dot of .claude as an octal escape: git writes the name of a
			// path that needs escaping this way, and writes .claude/settings.json
			// when it applies this patch.
			name: "an escaped instruction file",
			path: `"b/\056claude/settings.json"`,
			patch: `diff --git "a/seed.txt" "b/\056claude/settings.json"
new file mode 100644
--- /dev/null
+++ "b/\056claude/settings.json"
@@ -0,0 +1 @@
+one line
`,
			comp: ".claude",
		},
		{
			// An escaped slash: the refused component is behind a separator the
			// patch spells in bytes rather than in "/".
			name: "an escaped separator",
			path: `"b/nested\057.github/workflows/ci.yml"`,
			patch: `diff --git "a/ci.yml" "b/nested\057.github/workflows/ci.yml"
new file mode 100644
--- /dev/null
+++ "b/nested\057.github/workflows/ci.yml"
@@ -0,0 +1 @@
+one line
`,
			comp: ".github",
		},
		{
			// A rename names two paths in its own header lines, and both of
			// them are read: a rename *to* a refused path must not escape the
			// fence because its diff --git line looks innocent.
			name: "a rename to an escaped recipe",
			path: `"b/\056fugaro/recipes/deep.yaml"`,
			patch: `diff --git a/recipes/deep.yaml "b/\056fugaro/recipes/deep.yaml"
similarity index 100%
rename from recipes/deep.yaml
rename to "b/\056fugaro/recipes/deep.yaml"
`,
			comp: ".fugaro",
		},
		{
			name: "a copy from an escaped plugin directory",
			path: `"a/\056claude-plugin/plugin.json"`,
			patch: `diff --git "a/\056claude-plugin/plugin.json" b/plugin.json
similarity index 100%
copy from "\056claude-plugin/plugin.json"
copy to plugin.json
`,
			comp: ".claude-plugin",
		},
		{
			// Quoted, and decoded to a name no component of the fence refuses:
			// the fence reads the escaped spelling rather than refusing every
			// header that holds a quote or an escape.
			name: "an escaped name that is not refused",
			path: `"b/\056claude-notes.md"`,
			patch: `diff --git "a/seed.txt" "b/\056claude-notes.md"
new file mode 100644
--- /dev/null
+++ "b/\056claude-notes.md"
@@ -0,0 +1 @@
+one line
`,
		},
		{
			// The same bytes unquoted: git takes them as they stand, so the
			// file it writes is named \056claude2 and not .claude2.
			name: "an unquoted escape is a name of its own",
			path: `b/\056claude2/x`,
			patch: `diff --git a/x b/\056claude2/x
new file mode 100644
--- /dev/null
+++ b/\056claude2/x
@@ -0,0 +1 @@
+one line
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := armGWSpec(t, armCfg(t), "50", replayTask(task.ABSlotL2), armLayer)
			withBucket(g.harness)
			patch := tc.patch
			if patch == "" {
				patch = touchPatch(tc.path)
			}
			putChampionPatch(t, g.harness, patch)
			rec, err := g.run(t, replayReview("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			if tc.comp == "" {
				// Not refused: the one review ran. The record's status is no
				// witness here — a replay runs no tests, so finalize ends even
				// a clean arm failed (no_verified_test) — so the refusal is told
				// by its own reason and by the stage that ran.
				if len(g.agent.calls) != 1 {
					t.Fatalf("a patch touching %s ran %d stage(s), want the one review", tc.path, len(g.agent.calls))
				}
				if strings.Contains(rec.Reason, "may not be replayed") || rec.FailureClass == failure.ConfigInvalid {
					t.Fatalf("the replay of a patch touching %s was refused: %s", tc.path, rec.Reason)
				}
				return
			}
			if len(g.agent.calls) != 0 {
				t.Fatalf("the replay of a patch touching %s ran %d stage(s), want none", tc.path, len(g.agent.calls))
			}
			if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone || rec.FailureClass != failure.ConfigInvalid {
				t.Fatalf("record = %s/%s/%s, want failed/none/%s", rec.Status, rec.Outcome, rec.FailureClass, failure.ConfigInvalid)
			}
			if !strings.Contains(rec.Reason, "may not be replayed") {
				t.Fatalf("reason = %q, want the fence's own refusal", rec.Reason)
			}
			if !strings.Contains(rec.Reason, tc.comp) {
				t.Fatalf("reason = %q, want it to name the component %s it refused", rec.Reason, tc.comp)
			}
		})
	}
}
