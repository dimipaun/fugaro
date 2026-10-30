// Package keychain reads the per-pairing MAC key from the OS keychain, by item
// name, and never prints it.
//
// This is task T2 of docs/plans/2026-10-18-mobile-mailbox-consumer.md (mobile
// P4.1) and it lands the read M5 requires: the pairing key D171's tag is
// computed with lives in the operator's OS keychain, the consumer's settings
// hold only the item's **name** (Voice D42(2), D113b), and the key itself is
// read once per pairing, held in memory for the life of the process, never
// written to disk, never passed in argv and never logged — including in the
// error paths, where the message names the item and never the value.
//
// **Who puts the key there is P6.6**, `fugaro pair`'s delivery: Put writes one
// item per pairing, and Delete removes one when a delivery could not be written
// or when `pair revoke` revokes that phone. Until a pairing has run the item is
// absent, and an absent item is not a bug here: it is M4's first row, the
// availability check the command refuses to start on (§8.3). The tests use a
// Fake; no test in this repository touches a real keychain, and no agent reads
// one (M5).
//
// **The key never leaves through a printer.** Key's every fmt verb, its JSON
// and its GoString answer Redacted, so the one shape of leak this package could
// produce — a key in a log line or in a `--json` document — needs a deliberate
// call to Bytes rather than a `%v`. The bytes leave through Bytes only, which
// is what the MAC function takes (internal/mailbox's MacOf).
//
// **Nothing this package runs puts the key in an argv.** The programs below
// take the item's name, which is not a secret (M5) and may be printed, and
// print the key on their standard output. Their standard error is discarded
// rather than captured: a failing tool's own complaint could echo what it was
// asked for, and an error string is exactly where a key must never appear.
package keychain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/procgroup"
)

// ItemMax bounds an item name: M5's 64 characters, and every character the
// name may hold is one byte, so bytes and characters are the same count here.
const ItemMax = 64

// Redacted is what every printing of a Key says instead of the key. It is
// shared/digest's own spelling (REDACTED), so one shape of redaction covers
// this package and everything the command prints.
const Redacted = "[REDACTED]"

// itemRE is M5's alphabet for an item name: `[A-Za-z0-9._-]`, which is what a
// config file can hold and what cannot name an arbitrary keychain query (no
// space, no quote, no path separator).
var itemRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Item is a validated keychain item name: the name the pairing key lives at,
// never the key. Only ParseItem builds one, so a name no config could hold
// never reaches a lookup, and it is deliberately printable — M5's rule is that
// the name may be printed and the key may not.
type Item struct{ name string }

// ParseItem validates a configured item name (M5): at most ItemMax characters
// of `[A-Za-z0-9._-]`, not beginning with a dash (a name is an argv element of
// a keychain program, M11), with the project's name in it, so a config file
// cannot name an arbitrary keychain query — and so two projects on one machine
// cannot be made to read each other's pairing key.
//
// project is the Fugaro project's name (internal/localcfg's Config.Name). An
// empty project is not a project name to look for: the containment rule cannot
// be applied to it and the shape rule alone decides, which is why every caller
// that has a project passes it.
func ParseItem(name, project string) (Item, error) {
	switch {
	case name == "":
		return Item{}, errors.New("keychain: the item name is empty")
	case len(name) > ItemMax:
		return Item{}, fmt.Errorf("keychain: the item name %q is longer than %d characters", name, ItemMax)
	case !itemRE.MatchString(name):
		return Item{}, fmt.Errorf("keychain: the item name %q is not a name (at most %d characters of A-Z, a-z, 0-9, '.', '_' and '-')", name, ItemMax)
	case strings.HasPrefix(name, "-"):
		// M5's alphabet plus the one shape it leaves: a name that begins with
		// a dash is the argument-injection shape, since the name is an argv
		// element of a keychain program (M11). It is refused here rather than
		// left to whichever option parser the platform's tool happens to use.
		return Item{}, fmt.Errorf("keychain: the item name %q begins with '-' and would be read as a flag by a keychain program", name)
	case project != "" && !strings.Contains(name, project):
		return Item{}, fmt.Errorf("keychain: the item name %q does not contain the project's name %q, so it could name another project's key", name, project)
	}
	return Item{name: name}, nil
}

// Name is the item's name, for a keychain tool's argv and for a diagnostic.
func (i Item) Name() string { return i.name }

// String prints the item's name, deliberately: M5's rule is that the name may
// be printed and the key may not.
func (i Item) String() string { return i.name }

// Key is the per-pairing MAC key (D171): the bytes the tag is computed with,
// read once per pairing and held in memory for the life of the process.
//
// **No format verb prints it.** %v, %s, %q, %x, %X and %#v all answer Redacted,
// and so do encoding/json and a log line built from either, because the failure
// M5 names — a leaked key is a forged action on the operator's behalf — is one
// `%v` away in a command whose whole job is to print what it did. The material
// leaves through Bytes, which the MAC function calls and no printer does.
type Key struct{ material []byte }

