package cli

import (
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// This file is `fugaro accounts switch FROM TO`: the one command that moves
// every key naming an account, which the owner's own belong -> bmb move of
// 2026-11-16 took nine hand edits and an `fugaro update` after them.

// switchHead is the part of the local config no case varies. The file is written
// whole rather than appended to (budgetFixture.appendConfig), because the base
// config already writes a repos: block and a second one is a duplicate key the
// strict decoder refuses.
const switchHead = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: unused-bucket
user: someone@example.com
endpoints: { no_auth: true }
`

// switchChoiceTail is the tail every case that needs all five keys shares: two
// accounts that cover the one repository, and the default, a stage, a craft, the
// repository's own list and a profile's list all naming team-a — the whole of
// what the owner moved by hand.
const switchChoiceTail = `claude_accounts:
  team-a: { allow_data_to: [acme/app] }
  team-b: { allow_data_to: [acme/app] }
default_claude_account: team-a
stages:
  review: { claude_account: team-a }
crafts:
  coder: { access: team-a }
profiles:
  standard:
    provider: github
    allow_repos: [github/acme/app]
    claude_accounts: [team-a]
repos:
  acme/app:
    provider: github
    base_branch: main
    workflows: [web]
    claude_accounts:
      - team-a
`

// switchKeys are the five keys that tail names team-a in, as the command prints
// them: the whole of what a switch is for, and the reason it is one command
// rather than nine edits.
var switchKeys = []string{
	"default_claude_account",
	"stages.review.claude_account",
	"crafts.coder.access",
	"repos.acme/app.claude_accounts",
	"profiles.standard.claude_accounts",
}

// switchRigKeys is the same list for the converge rig, whose repository is
// owner/repo-a and which sets two crafts' access — the profile of a key is what
// a switch is judged on, and the rig's own file is the one an installation
// really holds.
var switchRigKeys = []string{
	"default_claude_account",
	"stages.review.claude_account",
	"crafts.coder.access",
	"crafts.review.access",
	"repos.owner/repo-a.claude_accounts",
	"profiles.standard.claude_accounts",
}

// switchFixture writes this machine's local project config, hermetic in the
// budget fixture's own temporary XDG: the head, the budget block the fixture's
// database answers to, and the caller's tail.
func switchFixture(t *testing.T, tail string) *budgetFixture {
	t.Helper()
	f := newBudgetFixture(t, "")
	cfg := switchHead +
		"budget: { mode: observe, per_run_usd: 5, firebase_project: proj-1234, rtdb_url: " + f.db.URL + " }\n" +
		tail
	if err := os.WriteFile(os.Getenv("FUGARO_CONFIG"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// switchFile is the local config's bytes as the file holds them: every refusal
// below is asserted against the file itself, so a refusal that wrote anything is
// red wherever a command printed nothing.
func switchFile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestAccountsSwitchWritesEveryKeyAndConverges is the command end to end: one
// edit of the local config that moves all five keys, and the three stages
// `fugaro update` runs, in-process.
//
// The published node is seeded with the members the switch REPLACES — the
// account every key named before the command ran — because that is the state the
// command exists for: a node naming another account is a publish that changes
// the project's account choice, and the command has to carry that change through
// with the owner's own command as the confirmation (D2097, F10) and with nothing
// left to run afterwards (the HARD RULE).
//
// What the assertions are about is the nine edits: every key the owner moved by
// hand, moved by one command, with the two blocks that are NOT the choice left
// standing.
func TestAccountsSwitchWritesEveryKeyAndConverges(t *testing.T) {
	r := newAddRigWith(t, switchRigLocal)
	node := addPolicyNode()
	node["default_claude_account"] = "team-a"
	node["claude_account_stages"] = map[string]any{"review": "team-a"}
	r.db.Set(projectpolicy.PathShared(), node)

	out, _, err := execute(t, "accounts", "switch", "team-a", "team-b", "--yes")
	if err != nil {
		t.Fatalf("accounts switch: %v\nstdout:\n%s", err, out)
	}
	lc := r.addLocal(t)
	if got := lc.DefaultClaudeAccount; got != "team-b" {
		t.Errorf("default_claude_account = %q, want team-b", got)
	}
	if got := lc.ClaudeAccountStages["review"].ClaudeAccount; got != "team-b" {
		t.Errorf("stages.review.claude_account = %q, want team-b", got)
	}
	if got := lc.Crafts.Coder.Access; got != "team-b" {
		t.Errorf("crafts.coder.access = %q, want team-b", got)
	}
	if got := lc.Repos["owner/repo-a"].ClaudeAccounts; !slices.Equal(got, []string{"team-b"}) {
		t.Errorf("repos.owner/repo-a.claude_accounts = %v, want [team-b]", got)
	}
	if got := lc.Profiles["standard"].ClaudeAccounts; !slices.Equal(got, []string{"team-b"}) {
		t.Errorf("profiles.standard.claude_accounts = %v, want [team-b]", got)
	}
	// Both accounts stay declared: a switch moves which key names which account
	// and takes no token away, which is what makes it reversible.
	for _, name := range []string{"team-a", "team-b"} {
		if _, ok := lc.ClaudeAccounts[name]; !ok {
			t.Errorf("claude_accounts.%s is gone: a switch leaves the block alone", name)
		}
	}
	p := r.policy(t)
	if p.DefaultClaudeAccount != "team-b" {
		t.Errorf("the node's default_claude_account = %q, want team-b: the node has to carry the account the keys now name", p.DefaultClaudeAccount)
	}
	if got := p.ClaudeAccountStages["review"]; got != "team-b" {
		t.Errorf("the node's claude_account_stages.review = %q, want team-b", got)
	}
	// One line per key, before the converge, is what replaces the hand edits.
	for _, key := range switchRigKeys {
		if !strings.Contains(out, key+": team-a -> team-b") {
			t.Errorf("the run does not print the line for %s:\n%s", key, out)
		}
	}
	if !strings.Contains(out, "Nothing else to run") {
		t.Errorf("the run does not close on the one thing left to do, which is nothing:\n%s", out)
	}
	if !strings.Contains(out, "a run already going keeps the account its launch resolved") {
		t.Errorf("the run does not say what a run in flight keeps (docs/design/update-in-flight.md):\n%s", out)
	}
}

// switchRigLocal is the converge rig's local config: the installation rig's own
// shape (two accounts, a profile serving them, one GitHub repository) with every
// choice key naming team-a, which is the account the command moves off. The
// profile lists team-a alone, so the switch takes its single entry over and the
// subset rule the write is judged by is asked of team-b.
const switchRigLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
claude_accounts:
  team-a: { allow_data_to: [owner/repo-a] }
  team-b: { allow_data_to: [owner/repo-a] }
default_claude_account: team-a
stages:
  review: { claude_account: team-a }
crafts:
  coder: { access: team-a }
  review: { access: team-a }
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
    claude_accounts: [team-a]
repos:
  owner/repo-a:
    provider: github
    github_app_id: "12345"
    workflows: [web]
    claude_accounts:
      - team-a
`

// TestAccountsSwitchWritesRefusedInAgentSession: a switch decides which
// credential this project's stages spend, so it is the owner's own step at their
// own terminal and a coding agent's session is refused whatever --yes says
// (D2097, the no-prompt policy D1540). The loop is over agentMarkers itself
// rather than a copy, so a marker the tree adds is one this command is held to.
//
// What the sentence has to carry is the marker as well as the command, and what
// has to be empty is stdout: the write path's own fence is a second one behind
// this, and it names the same marker and the same command, so a case that only
// read the outcome would stay green over a deleted call site (applySettingsEdit's
// refusal is what answers the write form then). The two forms that path never
// sees are the ones that carry the call site on their own — --dry-run and the
// form whose plan is empty — and both read the config, the access gate and the
// plan before printing, which is what D2097 keeps a session out of. The file is
// byte-identical afterwards, and nothing is printed at all — the refusal is the
// first thing the command does, which is why no diff is offered here.
func TestAccountsSwitchWritesRefusedInAgentSession(t *testing.T) {
	switchFixture(t, switchChoiceTail)
	before := switchFile(t)
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			t.Setenv(marker, "1")

			for _, tc := range []struct {
				name string
				args []string
			}{
				{"the write", []string{"accounts", "switch", "team-a", "team-b", "--yes"}},
				{"--dry-run", []string{"accounts", "switch", "team-a", "team-b", "--dry-run"}},
				// team-b is declared and its allow_data_to covers every
				// repository the keys serve, so nothing but the guard refuses
				// this one: without it the command would print the "no change"
				// line and exit 0.
				{"nothing names FROM", []string{"accounts", "switch", "team-b", "team-a"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					from, to := tc.args[2], tc.args[3]
					out, errOut, err := execute(t, tc.args...)
					if err == nil {
						t.Fatalf("the switch was admitted in a coding agent's session:\n%s\n%s", out, errOut)
					}
					if ExitCode(err) != ExitUserError {
						t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
					}
					// The guard's own sentence, and not only its shape: a
					// refusal from anywhere else would name another step.
					if !strings.Contains(err.Error(), "refusing to switch a model account") {
						t.Errorf("the refusal is not this command's own (accountsSwitchSession): %v", err)
					}
					if !strings.Contains(err.Error(), marker) {
						t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
					}
					if !strings.Contains(err.Error(), "fugaro accounts switch "+from+" "+to) {
						t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
					}
					if out != "" {
						t.Errorf("the refusal printed the plan a person would read:\n%s", out)
					}
					if after := switchFile(t); after != before {
						t.Errorf("the refusal wrote the file anyway:\n%s", after)
					}
				})
			}
		})
	}
}

