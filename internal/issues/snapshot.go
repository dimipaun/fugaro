package issues

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// snapshotFormat is the version of the snapshot file this package reads and
// writes. A file of any other version is "no snapshot": the shape is read by
// one binary at a time, and guessing at a newer one would print rows the
// printer no longer knows how to shape.
const snapshotFormat = 1

// installationRE accepts a digest of the runs bucket URL, the name the
// snapshot is filed under. Only hex, of any length F7's digest may grow to:
// the name is joined into a path, and a name that cannot hold a separator, a
// dot or a drive letter cannot leave the directory it is joined to.
var installationRE = regexp.MustCompile(`^[0-9a-fA-F]{1,64}$`)

// SnapshotRow is one row of a snapshot: the display data of one issue of the
// last successful `mine`, and nothing else. It is deliberately not T-IM1's
// row type: a snapshot outlives the shared read it came from, so it must not
// be able to carry that read's command or any reporter's identity into a
// later, offline run.
type SnapshotRow struct {
	// Number is the shared issue number, empty for a row that is still local
	// (LocalID names it then); Display is the derived status, and Coverage
	// what the version comparison said at the time of the read.
	Number  string `json:"number,omitempty"`
	LocalID string `json:"local_id,omitempty"`
	Summary string `json:"summary"`
	Display string `json:"display"`
	// The four dates are the row's own times, absent (nil) until the
	// transition happened; the hit time is the user's own, never the
	// upload's.
	HitAt       *time.Time `json:"hit_at,omitempty"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	DismissedAt *time.Time `json:"dismissed_at,omitempty"`
	// URL and ResolvedIn are stored as the shared record held them. The
	// printer shape-checks both before anything reaches a terminal; the store
	// keeps them as data.
	URL        string `json:"url,omitempty"`
	ResolvedIn string `json:"resolved_in,omitempty"`
	Coverage   string `json:"coverage,omitempty"`
	// OthersHit counts other people, never this user, and never their names.
	OthersHit int `json:"others_hit,omitempty"`
}

// Snapshot is the last successful `mine` for one installation. A zero
// Snapshot means there is none: AsOf is the clock of the run that wrote it,
// so a written snapshot never has a zero one, and a caller prints "as of"
// only when it is set.
type Snapshot struct {
	Format int           `json:"format"`
	AsOf   time.Time     `json:"as_of"`
	Rows   []SnapshotRow `json:"rows"`
}

// snapshotPath is <dir>/mine/<installation>.json, where dir is the fugaro
// directory in the user's cache (localcfg.UserDir with DirCache). One file
// per installation, because one machine serves several projects and an
// offline `mine` in one of them must never print another's rows.
func snapshotPath(dir, installation string) (string, error) {
	if !installationRE.MatchString(installation) {
		return "", fmt.Errorf("%q is not an installation digest", installation)
	}
	return filepath.Join(dir, "mine", installation+".json"), nil
}

// ReadSnapshot is the last successful `mine` of this installation. Anything
// wrong with the file — missing, unreadable, truncated, an unknown field or
// an unknown format version — is the zero Snapshot with no error, because
// this is the offline path and a bad cache file must never be what stops
// `mine` from printing the rows it still has. The installation name is the
// one argument that is refused: it is the only part of the path a caller
// supplies.
func ReadSnapshot(dir, installation string) (Snapshot, error) {
	path, err := snapshotPath(dir, installation)
	if err != nil {
		return Snapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, nil
	}
	var s Snapshot
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Snapshot{}, nil
	}
	// More is true for a second value after the object: a file this reader
	// would otherwise accept the first half of.
	if dec.More() || s.Format != snapshotFormat {
		return Snapshot{}, nil
	}
	return s, nil
}

// WriteSnapshot files the rows of a successful `mine`, stamped with now. It
// is best effort at the call site: a cache that cannot be written is one
// offline run without a snapshot, never an error the command reports. now is
// the caller's clock, so the "as of" line and the file agree.
func WriteSnapshot(dir, installation string, rows []SnapshotRow, now time.Time) error {
	path, err := snapshotPath(dir, installation)
	if err != nil {
		return err
	}
	data, err := json.Marshal(Snapshot{Format: snapshotFormat, AsOf: now.UTC(), Rows: rows})
	if err != nil {
		return err
	}
	return writeSnapshotFile(path, data)
}

// writeSnapshotFile writes data mode 0600 in a 0700 directory, through a
// temporary file renamed over the path: a reader sees the old snapshot or the
// new one, never a part of one. The sync before the rename means a crash
// cannot leave the name pointing at contents that never reached the disk —
// which, for this file, would be a snapshot the next offline run trusts.
func writeSnapshotFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
