package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// previousInstallation is the local config of an installation at the release
// before the craft vocabulary: two named Claude accounts, one of them the
// default for every stage, and a first-line review that runs on the other.
//
// The values are this fixture's own and never the spec's example: the spec's
// "Coder = DeepSeek through OpenRouter, Senior review = Opus through Claude
// OAuth" describes one installation, and a migration that wrote it into this
// one would be inventing a configuration nobody chose (reconciliation F17).
//
// The accounts carry allow_data_to, which is what `fugaro accounts add` writes
// for every account it declares (it refuses to declare one without, since a
// policy cannot bound it): the empty list is the shape no command creates, and
// a migration that only converted that shape would convert nothing in the
// installations this door exists for.
const previousInstallation = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
  team-b: { allow_data_to: [dimipaun/fugaro] }
default_claude_account: team-a
stages:
  review_first: { claude_account: team-b }
`

// migratedAccounts is the same installation as the keys the converge rigs
// append to their own config, so the door's tests read the previous release's
// shape and not a convenient one.
const migratedAccounts = "claude_accounts:\n" +
	"  team-a: { allow_data_to: [dimipaun/fugaro] }\n" +
	"  team-b: { allow_data_to: [dimipaun/fugaro] }\n" +
	"default_claude_account: team-a\nstages:\n  review_first: { claude_account: team-b }\n"

// craftStage is the stage whose account a craft stands for, for a test that has
// to say what each craft's access is: the first stage of the craft's own, in
// the run's order.
var craftStage = map[string]string{
	config.CraftCoder:          "implement",
	config.CraftReview:         "review_first",
	config.CraftSeniorReview:   "review",
	config.CraftSecurityReview: "adversarial",
}

// installationAccount is the account a stage runs on under the accounts design:
// the stage's own key, else the project's default. It is the answer the craft
// migration has to keep, read from the keys the migration came from.
func installationAccount(lc *localcfg.Config, stage string) string {
	if name := lc.ClaudeAccountStages[stage].ClaudeAccount; name != "" {
		return name
	}
	return lc.DefaultClaudeAccount
}

// TestCraftMigrationKeepsTheAccountEveryStageRunsOn is the migration's own
// test: the local config of an installation one release behind gains the
// credentials and the crafts its own account keys name, and the credential each
// craft resolves to afterwards is the account its stage ran on before.
//
// It is about the keys the migration writes rather than about the door's
// transcript, which TestUpdateConvergesPreviousInstallation reads.
func TestCraftMigrationKeepsTheAccountEveryStageRunsOn(t *testing.T) {
	before := parseLocalConfig(t, previousInstallation)
	lc := parseLocalConfig(t, previousInstallation)

	notes := migrateCraftKeys(lc)
	if len(notes) != 0 {
		t.Fatalf("the fixture migrates whole, but the run reported %v", notes)
	}
	for _, name := range []string{"team-a", "team-b"} {
		if !holdsCredential(lc, name) {
			t.Errorf("claude_accounts.%s did not become a credential: %+v", name, lc.Credentials)
		}
	}
	// The grant an account carries stays where it is. It is not moved into the
	// credential — a credential of this release has no field for it — and it is
	// not lost either, because the account entry the grant belongs to is still
	// in the file this migration only adds to.
	for _, name := range []string{"team-a", "team-b"} {
		if got := lc.ClaudeAccounts[name].AllowDataTo; !slices.Equal(got, []string{"dimipaun/fugaro"}) {
			t.Errorf("claude_accounts.%s.allow_data_to = %v after the migration, want the repositories it admitted before", name, got)
		}
	}
	// The one place that answers "which credential does this craft go out on"
	// (MC2) is what the test asks: the crafts resolve the credentials the
	// stages used, and every access names something this config defines.
	got := resolveCrafts(t, lc)
	for craftName, stage := range craftStage {
		want := installationAccount(before, stage)
		if got[craftName].Access != want {
			t.Errorf("craft %s resolves access %q, but its stage %s runs on %q before the migration", craftName, got[craftName].Access, stage, want)
		}
		if got[craftName].AccessFrom != craft.SourceLocal {
			t.Errorf("craft %s resolves its access from %q, want the installation's own config (%q)", craftName, got[craftName].AccessFrom, craft.SourceLocal)
		}
	}
}

// TestCraftMigrationIsIdempotent: the second run of the migration writes
// nothing and says nothing, so `fugaro update` twice is `fugaro update` once
// and a converged installation reports no changes.
func TestCraftMigrationIsIdempotent(t *testing.T) {
	lc := parseLocalConfig(t, previousInstallation)
	if notes := migrateCraftKeys(lc); len(notes) != 0 {
		t.Fatalf("the first run reported %v", notes)
	}
	once, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if notes := migrateCraftKeys(lc); len(notes) != 0 {
		t.Errorf("the second run reported %v", notes)
	}
	twice, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("the second migration changed the file:\n%s", lineDiff(string(once), string(twice)))
	}

	// A config the migration cannot convert whole is the other half of the
	// rule, and it is not about idempotence of the file but of the report: the
	// keys it cannot convert are named whenever it is run, because the file
	// says the same unresolved thing every time and the person who has to
	// handle them reads this run's output. Silence would swallow a key that
	// needs the owner — nothing else in the tree reports one.
	left := parseLocalConfig(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
  team-b: { allow_data_to: [dimipaun/fugaro] }
default_claude_account: team-a
stages:
  implement: { claude_account: team-b }
`)
	first := migrateCraftKeys(left)
	if len(first) == 0 {
		t.Fatal("the run that derived the crafts reported nothing about the craft it could not convert")
	}
	// The work it did converged: the second call adds nothing, and the file it
	// leaves is the file the first call left.
	converged, err := left.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if second := migrateCraftKeys(left); len(second) != len(first) {
		t.Errorf("the second run reported %v, want the keys the first run could not convert: %v", second, first)
	}
	again, err := left.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(converged, again) {
		t.Errorf("the second run changed the file:\n%s", lineDiff(string(converged), string(again)))
	}
	// An account that appears later is converted, and the note about the craft
	// the migration still cannot convert comes with it.
	left.ClaudeAccounts["team-c"] = config.ClaudeAccount{}
	if notes := migrateCraftKeys(left); len(notes) == 0 {
		t.Error("a run with an account left to convert reported nothing about the craft it still cannot convert")
	}
}

