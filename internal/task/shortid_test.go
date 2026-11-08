package task

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// shortidCaseFile is the case table of the short-ID rule. The console helper's
// own copy is compared with this file (SR10), so the cases are read from it
// rather than written a second time here: a case the table does not carry is a
// case the console is not held to.
const shortidCaseFile = "testdata/shortid_cases.json"

// The closed vocabulary of the table's display cases, one name per test below.
// A kind with no case fails the test that wants it rather than passing it in
// silence.
const (
	kindUnique         = "unique"
	kindCollision      = "collision"
	kindDash           = "dash"
	kindSameIDTwoRepos = "same_id_two_repos"
)

type shortIDCases struct {
	// MinEnding is the table's own copy of the minimum, so the console's copy
	// of the rule is pinned to the Go constant and not only to the cases.
	MinEnding int           `json:"min_ending"`
	Short     []shortCase   `json:"short"`
	Display   []displayCase `json:"display"`
}

type shortCase struct {
	ID    string `json:"id"`
	Short string `json:"short"`
}

type displayCase struct {
	Kind string       `json:"kind"`
	Refs []displayRef `json:"refs"`
}

type displayRef struct {
	Slug  string `json:"slug"`
	RunID string `json:"run_id"`
	Shown string `json:"shown"`
}

func loadShortIDCases(t *testing.T) shortIDCases {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "shortid_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c shortIDCases
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("%s: %v", shortidCaseFile, err)
	}
	if c.MinEnding != MinEnding {
		t.Fatalf("%s: min_ending = %d, want MinEnding = %d", shortidCaseFile, c.MinEnding, MinEnding)
	}
	return c
}

// ShortRun is the short form every surface shows: the last four characters, and
// the whole string when it is shorter than that.
func TestShortRun(t *testing.T) {
	cases := loadShortIDCases(t).Short
	if len(cases) == 0 {
		t.Fatalf("%s: no short case", shortidCaseFile)
	}
	for _, c := range cases {
		if got := ShortRun(c.ID); got != c.Short {
			t.Errorf("ShortRun(%q) = %q, want %q", c.ID, got, c.Short)
		}
		if n := utf8.RuneCountInString(ShortRun(c.ID)); n > MinEnding {
			t.Errorf("ShortRun(%q) = %d characters, longer than MinEnding", c.ID, n)
		}
	}
}

// Two runs shown together whose four characters are equal are shown with as
// many extra characters as it takes to tell them apart.
func TestDisplayRunsCollision(t *testing.T) {
	for _, c := range displayCases(t, kindUnique, kindCollision) {
		checkDisplayCase(t, c)
	}

	// The pair the plan's red step names, spelled out: both runs end in 3bb2
	// and in the dash before it, so each is shown with six characters — the
	// seconds digit the two do not share.
	a := Ref{Slug: "acme-app", RunID: "20261108-115248-3bb2"}
	b := Ref{Slug: "acme-app", RunID: "20261107-010101-3bb2"}
	got := DisplayRuns([]Ref{a, b})
	if got[a] != "8-3bb2" || got[b] != "1-3bb2" {
		t.Fatalf("DisplayRuns = %q/%q, want 8-3bb2/1-3bb2", got[a], got[b])
	}
}

// The extension crosses the dash and stays a valid ending: three runs sharing
// both their four hex digits and the dash need the seconds digit too, and every
// ending the rule shows is one EndsWith accepts (checkDisplayCase).
func TestDisplayRunsCrossesTheDash(t *testing.T) {
	for _, c := range displayCases(t, kindDash) {
		checkDisplayCase(t, c)
		for _, r := range c.Refs {
			if !strings.Contains(r.Shown, "-") {
				t.Errorf("%s: %s/%s is shown as %q, which does not cross the dash", c.Kind, r.Slug, r.RunID, r.Shown)
			}
		}
	}
}

// One run ID can exist in two repositories. No ending tells those rows apart,
// so each row of such a pair is shown with its repository in front of the
// ending; a row whose ID appears once is shown without one.
func TestDisplayRunsSameIDTwoRepos(t *testing.T) {
	for _, c := range displayCases(t, kindSameIDTwoRepos) {
		checkDisplayCase(t, c)
		count := map[string]int{}
		for _, r := range c.Refs {
			count[r.RunID]++
		}
		for _, r := range c.Refs {
			prefixed := strings.HasPrefix(r.Shown, r.Slug+"/")
			if prefixed != (count[r.RunID] > 1) {
				t.Errorf("%s: %s/%s is shown as %q, with its repository only when its ID is another row's too", c.Kind, r.Slug, r.RunID, r.Shown)
			}
		}
	}
}

// An ending shorter than four characters is refused even when it is unique, and
// even when it is the whole of a string too short to have a short form; the
// full ID and every longer tail are accepted.
func TestEndsWithMinimum(t *testing.T) {
	const id = "20261108-115248-3bb2"
	for _, c := range []struct {
		id, ending string
		want       bool
	}{
		{id, "3bb2", true},                       // the short form itself
		{id, "-3bb2", true},                      // one character past it, through the dash
		{id, "8-3bb2", true},                     // the collision extension
		{id, id, true},                           // the full ID is its own ending
		{id, "bb2", false},                       // below the minimum, though unique
		{id, "2", false},                         // below the minimum
		{id, "", false},                          // no ending at all
		{id, "a1c4", false},                      // the minimum length, another run's tail
		{id, "20261108-115248-3bb3", false},      // a near miss
		{"abc", "abc", false},                    // the whole of a too-short string is no ending
		{"20261108-115248-3bb2x", "3bb2", false}, // an ending is the tail, not a prefix of it
	} {
		if got := EndsWith(c.id, c.ending); got != c.want {
			t.Errorf("EndsWith(%q, %q) = %v, want %v", c.id, c.ending, got, c.want)
		}
	}
}

// displayCases is the table's display cases of the given kinds, and a failure
// when a kind has none: a renamed or dropped kind would otherwise leave its
// test green over an empty list.
func displayCases(t *testing.T, kinds ...string) []displayCase {
	t.Helper()
	want := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	var out []displayCase
	for _, c := range loadShortIDCases(t).Display {
		if want[c.Kind] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: no display case of kind %v", shortidCaseFile, kinds)
	}
	return out
}

// checkDisplayCase holds one case to the three properties every one of them
// has: the shown text, that no two rows of the set show the same text, and that
// the ending shown is one EndsWith accepts — so an extension that ran past the
// ID, or stopped short of telling the rows apart, fails here.
func checkDisplayCase(t *testing.T, c displayCase) {
	t.Helper()
	refs := make([]Ref, 0, len(c.Refs))
	for _, r := range c.Refs {
		refs = append(refs, Ref{Slug: r.Slug, RunID: r.RunID})
	}
	got := DisplayRuns(refs)
	seen := make(map[string]string, len(refs))
	for i, r := range c.Refs {
		shown := got[refs[i]]
		if shown != r.Shown {
			t.Errorf("%s: %s/%s is shown as %q, want %q", c.Kind, r.Slug, r.RunID, shown, r.Shown)
		}
		if prev, dup := seen[shown]; dup {
			t.Errorf("%s: %s and %s/%s show the same %q", c.Kind, prev, r.Slug, r.RunID, shown)
		}
		seen[shown] = r.Slug + "/" + r.RunID
		ending := strings.TrimPrefix(shown, r.Slug+"/")
		if !EndsWith(r.RunID, ending) {
			t.Errorf("%s: %s is shown as %q, and %q is no ending of %s", c.Kind, r.RunID, shown, ending, r.RunID)
		}
	}
}
