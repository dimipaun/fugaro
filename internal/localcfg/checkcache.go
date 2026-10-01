package localcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// nameCheckTTL is how long a successful check of a project's cloud name
// stays fresh.
const nameCheckTTL = 24 * time.Hour

// NameCheck is the result of the last successful check that a project
// config points at its own installation (fugaro/project.json in the runs
// bucket named the project). It is cached per project so a day of commands
// costs one read.
type NameCheck struct {
	GCPProject string    `json:"gcp_project"`
	RunsBucket string    `json:"runs_bucket"`
	CheckedAt  time.Time `json:"checked_at"`
}

// nameCheckPath is $XDG_CACHE_HOME/fugaro/project-check/<name>.json, else
// under ~/.cache.
func nameCheckPath(getenv func(string) string, name string) (string, error) {
	if !config.ProjectNameRE.MatchString(name) {
		return "", fmt.Errorf("%q is not a project name", name)
	}
	dir := getenv("XDG_CACHE_HOME")
	if dir == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("neither XDG_CACHE_HOME nor HOME is set, so the project check can't be cached")
		}
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "fugaro", "project-check", name+".json"), nil
}

// CachedNameCheck is the project's last check when it is less than a day
// old. Any trouble (no cache, a corrupt file, a timestamp from the future)
// is a miss. Callers compare GCPProject and RunsBucket with their config's
// and re-check when either differs.
func CachedNameCheck(getenv func(string) string, name string, now time.Time) (NameCheck, bool) {
	path, err := nameCheckPath(getenv, name)
	if err != nil {
		return NameCheck{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return NameCheck{}, false
	}
	var c NameCheck
	if json.Unmarshal(data, &c) != nil {
		return NameCheck{}, false
	}
	if age := now.Sub(c.CheckedAt); age < 0 || age >= nameCheckTTL {
		return NameCheck{}, false
	}
	return c, true
}

// SaveNameCheck stores the check, mode 0600. It is best effort: the
// caller ignores the error, and the next command checks again.
func SaveNameCheck(getenv func(string) string, name string, c NameCheck) error {
	path, err := nameCheckPath(getenv, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".check-*")
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
	return os.Rename(tmp.Name(), path)
}
