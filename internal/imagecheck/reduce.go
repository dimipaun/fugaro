package imagecheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// The two bounds on one key file. The bytes and the entries both come from a
// checkout Fugaro did not write, and the reduction feeds the key that decides
// which shared image a repository runs: a set read in part is a different set,
// and it would take another repository's key, so a file over either bound is
// refused rather than truncated. Both are far above a real lockfile — a
// repository with 200k resolved dependencies is one Fugaro's images are not
// built for — while the memory one reduction can take stays a number this
// package chose rather than one the repository does.
const (
	maxKeyFileBytes   = 8 << 20
	maxKeyFileEntries = 200_000
)

// UnkeyedReason is why a key file did not reduce to facts of a resolved set.
type UnkeyedReason string

const (
	// UnkeyedUnknownFormat: the file is not one the reduction table names and
	// not one the key layer carries without facts, so the set it states is one
	// this package cannot read — and the set is what the key would be built
	// without.
	UnkeyedUnknownFormat UnkeyedReason = "unknown-format"
	// UnkeyedUnpinned: the file is of a named format and at least one entry
	// states no version, so it does not say what would be installed.
	UnkeyedUnpinned UnkeyedReason = "unpinned"
	// UnkeyedUnreadable: the file does not parse as the format its name states,
	// or it states a field no fact can hold.
	UnkeyedUnreadable UnkeyedReason = "unreadable"
	// UnkeyedNonRegistry: the file resolves at least one package from
	// something other than its ecosystem's registry — a git, file, path or
	// alias source, a moving branch — so the versions in it are not the set the
	// registry holds, and the integrity beside them stands for an artifact the
	// registry would not serve (D618).
	UnkeyedNonRegistry UnkeyedReason = "non-registry-source"
	// UnkeyedTooLarge and UnkeyedTooManyEntries are the bounds above.
	UnkeyedTooLarge       UnkeyedReason = "too-large"
	UnkeyedTooManyEntries UnkeyedReason = "too-many-entries"
)

// UnkeyedError is a key file that does not reduce: the repository is unkeyed
// (design §2.5), so it keeps its own image per workflow and is on no shared
// one. It is the safe answer of the three — a key built from the facts beside
// a file this package could not read in full would name another repository's
// image as this one's.
type UnkeyedError struct {
	Path   string
	Reason UnkeyedReason
	// Detail names the entry or the size the reason is about, when the
	// reduction knows it.
	Detail string
}

func (e *UnkeyedError) Error() string {
	var why string
	switch e.Reason {
	case UnkeyedUnknownFormat:
		why = "it is a lock or tool file of a format the reduction table does not name"
	case UnkeyedUnpinned:
		why = "it states an entry without a version"
	case UnkeyedUnreadable:
		why = "it does not parse as the format its name states"
	case UnkeyedNonRegistry:
		why = "it resolves a package from a source that is not the ecosystem's registry"
	case UnkeyedTooLarge:
		why = fmt.Sprintf("it is larger than the %d MiB one key file may be", maxKeyFileBytes>>20)
	case UnkeyedTooManyEntries:
		why = fmt.Sprintf("it states more than the %d entries one key file may", maxKeyFileEntries)
	}
	if e.Detail != "" {
		why += ": " + e.Detail
	}
	return fmt.Sprintf("the key file %s: %s, so this repository is unkeyed (design §2.5): it keeps its own image per workflow and is on no shared one", e.Path, why)
}

// Reduce returns the facts the key file rel states about its resolved set,
// sorted and deduplicated: the package half of the key (design §2.3, §2.4).
// One row of that table per format, and the file's name is what states the
// format. Each fact is `name@version@integrity` — the integrity or checksum
// the file states for those two, which D618 made part of the key — or
// `name@version` alone when the file states none for that package.
//
// Three answers, and the third is the one that matters:
//
//   - a set of facts, for a lockfile of a named format;
//   - nil, nil, for a file on the known no-facts list: one the key layer
//     carries and that states no resolved version at all — a manifest, a
//     workspace definition, the registry and patch configuration a Node install
//     reads, a mise config. Its change is a rebuild (ImageConfigHash, the key
//     files), not another key;
//   - an *UnkeyedError for everything else. A lock or tool file, or a manifest,
//     of a format the table does not name; a requirements file with a line that
//     is not a pin; a package from a source other than the ecosystem's
//     registry; a file over a bound: each of those means "the key cannot be
//     known", and the caller keys the repository by nothing at all (§2.5). This
//     is the default and not nil, because a file this package does not
//     recognise states a set it cannot read, and reading it as "no
//     dependencies" would hand it the key of every repository whose files
//     reduce to nothing.
//
// The caller reads the file and so must bound that read itself (task C8b): the
// check below refuses a file that is already in memory, by which time the
// allocation the repository asked for has been made.
//
// It reads data alone: no network, no file of its own, and no repository code.
func Reduce(rel string, data []byte) ([]string, error) {
	if len(data) > maxKeyFileBytes {
		return nil, &UnkeyedError{Path: rel, Reason: UnkeyedTooLarge, Detail: fmt.Sprintf("%d bytes", len(data))}
	}
	if reduce, ok := reducerFor(path.Base(rel)); ok {
		return reduce(rel, data)
	}
	if keyLayerWithoutFacts(rel) {
		return nil, nil
	}
	return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnknownFormat}
}

// reducers are §2.3's rows, by the file name that states the format.
var reducers = map[string]func(string, []byte) ([]string, error){
	"package-lock.json":   reduceNPMLock,
	"npm-shrinkwrap.json": reduceNPMLock,
	"pnpm-lock.yaml":      reducePnpmLock,
	"pnpm-lock.yml":       reducePnpmLock,
	"yarn.lock":           reduceYarnLock,
	"go.sum":              reduceGoSum,
	"go.work.sum":         reduceGoSum,
	"poetry.lock":         reducePoetryLock,
	"uv.lock":             reduceUVLock,
	"Cargo.lock":          reduceCargoLock,
	"Pipfile.lock":        reducePipfileLock,
	"Gemfile.lock":        reduceGemfileLock,
	"composer.lock":       reduceComposerLock,
}

