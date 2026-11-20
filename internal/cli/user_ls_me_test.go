package cli

// WH1 of docs/console-improvements: `fugaro user ls --me --json`, the caller's
// own role and the scope it is held at (D2907, docs/console-improvements/
// reconciliation.md §4; RS7 of docs/roles-scopes/implementation-plan.md records
// the amendment — a caller may read their own role).
//
// The read is the gates' own resolution and nothing else: the roster is the
// grants node accessJudge reads, the caller is the credential access.Caller
// names and never the local config's `user:` key (F21), and the role is the
// highest one a grant names the caller with — an implicit project admin reads
// admin at project scope (F17.3). A console that maps this document to a
// disabled control therefore cannot disagree with the CLI that would refuse the
// write, which is what TestUserLsMeMatchesTheGate holds to the gate itself.

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// accessOtherAddr is the second person every case seats on the roster: a
// project admin, so a read that answered from the strongest pair in the node,
// or from any pair but the caller's own, is caught rather than passing as the
// caller's answer.
const accessOtherAddr = "other@example.com"

// updateWhoamiGolden rewrites testdata/whoami.golden.json. It is a flag of its
// own: the package's other goldens own -update, -update-review, -update-retire,
// -update-cloudrun, -update-diagnose and -update-coordinator, and a second
// flag.Bool of the same name panics.
var updateWhoamiGolden = flag.Bool("update-whoami", false, "rewrite testdata/whoami.golden.json")

// userLsMeJSON runs the read and answers the document it printed.
func userLsMeJSON(t *testing.T) (string, whoamiDoc) {
	t.Helper()
	out, errOut, err := execute(t, "user", "ls", "--me", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, accessOut(out, errOut, err))
	}
	var doc whoamiDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the --json document does not decode: %v\n%s", err, out)
	}
	return out, doc
}

// userLsMeLine runs the read without --json, which is the one sentence a person
// reads at their own terminal. It is named apart from the sentence builder the
// command itself calls, which is the same package.
func userLsMeLine(t *testing.T) string {
	t.Helper()
	out, errOut, err := execute(t, "user", "ls", "--me")
	if err != nil {
		t.Fatalf("%v\n%s", err, accessOut(out, errOut, err))
	}
	return out
}

// TestUserLsMeShowsOnlyTheCaller: the answer is the caller's own role and the
// scope it is held at, read from the credential this command is using and never
// from another member's pair, a `user ls` roster or the config's `user:` key.
// Without --json it is one sentence; an agent's session reads it too, because it
// is the caller's own role and nothing else — the refusal D1590 makes is on the
// two writing subcommands.
func TestUserLsMeShowsOnlyTheCaller(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleOperator, "github/acme/*"),
		userStored(t, accessMember(t, accessOtherAddr), accesslib.RoleAdmin, "project"),
	)
	out, doc := userLsMeJSON(t)
	if doc.Version != whoamiDocVersion {
		t.Errorf("the document is version %d, and the shape it publishes is %d", doc.Version, whoamiDocVersion)
	}
	if doc.Role != "operator" || doc.Scope != "repos" || !slices.Equal(doc.Repos, []string{"github/acme/*"}) {
		t.Errorf("--me reads %q at %q %v, want operator at repos [github/acme/*]", doc.Role, doc.Scope, doc.Repos)
	}
	// The other member's admin at project scope is not the caller's answer, and
	// no field of the document names anybody: the document is about the caller.
	for _, unwanted := range []string{accessOtherAddr, "admin", accessAdminAddr} {
		if strings.Contains(out, unwanted) {
			t.Errorf("--me prints %q, and the answer is the caller's own role with no member named in it:\n%s", unwanted, out)
		}
	}
	// The config's own `user:` key is a value a person edits (F21); this read is
	// about the credential, so the fixture's config names somebody else and the
	// answer is still the caller's pair.
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if lc.User == accessAdminAddr {
		t.Fatalf("the fixture's config names %q, so the case proves nothing about the credential", lc.User)
	}

	line := userLsMeLine(t)
	if strings.Count(strings.TrimSuffix(line, "\n"), "\n") != 0 {
		t.Errorf("--me without --json is one sentence, and it printed %d lines:\n%s", strings.Count(line, "\n"), line)
	}
	for _, want := range []string{accessMember(t, accessAdminAddr), "operator", "github/acme/*"} {
		if !strings.Contains(line, want) {
			t.Errorf("the sentence does not name %q:\n%s", want, line)
		}
	}

	t.Setenv("CLAUDECODE", "1")
	if _, agent := userLsMeJSON(t); agent.Role != doc.Role || agent.Scope != doc.Scope {
		t.Errorf("an agent's session reads %q at %q, want the same answer (%q at %q): --me is the caller's own role, which D1590 does not refuse", agent.Role, agent.Scope, doc.Role, doc.Scope)
	}
}

