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
