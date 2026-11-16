package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/settings"
)

// accountsPoolOwnedPool is the fixture the owner flags are written into: a pool
// with a strategy and a comment of its own, so a test reads whether the new
// fields joined the pool's own lines and whether the edit went through
// internal/yamledit rather than a decode-and-re-encode.
const accountsPoolOwnedPool = `
pools:
  - name: claude
    strategy: failover
    members:
      - oauth-one
      - key-one
`

// accountsPoolLegacyPool is the migration's own shape: a pool that predates
// owners, marked open (D1942), which no owner may be written onto.
const accountsPoolLegacyPool = `
pools:
  - name: legacy
    legacy_open: true
    members:
      - oauth-one
`

// accountsPoolOwnerHome is the fixture the owner tests write into: the pool
// above with the credentials it lists, in a config home of its own.
func accountsPoolOwnerHome(t *testing.T) string {
	t.Helper()
	return accountsPoolHomeWith(t, accountsPoolCredentials+accountsPoolCrafts+accountsPoolOwnedPool)
}

// accountsPoolClosedStdin runs one command line with stdin at EOF — the write
// end of a pipe closed before the run — which is the closed stdin the no-prompt
// policy names (Fugaro asks for nothing).
func accountsPoolClosedStdin(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	defer r.Close()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(r)
	cmd.SetArgs(NormalizeArgs(args))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestPoolAddWritesOwnerAndAllowedUsers is A1's own red step: a pool is a
// spending and data-egress grant, so it is declared with the people it is
// served to — the owner a craft's access is checked against and the allowed
// users beside them (D1628, D1936). The write joins the pool's own lines, and
// the fields the flags did not name — the strategy and the members — are left
// exactly as the owner wrote them.
func TestPoolAddWritesOwnerAndAllowedUsers(t *testing.T) {
	path := accountsPoolOwnerHome(t)

	out, _, err := execute(t, "accounts", "pool", "add", "claude",
		"--owner", "owner@example.com",
		"--allow-user", "dev@example.com",
		"--allow-user", "ops@example.com")
	if err != nil {
		t.Fatalf("accounts pool add --owner --allow-user: %v\n%s", err, out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the config this command wrote does not read back: %v", err)
	}
	pool, ok := accountsPoolOf(lc, "claude")
	if !ok {
		t.Fatalf("pools.claude is not in the config this command wrote: %+v", lc.Pools)
	}
	if pool.Owner != "owner@example.com" {
		t.Errorf("pools.claude.owner = %q, want owner@example.com", pool.Owner)
	}
	if got := strings.Join(pool.AllowedUsers, ", "); got != "dev@example.com, ops@example.com" {
		t.Errorf("pools.claude.allowed_users = %q, want the two addresses in the order given", got)
	}
	if got := strings.Join(pool.Members, ", "); got != "oauth-one, key-one" {
		t.Errorf("pools.claude.members = %q, want the members the pool already had", got)
	}
	if got := pool.StrategyOrDefault(); got != "failover" {
		t.Errorf("pools.claude.strategy = %q, want the strategy the pool already had", got)
	}
	// The bytes are spliced, not re-encoded: the pool's own line is still the
	// one the owner wrote and the two new fields hang under it.
	block := accountsPoolBlock(t, path)
	for _, want := range []string{
		"  - name: claude\n    strategy: failover\n    members:\n      - oauth-one\n      - key-one\n",
		"    owner: owner@example.com\n",
		"    allowed_users:\n      - dev@example.com\n      - ops@example.com\n",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the pools block does not carry %q:\n%s", want, block)
		}
	}
	// The sentence names what the write does, the people included (D1540).
	for _, want := range []string{"owner owner@example.com", "allowed users dev@example.com, ops@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("the sentence does not say %q:\n%s", want, out)
		}
	}
}

