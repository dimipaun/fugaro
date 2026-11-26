package cli

// workspace_state.go is WU15 of docs/workspace-projects-users: the state writer
// `workspace project save` and `workspace project rm` save through (D2807,
// D2824). It is the concrete side I1 adapts onto C2's workspace.StateWriter
// (contract-member.md §5).
//
// Three properties are this file's own, and none of them can be re-derived from
// the method list:
//
//   - A save ALWAYS leaves a local file it names. The designated repository is
//     an addition (D2807): a project that designates none, or a save that
//     cannot push, still leaves the snapshot on the disk of the machine that
//     ran it, and SnapshotWritten.File is where.
//   - The public fence comes BEFORE anything is written (D2824). A snapshot
//     carries the addresses of the people the project reaches, so a save into a
//     designated PUBLIC repository is refused outright -- nothing is read from
//     the cloud, no local file is left, and nothing is pushed -- and the
//     refusal names the --repo override, because a person who really means it
//     points the save at a repository of their own rather than being asked
//     (the repository asks for nothing, docs/no-prompt-policy).
//   - It is the owner's own act. The push uses the owner's own credentials --
//     `gh`'s login on GitHub, the project's Bitbucket token -- the pull request
//     opens through the repository's own host, and the whole command refuses in
//     a coding agent's session (A7), as every write of this family does.
//
// What the snapshot HOLDS is not this file's decision: it is the body the
// content reader answers, and the envelope around it carries the instant, the
// workspace and the member (or the audit flag) so that LastSaved can read a
// snapshot's time back without knowing what is inside it.

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/procgroup"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// stateSnapshotVersion is the version of the local file and of the body a save
// writes. It is 1 and it is written in every file, as every other document this
// repository writes carries one: a field never changes meaning, so a shape that
// has to move takes version 2 and a reader of the older version refuses the
// newer file rather than reading the half of it it recognizes.
const stateSnapshotVersion = 1

// stateAuditFileName is the file the workspace's own audit export is written
// as, directly inside its workspace's folder, and stateMemberDir is the folder
// the members' snapshots are written in under it. A member's file is named after
// that member, so the two kinds are told apart by the FOLDER and not by the
// name: a project may legally be called "audit" (config.ProjectNameRE), and a
// flat folder therefore let a member's settings and spend and the workspace's
// own export be one file — the same local path, the same path inside the
// designated repository, one overwriting the other, and D2824's "state repo in
// sync" row answering the other document's instant.
const (
	// stateAuditSegment names the workspace's own export wherever one word is
	// needed for it: the file's name is that word and ".json", and a save's
	// branch carries it where a member's carries the member's name.
	stateAuditSegment  = "audit"
	stateAuditFileName = stateAuditSegment + ".json"
	stateMemberDir     = "members"
)

// stateGit is the `git` a save clones, commits and pushes with, and stateGH is
// the `gh` it opens a GitHub pull request and reads a GitHub repository's
// visibility with. Both are variables for the reason every other spawn in this
// tree is: a test puts a fake in front of them on PATH and reads the argv back.
// Each is resolved through LookPath rather than named bare, so a missing one is
// a sentence and not a fork that fails.
var (
	stateGit = "git"
	stateGH  = "gh"
)

// stateGHRace is how long a gh or git this save spawned is given to leave once
// its context is done. A clone or a push is slow, so the grace is generous: the
// point is that a child which ignores the cancellation is still reaped, not that
// it is rushed.
const stateGHRace = 10 * time.Second

// stateWhat names one snapshot in the operator's words. It is what every line
// and every refusal of a save says the save is about.
func stateWhat(s workspace.Snapshot) string {
	if s.Audit {
		return "the audit export of the workspace " + pluginwire.Printable(s.Workspace)
	}
	return "the settings and spend of " + pluginwire.Printable(s.Member.Project)
}

// stateSnapshotName is one snapshot's name inside its workspace's folder:
// audit.json for the workspace's own export, members/<member>.json for a
// member's settings and spend. It is the name the file is written under locally
// and the path it is committed at inside the designated repository, so the two
// kinds cannot be one document in either place (stateAuditFileName,
// stateMemberDir).
//
// It is derived from the Snapshot and from nothing else, so LastSaved reads back
// the file the last Save wrote without being told where it went.
func stateSnapshotName(s workspace.Snapshot) (string, error) {
	if s.Audit {
		return stateAuditFileName, nil
	}
	member, err := stateSnapshotSegment(s)
	if err != nil {
		return "", err
	}
	return path.Join(stateMemberDir, member+".json"), nil
}

// stateSnapshotSegment is the one word that names the document a snapshot holds
// — the member project's name, or audit for the workspace's own export — held to
// being one path segment (statePathSegment). The file's name and the branch's
// are both built from it, so the two cannot disagree about which document a save
// is about.
func stateSnapshotSegment(s workspace.Snapshot) (string, error) {
	if s.Audit {
		return stateAuditSegment, nil
	}
	return statePathSegment("the member project", s.Member.Project)
}

// stateSnapshotPath is where one snapshot's local file lives and the name it is
// committed under inside the designated repository: the file is
// <dir>/<workspace>/<name>, and name is what statePublish carries.
func stateSnapshotPath(dir string, s workspace.Snapshot) (path, name string, err error) {
	workspaceSeg, err := statePathSegment("the workspace", s.Workspace)
	if err != nil {
		return "", "", err
	}
	name, err = stateSnapshotName(s)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, workspaceSeg, filepath.FromSlash(name)), name, nil
}

// stateDefaultFolder is where a save's local files live when the caller names no
// folder: <state dir>/fugaro/state/<workspace>/<member>.json — the state
// directory the mailbox's ledger, the issue store and the watch acks are kept
// in, which is the one directory rule the CLI has (F5), so a snapshot is found
// where a person already looks for what the tool keeps on their machine.
//
// It is one folder per machine and not per invocation, which is what lets
// LastSaved answer for a project another invocation saved, and what `status`'s
// "state repo in sync" row reads on a machine whose command named no folder.
func stateDefaultFolder(getenv func(string) string) (string, error) {
	d, err := localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirState)
	if err != nil {
		return "", userErr("this machine has no state directory to keep state snapshots in: %v", err)
	}
	return filepath.Join(d, "fugaro", "state"), nil
}

