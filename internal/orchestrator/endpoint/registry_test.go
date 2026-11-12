package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The registry file and its liveness: D1359's three rules of this slice
// (CG13b).

// registryEnv is one installation's state directory and the getenv that places
// the registry in it. It is a temp directory, so no write here can reach a
// developer's own state directory, and the directory the files land in is the
// one the plan names: `<UserDir state>/fugaro/run`.
func registryEnv(t *testing.T) (func(string) string, string) {
	t.Helper()
	dir := t.TempDir()
	getenv := func(name string) string {
		if name == "XDG_STATE_HOME" {
			return dir
		}
		return ""
	}
	return getenv, filepath.Join(dir, "fugaro", "run")
}

// registryFor is the content of one registry file, with a token and a nonce
// minted the way the endpoint mints them.
func registryFor(t *testing.T, id, addr string) Registry {
	t.Helper()
	token, err := mint()
	if err != nil {
		t.Fatalf("minting a token: %v", err)
	}
	nonce, err := mint()
	if err != nil {
		t.Fatalf("minting a nonce: %v", err)
	}
	return Registry{ID: id, PID: os.Getpid(), Addr: addr, Token: token, Nonce: nonce, Started: time.Now().UTC()}
}

// writeRegistryFile plants a registry document at mode, the way something
// other than Hold would: the reader's cases need documents this release's own
// writer never produces.
func writeRegistryFile(t *testing.T, dir string, reg Registry, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the run directory: %v", err)
	}
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("encoding the registry: %v", err)
	}
	path := filepath.Join(dir, reg.ID+".json")
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("writing the registry file: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("setting the mode of the registry file: %v", err)
	}
	return path
}

// readRegistryFile decodes what is at path, with the shape the writer wrote.
func readRegistryFile(t *testing.T, path string) Registry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the registry file at %s: %v", path, err)
	}
	var reg Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("decoding the registry file at %s: %v", path, err)
	}
	return reg
}

// freeAddr is an address on loopback nothing is listening on: the kernel hands
// one out and the listener is closed again.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("closing the loopback listener: %v", err)
	}
	return addr
}

// servedEndpoint is a real endpoint under test, with the registry file a daemon
// beside it would have written.
func servedEndpoint(t *testing.T, ctx context.Context, id string) (*Server, Registry) {
	t.Helper()
	srv, err := Start(ctx, Options{ID: id, Snapshot: func() (orchestrator.State, time.Time, bool) {
		return testState("wi-1"), tickedAt, true
	}})
	if err != nil {
		t.Fatalf("starting the endpoint: %v", err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Close(sctx); err != nil {
			t.Errorf("closing the endpoint: %v", err)
		}
	})
	return srv, Registry{
		ID: id, PID: os.Getpid(), Addr: srv.Addr(),
		Token: srv.Token(), Nonce: srv.Nonce(), Started: time.Now().UTC(),
	}
}

// TestRegistryFileModeAndNonce: the registry is 0600 in a directory that is
// 0700, and it carries a start-time nonce minted beside the token and never
// reused (D1359). The mode is what keeps the token a secret of this user's, and
// the nonce is what makes the file's identity a value rather than a number: a
// pid is reused, and a nonce is not.
func TestRegistryFileModeAndNonce(t *testing.T) {
	getenv, dir := registryEnv(t)
	reg := registryFor(t, endpointID, "127.0.0.1:41234")

	holder, err := Hold(getenv, "linux", reg)
	if err != nil {
		t.Fatalf("writing the registry: %v", err)
	}
	path := filepath.Join(dir, endpointID+".json")

	di, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("reading the run directory: %v", err)
	}
	if di.Mode()&os.ModeSymlink != 0 || !di.IsDir() {
		t.Errorf("the run directory at %s is not a directory", dir)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("the run directory is mode %04o, want 0700", got)
	}

	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("reading the registry file: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		t.Errorf("the registry file at %s is not a regular file", path)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("the registry file is mode %04o, want 0600", got)
	}

	got := readRegistryFile(t, path)
	if got.ID != endpointID {
		t.Errorf("the registry names %q, want %q", got.ID, endpointID)
	}
	if got.PID != os.Getpid() {
		t.Errorf("the registry carries pid %d, want %d", got.PID, os.Getpid())
	}
	if got.Addr != reg.Addr {
		t.Errorf("the registry carries the address %q, want %q", got.Addr, reg.Addr)
	}
	if got.Token != reg.Token {
		t.Errorf("the registry does not carry the token it was written with")
	}
	if got.Nonce == "" {
		t.Fatal("the registry carries no nonce")
	}
	if len(got.Nonce) != 2*tokenBytes {
		t.Errorf("the nonce is %d characters, want %d", len(got.Nonce), 2*tokenBytes)
	}
	if got.Nonce == got.Token {
		t.Error("the nonce is the token, and the two are minted as two values")
	}
	if !got.Started.Equal(reg.Started) {
		t.Errorf("the registry carries the start time %s, want %s", got.Started, reg.Started)
	}

	if err := holder.Remove(); err != nil {
		t.Fatalf("removing the registry: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the registry file is still at %s after Remove", path)
	}
	// The lock file stays: its inode is what the next daemon takes a lock on,
	// and removing it is how two daemons come to hold two locks on one name.
	if _, err := os.Stat(filepath.Join(dir, endpointID+".lock")); err != nil {
		t.Errorf("the lock file went with the registry: %v", err)
	}

	second, err := Hold(getenv, "linux", registryFor(t, endpointID, "127.0.0.1:41235"))
	if err != nil {
		t.Fatalf("writing a second registry: %v", err)
	}
	t.Cleanup(func() { _ = second.Remove() })
	again := readRegistryFile(t, path)
	if again.Nonce == got.Nonce {
		t.Errorf("a second start reused the nonce %q", again.Nonce)
	}
	if again.Token == got.Token {
		t.Errorf("a second start reused the token")
	}
}

