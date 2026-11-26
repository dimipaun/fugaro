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

// aliasInstallation is previousInstallation with its account keys naming
// aliases instead of accounts: the default names the alias standing for team-a
// and the first-line review names the one standing for team-b. An alias is a
// name the choice keys may hold (D2091), and the migration reads two of them —
// default_claude_account and stages.<stage>.claude_account — so the keys it
// derives have to be the accounts those aliases stand for, which is the account
// each stage ran on before (D2102).
const aliasInstallation = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: { allow_data_to: [dimipaun/fugaro] }
  team-b: { allow_data_to: [dimipaun/fugaro] }
claude_account_aliases:
  primary: team-a
  reviewer: team-b
default_claude_account: primary
stages:
  review_first: { claude_account: reviewer }
`

// TestCraftMigrationResolvesAnAlias: `fugaro update` on an installation whose
// account keys name aliases derives the crafts from the accounts the aliases
// stand for. A craft key written with the alias' own name would name something
// no credential of this release stands for — an alias is a name the choice keys
// hold, never an account's name (D2092) — and the run would go out on a
// credential the resolver cannot find.
//
// It is the same installation as TestCraftMigrationKeepsTheAccountEveryStage
// RunsOn under aliases, so the crafts it resolves are the ones that test pins.
func TestCraftMigrationResolvesAnAlias(t *testing.T) {
	before := parseLocalConfig(t, aliasInstallation)
	lc := parseLocalConfig(t, aliasInstallation)

	if notes := migrateCraftKeys(lc); len(notes) != 0 {
		t.Fatalf("the fixture migrates whole, but the run reported %v", notes)
	}
	// The alias is not an account, so no credential is derived from it: the
	// file gains one credential per account and none per name pointing at one.
	for _, alias := range []string{"primary", "reviewer"} {
		if holdsCredential(lc, alias) {
			t.Errorf("the alias %s became a credential, and an alias is not an account", alias)
		}
	}
	got := resolveCrafts(t, lc)
	for craftName, stage := range craftStage {
		want := installationAccount(before, stage)
		account, alias := lc.ResolveAccount(want)
		if alias == "" {
			t.Fatalf("the fixture's stage %s names %q, which is not one of its aliases", stage, want)
		}
		if got[craftName].Access != account {
			t.Errorf("craft %s resolves access %q, but its stage %s runs on the account %q the alias %s stands for", craftName, got[craftName].Access, stage, account, alias)
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

// TestCraftMigrationSpellsAnAbsentDefaultInAConflict: a craft whose stages
// disagree is reported with the accounts they name, and in a config that sets
// no default_claude_account at all the conflicting side is the stages that name
// no account — they run on the unsuffixed secret, whose name is not the empty
// string installationAccounts resolved them to. Printed as a name that string
// leaves a hole in the list ("team-b, ") and no key can be listed for it either,
// the file holding no default_claude_account to name.
func TestCraftMigrationSpellsAnAbsentDefaultInAConflict(t *testing.T) {
	// The coder's implement names team-b and its plan and fix name nothing, and
	// there is no default for them: one craft, two accounts, no access.
	const noDefault = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-b: { allow_data_to: [dimipaun/fugaro] }
stages:
  implement: { claude_account: team-b }
`
	lc := parseLocalConfig(t, noDefault)
	report := strings.Join(migrateCraftKeys(lc), "\n")
	if want := "(the unsuffixed default secret, team-b, the unsuffixed default secret)"; !strings.Contains(report, want) {
		t.Errorf("the conflict note does not spell the account of the stages that name none:\n%s", report)
	}
	if !strings.Contains(report, "stages.implement.claude_account and the stages that name no account name different accounts") {
		t.Errorf("the conflict note names one key and claims they disagree:\n%s", report)
	}
	// The phrase is the neighbouring note's own, and that note says why the two
	// halves are the same account: a config that sets no default uses the
	// unsuffixed secret, which no credential may be named after.
	if !strings.Contains(report, "the unsuffixed default secret (default_claude_account is not set)") {
		t.Errorf("the report does not say the unsuffixed secret is what the unset default means:\n%s", report)
	}
	// Nothing in the report is an empty name: "" is the account a stage names
	// none, and a list with a hole in it reads as a name that went missing.
	for _, hole := range []string{"(, ", ", )", ", ,"} {
		if strings.Contains(report, hole) {
			t.Errorf("the report prints an empty account name (%q):\n%s", hole, report)
		}
	}
	if got := lc.Crafts.Of(config.CraftCoder).Access; got != "" {
		t.Errorf("crafts.coder.access = %q, want no key for the craft whose stages disagree", got)
	}
	data, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parseLocalConfigBytes(t, data)
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
	r := newConvergeRig(t, v7NodeWithAccounts())
	r.appendConfig(t, migratedAccounts)
	// The keys go in before the copy the door reads is taken: the rig clears
	// FUGARO_CONFIG, so the local config `fugaro update` reads and writes is the
	// one this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

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
	r := newConvergeRig(t, v7NodeWithAccounts())
	r.appendConfig(t, migratedAccounts)
	// The keys go in before the copy the door reads is taken: the rig clears
	// FUGARO_CONFIG, so the local config `fugaro update` reads and writes is the
	// one this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

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
	node := v7NodeWithAccounts()
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
	// The note and the write it belongs to are the run's own account of the
	// file it converged, so both are in the log beside the screen (stageDetail),
	// and the order between them is the log's.
	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"}) + newestLog(t, "update")

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

