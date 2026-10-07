package cli

import (
	"bytes"
	"cmp"
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
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// anchorEdit is the gcp_project line's edit of a checkout's fugaro.yaml,
// prepared and not yet written.
type anchorEdit struct {
	path, id string
	old, out []byte
	changed  bool // false: the file already holds the line
}

func (a *anchorEdit) line() string { return "gcp_project: " + a.id }

// customBucketNote is why lc's checkouts get no gcp_project line, or "":
// teammates find the bucket from the line alone, so it is written only for
// the default runs-bucket name.
func customBucketNote(lc *localcfg.Config) string {
	if want := "fugaro-runs-" + lc.GCPProject; lc.RunsBucketName() != want {
		return fmt.Sprintf("gcp_project is not written to fugaro.yaml: shared config needs the default runs-bucket name %s (this installation's is %q)", want, lc.RunsBucketName())
	}
	return ""
}

// prepareAnchor reads the fugaro.yaml of the checkout at root and makes the
// edit that adds lc's gcp_project line (setGCPProjectLine). A file shape the
// edit refuses is a *byHandError, a differing value a user error.
func prepareAnchor(lc *localcfg.Config, root string) (*anchorEdit, error) {
	path := filepath.Join(root, "fugaro.yaml")
	old, err := readFugaroYAML(path)
	if err != nil {
		return nil, err
	}
	out, changed, err := setGCPProjectLine(old, lc.GCPProject)
	var bh *byHandError
	switch {
	case errors.As(err, &bh):
		return nil, err
	case err != nil:
		return nil, userErr("%v", err)
	}
	return &anchorEdit{path: path, id: lc.GCPProject, old: old, out: out, changed: changed}, nil
}

// writeAnchor is the one confirmation and write of the line, after the
// plugin stage's pattern: the diff, then --yes writes, a terminal asks
// [y/N], and anything else leaves the file alone and prints the line to add.
// planOnly prints what it would do and writes nothing. It reports whether
// the file was written; an edit with nothing to change writes nothing and
// prints nothing.
func (r *initRun) writeAnchor(a *anchorEdit, env initflow.Env, planOnly bool) (bool, error) {
	if !a.changed {
		return false, nil
	}
	fmt.Fprintf(r.w, "%s\n%s", a.path, lineDiff(string(a.old), string(a.out)))
	addLine := fmt.Sprintf("to let teammates use this installation without setup, add this line to %s: %s (every teammate's CLI and every CI job or pin that runs fugaro against the repository must be on this release before the line is merged: older versions refuse the key as unknown)\n", a.path, a.line())
	switch {
	case planOnly:
		fmt.Fprintln(r.w, "  (plan only: fugaro.yaml is not written)")
		return false, nil
	case env.Yes:
		fmt.Fprintln(r.w, "  confirmed by --yes")
	case env.Interactive:
		fmt.Fprintf(r.w, "Write this to %s? [y/N]: ", a.path)
		line, _ := r.in.ReadString('\n') // an ended input is a no
		if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
			fmt.Fprint(r.w, addLine)
			return false, nil
		}
	default:
		fmt.Fprint(r.w, addLine)
		return false, nil
	}
	if err := writeFileAtomic(a.path, a.out); err != nil {
		return false, err
	}
	fmt.Fprintf(r.w, "Updated %s with gcp_project. Review it with git diff and commit it like any change. Every teammate's CLI and every CI job or pin that runs fugaro against the repository must be on this release before you merge a change that adds gcp_project: older versions refuse the key as unknown.\n", a.path)
	return true, nil
}

