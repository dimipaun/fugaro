package cli

// accounts_rm_test.go is `fugaro accounts rm`'s world, built on the same rig
// `accounts add` runs in: the accounts these tests remove are ones the add half
// of the pair made, so what they read is the state that command leaves rather
// than a fixture written to look like it.

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestAccountsRmRefusesWhileAProfileListsIt is spec §4 step 1, and it is the
// step the whole order exists for. A profile's claude_accounts names a job the
// installation has deployed, so an account removed under a live job leaves a
// job the policy cannot describe: the config such a command would write is one
// ValidateProfileClaudeAccounts refuses, and the owner would be holding a file
// their own CLI will not read. The test asserts both halves — that the command
// refuses, and that the file it would otherwise leave is one its own reader
// refuses — because a refusal is only worth its text if the state behind it is
// a real one.
func TestAccountsRmRefusesWhileAProfileListsIt(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}

	_, _, rmErr := executeStdin(t, "", "accounts", "rm", "team-a", "--yes")

	after, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the command wrote %s while a profile still listed the account:\n%s", r.cfg, after)
	}
	if _, err := localcfg.Load(r.cfg); err != nil {
		t.Fatalf("the config that command left is one its own next load refuses: %v", err)
	}
	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("the command deleted %v while a profile still listed the account", got)
	}
	if rmErr == nil {
		t.Fatalf("the account was removed while profiles.standard still listed it")
	}
	if !strings.Contains(rmErr.Error(), "standard") {
		t.Errorf("the refusal does not name the profile that lists the account: %v", rmErr)
	}
	if !strings.Contains(rmErr.Error(), "--profile") {
		t.Errorf("the refusal does not say how to take the name out of the profile: %v", rmErr)
	}
}

// TestAccountsRmRemovesTheDeclarationAndTheProfileEntry is spec §4 step 2's
// first half, and it is the pair of edits this command's local-config side is:
// the name leaves the profile's list, and the declaration leaves the
// claude_accounts block. Both through internal/yamledit, so the file that is
// left is the file a person would have written by hand.
func TestAccountsRmRemovesTheDeclarationAndTheProfileEntry(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")

	out := r.rm(t, "team-a", "--profile", "standard")

	lc := r.addLocal(t)
	if _, ok := lc.ClaudeAccounts["team-a"]; ok {
		t.Errorf("claude_accounts.team-a is still declared: %v", slices.Sorted(maps.Keys(lc.ClaudeAccounts)))
	}
	if got := lc.Profiles["standard"].ClaudeAccounts; len(got) != 0 {
		t.Errorf("profiles.standard.claude_accounts = %v, want no entry", got)
	}
	if got := accountsAddRow(t, out, "profile"); !strings.Contains(got, "profiles.standard.claude_accounts -= team-a") {
		t.Errorf("the block's profile line reads %q, want the entry it removed named", got)
	}
	if got := accountsAddRow(t, out, "account"); !strings.Contains(got, "claude_accounts.team-a removed") {
		t.Errorf("the block's account line reads %q, want the declaration it removed named", got)
	}
	// The file it wrote is the config its own reader loads, which is the state
	// the refusal above is about not breaking.
	if _, err := localcfg.Load(r.cfg); err != nil {
		t.Fatalf("the config this command wrote does not load: %v", err)
	}
}

// TestAccountsRmDeletesTheSecretAndConverges is spec §4 steps 2 and 3 for the
// account that is fully removed: the secret's container takes the delete and
// its value is gone, the published node no longer names the account, and the
// job the account owned — and its service account — are gone from the
// installation.
func TestAccountsRmDeletesTheSecretAndConverges(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))
	if r.sm.Latest(id) == nil {
		t.Fatalf("the fixture's secret %s holds no value, so the delete has nothing to take away", id)
	}
	job := gcp.ProjectJobName("standard", "github", "team-a")
	if _, ok := r.projectJobs(t)[job]; !ok {
		t.Fatalf("the fixture deployed no %s, so the removal has no job to take away", job)
	}

	out := r.rm(t, "team-a", "--profile", "standard")

	if got, want := r.sm.Deleted(), []string{id}; !slices.Equal(got, want) {
		t.Errorf("the secret deletes were %v, want %v", got, want)
	}
	if v := r.sm.Latest(id); v != nil {
		t.Errorf("the secret %s still holds a value after the delete: %q", id, v)
	}
	if got := accountsAddRow(t, out, "secret"); !strings.Contains(got, id) {
		t.Errorf("the block's secret line reads %q, and the ID it deleted is %s", got, id)
	}
	if p := r.policy(t); len(p.Accounts) != 0 {
		t.Errorf("the published node still carries accounts = %v, want the account gone", slices.Sorted(maps.Keys(p.Accounts)))
	}
	if got := r.projectJobs(t); func() bool { _, ok := got[job]; return ok }() {
		t.Errorf("the job set still has %s: jobs = %v", job, slices.Sorted(maps.Keys(got)))
	}
	if got := accountsAddRow(t, out, "deploy"); !strings.HasPrefix(got, "deploy") || !strings.Contains(got, "- "+job) {
		t.Errorf("the block's deploy line reads %q, want the job it removed", got)
	}
	// The node's own line, read back rather than claimed: the account is gone
	// from what every run of the project is admitted by.
	if got := accountsAddRow(t, out, "publish"); !strings.Contains(got, "is gone from "+projectpolicy.PathShared()) {
		t.Errorf("the block's publish line reads %q, want the read-back that the node dropped it", got)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Nothing else to run:") {
		t.Errorf("the command's last line is %q, and there is nothing left for the owner to run", last)
	}
}

