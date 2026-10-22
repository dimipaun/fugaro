package keychain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
)

// testKey is the material every test here signs with. It is obviously not a
// secret and is asserted to be one below: nothing in this package's tests is
// ever a real key (M5).
const testKey = "not-a-secret-test-key-0001"

// TestParseItem is M5's shape, one row per way a configured name can be wrong.
// The item name is what the consumer's settings hold (D113b), so this function
// is the whole of what a config file can put into a keychain query.
func TestParseItem(t *testing.T) {
	long := strings.Repeat("a", ItemMax)
	tooLong := strings.Repeat("a", ItemMax+1)
	cases := []struct {
		name    string
		item    string
		project string
		ok      bool
	}{
		{name: "the project's name in it", item: "fugaro-mailbox-aurora", project: "aurora", ok: true},
		{name: "every character of the alphabet", item: "A.z_0-9", project: "A", ok: true},
		{name: "exactly ItemMax characters", item: long, project: "a", ok: true},
		{name: "no project to check against", item: "anything-at-all", project: "", ok: true},
		{name: "empty", item: "", project: "aurora"},
		{name: "one character too long", item: tooLong, project: "a"},
		{name: "a space", item: "fugaro aurora", project: "aurora"},
		{name: "a path separator", item: "fugaro/aurora", project: "aurora"},
		{name: "a quote", item: "fugaro'aurora", project: "aurora"},
		{name: "a newline", item: "fugaro\naurora", project: "aurora"},
		{name: "a command substitution", item: "fugaro$(id)aurora", project: "aurora"},
		{name: "a leading dash", item: "-aurora", project: "aurora"},
		{name: "another project's name", item: "fugaro-mailbox-borealis", project: "aurora"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item, err := ParseItem(c.item, c.project)
			if c.ok != (err == nil) {
				t.Fatalf("ParseItem(%q, %q) = %v, want ok=%v", c.item, c.project, err, c.ok)
			}
			if !c.ok {
				return
			}
			if item.Name() != c.item || item.String() != c.item {
				t.Errorf("the item does not answer its own name: Name() = %q, String() = %q, want %q", item.Name(), item.String(), c.item)
			}
		})
	}
}

// TestKeyIsNeverPrinted is T2's headline security test, and the one the plan's
// red step names: M5 forbids the key in any output, and every shape a printer
// can ask for one must answer Redacted. A key in a log line or in `--json` is a
// key an operator's terminal, a shell history or a bug report carries, and the
// material is what a forged action on the operator's behalf is computed with.
func TestKeyIsNeverPrinted(t *testing.T) {
	key := NewKey([]byte(testKey))
	doc, err := json.Marshal(map[string]any{"key": key, "n": key.Len()})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// Every printing of a Key there is: the verbs, a struct holding one (a
	// Gate, a config, a log line's fields), a slice of them, JSON, and a
	// string built from one.
	// A log line, through the standard library's own logger. slog formats a
	// value with the verbs — it reaches String() only for a type that has no
	// Format — so this row and the hand-built ones below are the two paths a
	// log line can take.
	var line bytes.Buffer
	slog.New(slog.NewTextHandler(&line, nil)).Info("signing", "key", key, "gate", struct{ K Key }{key})
	printings := map[string]string{
		"%v":             fmt.Sprintf("%v", key),
		"%s":             fmt.Sprintf("%s", key),
		"%q":             fmt.Sprintf("%q", key),
		"%x":             fmt.Sprintf("%x", key),
		"%X":             fmt.Sprintf("%X", key),
		"%#v":            fmt.Sprintf("%#v", key),
		"%+v":            fmt.Sprintf("%+v", key),
		"%v in a struct": fmt.Sprintf("%v", struct{ K Key }{key}),
		"%v in a slice":  fmt.Sprintf("%v", []Key{key}),
		"Errorf":         fmt.Errorf("the key is %v", key).Error(),
		"json":           string(doc),
		// The paths that reach String() without a format verb: a caller
		// building a message by hand, and slog's own Stringer branch.
		"a hand-built error":    errors.New("could not sign with " + key.String()).Error(),
		"a hand-built log line": "key=" + key.String(),
		"slog":                  line.String(),
	}
	for what, got := range printings {
		if strings.Contains(got, testKey) {
			t.Errorf("%s printed the key: %s", what, got)
		}
		if !strings.Contains(got, Redacted) {
			t.Errorf("%s does not say %s: %s", what, Redacted, got)
		}
	}
	if key.Len() != len(testKey) {
		t.Errorf("Len() = %d, want %d: the length is safe to print and is what a diagnostic uses", key.Len(), len(testKey))
	}
}

