package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/settings"
)

// secretsTestToday is the day every test below reads the calendar on. The
// command's clock is a variable (secretsSettingsNow) and not the wall clock, so
// a date is behind today or not because the test said so: no run of this file
// fails for having happened on the wrong day, and none of them depends on the
// order the tests ran in.
const secretsTestToday = "2026-11-16"

// secretsTestSentinel stands where a value would in the refusals below. It is a
// word and not anything shaped like a credential: what the tests pin is that
// this command prints no value it was given, and a fixture that looked like a
// token would be exactly the thing no test of this repository may carry.
const secretsTestSentinel = "sentinel-a-value-typed-into-the-wrong-flag"

// secretsClock pins the day --expires is read against.
func secretsClock(t *testing.T, day string) {
	t.Helper()
	when, err := time.Parse(localcfg.SecretExpiryLayout, day)
	if err != nil {
		t.Fatal(err)
	}
	old := secretsSettingsNow
	secretsSettingsNow = func() time.Time { return when.Add(12 * time.Hour) }
	t.Cleanup(func() { secretsSettingsNow = old })
}

// secretsLocalConfig is the local config this machine's project has after a run.
func secretsLocalConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("the local config does not read back: %v", err)
	}
	return lc
}

// secretsHookRecord replaces the after-write hook with a recorder and returns
// the changes it was handed: what each write told the hook, without running a
// stage (the stub is a no-op, and this is how the call itself is pinned before
// N3 lands).
func secretsHookRecord(t *testing.T) *[]afterWriteChange {
	t.Helper()
	var got []afterWriteChange
	old := runAfterWrite
	runAfterWrite = func(_ context.Context, _ *cobra.Command, change afterWriteChange) error {
		got = append(got, change)
		return nil
	}
	t.Cleanup(func() { runAfterWrite = old })
	return &got
}

// secretsUnreadableStdin fails every read, so a run that reads stdin fails. It
// is how "this command never reads a value here" is pinned by the read rather
// than by the absence of one.
type secretsUnreadableStdin struct{}

func (secretsUnreadableStdin) Read([]byte) (int, error) {
	return 0, errors.New("stdin was read")
}

func TestSecretsSetWritesExpiryAndRotation(t *testing.T) {
	settingsFixture(t, "")
	secretsClock(t, secretsTestToday)
	hooked := secretsHookRecord(t)

	out, errOut, err := execute(t, "secrets", "set", "bitbucket-token", "--expires", "2026-12-01", "--rotation-days", "30")
	if err != nil {
		t.Fatalf("stdout %q, stderr %q: %v", out, errOut, err)
	}
	lc := secretsLocalConfig(t)
	if got := lc.SecretExpires["bitbucket-token"]; got != "2026-12-01" {
		t.Fatalf("secret_expires.bitbucket-token = %q, want %q", got, "2026-12-01")
	}
	if lc.SecretRotationDays != 30 {
		t.Fatalf("secret_rotation_days = %d, want 30", lc.SecretRotationDays)
	}
	for _, key := range []string{"secret_expires", "secret_rotation_days"} {
		if !strings.Contains(out, key) {
			t.Fatalf("the write does not name %s: stdout %q", key, out)
		}
	}
	// One write per flag, each ending on the hook with the key it wrote and the
	// store it landed in — the store the registered writer names, read off the
	// registry rather than spelled at the call site (the hook's stage choice is
	// made from it, D2615).
	want := []afterWriteChange{
		{Key: "secret_expires", Store: settings.StoreLocal},
		{Key: "secret_rotation_days", Store: settings.StoreLocal},
	}
	if !slices.Equal(*hooked, want) {
		t.Fatalf("the write told the hook %v, want %v", *hooked, want)
	}
}

