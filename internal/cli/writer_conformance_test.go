package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/settings"
)

// writer_conformance_test.go is T1's executing half of the writer contract
// (docs/contracts/config-ownership.md §3): for every row of the settings
// registry it finds the writer or the label the registry answers with, runs the
// writer's own argv against a hermetic installation, and asserts the key
// changed in the store the writer named (D2610). A row no writer and no label
// answers for, and a row whose writer's argv does not write the key, are gaps;
// the gaps are testdata/writer_gaps.golden, which lists what the families of
// the plan's §3 have still to register. Regenerate it with
// `go test ./internal/cli -run 'TestWriterConformanceTheOwnerWritesTheKey' -update-writers`.
//
// The ratchet is deliberately one-sided while the families land: a gap the
// golden lists is not a failure, so T1 can merge before them, and a row the
// golden lists that now passes is reported with t.Log rather than failed —
// **I1** deletes those two exemptions and turns the file into a strict
// comparison. What is a failure from today is a gap nobody has recorded: a new
// row with no writer and no label, and a writer that no longer writes its key.
//
// Every row is probed on an installation of its own. The assertion is that
// *this* row's write changes *this* key, and two rows that spell one key — the
// wildcard `stages.*.claude_account` beside the seven concrete rows beside it —
// would otherwise see the second write answered with "the file already reads
// what was asked for".

// updateWriterGaps rewrites testdata/writer_gaps.golden. It is a flag of its
// own because the package's init_commands golden already owns -update, and a
// second flag.Bool of the same name panics.
var updateWriterGaps = flag.Bool("update-writers", false, "rewrite testdata/writer_gaps.golden")

// writerValueWord is the word a registered Argv carries where the value the
// caller supplies goes: the contract's `<value>`, which CR1 gives the registry
// as settings.ValuePlaceholder (docs/contracts/config-ownership.md §2) and
// which replaces this constant the day that revision lands. writers_table.go
// registers the same word and this file fills it, so the two spellings are one
// fact — a template that carries no such word fails below rather than being
// run with a literal placeholder as its value.
const writerValueWord = "<value>"

// writerGapsPath is where today's gaps are listed, one registry key per line,
// sorted, with # comments and blank lines allowed. It is absolute because the
// rig moves the process's working directory into a checkout, and a relative
// path would name a file inside it rather than the package's own testdata.
var writerGapsPath = func() string {
	path, err := filepath.Abs("testdata/writer_gaps.golden")
	if err != nil {
		panic(err)
	}
	return path
}()

// writerProbeNames is the concrete name a wildcard segment is probed at, keyed
// by the segments before it: a wildcard row is written at one concrete name
// (the contract's own rule — `workflows.*.commands.test` at
// `workflows.web.commands.test`), and these are the names the blocks' own tests
// and documents spell. A segment with no entry is probed at "conformance".
var writerProbeNames = map[string]string{
	"workflows":                  "web",
	"layer.profiles":             "web",
	"stages":                     "review",
	"escalation.modes":           "pro",
	"repos":                      "acme/web",
	"image_catalog.entries":      "tools",
	"layer.security.patterns":    "secret",
	"layer.defaults.rigor.areas": "src",
	"workspace_members":          "main",
	"project_secrets":            "CONFORMANCE",
	"workflow_secrets":           "CONFORMANCE",
	"bitbucket_tokens":           "conformance",
	"mobile_allow":               "operator",
	"layer.jobs":                 "reports",
}

// writerProbeLadder is the value ladder a scalar row is tried with, after the
// candidate its own default suggests: the registry states no type for a row, so
// what the test pins is that the owner's command writes the key at all, and the
// first word the document's own loader accepts is the value it is written with.
// The order is the one that writes most rows first — a boolean, a count, a
// duration, a list member, a name — so a passing row costs one command.
var writerProbeLadder = []string{
	"1", "2", "true", "false", "30m", "1h", "1m", "5s", "10s",
	"web", "svc", "conformance", "baked", "go", "24", "main", "github", "bitbucket",
	"2Gi", "30d", "off", "warn", "enforce", "acme/web", "src/**",
	"strong", "strongest", "fast", "oauth", "api-key", "vertex",
	"fugaro", "launcher", "18", "52",
}

// writerProbeListItems are the members a list key is tried with, after the
// first: an item is one word of the row's own type, and the registry states no
// type for a row, so a list of GitHub user IDs and a list of labels are told
// apart by what the document's loader takes.
var writerProbeListItems = []string{"conformance", "12345", "claude-sonnet-5"}

