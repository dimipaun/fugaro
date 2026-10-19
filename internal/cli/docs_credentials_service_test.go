package cli

import (
	"regexp"
	"strings"
	"testing"
)

// The credentials service's Slice 0 writes two live checks into
// docs/gcp-live-checklist.md (Checks 90 and 91), and both are instructions a
// human types against a real subscription token and a real cloud project. The
// independent review of 2026-10-19 (PR #584) found six defects in them, plus two
// wording defects in the C0.2 notes and one in the number register. The tests
// below pin each fix to the sentence or the command that carries it, so an edit
// that drops one fails here rather than passing on the section's heading: a
// guard that leaves an API key in the shell makes the control prove nothing, a
// recursive delete with no shape check removes a path the reader set, a service
// deployed without the registry repository its image tag names fails at the
// build, a token minted once is expired by the probe that runs an hour later,
// prose that promises a body the commands never fetch measures nothing, and an
// HTTP/2 row that is not end to end settles U5 wrongly.

const (
	checklistPath = "docs/gcp-live-checklist.md"
	chk90Heading  = "## Check 90: the OAuth header swap"
	chk91Heading  = "## Check 91: Cloud Run streaming"
	chk47Heading  = "## Check 47: the subscription OAuth token"
)

// docTail returns doc from marker to the end, failing the test if marker is
// absent. It is docSection's sibling for the steps inside a check, which are
// "###" headings and so do not terminate a docSection.
func docTail(t *testing.T, path, doc, marker string) string {
	t.Helper()
	i := strings.Index(doc, marker)
	if i == -1 {
		t.Fatalf("%s has no %q", path, marker)
	}
	return doc[i:]
}

// TestChecklistCheck90GuardClearsTheClientCredentialEnvironment: Claude Code
// reads ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN before anything the check
// sets, and ANTHROPIC_BASE_URL would decide where the call goes, so a control
// run in a shell carrying any of them is not a measurement of the setup-token —
// it can succeed on an API key and be recorded as a swap that works. The guard
// unsets all three and says so without printing a value.
func TestChecklistCheck90GuardClearsTheClientCredentialEnvironment(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk90Heading)
	guard := docSection(t, checklistPath, check, "### Check 90 step 1: setup and guard")
	for _, want := range []string{
		"for v in ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL; do",
		`unset "$v"`,
		"its value is not printed",
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 90's guard does not clear the client's own credential environment: it lacks %q", want)
		}
	}
	if !strings.Contains(check, "can only succeed on the setup-token") {
		t.Error("Check 90's section does not say why the client's credential variables are cleared: that the control can only succeed on the setup-token")
	}
}

// TestChecklistCheck90ScratchDirectoryIsTheChecksOwn: step 9 removes the
// scratch directory recursively, so the path may not be one the reader set: it
// is the one this check makes itself (mktemp -d, mode 0700, carrying a marker
// the check writes), remembered across steps, and the Restore re-checks the
// shape and the marker immediately before the delete.
func TestChecklistCheck90ScratchDirectoryIsTheChecksOwn(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk90Heading)
	guard := docSection(t, checklistPath, check, "### Check 90 step 1: setup and guard")
	for _, want := range []string{
		`SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk90.XXXXXXXX")`,
		`: > "$SCRATCH/.chk90-mine"`,
		`SCRATCH_RECORD`,
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 90's guard does not make its own scratch directory: it lacks %q", want)
		}
	}
	// A reader-set SCRATCH is what the review found: the value went into an
	// `rm -rf` with nothing checking it.
	if strings.Contains(check, `SCRATCH=${SCRATCH:-$HOME/chk90-scratch}`) {
		t.Error("Check 90 still takes the scratch directory from a value the reader sets, which is what step 9's rm -rf removes")
	}
	restore := docSection(t, checklistPath, check, "### Check 90 step 9: Restore")
	// The order is the point: both refusals have to stand between the path and
	// the delete, not after it.
	shape := strings.Index(restore, `|/|"$HOME"`)
	marker := strings.Index(restore, `[ -f "$SCRATCH/.chk90-mine" ]`)
	rmrf := strings.Index(restore, `rm -rf "$SCRATCH"`)
	if shape == -1 || marker == -1 || rmrf == -1 {
		t.Fatalf("Check 90's Restore lacks its scratch-directory guard (shape=%d, marker=%d, rm -rf=%d)", shape, marker, rmrf)
	}
	if !(shape < rmrf && marker < rmrf) {
		t.Error("Check 90's Restore runs `rm -rf \"$SCRATCH\"` before checking the path's shape and its marker")
	}
}

