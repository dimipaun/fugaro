package streams

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// realPath is the repository's own file as this package's tests reach it: the
// catalogue is a repository fact, and reading the one that ships is the first
// thing this package is asked for.
const realPath = "../../docs/release-streams.yaml"

// validYAML is a valid catalogue: smaller than the shipped one, but with all
// three streams, because the file names exactly the three. A test that varies
// one field rewrites one line of it (rewrite), so what a refusal is about stays
// visible in the test rather than hidden in the fixture.
const validYAML = `streams:
  - name: cli
    theme: animals
    names: [Badger, Camel, Dolphin]
    current: "0.10.4"
    next: Camel
    tag_prefix: v
  - name: console
    theme: fruits
    names: [Cherry, Mango, Melon]
    current: "0.110.0"
    next: Mango
    tag_prefix: console-v
  - name: mobile
    theme: birds
    names: [Falcon, Pelican]
    current: "0.0.0"
    next: Falcon
    tag_prefix: mobile-v
`

// mobileStream is the fixture's mobile entry, for the rows that take a stream
// away rather than change one of its fields.
const mobileStream = `  - name: mobile
    theme: birds
    names: [Falcon, Pelican]
    current: "0.0.0"
    next: Falcon
    tag_prefix: mobile-v
`

// rewrite returns the fixture with the first old replaced by new, and fails the
// test when the fixture no longer carries old: a row whose replacement silently
// stopped applying would assert a refusal about a file that is valid.
func rewrite(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(validYAML, old) {
		t.Fatalf("the fixture does not carry %q: the row it rewrites has moved", old)
	}
	return strings.Replace(validYAML, old, new, 1)
}

// parseFixture parses one fixture, naming the path every refusal quotes.
func parseFixture(body string) (Catalogue, error) {
	return Parse("release-streams.yaml", []byte(body))
}

// shipped reads the repository's own catalogue, failing the test rather than
// returning when it does not read: every assertion below it would be vacuous.
func shipped(t *testing.T) Catalogue {
	t.Helper()
	c, err := Read(realPath)
	if err != nil {
		t.Fatalf("%s is the repository's own catalogue, and it does not read: %v", realPath, err)
	}
	return c
}

// write puts content in a fresh temporary directory and returns its path, so
// every filesystem case is hermetic.
func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "release-streams.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCatalogueValid: a valid catalogue decodes field by field — the three
// streams, each one's names in order, the version it is at, the name next and
// the tag prefix — and every file this format refuses is refused with a sentence
// saying which stream and which field is the one that is wrong.
func TestCatalogueValid(t *testing.T) {
	c, err := parseFixture(validYAML)
	if err != nil {
		t.Fatalf("the fixture is a valid catalogue, and it did not parse: %v", err)
	}
	want := []Stream{
		{Name: "cli", Theme: "animals", Names: []string{"Badger", "Camel", "Dolphin"},
			Current: Version{Major: 0, Minor: 10, Patch: 4}, Next: "Camel", TagPrefix: "v"},
		{Name: "console", Theme: "fruits", Names: []string{"Cherry", "Mango", "Melon"},
			Current: Version{Major: 0, Minor: 110, Patch: 0}, Next: "Mango", TagPrefix: "console-v"},
		{Name: "mobile", Theme: "birds", Names: []string{"Falcon", "Pelican"},
			Current: Version{Major: 0, Minor: 0, Patch: 0}, Next: "Falcon", TagPrefix: "mobile-v"},
	}
	if !reflect.DeepEqual(c.Streams, want) {
		t.Errorf("catalogue = %+v, want %+v", c.Streams, want)
	}
	if s, ok := c.Lookup(Console); !ok || s.TagPrefix != "console-v" {
		t.Errorf("Lookup(console) = %+v, %v, want the console stream", s, ok)
	}
	if s, ok := c.Lookup("desktop"); ok {
		t.Errorf("Lookup(desktop) = %+v, want no stream: the three are %v", s, streamNames)
	}

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"not YAML at all", "streams: [\n", "is not valid YAML"},
		{"an empty file", "", "is empty"},
		{"a file of comments", "# nothing to see\n", "is empty"},
		{"not a mapping", "- cli\n", "is not a YAML mapping"},
		{"two documents", validYAML + "---\nstreams: []\n", "more than one YAML document"},
		{"no streams key", "stream: []\n", "field stream not found"},
		{"no streams at all", "streams: []\n", "names no stream"},
		{"a stream that is not one of the three", rewrite(t, "name: cli", "name: desktop"), `stream "desktop"`},
		{"the same stream twice", rewrite(t, "name: mobile", "name: cli"), "the cli stream twice"},
		{"a stream missing", rewrite(t, mobileStream, ""), "names no mobile stream"},
		{"a theme that is not this stream's", rewrite(t, "theme: fruits", "theme: vegetables"), `theme is "vegetables"`},
		{"a tag prefix that is not this stream's", rewrite(t, "tag_prefix: console-v", "tag_prefix: v"), `tag_prefix is "v", want "console-v"`},
		{"a name that is not a word", rewrite(t, "names: [Falcon, Pelican]", "names: [Falcon, Pelican 2]"), "is not a release name"},
		{"an unknown key", rewrite(t, "theme: animals", "themes: animals"), "field themes not found"},
		{"a current that is not a version", rewrite(t, `current: "0.10.4"`, `current: "0.10"`), "MAJOR.MINOR.PATCH"},
		{"a current written as a number", rewrite(t, `current: "0.10.4"`, "current: 0.5"), "quoted"},
		{"a next name that is not in the list", rewrite(t, "next: Camel", "next: Moose"), "not one of its names"},
		{"a next name that is already spent", rewrite(t, "next: Camel", "next: Badger"), "has already spent"},
		{"a next name with nothing spent", rewrite(t, "next: Falcon", "next: Pelican"), "released nothing"},
		{"a stream with no names", rewrite(t, "names: [Falcon, Pelican]", "names: []"), "the mobile stream names no release names"},
		{"an anchor", rewrite(t, "names: [Badger, Camel, Dolphin]", "names: &n [Badger, Camel, Dolphin]"), "anchor or alias"},
		{"a merge key", rewrite(t, "    theme: animals\n", "    <<: {theme: animals}\n"), "merge key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFixture(tc.body)
			if err == nil {
				t.Fatalf("parsed, want a refusal: this file is not a catalogue")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to carry %q", err, tc.want)
			}
		})
	}
}

