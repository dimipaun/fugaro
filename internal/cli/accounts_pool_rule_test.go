package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/routing"
)

// TestAccountsAddMakesAPoolOfOne is the pair the new members are for: a
// subscription token is put in a pool from the start rather than left a lone
// credential a launcher's designation has nothing to name (D3176, R26). The
// pool is the one --pool names and the member is the credential entry this same
// run declares, so the file that is left names the token once and walks it in
// one pool.
//
// The clauses that bound the write are the reasons the flag is not a default:
// a pool holds a subscription token, so a key credential is refused rather than
// pooled; the pool joins one the config already defines with its own members
// and their order kept; and the name is refused where a credential of the same
// config holds it, because a craft's access names a credential or a pool and
// does not say which (config.ValidatePools).
func TestAccountsAddMakesAPoolOfOne(t *testing.T) {
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

// TestAccountsAddPoolRefusesWhatAPoolMayNotHold: the two states the write may
// not leave behind, each refused before anything is written — a pool of a
// credential whose value is a provider key, and a pool whose name a credential
// of the same config holds.
func TestAccountsAddPoolRefusesWhatAPoolMayNotHold(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"a key credential", []string{"team-a", "--repos", "acme/app", "--type", "anthropic", "--pool", "paid"}, "a pool walks subscription tokens"},
		{"a name a credential holds", []string{"team-a", "--repos", "acme/app", "--type", "claude-oauth", "--pool", "team-a"}, "a pool and a credential may not share one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsAddFixture(t, accountsAddLocal)
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

	out, _, err := execute(t, "accounts", "pool", "ls", "--json")
	if err != nil {
		t.Fatalf("accounts pool ls --json: %v", err)
	}
	// The bytes are held to schemas/accounts-pool-ls.schema.json as well as to
	// C2's Go: the schema is what a consumer reads the document by, and a member
	// the command writes that it does not know is a contract the two sides do
	// not share.
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
