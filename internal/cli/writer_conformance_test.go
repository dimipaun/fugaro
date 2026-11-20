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
// The ratchet is strict, in both directions, since I1. The file is the ledger
// of every registry row the ratchet cannot prove a running command writes, and
// every line of it carries the row's own state: the label the registry answers
// it with, or a reason it is still open. A row the ratchet does not prove and
// the file does not list fails — that is the containment T1 landed — and a line
// whose row has come to be proven fails too, so a line is deleted by the change
// that makes it untrue rather than left to rot and the file only ever shrinks.
// The two tests that read it are TestWriterConformanceTheOwnerWritesTheKey,
// which is the measurement, and TestWriterConformanceStrict, which audits the
// ledger itself against the registry and runs no command.
//
// What the file is not is a list of tasks: a `label:` line is a row no command
// writes because nothing should (D2611), and an `open:` line is one this plan
// left for a later one, with the reason it is open.
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

// writerLineKind is which of the two states a golden line records: a row the
// registry answers with a label, and a row the ratchet leaves open.
type writerLineKind int

const (
	// writerLabelledLine is a row the registry answers with a label
	// (writers_labels.go, L1): no command writes its key, the label says why,
	// and the row is never run, so its line leaves the file only when a writer
	// takes the label's place (D2611).
	writerLabelledLine writerLineKind = iota + 1
	// writerOpenLine is a row the ratchet cannot prove: either nothing answers
	// for it at all, or a runnable writer does and the probe could not show it
	// changing the key.
	writerOpenLine
)

// The two open reasons, which are the whole vocabulary: each names a state of
// the registry and of the measurement rather than a task, so a line either
// states the truth about its row or fails TestWriterConformanceStrict.
const (
	// writerOpenNoWriter is a row no writer and no label answers for: the
	// family its Owner token names has not registered the command that writes
	// it (D2609, D2610).
	writerOpenNoWriter = "no-writer"
	// writerOpenWriterDoesNotWrite is a row a runnable writer answers for whose
	// argv the probe could not show changing the key in the store the writer
	// named — measured per candidate, and never a claim about the writer: the
	// probe's own fixture and value ladder are half of it (writerProbeWrite).
	writerOpenWriterDoesNotWrite = "writer-does-not-write-the-key"
)

// writerLine is one golden line's verdict: the kind and, for a line that has
// one, the label or the open reason. It is comparable, which is what lets the
// file and the measurement be compared as values rather than field by field.
type writerLine struct {
	kind  writerLineKind
	value string
}

// String is the line as the file spells it: `<key>` is the caller's, and this
// is the field beside it.
func (l writerLine) String() string {
	switch l.kind {
	case writerLabelledLine:
		return "label:" + l.value
	case writerOpenLine:
		return "open:" + l.value
	}
	return "?"
}

// writerLineFor is the line a measured state is written as.
func writerLineFor(kind writerLineKind, value string) writerLine {
	return writerLine{kind: kind, value: value}
}

// writerLineParse reads a line back, and refuses a verdict outside the
// vocabulary rather than reading it as something else: the file is a ledger of
// two states, and a third spelling is a state nobody defined.
func writerLineParse(text string) (writerLine, error) {
	kind, value, ok := strings.Cut(text, ":")
	if !ok {
		return writerLine{}, fmt.Errorf("%q is a bare word: a line is <key>, a tab, and label:<label> or open:<reason>", text)
	}
	switch kind {
	case "label":
		switch settings.Label(value) {
		case settings.LabelPlumbing, settings.LabelFakeEndpoint, settings.LabelFile:
			return writerLineFor(writerLabelledLine, value), nil
		}
		return writerLine{}, fmt.Errorf("the label %q is not one of %s, %s, %s (writers_labels.go, D2611)", value, settings.LabelPlumbing, settings.LabelFakeEndpoint, settings.LabelFile)
	case "open":
		switch value {
		case writerOpenNoWriter, writerOpenWriterDoesNotWrite:
			return writerLineFor(writerOpenLine, value), nil
		}
		return writerLine{}, fmt.Errorf("the reason %q is neither %s nor %s, which are the two states an open row is in", value, writerOpenNoWriter, writerOpenWriterDoesNotWrite)
	}
	return writerLine{}, fmt.Errorf("the verdict %q is neither label:<label> nor open:<reason>", kind)
}