// NewKey wraps key material, copying it: the caller's buffer is not aliased,
// so wiping it afterwards does not leave the key readable through this value.
func NewKey(material []byte) Key { return Key{material: bytes.Clone(material)} }

// Bytes is the key material, for the one caller that computes a tag with it.
// The result is a copy: mutating it does not change the key.
func (k Key) Bytes() []byte { return bytes.Clone(k.material) }

// Len is the key's length in bytes. It is safe to print: a length says nothing
// about a key.
func (k Key) Len() int { return len(k.material) }

// String, Format, GoString and MarshalJSON are the redaction, in every shape a
// printer can ask for one. Format answers every verb, so a verb added later is
// redacted by default rather than by remembering this list; GoString is what
// %#v would use if Format did not win first; MarshalJSON is the path Format
// cannot reach, since encoding/json never consults it.
func (k Key) String() string { return Redacted }

func (k Key) Format(state fmt.State, _ rune) { io.WriteString(state, Redacted) }

func (k Key) GoString() string { return Redacted }

func (k Key) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// The two ways a lookup fails, and the command classifies them differently
// (§4.4): ErrNoKey is M4's availability row — there is no pairing key, so the
// command refuses to start with exit 1 and runs no pass — and ErrBackend is the
// keychain that could not be read at all, which is exit 2, the same answer a
// database that cannot be reached gets. Both are matched with errors.Is; the
// messages around them name the item (or the program that failed) and never the
// key.
var (
	// ErrNoKey: there is no key at the item — it is absent, empty, or holds a
	// value that is not a key.
	ErrNoKey = errors.New("keychain: no key at the item")
	// ErrBackend: the OS keychain could not be read.
	ErrBackend = errors.New("keychain: the OS keychain could not be read")
)

// noKey is ErrNoKey with the item named and the reason told apart: M5 allows
// the item's name in a diagnostic and forbids the value, and §8.3's three
// triggers (absent, unreadable, not of the shape) are three different sentences
// for the operator.
func noKey(item Item, why string) error {
	return fmt.Errorf("%w %q: %s", ErrNoKey, item.Name(), why)
}

// backendErr is ErrBackend with the failure described by what is safe to say:
// the program and its exit status. Never a byte of either stream.
func backendErr(why string) error { return fmt.Errorf("%w: %s", ErrBackend, why) }

// Store is an OS keychain, as this repository needs one: it answers one item's
// key, and it is what the pairing writes that key through. It is an interface
// so that the tests — and the command tests T5 writes — hold a Fake instead of
// a real keychain, which no test in this repository touches (M5).
type Store interface {
	// Key reads item's key. It returns an error wrapping ErrNoKey when there
	// is no key at the item and one wrapping ErrBackend when the keychain
	// itself could not be read; neither error ever carries the key.
	Key(ctx context.Context, item Item) (Key, error)
	// Put stores key at item, replacing whatever was there. It is the write
	// `fugaro pair` makes once per pairing (§7.1 step 6, M5): the item is one
	// per pairing, and the value is what Key would read back.
	Put(ctx context.Context, item Item, key Key) error
	// Delete removes item. An item that is not there is not an error: absence
	// is the state the caller asked for, and both callers reach it that way —
	// the rollback of a delivery that could not be written, and `pair revoke`.
	Delete(ctx context.Context, item Item) error
}

// run is how a keychain program runs: one argv, no shell — the shape of
// internal/procgroup.Run, which is what the real one is (M11: nothing this
// command runs goes through a shell). stdin is where a *storing* program reads
// the key, so that the key is in no argv.
type run func(ctx context.Context, name string, stdin io.Reader, args ...string) (stdout []byte, code int, err error)

// reader is one platform's keychain program: what to run, the argv it takes,
// and the exit code that means "there is no such item".
type reader struct {
	program string
	args    func(Item) []string
	// notFound is the program's own "no such item" status. It is a field
	// rather than a shared rule because the two platforms disagree: security
	// answers errSecItemNotFound (44) and secret-tool answers 1.
	notFound int
}

// writer is one platform's storing side: the program, the argv that stores an
// item and the argv that removes one, and the exit code the removing argv
// answers when the item is already gone — which is not a failure.
type writer struct {
	program  string
	put      func(Item) []string
	del      func(Item) []string
	notFound int
}

// commandStore is the Store every platform's reader and writer become.
type commandStore struct {
	reader reader
	// writer is this platform's storing side, or nil where it has none. The
	// two halves are separate programs and a platform can have one without the
	// other: macOS reads a key through `security` and has no way to store one
	// that does not put the key in an argv (M11), which is what noStore says.
	writer  *writer
	noStore string
	run     run
}