// TestUserLsMeMatchesTheGate: the document and the gate answer from one roster
// through one resolver, so a console that disables a control on it cannot
// disable one the CLI would allow, nor leave enabled one the CLI would refuse.
//
// The gate is the real one: `image add` is a project-level write, so
// accessGateProject judges it (admin at project scope), and every case asserts
// the command's own verdict beside the document's. A case whose grant holds no
// project-level thing — an all-repos operator, a repository admin — is the
// honest half of the projection: the document names the role and the scope it is
// held at, the gate refuses the project-level write, and the CLI is still the
// one that refuses it server-side.
func TestUserLsMeMatchesTheGate(t *testing.T) {
	cases := []struct {
		name                string
		role                accesslib.Role
		scope               string
		implicit            bool
		wantRole, wantScope string
	}{
		{name: "a project admin", role: accesslib.RoleAdmin, scope: "project", wantRole: "admin", wantScope: "project"},
		{name: "a project operator", role: accesslib.RoleOperator, scope: "project", wantRole: "operator", wantScope: "project"},
		{name: "a project viewer", role: accesslib.RoleViewer, scope: "project", wantRole: "viewer", wantScope: "project"},
		{name: "an all-repos admin", role: accesslib.RoleAdmin, scope: "all-repos", wantRole: "admin", wantScope: "all"},
		{name: "a repository admin", role: accesslib.RoleAdmin, scope: accessRepoCanon, wantRole: "admin", wantScope: "repos"},
		{name: "an implicit budget admin", implicit: true, wantRole: "admin", wantScope: "project"},
		{name: "nobody", wantRole: "none", wantScope: "none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := accessFixture(t, true)
			if c.implicit {
				f.appendConfig(t, "terraform:\n  budget_admins: [\"user:"+accessAdminAddr+"\"]\n")
			}
			grants := []accesslib.Grant{
				userStored(t, accessMember(t, accessOtherAddr), accesslib.RoleAdmin, "project"),
			}
			if c.scope != "" {
				grants = append(grants, userStored(t, accessMember(t, accessAdminAddr), c.role, c.scope))
			}
			accessAs(t, f.db, accessAdminAddr, grants...)

			_, doc := userLsMeJSON(t)
			if doc.Role != c.wantRole || doc.Scope != c.wantScope {
				t.Fatalf("--me reads %q at %q %v, want %q at %q", doc.Role, doc.Scope, doc.Repos, c.wantRole, c.wantScope)
			}
			// The gate's own verdict on the same roster: the project-level write
			// accessGateProject judges, with the role it needs.
			_, errOut, err := execute(t, "image", "add", "extra", "--tags", "redis",
				"--inline", accessDockerfile(t), "--description", "a redis image", "--no-apply")
			allowed := err == nil
			if want := doc.Role == "admin" && doc.Scope == "project"; allowed != want {
				t.Errorf("the gate %s the project-level write while --me reads %q at %q: %s",
					verdict(allowed), doc.Role, doc.Scope, accessOut("", errOut, err))
			}
			if !allowed {
				accessRefused(t, err, "admin at project scope")
			}
		})
	}

	// A caller named by two grants of the same role is told the wider scope: the
	// one that decides more of their commands, and the answer that does not
	// depend on the order the node happens to hold the pairs in. Each case writes
	// the narrower grant first, so a read that answered with the first match — or
	// with the last — would be caught rather than passing as the resolver's.
	t.Run("one role held at two scopes", func(t *testing.T) {
		for _, c := range []struct{ name, narrow, wide, want string }{
			{name: "all-repos and a repository list", narrow: accessRepoCanon, wide: "all-repos", want: "all"},
			{name: "project and all-repos", narrow: "all-repos", wide: "project", want: "project"},
		} {
			t.Run(c.name, func(t *testing.T) {
				f := accessFixture(t, true)
				accessAs(t, f.db, accessAdminAddr,
					userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, c.narrow),
					userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleAdmin, c.wide))
				_, doc := userLsMeJSON(t)
				if doc.Role != "admin" || doc.Scope != c.want {
					t.Errorf("--me reads %q at %q, want admin at %q: the same role at two scopes is the wider one", doc.Role, doc.Scope, c.want)
				}
			})
		}
	})

	// And the credential: a caller nobody can be named from is refused by both
	// reads, with the gate's own sentence. A --me that guessed the caller from
	// the config — or from the strongest pair in the node — would answer here.
	t.Run("an unreadable credential", func(t *testing.T) {
		f := accessFixture(t, true)
		f.db.Set(accesslib.PathNode, accessNodeDoc(projectGrant(accessMember(t, accessOtherAddr), accesslib.RoleAdmin)))
		settingsAccessCallerFails(t)
		out, errOut, err := execute(t, "user", "ls", "--me", "--json")
		accessRefused(t, err, "could not read who that credential belongs to")
		if strings.Contains(out, "admin") {
			t.Errorf("--me answered with a role before it read the credential:\n%s", accessOut(out, errOut, err))
		}
		_, _, gateErr := execute(t, "image", "add", "extra", "--tags", "redis",
			"--inline", accessDockerfile(t), "--description", "a redis image", "--no-apply")
		accessRefused(t, gateErr, "could not read who that credential belongs to")
	})

	// An installation with no roster to read answers no role at all rather than
	// a guess: the gate makes no judgement there either — accessJudge answers
	// nil and the commands behave as they did before the roles existed — so a
	// console handed a role would disable controls the CLI still allows. It is
	// the same refusal `user ls --repo` prints for the same state, and the two
	// states are an installation with no backend at all and one whose grants
	// node has never been written.
	for _, c := range []struct {
		name    string
		fixture func(*testing.T)
		wants   []string
	}{
		{
			name:    "an installation with no budget backend",
			fixture: func(t *testing.T) { newBudgetFixture(t, "budget: { mode: observe, per_run_usd: 5 }\n") },
			wants:   []string{"no Firebase budget backend", "fugaro update"},
		},
		{
			name:    "an installation whose node has never been written",
			fixture: func(t *testing.T) { newBudgetFixture(t, "") },
			wants:   []string{"has none", "fugaro update", "F21"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.fixture(t)
			userPairAs(t, accessMember(t, accessAdminAddr))
			out, errOut, err := execute(t, "user", "ls", "--me", "--json")
			accessRefused(t, err, append(c.wants, "your own role")...)
			if strings.Contains(out, "{") {
				t.Errorf("--me printed a document for an installation with no roster:\n%s", accessOut(out, errOut, err))
			}
			// The roster read is unchanged: it still answers, and still says
			// which roles the installation has when the node is not one of them.
			if _, _, err := execute(t, "user", "ls", "--json"); err != nil {
				t.Errorf("the roster refused after --me did: %v", err)
			}
		})
	}
}