// TestSecretsSetRefusesAPastExpiry: a date behind today records an expiry that
// has already happened, which is a mistake and not a record, so it is refused
// with the fix, the file is left alone and no stage is owed.
func TestSecretsSetRefusesAPastExpiry(t *testing.T) {
	settingsFixture(t, "")
	secretsClock(t, secretsTestToday)
	hooked := secretsHookRecord(t)
	before, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = execute(t, "secrets", "set", "bitbucket-token", "--expires", "2026-11-15")
	if err == nil {
		t.Fatal("a date behind today was accepted")
	}
	for _, want := range []string{"has not passed", "behind today (" + secretsTestToday + ")", "rotate the secret at its provider"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	after, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused date changed the local config")
	}
	if len(*hooked) != 0 {
		t.Fatalf("a refused write told the hook %v", *hooked)
	}

	// A value that is not a date at all is refused the same way, and says which
	// spelling it wants.
	_, _, err = execute(t, "secrets", "set", "bitbucket-token", "--expires", "01/12/2026")
	if err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Fatalf("a malformed date is not refused naming the spelling it wants: %v", err)
	}
	// And today is a date that has not passed: the record is written.
	if _, _, err := execute(t, "secrets", "set", "bitbucket-token", "--expires", secretsTestToday); err != nil {
		t.Fatalf("a date of today was refused: %v", err)
	}
	if got := secretsLocalConfig(t).SecretExpires["bitbucket-token"]; got != secretsTestToday {
		t.Fatalf("secret_expires.bitbucket-token = %q, want %q", got, secretsTestToday)
	}
}

// TestSecretsSetRefusesANegativeRotation: a period cannot be behind today. The
// command's own refusal is what a person reads, not the loader's.
func TestSecretsSetRefusesANegativeRotation(t *testing.T) {
	settingsFixture(t, "")
	_, _, err := execute(t, "secrets", "set", "--rotation-days", "-1")
	if err == nil {
		t.Fatal("a negative rotation period was accepted")
	}
	if !strings.Contains(err.Error(), "negative one is not a period") {
		t.Fatalf("the refusal does not name what is wrong: %v", err)
	}
	// And it names a route that works for what the person was reaching for: the
	// generic settings path refuses this family's keys, so the remedy has to be
	// one of this command's own flags.
	if !strings.Contains(err.Error(), "fugaro secrets set --rotation-days 0") {
		t.Fatalf("the refusal names no route to record none: %v", err)
	}
	if lc := secretsLocalConfig(t); lc.SecretRotationDays != 0 {
		t.Fatalf("a refused period was written: %d", lc.SecretRotationDays)
	}
	// Zero is not negative: it is the registry row's own "no rotation period is
	// recorded", so it is written like any other period — and it is the way the
	// period is cleared once one is recorded.
	if _, _, err := execute(t, "secrets", "set", "--rotation-days", "30"); err != nil {
		t.Fatalf("a period was refused: %v", err)
	}
	if got := secretsLocalConfig(t).SecretRotationDays; got != 30 {
		t.Fatalf("secret_rotation_days = %d, want 30", got)
	}
	if _, _, err := execute(t, "secrets", "set", "--rotation-days", "0"); err != nil {
		t.Fatalf("a zero period was refused: %v", err)
	}
	if got := secretsLocalConfig(t).SecretRotationDays; got != 0 {
		t.Fatalf("secret_rotation_days = %d, want the row's own none", got)
	}
}