// statePathSegment holds a name that becomes one path segment to being exactly
// that: non-empty, one segment, and no separator or "." / ".." in it. A
// workspace or a project name reaching outside the folder a save was given
// would be a file written somewhere the operator did not name.
func statePathSegment(what, name string) (string, error) {
	if name == "" || !filepath.IsLocal(name) || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return "", userErr("the %s name %q is not one path segment, so a save cannot name its local file with it: it is written under a folder of the workspace's and the member's own name", what, pluginwire.Printable(name))
	}
	return name, nil
}

// stateSnapshotDoc is the local file's own shape, and the envelope a save
// writes around whatever the content reader answered.
type stateSnapshotDoc struct {
	Version   int             `json:"version"`
	Taken     time.Time       `json:"taken"`
	Workspace string          `json:"workspace"`
	Member    string          `json:"member,omitempty"`
	Audit     bool            `json:"audit,omitempty"`
	State     json.RawMessage `json:"state"`
}

// stateEncodeSnapshot renders one envelope. It refuses a body that is not JSON
// rather than writing a file its own reader would refuse.
func stateEncodeSnapshot(s workspace.Snapshot, taken time.Time, body json.RawMessage) ([]byte, error) {
	if len(body) == 0 || !json.Valid(body) {
		return nil, fmt.Errorf("the snapshot of %s is not a JSON document, so it is not written", stateWhat(s))
	}
	doc := stateSnapshotDoc{
		Version:   stateSnapshotVersion,
		Taken:     taken,
		Workspace: s.Workspace,
		Member:    s.Member.Project,
		Audit:     s.Audit,
		State:     body,
	}
	if s.Audit {
		doc.Member = ""
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rendering the snapshot of %s: %w", stateWhat(s), err)
	}
	return append(out, '\n'), nil
}

// stateDecodeSnapshot reads one envelope back. Unknown keys are an error, as
// every other format in this tree decodes: the file is one a person may edit
// and one a newer Fugaro may write, and half of a document read as a whole one
// is how a status row comes to state a time nothing was saved at.
func stateDecodeSnapshot(raw []byte) (stateSnapshotDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc stateSnapshotDoc
	if err := dec.Decode(&doc); err != nil {
		return stateSnapshotDoc{}, fmt.Errorf("the snapshot file is not one this build reads: %w", err)
	}
	if doc.Version != stateSnapshotVersion {
		return stateSnapshotDoc{}, userErr("the snapshot file is version %d, and this build writes and reads version %d: a newer Fugaro wrote it, and half of a document is not a snapshot -- run fugaro update", doc.Version, stateSnapshotVersion)
	}
	if doc.Taken.IsZero() {
		return stateSnapshotDoc{}, fmt.Errorf("the snapshot file states no time it was taken at, so it is not a snapshot")
	}
	return doc, nil
}

// stateVisibility is what a save read about the repository it is about to push
// into (D2824). The unknown value is a read that did not happen, which is what
// a save with no designated repository has.
type stateVisibility string

const (
	stateVisibilityUnknown stateVisibility = ""
	stateVisibilityPrivate stateVisibility = "private"
	stateVisibilityPublic  stateVisibility = "public"
)

// stateVisibilityWord says what the fence read, in the operator's words. The
// refusal has to be true of every answer it refuses, and the answer a host that
// reads nothing gives is not the word "public": a refusal that said so would be
// telling the operator their repository is public when nobody said it was.
func stateVisibilityWord(v stateVisibility) string {
	switch v {
	case stateVisibilityPublic:
		return "the repository is public"
	case stateVisibilityUnknown:
		return "the repository's visibility was not answered as private"
	}
	return fmt.Sprintf("the repository's visibility was answered %q", pluginwire.Printable(string(v)))
}

// statePublish is one push and one pull request: clone the repository, commit
// the snapshot's file at Dir/File on a new Branch, push it, and open the pull
// request through the host's own tool (D2807).
type statePublish struct {
	Repo    string // owner/name, as the designation spells it
	Dir     string // the folder inside the repository
	File    string // the file's name inside that folder
	Branch  string // the branch this save pushes
	Bytes   []byte // the snapshot document
	Subject string // the commit subject and the pull request's title
	Body    string // the pull request's description
}

// Path is where inside a clone the snapshot's file goes.
func (p statePublish) Path() string { return path.Join(p.Dir, p.File) }

// stateHost is the host the designated repository is on: whether it is public
// (D2824's fence) and how a snapshot is published there (D2807). It is one
// interface because both halves are the same repository's and the same
// credentials': a second seam would let a save read one repository and push
// into another.
//
// WU15 provides the GitHub host (gh) and the Bitbucket host (internal/gitprov,
// because Bitbucket has no CLI); a test injects its own.
type stateHost interface {
	// Visibility reads whether repo is public. It refuses rather than guessing:
	// a public repository taken for private is the leak D2824 is about.
	Visibility(ctx context.Context, repo string) (stateVisibility, error)

	// OpenPR answers the open pull request branch already has, and "" when it
	// has none. It is a read of its own and not a side effect of Publish
	// because the caller that needs it must know BEFORE anything is pushed:
	// a branch whose pull request is still open is not pushed again, and the
	// person is pointed at the pull request they have (enrolOpenStarter).
	OpenPR(ctx context.Context, repo, branch string) (string, error)

	// Publish clones, commits, pushes and opens the pull request, and answers
	// its URL.
	Publish(ctx context.Context, p statePublish) (string, error)
}

// stateHostFor opens the host a provider kind names. It is a function and not a
// switch inside the writer so that a test drives a save's GitHub path and its
// Bitbucket path without a forge of either.
type stateHostFor func(kind string) (stateHost, error)

