package keychain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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

// TestDarwinWriterKeepsKeyOutOfArgv is M11's rule for the platform this CLI is
// most likely to run on: the key reaches the darwin keychain through `security
// -i`, whose commands come from standard input, so no argv element ever carries
// the key — where every process on the machine would read it out of `ps`. The
// item's name is not a secret (M5) and may sit in the command.
func TestDarwinWriterKeepsKeyOutOfArgv(t *testing.T) {
	item := mustItem(t, "fugaro-mailbox-aurora", "aurora")
	w := darwinWriter(t)
	if w.program != "security" {
		t.Errorf("program = %q, want security", w.program)
	}
	args, stdin, err := w.put(item, NewKey([]byte(testKey)))
	if err != nil {
		t.Fatalf("the storing invocation = %v", err)
	}
	if strings.Join(args, " ") != "-i" {
		t.Errorf("the storing argv = %q, want the single element -i", args)
	}
	for _, a := range args {
		if strings.Contains(a, testKey) {
			t.Errorf("the key is an argv element: %q", a)
		}
	}
	// One command per run, and the key is the last word of it: what the tool
	// reads on standard input is the only place the key appears.
	want := `add-generic-password -U -s "fugaro-mailbox-aurora" -a "fugaro-mailbox-aurora" -w ` + testKey + "\n"
	if string(stdin) != want {
		t.Errorf("the storing command = %q, want %q", stdin, want)
	}

	// Delete names the item and carries no key at all: there is none to carry.
	delArgs, delStdin, err := w.del(item)
	if err != nil {
		t.Fatalf("the deleting invocation = %v", err)
	}
	if strings.Join(delArgs, " ") != "-i" {
		t.Errorf("the deleting argv = %q, want the single element -i", delArgs)
	}
	// The removal names the account too, so that it is the exact inverse of the
	// store rather than a match on the service alone.
	if got, want := string(delStdin), `delete-generic-password -s "fugaro-mailbox-aurora" -a "fugaro-mailbox-aurora"`+"\n"; got != want {
		t.Errorf("the deleting command = %q, want %q", got, want)
	}
	if strings.Contains(string(delStdin), testKey) {
		t.Errorf("the deleting command carries a key: %q", delStdin)
	}
}

// TestDarwinWriterRoundTripsThroughKeyFrom: what the storing command puts on the
// program's standard input is what the reader reads back. The round trip is
// taken through the reader's own decoder, over the words `security -i`'s line
// parser would hand the tool, so a storing side that wrote a value the reader
// refuses — or that split the key into two arguments — fails here rather than
// on an operator's Mac.
func TestDarwinWriterRoundTripsThroughKeyFrom(t *testing.T) {
	item := mustItem(t, "fugaro-mailbox-aurora", "aurora")
	w := darwinWriter(t)
	for _, key := range []string{testKey, "another-not-a-secret-0002", "Aa0-_Z9"} {
		t.Run(key, func(t *testing.T) {
			_, stdin, err := w.put(item, NewKey([]byte(key)))
			if err != nil {
				t.Fatalf("the storing invocation = %v", err)
			}
			words := securityWords(string(stdin))
			// The whole command, word by word: one bare key is what makes this
			// eight words, and the item is the quoted name both times.
			want := []string{"add-generic-password", "-U", "-s", item.Name(), "-a", item.Name(), "-w", key}
			if strings.Join(words, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("security would read %q, want %q", words, want)
			}
			// The reader's half: `find-generic-password -w` prints the value
			// and one newline, and keyFrom is what turns that into a key.
			got, why := keyFrom([]byte(words[len(words)-1] + "\n"))
			if why != "" {
				t.Fatalf("the reader refuses what the storing side wrote: %s", why)
			}
			if string(got.Bytes()) != key {
				t.Errorf("the key read back is %q, want %q", got.Bytes(), key)
			}
		})
	}
}

