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
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// The offer's line is resolvedFixedLine's (update_resolved.go), built there
// and not here because the resolved-issues step says the same thing about the
// issues it fixes: the spec's sentence for an issue a release fixed and the
// command it failed on, which is one sentence about one issue and may not
// drift into two.

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
// the origin a config is matched by. Which bucket the selection reads follows
// from the run as well (retryBucketURL).
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
	if lc == nil {
		return nil, errors.New("no project with a runs bucket is selected")
	}
	url, err := retryBucketURL(lc, p.runsBucket)
	if err != nil {
		return nil, err
	}
	return blobx.Open(ctx, url)
}

// retryBucketURL is the bucket the offer reads, from the config the run
// selected and the --runs-bucket the run was given: loadInitConfig's own two
// lines, applied to the selection rather than to a config about to be
// converged. The run converged against this bucket, so the offer has to read
// the same one — a machine that passed --runs-bucket here and whose config
// names no bucket_url of its own would otherwise read the bucket the config
// would have answered with and miss its own issue (or, if that bucket holds a
// row of the same number and fingerprint, offer an issue this release did not
// fix).
//
// The refusal is the converge's, spelled the same way: --runs-bucket and a
// bucket_url that is not it are two answers to one question. Every error is an
// offer not printed, never a failed update.
//
// It is a function of its own because the answer is what a test can check
// without a bucket: the seam it is called from opens a gs:// URL, which no
// hermetic test can reach.
func retryBucketURL(lc *localcfg.Config, runsBucket string) (string, error) {
	if runsBucket != "" {
		lc.RunsBucket = runsBucket
		if lc.Bucket != "" && lc.Bucket != "gs://"+runsBucket {
			return "", userErr("--runs-bucket %s is not the local config's bucket_url %s", runsBucket, lc.Bucket)
		}
	}
	if lc.Bucket == "" && lc.RunsBucket == "" {
		return "", errors.New("no project with a runs bucket is selected")
	}
	return lc.BucketURL(), nil
}

// retryProject is the project the run this offer follows converged, as the
// door still knows it after the fact: dir is the checkout --repo converged
// ("" is the working directory), name is the --name the run selected with, and
// runsBucket is the --runs-bucket the installation door applies to the config
// it selects. The three together are the run's own selection, which is the one
// the offer has to repeat — a project this offer found on its own would be a
// second selection, and the whole point is that the run's is the only one.
type retryProject struct {
	dir        string
	name       string
	runsBucket string
}