// crossedPoolInstallation is the blocks an installation whose pool spans the two
// halves of a run carries: the pool names oauth-two, which the credentials
// service serves, and router-one, which the run's own gateway does, and the
// crafts block below names that pool. It is the shape the release behind wrote
// and read as valid, and the one this release's own config write has to converge
// (MC20, MC21).
//
// The credentials are the block `fugaro accounts pool add`'s own tests use, so
// the two halves are the seam's own pair rather than a convenient one, and the
// pool carries everything a pool can — a strategy, an owner and an allowed user
// — so a split has to carry them onto both halves instead of deriving fresh
// ones.
//
// The blocks alone and no installation header: they are appended to a config
// that already carries version, name and the installation's own keys (the rig's
// and the update's own file), and a second `version:` key is a duplicate the
// strict decode refuses. The file ends inside the pools block, so a test may
// append a pool of its own to it, and the crafts are a separate const for the
// same reason.
const crossedPoolInstallation = accountsPoolCredentials + `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api/v1
    auth: bearer
    secret: openrouter-key
    models: ["deepseek/*"]
pools:
  - name: mixed
    strategy: round-robin
    owner: owner@example.com
    allowed_users: [other@example.com]
    members: [router-one, oauth-two, oauth-one]
`

// crossedPoolCrafts is the crafts block of the fixture above, and every entry is
// one of the rule's own cases:
//
//   - the coder's Claude ID goes direct, which is the credentials service's half;
//   - the review's DeepSeek model is claimed by the openrouter entry, and a
//     provider route is the run's own gateway;
//   - the merge judge sets no model and follows the coder's, which is the
//     inherited model and not the fallback below;
//   - the senior review sets no model and follows nobody (config.CraftParent:
//     the spec makes it required), so it is the craft the rule answers with the
//     pool's own first member;
//   - the security review names a credential and not a pool, so nothing
//     repoints it.
//
// The pool lists the gateway's member FIRST, which is what tells the inherited
// model and the fallback apart: the merge judge's answer is the credentials
// service's half, and a rule that read no model would have sent it to the
// gateway's.
const crossedPoolCrafts = `crafts:
  coder: { model: claude-opus-5-5, access: mixed }
  review: { model: deepseek/deepseek-v4-flash, access: mixed }
  merge-judge: { access: mixed }
  senior-review: { access: mixed }
  security-review: { access: oauth-one }
`

