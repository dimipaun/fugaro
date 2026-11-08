package cli

// fugaro seats (subscriptions S4.1, rulings D439 and T14): the advisory seat
// estimate.
//
// The estimate is three sources merged, each identity counted once: the two
// lists this machine's config holds (terraform.operators and terraform.admins)
// and the requesters the window's runs recorded. Every
// test here pins lsNow, the clock the 90-day window is measured back from, and
// mints its run IDs through runIDAt, so none is date-lucky (runid_test.go's
// guard).

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// seatsOut is `fugaro seats --json`'s document as these tests read it.
type seatsOut struct {
	Version    int               `json:"version"`
	Project    string            `json:"project"`
	Days       int               `json:"days"`
	Since      string            `json:"since"`
	Until      string            `json:"until"`
	People     []seatsPersonOut  `json:"people"`
	NotCounted []seatsFlaggedOut `json:"not_counted"`
	Unreadable int               `json:"unreadable"`
	Notes      []string          `json:"notes"`
}

type seatsPersonOut struct {
	Person  string   `json:"person"`
	Sources []string `json:"sources"`
}

type seatsFlaggedOut struct {
	Member  string   `json:"member"`
	Why     string   `json:"why"`
	Sources []string `json:"sources"`
}

// pinSeatsClock pins lsNow to a fixed instant and puts the real clock back
// when the test ends. A test that seeds runs must call it: seats measures its
// 90-day window back from lsNow, so a run ID minted against the wall clock
// ages out of that window as the calendar moves.
func pinSeatsClock(t *testing.T) time.Time {
	t.Helper()
	at := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	old := lsNow
	lsNow = func() time.Time { return at }
	t.Cleanup(func() { lsNow = old })
	return at
}

// seatsDocOf reads the document out of `fugaro seats --json`'s stdout.
func seatsDocOf(t *testing.T, out string) seatsOut {
	t.Helper()
	var doc seatsOut
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("seats --json: %v\n%s", err, out)
	}
	return doc
}

// seatsSources is the row named name's sources as one line, "" when the
// document names no such row in either table.
func seatsSources(doc seatsOut, name string) string {
	for _, p := range doc.People {
		if p.Person == name {
			return strings.Join(p.Sources, ", ")
		}
	}
	for _, g := range doc.NotCounted {
		if g.Member == name {
			return strings.Join(g.Sources, ", ")
		}
	}
	return ""
}

// seatsWhy is the reason the document lists member as not counted, "" when it
// lists them as a person.
func seatsWhy(doc seatsOut, member string) string {
	for _, g := range doc.NotCounted {
		if g.Member == member {
			return g.Why
		}
	}
	return ""
}

func TestSeatsCountsOperatorsAdminsAndRequesters(t *testing.T) {
	at := pinSeatsClock(t)
	f := newCloudFixture(t)
	// The lists are written in the version-1 keys this fixture's config carries,
	// which fold to the roles' own words: v1's `launchers` IS this version's
	// operators and v1's `operators` its admins (localcfg.foldLegacyLists), so
	// the sources below read operator and admin.
	f.appendConfig(t, "terraform:\n  launchers: [user:alice@example.com, user:bob@example.com]\n"+
		"  operators: [user:bob@example.com, user:carol@example.com]\n")
	seedRun(t, f, runIDAt(3, "090000", "aaaa"), "", "dave@example.com", false)

	out, _, err := execute(t, "seats", "--json")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	doc := seatsDocOf(t, out)
	if doc.Version != seatsDocVersion {
		t.Errorf("version = %d, want %d: a source names the list it was read from, and version 1 named them with the pre-rename words", doc.Version, seatsDocVersion)
	}
	if strings.Contains(out, `"launcher"`) {
		t.Errorf("the document still carries the retired source name:\n%s", out)
	}
	if doc.Project != "aurora" {
		t.Errorf("project = %q, want %q", doc.Project, "aurora")
	}
	if doc.Days != 90 || doc.Until != at.Format("2006-01-02") || doc.Since != at.AddDate(0, 0, -90).Format("2006-01-02") {
		t.Errorf("window = %d days %s to %s, want 90 days %s to %s",
			doc.Days, doc.Since, doc.Until, at.AddDate(0, 0, -90).Format("2006-01-02"), at.Format("2006-01-02"))
	}
	// Two operators, two admins -- bob is on both lists -- and the requester
	// of the window's one run: four people, each counted once. A row's sources
	// name the list it was read from in the roles' own words, so carol, who is
	// on terraform.admins alone, is seen as an admin and not as an operator.
	if len(doc.People) != 4 {
		t.Fatalf("people = %+v, want alice, bob, carol and dave, once each", doc.People)
	}
	for _, want := range []struct{ person, sources string }{
		{"alice@example.com", "operator"},
		{"bob@example.com", "operator, admin"},
		{"carol@example.com", "admin"},
		{"dave@example.com", "requester"},
	} {
		if got := seatsSources(doc, want.person); got != want.sources {
			t.Errorf("%s: sources = %q, want %q", want.person, got, want.sources)
		}
	}
	if len(doc.NotCounted) != 0 {
		t.Errorf("not_counted = %+v, want nothing: both lists name people alone", doc.NotCounted)
	}
	if doc.Unreadable != 0 {
		t.Errorf("unreadable = %d, want 0: the run's task.json is there", doc.Unreadable)
	}
}

