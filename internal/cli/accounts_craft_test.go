package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file is A2's own half: the three grants only a person may write — a
// craft's access, an escalation grade's access and a repository's own
// claude_accounts — and the two removal records beside them
// (docs/config-ownership/implementation-plan.md, A2; D2613, D2617, F22).

// accountsCraftExtra is the fixture the three commands are read off: two
// accounts with the credentials behind them, a craft that names one of them, and
// one repository that states its provider — the entry a repository's own
// accounts list lives in (docs/project-claude-accounts.md §3). The craft carries
// a model as well, so a write that should have left it alone has something to
// leave alone.
const accountsCraftExtra = `
claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
  team-b:
    allow_data_to:
      - owner/repo-b
credentials:
  - name: team-a
    type: claude-oauth
  - name: team-b
    type: claude-oauth
crafts:
  coder:
    model: deepseek-v4-1
    access: team-a
repos:
  owner/repo-a:
    provider: github
`

// accountsCraftHome is that fixture: project aurora in a config home of its own,
// the caller's own YAML appended, and no coding-agent marker set — the session
// these tests run in is not the one TestAccessWideningRefusedInAnAgentSession is
// about.
func accountsCraftHome(t *testing.T, extra string) string {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	settingsFixture(t, accountsCraftExtra+extra)
	return os.Getenv("FUGARO_CONFIG")
}

// accountsCraftAnchor is the header a layer file this fixture's project accepts:
// `--layer project` holds the document to the anchors the project config
// carries, which are writeProject's own.
const accountsCraftAnchor = "version: 1\nproject: aurora\ngcp_project: aurora-gcp\n"

// accountsCraftLayerFile is this machine's project layer file carrying the
// model_grades block a --layer project grade write is aimed at: the grade's own
// model is what makes the document one the write may change (config.ValidateModelGrades).
const accountsCraftLayerFile = accountsCraftAnchor + `defaults:
  model_grades:
    strong:
      model: claude-sonnet-5-5
`

// accountsCraftLocal is the fixture's config as the loader reads it, so a test
// asserts on what the file means and not only on its text.
func accountsCraftLocal(t *testing.T, path string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("%s does not load after the write: %v", path, err)
	}
	return lc
}

// TestCraftAccessWritesTheKey is the plan's red step: `fugaro accounts craft set
// NAME --access X` writes crafts.<NAME>.access — the key no command of this
// release could write before it, which the generic settings path refuses as a
// family-owned row (settings_set.go) and which every run of the installation
// resolves its craft's calls against. The craft's model and the rest of the file
// are the write's to leave alone.
func TestCraftAccessWritesTheKey(t *testing.T) {
	path := accountsCraftHome(t, "")

	out, _, err := execute(t, "accounts", "craft", "set", "coder", "--access", "team-b")
	if err != nil {
		t.Fatalf("accounts craft set: %v\n%s", err, out)
	}
	lc := accountsCraftLocal(t, path)
	if got := lc.Crafts.Of(config.CraftCoder).Access; got != "team-b" {
		t.Errorf("crafts.coder.access = %q, want the credential the command named", got)
	}
	if got := lc.Crafts.Of(config.CraftCoder).Model; got != "deepseek-v4-1" {
		t.Errorf("crafts.coder.model = %q, want the model half left alone: this command writes the access and nothing else", got)
	}
	if !strings.Contains(out, "crafts.coder.access = team-b") {
		t.Errorf("the sentence does not name the key it wrote:\n%s", out)
	}
	// The other craft of the fixture is untouched, and the write is a widening
	// one: what the owner reads is what it grants, not a confirmation.
	if !strings.Contains(out, "every coder stage of a run of this installation sends its calls out on it") {
		t.Errorf("a widening write does not say what it widens:\n%s", out)
	}
	if !strings.Contains(out, path) {
		t.Errorf("the write does not name the file it landed in (%s):\n%s", path, out)
	}
}