// TestDarwinWriterRefusesAKeyItCannotPutOnOneLine: the key goes into a command
// line, so a byte that ends the line would be a second command, a byte the
// parser quotes with would be a different value than the one this package
// meant, and any metacharacter at all is a byte whose meaning depends on the
// parsing rules of a program no test here runs. Nothing is escaped: the one
// spelling a pairing key has (EncodeKey's base64url) is one bare word of an
// alphabet that holds none of those bytes, and everything else is refused with
// a sentence that carries no byte of the key.
func TestDarwinWriterRefusesAKeyItCannotPutOnOneLine(t *testing.T) {
	item := mustItem(t, "fugaro-mailbox-aurora", "aurora")
	w := darwinWriter(t)
	for _, key := range []string{
		"", "two words", `a"quote`, `a'quote`, `a\backslash`, "line\nbreak", "tab\there",
		// Printable ASCII that is not base64url: each of these is a byte a
		// parser could read as something other than part of the value, and a
		// key stored as something else while Put reported success is the
		// failure this refusal exists to prevent.
		"Aa0._-~", "a=b", "a+b", "a/b", "a$b", "a;b", "a`b", "a|b", "a&b", "a#b", "a*b",
	} {
		t.Run(key, func(t *testing.T) {
			if _, _, err := w.put(item, NewKey([]byte(key))); err == nil {
				t.Fatalf("a key %q was accepted for a command line", key)
			} else if key != "" && strings.Contains(err.Error(), key) {
				t.Errorf("the refusal carries the key: %v", err)
			}
			s := &commandStore{writer: w, run: func(context.Context, string, io.Reader, ...string) ([]byte, []byte, int, error) {
				t.Error("a key that cannot be written reached the program")
				return nil, nil, 0, nil
			}}
			err := s.Put(t.Context(), item, NewKey([]byte(key)))
			if !errors.Is(err, ErrBackend) {
				t.Fatalf("Put of a key that is not one bare word = %v, want ErrBackend", err)
			}
			if key != "" && strings.Contains(err.Error(), key) {
				t.Errorf("the refusal carries the key: %v", err)
			}
		})
	}
}

// TestSecurityQuotedIsOneWord is ParseItem's other half: the item's name is a
// word of a `security -i` command line, and the alphabet ParseItem enforces is
// what makes quoting it a matter of two characters rather than of escaping. The
// assertion runs both ways — every name ParseItem accepts is quotable, and
// every name it refuses for a quote, a backslash or a control character is
// refused here too, so the writer never depends on the reader's alphabet
// without saying so.
func TestSecurityQuotedIsOneWord(t *testing.T) {
	accepted := []string{"fugaro-mailbox-aurora", "A.z_0-9", strings.Repeat("a", ItemMax)}
	for _, name := range accepted {
		item, err := ParseItem(name, "")
		if err != nil {
			t.Fatalf("ParseItem(%q) = %v", name, err)
		}
		quoted, err := securityQuoted(item.Name())
		if err != nil {
			t.Errorf("securityQuoted(%q) = %v, want a quoted word", name, err)
			continue
		}
		if words := securityWords(quoted); len(words) != 1 || words[0] != name {
			t.Errorf("security would read %q as %q, want one word %q", quoted, words, name)
		}
	}
	refused := []string{"", `fugaro"aurora`, `fugaro\aurora`, "fugaro\naurora", "fugaro\taurora", "fugaro\x7faurora"}
	for _, name := range refused {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseItem(name, ""); err == nil {
				t.Errorf("ParseItem(%q) accepted a name the command line cannot carry", name)
			}
			if _, err := securityQuoted(name); err == nil {
				t.Errorf("securityQuoted(%q) answered a word, want a refusal", name)
			}
		})
	}
}

// TestDarwinWriterReadsTheFailureSecurityHidesInItsOutput: `security -i` answers
// 0 for a command it could not carry out — a locked keychain, a session that
// cannot be asked to unlock it, a command it does not know — so a writer that
// trusted the exit status alone would report a successful store for a keychain
// that holds nothing, and `fugaro pair` would deliver a key that is not there.
// Every shape the tool prints is read here, through the real runner and a real
// child process standing in for the tool, and every one of them is ErrBackend.
func TestDarwinWriterReadsTheFailureSecurityHidesInItsOutput(t *testing.T) {
	cases := []struct {
		name string
		says string
		code int
	}{
		{name: "an item that is already there without -U", says: `security: SecKeychainItemCreateFromContent: The specified item already exists in the keychain.`},
		{name: "a locked keychain", says: `security: SecKeychainUnlock: The user name or passphrase you entered is not correct.`},
		{name: "a session that cannot be asked to unlock it", says: `security: SecKeychainItemCopyContent: User interaction is not allowed.`},
		{name: "a command the tool does not know", says: `security: unknown command "add-generic-passwords".  Type "help" for a list of commands.`},
		{name: "a failure the tool did not describe", says: `security: SecKeychainItemCreateFromContent: something this package has never seen.`},
		{name: "an exit status that does fail", code: 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			record := fakeSecurityOnPath(t, c.says, c.code)
			store := darwinStore(t)
			item := mustItem(t, "fugaro-mailbox-aurora", "aurora")
			if err := store.Put(t.Context(), item, NewKey([]byte(testKey))); !errors.Is(err, ErrBackend) {
				t.Fatalf("Put = %v, want ErrBackend", err)
			}
			// The store reached the tool exactly once: a refusal that never ran
			// the program would pass the assertion above for the wrong reason.
			if runs := fakeSecurityRuns(t, record); len(runs) != 1 {
				t.Errorf("the stand-in security ran %d times, want once", len(runs))
			}
		})
	}
}