// TestPoolAddWritesTheOwnerOfANewPool: the flags are the pool's declaration
// whatever else it carries, so a pool that does not exist yet is created with
// them in the one write.
func TestPoolAddWritesTheOwnerOfANewPool(t *testing.T) {
	path := accountsPoolOwnerHome(t)

	out, _, err := execute(t, "accounts", "pool", "add", "fresh", "oauth-one",
		"--owner", "owner@example.com", "--allow-user", "dev@example.com")
	if err != nil {
		t.Fatalf("accounts pool add for a new pool: %v\n%s", err, out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the config this command wrote does not read back: %v", err)
	}
	pool, ok := accountsPoolOf(lc, "fresh")
	if !ok {
		t.Fatalf("the new pool was not written: %+v", lc.Pools)
	}
	if pool.Owner != "owner@example.com" || len(pool.AllowedUsers) != 1 {
		t.Errorf("pools.fresh = %+v, want the owner and the allowed user the flags named", pool)
	}
	if _, ok := accountsPoolOf(lc, "claude"); !ok {
		t.Errorf("the write replaced the block instead of adding to it: %+v", lc.Pools)
	}
}

// TestPoolAddRefusesWhatTheOwnerBlockRefuses pins the rules the write inherits
// from config.ValidatePools for the two new fields — an address that is not an
// email, an address listed twice, and an owner written onto a pool the
// migration marked open, which is a pool that has no owner by definition
// (D1936, D1942). Each refusal is the block's own sentence, and each leaves the
// file byte-identical.
func TestPoolAddRefusesWhatTheOwnerBlockRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		local      string
		args       []string
	}{
		{
			"an owner that is not an email", "must be an email address",
			accountsPoolCredentials + accountsPoolOwnedPool,
			[]string{"claude", "--owner", "not-an-address"},
		},
		{
			"an allowed user that is not an email", "must be an email address",
			accountsPoolCredentials + accountsPoolOwnedPool,
			[]string{"claude", "--allow-user", "not-an-address"},
		},
		{
			"an address listed twice", "this address is listed twice; a pool lists each allowed user once",
			accountsPoolCredentials + accountsPoolOwnedPool,
			[]string{"claude", "--allow-user", "dev@example.com", "--allow-user", "dev@example.com"},
		},
		{
			"an owner on a pool the migration marked open", "a pool marked legacy_open has no owner",
			accountsPoolCredentials + accountsPoolLegacyPool,
			[]string{"legacy", "--owner", "owner@example.com"},
		},
		{
			"an allowed user on a pool the migration marked open", "a pool marked legacy_open names no allowed users",
			accountsPoolCredentials + accountsPoolLegacyPool,
			[]string{"legacy", "--allow-user", "dev@example.com"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := accountsPoolHomeWith(t, tc.local)
			before := readFileString(t, path)

			args := append([]string{"accounts", "pool", "add"}, tc.args...)
			out, _, err := execute(t, args...)
			if err == nil {
				t.Fatalf("the write was not refused:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is not the block's own sentence %q: %v", tc.want, err)
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
		})
	}
}

