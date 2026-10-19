package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// check67aGuard, check67bGuard and check68Guard are the three setup + guard
// blocks of docs/gcp-live-checklist.md's managed-projects checks, line for
// line. Each is Check 43's block in that check's variables, with the same
// three hard stops: `P` hard-coded to fugaro-dev and never read from gcloud,
// `${VAR:?}` on every argument, `fugaro doctor`'s project line and the
// checkout's own git origin as the last two. The tests below compare them
// verbatim, so an edit that drops a stop, weakens one or reorders a block
// fails here rather than at the owner's terminal.
//
// The variables differ from Check 43's because these checks act on a second
// project of their own — a throwaway managed installation this check creates
// (`F`/`M`, and `N` for the restore) — and because the two values a creating
// command needs are filled in in the guard itself (`ACCOUNT_ID`, `AMOUNT`).
// The STOPS do not differ: TestManagedChecklistGuardsCarryCheck43sStops below
// compares them against Check 43's own block in the same file.
var check67aGuard = []string{
	"# === Check 67a: setup + guard. RE-RUN THIS WHOLE BLOCK at the start of every",
	"# step (1 to 9, and the Restore). It is idempotent, and nothing below the",
	"# three hard stops runs unless all three pass. ===",
	"P=fugaro-dev                 # HARD-CODED, and never read from gcloud: the sandbox installation's GCP project",
	"F=fugaro-managed             # this check's throwaway managed installation, created in step 1 (fugaro init --name)",
	"M=fugaro-chk67               # the managed installation's GCP project ID: it MUST NOT exist before step 1",
	"L=us-east5                   # the installation's region",
	"R=edgeappinc/fugarosandbox   # the sandbox repository, owner/name: the only repository this check touches",
	"B=fugaro-runs-$M             # the managed project's runs bucket, where the marker and the delete list live (fugaro's own naming)",
	"ACCOUNT_ID=                  # the SANDBOX installation's billing account ID, no BILLING_ prefix: the throwaway project is created under it",
	"AMOUNT=                      # the monthly amount for the throwaway managed project, a number: fugaro init --managed has no default for it",
	"TERRAFORM_ADDRESS=           # ONE resource address out of a real plan, in terraform's own form: the single address fugaro allow-delete is given in step 7",
	"SANDBOX=$(git rev-parse --show-toplevel)   # the sandbox checkout this block is being re-run IN: the only directory where the three stops below hold, and the directory every step returns to",
	"",
	"# ${VAR:?} everywhere: an empty variable is a hard error, never an empty",
	"# argument that gcloud would resolve from its ambient configuration (a default",
	"# gcloud project is not this check's). No apostrophe inside a ${VAR:?...}",
	"# message: bash reads the message as a word expecting a closing quote and the",
	"# whole block then fails to parse. TERRAFORM_ADDRESS is the one value here no",
	"# step can choose for you: it comes out of a plan the owner reads. SANDBOX is",
	"# read, not typed: it is the checkout the block is being re-run in, so a step",
	"# that has cd-ed away returns to it with `cd \"${SANDBOX:?}\"` and never with",
	"# `cd -` (which would land in the clone).",
	"[ \"$P\" = fugaro-dev ] && [ -n \"$L\" ] && [ -n \"$R\" ] && [ -n \"$F\" ] && [ -n \"$M\" ] && [ -n \"$B\" ] && [ -n \"$ACCOUNT_ID\" ] && [ -n \"$AMOUNT\" ] && [ -n \"$TERRAFORM_ADDRESS\" ] && [ -n \"$SANDBOX\" ] \\",
	"  || { echo \"Check 67a stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L R=$R F=$F M=$M B=$B ACCOUNT_ID=$ACCOUNT_ID AMOUNT=$AMOUNT TERRAFORM_ADDRESS=$TERRAFORM_ADDRESS SANDBOX=$SANDBOX must not be empty)\" >&2; exit 1; }",
	"fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,' \\",
	"  || { echo \"Check 67a stopped: this is not the fugaro-dev installation (fugaro doctor's project: line is not fugaro-dev, or doctor refused the checkout: --project disagreeing with the checkout's project: is refused)\" >&2; exit 1; }",
	"git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\\.git)?$' \\",
	"  || { echo \"Check 67a stopped: this checkout's origin is not the sandbox (edgeappinc/fugarosandbox)\" >&2; exit 1; }",
}

var check67bGuard = []string{
	"# === Check 67b: setup + guard. RE-RUN THIS WHOLE BLOCK at the start of every",
	"# step (1 to 6, and the Restore). It is idempotent, and nothing below the",
	"# three hard stops runs unless all three pass. ===",
	"P=fugaro-dev                 # HARD-CODED, and never read from gcloud: the sandbox installation's GCP project",
	"F=fugaro-managed             # this check's throwaway managed installation, created by Check 67a's step 1",
	"M=fugaro-chk67               # the managed project Check 67a created: the OLD project, destroyed in step 1 here",
	"N=fugaro-chk67b              # the NEW project ID this restore creates: it MUST NOT exist before step 4",
	"E=                           # an EXISTING GCP project ID you may read and this check does NOT own: not $P, not $M, not $N. Step 2 presents it to watch the restore refuse to ADOPT a project (any other scratch project of your own will do)",
	"L=us-east5                   # the installation's region",
	"R=edgeappinc/fugarosandbox   # the sandbox repository, owner/name: the only repository this check touches",
	"ACCOUNT_ID=                  # the SANDBOX installation's billing account ID, no BILLING_ prefix: the new project is created under it",
	"AMOUNT=                      # the monthly amount for the new managed project, a number",
	"TERRAFORM_ADDRESS=           # ONE resource address out of a real plan, in terraform's own form: the single address fugaro allow-delete is given in step 5",
	"SANDBOX=$(git rev-parse --show-toplevel)   # the sandbox checkout this block is being re-run IN: the only directory where the three stops below hold, and the directory step 6 returns to",
	"",
	"# ${VAR:?} everywhere: an empty variable is a hard error, never an empty",
	"# argument that gcloud would resolve from its ambient configuration. No",
	"# apostrophe inside a ${VAR:?...} message (bash reads it as an unclosed quote).",
	"# TERRAFORM_ADDRESS comes out of a plan you read, exactly as in 67a, and E is",
	"# refused unless it names a project of its own: a restore pointed at the",
	"# sandbox project is the one creating command this check never runs. SANDBOX is",
	"# read rather than typed, so a step that has cd-ed away returns with",
	"# `cd \"${SANDBOX:?}\"` and never with `cd -` (which would land in the clone).",
	"[ \"$P\" = fugaro-dev ] && [ -n \"$L\" ] && [ -n \"$R\" ] && [ -n \"$F\" ] && [ -n \"$M\" ] && [ -n \"$N\" ] && [ -n \"$E\" ] && [ \"$E\" != \"$P\" ] && [ \"$E\" != \"$M\" ] && [ \"$E\" != \"$N\" ] && [ -n \"$ACCOUNT_ID\" ] && [ -n \"$AMOUNT\" ] && [ -n \"$TERRAFORM_ADDRESS\" ] && [ -n \"$SANDBOX\" ] \\",
	"  || { echo \"Check 67b stopped: re-run the whole setup block (P=$P must be fugaro-dev; E=$E must differ from P, M and N, and L=$L R=$R F=$F M=$M N=$N ACCOUNT_ID=$ACCOUNT_ID AMOUNT=$AMOUNT TERRAFORM_ADDRESS=$TERRAFORM_ADDRESS SANDBOX=$SANDBOX must not be empty)\" >&2; exit 1; }",
	"fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,' \\",
	"  || { echo \"Check 67b stopped: this is not the fugaro-dev installation (fugaro doctor's project: line is not fugaro-dev, or doctor refused the checkout: --project disagreeing with the checkout's project: is refused)\" >&2; exit 1; }",
	"git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\\.git)?$' \\",
	"  || { echo \"Check 67b stopped: this checkout's origin is not the sandbox (edgeappinc/fugarosandbox)\" >&2; exit 1; }",
}

var check68Guard = []string{
	"# === Check 68: setup + guard. RE-RUN THIS WHOLE BLOCK at the start of every",
	"# step (1 to 6, and the Restore). It is idempotent, and nothing below the",
	"# three hard stops runs unless all three pass. ===",
	"P=fugaro-dev                 # HARD-CODED, and never read from gcloud: the sandbox installation's GCP project",
	"F=fugaro-managed             # this check's throwaway managed installation, created by Check 67a's step 1",
	"M=fugaro-chk67               # the managed project whose deployer account this check measures",
	"L=us-east5                   # the installation's region",
	"R=edgeappinc/fugarosandbox   # the sandbox repository, owner/name: the only repository this check touches",
	"D=fugaro-deployer@$M.iam.gserviceaccount.com   # the account D75 defines (tf.DeployerAccountID, fugaro-deployer)",
	"B=fugaro-runs-$M             # the managed project's runs bucket: where the marker and the delete list are, and what the deployer must NOT reach",
	"SANDBOX=$(git rev-parse --show-toplevel)   # the sandbox checkout this block is being re-run IN: the only directory where the three stops below hold, and the directory step 6 returns to",
	"",
	"# ${VAR:?} everywhere: an empty variable is a hard error, never an empty",
	"# argument that gcloud would resolve from its ambient configuration. No",
	"# apostrophe inside a ${VAR:?...} message (bash reads it as an unclosed quote).",
	"# SANDBOX is read rather than typed, so a step that has cd-ed away returns with",
	"# `cd \"${SANDBOX:?}\"` and never with `cd -` (which would land in the clone).",
	"[ \"$P\" = fugaro-dev ] && [ -n \"$L\" ] && [ -n \"$R\" ] && [ -n \"$F\" ] && [ -n \"$M\" ] && [ -n \"$D\" ] && [ -n \"$B\" ] && [ -n \"$SANDBOX\" ] \\",
	"  || { echo \"Check 68 stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L R=$R F=$F M=$M D=$D B=$B SANDBOX=$SANDBOX must not be empty)\" >&2; exit 1; }",
	"fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,' \\",
	"  || { echo \"Check 68 stopped: this is not the fugaro-dev installation (fugaro doctor's project: line is not fugaro-dev, or doctor refused the checkout: --project disagreeing with the checkout's project: is refused)\" >&2; exit 1; }",
	"git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\\.git)?$' \\",
	"  || { echo \"Check 68 stopped: this checkout's origin is not the sandbox (edgeappinc/fugarosandbox)\" >&2; exit 1; }",
}