// TestChecklistCheck91CreatesItsOwnRegistryRepository: the image tag names an
// Artifact Registry repository in the project, and nothing created it, so the
// build failed at its first step. The check creates the repository its own name
// points at when the baseline said it was not already there — never a
// repository the installation had — and the Restore deletes it in the same
// case.
func TestChecklistCheck91CreatesItsOwnRegistryRepository(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	build := docSection(t, checklistPath, check, "### Check 91 step 3: build and deploy the throwaway service")
	create := strings.Index(build, `gcloud artifacts repositories create "${SVC:?}"`)
	submit := strings.Index(build, `gcloud builds submit`)
	if create == -1 {
		t.Fatal("Check 91 does not create the Artifact Registry repository its image tag names, so the build has nowhere to push")
	}
	if submit == -1 || create > submit {
		t.Error("Check 91's repository is created after the build that needs it")
	}
	if !strings.Contains(build, `--repository-format=docker`) {
		t.Error("Check 91's repository is not created as a Docker repository")
	}
	if !strings.Contains(build, `"$(cat "$CHK91/repo-existed")" = no`) {
		t.Error("Check 91 creates its registry repository without consulting the baseline that decides whether it was already there (and so whether the Restore may delete it)")
	}
	// The image tag is the repository's own name, which is what keeps the
	// creation, the push and the Restore's delete on one repository.
	if !strings.Contains(check, `IMG=${L}-docker.pkg.dev/${P}/${SVC}/${SVC}:chk91`) {
		t.Error("Check 91's image tag does not name the registry repository the check creates")
	}
	restore := docSection(t, checklistPath, check, "### Check 91 step 7: Restore")
	if !strings.Contains(restore, `gcloud artifacts repositories delete "${SVC:?}"`) {
		t.Error("Check 91's Restore does not delete the registry repository it creates")
	}
}

// TestChecklistCheck91MintsAnIdentityTokenPerProbe: `gcloud auth
// print-identity-token` mints a token valid for an hour, and the check waits
// longer than that before it is done (a 59-minute stream, then a probe per body
// size), so one token minted in step 3 is expired by step 6 — and every 401 it
// causes would be recorded as the size limit the step is measuring. Each
// request mints its own.
func TestChecklistCheck91MintsAnIdentityTokenPerProbe(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	if !strings.Contains(check, "idt()") {
		t.Error("Check 91 has no helper that mints an identity token, so a token minted once is reused")
	}
	probes := docTail(t, checklistPath, check, "### Check 91 step 4: cold start")
	if strings.Contains(probes, "${IDT") {
		t.Error("Check 91's probes reuse one identity token from step 3, which expires before the last of them")
	}
	// Every request from step 4 on carries a fresh token, so the two counts are
	// the same number: a site that goes back to one minted token changes one of
	// them and not the other.
	auths := strings.Count(probes, "Authorization: Bearer")
	mints := strings.Count(probes, "$(idt)")
	if mints < auths {
		t.Errorf("Check 91 makes %d requests from step 4 on but mints only %d identity tokens; the token from step 3 expires inside the long stream", auths, mints)
	}
	if !strings.Contains(check, "expires") || !strings.Contains(check, "401") {
		t.Error("Check 91 does not say why a fresh token is minted per probe: an expired one answers 401, which reads as a size limit")
	}
}

// TestChecklistCheck91BodyProbeReadsTheRelayBody: step 6's FACT says the
// relay's own `read N bytes` body is what proves the whole body arrived, and
// its commands fetched that body with `-o -` — while every command in it wrote
// `-o /dev/null`, so the byte count the step is built on was never read. The
// commands now keep the body and print it beside the status line.
func TestChecklistCheck91BodyProbeReadsTheRelayBody(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	probe := docSection(t, checklistPath, check, "### Check 91 step 6: the request-body limit")
	if strings.Contains(probe, "-o /dev/null") {
		t.Error("Check 91's body probe still discards the response body, so the relay's own byte count is never read")
	}
	for _, want := range []string{`-o "$CHK91/echo.out"`, `cat "$CHK91/echo.out"`} {
		if !strings.Contains(probe, want) {
			t.Errorf("Check 91's body probe does not keep and print the relay's own byte count: it lacks %q", want)
		}
	}
	if strings.Contains(probe, "`-o -`") {
		t.Error("Check 91's body probe still says the body is fetched with `-o -`, which its commands do not do")
	}
}

// TestChecklistCheck91DoesNotClaimEndToEndHTTP2: Cloud Run speaks HTTP/2 to a
// container only when the service is deployed with --use-http2 and the
// container serves cleartext HTTP/2 (h2c). This check deploys neither — the
// relay is Python's http.server — so its second row measures the front end's
// HTTP/2 ingress translating to HTTP/1.1, not an end-to-end HTTP/2 request, and
// U5's end-to-end half stays UNVERIFIED. Saying so is what keeps the number the
// service's forwarding is written against from being settled wrongly.
func TestChecklistCheck91DoesNotClaimEndToEndHTTP2(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	probe := docSection(t, checklistPath, check, "### Check 91 step 6: the request-body limit")
	for _, want := range []string{"not end to end", "h2c", "--use-http2", "UNVERIFIED"} {
		if !strings.Contains(probe, want) {
			t.Errorf("Check 91's body probe does not say what its second row is and is not: it lacks %q", want)
		}
	}
	if !strings.Contains(check, "U5's end-to-end HTTP/2 half stays UNVERIFIED") {
		t.Error("Check 91 does not say that its HTTP/2 half of U5 stays UNVERIFIED, so a reader takes the row as end to end")
	}
	// The register's row for 91 is what a reader sees first, so its claim has
	// to match the section's.
	row := docTail(t, checklistPath, doc, "| 91 | Cloud Run streaming through a throwaway service")
	if i := strings.Index(row, "\n"); i != -1 {
		row = row[:i]
	}
	if strings.Contains(row, "settles U5)") {
		t.Error("the register's row for Check 91 claims it settles U5 whole, which its HTTP/2 half does not")
	}
	if !strings.Contains(row, "U5's end-to-end HTTP/2 half stays UNVERIFIED") {
		t.Error("the register's row for Check 91 does not say that U5's end-to-end HTTP/2 half stays UNVERIFIED")
	}
}