// TestNamesAlphabetical: a stream spends its names in alphabetical order, one
// per minor, so the list is read as the stream's history and the next name is
// the one after what has been spent rather than a name in the middle of it.
func TestNamesAlphabetical(t *testing.T) {
	t.Run("out of order", func(t *testing.T) {
		_, err := parseFixture(rewrite(t, "names: [Badger, Camel, Dolphin]", "names: [Badger, Dolphin, Camel]"))
		if err == nil || !strings.Contains(err.Error(), "not in alphabetical order") {
			t.Errorf("refusal = %v, want the names refused for their order", err)
		}
	})
	t.Run("a name twice", func(t *testing.T) {
		_, err := parseFixture(rewrite(t, "names: [Badger, Camel, Dolphin]", "names: [Badger, Badger, Camel]"))
		if err == nil || !strings.Contains(err.Error(), "twice") {
			t.Errorf("refusal = %v, want the repeated name refused", err)
		}
	})
	t.Run("the shipped file", func(t *testing.T) {
		for _, s := range shipped(t).Streams {
			for i := 1; i < len(s.Names); i++ {
				if s.Names[i] <= s.Names[i-1] {
					t.Errorf("the %s stream's names are %v: %q is not after %q, and the list is the order the names are spent in",
						s.Name, s.Names, s.Names[i], s.Names[i-1])
				}
			}
		}
	})
}

// TestNoNameInTwoStreams: a release name belongs to one stream. Two lists that
// share a name make "Moose is out" ambiguous, and the whole point of a theme per
// stream is that the name alone says which product it names.
func TestNoNameInTwoStreams(t *testing.T) {
	t.Run("a shared name", func(t *testing.T) {
		body := rewrite(t, "names: [Badger, Camel, Dolphin]", "names: [Badger, Camel, Dolphin, Moose]")
		body = strings.Replace(body, "names: [Cherry, Mango, Melon]", "names: [Cherry, Mango, Melon, Moose]", 1)
		_, err := parseFixture(body)
		if err == nil || !strings.Contains(err.Error(), "belongs to one") {
			t.Errorf("refusal = %v, want the shared name refused", err)
		}
	})
	t.Run("the shipped file", func(t *testing.T) {
		owner := map[string]string{}
		for _, s := range shipped(t).Streams {
			for _, name := range s.Names {
				if other, ok := owner[name]; ok {
					t.Errorf("%q names both the %s and the %s streams", name, other, s.Name)
				}
				owner[name] = s.Name
			}
		}
	})
}

