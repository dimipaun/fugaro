package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The secret-reader audit (secrets-brokering plan, task B0.1): who reads a
// Secret Manager value today, one row per reader, each row carrying the
// decision that keeps it reading and the slice that ends it.
//
// The doc is the source of the IDs B0.2's guard lists in its own exceptions
// table, so its shape is a contract with that task and not a preference: this
// test pins the columns, the ID form, the kind and the exit vocabulary, and
// deliberately nothing else -- the prose around the table, the number of rows
// and the words in a cell are what B0.2, B1.10 and B1.13 edit.
//
// Both tests are named TestDocs... because every later docs task runs
// `-run Docs`, which is how a row that leaves the doc without leaving the
// guard (or the reverse) is caught by the task that moved it.
const (
	secretReadersDoc       = "docs/secrets-brokering/secret-readers.md"
	secretsBrokeringPlan   = "docs/secrets-brokering/implementation-plan.md"
	secretsBrokeringReadme = "docs/secrets-brokering/README.md"
	// release0140OperatorsDoc is the 0.14.0 operator-steps draft (task B1.13
	// part one), which the 0.14.0 release PR copies into that release's
	// "### For operators" section.
	release0140OperatorsDoc = "docs/secrets-brokering/release-0.14.0-operators.md"
)

// secretReaderColumns is the audit table's header, exactly: B0.2 parses these
// cells by position.
var secretReaderColumns = []string{"ID", "Kind", "Reader", "What it reads", "How", "Decision", "Exit slice"}

// secretsBrokeringSlicesHeader is the plan's own Slices table, the one place
// the exit vocabulary is written down.
var secretsBrokeringSlicesHeader = []string{"Slice", "Release", "What", "Inert?"}

var (
	secretReaderIDRE = regexp.MustCompile(`^E[0-9]+$`)
	sliceIDRE        = regexp.MustCompile(`^S[0-9]+$`)
)

// docRow is one data row of a markdown table, with the 1-based line it sits on
// so a failure names the row a reader can find.
type docRow struct {
	line  int
	cells []string
}

// tableCells splits one markdown table line into its cells. A line is a table
// row when it starts and ends with a pipe; the outer pipes go and the cells are
// trimmed, so a row parses the same however it is padded.
func tableCells(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
		return nil, false
	}
	cells := []string{}
	for _, cell := range strings.Split(trimmed[1:len(trimmed)-1], "|") {
		cells = append(cells, strings.TrimSpace(cell))
	}
	return cells, true
}

// tableSeparator reports whether cells is a table's |---|---| rule row.
func tableSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if cell == "" || strings.Trim(cell, "-:") != "" {
			return false
		}
	}
	return true
}

// docTable reads the one table in doc whose header row is exactly header: its
// data rows, and every table row that follows them (which a doc with one table
// has none of, so the caller can say so rather than miss a second table).
func docTable(t *testing.T, doc, path string, header []string) (rows, after []docRow) {
	t.Helper()
	lines := strings.Split(doc, "\n")
	found := -1
	for i, line := range lines {
		cells, ok := tableCells(line)
		if !ok || !slices.Equal(cells, header) {
			continue
		}
		if found >= 0 {
			t.Fatalf("%s has two tables whose header is %q: this test reads one", path, strings.Join(header, " | "))
		}
		found = i
	}
	if found < 0 {
		t.Fatalf("%s has no table whose header is exactly %q", path, strings.Join(header, " | "))
	}
	if found+1 >= len(lines) {
		t.Fatalf("%s:%d: the table's header is the file's last line, so there is no separator row", path, found+1)
	}
	if cells, ok := tableCells(lines[found+1]); !ok || !tableSeparator(cells) {
		t.Fatalf("%s:%d: the line after the table's header is not its |---| separator", path, found+2)
	}
	i := found + 2
	for ; i < len(lines); i++ {
		cells, ok := tableCells(lines[i])
		if !ok {
			break
		}
		rows = append(rows, docRow{line: i + 1, cells: cells})
	}
	for ; i < len(lines); i++ {
		if cells, ok := tableCells(lines[i]); ok {
			after = append(after, docRow{line: i + 1, cells: cells})
		}
	}
	return rows, after
}

