package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// Checks 94, 95 and 99 are the credentials service's owner-run live checks
// (plan Task C1.23): the service deployed, a run through it, and the cascade
// end to end. They are written as steps the owner follows rather than as a
// script, and until this file landed nothing read them: every neighbouring
// check is pinned by a test of its own (81, 82 and 89 in
// docs_mobile_checks_test.go and docs_managed_deployer_test.go, 68 in the
// latter, 90 and 91 in docs_credentials_service_checks_test.go), and the three
// that were not carried three defects to review -- a policy read at the wrong
// level of the document, a kill-switch pattern the renderer can never print,
// and a `credential` read off result.json, where the field is `oauth_account`.
//
// So this file is two things. The first is the register's own shape: each
// number has one row and one section, the row and the section say the check is
// the owner's and that it has not been run, and the section says plainly what
// is not observable on this tree -- the service does not serve and the
// issuance audit is not wired, so the owner is never asked to run a step that
// cannot pass.
//
// The second is the part the review found, and it is a tie rather than a
// spelling: every read a check makes must be a read of something the code
// actually prints. `project policy show --json` prints {"project": ...,
// "policy": ...} and nothing at the top level, so a check that reads `routing`
// there reads null and its own pass condition becomes unobservable;
// `budget show` renders an open kill switch as `ON since ... by ...`, so a
// pattern spelled in lower case matches nothing and a "0 kill switches" line
// is vacuous; and the cascade's credential name lands in result.json's
// `oauth_account` (internal/runner's recordCredential, F26/D140), not in a
// top-level `credential`, which is the registry entry's field. Each of the
// three is asserted against the code here -- two of them by calling it or
// reflecting over it, so a rename moves this test rather than the check.
const (
	check94Heading = "## Check 94: the credentials service deployed"
	check95Heading = "## Check 95: a run through the service"
	check99Heading = "## Check 99: the cascade end to end through the service"
)

// TestDocsChecklistHasChecks94And95And99: the three numbers are written, each
// as one row and one section, each owner-run and NOT RUN, and the register's
// own allocation paragraph says which task wrote them and which numbers stay
// empty.
func TestDocsChecklistHasChecks94And95And99(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	rows := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		for _, n := range []string{"| 94 |", "| 95 |", "| 99 |"} {
			if strings.HasPrefix(line, n) {
				if rows[n] != "" {
					t.Errorf("%s has more than one %q row: one check, one row", path, strings.Trim(n, "| "))
				}
				rows[n] = line
			}
		}
	}
	for _, n := range []string{"| 94 |", "| 95 |", "| 99 |"} {
		row := rows[n]
		if row == "" {
			t.Errorf("%s has no %q row: a section without a row is a check nobody reads first", path, strings.Trim(n, "| "))
			continue
		}
		for _, want := range []string{
			"C1.23", // which task wrote it
			"credentials-service/implementation-plan.md", // the plan that assigns the number
			"never `belong`",    // the installation the check may not touch
			"USER-RUN, NOT RUN", // the owner runs it, and it has not been run
		} {
			if !strings.Contains(row, want) {
				t.Errorf("%s's %q row does not carry %q: the row states the task, the plan, the installation and that the check is the owner's and unrun", path, strings.Trim(n, "| "), want)
			}
		}
	}

	for _, heading := range []string{check94Heading, check95Heading, check99Heading} {
		sec := registerSection(t, doc, heading)
		if sec == heading {
			t.Errorf("%s's section at %q is empty: a row that points at no steps cannot be followed", path, heading)
			continue
		}
		for _, want := range []string{
			"Task C1.23",
			"never `belong`",
			"run by you",
			"not observable",
		} {
			if !strings.Contains(sec, want) {
				t.Errorf("%s's section at %q does not carry %q: every one of the three is the owner's, refuses belong, and says which steps cannot be observed on this tree", path, heading, want)
			}
		}
	}

	// The register's own allocation paragraph: Task C1.23 wrote 94, 95 and 99,
	// and 93 and 96-98 are what stays empty.
	for _, want := range []string{
		"by that plan's Task C1.23",
		"**90, 91, 92, 94, 95 and 99 are written below**",
		"**96 to 98**",
		"**93 is reserved and unused:**",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not carry %q: the register says which task wrote 94, 95 and 99 and which numbers stay reserved and unwritten", path, want)
		}
	}
}

