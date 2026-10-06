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

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/task"
)

// gcpProjectFieldSince is the first fugaro release whose fugaro.yaml parser
// knows gcp_project:. A job image built by an older binary refuses the key.
// It is a placeholder for the release that adds the field: the release task
// bumps it if that number differs.
const gcpProjectFieldSince = "0.4.0"

var (
	projectLineRE    = regexp.MustCompile(`^project:\s`)
	gcpProjectLineRE = regexp.MustCompile(`^gcp_project:`)
	versionPrefixRE  = regexp.MustCompile(`^v?(\d{1,9})\.(\d{1,9})\.(\d{1,9})`)
)

// setGCPProjectLine inserts gcp_project: id on the line after the top-level
// project: line, leaving every other byte as it is. It is a no-op when the
// file already holds id, and an error when it holds anything else (a
// reviewed value is never rewritten) or id is not a GCP project ID.
func setGCPProjectLine(data []byte, id string) (out []byte, changed bool, err error) {
	if !config.GCPProjectRE.MatchString(id) {
		return nil, false, fmt.Errorf("%q is not a GCP project ID", id)
	}
	cur, err := config.GCPProjectOf(data)
	if err != nil {
		return nil, false, err
	}
	if cur == id {
		return data, false, nil
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	at := -1
	for i, l := range lines {
		switch {
		case gcpProjectLineRE.Match(l):
			if cur != "" {
				return nil, false, fmt.Errorf("fugaro.yaml has gcp_project: %s, not %s: not rewritten; change it by hand if that is intended", cur, id)
			}
			return nil, false, errors.New("fugaro.yaml has a gcp_project: line with no usable value: fix or remove it, then rerun")
		case at < 0 && projectLineRE.Match(l):
			at = i
		}
	}
	if at < 0 {
		return nil, false, errors.New("fugaro.yaml has no top-level project: line to put gcp_project: after")
	}
	l := lines[at]
	eol := "\n"
	switch {
	case bytes.HasSuffix(l, []byte("\r\n")):
		eol = "\r\n"
	case !bytes.HasSuffix(l, []byte("\n")): // the last line, unterminated
		lines[at] = append(slices.Clone(l), '\n')
		out = bytes.Join(lines[:at+1], nil)
		return append(out, []byte("gcp_project: "+id)...), true, nil
	}
	ins := []byte("gcp_project: " + id + eol)
	out = bytes.Join(lines[:at+1], nil)
	out = append(out, ins...)
	out = append(out, bytes.Join(lines[at+1:], nil)...)
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

// anchor writes the checkout's gcp_project: line (the trust anchor shared
// config is found from) and warns when a job image would refuse it. It runs
// under the stage's existing authorization for writes to the checkout.
// planOnly prints what it would do and writes nothing.
func (s *repositoryStage) anchor(ctx context.Context, planOnly bool) error {
	e, tg := s.e, s.tg
	if tg == nil || e.lc == nil || e.lc.GCPProject == "" {
		return nil
	}
	w := e.r.w
	if want := "fugaro-runs-" + e.lc.GCPProject; e.lc.RunsBucketName() != want {
		fmt.Fprintf(w, "note: gcp_project is not written to fugaro.yaml: shared config needs the default runs-bucket name %s (this installation's is %q)\n", want, e.lc.RunsBucketName())
		return nil
	}
	path := filepath.Join(tg.root, "fugaro.yaml")
	old, err := readFugaroYAML(path)
	if err != nil {
		return err
	}
	out, changed, err := setGCPProjectLine(old, e.lc.GCPProject)
	if err != nil {
		return userErr("%v", err)
	}
	if !changed {
		return nil
	}
	fmt.Fprintf(w, "%s\n%s", path, lineDiff(string(old), string(out)))
	if planOnly {
		fmt.Fprintln(w, "  (plan only: fugaro.yaml is not written)")
	} else if err := writeFileAtomic(path, out); err != nil {
		return err
	} else {
		fmt.Fprintf(w, "Updated %s with gcp_project. Review it with git diff and commit it like any change.\n", path)
	}
	s.warnOldImages(ctx)
	return nil
}

// warnOldImages says which workflows' job images predate the field.
func (s *repositoryStage) warnOldImages(ctx context.Context) {
	e, tg := s.e, s.tg
	slug, err := task.Slug(tg.cfg.Git.Provider, tg.repo)
	if err != nil {
		return
	}
	var b *blobx.Bucket
	if b, err = openRecordBucket(ctx, recordReadURL(e.lc)); err == nil {
		defer b.Close()
	}
	for _, wf := range slices.Sorted(maps.Keys(tg.cfg.Workflows)) {
		old, unknown := true, ""
		if err == nil {
			data, _, rerr := b.Read(ctx, imagecheck.RecordKey(slug, wf))
			switch {
			case errors.Is(rerr, blobx.ErrNotExist):
			case rerr != nil:
				unknown = " (its build record could not be read: " + oneLineCLI(rerr.Error()) + ")"
			default:
				if rec, perr := imagecheck.ParseRecord(data); perr == nil {
					old = imagePredates(rec.FugaroVersion, gcpProjectFieldSince)
				}
			}
		} else {
			unknown = " (the build records could not be read: " + oneLineCLI(err.Error()) + ")"
		}
		if old {
			e.r.warn(fmt.Sprintf("the job image of %s workflow %s was built before fugaro.yaml could carry gcp_project%s: runs and the daily image check will refuse the file until the image is rebuilt; run fugaro image build --repo %s --workflow %s BEFORE merging this change", tg.repo, wf, unknown, tg.repo, wf))
		}
	}
}