// writerGoldenEntry is one non-comment line of the golden: the key, the verdict
// beside it, and the line as the file spells it, which the shape checks read.
type writerGoldenEntry struct {
	key     string
	line    writerLine
	spelled string
}

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
	"2Gi", "30d", "off", "warn", "observe", "enforce", "acme/web", "src/**",
	"strong", "strongest", "fast", "oauth", "api-key", "vertex",
	"fugaro", "launcher", "18", "52",
	// A fraction, the one value domain the registry's rows carry that this list
	// had none of: a stage's budget is a share of the run's cap, and the loader
	// refuses every whole number beside it (config.StageBudget).
	"0.5",
}

// writerProbeListItems are the members a list key is tried with, after the
// first: an item is one word of the row's own type, and the registry states no
// type for a row, so a list of GitHub user IDs and a list of labels are told
// apart by what the document's loader takes. The last is the shape
// `verifier_accounts` is held to — the bare address a Google ID token's email
// claim carries (policy.ValidVerifierAccounts), which refuses every word beside
// it — so a probe of that row measures its writer and not the loader.
var writerProbeListItems = []string{"conformance", "12345", "claude-sonnet-5", "verifier@example.invalid"}

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
//
// Every mapping is a block, for writerRepoYAML's own reason: yamledit cannot
// splice a member into a flow collection, and the budget rows default to THIS
// file now (settingsDefaultLayer's merge rule, D2605), so a `budget: { ... }`
// written for compactness would measure the fixture's shape rather than the
// row's writer — which is what it did the day the default moved here.
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
	return "budget:\n" +
		"  mode: observe\n" +
		"  firebase_project: proj-1234\n" +
		"  rtdb_url: " + db.URL + "\n" +
		"bucket_url: file://" + dir + "\nuser: someone@example.com\n" +
		"endpoints:\n  no_auth: true\n"
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
		// A settings write runs the follow-up steps its key owes (AP5), and a
		// step that publishes stops at a confirmation this hermetic installation
		// cannot give: the key is written and the command exits non-zero. What
		// this test measures is that the writer writes the key, which
		// --no-apply leaves exactly as it was and answers with exit zero.
		if len(argv) >= 2 && argv[0] == "settings" && (argv[1] == "set" || argv[1] == "unset") {
			argv = append(argv, "--no-apply")
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
	recorded := writerGolden(t)
	for _, row := range settings.Rows() {
		key := writerProbeKey(row.Key)
		w, ok := settings.WriterOf(key)
		if ok && (w.Label != "" || len(w.Argv) > 0) {
			continue
		}
		line, ok := recorded[key]
		if !ok {
			t.Errorf("%s has no writer and no label: register the command that writes it in writers_table.go, or its label and reason in writers_labels.go (L1), or record it in %s",
				key, writerGapsPath)
			continue
		}
		if line != writerLineFor(writerOpenLine, writerOpenNoWriter) {
			t.Errorf("%s answers no writer and no label and %s records it as %s: the row's line says which of the two states it is in", key, writerGapsPath, line)
		}
	}
}

// writerMeasured is the whole measurement the golden is written from and
// checked against: every registry row's key with the line the file must carry
// for it, and no entry at all for a row a running command proved it writes
// (item 4 of the owner's direction, D2610).
//
// The three states are the registry's own answer first — nothing answers for
// the key, or a label does — and the probe's only where a runnable writer does.
// A labelled row is not run: the label and its reason are the fact that stands
// in for a writer (T5), so it can never pass and its line leaves the file only
// when a family registers the command that writes the key.
func writerMeasured(t *testing.T) map[string]writerLine {
	t.Helper()
	out := map[string]writerLine{}
	for _, row := range settings.Rows() {
		key := writerProbeKey(row.Key)
		w, ok := settings.WriterOf(key)
		switch {
		case !ok:
			out[key] = writerLineFor(writerOpenLine, writerOpenNoWriter)
		case w.Label != "":
			out[key] = writerLineFor(writerLabelledLine, string(w.Label))
		default:
			if err := writerProbeWrite(t, row, key, w); err != nil {
				out[key] = writerLineFor(writerOpenLine, writerOpenWriterDoesNotWrite)
			}
		}
	}
	return out
}

