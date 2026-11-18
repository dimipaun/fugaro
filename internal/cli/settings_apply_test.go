package cli

// settings_apply_test.go is AP5's own half of the composite design
// (docs/composite-commands/implementation-plan.md, the section "AP5:
// `settings set` / `unset` run their chain"): a settings write runs the
// follow-up steps its key declares, says so when none is owed — a checkout's
// file above all, whose merge is the only thing that makes it live — and a cap
// the chain would publish is never written from a coding agent's session.
//
// The four tests the plan names are here: the chain runs the key's own steps
// and no others (TestSettingsSetPublishesOnlyWhatTheKeyNeeds), a repo-layer
// write publishes nothing (TestRepoLayerWriteNeverPublishes), every key of the
// registry declares its steps (TestEverySettingKeyDeclaresItsSteps), and a cap
// write the chain would publish is refused in an agent's session with and
// without --no-apply (TestCapKeysRefusedInAnAgentSessionEvenWithNoApply,
// D1576). TestSettingsSetNoApplySavesAndSaysSo is the `--no-apply` half of the
// same rule over the same fixture (rule 3, D1565).
//
// Each test names the installation it needs — the after-write hook's own
// fixture for the chain, the light write fixture for the two claims that stop
// before a stage — and every one of them is hermetic: nothing here reaches a
// cloud.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsApplySet runs one `settings set` whose whole command line the case
// names, and returns the command's answer and its error.
func settingsApplySet(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := execute(t, append([]string{"settings", "set"}, args...)...)
	return out, err
}

// settingsApplyPending is what this machine owes, read the way `fugaro apply`
// reads it: the pending document of the project the test has selected (D1562),
// which is the one the write is bound to.
func settingsApplyPending(t *testing.T) []string {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := apply.LoadPending(os.Getenv, lc.Name, lc.GCPProject)
	if err != nil {
		t.Fatal(err)
	}
	return pending.Names()
}

// TestSettingsSetPublishesOnlyWhatTheKeyNeeds is the plan's first claim: the
// chain runs the steps the key's own declaration names and no others (D2615's
// two facts, the key and the store the write landed in).
//
// The installation is the after-write hook's own fixture — a converged
// installation whose node the policy stage can migrate — because the chain runs
// through that hook and through nothing else: a write that published by some
// second route would not be the rule this task is about.
//
// `budget.per_run_usd` at the local layer is a key the published node is
// derived from, so the write runs the policy stage and the node carries the
// file's ceiling afterwards. It is also exactly the cap D1576 says an owner's
// own session publishes, which is this half: no marker, and the write lands.
// `layer.governance.review_ratio` is a project-layer key, which owes the layer
// stage and not the policy one: the node carries nothing of it, and running
// that stage would republish a document this write did not change.
func TestSettingsSetPublishesOnlyWhatTheKeyNeeds(t *testing.T) {
	_, db := afterWriteFixture(t)

	out, err := settingsApplySet(t, "budget.per_run_usd", "3", "--layer", "local")
	if err != nil {
		t.Fatalf("settings set budget.per_run_usd --layer local: %v\n%s", err, out)
	}
	if !strings.Contains(out, "applied "+initflow.Policy) {
		t.Errorf("a key the node is derived from did not run the policy stage:\n%s", out)
	}
	if strings.Contains(out, "applied "+initflow.Layer) {
		t.Errorf("a key the project layer file does not carry ran the layer stage:\n%s", out)
	}
	// The stage ran for real: the node carries the file's ceiling now, which is
	// what makes the change live rather than merely saved.
	if got := ceilingOn(t, db); got != 3 {
		t.Errorf("the published node's ceiling = %v, want the file's 3: the chain's stage did not run", got)
	}
	// And nothing is left owed: the steps the run answered are cleared from the
	// record, so `fugaro apply` has nothing to do about them (D1563).
	if names := settingsApplyPending(t); len(names) != 0 {
		t.Errorf("a write that ran its steps left %v owed, want nothing", names)
	}

	settingsManagedLayerFile(t, minimalAnchored+"governance:\n  review_ratio: 3\n")
	out, err = settingsApplySet(t, "layer.governance.review_ratio", "2", "--layer", "project")
	if err != nil {
		t.Fatalf("settings set layer.governance.review_ratio --layer project: %v\n%s", err, out)
	}
	if !strings.Contains(out, "applied "+initflow.Layer) {
		t.Errorf("a project-layer key did not run the layer stage:\n%s", out)
	}
	if strings.Contains(out, "applied "+initflow.Policy) {
		t.Errorf("a key the node carries nothing for ran the policy stage:\n%s", out)
	}
}

