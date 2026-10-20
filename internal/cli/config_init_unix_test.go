//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// config init's write is a create with O_EXCL and O_NOFOLLOW where the
// platform has the flag (config_init.go, openfile_unix.go): a symlink
// planted at fugaro.yaml — by a checkout that arrived that way, or by a
// process racing the command — must be refused, not written through, and
// the file the link points at must not appear.
//
// Two refusals stand between the link and the write: writeMinimalFugaroYAML's
// Lstat, which refuses a link already at the path on every platform, and the
// flag, which is for one that appeared after the Lstat. The link here is
// already there when the call is made, so the Lstat is what answers — and on
// Unix the flag is inert anyway, because O_EXCL alone refuses a symlink at the
// path (measured: setting oNoFollow to 0 here leaves this test green, which is
// the honest negative openfile_unix.go and the PR body both record).
//
// Mutations, both measured:
//
//   - writeMinimalFugaroYAML's body to os.WriteFile(path, text, 0o644): the
//     dangling link is followed and the target created, so it fails on "the
//     create wrote through a symlink".
//   - its Lstat refusal removed, the create kept: nothing is written through
//     the link — O_EXCL still refuses it — but the error becomes the raw
//     "file exists", so it fails on "does not say what it refused". That
//     assertion is what guards the Lstat half; without it this test would not
//     notice, and the Lstat is what carries the refusal to a platform whose
//     O_EXCL create follows a dangling link (openfile_other.go).
//
// The unix tag is for os.Symlink and for the flag half of the pair; a
// Windows-runnable version of this row belongs to Slice 3's T3.5 (F36).
func TestConfigInitWriteRefusesASymlinkedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fugaro.yaml")
	target := filepath.Join(dir, "elsewhere.yaml")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := writeMinimalFugaroYAML(path, "version: 1\nproject: aurora\n"); err == nil {
		t.Fatal("the create wrote through a symlink")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the refusal %q does not say what it refused", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("wrote through the symlink: %v", err)
	}
	// The same call still writes where there is nothing in the way, so the
	// refusal is about the link and not about the directory.
	if err := writeMinimalFugaroYAML(filepath.Join(dir, "fresh.yaml"), "version: 1\n"); err != nil {
		t.Fatalf("writing where nothing is in the way: %v", err)
	}
}
