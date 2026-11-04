// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package coordinator holds the coordinator's deterministic half: the
// project view, computed, with no store, no service, no MCP front door and
// no model (docs/coordinator/implementation-plan.md, parts P1 and P2;
// reconciliation Q13 and Q12, answered (A) on 2026-10-19). It is what a
// coordinator *shows*, as data.
//
// Join is a pure function over the records it is handed, and Project (part
// P2) is the read that feeds it: one repository's pull requests, through a
// provider the caller opened on the coordinator's own read-only installation
// token — minted per repository and cached for the token's life (D193's
// answer). The readers behind both — a gitprov.Provider and the runs bucket —
// are opened by the CLI command that renders the view
// (ee/coordinator/cli/coordinator.go), which is where the plan puts them, so the
// view opens nothing itself.
//
// The one thing here that writes is state.go, the subtree D195 names and the
// ETag CAS every write of the coordinator's state goes through (P3); it is
// separate from the view because the view is computed and never stored, and a
// reader of one is not a reader of the other.
//
// What the view is about is *declarations*, never identities: the launcher a
// run records is the string it wrote about itself (reconciliation finding
// #11), and the files behind a pull request are the paths a run's own record
// says it changed. The view is advisory, which is why it may hold both.
package coordinator

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// ReleaseBranchPrefix is the repository's own release-branch spelling:
// docs/release.md's script creates release/vX.Y.Z, so a branch carrying this
// prefix is the one place the view can read a target release out of a branch
// name.
const ReleaseBranchPrefix = "release/v"

// PRStates as gitprov spells them, for the one comparison the view makes:
// a pull request is in flight while its state is open.
const (
	StateOpen   = "open"
	StateMerged = "merged"
	StateClosed = "closed"
)

// PullRequest is one pull request as the host shows it, reduced to what the
// view needs.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url,omitempty"`
	// Title is untrusted text written by whoever opened the pull request. It
	// is carried as data and is never a decision: a renderer sanitises it
	// before it reaches a line or a cell (safetext), the way every other
	// surface that draws a pull request's own words does.
	Title string `json:"title,omitempty"`
	// Branch is the source branch the host reports.
	Branch string `json:"branch,omitempty"`
	Draft  bool   `json:"draft,omitempty"`
	// State is the host's own answer ("open", "merged", "closed"). It is ""
	// when the host did not answer — a pull request a run names and the
	// reader did not return — and the record then falls back to whether the
	// runs behind it have finished.
	State string `json:"state,omitempty"`
}

// PRDocument is one pull request as the host has it, whole: the shape
// `fugaro coordinator show` prints (coordinator v2 C3, D816), and the read
// behind the console's inline description.
//
// It is separate from PullRequest, which is the view's own reduced record of
// the same thing, because the two answer different questions and the
// difference is the body. The view drops it by design — a body is unbounded
// text written by whoever opened the pull request, and one per record would
// ride in the document every reader of the view fetches (reconciliation F3,
// D816's (a)) — so the shape that carries it is a read of its own, and a
// second transport for it would be a second shape.
//
// Every field is the host's own answer and nothing is derived from it: the
// document says what the host said, and a field the host did not answer is
// absent rather than guessed. `body` is untrusted text like the view's Title:
// a renderer sanitises it before it reaches a line or a cell (safetext), and
// nothing in it is ever a link, a path or a decision.
type PRDocument struct {
	Number int `json:"number"`
	// URL is the host's own address for the pull request. It is carried as
	// data and is never drawn as a link by the surfaces that print this
	// document, for the reason PullRequest.URL's own comment gives.
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
	// Body is the description the host holds, as the host holds it. Untrusted
	// text: it is whatever whoever opened the pull request wrote, and the only
	// thing this read does with it is carry it.
	Body string `json:"body,omitempty"`
	// State is the host's own word ("open", "merged", "closed"), never a
	// derivation of the runs behind the pull request.
	State string `json:"state,omitempty"`
	// Draft is whether the host says the pull request is a draft, which is the
	// adapter's answer too (a title carrying DraftPrefix where the host has no
	// real drafts).
	Draft bool `json:"draft,omitempty"`
	// Branch is the source branch the host reports and Base the branch it
	// targets now, which a retargeted pull request makes different from the
	// one it was opened against.
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	// Author is the host's own identity for whoever opened it: GitHub's
	// numeric user id, Bitbucket's account_id. It is an account id and never a
	// display name, and nothing is authorised by it.
	Author string `json:"author,omitempty"`
}