// writerProbeKey is the concrete spelling a row is probed at.
func writerProbeKey(key string) string {
	segments := strings.Split(key, ".")
	for i, segment := range segments {
		if segment != "*" {
			continue
		}
		segments[i] = writerProbeSegment(strings.Join(segments[:i], "."))
	}
	return strings.Join(segments, ".")
}

// writerProbeSegment is the concrete name a wildcard segment is probed at,
// including the last segment of a pattern that ends in one.
func writerProbeSegment(prefix string) string {
	if name, ok := writerProbeNames[prefix]; ok {
		return name
	}
	return "conformance"
}

// writerProbeFills is the words a row's value word is replaced with, in the
// order they are tried. A block's entry is written by --add-entry with the
// fields the entry needs — a map's one member, which the registry names for no
// block and the caller therefore spells whole, and an object list's key field
// beside one member, which is the pair the entry write requires — a string list
// takes one member, and a scalar takes one value.
func writerProbeFills(row settings.Row) [][]string {
	if _, block, ok := settingsEntryBlockPattern(row.Key); ok {
		if block.kind == settingsEntryMap {
			return [][]string{{"command=conformance"}, {"command=echo {id}"}}
		}
		return [][]string{{block.keyField + "=conformance", "env=CONFORMANCE"}}
	}
	if settingsHintListKeys[row.Key] {
		out := [][]string{}
		for _, item := range writerProbeListItems {
			out = append(out, []string{item})
		}
		return out
	}
	out := [][]string{}
	switch row.Default.Value {
	case "true":
		out = append(out, []string{"false"})
	case "false":
		out = append(out, []string{"true"})
	case "":
	default:
		if n, err := strconv.ParseFloat(row.Default.Value, 64); err == nil {
			out = append(out, []string{strconv.FormatFloat(n+1, 'f', -1, 64)})
		}
	}
	for _, value := range writerProbeLadder {
		out = append(out, []string{value})
	}
	return out
}

// writerRig is the hermetic installation one row is probed on: a project config
// on this machine (the local store), this machine's layer file (the project
// store) and a checkout of the project's own repository (the repo store). No
// cloud is reached: the installation's bucket is a directory, and no test here
// names an account.
type writerRig struct {
	localPath string
	layerPath string
	repoPath  string
	// db is the installation's budget database, the fake the local config
	// names as its rtdb_url: the store the cloud rows are asserted in.
	db *gcpfake.RTDB
}

func newWriterRig(t *testing.T) *writerRig {
	t.Helper()
	for _, marker := range agentMarkers {
		t.Setenv(marker, "")
	}
	home := t.TempDir()
	db := gcpfake.NewRTDB(t)
	isolateProjects(t, home)
	localPath := writeProject(t, "aurora", "proj-1234")
	appendWriterFile(t, localPath, writerLocalAccounts+writerLocalBudget(db, home))
	t.Setenv("FUGARO_CONFIG", localPath)
	settingsBucketAt(t)
	layerPath := settingsLayerOwnLayerFile(t, writerLayerYAML)
	repoPath := settingsWriteCheckout(t, writerRepoYAML)
	return &writerRig{localPath: localPath, layerPath: layerPath, repoPath: repoPath, db: db}
}

// writerLocalBudget is the installation's budget block and the two keys the
// budget commands read before they write: the database the caps live in (the
// fake, addressed by its own URL), the account a write records as its author,
// and the runs bucket whose marker names the project the database belongs to.
// Without them a cap write is refused for the installation rather than for the
// row, and the conformance test would measure the fixture.
func writerLocalBudget(db *gcpfake.RTDB, home string) string {
	db.Set("fugaro/project", "aurora")
	db.Set(budget.PathMark, auroraMark())
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		panic(err)
	}
	dir := filepath.Join(home, "runs")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(infra.ProjectMarkerObject))), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(infra.ProjectMarkerObject)), marker, 0o600); err != nil {
		panic(err)
	}
	return "budget: { mode: observe, firebase_project: proj-1234, rtdb_url: " + db.URL + " }\n" +
		"bucket_url: file://" + dir + "\nuser: someone@example.com\nendpoints: { no_auth: true }\n"
}

// writerLayerYAML is this machine's layer file, the body under the header
// settingsLayerOwnLayerFile writes: one profile the project defaults to, holding
// the commands and image steps a project publishes — so a layer row is written
// into a profile a person could have written, and a key the loader cross-checks
// against a neighbour finds it.
const writerLayerYAML = `profiles:
  web:
    commands:
      test: go test ./...
    image:
      apt:
        - git
  svc: {}
default_profile: web
`

