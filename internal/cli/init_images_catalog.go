package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// A catalog image older than this release's base is the gap `fugaro update`
// left behind twice on 2026-11-14: the images stage pins base_images.base and
// copies the history image, and it rebuilt nothing FROM the base it recorded.
// The catalog check job builds an entry once a night, so a project whose
// schedule is paused — or whose entry nobody pushed — keeps an image built
// from a release whose launches the launcher refuses, while the stage that
// just pinned the new base says "No changes".
//
// So the stage reads the catalog's build records too. Every entry whose last
// build was built FROM a release base image older than this release's is
// rebuilt here, in process, by the machinery `fugaro image build <entry>`
// runs (runImageBuildCatalogOrDerived) — bounded the way that command's builds
// are, by the banner, the lease and the managed gate, never by a confirmation
// of this stage's, which a Cloud Build does not ask. An entry whose build
// belongs to another checkout is NOT
// passed over in silence: a catalog build runs as the build account of the
// checkout it is submitted from, holding that repository's credential and
// secrets, so this run cannot submit it — it prints one line per such entry
// naming the command to run in that checkout and ends not covered, and the
// stage never prints "No changes" while one of them is stale.
//
// A rebuild this run submits can also end on the entry's own definition being
// refused — a pinned branch whose Dockerfile mounts a secret, a definition
// over the ceiling — and such an entry is not handed a command: the run
// watched `fugaro image build <entry>` refuse those bytes once, so the step
// prints the refusal, which names the line and the change, instead of a
// command that would fail the same way and say nothing (the owner's 0.26.0
// update, where the images step ended on exactly that command). It is the
// refusal and not a reading of the file this run makes a second time: the
// rebuild IS the command, its first stage the resolve and the validation, so
// what the step prints is what the person would have watched happen.
//
// The rule is offline and record-only: it reads the base reference the
// entry's own record carries and compares its release with this binary's,
// exactly as the anchor check reads a workflow's record (baseRefReleaseRE,
// imagePredates). An entry with no record has never been built — a build that
// has not happened is not a stale image — and a base this tool did not write
// (a custom image, a development tag) matches no release and is left alone.

// catalogStaleEntry is one catalog entry whose recorded build was built FROM
// a base older than this release's, with the repository that build belongs
// to.
type catalogStaleEntry struct {
	entry config.Entry
	repo  infra.CatalogCheckRepoSpec
}

// catalogStaleSet is what the images stage reads of the installation's
// catalog before it does anything: the stale entries, each with the repository
// whose build account rebuilds it, and why the records could not be read when
// they could not.
//
// There is no "here" split: an entry carries its building repository and
// nothing about where this run stands, and whether this run may submit its
// rebuild is asked of the repositories it read (buildable), the way every
// other per-repository decision is (C1, R9).
type catalogStaleSet struct {
	// entries are the stale entries, in the order their building repositories
	// are named (infra.CatalogRepos: one repository's entries together).
	entries []catalogStaleEntry
	// unread is why no record could be read at all, "" when they were. It is
	// a state of its own: a record nobody read is not a stale image, and it is
	// not a current one either, so the stage says it could not tell rather
	// than claiming either (the layer stage's rule for a bucket it cannot
	// read). It never fails the update.
	unread string
}

// any reports whether any entry is known to be stale.
func (s catalogStaleSet) any() bool { return len(s.entries) > 0 }

// staleCatalog reads every catalog entry's build record and lists the stale
// ones with their building repository. It changes nothing and returns no
// error: a record the run cannot read is a state the stage reports, never a
// failure — `fugaro update` is the whole upgrade (the HARD RULE), and a
// project whose records are unreadable still has every other stage applied.
func (s *imagesStage) staleCatalog(ctx context.Context) catalogStaleSet {
	lc := s.e.lc
	if lc == nil || releaseVersion() == "" || s.only != nil {
		// A development build has no release base for a build to be older
		// than, and a refresh (s.only) is about base kinds alone: its answer
		// about the project's catalog images is the refresh's own steps.
		return catalogStaleSet{}
	}
	cat := lc.ImageCatalogEffective(nil)
	if cat == nil || len(cat.Entries) == 0 {
		return catalogStaleSet{}
	}
	// The assignment the check job is deployed with is what says whose build
	// account an entry's build runs as, so the two can never disagree about
	// it (infra.CatalogRepos). Its error is CatalogBuilds' own reading: a
	// repositories key the URL rules refuse, which localcfg validation has
	// already ruled out.
	repos, err := infra.CatalogRepos(lc)
	if err != nil || len(repos) == 0 {
		return catalogStaleSet{}
	}
	url := recordReadURL(lc)
	if fakeEndpointsOnGS(lc, url) {
		return catalogStaleSet{unread: "the local config's storage endpoint is a fake, so the real bucket is not read"}
	}
	rb, err := openRecordBucket(ctx, url)
	if err != nil {
		return catalogStaleSet{unread: oneLine(err.Error())}
	}
	defer func() { _ = rb.Close() }()
	var set catalogStaleSet
	for _, rs := range repos {
		for _, name := range rs.Entries {
			e, ok := catalogEntryNamed(cat, name)
			if !ok {
				continue
			}
			rec, err := catalogEntryRecord(ctx, rb, []string{rs.Slug}, name)
			switch {
			case err != nil:
				set.unread = oneLine(err.Error())
				continue
			case rec == nil || !catalogBaseStale(rec.BaseRef):
				continue
			}
			set.entries = append(set.entries, catalogStaleEntry{entry: e, repo: rs})
		}
	}
	return set
}

