package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// The promote launch is `fugaro run --promote <area> --mode <level>`
// (docs/rigor-modes/levels-spec.md §4, LD6, docs/rigor-modes/
// levels-reconciliation.md §3b item 12, answered 2026-10-22). It raises one
// area of one repository to a higher level instead of running a task of its
// own: the launcher resolves the area's rules and refuses a target it may not
// raise to, and the runner reads the area's recorded debt and works at the
// level asked for.
//
// **The area is the repository's own part of the rule.** "One promote run per
// member repository of the area" (§3b item 12a) is what this launch is: it
// raises the area **in the repository the run is in**, so a rule of that
// repository's `rigor.areas` — a glob in its own tree — and a project layer's
// area alike are promoted one member at a time. A project area is read for
// this repository's own member entry (`defaults.rigor.areas[].members`), whose
// paths are the scope the run's closes are held inside; a member with no paths
// is the whole repository, and an area that names no member of this repository
// is refused. What a project area does not get is a level written into a
// repository's file: item 12b raises a repo-owned area's level in the pull
// request and leaves a project area's to the operator, on the layer, so the run
// raises the area's code and the layer's rule stays where its writer put it.
//
// **The level is compared, not trusted.** A promote run raises: a target below
// the level the area already resolves to is refused, and equal is allowed
// (the same rule, one reading further on: the pass pays down the area's debt
// at the level it is already at). Hotfix is off the rigor axis — a level a run
// is asked for and never one an area holds (docs/rigor-modes/levels-spec.md
// §2) — so it is never a target.

// promoteExcludes are the flags a promote launch refuses, with why. Each is a
// flag that names or shapes a run of the caller's own: the area's paths are
// the launch's declared paths, and the brief is the runner's.
var promoteExcludes = []struct{ name, why string }{
	{"path", "the area's own paths are this launch's declared paths, and a promote run raises the area it named rather than paths of its own"},
	{"attempts", "a promote pass is one run of the area, not a best-of-N batch"},
	{"continues", "a promote pass is a run of the area's own, and --continues joins an existing chain"},
	{"review-run", "a promote pass raises the area's own code, and a fix round answers a review's findings"},
	{"review-levels", "a promote run resolves its review levels from the mode it raises the area to"},
}

// promoteArea is `--promote`'s one argument, validated: the name of the
// area, with every flag a promote run does not carry refused beside it.
//
// The level it raises to is `--mode`'s, and the flag is required here rather
// than resolved from the layers: a promote run's whole point is the level it
// takes the area to, and a launch that named none would work at whatever the
// area already resolves to while reporting a promote that changed nothing.
func promoteArea(cmd *cobra.Command, o *runOptions, args []string) (string, error) {
	if cmd.Flags().Changed("task-file") {
		return "", userErr("--promote takes no --task-file: the area it raises is its one argument (fugaro run --promote <area> --mode <level>)")
	}
	if len(args) != 1 {
		return "", userErr("--promote names the area it raises as its one argument, and %d were given: fugaro run --promote <area> --mode <level>", len(args))
	}
	area := strings.TrimSpace(args[0])
	if msg := (task.Promote{Area: area}).Problem(); msg != "" {
		return "", userErr("--promote: the area %s", msg)
	}
	if o.rigor == "" {
		return "", userErr("--promote %s: a promote run says which level it raises the area to, so it takes --mode <level> (one of %s)", area, rigor.RigorList())
	}
	if rigor.Rigor(o.rigor) == rigor.Hotfix {
		return "", userErr("--promote %s --mode hotfix: hotfix is off the rigor axis, a level a run is asked for and never one an area holds, so it is never a promote target (docs/rigor-modes/levels-spec.md §2)", area)
	}
	for _, x := range promoteExcludes {
		if cmd.Flags().Changed(x.name) {
			return "", userErr("--promote %s takes no --%s: %s", area, x.name, x.why)
		}
	}
	return area, nil
}

// promotePaths is the paths a spec declares: a promote run declares the area's
// own (--promote refuses --path, since the area is what is being raised), and
// every other launch declares the paths its launcher listed.
func promotePaths(promote *task.Promote, declared []string) []string {
	if promote != nil {
		return promote.Paths
	}
	return declared
}

// promoteTaskText is the task a promote launch carries: the launcher's own
// line about the area and the level. The brief the run works from is the
// runner's, built at bootstrap from the area's own backlog items
// (runner.promoteBrief), so this is what the task says rather than the whole
// of what the run reads — and it is what a reader of the stored task, the
// record and the pull request's fallback body is told the run was for.
func promoteTaskText(area string, target rigor.Rigor) string {
	return fmt.Sprintf("Raise the %s area to %s: work the area's code up to the level of %s, pay down the area's own recorded backlog items, and close the ones this run's diff pays down (docs/rigor-modes/levels-spec.md §4).", area, target, target)
}

