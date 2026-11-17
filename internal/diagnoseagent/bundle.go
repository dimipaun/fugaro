package diagnoseagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

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
	// BundleMaxBytes bounds the whole bundle — every part together — and it is
	// one spelling because two binaries hold it: the launcher refuses to upload
	// a bundle over it, and the runner refuses to read one, both before a byte
	// moves. The launcher judging it is what keeps the two ends from
	// disagreeing: the collector's own cap is larger (a log is kept to its last
	// maxLogBytes), so without the check a failed command with a log many
	// megabytes long would be uploaded and then refused before any model call,
	// spending the run's bucket on evidence no answer comes back for (D2404).
	BundleMaxBytes = 8 << 20
	// AnswerName and AnswerMarkdownName are the two files the job's diagnose
	// step writes and the launcher reads back out of the run's artifacts
	// (D2402, D2410): the document a command parses, and the markdown a person
	// opens. They are this program's own contract rather than the repository's
	// — the step's stage writes them, the catalog recipe's outputs name them —
	// and they are spelled here because the writer and the reader are two
	// binaries.
	AnswerName         = "diagnosis.json"
	AnswerMarkdownName = "diagnosis.md"
)

// BundleRedaction is the bundle's own statement of what was done to the
// evidence before it left the machine, carried in the manifest because the job
// reads the files and not the launcher's code (F5, D2404). It is a statement
// and not a promise this package keeps: the passes are the collector's, and
// what makes it true is that the only files uploaded are the ones it wrote.
const BundleRedaction = "Every part of this bundle is a file the local diagnosis had already written through both redaction passes " +
	"(the values this diagnosis knows, and the shapes nobody named), and the source is a reference to the tree rather than the tree: " +
	"its version, its commit and the digest of its files."

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
	// SourceTree is the digest of the launcher's own copy of the source, from
	// TreeDigest: the tree goes up as a reference and never as bytes, so this
	// is what a reader on the other side has to compare its own copy against
	// (D2404). It is "" for a tree whose digest could not be taken, which is a
	// manifest read with less to check and not one refused.
	SourceTree string `json:"source_tree"`
	// Redaction is BundleRedaction as the launcher wrote it: the manifest's
	// own account of what was done to the parts before they left the machine.
	Redaction string `json:"redaction"`
}

// Validate checks the manifest's own rules: a version this build reads, at
// least one part, a name that is a name, a size that is a size, and a SHA-256
// for every part.
//
// It is one function called from both ends — WriteBundle refuses to write a
// manifest Validate would not accept, and ReadBundle refuses to hand one on —
// so a bundle one end would refuse is never uploaded by the other, and a
// manifest that fails here is a refusal in the command that could still say
// why rather than in a job's log.
//
// The part names go through task.InputNameProblem over the whole object name
// (BundlePrefix+name), the rule the runner's own input download applies and
// the rule blobx's GetFile is given: a name that would leave the run's prefix,
// or is absolute, or holds a NUL, is refused here rather than handed to a
// downloader that trusts its caller. The manifest's own name is refused as a
// part for the same reason at one remove: a reader downloading a part named
// after it would write over the list it is reading.
func (b Bundle) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if b.Version != BundleVersion {
		bad("the diagnosis bundle's manifest is version %d, and this build reads version %d: a bundle another release wrote is refused rather than guessed at, because the fields are a wire shape both sides hold", b.Version, BundleVersion)
	}
	if len(b.Parts) == 0 {
		bad("the diagnosis bundle's manifest names no parts: a diagnosis with no evidence to read has nothing to answer about")
	}
	seen := make(map[string]bool, len(b.Parts))
	for _, p := range b.Parts {
		if problem := task.InputNameProblem(BundlePrefix + p.Name); problem != "" {
			bad("the diagnosis bundle names a part %q that %s", p.Name, problem)
			continue
		}
		if p.Name == BundleManifestName {
			bad("the diagnosis bundle names %s as one of its own parts: the manifest is the bundle's account of itself, and a reader that downloaded a part under that name would write over the list it is reading", BundleManifestName)
			continue
		}
		if seen[p.Name] {
			bad("the diagnosis bundle names the part %q twice; one file cannot be two", p.Name)
		}
		seen[p.Name] = true
		if p.Bytes <= 0 {
			bad("the diagnosis bundle's part %q claims %d bytes", p.Name, p.Bytes)
		}
		if !sha256RE.MatchString(p.SHA256) {
			bad("the diagnosis bundle's part %q carries no SHA-256: %q is not 64 lowercase hex digits, and a part nothing can be checked against is a part the run would read whatever the bucket holds", p.Name, p.SHA256)
		}
	}
	return errors.Join(errs...)
}

// ReadBundle parses and checks a bundle's manifest: the strict codec every
// wire shape in this program uses (an unknown field is an error), the version
// this build reads, and every part's name, size and digest. Nothing it
// returns has been downloaded yet; what it refuses, it refuses before a key
// is built or a model is called.
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
	if err := b.Validate(); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