// buildable reports whether this run may submit the entry's rebuild: its build
// runs as the build account of the repository whose Dockerfile it reads, with
// that repository's credential and secrets, so this run can submit it only from
// that repository's own checkout — and the repositories it reads are the ones
// C1 answers for it (the checkout this run is in, until I1 reads every enrolled
// repository at its default branch). Which builds `update` may submit is L3's.
func (s *imagesStage) buildable(ctx context.Context, se catalogStaleEntry) bool {
	views, _ := s.e.repos(ctx)
	for _, v := range views {
		if v.Read() && sameRepo(v.Repo, se.repo.Repo) {
			return true
		}
	}
	return false
}

// catalogBaseStale reports whether ref — the base an entry's build record
// says it was built FROM — is a release base image older than this binary's.
// A base this tool did not write (a custom image, a development tag) matches
// no release and is left alone: the rule the anchor check reads a record's
// base by, so the two never disagree about which images are behind.
func catalogBaseStale(ref string) bool {
	m := baseRefReleaseRE.FindStringSubmatch(ref)
	return m != nil && imagePredates(m[2], releaseVersion())
}

// catalogRebuild is the submission of one catalog entry's rebuild, a var
// because a real Cloud Build is not something a test has an account for: the
// stage's decision — which entries it rebuilds, which it names, and that it
// never reports "No changes" while one is behind — is what a test can drive,
// and the submission itself is runImageBuildCatalog's own tests'
// (image_build_catalog_test.go). The stage calls it once per entry, with the
// entry's name.
var catalogRebuild = runImageBuildCatalogOrDerived

// catalogLeft is one stale entry this run did not rebuild, with the reason it
// was not: where the command that rebuilds it is printed, it is the same one
// either way.
type catalogLeft struct {
	entry catalogStaleEntry
	why   string
	// refused is the entry's own definition refused where this run watched it
	// happen: the rebuild of this checkout's entry read and validated the
	// definition — the first thing `fugaro image build <entry>` does — and
	// refused it on its text, so that command refuses the same bytes the same
	// way and is not a command to hand anyone. The step prints the refusal
	// instead (catalogLeftFor), which is the one thing the reader can act on.
	//
	// Only an entry of THIS checkout is ever refused here, because only its
	// rebuild runs here. An entry another checkout builds has its command
	// offered unread: reading that definition is what that repository's
	// credential is for, and this run does not hold it — the same fact that
	// leaves the build to that checkout in the first place.
	refused bool
}

// catalogPlanDetail is the catalog half of a plan line: what the run will
// rebuild here, what it will name, and whether it could tell at all.
func (s *imagesStage) catalogPlanDetail(ctx context.Context, set catalogStaleSet) string {
	var mine, others []catalogStaleEntry
	for _, se := range set.entries {
		if s.buildable(ctx, se) {
			mine = append(mine, se)
			continue
		}
		others = append(others, se)
	}
	var parts []string
	if len(mine) > 0 {
		parts = append(parts, fmt.Sprintf("rebuilds %d catalog image(s) FROM this release's base image with Cloud Build (%s)", len(mine), strings.Join(catalogEntryNames(mine), ", ")))
	}
	if len(others) > 0 {
		parts = append(parts, fmt.Sprintf("%d catalog image(s) are on a base older than this release's and are built from another checkout: this run names each one and the command that rebuilds it", len(others)))
	}
	if set.unread != "" {
		parts = append(parts, "the catalog images' build records could not be read ("+set.unread+"), so this run cannot say whether they are on this release's base")
	}
	return strings.Join(parts, "; ")
}

