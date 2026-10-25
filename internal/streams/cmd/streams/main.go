// Command streams reads the repository's release streams catalogue,
// docs/release-streams.yaml, for scripts/release.sh: the release takes the
// version it must not go behind, the name it is spoken with, and the version it
// writes back into the file from it. The file is the one GD7 chose -- one list,
// two readers (docs/console-dependency-graph/reconciliation.md) -- and the
// console's graph reads the same file through this package in Go; the release
// script has no YAML reader of its own, so it runs this from the checkout root:
//
//	streams get <stream>             the stream's fields, one key=value per line
//	streams name <stream> <version>  the release's spoken name ("Camel two")
//	streams set <stream> <version>   record the version as the stream's current,
//	                                 and move `next` when it opens a new minor
//
// The path is the repository's own, relative to the working directory the script
// already stands in, and every refusal comes from the package's reader: a
// symlink, a file over MaxBytes, an anchor, a stream that is not one of the
// three, a tag prefix that is not the stream's own and a console version below
// the one the console has shipped are refused here exactly as they are for the
// graph. `set` refuses a write that does not read back, so the release commit
// cannot carry a catalogue this build cannot parse.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/streams"
	"gopkg.in/yaml.v3"
)

// cataloguePath is where the file sits in the checkout, the path release.sh
// runs this from.
const cataloguePath = "docs/release-streams.yaml"

const usage = "usage: streams get <stream> | streams name <stream> <version> | streams set <stream> <version>"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "streams: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	switch args[0] {
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("%s", usage)
		}
		s, err := stream(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "name=%s\ntheme=%s\nnames=%s\ncurrent=%s\nnext=%s\ntag_prefix=%s\n",
			s.Name, s.Theme, strings.Join(s.Names, ","), s.Current, s.Next, s.TagPrefix)
		return nil
	case "name":
		if len(args) != 3 {
			return fmt.Errorf("%s", usage)
		}
		s, err := stream(args[1])
		if err != nil {
			return err
		}
		v, err := parseVersion(args[2])
		if err != nil {
			return err
		}
		name, err := spokenName(s, v)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, name)
		return nil
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("%s", usage)
		}
		s, err := stream(args[1])
		if err != nil {
			return err
		}
		v, err := parseVersion(args[2])
		if err != nil {
			return err
		}
		return setCurrent(s.Name, v)
	default:
		return fmt.Errorf("%s", usage)
	}
}

// stream reads the catalogue and returns the stream named name.
func stream(name string) (streams.Stream, error) {
	c, err := streams.Read(cataloguePath)
	if err != nil {
		return streams.Stream{}, err
	}
	s, ok := c.Lookup(name)
	if !ok {
		return streams.Stream{}, fmt.Errorf("%s names no %s stream", cataloguePath, name)
	}
	return s, nil
}

// parseVersion reads a version argument through the catalogue's own shape check:
// what this helper writes into the file has to be a version the file accepts,
// and reusing Version's reader is how it is the same shape rather than a second
// one that can drift from it.
func parseVersion(s string) (streams.Version, error) {
	var v streams.Version
	if err := v.UnmarshalYAML(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}); err != nil {
		return streams.Version{}, err
	}
	return v, nil
}

// spokenName is the release at v as the stream speaks it: the name of the minor
// it falls in and the patch's word -- "Badger four" for 0.10.4, and the name
// alone for a minor ("Camel" for 0.11.0), as docs/release-streams.yaml's header
// writes it. The catalogue's `next` is the name the minor after the current one
// takes, so the name before it is the one the current version carries, and a
// version's own name is that many minors on. A version the file does not
// describe -- a major bump, or one past the last name the stream has -- is
// refused rather than guessed at: the names are the file's to spend.
func spokenName(s streams.Stream, v streams.Version) (string, error) {
	if s.Current.IsZero() {
		return "", fmt.Errorf("the %s stream has released nothing, so the catalogue names no release on it yet (its first is %q)", s.Name, s.Next)
	}
	if v.Major != s.Current.Major || v.Minor < s.Current.Minor {
		return "", fmt.Errorf("%s is not on the line the %s stream is at (%s): the catalogue's names are spent one per minor, from %q", v, s.Name, s.Current, s.Names[0])
	}
	i := slices.Index(s.Names, s.Next) - 1 + (v.Minor - s.Current.Minor)
	if i < 0 || i >= len(s.Names) {
		return "", fmt.Errorf("%s is past the last name the %s stream has (%q), so the catalogue has no name for it", v, s.Name, s.Names[len(s.Names)-1])
	}
	if v.Patch == 0 {
		return s.Names[i], nil
	}
	return s.Names[i] + " " + patchWord(v.Patch), nil
}

