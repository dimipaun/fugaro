package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// executeStdinReader is executeStdin for a reader of the test's own: it is how
// a test acts at the exact moment the command reads the confirmation — after
// the read it decided on and before the write it makes.
func executeStdinReader(t *testing.T, stdin io.Reader, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(stdin)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(NormalizeRepoArgs(args))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestAccountsLsNoProjectConfigNeedsNoCloud: with no project config selected
// at all (no FUGARO_CONFIG, nothing in $XDG_CONFIG_HOME/fugaro/projects), the
// command still answers with the always-available default account, never
// failing for lack of a GCP project, credentials or a runs bucket. This is
// the offline claim: claude_accounts lives only in the local config, so with
// none selected there is nothing to read but "default" is still real. Since
// selection genuinely failed (there is nothing to select), complete is
// false: a --json caller must not conclude "no other accounts exist" from
// this alone (review finding, task 9b round 2) — a real claude_accounts
// block could exist in a project config this call simply couldn't select.
func TestAccountsLsNoProjectConfigNeedsNoCloud(t *testing.T) {
	isolateProjects(t, t.TempDir())
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.Repo != "acme/app" || len(doc.Accounts) != 1 || doc.Accounts[0] != "default" {
		t.Fatalf("doc = %+v, want repo acme/app, accounts [default]", doc)
	}
	if doc.Complete || doc.Note == "" {
		t.Fatalf("doc = %+v, want complete=false with a note (selection genuinely failed)", doc)
	}
	// No project config means no database to read a mark from: the states are
	// absent with a note, never "every account is available", which is a claim
	// about marks this command did not read (C1.21).
	if doc.States != nil || doc.StateNote == "" {
		t.Fatalf("states = %v, state_note = %q, want no states and a note naming why", doc.States, doc.StateNote)
	}
}

// A mark carrying a state this release does not model is shown as the word it
// carries, in both views: rounding it to "available" is the one direction this
// reader must not take, because it would tell an operator a credential is in
// the cascade's rotation when a newer release has held it.
func TestAccountsLsShowsAStateItDoesNotKnow(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, "claude_accounts:\n  acme-personal:\n    allow_data_to: [\"acme/other\"]\n  acme-quota:\n    allow_data_to: [\"acme/other\"]\n")
	f.db.Set(credsMarkPath("acme-quota"), map[string]any{"state": "quota_hold"})

	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if got := doc.States["acme-quota"].State; got != "quota_hold" {
		t.Fatalf("states[acme-quota] = %q, want the word the mark carries", got)
	}
	// An account with no node is available: a credential with no mark and one
	// whose mark was cleared are the same credential (creds.Mark's own doc).
	if got := doc.States["acme-personal"].State; got != "available" {
		t.Fatalf("states[acme-personal] = %q, want available for an account with no node", got)
	}

	text, _, err := execute(t, "accounts", "ls", "--repo", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "acme-quota") {
			line = l
		}
	}
	if fields := strings.Fields(line); len(fields) != 2 || fields[1] != "quota_hold" {
		t.Fatalf("the text view does not show the unknown state verbatim: %q\n%s", line, text)
	}
}

// TestAccountsLsDefaultFirstThenNamedSorted pins config.AllowedClaudeAccounts'
// own order (design docs/design/named-oauth-tokens.md §6a): "default" always
// first, then every allowed name sorted.
func TestAccountsLsDefaultFirstThenNamedSorted(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig) // acme-personal, acme-overflow allow acme/other
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	want := []string{"default", "acme-overflow", "acme-personal"}
	if len(doc.Accounts) != len(want) {
		t.Fatalf("accounts = %v, want %v", doc.Accounts, want)
	}
	for i, w := range want {
		if doc.Accounts[i] != w {
			t.Fatalf("accounts = %v, want %v", doc.Accounts, want)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want none: accounts ls must never launch anything", n)
	}
	if !doc.Complete || doc.Note != "" {
		t.Fatalf("doc = %+v, want complete=true with no note (selection succeeded)", doc)
	}
}