// TestKeyBytesAreCopies: the material leaves through Bytes only, and both ends
// of that are copies, so wiping the buffer a Key was built from — or the buffer
// Bytes handed out — does not reach through the value.
func TestKeyBytesAreCopies(t *testing.T) {
	material := []byte(testKey)
	key := NewKey(material)
	for i := range material {
		material[i] = 'x'
	}
	got := key.Bytes()
	if string(got) != testKey {
		t.Fatalf("NewKey aliased its caller's buffer: Bytes() = %q", got)
	}
	for i := range got {
		got[i] = 'y'
	}
	if again := key.Bytes(); string(again) != testKey {
		t.Errorf("Bytes() handed out the key itself: %q", again)
	}
}

// TestKeyFrom is what a keychain program's standard output becomes. The value
// is the operator's key: the one trailing newline the tools print is theirs and
// is removed, and what is left must be a key mac.js can sign with (a non-empty
// UTF-8 string with no control character).
func TestKeyFrom(t *testing.T) {
	cases := []struct {
		name  string
		out   string
		key   string
		why   string // a substring of the reason, "" when out is a key
		empty bool
	}{
		{name: "the value and the tool's newline", out: testKey + "\n", key: testKey},
		{name: "a value with no trailing newline", out: testKey, key: testKey},
		{name: "a CRLF line ending", out: testKey + "\r\n", key: testKey},
		{name: "spaces inside the value", out: "a key with spaces\n", key: "a key with spaces"},
		{name: "empty output", out: "", empty: true, why: "empty"},
		{name: "one newline and nothing else", out: "\n", empty: true, why: "empty"},
		{name: "not UTF-8", out: "\xff\xfe\x00key\n", why: "not UTF-8"},
		{name: "a control character inside", out: "key\x01with\x02controls\n", why: "control character"},
		{name: "a second line", out: "one key\ntwo keys\n", why: "control character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, why := keyFrom([]byte(c.out))
			if c.why != "" {
				if !strings.Contains(why, c.why) {
					t.Fatalf("keyFrom(%q) why = %q, want it to mention %q", c.out, why, c.why)
				}
				if key.Len() != 0 {
					t.Errorf("keyFrom(%q) answered a %d-byte key as well as a reason", c.out, key.Len())
				}
				if strings.Contains(why, testKey) {
					t.Errorf("the reason carries the value: %q", why)
				}
				return
			}
			if why != "" {
				t.Fatalf("keyFrom(%q) why = %q, want a key", c.out, why)
			}
			if got := string(key.Bytes()); got != c.key {
				t.Errorf("keyFrom(%q) = %q, want %q", c.out, got, c.key)
			}
		})
	}
}

// TestReaderForEveryPlatform pins every platform's argv. The item's name is the
// only variable part of it — it is not a secret and may be printed (M5) — and
// the key is not an argv element on either platform: it is what the program
// prints, and nothing else.
func TestReaderForEveryPlatform(t *testing.T) {
	item, err := ParseItem("fugaro-mailbox-aurora", "aurora")
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	cases := []struct {
		goos     string
		program  string
		args     []string
		notFound int
	}{
		{
			goos:     "darwin",
			program:  "security",
			args:     []string{"find-generic-password", "-s", "fugaro-mailbox-aurora", "-w"},
			notFound: 44,
		},
		{
			goos:     "linux",
			program:  "secret-tool",
			args:     []string{"lookup", "service", "fugaro-mailbox-aurora"},
			notFound: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.goos, func(t *testing.T) {
			r, err := readerFor(c.goos)
			if err != nil {
				t.Fatalf("readerFor(%q): %v", c.goos, err)
			}
			if r.program != c.program {
				t.Errorf("program = %q, want %q", r.program, c.program)
			}
			got := r.args(item)
			if strings.Join(got, "\x00") != strings.Join(c.args, "\x00") {
				t.Errorf("argv = %q, want %q", got, c.args)
			}
			if r.notFound != c.notFound {
				t.Errorf("notFound = %d, want %d", r.notFound, c.notFound)
			}
			// The key cannot be in an argv: nothing here was given it. The
			// assertion is that the argv holds the item's name and only it.
			for _, a := range got {
				if a != item.Name() && !strings.HasPrefix(a, "-") && a != "find-generic-password" && a != "lookup" && a != "service" {
					t.Errorf("argv holds %q, which is not the item's name nor a fixed word", a)
				}
				if strings.Contains(a, testKey) {
					t.Errorf("argv holds the key: %q", a)
				}
			}
		})
	}
}