// poolNamed is the pool of this name, and whether the block holds one.
func poolNamed(lc *localcfg.Config, name string) (config.Pool, bool) {
	for _, p := range lc.Pools {
		if p.Name == name {
			return p, true
		}
	}
	return config.Pool{}, false
}

// TestTheSplitKeepsEveryMemberAndItsOrder: each half holds the members that half
// serves, in the order the pool listed them, and carries the pool's strategy,
// owner and allowed users. The split is a partition of the list and not a
// rewrite of it — a pool's listed order is what failover walks (D1632) — so a
// half that reordered or dropped a member would send a call to a credential the
// owner did not put there.
//
// The halves take the place of the pool that crossed, in the order the plan
// names them (the credentials service's first, which is the sorted order of the
// paths), so a reader of the block finds them where the pool was.
func TestTheSplitKeepsEveryMemberAndItsOrder(t *testing.T) {
	lc := parseLocalConfig(t, initConfig+crossedPoolInstallation+crossedPoolCrafts)
	migrateCraftKeys(lc)

	claude, ok := poolNamed(lc, "mixed-claude")
	if !ok {
		t.Fatalf("the split wrote no mixed-claude: %+v", lc.Pools)
	}
	provider, ok := poolNamed(lc, "mixed-provider")
	if !ok {
		t.Fatalf("the split wrote no mixed-provider: %+v", lc.Pools)
	}
	if _, ok := poolNamed(lc, "mixed"); ok {
		t.Errorf("the pool that crossed is still in the block: %+v", lc.Pools)
	}
	if got, want := claude.Members, []string{"oauth-two", "oauth-one"}; !slices.Equal(got, want) {
		t.Errorf("mixed-claude.members = %v, want %v: the service's members, in the order the pool listed them", got, want)
	}
	if got, want := provider.Members, []string{"router-one"}; !slices.Equal(got, want) {
		t.Errorf("mixed-provider.members = %v, want %v: the gateway's members alone", got, want)
	}
	for _, half := range []config.Pool{claude, provider} {
		if half.Owner != "owner@example.com" || half.Strategy != "round-robin" || !slices.Equal(half.AllowedUsers, []string{"other@example.com"}) {
			t.Errorf("%s = %+v, want the pool's owner, strategy and allowed users carried onto both halves", half.Name, half)
		}
	}
	var names []string
	for _, p := range lc.Pools {
		names = append(names, p.Name)
	}
	if want := []string{"mixed-claude", "mixed-provider"}; !slices.Equal(names, want) {
		t.Errorf("the block's pools = %v, want %v: the halves stand where the pool that crossed stood", names, want)
	}
	// The file the split wrote has to read back: a name that collides with a
	// credential's, or a half with no member, is a config this release refuses
	// — and it would be this migration's own output that did it.
	data, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back := parseLocalConfigBytes(t, data)
	if got, ok := poolNamed(back, "mixed-claude"); !ok || !slices.Equal(got.Members, claude.Members) {
		t.Errorf("the written config reads back as %+v, want the halves this run wrote", back.Pools)
	}
}