// verdict is a test sentence's word for a gate's answer.
func verdict(allowed bool) string {
	if allowed {
		return "allowed"
	}
	return "refused"
}

// TestUserLsRosterUnchanged: --me added a read beside the roster, and the roster
// is what it was — the same document, the same keys, and no call to the
// credential: `user ls` lists what the node holds whoever is asking, and a
// caller nobody can be named from still reads it (the F21 half that makes the
// node, and never one machine's own lists, the roster).
func TestUserLsRosterUnchanged(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessOtherAddr), accesslib.RoleAdmin, "project"))
	settingsAccessCallerFails(t)

	out, errOut, err := execute(t, "user", "ls", "--json")
	if err != nil {
		t.Fatalf("the roster read the credential: %v\n%s", err, accessOut(out, errOut, err))
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &keys); err != nil {
		t.Fatalf("the roster document does not decode: %v\n%s", err, out)
	}
	want := []string{"admins", "effective", "gcp_project", "grants", "legacy", "project", "run_operators", "version"}
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the roster document carries %v, want %v: WH1 adds no member to it", got, want)
	}
	if !strings.Contains(out, accessOtherAddr) {
		t.Errorf("the roster does not list the node's grant:\n%s", out)
	}
	human, errOut, err := execute(t, "user", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, accessOut(human, errOut, err))
	}
	if !strings.Contains(human, accessOtherAddr) || !strings.Contains(human, "grants of project") {
		t.Errorf("the roster listing is not the grants of the node:\n%s", human)
	}
}

