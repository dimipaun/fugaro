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

// UnkeyedReason is why a key file did not reduce to name@version pairs.
type UnkeyedReason string

const (
	// UnkeyedUnknownFormat: the file is a lock or tool file of a format the
	// reduction table does not name, so the set it states is one this package
	// cannot read — and the set is what the key would be built without.
	UnkeyedUnknownFormat UnkeyedReason = "unknown-format"
	// UnkeyedUnpinned: the file is of a named format and at least one entry
	// states no version, so it does not say what would be installed.
	UnkeyedUnpinned UnkeyedReason = "unpinned"
	// UnkeyedUnreadable: the file does not parse as the format its name states.
	UnkeyedUnreadable UnkeyedReason = "unreadable"
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

// Reduce returns the name@version pairs the key file rel states, sorted and
// deduplicated: the package half of the key (design §2.3, §2.4). One row of
// that table per format, and the file's name is what states the format.
//
// Three answers, and the third is the one that matters:
//
//   - a set of pairs, for a lockfile of a named format;
//   - nil, nil, for a file that belongs to the key layer and states no
//     resolved version: package.json, a workspace definition, the registry
//     and patch configuration a Node install reads, a mise config. Its change
//     is a rebuild (ImageConfigHash, the key files), not another key;
//   - an *UnkeyedError, for a file that states a resolved set this cannot
//     read in full. A lock or tool file of a format the table does not name,
//     a requirements file with a line that is not a pin, a file over a bound:
//     each of those means "the key cannot be known", and the caller keys the
//     repository by nothing at all (§2.5).
//
// It reads data alone: no network, no file of its own, and no repository code.
func Reduce(rel string, data []byte) ([]string, error) {
	if len(data) > maxKeyFileBytes {
		return nil, &UnkeyedError{Path: rel, Reason: UnkeyedTooLarge, Detail: fmt.Sprintf("%d bytes", len(data))}
	}
	base := path.Base(rel)
	if reduce, ok := reducerFor(base); ok {
		return reduce(rel, data)
	}
	if keyLayerWithoutFacts(base) {
		return nil, nil
	}
	if lockLike(base) || toolLike(base) {
		return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnknownFormat}
	}
	return nil, nil
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
	"poetry.lock":         reduceTOMLPackages,
	"uv.lock":             reduceTOMLPackages,
	"Cargo.lock":          reduceTOMLPackages,
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

// keyLayerWithoutFacts are the names the key layer carries for a reason other
// than a resolved set: the root and member manifests a lock is checked
// against, the workspace definition, the registry and patch configuration a
// Node install reads, and mise's own configuration. Each changes the image —
// through ImageConfigHash and the key files — and none names a dependency
// version, so none is a fact and none makes the repository unkeyed. The mise
// files are here rather than in toolLike because the toolchain is §2.1's
// second input, read from image.tools and the mise configuration, and not
// something this table reduces.
func keyLayerWithoutFacts(base string) bool {
	switch base {
	case "package.json", "pnpm-workspace.yaml", ".npmrc", ".yarnrc", ".yarnrc.yml", ".pnpmfile.cjs",
		"mise.toml", ".mise.toml", "mise.lock", ".tool-versions":
		return true
	}
	return false
}

// lockLike reports whether a name states a lockfile of some ecosystem by
// convention, so that one of a format this table does not name is refused
// rather than dropped.
func lockLike(base string) bool {
	for _, suffix := range []string{".lock", ".lockb", ".lock.json", ".lock.yaml", ".lock.yml", "-lock.json", ".sum"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// toolLike reports whether a name is a version manager's own file. mise and
// the asdf family state a toolchain there, and a toolchain is key material
// (§2.1): a tool file that is not a mise config is not one this package reads,
// so a repository that pins its toolchain outside mise is unkeyed rather than
// keyed by the packages beside it, whose image would then be built for the
// other repository's toolchain. (.nvmrc and its siblings are mise's idiomatic
// version files.)
func toolLike(base string) bool {
	switch base {
	case ".tool-versions", ".python-version", ".node-version", ".nvmrc", ".ruby-version",
		".go-version", "rust-toolchain", "rust-toolchain.toml":
		return true
	}
	return false
}

// reducedSet is the set one key file states while it is being read: the pairs
// already seen, and how many entries were read, against the entry cap.
type reducedSet struct {
	rel   string
	seen  map[string]struct{}
	count int
}

func newSet(rel string) *reducedSet {
	return &reducedSet{rel: rel, seen: map[string]struct{}{}}
}

func (s *reducedSet) add(name, version string) error {
	s.count++
	switch {
	case s.count > maxKeyFileEntries:
		return &UnkeyedError{Path: s.rel, Reason: UnkeyedTooManyEntries, Detail: fmt.Sprintf("%d entries", s.count)}
	case name == "" || version == "":
		return s.fail(UnkeyedUnpinned, fmt.Sprintf("%s@%s", name, version))
	case strings.ContainsAny(name, " \t") || strings.ContainsAny(version, " \t"):
		return s.fail(UnkeyedUnreadable, fmt.Sprintf("%q@%q", name, version))
	}
	s.seen[name+"@"+version] = struct{}{}
	return nil
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
// dependency of the repository's.
type npmPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// npmLockV1 is one entry of lockfileVersion 1's nested dependencies map, the
// shape npm 6 wrote before the flat packages map.
type npmLockV1 struct {
	Version      string                `json:"version"`
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
		for p, e := range doc.Packages {
			name, ok := npmName(p, e)
			// An entry without a version is a link to the repository's own
			// tree, not a dependency: it states nothing to install.
			if !ok || e.Version == "" {
				continue
			}
			if err := set.add(name, e.Version); err != nil {
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
				if err := set.add(name, d.Version); err != nil {
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

// reducePnpmLock reduces pnpm-lock.yaml. Its packages and snapshots maps hold
// one entry per resolved package, and the entries are the reduction: the
// importers' specifiers are ranges, not versions.
func reducePnpmLock(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	var err error
	section := ""
	eachLine(data, func(indent int, text string) bool {
		if text == "" || strings.HasPrefix(text, "#") {
			return true
		}
		if indent == 0 {
			section = strings.TrimSuffix(text, ":")
			return true
		}
		if section != "packages" && section != "snapshots" {
			return true
		}
		i := strings.IndexByte(text, ':')
		if indent != 2 || i < 0 {
			return true
		}
		name, version, ok := pnpmKey(text[:i])
		if !ok {
			err = set.fail(UnkeyedUnpinned, text)
			return false
		}
		if err = set.add(name, version); err != nil {
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return set.pairs(), nil
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

// reduceYarnLock reduces yarn.lock, both Yarn 1's and Berry's. The two differ
// in the separator after version — a space, or a colon — and Berry writes a
// __metadata block that is the lock's own header and not an entry.
func reduceYarnLock(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	var err error
	entry, inEntry := "", false
	eachLine(data, func(indent int, text string) bool {
		if text == "" || strings.HasPrefix(text, "#") {
			return true
		}
		if indent == 0 {
			inEntry = false
			key := strings.Trim(strings.TrimSuffix(text, ":"), `"'`)
			if i := strings.IndexByte(key, ','); i >= 0 {
				key = strings.TrimSpace(key[:i])
			}
			if name := yarnName(key); name != "__metadata" {
				entry, inEntry = name, true
			}
			return true
		}
		if !inEntry || !strings.HasPrefix(text, "version") {
			return true
		}
		v := strings.TrimSpace(strings.TrimPrefix(text, "version"))
		v = strings.Trim(strings.TrimSpace(strings.TrimPrefix(v, ":")), `"'`)
		if i := strings.IndexByte(v, '#'); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		inEntry = false
		if v == "" {
			err = set.fail(UnkeyedUnpinned, entry)
			return false
		}
		if err = set.add(entry, v); err != nil {
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return set.pairs(), nil
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

// reduceGoSum reduces go.sum and go.work.sum. Each line is a module path, a
// version and a hash; the /go.mod line for a version is that same module.
func reduceGoSum(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	var err error
	eachLine(data, func(indent int, text string) bool {
		if text == "" {
			return true
		}
		fields := strings.Fields(text)
		if len(fields) < 2 {
			err = set.fail(UnkeyedUnreadable, text)
			return false
		}
		if err = set.add(fields[0], strings.TrimSuffix(fields[1], "/go.mod")); err != nil {
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return set.pairs(), nil
}

// reduceTOMLPackages reduces the TOML lockfiles that hold one [[package]]
// table per resolved package: poetry's, uv's and Cargo's. A package's version
// is a literal in all three, and an entry without one — a path or git source
// poetry writes before it is resolved — means the file does not state what
// would be installed.
func reduceTOMLPackages(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	var err error
	inPackage := false
	name, version := "", ""
	flush := func() bool {
		if !inPackage {
			return true
		}
		inPackage = false
		if err = set.add(name, version); err != nil {
			return false
		}
		name, version = "", ""
		return true
	}
	eachLine(data, func(indent int, text string) bool {
		if text == "" || strings.HasPrefix(text, "#") {
			return true
		}
		if strings.HasPrefix(text, "[") {
			if !flush() {
				return false
			}
			inPackage = text == "[[package]]"
			return true
		}
		if !inPackage {
			return true
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return true
		}
		s, ok := tomlString(value)
		if !ok {
			return true
		}
		switch strings.TrimSpace(key) {
		case "name":
			name = s
		case "version":
			version = s
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

// reducePipfileLock reduces Pipfile.lock. Its default and develop sections
// both hold the packages pip would install, so both are read (§2.3, "every
// entry"), and pip writes each version with the == its own requirement syntax
// uses.
func reducePipfileLock(rel string, data []byte) ([]string, error) {
	var doc struct {
		Default map[string]struct {
			Version string `json:"version"`
		} `json:"default"`
		Develop map[string]struct {
			Version string `json:"version"`
		} `json:"develop"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnreadable, Detail: err.Error()}
	}
	set := newSet(rel)
	for _, section := range []map[string]struct {
		Version string `json:"version"`
	}{doc.Default, doc.Develop} {
		for _, name := range slices.Sorted(maps.Keys(section)) {
			v := section[name].Version
			// A section may hold a package pip resolves from elsewhere — a
			// git source, a path, a marker — and pip writes no version for
			// one of those.
			if !strings.HasPrefix(v, "==") {
				return nil, set.fail(UnkeyedUnpinned, fmt.Sprintf("%s (%s)", name, v))
			}
			if err := set.add(pypiName(name), strings.TrimPrefix(v, "==")); err != nil {
				return nil, err
			}
		}
	}
	return set.pairs(), nil
}

// reduceComposerLock reduces composer.lock. Its packages-dev is not exempt
// (§2.3): a shared image is built for the repository's dependencies, and what
// the repository's own tests need is one of them.
func reduceComposerLock(rel string, data []byte) ([]string, error) {
	var doc struct {
		Packages []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"packages"`
		PackagesDev []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"packages-dev"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, &UnkeyedError{Path: rel, Reason: UnkeyedUnreadable, Detail: err.Error()}
	}
	set := newSet(rel)
	for _, p := range append(doc.Packages, doc.PackagesDev...) {
		if err := set.add(p.Name, p.Version); err != nil {
			return nil, err
		}
	}
	return set.pairs(), nil
}

// gemSpecIndent is how far Bundler indents an entry under specs:. The entries
// are the specs: list's own lines, and each entry's dependencies are indented
// under it.
const gemSpecIndent = 4

// reduceGemfileLock reduces Gemfile.lock. Every block that resolves packages —
// GEM, GIT, PATH — holds a specs: list of the same shape, so the list is what
// is read rather than the block it is in; DEPENDENCIES' ranges and BUNDLED
// WITH's version are not specs.
func reduceGemfileLock(rel string, data []byte) ([]string, error) {
	set := newSet(rel)
	var err error
	inSpecs := false
	eachLine(data, func(indent int, text string) bool {
		switch {
		case text == "":
			return true
		case indent == 0:
			inSpecs = false
			return true
		case text == "specs:":
			inSpecs = true
			return true
		case !inSpecs, indent != gemSpecIndent:
			return true
		}
		name, version, ok := gemSpec(text)
		if !ok {
			err = set.fail(UnkeyedUnreadable, text)
			return false
		}
		if err = set.add(name, version); err != nil {
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
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
		if pending != "" {
			text, pending = pending+" "+text, ""
		}
		if i := strings.IndexByte(text, ';'); i >= 0 {
			text = text[:i]
		}
		if strings.HasPrefix(text, "#") {
			return true
		}
		if i := strings.Index(text, " #"); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return true
		}
		if strings.HasSuffix(text, `\`) {
			pending = strings.TrimSpace(strings.TrimSuffix(text, `\`))
			return true
		}
		fields := strings.Fields(text)
		for _, f := range fields[1:] {
			if !strings.HasPrefix(f, "--hash=") {
				err = set.fail(UnkeyedUnpinned, text)
				return false
			}
		}
		name, version, ok := requirementPin(fields[0])
		if !ok {
			err = set.fail(UnkeyedUnpinned, text)
			return false
		}
		if err = set.add(pypiName(name), version); err != nil {
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
