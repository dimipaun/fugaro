package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// accountsPoolCredentials is the owner's half of the pool fixture: four
// credentials, two subscriptions, a key and a route.
const accountsPoolCredentials = `
credentials:
  # the owner's own credentials: two subscriptions, a key and a route
  - name: oauth-one
    type: claude-oauth
  - name: oauth-two
    type: claude-oauth
  - name: key-one
    type: anthropic
  - name: router-one
    type: openrouter
`

// accountsPoolCrafts are the crafts that name them: one a credential, and one
// each of the pools the tests write to, so that only the refusal test has to
// point a craft at a pool.
const accountsPoolCrafts = `
crafts:
  coder:
    model: deepseek-v4-1
    access: router-one
  senior-review:
    model: claude-opus-5-5
    access: oauth-one
`

// accountsPoolCraftsOnPool is the same block with the senior review served by
// the pool `accounts pool rm` must refuse to remove.
const accountsPoolCraftsOnPool = `
crafts:
  coder:
    model: deepseek-v4-1
    access: router-one
  senior-review:
    model: claude-opus-5-5
    access: claude
`

// accountsPoolPools is the pools block, written the block way so the writes
// have bytes to splice into. The comments — above the first pool, above the
// members it walks and beside its last one — are the fixture's evidence that an
// edit goes through internal/yamledit and touches nothing but the lines it
// rewrites: a write that round-tripped the decoded config would drop them.
const accountsPoolPools = `
pools:
  # the owner's pool: a subscription and then the key
  - name: claude
    strategy: failover
    # the order it walks them: the subscription first
    members:
      - oauth-one
      - key-one   # the paid one
  - name: routes
    members:
      - router-one
      - oauth-one
`

// accountsPoolExtra is the whole fixture: the credentials, the crafts and the
// pools.
const accountsPoolExtra = accountsPoolCredentials + accountsPoolCrafts + accountsPoolPools

// accountsPoolHome is the pool fixture: project aurora in a config home of its
// own, its config the fixture above plus the caller's own YAML, selected, and
// no coding-agent marker set — the session these tests run in is not the one
// TestPoolWritesRefuseInAgentSession is about.
func accountsPoolHome(t *testing.T, extra string) string {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	settingsFixture(t, accountsPoolExtra+extra)
	return os.Getenv("FUGARO_CONFIG")
}

// accountsPoolHomeWith is accountsPoolHome without the fixture's own pools
// block, for the states a pools block is written in that a write has to refuse.
func accountsPoolHomeWith(t *testing.T, local string) string {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	settingsFixture(t, local)
	return os.Getenv("FUGARO_CONFIG")
}

// accountsPoolBlock is the pools block of a written config, from its key to the
// first line that is not part of it, so a test reads the pools without reading
// the rest of the file.
func accountsPoolBlock(t *testing.T, path string) string {
	t.Helper()
	file := readFileString(t, path)
	i := strings.Index(file, "\npools:\n")
	if i < 0 {
		t.Fatalf("the file has no pools block:\n%s", file)
	}
	lines := strings.Split(file[i+1:], "\n")
	for n, line := range lines[1:] {
		if line != "" && !strings.HasPrefix(line, " ") {
			return strings.Join(lines[:n+1], "\n") + "\n"
		}
	}
	return file[i+1:]
}

