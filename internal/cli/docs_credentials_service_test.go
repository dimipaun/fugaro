package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The credentials service's Slice 0 writes two live checks into
// docs/gcp-live-checklist.md (Checks 90 and 91), and both are instructions a
// human types against a real subscription token and a real cloud project. The
// independent review of 2026-10-19 (PR #584) found seven defects in them, plus
// two wording defects in the C0.2 notes and one in the number register. The tests
// below pin each fix to the sentence or the command that carries it, so an edit
// that drops one fails here rather than passing on the section's heading: a
// guard that leaves an API key in the shell makes the control prove nothing, a
// recursive delete with no shape check removes a path the reader set, a service
// deployed without the registry repository its image tag names fails at the
// build, a token minted once is expired by the probe that runs an hour later,
// prose that promises a body the commands never fetch measures nothing, a FACT
// that asks for a field of a body no flag of the check's own proxy could
// surface measures nothing either, and an HTTP/2 row that is not end to end
// settles U5 wrongly.

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

// stepBlock returns one "###" step of a check: from its heading to the next
// "###" heading (or to the end of the check). docSection cannot bound it — it
// stops only at a "##" heading, and Check 91 is the register's last "##"
// section — so a slice taken with docSection runs to the end of the file and a
// later step's text satisfies an assertion about this one: the guard's marker
// refusal and the Restore's are the same line, and deleting either left the
// other to answer for it.
func stepBlock(t *testing.T, path, check, heading string) string {
	t.Helper()
	sec := docSection(t, path, check, heading)
	if i := strings.Index(sec[len(heading):], "\n### "); i != -1 {
		return sec[:len(heading)+i]
	}
	return sec
}

// TestChecklistCheck90GuardClearsTheClientCredentialEnvironment: Claude Code
// reads CLAUDE_CODE_USE_VERTEX / CLAUDE_CODE_USE_BEDROCK and an apiKeyHelper
// setting before CLAUDE_CODE_OAUTH_TOKEN, and ANTHROPIC_API_KEY,
// ANTHROPIC_AUTH_TOKEN and ANTHROPIC_BASE_URL decide what the call
// authenticates with and where it goes, so a control run in a shell carrying
// any of them is not a measurement of the setup-token — it can succeed on a
// Vertex service account, on an API key or on a helper command and be recorded
// as a swap that works. The guard unsets all five, stops when a settings file
// configures an apiKeyHelper, and says so without printing a value.
func TestChecklistCheck90GuardClearsTheClientCredentialEnvironment(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk90Heading)
	guard := stepBlock(t, checklistPath, check, "### Check 90 step 1: setup and guard")
	flat := docFlat(guard)
	// The loop is one shell statement however markdown wraps it, so it is read
	// from `for v in` to its `; do`: every one of the five names has to be in
	// it, and each is a way `claude` can answer the control call with something
	// other than the setup-token.
	loop := flat[strings.Index(flat, "for v in"):]
	loop = loop[:strings.Index(loop, "; do")]
	for _, v := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK",
	} {
		if !strings.Contains(loop, v) {
			t.Errorf("Check 90's guard does not clear %s, so the control call can succeed on it and be recorded as a swap that works", v)
		}
	}
	for _, want := range []string{
		`unset "$v"`,
		"its value is not printed",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 90's guard does not clear the client's own credential environment: it lacks %q", want)
		}
	}
	// The fourth way in is a setting, not a variable, so nothing above clears
	// it: each file Claude Code reads one from is checked, and a configured
	// helper stops the block before the control runs.
	for _, want := range []string{
		`jq -e 'has("apiKeyHelper")'`,
		"$HOME/.claude/settings.json",
		".claude/settings.local.json",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 90's guard does not look for an apiKeyHelper where Claude Code reads one: it lacks %q, so a helper can answer the control call", want)
		}
	}
	helper := strings.Index(flat, `has("apiKeyHelper")`)
	if helper == -1 {
		t.Fatal("Check 90's guard never checks for an apiKeyHelper")
	}
	// The stop has to be the one the helper check itself takes (the sections
	// have many other stops, so "an exit 1 somewhere after it" would pass a
	// guard that only warns about the helper).
	if !strings.Contains(flat, "claude may authenticate with what that helper prints instead of with the setup-token") ||
		!strings.Contains(flat, `and re-run this block. Its value is not printed." >&2; exit 1; }`) {
		t.Fatal("Check 90's guard finds an apiKeyHelper but does not stop on it: a configured helper would answer the control call")
	}
	if !strings.Contains(flat[helper:], "no apiKeyHelper is configured") {
		t.Error("Check 90's guard does not report that no apiKeyHelper is configured when it passes, so an unconfigured helper and an unchecked one read the same")
	}
	if !strings.Contains(check, "can only succeed on the setup-token") {
		t.Error("Check 90's section does not say why the client's credential variables are cleared: that the control can only succeed on the setup-token")
	}
	if !strings.Contains(docFlat(check), "uses Vertex, which is exactly why this stop is here") {
		t.Error("Check 90 does not say why the two provider variables are cleared: with CLAUDE_CODE_USE_VERTEX left set (as on the owner's machine) the control measures Vertex")
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
	guard := stepBlock(t, checklistPath, check, "### Check 90 step 1: setup and guard")
	for _, want := range []string{
		`SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/chk90.XXXXXXXX")`,
		`: > "$SCRATCH/.chk90-mine"`,
		`SCRATCH_RECORD`,
		// The guard refuses a recorded path that carries no marker of its own,
		// so a stale $SCRATCH_RECORD naming someone else's directory is never
		// adopted — the same refusal Check 91's guard makes, and read from the
		// step itself: step 9 has a copy of this line, and an unbounded slice
		// would let that copy answer for this one.
		`[ -f "$SCRATCH/.chk90-mine" ]`,
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 90's guard does not make its own scratch directory and refuse a path it does not recognise: it lacks %q", want)
		}
	}
	// A reader-set SCRATCH is what the review found: the value went into an
	// `rm -rf` with nothing checking it.
	if strings.Contains(check, `SCRATCH=${SCRATCH:-$HOME/chk90-scratch}`) {
		t.Error("Check 90 still takes the scratch directory from a value the reader sets, which is what step 9's rm -rf removes")
	}
	restore := stepBlock(t, checklistPath, check, "### Check 90 step 9: Restore")
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

// TestChecklistCheck91ScratchDirectoryIsTheChecksOwn: step 7 removes the
// scratch directory recursively, so — exactly as in Check 90, whose twin guard
// is TestChecklistCheck90ScratchDirectoryIsTheChecksOwn — the path may not be
// one the reader set: it is the one this check makes itself (mktemp -d, mode
// 0700, carrying a marker the check writes) and remembers across steps in
// $CHK91_RECORD, and the Restore re-checks the shape and the marker immediately
// before the delete. Moving the `rm -rf` above either refusal, or dropping the
// marker check, fails here instead of removing a directory the check never made.
func TestChecklistCheck91ScratchDirectoryIsTheChecksOwn(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	guard := stepBlock(t, checklistPath, check, "### Check 91 step 1: setup and guard")
	for _, want := range []string{
		`CHK91=$(mktemp -d "${TMPDIR:-/tmp}/chk91.XXXXXXXX")`,
		`: > "$CHK91/.chk91-mine"`,
		`CHK91_RECORD`,
		`[ -f "$CHK91/.chk91-mine" ]`,
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("Check 91's guard does not make its own scratch directory and refuse a path it does not recognise: it lacks %q", want)
		}
	}
	restore := stepBlock(t, checklistPath, check, "### Check 91 step 7: Restore")
	// The order is the point: both refusals stand between the path and the
	// delete, not after it.
	shape := strings.Index(restore, `|/|"$HOME"`)
	marker := strings.Index(restore, `[ -f "$CHK91/.chk91-mine" ]`)
	rmrf := strings.Index(restore, `rm -rf "$CHK91"`)
	if shape == -1 || marker == -1 || rmrf == -1 {
		t.Fatalf("Check 91's Restore lacks its scratch-directory guard (shape=%d, marker=%d, rm -rf=%d)", shape, marker, rmrf)
	}
	if !(shape < rmrf && marker < rmrf) {
		t.Error("Check 91's Restore runs `rm -rf \"$CHK91\"` before checking the path's shape and its marker")
	}
	// The record file goes with the directory: a surviving $CHK91_RECORD names
	// a path the next run would adopt as its own.
	if !strings.Contains(restore, `rm -f "$CHK91_RECORD"`) {
		t.Error("Check 91's Restore removes the scratch directory but leaves $CHK91_RECORD naming it")
	}
}

