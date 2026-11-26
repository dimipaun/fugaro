package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
)

// routingMigrateRig is the fixture MG2's tests drive: this machine holds one
// checkout of the installation's repository — which is where the migration
// reads the repository's own file from — and a run of the update door, whose
// writer is the buffer the report lands in.
func routingMigrateRig(t *testing.T, repoYAML string) (*initEngine, string, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	isolateProjects(t, dir)
	writeProject(t, "aurora", "aurora-gcp")
	enrollRepo(t, "aurora", "acme/api")
	origin := "https://github.com/acme/api.git"
	co := checkoutsAt(t, filepath.Join(dir, "src", "api"), repoYAML, origin)
	checkoutsRemember(t, "aurora", co, origin)
	var out bytes.Buffer
	o := &initOptions{yes: true, cloud: cloudOptions{stderr: discard}}
	cmd := newUpdateCmdWith(o)
	cmd.SetOut(&out)
	path := filepath.Join(dir, "config.yaml")
	return &initEngine{r: newInitRun(cmd, o), lc: parseLocalConfig(t, routingMigrateLocalYAML), path: path}, path, &out
}

// convergeTail runs the two steps MG2 is written into a converge as
// (init.go's writeConfig, then init_stages.go's tail): the local config's own
// write, converted by the craft migration every write makes, and then the
// routing migration. The report is asked for first because the converge asks
// for it before its first stage plans — every line about the migration is above
// the writes, not between them.
func convergeTail(t *testing.T, e *initEngine, path string, original []byte) {
	t.Helper()
	e.r.routingMigrationFor(e.lc)
	if err := e.r.writeLocalConfig(e.lc, path, original, true); err != nil {
		t.Fatal(err)
	}
	e.migrateRouting(&initflow.Result{})
}

// routingMigrateRepoYAML is acme/api's own file as the release behind leaves
// it: an oauth repository whose stages run on the account ada, calling one
// Claude model.
const routingMigrateRepoYAML = "version: 1\nproject: aurora\ngit: { provider: github }\n" +
	"agent: { auth: oauth, oauth_token: ada, model: claude-haiku-4-5 }\n" +
	"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// routingMigrateModelsYAML is acme/api's own file as a repository that pins its
// models one by one rather than with `agent.model`: every key here is a lifted
// one (R21), so the config this file resolves to carries none of them.
const routingMigrateModelsYAML = "version: 1\nproject: aurora\ngit: { provider: github }\n" +
	"agent:\n  auth: oauth\n  oauth_token: ada\n  models: { coder: claude-sonnet-5, reviewer: claude-opus-5-5 }\n" +
	"model_grades: { strongest: { model: claude-opus-5-5 } }\n" +
	"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// routingMigrateLocalYAML is the installation's own config at the release
// behind: the account ada, and the repository enrolled.
const routingMigrateLocalYAML = `version: 1
name: aurora
gcp_project: aurora-gcp
region: us-east5
runs_bucket: fugaro-runs-aurora-gcp
claude_accounts:
  ada: { allow_data_to: [acme/api] }
repos:
  acme/api: {}
`

