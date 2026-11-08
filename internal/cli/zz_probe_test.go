package cli

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

var zzFlatten = regexp.MustCompile(`\s+`)

func TestZZInterior(t *testing.T) {
	root := NewRootCmd()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if len(c.Commands()) > 0 {
			if c.RunE != nil || c.Run != nil {
				fmt.Printf("INTERIOR %-24s use=%q\n", c.CommandPath(), c.Use)
			}
			for _, ch := range c.Commands() {
				walk(ch)
			}
			return
		}
	}
	walk(root)
	for _, p := range []string{
		"fugaro review", "fugaro settings", "fugaro creds", "fugaro secrets",
		"fugaro pair", "fugaro accounts", "fugaro backends",
	} {
		closedStdinWorld(t)
		out, ret, ent, err := closedStdinRun(t, p)
		flat := zzFlatten.ReplaceAllString(strings.TrimSpace(out), " ")
		if len(flat) > 180 {
			flat = flat[:180]
		}
		fmt.Printf("RUN %-20s ret=%v ent=%v err=%v | %s\n", p, ret, ent, err, flat)
	}
}