// stateDesignation is where a save's snapshot goes: the E6 designated repository
// (D2750), its folder inside that repository, the host's provider kind, and one
// line saying WHERE that setting came from -- which the save prints before it
// pushes, because a person is entitled to see which layer designated the
// repository their snapshot is about to land in (D2824).
type stateDesignation struct {
	Repo     string
	Dir      string
	Provider string
	Source   string
}

// stateDesignationReader answers E6's setting for one snapshot's project. The
// concrete reader (project_cmd.go's own designation) reads it from the layer
// the owner stated it in; WU15 only needs the answer and its provenance.
type stateDesignationReader interface {
	Designate(ctx context.Context, s workspace.Snapshot) (stateDesignation, error)
}

// stateContentReader produces the body of one snapshot: a member's settings and
// spend summary, or the workspace's own audit export (D2807). It is a seam
// because the documents are read from the project's own stores, which a test
// must not reach: the shape of what the body holds is not this file's to decide.
type stateContentReader interface {
	Body(ctx context.Context, s workspace.Snapshot) (json.RawMessage, error)
}

// stateWriter is WU15's concrete writer. I1 adapts it onto C2's
// workspace.StateWriter; `workspace project save` (WU23) and
// `workspace project rm` (WU6) build one and call Save.
type stateWriter struct {
	dir    string
	out    io.Writer
	getenv func(string) string
	now    func() time.Time
	rand   io.Reader

	designate stateDesignationReader
	content   stateContentReader
	hosts     stateHostFor

	// repoOverride is the command's --repo: the repository this save goes to,
	// whatever the project's setting says. It is what D2824's refusal names, and
	// the only way to save into a public repository is to name one that is not
	// public.
	repoOverride string
}

// stateWriterOptions are what a caller builds a writer out of. Designation,
// Content and Hosts are the three seams and have no default: a writer without
// them cannot save anything, and a default that silently reached a live cloud
// from a test is exactly what the hermetic-tests rule forbids.
type stateWriterOptions struct {
	Dir         string
	Out         io.Writer
	Getenv      func(string) string
	Now         func() time.Time
	Rand        io.Reader
	Designation stateDesignationReader
	Content     stateContentReader
	Hosts       stateHostFor
	Repo        string
}

// newStateWriter builds the writer. Every seam is required.
func newStateWriter(o stateWriterOptions) *stateWriter {
	w := &stateWriter{
		dir:          o.Dir,
		out:          o.Out,
		getenv:       o.Getenv,
		now:          o.Now,
		rand:         o.Rand,
		designate:    o.Designation,
		content:      o.Content,
		hosts:        o.Hosts,
		repoOverride: o.Repo,
	}
	if w.out == nil {
		w.out = io.Discard
	}
	if w.getenv == nil {
		w.getenv = os.Getenv
	}
	if w.now == nil {
		w.now = time.Now
	}
	if w.rand == nil {
		w.rand = crand.Reader
	}
	return w
}

// folder is where this writer's local files go: the folder the caller named, or
// stateDefaultFolder on a machine that named none. Both Save and LastSaved ask
// it, so the file a save writes and the file a later read looks for are the same
// one by construction and not by the two agreeing.
func (w *stateWriter) folder() (string, error) {
	if w.dir != "" {
		return w.dir, nil
	}
	return stateDefaultFolder(w.getenv)
}

// Save is C2's workspace.StateWriter.Save: one snapshot to a local file it
// names, and to the E6 designated repository as a pull request when one is set
// (D2807, D2824).
func (w *stateWriter) Save(ctx context.Context, s workspace.Snapshot) (workspace.SnapshotWritten, error) {
	if err := AgentSessionRefusal(w.getenv, "nothing was saved: a state snapshot goes to a repository with your own credentials, so it is your act and not an agent's"); err != nil {
		return workspace.SnapshotWritten{}, err
	}

	d, err := w.designation(ctx, s)
	if err != nil {
		return workspace.SnapshotWritten{}, err
	}

	// The public fence comes before anything is read or written: a save that may
	// not be pushed is not half a save, and a local file left behind by a refused
	// one would be a snapshot the operator did not ask for. D2824's --repo is
	// named, because the fix is a repository of the owner's own and not a
	// question.
	var host stateHost
	if d.Repo != "" {
		if host, err = w.hosts(d.Provider); err != nil {
			return workspace.SnapshotWritten{}, err
		}
		v, err := host.Visibility(ctx, d.Repo)
		if err != nil {
			return workspace.SnapshotWritten{}, err
		}
		// Fail-closed: only a repository this save READ as private is saved
		// into. The check is "not private" and not "public", because a host that
		// answers anything else — stateVisibilityUnknown with no error — would
		// otherwise be the one answer that turns the fence off, and what it turns
		// off is the push of a snapshot carrying the addresses of the people the
		// project reaches (D2824, D2753). A repository taken for private when it
		// is not is the leak this fence is about.
		if v != stateVisibilityPrivate {
			return workspace.SnapshotWritten{}, userErr("saving %s to %s is refused: %s, and a snapshot carries the addresses of the people the project reaches (D2824, D2753). Only a repository this save read as private is saved into. Designate a repository that is not public, or name one with --repo <owner/name>, which is the override this refusal leaves you", stateWhat(s), pluginwire.Printable(d.Repo), stateVisibilityWord(v))
		}
	}

	body, err := w.content.Body(ctx, s)
	if err != nil {
		return workspace.SnapshotWritten{}, err
	}
	folder, err := w.folder()
	if err != nil {
		return workspace.SnapshotWritten{}, err
	}
	path, name, err := stateSnapshotPath(folder, s)
	if err != nil {
		return workspace.SnapshotWritten{}, err
	}
	taken := w.now().UTC().Truncate(time.Second)
	// The branch is named here, before anything is written, so a name this save
	// cannot build — a workspace or a member that is not one path segment — is
	// still a refusal that leaves nothing behind.
	branch, err := stateBranch(s, taken, w.rand)
	if err != nil {
		return workspace.SnapshotWritten{}, err
	}
	doc, err := stateEncodeSnapshot(s, taken, body)
	if err != nil {
		return workspace.SnapshotWritten{}, err
	}
	// The snapshot is the operator's own alone: it carries the addresses the
	// project reaches (D2824), so another account on this machine must not be
	// able to read it — 0o700 on the folders and 0o600 on the file, which is
	// what the rest of this state directory is kept at (the mailbox's ledger,
	// the issue store).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return workspace.SnapshotWritten{}, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		return workspace.SnapshotWritten{}, fmt.Errorf("writing %s: %w", path, err)
	}
	written := workspace.SnapshotWritten{File: path}
	fmt.Fprintf(w.out, "wrote %s\n", pluginwire.Printable(path))

	if d.Repo == "" {
		fmt.Fprintf(w.out, "no state repository is designated for %s, so %s is the whole save (designate one with reports_commit in the owner's local project config, D2750)\n",
			stateWhat(s), pluginwire.Printable(path))
		return written, nil
	}

	// The target and where its setting came from, before anything is pushed
	// (D2824): a snapshot leaves this machine under the operator's own
	// credentials, and which layer aimed it is the one thing none of them can
	// read back afterwards.
	fmt.Fprintf(w.out, "saving %s to %s on %s, folder %s, the repository %s\n",
		stateWhat(s), pluginwire.Printable(d.Repo), stateHostName(d.Provider), pluginwire.Printable(d.Dir), d.Source)

	url, err := host.Publish(ctx, statePublish{
		Repo:    d.Repo,
		Dir:     d.Dir,
		File:    name,
		Branch:  branch,
		Bytes:   doc,
		Subject: stateCommitSubject(s, taken),
		Body:    statePRBody(s, taken),
	})
	if err != nil {
		return written, fmt.Errorf("%w; the snapshot is in %s, so the save is not lost", err, pluginwire.Printable(path))
	}
	written.PullRequest = url
	fmt.Fprintf(w.out, "opened %s\n", pluginwire.Printable(url))
	return written, nil
}

