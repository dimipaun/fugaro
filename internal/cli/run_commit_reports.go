package cli

// --commit-reports is the launcher's half of the stored-reports handover
// (R16, D2754): a commit run's task carries the reports the run commits, and
// the bytes are read and uploaded here, at the person's terminal, with the
// person's own launcher credential. That is the whole point of the split the
// ruling makes: reports/ of the runs bucket is written by the project's
// reports job alone and READ by no job account at all — the run's own account
// holds no grant on it — so a run that had to read the store itself could not
// be given one without making every run a reader of every repository's
// reports. Instead the launcher copies what the run needs into the run's own
// prefix, which the run's account already reads, and the task names those
// objects (task.Spec.Reports, R9's member and this flag's write side).
//
// Nothing here decides what a report is: every object under reports/ whose
// name is a mirror name (digest.ParseMirrorPath) is handed in, and the
// comparison against the checkout's own folder is a transfer bound — a report
// the folder already holds byte for byte needs no copy — never a filter that
// could withhold one. The commit step is the judge of what belongs on the
// branch, and it refuses a name that is not a report of the mirror's shape
// before writing a byte of it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// commitReportsLaunch is everything a --commit-reports launch refuses before
// the first byte it could upload: the pair refusal, the recipe refusal and
// the image gate, in that order. It uploads nothing — commitReportsUpload
// does that last, after the gates that can still refuse this launch — so a
// launch refused here leaves no report object under a run id that never gets
// a task.
//
// The pair refusal is FIRST, and it is here rather than on the recipe because
// the person typed both flags: a report object is never a recipe input. The
// reports a commit run reads are the task's own member, uploaded under its
// own reports/ leaf, and an --attach launch would upload copies into inputs/
// where no step of that recipe reads them — under caps that would then have to
// answer for files the run cannot use. The other direction needs nothing here:
// commit-reports is mode: implement, so checkAttachRecipe already refuses
// `--recipe commit-reports --attach` with "needs an artifact recipe", before
// its own first byte (TestRunCommitReportsRefusesAttachAndTheRecipeRefusesItToo).
func commitReportsLaunch(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, clone, attach bool, warn io.Writer) error {
	if attach {
		return userErr("--commit-reports hands the project's stored reports to the run and --attach adds files to a recipe's inputs, and a report object is never a recipe input: a commit run reads the reports its task names, not files in inputs/, so this launch refuses both flags at once — pass one of them")
	}
	if err := checkCommitReportsRecipe(ctx, spec); err != nil {
		return err
	}
	if clone {
		return checkCloneCommitReportsImage(env.lc, spec, warn)
	}
	return checkCommitReportsImage(ctx, env, slug, spec, kind, warn)
}

// checkCommitReportsRecipe refuses a --commit-reports launch whose run would
// not read the reports it hands in. The pairing is task.Spec.Validate's own
// (a task carrying Reports needs a recipe with a commit_reports step), made
// here so the refusal names the fix instead of leaving a run to die at
// bootstrap with the bytes already in the bucket.
//
// Every arm fails closed. The recipe is the one the RUN will resolve, through
// recipeForLaunch — the launcher's own reading, for a repository recipe as
// much as for the text a task carries, because task.RecipeUsesCommitReports
// leaves a repo recipe's steps to "the launcher, which resolves it from the
// checkout". A launch that leaves the recipe to the runner cannot be judged
// here at all (runnerChooses), and a recipe this CLI could not read is
// refused rather than assumed: the catalog's own commit-reports is in this
// binary, so the fix is a flag and not a wait.
func checkCommitReportsRecipe(ctx context.Context, spec *task.Spec) error {
	if runnerChooses(ctx, spec) {
		return userErr("--commit-reports hands the project's stored reports to the run, but this launch leaves the recipe to the runner (agent.recipe in fugaro.yaml at %s, else default), so the CLI cannot tell whether the run it starts is a commit run; pass --recipe commit-reports", spec.Ref)
	}
	rcp := recipeForLaunch(ctx, checkoutConfig(ctx, spec.Repo), spec)
	switch {
	case rcp != nil && recipe.UsesCommitReports(rcp):
		return nil
	case rcp != nil:
		return userErr("--commit-reports hands the project's stored reports to the run, and recipe %s has no commit_reports step: a task carrying them would be refused at bootstrap, with the reports already in the bucket. Launch the commit run with --recipe commit-reports", rcp.Name)
	case spec.Recipe != nil:
		return userErr("--commit-reports needs a recipe whose first step is commit_reports, and recipe %s could not be read here — the launcher is the judge of a repository recipe's steps, so the launch is refused rather than handing reports to a run that may not read them; pass --recipe commit-reports, whose text this build's catalog carries", spec.Recipe.Name)
	default:
		return userErr("--commit-reports needs a recipe whose first step is commit_reports; pass --recipe commit-reports")
	}
}