// TestPoolAddReplacesTheWholeMemberListInPlace is the storage half of the
// slice: a pool is its members in their order (D1632, spec §2), the write
// replaces the list rather than growing it — a member left out is dropped — and
// it lands in the file the owner wrote, comments and key order included, with
// no strategy flag given and the strategy the pool already has left alone.
func TestPoolAddReplacesTheWholeMemberListInPlace(t *testing.T) {
	path := accountsPoolHome(t, "")

	out, _, err := execute(t, "accounts", "pool", "add", "claude", "key-one", "oauth-two", "oauth-one", "--yes")
	if err != nil {
		t.Fatalf("accounts pool add: %v\n%s", err, out)
	}
	want := `pools:
  # the owner's pool: a subscription and then the key
  - name: claude
    strategy: failover
    # the order it walks them: the subscription first
    members:
      - key-one
      - oauth-two
      - oauth-one   # the paid one
  - name: routes
    members:
      - router-one
      - oauth-one
`
	if got := accountsPoolBlock(t, path); got != want {
		t.Errorf("the pools block is\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(out, "declare pools.claude.members = [key-one, oauth-two, oauth-one], in this order, keeping the strategy the pool has (failover when it is new)") {
		t.Errorf("the sentence does not name the whole list in order:\n%s", out)
	}
}

// TestPoolAddWritesANewPoolWithFailoverByDefault is D1632's answer, pinned: a
// new pool names no strategy, which is failover — the order it lists is the
// order it tries — and the pool appears beside the ones already there rather
// than replacing the block.
func TestPoolAddWritesANewPoolWithFailoverByDefault(t *testing.T) {
	path := accountsPoolHome(t, "")

	_, _, err := execute(t, "accounts", "pool", "add", "fresh", "oauth-one", "key-one", "--yes")
	if err != nil {
		t.Fatalf("accounts pool add: %v", err)
	}
	block := accountsPoolBlock(t, path)
	if !strings.Contains(block, "  - name: fresh\n    members:\n      - oauth-one\n      - key-one\n") {
		t.Errorf("the new pool is not in the block as a failover pool:\n%s", block)
	}
	if !strings.Contains(block, "  - name: claude\n") || !strings.Contains(block, "  - name: routes\n") {
		t.Errorf("the write replaced the block instead of adding to it:\n%s", block)
	}
	// The file still reads back as a config, with the new pool in it.
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the written config does not read back: %v", err)
	}
	for _, p := range lc.Pools {
		if p.Name == "fresh" && p.StrategyOrDefault() == "failover" {
			return
		}
	}
	t.Errorf("the new pool did not read back as a failover pool: %+v", lc.Pools)
}

