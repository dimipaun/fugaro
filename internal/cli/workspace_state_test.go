package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// WU15's tests. Every fake and helper here carries the task's id (wu15…), so the
// tasks that land in this package later merge without a name clash (plan §1).
//
// Nothing here reaches a cloud, a network or a home directory: the three things
// a save touches outside itself — where the snapshot goes (stateDesignationReader),
// what it holds (stateContentReader) and how it is published (stateHost) — are
// the writer's seams, and every test drives them. The one test that runs a real
// save's GitHub path puts its own `git` and `gh` on PATH (testutil.FakeCommand),
// which is the seam the production host itself uses: no test asserts that a
// forge was reached, only that the commands a save would run are the ones it ran.

// wu15Designation answers one snapshot's E6 designation.
type wu15Designation struct {
	d   stateDesignation
	err error

	asked []workspace.Snapshot
}

func (f *wu15Designation) Designate(_ context.Context, s workspace.Snapshot) (stateDesignation, error) {
	f.asked = append(f.asked, s)
	if f.err != nil {
		return stateDesignation{}, f.err
	}
	return f.d, nil
}

// wu15Content answers the body of one snapshot, and records that it was asked
// and what it was asked about: a save that read nothing wrote nothing.
type wu15Content struct {
	body json.RawMessage
	err  error

	asked []workspace.Snapshot
}

func (f *wu15Content) Body(_ context.Context, s workspace.Snapshot) (json.RawMessage, error) {
	f.asked = append(f.asked, s)
	if f.err != nil {
		return nil, f.err
	}
	return f.body, nil
}

// wu15Host is the host one repository is on, as a save reaches it: it answers
// the visibility the test set and records every push, so a test sees the branch,
// the folder, the file and the bytes that went out — with nothing pushed.
//
// mark is called as the push begins and answers everything written so far: it is
// how a test holds "before it pushes it prints the target" to an order and not
// just to a substring, which a line printed after the push would also satisfy.
type wu15Host struct {
	visibility stateVisibility
	visErr     error
	url        string
	pubErr     error

	mark func() string

	asked  []string
	pushed []statePublish
	atPush string
}

func (h *wu15Host) Visibility(_ context.Context, repo string) (stateVisibility, error) {
	h.asked = append(h.asked, repo)
	if h.visErr != nil {
		return stateVisibilityUnknown, h.visErr
	}
	return h.visibility, nil
}

// OpenPR is what a save never asks: a save pushes its snapshot and opens the
// pull request for it, while reusing a pull request already open on a branch is
// the starter's own path (enrolOpenStarter).
func (h *wu15Host) OpenPR(context.Context, string, string) (string, error) { return "", nil }

func (h *wu15Host) Publish(_ context.Context, p statePublish) (string, error) {
	if h.mark != nil {
		h.atPush = h.mark()
	}
	h.pushed = append(h.pushed, p)
	if h.pubErr != nil {
		return "", h.pubErr
	}
	return h.url, nil
}

// wu15Save is one writer under test, with its seams in hand.
type wu15Save struct {
	dir  string
	now  time.Time
	env  map[string]string
	out  *strings.Builder
	des  *wu15Designation
	body *wu15Content
	host *wu15Host

	kinds []string
	w     *stateWriter
}

// wu15New builds the writer over a temp folder. repo is the command's --repo,
// "" for a save that takes the project's own designation.
func wu15New(t *testing.T, repo string) *wu15Save {
	t.Helper()
	s := &wu15Save{
		dir:  t.TempDir(),
		now:  time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC),
		env:  map[string]string{},
		out:  &strings.Builder{},
		des:  &wu15Designation{},
		body: &wu15Content{body: json.RawMessage(`{"settings":{"burn_alert":1},"spend":{"day":"2026-11-21"}}`)},
		host: &wu15Host{visibility: stateVisibilityPrivate, url: "https://example.invalid/acme/state/pull/7"},
	}
	s.host.mark = func() string { return s.out.String() }
	s.w = newStateWriter(stateWriterOptions{
		Dir:         s.dir,
		Out:         s.out,
		Getenv:      func(k string) string { return s.env[k] },
		Now:         func() time.Time { return s.now },
		Rand:        &wu15Rand{},
		Designation: s.des,
		Content:     s.body,
		Hosts: func(kind string) (stateHost, error) {
			s.kinds = append(s.kinds, kind)
			return s.host, nil
		},
		Repo: repo,
	})
	return s
}

// wu15Rand is the writer's random source in a test: a counter, so the branch a
// save names is the same string on every run of the test and two saves through
// one writer are still distinct draws (the real source is crypto/rand).
type wu15Rand struct{ n byte }

func (r *wu15Rand) Read(p []byte) (int, error) {
	for i := range p {
		r.n++
		p[i] = r.n
	}
	return len(p), nil
}

// wu15Member is the snapshot of the usual member project.
func wu15Member() workspace.Snapshot {
	return workspace.Snapshot{
		Workspace: "acme-workspace",
		Member:    workspace.MemberTarget{Project: "acme-app", Firebase: "acme-app-firebase"},
	}
}

// wu15Audit is the workspace's own audit export, which is not any member's.
func wu15Audit() workspace.Snapshot {
	return workspace.Snapshot{Workspace: "acme-workspace", Audit: true}
}

// wu15Read reads a local snapshot file back.
func wu15Read(t *testing.T, path string) stateSnapshotDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the snapshot a save named (%s): %v", path, err)
	}
	doc, err := stateDecodeSnapshot(raw)
	if err != nil {
		t.Fatalf("the file the save wrote at %s is not one its own reader reads: %v", path, err)
	}
	return doc
}

// wu15Files lists the files under dir, relative and sorted, so a test can say
// what a save left behind — including nothing.
func wu15Files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return out
}

