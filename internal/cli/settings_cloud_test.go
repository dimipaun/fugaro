package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// settingsCloudRowKeys are the registry's keys whose only store is the
// Realtime Database — `caps.*` and `kill.*` (spec §2.1, §2.2). They are named
// here rather than derived from the registry's layer list, so a test that says
// "every cloud row says it was not read" is a claim about the rows a person
// sees and not about a predicate the read could satisfy vacuously.
var settingsCloudRowKeys = []string{
	"caps.mode",
	"caps.global.per_run_usd",
	"caps.global.per_day_usd",
	"caps.defaults.per_run_usd",
	"caps.defaults.per_day_usd",
	"caps.max_reserve_usd",
	"kill.global.on",
}

// settingsCloudFixture is a machine whose project has a budget backend: the
// fake Realtime Database newBudgetFixture wires into the project config, with
// the rules marker deployed. The marker is what decides whether this
// installation has a policy node at all (projectpolicy.Read answers "not
// found" under a rules generation older than PolicyRulesVersion), and a test
// about the *published* node has to be able to reach one.
func settingsCloudFixture(t *testing.T) *budgetFixture {
	t.Helper()
	f := newBudgetFixture(t, "")
	f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	return f
}

// settingsCloudPolicyNode is a published policy node: the version this Fugaro
// writes, and the budget block the caller names. Its mode is the one
// newBudgetFixture writes into the local project config (`observe`), so a node
// built here differs from that config in the caller's own members and nothing
// else.
func settingsCloudPolicyNode(mode string, perRunUSD float64) map[string]any {
	return map[string]any{
		"version": projectpolicy.Version,
		"budget":  map[string]any{"mode": mode, "per_run_usd": perRunUSD},
	}
}

// settingsCloudRowUnread asserts a cloud row says the database was not read:
// the STATE column carries the reason, and nothing else about the row claims a
// value. The row keeps the shape a key no layer decided has — the default value
// with WINS default (spec §2.3), which is the shape the repository scope's
// `unknown` prints — because `unread` is a state and not a value: a word in the
// VALUE column is a non-value where a consumer reads one, and the caps layer
// claimed for it would put seven rows under --changed on every installation
// whose cloud is out of reach.
func settingsCloudRowUnread(t *testing.T, doc settingsTestDoc, key string) {
	t.Helper()
	row := rowOf(t, doc, key, "project")
	if !strings.HasPrefix(row.State, "unread: ") {
		t.Errorf("%s: state = %q, want a reason", key, row.State)
	}
	if row.Wins != "default" {
		t.Errorf("%s: wins = %q, want default: no layer was read to decide it", key, row.Wins)
	}
	want := row.Default.Value
	if want == "" {
		want = row.Default.Unset
	}
	if deref(row.Value) != want {
		t.Errorf("%s: value = %q, want the default (%q): a row nobody read is not guessed", key, deref(row.Value), want)
	}
}

// TestSettingsUnreadIsNeverADefault: a cloud layer that was not read says so,
// in the STATE column, with the reason — and a row that says `unread` is never
// the same row as one whose layer was read and holds nothing, which is `ok`.
// Two reasons, because they are answered by different code: a project with no
// budget backend at all, which must not reach for a database, and a database
// that refuses the read.
func TestSettingsUnreadIsNeverADefault(t *testing.T) {
	settingsFixture(t, "")
	none := readSettingsJSON(t)
	for _, key := range settingsCloudRowKeys {
		settingsCloudRowUnread(t, none, key)
	}
	row := rowOf(t, none, "caps.mode", "project")
	if !strings.Contains(row.State, "budget.rtdb_url") {
		t.Errorf("state = %q, want the reason to name the key that is not set", row.State)
	}
	// The other half of the word: an installation whose database is reachable
	// and holds no cap reads `ok` on the same rows, so `unread` is a fact about
	// this read and not the way these rows always look.
	settingsCloudFixture(t)
	empty := readSettingsJSON(t)
	for _, key := range settingsCloudRowKeys {
		if got := rowOf(t, empty, key, "project").State; got != "ok" {
			t.Errorf("%s: state = %q on a database that holds no cap, want ok", key, got)
		}
	}

	f := settingsCloudFixture(t)
	f.db.DenyRead(budget.PathCapsGlobal)
	refused := readSettingsJSON(t)
	for _, key := range settingsCloudRowKeys {
		settingsCloudRowUnread(t, refused, key)
	}
	row = rowOf(t, refused, "caps.mode", "project")
	if !strings.Contains(row.State, "refused the read") {
		t.Errorf("state = %q, want the refusal dbErr words for a denied read", row.State)
	}
}