// WriteBundle writes the manifest the launcher uploads, refusing a bundle no
// reader would accept: it is the other half of ReadBundle, and the two ends
// are different binaries, so a manifest that fails Validate is one no job
// should be handed. Writing it anyway would put the refusal in the job's log
// instead of in the command that could still do something about it.
func WriteBundle(b Bundle) ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("writing the diagnosis bundle's manifest: %w", err)
	}
	return append(data, '\n'), nil
}

// PartObjectName is the leaf, under a run's inputs, that one part is stored
// at: runstore.InputKey(prefix, PartObjectName(name)). It is one spelling
// because the launcher's upload and this read must agree about it, and the
// name is validated by ReadBundle before this is ever called on it.
func PartObjectName(name string) string { return BundlePrefix + name }

// EvidenceNames is the four files Collect writes into the evidence folder of
// the run id, in the order it writes them: the failed command's log, the
// doctor's output, the version, and the effective configuration. The cloud
// tier uploads exactly these (D2404), and it asks for them by name rather than
// listing the folder, because the folder also holds what the CLI wrote beside
// them — the changes file and, for a report-carrying outcome, the report.
func EvidenceNames(id string) []string {
	return []string{id + logSuffix, doctorFileName, versionFileName, configFileName}
}

// ReadAnswerDocument reads an answer as this program holds it in a file
// (D2410): the runner writes out/diagnosis.json as json.Marshal of Answer, and
// the tier reads it back out of the run's artifacts. Both ends are Fugaro, so
// the decode is the strict one — a field this build does not know is one it
// would otherwise ignore while acting on the rest, which is exactly the
// reading the field-meaning rule forbids. The outcome is the one word the
// command acts on — what a person is shown, and whether a report is written —
// so a word Fugaro does not know is refused rather than guessed at.
func ReadAnswerDocument(data []byte) (Answer, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var a Answer
	if err := dec.Decode(&a); err != nil {
		return Answer{}, fmt.Errorf("the diagnosis job's answer is not readable: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Answer{}, errors.New("the diagnosis job's answer holds more than one JSON document")
	}
	if !slices.Contains(Outcomes, a.Outcome) {
		return Answer{}, fmt.Errorf("the diagnosis job named an outcome Fugaro does not know (%q): it has to be one of %s", a.Outcome, strings.Join(Outcomes, ", "))
	}
	return a, nil
}

// TreeDigest is the digest the manifest carries in place of the source tree's
// bytes: one SHA-256 over the tree's files, so a job that fetched its own copy
// can tell whether it is reading the same tree without the megabytes being
// uploaded twice (D2404).
//
// The walk is the tree's content and nothing else: every regular file by its
// path and the digest of its bytes, every symlink by its path and the digest
// of its target, in path order, with the path spelled with forward slashes so
// two platforms agree. `.git` is skipped whole, and that is the point of the
// digest being usable at all: the two sides get their tree two different ways
// — a git checkout on the person's machine, an unpacked tag archive in the job
// — and a repository's own directory differs between them by every packfile,
// ref and index git felt like writing, none of which is source. Modes are not
// hashed either: a git archive carries the executable bit and nothing else of
// one, and a file's mode is not what a diagnosis reads.
//
// It is a reference and not a promise of identity: two trees with the same
// files have the same digest, and what a reader does with a mismatch — the
// runner's own refusal, if it refuses — is that caller's decision.
func TreeDigest(dir string) (string, error) {
	type entry struct {
		path  string
		kind  string
		value string
	}
	var entries []entry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			entries = append(entries, entry{path: rel, kind: "l", value: sha256Hex([]byte(target))})
		case d.Type().IsRegular():
			sum, err := fileDigest(p)
			if err != nil {
				return err
			}
			entries = append(entries, entry{path: rel, kind: "f", value: sum})
		}
		// Anything else — a socket, a device, a fifo — is not part of a source
		// tree and has no content to digest.
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("reading the source tree %s: %w", dir, err)
	}
	// The entries are collected and hashed in path order rather than in the
	// order the walk found them: a digest that depends on the filesystem's own
	// order is a digest two machines cannot compare.
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.path, b.path) })
	h := sha256.New()
	for _, e := range entries {
		// kind and value are fixed-width, so the path — the only free-form
		// part, and one a tree may legally spell with a newline in it — is
		// everything after them and cannot be confused with either.
		fmt.Fprintf(h, "%s:%s:%s\n", e.kind, e.value, e.path)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileDigest is one file's content digest, read in a stream: a source tree
// holds files of megabytes and the digest is taken on a machine that is
// already diagnosing a failure.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sha256Hex is the lower-case hex sha256 of b, the one spelling of a digest
// inside this package.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
