// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package licence

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The CLI's keeper of the licence key (docs/licensing/licence-key.md, "Storage
// and hand-off"): one file in the CLI's own config directory, and the one
// variable a console hands a spawned CLI over in.
//
// **A key never arrives in argv**, because a key in a command line is a key
// every local process reads. The console therefore hands it to the child it
// spawns through the environment, and the CLI prefers that over its own file so
// the console's installation wins.
//
// EnvironmentKey is the variable, and the read removes it in the same step:
// the CLI starts children of its own -- git, the plugin install, terraform, the
// runner in `fugaro exec` -- and each inherits the environment as it stands at
// the moment it is started. A CLI that only read the variable would hand the
// organisation's key to every one of them (F41).
const EnvironmentKey = "FUGARO_LICENCE_KEY"

// keyFileName is the file inside the CLI's own config directory.
const keyFileName = "licence.key"

// maxInstalled is how many keys the store holds. One, and the renewal's 30-day
// overlap makes two (T26): the key that follows arrives before its predecessor
// has run out, and the store keeps both so the one that applies is chosen by
// its expiry rather than by which line was written last.
const maxInstalled = 2

// keyFileMode is the mode the key file is kept at: the group's and the other
// users' bits are refused rather than tolerated, because a key any local user
// can read is the file the design keeps out of argv for the same reason.
const keyFileMode = 0o600

// Store is the CLI's keys: the key file in the config directory, or the one key
// FUGARO_LICENCE_KEY carried. Exactly one of the two is set -- a console that
// handed a key over is the whole installation, and the file is not consulted.
type Store struct {
	// path is the key file. Empty when the environment handed a key over.
	path string
	// handed is the key the environment carried, already taken out of this
	// process's own environment.
	handed []byte
}

// Open is the registration point's call. It takes FUGARO_LICENCE_KEY out of
// this process's own environment as its first step, whatever else it goes on to
// do, and answers the store that key applies through.
//
// The environment is read through getenv rather than os.Getenv directly, the
// way internal/localcfg reads it, so a caller -- a test, or a future
// `fugaro licence` command with a config flag -- decides which environment
// names the config directory. A console that handed a key over needs no
// directory at all, and then none is looked for.
func Open(getenv func(string) string) (*Store, error) {
	handed, ok := TakeEnvironmentKey()
	if ok {
		return &Store{handed: []byte(handed)}, nil
	}
	dir, err := localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirConfig)
	if err != nil {
		return nil, fmt.Errorf("the CLI's config directory, where the licence key lives: %w", err)
	}
	// The join is internal/localcfg's own: its configDir composes exactly this
	// path, so the key file sits beside the CLI's config and not in a directory
	// of this package's own choosing.
	return &Store{path: filepath.Join(dir, "fugaro", keyFileName)}, nil
}

// TakeEnvironmentKey reads the handed-over key and takes it out of this
// process's own environment in the same step, so no child and no grandchild of
// this process inherits a key it has no business holding.
//
// An empty variable is not a key: a console with no key hands none over, and
// the CLI falls back to its own file rather than reading an empty key as one.
func TakeEnvironmentKey() (string, bool) {
	raw, ok := os.LookupEnv(EnvironmentKey)
	if !ok {
		return "", false
	}
	os.Unsetenv(EnvironmentKey)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	return raw, true
}

// Keys answers the keys that apply: the one the environment handed over, or the
// file's lines, oldest first as they are written. A store with neither answers
// none and no error -- the no-key state is the store being empty rather than a
// fourth state (F43).
func (s *Store) Keys() ([][]byte, error) {
	if s.handed != nil {
		return [][]byte{append([]byte(nil), s.handed...)}, nil
	}
	return readKeyFile(s.path)
}

// Active answers the key that applies now and the state it is in: among the
// installed keys that verify, the one with the later expiry (T26). A renewal
// that lands early changes nothing for the user, and the key it follows keeps
// working until its own grace runs out.
//
// A key that does not verify is skipped rather than fatal -- a store holding a
// good key and a corrupt one still works -- and a store whose every key is
// refused answers the first refusal, because that is the one a person has to
// act on.
func (s *Store) Active(now time.Time) (Claims, State, error) {
	keys, err := s.Keys()
	if err != nil {
		return Claims{}, "", err
	}
	return active(productionKeyring, keys, now)
}

// active is the choice among installed keys against a keyring of the caller's.
//
// Ordering by expiry alone is the whole rule and not an approximation of one: a
// key past its grace and a key inside its term cannot be ordered the other way,
// because the later expiry of the two is the one that is still inside its
// term whenever one of them is.
func active(ring Keyring, keys [][]byte, now time.Time) (Claims, State, error) {
	if len(keys) == 0 {
		return Claims{}, "", ErrNoKey
	}
	var (
		applied Claims
		state   State
		found   bool
		refusal error
	)
	for _, raw := range keys {
		claims, st, err := verify(raw, now, ring)
		if err != nil {
			if refusal == nil {
				refusal = err
			}
			continue
		}
		if !found || claims.Expires > applied.Expires {
			applied, state, found = claims, st, true
		}
	}
	if !found {
		return Claims{}, "", refusal
	}
	return applied, state, nil
}

// readKeyFile answers the keys one file holds: one per line, blank lines
// skipped, and at most two.
func readKeyFile(path string) ([][]byte, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyFile, err)
	}
	if mode := info.Mode().Perm(); mode&^fs.FileMode(keyFileMode) != 0 {
		return nil, fmt.Errorf("%w: it is mode %04o and not the 0600 the design keeps it at, so any other local user can read the organisation's key", ErrKeyFile, mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyFile, err)
	}
	var keys [][]byte
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		keys = append(keys, []byte(line))
	}
	if len(keys) > maxInstalled {
		return nil, fmt.Errorf("%w: it holds %d keys, and the renewal's overlap installs two at the most (T26)", ErrKeyFile, len(keys))
	}
	return keys, nil
}