// TestAccountsLsIncompleteWhenSelectionIsAmbiguous: a project selection
// failure that is not "nothing is configured at all" (here, two project
// configs with nothing to pick one) must still mark the fallback to
// ["default"] incomplete — the bug (review finding, task 9b round 2) was
// that this silent fallback looked exactly like "this repository genuinely
// has only the default account" to a --json caller, for any selection
// failure, not just the "no config exists" one.
func TestAccountsLsIncompleteWhenSelectionIsAmbiguous(t *testing.T) {
	newCloudFixture(t) // writes $XDG_CONFIG_HOME/fugaro/projects/aurora.yaml and sets $FUGARO_CONFIG to it
	// newCloudFixture's own $FUGARO_CONFIG would otherwise select aurora.yaml
	// directly (an explicit selector, same as --config), never reaching the
	// "several project configs, nothing selects one" case this test means
	// to exercise.
	t.Setenv("FUGARO_CONFIG", "")
	second := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", "second.yaml")
	if err := os.WriteFile(second, []byte("version: 1\nname: second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0] != "default" {
		t.Fatalf("doc = %+v, want accounts [default]", doc)
	}
	if doc.Complete || !strings.Contains(doc.Note, "several project configs") {
		t.Fatalf("doc = %+v, want complete=false noting the ambiguous selection", doc)
	}
}

// TestAccountsLsExcludesUnlistedRepo: a named account's allow_data_to not
// listing the repository leaves that repository with "default" only, exactly
// the set fugaro run --token would refuse every other name for
// (claudeAccountAllowed).
func TestAccountsLsExcludesUnlistedRepo(t *testing.T) {
	f := newCloudFixture(t) // acme/app
	f.appendConfig(t, claudeAccountsConfig)
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0] != "default" {
		t.Fatalf("accounts = %v, want [default]", doc.Accounts)
	}
}

// TestAccountsLsDefaultsToCheckoutOrigin: with no --repo, the repository is
// the checkout's origin remote, the same default every other repo-scoped
// command (secrets ls, run) uses.
func TestAccountsLsDefaultsToCheckoutOrigin(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	oauthCheckout(t, f, "")
	out, _, err := execute(t, "accounts", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.Repo != "acme/other" || len(doc.Accounts) != 3 {
		t.Fatalf("doc = %+v, want repo acme/other, 3 accounts", doc)
	}
}

// TestAccountsLsTextOutputIsNamesOnly: without --json, one name per line,
// nothing else (never a secret ID, a repo list or a token value).
func TestAccountsLsTextOutputIsNamesOnly(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, claudeAccountsConfig)
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := []string{"default", "acme-overflow", "acme-personal"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("lines = %v, want %v", lines, want)
		}
	}
}

// TestAccountsLsBadRepoRefused: a malformed --repo is refused before
// anything is read, the same message task.CanonicalRepo gives every other
// repo-scoped command.
func TestAccountsLsBadRepoRefused(t *testing.T) {
	_, _, err := execute(t, "accounts", "ls", "--repo", "not-a-repo")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "owner/name") {
		t.Fatalf("err = %v, want a user error naming owner/name", err)
	}
}

// TestAccountsLsNoOriginRefused: with no --repo and no origin remote, the
// command refuses exactly as secrets ls and run do, rather than guessing.
func TestAccountsLsNoOriginRefused(t *testing.T) {
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	t.Chdir(dir)
	_, _, err := execute(t, "accounts", "ls")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("err = %v, want a user error naming --repo", err)
	}
}

