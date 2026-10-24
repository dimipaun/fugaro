// Package streams reads the release streams catalogue: docs/release-streams.yaml,
// the one file that names the three release streams, the naming theme each one
// spends in order, the version each stream is at and the prefix its tags carry.
//
// One file rather than a list beside each reader, because two readers need the
// same facts and two lists is how one release gets two names (GD7,
// docs/console-dependency-graph/reconciliation.md): the console's graph view
// draws its stream columns from this catalogue, and scripts/release.sh asks this
// package, through internal/streams/cmd/streams, for the CLI stream's name and
// version. The file is a repository fact written by a person, like the release
// notes, and this package is its only reader.
//
// The file is untrusted in the sense docs/design/desktop-console.md §5.1 gives
// the word: a contributor's branch writes it, and what it carries is compared
// against a release and put on a screen. So Read and Parse refuse rather than
// repair — a symlink, a file over MaxBytes, YAML anchors and aliases, a key this
// build does not know, a stream that is not one of the three, a theme or a tag
// prefix that is not the stream's own, a name out of alphabetical order or in
// two lists, and a console version below the one the console has already
// shipped.
package streams

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxBytes is the largest streams file this build reads, and the cap the
// console applies to it as well. The file is a few hundred bytes and a name list
// grows by one per release: 64 KiB is room for every name the three streams will
// ever spend, and the point past which a file is a mistake rather than a
// catalogue.
const MaxBytes = 64 << 10

// The three streams, as the file and every caller spell them. A stream in the
// file that is not one of these, or one of these that the file does not carry,
// is a refusal: the catalogue is the three.
const (
	CLI     = "cli"
	Console = "console"
	Mobile  = "mobile"
)

// streamNames is the three in the order docs/release.md writes them, the CLI
// first, which is the order a message names them in.
var streamNames = []string{CLI, Console, Mobile}

// themes is each stream's naming theme, so that a release name alone says which
// stream it belongs to (the spec's addendum §2). The CLI's list carries no bird:
// Falcon moved to the app.
var themes = map[string]string{CLI: "animals", Console: "fruits", Mobile: "birds"}

// tagPrefixes is the prefix each stream's release tags carry. A tag starts
// exactly one stream's workflow, so a prefix that is not the stream's own would
// release the wrong product.
var tagPrefixes = map[string]string{CLI: "v", Console: "console-v", Mobile: "mobile-v"}

// forbiddenConsoleNames are the fruit names the console's list refuses: "Apple",
// because the desktop app is not an Apple product and a release titled after one
// would say it is, and "Lemon", because it reads as a disclaimer on a release
// that has not gone sour (docs/release.md, "Console releases"; GD7).
var forbiddenConsoleNames = []string{"Apple", "Lemon"}

// nameRE is a release name: one word, a capital letter and then lower-case
// letters. The lists are single words today ("Cherry", "Falcon"), and a name
// reaches a release note, a tag and the console's screen, so its shape is
// checked here rather than left to whoever reads it next.
var nameRE = regexp.MustCompile(`^[A-Z][a-z]+$`)

// versionRE is the shape a stream's version is written in: MAJOR.MINOR.PATCH,
// three numbers with no leading zeros, what a tag carries after its prefix.
var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ConsoleFloor is the lowest version the console stream may carry: 0.110.0, the
// console's first release on its own version line ("Cherry"). The console's
// updater refuses a downgrade, so a catalogue that fell below the release the
// console has already shipped would have every installation refuse every update
// it was offered afterwards — hence a floor in the reader rather than a note to
// whoever edits the file (F33). It is 0.110.0 rather than 0.11.0 because 0.11.0
// is the CLI's Camel (the owner's renumbering ruling of 2026-10-24).
var ConsoleFloor = Version{Major: 0, Minor: 110, Patch: 0}

// Version is a stream's version: the three numbers its tags carry after their
// prefix.
type Version struct {
	Major int
	Minor int
	Patch int
}

// String is the version as the file and a tag write it.
func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// IsZero reports whether the stream has shipped nothing yet. The file says that
// with 0.0.0, and it is a claim about the stream rather than a version anybody
// released: the first release of a stream is a minor of its own line.
func (v Version) IsZero() bool { return v == Version{} }

// atLeast reports whether v is later than or equal to other.
func (v Version) atLeast(other Version) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor > other.Minor
	}
	return v.Patch >= other.Patch
}

