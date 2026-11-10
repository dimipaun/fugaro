package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/spf13/cobra"
)

// TestSettingsReadNamesTheGenericWriterForAGenericKey: a key the allowlist
// carries is `settings set`'s, and the read says so instead of printing no
// owner at all (D1178, F11). The owner column was rendered through
// settingsFamilyCommands, the map a *write* refuses with, and that map has no
// entry for the default owner — so every key the generic path writes read with
// an empty owner beside it, and the console drew the row as one no command
// writes.
//
// `budget.mode` is one of them and the sharpest: it is the key the read prints
// as the repository's own ceiling (F13) and, until D1179 put it in the
// allowlist, the generic path refused it on purpose.
func TestSettingsReadNamesTheGenericWriterForAGenericKey(t *testing.T) {
	settingsWriteHome(t)
	rows := readSettingsWritability(t)

	for _, key := range []string{"budget.mode", "budget.per_day_usd", "agent.review_rounds", "max_parallel"} {
		row := writableRowOf(t, rows, key, "project")
		if row.Writable == nil || !*row.Writable {
			t.Errorf("%s: the generic path writes it and the document does not say so", key)
		}
		if row.Owner == nil {
			t.Errorf("%s: no owner member, and `fugaro settings set` writes it", key)
			continue
		}
		if *row.Owner != settingsOwnerCommands["settings set"] {
			t.Errorf("%s: owner = %q, and the command that writes it is %q (D1178)", key, *row.Owner, settingsOwnerCommands["settings set"])
		}
	}
}

// TestSettingsReadNamesSetupForAnInstallationKey: a key that is the
// installation's own — the endpoints, the batch machine, the version the
// converge recorded — is no command's edit, and the read names the command that
// owns it rather than printing nothing (D1178). The owner is the sentence the
// design asks for: what a person may run is `fugaro setup` (or `fugaro update`
// beside it), and no command in the tree edits the key.
func TestSettingsReadNamesSetupForAnInstallationKey(t *testing.T) {
	settingsWriteHome(t)
	rows := readSettingsWritability(t)
	root := NewRootCmd()

	for _, key := range []string{"endpoints.run", "batch.machine_type", "local.version"} {
		row := writableRowOf(t, rows, key, "project")
		if row.Writable != nil && *row.Writable {
			t.Errorf("%s: writable = true, and the generic path refuses the installation's own plumbing", key)
		}
		if row.Owner == nil || *row.Owner == "" {
			t.Errorf("%s: no owner member, and the installation's own key names the command that writes it (D1178)", key)
			continue
		}
		if !settingsOwnerNamesConverge(root, *row.Owner) {
			t.Errorf("%s: owner = %q, and the installation's own keys name `fugaro setup` or `fugaro update` (D1178; spec §4)", key, *row.Owner)
		}
		if settingsOwnerCommandExists(root, *row.Owner) {
			t.Errorf("%s: owner = %q is a bare command, and the design asks for a sentence naming the command rather than a name a reader takes for the key's own editor (spec §4)", key, *row.Owner)
		}
	}
}

// TestSettingsOwnerStringsAreAClosedVocabulary is the owner property of the
// design (spec §1, §6): every owner word the read prints is a command that
// exists or a sentence naming `fugaro setup` / `fugaro update`. A command is
// resolved against this binary's own tree, so a rename or a removal is red here
// rather than a word nothing runs; the family commands are the one exception,
// and settingsOwnerIsAFamilyCommand says why. The registry's tokens are the
// input: a row that grows a family the read's table does not carry fails here
// rather than printing nothing, which is the hole F11 was.
func TestSettingsOwnerStringsAreAClosedVocabulary(t *testing.T) {
	root := NewRootCmd()

	for _, r := range settings.Rows() {
		owner, ok := settingsOwnerCommands[r.Owner]
		if !ok || owner == "" {
			t.Errorf("%s: the registry's owner token %q has no entry in the read's table (settingsOwnerCommands), so the row prints no owner (F11)", r.Key, r.Owner)
			continue
		}
		if settingsOwnerCommandExists(root, owner) || settingsOwnerIsAFamilyCommand(owner) || settingsOwnerNamesConverge(root, owner) {
			continue
		}
		t.Errorf("%s: owner = %q, and a non-writable key names a command that exists or a sentence naming `fugaro setup` / `fugaro update` (D1178; spec §4)", r.Key, owner)
	}
}

