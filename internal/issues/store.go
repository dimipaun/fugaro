package issues

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

const (
	// dirMode is the issues folder's mode: what is in it is the user's own
	// text, and it can name a path or a project.
	dirMode fs.FileMode = 0o700
	// fileMode is an issue file's mode.
	fileMode fs.FileMode = 0o600
	// appDir is the directory the CLI keeps under the state directory.
	appDir = "fugaro"
	// storeDir is the issues folder inside it.
	storeDir = "issues"
	// fileSuffix ends an issue file's name.
	fileSuffix = ".md"
	// tmpPattern names a file being written, which is hidden, is not an issue
	// file, and is renamed over the target once it is whole.
	tmpPattern = ".issue-*.tmp"
)

// Store is the issues folder: one file per issue, named by the run stem until
// the issue is uploaded and by its number after (D1723). Every write is
// atomic and every file and the folder itself are the user's alone.
type Store struct {
	dir string
	// Installation names the project this store's issues belong to: the digest
	// of the runs bucket URL (F8b). Save stamps it on a record that names none,
	// so a machine that serves two projects never leaves one project's issue
	// where the other's `sync` would upload it or the other's `mine` would
	// print it — and never relabels an issue that already belongs to one.
	// Empty is a store opened with no project to name, which stamps nothing and
	// leaves whatever the record already carried.
	Installation string
}

// OpenStore places the store in the state directory the CLI keeps:
// `<localcfg.UserDir state>/fugaro/issues`, beside the logs and the terraform
// workdirs, which is the one directory rule the CLI has (F5).
func OpenStore(getenv func(string) string, goos string) (*Store, error) {
	base, err := localcfg.UserDir(getenv, goos, localcfg.DirState)
	if err != nil {
		return nil, fmt.Errorf("issues: no state directory to keep issues in: %w", err)
	}
	return &Store{dir: filepath.Join(base, appDir, storeDir)}, nil
}

// Dir is the folder the store keeps its issues in.
func (s *Store) Dir() string { return s.dir }

// Save writes the issue under the name its number and stem give it, stamped
// with the installation this store holds: an issue is saved by the project
// whose command failed, and the file outlives that command (F8b).
//
// The stamp only goes on a record that names no installation. SetStatus and
// MarkUploaded rewrite a record through this call, so a store that relabelled
// everything it wrote would hand a project's issue to whichever project wrote
// it last: an issue filed in A and dismissed on a machine working in B would
// become B's, and the listing and `sync` that filter on this field would carry
// and print it as the wrong project's. A store with no installation of its own
// stamps nothing either way.
func (s *Store) Save(i Issue) error {
	if s.Installation != "" && i.Installation == "" {
		i.Installation = s.Installation
	}
	name, err := i.FileName()
	if err != nil {
		return err
	}
	data, err := Marshal(i)
	if err != nil {
		return err
	}
	return s.write(name, data)
}

// Get reads the issue named by ref: its number, or its local ID (F23). It
// looks through List, so a record beside it that does not read back does not
// stop it; that record is simply not found.
func (s *Store) Get(ref string) (Issue, error) {
	number, localID, err := parseRef(ref)
	if err != nil {
		return Issue{}, err
	}
	all, err := s.List()
	if err != nil {
		return Issue{}, err
	}
	for _, i := range all {
		if (localID != "" && i.LocalID == localID) || (localID == "" && i.Number == number) {
			return i, nil
		}
	}
	return Issue{}, fmt.Errorf("issues: no issue %s", ref)
}

// List reads every issue in the folder, ordered by number with the ones that
// have no number yet first, so a caller prints a stable list.
//
// A file that is not one of ours is left alone, and so is one of ours that does
// not read back: a hand-edit or a record from a newer version is one issue a
// person can still open, while failing the whole folder would take `list`,
// `show`, `dismiss` and every upload down with it — and there is no command
// that removes one. It stays where it is.
func (s *Store) List() ([]Issue, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("issues: reading %s: %w", s.dir, err)
	}
	var out []Issue
	for _, e := range entries {
		if e.IsDir() || !isIssueFile(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		i, err := Unmarshal(data)
		if err != nil {
			continue
		}
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Number != out[b].Number {
			return out[a].Number < out[b].Number
		}
		return out[a].LocalID < out[b].LocalID
	})
	return out, nil
}

