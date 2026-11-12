package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// TestSettingsConsoleFixtureOwnerAndWritable holds the three members of the
// console's settings fixture whose answers are this package's own to the read
// itself, rather than to a restatement of what the read does: `owner`,
// `writable` and `target_layer`, the members of the pencil's own rule (C2).
//
// The console's hermetic CLI stub answers `fugaro settings --json` with
// console/test/fixtures/settings.json, and the e2e spec draws that document as
// the CLI's own read (the settings-edit plan's C4, D1118), so a member the
// fixture spells differently from the CLI is a screen drawn from a document no
// read prints. TestSettingsConsoleFixture (schemas/schemas_test.go) holds the
// document to the schema, to the registry's defaults, to the rules its own
// members keep with each other, and to `sensitive`, whose answer is in the
// registry; that half cannot ask the read, because the read is this package's.
//
// The three are not the registry's to answer and not a second list's either:
// projectRow computes them, `owner` through settingsOwnerCommands -- the read's
// own table from the registry's owner token to the command or the sentence a
// screen names (D1178) -- and the blanking rule beside it for a row the write
// refuses, `writable` by asking the write (D1102), and `target_layer` through
// settingsDefaultLayer. So the guard runs the read on the fixture home and
// compares the fixture's row to the row the read prints for the same key: a
// fixture member that differs is a row whose edit button the window shows or
// withholds against the CLI's own answer (its rule is a row that is `writable`
// with a `targetLayer` that is not empty, C2), and comparing against the parts
// instead of the whole is how the blanking rule went unseen -- a row spelled
// `writable: false` beside `owner: "fugaro settings set"` matched the table's
// entry for the default token and the write's own refusal, and only the two
// together say the read prints no owner at all for it (D1178, D1102).
//
// The console's own pin of the same values (test/unit/cli-stub.test.ts) restates
// the fixture's by hand; this is the tie to the CLI that keeps the fixture
// honest, so the drift C4 fixed -- `owner: "settings set"` and `writable: true`
// on rows no write produces, and `target_layer: "project"` on the row whose write
// lands in `repo` -- is red here rather than at a screen.
func TestSettingsConsoleFixtureOwnerAndWritable(t *testing.T) {
	settingsWriteHome(t)
	read := readSettingsWritability(t)

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
		got, ok := read[row.Key+"\x00project"]
		if !ok {
			t.Errorf("%s: the read prints no project row for it, and every row of the fixture is one `settings --json` prints (the registry's own rows, which no other document carries)", row.Key)
			continue
		}
		if got.Owner == nil || *got.Owner != row.Owner {
			t.Errorf("%s: the fixture's owner is %q, and the read prints %q for it (settingsOwnerCommands through projectRow, D1178)", row.Key, row.Owner, settingsMemberText(got.Owner))
		}
		if got.Writable == nil || *got.Writable != row.Writable {
			t.Errorf("%s: the fixture says writable = %v, and the read prints %q for it (the write's own answer, D1102)", row.Key, row.Writable, settingsMemberText(got.Writable))
		}
		if got.TargetLayer == nil || *got.TargetLayer != row.TargetLayer {
			t.Errorf("%s: the fixture's target_layer is %q, and the read prints %q for it (settingsDefaultLayer through projectRow, C2)", row.Key, row.TargetLayer, settingsMemberText(got.TargetLayer))
		}
	}
}

// settingsMemberText renders one of those members in a failure message: what
// the read printed, or the fact that it printed none — a member it omits is not
// the empty member, and the two would otherwise read alike.
func settingsMemberText[T any](v *T) string {
	if v == nil {
		return "no member"
	}
	return fmt.Sprintf("%v", *v)
}
