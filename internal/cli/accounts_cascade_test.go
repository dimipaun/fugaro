package cli

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file is K1's own half: `fugaro accounts cascade set`, the command D2613
// names for the credentials cascade, and the leaves it writes into the carrier
// N1 added (docs/config-ownership/implementation-plan.md, K1; D2607, D2613,
// D2617). The two the chain replaced — `fallback` and `wait_threshold` — are no
// longer among them (MG4 of docs/model-credentials-routing/implementation-plan.md),
// and the tests below pin their refusal instead of their write.

// accountsCascadeHome is the installation these tests write into: the fixture
// A2's tests use, whose project config declares the credentials and the crafts
// a cascade is a statement about. A cascade is read back by the file's own
// loader, so a fixture this Fugaro could not read would measure the fixture
// rather than the write.
func accountsCascadeHome(t *testing.T) string {
	t.Helper()
	return accountsCraftHome(t, "")
}

// accountsCascadeLocal is the file a write landed in, as the loader reads it:
// what the cascade means, and not only what the bytes spell.
func accountsCascadeLocal(t *testing.T, path string) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("%s does not load after the write: %v", path, err)
	}
	return lc
}

// TestCascadeSetWritesTheCarrier is K1's red step: the cascade's leaves have a
// command at last, and the file it writes them into is this installation's own
// config under the name F23 gives the block — `credential_cascade`, because the
// local `credentials` key is the list of typed credentials and one key may not
// mean two things in two documents (D2607). The members nobody named are left
// exactly as they were, the credentials beside them are untouched, and the
// sentence says which member the write put where the project's node will carry
// it (D1540).
func TestCascadeSetWritesTheCarrier(t *testing.T) {
	path := accountsCascadeHome(t)
	before := readFileString(t, path)

	out, _, err := execute(t, "accounts", "cascade", "set",
		"--backoff-initial", "20m", "--backoff-cap", "1h")
	if err != nil {
		t.Fatalf("accounts cascade set: %v\n%s", err, out)
	}

	after := readFileString(t, path)
	blk := accountsCascadeLocal(t, path).CredentialCascade
	if blk == nil {
		t.Fatalf("the write left no credential_cascade block:\n%s", after)
	}
	// The two members the chain replaced are not written by any run, whatever it
	// passes: the chain says what a run does when its credential is exhausted
	// (R4), and this command no longer states it at all.
	if blk.Fallback != "" || blk.WaitThreshold != "" {
		t.Errorf("credential_cascade carries fallback %q and wait_threshold %q, and neither is this command's to write any more", blk.Fallback, blk.WaitThreshold)
	}
	if blk.Backoff == nil || blk.Backoff.Initial != "20m" || blk.Backoff.Cap != "1h" {
		t.Errorf("credential_cascade.backoff = %+v, want the two waits the command named", blk.Backoff)
	}
	if blk.StopAfterExhaustionPerWindow != nil || blk.ConcurrencyPerToken != nil {
		t.Errorf("credential_cascade carries the two D142 controls (%v, %v) that no flag named: a member left out is a member left alone",
			blk.StopAfterExhaustionPerWindow, blk.ConcurrencyPerToken)
	}

	// The write is the editor's: the bytes the file already held are the head of
	// what it holds now, so the credentials list beside the block, the craft and
	// every comment are where the owner put them.
	if !strings.HasPrefix(after, before) {
		t.Errorf("the write did not splice into the file, so the bytes the owner wrote did not survive:\n%s", after)
	}
	if !strings.Contains(after, "credential_cascade:\n  backoff:\n    initial: 20m\n    cap: 1h\n") {
		t.Errorf("the credential_cascade block does not carry the two members the command named:\n%s", after)
	}
	for _, want := range []string{
		"write the credentials cascade: backoff.initial = 20m, backoff.cap = 1h, which the project's node carries to every run",
		path,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the write does not say %q:\n%s", want, out)
		}
	}
}

