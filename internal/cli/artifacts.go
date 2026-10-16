package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// What `fugaro artifacts` is for: an artifact run's deliverable is a file in
// the runs bucket, not a pull request, so there is no host to open it on and
// no PR to read it from (design artifact-runs.md §9).
//
// Both subcommands read result.json and nothing else. `ls` is the record —
// it lists what the run says it delivered, never what the bucket currently
// holds — and `get` is the object, so "recorded but not present" is `get`'s
// to report and cannot be hidden by an `ls` that quietly agreed with the
// bucket instead.
//
// The record is UNTRUSTED TEXT: it was written by a job that ran a model with
// --dangerously-skip-permissions, and a hostile sibling run of the same
// repository can rewrite it and the artifact together (design §8.3). So
// everything `get` does with it is fail-closed:
//
//   - a name becomes a path only after the upload sanitiser's own refusal
//     list — runner.ArtifactName, called by both sides — which is no absolute
//     path, no ".." element, no control character, no leading dot and an
//     explicit length limit (design §8.2, §9);
//   - a name the record gives no sha256, or a size that is not a size (a
//     negative one), is refused and not downloaded: those two are what make
//     the bytes checkable at all. A recorded size of 0 is a size — a render
//     that writes an empty file matched by a glob records bytes 0 with the
//     digest of "" (result.schema.json: {"minimum": 0}) — and is fetched
//     like any other artifact;
//   - the destination is a fresh directory this command creates
//     (./<run>-artifacts/, with --out naming its PARENT), never the directory
//     the user happens to stand in, so a record cannot drop
//     .git/hooks/pre-commit or .claude/settings.json for the user's next
//     command to run or load (§9);
//   - an existing file, a symlink and anything that is not a regular file are
//     refusals, --force or not (a symlink is a path out of the directory the
//     user named);
//   - the set of names is checked as a set, because the destination is a path
//     on the operator's filesystem and not a name in a bucket: a name recorded
//     twice, two names a case-insensitive (or normalizing) filesystem writes
//     to one file, and a name that is a directory of another are all refused
//     before a byte is read (design §8.2);
//   - the bytes are written by blobx.GetFileExactly into a temporary file in
//     the destination's own directory and put in place only once the
//     recorded size and sha256 both match — both verified on that temporary,
//     before the placement, so a refusal cannot remove what the placement put
//     there and never the operator's own file — and the download is bounded by
//     the recorded size and by a deadline derived from it: a mismatch, an
//     over-cap object, a truncated one, a stalled read or a record whose
//     bytes disagree with its hash leaves NOTHING at the destination path —
//     not even a partial file;
//   - without --force the placement itself does not replace anything (blobx
//     places the file with a no-replace link, or an O_EXCL create), so a file
//     that appears between the check above and the write, or one this
//     filesystem folds the artifact's name onto, makes the download FAIL with
//     a user error instead of being written over — the pre-check cannot see
//     either of those, and it is not what the promise rests on;
//   - and nothing downloaded is ever opened, executed or unpacked (§8.1): the
//     recorded content type is printed as the record's claim and stops there.
//
// The recorded sha256 proves integrity in transit — that the bytes are the
// bytes the run uploaded — and never authorship: it lives in result.json, in
// the same repository-scoped prefix as the artifact, so anything that can
// rewrite one can rewrite the other consistently (§8.3, P11).

// artifactDirSuffix names get's destination directory: <run-id><suffix> under
// the parent --out gives (design §9).
const artifactDirSuffix = "-artifacts"

func newArtifactsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifacts",
		Short: "List and download what an artifact run delivered",
		Long: "Read and fetch the files a `mode: artifact` run put in the runs bucket.\n\n" +
			"Both subcommands read the run's result.json, never a bucket listing: ls prints\n" +
			"the record's own table, and get is the object — so an artifact the record names\n" +
			"and the bucket no longer holds (they expire with the run, runs_days) is a\n" +
			"recorded row that ls shows and a `get` that says exactly that.\n\n" +
			"The record was written by an untrusted, model-driven run, so get treats every\n" +
			"name in it as hostile: it downloads into a fresh directory it creates\n" +
			"(./<run>-artifacts/, with --out naming that directory's parent), refuses a name\n" +
			"that is absolute, walks out of that directory or begins with a dot, refuses to\n" +
			"overwrite an existing file without --force, and verifies the recorded size and\n" +
			"sha256 as the bytes arrive, leaving nothing behind when they do not match.\n" +
			"What it writes is never opened, executed or unpacked.",
	}
	cmd.AddCommand(newArtifactsLsCmd(), newArtifactsGetCmd())
	return cmd
}

