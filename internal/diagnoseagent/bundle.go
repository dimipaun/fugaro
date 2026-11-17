package diagnoseagent

import (
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

// The bundle is what the cloud tier of `fugaro troubleshoot` sends to the
// diagnosis job, and what the job reads before it reads anything else
// (docs/troubleshoot-cloud, D2404): the files the local tier would have sent
// anyway, a manifest naming each one with the size and the SHA-256 of the
// bytes that reached the bucket, and a REFERENCE to the Fugaro source — its
// version, its commit and the tree's digest — never the tree's bytes. The job
// fetches the tree itself with srctree.Ensure, the same function and the same
// tag archive this CLI used, so the model reads the same source either way.
//
// Nothing here redacts: the evidence files are the collector's own, written
// through both passes before they were named in a manifest (evidence.go, F5),
// and the manifest says so in one sentence rather than leaving a reader to
// assume it. What this file adds is the promise a reader can check — every
// part's digest, so a job refuses a bundle that is not the one that was
// uploaded — and the names' own refusals, because every name here becomes an
// object key and then a path in the job's checkout.
const (
	// BundleVersion is the manifest format's version. It moves when a field is
	// removed or retyped and never when one is added (the field-meaning rule),
	// and a reader refuses a version it does not know rather than reading it as
	// this one.
	BundleVersion = 1
	// BundleDir is the leaf the bundle's objects live under inside the run's
	// own inputs: runs/<slug>/<run-id>/inputs/diagnose/<name> (D2404, the
	// --attach path). The CLI that uploads it, the runner that reads it and
	// the CLI that deletes it again spell this once.
	BundleDir = "diagnose"
	// BundleManifestName is the manifest's own name among those objects. It is
	// uploaded last and deleted last: a reader that finds it knows every part
	// it names is already there.
	BundleManifestName = "manifest.json"
	// AnswerName and AnswerMarkdownName are the two files the job's diagnose
	// step writes and the CLI reads back (D2402): the document a command
	// parses, and the markdown a person reads. They are the runner's own
	// contract, as the two output names of a render step are.
	AnswerName         = "diagnosis.json"
	AnswerMarkdownName = "diagnosis.md"
)

// BundleRedaction is the bundle's own statement of what was done to the
// evidence before it left the machine, carried in the manifest because the job
// reads the files and not this file's comments (D2404). It is a statement and
// not a promise this package keeps: the passes are the collector's, and what
// makes it true is that the only files uploaded are the ones it wrote.
const BundleRedaction = "Every part of this bundle is a file the local diagnosis had already written through both redaction passes " +
	"(the values this diagnosis knows, and the shapes nobody named), and the source is a reference to the tree rather than the tree: " +
	"its version, its commit and the digest of its files."

// Bundle is one diagnosis's manifest: what the job is about to read.
type Bundle struct {
	// Version is BundleVersion. A reader refuses any other.
	Version int `json:"version"`
	// RunID is the LOCAL run ID (D1609) — the failed command's log, the run
	// the person was told to pass back — and never the cloud run's own ID,
	// which the job knows without being told.
	RunID string `json:"run_id"`
	// Command is the Fugaro command that failed ("update"), and Fugaro the
	// version that failed. OS is the platform it failed on, "goos/goarch".
	Command string `json:"command"`
	Fugaro  string `json:"fugaro_version"`
	OS      string `json:"os"`
	// Parts is the evidence, in the order the collector wrote it, and
	// changes.txt last when the CLI managed to write it. A bundle with no
	// parts is refused: there would be nothing for a diagnosis to read.
	Parts []BundlePart `json:"parts"`
	// Source is the reference the job fetches the source by, and Redaction is
	// what was done to the files above.
	Source    BundleSource `json:"source"`
	Redaction string       `json:"redaction"`
}

// BundlePart is one file of the bundle: the name it went up under, its size,
// and the SHA-256 of the bytes that reached the bucket — taken in the pass
// that wrote them, not of a file read earlier that could have changed since.
type BundlePart struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// BundleSource is the source reference: which version and commit the tree is
// of, and the digest of its files, so a job that fetched its own copy can tell
// whether it is reading the same tree (D2404).
type BundleSource struct {
	Version string `json:"version"`
	// Commit is the commit the tree was fetched by, or "" for a build with no
	// anchor (D1611): the version then names the tag, and the digest is all a
	// reader has to compare.
	Commit string `json:"commit,omitempty"`
	// Digest is TreeDigest of the tree, or "" when it could not be taken —
	// which is a manifest a job reads with less to check, not a manifest it
	// refuses.
	Digest string `json:"tree_digest,omitempty"`
}

// Answer is the job's answer as its diagnose step writes it beside the
// markdown (D2402): the outcome and the signature the command reads, so that
// what a person is shown does not depend on parsing prose. It is the same pair
// the local tier's answer block carries (parseAnswer), written as a document
// instead of as two lines of a fenced block, because the cloud tier's reader
// and writer are two processes.
type Answer struct {
	Outcome   string `json:"outcome"`
	Signature string `json:"signature,omitempty"`
}

// ReadAnswer reads the job's answer document. An unknown outcome is a refusal,
// exactly as it is for a local answer: the command decides what a person is
// shown — and whether a report is written — from that one word, so a word
// Fugaro does not know is an answer this CLI cannot act on rather than one it
// may guess at. The decode is lenient the other way round from ReadBundle: a
// newer job may add a field, and this reader ignores what it does not know.
func ReadAnswer(r io.Reader) (Answer, error) {
	var a Answer
	if err := json.NewDecoder(r).Decode(&a); err != nil {
		return Answer{}, fmt.Errorf("the diagnosis job's answer is not readable: %w", err)
	}
	if !slices.Contains(Outcomes, a.Outcome) {
		return Answer{}, fmt.Errorf("the diagnosis job named an outcome Fugaro does not know (%q): it has to be one of %s", a.Outcome, strings.Join(Outcomes, ", "))
	}
	return a, nil
}

// WriteBundle writes the manifest as JSON, refusing a bundle no reader would
// accept: a manifest that fails Validate is one no job should be handed, and
// writing it anyway would put the refusal in the job's log instead of in the
// command that could still do something about it.
func WriteBundle(w io.Writer, b Bundle) error {
	if err := b.Validate(); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

// ReadBundle reads a manifest, refusing an unknown field, an unknown version
// and any part name this package would not have written. The decode is strict
// because the two ends are different binaries: the runner in the job image is
// whatever release built it, and a field it does not know is a manifest it
// would otherwise read as though the field were not there (the field-meaning
// rule).
func ReadBundle(r io.Reader) (Bundle, error) {
	var b Bundle
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Bundle{}, fmt.Errorf("the diagnosis bundle's manifest is not readable: %w", err)
	}
	if err := b.Validate(); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

// Validate checks the manifest's own rules: a version this package writes, at
// least one part, a name that is a name, a size that is a size, and a SHA-256
// for every part. It is called on both sides — the CLI before it uploads, the
// job after it downloads — so a bundle one end would refuse is never written
// by the other.
//
// The names go through task.InputNameProblem, the ONE rule for a name that
// becomes a key under inputs/ (the --attach path). It is the same rule here
// for a reason beyond tidiness: this manifest arrives from a bucket, and the
// runner builds the path it downloads each part to out of the name in it. A
// name that walks out of the checkout — "..", an absolute path, a NUL — is
// refused before any key or path is built, which is the difference between a
// hostile manifest and a hostile manifest that wrote outside the run's own
// directory.
func (b Bundle) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if b.Version != BundleVersion {
		bad("the bundle manifest is version %d and this reader knows version %d", b.Version, BundleVersion)
	}
	if len(b.Parts) == 0 {
		bad("the bundle manifest names no parts, so there is no evidence for a diagnosis to read")
	}
	seen := make(map[string]bool, len(b.Parts))
	for _, p := range b.Parts {
		if problem := task.InputNameProblem(p.Name); problem != "" {
			bad("the bundle manifest names a part %q that %s", p.Name, problem)
			continue
		}
		if seen[p.Name] {
			bad("the bundle manifest names the part %q twice", p.Name)
		}
		seen[p.Name] = true
		if p.Bytes < 0 {
			bad("the bundle manifest gives the part %q a size of %d bytes, which is not a size", p.Name, p.Bytes)
		}
		if !bundleSHA256RE.MatchString(p.SHA256) {
			bad("the bundle manifest gives the part %q no SHA-256 (%q), so a reader cannot check the bytes it downloads", p.Name, p.SHA256)
		}
	}
	return errors.Join(errs...)
}

// bundleSHA256RE is a part's SHA256: the lower-case hex sha256 every other
// hash in a run's records uses (task.inputSHA256RE, the artifact record's).
var bundleSHA256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// EvidenceNames is the four files Collect writes into the evidence folder of
// the run id, in the order it writes them: the failed command's log, the
// doctor's output, the version, and the effective configuration. The cloud
// tier uploads exactly these (D2404), and it asks for them by name rather than
// listing the folder, because the folder also holds what the CLI wrote beside
// them — the changes file and, for a report-carrying outcome, the report.
func EvidenceNames(id string) []string {
	return []string{id + logSuffix, doctorFileName, versionFileName, configFileName}
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
