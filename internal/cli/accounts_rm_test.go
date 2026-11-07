package cli

// accounts_rm_test.go is `fugaro accounts rm`'s world, built on the same rig
// `accounts add` runs in: the accounts these tests remove are ones the add half
// of the pair made, so what they read is the state that command leaves rather
// than a fixture written to look like it.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
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
	// The line's columns and not only its words: the secret's ID is longer than
	// the value column is wide, so a line that puts the ID there prints it run
	// together with the sentence beside it ("claude-oauth-token-team-adeleted:
	// …"), which a `strings.Contains` cannot see. add's report has the same shape
	// — the short account name in the column, the secret in the rest.
	if value, rest := accountsRmColumns(accountsAddRow(t, out, "secret")); value != "team-a" ||
		!strings.HasPrefix(rest, config.ClaudeAccountSecret("team-a")+" deleted: ") {
		t.Errorf("the block's secret line is %q / %q, want the account in the value column and the secret's ID opening the rest", value, rest)
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

	// The fixture is written beside the add rig's own file, so the two are read
	// against each other: everything but the account this test removes — the
	// project, the profile, the repositories — is accountsAddLocal's, and a
	// change there that this copy did not follow fails here rather than passing
	// against a file no rig writes.
	base, err := localcfg.Parse([]byte(accountsAddLocal))
	if err != nil {
		t.Fatalf("the add rig's own fixture does not parse: %v", err)
	}
	here, err := localcfg.Parse([]byte(accountsRmLocalCommented))
	if err != nil {
		t.Fatalf("accountsRmLocalCommented does not parse: %v", err)
	}
	served := here.Profiles["standard"]
	served.ClaudeAccounts = nil
	here.Profiles["standard"] = served
	delete(here.ClaudeAccounts, "team-a")
	if !reflect.DeepEqual(base, here) {
		t.Errorf("accountsRmLocalCommented is no longer accountsAddLocal with claude_accounts.team-a and the profile's entry added:\nthe rig's fixture: %+v\nthis one:          %+v", base, here)
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
// command deletes a credential, and a session the managed gate did not admit
// is refused outright — --yes included — with nothing written and the secret
// still there.
//
// The refusal asserted here is THIS command's and not the settings write
// path's. The two are not interchangeable: the write path refuses the same
// input on its own fence (applySettingsEdit, which every settings write goes
// through), and every assertion about the file, the secret and the words
// "coding agent's session" holds just as well when that fence answers instead
// — which is why the sentences that are only this command's are pinned too,
// and why the diff is pinned as absent. A run that reached the write path
// prints the removal before it refuses it, and says nothing about what it read
// off the session.
func TestAccountsRmRefusesACodingAgentsSession(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))
	t.Setenv("CLAUDECODE", "1") // a coding agent's session

	out, _, rmErr := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard", "--yes")
	if rmErr == nil {
		t.Fatalf("a coding agent's session removed the account:\n%s", out)
	}
	if !strings.Contains(rmErr.Error(), "coding agent's session") {
		t.Errorf("the refusal does not name the session it refused: %v", rmErr)
	}
	if !strings.Contains(rmErr.Error(), "fugaro accounts rm") {
		t.Errorf("the refusal does not name the command the person runs at their own terminal: %v", rmErr)
	}
	if !strings.Contains(rmErr.Error(), "nothing was read off this session") || !strings.Contains(rmErr.Error(), id) {
		t.Errorf("the refusal is not this command's own — it does not say that nothing was read off the session and that the secret %s is still there: %v", id, rmErr)
	}
	if strings.Contains(rmErr.Error(), "refusing to write") {
		t.Errorf("the settings write path refused this run and this command's own refusal never fired: %v", rmErr)
	}
	if strings.Contains(out, "CONFIRM") {
		t.Errorf("the refused session was asked to confirm the removal it may not make:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "- ") {
			t.Errorf("the refused session was shown the removal's diff before the refusal: %q\n%s", line, out)
		}
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
	if got := accountsAddRow(t, out, "secret"); !strings.Contains(got, "kept, not deleted") {
		t.Errorf("the block's secret line reads %q, and the secret was not deleted: a row that is missing reads as a deletion that printed nothing", got)
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

// TestAccountsRmManagedAdmitsTheRemovalInAnAgentSession is the red step of the
// accounts handoff's inverse: in a managed project's coding agent session the
// removal goes through, where the same session was refused before the gate
// existed — because the removal's referrers are checked and clean, which is the
// one rule this command's own file states for it (managed spec §4.2, D1180).
//
// The removal is the step the gate judges, and the converge behind it is add's
// own runner: its stages are the ones `fugaro update` runs, and a stage with
// something to apply is a person's step in a session, exactly as it is for
// `accounts add`. So what this asserts is the removal itself — written, deleted
// and recorded — and that the command does not claim a converged installation
// it did not reach.
//
// The record is the gate's and it follows the write: one admitted verdict,
// whose digest is of the document that was applied and whose rules name both
// halves of the check this command makes.
func TestAccountsRmManagedAdmitsTheRemovalInAnAgentSession(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))
	before := readFileString(t, r.cfg)
	lcBefore := r.addLocal(t)
	_, next, _, err := accountsRmResolve(r.cfg, lcBefore, "team-a", "standard", []byte(before))
	if err != nil {
		t.Fatalf("the resolver refused the config this test removes from: %v", err)
	}
	t.Setenv(accountsAddAgent, "1")

	out, _, rmErr := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard", "--yes")
	if after := readFileString(t, r.cfg); after == before {
		t.Fatalf("the removal was not written in a managed project's coding agent session: %v\nstdout:\n%s\nstderr:\n%s", rmErr, out, rmErr)
	}
	lc := r.addLocal(t)
	if _, ok := lc.ClaudeAccounts["team-a"]; ok {
		t.Errorf("claude_accounts.team-a is still declared: %v", slices.Sorted(maps.Keys(lc.ClaudeAccounts)))
	}
	if got := r.sm.Deleted(); !slices.Equal(got, []string{id}) {
		t.Errorf("the secret deletes were %v, want %v: the admission covers the account's removal, not the declaration alone", got, id)
	}
	if rmErr != nil && strings.Contains(rmErr.Error(), "coding agent's session") {
		t.Errorf("the session was refused a removal the gate admitted: %v", rmErr)
	}
	if rmErr != nil && !strings.Contains(rmErr.Error(), "in your own terminal window") {
		t.Errorf("the run failed for a reason that is not a step left to the person at their terminal: %v", rmErr)
	}
	if strings.Contains(out, "Nothing else to run") {
		t.Errorf("the command ended on its ready line, which is only true of a converged installation:\n%s", out)
	}

	recs := r.audits(t)
	if len(recs) != 1 {
		t.Fatalf("%d gate records, want the one admission of this removal: %+v", len(recs), recs)
	}
	rec := recs[0]
	if !strings.HasSuffix(rec.Command, " accounts rm") {
		t.Errorf("the record names %q, want the command a person types", rec.Command)
	}
	if rec.Verdict != managedmode.AuditAdmitted {
		t.Errorf("the record's verdict is %q, want %q: the step was applied", rec.Verdict, managedmode.AuditAdmitted)
	}
	if want := gateDigest(next); rec.PlanDigest != want {
		t.Errorf("the record's plan_digest is %q and the removal this command would write hashes to %q: the record does not name the document that was applied", rec.PlanDigest, want)
	}
	for _, want := range []string{"no profile lists claude_accounts.team-a", "no repository this machine holds a checkout of names team-a in the committed agent.oauth_token"} {
		if !slices.ContainsFunc(rec.Rules, func(rule string) bool { return strings.Contains(rule, want) }) {
			t.Errorf("the record's rules (%v) do not name the rule %q: the record is what an owner reads to see what was judged", rec.Rules, want)
		}
	}
}

// TestAccountsRmManagedRefusesWhileARepositoryStillNamesIt is the other half of
// that rule: a repository this machine can read whose committed fugaro.yaml
// names the account in agent.oauth_token is a referrer, and it refuses the
// removal BY THE GATE, before the file is written and before the secret is
// touched — with the refusal, and the record of it, naming the repository that
// was found rather than the key that would have been removed.
//
// The checkout's working tree has the line deleted and its commit still has it,
// which is the state a coding agent's session is in before it commits anything:
// the referrer is the COMMIT, so the uncommitted edit may not flip this refusal
// into an admission — the next run of that repository resolves its account out
// of the commit and would still stop at launch.
func TestAccountsRmManagedRefusesWhileARepositoryStillNamesIt(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	accountsRmCheckoutDirty(t, "owner/repo-a", accountsRmRepoYAML, accountsRmRepoYAMLWithoutToken)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard", "--yes")
	if err == nil {
		t.Fatalf("a repository that still names the account did not stop the removal:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the removal was not refused by the gate, so nothing says which control refused it: %v", err)
	}
	if !strings.Contains(err.Error(), "owner/repo-a") || !strings.Contains(err.Error(), "agent.oauth_token") {
		t.Errorf("the refusal does not name the repository and the key that still reference the account: %v", err)
	}
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("the file changed under a removal the gate refused:\n%s", after)
	}
	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("the refused removal deleted %v", got)
	}
	recs := r.audits(t)
	if len(recs) != 1 {
		t.Fatalf("%d gate records, want the one refusal: %+v", len(recs), recs)
	}
	if recs[0].Verdict != managedmode.AuditRefused {
		t.Errorf("the record's verdict is %q, want %q: the step was refused", recs[0].Verdict, managedmode.AuditRefused)
	}
	if !slices.ContainsFunc(recs[0].Rules, func(rule string) bool { return strings.Contains(rule, "owner/repo-a") }) {
		t.Errorf("the record's rules (%v) do not name the repository that refused the removal", recs[0].Rules)
	}
}

