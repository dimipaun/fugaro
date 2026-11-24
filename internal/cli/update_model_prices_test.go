package cli

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// cacheKeyFixture is an installation whose local config writes its cache
// prices under the old key names — the multiplication the old keys have always
// meant — and whose cache read is priced below what the built-in catalog prices
// the model's cheapest endpoint at: the model the route claims is one the
// catalog prices, and 0.0042 of a 0.14 input is $0.000588 per million tokens
// against the catalog's $0.002 floor.
//
// The two write dimensions carry values of their own rather than the list
// defaults, so a rename that kept the number a default would have put there
// anyway still reads as one: the catalog prices no cache_write endpoint tier of
// this model, so neither of them is an undercut and the run has exactly one
// warning to print.
const cacheKeyFixture = `
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models:
      - deepseek/*
model_prices:
  deepseek/deepseek-v4.1-flash:
    input_per_m: 0.14
    output_per_m: 0.42
    cache_read: 0.0042
    cache_write_5m: 1.4
    cache_write_1h: 2.5
`

// commentFixture is the same entry in a file a person has annotated: a note
// above the entry, one above the read, one on the read's own line, and one at
// the end of the file. Which of them the rename keeps is
// TestUpdateRenameKeepsTheCommentsItDoesNotRewrite's question.
const commentFixture = `
version: 2
name: aurora
runs_bucket: fugaro-runs-proj-1234
region: us-east5
gcp_project: proj-1234
# the note above the providers
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models:
      - deepseek/*
model_prices:
  # the note above the entry
  deepseek/deepseek-v4.1-flash:
    input_per_m: 0.14
    output_per_m: 0.42
    # the note above the read
    cache_read: 0.0042 # the note on the read's own line
    cache_write_5m: 1.4
    cache_write_1h: 2.5
# the note at the end of the file
`

// TestUpdateMigratesCacheReadKey is SA4.2's red step, and the state every
// installation whose owner priced a cache price by hand is in: the old key
// names still mean what they always meant (SA4.1 folds them), so the file has
// to be rewritten in place under the names this release documents — the
// owner's own file, through yamledit, with the values kept and nothing asked.
//
// Both halves are asserted here: the file, byte for byte, and the line the
// command prints about a price below the catalog's floor, which is the other
// thing R4 owes the one command the owner runs.
func TestUpdateMigratesCacheReadKey(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	r.appendConfig(t, cacheKeyFixture)
	// The keys go in before the copy the door reads is taken: the rig clears
	// FUGARO_CONFIG, so the local config `fugaro update` reads and writes is the
	// one this rig left in the XDG projects directory.
	registerInstallation(t, r.initRig)
	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	written := string(readFile(t, installationConfigPath()))
	for _, want := range []string{
		"cache_read_mult: 0.0042",
		"cache_write_5m_mult: 1.4",
		"cache_write_1h_mult: 2.5",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("after fugaro update the owner's config holds no %q:\n%s", want, written)
		}
	}
	for _, old := range []string{"cache_read:", "cache_write_5m:", "cache_write_1h:"} {
		if strings.Contains(written, old) {
			t.Errorf("after fugaro update the owner's config still writes %s, so the key this release documents is not the one the file holds:\n%s", old, written)
		}
	}
	// The prices themselves are the owner's and are not touched: a migration
	// that renamed the key and moved the number would be a price change nobody
	// asked for. The other keys of the entry say the same, and the map is what
	// every reader of the file resolves (localcfg folds the new names into the
	// same multiplier the old ones were).
	lc := parseLocalConfigBytes(t, []byte(written))
	mp := lc.ModelPrices["deepseek/deepseek-v4.1-flash"]
	for _, c := range []struct {
		what string
		got  *float64
		want float64
	}{
		{"cache_read", mp.CacheRead, 0.0042},
		{"cache_write_5m", mp.CacheWrite5m, 1.4},
		{"cache_write_1h", mp.CacheWrite1h, 2.5},
		{"input_per_m", mp.InputPerM, 0.14},
		{"output_per_m", mp.OutputPerM, 0.42},
	} {
		if c.got == nil || *c.got != c.want {
			t.Errorf("after the migration model_prices.deepseek/deepseek-v4.1-flash.%s = %v, want the %v the owner priced", c.what, c.got, c.want)
		}
	}
	// The one price of this entry below the catalog's floor is named, with both
	// figures and the day the catalog was checked: the owner reads what their
	// price is against, and nothing is refused.
	if n := strings.Count(out, "the override undercuts the catalog"); n != 1 {
		t.Errorf("fugaro update printed %d undercut warning(s), want the one the cache read is:\n%s", n, out)
	}
	if want := "Warning: model_prices.deepseek/deepseek-v4.1-flash.cache_read_per_m is $0.000588 per million tokens and the cheapest endpoint the built-in price catalog prices for it on the zdr tier is $0.002 per million tokens"; !strings.Contains(out, want) {
		t.Errorf("fugaro update did not print %q:\n%s", want, out)
	}
}