// TestDocsSecretReadersTableShape: every row of the audit table has a unique
// E<number> ID, a kind of runtime or operator, a decision, and an exit slice
// the plan's own Slices table names (or never). It is the shape B0.2 parses.
func TestDocsSecretReadersTableShape(t *testing.T) {
	exits := secretsBrokeringExitSlices(t)
	rows, after := docTable(t, readDoc(t, secretReadersDoc), secretReadersDoc, secretReaderColumns)
	if len(rows) == 0 {
		t.Fatalf("%s's audit table has no rows: an audit with no reader is not the table B0.2 keys its exceptions on", secretReadersDoc)
	}
	for _, r := range after {
		t.Errorf("%s:%d: a table row follows the audit table: the doc holds one table, and B0.2 parses it", secretReadersDoc, r.line)
	}
	seen := map[string]int{}
	for _, r := range rows {
		if len(r.cells) != len(secretReaderColumns) {
			t.Errorf("%s:%d: the row has %d cells, want %d (%s)", secretReadersDoc, r.line, len(r.cells), len(secretReaderColumns), strings.Join(secretReaderColumns, " | "))
			continue
		}
		id := r.cells[0]
		switch {
		case !secretReaderIDRE.MatchString(id):
			t.Errorf("%s:%d: the ID %q is not of the form E<number>", secretReadersDoc, r.line, id)
		case seen[id] > 0:
			t.Errorf("%s:%d: the ID %s is already the ID of the row on line %d: B0.2's exceptions table is keyed by these IDs, so a duplicate would make one row unreachable", secretReadersDoc, r.line, id, seen[id])
		default:
			seen[id] = r.line
		}
		if kind := r.cells[1]; kind != "runtime" && kind != "operator" {
			t.Errorf("%s:%d: %s's kind is %q, want runtime or operator", secretReadersDoc, r.line, id, kind)
		}
		if r.cells[5] == "" {
			t.Errorf("%s:%d: %s names no decision: every row says which decision keeps it reading", secretReadersDoc, r.line, id)
		}
		if exit := r.cells[6]; exit != "never" && !slices.Contains(exits, exit) {
			t.Errorf("%s:%d: %s's exit slice is %q, want one of %s or never", secretReadersDoc, r.line, id, exit, strings.Join(exits, ", "))
		}
	}
}

