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