// TestStateSaveAlwaysWritesLocal is the "always" of the acceptance: with no
// designated repository at all the local file is the whole save, it is named
// back to the caller and printed, it holds the body the reader answered under
// the instant the save was taken, and nothing anywhere was read or pushed.
//
// The audit export is the second half: it is the workspace's own file and not a
// member's, and it is the same save in every other respect.
func TestStateSaveAlwaysWritesLocal(t *testing.T) {
	ctx := context.Background()

	s := wu15New(t, "")
	got, err := s.w.Save(ctx, wu15Member())
	if err != nil {
		t.Fatalf("a save with no designated repository failed: %v", err)
	}

	want := filepath.Join(s.dir, "acme-workspace", stateMemberDir, "acme-app.json")
	if got.File != want {
		t.Errorf("Save answered File %q, want %q: the local file is the one thing every save leaves", got.File, want)
	}
	// The snapshot carries the addresses the project reaches (D2824), so it is
	// the operator's own alone: another account on this machine must not read it.
	wu15Mode(t, want, 0o600)
	wu15Mode(t, filepath.Dir(want), 0o700)
	if got.PullRequest != "" {
		t.Errorf("Save answered PullRequest %q with no repository designated, want empty", got.PullRequest)
	}
	if len(s.host.asked) != 0 || len(s.host.pushed) != 0 || len(s.kinds) != 0 {
		t.Errorf("a save with no designated repository reached a host: visibility asked %v, pushed %d, hosts opened %v",
			s.host.asked, len(s.host.pushed), s.kinds)
	}
	if len(s.body.asked) != 1 {
		t.Fatalf("the save read the body %d times, want once", len(s.body.asked))
	}

	doc := wu15Read(t, want)
	if doc.Workspace != "acme-workspace" || doc.Member != "acme-app" || doc.Audit {
		t.Errorf("the file names workspace %q, member %q, audit %v; want acme-workspace, acme-app, false", doc.Workspace, doc.Member, doc.Audit)
	}
	if !doc.Taken.Equal(s.now) {
		t.Errorf("the file records %s, want %s: the snapshot's time is the one it was taken at", doc.Taken, s.now)
	}
	var held, wantBody map[string]any
	if err := json.Unmarshal(doc.State, &held); err != nil {
		t.Fatalf("the file's state is not JSON: %v", err)
	}
	if err := json.Unmarshal(s.body.body, &wantBody); err != nil {
		t.Fatalf("the test's own body is not JSON: %v", err)
	}
	if !wu15SameJSON(held, wantBody) {
		t.Errorf("the file holds %v, want the reader's body %v: a save writes what the reader answered and invents nothing", held, wantBody)
	}
	if !strings.Contains(s.out.String(), want) {
		t.Errorf("the save did not print the file it wrote:\n%s", s.out.String())
	}
	if !strings.Contains(s.out.String(), "no state repository is designated") {
		t.Errorf("the save did not say why there is no pull request:\n%s", s.out.String())
	}

	// The workspace's own export is the workspace's file, beside the members'.
	a := wu15New(t, "")
	agot, err := a.w.Save(ctx, wu15Audit())
	if err != nil {
		t.Fatalf("saving the audit export failed: %v", err)
	}
	wantAudit := filepath.Join(a.dir, "acme-workspace", stateAuditFileName)
	if agot.File != wantAudit {
		t.Errorf("the audit export went to %q, want %q", agot.File, wantAudit)
	}
	adoc := wu15Read(t, wantAudit)
	if !adoc.Audit || adoc.Member != "" {
		t.Errorf("the audit file says audit %v, member %q; want true and no member: the export is not any member's", adoc.Audit, adoc.Member)
	}
}

// wu15SameJSON compares two decoded JSON values structurally, so a test does not
// depend on key order or on how a number was spelled.
func wu15SameJSON(a, b any) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	var an, bn any
	if json.Unmarshal(ab, &an) != nil || json.Unmarshal(bb, &bn) != nil {
		return false
	}
	return string(ab) == string(bb)
}

// TestStateSaveOpensPR is a save with E6's designated repository set: the local
// file and then a pull request — the repository asked about first, then the
// snapshot committed to a new branch inside the designated folder, and the
// pull request's own branch as what it targets.
func TestStateSaveOpensPR(t *testing.T) {
	s := wu15New(t, "")
	s.des.d = stateDesignation{
		Repo:     "acme/state",
		Dir:      "docs/state",
		Provider: gitprov.KindGitHub,
		Source:   "the local project layer's reports_commit",
	}

	got, err := s.w.Save(context.Background(), wu15Member())
	if err != nil {
		t.Fatalf("saving to a designated repository failed: %v", err)
	}
	if got.PullRequest != s.host.url {
		t.Errorf("Save answered PullRequest %q, want the host's own %q", got.PullRequest, s.host.url)
	}
	if len(s.host.asked) != 1 || s.host.asked[0] != "acme/state" {
		t.Errorf("the save read the visibility of %v, want one read of acme/state: the repository it is about to push into", s.host.asked)
	}
	if len(s.kinds) != 1 || s.kinds[0] != gitprov.KindGitHub {
		t.Errorf("the save opened hosts %v, want one GitHub host", s.kinds)
	}
	if len(s.host.pushed) != 1 {
		t.Fatalf("the save pushed %d times, want once", len(s.host.pushed))
	}
	p := s.host.pushed[0]
	if p.Repo != "acme/state" || p.Dir != "docs/state" {
		t.Errorf("the snapshot went to %s folder %q, want acme/state folder docs/state", p.Repo, p.Dir)
	}
	if p.File != stateMemberDir+"/acme-app.json" || p.Path() != "docs/state/"+stateMemberDir+"/acme-app.json" {
		t.Errorf("the snapshot's file is %q at %q, want members/acme-app.json at docs/state/members/acme-app.json", p.File, p.Path())
	}
	if !strings.HasPrefix(p.Branch, "fugaro/state-") {
		t.Errorf("the save pushed branch %q, want one under fugaro/state- so it cannot touch a person's own branch", p.Branch)
	}

	local, err := os.ReadFile(got.File)
	if err != nil {
		t.Fatalf("reading the local file the save named: %v", err)
	}
	if string(local) != string(p.Bytes) {
		t.Errorf("the pushed bytes are not the local file's:\nlocal: %s\npushed: %s", local, p.Bytes)
	}
	if p.Subject == "" || p.Body == "" {
		t.Errorf("the commit subject %q and pull request body %q: both are the pull request a reader sees", p.Subject, p.Body)
	}
	if !strings.Contains(s.out.String(), s.host.url) {
		t.Errorf("the save did not print the pull request it opened:\n%s", s.out.String())
	}
}

// TestStateSaveTwoSavesAtOneInstantPushTwoBranches: one command saves several
// members — `workspace project save` with no argument saves every one of them —
// so two of its saves carry the same second. A branch named after the instant
// alone would be one branch: the second push is rejected as a non-fast-forward,
// because that branch is already on the host with another commit, and every
// member after it is left with its local snapshot and no pull request at all.
//
// So the branch names the document and carries a random suffix. The two saves
// here are of the SAME document at the same instant, which is the case the
// document's own name in the branch cannot tell apart: only the suffix can.
func TestStateSaveTwoSavesAtOneInstantPushTwoBranches(t *testing.T) {
	s := wu15New(t, "")
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: "the project layer"}

	for i := 1; i <= 2; i++ {
		if _, err := s.w.Save(context.Background(), wu15Member()); err != nil {
			t.Fatalf("save %d of two at one instant failed: %v", i, err)
		}
	}
	if len(s.host.pushed) != 2 {
		t.Fatalf("the two saves pushed %d times, want two", len(s.host.pushed))
	}
	first, second := s.host.pushed[0].Branch, s.host.pushed[1].Branch
	if first == second {
		t.Errorf("two saves of one document at one instant pushed the branch %q twice, want two branches: the second push is rejected as a non-fast-forward and that member is left with no pull request", first)
	}
	// And the name says which document it is, so a person reading the branch
	// list can tell one member's save from another's.
	for i, branch := range []string{first, second} {
		if want := "fugaro/state-20261121-093000-acme-workspace-acme-app-"; !strings.HasPrefix(branch, want) {
			t.Errorf("save %d pushed the branch %q, want the instant, the workspace and the member in it (%s...)", i+1, branch, want)
		}
	}
	// The audit export is a document of the same workspace and the same second,
	// and it is named as itself.
	if _, err := s.w.Save(context.Background(), wu15Audit()); err != nil {
		t.Fatalf("saving the workspace's audit export failed: %v", err)
	}
	if got := s.host.pushed[2].Branch; !strings.HasPrefix(got, "fugaro/state-20261121-093000-acme-workspace-audit-") {
		t.Errorf("the audit export pushed the branch %q, want the workspace and audit in it", got)
	}
}

