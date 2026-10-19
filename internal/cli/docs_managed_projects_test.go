package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	"# step (1 to 5, and the Restore). It is idempotent, and nothing below the",
	"# three hard stops runs unless all three pass. ===",
	"P=fugaro-dev                 # HARD-CODED, and never read from gcloud: the sandbox installation's GCP project",
	"F=fugaro-managed             # this check's throwaway managed installation, created by Check 67a's step 1",
	"M=fugaro-chk67               # the managed project Check 67a created: the OLD project, destroyed in step 1 here",
	"N=fugaro-chk67b              # the NEW project ID this restore creates: it MUST NOT exist before step 3",
	"E=                           # an EXISTING GCP project ID you may read and this check does NOT own: not $P, not $M, not $N. Step 2 presents it to watch the restore refuse to ADOPT a project (any other scratch project of your own will do)",
	"L=us-east5                   # the installation's region",
	"R=edgeappinc/fugarosandbox   # the sandbox repository, owner/name: the only repository this check touches",
	"ACCOUNT_ID=                  # the SANDBOX installation's billing account ID, no BILLING_ prefix: the new project is created under it",
	"AMOUNT=                      # the monthly amount for the new managed project, a number",
	"TERRAFORM_ADDRESS=           # ONE resource address out of a real plan, in terraform's own form: the single address fugaro allow-delete is given in step 4",
	"SANDBOX=$(git rev-parse --show-toplevel)   # the sandbox checkout this block is being re-run IN: the only directory where the three stops below hold, and the directory step 5 returns to",
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
		"**(3) An apply the gate admits, with no confirmation** — **once per repo checkout",
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
		"with no confirmation and no prompt",
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
		"**(3) The restore itself** — **once per installation.**",
		"**(4) The new project is managed, and the delete list is genuinely gone** — **once per repo checkout.**",
		"**(5) No state read happened** — **once per repo checkout.**",
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
		t.Error("Check 67b's step 4 writes the owner's delete list without its ⚠ CONFIRM")
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
		t.Error("Check 67b's step 4 expects a plan with a delete without any step removing a resource from the root")
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
		"**The chain in 4b, and which account needs which grant for it to mean what it says.**",
		"the operator needs `roles/iam.serviceAccountTokenCreator` on the deployer account",
		"the deployer needs `roles/iam.serviceAccountTokenCreator` (or write-level `iam.serviceAccounts.actAs`) on `${NUM}-compute@developer.gserviceaccount.com`",
		"which is why the chain is one comma-separated flag and not two flags",
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
	firstPermApply := strings.Index(section, "fugaro managed apply ./managed-root-perm --project \"${F:?}\"")
	switch {
	case mkperm < 0:
		t.Error("no step of Check 68 writes ./managed-root-perm, so its permission rows apply a root that does not exist")
	case firstPermApply < 0:
		t.Error("Check 68 no longer applies ./managed-root-perm")
	case mkperm > firstPermApply:
		t.Error("Check 68 applies ./managed-root-perm before any step authors it")
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
	if !strings.Contains(section, "rm -f /tmp/chk68-deployer.txt /tmp/chk68-scoped.txt /tmp/chk68-operator.txt \\\n  /tmp/chk68-probe.txt /tmp/chk68-probe2.txt /tmp/chk68-marker.orig.json") {
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

	// The line citations, in a document that cites exactly: `managed_apply.go`'s
	// `applied …` print is pinned a line below the statement above it,
	// `restoreGCPProjectLine`'s citation is pinned against the line restore.go
	// declares it on (TestManagedChecklistCitationsNameSymbols), and the
	// operator's grant is `google_service_account_iam_member.operator` with
	// its `role` line after it. A citation one line out sends a reader to the
	// blank line above the thing.
	for _, want := range []string{
		"internal/cli/managed_apply.go:289",
		// The restore.go citation is derived from the source rather than
		// written here: the line is what a reader is sent to, so it is
		// checked against the line restore.go declares the symbol on, and an
		// edit that moves either one fails here instead of leaving a number
		// that was true once.
		"internal/cli/" + declaredLine(t, "restore.go", "func restoreGCPProjectLine("),
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
	for _, bad := range []string{
		"internal/cli/managed_apply.go:288",
		"deploy/terraform/gcp/modules/workflow/sa.tf:13",
	} {
		for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
			if strings.Contains(docSection(t, path, doc, h), bad) {
				t.Errorf("%s cites %q, which is a line off", h, bad)
			}
		}
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

// TestManagedChecklistCitationsNameSymbols is the review's first finding: the
// comment in this file cited restoreGCPProjectLine by line number, and the
// number was stale by two — the declaration and the refusal are far enough
// apart that the next edit above either one moves it again. The rule now is
// the symbol, in this file and in the section that makes the same claim; the
// one place a line is the subject (the `applied …` print) keeps its line,
// which is the line the review that asked for it named.
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
	// The doc keeps its line citation for this one -- an earlier review asked
	// the file to cite exactly -- so "exactly" has to be checked against the
	// source rather than trusted: whatever line Check 67a gives for
	// restoreGCPProjectLine must be the line restore.go declares it on. A
	// citation that drifts fails here instead of at the reader.
	want := declaredLine(t, "restore.go", "func restoreGCPProjectLine(")
	cited := false
	for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
		for _, m := range line.FindAllString(docSection(t, path, doc, h), -1) {
			cited = true
			if m != want {
				t.Errorf("%s cites %s; restore.go declares restoreGCPProjectLine on %s", h, m, want)
			}
		}
	}
	if !cited {
		t.Error("no managed section cites restore.go by line any more: if the citation was dropped rather than corrected, drop this half of the test with it")
	}
	// The line citation the earlier review asked to be exact stays exact: it
	// names a print, and the line is what the reader is sent to.
	for _, want := range []string{"internal/cli/managed_apply.go:289", "deploy/terraform/gcp/modules/workflow/sa.tf:12"} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 67a no longer carries the line citation %q", want)
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