// TestAccountsRmLeavesAnotherAccountsSecretAlone is the negative half of the
// delete, and the one that matters: one account's removal may not touch
// another's secret, its declaration, its entry or its job. The two secrets are
// two IDs and the fixture's own record of which one took the delete is what
// this asserts.
func TestAccountsRmLeavesAnotherAccountsSecretAlone(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	r.add(t, "team-b", "--repos", "owner/repo-a")
	aID := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))
	bID := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-b"))
	bValue := r.sm.Latest(bID)
	if bValue == nil {
		t.Fatalf("the fixture's secret %s holds no value", bID)
	}

	r.rm(t, "team-a", "--profile", "standard")

	if got, want := r.sm.Deleted(), []string{aID}; !slices.Equal(got, want) {
		t.Errorf("the secret deletes were %v, want only %v", got, want)
	}
	if got := r.sm.Latest(bID); !bytes.Equal(got, bValue) {
		t.Errorf("team-b's secret was written or deleted: it now holds %q", got)
	}
	lc := r.addLocal(t)
	if _, ok := lc.ClaudeAccounts["team-b"]; !ok {
		t.Errorf("claude_accounts.team-b went with team-a: %v", slices.Sorted(maps.Keys(lc.ClaudeAccounts)))
	}
	if !slices.Contains(lc.Profiles["standard"].ClaudeAccounts, "team-b") {
		t.Errorf("profiles.standard.claude_accounts = %v, and team-b still has to be on it", lc.Profiles["standard"].ClaudeAccounts)
	}
	jobs := r.projectJobs(t)
	if _, ok := jobs[gcp.ProjectJobName("standard", "github", "team-b")]; !ok {
		t.Errorf("team-b's job went with team-a's: jobs = %v", slices.Sorted(maps.Keys(jobs)))
	}
	if _, ok := jobs[gcp.ProjectJobName("standard", "github", "team-a")]; ok {
		t.Errorf("team-a's job is still deployed: jobs = %v", slices.Sorted(maps.Keys(jobs)))
	}
	p := r.policy(t)
	if _, ok := p.Accounts["team-b"]; !ok {
		t.Errorf("the published node dropped accounts.team-b: %v", slices.Sorted(maps.Keys(p.Accounts)))
	}
	if _, ok := p.Accounts["team-a"]; ok {
		t.Errorf("the published node still carries accounts.team-a")
	}
}