// settingsOwnerChanges is the table the plan asks G12 to carry in its own test:
// every key whose registry owner string this task moves, key → old owner → new
// owner, so the change is reviewable as a diff of names and a later task cannot
// move one silently (D1178, spec §4). The rule behind it is the design's: a key
// the allowlist carries is `settings set`'s and its row says so, and every other
// key names a command that exists or a sentence naming `fugaro setup` / `fugaro
// update`.
//
// None of D1179's four budget keys is here: `budget.mode`, `budget.per_day_usd`,
// `budget.per_run_usd` and `budget.allowed_models` already name the default
// owner and stay the generic writer's.
var settingsOwnerChanges = []struct{ key, was string }{
	{"base_branch", "config"},
	{"budget.max_run_tokens", "budget"},
	{"budget.unreachable_grace", "budget"},
	{"commit_email", "setup"},
	{"commit_name", "setup"},
	{"max_parallel", "config"},
	{"update_check", "setup"},
	{"watch.burn_alert_usd_per_hour", "budget"},
}

// TestSettingsOwnerChangesAreTheOnesThePlanLists holds the fixture above to the
// registry: each key's row now says the default owner, it did not before, and
// no other row the rule moves is left out. The last half is what keeps the table
// complete — a key the generic path writes whose row still names a family is a
// row `settings set` would refuse to write while writing it (F13, D1179).
func TestSettingsOwnerChangesAreTheOnesThePlanLists(t *testing.T) {
	listed := map[string]bool{}
	for _, tc := range settingsOwnerChanges {
		listed[tc.key] = true
		row, ok := settings.Lookup(tc.key)
		if !ok {
			t.Errorf("%s: no such registry row, and the fixture lists an owner change for it", tc.key)
			continue
		}
		if row.Owner != "settings set" {
			t.Errorf("%s: the registry row's owner is %q, and this task moves it to the default owner `settings set` (D1178)", tc.key, row.Owner)
		}
		if tc.was == "settings set" {
			t.Errorf("%s: the fixture records the old owner as the default, and a key whose owner does not move is not listed", tc.key)
		}
		if _, err := settingsWriteRow(tc.key); err != nil {
			t.Errorf("%s: the generic path refuses it (%v), and only a key the allowlist carries is `settings set`'s", tc.key, err)
		}
	}

	for _, r := range settings.Rows() {
		if r.Owner == "settings set" || r.Sensitive {
			continue
		}
		if _, err := settingsWriteRow(r.Key); err != nil {
			continue
		}
		if !listed[r.Key] {
			t.Errorf("%s: the generic path writes it and its row still names %q, and the fixture lists no owner change for it (D1178: a key the allowlist carries is `settings set`'s and its row says so)", r.Key, r.Owner)
		}
	}
}

// settingsOwnerCommandExists reports whether path names a command of this
// binary's tree. It is the test for "a command that exists" in D1178's rule:
// the owner word is resolved against the tree the CLI builds rather than against
// a list of names beside it, so a command renamed or removed is red here.
func settingsOwnerCommandExists(root *cobra.Command, path string) bool {
	words := strings.Fields(strings.TrimPrefix(path, root.Name()+" "))
	if len(words) == 0 {
		return false
	}
	c, _, err := root.Find(words)
	return err == nil && c != nil && c.Name() == words[len(words)-1]
}

// settingsOwnerNamesConverge reports whether the owner word is the sentence the
// design asks for rather than a bare command name (spec §4): it begins with
// `fugaro setup` or `fugaro update` — the two commands that write the
// installation's own facts — and that command is one this binary holds. A bare
// command name would read as the key's own editor, which none of these keys has.
func settingsOwnerNamesConverge(root *cobra.Command, owner string) bool {
	for _, name := range []string{"fugaro setup", "fugaro update"} {
		if strings.HasPrefix(owner, name) && settingsOwnerCommandExists(root, name) {
			return true
		}
	}
	return false
}

// settingsOwnerIsAFamilyCommand reports whether the owner word is one of the
// commands the write's own table names. The read's table answers with the same
// words, so this is not a loose match: `fugaro rigor set` and `fugaro rigor
// floor` are the rigor block's commands and G6/G7 of this plan build them, so
// until they land the write's table — the refusal a person reads today — is
// where their names exist, and the design has the read print them all the same.
func settingsOwnerIsAFamilyCommand(owner string) bool {
	for _, command := range settingsFamilyCommands {
		if command == owner {
			return true
		}
	}
	return false
}