// TestCascadeSetWritesOneMemberAtATime is the flag set's own rule: which members
// a run writes is read off the flags it was given and never off the values, so a
// second run changes its one member and leaves the first run's alone — and a run
// that asks for what the file already reads says so and writes nothing at all
// (yamledit's "no change", which is what keeps a repeated command from moving
// the file's mtime and its backups).
func TestCascadeSetWritesOneMemberAtATime(t *testing.T) {
	path := accountsCascadeHome(t)

	if _, _, err := execute(t, "accounts", "cascade", "set", "--backoff-cap", "1h"); err != nil {
		t.Fatalf("accounts cascade set --backoff-cap: %v", err)
	}
	out, _, err := execute(t, "accounts", "cascade", "set", "--concurrency-per-token", "2")
	if err != nil {
		t.Fatalf("accounts cascade set --concurrency-per-token: %v\n%s", err, out)
	}
	blk := accountsCascadeLocal(t, path).CredentialCascade
	if blk == nil || blk.Backoff == nil || blk.Backoff.Cap != "1h" {
		t.Errorf("credential_cascade = %+v, want the first run's cap kept by the second run", blk)
	}
	if blk.ConcurrencyPerToken == nil || *blk.ConcurrencyPerToken != 2 {
		t.Errorf("credential_cascade.concurrency_per_token = %v, want the 2 the second run named", blk.ConcurrencyPerToken)
	}
	if strings.Contains(out, "backoff.cap =") {
		t.Errorf("the second run's sentence names a member it did not write:\n%s", out)
	}

	before := readFileString(t, path)
	out, _, err = execute(t, "accounts", "cascade", "set", "--concurrency-per-token", "2")
	if err != nil {
		t.Fatalf("accounts cascade set with the value the file already reads: %v\n%s", err, out)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("a write of the value the file already reads changed the file:\n%s", got)
	}
	if !strings.Contains(out, "the cascade already reads concurrency_per_token = 2") {
		t.Errorf("the file was left alone and the command does not say why:\n%s", out)
	}
}

// TestCascadeSetWideningSaysWhatItWidens is D2617's other half at the owner's
// own terminal: the two directions that spend what the protective values hold
// back are written, and what the person reads before them is what they widen —
// a run spending past an exhausted window, and the runs holding one shared
// credential at once (D142). D142's third, the paid fallback, is the chain's to
// state now and this command refuses it (MG4), so it is not a case here. No
// gate is read for these: the
// owner's terminal is where they are written, and the command that refused them
// in a coding agent's session is the same line, run here.
//
// The lines the widening runs use are the ones a refusal prints word for word
// (`fugaro accounts cascade set --stop-after-exhaustion-per-window=false`), so a
// boolean flag spelled two words anywhere in this family would fail here rather
// than in the refusal a person reads (modelsWriterLeaf's own rule).
func TestCascadeSetWideningSaysWhatItWidens(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		want  string
		spent func(*localcfg.Config) bool
	}{
		{
			name: "stop false",
			args: []string{"accounts", "cascade", "set", "--stop-after-exhaustion-per-window=false"},
			want: "credential_cascade.stop_after_exhaustion_per_window false lets a run go on spending on a credential which has exhausted its window",
			spent: func(lc *localcfg.Config) bool {
				c := lc.CredentialCascade
				return c != nil && c.StopAfterExhaustionPerWindow != nil && !*c.StopAfterExhaustionPerWindow
			},
		},
		{
			name: "no cap",
			args: []string{"accounts", "cascade", "set", "--concurrency-per-token", "0"},
			want: "credential_cascade.concurrency_per_token 0 is no cap at all on how many runs may hold one shared credential",
			spent: func(lc *localcfg.Config) bool {
				c := lc.CredentialCascade
				return c != nil && c.ConcurrencyPerToken != nil && *c.ConcurrencyPerToken == 0
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsCascadeHome(t)

			out, _, err := execute(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v\n%s", tc.args, err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the widening write does not say what it widens (%q):\n%s", tc.want, out)
			}
			if !strings.Contains(out, "this widens the credentials cascade, which is the owner's own step") {
				t.Errorf("the widening write does not say whose step it is:\n%s", out)
			}
			if !tc.spent(accountsCascadeLocal(t, path)) {
				t.Errorf("the widening value did not land in the file:\n%s", readFileString(t, path))
			}
		})
	}
}

// TestCascadeFallbackIsRetired is the flag's own answer now (MG4, R4): whether a
// run waits for its credential's reset or falls over to the next source is the
// project's routing table's chain, so no value of this flag is written — not the
// two the old vocabulary knew and not a name outside it — and the refusal names
// the key and the command that owns the write.
func TestCascadeFallbackIsRetired(t *testing.T) {
	path := accountsCascadeHome(t)
	before := readFileString(t, path)

	for _, value := range []string{projectpolicy.FallbackOAuthOnly, projectpolicy.FallbackAuto, "spend-it"} {
		_, _, err := execute(t, "accounts", "cascade", "set", "--fallback", value)
		if err == nil {
			t.Fatalf("--fallback %s wrote a cascade", value)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
		}
		for _, want := range []string{"credential_cascade.fallback", "fugaro routing set", "fugaro accounts pool add", "nothing was written"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("--fallback %s: the refusal does not name %q: %v", value, want, err)
			}
		}
		if got := readFileString(t, path); got != before {
			t.Errorf("the refused write changed the file:\n%s", got)
		}
	}
}