// docFlat collapses a document's line breaks to single spaces, so an assertion
// can match a sentence the markdown hard-wraps. The checklist's paragraphs are
// wrapped by hand; its code blocks are not.
func docFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestChecklistCredentialsBlockIsCarvedOutOfTheWebsiteRange: the register says
// the website work holds 69 and up, and the credentials service's paragraph
// said 90 is "the first number free above every claim recorded here" — the two
// sentences cannot both be true. The block is carved out of that range by the
// owner's allocation, the way mobile's 80-89 is, and above 99 the register's
// standing rule (a number is chosen at the moment of merge) applies.
func TestChecklistCredentialsBlockIsCarvedOutOfTheWebsiteRange(t *testing.T) {
	doc := readDoc(t, checklistPath)
	para := docSection(t, checklistPath, doc, "**Check 90 to 99 are the credentials service's**")
	if i := strings.Index(para, "\n**Check 70 is"); i != -1 {
		para = para[:i]
	}
	flat := docFlat(para)
	for _, want := range []string{
		"carved out of the 69-and-up range",
		"80–89 carved out of the same range",
		"a number is chosen at the moment of merge",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the credentials service's block does not say how 90-99 is allocated: it lacks %q", want)
		}
	}
	if strings.Contains(flat, "90 is the first number free above every claim") {
		t.Error("the register still calls 90 the first number free above every claim, which contradicts the website work holding 69 and up")
	}
}

// TestProjectCentricSection7RecordsTheLifting: §7's C0.2 note said Task 3.4a's
// refusal of an OAuth launch onto a shared job "stays until then" — until the
// phase-3 shared-job rules land (C3.9). The owner ruled otherwise on
// 2026-10-18: the refusal is lifted in 0.9.0 and a shared job's token reaches it
// by the static mount, a stopgap. And the credentials service cannot replace
// that stopgap as its plan is written: it serves per-repository runs only.
func TestProjectCentricSection7RecordsTheLifting(t *testing.T) {
	const path = "docs/design/project-centric.md"
	doc := readDoc(t, path)
	sec := docSection(t, path, doc, "## 7. OAuth tokens (phase 5)")
	for _, want := range []string{
		"the owner's ruling of 2026-10-18",
		"is lifted",
		"project-named-oauth-tokens.md",
		"static mount",
		"per-repository runs only",
		"Global Constraint 6",
		"C1.10",
		"unscheduled",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("project-centric.md §7 does not state what is true of the shared-job refusal: it lacks %q", want)
		}
	}
	if strings.Contains(sec, "Task 3.4a, PR #511) stays until then") {
		t.Error("project-centric.md §7 still says Task 3.4a's refusal stays until C3.9, which the owner's ruling of 2026-10-18 supersedes")
	}
}

// TestM9Section11AttributesTheSidecarDropToF28: the C0.2 edit dropped M9 §11's
// gateway sidecar, and a dropped item is a finding's consequence, not a
// decision's: it is F28 (the spec rejects the sidecar for the same service
// account and metadata server), and D130 is the supersession of the
// OAuth-reviewer-stage plan. The same paragraph called the credentials service
// the only writer of `creds/`, which the operator's own clearing of a mark
// (C1.21, an admin write) contradicts.
func TestM9Section11AttributesTheSidecarDropToF28(t *testing.T) {
	const path = "docs/design/m9-budget-and-dashboard.md"
	doc := readDoc(t, path)
	sec := docSection(t, path, doc, "## 11. Threat model")
	if !strings.Contains(sec, "finding F28") {
		t.Error("M9 §11's dropped sidecar is not attributed to the finding (F28) that rejects it")
	}
	if strings.Contains(sec, "Dropped (D130") {
		t.Error("M9 §11 attributes the sidecar's drop to D130; D130 supersedes the OAuth-reviewer-stage plan, and it is F28 that drops the sidecar")
	}
	if !strings.Contains(sec, "only writer of the cascade's shared state") || !strings.Contains(sec, "at run time") {
		t.Error("M9 §11 still calls the credentials service the only writer of creds/, with nothing said of the operator's own write (C1.21)")
	}
	if !strings.Contains(sec, "C1.21") {
		t.Error("M9 §11 does not name the operator's clear of a mark (C1.21), which is the write beside the service's")
	}
}

