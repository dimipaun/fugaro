package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/task"
)

// gcpProjectFieldSince is the first fugaro release whose fugaro.yaml parser
// knows gcp_project:. A job image built by an older binary refuses the key.
// It must equal the release that ships the field: docs/release.md ("Before
// you tag") has the release checklist verify it.
const gcpProjectFieldSince = "0.4.0"

var (
	// A top-level project: line that is safe to insert after: a plain or
	// quoted scalar on one line, then at most a trailing comment. A block
	// scalar (> |), a flow collection, an anchor, a tag or a quote that does
	// not close on the line do not match.
	projectLineRE = regexp.MustCompile(`^project:[ \t]+(?:"(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^\s"'>|\[\]{}&*!#%@` + "`" + `,][^#]*?)[ \t]*(?:#.*)?$`)
	// Any line that is a top-level project: key at all.
	projectKeyRE = regexp.MustCompile(`^["']?project["']?[ \t]*:`)
	// Any line that looks like a top-level gcp_project key, quoted or not.
	gcpProjectKeyRE = regexp.MustCompile(`^["']?gcp_project["']?[ \t]*:`)
	versionPrefixRE = regexp.MustCompile(`^v?(\d{1,9})\.(\d{1,9})\.(\d{1,9})`)
)

// byHandError is an edit setGCPProjectLine will not make: the file's shape
// leaves no safe insertion point, or the result would not read back as
// intended. The line is the user's to add.
type byHandError struct{ reason, id string }

func (e *byHandError) Error() string {
	return e.reason + "; fugaro.yaml is not edited: add this line to it by hand, at the top level: " + e.line()
}

func (e *byHandError) line() string { return "gcp_project: " + e.id }

// setGCPProjectLine inserts gcp_project: id on the line after the top-level
// project: line, leaving every other byte as it is. It is a no-op when the
// file already holds id (in any quoting), and an error when it holds
// anything else (a reviewed value is never rewritten), id is not a GCP
// project ID, or the project: line is not a simple one-line scalar. The
// result is parsed again and must read back with no new problems, the same
// project and the new gcp_project; otherwise nothing is edited (*byHandError).
func setGCPProjectLine(data []byte, id string) (out []byte, changed bool, err error) {
	if !config.GCPProjectRE.MatchString(id) {
		return nil, false, fmt.Errorf("%q is not a GCP project ID", id)
	}
	byHand := func(format string, args ...any) error {
		return &byHandError{reason: fmt.Sprintf(format, args...), id: id}
	}
	cur, err := config.GCPProjectOf(data)
	if err != nil {
		return nil, false, byHand("fugaro.yaml cannot be read for gcp_project (%v)", err)
	}
	if cur == id {
		return data, false, nil
	}
	if cur != "" {
		return nil, false, fmt.Errorf("fugaro.yaml has gcp_project: %s, not %s: not rewritten; change it by hand if that is intended", cur, id)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	at := -1
	for i, l := range lines {
		t := bytes.TrimRight(l, "\r\n")
		switch {
		case gcpProjectKeyRE.Match(t):
			return nil, false, byHand("fugaro.yaml has a gcp_project: line with no usable value")
		case at < 0 && projectKeyRE.Match(t):
			if !projectLineRE.Match(t) {
				return nil, false, byHand("the project: line is not a simple one-line value")
			}
			if i+1 < len(lines) {
				next := lines[i+1]
				if rest := bytes.TrimLeft(next, " \t"); len(rest) < len(next) && len(bytes.TrimSpace(rest)) > 0 && rest[0] != '#' {
					return nil, false, byHand("the project: value continues on the next line")
				}
			}
			at = i
		}
	}
	if at < 0 {
		return nil, false, byHand("fugaro.yaml has no top-level project: line to put it after")
	}
	l := lines[at]
	var ins []byte
	var eol string
	switch {
	case bytes.HasSuffix(l, []byte("\r\n")):
		eol = "\r\n"
		ins = []byte("gcp_project: " + id + eol)
	case bytes.HasSuffix(l, []byte("\n")):
		eol = "\n"
		ins = []byte("gcp_project: " + id + eol)
	default: // the last line, unterminated: the new line is unterminated too
		ins = []byte("\ngcp_project: " + id)
	}
	out = bytes.Join(lines[:at+1], nil)
	out = append(out, ins...)
	out = append(out, bytes.Join(lines[at+1:], nil)...)
	// Read it back: the file must mean what it did, plus the one line.
	_, before := config.Parse(data)
	cfg, after := config.Parse(out)
	oldProject, _ := config.ProjectOf(data)
	newProject, perr := config.ProjectOf(out)
	got, gerr := config.GCPProjectOf(out)
	if cfg == nil || len(after) != len(before) || perr != nil || newProject != oldProject || gerr != nil || got != id {
		return nil, false, byHand("the edited file would not read back as intended")
	}
	return out, true, nil
}

// imagePredates reports whether a build record's fugaro version is older
// than since: the image holds a binary that does not know the field. A
// missing version is older; a version that is not a release number (a dev
// build) is not judged.
func imagePredates(recordVersion, since string) bool {
	if recordVersion == "" {
		return true
	}
	a, ok := parseNumeric(recordVersion)
	if !ok {
		return false
	}
	b, ok := parseNumeric(since)
	if !ok {
		return false
	}
	return slices.Compare(a, b) < 0
}

func parseNumeric(v string) ([]int, bool) {
	m := versionPrefixRE.FindStringSubmatch(v)
	if m == nil {
		return nil, false
	}
	var n []int
	for _, s := range m[1:] {
		x, _ := strconv.Atoi(s)
		n = append(n, x)
	}
	return n, true
}

// anchor puts the checkout's gcp_project: line (the trust anchor shared
// config is found from) into fugaro.yaml, after the plugin stage's pattern:
// the diff, then --yes writes, a terminal asks [y/N], and anything else
// leaves the file alone and prints the line to add. The repository stage's
// typed authorization (an unlisted repository) covers onboarding, not an edit
// to a committed file, so this keeps its own single prompt. planOnly prints
// what it would do and writes nothing. It also warns when a job image
// predates the field.
func (s *repositoryStage) anchor(ctx context.Context, env initflow.Env, planOnly bool) error {
	e, tg := s.e, s.tg
	if tg == nil || e.lc == nil || e.lc.GCPProject == "" {
		return nil
	}
	r, id := e.r, e.lc.GCPProject
	if want := "fugaro-runs-" + id; e.lc.RunsBucketName() != want {
		fmt.Fprintf(r.w, "note: gcp_project is not written to fugaro.yaml: shared config needs the default runs-bucket name %s (this installation's is %q)\n", want, e.lc.RunsBucketName())
		return nil
	}
	path := filepath.Join(tg.root, "fugaro.yaml")
	old, err := readFugaroYAML(path)
	if err != nil {
		return err
	}
	out, changed, err := setGCPProjectLine(old, id)
	var bh *byHandError
	switch {
	case errors.As(err, &bh):
		fmt.Fprintf(r.w, "note: %s\n", bh.Error())
		s.warnOldImages(ctx)
		return nil
	case err != nil:
		return userErr("%v", err)
	}
	if changed {
		fmt.Fprintf(r.w, "%s\n%s", path, lineDiff(string(old), string(out)))
		addLine := fmt.Sprintf("to let teammates use this installation without setup, add this line to %s: gcp_project: %s (every teammate's CLI and every CI job or pin that runs fugaro against the repository must be on this release before the line is merged: older versions refuse the key as unknown)\n", path, id)
		switch {
		case planOnly:
			fmt.Fprintln(r.w, "  (plan only: fugaro.yaml is not written)")
		case env.Yes:
			fmt.Fprintln(r.w, "  confirmed by --yes")
		case env.Interactive:
			fmt.Fprintf(r.w, "Write this to %s? [y/N]: ", path)
			line, _ := r.in.ReadString('\n') // an ended input is a no
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				fmt.Fprint(r.w, addLine)
				changed = false
			}
		default:
			fmt.Fprint(r.w, addLine)
			changed = false
		}
		if changed && !planOnly {
			if err := writeFileAtomic(path, out); err != nil {
				return err
			}
			fmt.Fprintf(r.w, "Updated %s with gcp_project. Review it with git diff and commit it like any change. Every teammate's CLI and every CI job or pin that runs fugaro against the repository must be on this release before you merge a change that adds gcp_project: older versions refuse the key as unknown.\n", path)
		}
	}
	s.warnOldImages(ctx)
	return nil
}