// TestRegistryFileModeAndNonceRefusesWhatThisReleaseDidNotWrite: the reader
// refuses a file that is not 0600 in a directory that is not 0700 rather than
// reading it, and refuses a document that is not the shape this release wrote
// (CG13c's rule, held here where the read lives).
func TestRegistryFileModeAndNonceRefusesWhatThisReleaseDidNotWrite(t *testing.T) {
	ctx := context.Background()
	getenv, dir := registryEnv(t)
	reg := registryFor(t, endpointID, freeAddr(t))

	writeRegistryFile(t, dir, reg, 0o644)
	if _, _, err := Probe(ctx, getenv, "linux", endpointID, nil); err == nil {
		t.Error("a registry file at mode 0644 was read")
	}
	writeRegistryFile(t, dir, reg, 0o600)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("widening the run directory: %v", err)
	}
	if _, _, err := Probe(ctx, getenv, "linux", endpointID, nil); err == nil {
		t.Error("a registry file in a directory at mode 0755 was read")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("restoring the run directory: %v", err)
	}

	noNonce := reg
	noNonce.Nonce = ""
	writeRegistryFile(t, dir, noNonce, 0o600)
	if _, _, err := Probe(ctx, getenv, "linux", endpointID, nil); err == nil {
		t.Error("a registry file with no nonce was read")
	}

	named := reg
	named.Addr = "localhost:41234"
	writeRegistryFile(t, dir, named, 0o600)
	if _, _, err := Probe(ctx, getenv, "linux", endpointID, nil); err == nil {
		t.Error("a registry file naming a host that is not the loopback literal was read")
	}

	other := reg
	other.ID = "gadgets"
	writeRegistryFile(t, dir, other, 0o600)
	if _, _, err := Probe(ctx, getenv, "linux", endpointID, nil); err == nil {
		t.Error("a registry file naming another orchestrator was read for this one")
	}
}