// anchor puts the checkout's gcp_project: line (the trust anchor shared
// config is found from) into fugaro.yaml (writeAnchor). The repository
// stage's typed authorization (an unlisted repository) covers onboarding, not
// an edit to a committed file, so the edit keeps its own single prompt.
// planOnly prints what it would do and writes nothing. It also warns while
// the line is not yet safe to merge (gcpProjectProblems).
func (s *repositoryStage) anchor(ctx context.Context, env initflow.Env, planOnly bool) error {
	e, tg := s.e, s.tg
	if tg == nil || e.lc == nil || e.lc.GCPProject == "" {
		return nil
	}
	if note := customBucketNote(e.lc); note != "" {
		fmt.Fprintf(e.r.w, "note: %s\n", note)
		return nil
	}
	a, err := prepareAnchor(e.lc, tg.root)
	var bh *byHandError
	switch {
	case errors.As(err, &bh):
		fmt.Fprintf(e.r.w, "note: %s\n", bh.Error())
	case err != nil:
		return err
	default:
		if _, err := e.r.writeAnchor(a, env, planOnly); err != nil {
			return err
		}
	}
	s.warnOldImages(ctx)
	return nil
}

// rebuildSteps is the order that makes a repository's job images accept
// the line.
func rebuildSteps(repo, wf string) string {
	return fmt.Sprintf("run fugaro init (it copies the current release base image), then fugaro image build --repo %s --workflow %s, then fugaro init --anchor, before you merge a change that adds gcp_project", repo, wf)
}

// The texts of gcpProjectProblems: the repository stage's warnings and
// fugaro init --anchor's failures alike.

func oldImageWarning(repo, wf, version string) string {
	what := "was built by fugaro " + version
	if version == "" {
		what = "has no build record that names its fugaro version"
	}
	return fmt.Sprintf("the job image of %s workflow %s %s, and only an image built by fugaro %s or later accepts gcp_project in fugaro.yaml: runs and the daily image check will refuse the file until the image is rebuilt; %s", repo, wf, what, gcpProjectFieldSince, rebuildSteps(repo, wf))
}

func devImageWarning(repo, wf, version string) string {
	return fmt.Sprintf("the job image of %s workflow %s was built by a development CLI (fugaro_version %s), not a release, so whether it accepts gcp_project in fugaro.yaml is unknown; rebuild it from a release base image: %s", repo, wf, version, rebuildSteps(repo, wf))
}

func unknownImageWarning(repo, wf, why string) string {
	return fmt.Sprintf("could not read the build record of %s workflow %s (unknown image age): %s; if its image was built before fugaro.yaml could carry gcp_project, runs and the daily image check will refuse the file, so %s", repo, wf, why, rebuildSteps(repo, wf))
}

func customBaseWarning(ref, kind, lcPath string) string {
	return fmt.Sprintf("the base image %s for kind %s is not a release >= %s (a base image that is set in the local config is never replaced by fugaro init --base); remove base_images.%s from %s (keep a backup) and run fugaro init --base %s from outside the checkout, then build the image (fugaro image build), before you merge a change that adds gcp_project", ref, kind, gcpProjectFieldSince, kind, cmp.Or(lcPath, "the local config"), kind)
}

func oldBaseWarning(ref, kind, version string) string {
	return fmt.Sprintf("the base image %s for kind %s is release %s, older than %s: run fugaro init --base %s (it copies this release's base image and moves base_images.%s to it), then build the image (fugaro image build), before you merge a change that adds gcp_project", ref, kind, version, gcpProjectFieldSince, kind, kind)
}

// anchorProblem is one reason the gcp_project line is not safe to merge
// yet; remote is a cloud read that failed (exit 2 for --anchor).
type anchorProblem struct {
	text   string
	remote bool
}

// recordVersionProblem judges a build record's fugaro version: a release at
// or after gcpProjectFieldSince is fine ("").
func recordVersionProblem(repo, wf, version string) string {
	v := strings.TrimPrefix(version, "v")
	switch {
	case v == "":
		return oldImageWarning(repo, wf, "")
	case !releaseRE.MatchString(v):
		return devImageWarning(repo, wf, version)
	case imagePredates(v, gcpProjectFieldSince):
		return oldImageWarning(repo, wf, version)
	}
	return ""
}

