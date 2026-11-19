package cli

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// settings_layer_default_test.go is W1 of docs/config-ownership: the layer a
// write lands in when the command line names none (D2605), and the spelling the
// project layer accepts for the repository keys it holds under `defaults:`
// (D2606). Both are one resolver's rules, and both are read by every writer
// that goes through settingsResolveTarget — so what these tests pin is the
// resolver's answer and the command that lands on it, not a second table.

// TestSettingsDefaultLayerByMergeRule: with no --layer the target follows the
// key's merge rule (D2605). A Tighten key is a ceiling, and a ceiling written
// into a repository's own file is one the repository it bounds can lower, which
// is the opposite of what a ceiling is for: it lands in the project layer when
// the key has one, and in this machine's own config when the project layer is
// not a layer the key may be set in. Every other merge rule keeps the narrowest
// layer the key may be set in, so a Replace key keeps the repo default it had.
//
// The rule is asserted over every row of the registry — a new row is classified
// by this test the day it lands — and then through the command itself, because
// a rule the resolver holds and the write path does not read would be a rule
// about nothing.
func TestSettingsDefaultLayerByMergeRule(t *testing.T) {
	// want is the rule in the test's own words, computed from the row and not
	// read back from settingsDefaultLayer: a test that asked the function under
	// test what it should answer would agree with it about anything.
	want := func(row settings.Row) string {
		if row.Merge == settings.Tighten {
			for _, l := range []string{settingsLayerProject, settingsLayerLocal} {
				if slices.Contains(row.Layers, l) {
					return l
				}
			}
		}
		for _, l := range []string{settingsLayerRepo, settingsLayerProject, settingsLayerLocal} {
			if slices.Contains(row.Layers, l) {
				return l
			}
		}
		return ""
	}
	tightenToProject, replaceToRepo, tightenToLocal := 0, 0, 0
	for _, row := range settings.Rows() {
		got := settingsDefaultLayer(row)
		if got != want(row) {
			t.Errorf("%s (merge %v, layers %v): the default layer is %q, want %q", row.Key, row.Merge, row.Layers, got, want(row))
		}
		switch {
		case row.Merge == settings.Tighten && got == settingsLayerProject:
			tightenToProject++
		case row.Merge == settings.Tighten && got == settingsLayerLocal:
			tightenToLocal++
		case row.Merge != settings.Tighten && got == settingsLayerRepo:
			replaceToRepo++
		}
	}
	if tightenToProject == 0 || replaceToRepo == 0 {
		t.Fatalf("the registry has %d ceiling rows defaulting to the project layer and %d other rows defaulting to the repo: the rule above is not the one the registry is written for, and the probe checked nothing", tightenToProject, replaceToRepo)
	}
	if tightenToLocal == 0 {
		t.Errorf("no ceiling row defaults to this machine's own config, and a ceiling whose layers hold no project layer is the case that rule is for")
	}

	// The wiring: the three keys the rule separates, written with no --layer.
	_, localPath := settingsWriteHome(t)
	layerPath := settingsWriteLayerFile(t, "")
	repoPath := settingsWriteCheckout(t, settingsRepoYAML(""))
	noTerminal(t)

	// A ceiling with a project layer: the project layer's file.
	if out, _, err := execute(t, "settings", "set", "governance.max_subagents", "4"); err != nil {
		t.Fatalf("a ceiling with no --layer was refused: %v\n%s", err, out)
	}
	if got := readFileString(t, layerPath); !strings.Contains(got, "max_subagents: 4") {
		t.Errorf("the ceiling did not land in the project layer:\n%s", got)
	}
	if got := readFileString(t, repoPath); strings.Contains(got, "max_subagents") {
		t.Errorf("the ceiling landed in the repository's own file:\n%s", got)
	}

	// A ceiling with no project layer: this machine's own config.
	if out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "5"); err != nil {
		t.Fatalf("a ceiling whose layers hold no project layer was refused: %v\n%s", err, out)
	}
	if got := readFileString(t, localPath); !strings.Contains(got, "per_run_usd: 5") {
		t.Errorf("the ceiling did not land in this machine's own config:\n%s", got)
	}

	// A Replace key: the repo default it always had.
	if out, _, err := execute(t, "settings", "set", "agent.review_rounds", "6"); err != nil {
		t.Fatalf("a Replace key with no --layer was refused: %v\n%s", err, out)
	}
	if got := readFileString(t, repoPath); !strings.Contains(got, "review_rounds: 6") {
		t.Errorf("the Replace key did not land in the repository's own file:\n%s", got)
	}
	if got := readFileString(t, layerPath); strings.Contains(got, "review_rounds") {
		t.Errorf("the Replace key landed in the project layer, which is the ceiling's own target:\n%s", got)
	}
}

