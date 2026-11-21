// The mapping is tested from outside the package because one test of it is
// about the Cloud Scheduler's list (gcp.SchedulerRegion), which the package
// itself never reads.
package place_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/place"
)

// TestThePlaceOfARegion: the place is derived from the region alone (D3080),
// with no cloud call and no config. London and Zürich are the two `europe-*`
// regions the European Union does not cover, so they are in neither place;
// every other `europe-*` region is in the EU, every `us-*` one in the US, and
// everything else — Africa, Asia, Australia, the Middle East, Canada and
// South America — in neither.
func TestThePlaceOfARegion(t *testing.T) {
	for region, want := range map[string]place.Place{
		"us-east5":      place.US,
		"us-west8":      place.US,
		"us-somewhere1": place.US, // not one of Regions: a region Google adds is placed by its prefix
		"europe-west1":  place.EU,
		"europe-west10": place.EU,
		"europe-west2":  place.None, // London
		"europe-west6":  place.None, // Zürich

		"northamerica-northeast1": place.None,
		"northamerica-south1":     place.None,
		"southamerica-east1":      place.None,
		"me-west1":                place.None,
		"africa-south1":           place.None,
		"asia-southeast1":         place.None,
		"australia-southeast1":    place.None,
		"mars-north1":             place.None,
	} {
		if got := place.Of(region); got != want {
			t.Errorf("Of(%q) = %s; want %s", region, got, want)
		}
	}

	// The list and the derivation agree about every region: no `us-*` region
	// of Regions is outside the US, no `europe-*` one outside the EU except
	// the two exceptions above, and both exceptions are regions Fugaro
	// accepts.
	for _, r := range place.Regions {
		switch {
		case strings.HasPrefix(r, "us-"):
			if got := place.Of(r); got != place.US {
				t.Errorf("Of(%q) = %s; a us- region is in the US", r, got)
			}
		case strings.HasPrefix(r, "europe-") && r != "europe-west2" && r != "europe-west6":
			if got := place.Of(r); got != place.EU {
				t.Errorf("Of(%q) = %s; a europe- region is in the EU but for London and Zürich", r, got)
			}
		}
	}
	for _, r := range []string{"europe-west2", "europe-west6"} {
		if !slices.Contains(place.Regions, r) {
			t.Errorf("%s is not a region Fugaro accepts, so the two exceptions to the EU are not tested", r)
		}
	}
}

// TestTheMappingForUSAndEU: the mapping for a region of each place, each row
// in the region itself where it can be, and the first row that cannot (the
// Realtime Database, offered in three locations for the whole world) carrying
// its reason. The EU region with no region typed is europe-west1 (D3086),
// which is also the Realtime Database's only EU location, so an EU
// installation gets everything in one region with nothing to explain.
func TestTheMappingForUSAndEU(t *testing.T) {
	for p, want := range map[place.Place]string{place.US: "us-east5", place.EU: "europe-west1", place.None: ""} {
		if got := place.DefaultRegion(p); got != want {
			t.Errorf("DefaultRegion(%s) = %q; want %q", p, got, want)
		}
	}

	us := place.For(place.DefaultRegion(place.US))
	if us.Place != place.US {
		t.Errorf("For(us-east5).Place = %s; want US", us.Place)
	}
	for what, want := range map[string]place.Location{
		"Firestore":        {Where: "us-east5"},
		"RealtimeDatabase": {Where: "us-central1"}, // the one in the US
		"Secrets":          {Where: "us-east5"},
		"LogBucket":        {Where: "us"}, // Cloud Logging offers no bucket in us-east5
	} {
		got := map[string]place.Location{
			"Firestore": us.Firestore, "RealtimeDatabase": us.RealtimeDatabase,
			"Secrets": us.Secrets, "LogBucket": us.LogBucket,
		}[what]
		if got.Where != want.Where {
			t.Errorf("US: %s goes to %q; want %q", what, got.Where, want.Where)
		}
		// A reason is what a row that leaves the region has, and the only
		// row that leaves us-east5 for another region is the database.
		if (got.Reason != "") != (got.Where != us.Region) {
			t.Errorf("US: %s goes to %q with reason %q; want a reason exactly when it is not %s", what, got.Where, got.Reason, us.Region)
		}
	}

	eu := place.For(place.DefaultRegion(place.EU))
	for what, loc := range map[string]place.Location{
		"Firestore": eu.Firestore, "RealtimeDatabase": eu.RealtimeDatabase,
		"Secrets": eu.Secrets, "LogBucket": eu.LogBucket,
	} {
		if loc.Where != "europe-west1" || loc.Reason != "" {
			t.Errorf("EU: %s goes to %q (%q); want europe-west1 with no fallback", what, loc.Where, loc.Reason)
		}
	}

	// A region of each place that is not its default: Firestore, the secrets
	// and the log bucket are in the region itself, and only the Realtime
	// Database leaves it, with its reason.
	for _, r := range []string{"us-east1", "europe-west9"} {
		m := place.For(r)
		for what, loc := range map[string]place.Location{
			"Firestore": m.Firestore, "Secrets": m.Secrets, "LogBucket": m.LogBucket,
		} {
			if loc.Where != r || loc.Reason != "" {
				t.Errorf("%s: %s goes to %q (%q); want %q with no reason", r, what, loc.Where, loc.Reason, r)
			}
		}
		if m.RealtimeDatabase.Where == r || m.RealtimeDatabase.Reason == "" {
			t.Errorf("%s: the Realtime Database goes to %q (%q); want another location, with its reason", r, m.RealtimeDatabase.Where, m.RealtimeDatabase.Reason)
		}
		if !inPlace(m.RealtimeDatabase.Where, m.Place) {
			t.Errorf("%s: the Realtime Database goes to %q, outside %s", r, m.RealtimeDatabase.Where, m.Place)
		}
	}
}