// TestPoolAddTakesNoLegacyOpenFlag is D2608's answer, pinned: the marker says a
// pool predates owners and is served to everyone the installation has, so it is
// the migration's to write and never a declaration. A creation flag for it
// would be a way to declare a pool with no owner and no boundary at all.
func TestPoolAddTakesNoLegacyOpenFlag(t *testing.T) {
	path := accountsPoolOwnerHome(t)
	before := readFileString(t, path)

	out, _, err := execute(t, "accounts", "pool", "add", "claude", "--legacy-open")
	if err == nil {
		t.Fatalf("--legacy-open was accepted as a creation flag:\n%s", out)
	}
	if !strings.Contains(err.Error(), "unknown flag: --legacy-open") {
		t.Errorf("the refusal does not name the flag there is not: %v", err)
	}
	if got := readFileString(t, path); got != before {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestPoolAddUsesTheResolverAndTheGate is T3's half of the write path (F4): a
// pool leaf is resolved to a layer by the shared resolver and edited through
// the shared fence, so `--layer local` names the same file the default resolves
// to and the write lands in that file and no other. The layer document's own
// gate has nothing to say about a local document — it is the layer file's rules
// — and is asked on this path exactly as `settings set` asks it.
func TestPoolAddUsesTheResolverAndTheGate(t *testing.T) {
	path := accountsPoolOwnerHome(t)

	// No --layer: the resolver's default for a pool leaf, which is local.
	if _, _, err := execute(t, "accounts", "pool", "add", "claude", "--owner", "owner@example.com"); err != nil {
		t.Fatalf("accounts pool add with no --layer: %v", err)
	}
	// --layer local names the file the default resolved to, and the write lands
	// there rather than anywhere the flags did not name.
	out, _, err := execute(t, "accounts", "pool", "add", "claude", "--allow-user", "dev@example.com", "--layer", "local")
	if err != nil {
		t.Fatalf("accounts pool add --layer local: %v\n%s", err, out)
	}
	if !strings.Contains(out, path) {
		t.Errorf("the write does not name the file the resolver resolved (%s):\n%s", path, out)
	}
	lc, err := localcfg.Parse([]byte(readFileString(t, path)))
	if err != nil {
		t.Fatalf("the config this command wrote does not read back: %v", err)
	}
	pool, ok := accountsPoolOf(lc, "claude")
	if !ok {
		t.Fatalf("pools.claude is not in the config this command wrote: %+v", lc.Pools)
	}
	if pool.Owner != "owner@example.com" || len(pool.AllowedUsers) != 1 || pool.AllowedUsers[0] != "dev@example.com" {
		t.Errorf("pools.claude = %+v, want both writes on the one local pool", pool)
	}
}

// TestPoolAddRefusesAForeignLayerNamingTheRight is the resolver's own refusal
// (T3, contract §6): a layer the key does not have refuses naming the layers it
// does have, so a person who typed --layer project is told where a pool is
// declared rather than left with a file that quietly did not change. Both of
// the layers a pool does not have are held to it, on the declaration and on the
// removal alike.
func TestPoolAddRefusesAForeignLayerNamingTheRight(t *testing.T) {
	for _, layer := range []string{"project", "repo"} {
		t.Run(layer, func(t *testing.T) {
			path := accountsPoolOwnerHome(t)
			before := readFileString(t, path)

			_, _, err := execute(t, "accounts", "pool", "add", "claude", "--owner", "owner@example.com", "--layer", layer)
			if err == nil {
				t.Fatalf("--layer %s wrote a pool, which has no such layer", layer)
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
	t.Run("rm", func(t *testing.T) {
		path := accountsPoolOwnerHome(t)
		before := readFileString(t, path)

		_, _, err := execute(t, "accounts", "pool", "rm", "claude", "--layer", "project")
		if err == nil {
			t.Fatal("--layer project removed a pool, which has no such layer")
		}
		for _, want := range []string{"--layer project", "local"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if got := readFileString(t, path); got != before {
			t.Errorf("the refused write changed the file:\n%s", got)
		}
	})
}

// TestPoolAddEndsWithTheAfterWriteHook is the write's last step (contract §3,
// D2615): the hook is called once, after the bytes landed and never instead of
// them, with the key the write is about and the store it landed in — the
// registry row's own layers are what choose the stages, so the key has to be
// one the registry answers for. Its answer is the command's: a hook that fails
// fails the write.
func TestPoolAddEndsWithTheAfterWriteHook(t *testing.T) {
	path := accountsPoolOwnerHome(t)
	old := runAfterWrite
	t.Cleanup(func() { runAfterWrite = old })

	var got []afterWriteChange
	var landed bool
	runAfterWrite = func(_ context.Context, _ *cobra.Command, c afterWriteChange) error {
		got = append(got, c)
		landed = strings.Contains(readFileString(t, path), "owner: owner@example.com")
		return nil
	}

	out, _, err := execute(t, "accounts", "pool", "add", "claude", "--owner", "owner@example.com")
	if err != nil {
		t.Fatalf("accounts pool add: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("the hook ran %d time(s), want once after the write: %+v", len(got), got)
	}
	if !landed {
		t.Error("the hook ran before the bytes landed, so the stages it owes were told about a write that had not happened")
	}
	if got[0].Key != "pools.claude.members" {
		t.Errorf("the hook was told about %q, want the key this write is about (pools.claude.members)", got[0].Key)
	}
	if got[0].Store != settings.StoreLocal {
		t.Errorf("the hook was told the write landed in %q, want %q: a pool is declared in the installation's own config", got[0].Store, settings.StoreLocal)
	}

	got = nil
	if _, _, err := execute(t, "accounts", "pool", "rm", "claude"); err != nil {
		t.Fatalf("accounts pool rm: %v", err)
	}
	if len(got) != 1 || got[0].Key != "pools.claude.name" {
		t.Errorf("the removal told the hook %+v, want one change to pools.claude.name", got)
	}

	runAfterWrite = func(context.Context, *cobra.Command, afterWriteChange) error {
		return errors.New("the stage refused")
	}
	if _, _, err := execute(t, "accounts", "pool", "add", "claude", "oauth-one", "--owner", "owner@example.com"); err == nil {
		t.Error("a hook that failed was swallowed: the command answered success for a write whose stages never ran")
	}
}

// TestPoolAddReadsNoStdin is the no-prompt policy's own pin (Fugaro asks for
// nothing): every leaf of the two pool writes runs with stdin at EOF and
// returns — the write lands, and nothing on the way waits for an answer that a
// closed stdin could never give.
func TestPoolAddReadsNoStdin(t *testing.T) {
	path := accountsPoolOwnerHome(t)

	out, _, err := accountsPoolClosedStdin(t, "accounts", "pool", "add", "claude", "oauth-one", "--owner", "owner@example.com")
	if err != nil {
		t.Fatalf("accounts pool add with stdin closed: %v\n%s", err, out)
	}
	if !strings.Contains(readFileString(t, path), "owner: owner@example.com") {
		t.Errorf("the write did not land with stdin closed:\n%s", readFileString(t, path))
	}

	out, _, err = accountsPoolClosedStdin(t, "accounts", "pool", "rm", "claude")
	if err != nil {
		t.Fatalf("accounts pool rm with stdin closed: %v\n%s", err, out)
	}
	if strings.Contains(readFileString(t, path), "name: claude") {
		t.Errorf("the removal did not land with stdin closed:\n%s", readFileString(t, path))
	}
}

// TestPoolWriteRefusedInAnAgentSessionUnderAnAdmittingGate is D2617's answer to
// F22, and it is the one fence the pool writes carry that no gate can lift: a
// pool decides where a project's calls may be spent and whose key they may
// carry, so a coding agent's session writes neither the declaration nor the
// removal. The edit here is handed an admitting gate — the strongest answer the
// managed gate can give, which lifts applySettingsEdit's own refusal — and the
// write is refused all the same, with the diff, the marker and the command to
// run at a terminal.
func TestPoolWriteRefusedInAnAgentSessionUnderAnAdmittingGate(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			path := accountsPoolOwnerHome(t)
			before := readFileString(t, path)
			t.Setenv(marker, "1")

			_, lc, err := settingsSelection()
			if err != nil {
				t.Fatal(err)
			}
			next, err := accountsPoolAddEdit([]byte(before), "claude", []string{"oauth-one", "key-one"},
				accountsPoolWriteOptions{owner: "owner@example.com"})
			if err != nil {
				t.Fatal(err)
			}
			cmd := newAccountsPoolAddCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader(""))
			err = accountsPoolWrite(cmd, lc, settingsEdit{
				Path:    path,
				Old:     []byte(before),
				New:     next,
				What:    "declare pools.claude.members = [oauth-one, key-one], in this order, owner owner@example.com",
				Kind:    settingsDocLocal,
				Command: "fugaro accounts pool add claude oauth-one key-one --owner owner@example.com",
				// The gate admits this edit: the pools' fence is the one that
				// refuses it, and it refuses whatever the gate says.
				Managed: initflow.Managed{Managed: true, Admitted: true},
			}, "pools.claude.members")
			if err == nil {
				t.Fatalf("the write landed in a coding agent's session under an admitting gate:\n%s", out.String())
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "fugaro accounts pool add claude oauth-one key-one --owner owner@example.com") {
				t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
			}
			if !strings.Contains(out.String(), path) || !(strings.Contains(out.String(), "  + ") || strings.Contains(out.String(), "  - ")) {
				t.Errorf("the refusal does not print the diff of what it refused:\n%s", out.String())
			}
			if got := readFileString(t, path); got != before {
				t.Errorf("the file changed in a coding agent's session:\n%s", got)
			}
		})
	}
}

// accountsPoolHasNodeRemovals reports whether the local config carries the list
// a removal is recorded in (N1's carrier). It is asked of the type rather than
// of a constant, so the test below starts running the moment the field lands.
func accountsPoolHasNodeRemovals() bool {
	t := reflect.TypeOf(localcfg.Config{})
	for i := range t.NumField() {
		if strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0] == "node_removals" {
			return true
		}
	}
	return false
}

// TestPoolRmWritesTheExplicitUnset is F22's storage half: a pool the config no
// longer declares has to be withdrawn from the node as well, and the node is
// reconciled from what the config says — so the removal leaves the name in the
// list the reconcile reads, and not merely an absence the reconcile cannot tell
// from a config it has never seen.
//
// It needs N1, which owns the carrier: the local config has no node_removals
// list yet, and this branch may not add one — the field, the shared-file
// exclusion and the registry row are three other tasks' files. The skip is
// detected rather than unconditional, so the test runs by itself once N1 lands.
func TestPoolRmWritesTheExplicitUnset(t *testing.T) {
	if !accountsPoolHasNodeRemovals() {
		t.Skip("needs N1: the local config carries no node_removals list yet, so a removal has nowhere to be recorded and the node keeps serving the pool (F22)")
	}
	path := accountsPoolOwnerHome(t)

	if _, _, err := execute(t, "accounts", "pool", "rm", "claude"); err != nil {
		t.Fatalf("accounts pool rm: %v", err)
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "node_removals:") || !strings.Contains(after, "claude") {
		t.Errorf("the removal left no explicit unset for the reconcile to carry to the node:\n%s", after)
	}
}

// TestPoolArgvFillsTheSegmentPlaceholder is the registration's own half: a
// template writes `<1>` where the pool's name goes, because the pool is a list
// item and the name is the item's key rather than a word the command line
// repeats, and ArgvFor fills it. It is written against the contract the
// registry is registered under, and it starts running when CR1 lands: on C1
// ArgvFor fills the whole key and leaves `<1>` as written.
func TestPoolArgvFillsTheSegmentPlaceholder(t *testing.T) {
	for _, tc := range []struct {
		key, want string
	}{
		{"pools.team.name", "accounts pool add team vertex"},
		{"pools.team.strategy", "accounts pool add team --strategy <value>"},
		{"pools.team.members", "accounts pool add team <value>"},
		{"pools.team.owner", "accounts pool add team --owner <value>"},
		{"pools.team.allowed_users", "accounts pool add team --allow-user <value>"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			w, ok := settings.WriterOf(tc.key)
			if !ok {
				t.Fatalf("%s: no registered writer", tc.key)
			}
			// The template keeps `<1>` on both bases; whether ArgvFor fills it
			// is the question, and it is asked of the filled argv rather than
			// of the registration.
			filled := w.ArgvFor(tc.key)
			for _, word := range filled {
				if word == "<1>" {
					t.Skip("needs CR1 (#2842): per-segment argv placeholders")
				}
			}
			if got := strings.Join(filled, " "); got != tc.want {
				t.Errorf("WriterOf(%s).ArgvFor = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// TestPoolWritersAreRegisteredAndDisjoint is the registration's other half: the
// leaves `accounts pool add` writes are registered under the pool's own
// patterns, one pattern per leaf, with the argv that changes exactly that leaf
// and the store it lands in — and no pool pattern collides with another family's
// or with a sibling leaf's, which is what RegisterWriter refuses.
func TestPoolWritersAreRegisteredAndDisjoint(t *testing.T) {
	for _, key := range []string{
		"pools.team.name",
		"pools.team.strategy",
		"pools.team.members",
		"pools.team.owner",
		"pools.team.allowed_users",
	} {
		w, ok := settings.WriterOf(key)
		if !ok {
			t.Errorf("%s: no registered writer, so a refusal for it can print no command", key)
			continue
		}
		if w.Command != "fugaro accounts pool add" {
			t.Errorf("%s: Command = %q, want the family that writes it", key, w.Command)
		}
		if w.Store != settings.StoreLocal {
			t.Errorf("%s: Store = %q, want %q", key, w.Store, settings.StoreLocal)
		}
		if len(w.Argv) == 0 {
			t.Errorf("%s: no argv, so the conformance test has nothing to run", key)
		}
	}
	// A leaf no command of this release writes is not registered: the migration
	// is legacy_open's only writer (D2608), and a registered argv that changed
	// some other leaf would be a writer that lies.
	if w, ok := settings.WriterOf("pools.team.legacy_open"); ok {
		t.Errorf("pools.team.legacy_open is registered (%+v), and no command writes it", w)
	}
}

// TestPoolLeafRowsSayLocalOnly: the row every pool leaf resolves through is the
// registry's own, and it says the local config is the one layer a pool has — the
// fact the resolver and the refusal above are read off.
func TestPoolLeafRowsSayLocalOnly(t *testing.T) {
	for _, leaf := range []string{"name", "strategy", "members", "owner", "allowed_users"} {
		key := "pools.team." + leaf
		row, ok := settings.Lookup("pools.*." + leaf)
		if !ok {
			t.Errorf("pools.*.%s: no registry row", leaf)
			continue
		}
		if len(row.Layers) != 1 || row.Layers[0] != settings.LayerLocal {
			t.Errorf("pools.*.%s: Layers = %v, want the local config alone", leaf, row.Layers)
		}
		// The command looks the row up at the concrete key it writes, and the
		// row it gets back is the one the layer comes from.
		got, ok := accountsPoolRow(key)
		if !ok || got.Key != key || got.Layers[0] != settings.LayerLocal {
			t.Errorf("accountsPoolRow(%s) = %+v (found %v), want the pool leaf's own row under the concrete key", key, got, ok)
		}
	}
}