// TestAccountsSwitchRefusesAliasPoolAndPoolMember: FROM has to be a name a
// choice key may hold as the account itself. An alias is the indirection such a
// key is meant to keep naming — `accounts alias set` moves every key naming one
// in a single edit, which is what the alias is for — and a pool or a pool's
// member is chosen INSIDE a run, at the stage boundary, so a switch of a craft
// naming one would re-resolve what a run spends while it is going (D2094).
//
// Each refusal is checked to leave the file alone and to name the command that
// settles it, because a refusal that printed the block's own name and no command
// would leave the person where the nine hand edits started.
func TestAccountsSwitchRefusesAliasPoolAndPoolMember(t *testing.T) {
	switchFixture(t, switchChoiceTail+`credentials:
  - name: team-cred
    type: claude-oauth
claude_account_aliases:
  reviewer: team-b
pools:
  - name: claude-pool
    strategy: failover
    members: [team-cred]
`)
	before := switchFile(t)
	for _, tc := range []struct{ name, from, want string }{
		{"alias", "reviewer", "fugaro accounts alias set reviewer team-b"},
		{"pool", "claude-pool", "fugaro accounts pool"},
		{"pool-member", "team-cred", "pools.claude-pool.members"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := execute(t, "accounts", "switch", tc.from, "team-b", "--yes")
			if err == nil {
				t.Fatalf("accounts switch %s team-b was admitted:\n%s\n%s", tc.from, out, errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name %q: %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal does not say nothing was written: %v", err)
			}
			if after := switchFile(t); after != before {
				t.Errorf("the refusal wrote the file anyway:\n%s", after)
			}
		})
	}
}