// catalogEntryNames is entries' names as a plan or a line names them.
func catalogEntryNames(entries []catalogStaleEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = pluginwire.Printable(e.entry.Name)
	}
	return out
}

// rebuildCatalogEntry submits one stale entry's rebuild, in this process, the
// way `fugaro image build <entry>` does: the entry's Dockerfile is copied out
// of this checkout, the build runs as this repository's build account with
// its credential and secrets, it is bounded by its own lease, and the record
// it writes names this release's base for the next check to read.
//
// Nothing of the command's own account is captured: what bounds the build is
// the banner it prints, the lease it takes and the gate's verdict, and its
// progress and its closing line go where any nested run's do — the nested
// reporter reads the flags off the command itself (reporterFor), so under
// --json they land on stderr with the rest of this run's account rather than in
// the document on stdout, and the stage's own lines below are what the reading
// of this run needs.
//
// --json is therefore NOT handed on, and that is the one flag this call must
// not carry: it becomes the nested build's own Conditions.JSON, which takes the
// session out of judgedHere (PersonAtTheTerminal), and openBuildManaged then
// returns nil — the build would be submitted with no kill-switch read, no
// project build rules and no admission record, under its lease alone, which is
// the opposite of the bound above. `fugaro image build` never gets there
// because runImageBuild refuses --json for a Cloud Build; a caller inside
// another command bypasses that refusal, so it must not create the state the
// refusal exists to prevent.
func (s *imagesStage) rebuildCatalogEntry(se catalogStaleEntry) error {
	r := s.e.r
	o := imageBuildOptions{cloud: r.o.cloud}
	return catalogRebuild(r.cmd, se.entry.Name, o)
}

// catalogOutcome is the images stage's catalog half, run once the base these
// builds start FROM is recorded: it rebuilds the stale entries of the
// repositories this run reads, prints one line per entry it cannot, and adds
// the half to the stage's own line.
//
// A "No changes" the ordinary half answered is replaced, never kept beside
// these lines: the stage may not report nothing to do while a catalog image
// of this installation is on an older base.
//
// These rebuilds ask the run's one confirmation nothing, and that is the rule
// and not an omission: a Cloud Build keeps its own bounds (a new file may not
// call init_review.go's run confirmation at all, TestRunConfirmationCallSites),
// and what bounds one is the banner the build prints, the lease it takes and —
// in a managed project — the gate's own verdict, exactly as they bound the
// checkout stage's image rebuilds and `fugaro image build <entry>` itself. A
// session the gate refuses builds nothing here either: it is left the command,
// which is the same step a stale entry of another checkout is left.
func (s *imagesStage) catalogOutcome(ctx context.Context, set catalogStaleSet, out initflow.Outcome) initflow.Outcome {
	if !set.any() && set.unread == "" {
		return out
	}
	w := s.e.r.w
	var parts []string
	if out.Changed && out.Detail != "" {
		parts = append(parts, out.Detail)
	}
	var built []string
	var missed []catalogLeft
	for _, se := range set.entries {
		if !s.buildable(ctx, se) {
			missed = append(missed, catalogLeft{entry: se, why: fmt.Sprintf("its build runs as %s's build account, with that repository's credential and secrets", pluginwire.Printable(se.repo.Repo))})
			continue
		}
		// The rebuild's own failure is read before it is described: the one
		// failure `fugaro image build <entry>` could not answer differently on
		// the same bytes is the entry's own definition being refused
		// (catalog.Refused), and an entry refused there is one this run must
		// not hand the person a command for. Every other failure — a lease
		// with no room, a quota, a read that did not come back — leaves the
		// command offered, because the command may well work where this run's
		// did not.
		if err := s.rebuildCatalogEntry(se); err != nil {
			missed = append(missed, catalogLeft{entry: se, why: catalogRebuildWhy(err), refused: catalog.IsRefused(err)})
			continue
		}
		built = append(built, pluginwire.Printable(se.entry.Name))
	}
	if len(built) > 0 {
		fmt.Fprintf(w, "  rebuilt %d catalog image(s) FROM this release's base image: %s\n", len(built), strings.Join(built, ", "))
		parts = append(parts, fmt.Sprintf("%d catalog image(s) rebuilt FROM this release's base image (%s)", len(built), strings.Join(built, ", ")))
	}
	if len(missed) > 0 {
		for _, m := range missed {
			if m.refused {
				// No command on the line, because there is none to give: the
				// entry's own definition is what has to change, and the
				// refusal just said so.
				fmt.Fprintf(w, "  %s: NOT rebuilt: %s\n", pluginwire.Printable(m.entry.entry.Name), m.why)
				continue
			}
			fmt.Fprintf(w, "  %s: NOT rebuilt: %s; run %s in %s's checkout\n",
				pluginwire.Printable(m.entry.entry.Name), m.why, catalogBuildCommand(m.entry.entry.Name), pluginwire.Printable(m.entry.repo.Repo))
		}
		fmt.Fprintf(w, "  not covered: %d catalog image(s) of this installation are on a base older than this release's and this run did not rebuild them — the lines above name each one and what rebuilding it takes\n", len(missed))
		parts = append(parts, catalogNotCovered(len(missed)))
	}
	if set.unread != "" {
		parts = append(parts, "the catalog images' build records could not be read ("+set.unread+"), so this run cannot say whether they are on this release's base")
	}
	detail := strings.Join(parts, "; ")
	if detail == "" {
		detail = "No changes"
	}
	return initflow.Outcome{Changed: out.Changed || len(built) > 0, Detail: detail, Left: catalogLeftFor(missed)}
}

