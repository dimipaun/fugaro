package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// installedPlugins is where Claude Code records installed plugins. Tests
// replace it.
var installedPlugins = pluginwire.DefaultInstalledPlugins

// updateSkillsOutput is `update-skills --json`.
//
// Every member stays declared with its meaning ("A field never changes
// meaning", CLAUDE.md), and the merge's deletion (D949, U2 of
// docs/plugin-user-scope/implementation-plan.md) is why three of them are now
// what the checkout and the machine say rather than what this command wrote:
// settings is the checkout's own .claude/settings.json, read for a pin an older
// fugaro left there; changed is whether the plugin in Claude Code was installed
// or updated; ref is the ref that leftover pin carries, "" when there is none.
// snippet and disabled are what the deleted merge would have printed and
// honoured; nothing sets them any more, and they are kept so the document a
// reader outside this binary consumes keeps its shape.
type updateSkillsOutput struct {
	Checkout bool   `json:"checkout"`           // false outside a checkout: nothing is read and nothing installed
	Settings string `json:"settings,omitempty"` // the checkout's settings file
	Changed  bool   `json:"changed"`            // the plugin in Claude Code was installed or updated (with --check: never)
	Ref      string `json:"ref,omitempty"`      // the ref the checkout's settings carry
	Note     string `json:"note,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
	Foreign  string `json:"foreign,omitempty"`  // the marketplace repository when it is not dimipaun/fugaro
	Disabled bool   `json:"disabled,omitempty"` // the repository disables the plugin
	Error    string `json:"error,omitempty"`
	// Notices are what else the settings file says (hooks, permissions, other
	// plugins...), shown with --check so the file a pin lives in is read, not
	// just detected.
	Notices []string `json:"notices,omitempty"`
	// NoticesNote says the notices are not exhaustive; set whenever they are shown.
	NoticesNote string             `json:"notices_note,omitempty"`
	Release     *releaseInfo       `json:"release,omitempty"` // the binary's tag and commit, and how to verify the tag
	Report      *pluginwire.Report `json:"report,omitempty"`
}

func newUpdateSkillsCmd() *cobra.Command {
	var (
		check  bool
		dir    string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "update-skills [--check] [--dir D] [--json]",
		Short: "Install or update the Fugaro plugin in your own Claude Code",
		Long: `update-skills installs the Fugaro plugin in Claude Code at user scope, or updates
the install it finds there, through the claude CLI on PATH: a fixed set of
claude plugin commands, never with -y, with the marketplace declared at this
binary's release tag. It writes nothing in the checkout — the plugin is this
machine's, and no repository's settings are merged into any more (D949) — and
it needs no credentials and no Terraform. Without claude on PATH it prints the
slash commands to type in Claude Code instead. Running Claude Code sessions
apply the new version after a restart.

--check writes nothing and runs no claude command: it prints how the plugin
installed on this machine compares with this binary, and the pin a checkout
that still carries one holds — the merge that wrote those pins is gone, so a
pin an older fugaro left is read here (and by fugaro doctor's plugin-pin
check). It exits 1 on any warning state (outdated, newer, unpinned, foreign,
not wired, installed differs); "not installed" and "cannot compare" are
informational.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdateSkills(cmd, dir, check, asJSON)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&check, "check", false, "print the state and exit 1 unless it is ok (informational states pass); write nothing and run no claude command")
	f.StringVar(&dir, "dir", ".", "a directory in the checkout")
	f.BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}

func runUpdateSkills(cmd *cobra.Command, dir string, check, asJSON bool) error {
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	// In --json mode the human output goes to stderr, so the document on stdout
	// stays one JSON object even when claude's own step has something to say.
	w := io.Writer(out)
	if asJSON {
		w = errOut
	}
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
	// fail reports a refusal: the JSON object on stdout in --json, the message
	// through the returned error, exit 1.
	fail := func(o updateSkillsOutput, err error) error {
		o.Error = err.Error()
		if asJSON {
			if e := emit(o); e != nil {
				return e
			}
		}
		return &ExitError{Code: ExitUserError, Err: err}
	}
	// warnFork says it loudly, in every output mode: the checkout's settings
	// point agents at someone else's plugin repository.
	warnFork := func(repo string) {
		fmt.Fprintf(errOut, "WARNING: the %q marketplace in this repository's settings is %s, not %s. Its skills are not Fugaro's; Claude Code can install that plugin for anyone who trusts the folder. Check it is yours.\n", pluginwire.Marketplace, repo, pluginwire.Repo)
	}
	if l := releaseLine(); l != "" {
		say("%s\n", l)
	}
	loc, ok := pluginwire.Locate(dir)
	if !ok {
		say("%s is not in a checkout (no .git above it), so there is no repository to install the plugin from.\n", pluginwire.Printable(dir))
		if check {
			return fail(updateSkillsOutput{}, fmt.Errorf("%s is not in a checkout", pluginwire.Printable(dir)))
		}
		return emit(updateSkillsOutput{})
	}
	report := pluginwire.Status(loc.Settings, Version, installedPlugins())
	if check {
		say("%s\n", describeReport(loc.Settings, report))
		o := updateSkillsOutput{Checkout: true, Settings: loc.Settings, Ref: report.Ref, Report: &report, Notices: pluginwire.NoticesFor(loc.Settings)}
		say("%s", pluginwire.NoticeText(o.Notices))
		o.NoticesNote = pluginwire.NoticesCaveat
		if report.Pin == pluginwire.Foreign {
			o.Foreign = report.Repo
			warnFork(report.Repo)
		}
		if err := emit(o); err != nil {
			return err
		}
		if report.Worst() == pluginwire.SeverityWarning {
			return &ExitError{Code: ExitUserError, Err: fmt.Errorf("the Fugaro plugin wiring is %s", joinStates(report.States()))}
		}
		return nil
	}
	// The machine-side half: fugaro upgrade's own plugin step, the same
	// function the plugin stage runs, which installs or updates the plugin in
	// the user's own Claude Code at user scope.
	u := &upgradeCtx{cmd: cmd, w: w, loc: loc}
	step := pluginStep(cmd.Context(), u)
	if step.state == stepFailed {
		return fail(updateSkillsOutput{Checkout: true, Settings: loc.Settings, Ref: report.Ref}, errors.New(step.reason))
	}
	say("%s\n", step.reason)
	report = pluginwire.Status(loc.Settings, Version, installedPlugins())
	return emit(updateSkillsOutput{Checkout: true, Settings: loc.Settings, Changed: step.state == stepDone, Ref: report.Ref, Report: &report})
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