// TestTheSplitRepointsOnlyTheCraftsThatNeedIt: the crafts whose access named the
// pool follow the half their own model routes to, and every other craft keeps
// the access it had. The half is the craft's own model and nothing else — a
// craft that sets none follows the craft it inherits from (config.CraftParent) —
// and a craft naming a credential, or a pool that did not cross, is left alone:
// repointing one of those would be the migration choosing a spending path for a
// craft that already had one.
func TestTheSplitRepointsOnlyTheCraftsThatNeedIt(t *testing.T) {
	lc := parseLocalConfig(t, initConfig+crossedPoolInstallation+crossedPoolCrafts)
	migrateCraftKeys(lc)

	for _, tc := range []struct{ craft, want, why string }{
		{config.CraftCoder, "mixed-claude", "its Claude ID goes direct, which is the credentials service's half"},
		{config.CraftReview, "mixed-provider", "the openrouter entry claims that model, and a provider route is the run's own gateway"},
		{config.CraftMergeJudge, "mixed-claude", "it sets no model and follows the coder's"},
		{config.CraftSeniorReview, "mixed-provider", "it follows nobody, so it keeps the half the pool's first member is on"},
		{config.CraftSecurityReview, "oauth-one", "it named a credential and not the pool"},
	} {
		if got := lc.Crafts.Of(tc.craft).Access; got != tc.want {
			t.Errorf("crafts.%s.access = %q, want %q: %s", tc.craft, got, tc.want, tc.why)
		}
	}
	if got := lc.Crafts.Of(config.CraftCoder).Model; got != "claude-opus-5-5" {
		t.Errorf("crafts.coder.model = %q, want the model the owner wrote: the split writes the access half alone", got)
	}
	// Every access the run leaves behind names something the file defines, which
	// is the one thing a repointed craft could get wrong.
	for _, name := range config.CraftNames {
		access := lc.Crafts.Of(name).Access
		if access == "" {
			continue
		}
		if _, ok := poolNamed(lc, access); ok {
			continue
		}
		if !holdsCredential(lc, access) {
			t.Errorf("crafts.%s.access = %q, which this config defines neither as a pool nor as a credential", name, access)
		}
	}
}

// TestEveryCraftCanBeWrittenAnAccess is the guard under setCraftAccess's switch:
// its callers walk config.CraftNames and write the access each craft is owed, so
// a craft the switch has no case for is a craft whose derived access is dropped
// without a word — which is exactly how the merge judge kept naming the pool the
// split had just replaced (MC21). A sixth craft added to the list fails here
// rather than in whichever migration reaches it second.
func TestEveryCraftCanBeWrittenAnAccess(t *testing.T) {
	var crafts config.Crafts
	for _, name := range config.CraftNames {
		setCraftAccess(&crafts, name, "somewhere")
		if got := crafts.Of(name).Access; got != "somewhere" {
			t.Errorf("setCraftAccess(%s) left access %q, want somewhere: every craft of config.CraftNames needs a case", name, got)
		}
	}
}

// TestASinglePathPoolIsUntouched: a pool whose members are all served by one
// half is left where it is, name and members and all, and a craft naming it is
// not repointed. The rule is about a pool that spans the two halves, and a
// migration that rewrote every pool would rename pools the owner wrote and
// repoint crafts that were already on the half their model routes to.
func TestASinglePathPoolIsUntouched(t *testing.T) {
	const singlePathPool = "  - { name: subs, members: [oauth-one, oauth-two] }\n"
	const namingIt = "crafts:\n" +
		"  coder: { model: claude-opus-5-5, access: subs }\n" +
		"  review: { model: deepseek/deepseek-v4-flash, access: mixed }\n"
	lc := parseLocalConfig(t, initConfig+crossedPoolInstallation+singlePathPool+namingIt)
	migrateCraftKeys(lc)

	subs, ok := poolNamed(lc, "subs")
	if !ok {
		t.Fatalf("the split rewrote a pool of one path away: %+v", lc.Pools)
	}
	if got, want := subs.Members, []string{"oauth-one", "oauth-two"}; !slices.Equal(got, want) {
		t.Errorf("subs.members = %v, want %v: the pool is not the one that crossed", got, want)
	}
	if got := lc.Crafts.Of(config.CraftCoder).Access; got != "subs" {
		t.Errorf("crafts.coder.access = %q, want subs: a craft naming a pool of one path is not repointed", got)
	}
	if got := lc.Crafts.Of(config.CraftReview).Access; got != "mixed-provider" {
		t.Errorf("crafts.review.access = %q, want mixed-provider: the pool that did cross is split all the same", got)
	}
	for _, name := range []string{"subs-claude", "subs-provider"} {
		if _, ok := poolNamed(lc, name); ok {
			t.Errorf("the split derived %s from a pool of one path: %+v", name, lc.Pools)
		}
	}
	// The pool that did cross is split all the same, so the test is about the
	// single-path pool and not about a migration that did nothing.
	if _, ok := poolNamed(lc, "mixed-claude"); !ok {
		t.Errorf("the crossed pool was not split: %+v", lc.Pools)
	}
}

