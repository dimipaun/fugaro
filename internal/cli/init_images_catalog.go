package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// catalogRebuildAtOnce is how many catalog rebuilds this stage runs at a time:
// four, the number D3444 and D3446 set.
const catalogRebuildAtOnce = 4

// A catalog image is stale in one of two ways (D3446), and an update treats
// the two differently:
//
//   - its build record carries another base-image epoch (imagecheck.BaseEpoch),
//     or none at all: the image was built from base-image files that have
//     changed since, so a run that boots it may meet a runner the launcher
//     refuses. That rebuild is eager — this stage submits it here, in process,
//     by the machinery `fugaro image build <entry>` runs
//     (runImageBuildCatalogOrDerived), and waits for it. A record stating no
//     epoch is release A's own case: every entry an earlier release built is
//     rebuilt once, which is D3445's one eager rebuild. A build that fails
//     here fails the images step in one line naming the entry — it is on a
//     runner the launcher may refuse, so it is never left silently behind —
//     and the step that fixes it is the one the run is: `fugaro update` again.
//   - its build is older than config.DefaultMaxAge: the image is current and
//     merely old, which is what the nightly rebuild is for (D3444). This stage
//     submits that build too, with --no-wait, and waits for none of them: the
//     entry is current either way, and an update that blocked on it would be
//     the release-long wait this task removes.
//
// A release alone makes nothing stale: the epoch moves when images/base,
// images/derived or images/common changes, never when the Fugaro version does
// (D3445, D3446), so a release's own images catch up on the nightly rebuild.
//
// Both kinds are submitted four at a time (D3444, D3446), each on a command of
// its own. Two things follow from the four that did not from the one, and both
// are named here rather than discovered later: four nested `fugaro image
// build` runs in the same second open the same log file name — report.OpenLog
// is O_EXCL and the name carries the pid and not the entry — so of two builds
// opened in one second the second keeps no log; and each build's own lines
// land in this stage's buffer, which is printed only for a build that failed,
// so a submitted build's Cloud Build log URL is on no line of this run. The
// build itself is untouched by either, and the entry's record and the Cloud
// Build console both carry what the URL carried.
//
// The checkout's own case is the gap `fugaro update` left behind twice on
// 2026-11-14: the images stage pins base_images.base and copies the history
// image, and it rebuilt nothing FROM the base it recorded, while the catalog
// check job builds an entry once a night — so a project whose schedule is
// paused, or whose entry nobody pushed, kept an image built from a release
// whose launches the launcher refuses, and the stage that had just pinned the
// new base said "No changes".
//
// A rebuild is bounded the way `fugaro image build <entry>`'s builds are, by
// the banner, the lease and the managed gate, never by a confirmation of this
// stage's, which a Cloud Build does not ask. An entry whose build belongs to
// another checkout is NOT passed over in silence while its epoch is behind: a
// catalog build runs as the build account of the checkout it is submitted
// from, holding that repository's credential and secrets, so this run cannot
// submit it — it prints one line per such entry naming the command to run in
// that checkout and ends not covered, and the stage never prints "No changes"
// while an entry is on an older base image. Such an entry past its rebuild age
// and no further behind is the nightly rebuild's alone, and this run says
// nothing about it: the entry is current, and a step for it every update,
// whether the vault checkout ran a build or not, would be a run that never
// finishes over an image that is fine.
//
// A rebuild this run submits can also end on the entry's own definition being
// refused — a pinned branch whose Dockerfile mounts a secret, a definition
// over the ceiling — and such an entry is not handed a command: the run
// watched `fugaro image build <entry>` refuse those bytes once, so the line
// prints the refusal, which names the line and the change, instead of a
// command that would fail the same way and say nothing (the owner's 0.26.0
// update, where the images step ended on exactly that command). It is the
// refusal and not a reading of the file this run makes a second time: the
// rebuild IS the command, its first stage the resolve and the validation, so
// what the line says is what the person would have watched happen.
//
// The rule is offline and record-only: it reads the epoch and the build time
// the entry's own record carries. An entry with no record has never been built
// — a build that has not happened is not a stale image.

