package cli

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// The launch is one of the readers an alias is resolved at (D2091, D2092): a
// choice key and the --token flag may name an alias, and what the run is given
// is the account the alias stands for. The tests here are that resolution at
// each rung the launch reads a name from, the allow-list asked about the
// account rather than the alias (F6), and the stored task carrying the account
// and never the alias by its own name (F4).

// aliasBlock is the local config's claude_account_aliases block: one alias
// standing for one account of the same file's claude_accounts.
func aliasBlock(alias, target string) string {
	return "claude_account_aliases:\n  " + alias + ": " + target + "\n"
}

// TestLaunchResolvesAnAliasOnEveryRung is D2091 read at the launch: every name
// a rung reads out of the local config — the flag, the stage key of rung 2 and
// the project default of rung 4 — may name an alias, and the run goes to the
// account it stands for. Each subtest asserts the *account*: the allow-list
// admits it (an alias handed to it would be a name no account answers to, F6)
// and the task carries it (F4).
//
// Mutation (drop the ResolveAccount calls in claudeAccountFor): every subtest
// fails, refused as an account nothing defines.
func TestLaunchResolvesAnAliasOnEveryRung(t *testing.T) {
	t.Run("the flag", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow"))
		oauthCheckout(t, f, "")
		writeBuildRecord(t, f, otherSlug, "svc", release065)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261111-120000-a1a1", "--token", "reviewer", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261111-120000-a1a1").OAuthToken; got != "acme-overflow" {
			t.Fatalf("OAuthToken = %q, want acme-overflow: --token named the alias reviewer", got)
		}
	})
	t.Run("the stage key", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow")+stagesBlock("review", "reviewer"))
		oauthCheckout(t, f, "  oauth_token: acme-personal\n")
		env := gcsEnv(t, f)
		co := checkoutConfig(context.Background(), "acme/other")
		if co == nil {
			t.Fatal("checkoutConfig found no checkout")
		}
		// The account the reviews run on, which is what the runner's environment
		// and the stage's own job variant are built from.
		got, err := claudeAccountFor(context.Background(), env, "acme/other", "", "review", "", co, nameWritten)
		if err != nil {
			t.Fatal(err)
		}
		if got != "acme-overflow" {
			t.Fatalf("the review stage's account = %q, want acme-overflow: its key names the alias reviewer", got)
		}
	})
	t.Run("the project default", func(t *testing.T) {
		f := newCloudFixture(t)
		f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow")+"default_claude_account: reviewer\n")
		oauthCheckout(t, f, "")
		writeBuildRecord(t, f, otherSlug, "svc", release065)
		f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
		if _, _, err := execute(t, "run", "--run-id", "20261111-120000-a1a2", "A task"); err != nil {
			t.Fatal(err)
		}
		if got := otherSpec(t, f, "20261111-120000-a1a2").OAuthToken; got != "acme-overflow" {
			t.Fatalf("OAuthToken = %q, want acme-overflow: the project default names the alias reviewer", got)
		}
	})
}

// TestLaunchRefusesAnAliasWhoseTargetIsNotAdmitted is F6 read as a refusal:
// the allow-list is asked about the account the alias stands for, so an alias
// whose target this repository may not use is refused in the account's own
// name — the sentence names acme-elsewhere and the claude_accounts entry that
// admits a token, which is the move that works — and nothing is started.
func TestLaunchRefusesAnAliasWhoseTargetIsNotAdmitted(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-elsewhere:\n    allow_data_to: [\"acme/person-1\"]\n"+
		aliasBlock("reviewer", "acme-elsewhere")+"default_claude_account: reviewer\n")
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	_, _, err := execute(t, "run", "--run-id", "20261111-120000-a2a2", "A task")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user refusal", err)
	}
	for _, want := range []string{"acme-elsewhere", "claude_accounts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none", n)
	}
}

// TestLaunchTaskCarriesTheResolvedAccount is F4 at the launch: the task is the
// thing every later reader of the run holds — the runner, the console, a
// follow-up, a resume — so the alias has to stop here or it reaches all of
// them. The spec names the account, and no field of it carries the alias by
// its own name.
func TestLaunchTaskCarriesTheResolvedAccount(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow")+"default_claude_account: reviewer\n")
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
	if _, _, err := execute(t, "run", "--run-id", "20261111-120000-a3a3", "A task"); err != nil {
		t.Fatal(err)
	}
	spec := otherSpec(t, f, "20261111-120000-a3a3")
	if spec.OAuthToken != "acme-overflow" {
		t.Fatalf("OAuthToken = %q, want acme-overflow", spec.OAuthToken)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "reviewer") {
		t.Fatalf("the stored task carries the alias by its own name: %s", raw)
	}
}

