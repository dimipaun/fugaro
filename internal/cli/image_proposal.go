package cli

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is §5's proposal: what a repository reads when its resolved
// dependency set is one the image it is on does not hold. A divergence is
// never forked silently (T6(c), resolved 2026-10-29), so the refresh names the
// facts and offers the three choices — fork, adopt <repository>, pin — and
// builds nothing until one is named.
//
// The choice is never asked as a question: this CLI asks nothing (the
// no-prompt policy, docs/no-prompt-policy/), so the proposal is printed, the
// run stops with a user error, and `--on-divergence` is the whole of how a
// person answers it — at a terminal, in a script, or in a run of the converge.
// A coding agent's session is refused the flag by the command's own gate,
// which is what keeps this from being the way an agent forks an image.
//
// The design (§5) has the choice read from a terminal where there is one, and
// printed as JSON under --json. Neither is built here: fugaro image refresh
// has no prompt and no --json, so the printed proposal plus the flag is the
// whole of the interface, and the run that cannot answer stops exactly where
// the design's non-interactive run does.

// onDivergence is --on-divergence's parsed choice: the answer to a divergence,
// named rather than asked. The zero value names none, which is what stops a
// run at the proposal.
type onDivergence struct {
	fork bool
	pin  bool
	// adopt is the repository whose image this repository would move onto,
	// canonicalised to owner/name.
	adopt string
}

// choice reports whether the flag named a choice at all.
func (c onDivergence) choice() bool { return c.fork || c.pin || c.adopt != "" }

// onDivergenceFlag is the flag's name as every message this file writes spells
// it: the refusal names it, and the proposal prints the three choices with it.
const onDivergenceFlag = "--on-divergence"

// parseOnDivergence reads --on-divergence's value. The three choices are §5's
// own, spelled there: `fork`, `adopt <repository>` and `pin`, the second
// written as adopt=<repository> on a command line.
func parseOnDivergence(v string) (onDivergence, error) {
	switch {
	case v == "":
		return onDivergence{}, nil
	case v == "fork":
		return onDivergence{fork: true}, nil
	case v == "pin":
		return onDivergence{pin: true}, nil
	case strings.HasPrefix(v, "adopt="):
		repo := strings.TrimPrefix(v, "adopt=")
		if repo == "" {
			return onDivergence{}, userErr("--on-divergence=adopt= names no repository: write --on-divergence=adopt=owner/name")
		}
		canonical, err := task.CanonicalRepo(repo)
		if err != nil {
			return onDivergence{}, userErr("--on-divergence=adopt=%s: %v", pluginwire.Printable(repo), err)
		}
		return onDivergence{adopt: canonical}, nil
	}
	return onDivergence{}, userErr("%s=%s is not a choice: write %s=fork, %s=adopt=owner/name or %s=pin",
		onDivergenceFlag, pluginwire.Printable(v), onDivergenceFlag, onDivergenceFlag, onDivergenceFlag)
}

// adoptCandidate is one repository whose image holds this repository's whole
// set (§4), with the packages it carries on top of it: the count §5's proposal
// names, so a person choosing adopt sees what the image would bring.
type adoptCandidate struct {
	Repo  string
	Key   string
	Image string
	Extra int
}

// divergence is a repository whose image does not hold the set its checkout
// resolves (§5): what it is on, what it resolved, and the images it could
// adopt instead.
type divergence struct {
	repo     string
	workflow string
	// resolved is the set the checkout resolves and key its normalised form,
	// neither of which is on any image yet.
	resolved imagecheck.Resolved
	key      string
	// on is the image the repository's job is on, and onKey its key.
	on    imagecheck.LockedImage
	onKey string
	// candidates are the lock's other images that hold the whole set, in the
	// order they are printed.
	candidates []adoptCandidate
}

// diff is §5's summary of what changed: the packages the image the repository
// is on does not hold, the ones it holds that the set no longer names, and the
// ones both name at different versions or integrities. It counts rather than
// lists, which is what the decision needs — the facts are in the lock and in
// the checkout for anyone who wants them.
func (d *divergence) diff() (added, removed, changed int) {
	have := map[string]string{}
	for _, f := range d.on.Packages {
		if name, version, integrity, ok := imagecheck.SplitFact(f); ok {
			have[name] = version + "\x00" + integrity
		}
	}
	want := map[string]string{}
	for _, f := range d.resolved.Packages {
		if name, version, integrity, ok := imagecheck.SplitFact(f); ok {
			want[name] = version + "\x00" + integrity
		}
	}
	for name, v := range want {
		switch got, ok := have[name]; {
		case !ok:
			added++
		case got != v:
			changed++
		}
	}
	for name := range have {
		if _, ok := want[name]; !ok {
			removed++
		}
	}
	return added, removed, changed
}