// TestCheck94KeepsItsPromises: the deployed service, read off IAM and read off
// the audit config, plus the two things the check temporarily changes and puts
// back -- and the kill-switch assertion, which is the one the review found.
func TestCheck94KeepsItsPromises(t *testing.T) {
	doc := readDoc(t, "docs/gcp-live-checklist.md")
	sec := registerSection(t, doc, check94Heading)

	for _, want := range []string{
		"fugaro init --provision-credentials", // the owner-run init stage this check reads
		"fugaro init --repo",                  // the repository root that loses the mounts
		"gcp.CredsBundleSecretID",             // the one container, by the name the code gives it
		"roles/secretmanager.secretAccessor",  // the one grant on the one container (Q6)
		"roles/run.invoker",                   // what every workflow job gains instead of the key
		"secretmanager.googleapis.com",        // the service the DATA_READ config is about
		"gcloud projects get-iam-policy",      // the config read back BEFORE the log is read
		"gcloud projects set-iam-policy",
		"AccessSecretVersion",        // the entry the log is looked for
		"fugaro budget kill --all",   // the stop flag the check raises
		"fugaro budget resume --all", // ... and clears again, which the Restore repeats
		"no entry observed",          // why the audit entry is not observable on this tree
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("%s's Check 94 section does not carry %q: the check reads the one bundle container, its one grant and the job half, and turns DATA_READ on with the config read back first", "docs/gcp-live-checklist.md", want)
		}
	}

	// The kill-switch read. `budget show` prints "  kill switch: OFF-ish" never:
	// killText renders an open switch as "ON since ... by ..." and a closed one
	// as "off", so a pattern in lower case matches nothing at all and the
	// assertion passes on a project whose kill switch is still on -- the one
	// thing this check must not leave behind.
	if !strings.Contains(sec, `kill switch: off`) {
		t.Error("Check 94 does not read the kill-switch lines by the form `budget show` actually prints (`kill switch: off`): the assertion has to match what killText returns, or it proves nothing")
	}
	if strings.Contains(sec, "'kill switch: on'") {
		t.Error("Check 94 asserts on `kill switch: on`, which killText never prints (it renders `ON since <time> by <who>`): the count is then always 0 and a project-wide kill switch left on would not be seen")
	}
}

// TestCheck95KeepsItsPromises: the run through the service -- the job's
// environment, the /proc inspection, the ended-run refusal and the unreachable
// service -- and the policy read, which is the second defect the review found.
func TestCheck95KeepsItsPromises(t *testing.T) {
	doc := readDoc(t, "docs/gcp-live-checklist.md")
	sec := registerSection(t, doc, check95Heading)

	for _, want := range []string{
		"CHK95_OWNER_RUN",          // the owner's own goal flag, typed by hand
		"CHK95_SANDBOX_REPO",       // the one repository the run is launched against
		"fugaro credentials serve", // the queued run that makes this check observable
		"/proc",                    // the inspection that happens inside the job
		"sets:",                    // ... and prints which variable is set, never a value
		"never a value",
		"budget_unavailable",    // what an unreachable service halts the run with
		"stateDetailUnadmitted", // the second route to the same halt, if the API refuses 0
		"--max-instances=0",
		"roles/run.invoker", // the binding the second route removes
		"write-once",        // the ended-run refusal (D134)
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("Check 95's section does not carry %q: the check reads the job's environment, the run's own /proc, the ended-run refusal and the halt an unreachable service produces", want)
		}
	}

	// The policy read. `project policy show --json` prints
	// {"project": ..., "policy": ...}: read at the top level, both `routing` and
	// `credentials` are null, and the section's own pass condition
	// (`routing.claude.via: cascade`) could never be observed.
	if strings.Contains(sec, "p=json.load(sys.stdin)") {
		t.Error("Check 95 reads the policy straight off `project policy show --json`'s top level: the document is an object whose only members are project and policy, so that read returns null for both the switch and the credentials block")
	}
	if !strings.Contains(sec, `.get("policy")`) {
		t.Error("Check 95 does not read the policy through the document's `policy` member, which is where `project policy show --json` puts it")
	}
}

