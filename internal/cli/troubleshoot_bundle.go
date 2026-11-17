package cli

// The cloud tier's half of `fugaro troubleshoot` that touches a bucket
// (docs/troubleshoot-cloud, D2404, D2405): the bundle goes up into the
// diagnosis run's own prefix, the answer comes back out of it, and the bundle
// is deleted once the answer has been read.
//
// What leaves the machine is what the local tier would have sent and nothing
// else (F5, D2404): the four files diagnoseagent.Collect wrote through both
// redaction passes, the changes file this command wrote beside them, and a
// manifest naming each one with the size and the SHA-256 of the bytes that
// reached the bucket. The source goes up as a reference — its version, its
// commit and the digest of its files — never as bytes: the job fetches the
// same tag archive itself with srctree.Ensure. The evidence travels the
// --attach path (runstore.InputKey, inputs/diagnose/…), so the runner's
// download, its caps and its name refusals are the ones that already exist,
// and the answer travels the artifact path, so `fugaro artifacts` reads a
// diagnosis the way it reads anything else a run delivered.
//
// Every call here runs on the credential the command already opened for its
// own project, and every object is under the run's own prefix in the person's
// own bucket.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/diagnoseagent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/srctree"
	"github.com/dimipaun/fugaro/internal/task"
)

// troubleshootBundleFile is one file of the bundle as it stands on this
// machine: the name it goes up under, and where it is read from.
type troubleshootBundleFile struct {
	name string
	path string
}

// troubleshootBundleFiles is the bundle's contents: the four evidence files,
// and the changes file when this command managed to write it — it is a note
// and not a failure when it could not (runTroubleshoot), so a bundle without
// one is an ordinary bundle and not a broken one.
//
// The four come from diagnoseagent.EvidenceNames and not from a listing of the
// evidence folder: the folder also holds what this command wrote beside them
// (the changes file, and the report of a report-carrying outcome), and a
// listing would upload whatever happens to be there — including a file a later
// release adds for a reader that is not this job.
func troubleshootBundleFiles(ev diagnoseagent.Evidence) []troubleshootBundleFile {
	files := make([]troubleshootBundleFile, 0, len(diagnoseagent.EvidenceNames(ev.RunID))+1)
	for _, name := range diagnoseagent.EvidenceNames(ev.RunID) {
		files = append(files, troubleshootBundleFile{name: name, path: filepath.Join(ev.EvidenceDir, name)})
	}
	if path := filepath.Join(ev.EvidenceDir, troubleshootChangesName); troubleshootIsFile(path) {
		files = append(files, troubleshootBundleFile{name: troubleshootChangesName, path: path})
	}
	return files
}

// troubleshootIsFile reports whether path holds a regular file. A changes file
// that is not one — a directory, a link somebody left at a predictable name —
// is not uploaded: the bundle is read from this machine and written into a
// job's checkout, and neither side has business following a name out of either
// place.
func troubleshootIsFile(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode().IsRegular()
}

// troubleshootBundleType is the content type recorded for one part. It is the
// --attach rule (mime.TypeByExtension over the name's extension, octet-stream
// when nothing knows it): a hint a reader sees and never trusts, since the
// manifest's own SHA-256 is what says the bytes are the bytes.
func troubleshootBundleType(name string) string {
	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return ct
}