// TestStateSaveRefusesAPublicRepo is D2824's fence, and the red step: a snapshot
// carries the addresses of the people the project reaches, so a public
// designated repository is refused — naming the --repo override — and refused
// before anything is read or written, because a save that may not be pushed must
// not leave a snapshot behind either.
func TestStateSaveRefusesAPublicRepo(t *testing.T) {
	s := wu15New(t, "")
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: "the project layer"}
	s.host.visibility = stateVisibilityPublic

	_, err := s.w.Save(context.Background(), wu15Member())
	if err == nil {
		t.Fatal("a save into a public designated repository succeeded, want a refusal: the snapshot carries addresses (D2824)")
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("the refusal exits %d, want %d: a fence, not a failure", code, ExitUserError)
	}
	if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("the refusal does not name the --repo override, which is the one way through it: %v", err)
	}
	if len(s.host.pushed) != 0 {
		t.Errorf("the refused save pushed %d times, want none", len(s.host.pushed))
	}
	if len(s.body.asked) != 0 {
		t.Errorf("the refused save read the body of %v, want nothing read: a snapshot that is refused must not be built", s.body.asked)
	}
	if files := wu15Files(t, s.dir); len(files) != 0 {
		t.Errorf("the refused save left %v behind, want nothing: nothing about a refused save is half-done", files)
	}
}

// TestStateSaveRefusesAVisibilityItCannotTrust: the fence is fail-closed. A
// host that answers "unknown" and no error — nothing to stop the save, and no
// private answer to let it through — must not be the answer that turns the fence
// off. What the fence guards is a push of a snapshot carrying the addresses of
// the people the project reaches (D2824), and a repository taken for private
// when it is not is exactly the leak it is about, so only a repository the save
// READ as private is saved into.
func TestStateSaveRefusesAVisibilityItCannotTrust(t *testing.T) {
	s := wu15New(t, "")
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: "the project layer"}
	s.host.visibility = stateVisibilityUnknown

	_, err := s.w.Save(context.Background(), wu15Member())
	if err == nil {
		t.Fatal("a save into a repository whose visibility was answered unknown went through, want the fence's refusal: nothing about that repository is known to be private")
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("the refusal exits %d, want %d: a fence, not a failure", code, ExitUserError)
	}
	if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("the refusal does not name the --repo override, which is the one way through it: %v", err)
	}
	if strings.Contains(err.Error(), "the repository is public") {
		t.Errorf("the refusal says the repository is public, which nobody said it was: %v", err)
	}
	if len(s.host.pushed) != 0 {
		t.Errorf("the refused save pushed %d times, want none", len(s.host.pushed))
	}
	if len(s.body.asked) != 0 {
		t.Errorf("the refused save read the body of %v, want nothing read: a snapshot that is refused must not be built", s.body.asked)
	}
	if files := wu15Files(t, s.dir); len(files) != 0 {
		t.Errorf("the refused save left %v behind, want nothing: nothing about a refused save is half-done", files)
	}
}

// TestStateSaveNamesTheTargetAndItsSource is D2824's other half: the operator
// sees the repository, the folder and where the setting came from, and sees them
// BEFORE the push — a line printed after it would leave them reading a snapshot
// they could not have headed off.
func TestStateSaveNamesTheTargetAndItsSource(t *testing.T) {
	const source = "the owner's local project config, reports_commit.repo"
	s := wu15New(t, "")
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: source}

	if _, err := s.w.Save(context.Background(), wu15Member()); err != nil {
		t.Fatalf("saving to a designated repository failed: %v", err)
	}
	if s.host.atPush == "" {
		t.Fatal("the test's host was never asked to push")
	}
	// The host is named too: the repository and its folder are not the whole
	// target, because the same owner/name on another forge is another
	// repository, and the operator is the one who can tell that the save is
	// about to reach the host they meant.
	for _, want := range []string{"acme/state", source, "github.com"} {
		if !strings.Contains(s.host.atPush, want) {
			t.Errorf("what the save had printed when it pushed does not name %q:\n%s", want, s.host.atPush)
		}
	}
}

// TestStateSaveRepoOverrideKeepsTheProjectsHost is the --repo override, which
// names a repository and no host: a Bitbucket project's override must not be
// read as a GitHub one (it used to be, outright, and the save then pushed at
// github.com/<owner>/<name> — a host the operator never named and never saw).
//
// The second half is the project whose own designation cannot be read at all:
// --repo is the way through exactly then (D2824 names it as one), so the save
// goes ahead on this build's default kind — and says which host that is in the
// same line, rather than pushing into it unseen.
func TestStateSaveRepoOverrideKeepsTheProjectsHost(t *testing.T) {
	t.Run("the project's own kind", func(t *testing.T) {
		s := wu15New(t, "acme/override")
		s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindBitbucket, Source: "the project layer"}

		got, err := s.w.Save(context.Background(), wu15Member())
		if err != nil {
			t.Fatalf("a save with --repo failed: %v", err)
		}
		if len(s.kinds) != 1 || s.kinds[0] != gitprov.KindBitbucket {
			t.Errorf("the save opened hosts %v, want the project's own %s", s.kinds, gitprov.KindBitbucket)
		}
		if len(s.host.asked) != 1 || s.host.asked[0] != "acme/override" {
			t.Errorf("the save read the visibility of %v, want the repository --repo named", s.host.asked)
		}
		p := s.host.pushed[0]
		if p.Repo != "acme/override" || p.Dir != stateDefaultRepoDir {
			t.Errorf("the snapshot went to %s folder %q, want acme/override folder %s", p.Repo, p.Dir, stateDefaultRepoDir)
		}
		if !strings.Contains(s.host.atPush, "bitbucket.org") {
			t.Errorf("what the save had printed when it pushed does not name the host it reaches:\n%s", s.host.atPush)
		}
		if !strings.Contains(s.host.atPush, "--repo") {
			t.Errorf("what the save had printed when it pushed does not say the repository came from --repo:\n%s", s.host.atPush)
		}
		if got.PullRequest != s.host.url {
			t.Errorf("Save answered PullRequest %q, want the host's %q", got.PullRequest, s.host.url)
		}
	})

	t.Run("a designation that cannot be read", func(t *testing.T) {
		s := wu15New(t, "acme/override")
		s.des.err = errors.New("the project config holds no reports_commit")

		if _, err := s.w.Save(context.Background(), wu15Member()); err != nil {
			t.Fatalf("a save with --repo failed over a designation it did not need: %v", err)
		}
		if len(s.host.pushed) != 1 {
			t.Fatalf("the save pushed %d times, want once through the default host", len(s.host.pushed))
		}
		for _, want := range []string{"github.com", "reports_commit"} {
			if !strings.Contains(s.host.atPush, want) {
				t.Errorf("what the save had printed when it pushed does not name %q: the host it fell back to, and why:\n%s", want, s.host.atPush)
			}
		}
	})
}

// TestStateSaveRefusedInAgentSession is A7: the snapshot leaves this machine
// under the owner's own credentials, so a coding agent's session is refused and
// nothing is read, written or pushed first.
func TestStateSaveRefusedInAgentSession(t *testing.T) {
	s := wu15New(t, "")
	s.env["CLAUDECODE"] = "1"
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: "the project layer"}

	_, err := s.w.Save(context.Background(), wu15Member())
	if err == nil {
		t.Fatal("a save in a coding agent's session succeeded, want a refusal (A7)")
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("the refusal exits %d, want %d", code, ExitUserError)
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker that made this an agent's session: %v", err)
	}
	if len(s.des.asked) != 0 || len(s.body.asked) != 0 || len(s.host.asked) != 0 || len(s.host.pushed) != 0 {
		t.Errorf("the refused save did work anyway: designation %d, body %d, visibility %v, pushes %d",
			len(s.des.asked), len(s.body.asked), s.host.asked, len(s.host.pushed))
	}
	if files := wu15Files(t, s.dir); len(files) != 0 {
		t.Errorf("the refused save left %v behind, want nothing", files)
	}
}

