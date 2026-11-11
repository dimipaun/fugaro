package cli

// issue_submit.go is `fugaro issue submit`: the one command of this family
// that sends text to a public repository. It is built on four rules and
// nothing else.
//
// D1720: a plain run is a preview and files nothing — the post, whole and
// exactly as it would be sent, and the sha256 of its bytes — and `--ask` at
// the owner's own terminal is the only thing that files. F19: the post is
// built once, so the bytes that were shown are the bytes that are filed, and
// what GitHub holds is what the person read. F22: an issue the project's store
// holds is filed under that store's claim, taken after the yes and given back
// when the post fails, so two people filing one issue file it once. F4 and F5:
// the post is projected from the record and scrubbed of the names of the
// project it came from, so a reader of the public repository learns the bug and
// not whose machine it was.
//
// The shared store is T-IS11's half and this command's two calls into it are
// the sync that uploads what this machine has not filed, and the claim a
// filing takes (F22). Everything else here is the person's own `gh` and their
// own credentials.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/issues"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// issueGH is the person's own `gh`, as the three questions submitting asks it
// (issues.GH). It is a variable so a test replaces it: no test here runs a
// binary, and none of them reaches GitHub.
var issueGH = issues.GH{}

// issueOSRelease is /etc/os-release as this machine has it, "" where there is
// none — the file issues.InstallHint reads a Linux distribution out of (F15).
// It is a variable for the same reason issueGH is: the hint is about the
// machine the command runs on, and a test pins the distribution it answers for
// rather than the one the test machine happens to have.
var issueOSRelease = func() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return string(data)
}

// newIssueSubmitCmd builds `fugaro issue submit`: what would be filed, and —
// with `--ask` at a terminal — the filing itself.
func newIssueSubmitCmd() *cobra.Command {
	var ask bool
	cmd := &cobra.Command{
		Use:   "submit [REF]",
		Short: "Show what would be filed on GitHub, and file it with --ask",
		Long: "Show exactly what would be filed on GitHub for one issue, or for every\n" +
			"issue that is still waiting.\n\n" +
			"REF is the issue's number — `7`, or the `#0007` a listing prints — or\n" +
			"its local ID `local-<run stem>`, the same argument `show` takes. With\n" +
			"no reference every `pending` issue is visited, and the ones already\n" +
			"filed or set aside are skipped.\n\n" +
			"The post is built once and printed as it is: the title, the body, and\n" +
			"the sha256 of the exact bytes. A run without --ask files nothing and\n" +
			"uploads nothing. --ask asks at your own terminal after showing the\n" +
			"preview and files exactly those bytes; a coding agent's session and a\n" +
			"pipe never file, because a confirmation nobody can answer is a refusal\n" +
			"and never a silent proceed.\n\n" +
			"--ask uploads this machine's own pending issues first, exactly as\n" +
			"`issue sync` does, and files the project's own record of a numbered\n" +
			"issue: the store is the truth once an issue is uploaded. The filing is\n" +
			"made under a claim on that record, so two people filing one issue at\n" +
			"the same moment file it once; a claim another submitter holds is\n" +
			"refused with who holds it and until when, and a record that changed\n" +
			"after the preview was shown is refused rather than filed unseen.\n\n" +
			"Filing needs GitHub's own `gh` with an account on github.com. Where it\n" +
			"is missing, or has no account there, the install command or `gh auth\n" +
			"login` is printed with the new-issue link holding the same title and\n" +
			"body; nothing is filed and the command exits non-zero.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return runIssueSubmit(cmd, ref, ask)
		},
	}
	cmd.Flags().BoolVar(&ask, "ask", false, "show the preview and file it after a yes at your own terminal")
	return cmd
}