type artifactsLsOptions struct {
	cloud  cloudOptions
	repo   string
	asJSON bool
}

func newArtifactsLsCmd() *cobra.Command {
	var o artifactsLsOptions
	cmd := &cobra.Command{
		Use:   "ls RUN",
		Short: "List what a run's record says it delivered",
		Long: "List the artifacts RUN (<repo-slug>/<run-id>, or a bare run ID) recorded in\n" +
			"its result.json: name, recorded size, short sha256, recorded content type and\n" +
			"the probe's verdict. The record is shown as it is — a name `get` cannot\n" +
			"retrieve is listed with the reason, never filtered out, and a missing object is\n" +
			"not ls's to hide: ls is the record, get is the object.\n\n" +
			"--json prints the record's artifact list verbatim.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runArtifactsLs(cmd, &o, args[0]) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository RUN belongs to (owner/name; needed only for a bare run ID)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

type artifactsGetOptions struct {
	cloud cloudOptions
	repo  string
	out   string
	force bool
}

func newArtifactsGetCmd() *cobra.Command {
	var o artifactsGetOptions
	cmd := &cobra.Command{
		Use:   "get RUN [NAME]",
		Short: "Download a run's artifacts, verifying them against its record",
		Long: "Download one artifact NAME, or every artifact the record lists when NAME is\n" +
			"omitted, verifying each one's size and sha256 against the run's result.json as\n" +
			"the bytes arrive. A mismatch is an error naming both hashes, and nothing is\n" +
			"written — not even the file the download streamed into.\n\n" +
			"The destination is a fresh directory this command creates,\n" +
			"./<run-id>-artifacts/ unless --out names another parent for it. A name in the\n" +
			"record that is absolute, walks out of that directory, begins with a dot or\n" +
			"holds a control character is refused — a refusal, never a flattened name.\n" +
			"An existing file needs --force to be replaced.\n\n" +
			"Nothing downloaded is ever opened, executed or unpacked: the recorded content\n" +
			"type, the size and the sha256 are printed, and that is all. The sha256 proves\n" +
			"the bytes are the bytes the run uploaded; it is not tamper evidence.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 2 {
				name = args[1]
			}
			return runArtifactsGet(cmd, &o, args[0], name)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository RUN belongs to (owner/name; needed only for a bare run ID)")
	f.StringVar(&o.out, "out", "", "the directory to create the run's artifact directory in (default: the working directory)")
	f.BoolVar(&o.force, "force", false, "replace a file that is already at a destination path")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// artifactsRun resolves RUN ([<repo-slug>/]<run-id>) and --repo to the store
// of one run. --repo names the repository a bare run ID belongs to, and a RUN
// that already carries a slug has to agree with it.
//
// The run itself is looked up in the bucket either way. --repo answers "which
// repository", not "does this run exist", so with it the lookup is one object
// check under the named repository rather than a search across all of them
// (locateRun: runstore.Locate). Doing without that check — an earlier version
// returned a store for anything that was shaped like a run ID — reports a run
// that was never launched as a run that recorded no artifacts: "it never
// finished" about a run that never started, which sends an operator looking
// for a container that died when they mistyped an id.
func artifactsRun(ctx context.Context, env *cloudEnv, ref, repo string) (*runstore.Store, error) {
	if repo == "" {
		slug, id, err := locateRun(ctx, env, ref)
		if err != nil {
			return nil, err
		}
		return runstore.Open(env.bucket.Bucket, slug, id), nil
	}
	slug, err := env.repoSlug(repo, checkoutOf(ctx, repo))
	if err != nil {
		return nil, err
	}
	if strings.Contains(ref, "/") {
		refSlug, id, err := parseRunRef(ref)
		if err != nil {
			return nil, err
		}
		if refSlug != slug {
			return nil, userErr("run %s is in %s, but --repo names %s", ref, refSlug, slug)
		}
		if _, _, err := locateRun(ctx, env, slug+"/"+id); err != nil {
			return nil, err
		}
		return runstore.Open(env.bucket.Bucket, slug, id), nil
	}
	// The run ID's form is checked here because it becomes part of an object
	// name (the same rule locateRun applies to a bare ID).
	if _, err := runstore.RunTime(ref); err != nil {
		return nil, userErr("%q is neither <repo-slug>/<run-id> nor a run ID", ref)
	}
	if _, _, err := locateRun(ctx, env, slug+"/"+ref); err != nil {
		return nil, err
	}
	return runstore.Open(env.bucket.Bucket, slug, ref), nil
}

// readArtifactRecord reads the run's result.json, the one thing both
// subcommands trust about what it delivered. A run that never finished has
// none, and there is then nothing to list and nothing to verify a download
// against, so that is a refusal and not an empty table.
func readArtifactRecord(ctx context.Context, s *runstore.Store) (*runstore.Record, error) {
	rec, err := s.ReadRecord(ctx)
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return nil, userErr("run %s has no result.json: it never finished, so it recorded no artifacts", s.Slug()+"/"+s.RunID())
	case err != nil:
		return nil, remote(err)
	}
	return rec, nil
}

func runArtifactsLs(cmd *cobra.Command, o *artifactsLsOptions, ref string) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	s, err := artifactsRun(ctx, env, ref, o.repo)
	if err != nil {
		return err
	}
	rec, err := readArtifactRecord(ctx, s)
	if err != nil {
		return err
	}
	return printArtifacts(cmd.OutOrStdout(), rec.Artifacts, o.asJSON)
}

// printArtifacts prints the record's artifact list: as JSON verbatim, or as a
// table with a line saying how many the record has. A name the retriever
// would refuse is shown with the reason — the record is printed as it is,
// never filtered (design §9) — and every field from the record goes through
// oneLine or cellOneLine, because a name is untrusted text and printed raw an
// escape sequence in it would rewrite the operator's screen.
func printArtifacts(w io.Writer, files []runstore.ArtifactFile, asJSON bool) error {
	if asJSON {
		if files == nil {
			files = []runstore.ArtifactFile{}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(files)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tBYTES\tSHA256\tTYPE\tPROBE")
	var total int64
	for _, a := range files {
		name := oneLine(a.Name)
		if problem := artifactNameProblem(a.Name); problem != "" {
			name += " (cannot be retrieved: " + problem + ")"
		}
		total += max(0, a.Bytes)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", cellOneLine(name), bytesCell(a.Bytes), shortSHA(a.SHA256), cellOneLine(oneLine(a.ContentType)), cellOneLine(probeCell(a.Probe)))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	noun := "artifacts"
	if len(files) == 1 {
		noun = "artifact"
	}
	_, err := fmt.Fprintf(w, "%d %s recorded, %s in all\n", len(files), noun, byteSize(total))
	return err
}

// probeCell is one artifact's probe verdict: the exit code, "not run" for a
// run with no probe at all (Review Focus 6: "there is no probe" is visible in
// the record rather than inferred from a missing field), and "-" for a record
// that predates the field.
func probeCell(p *runstore.ProbeResult) string {
	switch {
	case p == nil:
		return "-"
	case !p.Ran:
		return "not run"
	case p.Warning != "":
		return fmt.Sprintf("exit %d (%s)", p.ExitCode, oneLine(p.Warning))
	}
	return fmt.Sprintf("exit %d", p.ExitCode)
}

// bytesCell is a recorded byte count for a table cell: "?" for a count that
// is not a size at all, because the record is untrusted text.
func bytesCell(n int64) string {
	if n < 0 {
		return "?"
	}
	return byteSize(n)
}

// byteSize renders a byte count compactly for a table cell (the exact figure
// below a KiB, else one decimal of KiB/MiB/GiB). recipe.SizeText is the
// other shape — whole units only — which is what a refusal prints.
func byteSize(n int64) string {
	const (
		ki = int64(1) << 10
		mi = int64(1) << 20
		gi = int64(1) << 30
	)
	switch {
	case n < 0:
		return "?"
	case n < ki:
		return fmt.Sprintf("%d B", n)
	case n < mi:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(ki))
	case n < gi:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mi))
	default:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gi))
	}
}

