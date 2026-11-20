package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
)

// settingsMetaTable hands the read a metadata table of the test's own. The
// table internal/settings holds is package state no agent session unregisters
// (unregisterMeta is unexported and only that package's own tests call it), so
// a test of what the render emits reads through this seam rather than
// registering into the binary's table — the seam settingsNow and doctorNow are,
// and for the same reason: what a test hands in is what the case is about.
func settingsMetaTable(t *testing.T, table map[string]settings.Meta) {
	t.Helper()
	prev := settingsMetaOf
	settingsMetaOf = func(key string) (settings.Meta, bool) {
		m, ok := table[key]
		return m, ok
	}
	t.Cleanup(func() { settingsMetaOf = prev })
}

// settingsMetaTableFor is the one table the cases below start from: an int with
// both bounds, an enum whose values a select draws in order, a string with a
// format the CLI already validates, and a fourth key (max_parallel, selected
// beside them) with no metadata at all.
func settingsMetaTableFor(t *testing.T) {
	t.Helper()
	settingsMetaTable(t, map[string]settings.Meta{
		"agent.review_rounds": {
			Label:       "Review rounds",
			Description: "How many review rounds a run spends before it finalizes.",
			Group:       settings.GroupRuns,
			Type:        settings.TypeInt,
			Min:         "0",
			Max:         "10",
		},
		"budget.mode": {
			Label:       "Budget mode",
			Description: "Whether the budget is observed, warned about or enforced.",
			Group:       settings.GroupBudget,
			Type:        settings.TypeEnum,
			Enum:        []string{"observe", "warn", "enforce"},
		},
		"git.base_branch": {
			Label:       "Base branch",
			Description: "The branch a run opens its pull request against.",
			Group:       settings.GroupRepositories,
			Type:        settings.TypeString,
			Format:      settings.FormatBranch,
		},
	})
}

// settingsRowsByKey decodes the document's rows into their members, so a case
// can ask whether a member is there at all: a struct drops the ones it does not
// name, which is exactly the question — a member a row omits and a member it
// spells empty are the same struct field and two different answers (D848).
func settingsRowsByKey(t *testing.T, out string) map[string]map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Settings []map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	byKey := map[string]map[string]json.RawMessage{}
	for _, row := range doc.Settings {
		var key string
		if err := json.Unmarshal(row["key"], &key); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		byKey[key] = row
	}
	if len(byKey) == 0 {
		t.Fatalf("the document carries no rows:\n%s", out)
	}
	return byKey
}