// TestGradeAccessWritesTheKey is the same acceptance for the escalation ladder:
// `fugaro accounts grade set strong --access X` writes
// model_grades.strong.access in the repository's own fugaro.yaml, which is the
// layer the key's registry row defaults to — and the write lands in the working
// tree and nowhere else, because what makes a repository's change live is the
// merge of the owner's own branch (D1564).
func TestGradeAccessWritesTheKey(t *testing.T) {
	modelsWriteHomeWith(t, "")
	path := modelsWriteGitCheckout(t, "owner/repo-a",
		settingsRepoYAML("model_grades:\n  strong:\n    model: claude-sonnet-5-5\n"))

	out, _, err := execute(t, "accounts", "grade", "set", "strong", "--access", "vertex")
	if err != nil {
		t.Fatalf("accounts grade set: %v\n%s", err, out)
	}
	file := readFileString(t, path)
	if !strings.Contains(file, "  strong:\n    model: claude-sonnet-5-5\n    access: vertex\n") {
		t.Errorf("the repository's file does not carry the access it was asked for:\n%s", file)
	}
	if !strings.Contains(out, path) || !strings.Contains(out, "model_grades.strong.access = vertex") {
		t.Errorf("the sentence does not name the file and the key it wrote:\n%s", out)
	}
	// A repository's own file is the owner's to commit: the command says so, and
	// it makes no commit of its own.
	if !strings.Contains(out, "the change is in this file's working tree and nowhere else") {
		t.Errorf("the write does not say it is in a working tree and nowhere else:\n%s", out)
	}
	if _, err := config.Parse([]byte(file)); err != nil {
		t.Errorf("the file this command wrote is one its own reader refuses: %v", err)
	}
}

// TestRepoClaudeAccountsAdd is the third grant: `fugaro accounts repo add REPO
// ACCOUNT` adds the account to repos.<REPO>.claude_accounts, the list a
// repository's own runs are served by for the repositories no profile's job
// serves (docs/project-claude-accounts.md §3).
func TestRepoClaudeAccountsAdd(t *testing.T) {
	path := accountsCraftHome(t, "")

	out, _, err := execute(t, "accounts", "repo", "add", "owner/repo-a", "team-a")
	if err != nil {
		t.Fatalf("accounts repo add: %v\n%s", err, out)
	}
	lc := accountsCraftLocal(t, path)
	if got := lc.Repos["owner/repo-a"].ClaudeAccounts; len(got) != 1 || got[0] != "team-a" {
		t.Errorf("repos.owner/repo-a.claude_accounts = %v, want the account the command named", got)
	}
	if !strings.Contains(out, "repos.owner/repo-a.claude_accounts gains it") {
		t.Errorf("the sentence does not name the list it wrote:\n%s", out)
	}
	// The write is a widening one and the entry is provider-qualified: what the
	// command prints says what the repository's runs may now spend.
	if !strings.Contains(out, "admits runs of that repository to an account's token") {
		t.Errorf("a widening write does not say what it widens:\n%s", out)
	}
	// The list is additive, and a second run for the account already there
	// changes nothing.
	before := readFileString(t, path)
	if out, _, err := execute(t, "accounts", "repo", "add", "owner/repo-a", "team-a"); err != nil {
		t.Fatalf("accounts repo add for the account already listed: %v\n%s", err, out)
	} else if !strings.Contains(out, "already names team-a") {
		t.Errorf("the second run does not say the list already names the account:\n%s", out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the second run rewrote the file:\n%s", got)
	}
	// The axis's own non-account values are admitted without a declaration, the
	// way config.ValidClaudeAccountRef treats them: a project's Anthropic key and
	// its provider key name no account this config could declare, so the refusal
	// for an undeclared name — which sends the owner to `fugaro accounts add` —
	// would name a declaration no command of this release can write for them.
	for _, account := range []string{config.AnthropicKeyAccount, config.ProviderKeyAccount} {
		out, _, err := execute(t, "accounts", "repo", "add", "owner/repo-a", account)
		if err != nil {
			t.Fatalf("accounts repo add %s: %v\n%s", account, err, out)
		}
		if got := accountsCraftLocal(t, path).Repos["owner/repo-a"].ClaudeAccounts; !slices.Contains(got, account) {
			t.Errorf("repos.owner/repo-a.claude_accounts = %v, want %s added: the axis's own values are not accounts the config declares", got, account)
		}
	}
}

// TestCraftAccessRefusesAnUnknownCredentialNamingTheKnown is the refusal the
// command adds to the block's own: a name no layer of this installation defines
// is a grant that resolves to no credential — every run of the installation
// would be refused at its next call rather than at this write — so the write is
// refused before the file is touched and what it prints is what the installation
// does define, so the person has something to type instead.
func TestCraftAccessRefusesAnUnknownCredentialNamingTheKnown(t *testing.T) {
	path := accountsCraftHome(t, "")
	before := readFileString(t, path)

	_, _, err := execute(t, "accounts", "craft", "set", "coder", "--access", "team-c")
	if err == nil {
		t.Fatal("a craft was pointed at a credential this installation defines nowhere")
	}
	for _, want := range []string{"team-c", "names no credential and no pool this installation defines", "team-a", "team-b", config.CredentialVertex} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}

	// The grade and the repository take the same rule, from the same sentence.
	if _, _, err := execute(t, "accounts", "grade", "set", "strong", "--access", "team-c", "--layer", "project"); err == nil {
		t.Error("a grade was pointed at a credential this installation defines nowhere")
	}
	if _, _, err := execute(t, "accounts", "repo", "add", "owner/repo-a", "team-c"); err == nil {
		t.Error("a repository was admitted to an account this installation defines nowhere")
	}
}