// TestCredentialsPlanRecordsTheOpenDesignFindings: the 2026-10-19 review's
// findings against the service's trust model must be answered before C1.10,
// C1.11 and C1.16 are built, and the plan is where a reader looks for what is
// still open. The section records each with its reasoning, is not a decision
// (no D-number) and is not a task.
func TestCredentialsPlanRecordsTheOpenDesignFindings(t *testing.T) {
	const path = "docs/credentials-service/implementation-plan.md"
	doc := readDoc(t, path)
	sec := docSection(t, path, doc, "## Open design findings")
	if !strings.Contains(sec, "before C1.10, C1.11 and C1.16") {
		t.Error("the open design findings do not say what they block")
	}
	for _, want := range []string{
		"the attacker's side",
		"reserves and settles",
		"no binding between the caller's account and the run",
		"any repository's admitted tier-A accounts",
		"outlive the run",
		"break-glass",
		"startsWith",
		"the voice",
		"coordinator",
		"orchestrator",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("the open design findings omit a finding the review raised: %q", want)
		}
	}
	if strings.Contains(sec, "**D1") || strings.Contains(sec, "| D1") {
		t.Error("the open design findings carry a decision number; they are findings, not decisions")
	}
}

// TestCredentialsStatusLinesRecordTheC0Approval: the owner approved C0 on
// 2026-10-18 and its docs tasks are done, so the plan's and the README's status
// lines may not still read "plan only; nothing is implemented" and "no task
// approved" — and neither may any of the other four places that carried the
// same claim before the approval (the plan's rules of engagement, its gate, the
// README's plan row and the reconciliation's status line). Each stale claim is
// named below with the qualification that replaced it, so qualifying one file
// and leaving the other four standing fails here rather than shipping a plan
// that contradicts itself.
func TestCredentialsStatusLinesRecordTheC0Approval(t *testing.T) {
	for _, path := range []string{
		"docs/credentials-service/README.md",
		"docs/credentials-service/implementation-plan.md",
	} {
		doc := readDoc(t, path)
		if !strings.Contains(doc, "2026-10-18") {
			t.Errorf("%s does not record the owner's approval of C0 on 2026-10-18", path)
		}
	}
	// Each claim is anchored to the line that carries it, never to the document
	// around it: a document-wide check would stay green while one line was
	// reverted, so long as another line still said the right thing.
	for _, tc := range []struct {
		path, what string
		want       []string
		notWant    []string
	}{
		{"docs/credentials-service/implementation-plan.md", "the plan's status line",
			[]string{"2026-10-18", "C0.2, C0.3 and C0.6 are built", "no task of Slice 1, 2 or 3 is approved"},
			[]string{"plan only; nothing is implemented", "no task in this plan is approved"}},
		{"docs/credentials-service/README.md", "the README's status line",
			[]string{"2026-10-18", "Slice 0"},
			[]string{"plan only; nothing is implemented"}},
		{"docs/credentials-service/reconciliation.md", "the reconciliation's status line",
			[]string{"superseded in part on 2026-10-18", "Slice 0", "no task of them is approved"},
			[]string{"plan only; nothing is implemented"}},
	} {
		line := statusLine(t, tc.path)
		for _, want := range tc.want {
			if !strings.Contains(line, want) {
				t.Errorf("%s does not record the approval: it lacks %q", tc.what, want)
			}
		}
		for _, bad := range tc.notWant {
			if strings.Contains(line, bad) {
				t.Errorf("%s still reads %q, which the owner's approval of Slice 0 on 2026-10-18 contradicts", tc.what, bad)
			}
		}
	}

	// And nowhere else in the three documents either: the pre-approval wording
	// may not reappear on a line of its own, which is how it would come back —
	// one sentence at a time, in a place no anchored check above reads.
	for _, path := range []string{
		"docs/credentials-service/implementation-plan.md",
		"docs/credentials-service/README.md",
		"docs/credentials-service/reconciliation.md",
	} {
		for _, line := range strings.Split(readDoc(t, path), "\n") {
			for _, bad := range []string{"plan only; nothing is implemented", "no task in this plan is approved"} {
				if strings.Contains(line, bad) {
					t.Errorf("%s carries the pre-approval claim %q on a line of its own: %.120s", path, bad, line)
				}
			}
		}
	}

	// The other three lines that carried the same claim: the plan's rules of
	// engagement, the plan's gate, and the README's row for the plan. Each is
	// anchored to its own line, and each has to qualify the claim with Slice 0's
	// approval rather than drop it — the plan is still unapproved for everything
	// else.
	plan := readDoc(t, "docs/credentials-service/implementation-plan.md")
	if strings.Contains(plan, "**No task is approved.**") {
		t.Error("the plan's rules of engagement still say no task is approved, though the owner approved Slice 0 on 2026-10-18")
	}
	if !strings.Contains(plan, "**No task is approved but Slice 0's (2026-10-18); each of the others**") {
		t.Error("the plan's rules of engagement do not qualify the approval: they must say no task but Slice 0's is approved")
	}
	if strings.Contains(plan, "**Not passed. No task is approved.**") {
		t.Error("the plan's gate still reads \"Not passed. No task is approved.\", which the Slice 0 approval contradicts")
	}
	if !strings.Contains(plan, "**Passed for Slice 0, which is built; not passed for anything else") {
		t.Error("the plan's gate does not record that Slice 0 itself passed")
	}
	row := ""
	for _, l := range strings.Split(readDoc(t, "docs/credentials-service/README.md"), "\n") {
		if strings.Contains(l, "[implementation-plan.md]") {
			row = l
		}
	}
	if row == "" {
		t.Fatal("the README has no row for the implementation plan")
	}
	if strings.Contains(row, "**No task is approved.**") {
		t.Error("the README's row for the plan still says no task is approved, beside a status line that records the Slice 0 approval")
	}
	if !strings.Contains(row, "No Slice 1, 2 or 3 task is approved") {
		t.Error("the README's row for the plan does not qualify the approval: it must say no Slice 1, 2 or 3 task is approved")
	}
}

