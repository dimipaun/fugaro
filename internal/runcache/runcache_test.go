package runcache

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The tests in this file reach into the cache's own directory: the entry
// names, the permissions and the cap are the guarantees this package makes
// about a directory the CLI owns, and a test that went through the exported
// API alone could not see any of them.

// testOpen places the cache of project's objects of bucket under the cache
// home home.
func testOpen(t *testing.T, home, project, bucket string) *Cache {
	t.Helper()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return home
		}
		return ""
	}
	c, err := Open(getenv, project, bucket)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// testCache places a cache of bucket under dir, with dir as the whole cache
// home, and returns it with the project's directory.
func testCache(t *testing.T, dir, project, bucket string) (*Cache, string) {
	t.Helper()
	c := testOpen(t, dir, project, bucket)
	return c, c.dir
}

// entryFiles is every entry of dir, by name.
func entryFiles(t *testing.T, dir string) []string {
	t.Helper()
	items, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, it := range items {
		names = append(names, it.Name())
	}
	slices.Sort(names)
	return names
}

// TestAnEntryIsAnsweredOnlyAtItsOwnGeneration: a cached object is trusted
// for the generation it was read at and for nothing else. The runner
// rewrites result.json while a run is in flight, so a cache that answered
// the bytes it holds for any other generation, or for another run's key,
// would draw a status the bucket no longer has.
func TestAnEntryIsAnsweredOnlyAtItsOwnGeneration(t *testing.T) {
	c, dir := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	name := "runs/app/20261018-090000-0000/result.json"
	c.Put(name, 7, []byte(`{"status":"running"}`))
	if got, ok := c.Get(name, 7); !ok || string(got) != `{"status":"running"}` {
		t.Fatalf("Get at the generation it was stored at = %q, %v; want the bytes and a hit", got, ok)
	}
	for _, tc := range []struct {
		what string
		get  func() ([]byte, bool)
	}{
		{"a newer generation", func() ([]byte, bool) { return c.Get(name, 8) }},
		{"an older generation", func() ([]byte, bool) { return c.Get(name, 6) }},
		{"a generation that was never stored", func() ([]byte, bool) { return c.Get(name, 0) }},
		{"another object", func() ([]byte, bool) { return c.Get("runs/app/20261018-090000-0000/task.json", 7) }},
		{"no cache at all", func() ([]byte, bool) { return (*Cache)(nil).Get(name, 7) }},
	} {
		if got, ok := tc.get(); ok {
			t.Errorf("%s answered %q: a miss is what sends the caller back to the bucket", tc.what, got)
		}
	}
	if _, ok := c.Get(name, 7); !ok {
		t.Error("the misses above dropped the entry they did not answer")
	}

	// The same object name in another bucket is another object.
	other := &Cache{dir: dir, bucket: "another-bucket", max: MaxBytes}
	if got, ok := other.Get(name, 7); ok {
		t.Errorf("a cache of another bucket answered %q: entries are keyed by the bucket too", got)
	}
}