// The keys the new vocabulary cannot carry are left alone and named, and no
// craft key is written that would mean something other than the key it came
// from. Each case is one line of the migration's own report: the plan says a
// decision it does not answer is reported and not decided, and each of these is
// such a decision.
func TestCraftMigrationReportsWhatItCannotConvert(t *testing.T) {
	for _, tc := range []struct {
		name  string
		yaml  string
		want  []string // the accounts no craft key may name
		notes []string // what the report has to mention
	}{
		{
			name: "the unsuffixed default",
			yaml: `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
default_claude_account: default
`,
			want:  []string{"default"},
			notes: []string{"default_claude_account"},
		},
		{
			// The name is one a pool already holds, and a credential and a pool
			// share one namespace: config.ValidatePools refuses the two of one
			// name in the same block, so a credential written here would make
			// the file this run writes refuse itself — the local config write,
			// and so `fugaro update`, failing on its own output.
			name: "an account that is already a pool's name",
			yaml: `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
credentials:
  - { name: spare, type: claude-oauth }
pools:
  - { name: team-a, members: [spare] }
default_claude_account: team-a
`,
			want:  []string{"team-a"},
			notes: []string{"team-a", "pool"},
		},
		{
			// An account may be called `vertex` — the accounts block has no rule
			// against it — and the credential block has one: vertex is the
			// built-in credential of that name, the platform's own, which
			// carries no secret. An access naming it would send the stage's
			// calls to the built-in credential instead of this account's token,
			// so nothing names it and the account keeps its stages as they are.
			name: "an account named vertex",
			yaml: `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  vertex: { allow_data_to: [dimipaun/fugaro] }
default_claude_account: vertex
`,
			want:  []string{"vertex"},
			notes: []string{"vertex", "credential"},
		},
		{
			// A credential of another kind already holds the account's name. A
			// craft's access names a name and not a type, so a key written here
			// would send that craft's calls to the other credential — an
			// OpenRouter key, say — while this account's token serves nothing.
			// The name is taken, so nothing is derived and the clash is named.
			name: "an account whose name a credential of another type holds",
			yaml: `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
credentials:
  - { name: team-a, type: openrouter }
default_claude_account: team-a
`,
			want:  []string{"team-a"},
			notes: []string{"team-a", "another type (openrouter)"},
		},
		{
			name: "one craft, two accounts",
			yaml: `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
  team-b: { allow_data_to: [dimipaun/fugaro] }
default_claude_account: team-a
stages:
  implement: { claude_account: team-b }
`,
			want:  nil,
			notes: []string{"crafts.coder", "stages.implement.claude_account", "default_claude_account"},
		},
		{
			// The senior review's craft is two stages, and the tree gives the
			// triage the reviewer's role and the senior reviewer's model
			// (config.StageRole, config.MixedStageModel, D1848) — so a project
			// whose senior review names an account of its own runs its triage
			// on the default, and one access cannot say both. This is the
			// migration's sharpest refusal and it is reported rather than
			// resolved: the alternative is moving the triage's runs onto the
			// senior review's account without the owner asking for it.
			name: "the senior review and the triage",
			yaml: `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
  team-b: { allow_data_to: [dimipaun/fugaro] }
default_claude_account: team-a
stages:
  review: { claude_account: team-b }
`,
			want:  nil,
			notes: []string{"crafts.senior-review", "stages.review.claude_account", "team-b", "team-a"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := parseLocalConfig(t, tc.yaml)
			notes := migrateCraftKeys(lc)
			report := strings.Join(notes, "\n")
			for _, want := range tc.notes {
				if !strings.Contains(report, want) {
					t.Errorf("the migration's report does not mention %s:\n%s", want, report)
				}
			}
			// The crafts block itself is read, and not what a craft resolves
			// to: a craft the migration left unset resolves to the project's
			// default credential, which is vertex — the fallback is not a craft
			// key the migration wrote.
			for _, name := range tc.want {
				if holdsCredential(lc, name) {
					t.Errorf("%s became a credential though the config cannot hold one under that name: %+v", name, lc.Credentials)
				}
				for _, craft := range config.CraftNames {
					if lc.Crafts.Of(craft).Access == name {
						t.Errorf("crafts.%s.access names %s, which no credential of this release stands for", craft, name)
					}
				}
			}
			// Whatever the migration refused, the file it produces is one this
			// release reads: the door re-parses what it is about to write, and
			// a migration whose output that parse refuses would fail the write
			// — `fugaro update` refusing itself (CLAUDE.md's HARD RULE).
			data, err := lc.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			parseLocalConfigBytes(t, data)
		})
	}
}