// TestSeatsFlagsGroups is the red step: an identity neither list can size is
// listed apart and counted nowhere -- a group: or domain: member names an
// unknown number of people, and an account is not a person at all.
func TestSeatsFlagsGroups(t *testing.T) {
	pinSeatsClock(t)
	f := newCloudFixture(t)
	f.appendConfig(t, "terraform:\n  launchers: [user:alice@example.com, group:eng@example.com, domain:example.org]\n"+
		"  operators: [group:eng@example.com, serviceAccount:ci@proj-1234.iam.gserviceaccount.com]\n")

	out, _, err := execute(t, "seats", "--json")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	doc := seatsDocOf(t, out)
	if len(doc.People) != 1 || doc.People[0].Person != "alice@example.com" {
		t.Fatalf("people = %+v, want alice alone: a group names an unknown number of people and is not one seat, and an account is not a person", doc.People)
	}
	if len(doc.NotCounted) != 3 {
		t.Fatalf("not_counted = %+v, want the group, the domain and the account", doc.NotCounted)
	}
	for _, want := range []struct{ member, why, sources string }{
		{"group:eng@example.com", "unknown_size", "operator, admin"},
		{"domain:example.org", "unknown_size", "operator"},
		{"serviceAccount:ci@proj-1234.iam.gserviceaccount.com", "account", "admin"},
	} {
		if got := seatsSources(doc, want.member); got != want.sources {
			t.Errorf("%s: sources = %q, want %q", want.member, got, want.sources)
		}
		if got := seatsWhy(doc, want.member); got != want.why {
			t.Errorf("%s: why = %q, want %q", want.member, got, want.why)
		}
	}

	human, _, err := execute(t, "seats")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	for _, want := range []string{
		"estimate: 1 person, plus 3 listed and not counted",
		"NOT COUNTED",
		"group:eng@example.com",
		"domain:example.org",
		"an unknown number of people",
		"not a person",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("seats does not say %q:\n%s", want, human)
		}
	}
}

// TestSeatsDeduplicatesPeople: one person is one row however many sources name
// them, and the lists and the runs spell an address independently -- the lists
// are read from IAM members a person was written to, the requester from what
// the launching CLI typed -- so the comparison folds case (user.go's
// sameUserMemberFold) rather than counting one person twice.
func TestSeatsDeduplicatesPeople(t *testing.T) {
	pinSeatsClock(t)
	f := newCloudFixture(t)
	f.appendConfig(t, "terraform:\n  launchers: [user:Alice@Example.com]\n"+
		"  operators: [user:alice@example.com]\n")
	seedRun(t, f, runIDAt(3, "090000", "aaaa"), "", "alice@example.com", false)
	seedRun(t, f, runIDAt(4, "090000", "bbbb"), "", "ALICE@EXAMPLE.COM", false)

	out, _, err := execute(t, "seats", "--json")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	doc := seatsDocOf(t, out)
	if len(doc.People) != 1 {
		t.Fatalf("people = %+v, want one row: one person, however many sources name them", doc.People)
	}
	if got := seatsSources(doc, doc.People[0].Person); got != "operator, admin, requester" {
		t.Errorf("%s: sources = %q, want all three sources on the one row", doc.People[0].Person, got)
	}
}

