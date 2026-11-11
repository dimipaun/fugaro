package cli

// accounts_allow_test.go is `fugaro accounts allow|deny`'s world: the local
// config's claude_accounts block, edited through the settings write path's one
// helper, on the fixture `settings set`'s own writes run on (a project config
// on this machine and no coding-agent marker). No cloud, no checkout: these
// two forms write one local document and nothing else, and the line they end
// on is the whole of what they say about the rest of the world.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// accountsAllowLocal is the fixture: one declared account whose token may be
// used by one repository. It is the state `accounts add` leaves behind, written
// here rather than reached through that command because these tests are about
// the second edit of the pair.
const accountsAllowLocal = `claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
`

// TestAccountsAllowAppendsTheRepository is S8's red step: the subcommand does
// not exist, and every assertion below is about what it does once it does.
//
// The list is what the account's token may be spent from, so appending a
// repository is the widening this form is for — and the repository the entry
// already names is not lost, because the edit is one item added to the list the
// file holds rather than a list written from the flag.
func TestAccountsAllowAppendsTheRepository(t *testing.T) {
	_, path := settingsWriteHomeWith(t, accountsAllowLocal)

	out, _, err := execute(t, "accounts", "allow", "team-a", "owner/repo-b")
	if err != nil {
		t.Fatalf("accounts allow team-a owner/repo-b: %v", err)
	}

	lc := accountsAllowLoad(t, path)
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a", "owner/repo-b"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the entry's own repository and the one the flag named", got)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the run does not end on the line that says what is left:\n%s", out)
	}
}

// accountsAllowLoad reads the local config this fixture's commands wrote.
func accountsAllowLoad(t *testing.T, path string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("the config this command wrote is one its own loader refuses: %v", err)
	}
	return lc
}

// accountsAllowBytes is the file as it is, for the tests that assert a refusal
// left it byte-identical.
func accountsAllowBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestAccountsAllowCreatesTheAccountWithItsAllowList is the pair's first form:
// a NAME the config does not declare yet is declared by `allow` with the
// repositories it was given, so an account whose token may be spent from one
// repository needs no `accounts add` and no second command. The list is
// written into a claude_accounts block the file did not have, which is the
// edit's own work and not a block written by hand.
func TestAccountsAllowCreatesTheAccountWithItsAllowList(t *testing.T) {
	_, path := settingsWriteHome(t)
	if before := string(accountsAllowBytes(t, path)); strings.Contains(before, "claude_accounts") {
		t.Fatalf("the fixture declares an account, so it cannot show the block being created:\n%s", before)
	}

	if _, _, err := execute(t, "accounts", "allow", "team-b", "owner/repo-x"); err != nil {
		t.Fatalf("accounts allow team-b owner/repo-x: %v", err)
	}

	lc := accountsAllowLoad(t, path)
	if got := lc.ClaudeAccounts["team-b"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-x"}) {
		t.Fatalf("claude_accounts.team-b.allow_data_to = %v, want the repository this run named alone", got)
	}
}

// TestAccountsDenyRemovesIt is the other direction: `deny` takes the
// repository out of the list and leaves the rest of the entry where it is —
// the other repositories the account may be spent from, and the account
// itself, which stays declared (an account with no repositories is an account
// no run may choose, not a name the file forgot).
func TestAccountsDenyRemovesIt(t *testing.T) {
	_, path := settingsWriteHomeWith(t, accountsAllowLocal)
	if _, _, err := execute(t, "accounts", "allow", "team-a", "owner/repo-b"); err != nil {
		t.Fatalf("accounts allow team-a owner/repo-b: %v", err)
	}

	if _, _, err := execute(t, "accounts", "deny", "team-a", "owner/repo-a"); err != nil {
		t.Fatalf("accounts deny team-a owner/repo-a: %v", err)
	}

	lc := accountsAllowLoad(t, path)
	if got := lc.ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-b"}) {
		t.Fatalf("claude_accounts.team-a.allow_data_to = %v, want the one repository neither direction removed", got)
	}

	// The last repository goes the same way, and the account stays declared:
	// an account with an empty list is one no run may choose, which is the
	// state `deny` is asked for, and it is not a name the file quietly loses.
	if _, _, err := execute(t, "accounts", "deny", "team-a", "owner/repo-b"); err != nil {
		t.Fatalf("accounts deny team-a owner/repo-b: %v", err)
	}
	lc = accountsAllowLoad(t, path)
	acct, ok := lc.ClaudeAccounts["team-a"]
	if !ok {
		t.Fatalf("removing the last repository took the account itself out of the config: %v", lc.ClaudeAccounts)
	}
	if len(acct.AllowDataTo) != 0 {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want no repository left", acct.AllowDataTo)
	}
}