// TestChecklistCheck91NeverAdoptsWhatItDidNotCreate: the Restore deletes the
// service, its image and the registry repository, so every one of those deletes
// rests on the check knowing what it created. Hard stop 4 refuses a fugaro-chk91
// that is already there unless step 3 recorded the deploy in
// $CHK91/service-mine, and the baseline stops the check outright on a
// repository of its own name that is already in the project — it never adopts
// one — so $CHK91/repo-existed is the ownership record the Restore reads before
// the scratch directory holding it is removed. Deleting either stop leaves a
// check that would adopt, write into and then delete a service or repository
// the installation already had.
func TestChecklistCheck91NeverAdoptsWhatItDidNotCreate(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	guard := stepBlock(t, checklistPath, check, "### Check 91 step 1: setup and guard")

	// Hard stop 4, in the guard: an existing service is refused before anything
	// touches it, and the refusal says why.
	describe := strings.Index(guard, `gcloud run services describe "${SVC:?}"`)
	mine := strings.Index(guard, `[ -f "$CHK91/service-mine" ]`)
	if describe == -1 || mine == -1 {
		t.Fatalf("Check 91's guard has no hard stop 4 (describe=%d, service-mine=%d): an existing fugaro-chk91 would be adopted", describe, mine)
	}
	if !strings.Contains(guard[describe:], "it is not this check's") {
		t.Error("Check 91's hard stop 4 does not refuse a service that is not this check's")
	}
	// ... and step 3 records the deploy that makes the service this check's.
	build := stepBlock(t, checklistPath, check, "### Check 91 step 3: build and deploy the throwaway service")
	if !strings.Contains(build, `: > "$CHK91/service-mine"`) {
		t.Error("Check 91's step 3 does not record the service it deployed in $CHK91/service-mine, so hard stop 4 would refuse the service this check itself created")
	}

	// The baseline: taken once, before anything is created, and it is a hard
	// stop — an existing repository is never adopted, so the only answer the
	// file ever holds is `no`, and the file itself is the ownership record.
	baseStart := strings.Index(check, "**Baseline, once, before anything is created — and it is a hard stop.**")
	baseEnd := strings.Index(check, "### Check 91 step 2")
	if baseStart == -1 || baseEnd == -1 || baseStart > baseEnd {
		t.Fatal("Check 91 has no baseline block before step 2, so nothing says whether the registry repository was already there")
	}
	baseline := check[baseStart:baseEnd]
	for _, want := range []string{
		`[ ! -e "$CHK91/repo-existed" ]`,
		`gcloud artifacts repositories describe "${SVC:?}"`,
		`echo no > "$CHK91/repo-existed"`,
		`exit 1`,
	} {
		if !strings.Contains(baseline, want) {
			t.Errorf("Check 91's baseline does not stop on a repository that is already there: it lacks %q", want)
		}
	}
	// The answer that loses the ownership record is gone: a baseline that can
	// write `yes` is a baseline that lets the check write into, and its Restore
	// delete, a repository the check did not create.
	if strings.Contains(baseline, `echo yes`) {
		t.Error("Check 91's baseline records `yes` for a repository it found, which is the answer that makes it adopt and later delete a repository it did not create")
	}
	if !strings.Contains(docFlat(baseline), "never adopts") {
		t.Error("Check 91's baseline does not say that an existing repository is never adopted")
	}

	// The Restore reads the ownership record before the scratch directory
	// holding it is gone, and deletes the repository only on the answer that
	// says the check created it.
	restore := stepBlock(t, checklistPath, check, "### Check 91 step 7: Restore")
	read := strings.Index(restore, `REPO_MINE=$(cat "$CHK91/repo-existed")`)
	rmrf := strings.Index(restore, `rm -rf "$CHK91"`)
	del := strings.Index(restore, `gcloud artifacts repositories delete "${SVC:?}"`)
	if read == -1 || rmrf == -1 || del == -1 {
		t.Fatalf("Check 91's Restore lacks the baseline read, the scratch delete or the repository delete (read=%d, rm=%d, delete=%d)", read, rmrf, del)
	}
	if read > rmrf {
		t.Error("Check 91's Restore reads $CHK91/repo-existed after the scratch directory that holds it is removed")
	}
	if read > del {
		t.Error("Check 91's Restore deletes the registry repository before reading the baseline that says whether it was this check's to delete")
	}
	if !strings.Contains(restore, `[ "$REPO_MINE" = no ]`) || !strings.Contains(restore[read:], "exit 1") {
		t.Error("Check 91's Restore deletes the registry repository without the baseline's answer gating it and stopping it: a repository the installation already had would be deleted")
	}
}

// TestChecklistCheck91CreatesItsOwnRegistryRepository: the image tag names an
// Artifact Registry repository in the project, and nothing created it, so the
// build failed at its first step. The check creates the repository its own name
// points at — the baseline has already stopped it unless that repository was
// absent, so this one is the check's own — and the Restore deletes it.
func TestChecklistCheck91CreatesItsOwnRegistryRepository(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	build := stepBlock(t, checklistPath, check, "### Check 91 step 3: build and deploy the throwaway service")
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
	// The describe must come first, or a re-run of the step fails on a
	// repository that is already this check's own.
	if desc := strings.Index(build, `gcloud artifacts repositories describe "${SVC:?}"`); desc == -1 || desc > create {
		t.Error("Check 91 creates its registry repository without describing it first, so a re-run of the step fails on a repository that is already this check's")
	}
	// The image tag is the repository's own name, which is what keeps the
	// creation, the push and the Restore's delete on one repository.
	if !strings.Contains(check, `IMG=${L}-docker.pkg.dev/${P}/${SVC}/${SVC}:chk91`) {
		t.Error("Check 91's image tag does not name the registry repository the check creates")
	}
	restore := stepBlock(t, checklistPath, check, "### Check 91 step 7: Restore")
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
	probe := stepBlock(t, checklistPath, check, "### Check 91 step 6: the request-body limit")
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
	probe := stepBlock(t, checklistPath, check, "### Check 91 step 6: the request-body limit")
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
	// The row's own U4 clause is pinned by
	// TestChecklistCheck90RecordsU4sThirdPartOnlyWhenALimitIsMet; what is here
	// is that it may not read as if the check settled U2 or U3.
	for _, want := range []string{"settles U1", "Anthropic's documentation", "only if a usage limit can be observed"} {
		if !strings.Contains(row, want) {
			t.Errorf("the register's row for Check 90 does not say what the check does and does not settle: it lacks %q", want)
		}
	}
	check := docSection(t, checklistPath, doc, chk90Heading)
	for _, want := range []string{"Anthropic's documentation", "U3 stays open", "settle one of"} {
		if !strings.Contains(check, want) {
			t.Errorf("Check 90's own summary does not state what it settles: it lacks %q", want)
		}
	}
	if strings.Contains(check, "settle four of") {
		t.Error("Check 90's summary still claims it settles four of the reconciliation's open items")
	}
	// The same overclaim came back once, 70 lines below the corrected sentence:
	// step 2's FACT line read "That sentence is U2's answer". The step is read
	// on its own — a section-wide check is satisfied by the correction above it
	// — and no line of the section may call the check's expiry record U2's
	// answer.
	step := stepBlock(t, checklistPath, check, "### Check 90 step 2:")
	for _, want := range []string{"U2's **evidence, not its answer**", "Anthropic's documentation"} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 90's step 2 FACT line does not record the token's stated expiry as U2's evidence rather than its answer: it lacks %q", want)
		}
	}
	for _, line := range strings.Split(check, "\n") {
		if strings.Contains(line, "is U2's answer") {
			t.Errorf("Check 90 still says the check answers U2 on a line of its own: %.140s", line)
		}
	}
}