// TestSeatsReadsNinetyDays: the window is the last 90 days of runs, so a
// person whose only run is older than that is invisible unless a list names
// them. The run at 91 days is the discriminating one: a window read as "every
// run in the bucket" would count it.
func TestSeatsReadsNinetyDays(t *testing.T) {
	at := pinSeatsClock(t)
	f := newCloudFixture(t)
	seedRun(t, f, runIDAt(91, "090000", "aaaa"), "", "old@example.com", false)
	seedRun(t, f, runIDAt(89, "090000", "bbbb"), "", "recent@example.com", false)

	out, _, err := execute(t, "seats", "--json")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	doc := seatsDocOf(t, out)
	if doc.Days != 90 || doc.Since != at.AddDate(0, 0, -90).Format("2006-01-02") {
		t.Errorf("window = %d days from %s, want 90 days from %s", doc.Days, doc.Since, at.AddDate(0, 0, -90).Format("2006-01-02"))
	}
	if len(doc.People) != 1 || doc.People[0].Person != "recent@example.com" {
		t.Fatalf("people = %+v, want the requester of the run inside the 90 days alone", doc.People)
	}
}

// seatsTableRows is the rows of the table under head in a human seats
// output: the lines between that header and the next blank one.
func seatsTableRows(out, head string) []string {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, head) {
			continue
		}
		var rows []string
		for _, row := range lines[i+1:] {
			if strings.TrimSpace(row) == "" {
				break
			}
			rows = append(rows, row)
		}
		return rows
	}
	return nil
}

// seatsCells is a table row's cells, split where the tabwriter wrote its
// columns: the same reading runs_test.go's launcher-cell test makes.
func seatsCells(line string) []string {
	return regexp.MustCompile(`  +`).Split(strings.TrimRight(line, " "), -1)
}

// seatsPutRawTaskStores data as id's task.json, as it is: the object a build
// that cannot decode it reads. seedRun's own writer marshals a valid spec and
// cannot produce one.
func seatsPutRawTask(t *testing.T, f *cloudFixture, id, data string) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	if err := runstore.Open(b, appSlug, id).PutFile(ctx, "task.json", []byte(data), "application/json"); err != nil {
		t.Fatal(err)
	}
}

