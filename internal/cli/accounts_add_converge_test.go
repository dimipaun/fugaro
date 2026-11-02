package cli

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// accounts_add_converge_test.go is the converge half of `fugaro accounts add`
// (accounts_add_converge.go): what the command does after the local config is
// written — the secret, the mark, the three stages it invokes in-process, and
// the read-back that is the command's own last line.

// TestAccountsAddLeavesNothingToRun is A2's red step (spec §2.2 steps 5–8): a
// command that only declares the account leaves the owner three commands — a
// secret to store, a job set to deploy, a node to publish — and the whole
// point of the converge is that it leaves none of them. It fails against an
// implementation that writes the local config and stops: the node carries no
// account, no job mounts the token, and the last line names a run that is not
// ready.
func TestAccountsAddLeavesNothingToRun(t *testing.T) {
	r := newAddRig(t)
	out, _ := r.add(t, "team-a", "--repos", "owner/repo-a")

	// The node every run of the project is admitted by: the account and the
	// repositories its token may be used by, derived from the local config the
	// command just wrote and published by the Policy stage and no other writer.
	p := r.policy(t)
	acc, ok := p.Accounts["team-a"]
	if !ok {
		t.Fatalf("the published node names no account team-a: accounts = %v", slices.Sorted(maps.Keys(p.Accounts)))
	}
	if want := []string{"github/owner/repo-a"}; !slices.Equal(acc.AllowRepos, want) {
		t.Errorf("the published accounts.team-a.allow_repos = %v, want %v", acc.AllowRepos, want)
	}
	// The profile the command added the name to serves it, which is what makes
	// a job exist for it at all.
	if pr := p.Profiles["standard"]; !slices.Contains(pr.ClaudeAccounts, "team-a") {
		t.Errorf("the published profiles.standard.claude_accounts = %v, want team-a", pr.ClaudeAccounts)
	}

	// The job set: one job per (profile, provider, account), and this account's
	// token mounted on that job and no other — the secretAccessor grant is the
	// job's own secret_env, and the negative half is the one that matters.
	jobs := r.projectJobs(t)
	job, ok := jobs[gcp.ProjectJobName("standard", "github", "team-a")]
	if !ok {
		t.Fatalf("the job set has no standard/github/team-a: jobs = %v", slices.Sorted(maps.Keys(jobs)))
	}
	secret := config.ClaudeAccountSecret("team-a")
	if got := job.SecretEnv[config.ReservedSecrets[config.ClaudeOAuthToken]]; got != secret {
		t.Errorf("standard/github/team-a mounts %q as the Claude token, want %q", got, secret)
	}
	for name, j := range jobs {
		if name == job.Job {
			continue
		}
		for _, mounted := range j.SecretEnv {
			if mounted == secret {
				t.Errorf("job %s mounts %s too: one account's token rides on that account's job and no other", name, secret)
			}
		}
	}

	// The last line is the whole point of the command: it names the one thing
	// the owner does next, and it is a run.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "fugaro run") {
		t.Errorf("the command's last line is %q, and it has to name the fugaro run that is now ready", last)
	}
	if strings.Contains(last, "fugaro update") {
		t.Errorf("the command's last line is %q: it names an update, and there is nothing left to update", last)
	}
}