// TestConsoleFloor: the console's version line never goes backwards. The
// console's updater refuses a downgrade, so a catalogue that fell below the
// release the console has already shipped would have every installation refuse
// every update it was offered afterwards (F33). The floor is the first release
// on the console's own line, and the fixture the floor is read from is a version
// from before that line existed.
func TestConsoleFloor(t *testing.T) {
	for _, tc := range []struct {
		current string
		next    string
		refused bool
	}{
		{"0.110.0", "Mango", false},
		{"0.111.0", "Melon", false},
		{"0.109.9", "Mango", true},
		{"0.11.0", "Mango", true},
		{"0.9.7", "Mango", true},
		{"0.0.0", "Cherry", true},
	} {
		t.Run(tc.current, func(t *testing.T) {
			body := rewrite(t, `current: "0.110.0"`, fmt.Sprintf("current: %q", tc.current))
			body = strings.Replace(body, "next: Mango", "next: "+tc.next, 1)
			_, err := parseFixture(body)
			if !tc.refused {
				if err != nil {
					t.Fatalf("the console at %s was refused: %v; the floor is %s", tc.current, err, ConsoleFloor)
				}
				return
			}
			if err == nil {
				t.Fatalf("the console at %s parsed, want a refusal: it is below %s, a version the console has already shipped", tc.current, ConsoleFloor)
			}
			if !strings.Contains(err.Error(), ConsoleFloor.String()) {
				t.Errorf("refusal = %q, want it to name the floor %s", err, ConsoleFloor)
			}
		})
	}
	t.Run("the shipped file", func(t *testing.T) {
		console, ok := shipped(t).Lookup(Console)
		if !ok {
			t.Fatal("the shipped catalogue names no console stream")
		}
		if !console.Current.AtLeast(ConsoleFloor) {
			t.Errorf("the console stream is at %s and the floor is %s", console.Current, ConsoleFloor)
		}
	})
}

// TestForbiddenConsoleNames: the console's names are fruits, and two of them are
// avoided — "Apple", because the desktop app is not an Apple product and a
// release titled after one would say it is, and "Lemon", because it reads as a
// disclaimer on a release that has not gone sour (docs/release.md, "Console
// releases"; GD7). The list is refused at the file rather than caught in review.
func TestForbiddenConsoleNames(t *testing.T) {
	for _, tc := range []struct{ name, list string }{
		{"Apple", "[Apple, Cherry, Mango, Melon]"},
		{"Lemon", "[Cherry, Lemon, Mango, Melon]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFixture(rewrite(t, "names: [Cherry, Mango, Melon]", "names: "+tc.list))
			if err == nil {
				t.Fatalf("a console list carrying %s parsed, want a refusal", tc.name)
			}
			if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("refusal = %q, want it to name %s", err, tc.name)
			}
		})
	}
	t.Run("the shipped file", func(t *testing.T) {
		console, ok := shipped(t).Lookup(Console)
		if !ok {
			t.Fatal("the shipped catalogue names no console stream")
		}
		for _, forbidden := range forbiddenConsoleNames {
			if slices.Contains(console.Names, forbidden) {
				t.Errorf("the console's names are %v, and %q is one of the names its list avoids", console.Names, forbidden)
			}
		}
	})
}

