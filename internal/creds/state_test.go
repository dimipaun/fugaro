package creds

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The database the service uses satisfies the state's DB interface, checked by
// the compiler rather than by the first service that wires it (C1.10). Nothing
// in this tree constructs an RTDBStore with a real client yet, so without this
// line the claim in DB's doc — "*rtdb.Client is one" — is a comment until C1.10
// finds out otherwise; with it, a method that drifts turns into a build error
// here. It is the repository's own precedent for a seam with one real
// implementation: var _ backend.Backend = (*Backend)(nil)
// (internal/backend/batch/batch.go, internal/backend/gcp/run.go).
var _ DB = (*rtdb.Client)(nil)

// fakeDB is the database as the state uses it: nodes of JSON, read with their
// ETag and written under it. It is where the ETag path is exercised, without a
// network — and it can lose a race on purpose.
//
// It compares the ETag a writer hands it against the node's current one, which
// is the review's finding M5: the shape this fake used to have never looked at
// the precondition at all, so a cas that dropped it — one that wrote without
// retrying, or retried without re-reading — passed every test here. The two
// other ways it was not the database are fixed with it: an absent node answers
// the literal etag "null_etag" and an EMPTY etag is refused, both as
// rtdb.Client does (client.go: GetETag's doc, PutIfMatch's first check), so a
// writer that forgets to read the node first fails here the way it would fail
// against the real database instead of quietly succeeding.
type fakeDB struct {
	mu     sync.Mutex
	nodes  map[string]json.RawMessage
	etags  map[string]string
	writes int

	// conflicts is the number of writes that will lose a race before one lands.
	// Each lost write first lets rival take the node, which is what another
	// instance writing between this writer's read and its write looks like.
	conflicts int
	rival     any

	// onGet runs at the start of every read, under the lock: a test uses it to
	// slip a write in before a reader reads a node again, which is the one race a
	// scripted set of conflicts cannot express (a lease refreshed between the
	// sweep's first read and its own re-read of the node).
	onGet func(path string)

	gets, puts, patches, deletes int
	putErr, patchErr             error
}

func newFakeDB() *fakeDB {
	return &fakeDB{nodes: map[string]json.RawMessage{}, etags: map[string]string{}}
}

// nullEtag is what the database answers for a node that is not there
// (rtdb.Client.GetETag). It is a value a writer can hand back to PutIfMatch,
// which is exactly why it is not the empty string.
const nullEtag = "null_etag"

func (d *fakeDB) put(path string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	d.nodes[path] = b
	d.bump(path)
}

// bump gives the node a new etag, as any write to it does.
func (d *fakeDB) bump(path string) {
	d.writes++
	d.etags[path] = "etag-" + strconv.Itoa(d.writes)
}

func (d *fakeDB) etagOf(path string) string {
	if e, ok := d.etags[path]; ok {
		return e
	}
	return nullEtag
}

// GetETag answers the node at path, and — as the database does — the whole
// subtree below it when path is a prefix of the nodes that were written: the
// state reads creds/marks and creds/leases whole, so the fake has to have the
// same shape or the reads would silently be empty.
//
// A subtree's etag is the path's own, which is nullEtag unless something was
// written at that exact path. That is enough because nothing writes under a
// subtree's etag: every conditional write the state makes is one node, and the
// one multi-node write it makes (SetPlacement) is a Patch, which has no
// precondition at all.
func (d *fakeDB) GetETag(_ context.Context, path string, out any) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gets++
	if d.onGet != nil {
		d.onGet(path)
	}
	if raw, ok := d.nodes[path]; ok {
		if err := json.Unmarshal(raw, out); err != nil {
			return "", false, err
		}
		return d.etagOf(path), true, nil
	}
	tree, ok := d.subtree(path)
	if !ok {
		return nullEtag, false, nil
	}
	b, err := json.Marshal(tree)
	if err != nil {
		return "", false, err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return "", false, err
	}
	return d.etagOf(path), true, nil
}

