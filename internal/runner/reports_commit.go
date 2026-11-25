package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The commit-reports step is the commit run's own work (periodic-reviews R9,
// E5, D2750 and D2754). A commit-reports recipe's first step is this one
// (recipe.UsesCommitReports), and the runner -- never a model -- writes the
// stored run reports the launcher handed in (task.Spec.Reports) into the folder
// the project designates, regenerates the folder's index over every report it
// holds, commits them, and leaves the recipe's one senior review to judge the
// pull request.
//
// Three things are true at once and the code below is shaped by them:
//
//   - The bytes are the launcher's and the store's, never a model's. The
//     launcher read reports/ with the starting person's own account (D2754, so
//     no job account ever reads the store and the reports job stays its one
//     writer), uploaded each object under this run's own prefix and wrote its
//     name, size and sha256 into the task. This step downloads each object by
//     name -- the run's account cannot list, so a list is not a read it could
//     make anyway -- and what lands in the repository is the object whose bytes
//     match the hash the task declared, or nothing.
//   - The name decides where the bytes land, and the step commits the path it
//     writes. So the name is checked against the mirror's own shape before a
//     byte is written (digest.ParseMirrorPath), and the path is checked for a
//     symbolic link at every component before the file is opened
//     (reportTreePath). task.InputNameProblem is not that check and is not enough
//     for it: it admits a dot-prefixed name on purpose, because an --attach
//     input lands under the git-excluded inputs/ and cannot reach the
//     repository's own .git.
//   - Where the folder is is the owner's decision and nobody else's
//     (reports_commit on the published policy node, R14): this run commits into
//     the repository and folder the project designated or into nothing at all,
//     and a designation that is absent, that names another repository or that
//     carries a folder the path check refuses stops the run at bootstrap
//     (reportsCommitDesignation).

// reportsCommitStage is the stage the step records on the run. It is a stage
// name like "digest" or "provision": the record's own stage enum carries no
// commit step, and the stage timings' names are free strings.
const reportsCommitStage = "commit_reports"

// reportsCommitMessage is the commit this step makes, in the runner's voice:
// the reports are its work and not the model's.
const reportsCommitMessage = "fugaro: commit the stored run reports"

