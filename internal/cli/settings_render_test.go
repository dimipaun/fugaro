package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsFakeAPIKey is the placeholder every budget fixture in this package
// writes: it is shaped like a Google API key and is not one.
const settingsFakeAPIKey = "AIzaSyFakeFakeFakeFakeFake12345"

// TestSettingsNeverPrintsASensitiveValue: a sensitive row is the set/unset
// fact and nothing else — not in the table, not under --why, and not in the
// document, where the value member is absent rather than null or a sentinel.
// The value is written into the fixture so the test proves the redaction and
// not an unset key.
func TestSettingsNeverPrintsASensitiveValue(t *testing.T) {
	settingsFixture(t, "budget:\n  firebase_api_key: "+settingsFakeAPIKey+"\n")

	out, _, err := execute(t, "settings", "budget.firebase_api_key", "--why")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, settingsFakeAPIKey) {
		t.Errorf("the table printed the value of a sensitive key:\n%s", out)
	}
	if !strings.Contains(out, "set") {
		t.Errorf("the table does not say the key is set:\n%s", out)
	}

	raw, _, err := execute(t, "settings", "budget.firebase_api_key", "--json", "--why")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if strings.Contains(raw, settingsFakeAPIKey) {
		t.Errorf("the document printed the value of a sensitive key:\n%s", raw)
	}
	doc := readSettingsJSON(t, "budget.firebase_api_key", "--why")
	row := rowOf(t, doc, "budget.firebase_api_key", "project")
	if !row.Sensitive {
		t.Errorf("the row does not carry sensitive: true")
	}
	if row.Value != nil {
		t.Errorf("value = %q: a sensitive row carries no value member at all", *row.Value)
	}
	for _, l := range row.Levels {
		if strings.Contains(l.Value, settingsFakeAPIKey) {
			t.Errorf("level %s printed the value of a sensitive key", l.Layer)
		}
	}
}

// TestSettingsSensitiveKeyUnsetSaysSo: the same row with no layer setting it
// prints unset, so a reader can tell "set to something I may not see" from
// "set to nothing" without ever seeing the value.
func TestSettingsSensitiveKeyUnsetSaysSo(t *testing.T) {
	settingsFixture(t, "")
	out, _, err := execute(t, "settings", "budget.firebase_api_key")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "unset") {
		t.Errorf("an unset sensitive key does not print unset:\n%s", out)
	}
}

// TestSettingsJSONMatchesSchema validates the document the command writes
// against schemas/settings.schema.json, the contract a consumer reads it by.
// The fixture is the widest document one machine can produce offline: a
// project scope, a repository scope with a checkout and one without, a layer
// cache the read marks stale, and a sensitive row.
func TestSettingsJSONMatchesSchema(t *testing.T) {
	home := settingsFixture(t, "budget:\n  firebase_api_key: "+settingsFakeAPIKey+"\nrepos:\n  owner/repo-b: {}\n")
	settingsCheckout(t, home, "owner/repo-a", settingsRepoFromProfile+"agent:\n  review_rounds: 3\n")
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now().Add(-3 * time.Minute), YAML: testProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "settings", "--json", "--all-repos", "--why")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/settings.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/settings.schema.json: %v\n%s", err, out)
	}

	// The document is only worth validating while it carries the states the
	// schema closes over: a fixture that stopped producing them would leave
	// the vocabulary untested and still green.
	states := map[string]bool{}
	for _, s := range doc2states(t, out) {
		states[s] = true
	}
	for _, want := range []string{"ok", "stale 3m", "unknown (no checkout on this machine)"} {
		if !states[want] {
			t.Errorf("the document carries no %q state (it has %v)", want, states)
		}
	}

	// And with both branches of $defs.decider in it: the layer names and the
	// spelling for a value a profile decided (config.SourceProfile), in the
	// row and in the level --why adds beside it. A fixture that stopped
	// producing one would leave that branch of the schema unchecked.
	var parsed settingsTestDoc
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	byProfile, levelByProfile := false, false
	for _, r := range parsed.Settings {
		if r.Wins == config.SourceProfile("svc") {
			byProfile = true
		}
		for _, l := range r.Levels {
			if l.Layer == config.SourceProfile("svc") {
				levelByProfile = true
			}
		}
	}
	if !byProfile || !levelByProfile {
		t.Errorf("the document's rows decided by a profile: %v, and a level naming one: %v, want both", byProfile, levelByProfile)
	}
}