// TestChecklistCheck90RecordsU4sThirdPartOnlyWhenALimitIsMet: the register's row
// 90 said Check 90 "settles U1 and U4", and U4 is three parts — Claude Code's
// client timeout, its retry count and the wording it prints for a usage limit —
// of which no step recorded the third: step 7's FACT read the wire's status,
// error type and reset headers, and step 8 times a delayed request and counts
// the attempts, which is the timeout and the retry count, and F15 keeps that
// wording UNVERIFIED (the skill's loose "usage limit" match is what would read
// it). Step 7 asks for it now, when a limit is met, and the row, the section's
// own summary, the plan's Check 90 row and C0.3, and reconciliation's U4 row all
// say the same three things: the timeout and the retry count are settled, the
// wording only if a usage limit can be observed, and it stays UNVERIFIED
// otherwise — as U3 does when no limit can be met.
func TestChecklistCheck90RecordsU4sThirdPartOnlyWhenALimitIsMet(t *testing.T) {
	doc := readDoc(t, checklistPath)
	row := docTail(t, checklistPath, doc, "| 90 | The OAuth header swap")
	row = row[:strings.Index(row, "\n")]
	for _, want := range []string{
		"settles U1, and U4's client timeout and retry count",
		"what Claude Code itself prints when it meets one",
		"only if a usage limit can be observed",
		"that part stays UNVERIFIED otherwise",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("the register's row for Check 90 does not say which parts of U4 the check settles: it lacks %q", want)
		}
	}
	if strings.Contains(row, "settles U1 and U4") {
		t.Error("the register's row for Check 90 still says the check settles U4 whole, though no step records the wording Claude Code prints for a limit")
	}
	check := docSection(t, checklistPath, doc, chk90Heading)
	step7 := stepBlock(t, checklistPath, check, "### Check 90 step 7:")
	for _, want := range []string{
		"what `claude` itself printed for the limit",
		"U4's third part",
		"that part of U4 stays UNVERIFIED",
	} {
		if !strings.Contains(step7, want) {
			t.Errorf("Check 90's step 7 does not record Claude Code's own wording for a limit, which is U4's third part: it lacks %q", want)
		}
	}
	// The step the wording would otherwise be read off is step 8, and its FACT is
	// the timeout and the retry count: this pins that it still records only that,
	// so a later summary cannot source the wording to a step that never had it.
	step8 := stepBlock(t, checklistPath, check, "### Check 90 step 8:")
	if !strings.Contains(step8, "the exact text of a give-up") {
		t.Error("Check 90's step 8 no longer records the give-up's own text, which its FACT line claims")
	}
	for _, want := range []string{
		"U4**'s client timeout and retry count",
		"Claude Code's own wording when it meets a limit",
		"stays UNVERIFIED",
	} {
		if !strings.Contains(check, want) {
			t.Errorf("Check 90's own summary does not say what it settles of U4: it lacks %q", want)
		}
	}
	// The section's pass condition and its paste-back line carry the same claim:
	// when step 7 met a limit, the wording it recorded is part of what the run
	// returns and pastes back, not only a line inside the step.
	for _, want := range []string{
		"U4's timings and retry count are recorded (step 8)",
		"when step 7 met a limit, Claude Code's own wording for it",
		"when step 7 met a limit, what the client itself printed for it",
	} {
		if !strings.Contains(check, want) {
			t.Errorf("Check 90 does not carry Claude Code's own wording for a limit through to what the run returns: it lacks %q", want)
		}
	}
	if strings.Contains(check, "**U4** (Claude Code's client timeout and retries)") {
		t.Error("Check 90's summary still counts U4 among the items the check settles whole")
	}
	const reconPath = "docs/credentials-service/reconciliation.md"
	recon := readDoc(t, reconPath)
	u4 := docTail(t, reconPath, recon, "| U4 | Claude Code's client timeout, retry count and usage-limit text")
	u4 = u4[:strings.Index(u4, "\n")]
	for _, want := range []string{
		"step 8 records the timeout and the retry count",
		"step 7 records the client's own wording only if a usage limit can be observed",
		"stays UNVERIFIED otherwise",
	} {
		if !strings.Contains(u4, want) {
			t.Errorf("reconciliation's U4 row does not agree with the register on what Check 90 settles: it lacks %q", want)
		}
	}
	const planPath = "docs/credentials-service/implementation-plan.md"
	plan := readDoc(t, planPath)
	planRow := docTail(t, planPath, plan, "| **90** | **Header swap.**")
	planRow = planRow[:strings.Index(planRow, "\n")]
	for _, want := range []string{
		"its own wording for a usage limit when one can be observed",
		"stays UNVERIFIED if none is",
	} {
		if !strings.Contains(planRow, want) {
			t.Errorf("the plan's own Check 90 row does not say what the check records of U4: it lacks %q", want)
		}
	}
	if c03 := taskBlock(t, planPath, plan, "### Task C0.3: Checks 90 and 91"); !strings.Contains(c03, "the client's own wording for it, which is U4's third part") {
		t.Error("C0.3 does not list the client's own wording for a limit among the check's FACT lines, though step 7 asks for it")
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
	relay := stepBlock(t, checklistPath, check, "### Check 91 step 2: the relay")
	for _, want := range []string{"STARTED = time.time()", `"started=%s\n"`} {
		if !strings.Contains(relay, want) {
			t.Errorf("Check 91's relay does not report which instance answered: it lacks %q", want)
		}
	}
	step := stepBlock(t, checklistPath, check, "### Check 91 step 4: cold start")
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
	step := stepBlock(t, checklistPath, check, "### Check 91 step 6: the request-body limit")
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

// TestCredentialsPlanSaysWhatThisPullRequestChanges: the plan's first Global
// Constraint said the pull request changes "No Go, Terraform, rules, schema,
// skill or workflow" — while the same plan's status line records C0.3 and C0.6
// as built, the two tasks that add internal/cli/docs_credentials_service_test.go
// and internal/budget/rules/creds_emulator_test.go. A constraint that denies
// the files the status line reports is a reader's trap, so both halves are
// anchored here: the two test files named (and the absolute claim gone), and the
// reviewer's line, which called the pull request "docs only" for the same
// reason. The two files are read, so the constraint cannot name a path the
// pull request does not have.
func TestCredentialsPlanSaysWhatThisPullRequestChanges(t *testing.T) {
	const path = "docs/credentials-service/implementation-plan.md"
	doc := readDoc(t, path)
	gc1 := docTail(t, path, doc, "1. **This pull request changes nineteen files")
	if i := strings.Index(gc1, "\n2. **"); i != -1 {
		gc1 = gc1[:i]
	}
	const cliGuard = "internal/cli/docs_credentials_service_test.go"
	const rulesGuard = "internal/budget/rules/creds_emulator_test.go"
	for _, want := range []string{
		cliGuard,
		rulesGuard,
		"firebase",
		"test only",
		"No production Go, Terraform, rules, schema, skill or workflow change",
	} {
		if !strings.Contains(gc1, want) {
			t.Errorf("Global Constraint 1 does not say what this pull request actually changes: it lacks %q", want)
		}
	}
	if strings.Contains(doc, "No Go, Terraform, rules, schema, skill or workflow change") {
		t.Error("the plan still says the pull request changes no Go at all, though C0.3 and C0.6 add two Go test files")
	}
	// The two files it names are the two Slice 0's task blocks write, so the
	// constraint and the tasks below cannot drift apart.
	for _, tc := range []struct{ task, file string }{
		{"### Task C0.3: Checks 90 and 91", cliGuard},
		{"### Task C0.6:", rulesGuard},
	} {
		// The Files field, not the block: the file has to be one the task
		// writes, and naming it in the tests line alone would not make it so.
		files := taskField(t, path, taskBlock(t, path, doc, tc.task), "**Files.**")
		if !strings.Contains(files, tc.file) {
			t.Errorf("%s does not write %s, which Global Constraint 1 names as one of the two files this pull request adds", tc.task, tc.file)
		}
	}
	// ... and both are really there, with the shapes the constraint claims.
	if src := readDoc(t, cliGuard); !strings.HasPrefix(src, "package cli") {
		t.Errorf("%s is not the package cli guard file Global Constraint 1 names", cliGuard)
	}
	if src := readDoc(t, rulesGuard); !strings.Contains(src, "//go:build firebase") {
		t.Errorf("%s is not behind the firebase tag Global Constraint 1 claims for it", rulesGuard)
	}
	if strings.Contains(doc, "**this pull request** (docs only)") {
		t.Error("the plan's review focus still calls this pull request docs only, though it adds two Go test files")
	}
	// ... and it has to name them: the constraint's "the documents" was four
	// credential documents, and the pull request's diff
	// (`git diff origin/integration-0.9.0 --name-status`) is the nineteen
	// paths below — the eight of the credentials service and the register, the
	// four D130 and D131 supersede or amend, the documents the reviews
	// corrected where they contradicted the tree (this run's SECURITY.md,
	// docs/design/project-named-oauth-tokens.md and §7 of
	// docs/design/project-centric.md among them), and the five test files.
	// `spec.md` is the one document under docs/credentials-service/ that is not
	// among them — it is already on the base, added by #512 — so a constraint
	// that claims to add it describes someone else's change, and one that
	// names four documents leaves the register and the four superseded design
	// documents out. `docs/design/bucket-iam.md` is the one document this
	// list once carried and no longer does: its corrected text came in with the
	// base merge, so the pull request carries its guard file and not it.
	// The documents are enumerated before the sentence about spec.md, which is
	// the one credential document this pull request leaves alone: a path named
	// only in the constraint's closing sentence ("no file beyond those
	// nineteen") would otherwise answer for a document the pull request does
	// not change.
	docs := gc1
	if i := strings.Index(gc1, "`docs/credentials-service/spec.md`"); i != -1 {
		docs = gc1[:i]
	}
	for _, file := range []string{
		"docs/credentials-service/implementation-plan.md",
		"docs/credentials-service/README.md",
		"docs/credentials-service/reconciliation.md",
		"docs/gcp-live-checklist.md",
		"docs/design/mixed-credentials.md",
		"docs/design/project-centric.md",
		"docs/design/m9-budget-and-dashboard.md",
		"docs/plans/2026-10-14-oauth-reviewer-stage.md",
		"docs/git-providers.md",
		"docs/project-layer.md",
		"docs/checkout.md",
		"SECURITY.md",
		"docs/design/project-named-oauth-tokens.md",
		"website/content/security.md",
		cliGuard,
		"internal/cli/docs_project_layer_test.go",
		"internal/cli/docs_bucket_iam_test.go",
		"internal/cli/docs_checkout_test.go",
		rulesGuard,
	} {
		where := gc1
		if !strings.HasPrefix(file, "internal/") {
			where = docs
		}
		if !strings.Contains(where, file) {
			t.Errorf("Global Constraint 1 does not name %s, which this pull request changes", file)
		}
		readDoc(t, file) // the path it names is one the tree really has
	}
	// The documents this round's security review added to the constraint, named
	// as documents it changes and not only in passing: GC1's closing sentence
	// names SECURITY.md too ("no file beyond those nineteen"), which is not the
	// same claim as listing it among the changed documents.
	if !strings.Contains(docs, "`SECURITY.md`, `docs/design/project-named-oauth-tokens.md`") {
		t.Error("Global Constraint 1 does not name SECURITY.md and docs/design/project-named-oauth-tokens.md among the documents this pull request changes: the stopgap they promise to replace is stated there")
	}
	if !strings.Contains(docs, "`website/content/security.md`") {
		t.Error("Global Constraint 1 does not name website/content/security.md, the generated page SECURITY.md is the source of")
	}
	if !strings.Contains(gc1, "spec.md") || !strings.Contains(gc1, "already on the base") {
		t.Error("Global Constraint 1 does not say spec.md is already on the base, so the reader cannot tell it is the one credential document this pull request leaves alone")
	}
	if strings.Contains(gc1, "it adds and edits `spec.md`") {
		t.Error("Global Constraint 1 still says this pull request adds spec.md, which the base already carries")
	}
	if !strings.Contains(doc, "the fourteen documents and the five test files that pin them") {
		t.Error("the plan's review focus does not say what this pull request is: the fourteen documents and the five test files that pin them")
	}
	if strings.Contains(doc, "the four documents and the two test files") {
		t.Error("the plan's review focus still counts four documents, but the pull request changes eight")
	}
}

// TestCredentialsPlanC03ListsEveryChecklistGuardItsFileHas: C0.3's Tests line
// enumerates the guards the check sections are pinned by, and its Red step
// lists the defects each was proven against — and two guards this pull request
// ships (the Check 91 scratch-directory and adoption guards) were named in
// neither, so the normative task list disagreed with the file it governs. This
// reads that file and requires every checklist guard in it to be named in the
// task block, the way TestCredentialsPlanC06ListsEveryCaseTheFileHas does for
// the emulator cases: the next guard added to the file and not to the plan
// fails here.
func TestCredentialsPlanC03ListsEveryChecklistGuardItsFileHas(t *testing.T) {
	const path = "docs/credentials-service/implementation-plan.md"
	doc := readDoc(t, path)
	block := taskBlock(t, path, doc, "### Task C0.3: Checks 90 and 91")
	const file = "internal/cli/docs_credentials_service_test.go"
	src := readDoc(t, file)
	re := regexp.MustCompile(`(?m)^func (TestChecklist\w+)\(t \*testing\.T\)`)
	names := re.FindAllStringSubmatch(src, -1)
	if len(names) < 13 {
		t.Fatalf("%s has %d checklist guards; this test's floor is the thirteen known ones", file, len(names))
	}
	for _, m := range names {
		if !strings.Contains(block, m[1]) {
			t.Errorf("C0.3's task block does not name %s, which %s defines and which pins a sentence C0.3 writes", m[1], file)
		}
	}
	// The Red step carries its own list, and it is the one a reader checks a
	// guard's worth against: the mutations the two Check 91 guards were proven
	// with are named there, not only the guards.
	red := taskField(t, path, block, "**Red step.**")
	for _, want := range []string{
		"the `rm -rf` moved above its two refusals in either Restore",
		"the marker refusal deleted from a guard",
		"hard stop 4 deleted",
		"the `repo-existed` baseline deleted",
	} {
		if !strings.Contains(red, want) {
			t.Errorf("C0.3's red step omits a mutation the guards it lists were proven with: it lacks %q", want)
		}
	}
}

// check90Driver loads the proxy program out of Check 90 step 3's fenced block
// and prints what its error reader makes of one body. Loading it runs nothing:
// the program's server starts only under `if __name__ == "__main__"`, which is
// what makes the reader reachable without a port, a network or a token.
const check90Driver = `import importlib.util, sys
spec = importlib.util.spec_from_file_location("chk90proxy", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
body = open(sys.argv[2], "rb").read()
print("FIELDS:", " | ".join(map(str, mod.error_fields(body))))
`

// TestChecklistCheck90ProxyPrintsOnlyTheErrorTypeAndMessage: step 7's FACT (U3)
// asked for "the error's own `type` (and from the body only its `type` and
// `message`)" while the program step 3 hands the owner printed header names and
// statuses and read no body at all, and the step supplied no command that could
// fetch one — a FACT the check's own tooling could not produce (the review of
// #584). U3 is not optional here: F15's consequence is that reactive exhaustion
// detection reads the HTTP response — status, **error type**, reset headers —
// so a check that cannot see the type cannot answer U3. Step 3's program now
// carries an opt-in `--error-fields`, and this test runs the reader out of the
// document the owner copies, on the four shapes a body can arrive in: only the
// error object's `type` and `message` may reach the output, a fixed placeholder
// stands in when there is no error object or the body is not JSON, and no other
// value — a credential-shaped one above all — may be printed.
//
// Mutation (run, restore): make `error_fields` return the whole parsed body,
// or read a third key (`request_id`), or drop the isinstance guard on `type`
// and print the nested value instead of the placeholder — the test fails on
// each.
func TestChecklistCheck90ProxyPrintsOnlyTheErrorTypeAndMessage(t *testing.T) {
	doc := readDoc(t, checklistPath)
	step3 := stepBlock(t, checklistPath, docSection(t, checklistPath, doc, chk90Heading), "### Check 90 step 3:")
	var src string
	for _, b := range fencedBlocks(step3) {
		if strings.Contains(b, "def fact(") {
			src = b
		}
	}
	if src == "" {
		t.Fatal("Check 90 step 3 no longer fences the proxy program")
	}
	for _, want := range []string{
		`ap.add_argument("--error-fields"`,
		"def error_fields(",
		"def read_capped(",
		"json.loads(",
		"resp.status >= 400",
		`if __name__ == "__main__":`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("Check 90's proxy no longer carries the bounded error-field reader step 7's FACT (U3) needs: it lacks %q", want)
		}
	}

	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH: the behavioural half of this test did not run")
	}
	dir := t.TempDir()
	proxy := filepath.Join(dir, "proxy.py")
	if err := os.WriteFile(proxy, []byte(src+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "driver.py")
	if err := os.WriteFile(driver, []byte(check90Driver), 0o600); err != nil {
		t.Fatal(err)
	}

	// The credential-shaped value a body might carry is the thing this reader
	// must never print; the key names beside it stand for the other keys of an
	// error object, which it must not read either.
	const credential = "not-a-real-credential-0123456789abcdef"
	for _, tc := range []struct{ name, body, want, wantMessage string }{
		{
			"an error object with other keys beside the two",
			`{"error":{"type":"rate_limit_error","message":"usage limit reached",` +
				`"access_token":"` + credential + `","request_id":"req_0000"}}`,
			"type: rate_limit_error", "message: usage limit reached",
		},
		{
			"a body that is not JSON at all",
			"access_token=" + credential,
			"(unreadable body: not JSON)", "",
		},
		{
			"valid JSON with no error object",
			`{"message":"usage limit reached","access_token":"` + credential + `"}`,
			"(no error object in the body)", "",
		},
		{
			"an error object whose type is not a string",
			`{"error":{"type":{"nested":"` + credential + `"}}}`,
			"type: (none)", "",
		},
	} {
		body := filepath.Join(dir, "body")
		if err := os.WriteFile(body, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		out, err := exec.CommandContext(ctx, py, driver, proxy, body).CombinedOutput()
		cancel()
		if err != nil {
			t.Errorf("%s: the program's error reader did not run (%v): %s", tc.name, err, out)
			continue
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("%s: the error reader's output does not carry %q: %s", tc.name, tc.want, out)
		}
		if tc.wantMessage != "" && !strings.Contains(string(out), tc.wantMessage) {
			t.Errorf("%s: the error reader's output does not carry the message, which is one of the two fields it may print: %s", tc.name, out)
		}
		for _, leaked := range []string{credential, "access_token", "request_id", "req_0000"} {
			if strings.Contains(string(out), leaked) {
				t.Errorf("%s: the error reader printed %q, which is not one of the two fields of an error object it may print: %s", tc.name, leaked, out)
			}
		}
	}
}

// TestChecklistCheck90StepSevenNamesTheProxyFlagThatRecordsTheErrorType: the
// other half of the same defect — the fix has to be reachable from the step
// that asks for the fact, and the documents that describe the check have to
// agree about it. Step 3 has to say what the flag prints and that nothing else
// of a body is ever printed, step 7 has to carry the command that turns it on,
// and the two places that promise what a FACT line may contain (the section's
// own "never a value" sentence and its paste-back line) have to say which two
// fields of a body are the exception — as does reconciliation's U3 row, which
// claims the check records the error type. The register's row and the section's
// summary already claim the type; this pins that the check can produce it.
//
// Mutation (run, restore): drop `--error-fields` from step 7's command, drop
// the flag from step 3's prose, or return the paste-back's "never a body" to
// its unqualified form — the test fails on each.
func TestChecklistCheck90StepSevenNamesTheProxyFlagThatRecordsTheErrorType(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk90Heading)
	step3 := stepBlock(t, checklistPath, check, "### Check 90 step 3:")
	for _, want := range []string{
		"`--error-fields`",
		"never a header value",
		"prints the error object's own `type` and `message`",
		"an **error** response (status ≥ 400)",
	} {
		if !strings.Contains(step3, want) {
			t.Errorf("Check 90's step 3 does not say what its opt-in error-field flag prints: it lacks %q", want)
		}
	}
	step7 := stepBlock(t, checklistPath, check, "### Check 90 step 7:")
	for _, want := range []string{
		"--error-fields",
		"the error's own `type` and `message`",
		"(no error object in the body)",
		"(unreadable body: not JSON)",
	} {
		if !strings.Contains(step7, want) {
			t.Errorf("Check 90's step 7 does not carry the way to record the error's own type, which its FACT (U3) asks for: it lacks %q", want)
		}
	}
	// The flag has to be in the command the operator types, not only in the
	// prose around it: a step that describes a flag its command does not pass
	// leaves the FACT unobtainable, which is the defect this pins.
	var turnsItOn bool
	for _, b := range fencedBlocks(step7) {
		if strings.Contains(b, "proxy.py") && strings.Contains(b, "--error-fields") {
			turnsItOn = true
		}
	}
	if !turnsItOn {
		t.Error("Check 90's step 7 never starts the proxy with --error-fields, so the command it gives cannot produce the error type its FACT (U3) asks for")
	}
	// The two sentences that promise what a FACT line may contain, each read
	// where it stands: an unqualified "never a value" or "never a body" is what
	// hid the gap, because step 7's FACT asked for a body's fields anyway.
	for _, want := range []string{
		"the only body content anywhere in it is step 7's error `type` and `message`",
		"step 7's `type` and `message` are the only body fields the record carries",
	} {
		if !strings.Contains(check, want) {
			t.Errorf("Check 90's own prose does not bound what a FACT line may take from a body: it lacks %q", want)
		}
	}
	row := docTail(t, checklistPath, doc, "| 90 | The OAuth header swap")
	row = row[:strings.Index(row, "\n")]
	if !strings.Contains(row, "the error object's own `type` and `message`") {
		t.Error("the register's row for Check 90 does not say the check records the error object's own type and message, which is the half of U3 the flag exists for")
	}
	if !strings.Contains(check, "the proxy's opt-in `--error-fields`, which reads those two keys and no other") {
		t.Error("Check 90's summary does not say where the error type it claims to observe comes from")
	}
	recon := readDoc(t, "docs/credentials-service/reconciliation.md")
	u3 := docTail(t, "docs/credentials-service/reconciliation.md", recon, "| U3 | Which response status, error type and headers carry")
	u3 = u3[:strings.Index(u3, "\n")]
	for _, want := range []string{"step 7 records the status", "the error object's own `type` and `message`", "--error-fields"} {
		if !strings.Contains(u3, want) {
			t.Errorf("reconciliation's U3 row does not say how Check 90 records the error type: it lacks %q", want)
		}
	}
}