// TestPrintAccountsTextGoesThroughPrintable: the text path never writes a
// name's raw control characters to the terminal, the same defense
// ValidateClaudeAccounts and claudeOAuthAccountRefusal already give an
// account name everywhere else it is printed (internal/config/claude_accounts.go).
// A real claude_accounts name can never actually contain one (ClaudeAccountNameRE),
// but printAccounts does not assume that upstream check ran.
func TestPrintAccountsTextGoesThroughPrintable(t *testing.T) {
	var buf bytes.Buffer
	if err := printAccounts(&buf, accountsListDoc{Repo: "acme/app", Accounts: []string{"default", "evil\x1b[31mname"}}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Fatalf("output %q still has a raw control character", buf.String())
	}
	if !strings.Contains(buf.String(), `\u001b`) {
		t.Fatalf("output %q does not show the escaped form", buf.String())
	}

	// The state column is a tabwriter row, where a literal tab in a name would
	// open the next column instead of staying inside this one. Printable keeps
	// tabs (they are a printable form of their own), so the cell escapes it.
	buf.Reset()
	doc := accountsListDoc{Repo: "acme/app", Accounts: []string{"acme\tpersonal"},
		States: map[string]accountState{"acme\tpersonal": {State: "broken"}}}
	if err := printAccounts(&buf, doc, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\t") {
		t.Fatalf("output %q has a raw tab in a cell", buf.String())
	}
	if !strings.Contains(buf.String(), `acme\u0009personal`) || !strings.Contains(buf.String(), "broken") {
		t.Fatalf("output %q does not show the name and its state", buf.String())
	}
}

// TestAccountsLsNeverTouchesSecretManager: the fixture registers no
// secret_manager endpoint at all, yet the command succeeds — proof it never
// opens a Secret Manager client (unlike fugaro secrets ls, which would fail
// closed here). Pins the design's "no Secret Manager call" claim.
func TestAccountsLsNeverTouchesSecretManager(t *testing.T) {
	f := newCloudFixture(t) // no secret_manager endpoint among extraEndpoints
	f.appendConfig(t, claudeAccountsConfig)
	if _, _, err := execute(t, "accounts", "ls", "--repo", "acme/other"); err != nil {
		t.Fatalf("accounts ls failed without a secret_manager endpoint configured: %v", err)
	}
}

// credsMarkPath is where the service keeps one credential's mark
// (creds.PathMark). The tests seed it by hand: the write is the service's, and
// what these tests are about is what the CLI reads back.
//
// The name is escaped the way the service escapes it (budget.Key), and that is
// what makes a seeded mark readable at all: creds.RTDBStore.Marks refuses a
// subtree whose node is keyed some other way, so a fixture that wrote the raw
// name would seed a mark no reader returns — the read would fail and the test
// around it would be about the failure instead of the name. The names that
// carry nothing to escape key to themselves, so this is the identity for every
// ordinary one.
func credsMarkPath(name string) string { return "creds/marks/" + budget.Key(name) }

// TestAccountsLsShowsStates is C1.21's red step: every listed account carries
// its credential state beside its name, read from creds/marks — the four
// states of internal/creds and, for an exhaustion, the reset time. Names and
// states only: nothing here is a token or a secret ID.
func TestAccountsLsShowsStates(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(2 * time.Hour)
	f.db.Set(credsMarkPath("default"), map[string]any{
		"state": "broken", "since": now.Add(-time.Minute).Format(time.RFC3339Nano)})
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "exhausted", "until": until.Format(time.RFC3339Nano)})
	f.db.Set(credsMarkPath("acme-overflow"), map[string]any{
		"state": "backed_off", "since": now.Add(-time.Minute).Format(time.RFC3339Nano), "backoff": int64(60e9)})

	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.StateNote != "" {
		t.Fatalf("state_note = %q, want none: the marks were readable", doc.StateNote)
	}
	want := map[string]accountState{
		"default":       {State: "broken"},
		"acme-personal": {State: "exhausted", Until: until.Format(time.RFC3339)},
		"acme-overflow": {State: "backed_off"},
	}
	if len(doc.States) != len(want) {
		t.Fatalf("states = %+v, want one entry per listed account (%v)", doc.States, want)
	}
	for name, w := range want {
		if got := doc.States[name]; got != w {
			t.Errorf("states[%q] = %+v, want %+v", name, got, w)
		}
	}

	// The text view is the plan's column: the four states, and the reset time
	// spelled out for an exhaustion.
	text, _, err := execute(t, "accounts", "ls", "--repo", "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("line %q carries no state", line)
		}
		got[fields[0]] = strings.Join(fields[1:], " ")
	}
	for name, w := range map[string]string{
		"default":       "broken",
		"acme-personal": "exhausted until " + until.Format("2006-01-02 15:04Z"),
		"acme-overflow": "backed off",
	} {
		if got[name] != w {
			t.Errorf("the state column of %q is %q, want %q", name, got[name], w)
		}
	}
}

