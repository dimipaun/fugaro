package issueresolve

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// maxCursorFile bounds the cursor file's read: a few hundred issue numbers
// are a few KB, and anything past this is corruption or tampering, refused
// rather than buffered whole.
const maxCursorFile = 64 << 10

// readCursor reads all of r, up to maxCursorFile bytes; ok is false when r
// had more to give or the read itself failed. The bound is on the read, not
// on the file the Lstat above saw: a file swapped for a larger one, or
// grown, between that Lstat and this read is still refused unread.
func readCursor(r io.Reader) (data []byte, ok bool) {
	data, err := io.ReadAll(io.LimitReader(r, maxCursorFile+1))
	if err != nil || len(data) > maxCursorFile {
		return nil, false
	}
	return data, true
}

// cursorFile is the cursor as it is stored: the last time each issue number
// was asked about, keyed by the number in decimal.
type cursorFile struct {
	Checked map[string]time.Time `json:"checked"`
}

// Cursor remembers when each issue number was last asked about, in one file
// under the user's cache directory. A pass asks the numbers it has asked
// least recently first, so an issue that stays open for months cannot keep
// a newer one from ever being reached inside the pass's limit. The file is
// this machine's own business: losing or corrupting it costs one pass of
// asks in a different order, nothing more.
type Cursor struct {
	path    string
	checked map[int]time.Time
}

// LoadCursor reads the cursor from $XDG_CACHE_HOME/fugaro/issue-cursor.json,
// else under the platform's cache directory. Everything that is not a
// readable cursor file - none at all, a directory, a symlink, a file past
// maxCursorFile, garbage - is an empty cursor, never an error, and a single
// entry whose key is not an issue number is dropped on its own: the worst
// any of it can cost is asking about a few issues again.
func LoadCursor(getenv func(string) string) *Cursor {
	c := &Cursor{checked: map[int]time.Time{}}
	dir, err := localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirCache)
	if err != nil {
		return c
	}
	c.path = filepath.Join(dir, "fugaro", "issue-cursor.json")

	// Lstat, not Stat: a symlink (to a FIFO, to a device, anywhere) must
	// not be followed. Mark only ever puts a regular file here by renaming
	// a temporary one over the path.
	fi, err := os.Lstat(c.path)
	if err != nil || !fi.Mode().IsRegular() {
		return c
	}
	file, err := os.Open(c.path)
	if err != nil {
		return c
	}
	defer file.Close()
	data, ok := readCursor(file)
	if !ok {
		return c
	}
	var f cursorFile
	if json.Unmarshal(data, &f) != nil {
		return c
	}
	for key, at := range f.Checked {
		number, err := strconv.Atoi(key)
		if err != nil || number < 1 {
			continue
		}
		c.checked[number] = at
	}
	return c
}

// Order returns numbers least recently asked first: a number never asked
// comes before every number that has been, then the one asked longest ago
// comes first, and numbers asked at the same time (or never) keep their
// numeric order, so one pass is reproducible. numbers is not modified.
func (c *Cursor) Order(numbers []int) []int {
	out := append([]int(nil), numbers...)
	sort.Slice(out, func(i, j int) bool {
		a, b := c.checked[out[i]], c.checked[out[j]]
		if !a.Equal(b) {
			return a.Before(b)
		}
		return out[i] < out[j]
	})
	return out
}

// Mark records numbers as asked at now and stores the cursor, atomically
// and mode 0600 in a 0700 directory: a reader sees the old file or the new
// one, never a part. A cursor that cannot be written is not a failure of
// the pass - the next one asks the same numbers again.
func (c *Cursor) Mark(numbers []int, now time.Time) error {
	if c.path == "" {
		return fmt.Errorf("this machine names no usable cache directory")
	}
	for _, number := range numbers {
		if number < 1 {
			continue
		}
		c.checked[number] = now
	}
	f := cursorFile{Checked: make(map[string]time.Time, len(c.checked))}
	for number, at := range c.checked {
		f.Checked[strconv.Itoa(number)] = at
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}

	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.path)
}