// Key reads the item through the platform's program and turns what it printed
// into a key.
func (s *commandStore) Key(ctx context.Context, item Item) (Key, error) {
	stdout, code, err := s.run(ctx, s.reader.program, nil, s.reader.args(item)...)
	switch {
	case err != nil:
		// The program could not be started, or the context ended it. Its own
		// error text is not echoed: what is safe to say is the program.
		return Key{}, backendErr(fmt.Sprintf("%s could not be run: %v", s.reader.program, err))
	case code == s.reader.notFound:
		return Key{}, noKey(item, "the keychain holds no such item")
	case code != 0:
		return Key{}, backendErr(fmt.Sprintf("%s exited %d", s.reader.program, code))
	}
	key, why := keyFrom(stdout)
	if why != "" {
		return Key{}, noKey(item, why)
	}
	return key, nil
}

// Put stores the key through the platform's storing program, which reads it
// from standard input: the key is in no argv and in no environment, so nothing
// this package runs is a way for another process to read it out of `ps` or out
// of the process table (M11).
//
// Nothing it prints is echoed, for the reason the read side says: a failing
// tool's complaint could carry what it was asked for, and what it was asked for
// is the key. The exit status and the program are what is safe to say.
func (s *commandStore) Put(ctx context.Context, item Item, key Key) error {
	if s.writer == nil {
		return backendErr(s.noStore)
	}
	_, code, err := s.run(ctx, s.writer.program, bytes.NewReader(key.Bytes()), s.writer.put(item)...)
	switch {
	case err != nil:
		return backendErr(fmt.Sprintf("%s could not be run: %v", s.writer.program, err))
	case code != 0:
		return backendErr(fmt.Sprintf("%s exited %d storing the key at %q", s.writer.program, code, item.Name()))
	}
	return nil
}

// Delete removes the item through the platform's storing program. The item's
// absence is not an error: `secret-tool clear` answers 1 for an item that is
// not there, and a caller removing a key it is not sure it wrote — the rollback
// of a delivery that could not be written — asked for exactly that state.
func (s *commandStore) Delete(ctx context.Context, item Item) error {
	if s.writer == nil {
		return backendErr(s.noStore)
	}
	_, code, err := s.run(ctx, s.writer.program, nil, s.writer.del(item)...)
	switch {
	case err != nil:
		return backendErr(fmt.Sprintf("%s could not be run: %v", s.writer.program, err))
	case code == 0 || code == s.writer.notFound:
		return nil
	default:
		return backendErr(fmt.Sprintf("%s exited %d deleting the key at %q", s.writer.program, code, item.Name()))
	}
}