// statusLine returns the single "**Status:" line of a document. The plan and
// the README carry their status at the top, and the reconciliation — the record
// of a dated answer — keeps its own wording with the correction beside it, so
// each has to be read on its own line.
func statusLine(t *testing.T, path string) string {
	t.Helper()
	lines := []string{}
	for _, l := range strings.Split(readDoc(t, path), "\n") {
		if strings.HasPrefix(l, "**Status:") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("%s has %d \"**Status:\" lines; this guard reads exactly one", path, len(lines))
	}
	return lines[0]
}

// TestCredentialsDocsNeverStateTheSharedJobRefusalStays: the owner's ruling of
// 2026-10-18 lifted phase 3's refusal of an OAuth launch onto a shared job — the
// static `secret_env` mount carries it in 0.9.0 — so no line of the plan or the
// reconciliation may still say the refusal waits for the service or for C3.9.
// The reconciliation is a dated record, so its 2026-10-17 answer may stand; a
// line that repeats it must carry the correction beside it.
func TestCredentialsDocsNeverStateTheSharedJobRefusalStays(t *testing.T) {
	const planPath = "docs/credentials-service/implementation-plan.md"
	plan := readDoc(t, planPath)
	gc6 := docTail(t, planPath, plan, "6. **Per-repository jobs only**")
	if i := strings.Index(gc6, "\n7. **"); i != -1 {
		gc6 = gc6[:i]
	}
	for _, want := range []string{
		"is **lifted in 0.9.0**",
		"2026-10-18",
		"project-named-oauth-tokens.md",
		"stopgap",
		"unscheduled",
	} {
		if !strings.Contains(gc6, want) {
			t.Errorf("Global Constraint 6 does not record the owner's lifting of the shared-job refusal: it lacks %q", want)
		}
	}
	if strings.Contains(gc6, "stays until then") {
		t.Error("Global Constraint 6 still says the shared-job refusal stays until the phase-3 rules land, which the owner's ruling of 2026-10-18 supersedes")
	}
	c39 := docTail(t, planPath, plan, "- **C3.9 Shared jobs**")
	if i := strings.Index(c39, "\n\n"); i != -1 {
		c39 = c39[:i]
	}
	for _, want := range []string{"does not wait for this", "lifts it in 0.9.0", "stopgap"} {
		if !strings.Contains(c39, want) {
			t.Errorf("C3.9 does not say the refusal is already lifted and only the service's admission of a shared job waits: it lacks %q", want)
		}
	}
	if strings.Contains(c39, "refusal is lifted then") {
		t.Error("C3.9 still says Task 3.4a's refusal is lifted only when the phase-3 rules land")
	}
	// The stale sentence may appear on a line only when that line also marks it
	// as the superseded wording — which is how C0.2's red step quotes the phrase
	// it plants as a mutation — and never as a claim of its own.
	for _, line := range strings.Split(plan, "\n") {
		if strings.Contains(line, "stays until then") && !strings.Contains(line, "superseded wording") {
			t.Errorf("the plan still carries the stale claim on a line of its own: %.120s", line)
		}
	}
	const reconPath = "docs/credentials-service/reconciliation.md"
	recon := readDoc(t, reconPath)
	row := docTail(t, reconPath, recon, "| **D131** |")
	row = row[:strings.Index(row, "\n")]
	for _, want := range []string{"Superseded in part on 2026-10-18", "does **not** wait for", "project-named-oauth-tokens.md"} {
		if !strings.Contains(row, want) {
			t.Errorf("reconciliation D131 does not carry the 2026-10-18 correction of its own answer: it lacks %q", want)
		}
	}
	for _, line := range strings.Split(recon, "\n") {
		if strings.Contains(line, "stays until the service exists") && !strings.Contains(line, "Superseded in part on 2026-10-18") {
			t.Errorf("reconciliation repeats the refusal-stays claim without its correction: %.120s", line)
		}
	}
}