// TestSeatsSanitizesHostileValues: a member is text somebody typed into a
// config and a requester is text a run's own agent wrote, so both are
// untrusted and must clear the bar every other value this CLI prints clears:
// no tabwriter column or row of their own, no escape, bidi or line-separator
// rune, no unbounded width -- and the document carries the text the table
// does. Mutation caught: interpolating a member or a recorded requester into
// its cell as it is.
func TestSeatsSanitizesHostileValues(t *testing.T) {
	pinSeatsClock(t)
	f := newCloudFixture(t)
	bidi := string(rune(0x202e))
	f.appendConfig(t, fmt.Sprintf("terraform:\n  launchers: [\"user:%s@example.com\"]\n"+
		"  operators: [\"group:eng%s@example.com\"]\n", bidi, bidi))
	huge := strings.Repeat("x", 10_000)
	hostile := []string{
		"a\tb@example.com", "a\nb@example.com", "a\rb@example.com", "esc\x1b[2Jc@example.com",
		"bidi" + bidi + "evil@example.com",
		"line" + string(rune(0x2028)) + "sep@example.com",
		"para" + string(rune(0x2029)) + "graph@example.com",
		huge + "@example.com",
	}
	for i, who := range hostile {
		seedRun(t, f, runIDAt(i+1, "090000", fmt.Sprintf("%04x", i)), "", who, false)
	}

	out, _, err := execute(t, "seats")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	people, flagged := seatsTableRows(out, "PERSON"), seatsTableRows(out, "NOT COUNTED")
	// The operator list's one person, and the requesters: the three that
	// differ only in whitespace print alike and are one row, an ESC's CSI
	// sequence is dropped whole, and the rest are one each.
	if len(people) != 7 || len(flagged) != 1 {
		t.Fatalf("seats printed %d people and %d not-counted rows, want 7 and 1:\n%s", len(people), len(flagged), out)
	}
	for _, row := range people {
		if n := len(seatsCells(row)); n != 2 {
			t.Errorf("a hostile value opened a column of its own: %q has %d cells, want 2:\n%s", row, n, out)
		}
	}
	for _, row := range flagged {
		if n := len(seatsCells(row)); n != 3 {
			t.Errorf("a hostile value opened a column of its own: %q has %d cells, want 3:\n%s", row, n, out)
		}
	}
	for _, r := range out {
		switch {
		case r == '\x1b', r == '\r', r >= 0x202a && r <= 0x202e, r == 0x2028, r == 0x2029:
			t.Fatalf("hostile rune %U reached the table:\n%q", r, out)
		case r < 0x20 && r != '\t' && r != '\n':
			t.Fatalf("control rune %U reached the table:\n%q", r, out)
		}
	}
	for _, row := range people {
		if n := utf8.RuneCountInString(seatsCells(row)[0]); n > maxNameCellRunes+1 {
			t.Errorf("cell %q is %d runes, want at most %d: a value a source wrote is not printed whole", seatsCells(row)[0], n, maxNameCellRunes+1)
		}
	}
	if n := utf8.RuneCountInString(out); n > 4000 {
		t.Errorf("the table is %d runes, not bounded:\n%.200q", n, out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("the clipped value carries no mark that it was cut:\n%s", out)
	}
	if n := strings.Count(out, "a?b@example.com"); n != 1 {
		t.Errorf("the three whitespace variants became %d rows, want 1: two texts that print alike are one identity:\n%s", n, out)
	}

	// The document carries the same text the table does, sanitised and capped
	// the same way: a consumer of --json is as much a terminal as the table.
	jout, _, err := execute(t, "seats", "--json")
	if err != nil {
		t.Fatalf("seats --json: %v", err)
	}
	doc := seatsDocOf(t, jout)
	if len(doc.People) != 7 || len(doc.NotCounted) != 1 {
		t.Fatalf("the document holds %d people and %d not-counted rows, want 7 and 1", len(doc.People), len(doc.NotCounted))
	}
	for _, p := range doc.People {
		if n := utf8.RuneCountInString(p.Person); n > maxNameCellRunes+1 {
			t.Errorf("the document's person %q is %d runes, want at most %d", p.Person, n, maxNameCellRunes+1)
		}
		for _, r := range p.Person {
			if r == '\x1b' || r == '\t' || r == '\r' || r >= 0x202a && r <= 0x202e || r == 0x2028 || r == 0x2029 {
				t.Errorf("the document carried hostile rune %U in %q", r, p.Person)
			}
		}
	}
}

// TestSeatsReadsOnPastATaskItCannotDecode: one run's task.json this build
// cannot decode is that run's unreadable object, not the estimate's failure.
// The run's own agent can write that object (runs/<slug>/ is its account's),
// so a fatal read here would let a single run fail the command and print
// nothing for the whole window; runs ls reads the same object as one error
// row. The estimate is produced, the count is said, and whoever requested that
// run is counted nowhere. Mutation caught: returning the read error instead of
// counting it, or parsing with ReadTask and letting its error out.
func TestSeatsReadsOnPastATaskItCannotDecode(t *testing.T) {
	pinSeatsClock(t)
	f := newCloudFixture(t)
	seedRun(t, f, runIDAt(2, "090000", "aaaa"), "", "ok@example.com", false)
	// An unknown field: what a newer CLI's task.json looks like to this build,
	// which refuses unknown fields (task.Parse).
	id := runIDAt(3, "090000", "bbbb")
	seatsPutRawTask(t, f, id, `{"version":1,"run_id":"`+id+`","repo":"acme/app","ref":"main",`+
		`"workflow":"web","task":"x","requested_by":"ghost@example.com","future_field":1}`)

	out, _, err := execute(t, "seats", "--json")
	if err != nil {
		t.Fatalf("seats: %v (the estimate is produced with the unreadable run counted, not refused)", err)
	}
	doc := seatsDocOf(t, out)
	if doc.Unreadable != 1 {
		t.Errorf("unreadable = %d, want 1: the run whose task.json this build cannot decode", doc.Unreadable)
	}
	if len(doc.People) != 1 || doc.People[0].Person != "ok@example.com" {
		t.Fatalf("people = %+v, want the readable run's requester alone", doc.People)
	}
	if strings.Contains(out, "ghost@example.com") {
		t.Errorf("the unreadable run's requester reached the document: an object nobody can read names nobody:\n%s", out)
	}

	human, _, err := execute(t, "seats")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	for _, want := range []string{"estimate: 1 person", "1 run task of the window could not be read", "the estimate is a lower bound by that much"} {
		if !strings.Contains(human, want) {
			t.Errorf("seats does not say %q:\n%s", want, human)
		}
	}
	if strings.Contains(human, "ghost@example.com") {
		t.Errorf("the unreadable run's requester reached the table:\n%s", human)
	}
}