func runIssueSubmit(cmd *cobra.Command, ref string, ask bool) error {
	ctx := cmd.Context()
	agent := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin()))
	if ask && agent != "" {
		// F10: --ask is the half of this command that writes — this machine's
		// pending issues go up before anything is filed, and the filing itself
		// takes the project's claim — so a coding agent's session is refused
		// here rather than at the prompt, with nothing written on the way.
		return userErr("nothing was filed: fugaro issue submit --ask uploads to the project's store and files on GitHub, so a person runs it at their own terminal: %s", initflow.AgentRefusal(agent))
	}
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	// The project this run files for, and its store: its names are what the post
	// is scrubbed of (F4) and the digest of its runs bucket is the installation
	// that says which of the records in this machine's one issues folder are
	// this project's own (F8b). This is `list`, `show` and `mine`'s own
	// resolution — mineProject, the one openIssueShared reads too — so the
	// records this command files are the project those commands answered for. A
	// run with no project, or one that cannot reach the store, resolves neither
	// and files what this machine has (issueFile).
	//
	// It is deliberately read here rather than taken from the store: a coding
	// agent's session resolves no store at all (F10) and still has to scrub, and
	// the names are the local config and the checkout — the same files a session
	// is free to read — so a preview a session prints is the preview a person
	// would have printed. Without this a session would also file nothing of this
	// project's own: every stamped record would read as another project's
	// (issueForeign).
	lc, err := mineProject(ctx)
	if err != nil {
		lc = nil
	}
	installation := ""
	if lc != nil {
		installation = issues.Digest(lc.BucketURL())
	}
	sh := openIssueShared(ctx, agent)
	defer sh.Close()
	// A record this run writes is stamped with the project that wrote it, so an
	// issue filed here is never carried by another project's commands and never
	// filed a second time into another project's repository.
	store.Installation = installation
	if ask {
		// Sync first (T-IS11, F22): an issue this machine hit and has not
		// uploaded has to be in the store before it can be claimed and filed,
		// and the sweep then visits it as the store's own issue rather than as a
		// file here. A run without --ask writes nothing, the upload included.
		sh.uploadPending(ctx, store)
	}
	todo, err := issueSubmittable(ctx, store, sh, ref, installation)
	if err != nil {
		return err
	}
	// The lines about what this run could not reach go to stderr: stdout is the
	// previews, and a note between them would read as part of a post.
	for _, n := range sh.notes {
		fmt.Fprintln(cmd.ErrOrStderr(), n)
	}
	out := cmd.OutOrStdout()
	if len(todo) == 0 {
		// A sweep with nothing waiting is not a failure, and the gh question is
		// not asked: the fallback is about an issue that could not be filed,
		// and here there is none (spec 5).
		_, err := fmt.Fprintln(out, "Nothing is pending: no issue is waiting to be filed.")
		return err
	}
	names := issueNames(lc)
	if state := readIssueGHState(ctx, issueGH); !state.ok {
		return issueFallback(cmd, todo, names, state)
	}
	for _, i := range todo {
		if err := issueSubmit(cmd, store, sh, i, names, ask); err != nil {
			return err
		}
	}
	if !ask {
		// Once for the run and not once per issue: a sweep of five previews is
		// five previews and one thing to do about them.
		_, err := fmt.Fprintln(out, "\nNothing was filed. Pass --ask to file these bytes from your own terminal.")
		return err
	}
	return nil
}