// TestCascadeSetRefusesWhatTheLoaderRefuses is the read-back every settings
// write makes before it lands (spec §4.2 step 3): a duration the loader cannot
// parse and a backoff cap below its own first wait are refused in the loader's
// own sentence, so a file this Fugaro's very next command would refuse is never
// written. The cap rule is D153's, and the two members are compared as they
// would be used: a document writing only one of them is held to the other's
// default.
func TestCascadeSetRefusesWhatTheLoaderRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"not a duration", []string{"--backoff-initial", "banana"}, `"banana" is not a duration`},
		{"cap below the first wait", []string{"--backoff-initial", "5h", "--backoff-cap", "1h"}, "is below credential_cascade.backoff.initial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsCascadeHome(t)
			before := readFileString(t, path)

			_, _, err := execute(t, append([]string{"accounts", "cascade", "set"}, tc.args...)...)
			if err == nil {
				t.Fatalf("%v wrote a cascade the loader refuses", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not carry the loader's own sentence (%q): %v", tc.want, err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestCascadeSetSaysWhichFlagsItTakes is the call that names no member: the
// command writes nothing and answers with the members it writes, so a person who
// typed the command without a value is told what there is to write rather than
// left with a file the command read and did not change. The two the chain
// replaced are not offered, because every value of them is refused (MG4).
func TestCascadeSetSaysWhichFlagsItTakes(t *testing.T) {
	path := accountsCascadeHome(t)
	before := readFileString(t, path)

	_, _, err := execute(t, "accounts", "cascade", "set")
	if err == nil {
		t.Fatal("accounts cascade set with no member wrote something")
	}
	for _, leaf := range cascadeWriterLeaves {
		if accountsCascadeRetiredLeaf(leaf.leaf) {
			if strings.Contains(err.Error(), leaf.flagArg()) {
				t.Errorf("the refusal offers %s, and every value of it is refused: %v", leaf.flagArg(), err)
			}
			continue
		}
		if !strings.Contains(err.Error(), leaf.flagArg()) {
			t.Errorf("the refusal does not name %s: %v", leaf.flagArg(), err)
		}
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestCascadeOtherWideningRefusedInAnAgentSession is the same read for D142's
// two controls (D2617): turning the exhaustion stop off and taking the cap off
// a shared credential are the other two directions that spend what the
// protective values hold back, and neither is a coding agent's to write. The
// two are the lines a refusal prints, spelled as the flags are.
func TestCascadeOtherWideningRefusedInAnAgentSession(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				args    []string
				command string
			}{
				{"stop false", []string{"accounts", "cascade", "set", "--stop-after-exhaustion-per-window=false"},
					"fugaro accounts cascade set --stop-after-exhaustion-per-window=false"},
				{"no cap", []string{"accounts", "cascade", "set", "--concurrency-per-token", "0"},
					"fugaro accounts cascade set --concurrency-per-token 0"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					path := accountsCascadeHome(t)
					before := readFileString(t, path)
					t.Setenv(marker, "1")

					out, _, err := execute(t, tc.args...)
					assertAccessWriteRefused(t, err, out, marker, path, before, tc.command)
				})
			}
		})
	}
}

