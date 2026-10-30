//go:build darwin && keychain_live

// The live half of the darwin storing side: it writes a key into the real
// keychain of the machine it runs on, reads it back with the real reader,
// checks that no argv carried the key, and removes the item again.
//
// **No agent runs this, and no agent may.** M5's rule is that the key lives in
// the operator's keychain and that nothing in this repository reads one; the
// item this test writes is a throwaway of its own, but the program it drives is
// the real `security` against the real login keychain, and the only person who
// can say what happened to it is the person sitting at that Mac. The owner runs
// it in his own terminal:
//
//	go test -tags keychain_live ./internal/mailbox/keychain/ -run Live -count=1 -v
//
// The tags are what keep it out of every other build: without `keychain_live`
// the file is not compiled at all, so a plain `go test ./...` on a Mac never
// touches a keychain. The hermetic half — the invocation, the quoting, the
// failure reading, and the argv and the environment of a real child process —
// is keychain_test.go, which runs on every platform.
//
// Two things only this run can settle, and both are why the read-back below is
// an assertion rather than a formality: that a successful `add-generic-password`
// in interactive mode really is silent (the failure reading treats any line the
// tool prints as a failure, so a prompt would refuse every store), and that the
// value the storing command writes is the value the reader's own
// `find-generic-password -w` hands back.
package keychain

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveDarwinKeychainRoundTrip is the one test here: store, read back,
// delete, and look at the argv the tool was run with.
func TestLiveDarwinKeychainRoundTrip(t *testing.T) {
	ctx := context.Background()
	item := liveItem(t)
	key := liveKey(t)

	// A `security` that writes down its own argv and then runs the real tool:
	// the only way to see, from here, what every process on the machine could
	// read out of `ps` while the key was being stored.
	argvLog := liveSecurityWrapper(t)
	store := System()
	if _, err := readerFor("darwin"); err != nil {
		t.Fatalf("darwin has no reader: %v", err)
	}

	t.Cleanup(func() { liveForget(item) })

	if err := store.Put(ctx, item, key); err != nil {
		t.Fatalf("storing a %d-character key at %q: %v", key.Len(), item.Name(), err)
	}
	got, err := store.Key(ctx, item)
	if err != nil {
		t.Fatalf("reading the key back from %q: %v", item.Name(), err)
	}
	// Neither value is printed, even on failure: M5's rule holds in a
	// diagnostic too, and this test's own key is a key like any other.
	if string(got.Bytes()) != string(key.Bytes()) {
		t.Errorf("the key read back is not the %d-character key that was stored", key.Len())
	}

	logged := liveRead(t, argvLog)
	if strings.Contains(logged, string(key.Bytes())) {
		t.Errorf("an argv carried the key: %s", strings.ReplaceAll(logged, string(key.Bytes()), Redacted))
	}
	if !strings.Contains(logged, "-i") {
		t.Errorf("the tool was not run in interactive mode, so the key went somewhere else: %s", logged)
	}

	if err := store.Delete(ctx, item); err != nil {
		t.Fatalf("removing %q: %v", item.Name(), err)
	}
	if _, err := store.Key(ctx, item); !errors.Is(err, ErrNoKey) {
		t.Errorf("reading the item after removing it = %v, want ErrNoKey", err)
	}
	// An item that is not there is the state the caller asked for, and the
	// second delete is what `pair revoke` does to a pairing it already undid.
	if err := store.Delete(ctx, item); err != nil {
		t.Errorf("removing an item that is already gone = %v, want nil", err)
	}
}

// liveItem is a throwaway item name: one of this test's own, carrying no
// project's name, so nothing here can reach a real pairing's key.
func liveItem(t *testing.T) Item {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("drawing an item name: %v", err)
	}
	item, err := ParseItem("fugaro-test-"+hex.EncodeToString(suffix[:]), "")
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	return item
}

// liveKey is a random key in the one spelling a pairing key has (D171:
// `token.EncodeKey`'s unpadded base64url), so what is stored here is the shape
// the real storing side is given in production.
func liveKey(t *testing.T) Key {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("drawing a key: %v", err)
	}
	return NewKey([]byte(base64.RawURLEncoding.EncodeToString(raw)))
}

// liveSecurityWrapper puts a `security` first on PATH that records its argv and
// then runs the real tool, and answers the file it records into.
func liveSecurityWrapper(t *testing.T) string {
	t.Helper()
	const realSecurity = "/usr/bin/security"
	if _, err := os.Stat(realSecurity); err != nil {
		t.Fatalf("the real security tool is not at %s: %v", realSecurity, err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := fmt.Sprintf("#!/bin/sh\n{ printf 'argv:'; for arg in \"$@\"; do printf ' [%%s]' \"$arg\"; done; printf '\\n'; } >> %q\nexec %s \"$@\"\n", log, realSecurity)
	if err := os.WriteFile(filepath.Join(dir, "security"), []byte(script), 0o700); err != nil {
		t.Fatalf("writing the recording wrapper: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// liveRead is what the wrapper recorded, or a note that it recorded nothing —
// which is itself a failure of this test rather than of the code: the tool was
// reached through the wrapper or the store did not run it at all.
func liveRead(t *testing.T, log string) string {
	t.Helper()
	recorded, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the recording wrapper wrote nothing, so the store did not reach it: %v", err)
	}
	return string(recorded)
}

// liveForget removes the item through the real tool, whatever the test did with
// it, so a failed run leaves nothing behind in the operator's keychain.
func liveForget(item Item) {
	_ = exec.Command("/usr/bin/security", "delete-generic-password", "-s", item.Name()).Run()
}
