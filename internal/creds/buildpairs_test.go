package creds

// The build-pair store's tests (B1, D2302): the record under
// creds/buildpairs/<identity>/<build id>, the two keys a build's traffic is
// looked up by, the closed shape that holds no value, and the record that
// lapses on its own.
//
// They run over the fake database (state_test.go), so a pairing is written and
// read through the ETag discipline the service really uses rather than through
// a map of the test's own: what the proxy's admission reads is what
// PutBuildPair wrote, and a store that wrote without a precondition fails here
// as it would against the real database.

import (
	"errors"
	"testing"
	"time"
)

// buildPairIdentity is the project's build identity, in the address shape a
// service account has (auth.go's validAccount): an account is not a secret, and
// this one is the key the fixture's pairing is written under.
const buildPairIdentity = "fugaro-build@acme.iam.gserviceaccount.com"

// buildPairID and buildPairOtherID are two builds of that one project. Two ids
// and not one, because the record is keyed by the build and the test below is
// the reason: one project has ONE build identity (B5), so the id is the only
// thing that tells two builds' pairings apart.
const (
	buildPairID      = "b1c2d3e4-0000-4000-8000-000000000001"
	buildPairOtherID = "b1c2d3e4-0000-4000-8000-000000000002"
)

// testBuildPair is a pairing as the grant writes one: the entry the request
// named, the entry's repositories by canonical name, and an expiry after the
// fixture's clock.
func testBuildPair(buildID string) BuildPair {
	return BuildPair{
		Identity:     buildPairIdentity,
		BuildID:      buildID,
		Entry:        "store",
		Repositories: []string{"acme/app", "acme/lib"},
		Expires:      now.Add(10 * time.Minute),
	}
}

// buildPairStore is the store over the fake database: the path a pairing is
// written to and read from, without a network.
func buildPairStore(db *fakeDB) *RTDBStore { return &RTDBStore{DB: db} }

// A pairing is written under the identity and the build id and comes back whole,
// which is the one round trip the grant's answer and the proxy's admission are:
// what the service recorded is what a build's git is served against.
//
// The node's SHAPE is held here too, and it is not a detail: the identity and
// the build id are path segments and not members of the value, so a reader
// cannot be given a record whose keys disagree with where it was found — and the
// value carries the entry, the repositories and the expiry and NOTHING else, so
// no credential, no token and no hash of one is in it (G9, F19).
func TestBuildPairRoundTrips(t *testing.T) {
	db := newFakeDB()
	st := buildPairStore(db)
	want := testBuildPair(buildPairID)

	if err := st.PutBuildPair(ctx, want); err != nil {
		t.Fatalf("writing the pairing: %v", err)
	}
	got, found, err := st.BuildPair(ctx, want.Identity, want.BuildID)
	if err != nil {
		t.Fatalf("reading the pairing: %v", err)
	}
	if !found {
		t.Fatal("the pairing was written and read back as absent")
	}
	if got.Identity != want.Identity || got.BuildID != want.BuildID {
		t.Errorf("the pairing read back names (%q, %q), want the keys it was written under (%q, %q)", got.Identity, got.BuildID, want.Identity, want.BuildID)
	}
	if got.Entry != want.Entry {
		t.Errorf("the pairing names the entry %q, want %q", got.Entry, want.Entry)
	}
	if len(got.Repositories) != len(want.Repositories) {
		t.Fatalf("the pairing holds %v, want the entry's repositories %v", got.Repositories, want.Repositories)
	}
	for i, r := range want.Repositories {
		if got.Repositories[i] != r {
			t.Errorf("the pairing's repository %d is %q, want %q", i, got.Repositories[i], r)
		}
	}
	if !got.Expires.Equal(want.Expires) {
		t.Errorf("the pairing ends %s, want %s", got.Expires, want.Expires)
	}

	// The node itself: the keys a pairing is filed under are its path, and the
	// members it holds are the three the answer and the fence are made of.
	node := decodeNode(t, rawNode(t, db, PathBuildPair(want.Identity, want.BuildID)))
	for _, key := range []string{"entry", "repositories", "expires"} {
		if _, ok := node[key]; !ok {
			t.Errorf("the stored pairing does not carry %q: %v", key, node)
		}
	}
	if got := len(node); got != 3 {
		t.Errorf("the stored pairing carries %d members, want the three the record is made of: %v", got, node)
	}
	for _, key := range []string{"identity", "build_id", "token", "secret", "hash"} {
		if _, ok := node[key]; ok {
			t.Errorf("the stored pairing carries %q, and a pairing is keyed by its path and holds no credential: %v", key, node)
		}
	}
}

// The two keys are the record's identity: two builds of one project have two
// pairings, and neither overwrites the other.
//
// This is the contract's own reason for keying on the build id ("two concurrent
// builds do not share or overwrite one pairing") and the reason the grant writes
// rather than appends: a project's builds run at once, each against the entry
// its own image map put on it, and a record under the identity alone would be
// whichever build asked last.
func TestBuildPairsAreKeyedByTheBuild(t *testing.T) {
	db := newFakeDB()
	st := buildPairStore(db)
	first, second := testBuildPair(buildPairID), testBuildPair(buildPairOtherID)
	second.Entry = "other"

	for _, p := range []BuildPair{first, second} {
		if err := st.PutBuildPair(ctx, p); err != nil {
			t.Fatalf("writing the pairing for %s: %v", p.BuildID, err)
		}
	}
	for _, want := range []BuildPair{first, second} {
		got, found, err := st.BuildPair(ctx, want.Identity, want.BuildID)
		if err != nil || !found {
			t.Fatalf("reading the pairing for %s: found=%v err=%v", want.BuildID, found, err)
		}
		if got.Entry != want.Entry {
			t.Errorf("the pairing for %s names the entry %q, want %q: the two builds' pairings are two records", want.BuildID, got.Entry, want.Entry)
		}
	}

	// A pairing a grant answers twice for one build is refreshed rather than
	// refused: a retried call is the same build asking again, and the record it
	// replaces is its own.
	refreshed := testBuildPair(buildPairID)
	refreshed.Expires = now.Add(20 * time.Minute)
	if err := st.PutBuildPair(ctx, refreshed); err != nil {
		t.Fatalf("refreshing the pairing: %v", err)
	}
	got, found, err := st.BuildPair(ctx, refreshed.Identity, refreshed.BuildID)
	if err != nil || !found {
		t.Fatalf("reading the refreshed pairing: found=%v err=%v", found, err)
	}
	if !got.Expires.Equal(refreshed.Expires) {
		t.Errorf("the pairing ends %s, want the refreshed instant %s", got.Expires, refreshed.Expires)
	}
}