// TestLaunchLineNamesTheAliasAndItsAccount is the launch's own line (spec §7,
// D2099): a launch that read an alias prints the pair, so the operator sees
// which alias decided the run and which account its job mounts — the account
// alone would hide the first, the alias alone the second. A launch that read no
// alias prints what it always printed.
func TestLaunchLineNamesTheAliasAndItsAccount(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow")+"default_claude_account: reviewer\n")
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
	out, _, err := execute(t, "run", "--run-id", "20261111-120000-a4a4", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if want := "launch a4a4 (account reviewer -> acme-overflow)"; !strings.Contains(out, want) {
		t.Errorf("the launch line does not carry %q:\n%s", want, out)
	}

	// The same launch with no alias anywhere names no pair.
	f2 := newCloudFixture(t)
	f2.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f2, "")
	writeBuildRecord(t, f2, otherSlug, "svc", release065)
	f2.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
	out, _, err = execute(t, "run", "--run-id", "20261111-120000-a5a5", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, " -> ") {
		t.Errorf("a launch that read no alias printed an account pair:\n%s", out)
	}
}

// TestFollowUpLaunchLineNamesTheAliasItRead is the same line for the follow-up
// that reads a name itself: `--pr N --token ALIAS` takes the alias' current
// target (D2098), so this launch read an alias and names the pair. The follow-up
// that inherits has no alias to name — the previous run's account is what its
// record carries, and a record holds no alias at all (F4) — and prints what it
// always printed.
func TestFollowUpLaunchLineNamesTheAliasItRead(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-team:\n    allow_data_to: [\"acme/app\"]\n"+aliasBlock("reviewer", "acme-team"))
	oauthCheckout(t, f, "")
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	writeBuildRecord(t, f, appSlug, "web", release065)
	f.run.AddJob(gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-team")), "2", "4Gi")
	out, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--token", "reviewer", "--run-id", fuID, "A follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if want := "launch " + task.ShortRun(fuID) + " (follow-up of PR #7, after " + task.ShortRun(rootID) + ") (account reviewer -> acme-team)"; !strings.Contains(out, want) {
		t.Errorf("the follow-up's launch line does not carry %q:\n%s", want, out)
	}
}

// TestBatchLaunchLineNamesTheAliasOnEveryAttempt is the same line for a batch
// (--attempts): a best-of-N batch resolves its account once, from the same flag
// and keys a single launch does (resolveBatchPlan), and every attempt's own step
// is that resolution's line — one pair per attempt, which is what the operator
// reads to know which account all N jobs mount.
func TestBatchLaunchLineNamesTheAliasOnEveryAttempt(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig+aliasBlock("reviewer", "acme-overflow"))
	oauthCheckout(t, f, "")
	writeBuildRecord(t, f, otherSlug, "svc", release065)
	f.run.AddJob(gcp.JobName(otherSlug, gcp.WorkflowVariant("svc", "acme-overflow")), "2", "4Gi")
	out, _, err := execute(t, "run", "--repo", "acme/other", "--attempts", "2", "--token", "reviewer", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "(account reviewer -> acme-overflow)"); n != 2 {
		t.Fatalf("%d of the batch's launch lines carry the pair, want one per attempt:\n%s", n, out)
	}
}

