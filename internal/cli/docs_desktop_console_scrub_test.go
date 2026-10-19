package cli

// The scrub's vocabularies against the Go source (owner review of PR #643,
// item 1: "add a drift test comparing the scrub's vocabularies with the Go
// source's constants so they cannot rot silently").
//
// `console/tools/scrub-fixture/scrub.ts` copies a value only when it is in a
// fixed list, and refuses the rest -- deny by default, so the recorder cannot
// leak a field it does not know. The failure that review found is the other
// side of that rule: a list that is too SHORT does not leak, it makes the
// recorder unusable, because the ordinary run it was reading is refused. The
// lists were derived by hand from the Go source once and had already drifted
// (`bootstrap`, `review_first`, `finalize` and `vertex` were all missing, and
// `warn` was a mode no CLI writes).
//
// So the lists are derived here instead, from the source that writes them, and
// compared with the file: a stage the runner adds, an auth value the config
// gains, a mode, a kind, a status or an outcome fails this test until the
// scrub's list learns it. It reads the same way the rest of these console
// contract tests read: `readDoc`, no import of anything under console/, and no
// go.mod dependency to reach it.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

/** scrubTS is the file that holds the vocabularies. */
const scrubTS = "console/tools/scrub-fixture/scrub.ts"

// quoted returns every double-quoted Go string literal inside s, in order.
func quoted(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// tsQuoted is quoted for the other language: every single-quoted string literal
// inside s, in order.
func tsQuoted(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// collect returns the first capture of every match of re, in order. It says
// nothing about finding none: most files in a package do not carry the shape
// being looked for, and the caller asserts on the package's total.
func collect(src, re string) []string {
	var out []string
	for _, m := range regexp.MustCompile(re).FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

// sourceValues is collect for a scan that must find something: a pattern that
// silently matches nothing would compare two empty lists and pass.
func sourceValues(t *testing.T, where, src, re string) []string {
	t.Helper()
	out := collect(src, re)
	if len(out) == 0 {
		t.Fatalf("%s: the pattern %q matched nothing; the source has moved and this test is no longer reading it", where, re)
	}
	return out
}

// goFiles returns the non-test .go files of a package directory.
func goFiles(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob("../../" + dir + "/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range paths {
		if !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no source files under %s", dir)
	}
	return out
}

// tsList returns the single-quoted strings of one list in scrub.ts: `key: [...]`
// for a VOCAB entry, or `NAME = [...]` for an exported list.
func tsList(t *testing.T, scrub, pattern, what string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)` + pattern).FindStringSubmatch(scrub)
	if m == nil {
		t.Fatalf("%s: %s is not in the file in the form this test reads", scrubTS, what)
	}
	got := tsQuoted(m[1])
	slices.Sort(got)
	if len(got) == 0 {
		t.Fatalf("%s: %s parsed as an empty list", scrubTS, what)
	}
	return got
}

// sameSet fails with both lists when they differ.
func sameSet(t *testing.T, what string, want, got []string) {
	t.Helper()
	want, got = slices.Clone(want), slices.Clone(got)
	slices.Sort(want)
	slices.Sort(got)
	want, got = slices.Compact(want), slices.Compact(got)
	if !slices.Equal(want, got) {
		t.Errorf("%s: the Go source writes %v, the scrub knows %v", what, want, got)
	}
}

// TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource compares every list
// the scrub uses to copy a CLI value with the constants the Go source writes it
// from. A value on one side and not the other is a refusal waiting to happen
// (too short) or a pass-through (too long); both fail here.
func TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource(t *testing.T) {
	scrub := readDoc(t, scrubTS)

	// The stages. Every site the runner writes a stage at: the agent stages
	// (r.stage(ctx, "name", ...)), the record's own stage (r.rec.Stage = "x"),
	// the run's registry entry (Stage: "bootstrap"), and the stage name
	// provisioning records its own time under (const provisionStage = "provision").
	var stages []string
	for _, path := range goFiles(t, "internal/runner") {
		src := readDoc(t, strings.TrimPrefix(path, "../../"))
		stages = append(stages, collect(src, `\.stage\(ctx, "([a-z_]+)"`)...)
		stages = append(stages, collect(src, `\.rec\.Stage = "([a-z_]+)"`)...)
		stages = append(stages, collect(src, `Stage: "([a-z_]+)"`)...)
		stages = append(stages, collect(src, `const \w*Stage = "([a-z_]+)"`)...)
	}
	if len(stages) < 10 {
		t.Fatalf("internal/runner yielded %d stages (%v); the scans are no longer reading the source", len(stages), stages)
	}
	// A runs-bucket row with no registry entry yet carries watch's own "queued".
	stages = append(stages, sourceValues(t, "internal/watch/queued.go",
		readDoc(t, "internal/watch/queued.go"), `Stage: "([a-z_]+)"`)...)
	sameSet(t, "stage", stages, tsList(t, scrub, `\bstage: \[([^\]]*)\]`, "VOCAB.stage"))

	// The auth values: the config's allowed set, which is the one a run is
	// launched with, plus the dash the wall view writes for an empty one.
	auth := sourceValues(t, "internal/config/validate.go", readDoc(t, "internal/config/validate.go"),
		`\[\]string\{([^}]*)\}, c\.Agent\.Auth`)
	allowed := quoted(auth[0])
	if len(allowed) == 0 {
		t.Fatalf("internal/config/validate.go: the allowed-auth set parsed as empty")
	}
	sameSet(t, "auth", append(allowed, "-"), tsList(t, scrub, `\bauth: \[([^\]]*)\]`, "VOCAB.auth"))

	// The budget mode.
	sameSet(t, "mode", sourceValues(t, "internal/budget/model.go", readDoc(t, "internal/budget/model.go"),
		`Mode\w+ = "([a-z]+)"`), tsList(t, scrub, `\bmode: \[([^\]]*)\]`, "VOCAB.mode"))

	// The verify kinds: the three `fugaro verify` can run.
	sameSet(t, "verify kind", sourceValues(t, "internal/verify/verify.go", readDoc(t, "internal/verify/verify.go"),
		`Kind\w+\s+Kind = "([a-z]+)"`), tsList(t, scrub, `VERIFY_KINDS = \[([^\]]*)\]`, "VERIFY_KINDS"))

	// The connection states, the healths and the deadlines: three inline tables
	// in internal/watch/json.go, keyed by the constants they render.
	json := readDoc(t, "internal/watch/json.go")
	sameSet(t, "connection state", sourceValues(t, "internal/watch/json.go", json, `Conn\w+: "([a-z]+)"`),
		tsList(t, scrub, `\bconnectionState: \[([^\]]*)\]`, "VOCAB.connectionState"))
	sameSet(t, "health", sourceValues(t, "internal/watch/json.go", json, `Health\w+: "([a-z]+)"`),
		tsList(t, scrub, `\bhealth: \[([^\]]*)\]`, "VOCAB.health"))
	sameSet(t, "deadline", sourceValues(t, "internal/watch/json.go", json, `Deadline\w+: "([a-z]+)"`),
		tsList(t, scrub, `\bdeadline: \[([^\]]*)\]`, "VOCAB.deadline"))

	// The statuses and outcomes of a `finished` row. Both are runstore's own
	// constants; `running` is left out because a finished row is read from a
	// result.json, which only exists once the run has stopped (internal/watch/
	// finished.go), so no document ever carries it.
	sameSet(t, "status", without(sourceValues(t, "internal/runstore/runstore.go",
		readDoc(t, "internal/runstore/runstore.go"), `Status\w+\s+Status = "([a-z_]+)"`), "running"),
		tsList(t, scrub, `\bstatus: \[([^\]]*)\]`, "VOCAB.status"))
	outcomes := sourceValues(t, "internal/runstore/runstore.go", readDoc(t, "internal/runstore/runstore.go"),
		`Outcome\w+\s+Outcome = "([a-z]+)"`)
	outcomes = append(outcomes, sourceValues(t, "internal/runstore/artifact.go",
		readDoc(t, "internal/runstore/artifact.go"), `Outcome\w+\s+Outcome = "([a-z]+)"`)...)
	sameSet(t, "outcome", outcomes, tsList(t, scrub, `\boutcome: \[([^\]]*)\]`, "VOCAB.outcome"))

	// The verify line's spelling, which is not a list but three literals the
	// scrub may not paraphrase: `Record.Summary` writes the failure word in
	// upper case, opens with `(exit `, and appends the rerun flag to the kind.
	for _, want := range []string{"FAILED", "(exit ", "--rerun-failed"} {
		if !strings.Contains(scrub, fmt.Sprintf("'%s'", want)) && !strings.Contains(scrub, want) {
			t.Errorf("%s does not carry verify.go's %q: the verify shape would refuse the line the CLI writes", scrubTS, want)
		}
	}
}

// without returns values with every occurrence of drop removed.
func without(values []string, drop string) []string {
	var out []string
	for _, v := range values {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}
