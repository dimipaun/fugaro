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

// The reasons of gcpProjectProblems, one clause each, about one workflow's
// job image ("it") or the base image its next build would start from.

func reasonNoRecord() string { return "it has no build record" }

func reasonRecordNoVersion() string {
	return "its build record does not name the fugaro that submitted it"
}

func reasonRecordVersionOld(version string) string {
	return fmt.Sprintf("its build record says it was submitted by fugaro %s, older than %s", version, gcpProjectFieldSince)
}

func reasonDevCLI(version string) string {
	return fmt.Sprintf("it was built by a development CLI (fugaro_version %s), not a release", version)
}

func reasonUnreadable(why string) string {
	return fmt.Sprintf("its build record could not be read (%s), so its age is unknown", why)
}

func reasonNoBaseRef() string {
	return "its build record does not say which base image it was built from (a record older than that field), so the fugaro binary in it is unknown"
}

func reasonBaseRefOld(ref, version string) string {
	return fmt.Sprintf("it was built from the base image %s, release %s, older than %s, whose fugaro binary it holds", ref, version, gcpProjectFieldSince)
}

func reasonBaseRefDev(ref string) string {
	return fmt.Sprintf("it was built from the base image %s, which is not a release (a development or hand-pushed image), whose fugaro binary it holds", ref)
}

func reasonConfigBaseCustom(ref, kind string) string {
	return fmt.Sprintf("the local config's base image %s for kind %s is not a release >= %s, and a base image set in the local config is never replaced by fugaro init --base or fugaro image refresh, so the next build would start from it", ref, kind, gcpProjectFieldSince)
}

func reasonConfigBaseOld(ref, kind, version string) string {
	return fmt.Sprintf("the local config's base image %s for kind %s is release %s, older than %s, so the next build would start from it", ref, kind, version, gcpProjectFieldSince)
}

func reasonRegistryHost(kind string, err error) string {
	return fmt.Sprintf("the registry host is unknown (%s), so the local config's base image for kind %s cannot be checked", oneLineCLI(err.Error()), kind)
}

// anchorProblemText is the one text about repo's workflow wf (base kind
// kind, "" when none is known): its reasons, then one ordered fix: the
// local config's base entry removed first when it is a custom image
// (customBase), then fugaro image refresh (or fugaro image build when the
// kind is unknown, since refresh needs one), then --anchor. lcPath is the
// local config's file.
func anchorProblemText(repo, wf, kind, lcPath string, reasons []string, customBase bool) string {
	var steps []string
	if customBase {
		where := "the local config"
		if lcPath != "" {
			where = quoteWord(lcPath)
		}
		steps = append(steps, fmt.Sprintf("remove base_images.%s from %s (keep a backup)", kind, where))
	}
	if kind != "" {
		steps = append(steps, fmt.Sprintf("fugaro image refresh --repo %s --workflow %s in the checkout, in your own terminal window, or with --yes from CI or a script (never in a coding agent's session; it copies this release's %s base image, points the daily image check job at it and rebuilds the image)", repo, wf, kind))
	} else {
		steps = append(steps, fmt.Sprintf("fugaro image build --repo %s --workflow %s", repo, wf))
	}
	steps = append(steps, "fugaro init --anchor")
	for i, s := range steps {
		steps[i] = fmt.Sprintf("(%d) %s", i+1, s)
	}
	return fmt.Sprintf("the job image of %s workflow %s is not ready for gcp_project in fugaro.yaml (runs and the daily image check would refuse the file): %s. In order: %s, before you merge a change that adds gcp_project",
		repo, wf, strings.Join(reasons, "; "), strings.Join(steps, ", "))
}

// anchorProblem is one workflow's reasons the gcp_project line is not safe
// to merge yet, as one text; remote is a cloud read that failed (exit 2 for
// --anchor).
type anchorProblem struct {
	text   string
	remote bool
}

// recordVersionReason judges a build record's fugaro version (the CLI that
// submitted the build): a release at or after gcpProjectFieldSince is fine
// ("").
func recordVersionReason(version string) string {
	v := strings.TrimPrefix(version, "v")
	switch {
	case v == "":
		return reasonRecordNoVersion()
	case !releaseRE.MatchString(v):
		return reasonDevCLI(version)
	case imagePredates(v, gcpProjectFieldSince):
		return reasonRecordVersionOld(version)
	}
	return ""
}