// TestCascadeSetEndsWithTheAfterWriteHook is the write's last step (contract §3,
// D2615): the hook is called once, after the bytes landed and never instead of
// them, with the key the write is about and the store it landed in — the
// cascade reaches a run off the published node and nowhere else, so this write
// owes the policy stage and the hook is what runs it. Its answer is the
// command's: a hook that fails fails the write, whose bytes stay saved.
func TestCascadeSetEndsWithTheAfterWriteHook(t *testing.T) {
	path := accountsCascadeHome(t)
	old := runAfterWrite
	t.Cleanup(func() { runAfterWrite = old })

	var got []afterWriteChange
	var landed bool
	runAfterWrite = func(_ context.Context, _ *cobra.Command, c afterWriteChange) error {
		got = append(got, c)
		landed = strings.Contains(readFileString(t, path), "cap: 1h")
		return nil
	}

	out, _, err := execute(t, "accounts", "cascade", "set", "--backoff-cap", "1h")
	if err != nil {
		t.Fatalf("accounts cascade set: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("the hook ran %d time(s), want once after the write: %+v", len(got), got)
	}
	if !landed {
		t.Error("the hook ran before the bytes landed, so the stage it owes was told about a write that had not happened")
	}
	if got[0].Key != "credential_cascade.backoff.cap" {
		t.Errorf("the hook was told about %q, want the first member this run wrote (credential_cascade.backoff.cap)", got[0].Key)
	}
	if got[0].Store != settings.StoreLocal {
		t.Errorf("the hook was told the write landed in %q, want %q: the cascade is declared in the installation's own config", got[0].Store, settings.StoreLocal)
	}

	got = nil
	for _, w := range []struct {
		name string
		args []string
	}{
		{"one member", []string{"accounts", "cascade", "set", "--backoff-initial", "45m"}},
		{"a later member", []string{"accounts", "cascade", "set", "--concurrency-per-token", "3"}},
	} {
		if _, _, err := execute(t, w.args...); err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
	}
	if len(got) != 2 || got[0].Key != "credential_cascade.backoff.initial" || got[1].Key != "credential_cascade.concurrency_per_token" {
		t.Errorf("the two later writes told the hook %+v, want each write's own member", got)
	}

	runAfterWrite = func(context.Context, *cobra.Command, afterWriteChange) error {
		return errors.New("the policy stage refused")
	}
	if _, _, err := execute(t, "accounts", "cascade", "set", "--backoff-initial", "50m"); err == nil {
		t.Error("a hook that failed was swallowed: the command answered success for a write whose stage never ran")
	}
}

// TestCascadeWritersRegisterTheirKeys is the registration half of item 5
// (W3/R1, D2613): every member is registered under both spellings — the carrier
// this machine's file calls the block, and the node's own `credentials.<leaf>`
// the read and every refusal print — with the command line that writes it, the
// store the bytes land in, and the node flag that says the published node
// serves the key besides.
func TestCascadeWritersRegisterTheirKeys(t *testing.T) {
	for _, leaf := range cascadeWriterLeaves {
		for _, spelling := range []string{"credential_cascade." + leaf.leaf, "credentials." + leaf.leaf} {
			w, ok := settings.WriterOf(spelling)
			if !ok {
				t.Errorf("%s: no writer answers, and the refusal a person reads is the writer's own line (D2613)", spelling)
				continue
			}
			if w.Command != "fugaro accounts cascade set" {
				t.Errorf("%s: Command = %q, want the command D2613 names", spelling, w.Command)
			}
			if w.Store != settings.StoreLocal {
				t.Errorf("%s: Store = %q, want %q: the carrier is this installation's own config", spelling, w.Store, settings.StoreLocal)
			}
			if !w.Node {
				t.Errorf("%s: Node is false, and the published node is what serves a cascade to a run (D2615)", spelling)
			}
			want := append([]string{"accounts", "cascade", "set"}, leaf.valueWords(settings.ValuePlaceholder)...)
			if got := w.ArgvFor(spelling); !slices.Equal(got, want) {
				t.Errorf("%s: argv = %v, want %v", spelling, got, want)
			}
			wantLine := "fugaro " + strings.Join(want, " ")
			if !strings.Contains(settingsRefusalNamesTheWriter(spelling, settings.Row{}, w), wantLine) {
				t.Errorf("%s: the refusal does not print the line the row registers (%q)", spelling, wantLine)
			}
		}
	}
}

// TestCascadeSetRefusesAForeignLayerNamingTheRight is the resolver's own refusal
// (T3, contract §6): the cascade is this installation's own statement of the
// project's policy, and a launcher who could write it into a shared file would
// be choosing everyone's spending — so the only layer it has is local, and
// `--layer` with another one refuses naming the layer it does have.
func TestCascadeSetRefusesAForeignLayerNamingTheRight(t *testing.T) {
	for _, layer := range []string{"project", "repo"} {
		t.Run(layer, func(t *testing.T) {
			path := accountsCascadeHome(t)
			before := readFileString(t, path)

			_, _, err := execute(t, "accounts", "cascade", "set", "--backoff-cap", "1h", "--layer", layer)
			if err == nil {
				t.Fatalf("--layer %s wrote a cascade, which has no such layer", layer)
			}
			for _, want := range []string{"--layer " + layer, "local"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestCascadeSetReadsNoStdin is the no-prompt policy's own pin (Fugaro asks for
// nothing): the write runs with stdin at EOF and returns — the members land, and
// nothing on the way waits for an answer a closed stdin could never give.
func TestCascadeSetReadsNoStdin(t *testing.T) {
	path := accountsCascadeHome(t)

	out, _, err := accountsPoolClosedStdin(t, "accounts", "cascade", "set", "--backoff-cap", "1h")
	if err != nil {
		t.Fatalf("accounts cascade set with stdin closed: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "    cap: 1h\n") {
		t.Errorf("the write did not land with stdin closed:\n%s", got)
	}
}