// LastSaved is C2's workspace.StateWriter.LastSaved: when the snapshot of s was
// last taken, and whether one ever was (D2824's "state repo in sync" row reads
// it). It reads the local file every Save writes -- the one place a save always
// leaves something -- and it is the time written in it, not the file's own
// mtime, which a copy or a checkout would move.
//
// found is false for a project that was never saved, which is not a save at the
// zero time: `status` shows the two differently.
func (w *stateWriter) LastSaved(ctx context.Context, s workspace.Snapshot) (time.Time, bool, error) {
	folder, err := w.folder()
	if err != nil {
		return time.Time{}, false, err
	}
	path, _, err := stateSnapshotPath(folder, s)
	if err != nil {
		return time.Time{}, false, err
	}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("reading %s: %w", path, err)
	}
	doc, err := stateDecodeSnapshot(raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%s: %w", pluginwire.Printable(path), err)
	}
	return doc.Taken, true, nil
}

// designation answers where the snapshot of s goes: the --repo override when the
// command named one, else the project's E6 setting. The override says so in its
// own source line, because "this invocation" and "the project's node" are two
// different answers to the question the printed line asks.
//
// The project's own designation is read in BOTH cases, because --repo names a
// repository and never a host: which host a repository is on is the project's
// own fact, and the E6 reader is the only place that holds it. A kind chosen
// here instead (this used to be gitprov.KindGitHub, unconditionally) sends a
// Bitbucket project's override to github.com/<owner>/<name> — a repository that
// is not the one named, on a host the operator never named.
func (w *stateWriter) designation(ctx context.Context, s workspace.Snapshot) (stateDesignation, error) {
	d, readErr := w.designate.Designate(ctx, s)
	if readErr != nil && w.repoOverride == "" {
		return stateDesignation{}, readErr
	}
	if w.repoOverride == "" {
		if d.Repo != "" && d.Dir == "" {
			d.Dir = stateDefaultRepoDir
		}
		return d, nil
	}
	// --repo is the way through exactly when the project's own setting is
	// unusable (D2824 names it as one), so a designation that cannot be read
	// does not sink a save that named its own repository — but the giveaway is
	// not hidden: the host the save falls back to is printed with the target,
	// and this line says the project's own answer is missing.
	repo, err := task.CanonicalRepo(w.repoOverride)
	if err != nil {
		return stateDesignation{}, userErr("--repo %s: %v", pluginwire.Printable(w.repoOverride), err)
	}
	d.Repo, d.Dir = repo, stateDefaultRepoDir
	d.Source = "named by --repo on this command line"
	if readErr != nil {
		d.Source += "; the project's own designation could not be read (" + oneLineCLI(readErr.Error()) + ")"
	}
	return d, nil
}

// stateDefaultRepoDir is the folder a save's own file goes in, inside the
// designated repository. It is deliberately not the reports' mirror folder
// (projectpolicy.DefaultReportsCommitDir): a snapshot is not a report, and a
// snapshot and a report reaching one folder would put two kinds of document in
// one index.
const stateDefaultRepoDir = "docs/fugaro-state"

// stateBranch is the branch one save pushes:
// fugaro/state-<UTC stamp>-<workspace>-<member|audit>-<nonce>. One branch per
// save, so a save never rewrites a branch somebody is reviewing, and the
// fugaro/ prefix keeps a person's own branches and a run's out of its way.
//
// The instant alone is not a name. `workspace project save` with no argument
// saves every member of the workspace in one command, so several saves carry the
// same second: with the instant for a name, the second member's push is rejected
// as a non-fast-forward — the branch is already there with another commit — and
// every member after it is left with its local file and no pull request at all.
// The workspace and the document are in the name so a person reading the branch
// list can tell whose save it is, and the nonce (stateBranchNonce) is what makes
// two saves of the same document at the same instant two branches.
func stateBranch(s workspace.Snapshot, taken time.Time, rand io.Reader) (string, error) {
	workspaceSeg, err := statePathSegment("the workspace", s.Workspace)
	if err != nil {
		return "", err
	}
	doc, err := stateSnapshotSegment(s)
	if err != nil {
		return "", err
	}
	nonce, err := stateBranchNonce(rand)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("fugaro/state-%s-%s-%s-%s", taken.UTC().Format("20060102-150405"), workspaceSeg, doc, nonce), nil
}

