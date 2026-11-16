// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// This file is the lock one project's front-door credential is swapped under.
//
// **The refresh token is spent by the exchange that reads it (D930), so the
// read, the swap and the write-back are one step.** Two commands of one project
// started at once — a script that queues two roadmap writes, an editor that
// saves twice — would otherwise both present the token the keychain holds, and
// the door reads the second presentation as the reuse D930 answers by revoking
// the family: the person is signed out, has to sign in again in a browser, and
// a false theft signal is recorded against them. The lock is what makes the
// second command read the token the first one stored instead of the one it
// spent.
//
// **It is the kernel's lock and not a record in the file.** flock (and
// LockFileEx) is held on the open file description, so it is dropped when the
// process ends however it ends — a crash, a SIGKILL, a closed terminal — and
// nothing here leaves a record a later command has to decide is stale. The file
// holds nothing at all, and it is not removed on release: unlinking a lock file
// is the one way to hand the same lock to two commands at once, since the
// command that was waiting would hold an inode nothing else can reach.
//
// **It is taken for the swap and nothing else.** The front-door URL is read
// through Terraform and the tool call is a call to the door; neither touches the
// credential, so neither is inside the lock. What is inside is one token
// exchange, bounded by the client's own timeout — which is what bounds the wait
// of the command that finds the lock held.

// coordinatorCredentialLockSuffix names the lock file of a project, beside its
// own config: <config home>/fugaro/projects/<name>.coordinator.lock. The
// projects directory is where this machine's per-project files already live
// (<name>.pending.json, <name>.layer.yaml), and the credential is keyed by the
// project's name — the keychain item is named after it — so the lock is keyed
// by that name in the place its other files are.
const coordinatorCredentialLockSuffix = ".coordinator.lock"

// coordinatorBeforeCredentialLock, when set by a test, runs after the
// credential has been read and before the lock is taken. It is the seam a test
// needs to put two commands at the one point the lock exists for: whether the
// second command reads the credential before the first has stored its successor
// is otherwise a matter of timing, and a test that reaches the state it is
// about only sometimes proves nothing. internal/lock's beforeTakeoverDelete is
// the same seam for the same reason.
var coordinatorBeforeCredentialLock func()

// coordinatorCredentialLock is one project's credential lock, held.
type coordinatorCredentialLock struct{ f *os.File }

// coordinatorCredentialLockPath is the file two commands of one project exclude
// each other on.
func coordinatorCredentialLockPath(project string) (string, error) {
	dir, err := localcfg.ProjectsDir(os.Getenv)
	if err != nil {
		return "", fcli.UserErr("%v", err)
	}
	return filepath.Join(dir, project+coordinatorCredentialLockSuffix), nil
}

// coordinatorLockCredential takes project's credential lock, waiting for
// whoever holds it. It never refuses because another command is running: two
// writes are a queue to join and not a second run to refuse, because the whole
// point is that the one behind reads what the one ahead stored.
//
// A lock that cannot be taken at all is the machine's (exit 2) rather than the
// person's, and the command does not carry on without it: the reuse this lock
// exists to prevent is what a command that skipped it would risk, and being
// told that a file could not be opened is a better answer than being signed out
// with no way to tell why.
func coordinatorLockCredential(project string) (*coordinatorCredentialLock, error) {
	if coordinatorBeforeCredentialLock != nil {
		coordinatorBeforeCredentialLock()
	}
	path, err := coordinatorCredentialLockPath(project)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("the directory of this project's credential lock (%s): %w", filepath.Dir(path), err))
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("this project's credential lock (%s): %w", path, err))
	}
	if err := lockCredentialFile(f); err != nil {
		_ = f.Close()
		return nil, fcli.RemoteErr(fmt.Errorf("this project's credential lock (%s) could not be taken: %w", path, err))
	}
	return &coordinatorCredentialLock{f: f}, nil
}

// release gives the lock up, and is safe to call twice and on a lock that was
// never taken (nil), so a caller can defer it beside the call that takes it.
func (l *coordinatorCredentialLock) release() {
	if l == nil || l.f == nil {
		return
	}
	f := l.f
	l.f = nil
	_ = unlockCredentialFile(f)
	_ = f.Close()
}