// TestReviewLaunchLineNamesTheAliasItRead is the line for a review-mode launch
// (--recipe review-only --pr N): it resolves its account from the flag exactly
// as a fresh run does (reviewSpec), so `--token ALIAS` names the pair its step
// prints. A review job names no pull request of its own in the line's follow-up
// tail (it reviews one rather than continuing one), which is why the pair is
// the whole of what this line adds.
func TestReviewLaunchLineNamesTheAliasItRead(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-overflow: { allow_data_to: [\"acme/app\"] }\n"+aliasBlock("reviewer", "acme-overflow"))
	baseBranchCheckout(t, "acme/app", aliasChainYAML, "main")
	writeBuildRecord(t, f, appSlug, "web", release070)
	f.run.AddJob(gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-overflow")), "2", "4Gi")
	const reviewRun = "20261007-100000-abcd"
	out, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", reviewRun, "--recipe", "review-only", "--pr", "7", "--token", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if want := "launch " + task.ShortRun(reviewRun) + " (account reviewer -> acme-overflow)"; !strings.Contains(out, want) {
		t.Errorf("the review launch's line does not carry %q:\n%s", want, out)
	}
}

// TestReusedLaunchNamesNoPairItDidNotRead is the launch line's own edge, and it
// is the one the reuse path makes: a launch whose --run-id already holds its own
// task is answered with that task (reuseStored), so what starts is the account
// the *stored* task mounts — written when it was stored — while the builders
// above resolved a name out of the file as it stands now. A repeat after a
// repoint would otherwise print the pair this call read (reviewer -> the new
// target) while the job it starts is the old target's variant, which is a line
// about a run that is not the one going. The stored task carries its account and
// no alias at all (F4), so the repeat names no pair, exactly as a launch that
// read no alias does.
func TestReusedLaunchNamesNoPairItDidNotRead(t *testing.T) {
	f := aliasChainFixture(t)
	args := []string{"run", "--repo", limitsRepo, "--continues", chainRun, "--run-id", chainFu, "A task"}
	out, _, err := execute(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	if want := "launch " + task.ShortRun(chainFu) + " (account reviewer -> acme-overflow)"; !strings.Contains(out, want) {
		t.Fatalf("the launch that read the alias does not carry %q:\n%s", want, out)
	}

	// The one edit the aliases exist for: the same keys, the other account.
	aliasRepoint(t, "acme-overflow", "acme-personal")

	out, _, err = execute(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already launched") {
		t.Fatalf("the repeat is not the launch it repeats:\n%s", out)
	}
	if strings.Contains(out, " -> ") {
		t.Errorf("the repeat named a pair it did not read: the task it reused mounts %s, and the alias is not what decided it:\n%s",
			readSpec(t, f, chainFu).OAuthToken, out)
	}
	if got := readSpec(t, f, chainFu).OAuthToken; got != "acme-overflow" {
		t.Errorf("the stored task mounts %q, want the account its own launch resolved", got)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the one the first launch started", n)
	}
}

// TestRepeatRefusesAStoredAccountTheRepositoryMayNoLongerUse is the allow-list
// read at the one place a launch reuses a stored task as it is (F6, F4): a run
// ID's task is bucket data nobody vouches for, and the account it mounts was
// judged when it was written — a grant narrowed since, or an account taken out
// of claude_accounts, must not let a repeat start a job on that credential. The
// check is storedRepeat's, the funnel every reuse shape passes through
// (--continues, --pr, a review recipe, --retry), and it is asked about the
// account the stored task carries rather than about the one this call resolves.
func TestRepeatRefusesAStoredAccountTheRepositoryMayNoLongerUse(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-overflow: { allow_data_to: [\"acme/app\"] }\n")
	oauthCheckout(t, f, "")
	const id = "20261113-120000-c1c1"
	spec := firstRunSpec(id)
	spec.OAuthToken = "acme-overflow"
	seedSpec(t, f, spec, false)

	// The account's own grant, narrowed to a repository that is not this one.
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), `acme-overflow: { allow_data_to: ["acme/app"] }`, `acme-overflow: { allow_data_to: ["acme/elsewhere"] }`, 1)
	if updated == string(data) {
		t.Fatal("the fixture's claude_accounts block names no acme-overflow for acme/app")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}

	b, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	_, err = storedRepeat(context.Background(), envOn(t, f, b), appSlug, &runOptions{runID: id}, 0, "x", 0)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the repeat refused on the stored account's own grant", err)
	}
	for _, want := range []string{"acme-overflow", "not one acme/app may use"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// aliasRenameFixture is the installation the rename cases below are measured
// on: acme/app checked out with agent.auth oauth, one account that admits it,
// and — where a launch's own name was an account once — that name defined as an
// alias of it, which ValidateAccountAliases admits exactly because the two are
// not in claude_accounts together any more.
func aliasRenameFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-personal: { allow_data_to: [\"acme/app\"] }\n"+
		aliasBlock("acme-overflow", "acme-personal"))
	baseBranchCheckout(t, "acme/app", aliasChainYAML, "main")
	writeBuildRecord(t, f, appSlug, "web", release065)
	// Both variants exist, so a launch that resolved the recorded name through
	// the alias block would start on the other one rather than fail for a job
	// the installation does not have: the refusal below is the alias rule's and
	// not a missing job's.
	f.run.AddJob(gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-overflow")), "2", "4Gi")
	f.run.AddJob(gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-personal")), "2", "4Gi")
	return f
}

// The owner's rename, and the one shape that must not follow it: a name a
// *record* carries is the account itself — a task's OAuthToken and the account
// a follow-up inherits were written down after a launch resolved them (F4) — so
// it stands as written and is never read through the alias block again. The
// block may still take the name over (the account is gone from claude_accounts,
// so there is no clash to refuse): what is refused is the launch, because the
// checks it makes would then be asked about the alias' target while the job the
// task starts still mounts the recorded name's token — the allow-list judging
// one account and the credential being another. The three tests below are the
// three places a recorded name reaches the walk: the repeat funnel
// (storedRepeat), --retry's stored task, and the account a follow-up inherits.

// TestRepeatRefusesAStoredNameThatIsAnAliasNow is the repeat funnel's own case:
// a stored task naming an account the file no longer has.
func TestRepeatRefusesAStoredNameThatIsAnAliasNow(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-personal: { allow_data_to: [\"acme/app\"] }\n"+
		aliasBlock("acme-overflow", "acme-personal"))
	oauthCheckout(t, f, "")
	const id = "20261113-120000-c2c2"
	spec := firstRunSpec(id)
	spec.OAuthToken = "acme-overflow"
	seedSpec(t, f, spec, false)

	b, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	_, err = storedRepeat(context.Background(), envOn(t, f, b), appSlug, &runOptions{runID: id}, 0, "x", 0)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the repeat refused on the stored name the alias block answers", err)
	}
	for _, want := range []string{"acme-overflow", "acme-personal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestRetryRefusesAStoredNameThatIsAnAliasNow is the same name at --retry,
// which launches a stored task as it stands (retrySpec): the job it starts is
// the stored name's variant, so the name is judged as written and the launch is
// refused rather than moved to the account the name means today.
func TestRetryRefusesAStoredNameThatIsAnAliasNow(t *testing.T) {
	f := aliasRenameFixture(t)
	const id = "20261113-120000-c3c3"
	spec := firstRunSpec(id)
	spec.OAuthToken = "acme-overflow"
	seedSpec(t, f, spec, false)

	_, _, err := execute(t, "run", "--retry", id)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the retry refused on the stored name the alias block answers", err)
	}
	for _, want := range []string{"acme-overflow", "acme-personal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want the refusal to start nothing", n)
	}
}

// TestFollowUpRefusesAnInheritedNameThatIsAnAliasNow is the third place, and
// the one the launch line would otherwise lie about: a follow-up inherits its
// previous run's account (D2098), and that name is the account the previous run
// resolved and wrote down. Reading it through the alias block now would switch
// the follow-up to whatever the name means today, silently and on a credential
// nobody judged, so it is refused — --token is where the operator says which
// account they want, and that flag may name the alias.
func TestFollowUpRefusesAnInheritedNameThatIsAnAliasNow(t *testing.T) {
	f := aliasRenameFixture(t)
	spec := firstRunSpec(rootID)
	spec.OAuthToken = "acme-overflow"
	seedSpec(t, f, spec, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-time.Hour)
	writeRecord(t, f, rootID, rec)

	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "A follow-up")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want the follow-up refused on the inherited name the alias block answers", err)
	}
	for _, want := range []string{"acme-overflow", "acme-personal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want the refusal to start nothing", n)
	}
}

// aliasChainFixture is the installation a repeat after a repoint is measured
// on: acme/app checked out with agent.auth oauth and a chain to continue, two
// accounts of the repository, and the project default naming the alias. A
// repoint is one edit of that block and no other line (D2093), so the launch and
// its repeat differ in nothing else.
func aliasChainFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	f.appendConfig(t, "claude_accounts:\n  acme-overflow: { allow_data_to: [\"acme/app\"] }\n  acme-personal: { allow_data_to: [\"acme/app\"] }\n"+
		aliasBlock("reviewer", "acme-overflow")+"default_claude_account: reviewer\n")
	baseBranchCheckout(t, limitsRepo, aliasChainYAML, "main")
	writeBuildRecord(t, f, appSlug, "web", release065)
	f.run.AddJob(gcp.JobName(appSlug, gcp.WorkflowVariant("web", "acme-overflow")), "2", "4Gi")
	seedChainRun(t, f, chainRun, 0, task.Overrides{})
	return f
}