// stateBranchNonceBytes is the length of the random suffix a branch name
// carries: 4 bytes, 8 hex characters, which is short enough to read in a branch
// list and wide enough that two saves of one document in one second do not
// collide.
const stateBranchNonceBytes = 4

// stateBranchNonce draws that suffix from the writer's random source. A failure
// to read is a refusal and never a branch with no suffix: two saves that picked
// the same branch is exactly the collision the suffix exists to prevent.
func stateBranchNonce(rand io.Reader) (string, error) {
	b := make([]byte, stateBranchNonceBytes)
	if _, err := io.ReadFull(rand, b); err != nil {
		return "", fmt.Errorf("drawing the random suffix of this save's branch name: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// stateCommitSubject is the commit and pull request title of one save.
func stateCommitSubject(s workspace.Snapshot, taken time.Time) string {
	if s.Audit {
		return fmt.Sprintf("state: audit export of %s at %s", s.Workspace, taken.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("state: %s at %s", s.Member.Project, taken.UTC().Format(time.RFC3339))
}

// statePRBody is the pull request's description. It says what the document is
// and when it was taken, and it names the tool that wrote it, so a reader of
// the repository can tell a snapshot apart from a hand-written file.
func statePRBody(s workspace.Snapshot, taken time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The state snapshot of %s, taken %s by `fugaro workspace project save`.\n\n", stateWhat(s), taken.UTC().Format(time.RFC3339))
	if s.Audit {
		b.WriteString("It is the workspace's own audit export: the create-only entries of its audit trail, oldest first (D2811).\n")
	} else {
		b.WriteString("It is the project's published settings and its spend summary for the day it was taken.\n")
	}
	return b.String()
}

// stateGitHubHost is the GitHub half of a save's host work: `gh` for the
// visibility read and the pull request, and the owner's own `git` for the clone,
// commit and push (D2807).
//
// No token is read here at all. gh holds the person's own login, and it is gh's
// own credential helper that authenticates the cloned repository's push, which
// is the "owner's own credentials" D2807 asks for: a Fugaro that minted a token
// of its own would be a second identity pushing a person's state.
type stateGitHubHost struct{}

// stateGitHubRepo is the repository as a save names it to gh and to git: the
// host written out with the slug, in the [HOST/]OWNER/REPO form gh's --repo
// takes. A bare owner/name is resolved against gh's DEFAULT host, and GH_HOST
// is what sets that default.
//
// That is the whole hazard (F22; issues.GHEnv and internal/watch's own ask
// handle it the same way). An operator who is logged in to an enterprise GitHub
// as well, with GH_HOST exported, would have `gh repo view` read the visibility
// of THEIR enterprise's acme/state while the clone and the push go to
// github.com/acme/state. The fence would pass on another host's private
// repository and the snapshot — which carries the addresses the project reaches
// (D2824) — would land in a public one, with `gh pr create` failing only after
// the push. The host is stateHostName's github.com, the one map of provider and
// host this tree has, so the clone URL, the gh argument and the target line a
// save prints before pushing cannot drift apart.
func stateGitHubRepo(repo string) string {
	return stateHostName(gitprov.KindGitHub) + "/" + repo
}

// Visibility implements stateHost: `gh repo view github.com/<repo> --json
// visibility`, with GHEnv's environment, which drops GH_HOST and GH_REPO.
func (h stateGitHubHost) Visibility(ctx context.Context, repo string) (stateVisibility, error) {
	var answer struct {
		Visibility string `json:"visibility"`
	}
	if err := stateRunJSON(ctx, issues.GHEnv(os.Environ()), "", &answer, stateGH, "repo", "view", stateGitHubRepo(repo), "--json", "visibility"); err != nil {
		return stateVisibilityUnknown, err
	}
	switch strings.ToUpper(answer.Visibility) {
	case "PRIVATE":
		return stateVisibilityPrivate, nil
	case "INTERNAL":
		// An enterprise-internal repository is readable by the enterprise and
		// not by the world, so it is not the public repository D2824 refuses.
		return stateVisibilityPrivate, nil
	case "PUBLIC":
		return stateVisibilityPublic, nil
	}
	// Never guess: a visibility this build does not know read as private would
	// be the public repository D2824 exists to refuse.
	return stateVisibilityUnknown, fmt.Errorf("%s answered the visibility of %s as %q, which this build does not know: reading a repository as private when it is not is what D2824 refuses, so the save stops here",
		stateGH, pluginwire.Printable(repo), pluginwire.Printable(answer.Visibility))
}

// OpenPR implements stateHost with gh's own list. A branch with no pull request
// is an empty list and exit 0, which is an answer and not a failure.
func (h stateGitHubHost) OpenPR(ctx context.Context, repo, branch string) (string, error) {
	var found []struct {
		URL string `json:"url"`
	}
	if err := stateRunJSON(ctx, issues.GHEnv(os.Environ()), "", &found, stateGH, "pr", "list",
		"--repo", stateGitHubRepo(repo), "--head", branch, "--state", "open", "--json", "url"); err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", nil
	}
	return found[0].URL, nil
}

// Publish implements stateHost: clone, write, commit to a new branch, push, and
// open the pull request with gh.
//
// The URL is read off gh's standard output, which is where `gh pr create` puts
// it — the one line it prints on success — and there is no --json on that
// subcommand to ask for it another way: gh would refuse the whole invocation
// (`unknown flag: --json`), after the branch was already pushed, leaving a
// branch on the host and no pull request to tell anyone about it.
func (h stateGitHubHost) Publish(ctx context.Context, p statePublish) (string, error) {
	base, err := stateCloneAndCommit(ctx, nil, p, "https://"+stateGitHubRepo(p.Repo)+".git")
	if err != nil {
		return "", err
	}
	out, err := stateOutput(ctx, issues.GHEnv(os.Environ()), "", stateGH, "pr", "create",
		"--repo", stateGitHubRepo(p.Repo), "--head", p.Branch, "--base", base,
		"--title", p.Subject, "--body", p.Body)
	if err != nil {
		return "", err
	}
	url := statePRURL(out)
	if url == "" {
		return "", fmt.Errorf("%s opened the pull request on %s and printed no URL for it (%q), so there is nothing to point the operator at", stateGH, pluginwire.Printable(p.Repo), oneLineCLI(out))
	}
	return url, nil
}

// statePRURL is the pull request URL at the end of a gh invocation's output:
// the last non-empty line, and only when it is an https URL. gh prints the URL
// alone, but a notice it chose to print first (a new release, a warning) must
// not be taken for the pull request.
func statePRURL(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "https://") {
			return line
		}
		return ""
	}
	return ""
}

// stateBitbucketHost is the Bitbucket half of a save's host work, through
// internal/gitprov: Bitbucket has no CLI (D2807), so the pull request is opened
// by the adapter and the clone, commit and push are done by the owner's own git
// with the project's Bitbucket token.
type stateBitbucketHost struct {
	token   string
	baseURL string // the API root, empty for Bitbucket's own
}

// provider opens the adapter for one repository.
func (h stateBitbucketHost) provider(repo string) (*bitbucket.Provider, error) {
	owner, name, ok := gitprov.SplitRepo(repo)
	if !ok {
		return nil, userErr("%s is not owner/name, so there is no Bitbucket repository to open", pluginwire.Printable(repo))
	}
	return bitbucket.New(bitbucket.Options{Workspace: owner, Slug: name, Token: h.token, BaseURL: h.baseURL})
}

// Visibility implements stateHost: the repository's own is_private.
func (h stateBitbucketHost) Visibility(ctx context.Context, repo string) (stateVisibility, error) {
	p, err := h.provider(repo)
	if err != nil {
		return stateVisibilityUnknown, err
	}
	info, err := p.Repository(ctx)
	if err != nil {
		return stateVisibilityUnknown, err
	}
	if info.Private {
		return stateVisibilityPrivate, nil
	}
	return stateVisibilityPublic, nil
}

// OpenPR implements stateHost with the adapter's own find, which is also the
// first half of EnsurePR: Bitbucket has no CLI to ask, and the query language it
// is asked in is the adapter's.
func (h stateBitbucketHost) OpenPR(ctx context.Context, repo, branch string) (string, error) {
	p, err := h.provider(repo)
	if err != nil {
		return "", err
	}
	pr, found, err := p.FindPR(ctx, branch)
	if err != nil || !found {
		return "", err
	}
	return pr.URL, nil
}

// Publish implements stateHost: clone, write, commit to a new branch, push with
// the token, and open the pull request through the adapter.
func (h stateBitbucketHost) Publish(ctx context.Context, p statePublish) (string, error) {
	prov, err := h.provider(p.Repo)
	if err != nil {
		return "", err
	}
	// The token reaches git through the environment and never through the URL,
	// the argv or .git/config (gitops.CredentialVars): an error message that
	// echoed it would be a secret in a terminal's scrollback.
	vars, err := gitops.CredentialVars("https://bitbucket.org", bitbucket.GitUsername, h.token)
	if err != nil {
		return "", err
	}
	env := gitops.WithVars(os.Environ(), vars)
	base, err := stateCloneAndCommit(ctx, env, p, "https://bitbucket.org/"+p.Repo+".git")
	if err != nil {
		return "", err
	}
	pr, err := prov.EnsurePR(ctx, gitprov.PRSpec{Branch: p.Branch, Base: base, Title: p.Subject, Body: p.Body})
	if err != nil {
		return "", err
	}
	return pr.URL, nil
}

// stateCloneAndCommit clones remote into a scratch directory, writes the
// snapshot's file at its path, commits it to p.Branch and pushes that branch,
// answering the branch the clone started on (the pull request's base).
//
// The checkout is thrown away either way — it exists to carry one commit to one
// push — so a save leaves nothing behind but the snapshot itself, and a failed
// one leaves nothing at all: there is nothing in a half-made clone a person
// could use that `fugaro workspace project save` would not do again.
//
// env is the environment git runs with, nil for the process's own: GitHub's push
// is authenticated by gh's credential helper and needs nothing added, and
// Bitbucket's carries the token as a credential variable and never in a URL.
// Whatever it is, it is stateGitEnv's before the first spawn: a git that wants a
// username must fail rather than open a terminal and wait for a person to type
// one (GIT_TERMINAL_PROMPT=0, as every other git spawn in this tree does).
func stateCloneAndCommit(ctx context.Context, env []string, p statePublish, remote string) (base string, err error) {
	env = stateGitEnv(env)
	dir, err := os.MkdirTemp("", "fugaro-state-")
	if err != nil {
		return "", fmt.Errorf("making a directory to clone %s into: %w", pluginwire.Printable(p.Repo), err)
	}
	defer os.RemoveAll(dir)
	if err := stateRun(ctx, env, "", nil, stateGit, stateGitArgs("clone", "--quiet", "--", remote, dir)...); err != nil {
		return "", err
	}
	base, err = stateOutput(ctx, env, dir, stateGit, stateGitArgs("rev-parse", "--abbrev-ref", "HEAD")...)
	if err != nil {
		return "", err
	}
	if base == "" || base == "HEAD" {
		// A detached HEAD names no branch for a pull request to target, and a
		// pull request against a guess is worse than none.
		return "", fmt.Errorf("the clone of %s is on a detached HEAD, so there is no branch a pull request could target", pluginwire.Printable(p.Repo))
	}
	target := filepath.Join(dir, filepath.FromSlash(p.Path()))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, p.Bytes, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", target, err)
	}
	for _, args := range [][]string{
		{"checkout", "--quiet", "-b", p.Branch},
		{"add", "--", p.Path()},
		{"commit", "--quiet", "-m", p.Subject},
		{"push", "--quiet", "origin", p.Branch},
	} {
		if err := stateRun(ctx, env, dir, nil, stateGit, stateGitArgs(args...)...); err != nil {
			return "", err
		}
	}
	return base, nil
}

// stateGitArgs is the argv of one git a save spawns: the hardening internal/cli
// puts before every git call on a checkout that may be somebody else's
// (gitHardening), then the subcommand.
//
// A hook must never run here. The clone is of a repository the project's own
// setting named, and the operator may have a husky, a lint-staged or a pre-push
// test suite configured in it or in their global git config: a save is plumbing
// that carries one file to one branch, and a hook that fails would leave the
// snapshot unpushed for a reason that is not about the snapshot — while a hook
// that succeeds runs as this user, on the Bitbucket path with the project's
// token in its environment (stateGitEnv). --no-verify is not the answer: it
// skips pre-commit and commit-msg and nothing else.
func stateGitArgs(args ...string) []string {
	return append(slices.Clone(gitHardening), args...)
}

// stateGitEnv is the environment every git a save spawns runs with: env when the
// caller named one, the process's own otherwise, and GIT_TERMINAL_PROMPT=0 in
// both cases. A clone or a push that needs credentials — a machine with no gh
// credential helper for GitHub, a Bitbucket token the host rejects — would
// otherwise open the terminal and wait for a person to type a username, and a
// call to an underlying tool that comes back as a prompt is what this policy
// forbids in as many words (docs/no-prompt-policy/spec.md, rule 1: "Calls to
// underlying tools must also be non-interactive ... so that no prompt comes
// through from a lower layer"). Every other git spawn in this tree sets the same
// variable; with it, git fails at once and the save says what it refused with.
//
// gitops.WithVars is what sets it rather than a plain append: a
// GIT_TERMINAL_PROMPT the process already carries is REPLACED, because the first
// entry of a name is the one a program reads, and a machine that exported
// GIT_TERMINAL_PROMPT=1 would otherwise keep the prompt it asked to be rid of.
func stateGitEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	return gitops.WithVars(env, map[string]string{"GIT_TERMINAL_PROMPT": "0"})
}