// secretsBrokeringExitSlices is the exit vocabulary the audit's last column may
// use, read out of the plan's own Slices table rather than written down a second
// time here: a slice the plan adds is an exit the doc may name, and one the plan
// renames is a value this test then refuses.
//
// The table's first row -- the slice this audit itself ships in -- is dropped.
// An exit names the slice that ends a read, and a reader cannot exit into the
// slice that is auditing it.
func secretsBrokeringExitSlices(t *testing.T) []string {
	t.Helper()
	rows, _ := docTable(t, readDoc(t, secretsBrokeringPlan), secretsBrokeringPlan, secretsBrokeringSlicesHeader)
	var ids []string
	for _, r := range rows {
		if len(r.cells) == 0 {
			continue
		}
		if id := strings.Trim(r.cells[0], "* "); sliceIDRE.MatchString(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) < 2 {
		t.Fatalf("%s's Slices table yielded %d slice names (%v): this test reads the exit vocabulary from it, so a table it cannot read is a table that cannot check the doc", secretsBrokeringPlan, len(ids), ids)
	}
	return ids[1:]
}

// TestDocsSecretsBrokeringReadmeStatus pins one sentence of the README's status
// line and nothing else about that file: B0.4 writes it and B0.2, B1.10 and
// B1.11 edit it after, so a pin on its shape would fail each of them. What has
// to survive every one of those edits is the date the owner answered the
// decision block this whole directory is built on -- the fact a reader checks
// the rest of the plan against.
func TestDocsSecretsBrokeringReadmeStatus(t *testing.T) {
	status := flattenDoc(readDoc(t, secretsBrokeringReadme))
	const want = "The owner answered D490–D506 on 2026-10-27"
	if !strings.Contains(status, want) {
		t.Errorf("%s no longer says %q: the owner's answers of that date are what every slice in this directory is approved against", secretsBrokeringReadme, want)
	}
}

// secretsBrokeringLevelsHeader is the plan's own Levels table, the one place the
// release a task ships in is written down. The tests below read it rather than
// writing a release number down a second time, so the plan and the docs cannot
// drift apart -- which is how the owner's 2026-10-30 move, the Bitbucket proxy
// chain leaving 0.14.0 for 0.15.0, reached the docs without reaching the plan.
var secretsBrokeringLevelsHeader = []string{"Release", "Level", "Tasks (concurrent)"}

// secretsBrokeringProxyChain is the chain the owner moved: the service's route
// (B1.5), the runner's forwarder and proxy mode (B1.6, B1.7), doctor's checks
// (B1.9), the docs (B1.10) and the two live checks written (B1.11). The draft
// and the README name the release the plan's Levels table gives this group, and
// that release is not the one whose pieces ship inert.
var secretsBrokeringProxyChain = []string{"B1.5", "B1.6", "B1.7", "B1.9", "B1.10", "B1.11"}

// secretsBrokeringInertPieces are the S1 tasks that stay in the release before
// the chain: policy v4 with the block and the `proxy` switch (BW-T2.1, BW-T2.3),
// the API-root pin (BW-T2.8), the bundle's Bitbucket token (B1.2), the proxy's
// parser and rules (B1.3, B1.4) and the flip list, still empty (B1.8). None of
// them proxies a run on its own.
var secretsBrokeringInertPieces = []string{"BW-T2.1", "BW-T2.3", "BW-T2.8", "B1.2", "B1.3", "B1.4", "B1.8"}

// secretsBrokeringTaskReleases reads the plan's Levels table into task ID ->
// release. A task split across two releases ("B1.13 part one") is keyed with its
// part, because the two parts are two releases.
func secretsBrokeringTaskReleases(t *testing.T) map[string]string {
	t.Helper()
	rows, _ := docTable(t, readDoc(t, secretsBrokeringPlan), secretsBrokeringPlan, secretsBrokeringLevelsHeader)
	if len(rows) == 0 {
		t.Fatalf("%s's Levels table has no rows: this test reads every task's release from it", secretsBrokeringPlan)
	}
	releases := map[string]string{}
	for _, r := range rows {
		if len(r.cells) != len(secretsBrokeringLevelsHeader) {
			t.Fatalf("%s:%d: the Levels row has %d cells, want %d", secretsBrokeringPlan, r.line, len(r.cells), len(secretsBrokeringLevelsHeader))
		}
		for _, task := range strings.Split(r.cells[2], ",") {
			fields := strings.Fields(strings.Trim(task, "* "))
			if len(fields) == 0 {
				continue
			}
			id := fields[0]
			if len(fields) > 2 && fields[1] == "part" {
				id = fields[0] + " part " + fields[2]
			}
			releases[id] = r.cells[0]
		}
	}
	return releases
}

// secretsBrokeringReleaseFor is the one release every task in tasks ships in,
// out of the plan's Levels table, or "" when the group is empty. It fails when
// the table gives a task no release, or splits the group across releases: either
// way the docs have no single release to be held to.
func secretsBrokeringReleaseFor(t *testing.T, tasks []string) string {
	t.Helper()
	releases := secretsBrokeringTaskReleases(t)
	var found string
	for _, task := range tasks {
		release, ok := releases[task]
		if !ok {
			t.Fatalf("%s's Levels table gives no release for %s: this test reads the release the docs must name from it", secretsBrokeringPlan, task)
		}
		if found == "" {
			found = release
			continue
		}
		if release != found {
			t.Errorf("%s's Levels table splits this group across releases: %s ships in %q, the earlier entries in %q", secretsBrokeringPlan, task, release, found)
		}
	}
	return found
}

// TestDocsSecretsBrokeringOperatorSteps: the draft an operator reads for the
// release whose pieces are inert says only what that release ships. The owner
// moved the Bitbucket proxy chain to the next release on 2026-10-30, so the
// release whose pieces ship first keeps a Bitbucket run's
// job holding its own token and `proxy: true` changing nothing a run does, and
// the draft has to say so: an operator who reads the proxy steps as this
// release's keeps waiting for a behaviour that is not here. The releases are
// read out of the plan's own Levels table, so a draft that moves without the
// plan -- or the plan without the draft -- fails here.
func TestDocsSecretsBrokeringOperatorSteps(t *testing.T) {
	proxyRelease := secretsBrokeringReleaseFor(t, secretsBrokeringProxyChain)
	piecesRelease := secretsBrokeringReleaseFor(t, secretsBrokeringInertPieces)
	if proxyRelease == piecesRelease {
		t.Errorf("%s ships the proxy chain and the inert pieces in one release %q: the chain is the release after the pieces, and this test reads the split from that", secretsBrokeringPlan, proxyRelease)
	}
	draft := flattenDoc(readDoc(t, release0140OperatorsDoc))
	for _, want := range []string{
		// The step every installation owes, which the move does not touch.
		"policy v4 lockstep",
		"Bitbucket brokering (the proxy) arrives in " + proxyRelease,
		"In " + piecesRelease + " a Bitbucket run's job still holds its own token",
		"`proxy: true` in the published block changes nothing such a run does",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("%s does not say %q", release0140OperatorsDoc, want)
		}
	}
}