// doc2states is every state the document prints, rows and sources alike.
func doc2states(t *testing.T, out string) []string {
	t.Helper()
	var parsed struct {
		Sources []struct {
			Status string `json:"status"`
		} `json:"sources"`
		Settings []struct {
			State string `json:"state"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	var states []string
	for _, s := range parsed.Sources {
		states = append(states, s.Status)
	}
	for _, s := range parsed.Settings {
		states = append(states, s.State)
	}
	return states
}

// settingsWritabilityRow is the writability half of a row, decoded here rather
// than through settingsTestRow (settings_read_test.go) because the three
// members are this task's own contract: a test that decoded them through the
// producer's struct would agree with the producer whatever it wrote. The three
// are pointers so a test can tell a member that is absent from one that carries
// false or the empty string, which is the difference between a document a
// consumer may read and one it may not.
type settingsWritabilityRow struct {
	Key         string  `json:"key"`
	Scope       string  `json:"scope"`
	Writable    *bool   `json:"writable"`
	Owner       *string `json:"owner"`
	TargetLayer *string `json:"target_layer"`
}

// readSettingsWritability runs the read and decodes every row's writability,
// keyed by key and scope.
func readSettingsWritability(t *testing.T, args ...string) map[string]settingsWritabilityRow {
	t.Helper()
	out, _, err := execute(t, append([]string{"settings", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var doc struct {
		Settings []settingsWritabilityRow `json:"settings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	rows := make(map[string]settingsWritabilityRow, len(doc.Settings))
	for _, r := range doc.Settings {
		rows[r.Key+"\x00"+r.Scope] = r
	}
	return rows
}

// writableRowOf is the one row of key in scope, or a fatal.
func writableRowOf(t *testing.T, rows map[string]settingsWritabilityRow, key, scope string) settingsWritabilityRow {
	t.Helper()
	row, ok := rows[key+"\x00"+scope]
	if !ok {
		t.Fatalf("%s in %s: no such row (the document has %d rows)", key, scope, len(rows))
	}
	return row
}

// TestSettingsJSONMarksTheGenericPathsRowsWritable: the read answers, per row,
// whether the generic path writes it — the fact the console draws its pencils
// from, so no consumer has to keep a copy of the allowlist (D1102, F10). The
// answer is not the allowlist alone: spec §4.4 hands the generic path the
// `agent.` block whole, and `agent.model` is inside it while the models family
// owns the row, so a read drawn from the allowlist would offer an edit the
// write refuses.
func TestSettingsJSONMarksTheGenericPathsRowsWritable(t *testing.T) {
	settingsWriteHome(t)
	rows := readSettingsWritability(t)

	for _, key := range []string{
		"budget.per_run_usd", "budget.allowed_models", "commit_name", "max_parallel",
		"agent.review_rounds", "git.pr.labels", "layer.default_profile",
	} {
		row := writableRowOf(t, rows, key, "project")
		if row.Writable == nil {
			t.Errorf("%s: no writable member, and settings set writes it", key)
			continue
		}
		if !*row.Writable {
			t.Errorf("%s: writable = false, and settings set writes it", key)
		}
	}
	for _, key := range []string{
		"agent.model", "budget.firebase_project", "endpoints.run", "batch.machine_type",
		"budget.firebase_api_key",
	} {
		row := writableRowOf(t, rows, key, "project")
		if row.Writable == nil {
			t.Errorf("%s: no writable member, and settings set refuses it", key)
			continue
		}
		if *row.Writable {
			t.Errorf("%s: writable = true, and settings set refuses it", key)
		}
	}
}

// TestSettingsJSONNamesTheFamilyThatOwnsTheOthers: a row the generic path does
// not write carries the command family that does, in S5's own words, so the
// console says who writes it instead of drawing a pencil (D1102, D1111). A row
// no family owns — the installation's own plumbing — names nothing and is not
// writable: the honest answer, not an invented name.
func TestSettingsJSONNamesTheFamilyThatOwnsTheOthers(t *testing.T) {
	settingsWriteHome(t)
	rows := readSettingsWritability(t)

	for _, tc := range []struct{ key, family string }{
		{"agent.model", "fugaro models"},
		{"layer.defaults.rigor.default", "fugaro rigor set"},
		{"job_profiles.*.cpu", "fugaro project"},
		{"batch.secrets", "fugaro secrets"},
	} {
		row := writableRowOf(t, rows, tc.key, "project")
		if row.Owner == nil {
			t.Errorf("%s: no owner member, and %s writes it", tc.key, tc.family)
		} else if *row.Owner != tc.family {
			t.Errorf("%s: owner = %q, and the family that writes it is %q", tc.key, *row.Owner, tc.family)
		}
		if row.Writable != nil && *row.Writable {
			t.Errorf("%s: writable = true, and a family owns it", tc.key)
		}
	}

	row := writableRowOf(t, rows, "budget.firebase_project", "project")
	if row.Owner == nil {
		t.Errorf("budget.firebase_project: no owner member, and the empty string is the answer for a key no family owns")
	} else if *row.Owner != "" {
		t.Errorf("budget.firebase_project: owner = %q, and no family owns the installation's plumbing", *row.Owner)
	}
}

// TestSettingsJSONOwnerMatchesTheRefusalSettingsSetPrints is the anti-drift pin
// (D1102): every row the document names a family for is a row `settings set`
// refuses, and the command's own sentence names the same family. The two are
// read from one table in the CLI, and this is what makes a second copy — or a
// table that moved without the other — red rather than a pencil that offers an
// edit the write will not make.
func TestSettingsJSONOwnerMatchesTheRefusalSettingsSetPrints(t *testing.T) {
	settingsWriteHome(t)
	rows := readSettingsWritability(t)
	named := 0
	for _, r := range settings.Rows() {
		row := writableRowOf(t, rows, r.Key, "project")
		if row.Owner == nil || *row.Owner == "" {
			continue
		}
		named++
		if row.Writable != nil && *row.Writable {
			t.Errorf("%s: writable = true and owner = %q, and a row with an owner is one the generic path refuses", r.Key, *row.Owner)
		}
		_, _, err := execute(t, "settings", "set", r.Key, "x")
		if err == nil {
			t.Errorf("%s: settings set wrote a key the document says %s owns", r.Key, *row.Owner)
			continue
		}
		if !strings.Contains(err.Error(), *row.Owner) {
			t.Errorf("%s: the document names %q, and settings set refuses it with %q", r.Key, *row.Owner, err)
		}
	}
	if named == 0 {
		t.Fatal("no row of the document names a command family, and the registry has rows a family owns")
	}
}

// TestSettingsJSONTargetLayerMatchesWhatSetWrites: target_layer is the layer a
// write with no --layer lands in (D1102, D1105), which is what lets the console
// pass the layer explicitly and show the operator the file the edit reaches.
// Two rows with different answers: one the local config takes, one only a
// checkout's fugaro.yaml may hold.
func TestSettingsJSONTargetLayerMatchesWhatSetWrites(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	row := writableRowOf(t, readSettingsWritability(t), "commit_name", "project")
	if row.TargetLayer == nil || *row.TargetLayer != settingsLayerLocal {
		t.Fatalf("commit_name: target_layer = %v, and a write with no --layer lands in %s", row.TargetLayer, settingsLayerLocal)
	}
	if out, _, err := execute(t, "settings", "set", "commit_name", "Fugaro", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, localPath); !strings.Contains(got, "commit_name: Fugaro") {
		t.Errorf("the write did not land in the file the read named (%s):\n%s", localPath, got)
	}

	checkout := settingsWriteCheckout(t, settingsRepoYAML(""))
	row = writableRowOf(t, readSettingsWritability(t), "git.base_branch", "project")
	if row.TargetLayer == nil || *row.TargetLayer != settingsLayerRepo {
		t.Fatalf("git.base_branch: target_layer = %v, and a write with no --layer lands in %s", row.TargetLayer, settingsLayerRepo)
	}
	if out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, checkout); !strings.Contains(got, "base_branch: develop") {
		t.Errorf("the write did not land in the file the read named (%s):\n%s", checkout, got)
	}
}

// TestSettingsJSONIsStillVersionOne: the three members are members added within
// version 1 (D848), so neither the document nor the schema moves its version.
// A consumer that reads version 1 reads the new members as members it does not
// know rather than as a document it must refuse.
func TestSettingsJSONIsStillVersionOne(t *testing.T) {
	settingsWriteHome(t)
	doc := readSettingsJSON(t)
	if doc.Version != 1 {
		t.Errorf("the document's version is %d, want 1", doc.Version)
	}
	if settingsDocVersion != 1 {
		t.Errorf("settingsDocVersion is %d, want 1", settingsDocVersion)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Version struct {
				Const float64 `json:"const"`
			} `json:"version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties.Version.Const != 1 {
		t.Errorf("schemas/settings.schema.json: version const = %v, want 1", schema.Properties.Version.Const)
	}
}
