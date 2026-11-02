// Package runcache is the CLI's local cache of a project's run objects.
//
// A run listing reads three objects per run of its window -- task.json,
// launch.json and result.json -- and the same window is listed again and
// again: fugaro runs ls, its --watch redraws, the report command's rows. The
// cache is keyed by object name and the object's generation: ONE flat listing
// of the runs bucket carries every object's generation, and an object the
// listing shows at the generation the cache already holds is served from the
// local copy instead of being read a second time.
//
// Never assume immutability. A run's result.json is rewritten by the runner
// (runstore.WriteRecord, runstore.ReplaceRecordIf) as a run progresses, and
// task.json has more than one writer: an entry is trusted only for the exact
// generation the listing reported, and any other generation is read again. A
// cached object is a snapshot of the bucket taken when the listing ran, which
// is why it may serve DISPLAY NUMBERS ONLY -- fugaro runs ls, the report
// command's rows, the coordinator view when it adopts the cache (a later
// task). It must never serve a decision: whether a follow-up may launch, what
// a gate reads, what a budget check counts. Those reads have to see the bucket
// as it is now, and the caller that makes one reads the bucket itself: the
// zero Reader, which is what --no-cache and every decision path pass.
//
// Where it lives. $XDG_CACHE_HOME/fugaro/runs/<project>/, directories 0700 and
// files 0600, each entry named by a hash of the bucket and the object name and
// never by the object name itself: an object name carries a slug and a run ID,
// and this package never turns one into a path. The whole directory can be
// deleted at any time and a listing can be asked to ignore it (--no-cache):
// the cache changes only speed, so a cache that cannot be placed, written or
// read is no cache, never a failure of the command.
//
// A copy does not stay. An entry nothing has read for MaxAge (a week) is a
// miss, and the miss removes it, so a run's task text -- the object this cache
// is most worth keeping out of reach -- cannot outlive the bucket's own
// retention on the machine: it is dropped by the first read past the age, and
// by the first write to the cache for the entries no read comes back for, and
// the size cap (MaxBytes) drops what nothing has read for longest before that.
//
// That read path is also why nothing walks the directory for the age alone. A
// listing that is served entirely from the cache writes nothing and so reads
// not one file it did not need: the walk over the entries is part of writing
// to the cache, which is where the size the cap counts against is needed
// anyway.
//
// No secret is ever cached. The three objects carry none -- task.json's
// oauth_token is a named account, not a token -- and an entry holds one
// object's own bytes and nothing else: no environment, no credential and no
// path of the machine it was read on.
package runcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
)

const (
	// dirPerm and filePerm are the cache's own permissions: an entry holds what
	// a run's service account wrote into the runs bucket, which is the user's
	// alone on their machine.
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600

	// MaxBytes caps the whole cache directory. The three objects of one run are
	// a few kilobytes, so this holds a large installation's window; past it the
	// entries nobody has read for longest are dropped.
	MaxBytes = 64 << 20

	// maxObject caps one entry's object. Every object this cache holds is one
	// a run listing reads under runstore's own record cap, so an entry whose
	// object is larger than that is not one the bucket handed out -- a file
	// of another version, or one put there by hand -- and serving it would
	// have the CLI decode an object the bucket itself refuses
	// (runstore.ErrTooLarge), past the bound that keeps a run's own object
	// from exhausting the operator's memory.
	maxObject = runstore.MaxRecordBytes

	// MaxAge is how long a copy of an object is kept. A read is what makes an
	// entry young again (Get touches it), so this is the age of the bytes and
	// not the age of the directory: a run's task text, which is what a task's
	// prompt is, is gone from the machine a week after the last time a
	// listing wanted it -- whichever comes first, the bucket's own retention
	// or this, and never the machine's disk.
	MaxAge = 7 * 24 * time.Hour

	// maxEntry is the largest file an entry within maxObject can be. The
	// object's bytes are base64 in the JSON envelope, so the file is a third
	// larger than the object, and the rest is the fixed fields that place it.
	// A file past this is refused on its size alone, before a byte of it is
	// read: what the bucket bounds at the read, the cache bounds at the stat.
	// (maxObject+2)/3 rounds the third up, so the ceiling is base64's own
	// ceil(n/3)*4 and the headroom below is only the fixed fields.
	maxEntry = (maxObject+2)/3*4 + 64<<10
)

