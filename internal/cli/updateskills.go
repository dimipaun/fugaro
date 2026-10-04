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
	Checkout bool   `json:"checkout"`           // false outside a checkout: only the snippet
	Settings string `json:"settings,omitempty"` // the settings file
	Changed  bool   `json:"changed"`            // written (with --check: not written, would change)
	Ref      string `json:"ref,omitempty"`      // the tag it pins to
	Note     string `json:"note,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
	Foreign  string `json:"foreign,omitempty"`  // the marketplace repository when it is not dimipaun/fugaro
	Disabled bool   `json:"disabled,omitempty"` // the repository disables the plugin; left so
	Error    string `json:"error,omitempty"`
	// Notices are what else the settings file says (hooks, permissions, other
	// plugins...), shown before the file is blessed.
	Notices []string `json:"notices,omitempty"`
	// NoticesNote says the notices are not exhaustive; set whenever they are shown.
	NoticesNote string             `json:"notices_note,omitempty"`
	Release     *releaseInfo       `json:"release,omitempty"` // the binary's tag and commit, and how to verify the tag
	Report      *pluginwire.Report `json:"report,omitempty"`
}

func newUpdateSkillsCmd() *cobra.Command {
	var (
		check     bool
		allowFork bool
		dir       string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "update-skills [--check] [--dir D] [--json]",
		Short: "Pin the Fugaro plugin in the repository's .claude/settings.json to this release",
		Long: `update-skills merges the Fugaro plugin's marketplace and its enablement into the
checkout's .claude/settings.json, pinned to this binary's release tag (a dev
build writes no pin). Every other key is kept; a fork's marketplace
repository is kept and only its ref moves; a file that is not valid JSON is
never rewritten; a changed file is re-indented to two spaces. A marketplace
that names another repository than dimipaun/fugaro is reported with a
warning and left alone unless --allow-fork says it is yours (then only its ref
moves); a plugin the repository sets to false stays disabled. It shows the diff and writes the file; it never commits:
review it with git diff. It needs no credentials, no network and no
Terraform. Outside a checkout it prints the settings to add.

