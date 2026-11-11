package cli

import (
	"slices"
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
		if settingsOwnerIsBareCommand(root, *row.Owner) {
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

	// The token and the write have to agree, which is the half the words above
	// cannot see: a concrete key the generic path refuses may not be the
	// default owner's, because that token means "`settings set` writes it"
	// (D1178). The `*` spellings are left out — no write lands on one, and the
	// read prints no owner for it — and so are the sensitive rows, whose owner
	// is the family that holds the secret rather than the generic path.
	for _, r := range settings.Rows() {
		if r.Sensitive || r.Owner != settingsOwnerDefaultToken || strings.Contains(r.Key, "*") {
			continue
		}
		if _, err := settingsWriteRow(r.Key); err != nil {
			t.Errorf("%s: the write refuses it (%v) and its row names the generic writer, which is a row saying a command writes a key it will not write (D1178, F13)", r.Key, err)
		}
	}
}

// TestSettingsOwnerFileCarriersNameTheCommandThatCarriesTheFile closes the two
// blind spots the vocabulary test above has by construction, and which the
// senior review of this task put findings on.
//
// The first is the sentence's words: a sentence passes there as long as its head
// is a command of the tree, so the `config` token could read `fugaro update,
// which applies the file the key is stated in` — a command that applies the file
// and never edits it — and every test stayed green. Each file-carrier token
// names the command that writes the file the key is stated in (D1515):
// `fugaro config publish` carries the project layer, `fugaro config init` writes
// a checkout's own `fugaro.yaml`, and `fugaro update` writes the machine's own
// config.
//
// The second is the rows the sentence is about: nothing held a token's rows to
// the file its sentence names, so `config` carried six orchestrator ceilings and
// the two nudge thresholds — keys of the machine's own config, which
// `fugaro config publish` never reads — and the read printed the layer file's
// sentence beside a row whose own target_layer is `local`. The registry answers
// which file a row is stated in, as the layer that file is: the project layer,
// the repository's own file, the machine's own.
func TestSettingsOwnerFileCarriersNameTheCommandThatCarriesTheFile(t *testing.T) {
	for _, tc := range []struct{ token, command, layer string }{
		{"config", "fugaro config publish", settings.LayerProject},
		{"config init", "fugaro config init", settings.LayerRepo},
		{"config local", "fugaro update", settings.LayerLocal},
	} {
		owner, ok := settingsOwnerCommands[tc.token]
		if !ok {
			t.Errorf("%s: no entry in the read's table, and the registry's rows name that token", tc.token)
			continue
		}
		head, _, _ := strings.Cut(owner, ",")
		if got := strings.TrimSpace(head); got != tc.command {
			t.Errorf("%s: the owner reads %q, and the command that writes the file the key is stated in is %q (D1515)", tc.token, owner, tc.command)
		}
		rows := 0
		for _, r := range settings.Rows() {
			if r.Owner != tc.token {
				continue
			}
			rows++
			if !slices.Contains(r.Layers, tc.layer) {
				t.Errorf("%s: the token's sentence names the file the layer %s is, and the row's layers are %v: the key is stated in no such file, so the read prints a sentence about a file it is not in (D1178, D1515)", r.Key, tc.layer, r.Layers)
			}
		}
		if rows == 0 {
			t.Errorf("%s: no registry row names the token, and an entry nothing prints is a sentence no reader reads", tc.token)
		}
	}
}

// TestSettingsOwnerMovesLeaveTheRefusalAlone is what keeps an owner move inside
// this task's contract — the registry's owner strings, and nothing of the write's.
// The write's refusal is built from the same token: settingsFamilyCommands is
// keyed by it (settings_set.go), so a row moved onto a family's token is refused
// in that family's name, and the read's word for that token is the family's bare
// command. Neither is this task's to change.
//
// The six orchestrator ceilings are why the pin exists: one commit of this branch
// gave them the `project` token, which is a family's, so
// `fugaro settings set orchestrator.max_running 8` answered `orchestrator.
// max_running is written by fugaro project, not by the generic path` — a refusal
// about a key no `fugaro project` subcommand edits, where before this task the
// same write answered the allowlist's own words. The rule is asked of every row
// the fixture moves, not of the six, so a later move onto a family token is red
// in the same place.
//
// The wildcard spellings are left out, as everywhere else: their literal `*` is
// refused by the map rule whatever the row's owner, and it is their concrete
// spellings a write lands on.
func TestSettingsOwnerMovesLeaveTheRefusalAlone(t *testing.T) {
	for _, tc := range settingsOwnerChanges {
		if strings.Contains(tc.key, "*") {
			continue
		}
		_, err := settingsWriteRow(tc.key)
		if err == nil {
			continue // the generic path's own: only the read's word moved
		}
		for token, command := range settingsFamilyCommands {
			if strings.Contains(err.Error(), "is written by "+command) {
				t.Errorf("%s: this task moves its owner to %q, and the write refuses the key in %s's name (`%s`): the write's map is keyed by the same token, so an owner moved onto one changes a refusal this task does not touch (D853, D1178)", tc.key, tc.now, command, token)
			}
		}
	}

	// The six, by name, refused in the words the write used before this task:
	// the allowlist's own refusal rather than a family's. A move onto another
	// family token is the loop above's; this is the pin a reading of the fixture
	// cannot give, because it says which wording the keys keep.
	for _, key := range []string{
		"orchestrator.max_running", "orchestrator.batch_size",
		"orchestrator.spawn_per_minute", "orchestrator.spawn_per_hour",
		"orchestrator.spawn_per_day", "orchestrator.decision_model_per_class",
	} {
		_, err := settingsWriteRow(key)
		if err == nil {
			t.Errorf("%s: the generic path writes it, and its owner names the command that carries it (D1178)", key)
			continue
		}
		if !strings.Contains(err.Error(), "is not a key the generic path writes") {
			t.Errorf("%s: the write refuses it in %v, and a row this task moves is refused by the allowlist's words, not a family's (D1178, D853)", key, err)
		}
	}
}

// TestSettingsRefusedRowsNameNoGenericWriter is the pair a reader cannot draw:
// the console prints `written by <owner>, not by the generic path` on a
// `writable: false` row, so a refused row that names the generic writer says in
// one sentence that `settings set` writes the key and that it does not. The
// write is what decides (D1102, D1178): every refused row names a command other
// than the generic writer, or names none where no command writes the spelling —
// the literal `*` of a wildcard row, whose concrete spellings are the generic
// path's, and the sensitive rows no generic write carries. `budget.mode` is the
// row that made the pair impossible to miss: it is a repository's own ceiling,
// it prints as the generic writer's since D1179 put it in the allowlist, and
// until then it read `writable: false` beside that same word.
func TestSettingsRefusedRowsNameNoGenericWriter(t *testing.T) {
	settingsWriteHome(t)
	rows := readSettingsWritability(t)
	generic := settingsOwnerCommands[settingsOwnerDefaultToken]
	refused, named := 0, 0
	for _, row := range rows {
		if row.Writable == nil || *row.Writable {
			continue
		}
		refused++
		if row.Owner == nil {
			t.Errorf("%s (%s): no owner member, and the read writes one on every row (D1178)", row.Key, row.Scope)
			continue
		}
		if *row.Owner == generic {
			t.Errorf("%s (%s): writable = false and owner = %q, and the console draws that as `written by %s, not by the generic path` (D1178)", row.Key, row.Scope, *row.Owner, generic)
		}
		if *row.Owner != "" {
			named++
		}
	}
	if refused == 0 {
		t.Fatal("no row of the read is refused, and the document refuses the keys no command writes (D1178)")
	}
	if named == 0 {
		t.Fatal("every refused row names no command, and the families' rows name theirs (D1178)")
	}
}

// settingsOwnerChanges is the table the plan asks G12 to carry in its own test:
// every key whose registry owner string this task moves, key → old owner → new
// owner, so the change is reviewable as a diff of names and a later task cannot
// move one silently (D1178, spec §4). The rule behind it is the design's: a key
// the allowlist carries is `settings set`'s and its row says so, and every other
// key names a command that exists or a sentence naming `fugaro setup` /
// `fugaro update`.
//
// None of D1179's four budget keys is here: `budget.mode`, `budget.per_day_usd`,
// `budget.per_run_usd` and `budget.allowed_models` already name the default
// owner and stay the generic writer's.
// The second half of the table is the rows the rule moves the other way, and
// they are the ones F13's shape hides: a key whose owner is the default while
// the generic path refuses it says a command writes a key it will not write.
// They name the command that writes the file they are stated in — `fugaro
// config init` for the four a checkout's `fugaro.yaml` is opened with, `fugaro
// config publish` (the `config` token, D1515) for the layer file's own header
// and for the two files' shared `planning.decoupling`.
// The rest of the table is this task's own `config` token under review: a row of it
// whose key is stated in the machine's own config rather than in the layer file
// printed a sentence about a file it is not in. The six orchestrator ceilings
// name `project policy`, the token whose words are `fugaro project policy
// publish` carrying the local block to the node (`buildProjectPolicy` copies it
// there), and the two nudge thresholds name `config local`, the machine's own
// file, which `fugaro update` writes — the same shape `config` and `config init`
// name for their files. Neither the six nor the two name `project`, which is a
// token of the write's own map: that would refuse the keys by name of
// `fugaro project`, a wording of the write's this task does not touch, and print
// the bare command D1178 asks a sentence in place of.
var settingsOwnerChanges = []struct{ key, was, now string }{
	{"base_branch", "config", "settings set"},
	{"budget.max_run_tokens", "budget", "settings set"},
	{"budget.unreachable_grace", "budget", "settings set"},
	{"commit_email", "setup", "settings set"},
	{"commit_name", "setup", "settings set"},
	{"gcp_project", "settings set", "config init"},
	{"issues.nudge_after_days", "config", "config local"},
	{"issues.nudge_recurrence", "config", "config local"},
	{"layer.gcp_project", "settings set", "config"},
	{"layer.project", "settings set", "config"},
	{"layer.version", "settings set", "config"},
	{"max_parallel", "config", "settings set"},
	{"orchestrator.batch_size", "config", "project policy"},
	{"orchestrator.decision_model_per_class", "config", "project policy"},
	{"orchestrator.max_running", "config", "project policy"},
	{"orchestrator.spawn_per_day", "config", "project policy"},
	{"orchestrator.spawn_per_hour", "config", "project policy"},
	{"orchestrator.spawn_per_minute", "config", "project policy"},
	{"planning.decoupling", "settings set", "config"},
	{"profile", "settings set", "config init"},
	{"project", "settings set", "config init"},
	{"update_check", "setup", "settings set"},
	{"version", "settings set", "config init"},
	{"watch.burn_alert_usd_per_hour", "budget", "settings set"},
}

// TestSettingsOwnerChangesAreTheOnesThePlanLists holds the fixture above to the
// registry: each key's row says the owner the fixture records, it did not
// before, and no other row the rule moves is left out. The last half is what
// keeps the table complete, and it asks both directions of the rule (D1178): a
// key the generic path writes whose row still names a family is a row `settings
// set` would refuse while writing it (F13, D1179), and a key it refuses whose
// row still names the default is a row naming a command that will not write it
// (F13's shape again, which is why `settings set` and writability are one
// question and not two).
func TestSettingsOwnerChangesAreTheOnesThePlanLists(t *testing.T) {
	listed := map[string]bool{}
	for _, tc := range settingsOwnerChanges {
		listed[tc.key] = true
		row, ok := settings.Lookup(tc.key)
		if !ok {
			t.Errorf("%s: no such registry row, and the fixture lists an owner change for it", tc.key)
			continue
		}
		if row.Owner != tc.now {
			t.Errorf("%s: the registry row's owner is %q, and this task moves it to %q (D1178)", tc.key, row.Owner, tc.now)
		}
		if tc.was == tc.now {
			t.Errorf("%s: the fixture records the same owner before and after, and a key whose owner does not move is not listed", tc.key)
		}
		if _, ok := settingsOwnerCommands[tc.now]; !ok {
			t.Errorf("%s: the fixture's new owner %q is no owner token of the read's table", tc.key, tc.now)
		}
		_, err := settingsWriteRow(tc.key)
		if tc.now == settingsOwnerDefaultToken {
			if err != nil {
				t.Errorf("%s: the generic path refuses it (%v), and only a key the allowlist carries is `settings set`'s", tc.key, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: the generic path writes it, and its row now names %q: a key the allowlist carries is `settings set`'s and its row says so (D1178)", tc.key, tc.now)
		}
	}

	for _, r := range settings.Rows() {
		// A wildcard row's own spelling is no key a write lands on: its
		// concrete spellings are the generic path's wherever the allowlist
		// carries the row, and the read prints no owner for the literal `*`.
		if r.Sensitive || strings.Contains(r.Key, "*") {
			continue
		}
		_, err := settingsWriteRow(r.Key)
		subject := ""
		switch {
		case err == nil && r.Owner != settingsOwnerDefaultToken:
			subject = "the generic path writes it and its row still names " + r.Owner
		case err != nil && r.Owner == settingsOwnerDefaultToken:
			subject = "the generic path refuses it and its row names the default owner"
		default:
			continue
		}
		if !listed[r.Key] {
			t.Errorf("%s: %s, and the fixture lists no owner change for it (D1178)", r.Key, subject)
		}
	}
}

// settingsOwnerCommandExists reports whether the owner word begins with a
// command of this binary's tree — the head of the sentence, the words before the
// first comma (`fugaro setup, which writes ...` names `fugaro setup`). It is the
// test for "a command that exists" in D1178's rule: the word is resolved against
// the tree the CLI builds rather than against a list of names beside it, so a
// command renamed or removed is red here.
func settingsOwnerCommandExists(root *cobra.Command, path string) bool {
	head, _, _ := strings.Cut(path, ",")
	words := strings.Fields(strings.TrimPrefix(head, root.Name()+" "))
	if len(words) == 0 {
		return false
	}
	c, _, err := root.Find(words)
	return err == nil && c != nil && c.Name() == words[len(words)-1]
}

// settingsOwnerIsBareCommand reports whether the owner word is nothing but a
// command of the tree, no sentence around it. D1178 asks the keys no command
// edits for a sentence, so a reader does not take the name for the key's own
// editor (spec §4).
func settingsOwnerIsBareCommand(root *cobra.Command, owner string) bool {
	head, _, sentence := strings.Cut(owner, ",")
	return !sentence && settingsOwnerCommandExists(root, head)
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