// writerLocalAccounts is the account the installation declares, which the
// stage-account rows are held to (settingsStageAccountRule): a row of
// `stages.*.claude_account` names an account of this project's own file or
// "default", so an installation that declares none answers every name but
// "default" with a refusal about the name rather than about the write. The
// ladder offers "web", and this is what makes it a name the file holds.
const writerLocalAccounts = "claude_accounts:\n  web: { allow_data_to: [\"acme/web\"] }\n"

// writerRepoYAML is the repository the repo-store rows are written into: the
// smallest file config.Resolve accepts, carrying the commands, image steps and
// anchors a workflow a person has would — so a row is refused for its own value
// and not for a neighbour key a sparse fixture happened to lack (doubling a
// repository's `secrets`, or spilling a whole rebuild block, must not be how a
// row passes). Every mapping is a block: yamledit cannot splice a scalar into a
// flow collection, and a fixture whose shape the writer cannot edit would
// measure the fixture.
const writerRepoYAML = `version: 1
project: aurora
gcp_project: proj-1234
git:
  provider: github
workflows:
  web:
    commands:
      test: go test ./...
      build: go build ./...
      lint: go vet ./...
      fix: gofmt -w .
      reports:
        - "**/junit*.xml"
    image:
      apt:
        - git
      setup:
        - echo setup
      skip_build_scripts: true
`

// appendWriterFile adds the rig's own blocks to a file the fixtures wrote.
func appendWriterFile(t *testing.T, path, extra string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(data, []byte(extra)...), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// run executes one argv the way a person's shell would reach it, with stdin
// closed: no command of this plan may wait on input (docs/contracts/
// config-ownership.md §9), and an empty reader is what a closed one answers.
func (r *writerRig) run(argv []string) (string, error) {
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(NormalizeArgs(argv))
	err := cmd.Execute()
	return out.String() + errOut.String(), err
}

// document is the file a writer's store is and the path inside it the key
// lands on: the project layer's rows are spelled with the `layer.` prefix in
// the registry and without it in the file (settingsResolveTarget's own rule).
func writerDocument(rig *writerRig, store settings.Store, key string) (string, []string, error) {
	switch store {
	case settings.StoreLocal:
		return rig.localPath, strings.Split(key, "."), nil
	case settings.StoreRepo:
		return rig.repoPath, strings.Split(key, "."), nil
	case settings.StoreProject:
		return rig.layerPath, strings.Split(strings.TrimPrefix(key, "layer."), "."), nil
	}
	return "", nil, fmt.Errorf("the writer says the %s store, and this rig holds no file for it", store)
}

// writerStoreValue is what the store a writer named holds for the key when the
// write has run: the file and the path inside it for the three file stores, and
// the budget database's own node for a cloud row. It is the whole of the
// assertion's "the key changed in Store" (D2610) — a store this rig cannot read
// is an error rather than a pass.
func writerStoreValue(t *testing.T, rig *writerRig, store settings.Store, key string) (string, error) {
	t.Helper()
	if store == settings.StoreBudget {
		return writerBudgetValue(rig, key)
	}
	path, keyPath, err := writerDocument(rig, store, key)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %v", path, err)
	}
	return settingsValueIn(data, keyPath), nil
}

// writerBudgetValue is the budget database's value for a cloud-store row: the
// node the settings read reads the row from, which is its own table
// (settings_cloud.go's caps reader), rendered as one word so that before and
// after are one comparison. The rendering is not the read's `money` — the test
// asks whether the database changed, and a row that changes from unset to
// $5.00 is a change whichever way it is printed.
func writerBudgetValue(rig *writerRig, key string) (string, error) {
	var node, field string
	switch key {
	case "caps.mode":
		node = budget.PathMode
	case "caps.global.per_day_usd":
		node, field = budget.PathCapsGlobal, "dailyMicros"
	case "caps.global.per_run_usd":
		node, field = budget.PathCapsGlobal, "perRunMicros"
	case "caps.defaults.per_day_usd":
		node, field = budget.PathCapsDefaults, "repoDailyMicros"
	case "caps.defaults.per_run_usd":
		node, field = budget.PathCapsDefaults, "repoPerRunMicros"
	case "caps.max_reserve_usd":
		node, field = budget.PathLimits, "maxReserveMicros"
	case "kill.global.on":
		node, field = budget.PathKillGlobal, "on"
	default:
		return "", fmt.Errorf("no budget node is known for %s: the caps reader (settings_cloud.go) is the table this one mirrors", key)
	}
	value := rig.db.Value(node)
	if value == nil {
		return "", nil
	}
	if field == "" {
		return fmt.Sprint(value), nil
	}
	members, ok := value.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s holds %T and not a node with a %s member", node, value, field)
	}
	if members[field] == nil {
		return "", nil
	}
	return fmt.Sprint(members[field]), nil
}