// logicalCommands joins each backslash continuation in a fenced block into one
// line and drops comments, so a command spread over several lines is read as
// the one command it is. Without this, a rule spelled "every command carries
// --project" would pass a command whose `--project` sits on its own
// continuation line — and would check nothing about the arguments that matter.
func logicalCommands(block []string) []string {
	var out []string
	var cur string
	for _, line := range block {
		s := strings.TrimSpace(line)
		if cur == "" {
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
		}
		if strings.HasSuffix(s, "\\") {
			cur += strings.TrimSpace(strings.TrimSuffix(s, "\\")) + " "
			continue
		}
		if cur != "" {
			out = append(out, cur+s)
			cur = ""
			continue
		}
		out = append(out, s)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// TestManagedChecklistGuardsCarryCheck43sStops is the anti-drift half of the
// three guard pins below, and the reason they are not copies that could rot
// together: it reads Check 43's own guard block out of the same file and
// compares each of the three checks' stop bodies against it, with only the
// check's name normalised away. A later edit that weakens Check 43's stops
// fails here for the other three; an edit that weakens one of the three fails
// here for that one, whatever it did to the others.
//
// The `fugaro doctor` stop and the git-origin stop are byte for byte Check
// 43's. The first stop is Check 43's with a longer variable list — these
// checks act on a project Check 43 never heard of — so what is pinned is its
// fixed parts: the `P` equality, the `${VAR:?}`-free message shape and the
// `must not be empty` clause.
func TestManagedChecklistGuardsCarryCheck43sStops(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	stops := func(section string) map[string]string {
		blocks := bashBlocks(section)
		if len(blocks) == 0 {
			t.Fatalf("the section has no fenced bash block to read a guard from")
		}
		got := map[string]string{}
		for _, line := range blocks[0] {
			s := strings.TrimSpace(line)
			switch {
			case strings.Contains(s, "stopped: re-run the whole setup block"):
				got["setup"] = s
			case strings.Contains(s, "stopped: this is not the fugaro-dev installation"):
				got["doctor"] = s
			case strings.Contains(s, "stopped: this checkout's origin is not the sandbox"):
				got["origin"] = s
			case strings.HasPrefix(s, "fugaro doctor --project fugaro | grep -q"):
				got["doctor-line"] = s
			case strings.HasPrefix(s, "git remote get-url origin | grep -Eq"):
				got["origin-line"] = s
			}
		}
		for _, key := range []string{"setup", "doctor", "origin", "doctor-line", "origin-line"} {
			if got[key] == "" {
				t.Fatalf("the guard block carries no %s line", key)
			}
		}
		return got
	}
	// name strips the check's own name out of the one thing that legitimately
	// differs between the four blocks.
	name := func(s, check string) string {
		return strings.ReplaceAll(s, "Check "+check+" stopped:", "Check X stopped:")
	}

	ref := stops(docSection(t, path, doc, "## Check 43:"))
	for _, c := range []struct {
		check   string
		heading string
		guard   []string
	}{
		{"67a", "## Check 67a:", check67aGuard},
		{"67b", "## Check 67b:", check67bGuard},
		{"68", "## Check 68:", check68Guard},
	} {
		got := stops(docSection(t, path, doc, c.heading))
		for _, key := range []string{"doctor", "origin", "doctor-line", "origin-line"} {
			if name(got[key], c.check) != name(ref[key], "43") {
				t.Errorf("Check %s's %s is no longer Check 43's:\n  got  %s\n  want %s", c.check, key, got[key], ref[key])
			}
		}
		if name(got["setup"], c.check) == name(ref["setup"], "43") {
			t.Errorf("Check %s's setup stop is Check 43's verbatim, so it does not fail closed on the variables this check adds", c.check)
		}
		for _, want := range []string{
			"stopped: re-run the whole setup block (P=$P must be fugaro-dev; ",
			"must not be empty)\" >&2; exit 1; }",
		} {
			if !strings.Contains(got["setup"], want) {
				t.Errorf("Check %s's setup stop does not carry %q: %s", c.check, want, got["setup"])
			}
		}
	}
}

// TestChecklistHasCheck67a pins Check 67a of docs/gcp-live-checklist.md: the
// managed apply's own check (plan docs/plans/2026-10-16-managed-projects.md
// Task 5.3, design docs/design/managed-projects.md D55-D78). The row and the
// heading say it is the owner's to run; the guard block is verbatim and
// first; every step says whether it runs once per installation or once per
// repo checkout; every command that names a project names it explicitly; and
// the section states, rather than papers over, the one direction it cannot
// assert (the inventory is read live but does not yet feed the apply).
func TestChecklistHasCheck67a(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67a:")

	// 1. Whose check it is, and where it runs.
	row := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 67a |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("the checklist has no `| 67a |` table row")
	}
	for _, want := range []string{"USER-RUN, NOT RUN", "sandbox repository only", "no agent runs any step"} {
		if !strings.Contains(row, want) {
			t.Errorf("the `| 67a |` row does not carry %q", want)
		}
	}
	for _, want := range []string{
		"USER-RUN, NOT RUN",
		"run by you",
		"once per installation",
		"once per repo checkout",
		"sandbox repository only",
		"edgeappinc/fugarosandbox",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a does not say %q", want)
		}
	}
	// The boundary, said twice on the page and enforced by the guard: nothing
	// here acts on an installation this check did not create.
	for _, want := range []string{"No installation other than this sandbox one is named, read or written by any step", "the sandbox repository's default branch"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a does not state its boundary (%q)", want)
		}
	}

	// 2. The guard block, verbatim and first.
	blocks := bashBlocks(section)
	if len(blocks) < 8 {
		t.Fatalf("Check 67a has %d fenced bash blocks, want the guard block and the steps", len(blocks))
	}
	compareGuard(t, "67a", blocks[0], check67aGuard)

	// 3. Every step is labelled, because "once per installation" and "once per
	// repo checkout" are different costs and different failure modes: a step
	// whose label is missing is a step the owner has to guess about.
	for _, tag := range []string{
		"**(1) The throwaway managed project and its installation** — **once per installation.**",
		"**(2) The managed installation's own checkout, and the root every later step applies** — **once per repo checkout",
		"**(3) An apply the gate admits — prompt-free under the coding agent's marker, and asked without it** — **once per repo checkout",
		"**(4) A marker that names another project reads as not managed** — **once per repo checkout.**",
		"**(5) A project that is not managed still asks** — **once per repo checkout",
		"**(6) A plan that names a second project is refused** — **once per repo checkout, in the managed checkout.**",
		"**(7) The delete list: absent means refused, one written address means applied** — **once per repo checkout.**",
		"**(8) The run's own job credential: the grant that keeps it out of `fugaro/`, read live — and the write-level FACT that stays UNVERIFIED** — **once per repo checkout",
		"**(9) The whole-project inventory read, through the Cloud Asset API** — **once per repo checkout.**",
		"**Restore — once per installation, and it deletes only what this check created.**",
	} {
		if !strings.Contains(section, tag) {
			t.Errorf("Check 67a lacks the labelled step %q", tag)
		}
	}

	// 4. The facts the check exists for, each one a thing only a live run can
	// answer: the gate removes the confirmation, the delete list decides a
	// delete, the marker is the authority, and the run's own credential fails.
	for _, want := range []string{
		"prompt-free under the coding agent's marker",
		"asked and was not confirmed",
		"`fmgd-`",
		"`fugaro/managed.json`",
		"fugaro/allow-delete.json",
		"`managed.ErrNotManaged`",
		"`managed.ErrNotEnabled`",
		"internal/managed/budget.go",
		"internal/managed/apply.go",
		"`ErrSessionAddresses`",
		"`cloudasset.assets.searchAllResources`",
		"`fugaroCloudAssetReader`",
		"PERMISSION_DENIED",
		"`internal/localcfg/select.go:163`",
		"fugaro-deployer",
		"fugaro-managed-tfstate-",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a does not carry %q", want)
		}
	}

	// 5. The half that cannot be asserted yet, marked as such. The plan's
	// Task 5.3 writes the inventory as feeding the apply; the owner ruled on
	// 2026-10-18 that 0.8.5 applies without consulting it. A check that
	// claimed the fallback would send the owner to a failure that is the
	// release's own, so the direction is UNVERIFIED here and named by task.
	for _, want := range []string{
		"UNVERIFIED",
		"Task 4.5",
		"`TestApplyConsultsNoInventory`",
		"consults no inventory",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a does not record the inventory direction as %q", want)
		}
	}

	// 6. The Restore ends in counts, not in an empty-looking screen.
	if !strings.Contains(section, "must read `0`") {
		t.Error("Check 67a's Restore does not demand a `0` count on every line")
	}
	// And it deletes the project it made, by a variable, never a literal ID.
	if !strings.Contains(section, `gcloud projects delete "${M:?}" --project="${M:?}"`) {
		t.Error("Check 67a's Restore does not delete the throwaway project by its variable")
	}

	// 7. The four commands this check is unrunnable without, each one a
	// refusal the owner would hit on the check's first minutes. Every one of
	// them was missing from an earlier draft of this section, so they are
	// pinned here rather than left to a reader to notice:
	//
	//   - `--managed` refuses without `--billing-account` (the budget is built
	//     from it, `internal/cli/init_project.go:66`), so a creation that
	//     passes only `--link-billing` is refused before anything exists;
	//   - the second checkout has to move `gcp_project:` as well as `project:`
	//     (`setGCPProjectLine`; `restoreGCPProjectLine` in `restore.go`, which
	//     refuses a differing value rather than resolving it), or step 2's own
	//     onboarding and 67b's restore both stop;
	//   - the marker's ORIGINAL bytes have to be copied before the doctored
	//     ones are uploaded, or the restore the section instructs has nothing
	//     to restore from and steps 5 to 7 run against a marker that reads as
	//     not managed;
	//   - the Restore has to be conditional, because 68 and 67b both need the
	//     project and the checkout it deletes.
	//
	// The second review added four more, and each is the same shape: a step
	// whose command and whose expectation did not describe the same thing.
	//
	//   - step 5's root has to be INSIDE the checkout the command acts on,
	//     because `managedRoot` refuses a root from another project's checkout
	//     before any cloud call (`internal/cli/managed_apply.go`), so a root
	//     under /tmp with `--project fugaro` answers the checkout rule and not
	//     the gate, and the gate's own words are ErrNotEnabled's, not
	//     ErrNotManaged's;
	//   - step 6 restores the root from its own copy, because nothing commits
	//     `./managed-root` and `git checkout --` fails on an untracked path,
	//     leaving step 7 to apply a root that names a second project;
	//   - step 8 reads the job account's BUCKET-level binding and its condition
	//     instead of impersonating that account: the only
	//     `roles/iam.serviceAccountTokenCreator` grant in this installation is
	//     on D75's deployer account (`installation/deployer.tf`), so the
	//     impersonation would be refused to the OPERATOR and prove nothing about
	//     the job account, and the write level is UNVERIFIED here;
	//   - `TERRAFORM_ADDRESS` is declared and guarded in the two checks that use
	//     it, or step 7's and 67b's `fugaro allow-delete` is handed an empty
	//     address by a stop that never looked at it.
	for _, want := range []string{
		"--billing-account ${ACCOUNT_ID:?} --budget ${AMOUNT:?} --budget-currency USD --link-billing ${ACCOUNT_ID:?}",
		`sed -e 's/^project: .*/project: '"${F:?}"'/' -e 's/^gcp_project: .*/gcp_project: '"${M:?}"'/' fugaro.yaml > fugaro.yaml.new`,
		"NOT `sed -i`: BSD/macOS sed reads -e as -i's backup extension",
		`cp /tmp/chk67-marker.orig.json /tmp/chk67-marker-bad.json`,
		`gcloud storage cp /tmp/chk67-marker.orig.json "gs://${B:?}/fugaro/managed.json" --project="${M:?}"`,
		"ONLY if Check 68 and Check 67b are not going to run",
		// step 5: both refusals, each with the command that produces it
		`fugaro managed apply ./chk67-scratch-root --project fugaro`,
		`mkdir -p ./chk67-scratch-root`,
		"is in the checkout of project fugaro-managed, and this command is acting on fugaro",
		"so its plans keep the coding agent's refusal",
		// step 6: the root restored from a copy, tracked or not
		`cp -a ./managed-root /tmp/chk67-managed-root.orig`,
		`rm -rf ./managed-root && cp -a /tmp/chk67-managed-root.orig ./managed-root`,
		// step 8: the binding read, and the UNVERIFIED it carries
		`gcloud storage buckets get-iam-policy "gs://fugaro-runs-${P:?}" --project="${P:?}" --format=json`,
		`grep -c 'objects/fugaro/' /tmp/chk67-runs-policy.json`,
		"fugaro_deployer_token_creator",
		"deploy/terraform/gcp/modules/installation/deployer.tf",
		"deploy/terraform/gcp/modules/workflow/sa.tf:12",
		"the **write-level** denial itself",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a does not carry %q", want)
		}
	}
	// The shapes those eight replaced, each one a dead end the section must not
	// go back to: a creation with no --billing-account, a sed that moves only
	// the installation's name, a copy named after the doctored file, a Restore
	// that deletes what the next two checks run on, an expectation pinned to a
	// refusal that command cannot produce, a `git checkout` of a root nothing
	// commits, and an impersonation whose denial would be the operator's own.
	for _, bad := range []string{
		"--managed --name \"${F:?}\" --gcp-project \"${M:?}\" --region \"${L:?}\" \\\n  --link-billing",
		"'s/^project: .*/project: fugaro-managed/'",
		"/tmp/chk67-marker.json",
		"rm -rf /tmp/chk67-managed /tmp/chk67-marker.json",
		`fugaro managed apply /tmp/chk67-managed/managed-root --project fugaro   # expect: refused, "the project is not managed"`,
		"git checkout -- managed-root",
		`--impersonate-service-account="$JOB_SA"`,
		`gcloud storage cp /tmp/chk67-runs-policy.json "gs://${B:?}/`,
		"the run's job credential failed to write both objects",
		"repo/workflow",
	} {
		if strings.Contains(section, bad) {
			t.Errorf("Check 67a still carries the dead end %q", bad)
		}
	}
	// The one claim step 5 must not make again: `managed.ErrNotManaged` is the
	// apply's own creation-fact refusal, and this command ends at the gate
	// before the apply is reached. The section still names it, but only to say
	// which of the two it is NOT.
	if !strings.Contains(section, "**not** `managed.ErrNotManaged`") {
		t.Error("Check 67a's step 5 no longer says which refusal it is not")
	}

	// 9. The three fixes of this round, each pinned where it can regress.
	//
	// (a) Step 1 runs where its own guard holds. The guard's stops need a
	// sandbox checkout (its second stop asks `fugaro doctor --project fugaro`,
	// which a checkout naming another project refuses; its third asks
	// `git remote get-url origin`), and the sandbox checkout is exactly where
	// the creation CANNOT run: `--name fugaro-managed` there is refused with
	// "this checkout belongs to project fugaro; --name says fugaro-managed",
	// because a checkout is authoritative. So the step clones a working tree of
	// the sandbox and moves that checkout's fugaro.yaml aside, and the guard's
	// own second and third stops are re-made in the clone — which is what makes
	// the step reachable at all.
	for _, want := range []string{
		"**Which directory each step runs from, and why that is a rule and not a convenience.**",
		"this checkout belongs to project fugaro; --name says fugaro-managed",
		"mv fugaro.yaml /tmp/chk67-fugaroyaml.orig",
		"the third stop holds HERE",
		"mv /tmp/chk67-fugaroyaml.orig fugaro.yaml",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a's step 1 does not carry %q", want)
		}
	}
	// The creation must come AFTER the clone and the move: run before them it
	// is the dead end this round fixed — the command the sandbox checkout
	// refuses, from the only directory the guard admits.
	clone := strings.Index(section, "git clone \"$(git remote get-url origin)\" /tmp/chk67-managed")
	moved := strings.Index(section, "mv fugaro.yaml /tmp/chk67-fugaroyaml.orig")
	create := strings.Index(section, "fugaro init --create-project --managed --name \"${F:?}\"")
	switch {
	case clone < 0 || moved < 0 || create < 0:
		t.Error("Check 67a's step 1 no longer clones, moves the checkout's fugaro.yaml aside and creates the installation")
	case !(clone < moved && moved < create):
		t.Error("Check 67a's step 1 creates the managed installation before the clone that moves the checkout's fugaro.yaml aside")
	}

	// (b) The root the whole check applies is authored, with its contents in
	// full, and BEFORE the first apply of it. `fugaro managed apply` writes no
	// root, and no root in this repository is one the gate admits.
	for _, want := range []string{
		"cat > ./managed-root/main.tf <<EOF",
		"cat > ./managed-root/account.tf <<EOF",
		"resource \"google_service_account\" \"fmgd_root\"",
		`account_id   = "fmgd-chk67-root"`,
		`version = ">= 8.4.0, < 9.0.0"`,
		`provider "google" {`,
		"managedProviderProject`: the provider block",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a does not author the root it applies (%q)", want)
		}
	}
	mkroot := strings.Index(section, "mkdir -p ./managed-root")
	firstApply := strings.Index(section, "fugaro managed apply ./managed-root --project \"${F:?}\"")
	if mkroot < 0 {
		t.Error("no step of Check 67a writes ./managed-root, so every managed step applies a root that does not exist")
	} else if firstApply < 0 {
		t.Error("Check 67a no longer applies ./managed-root")
	} else if mkroot > firstApply {
		t.Error("Check 67a applies ./managed-root before any step authors it")
	}
	// And the delete both delete steps expect is produced, in the section that
	// expects it: a resource is removed from the root, not wished away.
	if !strings.Contains(section, "mv ./managed-root/account.tf /tmp/chk67-root-account.tf") {
		t.Error("Check 67a's step 7 expects a plan with a delete without any step removing a resource from the root")
	}

	// (c) Step 3's impersonation reads are commands the owner runs, not output
	// a successful apply prints: a managed apply prints "applied" and its
	// summary, and nothing about IAM.
	for _, want := range []string{
		`gcloud iam service-accounts get-iam-policy "fugaro-deployer@${M:?}.iam.gserviceaccount.com"`,
		`gcloud projects get-iam-policy "${M:?}" --project="${M:?}"`,
		"apply prints \"applied\" and its own summary",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a's step 3 does not give the impersonation reads as commands (%q)", want)
		}
	}
	if strings.Contains(section, "the command printed the two reads that show the impersonation grant is in place") {
		t.Error("Check 67a's step 3 claims the apply prints reads it does not print")
	}
	// ... and the same false claim must not survive anywhere else in the check:
	// the Method's own precondition used to say the apply prints them, and the
	// apply prints "applied" and nothing else.
	for _, bad := range []string{
		"the two reads that show it are printed by `fugaro managed apply` itself",
	} {
		if strings.Contains(section, bad) {
			t.Errorf("Check 67a still claims the apply prints the impersonation reads (%q)", bad)
		}
	}
	// Every step that acts inside the clone says so; the ones that need no
	// directory of their own say they are back in the sandbox checkout, because
	// the guard block cannot be re-run anywhere else once the clone's
	// fugaro.yaml names the managed installation.
	for _, want := range []string{
		"cd /tmp/chk67-managed                                        # the managed checkout: the guard block above was re-run in the sandbox checkout",
		`cd "${SANDBOX:?}"                                                       # back in the sandbox checkout`,
		// The return to the sandbox checkout is the guard block's own variable,
		// not `$OLDPWD`: the block was just re-run IN the sandbox checkout, so
		// `cd -` lands in whatever directory the previous step left the shell in
		// (`/tmp/chk67-managed`, unset in a fresh shell) — where the second stop
		// is refused, and where step 5 would put ./chk67-scratch-root as well,
		// replacing the gate's own refusal with the checkout rule's.
		`SANDBOX=$(git rev-parse --show-toplevel)`,
		"`cd -` would land in the clone",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a's step does not name the directory it runs from (%q)", want)
		}
	}
	for _, bad := range []string{"\ncd - ", "\ncd -\n", "\ncd -\t"} {
		if strings.Contains(section, bad) {
			t.Errorf("Check 67a returns to the sandbox checkout with %q, which lands in the clone", strings.TrimSpace(bad))
		}
	}
	// The Restore reverses the one thing this check can leave on the SANDBOX
	// installation — step 8's optional probe (iii) — before its counts, and
	// counts that reversal as one of its `0` lines. 68's Restore counts the
	// equivalent; a leftover grant here would be a change to the installation
	// the check is not allowed to make.
	for _, want := range []string{
		"JOB_SA=                      # the job account's email, from that read",
		`gcloud iam service-accounts remove-iam-policy-binding "$JOB_SA"`,
		`gcloud iam service-accounts get-iam-policy "$JOB_SA" --project="${P:?}"`,
		"the token-creator grant step 8's optional probe (iii) may have added to the job account",
		"if [ -n \"$JOB_SA\" ]; then",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a's Restore does not reverse probe (iii) (%q)", want)
		}
	}

	// 8. The commands. Every gcloud command names its project with
	// `--project=`; every fugaro command names its installation with
	// `--project`, with exactly one exception this check spells out (the
	// creating `fugaro init`, which takes `--name` because no config of that
	// name exists yet).
	assertProjectFlags(t, "67a", blocks, 5, 4)
}

