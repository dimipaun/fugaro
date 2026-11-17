package diagnoseagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/dimipaun/fugaro/internal/task"
)

// BundleVersion is the manifest format this build reads. A manifest naming
// another version is refused rather than parsed on a hope: the fields are a
// wire shape a launcher and a job image at different releases both hold, and
// a reader that guessed at a version it does not know would read a bundle the
// launcher meant something else by (D2404).
const BundleVersion = 1

// Where a diagnosis's bundle lives. The launcher writes it and the runner
// reads it back through the same two spellings, under the run's own prefix:
// runstore.InputKey(prefix, BundlePrefix+name) — runs/<slug>/<run>/inputs/
// diagnose/<name>, the --attach path (D2404, D2405). The manifest is one of
// those objects and names the rest.
const (
	// BundlePrefix is the segment, under a run's inputs, that holds the
	// bundle.
	BundlePrefix = "diagnose/"
	// BundleManifestName is the manifest's own leaf under BundlePrefix. It is
	// not a part: it is what names the parts and the hash each is checked
	// against, so it is the one object of the bundle no digest covers.
	BundleManifestName = "manifest.json"
	// BundleManifestMaxBytes bounds the manifest, which is a small document:
	// the read of it is bounded before anything is downloaded, so a bucket
	// holding a large object under that key cannot make the runner hold it.
	BundleManifestMaxBytes = 64 << 10
)

// sha256RE is one SHA-256 as the manifest spells it: 64 lowercase hex digits,
// the shape runstore.ArtifactFile and blobx both use. A digest in any other
// shape is refused rather than passed to the download, which would compare it
// against a hash it can never equal and report a mismatch that was really a
// malformed manifest.
var sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BundlePart is one file of a bundle as the manifest accounts for it: the
// name it is stored under (BundlePrefix+Name is its object name's leaf), the
// bytes it holds and the SHA-256 of those bytes. The runner checks both
// before any model call, so a part that changed in the bucket between the
// launch and the run is refused rather than diagnosed (D2404).
type BundlePart struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Bundle is the manifest one diagnosis's launcher wrote: what the evidence is
// about, the parts it holds, and where the source the answer is about comes
// from. It carries no evidence of its own — the parts do — and no source
// bytes: the job fetches the tree itself with srctree.Ensure from the version
// and commit named here, which are the same tag archive the launcher's own
// local tier reads (D2404).
//
// Every part is a file the collector wrote through both redaction passes plus
// the changes the CLI wrote beside them (F5): the bundle adds no redaction of
// its own, because it uploads exactly what the local tier already sends. The
// manifest says so in Redaction, as its own statement of the same fact, which
// the launcher writes and the runner quotes into the prompt.
type Bundle struct {
	Version       int          `json:"version"`
	RunID         string       `json:"run_id"`
	Command       string       `json:"command"`
	FugaroVersion string       `json:"fugaro_version"`
	OS            string       `json:"os"`
	Parts         []BundlePart `json:"parts"`
	SourceVersion string       `json:"source_version"`
	SourceCommit  string       `json:"source_commit"`
	SourceTree    string       `json:"source_tree"`
	Redaction     string       `json:"redaction"`
}

// ReadBundle parses and checks a bundle's manifest: the strict codec every
// wire shape in this program uses (an unknown field is an error), the version
// this build reads, and every part's name, size and digest. Nothing it
// returns has been downloaded yet; what it refuses, it refuses before a key
// is built or a model is called.
//
// The part names are checked by task.InputNameProblem over the whole object
// name (BundlePrefix+name), which is the rule the runner's own input download
// applies and the rule blobx's GetFile is given: a name that would leave the
// run's prefix, or is absolute, or holds a NUL, is refused here rather than
// handed to a downloader that trusts its caller (T-TC5's own rule for the
// upload, from the other side).
func ReadBundle(data []byte) (Bundle, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		return Bundle{}, fmt.Errorf("the diagnosis bundle's manifest cannot be read: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Bundle{}, errors.New("the diagnosis bundle's manifest holds more than one JSON document")
	}
	if b.Version != BundleVersion {
		return Bundle{}, fmt.Errorf("the diagnosis bundle's manifest is version %d, and this runner reads version %d: a bundle another release wrote is refused rather than guessed at, because the fields are a wire shape both sides hold", b.Version, BundleVersion)
	}
	if len(b.Parts) == 0 {
		return Bundle{}, errors.New("the diagnosis bundle's manifest names no parts: a diagnosis with no evidence to read has nothing to answer about")
	}
	seen := make(map[string]bool, len(b.Parts))
	for _, p := range b.Parts {
		if problem := task.InputNameProblem(BundlePrefix + p.Name); problem != "" {
			return Bundle{}, fmt.Errorf("the diagnosis bundle names a part %q that %s", p.Name, problem)
		}
		if seen[p.Name] {
			return Bundle{}, fmt.Errorf("the diagnosis bundle names the part %q twice; one file cannot be two", p.Name)
		}
		seen[p.Name] = true
		if p.Bytes <= 0 {
			return Bundle{}, fmt.Errorf("the diagnosis bundle's part %q claims %d bytes", p.Name, p.Bytes)
		}
		if !sha256RE.MatchString(p.SHA256) {
			return Bundle{}, fmt.Errorf("the diagnosis bundle's part %q carries no SHA-256: %q is not 64 lowercase hex digits, and a part nothing can be checked against is a part the run would read whatever the bucket holds", p.Name, p.SHA256)
		}
	}
	return b, nil
}

// PartObjectName is the leaf, under a run's inputs, that one part is stored
// at: runstore.InputKey(prefix, PartObjectName(name)). It is one spelling
// because the launcher's upload and this read must agree about it, and the
// name is validated by ReadBundle before this is ever called on it.
func PartObjectName(name string) string { return BundlePrefix + name }
