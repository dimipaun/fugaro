package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func scoutLeaves(t *testing.T) []*cobra.Command {
	t.Helper()
	var leaves []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if len(c.Commands()) == 0 {
			leaves = append(leaves, c)
			return
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(NewRootCmd())
	return leaves
}

func TestZZScoutLeaves(t *testing.T) {
	leaves := scoutLeaves(t)
	t.Logf("leaves: %d", len(leaves))
	for _, leaf := range leaves {
		entered := false
		orig := leaf.RunE
		origRun := leaf.Run
		if orig != nil {
			leaf.RunE = func(c *cobra.Command, args []string) error {
				entered = true
				return orig(c, args)
			}
		} else if origRun != nil {
			leaf.Run = func(c *cobra.Command, args []string) {
				entered = true
				origRun(c, args)
			}
		}
		closedStdinWorld(t)
		argv := strings.Fields(leaf.CommandPath())[1:]
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		w.Close()
		out := &syncBuf{}
		root := NewRootCmd()
		root.SetIn(r)
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(NormalizeArgs(argv))
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = root.Execute()
		}()
		returned := true
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			returned = false
		}
		r.Close()
		text := strings.TrimSpace(out.String())
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[:i]
		}
		t.Logf("%-34s entered=%-5v returned=%-5v | %s", leaf.CommandPath(), entered, returned, text)
	}
}

func TestZZScoutArgs(t *testing.T) {
	for _, c := range [][]string{
		{"settings", "set", "budget.perRunUsd", "50"},
		{"allow", "delete", "someone@example.com"},
		{"restore", "checkpoints"},
		{"image", "build", "base"},
		{"managed", "flag", "august-strand-461517"},
		{"managed", "unflag", "august-strand-461517"},
		{"project", "enroll"},
		{"policy", "migrate"},
		{"policy", "publish"},
		{"update", "workspace"},
		{"budget", "resume", "--all"},
	} {
		closedStdinWorld(t)
		r, w, _ := os.Pipe()
		w.Close()
		out := &syncBuf{}
		root := NewRootCmd()
		root.SetIn(r)
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(NormalizeArgs(c))
		done := make(chan struct{})
		go func() { defer close(done); _ = root.Execute() }()
		returned := true
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			returned = false
		}
		r.Close()
		t.Logf("=== %v returned=%v\n%s\n", c, returned, out.String())
	}
}