// TestChecklistHasCheck67b pins Check 67b: the restore round trip (D66, D68,
// D77). Its assertions are the durability ones — a lost project, a NEW ID, a
// from-zero apply that reads no state, and a not-restored list printed rather
// than implied — and its own trap, that it cannot run beside 67a.
func TestChecklistHasCheck67b(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67b:")

	row := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 67b |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("the checklist has no `| 67b |` table row")
	}
	for _, want := range []string{"USER-RUN, NOT RUN", "sandbox repository only", "cannot run at the same moment"} {
		if !strings.Contains(row, want) {
			t.Errorf("the `| 67b |` row does not carry %q", want)
		}
	}
	for _, want := range []string{
		"USER-RUN, NOT RUN",
		"run by you",
		"once per installation",
		"once per repo checkout",
		"Run 67a and Check 68 first",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67b does not say %q", want)
		}
	}

	blocks := bashBlocks(section)
	if len(blocks) < 6 {
		t.Fatalf("Check 67b has %d fenced bash blocks, want the guard block and the steps", len(blocks))
	}
	compareGuard(t, "67b", blocks[0], check67bGuard)

	for _, tag := range []string{
		"**(1) Destroy the managed project** — **once per installation.**",
		"**(2) Two existing project IDs, and the two different refusals they earn** — **once per repo checkout.**",
		"**(3) The root moves to the new project before the restore applies it** — **once per repo checkout, in the managed checkout.**",
		"**(4) The restore itself** — **once per installation.**",
		"**(5) The new project is managed, and the delete list is genuinely gone** — **once per repo checkout.**",
		"**(6) No state read happened** — **once per repo checkout.**",
		"**Restore — once per installation, and it deletes only what this check created.**",
	} {
		if !strings.Contains(section, tag) {
			t.Errorf("Check 67b lacks the labelled step %q", tag)
		}
	}

	// The round trip's own facts: a new ID, no state read, the line that
	// moves, the deployer account re-created, the secrets to re-enter by hand,
	// and the delete list that is NOT carried across (so deletes fail closed
	// in the new project until the owner writes it again).
	for _, want := range []string{
		"gcp_project:",
		"reading no prior state",
		"never adopts",
		"fugaro-managed-tfstate-",
		"fugaro secrets ls",
		"missing-secret column",
		"not-restored list",
		"the Firestore database's location",
		"fugaro/allow-delete.json",
		"`fugaro allow-delete`",
		"every delete and replace in the new project is refused",
		// step 2: TWO existing IDs, and the two code paths that refuse them.
		// The ID of the lost installation never reaches the existence check —
		// `restoreNewProjectID` refuses it first, before any cloud call — so
		// the "already exists … never adopts a project" sentence that the FACT
		// used to pin to ${M:?} needs a project of its own (${E:?}).
		`fugaro restore "${M:?}" ./managed-root --project "${F:?}" --billing-account ${ACCOUNT_ID:?} --budget ${AMOUNT:?} --budget-currency USD`,
		`fugaro restore "${E:?}" ./managed-root --project "${F:?}" --billing-account ${ACCOUNT_ID:?} --budget ${AMOUNT:?} --budget-currency USD`,
		"is the ID of the installation that was lost",
		"`restoreNewProjectID`",
		"`restoreRun.refuseExisting`",
		`"nothing was restored: GCP project ${E:?} already exists`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67b does not carry %q", want)
		}
	}
	if !strings.Contains(section, "must read `0`") {
		t.Error("Check 67b's Restore does not demand a `0` count on every line")
	}
	if !strings.Contains(section, `gcloud projects delete "${N:?}" --project="${N:?}"`) {
		t.Error("Check 67b's Restore does not delete the project its own restore made")
	}
	// The review's two findings against this section: a stray check number left
	// in the Method's prose (the section is 67b, and a reader who sees **73**
	// goes looking for a Check 73 that does not exist), and the one address
	// `fugaro allow-delete` is given, which was used here out of a variable no
	// setup block declared.
	for _, bad := range []string{"**73**"} {
		if strings.Contains(section, bad) {
			t.Errorf("Check 67b still carries the stray token %q", bad)
		}
	}
	if !strings.Contains(section, "The guard block is re-run before every step") {
		t.Error("Check 67b's Method no longer says the guard block is re-run before every step")
	}
	// The write of the owner's object carries its CONFIRM here too: it is the
	// same command 67a's step 7 marks, and an unmarked write of
	// fugaro/allow-delete.json is the one line in the section that can approve
	// a delete in the new project.
	if !strings.Contains(section, `fugaro allow-delete ${TERRAFORM_ADDRESS:?} --project "${F:?}"   # ⚠ CONFIRM`) {
		t.Error("Check 67b's step 5 writes the owner's delete list without its ⚠ CONFIRM")
	}
	// Every step names the directory it runs from, and the guard block's own
	// stops are only re-runnable in the sandbox checkout — so a step that acts
	// on ./managed-root has to cd there, and the pointer to 67a's clone has to
	// say so, or 67b inherits 67a step 1's dead end.
	for _, want := range []string{
		"**Which directory each step runs from**",
		"both are re-run in the sandbox checkout",
		"cd /tmp/chk67-managed    # the managed checkout",
		"cd /tmp/chk67-managed    # the managed checkout, again",
		// The return to the sandbox checkout is the guard block's own variable:
		// `cd -` would land in the clone the previous step left the shell in.
		`SANDBOX=$(git rev-parse --show-toplevel)`,
		`cd "${SANDBOX:?}"`,
		"never `cd -`, which would land in the clone just as it would in 67a",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67b does not say %q, so a step runs where its own guard cannot", want)
		}
	}
	for _, bad := range []string{"\ncd - ", "\ncd -\n"} {
		if strings.Contains(section, bad) {
			t.Errorf("Check 67b returns to the sandbox checkout with %q, which lands in the clone", strings.TrimSpace(bad))
		}
	}
	// Step 4's delete plan: the same absent-then-written sequence 67a's step 7
	// uses, so it too must remove the one resource from the root and put it
	// back — a section that expects a delete it never authors cannot run.
	if !strings.Contains(section, "mv ./managed-root/account.tf /tmp/chk67-root-account.tf") ||
		!strings.Contains(section, "mv /tmp/chk67-root-account.tf ./managed-root/account.tf") {
		t.Error("Check 67b's step 5 expects a plan with a delete without any step removing a resource from the root")
	}
	// No step of this check may name the sandbox installation's project in a
	// creating command: the only IDs a restore is ever pointed at here are the
	// two throwaway ones.
	for _, bad := range []string{
		`fugaro restore "${P:?}"`,
		`fugaro init --create-project`,
	} {
		if strings.Contains(section, bad) {
			t.Errorf("Check 67b creates against the sandbox installation's project (%q)", bad)
		}
	}
	assertProjectFlags(t, "67b", blocks, 4, 3)
}

// TestChecklistHasCheck68 pins Check 68: the deployer account's live IAM
// (plan Task 5.3, D75 as amended on 2026-10-16). It is the check that cannot
// be a fixture — a fixture cannot know which identity a person will use — so
// what is pinned here is that the section names every negative that must
// FAIL, the permissions the roles are known to LACK, and the honest limit the
// whole arrangement has.
func TestChecklistHasCheck68(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")

	row := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 68 |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("the checklist has no `| 68 |` table row")
	}
	for _, want := range []string{"USER-RUN, NOT RUN", "sandbox repository only", "after 67a and before 67b"} {
		if !strings.Contains(row, want) {
			t.Errorf("the `| 68 |` row does not carry %q", want)
		}
	}
	for _, want := range []string{
		"USER-RUN, NOT RUN",
		"run by you",
		"after 67a and before 67b",
		"once per installation",
		"once per repo checkout",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not say %q", want)
		}
	}

	blocks := bashBlocks(section)
	if len(blocks) < 6 {
		t.Fatalf("Check 68 has %d fenced bash blocks, want the guard block and the steps", len(blocks))
	}
	compareGuard(t, "68", blocks[0], check68Guard)

	for _, tag := range []string{
		"**(1) The one-time setup, and the identity the apply runs as** — **once per installation.**",
		"**(2) The two roles, permission by permission, read live** — **once per installation.**",
		"**(3) The negative FACT: it cannot write the marker or the delete list** — **once per repo checkout.**",
		"**(4) The transitive reach — each a real command as the deployer that must fail** — **once per repo checkout.**",
		"**(5) What the two roles are known to LACK, measured per type** — **once per repo checkout.**",
		"**(6) What D75 does not close, recorded rather than implied** — **once per repo checkout.**",
		"**Restore — once per installation, and it deletes only what this check created.**",
	} {
		if !strings.Contains(section, tag) {
			t.Errorf("Check 68 lacks the labelled step %q", tag)
		}
	}

	// The account and its two roles, and the two permission families that
	// must be absent from both.
	for _, want := range []string{
		"fugaro-deployer@",
		"fugaroDeployer",
		"fugaroDeployerScoped",
		"resourcemanager.projects.",
		"iam.roles.",
		"resource.name",
		"resource.type",
		"`fmgd-`",
		"the count is exactly **1**",
		"storage.buckets.create",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q", want)
		}
	}
	// Every negative, and the fact that it is a negative: a real command run
	// as the deployer whose FACT is its failure.
	for _, want := range []string{
		"must fail",
		"`fugaro/managed.json`",
		"fugaro/allow-delete.json",
		`"gs://${B:?}/fugaro/project.json"`,
		"iam.roles.update",
		"developer.gserviceaccount.com",
		"gcloud run jobs create",
		"access-token",
		"add-iam-policy-binding",
		"fugaro-tfstate-",
		"PERMISSION_DENIED",
		// The account whose token the deployer must not be able to mint: a
		// DELEGATION CHAIN, the deployer as the link before the last, because
		// `gcloud iam service-accounts get-access-token` is not a gcloud
		// subcommand at all (SDK 588.0.0 answers `Invalid choice:`) and a
		// command that fails that way measures nothing about actAs.
		`gcloud auth print-access-token --impersonate-service-account="${D:?},${NUM}-compute@developer.gserviceaccount.com"`,
		// The positive half of the same sentence: the one bucket it CAN write.
		"fugaro-managed-tfstate-",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q", want)
		}
	}
	// And the dead end that pin replaced: `fugaro`-style shorthand for
	// minting a token as an account is `gcloud iam service-accounts
	// get-access-token`, which gcloud does not have (the beta component
	// carries a variant and is not installed), so the command answers
	// `Invalid choice:` and the "the deployer cannot act as the compute
	// account" negative would be recorded as a pass for the wrong reason.
	if strings.Contains(section, "iam service-accounts get-access-token") {
		t.Error("Check 68 mints a token with a gcloud subcommand that does not exist: the negative would fail for the wrong reason")
	}
	// The chain is ONE comma-separated flag, not two flags: gcloud keeps only
	// the last REPEATED `--impersonate-service-account` (SDK 588.0.0 warns about
	// it), so the two-flag form builds no chain at all and the negative would be
	// run as the compute account directly — refused for the wrong reason and
	// proving nothing about the deployer's actAs. The section must carry the
	// comma form, the paragraph that says which account needs which grant for
	// each hop, and no repeated flag anywhere.
	if !strings.Contains(section, "--impersonate-service-account=\"${D:?},${NUM}-compute@developer.gserviceaccount.com\"") {
		// pinned above as a whole command; this is the comma form on its own, so
		// a rewrite of the command that drops the chain is named as a chain bug
		// rather than as a missing command.
		t.Error("Check 68's 4b no longer builds its chain as ONE comma-separated value")
	}
	for _, want := range []string{
		"**4b proves less than it looks like it proves, and 4c is the probe that tests the fence.**",
		"the operator needs `roles/iam.serviceAccountTokenCreator` on the deployer account",
		"`iam.serviceAccounts.actAs` is not that authority",
		"one comma-separated flag and not two flags",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not say %q, so the chain's failing hop is not the one the step is about", want)
		}
	}
	if strings.Contains(section, "--impersonate-service-account=\"${D:?}\" --impersonate-service-account=") {
		t.Error("Check 68 builds its delegation chain with a REPEATED --impersonate-service-account flag: gcloud keeps only the last one, so no chain is built")
	}
	// The apply prints no IAM read, so nothing anywhere in the three sections may
	// say it does (the claim step 3 of 67a used to make).
	if strings.Contains(section, "Record whether the apply in 67a printed the two reads") {
		t.Error("Check 68 asks the owner to record reads the apply does not print")
	}
	// The permissions the roles are known to LACK, each measured as its own
	// PERMISSION_DENIED before anything is added — and never added without the
	// fence that makes them safe.
	for _, want := range []string{
		"storage.buckets.getIamPolicy",
		"run.operations.",
		"serviceusage.operations.get",
		"apikeys.keys.getKeyString",
		"Artifact Registry",
		"never added unconditioned",
		"or the type is dropped from Task 1.1's list",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry the roles-lack measurement %q", want)
		}
	}
	// The limit, written down so a green run is not read as more than it is.
	for _, want := range []string{
		"wider than the deployer's",
		"D75's accepted cost",
		"§6 item 4",
		"an agent's session holds the operator's credentials",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not record D75's limit (%q)", want)
		}
	}
	// Step 5's root is written out in full before it is applied: one file per
	// row of the table, every name carrying the prefix the gate requires
	// (ManagedPrefix) and every type one the allowlist admits, so a row can be
	// put aside with one `mv` and measured alone. `fugaro managed apply` writes
	// no root for the reader.
	for _, want := range []string{
		"cat > ./managed-root-perm/main.tf <<EOF",
		`version = ">= 8.4.0, < 9.0.0"`,
		`provider "google" {`,
		"cat > ./managed-root-perm/sa.tf <<EOF",
		`account_id   = "fmgd-chk67-perm"`,
		"cat > ./managed-root-perm/bucket.tf <<EOF",
		`name     = "fmgd-chk67-perm-data-${M:?}"`,
		"cat > ./managed-root-perm/bucket-grant.tf <<EOF",
		"cat > ./managed-root-perm/job.tf <<EOF",
		"cat > ./managed-root-perm/service.tf <<EOF",
		"cat > ./managed-root-perm/key.tf <<EOF",
		"cat > ./managed-root-perm/repo.tf <<EOF",
		`mode          = "STANDARD_REPOSITORY"`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68's step 5 does not author the root it applies (%q)", want)
		}
	}
	mkperm := strings.Index(section, "mkdir -p ./managed-root-perm")
	firstPermApply := strings.Index(section, `fugaro managed apply "$RUN" --project "${F:?}"`)
	switch {
	case mkperm < 0:
		t.Error("no step of Check 68 writes ./managed-root-perm, so its permission rows apply a root that does not exist")
	case firstPermApply < 0:
		t.Error("Check 68 no longer applies a root built from ./managed-root-perm, so its permission rows measure nothing")
	case mkperm > firstPermApply:
		t.Error("Check 68 applies a permission root before any step authors the library it is built from")
	}
	// And every step names the directory it runs from, as 67a's do: the guard
	// block's stops hold in the sandbox checkout, not in the clone.
	for _, want := range []string{
		"cd /tmp/chk67-managed     # 67a's clone again",
		`cd "${SANDBOX:?}"                                                                                     # back in the sandbox checkout`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68's step does not name the directory it runs from (%q)", want)
		}
	}
	if !strings.Contains(section, "must read `0`") {
		t.Error("Check 68's Restore does not demand a `0` count on every line")
	}
	// Step 4e copies a file, so a step has to create it: the probe file step 3
	// makes is removed at the end of step 3, so a section that copies
	// /tmp/chk68-probe2.txt without writing it first sends the owner to a
	// `no such file` that says nothing about IAM. The create must come BEFORE
	// the copy, or it is the same dead end written in the other order.
	create := strings.Index(section, "printf x > /tmp/chk68-probe2.txt")
	use := strings.Index(section, `gcloud storage cp /tmp/chk68-probe2.txt "gs://fugaro-tfstate-${M:?}/fugaro/installation/probe.json"`)
	if create < 0 {
		t.Error("Check 68's step 4e copies /tmp/chk68-probe2.txt without any step creating it")
	} else if use < 0 {
		t.Error("Check 68 no longer copies the probe into the installation state bucket")
	} else if create > use {
		t.Error("Check 68's step 4e creates /tmp/chk68-probe2.txt after the copy that needs it")
	}
	// Step 6 overwrites the LIVE creation fact with the operator's own
	// credentials. It therefore has to keep the original first, carry its
	// CONFIRM on the written line rather than on the read after it, and put the
	// creation fact back before the check moves on — otherwise 67b runs on top
	// of a marker that reads as not managed.
	for _, want := range []string{
		`gcloud storage cp "gs://${B:?}/fugaro/managed.json" /tmp/chk68-marker.orig.json --project="${M:?}"`,
		`gcloud storage cp /tmp/chk68-operator.txt "gs://${B:?}/fugaro/managed.json" --project="${M:?}"       # ⚠ CONFIRM`,
		`gcloud storage cp /tmp/chk68-marker.orig.json "gs://${B:?}/fugaro/managed.json" --project="${M:?}"   # ⚠ CONFIRM`,
		"this read-back is what says the object is whole again",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68's step 6 does not carry %q", want)
		}
	}
	// And every file the section writes is in the Restore's own rm line, as one
	// command: a record file left in /tmp is a file the Restore's count of what
	// is gone cannot be right about.
	if !strings.Contains(section, "rm -f /tmp/chk68-deployer.txt /tmp/chk68-scoped.txt /tmp/chk68-operator.txt \\\n  /tmp/chk68-probe.txt /tmp/chk68-probe2.txt /tmp/chk68-probe3.txt /tmp/chk68-marker.orig.json") {
		t.Error("Check 68's Restore does not remove every file the check wrote")
	}
	assertProjectFlags(t, "68", blocks, 10, 1)
}

