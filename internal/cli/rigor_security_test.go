package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
)

// rigor_security_test.go is M4 of docs/config-ownership/implementation-plan.md:
// the security paths get their writer (D2613, D2617, D2612).
//
// A row of the list is addressed by its NAME and written whole — its glob and
// its category beside it — which is what D1163 asks of an object list and what
// separates this command from `fugaro rigor set` beside it, where one glob
// addresses one rule. The list is the one key whose two layers both spell it:
// `fugaro update`'s project layer file states it under its own `security:`
// block, a checkout's fugaro.yaml states the same rows by the bare key, and the
// layer file is what a write lands in when no --layer says otherwise. The
// project layer's `security.always_level_3` is the second half of the block and
// the layer file's alone.
//
// Three facts are this file's, one test each, and they are the plan's own tests:
//
//   - the two verbs really add and remove a row by its key field, in both files
//     the key has, and refuse the rows the loader would refuse before anything
//     is written (TestSecurityPathAddAndRemoveByName, the plan's red step);
//   - a removal is the lowering direction — `Typed`, so the owner's own terminal
//     writes it with the sentence saying what it lowers and no question in front
//     of it (D1552, D1540) — and the switch turned off is the same statement
//     about the whole project (TestSecurityPathRemovalIsAProjectLoosening);
//   - a coding agent's session is refused every removal whatever the gate says,
//     with the diff and the command to run at your own terminal
//     (TestSecurityPathRemovalRefusedInAnAgentSession, D2617).
//
// The world is the settings family's own (settingsWriteHome: project aurora,
// GCP project aurora-gcp, no marker set) with this machine's project layer file
// seeded by the test, plus a real git checkout of owner/repo-a for the repo
// layer.

// rigorRepoLayerYAML is a checkout's own file as these tests write it: the
// smallest document config.Parse accepts, carrying the project anchor a repo
// write is held to and no rigor block at all, which is the state a repository
// that never named a mode is in.
const rigorRepoLayerYAML = `version: 1
project: aurora
gcp_project: aurora-gcp
git:
  provider: github
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
`

// securityLayerSeedRows is the security block the two layer tests write into the
// project layer file: two rows with names no floor row spends (a floor name is
// refused by the loader, D1505) and the switch off, which is the state a
// project that has written its list and never asked for the whole-project level
// is in.
const securityLayerSeedRows = `security:
  patterns:
    - name: billing
      match: internal/billing/**
      category: credentials
    - name: payments
      match: internal/payments/**
      category: guards
`

// securityLayerFile seeds this machine's project layer file with the world's
// anchors and those rows, and answers its path.
func securityLayerFile(t *testing.T) string {
	t.Helper()
	return settingsManagedLayerFile(t, rigorAnchors+securityLayerSeedRows)
}

// securityLayerBlock is the layer file's own `security:` block, read with the
// loader the write path re-reads with (settingsReparse) rather than searched
// for in the text: a row written under `defaults:` — the layer's other spelling
// — would be no block at all to this reader, which is the reading that decides
// what a run of every repository of the project sees.
func securityLayerBlock(t *testing.T, path string) config.LayerSecurity {
	t.Helper()
	l, problems := config.ParseProjectLayer([]byte(readFileString(t, path)), rigorAnchor)
	if len(problems) > 0 {
		t.Fatalf("the layer file does not read after the write: %s", problems[0].String())
	}
	if l.Security == nil {
		return config.LayerSecurity{}
	}
	return *l.Security
}

// securityRepoBlock is a checkout's own `security:` block, read the way every
// run of that repository reads its file.
func securityRepoBlock(t *testing.T, path string) config.Security {
	t.Helper()
	cfg, problems := config.Parse([]byte(readFileString(t, path)))
	if len(problems) > 0 {
		t.Fatalf("the checkout's file does not read after the write: %s", problems[0].String())
	}
	if cfg.Security == nil {
		return config.Security{}
	}
	return *cfg.Security
}

// securityRowOf is the row a block holds under name, or nil.
func securityRowOf(rows []sensitivity.Pattern, name string) *sensitivity.Pattern {
	for i := range rows {
		if rows[i].Name == name {
			return &rows[i]
		}
	}
	return nil
}