// issueSubmittable is the issues a run would file: the one a reference names,
// or every `pending` issue in the store (acceptance 3), each one as the
// project's store holds it (F13, issueStored).
//
// A reference that names an issue already on GitHub is refused rather than
// filed a second time: the record's own status and URL say it is there, and a
// second post of one bug under someone else's name is the thing the record is
// there to prevent (F28). A dismissed issue named explicitly is filed all the
// same: the reference is the person's own decision, made about an issue whose
// own record says only that it was not worth filing then, and no command here
// takes a dismissal back. The sweep is the other way round for the same
// reason — it visits what is waiting and leaves the two finished statuses
// alone, so a dismissal nobody named is never filed by accident.
//
// installation is the project this run files for, and a record that names
// another one is never filed from here (F8b): the folder is one for the whole
// machine, and the post would be scrubbed of *this* project's names while
// carrying another project's summary, diagnosis and log excerpts — the exact
// leak the stamp exists to prevent. A record that names none is kept, the
// answer `mine` gives it: it was saved by a build that had no field to stamp,
// and dropping it would hide the user's own issue rather than attribute it to
// the wrong project.
//
// A run with no project selected files nothing a project stamped, for a second
// reason on top of the first: it has no names to scrub with (issueNames), so
// that project's name, GCP project and repositories would reach a public
// repository as they are. It says so rather than filing: the person is the one
// who can select the project the record belongs to, and this run is the one
// that would have published what only that project knows how to take out.
func issueSubmittable(ctx context.Context, store *issues.Store, sh *issueShared, ref, installation string) ([]issues.Issue, error) {
	if ref != "" {
		i, err := store.Get(issueRefArg(ref))
		if err != nil {
			return nil, userErr("nothing was filed: %v", err)
		}
		if issueForeign(i, installation) {
			if installation == "" {
				return nil, userErr("nothing was filed: %s belongs to another installation, and no project is selected here to scrub what it carries; select the project it belongs to, and file it there", issueRef(i))
			}
			return nil, userErr("nothing was filed: %s belongs to another installation, and submit files this project's issues only", issueRef(i))
		}
		if at := issueURL(i.URL); i.Status == issues.StatusSubmitted || at != "" {
			where := ""
			if at != "" {
				where = " at " + at
			}
			return nil, userErr("nothing was filed: %s was already filed%s, and submit files an issue once", issueRef(i), where)
		}
		stored, fromStore := issueStored(ctx, sh, i)
		if fromStore && issueStoreFiled(stored) {
			// The store's copy is the record this run would file, so its own status
			// is the one that says whether there is anything left to file (F13):
			// another machine filed it, or a release resolved it, and this machine's
			// file was never told. A set-aside issue is not one of these — naming it
			// is the person's own decision — and issueStoreFiled says why.
			at := issueURL(stored.URL)
			why := string(stored.Status)
			if at != "" {
				why += " at " + at
			}
			return nil, userErr("nothing was filed: the project's own record of %s is %s, and submit files an issue once", issueRef(i), why)
		}
		return []issues.Issue{stored}, nil
	}
	all, err := store.List()
	if err != nil {
		return nil, userErr("nothing was listed: %v", err)
	}
	var todo []issues.Issue
	for _, i := range all {
		if i.Status != issues.StatusPending || issueForeign(i, installation) {
			continue
		}
		stored, fromStore := issueStored(ctx, sh, i)
		if fromStore && !issueStoreWaiting(stored) {
			// The sweep decides on the store's copy and not on the file here (F13):
			// a teammate's dismissal, a resolution a release recorded and a filing
			// another machine made all live in the store, and nothing copies any of
			// them into this machine's folder — `issue list` prints the local status,
			// and `dismiss` writes it only where it was run. The file still says
			// `pending`, so left to itself the sweep would file what the project
			// already decided about.
			continue
		}
		todo = append(todo, stored)
	}
	return todo, nil
}

