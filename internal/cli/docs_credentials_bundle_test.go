package cli

import (
	"os"
	"strings"
	"testing"
)

// TestDocsCredentialsServiceKeepsOneBundleSecret pins the owner's ruling of
// 2026-10-19 on Q6 -- the open item of the first independent review of the
// credentials plan -- and the plan tasks it amends.
//
// The ruling: instead of a Secret Manager grant per credential (or a
// name-prefix IAM fence over the model secrets), every credential the service
// needs lives in ONE Secret Manager secret, a YAML document with a strict
// schema the service reads in one go, and the service holds exactly ONE grant
// (secretmanager.secretAccessor on that one secret). Reason: simpler and
// cheaper.
//
// What this test holds, in the three documents:
//
//   - reconciliation.md records Q6 as ruled, with the reason and the
//     consequences (the audit log shows access to one secret and not which
//     credential was used; a leak of the bundle is every service credential at
//     once -- the same set the service already holds; a teammate who may add a
//     token but not read one loses that path; the per-account job mounts of
//     0.9.0's named tokens on shared jobs, and SECURITY.md's wording, are
//     unchanged).
//   - implementation-plan.md's C1.18 creates one secret container and one
//     accessor grant for the service's service account, with NO per-secret
//     grant over the model secrets and no name-prefix filter, and its C1.11
//     reads the bundle (latest version, short cache) with a strict schema.
//   - implementation-plan.md carries the new owner-run task for
//     `fugaro creds set NAME` and `fugaro creds ls`, and the plan no longer
//     describes the service's access as reaching the model secrets one by one.
//
// The RED STEP for this test was run for real: written before the documents
// were amended, it failed on the ruling text, on the new task's number and on
// the old per-secret wording; it passes once the three documents carry the
// ruling. It is deliberately about wording and not about behaviour: nothing is
// implemented by the documents it reads.
func TestDocsCredentialsServiceKeepsOneBundleSecret(t *testing.T) {
	const (
		recon  = "../../docs/credentials-service/reconciliation.md"
		plan   = "../../docs/credentials-service/implementation-plan.md"
		readme = "../../docs/credentials-service/README.md"
	)
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	reconciliation, implementationPlan, index := read(recon), read(plan), read(readme)

	// The ruling, in the reconciliation, as the answer to Q6 of the first
	// independent review, dated, with its reason.
	for _, want := range []string{
		"Q6",
		"2026-10-19",
		"one Secret Manager secret",
		"simpler and cheaper",
	} {
		if !strings.Contains(reconciliation, want) {
			t.Errorf("%s does not record the Q6 ruling: it no longer says %q, so the answer to the review's open item is not written down", recon, want)
		}
	}

	// Its consequences, stated plainly rather than left to the reader.
	for _, want := range []string{
		"the audit log shows access to one secret",
		"leaks every service credential at once",
		"shared jobs are unchanged",
		"SECURITY.md's wording is unchanged",
	} {
		if !strings.Contains(reconciliation, want) {
			t.Errorf("%s no longer states the Q6 ruling's consequence %q: a ruling whose cost is not named is a ruling nobody can weigh", recon, want)
		}
	}

	// The Terraform: one container, one grant, and neither a per-secret grant
	// over the model secrets nor a name-prefix fence.
	for _, want := range []string{
		"one secret container",
		"one accessor grant",
		"NO per-secret grants",
		"no filter over model secrets",
	} {
		if !strings.Contains(implementationPlan, want) {
			t.Errorf("%s's C1.18 no longer says %q: the plan has to name the one bundle secret and the one grant the owner ruled for", plan, want)
		}
	}

	// The loader: the bundle, read once, strictly, and never echoed.
	for _, want := range []string{
		"the bundle (latest version, short cache)",
		"strict schema",
		"refuses unknown keys",
		"oversize document",
		"never logs or echoes any value",
		"fails closed on a missing or unparseable bundle",
	} {
		if !strings.Contains(implementationPlan, want) {
			t.Errorf("%s's C1.11 no longer says %q: the loader's contract is what makes one bundle secret as safe as the grants it replaces", plan, want)
		}
	}

	// The new owner-run task, placed after C1.18 and before the service's first
	// deploy, with the command surface the owner ruled for.
	for _, want := range []string{
		"### Task C1.25:",
		"`fugaro creds set NAME`",
		"`fugaro creds ls`",
		"owner-run only",
		"hidden prompt",
		"read-modify-write",
		"re-read after the write",
		"names and metadata only",
	} {
		if !strings.Contains(implementationPlan, want) {
			t.Errorf("%s no longer carries %q: the bundle's write path is an owner-run command the plan has to specify", plan, want)
		}
	}

	// The old model, gone: the plan no longer says the service's own access
	// reaches the model secrets one at a time. (Today's per-secret grants to
	// the JOB service accounts are a different statement and stay: the
	// reconciliation's F1/F2 record them as the model this work moves.)
	for _, forbidden := range []string{
		"`secretAccessor` on model secrets only",
		"grant per secret to the service",
	} {
		if strings.Contains(implementationPlan, forbidden) {
			t.Errorf("%s still says %q, which is the per-secret model the owner replaced on 2026-10-19: the service's access is one grant on the one bundle secret", plan, forbidden)
		}
	}

	// The index's status line is where a reader learns the plan has moved on;
	// it lists the settled items and so has to carry this one.
	for _, want := range []string{"Q6", "2026-10-19"} {
		if !strings.Contains(index, want) {
			t.Errorf("%s's status line no longer carries %q: the index lists the settled items, and Q6 is one", readme, want)
		}
	}
}