// TestDocsSecretsBrokeringReadmeReleaseTable: the README's release table is
// where a reader learns which release ships what, and its row for the release
// the inert pieces ship in promised the Bitbucket proxy. The chain moved on
// 2026-10-30, so that row names what its release actually ships and the chain's
// own row names the proxy. Both rows, and the plan's own Slices row, are held to
// the releases the plan's Levels table gives the two groups.
func TestDocsSecretsBrokeringReadmeReleaseTable(t *testing.T) {
	proxyRelease := secretsBrokeringReleaseFor(t, secretsBrokeringProxyChain)
	piecesRelease := secretsBrokeringReleaseFor(t, secretsBrokeringInertPieces)
	readme := flattenDoc(readDoc(t, secretsBrokeringReadme))
	for _, want := range []string{
		"| **" + piecesRelease + "** | **S1, data and inert pieces only:**",
		"the Bitbucket proxy itself arrives in " + proxyRelease + ", so no proxied run",
		"| **" + proxyRelease + "** | The Bitbucket proxy for per-repository jobs of opted-in installations",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s does not say %q", secretsBrokeringReadme, want)
		}
	}
	rows, _ := docTable(t, readDoc(t, secretsBrokeringPlan), secretsBrokeringPlan, secretsBrokeringSlicesHeader)
	for _, r := range rows {
		if len(r.cells) == 0 || strings.Trim(r.cells[0], "* ") != "S1" {
			continue
		}
		if row := strings.Join(r.cells, " | "); !strings.Contains(row, proxyRelease) {
			t.Errorf("%s:%d: the S1 row does not name %q, the release the Levels table gives the proxy chain: the plan's own Slices table is the third place the release is read", secretsBrokeringPlan, r.line, proxyRelease)
		}
		return
	}
	t.Errorf("%s's Slices table has no S1 row: this test holds it to the proxy chain's release", secretsBrokeringPlan)
}

// TestDocsCheck190IsRegistered: Check 190 — this work's one owner-run check, and
// the one number of its block that is registered — has both halves of its
// registration in the register: a `| 190 |` row in the summary table and a
// `## Check 190:` section below it, and the section says the owner has run it and
// that no agent runs any step of it. The pairing is the convention every work's
// own docs tests carry (docs_checks_145_149_test.go for 145–149): a row without
// its section is a promise of steps that are not written, and a section without
// its row is a check nothing points at, so either half alone is a number a
// reader cannot use. B0.6 recorded the owner's run of 2026-10-30 in that
// section, so this pin holds the registration and the STATEMENT that the check
// has been run, never the words of the result: the facts are the owner's paste,
// and a rerun rewrites them.
func TestDocsCheck190IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	if !regexp.MustCompile(`(?m)^\|\s*190\s*\|`).MatchString(register) {
		t.Errorf("%s's register has no `| 190 |` row: Check 190 is written below it, and the table is where a reader finds it", liveChecksDoc)
	}
	heading := "\n## Check 190:"
	i := strings.Index(register, heading)
	if i < 0 {
		t.Fatalf("%s has no `## Check 190:` section: the row above points at steps that have to be there", liveChecksDoc)
	}
	// Bounded by the next `## ` heading, so a check written after this one does
	// not widen what is read here.
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		// The owner ran the check on 2026-10-30 (task B0.5, recorded by B0.6):
		// the section states the run rather than the NOT RUN banner it carried
		// before.
		"**run 2026-10-30",
		// The register's own rule for an owner-run check: no agent runs any step
		// of it, and the text has to say so.
		"no agent runs any step of it",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 190's section does not say %q: the check is owner-run and its run is recorded, and a section that leaves either half out reads as something an agent did", want)
		}
	}
}

