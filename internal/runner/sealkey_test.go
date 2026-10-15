package runner_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// sealKeyFor builds a token.KeySize-byte key the tests can name. It is test
// material, never a real key.
func sealKeyFor(seed byte) []byte {
	k := make([]byte, token.KeySize)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

// envOf is a lookup over a map, as BackendFromEnv takes one.
func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// TestSealKeyMarkerIsParsedStrictlyAndNeverEchoed: the marker's value is "1"
// from a launcher that sealed nothing and the launch's seal key once the
// launcher seals (D15). Both must parse; everything else is damage and stops
// the run, and no refusal may carry the value, because this parse runs before
// any redactor exists (internal/cli's runExec calls it at bootstrap, and the
// failure becomes the run's infra_error).
func TestSealKeyMarkerIsParsedStrictlyAndNeverEchoed(t *testing.T) {
	key := sealKeyFor(0x11)
	enc := token.EncodeKey(key)

	// The key: marked, and the key itself.
	be, err := runner.BackendFromEnv(envOf(map[string]string{runner.BudgetTokenEnv: enc}), false)
	if err != nil || !be.TokenMarked || !bytes.Equal(be.TokenKey, key) {
		t.Fatalf("a key marker = %+v, %v, want TokenMarked with the key", be, err)
	}
	// Today's one-bit marker keeps working, with no key: a launcher that seals
	// nothing writes "1", which is every launch until the seal's second step.
	be, err = runner.BackendFromEnv(envOf(map[string]string{runner.BudgetTokenEnv: "1"}), false)
	if err != nil || !be.TokenMarked || be.TokenKey != nil {
		t.Fatalf("the one-bit marker = %+v, %v, want TokenMarked with no key", be, err)
	}
	// Everything else is damage to the job's environment, and it fails closed
	// however the run's mode reads (MalformedEnv), as a bad marker always has.
	for _, bad := range []string{"", "yes", "true", enc + "=", enc + "\n", enc + " ", "\t" + enc,
		enc[:len(enc)-1], enc + "A", "+" + enc[1:], enc + "." + enc} {
		be, err := runner.BackendFromEnv(envOf(map[string]string{runner.BudgetTokenEnv: bad}), false)
		if err == nil {
			t.Errorf("the marker %q was accepted (%+v)", bad, be)
			continue
		}
		if !be.MalformedEnv {
			t.Errorf("the marker %q: MalformedEnv = false, so a budget-off run would ignore it", bad)
		}
		if bad != "" && strings.Contains(err.Error(), bad) {
			t.Errorf("the marker %q reaches the error text: %v", bad, err)
		}
		// A value that merely starts like a key is a secret too: the refusal
		// may not carry a prefix of it either.
		if strings.Contains(err.Error(), enc[:8]) {
			t.Errorf("the error carries part of the marker's value: %v", err)
		}
	}
	// A value that merely starts like a key is a secret too: a refusal may not
	// carry a prefix of it either. This is the case the "%q" the marker's error
	// used to carry got wrong, and the leak D19's ordering is about.
	short := enc[:len(enc)-1]
	if _, err := runner.BackendFromEnv(envOf(map[string]string{runner.BudgetTokenEnv: short}), false); err == nil {
		t.Error("a short key was accepted")
	} else if strings.Contains(err.Error(), enc[:8]) {
		t.Errorf("a short key's refusal carries part of the value: %v", err)
	}
}

// sealTokenObject replaces the plaintext object the launcher's fixture minted
// with a copy sealed under key, as a launcher that seals will write it.
func sealTokenObject(t *testing.T, b *bk, key []byte) {
	t.Helper()
	ctx := context.Background()
	data, _, err := b.bucket.Read(ctx, token.ObjectKey(repoSlug, runID))
	if err != nil {
		t.Fatal(err)
	}
	if err := token.DeleteObject(ctx, b.bucket, repoSlug, runID); err != nil {
		t.Fatal(err)
	}
	if err := token.PutObject(ctx, b.bucket, repoSlug, runID, string(data), key); err != nil {
		t.Fatal(err)
	}
}

// TestASealedTokenBootstrapsAndTheKeyIsNeverPublished: the runner's half of
// the seal (Slice A) reads a sealed object end to end — the run bootstraps,
// takes its token and runs — and the key it was handed is registered with the
// redactor before anything else happens, so nothing the run publishes carries
// it.
func TestASealedTokenBootstrapsAndTheKeyIsNeverPublished(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "50", capScript(3)...)
	key := sealKeyFor(0x22)
	enc := token.EncodeKey(key)
	sealTokenObject(t, b, key)
	b.deps.Backend.TokenMarked, b.deps.Backend.TokenKey = true, key
	// What the run holds the moment it starts, and what its redactor does
	// with it then. Both are read through the runner's own hooks, because the
	// registration must happen before bootstrap rather than where the key is
	// used (takeBudgetToken).
	var atStart []string
	var redacted, hidden string
	runner.OnRun(t, func(h *runner.RunHandle) {
		atStart = h.Secrets()
		redacted = h.Redact("the job's marker is " + enc)
		hidden = h.Redact("the key itself is " + string(key))
	})
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("a sealed token did not run: rec = %+v, err = %v", rec, err)
	}
	if !b.tokenObjectGone() {
		t.Fatal("the sealed object was not taken")
	}
	if !slices.Contains(atStart, enc) {
		t.Errorf("the seal key is not among the values registered at the start of the run: %v", atStart)
	}
	if strings.Contains(redacted, enc) || strings.Contains(hidden, string(key)) {
		t.Errorf("the redactor leaves the key readable: %q, %q", redacted, hidden)
	}
	if strings.Contains(b.logs.String(), enc) {
		t.Error("the key reaches the run's log")
	}
	if keys := objectsContaining(t, b.harness, enc); len(keys) > 0 {
		t.Errorf("the key is stored in %v", keys)
	}
}

