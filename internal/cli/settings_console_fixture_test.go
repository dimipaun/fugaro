package cli

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dimipaun/fugaro/internal/settings"
)

// TestSettingsConsoleFixtureOwnerAndWritable holds the members of the console's
// settings fixture whose answers are this package's own functions to the
// functions themselves, rather than to a restatement of what they do: `owner`
// and `writable`, the two the name carries, and `target_layer`, the third member
// of the pencil's own rule (C2) and answered in this package too.
//
// The console's hermetic CLI stub answers `fugaro settings --json` with
// console/test/fixtures/settings.json, and the e2e spec draws that document as
// the CLI's own read (the settings-edit plan's C4, D1118), so a member the
// fixture spells differently from the CLI is a screen drawn from a document no
// read prints. TestSettingsConsoleFixture (schemas/schemas_test.go) holds the
// document to the schema, to the registry's defaults, to the rules its own
// members keep with each other, and to `sensitive`, whose answer is in the
// registry. It also restates the narrowest-layer rule for `target_layer` and the
// refusals for `writable`; those two restatements are the cross-check, and this
// is the guard, because a restatement cannot see the functions change.
//
// The three are not the registry's to answer: `owner` is the word the read prints
// for a key the generic path does not write, and it comes from
// settingsFamilyCommands -- a map from the registry's owner token to the command
// family a screen names; `writable` is the generic path's own decision for the
// key, asked of the write (D1102); and `target_layer` is settingsDefaultLayer's
// answer, the layer a write with no `--layer` lands in. projectRow sets all three
// that way and never from a second list beside them, so a fixture that answers
// any of them differently is a row whose edit button the window shows or
// withholds against the CLI's own answer (its rule is a row that is `writable`
// with a `targetLayer` that is not empty, C2).
//
// The console's own pin of the same values (test/unit/cli-stub.test.ts) restates
// the fixture's by hand; this is the tie to the CLI that keeps the fixture
// honest, so the drift C4 fixed -- `owner: "settings set"` and `writable: true`
// on rows no write produces, and `target_layer: "project"` on the row whose write
// lands in `repo` -- is red here rather than at a screen.
func TestSettingsConsoleFixtureOwnerAndWritable(t *testing.T) {
	const file = "../../console/test/fixtures/settings.json"
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Settings []struct {
			Key         string `json:"key"`
			Writable    bool   `json:"writable"`
			Owner       string `json:"owner"`
			TargetLayer string `json:"target_layer"`
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
		if want := settingsDefaultLayer(reg); row.TargetLayer != want {
			t.Errorf("%s: the fixture's target_layer is %q, and the read prints %q for the registry row's layers %v (settingsDefaultLayer, which projectRow copies)", row.Key, row.TargetLayer, want, reg.Layers)
		}
	}
}