// TestSecurityVerdictReadsEveryShapeTheToolPrints pins the classification
// itself, line by line, because it is the one rule the storing side's honesty
// rests on: a successful add prints nothing at all and a successful delete
// prints only its confirmation (after, on some macOS versions, the dump of the
// item it removed), so every other non-blank line is a failure — wherever in
// the line the message sits, since a prompt or an echo of the command can
// precede it — and the only failure that is not one for Delete is the line that
// says the item is already gone.
func TestSecurityVerdictReadsEveryShapeTheToolPrints(t *testing.T) {
	const prompt = "security> "
	cases := []struct {
		name     string
		stdout   string
		stderr   string
		deleting bool
		why      string
		missing  bool
	}{
		{name: "nothing at all"},
		{name: "lines the tool left blank", stderr: "\n \n"},
		{name: "an item that is already there", stderr: `security: SecKeychainItemCreateFromContent: The specified item already exists in the keychain.`, why: "already stored there"},
		{name: "a locked keychain", stderr: `security: SecKeychainUnlock: The user name or passphrase you entered is not correct.`, why: "locked"},
		{name: "a session that cannot be asked to unlock it", stderr: `security: SecKeychainItemCopyContent: User interaction is not allowed.`, why: "cannot ask for it to be unlocked"},
		{name: "a command the tool does not know", stderr: `security: unknown command "add-generic-passwords".  Type "help" for a list of commands.`, why: "does not know the command"},
		{name: "the item is not there", stderr: `security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.`, why: "no such item", missing: true},
		{name: "a failure behind a prompt", stderr: prompt + `security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.`, why: "no such item", missing: true},
		{name: "a message this package does not know", stderr: `security: SecKeychainItemCreateFromContent: something else entirely.`, why: "did not describe"},
		// The fail-closed cases: a line the tool wrote and this package cannot
		// place is a failure, because the alternative is reading a store that
		// did not happen as one that did. A prompt on a line of its own, and an
		// echo of the command on a line of its own — which is a shape
		// `security -i` does not produce over a pipe, since there is no terminal
		// to echo to, and which this reads as the failure it cannot tell it
		// apart from.
		{name: "a line the tool did not explain", stdout: prompt, why: "did not describe"},
		{name: "an echo of the command on a line of its own", stdout: `add-generic-password -U -s "fugaro-mailbox-aurora" -w <key>`, why: "did not describe"},
		{name: "an echo of the command before a failure", stdout: `add-generic-password -U -s "fugaro-mailbox-aurora" -w <key>` + "\n" + `security: SecKeychainItemCreateFromContent: The specified item already exists in the keychain.`, why: "already stored there"},
		{name: "a line the tool did not explain before the item being gone", stdout: prompt, stderr: `security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.`, deleting: true, why: "did not describe"},

		// What a delete that worked prints: the confirmation alone, or after
		// the dump of the item it removed, on either stream.
		{name: "a delete that worked", stdout: securityDeleted, deleting: true},
		{name: "a delete that worked, after the item it removed", stdout: securityDeletedWithItem, deleting: true},
		{name: "a delete that worked, its dump and its confirmation on two streams", stdout: strings.TrimSuffix(securityDeletedWithItem, securityDeleted), stderr: securityDeleted, deleting: true},
		{name: "a delete that worked, in CRLF", stdout: strings.ReplaceAll(securityDeletedWithItem, "\n", "\r\n"), deleting: true},

		// What a delete that worked prints is not a store that worked.
		{name: "a store answered with a delete's confirmation", stdout: securityDeleted, why: "did not describe"},
		{name: "a store answered with an item dump", stdout: securityDeletedWithItem, why: "did not describe"},
		{name: "a store answered with an item dump alone", stdout: strings.TrimSuffix(securityDeletedWithItem, securityDeleted), why: "did not describe"},

		// The confirmation is a whole line, the dump is accepted only with it,
		// and neither excuses a failure printed next to it.
		{name: "an item dump without the confirmation", stdout: strings.TrimSuffix(securityDeletedWithItem, securityDeleted), deleting: true, why: "without saying that it deleted it"},
		{name: "the confirmation inside another line", stdout: "security: " + securityDeleted, deleting: true, why: "did not describe"},
		{name: "the confirmation without its full stop", stdout: strings.TrimSuffix(securityDeleted, "."), deleting: true, why: "did not describe"},
		{name: "a dump line that is not one", stdout: "attributes: " + testKey + "\n" + securityDeleted, deleting: true, why: "did not describe"},
		{name: "a failure after the confirmation", stdout: securityDeletedWithItem, stderr: `security: SecKeychainItemDelete: User interaction is not allowed.`, deleting: true, why: "cannot ask for it to be unlocked"},
		{name: "a failure before the confirmation", stdout: `security: SecKeychainSearchCopyNext: The user name or passphrase you entered is not correct.` + "\n" + securityDeleted, deleting: true, why: "passphrase was not accepted"},
		{name: "a message this package does not know after the confirmation", stdout: securityDeletedWithItem + "\n" + `security: SecKeychainItemDelete: something else entirely.`, deleting: true, why: "did not describe"},

		// The item being gone is the item-not-found sentence only.
		{name: "the item is not there, on a delete", stderr: `security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.`, deleting: true, why: "no such item", missing: true},
		{name: "the keychain itself is not there", stderr: `security: SecKeychainSearchCopyNext: The specified keychain could not be found.`, deleting: true, why: "keychain the security tool was asked to use could not be found"},
		{name: "something else that could not be found", stderr: `security: SecKeychainItemDelete: The specified attribute could not be found.`, deleting: true, why: "did not describe"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := securityVerdict([]byte(c.stdout), []byte(c.stderr), c.deleting)
			if c.why == "" {
				if got.why != "" || got.missing {
					t.Fatalf("securityVerdict(%q, %q) = %+v, want no failure", c.stdout, c.stderr, got)
				}
				return
			}
			if !strings.Contains(got.why, c.why) {
				t.Errorf("securityVerdict(%q, %q) = %q, want it to mention %q", c.stdout, c.stderr, got.why, c.why)
			}
			if got.missing != c.missing {
				t.Errorf("securityVerdict(%q, %q) missing = %v, want %v", c.stdout, c.stderr, got.missing, c.missing)
			}
		})
	}
}