// TestAccountsRmWarnsWhileARepositoryStillNamesIt is the same referrer on the
// owner's own path, where it is a warning and not a block (spec §4 step 2,
// D783's shape): the file belongs to a repository this command cannot write, so
// the owner's own edit is not stopped by it — they are told which repository
// breaks with the removal and what its next run does — and the removal they
// confirmed goes through.
//
// The warning is printed BEFORE the confirmation and not after it, which is the
// order this test pins: the break it names is one the removal makes, so the
// moment it can still be declined is the moment it belongs in.
//
// The checkout's working tree has the line deleted and its commit still has it:
// the reference is the COMMIT — the file the repository's next run resolves its
// account from — so the uncommitted edit may not make this warning disappear,
// and a coding agent's session is exactly what leaves a tree in that state.
func TestAccountsRmWarnsWhileARepositoryStillNamesIt(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	accountsRmCheckoutDirty(t, "owner/repo-a", accountsRmRepoYAML, accountsRmRepoYAMLWithoutToken)
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))

	out := r.rm(t, "team-a", "--profile", "standard")

	if !strings.Contains(out, "warning: team-a is named by owner/repo-a's committed fugaro.yaml in agent.oauth_token") {
		t.Errorf("the run does not name the repository whose committed file still names the account:\n%s", out)
	}
	if !strings.Contains(out, "stops at launch") || !strings.Contains(out, "fugaro accounts add ") {
		t.Errorf("the warning does not say what that repository's next run does once the declaration is gone, and how to put the account back:\n%s", out)
	}
	if i, j := strings.Index(out, "warning:"), strings.Index(out, "CONFIRM"); i < 0 || j < 0 || i > j {
		t.Errorf("the warning is not printed while the confirmation can still be declined (warning at %d, confirm at %d):\n%s", i, j, out)
	}
	// The warning is not a refusal: what the person confirmed is what ran.
	if got := r.sm.Deleted(); !slices.Equal(got, []string{id}) {
		t.Errorf("the secret deletes were %v, want %v: the reference is a warning and not a block", got, id)
	}
	if lc := r.addLocal(t); func() bool { _, ok := lc.ClaudeAccounts["team-a"]; return ok }() {
		t.Errorf("claude_accounts.team-a is still declared after a removal the owner confirmed")
	}
}