// Cache is one project's run-object cache: a directory of entries, each holding
// one object's bytes at the generation they were read at.
//
// A Cache the CLI could not place is nil, and every method answers "no entry":
// the caller reads the bucket as it did before the cache existed.
type Cache struct {
	dir    string
	bucket string
	max    int64

	// mu guards total, measured and the files themselves: Put's write and
	// sweep, and the removal an expired read makes, are one at a time, so a
	// write cannot land between the stat an expiry decided on and the removal
	// it makes. total is what the entries take as against max, so that a
	// listing of thousands of objects does not walk the directory once per
	// object: the walk happens when the count says the cap may be reached, and
	// it is what decides the count again.
	mu       sync.Mutex
	total    int64
	measured bool
}

// entry is one cached object: the bucket and object name it belongs to, the
// generation its bytes were read at, and those bytes. The bucket and the name
// are stored beside the generation so that a hit can tell a colliding hash, or
// a directory written from another bucket, from its own object.
type entry struct {
	Bucket     string `json:"bucket"`
	Name       string `json:"name"`
	Generation int64  `json:"generation"`
	Data       []byte `json:"data"`
}

// Open places the cache of project's run objects of the bucket named bucket,
// under the user's cache directory: $XDG_CACHE_HOME/fugaro/runs/<project>/,
// else under ~/.cache. getenv is the environment reader (os.Getenv in the
// CLI). The directory is created by the first write, and it is only read from
// as the entries are asked for, so a command that caches nothing leaves
// nothing behind and touches nothing that is there.
//
// A project name that is not one, a machine that names no usable cache
// directory: a nil cache and the error that says why. Callers treat that as no
// cache rather than as a failure, which is what the cache is: speed, and only
// speed.
func Open(getenv func(string) string, project, bucket string) (*Cache, error) {
	if !config.ProjectNameRE.MatchString(project) {
		return nil, fmt.Errorf("%q is not a project name", project)
	}
	dir, err := localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirCache)
	if err != nil {
		return nil, fmt.Errorf("%w, so the run cache can't be written", err)
	}
	return &Cache{dir: filepath.Join(dir, "fugaro", "runs", project), bucket: bucket, max: MaxBytes}, nil
}

// Get answers the bytes of the object name at generation gen, when the cache
// holds them. Every other answer is a miss with no error: another generation,
// another object, another bucket, a file that is not an entry at all, an
// entry whose object is past the read cap (maxObject), a file past the size
// any entry can be (maxEntry), and a cache that could not be placed. A miss
// always means the caller reads the bucket -- and the caller's own read is
// what enforces the cap, so an entry larger than it is answered the way the
// bucket answers the object: with a read of the bucket, never with bytes no
// read of the bucket would have handed out.
//
// The size and the age are the ones the filesystem reports, read before the
// file is: a file too large to be an entry is refused without being read into
// memory, which is the bound the bucket keeps on its own reads, and an entry
// nothing has read for MaxAge is refused and removed (expire). What the file
// holds is what decides the entry, so the object inside is measured again once
// it has been read.
func (c *Cache) Get(name string, gen int64) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	path := c.path(name)
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxEntry {
		return nil, false
	}
	if time.Since(info.ModTime()) > MaxAge {
		c.expire(path)
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var e entry
	if json.Unmarshal(data, &e) != nil {
		return nil, false // a torn or corrupt entry is a miss, rewritten by the next read
	}
	if e.Bucket != c.bucket || e.Name != name || e.Generation != gen || len(e.Data) > maxObject {
		return nil, false
	}
	// A read is a use: what eviction drops first is what nothing has read for
	// longest.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return e.Data, true
}

