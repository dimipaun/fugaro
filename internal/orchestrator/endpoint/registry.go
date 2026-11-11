// The registry file: where a running daemon tells a local reader which
// endpoint it serves, and what proves the process behind it is the one that
// wrote it (CG13b of docs/cli-output-console-graph/implementation-plan-graph.md).
//
// One file per orchestrator, `<UserDir state>/fugaro/run/<id>.json`, beside the
// `<id>.lock` the daemon holds for its life. The state directory is the one
// localcfg.UserDir places and not a runtime directory of the endpoint's own:
// macOS has none, and the daemon's other local file -- the session-start cache
// -- is placed by the same rule (F5, F23).
//
// What the file is not is a record of a process. A pid is not an identity:
// after a crash and a reboot the number may belong to another process, and a
// SIGKILLed daemon never removes its file, so a reader that trusted it would
// hand the orchestrator's token to a stranger. Liveness is therefore two
// things, and neither of them is the pid: the **handshake** (connect to the
// literal address, present the token from the file, read back the id and the
// nonce the file carries) and the **flock** the daemon holds on `<id>.lock`,
// which the kernel drops when the process dies however it dies (D1359).
//
// The two decide different questions. The handshake is what makes an endpoint
// live -- an address that answers with the id and the nonce the file carries is
// the process that wrote it. The lock is what makes a file *removable*: a file
// whose lock somebody still holds is left alone even when nothing answers
// there, because a daemon between its lock and its listener must not have its
// registry deleted by the first reader to look. A file that fails both is a
// dead daemon's, and the reader that read it is the one that removes it:
// nothing sweeps this directory, and no process removes another's file.
//
// Nothing here fails a daemon. A registry that cannot be written is a line in
// the daemon's output and the daemon drives its id exactly as before (F28): the
// endpoint is what a reader on this machine prefers and never a condition of
// the work, and `fugaro update` neither installs nor migrates any of it -- the
// daemon creates its own file, and a machine that never runs one never has it.
package endpoint

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// runDirName is the directory under the user's state directory that holds one
// file per orchestrator. It is a subdirectory of fugaro's own state and not a
// runtime directory: macOS has no runtime directory, and a directory a reboot
// clears would take the file with it while the daemon that wrote it is still
// running on the other side of the reboot's own decision to keep it.
const runDirName = "run"

// registryMode and runDirMode are what D1359 fixes the two at. The file's mode
// is what keeps the token a secret of this user's -- a loopback port is
// reachable by every process of every user on the machine, so the file that
// carries the credential is the only thing bounding who can read the state --
// and the directory's mode is what keeps the file's name and its existence from
// every other user as well.
const (
	registryMode = 0o600
	runDirMode   = 0o700
)

// handshakeTimeout bounds a reader's handshake. An endpoint on loopback answers
// at once or not at all, and a file whose address nothing is listening on must
// not hold a reader for the life of a TCP connect timeout.
const handshakeTimeout = 2 * time.Second

// errLockHeld is what a platform's non-blocking lock reports when the lock is
// somebody else's. It is the one answer the reader treats as "a live process
// holds this file", and the one the daemon's own start-up refuses over.
var errLockHeld = errors.New("the endpoint's lock is held by another process")

// Registry is the file's shape on the wire. It is decoded strictly -- an
// unknown field is an error -- and validated at both ends, so a document this
// release did not write is refused rather than half-read.
//
// PID is written for a person reading the file and for nothing else: no
// decision in this package is made from it (see the package comment). Addr is
// the literal `127.0.0.1:<port>` the listener bound, never a name: a name would
// resolve to whatever that name has come to mean on this machine, which is the
// browser-rebinding case the endpoint's own Host check exists for.
type Registry struct {
	ID      string    `json:"id"`
	PID     int       `json:"pid"`
	Addr    string    `json:"addr"`
	Token   string    `json:"token"`
	Nonce   string    `json:"nonce"`
	Started time.Time `json:"started_at"`
}