// TestAccessWithoutTheFlagNamesWhereToLook: --access is the whole of what
// `craft set` and `grade set` write, so an invocation that carries none is
// refused by the flag's own sentence — and that sentence says where the names are
// read (the accounts listing, the pools listing, the built-in vertex) rather than
// claiming this installation defines none. A sentence built from no config at all
// said exactly that beside a file that declares two credentials, which sends the
// owner looking for a lost `credentials:` block instead of at the flag they left
// out. Nothing is written either way.
func TestAccessWithoutTheFlagNamesWhereToLook(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"craft set", []string{"accounts", "craft", "set", "coder"}},
		{"grade set", []string{"accounts", "grade", "set", "strong"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsCraftHome(t, "")
			before := readFileString(t, path)

			_, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatal("a write ran with no --access at all")
			}
			for _, want := range []string{"--access", "accounts ls", "accounts pool ls", config.CredentialVertex} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "defines no credential") {
				t.Errorf("the refusal says this installation defines nothing, which is false for this fixture: %v", err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestGradeAccessRefusesAGradeWithNoModel: a grade states the model its rung
// runs as well as the credential its calls go out on, and one written with an
// access and no model is a document no reader loads
// (config.ValidateModelGrades). The refusal names the key that is missing rather
// than letting the parse error arrive at the owner's next command.
func TestGradeAccessRefusesAGradeWithNoModel(t *testing.T) {
	modelsWriteHomeWith(t, "")
	path := modelsWriteGitCheckout(t, "owner/repo-a", settingsRepoYAML(""))
	before := readFileString(t, path)

	_, _, err := execute(t, "accounts", "grade", "set", "strong", "--access", "vertex")
	if err == nil {
		t.Fatal("a grade was written with an access and no model")
	}
	for _, want := range []string{"model_grades.strong.model", "states no", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestAccountsCraftRmRecordsTheRemoval: a craft taken away is not a craft never
// declared (F22, D2607). The published node keeps its own copy of the crafts
// block, so silence in this file would leave the node serving the craft the owner
// has just removed — the record beside it is the only statement that says the
// name was taken away, and a craft declared again clears it on the same bytes.
func TestAccountsCraftRmRecordsTheRemoval(t *testing.T) {
	path := accountsCraftHome(t, "")

	out, _, err := execute(t, "accounts", "craft", "rm", "coder")
	if err != nil {
		t.Fatalf("accounts craft rm: %v\n%s", err, out)
	}
	lc := accountsCraftLocal(t, path)
	if got := lc.Crafts.Of(config.CraftCoder); got != (config.Craft{}) {
		t.Errorf("crafts.coder = %+v after the removal, want the block gone", got)
	}
	removals := removedNames(lc)
	if len(removals.Crafts) != 1 || removals.Crafts[0] != "coder" {
		t.Errorf("node_removals.crafts = %v, want the craft recorded as removed: silence keeps the node's own copy, so a removal with no record leaves the craft served", removals.Crafts)
	}
	if !strings.Contains(out, "record it as removed, so this write's own publish takes it off the node") {
		t.Errorf("the sentence does not name the record it wrote:\n%s", out)
	}

	// Declared again: the record goes with the declaration, or the next publish
	// would take off the node the craft the owner has just pointed somewhere.
	if _, _, err := execute(t, "accounts", "craft", "set", "coder", "--access", "team-a"); err != nil {
		t.Fatalf("accounts craft set after a removal: %v", err)
	}
	after := readFileString(t, path)
	if lc := accountsCraftLocal(t, path); len(removedNames(lc).Crafts) != 0 {
		t.Errorf("the craft is declared and still recorded as removed:\n%s", after)
	}
	if strings.Contains(after, "node_removals") {
		t.Errorf("the record was left behind holding nothing:\n%s", after)
	}
}

// TestCraftWritesReachTheNode is the sentence both craft commands end on, read
// where it can be checked: the crafts block is served to a run off the published
// node and nowhere else, so `craft set` owes the publish that carries the access
// there and `craft rm` owes the one that takes the craft off it. Nothing in
// either command publishes by hand — the registry row of crafts.<name>.access is
// a `local` one, a `local` write is the policy stage's (settings.layerSteps), and
// the after-write hook runs the stages the write owes before it returns
// (contract §3, D2602; write_publish.go). Without that pair the sentences would
// be an overstatement and the node would go on serving the craft the owner had
// just taken away.
func TestCraftWritesReachTheNode(t *testing.T) {
	r := newAddRigWith(t, addRigLocal+`
credentials:
  - name: team-a
    type: claude-oauth
  - name: team-b
    type: claude-oauth
crafts:
  coder:
    model: deepseek-v4-1
    access: team-a
`)

	out, _, err := execute(t, "accounts", "craft", "set", "coder", "--access", "team-b")
	if err != nil {
		t.Fatalf("accounts craft set: %v\n%s", err, out)
	}
	if got := publishedCraft(t, r, config.CraftCoder).Access; got != "team-b" {
		t.Errorf("the published node carries crafts.coder.access = %q as this command returns, want team-b: every coder stage of a run of this installation resolves its calls against the node", got)
	}

	out, _, err = execute(t, "accounts", "craft", "rm", "coder")
	if err != nil {
		t.Fatalf("accounts craft rm: %v\n%s", err, out)
	}
	if got := publishedCraft(t, r, config.CraftCoder); got != (config.Craft{}) {
		t.Errorf("the published node still carries crafts.coder = %+v as this command returns, want it gone: the record the removal wrote is what the publish acts on", got)
	}
}

// publishedCraft is the craft the published node states, the zero block for a
// craft the node does not carry.
func publishedCraft(t *testing.T, r *addRig, name string) config.Craft {
	t.Helper()
	p := r.policy(t)
	if p.Crafts == nil {
		return config.Craft{}
	}
	return p.Crafts.Of(name)
}

// TestCredentialRmWritesTheExplicitUnset is F22's storage half for the craft
// side: `accounts rm` takes the typed entry away, and with the node keeping its
// own copy of craft_credentials and of the crafts block, the removal has to
// record both — the credential, and every craft whose whole block went with the
// access that named it. A craft that keeps a model of its own is not removed and
// is not recorded: half of it is still the owner's.
//
// The record is read off the bytes the removal writes rather than off the file
// the command leaves, because the command's own follow-up spends it: the publish
// that makes the removal live reconciles the node and takes a name off the record
// once the node no longer holds it (clearReconciledRemovals, D2607) — a record
// left standing would make every publish after this one delete a name the node
// does not have.
func TestCredentialRmWritesTheExplicitUnset(t *testing.T) {
	t.Run("a craft that keeps its model", func(t *testing.T) {
		r := newAddRigWith(t, accountsRmTypedCraftLocal)

		plan, next, _, err := accountsRmResolve(r.cfg, r.addLocal(t), "team-a", "", []byte(readFileString(t, r.cfg)))
		if err != nil {
			t.Fatalf("the removal refused a fixture it is meant to make: %v", err)
		}
		if !plan.typed {
			t.Fatalf("plan.typed = false, so this fixture exercises no removal record at all: the entry is not one accounts add --type could have declared")
		}

		lc, err := localcfg.Parse(next)
		if err != nil {
			t.Fatalf("the bytes this removal writes do not read back: %v", err)
		}
		removals := removedNames(lc)
		if !slices.Equal(removals.CraftCredentials, []string{"team-a"}) {
			t.Errorf("node_removals.craft_credentials = %v, want the credential recorded: the node keeps its own copy, so a removal with no record leaves the entry served and spent", removals.CraftCredentials)
		}
		if len(removals.Crafts) != 0 {
			t.Errorf("node_removals.crafts = %v, want nothing recorded: crafts.coder keeps its model, so its block is still the owner's", removals.Crafts)
		}
		if what := accountsRmWhat(plan); !strings.Contains(what, "record the removal in node_removals") {
			t.Errorf("the sentence does not name the record it wrote: %s", what)
		}
	})

	t.Run("a craft that was only that access", func(t *testing.T) {
		r := newAddRigWith(t, accountsRmCraftOnlyAccessLocal)

		plan, next, _, err := accountsRmResolve(r.cfg, r.addLocal(t), "team-a", "", []byte(readFileString(t, r.cfg)))
		if err != nil {
			t.Fatalf("the removal refused a fixture it is meant to make: %v", err)
		}

		lc, err := localcfg.Parse(next)
		if err != nil {
			t.Fatalf("the bytes this removal writes do not read back: %v", err)
		}
		removals := removedNames(lc)
		if !slices.Equal(removals.CraftCredentials, []string{"team-a"}) {
			t.Errorf("node_removals.craft_credentials = %v, want the credential recorded", removals.CraftCredentials)
		}
		if !slices.Equal(removals.Crafts, []string{config.CraftCoder}) {
			t.Errorf("node_removals.crafts = %v, want crafts.coder recorded: its whole block went with the access, and the node would go on serving it with the credential this run removed", removals.Crafts)
		}
		if what := accountsRmWhat(plan); !strings.Contains(what, "record crafts coder as removed with it") {
			t.Errorf("the sentence does not name the craft it recorded: %s", what)
		}
	})
}

// accountsRmCraftOnlyAccessLocal is a craft whose only leaf is the access that
// names the typed entry: the state in which the removal takes the block away
// whole rather than clearing one leaf of it.
const accountsRmCraftOnlyAccessLocal = addRigLocal + `
claude_accounts:
  team-a:
    allow_data_to:
      - owner/repo-a
credentials:
  - name: team-a
    type: claude-oauth
crafts:
  coder:
    access: team-a
`

// TestAccountsCraftReadsNoStdin is the no-prompt policy's own pin (Fugaro asks
// for nothing): every leaf of the three families runs with stdin at EOF — the
// write end of a pipe closed before the run — and returns, writing what it was
// asked for. Nothing on the way waits for an answer a closed stdin could never
// give, and none of the three has a confirmation step standing in for one.
func TestAccountsCraftReadsNoStdin(t *testing.T) {
	t.Run("craft set", func(t *testing.T) {
		path := accountsCraftHome(t, "")
		out, _, err := accountsPoolClosedStdin(t, "accounts", "craft", "set", "coder", "--access", "team-b")
		if err != nil {
			t.Fatalf("accounts craft set with stdin closed: %v\n%s", err, out)
		}
		if got := accountsCraftLocal(t, path).Crafts.Of(config.CraftCoder).Access; got != "team-b" {
			t.Errorf("crafts.coder.access = %q after the run, want the write to have landed with stdin closed", got)
		}
	})

	t.Run("craft rm", func(t *testing.T) {
		path := accountsCraftHome(t, "")
		out, _, err := accountsPoolClosedStdin(t, "accounts", "craft", "rm", "coder")
		if err != nil {
			t.Fatalf("accounts craft rm with stdin closed: %v\n%s", err, out)
		}
		if got := accountsCraftLocal(t, path).Crafts.Of(config.CraftCoder); got != (config.Craft{}) {
			t.Errorf("crafts.coder = %+v after the run, want the removal to have landed with stdin closed", got)
		}
	})

	t.Run("grade set", func(t *testing.T) {
		path := accountsCraftHome(t, "")
		layer := settingsManagedLayerFile(t, accountsCraftLayerFile)
		before := readFileString(t, path)
		out, _, err := accountsPoolClosedStdin(t, "accounts", "grade", "set", "strong", "--access", "team-b", "--layer", "project")
		if err != nil {
			t.Fatalf("accounts grade set with stdin closed: %v\n%s", err, out)
		}
		after := readFileString(t, layer)
		if !strings.Contains(after, "    strong:\n      model: claude-sonnet-5-5\n      access: team-b\n") {
			t.Errorf("the layer file does not carry the access it was asked for:\n%s", after)
		}
		if got := readFileString(t, path); got != before {
			t.Errorf("--layer project wrote the local config instead of the layer file:\n%s", got)
		}
	})

	t.Run("repo add", func(t *testing.T) {
		path := accountsCraftHome(t, "")
		out, _, err := accountsPoolClosedStdin(t, "accounts", "repo", "add", "owner/repo-a", "team-b")
		if err != nil {
			t.Fatalf("accounts repo add with stdin closed: %v\n%s", err, out)
		}
		if got := accountsCraftLocal(t, path).Repos["owner/repo-a"].ClaudeAccounts; len(got) != 1 || got[0] != "team-b" {
			t.Errorf("repos.owner/repo-a.claude_accounts = %v after the run, want the write to have landed with stdin closed", got)
		}
	})
}

// TestAccessWideningRefusedInAnAgentSession is D2617's answer, and it is the one
// fence these writes carry that no gate can lift: an access decides whose token
// a stage or a repository's runs may spend, so a coding agent's session writes
// none of the three. Each command is run in a session a marker names and inside a
// repository, which is the shape the fence is read in: a local or a layer
// document is refused there on the marker alone, whatever the edit says, while
// the repository's own file is refused by the edit's own Typed flag — the grade's
// run is the repository document and is therefore this file's mutation check
// (settings_write.go's `!e.insideRepo() || e.Typed`): drop Typed from that edit
// and the write lands. For the craft and the repository's list, whose documents
// are the local config, dropping Typed would change only the sentence printed,
// not the refusal — the flag is pinned for them by the two sentences
// TypedWhy puts on their output instead (the widening line each run prints).
func TestAccessWideningRefusedInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			t.Run("craft set", func(t *testing.T) {
				path := accountsCraftHome(t, "")
				before := readFileString(t, path)
				t.Setenv(marker, "1")

				out, _, err := execute(t, "accounts", "craft", "set", "coder", "--access", "team-b")
				assertAccessWriteRefused(t, err, out, marker, path, before,
					"fugaro accounts craft set coder --access team-b")
			})

			t.Run("grade set", func(t *testing.T) {
				modelsWriteHomeWith(t, "")
				path := modelsWriteGitCheckout(t, "owner/repo-a",
					settingsRepoYAML("model_grades:\n  strong:\n    model: claude-sonnet-5-5\n"))
				before := readFileString(t, path)
				t.Setenv(marker, "1")

				out, _, err := execute(t, "accounts", "grade", "set", "strong", "--access", "vertex")
				assertAccessWriteRefused(t, err, out, marker, path, before,
					"fugaro accounts grade set strong --access vertex")
			})

			t.Run("repo add", func(t *testing.T) {
				path := accountsCraftHome(t, "")
				before := readFileString(t, path)
				t.Setenv(marker, "1")

				out, _, err := execute(t, "accounts", "repo", "add", "owner/repo-a", "team-a")
				assertAccessWriteRefused(t, err, out, marker, path, before,
					"fugaro accounts repo add owner/repo-a team-a")
			})
		})
	}
}