// TestDocsCredsTaskKeepsTheBundleInMemory pins the first of the two rules the
// owner asked for on 2026-10-19, in the brief for the bundle's command task
// (C1.25) that the Q6 ruling created.
//
// The rule: the bundle exists only in memory while the command runs. Nothing is
// written to disk -- no temporary file, no editor buffer, no swap file, no
// shell history of the value -- no value reaches a log line, a crash report or
// an error message, and the command zeroes its buffers best-effort before it
// exits.
//
// Why it is a rule and not a habit: the bundle is every service credential in
// one object (Q6), and the accepted cost of the ruling is that a read of it is
// every credential at once. A copy on the disk, in a log or in an error message
// is that same leak with none of the service's own protections around it, so
// the command that writes the bundle is the one place the value must not
// survive.
//
// The plan's test list has to carry the test that holds it: one that runs the
// command with HOME and TMPDIR pointed at empty directories and asserts they
// stay empty, so the only thing the command writes is the Secret Manager
// version.
//
// RED STEP, run for real: written before the documents were amended, this test
// failed, naming every phrase below. It is deliberately about wording and not
// about behaviour: nothing is implemented by the documents it reads.
func TestDocsCredsTaskKeepsTheBundleInMemory(t *testing.T) {
	const (
		recon = "../../docs/credentials-service/reconciliation.md"
		plan  = "../../docs/credentials-service/implementation-plan.md"
	)
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	reconciliation, implementationPlan := read(recon), read(plan)

	// The rule itself, at the top of the task the command is specified in.
	for _, want := range []string{
		"**No temporary file, ever.**",
		"nothing is written to disk",
		"no temporary file, no editor buffer, no swap",
		"no value reaches a log line, a crash report or an error message",
		"zeroes its buffers best-effort before it exits",
	} {
		if !strings.Contains(implementationPlan, want) {
			t.Errorf("%s's C1.25 no longer says %q: the bundle's command has to keep the value off the disk, out of every log and out of every error", plan, want)
		}
	}

	// The test that holds it, in the task's own test list.
	for _, want := range []string{
		"TestCredsSetWritesNoFile",
		"`HOME` and `TMPDIR`",
	} {
		if !strings.Contains(implementationPlan, want) {
			t.Errorf("%s's C1.25 no longer carries %q: a rule with no test that would catch a temp file left behind is a rule nothing enforces", plan, want)
		}
	}

	// The record: an owner's rule that lives only in the plan reads as the
	// plan's own choice, so the reconciliation states it too.
	if !strings.Contains(reconciliation, "No temporary file") {
		t.Errorf("%s no longer records the owner's no-temp-file rule of 2026-10-19: the plan implements the owner's brief, and this rule is one line of it", recon)
	}
}

// TestDocsCredsTaskShowsOnlyThePromptAndOneLine pins the second of the two
// rules the owner asked for on 2026-10-19, in the brief for the bundle's
// command task (C1.25).
//
// The rule: the whole flow -- prompt for the value with no echo, read the
// current bundle, merge in memory, validate, write the new version, re-read to
// confirm -- is one command, and the owner sees nothing between its prompt and
// its one final line: no progress line, no listing of what it read, no diff, no
// intermediate state of any kind. The line is `added NAME (bundle version N)`
// when the name is new, `replaced NAME (bundle version N)` when the entry
// already existed, and `removed NAME (bundle version N)` for `fugaro creds rm
// NAME`, which goes through the same flow.
//
// Why: an owner who is shown the flow's intermediate state is shown the bundle
// (Q6 keeps every service credential in it), and a line per step is a line a
// value can hide in. The plan's test list has to carry the test of the exact
// output lines.
//
// RED STEP, run for real: written before the documents were amended, this test
// failed, naming every phrase below.
func TestDocsCredsTaskShowsOnlyThePromptAndOneLine(t *testing.T) {
	const (
		recon = "../../docs/credentials-service/reconciliation.md"
		plan  = "../../docs/credentials-service/implementation-plan.md"
	)
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	reconciliation, implementationPlan := read(recon), read(plan)

	// The rule itself, and the three lines it fixes word for word.
	for _, want := range []string{
		"**The user sees only the prompt and one final line.**",
		"no intermediate state",
		"`fugaro creds rm NAME`",
		"added NAME (bundle version N)",
		"replaced NAME (bundle version N)",
		"removed NAME (bundle version N)",
	} {
		if !strings.Contains(implementationPlan, want) {
			t.Errorf("%s's C1.25 no longer says %q: the command's whole output is the prompt and one line, and the line is fixed word for word", plan, want)
		}
	}

	// The test that holds it, in the task's own test list.
	if !strings.Contains(implementationPlan, "TestCredsCommandsPrintOnlyTheFinalLine") {
		t.Errorf("%s's C1.25 no longer carries TestCredsCommandsPrintOnlyTheFinalLine: an output rule with no test of the exact lines is a rule a progress line can quietly break", plan)
	}

	// The record, as in the file's first rule above.
	if !strings.Contains(reconciliation, "one final line") {
		t.Errorf("%s no longer records the owner's prompt-and-one-line rule of 2026-10-19: the plan implements the owner's brief, and this rule is one line of it", recon)
	}
}