// UnmarshalYAML reads a version from its own scalar, refusing anything that is
// not a quoted MAJOR.MINOR.PATCH string: an unquoted 0.10 is a YAML number, and
// a version whose value depends on which YAML schema a reader implements is a
// version that means two things.
func (v *Version) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return fmt.Errorf("a version is a quoted string, as in %q", "0.10.4")
	}
	m := versionRE.FindStringSubmatch(n.Value)
	if m == nil {
		return fmt.Errorf("%q is not MAJOR.MINOR.PATCH, the shape %q writes", n.Value, "0.10.4")
	}
	for i, field := range []*int{&v.Major, &v.Minor, &v.Patch} {
		num, err := strconv.Atoi(m[i+1])
		if err != nil {
			return fmt.Errorf("%q carries a number this build cannot read", n.Value)
		}
		*field = num
	}
	return nil
}

// Stream is one release stream: its name, the theme its release names come from,
// the names in the order they are spent, the version it is at, the name its next
// minor takes, and the prefix its tags carry.
type Stream struct {
	Name      string   `yaml:"name"`
	Theme     string   `yaml:"theme"`
	Names     []string `yaml:"names"`
	Current   Version  `yaml:"current"`
	Next      string   `yaml:"next"`
	TagPrefix string   `yaml:"tag_prefix"`
}

// Catalogue is the file: the three streams, in the order the file writes them,
// which is the order their columns are drawn in.
type Catalogue struct {
	Streams []Stream
}

// Lookup returns the stream named name and whether the catalogue carries it.
func (c Catalogue) Lookup(name string) (Stream, bool) {
	for _, s := range c.Streams {
		if s.Name == name {
			return s, true
		}
	}
	return Stream{}, false
}

// catalogueFile is the file's own shape: one key, because a key this build does
// not know is a file written to a format it does not have.
type catalogueFile struct {
	Streams []Stream `yaml:"streams"`
}

// Read reads and validates the catalogue at path. A symlink, a file that is not
// a regular file and a file over MaxBytes are refused before anything is
// decoded, so what a hostile file costs is a stat rather than a parse.
func Read(path string) (Catalogue, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Catalogue{}, refuse(path, "no such file; the streams catalogue is the repository's own docs/release-streams.yaml")
		}
		return Catalogue{}, refuse(path, "could not be read: %v", err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return Catalogue{}, refuse(path, "is a symlink; the catalogue is read where it sits, so a link out of the checkout is not a catalogue")
	case !info.Mode().IsRegular():
		return Catalogue{}, refuse(path, "is not a regular file")
	case info.Size() > MaxBytes:
		return Catalogue{}, refuse(path, "is %d bytes, over the %d byte (64 KiB) limit", info.Size(), MaxBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		return Catalogue{}, refuse(path, "could not be read: %v", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return Catalogue{}, refuse(path, "could not be read: %v", err)
	}
	// The stat and the open are two calls, so the path could have been pointed
	// somewhere else between them by whoever can write the checkout: SameFile
	// says the bytes about to be decoded are the bytes that were vetted.
	if !os.SameFile(info, opened) {
		return Catalogue{}, refuse(path, "changed between the check and the read")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return Catalogue{}, refuse(path, "could not be read: %v", err)
	}
	if len(data) > MaxBytes {
		return Catalogue{}, refuse(path, "is %d bytes, over the %d byte (64 KiB) limit", len(data), MaxBytes)
	}
	return Parse(path, data)
}

// Parse validates one catalogue's bytes. path is used for messages only, so a
// caller holding the file in memory gets the refusals a caller reading it off
// disk does.
func Parse(path string, data []byte) (Catalogue, error) {
	if len(data) > MaxBytes {
		return Catalogue{}, refuse(path, "is %d bytes, over the %d byte (64 KiB) limit", len(data), MaxBytes)
	}

	// The shape first, over the raw nodes: the refusals an anchor or an alias
	// earns have no decoded form to be caught in.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Catalogue{}, refuse(path, "is not valid YAML: %v", err)
	}
	if root.Kind == 0 {
		return Catalogue{}, refuse(path, "is empty")
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return Catalogue{}, refuse(path, "is not a YAML mapping")
	}
	if err := checkShape(path, &root); err != nil {
		return Catalogue{}, err
	}

	// Then strictly: at this format the field set is closed, so a key this build
	// does not know is a file written to a format it does not have.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var file catalogueFile
	if err := dec.Decode(&file); err != nil {
		return Catalogue{}, refuse(path, "does not decode: %v", err)
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return Catalogue{}, refuse(path, "holds more than one YAML document")
	}
	if err := validate(path, file.Streams); err != nil {
		return Catalogue{}, err
	}
	return Catalogue{Streams: file.Streams}, nil
}