// TestSettingsUnreadRowsAreNotChanges: --changed is the one-screen answer to
// "what have I changed on this installation", and it lists the rows a layer
// decided (its own help text). A row whose layer could not be read was decided
// by no layer, so it is not in that answer: every installation with no budget
// backend, and every read with --offline, would otherwise open with seven
// changes nobody made.
func TestSettingsUnreadRowsAreNotChanges(t *testing.T) {
	settingsFixture(t, "")
	for _, args := range [][]string{{"--changed"}, {"--changed", "--offline"}} {
		doc := readSettingsJSON(t, args...)
		for _, row := range doc.Settings {
			if settingsCapsKey(row.Key) {
				t.Errorf("%s: --changed lists %s (state %q, wins %q)", strings.Join(args, " "), row.Key, row.State, row.Wins)
			}
		}
	}
}

// TestSettingsMarksAnUnpublishedLocalValue: `unpublished` is the state the
// command exists for (SD3) — the local file holds a value the published node
// does not, so the edit reaches runs on the next `fugaro update` — and it is
// claimed only where the cloud side was read and the two really differ. The
// second half is the read after that update: the node carries what the file
// says, and the row reads ok with the node as the level a run is admitted by
// (the converge loop S11 pins: publish, read `unpublished`, update, read `ok`
// and `WINS policy`).
func TestSettingsMarksAnUnpublishedLocalValue(t *testing.T) {
	f := settingsCloudFixture(t)

	// The node's cap is the looser one, so the file's value is the one a run
	// gets and the state is what says it is not published yet.
	f.db.Set(projectpolicy.PathShared(), settingsCloudPolicyNode("observe", 100))
	row := rowOf(t, readSettingsJSON(t, "budget.per_run_usd"), "budget.per_run_usd", "project")
	if row.State != "unpublished" {
		t.Errorf("state = %q, want unpublished: the file's 5 is not what the node carries", row.State)
	}
	if deref(row.Value) != "5" {
		t.Errorf("value = %q, want the file's 5: the node's cap is the looser one", deref(row.Value))
	}
	if row.Wins != "local" {
		t.Errorf("wins = %q, want local: the value is the local file's", row.Wins)
	}

	// Published: the node carries the file's own value, so nothing is waiting
	// for an update, and the level a run is held to is named.
	f.db.Set(projectpolicy.PathShared(), settingsCloudPolicyNode("observe", 5))
	row = rowOf(t, readSettingsJSON(t, "budget.per_run_usd"), "budget.per_run_usd", "project")
	if row.State != "ok" {
		t.Errorf("state = %q, want ok: the node carries what the file says", row.State)
	}
	if deref(row.Value) != "5" || row.Wins != "policy" {
		t.Errorf("value/wins = %q/%q, want 5/policy: the node is the level a run is admitted by", deref(row.Value), row.Wins)
	}
}

// TestSettingsWhyShowsTheIgnoredLooserValue: the VALUE column is the value a
// run gets and not the raw value of any one layer (D849), so a cap a higher
// level clamps is printed as the clamped figure with the level that clamped it
// in WINS, and --why carries the value that lost and says why (spec §2.2,
// F17: `config show` reports a repository's 50 as source `repo` while the run
// is held to 10). Both directions are here, because only the tighter of the
// two ever decides: the node's cap looser than the file's, and the node's
// tighter than the checkout's.
func TestSettingsWhyShowsTheIgnoredLooserValue(t *testing.T) {
	f := settingsCloudFixture(t)
	f.db.Set(projectpolicy.PathShared(), settingsCloudPolicyNode("observe", 10))
	settingsCheckout(t, f.dir, "acme/app", settingsRepoYAML("budget:\n  per_run_usd: 50\n"))

	doc := readSettingsJSON(t, "budget.per_run_usd", "--all-repos")
	project := rowOf(t, doc, "budget.per_run_usd", "project")
	if deref(project.Value) != "5" || project.Wins != "local" {
		t.Errorf("the project scope = %q/%q, want the file's 5 decided by local: the node's 10 is the looser cap", deref(project.Value), project.Wins)
	}
	if len(project.Levels) != 2 || project.Levels[0].Layer != "local" || project.Levels[0].Outcome != "wins" ||
		project.Levels[1].Layer != "policy" || project.Levels[1].Value != "10" ||
		project.Levels[1].Outcome != "ignored (looser than local)" {
		t.Errorf("the project scope's levels = %+v, want the file's 5 winning and the node's 10 ignored", project.Levels)
	}

	repo := rowOf(t, doc, "budget.per_run_usd", "repo:acme/app")
	if deref(repo.Value) != "10" || repo.Wins != "policy" {
		t.Errorf("the repository scope = %q/%q, want the node's 10 decided by policy: the checkout's 50 is clamped", deref(repo.Value), repo.Wins)
	}
	if len(repo.Levels) != 2 || repo.Levels[0].Layer != "policy" || repo.Levels[0].Outcome != "wins" ||
		repo.Levels[1].Layer != "repo" || repo.Levels[1].Value != "50" ||
		repo.Levels[1].Outcome != "ignored (looser than policy)" {
		t.Errorf("the repository scope's levels = %+v, want the node's 10 winning and the checkout's 50 ignored", repo.Levels)
	}

	// The table is where a person reads it, so the same sub-row is checked
	// there: the column layout is the renderer's, and the sentence is not.
	out, _, err := execute(t, "settings", "budget.per_run_usd", "--why", "--all-repos")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "ignored (looser than policy)") {
		t.Errorf("--why prints no clamp:\n%s", out)
	}
}