// artifactCell is a run's artifacts in ls's table: the count and the
// recorded total, or "-" for a run that delivered none (the column only
// appears when some listed run has one).
func artifactCell(r runview.Row) string {
	if r.ArtifactCount == 0 {
		return "-"
	}
	noun := "files"
	if r.ArtifactCount == 1 {
		noun = "file"
	}
	return fmt.Sprintf("%d %s · %s", r.ArtifactCount, noun, byteSize(r.ArtifactBytes))
}

func runArtifactsGet(cmd *cobra.Command, o *artifactsGetOptions, ref, name string) error {
	ctx := cmd.Context()
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	s, err := artifactsRun(ctx, env, ref, o.repo)
	if err != nil {
		return err
	}
	rec, err := readArtifactRecord(ctx, s)
	if err != nil {
		return err
	}
	selected, err := selectArtifacts(s, rec.Artifacts, name)
	if err != nil {
		return err
	}
	// Every name and destination is checked BEFORE the first byte is read, so
	// a record with one bad entry downloads nothing at all: an "all
	// artifacts" run that silently skipped one would tell the owner
	// everything came back when it did not (design §9). The checks create
	// nothing — the directories a name needs are made by prepareArtifactDest
	// when that file is about to be written — so a refused record leaves no
	// half-made tree behind either.
	root := artifactDestRoot(o.out, s.RunID())
	if err := prepareArtifactRoot(root); err != nil {
		return err
	}
	dests := make([]string, len(selected))
	for i, a := range selected {
		if dests[i], err = checkArtifactDest(root, a.Name, o.force); err != nil {
			return err
		}
	}
	for i, a := range selected {
		if err := getArtifact(ctx, env, s, a, root, dests[i], o.force, cmd.OutOrStdout()); err != nil {
			return err
		}
	}
	return nil
}