// TestDarwinWriterDeletesOnlyWhatIsGone is the deleting half of the same
// reading, and the one asymmetry in it. A delete whose item is not there is not
// a failure — that is the state the caller asked for, and both callers reach it
// that way: the rollback of a delivery that could not be written, and `pair
// revoke` run twice — while every other failure the tool reports without a
// failing exit status is ErrBackend. The tool's own errSecItemNotFound (44) is
// the same answer arriving on the exit-status path, and is read here too.
func TestDarwinWriterDeletesOnlyWhatIsGone(t *testing.T) {
	cases := []struct {
		name    string
		says    string
		code    int
		wantErr bool
	}{
		{name: "the tool says the item is not there", says: `security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.`},
		{name: "the tool answers errSecItemNotFound", code: 44},
		{name: "the tool says the password was deleted", says: securityDeleted},
		{name: "the tool describes the item it deleted", says: securityDeletedWithItem},
		{name: "the tool says the keychain itself is not there", says: `security: SecKeychainSearchCopyNext: The specified keychain could not be found.`, wantErr: true},
		{name: "the tool describes an item and does not say it deleted it", says: strings.TrimSuffix(securityDeletedWithItem, securityDeleted), wantErr: true},
		{name: "the tool says it deleted the password and then fails", says: securityDeletedWithItem + "\n" + `security: SecKeychainItemDelete: User interaction is not allowed.`, wantErr: true},
		{name: "the tool says the keychain is locked", says: `security: SecKeychainUnlock: The user name or passphrase you entered is not correct.`, wantErr: true},
		{name: "the tool does not know the command", says: `security: unknown command "delete-generic-passwords".  Type "help" for a list of commands.`, wantErr: true},
		{name: "the tool exits with a failure", code: 2, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			record := fakeSecurityOnPath(t, c.says, c.code)
			store := darwinStore(t)
			item := mustItem(t, "fugaro-mailbox-aurora", "aurora")
			err := store.Delete(t.Context(), item)
			switch {
			case c.wantErr && !errors.Is(err, ErrBackend):
				t.Fatalf("Delete = %v, want ErrBackend", err)
			case !c.wantErr && err != nil:
				t.Fatalf("Delete = %v, want nil: the item is not there, which is the state the caller asked for", err)
			}
			if runs := fakeSecurityRuns(t, record); len(runs) != 1 {
				t.Errorf("the stand-in security ran %d times, want once", len(runs))
			}
		})
	}
}