// TestAPlaintextTokenIsRefusedWhenTheMarkerCarriesAKey: §7.4's row, at the
// runner's level. A mixed deployment is not an unsealing one: a run handed a
// key refuses a plaintext object rather than reading it, and the refusal names
// the object and never the key. This is the test the "accept plaintext with a
// key" hand mutation breaks.
func TestAPlaintextTokenIsRefusedWhenTheMarkerCarriesAKey(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "50", capScript(3)...)
	key := sealKeyFor(0x33)
	enc := token.EncodeKey(key)
	b.deps.Backend.TokenMarked, b.deps.Backend.TokenKey = true, key
	// The fixture's object is the plaintext one the launcher writes today.
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("a plaintext object with a key ran: rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(rec.Reason, "sealed") {
		t.Errorf("the refusal does not name the seal: %q", rec.Reason)
	}
	for _, text := range []string{rec.Reason, b.logs.String()} {
		if strings.Contains(text, enc) || strings.Contains(text, string(key)) {
			t.Fatal("the key reaches what the run publishes")
		}
	}
	// The take consumed the object before it refused it, which is TakeObject's
	// shipped order: the object is deleted before the token is checked, so a
	// refusal never leaves bytes behind for the next attempt to read.
	if !b.tokenObjectGone() {
		t.Fatal("the refused object was left in the bucket")
	}
}

// TestASealedObjectWithNoKeyIsRefused: with no key presented — a marker of
// "1", which is today's launcher — a sealed object is the theft case and not a
// plaintext token, so the run refuses instead of reading the bytes.
func TestASealedObjectWithNoKeyIsRefused(t *testing.T) {
	b := newBK(t, gwConfig(t, ""), "enforce", "50", capScript(3)...)
	sealTokenObject(t, b, sealKeyFor(0x44))
	rec, err := b.run(t, implement("feature"), review("ship", 0))
	if err == nil || rec == nil || rec.Status != runstore.StatusInfraError {
		t.Fatalf("a sealed object with no key ran: rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(rec.Reason, "sealed") {
		t.Errorf("the refusal does not name the seal: %q", rec.Reason)
	}
}
