package cli

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// noSkillWarningEnv silences the staleness line.
const noSkillWarningEnv = "FUGARO_NO_SKILL_WARNING"

// skillWarned is set once the line has been considered in this process.
var skillWarned atomic.Bool

// skillWarnCommands are the commands that carry the staleness line (design
// 4.5): the regular ones, never a machine-readable stream.
var skillWarnCommands = map[string]bool{"run": true, "ls": true, "validate": true, "init": true, "doctor": true}

// warnStaleSkills prints at most one stderr line per process when the plugin
// pinned in the checkout's .claude/settings.json is older or newer than this
// binary, or the installed plugin differs from the pin. It reads two small
// files, makes no network call, and never fails a command: nothing on
// stdout, nothing under --json, nothing inside a Cloud Run job, nothing with
// FUGARO_NO_SKILL_WARNING set (and not 0 or false), nothing for ls --watch.
// not wired, unpinned and foreign are for `doctor`, not a line on every
// command.
func warnStaleSkills(cmd *cobra.Command) {
	if cmd.Parent() == nil || cmd.Parent().Parent() != nil || !skillWarnCommands[cmd.Name()] {
		return
	}
	if w := cmd.Flags().Lookup("watch"); w != nil && w.Value.String() == "true" {
		return
	}
	if v := os.Getenv(noSkillWarningEnv); v != "" && v != "0" && v != "false" {
		return
	}
	if backend.OnCloudRun(os.Getenv) || !skillWarned.CompareAndSwap(false, true) {
		return
	}
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	loc, ok := pluginwire.Locate(wd)
	if !ok || !loc.Exists {
		return
	}
	if line := staleLine(pluginwire.Status(loc.Settings, Version, installedPlugins())); line != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), line)
	}
}

// staleLine is the one warning line for r, "" when there is none.
func staleLine(r pluginwire.Report) string {
	pin, bin := strings.TrimPrefix(r.Ref, "v"), strings.TrimPrefix(r.Binary, "v")
	switch {
	case r.Pin == pluginwire.Outdated:
		return fmt.Sprintf("warning: the Fugaro plugin pinned in .claude/settings.json is %s, this is %s: run fugaro upgrade --local", pin, bin)
	case r.Pin == pluginwire.Newer:
		return fmt.Sprintf("warning: the Fugaro plugin pinned in .claude/settings.json is %s, newer than this fugaro %s: upgrade fugaro (brew upgrade dimipaun/tap/fugaro)", pin, bin)
	case r.Install == pluginwire.InstalledDiffers:
		return fmt.Sprintf("warning: the installed Fugaro plugin is %s but .claude/settings.json pins %s: in Claude Code run /plugin marketplace update fugaro", r.Version, pin)
	}
	return ""
}
