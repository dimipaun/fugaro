package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// installedPlugins is where Claude Code records installed plugins. Tests
// replace it.
var installedPlugins = pluginwire.DefaultInstalledPlugins

// updateSkillsOutput is `update-skills --json`.
type updateSkillsOutput struct {
	Checkout bool               `json:"checkout"`           // false outside a checkout: only the snippet
	Settings string             `json:"settings,omitempty"` // the settings file
	Changed  bool               `json:"changed"`            // written (with --check: not written, would change)
	Ref      string             `json:"ref,omitempty"`      // the tag it pins to
	Note     string             `json:"note,omitempty"`
	Snippet  string             `json:"snippet,omitempty"`
	Report   *pluginwire.Report `json:"report,omitempty"`
}

func newUpdateSkillsCmd() *cobra.Command {
	var (
		check  bool
		dir    string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "update-skills [--check] [--dir D] [--json]",
		Short: "Pin the Fugaro plugin in the repository's .claude/settings.json to this release",
		Long: `update-skills merges the Fugaro plugin's marketplace and its enablement into the
checkout's .claude/settings.json, pinned to this binary's release tag (a dev
build writes no pin). Every other key is kept; a fork's marketplace
repository is kept and only its ref moves; a file that is not valid JSON is
never rewritten. It shows the diff and writes the file; it never commits:
review it with git diff. It needs no credentials, no network and no
Terraform. Outside a checkout it prints the settings to add.

--check writes nothing: it prints how the pin compares with this binary (and
with the plugin installed on this machine when Claude Code's record is
readable) and exits 1 on any warning state (outdated, newer, unpinned,
foreign, not wired, installed differs); "not installed" and "cannot
compare" are informational.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdateSkills(cmd, dir, check, asJSON)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&check, "check", false, "print the state and exit 1 unless it is ok (informational states pass); write nothing")
	f.StringVar(&dir, "dir", ".", "a directory in the checkout")
	f.BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runUpdateSkills(cmd *cobra.Command, dir string, check, asJSON bool) error {
	out := cmd.OutOrStdout()
	emit := func(o updateSkillsOutput) error {
		if !asJSON {
			return nil
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(o)
	}
	say := func(format string, a ...any) {
		if !asJSON {
			fmt.Fprintf(out, format, a...)
		}
	}
	loc, ok := pluginwire.Locate(dir)
	if !ok {
		snippet := pluginwire.Snippet(Version)
		say("%s is not in a checkout (no .git above it), so there is nothing to update.\nTo wire the plugin by hand, merge this into the repository's .claude/settings.json:\n\n%s", dir, snippet)
		return emit(updateSkillsOutput{Snippet: snippet})
	}
	if check {
		r := pluginwire.Status(loc.Settings, Version, installedPlugins())
		say("%s\n", describeReport(loc.Settings, r))
		if err := emit(updateSkillsOutput{Checkout: true, Settings: loc.Settings, Report: &r}); err != nil {
			return err
		}
		if r.Worst() == pluginwire.SeverityWarning {
			return &ExitError{Code: ExitUserError, Err: fmt.Errorf("the Fugaro plugin wiring is %s", joinStates(r.States()))}
		}
		return nil
	}
	ch, err := pluginwire.Plan(loc.Settings, Version)
	switch {
	case errors.Is(err, pluginwire.ErrDev):
		snippet := pluginwire.Snippet(Version)
		say("This is a dev build of fugaro, which has no release tag to pin the plugin to, so nothing was written.\nWith a release build this adds to %s:\n\n%s", loc.Settings, snippet)
		return emit(updateSkillsOutput{Checkout: true, Settings: loc.Settings, Snippet: snippet})
	case pluginwire.IsInvalid(err):
		fmt.Fprintf(cmd.ErrOrStderr(), "%v\nthe file was not changed; fix it, or merge this in by hand:\n\n%s", err, pluginwire.Snippet(Version))
		return &ExitError{Code: ExitUserError, Err: errors.New("the settings file was left as it is")}
	case err != nil:
		return err
	}
	if !ch.Changed {
		say("%s already wires the Fugaro plugin pinned to %s; nothing to do.\n", loc.Settings, ch.Tag)
	} else {
		say("%s\n%s", loc.Settings, ch.Diff())
		if ch.Note != "" {
			say("note: %s\n", ch.Note)
		}
		if err := ch.Apply(); err != nil {
			return err
		}
		say("Updated %s to pin the Fugaro plugin at %s. Review it with git diff and commit it like any change.\n", loc.Settings, ch.Tag)
	}
	say("Teammates are offered the plugin when they open this folder in Claude Code and trust it. To install it now,\nrun `claude plugin install fugaro@fugaro --scope project` (or /plugin install fugaro@fugaro in Claude Code);\nif its skills are not listed, restart Claude Code in this folder.\n")
	r := pluginwire.Status(loc.Settings, Version, installedPlugins())
	return emit(updateSkillsOutput{Checkout: true, Settings: loc.Settings, Changed: ch.Changed, Ref: ch.Tag, Note: ch.Note, Report: &r})
}

func joinStates(ss []pluginwire.State) string {
	var s []string
	for _, x := range ss {
		s = append(s, string(x))
	}
	return strings.Join(s, ", ")
}

// describeReport is the report in words, one line per state that is not ok
// and the fix to run.
func describeReport(path string, r pluginwire.Report) string {
	if len(r.States()) == 0 {
		return fmt.Sprintf("%s: ok (plugin pinned to %s)", path, r.Ref)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:", path)
	for _, s := range r.States() {
		fmt.Fprintf(&b, "\n  %s", s)
		if s == r.Pin && r.Detail != "" {
			fmt.Fprintf(&b, ": %s", r.Detail)
		} else if s == r.Install && s == pluginwire.InstalledDiffers {
			fmt.Fprintf(&b, ": the installed plugin is %s, the pin is %s", r.Version, r.Ref)
		}
		if fix := s.Fix(); fix != "" {
			fmt.Fprintf(&b, " (%s)", fix)
		}
	}
	return b.String()
}