// TestLastSaved is the read half: no snapshot is found==false and not the zero
// time, a save is found at the instant it recorded, and a later save moves it on
// — which is what `status`'s "state repo in sync" row reads.
func TestLastSaved(t *testing.T) {
	ctx := context.Background()
	s := wu15New(t, "")
	snap := wu15Member()

	if at, found, err := s.w.LastSaved(ctx, snap); err != nil || found || !at.IsZero() {
		t.Fatalf("LastSaved answered (%s, %v, %v) before any save, want (zero, false, nil)", at, found, err)
	}

	if _, err := s.w.Save(ctx, snap); err != nil {
		t.Fatalf("saving failed: %v", err)
	}
	at, found, err := s.w.LastSaved(ctx, snap)
	if err != nil || !found {
		t.Fatalf("LastSaved answered (%s, %v, %v) after a save, want the save's time and found", at, found, err)
	}
	if !at.Equal(s.now) {
		t.Errorf("LastSaved answered %s, want the instant the snapshot records (%s)", at, s.now)
	}

	// Another member's snapshot is a different file: a project that was never
	// saved is not the one that was.
	other := workspace.Snapshot{Workspace: "acme-workspace", Member: workspace.MemberTarget{Project: "acme-other"}}
	if at, found, err := s.w.LastSaved(ctx, other); err != nil || found || !at.IsZero() {
		t.Errorf("LastSaved answered (%s, %v, %v) for a member never saved, want (zero, false, nil)", at, found, err)
	}

	s.now = s.now.Add(26 * time.Hour)
	if _, err := s.w.Save(ctx, snap); err != nil {
		t.Fatalf("the second save failed: %v", err)
	}
	if at, _, err := s.w.LastSaved(ctx, snap); err != nil || !at.Equal(s.now) {
		t.Errorf("LastSaved answered (%s, %v), want the later save's instant %s", at, err, s.now)
	}
}

// TestStateSaveNamesALocalFolderOfItsOwn: a save that names no folder keeps its
// files in the state directory the rest of the CLI keeps its own in (F5), under
// the workspace's and the member's names — so the folder `status` reads on a
// machine whose command never named one is where a person already looks, and a
// save and a later read cannot disagree about it.
func TestStateSaveNamesALocalFolderOfItsOwn(t *testing.T) {
	state := t.TempDir()
	ctx := context.Background()
	s := wu15New(t, "")
	s.w.dir = ""
	s.env["XDG_STATE_HOME"] = state

	got, err := s.w.Save(ctx, wu15Member())
	if err != nil {
		t.Fatalf("a save with no folder named failed: %v", err)
	}
	want := filepath.Join(state, "fugaro", "state", "acme-workspace", stateMemberDir, "acme-app.json")
	if got.File != want {
		t.Errorf("the save wrote %q, want %q: the state directory, so the file is where a person expects it", got.File, want)
	}
	if _, found, err := s.w.LastSaved(ctx, wu15Member()); err != nil || !found {
		t.Errorf("LastSaved answered found=%v err=%v for the file the same writer just wrote", found, err)
	}
}

// TestStateSaveRefusesANameThatIsNotOnePathSegment: the local file is
// <folder>/<workspace>/<member>.json, so both names are path segments — and the
// guard is the only thing between a name and a write outside the folder the
// operator named. A workspace or member that is not one segment ("../escape",
// "acme/other", a backslash, an empty name) must refuse the save outright: the
// writer is a seam WU6, WU11 and WU23 call, and the names upstream come from
// projects, whose names this build holds to [a-z0-9-] — so nothing else would
// notice a name that is not.
func TestStateSaveRefusesANameThatIsNotOnePathSegment(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap workspace.Snapshot
		what string
	}{
		{
			"a workspace that climbs out of the folder",
			workspace.Snapshot{Workspace: "../escape", Member: workspace.MemberTarget{Project: "acme-app"}},
			"the workspace",
		},
		{
			"a workspace that is two segments",
			workspace.Snapshot{Workspace: "acme/other", Member: workspace.MemberTarget{Project: "acme-app"}},
			"the workspace",
		},
		{
			"a workspace with no name",
			workspace.Snapshot{Member: workspace.MemberTarget{Project: "acme-app"}},
			"the workspace",
		},
		{
			"a member that is two segments",
			workspace.Snapshot{Workspace: "acme-workspace", Member: workspace.MemberTarget{Project: "acme/app"}},
			"the member project",
		},
		{
			"a member that climbs out of the folder",
			workspace.Snapshot{Workspace: "acme-workspace", Member: workspace.MemberTarget{Project: ".."}},
			"the member project",
		},
		{
			"a member with a backslash",
			workspace.Snapshot{Workspace: "acme-workspace", Member: workspace.MemberTarget{Project: `acme\app`}},
			"the member project",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := wu15New(t, "")
			_, err := s.w.Save(context.Background(), tc.snap)
			if err == nil {
				t.Fatal("a save whose workspace or member name is not one path segment succeeded, want a refusal: the file it names is written outside the folder the operator gave")
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("the refusal exits %d, want %d: a name the operator can fix, not a failure", code, ExitUserError)
			}
			if !strings.Contains(err.Error(), tc.what) || !strings.Contains(err.Error(), "not one path segment") {
				t.Errorf("the refusal does not say which name is not one path segment, so the operator cannot tell what to fix: %v", err)
			}
			if files := wu15Files(t, s.dir); len(files) != 0 {
				t.Errorf("the refused save left %v behind, want nothing: the name is refused before anything is written", files)
			}
		})
	}
}

// TestStateSaveAMemberCalledAuditIsNotTheWorkspacesExport: "audit" is a legal
// project name (config.ProjectNameRE), so a member project called audit and the
// workspace's own audit export are two documents — and a folder that told them
// apart by the file's name alone made them one: the same local path, the same
// path inside the designated repository, the second save overwriting the first,
// and LastSaved answering the other document's instant, which is exactly what
// D2824's "state repo in sync" row reads.
func TestStateSaveAMemberCalledAuditIsNotTheWorkspacesExport(t *testing.T) {
	ctx := context.Background()
	s := wu15New(t, "")
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: "the project layer"}

	export := wu15Audit()
	member := wu15Member()
	member.Member.Project = "audit"

	first, err := s.w.Save(ctx, export)
	if err != nil {
		t.Fatalf("saving the audit export failed: %v", err)
	}
	exportAt := s.now
	s.now = s.now.Add(time.Hour)
	second, err := s.w.Save(ctx, member)
	if err != nil {
		t.Fatalf("saving a member called audit failed: %v", err)
	}
	memberAt := s.now

	if first.File == second.File {
		t.Fatalf("the workspace's audit export and a member project called audit were written to one file, %q: the second save overwrites the first", first.File)
	}
	for path, want := range map[string]bool{first.File: true, second.File: false} {
		if doc := wu15Read(t, path); doc.Audit != want {
			t.Errorf("%s holds audit %v, want %v: the two documents are not one", path, doc.Audit, want)
		}
	}
	if len(s.host.pushed) != 2 {
		t.Fatalf("the save pushed %d snapshots, want the two", len(s.host.pushed))
	}
	if s.host.pushed[0].Path() == s.host.pushed[1].Path() {
		t.Errorf("both documents were committed at %q inside the designated repository", s.host.pushed[0].Path())
	}
	for _, tc := range []struct {
		what string
		snap workspace.Snapshot
		at   time.Time
	}{
		{"the audit export", export, exportAt},
		{"the member called audit", member, memberAt},
	} {
		at, found, err := s.w.LastSaved(ctx, tc.snap)
		if err != nil || !found || !at.Equal(tc.at) {
			t.Errorf("LastSaved for %s answered (%s, found=%v, err=%v), want its own instant %s", tc.what, at, found, err, tc.at)
		}
	}
}

