package cli

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
)

// TestSettingsConsoleFixtureOwnerAndWritable holds the two members of the
// console's settings fixture that the registry cannot answer to the read's own
// functions.
//
// The console's hermetic CLI stub answers `fugaro settings --json` with
// console/test/fixtures/settings.json, and the e2e spec draws that document as
// the CLI's own read (the settings-edit plan's C4, D1118), so a member the
// fixture spells differently from the CLI is a screen drawn from a document no
// read prints. TestSettingsConsoleFixture (schemas/schemas_test.go) holds the
// document to the schema, to the registry's defaults, to the rules its own
// members keep with each other, and to `target_layer` and `sensitive`, whose
// answers are in the registry.
//
// These two are not, which is why they are checked here: `owner` is the word the
// read prints for a key the generic path does not write, and it comes from
// settingsFamilyCommands -- a map from the registry's owner token to the command
// family a screen names -- and `writable` is the generic path's own decision for
// the key, asked of the write (D1102): projectRow sets it from settingsWriteRow,
// the same function `settings set` refuses with, and never from a second list
// beside it. Both are what the console draws a pencil from (its rule is a row
// that is `writable` with a `targetLayer` that is not empty, C2), so a fixture
// that answers either one differently is a row whose edit button the window
// shows or withholds against the CLI's own answer.
//
// The console's own pin of the same two members (test/unit/cli-stub.test.ts)
// restates the fixture's values by hand; this is the tie to the CLI that keeps
// the fixture honest, so the drift C4 fixed -- `owner: "settings set"` and
// `writable: true` on rows no write produces, and `target_layer` on a row the
// write never lands in -- is red here rather than at a screen.
func TestSettingsConsoleFixtureOwnerAndWritable(t *testing.T) {
	const file = "../../console/test/fixtures/settings.json"
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Settings []struct {
			Key      string `json:"key"`
			Writable bool   `json:"writable"`
			Owner    string `json:"owner"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Settings) == 0 {
		t.Fatalf("%s carries no settings rows, and the stub's answer to the Project tab is a document with rows", file)
	}
	for _, row := range fixture.Settings {
		reg, known := settings.Lookup(row.Key)
		if !known {
			t.Errorf("%s: the fixture carries a row the registry has no key for, and no read prints such a row", row.Key)
			continue
		}
		if want := settingsFamilyCommands[reg.Owner]; row.Owner != want {
			t.Errorf("%s: the fixture's owner is %q, and the read prints %q for a row the registry gives the owner token %q (settingsFamilyCommands)", row.Key, row.Owner, want, reg.Owner)
		}
		_, writeErr := settingsWriteRow(row.Key)
		if want := writeErr == nil; row.Writable != want {
			t.Errorf("%s: the fixture says writable = %v, and the generic path's own answer for the key is %v (settingsWriteRow, which `settings set` refuses with): %v", row.Key, row.Writable, want, writeErr)
		}
	}
}