// keyFrom turns what a keychain program printed into a key, or says why the
// value is not one. The second answer is a string rather than an error so that
// the caller wraps it as the availability refusal it is (§8.3: "absent,
// unreadable, or not of M5's shape" is one row, one exit).
//
// The three shape rules are the ones that make the value usable as a key and
// nothing more: both programs print the value followed by one newline, so one
// trailing line ending is the tool's and is removed; what is left must be
// non-empty (an empty item is not a key), must be UTF-8 (the module's key is a
// JavaScript string, and mac.js refuses anything else) and must carry no
// control character (a value with a newline in it is a value the operator did
// not store, or a stream this package misread).
func keyFrom(stdout []byte) (Key, string) {
	material := bytes.TrimSuffix(stdout, []byte("\n"))
	material = bytes.TrimSuffix(material, []byte("\r"))
	switch {
	case len(material) == 0:
		return Key{}, "the item holds an empty value"
	case !utf8.Valid(material):
		return Key{}, "the value stored there is not UTF-8, so it is not the string mac.js signs with"
	case bytes.IndexFunc(material, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0:
		return Key{}, "the value stored there carries a control character, so it is not one key"
	}
	return NewKey(material), ""
}

// readerFor is the platform's keychain program. goos is a parameter rather than
// a read of runtime.GOOS so that every platform's argv is testable from one
// checkout — the rule internal/localcfg/platformdirs.go's UserDir already
// follows for the Windows directories — because the alternative is argv code no
// test on this runner could ever exercise.
//
// **Neither argv carries the key**: the item's name is an argument and is not a
// secret, and each program is asked to print only the value — `security`'s -w
// is what makes it print the password and nothing else, and `secret-tool
// lookup` prints the value alone.
//
// Windows has no reader in this build. The Windows credential store is reached
// through advapi32's CredReadW rather than through a program, which is code no
// test on this runner could compile or run, and nothing needs it yet: the key
// does not exist until P6.6 runs, and this package's answer on that platform is
// the honest one — an unopenable backend (ErrBackend, exit 2), not a silent
// empty key.
func readerFor(goos string) (reader, error) {
	switch goos {
	case "darwin":
		return reader{
			program: "security",
			// -w prints the password alone; the item is named as the generic
			// password's service, which is the field the storing side sets.
			args:     func(i Item) []string { return []string{"find-generic-password", "-s", i.Name(), "-w"} },
			notFound: 44, // errSecItemNotFound
		}, nil
	case "linux":
		return reader{
			program: "secret-tool",
			// libsecret's lookup takes attribute pairs; `service` is the
			// attribute this package's storing side uses.
			args:     func(i Item) []string { return []string{"lookup", "service", i.Name()} },
			notFound: 1,
		}, nil
	case "windows":
		return reader{}, errors.New("this build has no Windows keychain reader (the Windows credential store is reached through advapi32, not through a program, and P4.1 reads the macOS keychain and libsecret)")
	default:
		return reader{}, fmt.Errorf("%q is not a platform this build reads a keychain on", goos)
	}
}

// writerFor is the platform's storing program, and the sentence that says why
// there is none. goos is a parameter for readerFor's reason: every platform's
// argv is testable from one checkout.
//
// **The key reaches the storing program on standard input, never as an argv
// element.** `secret-tool store` reads the secret from its standard input, and
// that is the only storing path this build has: `security`'s -w takes the
// password as an argv element, where every process on the machine reads it out
// of `ps` for as long as the call lasts (M11). A platform with no such path
// answers ErrBackend naming it rather than storing a key in the open, exactly
// as the read side refuses Windows.
func writerFor(goos string) (*writer, string) {
	switch goos {
	case "linux":
		return &writer{
			program: "secret-tool",
			// libsecret's store takes attribute pairs and reads the secret
			// from standard input; the label is what a person sees in the
			// keyring UI, and the item's name is not a secret.
			put: func(i Item) []string { return []string{"store", "--label", i.Name(), "service", i.Name()} },
			del: func(i Item) []string { return []string{"clear", "service", i.Name()} },
			// secret-tool answers 1 both for "no such item" and for its own
			// failures; a clear that failed and an item that was already gone
			// leave the same state behind, which is what the caller asked for.
			notFound: 1,
		}, ""
	default:
		return nil, fmt.Sprintf("this build cannot store a pairing key in the %s keychain: the storing program takes the secret as an argv element, where every process on the machine reads it out of `ps`, and no argv of this package carries a key (M11)", goos)
	}
}

// System is the keychain of the machine this process runs on.
//
// A platform this build has no reader for is a Store whose every lookup is
// ErrBackend naming the platform: the command's answer is then the exit-2 row
// (§4.4), which is what an unopenable backend gets, and never a zero key. A
// platform this build has a reader for but no writer keeps its reads and
// answers the writes with the sentence writerFor gives.
func System() Store {
	r, err := readerFor(runtime.GOOS)
	if err != nil {
		return noBackend{why: err}
	}
	w, noStore := writerFor(runtime.GOOS)
	return &commandStore{reader: r, writer: w, noStore: noStore, run: runProcgroup}
}

// noBackend is System's Store on a platform with no reader.
type noBackend struct{ why error }

func (n noBackend) Key(context.Context, Item) (Key, error) {
	return Key{}, backendErr(n.why.Error())
}

func (n noBackend) Put(context.Context, Item, Key) error { return backendErr(n.why.Error()) }

func (n noBackend) Delete(context.Context, Item) error { return backendErr(n.why.Error()) }

// runProcgroup is how a real reader runs its program: in a process group of its
// own, with a deadline the caller sets on the whole group (internal/procgroup,
// the discipline internal/watch/prstate.go's ask uses). The program's standard
// output is capped and kept — it is the key, and it goes into a Key and nowhere
// else — and its standard error is discarded rather than read, because a
// failing tool's complaint is not worth the risk of it echoing what it was
// asked for. stdin is the storing side's, and is nil for a read.
func runProcgroup(ctx context.Context, name string, stdin io.Reader, args ...string) ([]byte, int, error) {
	var out boundedBuffer
	code, err := procgroup.Run(ctx, procgroup.Cmd{Name: name, Args: args, Stdin: stdin, Stdout: &out})
	return out.Bytes(), code, err
}

// boundedMax caps what one keychain program's standard output may occupy. A key
// is a few dozen bytes; a program that prints more than this is not one this
// package asked a question it can answer.
const boundedMax = 4096

// boundedBuffer keeps the first boundedMax bytes written to it and counts, but
// drops, the rest: a runaway program cannot grow this process's memory, and a
// write never fails, so the child is never left blocked on a full pipe. It has
// a mutex of its own because the writer is not always done when the read is:
// procgroup gives up on a child that escaped the process group after its own
// drain timeout, and that child's copy goroutine may still be writing here.
type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := boundedMax - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// Bytes is what was kept, as a copy.
func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf)
}