func TestSecretsBitbucketAPIRootsFlagWritesTheRow(t *testing.T) {
	settingsFixture(t, "")
	hooked := secretsHookRecord(t)

	// No NAME: the roots are the installation's own record, not one secret's,
	// which is what makes the registered argv (`secrets set
	// --bitbucket-api-root <value>`) the whole command line.
	const root = "https://bitbucket.example.com"
	out, errOut, err := execute(t, "secrets", "set", "--bitbucket-api-root", root)
	if err != nil {
		t.Fatalf("stdout %q, stderr %q: %v", out, errOut, err)
	}
	if got := secretsLocalConfig(t).BitbucketAPIRoots; !slices.Equal(got, []string{root}) {
		t.Fatalf("bitbucket_api_roots = %v, want [%s]", got, root)
	}
	if want := []afterWriteChange{{Key: "bitbucket_api_roots", Store: settings.StoreLocal}}; !slices.Equal(*hooked, want) {
		t.Fatalf("the write told the hook %v, want %v", *hooked, want)
	}

	// A second run adds rather than writes the key whole: the owner's list is
	// not this flag's to replace.
	second := "https://api.bitbucket.org/2.0"
	if _, _, err := execute(t, "secrets", "set", "--bitbucket-api-root", second); err != nil {
		t.Fatal(err)
	}
	if got := secretsLocalConfig(t).BitbucketAPIRoots; !slices.Equal(got, []string{root, second}) {
		t.Fatalf("bitbucket_api_roots = %v, want [%s %s]", got, root, second)
	}
	// A root the list already holds is no change, and a write that changed
	// nothing owes no stage.
	*hooked = nil
	if _, _, err := execute(t, "secrets", "set", "--bitbucket-api-root", root); err != nil {
		t.Fatal(err)
	}
	if got := secretsLocalConfig(t).BitbucketAPIRoots; !slices.Equal(got, []string{root, second}) {
		t.Fatalf("a repeated root changed the list: %v", got)
	}
	if len(*hooked) != 0 {
		t.Fatalf("a write that changed nothing told the hook %v", *hooked)
	}

	// A credential-shaped value is refused for carrying userinfo, and the
	// refusal does not print it: this flag takes a root and never a credential.
	_, errOut, err = execute(t, "secrets", "set", "--bitbucket-api-root", "https://"+secretsTestSentinel+"@api.example.com")
	if err == nil {
		t.Fatal("a root carrying userinfo was accepted")
	}
	if !strings.Contains(err.Error(), "carries userinfo") {
		t.Fatalf("the refusal does not name the userinfo: %v", err)
	}
	if strings.Contains(errOut+err.Error(), secretsTestSentinel) {
		t.Fatalf("the refusal echoed the value it refused: stderr %q, err %v", errOut, err)
	}
	if got := secretsLocalConfig(t).BitbucketAPIRoots; len(got) != 2 {
		t.Fatalf("a refused root was written: %v", got)
	}
}