// Warning texts of warnOldImages.
func oldImageWarning(repo, wf string) string {
	return fmt.Sprintf("the job image of %s workflow %s was built before fugaro.yaml could carry gcp_project: runs and the daily image check will refuse the file until the image is rebuilt; run fugaro init (it copies the current base image), then fugaro image build --repo %s --workflow %s, then fugaro init again, before you merge a change that adds gcp_project", repo, wf, repo, wf)
}

func unknownImageWarning(repo, wf, why string) string {
	return fmt.Sprintf("could not read the build record of %s workflow %s (unknown image age): %s; if its image was built before fugaro.yaml could carry gcp_project, runs and the daily image check will refuse the file, so run fugaro init (it copies the current base image), then fugaro image build --repo %s --workflow %s, then fugaro init again, before you merge a change that adds gcp_project", repo, wf, why, repo, wf)
}

// warnOldImages says which workflows' job images predate the field (or have
// no record), or whose record could not be read. It is said every run while
// the line is, or is about to be, in the file: the owner merges it only after
// the rebuild.
func (s *repositoryStage) warnOldImages(ctx context.Context) {
	e, tg := s.e, s.tg
	slug, err := task.Slug(tg.cfg.Git.Provider, tg.repo)
	if err != nil {
		return
	}
	var b *blobx.Bucket
	if u := recordReadURL(e.lc); fakeEndpointsOnGS(e.lc, u) {
		err = errors.New("the storage endpoint is a fake, so the real bucket is not read")
	} else if b, err = openRecordBucket(ctx, u); err == nil {
		defer b.Close()
	}
	for _, wf := range slices.Sorted(maps.Keys(tg.cfg.Workflows)) {
		if err != nil {
			e.r.warn(unknownImageWarning(tg.repo, wf, oneLineCLI(err.Error())))
			continue
		}
		data, _, rerr := b.Read(ctx, imagecheck.RecordKey(slug, wf))
		switch {
		case errors.Is(rerr, blobx.ErrNotExist):
			e.r.warn(oldImageWarning(tg.repo, wf))
		case rerr != nil:
			e.r.warn(unknownImageWarning(tg.repo, wf, oneLineCLI(rerr.Error())))
		default:
			rec, perr := imagecheck.ParseRecord(data)
			switch {
			case perr != nil:
				e.r.warn(unknownImageWarning(tg.repo, wf, oneLineCLI(perr.Error())))
			case imagePredates(rec.FugaroVersion, gcpProjectFieldSince):
				e.r.warn(oldImageWarning(tg.repo, wf))
			}
		}
	}
}
