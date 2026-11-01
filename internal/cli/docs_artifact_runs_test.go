package cli

import (
	"os"
	"strings"
	"testing"
)

// check58Guard is Check 58's setup + guard block, line for line: Check 43's
// block in this check's variables, with the same three hard stops (P
// hard-coded to fugaro-dev, ${VAR:?} on every argument, `fugaro doctor`'s
// project line and the checkout's own git origin). The test below compares it
// verbatim, so an edit that drops a stop, weakens one or reorders the block
// fails here rather than at the owner's terminal.
var check58Guard = []string{
	"# === Check 58: setup + guard. RE-RUN THIS WHOLE BLOCK at the start of every",
	"# step (1, 2, 3, 4, 5, 6, 7, Restore). It is idempotent, and nothing below",
	"# the three hard stops runs unless all three pass. ===",
	"P=fugaro-dev                 # HARD-CODED, and never read from gcloud",
	"L=us-east5                   # the installation's region",
	"R=edgeappinc/fugarosandbox   # the sandbox repository, owner/name: the only repository this check touches",
	"S=edgeappinc-fugarosandbox   # the sandbox's slug WITHOUT its 16-hex tail (task.Slug): every run this check reads back is asserted to sit under a slug that starts with it, which is what keeps the check off `belong`",
	"W=web                        # the sandbox workflow this check adds ffmpeg to: it MUST be a key of the checkout's fugaro.yaml `workflows:` — the read-back block below refuses a W the checkout has no job for, so pick another key if the sandbox has no `web`",
	"B=chk58                      # this check's batch label, on every launch; the fresh-record rule and every read-back use it",
	"",
	"# ${VAR:?} everywhere: an empty variable is a hard error, never an empty",
	"# argument that gcloud would resolve from its ambient configuration (the",
	"# owner's ambient project is a *-prod one).",
	"[ \"$P\" = fugaro-dev ] && [ -n \"$L\" ] && [ -n \"$R\" ] && [ -n \"$S\" ] && [ -n \"$W\" ] && [ -n \"$B\" ] \\",
	"  || { echo \"Check 58 stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L R=$R S=$S W=$W B=$B must not be empty)\" >&2; exit 1; }",
	"fugaro doctor --project fugaro --all | grep -q '^project: .*(GCP fugaro-dev,' \\",
	"  || { echo \"Check 58 stopped: this is not the fugaro-dev installation (fugaro doctor's project: line is not fugaro-dev, or doctor refused the checkout: --project disagreeing with the checkout's project: is refused)\" >&2; exit 1; }",
	"git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\\.git)?$' \\",
	"  || { echo \"Check 58 stopped: this checkout's origin is not the sandbox (edgeappinc/fugarosandbox)\" >&2; exit 1; }",
}

// bashBlocks returns every fenced ```bash block in section, in order, each as
// its lines with the fence lines removed. A fence counts when its own line is
// exactly ```bash after trimming, so an indented fence inside a numbered step
// opens a block like any other, and the closing fence is the next trimmed ```.
func bashBlocks(section string) [][]string {
	var blocks [][]string
	var cur []string
	open := false
	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.TrimSpace(line) == "```bash":
			open, cur = true, nil
		case open && strings.TrimSpace(line) == "```":
			blocks = append(blocks, cur)
			open = false
		case open:
			cur = append(cur, line)
		}
	}
	return blocks
}

// bashFunc returns the body of the shell function name declared in section,
// from its `name() {` line to the first `}` at column 0 (the way this file's
// blocks close one). It is how the tests below read one helper's own rules
// rather than the section at large.
func bashFunc(t *testing.T, section, name string) string {
	t.Helper()
	start := strings.Index(section, "\n"+name+"()")
	if start < 0 {
		t.Fatalf("Check 58 does not declare %s()", name)
	}
	rest := section[start+1:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end+1]
	}
	t.Fatalf("Check 58's %s() is never closed by a `}` at column 0", name)
	return ""
}