// wu15Mode asserts the permission bits of a path a save wrote: a snapshot
// carries the addresses the project reaches (D2824), so it is the operator's own
// alone on a machine other accounts can log in to.
func wu15Mode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the mode of %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has mode %04o, want %04o", path, got, want)
	}
}

// TestStateSaveFileRefusesWhatItCannotRead: the local file is one a person
// may edit and a newer Fugaro may write, so a version this build does not read,
// an unknown key and a file with no time are refusals and never half a snapshot.
func TestStateSaveFileRefusesWhatItCannotRead(t *testing.T) {
	base := `{"version":1,"taken":"2026-11-21T09:30:00Z","workspace":"acme-workspace","member":"acme-app","state":{}}`
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"a version this build does not write", `{"version":2,"taken":"2026-11-21T09:30:00Z","workspace":"w","state":{}}`},
		{"a key this build does not know", `{"version":1,"taken":"2026-11-21T09:30:00Z","workspace":"w","state":{},"extra":true}`},
		{"no time", `{"version":1,"workspace":"w","state":{}}`},
		{"not an object at all", `[]`},
	} {
		if _, err := stateDecodeSnapshot([]byte(tc.raw)); err == nil {
			t.Errorf("%s: stateDecodeSnapshot accepted it, want a refusal", tc.name)
		}
	}
	if _, err := stateDecodeSnapshot([]byte(base)); err != nil {
		t.Errorf("stateDecodeSnapshot refused the file a save writes: %v", err)
	}
}

// TestStateSaveHostIsTheRepositorysOwn: the GitHub host runs `gh` for the
// visibility read and for the pull request, and the owner's own git for the
// clone, the commit and the push (D2807) — no token of Fugaro's anywhere. The
// commands are the test's own, on PATH, and the assert is on what a save ran.
func TestStateSaveHostIsTheRepositorysOwn(t *testing.T) {
	gitCalls := wu15FakeGit(t)
	ghCalls := wu15FakeGH(t, "PRIVATE")

	s := wu15New(t, "")
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: "the project layer"}
	s.w.hosts = stateHostsFor("")

	got, err := s.w.Save(context.Background(), wu15Member())
	if err != nil {
		t.Fatalf("a save through the GitHub host failed: %v", err)
	}
	if got.PullRequest != "https://example.invalid/acme/state/pull/7" {
		t.Errorf("the save answered PullRequest %q, want the URL the fake gh printed", got.PullRequest)
	}
	ran := wu15Calls(t, gitCalls, ghCalls)
	for _, want := range []string{
		"repo view github.com/acme/state --json visibility",
		"clone --quiet -- https://github.com/acme/state.git",
		"checkout --quiet -b fugaro/state-20261121-093000-acme-workspace-acme-app-01020304",
		"add -- docs/state/members/acme-app.json",
		"push --quiet origin fugaro/state-20261121-093000-acme-workspace-acme-app-01020304",
		"pr create --repo github.com/acme/state --head fugaro/state-20261121-093000-acme-workspace-acme-app-01020304 --base main",
	} {
		if !strings.Contains(ran, want) {
			t.Errorf("the save never ran %q. Everything it ran:\n%s", want, ran)
		}
	}
	// `gh pr create` has no --json — the real gh answers "unknown flag: --json"
	// and exits 1, so a save that asked for one would push the branch and then
	// fail without opening anything — and the fake gh refuses it the way gh does
	// (wu15FakeGH), which is what makes a save that asks for it fail here. The
	// recorded argv is asserted too, because the pull request's body is a
	// multi-line argument and an invocation is therefore not one line of the
	// calls file: the pull request's own call is the last thing gh is asked.
	ghRan := wu15Calls(t, ghCalls)
	if i := strings.Index(ghRan, "pr create"); i >= 0 && strings.Contains(ghRan[i:], "--json") {
		t.Errorf("the save asked `gh pr create` for JSON, which that subcommand does not have:\n%s", ghRan[i:])
	}
	// git is never allowed to ask a person for a username: this save hands git
	// no environment of its own (gh's own login authenticates the push), so the
	// variable has to be one the save adds.
	if got := wu15GitEnv(t, gitCalls)["GIT_TERMINAL_PROMPT"]; got != "0" {
		t.Errorf("the save ran git with GIT_TERMINAL_PROMPT=%q, want 0: a clone or a push git cannot authenticate must fail rather than open a terminal and wait for someone to type a username", got)
	}
	// And no hook of the repository's or the person's runs: every git a save
	// spawns opens with internal/cli's hardening (gitHardening), which is what
	// turns core.hooksPath off, and --no-verify would not have been enough —
	// prepare-commit-msg, post-checkout and pre-push are not covered by it.
	for _, line := range wu15GitLines(t, gitCalls) {
		if !strings.HasPrefix(line, strings.Join(gitHardening, " ")) {
			t.Errorf("the save ran `git %s` without the hardening every other git call in this tree carries, so a hook of the repository's or the person's would run as this user", line)
		}
	}
	// The URL is read off gh's output, which is the one line `gh pr create`
	// prints on success.
	if url := statePRURL("A new release of gh is available\nhttps://example.invalid/acme/state/pull/7\n"); url != "https://example.invalid/acme/state/pull/7" {
		t.Errorf("statePRURL read %q off gh's output, want the URL: a notice before it must not be taken for the pull request", url)
	}
	if url := statePRURL("opened a pull request\n"); url != "" {
		t.Errorf("statePRURL read %q off an answer that holds no URL, want nothing so the save refuses", url)
	}
}