// TestUpdateRenameKeepsTheCommentsItDoesNotRewrite pins the bytes the rename
// leaves, which is the claim the run's own line makes: SetScalar writes the new
// key and RemoveKey takes the old one, so a note written on one of the renamed
// keys — above it or on its line — goes with the key, while every comment
// written anywhere else in the file stays where it was.
//
// It is asserted on the rename itself rather than on a whole `fugaro update`:
// update writes the local config again from the struct later in its own run, so
// what a comment's life over a whole run is, is that writer's question and not
// this stage's (see writeLocalConfig).
func TestUpdateRenameKeepsTheCommentsItDoesNotRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aurora.yaml")
	if err := os.WriteFile(path, []byte(commentFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse([]byte(commentFixture))
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuf{}
	r := &initRun{o: &initOptions{}, w: out}
	wrote, refused, err := renameModelPriceKeys(r, lc, path)
	if err != nil || refused {
		t.Fatalf("the rename was refused over a file of comments (refused=%v, err=%v), want the splice", refused, err)
	}
	if len(wrote) != 3 {
		t.Fatalf("the rename wrote %d key(s), want the three the file holds: %v", len(wrote), wrote)
	}
	written := string(readFile(t, path))
	for _, kept := range []string{
		"# the note above the providers",
		"# the note above the entry",
		"# the note at the end of the file",
	} {
		if !strings.Contains(written, kept) {
			t.Errorf("the rename dropped %q, a note written away from the keys it renamed:\n%s", kept, written)
		}
	}
	for _, gone := range []string{
		"# the note above the read",
		"# the note on the read's own line",
	} {
		if strings.Contains(written, gone) {
			t.Errorf("the file still holds %q, so a note written on a renamed key outlived the key: the run's line says it goes with it, and this is where that sentence is kept true:\n%s", gone, written)
		}
	}
	// The line the run prints is the other half of the same claim: an owner
	// whose note went reads that it went, and the file it replaced is named.
	if detail := out.String(); !strings.Contains(detail, "a comment written on one of the renamed keys went with it") {
		t.Errorf("the run's line does not say the notes on the renamed keys went with them:\n%s", detail)
	}
}

// TestModelPricesStageOnAFirstRunHasNothingToRename is the state a machine with
// no local config is in, which is every first run: loadInitConfig hands the
// stage the path the installation stage will write the file to, with no bytes
// read from it, and the loop runs Check on every stage BEFORE any stage applies
// — so the file this stage is pointed at does not exist when it reads.
//
// A file that is not there is the empty case and not a failure: nothing can be
// written the old way in a file that holds nothing, and a stage that failed here
// would end `fugaro setup` before its first apply. The stage is driven on its
// own, as the other one-stage tests drive theirs.
func TestModelPricesStageOnAFirstRunHasNothingToRename(t *testing.T) {
	isolateProjects(t, t.TempDir())
	t.Chdir(t.TempDir()) // outside any checkout
	o := &initOptions{name: "aurora", cloud: cloudOptions{gcpProject: "proj-1234", region: "us-east5", stderr: discard}}
	lc, path, old, err := loadInitConfig(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := localcfg.ProjectPath(os.Getenv, "aurora"); path != want || old != nil {
		t.Fatalf("a first run holds %s (old %q), want %s with no bytes read", path, old, want)
	}
	cmd := NewRootCmd()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetErr(&syncBuf{})
	run := &initRun{o: o, w: &syncBuf{}, in: bufio.NewReader(strings.NewReader("")), cmd: cmd}
	run.setProject(lc)
	e := &initEngine{r: run, lc: lc, path: path, old: old}

	res, err := initflow.Run(t.Context(), []initflow.Stage{newModelPricesStage(e)}, e.options())
	if err != nil {
		t.Fatalf("the loop failed a first run over a config file that is not there yet: %v", err)
	}
	if res.Failed != nil || res.ExitCode() != 0 {
		t.Fatalf("a first run ended %+v (failed %+v), want the stage skipped and the run going on", res.Stages, res.Failed)
	}
	if got := stateOf(res, initflow.ModelPrices); got != initflow.Skipped {
		t.Errorf("the stage is %s on a first run, want it skipped: %+v", got, res.Stages)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the stage left a file at %s (stat: %v), want it to write nothing", path, err)
	}
}

// TestUpdateMigrationIdempotent: the config `fugaro update` leaves behind is one
// the rename has nothing left to do to. The keys it wrote are the keys it would
// write, so the line about them is printed once in an installation's life and no
// run that follows finds anything to change.
//
// The door is not run twice here, for the reason TestUpdateIsIdempotent's own
// comment gives (the rig's first run onboards the repository, and the second
// run's repository stage stops over exactly that). What idempotence means for
// this migration is a property of the file the run produced, and the stage's own
// read of it is what the assertion asks: the plan of a second run over these
// bytes renames nothing.
func TestUpdateMigrationIdempotent(t *testing.T) {
	r := newConvergeRig(t, currentNode())
	r.appendConfig(t, cacheKeyFixture)
	registerInstallation(t, r.initRig)
	convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	path := installationConfigPath()
	second, err := modelPricesPlanFor(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.renames) != 0 {
		t.Errorf("a second pass over the config fugaro update wrote renames %s again, so the file is not one the rename has finished with",
			modelPriceRenamesText(second.renames))
	}
	// The prices are the owner's, before and after, and the file this binary
	// would write back out of them keeps the name the run just wrote: the two
	// spellings agree, which is what the run's own later writes to this file —
	// and the local-config write of a stage behind this one — read.
	lc := parseLocalConfigBytes(t, readFile(t, path))
	mp := lc.ModelPrices["deepseek/deepseek-v4.1-flash"]
	for _, c := range []struct {
		what string
		got  *float64
		want float64
	}{
		{"cache_read", mp.CacheRead, 0.0042},
		{"cache_write_5m", mp.CacheWrite5m, 1.4},
		{"cache_write_1h", mp.CacheWrite1h, 2.5},
	} {
		if c.got == nil || *c.got != c.want {
			t.Errorf("a second read of the file gives model_prices.deepseek/deepseek-v4.1-flash.%s = %v, want the %v the owner priced", c.what, c.got, c.want)
		}
	}
	out, err := lc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"cache_read:", "cache_write_5m:", "cache_write_1h:"} {
		if strings.Contains(string(out), old) {
			t.Errorf("the config read back from the file fugaro update wrote would be written out with %s again:\n%s", old, out)
		}
	}
}
