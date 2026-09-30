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
	"syscall"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// Claude Code keeps each session as ~/.claude/projects/<escaped
// workdir>/<id>.jsonl. Every run saves the session of its latest implement
// or fix stage to its prefix (session/<id>.jsonl, then
// session/session.json), so a follow-up on its pull request can resume
// it. The session file is written by the agent's own process, so it is
// read and written as a regular file only, never through a symlink, and
// redacted before upload.

// sessionSaveTimeout bounds the save Run makes when finalize fails, on a
// context of its own.
const sessionSaveTimeout = 30 * time.Second

// commitRE is a full commit ID. session.json is writable by the previous
// run's agent, so its head_sha is checked against it before it reaches
// any git argument (a value such as --output=/x would be an option).
var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// errSessionTooLarge means the session file is over MaxSessionBytes.
var errSessionTooLarge = errors.New("session file is larger than its cap")

// noteSession remembers the session a stage ran in: the one its result
// reports (the result event's ID, else its init event's, so a stage killed
// before its result still names it). Claude Code may report another ID
// than the one it was asked for, and that is the one that holds the
// conversation. A stage that reported none keeps the one it was asked to
// resume, if any: that session existed before it ran.
func (r *run) noteSession(req agent.Request, res agent.Result) {
	switch {
	case res.SessionID == "" && req.Resume && agent.ValidSessionID(req.SessionID):
		r.sessionID = req.SessionID
	case res.SessionID == "":
	case !agent.ValidSessionID(res.SessionID):
		r.d.Log.Warn("the agent reported a session ID that is not a lower-case UUID; ignoring it")
	default:
		r.sessionID = res.SessionID
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

// saveSession uploads the session of the latest implement or fix stage.
// Nothing here fails the run: a session that can't be saved is a
// warning, and the next follow-up starts fresh.
func (r *run) saveSession(ctx context.Context) {
	if r.sessionID == "" {
		return
	}
	log := r.d.Log.With("session", r.sessionID)
	data, err := r.readSessionFile(r.sessionID)
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
	m := runstore.SessionMeta{Version: 1, ID: r.sessionID, HeadSHA: head, Pushed: r.rec.PushedHead != "",
		WorkDir: r.d.WorkDir, Bytes: int64(len(data))}
	if err := r.d.Store.PutSession(ctx, m, data); err != nil {
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
	// O_NOFOLLOW and O_NONBLOCK: a symlink or FIFO swapped in after the
	// Lstat fails the open, or at least never blocks it; SameFile below
	// refuses anything that isn't the file Lstat saw.
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
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
	redact := agent.RedactFunc(r.secrets)
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
var sessionGit = func(ctx context.Context, repo *gitops.Repo, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo.Dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), repo.Env...)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
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

// writeSessionFile writes data as session id's file through an os.Root on
// HOME, creating the project directory 0700 and the file 0600, and never
// replacing a file (or following a symlink) already there.
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