// writerFillValue replaces the value words of an argv, left to right.
func writerFillValue(argv, fill []string) ([]string, error) {
	out := make([]string, len(argv))
	copy(out, argv)
	next := 0
	for i, word := range out {
		if word != writerValueWord {
			continue
		}
		if next >= len(fill) {
			return nil, fmt.Errorf("the registered argv carries more %s words than the %d value(s) it is filled with", writerValueWord, len(fill))
		}
		out[i] = fill[next]
		next++
	}
	if next != len(fill) {
		return nil, fmt.Errorf("the registered argv carries no %s word for %d of the %d value(s), so its value would be the literal placeholder", writerValueWord, len(fill)-next, len(fill))
	}
	return out, nil
}

// writerProbeWrite runs one row's writer and asserts the key changed in the
// store the writer named. The installation is built fresh for every attempt: a
// candidate the document's loader refuses writes nothing, and the next one has
// to meet the file as the first found it.
func writerProbeWrite(t *testing.T, row settings.Row, key string, w settings.Writer) error {
	t.Helper()
	fills := writerProbeFills(row)
	if !slices.Contains(w.Argv, writerValueWord) {
		// A writer whose command line carries no value word: the kill switch is
		// thrown by the command itself, so there is no word to vary and one
		// attempt is the whole measurement.
		fills = [][]string{nil}
	}
	refusals := map[string]int{}
	var refused string
	for _, fill := range fills {
		argv, err := writerFillValue(w.ArgvFor(key), fill)
		if err != nil {
			return err
		}
		rig := newWriterRig(t)
		was, err := writerStoreValue(t, rig, w.Store, key)
		if err != nil {
			return err
		}
		if len(fill) == 1 && fill[0] == was {
			// The candidate is the value the document already reads, and a write
			// answered with "already reads what was asked for" changes nothing the
			// test can see: the next candidate is the one that measures the writer.
			continue
		}
		out, err := rig.run(argv)
		if err != nil {
			// Every candidate's own refusal, counted rather than kept one by one:
			// the value handed to a refusal is the one it quotes back, and this
			// error reaches testdata/writer_gaps.golden's log lines, which list
			// keys and never values (a value a row's loader rejects is a value,
			// whatever it is spelled).
			refusals[writerRefusalKind(err, out)]++
			refused = strings.Join(argv, " ")
			continue
		}
		got, err := writerStoreValue(t, rig, w.Store, key)
		if err != nil {
			return err
		}
		if got != was && got != "" {
			return nil
		}
		refusals[fmt.Sprintf("exited zero and %s still reads what it read", key)]++
		refused = strings.Join(argv, " ")
	}
	if len(refusals) == 0 {
		return fmt.Errorf("no value candidate was tried for %s", key)
	}
	kinds := make([]string, 0, len(refusals))
	for kind, n := range refusals {
		kinds = append(kinds, fmt.Sprintf("%s (%d of %d candidates)", kind, n, len(fills)))
	}
	slices.Sort(kinds)
	return fmt.Errorf("every candidate failed, e.g. `fugaro %s`: %s", refused, strings.Join(kinds, "; "))
}

// writerRefusalKind is why a candidate did not write the key, as a sentence
// about the command rather than about the value: the root command silences its
// own errors and hands them back, so the message is the returned error, and the
// streams are read only for a command that failed without returning one.
func writerRefusalKind(err error, out string) string {
	text := strings.TrimSpace(out)
	if err != nil && err.Error() != "" {
		text = err.Error()
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Usage:") {
			continue
		}
		if len(line) > 160 {
			line = line[:160]
		}
		return line
	}
	return "exited non-zero with nothing on the streams"
}

// TestWriterConformanceEveryRowHasAWriterOrALabel is the containment rule: a
// row answers with a runnable writer or with a label, and a row that answers
// with neither is a gap the golden has to have recorded (items 4 and 8 of the
// owner's direction; D2610, D2611).
func TestWriterConformanceEveryRowHasAWriterOrALabel(t *testing.T) {
	if *updateWriterGaps {
		// The regeneration is the write test's: it is the one that measures both
		// halves, and this test would only read a file the other test rewrites.
		t.Skip("regenerating testdata/writer_gaps.golden")
	}
	known := writerGolden(t)
	for _, row := range settings.Rows() {
		key := writerProbeKey(row.Key)
		w, ok := settings.WriterOf(key)
		if ok && (w.Label != "" || len(w.Argv) > 0) {
			continue
		}
		if !known[key] {
			t.Errorf("%s has no writer and no label: register the command that writes it in writers_table.go, or its label and reason in writers_labels.go (L1), or record it in %s",
				key, writerGapsPath)
		}
	}
}