// catalogStaleEntry is one catalog entry whose recorded build is stale, with
// the repository that build belongs to and which of the two stale kinds it is:
// an eager rebuild (the base image changed, and this run waits for it) or one
// past its rebuild age (the nightly rebuild's, submitted and not waited for).
type catalogStaleEntry struct {
	entry config.Entry
	repo  infra.CatalogCheckRepoSpec
	eager bool
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
	now := time.Now()
	for _, rs := range repos {
		for _, name := range rs.Entries {
			e, ok := catalogEntryNamed(cat, name)
			if !ok {
				continue
			}
			rec, err := catalogEntryRecord(ctx, rb, []string{rs.Slug}, name)
			if err != nil {
				set.unread = oneLine(err.Error())
				continue
			}
			stale, eager := catalogStaleRecord(rec, now)
			if !stale {
				continue
			}
			set.entries = append(set.entries, catalogStaleEntry{entry: e, repo: rs, eager: eager})
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

// catalogStaleRecord reads one entry's build record and says whether the entry
// is stale and, when it is, whether its rebuild is eager — one this stage
// submits and waits for, because the image is on a base the launcher may
// refuse — rather than one it hands to the background (D3445, D3446).
//
// Two fields of the record answer, and nothing else does: the epoch it was
// built under, and when. What the record says it was built FROM names no
// release any more — a release is not what makes an image stale.
func catalogStaleRecord(rec *imagecheck.CatalogRecordState, now time.Time) (stale, eager bool) {
	if rec == nil {
		// Never built: a build that has not happened is not a stale image.
		return false, false
	}
	if rec.Epoch != imagecheck.BaseEpoch {
		return true, true
	}
	return now.Sub(rec.BuiltAt) > config.DefaultMaxAge, false
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
// rebuild here and wait for, what it will submit and leave running, what it
// will name, and whether it could tell at all.
func (s *imagesStage) catalogPlanDetail(ctx context.Context, set catalogStaleSet) string {
	var eager, later, others []catalogStaleEntry
	for _, se := range set.entries {
		switch {
		case !s.buildable(ctx, se):
			if se.eager {
				others = append(others, se)
			}
		case se.eager:
			eager = append(eager, se)
		default:
			later = append(later, se)
		}
	}
	var parts []string
	if len(eager) > 0 {
		parts = append(parts, fmt.Sprintf("rebuilds %d catalog image(s) FROM this release's base image with Cloud Build and waits for it (%s)", len(eager), strings.Join(catalogEntryNames(eager), ", ")))
	}
	if len(later) > 0 {
		parts = append(parts, fmt.Sprintf("submits %d catalog image(s) past their rebuild age to Cloud Build in the background (%s)", len(later), strings.Join(catalogEntryNames(later), ", ")))
	}
	if len(others) > 0 {
		parts = append(parts, fmt.Sprintf("%d catalog image(s) are behind this release's base image and are built from another checkout: this run names each one and the command that rebuilds it", len(others)))
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

// catalogBuildResult is one entry's rebuild attempt: the error it ended on,
// and what its nested run printed, which the stage writes out only for a build
// that failed.
type catalogBuildResult struct {
	err error
	out bytes.Buffer
}

// rebuildCatalog runs the entries' rebuilds, four at a time, and returns one
// result per entry in the entries' order. Each runs in this process the way
// `fugaro image build <entry>` does — the entry's Dockerfile is copied out of
// this checkout, the build runs as this repository's build account with its
// credential and secrets, it is bounded by its own lease, and the record it
// writes carries this release's base for the next check to read — on a command
// of its own that shares the run's flags and its input. The input is what the
// managed gate reads a person at a terminal by, so the build is judged as it
// would be on the run's own command.
//
// noWait is the background arm (D3446): `fugaro image build --no-wait` submits
// the build and returns without waiting for it. The call itself is always
// waited for, in both arms: a submission's own answer is what says whether the
// build was started at all.
//
// --json is NOT handed on, and that is the one flag this call must not carry:
// it becomes the nested build's own Conditions.JSON, which takes the session
// out of judgedHere (PersonAtTheTerminal), and openBuildManaged then returns
// nil — the build would be submitted with no kill-switch read, no project build
// rules and no admission record, under its lease alone, which is the opposite
// of the bound above. `fugaro image build` never gets there because
// runImageBuild refuses --json for a Cloud Build; a caller inside another
// command bypasses that refusal, so it must not create the state the refusal
// exists to prevent. The nested reporter reads the rest of the flags off the
// command itself (reporterFor), so a nested run's own account lands where this
// run's does rather than in the document on stdout.
func (s *imagesStage) rebuildCatalog(ctx context.Context, entries []catalogStaleEntry, noWait bool) []catalogBuildResult {
	r := s.e.r
	res := make([]catalogBuildResult, len(entries))
	slots := make(chan struct{}, catalogRebuildAtOnce)
	var wg sync.WaitGroup
	for i, se := range entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			sub := &cobra.Command{Use: r.cmd.Use}
			sub.Flags().AddFlagSet(r.cmd.Flags())
			sub.SetIn(r.cmd.InOrStdin())
			sub.SetOut(&res[i].out)
			sub.SetErr(&res[i].out)
			sub.SetContext(ctx)
			res[i].err = catalogRebuild(sub, se.entry.Name, imageBuildOptions{cloud: r.o.cloud, noWait: noWait})
		}()
	}
	wg.Wait()
	return res
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
// The two kinds end differently on purpose. An eager rebuild is waited for and
// its failure fails this stage, in one line naming the entry: the image is on
// a base the launcher may refuse, and the run that fixes it is `fugaro update`
// again — the loop's own fix line, so no second step is left saying the same
// thing. A background submission's failure is said with the command that
// rebuilds the entry, and leaves the step the missed entries have always left,
// because the entry is current and the failure is the run's (a lease, a quota,
// a read) and may well not repeat. An eager failure stops the background
// submissions of the same run: a run about to fail leaves no build of its own
// running.
//
// These rebuilds ask the run's one confirmation nothing, and that is the rule
// and not an omission: a Cloud Build keeps its own bounds (a new file may not
// call init_review.go's run confirmation at all, TestRunConfirmationCallSites),
// and what bounds one is the banner the build prints, the lease it takes and —
// in a managed project — the gate's own verdict, exactly as they bound the
// checkout stage's image rebuilds and `fugaro image build <entry>` itself. A
// session the gate refuses builds nothing here either: it is left the command,
// which is the same step a stale entry of another checkout is left.
func (s *imagesStage) catalogOutcome(ctx context.Context, set catalogStaleSet, out initflow.Outcome) (initflow.Outcome, error) {
	if !set.any() && set.unread == "" {
		return out, nil
	}
	w, errw := s.e.r.w, s.e.r.cmd.ErrOrStderr()
	var parts []string
	if out.Changed && out.Detail != "" {
		parts = append(parts, out.Detail)
	}
	var missed []catalogLeft
	var eager, later []catalogStaleEntry
	for _, se := range set.entries {
		if !s.buildable(ctx, se) {
			if se.eager {
				missed = append(missed, catalogLeft{entry: se, why: fmt.Sprintf("its build runs as %s's build account, with that repository's credential and secrets", pluginwire.Printable(se.repo.Repo))})
			}
			continue
		}
		if se.eager {
			eager = append(eager, se)
			continue
		}
		later = append(later, se)
	}
	var built, submitted []string
	for i, res := range s.rebuildCatalog(ctx, eager, false) {
		if res.err != nil {
			// The nested run's own account is what says why, in as much detail
			// as the build had; the step's line is the one sentence.
			_, _ = errw.Write(res.out.Bytes())
			return initflow.Outcome{}, userErr("%s: the rebuild from this release's base image did not finish (%s); run %s again", pluginwire.Printable(eager[i].entry.Name), oneLine(res.err.Error()), selfCommand()+" update")
		}
		built = append(built, pluginwire.Printable(eager[i].entry.Name))
	}
	for i, res := range s.rebuildCatalog(ctx, later, true) {
		// The failure is read before it is described: the one failure `fugaro
		// image build <entry>` could not answer differently on the same bytes is
		// the entry's own definition being refused (catalog.Refused), and an
		// entry refused there is one this run must not hand the person a command
		// for. Every other failure — a lease with no room, a quota, a read that
		// did not come back — leaves the command offered, because the command may
		// well work where this run's did not.
		if res.err != nil {
			missed = append(missed, catalogLeft{entry: later[i], why: catalogRebuildWhy(res.err), refused: catalog.IsRefused(res.err)})
			_, _ = errw.Write(res.out.Bytes())
			continue
		}
		submitted = append(submitted, pluginwire.Printable(later[i].entry.Name))
	}
	parts = append(parts, reportCatalogBuilds(w, built, submitted, missed)...)
	if set.unread != "" {
		parts = append(parts, "the catalog images' build records could not be read ("+set.unread+"), so this run cannot say whether they are on this release's base")
	}
	detail := strings.Join(parts, "; ")
	if detail == "" {
		detail = "No changes"
	}
	changed := out.Changed || len(built) > 0 || len(submitted) > 0
	return initflow.Outcome{Changed: changed, Detail: detail, Left: catalogLeftFor(missed)}, nil
}

// reportCatalogBuilds prints what a run's catalog rebuilds came to — the ones
// it rebuilt and waited for, the ones it left running in the background, one
// line per entry it did not rebuild — and returns the parts of the stage's own
// line.
func reportCatalogBuilds(w io.Writer, built, submitted []string, missed []catalogLeft) []string {
	var parts []string
	if len(built) > 0 {
		fmt.Fprintf(w, "  rebuilt %d catalog image(s) FROM this release's base image: %s\n", len(built), strings.Join(built, ", "))
		parts = append(parts, fmt.Sprintf("%d catalog image(s) rebuilt FROM this release's base image (%s)", len(built), strings.Join(built, ", ")))
	}
	if len(submitted) > 0 {
		fmt.Fprintf(w, "  submitted %d catalog image(s) to rebuild in the background: %s\n", len(submitted), strings.Join(submitted, ", "))
		parts = append(parts, fmt.Sprintf("%d catalog image(s) rebuilding in the background (%s)", len(submitted), strings.Join(submitted, ", ")))
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
		fmt.Fprintf(w, "  not covered: %d catalog image(s) this run did not rebuild\n", len(missed))
		parts = append(parts, catalogNotCovered(len(missed)))
	}
	return parts
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
// the fact the stage's own line carries — this run did not rebuild them.
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
	text := fmt.Sprintf("this run did not rebuild %d catalog image(s): %s. ", len(missed), strings.Join(names, ", "))
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
// both carry: this run did not rebuild them. It is one sentence in two places
// — the stage's line, where it follows what it did rebuild, and the step's
// reason, where it stands alone — so the two can never disagree about what was
// left behind. Which of the two kinds each entry is, and why it was left, is
// on the line that names the entry: what the entries have in common is that
// this run did not rebuild them.
func catalogNotCovered(n int) string {
	return fmt.Sprintf("not covered: %d catalog image(s) this run did not rebuild", n)
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
