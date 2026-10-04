package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/dimipaun/fugaro/internal/config"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The plugin-wiring stage (stage 8, design §4.3): merges the Fugaro
// marketplace and enabledPlugins entries into the checkout's
// .claude/settings.json, pinned to the binary's release tag, after showing the
// diff and a confirmation of its own (--yes confirms it). Only those two
// entries change; a file that is not valid JSON, a marketplace named fugaro
// that is not Fugaro's, and a fork are left alone and handed to the user. A
// dev build has no tag to pin to and the stage is skipped saying so; outside a
// checkout it prints the settings to add. It never commits.

// pluginFirstRun ends the wiring: Claude Code offers the plugin only after the
// folder-trust prompt (design §4.7).
const pluginFirstRun = "Teammates are offered the plugin when they open this folder in Claude Code and trust it. To install it now,\n" +
	"run `claude plugin install fugaro@fugaro --scope project` (or /plugin install fugaro@fugaro in Claude Code);\n" +
	"if its skills are not listed, restart Claude Code in this folder.\n"

type pluginStage struct {
	e       *initEngine
	printed bool // the outside-a-checkout snippet, said once
	// ask: the checkout's repository is not the project's yet and has to be
	// typed in before anything is written (set by plan); repo and root name it.
	ask    bool
	origin originInfo
	root   string
}

func newPluginStage(e *initEngine) *pluginStage { return &pluginStage{e: e} }

func (*pluginStage) Name() string { return initflow.Plugin }

func (s *pluginStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftCommand, Text: selfCommand() + " update-skills"}
}

// plan is the read-only merge for the working directory's checkout: the
// change, or the status that says why there is none.
func (s *pluginStage) plan(ctx context.Context) (*pluginwire.Change, *initflow.Status, error) {
	ch, st, err := s.plan1(ctx)
	if st != nil && st.State == initflow.NeedsYou && !strings.Contains(st.Detail, "repository stage was not reached") {
		st.Detail += "; the repository stage was not reached"
	}
	return ch, st, err
}

func (s *pluginStage) plan1(ctx context.Context) (*pluginwire.Change, *initflow.Status, error) {
	s.ask = false
	status := func(st initflow.State, detail string, left *initflow.Left) (*pluginwire.Change, *initflow.Status, error) {
		return nil, &initflow.Status{State: st, Detail: detail, Left: left}, nil
	}
	loc, ok := pluginwire.Locate(".")
	if !ok {
		if !s.printed {
			s.printed = true
			fmt.Fprintf(s.e.r.w, "not in a checkout, so nothing is wired. To use the Fugaro plugin, merge this into the repository's .claude/settings.json:\n\n%s\n", pluginwire.Snippet(Version))
		}
		return status(initflow.Skipped, "not in a checkout: the settings to add are printed above", nil)
	}
	if _, err := pluginwire.Tag(Version); err != nil {
		return status(initflow.Skipped, "this fugaro build has no release tag to pin the plugin to, so nothing is wired (a release build adds it)", nil)
	}
	// Only the checkout the project's own repository stage would target.
	e := s.e
	repo, ok := readOrigin(ctx, loc.Root)
	if !ok {
		return status(initflow.Skipped, "this checkout has no origin repository, so it is not one to wire", nil)
	}
	if data, rerr := os.ReadFile(filepath.Join(loc.Root, "fugaro.yaml")); rerr == nil {
		if p, perr := config.ProjectOf(data); perr != nil || p != e.lc.Name {
			return status(initflow.Skipped, "this checkout's fugaro.yaml names another project (or none), not "+pluginwire.Printable(e.lc.Name)+": the plugin is not wired here", nil)
		}
	}
	switch a, err := e.authState(repo); {
	case err != nil:
		return nil, nil, err
	case a == authNeeded:
		lf := onboardLeft(repo)
		return status(initflow.NeedsYou, unknownDetail(e.lc.Name, repo), &lf)
	case a == authAsk:
		s.origin, s.root, s.ask = repo, loc.Root, true
	}
	ch, err := pluginwire.Plan(loc.Settings, Version, s.e.r.o.allowFork)
	var fe *pluginwire.ForeignError
	var fk *pluginwire.ForkError
	switch {
	case errors.As(err, &fk):
		lf := initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftCommand, Text: selfCommand() + " update-skills --allow-fork"}
		return status(initflow.NeedsYou, fmt.Sprintf("the %q marketplace in %s names %s, not %s: not moved without --allow-fork", pluginwire.Marketplace, loc.Settings, pluginwire.Printable(fk.Repo), pluginwire.Repo), &lf)
	case errors.As(err, &fe), pluginwire.IsInvalid(err):
		lf := initflow.Left{Stage: initflow.Plugin, Kind: initflow.LeftConsole, Text: "fix " + loc.Settings + " (or merge the settings by hand: " + selfCommand() + " update-skills prints them), then rerun fugaro init"}
		return status(initflow.NeedsYou, pluginwire.Printable(oneLineCLI(err.Error())), &lf)
	case err != nil:
		return nil, &initflow.Status{State: initflow.NeedsYou, Detail: pluginwire.Printable(oneLineCLI(err.Error())), Left: nil}, nil
	}
	return ch, nil, nil
}

