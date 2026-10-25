package cli

import (
	"os"
	"strings"
	"testing"
)

// sourceOfFunc returns the source text of the top-level function fn in path.
// The closing brace of a top-level function is a line of its own ("}" at
// column 0) in a gofmt'd file, which is what ends the slice.
func sourceOfFunc(t *testing.T, path, fn string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	i := strings.Index(src, "func "+fn+"(")
	if i < 0 {
		t.Fatalf("%s has no func %s: the publisher was restructured, and this test's code tie needs a new one", path, fn)
	}
	rest := src[i:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("the source of %s in %s does not end with a top-level closing brace", fn, path)
	}
	return rest[:end]
}

// TestDocsProjectPolicyPublishAfterDeleteReseedsTheList pins the rollout step a
// security review of PR #642 found: both operator pages tell an operator whose
// published node is an older version to run `fugaro project policy publish
// --delete` and then `fugaro project policy publish`, and with no node to
// replace the second publish has no list to keep. It seeds allow_repos from the
// installation's config (publishAllowRepos, whose seed branch is seedAllowRepos),
// so a repository removed with `fugaro project repos rm` comes back when the
// config still names it and one added with `fugaro project repos add` is
// dropped; the node's clone_urls block and a hand-written routing block go with
// it, because buildProjectPolicy and policyNodeBytes carry those only from a
// node that exists; and a shared job's runs halt in between (the runner's
// matchJobName refuses a project job its published policy does not define).
//
// What the pages therefore have to tell the operator is the order: read the
// list first with `fugaro project policy show` and keep it, and repair it after
// the publish from the diff the publish prints. Each page is checked for each
// half, and the mechanism behind every half is read out of the code here, so a
// change on either side fails and names the other:
//
//   - docs/project-claude-accounts.md §5 — the operator guide's rollout;
//   - docs/gcp-setup.md's "The Version 3 rollout" step 2 — the runbook;
//   - docs/project-layer.md's version-2 paragraph, beside the hand-added half.
//
// Mutation (verified, run and restored): change publishAllowRepos to answer from
// the config seed even when a node exists (drop "&& !seed"), or drop any of the
// sentences below, and this fails naming the page or the function.
func TestDocsProjectPolicyPublishAfterDeleteReseedsTheList(t *testing.T) {
	// The code ties, one per claim the pages make.
	publish := sourceOfFunc(t, "project_cmd.go", "publishAllowRepos")
	for _, want := range []string{"if old != nil && !seed", "seedAllowRepos(ctx, lc)"} {
		if !strings.Contains(publish, want) {
			t.Errorf("publishAllowRepos no longer seeds from the config only when no node exists (%q is gone): the pages' \"the publish that follows rebuilds the list\" is now wrong, and docs/project-claude-accounts.md §5, docs/gcp-setup.md's rollout step 2 and docs/project-layer.md state it", want)
		}
	}
	build := sourceOfFunc(t, "project_cmd.go", "buildProjectPolicy")
	if !strings.Contains(build, "if old != nil {") || !strings.Contains(build, "pol.CloneURLs = old.CloneURLs") {
		t.Error("buildProjectPolicy carries clone_urls without the old-node guard (or dropped the assignment): the pages' \"clone_urls goes with the delete\" is now wrong")
	}
	node := sourceOfFunc(t, "project_cmd.go", "policyNodeBytes")
	if !strings.Contains(node, "if !exists {") || !strings.Contains(node, "return node, nil") {
		t.Error("policyNodeBytes no longer returns the freshly built node when there is none to replace: the pages' \"clone_urls and a hand-written routing block go with the delete\" is now wrong")
	}
	runner, err := os.ReadFile("../../internal/runner/projectjob.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runner), "This project has published no policy, so it defines no project job at all") {
		t.Error("the runner no longer refuses a run in a job its published policy does not define with that sentence: the pages' \"a shared job's runs halt in between\" is now wrong")
	}

	// And the pages themselves.
	pages := []struct {
		path string
		want []string
	}{
		{projectClaudeAccountsDoc, []string{
			"fugaro project policy publish --delete",
			"fugaro project policy show",
			"**Read the list out first: the publish that follows rebuilds it.**",
			"seeds `allow_repos` from this machine's config",
			"`seedAllowRepos`",
			"a repository you had removed with `fugaro project repos rm` comes back when the config still names it",
			"one you had added by hand with `fugaro project repos add` is dropped",
			"`clone_urls` block",
			"any `routing` block a person wrote are dropped with it",
			"repair the list with `fugaro project repos add <owner/name>` and `fugaro project repos rm <owner/name>`",
			"Runs of a shared job halt in between",
		}},
		{"docs/project-layer.md", []string{
			"reading the list you are about to lose with `fugaro project policy show`",
			"a publish with no node seeds the list from your config",
			"a repository you had added by hand (`fugaro project repos add`) is dropped",
			"one you had removed (`fugaro project repos rm`) comes back when the config still names it",
			"`clone_urls` block",
			"any `routing` block a person wrote go with it",
			"Repair the list from the diff the publish prints",
			"a shared job's runs halt in between",
		}},
	}
	for _, p := range pages {
		data, err := os.ReadFile("../../" + p.path)
		if err != nil {
			t.Fatal(err)
		}
		// The pages wrap their prose, so a phrase is matched with every run of
		// whitespace read as one space: a rewrap is not a change of claim, and a
		// dropped half still fails.
		doc := strings.Join(strings.Fields(string(data)), " ")
		for _, want := range p.want {
			if !strings.Contains(doc, want) {
				t.Errorf("%s no longer says %q: the --delete-then-publish step rebuilds the policy's allow_repos from the config, drops clone_urls and a hand-written routing block, and halts a shared job's runs until the publish, so the page has to say so", p.path, want)
			}
		}
	}
}