// TestCraftMigrationKeepsANarrowedAccount is the shape every real installation
// has: `fugaro accounts add` writes claude_accounts.<name>.allow_data_to and
// has no way to declare an account without one, so an installation whose
// accounts the migration skipped would be every installation — no credential
// and no craft key derived anywhere.
//
// What the grant needs is not the credential: it belongs to the account entry,
// which this migration keeps and every reader of egress (the policy node's
// allow_repos, doctor, the reach a secret is allowed) still reads it from.
func TestCraftMigrationKeepsANarrowedAccount(t *testing.T) {
	const narrowed = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [acme/app] }
  team-b: { allow_data_to: [acme/app] }
default_claude_account: team-a
stages:
  review_first: { claude_account: team-b }
`
	lc := parseLocalConfig(t, narrowed)
	if notes := migrateCraftKeys(lc); len(notes) != 0 {
		t.Fatalf("an account narrowed by allow_data_to is the shape the CLI writes, and the migration reported %v", notes)
	}
	for _, name := range []string{"team-a", "team-b"} {
		if !holdsCredential(lc, name) {
			t.Errorf("claude_accounts.%s admits two repositories and became no credential: %+v", name, lc.Credentials)
		}
	}
	got := resolveCrafts(t, lc)
	for craftName, stage := range craftStage {
		if want := installationAccount(lc, stage); got[craftName].Access != want {
			t.Errorf("craft %s resolves access %q, want the account its stage %s runs on (%q)", craftName, got[craftName].Access, stage, want)
		}
	}
	// And the grant is still the file's, which is what makes the conversion
	// safe to make: nothing here moved it and nothing dropped it.
	if allow := lc.ClaudeAccounts["team-b"].AllowDataTo; !slices.Equal(allow, []string{"acme/app"}) {
		t.Errorf("claude_accounts.team-b.allow_data_to = %v, want the repositories it admitted before the migration", allow)
	}
}

// TestCraftMigrationKeepsAnOwnerWrittenCraftAccess: a crafts.<craft>.access the
// owner has written is left exactly as it is, and the crafts the owner said
// nothing about are derived beside it.
//
// A craft the owner pointed at another credential must keep spending it. The
// migration is the only writer of this key (no command writes one, and the
// generic settings path refuses a family-owned row), and it runs on every write
// of the installation's config — `fugaro update`, but also the setup and init
// paths that write the same file — so a pass that dropped or reordered the skip
// would move that craft's runs onto the account its stages name, on the next
// write, with nothing printed and nothing to put it back.
func TestCraftMigrationKeepsAnOwnerWrittenCraftAccess(t *testing.T) {
	// The coder's stages run on team-a (the project's default). The owner
	// pointed the craft at team-b, which is what the accounts design could not
	// say and the crafts block is for.
	const owned = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
  team-b: { allow_data_to: [dimipaun/fugaro] }
default_claude_account: team-a
stages:
  review_first: { claude_account: team-b }
crafts:
  coder: { access: team-b }
`
	lc := parseLocalConfig(t, owned)
	if notes := migrateCraftKeys(lc); len(notes) != 0 {
		t.Fatalf("a craft the owner has already pointed somewhere migrates whole, but the run reported %v", notes)
	}
	if got := lc.Crafts.Of(config.CraftCoder).Access; got != "team-b" {
		t.Errorf("crafts.coder.access = %q after the migration, want the team-b the owner wrote", got)
	}
	// The rest of the block is still the migration's: one access the owner wrote
	// is not a reason to leave the crafts beside it unset.
	for craftName, want := range map[string]string{
		config.CraftReview:         "team-b",
		config.CraftSeniorReview:   "team-a",
		config.CraftSecurityReview: "team-a",
	} {
		if got := lc.Crafts.Of(craftName).Access; got != want {
			t.Errorf("crafts.%s.access = %q, want the %s its stage runs on", craftName, got, want)
		}
	}
	// And the key survives the file: the next write reads the owner's craft back
	// and keeps it again, which is what makes the skip worth its lines on the
	// third write and the tenth as much as on this one.
	once, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	next := parseLocalConfigBytes(t, once)
	if notes := migrateCraftKeys(next); len(notes) != 0 {
		t.Errorf("the second run reported %v", notes)
	}
	twice, err := next.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("the second migration changed the file:\n%s", lineDiff(string(once), string(twice)))
	}
	if got := next.Crafts.Of(config.CraftCoder).Access; got != "team-b" {
		t.Errorf("crafts.coder.access = %q after the file was written and read back, want the team-b the owner wrote", got)
	}
}