// TestSettingsCapsRowsComeFromTheBudgetReader: `caps.*` and `kill.*` are the
// Realtime Database's own keys (spec §2.2) — the mode `fugaro budget set
// --mode` writes, the two cap scopes every lease is compared against, the
// reserve a run may hold and the global switch — so their rows are answered by
// the reader the budget commands use and by nothing else. `caps.mode` is read
// beside `budget.mode`: SD6 keeps the two apart, and a read that answered one
// with the other's store would print the database's mode as the ceiling the
// policy node carries.
func TestSettingsCapsRowsComeFromTheBudgetReader(t *testing.T) {
	f := settingsCloudFixture(t)
	f.db.Set(budget.PathMode, "enforce")
	f.db.Set(budget.PathCapsGlobal, map[string]any{"dailyMicros": 40 * usd1, "perRunMicros": 7 * usd1})
	f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 3 * usd1, "repoPerRunMicros": 2 * usd1})
	f.db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 9 * usd1})
	f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "someone@example.com", "reason": "spend"})

	doc := readSettingsJSON(t)
	for key, want := range map[string]string{
		"caps.mode":                 "enforce",
		"caps.global.per_day_usd":   "$40",
		"caps.global.per_run_usd":   "$7",
		"caps.defaults.per_day_usd": "$3",
		"caps.defaults.per_run_usd": "$2",
		"caps.max_reserve_usd":      "$9",
		"kill.global.on":            "true",
	} {
		row := rowOf(t, doc, key, "project")
		if deref(row.Value) != want || row.Wins != "caps" || row.State != "ok" {
			t.Errorf("%s = %q/%q/%q, want %q decided by caps", key, deref(row.Value), row.Wins, row.State, want)
		}
	}

	// The ceiling's mode is a key of its own and holds what the local file
	// says, unpublished because the project has published no policy node.
	mode := rowOf(t, doc, "budget.mode", "project")
	if deref(mode.Value) != "observe" || mode.Wins != "local" || mode.State != "unpublished" {
		t.Errorf("budget.mode = %q/%q/%q, want the local file's observe, unpublished: the database's mode is caps.mode", deref(mode.Value), mode.Wins, mode.State)
	}
}

// TestSettingsCloudLayersAreBoundedInTime: a source that accepts the request
// and never answers — the database that drops packets rather than refusing —
// must not hold the table: the read bounds its cloud half (spec §2.7, the
// five-second precedent accounts ls sets) and says on the row that it was cut
// off, naming the bound rather than leaving the reader to guess whether the
// command hung or the installation holds no caps.
//
// The elapsed time is the assertion: internal/rtdb's per-call timeout is far
// longer, so the threshold below is wide enough for a loaded machine and far
// below the wait an unbounded read would take. The policy layer answers in the
// same read, because the two sources are separate reads with separate states.
func TestSettingsCloudLayersAreBoundedInTime(t *testing.T) {
	f := settingsCloudFixture(t)
	f.db.Set(projectpolicy.PathShared(), settingsCloudPolicyNode("observe", 5))
	_, release := f.db.HoldNext(budget.PathMode)
	defer release()

	start := time.Now()
	doc := readSettingsJSON(t)
	took := time.Since(start)
	if took > 15*time.Second {
		t.Fatalf("the read took %s on a source that never answered, want it bounded well below the per-call timeout", took)
	}

	row := rowOf(t, doc, "caps.mode", "project")
	if !strings.HasPrefix(row.State, "unread: ") {
		t.Errorf("state = %q, want the source that never answered marked unread", row.State)
	}
	if !strings.Contains(row.State, "no answer within 5s") {
		t.Errorf("state = %q, want the reason to name the bound the read was cut off at", row.State)
	}
	published := rowOf(t, doc, "budget.per_run_usd", "project")
	if published.Wins != "policy" || published.State != "ok" {
		t.Errorf("budget.per_run_usd = %q/%q, want the node's value and ok: one source that did not answer does not make the other unread", published.Wins, published.State)
	}
}
