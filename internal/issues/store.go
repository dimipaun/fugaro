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

// Save writes the issue under the name its number and stem give it.
func (s *Store) Save(i Issue) error {
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

// Get reads the issue named by ref: its number, or its local ID (F23).
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
// have no number yet first, so a caller prints a stable list. A file that is
// not one of ours is left alone; one of ours that does not parse is an error
// rather than an issue that quietly disappears.
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
			return nil, fmt.Errorf("issues: reading %s: %w", e.Name(), err)
		}
		i, err := Unmarshal(data)
		if err != nil {
			return nil, fmt.Errorf("issues: %s: %w", e.Name(), err)
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

// SetStatus records where the issue stands. A status outside the three is
// refused before the file is read.
func (s *Store) SetStatus(ref string, status Status) error {
	if !status.Valid() {
		return fmt.Errorf("issues: %q is not a status, want %q, %q or %q", status, StatusPending, StatusSubmitted, StatusDismissed)
	}
	i, err := s.Get(ref)
	if err != nil {
		return err
	}
	i.Status = status
	return s.Save(i)
}

// MarkUploaded records the number the shared store gave the issue and the URL
// it was filed as, and renames the file to `<NNNN>-<stem>.md` (D1723). The
// local name is gone afterwards, so a second call finds the issue by its
// number and is a no-op.
func (s *Store) MarkUploaded(ref string, number int, url string) error {
	if err := validateNumber(number); err != nil {
		return err
	}
	i, err := s.Get(ref)
	if err != nil {
		return err
	}
	i.Number, i.Uploaded, i.URL = number, true, url
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