// baseRefReleaseRE is a release base image of one of config.Bases, on any
// host (an unset base is the release's own on ghcr.io, and --image-source
// names another): <host and path>/fugaro-<kind>:<X.Y.Z>, with an optional
// digest, as the whole string.
var baseRefReleaseRE = func() *regexp.Regexp {
	kinds := make([]string, len(config.Bases))
	for i, k := range config.Bases {
		kinds[i] = regexp.QuoteMeta(k)
	}
	return regexp.MustCompile(`\A[^\s@]+/fugaro-(` + strings.Join(kinds, "|") + `):v?([0-9]+\.[0-9]+\.[0-9]+)(?:@sha256:[0-9a-f]{64})?\z`)
}()

// baseRefReason judges the base image a build ran FROM, as its record says:
// the image holds that base's fugaro binary, whatever CLI submitted it. It
// must be a release image of the workflow's base kind (when kind is known),
// at or after gcpProjectFieldSince.
func baseRefReason(ref, kind string) string {
	if ref == "" {
		return reasonNoBaseRef()
	}
	m := baseRefReleaseRE.FindStringSubmatch(ref)
	switch {
	case m == nil || (kind != "" && m[1] != kind):
		return reasonBaseRefDev(ref)
	case imagePredates(m[2], gcpProjectFieldSince):
		return reasonBaseRefOld(ref, m[2])
	}
	return ""
}

// gcpProjectProblems are the reasons repo's fugaro.yaml may not carry
// gcp_project yet, one problem per workflow, the one source of the
// repository stage's warnings and of fugaro init --anchor's checks. bases
// are the repository's workflows, each with its base kind ("" for none
// known). For every workflow, its build record in the runs bucket must exist
// and be readable, name a release at or after gcpProjectFieldSince as the
// CLI that submitted it, and name a release base image at or after it as the
// base it was built FROM (the image holds that base's binary); and the
// local config's base image for its kind, when one is set, must be a
// release image fugaro init copied, at or after that release, else the next
// build regresses. lcPath is the local config's file, for the advice. It
// reads only the records: no other cloud call.
func gcpProjectProblems(ctx context.Context, lc *localcfg.Config, lcPath, provider, repo string, bases map[string]string) []anchorProblem {
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
	host, herr := infra.RegistryHost(lc)
	var ps []anchorProblem
	for _, wf := range slices.Sorted(maps.Keys(bases)) {
		var reasons []string
		remote := false
		add := func(r string) {
			if r != "" {
				reasons = append(reasons, r)
			}
		}
		kind, customBase := bases[wf], false
		if !slices.Contains(config.Bases, kind) {
			kind = ""
		}
		if berr != nil {
			add(reasonUnreadable(oneLineCLI(berr.Error())))
			remote = berrRemote
		} else {
			data, _, rerr := b.Read(ctx, imagecheck.RecordKey(slug, wf))
			switch {
			case errors.Is(rerr, blobx.ErrNotExist):
				add(reasonNoRecord())
			case rerr != nil:
				add(reasonUnreadable(oneLineCLI(rerr.Error())))
				remote = true
			default:
				if rec, perr := imagecheck.ParseRecord(data); perr != nil {
					add(reasonUnreadable(oneLineCLI(perr.Error())))
				} else {
					add(recordVersionReason(rec.FugaroVersion))
					add(baseRefReason(rec.BaseRef, kind))
				}
			}
		}
		if ref := lc.BaseImage(kind); kind != "" && ref != "" {
			if herr != nil {
				add(reasonRegistryHost(kind, herr))
			} else if v, managed := managedVersion(ref, host, kind); !managed {
				add(reasonConfigBaseCustom(ref, kind))
				customBase = true
			} else if imagePredates(v, gcpProjectFieldSince) {
				add(reasonConfigBaseOld(ref, kind, v))
			}
		}
		if len(reasons) > 0 {
			ps = append(ps, anchorProblem{text: anchorProblemText(repo, wf, kind, lcPath, reasons, customBase), remote: remote})
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