// compareGuard compares a section's first fenced block with the checked-in
// copy of it, line for line.
func compareGuard(t *testing.T, check string, guard, want []string) {
	t.Helper()
	if len(guard) != len(want) {
		t.Errorf("Check %s's guard block is %d lines, want %d", check, len(guard), len(want))
	}
	for i := range want {
		if i >= len(guard) {
			break
		}
		if guard[i] != want[i] {
			t.Errorf("Check %s's guard block line %d is\n  %q\nwant\n  %q", check, i+1, guard[i], want[i])
		}
	}
}

// assertProjectFlags is the rule that keeps a live check on the project it was
// written for. Two halves, and both are needed:
//
//   - every `gcloud` command carries `--project=`, so no argument is ever
//     empty and no ambient default is ever reached (the owner's ambient
//     project is a live one, which is the whole reason `P` is a constant);
//   - every `fugaro` command carries `--project`, with ONE exception the
//     sections spell out: the `fugaro init --create-project` that CREATES a
//     project takes `--name`, because `--project` names a config that does
//     not exist yet (`internal/cli/init.go`'s `--name` help,
//     `internal/localcfg/select.go`).
//
// `gcloud config`/`gcloud auth` are not project-scoped commands and carry no
// `--project`; a line that only PRINTS a command is a message, not a run.
func assertProjectFlags(t *testing.T, check string, blocks [][]string, minGcloud, minFugaro int) {
	t.Helper()
	gcloud, fugaro := 0, 0
	for i, block := range blocks {
		for _, line := range logicalCommands(block) {
			if strings.HasPrefix(line, "#") || strings.Contains(line, "echo ") {
				continue
			}
			// A trailing comment is documentation, not an argument: the guard
			// blocks' own variable declarations end in one, and without this the
			// word `fugaro` inside a comment reads as a command.
			if c := strings.Index(line, " #"); c >= 0 {
				line = strings.TrimSpace(line[:c])
			}
			if line == "" {
				continue
			}
			if strings.Contains(line, "gcloud ") &&
				!strings.HasPrefix(line, "gcloud config ") && !strings.HasPrefix(line, "gcloud auth ") {
				gcloud++
				if !strings.Contains(line, "--project=") {
					t.Errorf("Check %s block %d: %q runs gcloud without --project=", check, i, line)
				}
			}
			if !strings.Contains(line, "fugaro ") || !strings.Contains(line, " --") {
				continue
			}
			fugaro++
			if strings.Contains(line, "--project") {
				continue
			}
			if strings.Contains(line, "fugaro init --create-project") && strings.Contains(line, `--name "${F:?}"`) {
				continue
			}
			t.Errorf("Check %s block %d: %q takes flags but names no installation", check, i, line)
		}
	}
	if gcloud < minGcloud || fugaro < minFugaro {
		t.Errorf("Check %s has %d gcloud and %d fugaro commands, want at least %d and %d: the flag rule above has too few subjects to mean anything", check, gcloud, fugaro, minGcloud, minFugaro)
	}
}

// TestManagedChecklistAddressVariablesAreGuarded is the rule the review found
// broken: a `${VAR:?}` a step uses has to be a variable the check's OWN setup
// block declares and its OWN first hard stop refuses empty. A step that uses
// `${TERRAFORM_ADDRESS:?}` while no setup block declares it is a block that
// dies on an unbound variable at the owner's terminal, and a declaration the
// stop does not read is an empty address handed to the one command in Fugaro
// that approves a delete — so both halves are asserted here, for every
// variable the three sections use in the `${VAR:?}` form.
func TestManagedChecklistAddressVariablesAreGuarded(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	uses := regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*):\?`)
	for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
		section := docSection(t, path, doc, h)
		blocks := bashBlocks(section)
		if len(blocks) == 0 {
			t.Fatalf("%s has no fenced bash block", h)
		}
		guard := strings.Join(blocks[0], "\n")
		declared, stopped := map[string]bool{}, map[string]bool{}
		for _, m := range regexp.MustCompile(`(?m)^([A-Z_][A-Z0-9_]*)=`).FindAllStringSubmatch(guard, -1) {
			declared[m[1]] = true
		}
		// The stop's condition and its message are two lines of one compound
		// command, so the `[ -n "$VAR" ]` clauses are read out of the block as
		// a whole rather than out of the message's own line.
		for _, m := range regexp.MustCompile(`\[ -n "\$([A-Z_][A-Z0-9_]*)" \]`).FindAllStringSubmatch(guard, -1) {
			stopped[m[1]] = true
		}
		// `P` is not refused empty but pinned to its one value, which is the
		// stronger form Check 43 uses and the same guard.
		for _, m := range regexp.MustCompile(`\[ "\$([A-Z_][A-Z0-9_]*)" = [^]]+ \]`).FindAllStringSubmatch(guard, -1) {
			stopped[m[1]] = true
		}
		if !strings.Contains(guard, "stopped: re-run the whole setup block") {
			t.Fatalf("%s's guard block carries no first hard stop", h)
		}
		// The three of this section's own variables that the steps reach for.
		// `P`, `F`, `M`, `L`, `R`, `B`, `N`, `D`, `ACCOUNT_ID` and `AMOUNT`
		// are the same rule; the test applies it to every one of them at once
		// rather than listing them, so a new variable cannot be added to a step
		// without its guard.
		named := map[string]bool{}
		for _, b := range blocks[1:] {
			for _, m := range uses.FindAllStringSubmatch(strings.Join(b, "\n"), -1) {
				named[m[1]] = true
			}
		}
		for name := range named {
			if !declared[name] {
				t.Errorf("%s's steps use ${%s:?} and its setup block never declares %s=", h, name, name)
			}
			if !stopped[name] {
				t.Errorf("%s declares %s= and its first hard stop never refuses an empty %s", h, name, name)
			}
		}
		// The two checks that hand `fugaro allow-delete` an address are the two
		// this test exists for: if neither reaches for it any more, the rule
		// above has no subject and this test would pass on an empty section.
		if h != "## Check 68:" && !named["TERRAFORM_ADDRESS"] {
			t.Errorf("%s no longer uses ${TERRAFORM_ADDRESS:?}, so this test has nothing to guard", h)
		}
	}
}

// TestManagedChecklistSectionsArePinned is the structural half: the three
// sections exist, in order, between the checks they belong with, every fenced
// bash block parses (the shape an unclosed quote breaks), no section names an
// installation it must never touch, and none of them carries a secret-shaped
// literal.
func TestManagedChecklistSectionsArePinned(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	order := []string{"## Check 58", "## Check 67a:", "## Check 67b:", "## Check 68:"}
	at := -1
	for _, h := range order {
		i := strings.Index(doc, h)
		if i < 0 {
			t.Fatalf("docs/gcp-live-checklist.md has no %q heading", h)
		}
		if i < at {
			t.Errorf("the checklist's %q heading is out of order", h)
		}
		at = i
	}

	// The summary table's rows, in the same order and next to Check 58's: a
	// check with no row is a check an owner reading the table never finds.
	var rows []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| ") {
			if f := strings.SplitN(strings.TrimPrefix(line, "| "), " |", 2); len(f) == 2 {
				rows = append(rows, f[0])
			}
		}
	}
	pos := func(id string) int {
		for i, r := range rows {
			if r == id {
				return i
			}
		}
		return -1
	}
	p58, p67a, p67b, p68, p69a := pos("58"), pos("67a"), pos("67b"), pos("68"), pos("69a")
	if p67a < 0 || p67b < 0 || p68 < 0 {
		t.Fatalf("the summary table is missing a managed-projects row (67a=%d 67b=%d 68=%d)", p67a, p67b, p68)
	}
	if p58 < 0 || p69a < 0 {
		t.Fatal("the summary table no longer holds checks 58 and 69a, so the placement asserted here is not the one intended")
	}
	if !(p58 < p67a && p67a < p67b && p67b < p68 && p68 < p69a) {
		t.Errorf("the managed-projects rows are not between checks 58 and 69a in order: 58=%d 67a=%d 67b=%d 68=%d 69a=%d", p58, p67a, p67b, p68, p69a)
	}

	// And the SECTIONS sit where the table says they do: the three of them
	// between Check 58's heading and Check 69a's. The order test above only
	// walks the four headings among themselves, so three sections appended
	// after Check 70c — where they were, with the table still promising them
	// between 58 and 69a — satisfied it, and the placement a reader follows was
	// pinned by nothing.
	i58 := strings.Index(doc, "## Check 58")
	i67a := strings.Index(doc, "## Check 67a:")
	i67b := strings.Index(doc, "## Check 67b:")
	i68 := strings.Index(doc, "## Check 68:")
	i69a := strings.Index(doc, "## Check 69a:")
	if i58 < 0 || i67a < 0 || i67b < 0 || i68 < 0 || i69a < 0 {
		t.Fatalf("the checklist is missing one of the headings this placement is asserted over (58=%d 67a=%d 67b=%d 68=%d 69a=%d)", i58, i67a, i67b, i68, i69a)
	}
	if !(i58 < i67a && i67a < i67b && i67b < i68 && i68 < i69a) {
		t.Errorf("the three managed sections do not sit between Check 58 and Check 69a, which is where the summary table puts them: 58=%d 67a=%d 67b=%d 68=%d 69a=%d", i58, i67a, i67b, i68, i69a)
	}

	// `belong`, EdgeWeb and EdgeServer are the installations and repositories
	// these checks must never name as anything but forbidden. They are not
	// named at all here: the guard proves the boundary structurally instead
	// (`P` is a constant, the origin must be the sandbox), which is stronger
	// than a sentence, and a sentence is what a reader skims.
	for _, bad := range []string{"`belong`", "EdgeWeb", "EdgeServer"} {
		for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
			if strings.Contains(docSection(t, path, doc, h), bad) {
				t.Errorf("%s names %q", h, bad)
			}
		}
	}

	// The citations. Three of them are the SYMBOL, because 0.8.6 and 0.8.7
	// moved every line number this section used to carry — each was six to
	// twelve lines out on the merged tree — and a number that was true when it
	// was written is a number a reader is sent to the wrong place by. The one
	// line citation that stays is the Terraform one,
	// which names a resource and the `role` line in it; that file has not
	// moved, and a misplaced line there is checked below.
	for _, want := range []string{
		"`managedApply` in `internal/cli/managed_apply.go`",
		"`restoreGCPProjectLine`",
		"`loadInitConfig`",
		"deploy/terraform/gcp/modules/workflow/sa.tf:12",
	} {
		found := false
		for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
			if strings.Contains(docSection(t, path, doc, h), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no managed section carries the corrected citation %q", want)
		}
	}
	// The needles for restore.go are split, exactly as the check below splits
	// its own: this file is read by that test for the shape `restore.go:<n>`,
	// so writing one here would fail it.
	for _, bad := range []string{
		// The numbers these sections carried before the merge, each one a
		// line off or more on the tree this branch now stands on.
		"internal/cli/managed_apply.go:289",
		"internal/cli/managed_apply.go:288",
		"internal/cli/restore.go:" + "1015",
		"restore.go:" + "1042",
		"internal/cli/init.go:1070",
		"deploy/terraform/gcp/modules/workflow/sa.tf:13",
	} {
		for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
			if strings.Contains(docSection(t, path, doc, h), bad) {
				t.Errorf("%s cites %q, which is a stale line citation", h, bad)
			}
		}
	}
	// And the Terraform line citation that stays is the right one: the
	// operator's grant is `google_service_account_iam_member.operator`, and
	// its `role` line follows the resource. A citation one line out sends the
	// reader to a blank line, so both halves are checked against the file.
	if l := declaredLine(t, "../../deploy/terraform/gcp/modules/workflow/sa.tf", `resource "google_service_account_iam_member" "operator"`); !strings.HasSuffix(l, "sa.tf:12") {
		t.Errorf("the checklist cites deploy/terraform/gcp/modules/workflow/sa.tf:12 for the operator's grant, and that file declares it on %s now", l)
	}

	// No secret-shaped literal, in a section whose whole subject is IAM: the
	// check reads permissions and bindings, never a key or a token value.
	for _, bad := range []string{"AIza", "-----BEGIN", "PRIVATE KEY", "client_secret", "Bearer "} {
		for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
			if strings.Contains(docSection(t, path, doc, h), bad) {
				t.Errorf("%s carries the secret-shaped literal %q", h, bad)
			}
		}
	}

	// Every fenced block in the three sections is meant to be pasted into a
	// shell and run. An apostrophe inside a ${VAR:?...} message, or any other
	// unclosed quote, makes a block that dies on its first line — the whole
	// file is checked for the first shape above, and bash -n is the general
	// one, and it needs nothing installed and no cloud.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Log("bash is not on PATH: the syntax half of this test did not run")
		return
	}
	for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
		blocks := bashBlocks(docSection(t, path, doc, h))
		if len(blocks) < 6 {
			t.Errorf("%s has %d fenced bash blocks; the extractor or the section lost some", h, len(blocks))
		}
		for i, b := range blocks {
			f := filepath.Join(t.TempDir(), fmt.Sprintf("managed-%d.sh", i))
			if err := os.WriteFile(f, []byte(strings.Join(b, "\n")), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bash, "-n", f).CombinedOutput(); err != nil {
				t.Errorf("%s's fenced block %d does not parse as bash (%v): %s", h, i, err, out)
			}
		}
	}
}

// TestManagedChecklistSectionsUseDocSection is the review's second finding:
// managedCheckSection was a copy of docSection (docs_providers_test.go), same
// package, same behaviour, and a second copy is a second place for the
// boundary rule to drift. The helper is gone; what is pinned here is both
// halves of that: no `managedCheckSection` in this file, and the boundary
// docSection itself draws — measured on the real document, because the three
// managed sections are the ones whose trimming now matters (67a must stop at
// 67b's heading, and 68 at 69a's).
func TestManagedChecklistSectionsUseDocSection(t *testing.T) {
	src, err := os.ReadFile("docs_managed_projects_test.go")
	if err != nil {
		t.Fatal(err)
	}
	// The needles are split so this test's own source -- which carries the
	// words below -- is not what it finds.
	name := "managedCheckSection"
	if strings.Contains(string(src), "func "+name+"(") {
		t.Error("docs_managed_projects_test.go defines managedCheckSection again: docSection is the same helper and lives in docs_providers_test.go")
	}
	if strings.Contains(string(src), name+"(t,") {
		t.Error("docs_managed_projects_test.go still calls managedCheckSection: use docSection")
	}

	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	// The boundary, on the three sections: each ends at the NEXT `## ` heading.
	for _, c := range []struct{ heading, next string }{
		{"## Check 67a:", "## Check 67b:"},
		{"## Check 67b:", "## Check 68:"},
		{"## Check 68:", "## Check 69a:"},
	} {
		section := docSection(t, path, doc, c.heading)
		if !strings.HasPrefix(section, c.heading) {
			t.Errorf("docSection(%q) does not start at its own heading", c.heading)
		}
		if strings.Contains(section, c.next) {
			t.Errorf("docSection(%q) runs past %q, so every assertion anchored to it reads the next section too", c.heading, c.next)
		}
	}
	// And the boundary the other way: the section before it is not swallowed.
	if strings.Contains(docSection(t, path, doc, "## Check 58"), "## Check 67a:") {
		t.Error("docSection(\"## Check 58\") runs into Check 67a")
	}
}

