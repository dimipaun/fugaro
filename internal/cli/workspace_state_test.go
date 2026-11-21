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

	want := filepath.Join(s.dir, "acme-workspace", "acme-app.json")
	if got.File != want {
		t.Errorf("Save answered File %q, want %q: the local file is the one thing every save leaves", got.File, want)
	}
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
	if p.File != "acme-app.json" || p.Path() != "docs/state/acme-app.json" {
		t.Errorf("the snapshot's file is %q at %q, want acme-app.json at docs/state/acme-app.json", p.File, p.Path())
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
	want := filepath.Join(state, "fugaro", "state", "acme-workspace", "acme-app.json")
	if got.File != want {
		t.Errorf("the save wrote %q, want %q: the state directory, so the file is where a person expects it", got.File, want)
	}
	if _, found, err := s.w.LastSaved(ctx, wu15Member()); err != nil || !found {
		t.Errorf("LastSaved answered found=%v err=%v for the file the same writer just wrote", found, err)
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
	ghCalls := wu15FakeGH(t)

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
		"repo view acme/state --json visibility",
		"clone --quiet -- https://github.com/acme/state.git",
		"checkout --quiet -b fugaro/state-20261121-093000",
		"add -- docs/state/acme-app.json",
		"push --quiet origin fugaro/state-20261121-093000",
		"pr create --repo acme/state --head fugaro/state-20261121-093000 --base main",
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
	// The URL is read off gh's output, which is the one line `gh pr create`
	// prints on success.
	if url := statePRURL("A new release of gh is available\nhttps://example.invalid/acme/state/pull/7\n"); url != "https://example.invalid/acme/state/pull/7" {
		t.Errorf("statePRURL read %q off gh's output, want the URL: a notice before it must not be taken for the pull request", url)
	}
	if url := statePRURL("opened a pull request\n"); url != "" {
		t.Errorf("statePRURL read %q off an answer that holds no URL, want nothing so the save refuses", url)
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
	gitCalls := wu15FakeGitPushing(t)
	envFile := filepath.Join(filepath.Dir(gitCalls), wu15GitEnvFile)

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

	raw, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("the fake git recorded no environment, so the push was handed none (%v)", err)
	}
	env := string(raw)
	for _, want := range []string{
		"FUGARO_GIT_TOKEN=" + token,
		"FUGARO_GIT_USERNAME=x-token-auth",
		"GIT_CONFIG_KEY_0=credential.https://bitbucket.org.helper",
		"GIT_CONFIG_KEY_1=credential.https://bitbucket.org.helper",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("the push ran without %s in its environment, so the token does not reach git through the credential helper", want)
		}
	}

	ran := wu15Calls(t, gitCalls)
	for _, want := range []string{
		"clone --quiet -- https://bitbucket.org/acme/state.git",
		"push --quiet origin fugaro/state-20261121-093000",
	} {
		if !strings.Contains(ran, want) {
			t.Errorf("the save never ran %q. Everything it ran:\n%s", want, ran)
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
	if branchOf(source) != "fugaro/state-20261121-093000" || branchOf(dest) != "main" {
		t.Errorf("the pull request asked to merge %v into %v, want the saved branch into the branch the clone was on", post["source"], post["destination"])
	}
}

// wu15GitEnvFile is where the fake git dumps its environment — the push's, the
// one call whose environment carries the credential — beside the calls it
// records, which is the same directory testutil.FakeCommand writes both to.
const wu15GitEnvFile = "env"

// wu15FakeGitPushing is wu15FakeGit with one addition: the push's environment is
// written out, so a test can see what the token travelled in.
func wu15FakeGitPushing(t *testing.T) string {
	t.Helper()
	return testutil.FakeCommand(t, stateGit, `
case "$1" in
clone) mkdir -p "$5" ;;
rev-parse) echo main ;;
push) env > "${0%/*}/`+wu15GitEnvFile+`" ;;
esac
exit 0
`)
}

// branchOf reads the branch name out of a Bitbucket endpoint object.
func branchOf(e map[string]any) string {
	b, _ := e["branch"].(map[string]any)
	name, _ := b["name"].(string)
	return name
}

// wu15FakeGit puts a `git` on PATH that answers the subcommands a save runs —
// the clone makes the directory git would make, and the clone's HEAD is a
// branch — and answers the path of the file every argv is appended to.
func wu15FakeGit(t *testing.T) string {
	t.Helper()
	return testutil.FakeCommand(t, stateGit, `
case "$1" in
clone) mkdir -p "$5" ;;
rev-parse) echo main ;;
esac
exit 0
`)
}

// wu15FakeGH puts a `gh` on PATH that answers the two calls a save makes: the
// repository's visibility as JSON (`gh repo view --json` is real), and the pull
// request as the plain URL `gh pr create` prints.
//
// It is gh's behaviour over the flag it does NOT have, though, that a save has
// to be held to: `gh pr create` has no --json, and the real gh refuses the whole
// invocation over it ("unknown flag: --json", exit 1) after the branch has
// already been pushed. A fake that answered whatever it was asked would pass a
// save that fails against the real gh, so this one refuses --json the same way —
// which is a fake doing what the tool does, not a test asserting on argv alone.
func wu15FakeGH(t *testing.T) string {
	t.Helper()
	return testutil.FakeCommand(t, stateGH, `
case "$1 $2" in
"repo view") echo '{"visibility":"PRIVATE"}' ;;
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
