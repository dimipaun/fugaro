package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
)

// settings_refusal_test.go is W3's half of the write path
// (docs/config-ownership/implementation-plan.md §3, item 6 of the owner's
// direction): the refusal a key meets when the generic path may not write it
// names the writer and the command line that works, from the writer table
// (docs/contracts/config-ownership.md §3) rather than from a sentence about a
// family. What the four tests here pin is the four halves of the acceptance:
// the line for a key a writer answers for, the family sentence for one no
// writer answers for, the rule that no refusal sends the reader to `fugaro
// update` as a step, and the allowlist's admission of the rows R1/D2603 handed
// this path.

// TestRefusalNamesTheWorkingCommandLine is item 6's pin: a key whose writer is
// registered is refused with that writer's own argv, the key filled in and the
// value word left for the caller, and with the layer spellings the key has
// beside it. The key is named once, at the head, and the sentence still says
// which command writes it — the read's own pair (D1178) — so a person reading
// the refusal learns both what to run and where the value lands.
func TestRefusalNamesTheWorkingCommandLine(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want []string
	}{
		// A family's own row: the `models` family's key is refused with the
		// command that writes it and its whole line. The line names the project
		// layer, because a model is the project's choice and no repository
		// states one any more (D3237, S4).
		{
			key:  "agent.model",
			want: []string{"agent.model is written by fugaro models use", "run `fugaro models use agent.model <value> --layer project`"},
		},
		// A row no family of the write's own map carries: the budget's caps are
		// T1's writers and the allowlist refuses them, so the sentence is the
		// one settingsWriteRowHinted's own allowlist branch prints.
		{
			key:  "caps.global.per_day_usd",
			want: []string{"written by fugaro budget set", "run `fugaro budget set --global --daily <value>`"},
		},
		// A line that carries a word ArgvFor cannot fill: the category is the
		// caller's, and it is printed as written rather than dropped.
		{
			key:  "layer.security.always_level_3",
			want: []string{"written by fugaro rigor security", "run `fugaro rigor security add --always-level-3 --layer project`"},
		},
	} {
		_, err := settingsWriteRow(tc.key)
		if err == nil {
			t.Errorf("%s: the generic path writes it, and this test is about the refusal", tc.key)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal does not carry %q: %v", tc.key, want, err)
			}
		}
	}

	// The layer spellings that work: a key with more than one of the three
	// write layers says so, and one with a single layer needs no second word —
	// the argv's own `--layer` is that spelling.
	_, err := settingsWriteRow("agent.model")
	if err != nil && !strings.Contains(err.Error(), "project, repo") {
		t.Errorf("agent.model is set at the project layer and at a repository's own file, and the refusal names neither: %v", err)
	}
	if _, err := settingsWriteRow("binaries.allow"); err == nil {
		t.Fatal("binaries.allow: the generic path writes it, and the family refusal is what this test reads")
	}
}

// TestRefusalForAKeyWithoutAWriterKeepsTheFamilySentence: the writer table is
// the source of the line, and where it answers nothing the family's sentence
// stays exactly as it is — the write says so in the family's own words rather
// than printing a line no command runs.
//
// The cascade block's six rows were this test's own list until K1 registered
// `fugaro accounts cascade set` for them (D2613), which is the day the family
// sentence stopped being their answer: what is left are rows the `accounts`
// family still owns with no command behind them — the craft's access at the
// agent layer's own spelling, whose model half is `models`'s, and the alias
// record, which no command of this release writes.
func TestRefusalForAKeyWithoutAWriterKeepsTheFamilySentence(t *testing.T) {
	for _, key := range []string{
		"agent.crafts.coder.access",
		"layer.defaults.agent.crafts.review.access",
		"claude_account_aliases",
	} {
		if w, ok := settings.WriterOf(key); ok && w.Label == "" && len(w.Argv) > 0 {
			t.Fatalf("%s: a writer answers for it (`%s`), and this test is about the rows none answers for", key, strings.Join(w.ArgvFor(key), " "))
		}
		_, err := settingsWriteRow(key)
		if err == nil {
			t.Errorf("%s: the generic path writes it, and its owner is the accounts family's", key)
			continue
		}
		if !strings.Contains(err.Error(), "is written by "+settingsFamilyCommands["accounts"]) {
			t.Errorf("%s: the refusal is %v, and a key no writer answers for keeps the family sentence", key, err)
		}
		if strings.Contains(err.Error(), "run `") {
			t.Errorf("%s: the refusal prints a command line for a key no writer answers for: %v", key, err)
		}
	}
}