// TestAccountsRmWarnsAboutARepositoryWithNoCheckoutHere is one half of the way
// the read can come up short: a repository this machine holds no checkout of is
// unknown rather than clean, and unknown is said rather than assumed. It is a
// warning and not a refusal on purpose — refusing on it would refuse every
// machine that does not hold every checkout of the project, and what the unread
// reference would break is a run that stops at launch, which the owner reads
// about here.
func TestAccountsRmWarnsAboutARepositoryWithNoCheckoutHere(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))

	out := r.rm(t, "team-a", "--profile", "standard")

	if !strings.Contains(out, "warning: this machine has no checkout of owner/repo-a") {
		t.Errorf("the run does not say which repository it has no checkout of:\n%s", out)
	}
	if !strings.Contains(out, "fugaro accounts ls") {
		t.Errorf("the warning does not name the read that shows the reference:\n%s", out)
	}
	if got := r.sm.Deleted(); !slices.Equal(got, []string{id}) {
		t.Errorf("the secret deletes were %v, want %v: a repository with no checkout here is a warning and not a block", got, id)
	}
}

// TestAccountsRmWarnsAboutACheckoutThatIsGone is the second shape of "no
// checkout here": the store remembers a row whose directory is not there any
// more, which is the same answer as a repository this machine never checked out
// — the question was never asked — and therefore the warning, not the refusal a
// checkout whose document could not be read gets.
func TestAccountsRmWarnsAboutACheckoutThatIsGone(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	dir := accountsRmCheckout(t, "owner/repo-a", accountsRmRepoYAML)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))

	out := r.rm(t, "team-a", "--profile", "standard")

	if !strings.Contains(out, "warning: this machine has no checkout of owner/repo-a") {
		t.Errorf("the run does not say which repository it has no checkout of:\n%s", out)
	}
	if strings.Contains(out, "could not read the committed fugaro.yaml") {
		t.Errorf("a checkout whose directory is gone is reported as a document that could not be read:\n%s", out)
	}
	if got := r.sm.Deleted(); !slices.Equal(got, []string{id}) {
		t.Errorf("the secret deletes were %v, want %v: a row whose directory is gone is a warning and not a block", got, id)
	}
}