// issueStored is one issue as the project's store has it: for an issue with a
// number, the record the store holds and whether it was read, and for one
// without, this machine's own file (F13, F23).
//
// A number is the store's, and the copy here is the one this machine uploaded
// when it took that number: a teammate may have dismissed the issue, a release
// may have resolved it and another machine may have filed it since, and the file
// here would still say `pending`. The post is built from the store's copy,
// because the bytes previewed are the bytes filed (F19) and the store's record
// is the one every reader of the issue knows — and whether it was read is
// answered too, because it is the store's own status that decides what there is
// left to file (issueStoreFiled, issueStoreWaiting) and not the file here.
//
// A record the store cannot be read for keeps this machine's own copy and says
// so in one line: the preview is then a preview of the file here, and the claim
// a filing takes reads the same record and answers with the store's own failure
// (issueClaim), so a record that cannot be read is never filed unclaimed. A run
// with no store at all — no project selected, a coding agent's session — reads
// nothing and files what this machine has (issueFile).
func issueStored(ctx context.Context, sh *issueShared, i issues.Issue) (issues.Issue, bool) {
	if i.Number == 0 || sh.bucket == nil {
		return i, false
	}
	stored, err := issues.SharedIssue(ctx, sh.bucket, i.Number)
	if err != nil {
		sh.notes = append(sh.notes, "note: the project's issue "+issueRef(i)+" could not be read ("+oneLine(err.Error())+"), so this machine's own copy of it stands in")
		return i, false
	}
	return stored, true
}

// issueStoreFiled is whether the project's own record of an issue says there is
// nothing left to file: it is on GitHub already, or a release resolved it. A
// reference that names one is refused before any preview is shown, because a
// person who names an issue that is filed is told so rather than shown a post
// that will never be made (F28).
//
// A dismissal is deliberately not one of these. It says the issue was set aside
// rather than that it is finished, and a person who names one has already made
// the decision to file it all the same (issueSubmittable).
func issueStoreFiled(i issues.Issue) bool {
	return i.Status == issues.StatusSubmitted || i.Status == issues.StatusResolved || issueURL(i.URL) != ""
}

// issueStoreWaiting is whether the sweep has anything to do with the project's
// own record of an issue: it is still pending and on GitHub nowhere. Every other
// status, the dismissal included, is a decision the store holds and this
// machine's file does not (issueStored), so the sweep steps over it rather than
// filing what the project already settled.
func issueStoreWaiting(i issues.Issue) bool {
	return i.Status == issues.StatusPending && issueURL(i.URL) == ""
}

// issueForeign reports whether a record belongs to an installation this run
// does not file for: the one condition under which it is neither filed nor
// previewed here.
//
// A record that names no installation is never foreign. It was saved by a build
// that had no field to stamp, so it is the user's own issue and there is no
// other project to attribute it to: a run with a project files it and stamps
// it, and a run with none files it as it is, which is what makes `submit` work
// in a directory that selects no project at all.
//
// A record that names one is foreign unless this run names the same one. That
// includes the run that names none, and it is the half worth spelling out: with
// no project selected there are no names to scrub the post with (F4, F8b), so
// filing another project's record from there would put that project's name, GCP
// project and repositories into a public issue with nothing taken out. `mine`
// is looser than this — it prints an unattributable row rather than hiding it —
// and rightly so: printing a local row files nothing, and `submit` is the one
// command here that publishes.
func issueForeign(i issues.Issue, installation string) bool {
	return i.Installation != "" && i.Installation != installation
}

// issueSubmit is one issue's whole submission: the post built once, the
// preview printed from it, and — with `--ask` and a yes at the owner's
// terminal — that post filed under the project's claim (D1720, F19, F22).
func issueSubmit(cmd *cobra.Command, store *issues.Store, sh *issueShared, i issues.Issue, names []string, ask bool) error {
	out := cmd.OutOrStdout()
	p := issues.BuildPost(i, names)
	// The preview goes out first and decides nothing: it is what the person
	// reads before answering, and it is the same text whether or not the flag
	// is passed, so a run with no --ask is the same run with no gh call and no
	// record written after it.
	if _, err := fmt.Fprint(out, issues.Preview(p)); err != nil {
		return err
	}
	if !ask {
		// The run's one line about the flag comes after the last preview
		// (runIssueSubmit): the preview is this issue's, the instruction is the
		// run's.
		return nil
	}
	ok, err := issueConfirm(cmd)
	if err != nil {
		return err
	}
	if !ok {
		_, err := fmt.Fprintf(out, "\nNothing was filed for %s.\n", issueRef(i))
		return err
	}
	return issueFile(cmd, store, sh, i, p)
}