// TestRealFileParses: the file that ships is a catalogue this build reads, and
// it carries the three streams docs/release.md describes. What is pinned here is
// what a release cannot move: the streams and their order, each one's theme and
// tag prefix, the first name of each list, the CLI's hold on the animal theme,
// the CLI's line staying below the console's, and the console's floor. A version
// or a next name changes at every release, so no assertion here may be one a
// release moves: the CLI's own minor is the one number in this file the release
// process rewrites.
func TestRealFileParses(t *testing.T) {
	c := shipped(t)
	if len(c.Streams) != 3 {
		t.Fatalf("the catalogue names %d streams, want the three: %v", len(c.Streams), streamNames)
	}
	for i, want := range []struct{ name, theme, tag, first string }{
		{CLI, "animals", "v", "Badger"},
		{Console, "fruits", "console-v", "Cherry"},
		{Mobile, "birds", "mobile-v", "Falcon"},
	} {
		s := c.Streams[i]
		if s.Name != want.name || s.Theme != want.theme || s.TagPrefix != want.tag || len(s.Names) == 0 || s.Names[0] != want.first {
			t.Errorf("stream %d = %+v, want the %s stream, %s, %s, starting at %s", i, s, want.name, want.theme, want.tag, want.first)
		}
	}

	cli, _ := c.Lookup(CLI)
	if cli.Current.Major != 0 || cli.Current.Minor >= ConsoleFloor.Minor {
		t.Errorf("the CLI stream is at %s, want a 0.x line below the console's %s: the two lines are numbered apart so that a version on its own says which stream it belongs to, and this one's minor is not pinned here because every CLI release moves it",
			cli.Current, ConsoleFloor)
	}
	if slices.Index(cli.Names, cli.Next) < 1 {
		t.Errorf("the CLI's next name is %q and its names are %v: the CLI has shipped a release, so its next name cannot be the first of its list", cli.Next, cli.Names)
	}

	// The app's stream is at the 0.0.0 placeholder until its first release and at
	// a version on its own line from then on, so either is what the file may say:
	// pinning the placeholder would break on the commit that cuts Falcon.
	mobile, _ := c.Lookup(Mobile)
	if first := FirstVersion(Mobile); !mobile.Current.IsZero() && !mobile.Current.AtLeast(first) {
		t.Errorf("the mobile stream is at %s: want the 0.0.0 placeholder or a version on the app's own line, %s or later", mobile.Current, first)
	}
	for _, name := range mobile.Names {
		if slices.Contains(cli.Names, name) {
			t.Errorf("%q is in the CLI's list and the mobile app's: the CLI's names carry no bird, because Falcon moved to the app", name)
		}
	}
}

// TestNextIsFirstUnspent: names are spent one per minor from the stream's first
// minor (the CLI's Badger is 0.10, the console's Cherry 0.110, the app's Falcon
// 0.200), so the version a stream is at says how many names it has spent, and
// its next name is the one after them: not one already spent, and not one
// further down the list that skips a name nobody released under.
func TestNextIsFirstUnspent(t *testing.T) {
	for _, tc := range []struct {
		name, current, next, want string
	}{
		{"the first minor spent its first name", "0.10.4", "Camel", ""},
		{"the second minor spent its second name", "0.11.2", "Dolphin", ""},
		{"a name skipped", "0.10.4", "Dolphin", `next name is "Camel"`},
		{"a name already spent", "0.11.0", "Camel", "has already spent"},
		{"the first name already spent", "0.11.0", "Badger", "has already spent"},
		{"every name spent", "0.12.0", "Dolphin", "has spent every name"},
		{"a version before the named line", "0.9.7", "Camel", "0.10.0"},
		{"a major the line does not have", "1.10.0", "Camel", "0.10.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := rewrite(t, `current: "0.10.4"`, fmt.Sprintf("current: %q", tc.current))
			body = strings.Replace(body, "next: Camel", "next: "+tc.next, 1)
			_, err := parseFixture(body)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("the CLI at %s with next %s was refused: %v", tc.current, tc.next, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %v, want it to carry %q", err, tc.want)
			}
		})
	}
	t.Run("the shipped file", func(t *testing.T) {
		for _, s := range shipped(t).Streams {
			if s.Current.IsZero() {
				if s.Next != s.Names[0] {
					t.Errorf("the %s stream has shipped nothing and its next name is %q, want %q", s.Name, s.Next, s.Names[0])
				}
				continue
			}
			spent := s.Current.Minor - FirstVersion(s.Name).Minor
			if spent < 0 || spent+1 >= len(s.Names) || s.Names[spent+1] != s.Next {
				t.Errorf("the %s stream is at %s and its next name is %q; its names are %v", s.Name, s.Current, s.Next, s.Names)
			}
		}
	})
}

// TestVersionScalar: a version that YAML reads as a number is refused, because
// what it means depends on the reader's schema; one that no YAML schema reads as
// a number is the string it looks like, quoted or not.
func TestVersionScalar(t *testing.T) {
	if _, err := parseFixture(rewrite(t, `current: "0.10.4"`, "current: 0.10.4")); err != nil {
		t.Errorf("an unquoted 0.10.4 was refused: %v; YAML reads it as a string in every schema", err)
	}
	if _, err := parseFixture(rewrite(t, `current: "0.10.4"`, "current: 0.10")); err == nil || !strings.Contains(err.Error(), "quoted") {
		t.Errorf("refusal = %v, want an unquoted 0.10 refused as a number", err)
	}
}

