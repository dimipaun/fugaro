package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/routing"
)

// TestAccountsAddPoolHoldsTheAccountAlone: --pool names the pool the token is
// written into, and a name this config defines no pool of is declared with the
// credential entry this same run declares as its one member, so the file that
// is left names the token once and walks it in one pool.
//
// The clauses that bound the write: a pool holds a subscription token, so a key
// credential is refused rather than pooled; the pool joins one the config
// already defines with its own members and their order kept; and the name is
// refused where a credential of the same config holds it, unless the pool would
// walk that credential alone (config.ValidatePools).
func TestAccountsAddPoolHoldsTheAccountAlone(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal)
	ownerAtTheTerminal(t)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a",
		"--repos", "acme/app", "--type", "claude-oauth", "--pool", "paid")
	if err != nil {
		t.Fatalf("accounts add --pool: %v\n%s", err, out)
	}
	if !strings.Contains(out, "declare pools.paid.members = [team-a]") {
		t.Errorf("the sentence does not name the pool of one it wrote:\n%s", out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the file this command wrote does not read back: %v", err)
	}
	pool, ok := accountsPoolOf(lc, "paid")
	if !ok {
		t.Fatalf("pools.paid is not in the config this command wrote: %+v", lc.Pools)
	}
	if got := strings.Join(pool.Members, ", "); got != "team-a" {
		t.Errorf("pools.paid.members = %q, want the account alone", got)
	}
	if c, ok := config.CredentialNamed(lc.Credentials, "team-a"); !ok || c.Type != config.CredentialTypeClaudeOAuth {
		t.Errorf("the credential the pool walks was not declared as a subscription token: %+v", lc.Credentials)
	}
}

// TestAccountsAddDefaultsToAPoolOfOneNamedAfterTheAccount is the default this
// command ships today and D3200 retires (the owner's ruling of 2026-11-26:
// `accounts add` writes a lone account, and a pool is made with `fugaro
// accounts pool add`): with no --pool, a subscription token is put in a pool of
// one named after the account, and a key credential is put in none — no pool
// walks a provider key.
//
// The name the pool takes is the credential's own, which is the one collision
// the loader admits between the two blocks (config.ValidatePools): the pool's
// only member is the credential the name holds. The run that spells the name
// out is the same write, which is what keeps the command a person re-runs equal
// to the default.
func TestAccountsAddDefaultsToAPoolOfOneNamedAfterTheAccount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		account  string
		typ      string
		wantPool string
	}{
		{"a subscription token", "team-a", "claude-oauth", "team-a"},
		{"a key credential", "key-one", "anthropic", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsAddFixture(t, accountsAddLocal)
			ownerAtTheTerminal(t)

			out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", tc.account,
				"--repos", "acme/app", "--type", tc.typ)
			if err != nil {
				t.Fatalf("accounts add --type %s: %v\n%s", tc.typ, err, out)
			}
			lc, err := localcfg.Parse([]byte(readFileString(t, path)))
			if err != nil {
				t.Fatalf("the file this command wrote does not read back: %v", err)
			}
			pool, ok := accountsPoolOf(lc, tc.wantPool)
			if tc.wantPool == "" {
				if ok {
					t.Fatalf("a key credential was put in pools.%s: %+v", tc.wantPool, lc.Pools)
				}
				if strings.Contains(out, "declare pools.") {
					t.Errorf("the sentence names a pool no key credential has:\n%s", out)
				}
				return
			}
			if !ok {
				t.Fatalf("a subscription token was left out of pools.%s: %+v", tc.wantPool, lc.Pools)
			}
			if got := strings.Join(pool.Members, ", "); got != tc.account {
				t.Errorf("pools.%s.members = %q, want the account alone", tc.wantPool, got)
			}
			if !strings.Contains(out, "declare pools."+tc.wantPool+".members = ["+tc.account+"]") {
				t.Errorf("the sentence does not name the pool of one it wrote:\n%s", out)
			}
			// The name is the credential's own, so the run that spells it out
			// makes the same edit.
			explicit := accountsAddFixture(t, accountsAddLocal)
			if _, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", tc.account,
				"--repos", "acme/app", "--type", tc.typ, "--pool", tc.account); err != nil {
				t.Fatalf("accounts add --pool %s: %v", tc.account, err)
			}
			other, err := localcfg.Parse([]byte(readFileString(t, explicit)))
			if err != nil {
				t.Fatalf("the file --pool wrote does not read back: %v", err)
			}
			explicitPool, ok := accountsPoolOf(other, tc.account)
			if len(other.Pools) != len(lc.Pools) || !ok || !slices.Equal(explicitPool.Members, pool.Members) {
				t.Errorf("--pool %s wrote other pools than the default: %+v, want %+v", tc.account, other.Pools, lc.Pools)
			}
		})
	}
}