// checkShape refuses anchors, aliases and merge keys anywhere in the document. A
// catalogue is small enough that none of them is ever needed, and each is a way
// for the text a reader sees to differ from the values a reader gets.
func checkShape(path string, n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return refuse(path, "line %d uses a YAML anchor or alias, which the streams file refuses", n.Line)
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i]; k.Value == "<<" || k.Tag == "!!merge" {
				return refuse(path, "line %d uses a YAML merge key <<, which the streams file refuses", k.Line)
			}
		}
	}
	for _, c := range n.Content {
		if err := checkShape(path, c); err != nil {
			return err
		}
	}
	return nil
}

// validate holds the file to what the three streams are: the three and no
// others, each one's theme and tag prefix, the names spent in alphabetical order
// and never twice, the next name the one after what is spent, and the version a
// release can no longer go behind.
func validate(path string, list []Stream) error {
	if len(list) == 0 {
		return refuse(path, "names no stream; the file names the three: %s", strings.Join(streamNames, ", "))
	}
	owner := map[string]string{}
	seen := map[string]bool{}
	for _, s := range list {
		theme, known := themes[s.Name]
		if !known {
			return refuse(path, "names the stream %q, and the three streams are %s", s.Name, strings.Join(streamNames, ", "))
		}
		if seen[s.Name] {
			return refuse(path, "names the %s stream twice", s.Name)
		}
		seen[s.Name] = true
		if s.Theme != theme {
			return refuse(path, "the %s stream's theme is %q, want %q: a name alone has to say which stream it belongs to", s.Name, s.Theme, theme)
		}
		if prefix := tagPrefixes[s.Name]; s.TagPrefix != prefix {
			return refuse(path, "the %s stream's tag_prefix is %q, want %q", s.Name, s.TagPrefix, prefix)
		}
		if len(s.Names) == 0 {
			return refuse(path, "the %s stream names no release names", s.Name)
		}
		for i, name := range s.Names {
			if !nameRE.MatchString(name) {
				return refuse(path, "the %s stream's name %q is not a release name: one word, a capital letter and then lower-case letters", s.Name, name)
			}
			switch {
			case i > 0 && name == s.Names[i-1]:
				return refuse(path, "the %s stream names %q twice", s.Name, name)
			case i > 0 && name < s.Names[i-1]:
				return refuse(path, "the %s stream's names are not in alphabetical order: %q comes after %q, and the list is the order they are spent in", s.Name, name, s.Names[i-1])
			}
			if other, taken := owner[name]; taken && other != s.Name {
				return refuse(path, "%q names the %s and the %s streams: a release name belongs to one stream", name, other, s.Name)
			}
			owner[name] = s.Name
			if s.Name == Console && slices.Contains(forbiddenConsoleNames, name) {
				return refuse(path, "the console stream's name %q is avoided: the desktop app is not an Apple product, and a lemon reads as a disclaimer on a release that has not gone sour", name)
			}
		}
		next := slices.Index(s.Names, s.Next)
		if next < 0 {
			return refuse(path, "the %s stream's next name is %q, which is not one of its names", s.Name, s.Next)
		}
		switch {
		case s.Current.IsZero() && next != 0:
			return refuse(path, "the %s stream has released nothing, so its next name is %q, the first of its list", s.Name, s.Names[0])
		case !s.Current.IsZero() && next == 0:
			return refuse(path, "the %s stream is at %s, so its next name cannot be %q: the release that version came from has already spent it", s.Name, s.Current, s.Next)
		}
		if s.Name == Console && !s.Current.atLeast(ConsoleFloor) {
			return refuse(path, "the console stream is at %s, below %s, a version it has already shipped: the console's updater refuses a downgrade, so its version line never goes backwards", s.Current, ConsoleFloor)
		}
	}
	for _, name := range streamNames {
		if !seen[name] {
			return refuse(path, "names no %s stream", name)
		}
	}
	return nil
}

// refuse is a sentence naming the file and what is wrong with it.
func refuse(path, format string, args ...any) error {
	return fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...))
}