// reducerFor matches pip's requirement files by name as well as by the table:
// pip-compile writes requirements-dev.txt beside requirements.txt out of the
// same format, and a file that states a set this table names is one the table
// has to reduce rather than drop — dropping it would key the repository
// without the pins it holds.
func reducerFor(base string) (func(string, []byte) ([]string, error), bool) {
	if reduce, ok := reducers[base]; ok {
		return reduce, true
	}
	if strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
		return reduceRequirements, true
	}
	return nil, false
}

// keyLayerWithoutFacts reports whether a key file is one the reduction table
// reads nothing from. The key layer carries more than lockfiles — the install
// above the source layer reads all of them (D620) — and these are the names on
// it that state no resolved set at all: the manifests a lock is checked
// against, which state ranges rather than what they resolved to; the workspace
// definition and the registry and patch configuration a Node install reads;
// mise's own configuration, whose toolchain is §2.1's second input, read from
// image.tools and the mise configuration and not reduced here; and the Yarn
// and patch files NodeInstallPaths and PnpmPatches carry, which are a Yarn
// build's own binary, its plugins and the patches an install applies. Each
// changes the image — through ImageConfigHash and the key files — and none
// names a resolved dependency version.
//
// The list is closed on purpose, and it holds no lockfile: a lock of a format
// this table does not name cannot be reduced, and reading it as "no fact" would
// key the repository without the versions it states, so it is refused instead
// (Reduce's third answer). A manifest joins it because a manifest is the one
// file shape whose contents are ranges of a set, never the set itself.
func keyLayerWithoutFacts(rel string) bool {
	switch path.Base(rel) {
	case "package.json", "pnpm-workspace.yaml", ".npmrc", ".yarnrc", ".yarnrc.yml", ".pnpmfile.cjs",
		"mise.toml", ".mise.toml", "mise.lock", ".tool-versions",
		"go.mod", "Cargo.toml", "pyproject.toml", "Gemfile", "composer.json", "Pipfile":
		return true
	}
	switch {
	// The release, plugin and patch files a Yarn Berry repository commits,
	// under .yarn/, and the patch a pnpm patchedDependencies entry names.
	case strings.HasPrefix(rel, ".yarn/"), strings.HasSuffix(rel, ".patch"), strings.HasSuffix(rel, ".diff"):
		return true
	case miseConfig(rel):
		return true
	}
	return false
}

// miseConfig reports whether a path is one of mise's configuration documents:
// the file it reads at each of its directories, and the conf.d fragments
// beside them. A file under a mise/ directory that is not one of these — a
// task, a script — is not configuration and is not on the no-facts list.
func miseConfig(rel string) bool {
	switch rel {
	case "mise/config.toml", ".mise/config.toml", ".config/mise.toml", ".config/mise/config.toml":
		return true
	}
	for _, dir := range []string{"mise/conf.d/", ".mise/conf.d/", ".config/mise/conf.d/"} {
		if strings.HasPrefix(rel, dir) && strings.HasSuffix(rel, ".toml") {
			return true
		}
	}
	return false
}

// reducedSet is the set one key file states while it is being read: the facts
// already seen, and how many entries were read, against the entry cap.
type reducedSet struct {
	rel   string
	seen  map[string]struct{}
	count int
}

func newSet(rel string) *reducedSet {
	return &reducedSet{rel: rel, seen: map[string]struct{}{}}
}

// add records one package as the fact the file states for it: its name, the
// version it resolved to, and the integrity or checksum of the artifact that
// version was resolved to (D618) — which a lockfile that states none leaves
// out, because the fact is what the file says and not what a registry would
// say. The three are one fact string, so the pair a name and a version with a
// new integrity make is a different fact from the same pair without one.
func (s *reducedSet) add(name, version, integrity string) error {
	s.count++
	switch {
	case s.count > maxKeyFileEntries:
		return &UnkeyedError{Path: s.rel, Reason: UnkeyedTooManyEntries, Detail: fmt.Sprintf("%d entries", s.count)}
	case name == "" || version == "":
		return s.fail(UnkeyedUnpinned, fmt.Sprintf("%s@%s", name, version))
	case strings.ContainsAny(name, " \t") || strings.ContainsAny(version, " \t") || strings.ContainsAny(integrity, " \t"):
		return s.fail(UnkeyedUnreadable, fmt.Sprintf("%q@%q", name, version))
	case strings.Contains(version, "@") || hasControl(name) || hasControl(version) || hasControl(integrity):
		// The fact's fields are split from the right — a name may hold an @
		// (a scoped npm name) and a version may not, since one that does would
		// let name, version and integrity be read as different fields — and no
		// ecosystem writes a NUL in a name or any other control character in
		// either of the three.
		return s.fail(UnkeyedUnreadable, fmt.Sprintf("%q@%q", name, version))
	}
	fact := name + "@" + version
	if integrity != "" {
		fact += "@" + integrity
	}
	s.seen[fact] = struct{}{}
	return nil
}

// hasControl reports whether s holds a C0 or C1 control character or DEL. §2.4
// quotes every fact, so one holding a newline cannot forge a line of its own —
// but a name, a version or an integrity holding a control character is not
// what any ecosystem writes, and a key built from one would be a key no
// registry could have produced.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) })
}

// joinedHashes is the integrity a file states for one package: the artifact
// hashes it lists, sorted and deduplicated so that the order a file writes them
// in cannot matter, and joined, because a package with several wheels or
// several platforms has several.
func joinedHashes(list []string) string {
	sorted := slices.Sorted(slices.Values(list))
	return strings.Join(slices.Compact(sorted), ",")
}

func (s *reducedSet) fail(reason UnkeyedReason, detail string) error {
	return &UnkeyedError{Path: s.rel, Reason: reason, Detail: detail}
}

// pairs is the set as Reduce returns it: sorted bytewise, so the reduction is
// a function of the facts and not of the order a file happens to write them.
func (s *reducedSet) pairs() []string {
	return slices.Sorted(maps.Keys(s.seen))
}

// eachLine calls f for each line of data with its indentation, the count of
// its leading spaces, and its text without those spaces or the newline. f
// returns false to stop.
func eachLine(data []byte, f func(indent int, text string) bool) {
	for len(data) > 0 {
		var raw string
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			raw, data = string(data[:i]), data[i+1:]
		} else {
			raw, data = string(data), nil
		}
		raw = strings.TrimSuffix(raw, "\r")
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		if !f(indent, raw[indent:]) {
			return
		}
	}
}