// TestSettingsSetNoApplySavesAndSaysSo is the other half of rule 3: --no-apply
// runs none of the steps, the change is saved, and the command says which steps
// are still owed and which command runs them (D1563, D1565). What it leaves is
// the record `fugaro apply` reads, so the two commands cannot come to different
// answers about what this write still needs.
func TestSettingsSetNoApplySavesAndSaysSo(t *testing.T) {
	_, db := afterWriteFixture(t)
	before := ceilingOn(t, db)

	out, err := settingsApplySet(t, "budget.per_run_usd", "3", "--layer", "local", "--no-apply")
	if err != nil {
		t.Fatalf("settings set --no-apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "saved, not live yet: "+initflow.Policy+" still owed") {
		t.Errorf("--no-apply does not say what is still owed:\n%s", out)
	}
	if !strings.Contains(out, "fugaro apply") {
		t.Errorf("--no-apply does not name the command that finishes the change:\n%s", out)
	}
	if strings.Contains(out, "applied ") {
		t.Errorf("--no-apply ran a step:\n%s", out)
	}
	// The node is untouched: nothing published the file's ceiling, which is what
	// makes the sentence true rather than a figure of speech.
	if got := ceilingOn(t, db); got != before {
		t.Errorf("the published node's ceiling = %v after --no-apply, want the %v it was", got, before)
	}
	if names := settingsApplyPending(t); !slices.Equal(names, []string{initflow.Policy}) {
		t.Errorf("the record holds %v, want the policy stage the write owes", names)
	}
}

// TestRepoLayerWriteNeverPublishes is D1564: a write into a checkout's
// `fugaro.yaml` declares no step at all — whatever the key's own row says, and
// the two keys below are one whose row also serves the machine's own config and
// one whose only layer is the checkout's — and the command says so: that file
// is live once the change is merged, and no step of this machine's can make it
// live before then. Nothing is queued for `fugaro apply` either, because the
// sentence is "nothing is pending": an entry in the record would be a debt no
// step can pay.
func TestRepoLayerWriteNeverPublishes(t *testing.T) {
	settingsWriteHome(t)
	settingsWriteCheckout(t, settingsRepoYAML(""))

	for _, tc := range []struct {
		name string
		key  string
		args []string
	}{
		{
			// The same key at the local layer or the project layer owes a stage;
			// written into a checkout it owes none of them.
			name: "a key the machine's own config carries too",
			key:  "budget.per_run_usd",
			args: []string{"budget.per_run_usd", "9", "--layer", "repo"},
		},
		{
			// A row whose only layer is the checkout's, so its own declaration is
			// the empty one — and the write still runs nothing.
			name: "a key only a checkout's file carries",
			key:  "workflows.web.commands.test",
			args: []string{"workflows.web.commands.test", "make test", "--layer", "repo", "--executable-changes"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := apply.WriteSteps(apply.Change{Kind: apply.Setting, Key: tc.key}, settings.StoreRepo); len(got) != 0 {
				t.Fatalf("the steps of %s written in a checkout = %v, want none (D1564)", tc.key, got)
			}
			out, err := settingsApplySet(t, tc.args...)
			if err != nil {
				t.Fatalf("settings set %s --layer repo: %v\n%s", tc.key, err, out)
			}
			if !strings.Contains(out, "nothing is pending") || !strings.Contains(out, "live once the change is merged") {
				t.Errorf("a repo-layer write does not say it is live once merged:\n%s", out)
			}
			if strings.Contains(out, "applied ") {
				t.Errorf("a repo-layer write ran a step:\n%s", out)
			}
		})
	}

	if names := settingsApplyPending(t); len(names) != 0 {
		t.Errorf("a repo-layer write queued %v, want nothing (D1564's \"nothing is pending\")", names)
	}
}

// TestEverySettingKeyDeclaresItsSteps is the census the plan names: every key
// of the settings registry declares the steps a write of it owes, and the
// declaration is its own row's layers — a key added to the registry declares
// its steps by being added, which is what makes a second list unnecessary
// (D1561).
//
// It holds the three readings of one rule to each other. The vocabulary is
// closed: every layer a row may carry names a stage or explicitly names none.
// The registry's rows are the declaration: the table answers a key of the
// registry and never a gap. And the write path's own resolution agrees with it
// for every key both reach, which is the pair that would drift if the generic
// allowlist and this declaration answered differently about one key.
//
// A key nothing holds is the one answer that is not "declared, owes nothing":
// it is the gap, and the table still says so.
func TestEverySettingKeyDeclaresItsSteps(t *testing.T) {
	for _, layer := range settings.Layers {
		if _, ok := settings.LayerStage(layer); !ok {
			t.Fatalf("%s: the vocabulary holds a layer that names no stage and no explicit none, so a row carrying it would owe nothing in silence", layer)
		}
	}
	for _, row := range settings.Rows() {
		key := settingsConcreteKey(row.Key)
		steps, ok := settings.KeySteps(key)
		if !ok {
			t.Errorf("%s: the registry's own row declares no steps", key)
			continue
		}
		if want := settingsStagesOfLayers(row.Layers); !slices.Equal(steps, want) {
			t.Errorf("%s: declares %v, want the row's own layers' %v", key, steps, want)
		}
		if _, ok := apply.Steps(apply.Change{Kind: apply.Setting, Key: key}); !ok {
			t.Errorf("%s: the table answers a gap for a key the registry holds", key)
		}
		writeRow, _, found := settingsWriteLookup(key)
		if !found {
			// A row the generic path never writes (a sensitive key, a family's
			// own, the installation's plumbing) is written by its own command off
			// the same row: there is nothing here to compare it against.
			continue
		}
		if got := settingsStagesOfLayers(writeRow.Layers); !slices.Equal(got, steps) {
			t.Errorf("%s: the write path resolves it as %v and the declaration reads %v", key, got, steps)
		}
	}
	if _, ok := settings.KeySteps("no.such.key"); ok {
		t.Error("a key the registry does not hold declares steps, want the gap")
	}
	if _, ok := apply.Steps(apply.Change{Kind: apply.Setting, Key: "no.such.key"}); ok {
		t.Error("the table answers for a key the registry does not hold, want the gap")
	}
}

// settingsConcreteKey spells a registry pattern as the key a person writes, so
// a row that varies (`workflows.*.commands.test`) can be asked about at all.
func settingsConcreteKey(pattern string) string {
	return strings.ReplaceAll(pattern, "*", "web")
}

// settingsStagesOfLayers is the census's own reading of a row's layers: the
// stages the vocabulary gives them, in the row's order and deduped. It is
// written from settings.Layers and LayerStage rather than from KeySteps, so the
// comparison is between the registry's statement and the declaration read off
// it and not a value compared with itself.
func settingsStagesOfLayers(layers []string) []string {
	var out []string
	for _, layer := range layers {
		stage, ok := settings.LayerStage(layer)
		if !ok || stage == "" {
			continue
		}
		if !slices.Contains(out, stage) {
			out = append(out, stage)
		}
	}
	return out
}

// TestCapKeysRefusedInAnAgentSessionEvenWithNoApply is D1576, the fence F13
// asks for: in a coding agent's session a cap write the chain would publish is
// refused — with and without --no-apply, so a later owner `fugaro apply` never
// publishes a raise an agent queued — and nothing of it is left behind: no
// write, no queued step.
//
// Which control refuses is the installation's own, and each case names the one
// it expects, because the class is wider than any single fence and the sentence
// the session reads is what tells them apart:
//
//   - An unmanaged installation's session (`budget.per_run_usd` at the local
//     layer, `governance.review_ratio` at the project layer) has no marks to
//     read, so the chain's own fence answers with `budget set`'s refusal
//     (settingsChainCapFence, D1552/D1576) — or the project layer file's own
//     reserved-run sentence, which is asked first and refuses `governance.*`
//     whatever the registry says (settingsLayerReservedGate, D1175).
//   - A managed project's session reads the gate's refusal and carries its
//     record: today the allowlist and the reserved runs refuse every cap key
//     before the fence is reached, and the fence is the answer for the day the
//     gate stops refusing the class (D1545's audit-only gate, which is the state
//     F13 is about). The record is what tells the two apart here, and a test
//     that only read the error could never tell them.
//   - A cap whose ceiling a file states (`agent.max_budget_usd` in a checkout
//     that carries 10) is the widening fence's, as it was before this task: the
//     class is one behaviour and the fence above does not replace it.
//   - A cap whose only layer is a checkout's (`agent.max_run_tokens`): the class
//     stops there. No step of this machine's publishes it (D1564), so the write
//     lands with the sentence every repo-layer write ends on. The fence is not
//     wider than the rule it enforces.
func TestCapKeysRefusedInAnAgentSessionEvenWithNoApply(t *testing.T) {
	// ordinary, managed, projectLayer and checkout are the four installations
	// the cases need, each returning the file the write would land on and what
	// to read back about it afterwards.
	ordinary := func(t *testing.T) (string, func(*testing.T)) {
		t.Helper()
		_, localPath := settingsWriteHome(t)
		return localPath, nil
	}
	managed := func(t *testing.T) (string, func(*testing.T)) {
		t.Helper()
		r := newSettingsManagedRig(t, settingsMarksManaged)
		r.managedRunsBucket(t)
		return os.Getenv("FUGARO_CONFIG"), func(t *testing.T) {
			t.Helper()
			if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
				t.Errorf("the refused write filed %+v, want the one refused record the gate writes", recs)
			}
		}
	}
	projectLayer := func(t *testing.T) (string, func(*testing.T)) {
		t.Helper()
		settingsConvergeFixture(t)
		return settingsConvergeLayerFile(t), nil
	}
	checkout := func(t *testing.T) (string, func(*testing.T)) {
		t.Helper()
		settingsWriteHome(t)
		return settingsWriteCheckout(t, settingsRepoYAML("agent:\n  max_budget_usd: 10\n")), nil
	}

	for _, tc := range []struct {
		name  string
		key   string
		args  []string
		setup func(*testing.T) (string, func(*testing.T))
		// control is the sentence the session reads, and "" says the write is
		// admitted — the class does not reach it — and ends on the D1564
		// sentence instead.
		control string
		// lands says the write is admitted and the file carries it afterwards.
		lands bool
		// command says the refusal names the invocation the owner runs
		// themselves. The chain's own fence and the write path's do; the gate's
		// refusal and the layer document's are rules of those doors and name no
		// command (D1025, D1175).
		command bool
	}{
		{
			name:    "a cap nothing states, in an ordinary installation",
			key:     "budget.per_run_usd",
			args:    []string{"budget.per_run_usd", "500", "--layer", "local"},
			setup:   ordinary,
			control: "is the owner's own step",
			command: true,
		},
		{
			name:    "a cap nothing states, in a managed project's session",
			key:     "budget.per_run_usd",
			args:    []string{"budget.per_run_usd", "500", "--layer", "local"},
			setup:   managed,
			control: "is a key no coding agent's session writes",
		},
		{
			name:    "a cap nothing states at the project layer",
			key:     "governance.review_ratio",
			args:    []string{"governance.review_ratio", "20", "--layer", "project"},
			setup:   projectLayer,
			control: "is a key no coding agent's session writes",
		},
		{
			name:    "a cap the checkout's own file states",
			key:     "agent.max_budget_usd",
			args:    []string{"agent.max_budget_usd", "500", "--layer", "repo"},
			setup:   checkout,
			control: "refusing to write",
			command: true,
		},
		{
			name:  "a cap only a checkout carries, which no step publishes",
			key:   "agent.max_run_tokens",
			args:  []string{"agent.max_run_tokens", "400000", "--layer", "repo"},
			setup: checkout,
			lands: true,
		},
	} {
		for _, noApply := range []bool{false, true} {
			name := tc.name
			if noApply {
				name += ", with --no-apply"
			}
			t.Run(name, func(t *testing.T) {
				path, after := tc.setup(t)
				before := readFileString(t, path)
				agentSession(t)

				args := slices.Clone(tc.args)
				if noApply {
					args = append(args, "--no-apply")
				}
				out, err := settingsApplySet(t, args...)
				if tc.lands {
					if err != nil {
						t.Fatalf("a cap the chain publishes nothing for was refused in an agent's session: %v\n%s", err, out)
					}
					if !strings.Contains(out, "nothing is pending") {
						t.Errorf("the write does not say nothing can make it live:\n%s", out)
					}
					if after := readFileString(t, path); after == before {
						t.Errorf("the admitted write did not change %s:\n%s", path, after)
					}
					if names := settingsApplyPending(t); len(names) != 0 {
						t.Errorf("a repo-layer write queued %v, want nothing", names)
					}
					return
				}
				if err == nil {
					t.Fatalf("an agent's session wrote %s:\n%s", tc.key, out)
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
				}
				got := ErrorText(err)
				if !strings.Contains(got, tc.control) {
					t.Errorf("the refusal is not the control this case expects (%q): %q", tc.control, got)
				}
				if tc.command != strings.Contains(got, "fugaro settings set "+tc.key) {
					t.Errorf("the refusal names the owner's command: %v, want %v: %q", !tc.command, tc.command, got)
				}
				if after := readFileString(t, path); after != before {
					t.Errorf("the refused write changed %s:\n%s", path, after)
				}
				if names := settingsApplyPending(t); len(names) != 0 {
					t.Errorf("the refused write queued %v, want nothing", names)
				}
				if after != nil {
					after(t)
				}
			})
		}
	}
}
