package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/shellword"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// update_retry.go is the retry offer of `fugaro update` (T-IS19): after a run
// that converged, the door says which of this machine's own reports a release
// it just installed fixed, and names the command the report recorded, so the
// person can run it again.
//
// It is an offer and nothing else. It runs no command, reads no stdin and
// writes nothing anywhere but this machine's own issue file; the shared store
// is read, never written (D1735), and every way that read can fail is a line
// not printed rather than a failed update — `fugaro update` is the whole
// upgrade, and an offer to retry something is not worth an installation that
// stops half way.

// retryOfferPrefix is the offer's line, the one a person reads and copies
// from. The command follows it, printed as data.
const retryOfferPrefix = "You can retry: "

// updateRetryBucket opens the runs bucket the shared store is read from: the
// selected project's own bucket, which is where the issues prefix lives
// (D1727). It is a seam because the tests of this file drive the hint over a
// bucket of their own.
//
// The selection is the converge's, minus everything that speaks: an offer may
// print no line at all, so no project header and no note (selectNamed, not
// selectProject, which announces), and no shared config to fall back to — an
// update that converged selected a project to converge, and an offer does not
// get to be the thing that finds a second config. dir is the checkout the run
// converged ("" is the working directory), and it selects in both places the
// converge selected in: the checkout whose fugaro.yaml names the project, and
// the origin a config is matched by.
var updateRetryBucket = func(ctx context.Context, o cloudOptions, p retryProject) (*blobx.Bucket, error) {
	co, err := checkoutProject(ctx, p.dir)
	if err != nil {
		return nil, err
	}
	if p.dir != "" {
		// The origin that selects is the checkout's, not the working
		// directory's (loadRepoConfig's own line, for the same reason).
		o.originDir = p.dir
	}
	// The converge's own call, name and all (loadInitConfig's selectNamed):
	// on a machine with several project configs only --name selects one, and
	// an offer that resolved them without it would find no project at all.
	// creating is the same argument the converge passed and means nothing is
	// written here — it is what makes a machine with no config for the project
	// answer "no config" (lc nil, and the offer stops) instead of a refusal.
	_, lc, err := selectNamed(o, co, true, p.name)
	if err != nil {
		return nil, err
	}
	if lc == nil || (lc.Bucket == "" && lc.RunsBucket == "") {
		return nil, errors.New("no project with a runs bucket is selected")
	}
	return blobx.Open(ctx, lc.BucketURL())
}

// retryProject is the project the run this offer follows converged, as the
// door still knows it after the fact: dir is the checkout --repo converged
// ("" is the working directory), and name is the --name the run selected with.
// The two together are the run's own selection, which is the one the offer has
// to repeat — a project this offer found on its own would be a second
// selection, and the whole point is that the run's is the only one.
type retryProject struct {
	dir  string
	name string
}

// updateRetryProject is that project, from the flags and the door's own
// positional arguments. dir is --repo's PATH, which init.go's runInitRepo
// defaults to the working directory: a --repo run's project is the checkout's,
// not the working directory's, so an offer that resolved the working directory
// would read another project's store (or none). name is --name, which
// loadInitConfig selects with and which loadRepoConfig validates against the
// checkout's own project before this offer can run — on a machine with several
// project configs, nothing else selects.
func updateRetryProject(o *initOptions, args []string) retryProject {
	p := retryProject{name: o.name}
	if !o.repo {
		return p
	}
	p.dir = "."
	if len(args) > 0 {
		p.dir = args[0]
	}
	return p
}

// updateRetryHint prints the offer for the issues a run just fixed, and marks
// them as offered in this machine's own file. args are the door's own
// positional arguments, which is how --repo's checkout is known.
//
// It runs after a converge that succeeded (update.go's PostRunE) and after
// nothing else, and it is built to be silent: no line when this machine has no
// issue waiting, when the shared store cannot be read, when nothing is
// resolved, or when what is resolved names no command of this build. The line
// goes to stderr with the run's other human lines, so --json's one object and
// --print-vars' document stay alone on stdout.
func updateRetryHint(cmd *cobra.Command, o *initOptions, args []string) {
	if !updateRetryOffers(o) {
		return
	}
	store, err := openIssueStore()
	if err != nil {
		return
	}
	local, err := store.List()
	if err != nil {
		return
	}
	// Nothing of this machine's is waiting for a release that fixed it: the
	// shared store is not read at all, so an ordinary update — the
	// overwhelmingly common one, a machine that filed nothing — opens no
	// bucket and makes no call.
	waiting := false
	for _, i := range local {
		if i.Number != 0 && !i.Notified {
			waiting = true
			break
		}
	}
	if !waiting {
		return
	}
	ctx := cmd.Context()
	b, err := updateRetryBucket(ctx, o.cloud, updateRetryProject(o, args))
	if err != nil {
		return
	}
	defer func() { _ = b.Close() }()
	rows, err := issues.ListShared(ctx, b)
	if err != nil {
		return
	}
	lines, mark := retryHintLines(local, rows, Version, cmd.Root())
	for _, line := range lines {
		fmt.Fprintln(cmd.ErrOrStderr(), line)
	}
	// An agent's session reads the same line and marks nothing: the mark is a
	// write, and a session's writes are the person's own — the rule every
	// issue command's upload and dismissal refuse on (F10) — so the offer is
	// printed for whoever reads the session's output and the bookkeeping waits
	// for the person's own run of the command.
	if len(mark) == 0 || agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())) != "" {
		return
	}
	for _, i := range mark {
		i.Notified = true
		// Best effort, like the rest of this file: a store that cannot be
		// written offers the line again on the next update and fails nothing.
		_ = store.Save(i)
	}
}