// gcpProjectProblems are the reasons repo's fugaro.yaml may not carry
// gcp_project yet, the one source of the repository stage's warnings and of
// fugaro init --anchor's checks. bases are the repository's workflows, each
// with its base kind ("" for none known). For every workflow, the build
// record in the runs bucket must name a release at or after
// gcpProjectFieldSince (no record, an older one or a development CLI's is a
// problem, and so is a record that cannot be read); for every base kind, the
// local config's base image, when one is set, must be a release image
// fugaro init copied, at or after that release (lcPath is the local config's
// file, for the advice).
func gcpProjectProblems(ctx context.Context, lc *localcfg.Config, lcPath, provider, repo string, bases map[string]string) []anchorProblem {
	var ps []anchorProblem
	add := func(remote bool, text string) { ps = append(ps, anchorProblem{text: text, remote: remote}) }
	wfs := slices.Sorted(maps.Keys(bases))
	slug, serr := task.Slug(provider, repo)
	var b *blobx.Bucket
	var berr error
	berrRemote := true
	switch u := recordReadURL(lc); {
	case serr != nil:
		berr, berrRemote = serr, false
	case fakeEndpointsOnGS(lc, u):
		berr, berrRemote = errors.New("the storage endpoint is a fake, so the real bucket is not read"), false
	default:
		if b, berr = openRecordBucket(ctx, u); berr == nil {
			defer b.Close()
		}
	}
	for _, wf := range wfs {
		if berr != nil {
			add(berrRemote, unknownImageWarning(repo, wf, oneLineCLI(berr.Error())))
			continue
		}
		data, _, rerr := b.Read(ctx, imagecheck.RecordKey(slug, wf))
		switch {
		case errors.Is(rerr, blobx.ErrNotExist):
			add(false, oldImageWarning(repo, wf, ""))
		case rerr != nil:
			add(true, unknownImageWarning(repo, wf, oneLineCLI(rerr.Error())))
		default:
			rec, perr := imagecheck.ParseRecord(data)
			if perr != nil {
				add(false, unknownImageWarning(repo, wf, oneLineCLI(perr.Error())))
			} else if t := recordVersionProblem(repo, wf, rec.FugaroVersion); t != "" {
				add(false, t)
			}
		}
	}
	kinds := map[string]bool{}
	for _, k := range bases {
		if slices.Contains(config.Bases, k) {
			kinds[k] = true
		}
	}
	host, herr := infra.RegistryHost(lc)
	for _, k := range slices.Sorted(maps.Keys(kinds)) {
		ref := lc.BaseImage(k)
		if ref == "" {
			continue // fugaro init copies the current release
		}
		v, managed := "", false
		if herr == nil {
			v, managed = managedVersion(ref, host, k)
		}
		switch {
		case !managed:
			add(false, customBaseWarning(ref, k, lcPath))
		case imagePredates(v, gcpProjectFieldSince):
			add(false, oldBaseWarning(ref, k, v))
		}
	}
	return ps
}

// workflowBases are cfg's workflows with their base kinds, plus the names
// in extra (the local config's list) that cfg lacks, with none known.
func workflowBases(cfg *config.Config, extra []string) map[string]string {
	m := map[string]string{}
	for name, w := range cfg.Workflows {
		m[name] = w.Base
	}
	for _, n := range extra {
		if _, ok := m[n]; !ok {
			m[n] = ""
		}
	}
	return m
}

// warnOldImages warns of each reason the line is not safe to merge yet
// (gcpProjectProblems). It is said every run while the line is, or is about
// to be, in the file: the owner merges it only after the rebuild.
func (s *repositoryStage) warnOldImages(ctx context.Context) {
	e, tg := s.e, s.tg
	for _, p := range gcpProjectProblems(ctx, e.lc, e.path, tg.cfg.Git.Provider, tg.repo, workflowBases(tg.cfg, nil)) {
		e.r.warn(p.text)
	}
}