// gcloudCommands returns every `gcloud` invocation in a section's fenced
// blocks, one per occurrence, as the command from `gcloud` to the end of the
// line it stands on. The checklist hard-wraps prose but not code, so a command
// broken over several lines with backslashes is one line per segment and a flag
// on a continuation line lands in the segment of the `gcloud` above it.
func gcloudCommands(section string) []string {
	var out []string
	re := regexp.MustCompile(`\bgcloud\b`)
	for _, b := range fencedBlocks(section) {
		for _, line := range strings.Split(b, "\n") {
			// A comment is not a command the owner types, and the register's
			// own "never `gcloud config get-value project`" warnings are
			// comments: only what is run has to name the project.
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			// ... and neither is a trailing comment, where the register's own
			// "never `gcloud config get-value project`" notes live.
			hash := strings.Index(line, "#")
			for _, loc := range re.FindAllStringIndex(line, -1) {
				if hash != -1 && loc[0] > hash {
					continue
				}
				out = append(out, line[loc[0]:])
			}
		}
	}
	return out
}

// fencedText is every fenced block of a section, joined: the code the owner
// copies, with the prose that explains it excluded, so an assertion about a
// command cannot be satisfied by a sentence about the command.
func fencedText(section string) string {
	return strings.Join(fencedBlocks(section), "\n")
}