// Run is one run behind a pull request, as the run store records it.
type Run struct {
	ID string `json:"id"`
	// PR is the pull request the run is on, 0 when it is on none: a run
	// that has not opened one yet, or one that produced none at all.
	PR int `json:"pr,omitempty"`
	// Status and Outcome are the run record's own words ("running",
	// "succeeded", ...). Status is "" for a run whose object the store does
	// not hold.
	Status  string `json:"status,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	// Terminal is whether the run has finished. A run with no readable
	// record is not terminal, which is the safe direction: it is still in
	// flight, and the view says so rather than guessing.
	Terminal bool `json:"terminal,omitempty"`
	// Branch is the run's own branch, and BaseBranch the branch its pull
	// request targets (the run record's base_branch). Either may be "".
	Branch     string `json:"branch,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	// DeclaredBy is the launcher the run's own launch record wrote at
	// launch time — the local config's user:, else git's user.email, which a
	// launcher writes about itself and nothing verifies (reconciliation
	// finding #11, launch.json's launched_by). It is carried as a
	// DECLARATION and is never an identity: no field of this view promises
	// that the string names the person, the account or the machine a run
	// actually ran as, and nothing may authorise or admit anything by it.
	DeclaredBy string `json:"declared_by,omitempty"`
	// Files are the paths the run's record says it changed, and
	// FilesTruncated is the record's own admission that the set it stored is
	// short of the set it changed (runstore.ChangedFiles). A truncated set
	// is not a whole one, and the view says so rather than presenting it as
	// one. An empty list is the answer "no path", never a missing answer:
	// the store's record tells "recorded no set" from "recorded an empty
	// one" and the view does not, because both answer the one question the
	// view asks of a run's paths — may two records collide on one? — with
	// no.
	Files          []string `json:"files"`
	FilesTruncated bool     `json:"files_truncated,omitempty"`
}

// Record is one unit of work the coordinator holds a view of: a pull request
// and the runs behind it, or — for work that has not reached the host — the
// in-flight runs on their own.
type Record struct {
	// PR is the pull request this record is about, nil for an in-flight run
	// that has opened none.
	PR *PullRequest `json:"pr,omitempty"`
	// Runs are the runs behind it, newest first. An empty list is a fact
	// this view states and never drops: a pull request the store holds no
	// run for is still a pull request somebody has open, and the view lists
	// it as having no run behind it.
	Runs []Run `json:"runs"`
	// Branch is the branch the record declares: the pull request's source
	// branch when the host answered, else the newest run's own branch.
	Branch string `json:"branch,omitempty"`
	// Release is the target release the record declares, read from a branch
	// that names one (ReleaseOf). "" is a record that declares none, which
	// is the ordinary case and not a gap: most work targets no release.
	Release string `json:"release,omitempty"`
	// InFlight is whether the work is still in flight, and it is the only
	// thing a collision is computed over.
	InFlight bool `json:"in_flight"`
	// Files are every path the runs behind the record say they changed, and
	// Services every service those paths belong to (ServiceOf).
	Files    []string `json:"files"`
	Services []string `json:"services"`
}

// Label names a record in one word for a reader: its pull request's number,
// or the run's ID for work that has no pull request yet. It is the view's own
// name for the record and never the host's text.
func (r Record) Label() string {
	switch {
	case r.PR != nil && r.PR.Number > 0:
		return fmt.Sprintf("#%d", r.PR.Number)
	case len(r.Runs) > 0:
		return r.Runs[0].ID
	}
	return "-"
}