// securityDeleted is the line `security delete-generic-password` prints when it
// removed the item, and securityDeletedWithItem is the same answer preceded by
// the attribute dump the tool prints of the item it removed: the keychain's
// path, the record's version and class, and one line per attribute.
const (
	securityDeleted         = "password has been deleted."
	securityDeletedWithItem = `keychain: "/Users/operator/Library/Keychains/login.keychain-db"
version: 512
class: "genp"
attributes:
    0x00000007 <blob>="fugaro-mailbox-aurora"
    0x00000008 <blob>=<NULL>
    "acct"<blob>="fugaro-mailbox-aurora"
    "cdat"<timedate>=0x32303236313033303132303030305A00  "20261030120000Z\000"
    "crtr"<uint32>=<NULL>
    "cusi"<sint32>=<NULL>
    "desc"<blob>=<NULL>
    "gena"<blob>=<NULL>
    "icmt"<blob>=<NULL>
    "invi"<sint32>=<NULL>
    "mdat"<timedate>=0x32303236313033303132303030305A00  "20261030120000Z\000"
    "nega"<sint32>=<NULL>
    "prot"<blob>=<NULL>
    "scrp"<sint32>=<NULL>
    "svce"<blob>="fugaro-mailbox-aurora"
    "type"<uint32>=<NULL>
` + securityDeleted
)

// TestSecurityUnknownLineIsShownRedacted: a line the verdict cannot place is
// quoted in the error, so the operator's terminal says what the real tool
// printed, and every key-length run of the key's alphabet in it is redacted, so
// a tool that echoed the command line it was given does not put the key there.
func TestSecurityUnknownLineIsShownRedacted(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
		not  []string
	}{
		{name: "a message", line: `security: SecKeychainItemDelete: something new.`, want: `"security: [REDACTED]: something new."`},
		{name: "an echoed command line", line: `add-generic-password -U -s "fugaro-mailbox-aurora" -w ` + testKey, want: `-w [REDACTED]"`, not: []string{testKey, testKey[:19], testKey[len(testKey)-19:]}},
		{name: "a short word is kept", line: `version: 512 class genp`, want: `"version: 512 class genp"`},
		{name: "a run one short of the threshold is kept", line: strings.Repeat("a", 19), want: strings.Repeat("a", 19)},
		{name: "a run at the threshold is redacted", line: "x " + strings.Repeat("a", 20) + " y", want: `"x [REDACTED] y"`},
		{name: "a control character is escaped", line: "bell\a", want: `"bell\a"`},
		{name: "a long line is cut", line: strings.Repeat("word ", 100), want: `..."`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := securityShown(c.line)
			if !strings.Contains(got, c.want) {
				t.Errorf("securityShown(%q) = %s, want it to contain %s", c.line, got, c.want)
			}
			for _, part := range c.not {
				if strings.Contains(got, part) {
					t.Errorf("securityShown(%q) = %s carries %q", c.line, got, part)
				}
			}
			if len(got) > securityShownMax+len(`"..."`) {
				t.Errorf("securityShown answered %d bytes, more than %d", len(got), securityShownMax)
			}
		})
	}
	v := securityVerdict([]byte("security: SecKeychainItemDelete: something new.\n"), nil, true)
	if !strings.Contains(v.why, securityUnknownWhy) || !strings.Contains(v.why, "something new.") {
		t.Errorf("the unknown-line verdict = %q, want the sentence and the line", v.why)
	}
}

// TestDarwinWriterErrorsNeverCarryTheKey is M5 on the write path, through the
// same real child process: whatever the tool printed, the error the operator
// sees carries no part of the key, so none reaches a terminal, a log line or a
// bug report. The command line the tool was given does hold the key, which is
// why a line of the tool's own is only ever quoted redacted — and a key too
// short for the redaction's run length is removed by name.
func TestDarwinWriterErrorsNeverCarryTheKey(t *testing.T) {
	item := mustItem(t, "fugaro-mailbox-aurora", "aurora")
	const shortKey = "short-key-01"
	for _, c := range []struct {
		name string
		key  string
		says string
		code int
	}{
		{name: "the tool reported a failure", key: testKey, says: `security: SecKeychainItemCreateFromContent: The specified item already exists in the keychain.`},
		{name: "the tool echoed its command line", key: testKey, says: `add-generic-password -U -s "fugaro-mailbox-aurora" -a "fugaro-mailbox-aurora" -w ` + testKey},
		{name: "the tool echoed a key too short to look like one", key: shortKey, says: `add-generic-password -U -s "fugaro-mailbox-aurora" -a "fugaro-mailbox-aurora" -w ` + shortKey},
		{name: "the tool failed", key: testKey, code: 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeSecurityOnPath(t, c.says, c.code)
			store := darwinStore(t)
			err := store.Put(t.Context(), item, NewKey([]byte(c.key)))
			if !errors.Is(err, ErrBackend) {
				t.Fatalf("Put = %v, want ErrBackend", err)
			}
			for _, part := range []string{c.key, c.key[:8], c.key[8:]} {
				if strings.Contains(err.Error(), part) {
					t.Errorf("the error carries the key: %v", err)
				}
			}
		})
	}
}