func TestSecretsSetReadsNoStdin(t *testing.T) {
	settingsFixture(t, "")
	secretsClock(t, secretsTestToday)

	// A value write reads the value here. A record reads nothing: a run that
	// read this reader would fail, and one that wrote the record without reading
	// it is the whole of what the flag promises.
	out, errOut, err := executeStdinReader(t, secretsUnreadableStdin{}, "secrets", "set", "bitbucket-token", "--expires", "2026-12-01")
	if err != nil {
		t.Fatalf("stdout %q, stderr %q: %v", out, errOut, err)
	}
	if got := secretsLocalConfig(t).SecretExpires["bitbucket-token"]; got != "2026-12-01" {
		t.Fatalf("secret_expires.bitbucket-token = %q, want %q", got, "2026-12-01")
	}
	// The two flags that take no NAME read nothing either, and they are the ones
	// whose registered argv is a whole command line: a run of them that read a
	// value would be a prompt with nothing to show for it.
	if _, _, err := executeStdinReader(t, secretsUnreadableStdin{}, "secrets", "set", "--rotation-days", "7"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeStdinReader(t, secretsUnreadableStdin{}, "secrets", "set", "--bitbucket-api-root", "https://api.bitbucket.org"); err != nil {
		t.Fatal(err)
	}
	if lc := secretsLocalConfig(t); lc.SecretRotationDays != 7 || len(lc.BitbucketAPIRoots) != 1 {
		t.Fatalf("the records were not written: %d days, %v", lc.SecretRotationDays, lc.BitbucketAPIRoots)
	}
}

// TestSecretsSetRefusesALayerTheKeyLacks is T3 read through this family: the
// three records are local config keys, and a write aimed at another layer is
// refused with the layers the key does have (docs/contracts/config-ownership.md §6).
func TestSecretsSetRefusesALayerTheKeyLacks(t *testing.T) {
	settingsFixture(t, "")
	secretsClock(t, secretsTestToday)
	_, _, err := execute(t, "secrets", "set", "bitbucket-token", "--expires", "2026-12-01", "--layer", "project")
	if err == nil {
		t.Fatal("a record was written at a layer its key lacks")
	}
	if !strings.Contains(err.Error(), "may not be set at --layer project") || !strings.Contains(err.Error(), "local, shared") {
		t.Fatalf("the refusal does not name the layers the key has: %v", err)
	}
	// The layer the key does have is accepted, spelled or taken by default.
	if _, _, err := execute(t, "secrets", "set", "bitbucket-token", "--expires", "2026-12-01", "--layer", "local"); err != nil {
		t.Fatal(err)
	}
	// --layer on its own names no record: refusing is what keeps it from reading
	// as a flag that did something.
	_, _, err = execute(t, "secrets", "set", "bitbucket-token", "--layer", "local")
	if err == nil || !strings.Contains(err.Error(), "goes with the record flags (--expires, --rotation-days, --bitbucket-api-root, --remove-bitbucket-api-root)") {
		t.Fatalf("a bare --layer is not refused: %v", err)
	}
}

// TestSecretsWritersArgvWriteTheirKey runs what writers_secrets.go registers:
// the three rows' own templates, with the placeholders filled, against a
// hermetic config. It is the proof that each registration is a command this
// binary really runs and that the key really changes where the writer says.
func TestSecretsWritersArgvWriteTheirKey(t *testing.T) {
	settingsFixture(t, "")
	secretsClock(t, secretsTestToday)
	hooked := secretsHookRecord(t)

	cases := []struct{ key, name, value string }{
		{"secret_expires", "bitbucket-token", "2026-12-01"},
		{"secret_rotation_days", "", "30"},
		{"bitbucket_api_roots", "", "https://bitbucket.example.com"},
	}
	for _, c := range cases {
		w, ok := settings.WriterOf(c.key)
		if !ok {
			t.Fatalf("%s: no registered writer", c.key)
		}
		if w.Command != "fugaro secrets set" || w.Store != settings.StoreLocal {
			t.Fatalf("%s: registered as %q in %q", c.key, w.Command, w.Store)
		}
		args := w.ArgvFor(c.key)
		// `<value>` is the contract's word for where the value goes (CR1's
		// settings.ValuePlaceholder; written here as its own literal until that
		// revision merges) and ArgvFor leaves it as written, so the caller fills
		// it. `<name>` is this family's own word and is filled the same way.
		for i, word := range args {
			switch word {
			case secretsValuePlaceholder:
				args[i] = c.value
			case secretsNamePlaceholder:
				args[i] = c.name
			}
		}
		if _, errOut, err := execute(t, args...); err != nil {
			t.Fatalf("%v: stderr %q: %v", args, errOut, err)
		}
		*hooked = nil
	}
	lc := secretsLocalConfig(t)
	if lc.SecretExpires["bitbucket-token"] != "2026-12-01" || lc.SecretRotationDays != 30 ||
		!slices.Equal(lc.BitbucketAPIRoots, []string{"https://bitbucket.example.com"}) {
		t.Fatalf("the three registered argvs did not write their keys: %v, %d, %v", lc.SecretExpires, lc.SecretRotationDays, lc.BitbucketAPIRoots)
	}
}

// TestSecretsWritersNeverCarryAValueOnArgv: no run of this command carries a
// value — the value path reads stdin, the records take a date, a period and a
// URL root — so nothing it prints may echo one either, however a person reaches
// it. Each case below puts the sentinel where a value would land if the
// placeholder were ever a value, and every stream is read back for it.
func TestSecretsWritersNeverCarryAValueOnArgv(t *testing.T) {
	settingsFixture(t, "")
	secretsClock(t, secretsTestToday)

	cases := [][]string{
		{"secrets", "set", "bitbucket-token", "--expires", secretsTestSentinel},
		{"secrets", "set", "bitbucket-token", "--rotation-days", secretsTestSentinel},
		{"secrets", "set", "--bitbucket-api-root", secretsTestSentinel},
		{"secrets", "set", "--bitbucket-api-root", "https://" + secretsTestSentinel + "@api.example.com"},
		{"secrets", "set", "bitbucket-token", "--expires", "2026-12-01", "--repo", "acme/app"},
		{"secrets", "set", "bitbucket-token", secretsTestSentinel},
		{"secrets", "set", "bitbucket-token", "--not-a-flag", secretsTestSentinel},
	}
	for _, args := range cases {
		out, errOut, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v was accepted", args)
		}
		if strings.Contains(out+errOut, secretsTestSentinel) || strings.Contains(err.Error(), secretsTestSentinel) {
			t.Fatalf("%v printed the value it was given: stdout %q, stderr %q, err %v", args, out, errOut, err)
		}
	}

	// And the registered templates themselves state where a value goes, not a
	// value: a writer that carried one would make the conformance test (T1) run
	// a command line that stores it.
	for _, key := range []string{"secret_expires", "secret_rotation_days", "bitbucket_api_roots"} {
		w, ok := settings.WriterOf(key)
		if !ok {
			t.Fatalf("%s: no registered writer", key)
		}
		if !slices.Contains(w.Argv, secretsValuePlaceholder) {
			t.Fatalf("%s: the argv states no placeholder where the value goes: %v", key, w.Argv)
		}
		if got := w.ArgvFor(key); slices.Contains(got, secretsTestSentinel) {
			t.Fatalf("%s: ArgvFor produced a value: %v", key, got)
		}
	}
}