// TestNoFallbackLeavesItsPlace: a US installation's data is in the US and an
// EU installation's in the EU, every region of the list, every row, the
// Cloud Scheduler's included (its place is the test
// TestEveryRegionOfAPlaceHasASchedulerLocationInIt). A region in neither
// place promises nothing and is not walked here.
func TestNoFallbackLeavesItsPlace(t *testing.T) {
	for _, r := range place.Regions {
		m := place.For(r)
		if m.Place == place.None {
			continue
		}
		for what, loc := range map[string]place.Location{
			"Firestore": m.Firestore, "RealtimeDatabase": m.RealtimeDatabase,
			"Secrets": m.Secrets, "LogBucket": m.LogBucket,
		} {
			if !inPlace(loc.Where, m.Place) {
				t.Errorf("%s of %s goes to %q, outside %s: %s", what, r, loc.Where, m.Place, loc.Reason)
			}
		}
	}
}

// inPlace reports whether a location lies in a place: a region of it, or the
// place's own multi-region (us, eu), which is what Cloud Logging calls a
// bucket that is not in one region.
func inPlace(location string, p place.Place) bool {
	switch location {
	case "us":
		return p == place.US
	case "eu":
		return p == place.EU
	}
	return place.Of(location) == p
}

// TestARegionInNeitherPlaceStatesItsFallbacks: D3081 keeps every region, and
// what a region in neither place gets is stated rather than promised. London
// and Zürich are in no place though their own region holds Firestore, a
// logging bucket and the secrets; Bangkok and Johannesburg are in no place
// and hold nothing but the secrets, so every other row is a fallback that
// carries its reason.
func TestARegionInNeitherPlaceStatesItsFallbacks(t *testing.T) {
	for _, r := range []string{"europe-west2", "europe-west6"} {
		m := place.For(r)
		if m.Place != place.None {
			t.Errorf("For(%s).Place = %s; want none", r, m.Place)
		}
		for what, want := range map[string]string{
			"Firestore": r, "Secrets": r, "LogBucket": r,
		} {
			got := map[string]place.Location{
				"Firestore": m.Firestore, "Secrets": m.Secrets, "LogBucket": m.LogBucket,
			}[what]
			if got.Where != want || got.Reason != "" {
				t.Errorf("%s: %s goes to %q (%q); want the region itself", r, what, got.Where, got.Reason)
			}
		}
		if m.RealtimeDatabase.Where != "europe-west1" || m.RealtimeDatabase.Reason == "" {
			t.Errorf("%s: the Realtime Database goes to %q (%q); want europe-west1, with its reason", r, m.RealtimeDatabase.Where, m.RealtimeDatabase.Reason)
		}
	}

	for _, c := range []struct {
		region string
		rows   map[string]string
	}{
		{"asia-southeast3", map[string]string{
			"Firestore": "us-east5", "RealtimeDatabase": "asia-southeast1", "LogBucket": "global"}},
		{"africa-south1", map[string]string{
			"Firestore": "us-east5", "RealtimeDatabase": "europe-west1", "LogBucket": "global"}},
	} {
		m := place.For(c.region)
		if m.Place != place.None {
			t.Errorf("For(%s).Place = %s; want none", c.region, m.Place)
		}
		if m.Secrets.Where != c.region || m.Secrets.Reason != "" {
			t.Errorf("%s: the secrets go to %q (%q); a container created from now on is replicated in the region", c.region, m.Secrets.Where, m.Secrets.Reason)
		}
		for what, want := range c.rows {
			got := map[string]place.Location{
				"Firestore": m.Firestore, "RealtimeDatabase": m.RealtimeDatabase, "LogBucket": m.LogBucket,
			}[what]
			if got.Where != want {
				t.Errorf("%s: %s goes to %q; want %q", c.region, what, got.Where, want)
			}
			if got.Reason == "" {
				t.Errorf("%s: %s goes to %q with no reason; a fallback outside the place is stated", c.region, what, got.Where)
			}
		}
	}

	// Johannesburg is not a region of the list either: Of and For answer for
	// a region Fugaro does not carry, which is what lets an installation be
	// set up in a region Google adds after this list was read.
	if slices.Contains(place.Regions, "africa-south1") {
		t.Errorf("africa-south1 is a region of Regions now: the case above tests a region the list does not carry")
	}
}