// TestUpdateMigratesToTheTable: `fugaro update` writes what MG1 answers into
// the installation's own config — the table for the model the repository
// calls, with the shared pool in its chain, and the account's credential that
// member resolves to. D3176's pool of one of that name is not written: a pool
// and a credential share one namespace, and the credential is that pool of one
// already. Before this release the key was an unknown field and the update
// wrote no table at all.
func TestUpdateMigratesToTheTable(t *testing.T) {
	// MG1 spells the shared pool's name itself because internal/routing imports
	// no package of this tree, and this is the one side that sees both: a config
	// whose pool the table names under another string would route nothing.
	if routing.SharedPool != config.PoolCascade {
		t.Fatalf("the migration names the shared pool %q and the config spells it %q", routing.SharedPool, config.PoolCascade)
	}
	e, path, _ := routingMigrateRig(t, routingMigrateRepoYAML)
	// The write every stage of the converge makes: it carries no table, because
	// the stages after it plan from this config and the table is what moves
	// their plan (ChainPlanFor). The migration's own write is the tail's.
	if err := e.r.writeLocalConfig(e.lc, path, nil, true); err != nil {
		t.Fatal(err)
	}
	if planned := parseLocalConfigBytes(t, readFile(t, path)); len(planned.ModelRouting) > 0 {
		t.Fatalf("the run's own write of the config carries the table, so the update plans from it: %+v", planned.ModelRouting)
	}
	e.migrateRouting(&initflow.Result{})
	written := parseLocalConfigBytes(t, readFile(t, path))

	if len(written.ModelRouting) == 0 {
		t.Fatalf("the update wrote no table into the installation's config: %+v", written.ModelRouting)
	}
	entry, ok := routingEntryFor(written.ModelRouting, "claude-haiku-4-5")
	if !ok {
		t.Fatalf("the table has no entry for claude-haiku-4-5: %+v", written.ModelRouting)
	}
	if want := (routing.Source{Kind: routing.KindPool, Pool: routing.SharedPool}); len(entry.Chain) != 1 || entry.Chain[0].Kind != want.Kind || entry.Chain[0].Pool != want.Pool {
		t.Errorf("the entry's chain is %+v, want the shared pool %s alone: an oauth repository's calls go out on its accounts, which the shared pool is (D3183)",
			entry.Chain, routing.SharedPool)
	}
	if len(written.Pools) != 1 || written.Pools[0].Name != routing.SharedPool {
		t.Fatalf("the update wrote the pools %+v, want the shared pool alone: a pool and a credential share one namespace, so the account's pool of one cannot stand beside the credential of its own name (D3176, D3183)", written.Pools)
	}
	// The account's credential is what the shared pool's member resolves to, and
	// what the craft migration derived for it: the migration keeps it, and the
	// file's own validation is the proof that the member resolves at all.
	var held bool
	for _, c := range written.Credentials {
		if c.Name == "ada" {
			held = c.Type == config.CredentialTypeClaudeOAuth
		}
	}
	if !held {
		t.Errorf("the account's claude-oauth credential was not written: %+v, and the shared pool's member names it", written.Credentials)
	}
}

// TestUpdateMigratesTheModelsAFileWrites: a repository whose file pins its
// models one by one — `agent.models` and a grade's model, both lifted keys — is
// migrated to a table carrying an entry for each of them, although the config
// this run resolves for it carries none: Resolve drops every lifted key (R21),
// and the migration is the one reader of the models that has to see the file as
// it is written, because it is the step that moves them.
//
// The red step is the drop. Read off the resolved config alone the migration
// answers the project layer's models — this fixture's project has none — so the
// update writes no table at all and every call of the repository routes nowhere.
func TestUpdateMigratesTheModelsAFileWrites(t *testing.T) {
	// The claim this test rests on: the file's own resolution carries none of
	// the models the file writes.
	file := []byte(routingMigrateModelsYAML)
	if cfg, _, ps := config.Resolve(file, nil); len(ps) > 0 || cfg.Agent.Models.Coder != "" || cfg.Agent.Models.Reviewer != "" {
		t.Fatalf("the fixture's resolved config carries the models the file writes, so this test proves nothing (problems=%v): %+v", ps, cfg.Agent.Models)
	}

	e, path, _ := routingMigrateRig(t, routingMigrateModelsYAML)
	convergeTail(t, e, path, nil)
	written := parseLocalConfigBytes(t, readFile(t, path))

	for _, model := range []string{"claude-sonnet-5", "claude-opus-5-5"} {
		if _, ok := routingEntryFor(written.ModelRouting, model); !ok {
			t.Errorf("the table has no entry for %s, the model the repository's file writes: %+v", model, written.ModelRouting)
		}
	}
}