// commitReportsRefusesStoredRun is --attach's own freshness check, read for
// the reports and for the same reason: a run id whose task.json is already
// there is refused before the first byte, whether the stored task differs
// from this one or is byte-identical to it. --commit-reports does not adopt —
// the stored task already names its reports and those objects are already in
// the bucket, so the create-only write this launch would do is a failure and
// not a no-op, and a mistyped --run-id would have written over a finished
// run's reports before anyone looked at its task.
func commitReportsRefusesStoredRun(ctx context.Context, s *runstore.Store, runID string) error {
	_, err := s.ReadTask(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil
	case err != nil:
		return remote(err)
	}
	return userErr("run ID %s already holds a task; --commit-reports uploads the stored reports into a run's own reports/, so it never hands them to an existing run — use a new run ID", runID)
}

// commitReportsUpload is the last thing a --commit-reports launch does before
// createTask, and the only write it makes that the task then names: it lists
// the project's stored reports with the launcher's own credential, reads
// every one the checkout's mirror folder does not already hold byte for byte,
// uploads it under the run's own prefix and fills in spec.Reports.
//
// It is called after every gate that can refuse the launch, so a refused
// launch leaves no report object under a run id that never gets a task. The
// objects are written create-only: a report object that is already there
// belongs to a run whose task already names it, which is a race this launcher
// lost (the freshness check above saw no task) and not a transfer to complete.
//
// The bytes that go up are the STORE's, read here, and never the checkout's
// copy of the same report: the runner compares what it is handed against the
// clone it makes, so the store is what reaches the branch, and a folder that
// holds a stale copy of a report is a reason to hand the store's bytes in and
// not a reason to commit the stale ones. What comes back from a read is what
// this function names in the task, hashed in the same pass that holds the
// bytes it wrote, so a task can never name a size or a hash that is not the
// object's.
func commitReportsUpload(ctx context.Context, env *cloudEnv, spec *task.Spec, prefix string, warn io.Writer) error {
	names, err := storedReportNames(ctx, env)
	if err != nil {
		return err
	}
	have := mirrorFolderDigests(ctx, env, spec, warn)
	url := "gs://fugaro-runs-" + env.lc.GCPProject
	leaf := prefix + digest.ReportsPrefix + "/"
	var reports []task.InputFile
	for _, name := range names {
		body, _, err := env.bucket.ReadStrict(ctx, digest.ReportsPrefix+"/"+name)
		switch {
		case errors.Is(err, blobx.ErrNotExist):
			// The store changed between the list and this read: reports/ is
			// a writer's folder, and an object a later report run replaced or
			// removed is not this run's to hand in.
			continue
		case err != nil:
			return bucketErrFor(url, "reading the stored report "+name, "the project's stored reports", err)
		}
		sum := sha256.Sum256(body)
		hexsum := hex.EncodeToString(sum[:])
		if have[name] == hexsum {
			continue
		}
		if _, err := env.bucket.Create(ctx, leaf+name, body, "text/markdown"); err != nil {
			if errors.Is(err, blobx.ErrExists) {
				return userErr("report %s is already in run %s; --commit-reports uploads into a run's own reports/, so it only ever hands reports to a new run — use a new run ID", name, spec.RunID)
			}
			return bucketErrFor(url, "uploading the stored report "+name, "the project's stored reports", err)
		}
		reports = append(reports, task.InputFile{Name: name, Bytes: int64(len(body)), SHA256: hexsum})
	}
	spec.Reports = reports
	if len(reports) > 0 {
		fmt.Fprintf(warn, "handing %d stored report(s) to the run, read with your own account: %s\n", len(reports), commitReportNames(reports))
	}
	return nil
}