// Collision is two records in flight that touch the same file or the same
// service. It is what the coordinator exists to say early — *"someone is
// already changing that API contract"* — and it is advice: the view reports
// it and never blocks, reorders or refuses anything (spec §3, Q10(C)
// refused).
//
// One collision is one *pair* of records: two pull requests that share five
// files collide once, and A and B list them.
type Collision struct {
	// A and B name the two records, A first (Record.Label, in the order the
	// records are listed).
	A string `json:"a"`
	B string `json:"b"`
	// Files are the paths both records touch, and Services the services
	// both touch, each sorted. At least one of the two is non-empty: a pair
	// that shares neither is not a collision.
	Files    []string `json:"files"`
	Services []string `json:"services"`
}

// View is one repository's record: the work it holds and where that work
// collides.
type View struct {
	// Repo is the repository these records are about, "owner/name".
	Repo string `json:"repo"`
	// Records is every record the join found, in a fixed order: work still
	// in flight first, then by pull request number, then by run ID. Sorting
	// is the view's, not the reader's: the same inputs give the same view,
	// in the same order, whichever reader produced them.
	Records []Record `json:"records"`
	// Collisions is every pair of in-flight records that share a file or a
	// service, in Records' own order rather than sorted by label: by the
	// earlier of the two records, then by the later (collisions() below).
	// The two sections of the view are then read the same way, and a
	// reader can predict the order without running the code.
	Collisions []Collision `json:"collisions"`
	// Unread, Omitted and OmittedCollisions are written by the sweep alone
	// (sweep.go) and are absent from a view status computes. Unread is every
	// pull request whose host state this view could not read on this pass,
	// sorted by number, each with why and with whether the state shown is
	// the last one a sweep did read. Omitted is how many records the
	// document's bound left out and OmittedCollisions how many collisions
	// (MaxViewRecords, MaxViewCollisions): the records dropped are the
	// finished ones whose newest run is oldest, never one in flight while a
	// finished one is kept.
	Unread            []Unread `json:"unread,omitempty"`
	Omitted           int      `json:"omitted,omitempty"`
	OmittedCollisions int      `json:"omitted_collisions,omitempty"`
}

// InFlight is how many of the view's records are still in flight.
func (v View) InFlight() int {
	n := 0
	for _, r := range v.Records {
		if r.InFlight {
			n++
		}
	}
	return n
}

// ReleaseOf is the release a branch name declares: "release/v0.11.0"
// declares "0.11.0", and any other branch name declares nothing.
//
// The prefix is the repository's own release-branch spelling
// (docs/release.md). The rule stops there on purpose: a branch the view
// cannot read a release out of — "main", a run's own fugaro/… branch, a
// name with a further "/" in it — declares none, and a record that declares
// none is reported that way rather than guessed at.
func ReleaseOf(branch string) string {
	rel, ok := strings.CutPrefix(branch, ReleaseBranchPrefix)
	if !ok || rel == "" || strings.Contains(rel, "/") {
		return ""
	}
	return rel
}

// ServiceOf is the service a path belongs to, and the rule is about the
// path's *shape*: it reads two segments where the tree under the first one
// holds more than one unit, and one everywhere else.
//
//   - "internal", "cmd" and "pkg" are the three trees this repository
//     partitions that way — internal/cli/exec.go is the CLI, and
//     internal/coordinator/view.go is the coordinator, not both "internal" —
//     so a path under one of them belongs to its first two segments:
//     "internal/coordinator", "cmd/fugaro", "pkg/budget".
//   - Every other path belongs to its first segment: "schemas" for
//     schemas/result.schema.json, "website" for website/content/cli-reference.md.
//   - A path with no separator belongs to no service — it sits in the
//     repository's own root — and answers "", as "" does for an empty path and
//     for one that starts with a separator.
//
// This is the whole of the rule, and it is a *derivation*: the view's files
// come from a run's own record of the paths it changed
// (runstore.ChangedFiles), and nothing in the tree declares a service yet.
// The intent records P4 adds are where a declared service arrives; until
// then this is what turns a set of paths into the "who is changing which
// service" the spec's §10 asks for.
//
// The two-segment half is what keeps the answer useful at this repository's
// size: with the first segment alone, every path under internal/ — which is
// most of the tree — collapsed into one "service", and two pull requests
// working in different packages were reported as colliding on it. It is a
// naming rule and not a walk: the view reads no working copy, and a
// two-segment path whose second segment is a file ("internal/notes.go") is
// answered with its own two segments rather than with "internal", so that
// two such files are not merged into one unit by a rule that never looked at
// the tree.
func ServiceOf(path string) string {
	i := strings.IndexByte(path, '/')
	if i <= 0 {
		return ""
	}
	first := path[:i]
	if first != "internal" && first != "cmd" && first != "pkg" {
		return first
	}
	rest := path[i+1:]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return path[:i+1+j]
	}
	if rest == "" {
		return first
	}
	return path
}