// TestStateSaveGHHostCannotMoveTheRepository: the repository a save's fence
// reads and the repository it pushes to are the same one, whatever the
// environment says. Both gh calls write the host into the repository argument
// (the [HOST/]OWNER/REPO form gh takes), and gh is handed GHEnv's environment,
// with GH_HOST and GH_REPO dropped.
//
// The scenario this holds off (F22, the same one issues.GHEnv and
// internal/watch's ask guard against): a person logged in to an enterprise
// GitHub as well, with GH_HOST exported. A
// bare owner/name is resolved against that default host, so the visibility read
// would be about ghe.corp's acme/state — private — while the clone and the push
// went to github.com/acme/state, which may be public: the fence would pass and
// the snapshot, which carries the addresses the project reaches (D2824), would
// land in a public repository, with `gh pr create` failing only afterwards.
func TestStateSaveGHHostCannotMoveTheRepository(t *testing.T) {
	t.Setenv("GH_HOST", "ghe.corp.example")
	t.Setenv("GH_REPO", "someone/else")

	gitCalls := wu15FakeGit(t)
	ghCalls := wu15FakeGH(t, "PRIVATE")

	s := wu15New(t, "")
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: "the project layer"}
	s.w.hosts = stateHostsFor("")

	if _, err := s.w.Save(context.Background(), wu15Member()); err != nil {
		t.Fatalf("a save through the GitHub host failed: %v", err)
	}
	ran := wu15Calls(t, ghCalls)
	for _, want := range []string{
		"repo view github.com/acme/state --json visibility",
		"pr create --repo github.com/acme/state --head",
	} {
		if !strings.Contains(ran, want) {
			t.Errorf("the save never ran %q, so with GH_HOST exported its gh resolved the repository against the wrong host. Everything gh ran:\n%s", want, ran)
		}
	}
	// The other direction: a bare repository is what GH_HOST moves, so neither
	// call may name one — that is the mutation this test exists for.
	for _, bare := range []string{"view acme/state", "--repo acme/state"} {
		if strings.Contains(ran, bare) {
			t.Errorf("the save ran gh with %q, which its default host resolves. Everything gh ran:\n%s", bare, ran)
		}
	}
	// The clone and the push are the other half: they go to github.com whatever
	// GH_HOST says, which is the repository the fence above has to have been
	// about.
	if cloned := wu15Calls(t, gitCalls); !strings.Contains(cloned, "clone --quiet -- https://github.com/acme/state.git") {
		t.Errorf("the save's clone did not name github.com:\n%s", cloned)
	}
	// And the environment itself, for both calls: an inherited GH_HOST or
	// GH_REPO is dropped, and GH_PROMPT_DISABLED is set, because no command of
	// this tree may leave anything able to ask a person a question
	// (docs/no-prompt-policy).
	for _, sub := range []string{"repo", "pr"} {
		env := wu15GHEnv(t, ghCalls, sub)
		for _, name := range []string{"GH_HOST", "GH_REPO"} {
			if v, ok := env[name]; ok {
				t.Errorf("the save handed the `gh %s` call %s=%q, want it dropped: gh would resolve the repository against it", sub, name, v)
			}
		}
		if got := env["GH_PROMPT_DISABLED"]; got != "1" {
			t.Errorf("the save ran `gh %s` with GH_PROMPT_DISABLED=%q, want 1: a gh that could ask would wait on a terminal", sub, got)
		}
	}
}

// TestStateSaveGitHubVisibilityIsTheShippedFence: the fence is only as good as
// the mapping a save really runs, and GitHub is the provider this build pushes
// to. Every test above drives the fence through the writer's own host seam, which
// says nothing about stateGitHubHost.Visibility — so mapping gh's PUBLIC answer
// to private, the fence off for the provider a save actually reaches, passed the
// whole suite.
//
// This drives the shipped mapping: a real `gh` on PATH answers the word, and the
// answer decides. The words are gh's own (`gh repo view --json visibility`), and
// the last case is the one the switch's default arm is for: a word this build
// does not know must never be read as private.
func TestStateSaveGitHubVisibilityIsTheShippedFence(t *testing.T) {
	for _, tc := range []struct {
		answer  string
		refused bool
		fence   bool // the refusal is D2824's own, which names the --repo override
		why     string
	}{
		{"PUBLIC", true, true, "a public repository is exactly what D2824 refuses: a snapshot carries the addresses of the people the project reaches"},
		{"PRIVATE", false, false, "gh's own private answer is the one answer a save may go through on"},
		{"private", false, false, "gh's word in another case is still the private answer"},
		{"INTERNAL", false, false, "an enterprise-internal repository is readable by the enterprise and not by the world, so it is not the public repository D2824 is about"},
		{"SECRET", true, false, "a visibility this build does not know, read as private, is the public repository the fence exists to refuse"},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			gitCalls := wu15FakeGit(t)
			ghCalls := wu15FakeGH(t, tc.answer)

			s := wu15New(t, "")
			s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindGitHub, Source: "the project layer"}
			s.w.hosts = stateHostsFor("")

			got, err := s.w.Save(context.Background(), wu15Member())
			if !tc.refused {
				if err != nil {
					t.Fatalf("a save into a repository gh answered %s failed: %v (%s)", tc.answer, err, tc.why)
				}
				if got.PullRequest == "" {
					t.Errorf("the save of a repository gh answered %s opened no pull request, want one: %s", tc.answer, tc.why)
				}
				return
			}
			if err == nil {
				t.Fatalf("a save into a repository gh answered %s went through, want the fence's refusal: %s", tc.answer, tc.why)
			}
			if tc.fence && !strings.Contains(err.Error(), "--repo") {
				t.Errorf("the refusal does not name the --repo override, which is the one way through it: %v", err)
			}
			if files := wu15Files(t, s.dir); len(files) != 0 {
				t.Errorf("the refused save left %v behind, want nothing: nothing about a refused save is half-done", files)
			}
			// The push is what the fence is about, so the refusal has to land
			// before the clone that heads it: a snapshot gh was asked about and
			// refused must not have been committed and pushed first.
			if cloned := wu15Calls(t, gitCalls); strings.Contains(cloned, "clone") {
				t.Errorf("the refused save still ran git:\n%s", cloned)
			}
			if ran := wu15Calls(t, ghCalls); strings.Contains(ran, "pr create") {
				t.Errorf("the refused save still opened a pull request:\n%s", ran)
			}
		})
	}
}