// TestAccountsLsShowsAvailableForAnUnmarkedAccount: an account with no node,
// one whose node says available and one whose mark was cleared are the same
// account (creds.Mark's own doc), and the column says so for each.
func TestAccountsLsShowsAvailableForAnUnmarkedAccount(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{"state": "available"})
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	for _, name := range []string{"default", "acme-personal", "acme-overflow"} {
		if got := doc.States[name]; got.State != "available" {
			t.Errorf("states[%q] = %+v, want available", name, got)
		}
	}
}

// TestAccountsLsNamesNoStateWithoutABackend: an installation with no budget
// database has no marks to read, and the command says so rather than printing
// every account as available — "no mark" and "no database" are not the same
// answer (the fallback accountsConfig already makes for selection).
func TestAccountsLsNamesNoStateWithoutABackend(t *testing.T) {
	f := newCloudFixture(t) // no budget: block
	f.appendConfig(t, claudeAccountsConfig)
	out, errOut, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.States != nil {
		t.Fatalf("states = %+v, want none: this project has no budget database to read a mark from", doc.States)
	}
	if doc.StateNote == "" || !strings.Contains(errOut, "state") {
		t.Fatalf("doc.StateNote = %q, stderr = %q: want a note saying why no state is shown", doc.StateNote, errOut)
	}
}

// TestAccountsLsNeverFailsOnAnUnreadableMark: a marks read the person may not
// make (the rules deny it, the database is down) is a note beside the names,
// not a failure: the names are a local read and stay right, and a command that
// exited non-zero would take the answer away with it.
func TestAccountsLsNeverFailsOnAnUnreadableMark(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	f.db.DenyRead("creds")
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(doc.Accounts) != 3 {
		t.Fatalf("accounts = %v, want the three names even though the marks could not be read", doc.Accounts)
	}
	if doc.States != nil || doc.StateNote == "" {
		t.Fatalf("doc = %+v, want no states and a note naming the read that failed", doc)
	}
}