// TestDarwinWriterKeyNeverInArgvOrEnv is M11 where it can actually be observed:
// the argv and the environment of the child process the writer runs. Nothing
// this package does passes the key in either — a process's argv and environment
// are readable by every process on the machine — and the key reaches the tool
// only on the standard input it was given.
func TestDarwinWriterKeyNeverInArgvOrEnv(t *testing.T) {
	record := fakeSecurityOnPath(t, "", 0)
	store := darwinStore(t)
	item := mustItem(t, "fugaro-mailbox-aurora", "aurora")
	if err := store.Put(t.Context(), item, NewKey([]byte(testKey))); err != nil {
		t.Fatalf("Put = %v", err)
	}
	if err := store.Delete(t.Context(), item); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	runs := fakeSecurityRuns(t, record)
	if len(runs) != 2 {
		t.Fatalf("the stand-in security ran %d times, want the store and the delete", len(runs))
	}
	for _, run := range runs {
		if len(run.Argv) != 2 || run.Argv[1] != "-i" {
			t.Errorf("the program ran as %q, want its own name and -i", run.Argv)
		}
		for _, arg := range run.Argv {
			if strings.Contains(arg, testKey) {
				t.Errorf("the key is an argv element: %q", run.Argv)
			}
		}
		for _, env := range run.Env {
			if strings.Contains(env, testKey) {
				t.Errorf("the key is in the environment: %q", env)
			}
		}
	}
	if !strings.Contains(runs[0].Stdin, testKey) {
		t.Errorf("the store handed the program no key at all: %q", runs[0].Stdin)
	}
	if strings.Contains(runs[1].Stdin, testKey) {
		t.Errorf("the delete handed the program a key: %q", runs[1].Stdin)
	}
}

// TestPlatformsWithNoWriterRefuse: darwin stores now, and the platforms this
// build has no storing program for still answer ErrBackend rather than storing
// a key somewhere else. The sentence has to be true of the platform — a
// platform whose tool has an argv-safe form this build simply has not written
// is not the same refusal as one whose tool has none — so the reason names the
// platform and the argv rule (M11).
func TestPlatformsWithNoWriterRefuse(t *testing.T) {
	if w, why := writerFor("darwin"); w == nil {
		t.Fatalf("darwin has no storing program, so `fugaro pair` cannot complete on the platform this CLI runs on: %s", why)
	}
	item := mustItem(t, "fugaro-mailbox-aurora", "aurora")
	for _, goos := range []string{"windows", "plan9", "js", ""} {
		t.Run("goos="+goos, func(t *testing.T) {
			w, why := writerFor(goos)
			if w != nil {
				t.Fatalf("writerFor(%q) answered a storing program: %+v", goos, w)
			}
			for _, want := range []string{"argv", "M11"} {
				if !strings.Contains(why, want) {
					t.Errorf("the refusal does not say %q, so the operator is sent to fix the wrong thing: %s", want, why)
				}
			}
			s := &commandStore{writer: w, noStore: why, run: func(context.Context, string, io.Reader, ...string) ([]byte, []byte, int, error) {
				t.Error("a store with no writer ran a program")
				return nil, nil, 0, nil
			}}
			if err := s.Put(t.Context(), item, NewKey([]byte(testKey))); !errors.Is(err, ErrBackend) {
				t.Errorf("Put on %q = %v, want ErrBackend", goos, err)
			}
			if err := s.Delete(t.Context(), item); !errors.Is(err, ErrBackend) {
				t.Errorf("Delete on %q = %v, want ErrBackend", goos, err)
			}
		})
	}
}

// The stand-in `security` the darwin tests run: this test binary under that
// name, first on PATH. A program reached through PATH is a real child process,
// which is the only place where M11's claims — no argv element and no
// environment variable carries the key — can be observed at all, and the only
// place where a failure the tool reports in its output rather than in its exit
// status can be exercised.
const (
	fakeSecurityRecord = "FUGARO_KEYCHAIN_TEST_SECURITY_RECORD"
	fakeSecuritySays   = "FUGARO_KEYCHAIN_TEST_SECURITY_SAYS"
	fakeSecurityCode   = "FUGARO_KEYCHAIN_TEST_SECURITY_CODE"
)

// TestMain is what lets the test binary be the stand-in: the writer runs it with
// the argv it builds for `security` (`-i`), which the testing package's own flag
// parsing would refuse, so the check has to come before m.Run().
func TestMain(m *testing.M) {
	if os.Getenv(fakeSecurityRecord) != "" {
		fakeSecurity()
		return
	}
	os.Exit(m.Run())
}

// fakeSecurityRun is one run of the stand-in, as it recorded it.
type fakeSecurityRun struct {
	Argv  []string `json:"argv"`
	Stdin string   `json:"stdin"`
	Env   []string `json:"env"`
}