// TestAccountsAddPoolJoinsOneThatExists: the pool is a set, so a second token
// added to one is appended to the members the owner wrote, in their order, and
// the pool's own fields are left alone.
func TestAccountsAddPoolJoinsOneThatExists(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal+accountsPoolCredentials+accountsPoolPools)
	ownerAtTheTerminal(t)

	if _, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "oauth-two",
		"--repos", "acme/app", "--type", "claude-oauth", "--pool", "claude"); err != nil {
		t.Fatalf("accounts add --pool on an existing pool: %v", err)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the file this command wrote does not read back: %v", err)
	}
	pool, ok := accountsPoolOf(lc, "claude")
	if !ok {
		t.Fatalf("pools.claude is not in the config this command wrote: %+v", lc.Pools)
	}
	if got := strings.Join(pool.Members, ", "); got != "oauth-one, key-one, oauth-two" {
		t.Errorf("pools.claude.members = %q, want the members the owner wrote and the new one last", got)
	}
	if pool.StrategyOrDefault() != "failover" {
		t.Errorf("pools.claude.strategy = %q, want the strategy the pool already had", pool.StrategyOrDefault())
	}
}

// TestAccountsAddPoolRefusesWhatAPoolMayNotHold: the states the write may not
// leave behind, each refused before anything is written — a pool of a credential
// whose value is a provider key, a pool whose name another credential of the
// same config holds (the shared name is admitted only for the credential's own
// pool of one, config.ValidatePools), and an account that declares no credential
// at all for the pool to walk.
func TestAccountsAddPoolRefusesWhatAPoolMayNotHold(t *testing.T) {
	for _, tc := range []struct {
		name  string
		local string
		args  []string
		want  string
	}{
		{"a key credential", accountsAddLocal, []string{"team-a", "--repos", "acme/app", "--type", "anthropic", "--pool", "paid"}, "a pool walks subscription tokens"},
		// The name is another account's credential, so a pool of it would walk
		// that credential and this one: the shared name means two things and
		// the loader refuses it (config.ValidatePools).
		{"a name another credential holds", accountsPoolTypedLocalSameType, []string{"team-a", "--repos", "owner/repo-a", "--type", "claude-oauth", "--pool", "key-one"}, "would walk more than that credential alone"},
		// No type at all is a third state, and the refusal may not read as one
		// with a blank where the type goes: nothing is declared for the pool to
		// walk, and the sentence names the flag that leaves it that way.
		{"no type at all", accountsAddLocal, []string{"team-a", "--repos", "acme/app", "--pool", "paid"}, "team-a declares none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsAddFixture(t, tc.local)
			ownerAtTheTerminal(t)
			before := readFileString(t, path)

			out, _, err := executeStdin(t, addToken()+"\n", append([]string{"accounts", "add"}, tc.args...)...)
			if err == nil {
				t.Fatalf("the write was not refused:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), "nothing was written") {
				t.Errorf("the refusal does not say nothing was written: %v", err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestAccountsAddPoolClearsTheRemovalRecord: a pool declared here is not a
// removed one (F22). The record an earlier removal left has the last word over
// the pools block when the node is published, so a name left in it would take
// the pool this run declares off the node; `accounts pool add` clears it on the
// bytes it writes, and this write declares a pool by the same right.
func TestAccountsAddPoolClearsTheRemovalRecord(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal+accountsAddPoolRemovalRecord)
	ownerAtTheTerminal(t)

	if _, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "team-a",
		"--repos", "acme/app", "--type", "claude-oauth", "--pool", "paid"); err != nil {
		t.Fatalf("accounts add --pool: %v", err)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the file this command wrote does not read back: %v", err)
	}
	if lc.NodeRemovals != nil && slices.Contains(lc.NodeRemovals.Pools, "paid") {
		t.Errorf("node_removals.pools still names the pool this run declared: %+v", lc.NodeRemovals.Pools)
	}
	if _, ok := accountsPoolOf(lc, "paid"); !ok {
		t.Errorf("pools.paid is not in the config this command wrote: %+v", lc.Pools)
	}
}

// accountsAddPoolRemovalRecord is the state the clearing above is about: the
// record of a pool an earlier `accounts rm` took away, naming the one this
// fixture declares again.
const accountsAddPoolRemovalRecord = `
node_removals:
  pools:
    - paid
`

// TestPoolAddTakesARule: --rule is the member R5 adds, written on the pool's
// own line beside the strategy it was written with — an absent rule resolves to
// the strategy (R21), so a pool that names both is a pool an earlier release
// kept its meaning on. A value outside the vocabulary is refused by the block's
// own sentence, and --rule beside --strategy — two spellings of one decision —
// is refused before either is written.
func TestPoolAddTakesARule(t *testing.T) {
	path := accountsPoolHome(t, "")

	out, _, err := execute(t, "accounts", "pool", "add", "claude", "--rule", "ordered")
	if err != nil {
		t.Fatalf("accounts pool add --rule: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rule ordered") {
		t.Errorf("the sentence does not name the rule it wrote:\n%s", out)
	}
	block := accountsPoolBlock(t, path)
	if !strings.Contains(block, "  - name: claude\n    strategy: failover\n") || !strings.Contains(block, "    rule: ordered\n") {
		t.Errorf("the rule is not written beside the strategy the pool already had:\n%s", block)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the written config does not read back: %v", err)
	}
	if pool, ok := accountsPoolOf(lc, "claude"); !ok || pool.Rule != config.PoolRuleOrdered {
		t.Errorf("pools.claude.rule did not read back as ordered: %+v", lc.Pools)
	}
}

// TestPoolAddTakesAServiceAccountPrincipal: --service-account writes the pool's
// principal as the kind and the address, which is the enforcing half of who the
// pool's tokens are handed to (D3181) and a different member from --owner.
func TestPoolAddTakesAServiceAccountPrincipal(t *testing.T) {
	path := accountsPoolHome(t, "")

	out, _, err := execute(t, "accounts", "pool", "add", "claude", "--service-account", "runner@proj-1234.iam.gserviceaccount.com")
	if err != nil {
		t.Fatalf("accounts pool add --service-account: %v\n%s", err, out)
	}
	if !strings.Contains(out, "principal service-account runner@proj-1234.iam.gserviceaccount.com") {
		t.Errorf("the sentence does not name the principal it wrote:\n%s", out)
	}
	block := accountsPoolBlock(t, path)
	if !strings.Contains(block, "    principal:\n      id: runner@proj-1234.iam.gserviceaccount.com\n      kind: service-account\n") {
		t.Errorf("the principal is not a mapping of kind and id under the pool:\n%s", block)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the written config does not read back: %v", err)
	}
	pool, ok := accountsPoolOf(lc, "claude")
	if !ok || pool.Principal == nil || pool.Principal.Kind != config.PrincipalServiceAccount ||
		pool.Principal.ID != "runner@proj-1234.iam.gserviceaccount.com" {
		t.Errorf("pools.claude.principal did not read back as the service account: %+v", pool.Principal)
	}
}

// TestPoolAddRefusesARuleTheBlockRefuses: each refusal is the block's own
// sentence, and the file is left byte-identical.
func TestPoolAddRefusesARuleTheBlockRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"a rule outside the vocabulary", []string{"claude", "--rule", "spread"}, "must be one of ordered, round-robin, failover"},
		{"a rule beside a strategy", []string{"claude", "--rule", "ordered", "--strategy", "round-robin"}, "--rule and --strategy decide the same thing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsPoolHome(t, "")
			before := readFileString(t, path)

			args := append([]string{"accounts", "pool", "add"}, tc.args...)
			out, _, err := execute(t, args...)
			if err == nil {
				t.Fatalf("the write was not refused:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is not %q: %v", tc.want, err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestPoolLsJSONHasNoValue is F19's half of the contract: the document carries
// names and figures and never a value, for anyone. What that means concretely
// is that every member is a name, a state and the three figures PL10 fills —
// no member object has a key of its own to carry a secret under, and the
// derived secret names this same config's credentials are stored under appear
// nowhere in the bytes.
//
// The shape is C2's (routing.PoolListDoc), decoded here by the package that
// declares it, so a member renamed on either side fails rather than being read
// as an absence. The figures are absent, not invented: a member nothing holds
// is "available" and its reset, usage and headroom are null until PL10 counts
// them.
func TestPoolLsJSONHasNoValue(t *testing.T) {
	path := accountsPoolHomeWith(t, accountsPoolCredentials+accountsPoolLsPools)
	// The rule the document carries is the one the write put there, read back
	// through the same command: `accounts pool add --rule` is its writer.
	if _, _, err := execute(t, "accounts", "pool", "add", "subs", "--rule", "ordered"); err != nil {
		t.Fatalf("accounts pool add --rule: %v", err)
	}

	out, errOut, err := execute(t, "accounts", "pool", "ls", "--json")
	if err != nil {
		t.Fatalf("accounts pool ls --json: %v", err)
	}
	// The fixture has no budget backend, so no figure could be read, and the
	// command says so rather than printing every member as one the service
	// counts nothing for — which would be a claim about figures it did not read.
	if !strings.Contains(errOut, "no Firebase budget backend") {
		t.Errorf("a project with no database printed the figures as absent without saying why: %q", errOut)
	}
	poolLsMatchesTheSchema(t, out)

	var doc routing.PoolListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not C2's shape: %v\n%s", err, out)
	}
	if doc.Project != "aurora" || len(doc.Pools) != 2 {
		t.Fatalf("the document is %q with %d pools, want aurora with the two the config defines:\n%s", doc.Project, len(doc.Pools), out)
	}
	// The rule is the pool's OWN, as its owner wrote it, and the listing
	// resolves nothing: the pool that names none carries an empty rule, and a
	// reader that needs the resolved one reads the config, where the strategy is
	// (contract-pools.md, R21).
	subs, routes := doc.Pools[0], doc.Pools[1]
	if subs.Name != "subs" || subs.Rule != routing.RuleOrdered {
		t.Errorf("the pool's own rule was not printed as its owner wrote it: %+v", subs)
	}
	if routes.Name != "routes" || routes.Rule != "" {
		t.Errorf("a pool that names no rule did not print an empty one: %+v", routes)
	}
	if len(subs.Members) != 2 || subs.Members[0].Name != "oauth-one" || subs.Members[1].Name != "oauth-two" {
		t.Errorf("the members are not the pool's own, in order: %+v", subs.Members)
	}
	for _, m := range subs.Members {
		if m.State != string(creds.StateAvailable) {
			t.Errorf("member %s state = %q, want the word a member nothing holds has", m.Name, m.State)
		}
		if m.NextReset != nil || m.Usage != nil || m.Headroom != nil {
			t.Errorf("member %s carries a figure nothing counted: reset %v usage %v headroom %v", m.Name, m.NextReset, m.Usage, m.Headroom)
		}
	}
	// Every key the document may carry, from C2's own table: a value would have
	// to arrive under a name outside it.
	keys := map[string][]string{
		"":       {"project", "pools"},
		"pool":   {"name", "rule", "principal", "threshold", "members"},
		"member": {"name", "state", "next_reset", "usage", "headroom"},
	}
	var fields struct {
		Pools []map[string]json.RawMessage `json:"pools"`
	}
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		t.Fatal(err)
	}
	for _, p := range fields.Pools {
		if !onlyFields(p, keys["pool"]) {
			t.Errorf("a pool carries fields outside the contract: %v", fieldNames(p))
		}
		var members []map[string]json.RawMessage
		b, err := json.Marshal(p["members"])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &members); err != nil {
			t.Fatal(err)
		}
		for _, m := range members {
			if !onlyFields(m, keys["member"]) {
				t.Errorf("a member carries fields outside the contract, which is where a value would hide: %v", fieldNames(m))
			}
		}
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatal(err)
	}
	if !onlyFields(top, keys[""]) {
		t.Errorf("the document carries fields outside the contract: %v", fieldNames(top))
	}
	// The config's own credentials here are stored under derived secret names
	// (config.CredentialValueSecret): neither those names nor the account's own
	// token container may appear, because the document carries names of members
	// and never the containers their values live in (F19).
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range lc.Credentials {
		for _, secret := range []string{config.CredentialValueSecret(lc.Credentials, c.Name), config.CredentialSecret(c.Name)} {
			if secret != "" && strings.Contains(out, secret) {
				t.Errorf("the document names the container %s a value lives under:\n%s", secret, out)
			}
		}
	}
}

// TestPoolLsPrintsUsageAndNoValue is PL10's read: the document carries what the
// service counted — the usage of the window, the instant a mark frees a member,
// and what a provider's own figure leaves of its limit — and it still carries no
// value (F19). Headroom is there only where a figure was reported and is never
// guessed, a member nothing was counted for keeps the absent figures the other
// test pins, and the read WRITES nothing even though it reads the leases: the
// reset of a member no mark holds is the time its leases lapse, and it is read
// through creds.RTDBStore.LeasesUnpruned — the sweeping read is the service's,
// and the lapsed nodes seeded below are what tells the two apart.
func TestPoolLsPrintsUsageAndNoValue(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.appendConfig(t, accountsPoolCredentials+accountsPoolLsPools)

	now := time.Now().UTC()
	until := now.Add(90 * time.Minute).Truncate(time.Second)
	// oauth-one is exhausted until then, and its window holds three calls a
	// provider reported at 0.8 of its limit.
	f.db.Set(credsMarkPath("oauth-one"), map[string]any{
		"state": "exhausted", "until": until.Format(time.RFC3339Nano)})
	f.db.Set(credsUsagePath("oauth-one"), map[string]any{
		"calls": 3, "tokens": 1200, "window": now.Add(-time.Hour).Format(time.RFC3339Nano),
		"utilisation": 0.8})
	// oauth-two served one call, and no provider reported a figure for it.
	f.db.Set(credsUsagePath("oauth-two"), map[string]any{
		"calls": 1, "tokens": 40, "window": now.Add(-time.Minute).Format(time.RFC3339Nano)})
	// router-one is held by a run's own lease and by no mark, and nothing was
	// counted for it: the reset is the time that lease lapses, and the usage
	// stays absent.
	f.db.Set("creds/leases/gh-acme-app/r1", map[string]any{
		"credential": "router-one", "until": until.Format(time.RFC3339Nano)})
	lease := f.db.Value("creds/leases/gh-acme-app/r1")
	if lease == nil {
		t.Fatal("the lease node was not seeded: the assertions below would pass vacuously")
	}
	// More lapsed leases than creds.RTDBStore.Leases keeps as residue, all of
	// them older than the live hold above: the sweeping read would delete some of
	// these, so their survival is what tells LeasesUnpruned from Leases.
	dead := make([]string, 0, creds.DefaultLeaseResidue+1)
	for i := 0; i <= creds.DefaultLeaseResidue; i++ {
		path := fmt.Sprintf("creds/leases/gh-acme-app/dead-%03d", i)
		f.db.Set(path, map[string]any{
			"credential": "oauth-two", "until": now.Add(-time.Hour).Format(time.RFC3339Nano)})
		dead = append(dead, path)
	}

	out, errOut, err := execute(t, "accounts", "pool", "ls", "--json")
	if err != nil {
		t.Fatalf("accounts pool ls --json: %v", err)
	}
	if strings.Contains(errOut, "no usage could be read") || strings.Contains(errOut, "no Firebase budget backend") {
		t.Errorf("a read that answered said it could not read the figures: %q", errOut)
	}
	poolLsMatchesTheSchema(t, out)

	var doc routing.PoolListDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not C2's shape: %v\n%s", err, out)
	}
	if len(doc.Pools) != 2 {
		t.Fatalf("the document carries %d pools, want the two the config defines:\n%s", len(doc.Pools), out)
	}
	subs, routes := doc.Pools[0], doc.Pools[1]

	one := subs.Members[0]
	if one.State != string(creds.StateExhausted) {
		t.Errorf("a member held by its mark reads %q; want the state the mark is in", one.State)
	}
	if one.NextReset == nil || !one.NextReset.Equal(until) {
		t.Errorf("the exhausted member's reset reads %v; want the instant its mark names, %v", one.NextReset, until)
	}
	if one.Usage == nil || one.Usage.Calls != 3 || one.Usage.Tokens != 1200 {
		t.Errorf("the member's usage reads %+v; want the three calls and 1200 tokens the service counted", one.Usage)
	}
	if one.Headroom == nil {
		t.Error("a member a provider reported at 0.8 of its limit carries no headroom; want 0.2")
	} else if math.Abs(*one.Headroom-0.2) > 1e-9 {
		t.Errorf("a member reported at 0.8 of its limit reads %v of headroom; want 0.2", *one.Headroom)
	}

	two := subs.Members[1]
	if two.State != string(creds.StateAvailable) {
		t.Errorf("a member nothing holds reads %q; want the word that means nothing holds it", two.State)
	}
	if two.NextReset != nil {
		t.Errorf("a member nothing holds carries the reset %v; want none", two.NextReset)
	}
	if two.Usage == nil || two.Usage.Calls != 1 || two.Usage.Tokens != 40 {
		t.Errorf("the member's usage reads %+v; want its own one call and 40 tokens", two.Usage)
	}
	if two.Headroom != nil {
		t.Errorf("a member no provider reported a figure for carries %v of headroom; want none — it is never guessed", *two.Headroom)
	}

	held := routes.Members[0]
	if held.State != string(creds.StateAvailable) {
		t.Errorf("a member held by a run's lease reads %q; want the available state, which is what a member no mark holds has", held.State)
	}
	if held.NextReset == nil || !held.NextReset.Equal(until) {
		t.Errorf("a member held by a run's lease carries the reset %v; want the instant its lease lapses, %v", held.NextReset, until)
	}
	if held.Usage != nil || held.Headroom != nil {
		t.Errorf("a member nothing was counted for carries usage %+v and headroom %v; want both absent", held.Usage, held.Headroom)
	}
	if free := routes.Members[1]; free.NextReset != nil || free.Usage != nil || free.Headroom != nil {
		t.Errorf("a member nothing holds and nothing counted carries a figure: %+v", free)
	}
	// The listing wrote nothing: the live lease is where it was, and the lapsed
	// ones a sweeping read would have taken are all still there. The service
	// stays the only writer of the nodes this command reads.
	if after := f.db.Value("creds/leases/gh-acme-app/r1"); after == nil {
		t.Error("the listing deleted the live lease it read: a read that sweeps is a write, and the service is this database's only writer")
	} else if !reflect.DeepEqual(after, lease) {
		t.Errorf("the listing changed the lease node: %v became %v", lease, after)
	}
	for _, path := range dead {
		if f.db.Value(path) == nil {
			t.Fatalf("the listing swept the lapsed lease %s: it reads the leases through LeasesUnpruned, and the sweep is the service's", path)
		}
	}

	// No value, for anyone: a member is the five names of the contract and a
	// usage is the two counts, and a value would have to arrive under a name
	// outside them (F19).
	for _, p := range doc.Pools {
		for _, m := range p.Members {
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(b, &fields); err != nil {
				t.Fatal(err)
			}
			if !onlyFields(fields, []string{"name", "state", "next_reset", "usage", "headroom"}) {
				t.Errorf("member %s carries fields outside the contract, which is where a value would hide: %v", m.Name, fieldNames(fields))
			}
			if m.Usage == nil {
				continue
			}
			b, err = json.Marshal(m.Usage)
			if err != nil {
				t.Fatal(err)
			}
			var counts map[string]json.RawMessage
			if err := json.Unmarshal(b, &counts); err != nil {
				t.Fatal(err)
			}
			if !onlyFields(counts, []string{"calls", "tokens"}) {
				t.Errorf("member %s's usage carries fields outside the contract: %v", m.Name, fieldNames(counts))
			}
		}
	}
}

// credsUsagePath is where the service counts one member's usage
// (creds.PathUsage), escaped as the service escapes it: the same hand-seeded
// node the marks' own helper writes, for the same reason (credsMarkPath).
func credsUsagePath(name string) string { return "creds/usage/" + budget.Key(name) }

// TestPoolLsJSONAnEmptyProjectPrintsAnEmptyList: a project with no pool says so
// with an empty list and never a null, which is what makes a reader's "the
// project has no pool" a fact rather than a guess (routing.PoolListDoc).
func TestPoolLsJSONAnEmptyProjectPrintsAnEmptyList(t *testing.T) {
	accountsPoolHomeWith(t, accountsPoolCredentials)

	out, _, err := execute(t, "accounts", "pool", "ls", "--json")
	if err != nil {
		t.Fatalf("accounts pool ls --json: %v", err)
	}
	if !strings.Contains(out, `"pools": []`) {
		t.Errorf("a project with no pool did not print an empty list:\n%s", out)
	}
}

// onlyFields reports whether m carries exactly the named keys, and fieldNames
// spells the ones it does carry for the failure. A document with a key outside
// C2's table is the document F19 forbids: a value has to arrive under a name,
// and no name outside the table is one the contract admits.
func onlyFields(m map[string]json.RawMessage, want []string) bool {
	if len(m) != len(want) {
		return false
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func fieldNames(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// poolLsMatchesTheSchema holds the document's bytes to
// schemas/accounts-pool-ls.schema.json as well as to C2's Go: the schema is what
// a consumer reads the document by, and a member the command writes that it
// does not know is a contract the two sides do not share.
func poolLsMatchesTheSchema(t *testing.T, out string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "accounts-pool-ls.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := schema.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/accounts-pool-ls.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, schema); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/accounts-pool-ls.schema.json: %v\n%s", err, out)
	}
}

// TestAccountsAddReplacingATokenLeavesPoolsAlone: the default pool of one is
// for a NEW account (the owner's ruling of 2026-11-25). A run that only
// replaces the token of an account the config already has writes no pool, so
// the edit never reaches the clearing of a pending pool removal either (that
// record is consumed by the apply this rig runs, so the pool is what is read).
func TestAccountsAddReplacingATokenLeavesPoolsAlone(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal)
	ownerAtTheTerminal(t)

	out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "legacy",
		"--repos", "acme/app", "--type", "claude-oauth")
	if err != nil {
		t.Fatalf("accounts add (a replace): %v\n%s", err, out)
	}
	if strings.Contains(out, "declare pools.") {
		t.Errorf("a token replace names a pool it wrote:\n%s", out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the file this command wrote does not read back: %v", err)
	}
	if len(lc.Pools) != 0 {
		t.Errorf("a token replace wrote a pool: %+v", lc.Pools)
	}
}

// TestAccountsAddExplicitPoolStillWritesOnAReplace pins that --pool is not
// changed by the rule above: a person who names the pool gets it, replace or not.
func TestAccountsAddExplicitPoolStillWritesOnAReplace(t *testing.T) {
	path := accountsAddFixture(t, accountsAddLocal)
	ownerAtTheTerminal(t)

	if out, _, err := executeStdin(t, addToken()+"\n", "accounts", "add", "legacy",
		"--repos", "acme/app", "--type", "claude-oauth", "--pool", "paid"); err != nil {
		t.Fatalf("accounts add --pool (a replace): %v\n%s", err, out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the file this command wrote does not read back: %v", err)
	}
	if pool, ok := accountsPoolOf(lc, "paid"); !ok || strings.Join(pool.Members, ",") != "legacy" {
		t.Errorf("pools.paid was not written with the account: %+v", lc.Pools)
	}
}