// troubleshootUploadBundle writes the diagnosis's evidence and the manifest
// that names it into the run's own prefix, under inputs/diagnose/ (D2404).
//
// Each part is streamed rather than read whole — the log may be 32 MiB
// (diagnoseagent's own cap) and the upload is one pass over it — and each
// part's size and SHA-256 come back from the write itself, so the manifest
// attests to the bytes that reached the bucket and not to a file read earlier
// that could have changed since. The manifest goes up LAST: a job that finds
// one finds every part it names already there.
//
// A part whose upload fails leaves the parts before it in the bucket. They are
// the bundle of a run that never started, and they expire with the run
// (D2405): a cleanup here would be a second write on a path whose whole point
// is that the person can see what happened.
func troubleshootUploadBundle(ctx context.Context, s *runstore.Store, ev diagnoseagent.Evidence, rec srctree.Record) error {
	b := diagnoseagent.Bundle{
		Version: diagnoseagent.BundleVersion,
		RunID:   ev.RunID,
		Command: ev.Command,
		Fugaro:  ev.Version,
		OS:      runtime.GOOS + "/" + runtime.GOARCH,
		Source:  diagnoseagent.BundleSource{Version: rec.Version, Commit: rec.Commit},
		// The statement is written by the side that knows what it did: the
		// files are the collector's, and this command uploads nothing else.
		Redaction: diagnoseagent.BundleRedaction,
	}
	digest, err := diagnoseagent.TreeDigest(ev.SourceDir)
	if err != nil {
		return userErr("the source tree of %s cannot be read, so the diagnosis job cannot be told which tree to read: %v", ev.Version, err)
	}
	b.Source.Digest = digest
	for _, f := range troubleshootBundleFiles(ev) {
		// The name becomes an object key here and a path in the job's checkout
		// there, so it is judged before either exists. Both names this command
		// chooses are its own, but the rule is one function and the run ID in
		// the log's name comes from a file on this machine
		// (task.InputNameProblem, the --attach rule).
		if problem := task.InputNameProblem(f.name); problem != "" {
			return userErr("the diagnosis bundle's part %q %s, so nothing was uploaded", f.name, problem)
		}
		size, sum, err := s.PutFileFrom(ctx, runstore.InputName(diagnoseagent.BundleDir, f.name), f.path, troubleshootBundleType(f.name), "")
		if err != nil {
			if errors.Is(err, blobx.ErrExists) {
				return userErr("run %s already holds a diagnosis bundle, so this one was not uploaded: a run's inputs are written once", shortRef(s))
			}
			return remote(fmt.Errorf("uploading the diagnosis bundle's %s: %w", f.name, err))
		}
		b.Parts = append(b.Parts, diagnoseagent.BundlePart{Name: f.name, Bytes: size, SHA256: sum})
	}
	var buf bytes.Buffer
	if err := diagnoseagent.WriteBundle(&buf, b); err != nil {
		return userErr("the diagnosis bundle cannot be described: %v", err)
	}
	// Not create-only, unlike the parts, and it does not need to be: a second
	// launcher that won the same run id fails on the first part with ErrExists,
	// so nothing can reach this line twice.
	if err := s.PutFile(ctx, runstore.InputName(diagnoseagent.BundleDir, diagnoseagent.BundleManifestName), buf.Bytes(), "application/json"); err != nil {
		return remote(fmt.Errorf("uploading the diagnosis bundle's manifest: %w", err))
	}
	return nil
}

// troubleshootAnswer is the job's answer as this command reads it: the outcome
// and the signature the job wrote beside the markdown, the markdown a person
// reads, and what the run cost.
type troubleshootAnswer struct {
	Outcome   string
	Signature string
	Text      string
	// CostUSD is the run record's own figure, never the job's account of
	// itself: it is the number `fugaro runs show` and the budget ledger carry,
	// and a diagnosis that cost something else than the run that produced it
	// would be two answers to one question.
	CostUSD float64
}

// troubleshootReadAnswer reads the diagnosis job's answer back out of the
// run's own prefix: result.json, and the two files under artifacts/ that the
// record names (D2402, D2404).
//
// The record is read through readArtifactRecord — the one reader `fugaro
// artifacts` reads a run with — and each file is checked against the size and
// the SHA-256 the record gives it before a byte of it is used, so what is
// printed is what the run recorded rather than whatever is in the bucket now.
// Both files are required: a diagnosis with a document and no markdown is a
// job that answered halfway, and printing an outcome with nothing under it is
// worse than saying so.
//
// The answer is model output, and this is the second look at it (F20): the job
// that wrote it holds the project's secrets and not this machine's, so the
// values this machine knows come out here or nowhere.
func troubleshootReadAnswer(ctx context.Context, s *runstore.Store) (troubleshootAnswer, error) {
	rec, err := readArtifactRecord(ctx, s)
	if err != nil {
		return troubleshootAnswer{}, err
	}
	doc, err := troubleshootArtifact(ctx, s, rec, diagnoseagent.AnswerName)
	if err != nil {
		return troubleshootAnswer{}, err
	}
	text, err := troubleshootArtifact(ctx, s, rec, diagnoseagent.AnswerMarkdownName)
	if err != nil {
		return troubleshootAnswer{}, err
	}
	a, err := diagnoseagent.ReadAnswer(bytes.NewReader(doc))
	if err != nil {
		return troubleshootAnswer{}, remote(fmt.Errorf("run %s: %w", shortRef(s), err))
	}
	secrets := cliSecrets(getenvOS)
	return troubleshootAnswer{
		Outcome:   a.Outcome,
		Signature: diagnoseagent.RedactOutput(a.Signature, secrets),
		Text:      diagnoseagent.RedactOutput(string(text), secrets),
		CostUSD:   rec.CostUSD,
	}, nil
}

