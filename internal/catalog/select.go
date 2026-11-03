// Package catalog holds the project image catalog's selection: which entry
// of a project's catalog a job runs, from the tags its repositories need
// (reconciliation §3.1, D1146). The algorithm is total and deterministic —
// two runs of the same inputs pick the same entry, whatever order the
// catalog holds its entries in — because the entry it picks reaches a job's
// image and a person has to be able to explain the choice.
//
// It reads the catalog type of internal/config and never writes one: the
// definitions are published by fugaro update (D1140) and built by the
// catalog build (PIC4).
package catalog

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
)

// DockerTag is the tag that claims a Docker daemon (D1149). It is one
// constant because three readers compare it: this package's candidate rule,
// the refusal it writes, and the launcher that turns the claim into the
// socket a run gets.
const DockerTag = "docker"

// Backend is the execution backend a job runs on. The docker tag is a claim
// the rule checks against it and not an image property (D1149): a backend
// with no daemon must refuse a docker-tagged image before the job starts,
// rather than let a job start and die on its first docker command.
type Backend string

const (
	// CloudRun is gen-2 Cloud Run: no Docker daemon at all (design v1.md
	// §7.1.1, F16).
	CloudRun Backend = "cloud-run"
	// Batch is the VM backend, whose container carries the host's Docker
	// socket (design compute-backend.md §2.2), so a docker-tagged image is
	// an ordinary candidate there.
	Batch Backend = "cloud-batch"
)

// hasDockerDaemon answers whether a backend can give a run a Docker daemon.
// Every backend but the VM is answered no, including a name this package
// does not know: the unknown case fails closed, because the failure it
// prevents is a job that starts and then cannot run docker.
func (b Backend) hasDockerDaemon() bool { return b == Batch }

// Repo is one repository's reference to the catalog (D1155): the entry it
// pins with image_name:, and the tags its needs: asks for. A repository that
// names no entry and needs no tag is the zero value, and it is what makes a
// job ask for the project's default.
type Repo struct {
	// Slug names the repository in the sentences a refusal writes.
	Slug string
	// Pin is the catalog entry the repository names, or "" when it names
	// none. A pin is not the algorithm: the named entry is used as it is.
	Pin string
	// Needs are the tags the repository's job needs.
	Needs []string
}

// Job is one selection's question: a job's repositories, the project's
// catalog, and the backend the job runs on.
//
// The rule is stated for a job over a set of repositories because the
// multi-repo job (D1154) will need it; today the set has one member (F12),
// so the union over the repositories is one repository's needs.
type Job struct {
	Catalog config.Catalog
	Repos   []Repo
	Backend Backend
}

// Choice is what the rule picked.
type Choice struct {
	// Entry is the catalog entry the job runs.
	Entry config.Entry
	// Pinned is true when a repository named the entry with image_name:
	// the rule did not choose it (D1155, D1146).
	Pinned bool
	// Warn is D1146's deploy-time warning, "" when there is nothing to
	// warn about: only a pin that does not cover the job's needs has one,
	// and the pin is used anyway.
	Warn string
}

// The two refusals of reconciliation §3.3. Both are raised where the job
// spec is built, before any job starts (F7), and both are conditions of the
// project's own configuration rather than of a run, which is why they are
// raised at deploy time and not by a running job.
var (
	// ErrNoCoveringEntry is the tags the job needs and no entry of the
	// catalog covers.
	ErrNoCoveringEntry = errors.New("no image in this project's catalog covers the tags this job needs")
	// ErrDockerOnCloudRun is the docker tag on a backend with no daemon.
	ErrDockerOnCloudRun = errors.New("this job's image is tagged docker, which needs a VM backend")
	// ErrUnknownEntry is a repository pinning a name the catalog does not
	// hold. It is not one of §3.3's two: it is a configuration mistake —
	// the name is grammar-checked where the file is read (PIC1) and only
	// the catalog can say whether it exists — and it names the entries
	// there are rather than the tags no entry covers.
	ErrUnknownEntry = errors.New("this job pins a catalog entry this project's catalog does not have")
)

// NoCoveringEntryError is §3.3's first message: the needed tags, what the
// catalog's images do cover, and the next move. The tags are printed in the
// order the repositories named them, so the message is deterministic and a
// person can read it against their own needs: line.
//
// entries is the whole catalog, not the candidate set: the person's fix is a
// catalog entry, so the near misses a reader needs to see are all of them. An
// entry the backend's docker rule left out is listed and marked as such
// (D1149), because it is a near miss for another reason than its tags — and a
// list a reader cannot trust is a list that has them adding an entry they
// already have.
func NoCoveringEntryError(needed []string, entries []config.Entry, backend Backend) error {
	return fmt.Errorf("%w: %s\nthe catalog's images cover: %s\nadd an entry that covers the missing tags (fugaro image add <name> --tags %s), or add a \"universal\" entry that covers every tag this project uses",
		ErrNoCoveringEntry, brackets(needed), coverList(entries, backend), strings.Join(needed, " "))
}