// TestDocsCheck195IsRegistered: B4.1 writes Check 195, the reading S4's build
// exception rests on, and the register carries both halves of its registration —
// a `| 195 |` row in the summary table and a `## Check 195:` section below it —
// in the same pairing TestDocsCheck190IsRegistered pins for Check 190: a row
// without its section is a promise of steps that are not written, and a section
// without its row is a check nothing points at. The section says three things a
// reader needs and cannot infer: the check is **NOT RUN** (the owner runs it in
// B4.2, and its answer is the gate S4 stops on), no agent runs any step of it,
// and the owner's own steps are in the work's own file rather than in the
// register.
func TestDocsCheck195IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	if !regexp.MustCompile(`(?m)^\|\s*195\s*\|`).MatchString(register) {
		t.Errorf("%s's register has no `| 195 |` row: Check 195 is written below it, and the table is where a reader finds it", liveChecksDoc)
	}
	heading := "\n## Check 195:"
	i := strings.Index(register, heading)
	if i < 0 {
		t.Fatalf("%s has no `## Check 195:` section: the row above points at steps that have to be there", liveChecksDoc)
	}
	// Bounded by the next `## ` heading, so a check written after this one does
	// not widen what is read here.
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		// The banner, before the owner's gate (B4.2) has been run: the section
		// states that nothing has measured this yet.
		"**run 2026-11-26: done",
		// The register's own rule for an owner-run check: no agent runs any step
		// of it, and the text has to say so.
		"no agent runs any step of it",
		// Where the owner's own steps live, so the section does not have to
		// carry them (and cannot drift from them).
		"secrets-brokering/check195-steps.md",
		// The gate: a FAIL here is a decision for the owner and stops S4, which
		// is the one thing a reader of this row has to know before running it.
		"stops s4",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 195's section does not say %q: the check is owner-run, not yet run, its steps are the work's own file, and a FAIL stops S4 — a section that leaves any of them out reads as something an agent did or as a reading that is already in", want)
		}
	}
}

// TestDocsCheck196IsRegistered: B4.7 writes Check 196, S4's own reading — a
// GitHub and a Bitbucket sandbox repository whose image build and daily image
// check bake with a credential the service served, the boundary the service's
// own policy draws around whose build may call it, and the flip measured by the
// same script's `--after-flip` — and the register carries both halves of its
// registration, in the pairing TestDocsCheck190IsRegistered and
// TestDocsCheck195IsRegistered pin: a `| 196 |` row in the summary table and a
// `## Check 196:` section below it. A row without its section is a promise of
// steps that are not written, and a section without its row is a check nothing
// points at. The section says four things a reader needs and cannot infer: the
// check is **NOT RUN** (the owner runs it in B4.8, and again in B4.10 after the
// flip), no agent runs any step of it, the owner's own steps are in the work's
// own file rather than in the register, and it has two halves — one script, one
// `--after-flip` — because a section that named only the first would read as a
// check the flip is not measured by. A FAIL stops S4 and returns to the owner
// as a decision, which is what B4.8's own row in the plan says of it.
func TestDocsCheck196IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	if !regexp.MustCompile(`(?m)^\|\s*196\s*\|`).MatchString(register) {
		t.Errorf("%s's register has no `| 196 |` row: Check 196 is written below it, and the table is where a reader finds it", liveChecksDoc)
	}
	heading := "\n## Check 196:"
	i := strings.Index(register, heading)
	if i < 0 {
		t.Fatalf("%s has no `## Check 196:` section: the row above points at steps that have to be there", liveChecksDoc)
	}
	// Bounded by the next `## ` heading, so a check written after this one does
	// not widen what is read here.
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		// The banner, before either run of it: nothing has measured S4 yet.
		"**not run",
		// The register's own rule for an owner-run check: no agent runs any step
		// of it, and the text has to say so.
		"no agent runs any step of it",
		// Where the owner's own steps live, so the section does not have to carry
		// them (and cannot drift from them).
		"secrets-brokering/check196-steps.md",
		// The second half: one script, and the mode the flip is read by.
		"check196.sh --after-flip",
		// The gate: a FAIL here is a decision for the owner and stops S4, which
		// is the one thing a reader of this row has to know before running it.
		"stops s4",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 196's section does not say %q: the check is owner-run, not yet run, its steps are the work's own file, its second half is the --after-flip mode, and a FAIL stops S4 — a section that leaves any of them out reads as something an agent did, or as a check the flip is not measured by", want)
		}
	}
}

