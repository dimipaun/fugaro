package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// Claude Code keeps each session as ~/.claude/projects/<escaped
// workdir>/<id>.jsonl. Every run saves the session of its work chain — the
// implement or fix stage's, the conversation that built the branch — to its
// prefix (session/<id>.jsonl, then session/session.json), so a follow-up on
// its pull request can resume it. A review stage runs in a session of its
// own (agentLoop gives each review a fresh ID) and its session is saved too,
// but beside the work session rather than in its place: a follow-up must
// continue the work, not the review of it (C1.17, D138). Nothing that
// resumes a session reads these objects: --resume is given the local file,
// which is why a stage the credentials cascade interrupted is resumed from
// ~/.claude, not from the upload. The session file is written by the agent's
// own process, so it is read and written as a regular file only, never
// through a symlink, and redacted before upload.

// sessionSaveTimeout bounds the save Run makes when finalize fails, on a
// context of its own.
const sessionSaveTimeout = 30 * time.Second

// commitRE is a full commit ID. session.json is writable by the previous
// run's agent, so its head_sha is checked against it before it reaches
// any git argument (a value such as --output=/x would be an option).
var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// errSessionTooLarge means the session file is over MaxSessionBytes.
var errSessionTooLarge = errors.New("session file is larger than its cap")

// noteSession remembers the session a work stage ran in: the one the next fix
// resumes, and the one writeback saves as the run's session.
func (r *run) noteSession(req agent.Request, res agent.Result) {
	if id := r.stageSession(req, res); id != "" {
		r.sessionID = id
	}
}

// noteReviewSession remembers the session a review stage ran in, for the save
// alone (C1.17, D138). A review runs in a session of its own (agentLoop hands
// each one a fresh ID), so it is never what the next fix resumes — a fix
// continues the work, not the review of it — and it is saved beside the work
// session rather than in its place: what a follow-up must resume is the
// conversation that built the branch, not the one that read it.
func (r *run) noteReviewSession(req agent.Request, res agent.Result) {
	if id := r.stageSession(req, res); id != "" {
		r.reviewSession = id
	}
}

// stageSession is the session a stage ran in: the one its result reports
// (the result event's ID, else its init event's, so a stage killed before
// its result still names it). Claude Code may report another ID than the one
// it was asked for, and that is the one that holds the conversation. A stage
// that reported none keeps the one it was asked to resume, if any: that
// session existed before it ran.
func (r *run) stageSession(req agent.Request, res agent.Result) string {
	switch {
	case res.SessionID == "" && req.Resume && agent.ValidSessionID(req.SessionID):
		return req.SessionID
	case res.SessionID == "":
		return ""
	case !agent.ValidSessionID(res.SessionID):
		r.d.Log.Warn("the agent reported a session ID that is not a lower-case UUID; ignoring it")
		return ""
	default:
		return res.SessionID
	}
}

// sessionDir is where Claude Code keeps this run's sessions: the agent's
// HOME, and the session directory relative to it, named after the
// checkout's real path (Claude Code names it after its resolved working
// directory). Callers open an os.Root on home and reach the directory
// through it, so no link on the way can lead out of HOME.
func (r *run) sessionDir() (home, rel string, err error) {
	home = envLookup(r.d.Env, "HOME")
	if home == "" {
		return "", "", errors.New("HOME is not set")
	}
	wd := r.d.WorkDir
	if real, err := filepath.EvalSymlinks(wd); err == nil {
		wd = real
	}
	rel, err = filepath.Rel(home, agent.SessionDir(home, wd))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("the session directory is not under HOME")
	}
	return home, rel, nil
}

// saveSession uploads the run's sessions. The work chain's (implement or fix)
// goes to session/<id>.jsonl + session/session.json, exactly as before C1.17:
// it is what a follow-up resumes. A review stage's, which is a session of its
// own, is saved too (D138), beside it as session/review/<id>.jsonl +
// session/review.json — never in its place, or a follow-up would continue the
// reviewer's conversation instead of the work. Nothing here fails the run: a
// session that can't be saved is a warning, and the next follow-up starts
// fresh.
func (r *run) saveSession(ctx context.Context) {
	r.saveOneSession(ctx, r.sessionID, false)
	r.saveOneSession(ctx, r.reviewSession, true)
}