// TestChecklistCheck91CommandsPassTheProjectExplicitly: Check 91 deploys a
// service, pushes an image and deletes both in a Google Cloud project, and the
// owner's gcloud has a default project that is very often another installation
// (EdgeWeb's or EdgeServer's). A command without `--project` therefore runs
// against whatever the default is: it can create a Cloud Run service and a
// Registry repository in the wrong project, and the Restore's deletes can miss
// what it made and remove something else. Every command names the project from
// the guard's own `$P`, and `$P` is `fugaro-dev` — hard stop 1 refuses anything
// else, hard stop 2 refuses a checkout that is not that installation — so the
// only project any command of this check can name is the owner's own.
// `print-identity-token` is the one exception: it mints a token for the caller,
// and a token is not scoped to a project.
//
// Mutation (run, restore): delete `--project="${P:?}"` from the build, the
// deploy, an image delete or a list, change the guard's `P=fugaro-dev` to
// `P=belong`, or drop hard stop 1 — the test fails on each.
func TestChecklistCheck91CommandsPassTheProjectExplicitly(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	cmds := gcloudCommands(check)
	if len(cmds) < 10 {
		t.Fatalf("Check 91's blocks carry only %d gcloud invocations, which is fewer than the commands this check needs; the section was cut", len(cmds))
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "gcloud auth print-identity-token") {
			continue
		}
		if !strings.Contains(cmd, "--project=") {
			t.Errorf("a command of Check 91 does not name its project, so it runs against the owner's default (which may be another installation): %s", cmd)
		}
		// Naming a project is not enough: it has to be the one the guard pinned,
		// or a literal that is not fugaro-dev would satisfy the check above.
		if !strings.Contains(cmd, `--project="${P:?}"`) && !strings.Contains(cmd, "--project=${P}") {
			t.Errorf("a command of Check 91 names a project other than the guard's own $P: %s", cmd)
		}
	}
	guard := stepBlock(t, checklistPath, check, "### Check 91 step 1: setup and guard")
	flat := docFlat(guard)
	for _, want := range []string{
		"P=fugaro-dev",
		`[ "$P" = fugaro-dev ]`,
		`fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,'`,
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 91's guard does not pin the project to the fugaro-dev installation: it lacks %q", want)
		}
	}
	// Hard stop 2 is what makes the check testable against the right
	// installation at all: without it a checkout of another installation runs
	// the check against its own default project.
	if !strings.Contains(flat, "not another one") {
		t.Error("Check 91's guard does not say hard stop 2 refuses another installation's checkout")
	}
}