// TestChecklistCheck90RowDoesNotOverclaimU2AndU3: step 7 records that U3 stays
// open when no usage limit can be met, and reconciliation sources U2 to
// Anthropic's documentation rather than to this check, so the register's row
// and the section's own summary may not read as if the check settled both. Row
// 91's parallel overclaim was fixed; this is row 90's.
func TestChecklistCheck90RowDoesNotOverclaimU2AndU3(t *testing.T) {
	doc := readDoc(t, checklistPath)
	row := docTail(t, checklistPath, doc, "| 90 | The OAuth header swap")
	row = row[:strings.Index(row, "\n")]
	for _, bad := range []string{"settles U1, U2, U3 and U4", "settles U2", "settles U3"} {
		if strings.Contains(row, bad) {
			t.Errorf("the register's row for Check 90 claims more than the check settles: it says %q", bad)
		}
	}
	for _, want := range []string{"settles U1 and U4", "Anthropic's documentation", "only if a usage limit can be observed"} {
		if !strings.Contains(row, want) {
			t.Errorf("the register's row for Check 90 does not say what the check does and does not settle: it lacks %q", want)
		}
	}
	check := docSection(t, checklistPath, doc, chk90Heading)
	for _, want := range []string{"Anthropic's documentation", "U3 stays open", "settle two of"} {
		if !strings.Contains(check, want) {
			t.Errorf("Check 90's own summary does not state what it settles: it lacks %q", want)
		}
	}
	if strings.Contains(check, "settle four of") {
		t.Error("Check 90's summary still claims it settles four of the reconciliation's open items")
	}
}

// TestChecklistCheck91ColdStartProvesAFreshInstance: the step waited five
// minutes and asserted the first request was cold, while Cloud Run holds an idle
// instance far longer — this repository's own cold-start check (Check 41) waits
// 35 minutes — so U5 would have been written from a warm measurement. The relay
// now reports the time its process started, the step reads it before and after
// the wait, and the FACT only counts a cold figure when the two differ.
func TestChecklistCheck91ColdStartProvesAFreshInstance(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	relay := docSection(t, checklistPath, check, "### Check 91 step 2: the relay")
	for _, want := range []string{"STARTED = time.time()", `"started=%s\n"`} {
		if !strings.Contains(relay, want) {
			t.Errorf("Check 91's relay does not report which instance answered: it lacks %q", want)
		}
	}
	step := docSection(t, checklistPath, check, "### Check 91 step 4: cold start")
	for _, want := range []string{
		"sleep 2100",
		"35 minutes",
		"BEFORE=",
		"AFTER=",
		"NOT a cold start",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 91's cold-start step does not prove the instance was released: it lacks %q", want)
		}
	}
	if strings.Contains(step, "sleep 300") {
		t.Error("Check 91's cold-start step still waits only five minutes for the idle instance, which Cloud Run keeps far longer")
	}
	if !strings.Contains(check, "A cold figure is one whose two `started=` values differ") {
		t.Error("Check 91's FACT does not make the cold figure conditional on a new instance having answered")
	}
	if !strings.Contains(check, "started=` values that show a **different** instance") {
		t.Error("Check 91's Passes-when does not require the evidence that the cold request met a new instance")
	}
}

// TestChecklistCheck91PinsTheProtocolOfEachRow: the second body row relied on
// curl's default ALPN and recorded no version, so a curl built without HTTP/2
// would have sent HTTP/1.1 with no error and the row would have duplicated the
// first while its FACT claimed to record the front end's HTTP/2 behaviour. Both
// rows now pin their protocol and print `%{http_version}`, and the step stops
// when the client cannot speak HTTP/2 at all.
func TestChecklistCheck91PinsTheProtocolOfEachRow(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	step := docSection(t, checklistPath, check, "### Check 91 step 6: the request-body limit")
	for _, want := range []string{
		`--http1.1 -o "$CHK91/echo.out"`,
		`--http2 -o "$CHK91/echo.out"`,
		"%{http_version}",
		"curl -V | grep -q 'HTTP2'",
		"no HTTP/2 support",
		"degrades to HTTP/1.1 silently",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 91's body probe does not pin and record each row's protocol: it lacks %q", want)
		}
	}
}

// taskBlock returns one "###" task block of the plan: from its heading to the
// next "###" heading. docSection cannot bound it — it stops only at a "##"
// heading, which would take in every later task of the slice.
func taskBlock(t *testing.T, path, doc, heading string) string {
	t.Helper()
	sec := docSection(t, path, doc, heading)
	if i := strings.Index(sec[len(heading):], "\n### "); i != -1 {
		return sec[:len(heading)+i]
	}
	return sec
}