// TestChecklistCheck58PrefixOfFailsClosed pins the one helper the Restore
// *deletes* with. prefix_of used to be a one-line printf around a command
// substitution, and that shape has two ways to delete everything:
//
//   - `$(ref_of "$1")` swallows ref_of's refusal — a failing substitution
//     leaves an empty string, and the printf around it still exits 0, so the
//     caller's `|| continue` never fires;
//   - an empty run id then makes the prefix `${BUCKET}/runs/`, which is the
//     whole runs tree of every repository in the bucket, and the Restore's
//     `gcloud storage rm --recursive` deletes exactly what it is given.
//
// Each clause asserted below closes one of those, and each is a mutation that
// puts the hazard back: drop `|| return 1` and the refusal is swallowed again;
// drop the run-ID `case` and an empty id builds the whole-tree prefix; drop the
// ref `case` and only ref_of's own slug check stands between a stray id and a
// delete.
func TestChecklistCheck58PrefixOfFailsClosed(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	section := docSection(t, path, string(data), "## Check 58:")
	body := bashFunc(t, section, "prefix_of")

	for _, want := range []string{
		// An empty id is a case this REFUSES, not one that kills the shell
		// mid-Restore, so the parameter is taken plainly and judged below.
		"local id=$1 ref",
		// The id must have task.ValidRunID's exact form — 8 digits, a dash,
		// 6 digits, a dash, four hex digits. This is the clause an empty id
		// dies on.
		"[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]-[0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;",
		// ref_of's own refusal is CHECKED rather than swallowed.
		`ref=$(ref_of "$id") || return 1`,
		// And what comes back must still be a whole `<slug>/<run-id>` under
		// the sandbox's slug before it becomes a path.
		`case "$ref" in "${S:?}"-*/"$id") ;;`,
		`printf '%s/runs/%s/' "${BUCKET:?}" "$ref"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("prefix_of does not carry %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{
		// The old shape, in either of its spellings: a bare substitution, or
		// one with no status check.
		`"$(ref_of`,
		`$(ref_of "$1")`,
	} {
		if strings.Contains(body, bad) {
			t.Errorf("prefix_of builds its path from an unchecked substitution (%q): a refusal inside one is swallowed and the printf still exits 0", bad)
		}
	}
	// The Restore must still look at prefix_of's status, or the fix is moot.
	if !strings.Contains(section, `p=$(prefix_of "$id") ||`) {
		t.Error("Check 58's Restore does not check prefix_of's exit status, so a refusal there cannot stop a delete")
	}
}