--check writes nothing: it prints how the pin compares with this binary (and
with the plugin installed on this machine when Claude Code's record is
readable) and exits 1 on any warning state (outdated, newer, unpinned,
foreign, not wired, installed differs); "not installed" and "cannot
compare" are informational.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdateSkills(cmd, dir, check, allowFork, asJSON)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&check, "check", false, "print the state and exit 1 unless it is ok (informational states pass); write nothing")
	f.BoolVar(&allowFork, "allow-fork", false, "move the ref of a marketplace repository other than dimipaun/fugaro (a fork you host) to this release's tag")
	f.StringVar(&dir, "dir", ".", "a directory in the checkout")
	f.BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runUpdateSkills(cmd *cobra.Command, dir string, check, allowFork, asJSON bool) error {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	emit := func(o updateSkillsOutput) error {
		if !asJSON {
			return nil
		}
		o.Release = release()
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(o)
	}
	say := func(format string, a ...any) {
		if !asJSON {
			fmt.Fprintf(out, format, a...)
		}
	}
	// fail reports a refusal: the message and the snippet on stderr (a JSON
	// object on stdout in --json), exit 1, the file untouched.
	fail := func(o updateSkillsOutput, err error, snippet bool) error {
		o.Error = err.Error()
		if snippet {
			o.Snippet = pluginwire.Snippet(Version)
		}
		if !asJSON {
			if snippet {
				fmt.Fprintf(errOut, "the file was not changed; fix it, or merge this in by hand:\n\n%s", o.Snippet)
			}
		} else if e := emit(o); e != nil {
			return e
		}
		return &ExitError{Code: ExitUserError, Err: err}
	}
	// warnFork says it loudly, in every output mode: the file points agents at
	// someone else's plugin repository.
	warnFork := func(repo string) {
		fmt.Fprintf(errOut, "WARNING: the %q marketplace in this repository's settings is %s, not %s. Its skills are not Fugaro's; that plugin installs by itself, without a prompt, for anyone who trusts the folder. Check it is yours.\n", pluginwire.Marketplace, repo, pluginwire.Repo)
	}
	if l := releaseLine(); l != "" {
		say("%s\n", l)
	}
	loc, ok := pluginwire.Locate(dir)
	if !ok {
		snippet := pluginwire.Snippet(Version)
		say("%s is not in a checkout (no .git above it), so there is nothing to update.\nTo wire the plugin by hand, merge this into the repository's .claude/settings.json:\n\n%s", pluginwire.Printable(dir), snippet)
		if check {
			return fail(updateSkillsOutput{}, fmt.Errorf("%s is not in a checkout", pluginwire.Printable(dir)), false)
		}
		return emit(updateSkillsOutput{Snippet: snippet})
	}
	if check {
		r := pluginwire.Status(loc.Settings, Version, installedPlugins())
		say("%s\n", describeReport(loc.Settings, r))
		o := updateSkillsOutput{Checkout: true, Settings: loc.Settings, Report: &r, Notices: pluginwire.NoticesFor(loc.Settings)}
		for _, n := range o.Notices {
			say("heads-up: %s\n", n)
		}
		say("heads-up: %s\n", pluginwire.NoticesCaveat)
		o.NoticesNote = pluginwire.NoticesCaveat
		if r.Pin == pluginwire.Foreign {
			o.Foreign = r.Repo
			warnFork(r.Repo)
		}
		if err := emit(o); err != nil {
			return err
		}
		if r.Worst() == pluginwire.SeverityWarning {
			return &ExitError{Code: ExitUserError, Err: fmt.Errorf("the Fugaro plugin wiring is %s", joinStates(r.States()))}
		}
		return nil
	}
	ch, err := pluginwire.Plan(loc.Settings, Version, allowFork)
	var fe *pluginwire.ForeignError
	var fk *pluginwire.ForkError
	switch {
	case errors.Is(err, pluginwire.ErrDev):
		snippet := pluginwire.Snippet(Version)
		say("This is a dev build of fugaro, which has no release tag to pin the plugin to, so nothing was written.\nWith a release build this adds to %s:\n\n%s", loc.Settings, snippet)
		return emit(updateSkillsOutput{Checkout: true, Settings: loc.Settings, Snippet: snippet})
	case errors.As(err, &fk):
		warnFork(pluginwire.Printable(fk.Repo))
		return fail(updateSkillsOutput{Checkout: true, Settings: loc.Settings, Foreign: pluginwire.Printable(fk.Repo)}, err, false)
	case errors.As(err, &fe):
		return fail(updateSkillsOutput{Checkout: true, Settings: loc.Settings}, err, true)
	case pluginwire.IsInvalid(err):
		return fail(updateSkillsOutput{Checkout: true, Settings: loc.Settings}, err, true)
	case err != nil:
		return err
	}
	if ch.Foreign != "" {
		warnFork(ch.Foreign)
	}
	if ch.Note != "" {
		fmt.Fprintf(errOut, "note: %s\n", ch.Note)
	}
	if !ch.Changed {
		say("%s already wires the Fugaro plugin pinned to %s; nothing to do.\n%s", loc.Settings, ch.Tag, ch.NoticeText())
	} else {
		say("%s\n%s%s", loc.Settings, ch.Diff(), ch.NoticeText())
		if err := ch.Apply(); err != nil {
			return err
		}
		say("Updated %s to pin the Fugaro plugin at %s. Review it with git diff and commit it like any change.\n%s", loc.Settings, ch.Tag, pluginRefresh)
	}
	r := pluginwire.Status(loc.Settings, Version, installedPlugins())
	if r.Pin == pluginwire.Foreign || ch.Disabled {
		say("%s\n", describeReport(loc.Settings, r))
	}
	say("%s", pluginFirstRun)
	return emit(updateSkillsOutput{Checkout: true, Settings: loc.Settings, Changed: ch.Changed, Ref: ch.Tag, Note: ch.Note, Foreign: ch.Foreign, Disabled: ch.Disabled, Notices: ch.Notices, NoticesNote: pluginwire.NoticesCaveat, Report: &r})
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