// TestAccountsLsStateReadIsBounded: the state column's read accepts the
// request and never answers — the unreachable database that drops packets
// rather than refusing, which is the case a per-call timeout turns into a
// minute of a terminal showing nothing, because the project check is a call of
// its own. The command bounds the whole read (accountsStateBudget) instead, so
// it answers in seconds with the names and a note saying why, and the note
// names the bound rather than leaving the operator to guess whether the command
// hung or the database is empty.
//
// The elapsed time is the assertion: with the bound removed the marks read
// waits out internal/rtdb's 30 s per-call timeout, so the threshold below is
// wide enough for a loaded machine and far below the unbounded wait.
func TestAccountsLsStateReadIsBounded(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	_, release := f.db.HoldNext("creds/marks")
	defer release()

	start := time.Now()
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	took := time.Since(start)
	if err != nil {
		t.Fatalf("accounts ls failed on a read that never answered: %v", err)
	}
	if took > 15*time.Second {
		t.Fatalf("accounts ls took %s on a read that never answered, want it bounded well below the per-call timeout", took)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(doc.Accounts) != 3 {
		t.Errorf("accounts = %v, want the three names: they are answered without the network", doc.Accounts)
	}
	if doc.States != nil {
		t.Errorf("states = %+v, want none: the read did not answer", doc.States)
	}
	if !strings.Contains(doc.StateNote, accountsStateBudget.String()) {
		t.Errorf("state_note = %q, want it to name the bound (%s) the read was cut off at", doc.StateNote, accountsStateBudget)
	}
}

// TestAccountsClearMark is C1.21's clear: the operator replaces a refused
// token, then clears the account's mark, and the mark becomes available. The
// write is the operator's own (an admin), it is conditional (PutIfMatch, so a
// mark the service changed under it is not lost), and it needs the project's
// name typed — a state the service is holding is not dropped unconfirmed.
func TestAccountsClearMark(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	now := time.Now().UTC().Truncate(time.Second)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "broken", "since": now.Add(-time.Minute).Format(time.RFC3339Nano)})
	f.db.Set(credsMarkPath("acme-overflow"), map[string]any{
		"state": "exhausted", "until": now.Add(time.Hour).Format(time.RFC3339Nano)})

	// No terminal and no --yes: refused, and nothing written.
	noTerminal(t)
	if _, _, err := execute(t, "accounts", "clear", "acme-personal"); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error refusing without a confirmation", err)
	}
	if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != "broken" {
		t.Fatalf("mark = %v, want it untouched", m)
	}

	// A terminal, and the wrong project name typed: still refused.
	typed(t)
	if _, _, err := executeStdin(t, "yes\n", "accounts", "clear", "acme-personal"); err == nil {
		t.Fatal("a wrong project name confirmed the clear")
	}
	if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != "broken" {
		t.Fatalf("mark = %v, want it untouched", m)
	}

	// The project's name: cleared.
	out, _, err := executeStdin(t, "aurora\n", "accounts", "clear", "acme-personal")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any)
	// creds.Cleared() is the available mark and nothing else: its two zero
	// times are the package's own wire form (Mark's fields carry omitempty,
	// which encoding/json does not apply to a time.Time), and the state is
	// what says the mark holds nothing.
	if m["state"] != "available" {
		t.Fatalf("mark = %v, want the available mark (creds.Cleared)", m)
	}
	if !strings.Contains(out, "acme-personal") || !strings.Contains(out, "broken") {
		t.Fatalf("output = %q, want the account and the state that was cleared", out)
	}

	// An exhausted mark is the other state an operator clears, and --yes needs
	// no terminal.
	noTerminal(t)
	if _, _, err := execute(t, "accounts", "clear", "acme-overflow", "--yes"); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.db.Value(credsMarkPath("acme-overflow")).(map[string]any); m["state"] != "available" {
		t.Fatalf("mark = %v, want available", m)
	}

	// Clearing an available mark is refused rather than written: there is
	// nothing to clear, and a write would be a change the operator did not ask
	// for.
	if _, _, err := execute(t, "accounts", "clear", "acme-overflow", "--yes"); err == nil {
		t.Fatal("clearing an available mark was accepted")
	}
}