// TestWriterConformanceTheOwnerWritesTheKey runs every row that answers with a
// runnable writer and compares the measurement with the golden, strictly and in
// both directions (I1): a row the ratchet does not prove and the file does not
// list fails, a row the file lists whose line the measurement disagrees with
// fails, and a line whose row a running command now writes fails — that last one
// is the strictness I1 adds, and it is what makes the file shrink: a line is
// deleted by the change that makes it untrue rather than left to rot.
func TestWriterConformanceTheOwnerWritesTheKey(t *testing.T) {
	measured := writerMeasured(t)
	if *updateWriterGaps {
		writerGoldenWrite(t, measured)
		return
	}
	recorded := writerGolden(t)
	for key, line := range measured {
		got, ok := recorded[key]
		if !ok {
			t.Errorf("%s is not written by any command and %s does not list it: register the command that writes it in writers_table.go, label it in writers_labels.go (L1), or record the row's state in %s",
				key, writerGapsPath, writerGapsPath)
			continue
		}
		if got != line {
			t.Errorf("%s is recorded as %s and the ratchet measures it as %s: regenerate the file with -update-writers once the change that moved it has landed",
				key, got, line)
		}
	}
	for key, line := range recorded {
		if _, ok := measured[key]; ok {
			continue
		}
		t.Errorf("%s is recorded as %s and a command writes it today: delete its line — a golden line that passes fails this test (I1)",
			key, line)
	}
}

