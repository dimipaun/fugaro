package diagnoseagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/cmdlog"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/redact"
)

// The evidence folder is what the agent reads (T-DG3's guide says what is in
// it, and Prompt points the agent at it rather than at each file). This file
// collects it: the failed command's log, `fugaro doctor`'s output, the version
// the binary is, and the effective configuration — every one of them through
// internal/redact, because the log holds text nobody here wrote (F5) and the
// configuration holds whatever the user put in it.
//
// The folder is `<UserDir state>/fugaro/troubleshoot/<run ID>` (D1603: the
// diagnosis's own outputs live in the state directory, beside the logs of
// D1351). It is created 0700 and its files 0600, and every component of the
// path is checked before it is used: the path is attacker-influenced — an
// agent's session often points XDG_STATE_HOME at a temporary directory — and
// the names are predictable.
const (
	// logSuffix ends the evidence file the log is written to, so the file's
	// name is the log store's own (`<run ID>.log`, D1609).
	logSuffix = ".log"
	// doctorFileName, versionFileName and configFileName are the other three.
	// Each is named for what it holds, because the agent reads a folder rather
	// than a list.
	doctorFileName  = "doctor.txt"
	versionFileName = "version.txt"
	configFileName  = "config.yaml"

	// evidenceDirMode and evidenceFileMode are D1603's modes: the folder holds
	// the failing command's own output and the user's configuration, and its
	// neighbours of this account have no business in either.
	evidenceDirMode  fs.FileMode = 0o700
	evidenceFileMode fs.FileMode = 0o600
)

// The two bounds of a collection, as variables because a test drives them
// directly and nothing else does: neither is a setting, and neither is worth a
// field in Options for a caller that cannot answer the question better than
// these do.
var (
	// doctorTimeout bounds `fugaro doctor` (F23). Doctor makes cloud calls and
	// may hang exactly when the cloud is broken, which is the one state this
	// whole diagnosis exists for, so the call is abandoned rather than
	// waited on. What it costs is a doctor that was slow rather than broken;
	// what it buys is that a broken cloud cannot hang the diagnosis.
	doctorTimeout = 20 * time.Second
	// maxLogBytes is how much of a log one collection reads. A log is a
	// command's whole stream and unbounded, so it is read through this cap
	// rather than into memory whole — the same number internal/runstore
	// keeps for a stage transcript, which is the same kind of text — and what
	// is kept is the end of it (see logText), because that is where a command
	// that failed says why.
	maxLogBytes int64 = 32 << 20
)

// evidenceParts are the two components the evidence folder sits under inside
// the state directory: `<state>/fugaro/troubleshoot`. They are one component
// at a time because each of them is checked (evidenceDir), the way
// internal/report checks the log directory's own.
var evidenceParts = []string{"fugaro", "troubleshoot"}

// Options is one Collect call. Everything a diagnosis cannot read for itself is
// a field here rather than a decision inside this package: the log is the log
// store's (T-DG1), doctor is the CLI's, and the effective configuration is the
// project layer's read, which the CLI already owns. Every one of them has a
// default or a defined absence, so a test can collect the evidence of one
// failure without a cloud, a checkout or a command log.
type Options struct {
	// Version is the installed fugaro version (`cli.Version`), and Commit the
	// commit the binary was built from (`cli.Commit`, "unknown" for a build
	// with none). Both are written down: the version is what a report names,
	// and the commit is what the source tree is anchored to (D1610).
	Version string
	Commit  string
	// Getenv and GOOS place the evidence folder (localcfg.UserDir).
	Getenv func(string) string
	GOOS   string
	// Open reads the log the run ID names. nil is cmdlog.Open with Getenv,
	// which is what the command uses; a test hands over a reader.
	Open func(cmdlog.ID) (io.ReadCloser, error)
	// Doctor returns `fugaro doctor`'s output. nil means this diagnosis has no
	// doctor to run, which is written down as such rather than failed over: a
	// doctor makes cloud calls, and the cloud being broken is common enough
	// that it may not answer at all (F23).
	Doctor func(ctx context.Context) ([]byte, error)
	// Config returns the effective configuration as text — the checkout's
	// fugaro.yaml resolved over the project layer, with the values both
	// resolve to, secrets and all: redaction is this file's, in one place
	// (writeFiles). nil means there is none to collect, and is written down
	// the way a missing doctor is.
	Config func(ctx context.Context) ([]byte, error)
	// Secrets are the secret values redaction knows besides the shapes
	// internal/redact's pattern pass carries (T-DG2): the credential this
	// diagnosis holds (creds.Handle.Secret), and any value the caller knows
	// the log may hold.
	Secrets []string
}

