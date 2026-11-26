package imagecheck

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// This file reads §2.1's second input, the toolchain: the pins the root mise
// configuration states, as "tool@version". Reduce deliberately reads nothing
// from those files (keyLayerWithoutFacts), because a mise config states
// versions of tools and not of packages, so the key's tool half is read here
// and not by the reduction table.
//
// A tool file this reader cannot read leaves the repository **unkeyed**
// (§2.5), exactly as an unknown lockfile does: two repositories whose
// toolchains differ must not share an image, and a file read as "no tool" is
// the one answer that would give them one. What that costs is named where it
// is paid — a mise.lock or a sub-table pin ([[tools.node]]) is refused rather
// than read, so a repository that states its toolchain that way keeps its own
// per-repository image until this reader grows the format.

// toolChain returns the toolchain facts of a repository: the pins declared in
// image.tools, and the pins of every root mise configuration in tree. The
// result is sorted and deduplicated, since the facts are a set.
func toolChain(tree Tree, declared map[string]string) ([]string, error) {
	seen := map[string]struct{}{}
	for name, version := range declared {
		seen[name+"@"+version] = struct{}{}
	}
	files, err := miseToolFiles(tree)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		data, err := readKeyFile(tree, f)
		if err != nil {
			return nil, err
		}
		pins, err := ToolPins(f, data)
		if err != nil {
			return nil, err
		}
		for _, p := range pins {
			seen[p] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// miseToolFiles are the root mise configuration documents of tree: the paths
// mise reads at each of its directories, the conf.d fragments beside them, and
// its own lockfile. Files only, as every key file is, and a hidden fragment is
// left out, as MainConfigs' own listing leaves it.
func miseToolFiles(tree Tree) ([]string, error) {
	out := map[string]struct{}{}
	for _, p := range append(slices.Clone(config.MiseConfigPaths), config.MiseLockfile) {
		switch _, err := tree.BlobID(p); {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("the mise configuration %s: %w", p, err)
		default:
			out[p] = struct{}{}
		}
	}
	for _, dir := range config.MiseConfigDirs {
		matches, err := tree.Glob(dir + "/*.toml")
		if err != nil {
			return nil, fmt.Errorf("the mise configuration %s: %w", dir, err)
		}
		for _, m := range matches {
			if strings.HasPrefix(path.Base(m), ".") {
				continue
			}
			out[m] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(out)), nil
}

// ToolPins returns the tool pins one mise configuration states, as
// "tool@version". rel names the file in an error. It is exported because the
// asdf format and the TOML one are the whole of what a tool file may be, and
// the refresh reports which file left a repository unkeyed.
func ToolPins(rel string, data []byte) ([]string, error) {
	if path.Base(rel) == ".tool-versions" {
		return asdfPins(rel, data)
	}
	return tomlToolPins(rel, data)
}

// unreadable is the refusal a tool file this reader cannot read gets: the
// repository is unkeyed rather than keyed by the pins beside the shape it did
// not understand.
func unreadable(rel, detail string) error {
	return &UnkeyedError{Path: rel, Reason: UnkeyedUnreadable, Detail: detail}
}

// asdfPins reads .tool-versions: one tool per line, then the version or
// versions installed for it ("nodejs 20.11.0"), a "tool version" pair being
// the format's whole grammar. A "#" comment ends a line, as it does for
// asdf. A line naming a tool with no version states no pin, and is refused
// rather than read as one: the toolchain would then be the toolchain of
// every other repository whose file states nothing either.
func asdfPins(rel string, data []byte) ([]string, error) {
	var out []string
	for i, raw := range strings.Split(string(data), "\n") {
		line, _, _ := strings.Cut(raw, "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		tool := fields[0]
		if len(fields) == 1 {
			return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnpinned,
				Detail: fmt.Sprintf("line %d names %s and no version", i+1, strconv.Quote(tool))}
		}
		for _, v := range fields[1:] {
			if !config.ValidMiseTool(tool, v) {
				return nil, unreadable(rel, fmt.Sprintf("line %d is not a tool and a version this key can state", i+1))
			}
			out = append(out, tool+"@"+v)
		}
	}
	return out, nil
}

// tomlToolPins reads the tool pins of a mise TOML configuration: the
// "[tools]" table's own entries, each a bare tool name and a quoted version.
//
// The reader is deliberately narrow, and everything it does not read it
// refuses rather than skips: a sub-table of tools ([[tools.node]], which is
// what a mise.lock and a version's own table are written as), a dotted key
// naming one (tools.node = "22"), an array or inline table under [tools]
// (mise's alternative spelling of several versions), and a multi-line string
// anywhere in the document, which would hide a "[tools]" line from a reader
// that works a line at a time. A refused file leaves the repository unkeyed
// (§2.5), which is the safe half of that trade: the alternative would key a
// repository by a toolchain it did not read.
//
// Everything outside the [tools] table is other mise configuration — env,
// settings, tasks — and states no tool, so it contributes nothing. That is
// the table's own statement read the way §2.3 reads it, and not a licence to
// ignore a line: a line naming tools outside the table is refused above.
func tomlToolPins(rel string, data []byte) ([]string, error) {
	var out []string
	inTools := false
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, `"""`) || strings.Contains(line, "'''") {
			return nil, unreadable(rel, fmt.Sprintf("line %d opens a multi-line string, which hides the rest of the file from this reader", i+1))
		}
		if strings.HasPrefix(line, "[") {
			name, ok := sectionName(line)
			if !ok {
				return nil, unreadable(rel, fmt.Sprintf("line %d is not a section this reader can read", i+1))
			}
			switch {
			case name == "tools":
				inTools = true
			case strings.HasPrefix(name, "tools."):
				return nil, unreadable(rel, fmt.Sprintf("line %d names a table of tools other than [tools], which this reader cannot read", i+1))
			default:
				inTools = false
			}
			continue
		}
		key, value, ok := assignment(line)
		if !ok {
			// Outside the tools table the line is another setting's, and this
			// reader has nothing to say about it; inside it, an entry it
			// cannot read is a pin it would otherwise drop.
			if !inTools {
				continue
			}
			return nil, unreadable(rel, fmt.Sprintf("line %d is not a tool pin this reader can read", i+1))
		}
		if !inTools {
			if key == "tools" || strings.HasPrefix(key, "tools.") {
				return nil, unreadable(rel, fmt.Sprintf("line %d states a tool outside the [tools] table, which this reader cannot read", i+1))
			}
			continue
		}
		version, ok := quotedString(value)
		if !ok || !config.ValidMiseTool(key, version) {
			return nil, unreadable(rel, fmt.Sprintf("line %d is not a tool name and a quoted version", i+1))
		}
		out = append(out, key+"@"+version)
	}
	return out, nil
}

// sectionName is a TOML table header's name: "[tools]" is "tools", a trailing
// comment is allowed, and anything else — an array of tables ("[[tools]]"), a
// quoted or dotted name, a header this reader cannot close — is refused.
func sectionName(line string) (string, bool) {
	if strings.HasPrefix(line, "[[") {
		return "", false
	}
	rest, ok := strings.CutPrefix(line, "[")
	if !ok {
		return "", false
	}
	body, tail, ok := strings.Cut(rest, "]")
	if !ok {
		return "", false
	}
	if tail = strings.TrimSpace(tail); tail != "" && !strings.HasPrefix(tail, "#") {
		return "", false
	}
	body = strings.TrimSpace(body)
	if body == "" || strings.ContainsAny(body, `"'`) {
		return "", false
	}
	return body, true
}

// assignment splits a TOML key/value line at its first "=" outside a quoted
// string, and reports whether the line is one at all.
func assignment(line string) (key, value string, ok bool) {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			j := strings.IndexByte(line[i+1:], '"')
			if j < 0 {
				return "", "", false
			}
			i += j + 1
		case '\'':
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				return "", "", false
			}
			i += j + 1
		case '=':
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
		}
	}
	return "", "", false
}

// quotedString is a TOML basic or literal string's own value, and nothing
// else: an array, a number, a boolean and an inline table are not a version,
// and reading one as text would key a toolchain mise does not install.
func quotedString(value string) (string, bool) {
	switch {
	case strings.HasPrefix(value, `"`):
		s, err := strconv.Unquote(value)
		if err != nil {
			return "", false
		}
		return s, true
	case strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2:
		return value[1 : len(value)-1], true
	}
	return "", false
}