// DockerOnCloudRunError is §3.3's second message: the missing capability is
// the backend, and the next move is a backend or another image.
func DockerOnCloudRunError() error {
	return fmt.Errorf("%w: Cloud Run gen-2 has no Docker daemon\nrun this job on the Batch/VM backend, or choose an image that does not carry the docker tag", ErrDockerOnCloudRun)
}

// UnknownEntryError refuses a pin naming no entry of the catalog, and names
// the entries there are: a name that names nothing would otherwise be
// resolved by a job that runs something else.
func UnknownEntryError(name string, entries []config.Entry) error {
	if len(entries) == 0 {
		return fmt.Errorf("%w: %q, and this project publishes no image catalog", ErrUnknownEntry, name)
	}
	return fmt.Errorf("%w: %q; the catalog holds %s", ErrUnknownEntry, name, strings.Join(catalogNames(entries), ", "))
}

// brackets prints a tag list the way §3.3's message reads it.
func brackets(tags []string) string { return "[" + strings.Join(tags, " ") + "]" }

// coverList is what the catalog's entries cover, in catalog order: each
// entry's name and its tags. An entry with no tags reads as an empty list
// rather than as nothing, because it is still an entry the message is about.
//
// An entry the docker rule took out of this job's candidate set (D1149) is
// marked, so that every entry the message lists is a true near miss: without
// the mark, a catalog whose only node-carrying image is tagged docker is read
// as "nothing covers [node], and here are the images that do".
func coverList(entries []config.Entry, backend Backend) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		part := e.Name + " " + brackets(e.Tags)
		if !backend.hasDockerDaemon() && slices.Contains(e.Tags, DockerTag) {
			part += " (" + DockerTag + ": VM backend only)"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

// catalogNames is the entries' names, for a refusal that has to name them.
func catalogNames(entries []config.Entry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}

// Needed is the needed set N (reconciliation §3.1 step 2): the union of the
// tags of the repositories that do not pin, in the order they are first
// named, with duplicates collapsed. A repository that pins names no tags
// here: the choice below does not run for it.
func Needed(repos []Repo) []string {
	var out []string
	for _, r := range repos {
		if r.Pin != "" {
			continue
		}
		for _, tag := range r.Needs {
			if !slices.Contains(out, tag) {
				out = append(out, tag)
			}
		}
	}
	return out
}

// Covers reports whether tags holds every tag of needed (§3.1 step 4). Extra
// tags are allowed: a candidate covers a set it is a superset of.
func Covers(tags, needed []string) bool {
	for _, tag := range needed {
		if !slices.Contains(tags, tag) {
			return false
		}
	}
	return true
}

// Choose is the tag-cover rule (reconciliation §3.1): a pin is used as it
// is, an empty needed set takes the entry marked default, and otherwise the
// covering candidate with the fewest tags wins, then the default among the
// ties, then the lexicographically first name. No covering candidate is
// §3.3's refusal.
func Choose(j Job) (Choice, error) {
	needed := Needed(j.Repos)
	pins := distinctPins(j.Repos)
	if len(pins) > 1 {
		// One job runs one image, so a job whose repositories pin two
		// different entries has no answer. Today's job has one repository
		// (F12, D1154), so this is unreachable until a multi-repo job
		// exists; refusing it then is the honest answer, rather than
		// running an image half the job did not ask for.
		return Choice{}, fmt.Errorf("this job's repositories pin two different catalog entries (%s), and a job runs one image", strings.Join(pins, " and "))
	}
	if len(pins) == 1 {
		e, ok := entryNamed(j.Catalog, pins[0])
		if !ok {
			return Choice{}, UnknownEntryError(pins[0], j.Catalog.Entries)
		}
		if j.carriesDocker(e) {
			return Choice{}, DockerOnCloudRunError()
		}
		c := Choice{Entry: e, Pinned: true}
		// A pin's tags are checked only to warn (D1146): the pin is used
		// whatever it covers, and what it does not cover is everything
		// this job needs — the tags the other repositories need, and its
		// own repository's.
		if missing := missingTags(e.Tags, jobNeeds(j.Repos)); len(missing) > 0 {
			c.Warn = fmt.Sprintf("the catalog entry %s this job pins does not cover the tags it needs: missing %s", e.Name, brackets(missing))
		}
		return c, nil
	}

	// §3.1 step 1: on Cloud Run every docker-tagged entry is left out of
	// the candidate set; a VM job keeps the whole catalog.
	candidates := j.Catalog.Entries
	if !j.Backend.hasDockerDaemon() {
		candidates = slices.DeleteFunc(slices.Clone(candidates), func(e config.Entry) bool {
			return slices.Contains(e.Tags, DockerTag)
		})
	}
	if slices.Contains(needed, DockerTag) && !j.Backend.hasDockerDaemon() {
		// The needed set itself carries the tag, so no candidate can
		// cover it and the missing capability is the backend: §3.3's
		// second message, not the first. Checked before the coverage
		// below, which would otherwise report a missing tag the catalog
		// may well hold.
		return Choice{}, DockerOnCloudRunError()
	}
	// §3.1 step 3: an empty needed set takes the entry marked default
	// outright, and the tag counts are never compared. A catalog that
	// marks none leaves the order below, which is total: the refusal of
	// §3.3 names tags, and this job needs none.
	if len(needed) == 0 {
		if e, ok := Default(j.Catalog); ok {
			// The candidate set's docker rule does not rescue the
			// default: a catalog that marks a docker-tagged entry as
			// what "nothing" runs is refused here, where the job's
			// backend is known, rather than dropped quietly.
			if j.carriesDocker(e) {
				return Choice{}, DockerOnCloudRunError()
			}
			return Choice{Entry: e}, nil
		}
	}
	var covering []config.Entry
	for _, e := range candidates {
		if Covers(e.Tags, needed) {
			covering = append(covering, e)
		}
	}
	if len(covering) == 0 {
		// §3.1 step 6, and only for a non-empty needed set: a job that
		// needs nothing is never refused for want of a candidate.
		return Choice{}, NoCoveringEntryError(needed, j.Catalog.Entries, j.Backend)
	}
	slices.SortFunc(covering, func(a, b config.Entry) int {
		if c := len(a.Tags) - len(b.Tags); c != 0 {
			return c
		}
		if a.Default != b.Default {
			if a.Default {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return Choice{Entry: covering[0]}, nil
}

// Applies reports whether the project's catalog is what decides a workflow's
// job image at all. Three workflows are not the catalog's: one that keeps its
// own image definition (image: or dockerfile:) runs the image the project
// builds for that repository and never a catalog entry (D1155); a clone
// workflow runs the installation's base image; and a shared workflow's image
// is a shared dependency-layer digest no catalog entry produces. A repository
// that names no entry and needs no tag is the catalog's only when the project
// marks a default — "nothing: it uses the project default" is the spec's rule
// (§3.1 step 3) — which is also what leaves an installation whose catalog is
// not yet curated exactly as it was (F1).
//
// Both halves of a selection ask this: the deploy-time job spec, which fixes
// the job's image (F7), and the launcher, which judges the image again against
// the backend it picks (D1149). A workflow one of them judged by the catalog
// and the other did not would be refused for an entry it does not run, or
// asked for a Docker socket it never needed, so the predicate lives here and
// not twice.
func Applies(c config.Catalog, w config.Workflow) bool {
	if w.Checkout == config.CheckoutClone || w.Checkout == config.CheckoutShared {
		return false
	}
	if !w.Image.IsZero() || w.Dockerfile != "" {
		return false
	}
	if w.ImageName == "" && len(w.Needs) == 0 {
		_, ok := Default(c)
		return ok
	}
	return true
}

// Default is the entry the catalog marks as its default, and whether it
// marks one: PIC1's rule allows one entry or none. It is what a repository
// that names nothing runs (§3.1 step 3), and what a caller asks before it
// offers such a repository to the rule at all.
func Default(c config.Catalog) (config.Entry, bool) {
	for _, e := range c.Entries {
		if e.Default {
			return e, true
		}
	}
	return config.Entry{}, false
}

// carriesDocker reports whether a pinned entry claims a Docker daemon on a
// backend that has none. The rule is one rule everywhere (D1149): a pin
// short-circuits the choice and not the backend's capability, because a
// pinned docker-tagged image on Cloud Run starts and then dies on its first
// docker command — the fail-after-start the spec forbids.
func (j Job) carriesDocker(e config.Entry) bool {
	return !j.Backend.hasDockerDaemon() && slices.Contains(e.Tags, DockerTag)
}

// distinctPins are the entries the job's repositories pin, in repository
// order and without duplicates.
func distinctPins(repos []Repo) []string {
	var out []string
	for _, r := range repos {
		if r.Pin != "" && !slices.Contains(out, r.Pin) {
			out = append(out, r.Pin)
		}
	}
	return out
}

// jobNeeds is every tag the job needs, from the repositories that name them,
// pinned ones included: a pin is checked against the whole job's needs, and
// not only against the repositories the rule chooses for.
func jobNeeds(repos []Repo) []string {
	var out []string
	for _, r := range repos {
		for _, tag := range r.Needs {
			if !slices.Contains(out, tag) {
				out = append(out, tag)
			}
		}
	}
	return out
}

// entryNamed is the catalog's entry with this name, and whether it has one.
func entryNamed(c config.Catalog, name string) (config.Entry, bool) {
	for _, e := range c.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return config.Entry{}, false
}

// missingTags are the tags of needed that tags does not hold, in needed's
// order.
func missingTags(tags, needed []string) []string {
	var out []string
	for _, tag := range needed {
		if !slices.Contains(tags, tag) {
			out = append(out, tag)
		}
	}
	return out
}