// TestAccountsRmKeepsTheOtherRepositoriesComments is the fixture whose comments
// and key order tell an edit that went through internal/yamledit from one that
// round-tripped the decoded config: removing an account may not touch the
// comment above another key, nor the hand-written block beside it.
//
// The file on disk is not the observable. A successful removal converges, and
// the stages of that converge rewrite the local config the way `fugaro update`
// does — materializing defaults and dropping comments, as they do after
// `accounts add` too — so what this test reads is the edit itself: the diff the
// command prints from the bytes before the write, which is one line per key
// removed and no other, and the bytes accountsRmResolve built, which still hold
// every comment of the file it read.
func TestAccountsRmKeepsTheOtherRepositoriesComments(t *testing.T) {
	r := newAddRigWith(t, accountsAddLocal)
	r.add(t, "team-a", "--repos", "acme/app")

	out := r.rm(t, "team-a", "--profile", "standard")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "+ ") {
			t.Errorf("the removal's diff adds a line, so the edit did not go through internal/yamledit: %q\n%s", line, out)
		}
	}
	if !strings.Contains(out, "- ") {
		t.Errorf("the removal's diff removes nothing:\n%s", out)
	}

	// The bytes of the edit, read off the resolver on the hand-written file: the
	// two keys this command removes are the only lines that move.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(accountsRmLocalCommented), 0o600); err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, next, _, err := accountsRmResolve(path, lc, "team-a", "standard", old)
	if err != nil {
		t.Fatalf("the resolver refused a config it is written for: %v", err)
	}
	if !plan.drop || !plan.entry {
		t.Errorf("the resolver planned entry=%v drop=%v, want the entry and the declaration", plan.entry, plan.drop)
	}
	for _, want := range []string{
		"# aurora's own config, kept in the owner's own order.",
		"# the bucket every run of this project lands in",
		"# the account the 2026 migration left behind",
		"  legacy:\n    allow_data_to:\n      - acme/app\n",
	} {
		if !strings.Contains(string(next), want) {
			t.Errorf("the edit dropped %q, which it does not touch:\n%s", want, next)
		}
	}
	if strings.Contains(string(next), "team-a") || strings.Contains(string(next), "repo-b") {
		t.Errorf("the account's own lines are still in the file:\n%s", next)
	}
}

// accountsRmLocalCommented is accountsAddLocal with the account this test
// removes written by hand beside the one the 2026 migration left behind: the
// account a profile serves — so the removal is the entry and the declaration
// both — with a second repository of its own, which is the other line the edit
// moves.
const accountsRmLocalCommented = `# aurora's own config, kept in the owner's own order.
version: 1
name: aurora
# the bucket every run of this project lands in
runs_bucket: fugaro-runs-proj-1234
region: us-east5
gcp_project: proj-1234

claude_accounts:
  # the account the 2026 migration left behind
  legacy:
    allow_data_to:
      - acme/app
  team-a:
    allow_data_to:
      - acme/app
      - acme/repo-b

profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/acme/app
    claude_accounts:
      - team-a

repos:
  acme/app: { provider: github, github_app_id: "12345", workflows: [web] }
`

// TestAccountsRmSaysTheTokenIsNotRevokedAtTheProvider is D866, and it is about
// the confirmation rather than the run: the secret's ID is named before the
// secret goes, and the block says plainly that deleting it does not revoke the
// token at the provider. The run below is the one that never happens — no
// terminal and no --yes — so what it prints is printed while the secret is
// still there, which is the order the sentence is about.
func TestAccountsRmSaysTheTokenIsNotRevokedAtTheProvider(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))

	out, _, rmErr := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard")
	if rmErr == nil {
		t.Fatalf("a run with no terminal and no --yes removed the account:\n%s", out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("the confirmation does not name the secret's ID %s, which is the credential it deletes:\n%s", id, out)
	}
	if !strings.Contains(out, "does not revoke") || !strings.Contains(out, "Anthropic") {
		t.Errorf("the confirmation does not say that deleting the secret leaves the token valid at the provider:\n%s", out)
	}
	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("the secret was deleted by a run that was not confirmed: %v", got)
	}
	after, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the unconfirmed run wrote %s:\n%s", r.cfg, after)
	}
}

// TestAccountsRmRefusesACodingAgentsSession is spec §4's last line: this
// command deletes a credential, and no plan gate admits a removal of a secret
// (D1172), so a coding agent's session is refused outright — --yes included —
// with nothing written and the secret still there.
func TestAccountsRmRefusesACodingAgentsSession(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDECODE", "1") // a coding agent's session

	_, _, rmErr := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard", "--yes")
	if rmErr == nil {
		t.Fatalf("a coding agent's session removed the account")
	}
	if !strings.Contains(rmErr.Error(), "coding agent's session") {
		t.Errorf("the refusal does not name the session it refused: %v", rmErr)
	}
	if !strings.Contains(rmErr.Error(), "fugaro accounts rm") {
		t.Errorf("the refusal does not name the command the person runs at their own terminal: %v", rmErr)
	}
	after, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the refused session wrote %s:\n%s", r.cfg, after)
	}
	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("the refused session deleted %v", got)
	}
}

