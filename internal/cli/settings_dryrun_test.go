package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsDryRunJSON is the dry run's document as a consumer reads it. It is
// written out here rather than read off the CLI's own type on purpose: the
// member names are the contract (schemas/settings-write.schema.json is the
// other half of it), and a test that borrowed the struct would follow the
// command wherever it renamed a member.
type settingsDryRunJSON struct {
	Version  int    `json:"version"`
	Key      string `json:"key"`
	Layer    string `json:"layer"`
	Path     string `json:"path"`
	Changed  bool   `json:"changed"`
	Widening bool   `json:"widening"`
	Why      string `json:"why"`
	Old      string `json:"old"`
	New      string `json:"new"`
	Refusal  string `json:"refusal"`
}

// settingsDryRunDoc runs a dry run with --json and decodes the document it
// prints, which is the contract the console draws its dialog from (D1106).
func settingsDryRunDoc(t *testing.T, args ...string) settingsDryRunJSON {
	t.Helper()
	out, _, err := execute(t, append(slices.Clone(args), "--dry-run", "--json")...)
	if err != nil {
		t.Fatalf("the dry run was refused: %v\n%s", err, out)
	}
	var doc settingsDryRunJSON
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the dry run printed no document: %v\n%s", err, out)
	}
	return doc
}

// settingsNoBackup fails when a backup file sits beside path: a dry run leaves
// none, because it never gets as far as backing the file up (D1106).
func settingsNoBackup(t *testing.T, path string) {
	t.Helper()
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) > 0 {
		t.Errorf("the dry run left %v behind", backups)
	}
}

// TestSettingsSetDryRunWritesNothing: --dry-run says what the edit would do and
// writes nothing at all — not the file, not its mtime, and not the backup a
// write makes first — so the console can ask what an edit would do without
// being able to change the installation it is reading (D1106).
func TestSettingsSetDryRunWritesNothing(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)
	beforeStat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	noTerminal(t)

	out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo", "--dry-run")
	if err != nil {
		t.Fatalf("the dry run of a widening was refused with no terminal: %v\n%s", err, out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the dry run changed fugaro.yaml:\n%s", got)
	}
	afterStat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !afterStat.ModTime().Equal(beforeStat.ModTime()) {
		t.Errorf("the dry run touched fugaro.yaml: mtime %s, was %s", afterStat.ModTime(), beforeStat.ModTime())
	}
	settingsNoBackup(t, path)

	// The facts, and neither a confirmation nor a write's own closing line:
	// the two lines that would say something was written are the two the dry
	// run must not print.
	for _, want := range []string{"budget.per_run_usd", "repo", path, "widening", "10", "50"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run's table does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "CONFIRM") {
		t.Errorf("the dry run asked to confirm a write it does not make:\n%s", out)
	}
	if strings.Contains(out, "next: fugaro update") {
		t.Errorf("the dry run closes like a write:\n%s", out)
	}
}