// SetStatus records where the issue stands. A status outside the four is
// refused before the file is read.
//
// Setting an issue aside stamps when it was set aside, from the caller's clock:
// this machine's own copy of the record is the only one an issue dismissed
// before it was uploaded ever gets, and the retirement of a finished issue
// needs the date (D1781, F4). Setting aside an issue that is already set aside
// keeps the stamp it has, because the date says when the issue was set aside
// and not when the command was run again.
func (s *Store) SetStatus(ref string, status Status, now time.Time) error {
	if !status.Valid() {
		return fmt.Errorf("issues: %q is not a status, want %q, %q, %q or %q", status, StatusPending, StatusSubmitted, StatusDismissed, StatusResolved)
	}
	i, err := s.Get(ref)
	if err != nil {
		return err
	}
	if status == StatusDismissed && i.Status != StatusDismissed {
		i.DismissedAt = stamp(now)
	}
	i.Status = status
	return s.Save(i)
}

// MarkUploaded records the number the shared store gave the issue and the URL
// it was filed as, and renames the file to `<NNNN>-<stem>.md` (D1723). The
// local name is gone afterwards, so a second call finds the issue by its
// number and is a no-op.
//
// A caller that names no URL leaves the one the record already carries: `issue
// submit` files with the person's own `gh` and records the link it showed them
// while the record is still `uploaded: false` (T-IS7), and the shared store's
// own upload numbers the issue without having a link of its own (SyncPending,
// Uploaded). Clearing the field there would take away the only place that link
// is kept, and nothing restores it.
func (s *Store) MarkUploaded(ref string, number int, url string) error {
	if err := validateNumber(number); err != nil {
		return err
	}
	i, err := s.Get(ref)
	if err != nil {
		return err
	}
	i.Number, i.Uploaded = number, true
	if url != "" {
		i.URL = url
	}
	if err := s.Rename(i); err != nil {
		return err
	}
	return s.Save(i)
}

// Rename moves the issue's file from its local name to the name its number
// gives it (D1723). It is a no-op when the file is already there, which is
// what makes MarkUploaded safe to retry.
func (s *Store) Rename(i Issue) error {
	to, err := i.FileName()
	if err != nil {
		return err
	}
	from := i.LocalID + fileSuffix
	if from == to {
		return nil
	}
	err = os.Rename(filepath.Join(s.dir, from), filepath.Join(s.dir, to))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("issues: renaming %s to %s: %w", from, to, err)
	}
	return nil
}

// write puts data in the folder atomically: a hidden temporary file beside
// the target, 0600, renamed over it, so a reader never sees half a record and
// a crash leaves the old one.
func (s *Store) write(name string, data []byte) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, tmpPattern)
	if err != nil {
		return fmt.Errorf("issues: writing %s: %w", name, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("issues: writing %s: %w", name, err)
	}
	if err := tmp.Chmod(fileMode); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("issues: writing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("issues: writing %s: %w", name, err)
	}
	if err := os.Rename(tmpName, filepath.Join(s.dir, name)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("issues: writing %s: %w", name, err)
	}
	return nil
}

// ensureDir creates the issues folder 0700, or tightens one that is wider:
// the folder is the user's, and a file left group-readable would hand a
// teammate's path or project name to every account on the machine.
func (s *Store) ensureDir() error {
	if err := os.MkdirAll(s.dir, dirMode); err != nil {
		return fmt.Errorf("issues: %w", err)
	}
	if err := os.Chmod(s.dir, dirMode); err != nil {
		return fmt.Errorf("issues: %w", err)
	}
	return nil
}

// isIssueFile reports whether a name in the folder is one this package wrote:
// `<NNNN>-<stem>.md` or `local-<stem>.md`. Anything else — an editor's
// backup, a temporary file a crash left — is not read.
func isIssueFile(name string) bool {
	base, ok := strings.CutSuffix(name, fileSuffix)
	if !ok {
		return false
	}
	if stem, ok := strings.CutPrefix(base, localPrefix); ok {
		return validateRun(stem) == nil
	}
	number, rest, ok := strings.Cut(base, "-")
	if !ok || !digits(number) {
		return false
	}
	n, err := strconv.Atoi(number)
	if err != nil || validateNumber(n) != nil {
		return false
	}
	return validateRun(rest) == nil
}