// TestUpdateConvergesPreviousInstallation: an installation at the previous
// release with default_claude_account set reaches the craft vocabulary with
// `fugaro update` alone, and doctor is clean afterwards (the HARD RULE, F17).
func TestUpdateConvergesPreviousInstallation(t *testing.T) {
	r := newConvergeRig(t, v4NodeWithAccounts())
	r.appendConfig(t, migratedAccounts)
	// The keys go in before the copy the door reads is taken: the rig clears
	// FUGARO_CONFIG, so the local config `fugaro update` reads and writes is the
	// one this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	written := parseLocalConfigBytes(t, readFile(t, installationConfigPath()))
	for _, name := range []string{"team-a", "team-b"} {
		if !holdsCredential(written, name) {
			t.Errorf("after fugaro update, credentials holds no %s: %+v", name, written.Credentials)
		}
	}
	// The grant is the account's and stays its own: the account entry is still
	// in the file (nothing here removes one), so deriving a credential for a
	// narrowed account takes no reach from anyone, and the readers that decide
	// egress — the policy node's allow_repos, doctor, the reach of a secret —
	// read exactly what they read before.
	for _, name := range []string{"team-a", "team-b"} {
		if allow := written.ClaudeAccounts[name].AllowDataTo; !slices.Equal(allow, []string{"dimipaun/fugaro"}) {
			t.Errorf("after fugaro update, claude_accounts.%s.allow_data_to = %v, want the repositories it admitted before", name, allow)
		}
	}
	got := resolveCrafts(t, written)
	for craftName, stage := range craftStage {
		if want := installationAccount(written, stage); got[craftName].Access != want {
			t.Errorf("after fugaro update, craft %s resolves access %q, but its stage %s runs on %q", craftName, got[craftName].Access, stage, want)
		}
	}
	// The account keys are still there, and that is deliberate: the launch, the
	// runner and `fugaro models` read them until MC5 moves those readers to the
	// crafts, and a migration that emptied them first would leave a run with no
	// account at all.
	if written.DefaultClaudeAccount != "team-a" {
		t.Errorf("the migration removed default_claude_account: %q", written.DefaultClaudeAccount)
	}
}

