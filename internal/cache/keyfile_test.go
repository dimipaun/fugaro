package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// A key file the agent (or a commit) turns into a FIFO or a link to
// /dev/zero must not hang the key: it is left out, with a warning.
func TestKeyOfSkipsKeyFilesThatAreNotRegular(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, p string){
		"fifo":     func(t *testing.T, p string) { must(t, syscall.Mkfifo(p, 0o644)) },
		"dev-zero": func(t *testing.T, p string) { must(t, os.Symlink("/dev/zero", p)) },
		"link-in":  func(t *testing.T, p string) { must(t, os.Symlink("README.md", p)) },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, map[string]string{"README.md": "x"})
			plant(t, filepath.Join(root, "package-lock.json"))
			e := config.CacheEntry{Key: []string{"package-lock.json"}, Paths: []string{"~/.npm"}}
			done := make(chan error, 1)
			go func() {
				var warned []string
				_, ok, err := KeyOf(context.Background(), root, e, "", "tc", func(msg string, args ...any) { warned = append(warned, msg) })
				if err != nil || ok || len(warned) != 1 {
					t.Errorf("KeyOf over a %s = ok %v, err %v, warnings %q; want it left out with a warning", name, ok, err, warned)
				}
				done <- err
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("KeyOf still reading a %s after 5s", name)
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// The other key files still make the key when one is left out.
func TestKeyOfKeepsTheRegularKeyFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"a.lock": "x"})
	must(t, syscall.Mkfifo(filepath.Join(root, "b.lock"), 0o644))
	e := config.CacheEntry{Key: []string{"*.lock"}, Paths: []string{"~/.npm"}}
	if _, ok, err := KeyOf(context.Background(), root, e, "", "tc", nil); err != nil || !ok {
		t.Fatalf("KeyOf = ok %v, err %v", ok, err)
	}
}

// An oversized key file makes the entry not cached, and an ended context
// stops the key.
func TestKeyOfCapsAndContext(t *testing.T) {
	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "big.lock"))
	must(t, err)
	must(t, f.Truncate(maxKeyFileBytes+1))
	must(t, f.Close())
	e := config.CacheEntry{Key: []string{"big.lock"}, Paths: []string{"~/.npm"}}
	if _, ok, err := KeyOf(context.Background(), root, e, "", "tc", nil); ok || !errors.Is(err, ErrKeyTooLarge) {
		t.Fatalf("KeyOf over an oversized key file = ok %v, err %v", ok, err)
	}
	write(t, root, map[string]string{"small.lock": "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := KeyOf(ctx, root, config.CacheEntry{Key: []string{"small.lock"}, Paths: []string{"~/.npm"}}, "", "tc", nil); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("KeyOf on an ended context = ok %v, err %v", ok, err)
	}
}
