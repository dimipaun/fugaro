package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// settingsMetaAgentFamilies is the six prefixes SM1 backfills (docs/
// console-improvements/implementation-plan.md, D2908): a run's own settings —
// the agent, the review ladder, the stages, the orchestrator and the issues —
// and the repositories block. The walk is the registry's, not a list here, so
// a key one of these blocks gains is a key the completeness test reads on the
// day it lands.
var settingsMetaAgentFamilies = []string{"agent.", "escalation.", "stages.", "orchestrator.", "issues.", "repos."}

// settingsMetaAgentFamilyKey reports whether key belongs to one of the six
// blocks, whatever the generic path does with it.
func settingsMetaAgentFamilyKey(key string) bool {
	for _, p := range settingsMetaAgentFamilies {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// settingsMetaAgentRows walks the registry's own rows for the family's keys:
// the ones settingsGenericKey hands the generic path, which are the ones D2908
// backfills. It is a slice and not an iterator so both tests read the same
// rows in the same order.
func settingsMetaAgentRows() []settings.Row {
	out := []settings.Row{}
	for _, row := range settings.Rows() {
		if settingsMetaAgentFamilyKey(row.Key) && settingsGenericKey(row.Key) {
			out = append(out, row)
		}
	}
	return out
}

// TestSettingsMetaAgentComplete is D2908's coverage rule for the six blocks:
// every key of them the generic path writes carries a label of at most six
// words, a one-sentence description, a group of the order and a type of the
// vocabulary. It fails on the first key without an entry, because that is the
// one a person has to write before the rest of the walk means anything.
func TestSettingsMetaAgentComplete(t *testing.T) {
	rows := settingsMetaAgentRows()
	if len(rows) == 0 {
		t.Fatal("no key of the family was read, and the walk over the registry is what this test is")
	}
	for _, row := range rows {
		m, ok := settings.MetaOf(row.Key)
		if !ok {
			t.Fatalf("%s is a key of the family the generic path writes and it has no metadata", row.Key)
		}
		if n := len(strings.Fields(m.Label)); n == 0 || n > 6 {
			t.Errorf("%s: the label %q is %d words, and a label is at most six", row.Key, m.Label, n)
		}
		if body, ok := strings.CutSuffix(m.Description, "."); !ok || body == "" || strings.Contains(body, ".") {
			t.Errorf("%s: the description %q is not one sentence", row.Key, m.Description)
		}
		if !slices.Contains(settings.MetaGroups, m.Group) {
			t.Errorf("%s: the group %q is not one of the order", row.Key, m.Group)
		}
		if !slices.Contains(settings.MetaTypes, m.Type) {
			t.Errorf("%s: the type %q is not one of the vocabulary", row.Key, m.Type)
		}
	}
}

// settingsMetaAgentWildcards is the concrete spelling the boundary test writes
// a wildcard row at: a row whose varying segment is a name the loader reads
// has no spelling of its own, and the test needs one to write. The name is a
// value the loader accepts for that segment — a rigor mode for the ladder's
// table, a repository for the repositories block, a stage for the stage
// accounts — and never a spelling the registry carries.
var settingsMetaAgentWildcards = map[string]string{
	"escalation.modes.*.r2_after_attempts": "pro",
	"escalation.modes.*.r2_after_hours":    "pro",
	"escalation.modes.*.r2_coder":          "pro",
	"repos.*.provider":                     "acme/app",
	"stages.*.claude_account":              "review",
}

// settingsMetaAgentSpelling is the key a write lands on for a registry row:
// the key itself, or the key with its varying segment filled in.
func settingsMetaAgentSpelling(key string) string {
	if !strings.Contains(key, "*") {
		return key
	}
	return strings.Replace(key, "*", settingsMetaAgentWildcards[key], 1)
}

// TestSettingsMetaAgentBoundariesAreAccepted is the other half of the
// metadata: an enum's values and a numeric range's ends are what the strict
// decode takes, proven rather than asserted. Every value a family entry states
// — each member of an enum, and each bound a key carries — is written into the
// document that key lands in and read back through settingsReparse, which is
// the loader's own read of the edited bytes. A bound the loader refuses is a
// bound the console would draw as valid and the CLI would then refuse.
func TestSettingsMetaAgentBoundariesAreAccepted(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	local, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	repoPath := filepath.Join(t.TempDir(), "fugaro.yaml")
	if err := os.WriteFile(repoPath, []byte(settingsRepoYAML("")), 0o600); err != nil {
		t.Fatal(err)
	}
	docs := map[string]struct {
		path []byte
		tgt  settingsTarget
	}{
		settingsLayerLocal: {path: local, tgt: settingsTarget{layer: settingsLayerLocal, kind: settingsDocLocal, path: localPath}},
		settingsLayerRepo:  {path: []byte(settingsRepoYAML("")), tgt: settingsTarget{layer: settingsLayerRepo, kind: settingsDocRepo, path: repoPath}},
	}

	checked := 0
	for _, row := range settingsMetaAgentRows() {
		m, ok := settings.MetaOf(row.Key)
		if !ok {
			t.Fatalf("%s has no metadata", row.Key)
		}
		values := slices.Clone(m.Enum)
		if m.Min != "" {
			values = append(values, m.Min)
		}
		if m.Max != "" {
			values = append(values, m.Max)
		}
		if len(values) == 0 {
			continue
		}
		layer := settingsDefaultLayer(row)
		doc, ok := docs[layer]
		if !ok {
			t.Fatalf("%s is written at --layer %s, and this test knows no document for it", row.Key, layer)
		}
		spelling := settingsMetaAgentSpelling(row.Key)
		tgt := doc.tgt
		tgt.keyPath = strings.Split(spelling, ".")
		for _, value := range values {
			next, err := yamledit.SetScalar(doc.path, tgt.keyPath, value)
			if err != nil {
				t.Fatalf("%s: writing %q: %v", spelling, value, err)
			}
			if err := settingsReparse(tgt, next); err != nil {
				t.Errorf("%s: the metadata offers %q and settingsReparse refuses it: %v", row.Key, value, err)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no enum value and no bound was read, and the write through the strict decode is what this test is")
	}
}
