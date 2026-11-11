// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	report "github.com/dimipaun/fugaro/ee/report"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestMain closes the ways a test could reach the developer's own machine or
// Google for real (no application default credentials, no metadata server, no
// egress) and keeps the caches a command writes out of the developer's home, as
// ee/report/cli's and internal/cli's TestMains do: this package drives the same
// command tree, and without it a read would go to the ambient credentials and a
// real runs bucket.
func TestMain(m *testing.M) {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/fugaro-tests-have-no-credentials.json")
	os.Setenv("GCE_METADATA_HOST", "127.0.0.1:1")
	os.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	os.Setenv("https_proxy", "http://127.0.0.1:1")
	os.Unsetenv("STORAGE_EMULATOR_HOST")
	for _, k := range []string{"XDG_CACHE_HOME", "XDG_CONFIG_HOME"} {
		if dir, err := os.MkdirTemp("", "fugaro-test-ee-ab-"); err == nil {
			os.Setenv(k, dir)
		}
	}
	// git stops before it reaches the repository root, so a command that
	// resolves a checkout resolves the fixture's project and never this
	// repository's own fugaro.yaml.
	if wd, err := os.Getwd(); err == nil {
		os.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(filepath.Dir(wd)))
	}
	os.Exit(m.Run())
}

// jsonAB runs `ab --json` with args and decodes the document into the type the
// command itself prints, so a field renamed on the wire cannot pass: what is
// read here is the contract, not a copy of it.
func (f *abFixture) jsonAB(t *testing.T, args ...string) report.ABJSONDoc {
	t.Helper()
	out, errOut, err := executeAB(t, append([]string{"ab", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("%v\nstderr: %s", err, errOut)
	}
	var doc report.ABJSONDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v in %s", err, out)
	}
	return doc
}

// row returns the document's row for one mode, so a test names the group it is
// about rather than a position in the table.
func abRow(t *testing.T, doc report.ABJSONDoc, mode string) report.ABJSONRow {
	t.Helper()
	for _, r := range doc.Rows {
		if r.Mode == mode {
			return r
		}
	}
	t.Fatalf("the document holds no %s row: %+v", mode, doc.Rows)
	return report.ABJSONRow{}
}

// warning returns the one warning a document carries that contains want, and
// fails the test when it carries none: a check that never ran is not a check.
func abWarning(t *testing.T, doc report.ABJSONDoc, want string) string {
	t.Helper()
	for _, w := range doc.Warnings {
		if strings.Contains(w, want) {
			return w
		}
	}
	t.Fatalf("the report carries no warning about %q: %v", want, doc.Warnings)
	return ""
}

// TestABCommandReadsTheWindow is the command's own job: read the window's run
// records, score the pairs in them, and print the rows the report built. It
// also pins that the window is a window — a pair minted outside it is in no
// row, or --since would be decoration.
func TestABCommandReadsTheWindow(t *testing.T) {
	f := newABFixture(t)
	f.seedPair(t, "pair-one", "20261001-120000-aaaa", "pro", "small", "model-alpha", "model-beta")

	doc := f.jsonAB(t, "--since", "0")
	if doc.Project != "aurora" || doc.Source != "run-records" {
		t.Errorf("the document says project %q source %q, want the fixture's project and run-records", doc.Project, doc.Source)
	}
	if len(doc.Rows) != 1 {
		t.Fatalf("the document holds %d rows, want 1: %+v", len(doc.Rows), doc.Rows)
	}
	row := abRow(t, doc, "pro")
	if row.Tier != "small" || row.Champion != "model-alpha" || row.Challenger != "model-beta" {
		t.Errorf("the row reads %s/%s %s vs %s, want small/model-alpha vs model-beta", row.Mode, row.Tier, row.Champion, row.Challenger)
	}
	// Both arms are the same task at the same commit, so the pair is valid and
	// decided on the signals: identical records are a tie, and nothing in the
	// report may claim otherwise.
	if row.Pairs != 1 || row.Ties != 1 || row.Wins != 0 || row.Losses != 0 {
		t.Errorf("the row counts %d pairs (%d wins, %d losses, %d ties), want one tie", row.Pairs, row.Wins, row.Losses, row.Ties)
	}
	if row.Recommendation.Decision != "no-conclusion" {
		t.Errorf("one tied pair reads %q, want no-conclusion: the sample cannot say", row.Recommendation.Decision)
	}
	if row.Recommendation.MedianCostRatio == nil || *row.Recommendation.MedianCostRatio != 0.5 {
		t.Errorf("the row's median cost ratio is %v, want 0.5: the challenger cost half", row.Recommendation.MedianCostRatio)
	}

	// The same read without --since is the 30d default, and a pair minted
	// outside it is in no row.
	old := f.jsonAB(t)
	if len(old.Rows) != 0 {
		t.Errorf("a pair minted in October is in the %d-day default window: %+v", 30, old.Rows)
	}

	// A pair minted inside the default window is read without any flag, and
	// --repo names the repository whose pair it is.
	recent := time.Now().UTC().Add(-time.Hour).Format("20060102-150405")
	f.seedPair(t, "pair-two", recent+"-dddd", "pro", "small", "model-alpha", "model-beta")
	if got := f.jsonAB(t, "--repo", "acme/app"); len(got.Rows) != 1 || abRow(t, got, "pro").Pairs != 1 {
		t.Errorf("--repo acme/app reads %+v, want the one recent pair", got.Rows)
	}

	// The table prints the same row, and every column a reader decides with.
	out, errOut, err := executeAB(t, "ab", "--repo", "acme/app")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{"model-alpha", "model-beta", "small", "ADVICE", "no conclusion", "COST", "0.50x"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table lacks %q:\n%s", want, out)
		}
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("the table carries an escape sequence:\n%q", out)
	}
}