// TestUpdateSaysWhatChangesFirst: everything the migration prints comes before
// the write of the config it describes — the one line per repository saying
// what its runs go out on, the line per pool that starts rotating now that the
// spread switch is gone (D3177), the line for a project whose fallback was
// auto (D3192), and one per entry left on the release-1 bridge (D3196).
func TestUpdateSaysWhatChangesFirst(t *testing.T) {
	// The coder's craft goes out on a credential of its own, which one chain
	// per model cannot keep: the call stays on the release-1 bridge (D3196).
	const repoYAML = "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"agent:\n  auth: oauth\n  oauth_token: ada\n  model: claude-haiku-4-5\n" +
		"  crafts:\n    coder: { access: vertex }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	e, path, out := routingMigrateRig(t, repoYAML)
	// A pool with two members, the spread switch off: it rotates among its
	// tokens once migrated (D3177). And a project whose fallback is auto: a run
	// pays on the key as soon as its subscriptions are exhausted (D3192).
	const local = routingMigrateLocalYAML + `pools:
  - { name: pair, members: [ada, grace], strategy: failover }
credentials:
  - { name: ada, type: claude-oauth }
  - { name: grace, type: claude-oauth }
credential_cascade: { fallback: auto }
`
	e.lc = parseLocalConfig(t, local)
	convergeTail(t, e, path, nil)
	report := out.String()
	for _, want := range []string{
		"routing: acme/api's runs go out on the account default, then the account ada, as they do today",
		"routing: the pool pair rotates among its tokens now that subscription_spread is gone",
		"routing: fallback is auto, so a run pays on the key as soon as its subscriptions are exhausted, not after the old threshold",
		"routing: acme/api's calls of claude-haiku-4-5 stay on the release-1 bridge (craft-access)",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not say %q:\n%s", want, report)
		}
	}
	if wrote := strings.Index(report, "wrote "+path); wrote < 0 || strings.LastIndex(report, "routing:") > wrote {
		t.Errorf("the report is not printed before the write it describes (wrote at %d):\n%s", wrote, report)
	}
}

// TestUpdateMigrationIsIdempotent: a second update changes nothing — the table
// in the file is the marker (PL3 reads the same one off the published node),
// and the file is written byte for byte as it stands.
func TestUpdateMigrationIsIdempotent(t *testing.T) {
	e, path, out := routingMigrateRig(t, routingMigrateRepoYAML)
	convergeTail(t, e, path, nil)
	once := readFile(t, path)
	out.Reset()

	// A second run, on the file the first one left: the marker is the file's
	// own, and a fresh run is what reads it rather than this run's guard.
	second, _, out2 := routingMigrateRig(t, routingMigrateRepoYAML)
	second.lc = parseLocalConfigBytes(t, once)
	second.path = path
	convergeTail(t, second, path, once)
	if now := readFile(t, path); !bytes.Equal(once, now) {
		t.Errorf("the second update changed the installation's config:\n--- first\n%s\n--- second\n%s", once, now)
	}
	if s := out2.String(); strings.Contains(s, "wrote "+path) || strings.Contains(s, "routing:") {
		t.Errorf("the second update wrote the table again:\n%s", s)
	}
}

