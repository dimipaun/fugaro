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
// **The area is the repository's own rule.** A rule of `rigor.areas` in the
// repository's fugaro.yaml is written by the repository and matches paths in
// it, so "one promote run per member repository of the area" (§3b item 12a) is
// the launch's own repository: the member repo is the one whose file declares
// the area, and a run of it cannot raise an area of a repository it is not in.
// A project layer's area names other repositories as its members
// (`defaults.rigor.areas[].members`), and raising one is the operator's call
// on the layer rather than a run's — this launch refuses such a name rather
// than raising a level in a repository whose own file never asked for it.
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
		return nil, userErr("--promote reads the area out of %s's own fugaro.yaml (rigor.areas), and this launch has no checkout of that repository in the working directory: run it from the repository's own checkout, or name the area of the repository you are in", repo)
	}
	rc := cfg.RigorConfig()
	names := make([]string, 0, len(rc.RepoAreas))
	var found *rigor.Area
	for i := range rc.RepoAreas {
		names = append(names, rc.RepoAreas[i].Name)
		if rc.RepoAreas[i].Name == area {
			found = &rc.RepoAreas[i]
		}
	}
	if found == nil {
		if len(names) == 0 {
			return nil, userErr("--promote %s: %s's fugaro.yaml declares no rigor.areas rule at all, so the repository has no area to raise; a project layer's area is the operator's to raise, on the layer (docs/rigor-modes/levels-reconciliation.md §3b item 12a)", area, repo)
		}
		return nil, userErr("--promote %s: %s's fugaro.yaml declares no area by that name; it declares %s (rigor.areas). A project layer's area names its own members and is the operator's to raise, on the layer", area, repo, strings.Join(names, ", "))
	}
	if strings.TrimSpace(found.Match) == "" {
		return nil, userErr("--promote %s: the area names no path in this repository, so a run of it has no code to raise (rigor.areas[].match)", area)
	}
	paths := []string{found.Match}
	canon, err := task.CanonicalRepo(repo)
	if err != nil {
		return nil, userErr("--promote %s: %v", area, err)
	}
	current := rigor.Resolve(rc, rigor.Request{Repo: canon, Paths: paths}).Level
	if current == "" {
		current = rigor.Default
	}
	want := rigor.Rigor(target)
	if want.Rank() < current.Rank() {
		return nil, userErr("--promote %s --mode %s: the area already resolves to %s, and a promote run raises an area — a target below the area's own level is refused, and a target equal to it is allowed (docs/rigor-modes/levels-spec.md §4)", area, want, current)
	}
	return &task.Promote{Area: found.Name, Paths: paths}, nil
}