// TestPlatformsWithNoReaderRefuse: Windows has no reader in this build, and
// neither has any platform nobody wrote one for. The answer is the exit-2 row
// (§4.4: an unopenable backend), never a zero key and never a silent success.
func TestPlatformsWithNoReaderRefuse(t *testing.T) {
	for _, goos := range []string{"windows", "plan9", "js", ""} {
		t.Run("goos="+goos, func(t *testing.T) {
			if _, err := readerFor(goos); err == nil {
				t.Fatalf("readerFor(%q) answered a reader", goos)
			}
			store := noBackend{why: errors.New("no reader on " + goos)}
			item, err := ParseItem("fugaro-mailbox-aurora", "aurora")
			if err != nil {
				t.Fatalf("ParseItem: %v", err)
			}
			key, err := store.Key(context.Background(), item)
			if !errors.Is(err, ErrBackend) {
				t.Fatalf("Key on a platform with no reader = %v, want ErrBackend", err)
			}
			if errors.Is(err, ErrNoKey) {
				t.Errorf("an unopenable backend is not an absent key: %v", err)
			}
			if key.Len() != 0 {
				t.Errorf("a refused lookup answered a %d-byte key", key.Len())
			}
		})
	}
}

// TestSystemReadsThisPlatform: System is readerFor(runtime.GOOS), so the
// program this process would run is the one the table above pins.
func TestSystemReadsThisPlatform(t *testing.T) {
	store := System()
	r, err := readerFor(runtime.GOOS)
	if err != nil {
		if _, ok := store.(noBackend); !ok {
			t.Fatalf("System() = %T on a platform with no reader", store)
		}
		return
	}
	cs, ok := store.(*commandStore)
	if !ok {
		t.Fatalf("System() = %T, want *commandStore", store)
	}
	if cs.reader.program != r.program || cs.run == nil {
		t.Errorf("System() runs %q (run nil: %v), want %q", cs.reader.program, cs.run == nil, r.program)
	}
}

// TestCommandStoreClassifiesEveryExit is what the command's exit codes are
// built from (§4.4): an absent item and an unreadable keychain are two rows,
// and no error either produces carries a byte of what the program printed.
func TestCommandStoreClassifiesEveryExit(t *testing.T) {
	item, err := ParseItem("fugaro-mailbox-aurora", "aurora")
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	r, err := readerFor("linux")
	if err != nil {
		t.Fatalf("readerFor: %v", err)
	}
	// What procgroup.Run really answers when the program cannot be started:
	// the program and the OS's complaint about it, and nothing the child
	// printed (TestTheProgramsComplaintIsNeverEchoed is the real path).
	complaint := errMissing
	cases := []struct {
		name string
		out  string
		code int
		err  error
		want error // nil means the lookup answered a key
	}{
		{name: "the key", out: testKey + "\n", code: 0},
		{name: "no such item", out: "", code: 1, want: ErrNoKey},
		{name: "an empty item", out: "\n", code: 0, want: ErrNoKey},
		{name: "another failure", out: "", code: 2, want: ErrBackend},
		{name: "could not be run", code: -1, err: complaint, want: ErrBackend},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &commandStore{reader: r, run: func(context.Context, string, ...string) ([]byte, int, error) {
				return []byte(c.out), c.code, c.err
			}}
			key, err := s.Key(context.Background(), item)
			if c.want == nil {
				if err != nil {
					t.Fatalf("Key = %v, want the key", err)
				}
				if got := string(key.Bytes()); got != testKey {
					t.Errorf("Key = %q, want %q", got, testKey)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("Key = %v, want %v", err, c.want)
			}
			if key.Len() != 0 {
				t.Errorf("a refused lookup answered a %d-byte key", key.Len())
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("the error carries what the program printed: %v", err)
			}
			// M5: the item's name may be printed and the key may not. A
			// missing key is diagnosed by naming the item.
			if c.want == ErrNoKey && !strings.Contains(err.Error(), item.Name()) {
				t.Errorf("the absence of a key does not name the item: %v", err)
			}
			if c.want == ErrBackend && !strings.Contains(err.Error(), r.program) {
				t.Errorf("an unreadable keychain does not name the program: %v", err)
			}
		})
	}
}

// TestTheItemNameAppearsInTheDiagnostic is the other half of M5's rule: the key
// may never be printed and the item's name always may. An operator told only
// "there is no key" has nothing to act on, and the name is not a secret — it is
// what they check their own keychain with. Both failures name something safe:
// the item for an absent key, the program for a keychain that could not be read.
func TestTheItemNameAppearsInTheDiagnostic(t *testing.T) {
	item, err := ParseItem("fugaro-mailbox-aurora", "aurora")
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	r, err := readerFor("linux")
	if err != nil {
		t.Fatalf("readerFor: %v", err)
	}
	cases := []struct {
		name  string
		store Store
		names string
	}{
		{name: "an item the keychain does not hold", store: &Fake{}, names: item.Name()},
		{name: "an unreadable keychain", store: &Fake{Backend: ErrBackend}, names: "the OS keychain"},
		{
			name:  "a program that could not be run",
			store: &commandStore{reader: r, run: func(context.Context, string, ...string) ([]byte, int, error) { return nil, -1, errMissing }},
			names: r.program,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, err := c.store.Key(t.Context(), item)
			if err == nil {
				t.Fatalf("Key answered a key: %q", key.Bytes())
			}
			if !strings.Contains(err.Error(), c.names) {
				t.Errorf("the diagnostic does not name %q: %v", c.names, err)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("the diagnostic carries a key: %v", err)
			}
			if key.Len() != 0 {
				t.Errorf("a refused lookup answered a %d-byte key", key.Len())
			}
		})
	}
	// The item is printable, and it is the same string a keychain program is
	// asked for.
	if item.String() != "fugaro-mailbox-aurora" {
		t.Errorf("the item prints %q, want its name", item.String())
	}
}