// TestStateSaveBodyIsTheProjectsOwnState: the body a member's snapshot holds
// is the project's published settings node and the day's spend counters, read
// from the project's own two stores (its Realtime Database) — the "settings and
// spend summary" of the acceptance, and nothing invented.
//
// The audit export is the other half: the workspace's own Firestore collection,
// each entry decoded and re-encoded through C1's own codec, oldest first, so the
// export restates no field of an event and a reader of the file reads what the
// trail stores.
func TestStateSaveBodyIsTheProjectsOwnState(t *testing.T) {
	ctx := context.Background()
	fixed := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	day := budget.Day(fixed)

	db, fs := gcpfake.NewRTDB(t), gcpfake.NewFirestore(t)
	db.SetClock(func() time.Time { return fixed })
	// Shallowest first: the fake's Set replaces the node it is given, so a
	// later write of "config" would take the policy child with it.
	db.Set("config", map[string]any{"mode": "observe"})
	db.Set(projectpolicy.PathShared(), map[string]any{"burn_alert": 12.5})
	db.Set(budget.PathSpendGlobal(day), map[string]any{"micros": 1234})
	db.Set("spend/"+budget.DayKey(day)+"/repos", map[string]any{"acme/app": map[string]any{"micros": 99}})

	lc := &localcfg.Config{
		Name:       "acme-app",
		GCPProject: "acme-app",
		Budget:     &localcfg.Budget{RTDBURL: db.URL, FirebaseProject: "acme-app-firebase"},
		Endpoints:  localcfg.Endpoints{NoAuth: true, Firestore: fs.URL},
	}
	client, err := newBudgetClient(ctx, db.URL, true)
	if err != nil {
		t.Fatalf("opening the fake database: %v", err)
	}
	fsc, err := newReportFirestore(ctx, lc)
	if err != nil {
		t.Fatalf("opening the fake Firestore: %v", err)
	}

	// The workspace's trail, two events whose ids are in time order, seeded the
	// way C1 writes them.
	older, err := workspace.NewAuditEventID(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC), bytes.NewReader(make([]byte, 8)))
	if err != nil {
		t.Fatal(err)
	}
	newer, err := workspace.NewAuditEventID(time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC), bytes.NewReader(bytes.Repeat([]byte{1}, 8)))
	if err != nil {
		t.Fatal(err)
	}
	for id, at := range map[string]time.Time{newer: time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC), older: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)} {
		fs.Set(workspace.AuditCollection, id, map[string]any{
			"version": workspace.AuditEventVersion,
			"id":      id,
			"time":    at.Format(time.RFC3339),
			"actor":   "owner@example.com",
			"action":  string(workspace.ActionProjectAdd),
			"subject": "acme-app",
		})
	}

	c := stateProjectContent{lc: lc, db: client, fs: fsc}

	raw, err := c.Body(ctx, wu15Member())
	if err != nil {
		t.Fatalf("reading a member's body: %v", err)
	}
	var state struct {
		Settings map[string]any `json:"settings"`
		Spend    struct {
			Day    string         `json:"day"`
			Caps   map[string]any `json:"caps"`
			Global map[string]any `json:"global"`
			Repos  map[string]any `json:"repos"`
		} `json:"spend"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("the body a save would write is not JSON: %v\n%s", err, raw)
	}
	if state.Settings["burn_alert"] != 12.5 {
		t.Errorf("the settings are %v, want the published policy node's own", state.Settings)
	}
	if state.Spend.Day != budget.DayDate(day) {
		t.Errorf("the spend summary is about %q, want %q: the day the counters are read for", state.Spend.Day, budget.DayDate(day))
	}
	if state.Spend.Caps["mode"] != "observe" {
		t.Errorf("the caps in force are %v, want the database's config node", state.Spend.Caps)
	}
	if state.Spend.Global["micros"] != float64(1234) {
		t.Errorf("the global spend is %v, want the day's counters", state.Spend.Global)
	}
	if _, ok := state.Spend.Repos["acme/app"]; !ok {
		t.Errorf("the per-repository spend is %v, want the day's repos node", state.Spend.Repos)
	}

	// A project with no published settings has none: the key is left out rather
	// than written as an empty object, which a reader would take for a policy.
	empty := &localcfg.Config{Name: "acme-app", GCPProject: "acme-app", Budget: lc.Budget, Endpoints: lc.Endpoints}
	db2 := gcpfake.NewRTDB(t)
	db2.SetClock(func() time.Time { return fixed })
	client2, err := newBudgetClient(ctx, db2.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	empty.Budget = &localcfg.Budget{RTDBURL: db2.URL, FirebaseProject: "acme-app-firebase"}
	raw2, err := stateProjectContent{lc: empty, db: client2, fs: fsc}.Body(ctx, wu15Member())
	if err != nil {
		t.Fatalf("reading the body of a project with no published settings: %v", err)
	}
	var bare map[string]json.RawMessage
	if err := json.Unmarshal(raw2, &bare); err != nil {
		t.Fatal(err)
	}
	if _, ok := bare["settings"]; ok {
		t.Errorf("a project with no published policy wrote a settings key: %s", raw2)
	}

	// The workspace's export is the audit trail, oldest first, in C1's own shape.
	// The workspace's own project is the one this reader was built for: its
	// trail lives in its project's Firestore, so a machine saving it is
	// configured for that project.
	araw, err := c.Body(ctx, workspace.Snapshot{Workspace: lc.Name, Audit: true})
	if err != nil {
		t.Fatalf("reading the workspace's audit export: %v", err)
	}
	var export struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(araw, &export); err != nil {
		t.Fatalf("the audit export is not JSON: %v\n%s", err, araw)
	}
	if len(export.Events) != 2 {
		t.Fatalf("the export holds %d events, want the two the trail does:\n%s", len(export.Events), araw)
	}
	var first workspace.AuditEvent
	if err := json.Unmarshal(export.Events[0], &first); err != nil {
		t.Fatalf("the export's first entry is not an event: %v", err)
	}
	if first.ID != older {
		t.Errorf("the export leads with %q, want the older event %q: an entry's id opens with its instant, so the export is in time order", first.ID, older)
	}
	if first.Actor != "owner@example.com" || first.Action != workspace.ActionProjectAdd {
		t.Errorf("the export restates an event's fields rather than carrying them: %+v", first)
	}

	// A reader built for one project refuses another's snapshot rather than
	// answering with its own figures.
	other := workspace.Snapshot{Workspace: "acme-workspace", Member: workspace.MemberTarget{Project: "someone-else"}}
	if _, err := c.Body(ctx, other); err == nil || ExitCode(err) != ExitUserError {
		t.Errorf("a reader built for acme-app answered %v for someone-else's snapshot, want a refusal", err)
	}
}

// TestStateSaveBitbucketHostReadsVisibility: the fence is the repository's own
// is_private on the host that has no CLI, read through internal/gitprov — and an
// answer that does not say is a refusal, never a public repository taken for
// private (D2824).
func TestStateSaveBitbucketHostReadsVisibility(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want stateVisibility
	}{
		{"a private repository", `{"is_private":true}`, stateVisibilityPrivate},
		{"a public one", `{"is_private":false}`, stateVisibilityPublic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			h := stateBitbucketHost{token: "a-token-this-test-invented", baseURL: srv.URL + "/2.0"}
			got, err := h.Visibility(context.Background(), "acme/state")
			if err != nil {
				t.Fatalf("reading the visibility of a %s repository: %v", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("Visibility answered %q, want %q", got, tc.want)
			}
			if want := "/2.0/repositories/acme/state"; path != want {
				t.Errorf("the read went to %s, want %s: the repository the save is about", path, want)
			}
		})
	}

	// An answer with no is_private says nothing about who may read the
	// repository, and a save that guessed "private" would push addresses into
	// the open (D2824).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	h := stateBitbucketHost{token: "a-token-this-test-invented", baseURL: srv.URL + "/2.0"}
	if v, err := h.Visibility(context.Background(), "acme/state"); err == nil {
		t.Errorf("Visibility answered %q for a repository whose answer does not say, want a refusal", v)
	}
}

// TestStateSaveBitbucketHostPushesWithTheTokenInTheEnvironment is the Bitbucket
// publish path: Bitbucket has no CLI (D2807), so the save clones, commits and
// pushes with the owner's git and opens the pull request through
// internal/gitprov — and the token goes to git in the ENVIRONMENT
// (gitops.CredentialVars), never in the remote URL, the argv or .git/config,
// where it would land in a terminal's scrollback.
//
// This is the only place a save hands a credential to another program, which is
// why it is asserted rather than left to the tested visibility read: a save that
// spelled the token into the clone URL would pass every other test here.
func TestStateSaveBitbucketHostPushesWithTheTokenInTheEnvironment(t *testing.T) {
	const token = "a-token-this-test-invented"
	gitCalls := wu15FakeGit(t)

	var (
		mu    sync.Mutex
		seen  []string
		asked map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			asked = body
		}
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pullrequests"):
			io.WriteString(w, `{"values":[]}`)
		case r.Method == http.MethodPost:
			io.WriteString(w, `{"id":7,"links":{"html":{"href":"https://example.invalid/acme/state/pull/7"}}}`)
		default:
			io.WriteString(w, `{"is_private":true}`)
		}
	}))
	defer srv.Close()

	s := wu15New(t, "")
	s.des.d = stateDesignation{Repo: "acme/state", Dir: "docs/state", Provider: gitprov.KindBitbucket, Source: "the project layer"}
	s.w.hosts = func(kind string) (stateHost, error) {
		if kind != gitprov.KindBitbucket {
			t.Errorf("the save opened a %q host for a Bitbucket project", kind)
		}
		return stateBitbucketHost{token: token, baseURL: srv.URL + "/2.0"}, nil
	}

	got, err := s.w.Save(context.Background(), wu15Member())
	if err != nil {
		t.Fatalf("a save through the Bitbucket host failed: %v", err)
	}
	if got.PullRequest != "https://example.invalid/acme/state/pull/7" {
		t.Errorf("the save answered PullRequest %q, want the URL the host returned", got.PullRequest)
	}

	env := wu15GitEnv(t, gitCalls)
	for want, value := range map[string]string{
		"FUGARO_GIT_TOKEN":    token,
		"FUGARO_GIT_USERNAME": "x-token-auth",
		"GIT_CONFIG_KEY_0":    "credential.https://bitbucket.org.helper",
		"GIT_CONFIG_KEY_1":    "credential.https://bitbucket.org.helper",
		"GIT_TERMINAL_PROMPT": "0",
	} {
		if got := env[want]; got != value {
			t.Errorf("the push ran with %s=%q, want %q: the token reaches git through the credential helper, and git never asks a person for one", want, got, value)
		}
	}

	ran := wu15Calls(t, gitCalls)
	for _, want := range []string{
		"clone --quiet -- https://bitbucket.org/acme/state.git",
		"push --quiet origin fugaro/state-20261121-093000-acme-workspace-acme-app-01020304",
	} {
		if !strings.Contains(ran, want) {
			t.Errorf("the save never ran %q. Everything it ran:\n%s", want, ran)
		}
	}
	// Every one of those spawns carries the hardening, which matters most on
	// this path: a hook the repository or the person configured would otherwise
	// run with the project's token in its environment, right here.
	for _, line := range wu15GitLines(t, gitCalls) {
		if !strings.HasPrefix(line, strings.Join(gitHardening, " ")) {
			t.Errorf("the save ran `git %s` without the hardening, so a hook of the repository's or the person's would run with the token in its environment", line)
		}
	}
	if strings.Contains(ran, token) {
		t.Errorf("the token is in what the save ran, where it lands in a shell history, a process listing and .git/config:\n%s", ran)
	}

	mu.Lock()
	paths := strings.Join(seen, "\n")
	post := asked
	mu.Unlock()
	for _, want := range []string{"GET /2.0/repositories/acme/state", "POST /2.0/repositories/acme/state/pullrequests"} {
		if !strings.Contains(paths, want) {
			t.Errorf("the save never asked Bitbucket %q. Everything it asked:\n%s", want, paths)
		}
	}
	if post == nil {
		t.Fatal("the save opened no pull request")
	}
	source, _ := post["source"].(map[string]any)
	dest, _ := post["destination"].(map[string]any)
	if branchOf(source) != "fugaro/state-20261121-093000-acme-workspace-acme-app-01020304" || branchOf(dest) != "main" {
		t.Errorf("the pull request asked to merge %v into %v, want the saved branch into the branch the clone was on", post["source"], post["destination"])
	}
}

// wu15GitEnvFile is where the fake git dumps the environment it was run with,
// beside the calls it records, which is the same directory testutil.FakeCommand
// writes both to. Each invocation overwrites it, so a test that reads it after a
// save sees the environment of that save's last git call.
const wu15GitEnvFile = "env"

// wu15GitEnv is the environment the last git a save spawned was handed, by name:
// a save that hands git no environment at all is the process's own, which is
// what the fake is run with and dumps.
func wu15GitEnv(t *testing.T, calls string) map[string]string {
	t.Helper()
	return wu15EnvNamed(t, calls, wu15GitEnvFile)
}

// wu15GitLines is every git invocation one save made, one argv per line — what
// the fake recorded, which is one line per spawn because a save's git argv holds
// no newline.
func wu15GitLines(t *testing.T, calls string) []string {
	t.Helper()
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the fake git recorded no calls: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// wu15GHEnvPrefix is the stem of the file the fake gh dumps its environment
// into, with the subcommand's first word appended: "ghenv-repo" for the
// visibility read, "ghenv-pr" for the pull request. Both are read because both
// are handed an environment of their own, and a save that got it right for one
// call and not the other would otherwise pass.
const wu15GHEnvPrefix = "ghenv-"

// wu15GHEnv is the environment the gh of one subcommand family was handed, by
// name. sub is that word: "repo" or "pr".
func wu15GHEnv(t *testing.T, calls, sub string) map[string]string {
	t.Helper()
	return wu15EnvNamed(t, calls, wu15GHEnvPrefix+sub)
}

// wu15EnvNamed reads the environment a fake dumped into name, beside the calls
// it recorded.
func wu15EnvNamed(t *testing.T, calls, name string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(calls), name))
	if err != nil {
		t.Fatalf("the fake recorded no environment in %s, so the command was handed none of its own: %v", name, err)
	}
	env := map[string]string{}
	for _, kv := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return env
}

// branchOf reads the branch name out of a Bitbucket endpoint object.
func branchOf(e map[string]any) string {
	b, _ := e["branch"].(map[string]any)
	name, _ := b["name"].(string)
	return name
}

// wu15FakeGit puts a `git` on PATH that answers the subcommands a save runs —
// the clone makes the directory git would make, and the clone's HEAD is a
// branch — records the environment it was run with, and answers the path of the
// file every argv is appended to.
func wu15FakeGit(t *testing.T) string {
	t.Helper()
	return testutil.FakeCommand(t, stateGit, `
env > "${0%/*}/`+wu15GitEnvFile+`"
# every git a save spawns opens with internal/cli's hardening (-c k=v pairs),
# so the subcommand is behind them
while [ "$1" = "-c" ]; do shift 2; done
case "$1" in
clone) mkdir -p "$5" ;;
rev-parse) echo main ;;
esac
exit 0
`)
}

// wu15FakeGH puts a `gh` on PATH that answers the two calls a save makes: the
// repository's visibility as JSON (`gh repo view --json` is real), and the pull
// request as the plain URL `gh pr create` prints. It records the environment it
// was run with, one file per subcommand, which is what tells apart the
// environment one call was handed from the other's (wu15GHEnv).
//
// It is gh's behaviour over the flag it does NOT have, though, that a save has
// to be held to: `gh pr create` has no --json, and the real gh refuses the whole
// invocation over it ("unknown flag: --json", exit 1) after the branch has
// already been pushed. A fake that answered whatever it was asked would pass a
// save that fails against the real gh, so this one refuses --json the same way —
// which is a fake doing what the tool does, not a test asserting on argv alone.
//
// visibility is the word `gh repo view --json visibility` answers with, because
// the mapping from that word to the fence is the save's own code and not gh's:
// a test that cannot choose the word cannot tell the mapping apart
// (TestStateSaveGitHubVisibilityIsTheShippedFence).
func wu15FakeGH(t *testing.T, visibility string) string {
	t.Helper()
	return testutil.FakeCommand(t, stateGH, `
env > "${0%/*}/`+wu15GHEnvPrefix+`$1"
case "$1 $2" in
"repo view") echo '{"visibility":"`+visibility+`"}' ;;
"pr create")
  for a in "$@"; do
    if [ "$a" = "--json" ]; then
      echo "unknown flag: --json" >&2
      exit 1
    fi
  done
  echo https://example.invalid/acme/state/pull/7
  ;;
*) echo '{}' ;;
esac
exit 0
`)
}

// wu15Calls is everything the fakes of one test ran, one line per argv.
func wu15Calls(t *testing.T, calls ...string) string {
	t.Helper()
	var b strings.Builder
	for _, path := range calls {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("reading the calls a fake recorded: %v", err)
		}
		b.Write(raw)
	}
	return b.String()
}