// TestManagedChecklistCitationsNameSymbols is the review's first finding, twice
// over: the comment in this file cited restoreGCPProjectLine by line number and
// the number was stale by two, and then the 0.8.6 merge showed the same thing
// about every line citation the managed sections carried — the restore.go one
// moved by six lines, the managed-apply print and the init refusal by five and
// twelve. The rule now is
// the symbol for all three, in this file and in the sections that make the same
// claims; the one place a line is still the subject is the Terraform resource,
// whose file has not moved, and that citation is checked against the file.
func TestManagedChecklistCitationsNameSymbols(t *testing.T) {
	line := regexp.MustCompile(`restore\.go:\d+`)

	src, err := os.ReadFile("docs_managed_projects_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if m := line.FindString(string(src)); m != "" {
		t.Errorf("docs_managed_projects_test.go cites restore.go by line (%s): cite restoreGCPProjectLine, the symbol", m)
	}
	if !strings.Contains(string(src), "restoreGCPProjectLine") {
		t.Error("docs_managed_projects_test.go no longer names restoreGCPProjectLine at all")
	}

	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67a:")
	if !strings.Contains(section, "`restoreGCPProjectLine`") {
		t.Error("Check 67a no longer cites restoreGCPProjectLine by name")
	}
	// The doc cited this by line, and the line was stale the moment 0.8.6
	// landed: the symbol is what the file can be read by, and the symbol is
	// what the section names now. The declaration is still read out of the
	// source, so a rename fails here rather than leaving a name in the doc
	// that the tree no longer carries.
	declaredLine(t, "restore.go", "func restoreGCPProjectLine(")
	for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
		if got := line.FindAllString(docSection(t, path, doc, h), -1); len(got) > 0 {
			t.Errorf("%s cites restore.go by line (%v): cite restoreGCPProjectLine, the symbol", h, got)
		}
	}
	// And the line citation that stays is the Terraform one, which names a
	// resource: Check 67a keeps it, and it is still the resource's own line.
	if !strings.Contains(section, "deploy/terraform/gcp/modules/workflow/sa.tf:12") {
		t.Error("Check 67a no longer carries the line citation deploy/terraform/gcp/modules/workflow/sa.tf:12")
	}
	// The three citations that moved are symbols in every one of the three
	// sections that carries them: a reader following them is not sent to a
	// line that was true for one release.
	for _, want := range []string{"`managedApply`", "`restoreGCPProjectLine`", "`loadInitConfig`"} {
		found := false
		for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
			if strings.Contains(docSection(t, path, doc, h), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no managed section names %s, so the citation that moved is neither a line nor a symbol", want)
		}
	}
}

// TestRestoreReadsTheConfigBeforeTheFreshIDRule is the review's third finding,
// in the production order it is about: Check 67b's step 2 says
// restoreNewProjectID "runs before the config is read and before any cloud
// call", and restorePreflight calls selectProject — which reads the project
// config the rule then takes as its argument — first. The order is pinned in
// the source, and the claim is pinned in the section.
func TestRestoreReadsTheConfigBeforeTheFreshIDRule(t *testing.T) {
	src, err := os.ReadFile("restore.go")
	if err != nil {
		t.Fatal(err)
	}
	// bodyOf is the pattern the other source-reading tests in this package
	// use (init_retires_leftover_test.go): from the signature to the next
	// top-level func, so an assertion cannot be satisfied by a call elsewhere
	// in the file.
	bodyOf := func(sig string) string {
		t.Helper()
		i := strings.Index(string(src), sig)
		if i < 0 {
			t.Fatalf("restore.go no longer declares %s", sig)
		}
		body := string(src)[i:]
		if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
			body = body[:j+1]
		}
		return body
	}
	body := bodyOf("func restorePreflight(")
	sel := strings.Index(body, "selectProject(ctx, o.cloud)")
	// The bare call, not the one statement: what the claim is about is the
	// rule running, and an earlier call -- of any shape -- would break it.
	fresh := strings.Index(body, "restoreNewProjectID(")
	switch {
	case sel < 0 || fresh < 0:
		t.Fatalf("restorePreflight no longer holds both the selection (%d) and the fresh-ID rule (%d): the order this test pins is not the order it has", sel, fresh)
	case sel > fresh:
		t.Error("restorePreflight now calls restoreNewProjectID before the selection: Check 67b's step 2 must stop claiming the config is read after it, and this test says which one moved")
	}
	// restoreNewProjectID reads the config it is handed rather than reading one
	// itself, which is why the refusal can be before the budget and the
	// creation and still after the config.
	rule := bodyOf("func restoreNewProjectID(")
	if !strings.Contains(rule, "lc *localcfg.Config") {
		t.Error("restoreNewProjectID no longer takes the project config as its argument")
	}

	const path = "docs/gcp-live-checklist.md"
	section := docSection(t, path, readDoc(t, path), "## Check 67b:")
	if strings.Contains(section, "runs before the config is read") {
		t.Error("Check 67b's step 2 still says restoreNewProjectID runs before the config is read, and restorePreflight selects (and so reads) it first")
	}
	for _, want := range []string{
		"runs after the selection has read the project config",
		"before the root, the checkout's `gcp_project:` line, the old project's status, the budget and any creation",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67b's step 2 does not say %q", want)
		}
	}
}

// declaredLine returns "file:line" for the line file (read from this
// package's own directory) declares signature on, so a citation in the
// checklist can be checked against the source it points at rather than
// trusted. The last occurrence wins is NOT assumed: the first line whose
// trimmed text starts with the signature is the declaration, and a signature
// the file does not carry fails the test instead of returning 0.
func declaredLine(t *testing.T, file, signature string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, signature) {
			return fmt.Sprintf("%s:%d", file, i+1)
		}
	}
	t.Fatalf("%s no longer declares %s: the citation this test checks against has moved or gone", file, signature)
	return ""
}

// TestChecklist67aDirectoryRuleMatchesItsSteps is the review's finding against
// Check 67a's Method paragraph: the paragraph is the normative statement of
// which directory each step runs from, and it listed step 9 among the steps
// that act in /tmp/chk67-managed — while step 9's own block opens with
// `cd "${SANDBOX:?}"` and its heading says "once per repo checkout", exactly
// like steps 5 and 8. A reader who trusts the paragraph runs step 9 where its
// own guard block cannot be re-run. The paragraph is pinned as the sentence it
// has to be, and the sentence is pinned against the steps below it: each
// step's first `cd` decides which side it is on, and the two sets are pinned.
func TestChecklist67aDirectoryRuleMatchesItsSteps(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67a:")

	const clause = "steps 2 to 4, 6 and 7 in `/tmp/chk67-managed`, and steps 5, 8 and 9 back in the sandbox checkout"
	if !strings.Contains(section, clause) {
		t.Errorf("Check 67a's Method does not state the directory rule as %q, so the paragraph and the steps below it can disagree", clause)
	}

	// Where each step's own block actually starts: the first `cd` in the
	// step's own bash. The guard block is the first fenced block of the
	// section and belongs to no step, so the blocks are read step by step off
	// the `**(N) ` headings.
	where := map[string]int{} // step heading number -> 1 managed, 0 sandbox
	for _, part := range strings.Split(section, "\n**(")[1:] {
		head, body, ok := strings.Cut(part, "\n")
		if !ok {
			continue
		}
		n, _, ok := strings.Cut(head, ")")
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(n); err != nil {
			continue
		}
		for _, block := range bashBlocks(body) {
			if got := firstCD(block); got != "" {
				switch got {
				case `cd "${SANDBOX:?}"`:
					where[n] = 0
				case "cd /tmp/chk67-managed":
					where[n] = 1
				}
				break
			}
		}
	}
	// Step 1 is its own case and the paragraph says so separately -- it starts
	// in the sandbox checkout and ends in the clone it makes -- so the rule
	// derived here covers steps 2 to 9.
	if !strings.Contains(section, "step 1 acts in the managed checkout it clones below") {
		t.Error("Check 67a's Method no longer gives step 1 its own clause, and step 1 is the one step that moves between the two directories")
	}
	var sandbox, managed []string
	for _, n := range []string{"2", "3", "4", "5", "6", "7", "8", "9"} {
		switch where[n] {
		case 0:
			sandbox = append(sandbox, n)
		case 1:
			managed = append(managed, n)
		default:
			t.Errorf("Check 67a's step %s names no directory in its first command", n)
		}
	}
	if got, want := strings.Join(sandbox, ","), "5,8,9"; got != want {
		t.Errorf("Check 67a's steps that run in the sandbox checkout are %s, and the Method's rule has to say so (want %s)", got, want)
	}
	if got, want := strings.Join(managed, ","), "2,3,4,6,7"; got != want {
		t.Errorf("Check 67a's steps that run in the managed checkout are %s, and the Method's rule has to say so (want %s)", got, want)
	}
}

// firstCD is the first line of block that is a `cd`, with its trailing
// comment stripped: a step's own block says where it runs on that line.
func firstCD(block []string) string {
	for _, line := range logicalCommands(block) {
		if strings.HasPrefix(line, "cd ") {
			if c := strings.Index(line, " #"); c >= 0 {
				line = strings.TrimSpace(line[:c])
			}
			return line
		}
	}
	return ""
}