// fakeSecurity is the stand-in's whole behaviour: it writes down what it was
// given — its argv, the command line it read, its environment — and then answers
// the shape the test asked for, which is the text on its standard error and the
// exit status. The real tool's usual answer to a command it could not carry out
// is a message and a zero status, which is the case the writer has to read.
func fakeSecurity() {
	stdin, _ := io.ReadAll(os.Stdin)
	record := fakeSecurityRun{Argv: os.Args, Stdin: string(stdin), Env: os.Environ()}
	if f, err := os.OpenFile(os.Getenv(fakeSecurityRecord), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_ = json.NewEncoder(f).Encode(record)
		_ = f.Close()
	}
	if says := os.Getenv(fakeSecuritySays); says != "" {
		fmt.Fprintln(os.Stderr, says)
	}
	code, _ := strconv.Atoi(os.Getenv(fakeSecurityCode))
	os.Exit(code)
}

// fakeSecurityOnPath puts the stand-in first on PATH and returns the file it
// records into. PATH holds nothing else, so a test that uses it cannot reach a
// real keychain even by accident (M5).
func fakeSecurityOnPath(t *testing.T, says string, code int) string {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if err := os.Symlink(self, filepath.Join(dir, "security")); err != nil {
		t.Fatalf("linking the test binary as security: %v", err)
	}
	record := filepath.Join(dir, "security.json")
	t.Setenv("PATH", dir)
	t.Setenv(fakeSecurityRecord, record)
	t.Setenv(fakeSecuritySays, says)
	t.Setenv(fakeSecurityCode, strconv.Itoa(code))
	return record
}

// fakeSecurityRuns is what the stand-in recorded, one entry per run.
func fakeSecurityRuns(t *testing.T, record string) []fakeSecurityRun {
	t.Helper()
	f, err := os.Open(record)
	if err != nil {
		t.Fatalf("the stand-in security recorded nothing: %v", err)
	}
	defer f.Close()
	var runs []fakeSecurityRun
	dec := json.NewDecoder(f)
	for {
		var run fakeSecurityRun
		switch err := dec.Decode(&run); {
		case errors.Is(err, io.EOF):
			return runs
		case err != nil:
			t.Fatalf("reading what the stand-in security recorded: %v", err)
		}
		runs = append(runs, run)
	}
}

// securityWords is what `security -i`'s line parser makes of one command line:
// whitespace separates words and a double-quoted run is one word. It is the
// reader's half of the round trip above, so a command line the tool would read
// as something else — a key split in two, an item name that ends the word it is
// in — fails there rather than on an operator's Mac.
func securityWords(command string) []string {
	var words []string
	var word strings.Builder
	quoted := false
	for _, r := range strings.TrimSuffix(command, "\n") {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
		default:
			word.WriteRune(r)
		}
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}

// darwinWriter is writerFor("darwin") with the test failing where the operator
// would be refused: every darwin test starts from it, and a nil writer is the
// one state none of them can say anything about.
func darwinWriter(t *testing.T) *writer {
	t.Helper()
	w, why := writerFor("darwin")
	if w == nil {
		t.Fatalf("writerFor(darwin) answered no storing program, so a pairing key has no argv-safe way into the darwin keychain: %s", why)
	}
	return w
}

// darwinStore is the darwin Store as System() would build it on a Mac, run
// through the real runner. The tests that need a real child process — the
// stand-in security, and the argv and the environment — drive this rather than
// System(), which on this runner is the linux store.
func darwinStore(t *testing.T) *commandStore {
	t.Helper()
	r, err := readerFor("darwin")
	if err != nil {
		t.Fatalf("darwin has no reader: %v", err)
	}
	w := darwinWriter(t)
	return &commandStore{reader: r, writer: w, run: runProcgroup}
}