// TestAccountsClearOnlyNamedAccount: the clear writes one node — the named
// account's — and never a second. The name must be one this installation has,
// and there are three ways it can be: "default", a claude_accounts entry of
// the selected config, or a mark the database already carries. The first two
// are decided locally, before the database is opened; the third is the mark
// itself, which is the account existing. A name with none of them is refused
// and leaves no node behind, so a typo cannot create a mark for a credential
// that does not exist.
func TestAccountsClearOnlyNamedAccount(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	since := time.Now().UTC().Add(-time.Minute).Truncate(time.Second).Format(time.RFC3339Nano)
	for _, name := range []string{"default", "acme-personal", "acme-overflow"} {
		f.db.Set(credsMarkPath(name), map[string]any{"state": "broken", "since": since})
	}
	// A name with neither a claude_accounts entry nor a mark: a typo. It is
	// refused — the name is what refuses it, since the state the same run
	// would otherwise report is "no mark to clear" — and no node appears for
	// it, which is the whole point of the check.
	noTerminal(t)
	if _, _, err := execute(t, "accounts", "clear", "acme-other", "--yes"); err == nil || ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "claude_accounts") {
		t.Fatalf("err = %v, want a user error naming the name the installation does not know", err)
	}
	if v := f.db.Value(credsMarkPath("acme-other")); v != nil {
		t.Fatalf("the refused clear left a node for a name no account has: %v", v)
	}
	for _, name := range []string{"default", "acme-personal", "acme-overflow"} {
		if m, _ := f.db.Value(credsMarkPath(name)).(map[string]any); m["state"] != "broken" {
			t.Fatalf("the refused clear touched %q: %v", name, m)
		}
	}
	// A mark whose name this checkout's claude_accounts does not list: the
	// ordinary case for a project several checkouts share (claude_accounts is
	// per-checkout and never published, while the marks are the project's
	// own). The mark is the account existing, so the clear takes it — and it
	// has to, because `fugaro doctor` names exactly such marks and prescribes
	// this command as the fix (TestDoctorBrokenMarkFixRuns).
	f.db.Set(credsMarkPath("team-shared"), map[string]any{"state": "broken", "since": since})
	if _, _, err := execute(t, "accounts", "clear", "team-shared", "--yes"); err != nil {
		t.Fatalf("a mark for a name this config does not list was refused: %v", err)
	}
	if m, _ := f.db.Value(credsMarkPath("team-shared")).(map[string]any); m["state"] != "available" {
		t.Fatalf("the mark of a name no claude_accounts entry carries was not cleared: %v", m)
	}

	if _, _, err := execute(t, "accounts", "clear", "acme-personal", "--yes"); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != "available" {
		t.Fatalf("the named account was not cleared: %v", m)
	}
	for _, other := range []string{"default", "acme-overflow"} {
		m, _ := f.db.Value(credsMarkPath(other)).(map[string]any)
		if m["state"] != "broken" || m["since"] != since {
			t.Fatalf("clearing acme-personal rewrote %q's mark: %v", other, m)
		}
	}
}

// TestAccountsClearRefusesStatesItMustNotClear: the clear is for the two
// states an operator's action is what ends — a 401 (broken) and an
// exhaustion — and it refuses the others. A backed-off credential waits for a
// probe (C1.12), not for the operator, and a state this release does not model
// is refused rather than overwritten: clearing it could take off a hold a
// newer release placed, which is the one direction that must not be rounded.
func TestAccountsClearRefusesStatesItMustNotClear(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	noTerminal(t)
	for state, want := range map[string]string{
		"backed_off": "probe",
		"quota_hold": "does not know",
	} {
		f.db.Set(credsMarkPath("acme-personal"), map[string]any{"state": state})
		_, _, err := execute(t, "accounts", "clear", "acme-personal", "--yes")
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), want) {
			t.Errorf("state %s: err = %v, want a user error naming %q", state, err, want)
		}
		if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != state {
			t.Errorf("state %s: the refused clear wrote %v", state, m)
		}
	}
}

// TestAccountsClearNeverPrintsAValue: the command reads and writes names and
// states only (F27). Nothing it prints is a token, a secret ID or a prefix of
// one, and what it writes is the available mark — four fields of
// internal/creds.Mark, never anything else.
func TestAccountsClearNeverPrintsAValue(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "broken", "since": time.Now().UTC().Truncate(time.Second).Format(time.RFC3339Nano)})
	noTerminal(t)
	out, errOut, err := execute(t, "accounts", "clear", "acme-personal", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out+errOut, "projects/") || strings.Contains(out+errOut, "secret") {
		t.Fatalf("the output names a secret: %q / %q", out, errOut)
	}
	m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any)
	for k := range m {
		switch k {
		case "state", "until", "since", "backoff":
		default:
			t.Fatalf("the cleared mark carries %q, which is not a field of creds.Mark: %v", k, m)
		}
	}
}