// secretsWriteDoc is the dry run's document as a consumer reads it: the members
// this test asserts are named here, so a renamed member is a failure here and
// not a test that follows the command.
type secretsWriteDoc struct {
	Key     string `json:"key"`
	Layer   string `json:"layer"`
	Changed bool   `json:"changed"`
	Old     string `json:"old"`
	New     string `json:"new"`
}

// secretsDryRun answers the document a --dry-run --json run printed.
func secretsDryRun(t *testing.T, args ...string) (secretsWriteDoc, string) {
	t.Helper()
	out, errOut, err := execute(t, append(append([]string{}, args...), "--dry-run", "--json")...)
	if err != nil {
		t.Fatalf("%v: stdout %q, stderr %q: %v", args, out, errOut, err)
	}
	var doc secretsWriteDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v printed no document: %v\n%s", args, err, out)
	}
	return doc, out
}

// TestSecretsSetDryRunAnswersTheRecord: the record path's dry run is the
// settings write path's own document (D1106), and the values it carries are the
// ones the file holds and would hold. `secret_expires.NAME: 2026-12-01` is one
// date to the loader and a timestamp to the YAML reader, and the document a
// console draws its confirmation from may not answer with that instant: the
// date the file spells is the value, old and new alike.
func TestSecretsSetDryRunAnswersTheRecord(t *testing.T) {
	settingsFixture(t, "bitbucket_api_roots: [\"https://api.bitbucket.org\"]\n")
	secretsClock(t, secretsTestToday)
	hooked := secretsHookRecord(t)
	path := os.Getenv("FUGARO_CONFIG")
	if _, _, err := execute(t, "secrets", "set", "bitbucket-token", "--expires", "2026-12-01"); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The write above is the one that owed a stage; what the hook is read for
	// from here is the dry runs, which owe none.
	*hooked = nil

	doc, out := secretsDryRun(t, "secrets", "set", "bitbucket-token", "--expires", "2026-12-02")
	if doc.Key != "secret_expires.bitbucket-token" || doc.Layer != settingsLayerLocal {
		t.Fatalf("key/layer = %q/%q, want the record's own key in the local config", doc.Key, doc.Layer)
	}
	if !doc.Changed || doc.Old != "2026-12-01" || doc.New != "2026-12-02" {
		t.Fatalf("changed/old/new = %t/%q/%q, want the two dates the file holds and the write would hold:\n%s", doc.Changed, doc.Old, doc.New, out)
	}
	for _, instant := range []string{"00:00:00", "UTC", "+0000"} {
		if strings.Contains(out, instant) {
			t.Fatalf("the document reports the date as an instant (%q):\n%s", instant, out)
		}
	}

	// The period and the list are answered the same way, and the removal's new
	// value is empty because the key goes with the last root.
	if doc, _ := secretsDryRun(t, "secrets", "set", "--rotation-days", "30"); doc.Key != "secret_rotation_days" || doc.Old != "" || doc.New != "30" {
		t.Fatalf("the period's document is %+v", doc)
	}
	if doc, _ := secretsDryRun(t, "secrets", "set", "--bitbucket-api-root", "https://bitbucket.example.com"); doc.Old != "https://api.bitbucket.org" || doc.New != "https://api.bitbucket.org,https://bitbucket.example.com" {
		t.Fatalf("the list's document is %+v", doc)
	}
	if doc, _ := secretsDryRun(t, "secrets", "set", "--remove-bitbucket-api-root", "https://api.bitbucket.org"); doc.Old != "https://api.bitbucket.org" || doc.New != "" {
		t.Fatalf("the removal's document is %+v", doc)
	}

	// A dry run wrote nothing and owes no stage, whichever record it answered.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, after) {
		t.Fatalf("a dry run wrote the local config:\nbefore:\n%s\nafter:\n%s", written, after)
	}
	if len(*hooked) != 0 {
		t.Fatalf("a dry run told the hook %v", *hooked)
	}
}

