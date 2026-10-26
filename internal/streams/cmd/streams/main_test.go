package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/streams"
)

// catalogue is the fixture this package's tests read and write: the real file's
// shape -- the cli stream at 0.11.1 "Camel one" with Dolphin next, so a version
// one minor on (0.12.0) spends Dolphin and moves `next` to Gecko -- with the
// comments the real file carries, because replaceStream has to leave them alone.
const catalogue = `# The release streams catalogue.
streams:
  - name: cli
    theme: animals
    # Badger is 0.10, Camel 0.11, and one name per minor after that.
    names: [Badger, Camel, Dolphin, Gecko, Hippo, Koala, Moose]
    current: "0.11.1"
    next: Dolphin
    tag_prefix: v

  - name: console
    theme: fruits
    names: [Cherry, Mango, Melon]
    current: "0.111.0"
    next: Melon
    tag_prefix: console-v

  - name: mobile
    theme: birds
    names: [Falcon, Pelican, Robin]
    current: "0.0.0"
    next: Falcon
    tag_prefix: mobile-v
`

// cliStream is that fixture's cli entry, for the helpers that take a Stream.
func cliStream(t *testing.T) streams.Stream {
	t.Helper()
	c, err := streams.Parse(cataloguePath, []byte(catalogue))
	if err != nil {
		t.Fatalf("the fixture is a catalogue, and it did not parse: %v", err)
	}
	s, ok := c.Lookup(streams.CLI)
	if !ok {
		t.Fatal("the fixture names no cli stream")
	}
	return s
}

// inCatalogue runs the command from a fresh directory holding the fixture, so
// every case reads and writes a file of its own, and returns that directory's
// path for a test that then reads the file back.
func inCatalogue(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, cataloguePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return path
}