// Numbers is the pull requests the runs name, each once, sorted.
//
// gitprov.Provider reads one pull request by number and has no call that
// lists a repository's (reconciliation finding #10), so this is the whole of
// what a reader can be asked for however it reads them: each run's task or
// record names the pull request it is on, and a pull request no run names is
// never asked for. It is a function of the runs alone — the same runs ask for
// the same numbers in the same order — so a reader that caches, and the
// sweep that re-reads what has not changed, ask the same question twice.
func Numbers(runs []Run) []int {
	out := make([]int, 0, len(runs))
	for _, run := range runs {
		if run.PR != 0 && !slices.Contains(out, run.PR) {
			out = append(out, run.PR)
		}
	}
	slices.Sort(out)
	return out
}

// Join builds the view of one repository from its pull requests and its runs.
// It reads nothing and writes nothing: the caller has already read both.
//
// The join's rules, in full, because each one is a decision a reader would
// otherwise have to guess at:
//
//   - Every pull request the reader returned is one record. One that no run
//     names is listed with no run behind it, never dropped: it is work
//     somebody has open, and the view's job is to say so.
//   - Every run is attached to the pull request it names. A run that names
//     none is its own record while it is still in flight — that is "who is
//     working on what" before any code exists, which is the spec's §6 intent
//     signal in its crudest form — and is left out once it has finished,
//     because finished work with no pull request is not work in flight.
//   - A run naming a pull request the reader did not return still names one:
//     the view adds the number with the host's state unknown, so that a
//     reader which returned fewer pull requests than the store knows about
//     loses no work.
//   - Two records in flight collide when they share a changed path or a
//     service. Records that are not in flight collide with nothing: a merged
//     pull request is history, and history does not collide with anything.
func Join(repo string, prs []PullRequest, runs []Run) View {
	v := View{Repo: repo, Records: []Record{}, Collisions: []Collision{}}
	var recs []*Record
	byNumber := map[int]*Record{}

	// The pull requests first, so that a run never invents a record the
	// reader already gave the view.
	for _, pr := range slices.SortedFunc(slices.Values(prs), func(a, b PullRequest) int {
		return cmp.Compare(a.Number, b.Number)
	}) {
		p := pr
		r := &Record{PR: &p, Runs: []Run{}, Files: []string{}, Services: []string{}}
		recs = append(recs, r)
		byNumber[p.Number] = r
	}

	// Then every run, newest first (a run ID starts with its mint time, so a
	// reverse sort by ID is newest first), into the record it belongs to.
	var orphan []Run
	for _, run := range slices.SortedFunc(slices.Values(runs), func(a, b Run) int {
		return cmp.Compare(b.ID, a.ID)
	}) {
		if run.PR == 0 {
			orphan = append(orphan, run)
			continue
		}
		r, ok := byNumber[run.PR]
		if !ok {
			// A pull request the reader did not return: the number is still
			// what the run says, so the record exists, with the host's own
			// answer missing rather than invented.
			p := PullRequest{Number: run.PR}
			r = &Record{PR: &p, Runs: []Run{}, Files: []string{}, Services: []string{}}
			recs = append(recs, r)
			byNumber[run.PR] = r
		}
		r.Runs = append(r.Runs, run)
	}
	// A run with no pull request is a record of its own, and only while it
	// is in flight.
	for _, run := range orphan {
		if run.Terminal {
			continue
		}
		recs = append(recs, &Record{Runs: []Run{run}, Files: []string{}, Services: []string{}})
	}

	for _, r := range recs {
		v.Records = append(v.Records, finish(*r))
	}
	sortRecords(v.Records)
	v.Collisions = collisions(v.Records)
	return v
}