// reportsCommitDesignation is the project's designation for this run, read at
// bootstrap and before the step: the folder inside the checkout this run may
// write reports into, or a refusal naming the key, the file it is written in
// and the command that publishes it.
//
// The three refusals are the plan's own (R9, D2750, D2751): a project that
// designates no repository commits no report anywhere -- and that is what every
// project that never named one means, since the block is absent rather than
// empty -- a project that designates another repository is refused because this
// run stands in one repository and the store holds every repository's reports,
// and a folder the layout's own path check refuses (an absolute path, a leading
// "/", a ".." segment) would put a committed report outside the repository the
// owner designated. The repository is compared through task.Slug, the
// tree's one reading of what a repository is, so the designation and the run
// cannot disagree about it the way two spellings of "owner/name" could.
//
// The folder's check is the second reading of that rule and not its only home:
// projectpolicy refuses a bad dir when the node is decoded (ValidReportsCommit,
// which the publish door applies too), so what reaches here has already passed
// it once. It is repeated because the folder is joined into a checkout path a
// few lines below, where the cost of being wrong is a commit outside the
// repository the owner designated, and a rule read once at the far end of a
// decode is not a rule this file can see.
//
// It is called once, from bootstrap, before any stage: a run that reached its
// model stage with no designation would spend a stage and then fail to commit,
// which is the failure this refusal exists to prevent.
func (r *run) reportsCommitDesignation(ctx context.Context, name string) (string, error) {
	const unpublishable = "no report may be committed into any repository until it is set"
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return "", err
	}
	if !found || p == nil {
		return "", fmt.Errorf("recipe %s commits this project's stored run reports, and this project designates no repository for them: %s. Set reports_commit.repo (and reports_commit.dir for the folder, %s unless you name another) in %s, then publish it with `fugaro project policy publish`",
			name, unpublishable, projectpolicy.DefaultReportsCommitDir, "the owner's local project config")
	}
	repo := p.ReportsCommitRepo()
	if repo == "" {
		return "", fmt.Errorf("recipe %s commits this project's stored run reports, and this project designates no repository for them: %s. Set reports_commit.repo (and reports_commit.dir for the folder, %s unless you name another) in %s, then publish it with `fugaro project policy publish`",
			name, unpublishable, projectpolicy.DefaultReportsCommitDir, "the owner's local project config")
	}
	kind, ok := providerForSlug(r.d.Store.Slug(), r.spec.Repo)
	if !ok {
		// The slug this run reads and writes the bucket under is not one any
		// provider kind computes for its own repository: the run is refused
		// here rather than comparing a designation against a slug that names
		// no repository (the binding check of project.go says the same thing).
		return "", fmt.Errorf("recipe %s commits reports into the project's designated repository %s, and this run's slug %s is not one this tree computes for %s, so the two cannot be compared",
			name, safetext.OneLine(safetext.Strip(repo)), r.d.Store.Slug(), safetext.OneLine(safetext.Strip(r.spec.Repo)))
	}
	want, err := task.Slug(kind, repo)
	if err != nil {
		return "", fmt.Errorf("the project's designated repository for committed reports (%s) is not one this tree can name: %v", safetext.OneLine(safetext.Strip(repo)), err)
	}
	if want != r.d.Store.Slug() {
		return "", fmt.Errorf("recipe %s commits this project's stored run reports, and this project designates %s for them: this run stands in %s, and a report may be committed into the designated repository alone. Run it on the designated repository's own checkout, or change reports_commit.repo and publish it with `fugaro project policy publish`",
			name, safetext.OneLine(safetext.Strip(repo)), r.d.Store.Slug())
	}
	dir := p.ReportsCommitDir()
	if err := projectpolicy.ValidReportsCommitDir(dir); err != nil {
		return "", fmt.Errorf("the project's designated folder for committed reports (%s) is not one a report may be written under: %v. Change reports_commit.dir and publish it with `fugaro project policy publish`", dir, err)
	}
	return dir, nil
}

// reportsCommitStep is the commit run's first step: it writes the stored
// reports the launcher handed in, regenerates the index over the folder and
// commits them. It records its own stage on the record, so a commit run's
// figures show what the step cost (nothing but the downloads it made) beside
// its review's.
func (r *run) reportsCommitStep(ctx context.Context) error {
	started := r.d.Now()
	written, err := r.writeCommitReports(ctx)
	if err == nil && len(written) > 0 {
		// The commit the step just made carries the verified test run the pull
		// request's readiness is decided on (outcome.go's Decide), exactly as
		// the digest step's own commit does: no stage of this recipe could make
		// it -- the step takes implement's place and no model writes a report --
		// and without it every commit run's pull request would be a draft whose
		// reason names a test run no step of the recipe was asked for.
		r.verifyOnce(ctx, verify.KindTest, "the commit-reports step's reports")
		// The commit this step just made is the one the pull request must carry
		// and nothing else: it is recorded here, before the recipe's review
		// runs, so finalize can put the branch back to it and refuse a branch
		// that moved (digest.go's deterministicFinalize).
		if err := r.rememberDeterministicStep(ctx); err != nil {
			return err
		}
	}
	r.rec.Stages = append(r.rec.Stages, runstore.StageTiming{
		Name: reportsCommitStage, StartedAt: started.UTC(), DurationS: r.d.Now().Sub(started).Seconds(),
	})
	r.save(ctx)
	if err != nil {
		return err
	}
	if len(written) == 0 {
		// Every report the launcher handed in is already in the folder with the
		// same bytes, and the index over it is the index this step would write:
		// nothing to write, nothing to review, and no pull request to open. The
		// run ends here, the way a caught-up report run does.
		r.endWithoutPR(ctx, "every stored report is already in the folder: nothing to commit")
	}
	return nil
}