// TestSecurityPathAddAndRemoveByName is the plan's red step and its acceptance
// (D2613, D1163): the two verbs write one row of the list by the name that is
// its identity in the union, at the layers the key has, and the document is one
// the loader of that layer reads back.
//
// The stages are the write's whole surface, in the order a person meets them:
// the key created from nothing, a row replaced rather than duplicated, the
// values the row's own rule refuses, a removal that takes the row away whole,
// the checkout's own file, and the switch. Every refusal is checked against the
// file as well as against the sentence, so a refused write that wrote anyway
// fails here.
func TestSecurityPathAddAndRemoveByName(t *testing.T) {
	settingsWriteHome(t)
	noAgentSession(t)
	noTerminal(t)
	// No security block at all: the first add is the write that creates the key,
	// and what it creates is the layer's own top-level block rather than a key
	// under `defaults:`.
	layerPath := settingsManagedLayerFile(t, rigorAnchors)

	out, _, err := execute(t, "rigor", "security", "add",
		"--name", "billing", "--match", "internal/billing/**", "--category", "credentials")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	block := securityLayerBlock(t, layerPath)
	if len(block.Patterns) != 1 {
		t.Fatalf("the layer file reads %d security paths after the add, want 1:\n%s", len(block.Patterns), readFileString(t, layerPath))
	}
	row := block.Patterns[0]
	if row.Name != "billing" || row.Match != "internal/billing/**" || row.Category != sensitivity.Credentials {
		t.Errorf("the row reads %+v, want billing on internal/billing/** under credentials", row)
	}
	if !strings.Contains(out, "add the security path billing to security.patterns in") {
		t.Errorf("the write does not name the row and the key it lands on:\n%s", out)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("a project layer write does not name the stage that publishes it:\n%s", out)
	}
	if strings.Contains(out, "lowers") {
		t.Errorf("adding a row reads as a lowering:\n%s", out)
	}

	// A second name, and the list keeps the order the rows were written in.
	if out, _, err = execute(t, "rigor", "security", "add",
		"--name", "payments", "--match", "internal/payments/**", "--category", "guards"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	block = securityLayerBlock(t, layerPath)
	if len(block.Patterns) != 2 || block.Patterns[0].Name != "billing" || block.Patterns[1].Name != "payments" {
		t.Errorf("the layer file reads %+v, want billing and then payments", block.Patterns)
	}

	// The name the list carries is corrected, never duplicated: the flag that is
	// given replaces that field and the flag that is absent keeps the row's own.
	out, _, err = execute(t, "rigor", "security", "add", "--name", "billing", "--match", "internal/billing/v2/**")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	block = securityLayerBlock(t, layerPath)
	if len(block.Patterns) != 2 {
		t.Fatalf("re-aiming a row left %d rows, want 2:\n%s", len(block.Patterns), readFileString(t, layerPath))
	}
	if got := securityRowOf(block.Patterns, "billing"); got == nil || got.Match != "internal/billing/v2/**" || got.Category != sensitivity.Credentials {
		t.Errorf("the row reads %+v, want the new glob with the category it had", got)
	}
	// The one lowering an add can be (D2617): the paths the row named before
	// leave the list. What each sentence lowers is
	// TestSecurityPathRemovalIsAProjectLoosening's, and the refusal a session
	// meets for it is the next test's.
	if !strings.Contains(out, "this lowers the project's security list by giving the security path billing another glob") {
		t.Errorf("an add that re-aims a row does not say what it lowers:\n%s", out)
	}

	// The row's own rule, before anything is written: a name the list does not
	// carry is written whole, so it takes both fields, and each field is held to
	// sensitivity.Pattern.Validate's own words rather than to a second copy of
	// them here. The file is compared byte for byte, because a refusal that wrote
	// anyway is the failure this half is for.
	before := readFileString(t, layerPath)
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{
			name: "a name the list does not carry, with neither field",
			argv: []string{"rigor", "security", "add", "--name", "brand-new"},
			want: "--match GLOB and --category CATEGORY",
		},
		{
			name: "a category outside the seven",
			argv: []string{"rigor", "security", "add", "--name", "brand-new", "--match", "internal/new/**", "--category", "nope"},
			want: sensitivity.CategoryList(),
		},
		{
			name: "a glob that is the whole repository",
			argv: []string{"rigor", "security", "add", "--name", "brand-new", "--match", "**", "--category", "guards"},
			want: "always_level_3",
		},
		{
			name: "a name outside the character set",
			argv: []string{"rigor", "security", "add", "--name", "a/b", "--match", "internal/new/**", "--category", "guards"},
			want: "a name must be",
		},
		{
			name: "the switch and a row in one call",
			argv: []string{"rigor", "security", "add", "--name", "brand-new", "--always-level-3"},
			want: "--always-level-3 is the project layer's switch and not a row",
		},
		{
			name: "neither the switch nor a row",
			argv: []string{"rigor", "security", "add"},
			want: "one write names one thing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := execute(t, tc.argv...)
			if err == nil {
				t.Fatalf("the write was accepted:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			assertNoPrompt(t, out)
			if got := readFileString(t, layerPath); got != before {
				t.Errorf("the refused write changed the layer file:\n%s", got)
			}
		})
	}

	// A removal takes the row away whole and leaves the others: the list is
	// addressed by name, so nothing else in it moves.
	out, _, err = execute(t, "rigor", "security", "rm", "--name", "billing")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	block = securityLayerBlock(t, layerPath)
	if len(block.Patterns) != 1 || block.Patterns[0].Name != "payments" {
		t.Errorf("the layer file reads %+v, want payments alone", block.Patterns)
	}
	// A name the list does not carry is not a failure, and nothing is written:
	// the answer is the state, said in one line.
	before = readFileString(t, layerPath)
	out, _, err = execute(t, "rigor", "security", "rm", "--name", "billing")
	if err != nil {
		t.Fatalf("removing a row the list does not carry was a failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "carries no security path named billing") {
		t.Errorf("the run does not say the list carries no such row:\n%s", out)
	}
	if got := readFileString(t, layerPath); got != before {
		t.Errorf("a removal that found nothing wrote anyway:\n%s", got)
	}
	// A removal takes no field of the row: the two flags are the add's, and cobra
	// refuses them rather than the command silently ignoring one.
	if _, _, err := execute(t, "rigor", "security", "rm", "--name", "payments", "--match", "internal/payments/**"); err == nil {
		t.Error("a removal accepted --match, which is a field of a row it takes away whole")
	} else if !strings.Contains(err.Error(), "unknown flag: --match") {
		t.Errorf("the refusal does not name the flag that does not belong: %v", err)
	}

	// The switch, which is the layer file's own (D1517): on, then off, then off
	// again as a state rather than a failure.
	out, _, err = execute(t, "rigor", "security", "add", "--always-level-3")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !securityLayerBlock(t, layerPath).AlwaysLevel3 {
		t.Errorf("the layer file's switch is off after `add --always-level-3`:\n%s", readFileString(t, layerPath))
	}
	if strings.Contains(out, "lowers") {
		t.Errorf("turning the switch on reads as a lowering:\n%s", out)
	}
	if out, _, err = execute(t, "rigor", "security", "rm", "--always-level-3"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if securityLayerBlock(t, layerPath).AlwaysLevel3 {
		t.Errorf("the layer file's switch is on after `rm --always-level-3`:\n%s", readFileString(t, layerPath))
	}
	before = readFileString(t, layerPath)
	out, _, err = execute(t, "rigor", "security", "rm", "--always-level-3")
	if err != nil {
		t.Fatalf("turning an unset switch off was a failure: %v\n%s", err, out)
	}
	if !strings.Contains(out, "security.always_level_3 is not set in it") {
		t.Errorf("the run does not say the layer carries no switch:\n%s", out)
	}
	if got := readFileString(t, layerPath); got != before {
		t.Errorf("a switch that was already off was written anyway:\n%s", got)
	}

	// The repository's own layer, which is the same list by its bare key in the
	// checkout's fugaro.yaml: written in the working tree and nowhere else
	// (D1564), and this machine's layer file is not touched.
	beforeLayer := readFileString(t, layerPath)
	repoPath := modelsWriteGitCheckout(t, "owner/repo-a", rigorRepoLayerYAML)
	dir := filepath.Dir(repoPath)
	head := modelsUseGit(t, dir, "rev-parse", "HEAD")
	out, _, err = execute(t, "rigor", "security", "add",
		"--name", "billing", "--match", "internal/billing/**", "--category", "credentials",
		"--layer", "repo", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	rows := securityRepoBlock(t, repoPath).Patterns
	if row := securityRowOf(rows, "billing"); row == nil || row.Match != "internal/billing/**" {
		t.Errorf("the checkout's file reads %+v, want billing on internal/billing/**:\n%s", rows, readFileString(t, repoPath))
	}
	if !strings.Contains(out, "owner/repo-a's working tree and nowhere else") {
		t.Errorf("the repository write does not say where the change is:\n%s", out)
	}
	if strings.Contains(out, "next: fugaro update") {
		t.Errorf("a repository's own file was answered with the layer's next step:\n%s", out)
	}
	if got := readFileString(t, layerPath); got != beforeLayer {
		t.Errorf("the --layer repo write reached this machine's project layer file:\n%s", got)
	}
	if got := modelsUseGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("the command committed: HEAD moved from %s to %s", head, got)
	}
	if changed := modelsUseGit(t, dir, "diff", "--name-only"); changed != "fugaro.yaml" {
		t.Errorf("the working tree's changed files are %q, want only fugaro.yaml", changed)
	}
	if out, _, err = execute(t, "rigor", "security", "rm", "--name", "billing", "--layer", "repo", "--repo", "owner/repo-a"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if rows = securityRepoBlock(t, repoPath).Patterns; len(rows) != 0 {
		t.Errorf("the checkout's file reads %+v after the removal, want no rows", rows)
	}

	// T3 for these keys: the list is settable at project and repo (config.Scopes)
	// and nowhere else, so a third layer is refused naming the two it has, and the
	// switch is refused at the repository because a repository's block is its rows
	// and nothing else (D1501).
	if _, _, err := execute(t, "rigor", "security", "add",
		"--name", "billing", "--match", "internal/billing/**", "--category", "credentials",
		"--layer", "local"); err == nil {
		t.Fatal("a --layer local write of a key with two layers was accepted")
	} else if !strings.Contains(err.Error(), "security.patterns may not be set at --layer local") ||
		!strings.Contains(err.Error(), "it may be set in project, repo") {
		t.Errorf("the refusal does not name the key and the layers it has: %v", err)
	}
	if _, _, err := execute(t, "rigor", "security", "add", "--always-level-3", "--layer", "repo", "--repo", "owner/repo-a"); err == nil {
		t.Fatal("a repository wrote the project's own switch")
	} else if !strings.Contains(err.Error(), "the project layer's alone") ||
		!strings.Contains(err.Error(), "not one repository's call") {
		t.Errorf("the refusal does not say why the switch is the layer's: %v", err)
	}
}

// TestSecurityPathRemovalIsAProjectLoosening is the plan's second test: a
// removal is the lowering direction, and the plan judges it as every other
// lowering is judged (D2617). `Typed` means three things here, and all three
// are checked: the widening is refused in a coding agent's session whatever the
// gate says (the next test walks the markers), the owner's own terminal writes
// it and asks NOTHING (D1552, D1540 — a Typed edit is never gate-admitted and
// no prompt stands in front of it), and the sentence naming what it lowers is
// printed beside the diff (D1555).
//
// The two halves of the security block are one answer each, and the switch is
// the whole project's: a row removed stops the escalation of the paths it
// names, and the switch turned off stops it for every change of every
// repository of the project.
func TestSecurityPathRemovalIsAProjectLoosening(t *testing.T) {
	settingsWriteHome(t)
	noAgentSession(t)
	noTerminal(t)
	layerPath := securityLayerFile(t)

	// The row: the sentence says what leaves the list and what that costs, and
	// the write lands with no question in front of it.
	out, _, err := execute(t, "rigor", "security", "rm", "--name", "billing")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, "this lowers the project's security list by removing the security path billing") {
		t.Errorf("the removal does not say what it lowers:\n%s", out)
	}
	if !strings.Contains(out, "stops being escalated to review level 3") {
		t.Errorf("the sentence does not say what the paths lose:\n%s", out)
	}
	if !strings.Contains(out, "every run of the project's branches") {
		t.Errorf("the sentence is not about this project's runs:\n%s", out)
	}
	if !strings.Contains(out, "remove the security path billing from security.patterns in") {
		t.Errorf("the run does not name the edit it makes:\n%s", out)
	}
	if rows := securityLayerBlock(t, layerPath).Patterns; len(rows) != 1 || rows[0].Name != "payments" {
		t.Errorf("the layer file reads %+v, want payments alone", rows)
	}

	// The switch off, which is the same statement about the whole project: with
	// it off, a change reaches review level 3 through the paths the list matches
	// and the per-run request, and by nothing else (D1503).
	if out, _, err = execute(t, "rigor", "security", "add", "--always-level-3"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, _, err = execute(t, "rigor", "security", "rm", "--always-level-3")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, "this lowers the project's review level by turning security.always_level_3 off") {
		t.Errorf("the switch does not say what it lowers:\n%s", out)
	}
	if !strings.Contains(out, "whatever paths it changed") {
		t.Errorf("the sentence is not about every change of the project:\n%s", out)
	}
	if securityLayerBlock(t, layerPath).AlwaysLevel3 {
		t.Errorf("the switch is still on after the lowering:\n%s", readFileString(t, layerPath))
	}

	// An add that creates a row lowers nothing, and the command says nothing
	// about lowering: the classification is the edit's and not the command's.
	out, _, err = execute(t, "rigor", "security", "add",
		"--name", "billing", "--match", "internal/billing/**", "--category", "credentials")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "lowers") {
		t.Errorf("a write that lowers nothing reads as a widening:\n%s", out)
	}

	// The repository's own file, the same edit and the same sentence: the file
	// is where that repository's list is stated, and the runs that lose the
	// escalation are its own.
	repoPath := modelsWriteGitCheckout(t, "owner/repo-a",
		rigorRepoLayerYAML+"security:\n  patterns:\n    - name: billing\n      match: internal/billing/**\n      category: credentials\n")
	out, _, err = execute(t, "rigor", "security", "rm", "--name", "billing", "--layer", "repo", "--repo", "owner/repo-a")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, "this lowers this repository's security list by removing the security path billing") {
		t.Errorf("the repository removal does not say what it lowers:\n%s", out)
	}
	if !strings.Contains(out, "every run of its branches") {
		t.Errorf("the sentence is not about that repository's runs:\n%s", out)
	}
	if !strings.Contains(out, "the file where the list is stated") {
		t.Errorf("the sentence does not say which document the list is stated in:\n%s", out)
	}
	if !strings.Contains(out, "owner/repo-a's working tree and nowhere else") {
		t.Errorf("the repository write does not say where the change is:\n%s", out)
	}
	if rows := securityRepoBlock(t, repoPath).Patterns; len(rows) != 0 {
		t.Errorf("the checkout's file reads %+v after the removal, want no rows", rows)
	}
}

// TestSecurityPathRemovalRefusedInAnAgentSession is D2617 for this command: the
// widening writes are refused in a coding agent's session whatever the gate
// says, for every marker the tree sets (agentenv.Markers itself rather than a
// copy) and in both files the list has. The refusal prints the diff, names the
// command to run at the owner's own terminal, and writes nothing at all.
//
// Three edits lower and all three are here: a row taken away, the project's
// switch turned off, and — the one an `add` can be — a row the list already
// carries given another glob, which takes the paths it named before out of the
// list. The last is why the classification is asked of the edit and not of the
// verb: a session that could re-aim a row could empty the list without ever
// running `rm`.
//
// The marker is what refuses and not the gate: this world's installation names
// no marks at all, so the gate stands in for nothing here, and the same edits
// land at the owner's own terminal (the two tests above) — while in the session
// the fence holds whatever the gate would have said (D1026).
func TestSecurityPathRemovalRefusedInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		for _, tc := range []struct {
			name string
			argv []string
			// terminal is the command the refusal must print, as the command
			// spells it: the flags that were given and nothing else, with a word
			// the shell would read as something other than itself quoted.
			terminal string
			// seed is the world's own state, and the file the refusal must leave
			// byte-identical is the one it answers with.
			seed func(t *testing.T) (path, before string)
		}{
			{
				name:     "a row of the project's list",
				argv:     []string{"rigor", "security", "rm", "--name", "billing"},
				terminal: "fugaro rigor security rm --name billing",
				seed: func(t *testing.T) (string, string) {
					t.Helper()
					path := securityLayerFile(t)
					return path, readFileString(t, path)
				},
			},
			{
				name:     "the project's switch",
				argv:     []string{"rigor", "security", "rm", "--always-level-3"},
				terminal: "fugaro rigor security rm --always-level-3",
				seed: func(t *testing.T) (string, string) {
					t.Helper()
					path := settingsManagedLayerFile(t, rigorAnchors+securityLayerSeedRows+"  always_level_3: true\n")
					return path, readFileString(t, path)
				},
			},
			{
				name:     "a row re-aimed by an add",
				argv:     []string{"rigor", "security", "add", "--name", "billing", "--match", "internal/other/**", "--category", "credentials"},
				terminal: "fugaro rigor security add --name billing --match 'internal/other/**' --category credentials",
				seed: func(t *testing.T) (string, string) {
					t.Helper()
					path := securityLayerFile(t)
					return path, readFileString(t, path)
				},
			},
			{
				name:     "a row of a repository's own list",
				argv:     []string{"rigor", "security", "rm", "--name", "billing", "--layer", "repo", "--repo", "owner/repo-a"},
				terminal: "fugaro rigor security rm --name billing --layer repo --repo owner/repo-a",
				seed: func(t *testing.T) (string, string) {
					t.Helper()
					path := modelsWriteGitCheckout(t, "owner/repo-a",
						rigorRepoLayerYAML+"security:\n  patterns:\n    - name: billing\n      match: internal/billing/**\n      category: credentials\n")
					return path, readFileString(t, path)
				},
			},
		} {
			t.Run(marker+"/"+tc.name, func(t *testing.T) {
				settingsWriteHome(t)
				noAgentSession(t)
				path, before := tc.seed(t)
				t.Setenv(marker, "1")

				out, _, err := execute(t, tc.argv...)
				if err == nil {
					t.Fatalf("a lowering write landed in a coding agent's session:\n%s", out)
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
				}
				if !strings.Contains(err.Error(), marker) {
					t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
				}
				if !strings.Contains(err.Error(), "coding agent's session") {
					t.Errorf("the refusal does not name the session: %v", err)
				}
				if !strings.Contains(err.Error(), "nothing was written") {
					t.Errorf("the refusal does not say that nothing was written: %v", err)
				}
				if !strings.Contains(err.Error(), tc.terminal) {
					t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
				}
				assertNoPrompt(t, out)
				if !strings.Contains(out, path) || !(strings.Contains(out, "  + ") || strings.Contains(out, "  - ")) {
					t.Errorf("the refusal does not print the diff of what it refused:\n%s", out)
				}
				if got := readFileString(t, path); got != before {
					t.Errorf("the file changed in a coding agent's session:\n%s", got)
				}
			})
		}
	}

	// Whatever the gate says, and whatever --ask asks for: the flag that puts a
	// managed project's confirmation back is refused where nobody can answer it,
	// and the file is still written by nothing.
	settingsWriteHome(t)
	noAgentSession(t)
	noTerminal(t)
	layerPath := securityLayerFile(t)
	before := readFileString(t, layerPath)
	agentSession(t)
	out, _, err := execute(t, "rigor", "security", "rm", "--name", "billing", "--ask")
	if err == nil {
		t.Fatalf("a removal with --ask landed in a coding agent's session:\n%s", out)
	}
	assertNoPrompt(t, out)
	if got := readFileString(t, layerPath); got != before {
		t.Errorf("the file changed in a coding agent's session:\n%s", got)
	}
}