// resolvePromote turns the area's name and the level asked for into the block
// the task carries (task.Spec.Promote), or refuses the launch: an area the
// repository's own fugaro.yaml does not declare, an area with no paths to
// raise, and a target below the level the area already resolves to.
//
// It reads the checkout's own document (checkoutConfig) and the levels that
// resolve over it, which is the reading a launch can make: whether the run
// itself resolves a different level from the config at the ref it checks out
// is the runner's question (it resolves again after cloning), and this is the
// refusal that has to be made before the run is paid for. The refusal is
// therefore the same one the run could have made, and the runner's own
// resolution is what the run works at.
func resolvePromote(ctx context.Context, repo, area, target string) (*task.Promote, error) {
	cfg := checkoutConfig(ctx, repo)
	if cfg == nil {
		return nil, userErr("--promote reads the area out of %s's own fugaro.yaml (rigor.areas) and the project layer it resolves over, and this launch has no checkout of that repository in the working directory: run it from the repository's own checkout, or name the area of the repository you are in", repo)
	}
	rc := cfg.RigorConfig()
	canon, err := task.CanonicalRepo(repo)
	if err != nil {
		return nil, userErr("--promote %s: %v", area, err)
	}
	// This repository's part of the area, and the level the rule itself
	// declares: the rule's own level for a rule of the repository's file, the
	// project rule's for a project area — and, for the latter, only this
	// repository's member paths, which is what "one promote run per member
	// repo" means (§3b item 12a: a project area is promoted one member at a
	// time, each run in the member repository it raises).
	names := make([]string, 0, len(rc.RepoAreas)+len(rc.ProjectAreas))
	for _, a := range rc.RepoAreas {
		names = append(names, a.Name)
	}
	for _, a := range rc.ProjectAreas {
		names = append(names, a.Name)
	}
	// found is this repository's part of the area: the paths the run's closes
	// are held inside, and the level the rule itself declares.
	var found bool
	var declared rigor.Rigor
	var paths []string
	for _, a := range rc.RepoAreas {
		if a.Name != area {
			continue
		}
		if strings.TrimSpace(a.Match) == "" {
			return nil, userErr("--promote %s: the area names no path in this repository, so a run of it has no code to raise (rigor.areas[].match)", area)
		}
		found, declared, paths = true, a.Level, []string{a.Match}
	}
	for _, a := range rc.ProjectAreas {
		if a.Name != area || found {
			continue
		}
		// A project area is promoted one member at a time, from the member
		// repository the run is in (§3b item 12a): what this launch needs is
		// this repository's own part of the rule, which is its member entry —
		// or, for a rule of the older whole-layer shape, the glob that is in
		// effect in every repository of the project.
		for _, m := range a.Members {
			if m.Repo != canon {
				continue
			}
			// A member with no paths is the whole repository: a scope with no
			// globs in it, inside which every path of the run's diff falls.
			found, declared, paths = true, a.Level, m.Paths
			break
		}
		if !found && strings.TrimSpace(a.Match) != "" {
			found, declared, paths = true, a.Level, []string{a.Match}
		}
	}
	if !found {
		if len(names) == 0 {
			return nil, userErr("--promote %s: neither %s's fugaro.yaml (rigor.areas) nor the project layer its configuration resolves over declares a rigor.areas rule, so there is no area to raise", area, repo)
		}
		return nil, userErr("--promote %s: %s declares no area by that name — neither its own fugaro.yaml (rigor.areas) nor the project layer its configuration resolves over; they declare %s. A project area is promoted from one of its member repositories — the run's own — so this launch needs an area that names this repository as a member (docs/rigor-modes/levels-reconciliation.md §3b item 12a)", area, repo, strings.Join(names, ", "))
	}
	// The level this area already resolves to. The rule's own level is read
	// beside what the resolver answers, because a rule may be written with a
	// brace pattern (`internal/{payments,ledger}/**`): the resolver is asked
	// about the paths a run touches, and a glob is not a path — matched against
	// its own text it decides nothing — so the rule's declaration is the
	// stronger of the two and the one that has to hold.
	current := rigor.Resolve(rc, rigor.Request{Repo: canon, Paths: paths}).Level
	if current == "" {
		current = rigor.Default
	}
	if declared.Rank() > current.Rank() {
		current = declared
	}
	want := rigor.Rigor(target)
	if want.Rank() < current.Rank() {
		return nil, userErr("--promote %s --mode %s: the area already resolves to %s, and a promote run raises an area — a target below the area's own level is refused, and a target equal to it is allowed (docs/rigor-modes/levels-spec.md §4)", area, want, current)
	}
	return &task.Promote{Area: area, Paths: paths}, nil
}