// selectArtifacts is the record's artifact list, narrowed to NAME when the
// user named one. A name the record does not carry is refused rather than
// fetched: only the record fixes the hash and the size that make the bytes
// checkable, so an unrecorded object's bytes are bytes nothing attests to.
func selectArtifacts(s *runstore.Store, files []runstore.ArtifactFile, name string) ([]runstore.ArtifactFile, error) {
	if name != "" {
		chosen := make([]runstore.ArtifactFile, 0, 1)
		for _, a := range files {
			if a.Name == name {
				chosen = append(chosen, a)
			}
		}
		// Two answers, because they are two different records: one that
		// does not carry the name at all, and one that carries it twice —
		// for which "recorded no artifact named X" would be a lie, since
		// `fugaro artifacts ls` prints X. The second is the same refusal the
		// wholesale download makes (below), reached by name: naming one of
		// them does not say which entry's bytes to write.
		switch {
		case len(chosen) == 0:
			return nil, userErr("run %s recorded no artifact named %q: fugaro artifacts ls %s lists what it did", s.Slug()+"/"+s.RunID(), name, s.Slug()+"/"+s.RunID())
		case len(chosen) > 1:
			return nil, userErr("run %s recorded the artifact %q twice: one name is one artifact, and naming it does not say which entry's bytes to write (fugaro artifacts ls %s prints the record as it is)", s.Slug()+"/"+s.RunID(), name, s.Slug()+"/"+s.RunID())
		}
		files = chosen
	} else {
		if len(files) == 0 {
			return nil, userErr("run %s recorded no artifacts", s.Slug()+"/"+s.RunID())
		}
		// One name, one artifact, one path: the record is untrusted text, and
		// two entries that a filesystem would write to one file are refused
		// before any destination exists or any byte is read. This is the
		// retriever's half of the uploader's "no duplicate after
		// normalisation" refusal (design §8.2).
		//
		// The comparison is artifactSetConflict's, and it is the whole set's
		// and not a running one: a check that keeps one name per key and
		// overwrites it is order-dependent, and a record that lists its names
		// in the order that hides a pair defeats it. It is also the set's
		// because one of the refusals it makes is between two DIFFERENT
		// names — a name that is a directory of another.
		//
		// Naming one of a colliding pair still works (`get RUN A.png`): the
		// refusal is about writing two artifacts to one path, not about the
		// record being unreadable, and a named fetch of a doubled name is
		// refused above, by the case that names the duplication.
		if conflict := artifactSetConflict(files); conflict != "" {
			return nil, userErr("run %s recorded %s", s.Slug()+"/"+s.RunID(), conflict)
		}
	}
	// The refusals below are the selected set's, whether it is the whole
	// record or the one name the user asked for: a named artifact is checked
	// exactly as the record's entry for it is, or naming one would be a way
	// around every check.
	//
	// The name refusals come first, for the whole set, before any
	// destination exists or any byte is read.
	for _, a := range files {
		if problem := artifactNameProblem(a.Name); problem != "" {
			return nil, userErr("run %s recorded an artifact named %q, which cannot be retrieved: %s", s.Slug()+"/"+s.RunID(), a.Name, problem)
		}
	}
	for _, a := range files {
		switch {
		case !validSHA256(a.SHA256):
			// An empty or malformed hash would turn the verification off
			// entirely, and the bytes would then be bytes nothing attests
			// to: the record's own hash is what makes the download
			// checkable, so a record without one is refused.
			return nil, userErr("run %s recorded %q without a sha256: there is nothing to verify the download against", s.Slug()+"/"+s.RunID(), a.Name)
		case a.Bytes < 0:
			// 0 is a size a run records — result.schema.json's "bytes" has
			// {"minimum": 0}, and a render that writes an empty file matched
			// by a glob records bytes 0 with the digest of "" — so an empty
			// artifact is downloaded like any other, with a cap of 0 bytes.
			// A negative count is not a size, and there is then no cap to
			// bound the download by at all.
			return nil, userErr("run %s recorded %q as %d bytes: a negative count is not a size, and there is no size to bound the download by", s.Slug()+"/"+s.RunID(), a.Name, a.Bytes)
		case a.Bytes > recipe.MaxOutputBytes:
			// The record's own byte count is the cap, and the cap itself has
			// a ceiling: no runner can upload an artifact larger than the
			// recipe format's per-artifact maximum, so a record that claims
			// one is refused rather than believed (design §6, P6).
			return nil, userErr("run %s recorded %q as %s, over the %s ceiling a recipe's outputs.max_bytes may not exceed: this is not an artifact any run could have delivered", s.Slug()+"/"+s.RunID(), a.Name, recipe.SizeText(a.Bytes), recipe.SizeText(recipe.MaxOutputBytes))
		}
	}
	return files, nil
}