// TestAnEntryOfAnotherBucketIsNotAnswered: one project's cache is read for
// one bucket, and an entry names the bucket its bytes came from. A file at
// the cache's own entry path that says another bucket -- a hash collision, or
// a directory copied from one bucket to another -- is not the object that was
// asked for, whatever its name and generation say. This is the second of the
// two layers that keep one installation's runs out of another's read: the
// project directory is the first (TestTwoProjectsDoNotShareACache), and this
// one holds even inside a single project's directory.
func TestAnEntryOfAnotherBucketIsNotAnswered(t *testing.T) {
	c, _ := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	name := "runs/app/20261018-090000-0000/result.json"
	c.Put(name, 3, []byte(`{"version":1,"repo":"owner/repo-a"}`))

	// The entry a cache of another bucket would have written for the same
	// name, at the same generation, at this cache's own path.
	foreign, err := json.Marshal(entry{Bucket: "another-bucket", Name: name, Generation: 3,
		Data: []byte(`{"version":1,"repo":"owner/repo-b"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path(name), foreign, filePerm); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get(name, 3); ok {
		t.Errorf("an entry of bucket another-bucket answered %q: the bucket an entry came from is part of what places the object", got)
	}
	// The same file with this cache's own bucket in it is answered at once:
	// what refused the entry above is the bucket it names and nothing else
	// about it.
	same, err := json.Marshal(entry{Bucket: c.bucket, Name: name, Generation: 3, Data: []byte(`{"version":1,"repo":"owner/repo-a"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path(name), same, filePerm); err != nil {
		t.Fatal(err)
	}

	if got, ok := c.Get(name, 3); !ok || !bytes.Contains(got, []byte("owner/repo-a")) {
		t.Errorf("the same entry naming the cache's own bucket answered %q, %v", got, ok)
	}
}

// TestAnOversizedFileIsRefusedWithoutBeingRead: an entry is refused on the
// size the filesystem reports, before its bytes are read into memory. The
// file below is a perfectly good entry -- one object, under every cap, with
// trailing space after it, which json.Unmarshal ignores -- in a file larger
// than any entry can be. What refuses it is the file's size alone, so the
// refusal costs no read of a file that could be arbitrarily large; the
// object inside a file that is read is measured again
// (TestAnEntryPastTheReadCapIsNotServed).
func TestAnOversizedFileIsRefusedWithoutBeingRead(t *testing.T) {
	c, _ := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	name := "runs/app/20261018-090000-0000/result.json"
	c.Put(name, 3, []byte(`{"version":1,"repo":"owner/repo-a"}`))
	whole, err := os.ReadFile(c.path(name))
	if err != nil {
		t.Fatal(err)
	}
	padded := append(whole, bytes.Repeat([]byte(" "), maxEntry)...)
	if len(padded) <= maxEntry {
		t.Fatalf("the padded entry is %d bytes, want one past the %d any entry can be", len(padded), maxEntry)
	}
	if err := os.WriteFile(c.path(name), padded, filePerm); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get(name, 3); ok {
		t.Errorf("a file of %d bytes answered %q: an entry past what any entry can be is refused on its size", len(padded), got)
	}
	// The same bytes without the padding are the entry they always were: the
	// object is the entry's own, under every cap.
	if err := os.WriteFile(c.path(name), whole, filePerm); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get(name, 3); !ok || !bytes.Contains(got, []byte("owner/repo-a")) {
		t.Errorf("the unpadded entry answered %q, %v", got, ok)
	}
}

// TestACorruptEntryIsRebuilt: an entry the cache cannot read is a miss, and
// the read that follows rewrites it. The whole directory is rebuildable, so
// a torn file -- a killed process, a full disk, a hand-edit -- costs one
// bucket read and never a wrong answer.
func TestACorruptEntryIsRebuilt(t *testing.T) {
	c, dir := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	name := "runs/app/20261018-090000-0000/task.json"
	c.Put(name, 3, []byte(`{"version":1}`))
	path := c.path(name)

	for _, torn := range []string{"", `{`, `{"bucket":}`, `not json at all`, `[]`} {
		if err := os.WriteFile(path, []byte(torn), filePerm); err != nil {
			t.Fatal(err)
		}
		if got, ok := c.Get(name, 3); ok {
			t.Fatalf("a corrupt entry (%q) answered %q", torn, got)
		}
		c.Put(name, 3, []byte(`{"version":1}`))
		if got, ok := c.Get(name, 3); !ok || string(got) != `{"version":1}` {
			t.Fatalf("after rebuilding a corrupt entry (%q) Get = %q, %v", torn, got, ok)
		}
	}

	// A truncated entry is the case a crash mid-write leaves; the write's own
	// temporary-and-rename means a reader never sees one, and a file that
	// holds the first half of an entry is still a miss rather than a hit on
	// half an object.
	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, whole[:len(whole)/2], filePerm); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get(name, 3); ok {
		t.Errorf("a half-written entry answered %q", got)
	}
	files := entryFiles(t, dir)
	if len(files) != 1 {
		t.Errorf("the cache holds %v, want the one entry", files)
	}
	if strings.HasPrefix(files[0], ".") {
		t.Errorf("the write left its temporary file behind: %v", files)
	}
}

// TestTheCacheEvictsWhatNothingHasReadForLongest: the cache is capped, and
// what it drops past the cap is what was read longest ago. An entry read
// again is used again, which is what keeps the runs a listing keeps looking
// at in the cache while the rest of the window ages out.
func TestTheCacheEvictsWhatNothingHasReadForLongest(t *testing.T) {
	c, dir := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	names := []string{"a.json", "b.json", "c.json", "d.json"}
	body := bytes.Repeat([]byte("x"), 100)
	for _, n := range names {
		c.Put(n, 1, body)
	}
	// evict() is called on its own here: Put's own call runs before the test
	// has set the times the order is read from, and the order is the subject.
	base := time.Now().Add(-time.Hour)
	for i, n := range names {
		at := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(c.path(n), at, at); err != nil {
			t.Fatal(err)
		}
	}
	// A read is a use: b is the entry the next listing still wants.
	if _, ok := c.Get("b.json", 1); !ok {
		t.Fatal("b.json is not in the cache")
	}

	var total int64
	for _, f := range entryFiles(t, dir) {
		info, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
	}
	// Just under what the four entries take, so eviction has to drop some
	// and can stop before it drops them all.
	c.max = total * 2 / 4
	c.evict()

	kept := entryFiles(t, dir)
	if len(kept) == 0 || len(kept) == len(names) {
		t.Fatalf("eviction kept %v of %v entries: it has to drop some and stop", kept, names)
	}
	var after int64
	for _, f := range kept {
		info, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		after += info.Size()
	}
	if after > c.max {
		t.Errorf("the cache holds %d bytes after eviction, over its cap of %d", after, c.max)
	}
	if _, ok := c.Get("d.json", 1); !ok {
		t.Errorf("eviction dropped d.json, the entry written last and read most recently: %v", kept)
	}
	if _, ok := c.Get("b.json", 1); !ok {
		t.Errorf("eviction dropped b.json, which was just read: %v", kept)
	}
	if _, ok := c.Get("a.json", 1); ok {
		t.Errorf("eviction kept a.json, the entry nothing has read for longest: %v", kept)
	}
}

// backdate moves an entry's last use back by age, which is what nothing
// having read it for that long looks like to the cache: the time of a file
// is the only record of when it was last wanted.
func backdate(t *testing.T, path string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// TestAnEntryNothingHasReadForAWeekIsGone: what this cache holds is a run's
// object bytes, the task text among them, and nothing keeps a copy on the
// machine for good. An entry nothing has read for MaxAge is not an answer but
// a miss, and the miss removes it -- so the copy leaves the machine then, not
// whenever the size cap happens to reach it -- and an entry inside the age is
// answered as before.
func TestAnEntryNothingHasReadForAWeekIsGone(t *testing.T) {
	c, dir := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	old := "runs/app/20261018-090000-0000/task.json"
	young := "runs/app/20261018-090100-0001/task.json"
	c.Put(old, 3, []byte(`{"prompt":"the old task"}`))
	c.Put(young, 4, []byte(`{"prompt":"the new task"}`))
	backdate(t, c.path(old), MaxAge+time.Hour)

	if got, ok := c.Get(old, 3); ok {
		t.Errorf("an entry nothing read for %s answered %q: a copy that old is not a copy of the bucket any more", MaxAge, got)
	}
	if _, err := os.Stat(c.path(old)); !os.IsNotExist(err) {
		t.Errorf("the expired entry is still at %s (%v): the miss that refuses it is also what removes it", c.path(old), err)
	}
	if got, ok := c.Get(young, 4); !ok || string(got) != `{"prompt":"the new task"}` {
		t.Fatalf("the entry inside the age answered %q, %v; want the cached bytes", got, ok)
	}
	// The age is the age of the bytes, and the read that answered them is
	// what makes them young again: the next command does not drop an entry
	// this one just used.
	if info, err := os.Stat(c.path(young)); err != nil {
		t.Fatal(err)
	} else if time.Since(info.ModTime()) > time.Minute {
		t.Errorf("the entry read a moment ago was last used %s ago: a read is a use", time.Since(info.ModTime()))
	}
	if files := entryFiles(t, dir); len(files) != 1 {
		t.Errorf("the cache holds %v, want the one entry nothing has expired", files)
	}
}

// TestAWriteDropsWhatNobodyHasReadForAWeek: the age is a bound on the machine
// and not only on what a listing answers. An entry past MaxAge goes even when
// nothing asks for it again -- the run a person stopped listing -- because the
// write that seeds the size the cap counts against walks the directory first,
// and the walk is what finds it.
func TestAWriteDropsWhatNobodyHasReadForAWeek(t *testing.T) {
	home := t.TempDir()
	c := testOpen(t, home, "aurora", "fugaro-runs")
	old, young := "runs/app/20261018-090000-0000/task.json", "runs/app/20261018-090100-0001/task.json"
	c.Put(old, 3, []byte(`{"prompt":"the old task"}`))
	c.Put(young, 4, []byte(`{"prompt":"the new task"}`))
	backdate(t, c.path(old), MaxAge+time.Hour)

	// The next command reads an object the cache does not hold: the write it
	// makes sweeps the entries nobody has read for a week.
	again := testOpen(t, home, "aurora", "fugaro-runs")
	if got, ok := again.Get(young, 4); !ok || string(got) != `{"prompt":"the new task"}` {
		t.Fatalf("the entry inside the age answered %q, %v", got, ok)
	}
	again.Put("runs/app/20261018-090200-0002/task.json", 5, []byte(`{"prompt":"the newest task"}`))
	if _, err := os.Stat(again.path(old)); !os.IsNotExist(err) {
		t.Errorf("the entry past %s is still at %s (%v): the write that walks the directory drops what nothing reads", MaxAge, again.path(old), err)
	}
	if got, ok := again.Get(young, 4); !ok || string(got) != `{"prompt":"the new task"}` {
		t.Errorf("the entry inside the age answered %q, %v after the sweep", got, ok)
	}
	// A cache that is only read from walks nothing: the entry nothing asks
	// for is left where it is, and the listing pays for no walk it did not
	// need (the whole reason the sweep is on this path and not on Open).
	third := testOpen(t, home, "aurora", "fugaro-runs")
	fresh := "runs/app/20261018-090300-0003/task.json"
	third.Put(fresh, 6, []byte(`{"prompt":"a task"}`))
	backdate(t, third.path(fresh), MaxAge+time.Hour)
	fourth := testOpen(t, home, "aurora", "fugaro-runs")
	if _, ok := fourth.Get(young, 4); !ok {
		t.Fatal("the entry the read is for was not answered")
	}
	if _, err := os.Stat(fourth.path(fresh)); err != nil {
		t.Errorf("opening a cache and reading one entry dropped another (%v): the age is enforced on the entry a read asks for, and by the walk a write makes", err)
	}
	// And a first open of a cache that was never written is not a failure.
	if other := testOpen(t, t.TempDir(), "borealis", "fugaro-runs"); other == nil {
		t.Fatal("a project with no cache directory opened as no cache")
	}
}

// TestARewriteIsNotExpiredByAStaleRead: Get decides an entry is expired from
// the time the filesystem reports, and a write may replace the file between
// that stat and the removal the miss makes (write renames over it). Removing
// what the write stored would lose a live entry, so the age is measured again
// under the lock -- the one place a write cannot be in flight -- and the file
// that is removed is the file that was measured.
func TestARewriteIsNotExpiredByAStaleRead(t *testing.T) {
	c, _ := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	key := "runs/app/20261018-090000-0000/task.json"
	c.Put(key, 1, []byte(`{"prompt":"the task as it was"}`))
	backdate(t, c.path(key), MaxAge+time.Hour)
	// The read's stat saw the entry above; this write replaced it before the
	// miss it decided on was carried out.
	c.Put(key, 2, []byte(`{"prompt":"the task as it is"}`))

	c.expire(c.path(key))
	if got, ok := c.Get(key, 2); !ok || string(got) != `{"prompt":"the task as it is"}` {
		t.Errorf("the entry written over the expired one answered %q, %v: an expiry of the file a read saw must not remove the file a write stored", got, ok)
	}

	// And the removal the miss makes still happens when nothing replaced it.
	backdate(t, c.path(key), MaxAge+time.Hour)
	c.expire(c.path(key))
	if _, err := os.Stat(c.path(key)); !os.IsNotExist(err) {
		t.Errorf("the expired entry is still at %s (%v)", c.path(key), err)
	}
}

// TestTheCacheIsReadableOnlyByItsOwner: the entries hold what a run's
// service account wrote into the runs bucket, and the directory and every
// file in it are the user's alone.
//
// The modes are spelled out here rather than named by this package's own
// constants: a test that compares a mode against the constant the code wrote
// it with passes whatever that constant comes to say, and what has to hold is
// the literal -- no bit for the group and none for anyone else.
func TestTheCacheIsReadableOnlyByItsOwner(t *testing.T) {
	c, dir := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	c.Put("runs/app/20261018-090000-0000/task.json", 1, []byte(`{"version":1}`))
	modes := map[string]os.FileMode{dir: 0o700}
	for _, f := range entryFiles(t, dir) {
		modes[filepath.Join(dir, f)] = 0o600
	}
	if len(modes) != 2 {
		t.Fatalf("the cache holds %d paths to check, want the directory and the one entry", len(modes))
	}
	for path, want := range modes {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %04o, want %04o: the user's own account and nothing else", path, got, want)
		}
	}
}

// TestAnEntryNameCannotLeaveTheCacheDirectory: an object name is a slug and
// a run ID read from a bucket, and this package turns one into a filename.
// The name is hashed and never used as a path, so no name -- not a
// traversal, not a separator, not a name of the filesystem's own -- decides
// where a byte is written.
func TestAnEntryNameCannotLeaveTheCacheDirectory(t *testing.T) {
	c, dir := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	hostile := []string{
		"../../../../etc/passwd",
		"..",
		"../sibling",
		"/etc/passwd",
		"a/b/c.json",
		`..\..\windows\system32`,
		"runs/app/20261018-090000-0000/" + strings.Repeat("x", 300) + ".json",
		"",
	}
	for _, name := range hostile {
		path := c.path(name)
		if filepath.Dir(path) != dir {
			t.Fatalf("the entry of %q is %s, outside the cache directory %s", name, path, dir)
		}
		c.Put(name, 1, []byte(name))
	}
	// Every entry of the cache is one of the package's own digests, and the
	// only thing beside the cache directory is the project directory it is.
	for _, f := range entryFiles(t, dir) {
		if !entryNameRE.MatchString(f) {
			t.Errorf("the cache holds %q, which is not one of its own entry names", f)
		}
	}
	siblings, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(siblings) != 1 || !siblings[0].IsDir() {
		t.Errorf("a name left the cache: beside %s the directory holds %v", dir, siblings)
	}
	// Each name is still its own entry: hashing keeps them apart.
	for _, name := range hostile {
		if got, ok := c.Get(name, 1); !ok || string(got) != name {
			t.Errorf("Get(%q) = %q, %v: the entry was not answered", name, got, ok)
		}
	}
}

// entryNameRE is the shape of an entry file: the hex SHA-256 of the bucket
// and the object name.
var entryNameRE = regexp.MustCompile(`^[0-9a-f]{64}\.json$`)

// TestOpenRefusesANameThatIsNotAProjectName: the cache is placed by project
// name, and a name that is not one is refused rather than joined onto the
// cache directory.
func TestOpenRefusesANameThatIsNotAProjectName(t *testing.T) {
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return t.TempDir()
		}
		return ""
	}
	for _, name := range []string{"", "..", "../other", "a/b", "Aurora", ".", "a b"} {
		c, err := Open(getenv, name, "fugaro-runs")
		if err == nil || c != nil {
			t.Errorf("Open(%q) = %v, %v; want no cache and the reason", name, c, err)
		}
	}
	// And a machine that names no cache directory is no cache, never a
	// failure: the caller reads the bucket as it did before.
	none := func(string) string { return "" }
	if c, err := Open(none, "aurora", "fugaro-runs"); err == nil && c != nil {
		t.Errorf("Open with no cache home = %v, want no cache", c)
	}
}