// withDefaults fills in what a caller may leave out. A nil Getenv reads an
// empty environment, the way internal/report and internal/cmdlog read one: a
// diagnosis with nowhere to keep its evidence has to say so rather than reach
// for the machine's own directories.
func (o Options) withDefaults() Options {
	if o.Getenv == nil {
		o.Getenv = func(string) string { return "" }
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.Open == nil {
		o.Open = func(id cmdlog.ID) (io.ReadCloser, error) { return cmdlog.Open(o.Getenv, id) }
	}
	return o
}

// Collect gathers the evidence of one failed command and returns the Evidence
// the prompt and the agent run read: the run ID, the command its log names, the
// version, and the folder everything was written to. The caller adds what this
// collector cannot know — the source tree and its note (D1611), the analysis's
// nonce — before it hands the Evidence on.
//
// The log is read first and is the one thing a collection cannot do without: a
// run whose log is gone is not a diagnosis with less evidence but no diagnosis
// at all, so nothing is created before it is in hand. Everything after it
// degrades into a note in the folder instead (F23), so a broken cloud or a
// configuration that cannot be read leaves the person with the log, the
// version and the reason.
func Collect(ctx context.Context, id cmdlog.ID, o Options) (Evidence, error) {
	o = o.withDefaults()
	// The folder is named after the run ID, so an ID nothing wrote is refused
	// here rather than becoming a directory name: ParseID is the one place the
	// shape is decided (T-DG1), and this asks it about the spelling this call
	// is about to use.
	if _, err := cmdlog.ParseID(id.String()); err != nil {
		return Evidence{}, fmt.Errorf("diagnoseagent: %w", err)
	}
	ev := Evidence{RunID: id.String(), Command: id.Command, Version: o.Version}
	logText, err := o.logText(id)
	if err != nil {
		return Evidence{}, err
	}
	files := []evidenceFile{
		{name: id.String() + logSuffix, text: logText},
		{name: doctorFileName, text: o.doctorText(ctx)},
		{name: versionFileName, text: o.versionText()},
		{name: configFileName, text: o.configText(ctx)},
	}
	base, dir, err := o.writeFiles(id, files)
	if err != nil {
		return Evidence{}, fmt.Errorf("diagnoseagent: no evidence for %s: %w", id, err)
	}
	ev.EvidenceDir = filepath.Join(base, dir)
	return ev, nil
}

// evidenceFile is one file of the folder.
type evidenceFile struct {
	name string
	text string
}

// logText reads the log the run ID names and returns what goes into the
// evidence file, redacted.
//
// It is read through maxLogBytes, and a log past the cap is kept by its end:
// the file holds the last maxLogBytes of the log, under a first line that says
// how much was left out, because a failed command's reason is the last thing it
// wrote. A log that fits is written as it stands, and the two are told apart by
// the note rather than by a marker in the text.
//
// One thing the cap costs, and it is not paid back anywhere: a value that
// straddles the byte the cap cuts at is split, and neither pass knows half of
// one — only the part after the cut is redacted. Removing that would mean
// redacting the whole log before the cut, which is the memory this cap exists
// to bound, or a streaming redactor internal/redact does not have (T-DG2).
// What it can cost is bounded by the note above: the file says it is the end of
// a longer log, so a fragment at its very first byte is a fragment the agent
// knows was cut, and the rest of maxLogBytes — the failure itself — is whole.
func (o Options) logText(id cmdlog.ID) (string, error) {
	r, err := o.Open(id)
	if err != nil {
		return "", fmt.Errorf("the log of %s cannot be read, so there is nothing for a diagnosis to explain: %w", id, err)
	}
	defer r.Close()
	data, total, err := tailOf(r, maxLogBytes)
	if err != nil {
		return "", fmt.Errorf("reading the log of %s: %w", id, err)
	}
	var b strings.Builder
	switch {
	case maxLogBytes <= 0:
		// No cap at all is nothing read rather than everything: a caller that
		// asks for an empty read gets an empty file that says so, which is the
		// difference between "the log said nothing" and "the log was not read".
		fmt.Fprintf(&b, "The log of %s was not read: this diagnosis reads a log through a %d-byte cap.\n", id, maxLogBytes)
	case total > int64(len(data)):
		fmt.Fprintf(&b, "The log of %s is %d bytes; only its last %d bytes are below, because a log is read through a %d-byte cap. The beginning was left out: a command that failed says why at its end.\n\n",
			id, total, len(data), maxLogBytes)
	}
	b.Write(data)
	return b.String(), nil
}

// tailOf reads r and returns at most max bytes of its end, the number of bytes
// it holds, and the read's error. It is read a chunk at a time and the front is
// dropped as it goes, so a stream of any size costs max bytes of memory — the
// buffer is slid rather than grown. The whole stream is still read, because
// what was left out is a number the file has to state (logText), and the reader
// is a local file the log store wrote.
func tailOf(r io.Reader, max int64) ([]byte, int64, error) {
	if max <= 0 {
		return nil, 0, nil
	}
	var buf []byte
	var total int64
	chunk := make([]byte, 32<<10)
	if int64(len(chunk)) > max {
		chunk = make([]byte, max)
	}
	// A reader that answers every call with nothing and no error is not a
	// reader that reached its end, and without this the loop below would spin
	// on it forever. Open is a seam a caller supplies, so it is not only this
	// package's own file reader that has to terminate (io.Copy gives up after
	// the same number of empty reads).
	const maxEmptyReads = 100
	empty := 0
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			empty = 0
			total += int64(n)
			buf = append(buf, chunk[:n]...)
			if int64(len(buf)) > max {
				copy(buf, buf[int64(len(buf))-max:])
				buf = buf[:max]
			}
		} else if err == nil {
			empty++
			if empty >= maxEmptyReads {
				return nil, total, io.ErrNoProgress
			}
		}
		if errors.Is(err, io.EOF) {
			return buf, total, nil
		}
		if err != nil {
			return nil, total, err
		}
	}
}

