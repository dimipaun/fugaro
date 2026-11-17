package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/diagnoseagent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/srctree"
)

// stageDiagnose is the diagnose stage's name: the record's Stage while it
// runs, the name the log lines about it carry, and the name every
// stage-keyed reader resolves its model and its craft by (routing.go's
// stageModel and stageCraft).
const stageDiagnose = "diagnose"

// Where the diagnose step's own files live: the bundle is downloaded into the
// state directory, which is the runner's own and holds nothing the agent may
// read, and the answer is written into the checkout, where the recipe's
// outputs.globs select it and the artifact path uploads and records it. The
// four names are this program's contract rather than the repository's, the way
// the digest step's are: the catalog recipe names the two output files (T-TC3)
// and nothing else may decide them.
const (
	diagnoseBundleDir = "diagnose"
	diagnoseOutDir    = "out"
	diagnoseMDName    = "diagnosis.md"
	diagnoseJSONName  = "diagnosis.json"
)

// maxDiagnoseBundleBytes bounds the whole bundle the stage will hold. It is a
// backstop and not a policy: the launcher writes the bundle out of the
// evidence files and the changes file, which are bounded in the hundreds of
// kilobytes, so a manifest claiming more than this is one no launcher of this
// program wrote — and refusing it before the downloads is what keeps a bucket
// object from being read into the job's memory on its own say-so.
const maxDiagnoseBundleBytes int64 = 8 << 20

// diagnoseSource is srctree.Ensure: the fetch of the Fugaro source at the
// version the bundle names (D2404). It is a variable because a test may not
// fetch a tag over the network (the repository's hermetic-test rule), and
// because it is the one call of this stage that leaves the machine.
var diagnoseSource = srctree.Ensure

// runDiagnose is the diagnose stage: the model stage a mode: artifact recipe
// holding the diagnose step runs in place of render (D2402). It reads the
// bundle the launcher left under the run's own prefix, checks every part
// against the manifest before any model call, fetches the source tree the
// answer is about, builds the prompt with the evidence fenced under a nonce of
// this diagnosis's own, asks the model once through the gateway under the
// run's own lease, and writes the answer's two files into the checkout for the
// artifact path to upload.
//
// The first return value is whether the stage itself failed: r.stage records
// that failure — its reason and its class — on its own, and the caller ends the
// run from what it recorded rather than from a sentence built here
// (artifactMode's own arm, the way the plan stage already ends). The second is
// this function's own error, which is a refusal the run must make before the
// stage: a bundle that does not match its manifest, a source that could not be
// fetched, an answer that could not be read.
func (r *run) runDiagnose(ctx context.Context) (stageFailed bool, err error) {
	b, parts, err := r.readDiagnoseBundle(ctx)
	if err != nil {
		return false, err
	}
	if h := r.haltValue(); h != nil {
		// A halt that landed while the bundle was read: the stage never ran,
		// and the run must not spend a model call under it. The caller's halt
		// arm ends the run from the same value.
		return false, nil
	}
	srcDir, rec, err := r.diagnoseSourceTree(ctx, b)
	if err != nil {
		return false, err
	}
	nonce := diagnoseagent.NewNonce()
	prompt := diagnoseagent.PromptBundle(diagnoseagent.Evidence{
		RunID:      b.RunID,
		Command:    b.Command,
		Version:    b.FugaroVersion,
		SourceNote: diagnoseSourceNote(rec),
		SourceDir:  srcDir,
		Nonce:      nonce,
	}, parts)
	r.rec.Stage = stageDiagnose
	r.save(ctx)
	// The stage's agent gets the three reading tools, no settings source of the
	// machine's and no MCP server, exactly as the triage judge does: nothing
	// here writes, runs or fetches anything, and the evidence it is handed is
	// data (F5). Its working directory is the source tree the job fetched,
	// which is the only thing on this container it is asked to read — the
	// bundle's own text is in the prompt, not on disk, and it stays there.
	res, ok, err := r.stage(ctx, stageDiagnose, agent.Request{
		Prompt:          prompt,
		SessionID:       agent.NewSessionID(),
		Tools:           []string{"Read", "Grep", "Glob"},
		SettingSources:  []string{},
		StrictMCPConfig: true,
	}, stageOpts{Dir: srcDir})
	r.countTokens(res)
	switch {
	case err != nil:
		return false, err
	case !ok:
		return true, nil
	}
	answer, err := diagnoseagent.ReadAnswer(res.Text, nonce, r.secretList())
	if err != nil {
		return false, err
	}
	return false, r.writeDiagnosisAnswer(answer, b)
}