// TestSecretsSetRefusesARecordBesideAPolicyFlag: a record and a policy entry
// are two writes, and this command makes one per run. Neither flag set is the
// other's to drop: --allow-data-to beside --rotation-days would leave the
// repository list the person asked for silently unwritten, which is the one
// outcome a run may not report as success. The refusal names both halves and
// nothing is written.
func TestSecretsSetRefusesARecordBesideAPolicyFlag(t *testing.T) {
	settingsFixture(t, "")
	secretsClock(t, secretsTestToday)
	hooked := secretsHookRecord(t)
	before, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"secrets", "set", "bitbucket-token", "--rotation-days", "30", "--allow-data-to", "acme/other"},
		{"secrets", "set", "bitbucket-token", "--expires", "2026-12-01", "--proxy"},
		{"secrets", "set", "--bitbucket-api-root", "https://api.bitbucket.org", "--allow-data-to", "acme/other"},
		{"secrets", "set", "--remove-bitbucket-api-root", "https://api.bitbucket.org", "--proxy"},
	} {
		out, errOut, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v was accepted: stdout %q, stderr %q", args, out, errOut)
		}
		for _, want := range []string{"--allow-data-to and --proxy", "one write per run", "--expires, --rotation-days, --bitbucket-api-root, --remove-bitbucket-api-root"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%v: the refusal does not say %q:\n%v", args, want, err)
			}
		}
	}
	after, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("a refused run wrote the local config:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if len(*hooked) != 0 {
		t.Fatalf("a refused run told the hook %v", *hooked)
	}
	// One of the two, alone, is the write it says it is: the refusal is about
	// the combination and not about either half.
	if _, _, err := execute(t, "secrets", "set", "bitbucket-token", "--rotation-days", "30"); err != nil {
		t.Fatalf("the record alone was refused: %v", err)
	}
	if got := secretsLocalConfig(t).SecretRotationDays; got != 30 {
		t.Fatalf("secret_rotation_days = %d, want 30", got)
	}
}