// A budget.rtdb_url pointing at another project's database is refused rather
// than read or written: the states of one project's cascade are not another
// project's, and this command's write must land in the operator's own
// installation (checkDBProject, the check every other reader and writer makes).
func TestAccountsRefuseAnotherProjectsDatabase(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	f.db.Set("fugaro/project", "birch") // the database belongs to a different project
	now := time.Now().UTC().Truncate(time.Second)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "broken", "since": now.Add(-time.Minute).Format(time.RFC3339Nano)})

	// The read: the names are local and stay right, but no state is claimed.
	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc accountsListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if doc.States != nil || !strings.Contains(doc.StateNote, "birch") {
		t.Fatalf("states = %v, state_note = %q, want no states and a note naming the other project", doc.States, doc.StateNote)
	}

	// The write: refused, and the mark is untouched.
	noTerminal(t)
	if _, _, err := execute(t, "accounts", "clear", "acme-personal", "--yes"); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error for another project's database", err)
	}
	if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != "broken" {
		t.Fatalf("mark = %v, want it untouched", m)
	}
}

// TestAccountsRefuseHTTP2DebugBeforeOpening: both of this task's commands open
// the project's budget database with the person's own Application Default
// Credentials, and GODEBUG=http2debug makes Go print HTTP/2 requests with
// their headers — the OAuth bearer token among them — to stderr. GODEBUG is
// read before main runs, so it cannot be unset in time: every command that
// opens the cloud refuses instead (refuseHTTP2Debug, cloud.go), and these two
// are no exception, though the command they belong to was a purely local one
// before the state column existed. The refusal comes before the open, so
// nothing is read and nothing is written.
func TestAccountsRefuseHTTP2DebugBeforeOpening(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	since := time.Now().UTC().Add(-time.Minute).Truncate(time.Second).Format(time.RFC3339Nano)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{"state": "broken", "since": since})
	noTerminal(t)
	t.Setenv("GODEBUG", "http2debug=2")

	out, _, err := execute(t, "accounts", "ls", "--repo", "acme/other", "--json")
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("accounts ls: err = %v, want the GODEBUG refusal", err)
	}
	if out != "" {
		t.Fatalf("accounts ls printed %q before refusing", out)
	}

	if _, _, err := execute(t, "accounts", "clear", "acme-personal", "--yes"); err == nil || ExitCode(err) != ExitUserError ||
		!strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("accounts clear: err = %v, want the GODEBUG refusal", err)
	}
	if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != "broken" || m["since"] != since {
		t.Fatalf("a clear refused for GODEBUG wrote anyway: %v", m)
	}
}

// TestAccountsClearRefusesNoAgentsSession: the clear takes no agent-marker
// refusal, and that is a decision rather than an omission. The commands that
// refuse one — fugaro allow-delete and fugaro managed flag — are the ones
// whose own output is an approval of a later cloud change, read back by a
// gate; this command approves nothing and records no owner decision, and its
// neighbours are fugaro budget set and fugaro budget kill, the same write to
// the same database with the same person's credentials, which refuse no agent
// either. What it can do is take off a hold the service placed on a credential
// whose token the operator has replaced, and the service marks it broken again
// if the new token is refused too. The test is here so that a later change to
// the decision is a deliberate one.
func TestAccountsClearRefusesNoAgentsSession(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	since := time.Now().UTC().Add(-time.Minute).Truncate(time.Second).Format(time.RFC3339Nano)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{"state": "broken", "since": since})
	noTerminal(t)
	t.Setenv("CLAUDECODE", "1") // a coding agent's session

	if _, _, err := execute(t, "accounts", "clear", "acme-personal", "--yes"); err != nil {
		t.Fatalf("a coding agent's session was refused: %v (the decision is that it is not, like fugaro budget set and kill)", err)
	}
	if m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any); m["state"] != "available" {
		t.Fatalf("the mark was not cleared: %v", m)
	}
}