// TestSettingsAgentKeyProjectSpelling: the 17 `agent.*`/`git.*` rows are
// repository keys the project layer holds under `defaults:`, and the short
// spelling a person types (`agent.max_budget_usd`) names them without saying
// so. D2606 maps it in the one resolver rather than dropping the layer from
// those rows, so `agent.x --layer project` and `layer.defaults.agent.x --layer
// project` are one write: the same bytes, and a file the layer's own loader
// reads back.
//
// The red step is the short spelling: before W1 the resolver wrote the key at
// the file's top level, where ParseProjectLayer refuses it — "agent: is not a
// project layer key" — so the write was refused after the edit was built and
// nothing landed.
func TestSettingsAgentKeyProjectSpelling(t *testing.T) {
	settingsWriteHome(t)
	layerPath := settingsWriteLayerFile(t, "")
	noTerminal(t)
	seed := readFileString(t, layerPath)

	if out, _, err := execute(t, "settings", "set", "agent.max_budget_usd", "20", "--layer", "project"); err != nil {
		t.Fatalf("the short spelling of a project-layer agent key was refused: %v\n%s", err, out)
	}
	short := readFileString(t, layerPath)

	// The same write by the long spelling, on the same file: the two spellings
	// are one edit, so the bytes are the ones to compare.
	if err := os.WriteFile(layerPath, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, err := execute(t, "settings", "set", "layer.defaults.agent.max_budget_usd", "20", "--layer", "project"); err != nil {
		t.Fatalf("the long spelling of the same key was refused: %v\n%s", err, out)
	}
	if long := readFileString(t, layerPath); long != short {
		t.Errorf("the two spellings wrote different documents:\nshort:\n%s\nlong:\n%s", short, long)
	}

	// The document reads back, and the key it reads back to is the one the
	// write named: a file the layer's loader refuses is the failure the short
	// spelling used to be, and a file that reads back holding the key somewhere
	// else would be the same failure with no refusal at all.
	l, ps := config.ParseProjectLayer([]byte(short), config.LayerAnchor{Project: "aurora", GCPProject: "aurora-gcp"})
	if len(ps) > 0 {
		t.Fatalf("the layer file the write produced does not read back: %s\n%s", ps[0].String(), short)
	}
	if l.Defaults.Agent.MaxBudgetUSD != 20 {
		t.Errorf("defaults.agent.max_budget_usd reads back as %v, want 20:\n%s", l.Defaults.Agent.MaxBudgetUSD, short)
	}

	// The fence in front of that key reads the old value at the mapped path
	// too, so the two spellings classify one edit alike: the file states 20 and
	// 22 is a raise, which is a fact only a read at defaults.agent can reach —
	// a read at the file's top level finds no value, falls back to the built-in
	// 25, and calls 22 a tightening.
	for _, key := range []string{"agent.max_budget_usd", "layer.defaults.agent.max_budget_usd"} {
		doc := settingsDryRunDoc(t, "settings", "set", key, "22", "--layer", "project")
		if !doc.Widening {
			t.Errorf("%s: the dry run says a raise from 20 to 22 is no widening (old %q, why %q): the old value was not read at defaults.agent.max_budget_usd", key, doc.Old, doc.Why)
		}
	}
}

// TestSettingsWrongLayerRefusesNamingTheRight: a --layer the key may not be set
// in refuses and names the layers it does have, so a reader is told where the
// key goes rather than left to guess. The default layer is the same question
// asked the other way — settingsDefaultLayer may only answer a layer the row
// has, or a write with no --layer would land where the same check refuses it.
func TestSettingsWrongLayerRefusesNamingTheRight(t *testing.T) {
	settingsWriteHome(t)
	layerPath := settingsWriteLayerFile(t, "")
	repoPath := settingsWriteCheckout(t, settingsRepoYAML(""))
	noTerminal(t)

	for _, c := range []struct {
		key, layer string
		want       []string
	}{
		// A ceiling the project layer may set, asked for in this machine's own
		// config: the two files that would hold it are named and this one is
		// not.
		{"governance.max_subagents", settingsLayerLocal, []string{"project", "repo"}},
		// A key whose only store is the layer file, asked for in the
		// repository.
		{"layer.default_profile", settingsLayerRepo, []string{"project"}},
		// A ceiling of a repository's own file, asked for in the project
		// layer: the short spelling does not widen the key's layers either.
		{"budget.per_run_usd", settingsLayerProject, []string{"repo", "local"}},
	} {
		before := readFileString(t, layerPath) + readFileString(t, repoPath)
		out, _, err := execute(t, "settings", "set", c.key, "4", "--layer", c.layer)
		if err == nil {
			t.Fatalf("%s at --layer %s was written:\n%s", c.key, c.layer, out)
		}
		if !strings.Contains(err.Error(), c.key) || !strings.Contains(err.Error(), "--layer "+c.layer) {
			t.Errorf("%s at --layer %s: the refusal names neither the key nor the layer: %v", c.key, c.layer, err)
		}
		for _, want := range c.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s at --layer %s: the refusal does not name %s, a layer the key has: %v", c.key, c.layer, want, err)
			}
		}
		if after := readFileString(t, layerPath) + readFileString(t, repoPath); after != before {
			t.Errorf("%s at --layer %s changed a file under a refusal", c.key, c.layer)
		}
	}

	// And the other direction, over every row: the default is "" or a layer the
	// row has, so a write that names no --layer is never refused by the check
	// above for a reason the default invented.
	for _, row := range settings.Rows() {
		if layer := settingsDefaultLayer(row); layer != "" && !slices.Contains(row.Layers, layer) {
			t.Errorf("%s defaults to --layer %s, and its own layers are %v", row.Key, layer, row.Layers)
		}
	}
}

