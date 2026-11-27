package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runindex"
)

// TestAgentMayMaintain is AG5a: the maintenance commands are an agent's too
// (D3425). `recipes publish` and `rm`, `runs reindex`, `issue sync` and
// `dismiss`, and `stats runs` carry no session fence any more, so a session
// runs each one and it does its work rather than refusing with the marker's
// name.
//
// Every subtest drives the command through its own fixture and asserts the
// work landed -- the object published and removed, the job started, the issue
// uploaded and set aside, the figures printed -- because "no refusal" is not
// the acceptance: the command has to get past the guard and finish.
func TestAgentMayMaintain(t *testing.T) {
	t.Run("recipes publish", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, nil)
		noAgentSession(t)
		t.Setenv("CLAUDECODE", "1")
		path := writeRecipeFile(t, t.TempDir(), "solo.yaml", projectSoloCatalogName)

		out, _, err := execute(t, "recipes", "publish", path)
		if err != nil {
			t.Fatalf("an agent's session may run recipes publish: %v", err)
		}
		if !strings.Contains(out, "published solo to ") {
			t.Errorf("publish does not say what it published:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("solo")))); err != nil {
			t.Errorf("nothing reached the project's store: %v", err)
		}
	})

	t.Run("recipes rm", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
		noAgentSession(t)
		t.Setenv("CLAUDECODE", "1")

		if _, _, err := execute(t, "recipes", "rm", "team"); err != nil {
			t.Fatalf("an agent's session may run recipes rm: %v", err)
		}
		if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team")))); err == nil {
			t.Error("the recipe survived fugaro recipes rm")
		}
	})

	t.Run("runs reindex", func(t *testing.T) {
		f, fs := reindexRig(t)
		noAgentSession(t)
		t.Setenv("CLAUDECODE", "1")

		out, _, err := execute(t, "runs", "reindex")
		if err != nil {
			t.Fatalf("an agent's session may run runs reindex: %v", err)
		}
		if !strings.Contains(out, "index: marked incomplete") {
			t.Errorf("the command does not say what it marked: %q", out)
		}
		if got := len(f.run.RunRequests()); got != 1 {
			t.Errorf("the command started %d executions, want one", got)
		}
		if _, ok := fs.Value(runindex.SummaryCollection, runindex.SummaryDocument); !ok {
			t.Error("the command wrote no summary, so no sweep would walk")
		}
	})

	t.Run("issue sync", func(t *testing.T) {
		rig := newMineRig(t)
		rig.save(mineRec("1", "sig-a"))
		noAgentSession(t)
		t.Setenv("CLAUDECODE", "1")

		out, errOut, err := rig.sync()
		if err != nil {
			t.Fatalf("an agent's session may run issue sync: %v\n%s%s", err, out, errOut)
		}
		if !strings.Contains(out, "Uploaded 1 issue(s).") {
			t.Errorf("sync does not say what it uploaded:\n%s", out)
		}
	})

	t.Run("issue dismiss", func(t *testing.T) {
		rig := newMineRig(t)
		rec := mineRec("1", "sig-a")
		up := rig.upload(rec, rig.caller)
		local := rec
		local.Number, local.Uploaded = up.Number, true
		rig.save(local)
		noAgentSession(t)
		t.Setenv("CLAUDECODE", "1")

		out, errOut, err := rig.run("issue", "dismiss", mineRefOf(up.Number))
		if err != nil {
			t.Fatalf("an agent's session may run issue dismiss: %v\n%s%s", err, out, errOut)
		}
		if !strings.Contains(out, "Dismissed "+mineRefOf(up.Number)) {
			t.Errorf("dismiss does not name the issue it set aside:\n%s", out)
		}
		shared, err := issues.SharedIssue(t.Context(), rig.bucket, up.Number)
		if err != nil {
			t.Fatalf("reading the shared issue: %v", err)
		}
		if shared.Status != issues.StatusDismissed {
			t.Errorf("the project's copy is %q, want %q", shared.Status, issues.StatusDismissed)
		}
	})

	t.Run("stats runs", func(t *testing.T) {
		f := newCloudFixture(t)
		var seeds []statsSeed
		for i := range 21 {
			seeds = append(seeds, statsSeed{recipe: "standard", duration: time.Duration(600+i*60) * time.Second, cost: 1 + float64(i)/10})
		}
		seedStatsRuns(t, f, seeds, time.Now().UTC())
		noAgentSession(t)
		t.Setenv("CLAUDECODE", "1")

		out, _, err := execute(t, "stats", "runs", "--project", "aurora", "--json")
		if err != nil {
			t.Fatalf("an agent's session may run stats runs: %v", err)
		}
		doc := map[string]any{}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		groups, _ := doc["recipes"].([]any)
		if len(groups) != 1 {
			t.Fatalf("the figures name %d groups, want the one recipe: %s", len(groups), out)
		}
		if group, _ := groups[0].(map[string]any); group["recipe"] != "standard" {
			t.Errorf("group = %v", groups[0])
		}
	})
}
