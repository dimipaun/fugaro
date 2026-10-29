package keychain

import (
	"context"
	"sync"
)

// Fake is a Store for tests: an in-memory keychain, one map from item name to
// the value the item holds. It is what every test in this repository uses (M5:
// no test touches a real keychain, and no agent reads one), and what T5's
// command tests use in place of the operator's machine.
//
// It answers the shape rules the real readers do, by running the stored value
// through the same keyFrom: a value that is empty, not UTF-8 or carrying a
// control character is ErrNoKey here exactly as it would be from `security` or
// `secret-tool`, so a test cannot pass on a key the operator's keychain would
// refuse. The one trailing line ending a keychain program prints is removed the
// same way, so a stored "k\n" and a stored "k" are the same key.
type Fake struct {
	// Keys is the keychain's contents: item name → what the item holds.
	Keys map[string]string
	// Backend, when non-nil, is what every lookup answers instead of reading
	// Keys: the keychain that could not be read at all (§4.4's exit-2 row).
	// Set it to ErrBackend — or to an error wrapping it — for that row;
	// anything else is a backend failing in its own way, and is returned as
	// it is.
	Backend error

	mu      sync.Mutex
	lookups []string
}

// Key reads the fake's keychain. The error is one of the real package's: it
// wraps ErrNoKey when the fake holds no usable value at the item and is
// Backend when the fake's backend is broken.
func (f *Fake) Key(_ context.Context, item Item) (Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, item.Name())
	if f.Backend != nil {
		return Key{}, f.Backend
	}
	material, ok := f.Keys[item.Name()]
	if !ok {
		return Key{}, noKey(item, "the fake holds no such item")
	}
	key, why := keyFrom([]byte(material))
	if why != "" {
		return Key{}, noKey(item, why)
	}
	return key, nil
}

// Lookups is the item names this Fake was asked for, in order, as a copy. It is
// how a test asserts M5's "read once per pairing": the count is the lookups,
// not the passes.
func (f *Fake) Lookups() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lookups...)
}

// Forget drops the recorded lookups, so a test that has set the fake up can
// count only the lookups that follow.
func (f *Fake) Forget() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = nil
}