// Holder is one daemon's registry: the lock it holds for its life and the file
// it wrote. It is a value rather than two files the caller keeps because the
// lock's whole meaning is "until this is released", and a caller that dropped
// one of the two would leave a file the next reader has to judge stale.
type Holder struct {
	path string
	lock *os.File
}

// Hold takes id's lock and writes r's registry file, once, at the daemon's
// start-up.
//
// The lock is taken before the file is written and held until Remove: a reader
// that finds the file can then never find the lock free while its writer is
// alive, which is what makes the reader's removal safe. A lock somebody else
// holds is a refusal -- another daemon is serving this id on this machine, and
// two files for one id is what a reader could not choose between.
func Hold(getenv func(string) string, goos string, r Registry) (*Holder, error) {
	if err := r.validate(r.ID); err != nil {
		return nil, fmt.Errorf("the endpoint's registry: %w", err)
	}
	path, err := registryPath(getenv, goos, r.ID)
	if err != nil {
		return nil, err
	}
	if err := makeRunDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	lockPath, err := lockPath(getenv, goos, r.ID)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|openNoFollow, registryMode)
	if err != nil {
		return nil, fmt.Errorf("opening the endpoint's lock file at %s: %w", lockPath, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errLockHeld) {
			return nil, fmt.Errorf("orchestrator %s already serves a local endpoint on this machine", safetext.OneLine(r.ID))
		}
		return nil, fmt.Errorf("taking the endpoint's lock at %s: %w", lockPath, err)
	}
	if err := write(path, filepath.Dir(path), r); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Holder{path: path, lock: f}, nil
}

// Remove is the daemon's clean exit: its own registry file goes, and the lock
// it held is released with it -- by the close, which is also what a SIGKILL
// leaves to the kernel.
//
// It removes nothing else. Never another orchestrator's file, which is a
// daemon's own to take away, and never the lock file, whose inode is what the
// next daemon takes a lock on: a lock file unlinked while a holder is alive is
// two daemons holding two locks on one name.
func (h *Holder) Remove() error {
	err := os.Remove(h.path)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if cerr := h.lock.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("removing the endpoint's registry at %s: %w", h.path, err)
	}
	return nil
}

// Probe reads id's registry file and decides whether a live endpoint is behind
// it, removing the file when nothing is.
//
// It is the reader's whole half of D1359's liveness rule, and the only place a
// registry file is ever removed by a reader. The handshake decides liveness;
// the lock decides whether this reader may take the file away, and it is held
// across the removal so that a daemon starting in the same instant either
// writes after this and is found, or cannot take the lock and serves nothing
// this run. A file that fails both is removed; a file whose lock is still held
// is left where it is and reported as not live, which is the state a reader
// falls back to the node and the cache on (T5).
//
// A file that is not there is "nothing here" rather than a failure: a machine
// that has never run a daemon has no run directory at all. A file that is there
// and is not what this release writes -- another mode, a symlink, a document
// that does not decode -- is an error and is left alone: this reader cannot
// judge it, and the next daemon's write replaces it anyway.
func Probe(ctx context.Context, getenv func(string) string, goos, id string, hc *http.Client) (Registry, bool, error) {
	path, err := registryPath(getenv, goos, id)
	if err != nil {
		return Registry{}, false, err
	}
	reg, err := read(path, id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Registry{}, false, nil
	case err != nil:
		return Registry{}, false, err
	}
	lock, free, err := lockFree(getenv, goos, id)
	if err != nil {
		return Registry{}, false, err
	}
	if lock != nil {
		defer func() { _ = lock.Close() }()
	}
	if handshake(ctx, hc, reg) {
		return reg, true, nil
	}
	if !free {
		return reg, false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return reg, false, fmt.Errorf("removing the endpoint's dead registry at %s: %w", path, err)
	}
	return reg, false, nil
}