// TestSettingsSetDryRunClassifiesAWideningAsAWidening: raising a ceiling is the
// widening direction, and the classification is the write path's own decision
// and not a second copy of it — the dry run answers with the value of the call
// that decides whether the typed name is demanded, so a key added to
// settingsCeilingKey moves the console's dialog with no console edit (D1106).
func TestSettingsSetDryRunClassifiesAWideningAsAWidening(t *testing.T) {
	settingsWriteHome(t)
	settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	noTerminal(t)

	doc := settingsDryRunDoc(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo")
	if doc.Key != "budget.per_run_usd" || doc.Layer != "repo" {
		t.Errorf("the document names %s at layer %s, and the edit was budget.per_run_usd at layer repo", doc.Key, doc.Layer)
	}
	if !doc.Changed {
		t.Error("the document says the edit changes nothing, and it raises the cap")
	}
	if !doc.Widening {
		t.Errorf("raising a ceiling is not classified as a widening: %+v", doc)
	}
	if doc.Why == "" {
		t.Errorf("a widening carries no sentence saying why the typed form is asked for: %+v", doc)
	}
	if doc.Old != "10" || doc.New != "50" {
		t.Errorf("the document says %q -> %q, and the file holds 10 and the edit writes 50", doc.Old, doc.New)
	}
	// A classification is a document and a refusal is a sentence with a
	// non-zero exit and no document (D1106, D1112), so this member is empty
	// on every document the CLI prints: it is the shape's reserved half.
	if doc.Refusal != "" {
		t.Errorf("a classification carries a refusal sentence: %q", doc.Refusal)
	}

	row, ok := settings.Lookup("budget.per_run_usd")
	if !ok {
		t.Fatal("the registry has no budget.per_run_usd row, so this test covers nothing")
	}
	if typed, why := settingsTypedWrite(row, "10", "50", "set"); doc.Widening != typed || doc.Why != why {
		t.Errorf("the document says widening=%v why=%q, and settingsTypedWrite says %v %q", doc.Widening, doc.Why, typed, why)
	}
}

// TestSettingsSetDryRunClassifiesANarrowingAsANarrowing: lowering a ceiling is
// not the typed direction, and the document says so — the other half of the
// classification, and the half a wrong answer in the widening direction would
// hide.
func TestSettingsSetDryRunClassifiesANarrowingAsANarrowing(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 50\n"))
	before := readFileString(t, path)
	noTerminal(t)

	doc := settingsDryRunDoc(t, "settings", "set", "budget.per_run_usd", "10", "--layer", "repo")
	if !doc.Changed {
		t.Error("the document says the edit changes nothing, and it lowers the cap")
	}
	if doc.Widening {
		t.Errorf("lowering a ceiling is classified as a widening: %+v", doc)
	}
	if doc.Why != "" {
		t.Errorf("a narrowing carries the widening's own sentence: %q", doc.Why)
	}
	if doc.Old != "50" || doc.New != "10" {
		t.Errorf("the document says %q -> %q, and the file holds 50 and the edit writes 10", doc.Old, doc.New)
	}
	if readFileString(t, path) != before {
		t.Error("the dry run changed fugaro.yaml")
	}
}

// TestSettingsSetDryRunRefusesASensitiveKey: a sensitive row is refused before
// the edit is built, so the dry run refuses it the same way the write does —
// with the CLI's own sentence, a non-zero exit and no document at all, and
// never the value (spec §2.3, D1112).
func TestSettingsSetDryRunRefusesASensitiveKey(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	const value = "not-a-real-credential-value"

	out, _, err := execute(t, "settings", "set", "budget.firebase_api_key", value, "--layer", "local", "--dry-run", "--json")
	if err == nil {
		t.Fatalf("the dry run of a sensitive row was answered:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "sensitive") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if printed := out + err.Error(); strings.Contains(printed, value) {
		t.Errorf("the sensitive value reached the output:\n%s", printed)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("a refusal printed a document, and the console branches on a non-zero exit with none:\n%s", out)
	}
	if readFileString(t, localPath) != before {
		t.Error("the refused dry run changed the local config")
	}
}

// TestSettingsSetDryRunRefusesAKeyTheGenericPathDoesNotOwn: the dry run answers
// for the write, so a key the write refuses is refused here too, in the write's
// own words — a key a family owns, and a key spec §4.4 never handed the generic
// path (D1106).
func TestSettingsSetDryRunRefusesAKeyTheGenericPathDoesNotOwn(t *testing.T) {
	settingsWriteHome(t)
	for _, tc := range []struct{ key, want string }{
		{"layer.defaults.agent.model", "fugaro models"},
		{"runs_bucket", "not a key the generic path writes"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			out, _, err := execute(t, "settings", "set", tc.key, "a-value", "--layer", "local", "--dry-run", "--json")
			if err == nil {
				t.Fatalf("the dry run answered for %s, and the write refuses it:\n%s", tc.key, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name %q: %v", tc.want, err)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("a refusal printed a document:\n%s", out)
			}
		})
	}
}

// TestSettingsSetDryRunJSONMatchesSchema validates the dry run's document
// against schemas/settings-write.schema.json, the contract a consumer reads it
// by. Both shapes are validated: a widening with an old and a new value, and a
// removal, whose new value is empty.
func TestSettingsSetDryRunJSONMatchesSchema(t *testing.T) {
	settingsWriteHome(t)
	// The schema is read before the checkout moves the working directory into
	// its own temp dir.
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "settings-write.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/settings-write.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	noTerminal(t)

	for _, args := range [][]string{
		{"settings", "set", "budget.per_run_usd", "50", "--layer", "repo"},
		{"settings", "unset", "budget.per_run_usd", "--layer", "repo"},
	} {
		out, _, err := execute(t, append(slices.Clone(args), "--dry-run", "--json")...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
		if err != nil {
			t.Fatalf("%v printed no document: %v\n%s", args, err, out)
		}
		if err := sch.Validate(inst); err != nil {
			t.Fatalf("%v does not match schemas/settings-write.schema.json: %v\n%s", args, err, out)
		}
	}
}

// TestSettingsUnsetDryRunWritesNothing: the removal of a ceiling is the second
// door --dry-run answers, and it writes nothing either — the file, its mtime
// and the backup a write makes first all stay as they were.
func TestSettingsUnsetDryRunWritesNothing(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)
	beforeStat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	noTerminal(t)

	out, _, err := execute(t, "settings", "unset", "budget.per_run_usd", "--layer", "repo", "--dry-run")
	if err != nil {
		t.Fatalf("the dry run of a removal was refused with no terminal: %v\n%s", err, out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the dry run changed fugaro.yaml:\n%s", got)
	}
	afterStat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !afterStat.ModTime().Equal(beforeStat.ModTime()) {
		t.Errorf("the dry run touched fugaro.yaml: mtime %s, was %s", afterStat.ModTime(), beforeStat.ModTime())
	}
	settingsNoBackup(t, path)
	if strings.Contains(out, "next: fugaro update") {
		t.Errorf("the dry run closes like a write:\n%s", out)
	}

	doc := settingsDryRunDoc(t, "settings", "unset", "budget.per_run_usd", "--layer", "repo")
	if !doc.Changed {
		t.Error("the document says removing the cap changes nothing, and the file holds it")
	}
	if !doc.Widening {
		t.Errorf("removing a ceiling is not classified as a widening: %+v", doc)
	}
	if doc.Old != "10" || doc.New != "" {
		t.Errorf("the document says %q -> %q, and the file holds 10 and the removal leaves nothing", doc.Old, doc.New)
	}
}

// TestSettingsSetDryRunNeedsNoTerminal is the property the console depends on:
// the console's child has no terminal at all, so the dry run of a widening —
// whose write would demand the project's name typed at one — has to answer with
// stdin closed (D1106).
func TestSettingsSetDryRunNeedsNoTerminal(t *testing.T) {
	settingsWriteHome(t)
	settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	noTerminal(t)

	for _, args := range [][]string{
		{"settings", "set", "budget.per_run_usd", "50", "--layer", "repo"},
		{"settings", "set", "budget.per_run_usd", "10", "--layer", "repo"},
		{"settings", "unset", "budget.per_run_usd", "--layer", "repo"},
	} {
		out, _, err := execute(t, append(slices.Clone(args), "--dry-run", "--json")...)
		if err != nil {
			t.Fatalf("the dry run of %v needs a terminal: %v\n%s", args, err, out)
		}
		if strings.Contains(out, "typed confirmation") {
			t.Errorf("the dry run of %v asked for the typed confirmation:\n%s", args, out)
		}
	}
}

// TestSettingsSetDryRunReportsNoChange: an edit that changes nothing is a
// classification like any other — the document says so and the command exits 0
// — because the console draws the same window for it, where the write would
// print its own sentence and stop.
func TestSettingsSetDryRunReportsNoChange(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)

	doc := settingsDryRunDoc(t, "settings", "set", "budget.per_run_usd", "10", "--layer", "repo")
	if doc.Changed {
		t.Errorf("the document says the edit changes something, and the file already reads 10: %+v", doc)
	}
	if doc.Widening {
		t.Errorf("an edit that changes nothing is classified as a widening: %+v", doc)
	}
	if doc.Old != "10" || doc.New != "10" {
		t.Errorf("the document says %q -> %q, and the file holds 10 and the edit writes 10", doc.Old, doc.New)
	}
	if readFileString(t, path) != before {
		t.Error("the dry run changed fugaro.yaml")
	}
}

// TestSettingsDryRunTakesNeitherYesNorABareJSON: --dry-run and --yes answer
// different questions, and the pair is refused rather than resolved; --json is
// the dry run's own shape, so it is refused without it (the two flags the
// console's argv carries, and the two ways a caller could mistake what it got).
func TestSettingsDryRunTakesNeitherYesNorABareJSON(t *testing.T) {
	settingsWriteHome(t)
	settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))

	out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo", "--dry-run", "--yes")
	if err == nil {
		t.Fatalf("--dry-run with --yes was accepted:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	for _, want := range []string{"--dry-run", "--yes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("the refused pair printed a document:\n%s", out)
	}

	out, _, err = execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo", "--json")
	if err == nil {
		t.Fatalf("--json without --dry-run was accepted:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("the refusal does not name the flag --json goes with: %v", err)
	}
}

// TestSettingsSetDryRunTakesTheValueAfterTheTerminator measures U1, which
// D1104's whole safety argument rests on: the console spawns
// `settings set KEY --layer L --dry-run --json -- <VALUE>`, and the value is
// the second positional argument — read after the terminator, so a value
// spelled `--layer` or `-draft` is a value and not a flag.
func TestSettingsSetDryRunTakesTheValueAfterTheTerminator(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	before := readFileString(t, localPath)
	noTerminal(t)

	out, _, err := execute(t, "settings", "set", "commit_name", "--layer", "local", "--dry-run", "--json", "--", "--draft")
	if err != nil {
		t.Fatalf("the console's own argv was refused: %v\n%s", err, out)
	}
	var doc settingsDryRunJSON
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the dry run printed no document: %v\n%s", err, out)
	}
	if doc.New != "--draft" {
		t.Errorf("the value after -- did not reach the edit: new = %q\n%s", doc.New, out)
	}
	if doc.Key != "commit_name" || doc.Layer != "local" {
		t.Errorf("the document names %s at layer %s, and the edit was commit_name at layer local", doc.Key, doc.Layer)
	}
	if readFileString(t, localPath) != before {
		t.Error("the dry run changed the local config")
	}
}