// TestSettingsJSONCarriesMeta: the eight metadata members of D2908 are on the
// row of every key the metadata table has an entry for, spelled:
//
//   - `label`, `description`, `group` and `type` on every row that has an
//     entry, because Register refuses an entry without them;
//   - `enum` on a `type: enum` row alone, in the order a select draws it;
//   - `min` and `max` on a bounded int, float or duration row alone, each in
//     the value's own spelling;
//   - `format` on a string whose shape the CLI already validates, and on no
//     other row;
//   - and none of the eight on a key the table has no entry for, which is the
//     fallback the console reads as "the raw key and a text control" (D2908,
//     SU1 of the same plan).
//
// A member that is absent rather than empty is the whole point: a consumer
// branches on `type` to draw a control, and an empty `type` would be a control
// the CLI never named. The last block is the other half — that the members the
// document already had are still on the row, so the eight are an addition and
// not a rewrite (D848).
func TestSettingsJSONCarriesMeta(t *testing.T) {
	settingsFixture(t, "")
	settingsMetaTableFor(t)

	out, _, err := execute(t, "settings", "--json",
		"agent.review_rounds", "budget.mode", "git.base_branch", "max_parallel")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	rows := settingsRowsByKey(t, out)

	for _, tc := range []struct {
		key   string
		want  map[string]string
		lists map[string][]string
		omit  []string
	}{
		{
			key: "agent.review_rounds",
			want: map[string]string{
				"label":       "Review rounds",
				"description": "How many review rounds a run spends before it finalizes.",
				"group":       "Runs",
				"type":        "int",
				"min":         "0",
				"max":         "10",
			},
			omit: []string{"enum", "format"},
		},
		{
			key: "budget.mode",
			want: map[string]string{
				"label":       "Budget mode",
				"description": "Whether the budget is observed, warned about or enforced.",
				"group":       "Budget",
				"type":        "enum",
			},
			lists: map[string][]string{"enum": {"observe", "warn", "enforce"}},
			omit:  []string{"min", "max", "format"},
		},
		{
			key: "git.base_branch",
			want: map[string]string{
				"label":       "Base branch",
				"description": "The branch a run opens its pull request against.",
				"group":       "Repositories",
				"type":        "string",
				"format":      "branch",
			},
			omit: []string{"enum", "min", "max"},
		},
		{
			key:  "max_parallel",
			omit: []string{"label", "description", "group", "type", "enum", "min", "max", "format"},
		},
	} {
		row, ok := rows[tc.key]
		if !ok {
			t.Errorf("%s: the read printed no row for it", tc.key)
			continue
		}
		for member, want := range tc.want {
			raw, ok := row[member]
			if !ok {
				t.Errorf("%s: the row carries no %s member, and the metadata table has an entry for the key: %v", tc.key, member, row)
				continue
			}
			var got string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Errorf("%s: %s = %s, and it is a string: %v", tc.key, member, raw, err)
				continue
			}
			if got != want {
				t.Errorf("%s: %s = %q, and the metadata says %q", tc.key, member, got, want)
			}
		}
		for member, want := range tc.lists {
			raw, ok := row[member]
			if !ok {
				t.Errorf("%s: the row carries no %s member, and the metadata has one", tc.key, member)
				continue
			}
			var got []string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Errorf("%s: %s = %s, and it is an array of strings: %v", tc.key, member, raw, err)
				continue
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s: %s = %v, and the metadata's order is %v", tc.key, member, got, want)
			}
		}
		for _, member := range tc.omit {
			if raw, ok := row[member]; ok {
				t.Errorf("%s: the row carries %s = %s, and a member the metadata has no value for is left out rather than written empty (D848)", tc.key, member, raw)
			}
		}
		// The members the document already had, on every row of this read: the
		// eight are members added within version 1, never a rewrite of the row
		// (D848, and "no existing member changes meaning" in the task's own
		// words). A reader outside this binary draws the table and the pencil
		// from them, so a row that lost one is a screen drawn from a document
		// no read prints.
		for _, member := range []string{"key", "scope", "wins", "default", "state", "writable", "owner", "target_layer"} {
			if _, ok := row[member]; !ok {
				t.Errorf("%s: the row no longer carries %s, and the metadata members are an addition to the row the console already reads", tc.key, member)
			}
		}
	}
}

// TestSettingsGetTextShowsLabel: `settings get KEY` prints the setting's own
// name and description above the table (D2908), so a person who asked for one
// key does not have to read a dotted name to know what it is. The wide read
// prints neither — 484 keys with two lines of prose each is a document, not a
// table — and a key with no metadata falls back to the key, which the table's
// own KEY column already carries.
func TestSettingsGetTextShowsLabel(t *testing.T) {
	settingsFixture(t, "")
	settingsMetaTableFor(t)

	const (
		label = "Review rounds"
		desc  = "How many review rounds a run spends before it finalizes."
	)
	out, _, err := execute(t, "settings", "get", "agent.review_rounds")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("one key with metadata prints its name, its description, the header and its row, got:\n%s", out)
	}
	if lines[0] != label {
		t.Errorf("the first line is %q, and the setting's label is %q", lines[0], label)
	}
	if lines[1] != desc {
		t.Errorf("the second line is %q, and the setting's description is %q", lines[1], desc)
	}
	if !strings.Contains(lines[3], "agent.review_rounds") {
		t.Errorf("the last line is %q, want the key's own row", lines[3])
	}
	if got := strings.Join(strings.Fields(lines[2]), " "); got != "KEY SCOPE VALUE WINS DEFAULT STATE" {
		t.Errorf("the third line is %q, want the six columns the read prints", lines[2])
	}

	// The fallback: a key the table has no entry for prints the table alone, so
	// the header block is a fact about the key and not about the subcommand.
	out, _, err = execute(t, "settings", "get", "max_parallel")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	lines = strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("a key with no metadata prints its header and its row, got:\n%s", out)
	}
	if strings.Contains(out, label) || strings.Contains(out, desc) {
		t.Errorf("the read printed another key's metadata above max_parallel:\n%s", out)
	}

	// And the wide read prints no label above any of its rows, even for a key
	// that has one.
	out, _, err = execute(t, "settings", "agent.review_rounds", "max_parallel")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, label) {
		t.Errorf("the read of two keys printed a label above the table:\n%s", out)
	}
}