// mustItem is ParseItem with the test failing on a name the table above says is
// one: every test that needs an item has the same reason to stop.
func mustItem(t *testing.T, name, project string) Item {
	t.Helper()
	item, err := ParseItem(name, project)
	if err != nil {
		t.Fatalf("ParseItem(%q, %q): %v", name, project, err)
	}
	return item
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
			s := &commandStore{reader: r, run: func(context.Context, string, io.Reader, ...string) ([]byte, []byte, int, error) {
				return []byte(c.out), nil, c.code, c.err
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
			name: "a program that could not be run",
			store: &commandStore{reader: r, run: func(context.Context, string, io.Reader, ...string) ([]byte, []byte, int, error) {
				return nil, nil, -1, errMissing
			}},
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
// reaches no error the operator sees. The stream is kept — the darwin storing
// side reads it for the failure `security -i` does not report in its exit
// status — and the read side drops it unread, because a failing tool's own text
// could echo what it was asked for and an error string is exactly where a key
// must never appear.
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
		run: func(ctx context.Context, name string, stdin io.Reader, args ...string) ([]byte, []byte, int, error) {
			// The helper needs its variable set; runProcgroup is what the
			// real Store uses, so this test drives that, not a stand-in.
			t.Setenv(env, "1")
			return runProcgroup(ctx, name, stdin, args...)
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

// TestPutAndDeleteAreTheStoringHalf drives the write path through the same
// seam the read path is driven through: the program's argv, and the key on
// standard input rather than in an argument. What the storing argv is worth
// pinning is that the key is in none of it (M11) and that the item's name is
// (M5: the name is not a secret and the value is).
func TestPutAndDeleteAreTheStoringHalf(t *testing.T) {
	item, err := ParseItem("fugaro-mailbox-aurora", "aurora")
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	w, why := writerFor("linux")
	if w == nil {
		t.Fatalf("linux has no storing program: %s", why)
	}
	var gotStdin []byte
	var gotArgs []string
	code := 0
	s := &commandStore{writer: w, run: func(_ context.Context, name string, stdin io.Reader, args ...string) ([]byte, []byte, int, error) {
		gotArgs = append([]string{name}, args...)
		if stdin != nil {
			gotStdin, _ = io.ReadAll(stdin)
		}
		return nil, nil, code, nil
	}}
	if err := s.Put(context.Background(), item, NewKey([]byte(testKey))); err != nil {
		t.Fatalf("Put = %v", err)
	}
	if string(gotStdin) != testKey {
		t.Errorf("the storing program read %q on stdin, want the key", gotStdin)
	}
	for _, arg := range gotArgs {
		if strings.Contains(arg, testKey) {
			t.Errorf("the key is an argv element: %v", gotArgs)
		}
	}
	if !slices.Contains(gotArgs, item.Name()) {
		t.Errorf("the storing argv does not name the item: %v", gotArgs)
	}

	// A storing program that failed is ErrBackend, and it never echoes what it
	// was asked for.
	code = 2
	err = s.Put(context.Background(), item, NewKey([]byte(testKey)))
	if !errors.Is(err, ErrBackend) {
		t.Fatalf("a failed store = %v, want ErrBackend", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Errorf("the failure carries the key: %v", err)
	}

	// An item that is not there is the state Delete asked for, so the program's
	// own "no such item" status is not an error; another status is.
	code = w.notFound
	if err := s.Delete(context.Background(), item); err != nil {
		t.Errorf("deleting an item that is not there = %v, want nil", err)
	}
	code = 3
	if err := s.Delete(context.Background(), item); !errors.Is(err, ErrBackend) {
		t.Errorf("a failed delete = %v, want ErrBackend", err)
	}

	// A platform with no storing program answers ErrBackend naming it, and
	// never stores a key somewhere else.
	none := &commandStore{noStore: "this platform has no storing program"}
	if err := none.Put(context.Background(), item, NewKey([]byte(testKey))); !errors.Is(err, ErrBackend) {
		t.Errorf("a store with no writer = %v, want ErrBackend", err)
	}
	if err := none.Delete(context.Background(), item); !errors.Is(err, ErrBackend) {
		t.Errorf("a delete with no writer = %v, want ErrBackend", err)
	}
}

// TestTheFakeStoresWhatTheRealOneWould: a test that pairs through the Fake must
// not pass on a value the operator's keychain would refuse to read back, so the
// fake's Put and the real read agree on the shape.
func TestTheFakeStoresWhatTheRealOneWould(t *testing.T) {
	item, err := ParseItem("fugaro-mailbox-aurora", "aurora")
	if err != nil {
		t.Fatalf("ParseItem: %v", err)
	}
	ctx := context.Background()
	f := &Fake{}
	if err := f.Put(ctx, item, NewKey([]byte(testKey))); err != nil {
		t.Fatalf("Put = %v", err)
	}
	key, err := f.Key(ctx, item)
	if err != nil {
		t.Fatalf("Key = %v", err)
	}
	if got := string(key.Bytes()); got != testKey {
		t.Errorf("Key = %q, want %q", got, testKey)
	}
	if err := f.Delete(ctx, item); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	if _, err := f.Key(ctx, item); !errors.Is(err, ErrNoKey) {
		t.Errorf("Key after Delete = %v, want ErrNoKey", err)
	}
	// A broken backend refuses the writes too, and writes nothing.
	f = &Fake{Backend: ErrBackend}
	if err := f.Put(ctx, item, NewKey([]byte(testKey))); !errors.Is(err, ErrBackend) {
		t.Errorf("Put on a broken backend = %v, want ErrBackend", err)
	}
	if len(f.Keys) != 0 {
		t.Errorf("a broken backend stored %v", f.Keys)
	}
}