// writeCommitReports writes the handed-in reports into the designated folder,
// regenerates the index over the whole folder, and commits them, returning the
// paths it wrote.
//
// The list is the launcher's transfer bound and never a decision: this step
// compares again against the clone it made, so the bytes that reach the branch
// are the store's whatever the launch handed in, and a launcher that handed in
// everything the store holds (a stale checkout, whose folders lack reports the
// store already has) costs downloads and changes nothing.
//
// A file the folder holds and this run was not handed is left alone and named
// on the log: the store only adds and overwrites, and a run is not where a
// report is taken away. The index is regenerated over every report the folder
// holds, that file among them, so the foldera reader opens names what is there
// rather than what this run happened to write.
func (r *run) writeCommitReports(ctx context.Context) ([]string, error) {
	if len(r.spec.Reports) == 0 {
		// Nothing was handed in, and that is the launcher's ordinary answer on a
		// checkout that already holds everything: the launcher hands in every
		// stored report the folder lacks (R16), so an empty list is a launch
		// that found nothing to transfer -- an up-to-date checkout, which is
		// what a second run of a caught-up project is. It is the
		// nothing-to-commit case rather than a refusal: the step writes
		// nothing, downloads nothing and commits nothing, and the run ends the
		// way a caught-up digest run does, with no pull request to open.
		// Nothing is compared against the folder here either, because the run
		// has no list of its own to compare with -- only the store's writer
		// reads reports/ (D2754) -- and a step that wrote an index into a
		// repository it was handed nothing for would open the very pull request
		// this case exists to avoid.
		return nil, nil
	}
	if r.d.Bucket == nil {
		return nil, errors.New("the task carries stored reports, and this run has no runs bucket to download them from")
	}
	dir := r.reportsDir
	if dir == "" {
		return nil, errors.New("this run resolved no designated folder to commit reports into, so it cannot write one: the folder is the project policy's reports_commit.dir, read and checked at bootstrap")
	}
	// The name check comes first and covers every object, before a byte is
	// written or a download is made: it is the path this step commits, and a
	// name that is not a report of the mirror's shape -- a dot-prefixed one, an
	// absolute one, a name with a path in it, a report whose year and key
	// disagree -- has no place in the folder however right its bytes are
	// (digest.ParseMirrorPath).
	for _, rf := range r.spec.Reports {
		if _, _, _, ok := digest.ParseMirrorPath(rf.Name); !ok {
			return nil, fmt.Errorf("the task hands in a report named %q, which is not a report of the committed mirror's shape: the step writes %s/<slug>/<YYYY>/<key>.md for a repository's report and %s/<YYYY>/<key>.md for the project's aggregate, and it commits the path it writes, so a name outside that shape is refused before a byte is written",
				safetext.OneLine(safetext.Strip(rf.Name)), dir, dir)
		}
	}
	// Where the launcher put each object: the run's own prefix -- the one
	// prefix the run's account reads, written there by the launcher's own
	// credential (D2754) -- and the report's name under the reports prefix.
	// That name is the one this step just checked against the mirror's shape,
	// so the object a launch hands in is addressed by the very name the step
	// writes into the folder, and there is no second path to keep in step.
	prefix := r.d.Store.Prefix() + digest.ReportsPrefix + "/"
	var written []string
	handed := make(map[string]bool, len(r.spec.Reports))
	for _, rf := range r.spec.Reports {
		handed[rf.Name] = true
		rel := dir + "/" + rf.Name
		path, err := r.reportTreePath(rel)
		if err != nil {
			return nil, err
		}
		if mirrorFileMatches(path, rf.SHA256, rf.Bytes) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", filepath.Dir(filepath.FromSlash(rel)), err)
		}
		// The expected hash and the expected size are the task's own, and both
		// are checked on the download's temporary, before the rename: what lands
		// at path is the object the task described or nothing at all. The
		// replacement is the store's own rule -- a report the store overwrote
		// replaces the folder's copy -- and the path was just checked for a link
		// at every component, so the rename cannot be pointed anywhere else by
		// one (reportTreePath).
		key := prefix + rf.Name
		if _, _, err := r.d.Bucket.GetFileExactly(ctx, key, path, rf.SHA256, rf.Bytes, false, true); err != nil {
			if errors.Is(err, blobx.ErrNotExist) {
				return nil, fmt.Errorf("the task hands in a report %q that is not in this run's bucket (%s), so the step has no bytes to commit", rf.Name, key)
			}
			return nil, fmt.Errorf("downloading the report %q: %w", rf.Name, err)
		}
		written = append(written, rel)
	}
	rows, err := digest.MirrorRows(filepath.Join(r.d.WorkDir, filepath.FromSlash(dir)))
	if err != nil {
		return nil, err
	}
	r.nameUnhandedReports(dir, rows, handed)
	index := dir + "/" + digest.IndexName
	body := []byte(digest.IndexMarkdown(rows))
	path, err := r.reportTreePath(index)
	if err != nil {
		return nil, err
	}
	held, err := os.ReadFile(path)
	switch {
	case err == nil && string(held) == string(body):
		// The index is the one this step would write: a folder whose reports
		// just changed still commits, and one where nothing changed at all does
		// not.
	case errors.Is(err, fs.ErrNotExist) || err == nil:
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", filepath.Dir(filepath.FromSlash(index)), err)
		}
		if err := writeMirrorFile(path, body); err != nil {
			return nil, err
		}
		written = append(written, index)
	default:
		return nil, fmt.Errorf("reading %s: %w", index, err)
	}
	sort.Strings(written)
	if len(written) == 0 {
		return nil, nil
	}
	if _, err := r.repo.CommitAll(ctx, reportsCommitMessage); err != nil {
		return nil, fmt.Errorf("committing the reports: %w", err)
	}
	return written, nil
}

