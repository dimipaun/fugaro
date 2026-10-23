package localcfg

import (
	"strings"
	"testing"
)

// The mailbox block, which is T2's whole footprint in this package (mobile
// P4.1, plan §12 and §13): one field, the keychain item's name, validated, and
// its layered merge. The key itself is never here (M5, D113b) — the config
// holds the item's name, and the name is not a secret.

// TestParseRefusesAKeychainItemNameOfTheWrongShape is M5's fixed shape, one
// row per way an operator can write it wrong. The name becomes an argv element
// of `security` or `secret-tool`, and the project's own name must be in it, so
// a config file cannot name an arbitrary keychain query nor read another
// project's key. The rule is keychain.ParseItem's; this file is the one place
// it is applied to a config, and the refusal names the key the operator wrote.
func TestParseRefusesAKeychainItemNameOfTheWrongShape(t *testing.T) {
	block := func(item string) string { return "mailbox:\n  keychain_item: " + item + "\n" }
	cases := []struct {
		name string
		item string
		want string
	}{
		{name: "empty", item: `""`, want: "the item name is empty"},
		{name: "no keychain_item at all", item: "", want: "the item name is empty"},
		{name: "longer than 64 characters", item: strings.Repeat("a", 65), want: "longer than 64"},
		{name: "a space", item: `"fugaro aurora"`, want: "is not a name"},
		{name: "a path separator", item: `"fugaro/aurora"`, want: "is not a name"},
		{name: "a quote", item: `"fugaro'aurora"`, want: "is not a name"},
		{name: "a command substitution", item: `"fugaro$(id)aurora"`, want: "is not a name"},
		{name: "a leading dash", item: `"-aurora"`, want: "begins with '-'"},
		{name: "another project's name", item: `"fugaro-mailbox-borealis"`, want: "does not contain the project's name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			extra := ""
			if c.item != "" {
				extra = block(c.item)
			} else {
				extra = "mailbox: {}\n"
			}
			_, err := Parse([]byte(sample + extra))
			if err == nil {
				t.Fatalf("the config parsed with %s", extra)
			}
			if !strings.Contains(err.Error(), "mailbox.keychain_item") {
				t.Errorf("the refusal does not name the key the operator wrote: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want ~%q", err, c.want)
			}
		})
	}
	// And the shape M5 allows, including every character of the alphabet and
	// exactly the bound, parses and round-trips.
	for _, item := range []string{"fugaro-mailbox-aurora", "aurora.A_z-0", "aurora" + strings.Repeat("b", 58)} {
		lc, err := Parse([]byte(sample + block(item)))
		if err != nil {
			t.Fatalf("Parse(%q): %v", item, err)
		}
		if lc.Mailbox == nil || lc.Mailbox.KeychainItem != item {
			t.Fatalf("mailbox = %+v, want the item %q", lc.Mailbox, item)
		}
		out, err := lc.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "keychain_item: "+item) {
			t.Errorf("the item name did not survive a round trip:\n%s", out)
		}
	}
}

// TestMailboxBlockIsOptional: `fugaro mailbox` refuses at startup when no
// mailbox is configured (plan §8's run-level row), so the *config* must be able
// to carry none — a project that never pairs is not a broken project. The nil
// block is the state T5 refuses on, and it must be distinguishable from a
// block with a name in it.
func TestMailboxBlockIsOptional(t *testing.T) {
	lc, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("a config with no mailbox block: %v", err)
	}
	if lc.Mailbox != nil {
		t.Errorf("mailbox = %+v, want nil", lc.Mailbox)
	}
}

// TestTheKeychainItemNameIsPublishedAndMerged is the merge rule the plan names
// for this block (§13): the item name is the installation's, like batch: and
// watch:, so Shared() carries it and MergeShared takes the published one when
// the publishing machine has none — otherwise a launcher whose own config has
// no mailbox: block erases the name another machine recorded, silently, in the
// only place the installation's copy lives.
//
// Mutation (verified, run and restored): delete `if out.Mailbox == nil {
// out.Mailbox = published.Mailbox }` from MergeShared: the first assertion
// fails on a nil block, and no other test does.
func TestTheKeychainItemNameIsPublishedAndMerged(t *testing.T) {
	block := "mailbox:\n  keychain_item: fugaro-mailbox-aurora\n"
	existing := sharedBase(t, block)
	local := sharedBase(t, "")

	// Published: what MergeShared merges from is what readers get.
	if s := existing.Shared(); s.Mailbox == nil || s.Mailbox.KeychainItem != "fugaro-mailbox-aurora" {
		t.Fatalf("Shared() dropped the mailbox block: %+v", s.Mailbox)
	}
	got := MergeShared(existing, local)
	if got.Mailbox == nil || got.Mailbox.KeychainItem != "fugaro-mailbox-aurora" {
		t.Fatalf("a publish from a machine with no mailbox: block dropped the published one: %+v", got.Mailbox)
	}
	out, err := got.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "keychain_item: fugaro-mailbox-aurora") {
		t.Errorf("the merged file carries no item name:\n%s", out)
	}
	// The local block wins when there is one: a pointer field takes the local
	// value, and the operator who wrote their own item name is the one who
	// knows where their key is.
	local = sharedBase(t, "mailbox:\n  keychain_item: fugaro-mailbox-aurora-local\n")
	if got := MergeShared(existing, local); got.Mailbox == nil || got.Mailbox.KeychainItem != "fugaro-mailbox-aurora-local" {
		t.Errorf("the local item name must win: %+v", got.Mailbox)
	}
	// A published object with none leaves the local one alone.
	if got := MergeShared(sharedBase(t, ""), local); got.Mailbox == nil || got.Mailbox.KeychainItem != "fugaro-mailbox-aurora-local" {
		t.Errorf("a published object with no mailbox block dropped the local one: %+v", got.Mailbox)
	}
	// Neither side having one writes no mailbox: key at all.
	if got := MergeShared(sharedBase(t, ""), sharedBase(t, "")); got.Mailbox != nil {
		t.Errorf("two configs with no mailbox block merged into %+v", got.Mailbox)
	}
	// The published name is the name and never a key: it is what the operator
	// checks their own keychain with, and M5 prints it.
	if !strings.Contains(string(out), "fugaro-mailbox-aurora") {
		t.Errorf("the published file does not carry the item name:\n%s", out)
	}
}