// issueFile files exactly the bytes that were previewed and records what
// GitHub answered (F19, spec 4).
//
// The post p is the one the caller built and showed: nothing is rebuilt here,
// which is the whole of F19 — a record changed between the preview and the yes
// would otherwise be filed unseen under this person's account.
//
// An issue the project's store holds is filed under a claim on it (F22). The
// claim is taken here, after the yes, and the record it was taken on is the one
// the preview came from (issueStored): a record that changed in between is
// refused and the claim given back, so the bytes one person read are the bytes
// one person files, and two people filing at the same moment file once.
func issueFile(cmd *cobra.Command, store *issues.Store, sh *issueShared, i issues.Issue, p issues.Post) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	var c *issues.Claimed
	if i.Number != 0 {
		var err error
		if c, err = issueClaim(cmd, sh, i); err != nil {
			return err
		}
	}
	url, err := issueGH.Create(ctx, p)
	if err != nil {
		if c != nil {
			// F22: the claim goes back, so a post that failed is one the next
			// run claims and tries again without waiting out the lease.
			if relErr := issues.Release(ctx, sh.bucket, c); relErr != nil {
				return userErr("%s was not filed: %v (the claim this run took could not be given back: %v)", issueRef(i), err, relErr)
			}
		}
		return userErr("%s was not filed: %v", issueRef(i), err)
	}
	if c != nil {
		// F22: the URL, `submitted` and the instant are written with the claim
		// cleared, in the one write conditioned on still holding it.
		if err := issues.RecordSubmitted(ctx, sh.bucket, c, url, mineNow()); err != nil {
			// The issue IS on GitHub — gh printed its URL — so this is reported
			// with the URL rather than as a failure to file: a person told
			// "nothing was filed" would file the same bug a second time by hand.
			fmt.Fprintf(out, "Filed %s as %s.\n", issueRef(i), url)
			return userErr("the issue was filed, but the project's store could not record it: %v", err)
		}
	}
	i.URL, i.Status = url, issues.StatusSubmitted
	if err := store.Save(i); err != nil {
		// The issue IS on GitHub, so this is reported the same way the store's
		// own failure is (F19): with the URL.
		fmt.Fprintf(out, "Filed %s as %s.\n", issueRef(i), url)
		return userErr("the issue was filed, but its record could not be written: %v", err)
	}
	_, err = fmt.Fprintf(out, "Filed %s as %s.\n", issueRef(i), url)
	return err
}

// issueClaim takes the project's claim on one issue, and answers with a refusal
// instead when the issue is somebody else's to file, or the record the preview
// was built from is gone (F19, F22).
//
// The claim is taken only after the yes (issueFile): a prompt left open holds
// nothing, so an issue nobody is filing is never made to wait out a lease. It
// names who holds it by the credential this machine can stand behind (F3): a
// name the team reads in a claim is the one a filing is attributed to, and the
// cache is not a name a write may carry.
//
// A claim taken on a record that is not the one previewed is given back and
// refused, and it is the comparison this whole file is for: the store's writes
// are conditioned on the version they read, and the two reads — the preview's
// and the claim's — have to be the same record for those conditions to mean
// "the bytes you read". A record that changed in between is the one case the
// person's yes was not given for, and filing it would put text nobody read on
// GitHub under their account.
func issueClaim(cmd *cobra.Command, sh *issueShared, i issues.Issue) (*issues.Claimed, error) {
	ctx := cmd.Context()
	if sh.bucket == nil {
		return nil, userErr("%s was not filed: the project's issue is filed under a claim, and %s", issueRef(i), sh.why())
	}
	holder, err := issues.VerifiedOnly(ctx, sh.deps)
	if err != nil {
		return nil, userErr("%s was not filed: the credential that would name the claim could not be read: %v", issueRef(i), err)
	}
	c, err := issues.Claim(ctx, sh.bucket, i.Number, holder.Address, mineNow())
	if err != nil {
		var taken *issues.ClaimedError
		if errors.As(err, &taken) {
			return nil, userErr("%s was not filed: %s is filing it until %s", issueRef(i), taken.Holder, taken.Expires.UTC().Format(time.RFC3339))
		}
		return nil, userErr("%s was not filed: %v", issueRef(i), err)
	}
	if !issueSame(i, c.Issue) {
		if relErr := issues.Release(ctx, sh.bucket, c); relErr != nil {
			// The claim is spent rather than released: it lapses on its own at
			// the time it was taken for, which is how long the next submitter
			// waits — and that is worth the line.
			return nil, userErr("%s was not filed: the project's record of it changed while you were reading the preview; run the command again (the claim this run took could not be given back: %v)", issueRef(i), relErr)
		}
		return nil, userErr("%s was not filed: the project's record of it changed while you were reading the preview; run the command again to file the record that is there now", issueRef(i))
	}
	return c, nil
}