// artifactSetConflict is why the record's artifacts cannot all be downloaded
// into one destination root, as a sentence naming them, or "" when they can.
//
// Two questions, both about the destination being a path on the operator's
// filesystem rather than about the record being readable:
//
//   - two names a case-insensitive filesystem writes to one file. APFS and
//     NTFS ignore case; macOS normalises as well, so "café.png" in its NFC
//     form (U+00E9) and in its NFD form (e + U+0301) are one file there. The
//     collision is decided by runner.ArtifactCollisionKey, the uploader's own
//     key for the same question (one rule, two callers, as with
//     runner.ArtifactName below), and the answer is the set's: every name is
//     filed under its key before any pair is reported, so the order the
//     record lists them in cannot hide a pair, and no entry can evict another
//     from the table.
//   - a name that is a directory of another ("a" and "a/b.png"). Only one of
//     the two can exist: the first needs root/a to be a file and the second
//     needs it to be a directory. Left to the download, whichever is written
//     first decides what root/a is and the other then fails — a rename onto a
//     directory, or a write into a file — as a remote error, which is not
//     what a record's own contradiction is. So the set is refused before
//     anything is created (and the check phase creates nothing at all).
func artifactSetConflict(files []runstore.ArtifactFile) string {
	byKey := make(map[string]string, len(files))
	for _, a := range files {
		key := runner.ArtifactCollisionKey(a.Name)
		first, ok := byKey[key]
		switch {
		case !ok:
			byKey[key] = a.Name
		case first == a.Name:
			return fmt.Sprintf("the artifact %q twice: one name is one artifact, and two downloads of it would write the same file twice", a.Name)
		default:
			return fmt.Sprintf("%q and %q, which differ only by case, or in a Unicode normalization a filesystem may apply: one of the two would be written over the other", first, a.Name)
		}
	}
	// The prefix question, asked of the whole set: every ancestor of every
	// name is looked up, and the map above holds one name per key, so this is
	// independent of the order the record lists its entries in.
	for _, a := range files {
		key := runner.ArtifactCollisionKey(a.Name)
		for i := strings.LastIndexByte(key, '/'); i > 0; i = strings.LastIndexByte(key[:i], '/') {
			if ancestor, ok := byKey[key[:i]]; ok {
				return fmt.Sprintf("%q and %q: the first names a file and the second a file inside a directory of that name, so one download would have to write a file where the other needs a directory", ancestor, a.Name)
			}
		}
	}
	return ""
}

// artifactNameProblem is why name, read out of a run's result.json, may not
// become a path under the destination root, or "" when it may.
//
// The rule itself is the uploader's, not a copy of it: runner.ArtifactName is
// the one refusal list both sides call (design §9: "the upload path cannot
// produce what the retrieval path refuses"). A copy is what a first draft of
// this did, and it refused the zero-width and bidi characters
// internal/safetext escapes — a class the uploader accepts, because a
// variation selector in a filename is a legal name a run can deliver — so an
// artifact a run had recorded could never be fetched back by name, and
// `get RUN` with no NAME refused the whole run because of that one entry.
//
// The one refusal this adds is the disagreement between a name the uploader
// records and the name it was given: sanitiseName returns path.Clean of its
// input, so a record carrying an unclean name ("clips/./a.png") is one the
// runner could not have written, and reading it as a path is exactly the
// "clean it into something plausible" this side must not do.
func artifactNameProblem(name string) string {
	clean, problem := runner.ArtifactName(name)
	if problem != "" {
		return problem
	}
	if clean != name {
		return fmt.Sprintf("the name is not the cleaned form the uploader records (%s)", clean)
	}
	return ""
}

