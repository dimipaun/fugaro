package watch

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ackPrune is how long an acknowledgement is kept: past this it is dropped
// on the next write, so the file does not grow forever (design
// generic-tool §10.3).
const ackPrune = 7 * 24 * time.Hour

// ackMaxBytes bounds how much of the acks file LoadAcks reads: a real one
// holds at most a handful of small entries, so anything past this is not
// trusted (a huge or hostile file at this path must not make every `fugaro
// watch` read it all into memory). It is read as "too big", the same as a
// corrupt file: the empty set.
const ackMaxBytes = 1 << 20 // 1 MiB

// Acks is a viewer's local acknowledgements of failed finished runs (design
// generic-tool §10.3): best effort, per machine, never shared. Losing the
// file only shows those failures again.
type Acks struct {
	path string
	at   map[string]time.Time
}

func ackKey(slug, run string) string { return slug + "/" + run }

// LoadAcks never fails: a missing, oversized, or unreadable-as-the-expected-
// shape file is the empty set. JSON null (or any value whose shape is not an
// object of timestamps) unmarshals into a nil map with no error from
// encoding/json; treating a nil result the same as an error keeps a.at a
// live, writable map, so the next Ack never panics assigning into it.
func LoadAcks(path string) *Acks {
	a := &Acks{path: path, at: map[string]time.Time{}}
	f, err := os.Open(path)
	if err != nil {
		return a
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || fi.Size() > ackMaxBytes {
		return a // too big to trust; never read any of it
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return a
	}
	var raw map[string]time.Time
	if err := json.Unmarshal(b, &raw); err != nil || raw == nil {
		return a
	}
	a.at = raw
	return a
}

// Has reports whether slug/run is acknowledged.
func (a *Acks) Has(slug, run string) bool {
	_, ok := a.at[ackKey(slug, run)]
	return ok
}

// Ack records slug/run as acknowledged as of now, prunes entries older than
// ackPrune, and persists the set atomically (temp file plus rename), mode
// 0600.
func (a *Acks) Ack(slug, run string, now time.Time) error {
	a.at[ackKey(slug, run)] = now
	for k, t := range a.at {
		if now.Sub(t) > ackPrune {
			delete(a.at, k)
		}
	}
	return a.save()
}

func (a *Acks) save() error {
	b, err := json.Marshal(a.at)
	if err != nil {
		return err
	}
	dir := filepath.Dir(a.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".watch-acks-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, a.path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// AckPath is the default acknowledgements file: $XDG_STATE_HOME's
// fugaro/watch-acks.json, or ~/.local/state's when XDG_STATE_HOME is unset.
func AckPath(getenv func(string) string) string {
	base := getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "fugaro", "watch-acks.json")
}