// TestRefusalNeverSendsTheUserToUpdateAsAStep is the sentence's own fence
// (item 6, the plugin lint's rule): a refusal may name `fugaro update` or
// `fugaro setup` as the fix for a stale installation and never as the step that
// writes a key, because the words "run `fugaro update`" in a sentence about a
// setting are a manual step no release may ask of the user (the update rule of
// the repository's own CLAUDE.md; plugin/skills_lint_test.go's ownerViolation).
//
// The walk is the whole registry: every row is probed at its concrete spelling
// — the spelling a person types — and each refusal that comes back is read.
// What the test pins is that the newly printed line did not open the door the
// family sentences kept shut, so it also asserts the walk reached both kinds of
// sentence and that a labelled row's refusal is the allowlist's and not a
// command line at all.
func TestRefusalNeverSendsTheUserToUpdateAsAStep(t *testing.T) {
	refusals, writerLines := 0, 0
	for _, row := range settings.Rows() {
		key := writerProbeKey(row.Key)
		if strings.Contains(key, "*") {
			continue
		}
		_, err := settingsWriteRow(key)
		if err == nil {
			continue
		}
		refusals++
		sentence := err.Error()
		if strings.Contains(sentence, "run `fugaro ") {
			writerLines++
		}
		for _, step := range []string{"fugaro update", "fugaro setup"} {
			if strings.Contains(sentence, step) {
				t.Errorf("%s: the refusal names %q, and neither is ever the step that writes a key: %v", key, step, sentence)
			}
		}
	}
	if refusals < 20 || writerLines == 0 {
		t.Errorf("the walk read %d refusals and %d of them name a command line, and the regression this test exists for is in the ones that do", refusals, writerLines)
	}

	// A labelled row is the case the sentence is a label about rather than a
	// command: `region` is `fugaro setup`'s own bookkeeping (T5's plumbing
	// label), so its refusal is the allowlist's and prints no line, and the
	// installation command is named as what wrote the value and never as what a
	// person runs to change it.
	_, err := settingsWriteRow("region")
	if err == nil {
		t.Fatal("region: the generic path writes it, and this test reads its refusal")
	}
	if strings.Contains(err.Error(), "run `fugaro ") {
		t.Errorf("a labelled row is refused with a command line: %v", err)
	}
	if w, ok := settings.WriterOf("region"); !ok || w.Label == "" {
		t.Errorf("region: the writer table answers %v, and this test is about a labelled row", w)
	}
}

// TestSettingsGenericAllowlistAdmitsTheScalarRows is the acceptance's second
// half (D2603): the rows R1 gave the owner `settings set` are rows this path
// writes, so the allowlist names them and the write reaches them — a row whose
// owner column says the generic writer and which the write refuses is the pair
// F13 forbids, and R1's three wildcard rows were exactly that until W3 named
// them (`workflows.*.needs` and `workflows.*.ab.reviewer_arm` as the repository
// rows they are, and `mobile_allow` as the block whose entries the write takes
// whole).
//
// The test reads the write's own answer for each row and then runs two of them
// for real, because a name in the allowlist is a claim about a command and not
// about a list: the phone a person declares lands in the machine's own config,
// and a repository's `needs` lands in its checkout's `fugaro.yaml`.
func TestSettingsGenericAllowlistAdmitsTheScalarRows(t *testing.T) {
	for _, key := range []string{
		"orchestrator.max_running",
		"orchestrator.batch_size",
		"orchestrator.spawn_per_minute",
		"orchestrator.spawn_per_hour",
		"orchestrator.spawn_per_day",
		"orchestrator.decision_model_per_class",
		"issues.nudge_after_days",
		"issues.nudge_recurrence",
		"triage",
		"verifier_accounts",
		"layer.jobs.reports.paused",
		"layer.jobs.catalog_check.paused",
		"planning.decoupling",
		"workflows.web.needs",
		"workflows.web.ab.reviewer_arm",
	} {
		row, err := settingsWriteRow(key)
		if err != nil {
			t.Errorf("%s: the write refuses a row R1 gave this path (D2603): %v", key, err)
			continue
		}
		if row.Owner != settingsOwnerDefaultToken {
			t.Errorf("%s: owner = %q, want the generic writer's own token", key, row.Owner)
		}
	}

	// The block the entries are declared in: the three `mobile_allow.*` rows
	// carry a `*`, which is no key a write lands on, so it is the block the
	// allowlist names and the entry pair reaches (D1162, D1175).
	if row, err := settingsEntryRow("mobile_allow"); err != nil {
		t.Errorf("mobile_allow: the entry write does not reach the block R1 gave this path: %v", err)
	} else if row.Owner != settingsOwnerDefaultToken {
		t.Errorf("mobile_allow: owner = %q, want the generic writer's own token", row.Owner)
	}

	// `workflows.*.needs` is a list, and the writer table's line for it is the
	// `--add` spelling: a scalar word would put one tag where the file reads a
	// list, which is the loader's refusal and not a write — so the row's own
	// measurement (T1's conformance test) is a write rather than a value domain
	// the ladder cannot reach.
	if w, ok := settings.WriterOf("workflows.web.needs"); !ok || !strings.Contains(strings.Join(w.Argv, " "), "--add") {
		t.Errorf("workflows.web.needs: the writer table's argv is %v, want the --add spelling a list takes", w.Argv)
	}

	_, localPath := settingsWriteHome(t)
	repoPath := settingsWriteCheckout(t, settingsRepoYAML(""))

	if out, _, err := execute(t, "settings", "set", "mobile_allow",
		"--add-entry", "uid=m~7f3a2b9c", "--add-entry", "address=dimi@example.com", "--add-entry", "role=admin",
		"--layer", "local"); err != nil {
		t.Fatalf("the phone R1 handed this path was not declared: %v\n%s", err, out)
	}
	if got := readFileString(t, localPath); !strings.Contains(got, "m~7f3a2b9c") || !strings.Contains(got, "mobile_allow") {
		t.Errorf("the write left the allowlist out of the machine's own config:\n%s", got)
	}

	if out, _, err := execute(t, "settings", "set", "workflows.web.needs", "--add", "tools", "--layer", "repo"); err != nil {
		t.Fatalf("the repository's own `needs` was not written: %v\n%s", err, out)
	}
	if got := readFileString(t, repoPath); !strings.Contains(got, "tools") {
		t.Errorf("the write left `needs` out of the checkout's fugaro.yaml:\n%s", got)
	}
}