// TestAccountsRmPutsTheConfigBackWhenTheDeleteFails is #1691's lesson, and the
// one state this command may not leave behind: a delete that fails after the
// write. The config is put back exactly as it was — behind the same
// compare-and-swap the write path makes — and the command stops before the
// converge, so nothing is published or deployed either.
//
// The refused delete is a server of this test's: the fake answers a delete
// truthfully (404 for a secret that is not there), so an installation whose
// Secret Manager refuses the call is the one way to reach this branch
// hermetically.
func TestAccountsRmPutsTheConfigBackWhenTheDeleteFails(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))
	accountsRmRefuseSecrets(t, r.cfg)
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}

	out, _, rmErr := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard", "--yes")
	if rmErr == nil {
		t.Fatalf("the delete was refused and the command succeeded:\n%s", out)
	}
	if !strings.Contains(rmErr.Error(), "deleting the secret "+id) {
		t.Errorf("the error does not name the delete that failed: %v", rmErr)
	}
	if !strings.Contains(out, "put "+r.cfg+" back the way it was") {
		t.Errorf("the command does not say it put the config back:\n%s", out)
	}

	after, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the config was left as the failed run wrote it, and not put back:\n%s", after)
	}
	lc := r.addLocal(t)
	if _, ok := lc.ClaudeAccounts["team-a"]; !ok {
		t.Errorf("claude_accounts.team-a was not restored: %v", slices.Sorted(maps.Keys(lc.ClaudeAccounts)))
	}
	if !slices.Contains(lc.Profiles["standard"].ClaudeAccounts, "team-a") {
		t.Errorf("profiles.standard.claude_accounts = %v, and the entry was not restored", lc.Profiles["standard"].ClaudeAccounts)
	}
	// Nothing converged: the node still carries the account and the job is
	// still deployed, which is the state the restore is meant to leave.
	if p := r.policy(t); len(p.Accounts) == 0 {
		t.Errorf("the publish ran after the failed delete: the node no longer carries the account")
	}
	if _, ok := r.projectJobs(t)[gcp.ProjectJobName("standard", "github", "team-a")]; !ok {
		t.Errorf("the deployment ran after the failed delete: the account's job is gone")
	}
}

// TestAccountsRmRestoreRefusesAFileThatMoved is the compare-and-swap itself,
// without a command around it: the file is put back only while it still holds
// what the command wrote. A file that moved belongs to a later edit, and the
// one thing a failed delete may not do is overwrite it with older bytes.
func TestAccountsRmRestoreRefusesAFileThatMoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	wrote := []byte("claude_accounts: {}\n")
	back := []byte("claude_accounts:\n  team-a:\n    allow_data_to:\n      - owner/repo-a\n")
	moved := []byte("claude_accounts: {}\nname: aurora\n")
	cmd := NewRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := os.WriteFile(path, moved, 0o600); err != nil {
		t.Fatal(err)
	}
	err := accountsRmRestore(cmd, path, wrote, back, "team-a")
	if err == nil {
		t.Fatalf("the restore overwrote a file that had moved since the write")
	}
	if !strings.Contains(err.Error(), "changed while this ran") {
		t.Errorf("the refusal does not say why it did not put the file back: %v", err)
	}
	if got, rerr := os.ReadFile(path); rerr != nil || !bytes.Equal(got, moved) {
		t.Errorf("the file is %q (err %v), and a moved file is left alone", got, rerr)
	}

	if err := os.WriteFile(path, wrote, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := accountsRmRestore(cmd, path, wrote, back, "team-a"); err != nil {
		t.Fatalf("a file that still holds what the command wrote was not put back: %v", err)
	}
	if got, rerr := os.ReadFile(path); rerr != nil || !bytes.Equal(got, back) {
		t.Errorf("the file is %q (err %v), want the bytes before the write", got, rerr)
	}
}