// TestAccountsDenyRefusesASubsetViolation is the rule a removal can break and
// an addition cannot (accountsAddSubsetRule's rule, read on the account's own
// list). The profile admits the repository the account would stop covering, so
// the removal is refused with nothing written — silently narrowing what the
// token may be spent from, in a profile that still admits it, is the state the
// rule exists to prevent.
func TestAccountsDenyRefusesASubsetViolation(t *testing.T) {
	local := accountsAllowLocal + `
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
`
	_, path := settingsWriteHomeWith(t, local)
	before := accountsAllowBytes(t, path)

	_, _, err := execute(t, "accounts", "deny", "team-a", "owner/repo-a")
	if err == nil {
		t.Fatal("the subset rule was not applied")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, want := range []string{"owner/repo-a", "profiles.standard"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal does not say nothing was written: %v", err)
	}
	if after := accountsAllowBytes(t, path); string(after) != string(before) {
		t.Errorf("a refused removal wrote the file:\n%s", after)
	}
}

// TestAccountsAllowPrintsOnlyFugaroUpdateNext is the closing line's own rule
// (spec §2.5): one local file edit leaves exactly one step, and the run says
// that step and no other. A second "next:" line would be a second thing for
// the person to do, which no settings write has — the publish the change
// reaches the project's runs through is `fugaro update`'s own.
func TestAccountsAllowPrintsOnlyFugaroUpdateNext(t *testing.T) {
	settingsWriteHomeWith(t, accountsAllowLocal)

	out, _, err := execute(t, "accounts", "allow", "team-a", "owner/repo-b")
	if err != nil {
		t.Fatalf("accounts allow team-a owner/repo-b: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if got := lines[len(lines)-1]; got != "next: fugaro update" {
		t.Errorf("the run ends on %q, want the one line every settings write ends on", got)
	}
	var nexts []string
	for _, line := range lines {
		if strings.HasPrefix(line, "next:") {
			nexts = append(nexts, line)
		}
	}
	if len(nexts) != 1 {
		t.Errorf("the run prints %d next: lines (%v), want exactly one", len(nexts), nexts)
	}
}

// TestAccountsLsShowsWhatAccountsAddAndAllowWrote is the read-back: the names
// `accounts ls` answers with are decided by the very list these two commands
// write, so a repository the allow_list holds is one the account may be
// chosen by, and one it does not hold is not. The fixture's declaration is
// what `accounts add` leaves behind (claude_accounts.NAME.allow_data_to);
// `allow` is what adds the second repository, and both are read here through
// the loader's own answer rather than through the file.
func TestAccountsLsShowsWhatAccountsAddAndAllowWrote(t *testing.T) {
	settingsWriteHomeWith(t, accountsAllowLocal)
	if _, _, err := execute(t, "accounts", "allow", "team-a", "owner/repo-b"); err != nil {
		t.Fatalf("accounts allow team-a owner/repo-b: %v", err)
	}

	out, _, err := execute(t, "accounts", "ls", "--repo", "owner/repo-b")
	if err != nil {
		t.Fatalf("accounts ls --repo owner/repo-b: %v", err)
	}
	if !strings.Contains(out, "team-a") {
		t.Errorf("the repository accounts allow wrote is not one the account may be chosen by:\n%s", out)
	}

	other, _, err := execute(t, "accounts", "ls", "--repo", "owner/repo-c")
	if err != nil {
		t.Fatalf("accounts ls --repo owner/repo-c: %v", err)
	}
	if strings.Contains(other, "team-a") {
		t.Errorf("a repository no list holds is one the account may be chosen by:\n%s", other)
	}
}

// TestAccountsAllowAndDenyReachAFlowStyleList: both directions have the second
// half yamledit's item operations need, because a flow sequence is the one
// shape AddListItem and RemoveListItem both refuse — an allow_data_to the
// operator wrote as [owner/repo-a] is one, and this pair is what makes the
// removal direction reachable at all. The list is written again in the block
// style this program writes a list in, with the repositories the file holds, and
// the edit is made there; the account's entry keeps its place either way.
func TestAccountsAllowAndDenyReachAFlowStyleList(t *testing.T) {
	_, path := settingsWriteHomeWith(t, "claude_accounts:\n  team-a: { allow_data_to: [owner/repo-a] }\n")

	if _, _, err := execute(t, "accounts", "allow", "team-a", "owner/repo-b"); err != nil {
		t.Fatalf("accounts allow team-a owner/repo-b on a flow list: %v", err)
	}
	if got := accountsAllowLoad(t, path).ClaudeAccounts["team-a"].AllowDataTo; !slices.Equal(got, []string{"owner/repo-a", "owner/repo-b"}) {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want the entry's own repository and the one the run named", got)
	}
	// The block's own bytes: the fixture's other blocks are flow by
	// construction, so the shape is read from this block alone.
	if after := string(accountsAllowBytes(t, path)); strings.Contains(after[strings.Index(after, "claude_accounts:"):], "[") {
		t.Errorf("the list is still written in the flow style the edit cannot splice into:\n%s", after)
	}

	if _, _, err := execute(t, "accounts", "deny", "team-a", "owner/repo-a", "owner/repo-b"); err != nil {
		t.Fatalf("accounts deny team-a owner/repo-a owner/repo-b on a flow list: %v", err)
	}
	if got := accountsAllowLoad(t, path).ClaudeAccounts["team-a"].AllowDataTo; len(got) != 0 {
		t.Errorf("claude_accounts.team-a.allow_data_to = %v, want no repository left", got)
	}
	after := string(accountsAllowBytes(t, path))
	for _, want := range []string{"team-a", "allow_data_to"} {
		if !strings.Contains(after[strings.Index(after, "claude_accounts:"):], want) {
			t.Errorf("the account lost its %s: an empty list is not a removed account (that is `accounts rm`)\n%s", want, after)
		}
	}
}

// TestAccountsAllowNoChangePrintsNoNextLine: the hand-off line is the promise
// that the published policy's accounts block is now behind this list, so a run
// that wrote nothing — the state it was asked for is the state the file is in —
// prints the "no change" and not the line. Both directions are in the loop,
// because the settings write path answers both the same way
// (yamledit.Changed) and the two spellings of one rule cannot disagree.
func TestAccountsAllowNoChangePrintsNoNextLine(t *testing.T) {
	_, path := settingsWriteHomeWith(t, accountsAllowLocal)
	before := accountsAllowBytes(t, path)

	for _, args := range [][]string{
		{"accounts", "allow", "team-a", "owner/repo-a"},
		{"accounts", "deny", "team-a", "owner/repo-absent"},
	} {
		out, _, err := execute(t, args...)
		if err != nil {
			t.Fatalf("%s: %v", strings.Join(args, " "), err)
		}
		if !strings.Contains(out, "no change") {
			t.Errorf("%s does not answer no change:\n%s", strings.Join(args, " "), out)
		}
		if strings.Contains(out, "next: fugaro update") {
			t.Errorf("%s promises an update for a write that did not happen:\n%s", strings.Join(args, " "), out)
		}
	}
	if after := accountsAllowBytes(t, path); string(after) != string(before) {
		t.Errorf("a run with nothing to write wrote the config:\n%s", after)
	}
}