// issueSame is whether two reads of one record are the same record. Marshal is
// the record's own codec and writes the same bytes for the same record, so two
// reads that differ anywhere — a summary, a line of the diagnosis, a status — are
// two records here (F19).
func issueSame(a, b issues.Issue) bool {
	ra, err := issues.Marshal(a)
	if err != nil {
		return false
	}
	rb, err := issues.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ra, rb)
}

// issueConfirm is D1720's confirmation: the yes that files. It is `--ask` and
// nothing else — D1544's one confirmation flag, with no --yes beside it — and
// it is reached only with the flag passed, because issueSubmit answers a run
// without it by printing the preview and filing nothing.
//
// The read is askAtTerminal's, the package's one read of a confirmation, so
// this command extends the flag's helper rather than carrying a second body of
// the same three answers (T1). What it passes as the refusal is its own line
// (issueAskRefusal, F24), and it is worth saying why that is not askConfirm:
// askConfirm answers true for a run that did not pass the flag, because the
// steps it serves apply unless they are refused, while this command's no-flag
// answer is that nothing is filed at all — an askConfirm here would file every
// issue a plain `fugaro issue submit` names, which is the one thing D1720 was
// written against.
func issueConfirm(cmd *cobra.Command) (bool, error) {
	return askAtTerminal(cmd, "\nFile this issue on github.com as it is shown above?", issueAskRefusal)
}

// issueAskRefusal is why this command's confirmation cannot be answered, in a
// line of its own (F24). initflow.AskRefusal is the managed gate's sentence —
// it is about the prompts a project's plan gate stood in for and about the
// verdict that is deliberately not taken instead — and this command has no
// gate: the yes at a terminal is the only thing that files an issue, so the
// refusal says what is missing instead of naming another command's marks.
//
// The agent case is initflow's own refusal, the one `dismiss` and `recipes
// publish` give in the same session (F10), so the two commands that refuse a
// coding agent here refuse it in one sentence.
//
// The four cases are the sessions askAtTerminal can refuse in, and not the four
// flags this command carries: --json and --non-interactive are not flags of
// `submit` today, so a run reads them as false and their two sentences wait for
// the day one is added — which is why the switch answers the conditions rather
// than the flag list, and why the help says what a pipe and a coding agent's
// session do rather than naming a flag nobody can pass.
func issueAskRefusal(c initflow.Conditions) string {
	switch {
	case c.Agent != "":
		return "nothing was filed: " + initflow.AgentRefusal(c.Agent)
	case c.NonInteractive:
		return initflow.NeedsTerminal("filing an issue on GitHub") + ", and --non-interactive never prompts: nothing was filed"
	case c.JSON:
		return initflow.NeedsTerminal("filing an issue on GitHub") + ", and --json never prompts: nothing was filed"
	default:
		return initflow.NeedsTerminal("filing an issue on GitHub") + ": nothing was filed"
	}
}

