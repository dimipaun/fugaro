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

// fakeDB is the database as the state uses it: nodes of JSON, read with their
// ETag and written under it. It is where the ETag path is exercised, without a
// network — and it can lose a race on purpose.
type fakeDB struct {
	mu    sync.Mutex
	nodes map[string]json.RawMessage
	etags map[string]int

	// conflicts is the number of writes that will lose a race before one lands.
	// Each lost write first lets rival take the node, which is what another
	// instance writing between this writer's read and its write looks like.
	conflicts int
	rival     any

	gets, puts int
	putErr     error
}

func newFakeDB() *fakeDB {
	return &fakeDB{nodes: map[string]json.RawMessage{}, etags: map[string]int{}}
}

func (d *fakeDB) put(path string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	d.nodes[path] = b
}

// GetETag answers the node at path, and — as the database does — the whole
// subtree below it when path is a prefix of the nodes that were written: the
// state reads creds/marks and creds/leases whole, so the fake has to have the
// same shape or the reads would silently be empty.
func (d *fakeDB) GetETag(_ context.Context, path string, out any) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gets++
	if raw, ok := d.nodes[path]; ok {
		if err := json.Unmarshal(raw, out); err != nil {
			return "", false, err
		}
		return strconv.Itoa(d.etags[path]), true, nil
	}
	tree, ok := d.subtree(path)
	if !ok {
		return "", false, nil
	}
	b, err := json.Marshal(tree)
	if err != nil {
		return "", false, err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return "", false, err
	}
	return strconv.Itoa(d.etags[path]), true, nil
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

func (d *fakeDB) PutIfMatch(_ context.Context, path, etag string, v any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.puts++
	if d.putErr != nil {
		return d.putErr
	}
	if d.conflicts > 0 {
		d.conflicts--
		// The rival wrote between this writer's read and its write: the node
		// moved and so did its ETag, which is what the database reports.
		if d.rival != nil {
			d.put(path, d.rival)
		}
		d.etags[path]++
		return rtdb.ErrPrecondition
	}
	d.put(path, v)
	d.etags[path]++
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

	leases, err := st.Leases(ctx)
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

	// SetLease writes exactly what a reader reads back, with the run filled in
	// from the path.
	if err := st.SetLease(ctx, run, Lease{Credential: a1.Name, Until: now.Add(DefaultLeaseTTL)}); err != nil {
		t.Fatal(err)
	}
	after, err := st.Leases(ctx)
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
	if _, err := (&RTDBStore{DB: db2}).Leases(ctx); err == nil {
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
