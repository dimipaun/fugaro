package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	"",
	"# ${VAR:?} everywhere: an empty variable is a hard error, never an empty",
	"# argument that gcloud would resolve from its ambient configuration (a default",
	"# gcloud project is not this check's). No apostrophe inside a ${VAR:?...}",
	"# message: bash reads the message as a word expecting a closing quote and the",
	"# whole block then fails to parse.",
	"[ \"$P\" = fugaro-dev ] && [ -n \"$L\" ] && [ -n \"$R\" ] && [ -n \"$F\" ] && [ -n \"$M\" ] && [ -n \"$B\" ] && [ -n \"$ACCOUNT_ID\" ] && [ -n \"$AMOUNT\" ] \\",
	"  || { echo \"Check 67a stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L R=$R F=$F M=$M B=$B ACCOUNT_ID=$ACCOUNT_ID AMOUNT=$AMOUNT must not be empty)\" >&2; exit 1; }",
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
	"L=us-east5                   # the installation's region",
	"R=edgeappinc/fugarosandbox   # the sandbox repository, owner/name: the only repository this check touches",
	"ACCOUNT_ID=                  # the SANDBOX installation's billing account ID, no BILLING_ prefix: the new project is created under it",
	"AMOUNT=                      # the monthly amount for the new managed project, a number",
	"",
	"# ${VAR:?} everywhere: an empty variable is a hard error, never an empty",
	"# argument that gcloud would resolve from its ambient configuration. No",
	"# apostrophe inside a ${VAR:?...} message (bash reads it as an unclosed quote).",
	"[ \"$P\" = fugaro-dev ] && [ -n \"$L\" ] && [ -n \"$R\" ] && [ -n \"$F\" ] && [ -n \"$M\" ] && [ -n \"$N\" ] && [ -n \"$ACCOUNT_ID\" ] && [ -n \"$AMOUNT\" ] \\",
	"  || { echo \"Check 67b stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L R=$R F=$F M=$M N=$N ACCOUNT_ID=$ACCOUNT_ID AMOUNT=$AMOUNT must not be empty)\" >&2; exit 1; }",
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
	"",
	"# ${VAR:?} everywhere: an empty variable is a hard error, never an empty",
	"# argument that gcloud would resolve from its ambient configuration. No",
	"# apostrophe inside a ${VAR:?...} message (bash reads it as an unclosed quote).",
	"[ \"$P\" = fugaro-dev ] && [ -n \"$L\" ] && [ -n \"$R\" ] && [ -n \"$F\" ] && [ -n \"$M\" ] && [ -n \"$D\" ] && [ -n \"$B\" ] \\",
	"  || { echo \"Check 68 stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L R=$R F=$F M=$M D=$D B=$B must not be empty)\" >&2; exit 1; }",
	"fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,' \\",
	"  || { echo \"Check 68 stopped: this is not the fugaro-dev installation (fugaro doctor's project: line is not fugaro-dev, or doctor refused the checkout: --project disagreeing with the checkout's project: is refused)\" >&2; exit 1; }",
	"git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\\.git)?$' \\",
	"  || { echo \"Check 68 stopped: this checkout's origin is not the sandbox (edgeappinc/fugarosandbox)\" >&2; exit 1; }",
}

// managedCheckSection returns the whole section of docs/gcp-live-checklist.md
// whose heading starts with marker, up to the next `## ` heading (or the end
// of the file, which is where Check 68 sits).
func managedCheckSection(t *testing.T, path, doc, marker string) string {
	t.Helper()
	i := strings.Index(doc, marker)
	if i < 0 {
		t.Fatalf("%s has no %q heading", path, marker)
	}
	rest := doc[i:]
	if j := strings.Index(rest[len(marker):], "\n## "); j >= 0 {
		return rest[:len(marker)+j]
	}
	return rest
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
		got := stops(managedCheckSection(t, path, doc, c.heading))
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
	section := managedCheckSection(t, path, doc, "## Check 67a:")

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
		"**(2) The managed installation's own checkout** — **once per repo checkout",
		"**(3) An apply the gate admits, with no confirmation** — **once per repo checkout",
		"**(4) A marker that names another project reads as not managed** — **once per repo checkout.**",
		"**(5) A project that is not managed still asks** — **once per repo checkout",
		"**(6) A plan that names a second project is refused** — **once per repo checkout, in the managed checkout.**",
		"**(7) The delete list: absent means refused, one written address means applied** — **once per repo checkout.**",
		"**(8) The run's own job credential cannot write either object** — **once per repo checkout",
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
		"the project is not managed",
		"`managed.ErrNotManaged`",
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

	// 7. The commands. Every gcloud command names its project with
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
	section := managedCheckSection(t, path, doc, "## Check 67b:")

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
		"**(2) Present an existing project ID, and watch it be refused** — **once per repo checkout.**",
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
	section := managedCheckSection(t, path, doc, "## Check 68:")

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
		// The positive half of the same sentence: the one bucket it CAN write.
		"fugaro-managed-tfstate-",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 68 does not carry %q", want)
		}
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
	if !strings.Contains(section, "must read `0`") {
		t.Error("Check 68's Restore does not demand a `0` count on every line")
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

	// `belong`, EdgeWeb and EdgeServer are the installations and repositories
	// these checks must never name as anything but forbidden. They are not
	// named at all here: the guard proves the boundary structurally instead
	// (`P` is a constant, the origin must be the sandbox), which is stronger
	// than a sentence, and a sentence is what a reader skims.
	for _, bad := range []string{"`belong`", "EdgeWeb", "EdgeServer"} {
		for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
			if strings.Contains(managedCheckSection(t, path, doc, h), bad) {
				t.Errorf("%s names %q", h, bad)
			}
		}
	}

	// No secret-shaped literal, in a section whose whole subject is IAM: the
	// check reads permissions and bindings, never a key or a token value.
	for _, bad := range []string{"AIza", "-----BEGIN", "PRIVATE KEY", "client_secret", "Bearer "} {
		for _, h := range []string{"## Check 67a:", "## Check 67b:", "## Check 68:"} {
			if strings.Contains(managedCheckSection(t, path, doc, h), bad) {
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
		blocks := bashBlocks(managedCheckSection(t, path, doc, h))
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