// issueFallback is what a machine without `gh`, or without an account on
// github.com, is given instead of a filing (spec 5): the line that says what is
// missing, and then, for every issue the run would have filed, its preview, the
// new-issue link carrying the same title and body, and the command that prints
// the whole issue here.
//
// Nothing is filed and the run exits non-zero: the person asked for a filing
// and did not get one, and a zero would tell a script the issue is on GitHub.
func issueFallback(cmd *cobra.Command, todo []issues.Issue, names []string, state issueGHState) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintln(out, state.line); err != nil {
		return err
	}
	for _, i := range todo {
		p := issues.BuildPost(i, names)
		if _, err := fmt.Fprintf(out, "\n%s\nOpen this link to file it in a browser:\n%s\n\nThe whole issue is on this machine: fugaro issue show %s\n",
			issues.Preview(p), issues.NewIssueURL(p), p.Ref); err != nil {
			return err
		}
	}
	return userErr("nothing was filed: %s", state.why)
}

// issueGHState is whether this machine can file at all, and the two lines that
// say why not (spec 5): the install hint when `gh` is missing, and `gh auth
// login` when it is there with no account on the host the post goes to.
//
// It is the whole of what a person is told, printed once for the run and not
// once per issue: a machine with five waiting issues gets one line about the
// one thing that is wrong and five links, which is the shape setup's gh hint
// is printed in too.
type issueGHState struct {
	// ok is true when `gh` is installed and logged in to github.com.
	ok bool
	// line is the whole sentence printed for a machine that cannot file, and
	// why the shortened reason the error carries after it.
	line, why string
}

// readIssueGHState reads the two questions `gh` is asked before the post is
// built (issues.GH). A no to either is a machine that cannot file, and the two
// are told apart because the answer to a person is different: install `gh`, or
// log in to it.
//
// A login that cannot be read — a network that just blipped, which is why
// scripts/retry.sh retries `gh` on any failure — is answered as a login that is
// missing: LoggedIn is a bool, so there is no failure to tell apart from an
// account that is not there, and `gh auth login` is the answer that costs a
// person nothing when the account was simply absent.
func readIssueGHState(ctx context.Context, gh issues.GH) issueGHState {
	if !gh.Installed() {
		return issueGHState{
			line: "GitHub's `gh` is not installed on this machine, so nothing can be filed from here. Install it with: " +
				issues.InstallHint(runtime.GOOS, issueOSRelease()),
			why: "gh is not installed",
		}
	}
	if !gh.LoggedIn(ctx) {
		return issueGHState{
			line: "`gh` has no account on github.com, so nothing can be filed from here. Run: gh auth login",
			why:  "gh is not logged in to github.com",
		}
	}
	return issueGHState{ok: true}
}

// issueNames is the names the post is scrubbed of (F4): the project's own name,
// its GCP project and the repositories under it, as the selected config knows
// them.
//
// Scrub is not a boundary (F24) and a machine with no project selected passes
// no names: the post is then projected and re-redacted like every other, and
// the names a config nobody selected would have listed survive. That is the
// same answer a machine with no config has for every other command here — the
// issues folder is the user's own, and `submit` works in a directory that names
// no project at all. It works there on the records that name no project either
// (issueForeign): the names that survive are the ones of an issue no config
// here wrote, and not another project's, which is not filed from here at all.
func issueNames(lc *localcfg.Config) []string {
	if lc == nil {
		return nil
	}
	names := []string{lc.Name, lc.GCPProject}
	for slug := range lc.Repos {
		names = append(names, slug)
		// The slug is what the record's own text is most likely to carry, and
		// its two halves are what a sentence uses instead: `acme/widget` in a
		// path, `widget` in prose.
		if owner, name, ok := gitprov.SplitRepo(slug); ok {
			names = append(names, owner, name)
		}
	}
	return names
}