// taskField returns one field of a task block: from the marker's line to the
// line before the next one that opens a field ("**Tests.**", "**Red step.**",
// …). A field is a hard-wrapped paragraph, so anchoring to the field keeps an
// assertion from being satisfied by the same words in a neighbouring one — a
// file named under Discarded is not a file the task writes.
func taskField(t *testing.T, path, block, marker string) string {
	t.Helper()
	i := strings.Index(block, marker)
	if i == -1 {
		t.Fatalf("%s: a task block has no %s field", path, marker)
	}
	field := block[i:]
	if j := strings.Index(field[len(marker):], "\n**"); j != -1 {
		field = field[:len(marker)+j]
	}
	return field
}

// TestCredentialsPlanTaskBlocksNameTheGuardThatPinsThem: Slice 0's docs tasks
// were written when the docs had no guards, so C0.3 said "Tests. None (a
// document)" and "Red step. n/a (docs)" and neither it nor C0.2 listed
// internal/cli/docs_credentials_service_test.go — which is now what pins the
// sentences those tasks wrote, and which was mutation-proven against them. A
// task block that still says it has no test, or that names no file for one,
// tells the next reader the guard can be deleted with the text it protects.
func TestCredentialsPlanTaskBlocksNameTheGuardThatPinsThem(t *testing.T) {
	const path = "docs/credentials-service/implementation-plan.md"
	doc := readDoc(t, path)
	const guard = "internal/cli/docs_credentials_service_test.go"

	c03 := taskBlock(t, path, doc, "### Task C0.3: Checks 90 and 91")
	if files := taskField(t, path, c03, "**Files.**"); !strings.Contains(files, guard) {
		t.Errorf("C0.3's Files line does not name %s, which is the guard for both checks it writes", guard)
	}
	tests := taskField(t, path, c03, "**Tests.**")
	if !strings.Contains(tests, guard) {
		t.Errorf("C0.3's Tests line does not name %s", guard)
	}
	if strings.Contains(tests, "None") {
		t.Error("C0.3 still says it has no tests, though the guards for Checks 90 and 91 are part of this work")
	}
	red := taskField(t, path, c03, "**Red step.**")
	if strings.Contains(red, "n/a") {
		t.Error("C0.3 still has no red step, though each of its guards was proven by planting the defect it catches")
	}
	for _, want := range []string{"plant", "mutation"} {
		if !strings.Contains(strings.ToLower(red), want) {
			t.Errorf("C0.3's red step does not describe how its guards were proven: it lacks %q", want)
		}
	}

	c02 := taskBlock(t, path, doc, "### Task C0.2: The supersession notes")
	if tests := taskField(t, path, c02, "**Tests.**"); !strings.Contains(tests, guard) {
		t.Errorf("C0.2's Tests line does not name %s, which pins the notes it writes (§7's lifting and m9 §11)", guard)
	}
}