// TestDocsCheck193IsRegistered: B2.5 writes Check 193, S2's own reading — an
// `api-key` and an `oauth` repository through the credentials service, and the
// flip measured by the same script's `--after-flip` — and the register carries
// both halves of its registration, in the pairing TestDocsCheck190IsRegistered
// and TestDocsCheck195IsRegistered pin: a `| 193 |` row in the summary table and
// a `## Check 193:` section below it. A row without its section is a promise of
// steps that are not written, and a section without its row is a check nothing
// points at. The section says four things a reader needs and cannot infer: the
// check is **NOT RUN** (the owner runs it in B2.6, and again in B2.8 after the
// flip), no agent runs any step of it, the owner's own steps are in the work's
// own file rather than in the register, and it has two halves — one script, one
// `--after-flip` — because a section that named only the first would read as a
// check the flip is not measured by.
func TestDocsCheck193IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	if !regexp.MustCompile(`(?m)^\|\s*193\s*\|`).MatchString(register) {
		t.Errorf("%s's register has no `| 193 |` row: Check 193 is written below it, and the table is where a reader finds it", liveChecksDoc)
	}
	heading := "\n## Check 193:"
	i := strings.Index(register, heading)
	if i < 0 {
		t.Fatalf("%s has no `## Check 193:` section: the row above points at steps that have to be there", liveChecksDoc)
	}
	// Bounded by the next `## ` heading, so a check written after this one does
	// not widen what is read here.
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		// The banner, before either run of it: nothing has measured S2 yet.
		"**not run",
		// The register's own rule for an owner-run check: no agent runs any step
		// of it, and the text has to say so.
		"no agent runs any step of it",
		// Where the owner's own steps live, so the section does not have to carry
		// them (and cannot drift from them).
		"secrets-brokering/check193-steps.md",
		// The second half: one script, and the mode the flip is read by.
		"check193.sh --after-flip",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 193's section does not say %q: the check is owner-run, not yet run, its steps are the work's own file, and its second half is the --after-flip mode — a section that leaves any of them out reads as something an agent did, or as a check the flip is not measured by", want)
		}
	}
}

// TestDocsCheck197IsRegistered: B5.5 writes Check 197, S5's own reading — a
// coding run on a shared project job served by the credentials service, a
// repository outside the job's profile refused, and the flip measured by the
// same script's `--after-flip` — and the register carries both halves of its
// registration, in the pairing TestDocsCheck190IsRegistered,
// TestDocsCheck195IsRegistered and TestDocsCheck193IsRegistered pin: a
// `| 197 |` row in the summary table and a `## Check 197:` section below it. A
// row without its section is a promise of steps that are not written, and a
// section without its row is a check nothing points at. The section says four
// things a reader needs and cannot infer: the check is **NOT RUN** (the owner
// runs it in B5.6, and again in B5.8 after the flip, which B5.7 gates), no
// agent runs any step of it, the owner's own steps are in the work's own file
// rather than in the register, and it has two halves — one script, one
// `--after-flip` — because a section that named only the first would read as a
// check the flip is not measured by. A FAIL stops S5 and returns to the owner
// as a decision, which is what B5.6's own row in the plan says of it.
func TestDocsCheck197IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	if !regexp.MustCompile(`(?m)^\|\s*197\s*\|`).MatchString(register) {
		t.Errorf("%s's register has no `| 197 |` row: Check 197 is written below it, and the table is where a reader finds it", liveChecksDoc)
	}
	heading := "\n## Check 197:"
	i := strings.Index(register, heading)
	if i < 0 {
		t.Fatalf("%s has no `## Check 197:` section: the row above points at steps that have to be there", liveChecksDoc)
	}
	// Bounded by the next `## ` heading, so a check written after this one does
	// not widen what is read here.
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		// The banner, before either run of it: nothing has measured S5 yet.
		"**not run",
		// The register's own rule for an owner-run check: no agent runs any step
		// of it, and the text has to say so.
		"no agent runs any step of it",
		// Where the owner's own steps live, so the section does not have to carry
		// them (and cannot drift from them).
		"secrets-brokering/check197-steps.md",
		// The second half: one script, and the mode the flip is read by.
		"check197.sh --after-flip",
		// The gate: a FAIL here is a decision for the owner and stops S5, which
		// is the one thing a reader of this row has to know before running it.
		"stops s5",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 197's section does not say %q: the check is owner-run, not yet run, its steps are the work's own file, its second half is the --after-flip mode, and a FAIL stops S5 — a section that leaves any of them out reads as something an agent did, or as a check the flip is not measured by", want)
		}
	}
}