// print writes the proposal: the two keys, the diff by count, the candidate
// images and the three choices. It is what a run that cannot answer has
// printed for it, and what a run at a terminal reads before it writes the
// flag.
func (d *divergence) print(w io.Writer) {
	added, removed, changed := d.diff()
	fmt.Fprintf(w, "  %s: its image does not hold the set this checkout resolves\n", d.workflow)
	fmt.Fprintf(w, "    the image it is on: key %s, %s\n", shortKey(d.onKey), d.on.Image)
	fmt.Fprintf(w, "    the set it resolves: key %s, %d packages, %d tools\n", shortKey(d.key), len(d.resolved.Packages), len(d.resolved.Tools))
	fmt.Fprintf(w, "    packages: %d added, %d removed, %d at another version\n", added, removed, changed)
	if len(d.candidates) == 0 {
		fmt.Fprintln(w, "    no other repository's image holds this set in full")
	} else {
		fmt.Fprintln(w, "    an image that holds it in full, with packages this repository does not name:")
		for _, c := range d.candidates {
			fmt.Fprintf(w, "      %s (key %s, +%d packages)\n", pluginwire.Printable(c.Repo), shortKey(c.Key), c.Extra)
		}
	}
	fmt.Fprintf(w, "    nothing is built and no job moves until one of the three choices is named: %s=fork builds a new image for this key and moves only this repository's job; %s=adopt=<repository> moves this repository onto an image listed above; %s=pin keeps the image it is on and changes nothing\n",
		onDivergenceFlag, onDivergenceFlag, onDivergenceFlag)
}

// unanswered is the refusal a divergence that was named no choice gets. It is
// a user error (exit 1), as §5's exit codes say, and it names the flag: the
// proposal above it carries the facts.
func (d *divergence) unanswered() error {
	return userErr("%s workflow %s diverges and nothing is built until %s names a choice: write %s=fork, %s=adopt=<repository> or %s=pin",
		pluginwire.Printable(d.repo), d.workflow, onDivergenceFlag, onDivergenceFlag, onDivergenceFlag, onDivergenceFlag)
}

// shortKey is a key as a message names it: the first 12 hex characters, which
// is what the image's own name carries (§2.6). A shorter key stands as it is,
// so a key this never wrote is not cut into a lie.
func shortKey(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:12]
}

// candidatesFor are the lock's images, other than the one the repository is
// on, that hold the whole of resolved (§4). The repository each names is the
// one it is known by — the first of its repositories — and an entry nothing is
// on any more is not a candidate: adopting an image no job runs would move
// this repository onto one nothing has ever smoke-tested.
func candidatesFor(lock *imagecheck.Lock, repo string, resolved imagecheck.Resolved, skip string) []adoptCandidate {
	if lock == nil {
		return nil
	}
	var out []adoptCandidate
	for _, key := range slices.Sorted(maps.Keys(lock.Images)) {
		img := lock.Images[key]
		if key == skip || !img.Holds(resolved) || len(img.Repositories) == 0 {
			continue
		}
		owner := ""
		for _, r := range img.Repositories {
			if r != repo {
				owner = r
				break
			}
		}
		if owner == "" {
			continue
		}
		out = append(out, adoptCandidate{Repo: owner, Key: key, Image: img.Image, Extra: extraPackages(img, resolved)})
	}
	slices.SortFunc(out, func(a, b adoptCandidate) int {
		if a.Extra != b.Extra {
			return a.Extra - b.Extra
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out
}

// extraPackages is how many packages an image holds that the set does not
// name: §4's cost, stated as the count a person choosing adopt reads.
func extraPackages(img imagecheck.LockedImage, resolved imagecheck.Resolved) int {
	named := map[string]struct{}{}
	for _, f := range resolved.Packages {
		named[f] = struct{}{}
	}
	n := 0
	for _, f := range img.Packages {
		if _, ok := named[f]; !ok {
			n++
		}
	}
	return n
}
