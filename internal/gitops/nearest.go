package gitops

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// BranchTip is one of origin's branches as the checkout tracks it: the
// commit its remote-tracking ref names here, and how many commits the head
// it was measured against has beyond it.
type BranchTip struct {
	Name, SHA string
	Ahead     int
}

// NearestBranches finds the branches head's history grew from: origin's
// branches whose remote-tracking tip in this checkout is an ancestor of head
// (a full commit SHA), keeping only the nearest of them — a tip that another
// candidate's tip contains is always further from head, so it is dropped
// (`git merge-base --independent`) — each with the commits head has beyond
// it. The result is sorted by that count, then by name; it is empty when no
// tracked branch is an ancestor.
//
// Only remote-tracking refs are read, never a tip fetched for the purpose:
// every commit compared is one the checkout already has, so nothing here
// makes a blobless clone fetch history lazily. A tracking ref whose branch
// origin no longer has is left out (ls-remote), and so is every name keep
// refuses (nil keeps all); keep is applied before the pruning, so a refused
// branch never hides an ancestor of its own.
func (r *Repo) NearestBranches(ctx context.Context, head string, keep func(name string) bool) ([]BranchTip, error) {
	if !IsFullSHA(head) {
		return nil, fmt.Errorf("refusing to measure against %q: not a full commit SHA", head)
	}
	remote, err := r.git(ctx, "ls-remote", "--heads", "origin")
	if err != nil {
		return nil, fmt.Errorf("listing origin's branches: %w", err)
	}
	onOrigin := map[string]bool{}
	for _, line := range strings.Split(remote, "\n") {
		if _, ref, ok := strings.Cut(line, "\t"); ok {
			if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
				onOrigin[name] = true
			}
		}
	}
	tracked, err := r.git(ctx, "for-each-ref", "--format=%(objectname) %(refname)", "refs/remotes/origin/")
	if err != nil {
		return nil, fmt.Errorf("listing the remote-tracking branches: %w", err)
	}
	names := map[string][]string{}
	for _, line := range strings.Split(tracked, "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		if !ok || !IsFullSHA(sha) {
			continue
		}
		name := strings.TrimPrefix(ref, "refs/remotes/origin/")
		if !onOrigin[name] || (keep != nil && !keep(name)) {
			continue
		}
		names[sha] = append(names[sha], name)
	}
	var ancestors []string
	for _, sha := range slices.Sorted(maps.Keys(names)) {
		ok, err := r.isAncestor(ctx, sha, head)
		if err != nil {
			return nil, fmt.Errorf("checking whether %s is in the branch's history: %w", sha, err)
		}
		if ok {
			ancestors = append(ancestors, sha)
		}
	}
	if len(ancestors) == 0 {
		return nil, nil
	}
	out, err := r.git(ctx, append([]string{"merge-base", "--independent", "--end-of-options"}, ancestors...)...)
	if err != nil {
		return nil, fmt.Errorf("finding the nearest of the branches: %w", err)
	}
	var tips []BranchTip
	for _, sha := range strings.Fields(out) {
		n, err := r.git(ctx, "rev-list", "--count", "--end-of-options", sha+".."+head)
		if err != nil {
			return nil, fmt.Errorf("counting the commits beyond %s: %w", sha, err)
		}
		ahead, err := strconv.Atoi(n)
		if err != nil {
			return nil, fmt.Errorf("counting the commits beyond %s: %w", sha, err)
		}
		for _, name := range names[sha] {
			tips = append(tips, BranchTip{Name: name, SHA: sha, Ahead: ahead})
		}
	}
	slices.SortFunc(tips, func(a, b BranchTip) int {
		return cmp.Or(cmp.Compare(a.Ahead, b.Ahead), cmp.Compare(a.Name, b.Name))
	})
	return tips, nil
}