// TestDocsCheck194IsRegistered: B3.6 writes Check 194, S3's own reading — a
// repository's workflow secrets served by the credentials service instead of
// mounted into its job, and the flip measured by the same script's
// `--after-flip` — and the register carries both halves of its registration, in
// the pairing TestDocsCheck190IsRegistered, TestDocsCheck193IsRegistered and
// TestDocsCheck195IsRegistered pin: a `| 194 |` row in the summary table and a
// `## Check 194:` section below it. A row without its section is a promise of
// steps that are not written, and a section without its row is a check nothing
// points at. The section says five things a reader needs and cannot infer: the
// check is **NOT RUN** (the owner runs it in B3.7, and again in B3.9 after the
// flip), no agent runs any step of it, the owner's own steps are in the work's
// own file rather than in the register, it has two halves — one script, one
// `--after-flip` — because a section that named only the first would read as a
// check the flip is not measured by, and the containers the record names are the
// installation's PROJECT ones and never a repository's (D2313), which is the one
// reading a person setting the allow-list up cannot guess: the plan's "one
// per-repository and one project workflow secret" predates that decision, and a
// section that left it out would send the owner looking for a container the
// merged code never writes.
func TestDocsCheck194IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	if !regexp.MustCompile(`(?m)^\|\s*194\s*\|`).MatchString(register) {
		t.Errorf("%s's register has no `| 194 |` row: Check 194 is written below it, and the table is where a reader finds it", liveChecksDoc)
	}
	heading := "\n## Check 194:"
	i := strings.Index(register, heading)
	if i < 0 {
		t.Fatalf("%s has no `## Check 194:` section: the row above points at steps that have to be there", liveChecksDoc)
	}
	// Bounded by the next `## ` heading, so a check written after this one does
	// not widen what is read here.
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		// The banner, before either run of it: nothing has measured S3 yet.
		"**not run",
		// The register's own rule for an owner-run check: no agent runs any step
		// of it, and the text has to say so.
		"no agent runs any step of it",
		// Where the owner's own steps live, so the section does not have to carry
		// them (and cannot drift from them).
		"secrets-brokering/check194-steps.md",
		// The second half: one script, and the mode the flip is read by.
		"check194.sh --after-flip",
		// Which containers the record may name, which is the reading D2313 moved
		// after this check was planned.
		"**d2313**",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check 194's section does not say %q: the check is owner-run, not yet run, its steps are the work's own file, its second half is the --after-flip mode, and the containers it reads are the installation's project ones whatever the allow-list entry says — a section that leaves any of them out reads as something an agent did, as a check the flip is not measured by, or as a per-repository container the merged code never writes", want)
		}
	}
}

// TestDocsSecretReadersStatesTheBitbucketProxysState is B1.10's pin on the
// audit's Bitbucket rows, corrected by B1.13 part two. The proxy shipped (B1.5,
// B1.6, B1.7), so a reader has to learn two things and not one: on a repository
// the published node admits with `proxy: true` the job reads no `bitbucket-token`
// at all (the runner opens its provider tokenless and the service puts the
// credential on the wire), and the flip that takes the mount and the accessor
// away has landed -- `config.ProxiedGitSecretNames` names the token -- so E1's
// row is the token given up for that repository and no other. A doc that says
// only one of the two reads as "the proxy took the token everywhere" or as
// "nothing changed".
//
// Mutation (verified, run and restored): the paragraph's "given up" sentence
// dropped fails this on that pin.
func TestDocsSecretReadersStatesTheBitbucketProxysState(t *testing.T) {
	flat := flattenDoc(readDoc(t, secretReadersDoc))
	for _, want := range []string{
		// The half that moved: a proxied job reads no token.
		"a proxied job reads no token at all",
		// The half the flip moves: the mount and the accessor are gone for a
		// proxied repository's job, and the list is what names the credential.
		"config.ProxiedGitSecretNames",
		"names `bitbucket-token`",
		"given up",
		// And the third copy that is older than both: the build account's,
		// which S4 ends (E3).
		"build account keeps its own copy until S4",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q: the Bitbucket rows have to state what the proxy changed and what the flip has taken away", secretReadersDoc, want)
		}
	}
}