// TestSymlinkRefused: the catalogue is read where it sits. A link is refused
// before anything is decoded, so a file that points out of the checkout — at a
// device, or at a catalogue somewhere else on the machine — is not read at all.
func TestSymlinkRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.yaml")
	if err := os.WriteFile(target, []byte(validYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, target string }{
		{"a link to a catalogue", target},
		{"a link to nothing", filepath.Join(dir, "gone.yaml")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := filepath.Join(dir, "release-streams.yaml")
			if err := os.Symlink(tc.target, link); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(link)
			_, err := Read(link)
			if err == nil {
				t.Fatal("read a symlink, want a refusal: a link is how a file leaves the checkout it is read in")
			}
			if !strings.Contains(err.Error(), "symlink") {
				t.Errorf("refusal = %q, want it to name the symlink", err)
			}
		})
	}
	t.Run("a directory", func(t *testing.T) {
		_, err := Read(dir)
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("refusal = %v, want a directory refused as not a regular file", err)
		}
	})
}

// TestMissingFileRefused: a path with nothing behind it is refused with a
// sentence naming the path and the file the catalogue is, which is what a caller
// that guessed one has to show — the console hands the CLI an installation's
// checkout, and a checkout without the file is a checkout this build says so
// about rather than an empty catalogue.
func TestMissingFileRefused(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "release-streams.yaml")
	_, err := Read(missing)
	if err == nil {
		t.Fatal("read a path with no file behind it, want a refusal")
	}
	if !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("refusal = %q, want it to name %s and say it is not there", err, missing)
	}
}

// TestOversizeRefused: a file over the limit is refused by its size, before any
// of it is decoded. The file is repository-written, and nothing about it is
// worth a parse that a stat refuses.
func TestOversizeRefused(t *testing.T) {
	// One byte over, with content that is not YAML at all: a refusal that names
	// the limit is a refusal that never looked at the bytes.
	over := strings.Repeat("x", MaxBytes+1)
	_, err := Read(write(t, over))
	if err == nil {
		t.Fatal("read a file over the limit, want a refusal")
	}
	if !strings.Contains(err.Error(), "limit") || strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("refusal = %q, want it to be about the %d byte limit rather than about the file's content", err, MaxBytes)
	}
	if _, err := Parse("release-streams.yaml", []byte(over)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("Parse refusal = %v, want the same limit", err)
	}

	// At the limit exactly, the size is not what refuses it: the fixture padded
	// with a trailing comment is still a catalogue.
	pad := MaxBytes - len(validYAML) - len("\n#")
	if pad < 0 {
		t.Fatalf("the fixture no longer fits under the %d byte limit", MaxBytes)
	}
	at := validYAML + "\n#" + strings.Repeat("p", pad)
	if len(at) != MaxBytes {
		t.Fatalf("padded the fixture to %d bytes, want %d", len(at), MaxBytes)
	}
	if _, err := Read(write(t, at)); err != nil {
		t.Errorf("a file of exactly %d bytes was refused: %v", MaxBytes, err)
	}
}

// TestAliasRefused: YAML anchors and aliases are refused, so the document that
// reaches a reader is the document a person wrote. The catalogue is small enough
// that an alias is never needed, and an alias is how one file says two things:
// what a reader sees, and what the text under it names.
func TestAliasRefused(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"an anchor", rewrite(t, "names: [Badger, Camel, Dolphin]", "names: &cli [Badger, Camel, Dolphin]")},
		{"an alias", rewrite(t, "names: [Badger, Camel, Dolphin]", "names: &cli [Badger, Camel, Dolphin]\n    spent: *cli")},
		{"an alias in the list", rewrite(t, "names: [Badger, Camel, Dolphin]", "names: [Badger, &b Camel, Dolphin, *b]")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFixture(tc.body)
			if err == nil {
				t.Fatal("parsed, want a refusal")
			}
			if !strings.Contains(err.Error(), "anchor or alias") {
				t.Errorf("refusal = %q, want it to name the anchor or the alias", err)
			}
		})
	}
}
