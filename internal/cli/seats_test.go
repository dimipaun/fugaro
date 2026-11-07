package cli

// fugaro seats (subscriptions S4.1, rulings D439 and T14): the advisory seat
// estimate.
//
// The estimate is three sources merged, each identity counted once: the two
// lists this machine's config holds (terraform.launchers and
// terraform.operators) and the requesters the window's runs recorded. Every
// test here pins lsNow, the clock the 90-day window is measured back from, and
// mints its run IDs through runIDAt, so none is date-lucky (runid_test.go's
// guard).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// seatsOut is `fugaro seats --json`'s document as these tests read it.
type seatsOut struct {
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

func TestSeatsCountsLaunchersOperatorsAndRequesters(t *testing.T) {
	at := pinSeatsClock(t)
	f := newCloudFixture(t)
	f.appendConfig(t, "terraform:\n  launchers: [user:alice@example.com, user:bob@example.com]\n"+
		"  operators: [user:bob@example.com, user:carol@example.com]\n")
	seedRun(t, f, runIDAt(3, "090000", "aaaa"), "", "dave@example.com", false)

	out, _, err := execute(t, "seats", "--json")
	if err != nil {
		t.Fatalf("seats: %v", err)
	}
	doc := seatsDocOf(t, out)
	if doc.Project != "aurora" {
		t.Errorf("project = %q, want %q", doc.Project, "aurora")
	}
	if doc.Days != 90 || doc.Until != at.Format("2006-01-02") || doc.Since != at.AddDate(0, 0, -90).Format("2006-01-02") {
		t.Errorf("window = %d days %s to %s, want 90 days %s to %s",
			doc.Days, doc.Since, doc.Until, at.AddDate(0, 0, -90).Format("2006-01-02"), at.Format("2006-01-02"))
	}
	// Two launchers, two operators -- bob is on both lists -- and the
	// requester of the window's one run: four people, each counted once.
	if len(doc.People) != 4 {
		t.Fatalf("people = %+v, want alice, bob, carol and dave, once each", doc.People)
	}
	for _, want := range []struct{ person, sources string }{
		{"alice@example.com", "launcher"},
		{"bob@example.com", "launcher, operator"},
		{"carol@example.com", "operator"},
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
		{"group:eng@example.com", "unknown_size", "launcher, operator"},
		{"domain:example.org", "unknown_size", "launcher"},
		{"serviceAccount:ci@proj-1234.iam.gserviceaccount.com", "account", "operator"},
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
	if got := seatsSources(doc, doc.People[0].Person); got != "launcher, operator, requester" {
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
