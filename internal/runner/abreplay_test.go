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
//
// That reading of the headers is not the one that decides, and the next two
// cases are the reviews' findings of 2026-11-16: git's older `rename old`/
// `rename new` header is applied by git and named by no prefix the header
// reading holds, and a quote git never closes is read by git from the line's
// own bytes while the header reading keeps the whole remainder of the line. Both
// patches write a refused path and both are refused by the fence's second
// reading — the paths git wrote, listed from git once the patch is applied —
// which is why the reviewer below runs no stage in either case.
//
// The case after them is what that second reading missed: it listed the paths a
// commit would carry, and a path the base's own .gitignore excludes is in the
// checkout, in the reviewer's reach and in no commit at all. The listing now
// takes git status as well, and that case is the one that fails without it.
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
		// files, when set, are files the base repository carries, over the
		// fixture's own: the .gitignore below is what makes the refused path
		// one no commit of this checkout carries.
		files map[string]string
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
			// git's older rename spelling, which git applies and the fence's
			// prefix list does not hold: the only header the first reading
			// recognizes is the innocent diff --git line, so the fence's
			// first reading returns "" and the path git writes is named by no
			// header it looks at. The second reading — the paths git wrote —
			// is what refuses it.
			name: "a rename header git applies and the first reading does not hold",
			path: "CLAUDE.md",
			patch: `diff --git a/README.md b/README.md
similarity index 100%
rename old README.md
rename new CLAUDE.md
`,
			comp: "CLAUDE.md",
		},
		{
			// A quote git never closes: git gives up on the name and reads the
			// line's own bytes, dropping the first path part, so the file it
			// writes is CLAUDE.md while the fence's reader takes the whole
			// remainder of the line — the trailing newline included — as the
			// name, which matches no component. Refused by the paths git wrote.
			name:  "a quoted path git never closes",
			path:  "CLAUDE.md",
			patch: "diff --git \"a/q b/q\nnew file mode 100644\n--- /dev/null\n+++ \"b/CLAUDE.md\n@@ -0,0 +1 @@\n+one line\n",
			comp:  "CLAUDE.md",
		},
		{
			// The same rename header as the case above, to a path the base's own
			// .gitignore excludes: git apply writes it, and `git add -A` stages
			// nothing for it, so a fence that reads the commit's own listing —
			// the second reading before the review of 2026-11-16 — names it
			// never and the reviewer runs with the file in its checkout. The
			// rename is what makes the case bite: the patch stages README.md's
			// deletion, so the arm has a change to commit and the review really
			// runs, where a patch writing the ignored file alone would have been
			// refused for changing nothing. What refuses it is the listing of
			// the paths git status names, ignored and untracked alike.
			name: "a rename to a path the base ignores",
			path: "CLAUDE.local.md",
			files: map[string]string{
				".gitignore": "build/\nnode_modules/\nCLAUDE.local.md\n",
			},
			patch: `diff --git a/README.md b/README.md
similarity index 100%
rename old README.md
rename new CLAUDE.local.md
`,
			comp: "CLAUDE.local.md",
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
			g, _ := armGWFiles(t, armCfg(t), "50", replayTask(task.ABSlotL2), armLayer, tc.files)
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

// TestABReplayQuotesTheRefusedComponent: the refusal names a component out of
// the patch's own bytes, and those bytes are untrusted -- a C-quoted header
// spells them in escapes, so a name may carry a newline, an escape sequence or
// the whole MaxPatchBytes -- so the reason carries it folded onto one line and
// clipped, the treatment a stage's own error text gets (stageErrorText).
// Anything that can write the champion's patch object could otherwise put a
// line break, a control sequence or a wall of text into the run record, the run
// log and the owner's watch output.
func TestABReplayQuotesTheRefusedComponent(t *testing.T) {
	g, _ := armGWSpec(t, armCfg(t), "50", replayTask(task.ABSlotL2), armLayer)
	withBucket(g.harness)
	// A refused prefix, a newline, and 4 KiB past it: the patch spells the name
	// as a C-quoted header does, so the bytes the fence decodes are these.
	name := `"b/.git\n` + strings.Repeat("A", 4<<10) + `"`
	putChampionPatch(t, g.harness, "diff --git "+name+" "+name+"\nnew file mode 100644\n--- /dev/null\n+++ "+name+"\n@@ -0,0 +1 @@\n+one line\n")
	rec, err := g.run(t, replayReview("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.agent.calls) != 0 {
		t.Fatalf("a patch touching .git ran %d stage(s), want none", len(g.agent.calls))
	}
	if !strings.Contains(rec.Reason, ".git?") {
		t.Fatalf("reason = %q, want the refused component in it, folded onto one line", rec.Reason)
	}
	if strings.ContainsAny(rec.Reason, "\n\r\x1b") {
		t.Fatalf("reason = %q, want no line break or escape out of the patch's own bytes", rec.Reason)
	}
	if strings.Contains(rec.Reason, strings.Repeat("A", 400)) {
		t.Fatalf("reason = %q, want the component clipped to one log line", rec.Reason)
	}
}

// TestABReplayQuotesGitsRefusal: the apply's own failure carries git's message,
// which quotes the paths the patch named and runs to several lines, so the
// reason carries it the same way -- folded onto one line (stageErrorText) --
// rather than as git wrote it.
func TestABReplayQuotesGitsRefusal(t *testing.T) {
	g, _ := armGWSpec(t, armCfg(t), "50", replayTask(task.ABSlotL2), armLayer)
	withBucket(g.harness)
	// A patch written for another base: its hunk changes a line of the fixture's
	// README that is not there, which is what git refuses and reports on.
	putChampionPatch(t, g.harness, "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1,1 +1,1 @@\n-a line the fixture does not have\n+the champion's change\n")
	rec, err := g.run(t, replayReview("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.agent.calls) != 0 {
		t.Fatalf("a patch that does not apply ran %d stage(s), want none", len(g.agent.calls))
	}
	if rec.Status != runstore.StatusFailed || rec.FailureClass != failure.ConfigInvalid {
		t.Fatalf("record = %s/%s, want failed/%s", rec.Status, rec.FailureClass, failure.ConfigInvalid)
	}
	if !strings.Contains(rec.Reason, "does not apply to this checkout") || !strings.Contains(rec.Reason, "patch failed") {
		t.Fatalf("reason = %q, want the apply's refusal carrying git's own message", rec.Reason)
	}
	if strings.ContainsAny(rec.Reason, "\n\r") {
		t.Fatalf("reason = %q, want git's message folded onto one line", rec.Reason)
	}
}

// TestABReplayRefusesALevelTheRunDoesNotRun: a reviewer arm's one review is the
// slot level's, and a level the run itself does not run has no stage to replay
// it in — the champion's own run made no round there either, so the pair would
// compare a round with nothing (applyReplayLevel). The fixture's rigor is the
// default mode, whose row runs the senior level and leaves the adversarial one
// unset, so a slot at level 3 is the shape of it: the arm is refused by name at
// bootstrap, before the champion's patch object is even read, and the level is
// never forced on — forcing it would hand the pair two rounds that are not the
// same round, which is the comparison the arm exists to make.
func TestABReplayRefusesALevelTheRunDoesNotRun(t *testing.T) {
	g, _ := armGWSpec(t, armCfg(t), "50", replayTask(task.ABSlotL3), armLayer)
	withBucket(g.harness)
	putChampionPatch(t, g.harness, championPatch(t, g.harness, championChange(g.harness)))
	rec, err := g.run(t, replayReview("ship", 0))
	if err == nil {
		t.Fatal("a task replaying a level the run does not run was run")
	}
	if len(g.agent.calls) != 0 {
		t.Fatalf("a refused replay ran %d stage(s), want none", len(g.agent.calls))
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone || rec.FailureClass != failure.ConfigInvalid {
		t.Fatalf("record = %s/%s/%s, want failed/none/%s", rec.Status, rec.Outcome, rec.FailureClass, failure.ConfigInvalid)
	}
	for _, want := range []string{"review level l3", "does not run that level"} {
		if !strings.Contains(rec.Reason, want) {
			t.Fatalf("reason = %q, want it to carry %q", rec.Reason, want)
		}
	}
}