// npmPackage is one entry of a package-lock's packages map. version is absent
// for a link — a workspace member or a symlink — which is not a resolved
// dependency of the repository's. resolved and integrity are the tarball npm
// took the version from and the hash it states for it, and the two together are
// what says whether the entry came from the registry at all (D618).
type npmPackage struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Resolved  string `json:"resolved"`
	Integrity string `json:"integrity"`
}

// npmLockV1 is one entry of lockfileVersion 1's nested dependencies map, the
// shape npm 6 wrote before the flat packages map. It states the same resolved
// and integrity fields as a packages entry.
type npmLockV1 struct {
	Version      string                `json:"version"`
	Resolved     string                `json:"resolved"`
	Integrity    string                `json:"integrity"`
	Dependencies map[string]*npmLockV1 `json:"dependencies"`
}

// reduceNPMLock reduces package-lock.json and npm-shrinkwrap.json, one format
// under two names.
func reduceNPMLock(rel string, data []byte) ([]string, error) {
	var doc struct {
		Packages     map[string]npmPackage `json:"packages"`
		Dependencies map[string]*npmLockV1 `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnreadable, Detail: err.Error()}
	}
	set := newSet(rel)
	switch {
	case doc.Packages != nil:
		// The map is walked in sorted order, so that the entry a refusal names
		// is a function of the lockfile and not of the order Go's map
		// iteration happened to visit it in: a repository whose key file is
		// refused must be refused by the same entry on every machine and in
		// every run, or the same file would key two different ways.
		for _, p := range slices.Sorted(maps.Keys(doc.Packages)) {
			e := doc.Packages[p]
			name, ok := npmName(p, e)
			// An entry without a version is a link to the repository's own
			// tree, not a dependency: it states nothing to install.
			if !ok || e.Version == "" {
				continue
			}
			// npm writes an alias as an entry under the alias's own path whose
			// name is the package it points at: "alias": "npm:left-pad@1.3.0"
			// locks to node_modules/alias with name left-pad. The pair beside
			// it is the aliased package's, so it says nothing about what the
			// alias resolves to today, and the entry is not the registry's
			// (D618).
			if strings.Contains(p, "node_modules/") && e.Name != "" && e.Name != name {
				return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (the entry states the name %s)", p, e.Name))
			}
			if !npmRegistrySource(e.Resolved) {
				return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (resolved from %s)", name, e.Resolved))
			}
			if err := set.add(name, e.Version, e.Integrity); err != nil {
				return nil, err
			}
		}
	case doc.Dependencies != nil:
		// lockfileVersion 1 nests each package's dependencies under it, so
		// the walk is the whole tree and every level states versions.
		var walk func(m map[string]*npmLockV1) error
		walk = func(m map[string]*npmLockV1) error {
			for _, name := range slices.Sorted(maps.Keys(m)) {
				d := m[name]
				if d == nil {
					continue
				}
				if !npmRegistrySource(d.Resolved) {
					return set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (resolved from %s)", name, d.Resolved))
				}
				if err := set.add(name, d.Version, d.Integrity); err != nil {
					return err
				}
				if err := walk(d.Dependencies); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(doc.Dependencies); err != nil {
			return nil, err
		}
	default:
		// Neither map is there, so the file is not a lockfile this format
		// describes: an empty set here would key the repository as if it had
		// no dependencies at all.
		return nil, set.fail(UnkeyedUnreadable, "no packages or dependencies")
	}
	return set.pairs(), nil
}

// npmName is the package an entry of the packages map names: the last
// node_modules segment ends the path to it, and an entry outside node_modules
// is a path the lock writes for a workspace member, whose name is the entry's
// own. The root entry ("") is the repository itself rather than a dependency.
func npmName(p string, e npmPackage) (string, bool) {
	if p == "" {
		return "", false
	}
	name := e.Name
	const segment = "node_modules/"
	if i := strings.LastIndex(p, segment); i >= 0 {
		name = p[i+len(segment):]
	}
	return name, name != ""
}

// npmRegistrySource reports whether the resolved value npm wrote for an entry
// names something the registry served. Every entry npm 6 wrote carries the
// tarball's http(s) URL, and npm 7 and later write none at all for a workspace
// member's own entry, which is the repository's tree. Everything else — git+,
// file:, link:, a bare path — is a source the registry does not hold, and a
// package from one leaves the repository unkeyed (D618). An entry that states
// no source is read as the registry's, since the lock states nothing that
// refuses it.
func npmRegistrySource(resolved string) bool {
	switch {
	case resolved == "":
		return true
	case strings.HasPrefix(resolved, "http://"), strings.HasPrefix(resolved, "https://"):
		return true
	}
	return false
}

// pnpmEntry is one packages or snapshots entry: the package its key names, and
// the integrity its resolution line states. The two maps hold the same keys —
// a snapshots entry restates a package's own dependencies and not its
// resolution — so an entry is read from whichever section states the integrity
// and the key is what the fact is keyed by.
type pnpmEntry struct {
	name, version string
	integrity     string
	// source is the non-registry source the entry resolves from, as the entry
	// or its resolution line writes it, or "" for one the registry served.
	source string
}

func (e pnpmEntry) merged(o pnpmEntry) pnpmEntry {
	if e.integrity == "" {
		e.integrity = o.integrity
	}
	if e.source == "" {
		e.source = o.source
	}
	if e.name == "" {
		e.name, e.version = o.name, o.version
	}
	return e
}

// reducePnpmLock reduces pnpm-lock.yaml. Its packages and snapshots maps hold
// one entry per resolved package, and the entries are the reduction: the
// importers' specifiers are ranges, not versions. A package's integrity is in
// its packages entry's resolution — pnpm 6 and 7 write `resolution: {integrity:
// sha512-…}` — and its source is in the key itself or in that resolution line.
func reducePnpmLock(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	entries := map[string]pnpmEntry{}
	section, sawSection, unplaced := "", false, false
	key := ""
	eachLine(data, func(indent int, text string) bool {
		if text == "" || strings.HasPrefix(text, "#") {
			return true
		}
		if indent == 0 {
			section = strings.TrimSuffix(text, ":")
			sawSection = sawSection || section == "packages" || section == "snapshots"
			key = ""
			return true
		}
		if section != "packages" && section != "snapshots" {
			return true
		}
		if indent != 2 {
			// A line deeper than an entry is the entry's body: its resolution
			// line states the integrity and the source, and the rest — engines,
			// dependencies, peer suffixes — states ranges or nothing. A line
			// deeper than that with no entry above it is a section whose entries
			// are written at an indent this does not read.
			if key == "" {
				unplaced = true
				return true
			}
			if i := strings.Index(text, "resolution:"); i == 0 {
				entries[key] = entries[key].merged(pnpmResolution(text))
			}
			return true
		}
		// The key line ends at its separator colon, and the key may hold a
		// colon of its own: an alias's key is `alias@npm:left-pad@1.3.0` and a
		// git resolution's holds a URL. So the separator is the first colon
		// followed by a space when the line states a value — pnpm writes an
		// entry that holds nothing as `key: {}` — and the line's last colon
		// otherwise.
		key = ""
		if i := strings.Index(text, ": "); i >= 0 {
			key = text[:i]
		} else if i := strings.LastIndexByte(text, ':'); i >= 0 {
			key = text[:i]
		} else {
			unplaced = true
			return true
		}
		e, ok := pnpmEntryAt(key)
		if !ok {
			unplaced = true
			return true
		}
		entries[key] = entries[key].merged(e)
		return true
	})
	// The entries are read in the order their keys sort, so the entry a refusal
	// names is a function of the lockfile rather than of the order Go's map
	// iteration happened to visit it in.
	keys := slices.Sorted(maps.Keys(entries))
	for _, k := range keys {
		if e := entries[k]; e.source != "" {
			return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (from %s)", k, e.source))
		}
	}
	for _, k := range keys {
		e := entries[k]
		if err := set.add(e.name, e.version, e.integrity); err != nil {
			return nil, err
		}
	}
	// A section this reader names, holding a line it could not place and
	// yielding no entry, is written in a shape it does not understand — an
	// indentation of its own — and the empty set would key the repository as
	// if it depended on nothing, which is the set every other such repository
	// would take too. A section with no line in it at all states no
	// dependencies and is read as that.
	if sawSection && set.count == 0 && unplaced {
		return nil, set.fail(UnkeyedUnreadable, "the packages section states no entry at the indent this reads")
	}
	return set.pairs(), nil
}

// pnpmEntryAt reads one packages or snapshots key. A key that names a source
// rather than a registry version — pnpm writes a git, file, link, patch or
// alias resolution into the key — carries that source and no name, and the
// entry is refused: its "version" is the source, and a package from one is not
// the registry's (D618).
func pnpmEntryAt(raw string) (pnpmEntry, bool) {
	for _, marker := range []string{"@npm:", "@git+", "@git:", "@file:", "@link:", "@workspace:", "@http:", "@https:", "@patch:"} {
		if strings.Contains(raw, marker) {
			return pnpmEntry{source: raw}, true
		}
	}
	name, version, ok := pnpmKey(raw)
	if !ok {
		return pnpmEntry{}, false
	}
	return pnpmEntry{name: name, version: version}, true
}

// pnpmResolution reads a resolution line's integrity and source. pnpm writes
// `resolution: {integrity: sha512-…}` for one the registry served, and a
// tarball, git, repo or directory for one it did not.
func pnpmResolution(text string) pnpmEntry {
	e := pnpmEntry{}
	body := strings.TrimSpace(strings.TrimPrefix(text, "resolution:"))
	body = strings.TrimSuffix(strings.TrimPrefix(body, "{"), "}")
	for _, field := range strings.Split(body, ",") {
		k, v, ok := strings.Cut(field, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "integrity":
			e.integrity = v
		case "git", "repo", "directory", "file", "link", "tarball", "path", "type":
			if v != "registry" {
				e.source = k + ":" + v
			}
		}
	}
	return e
}

// pnpmKey reads a packages or snapshots key. pnpm 6 wrote the registry path's
// leading slash and pnpm 7 and later write a peer suffix in parentheses; the
// name is everything before the last @, which is the separator a scoped name's
// own @ is not.
func pnpmKey(raw string) (name, version string, ok bool) {
	s := strings.Trim(strings.TrimSpace(raw), `"'`)
	s = strings.TrimPrefix(s, "/")
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = s[:i]
	}
	i := strings.LastIndexByte(s, '@')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// yarnSpec is one yarn.lock entry while it is being read: the name its header
// states, the version, the integrity the entry carries (Yarn 1's integrity
// line, Berry's checksum), and the source its resolved or resolution line
// names, which is what says whether the entry came from a registry (D618).
type yarnSpec struct {
	name, version string
	integrity     string
	source        string
	versioned     bool
}

// reduceYarnLock reduces yarn.lock, both Yarn 1's and Berry's. The two differ
// in the separator after version — a space, or a colon — and Berry writes a
// __metadata block that is the lock's own header and not an entry.
func reduceYarnLock(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	var err error
	cur, inEntry := yarnSpec{}, false
	flush := func() bool {
		if !inEntry {
			return true
		}
		inEntry = false
		// An entry that never stated a version is one this reader would
		// otherwise drop, and the set would then be short by a package the
		// file does resolve.
		if !cur.versioned || cur.version == "" {
			err = set.fail(UnkeyedUnpinned, cur.name)
			return false
		}
		if cur.source != "" {
			err = set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s@%s (resolved from %s)", cur.name, cur.version, cur.source))
			return false
		}
		if err = set.add(cur.name, cur.version, cur.integrity); err != nil {
			return false
		}
		return true
	}
	eachLine(data, func(indent int, text string) bool {
		if text == "" || strings.HasPrefix(text, "#") {
			return true
		}
		if indent == 0 {
			if !flush() {
				return false
			}
			key := strings.Trim(strings.TrimSuffix(text, ":"), `"'`)
			if i := strings.IndexByte(key, ','); i >= 0 {
				key = strings.TrimSpace(key[:i])
			}
			if name := yarnName(key); name != "__metadata" {
				cur, inEntry = yarnSpec{name: name}, true
			}
			return true
		}
		if !inEntry {
			return true
		}
		switch {
		case strings.HasPrefix(text, "version"):
			v := yarnValue(text, "version")
			if i := strings.IndexByte(v, '#'); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
			cur.version, cur.versioned = v, true
		case strings.HasPrefix(text, "integrity"):
			cur.integrity = yarnValue(text, "integrity")
		case strings.HasPrefix(text, "checksum"):
			cur.integrity = yarnValue(text, "checksum")
		case strings.HasPrefix(text, "resolved"), strings.HasPrefix(text, "resolution"):
			cur.source = yarnSource(yarnValue(text, strings.Fields(text)[0]))
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if !flush() {
		return nil, err
	}
	return set.pairs(), nil
}

// yarnValue reads a field's value: the text after the field's name, past the
// space Yarn 1 writes or the colon Berry writes, without the quotes either may
// wrap it in.
func yarnValue(text, field string) string {
	v := strings.TrimSpace(strings.TrimPrefix(text, field))
	v = strings.TrimSpace(strings.TrimPrefix(v, ":"))
	return strings.Trim(v, `"'`)
}

// yarnSource is the non-registry source a resolved or resolution line names, or
// "" for one a registry served. Berry writes the resolved package as
// `name@npm:1.3.0`, `name@patch:…` (a registry package whose artifact a patch
// file changed, and a key file of its own) or `name@workspace:…` (a member of
// the repository's own tree, which is not a dependency the registry holds but
// is part of the set); Yarn 1 writes the tarball's URL. A git, file, link,
// exec, portal or http source is one whose artifact the registry did not serve,
// and it leaves the repository unkeyed (D618).
func yarnSource(value string) string {
	if value == "" || yarnRegistrySource(value) {
		return ""
	}
	// Berry's form has the package's name before the source, and the name may
	// have an @ of its own (a scoped name), so the source is what follows the
	// last one — the same split the fact's fields take.
	if i := strings.LastIndexByte(value, '@'); i >= 0 && yarnRegistrySource(value[i+1:]) {
		return ""
	}
	return value
}

// yarnRegistrySource reports whether a resolved value, on its own, names a
// source a registry served: Yarn 1 states the tarball's URL there, Berry a
// scheme naming the registry it took the package from, and a workspace or a
// patched registry package, which are resolved from the repository's own tree
// or from a key file of its own.
func yarnRegistrySource(value string) bool {
	for _, prefix := range []string{"npm:", "patch:", "workspace:", "virtual:", "http://", "https://"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// yarnName is the package a yarn.lock entry header names: the header lists the
// ranges one resolution satisfies, and the version range begins at the last @
// (a scoped name starts with one).
func yarnName(key string) string {
	if i := strings.LastIndexByte(key, '@'); i > 0 {
		return key[:i]
	}
	return key
}

// goModule is one module's two go.sum lines: the hash of the module's own zip,
// from the line whose version has no /go.mod suffix, and the hash of its
// go.mod. A module's integrity is the zip's — the artifact the module is built
// from (D618) — and a module the file states only a /go.mod line for is one
// whose zip was never brought in, and what the file states for it is that hash.
type goModule struct {
	name, version string
	zipHash       string
	modHash       string
}

func (m goModule) integrity() string {
	if m.zipHash != "" {
		return m.zipHash
	}
	return m.modHash
}

// reduceGoSum reduces go.sum and go.work.sum. Each line is a module path, a
// version and a hash; the /go.mod line for a version is that same module, so
// the two lines are one fact rather than two — the version is the same and the
// hashes are of two different artifacts of it.
func reduceGoSum(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	modules := map[string]goModule{}
	var err error
	eachLine(data, func(indent int, text string) bool {
		if text == "" {
			return true
		}
		fields := strings.Fields(text)
		if len(fields) < 3 {
			err = set.fail(UnkeyedUnreadable, text)
			return false
		}
		name, version := fields[0], fields[1]
		// The two lines of one module are keyed by the version they share: a
		// /go.mod line states the same module at the same version, and keying it
		// by the suffix as written would make it a second, differently keyed
		// module and so a second fact.
		mod := strings.HasSuffix(version, "/go.mod")
		version = strings.TrimSuffix(version, "/go.mod")
		key := name + "@" + version
		m := modules[key]
		m.name, m.version = name, version
		if mod {
			m.modHash = fields[2]
		} else {
			m.zipHash = fields[2]
		}
		modules[key] = m
		return true
	})
	if err != nil {
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(modules)) {
		m := modules[key]
		if err := set.add(m.name, m.version, m.integrity()); err != nil {
			return nil, err
		}
	}
	return set.pairs(), nil
}

// tomlPackage is one [[package]] table of a TOML lockfile while it is being
// read: the name and version it states, the artifact hashes its own lines
// carry, and the source it came from — Cargo writes a source string, poetry a
// [package.source] table, uv an inline one.
type tomlPackage struct {
	name, version string
	hashes        []string
	source        map[string]string
	sourceString  string
}

// readTOMLPackages reads the [[package]] tables of a TOML lockfile, and
// poetry's [metadata.files] hashes, keyed by the package name they are written
// under. The three formats place the source and the hashes differently — the
// hashes are in the package's own files list or in a hash line, and the source
// is a string, a table or an inline table — and the shape of the package table
// itself is the same, so one reader serves all three.
func readTOMLPackages(data []byte) ([]tomlPackage, map[string][]string) {
	var (
		out      []tomlPackage
		metadata = map[string][]string{}
		cur      *tomlPackage
		table    string
		mdName   string
	)
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	eachLine(data, func(indent int, text string) bool {
		if text == "" || strings.HasPrefix(text, "#") {
			return true
		}
		if strings.HasPrefix(text, "[") {
			header := strings.TrimSpace(text)
			// A [package.x] table belongs to the package in hand — its
			// dependencies, its extras, its source — and ends it for no one.
			if strings.HasPrefix(header, "[package.") {
				table = strings.Trim(header, "[]")
				return true
			}
			flush()
			mdName = ""
			switch header {
			case "[[package]]":
				cur = &tomlPackage{}
				table = "package"
			case "[metadata.files]":
				table = "metadata.files"
			default:
				table = ""
			}
			return true
		}
		switch table {
		case "package":
			key, value, ok := strings.Cut(text, "=")
			if !ok {
				return true
			}
			cur.hashes = append(cur.hashes, tomlHashes(value)...)
			switch strings.TrimSpace(key) {
			case "name":
				if s, ok := tomlString(value); ok {
					cur.name = s
				}
			case "version":
				if s, ok := tomlString(value); ok {
					cur.version = s
				}
			case "source":
				if s, ok := tomlString(value); ok {
					cur.sourceString = s
				} else {
					cur.source = tomlFields(value)
				}
			case "checksum":
				if s, ok := tomlString(value); ok {
					cur.hashes = append(cur.hashes, s)
				}
			}
		case "package.source":
			key, value, ok := strings.Cut(text, "=")
			if !ok {
				return true
			}
			if s, ok := tomlString(value); ok {
				if cur.source == nil {
					cur.source = map[string]string{}
				}
				cur.source[strings.TrimSpace(key)] = s
			}
		case "metadata.files":
			// poetry before 1.8 wrote each package's files here rather than in
			// the package's own table: a name, then the list of hashes under it.
			if strings.TrimSpace(text) == "]" {
				mdName = ""
				return true
			}
			key, value, ok := strings.Cut(text, "=")
			if !ok {
				return true
			}
			if mdName == "" {
				mdName = pypiName(strings.Trim(strings.TrimSpace(key), `"'`))
			}
			metadata[mdName] = append(metadata[mdName], tomlHashes(value)...)
			if strings.Contains(value, "]") {
				mdName = ""
			}
		}
		return true
	})
	flush()
	return out, metadata
}

// reducePoetryLock reduces poetry.lock. poetry resolves from PyPI, from a
// private index it writes as a legacy source, and from git, a path or a URL,
// and only the first two are the set a registry holds: a package from any of
// the others leaves the repository unkeyed (D618). Its hashes are the artifact
// hashes of the package's wheels and sdists, written in the package's own files
// list by poetry 1.8 and later and in the [metadata.files] table before it, and
// they are the integrity the key takes.
func reducePoetryLock(rel string, data []byte) ([]string, error) {
	packages, metadata := readTOMLPackages(data)
	set := newSet(rel)
	for _, p := range packages {
		switch p.source["type"] {
		case "git", "file", "directory", "url":
			return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (%s from %s)", pypiName(p.name), p.source["type"], p.source["url"]))
		}
		hashes := append(slices.Clone(p.hashes), metadata[pypiName(p.name)]...)
		if err := set.add(pypiName(p.name), p.version, joinedHashes(hashes)); err != nil {
			return nil, err
		}
	}
	return set.pairs(), nil
}

// reduceUVLock reduces uv.lock. uv writes each package's source as an inline
// table: the index it was resolved from, the project itself (virtual or
// editable at "."), or a git, path, directory, file or URL source, and a
// package from one of those leaves the repository unkeyed (D618). Its hashes
// are its sdist's and each of its wheels', which are the integrity the key
// takes.
func reduceUVLock(rel string, data []byte) ([]string, error) {
	packages, _ := readTOMLPackages(data)
	set := newSet(rel)
	for _, p := range packages {
		for _, field := range []string{"git", "path", "directory", "file", "url"} {
			if v := p.source[field]; v != "" {
				return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (%s: %s)", pypiName(p.name), field, v))
			}
		}
		for _, field := range []string{"editable", "virtual"} {
			if v := p.source[field]; v != "" && v != "." {
				return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (%s: %s)", pypiName(p.name), field, v))
			}
		}
		if err := set.add(pypiName(p.name), p.version, joinedHashes(p.hashes)); err != nil {
			return nil, err
		}
	}
	return set.pairs(), nil
}

// reduceCargoLock reduces Cargo.lock. A package's source is the registry's
// index, written as registry+ or as the sparse protocol's sparse+, or a git
// repository; the one D618 names is the git source, which leaves the repository
// unkeyed. A package with no source at all is a path crate — the workspace's
// own, or one a path depends on — which is a tree rather than an artifact any
// registry served, and it is keyed by its name and version like the workspace
// crate §2.3 includes. A registry package's integrity is its checksum, the hash
// Cargo wrote for the published archive.
func reduceCargoLock(rel string, data []byte) ([]string, error) {
	packages, _ := readTOMLPackages(data)
	set := newSet(rel)
	for _, p := range packages {
		switch {
		case p.sourceString == "",
			strings.HasPrefix(p.sourceString, "registry+"),
			strings.HasPrefix(p.sourceString, "sparse+"):
		default:
			return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (%s)", p.name, p.sourceString))
		}
		if err := set.add(p.name, p.version, joinedHashes(p.hashes)); err != nil {
			return nil, err
		}
	}
	return set.pairs(), nil
}

// tomlString reads a TOML value that is a string. Anything else — an inline
// table, a list, a number, a version written as version.workspace — is not a
// resolved version, and the entry it is on is read as one that states none.
func tomlString(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1], true
	}
	return "", false
}

// tomlFields reads the fields of a TOML inline table, or of a lone assignment:
// the key and the value as written, without the table's braces or the value's
// quotes.
func tomlFields(value string) map[string]string {
	v := strings.TrimSpace(value)
	if !strings.HasPrefix(v, "{") {
		return nil
	}
	out := map[string]string{}
	for _, field := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(v, "{"), "}"), ",") {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return out
}

// tomlHashes are the hash values a line states: an artifact line writes one or
// more `hash = "sha256:…"` pairs, in a list of inline tables or in one.
func tomlHashes(text string) []string {
	var out []string
	for rest := text; ; {
		i := strings.Index(rest, "hash = ")
		if i < 0 {
			return out
		}
		rest = strings.TrimLeft(rest[i+len("hash = "):], " \t")
		if rest == "" || (rest[0] != '"' && rest[0] != '\'') {
			continue
		}
		j := strings.IndexByte(rest[1:], rest[0])
		if j < 0 {
			return out
		}
		out = append(out, rest[1:1+j])
		rest = rest[j+2:]
	}
}

// pipfileEntry is one entry of a Pipfile.lock section: the version pip wrote
// with the == its own requirement syntax uses, the hashes of the artifacts it
// resolved to, and the source the entry names when pip did not take it from the
// index.
type pipfileEntry struct {
	Version string   `json:"version"`
	Hashes  []string `json:"hashes"`
	Git     string   `json:"git"`
	Path    string   `json:"path"`
	File    string   `json:"file"`
}

// reducePipfileLock reduces Pipfile.lock. Its default and develop sections
// both hold the packages pip would install, so both are read (§2.3, "every
// entry"), and pip writes each version with the == its own requirement syntax
// uses. The integrity is the hashes pip writes for the artifacts the version
// resolved to (D618).
func reducePipfileLock(rel string, data []byte) ([]string, error) {
	var doc struct {
		Default map[string]pipfileEntry `json:"default"`
		Develop map[string]pipfileEntry `json:"develop"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnreadable, Detail: err.Error()}
	}
	set := newSet(rel)
	for _, section := range []map[string]pipfileEntry{doc.Default, doc.Develop} {
		// The entries are read in sorted order, so the entry a refusal names is
		// a function of the lockfile and not of Go's map iteration.
		for _, name := range slices.Sorted(maps.Keys(section)) {
			e := section[name]
			for _, source := range []string{e.Git, e.Path, e.File} {
				if source != "" {
					return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (from %s)", name, source))
				}
			}
			// A section may hold a package pip resolves from elsewhere — a
			// marker, an index it cannot name — and pip writes no version for
			// one of those.
			if !strings.HasPrefix(e.Version, "==") {
				return nil, set.fail(UnkeyedUnpinned, fmt.Sprintf("%s (%s)", name, e.Version))
			}
			if err := set.add(pypiName(name), strings.TrimPrefix(e.Version, "=="), joinedHashes(e.Hashes)); err != nil {
				return nil, err
			}
		}
	}
	return set.pairs(), nil
}

// composerPackage is one entry of composer.lock's packages or packages-dev.
// The dist and source blocks are how composer says where the package came from,
// and which of the two carries the integrity: a dist's shasum of the archive,
// or a source's reference, which for a VCS source is the commit it was
// installed at (D618).
type composerPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dist    struct {
		Type   string `json:"type"`
		Shasum string `json:"shasum"`
	} `json:"dist"`
	Source struct {
		Type      string `json:"type"`
		Reference string `json:"reference"`
	} `json:"source"`
}

// reduceComposerLock reduces composer.lock. Its packages-dev is not exempt
// (§2.3): a shared image is built for the repository's dependencies, and what
// the repository's own tests need is one of them. A version that is a branch —
// dev-main and the like — is a moving target whose reference changes under it,
// and a path repository is a tree rather than a published archive: either
// leaves the repository unkeyed (D618).
func reduceComposerLock(rel string, data []byte) ([]string, error) {
	var doc struct {
		Packages    []composerPackage `json:"packages"`
		PackagesDev []composerPackage `json:"packages-dev"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnreadable, Detail: err.Error()}
	}
	set := newSet(rel)
	for _, p := range append(doc.Packages, doc.PackagesDev...) {
		if strings.HasPrefix(p.Version, "dev-") || strings.HasSuffix(p.Version, "-dev") {
			return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (%s)", p.Name, p.Version))
		}
		if p.Dist.Type == "path" {
			return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("%s (a path repository)", p.Name))
		}
		integrity := p.Dist.Shasum
		if integrity == "" {
			integrity = p.Source.Reference
		}
		if err := set.add(p.Name, p.Version, integrity); err != nil {
			return nil, err
		}
	}
	return set.pairs(), nil
}

// gemSpecIndent is how far Bundler indents an entry under specs:. The entries
// are the specs: list's own lines, and each entry's dependencies are indented
// under it.
const gemSpecIndent = 4

// gemEntry is one entry of a Gemfile.lock specs: list, and the integrity the
// CHECKSUMS section states for it — the digest of the gem Bundler installed
// (D618). The section is written after every block, so the entries are read
// first and added once it has been.
type gemEntry struct {
	name, version string
}

// reduceGemfileLock reduces Gemfile.lock. Every block that resolves packages —
// GEM, GIT, PATH — holds a specs: list of the same shape, so the list is what
// is read rather than the block it is in; DEPENDENCIES' ranges and BUNDLED
// WITH's version are not specs. GIT is the exception, and it is D618's: a gem
// Bundler took from a git repository is one the gem server did not serve, so
// the repository is unkeyed. A PATH block names gems in the repository's own
// tree, which Bundler installs from the checkout rather than from an artifact,
// and they are keyed by their version like Cargo's path crates.
func reduceGemfileLock(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	specs := map[string]gemEntry{}
	checksums := map[string][]string{}
	var err error
	inSpecs, sawSpecs, unplaced := false, false, false
	inChecksums, inGit, sawGit := false, false, false
	gitRemote := ""
	eachLine(data, func(indent int, text string) bool {
		switch {
		case text == "":
			return true
		case indent == 0:
			inSpecs = false
			inChecksums = text == "CHECKSUMS"
			inGit = text == "GIT"
			sawGit = sawGit || inGit
			return true
		case inGit && strings.HasPrefix(text, "remote:"):
			gitRemote = strings.TrimSpace(strings.TrimPrefix(text, "remote:"))
			return true
		case inChecksums:
			name, version, digest, ok := gemChecksum(text)
			if ok {
				checksums[name+"\x00"+version] = append(checksums[name+"\x00"+version], digest)
			}
			return true
		case text == "specs:":
			inSpecs, sawSpecs = true, true
			return true
		case !inSpecs:
			return true
		case indent != gemSpecIndent:
			// Deeper than an entry is that entry's own dependency line, which
			// states the range it was resolved from rather than a resolved
			// version. At or above the entry indent it is a specs list written
			// at another indent, whose entries this reader would drop.
			if _, _, ok := gemSpec(text); ok && indent < gemSpecIndent {
				unplaced = true
			}
			return true
		}
		name, version, ok := gemSpec(text)
		if !ok {
			err = set.fail(UnkeyedUnreadable, text)
			return false
		}
		specs[name+"\x00"+version] = gemEntry{name: name, version: version}
		return true
	})
	if err != nil {
		return nil, err
	}
	if sawGit {
		return nil, set.fail(UnkeyedNonRegistry, fmt.Sprintf("a gem resolved from %s", gitRemote))
	}
	// The specs are read in sorted order, so the entry a refusal names is a
	// function of the lockfile and not of Go's map iteration.
	for _, key := range slices.Sorted(maps.Keys(specs)) {
		e := specs[key]
		if err := set.add(e.name, e.version, joinedHashes(checksums[key])); err != nil {
			return nil, err
		}
	}
	// A specs list holding an entry this reader could not place and yielding
	// no entry of its own is what an indented differently leaves, and the empty
	// set would key the repository as if it had no gems — the set every other
	// such repository would take too. A list with no line under it at all
	// states no gems and is read as that.
	if sawSpecs && set.count == 0 && unplaced {
		return nil, set.fail(UnkeyedUnreadable, "the specs list states no entry at the indent this reads")
	}
	return set.pairs(), nil
}

// gemSpec reads one entry of a Gemfile.lock specs: list, name (version).
func gemSpec(text string) (name, version string, ok bool) {
	if !strings.HasSuffix(text, ")") {
		return "", "", false
	}
	i := strings.LastIndex(text, " (")
	if i <= 0 {
		return "", "", false
	}
	return text[:i], text[i+2 : len(text)-1], true
}

// gemChecksum reads one line of a Gemfile.lock CHECKSUMS section: the spec
// Bundler wrote it for, then the digest of the gem it installed.
func gemChecksum(text string) (name, version, digest string, ok bool) {
	i := strings.LastIndexByte(text, ' ')
	if i <= 0 {
		return "", "", "", false
	}
	digest = text[i+1:]
	if !strings.Contains(digest, "=") {
		return "", "", "", false
	}
	// The checksum's version carries the platform Bundler built for —
	// nokogiri (1.16.0-x86_64-linux) — and the specs list writes it the same
	// way, so the two match by name and version as written.
	name, version, ok = gemSpec(text[:i])
	return name, version, digest, ok
}

// reduceRequirements reduces pip's requirement files, the format a workflow
// caches and the format pip-compile writes: every line is a pin
// name==version, a comment, or the hashes a pin's continuation lines carry.
//
// One line that is none of those makes the whole file unkeyed. An include
// (-r), a URL, an editable install, an index option or an unpinned name each
// means the file does not state what pip would install, so the pins beside it
// are not the repository's dependency set — and a key built from them would
// name another repository's image as this one's.
func reduceRequirements(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	var err error
	pending := ""
	eachLine(data, func(indent int, text string) bool {
		text = strings.TrimSpace(text)
		if pending != "" {
			text, pending = pending+" "+text, ""
		}
		if strings.HasPrefix(text, "#") {
			return true
		}
		if text == "" {
			return true
		}
		if i := strings.Index(text, " #"); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		// The continuation is read before anything is cut from the line: pip
		// writes a requirement's marker and its hashes together, so
		// `name==1.0 ; python_version < "3.11" \` is one pin whose hashes are
		// the lines after it, and reading the line's first field alone would
		// take the \ with it — every --hash line would then be read as a
		// requirement of its own and the file refused.
		if strings.HasSuffix(text, `\`) {
			pending = strings.TrimSpace(strings.TrimSuffix(text, `\`))
			return true
		}
		// The hashes are the integrity the file states for the pin (D618), and
		// they come after the marker when a pin has one, so they are read out of
		// the whole requirement before the marker is dropped: cutting at the ;
		// first would take them with it and key the pin as one whose artifact
		// hash the file never stated.
		var hashes, rest []string
		for _, f := range strings.Fields(text) {
			if h, ok := strings.CutPrefix(f, "--hash="); ok {
				hashes = append(hashes, h)
				continue
			}
			rest = append(rest, f)
		}
		text = strings.Join(rest, " ")
		if i := strings.IndexByte(text, ';'); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		if text == "" {
			err = set.fail(UnkeyedUnreadable, "--hash with no requirement to attach it to")
			return false
		}
		// A direct reference — `name @ https://…`, a URL, a git or file
		// requirement — is a package pip takes from somewhere the index does
		// not serve, and it leaves the repository unkeyed (D618) rather than
		// counted as a pin without a version.
		if strings.Contains(text, " @ ") || strings.Contains(text, "://") {
			err = set.fail(UnkeyedNonRegistry, text)
			return false
		}
		// What is left of a requirement is its pin and nothing else: a second
		// field is an option, an include or a marker written without its ;, and
		// each of those means the file does not state what pip would install.
		if strings.ContainsAny(text, " \t") {
			err = set.fail(UnkeyedUnpinned, text)
			return false
		}
		name, version, ok := requirementPin(text)
		if !ok {
			err = set.fail(UnkeyedUnpinned, text)
			return false
		}
		if err = set.add(pypiName(name), version, joinedHashes(hashes)); err != nil {
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if pending != "" {
		return nil, set.fail(UnkeyedUnpinned, pending)
	}
	return set.pairs(), nil
}

// requirementPin reads name==version, the one form of a requirement that
// states a version. Extras are part of the name a requirement writes and not
// part of the package's name, so they are dropped.
func requirementPin(spec string) (name, version string, ok bool) {
	i := strings.Index(spec, "==")
	if i <= 0 {
		return "", "", false
	}
	name, version = spec[:i], spec[i+2:]
	if j := strings.IndexByte(name, '['); j >= 0 {
		name = name[:j]
	}
	if strings.ContainsAny(name, "<>=!~, ") || strings.ContainsAny(version, "<>=!~*, ") {
		return "", "", false
	}
	return name, version, true
}

// pypiName is a Python distribution name as PEP 503 normalises it: lowercase,
// with runs of -, _ and . folded to one -. PyPI's own names are already
// normalised, so the pins a requirements file and a lockfile write for one
// package take one key.
func pypiName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch r {
		case '-', '_', '.':
			dash = true
			continue
		}
		if dash && b.Len() > 0 {
			b.WriteByte('-')
		}
		dash = false
		b.WriteRune(r)
	}
	return b.String()
}