// Put stores data as the object name at generation gen, and drops the entries
// that no longer fit under the cap. It is best effort: a cache that cannot be
// written is not a failure of the command that read the object, so nothing
// here is reported.
//
// The sweep and the write are one step, under the lock: a listing that is
// served entirely from the cache writes nothing and so walks nothing, which is
// the case a listing of a large window is in almost every time it runs.
func (c *Cache) Put(name string, gen int64, data []byte) {
	if c == nil {
		return
	}
	blob, err := json.Marshal(entry{Bucket: c.bucket, Name: name, Generation: gen, Data: data})
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep()
	if err := c.write(name, blob); err != nil {
		return
	}
	c.note(int64(len(blob)))
}

// sweep drops the entries past MaxAge and takes what the ones left hold, once
// per cache: it is the walk that says what the directory holds, and the entries
// nothing ever asks for again -- the ones a read never comes back for -- leave
// the machine with it. Called with c.mu held, and before the write whose size
// note adds, so the count it leaves is the directory's.
//
// Every failure here is a cache that holds a little more than it should: the
// entries it could not read are counted and left, and the ones it could not
// remove are counted again by the eviction that passes over them.
func (c *Cache) sweep() {
	c.total, c.measured = 0, true
	items, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, it := range items {
		if it.IsDir() || filepath.Ext(it.Name()) != ".json" {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > MaxAge && os.Remove(filepath.Join(c.dir, it.Name())) == nil {
			continue
		}
		c.total += info.Size()
	}
}

// expire drops the entry a read found past MaxAge, and takes what it held off
// the count. The age is measured again here, with the cache locked and every
// write locked out: the stat the read decided on is the state of a file a
// write may have replaced since (write renames over it), and removing what
// that write stored would lose a live entry in place of the one that expired.
// What leaves the count is the size of the file that was actually removed.
func (c *Cache) expire(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) <= MaxAge {
		return // replaced by a fresh write, or gone already
	}
	if os.Remove(path) != nil {
		return
	}
	if c.measured && c.total >= info.Size() {
		c.total -= info.Size()
	}
}

// note records what the entry just written added, and evicts when that may
// have taken the cache past its cap. The walking of the directory that says
// whether it has is left to the eviction, so a listing that keeps the cache
// under its cap -- the ordinary case -- never walks it. Called with c.mu held.
//
// An entry written over another one counts as an addition rather than as the
// difference, so the count can run ahead of the directory: that only evicts
// sooner, and every eviction re-measures.
func (c *Cache) note(added int64) {
	c.total += added
	if c.total <= c.max {
		return
	}
	c.total = c.evict()
}

// path is where name's entry lives: the digest of the bucket and the name, so
// no byte of the name reaches the filesystem.
func (c *Cache) path(name string) string {
	sum := sha256.Sum256([]byte(c.bucket + "\x00" + name))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}

// write stores blob at name's entry through a temporary file renamed over it,
// so a reader sees the old entry or the new one and never a part of either.
// Called with c.mu held (Put).
func (c *Cache) write(name string, blob []byte) error {
	if err := os.MkdirAll(c.dir, dirPerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(filePerm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path(name))
}

// evict drops the least recently used entries while the cache is over its cap,
// and answers what the entries left take. The entry just written is the newest,
// so a listing that keeps reading the same runs never drops what it is reading.
func (c *Cache) evict() int64 {
	type cached struct {
		path string
		used time.Time
		size int64
	}
	var entries []cached
	var total int64
	items, err := os.ReadDir(c.dir)
	if err != nil {
		return 0
	}
	for _, it := range items {
		if it.IsDir() || filepath.Ext(it.Name()) != ".json" {
			continue // a temporary file of a write in flight, or a foreign file
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		entries = append(entries, cached{filepath.Join(c.dir, it.Name()), info.ModTime(), info.Size()})
		total += info.Size()
	}
	if total <= c.max {
		return total
	}
	slices.SortFunc(entries, func(a, b cached) int { return a.used.Compare(b.used) })
	for _, e := range entries {
		if total <= c.max {
			break
		}
		if err := os.Remove(e.path); err != nil {
			continue
		}
		total -= e.size
	}
	return total
}