// TestChecklistHasCheck67bRootMovesToTheNewProject is the review's major
// finding: 67a step 2 authors ./managed-root with the project written out, and
// the literal it writes is the OLD project's. The restore's last act is a
// managed apply — the same managedApply `fugaro managed apply` runs — and the
// gate it builds its cover from names the config the restore has just moved,
// so a root still naming the old project is refused by the gate's own project
// rule before anything is applied: step 3's from-zero FACT never happens, and
// step 4's expected delete refusal is masked by this one. The step that
// rewrites the two files is pinned here, together with the code that makes it
// necessary — a doc step and the refusal it exists to avoid have to move
// together.
func TestChecklistHasCheck67bRootMovesToTheNewProject(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67b:")

	const heading = "**(3) The root moves to the new project before the restore applies it** — **once per repo checkout"
	if !strings.Contains(section, heading) {
		t.Fatalf("Check 67b has no step 3 that moves the root to the new project, so its restore applies a root naming the old one")
	}
	// The step is 3 and the restore is 4: the rewrite has to be before the
	// command whose apply reads it.
	if strings.Index(section, heading) > strings.Index(section, "**(4) The restore itself**") {
		t.Error("Check 67b's root rewrite comes after the restore, whose apply is what reads it")
	}
	rootMove := docSection(t, path, section, heading)
	for _, want := range []string{
		// both files, and the literal, in the portable form 67a step 2 uses.
		`sed -e 's/project *= *"'"${M:?}"'"/project = "'"${N:?}"'"/' ./managed-root/main.tf`,
		`sed -e 's/project *= *"'"${M:?}"'"/project = "'"${N:?}"'"/' ./managed-root/account.tf`,
		"NOT `sed -i`",
		"terraform -chdir=./managed-root fmt -check",
		// The edits do not show up in a `git diff`: nothing commits the root.
		"git status --porcelain ./managed-root",
		"UNTRACKED",
		"step 4's `git diff` still shows only",
	} {
		if !strings.Contains(rootMove, want) {
			t.Errorf("Check 67b's root rewrite does not carry %q", want)
		}
	}
	if strings.Contains(rootMove, "cd -") {
		t.Error("Check 67b's root rewrite returns with `cd -`")
	}

	// The code the step exists for: the gate names the cover's project, and
	// the cover is the config's own project — which the restore has moved by
	// the time the apply runs. The refusal the step quotes has to be the
	// string the gate prints, not a paraphrase.
	coverSrc, err := os.ReadFile("managed_apply.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(coverSrc), "Projects: []string{lc.GCPProject}") {
		t.Error("managedApply no longer builds its cover from the selected config's project: Check 67b's root rewrite may no longer be what the gate reads")
	}
	gatesrc, err := os.ReadFile("../../internal/infra/tf/managed_cover.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gatesrc), `", which is not this run's)"`) {
		t.Error("the gate no longer refuses a provider project that is not this run's, which is the refusal Check 67b's root rewrite avoids")
	}
	if !strings.Contains(rootMove, `which is not this run's`) {
		t.Error("Check 67b's root rewrite does not name the refusal it exists to avoid")
	}
	restoreSrc, err := os.ReadFile("restore.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(restoreSrc), "restoreManagedApply = managedApply") {
		t.Error("the restore's final apply is no longer managedApply, so Check 67b's root rewrite and this test's premise need re-reading")
	}
}

// TestChecklistHasCheck67bMissingSecretColumnIsReadNotPrinted is the review's
// third finding: step 3 listed the missing-secret column among the FACT lines
// "in the order the command prints them", and `fugaro restore` prints no such
// column — MISSING belongs to `fugaro secrets ls --repo`, and the creating
// path the restore runs prints none. A person following the step would write
// down a FACT the command never printed. The column is now a read of its own,
// named as one, and the "prints them" list is pinned not to carry it.
func TestChecklistHasCheck67bMissingSecretColumnIsReadNotPrinted(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67b:")

	const marker = "**FACT lines, in the order the command prints them:**"
	idx := strings.Index(section, marker)
	if idx < 0 {
		t.Fatalf("Check 67b's restore no longer lists its FACT lines as what the command prints")
	}
	printed := section[idx:]
	if next := strings.Index(printed[len(marker):], "\n\n"); next >= 0 {
		printed = printed[:len(marker)+next]
	}
	for _, bad := range []string{"missing-secret", "MISSING"} {
		if strings.Contains(printed, bad) {
			t.Errorf("Check 67b lists %q among the FACT lines the restore prints, and the restore prints no missing-secret column", bad)
		}
	}
	// Where the column is read instead, as a command, after the restore.
	if !strings.Contains(section, `fugaro secrets ls --project "${F:?}" --repo`) {
		t.Error("Check 67b does not name the read that carries the missing-secret column")
	}
	if !strings.Contains(section, "not printed by the restore") {
		t.Error("Check 67b does not say the missing-secret column is not printed by the restore")
	}
	// And the code's half: the column is the --repo listing's, and no restore
	// report prints one.
	secSrc, err := os.ReadFile("secrets.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secSrc), `"NAME\tSECRET\tSCOPE\tMISSING\tLATEST\tVERSIONS\tCREATED\tLABELS"`) {
		t.Error("the MISSING column is no longer the --repo listing's header, so the doc's read names the wrong command")
	}
	restoreSrc, err := os.ReadFile("restore.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(restoreSrc), "MISSING") {
		t.Error("the restore now prints a MISSING column, so Check 67b may list it as printed again")
	}
}

// check68Step returns the body of one numbered step of Check 68, from its
// `**(N) ` heading to the next one, so a pin reads the step and not the
// section around it.
func check68Step(t *testing.T, section, head string) string {
	t.Helper()
	i := strings.Index(section, head)
	if i < 0 {
		t.Fatalf("Check 68 has no step beginning %q", head)
	}
	step := section[i:]
	if j := strings.Index(step[len(head):], "\n**("); j >= 0 {
		step = step[:len(head)+j]
	}
	return step
}

// TestChecklistHasCheck68NegativeProbesAreNonDestructive is the review's first
// finding: step 3's probes copied probe bytes ONTO the live creation fact
// (`fugaro/managed.json`), the delete list and `project.json`, with no copy of
// what stood there. On the expected path the write is refused and nothing is
// lost — but the probe's own premise is "this permission is absent", and a
// check that destroys the object when its premise turns out to be wrong is
// worse than no check. Two designs are pinned here, one per property being
// measured: a throwaway object name where the permission is the bucket-level
// write, and a byte-exact backup plus a must-not-exist precondition where the
// property is the named object itself.
func TestChecklistHasCheck68NegativeProbesAreNonDestructive(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	step := check68Step(t, section, "**(3) The negative FACT: it cannot write the marker or the delete list**")

	var cmds []string
	for _, b := range bashBlocks(step) {
		cmds = append(cmds, logicalCommands(b)...)
	}

	// 1. No probe may name one of the three live objects without a
	// precondition: `--if-generation-match=0` writes only where there is no
	// object, so a probe of an object that IS there cannot overwrite it.
	named := 0
	for _, c := range cmds {
		if !strings.Contains(c, "chk68-probe.txt") || !strings.Contains(c, "gcloud storage cp") {
			continue
		}
		if !strings.Contains(c, "/fugaro/managed.json") &&
			!strings.Contains(c, "/fugaro/allow-delete.json") &&
			!strings.Contains(c, "/fugaro/project.json") {
			continue
		}
		named++
		if !strings.Contains(c, "--if-generation-match=0") {
			t.Errorf("Check 68's probe of a live object carries no precondition, so it can overwrite the object it names:\n  %s", c)
		}
	}
	if named < 3 {
		t.Errorf("Check 68's step 3 probes %d of the three named objects (fugaro/managed.json, fugaro/allow-delete.json, fugaro/project.json): the FACT is about all three", named)
	}

	// 2. The bucket-level write is measured on a throwaway name under the same
	// prefix — a name that cannot be any object this check reads — and the
	// object is removed afterwards ONLY if the write succeeded.
	for _, want := range []string{
		`PROBE="fugaro/chk68-probe-`,
		"cannot be `fugaro/managed.json`",
		"only if the write succeeded",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 3 does not carry %q, so its bucket-level probe is not non-destructive by construction", want)
		}
	}

	// 3. The named objects are backed up byte-exactly BEFORE the probes, the
	// backup is read back and compared, and the doc says what to do if a probe
	// unexpectedly succeeds.
	for _, want := range []string{
		"chk68-managed.orig.json",
		"chk68-allow-delete.orig.json",
		"chk68-project.orig.json",
		"cmp - ",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 3 does not carry %q", want)
		}
	}
	// The way back carries its CONFIRM on the command itself, not only in the
	// prose above it: this is the one line in the check that writes the live
	// creation fact with the owner's own credentials.
	recovered := false
	for _, c := range cmds {
		if strings.Contains(c, "chk68-managed.orig.json") && strings.Contains(c, "⚠ CONFIRM") {
			recovered = true
		}
	}
	if !recovered {
		t.Error("Check 68's step 3 restores the creation fact from its backup without a ⚠ CONFIRM on the command, and that write is the owner's own")
	}

	// The order is read off the commands, not the prose around them: the first
	// backup command has to come before the first preconditioned copy.
	code := strings.Join(cmds, "\n")
	if strings.Index(code, "chk68-managed.orig.json") > strings.Index(code, "--if-generation-match=0") {
		t.Error("Check 68's step 3 takes its backup after the probes, and a backup taken after the write is not a backup")
	}

	// 4. Which design each negative uses, and why: the reader has to know
	// which half is protected by the name and which by the copy.
	for _, want := range []string{
		"non-destructive by construction",
		"byte-exact",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 3 does not say %q, so which negative uses which design is left to the reader", want)
		}
	}

	// 5. The precondition flag is the pinned SDK's own, and the doc says so.
	if !strings.Contains(step, "588.0.0") {
		t.Error("Check 68's step 3 uses --if-generation-match without naming the SDK it was checked against")
	}
	if !strings.Contains(step, "gcloud storage cp --help") {
		t.Error("Check 68's step 3 does not say where the precondition flag was checked")
	}

	// 6. The scratch the step now leaves is in the Restore's own list.
	if !strings.Contains(section, "chk68-managed.orig.json") {
		t.Error("Check 68's Restore does not remove the backups step 3 writes")
	}

	// 7. The summary row a reader meets first says the same thing: a reader who
	// stops at the table must not take away "a cp of fugaro/managed.json" with
	// nothing about the precondition and the backup.
	row := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 68 |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("the checklist has no `| 68 |` table row")
	}
	for _, want := range []string{
		"non-destructive by construction",
		"backed up byte-exactly",
		"conditioned on the destination object not existing",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("the `| 68 |` row does not carry %q, so the table still describes the probes as plain overwrites", want)
		}
	}
}

// TestChecklistHasCheck68RowAloneExampleIsolatesTheRow is the review's second
// finding, twice over. The first version moved three of the five rows aside and
// measured a "bucket-grant row alone" that was really two rows; the second moved
// every other row aside after applying them all, which the shared state makes
// worse than wrong: every root of a managed project writes the SAME state object
// (infra.StatePrefixManaged is "fugaro/managed", one key, no per-root suffix), so
// the files moved aside are planned DELETES, and the delete gate runs BEFORE the
// cover — the refusal recorded against the row would be the delete gate's, and
// the row would not have been measured at all. A row is now measured in its own
// FRESH root, built from the library of fixtures and row files, with the state
// read before and after it and the owner's delete list used (only) when the row
// applied.
func TestChecklistHasCheck68RowAloneExampleIsolatesTheRow(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	step := check68Step(t, section, "**(5) What the two roles are known to LACK, measured per type**")

	var cmds []string
	for _, b := range bashBlocks(step) {
		cmds = append(cmds, logicalCommands(b)...)
	}
	code := strings.Join(cmds, "\n")

	// 1. The five rows of the table are authored as a LIBRARY, one file each.
	for _, r := range []string{"bucket-grant", "job", "service", "key", "repo"} {
		if !strings.Contains(step, "cat > ./managed-root-perm/"+r+".tf <<EOF") {
			t.Errorf("Check 68's step 5 no longer authors ./managed-root-perm/%s.tf, so the table's rows and the library have drifted apart", r)
		}
		if !strings.Contains(step, "./managed-root-perm/"+r+".tf") {
			t.Errorf("Check 68's step 5 loses ./managed-root-perm/%s.tf between the table and the library that is moved into rows/", r)
		}
	}
	if !strings.Contains(step, "mkdir -p ./managed-root-perm/rows") {
		t.Error("Check 68's step 5 has no library directory, so a row's file and the fixtures are not separated from what is applied")
	}

	// 2. And a row is measured in a FRESH root holding the fixtures and that row:
	// the directory is removed and rebuilt, the two fixtures and the row are
	// copied in, and the local reads say what the root holds before it applies.
	for _, want := range []string{
		`RUN="./managed-root-perm/run-$ROW"`,
		`rm -rf "$RUN"; mkdir -p "$RUN"`,
		`cp "./managed-root-perm/rows/$ROW.tf" "$RUN/row.tf"`,
		`ls "$RUN"/*.tf`,
		`grep -H '^resource' "$RUN"/*.tf`,
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 5 does not carry %q, so a row is not measured in a fresh root of its own", want)
		}
	}
	if strings.Contains(step, "managed-root-perm/*.tf") {
		t.Error("Check 68's step 5 still confirms the library directory itself, which is not the root a row is applied from")
	}

	// 3. The apply is made against THAT root, with the coding agent's marker
	// inline: without it the operator is asked to type the installation's name
	// (managed.Request.confirm), which would be read as a refused row.
	apply := ""
	for _, c := range cmds {
		if strings.Contains(c, "fugaro managed apply") {
			apply = c
		}
	}
	if apply == "" {
		t.Fatal("Check 68's step 5 has no `fugaro managed apply` command, so nothing measures a row")
	}
	if !strings.Contains(apply, `"$RUN"`) {
		t.Errorf("Check 68's step 5 applies something other than the row's own fresh root:\n  %s", apply)
	}
	if !strings.HasPrefix(apply, "AI_AGENT=chk67 ") {
		t.Errorf("Check 68's step 5 applies without the coding agent's marker, so a person at a terminal is asked to confirm and a refused row is recorded:\n  %s", apply)
	}
	if strings.Contains(code, "export AI_AGENT") {
		t.Error("Check 68's step 5 exports the agent marker, and `fugaro allow-delete` refuses to run in a session that carries one")
	}

	// 4. Nothing is moved aside any more, and the aside directory the earlier
	// design used is gone from the section — including from its Restore.
	if strings.Contains(section, "chk67-perm-aside") {
		t.Error("Check 68 still names the aside directory of the design that measured a row by moving the others out of an already-applied root")
	}
	for _, c := range cmds {
		if !strings.HasPrefix(c, "mv ") {
			continue
		}
		// One move is legitimate: the authored files become the library. What
		// must not come back is a move of a ROW in or out of a root that has
		// already been applied.
		if strings.Contains(c, "run-") || strings.Contains(c, "managed-root-perm/*.tf") {
			t.Errorf("Check 68's step 5 moves files about to isolate a row, and the shared state turns that into planned deletes:\n  %s", c)
		}
	}

	// 5. The reason is stated, and it is the one state every root writes.
	for _, want := range []string{
		"infra.StatePrefixManaged",
		"one key with no per-root suffix",
		"runs BEFORE the cover",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 5 does not say %q, so the fresh root per row looks like tidiness rather than the fix for the wrong refusal", want)
		}
	}

	// 6. The state is read AS THE DEPLOYER, by address, before the first row and
	// after every row — that read is what says whether a row created anything.
	for _, want := range []string{
		`STATE="gs://fugaro-managed-tfstate-${M:?}/fugaro/managed/default.tfstate"`,
		`gcloud storage cat "$STATE" --impersonate-service-account="${D:?}" --project="${M:?}"`,
		`'.resources[]? | .type + "." + .name'`,
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 5 does not carry %q, so what the one state holds is read from nowhere", want)
		}
	}
	if n := strings.Count(code, `gcloud storage cat "$STATE"`); n < 2 {
		t.Errorf("Check 68's step 5 reads the managed state %d time(s): it must be read before the rows and again after each one", n)
	}

	// 7. What to do when a row APPLIES is a rule with commands behind it, and the
	// command that records the approval REPLACES the list — so an invocation that
	// names only the new address drops every approval already in it.
	for _, want := range []string{
		"IF THAT ROW APPLIED",
		"REPLACES the list",
		"plus the one this row just added",
		"nothing is approved",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 5 does not carry %q, so a row that applied leaves an address the next row's plan would delete", want)
		}
	}
	allowDeletes := 0
	for _, c := range cmds {
		if strings.Contains(c, "fugaro allow-delete") {
			allowDeletes++
		}
	}
	if allowDeletes < 2 {
		t.Errorf("Check 68's step 5 runs `fugaro allow-delete` %d time(s): the list has to be read before it is written, and the write is what makes the next row measurable", allowDeletes)
	}

	// 8. The rotation says the same thing as the example, in the same arithmetic:
	// five rows, one per fresh root, so FOUR are named as the others.
	if !strings.Contains(step, "the other four rows") {
		t.Error("Check 68's step 5 does not say the rotation covers the FOUR other rows, so its arithmetic disagrees with the five-row table")
	}
	if strings.Contains(step, "move the other four rows aside") || strings.Contains(step, "move the other three rows aside") {
		t.Error("Check 68's step 5 still describes the rotation as moving rows aside, which the shared state turns into planned deletes")
	}
	if !strings.Contains(step, "each in its own fresh run-<row> directory") {
		t.Error("Check 68's step 5 does not say every other row is measured in a fresh directory of its own")
	}

	// 9. The prose says what each row records, and that a fresh root per row is
	// what makes the refusal the row's.
	for _, want := range []string{
		"What each row records",
		"its own fresh root",
		"the wrong cause",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 5 does not carry %q, so what a row records is left to the reader", want)
		}
	}
}