// subtree is the nested object the leaves below prefix make, or false when
// there are none. The caller holds the lock.
func (d *fakeDB) subtree(prefix string) (map[string]any, bool) {
	root := map[string]any{}
	found := false
	for p, raw := range d.nodes {
		if !strings.HasPrefix(p, prefix+"/") {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		at := root
		segs := strings.Split(strings.TrimPrefix(p, prefix+"/"), "/")
		for _, s := range segs[:len(segs)-1] {
			next, ok := at[s].(map[string]any)
			if !ok {
				next = map[string]any{}
				at[s] = next
			}
			at = next
		}
		at[segs[len(segs)-1]] = v
		found = true
	}
	return root, found
}

// PutIfMatch writes v at path if the node's etag is still the one the writer
// read, and nil deletes it — the client's own contract, including the two ways
// this call refuses.
func (d *fakeDB) PutIfMatch(_ context.Context, path, etag string, v any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.puts++
	if etag == "" {
		// rtdb.Client.PutIfMatch: an empty etag is no precondition. A writer that
		// never read the node is refused here rather than allowed to write.
		return errors.New("rtdb: an empty etag is no precondition")
	}
	if d.putErr != nil {
		return d.putErr
	}
	if d.conflicts > 0 {
		d.conflicts--
		// The rival wrote between this writer's read and its write: the node
		// moved and so did its ETag, which is what the database reports.
		if d.rival != nil {
			d.put(path, d.rival)
		} else {
			d.bump(path)
		}
		return rtdb.ErrPrecondition
	}
	if etag != d.etagOf(path) {
		// The precondition the writer handed in is not the node's: this is the
		// check the fake did not have, and without it a cas that skipped the
		// re-read after a conflict was untestable.
		return rtdb.ErrPrecondition
	}
	if v == nil {
		delete(d.nodes, path)
		d.deletes++
		d.bump(path)
		return nil
	}
	d.put(path, v)
	return nil
}

// Patch writes every location of updates under root in one atomic step (nil
// deletes), as rtdb.Client.Patch does: either all of them land or none does. The
// paths are relative to root and without a leading slash.
func (d *fakeDB) Patch(_ context.Context, root string, updates map[string]any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.patches++
	if d.patchErr != nil {
		return d.patchErr
	}
	for rel, v := range updates {
		path := root + "/" + rel
		if v == nil {
			delete(d.nodes, path)
			d.deletes++
			d.bump(path)
			continue
		}
		d.put(path, v)
	}
	return nil
}

// A conditional write that loses a race re-reads the node and computes again,
// so the value that lands was computed from the value that was there: a lost
// update to a shared mark is the one failure this state cannot have.
func TestMarkCASRetriesOnPrecondition(t *testing.T) {
	const name = "shared-1"
	rivalSince := now.Add(-2 * time.Hour)

	db := newFakeDB()
	db.put(PathMark(name), Mark{State: StateAvailable})
	db.conflicts = 1
	db.rival = Mark{State: StateExhausted, Since: rivalSince, Until: now.Add(time.Hour)}
	st := &RTDBStore{DB: db}

	calls := 0
	m, err := st.UpdateMark(ctx, name, func(prev Mark, found bool) (Mark, error) {
		calls++
		if !found {
			return Mark{}, errors.New("the test's closure was handed a mark that was not found")
		}
		// The transition keeps Since, so the mark that lands carries the value
		// only the re-read could have given it.
		return Mark{State: StateBackedOff, Since: prev.Since, Until: now.Add(15 * time.Minute)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("the change was computed %d times; want 2 — the first attempt's write lost the race", calls)
	}
	if !m.Since.Equal(rivalSince) {
		t.Fatalf("the mark written carries Since %s; want %s, the value the winner left", m.Since, rivalSince)
	}
	// And the node holds what was returned, not the first attempt's value.
	got := Mark{}
	if _, _, err := db.GetETag(ctx, PathMark(name), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Since.Equal(rivalSince) || got.State != StateBackedOff {
		t.Fatalf("the node holds %+v; want the second attempt's mark", got)
	}
}

// A write that keeps losing is refused rather than landing unconditionally: an
// unconditional write is the lost update the loop exists to prevent, and a
// caller that is told ErrContention can pick a credential that is not contended.
func TestMarkCASGivesUpUnderContention(t *testing.T) {
	const name = "shared-1"
	db := newFakeDB()
	db.put(PathMark(name), Mark{State: StateAvailable})
	db.conflicts = 100
	db.rival = Mark{State: StateAvailable}
	st := &RTDBStore{DB: db, Attempts: 3}

	calls := 0
	_, err := st.UpdateMark(ctx, name, func(prev Mark, _ bool) (Mark, error) {
		calls++
		return prev, nil
	})
	if !errors.Is(err, ErrContention) {
		t.Fatalf("a write that lost every race = %v; want ErrContention", err)
	}
	if calls != 3 {
		t.Fatalf("the change was computed %d times; want the bound of 3", calls)
	}
	if db.puts != 3 {
		t.Fatalf("the database was written to %d times; want 3 attempts and no more", db.puts)
	}
	// A read error is returned as it is, not retried as a lost race.
	db2 := newFakeDB()
	db2.nodes[PathMark(name)] = json.RawMessage(`"not a mark"`)
	if _, err := (&RTDBStore{DB: db2}).UpdateMark(ctx, name, func(prev Mark, _ bool) (Mark, error) {
		return prev, nil
	}); err == nil || errors.Is(err, ErrContention) {
		t.Fatalf("a mark node that does not decode = %v; want the decode error", err)
	}
}

// Names and run identities are escaped into the path, so a credential name that
// carries a dot or a slash is one key and not a path of its own, and an empty
// one panics rather than collapsing the path onto a different node.
func TestPathsEscapeNamesAndRunIDs(t *testing.T) {
	name := "acme/shared.1"
	if got, want := PathMark(name), pathMarks+"/"+budget.Key(name); got != want {
		t.Fatalf("PathMark(%q) = %q; want %q", name, got, want)
	}
	// Escaping is the repository's one implementation, so a name that would
	// otherwise be read as a path is a single segment here.
	if strings.Contains(strings.TrimPrefix(PathMark(name), pathMarks+"/"), "/") {
		t.Fatalf("PathMark(%q) = %q: the name is more than one segment", name, PathMark(name))
	}
	escaped := strings.TrimPrefix(PathMark(name), pathMarks+"/")
	if back, err := budget.Unkey(escaped); err != nil || back != name {
		t.Fatalf("the escaped key %q reads back as %q, %v; want %q", escaped, back, err, name)
	}

	// The run identity is two segments, so one run's slug is never another's
	// node prefix: a slug "a/b" and a run id "c" is not the slug "a" and the run
	// id "b/c".
	same := []Run{
		Run{Slug: "a/b", ID: "c"},
		Run{Slug: "a", ID: "b/c"},
	}
	if PathCurrent(same[0]) == PathCurrent(same[1]) {
		t.Fatalf("two different runs share the node %q", PathCurrent(same[0]))
	}

	// An empty name or run id is a programming error: budget.Key("") would make
	// creds/marks/ collapse onto creds/marks, a different node entirely.
	for _, f := range []func(){
		func() { PathMark("") },
		func() { PathCurrent(Run{Slug: "", ID: "r1"}) },
		func() { PathCurrent(Run{Slug: "s", ID: ""}) },
		func() { PathLease(Run{Slug: "", ID: ""}) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("an empty path segment did not panic")
				}
			}()
			f()
		}()
	}
}

// The store reads each subtree whole and gives back names, not keys: a mark
// written under an escaped key comes back under the name it was written for.
func TestStoreReadsWholeSubtrees(t *testing.T) {
	db := newFakeDB()
	name := "acme/shared.1"
	db.put(PathMark(name), Mark{State: StateExhausted, Until: now.Add(time.Hour), Since: now})
	db.put(PathMark(a1.Name), Mark{State: StateBroken, Since: now})

	other := Run{Slug: "acme/repo", ID: "r1/2"}
	db.put(PathLease(other), Lease{Credential: name, Until: now.Add(time.Minute)})
	db.put(PathCurrent(run), a1.Name)
	st := &RTDBStore{DB: db}

	marks, err := st.Marks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 2 {
		t.Fatalf("the marks read as %v; want the two that were written", marks)
	}
	m, ok := marks[name]
	if !ok {
		t.Fatalf("the mark of %q came back under the keys %v: the escape was not reversed", name, keysOf(marks))
	}
	if m.State != StateExhausted || !m.Until.Equal(now.Add(time.Hour)) {
		t.Fatalf("the mark of %q read as %+v", name, m)
	}

	leases, err := st.Leases(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("the leases read as %v; want one", leases)
	}
	if l := leases[0]; l.Slug != other.Slug || l.Run != other.ID || l.Credential != name {
		t.Fatalf("the lease read as %+v; want the run %+v on %q: the path carries the run, the value the credential", l, other, name)
	}

	// A lease's value carries no copy of its own run, so the path is the only
	// place it can be wrong.
	b, err := json.Marshal(Lease{Slug: other.Slug, Run: other.ID, Credential: name, Until: now})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), other.ID) {
		t.Fatalf("a lease's value holds its own run id: %s", b)
	}

	if got, found, err := st.Current(ctx, run); err != nil || !found || got != a1.Name {
		t.Fatalf("the current credential of the run = %q, %v, %v; want %q", got, found, err, a1.Name)
	}
	// A run that has not been placed yet is not an error and has no name.
	if got, found, err := st.Current(ctx, Run{Slug: run.Slug, ID: "never"}); err != nil || found || got != "" {
		t.Fatalf("an unplaced run's current credential = %q, %v, %v; want none", got, found, err)
	}

	// SetPlacement writes exactly what a reader reads back, with the run filled
	// in from the path — and it writes both halves of the placement, so the run
	// that was placed somewhere has a lease and a current credential that agree.
	if err := st.SetPlacement(ctx, run, a1.Name, Lease{Credential: a1.Name, Until: now.Add(DefaultLeaseTTL)}); err != nil {
		t.Fatal(err)
	}
	if got, found, err := st.Current(ctx, run); err != nil || !found || got != a1.Name {
		t.Fatalf("after a placement the run is on %q, %v, %v; want %q", got, found, err, a1.Name)
	}
	after, err := st.Leases(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	var mine []Lease
	for _, l := range after {
		if l.Slug == run.Slug && l.Run == run.ID {
			mine = append(mine, l)
		}
	}
	if len(mine) != 1 || mine[0].Credential != a1.Name || !mine[0].Until.Equal(now.Add(DefaultLeaseTTL)) {
		t.Fatalf("the lease of %+v read back as %v", run, mine)
	}
}

// A mark node that is not a mark is an error, and the whole read fails: a
// selection that reads "no marks" because it could not read the marks is a
// selection that treats an unreadable state as an empty one, which is the one
// direction this package must never fail in.
func TestStoreRefusesAMarkItCannotRead(t *testing.T) {
	db := newFakeDB()
	db.put(PathMark(a1.Name), Mark{State: StateBroken, Since: now})
	db.nodes["creds/marks"] = json.RawMessage(`"a string, not the subtree"`)

	if _, err := (&RTDBStore{DB: db}).Marks(ctx); err == nil {
		t.Fatal("a marks subtree that is not an object read as no marks")
	}

	// A key that is not an escaped name is an error too: a hand-written node
	// under creds/marks must not be read as a credential called by its key.
	db2 := newFakeDB()
	db2.nodes[pathMarks] = json.RawMessage(`{"%zz": {"state": "available"}}`)
	if _, err := (&RTDBStore{DB: db2}).Marks(ctx); err == nil {
		t.Fatal("a mark node keyed by something that is not an escaped name read as a credential")
	}
}

// A node carrying a member this release does not model stops the read. The
// alternative is the worst failure this package has: encoding/json drops a key
// it does not know, so {"status":"broken"} — a mark a newer release wrote, or a
// hand-edit — used to decode to the ZERO Mark, which is available and usable.
// A broken credential read as an available one is fail-open, and the contract
// on Marks says the opposite.
func TestStoreRefusesAMarkItDoesNotModel(t *testing.T) {
	db := newFakeDB()
	db.nodes[pathMarks] = json.RawMessage(`{"shared-1":{"status":"broken","until":"2026-10-20T13:00:00Z"}}`)
	if _, err := (&RTDBStore{DB: db}).Marks(ctx); err == nil {
		t.Fatal("a mark node with a member this release does not model read as an available credential")
	}

	db2 := newFakeDB()
	db2.nodes[pathLeases] = json.RawMessage(`{"acme":{"r1":{"credential":"shared-1","expires":"2026-10-20T13:00:00Z"}}}`)
	if _, err := (&RTDBStore{DB: db2}).Leases(ctx, now); err == nil {
		t.Fatal("a lease node with a member this release does not model read as a hold")
	}

	// Strictness is about the members, not a blanket refusal: a mark this
	// release wrote reads back whole, Backoff included.
	db3 := newFakeDB()
	db3.put(PathMark(a1.Name), Mark{State: StateBackedOff, Until: now.Add(time.Hour), Since: now, Backoff: time.Minute})
	marks, err := (&RTDBStore{DB: db3}).Marks(ctx)
	if err != nil {
		t.Fatalf("a mark this release wrote did not read back: %v", err)
	}
	if m := marks[a1.Name]; m.State != StateBackedOff || m.Backoff != time.Minute || !m.Since.Equal(now) {
		t.Fatalf("the mark read back as %+v", m)
	}
}

// The round-robin counter hands out the value it consumed and leaves the next
// one behind, and a lost race hands out the value that was actually there: two
// instances breaking a tie at once must get two different values.
func TestRoundRobinConsumesAndAdvances(t *testing.T) {
	db := newFakeDB()
	st := &RTDBStore{DB: db}

	for i := int64(0); i < 3; i++ {
		n, err := st.RoundRobin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n != i {
			t.Fatalf("the counter handed out %d at step %d; want %d", n, i, i)
		}
	}
	var left int64
	if _, _, err := db.GetETag(ctx, pathRoundRobin, &left); err != nil {
		t.Fatal(err)
	}
	if left != 3 {
		t.Fatalf("the counter was left at %d; want 3", left)
	}

	// A counter a lost race made stale is not read twice: after the conflict
	// the next value handed out is the winner's successor, not the loser's.
	db2 := newFakeDB()
	db2.conflicts = 1
	db2.rival = int64(7)
	if n, err := (&RTDBStore{DB: db2}).RoundRobin(ctx); err != nil || n != 7 {
		t.Fatalf("the counter after a lost race = %d, %v; want the winner's 7", n, err)
	}
}

func keysOf(m map[string]Mark) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The compare-and-set decodes the node it read exactly as strictly as the
// subtree reads do, and it matters most here: f is handed the value it read and
// answers the next one, so a node carrying a member this release does not model
// used to decode to the ZERO Mark — and the write that followed would have been
// derived from "available" and taken a refused token off. Review finding m6.
func TestTheCASRefusesAMarkItDoesNotModel(t *testing.T) {
	const name = "shared-1"
	db := newFakeDB()
	raw := `{"status":"broken","until":"2026-10-20T13:00:00Z"}`
	db.nodes[PathMark(name)] = json.RawMessage(raw)

	calls := 0
	_, err := (&RTDBStore{DB: db}).UpdateMark(ctx, name, func(prev Mark, _ bool) (Mark, error) {
		calls++
		return Exhaust(prev, now, time.Time{}, noWindow()), nil
	})
	if err == nil {
		t.Fatal("a mark node with a member this release does not model was written over")
	}
	if calls != 0 {
		t.Fatalf("the change was computed %d times from a node that could not be read", calls)
	}
	if db.puts != 0 {
		t.Fatalf("the refused change reached the database %d times", db.puts)
	}
	var after json.RawMessage
	if _, _, err := db.GetETag(ctx, PathMark(name), &after); err != nil || string(after) != raw {
		t.Fatalf("the node after the refused write = %s, %v; want it exactly as it was", after, err)
	}

	// Strictness is about the members, not about the shape: a mark this release
	// wrote is written over as before.
	db2 := newFakeDB()
	db2.put(PathMark(name), Mark{State: StateAvailable})
	if _, err := (&RTDBStore{DB: db2}).UpdateMark(ctx, name, func(prev Mark, found bool) (Mark, error) {
		if !found {
			return Mark{}, errors.New("the closure was handed a mark that was not found")
		}
		return Exhaust(prev, now, now.Add(time.Hour), Settings{}), nil
	}); err != nil {
		t.Fatalf("a mark this release wrote was not written over: %v", err)
	}
}

// A node that is not there is written under the etag the database answers for
// one — "null_etag", and not the empty string, which PutIfMatch refuses — so a
// writer that never read the node fails here exactly as it would against the
// real database. Review finding M5: the fake answered "" for an absent node and
// accepted it, so neither half of that was testable.
func TestAnAbsentNodeIsWrittenUnderTheNullEtag(t *testing.T) {
	db := newFakeDB()
	st := &RTDBStore{DB: db}

	// creds/roundrobin has never been written: the first read is of a node that
	// is not there, and the write that follows it must land.
	if n, err := st.RoundRobin(ctx); err != nil || n != 0 {
		t.Fatalf("the first counter value = %d, %v; want 0 read from an absent node", n, err)
	}
	var left int64
	if _, found, err := db.GetETag(ctx, pathRoundRobin, &left); err != nil || !found || left != 1 {
		t.Fatalf("after the first advance the counter = %d, found=%v, %v; want 1", left, found, err)
	}
	// The database's own refusal, stated here so a later change to its user is
	// caught: an empty etag is no precondition at all.
	if err := db.PutIfMatch(ctx, pathRoundRobin, "", int64(9)); err == nil {
		t.Fatal("the fake database accepted an empty etag as a precondition")
	}
	if _, found, err := db.GetETag(ctx, pathRoundRobin, &left); err != nil || !found || left != 1 {
		t.Fatalf("the refused write landed: the counter = %d, found=%v, %v", left, found, err)
	}

	// The same for a mark with no node: the closure is handed a zero mark and
	// found=false, which is what a first exhaustion looks like.
	if _, err := st.UpdateMark(ctx, a1.Name, func(prev Mark, found bool) (Mark, error) {
		if found {
			return Mark{}, errors.New("an absent mark was reported as found")
		}
		return Exhaust(prev, now, now.Add(time.Hour), Settings{}), nil
	}); err != nil {
		t.Fatal(err)
	}
	var m Mark
	if _, found, err := db.GetETag(ctx, PathMark(a1.Name), &m); err != nil || !found || m.State != StateExhausted {
		t.Fatalf("the mark written to an absent node = %+v, found=%v, %v", m, found, err)
	}
}

// A placement — the run's current credential and its lease — is written as one
// atomic Patch, so a failure takes neither and a success leaves the two
// agreeing. Review finding m5: as two conditional writes they were
// compare-and-set in name only (neither value was computed from the one it read)
// and could be torn apart, leaving the run sticky on a credential it held no
// slot for.
func TestAPlacementIsWrittenAsOneThing(t *testing.T) {
	db := newFakeDB()
	db.patchErr = errors.New("the database refused the patch")
	st := &RTDBStore{DB: db}
	l := Lease{Credential: a1.Name, Until: now.Add(time.Minute)}

	if err := st.SetPlacement(ctx, run, a1.Name, l); err == nil {
		t.Fatal("a placement the database refused reported success")
	}
	if _, found, err := st.Current(ctx, run); err != nil || found {
		t.Fatalf("a refused placement left the run on a credential: found=%v, %v", found, err)
	}
	if held, err := st.Leases(ctx, now); err != nil || len(held) != 0 {
		t.Fatalf("a refused placement left the leases %v, %v; want none", held, err)
	}

	// And each placement is one request: a two-write implementation would have
	// landed the first of the two before the second was refused.
	db.patchErr = nil
	before := db.patches
	if err := st.SetPlacement(ctx, run, a1.Name, l); err != nil {
		t.Fatal(err)
	}
	if db.patches-before != 1 {
		t.Fatalf("the placement took %d requests; want exactly one", db.patches-before)
	}
	if db.patches != 2 {
		t.Fatalf("the two placements took %d requests in all; want one for each", db.patches)
	}
	if name, found, err := st.Current(ctx, run); err != nil || !found || name != a1.Name {
		t.Fatalf("the placed run is on %q, found=%v, %v; want %q", name, found, err, a1.Name)
	}
	held, err := st.Leases(ctx, now)
	if err != nil || len(held) != 1 || held[0].Credential != a1.Name || held[0].Slug != run.Slug || held[0].Run != run.ID {
		t.Fatalf("the placed run's lease = %v, %v; want one hold on %q for %+v", held, err, a1.Name, run)
	}
}

// leaseDB is a database holding lapsed leases: n of them, one per minute from
// two hours ago — so every one of them has lapsed, whatever n is — plus one live
// hold, which is what a project that has been running for a while looks like.
func leaseDB(n int) (*fakeDB, Run) {
	db := newFakeDB()
	base := now.Add(-2 * time.Hour)
	for i := 0; i < n; i++ {
		r := Run{Slug: "acme", ID: "gone-" + strconv.Itoa(i)}
		db.put(PathLease(r), Lease{Credential: a1.Name, Until: base.Add(time.Duration(i) * time.Minute)})
	}
	live := Run{Slug: "acme", ID: "live"}
	db.put(PathLease(live), Lease{Credential: a1.Name, Until: now.Add(time.Minute)})
	return db, live
}

// The lease read sweeps the dead nodes it finds, so what the next read must
// decode stays at a residue plus the live holds instead of growing with every run
// the project has ever had. Review finding M6: the subtree was unbounded, every
// selection read it whole, and the database caps one response at 16 MiB — past
// which the decode fails and EVERY selection in the project fails closed.
func TestTheLeaseReadSweepsTheDeadNodesItFinds(t *testing.T) {
	const gone = DefaultLeaseResidue + 6
	db, live := leaseDB(gone)
	// The runs are placed, not only leased: a run holds a placement node of its
	// own (PathCurrent), and what the sweep does to it is part of what this read
	// promises — it deletes the dead LEASE, and a run's placement is not its to
	// delete. Review finding: the sweep's placement was asserted with Current on
	// a run the fixture never placed, so the assertion could not fail whatever
	// the code did.
	for _, id := range []string{"gone-0", "gone-5", "gone-6", "live"} {
		db.put(PathCurrent(Run{Slug: "acme", ID: id}), a1.Name)
	}
	st := &RTDBStore{DB: db}
	// The fixture holds leases and no placements, so the oldest lapsed run is
	// placed here first: the sweep deletes leases, not placements (a lapsed
	// run's placement is the service's to remove when the run ends), and this
	// node is what makes the assertion below an assertion at all — with no
	// PathCurrent in the fixture, Current answers absent whether or not the
	// sweep ran, whatever the node's message claims.
	swept := Run{Slug: "acme", ID: "gone-0"}
	db.put(PathCurrent(swept), a1.Name)

	leases, err := st.Leases(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	// The answer is every lease as it was read: a sweep does not change what this
	// reader saw, and a lapsed lease holds nothing either way.
	if want := gone + 1; len(leases) != want {
		t.Fatalf("the read answered %d leases; want %d", len(leases), want)
	}
	if want := gone - DefaultLeaseResidue; db.deletes != want {
		t.Fatalf("the sweep deleted %d nodes; want %d — the lapsed ones above the residue of %d", db.deletes, want, DefaultLeaseResidue)
	}
	// And the runs are still placed where they were, the ones it took a lease
	// from and the one it left alike: the sweep deletes a dead LEASE and not the
	// placement beside it. A lapsed lease is not a finished run — a run between
	// two selections holds one, and the lease it refreshed is the only thing the
	// sweep re-checks — so a sweep that took the placement too would move a
	// working run off the credential it is on. A run's placement is removed
	// where the run ends and not on this read path (the plan's own C1.9 note
	// N4: `creds/current/*` is not swept at all).
	for _, id := range []string{"gone-0", "gone-5", "gone-6"} {
		r := Run{Slug: "acme", ID: id}
		if name, found, err := st.Current(ctx, r); err != nil || !found || name != a1.Name {
			t.Fatalf("the run %+v is on %q, found=%v, %v; want the placement %q left where it was: the sweep deletes dead leases and not placements", r, name, found, err, a1.Name)
		}
	}
	// The oldest went, the newest lapsed stayed, and the live hold was never a
	// candidate. The swept run's placement is still there: the sweep's subtree
	// is creds/leases, and reading a change into it would be a delete the
	// service owns.
	if name, found, err := st.Current(ctx, swept); err != nil || !found || name != a1.Name {
		t.Fatalf("the placement of the swept run = (%q, %v, %v); want %q — the sweep deletes leases, and a placement is not its to delete", name, found, err, a1.Name)
	}
	for _, r := range []Run{{Slug: "acme", ID: "gone-0"}, {Slug: "acme", ID: "gone-5"}} {
		if _, ok := db.nodes[PathLease(r)]; ok {
			t.Fatalf("the sweep left %+v: it deletes the oldest lapsed leases first", r)
		}
	}
	if _, ok := db.nodes[PathLease(Run{Slug: "acme", ID: "gone-6"})]; !ok {
		t.Fatal("the sweep deleted a lease newer than the ones it should have taken")
	}
	if _, ok := db.nodes[PathLease(live)]; !ok {
		t.Fatal("the sweep deleted a live hold")
	}

	// A second read finds the residue and prunes nothing: the common project pays
	// no writes at all on the read path.
	before := db.deletes
	if _, err := st.Leases(ctx, now); err != nil {
		t.Fatal(err)
	}
	if db.deletes != before {
		t.Fatalf("a read inside the residue deleted %d more nodes; want none", db.deletes-before)
	}

	// A read that finds only live holds deletes nothing, whatever their number:
	// the cap is about runs working, and the sweep is about runs gone.
	busy := newFakeDB()
	for i := 0; i < DefaultLeaseResidue*2; i++ {
		r := Run{Slug: "acme", ID: "busy-" + strconv.Itoa(i)}
		busy.put(PathLease(r), Lease{Credential: a1.Name, Until: now.Add(time.Minute)})
	}
	if _, err := (&RTDBStore{DB: busy}).Leases(ctx, now); err != nil {
		t.Fatal(err)
	}
	if busy.deletes != 0 {
		t.Fatalf("the sweep deleted %d live holds", busy.deletes)
	}
}

// The sweep's prune is BOUNDED, and the bound is what keeps the read path from
// turning into a delete of the whole subtree the first time a project comes back
// from a long quiet spell: a read deletes the oldest DefaultLeasePrune lapsed
// nodes — never more, whatever the residue above DefaultLeaseResidue is — and
// the next read takes the next ones. Each one lands under the ETag it re-read,
// so a read that deletes eight costs eight conditional writes and not one big
// one.
//
// Review finding N3: every existing case had DefaultLeaseResidue+6 lapsed nodes
// at most, which is a prune of six — the clamp at DefaultLeasePrune was never
// reached, so removing it, or pruning all of the lapsed nodes, went unnoticed.
func TestTheLeaseSweepPrunesAtMostItsBound(t *testing.T) {
	const gone = DefaultLeaseResidue + 20
	db, live := leaseDB(gone)
	st := &RTDBStore{DB: db}

	if _, err := st.Leases(ctx, now); err != nil {
		t.Fatal(err)
	}
	if db.deletes != DefaultLeasePrune {
		t.Fatalf("a read over %d lapsed leases pruned %d nodes; want the bound of %d", gone, db.deletes, DefaultLeasePrune)
	}
	// The oldest went, and the one just past the bound stayed: the prune is the
	// oldest DefaultLeasePrune of the lapsed nodes and not an arbitrary eight.
	for i := 0; i < DefaultLeasePrune; i++ {
		r := Run{Slug: "acme", ID: "gone-" + strconv.Itoa(i)}
		if _, ok := db.nodes[PathLease(r)]; ok {
			t.Fatalf("the sweep left %+v: it prunes the oldest lapsed leases first", r)
		}
	}
	if _, ok := db.nodes[PathLease(Run{Slug: "acme", ID: "gone-" + strconv.Itoa(DefaultLeasePrune)})]; !ok {
		t.Fatalf("the sweep pruned past its bound of %d", DefaultLeasePrune)
	}
	if _, ok := db.nodes[PathLease(live)]; !ok {
		t.Fatal("the sweep deleted a live hold")
	}

	// The next read prunes the next oldest eight: the subtree converges instead
	// of being emptied in one read, which is the property the bound is for.
	before := db.deletes
	if _, err := st.Leases(ctx, now); err != nil {
		t.Fatal(err)
	}
	if db.deletes-before != DefaultLeasePrune {
		t.Fatalf("the second read pruned %d nodes; want the same bound of %d", db.deletes-before, DefaultLeasePrune)
	}
	if _, ok := db.nodes[PathLease(Run{Slug: "acme", ID: "gone-" + strconv.Itoa(DefaultLeasePrune)})]; ok {
		t.Fatalf("the second read left gone-%d: the sweep is the oldest first, read after read", DefaultLeasePrune)
	}

	// And it stops at the residue rather than pruning to nothing: reads keep
	// going until at most DefaultLeaseResidue lapsed nodes are left, and then
	// the project pays no writes at all on the read path.
	for i := 0; i < 20; i++ {
		if _, err := st.Leases(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	lapsed := 0
	for p := range db.nodes {
		if p == PathLease(live) {
			continue
		}
		lapsed++
	}
	if lapsed > DefaultLeaseResidue {
		t.Fatalf("%d lapsed leases were left after twenty reads; want at most the residue of %d", lapsed, DefaultLeaseResidue)
	}
	before = db.deletes
	if _, err := st.Leases(ctx, now); err != nil {
		t.Fatal(err)
	}
	if db.deletes != before {
		t.Fatalf("a read at the residue pruned %d more nodes; want none", db.deletes-before)
	}

	// BELOW the residue the sweep does nothing at all — and it gets there
	// without indexing a slice, which is the reason the guard is the residue
	// rather than "anything lapsed": the prune count is lapsed-minus-residue, so
	// it is negative for a handful of dead nodes, and a sweep that entered the
	// loop anyway would panic the read path — every selection in the project
	// failing closed on a project that has simply been quiet.
	few, _ := leaseDB(DefaultLeaseResidue - 1)
	fewst := &RTDBStore{DB: few}
	if _, err := fewst.Leases(ctx, now); err != nil {
		t.Fatalf("a read over %d lapsed leases failed: %v", DefaultLeaseResidue-1, err)
	}
	if few.deletes != 0 {
		t.Fatalf("a read over %d lapsed leases, below the residue of %d, pruned %d nodes; want none", DefaultLeaseResidue-1, DefaultLeaseResidue, few.deletes)
	}
}

// One read deletes at most DefaultLeasePrune lapsed nodes, however many are past
// the residue: the sweep runs on the READ path, and a project coming back after
// a long quiet spell must not pay for its whole backlog of dead leases in one
// selection. The bound is what keeps the read's cost flat; without it a read
// with a ten-thousand-node backlog would issue ten thousand deletes. Review
// finding N3: every other test stops at residue+6 lapsed, so the clamp never
// fired.
func TestTheLeaseSweepDeletesAtMostOneBoundPerRead(t *testing.T) {
	const lapsed = DefaultLeaseResidue + DefaultLeasePrune + 5
	db, live := leaseDB(lapsed)

	if _, err := (&RTDBStore{DB: db}).Leases(ctx, now); err != nil {
		t.Fatal(err)
	}
	if db.deletes != DefaultLeasePrune {
		t.Fatalf("the sweep deleted %d nodes; want the bound of %d, not all %d of the lapsed ones", db.deletes, DefaultLeasePrune, lapsed-DefaultLeaseResidue)
	}
	// What it did delete is the oldest, in the tie-broken order (the path breaks
	// ties, so repeated sweeps converge): gone-0 through gone-(prune-1).
	for i := 0; i < DefaultLeasePrune; i++ {
		r := Run{Slug: "acme", ID: "gone-" + strconv.Itoa(i)}
		if _, ok := db.nodes[PathLease(r)]; ok {
			t.Fatalf("the sweep left %+v; it deletes the oldest lapsed leases first", r)
		}
	}
	if _, ok := db.nodes[PathLease(Run{Slug: "acme", ID: "gone-" + strconv.Itoa(DefaultLeasePrune)})]; !ok {
		t.Fatal("the sweep deleted a lapsed lease past its per-read bound")
	}
	if _, ok := db.nodes[PathLease(live)]; !ok {
		t.Fatal("the sweep deleted a live hold")
	}

	// The next read takes the next batch, so the backlog drains a bound at a time
	// rather than being abandoned at the residue: 77 lapsed, 8 swept, leaves 69 —
	// still over the residue, so the next read sweeps the 5 above it.
	before := db.deletes
	if _, err := (&RTDBStore{DB: db}).Leases(ctx, now); err != nil {
		t.Fatal(err)
	}
	want := lapsed - DefaultLeaseResidue - DefaultLeasePrune
	if db.deletes-before != want {
		t.Fatalf("the second sweep deleted %d nodes; want the %d the residue left over", db.deletes-before, want)
	}
}

// A sweep that cannot land is not a read that fails: the leases it answered with
// are the leases as they were, and failing a selection because another run's dead
// node was not deleted would let the sweep decide the call.
func TestTheLeaseSweepIsBestEffort(t *testing.T) {
	db, _ := leaseDB(DefaultLeaseResidue + 3)
	db.putErr = errors.New("the database refused the write")
	st := &RTDBStore{DB: db}

	leases, err := st.Leases(ctx, now)
	if err != nil {
		t.Fatalf("a sweep that could not write failed the read: %v", err)
	}
	if want := DefaultLeaseResidue + 4; len(leases) != want {
		t.Fatalf("the read answered %d leases after a refused sweep; want %d", len(leases), want)
	}
	if db.deletes != 0 {
		t.Fatalf("a refused write deleted %d nodes", db.deletes)
	}
}

// A hold that is refreshed between the read and the sweep is not deleted: the
// sweep re-reads each node it is about to remove and leaves it when it is live
// again. Deleting a hold that had just been renewed would put a working run back
// under a cap it had already taken a slot against.
func TestTheLeaseSweepLeavesARefreshedHold(t *testing.T) {
	db, live := leaseDB(DefaultLeaseResidue + 6)
	oldest := Run{Slug: "acme", ID: "gone-0"}
	refreshed := false
	db.onGet = func(path string) {
		// The lease's own run refreshed it between this reader's read and the
		// sweep's re-read. The node is replaced in place, without a new etag, so
		// what saves it here is the liveness check and not the precondition (a
		// real refresh moves the etag too, and either guard leaves the hold).
		if path == PathLease(oldest) && !refreshed {
			refreshed = true
			b, err := json.Marshal(Lease{Credential: a1.Name, Until: now.Add(30 * time.Minute)})
			if err != nil {
				panic(err)
			}
			db.nodes[path] = b
		}
	}

	if _, err := (&RTDBStore{DB: db}).Leases(ctx, now); err != nil {
		t.Fatal(err)
	}
	if !refreshed {
		t.Fatal("the sweep never re-read the lease it was about to delete: it deletes what was lapsed at the read time alone")
	}
	if _, ok := db.nodes[PathLease(oldest)]; !ok {
		t.Fatal("the sweep deleted a hold that had been refreshed since the read")
	}
	if _, ok := db.nodes[PathLease(live)]; !ok {
		t.Fatal("the sweep deleted a live hold")
	}
	// Six candidates, five deleted: the refreshed one is the one that stayed.
	if want := 5; db.deletes != want {
		t.Fatalf("the sweep deleted %d nodes; want %d, the refreshed hold excepted", db.deletes, want)
	}
}

// A node keyed by the empty string unescapes to an empty name, slug or run id,
// and none of those is a credential or a run: the read refuses it. It must not
// be carried onward as one either, because this package's path builders panic on
// an empty segment (seg) and a panic in the read path is a hand-written node
// stopping the whole service rather than failing one read closed.
func TestStoreRefusesANodeWithNoKey(t *testing.T) {
	for _, raw := range []string{
		// An empty slug, and an empty run id under a real one.
		`{"":{"r1":{"credential":"shared-1","until":"2026-10-20T13:00:00Z"}}}`,
		`{"":{"":{"credential":"shared-1","until":"2026-10-20T13:00:00Z"}}}`,
	} {
		db := newFakeDB()
		db.nodes[pathLeases] = json.RawMessage(raw)
		if _, err := (&RTDBStore{DB: db}).Leases(ctx, now); err == nil {
			t.Fatalf("the lease nodes %s read as holds; want a refusal", raw)
		}
	}

	db2 := newFakeDB()
	db2.nodes[pathLeases] = json.RawMessage(`{"acme":{"":{"credential":"shared-1","until":"2026-10-20T13:00:00Z"}}}`)
	if _, err := (&RTDBStore{DB: db2}).Leases(ctx, now); err == nil {
		t.Fatal("a lease node under an empty run id read as a hold")
	}

	db3 := newFakeDB()
	db3.nodes[pathMarks] = json.RawMessage(`{"":{"state":"exhausted","until":"2026-10-20T13:00:00Z"}}`)
	if _, err := (&RTDBStore{DB: db3}).Marks(ctx); err == nil {
		t.Fatal("a mark node under an empty name read as a credential")
	}
}