// saveOneSession reads, redacts and uploads one session: the work session, or
// (review) the latest review stage's, under its own two objects.
func (r *run) saveOneSession(ctx context.Context, id string, review bool) {
	if id == "" {
		return
	}
	log := r.d.Log.With("session", id)
	if review {
		log = log.With("stage", "review")
	}
	data, err := r.readSessionFile(id)
	switch {
	case errors.Is(err, errSessionTooLarge):
		log.Warn("session too large to save; the next follow-up starts fresh", "cap_bytes", runstore.MaxSessionBytes)
		return
	case err != nil:
		log.Warn("session not saved; the next follow-up starts fresh", "err", r.redact(err.Error()))
		return
	}
	data = r.redactLines(data)
	if len(data) > runstore.MaxSessionBytes {
		// Redaction can lengthen it: "[REDACTED]" is longer than a short
		// secret. A follow-up would refuse it, so it isn't stored.
		log.Warn("session too large to save once redacted; the next follow-up starts fresh", "cap_bytes", runstore.MaxSessionBytes)
		return
	}
	head := r.rec.PushedHead
	if head == "" {
		if r.repo == nil {
			log.Warn("session not saved: no checkout")
			return
		}
		if head, err = r.repo.HeadSHA(ctx); err != nil {
			log.Warn("session not saved: reading the head failed", "err", r.redact(err.Error()))
			return
		}
	}
	m := runstore.SessionMeta{Version: 1, ID: id, HeadSHA: head, Pushed: r.rec.PushedHead != "",
		WorkDir: r.d.WorkDir, Bytes: int64(len(data))}
	put := r.d.Store.PutSession
	if review {
		put = r.d.Store.PutReviewSession
	}
	if err := put(ctx, m, data); err != nil {
		log.Warn("storing the session failed; the next follow-up starts fresh", "err", r.redact(err.Error()))
		return
	}
	log.Info("session saved", "bytes", len(data))
}

// readSessionFile reads session id's file through an os.Root on HOME, as
// a regular file of at most MaxSessionBytes.
func (r *run) readSessionFile(id string) ([]byte, error) {
	home, rel, err := r.sessionDir()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, fmt.Errorf("opening HOME: %w", err)
	}
	defer root.Close()
	name := filepath.Join(rel, id+".jsonl")
	li, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("the session file: %w", err)
	}
	if !li.Mode().IsRegular() {
		return nil, fmt.Errorf("the session file is not a regular file (%s)", li.Mode().Type())
	}
	// openRootNoFollow passes O_NOFOLLOW and O_NONBLOCK where the platform
	// has them: a symlink or FIFO swapped in after the Lstat fails the open,
	// or at least never blocks it; SameFile below refuses anything that
	// isn't the file Lstat saw. (openfile_other.go states what the Windows
	// half of that pair loses.)
	f, err := openRootNoFollow(root, name)
	if err != nil {
		return nil, fmt.Errorf("opening the session file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("the session file: %w", err)
	}
	if !fi.Mode().IsRegular() || !os.SameFile(li, fi) {
		return nil, errors.New("the session file changed while it was opened")
	}
	if fi.Size() > runstore.MaxSessionBytes {
		return nil, errSessionTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, runstore.MaxSessionBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the session file: %w", err)
	}
	if len(data) > runstore.MaxSessionBytes {
		return nil, errSessionTooLarge
	}
	return data, nil
}

// redactLines redacts each line of data with the run's secrets.
func (r *run) redactLines(data []byte) []byte {
	redact := agent.RedactFunc(r.secretList())
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		lines[i] = redact(l)
	}
	return []byte(strings.Join(lines, "\n"))
}

// restored is what restoreSession decided: resume session ID, or start
// fresh (Resumed false) for the reason in Note. Moved lists the commits
// made on the branch since the resumed session (git log --oneline, at
// most followup.MaxMovedCommits), for the agent to re-read.
type restored struct {
	ID      string
	Resumed bool
	Note    string
	Moved   []string
}