// TestChecklistHasCheck68ConditionReadIsACommand is a review finding: step 2's
// FACT bullet made the scoped binding's `condition.expression` a thing to check
// "clause by clause", and the Paste-back line and the Passes-when criterion both
// demand it — but the only project-policy read in the step printed
// `table(bindings.role)`, which drops the condition, and the `--format=json` read
// the bullet named in prose was inside no fenced block. A criterion the operator
// has no command for is not a criterion, and the condition IS the fence this
// whole check exists to evidence.
func TestChecklistHasCheck68ConditionReadIsACommand(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	step := check68Step(t, section, "**(2) The two roles, permission by permission, read live**")

	var cmds []string
	for _, b := range bashBlocks(step) {
		cmds = append(cmds, logicalCommands(b)...)
	}
	// The json read is a command in the block, not a phrase in the prose around it.
	jsonRead := ""
	for _, c := range cmds {
		if strings.Contains(c, "gcloud projects get-iam-policy") && strings.Contains(c, "--format=json") {
			jsonRead = c
		}
	}
	if jsonRead == "" {
		t.Fatal("Check 68's step 2 has no `gcloud projects get-iam-policy ... --format=json` command, so the condition its own bullet, Paste-back line and Passes-when criterion require cannot be read")
	}
	if !strings.Contains(jsonRead, `"${M:?}"`) {
		t.Errorf("Check 68's step 2 policy read does not name the managed project:\n  %s", jsonRead)
	}
	// And the condition is extracted from it, in the step, by name.
	extracted := false
	for _, c := range cmds {
		if strings.Contains(c, "condition.expression") && strings.Contains(c, "chk68-project-policy.json") {
			extracted = true
		}
	}
	if !extracted {
		t.Error("Check 68's step 2 never reads `condition.expression` out of the json it now fetches, so the condition is still fetched and not printed")
	}
	// The count the Passes-when criterion asks for ("bound exactly once") is a
	// command too: a role bound twice grants the whole role.
	if !strings.Contains(step, "exactly 1") {
		t.Error("Check 68's step 2 does not count the scoped role's bindings, so \"bound exactly once\" has no command behind it")
	}
	// What the step now writes is scratch, and the Restore has to count it gone.
	for _, want := range []string{"chk68-project-policy.json", "chk68-scoped-condition.txt"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68's Restore does not remove %s, which step 2 now writes", want)
		}
	}
}

// TestChecklistHasCheck67bRefusalQuoteMatchesTheBinary is a review finding: the
// refusal the step tells the operator to expect was quoted with the word
// "project" missing and with `${M:?}` standing where the binary prints the
// project ID, so an operator matching the quoted text against stderr would find
// nothing. The real message is built in managedProviderProject as
// "provider_config." + key + " (lands resources in " + "project " + proj +
// ", which is not this run's)".
func TestChecklistHasCheck67bRefusalQuoteMatchesTheBinary(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67b:")

	const real = "lands resources in project"
	if n := strings.Count(section, real); n < 2 {
		t.Errorf("Check 67b quotes the gate's project refusal %d time(s) with the binary's own wording: both the diagnostic and the Fails-when criterion must read %q", n, real)
	}
	// The broken quote, in either half: "lands resources in ${M:?}" or
	// "lands resources in `", i.e. the preposition followed by anything but
	// "project".
	for _, bad := range []string{"lands resources in ${M:?}", "lands resources in fugaro-chk67, which"} {
		if strings.Contains(section, bad) {
			t.Errorf("Check 67b still quotes the refusal as %q, which is not what the binary prints", bad)
		}
	}
	// And the placeholder is not what renders: the message carries the ID.
	if !strings.Contains(section, "lands resources in project fugaro-chk67") {
		t.Error("Check 67b does not show the refusal with the project ID the binary actually prints")
	}
}

// TestChecklistHasCheck67aNamesAllFourEmbeddedRoots is a review finding: the
// premise enumerated "the three embedded roots
// (deploy/terraform/gcp/roots/{installation,firebase,repo})" and drew its
// conclusion from that count. There are four — `compute` is embedded by the same
// //go:embed all:gcp and its plan carries project-level grants too — so a reader
// auditing the argument found a root the argument did not mention.
func TestChecklistHasCheck67aNamesAllFourEmbeddedRoots(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67a:")

	if !strings.Contains(section, "**four** embedded roots") {
		t.Error("Check 67a's premise still counts the embedded roots as three, and the count is what its conclusion rests on")
	}
	for _, root := range []string{"installation", "firebase", "repo", "compute"} {
		if !strings.Contains(section, "roots/{installation,firebase,repo,compute}") {
			t.Errorf("Check 67a's premise does not name every embedded root (deploy/terraform/gcp/roots/%s is missing from the enumeration)", root)
			break
		}
	}
	if strings.Contains(section, "the three embedded roots") {
		t.Error("Check 67a still says \"the three embedded roots\", so the enumeration and the count disagree")
	}
}

// TestChecklistHasCheck68Step5FixtureAccountIsNotARow is a review finding: the
// sa.tf heredoc comment called the fixture account "the row 68 step 4 already
// measured as creatable". Step 4 is the transitive-reach block, every command of
// which must fail and none of which creates an account; sa.tf is a fixture kept
// in every run, not one of the table's rows, so "the row" resolves to nothing.
func TestChecklistHasCheck68Step5FixtureAccountIsNotARow(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	step := check68Step(t, section, "**(5) What the two roles are known to LACK, measured per type**")

	flat := strings.NewReplacer("\n# ", " ", "\n", " ", "# ", "").Replace(step)
	if strings.Contains(flat, "the row 68 step 4 already measured as creatable") {
		t.Error("Check 68's step 5 still attributes the fixture account to step 4, which creates no service account and must refuse every command")
	}
	// The attribution it now carries is the one the tree supports.
	if !strings.Contains(flat, "google_service_account.fmgd_root") {
		t.Error("Check 68's step 5 does not say which apply proved the fixture account is creatable")
	}
	if !strings.Contains(flat, "67a step 3") {
		t.Error("Check 68's step 5 does not name 67a step 3, the apply that created google_service_account.fmgd_root")
	}
	if !strings.Contains(flat, "not one of the table's rows") {
		t.Error("Check 68's step 5 does not say the account is a fixture kept in every run rather than a row of the table it measures")
	}
}

// shellGuardedDelete finds the fenced block that carries name and returns the
// line indexes of the guard, of the delete and of the else branch, so a test
// reads the ORDER off the block's own lines instead of trusting the prose above
// it. A comment beside a delete is not a guard, and the review's surviving
// mutation was exactly that: replacing the "ONLY if ..." sentence with a bare
// `⚠ CONFIRM` changed nothing a reader or a shell would notice.
func shellGuardedDelete(t *testing.T, section, name, deleteNeedle string) (guard, del, els int, block []string) {
	t.Helper()
	for _, b := range bashBlocks(section) {
		for _, l := range b {
			if strings.Contains(l, name+"=") || strings.Contains(l, "${"+name+"}") {
				block = b
			}
		}
	}
	if block == nil {
		t.Fatalf("%s: no fenced block carries %s", name, name)
	}
	guard, del, els = -1, -1, -1
	for i, l := range block {
		switch {
		case strings.Contains(l, `if [ "${`+name+`}" = yes ]; then`):
			guard = i
		case strings.Contains(l, deleteNeedle):
			del = i
		case strings.TrimSpace(l) == "else":
			els = i
		}
	}
	return guard, del, els, block
}

// TestChecklistHasCheck67aRestoreIsGuardedInTheShell and its Check 68 sibling
// pin the guard, not its description. Both Restores delete a whole GCP project,
// and both said in a comment that the delete ran "ONLY if" a later check was not
// going to run — a condition a pasted block would not honour, and one the
// project's own checkout and the reserved-for-30-days project ID make expensive
// rather than merely untidy. The delete is now inside a shell `if` on a variable
// that is EMPTY by default, with an `else` that reports what was left.
func TestChecklistHasCheck67aRestoreIsGuardedInTheShell(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67a:")

	guard, del, els, block := shellGuardedDelete(t, section, "CHK67_CLEANUP", "gcloud projects delete")
	if guard < 0 {
		t.Fatalf("Check 67a's Restore has no `if [ \"${CHK67_CLEANUP}\" = yes ]; then`, so its project delete is not conditional in the shell:\n  %s", strings.Join(block, "\n  "))
	}
	if del < 0 {
		t.Fatal("Check 67a's Restore no longer deletes the throwaway project at all")
	}
	if els < 0 {
		t.Fatal("Check 67a's Restore guard has no `else` branch, so an unset variable deletes in silence or reports nothing")
	}
	if !(guard < del && del < els) {
		t.Errorf("Check 67a's `gcloud projects delete` is not inside the guarded branch (guard at line %d, delete at %d, else at %d of the block)", guard, del, els)
	}
	for _, want := range []string{"⚠ CONFIRM", "${M:?}"} {
		if !strings.Contains(block[del], want) {
			t.Errorf("Check 67a's guarded delete does not carry %q:\n  %s", want, block[del])
		}
	}
	// The default is empty: a guard whose variable defaults to `yes` deletes on
	// a paste, which is the whole thing this guard exists to stop.
	setLine := ""
	for _, l := range block {
		if strings.HasPrefix(strings.TrimSpace(l), "CHK67_CLEANUP=") {
			setLine = strings.TrimSpace(l)
		}
	}
	if setLine == "" {
		t.Fatal("Check 67a's Restore never assigns CHK67_CLEANUP, so the variable its guard reads comes from nowhere")
	}
	if got := strings.TrimSpace(strings.SplitN(setLine, "#", 2)[0]); got != "CHK67_CLEANUP=" {
		t.Errorf("Check 67a's Restore defaults CHK67_CLEANUP to something (%q): the default has to be empty, so a pasted block deletes nothing", got)
	}
	if !strings.Contains(strings.Join(block, "\n"), "so NOTHING was deleted") {
		t.Error("Check 67a's else branch does not report that nothing was deleted, so an unset variable looks like a clean run")
	}
	if !strings.Contains(section, "30 days") {
		t.Error("Check 67a's Restore does not warn that a deleted project ID stays reserved for about 30 days")
	}
}

func TestChecklistHasCheck68RestoreIsGuardedInTheShell(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")

	guard, del, els, block := shellGuardedDelete(t, section, "CHK68_CLEANUP", "gcloud projects delete")
	if guard < 0 {
		t.Fatalf("Check 68's Restore has no `if [ \"${CHK68_CLEANUP}\" = yes ]; then`, so its project delete is not conditional in the shell:\n  %s", strings.Join(block, "\n  "))
	}
	if del < 0 || els < 0 {
		t.Fatal("Check 68's Restore has no guarded delete, or no else branch to report what was left")
	}
	if !(guard < del && del < els) {
		t.Errorf("Check 68's `gcloud projects delete` is not inside the guarded branch (guard at %d, delete at %d, else at %d)", guard, del, els)
	}
	if !strings.Contains(block[del], "⚠ CONFIRM") {
		t.Errorf("Check 68's guarded delete carries no ⚠ CONFIRM:\n  %s", block[del])
	}
	setLine := ""
	for _, l := range block {
		if strings.HasPrefix(strings.TrimSpace(l), "CHK68_CLEANUP=") {
			setLine = strings.TrimSpace(l)
		}
	}
	if setLine == "" || strings.TrimSpace(strings.SplitN(setLine, "#", 2)[0]) != "CHK68_CLEANUP=" {
		t.Errorf("Check 68's Restore does not default CHK68_CLEANUP to empty (%q): the default has to delete nothing", setLine)
	}
	if !strings.Contains(strings.Join(block, "\n"), "was NOT deleted") {
		t.Error("Check 68's else branch does not report that the project was left standing, and Check 67b needs it")
	}
	// The other half of the same rule: the reason this default is the safe one.
	if !strings.Contains(section, "67b needs it") {
		t.Error("Check 68's Restore does not say why the default direction is not to delete: Check 67b needs the project")
	}
}