// artifactDestRoot is get's destination directory: <run-id>-artifacts under
// parent (the working directory unless --out says otherwise). --out names the
// PARENT, never the destination itself (design §9).
func artifactDestRoot(parent, runID string) string {
	if parent == "" {
		parent = "."
	}
	return filepath.Join(parent, runID+artifactDirSuffix)
}

// prepareArtifactRoot creates the destination directory. An existing entry at
// that path is only usable when it is a real directory: a symlink there is a
// path out of the directory the user named, and anything that is not a
// directory is not a place to write files into.
func prepareArtifactRoot(root string) error {
	fi, err := os.Lstat(root)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		return userErr("%s is a symlink: refusing to download into whatever it points at", oneLine(root))
	case err == nil && !fi.IsDir():
		return userErr("%s is not a directory", oneLine(root))
	case err == nil:
		return nil
	case !os.IsNotExist(err):
		return remote(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return userErr("creating %s: %v", oneLine(root), err)
	}
	return nil
}

// checkArtifactDest returns the path name is downloaded to, refusing a
// destination that must not be written: an existing file without --force, a
// symlink, anything that is not a regular file, a directory the name needs
// that is not a directory, and a name whose directories resolve outside root
// (a symlinked directory planted under it is the same escape as a name with a
// ".." in it).
//
// It creates nothing and it deletes nothing. The directories the name needs
// are made by prepareArtifactDest, at the moment that file is about to be
// written, and the two steps that have to be in that order — prove the parent
// is inside root, create, prove it again — live there. Splitting it this way
// is what keeps the check phase a read: a phase that creates directories
// changes the filesystem it is checking, so a refusal on a later entry leaves
// a half-made tree behind, and a record naming both "a" and "a/b.png" has its
// contradiction resolved into a directory by the check for the second entry
// rather than reported by artifactSetConflict before anything is made.
//
// "Writes only under the fresh root it created" still holds for directories,
// which is why the proving happened before the creating rather than after:
// Chesterton, a name like frames/sub/a.png whose frames/ is a link out of
// root must not have outside/sub/ created and then be refused.
func checkArtifactDest(root, name string, force bool) (string, error) {
	dest := filepath.Join(root, filepath.FromSlash(name))
	parent := filepath.Dir(dest)
	if err := parentUnderRoot(root, parent); err != nil {
		return "", err
	}
	// The parent is proved and nothing is created (see the doc comment):
	// prepareArtifactDest makes the directory when a file is about to be
	// written into it.
	fi, err := os.Lstat(dest)
	switch {
	case os.IsNotExist(err):
		return dest, nil
	case err != nil:
		return "", remote(err)
	case fi.Mode()&os.ModeSymlink != 0:
		// The write would replace the link (rename never follows the final
		// component), but a link is not a file this command put there and
		// --force is not a licence to change what the user's own path means:
		// they can remove it.
		return "", userErr("%s is a symlink: refusing to write through it (remove it first)", oneLine(dest))
	case !fi.Mode().IsRegular():
		return "", userErr("%s is not a regular file: refusing to replace it", oneLine(dest))
	case !force:
		return "", userErr("%s already exists: pass --force to replace it", oneLine(dest))
	}
	return dest, nil
}

// prepareArtifactDest creates the directories dest's name needs, at the moment
// a file is about to be written into them, and re-proves the parent is inside
// root now that it exists.
//
// The parent was proved once already, by checkArtifactDest, on the deepest
// directory that existed then; this repeats the two steps in the order that
// matters — prove, create, prove again — so a link swapped in between the
// check and the write is caught, and nothing is created outside root.
func prepareArtifactDest(root, dest string) error {
	parent := filepath.Dir(dest)
	if err := parentUnderRoot(root, parent); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return userErr("creating %s: %v", oneLine(parent), err)
	}
	// Again, now that it exists: MkdirAll created nothing outside root (the
	// check above resolved the deepest directory that was there and the rest
	// is names it made), and this resolves what is really there now.
	return underArtifactRoot(root, parent)
}

// underArtifactRoot refuses dir unless, with every symlink on the way
// resolved, it is root or below it.
func underArtifactRoot(root, dir string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return remote(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return remote(err)
	}
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return userErr("the artifact's directory resolves to %s, outside the destination %s: the name reached it through a link", oneLine(real), oneLine(root))
	}
	return nil
}