// updateRetryOffers reports whether this run is one that can offer anything.
// A run that applied nothing offers nothing: --plan-only, --print-vars,
// --config-only, --publish-config and --anchor stop before the converge and
// write a document (or one line of a checkout) rather than bringing the
// installation up to this release, and --forget takes the installation apart
// instead. The offer is about a run that has just installed the release that
// fixed the issue, which is why the list is the one the modes exclude one
// another by (init.go) and not a set of flags this file invents.
func updateRetryOffers(o *initOptions) bool {
	for _, oneShot := range []bool{o.planOnly, o.printVars, o.configOnly, o.forget, o.publishConfig, o.anchor} {
		if oneShot {
			return false
		}
	}
	// --non-interactive without --yes applies nothing either, by the loop's own
	// rule: "An apply needs --yes; without it the run is --plan-only"
	// (initflow/loop.go, which says so in its own result note). Such a run
	// installed no release, so no release fixed anything for it.
	if o.nonInteractive && !o.yes {
		return false
	}
	return true
}

// retryHintLines is the offer's whole decision over the two stores: the lines
// to print, and the records to mark as offered.
//
// local is this machine's own store, and that is what makes an issue this
// reporter's and not a teammate's (D1733): the shared store holds the whole
// team's. rows is the shared store, read for two fields — resolved_in, the
// release that fixed the issue (F27; the spec that writes it is another
// task's, so an empty one resolves nothing) — compared as a release against
// the version this binary IS (D1736): a dev build matches nothing, and so
// does a value that is not a release.
//
// The two stores are joined by number AND fingerprint (F8): a local issue
// file is global to the machine, so project A's #0007 must not supply project
// B's command, and a number is only unique inside one project's store. A
// local file with no fingerprint is one no shared record can be about —
// the shared store keys its records by fingerprint (F21) — so it is not
// matched to one either.
//
// The command is the one this machine recorded for its own issue and never
// the one a shared record carries (F28): the line is what a reader
// copy-pastes, the shared store is writable by every launcher, and the local
// file is the only place this machine's own command line lives.
func retryHintLines(local []issues.Issue, rows []issues.SharedRow, version string, root *cobra.Command) (lines []string, mark []issues.Issue) {
	type key struct {
		number      int
		fingerprint string
	}
	fixed := map[key]bool{}
	for _, row := range rows {
		if updatecheck.AtOrBelow(row.ResolvedIn, version) {
			fixed[key{row.Number, row.Fingerprint}] = true
		}
	}
	for _, i := range local {
		if i.Number == 0 || i.Notified || i.Fingerprint == "" {
			continue
		}
		if !fixed[key{i.Number, i.Fingerprint}] {
			continue
		}
		command, ok := retryCommand(i.FailedCommand, root)
		if !ok {
			continue
		}
		lines = append(lines, retryOfferPrefix+command)
		mark = append(mark, i)
	}
	return lines, mark
}

// retryCommand is the command an issue offers to run again, as one line, or
// ok false when this build has no such command. Three things are checked, and
// each is about the same thing: the offer is a line a person pastes into a
// shell.
//
// It has to begin with this build's own name. A command the person ran by
// path was recorded with that path redacted (internal/issues), so its first
// word is [REDACTED] and nothing can be offered: a name that is not the
// binary's is not something this build can vouch for.
//
// Every word has to be one a shell reads as a literal (retryWordsInert). The
// record is a file on this machine, and a coding agent running as the user can
// write it — internal/issues says as much of the store it reads — so the text
// is not the person's own until it is checked. `fugaro update --yes; curl … |
// sh` is a stored command a shell runs as two, and an offer that printed it
// would hand the reader the injection with `You can retry: ` in front of it,
// on a line whose whole purpose is to be pasted.
//
// Every word after the name walks down this build's own tree until one is not
// a subcommand — a flag, or an argument such as a task or a checkout path — and
// a word that a command WITH subcommands does not answer to refuses the whole
// line: `fugaro frobnicate` is not a command of this build, and neither is
// `fugaro issue frobnicate`, so an offer naming one would send the reader to a
// command that answers "unknown command" (D1614, F28).
//
// What comes back is the record's own text through safetext: it is a person's
// command line, and a terminal is no place to trust one.
func retryCommand(command string, root *cobra.Command) (string, bool) {
	words := strings.Fields(command)
	if len(words) == 0 || words[0] != root.Name() || !retryWordsInert(words) {
		return "", false
	}
	cur := root
	for _, word := range words[1:] {
		if strings.HasPrefix(word, "-") {
			break
		}
		next, _, err := cur.Find([]string{word})
		if err != nil || next == cur {
			if cur.HasSubCommands() {
				return "", false
			}
			break
		}
		cur = next
	}
	return safetext.OneLine(command), true
}

// retryRedacted is the word the record carries where a value it may not keep
// stood: internal/issues writes it (its redactedValue, unexported there), and
// a command run by path has it as its first word. It is the one word of an
// offer that is not a run of shell-literal bytes.
const retryRedacted = "[REDACTED]"

// retryWordsInert reports whether every word is one a shell reads as a literal
// word: shellword's own set of bytes, which is the set this program's printed
// commands are made of, so a line this build wrote passes it — plus the
// redaction marker, which stands in a value's place and is not literal.
//
// A word holding anything else is one a shell does something with — a
// separator, a substitution, a redirect, a glob, an expansion — and the whole
// line is refused rather than the word quoted: the offer is the record's own
// text (F28), so a text that is not the person's own is not offered at all.
func retryWordsInert(words []string) bool {
	for _, w := range words {
		if w == retryRedacted {
			continue
		}
		for i := 0; i < len(w); i++ {
			if !shellword.SafeByte(w[i]) {
				return false
			}
		}
	}
	return true
}
