package infra

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/place"
)

// fsTestFP is the Firebase project the Firestore step tests read and write:
// the run history's database lives in the Firebase project, not in the
// installation's own GCP project (design D7).
const fsTestFP = "hist-1234"

// fsStep is the Firestore step over the fakes, as init builds it for an
// installation whose region is region: the database's location and the place
// are the mapping's answers (D3085), which is what the CLI passes and what
// these tests hold.
func fsStep(t *testing.T, region string) (*FirestoreStep, *gcpfake.Firestore) {
	t.Helper()
	fs := gcpfake.NewFirestore(t)
	rules := gcpfake.NewFirebaseRules(t, fsTestFP)
	c, err := firestore.New(fs.URL, fsTestFP, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"}),
		firestore.WithPollInterval(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	m := place.For(region)
	return &FirestoreStep{
		FS:    c,
		Rules: &RulesClient{Endpoint: rules.URL, Project: fsTestFP, HTTP: rules.Client()},
		FP:    fsTestFP, Project: "aurora", GCPProject: "proj-1234", Version: "test", Wait: time.Millisecond,
		Location: m.Firestore.Where, Place: m.Place,
	}, fs
}

// writesSent reports the calls that are not reads: a step that refused, or
// that adopted what it found, must send none.
func writesSent(t *testing.T, fs *gcpfake.Firestore) []string {
	t.Helper()
	var out []string
	for _, q := range fs.Requests() {
		if q.Method != "GET" {
			out = append(out, q.Method+" "+q.Path)
		}
	}
	return out
}

// TestFirestoreIsCreatedWhereTheMappingSays is LP3 and D3085: the location the
// step creates the database in is place.For's answer for the installation's
// region — Firestore's own location where it offers one there, the place's
// default region where it does not, and Fugaro's own default for a region in
// neither place. The region itself is never the answer by itself.
func TestFirestoreIsCreatedWhereTheMappingSays(t *testing.T) {
	for _, tc := range []struct {
		region, want string
		why          string
	}{
		{"europe-west4", "europe-west4", "Firestore is offered in the region"},
		{"us-east5", "us-east5", "the default region, where Firestore is offered"},
		{"us-west8", "us-east5", "Firestore is not offered in us-west8, so the US default region"},
		{"europe-west2", "europe-west2", "London is in neither place and Firestore is offered there"},
		{"asia-southeast3", "us-east5", "in neither place and Firestore is not offered there"},
	} {
		t.Run(tc.region, func(t *testing.T) {
			if got := place.For(tc.region).Firestore.Where; got != tc.want {
				t.Fatalf("the mapping puts %s in %s, want %s", tc.region, got, tc.want)
			}
			s, fs := fsStep(t, tc.region)
			fs.RemoveDatabase()
			lines, _, err := s.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) == 0 || !strings.Contains(lines[0], "CREATE the (default) database in "+tc.want) {
				t.Fatalf("the plan does not create in %s (%s): %q", tc.want, tc.why, lines)
			}
			if err := s.Apply(context.Background()); err != nil {
				t.Fatal(err)
			}
			if loc, prot := fs.Database(); loc != tc.want || prot != "DELETE_PROTECTION_ENABLED" {
				t.Errorf("the database is %s %s, want %s with delete protection on", loc, prot, tc.want)
			}
		})
	}
}

// TestAFirestoreDatabaseElsewhereIsAdopted is LP3, R3 and D3091: a native-mode
// database that exists is adopted wherever it is — nothing moves it — and the
// line says where it is when that is outside the installation's place. A
// multi-region is in a place like a region is, and a region in neither place
// promises nothing for a database to be outside of.
func TestAFirestoreDatabaseElsewhereIsAdopted(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ name, region, db, want string }{
		{"in the other place", "us-east5", "europe-west1",
			"Cloud Firestore database: exists in europe-west1 (outside the US), adopted as it is"},
		{"an EU installation adopting a US database", "europe-west1", "us-east5",
			"Cloud Firestore database: exists in us-east5 (outside the EU), adopted as it is"},
		{"a multi-region of the same place", "us-east5", "nam5",
			"Cloud Firestore database: exists in nam5, adopted as it is"},
		{"a multi-region of the other place", "europe-west1", "nam5",
			"Cloud Firestore database: exists in nam5 (outside the EU), adopted as it is"},
		{"a region in neither place", "us-east5", "europe-west2",
			"Cloud Firestore database: exists in europe-west2 (outside the US), adopted as it is"},
		{"an installation in neither place", "europe-west2", "us-east5",
			"Cloud Firestore database: exists in us-east5, adopted as it is"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fs := fsStep(t, tc.region)
			fs.SetDatabase(tc.db)
			lines, _, err := s.Plan(ctx)
			if err != nil {
				t.Fatalf("a native database in %s was not adopted: %v", tc.db, err)
			}
			if !containsLine(lines, tc.want) {
				t.Errorf("the plan does not say where the database is:\n%s", strings.Join(lines, "\n"))
			}
			// Empty and unmarked: it is adopted and marked, and nothing is created.
			if !containsLine(lines, "Firestore database holds no collection and no mark: it is empty, adopted") {
				t.Errorf("an empty database was not adopted as empty:\n%s", strings.Join(lines, "\n"))
			}
			if err := s.Apply(ctx); err != nil {
				t.Fatal(err)
			}
			if loc, _ := fs.Database(); loc != tc.db {
				t.Errorf("the database is now in %s, want it left in %s", loc, tc.db)
			}
			if _, ok := fs.Value(FirestoreMetaCollection, FirestoreMarkDoc); !ok {
				t.Error("the adopted database was not marked")
			}
			for _, w := range writesSent(t, fs) {
				if strings.HasSuffix(w, "/databases") || strings.HasPrefix(w, "DELETE") {
					t.Errorf("an adopted database was created or deleted: %s", w)
				}
			}
		})
	}

	// Ours, and nowhere near the installation: adopted as it is, with the mark
	// left where it is and the location never touched.
	t.Run("ours, elsewhere", func(t *testing.T) {
		s, fs := fsStep(t, "us-east5")
		fs.SetDatabase("europe-west1")
		fs.Set(FirestoreMetaCollection, FirestoreMarkDoc, s.mark().fields(s.Version))
		lines, _, err := s.Plan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !containsLine(lines, "Cloud Firestore database: exists in europe-west1 (outside the US), adopted as it is") ||
			!containsLine(lines, "Firestore mark (meta/installation): present, left as it is") {
			t.Errorf("our database elsewhere was not adopted as it is:\n%s", strings.Join(lines, "\n"))
		}
		if s.CreatesDatabase() {
			t.Error("a database that exists would be created")
		}
		if err := s.Apply(ctx); err != nil {
			t.Fatal(err)
		}
		if loc, _ := fs.Database(); loc != "europe-west1" {
			t.Errorf("the database is now in %s", loc)
		}
	})
}