// aliasChainYAML is aliasChainFixture's checkout: the workflow the launch runs
// and the one line that makes a named account usable at all (a non-default name
// is refused under every other auth), with no chain limit the launch would have
// to inherit.
const aliasChainYAML = "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n" +
	"agent: { auth: oauth }\n" +
	"workflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"

// aliasRepoint moves the fixture's alias from one account to another, which is
// the whole of what a repoint is on this machine.
func aliasRepoint(t *testing.T, from, to string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "reviewer: "+from+"\n", "reviewer: "+to+"\n", 1)
	if updated == string(data) {
		t.Fatalf("the fixture's alias block names no %s", from)
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The publish is the other reader an alias is resolved at (D2091, D2090): the
// node is what every job image and every teammate's launcher reads, and it
// holds accounts and never this machine's names for them. The tests here are
// what the publish writes for a file whose keys name aliases, and the repoint —
// the one edit the aliases exist for — as the widening the operator's own hand
// confirms (F4, F10).

// aliasPublishConfig is the machine's file for the publish tests: two accounts
// of the same project, both admitting it, and the choice keys naming an alias
// rather than the account. The alias stands for target, so a repoint is one
// edit of this block and no other line (D2093), which is what makes the account
// the keys resolve to move without the keys themselves changing.
func aliasPublishConfig(target string) string {
	return "claude_accounts:\n" +
		"  team-b: { allow_data_to: [acme/app] }\n" +
		"  team-c: { allow_data_to: [acme/app] }\n" +
		"claude_account_aliases:\n  reviewer: " + target + "\n" +
		"default_claude_account: reviewer\n" +
		"stages:\n  review: { claude_account: reviewer }\n"
}

// TestPublishWritesTheResolvedAccountNotTheAlias is the publish half of the
// launch's rule, member by member and before anything is written: the account
// members a publish carries are the accounts the file's keys resolve to, so the
// node speaks the account the alias stands for (D2090, F4) — a name a job image
// knows — and the alias' own name reaches no member.
func TestPublishWritesTheResolvedAccountNotTheAlias(t *testing.T) {
	lc := parseLocalConfig(t, "version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\n"+
		aliasPublishConfig("team-c"))
	got := configAccountValues(lc)
	want := map[string]string{
		"default_claude_account":       "team-c",
		"claude_account_stages.review": "team-c",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("the publish carries %v, want the accounts the aliases resolve to: %v", got, want)
	}
	if slices.Contains(slices.Collect(maps.Values(got)), "reviewer") {
		t.Errorf("a member carries the alias by its own name: %v", got)
	}
}

// repointedAliasFixture is the installation a repoint is measured on: the node
// is the document the publisher's own builder writes for a file whose keys name
// an alias standing for team-b, and the file is then edited so the same keys
// name team-c. Nothing but the alias block differs between the two, which is
// what makes the account members the only thing the publish can see.
func repointedAliasFixture(t *testing.T) *projectFixture {
	t.Helper()
	f := newProjectFixture(t, "")
	rewriteProjectConfig(t, f, aliasPublishConfig("team-b"))
	lc, err := localcfg.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	body, _, _, err := policyNodeForPublish(t.Context(), lc, nil, nil, false, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(body, &node); err != nil {
		t.Fatal(err)
	}
	f.db.Set(projectpolicy.PathShared(), node)
	rewriteProjectConfig(t, f, aliasPublishConfig("team-c"))
	return f
}

// TestRepointIsAPublishWidening: a repoint moves which account the project's
// runs go out on — the review credential, chosen for every run of the project
// and mounted into the job from this machine's file — so it is the widening the
// typed name guards (F10, D846). --yes alone is refused and the refusal names
// each member and both accounts, the node keeps the account it published, and
// the project's own name at a terminal is what confirms it.
func TestRepointIsAPublishWidening(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })

	f := repointedAliasFixture(t)
	h := newMigrationRig(t, f, &initOptions{})
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	out, err := h.apply(t, true)
	if err == nil {
		t.Fatalf("--yes alone repointed the project's runs onto another account:\n%s", out)
	}
	for _, want := range []string{
		"default_claude_account team-b -> team-c",
		"claude_account_stages.review team-b -> team-c",
		"fugaro update",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if p := publishedPolicy(t, f); p.DefaultClaudeAccount != "team-b" || p.ClaudeAccountStages["review"] != "team-b" {
		t.Errorf("the refused publish wrote the repointed accounts anyway: %q / %v", p.DefaultClaudeAccount, p.ClaudeAccountStages)
	}

	h = typedRig(t, f)
	if out, err := h.apply(t, false); err != nil {
		t.Fatalf("the project's name typed at a terminal did not confirm the repoint: %v\n%s", err, out)
	}
	p := publishedPolicy(t, f)
	if p.DefaultClaudeAccount != "team-c" || p.ClaudeAccountStages["review"] != "team-c" {
		t.Errorf("the published node states default_claude_account %q and review %q, want the account the alias stands for now",
			p.DefaultClaudeAccount, p.ClaudeAccountStages["review"])
	}
}