// TestChecklistChecks90And91NeverNameTheOtherInstallations: the owner's
// instruction is that these checks never touch the belong installation, EdgeWeb
// or EdgeServer. The guard runs on the machine that has all three, so a command
// or a path naming one of them is a live action against a system this work was
// told to leave alone. In the code the owner copies they may not appear at all;
// in the prose they may appear only on a line that says the check never touches
// them, which is what the register's warning lines are.
//
// Mutation (run, restore): change `P=fugaro-dev` to `P=belong`, point an image
// tag at `edgeweb-docker.pkg.dev`, or drop a "never" from the sentence that
// names them — the test fails on each.
func TestChecklistChecks90And91NeverNameTheOtherInstallations(t *testing.T) {
	doc := readDoc(t, checklistPath)
	for _, heading := range []string{chk90Heading, chk91Heading} {
		check := docSection(t, checklistPath, doc, heading)
		code := strings.ToLower(fencedText(check))
		for _, name := range []string{`\bbelong\b`, `\bedgeweb\b`, `\bedgeserver\b`} {
			if regexp.MustCompile(name).MatchString(code) {
				t.Errorf("%s's code blocks name %s, which this work may never touch", heading, name)
			}
		}
		for _, line := range strings.Split(check, "\n") {
			lower := strings.ToLower(line)
			for _, name := range []string{`\bbelong\b`, `\bedgeweb\b`, `\bedgeserver\b`} {
				if !regexp.MustCompile(name).MatchString(lower) {
					continue
				}
				if !strings.Contains(lower, "never") && !strings.Contains(lower, "nowhere") {
					t.Errorf("%s mentions %s without saying it is never touched: %q", heading, name, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestChecklistCheck91ServesAuthenticatedCallsOnly: the check deploys a public
// Cloud Run service that streams for the better part of an hour and answers
// arbitrary request bodies. `--allow-unauthenticated` would make that endpoint
// reachable by anyone who learns the URL — the owner is billed for the
// requests, and the relay would run for them — while the point of the check is
// what the owner's own authenticated request does. The deploy has to pass
// `--no-allow-unauthenticated`, and every request carries a freshly minted
// identity token.
//
// Mutation (run, restore): change `--no-allow-unauthenticated` to
// `--allow-unauthenticated`, or drop `Authorization: Bearer $(idt)` from a
// probe — the test fails on each.
func TestChecklistCheck91ServesAuthenticatedCallsOnly(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	flat := docFlat(check)
	lower := strings.ToLower(flat)
	for i := 0; ; {
		j := strings.Index(lower[i:], "allow-unauthenticated")
		if j == -1 {
			break
		}
		k := i + j
		if k < 3 || lower[k-3:k] != "no-" {
			t.Fatalf("Check 91 says allow-unauthenticated without the `no-`, so the service it deploys is open to anyone: %q", flat[k-40:min(k+40, len(flat))])
		}
		i = k + 1
	}
	build := stepBlock(t, checklistPath, check, "### Check 91 step 3:")
	if !strings.Contains(build, "--no-allow-unauthenticated") {
		t.Error("Check 91's deploy does not pass --no-allow-unauthenticated, so the throwaway service it leaves running for an hour is public")
	}
	probes := docTail(t, checklistPath, check, "### Check 91 step 4:")
	if strings.Count(probes, "Authorization: Bearer $(idt)") < 2 {
		t.Error("Check 91's probes do not carry a freshly minted identity token, and its deploy forbids unauthenticated calls: the requests would be rejected before they measure anything")
	}
}

// TestChecklistChecks90And91PrintNoCredentialValue: what the owner types is
// pasted back into the record, so a command that prints a token prints it into
// the record. Check 90 reads its setup-token from a file and Check 91 mints
// identity tokens; neither value may reach stdout — only the file's length and
// mode, and for Check 91 the commands that mint a token discard it.
//
// Mutation (run, restore): add `echo "$TOKEN"` to Check 90's steps, or make a
// Check 91 command print the identity token (`gcloud auth print-identity-token`
// with its output kept) — the test fails on each.
func TestChecklistChecks90And91PrintNoCredentialValue(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check90 := docSection(t, checklistPath, doc, chk90Heading)
	// In Check 90 the two names a value can hide behind are the file and the
	// variable the reader types the value into; no line that prints may name
	// either. (TOKEN_FILE's length and mode are printed, which is why the rule
	// is about printing TOKEN, not about naming TOKEN_FILE.)
	prints := regexp.MustCompile(`(^|\s)(echo|printf|print)\b`)
	// Read one command at a time: step 2's `printf '%s' "$TOKEN" > "$TOKEN_FILE"`
	// sends the value to a file and is the mechanism the check is built on, while
	// a print of its own in the same line — `read ... && echo "$TOKEN" && printf
	// ... > "$TOKEN_FILE"` — is the defect. The redirect has to be in the command
	// that does the printing.
	segments := regexp.MustCompile(`[;&]`)
	toTerminal := func(line string) bool {
		for _, seg := range segments.Split(line, -1) {
			if !prints.MatchString(seg) || !regexp.MustCompile(`\bTOKEN\b`).MatchString(seg) {
				continue
			}
			if !strings.Contains(seg, ">") {
				return true
			}
			for _, leak := range []string{"/dev/stdout", "/dev/tty", ">&1", "| tee", "| cat"} {
				if strings.Contains(seg, leak) {
					return true
				}
			}
		}
		return false
	}
	for _, b := range fencedBlocks(check90) {
		for _, line := range strings.Split(b, "\n") {
			if toTerminal(line) {
				t.Errorf("Check 90 prints the setup-token: %q", strings.TrimSpace(line))
			}
		}
	}
	if !strings.Contains(check90, "Never paste the token") {
		t.Error("Check 90 no longer tells the reader that the token's value is never pasted back")
	}
	check91 := docSection(t, checklistPath, doc, chk91Heading)
	mintsToTerminal := regexp.MustCompile(`\b(echo|printf)\b[^;|&]*\$\(idt\)`)
	for _, b := range fencedBlocks(check91) {
		for _, line := range strings.Split(b, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			// Only the guard's own helper may mint a token without discarding
			// it: anywhere else the minted value would land on the terminal, in
			// the record.
			if strings.Contains(line, "print-identity-token") &&
				!strings.Contains(line, "idt()") && !strings.Contains(line, "/dev/null") {
				t.Errorf("Check 91 mints an identity token outside its own helper, so a command can print it: %q", trimmed)
			}
			if mintsToTerminal.MatchString(line) {
				t.Errorf("Check 91 prints an identity token: %q", trimmed)
			}
		}
	}
	if !strings.Contains(check91, "Never paste the identity token") {
		t.Error("Check 91 no longer tells the reader that a minted token is never pasted back")
	}
}

// TestChecklistCheck90ProxyRefusesEveryHostButItsOwnLoopback: the proxy listens
// on loopback, which is reachable from any page the owner's browser loads —
// with no Host check a page can spend the owner's subscription quota through
// the proxy and, via DNS rebinding, read the responses it relays. It serves
// only the client's own address and port, refuses anything else with a 403
// before reading a body or opening an upstream connection, and says so where
// the owner reads what the proxy does. The two decisions are pure functions, so
// the test runs them out of the document itself.
//
// Mutation (run, restore): drop the Host check from `forward`, accept any host
// in `loopback_host`, or move the refusal below `HTTPSConnection` — the test
// fails on each.
func TestChecklistCheck90ProxyRefusesEveryHostButItsOwnLoopback(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk90Heading)
	var src string
	for _, b := range fencedBlocks(stepBlock(t, checklistPath, check, "### Check 90 step 3:")) {
		if strings.Contains(b, "def fact(") {
			src = b
		}
	}
	if src == "" {
		t.Fatal("Check 90 step 3 no longer fences the proxy program")
	}
	// The refusal stands between the Host check and everything else the handler
	// does, and it is a refusal: the request is not forwarded.
	host := strings.Index(src, `loopback_host(self.headers.get("Host")`)
	refuse := strings.Index(src, `self.send_error(403,`)
	upstream := strings.Index(src, "HTTPSConnection(")
	if host == -1 {
		t.Fatal("Check 90's proxy forwards whatever Host it is sent, so any page the owner's browser loads can spend their quota through it")
	}
	if refuse == -1 || upstream == -1 || refuse < host || refuse > upstream {
		t.Error("Check 90's proxy does not refuse a foreign Host before it opens the upstream connection")
	}
	// ... and the listener is loopback and nothing else: a proxy bound to every
	// interface is reachable from the network the machine is on, whatever the
	// Host check says.
	if !strings.Contains(src, `("127.0.0.1", ARGS.port)`) {
		t.Error("Check 90's proxy does not bind its listener to 127.0.0.1, so the port is reachable from the network the machine is on")
	}
	if strings.Contains(src, "0.0.0.0") {
		t.Error("Check 90's proxy names 0.0.0.0, which binds every interface rather than loopback")
	}
	step3 := docFlat(stepBlock(t, checklistPath, check, "### Check 90 step 3:"))
	for _, want := range []string{
		"refused with 403 and not forwarded",
		"`Host`",
		"DNS-rebinding",
		"records **by name** which credential header the client sent",
	} {
		if !strings.Contains(step3, want) {
			t.Errorf("Check 90's step 3 does not say what its proxy does about the Host header: it lacks %q", want)
		}
	}

	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH: the behavioural half of this test did not run")
	}
	dir := t.TempDir()
	proxy := filepath.Join(dir, "proxy.py")
	if err := os.WriteFile(proxy, []byte(src+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "driver.py")
	if err := os.WriteFile(driver, []byte(check90HostDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	out, err := exec.CommandContext(ctx, py, driver, proxy).CombinedOutput()
	cancel()
	if err != nil {
		t.Fatalf("the proxy program's Host decision did not run (%v): %s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		// Its own listener, in the spellings a client may use.
		"HOST 127.0.0.1:8787 true",
		"HOST localhost:8787 true",
		"HOST [::1]:8787 true",
		// ... and nothing else: another port, another host, no port, a host that
		// merely ends in the right one, and a rebinding-style host.
		"HOST 127.0.0.1:8788 false",
		"HOST evil.example:8787 false",
		"HOST (empty) false",
		"HOST 127.0.0.1 false",
		"HOST 127.0.0.1:8787.evil.example false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the proxy's Host decision is wrong for %q; it printed:\n%s", want, got)
		}
	}
}

// TestChecklistCheck90ProxyRecordsTheCredentialHeaderName: clearing the
// environment is not evidence about what the client sent — a Vertex or
// apiKeyHelper configuration can be outside it — so the proxy records the NAME
// of the credential header each request actually carried, and never a value.
// The control's line says whether the swap ran on the setup-token at all: an
// `x-api-key` or `(neither)` line means it measured something else.
//
// Mutation (run, restore): drop the `FACT credential-header` line, print the
// header's value instead of its name, or drop it from the two sentences that
// say what a FACT line may carry — the test fails on each.
func TestChecklistCheck90ProxyRecordsTheCredentialHeaderName(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk90Heading)
	var src string
	for _, b := range fencedBlocks(stepBlock(t, checklistPath, check, "### Check 90 step 3:")) {
		if strings.Contains(b, "def fact(") {
			src = b
		}
	}
	if src == "" {
		t.Fatal("Check 90 step 3 no longer fences the proxy program")
	}
	if !strings.Contains(src, `"credential-header"`) || !strings.Contains(src, "credential_headers(self.headers)") {
		t.Error("Check 90's proxy does not record which credential header the request carried, so the control proves nothing about what the client sent")
	}
	if !strings.Contains(src, "never a value") {
		t.Error("Check 90's proxy does not say that the credential-header line is a name and never a value")
	}
	for _, want := range []string{
		"FACT credential-header",
		"(neither)",
		"x-api-key",
	} {
		if !strings.Contains(docFlat(check), want) {
			t.Errorf("Check 90 does not read the proxy's credential-header line where the owner decides the control passed: it lacks %q", want)
		}
	}
	// The line is one of the two facts a step has to carry, so it is pinned in
	// the step's own FACT and not only in the prose around it.
	step4 := stepBlock(t, checklistPath, check, "### Check 90 step 4:")
	if !strings.Contains(step4, "FACT credential-header") {
		t.Error("Check 90's step 4 FACT does not ask for the credential-header line, so an API-key or Vertex control and a setup-token one read the same")
	}

	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH: the behavioural half of this test did not run")
	}
	dir := t.TempDir()
	proxy := filepath.Join(dir, "proxy.py")
	if err := os.WriteFile(proxy, []byte(src+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "driver.py")
	if err := os.WriteFile(driver, []byte(check90HostDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	out, err := exec.CommandContext(ctx, py, driver, proxy).CombinedOutput()
	cancel()
	if err != nil {
		t.Fatalf("the proxy program's credential-header decision did not run (%v): %s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"CRED Authorization authorization",
		"CRED x-api-key x-api-key",
		"CRED both authorization,x-api-key",
		"CRED neither (neither)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the proxy's credential-header decision is wrong for %q; it printed:\n%s", want, got)
		}
	}
	// The names, and no value: the driver sends "Bearer chk90-name-only", and a
	// program that echoed a value would put it in the output this test reads.
	if strings.Contains(got, "chk90-name-only") {
		t.Error("the proxy's credential-header line carries a value, not only a name")
	}
}

// check90HostDriver loads the proxy program out of Check 90 step 3's fenced
// block and prints its two pre-forward decisions: whether a Host header is this
// check's own listener, and which credential header a request carried (by NAME,
// never by value). Loading runs nothing — the server starts only under
// `if __name__ == "__main__"` — so both are reachable without a port, a network
// or a token.
const check90HostDriver = `import email.message, importlib.util, sys
spec = importlib.util.spec_from_file_location("chk90proxy", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
print("HOST (empty) " + str(mod.loopback_host(None, 8787)).lower())
for host in ["127.0.0.1:8787", "localhost:8787", "[::1]:8787", "127.0.0.1:8788",
             "evil.example:8787", "127.0.0.1", "127.0.0.1:8787.evil.example"]:
    print("HOST " + host + " " + str(mod.loopback_host(host, 8787)).lower())
def headers(**kw):
    m = email.message.Message()
    for k, v in kw.items():
        m[k] = v
    return m
for name, m in [("Authorization", headers(Authorization="Bearer chk90-name-only")),
                ("x-api-key", headers(**{"x-api-key": "chk90-name-only"})),
                ("both", headers(Authorization="Bearer chk90-name-only",
                                 **{"x-api-key": "chk90-name-only"})),
                ("neither", headers(**{"content-type": "application/json"}))]:
    print("CRED " + name + " " + (",".join(mod.credential_headers(m)) or "(neither)"))
`

// TestChecklistCheck91RestoreKeepsItsRecordsUntilTheCountsAreZero: the scratch
// directory holds `service-mine` and `repo-existed`, the two records that say
// the service and the repository are this check's, and both hard stop 4 and the
// baseline refuse to touch a resource they do not recognise. So a Restore that
// removes the directory while something it created is still there (a delete
// that failed, a service still terminating) is a Restore that turns one failed
// delete into a check that can never be finished: the next run's stop and stop 4
// both fire on the check's own leftovers. The directory goes only when all three
// counts read 0.
//
// Mutation (run, restore): move `rm -rf "$CHK91"` above the count guard, or
// delete the guard — the test fails on each.
func TestChecklistCheck91RestoreKeepsItsRecordsUntilTheCountsAreZero(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	restore := stepBlock(t, checklistPath, check, "### Check 91 step 7: Restore")
	var counts string
	if i := strings.Index(restore, "SVC_LEFT="); i != -1 {
		counts = restore[i:]
	}
	if counts == "" {
		t.Fatal("Check 91's Restore counts nothing, so it cannot know whether what it created is gone")
	}
	for _, want := range []string{
		"SVC_LEFT=", "IMG_LEFT=", "REPO_LEFT=",
		`if [ "$SVC_LEFT" != 0 ] || [ "$IMG_LEFT" != 0 ] || [ "$REPO_LEFT" != 0 ]; then`,
		`KEPT on purpose`,
	} {
		if !strings.Contains(counts, want) {
			t.Errorf("Check 91's Restore does not keep its records unless every count is 0: it lacks %q", want)
		}
	}
	guard := strings.Index(counts, `if [ "$SVC_LEFT" != 0 ]`)
	rmrf := strings.Index(counts, `rm -rf "$CHK91"`)
	record := strings.Index(counts, `rm -f "$CHK91_RECORD"`)
	if rmrf == -1 || record == -1 {
		t.Fatalf("Check 91's Restore does not remove the scratch directory and its record (rm=%d, record=%d)", rmrf, record)
	}
	if guard == -1 || guard > rmrf || guard > record {
		t.Error("Check 91's Restore removes the scratch directory (or the record naming it) before checking that the three counts are 0, so a failed delete loses the records that say what is the check's own")
	}
	// ... and what it means is written where the owner reads it, in the FACT and
	// in what counts as a failure.
	flat := docFlat(check)
	if !strings.Contains(flat, "it holds the records that say what is this check's") {
		t.Error("Check 91's Restore does not say why a non-zero count keeps the scratch directory, so a reader deletes it and cannot re-run the step")
	}
	if !strings.Contains(flat, "the directory is then **kept**, with the stop's message saying so") {
		t.Error("Check 91's FACT does not say that the scratch directory is kept when a count is not 0")
	}
}

// TestChecklistCheck91BuildDoesNotAdoptWhatTheInstallationOwns: `gcloud builds
// submit` stages its source in the project's own `gs://<project>_cloudbuild`
// bucket and creates it if it is absent, and on a Terraform-managed project it
// can offer to enable an API. Both belong to the installation — neither is in
// this project's Terraform — so accepting the prompt would put an
// unmanaged resource into a managed project, which is a change to live
// infrastructure that no check of this work may make. The step says to refuse,
// stop and report.
//
// Mutation (run, restore): delete that paragraph, or weaken it to "accept if it
// asks" — the test fails on each.
func TestChecklistCheck91BuildDoesNotAdoptWhatTheInstallationOwns(t *testing.T) {
	doc := readDoc(t, checklistPath)
	check := docSection(t, checklistPath, doc, chk91Heading)
	flat := docFlat(check)
	for _, want := range []string{
		"`gs://<project>_cloudbuild`",
		"creates that bucket if it is not there",
		"Terraform-managed",
		"say no, stop the check here and report it",
		"do not accept on the owner's behalf",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 91 does not say what to do when the build offers to create the staging bucket or enable an API: it lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(flat), "accept the prompt") {
		t.Error("Check 91 tells the owner to accept a prompt that would create infrastructure outside Terraform")
	}
}

// TestChecklistChecksSayTheirHardStopsEndTheShell: both checks are pasted into
// an interactive shell and stop with `exit 1`, which closes it. That is
// deliberate — nothing after a stop may run — but a reader who is not told
// reads the closed window as a fault and works around it, and a "workaround"
// here means running the steps after a stop. Both sections carry Check 43's
// paragraphs and the subshell that keeps the window while still stopping the
// block.
//
// Mutation (run, restore): delete either paragraph, or the subshell sentence —
// the test fails on each.
func TestChecklistChecksSayTheirHardStopsEndTheShell(t *testing.T) {
	doc := readDoc(t, checklistPath)
	for _, heading := range []string{chk90Heading, chk91Heading} {
		check := docFlat(docSection(t, checklistPath, doc, heading))
		for _, want := range []string{
			"Hard stops end the shell they are run in, and that is the intended outcome.",
			"exit 1",
			"closes the window",
			"Nothing after a stop may run",
		} {
			if !strings.Contains(check, want) {
				t.Errorf("%s does not tell the reader what a hard stop does to the shell, so a closed window reads as a fault and the steps after the stop get run: it lacks %q", heading, want)
			}
		}
		if !strings.Contains(check, "subshell") {
			t.Errorf("%s gives the reader no way to keep the window while still stopping the block", heading)
		}
	}
	if !strings.Contains(docSection(t, checklistPath, doc, chk90Heading), "( . ./chk90-env.sh )") {
		t.Error("Check 90 does not name the subshell form of its own guard, so the reader who wants to keep the window has to invent one")
	}
	if !strings.Contains(docFlat(docSection(t, checklistPath, doc, chk91Heading)), "( . ./chk91-env.sh )") {
		t.Error("Check 91 does not name the subshell form of its own guard, so the reader who wants to keep the window has to invent one")
	}
}

// TestTheStopgapIsStatedWithoutAPromisedSlot: the review of #584 found the
// documents contradicting themselves about the shared job's static token mount.
// §7 of project-centric.md said it was accepted "precisely because the service
// replaces it" and, in the next sentence, that the service cannot replace it —
// and SECURITY.md and project-named-oauth-tokens.md still promised the
// replacement, while the task that would do it (C3.9) has no slot in any plan.
// A promise nothing is scheduled to keep is what makes a known stopgap read as
// already handled. All four documents now say what is true: the mount is a
// stopgap accepted on its own terms, the service as its plan is written cannot
// replace it, and it stays until C3.9 is scheduled — with no slot promised.
//
// Mutation (run, restore): restore "precisely because the service replaces it"
// to §7, drop "unscheduled" from the service's constraint, or drop "no slot"
// from any of the four — the test fails on each.
func TestTheStopgapIsStatedWithoutAPromisedSlot(t *testing.T) {
	// project-centric.md §7, where the contradiction was.
	const section7 = "docs/design/project-centric.md"
	doc := readDoc(t, section7)
	sec := docFlat(docSection(t, section7, doc, "## 7. OAuth tokens (phase 5)"))
	for _, want := range []string{
		"the owner's ruling of 2026-10-18",
		"not a replacement date: the credentials service is where this ends, and that work has no slot",
		"**The service cannot replace this stopgap as its plan is written:**",
		"unscheduled",
		"it serves **per-repository runs only**",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("project-centric.md §7 does not settle what happens to the shared job's token mount: it lacks %q", want)
		}
	}
	for _, gone := range []string{
		"precisely because the service replaces it",
		"stays until then",
	} {
		if strings.Contains(sec, gone) {
			t.Errorf("project-centric.md §7 still promises a replacement for the shared job's token mount (%q), which no scheduled work can keep", gone)
		}
	}

	// SECURITY.md: the carrier bullet, which is where a reader learns what
	// protects a shared job's token today.
	sec2 := docFlat(readDoc(t, "SECURITY.md"))
	for _, want := range []string{
		"This carrier is a stopgap, and it stays until the service is built for shared jobs.",
		"That destination is not a schedule, and no version promises it",
		"serves **per-repository runs only**",
		"is **unscheduled**",
		"it is accepted for its lesser exposure on its own terms rather than against a replacement date",
		"docs/credentials-service/README.md",
	} {
		if !strings.Contains(sec2, want) {
			t.Errorf("SECURITY.md's shared-job carrier bullet does not state the stopgap honestly: it lacks %q", want)
		}
	}

	// The design note that owns the swap: §4.6 is the section that would take
	// the mount away, and it says so without a slot — the sentence a reader
	// takes as the plan for the swap.
	const tokens = "docs/design/project-named-oauth-tokens.md"
	noteDoc := readDoc(t, tokens)
	note := docFlat(noteDoc)
	for _, want := range []string{
		"**The swap has no slot.**",
		"unscheduled",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("%s does not say that the work which would replace the mount has no slot: it lacks %q", tokens, want)
		}
	}
	// ... and §6.1's own line, which is where the mount's interim character is
	// stated beside the tasks that are still planned.
	sec61 := docTail(t, tokens, noteDoc, "### 6.1")
	if i := strings.Index(sec61, "\n### "); i != -1 {
		sec61 = sec61[:i]
	}
	if !strings.Contains(docFlat(sec61), "has no slot") {
		t.Errorf("%s §6.1 does not say that the swap it plans has no slot", tokens)
	}

	// ... and the service's own plan, where a reader looks for the phase that
	// does it: its Global Constraint 6 is the constraint the other three cite.
	plan := docFlat(readDoc(t, "docs/credentials-service/implementation-plan.md"))
	for _, want := range []string{
		"the mount stays until C3.9 is given a slot, and no slot is promised",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("the implementation plan does not state the constraint the other documents cite: it lacks %q", want)
		}
	}
}

// TestCredentialsNumbersReserveNoDecisionWithoutTheOwnersWord: the register's
// number blocks are the owner's to allocate, and this work's own text had
// claimed D155–D159 — five numbers nobody gave it. They are left free, and both
// places that describe the blocks say which numbers the owner allocated and that
// these five are this work's own self-reservation, so a later task cannot read
// them as allocated-and-unused, as the register's 93 is.
//
// Mutation (run, restore): say the owner allocated D130–D159, drop the sentence
// that the owner has given no word for these five, or claim one of them —
// the test fails on each.
func TestCredentialsNumbersReserveNoDecisionWithoutTheOwnersWord(t *testing.T) {
	const readme = "docs/credentials-service/README.md"
	readmeFlat := docFlat(readDoc(t, readme))
	for _, want := range []string{
		"the owner allocated **D130–D154**",
		"**D155–D159 stand only in this work's own text: this document reserved them by itself, the owner has given no word for them, and no task here uses one.**",
	} {
		if !strings.Contains(readmeFlat, want) {
			t.Errorf("the credentials service README does not say who allocated the decision numbers it claims: it lacks %q", want)
		}
	}
	if strings.Contains(readmeFlat, "the owner allocated **D130–D159**") {
		t.Error("the credentials service README claims the owner allocated D155–D159, which the owner never gave")
	}
	const recon = "docs/credentials-service/reconciliation.md"
	reconFlat := docFlat(readDoc(t, recon))
	for _, want := range []string{
		"D130–D154 were allocated and are answered",
		"D155–D159 are unused because this work's own text reserved them, not because the owner allocated them",
	} {
		if !strings.Contains(reconFlat, want) {
			t.Errorf("reconciliation.md does not distinguish the numbers the owner allocated from the ones this work reserved by itself: it lacks %q", want)
		}
	}
}