// TestManagedChecklistSectionsCarryThe086Banner is the round-6 blocking finding:
// release 0.8.6 turned managed projects off until 0.9.0
// (managedDeployerShipped is false), so every command the three sections rest on
// refuses before any cloud call and the deployer's scoped role is never created.
// A page that reads as runnable today sends an operator into three checks that
// cannot start — and into Check 68 step 2, whose assertions are about a fence
// IAM rejects as written.
func TestManagedChecklistSectionsCarryThe086Banner(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	sections := []struct{ heading, firstStep string }{
		{"## Check 67a:", "**(1) "},
		{"## Check 67b:", "**(1) "},
		{"## Check 68:", "**(1) "},
	}
	for _, s := range sections {
		section := docSection(t, path, doc, s.heading)
		if !strings.Contains(section, "⛔ **CANNOT RUN BEFORE 0.9.0") {
			t.Errorf("%s carries no 0.8.6 banner, so it reads as runnable on this release", s.heading)
			continue
		}
		if i, j := strings.Index(section, "CANNOT RUN BEFORE 0.9.0"), strings.Index(section, s.firstStep); j >= 0 && i > j {
			t.Errorf("%s puts the banner after its first step, so the steps come before the warning", s.heading)
		}
		for _, want := range []string{
			"managedDeployerShipped",
			"0.9.0 or later",
			"managed-deployer-disabled",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("%s's banner does not carry %q", s.heading, want)
			}
		}
	}

	// The summary rows are what a reader meets first.
	rows := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		for _, id := range []string{"67a", "67b", "68"} {
			if strings.HasPrefix(line, "| "+id+" |") {
				rows[id] = line
			}
		}
	}
	for _, id := range []string{"67a", "67b", "68"} {
		row, ok := rows[id]
		if !ok {
			t.Fatalf("the checklist has no `| %s |` table row", id)
		}
		for _, want := range []string{"⛔ CANNOT RUN BEFORE 0.9.0", "managedDeployerShipped", "USER-RUN, NOT RUN"} {
			if !strings.Contains(row, want) {
				t.Errorf("the `| %s |` summary row does not carry %q, so a reader who stops at the table thinks the row is runnable", id, want)
			}
		}
	}

	// Check 68's banner, and step 2's own paragraph, must not present the
	// condition the tree carries as the expected truth: it is the specification
	// the 0.9.0 redesign is written against and rewritten by.
	s68 := docSection(t, path, doc, "## Check 68:")
	for _, want := range []string{
		"not as the expected truth",
		"rewritten when the redesign lands",
		"17 logical operators",
		"resource.name.contains()",
		"resource.name` on types that do not support it",
	} {
		if !strings.Contains(s68, want) {
			t.Errorf("Check 68 does not carry %q, so the invalid condition is left reading as the fence that should exist", want)
		}
	}
	if strings.Contains(s68, "as the expected truth of the fence") {
		t.Error("Check 68 still calls the condition in this tree the expected truth")
	}
	// Step 2 itself — not only the banner above it — has to say that what it
	// reads is the condition as the tree stands and the specification the
	// redesign is written against. The banner alone is not enough: the reader
	// who reaches step 2 has already passed it.
	step2 := check68Step(t, s68, "**(2) The two roles, permission by permission, read live**")
	for _, want := range []string{
		"not as the expected truth",
		"specification the 0.9.0 redesign has to satisfy",
		"rewritten against the new fence",
	} {
		if !strings.Contains(step2, want) {
			t.Errorf("Check 68's step 2 does not carry %q, so the condition it counts clause by clause reads as the expected truth", want)
		}
	}
}

// TestChecklistHasCheck67aConfirmationIsAskedWithoutTheMarker is the round-6
// blocking finding about the confirmation: `managed.Request.confirm` asks a
// person at a terminal to TYPE the installation's name, and only a coding
// agent's marker skips the question — there is no --yes and no
// --non-interactive on the command. The section said the applies ran "with no
// confirmation and no prompt" and counted a prompt as a failure, which is the
// opposite of what the operator sees.
func TestChecklistHasCheck67aConfirmationIsAskedWithoutTheMarker(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67a:")

	if strings.Contains(section, "with no confirmation and no prompt") {
		t.Error("Check 67a still says its applies run with no confirmation and no prompt, and a person at a terminal without the marker IS asked")
	}

	var cmds []string
	for _, b := range bashBlocks(section) {
		cmds = append(cmds, logicalCommands(b)...)
	}
	marked, asked := 0, 0
	for _, c := range cmds {
		if !strings.Contains(c, "fugaro managed apply") {
			continue
		}
		if strings.HasPrefix(c, "AI_AGENT=chk67 ") {
			marked++
			continue
		}
		// An apply that never reaches the confirmation — refused by the gate, by
		// the checkout rule, or as not managed — needs no marker. One that is
		// expected to APPLY and carries none would stop at the prompt, and one
		// unmarked apply is the run this step records: the person who IS asked.
		if strings.Contains(c, "APPLIED") {
			t.Errorf("Check 67a has an apply expected to succeed without the coding agent's marker, so it stops at the confirmation prompt:\n  %s", c)
		}
		if strings.Contains(c, `./managed-root --project "${F:?}"`) && !strings.Contains(c, "expect") {
			asked++
		}
	}
	if marked < 2 {
		t.Errorf("Check 67a marks %d of its applies with the coding agent's marker: step 3b and step 7 both apply a plan, and without the marker the operator is asked to type the installation's name", marked)
	}
	if asked != 1 {
		t.Errorf("Check 67a has %d applies of ./managed-root without the marker: step 3a is the ONE that must be asked, and it is the FACT the other half of D55 rests on", asked)
	}

	// The unmarked run is expected to ASK, on the exact text the binary prints,
	// and to apply nothing when it is answered wrongly.
	for _, want := range []string{
		"⚠ CONFIRM (project fugaro-managed):",
		"Type fugaro-managed to confirm:",
		"the saved plan of … was not confirmed, so nothing is applied",
		"answer it wrongly",
		"If this run APPLIES",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a's unmarked run does not carry %q, so the prompt it is supposed to record is not specified", want)
		}
	}

	// And the two criteria say the same thing.
	passes := section[strings.Index(section, "**Passes when:**"):]
	passes = passes[:strings.Index(passes, "**Fails when:**")]
	if !strings.Contains(passes, "prompt-free under the coding agent's marker") || !strings.Contains(passes, "asked and was not confirmed") {
		t.Error("Check 67a's Passes-when does not require both halves: a marked apply that did not prompt, and an unmarked one that asked and was refused")
	}
	fails := section[strings.Index(section, "**Fails when:**"):]
	if !strings.Contains(fails, "prompted while the coding agent's marker was set") {
		t.Error("Check 67a's Fails-when no longer counts a prompt under the marker as the failure it is")
	}
	if !strings.Contains(fails, "a prompt **without** the marker is not a failure") {
		t.Error("Check 67a's Fails-when does not say that a prompt WITHOUT the marker is the FACT the step records rather than a failure")
	}

	// The same rule in the other two sections: an apply that is expected to
	// APPLY and carries no marker stops at the confirmation instead — and 67b's
	// step 5 is an apply in a project whose delete list is empty.
	for _, heading := range []string{"## Check 67b:", "## Check 68:"} {
		sec := docSection(t, path, doc, heading)
		var apply []string
		for _, b := range bashBlocks(sec) {
			apply = append(apply, logicalCommands(b)...)
		}
		for _, c := range apply {
			if !strings.Contains(c, "fugaro managed apply") || !strings.Contains(c, "APPLIED") {
				continue
			}
			if !strings.HasPrefix(c, "AI_AGENT=chk67 ") {
				t.Errorf("%s applies a plan it expects to succeed without the coding agent's marker, so the operator is asked to type the installation's name first:\n  %s", heading, c)
			}
		}
	}
}

// TestChecklistHasCheck68Step4NegativesAreReversible is the round-6 finding
// about step 4: every one of the five negatives is a real command that must
// fail, none had an undo if one succeeded, and the one command that mints a
// token printed a LIVE credential to the terminal. It also corrects what 4b
// proves — actAs does not mint a token and neither role holds getAccessToken,
// so its chain says nothing about the fence; 4c is the probe that tests it —
// and gives the positive half (the deployer CAN write its own state bucket) the
// command it never had.
func TestChecklistHasCheck68Step4NegativesAreReversible(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 68:")
	step := check68Step(t, section, "**(4) The transitive reach")

	var cmds []string
	for _, b := range bashBlocks(step) {
		cmds = append(cmds, logicalCommands(b)...)
	}
	code := strings.Join(cmds, "\n")

	// 1. No command prints a token: the only mint is redirected to /dev/null,
	// and nothing captures it for display.
	tokens := 0
	for _, c := range cmds {
		if !strings.Contains(c, "print-access-token") {
			continue
		}
		tokens++
		if !strings.Contains(c, ">/dev/null") {
			t.Errorf("Check 68's step 4 mints a token without discarding it, so a successful mint prints a live credential:\n  %s", c)
		}
		if strings.Contains(c, "$(") {
			t.Errorf("Check 68's step 4 captures the minted token into a variable that something could print:\n  %s", c)
		}
	}
	if tokens != 1 {
		t.Errorf("Check 68's step 4 has %d token-minting commands: 4b's chain is the one, and every one of them must discard what it mints", tokens)
	}

	// 2. Every negative has an undo, each carries its own ⚠ CONFIRM, and every
	// undo runs as the operator — none of them impersonates the deployer, which
	// is the identity whose refusal is being measured.
	undos := map[string]string{
		"4a": "--remove-permissions=storage.objects.create",
		"4b": "gcloud projects remove-iam-policy-binding",
		"4c": "gcloud run jobs delete chk68-foreign",
		"4d": `gcloud storage buckets remove-iam-policy-binding "gs://${B:?}"`,
		"4e": `gcloud storage rm "gs://fugaro-tfstate-${M:?}/fugaro/installation/probe.json"`,
	}
	for id, want := range undos {
		found := false
		for _, c := range cmds {
			if !strings.Contains(c, want) {
				continue
			}
			found = true
			if !strings.Contains(c, "⚠ CONFIRM") {
				t.Errorf("Check 68's undo of %s carries no ⚠ CONFIRM, and it changes live IAM or deletes something:\n  %s", id, c)
			}
			if strings.Contains(c, "--impersonate-service-account") {
				t.Errorf("Check 68's undo of %s runs as the deployer, which cannot undo what it could not do:\n  %s", id, c)
			}
		}
		if !found {
			t.Errorf("Check 68's step 4 has no undo for %s, so a negative that succeeds leaves its effect standing", id)
		}
	}
	if !strings.Contains(step, "IF ONE OF THE FIVE NEGATIVES SUCCEEDS") {
		t.Error("Check 68's step 4 does not introduce its undos as a block to run when a negative succeeds")
	}
	// 4d binds two buckets and 4a widens two roles: both halves of each.
	if strings.Count(code, "--remove-permissions=storage.objects.create") < 2 {
		t.Error("Check 68's undo of 4a covers one role: that negative updates both")
	}
	if !strings.Contains(code, `gcloud storage buckets remove-iam-policy-binding "gs://fugaro-tfstate-${M:?}"`) {
		t.Error("Check 68's undo of 4d covers one bucket: that negative binds both the runs and the state bucket")
	}

	// 3. What 4b proves, corrected: actAs is not a way to mint, getAccessToken
	// is, and 4c is the probe about the fence.
	for _, want := range []string{
		"`iam.serviceAccounts.actAs` is not that authority",
		"iam.serviceAccounts.getAccessToken",
		"even if the binding were unconditioned",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("Check 68's step 4 does not carry %q, so 4b is still read as evidence about the fence", want)
		}
	}
	if !strings.Contains(step, "THIS is the probe that tests the fence") {
		t.Error("Check 68's 4c is not named as the probe that tests the condition: it is the only command there that needs actAs on a name outside the prefix")
	}

	// 4. The positive half is a command that must SUCCEED, and its object is
	// this check's own — not the state the applies write.
	if !strings.Contains(step, "THE POSITIVE HALF") {
		t.Error("Check 68's step 4 does not measure the positive half: the deployer CAN write its own state bucket")
	}
	positive := ""
	for _, c := range cmds {
		if strings.Contains(c, "chk68-probe3.txt") && strings.Contains(c, "gcloud storage cp") {
			positive = c
		}
	}
	if positive == "" {
		t.Fatal("Check 68's step 4 has no command that writes D77's state bucket as the deployer, so the positive half of the sentence is asserted and never measured")
	}
	if !strings.Contains(positive, "fugaro-managed-tfstate-${M:?}") {
		t.Errorf("Check 68's positive probe does not write the managed state bucket:\n  %s", positive)
	}
	if !strings.Contains(positive, `--impersonate-service-account="${D:?}"`) {
		t.Errorf("Check 68's positive probe does not run as the deployer:\n  %s", positive)
	}
	// Its own undo, as the identity that wrote it, and the read-back that says
	// the bucket was left as it was found.
	if !strings.Contains(code, `gcloud storage rm "gs://fugaro-managed-tfstate-${M:?}/chk68-probe.json"`) {
		t.Error("Check 68's positive probe leaves its object in the state bucket: the probe removes what it wrote")
	}
	// And the Restore removes its scratch file.
	if !strings.Contains(section, "chk68-probe3.txt") {
		t.Error("Check 68's Restore does not remove /tmp/chk68-probe3.txt, which step 4's positive probe writes")
	}
}

// TestChecklistHasCheck67aDoesNotAskForAnUnelicitedRefusal is the round-6
// finding about claims nothing produces: the step's FACT line asked the
// operator to record the refusal a root without the `fmgd-` prefix (or one
// writing on the runs bucket) gets, and no command in the section feeds the
// gate such a root. Eliciting it live is not free — every root of a managed
// project writes ONE state object, so a root declaring less than the applied
// one is refused by the delete gate FIRST — so the claim is dropped and the
// rules are cited where they are pinned.
func TestChecklistHasCheck67aDoesNotAskForAnUnelicitedRefusal(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67a:")

	if strings.Contains(section, "record that refusal's text too") {
		t.Error("Check 67a still asks for a refusal its commands do not produce")
	}
	for _, want := range []string{
		"the two refusals this step does NOT elicit",
		"TestManagedCoverRefusesNamesOutsideThePrefix",
		"TestManagedCoverRefusesTheRunsBucketByName",
		"TestManagedCoverRefusesTheStateBucketByThePrefix",
		"one object per project",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a does not carry %q, so dropping the claim looks like an omission rather than a decision", want)
		}
	}
}

// TestChecklistHasCheck67bRestoreDoesNotClaimDatabasesComeBack is the round-6
// finding about the restore's own report: the step's not-restored bullet said
// the Firestore and RTDB databases are re-created and only their contents are
// not. They are not re-created at all — restoreConfig strips the budget
// backend and names `fugaro init --firebase` as the way to build one again, the
// Firestore database has no Terraform resource anywhere in the tree, and the
// RTDB instance's resource lives in the firebase root, which a restore never
// applies.
func TestChecklistHasCheck67bRestoreDoesNotClaimDatabasesComeBack(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 67b:")

	if strings.Contains(section, "the databases are re-created") {
		t.Error("Check 67b still says the restore re-creates the databases, and a restore creates neither")
	}
	for _, want := range []string{
		"the databases are NOT re-created",
		"restoreConfig",
		"TestNoFirestoreDatabaseResource",
		"fugaro init --firebase",
		"the budget backend",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67b's not-restored list does not carry %q, so what a restore hands back is left vague", want)
		}
	}
	// The command the step lists is named by symbol, not by a line number that
	// has already moved once.
	for _, bad := range []string{"restore.go:" + "726", "restore.go: "} {
		if strings.Contains(section, bad) {
			t.Errorf("Check 67b cites the restore by %q, and the line moved in 0.8.6", bad)
		}
	}
	// The summary row a reader meets first says the same thing.
	row := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 67b |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("the checklist has no `| 67b |` table row")
	}
	if strings.Contains(row, "the databases are re-created") {
		t.Error("the `| 67b |` row still says the restore re-creates the databases")
	}
}
