package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/routing"
)

// routingMigrateRig is the fixture MG2's tests drive: this machine holds one
// checkout of the installation's repository — which is where the migration
// reads the repository's own file from — and a run of the update door, whose
// writer is the buffer the report lands in.
func routingMigrateRig(t *testing.T, repoYAML string) (*initRun, string, *bytes.Buffer) {
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
	return newInitRun(cmd, o), filepath.Join(dir, "config.yaml"), &out
}

// routingMigrateRepoYAML is acme/api's own file as the release behind leaves
// it: an oauth repository whose stages run on the account ada, calling one
// Claude model.
const routingMigrateRepoYAML = "version: 1\nproject: aurora\ngit: { provider: github }\n" +
	"agent: { auth: oauth, oauth_token: ada, model: claude-haiku-4-5 }\n" +
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
// calls, with the shared pool in its chain, and the pool of one D3176 wraps
// the account in. Before this release the key was an unknown field and the
// update wrote no table at all.
func TestUpdateMigratesToTheTable(t *testing.T) {
	r, path, _ := routingMigrateRig(t, routingMigrateRepoYAML)
	lc := parseLocalConfig(t, routingMigrateLocalYAML)
	if err := r.writeLocalConfig(lc, path, nil, true); err != nil {
		t.Fatal(err)
	}
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
	if len(written.Pools) != 2 {
		t.Fatalf("the update wrote %d pools (%+v), want the shared pool and the account's pool of one (D3176, D3183)", len(written.Pools), written.Pools)
	}
	pools := map[string]bool{}
	for _, p := range written.Pools {
		pools[p.Name] = true
	}
	for _, name := range []string{routing.SharedPool, "ada"} {
		if !pools[name] {
			t.Errorf("the update wrote no pool %q: %+v", name, written.Pools)
		}
	}
	// A pool and a credential of one name may not both exist (config.ValidatePools):
	// the account's pool of one stands for the credential the craft migration
	// derived for it.
	for _, c := range written.Credentials {
		if c.Name == "ada" {
			t.Errorf("the account's credential %q was left beside the pool of the same name: an access names a name and not a kind, so the file refuses itself", c.Name)
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
	r, path, out := routingMigrateRig(t, repoYAML)
	// A pool with two members, the spread switch off: it rotates among its
	// tokens once migrated (D3177). And a project whose fallback is auto: a run
	// pays on the key as soon as its subscriptions are exhausted (D3192).
	lc := parseLocalConfig(t, routingMigrateLocalYAML+`pools:
  - { name: pair, members: [ada, grace], strategy: failover }
credentials:
  - { name: ada, type: claude-oauth }
  - { name: grace, type: claude-oauth }
credential_cascade: { fallback: auto }
`)
	if err := r.writeLocalConfig(lc, path, nil, true); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{
		"routing: acme/api's runs go out on the account ada, as they do today",
		"routing: the pool pair rotates among its tokens now that subscription_spread is gone",
		"routing: fallback is auto, so a run pays on the project's key as soon as its subscriptions are exhausted",
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
	r, path, out := routingMigrateRig(t, routingMigrateRepoYAML)
	first := parseLocalConfig(t, routingMigrateLocalYAML)
	if err := r.writeLocalConfig(first, path, nil, true); err != nil {
		t.Fatal(err)
	}
	once := readFile(t, path)
	out.Reset()

	// A second run, on the file the first one left: the marker is the file's
	// own, and a fresh run is what reads it rather than this run's guard.
	o := &initOptions{yes: true, cloud: cloudOptions{stderr: discard}}
	cmd := newUpdateCmdWith(o)
	cmd.SetOut(out)
	second := newInitRun(cmd, o)
	if err := second.writeLocalConfig(parseLocalConfigBytes(t, once), path, once, true); err != nil {
		t.Fatal(err)
	}
	if now := readFile(t, path); !bytes.Equal(once, now) {
		t.Errorf("the second update changed the installation's config:\n--- first\n%s\n--- second\n%s", once, now)
	}
	if !strings.Contains(out.String(), "is up to date") {
		t.Errorf("the second update did not report the config as current:\n%s", out.String())
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