// TestSecretsRemoveBitbucketAPIRootTakesItBackOut: the other direction of the
// list, which is the route the help names for taking a root away — the generic
// settings path refuses this family's keys (the row's owner is `secrets`,
// D853), so an add-only flag would leave an owner's own allow-list uneditable.
// It takes the last root's key with it, an empty list and an absent one being
// different records to the loader, and a root the list does not hold is left
// alone.
func TestSecretsRemoveBitbucketAPIRootTakesItBackOut(t *testing.T) {
	t.Run("a block list", func(t *testing.T) {
		settingsFixture(t, "")
		hooked := secretsHookRecord(t)
		const root = "https://api.bitbucket.org"
		if _, _, err := execute(t, "secrets", "set", "--bitbucket-api-root", root); err != nil {
			t.Fatal(err)
		}

		// A root the list does not hold changes nothing, and a write that
		// changed nothing owes no stage.
		*hooked = nil
		if _, _, err := execute(t, "secrets", "set", "--remove-bitbucket-api-root", "https://bitbucket.example.com"); err != nil {
			t.Fatal(err)
		}
		if got := secretsLocalConfig(t).BitbucketAPIRoots; !slices.Equal(got, []string{root}) {
			t.Fatalf("removing a root the list does not hold changed it: %v", got)
		}
		if len(*hooked) != 0 {
			t.Fatalf("a write that changed nothing told the hook %v", *hooked)
		}

		out, errOut, err := execute(t, "secrets", "set", "--remove-bitbucket-api-root", root)
		if err != nil {
			t.Fatalf("stdout %q, stderr %q: %v", out, errOut, err)
		}
		if !strings.Contains(out, "bitbucket_api_roots") {
			t.Fatalf("the write does not name the key it changed: %q", out)
		}
		if got := secretsLocalConfig(t).BitbucketAPIRoots; len(got) != 0 {
			t.Fatalf("bitbucket_api_roots = %v, want the last root gone", got)
		}
		if want := []afterWriteChange{{Key: "bitbucket_api_roots", Store: settings.StoreLocal}}; !slices.Equal(*hooked, want) {
			t.Fatalf("the write told the hook %v, want %v", *hooked, want)
		}
		// The key is gone rather than left empty: the loader tells the two
		// apart, and "no roots" is what the person asked for.
		data, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("bitbucket_api_roots")) {
			t.Fatalf("the key was left behind:\n%s", data)
		}
	})

	t.Run("a flow list", func(t *testing.T) {
		settingsFixture(t, "bitbucket_api_roots: [\"https://api.bitbucket.org\", \"https://bitbucket.example.com\"]\n")
		if _, _, err := execute(t, "secrets", "set", "--remove-bitbucket-api-root", "https://api.bitbucket.org"); err != nil {
			t.Fatal(err)
		}
		if got := secretsLocalConfig(t).BitbucketAPIRoots; !slices.Equal(got, []string{"https://bitbucket.example.com"}) {
			t.Fatalf("bitbucket_api_roots = %v, want the other root alone", got)
		}
	})
}

// TestSecretsBitbucketAPIRootsFlagReachesAFlowList: a flow-style list is a
// shape the loader accepts and this program's own fixtures write, and it is the
// one shape the editor's item operations refuse. The flag writes the list again
// in the block style this program writes one in — the roots the file already
// names, plus the one asked for — rather than refusing to reach the list the
// help names, which is what the family's policy lists already do.
func TestSecretsBitbucketAPIRootsFlagReachesAFlowList(t *testing.T) {
	settingsFixture(t, "bitbucket_api_roots: [\"https://api.bitbucket.org\"]\n")
	hooked := secretsHookRecord(t)

	out, errOut, err := execute(t, "secrets", "set", "--bitbucket-api-root", "https://bitbucket.example.com")
	if err != nil {
		t.Fatalf("stdout %q, stderr %q: %v", out, errOut, err)
	}
	want := []string{"https://api.bitbucket.org", "https://bitbucket.example.com"}
	if got := secretsLocalConfig(t).BitbucketAPIRoots; !slices.Equal(got, want) {
		t.Fatalf("bitbucket_api_roots = %v, want %v", got, want)
	}
	if wantHook := []afterWriteChange{{Key: "bitbucket_api_roots", Store: settings.StoreLocal}}; !slices.Equal(*hooked, wantHook) {
		t.Fatalf("the write told the hook %v, want %v", *hooked, wantHook)
	}
	// The reformatting is not silent: the block style is what the diff shows.
	data, err := os.ReadFile(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("- https://api.bitbucket.org")) || !bytes.Contains(data, []byte("- https://bitbucket.example.com")) {
		t.Fatalf("the list was not written in the block style this program writes:\n%s", data)
	}
	// A root the flow list already holds is still no change, and the second run
	// reaches the block list the first one wrote.
	*hooked = nil
	if _, _, err := execute(t, "secrets", "set", "--bitbucket-api-root", "https://api.bitbucket.org"); err != nil {
		t.Fatal(err)
	}
	if len(*hooked) != 0 {
		t.Fatalf("a repeated root told the hook %v", *hooked)
	}
}