// TestSettingsRowsNeverWriteWhereTheLayerRefusesAtReread: the ratchet under the
// mapping. Every registry row whose layers include the project layer resolves
// to a path in the layer file, and a path the layer's own loader refuses at
// re-read is a key the command would refuse AFTER building the edit — the shape
// the short `agent.` spelling had before W1. The resolver is asked for the path
// and the loader is asked whether it takes it, so a row added to the registry
// whose block is missing from settingsLayerDefaultsBlocks fails here rather
// than in a person's terminal.
//
// What is filtered out is the value: the probe writes one word into every row's
// path and most rows do not hold a word, so the loader's complaints about the
// value it found are not complaints about where the key is. The three messages
// left are the loader's own answers about the key's PLACE — a top-level name the
// layer file does not know, a `defaults:` member that is not a fugaro.yaml key,
// and a path that lands on a block of keys rather than one of them — and they
// are the failure W1's mapping is written against.
//
// The fourth answer about a key's place is the scope table's ("may only be set
// in: ..."), and it is compared against the list below rather than refused
// outright. The five rows on it are a drift W1 neither caused nor may repair:
// the registry lists `layer.defaults.agent.crafts.*.access` as writable at the
// project layer, config.Scopes holds every craft's access to InRepo on purpose
// (whyCredentialAccess: only the repository's own, PR-reviewed file chooses a
// credential or pool), and the two have disagreed since the rows landed —
// `layer.`-prefixed rows, so W1's mapping does not move them. The comparison
// is two-sided on purpose: a sixth row that lands with the same drift fails
// here, and so does one of the five the day the registry is repaired, which is
// the day this list is emptied.
var settingsScopeRefusedRows = []string{
	"layer.defaults.agent.crafts.coder.access",
	"layer.defaults.agent.crafts.merge-judge.access",
	"layer.defaults.agent.crafts.review.access",
	"layer.defaults.agent.crafts.security-review.access",
	"layer.defaults.agent.crafts.senior-review.access",
}

