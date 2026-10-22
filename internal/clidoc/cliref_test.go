package main

// The generator's own tests (website plan, Task W0.4).
//
// TestCliReferenceIsCurrent is the one that makes "generated" true rather than
// "generated once": it runs Generate over the real command tree and compares
// the bytes with the committed website/content/cli-reference.md, so a command
// or a flag that lands in internal/cli without the reference being regenerated
// fails here, with the command that regenerates it in the message.
//
// The rest are the generator's own rules, checked on a synthetic tree so they
// hold whatever the real tree happens to contain: a hidden command and a
// hidden flag stay out, a required flag is marked as one, and the same tree
// produces the same bytes twice. The two directions of the drift check against
// the real tree -- every command and flag named, nothing invented -- live in
// internal/cli/docs_website_cliref_test.go, which is where the command tree
// and the other docs tests are; this package cannot be imported from there,
// because it imports internal/cli.

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/cli"
)

// TestCliReferenceIsCurrent holds the committed reference to the generator's
// output. The two files cannot differ: the committed one is the generated one.
func TestCliReferenceIsCurrent(t *testing.T) {
	want := Generate(cli.NewRootCmd())
	got, err := os.ReadFile("../../" + defaultPath)
	if err != nil {
		t.Fatalf("%s: %v (regenerate it with `go run ./internal/clidoc` at the repository root)", defaultPath, err)
	}
	if string(got) != want {
		t.Errorf("%s is not what the command tree generates: regenerate it with `go run ./internal/clidoc` at the repository root (generated %d bytes, committed %d)", defaultPath, len(want), len(got))
	}
}

// TestCliReferenceGeneratorSkipsHiddenCommandsAndFlags: the skip rule is read
// off the tree (cobra's Hidden), so a hidden command, the subtree under it and
// a hidden flag of a visible command all stay out of the reference.
func TestCliReferenceGeneratorSkipsHiddenCommandsAndFlags(t *testing.T) {
	root := fakeTree()
	doc := Generate(root)
	for _, absent := range []string{
		"`fake secret`",     // the hidden command itself
		"`fake secret sub`", // and the subtree under it
		"--internal",        // the visible command's hidden flag
	} {
		if strings.Contains(doc, absent) {
			t.Errorf("the reference names %s, which the tree hides: a hidden command is not user documentation (F38)\n%s", absent, doc)
		}
	}
	if !strings.Contains(doc, "## `fake run`") {
		t.Errorf("the reference leaves out the visible command `fake run`:\n%s", doc)
	}
}

// TestCliReferenceDocumentsCobraDefaults is the regression test for the claim
// the preamble makes: cobra adds its `help` and `completion` commands and the
// `-h, --help` flag of every command inside Execute, not when the tree is
// built, and `fugaro --help` lists all three. A reference generated from the
// un-executed tree would print "every command and flag `fugaro` ships" while
// omitting them, and no test could catch it, because a test deriving its
// expectations from the same un-executed tree agrees with the mistake.
func TestCliReferenceDocumentsCobraDefaults(t *testing.T) {
	doc := Generate(cli.NewRootCmd())
	for _, want := range []string{
		"## `fugaro completion`",
		"## `fugaro completion bash`",
		"## `fugaro help`",
		"| `-h`, `--help` | bool | `false` | no | help for fugaro |",
		"| `-v`, `--version` | bool | `false` | no | version for fugaro |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the reference does not carry %q, which `fugaro --help` lists:\n%s", want, doc)
		}
	}
}

// TestCliReferenceGeneratorStatesFlags: a flag's type, default, required-ness
// and description come from the pflag itself, so a required flag is marked
// required (from cobra's own annotation, not from a reading of the default)
// and a flag with no default shows the empty string rather than a blank cell.
func TestCliReferenceGeneratorStatesFlags(t *testing.T) {
	doc := Generate(fakeTree())
	for _, want := range []string{
		"| `--name` | string | \"\" | yes | the task text |",
		"| `--dry-run` | bool | `false` | no | print what would happen |",
		"| `--since` | duration | `24h0m0s` | no | how far back to look |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the reference does not carry the flag row %q:\n%s", want, doc)
		}
	}
}

// TestCliReferenceGeneratorIsPure: the same tree produces the same bytes, so a
// regenerated file diffs only where the tree changed.
func TestCliReferenceGeneratorIsPure(t *testing.T) {
	root := fakeTree()
	first, second := Generate(root), Generate(root)
	if first != second {
		t.Errorf("Generate is not deterministic: two runs over one tree differ")
	}
}

// fakeTree is the smallest tree that exercises every rule: a visible command
// with flags (one required, one with a default, one with none), a hidden
// command with a visible child, and a hidden flag on a visible command.
func fakeTree() *cobra.Command {
	root := &cobra.Command{Use: "fake", Short: "a synthetic tree"}
	run := &cobra.Command{
		Use:   "run [TEXT]",
		Short: "launch a task",
		Long:  "Launch a task in the cloud.\n\nIt always ends with a pull request.",
	}
	run.Flags().String("name", "", "the task text")
	run.Flags().Bool("dry-run", false, "print what would happen")
	run.Flags().Duration("since", 24*60*60*1e9, "how far back to look")
	run.Flags().String("internal", "", "tests only")
	_ = run.Flags().MarkHidden("internal")
	_ = run.MarkFlagRequired("name")

	secret := &cobra.Command{Use: "secret", Short: "hidden", Hidden: true}
	secret.AddCommand(&cobra.Command{Use: "sub", Short: "hidden too"})

	root.AddCommand(run, secret)
	return root
}