// TestSecretsHelpNamesRoutesThatWork: the help's remedies for the three records
// are commands this binary really runs. The first review found the sentence
// pointing at `settings set bitbucket_api_roots --remove` and `settings unset
// secret_rotation_days`, and the generic path refuses both for keys this family
// owns (the row's owner is `secrets`, D853) — a help that names a refused
// command is how a person learns the CLI lies. Each route the help names is run
// here.
func TestSecretsHelpNamesRoutesThatWork(t *testing.T) {
	settingsFixture(t, "")
	secretsClock(t, secretsTestToday)
	help := newSecretsSetCmd().Long
	for _, dead := range []string{"settings set bitbucket_api_roots", "settings unset secret_rotation_days"} {
		if strings.Contains(help, dead) {
			t.Errorf("the help still names %q, which this family's own keys refuse", dead)
		}
	}
	for _, live := range []string{
		"fugaro secrets set --remove-bitbucket-api-root URL",
		"fugaro secrets set --rotation-days 0",
	} {
		if !strings.Contains(help, live) {
			t.Errorf("the help names no route for this: %q is not in it", live)
		}
	}

	// And both are routes that work: the root goes back out, and the period is
	// recorded as none.
	if _, _, err := execute(t, "secrets", "set", "--bitbucket-api-root", "https://api.bitbucket.org"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "secrets", "set", "--remove-bitbucket-api-root", "https://api.bitbucket.org"); err != nil {
		t.Fatalf("the route the help names for taking a root away: %v", err)
	}
	if got := secretsLocalConfig(t).BitbucketAPIRoots; len(got) != 0 {
		t.Fatalf("the named route left %v", got)
	}
	if _, _, err := execute(t, "secrets", "set", "--rotation-days", "30"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "secrets", "set", "--rotation-days", "0"); err != nil {
		t.Fatalf("the route the help names for clearing the period: %v", err)
	}
	if got := secretsLocalConfig(t).SecretRotationDays; got != 0 {
		t.Fatalf("secret_rotation_days = %d, want the row's own none", got)
	}
}

// TestSecretsAPIRootRefusalHoldsTheNodesRule keeps this command's own sentences
// in step with the rule the node is held to (projectpolicy.
// ValidBitbucketAPIRoots): the two are one rule written twice — the node's
// quotes the value and this one may not — so a value the two disagree about is
// the failure, whichever way it disagrees.
func TestSecretsAPIRootRefusalHoldsTheNodesRule(t *testing.T) {
	values := []string{
		"https://api.bitbucket.org",
		"https://bitbucket.example.com/2.0",
		"https://bitbucket.example.com/",
		"http://api.bitbucket.org",
		"api.bitbucket.org",
		"https://",
		"https://user:pw@api.bitbucket.org",
		"https://api.bitbucket.org?x=1",
		"https://api.bitbucket.org#frag",
		"not a url",
		"",
	}
	for _, value := range values {
		mine := secretsAPIRootProblem(value) == ""
		nodes := projectpolicy.ValidBitbucketAPIRoots([]string{value}) == nil
		if mine != nodes {
			t.Fatalf("%q: this command accepts it: %v, the node's rule does: %v", value, mine, nodes)
		}
	}
}