// validate refuses a document this release did not write, whether it was just
// built here or decoded from a file.
func (r Registry) validate(id string) error {
	if r.ID != id {
		return fmt.Errorf("it names %q and %q was asked for", safetext.OneLine(r.ID), safetext.OneLine(id))
	}
	if _, err := orchestrator.PathNode(r.ID); err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(r.Addr)
	if err != nil || host != "127.0.0.1" {
		return fmt.Errorf("its address %q is not the loopback literal this endpoint binds", safetext.OneLine(r.Addr))
	}
	if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
		return fmt.Errorf("its address %q carries no port", safetext.OneLine(r.Addr))
	}
	for _, v := range []struct {
		what  string
		value string
	}{{"token", r.Token}, {"nonce", r.Nonce}} {
		if len(v.value) != 2*tokenBytes {
			return fmt.Errorf("its %s is not the %d bytes this endpoint mints", v.what, tokenBytes)
		}
		if _, err := hex.DecodeString(v.value); err != nil {
			return fmt.Errorf("its %s is not hex", v.what)
		}
	}
	if r.Started.IsZero() {
		return errors.New("it carries no start time")
	}
	return nil
}

// read is Probe's file half: everything between the path and a document this
// release would have written. The file is looked at before the directory it
// sits in, so a machine with no run directory at all reports a missing file
// rather than a missing directory.
func read(path, id string) (Registry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Registry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Registry{}, fmt.Errorf("the endpoint's registry at %s is not a regular file", path)
	}
	if got := info.Mode().Perm(); got != registryMode {
		return Registry{}, fmt.Errorf("the endpoint's registry at %s is mode %04o, and one this release did not write at %04o is refused rather than read", path, got, registryMode)
	}
	if err := checkRunDir(filepath.Dir(path)); err != nil {
		return Registry{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, fmt.Errorf("reading the endpoint's registry at %s: %w", path, err)
	}
	var reg Registry
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&reg); err != nil {
		return Registry{}, fmt.Errorf("the endpoint's registry at %s is not a document this release reads: %w", path, err)
	}
	if err := reg.validate(id); err != nil {
		return Registry{}, fmt.Errorf("the endpoint's registry at %s: %w", path, err)
	}
	return reg, nil
}

// write places the document atomically: a temp file created exclusively beside
// the registry and renamed over it, so a reader sees the previous file or this
// one and never half of either (F6a's rule, the cache's own).
//
// The temp file is created O_CREATE|O_EXCL|O_NOFOLLOW at 0600, and the rename
// replaces whatever is at the path rather than following it -- a stale file
// from a daemon that was killed, a symlink somebody planted -- so what the
// reader opens next is a file this release wrote at the mode it wrote it.
func write(path, dir string, r Registry) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding the endpoint's registry: %w", err)
	}
	seed, err := mint()
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+r.ID+"-"+seed[:16]+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|openNoFollow, registryMode)
	if err != nil {
		return fmt.Errorf("writing the endpoint's registry beside %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(registryMode); err != nil {
		_ = f.Close()
		return fmt.Errorf("setting the mode of the endpoint's registry: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing the endpoint's registry: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing the endpoint's registry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("placing the endpoint's registry at %s: %w", path, err)
	}
	return nil
}

// makeRunDir creates the directory the registry files live in, at mode 0700,
// and refuses one this release did not make: a symlink, something that is not
// a directory, a directory another user owns.
//
// The directory is half of what makes the token in the file a secret of this
// user's, so a directory that is not this release's own is refused rather than
// written into, and one whose mode has been widened is put back rather than
// written into at a mode this release did not choose.
func makeRunDir(dir string) error {
	if err := os.MkdirAll(dir, runDirMode); err != nil {
		return fmt.Errorf("creating the endpoint registry's directory at %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("reading the endpoint registry's directory at %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("the endpoint registry's directory at %s is not a directory this release made", dir)
	}
	if uid, ok := fileOwner(info); ok && uid != uint32(os.Geteuid()) {
		return fmt.Errorf("the endpoint registry's directory at %s is owned by uid %d, not by this user", dir, uid)
	}
	if info.Mode().Perm() != runDirMode {
		if err := os.Chmod(dir, runDirMode); err != nil {
			return fmt.Errorf("setting the mode of the endpoint registry's directory at %s: %w", dir, err)
		}
	}
	return nil
}

// checkRunDir is the reader's half of makeRunDir: a directory this release did
// not make is one whose file this reader does not trust, because the mode of
// the file is only a secret while the directory around it is this user's.
func checkRunDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("the endpoint registry's directory at %s is not a directory this release made", dir)
	}
	if got := info.Mode().Perm(); got != runDirMode {
		return fmt.Errorf("the endpoint registry's directory at %s is mode %04o, and one this release did not make at %04o is refused rather than read", dir, got, runDirMode)
	}
	if uid, ok := fileOwner(info); ok && uid != uint32(os.Geteuid()) {
		return fmt.Errorf("the endpoint registry's directory at %s is owned by uid %d, not by this user", dir, uid)
	}
	return nil
}