// TestADatastoreModeDatabaseIsStillRefused is LP3: the location stopped
// deciding anything, and the mode is what is left — a database in Datastore
// mode cannot be converted and cannot serve Firestore clients, so it is
// refused wherever it is.
func TestADatastoreModeDatabaseIsStillRefused(t *testing.T) {
	ctx := context.Background()
	for _, loc := range []string{"us-east5", "europe-west1"} {
		t.Run(loc, func(t *testing.T) {
			s, fs := fsStep(t, "us-east5")
			fs.SetDatabase(loc)
			fs.SetDatabaseType("DATASTORE_MODE")
			_, _, err := s.Plan(ctx)
			var ue *UserError
			if !errors.As(err, &ue) {
				t.Fatalf("Plan err = %v, want a refusal", err)
			}
			if !strings.Contains(err.Error(), "DATASTORE_MODE") || !strings.Contains(err.Error(), loc) {
				t.Errorf("the refusal does not name the mode and where the database is: %v", err)
			}
			if s.Pending() {
				t.Error("a refused database is pending")
			}
			if w := writesSent(t, fs); len(w) != 0 {
				t.Errorf("a refused database was written to: %v", w)
			}
			if got, _ := fs.Database(); got != loc {
				t.Errorf("the database is now in %s, want it left in %s", got, loc)
			}
		})
	}
}