// TestAccountsSwitchRefusesATargetItMayNotUse: TO is a declaration this file
// already holds — an account of the claude_accounts block, never an alias
// standing for one and never a name nothing here defines — because the switch
// moves the keys onto an account this project may already spend, and a name that
// is not one would leave the file the loader refuses. "default" is the third
// case of that rule and not a fourth kind: the unsuffixed secret is no
// claude_accounts entry, so it has no allow_data_to for the subset rule to read.
func TestAccountsSwitchRefusesATargetItMayNotUse(t *testing.T) {
	switchFixture(t, switchChoiceTail+`claude_account_aliases:
  reviewer: team-b
`)
	before := switchFile(t)
	for _, tc := range []struct{ name, to, want string }{
		{"alias", "reviewer", "claude_account_aliases.reviewer is an alias"},
		{"undeclared", "nope", "is not an account this config declares"},
		{"default", "default", "is not an account this config declares"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := execute(t, "accounts", "switch", "team-a", tc.to, "--yes")
			if err == nil {
				t.Fatalf("accounts switch team-a %s was admitted:\n%s\n%s", tc.to, out, errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if after := switchFile(t); after != before {
				t.Errorf("the refusal wrote the file anyway:\n%s", after)
			}
		})
	}
}

// TestAccountsSwitchRefusesAnAccountThatDoesNotCoverTheRepository: TO's own
// allow_data_to has to cover every repository the keys it takes over serve,
// because a repository's own claude_accounts list is what its runs are admitted
// to spend, and a profile that lists an account covers every repository the
// profile admits — the intersection of the two and never their union. Moving
// either entry onto an account whose token may not reach the repository would
// write a config the very next publish refuses.
//
// The two cases are the two list keys, and the sentence each one prints is what
// says which half refused: the repository's own list names the repository, and
// the profile's names the profile. Both remedies are `accounts allow`, because
// widening the account's own list is the one edit that settles either.
func TestAccountsSwitchRefusesAnAccountThatDoesNotCoverTheRepository(t *testing.T) {
	for _, tc := range []struct{ name, tail, want string }{
		{
			// No profile lists accounts at all, so the repository's own list is
			// the only key the write has to judge.
			name: "repos",
			tail: `claude_accounts:
  team-a: { allow_data_to: [acme/app] }
  team-c: { allow_data_to: [acme/other] }
default_claude_account: team-a
repos:
  acme/app:
    provider: github
    base_branch: main
    workflows: [web]
    claude_accounts:
      - team-a
`,
			want: "repos.acme/app.claude_accounts names team-c",
		},
		{
			name: "profile",
			tail: `claude_accounts:
  team-a: { allow_data_to: [acme/app] }
  team-c: { allow_data_to: [acme/other] }
profiles:
  standard:
    provider: github
    allow_repos: [github/acme/app]
    claude_accounts: [team-a]
`,
			want: "profiles.standard.allow_repos lists github/acme/app",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			switchFixture(t, tc.tail)
			before := switchFile(t)
			out, errOut, err := execute(t, "accounts", "switch", "team-a", "team-c", "--yes")
			if err == nil {
				t.Fatalf("the switch onto an account that does not cover the repository was admitted:\n%s\n%s", out, errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name %q, so the half that refused is not the one this case is about: %v", tc.want, err)
			}
			// The remedy is spelled by accountsAllowCommand, which names the
			// program the run was started as (selfCommand): the assertion is on
			// the command and its arguments, not on the word in front of them.
			if !strings.Contains(err.Error(), "accounts allow team-c acme/app") {
				t.Errorf("the refusal does not name the command that widens the account's list: %v", err)
			}
			if after := switchFile(t); after != before {
				t.Errorf("the refusal wrote the file anyway:\n%s", after)
			}
		})
	}
}

// TestAccountsSwitchRefusesAProfileWhoseCoverageIsUndecidable is the subset
// rule's own precondition, in accountsDenySubsetRule's words: a profile that
// lists accounts writes its own exact allow_repos, and one that writes none or
// writes a wildcard makes "does this account cover it" undecidable repository by
// repository. Both states are ones `project policy publish` refuses, and a
// switch would otherwise write the very config the owner's next publish refuses.
func TestAccountsSwitchRefusesAProfileWhoseCoverageIsUndecidable(t *testing.T) {
	for _, tc := range []struct{ name, allow, want string }{
		{"no-allow-repos", "", "writes no allow_repos"},
		{"wildcard", "    allow_repos: [\"github/acme/*\"]\n", "carries the wildcard github/acme/*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			switchFixture(t, `claude_accounts:
  team-a: {}
  team-b: {}
profiles:
  standard:
    provider: github
`+tc.allow+`    claude_accounts: [team-a]
`)
			before := switchFile(t)
			out, errOut, err := execute(t, "accounts", "switch", "team-a", "team-b", "--yes")
			if err == nil {
				t.Fatalf("the switch was admitted over a profile whose coverage is undecidable:\n%s\n%s", out, errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if after := switchFile(t); after != before {
				t.Errorf("the refusal wrote the file anyway:\n%s", after)
			}
		})
	}
}

// TestAccountsSwitchDryRunWritesNothing: --dry-run is the form that shows the
// work before it is done — the five keys, the same lines the run prints, and the
// diff of the file it would write — and the file is byte-identical afterwards,
// which is the whole of what it promises.
func TestAccountsSwitchDryRunWritesNothing(t *testing.T) {
	switchFixture(t, switchChoiceTail)
	before := switchFile(t)

	out, errOut, err := execute(t, "accounts", "switch", "team-a", "team-b", "--dry-run")
	if err != nil {
		t.Fatalf("accounts switch --dry-run: %v\n%s\n%s", err, out, errOut)
	}
	for _, key := range switchKeys {
		if !strings.Contains(out, key+": team-a -> team-b") {
			t.Errorf("the dry run does not print the line for %s:\n%s", key, out)
		}
	}
	if !strings.Contains(out, "dry run: nothing was written") {
		t.Errorf("the dry run does not say it wrote nothing:\n%s", out)
	}
	if after := switchFile(t); after != before {
		t.Errorf("the dry run wrote the file anyway:\n%s", after)
	}
}

// TestAccountsSwitchSaysWhenNoKeyNamesTheAccount: a switch FROM a name no key
// holds is the "no change" answer rather than a refusal, as `accounts deny`
// answers a state the config has already reached — and it writes nothing, which
// is what the file check is for.
func TestAccountsSwitchSaysWhenNoKeyNamesTheAccount(t *testing.T) {
	switchFixture(t, switchChoiceTail)
	before := switchFile(t)

	out, errOut, err := execute(t, "accounts", "switch", "team-b", "team-a", "--yes")
	if err != nil {
		t.Fatalf("accounts switch onto an account no key names: %v\n%s\n%s", err, out, errOut)
	}
	if !strings.Contains(out, "no change") || !strings.Contains(out, "team-b") {
		t.Errorf("the answer does not say that no key names team-b:\n%s", out)
	}
	if after := switchFile(t); after != before {
		t.Errorf("the no-change answer wrote the file anyway:\n%s", after)
	}
}

// TestAccountsSwitchLeavesAListHoldingBothAccountsAlone is the list rule at its
// sharpest: a repository's list that already names BOTH accounts is a list the
// switch has nothing to do to, and one that names FROM alone keeps every entry
// it does not take over. The editor has no in-place item replacement, so the list
// is written by taking FROM out and appending TO (settingsSetList), and both
// cases are what says that writing loses neither half: an entry the list already
// holds is not doubled, and an entry it holds beside FROM is not dropped.
func TestAccountsSwitchLeavesAListHoldingBothAccountsAlone(t *testing.T) {
	r := newAddRigWith(t, switchListRigLocal)
	out, _, err := execute(t, "accounts", "switch", "team-a", "team-b", "--yes")
	if err != nil {
		t.Fatalf("accounts switch: %v\n%s", err, out)
	}
	after := r.addLocal(t)
	// The repository's own list named BOTH accounts, so the entry the switch
	// takes over is already there: what is written is team-b once, and not the
	// two the list would hold if TO were appended to a list FROM had not left.
	if got := after.Repos["owner/repo-a"].ClaudeAccounts; !slices.Equal(got, []string{"team-b"}) {
		t.Errorf("repos.owner/repo-a.claude_accounts = %v, want [team-b] once — the entry it already held is not doubled", got)
	}
	// The profile's list named another account beside FROM, and that entry is
	// kept: it is read as the set it is (config.AllowedClaudeAccounts sorts it,
	// projectpolicy.DeriveAccounts sorts it, config.ProjectJobAccounts sorts
	// it), so where TO lands among the entries is not a fact about the config —
	// the write appends it rather than putting it where FROM stood.
	got := slices.Sorted(slices.Values(after.Profiles["standard"].ClaudeAccounts))
	if !slices.Equal(got, []string{"team-b", "team-c"}) {
		t.Errorf("profiles.standard.claude_accounts holds %v, want team-b and team-c: an entry beside the one the switch takes over is kept", got)
	}
}

// switchListRigLocal is the converge rig for that one case: the same file with a
// third account, a repository list that already names the target and a profile
// list that names an account beside it.
const switchListRigLocal = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
base_images: { base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc }
claude_accounts:
  team-a: { allow_data_to: [owner/repo-a] }
  team-b: { allow_data_to: [owner/repo-a] }
  team-c: { allow_data_to: [owner/repo-a] }
default_claude_account: team-a
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos:
      - github/owner/repo-a
    claude_accounts: [team-a, team-c]
repos:
  owner/repo-a:
    provider: github
    github_app_id: "12345"
    workflows: [web]
    claude_accounts:
      - team-a
      - team-b
`

// switchFlowTail is the same five keys with one of them inside a one-line flow
// mapping — repos.acme/app on one line, its claude_accounts a flow sequence
// inside it. The command does not write that shape and no writer here does, so
// the case is what a hand-written entry looks like, and the refusal it meets is
// the one the editor's own reason class cannot say to a person.
const switchFlowTail = `claude_accounts:
  team-a: { allow_data_to: [acme/app] }
  team-b: { allow_data_to: [acme/app] }
repos:
  acme/app: { provider: github, base_branch: main, workflows: [web], claude_accounts: [team-a] }
`

// TestAccountsSwitchRefusesAChoiceKeyWrittenAsAFlowMapping: the editor refuses
// every flow shape it cannot splice into rather than re-formatting the line, and
// a list inside a one-line flow mapping is one of them, so the command refuses
// with it and names the style the fix is in — the entry written in block style,
// as docs/project-claude-accounts.md writes it. The refusal is named for the
// key's own entry and not for the list inside it, because the line that has to
// change is the entry's.
//
// Nothing is printed before the refusal: the keys are printed after the edit is
// built, so a run that cannot write says which key refused and no line that
// reads like a move.
func TestAccountsSwitchRefusesAChoiceKeyWrittenAsAFlowMapping(t *testing.T) {
	switchFixture(t, switchFlowTail)
	before := switchFile(t)
	out, errOut, err := execute(t, "accounts", "switch", "team-a", "team-b", "--yes")
	if err == nil {
		t.Fatalf("a key inside a flow mapping was admitted:\n%s\n%s", out, errOut)
	}
	for _, want := range []string{
		"repos.acme/app is written as a one-line flow mapping",
		"write repos.acme/app in block style",
		"nothing was written",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if out != "" {
		t.Errorf("the refusal printed a line that reads like a move:\n%s", out)
	}
	if after := switchFile(t); after != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", after)
	}
}

// TestAccountsSwitchRefusesBeforeItWritesWhenAProjectJobIsRunning is issue
// #3686's first half, and the reason the command plans before it writes: a
// switch takes FROM out of every profiles.<profile>.claude_accounts that names
// it, so the shared job of that profile's (from) triple stops being declared —
// and when the converge then meets a job it cannot retire, the config is left
// half-switched, which is what the issue records (nine keys written, a refusal
// on two protected jobs, and the block put back by hand).
//
// The job here is the one thing that stops a retirement: an execution that has
// not finished, which deleting the job would cancel. The refusal has to name
// the profile the switch moves, and the file has to be byte-identical
// afterwards — not "restored", never written at all.
//
// A cloud nobody can read is the same direction, and the second half of the
// case: a job of a profile this switch moves cannot be promised a retirement
// when its own installation cannot be listed.
func TestAccountsSwitchRefusesBeforeItWritesWhenAProjectJobIsRunning(t *testing.T) {
	r := newAddRigWith(t, switchRigLocal)
	job := gcp.ProjectJobName("standard", "github", "team-a")
	r.run.AddProjectJob(job, "standard", "github", "team-a", "2", "8Gi")
	r.run.SetJob(job, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProfile: "standard",
		"fugaro_provider": "github", "fugaro_account": "team-a",
	}, "image")
	r.run.Start(job)

	before := switchFile(t)
	out, errOut, err := execute(t, "accounts", "switch", "team-a", "team-b", "--yes")
	if err == nil {
		t.Fatalf("a switch of a profile whose job has a run on it was admitted:\n%s\n%s", out, errOut)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, want := range []string{job, "profile standard", "nothing was written", "fugaro accounts switch team-a team-b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if out != "" {
		t.Errorf("the refusal printed the plan before it refused:\n%s", out)
	}
	if after := switchFile(t); after != before {
		t.Errorf("the refusal wrote the file anyway, which is the half-switch the issue records:\n%s", after)
	}
}

// TestAccountsSwitchRefusesWhenTheInstallationsJobsCannotBeRead: the read that
// decides whether a switch may land is a fact of the installation, and a read
// that could not be made is not the same answer as "no job of that profile is
// running". A switch that took it for one would write the file and find out
// afterwards, which is the failure this half exists to close.
func TestAccountsSwitchRefusesWhenTheInstallationsJobsCannotBeRead(t *testing.T) {
	r := newAddRigWith(t, switchRigLocal)
	r.run.FailListWith = http.StatusForbidden

	before := switchFile(t)
	out, errOut, err := execute(t, "accounts", "switch", "team-a", "team-b", "--yes")
	if err == nil {
		t.Fatalf("a switch whose cloud could not be read was admitted:\n%s\n%s", out, errOut)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, want := range []string{"could not be read", "gcloud run jobs list", "fugaro accounts switch team-a team-b", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if out != "" {
		t.Errorf("the refusal printed the plan before it refused:\n%s", out)
	}
	if after := switchFile(t); after != before {
		t.Errorf("the refusal wrote the file anyway:\n%s", after)
	}
}

// TestAccountsSwitchAdmitsAJobItCanRetire: the other half of the rule — a job
// of the profile this switch moves, with nothing running on it, is a job the
// converge retires by itself, and the switch goes through. Without this case a
// refusal that fired on every live job would read as the feature.
//
// The converge has to retire it and not merely tolerate it: the switch's own
// run applies the installation root, and an instance the document no longer
// declares is a plan that deletes a job the module protects — the refusal that
// leaves the config half-written when it lands after the write. So the fixture
// is the installation the issue is about (the state still manages the instance
// of the triple the profile served, the cloud still runs its job) and the
// assertion is on the delete.
func TestAccountsSwitchAdmitsAJobItCanRetire(t *testing.T) {
	r := newAddRigWith(t, switchRigLocal)
	job := gcp.ProjectJobName("standard", "github", "team-a")
	r.run.AddProjectJob(job, "standard", "github", "team-a", "2", "8Gi")
	r.run.SetJob(job, projectJobStateLabels("standard", "github", "team-a"), "image")
	done := r.run.Start(job)
	r.run.SetState(done, backend.StateSucceeded)
	r.script["show -json@installation"] = map[string]any{"stdout": projectJobStateDoc(t,
		projectJobState{job, projectJobStateLabels("standard", "github", "team-a")})}
	r.save(t)

	out, errOut, err := execute(t, "accounts", "switch", "team-a", "team-b", "--yes")
	if err != nil {
		t.Fatalf("a switch of a quiet job was refused: %v\n%s\n%s", err, out, errOut)
	}
	if got := r.addLocal(t).Profiles["standard"].ClaudeAccounts; !slices.Equal(got, []string{"team-b"}) {
		t.Errorf("profiles.standard.claude_accounts = %v, want [team-b]", got)
	}
	if d := r.run.JobDeletes(); !slices.Contains(d, job) {
		t.Errorf("deleted jobs = %v; want %s: the switch's own converge retires the job of the account it moved off", d, job)
	}
}