// TestTwoProjectsDoNotShareACache: the entries of two projects live in two
// directories, so one project's listing never reads another's objects -- the
// first of the two layers that keep them apart, the project name being what
// places the directory (TestAnEntryOfAnotherBucketIsNotAnswered is the second,
// inside one project's own directory).
func TestTwoProjectsDoNotShareACache(t *testing.T) {
	home := t.TempDir()
	one := testOpen(t, home, "aurora", "fugaro-runs")
	two := testOpen(t, home, "borealis", "fugaro-runs")
	if one.dir == two.dir {
		t.Fatalf("two projects share %s: an entry of one answers for the other", one.dir)
	}
	name := "runs/app/20261018-090000-0000/result.json"
	one.Put(name, 3, []byte(`{"version":1,"repo":"owner/repo-a"}`))
	if got, ok := two.Get(name, 3); ok {
		t.Errorf("the other project's cache answered %q: entries are per project", got)
	}
	if got, ok := one.Get(name, 3); !ok || !bytes.Contains(got, []byte("owner/repo-a")) {
		t.Errorf("the project's own cache answered %q, %v", got, ok)
	}
	// And the two directories are the project's name under one cache home:
	// not siblings that happen to differ, but the same rule for both.
	if filepath.Dir(one.dir) != filepath.Dir(two.dir) || filepath.Base(one.dir) != "aurora" || filepath.Base(two.dir) != "borealis" {
		t.Errorf("the caches are at %s and %s, want the two project names under one directory", one.dir, two.dir)
	}
}