// runStreams runs the command with args and returns what it printed.
func runStreams(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

// TestSpokenName: a release is spoken with the name of the minor it falls in and
// the patch's word. The name of a minor is the catalogue's `next` moved on by
// however many minors the version is past the stream's current, so a version one
// minor past it is spoken with `next` itself and the one after that with the
// name following `next` -- the arithmetic the file's `next` and `current` are
// two halves of, and the reason `set` moves both (TestSetMovesNext).
func TestSpokenName(t *testing.T) {
	s := cliStream(t)
	for _, tc := range []struct{ version, want string }{
		{"0.11.1", "Camel one"},
		{"0.11.2", "Camel two"},
		{"0.11.10", "Camel 10"},
		{"0.12.0", "Dolphin"},
		{"0.12.1", "Dolphin one"},
		{"0.13.0", "Gecko"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			v, err := parseVersion(tc.version)
			if err != nil {
				t.Fatalf("%s is a version: %v", tc.version, err)
			}
			got, err := spokenName(s, v)
			if err != nil {
				t.Fatalf("spokenName(%s) was refused: %v", tc.version, err)
			}
			if got != tc.want {
				t.Errorf("spokenName(%s) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}

// TestSpokenNameRefused: a version the catalogue does not describe is refused
// rather than guessed at. A major bump and a minor below the named line (the
// CLI's starts at 0.10) are off the line the names describe, and a minor past
// the last name has none.
func TestSpokenNameRefused(t *testing.T) {
	s := cliStream(t)
	for _, tc := range []struct{ version, want string }{
		{"1.0.0", "is not on the line"},
		{"0.9.0", "is not on the line"},
		{"0.0.1", "is not on the line"},
		{"0.17.0", "past the last name"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			v, err := parseVersion(tc.version)
			if err != nil {
				t.Fatal(err)
			}
			got, err := spokenName(s, v)
			if err == nil {
				t.Fatalf("spokenName(%s) = %q, want a refusal", tc.version, got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to carry %q", err, tc.want)
			}
		})
	}

	// The name is counted from the stream's own first version, by the reader's
	// rule, not from where the stream is now: a stream that has shipped nothing
	// still names its first release, and a version below its line has no name.
	t.Run("a stream that has released nothing", func(t *testing.T) {
		c, err := streams.Parse(cataloguePath, []byte(catalogue))
		if err != nil {
			t.Fatal(err)
		}
		mobile, _ := c.Lookup(streams.Mobile)
		for version, want := range map[string]string{"0.200.0": "Falcon", "0.201.2": "Pelican two"} {
			v, err := parseVersion(version)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := spokenName(mobile, v); err != nil || got != want {
				t.Errorf("spokenName(mobile, %s) = %q, %v, want %q", version, got, err, want)
			}
		}
		v, err := parseVersion("0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		if got, err := spokenName(mobile, v); err == nil || !strings.Contains(err.Error(), "is not on the line") {
			t.Errorf("spokenName(mobile, 0.1.0) = %q, %v, want a refusal: the app's line starts at 0.200", got, err)
		}
	})
}

// TestGet: get prints the stream's fields one key=value per line, which is what
// the release script reads its floor out of.
func TestGet(t *testing.T) {
	inCatalogue(t, catalogue)
	out, err := runStreams(t, "get", "cli")
	if err != nil {
		t.Fatalf("get cli: %v", err)
	}
	want := "name=cli\ntheme=animals\nnames=Badger,Camel,Dolphin,Gecko,Hippo,Koala,Moose\ncurrent=0.11.1\nnext=Dolphin\ntag_prefix=v\n"
	if out != want {
		t.Errorf("get cli printed:\n%s\nwant:\n%s", out, want)
	}
}

// TestSetMovesNext: `set` writes the released version as the stream's current
// and moves `next` with it when the version opens a new minor. A patch stays
// inside the current minor, so `next` does not move; the release that opens the
// next minor spends the name `next` holds, so `next` moves on to the one after
// it. Without that second half the file would say current 0.12.0 with next
// Dolphin -- the name 0.12.0 itself carries -- and spokenName would speak 0.12.1
// "Camel one" and 0.13.0 "Dolphin".
func TestSetMovesNext(t *testing.T) {
	for _, tc := range []struct{ version, current, next string }{
		{"0.11.2", `current: "0.11.2"`, "next: Dolphin"},
		{"0.12.0", `current: "0.12.0"`, "next: Gecko"},
		{"0.13.0", `current: "0.13.0"`, "next: Hippo"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			path := inCatalogue(t, catalogue)
			if _, err := runStreams(t, "set", "cli", tc.version); err != nil {
				t.Fatalf("set cli %s: %v", tc.version, err)
			}
			body := read(t, path)
			if !strings.Contains(body, tc.current) {
				t.Errorf("the file does not carry %q:\n%s", tc.current, body)
			}
			if !strings.Contains(body, tc.next) {
				t.Errorf("the file does not carry %q:\n%s", tc.next, body)
			}
		})
	}

	t.Run("the file reads back", func(t *testing.T) {
		path := inCatalogue(t, catalogue)
		if _, err := runStreams(t, "set", "cli", "0.12.0"); err != nil {
			t.Fatal(err)
		}
		c, err := streams.Read(path)
		if err != nil {
			t.Fatalf("the written file is not a catalogue this build reads: %v", err)
		}
		cli, _ := c.Lookup(streams.CLI)
		v, err := parseVersion("0.12.1")
		if err != nil {
			t.Fatal(err)
		}
		if name, err := spokenName(cli, v); err != nil || name != "Dolphin one" {
			t.Errorf("after set 0.12.0, spokenName(0.12.1) = %q, %v, want %q: the file the release commits has to name the release after it", name, err, "Dolphin one")
		}
	})

	t.Run("the comments and the other streams", func(t *testing.T) {
		path := inCatalogue(t, catalogue)
		if _, err := runStreams(t, "set", "cli", "0.12.0"); err != nil {
			t.Fatal(err)
		}
		body := read(t, path)
		for _, kept := range []string{
			"# The release streams catalogue.",
			"    # Badger is 0.10, Camel 0.11, and one name per minor after that.",
			`current: "0.111.0"`,
			`current: "0.0.0"`,
			"next: Melon",
			"next: Falcon",
		} {
			if !strings.Contains(body, kept) {
				t.Errorf("the write dropped %q:\n%s", kept, body)
			}
		}
	})

	// A version the reader refuses as a stream's current -- another major, or a
	// minor below the named line -- is refused before anything is written: the
	// catalogue cannot record it, and a write that tried would fail its own
	// re-read after the release had already made its branch.
	// The app's stream is at 0.0.0, so no floor stands in front of its
	// 0.199.0: the line is the only refusal there.
	for _, tc := range [][2]string{{"cli", "1.0.0"}, {"mobile", "0.199.0"}, {"mobile", "1.200.0"}} {
		name, version := tc[0], tc[1]
		t.Run("a version off the stream's line: "+name+" "+version, func(t *testing.T) {
			path := inCatalogue(t, catalogue)
			_, err := runStreams(t, "set", name, version)
			if err == nil || !strings.Contains(err.Error(), "is not on the line") {
				t.Fatalf("set %s %s = %v, want a refusal naming the line", name, version, err)
			}
			if body := read(t, path); body != catalogue {
				t.Errorf("a refused write changed the file:\n%s", body)
			}
		})
	}

	// The file is the floor the next release reads, so a version below the
	// stream's current is refused, and the version it is already at is not
	// (an interrupted release re-runs with it).
	t.Run("a version below current", func(t *testing.T) {
		path := inCatalogue(t, catalogue)
		_, err := runStreams(t, "set", "cli", "0.10.4")
		if err == nil || !strings.Contains(err.Error(), "below the cli stream's current 0.11.1") {
			t.Fatalf("set cli 0.10.4 = %v, want a refusal: it would lower the floor", err)
		}
		if body := read(t, path); body != catalogue {
			t.Errorf("a refused write changed the file:\n%s", body)
		}
		if _, err := runStreams(t, "set", "cli", "0.11.1"); err != nil {
			t.Errorf("set cli 0.11.1 (the version the stream is at) = %v, want it allowed", err)
		}
		if body := read(t, path); body != catalogue {
			t.Errorf("setting the version the stream is at changed the file:\n%s", body)
		}
	})

	t.Run("a version past the last name", func(t *testing.T) {
		path := inCatalogue(t, catalogue)
		_, err := runStreams(t, "set", "cli", "0.16.0")
		if err == nil {
			t.Fatal("set cli 0.16.0 wrote, want a refusal: the stream has no name for the minor after it")
		}
		if !strings.Contains(err.Error(), "no name for the minor after it") {
			t.Errorf("refusal = %q, want it to name the missing name", err)
		}
		if body := read(t, path); body != catalogue {
			t.Errorf("a refused write changed the file:\n%s", body)
		}
	})

	t.Run("a file that is not a catalogue", func(t *testing.T) {
		path := inCatalogue(t, "streams: []\n")
		if _, err := runStreams(t, "set", "cli", "0.12.0"); err == nil {
			t.Fatal("set on a file with no streams wrote, want a refusal")
		}
		if body := read(t, path); body != "streams: []\n" {
			t.Errorf("a refused write changed the file:\n%s", body)
		}
	})
}

// TestSetFirstReleaseSpendsTheFirstName: a stream that has released nothing has
// `next` at the first name, and its first release -- the first minor of its
// named line, 0.200.0 for the app -- spends it, so the minor after that release
// takes the second. The file's own rule is that a non-zero current cannot keep
// the first name as `next`, so this is also what keeps the write readable at
// all.
func TestSetFirstReleaseSpendsTheFirstName(t *testing.T) {
	path := inCatalogue(t, catalogue)
	if _, err := runStreams(t, "set", "mobile", "0.200.0"); err != nil {
		t.Fatalf("set mobile 0.200.0: %v", err)
	}
	body := read(t, path)
	if !strings.Contains(body, `current: "0.200.0"`) || !strings.Contains(body, "next: Pelican") {
		t.Errorf("the mobile stream's first release should have spent Falcon and left Pelican next:\n%s", body)
	}
	if _, err := streams.Read(path); err != nil {
		t.Errorf("the written file is not a catalogue this build reads: %v", err)
	}
}

// TestSetCheck: `set --check` is set's whole computation -- the floor, the next
// name, the line-targeted rewrite and its re-read -- with no write, so the
// release's step 1 can refuse what its step 2 would. It prints the row it would
// write, and it refuses everything set refuses, the file untouched either way.
func TestSetCheck(t *testing.T) {
	t.Run("a release it would write", func(t *testing.T) {
		path := inCatalogue(t, catalogue)
		out, err := runStreams(t, "set", "--check", "cli", "0.12.0")
		if err != nil {
			t.Fatalf("set --check cli 0.12.0: %v", err)
		}
		if out != "current=0.12.0\nnext=Gecko\n" {
			t.Errorf("set --check printed %q, want the row it would write", out)
		}
		if body := read(t, path); body != catalogue {
			t.Errorf("set --check wrote the file:\n%s", body)
		}
	})
	unquoted := strings.Replace(catalogue, `current: "0.11.1"`, "current: 0.11.1", 1)
	for _, tc := range []struct {
		name, body, version, want string
	}{
		// The reader accepts an unquoted 0.11.1 (it is a string in every YAML
		// schema), and set's line-targeted rewrite only finds a quoted one.
		{"an unquoted current", unquoted, "0.11.2", "no current version"},
		{"no name left after it", catalogue, "0.16.0", "no name for the minor after it"},
		{"a version the reader refuses", catalogue, "1.0.0", "is not on the line"},
		{"a version below current", catalogue, "0.11.0", "below the cli stream's current"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := inCatalogue(t, tc.body)
			if _, err := runStreams(t, "set", "--check", "cli", tc.version); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("set --check cli %s = %v, want a refusal carrying %q", tc.version, err, tc.want)
			}
			if body := read(t, path); body != tc.body {
				t.Errorf("set --check changed the file:\n%s", body)
			}
		})
	}
}

// TestReplaceStream: the rewrite is line-targeted, so a stream's block is the
// only thing that moves. A file missing either line is refused rather than
// half-written, and the block is found by its own `- name:` line, so a stream
// whose name is a prefix of another is not the one that gets written.
func TestReplaceStream(t *testing.T) {
	v := streams.Version{Major: 0, Minor: 12, Patch: 0}

	t.Run("a missing next line", func(t *testing.T) {
		body := strings.Replace(catalogue, "    next: Dolphin\n", "", 1)
		_, err := replaceStream([]byte(body), streams.CLI, v, "Gecko")
		if err == nil || !strings.Contains(err.Error(), "no next name") {
			t.Errorf("refusal = %v, want the missing next line named", err)
		}
	})
	t.Run("a missing current line", func(t *testing.T) {
		body := strings.Replace(catalogue, `    current: "0.11.1"`+"\n", "", 1)
		_, err := replaceStream([]byte(body), streams.CLI, v, "Gecko")
		if err == nil || !strings.Contains(err.Error(), "no current version") {
			t.Errorf("refusal = %v, want the missing current line named", err)
		}
	})
	t.Run("a stream the file does not carry", func(t *testing.T) {
		_, err := replaceStream([]byte(catalogue), "desktop", v, "Gecko")
		if err == nil {
			t.Error("rewrote a stream the file does not name, want a refusal")
		}
	})
	t.Run("the last line unterminated", func(t *testing.T) {
		body := strings.TrimSuffix(catalogue, "\n")
		updated, err := replaceStream([]byte(body), streams.CLI, v, "Gecko")
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(string(updated), "\n") {
			t.Errorf("the rewrite added a newline the file did not have:\n%q", string(updated))
		}
	})
}

// TestRunUsageAndRefusals: the command's own argument handling. Every shape it
// does not know is refused with the usage line, and an unknown stream or a
// version that is not a version is refused by the reader that names it.
func TestRunUsageAndRefusals(t *testing.T) {
	inCatalogue(t, catalogue)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no arguments", nil, "usage: streams"},
		{"an unknown verb", []string{"speak", "cli"}, "usage: streams"},
		{"get without a stream", []string{"get"}, "usage: streams"},
		{"name without a version", []string{"name", "cli"}, "usage: streams"},
		{"set without a version", []string{"set", "cli"}, "usage: streams"},
		{"set --check without a version", []string{"set", "--check", "cli"}, "usage: streams"},
		{"a stream the file does not name", []string{"get", "desktop"}, "names no desktop stream"},
		{"a version that is not one", []string{"name", "cli", "0.12"}, "MAJOR.MINOR.PATCH"},
		{"a version with a v", []string{"set", "cli", "v0.12.0"}, "MAJOR.MINOR.PATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runStreams(t, tc.args...)
			if err == nil {
				t.Fatalf("run(%v) printed %q, want a refusal", tc.args, out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to carry %q", err, tc.want)
			}
		})
	}
}

// read returns the file at path, failing the test rather than returning when it
// cannot be read: every assertion about a write needs it.
func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
