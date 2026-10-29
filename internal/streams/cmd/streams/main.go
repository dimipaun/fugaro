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
//	streams set --check <stream> <version>
//	                                 everything set does, in memory: the
//	                                 rewrite and its re-read, and no write
//
// The path is the repository's own, relative to the working directory the script
// already stands in, and every refusal comes from the package's reader: a
// symlink, a file over MaxBytes, an anchor, a stream that is not one of the
// three, a tag prefix that is not the stream's own and a console version below
// the one the console has shipped are refused here exactly as they are for the
// graph. `set` refuses a version below the stream's current and a write that
// does not read back, so the release commit cannot lower the floor or carry a
// catalogue this build cannot parse; `set --check` is the same computation with
// no write, which is how the release's dry run predicts its step 2.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/streams"
	"gopkg.in/yaml.v3"
)

// cataloguePath is where the file sits in the checkout, the path release.sh
// runs this from.
const cataloguePath = "docs/release-streams.yaml"

const usage = "usage: streams get <stream> | streams name <stream> <version> | streams set [--check] <stream> <version>"

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
		check := len(args) > 1 && args[1] == "--check"
		if check {
			args = args[1:]
		}
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
		return setCurrent(s.Name, v, check, out)
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

// nameIndex is the index in the stream's names of the name v's minor spends, by
// the reader's own rule (internal/streams): the first name is the stream's
// FirstVersion minor, and each minor after it on that major spends the next
// name. A version off that line -- another major, or a minor below the named
// line -- is refused: the catalogue cannot record it, and the reader would
// refuse a file that did.
func nameIndex(s streams.Stream, v streams.Version) (int, error) {
	first := streams.FirstVersion(s.Name)
	if v.Major != first.Major || v.Minor < first.Minor {
		return 0, fmt.Errorf("%s is not on the line the %s stream's names describe, which starts at %s %q: the catalogue cannot record it", v, s.Name, first, s.Names[0])
	}
	return v.Minor - first.Minor, nil
}

// spokenName is the release at v as the stream speaks it: the name of the minor
// it falls in and the patch's word -- "Badger four" for 0.10.4, and the name
// alone for a minor ("Camel" for 0.11.0), as docs/release-streams.yaml's header
// writes it. A version the file does not describe -- off the named line, or past
// the last name the stream has -- is refused rather than guessed at: the names
// are the file's to spend.
func spokenName(s streams.Stream, v streams.Version) (string, error) {
	i, err := nameIndex(s, v)
	if err != nil {
		return "", err
	}
	if i >= len(s.Names) {
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
// `next` with it (nextAfter). Both fields move because they are one statement:
// `next` is the name the minor after the current one takes, and the reader
// refuses a file where the two disagree.
//
// It refuses a version below the stream's current: the file is the floor the
// next release reads, and a write that lowered it would let a release go behind
// one that has shipped. The version the stream is already at is allowed, since
// an interrupted release re-runs with it. With check it computes, rewrites and
// re-reads in memory and writes nothing, printing the row it would write.
func setCurrent(name string, v streams.Version, check bool, out io.Writer) error {
	// Read vets the path before anything is written through it: a symlink and a
	// file that is not the catalogue are refused, as they are for every reader.
	// It is also where the stream's own names come from, so the write moves
	// `next` from the file's list rather than from a second reading of it.
	s, err := stream(name)
	if err != nil {
		return err
	}
	if !v.AtLeast(s.Current) {
		return fmt.Errorf("%s is below the %s stream's current %s in %s, the version it has already shipped: a release never lowers that floor", v, s.Name, s.Current, cataloguePath)
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
	if check {
		fmt.Fprintf(out, "current=%s\nnext=%s\n", v, next)
		return nil
	}
	return writeAtomic(cataloguePath, updated)
}

// nextAfter is the name `next` takes once v is the stream's current version: the
// name after the one v's minor spends. A version with no name after it is
// refused, because `next` would have to name a release that has already shipped.
func nextAfter(s streams.Stream, v streams.Version) (string, error) {
	i, err := nameIndex(s, v)
	if err != nil {
		return "", err
	}
	if i+1 >= len(s.Names) {
		return "", fmt.Errorf("%s leaves the %s stream no name for the minor after it: its names are %v, and `next` would have to name a release that has already shipped. Add a name to %s, or release a patch of the minor the stream is at", v, s.Name, s.Names, cataloguePath)
	}
	return s.Names[i+1], nil
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