// TestAccountsRmWarnsAboutACheckoutItCannotRead is the other half: a checkout
// this machine HAS, whose own committed fugaro.yaml it could not read, is unknown
// in a different way — the question was asked and the answer could not be read —
// and it is still a warning on the owner's own path, because that file is a
// repository's and their edit may not be blocked by one this command cannot
// write. The session that makes the same read refuses it (accountsRmRules,
// managed spec §4.1: an unresolved value is a refusal and never a pass).
func TestAccountsRmWarnsAboutACheckoutItCannotRead(t *testing.T) {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	accountsRmCheckout(t, "owner/repo-a", accountsRmRepoYAMLUnreadable)
	id := gcp.ProjectSecretID("aurora", config.ClaudeAccountSecret("team-a"))

	out := r.rm(t, "team-a", "--profile", "standard")

	if !strings.Contains(out, "warning: this machine could not read the committed fugaro.yaml of owner/repo-a") {
		t.Errorf("the run does not say whose committed file it could not read:\n%s", out)
	}
	if !strings.Contains(out, "fugaro accounts ls") {
		t.Errorf("the warning does not name the read that shows the reference:\n%s", out)
	}
	if got := r.sm.Deleted(); !slices.Equal(got, []string{id}) {
		t.Errorf("the secret deletes were %v, want %v: on the owner's own path an unread checkout is a warning and not a block", got, id)
	}
}

