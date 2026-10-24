package billing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/shellword"
)

// This file is the second of the package's two sources (accounts.go is the
// first): `gcloud billing accounts list`, run only when the Cloud Billing API
// could not answer and only when gcloud is on PATH, and only inside the
// wizard's billing question (D6's answer, W4.3). It exists because the two can
// disagree — gcloud asks with its own account, its own project and its own
// credentials — so the caller is told which of the two answered rather than
// being shown a list that may not be the API's.
//
// The command is run through an injected runner and never by this package's
// tests: the argv is the list command and nothing else, the answer is parsed
// here, and neither what gcloud printed nor the argv is written, logged or
// kept between calls — exactly the bounds the client in accounts.go works
// under, restated for a source whose output is text rather than JSON.

// GcloudBin is the binary both the fallback and the guided line name.
const GcloudBin = "gcloud"

// Runner runs gcloud with args and returns what it wrote to stdout. It is the
// fallback's whole seam: a test replaces it and no binary is run, and the real
// one is ExecRunner.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// ExecRunner runs gcloud with args as a child process. The answer is returned
// rather than printed, and stderr — gcloud's own words about a refusal — is
// folded into the error, which a caller prints as the reason the fallback did
// not answer.
func ExecRunner(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, GcloudBin, args...)
	cmd.Env = gcloudEnv(cmd.Environ())
	// A leaked descendant holding the output pipe must not hang us (see
	// internal/image's runner, which waits the same way).
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := firstLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s %s: %w: %s", GcloudBin, strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("%s %s: %w", GcloudBin, strings.Join(args, " "), err)
	}
	return out, nil
}

// gcloudEnv is the child's environment: the operator's own — exec.Cmd's own
// copy of it, so this file still names no package that could store what gcloud
// answers with (TestGcloudOutputNeverStored) — with gcloud's prompt to enable
// an API turned off, appended last so it wins over whatever the operator's own
// environment says. The fallback runs `billing accounts list` and nothing
// else, and the one thing this question must never do is offer to turn a
// project's APIs on: that is a change to a project this run was not asked
// about, and a prompt nobody answers hangs a question that was a read.
func gcloudEnv(environ []string) []string {
	out := append([]string{}, environ...)
	return append(out, "CLOUDSDK_CORE_SHOULD_PROMPT_TO_ENABLE_API=false")
}

// Gcloud is the fallback's two seams. The zero value is the real gcloud: the
// binary is looked up on PATH with exec.LookPath and run with ExecRunner, and
// a test replaces either.
type Gcloud struct {
	// LookPath reports gcloud's path on PATH; an error means it is not there,
	// and the fallback is then not tried at all. Nil is exec.LookPath.
	LookPath func(file string) (string, error)
	// Run runs the command. Nil is ExecRunner.
	Run Runner
}

// Options is one billing question: the API's identity, the project the guided
// line names, and the fallback's seams.
type Options struct {
	// Credentials is the identity the API call uses, and where it goes.
	Credentials Credentials
	// Project is the project the account would be linked to. It reaches
	// neither source: it is used for one thing only, the line *ErrNoSource
	// hands back when neither source answered.
	Project string
	// Gcloud is the fallback's seams; the zero value is the real gcloud.
	Gcloud Gcloud
}

// Source is which of the two sources answered the billing question. The two
// can disagree, so the caller says which one it is showing.
type Source string

const (
	// SourceNone is neither source answering: the API could not and the
	// fallback was not available either.
	SourceNone Source = ""
	// SourceAPI is the Cloud Billing API with the operator's own ADC — the
	// source the wizard prefers.
	SourceAPI Source = "api"
	// SourceGcloud is `gcloud billing accounts list`.
	SourceGcloud Source = "gcloud"
)

// String names the source the way a message says it, so a caller prints the
// name rather than re-deriving it.
func (s Source) String() string {
	switch s {
	case SourceAPI:
		return "the Cloud Billing API"
	case SourceGcloud:
		return "gcloud billing accounts list"
	}
	return ""
}

// ErrNoSource is neither source answering: the Cloud Billing API did not
// answer and gcloud could not be asked, or was asked and could not answer
// either. Guided is the line the wizard already prints for an account whose ID
// it does not know — internal/cli's guided() text, which needs no API and no
// gcloud — and Cause is the API's own failure, carried so a caller can still
// say why. Gcloud is why the fallback did not answer: nil when gcloud is not on
// PATH, otherwise what running it said.
type ErrNoSource struct {
	Guided string
	Cause  error
	Gcloud error
}

func (e *ErrNoSource) Error() string {
	why := "the Cloud Billing API did not answer"
	if e.Gcloud == nil {
		why += ", and gcloud is not on PATH"
	} else {
		why += ", and gcloud did not answer either (" + safetext.OneLine(firstLine(e.Gcloud.Error())) + ")"
	}
	return "no billing accounts could be listed: " + why + "; to link an account you already have: " + e.Guided
}

// Unwrap is the API's own failure, so a caller that has an answer for one of
// its kinds — a disabled API, a missing permission — still finds it.
func (e *ErrNoSource) Unwrap() error { return e.Cause }

// Guided is today's guided line with project in it, the line the wizard prints
// when it has no account to offer: internal/cli's guided(), which is the same
// words and the same quoting (W4.3 points its own at this one, or pins them
// equal). It is here because *ErrNoSource hands it back, and because it is the
// one thing the fallback can still say when it cannot list anything.
func Guided(project string) string {
	return GcloudBin + " billing projects link " + shellword.Quote(project) + " --billing-account=<billing-account-id>"
}