// storedReportNames lists the project's stored reports with the launcher's
// own credential, sorted by name: every object under reports/ whose name is a
// mirror name, which is the same shape the commit step holds every name it
// receives to before writing a byte of it (digest.ParseMirrorPath). Anything
// else there — the layout's index beside the reports, a file a person put in
// the store — is no report and no row, exactly as it is no row of the
// checkout's own folder.
//
// A read the launcher's credential cannot make is refused and never skipped:
// a commit run handed fewer reports than the store holds would commit a
// branch that silently leaves them out, and the fix — a launcher or operator
// role in the project — is the person's to make.
func storedReportNames(ctx context.Context, env *cloudEnv) ([]string, error) {
	url := "gs://fugaro-runs-" + env.lc.GCPProject
	it := env.bucket.Bucket.List(&blob.ListOptions{Prefix: digest.ReportsPrefix + "/"})
	var names []string
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, bucketErrFor(url, "listing "+digest.ReportsPrefix+"/", "the project's stored reports", err)
		}
		if obj.IsDir {
			continue
		}
		name, ok := strings.CutPrefix(obj.Key, digest.ReportsPrefix+"/")
		if !ok {
			continue
		}
		if _, _, _, ok := digest.ParseMirrorPath(name); !ok {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// mirrorFolderDigests is the checkout's own mirror of the project's reports,
// keyed by the name each report carries under it, holding the sha256 of every
// report the launcher could read there. It is the transfer bound — a report
// whose bytes are already committed needs no copy in the run's prefix — and
// never a decision: a folder the launcher cannot read here (no checkout of
// the repository, a designation this launcher resolves to no folder, a
// symbolic link under the folder, a file it cannot open) answers no digests
// and hands every stored report in.
//
// That direction is the only safe one. A report withheld on the strength of a
// comparison made against something the launcher could not read would be a
// report the run never sees and never commits, and the folder is the
// repository's to shape: the commit step reads it again, on the clone it
// makes, and refuses a link rather than following it (digest.MirrorRows).
func mirrorFolderDigests(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) map[string]string {
	root := checkoutRoot(ctx, spec.Repo)
	if root == "" {
		return nil
	}
	dir := nodePolicy(ctx, &env.policy, env.lc).ReportsCommitDir()
	if dir == "" {
		return nil
	}
	rows, err := digest.MirrorRows(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		fmt.Fprintf(warn, "warning: reading %s here (%s): every stored report is handed in, and the commit step judges the folder itself\n", dir, oneLine(err.Error()))
		return nil
	}
	have := make(map[string]string, len(rows))
	for _, row := range rows {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), filepath.FromSlash(row.Rel)))
		if err != nil {
			// A report this launcher cannot read here is handed in: the
			// comparison is a transfer bound, and a bound it cannot compute
			// is the widest one.
			continue
		}
		sum := sha256.Sum256(data)
		have[row.Rel] = hex.EncodeToString(sum[:])
	}
	return have
}

// commitReportNames is the reports a launch handed in, for the one line the
// person reads before the run starts: the names in the order the task carries
// them.
func commitReportNames(reports []task.InputFile) string {
	names := make([]string, 0, len(reports))
	for _, rf := range reports {
		names = append(names, rf.Name)
	}
	return strings.Join(names, ", ")
}