// finish fills in what a record's own runs say: its files and services, the
// branch and release it declares, and whether it is still in flight.
func finish(r Record) Record {
	if r.Runs == nil {
		r.Runs = []Run{}
	}
	for i := range r.Runs {
		// The view emits a run's paths as its own answer ("none"), never as
		// a null a reader has to interpret (Run.Files).
		if r.Runs[i].Files == nil {
			r.Runs[i].Files = []string{}
		}
	}
	for _, run := range r.Runs {
		r.Files = appendUnique(r.Files, run.Files...)
		for _, f := range run.Files {
			if s := ServiceOf(f); s != "" {
				r.Services = appendUnique(r.Services, s)
			}
		}
	}
	r.Branch = ""
	if r.PR != nil {
		r.Branch = r.PR.Branch
	}
	if r.Branch == "" && len(r.Runs) > 0 {
		r.Branch = r.Runs[0].Branch
	}
	r.Release = ReleaseOf(r.Branch)
	if r.Release == "" && len(r.Runs) > 0 {
		r.Release = ReleaseOf(r.Runs[0].BaseBranch)
	}
	r.InFlight = inFlight(r)
	return r
}

// inFlight is whether a record's work is still going: the host's own answer
// when it gave one (the pull request is open), and otherwise whether any run
// behind it has not finished.
//
// The fallback is not a second opinion about a pull request the host did
// answer for — a merged pull request is out of flight however its runs
// ended — it is what the view does with a host that did not answer at all.
func inFlight(r Record) bool {
	if r.PR != nil && r.PR.State != "" {
		return r.PR.State == StateOpen
	}
	for _, run := range r.Runs {
		if !run.Terminal {
			return true
		}
	}
	return false
}

// sortRecords puts the view's records in the order it publishes them: work
// still in flight first (that is what a person opens this for), then by pull
// request number, then by run ID.
func sortRecords(recs []Record) {
	key := func(r Record) (int, int, string) {
		n := 0
		if !r.InFlight {
			n = 1
		}
		num := 0
		if r.PR != nil {
			num = r.PR.Number
		}
		id := ""
		if len(r.Runs) > 0 {
			id = r.Runs[0].ID
		}
		return n, num, id
	}
	slices.SortStableFunc(recs, func(a, b Record) int {
		an, anum, aid := key(a)
		bn, bnum, bid := key(b)
		if an != bn {
			return cmp.Compare(an, bn)
		}
		if anum != bnum {
			return cmp.Compare(anum, bnum)
		}
		return cmp.Compare(aid, bid)
	})
}

// collisions is every pair of in-flight records that shares a changed path or
// a service, one collision per pair, in the order the records are listed.
func collisions(recs []Record) []Collision {
	out := []Collision{}
	for i := range recs {
		if !recs[i].InFlight {
			continue
		}
		for j := i + 1; j < len(recs); j++ {
			if !recs[j].InFlight {
				continue
			}
			files := intersect(recs[i].Files, recs[j].Files)
			services := intersect(recs[i].Services, recs[j].Services)
			if len(files) == 0 && len(services) == 0 {
				continue
			}
			out = append(out, Collision{
				A: recs[i].Label(), B: recs[j].Label(),
				Files: files, Services: services,
			})
		}
	}
	return out
}

// intersect is the values in both a and b, each once, sorted. Both inputs are
// already sorted and deduplicated, so this walks them together.
func intersect(a, b []string) []string {
	out := []string{}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

// appendUnique adds the values not already in dst, and keeps dst sorted: the
// view is a set of paths and of services, and two runs of one pull request
// changing one path must not report it twice.
func appendUnique(dst []string, vals ...string) []string {
	for _, v := range vals {
		if v == "" {
			continue
		}
		if _, ok := slices.BinarySearch(dst, v); !ok {
			dst = append(dst, v)
			slices.Sort(dst)
		}
	}
	return dst
}