// TestUpdateSplitsACrossPathPool is MC21's own convergence test, the one
// CLAUDE.md's HARD RULE asks of a rule that tightens a format: an installation
// whose config carries a pool naming members of both halves reaches this release
// with `fugaro update` alone, and the file it leaves behind is one this release
// reads — the pool split in two, each craft on the half its own model routes to.
//
// Nothing else is asked of the operator: the split is part of the config write
// the update already makes, so there is no second command for the pool and none
// for the node the same run migrates (internal/projectpolicy's own step).
func TestUpdateSplitsACrossPathPool(t *testing.T) {
	r := newConvergeRig(t, v7NodeWithAccounts())
	r.appendConfig(t, crossedPoolInstallation+crossedPoolCrafts)
	// The keys go in before the copy the door reads is taken: the rig clears
	// FUGARO_CONFIG, so the local config `fugaro update` reads and writes is the
	// one this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	written := parseLocalConfigBytes(t, readFile(t, installationConfigPath()))
	claude, ok := poolNamed(written, "mixed-claude")
	if !ok {
		t.Fatalf("after fugaro update the installation holds no mixed-claude: %+v", written.Pools)
	}
	provider, ok := poolNamed(written, "mixed-provider")
	if !ok {
		t.Fatalf("after fugaro update the installation holds no mixed-provider: %+v", written.Pools)
	}
	if !slices.Equal(claude.Members, []string{"oauth-two", "oauth-one"}) || !slices.Equal(provider.Members, []string{"router-one"}) {
		t.Errorf("after fugaro update the halves are %v and %v, want the pool's members by the half that serves them", claude.Members, provider.Members)
	}
	if _, ok := poolNamed(written, "mixed"); ok {
		t.Errorf("after fugaro update the pool that crossed is still in the file: %+v", written.Pools)
	}
	for _, tc := range []struct{ craft, want string }{
		{config.CraftCoder, "mixed-claude"},
		{config.CraftReview, "mixed-provider"},
		{config.CraftSecurityReview, "oauth-one"},
	} {
		if got := written.Crafts.Of(tc.craft).Access; got != tc.want {
			t.Errorf("after fugaro update crafts.%s.access = %q, want %q", tc.craft, got, tc.want)
		}
	}
}

// TestUpdateIsIdempotentForTheSplit: the file `fugaro update` leaves behind is
// one the split has nothing left to do to, so the second update of an
// installation says nothing and writes nothing — which is what a migration whose
// rule reads the block it rewrites has to be, or every update would split a half
// again and the names would grow a number apiece.
func TestUpdateIsIdempotentForTheSplit(t *testing.T) {
	r := newConvergeRig(t, v7NodeWithAccounts())
	r.appendConfig(t, crossedPoolInstallation+crossedPoolCrafts)
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	written := parseLocalConfigBytes(t, readFile(t, installationConfigPath()))
	once, err := written.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if notes := migrateCraftKeys(written); len(notes) != 0 {
		t.Errorf("the config fugaro update wrote is not converged: %v", notes)
	}
	twice, err := written.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// The bytes and not a field of the block: a second pass that derived
	// `mixed-claude-2` beside the half it already wrote would leave the pools
	// reading alike to a test that compared members.
	if !bytes.Equal(once, twice) {
		t.Errorf("a second pass over the file fugaro update wrote changes it:\n%s", lineDiff(string(once), string(twice)))
	}
}

// v7NodeWithAccounts is v7Node carrying the account-choice members this
// fixture's local config states, so the policy stage's publish is a publish of
// the verifier list alone: an account key the node does not carry is a widening
// of what every run of the project may reach, and publishing one needs a typed
// confirmation at a real terminal (policyAccountWidening), which is the one
// thing a headless test cannot give.
func v7NodeWithAccounts() map[string]any {
	node := v7Node()
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