// TestPoolAddStrategyFlag: --strategy is written when it is given, and a
// strategy outside the vocabulary is refused with the config's own sentence
// before anything is written.
func TestPoolAddStrategyFlag(t *testing.T) {
	path := accountsPoolHome(t, "")

	_, _, err := execute(t, "accounts", "pool", "add", "fresh", "oauth-one", "oauth-two", "--strategy", "round-robin", "--yes")
	if err != nil {
		t.Fatalf("accounts pool add --strategy: %v", err)
	}
	if block := accountsPoolBlock(t, path); !strings.Contains(block, "  - name: fresh\n    strategy: round-robin\n") {
		t.Errorf("the strategy the flag named was not written:\n%s", block)
	}

	before := readFileString(t, path)
	out, _, err := execute(t, "accounts", "pool", "add", "fresh", "oauth-one", "--strategy", "spread", "--yes")
	if err == nil {
		t.Fatalf("a strategy outside the vocabulary was written:\n%s", out)
	}
	if !strings.Contains(err.Error(), "must be one of failover, round-robin") {
		t.Errorf("the refusal does not name the strategies there are: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestPoolAddRefusesWhatTheBlockRefuses pins the rules the write inherits from
// config.ValidatePools — a member the document does not define, a member listed
// twice, and the name a credential and a pool share, which may mean only one of
// them — and the one the built-in credential's name carries. Each refusal
// leaves the file byte-identical.
func TestPoolAddRefusesWhatTheBlockRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"an undefined member", "no credential of this name is defined in this block", []string{"claude", "oauth-one", "nowhere"}},
		{"a member listed twice", "this credential is listed twice", []string{"claude", "oauth-one", "oauth-one"}},
		{"a name a credential has", "a credential of this name is defined in the same block", []string{"oauth-one", "oauth-two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsPoolHome(t, "")
			before := readFileString(t, path)

			args := append(append([]string{"accounts", "pool", "add"}, tc.args...), "--yes")
			out, _, err := execute(t, args...)
			if err == nil {
				t.Fatalf("the write was not refused:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is not the block's own sentence %q: %v", tc.want, err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestPoolAddAcceptsTheBuiltInVertex: vertex is a credential every installation
// has without defining one, so it is a member like any other — the spec's own
// shape is a pool of subscriptions with the built-in as its last fallback
// (D1624). It is the ONE name a pool may list that this config does not define.
func TestPoolAddAcceptsTheBuiltInVertex(t *testing.T) {
	path := accountsPoolHome(t, "")

	out, _, err := execute(t, "accounts", "pool", "add", "routes", "oauth-one", "vertex", "--yes")
	if err != nil {
		t.Fatalf("accounts pool add with vertex: %v\n%s", err, out)
	}
	block := accountsPoolBlock(t, path)
	if !strings.Contains(block, "  - name: routes\n    members:\n      - oauth-one\n      - vertex\n") {
		t.Errorf("the built-in was not written as the pool's last member:\n%s", block)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the written config does not read back: %v", err)
	}
	paths, unplaced := config.PoolServingPaths(lc.Pools[1], lc.Credentials)
	if len(unplaced) != 0 || len(paths) != 2 {
		t.Errorf("a pool of a subscription and the built-in is served by %q (unplaced %q), want both paths", paths, unplaced)
	}
}

// TestPoolAddRefusesAFlowMemberList: yamledit has no bytes for splicing a block
// sequence into a flow collection, and the refusal is that package's own — the
// same one `settings set --add` prints — rather than a pool being written over
// a shape the owner chose.
func TestPoolAddRefusesAFlowMemberList(t *testing.T) {
	const flow = accountsPoolCredentials + "\npools:\n  - { name: flow, members: [oauth-one] }\n"
	path := accountsPoolHomeWith(t, flow)
	before := readFileString(t, path)

	out, _, err := execute(t, "accounts", "pool", "add", "flow", "oauth-one", "key-one", "--yes")
	if err == nil {
		t.Fatalf("a flow member list was written:\n%s", out)
	}
	if !strings.Contains(err.Error(), "pools.flow") {
		t.Errorf("the refusal does not name the pool it could not write: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestPoolRmDropsThePoolAndAnEmptyBlock: the write removes the pool and, when
// it was the only one, the key the block hung from — a `pools:` with nothing
// under it is a shape the loader reads as an empty block, and leaving it would
// be a change the owner did not ask for. The two pools go one at a time, and
// the second removal is the one that empties the block.
func TestPoolRmDropsThePoolAndAnEmptyBlock(t *testing.T) {
	path := accountsPoolHome(t, "")

	if _, _, err := execute(t, "accounts", "pool", "rm", "routes", "--yes"); err != nil {
		t.Fatalf("accounts pool rm: %v", err)
	}
	if block := accountsPoolBlock(t, path); strings.Contains(block, "name: routes") {
		t.Errorf("the pool was not removed:\n%s", block)
	}
	out, _, err := execute(t, "accounts", "pool", "rm", "claude", "--yes")
	if err != nil {
		t.Fatalf("accounts pool rm (last): %v\n%s", err, out)
	}
	after := readFileString(t, path)
	if strings.Contains(after, "\npools:") {
		t.Errorf("the empty pools block was left behind:\n%s", after)
	}
	if !strings.Contains(after, "# the owner's own credentials: two subscriptions, a key and a route") {
		t.Errorf("the removal dropped a comment it was not asked for:\n%s", after)
	}
	if _, err := localcfg.Parse([]byte(after)); err != nil {
		t.Errorf("the written config does not read back: %v", err)
	}
}

// TestPoolRmRefusesWhileACraftNamesThePool: a craft whose access names the pool
// would be left pointing at nothing — no credential would serve its calls, and
// the config would load with an access that resolves to no grant. The refusal
// names the craft, and nothing is written.
func TestPoolRmRefusesWhileACraftNamesThePool(t *testing.T) {
	path := accountsPoolHomeWith(t, accountsPoolCredentials+accountsPoolCraftsOnPool+accountsPoolPools)
	before := readFileString(t, path)

	out, _, err := execute(t, "accounts", "pool", "rm", "claude", "--yes")
	if err == nil {
		t.Fatalf("the pool a craft names was removed:\n%s", out)
	}
	if !strings.Contains(err.Error(), "crafts.senior-review.access names pools.claude") {
		t.Errorf("the refusal does not name the craft that would be left with nothing: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestPoolRmRefusesAPoolThatIsNotDefined: a typo is refused with the names the
// config does define, rather than being written as a removal that matched
// nothing.
func TestPoolRmRefusesAPoolThatIsNotDefined(t *testing.T) {
	path := accountsPoolHome(t, "")
	before := readFileString(t, path)

	_, _, err := execute(t, "accounts", "pool", "rm", "claud", "--yes")
	if err == nil {
		t.Fatal("an undefined pool was removed without a complaint")
	}
	for _, want := range []string{"pools.claud is not defined in", "claude", "routes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestPoolWritesRefuseInAgentSession is the pinned rule of this module's plan
// (F22, D1636): a pool decides where a project's calls may be spent and whose
// key they may carry, so a coding agent's session writes neither the pool nor
// its removal, --yes included — the diff is printed, the command to run at a
// terminal is named, and the file is left alone. The loop is over
// agentenv.Markers itself rather than a copy, so a marker the tree adds is one
// these writes are held to.
func TestPoolWritesRefuseInAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		for _, tc := range []struct {
			name    string
			args    []string
			command string
		}{
			{"add", []string{"accounts", "pool", "add", "fresh", "oauth-one"}, "fugaro accounts pool add fresh oauth-one"},
			{"rm", []string{"accounts", "pool", "rm", "routes"}, "fugaro accounts pool rm routes"},
		} {
			t.Run(marker+"/"+tc.name, func(t *testing.T) {
				path := accountsPoolHome(t, "")
				before := readFileString(t, path)
				t.Setenv(marker, "1")

				out, _, err := execute(t, append(tc.args, "--yes")...)
				if err == nil {
					t.Fatalf("the write landed in a coding agent's session:\n%s", out)
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
				}
				if !strings.Contains(err.Error(), marker) {
					t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
				}
				if !strings.Contains(err.Error(), tc.command) {
					t.Errorf("the refusal does not print the command to run at a terminal (%q): %v", tc.command, err)
				}
				if !strings.Contains(out, path) || !(strings.Contains(out, "  + ") || strings.Contains(out, "  - ")) {
					t.Errorf("the refusal does not print the diff of what it refused:\n%s", out)
				}
				if got := readFileString(t, path); got != before {
					t.Errorf("the file changed in a coding agent's session:\n%s", got)
				}
			})
		}
	}
}

// accountsPoolLsPools is the read's own fixture: a round-robin pool of two
// subscriptions (the credentials service alone), a pool of a route and the
// built-in vertex (a run's own gateway alone), and one that lists a member of
// each, which is the pool that can only fail over at a stage boundary.
const accountsPoolLsPools = `
pools:
  - name: subs
    strategy: round-robin
    members:
      - oauth-one
      - oauth-two
  - name: routes
    members:
      - router-one
      - vertex
  - name: mixed
    members:
      - oauth-one
      - router-one
`

// TestPoolLsReadsEachMemberAndThePathThatServesIt is the read: each pool's
// strategy, its members in order, and which half of a run serves them — the
// credentials service for the Anthropic and OAuth members, a run's own gateway
// for the OpenRouter and Vertex ones, and both, failing over at a stage
// boundary, for a pool that lists one of each (F7, D1634). It is offline: the
// fixture has no endpoints and no budget backend, so a read that reached the
// cloud would fail rather than print.
func TestPoolLsReadsEachMemberAndThePathThatServesIt(t *testing.T) {
	accountsPoolHomeWith(t, accountsPoolCredentials+accountsPoolLsPools)

	out, _, err := execute(t, "accounts", "pool", "ls")
	if err != nil {
		t.Fatalf("accounts pool ls: %v\n%s", err, out)
	}
	for _, want := range []string{
		"NAME    STRATEGY     MEMBERS                SERVED BY",
		"subs    round-robin  oauth-one, oauth-two   the credentials service",
		"routes  failover     router-one, vertex     a run's own gateway",
		"mixed   failover     oauth-one, router-one  the credentials service and a run's own gateway: failover at a stage boundary",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the read does not carry %q:\n%s", want, out)
		}
	}
}

// TestPoolLsSaysWhenNobodySelectedAProject: the read is the same selected
// config `fugaro craft ls` reads, and a machine with no selection says so
// rather than printing an empty table nobody can tell from "no pools".
func TestPoolLsSaysWhenNobodySelectedAProject(t *testing.T) {
	isolateProjects(t, t.TempDir())
	out, _, err := execute(t, "accounts", "pool", "ls")
	if err != nil {
		t.Fatalf("accounts pool ls: %v", err)
	}
	if !strings.Contains(out, "no project config selected") {
		t.Errorf("the read does not say why it has nothing to show:\n%s", out)
	}
}

// TestPoolFamilyUnknownSubcommand: the family names its own children when it is
// given one it does not have, the way every other subcommand family here does.
func TestPoolFamilyUnknownSubcommand(t *testing.T) {
	_, _, err := execute(t, "accounts", "pool", "wipe")
	if err == nil {
		t.Fatal("an unknown pool subcommand was accepted")
	}
	if !strings.Contains(err.Error(), "want ls, add or rm") {
		t.Errorf("the refusal does not name the subcommands there are: %v", err)
	}
}

// TestAccountsAddTypeRefusesATypeNoRunCouldServe: --type is a definition's own
// type, so the config's rule judges it — the built-in vertex is never defined,
// and the two types the spec calls future work are refused in words that say so
// — and the types whose key no command stores yet are refused rather than
// half-declared: a credentials entry with no secret is inert config that a pool
// would still be allowed to list.
func TestAccountsAddTypeRefusesATypeNoRunCouldServe(t *testing.T) {
	for _, tc := range []struct{ typ, want string }{
		{"vertex", "vertex is built in: it needs no secret and exists in every installation, so it is never defined as an entry"},
		{"openai", "openai is not yet supported"},
		{"codex-oauth", "codex-oauth is not yet supported"},
		{"bedrock", "must be one of openrouter, anthropic, claude-oauth"},
		{"anthropic", "no command stores one under a credential's own name yet"},
		{"openrouter", "no command stores one under a credential's own name yet"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			path := accountsPoolHome(t, "")
			before := readFileString(t, path)

			out, _, err := execute(t, "accounts", "add", "team-a", "--repos", "acme/app", "--type", tc.typ, "--yes")
			if err == nil {
				t.Fatalf("--type %q was accepted:\n%s", tc.typ, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// accountsPoolTypedLocal is the fixture for the refusal below: the account's
// name is already a credential in the config, under the type whose key that
// credential authenticates with, and a pool walks it.
const accountsPoolTypedLocal = addRigLocal + `
credentials:
  - name: key-one
    type: anthropic
pools:
  - name: paid
    members:
      - key-one
`

// accountsPoolTypedLocalSameType is that fixture with the credential defined as
// the type the flag names: the state the refusal must leave alone.
const accountsPoolTypedLocalSameType = addRigLocal + `
credentials:
  - name: key-one
    type: claude-oauth
`

// TestAccountsAddTypeRefusesToRetypeACredential is the other half of --type's
// rule, and it is about what the flag may NOT do: the entry is the owner's —
// this command adds the account beside it, not a new meaning for it. A retype
// is not a declaration: every pool that lists the name would be served on the
// other tier (for claude-oauth, into D1622's subscription switch), the sentence
// would read as a plain declaration, and `accounts rm` would afterwards read
// the entry as the account's own and remove a definition this command never
// wrote. So the name is refused, with the type it already has, and the file is
// byte for byte what it was.
func TestAccountsAddTypeRefusesToRetypeACredential(t *testing.T) {
	path := accountsAddFixture(t, accountsPoolTypedLocal)
	before := readFileString(t, path)

	_, _, addErr := executeStdin(t, "", "accounts", "add", "key-one", "--repos", "owner/repo-a", "--type", "claude-oauth", "--yes")

	if addErr == nil {
		t.Fatalf("the credential key-one, defined as anthropic, was retyped as claude-oauth")
	}
	if !strings.Contains(addErr.Error(), "credentials.key-one is already defined as anthropic; nothing was written") {
		t.Errorf("the refusal reads %v, want the type the entry already has named", addErr)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestAccountsAddTypeKeepsASameTypedCredential is that refusal's other side:
// the name already defined as the very type --type names is not a retype, so
// the entry stays exactly as the owner wrote it and the account is declared
// beside it.
func TestAccountsAddTypeKeepsASameTypedCredential(t *testing.T) {
	path := accountsAddFixture(t, accountsPoolTypedLocalSameType)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "key-one", "--repos", "owner/repo-a", "--type", "claude-oauth", "--yes")
	if err != nil {
		t.Fatalf("accounts add --type claude-oauth beside a claude-oauth credential: %v\n%s", err, out)
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lc.Credentials) != 1 || lc.Credentials[0].Name != "key-one" || lc.Credentials[0].Type != config.CredentialTypeClaudeOAuth {
		t.Errorf("the credentials block is %v, want key-one alone, still claude-oauth", lc.Credentials)
	}
	if _, ok := lc.ClaudeAccounts["key-one"]; !ok {
		t.Errorf("the account was not declared beside the entry: %v", lc.ClaudeAccounts)
	}
}

// TestAccountsAddTypeWritesTheCredentialEntry is --type's positive half: the
// name is declared as a typed credential beside the claude_accounts entry, in
// the same edit, which is what lets a pool list it as a member. The evidence is
// the diff of this command's own write, printed before the converge, because
// the stages that follow write the file again the way `fugaro update` does
// (TestAccountsAddKeepsCommentsAndOrder's own reading).
func TestAccountsAddTypeWritesTheCredentialEntry(t *testing.T) {
	accountsAddFixture(t, accountsAddLocal)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a", "--repos", "acme/app", "--type", "claude-oauth", "--yes")
	if err != nil {
		t.Fatalf("accounts add --type: %v\n%s", err, out)
	}
	for _, want := range []string{
		"  + credentials:",
		"  +   - name: team-a",
		"  +     type: claude-oauth",
		"declare credentials.team-a.type = claude-oauth; declare claude_accounts.team-a.allow_data_to = [acme/app]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the write does not carry %q:\n%s", want, out)
		}
	}
}