// TestAccountsRmManagedRefusesACheckoutItCannotRead is the gate's half of that
// split, and the decision this round took on it: a repository with no checkout
// here stays a warning (accountsRmWarn), while a checkout whose own file did not
// resolve is a refusal — "a value the gate cannot resolve at plan time" is "a
// refusal, never a pass" (managed spec §4.1). Without it, a file this machine
// could not parse would be scored as a clean referrer and a session would take
// the one-way delete while the repository may still name the account.
func TestAccountsRmManagedRefusesACheckoutItCannotRead(t *testing.T) {
	r := newManagedAddRig(t, addRigLocal, addMarksManaged)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	accountsRmCheckout(t, "owner/repo-a", accountsRmRepoYAMLUnreadable)
	before := readFileString(t, r.cfg)
	t.Setenv(accountsAddAgent, "1")

	out, _, err := executeStdin(t, "", "accounts", "rm", "team-a", "--profile", "standard", "--yes")
	if err == nil {
		t.Fatalf("a checkout this machine could not read did not stop the removal:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the removal was not refused by the gate, so nothing says which control refused it: %v", err)
	}
	if !strings.Contains(err.Error(), "owner/repo-a") {
		t.Errorf("the refusal does not name the checkout whose file could not be read: %v", err)
	}
	if after := readFileString(t, r.cfg); after != before {
		t.Errorf("the file changed under a removal the gate refused:\n%s", after)
	}
	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("the refused removal deleted %v", got)
	}
	recs := r.audits(t)
	if len(recs) != 1 {
		t.Fatalf("%d gate records, want the one refusal: %+v", len(recs), recs)
	}
	if recs[0].Verdict != managedmode.AuditRefused {
		t.Errorf("the record's verdict is %q, want %q: the step was refused", recs[0].Verdict, managedmode.AuditRefused)
	}
	if !slices.ContainsFunc(recs[0].Rules, func(rule string) bool { return strings.Contains(rule, "owner/repo-a") }) {
		t.Errorf("the record's rules (%v) do not name the checkout that refused the removal", recs[0].Rules)
	}
}

// accountsRmRepoYAML is the committed fugaro.yaml of the repository a removal
// is warned about and refused on: the file's own bytes are the whole answer for
// agent.oauth_token (it is repository-scope), so the fixture is a file a
// repository could hold rather than a fragment — it decodes strictly, which is
// what config.AgentOf reads it with.
const accountsRmRepoYAML = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: oauth-token
  oauth_token: team-a
workflows:
  web:
    commands: { build: go build ./..., test: go test ./... }
`

// accountsRmRepoYAMLWithoutToken is that same file with the oauth_token line
// deleted — what a session that took the account out of the repository it owns
// leaves in the working tree until someone commits it, and therefore the half of
// the fixture that says the reference is read out of the commit.
const accountsRmRepoYAMLWithoutToken = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: oauth-token
workflows:
  web:
    commands: { build: go build ./..., test: go test ./... }
`

// accountsRmRepoYAMLUnreadable is a fugaro.yaml this Fugaro cannot read at all —
// a key the strict reader refuses — which is "cannot tell" and not the same
// answer as "names no account" (config.AgentOf).
const accountsRmRepoYAMLUnreadable = `version: 1
project: aurora
git: { provider: github }
a_key_this_fugaro_does_not_know: 1
`

// accountsRmCheckout makes a real checkout of repo and remembers it for the
// project, so the reference check has something to read: the file is committed
// on the base branch, as the file a run would check out is, and the working tree
// it is read against is the same file.
func accountsRmCheckout(t *testing.T, repo, fugaroYAML string) string {
	t.Helper()
	return accountsRmCheckoutDirty(t, repo, fugaroYAML, "")
}

// accountsRmCheckoutDirty is accountsRmCheckout for the checkout whose working
// tree holds something other than what it committed — the state a coding agent's
// session is in between its edit and its commit. An empty working file leaves
// the tree clean.
func accountsRmCheckoutDirty(t *testing.T, repo, committed, working string) string {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": committed})
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository's files")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	if working != "" {
		if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(working), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := localcfg.RecordCheckout(os.Getenv, "aurora",
		localcfg.CheckoutIdentity{Provider: "github", Repo: repo}, dir, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}
	return dir
}

// accountsRmColumns splits one of the block's lines into its value column and
// everything after it (the block writes label 10, value 18, rest). It is what a
// `strings.Contains` cannot see: a value wider than its column runs into the
// sentence beside it, and the assertion that catches it is about where the two
// begin.
func accountsRmColumns(line string) (value, rest string) {
	if len(line) <= 10 {
		return "", ""
	}
	if len(line) <= 28 {
		return strings.TrimSpace(line[10:]), ""
	}
	return strings.TrimSpace(line[10:28]), line[28:]
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