// gcloudListArgs is the one command the fallback runs: the list command, with
// no flag. The table it prints is what the operator sees in their own terminal
// too, and the parsing below is why no --format is needed.
var gcloudListArgs = []string{"billing", "accounts", "list"}

// ListWithFallback asks the Cloud Billing API first, and asks
// `gcloud billing accounts list` only when the API could not answer and gcloud
// is on PATH. It returns the open accounts and which source produced them, so
// the caller can say which one answered; a failure of both is *ErrNoSource,
// which carries the guided line to print instead.
//
// It is the wizard's one entry point into this package's two sources: nothing
// else calls it, and it lists nothing anywhere a person is not watching (D6).
func ListWithFallback(ctx context.Context, o Options) ([]Account, Source, error) {
	accounts, err := List(ctx, o.Credentials)
	if err == nil {
		return accounts, SourceAPI, nil
	}
	g := o.Gcloud
	lookPath := g.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, lerr := lookPath(GcloudBin); lerr != nil {
		return nil, SourceNone, &ErrNoSource{Guided: Guided(o.Project), Cause: err}
	}
	run := g.Run
	if run == nil {
		run = ExecRunner
	}
	out, rerr := run(ctx, gcloudListArgs...)
	if rerr != nil {
		return nil, SourceNone, &ErrNoSource{Guided: Guided(o.Project), Cause: err, Gcloud: rerr}
	}
	accounts, perr := parseAccounts(out)
	if perr != nil {
		return nil, SourceNone, &ErrNoSource{Guided: Guided(o.Project), Cause: err, Gcloud: perr}
	}
	return accounts, SourceGcloud, nil
}

// The columns of gcloud's table, spelled as its header spells them. The
// offsets are read out of the header rather than assumed: a display name holds
// spaces, and an account belonging to a reseller carries a master account id
// in the last column, so counting whitespace-separated tokens reads the wrong
// cells.
const (
	gcloudColAccountID = "ACCOUNT_ID"
	gcloudColName      = "NAME"
	gcloudColOpen      = "OPEN"
	gcloudColMaster    = "MASTER_ACCOUNT_ID"
)

// listedItemsPrefix is what gcloud prints in place of a table when the list is
// empty ("Listed 0 items."): no accounts is an answer, not a failure.
const listedItemsPrefix = "Listed "

// parseAccounts reads the table `gcloud billing accounts list` prints and
// returns the open accounts, in the order gcloud listed them — the same shape
// List returns, so the picker has one input type. An entry whose ID is not an
// account id is dropped, as List drops one: it ends up in a typed confirmation
// and in a printed gcloud line. An answer that is not the table at all is an
// error rather than an empty list, and the answer's text is not carried into
// it: it can hold an account's name.
func parseAccounts(out []byte) ([]Account, error) {
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	header := -1
	for i, line := range lines {
		if strings.HasPrefix(line, gcloudColAccountID) {
			header = i
			break
		}
	}
	if header < 0 {
		if emptyListing(lines) {
			return nil, nil
		}
		return nil, errors.New("gcloud's answer was not the table of billing accounts")
	}
	line := lines[header]
	idAt := strings.Index(line, gcloudColAccountID)
	nameAt := after(line, idAt, gcloudColAccountID, gcloudColName)
	openAt := after(line, nameAt, gcloudColName, gcloudColOpen)
	masterAt := after(line, openAt, gcloudColOpen, gcloudColMaster)
	if nameAt < 0 || openAt < 0 {
		return nil, errors.New("gcloud's answer was not the table of billing accounts")
	}
	var accounts []Account
	for _, row := range lines[header+1:] {
		id := strings.TrimSpace(cell(row, idAt, nameAt))
		if !billingAccountIDRE.MatchString(id) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(cell(row, openAt, masterAt)), "True") {
			continue
		}
		accounts = append(accounts, Account{
			ID:          id,
			DisplayName: safetext.OneLine(strings.TrimSpace(cell(row, nameAt, openAt))),
			Open:        true,
		})
	}
	return accounts, nil
}

// after is the offset of col in line, searched from the end of the column
// before it, so that a column name that is a suffix of an earlier one
// (ACCOUNT_ID inside MASTER_ACCOUNT_ID) is not found in the wrong place. -1
// when the header does not name it.
func after(line string, prevEnd int, prev, col string) int {
	if prevEnd < 0 {
		return -1
	}
	at := strings.Index(line[prevEnd+len(prev):], col)
	if at < 0 {
		return -1
	}
	return prevEnd + len(prev) + at
}

// cell is one column of a row: what sits between two header columns, "" when
// the row is too short to reach it. masterAt is -1 when the header names no
// column after OPEN, and then the cell runs to the end of the row.
func cell(row string, at, next int) string {
	if at < 0 || at >= len(row) {
		return ""
	}
	if next < 0 || next > len(row) {
		return row[at:]
	}
	return row[at:next]
}

// emptyListing is whether an answer with no table is gcloud's empty list:
// blank lines, and its own "Listed N items." trailer. Anything else — a
// refusal, a prompt, a warning — is not, and is refused by the caller.
func emptyListing(lines []string) bool {
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, listedItemsPrefix) {
			return false
		}
	}
	return true
}

// firstLine is s's first non-blank line, trimmed: gcloud's stderr can be prose
// over several lines, and one of them is enough to say why it did not answer.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