// parentUnderRoot is underArtifactRoot for a directory that may not exist yet
// — the check that has to come before MkdirAll creates it. It resolves the
// deepest directory of dir that is there and requires that one to be inside
// root, so nothing is created outside root and a link in the middle of the
// name is caught before the first directory is made rather than after.
//
// The anchor that IS there also has to be a directory (or a link to one): a
// plain file at "frames" while the name is "frames/a.png" means nothing can be
// written there at all, and it is the operator's own filesystem that says so.
// Reported here it is a user error naming the path; left to the write, it
// surfaces from os.Lstat or MkdirAll as a bare "not a directory" and reaches
// the user through remote(err) — exit 2 for a local condition, telling a
// script to retry and blaming the infrastructure for a name that cannot be
// fetched into this directory.
func parentUnderRoot(root, dir string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return remote(err)
	}
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Lstat(dir); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return remote(err)
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		// The walk above stopped on something that existed a moment ago;
		// whatever happened to it, there is no anchor to resolve.
		return remote(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		// A link is resolved below and refused there if it leaves root; here
		// it only has to lead to a directory. A dangling one, or one to a
		// file, is not a place to make the artifact's directories in, and
		// EvalSymlinks would otherwise report it as a remote failure.
		if fi, err = os.Stat(dir); err != nil {
			return userErr("%s is a link that does not resolve: the artifact's name needs a directory here, and this command will not write through a dangling link", oneLine(dir))
		}
	}
	if !fi.IsDir() {
		return userErr("%s is not a directory: the artifact's name needs a directory here, and this command will not replace what is there", oneLine(dir))
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return remote(err)
	}
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return userErr("the artifact's directory resolves to %s, outside the destination %s: the name reached it through a link", oneLine(real), oneLine(root))
	}
	return nil
}

// artifactObjectKey is where an artifact's bytes live: the run's own prefix,
// artifacts/, and the name. It is also the last of the name refusals — the
// bucket is asked only for a key under the run's artifacts/ prefix and never
// for one a ".." or a leading slash resolved out of it — so the check stands
// even for a caller that reached here without going through
// artifactNameProblem (design §8.2).
func artifactObjectKey(prefix, name string) (string, error) {
	key := path.Clean(prefix + runstore.ArtifactPrefix + name)
	if !strings.HasPrefix(key, prefix+runstore.ArtifactPrefix) {
		return "", fmt.Errorf("%s is not under %s%s: refusing to read an object outside the run's own artifacts", key, prefix, runstore.ArtifactPrefix)
	}
	return key, nil
}