// assertAccessWriteRefused is the three grants' one refusal read the same way:
// the run failed as a user error, the file is byte-identical, the refusal names
// the marker that makes the session one and prints the command to run at the
// owner's own terminal, and the diff of what was refused is on the command's own
// output.
func assertAccessWriteRefused(t *testing.T, err error, out, marker, path, before, command string) {
	t.Helper()
	if err == nil {
		t.Fatalf("a widening write landed in a coding agent's session:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), marker) {
		t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
	}
	if !strings.Contains(err.Error(), command) {
		t.Errorf("the refusal does not print the command to run at a terminal (%s): %v", command, err)
	}
	if !strings.Contains(out, path) || !(strings.Contains(out, "  + ") || strings.Contains(out, "  - ")) {
		t.Errorf("the refusal does not print the diff of what it refused:\n%s", out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the file changed in a coding agent's session:\n%s", got)
	}
}

// TestCraftWritesRefuseAForeignLayerNamingTheRight is the resolver's own refusal
// (T3, contract §6) on this family: a craft's access and a repository's own
// accounts list are the installation's own config and have no other layer, and a
// grade is a repository's or the project layer's and has neither local nor a
// choice. Each command refuses the layer its key does not have by naming the ones
// it does, and the file is left alone.
func TestCraftWritesRefuseAForeignLayerNamingTheRight(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		layer string
		has   []string
	}{
		{"craft set", []string{"accounts", "craft", "set", "coder", "--access", "team-b"}, "project", []string{"local"}},
		{"craft rm", []string{"accounts", "craft", "rm", "coder"}, "repo", []string{"local"}},
		{"repo add", []string{"accounts", "repo", "add", "owner/repo-a", "team-a"}, "project", []string{"local"}},
		{"grade local", []string{"accounts", "grade", "set", "strong", "--access", "vertex", "--layer", "local"}, "local", []string{"project", "repo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsCraftHome(t, "")
			before := readFileString(t, path)

			_, _, err := execute(t, append(tc.args, "--layer", tc.layer)...)
			if err == nil {
				t.Fatalf("--layer %s wrote a key that has no such layer", tc.layer)
			}
			for _, want := range append([]string{"--layer " + tc.layer}, tc.has...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestAccountsCraftWritersRegisterTheirKeys is the registration's own half
// (contract §2, §4): every leaf these commands write is registered under its own
// pattern, with the argv that changes exactly that leaf, the store it lands in
// and whether the node serves the key besides it. The layer flag is in the two
// grade templates because a grade has two layers and the argv is the sentence a
// refusal prints for either of them; a craft and a repository's list carry no
// flag because they have the one layer and no choice to state.
func TestAccountsCraftWritersRegisterTheirKeys(t *testing.T) {
	for _, tc := range []struct {
		key     string
		command string
		store   settings.Store
		node    bool
		argv    string
	}{
		{"crafts.coder.access", "fugaro accounts craft set", settings.StoreLocal, true, "accounts craft set coder --access <value>"},
		{"node_removals.crafts", "fugaro accounts craft rm", settings.StoreLocal, true, "accounts craft rm <value>"},
		{"node_removals.craft_credentials", "fugaro accounts rm", settings.StoreLocal, true, "accounts rm <value>"},
		{"model_grades.strong.access", "fugaro accounts grade set", settings.StoreRepo, false, "accounts grade set strong --access <value> --layer repo"},
		{"layer.defaults.model_grades.strongest.access", "fugaro accounts grade set", settings.StoreProject, false, "accounts grade set strongest --access <value> --layer project"},
		{"repos.owner/repo-a.claude_accounts", "fugaro accounts repo add", settings.StoreLocal, true, "accounts repo add owner/repo-a <value>"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			w, ok := settings.WriterOf(tc.key)
			if !ok {
				t.Fatalf("%s: no registered writer, so a refusal for it can print no command", tc.key)
			}
			if w.Command != tc.command {
				t.Errorf("Command = %q, want the family that writes it (%q)", w.Command, tc.command)
			}
			if w.Store != tc.store {
				t.Errorf("Store = %q, want %q", w.Store, tc.store)
			}
			if w.Node != tc.node {
				t.Errorf("Node = %v, want %v", w.Node, tc.node)
			}
			if got := strings.Join(w.ArgvFor(tc.key), " "); got != tc.argv {
				t.Errorf("ArgvFor(%s) = %q, want %q", tc.key, got, tc.argv)
			}
		})
	}

	// A craft's access, a repository's own list and the two removal records are
	// served from the node, and each row states it: the crafts block and the
	// records that change it are what the node carries, and a repository's
	// admission is served out of the `accounts` block the publish derives from
	// that list — so a hook reading the store alone, which is `local` for all of
	// them, would run no stage and the write would reach no run (D2615).
	for _, key := range []string{"crafts.coder.access", "repos.owner/repo-a.claude_accounts", "node_removals.crafts", "node_removals.craft_credentials"} {
		w, ok := settings.WriterOf(key)
		if !ok || !w.Node {
			t.Errorf("%s: %+v (found %v), want a writer that states the node serves it (D2615)", key, w, ok)
		}
	}
	// The two grade rows state none, and not for the crafts' reason: the node's
	// model_grades block is published from the local config's own model_grades
	// carrier, which no command of this release writes, while these two write the
	// project layer file and a checkout's own file — and each of those reaches
	// runs by the stage its own store names (the layer stage, or the merge).
	for _, key := range []string{"model_grades.strong.access", "layer.defaults.model_grades.strong.access"} {
		w, ok := settings.WriterOf(key)
		if !ok || w.Node {
			t.Errorf("%s: %+v (found %v), want no node stated beside that store", key, w, ok)
		}
	}
}