// TestAnEntryHoldsTheObjectsOwnBytesAndNothingElse: the cache is a copy of
// three bucket objects, and an entry is exactly those bytes under the four
// fields that place them. Nothing else is stored -- no environment, no
// credential, no path of the machine the read happened on -- so whatever a
// run object can hold is all the cache can hold.
func TestAnEntryHoldsTheObjectsOwnBytesAndNothingElse(t *testing.T) {
	c, dir := testCache(t, t.TempDir(), "aurora", "fugaro-runs")
	for _, obj := range cachedShapes(t) {
		c.Put(obj.name, 5, obj.raw)
		file, err := os.ReadFile(c.path(obj.name))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(file, &fields); err != nil {
			t.Fatalf("%s's entry: %v", obj.name, err)
		}
		if got := slices.Sorted(maps.Keys(fields)); !slices.Equal(got, []string{"bucket", "data", "generation", "name"}) {
			t.Errorf("%s's entry holds %v, want the four fields that place the object", obj.name, got)
		}
		if got, ok := c.Get(obj.name, 5); !ok || !bytes.Equal(got, obj.raw) {
			t.Errorf("%s's entry holds %q, want the object's own bytes", obj.name, got)
		}
	}
	if files := entryFiles(t, dir); len(files) != 3 {
		t.Errorf("the cache holds %d entries, want one per object", len(files))
	}
}

