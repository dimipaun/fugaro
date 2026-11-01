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
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
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
// CLI). The directory is created by the first write, so a command that caches
// nothing leaves nothing behind.
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
// another object, another bucket, a file that is not an entry at all, and a
// cache that could not be placed. A miss always means the caller reads the
// bucket.
func (c *Cache) Get(name string, gen int64) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	path := c.path(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var e entry
	if json.Unmarshal(data, &e) != nil {
		return nil, false // a torn or corrupt entry is a miss, rewritten by the next read
	}
	if e.Bucket != c.bucket || e.Name != name || e.Generation != gen {
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
func (c *Cache) Put(name string, gen int64, data []byte) {
	if c == nil {
		return
	}
	blob, err := json.Marshal(entry{Bucket: c.bucket, Name: name, Generation: gen, Data: data})
	if err != nil {
		return
	}
	if err := c.write(name, blob); err != nil {
		return
	}
	c.evict()
}

// path is where name's entry lives: the digest of the bucket and the name, so
// no byte of the name reaches the filesystem.
func (c *Cache) path(name string) string {
	sum := sha256.Sum256([]byte(c.bucket + "\x00" + name))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".json")
}

// write stores blob at name's entry through a temporary file renamed over it,
// so a reader sees the old entry or the new one and never a part of either.
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

// evict drops the least recently used entries while the cache is over its cap.
// The entry just written is the newest, so a listing that keeps reading the
// same runs never drops what it is reading.
func (c *Cache) evict() {
	type cached struct {
		path string
		used time.Time
		size int64
	}
	var entries []cached
	var total int64
	items, err := os.ReadDir(c.dir)
	if err != nil {
		return
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
		return
	}
	slices.SortFunc(entries, func(a, b cached) int { return a.used.Compare(b.used) })
	for _, e := range entries {
		if total <= c.max {
			return
		}
		if err := os.Remove(e.path); err != nil {
			continue
		}
		total -= e.size
	}
}