// TestUpdateIsIdempotent: the config `fugaro update` leaves behind is one the
// migration has nothing left to do to — the keys it wrote are the keys it would
// write, and a second pass over the file changes none of its bytes.
//
// The door is not run twice here: this rig's first run onboards the repository,
// which leaves the checkout different from the default branch, and the second
// run's repository stage stops for the user over exactly that (its own, correct
// refusal). What idempotence means for this migration is a property of the file
// it produced, and that is what the test reads.
func TestUpdateIsIdempotent(t *testing.T) {
	r := newConvergeRig(t, v4NodeWithAccounts())
	r.appendConfig(t, migratedAccounts)
	// The keys go in before the copy the door reads is taken: the rig clears
	// FUGARO_CONFIG, so the local config `fugaro update` reads and writes is the
	// one this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	written := parseLocalConfigBytes(t, readFile(t, installationConfigPath()))
	credentials, crafts := slices.Clone(written.Credentials), written.Crafts
	if notes := migrateCraftKeys(written); len(notes) != 0 {
		t.Errorf("the config fugaro update wrote is not converged: %v", notes)
	}
	if !slices.Equal(written.Credentials, credentials) {
		t.Errorf("a second pass over the file fugaro update wrote adds credentials: %+v, was %+v", written.Credentials, credentials)
	}
	if written.Crafts != crafts {
		t.Errorf("a second pass over the file fugaro update wrote rewrites the crafts: %+v, was %+v", written.Crafts, crafts)
	}
}

// TestUpdateReportsTheKeysItCannotConvert: what the migration could not
// convert reaches the person running the one command, and it arrives with the
// write it belongs to. A report printed before the diff a person is asked to
// confirm — or on a run that writes nothing, because the file is current
// already — is one nobody reads, and the keys it is about are the same keys
// every time.
func TestUpdateReportsTheKeysItCannotConvert(t *testing.T) {
	node := v4NodeWithAccounts()
	// The node carries the stage key too: an account key the node does not
	// carry is a widening of what the project's runs may reach, and publishing
	// one needs a typed confirmation at a real terminal (policyAccountWidening).
	node["claude_account_stages"] = map[string]any{"implement": "team-b"}
	r := newConvergeRig(t, node)
	r.appendConfig(t, "claude_accounts:\n"+
		"  team-a: { allow_data_to: [dimipaun/fugaro] }\n"+
		"  team-b: { allow_data_to: [dimipaun/fugaro] }\n"+
		"default_claude_account: team-a\nstages:\n  implement: { claude_account: team-b }\n")
	registerInstallation(t, r.initRig)
	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	note := "note: crafts.coder:"
	if !strings.Contains(out, note) {
		t.Fatalf("the run did not report the craft whose stages name different accounts:\n%s", out)
	}
	if at, wrote := strings.Index(out, note), strings.Index(out, "wrote "+installationConfigPath()); wrote < 0 || at < wrote {
		t.Errorf("the migration's report is not printed with the write of the config it describes (wrote at %d, note at %d):\n%s", wrote, at, out)
	}
	// Its refusal is about the craft key alone: the credentials the accounts
	// name are derived all the same.
	written := parseLocalConfigBytes(t, readFile(t, installationConfigPath()))
	for _, name := range []string{"team-a", "team-b"} {
		if !holdsCredential(written, name) {
			t.Errorf("a craft it could not convert left the accounts unconverted too: %+v", written.Credentials)
		}
	}
}

