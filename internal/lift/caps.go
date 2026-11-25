package lift

import (
	"fmt"
	"slices"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/task"
)

// PlanCaps is B18's planner (RC4): a repository's own cap node that stands
// above the project's ceiling is lowered to it, in place, and named before any
// write (spec §5.6).
//
// The ceiling is RC1's — budget.Caps.RepoCeiling, the global cap where it is
// set and the default repository cap otherwise, per dimension — so a node this
// item lowers is exactly the node budget.Caps clamps at read and the database
// rules clamp at the lease (RC2). Each dimension falls back on its own, and
// each is judged on its own: a node above its ceiling on the daily figure and
// under it on the per-run one keeps the per-run figure, which is the
// repository's own value and not the default's to replace.
//
// What a line says about the runs depends on which figure the ceiling is, and
// the two cases are the whole of why this item is safe with runs in flight
// (spec §7): where the ceiling is the global cap, a lease already compares
// against it — Caps.PerRun takes the smaller of the node and the global — so
// lowering the node changes nothing a run meets; where it is the default
// repository cap, the node was the figure a lease compared against, and a run
// in flight meets the lower one at its next lease.
//
// It acts only on a repository this run read (D3306) and has not already
// migrated (D3305). A node of a repository the reader could not read, or of
// one this project cannot list at all, is named and left where it stands: the
// clamp at read and at the lease is what holds it, and recording such a
// repository as migrated would keep the file values the update that can read
// it has to move from ever being moved. A node of a repository already
// migrated is named for the same reason — nothing is written twice.
//
// It answers no error: the node is the whole of its input, and every node it
// leaves behind is a line.
func PlanCaps(p Project, repos []Repo) (Change, error) {
	ceil := budget.Caps{Global: p.Caps.Global, Defaults: p.Caps.Defaults}.RepoCeiling()
	read := readReposBySlug(repos)
	var out Change
	for _, slug := range sortedCapSlugs(p.Caps.Repos) {
		node := p.Caps.Repos[slug]
		lower, over := lowerCaps(node, ceil, p.Caps.Global)
		if len(over) == 0 {
			continue
		}
		repo := capRepoName(slug, node, read)
		switch {
		case read[slug] == "":
			out.Lines = append(out.Lines, capLeftLines(repo, slug, over, "this run did not read the repository")...)
		case p.Migrated[slug]:
			out.Lines = append(out.Lines, capLeftLines(repo, slug, over, "the repository is already migrated")...)
		default:
			out.DBWrites = append(out.DBWrites, DBWrite{Path: budget.PathCapsRepo(slug), Value: lower})
			out.Lines = append(out.Lines, capLowerLines(repo, slug, over)...)
			out.Records = append(out.Records, slug)
		}
	}
	return out, nil
}

// capOver is one dimension of a repository's node that stands above its
// ceiling: the figure the node holds, the ceiling it is lowered to, and which
// cap that ceiling is — the project's global one, which a lease already
// compares against, or the default repository cap, which it does not.
type capOver struct {
	dim    string
	from   budget.Micros
	to     budget.Micros
	global bool
}

// lowerCaps answers the node with every dimension above its ceiling lowered to
// it, and one capOver per dimension it moved.
func lowerCaps(node budget.RepoCaps, ceil budget.RepoCeiling, global *budget.GlobalCaps) (budget.RepoCaps, []capOver) {
	lower := node
	var over []capOver
	if aboveCeiling(node.DailyMicros, ceil.DailyMicros) {
		lower.DailyMicros = capMicrosOf(*ceil.DailyMicros)
		over = append(over, capOver{dim: "daily", from: *node.DailyMicros, to: *ceil.DailyMicros,
			global: global != nil && global.DailyMicros != nil})
	}
	if aboveCeiling(node.PerRunMicros, ceil.PerRunMicros) {
		lower.PerRunMicros = capMicrosOf(*ceil.PerRunMicros)
		over = append(over, capOver{dim: "per-run", from: *node.PerRunMicros, to: *ceil.PerRunMicros,
			global: global != nil && global.PerRunMicros != nil})
	}
	return lower, over
}

// aboveCeiling reports whether a node's figure stands above its ceiling. A
// dimension with no ceiling — neither a global nor a default one — has no
// figure above it, and the node stands (spec §5.6).
func aboveCeiling(node, ceil *budget.Micros) bool {
	return node != nil && ceil != nil && *node > *ceil
}

// capMicrosOf is one cap figure as a node carries it. The copy is this node's
// own: two nodes lowered to one ceiling are two values, not one shared.
func capMicrosOf(m budget.Micros) *budget.Micros {
	v := m
	return &v
}

// capLowerLines are the lines for one node this item lowers, one per dimension
// and each naming the figure that decided it.
func capLowerLines(repo, slug string, over []capOver) []string {
	out := make([]string, 0, len(over))
	for _, o := range over {
		line := fmt.Sprintf("%s: %s %s %s -> %s", repo, budget.PathCapsRepo(slug), o.dim, capMoney(o.from), capMoney(o.to))
		if o.global {
			line += " (the global cap; no run's limit changes)"
		} else {
			line += " (the default repository cap; a run in flight meets it at its next lease)"
		}
		out = append(out, line)
	}
	return out
}

// capLeftLines are the lines for one node this item leaves where it stands,
// each with the reason it is left — a repository this run did not read, or one
// it has already migrated — so the person upgrading knows the node is above
// the ceiling and why nothing was written for it.
func capLeftLines(repo, slug string, over []capOver, why string) []string {
	out := make([]string, 0, len(over))
	for _, o := range over {
		out = append(out, fmt.Sprintf("%s: %s %s %s stays above the ceiling %s; %s",
			repo, budget.PathCapsRepo(slug), o.dim, capMoney(o.from), capMoney(o.to), why))
	}
	return out
}

// capMoney is one cap figure as a person reads it, the way every budget
// command prints money.
func capMoney(m budget.Micros) string { return fmt.Sprintf("$%.2f", m.USD()) }

// capRepoName is the repository a cap node belongs to: the name this run's own
// repository list carries for its slug, else the name the node itself holds,
// else the slug (the node a hand edit left without its name).
func capRepoName(slug string, node budget.RepoCaps, read map[string]string) string {
	if name := read[slug]; name != "" {
		return name
	}
	if node.Repo != "" {
		return node.Repo
	}
	return slug
}

// readReposBySlug is the repositories this run read, by slug: the list M1's
// reader answered, minus every repository it could not read and every one whose
// provider and name are not a repository this can key. A repository it could
// not read is already named by update's own line (D3306); a planner acts on
// neither it nor one no listing carries.
func readReposBySlug(repos []Repo) map[string]string {
	out := make(map[string]string, len(repos))
	for _, r := range repos {
		if r.Repo == "" || r.Unreadable != "" {
			continue
		}
		slug, err := task.Slug(r.Provider, r.Repo)
		if err != nil {
			continue
		}
		out[slug] = r.Repo
	}
	return out
}

// sortedCapSlugs orders the cap nodes by slug, so the lines, the writes and the
// records do not move between two runs of update over the same project.
func sortedCapSlugs(nodes map[string]budget.RepoCaps) []string {
	out := make([]string, 0, len(nodes))
	for slug := range nodes {
		out = append(out, slug)
	}
	slices.Sort(out)
	return out
}