func TestSettingsRowsNeverWriteWhereTheLayerRefusesAtReread(t *testing.T) {
	settingsWriteHome(t)
	layerPath := settingsWriteLayerFile(t, "")
	seed := readFileString(t, layerPath)
	sel, lc, err := settingsSelection()
	if err != nil {
		t.Fatal(err)
	}
	anchor := config.LayerAnchor{Project: lc.Name, GCPProject: lc.GCPProject}
	refuses := []string{
		"is not a project layer key (",
		"is not a fugaro.yaml key the project layer knows",
		// A path that lands where a fugaro.yaml block belongs, in a file that
		// states a scalar there: a row whose key is the block rather than a key
		// inside it writes what the layer refuses, exactly as the other two do.
		"is a block of fugaro.yaml keys, so it must be a mapping of them",
	}
	const scopeRefusal = "may only be set in:"
	checked, mapped := 0, 0
	var scopeRefused []string
	for _, row := range settings.Rows() {
		if !slices.Contains(row.Layers, settingsLayerProject) {
			continue
		}
		target, err := settingsResolveTarget(context.Background(), row, settingsWriteOptions{layer: settingsLayerProject}, lc, sel)
		if err != nil {
			t.Errorf("%s: --layer project does not resolve: %v", row.Key, err)
			continue
		}
		if target.kind != settingsDocLayer || target.path != layerPath {
			t.Errorf("%s: --layer project resolves to %v at %s, want the layer file %s", row.Key, target.kind, target.path, layerPath)
			continue
		}
		if slices.Contains(target.keyPath, "*") {
			// A wildcard row is a pattern the file fills in per name, and the
			// probe has no name to write: the writes that land one are the
			// concrete key and the entry pair, each covered by its own tests.
			continue
		}
		data, err := yamledit.SetScalar([]byte(seed), target.keyPath, "probe")
		if err != nil {
			t.Errorf("%s: the probe cannot be written at %v: %v", row.Key, target.keyPath, err)
			continue
		}
		_, ps := config.ParseProjectLayer(data, anchor)
		for _, p := range ps {
			if strings.Contains(p.Message, scopeRefusal) {
				scopeRefused = append(scopeRefused, row.Key)
				continue
			}
			for _, refusal := range refuses {
				if strings.Contains(p.Message, refusal) {
					t.Errorf("%s lands at %s, and the layer file refuses it there: %s", row.Key, strings.Join(target.keyPath, "."), p.String())
				}
			}
		}
		checked++
		if len(target.keyPath) > 1 && target.keyPath[0] == "defaults" && !strings.HasPrefix(row.Key, "layer.") {
			mapped++
		}
	}
	if checked == 0 {
		t.Fatal("no row of the registry resolves into the project layer: the probe checked nothing")
	}
	if mapped == 0 {
		t.Errorf("no row of the registry reaches defaults. through the short spelling, and the mapping above is what this test exists for")
	}
	slices.Sort(scopeRefused)
	if !slices.Equal(scopeRefused, settingsScopeRefusedRows) {
		t.Errorf("the rows the scope table refuses at the project layer are\n%v\nand the drift W1 found is\n%v\na row that joined or left the list is a registry change this test is the ratchet for", scopeRefused, settingsScopeRefusedRows)
	}
}

// TestAgentCeilingRaiseThroughTheProjectSpellingIsRefused: the fence in front of
// the short spelling. `agent.max_budget_usd --layer project` is the project
// layer's own `defaults.agent.max_budget_usd` — the cap every repository the
// layer templates runs under — so raising it in a coding agent's session is the
// typed widening D1026 reserves for the project's own hand, exactly as raising
// the repository's own copy is.
//
// The value the fence compares against is read at the MAPPED path: the layer
// file states 5, the write asks for 20, and 20 is under the built-in default
// (25) the fence falls back to when no file states a value. So the refusal can
// only be the typed one if the old value was read at defaults.agent — a
// resolver that read the key at the file's top level would find no value, take
// the built-in 25 for its base, and admit the raise as a write that tightens.
func TestAgentCeilingRaiseThroughTheProjectSpellingIsRefused(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsManagedLayerFile(t, minimalAnchored+"defaults:\n  agent:\n    max_budget_usd: 5\n")
	before := readFileString(t, path)
	agentSession(t)

	out, _, err := execute(t, "settings", "set", "agent.max_budget_usd", "20", "--layer", "project")
	if err == nil {
		t.Fatalf("a ceiling was raised through the project layer's spelling in a coding agent's session:\n%s", out)
	}
	if !errors.Is(err, managedmode.ErrNotConverged) {
		t.Fatalf("the raise was not refused by the gate, so nothing says which control refused it: %v", err)
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed class, so the old value was not read at the mapped defaults.agent path: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the file changed under a refused raise")
	}
	recs := r.audits(t)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("the refusal left %+v, want exactly one refused record", recs)
	}
	if !slices.ContainsFunc(recs[0].Rules, func(rule string) bool { return strings.Contains(rule, "typed confirmation") }) {
		t.Errorf("the refused record's rules (%v) do not name the typed class", recs[0].Rules)
	}
}