// nameUnhandedReports logs the reports the folder holds that this run was not
// handed: the store only adds and overwrites, so a run never takes a report
// away, and the ones a person or an older run put there stay where they are.
// They are named by their path -- a name this step checked against its own
// shape, never anything a file holds (G1) -- and the log is the run's own
// record of them: in the message, so that the product's own log reader prints
// them (`fugaro logs` projects a field's message and tags and no attributes),
// and beside it as the "paths" attribute, for a structured reader.
func (r *run) nameUnhandedReports(dir string, rows []digest.Row, handed map[string]bool) {
	var extra []string
	for _, row := range rows {
		if !handed[row.Rel] {
			extra = append(extra, dir+"/"+row.Rel)
		}
	}
	if len(extra) == 0 {
		return
	}
	sort.Strings(extra)
	names := strings.Join(extra, " ")
	r.d.Log.Warn("the report folder holds reports this run was not handed; they are left alone, because the store only adds and overwrites and a run never takes a report away: "+names,
		"paths", names)
}

// mirrorFileMatches reports whether the file at path already holds exactly what
// the task says a report is: a regular file of the claimed size whose sha256 is
// the claimed hash. It is what makes this step's second half cheap and honest —
// a report already in the folder is not downloaded again — and it is a read of
// the folder's own bytes, never a word the folder's file says about itself.
//
// A path that is not there, is not a regular file (a directory, a FIFO, a
// symlink: os.Stat follows one, and the component walk above has already
// refused a link at any component including this one), or cannot be read is no
// match, and the download decides.
func mirrorFileMatches(path, sum string, size int64) bool {
	if sum == "" || size < 0 {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() != size {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == strings.TrimSpace(sum)
}

// writeMirrorFile writes one generated file at a path reportTreePath has already
// checked, without following a link planted between that check and this open.
func writeMirrorFile(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|oNoFollow, 0o644)
	if err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}