// troubleshootArtifact reads one file the record says the run delivered. The
// record's own row for the name decides which object is read and what it has
// to hash to, and a name the record does not carry is a refusal rather than an
// attempt: the object may exist, and it would be something else entirely.
//
// The row is held to the same rules `fugaro artifacts get` holds it to (a
// digest of the right shape, a size that is a size) and a row that fails one
// is refused rather than compared against: the record was written by a run
// that drove a model, so a row with no digest is a row nothing can check.
func troubleshootArtifact(ctx context.Context, s *runstore.Store, rec *runstore.Record, name string) ([]byte, error) {
	var row *runstore.ArtifactFile
	for i := range rec.Artifacts {
		if rec.Artifacts[i].Name == name {
			row = &rec.Artifacts[i]
			break
		}
	}
	if row == nil {
		return nil, userErr("run %s recorded no %s: the diagnosis job did not answer", shortRef(s), name)
	}
	if row.Bytes < 0 || !validSHA256(row.SHA256) {
		return nil, remote(fmt.Errorf("run %s recorded %s with no usable size and sha256 (bytes %d, sha256 %q), so there is nothing to check what it delivered against",
			shortRef(s), name, row.Bytes, row.SHA256))
	}
	data, err := s.ReadFile(ctx, runstore.ArtifactPrefix+name)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil, userErr("run %s recorded %s and the bucket no longer holds it: artifacts expire with the run", shortRef(s), name)
	case err != nil:
		return nil, remote(fmt.Errorf("reading %s of run %s: %w", name, shortRef(s), err))
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != row.Bytes || hex.EncodeToString(sum[:]) != row.SHA256 {
		return nil, remote(fmt.Errorf("run %s recorded %s as %d bytes with sha256 %s and the bucket holds %d bytes with sha256 %s, so its answer is not shown",
			shortRef(s), name, row.Bytes, row.SHA256, len(data), hex.EncodeToString(sum[:])))
	}
	return data, nil
}

// troubleshootDeleteBundle removes the diagnosis's bundle from the run's own
// prefix, once the answer has been read (D2405).
//
// It is best effort and it is not allowed to fail the diagnosis: the answer is
// in hand, the person is about to read it, and a bucket that will not answer
// is a reason to leave the objects for the run's lifecycle to collect (they
// expire with it) rather than to take the answer away. A failure is one line
// on warn, which the command points at its own stderr.
//
// The names to delete are read back out of the manifest, and that is not a
// detour: the bundle is the only thing that knows what was uploaded — the log
// part is named after the LOCAL run ID, which nothing else here holds — and
// the manifest is the one object that lists them. Reading it also puts the
// names through the manifest's own refusals before a key is built from them,
// which is what makes this function safe to call with a manifest that came
// back from a bucket. The manifest itself goes last: a delete that fails
// halfway leaves a bundle a reader can still account for.
func troubleshootDeleteBundle(ctx context.Context, s *runstore.Store, warn io.Writer) {
	if warn == nil {
		warn = io.Discard
	}
	b, err := troubleshootReadManifest(ctx, s)
	if err != nil {
		fmt.Fprintf(warn, "note: the diagnosis bundle in run %s was not removed: %v\n", shortRef(s), oneLine(err.Error()))
		return
	}
	names := make([]string, 0, len(b.Parts)+1)
	for _, p := range b.Parts {
		names = append(names, p.Name)
	}
	names = append(names, diagnoseagent.BundleManifestName)
	var left []string
	for _, name := range names {
		if err := s.DeleteFile(ctx, runstore.InputName(diagnoseagent.BundleDir, name)); err != nil {
			left = append(left, name)
		}
	}
	if len(left) > 0 {
		fmt.Fprintf(warn, "note: the diagnosis bundle in run %s was not removed in full: %s could not be deleted, and the run's own expiry will collect them\n",
			shortRef(s), strings.Join(left, ", "))
	}
}

// troubleshootReadManifest reads the bundle's manifest back out of the run's
// prefix. The decode is the strict one (diagnoseagent.ReadBundle): the
// manifest was written by this command, and a reader that took an unknown
// field or an unvalidated name from a bucket would be the one place where
// "what this CLI uploaded" stops being an assumption.
func troubleshootReadManifest(ctx context.Context, s *runstore.Store) (diagnoseagent.Bundle, error) {
	data, err := s.ReadFile(ctx, runstore.InputName(diagnoseagent.BundleDir, diagnoseagent.BundleManifestName))
	if err != nil {
		return diagnoseagent.Bundle{}, err
	}
	return diagnoseagent.ReadBundle(bytes.NewReader(data))
}