// TestCredentialsPlanC06ListsEveryCaseTheFileHas: C0.6's Tests line listed four
// cases while the file it writes has five — the mailbox and phone identities'
// denial was added and never written back — so the plan's own count of its
// guards was wrong. This reads the test file itself and requires every case in
// it to be named in the task block, which is what keeps the two from drifting
// the next time a case is added.
func TestCredentialsPlanC06ListsEveryCaseTheFileHas(t *testing.T) {
	const path = "docs/credentials-service/implementation-plan.md"
	doc := readDoc(t, path)
	block := taskBlock(t, path, doc, "### Task C0.6:")
	const file = "internal/budget/rules/creds_emulator_test.go"
	src := readDoc(t, file)
	re := regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`)
	names := re.FindAllStringSubmatch(src, -1)
	if len(names) < 5 {
		t.Fatalf("%s has %d test functions; this test's floor is the five known cases", file, len(names))
	}
	for _, m := range names {
		if !strings.Contains(block, m[1]) {
			t.Errorf("C0.6's Tests line does not name %s, which %s defines", m[1], file)
		}
	}
}

// TestCredentialsReadmeCallsTheSupersededPlanSuperseded: the README's Related
// list called the OAuth-reviewer-stage plan "the held" one, while the plan's
// own banner reads SUPERSEDED and this plan's Related line reads "(superseded
// by D130)" — the same stale sentence that named the parked design, which is
// mixed-credentials and not that plan.
func TestCredentialsReadmeCallsTheSupersededPlanSuperseded(t *testing.T) {
	const path = "docs/credentials-service/README.md"
	doc := readDoc(t, path)
	plan, mixed := "", ""
	for _, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, "[docs/plans/2026-10-14-oauth-reviewer-stage.md]") {
			plan = l
		}
		if strings.Contains(l, "[docs/design/mixed-credentials.md]") {
			mixed = l
		}
	}
	if plan == "" || mixed == "" {
		t.Fatalf("%s no longer links the OAuth-reviewer-stage plan and the parked design", path)
	}
	if strings.Contains(plan, "held") {
		t.Errorf("the README still calls the superseded OAuth-reviewer-stage plan a held one: %.160s", plan)
	}
	for _, want := range []string{"superseded", "D130"} {
		if !strings.Contains(plan, want) {
			t.Errorf("the README's entry for the OAuth-reviewer-stage plan does not say what it is: it lacks %q", want)
		}
	}
	// The sentence's other half: the parked design the spec answers is
	// mixed-credentials, and that is the entry that has to say so.
	for _, want := range []string{"parked", "answers", "D130"} {
		if !strings.Contains(mixed, want) {
			t.Errorf("the README's entry for mixed-credentials does not say what it is: it lacks %q", want)
		}
	}
	if strings.Contains(doc, "the held [docs/plans/2026-10-14-oauth-reviewer-stage.md]") {
		t.Error("the README still calls the OAuth-reviewer-stage plan the held one")
	}
}

// TestCredentialsPlanListsTheGuardFileAmongItsFiles: the guard file is new in
// this work, so every list of this work's files has to carry it — the docs
// guards the plan builds on, the files the plan creates, and the L0 level row
// that enumerates what C0.2, C0.3 and C0.6 write. A list that omits it is how
// a reader concludes the file is not part of the task.
func TestCredentialsPlanListsTheGuardFileAmongItsFiles(t *testing.T) {
	const path = "docs/credentials-service/implementation-plan.md"
	doc := readDoc(t, path)
	const guard = "internal/cli/docs_credentials_service_test.go"

	// That row lists bare file names, the way it lists docs_security_test.go
	// beside docs_project_centric_test.go.
	guards := docTail(t, path, doc, "| The docs guards |")
	guards = guards[:strings.Index(guards, "\n")]
	if !strings.Contains(guards, "docs_credentials_service_test.go") {
		t.Error("the plan's list of the docs guards does not name the guards this work adds")
	}
	created := docSection(t, path, doc, "### Created")
	if !strings.Contains(created, guard) {
		t.Error("the plan's Created table does not list the guard file this work adds")
	}
	l0 := docTail(t, path, doc, "| **L0** |")
	l0 = l0[:strings.Index(l0, "\n")]
	if !strings.Contains(l0, guard) {
		t.Error("the plan's L0 row (what C0.2, C0.3 and C0.6 write) does not list the guard file")
	}
}

// TestChecklistCheck47IsSupersededNotHeld: the OAuth-reviewer-stage plan was
// superseded on 2026-10-17 (D130), so Check 47 does not wait for phase 5 of
// docs/design/project-centric.md any more — the hold is moot and the number is
// reserved and unused. Three places in the register still carried the hold as a
// live instruction (the general paragraph, row 47 and the section banner), each
// telling a reader the check would run when the hold lifted. Each is anchored
// here, and no line may carry the held wording without its supersession beside
// it on that same line.
func TestChecklistCheck47IsSupersededNotHeld(t *testing.T) {
	doc := readDoc(t, checklistPath)

	anchors := []struct {
		what, marker string
		want         []string
	}{
		{"the register's paragraph about the number", "**Check 47 belongs to the OAuth-reviewer-stage plan**",
			[]string{"superseded by the credentials service on 2026-10-17 (D130)", "hold is moot", "reserved"}},
		{"the register's row 47", "| 47 |",
			[]string{"SUPERSEDED by the credentials service on 2026-10-17 (D130)", "never to be run", "reserved and unused"}},
	}
	for _, a := range anchors {
		line := ""
		for _, l := range strings.Split(doc, "\n") {
			if strings.Contains(l, a.marker) {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("%s is gone from the register; this guard anchors to it", a.what)
		}
		for _, want := range a.want {
			if !strings.Contains(line, want) {
				t.Errorf("%s does not record that Check 47 is superseded rather than held: it lacks %q", a.what, want)
			}
		}
	}

	// The section's banner, inside the section so that a "> **STATUS:" line
	// elsewhere in the register cannot satisfy it.
	check := docSection(t, checklistPath, doc, chk47Heading)
	banner := ""
	for _, l := range strings.Split(check, "\n") {
		if strings.HasPrefix(l, "> **STATUS:") {
			banner = l
		}
	}
	if banner == "" {
		t.Fatal("Check 47 has no STATUS banner; this guard anchors to it")
	}
	for _, want := range []string{
		"SUPERSEDED by the credentials service on 2026-10-17 (D130)",
		"never to be run",
		"reserved and unused",
		"hold is moot",
	} {
		if !strings.Contains(banner, want) {
			t.Errorf("Check 47's banner does not record the supersession of the plan it belongs to: it lacks %q", want)
		}
	}
	if strings.Contains(check, "When the hold lifts") {
		t.Error("Check 47 still tells the reader what happens when the hold lifts, though the plan was superseded before the phase it was held for")
	}

	// And nowhere in the register may the hold stand as a live instruction.
	for _, line := range strings.Split(doc, "\n") {
		if !strings.Contains(line, "HELD until phase 5") {
			continue
		}
		if !strings.Contains(line, "D130") && !strings.Contains(line, "superseded") {
			t.Errorf("the register still holds a check against phase 5 without recording the supersession that moots it: %.120s", line)
		}
	}
}