// updateRetryProject is that project, from the flags and the door's own
// positional arguments. dir is --repo's PATH, which init.go's runInitRepo
// defaults to the working directory: a --repo run's project is the checkout's,
// not the working directory's, so an offer that resolved the working directory
// would read another project's store (or none). name is --name, which
// loadInitConfig selects with and which loadRepoConfig validates against the
// checkout's own project before this offer can run — on a machine with several
// project configs, nothing else selects. runsBucket is --runs-bucket, and the
// installation door's alone: it converges the config loadInitConfig selected
// against that bucket, so the offer reads it, and a --repo run never applied it
// (loadRepoConfig does not).
func updateRetryProject(o *initOptions, args []string) retryProject {
	if !o.repo {
		// The installation door's selection, --runs-bucket included:
		// loadInitConfig applies it to the config it selected.
		return retryProject{name: o.name, runsBucket: o.runsBucket}
	}
	// --repo converges the checkout's repository through loadRepoConfig, which
	// selects with the checkout's own config and does not apply --runs-bucket
	// (the flag is the installation door's), so the offer repeats that too and
	// reads the bucket the run actually converged against.
	p := retryProject{name: o.name}
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
		// The offer is this machine's own command, so it is the wording that
		// suggests trying it again (O4): the offer exists for the person who
		// ran it.
		lines = append(lines, resolvedFixedLine(i.Number, command, true))
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
// Every word has to be one a shell reads as a literal (retryWordsInert), over
// the record's own bytes rather than over a tokenizer's reading of them
// (retryWords): the bytes that are checked have to be the bytes the printer
// writes. The record is a file on this machine, and a coding agent running as
// the user can write it — internal/issues says as much of the store it reads —
// so the text is not the person's own until it is checked. `fugaro update
// --yes; curl … | sh` is a stored command a shell runs as two, and an offer
// that printed it would hand the reader the injection with `You can retry: `
// in front of it, on a line whose whole purpose is to be pasted.
//
// Every word after the name is read by this build's own tree (root.Find),
// which is what makes the check agree with the line: cobra strips the flags the
// way the parser that will run the pasted line strips them, so a flag before a
// subcommand — `fugaro -v issue show …` — cannot stop the check at the flag.
// Two things refuse the line (D1614, F28), and both are the same defect: the
// reader would be sent to a command that answers "unknown command". The walk
// itself returns one for a word the root cannot place (`fugaro frobnicate`);
// the other is a word left over at a command with subcommands
// (`fugaro issue frobnicate`, and `fugaro -v issue frobnicate` once the flag is
// read as the flag it is), which cobra does not report at all.
//
// What comes back is the record's own text through safetext, which is that
// same text unchanged once every byte of it has passed the check: it is a
// person's command line, and a terminal is no place to trust one.
func retryCommand(command string, root *cobra.Command) (string, bool) {
	words := retryWords(command)
	if len(words) == 0 || words[0] != root.Name() || !retryWordsInert(words) {
		return "", false
	}
	found, rest, err := root.Find(words[1:])
	if err != nil {
		return "", false
	}
	if found.HasSubCommands() && retryUnknownWord(rest, found) {
		return "", false
	}
	return safetext.OneLine(command), true
}

// retryUnknownWord reports whether the words the tree walk could not place
// still hold one cobra would read as an unknown subcommand: a word that is
// neither a flag nor a flag's own value. It is the half of the check Find
// cannot make itself, because cobra only answers "unknown command" for the
// root (legacyArgs): `fugaro issue frobnicate` reaches `issue` with
// `frobnicate` unplaced and no error at all.
//
// A flag's value is what the flag's own declaration says it is, which is how
// cobra reads the line too: `--repo [REDACTED]` is one flag and its argument,
// while a boolean (`-v`) and a `--flag=value` pair take no word after them.
func retryUnknownWord(words []string, cmd *cobra.Command) bool {
	flags := cmd.Flags()
	for len(words) > 0 {
		word := words[0]
		words = words[1:]
		if !strings.HasPrefix(word, "-") {
			return true
		}
		var takesValue bool
		switch {
		case strings.Contains(word, "="):
		case strings.HasPrefix(word, "--"):
			f := flags.Lookup(word[2:])
			takesValue = f != nil && f.NoOptDefVal == ""
		case len(word) == 2:
			f := flags.ShorthandLookup(word[1:])
			takesValue = f != nil && f.NoOptDefVal == ""
		}
		if takesValue && len(words) > 0 {
			words = words[1:]
		}
	}
	return false
}

// retryRedacted is the word the record carries where a value it may not keep
// stood: internal/issues writes it (its redactedValue, unexported there), and
// a command run by path has it as its first word. It is the one word of an
// offer that is not a run of shell-literal bytes.
const retryRedacted = "[REDACTED]"

// retryWords is the record's words, split on the space and the tab alone —
// which is what this program's own printed commands separate on, and the two
// separators safetext leaves as they are.
//
// It is deliberately not strings.Fields, and that is the whole of it: Fields
// splits on every Unicode space, so a vertical tab, a next line (U+0085) or a
// line separator (U+2028) is a separator to the check and not to the shell —
// `fugaro\vupdate` would pass as the words `fugaro` and `update`, and
// safetext's OneLine prints it as the glob `fugaro?update`, a command position
// on the one line a person pastes. The bytes checked here are the bytes
// printed, and every byte that is neither a word byte nor one of these two
// separators is refuted by retryWordsInert.
func retryWords(command string) []string {
	return strings.FieldsFunc(command, func(r rune) bool { return r == ' ' || r == '\t' })
}

// retryWordsInert reports whether every word is one a shell reads as a literal
// word: shellword's own set of bytes, which is the set this program's printed
// commands are made of, so a line this build wrote passes it — plus the
// redaction marker, which stands in a value's place and is not literal. The
// exemption is the whole word: a marker run together with anything else is a
// word of markers and brackets no writer here produces.
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