// getArtifact downloads one artifact into dest and prints what it wrote.
//
// blobx.GetFile streams the bytes into a temporary file in dest's own
// directory, hashing as it goes, and puts it in place only once the
// sha256 matches: a mismatch, an over-cap object or a truncated one removes
// the temporary and leaves NOTHING at dest (Review Focus 9), and limit is the
// record's own byte count, so the download is bounded by what the run
// recorded rather than by the object's own claim (design §8.4). It is asked
// for strict classification: an operator or a launcher holds unconditional
// objectAdmin, which includes storage.objects.list, so a 403 here is an
// access problem and must not be reported as "no such artifact" (design §3).
//
// The recorded byte count is verified inside blobx, not here: GetFileExactly
// takes the record's own size as the size the object must have, checks it on
// the temporary file beside the hash, and refuses before the file is put in
// place. Checking it here instead — which a first version did, on the size
// GetFile returned — means comparing AFTER the placement, when both ways out
// are wrong: leaving the file breaks "nothing is written", and removing it
// removes whatever was at that path before, which with --force is the
// operator's own file.
//
// The placement is blobx's too, and it is what --force governs: without it the
// file is put in place without replacing what is there, so the refusal covers
// the two collisions checkArtifactDest's pre-check cannot — a file that
// appears after it, and a path this filesystem folds the artifact's name onto.
//
// The record's own two fields therefore have to agree with each other and
// with the bytes: two recorded fields that disagree are a reason to refuse
// rather than to believe the hash (the record is untrusted text; design §9
// "get verifies the byte count and the sha256 against the record").
func getArtifact(ctx context.Context, env *cloudEnv, s *runstore.Store, a runstore.ArtifactFile, root, dest string, force bool, out io.Writer) error {
	key, err := artifactObjectKey(s.Prefix(), a.Name)
	if err != nil {
		return userErr("run %s recorded an artifact named %q, which cannot be retrieved: %v", s.Slug()+"/"+s.RunID(), a.Name, err)
	}
	// The directories this name needs, made now that this file is the one
	// about to be written — after every destination has been checked, and
	// before a byte is read.
	if err := prepareArtifactDest(root, dest); err != nil {
		return err
	}
	// A bounded read: a stalled object must not hang the command forever.
	ctx, cancel := artifactReadContext(ctx, a.Bytes)
	defer cancel()
	// The last two arguments are the classification and the placement:
	// strict, so a 403 is an access problem and not a missing artifact, and
	// replace=force, so without --force the placement itself refuses to
	// replace anything — the operator's own file is not written over, and
	// neither is one this filesystem folds the artifact's name onto (the
	// pre-check above can see neither a file that arrives in between nor what
	// a folding filesystem makes of two different names).
	size, sum, err := env.bucket.GetFileExactly(ctx, key, dest, a.SHA256, a.Bytes, true, force)
	switch {
	case errors.Is(err, blobx.ErrDestExists):
		return userErr("%s already exists: pass --force to replace it", oneLine(dest))
	case errors.Is(err, blobx.ErrNotExist):
		return userErr("run %s/%s recorded %q, but there is no such artifact in the runs bucket: artifacts expire with their run (runs_days), and fugaro artifacts ls %s/%s shows the record's list", s.Slug(), s.RunID(), a.Name, s.Slug(), s.RunID())
	case errors.Is(err, blobx.ErrForbidden):
		return remote(fmt.Errorf("reading %s: %w (this account may not read the run's artifacts: objectViewer on the runs bucket is what an operator holds)", key, err))
	case errors.Is(err, blobx.ErrMismatch), errors.Is(err, blobx.ErrChanged), errors.Is(err, blobx.ErrTooLarge):
		// The bytes in the bucket are not the bytes the record describes: a
		// different hash, a different size, or more of them than it recorded.
		// Nothing was written — blobx verifies on its own temporary file,
		// before the placement — and whatever was at the destination path is
		// still there.
		return userErr("run %s/%s recorded %q as %d bytes with sha256 %s, and the object in the runs bucket is not that: %v (nothing was written)", s.Slug(), s.RunID(), a.Name, a.Bytes, a.SHA256, err)
	case err != nil:
		return remote(err)
	}
	// The content type is a field a record may leave empty (nothing forces a
	// render's file to have one — result.schema.json's content_type is
	// optional), and an empty one interpolated into the parenthetical leaves
	// a gap and two spaces where a value belongs. Saying so is what the line
	// means: the run recorded none.
	typeText := oneLine(a.ContentType)
	if typeText == "" {
		typeText = "no content type"
	}
	fmt.Fprintf(out, "%s -> %s (%s, sha256 %s, %s recorded)\n", oneLine(a.Name), oneLine(dest), byteSize(size), sum, typeText)
	return nil
}

// artifactReadTimeout caps how long one artifact's download may take. It is
// derived from the recorded size rather than fixed, because the sizes a
// recipe may allow run to 2 GiB (recipe.MaxOutputBytes) and a flat minute
// would refuse a large artifact on a slow link: a floor for the request
// itself, plus an assumed 1 MiB/s, which the smallest recorded size and the
// largest alike stay well above in practice. The point is not the exact
// number — it is that a read which stops producing bytes (a stalled
// connection, a wedged driver) ends the command with an error instead of
// hanging it until the operator kills it.
const (
	artifactReadBaseTimeout = 5 * time.Minute
	artifactReadBytesPerS   = 1 << 20
	// The derived bonus is clamped: the size reaching here is a record's own
	// byte count, and a timeout computed from untrusted text must not be
	// able to overflow into a negative duration (no deadline at all) or into
	// one no operator would wait out. MaxOutputBytes gives ~34 minutes, so
	// the clamp costs nothing today; it is here so that a record cannot make
	// the bound meaningless by being absurd.
	artifactReadMaxTimeout = 2 * time.Hour
)

// artifactReadContext bounds one artifact's download.
func artifactReadContext(ctx context.Context, size int64) (context.Context, context.CancelFunc) {
	timeout := artifactReadBaseTimeout
	if size > 0 {
		bonus := time.Duration(size/artifactReadBytesPerS) * time.Second
		if bonus < 0 || bonus > artifactReadMaxTimeout {
			bonus = artifactReadMaxTimeout
		}
		timeout += bonus
	}
	return context.WithTimeout(ctx, timeout)
}