// TestChecklistHasCheck58 pins Check 58 of docs/gcp-live-checklist.md (the
// artifact-runs check, design docs/design/artifact-runs.md §3–§9): the row and
// the heading say it is the owner's to run and never against `belong`, the
// guard block is verbatim, every `fugaro` command carries `--project fugaro`,
// every state-changing step records its commit and every launch records its
// run in the per-attempt record file, no read-back picks a run out of the
// batch by position, and the Restore reverts only the commits that record
// holds and that are not already reverted.
//
// The last two are the point of the test rather than decoration. The check
// changes the sandbox in five commits (step 1, 6b and step 7's three halves)
// and launches five runs, three of which are meant to fail: a Restore that
// reverts by eye — `git revert --no-edit HEAD` in a loop, or a read-back that
// takes `batch_refs | head -1` — reverts a commit that is already reverted, or
// reads the failed attempt's run instead of this one's. Both fail here.
func TestChecklistHasCheck58(t *testing.T) {
	const path = "../../docs/gcp-live-checklist.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	section := docSection(t, path, doc, "## Check 58:")

	// 1. Whose check it is. The row is the checklist's own index of it, and
	// both it and the heading carry the two facts an owner reads first: this
	// is run by a person, and never against the live repositories.
	row := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 58 |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("the checklist has no `| 58 |` table row")
	}
	for _, want := range []string{
		"USER-RUN, NOT RUN",
		"never `belong`",
		"sandbox repository only",
		"per-attempt record file",
		"reverts only the commits that record holds",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("the `| 58 |` row does not carry %q", want)
		}
	}
	for _, want := range []string{
		"NOT RUN",
		"run by you",
		"never `belong`",
		"No agent runs any step",
		"U2's mise half stays open",
		"step 6b",
		"checks out `origin/<ref>`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 58 does not say %q", want)
		}
	}

	blocks := bashBlocks(section)
	if len(blocks) < 5 {
		t.Fatalf("Check 58 has %d bash blocks, want the guard block and at least four steps", len(blocks))
	}

	// 2. The guard block, verbatim and first.
	guard := blocks[0]
	if len(guard) != len(check58Guard) {
		t.Errorf("Check 58's guard block is %d lines, want %d", len(guard), len(check58Guard))
	}
	for i, want := range check58Guard {
		if i >= len(guard) {
			break
		}
		if guard[i] != want {
			t.Errorf("Check 58's guard block line %d is\n  %q\nwant\n  %q", i+1, guard[i], want)
		}
	}

	// 3. `--project fugaro` on every fugaro command that takes flags, in
	// every block. A line that only prints (echo) is a message, not a
	// command: the guard block's hard-stop text names `fugaro doctor` and
	// `--project` without calling either.
	commands := 0
	for i, block := range blocks {
		for _, line := range block {
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "#") || strings.Contains(s, "echo ") {
				continue
			}
			if !strings.Contains(s, "fugaro ") || !strings.Contains(s, " --") {
				continue
			}
			commands++
			if !strings.Contains(s, "--project fugaro") {
				t.Errorf("block %d: %q takes flags but carries no `--project fugaro`", i, s)
			}
		}
	}
	if commands < 8 {
		t.Errorf("Check 58 has %d fugaro commands, want the launches, the read-backs and diagnose", commands)
	}

	// 4. The per-attempt record file: step 1 records what it found before it
	// changes anything, every commit and every run goes into the file, and
	// the fresh-attempt rule refuses a second attempt under the same batch.
	for _, want := range []string{
		"RECDIR=/tmp/chk58-records",
		`REC="$RECDIR/${B:?}.env"`,
		`rec_put base_head "$(git rev-parse HEAD)"`,
		`rec_put job_image_before "${IMAGE:?}"`,
		`rec_put job_image_after "`,
		`rec_put commit_5801 "$(git rev-parse HEAD)"`,
		`[ ! -f "${REC:?}" ] ||`,
		"rec_commit_keys",
		"ref_or_stop",
		"step_guard",
		"at_pushed_head",
		// A key the record does not hold is an error, not an empty value:
		// `sed -n` alone exits 0 with nothing, and every refusal below is
		// spelled as an exit status. Without this line `ref_or_stop` and
		// `revert_once` only refuse a *missing record file*, which is not what
		// the section says they do.
		`[ -n "$v" ] || return 1`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 58 does not carry the record-file rule %q", want)
		}
	}
	if strings.Contains(section, "batch_refs | head -1") {
		t.Error("Check 58 recovers a run by position (`batch_refs | head -1`): the batch holds this attempt's failures too")
	}

	// 5. Every one of the five launches is guarded per step, pins the commit
	// it reads, and records the run it minted. The tag is 58 and the step's
	// own digits, so the four patterns below name one launch each.
	for _, tag := range []string{"5802", "586b", "587a", "587b", "587c"} {
		for _, want := range []string{
			"step_guard " + tag + " ",
			"at_pushed_head " + tag + " ",
			"new_run_id " + tag + ")",
			"rec_put run_" + tag + " ",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("the launch tagged %s does not carry %q", tag, want)
			}
		}
	}
	// The task text every launch passes is one variable, and the launch
	// blocks pass it: `fugaro run` requires TEXT of every non-follow-up.
	for i, block := range blocks {
		joined := strings.Join(block, "\n")
		if !strings.Contains(joined, "fugaro run ") {
			continue
		}
		if !strings.Contains(joined, `"${TASK:?}"`) && !strings.Contains(joined, `"${TASK_BIG:?}"`) {
			t.Errorf("block %d launches `fugaro run` without passing the task text", i)
		}
	}

	// 5b. The rebuild that bakes `image.apt` in is `fugaro image build`, and
	// the check must not send the owner to `fugaro image refresh` instead.
	// refresh decides on the build record's `base_ref` alone
	// (refreshVerdict, internal/cli/image_refresh_rules.go): on a sandbox
	// already built from this release's base it prints `current: built from …`,
	// submits no Cloud Build, and ffmpeg never arrives — so steps 2–7 would
	// fail for a reason that is the check's own instruction.
	if !strings.Contains(section, `fugaro image build --project fugaro --repo "${R:?}" --workflow "${W:?}"`) {
		t.Error("Check 58 does not rebuild the workflow with `fugaro image build`, the command that carries an `image.apt` change")
	}
	for _, line := range strings.Split(section, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "fugaro image refresh") || strings.Contains(s, "&& fugaro image refresh") {
			t.Errorf("Check 58 rebuilds with `fugaro image refresh`, which ignores the image config: %q", s)
		}
	}

	// 6. The reverts. Exactly one `git revert` *command* in the whole section,
	// and it is the guarded one: the SHA comes out of the attempt record, a
	// missing record refuses, and an already-reverted commit is a no-op. An
	// unguarded `git revert --no-edit HEAD` in the Restore fails here. A line
	// that only talks about reverting — a comment, or the refusal message that
	// names `git revert --continue` as the way out — is not a command.
	reverts := 0
	for _, line := range strings.Split(section, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "git revert") {
			continue
		}
		reverts++
		if !strings.Contains(s, `git revert --no-edit "${c:?}"`) {
			t.Errorf("Check 58 has an unguarded revert: %q", s)
		}
	}
	if reverts != 1 {
		t.Errorf("Check 58 has %d `git revert` lines, want exactly one inside `revert_once`", reverts)
	}
	for _, want := range []string{
		`rec_get "commit_${key}") ||`,
		`grep -qF "This reverts commit ${c}."`,
		`rec_put "revert_${key}" "$rv"`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("`revert_once` does not carry %q", want)
		}
	}
	restore := -1
	for i, block := range blocks {
		if strings.Contains(strings.Join(block, "\n"), "rec_commit_keys") {
			restore = i
		}
	}
	if restore < 0 {
		t.Fatal("Check 58's Restore block does not loop over the recorded commits")
	}
	for _, want := range []string{
		"git status --porcelain",
		"rec_has commit_5801 ||",
		// NEWEST FIRST: `git revert` is a 3-way merge, so reverting step 1's
		// commit while a later one touching the same `fugaro.yaml` lines still
		// stands conflicts — the half-finished attempt this Restore exists for.
		// The order is pinned here, and so is the refusal that names the step
		// that would not revert.
		"keys=$(rec_commit_keys | awk ",
		"for key in $keys; do",
		`revert_once "$key" || { echo "Check 58 stopped: the step tagged ${key} did not revert`,
		`git push origin "$(rec_get base_branch)"`,
	} {
		if !strings.Contains(strings.Join(blocks[restore], "\n"), want) {
			t.Errorf("Check 58's Restore does not carry %q", want)
		}
	}

	// 7. Destructive commands name their target and nothing else: every
	// `gcloud storage rm` deletes one run's own prefix under ${P:?}, and the
	// only absolute path any `rm -rf` touches is this check's own /tmp
	// scratch. (`rm -rf out` inside a render script is the recipe's own
	// output directory, in the sandbox checkout.)
	for i, block := range blocks {
		for _, line := range block {
			s := strings.TrimSpace(line)
			// A comment about deleting is not a delete, and a line that PRINTS
			// a command for the operator (the leftover-runs branch) spells the
			// same quotes escaped — `\"${p:?}\"` — so both are read here in
			// the form they would have when run.
			probe := strings.ReplaceAll(s, `\"`, `"`)
			if strings.HasPrefix(probe, "#") {
				continue
			}
			if strings.Contains(probe, "gcloud storage rm") {
				for _, want := range []string{`"${p:?}"`, `--project="${P:?}"`, "--recursive"} {
					if !strings.Contains(probe, want) {
						t.Errorf("block %d: %q deletes without %s", i, s, want)
					}
				}
			}
			if strings.Contains(s, "rm -rf") {
				code := s
				if c := strings.Index(code, " #"); c >= 0 {
					code = strings.TrimSpace(code[:c])
				}
				if code != "rm -rf out && mkdir -p out" && !strings.HasPrefix(code, "rm -rf /tmp/chk58") {
					t.Errorf("block %d deletes outside the check's own scratch paths: %q", i, s)
				}
			}
		}
	}
	if strings.Contains(section, "--force") {
		t.Error("Check 58 pushes or deletes with --force")
	}

	// 8. The design's own §12 rows this check claims to settle name the step
	// that settles them, so a section that renumbers its steps does not leave
	// the design pointing at nothing.
	design, err := os.ReadFile("../../docs/design/artifact-runs.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(string(design), "\n") {
		for _, id := range []string{"U2", "U3", "U5", "U6"} {
			if strings.HasPrefix(line, "| "+id+" |") {
				rows[id] = line
			}
		}
	}
	for _, id := range []string{"U2", "U3", "U5", "U6"} {
		if rows[id] == "" {
			t.Fatalf("the design has no %s row in its §12 table", id)
		}
	}
	if !strings.Contains(rows["U2"], "mise") {
		t.Error("the design's U2 row no longer asks about mise: Check 58's wording says which half of it stays open")
	}
	for _, id := range []string{"U3", "U5", "U6"} {
		if !strings.Contains(rows[id], "Check 58") {
			t.Errorf("the design's %s row does not name the check that settles it", id)
		}
	}
	if !strings.Contains(rows["U6"], "6b") {
		t.Error("the design's U6 row does not name step 6b, the near-cap upload that settles it")
	}

	// 9. Guard FIRST, before any side effect. A re-run of a half that already
	// ran must find that out before it stages, commits and pushes again: with
	// `step_guard` after the commit, a dirty tree is committed and pushed a
	// second time before the refusal can fire. Every block that both guards
	// and writes therefore has to put the guard first, and step 1's own commit
	// block — guarded by the fresh-attempt rule, in its own block above — has
	// to say so.
	guarded := 0
	for i, block := range blocks {
		joined := strings.Join(block, "\n")
		if !strings.Contains(joined, "step_guard ") {
			continue
		}
		guard := strings.Index(joined, "step_guard ")
		for _, after := range []string{"git add ", "git commit", "git push"} {
			if j := strings.Index(joined, after); j >= 0 && j < guard {
				t.Errorf("block %d %ss before `step_guard`: a re-run commits and pushes again before the guard can refuse", i, after)
			}
		}
		if strings.Contains(joined, "git commit") {
			guarded++
		}
	}
	if guarded == 0 {
		t.Error("no block in Check 58 both guards a step and commits: the ordering rule above has no subject")
	}
	if !strings.Contains(section, "This step's guard is the fresh-attempt rule just above") {
		t.Error("step 1's commit block does not say which guard precedes it (the fresh-attempt rule)")
	}

	// 10. The leftover-runs branch. The fresh-attempt rule and step_guard both
	// send the operator to the Restore for a batch an earlier attempt left
	// behind — and that is the one case that cannot reach the Restore's own
	// deletion loop, because the record it would need is gone and the
	// `rec_has commit_5801` refusal stops the run first. So the branch has to
	// exist, has to come BEFORE that refusal, and must delete nothing itself:
	// it lists every run of the batch with the exact one-run command, and the
	// operator runs those by hand.
	if !strings.Contains(section, "if ! rec_has commit_5801; then") {
		t.Error("Check 58's Restore has no leftover-runs branch for a batch whose record is gone")
	}
	if branch, refusal := strings.Index(section, "if ! rec_has commit_5801; then"), strings.Index(section, "rec_has commit_5801 ||"); branch < 0 || refusal < 0 || branch > refusal {
		t.Error("the leftover-runs branch does not come before the `rec_has commit_5801` refusal, so the case it exists for exits before reaching it")
	}
	if !strings.Contains(section, "NOTHING IS DELETED BY THIS BLOCK") {
		t.Error("the leftover-runs branch does not say that it deletes nothing itself")
	}
	if rest := blocks[restore]; !strings.Contains(strings.Join(rest, "\n"), `echo "    gcloud storage rm --recursive \"${p:?}\" --project=\"${P:?}\""`) {
		t.Error("the leftover-runs branch does not print the exact one-run delete command")
	}
	// Exactly ONE executed delete in the whole Restore. Every other
	// `gcloud storage rm` is inside an `echo` — a line for the operator to
	// read and run deliberately, never a batch-wide delete by guess.
	executed := 0
	for _, line := range blocks[restore] {
		s := strings.TrimSpace(line)
		if !strings.Contains(s, "gcloud storage rm") || strings.HasPrefix(s, "echo ") {
			continue
		}
		executed++
	}
	if executed != 1 {
		t.Errorf("Check 58's Restore %d `gcloud storage rm` commands, want exactly one (the loop over this attempt's own runs): any other delete must be printed for the operator, not run", executed)
	}

	// 11. Every block that reads `${p:?}` builds it, and every block that
	// reads `${REF_OK:?}` recovers it from the record or mints it. This is the
	// rule step 5 broke: its block expanded `${p:?}` three times with nothing
	// in the block — or in the blocks it is re-run with — setting `p`.
	for i, block := range blocks {
		joined := strings.Join(block, "\n")
		if strings.Contains(joined, "${p:?}") && !strings.Contains(joined, "p=$(prefix_of") {
			t.Errorf("block %d reads ${p:?} without building it (`p=$(prefix_of …)`) in the same block", i)
		}
	}

	// 12. 7c's caps have to pass the launch gate, which is an arithmetic rule
	// against the job's memory — not the design's §5.2 prose. The old numbers
	// (2 GiB of outputs) needed 5 GiB of memory and would have been refused at
	// launch on the sandbox's own 2Gi job, so the section names the rule, the
	// function it lives in and the arithmetic of the numbers it uses.
	for _, want := range []string{
		"2 × outputs.max_total_bytes + inputs.max_total_bytes",
		"outputsMemoryProblem",
		"internal/runner/gate.go",
		"DefaultInputTotalBytes",
		"2 × 256 MiB + 1 GiB = 1.5 GiB",
		"512 MiB at 2Gi",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 58's 7c does not carry %q, so the caps it commits are not shown to pass the memory gate", want)
		}
	}
	if strings.Contains(section, "2147483648` and `max_total_bytes: 2147483648") {
		t.Error("7c still declares 2 GiB caps, which need 5 GiB of memory and are refused at launch by the sandbox's 2Gi job")
	}

	// 13. The NOT RUN banner the section opens with, and the design's own
	// outline of it saying `image build` rather than the `image refresh` the
	// section no longer uses.
	if !strings.Contains(section, "**NOT RUN.** Its blocks have been through several review rounds; read the guard and the Restore once more before running it, and run it after 0.8.0 is tagged.") {
		t.Error("Check 58 does not open with the NOT RUN banner")
	}
	outline := designSection(t, "../../docs/design/artifact-runs.md", "## 11. Check 58, in outline")
	if !strings.Contains(outline, "`fugaro image build`") {
		t.Error("the design's §11 outline does not name `fugaro image build` as Check 58's step-1 rebuild")
	}
	if strings.Contains(outline, "one `fugaro image refresh`") {
		t.Error("the design's §11 outline still sends Check 58's step 1 to `image refresh`, which never reads the image config")
	}
}