// TestAccountsRmTakesTheNameOutOfOneProfileAtATime is the shape two profiles
// sharing an account need: the entry leaves the named profile alone, the
// declaration, the secret and the other profile's job stay, and the block says
// so rather than reporting an account it did not remove.
func TestAccountsRmTakesTheNameOutOfOneProfileAtATime(t *testing.T) {
	r := newAddRigWith(t, accountsRmLocalShared)
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))
	// The secret the declaration implies, stored the way the command stores it:
	// a secret seeded by hand carries no labels, and the converge refuses a
	// project secret that is not this installation's (infra's own discovery), so
	// the store is the command's own door. The entry the add writes is already
	// on both profiles, which makes it the rotation it is and nothing else.
	r.add(t, "team-a", "--repos", "owner/repo-a", "--profile", "standard")

	out := r.rm(t, "team-a", "--profile", "ops")

	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("the secret of an account another profile still serves was deleted: %v", got)
	}
	if r.sm.Latest(id) == nil {
		t.Errorf("the secret %s was deleted, and profiles.standard still serves the account", id)
	}
	lc := r.addLocal(t)
	if _, ok := lc.ClaudeAccounts["team-a"]; !ok {
		t.Errorf("the declaration went with one profile's entry: %v", slices.Sorted(maps.Keys(lc.ClaudeAccounts)))
	}
	if slices.Contains(lc.Profiles["ops"].ClaudeAccounts, "team-a") {
		t.Errorf("profiles.ops.claude_accounts = %v, want the name gone", lc.Profiles["ops"].ClaudeAccounts)
	}
	if !slices.Contains(lc.Profiles["standard"].ClaudeAccounts, "team-a") {
		t.Errorf("profiles.standard.claude_accounts = %v, want the name kept", lc.Profiles["standard"].ClaudeAccounts)
	}
	jobs := r.projectJobs(t)
	if _, ok := jobs[gcp.ProjectJobName("ops", "github", "team-a")]; ok {
		t.Errorf("the job ops served is still deployed: jobs = %v", slices.Sorted(maps.Keys(jobs)))
	}
	if _, ok := jobs[gcp.ProjectJobName("standard", "github", "team-a")]; !ok {
		t.Errorf("standard's job went with ops's: jobs = %v", slices.Sorted(maps.Keys(jobs)))
	}
	if p := r.policy(t); len(p.Accounts) == 0 {
		t.Errorf("the published node dropped an account profiles.standard still serves")
	}
	if got := accountsAddRow(t, out, "account"); !strings.Contains(got, "stays") {
		t.Errorf("the block's account line reads %q, and the account stays", got)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "standard still does") {
		t.Errorf("the command's last line is %q, and it names what is left", last)
	}
}

// TestAccountsRmRemovesAnAccountWhoseSecretIsAlreadyGone is the delete that
// 404s: the state this step exists for is reached, so the command reports the
// secret as already gone and goes on with the converge rather than failing an
// owner whose credential a hand-delete or an earlier run already took away.
func TestAccountsRmRemovesAnAccountWhoseSecretIsAlreadyGone(t *testing.T) {
	r := newAddRigWith(t, accountsAddLocal)
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("legacy"))
	if got := r.sm.Latest(id); got != nil {
		t.Fatalf("the fixture's secret %s holds a value, so this is not the 404 case", id)
	}

	out := r.rm(t, "legacy")

	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("a secret that was not there was deleted: %v", got)
	}
	if got := accountsAddRow(t, out, "secret"); !strings.Contains(got, "already gone: "+id) {
		t.Errorf("the block's secret line reads %q, want the ID named as already gone", got)
	}
	lc := r.addLocal(t)
	if _, ok := lc.ClaudeAccounts["legacy"]; ok {
		t.Errorf("claude_accounts.legacy is still declared: %v", slices.Sorted(maps.Keys(lc.ClaudeAccounts)))
	}
}

// accountsRmLocalShared is the world two profiles sharing one account are in:
// each writes its own allow_repos, and the account covers both, which is the
// subset rule that decides whether a profile may list it.
const accountsRmLocalShared = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
    claude_accounts:
      - team-a
  ops:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
    claude_accounts:
      - team-a
repos:
  owner/repo-a: { provider: github, github_app_id: "12345", workflows: [web] }
`

// rm runs the command the way the owner does — `--yes` for the confirmation, no
// terminal — and answers what it printed.
func (r *addRig) rm(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, errOut, err := executeStdin(t, "", append([]string{"accounts", "rm", name, "--yes"}, args...)...)
	if err != nil {
		t.Fatalf("accounts rm %s: %v\nstdout:\n%s\nstderr:\n%s", name, err, out, errOut)
	}
	return out
}

// accountsRmRefuseSecrets points the config's Secret Manager endpoint at a
// server that refuses every call, which is how a delete fails hermetically: the
// fake in internal/gcpfake answers a delete truthfully, so the installation
// that says no is one of this test's.
func accountsRmRefuseSecrets(t *testing.T, cfg string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"code":500,"message":"injected failure"}}`)
	}))
	t.Cleanup(srv.Close)
	lc, err := localcfg.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	url := lc.Endpoints.SecretManager
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), url); n != 1 {
		t.Fatalf("the config names the Secret Manager endpoint %q %d times, and this fixture replaces exactly one", url, n)
	}
	swapped := strings.Replace(string(data), url, srv.URL, 1)
	if err := os.WriteFile(cfg, []byte(swapped), 0o600); err != nil {
		t.Fatal(err)
	}
}