// TestWriterConformanceStrict audits the golden itself, and it is the half of
// the ratchet that runs no command: the file is a ledger of the two states the
// plan allows it to hold — a row the registry answers with a label, and a row
// the ratchet leaves open with a reason — and every line's claim about the
// registry is read here rather than trusted.
//
// Four properties, none of which needs a probe. The vocabulary: every verdict
// is `label:<one of the three>` or `open:<one of the two reasons>`, so a third
// spelling (the bare registry key the file was before I1) is a failure rather
// than a line no reader knows. The shape: one line per key, sorted, and every
// key a row of the registry still spells. The claim: a `label:` line names the
// label the registry answers that row with, an `open:no-writer` line's key
// answers nothing, and an `open:writer-does-not-write-the-key` line's key
// answers a runnable writer. And the ledger's completeness over the states no
// command decides: every labelled row and every row nothing answers for is in
// the file, and no row a runnable writer answers for is recorded as labelled.
//
// What stays out of this test is the one claim only the probe can settle — that
// a row absent from the file really is written by its command — which is
// TestWriterConformanceTheOwnerWritesTheKey's measurement.
func TestWriterConformanceStrict(t *testing.T) {
	entries := writerGoldenEntries(t)
	if len(entries) == 0 {
		t.Fatalf("%s lists nothing: a ledger that holds no row would pass this test on a tree where every row is open", writerGapsPath)
	}
	labelled := map[string]settings.Label{}
	unwritten, runnable := map[string]bool{}, map[string]bool{}
	for _, row := range settings.Rows() {
		key := writerProbeKey(row.Key)
		w, ok := settings.WriterOf(key)
		switch {
		case !ok:
			unwritten[key] = true
		case w.Label != "":
			labelled[key] = w.Label
		default:
			runnable[key] = true
		}
	}
	seen := map[string]bool{}
	for i, entry := range entries {
		if seen[entry.key] {
			t.Errorf("%s lists %s twice in %q", writerGapsPath, entry.key, entry.spelled)
		}
		if i > 0 && entries[i-1].key >= entry.key {
			t.Errorf("%s is not sorted: %s comes after %s", writerGapsPath, entry.key, entries[i-1].key)
		}
		seen[entry.key] = true
		if !writerGapIsARegistryKey(entry.key) {
			t.Errorf("%s lists %s, and no registry row spells it: a line for a row that is gone records a state no row can be in", writerGapsPath, entry.key)
		}
		switch entry.line.kind {
		case writerLabelledLine:
			label, ok := labelled[entry.key]
			if !ok {
				t.Errorf("%s records %s as %s and the registry answers the row with a runnable writer or with nothing: a labelled line stands for a row no command writes (D2611)",
					writerGapsPath, entry.key, entry.line)
				continue
			}
			if got := string(label); got != entry.line.value {
				t.Errorf("%s records %s as %s and the registry answers it with the %s label (writers_labels.go)",
					writerGapsPath, entry.key, entry.line, got)
			}
		case writerOpenLine:
			switch entry.line.value {
			case writerOpenNoWriter:
				if !unwritten[entry.key] {
					t.Errorf("%s records %s as %s and a writer or a label answers it in the registry: the reason names the state the row is in",
						writerGapsPath, entry.key, entry.line)
				}
			case writerOpenWriterDoesNotWrite:
				if !runnable[entry.key] {
					t.Errorf("%s records %s as %s and no runnable writer answers it: an open row's reason names the state it is open in",
						writerGapsPath, entry.key, entry.line)
				}
			}
		}
	}
	for key := range labelled {
		if !seen[key] {
			t.Errorf("%s is answered with the label %s and %s holds no line for it: the file is the ledger of every row the ratchet cannot prove, labels included",
				key, labelled[key], writerGapsPath)
		}
	}
	for key := range unwritten {
		if !seen[key] {
			t.Errorf("%s answers no writer and no label and %s holds no line for it: the file is the ledger of every row the ratchet cannot prove", key, writerGapsPath)
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

// writerGolden is the golden as a map from key to the state the file records
// for it.
func writerGolden(t *testing.T) map[string]writerLine {
	t.Helper()
	out := map[string]writerLine{}
	for _, entry := range writerGoldenEntries(t) {
		out[entry.key] = entry.line
	}
	return out
}

// writerGoldenEntries reads the golden's lines in the file's own order, which
// is what the shape checks read: a map would lose the order the sortedness is
// about, and the text as the file spells it, which a failure quotes back.
func writerGoldenEntries(t *testing.T) []writerGoldenEntry {
	t.Helper()
	var out []writerGoldenEntry
	for _, text := range writerGoldenLines(t) {
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, verdict, ok := strings.Cut(text, "\t")
		if !ok {
			t.Fatalf("%s holds the line %q, which is a key and nothing beside it: a line is <key>, a tab, and label:<label> or open:<reason> (I1)", writerGapsPath, text)
		}
		line, err := writerLineParse(verdict)
		if err != nil {
			t.Fatalf("%s holds the verdict %q for %s: %v", writerGapsPath, verdict, key, err)
		}
		out = append(out, writerGoldenEntry{key: key, line: line, spelled: text})
	}
	return out
}

// writerGoldenLines reads the golden, comments and blank lines included, so the
// shape checks can see them.
func writerGoldenLines(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(writerGapsPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with -update-writers)", writerGapsPath, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// writerGoldenHeader is the file's own comment block: what a line is, the two
// states it records, and the rule that keeps it honest. It is written by the
// regeneration rather than kept beside it, so the file a run rewrites is the
// file a reader reads.
const writerGoldenHeader = `# Every registry row the writer conformance test cannot prove a command writes,
# one per line and sorted by key: the key, a tab, and the state the row is in.
#
#   label:<plumbing|fake-endpoint|file>
#     the registry answers the row with a label (writers_labels.go, L1): no
#     command writes the key, the label's own line says why (D2611), and the row
#     is never run. Such a line leaves the file when a family registers a writer
#     for the row, which the strict ratchet then requires.
#   open:no-writer
#     no writer and no label answers the row: the family its Owner token names
#     has not registered the command that writes it (D2609, D2610).
#   open:writer-does-not-write-the-key
#     a runnable writer answers the row and the probe could not show its argv
#     changing the key in the store the writer named. This is a measurement of
#     the probe as much as of the writer: its own hermetic fixture and value
#     ladder are half of it (writerProbeWrite), so the line leaves the file when
#     either the writer or the probe changes.
#
# The ratchet is strict in both directions (I1): a row the measurement does not
# prove and this file does not list fails, and a line whose row a command writes
# today fails too. So the file only ever shrinks, and a line is deleted by the
# change that makes it untrue rather than left to rot.
#
# Generated by
#   go test ./internal/cli -run TestWriterConformanceTheOwnerWritesTheKey -update-writers
# and audited by TestWriterConformanceStrict, which reads every line's claim
# against the registry.
`

// writerGoldenWrite rewrites the golden from the measurement: every row the
// ratchet cannot prove a running command writes, sorted, with the state it is
// in beside it.
func writerGoldenWrite(t *testing.T, measured map[string]writerLine) {
	t.Helper()
	keys := make([]string, 0, len(measured))
	for key := range measured {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString(writerGoldenHeader)
	labelled, open := 0, 0
	for _, key := range keys {
		line := measured[key]
		fmt.Fprintf(&b, "%s\t%s\n", key, line)
		switch line.kind {
		case writerLabelledLine:
			labelled++
		case writerOpenLine:
			open++
		}
	}
	if err := os.WriteFile(writerGapsPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing %s: %v", writerGapsPath, err)
	}
	t.Logf("%s: %d lines (%d labelled, %d open)", writerGapsPath, len(keys), labelled, open)
}