// TestWriteLocalConfigReportsAnUnconvertibleAccountOnACurrentFile: the notes
// are a state of the file and not a delta, so a write whose file is already
// current reports the account it cannot convert under the line that says so,
// and never before the confirmation a diff is asked for. A config the
// migration converted whole has no note on either path, which is what keeps
// the converged installation's `fugaro update` silent.
func TestWriteLocalConfigReportsAnUnconvertibleAccountOnACurrentFile(t *testing.T) {
	// The account's name is a pool's, so no credential may be derived from it
	// and no craft key may name it: the migration has nothing to add, and a
	// note to print.
	lc := parseLocalConfig(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
credentials:
  - { name: spare, type: claude-oauth }
pools:
  - { name: team-a, members: [spare] }
default_claude_account: team-a
`)
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	cmd := newSetupCmd()
	cmd.SetOut(&out)
	r := newInitRun(cmd, &initOptions{yes: true, cloud: cloudOptions{stderr: discard}})
	path := filepath.Join(t.TempDir(), "config.yaml")

	if err := r.writeLocalConfig(lc, path, nil, true); err != nil {
		t.Fatal(err)
	}
	written := out.String()
	note := "note: the account team-a"
	if at, wrote := strings.Index(written, note), strings.Index(written, "wrote "+path); wrote < 0 || at < wrote {
		t.Errorf("the write did not report the account it could not convert (wrote at %d, note at %d):\n%s", wrote, at, written)
	}

	out.Reset()
	if err := r.writeLocalConfig(lc, path, readFile(t, path), true); err != nil {
		t.Fatal(err)
	}
	current := out.String()
	if up, at := strings.Index(current, "is up to date"), strings.Index(current, note); up < 0 || at < up {
		t.Errorf("the current file did not report the account it cannot convert (up to date at %d, note at %d):\n%s", up, at, current)
	}

	// The same path with a config the migration converts whole says nothing.
	out.Reset()
	if err := r.writeLocalConfig(parseLocalConfig(t, previousInstallation), path, readFile(t, path), true); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Contains(got, "note:") {
		t.Errorf("a converged config reported something:\n%s", got)
	}
}

// v4NodeWithAccounts is v4Node carrying the account-choice members this
// fixture's local config states, so the policy stage's publish is a publish of
// the verifier list alone: an account key the node does not carry is a widening
// of what every run of the project may reach, and publishing one needs a typed
// confirmation at a real terminal (policyAccountWidening), which is the one
// thing a headless test cannot give.
func v4NodeWithAccounts() map[string]any {
	node := v4Node()
	node["default_claude_account"] = "team-a"
	node["claude_account_stages"] = map[string]any{"review_first": "team-b"}
	return node
}

// installationConfigPath is the local config the door reads and writes in a
// rig: FUGARO_CONFIG is cleared by the converge rig, so the project resolves to
// the copy `fugaro setup` leaves in the XDG projects directory, and that is the
// file `fugaro update` writes.
func installationConfigPath() string {
	return filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml")
}

// parseLocalConfig reads a fixture through the same decoder the door reads a
// file with, so a fixture the release would refuse cannot pass for a previous
// installation.
func parseLocalConfig(t *testing.T, yaml string) *localcfg.Config {
	t.Helper()
	return parseLocalConfigBytes(t, []byte(yaml))
}

func parseLocalConfigBytes(t *testing.T, data []byte) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("parsing the fixture: %v\n%s", err, data)
	}
	return lc
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// resolveCrafts is what each craft goes out on, from the installation's own
// layer, through the one resolver that answers it (internal/craft, MC2).
func resolveCrafts(t *testing.T, lc *localcfg.Config) map[string]craft.Effective {
	t.Helper()
	eff, problems := craft.Resolve([]craft.Layer{{Name: craft.SourceLocal, Crafts: lc.Crafts, Credentials: lc.Credentials, Pools: lc.Pools}}, false)
	if len(problems) > 0 {
		t.Errorf("the migrated config does not resolve: %+v", problems)
	}
	return eff
}

// holdsCredential is whether the migration derived the account's own credential
// — the typed entry this migration writes. A credential of another type under
// the same name is the clash it refuses over, not the entry it derived.
func holdsCredential(lc *localcfg.Config, name string) bool {
	for _, cred := range lc.Credentials {
		if cred.Name == name && cred.Type == config.CredentialTypeClaudeOAuth {
			return true
		}
	}
	return false
}