// stateCapped keeps the first stateCappedBytes of what a child complained with
// and drops the rest, so a git that prints a megabyte of progress cannot grow
// the save's memory — and, because only the first line of it is ever quoted
// (oneLineCLI), keeping more than a few hundred bytes would keep nothing a
// reader sees. A write never fails, so the child is never blocked on it.
type stateCapped struct{ s string }

const stateCappedBytes = 4096

func (c *stateCapped) Write(p []byte) (int, error) {
	if room := stateCappedBytes - len(c.s); room > 0 {
		c.s += string(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (c *stateCapped) String() string { return c.s }

// stateRun runs one command for a save, with its output dropped and its exit
// status in the error. It is procgroup's own spawn, as every other child this
// tree starts is: a helper or a pager a git or a gh leaves behind must not hold
// the save open past its context.
func stateRun(ctx context.Context, env []string, dir string, stdin io.Reader, name string, args ...string) error {
	return stateRunInto(ctx, env, dir, stdin, nil, name, args...)
}

// stateRunInto is stateRun with a destination for the child's standard output.
func stateRunInto(ctx context.Context, env []string, dir string, stdin io.Reader, stdout io.Writer, name string, args ...string) error {
	bin, err := exec.LookPath(name)
	if err != nil {
		return userErr("%s is not on this machine's PATH: a save clones, commits and pushes with your own credentials, and it runs them as you", name)
	}
	if stdout == nil {
		stdout = io.Discard
	}
	var complaint stateCapped
	code, err := procgroup.Run(ctx, procgroup.Cmd{
		Name:   bin,
		Args:   args,
		Dir:    dir,
		Env:    env,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: &complaint,
		Grace:  stateGHRace,
	})
	if err != nil {
		return fmt.Errorf("running %s: %w", name, err)
	}
	if code != 0 {
		return fmt.Errorf("`%s %s` exited %d: %s", name, strings.Join(args, " "), code, oneLineCLI(complaint.String()))
	}
	return nil
}

// stateOutput runs one command and answers its standard output, trimmed.
func stateOutput(ctx context.Context, env []string, dir string, name string, args ...string) (string, error) {
	var out strings.Builder
	if err := stateRunInto(ctx, env, dir, nil, &out, name, args...); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// stateRunJSON runs one command and decodes its standard output into out.
func stateRunJSON(ctx context.Context, env []string, dir string, out any, name string, args ...string) error {
	raw, err := stateOutput(ctx, env, dir, name, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("%s answered something this build cannot read as JSON: %w", name, err)
	}
	return nil
}

// stateHostName is the host a provider kind's repositories live on, as the
// printed target names it (localcfg.ProviderHosts, the one map of that pair). A
// designation naming no kind is the default kind here — GitHub, the kind an
// empty kind opens too (stateHostsFor) — so the line a save prints before it
// pushes names the host the save is really about to reach, whichever branch of
// the designation put it there.
func stateHostName(kind string) string {
	if kind == "" {
		kind = gitprov.KindGitHub
	}
	if host := localcfg.ProviderHosts[kind]; host != "" {
		return host
	}
	return kind
}

// stateHostsFor is the opener a command hands the writer: GitHub through gh,
// Bitbucket through the token the project keeps. token is "" on a machine that
// holds none, which only a Bitbucket designation minds.
func stateHostsFor(token string) stateHostFor {
	return func(kind string) (stateHost, error) {
		switch kind {
		case gitprov.KindGitHub, "":
			return stateGitHubHost{}, nil
		case gitprov.KindBitbucket:
			if token == "" {
				return nil, userErr("a save into a Bitbucket repository pushes with the project's own Bitbucket token, and this machine holds none: store it with `%s secrets set bitbucket-token --repo <owner/name>`, or run the save where that token is", selfCommand())
			}
			return stateBitbucketHost{token: token}, nil
		}
		return nil, userErr("%q is not a git host this save knows (%s)", pluginwire.Printable(kind), strings.Join(gitprov.Kinds, " or "))
	}
}

// stateProjectContent is the body a save of one project's own documents holds:
// the project's published settings node and its spend summary for the day it was
// taken, or the workspace's audit export for a workspace snapshot (D2807).
//
// It serves the project the config it was built from names, and refuses any
// other: a save reads the documents of the project whose state it is, and a
// writer built for one project must not answer with another's figures. A caller
// saving several members (WU23) builds one content reader per member, out of
// that member's own config.
type stateProjectContent struct {
	lc *localcfg.Config
	db *rtdb.Client
	fs *firestore.Client // the workspace's audit trail's database
}

// stateMemberState is one member's body: its settings and its spend summary.
type stateMemberState struct {
	Settings json.RawMessage `json:"settings,omitempty"`
	Spend    stateSpend      `json:"spend"`
}

// stateSpend is the spend summary: the day it is about, the caps in force, and
// the counters the project and its repositories reached on it.
type stateSpend struct {
	Day    string          `json:"day"`
	Caps   json.RawMessage `json:"caps,omitempty"`
	Global json.RawMessage `json:"global,omitempty"`
	Repos  json.RawMessage `json:"repos,omitempty"`
}

// stateAuditExport is the workspace's body: its audit entries, in the order the
// trail stores them (an event id leads with its instant, so key order is time
// order). Each entry is C1's own document, encoded by C1's own codec: the export
// restates no field of the event.
type stateAuditExport struct {
	Events []json.RawMessage `json:"events"`
}

// Body implements stateContentReader.
func (c stateProjectContent) Body(ctx context.Context, s workspace.Snapshot) (json.RawMessage, error) {
	if err := c.owns(s); err != nil {
		return nil, err
	}
	if s.Audit {
		return c.auditBody(ctx)
	}
	return c.memberBody(ctx)
}

// owns holds a snapshot to the project this reader was built for.
func (c stateProjectContent) owns(s workspace.Snapshot) error {
	project := s.Member.Project
	if s.Audit {
		project = s.Workspace
	}
	if c.lc == nil {
		return errors.New("the state reader has no project config")
	}
	if project != c.lc.GCPProject && project != c.lc.Name {
		return userErr("this state reader is %s's and the snapshot is %s's, and a reader answers with the documents of the project it was built for: a save opens one reader per project, out of that project's own configuration",
			pluginwire.Printable(c.lc.Name), pluginwire.Printable(project))
	}
	return nil
}

// memberBody reads the member's settings and spend.
func (c stateProjectContent) memberBody(ctx context.Context) (json.RawMessage, error) {
	state := stateMemberState{}
	_, found, raw, err := readPolicyNode(ctx, c.db, c.lc)
	if err != nil {
		return nil, err
	}
	// An absent node is a project that has published no policy, which is not an
	// empty one: the key is left out rather than written as an empty object.
	if found {
		state.Settings = raw
	}
	// The day is the database's, not this machine's, exactly as every other
	// reader of the counters picks it (budgetNow plus the Date of the newest
	// response): a snapshot written by a machine whose clock is a day out would
	// otherwise file the spend under the wrong day and read as a project that
	// spent nothing.
	now := budgetNow()
	if sn, ok := c.db.ServerNow(); ok {
		now = sn
	}
	day := budget.Day(now)
	state.Spend.Day = budget.DayDate(day)
	for _, part := range []struct {
		path string
		into *json.RawMessage
	}{
		{"config", &state.Spend.Caps},
		{budget.PathSpendGlobal(day), &state.Spend.Global},
		{"spend/" + budget.DayKey(day) + "/repos", &state.Spend.Repos},
	} {
		var v json.RawMessage
		found, err := c.db.Get(ctx, part.path, &v)
		if err != nil {
			return nil, dbErr(c.lc, err, false)
		}
		if found {
			*part.into = v
		}
	}
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rendering the state of %s: %w", pluginwire.Printable(c.lc.Name), err)
	}
	return out, nil
}

// auditBody reads the workspace's audit trail. The read is the workspace's own
// Firestore collection, whose rules deny every client and which IAM alone
// reaches (WU1's AuditCollection).
func (c stateProjectContent) auditBody(ctx context.Context) (json.RawMessage, error) {
	if c.fs == nil {
		return nil, userErr("the audit export of %s needs the workspace project's Firestore, and this save has no client of it: run it on the workspace project's own machine", pluginwire.Printable(c.lc.Name))
	}
	docs, err := c.fs.Query(ctx, workspace.AuditCollection, "id", nil, nil)
	if err != nil {
		return nil, stateAuditReadErr(c.lc, err)
	}
	export := stateAuditExport{Events: make([]json.RawMessage, 0, len(docs))}
	for _, doc := range docs {
		fields, err := json.Marshal(doc.Fields)
		if err != nil {
			return nil, fmt.Errorf("reading the audit entry %s: %w", pluginwire.Printable(doc.ID), err)
		}
		event, err := workspace.DecodeAuditEvent(fields)
		if err != nil {
			return nil, fmt.Errorf("the audit entry %s is not an event this build reads: %w", pluginwire.Printable(doc.ID), err)
		}
		encoded, err := workspace.EncodeAuditEvent(event)
		if err != nil {
			return nil, fmt.Errorf("the audit entry %s: %w", pluginwire.Printable(doc.ID), err)
		}
		export.Events = append(export.Events, encoded)
	}
	out, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rendering the audit export of %s: %w", pluginwire.Printable(c.lc.Name), err)
	}
	return out, nil
}

// stateAuditReadErr classifies a failed read of the audit trail. It is
// reportcore's firestoreErr for the same database and the same role (the audit
// collection is in the project's own Firestore, closed to every client), and it
// is a sentence of its own because that one says "the spend history", which is
// not what a save of the trail was reading.
func stateAuditReadErr(lc *localcfg.Config, err error) error {
	if errors.Is(err, firestore.ErrPermission) {
		return userErr("project %s's audit trail refused the read: you need the Cloud Datastore Viewer role (roles/datastore.viewer) on its Firebase project %s, plus the Service Usage Consumer role (roles/serviceusage.serviceUsageConsumer); GCP owners and editors have both; gcloud auth application-default login may be needed (%s)",
			lc.Name, safetext.Strip(lc.Budget.FirebaseProject), safetext.Strip(err.Error()))
	}
	return remote(fmt.Errorf("reading the audit trail of %s: %s", lc.Name, safetext.Strip(err.Error())))
}