// TestLivenessIsHandshakePlusLockNotPID: a live endpoint is proven by the
// handshake and by the lock the daemon holds, and never by the pid in the file
// (D1359). The pid names this very process in every case below, so a reader
// that decided from it would call all three of them live.
func TestLivenessIsHandshakePlusLockNotPID(t *testing.T) {
	ctx := context.Background()
	getenv, dir := registryEnv(t)
	path := filepath.Join(dir, endpointID+".json")

	// A live endpoint, and the file a daemon beside it would have written.
	_, reg := servedEndpoint(t, ctx, endpointID)
	holder, err := Hold(getenv, "linux", reg)
	if err != nil {
		t.Fatalf("writing the registry: %v", err)
	}
	got, live, err := Probe(ctx, getenv, "linux", endpointID, nil)
	if err != nil {
		t.Fatalf("probing a live endpoint: %v", err)
	}
	if !live {
		t.Error("a registry file a live endpoint answers for read as dead")
	}
	if got.Nonce != reg.Nonce || got.Addr != reg.Addr {
		t.Errorf("the handshake answered for a file other than the one read")
	}
	if err := holder.Remove(); err != nil {
		t.Fatalf("removing the registry: %v", err)
	}

	// A live process that is not the daemon: the pid is this test's and nothing
	// answers at the address, so both signals fail and the file is a dead one.
	writeRegistryFile(t, dir, registryFor(t, endpointID, freeAddr(t)), 0o600)
	if _, live, err := Probe(ctx, getenv, "linux", endpointID, nil); err != nil {
		t.Fatalf("probing a dead endpoint: %v", err)
	} else if live {
		t.Error("a registry file nothing answers at read as live because its pid is a running process")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the reader that found the file dead did not remove it: %v", err)
	}

	// The lock is the other half: a file whose lock a live process holds is
	// never removed, however the handshake goes. The daemon may be between its
	// lock and its listener, and a reader that deleted the file would delete
	// the registry of a daemon that is coming.
	held, err := Hold(getenv, "linux", registryFor(t, endpointID, freeAddr(t)))
	if err != nil {
		t.Fatalf("writing the registry: %v", err)
	}
	t.Cleanup(func() { _ = held.Remove() })
	if _, live, err := Probe(ctx, getenv, "linux", endpointID, nil); err != nil {
		t.Fatalf("probing a locked endpoint: %v", err)
	} else if live {
		t.Error("a registry file nothing answers at read as live because its lock is held")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file of a lock still held was removed: %v", err)
	}
}

// TestStaleFileRemovedOnlyByTheReaderThatFoundItDead: nothing sweeps the run
// directory. The reader that read a file and found it dead removes that file
// and no other, the daemon removes its own on the way out and never another
// process's, and a file a live endpoint answers for is left where it is
// (D1359).
func TestStaleFileRemovedOnlyByTheReaderThatFoundItDead(t *testing.T) {
	ctx := context.Background()
	getenv, dir := registryEnv(t)
	file := func(id string) string { return filepath.Join(dir, id+".json") }

	writeRegistryFile(t, dir, registryFor(t, "widgets", freeAddr(t)), 0o600)
	writeRegistryFile(t, dir, registryFor(t, "gadgets", freeAddr(t)), 0o600)
	_, live := servedEndpoint(t, ctx, "sprockets")
	liveHolder, err := Hold(getenv, "linux", live)
	if err != nil {
		t.Fatalf("writing the registry of the live endpoint: %v", err)
	}

	if _, live, err := Probe(ctx, getenv, "linux", "widgets", nil); err != nil {
		t.Fatalf("probing a dead endpoint: %v", err)
	} else if live {
		t.Error("a registry file nothing answers at read as live")
	}
	if _, err := os.Stat(file("widgets")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the reader did not remove the file it found dead: %v", err)
	}
	for _, id := range []string{"gadgets", "sprockets"} {
		if _, err := os.Stat(file(id)); err != nil {
			t.Errorf("the reader removed %s's file, and it removed only the one it read: %v", id, err)
		}
	}

	if _, live, err := Probe(ctx, getenv, "linux", "sprockets", nil); err != nil {
		t.Fatalf("probing the live endpoint: %v", err)
	} else if !live {
		t.Error("a registry file a live endpoint answers for read as dead")
	}
	if _, err := os.Stat(file("sprockets")); err != nil {
		t.Errorf("a file a live endpoint answers for was removed: %v", err)
	}

	if err := liveHolder.Remove(); err != nil {
		t.Fatalf("removing the registry of the live endpoint: %v", err)
	}
	if _, err := os.Stat(file("sprockets")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the daemon did not remove its own file on the way out: %v", err)
	}
	if _, err := os.Stat(file("gadgets")); err != nil {
		t.Errorf("the daemon removed another orchestrator's file: %v", err)
	}
}

// TestNoRegistryIsNotAnError: a machine that has never run a daemon has no run
// directory at all, and that is a reader's "nothing here" rather than a
// failure. It is the case the local scope falls back to the node and the cache
// on (T5).
func TestNoRegistryIsNotAnError(t *testing.T) {
	getenv, _ := registryEnv(t)
	reg, live, err := Probe(context.Background(), getenv, "linux", endpointID, nil)
	if err != nil {
		t.Fatalf("probing a machine with no registry: %v", err)
	}
	if live || reg != (Registry{}) {
		t.Errorf("a machine with no registry answered %+v, live %v", reg, live)
	}
}