// TestABCommandSaysWhatItCouldNotRead: a record the bucket holds and this read
// cannot use takes its pair out of every figure, and a window wider than the
// read's cap is read to the cap. Both are said rather than left for a reader to
// notice in a count that does not add up.
func TestABCommandSaysWhatItCouldNotRead(t *testing.T) {
	f := newABFixture(t)
	f.seedPair(t, "pair-one", "20261001-120000-aaaa", "pro", "small", "model-alpha", "model-beta")
	f.seedCorrupt(t, abAppSlug, "20261002-120000-bbbb")

	doc := f.jsonAB(t, "--since", "0")
	note := abWarning(t, doc, "cannot read")
	if !strings.Contains(note, "1 of the window's runs") {
		t.Errorf("the corruption warning reads %q, want it to count the one unreadable record", note)
	}
	if len(doc.Rows) != 1 || abRow(t, doc, "pro").Pairs != 1 {
		t.Errorf("an unreadable record of another run changed the pairs: %+v", doc.Rows)
	}

	// The cap: three runs, a read of two, and a warning that says so.
	withABMaxRuns(t, 2)
	f.seedPair(t, "pair-two", "20261003-120000-eeee", "pro", "small", "model-gamma", "model-delta")
	doc = f.jsonAB(t, "--since", "0")
	note = abWarning(t, doc, "more runs than the 2")
	if !strings.Contains(note, "only its newest 2") {
		t.Errorf("the truncation warning reads %q, want it to say only the newest 2 are counted", note)
	}
}

// TestABCommandRatesComeFromTheCachedLayer: the advice is read at the mode's
// exchange rate (D565), which is the project's own when this machine has read
// the project layer and Fugaro's shipped table when it has not — and the report
// says which, because the two can give different advice.
func TestABCommandRatesComeFromTheCachedLayer(t *testing.T) {
	f := newABFixture(t)
	f.seedPair(t, "pair-one", "20261001-120000-aaaa", "startup", "small", "model-alpha", "model-beta")

	doc := f.jsonAB(t, "--since", "0")
	if rate := abRow(t, doc, "startup").Recommendation.ExchangeRate; rate != 0.25 {
		t.Errorf("with no layer cached the Startup rate reads %v, want the shipped 0.25", rate)
	}
	note := abWarning(t, doc, "no project layer is cached")
	// The advice names a command that reads the layer the bucket holds and
	// caches it. `fugaro models ls` reads that cache alone and would leave a
	// reader exactly where this warning found them.
	if !strings.Contains(note, "fugaro config layer") {
		t.Errorf("the no-cache warning reads %q, want it to name `fugaro config layer`, which fills the cache", note)
	}

	// The project's own layer: Startup's allowance is raised, so a pair that
	// costs a quarter more is inside it rather than outside.
	seedLayer(t, "aurora", "ab:\n  exchange_rate:\n    startup: 1.5\n")
	doc = f.jsonAB(t, "--since", "0")
	if rate := abRow(t, doc, "startup").Recommendation.ExchangeRate; rate != 1.5 {
		t.Errorf("with the layer cached the Startup rate reads %v, want the project's 1.5", rate)
	}
	for _, w := range doc.Warnings {
		if strings.Contains(w, "no project layer is cached") || strings.Contains(w, "could not be read") {
			t.Errorf("a report read at the project's own rates warned %q", w)
		}
	}

	// A layer this build cannot read is the shipped table and a line saying so
	// rather than a silent fallback.
	seedLayer(t, "aurora", "ab: {exchange_rate: {startup: not-a-number}}\n")
	doc = f.jsonAB(t, "--since", "0")
	if rate := abRow(t, doc, "startup").Recommendation.ExchangeRate; rate != 0.25 {
		t.Errorf("with an unreadable layer the Startup rate reads %v, want the shipped 0.25", rate)
	}
	abWarning(t, doc, "could not be read")
}

// seedLayer writes the project layer cache the last cloud read would have left
// on this machine.
func seedLayer(t *testing.T, project, yaml string) {
	t.Helper()
	entry := localcfg.SharedCacheEntry{
		GCPProject: "proj-1234",
		Bucket:     "aurora-layers",
		CheckedAt:  time.Now().UTC(),
		YAML:       "version: 1\nproject: " + project + "\ngcp_project: proj-1234\n" + yaml,
	}
	if err := localcfg.SaveLayerCache(os.Getenv, project, entry); err != nil {
		t.Fatal(err)
	}
}

// TestABCommandRefusesABadWindow: the window is parsed before anything is
// selected or opened, so a typo costs no cloud call — and the refusal is this
// command's own, with nothing on stdout.
func TestABCommandRefusesABadWindow(t *testing.T) {
	newABFixture(t)
	out, _, err := executeAB(t, "ab", "--since", "yesterday")
	if err == nil {
		t.Fatal("ab --since yesterday was accepted")
	}
	if !strings.Contains(err.Error(), `--since "yesterday"`) {
		t.Errorf("ab --since yesterday = %v, want the command's own --since refusal", err)
	}
	if out != "" {
		t.Errorf("the refused ab wrote %q to stdout, want nothing", out)
	}
}