// lockFree opens id's lock file and takes it if nobody holds it, reporting
// whether it got it and the open file, which holds the lock until it is closed.
//
// A lock file that is not there is free: the daemon takes the lock before it
// writes the registry, so a registry file whose lock file is missing is one
// whose writer never got that far or is gone. The file is not created here --
// a reader plants nothing -- and nothing here waits.
func lockFree(getenv func(string) string, goos, id string) (*os.File, bool, error) {
	path, err := lockPath(getenv, goos, id)
	if err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|openNoFollow, registryMode)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("opening the endpoint's lock file at %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		if errors.Is(err, errLockHeld) {
			return f, false, nil
		}
		_ = f.Close()
		return nil, false, fmt.Errorf("taking the endpoint's lock at %s: %w", path, err)
	}
	return f, true, nil
}

// handshake proves that the process answering at the file's address is the one
// that wrote the file: it connects to the literal address, presents the token
// from the file, and reads back the id and the nonce the file carries.
//
// The pid is deliberately not part of this, and neither is the port on its own.
// A process that took the pid over after a reboot, and an unrelated server that
// took the port, both answer nothing that compares equal to the nonce a file
// carries -- and the nonce is minted with the endpoint and never reused, which
// is what makes it an identity rather than a number (D1359).
func handshake(ctx context.Context, hc *http.Client, r Registry) bool {
	if hc == nil {
		hc = &http.Client{Timeout: handshakeTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+r.Addr+routeState, nil)
	if err != nil {
		return false
	}
	req.Header.Set(authorizationHeader, bearerScheme+" "+r.Token)
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	// Drained, and bounded: the answer is a state document, and a connection
	// left unread is one the daemon's handler is still writing into.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	h := resp.Header
	// The compare is the token's own (token.go): a reader that compared these
	// two byte by byte would let the process that holds the port narrow the
	// nonce one byte at a time and read the answer off its own clock.
	return sameToken(h.Get(headerOrchestrator), r.ID) && sameToken(h.Get(headerNonce), r.Nonce)
}

// runDir is the directory the registry files live in:
// `<UserDir state>/fugaro/run`, placed the way orchestrator.CacheDir places the
// cached brief. goos is a parameter for the reason localcfg.UserDir takes one.
func runDir(getenv func(string) string, goos string) (string, error) {
	dir, err := localcfg.UserDir(getenv, goos, localcfg.DirState)
	if err != nil {
		return "", fmt.Errorf("the endpoint's registry cannot be placed: %w", err)
	}
	return filepath.Join(dir, "fugaro", runDirName), nil
}

// registryPath is the file of one orchestrator, and lockPath the lock beside
// it.
func registryPath(getenv func(string) string, goos, id string) (string, error) {
	dir, err := runDir(getenv, goos)
	if err != nil {
		return "", err
	}
	if _, err := orchestrator.PathNode(id); err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

func lockPath(getenv func(string) string, goos, id string) (string, error) {
	dir, err := runDir(getenv, goos)
	if err != nil {
		return "", err
	}
	if _, err := orchestrator.PathNode(id); err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".lock"), nil
}