// TestWriterConformanceTheOwnerWritesTheKey runs every row that answers with a
// runnable writer: the registry's owner column names a writer, and this is the
// test that the writer's own command line really writes the key (item 4,
// D2610). A row that fails and is not in the golden is a failure; a row in the
// golden that now passes is reported and not failed, until I1 flips the ratchet.
func TestWriterConformanceTheOwnerWritesTheKey(t *testing.T) {
	known := writerGolden(t)
	gaps := map[string]string{}
	for _, row := range settings.Rows() {
		key := writerProbeKey(row.Key)
		w, ok := settings.WriterOf(key)
		if !ok {
			gaps[key] = "no writer and no label answers for it"
			continue
		}
		if w.Label != "" {
			continue
		}
		if err := writerProbeWrite(t, row, key, w); err != nil {
			gaps[key] = err.Error()
			continue
		}
		if known[key] {
			t.Logf("%s is written by `fugaro %s` today: %s may lose that line (I1 turns this into a failure)",
				key, strings.Join(w.ArgvFor(key), " "), writerGapsPath)
		}
	}
	if *updateWriterGaps {
		writerGoldenWrite(t, gaps)
		return
	}
	for key, why := range gaps {
		if !known[key] {
			t.Errorf("%s: %s", key, why)
		}
	}
}

// TestWriterGapsGoldenOnlyShrinks holds the golden's own shape: the file is a
// list of registry keys, sorted and without repeats, and every key is one a
// registry row still spells — so the file can lose a line when a family lands
// or a row goes away, and can gain one only where a measurement put it there.
// That a failing row is missing from it is the write test's failure, and a line
// for a row that now passes is the log line there.
func TestWriterGapsGoldenOnlyShrinks(t *testing.T) {
	lines := writerGoldenLines(t)
	if len(lines) == 0 {
		t.Fatalf("%s lists nothing: a file that lists no gap would pass this test on a tree where every row fails", writerGapsPath)
	}
	seen := map[string]bool{}
	for i, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if seen[line] {
			t.Errorf("%s lists %s twice", writerGapsPath, line)
		}
		if i > 0 && !strings.HasPrefix(lines[i-1], "#") && lines[i-1] != "" && lines[i-1] >= line {
			t.Errorf("%s is not sorted: %s comes after %s", writerGapsPath, line, lines[i-1])
		}
		seen[line] = true
		if !writerGapIsARegistryKey(line) {
			t.Errorf("%s lists %s, and no registry row spells it: a line for a row that is gone is a gap nothing can close", writerGapsPath, line)
		}
	}
}

// writerGapIsARegistryKey reports whether a golden line is a key a row of the
// registry still answers for, at the concrete spelling the row is probed at.
func writerGapIsARegistryKey(key string) bool {
	if _, ok := settings.Lookup(key); ok {
		return true
	}
	for _, row := range settings.Rows() {
		if writerProbeKey(row.Key) == key {
			return true
		}
	}
	return false
}

// writerGolden is the golden as a set of keys.
func writerGolden(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, line := range writerGoldenLines(t) {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

// writerGoldenLines reads the golden, comments and blank lines included, so the
// shape test can see them.
func writerGoldenLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(writerGapsPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with -update-writers)", writerGapsPath, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// writerGoldenWrite rewrites the golden from a measurement: the keys the
// registry answers for today, sorted, with the reason each one is a gap beside
// it as a comment, so the file says what a family has still to register.
func writerGoldenWrite(t *testing.T, gaps map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(gaps))
	for key := range gaps {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("# Every registry key the writer table cannot write today, one per line,\n")
	b.WriteString("# sorted: a row no writer and no label answers for, and a row whose writer's\n")
	b.WriteString("# argv does not change the key in the store it named. Generated by\n")
	b.WriteString("# `go test ./internal/cli -run TestWriterConformanceTheOwnerWritesTheKey -update-writers`.\n")
	b.WriteString("# It only shrinks: a family that lands removes its rows' lines (I1 makes it strict).\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "%s\n", key)
		// The file lists keys and never values, and the reasons are not values
		// either: a refusal quotes the value it was handed, so they are logged
		// for whoever regenerates the file rather than written beside a key.
		t.Logf("gap %s: %s", key, gaps[key])
	}
	if err := os.WriteFile(writerGapsPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing %s: %v", writerGapsPath, err)
	}
	t.Logf("%s: %d gaps", writerGapsPath, len(keys))
}