// TestWhoamiSchema validates what the CLI writes against
// schemas/whoami.schema.json: the three words of each field are the contract a
// console reads them by, and a role the console does not know — a word no
// version of this document carries — is refused here rather than read as a role.
func TestWhoamiSchema(t *testing.T) {
	sch := whoamiSchema(t)
	for _, c := range []struct {
		name  string
		role  accesslib.Role
		scope string
		repos []string
	}{
		{name: "a repository operator", role: accesslib.RoleOperator, scope: "github/acme/*", repos: []string{"github/acme/*"}},
		{name: "a project admin", role: accesslib.RoleAdmin, scope: "project"},
		{name: "an all-repos viewer", role: accesslib.RoleViewer, scope: "all-repos"},
		{name: "nobody"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := accessFixture(t, false)
			grants := []accesslib.Grant{
				userStored(t, accessMember(t, accessOtherAddr), accesslib.RoleAdmin, "project"),
			}
			if c.scope != "" {
				grants = append(grants, userStored(t, accessMember(t, accessAdminAddr), c.role, c.scope))
			}
			accessAs(t, f.db, accessAdminAddr, grants...)
			out, doc := userLsMeJSON(t)
			if err := sch.Validate(jsonInstance(t, out)); err != nil {
				t.Errorf("the schema refuses the CLI's own document: %v\n%s", err, out)
			}
			// repos is the scope's own patterns and nothing else: a list of
			// repositories this machine knows would be a claim the grant does
			// not make (F27).
			if want := append([]string{}, c.repos...); !slices.Equal(doc.Repos, want) {
				t.Errorf("--me reads repos %v, want %v", doc.Repos, want)
			}
		})
	}
	for _, bad := range []struct{ name, doc string }{
		{"a role no version of this document carries", `{"version":1,"role":"superuser","scope":"project","repos":[]}`},
		{"a missing repos member", `{"version":1,"role":"admin","scope":"project"}`},
		{"a scope outside the three", `{"version":1,"role":"admin","scope":"all-repos","repos":[]}`},
		{"a version this shape is not", `{"version":2,"role":"admin","scope":"project","repos":[]}`},
	} {
		if err := sch.Validate(jsonInstance(t, bad.doc)); err == nil {
			t.Errorf("the schema accepts %s: %s", bad.name, bad.doc)
		}
	}
}

// TestWhoamiGolden holds the document the CLI writes to a golden recorded from
// the CLI's own output, which is the copy the console's own fixture is compared
// with (I-OPS of docs/console-improvements/implementation-plan.md): the shape
// the console decodes is this file, byte for byte.
func TestWhoamiGolden(t *testing.T) {
	f := accessFixture(t, false)
	accessAs(t, f.db, accessAdminAddr,
		userStored(t, accessMember(t, accessAdminAddr), accesslib.RoleOperator, "github/acme/*"),
		userStored(t, accessMember(t, accessOtherAddr), accesslib.RoleAdmin, "project"),
	)
	out, _ := userLsMeJSON(t)

	path := filepath.Join("testdata", "whoami.golden.json")
	if *updateWhoamiGolden {
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("no golden file: run go test ./internal/cli -run TestWhoamiGolden -update-whoami")
		}
		t.Fatal(err)
	}
	if string(want) != out {
		t.Fatalf("the --me document changed (go test -update-whoami to accept):\n%s\nwant:\n%s", out, want)
	}
}

// whoamiSchema compiles schemas/whoami.schema.json, the way the schema
// package's own tests do: under its own $id, so a $ref inside it resolves.
func whoamiSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "whoami.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/whoami.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

// jsonInstance is one document as the validator's value model.
func jsonInstance(t *testing.T, doc string) any {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return inst
}