// TestCheck99KeepsItsPromises: the cascade end to end -- the marks, the probe,
// the switch log, the revoked token, the wait and the two policy directions --
// and the result.json read, which is the third defect the review found.
func TestCheck99KeepsItsPromises(t *testing.T) {
	doc := readDoc(t, "docs/gcp-live-checklist.md")
	sec := registerSection(t, doc, check99Heading)

	for _, want := range []string{
		"CHK99_OWNER_RUN",    // the owner's own goal flag
		"CHK99_SANDBOX_REPO", // the one repository the runs are launched against
		"CHK99_A1",           // the two admitted test accounts, typed by name
		"CHK99_A2",
		"config/policy/shared/credentials", // the block written by hand and put back (D153)
		`{"fallback":"oauth-only"}`,        // the contrast that makes D152 a fact
		"probe",                            // what clears a backed-off mark (C1.12)
		"fugaro accounts clear",            // the operator's own clear (C1.21)
		"fugaro creds set",                 // the hidden prompt the token comes from, never argv
		"fugaro doctor",                    // the creds-broken finding
		"creds-broken",
		"maxCredentialResumes", // the bound on the resume loop
		"audit.Write",          // the queued run that would make issuance observable
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("Check 99's section does not carry %q: the check walks the cascade by hand, clears what it wrote, and says which of its steps the service does not reach yet", want)
		}
	}
}

// TestTheCascadeChecksReadTheShapesTheCodePrints holds the three reads the
// review found to the code that produces them. The doc-side assertions are in
// the three tests above; these are the other half -- the shape, taken from the
// code rather than from the check's memory of it.
func TestTheCascadeChecksReadTheShapesTheCodePrints(t *testing.T) {
	// 1. The policy document. `project policy show --json` is built as
	// map[string]any{"project": ..., "policy": ...} and gains nothing at the top
	// level, so every reader of it has to go through `policy`.
	polSrc := readRepoFile(t, "internal/cli/project_cmd.go")
	if !strings.Contains(polSrc, `map[string]any{"project": lc.Name, "policy": nil}`) {
		t.Error("internal/cli/project_cmd.go no longer builds `project policy show --json` as an object of two members, project and policy: the register's checks read it through `policy`, and that has to move with this")
	}

	// 2. The kill switch. killText is the renderer every `budget show` line goes
	// through, so this is called rather than grepped: a check can only match what
	// this returns.
	if got := killText(nil); got != "off" {
		t.Errorf("killText(nil) = %q, want %q: the register's checks read a closed kill switch by that word", got, "off")
	}
	if got := killText(&showKill{At: "2026-10-22T00:00:00Z", By: "owner@example.com"}); !strings.HasPrefix(got, "ON since ") {
		t.Errorf("killText(open) = %q, want it to start with %q: an open switch is rendered in upper case, and a check that looks for a lower-case `on` matches nothing", got, "ON since ")
	}

	// 3. result.json. The cascade's credential name reaches the record through
	// runstore.Record.OAuthAccount -- the field F26 says the runner should write
	// first (D140, C1.19) -- and there is no top-level `credential` in the
	// record at all: that field belongs to the registry entry
	// (budget.AgentEntry), which is what `fugaro watch` shows.
	rec := reflect.TypeOf(runstore.Record{})
	if f, ok := rec.FieldByName("OAuthAccount"); !ok || strings.Split(f.Tag.Get("json"), ",")[0] != "oauth_account" {
		t.Errorf("runstore.Record.OAuthAccount's json name is no longer %q: the register's Check 99 reads the cascade's credential there", "oauth_account")
	}
	for i := 0; i < rec.NumField(); i++ {
		if strings.Split(rec.Field(i).Tag.Get("json"), ",")[0] == "credential" {
			t.Errorf("runstore.Record now has a %s field with the json tag %q: Check 99 reads the cascade's credential from result.json's oauth_account, and a second field with that name means one of the two is now wrong", rec.Field(i).Name, "credential")
		}
	}
	runnerSrc := readRepoFile(t, "internal/runner/runner.go")
	if !strings.Contains(runnerSrc, "r.rec.OAuthAccount = name") {
		t.Error("internal/runner/runner.go no longer writes the cascade's credential into the record's OAuthAccount: Check 99 reads result.json's oauth_account for it")
	}

	// The doc side of the third tie: every result.json read in Check 99 either
	// prints the credential field the record has, or does not print a credential
	// at all (step 4 reads the switch log alone). A read of `credential` there is
	// the review's defect: it is always None.
	doc := readDoc(t, "docs/gcp-live-checklist.md")
	sec := registerSection(t, doc, check99Heading)
	for i, part := range strings.Split(sec, "result.json") {
		if i == 0 {
			continue // the text before the first mention
		}
		head := part
		if len(head) > 900 {
			head = head[:900]
		}
		if strings.Contains(head, `r.get("credential")`) {
			t.Errorf("Check 99 reads `credential` off result.json (mention %d): the record has no such field -- the cascade's name is `oauth_account` there, and the registry entry is what carries `credential`", i)
		}
	}
	if !strings.Contains(sec, `r.get("oauth_account")`) {
		t.Error("Check 99 does not read result.json's `oauth_account`, which is where the runner writes the cascade's credential name")
	}
}