// sessionGit runs a read-only git command in repo; tests may replace it.
//
// --no-replace-objects is passed to every call, as every read in gitops passes
// it: the checkout is the agent's, so refs/replace is a ref it can plant, and a
// read that follows one answers about a commit the run never made — a range
// that diffs as a run which added nothing, while the commit the branch really
// carries holds what the rule is looking for.
var sessionGit = func(ctx context.Context, repo *gitops.Repo, args ...string) (string, error) {
	var stdout bytes.Buffer
	if err := sessionGitOut(ctx, repo, &stdout, args...); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// sessionGitHead is sessionGit for a read whose answer is in a file's head and
// whose output can be the whole of a file: at most n bytes are kept, and every
// byte is still read (see headWriter).
func sessionGitHead(ctx context.Context, repo *gitops.Repo, n int, args ...string) ([]byte, error) {
	stdout := &headWriter{n: n}
	if err := sessionGitOut(ctx, repo, stdout, args...); err != nil {
		return nil, err
	}
	return stdout.buf, nil
}

// sessionGitOut runs one read-only git command in repo with its stdout going
// to out — every session read goes through here, so every one of them passes
// --no-replace-objects and reports a failure the same way.
func sessionGitOut(ctx context.Context, repo *gitops.Repo, out io.Writer, args ...string) error {
	args = append([]string{"--no-replace-objects"}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo.Dir
	cmd.Env = repo.Environ()
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// headWriter keeps the first n bytes written to it and drops the rest, while
// reporting every write as taken: git writing a file's whole content must be
// read to the end, or it ends up blocked on a full pipe with nobody reading.
type headWriter struct {
	buf []byte
	n   int
}

func (w *headWriter) Write(p []byte) (int, error) {
	n := len(p)
	if room := w.n - len(w.buf); room > 0 {
		if room < n {
			p = p[:room]
		}
		w.buf = append(w.buf, p...)
	}
	return n, nil
}

// restoreSession puts the previous run's saved session (prev) in place
// for a follow-up whose checkout is at head, when it is safe to resume:
// its ID is a session ID, it ran in this workdir, its file is there and
// within the cap, and its head is an ancestor of head. Every refusal is a
// fresh start with a Note saying which check failed, never an error.
func (r *run) restoreSession(ctx context.Context, prev *runstore.Store, head string) restored {
	fresh := func(note string) restored {
		r.d.Log.Info("starting a fresh session", "note", note)
		return restored{Note: note}
	}
	m, data, err := prev.ReadSession(ctx)
	switch {
	case m == nil && errors.Is(err, runstore.ErrNotFound):
		return fresh("the previous run saved no session")
	case m == nil:
		r.d.Log.Warn("reading the previous session failed", "err", r.redact(err.Error()))
		return fresh("the previous run's session could not be read")
	case !agent.ValidSessionID(m.ID):
		return fresh("the previous run's session ID is not valid")
	case errors.Is(err, runstore.ErrInvalidSession):
		return fresh("the previous run's session metadata is not usable")
	case m.WorkDir != r.d.WorkDir:
		return fresh("the previous session ran in another working directory")
	case !commitRE.MatchString(m.HeadSHA):
		return fresh("the previous session's head is not a commit ID")
	case !commitRE.MatchString(head):
		return fresh("this run's head is not a commit ID")
	case errors.Is(err, runstore.ErrTooLarge):
		r.d.Log.Warn("session too large to restore; starting fresh", "cap_bytes", runstore.MaxSessionBytes)
		return fresh("the previous session file is too large to restore")
	case errors.Is(err, runstore.ErrNotFound):
		return fresh("the previous session file is missing")
	case err != nil:
		r.d.Log.Warn("reading the previous session file failed", "err", r.redact(err.Error()))
		return fresh("the previous session file could not be read")
	case int64(len(data)) != m.Bytes:
		return fresh("the previous session file does not match its recorded size")
	case !m.Pushed:
		// Its head is the previous run's local HEAD, which the branch
		// never had: resuming would pick up work that isn't there.
		return fresh("the previous session's head commit was never pushed")
	}
	// Exit 1 (not an ancestor) and 128 (an unknown commit) both mean
	// the session saw a history the branch no longer has.
	if _, err := sessionGit(ctx, r.repo, "merge-base", "--is-ancestor", m.HeadSHA, head); err != nil {
		return fresh("the branch was rewritten since the previous session")
	}
	var moved []string
	if m.HeadSHA != head {
		out, err := sessionGit(ctx, r.repo, "log", "--oneline", "--no-decorate", "-n", fmt.Sprint(followup.MaxMovedCommits), m.HeadSHA+".."+head)
		if err != nil {
			r.d.Log.Warn("listing the commits since the previous session failed", "err", r.redact(err.Error()))
			return fresh("the commits since the previous session could not be listed")
		}
		if out != "" {
			moved = strings.Split(out, "\n")
		}
	}
	switch err := r.writeSessionFile(m.ID, data); {
	case errors.Is(err, fs.ErrExist):
		return fresh("a session file is already in place")
	case err != nil:
		r.d.Log.Warn("writing the previous session failed", "err", r.redact(err.Error()))
		return fresh("the previous session could not be written")
	}
	res := restored{ID: m.ID, Resumed: true, Moved: moved}
	if len(moved) > 0 {
		res.Note = "the branch has new commits since the previous session"
	}
	r.d.Log.Info("session restored", "session", m.ID, "new_commits", len(moved))
	return res
}

// prepareResume is a resumed run's bootstrap (R18, D3185): it reads the pause
// the run it continues left in its own prefix, checks that this run's checkout
// is the checkpoint that pause names, and puts the saved session in place, so
// the run enters the stage the paused one was in rather than the first one.
//
// Every way the pause is not one this image can act on ends the run, named
// (R18): a version it does not read, a record whose shape it cannot place, a
// session that is not there, a head that is not the checkpoint. It never falls
// back to a fresh session the way a follow-up does, and it never checks the
// checkpoint out: what the paused run had half-done is in the session and the
// commit alone, so a resume that cannot have both starts nothing at all. The
// error it returns carries runstore.ErrInvalidPause, which is the name the run
// ends with (Run's own bootstrap handling writes it to the record's reason).
//
// The pause record is written inside the paused run's job and is trusted
// exactly as far as the run record a follow-up already trusts is, and no
// further (R23, R26): every member of it that this run acts on — the version,
// the stage, the session's name, the checkpoint — is checked by ReadPause and
// by the restore below before anything is checked out, restored or started.
func (r *run) prepareResume(ctx context.Context) error {
	pausedID := r.spec.Resumes
	paused := r.d.Store.Sibling(pausedID)
	rec, err := paused.ReadPause(ctx)
	if err != nil {
		// ReadPause carries ErrInvalidPause itself for a record this image
		// cannot read (a version, a member it cannot place, a member that is
		// not what it must be). A run that never suspended has no record at
		// all, and a resume of one is refused the same way: there is no pause
		// to pick up, and starting the work afresh is not this run's to do.
		return fmt.Errorf("resuming run %s: %w", pausedID, err)
	}
	// What the paused run worked from, read before anything is restored: a
	// resume continues the chain's questions as well as its work. Suspending
	// never ended a ruling — a run launched with `--owner-ruled` that paused
	// mid-stage has an answer the owner has already given — and a resumed run
	// whose reviewers were told nothing would mark the same finding needs_owner
	// and stop on a settled question, while its own record, written with no
	// ruling, would drop that answer for every run after it. The record is an
	// object the paused run's own job wrote beside the pause, so a resume that
	// cannot read it cannot say what it is continuing and refuses rather than
	// continuing the work without the answers it was given (R18, R23).
	prevRec, err := paused.ReadRecord(ctx)
	if err != nil {
		r.d.Log.Warn("reading the paused run's record failed", "paused_run", pausedID, "err", r.redact(err.Error()))
		return fmt.Errorf("resuming run %s: %w: it has no readable record, so what it worked from cannot be read", pausedID, runstore.ErrInvalidPause)
	}
	r.inheritRulings(prevRec, pausedID)
	if !gitprov.SameCommit(r.follow.startSHA, rec.Checkpoint) {
		return fmt.Errorf("resuming run %s: %w: it paused at %s and %s is at %s, so this checkout is not the work in progress it pushed",
			pausedID, runstore.ErrInvalidPause, rec.Checkpoint, r.spec.Branch, r.follow.startSHA)
	}
	res := r.restoreSession(ctx, paused, rec.Checkpoint)
	if !res.Resumed {
		return fmt.Errorf("resuming run %s: %w: %s", pausedID, runstore.ErrInvalidPause, res.Note)
	}
	if res.ID != rec.Session {
		// The record names the session the paused run saved, and the session
		// stored under its prefix is a different one: one of the two is not
		// the work this run is meant to carry on, and neither is a guess this
		// run may make (R18).
		return fmt.Errorf("resuming run %s: %w: its pause names session %s and the session it saved is %s",
			pausedID, runstore.ErrInvalidPause, rec.Session, res.ID)
	}
	stage, err := resumeStage(rec.Stage)
	if err != nil {
		return fmt.Errorf("resuming run %s: %w", pausedID, err)
	}
	r.follow.restored, r.follow.resumedStage = res, stage
	// A resume reads no comment of the pull request (its prompt says so: it
	// acts on its launch's instructions alone). The delimiters that would wrap
	// one — and the ruling inherited above — still have to be a nonce this run
	// chose: the builders refuse an empty one, so text from outside could never
	// pass for the prose around a block, and a resume that emitted none would
	// panic where it is read.
	r.follow.nonce = followup.NewNonce()
	// The record says what a follow-up's own record says (runstore.FollowUp):
	// which session this run continued, and why. Its Comments stays zero — a
	// resume reads no comment of the pull request and acts on its launch's
	// instructions alone.
	r.rec.FollowUp.Session, r.rec.FollowUp.SessionNote = "resumed", res.Note
	// And the rulings this run works from, onto its own record (OD1): the pair
	// the paused run held, behind whatever its own launch carried, which is
	// what a later follow-up on this chain inherits (recordRuling,
	// workingRulings). Written before the save so the record a reader finds
	// while the run goes says the same as the briefs the agent is handed.
	r.recordRuling()
	r.save(ctx)
	r.d.Log.Info("resuming the paused run", "paused_run", pausedID, "stage", stage, "session", res.ID, "checkpoint", rec.Checkpoint)
	return nil
}

// resumeStage is the stage a resume enters: the stage the paused run was in,
// when it is one this image can enter. Those are the runner's two work stages
// and the review stages — the stages whose whole state is what the pause
// names, the session the branch was worked in, the checkout at the commit it
// pushed and the pull request's own diff — so entering one of them again is
// carrying on the paused stage and not starting it afresh.
func resumeStage(stage string) (string, error) {
	switch {
	case stage == "implement" || stage == "fix" || config.IsReviewStage(stage):
		return stage, nil
	}
	// Anything else — a plan, a render, a digest, a finalize, a stage a later
	// release adds — is a stage this image cannot enter from a standing start:
	// what it had half-done lives in no record this release reads, and a run
	// that started another stage instead would be the fresh start R18 refuses.
	return "", fmt.Errorf("%w: the paused run was in stage %q, which this image cannot enter", runstore.ErrInvalidPause, stage)
}

// writeSessionFile writes data as session id's file through an os.Root on
// HOME, creating the project directory 0700 and the file 0600, and never
// replacing a file (or following a symlink) already there.
//
// The modes are the POSIX half of what keeps the transcript to its owner — it
// is redacted, but it is still this run's own turns — and they are not claimed
// to protect it on Windows: Go's os.Chmod there sets the read-only attribute
// and nothing else, so what keeps another local user out is the ACL of HOME.
// **D12 (file modes) is open** in docs/windows-cli/reconciliation.md, the
// Windows CLI gets its release in **Slice 5**, and docs/windows-cli/README.md
// records that a Windows release must not ship without that decision. The long
// form of this note is at the deployer credential in internal/cli/managed_apply.go.
func (r *run) writeSessionFile(id string, data []byte) error {
	home, rel, err := r.sessionDir()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return fmt.Errorf("opening HOME: %w", err)
	}
	defer root.Close()
	if err := root.MkdirAll(rel, 0o700); err != nil {
		return fmt.Errorf("creating the session directory: %w", err)
	}
	name := filepath.Join(rel, id+".jsonl")
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = root.Remove(name)
		return fmt.Errorf("writing the session file: %w", werr)
	}
	return nil
}