// patchWords are the words a patch is spoken with; 0 is the name alone, and a
// patch past nine is spoken as its number rather than invented a word.
var patchWords = []string{"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}

func patchWord(patch int) string {
	if patch < len(patchWords) {
		return patchWords[patch]
	}
	return strconv.Itoa(patch)
}

// The three lines setCurrent rewrites: a stream's block starts at its own
// `- name: <stream>` line and holds one `current: "X.Y.Z"` and one `next: Name`.
var (
	streamStartRE = regexp.MustCompile(`^\s*- name:\s*(\S+)\s*$`)
	currentRE     = regexp.MustCompile(`^(\s*)current:\s*".*"\s*$`)
	nextRE        = regexp.MustCompile(`^(\s*)next:\s*\S+\s*$`)
)

// setCurrent records v as the stream's current version in the file, and moves
// `next` with it when v opens a new minor (nextAfter). Both fields move because
// they are one statement: `next` is the name the minor after the current one
// takes, so a file whose current is 0.12.0 while `next` still says Dolphin --
// the name 0.12.0 itself carries -- is a file that contradicts its own header,
// and spokenName would then speak every later release one minor behind ("Camel
// one" for 0.12.1, "Dolphin" for 0.13.0). The reader accepts such a file (it
// cannot tell where a line's names started), so nothing but this write keeps the
// two in step.
func setCurrent(name string, v streams.Version) error {
	// Read vets the path before anything is written through it: a symlink and a
	// file that is not the catalogue are refused, as they are for every reader.
	// It is also where the stream's own names come from, so the write moves
	// `next` from the file's list rather than from a second reading of it.
	s, err := stream(name)
	if err != nil {
		return err
	}
	next, err := nextAfter(s, v)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(cataloguePath)
	if err != nil {
		return err
	}
	updated, err := replaceStream(data, s.Name, v, next)
	if err != nil {
		return err
	}
	if _, err := streams.Parse(cataloguePath, updated); err != nil {
		return fmt.Errorf("refusing to write a catalogue that does not read back: %w", err)
	}
	return writeAtomic(cataloguePath, updated)
}

// nextAfter is the name `next` takes once v is the stream's current version. The
// names are spent one per minor, so the name of the minor after v's is v's own
// name moved on by however many minors v is past the current one -- and on a
// stream that has released nothing the first release spends the first name, so
// the minor after it takes the second. A version off the stream's line (a major
// bump, or one below the current minor) leaves `next` where it is: the
// catalogue's names describe one version line, and a line of its own needs names
// of its own, which is not a release's to invent.
func nextAfter(s streams.Stream, v streams.Version) (string, error) {
	if v.Major != s.Current.Major || (!s.Current.IsZero() && v.Minor < s.Current.Minor) {
		return s.Next, nil
	}
	i := 1
	if !s.Current.IsZero() {
		i = slices.Index(s.Names, s.Next) + (v.Minor - s.Current.Minor)
	}
	if i >= len(s.Names) {
		return "", fmt.Errorf("%s leaves the %s stream no name for the minor after it: its names are %v, and `next` would have to name a release that has already shipped. Add a name to %s, or release a patch of the minor the stream is at", v, s.Name, s.Names, cataloguePath)
	}
	return s.Names[i], nil
}

// replaceStream rewrites the `current:` and `next:` lines of one stream's block
// and leaves every other byte of the file alone: the file carries the comments
// that explain the names and the versions, and a YAML round-trip would drop them.
func replaceStream(data []byte, name string, v streams.Version, next string) ([]byte, error) {
	lines := strings.SplitAfter(string(data), "\n")
	inBlock := false
	current, nextName := false, false
	for i, line := range lines {
		text := strings.TrimSuffix(line, "\n")
		if m := streamStartRE.FindStringSubmatch(text); m != nil {
			inBlock = m[1] == name
			continue
		}
		if !inBlock {
			continue
		}
		// The line's own newline, so the rewrite keeps the file's last line
		// unterminated when it was.
		rest := strings.TrimPrefix(line, text)
		if m := currentRE.FindStringSubmatch(text); m != nil {
			lines[i] = m[1] + `current: "` + v.String() + `"` + rest
			current = true
			continue
		}
		if m := nextRE.FindStringSubmatch(text); m != nil {
			lines[i] = m[1] + "next: " + next + rest
			nextName = true
			continue
		}
	}
	if !current {
		return nil, fmt.Errorf("%s carries no current version for the %s stream to write", cataloguePath, name)
	}
	if !nextName {
		return nil, fmt.Errorf("%s carries no next name for the %s stream to write", cataloguePath, name)
	}
	return []byte(strings.Join(lines, "")), nil
}

// writeAtomic writes data to path through a temporary file in the same
// directory: a failure leaves the catalogue as it was rather than half written,
// and a half-written one would be committed by the release.
func writeAtomic(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file, so it is not the catalogue to write", path)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".release-streams-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
