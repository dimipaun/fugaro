package pluginwire

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// maxSettingsBytes bounds what is read: a settings file is a few lines.
const maxSettingsBytes = 1 << 20

// userConfigDir is Claude Code's per-user directory (the user's settings
// live there), which is never a project. Tests replace it.
var userConfigDir = func() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".claude")
	}
	return ""
}

// checkPath refuses a path that is not <root>/.claude/settings.json, whose
// .claude directory is the user's own, or where the directory or the file
// is a symlink (a link could point out of the repository) or not a plain
// directory or file. A missing directory or file is fine.
func checkPath(path string) error {
	if filepath.Base(path) != "settings.json" || filepath.Base(filepath.Dir(path)) != ".claude" {
		return fmt.Errorf("%s is not a project's .claude/settings.json", path)
	}
	dir := filepath.Dir(path)
	di, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case di.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink; refusing to write through it", dir)
	case !di.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	}
	if u := userConfigDir(); u != "" {
		if ui, err := os.Stat(u); err == nil && os.SameFile(ui, di) {
			return fmt.Errorf("%s is Claude Code's user settings directory, not a project's; it is never touched", dir)
		}
	}
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case fi.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink; refusing to follow it", path)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// readFile reads path without following a symlink; a missing file is
// (nil, false, nil).
func readFile(path string, limit int64) (data []byte, perm fs.FileMode, exists bool, err error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("%s is not a regular file", path)
	}
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, 0, false, err
	}
	if int64(len(data)) > limit {
		return nil, 0, false, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return data, fi.Mode().Perm(), true, nil
}

// replaceFile writes data to path atomically: a temporary file beside it,
// synced, then renamed. It keeps perm (0644 for a new file) and refuses when
// the file is no longer what the caller read (before, or absent when
// existed is false), so a concurrent edit is never overwritten.
func replaceFile(path string, data, before []byte, existed bool, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := checkPath(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
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
	cur, _, nowExists, err := readFile(path, maxSettingsBytes)
	if err != nil {
		return err
	}
	if nowExists != existed || !bytes.Equal(cur, before) {
		return fmt.Errorf("%s changed while it was being updated; nothing was written, run the command again", path)
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