// A pairing the store cannot record is refused where the mistake is, and it is
// refused WHOLE: what no call can be decided from is never written, so no reader
// afterwards has to defend against a record that is half there (the shape
// CreateRefusesWhatItCannotRecord holds for a slip).
func TestPutBuildPairRefusesWhatItCannotRecord(t *testing.T) {
	db := newFakeDB()
	st := buildPairStore(db)

	// A key that is empty is a refusal and never a path builder: PathBuildPair
	// panics on an empty segment, and both arrive from a request (the identity
	// from a verified token, the id from the call's own header).
	for _, c := range []struct {
		name string
		drop func(p *BuildPair)
	}{
		{"no identity", func(p *BuildPair) { p.Identity = "" }},
		{"no build id", func(p *BuildPair) { p.BuildID = "" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := testBuildPair(buildPairID)
			c.drop(&p)
			if err := st.PutBuildPair(ctx, p); !errors.Is(err, ErrNoBuildPair) {
				t.Fatalf("a pairing with %s answered %v; want ErrNoBuildPair", c.name, err)
			}
		})
	}

	// One field at a time, so a checkWritable that lost one of them is a failure
	// here and not a hole the next reader has to close.
	for _, c := range []struct {
		name string
		drop func(p *BuildPair)
	}{
		{"no entry", func(p *BuildPair) { p.Entry = "" }},
		{"no repositories", func(p *BuildPair) { p.Repositories = nil }},
		{"an empty list of repositories", func(p *BuildPair) { p.Repositories = []string{} }},
		{"a repository that is the empty name", func(p *BuildPair) { p.Repositories = []string{"acme/app", ""} }},
		{"no expiry", func(p *BuildPair) { p.Expires = time.Time{} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := testBuildPair(buildPairID)
			c.drop(&p)
			if err := st.PutBuildPair(ctx, p); !errors.Is(err, ErrBuildPairMalformed) {
				t.Fatalf("a pairing with %s answered %v; want ErrBuildPairMalformed", c.name, err)
			}
			if _, found, err := st.BuildPair(ctx, buildPairIdentity, buildPairID); err != nil || found {
				t.Fatalf("a refused write left a node behind: found=%v err=%v", found, err)
			}
		})
	}
}

// A pairing that was never written is absent and not an error: what an absent
// pairing MEANS is the proxy's to decide, and what it means is that the build's
// call is refused (buildProxy).
func TestBuildPairAbsentIsNotAnError(t *testing.T) {
	st := buildPairStore(newFakeDB())
	p, found, err := st.BuildPair(ctx, buildPairIdentity, buildPairID)
	if err != nil {
		t.Fatalf("reading a pairing that was never written answered %v; want no error", err)
	}
	if found {
		t.Fatalf("a pairing that was never written read back as %+v", p)
	}
	// And a read with no key is the writer's own refusal rather than a path:
	// the two arrive together and are checked together.
	if _, _, err := st.BuildPair(ctx, "", buildPairID); !errors.Is(err, ErrNoBuildPair) {
		t.Fatalf("reading with no identity answered %v; want ErrNoBuildPair", err)
	}
}

// A record this release cannot read in full is an error and never a pairing that
// is half read: a member the record does not model would be dropped silently,
// and for a pairing the dropped member is its fence — no repositories, which is
// a pairing that admits nothing or, read the other way, a record nothing can
// tell from a whole one.
func TestBuildPairRefusesAMemberItDoesNotModel(t *testing.T) {
	db := newFakeDB()
	path := PathBuildPair(buildPairIdentity, buildPairID)
	db.put(path, map[string]any{
		"entry":        "store",
		"repositories": []string{"acme/app"},
		"expires":      now.Add(time.Minute),
		"scope":        "write",
	})
	if _, _, err := buildPairStore(db).BuildPair(ctx, buildPairIdentity, buildPairID); err == nil {
		t.Fatal("a pairing carrying a member this release does not model was read and answered without an error")
	}
}

// A pairing lapses on its own, and nothing has to be deleted for that to be true:
// the instant it names is where it ends, and an instant the clock has reached is
// an ended pairing — a lease's own reading of the same question (Lease.Live).
func TestBuildPairLapsesAtItsOwnInstant(t *testing.T) {
	p := testBuildPair(buildPairID)
	if !p.Live(now) {
		t.Errorf("a pairing ending %s is not live at %s", p.Expires, now)
	}
	if !p.Live(p.Expires.Add(-time.Nanosecond)) {
		t.Error("a pairing is not live an instant before it ends")
	}
	if p.Live(p.Expires) {
		t.Errorf("a pairing ending %s is live AT its own instant: the end of a pairing serves nothing", p.Expires)
	}
	if p.Live(p.Expires.Add(time.Nanosecond)) {
		t.Error("a pairing is live after it ends")
	}
}