// markFlip is the operator's terminal in the test below: the first time the
// command reads the confirmation, it changes the stored mark — exactly where
// the service marking the credential at that same moment would — and then
// answers the confirmation with the project's name.
type markFlip struct {
	db   *gcpfake.RTDB
	path string
	mark map[string]any
	line string
	done bool
}

func (m *markFlip) Read(p []byte) (int, error) {
	if !m.done {
		m.done = true
		m.db.Set(m.path, m.mark)
	}
	if m.line == "" {
		return 0, io.EOF
	}
	n := copy(p, m.line)
	m.line = m.line[n:]
	return n, nil
}

// The clear re-decides on the value it writes under, not on the one it read
// (creds.RTDBStore.UpdateMark's callback runs under the ETag): a credential
// the service marked between this command's read and its write keeps the
// service's state, because a clear meant for "broken" must not take off a
// hold placed a moment later.
func TestAccountsClearReDecidesUnderTheWrite(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	now := time.Now().UTC().Truncate(time.Second)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "broken", "since": now.Add(-time.Minute).Format(time.RFC3339Nano)})
	typed(t)

	// The state the service writes while the operator is answering: one this
	// Fugaro must not clear (a probe is what ends a backoff, C1.12).
	held := map[string]any{"state": "backed_off", "until": now.Add(15 * time.Minute).Format(time.RFC3339Nano)}
	in := &markFlip{db: f.db, path: credsMarkPath("acme-personal"), mark: held, line: "aurora\n"}
	if _, _, err := executeStdinReader(t, in, "accounts", "clear", "acme-personal"); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: the state under the write is not one to clear", err)
	}
	if !in.done {
		t.Fatal("the command never read the confirmation, so the mark was never changed under it")
	}
	m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any)
	if m["state"] != "backed_off" || m["until"] != held["until"] {
		t.Fatalf("mark = %v, want the service's own write untouched", m)
	}
}

// The state the write lands on must be the state the operator CONFIRMED, and
// not merely one of the two an operator may clear. Exhausted and broken are
// different holds — one ends at a reset time by itself, the other is a 401 only
// an operator ends — and the reachable flip is exhausted to broken: creds.Break
// is what the 401 path writes and it takes no previous mark, so an exhausted
// mark becomes broken at any moment, including between the read the
// confirmation was worded from and the write. (Broken to exhausted is not a
// transition this release makes — Exhaust and ProbeFailed return a broken mark
// unchanged, ProbeSucceeded applies only to a backed-off one — and that refusal
// is what keeps a 429 or a probe from taking a 401 off; the guard below is the
// same direction, not its opposite.) A clear confirmed for "exhausted until
// 14:00" that landed on a broken mark the service recorded meanwhile would take
// off the newer hold and put a refused token back in the cascade's rotation:
// the same failure the test above covers, one state over, and the one the
// confirmation's own words ("clear the exhausted mark of …") do not cover.
func TestAccountsClearRefusesTheOtherClearableState(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, claudeAccountsConfig)
	now := time.Now().UTC().Truncate(time.Second)
	f.db.Set(credsMarkPath("acme-personal"), map[string]any{
		"state": "exhausted", "until": now.Add(time.Hour).Format(time.RFC3339Nano)})
	typed(t)

	// The 401 the service saw while the operator was reading the confirmation.
	broken := map[string]any{"state": "broken", "since": now.Format(time.RFC3339Nano)}
	in := &markFlip{db: f.db, path: credsMarkPath("acme-personal"), mark: broken, line: "aurora\n"}
	if _, _, err := executeStdinReader(t, in, "accounts", "clear", "acme-personal"); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: the mark became broken while the operator confirmed an exhausted one", err)
	}
	if !in.done {
		t.Fatal("the command never read the confirmation, so the mark was never changed under it")
	}
	m, _ := f.db.Value(credsMarkPath("acme-personal")).(map[string]any)
	if m["state"] != "broken" || m["since"] != broken["since"] {
		t.Fatalf("mark = %v, want the service's own broken mark untouched", m)
	}
}