// TestACreateRaceAdoptsOnlyTheConfirmedLocation: the database the person
// confirmed is the one in the step's own location (D3085). One that appears
// there between the plan and the write is adopted as ours, and the step says
// where it is, because this run did not create it; one that appears anywhere
// else is a state nothing confirmed — a location is permanent — so Apply
// refuses, names where the database is and what to run, and writes nothing.
func TestACreateRaceAdoptsOnlyTheConfirmedLocation(t *testing.T) {
	ctx := context.Background()

	t.Run("the confirmed location", func(t *testing.T) {
		s, fs := fsStep(t, "us-east5")
		fs.RemoveDatabase()
		if _, _, err := s.Plan(ctx); err != nil {
			t.Fatal(err)
		}
		fs.RaceCreate("us-east5")
		if err := s.Apply(ctx); err != nil {
			t.Fatalf("a database that appeared in the confirmed location was not adopted: %v", err)
		}
		if got := s.AdoptedLocation(); got != "us-east5" {
			t.Errorf("the step says the adopted database is in %q, want us-east5", got)
		}
		if loc, _ := fs.Database(); loc != "us-east5" {
			t.Errorf("the adopted database is now in %s, want it left in us-east5", loc)
		}
		if _, ok := fs.Value(FirestoreMetaCollection, FirestoreMarkDoc); !ok {
			t.Error("the adopted database was not marked")
		}
	})

	t.Run("another location", func(t *testing.T) {
		s, fs := fsStep(t, "us-east5")
		fs.RemoveDatabase()
		if _, _, err := s.Plan(ctx); err != nil {
			t.Fatal(err)
		}
		fs.RaceCreate("europe-west1")
		err := s.Apply(ctx)
		var ue *UserError
		if !errors.As(err, &ue) {
			t.Fatalf("Apply err = %v, want a refusal", err)
		}
		for _, want := range []string{"europe-west1", "us-east5", "run it again"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if got := s.AdoptedLocation(); got != "" {
			t.Errorf("a refused database was reported as adopted in %q", got)
		}
		if loc, _ := fs.Database(); loc != "europe-west1" {
			t.Errorf("the refused database was changed: %s", loc)
		}
		if _, ok := fs.Value(FirestoreMetaCollection, FirestoreMarkDoc); ok {
			t.Error("a database that is not where the plan said was marked")
		}
	})
}

// containsLine reports whether one of the plan's lines is exactly want.
func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func TestNormalizeRulesKeepsLiterals(t *testing.T) {
	same := [][2]string{
		{"a  b // c\n d", "a b d"},
		{"a /* x */ b", "a b"},
		{`x == "a//b" // c`, `x ==   "a//b"`},
		{`x == 'a /* b */'`, "x == 'a /* b */'\r\n"},
	}
	for _, p := range same {
		if normalizeRules(p[0]) != normalizeRules(p[1]) {
			t.Errorf("%q != %q: %q vs %q", p[0], p[1], normalizeRules(p[0]), normalizeRules(p[1]))
		}
	}
	diff := [][2]string{
		{`x == "a//b"`, `x == "a"`},
		{`x == "a  b"`, `x == "a b"`},
		{`allow read: if true;`, `allow read: if false;`},
		{"ab", "a b"},
	}
	for _, p := range diff {
		if normalizeRules(p[0]) == normalizeRules(p[1]) {
			t.Errorf("%q == %q", p[0], p[1])
		}
	}
}