func (s *pluginStage) Check(ctx context.Context) (initflow.Status, error) {
	ch, st, err := s.plan(ctx)
	switch {
	case err != nil:
		return initflow.Status{}, err
	case st != nil:
		return *st, nil
	case !ch.Changed:
		d := "No changes: " + ch.Path + " pins the plugin to " + ch.Tag
		if ch.Disabled {
			d += " (the repository disables it)"
		}
		return initflow.Status{State: initflow.Done, Detail: d}, nil
	}
	d := "adds the Fugaro plugin to " + ch.Path + ", pinned to " + ch.Tag
	if s.ask {
		d += "; asks you to type the repository's name first (it is not the project's yet)"
	}
	return initflow.Status{State: initflow.Todo, Detail: d}, nil
}

func (s *pluginStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	ch, st, err := s.plan(ctx)
	switch {
	case err != nil:
		return initflow.Plan{}, err
	case st != nil:
		return initflow.Plan{Detail: st.Detail, NothingToDo: st.State != initflow.NeedsYou}, nil
	case !ch.Changed:
		return initflow.Plan{NothingToDo: true, Detail: "No changes"}, nil
	}
	fmt.Fprintf(s.e.r.w, "%s\n%s", ch.Path, ch.Diff())
	return initflow.Plan{Detail: "adds the Fugaro plugin to " + ch.Path + ", pinned to " + ch.Tag}, nil
}

func (s *pluginStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	s.ask = false
	ch, st, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if st != nil {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.leftFor(st)}
	}
	if !ch.Changed {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	r := s.e.r
	if s.ask { // not the project's repository yet: its own typed confirmation, which --yes never gives
		if !env.Interactive {
			return initflow.Outcome{}, &initflow.NeedsYouError{Left: onboardLeft(s.origin)}
		}
		ok, err := s.e.confirmRepo(s.root, s.origin)
		if err != nil {
			return initflow.Outcome{}, err
		}
		if !ok {
			return initflow.Outcome{}, &initflow.NeedsYouError{Left: onboardLeft(s.origin)}
		}
	}
	fmt.Fprintf(r.w, "%s\n%s", ch.Path, ch.Diff())
	if ch.Note != "" {
		fmt.Fprintf(r.w, "note: %s\n", ch.Note)
	}
	switch {
	case env.Yes:
		fmt.Fprintln(r.w, "  confirmed by --yes")
	case env.Interactive:
		fmt.Fprintf(r.w, "Write this to %s? [y/N]: ", ch.Path)
		line, _ := r.in.ReadString('\n') // an ended input is a no
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
		}
	default:
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	if err := ch.Apply(); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(r.w, "Updated %s to pin the Fugaro plugin at %s. Review it with git diff and commit it like any change.\n%s", ch.Path, ch.Tag, pluginFirstRun)
	return initflow.Outcome{Changed: true, Detail: "wired " + ch.Path + " to " + ch.Tag}, nil
}

func (s *pluginStage) leftFor(st *initflow.Status) initflow.Left {
	if st.Left != nil {
		return *st.Left
	}
	return s.Left()
}

// Verify re-reads the file: the merge is in, and a second merge changes
// nothing.
func (s *pluginStage) Verify(ctx context.Context) error {
	ch, st, err := s.plan(ctx)
	switch {
	case err != nil:
		return err
	case st != nil:
		return fmt.Errorf("the plugin wiring cannot be read back: %s", st.Detail)
	case ch.Changed:
		return fmt.Errorf("%s still lacks the Fugaro plugin after the write", ch.Path)
	}
	return nil
}
