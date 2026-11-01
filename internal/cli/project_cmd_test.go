package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// project_cmd_test.go holds the tests of the two things this release adds to
// `fugaro project policy publish` itself: the refusal of a coding agent's
// session (SD8: the node this command writes is what every run of the project
// is admitted by, so it is a person's write and not an agent's) and the flags
// that are this door's own — --delete and --seed-repos — which the shared build
// the door now takes (policyNodeForPublish) must not swallow. Everything else
// about the command is project_policy_test.go's.

// TestProjectPolicyPublishIsRefusedInAnAgentSession: every form of the door is
// refused before anything is read, --delete included — taking the policy away
// changes what every run is admitted by exactly as much as publishing one does,
// and it is not an agent's to do on its own. The refusal is a user error and
// not a crash, and it names the write it stopped.
func TestProjectPolicyPublishIsRefusedInAnAgentSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"publish", []string{"project", "policy", "publish"}},
		{"--seed-repos", []string{"project", "policy", "publish", "--seed-repos"}},
		{"--delete", []string{"project", "policy", "publish", "--delete", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjectFixture(t, "")
			f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
			t.Setenv("CLAUDECODE", "1")
			before := nodeOf(t, f.db)

			out, errOut, err := executeStdin(t, "", tc.args...)
			if err == nil {
				t.Fatalf("a coding agent's session wrote the policy:\n%s%s", out, errOut)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			for _, want := range []string{"nothing was written", "coding agent", "fugaro project policy publish"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if after := nodeOf(t, f.db); after != before {
				t.Errorf("the database changed in a coding agent's session:\nbefore %s\nafter  %s", before, after)
			}
			if gen := recordedGeneration(t, f); gen != "" {
				t.Errorf("a coding agent's session recorded policy_generation = %q", gen)
			}
		})
	}
}

// TestProjectPolicyPublishKeepsDeleteAndSeedRepos: the two flags the door owns
// and the stage does not have. The node's allow-list is the node's own and a
// publish keeps it (P4), so the seed is only ever written when the operator asks
// for it by name — and a publish that went through the shared build and dropped
// the flag would look exactly like one that had never been asked.
func TestProjectPolicyPublishKeepsDeleteAndSeedRepos(t *testing.T) {
	t.Run("--seed-repos", func(t *testing.T) {
		f := newProjectFixture(t, "")
		node := staleBudgetNode()
		node["allow_repos"] = []string{"github/acme/hand-added"}
		f.db.Set(projectpolicy.PathShared(), node)

		if _, _, err := execute(t, "project", "policy", "publish", "--seed-repos"); err != nil {
			t.Fatal(err)
		}
		got := publishedPolicy(t, f).AllowRepos
		want := []string{"bitbucket/acme/other", "github/acme/app"}
		if len(got) != len(want) {
			t.Fatalf("allow_repos = %v, want the operator's repositories %v", got, want)
		}
		for i, r := range want {
			if got[i] != r {
				t.Fatalf("allow_repos = %v, want %v", got, want)
			}
		}
	})

	t.Run("--delete", func(t *testing.T) {
		f := newProjectFixture(t, "")
		f.db.Set(projectpolicy.PathShared(), staleBudgetNode())

		out, _, err := execute(t, "project", "policy", "publish", "--delete", "--yes")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if v := f.db.Value(projectpolicy.PathShared()); v != nil {
			t.Errorf("--delete left the node: %v", v)
		}
	})
}