// publishFixture is the fixture the two publish tests share: the project
// fixture's own two repositories, both with a checkout this machine holds —
// because a table written with one of them unreadable is no table at all, and
// the migration's fail-safe would leave the node without a `model_routing`
// member and the test would prove nothing — and the account both of them run
// on. The extra block is the test's own.
func publishFixture(t *testing.T, extra string) *projectFixture {
	t.Helper()
	f := newProjectFixture(t, "claude_accounts:\n  ada: { allow_data_to: [acme/app, acme/other] }\n"+extra)
	origin := "https://github.com/acme/app.git"
	co := checkoutsAt(t, filepath.Join(f.dir, "src", "app"),
		strings.Replace(routingMigrateRepoYAML, "acme/api", "acme/app", -1), origin)
	checkoutsRemember(t, "aurora", co, origin)
	other := "https://bitbucket.org/acme/other"
	coOther := checkoutsAt(t, filepath.Join(f.dir, "src", "other"),
		"version: 1\nproject: aurora\ngit: { provider: bitbucket }\nagent: { auth: oauth, oauth_token: ada, model: claude-haiku-4-5 }\n"+
			"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n", other)
	// checkoutsRemember is the github shape alone; this fixture's second
	// repository is the bitbucket one its own config names.
	if err := localcfg.RecordCheckout(os.Getenv, "aurora",
		localcfg.CheckoutIdentity{Provider: "bitbucket", Repo: "acme/other"}, coOther, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestUpdatePublishesThemToTheNode: the same answer reaches the published node
// through RT4's publisher, in the update that migrates — and it reaches it from
// a copy of the config, so the config the run holds and plans from carries no
// table while the node does. The node's `model_routing` key is PL3's marker:
// the credentials service reads it as the installation's migration.
//
// The fixture is a project whose cascade serves, which is the shape both halves
// agree on: the pool the migration writes is the cascade's, and the publisher
// carries it as the ordinary legacy_open pool the node's readers resolve (F36).
// The pool the migration writes for a project whose cascade does not serve is
// the next test's.
func TestUpdatePublishesThemToTheNode(t *testing.T) {
	f := publishFixture(t, "routing: { claude: { via: cascade } }\n"+
		"credentials:\n  - { name: ada, type: claude-oauth }\n"+
		"pools:\n  - { name: cascade, members: [ada], cascade: true }\n")
	// A node that is there, so this is the publish an update makes of a project
	// it already serves rather than the first one a person confirms by typing
	// the project's name.
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{yes: true})
	if out, err := h.apply(t, true); err != nil {
		t.Fatalf("the update refused the publish: %v\n%s", err, out)
	}
	pol := publishedPolicy(t, f)
	if pol.ModelRouting == nil {
		t.Fatalf("the published node carries no model_routing: %s", nodeJSON(t, f, projectpolicy.PathShared()))
	}
	entry, ok := routingEntryFor(pol.ModelRouting.Entries, "claude-haiku-4-5")
	if !ok {
		t.Fatalf("the published table has no entry for claude-haiku-4-5: %+v", pol.ModelRouting.Entries)
	}
	if want := routing.SharedPool; len(entry.Chain) != 1 || entry.Chain[0].Pool != want {
		t.Errorf("the published entry's chain is %+v, want the shared pool %s alone", entry.Chain, want)
	}
	pool, ok := publishedCraftPool(t, pol, routing.SharedPool)
	if !ok || !pool.LegacyOpen {
		t.Errorf("craft_pools %s = %+v, %v, want the ordinary legacy_open pool a source of the table is resolved against (F36)", routing.SharedPool, pool, ok)
	}
	// The copy is what the publisher read: the run's own config is left as it
	// was, which is the whole reason the plan of this update cannot move.
	if len(h.s.e.lc.ModelRouting) > 0 {
		t.Errorf("the migration reached the run's own config, so this update plans from the migrated table: %+v", h.s.e.lc.ModelRouting)
	}
}

// TestUpdatePublishesNoTableTheNodeCannotCarry: a project whose cascade does
// not serve gets the same migration, and the pool it is served from — the
// installation's own accounts, nobody's pool — is one the publisher's rule
// leaves out of the node (an ownerless pool a config states is not published,
// D2608). The table is not published rather than refused: `fugaro update` may
// not end on a member of the migration's own answer (the HARD RULE), one line
// names the pool that stayed behind, and the installation's config keeps the
// table for the release that can publish it.
func TestUpdatePublishesNoTableTheNodeCannotCarry(t *testing.T) {
	f := publishFixture(t, "")
	// A node the stage has a reason to rewrite, so the test reads the one it
	// writes rather than a document nothing touched.
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	h := newMigrationRig(t, f, &initOptions{yes: true})
	out, err := h.apply(t, true)
	if err != nil {
		t.Fatalf("the update ended on the table the node cannot carry: %v\n%s", err, out)
	}
	if want := "the routing table was not published: the project's policy cannot carry the pool " + routing.SharedPool; !strings.Contains(out, want) {
		t.Errorf("the update does not say the table stayed behind (%q):\n%s", want, out)
	}
	if pol := publishedPolicy(t, f); pol.ModelRouting != nil {
		t.Errorf("the node carries a table naming a pool it does not carry: %+v", pol.ModelRouting)
	}
}

// routingEntryFor is the table's entry for one model.
func routingEntryFor(table routing.Table, model string) (routing.Entry, bool) {
	for _, e := range table {
		if e.Match == model {
			return e, true
		}
	}
	return routing.Entry{}, false
}
