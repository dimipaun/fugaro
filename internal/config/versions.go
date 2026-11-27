package config

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// CurrentVersion is the fugaro.yaml version this release writes and reads: the
// version a file reaches once the loader has lifted it, and the one
// `fugaro config example` writes.
const CurrentVersion = 1

// SupportedVersions is every fugaro.yaml version this release reads, oldest
// first, each with the release that began its deprecation window (D3422,
// D3423). It is the one place a version's support is recorded: a file below
// the oldest (or carrying no version at all) is not a fugaro.yaml this release
// reads, a file above the newest needs a newer release instead, and a file
// between the two is lifted to CurrentVersion in memory by the registered
// upgraders before anything reads it. The per-key image-age gates retire
// beside this table (D3446): a runner reads what a launcher's file says rather
// than refusing the key it was written with.
var SupportedVersions = []Version{
	{Number: 1},
}

// Version is one fugaro.yaml version and, once it has one, its deprecation
// window (D3423).
type Version struct {
	Number int
	// DeprecatedIn is the release that began this version's deprecation
	// window: the first of the two minor releases through which doctor and
	// update name a file still on it, after which a release may refuse it by
	// its own ruling. "" while the version is current.
	DeprecatedIn string
}

// OldestVersion is the oldest fugaro.yaml version this release reads: below it
// a version is a file no release supports any more, which is a refusal the
// strict decode's own version rule makes.
func OldestVersion() int { return SupportedVersions[0].Number }

// Upgrader lifts one fugaro.yaml document from the version it was written at
// to the next one, in memory: it edits the tree docTree builds (the mapping a
// file's own text decodes to, with aliases and merge keys expanded) and leaves
// it as the reader at the next version must see it. It never writes the file,
// and it is the whole cost a version bump may have on a reader that still
// supports the version below (D3422).
type Upgrader func(tree map[string]any) error

// upgraders maps each version to the upgrader that lifts a document at it to
// the next. v1 is the identity today, so the map is empty: the release that
// first changes a shape adds 1 → 2 here beside SupportedVersions' new row, and
// a version it leaves out is a step the loader simply takes with no change.
var upgraders = map[int]Upgrader{}

// upgradeVersion gives back data's document at CurrentVersion, reading the
// file's version before anything else (D3422): the registered upgraders run
// over it in memory, oldest step first, and the bytes that reach the strict
// decode are the file as this release reads it.
func upgradeVersion(data []byte) ([]byte, []Problem) {
	return upgradeTo(data, CurrentVersion)
}

// upgradeTo is upgradeVersion with the target version named, which is what
// lets a test drive the chain past today's single version.
func upgradeTo(data []byte, target int) ([]byte, []Problem) {
	v, ok := fileVersion(data)
	if !ok {
		// Not a document carrying an int version at all: the strict decode
		// refuses it in the words it always did, so the version read is never
		// the one that speaks for a broken file.
		return data, nil
	}
	if v > target {
		return nil, []Problem{{Path: "version", Message: fmt.Sprintf("is %d, newer than the %d this release reads: run `brew upgrade`", v, target)}}
	}
	if v < OldestVersion() || v == target {
		// Already read as written: current, or below every version this
		// release supports, which Validate's own version rule then refuses —
		// a file is never lifted into support it does not have.
		return data, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, yamlProblems(err)
	}
	tree, ps := docTree(&doc)
	if len(ps) > 0 {
		return nil, ps
	}
	for n := v; n < target; n++ {
		up, ok := upgraders[n]
		if !ok {
			continue
		}
		if err := up(tree); err != nil {
			return nil, []Problem{{Path: "version", Message: fmt.Sprintf("upgrading from version %d: %v", n, err)}}
		}
	}
	tree["version"] = target
	out, err := yaml.Marshal(tree)
	if err != nil {
		return nil, []Problem{{Path: "version", Message: "upgrading: " + err.Error()}}
	}
	return out, nil
}

// versionOnly is the lenient read of a fugaro.yaml's version key alone: every
// other key is ignored, so a file a newer release wrote — carrying keys this
// one does not know — still answers with its version, which is the whole point
// of reading the version first (D3422).
type versionOnly struct {
	Version int `yaml:"version"`
}

// fileVersion reads a fugaro.yaml's own version. ok is false for a document
// that carries no int version at all — an empty file, a scalar, a version that
// is not a number — and its caller then leaves the file to the strict decode.
func fileVersion(data []byte) (int, bool) {
	var only versionOnly
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&only); err != nil {
		return 0, false
	}
	return only.Version, true
}