// TestTheCascadeChecksCarryRunnableBlocks: the three checks are steps the owner
// pastes into a shell, so what the register carries has to be what runs. Each
// read hands a program to `python3 -c`, and python -- unlike the shell around
// it -- cares where a continuation line starts: a body written at the fence's
// own indent, which is how these sections were first drafted, is a body that
// raises IndentationError the moment a block is copied out of the file rather
// than off a rendered page, and the step then reports nothing at all. So each
// program in the three sections is compiled exactly as the file carries it,
// every heredoc terminator in them stands in column one (a shell ends a heredoc
// only at a line that starts with it, so an indented one silently swallows the
// rest of the block), and each block that carries a program parses as bash.
func TestTheCascadeChecksCarryRunnableBlocks(t *testing.T) {
	doc := readDoc(t, "docs/gcp-live-checklist.md")
	var blocks []string
	for _, heading := range []string{check94Heading, check95Heading, check99Heading} {
		blocks = append(blocks, fencedBlocks(registerSection(t, doc, heading))...)
	}
	// 25 blocks were written across the three sections; a floor, not a count,
	// so that a section growing does not turn this test red but a section
	// losing blocks -- or the extractor stopping early -- does.
	if len(blocks) < 20 {
		t.Fatalf("the three sections hold %d fenced blocks, and 25 were written: the extractor or the sections lost some", len(blocks))
	}

	programs := 0
	for i, b := range blocks {
		for _, l := range strings.Split(b, "\n") {
			if term := strings.TrimSpace(l); (term == "PY" || term == "TASK" || term == "EOF") && l != term {
				t.Errorf("fenced block %d of the three checks ends a heredoc at %q: only a line that starts with the terminator ends one, so this block runs on past its own text", i, l)
			}
		}
		programs += len(pythonPrograms(b))
	}
	if programs < 20 {
		t.Errorf("the three sections hand %d programs to `python3 -c`, and 26 were written: this test is not reading the blocks it is meant to check", programs)
	}

	bash, berr := exec.LookPath("bash")
	if berr != nil {
		t.Log("bash is not on PATH: the syntax half of this test did not run")
	}
	python, perr := exec.LookPath("python3")
	if perr != nil {
		t.Log("python3 is not on PATH: the program half of this test did not run")
	}
	for i, b := range blocks {
		srcs := pythonPrograms(b)
		if bash != "" && len(srcs) > 0 {
			// A block that carries a program is a shell block: check the
			// quoting that hands the program over as well as the program.
			f := filepath.Join(t.TempDir(), fmt.Sprintf("cascade-checks-%d.sh", i))
			if err := os.WriteFile(f, []byte(b), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bash, "-n", f).CombinedOutput(); err != nil {
				t.Errorf("fenced block %d of the three checks does not parse as bash (%v): %s", i, err, out)
			}
		}
		if python == "" {
			continue
		}
		for j, src := range srcs {
			// Compiled, never run: the programs read stdin, argv and files,
			// and running them here would be running the check.
			cmd := exec.Command(python, "-c", "import sys; compile(sys.stdin.read(), '<register>', 'exec')")
			cmd.Stdin = strings.NewReader(src)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("fenced block %d's program %d does not compile as python (%v): it is handed to `python3 -c` as the file carries it, and this is what the owner's shell would print\n--- the program as the register carries it ---\n%s\n--- python3 ---\n%s", i, j, err, src, out)
			}
		}
	}
}

// pythonPrograms returns every program a block hands to `python3 -c`: the text
// after the flag on its own line, then every line up to the closing quote,
// exactly as the block carries them.
func pythonPrograms(block string) []string {
	const open = "python3 -c '"
	var out []string
	lines := strings.Split(block, "\n")
	for i := 0; i < len(lines); i++ {
		k := strings.Index(lines[i], open)
		if k < 0 {
			continue
		}
		rest := lines[i][k+len(open):]
		if q := strings.LastIndex(rest, "'"); q >= 0 {
			out = append(out, rest[:q]) // the whole program is on one line
			continue
		}
		body := []string{rest}
		j := i + 1
		for ; j < len(lines); j++ {
			if q := strings.LastIndex(lines[j], "'"); q >= 0 {
				body = append(body, lines[j][:q])
				break
			}
			body = append(body, lines[j])
		}
		out = append(out, strings.Join(body, "\n"))
		i = j
	}
	return out
}