// cachedObject is one of the three objects a listing caches, as its own
// writer encodes it.
type cachedObject struct {
	name string
	raw  []byte
}

// cachedShapes encodes task.json, launch.json and result.json from their own
// types, with every field the CLI fills in filled in. It is the three shapes
// the cache is asked about, encoded the way the bucket holds them.
func cachedShapes(t *testing.T) []cachedObject {
	t.Helper()
	slug, id := "app", "20261018-090000-0000"
	spec := &task.Spec{
		Version: 1, RunID: id, Repo: "owner/repo-a", Ref: "main", Workflow: "web",
		Task: "make the listing faster", RequestedBy: "person-1", Batch: "batch-a",
		OAuthToken: "work-account", Branch: "fugaro/fix-a", PR: 12, PreviousRun: "20261017-090000-0000",
	}
	taskJSON, err := spec.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	up := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	launchJSON, err := json.MarshalIndent(&runstore.Launch{
		Version: 1, RunID: id, Backend: "cloud-run", Execution: "projects/proj-1234/locations/us-east5/executions/run-a",
		Job: "fugaro-app-web", LogURL: "https://logs.example/run-a", LaunchedBy: "person-1", LaunchedAt: up,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	recordJSON, err := json.Marshal(&runstore.Record{
		Version: 1, RunID: id, Repo: "owner/repo-a", Workflow: "web", OAuthAccount: "work-account",
		Execution: "projects/proj-1234/locations/us-east5/executions/run-a", Status: runstore.StatusSucceeded,
		Stage: "finalize", Outcome: runstore.OutcomeReady, Branch: "fugaro/fix-a", BaseBranch: "main",
		PR:      &runstore.PRRef{Number: 12, URL: "https://example.invalid/owner/repo-a/pull/12"},
		CostUSD: 1.5, StartedAt: up, FinishedAt: &up, ConfigSHA256: strings.Repeat("ab", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	return []cachedObject{
		{"runs/" + slug + "/" + id + "/task.json", taskJSON},
		{"runs/" + slug + "/" + id + "/launch.json", launchJSON},
		{"runs/" + slug + "/" + id + "/result.json", recordJSON},
	}
}

// cachedSecretShapes are what a credential looks like: the prefixes the
// providers this project talks to mint, an encoded token, a PEM header, and
// the long mixed-case alphanumeric blob a base64 or hex key is.
//
// Every pattern is a shape of a credential and never a credential: none of
// them matches itself, and the positive control below assembles its examples
// at run time from harmless pieces, so this file holds no secret-shaped
// literal either (the repository's Convention 2).
var cachedSecretShapes = []struct {
	what string
	re   *regexp.Regexp
}{
	{"an API key", regexp.MustCompile(`sk-[a-z]+-[A-Za-z0-9_-]{16,}`)},
	{"a forge token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{16,}`)},
	{"an encoded token", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`)},
	{"a cloud access key id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"a PEM block", regexp.MustCompile(`-----BEGIN [A-Z ]+-----`)},
}

// cachedBlobish is a long run of letters and digits, which a base64 or hex
// key essentially always is and which prose almost never is.
var cachedBlobish = regexp.MustCompile(`\b[A-Za-z0-9]{40,}\b`)

// cachedSecretShaped is every secret-shaped run of text in s.
func cachedSecretShaped(s string) []string {
	var found []string
	for _, shape := range cachedSecretShapes {
		for _, hit := range shape.re.FindAllString(s, -1) {
			found = append(found, shape.what+": "+hit)
		}
	}
	for _, hit := range cachedBlobish.FindAllString(s, -1) {
		if strings.ContainsAny(hit, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") &&
			strings.ContainsAny(hit, "abcdefghijklmnopqrstuvwxyz") &&
			strings.ContainsAny(hit, "0123456789") {
			found = append(found, "a long alphanumeric blob: "+hit)
		}
	}
	return found
}

// TestTheCachedObjectsCarryNoSecret: the cache may hold the three run
// objects and nothing else, and this is what makes that safe -- none of the
// three carries a credential. The one field of each that is adjacent to one
// (task.json's oauth_token, result.json's oauth_account) names a Claude
// account, which is what the launching CLI validates it as
// (config.ClaudeAccountNameRE), never the token itself.
//
// The scan runs over the objects as their own writers encode them, with
// every field filled in, so a field added to one of the three shapes is
// scanned the day it is added.
func TestTheCachedObjectsCarryNoSecret(t *testing.T) {
	for _, obj := range cachedShapes(t) {
		if found := cachedSecretShaped(string(obj.raw)); len(found) != 0 {
			t.Errorf("%s carries %d secret-shaped run(s) of text: %v", obj.name, len(found), found)
		}
	}
	// The account it is supposed to hold, so the scan cannot be satisfied by
	// objects that name no account at all.
	shapes := cachedShapes(t)
	for _, tc := range []struct {
		file  string
		field string
	}{
		{"task.json", "oauth_token"},
		{"result.json", "oauth_account"},
	} {
		raw := shapes[0].raw
		if tc.field == "oauth_account" {
			raw = shapes[2].raw
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		if got := obj[tc.field]; got != "work-account" {
			t.Errorf("%s carries %s = %v, want the account name: the scan above is only meaningful over objects that name one", tc.file, tc.field, got)
		}
	}
}

// TestTheSecretScanFindsEveryShape is the positive control, and it comes
// first for the reason every fence in this repository has one: a scan whose
// patterns match nothing reports the objects clean whatever they say.
func TestTheSecretScanFindsEveryShape(t *testing.T) {
	planted := []struct {
		what string
		text string
	}{
		{"an API key", "sk" + "-ant" + "-api03-" + strings.Repeat("a", 20)},
		{"a forge token", "gh" + "p_" + strings.Repeat("b7", 15)},
		{"an encoded token", "eyJ" + strings.Repeat("A", 10) + "." + strings.Repeat("B", 10) + "." + "c2ln"},
		{"a cloud access key id", "AK" + "IA" + "QWERTYUIOPASDFGH"},
		{"a PEM block", "-----BEGIN " + "PRIVATE KEY" + "-----"},
		{"a long alphanumeric blob", strings.Repeat("Ab3", 20)},
	}
	for _, tc := range planted {
		found := cachedSecretShaped("prefix " + tc.text + " suffix")
		if len(found) == 0 {
			t.Errorf("the scan found nothing in %s", tc.what)
		}
	}
	// And the shapes are not so eager that ordinary text trips them: the
	// listing this cache serves is full of names of this shape.
	for _, clean := range []string{
		`{"run_id":"20261018-090000-0000","repo":"owner/repo-a"}`,
		"projects/proj-1234/locations/us-east5/executions/fugaro-app-web-abcde",
		"fugaro/fix-the-run-listing-20261018",
	} {
		if found := cachedSecretShaped(clean); len(found) != 0 {
			t.Errorf("the scan calls %q secret-shaped: %v", clean, found)
		}
	}
}