// TestTheListsAreDated: the lists are Google's, so they carry the date they
// were read and the page each came from, and docs/location-preference/
// locations.md is the same lists for a person to read. The document is
// written one location per line, in the code's order, so the test compares
// the whole block rather than its entries.
func TestTheListsAreDated(t *testing.T) {
	when, err := time.Parse("2006-01-02", place.ListsReadOn)
	if err != nil {
		t.Fatalf("place.ListsReadOn = %q, which is not a date: %v", place.ListsReadOn, err)
	}
	if when.After(time.Now()) {
		t.Errorf("place.ListsReadOn = %s, which is in the future", place.ListsReadOn)
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "location-preference", "locations.md"))
	if err != nil {
		t.Fatalf("the document the lists are read from: %v", err)
	}
	doc := string(raw)
	if !strings.Contains(doc, place.ListsReadOn) {
		t.Errorf("docs/location-preference/locations.md does not carry the date the lists were read (%s)", place.ListsReadOn)
	}
	for _, l := range []struct {
		name string
		list []string
		page string
	}{
		{"Regions", place.Regions, "cloud.google.com/run/docs/locations"},
		{"FirestoreLocations", place.FirestoreLocations, "firebase.google.com/docs/firestore/locations"},
		{"LoggingLocations", place.LoggingLocations, "cloud.google.com/logging/docs/region-support"},
	} {
		// A list that moves in one place and not the other is the failure
		// this test exists for: the copy a person reads would otherwise
		// quietly stop being the list the mapping answers from.
		if !strings.Contains(doc, strings.Join(l.list, "\n")) {
			t.Errorf("docs/location-preference/locations.md does not carry %s as the code does: its %d entries, one per line, in this order", l.name, len(l.list))
		}
		if !strings.Contains(doc, l.page) {
			t.Errorf("docs/location-preference/locations.md does not name %s, the page %s was read from", l.page, l.name)
		}
		// A location named twice is a document a reader counts twice, and it
		// would move under the join above without the list changing length.
		sorted := slices.Clone(l.list)
		slices.Sort(sorted)
		if dup := slices.Compact(sorted); len(dup) != len(l.list) {
			t.Errorf("%s names a location twice (%d entries, %d distinct)", l.name, len(l.list), len(dup))
		}
	}
	if len(place.Regions) == 0 || len(place.FirestoreLocations) == 0 || len(place.LoggingLocations) == 0 {
		t.Error("a list is empty: a package with no list answers from nothing")
	}
}

// TestEveryRegionOfAPlaceHasASchedulerLocationInIt: Cloud Scheduler is not
// offered in every Cloud Run region, and gcp.SchedulerRegion answers a fixed
// nearby one where it is not (F3). Every region Fugaro accepts has an answer
// at all — europe-west10 and southamerica-west1 are the two the table lacked
// — and a region of a place is given a Scheduler location of that same place,
// so a US job's daily check is scheduled from the US and an EU job's from the
// EU.
func TestEveryRegionOfAPlaceHasASchedulerLocationInIt(t *testing.T) {
	for _, r := range place.Regions {
		loc, err := gcp.SchedulerRegion(r)
		if err != nil {
			t.Errorf("SchedulerRegion(%q): %v", r, err)
			continue
		}
		if p := place.Of(r); p != place.None {
			if got := place.Of(loc); got != p {
				t.Errorf("SchedulerRegion(%q) = %q, in %s; want a location in %s", r, loc, got, p)
			}
		}
	}
}