// designSection returns the text of doc from the heading that starts with
// marker up to the next `## ` heading.
func designSection(t *testing.T, path, marker string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start := strings.Index(doc, marker)
	if start < 0 {
		t.Fatalf("%s has no %q", path, marker)
	}
	rest := doc[start:]
	if end := strings.Index(rest[1:], "\n## "); end >= 0 {
		return rest[:end+1]
	}
	return rest
}

// TestArtifactRunsDesignSecurityText keeps the design's §8.9 block quote equal
// to the text SECURITY.md actually ships. The design says the file is the
// source of the wording, so the two being the same four paragraphs is the
// claim; this compares them with whitespace normalised (both files wrap, and
// SECURITY.md wraps its paragraphs as single lines) rather than character for
// character, which is what an editor's reflow would otherwise fail on.
func TestArtifactRunsDesignSecurityText(t *testing.T) {
	security, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	// The four paragraphs live in "### The assumption", from the one that
	// starts with the new sentence up to the next heading.
	var paras []string
	in := false
	for _, line := range strings.Split(string(security), "\n") {
		switch {
		case strings.HasPrefix(line, "### What limits the blast radius"):
			in = false
		case strings.HasPrefix(line, "**A run may also be given files"):
			in = true
		}
		if in && strings.TrimSpace(line) != "" {
			paras = append(paras, normSpace(line))
		}
	}
	if len(paras) != 4 {
		t.Fatalf("SECURITY.md's artifact paragraphs are %d, want the four this design pins", len(paras))
	}

	design, err := os.ReadFile("../../docs/design/artifact-runs.md")
	if err != nil {
		t.Fatal(err)
	}
	quoted := normSpace(designBlockQuote(t, string(design), "9. **`SECURITY.md` carries this text"))
	for i, p := range paras {
		if !strings.Contains(quoted, p) {
			t.Errorf("SECURITY.md's artifact paragraph %d is not quoted in the design's §8.9:\n%s", i+1, p)
		}
	}
	if !strings.Contains(string(design), "The file is the source of the wording:") {
		t.Error("the design's §8.9 does not say SECURITY.md is the source of the wording")
	}
}

// designBlockQuote returns the text of the block quote under the numbered item
// starting with marker (its "> " prefixes stripped), which is how the design
// carries a block of SECURITY.md's prose.
func designBlockQuote(t *testing.T, doc, marker string) string {
	t.Helper()
	idx := strings.Index(doc, marker)
	if idx == -1 {
		t.Fatalf("the design has no %q item", marker)
	}
	var out []string
	for _, line := range strings.Split(doc[idx:], "\n") {
		if strings.HasPrefix(line, ">") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, ">")))
			continue
		}
		if len(out) > 0 {
			break
		}
	}
	if len(out) == 0 {
		t.Fatalf("the item %q carries no block quote", marker)
	}
	return strings.Join(out, " ")
}

// normSpace collapses every run of whitespace to one space, so two copies of a
// paragraph compare equal whatever each file's line wrapping is.
func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }
