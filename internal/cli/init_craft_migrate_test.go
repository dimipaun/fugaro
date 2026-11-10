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
// default for every stage, and a senior review that runs on the other.
//
// The values are this fixture's own and never the spec's example: the spec's
// "Coder = DeepSeek through OpenRouter, Senior review = Opus through Claude
// OAuth" describes one installation, and a migration that wrote it into this
// one would be inventing a configuration nobody chose (reconciliation F17).
const previousInstallation = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: {}
  team-b: {}
default_claude_account: team-a
stages:
  review_first: { claude_account: team-b }
`

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
}

// The three keys the new vocabulary cannot carry are left alone and named, and
// no craft key is written that would mean something other than the key it came
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
  team-a: {}
default_claude_account: default
`,
			want:  []string{"default"},
			notes: []string{"default_claude_account"},
		},
		{
			name: "an account narrowed by allow_data_to",
			yaml: `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: {}
  team-b: { allow_data_to: [acme/app] }
default_claude_account: team-b
`,
			want:  []string{"team-b"},
			notes: []string{"team-b", "allow_data_to"},
		},
		{
			name: "one craft, two accounts",
			yaml: `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
claude_accounts:
  team-a: {}
  team-b: {}
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
  team-a: {}
  team-b: {}
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
			// A credential entry carries a name and a type and no grant, so
			// deriving one from a narrowed account would drop allow_data_to:
			// the entry is not written and no craft names the account.
			got := resolveCrafts(t, lc)
			for _, name := range tc.want {
				if holdsCredential(lc, name) {
					t.Errorf("%s became a credential although the new vocabulary cannot carry it: %+v", name, lc.Credentials)
				}
				for craftName, eff := range got {
					if eff.Access == name {
						t.Errorf("craft %s names %s, which no credential of this release stands for", craftName, name)
					}
				}
			}
		})
	}
}

// TestUpdateConvergesPreviousInstallation: an installation at the previous
// release with default_claude_account set reaches the craft vocabulary with
// `fugaro update` alone, and doctor is clean afterwards (the HARD RULE, F17).
func TestUpdateConvergesPreviousInstallation(t *testing.T) {
	r := newConvergeRig(t, v4NodeWithAccounts())
	r.appendConfig(t, "claude_accounts:\n  team-a: {}\n  team-b: {}\n"+
		"default_claude_account: team-a\nstages:\n  review_first: { claude_account: team-b }\n")
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
	r.appendConfig(t, "claude_accounts:\n  team-a: {}\n  team-b: {}\n"+
		"default_claude_account: team-a\nstages:\n  review_first: { claude_account: team-b }\n")
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

func holdsCredential(lc *localcfg.Config, name string) bool {
	for _, cred := range lc.Credentials {
		if cred.Name == name {
			return true
		}
	}
	return false
}