// diagnoseModel is the model the diagnose stage runs: the same key the local
// tier reads (D2403's reading of D1607), resolved the same way
// (internal/cli's troubleshootModel), so a diagnosis comes from the same model
// whichever tier answered — the repository's own agent.models.diagnose, else
// the project layer's default for it, else Fugaro's own choice.
//
// It deliberately does not fall back on a role slot, as a stage's own model
// otherwise does: a diagnosis is a few calls of a model that reads code and
// logs, and it must not follow a setting that moves every run.
func (r *run) diagnoseModel() string {
	if m := r.cfg.Agent.Models.Diagnose; m != "" {
		return m
	}
	if l := r.cfg.Layer; l != nil {
		if m := l.Defaults.Agent.Models.Diagnose; m != "" {
			return m
		}
	}
	return config.DefaultDiagnoseModel
}

// readDiagnoseBundle downloads the manifest and every part it names from the
// run's own prefix, and hands back each part's text. Everything the manifest
// says is checked before the model is asked anything (D2404): the version this
// runner reads, each part's name, and each part's bytes and SHA-256 as the
// download writes it. A run whose bundle does not match its manifest therefore
// makes no model call at all.
//
// The bucket is read with the run's own account, which cannot list: a part the
// bucket does not hold answers 403 and is reported as a part that is not there
// rather than as a permissions problem, the way the attached-input download
// reports it (blobx.GetFile's non-strict arm).
func (r *run) readDiagnoseBundle(ctx context.Context) (diagnoseagent.Bundle, []diagnoseagent.BundleText, error) {
	var none diagnoseagent.Bundle
	if r.d.Bucket == nil {
		return none, nil, errors.New("this recipe's diagnose step reads the bundle its launcher left under the run's own prefix, and this run has no runs bucket to read it from")
	}
	dir := filepath.Join(r.d.StateDir, diagnoseBundleDir)
	if err := os.RemoveAll(dir); err != nil {
		return none, nil, fmt.Errorf("emptying the diagnosis bundle's directory %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return none, nil, fmt.Errorf("creating the diagnosis bundle's directory %s: %w", dir, err)
	}
	prefix := r.d.Store.Prefix()
	manifestPath := filepath.Join(dir, diagnoseagent.BundleManifestName)
	manifestKey := runstore.InputKey(prefix, diagnoseagent.PartObjectName(diagnoseagent.BundleManifestName))
	// No expected hash for the manifest: it is the bundle's root of trust, and
	// it is the document every other part is checked against. Its size is
	// bounded, so an object that is not a manifest is refused rather than read
	// whole.
	if _, _, err := r.d.Bucket.GetFile(ctx, manifestKey, manifestPath, "", diagnoseagent.BundleManifestMaxBytes, false); err != nil {
		return none, nil, diagnoseBundleObjectErr("the diagnosis bundle's manifest", manifestKey, err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return none, nil, fmt.Errorf("reading the diagnosis bundle's manifest: %w", err)
	}
	b, err := diagnoseagent.ReadBundle(data)
	if err != nil {
		return none, nil, err
	}
	parts := make([]diagnoseagent.BundleText, 0, len(b.Parts))
	var total int64
	for _, p := range b.Parts {
		total += p.Bytes
		if total > maxDiagnoseBundleBytes {
			return none, nil, fmt.Errorf("the diagnosis bundle's parts claim %d bytes together, over the %d this runner reads", total, maxDiagnoseBundleBytes)
		}
		key := runstore.InputKey(prefix, diagnoseagent.PartObjectName(p.Name))
		dest := filepath.Join(dir, filepath.FromSlash(p.Name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return none, nil, fmt.Errorf("creating the directory for the bundle's part %q: %w", p.Name, err)
		}
		// The expected hash and the expected size are the manifest's own, and
		// both are checked on the download's own temporary, before the rename
		// (blobx.GetFileExactly): what lands at dest is the file the manifest
		// described or nothing at all, so the prompt cannot carry a part that
		// changed in the bucket between the launch and this read (D2404). The
		// size is checked beside the hash because it is a claim of its own —
		// a manifest that named the hash of a shorter file would otherwise be
		// served that file.
		if _, _, err := r.d.Bucket.GetFileExactly(ctx, key, dest, p.SHA256, p.Bytes, false, false); err != nil {
			if errors.Is(err, blobx.ErrNotExist) {
				return none, nil, fmt.Errorf("the diagnosis bundle names a part %q that is not in this run's bucket (%s)", p.Name, key)
			}
			return none, nil, fmt.Errorf("downloading the diagnosis bundle's part %q: %w", p.Name, err)
		}
		text, err := os.ReadFile(dest)
		if err != nil {
			return none, nil, fmt.Errorf("reading the diagnosis bundle's part %q: %w", p.Name, err)
		}
		parts = append(parts, diagnoseagent.BundleText{Name: p.Name, Text: string(text)})
	}
	r.d.Log.Info("the diagnosis bundle was read", "parts", len(parts), "bytes", total)
	return b, parts, nil
}

// diagnoseBundleObjectErr turns one failed bundle read into the run's own
// sentence: a part the run's account cannot read and a part that is not there
// are the same answer from the bucket, and the run says what it was looking for
// rather than reporting an IAM problem the owner would chase for nothing.
func diagnoseBundleObjectErr(what, key string, err error) error {
	if errors.Is(err, blobx.ErrNotExist) {
		return fmt.Errorf("%s is not in this run's bucket (%s): %w", what, key, err)
	}
	return fmt.Errorf("reading %s (%s): %w", what, key, err)
}

// diagnoseSourceTree fetches the Fugaro source the bundle names, with the same
// function and the same tag archive the CLI that collected the evidence used
// (D2404): the bundle carries the version and the commit, never the tree's
// bytes, so the job reads the same code either way.
func (r *run) diagnoseSourceTree(ctx context.Context, b diagnoseagent.Bundle) (string, srctree.Record, error) {
	dir, rec, err := diagnoseSource(ctx, srctree.Options{
		Version: b.SourceVersion,
		Commit:  b.SourceCommit,
		Getenv:  os.Getenv,
		GOOS:    runtime.GOOS,
	})
	if err != nil {
		return "", srctree.Record{}, fmt.Errorf("fetching the Fugaro source at %s: %w", b.SourceVersion, err)
	}
	return dir, rec, nil
}

// diagnoseSourceNote is D1611's sentence for a tree that could not be tied to
// the version that failed. The local tier says the same thing about its own
// build (internal/cli's troubleshoot note); here the tree's own record is what
// is known, so the sentence names it.
func diagnoseSourceNote(rec srctree.Record) string {
	if !rec.MayNotMatch {
		return ""
	}
	return fmt.Sprintf("the failed command's version is not tied to a release tag, so the source tree is the newest release (%s) and may not be the code that failed", rec.Version)
}

// writeDiagnosisAnswer writes the answer's two files into the checkout, where
// the recipe's outputs.globs select them: the answer as a person reads it, and
// the same answer as this program reads it back. collectArtifacts uploads and
// records them straight after this, so the run ends succeeded/artifacts with
// the two files named and hashed in its record (D2402), and the tier reads the
// answer back out of the bucket (D2410).
//
// The directory is emptied and recreated and each file is created
// O_EXCL|O_NOFOLLOW after its own path is cleared, the shape every other write
// this runner makes into a directory an agent can reach has (writeArtifactNames,
// the staging copy). The checkout is the repository's — the plan stage that
// recipe.Parse allows before this step writes in it too — so a symlink at out,
// out/diagnosis.md or out/diagnosis.json would otherwise choose what the
// model's answer is written through, anywhere the runner user can write, and
// the artifact path would then refuse the link and record an answer missing the
// file a person reads while the run still ended succeeded/artifacts. An out
// that is itself a link out of the checkout is refused before anything is
// created (outputsRootAbs), and a file that cannot be created this way fails the
// run.
func (r *run) writeDiagnosisAnswer(a diagnoseagent.Answer, b diagnoseagent.Bundle) error {
	// Measured the way the artifact path measures its own outputs root, so a
	// link standing in for out is refused here rather than followed. A root
	// that is not there yet is no link: the recipe's globs have matched
	// nothing, and this stage is what creates it.
	if _, err := r.outputsRootAbs(diagnoseOutDir); err != nil {
		return err
	}
	dir := filepath.Join(r.d.WorkDir, diagnoseOutDir)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("emptying %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	js, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("writing the diagnosis answer as JSON: %w", err)
	}
	for _, f := range []struct {
		name string
		body []byte
	}{
		{diagnoseJSONName, append(js, '\n')},
		{diagnoseMDName, []byte(diagnosisMarkdown(a, b))},
	} {
		path := filepath.Join(dir, f.name)
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("clearing %s: %w", path, err)
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, 0o644)
		if err != nil {
			return fmt.Errorf("creating %s: %w", path, err)
		}
		if _, err := out.Write(f.body); err != nil {
			out.Close()
			return fmt.Errorf("writing %s: %w", path, err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// diagnosisMarkdown is the answer as a person reads it: what the diagnosis was
// about, the two lines the machine reads, and the answer itself. The heading
// fields are the bundle's own — the command, the run and the version the
// launcher wrote — one line each, so a reader who opens the file out of the
// bucket knows what it explains.
func diagnosisMarkdown(a diagnoseagent.Answer, b diagnoseagent.Bundle) string {
	var sb []byte
	add := func(s string) { sb = append(sb, s...) }
	add("# Diagnosis\n\n")
	add("- command: `fugaro " + safetext.Field(b.Command) + "`\n")
	add("- run: " + safetext.Field(b.RunID) + "\n")
	add("- version: " + safetext.Field(b.FugaroVersion) + "\n")
	add("- outcome: " + safetext.Field(a.Outcome) + "\n")
	add("- signature: " + safetext.Field(a.Signature) + "\n\n")
	add(a.Text)
	if a.Text != "" && !strings.HasSuffix(a.Text, "\n") {
		add("\n")
	}
	return string(sb)
}