// doctorText is the doctor's own output, or a line saying why there is none.
// The timeout is this collector's (F23), and the note is what tells the agent
// "doctor found nothing wrong" from "doctor never answered" — a distinction the
// diagnosis turns on, because the cloud being broken is what a person runs this
// command about.
//
// The output is not tidied: it goes through the redactor with everything else
// when it is written (writeFiles), and the agent reads it as the text it is.
func (o Options) doctorText(ctx context.Context) string {
	const what = "fugaro doctor"
	if o.Doctor == nil {
		return what + " was not run: this diagnosis has no doctor to run. It makes cloud calls, so this is often how a broken cloud shows up here.\n"
	}
	dctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	out, err := o.Doctor(dctx)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(dctx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf("%s did not finish within %s and was abandoned: it makes cloud calls, and a cloud that does not answer is one of the things this diagnosis exists to explain. Nothing it printed is here.\n", what, doctorTimeout)
	case err != nil:
		return fmt.Sprintf("%s failed: %v\n", what, err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return what + " printed nothing.\n"
	}
	return string(out)
}

// versionText is the two facts a report has to carry: the version that failed
// and the commit it was built from. A build with no commit says so rather than
// printing the empty string, because "unknown" is a fact about the build
// (D1611: a dev build has no release tag) and a blank line is not.
func (o Options) versionText() string {
	commit := o.Commit
	if commit == "" || commit == "unknown" {
		commit = "none: this build carries no commit, so it is not a release build"
	}
	return fmt.Sprintf("version: %s\ncommit: %s\n", o.Version, commit)
}

// configText is the effective configuration, under a first line that says what
// the file is: the agent reads a folder and has to know which of these files
// the values came from. The text is the caller's — the CLI's own resolution of
// the checkout over the project layer — and it is redacted with everything else
// when it is written.
func (o Options) configText(ctx context.Context) string {
	const header = "# The effective configuration: this checkout's fugaro.yaml resolved over the project layer, with the values both resolve to and the secrets removed.\n"
	if o.Config == nil {
		return header + "# It was not collected: this diagnosis has no configuration to read.\n"
	}
	data, err := o.Config(ctx)
	if err != nil {
		return header + fmt.Sprintf("# It could not be read: %v\n", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return header + "# Nothing in it is set: no fugaro.yaml was found and the project layer sets nothing.\n"
	}
	return header + string(data)
}

// writeFiles creates the folder and writes every file into it, redacted, and
// returns the state directory and the folder's name inside it.
//
// The redaction is here and nowhere else, so that "every file this collector
// writes passed both passes" is one line a reader can check rather than a rule
// each text builder has to remember. redact.Text is the exact pass and the
// pattern pass together: the values the caller named, and the shapes nobody
// named (F4).
func (o Options) writeFiles(id cmdlog.ID, files []evidenceFile) (base, dir string, err error) {
	base, err = localcfg.UserDir(o.Getenv, o.GOOS, localcfg.DirState)
	if err != nil {
		return "", "", fmt.Errorf("%w, so the evidence of %s cannot be kept", err, id)
	}
	if err := os.MkdirAll(base, evidenceDirMode); err != nil {
		return "", "", err
	}
	// Everything is resolved inside a root opened at the state directory, so
	// no link along the path can take the evidence — the log holds text nobody
	// here wrote — somewhere else. The root alone is not the refusal: os.Root
	// follows a link that stays inside it, and the state directory holds other
	// tools' files, so every component is Lstat'ed as well (evidenceDir).
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	dir, err = evidenceDir(root, id)
	if err != nil {
		return "", "", err
	}
	for _, f := range files {
		if err := writeEvidence(root, dir, f.name, redact.Text(f.text, o.Secrets)); err != nil {
			return "", "", err
		}
	}
	return base, dir, nil
}

// evidenceDir returns the folder's name inside root, creating it 0700 (and
// tightening one that exists), and refusing what is already at a name it may
// not use: a symlink, something that is not a directory, a directory another
// account owns. Every component is checked, not only the last, because
// Lstat'ing the whole path resolves a link at `fugaro` on the way — a link
// there would decide where the evidence of this run's own failure is left, and
// who can read it (F6a, the rule internal/report and internal/srctree apply to
// the directories they write).
//
// The run ID's component is deliberately reused rather than removed: one
// diagnosis exists per failed command, and a second collection of the same run
// ID replaces what the first wrote (writeEvidence) instead of leaving a folder
// of evidence for a diagnosis that is no longer the one being run.
func evidenceDir(root *os.Root, id cmdlog.ID) (string, error) {
	dir := ""
	for _, part := range evidenceParts {
		dir = filepath.Join(dir, part)
		if err := checkEvidenceDir(root, dir); err != nil {
			return "", err
		}
	}
	dir = filepath.Join(dir, id.String())
	if err := checkEvidenceDir(root, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// checkEvidenceDir creates one component of the folder's path inside root, or
// refuses what is already at it. It is the check internal/report's
// checkLogDir makes before it writes a log, through the root rather than by
// path because this caller has one.
func checkEvidenceDir(root *os.Root, name string) error {
	fi, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := root.Mkdir(name, evidenceDirMode); err != nil {
			return err
		}
		// Mkdir applies the umask, so the mode is set once more rather than
		// assumed.
		return root.Chmod(name, evidenceDirMode)
	case err != nil:
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", name)
	}
	if !ownedByUser(fi) {
		return fmt.Errorf("%s is not this user's directory, so this diagnosis will not leave a log it read in it", name)
	}
	if fi.Mode().Perm() != evidenceDirMode {
		_ = root.Chmod(name, evidenceDirMode)
	}
	return nil
}

// writeEvidence writes one file of the folder, replacing whatever is at its
// name and never writing through a name already there: the path is
// attacker-influenced and the names are predictable, so what is at the name
// decides nothing (the rule internal/srctree's writeRecord makes for its own
// record). A name holding a file is replaced, because a second collection of
// one run ID is the diagnosis as it is now; a name holding something else — a
// directory, which Remove cannot take — is refused rather than removed, as
// internal/srctree refuses an occupied one. The file is 0600 from its first
// byte.
func writeEvidence(root *os.Root, dir, name, text string) error {
	at := filepath.Join(dir, name)
	if err := root.Remove(at); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := root.OpenFile(at, os.O_WRONLY|os.O_CREATE|os.O_EXCL, evidenceFileMode)
	if err != nil {
		return err
	}
	_, werr := io.WriteString(f, text)
	return errors.Join(werr, f.Close())
}