// errMissing is the OS's complaint about a program that is not installed: what
// procgroup.Run really answers, and the only text of a failure this package
// ever echoes.
var errMissing = errors.New(`exec: "secret-tool": executable file not found in $PATH`)

// TestTheProgramsComplaintIsNeverEchoed runs a real child through the real
// runner: the keychain tool complains on its standard error, and the complaint
// is discarded rather than captured. A failing tool's own text could echo what
// it was asked for, and an error string is exactly where a key must never
// appear — so this package never reads that stream at all.
func TestTheProgramsComplaintIsNeverEchoed(t *testing.T) {
	const env = "FUGARO_KEYCHAIN_STDERR_HELPER"
	if os.Getenv(env) == "1" {
		fmt.Fprintln(os.Stderr, testKey)
		os.Exit(3)
	}
	item, err := ParseItem("fugaro-mailbox-aurora", "aurora")
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	s := &commandStore{
		reader: reader{program: os.Args[0], args: func(Item) []string {
			return []string{"-test.run=^TestTheProgramsComplaintIsNeverEchoed$"}
		}, notFound: 44},
		run: func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
			// The helper needs its variable set; runProcgroup is what the
			// real Store uses, so this test drives that, not a stand-in.
			t.Setenv(env, "1")
			return runProcgroup(ctx, name, args...)
		},
	}
	key, err := s.Key(context.Background(), item)
	if err == nil {
		t.Fatalf("Key answered a key from a program that failed: %q", key.Bytes())
	}
	if !errors.Is(err, ErrBackend) {
		t.Fatalf("Key = %v, want ErrBackend", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("the child's complaint reached the error: %v", err)
	}
}

// TestTheFakeAnswersLikeAKeychain: the Fake is what T5's command tests hold, so
// it must answer the real package's rows — an absent item, a value that is not
// a key, and a backend that cannot be read — and it must not be more permissive
// than the programs it stands in for.
func TestTheFakeAnswersLikeAKeychain(t *testing.T) {
	item, err := ParseItem("fugaro-mailbox-aurora", "aurora")
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	ctx := context.Background()

	f := &Fake{Keys: map[string]string{item.Name(): testKey + "\n"}}
	key, err := f.Key(ctx, item)
	if err != nil {
		t.Fatalf("Key = %v, want the key", err)
	}
	if got := string(key.Bytes()); got != testKey {
		t.Errorf("Key = %q, want %q", got, testKey)
	}

	// An item the fake does not hold, and one it holds nothing usable at: both
	// are M4's availability row, never a zero key.
	for name, value := range map[string]string{"empty": "", "not UTF-8": "\xff\xfe", "controls": "a\nb"} {
		f = &Fake{Keys: map[string]string{item.Name(): value}}
		if _, err := f.Key(ctx, item); !errors.Is(err, ErrNoKey) {
			t.Errorf("a %s value = %v, want ErrNoKey", name, err)
		}
	}
	f = &Fake{Keys: map[string]string{}}
	if _, err := f.Key(ctx, item); !errors.Is(err, ErrNoKey) {
		t.Errorf("an item the fake does not hold = %v, want ErrNoKey", err)
	}
	if lookups := f.Lookups(); len(lookups) != 1 || lookups[0] != item.Name() {
		t.Errorf("the fake does not record the lookups: %v", lookups)
	}
	f.Forget()
	if len(f.Lookups()) != 0 {
		t.Errorf("Forget left %v", f.Lookups())
	}

	// A broken backend is ErrBackend and is not overridden by a stored key.
	f = &Fake{Keys: map[string]string{item.Name(): testKey}, Backend: ErrBackend}
	if _, err := f.Key(ctx, item); !errors.Is(err, ErrBackend) {
		t.Errorf("a broken backend = %v, want ErrBackend", err)
	}
	// And the fake's lookups are recorded whatever the answer.
	f = &Fake{Keys: map[string]string{item.Name(): testKey}}
	for i := 0; i < 2; i++ {
		if _, err := f.Key(ctx, item); err != nil {
			t.Fatalf("Key = %v", err)
		}
	}
	if got := f.Lookups(); len(got) != 2 {
		t.Errorf("Lookups() = %v, want two lookups", got)
	}
}