// catalogLeftFor is the step this run leaves for the person when a stale
// catalog image is not rebuilt here: one line per entry naming it and why it
// was not, and one command per entry whose command can run, each in the
// checkout whose build account that entry's build runs as. It is what makes
// the run end not finished (exit 1) rather than reporting a stage that
// skipped half of itself as done.
//
// An entry this run watched its own definition refuse gets its refusal in the
// line and no command under it: `fugaro image build <entry>` reads the same
// bytes and refuses them the same way, so printing it would be handing the
// reader a step that fails and says nothing about what to change. What does
// change the entry is the definition the refusal names — the run cannot edit
// a file in another repository, and an edit here is not what the entry's
// build reads — so the command stays in the sentence the reader acts on:
// change the definition, then `fugaro update` again, which is the step this
// run is.
//
// The step's reason is set here and not left to the loop: this stage applied
// what it could before it left this, so its own line is a success line and not
// a reason (loop.applied reads none from it), and what the reader is owed is
// the fact the stage's own line carries — the images are on an older base and
// this run did not rebuild them.
func catalogLeftFor(missed []catalogLeft) *initflow.Left {
	if len(missed) == 0 {
		return nil
	}
	var cmds, names []string
	for _, m := range missed {
		names = append(names, fmt.Sprintf("%s (%s's checkout): %s", pluginwire.Printable(m.entry.entry.Name), pluginwire.Printable(m.entry.repo.Repo), m.why))
		if !m.refused {
			cmds = append(cmds, catalogBuildCommand(m.entry.entry.Name))
		}
	}
	text := fmt.Sprintf("this run did not rebuild %d catalog image(s) that are on a base older than this release's: %s. ", len(missed), strings.Join(names, ", "))
	if len(cmds) == 0 {
		// Nothing below to run: every entry left is one whose own definition
		// is refused, and each refusal says what about it has to change.
		text += "Change what the refusals name, then fugaro update again"
	} else {
		text += "Run each command below in the checkout it names, then fugaro update again"
	}
	return &initflow.Left{
		Stage:    initflow.Images,
		Kind:     initflow.LeftCommand,
		Text:     text,
		Commands: cmds,
		Why:      catalogNotCovered(len(missed)),
	}
}

// catalogNotCovered is the fact the stage's own line and the step it leaves
// both carry: the images are on a base older than this release's and this run
// did not rebuild them. It is one sentence in two places — the stage's line,
// where it follows what it did rebuild, and the step's reason, where it stands
// alone — so the two can never disagree about what was left behind.
func catalogNotCovered(n int) string {
	return fmt.Sprintf("not covered: %d catalog image(s) are on a base older than this release's and this run did not rebuild them", n)
}

// catalogRebuildWhy is why one of this checkout's entries was not rebuilt, in
// the words its reader acts on. A refusal of the entry's own definition is the
// refusal itself and nothing else: it is about the entry, it names the line
// and the change, and the run adds no frame of its own to it — that is the
// sentence the step prints instead of the command. Any other failure is this
// run's own (a lease with no room, a quota, a read that did not come back) and
// is said as such, because the same command run by the person may well answer
// differently.
//
// Both are one line: a failure that reaches this point can carry a provider's
// whole answer, and the step is what the run ends on.
func catalogRebuildWhy(err error) string {
	if catalog.IsRefused(err) {
		return oneLine(err.Error())
	}
	return "its rebuild did not finish (" + oneLine(err.Error()) + ")"
}

// catalogBuildCommand is the command that rebuilds one catalog entry from the
// checkout its build runs as: the command whose machinery the stage runs in
// process for the entries of this checkout.
func catalogBuildCommand(entry string) string {
	return selfCommand() + " image build " + quoteWord(entry)
}
