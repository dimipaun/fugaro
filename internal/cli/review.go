package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// reviewJobAdmitted is E7's second half — whether the installation's
// credentials service admits a review caller of a project-scoped job — and it
// is TRUE since 0.21.0, on the owner's ruling of 2026-11-07 for that release.
// It was false while the admission was C3.9 and unbuilt (§1): the service
// refused a caller whose account belongs to a shared project job whole
// (internal/credsvc's Global Constraint 6), and the review caller is exactly
// that.
//
// What made it true is two merges and one property of `fugaro update`, all of
// them on this tree: the service reads its review class out of
// FUGARO_REVIEW_JOB_ACCOUNTS beside its shared job table (CSRW1, PR #1932), the
// installation module renders that variable from the review-only variants the
// deployment creates (CSRW2, PR #1934), and `fugaro update` deploys the
// credentials service on every installation that can carry one (DON1) — so the
// class this constant is about is one the update puts in front of the launcher,
// and the owner's ruling is that the wiring, and not the live check, is what
// the flip waited on. The owner-run live checks (C3.9's eleven steps, Check
// 125's steps 6 and 7) remain the owner's and are what confirms the class on a
// live installation; no agent runs them and this constant is not a claim that
// they passed.
//
// It stays a constant because the launcher can ask the service nothing: there
// is no admin route to its configuration (D143), and the admission decision is
// not visible without a call that mints a credential. What the launcher can
// still judge is the installation the service was deployed with, and that is
// what checkReviewAdmission does — the class is compiled in here since 0.21.0,
// and a launch is refused while the installation's own release predates it.
//
// A binary older than this release keeps refusing whatever the installation in
// front of it admits, because the fact is compiled in; the refusal below says
// so. A test that has to reach that refusal sets the constant to false, the way
// compressionForTest's seam is set, so the refusal and the launch both stay
// covered.
var reviewJobAdmitted = true

// reviewOnlyRecipe is the catalog recipe a review launch runs: mode: review,
// exactly one --pr — the same recipe `fugaro run --pr N --recipe review-only`
// has always used. The command resolves it by name instead of carrying a copy,
// so the reviewer's own prompt stays one file (recipe/catalog/review-only.yaml)
// and a project or agent recipe may shadow it deliberately, exactly as it may
// for fugaro run.
const reviewOnlyRecipe = "review-only"

// reviewOptions are `fugaro review`'s own flags: the pull request, the account
// and the mutant. Everything else a launch needs — the repository, the ref, the
// workflow, the title — is resolved from the checkout, by reviewSpec, which is
// the same builder `fugaro run --recipe review-only` goes through: one task
// shape, and a review launched by either verb is the same task.
type reviewOptions struct {
	cloud  cloudOptions
	pr     int
	token  string
	mutate []string
	asJSON bool
}

func newReviewCmd() *cobra.Command {
	o := &reviewOptions{}
	cmd := &cobra.Command{
		Use:   "review --pr N",
		Short: "Review a pull request in a job of its own, on a named account",
		Long: "Launch an independent review of pull request N, in a container the coding\n" +
			"run never ran in and with the account you name.\n\n" +
			"The review runs on the review-only variant of the repository's job: no App\n" +
			"key, no Claude token and no signing key are mounted in it, and its git\n" +
			"credential is a read-only one it fetches from the installation's credentials\n" +
			"service. It reads a fresh clone of the pull request's head, writes its verdict\n" +
			"as an artifact (fugaro review show RUN prints it) and posts no comment and no\n" +
			"review on the pull request: what reaches the pull request is the owner's and\n" +
			"the coordinator's to decide.\n\n" +
			"The account is picked as everywhere else: --token, else agent.oauth_token in\n" +
			"fugaro.yaml at the run's ref, else the project's default_claude_account.\n\n" +
			"--mutate FILE:TEST asks for one mutation check: the reviewer flips a boolean\n" +
			"literal in FILE, which must be Go source in the head under review, and TEST —\n" +
			"a Go test name — must then fail. The artifact carries what was observed; it is\n" +
			"evidence and never a verdict.\n\n" +
			"--json prints the launch's own result, the run id, its job and the account, as\n" +
			"every other command's --json does. The verdict does not exist yet: it is\n" +
			"written by the job, and `fugaro review show RUN` reads it back.\n\n" +
			"A pull request whose own coding run is still going is refused: the review\n" +
			"records the head it read, and a head that moves under it would leave the\n" +
			"verdict about a commit that is no longer there.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runReviewLaunch(cmd, o) },
	}
	f := cmd.Flags()
	f.IntVar(&o.pr, "pr", 0, "the pull request to review (required)")
	f.StringVar(&o.token, "token", "", "named Claude account (claude_accounts in the local config) this review runs as; default: agent.oauth_token in fugaro.yaml at the run's ref, else the project's default_claude_account")
	f.StringArrayVar(&o.mutate, "mutate", nil, "ask for one mutation check, as <file:test>: the Go source in the reviewed head to flip and the Go test name that must then fail; at most one")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	addCloudFlags(cmd, &o.cloud)
	cmd.AddCommand(newReviewShowCmd())
	return cmd
}

func runReviewLaunch(cmd *cobra.Command, o *reviewOptions) error {
	ctx := cmd.Context()
	if o.pr <= 0 {
		return userErr("--pr: use the pull request to review, as --pr 7")
	}
	mutate, err := parseMutate(o.mutate)
	if err != nil {
		return err
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	repo, err := originRepo(ctx)
	if err != nil {
		return err
	}
	rr, err := resolveRecipe(ctx, env, checkoutRoot(ctx, repo), reviewOnlyRecipe, time.Now(), false)
	if err != nil {
		return err
	}
	if rr.Recipe.Mode != recipe.ModeReview {
		return userErr("the recipe %q this review resolves is not a mode: review recipe, so there is no review to launch: it reviews nothing and never pushes. A project, agent or repository recipe of that name shadows the catalog's; rename it, or review the pull request with fugaro run --pr %d --recipe %s", oneLineCLI(rr.Name), o.pr, reviewOnlyRecipe)
	}
	o2 := &runOptions{repo: repo, pr: []int{o.pr}, token: o.token, asJSON: o.asJSON}
	slug, spec, err := reviewSpec(ctx, env, o2, rr, "", 0, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	// The mode the task carries is what makes this launch a review for every
	// reader that never sees the command: finishLaunch's job variant and image
	// gate here, and the console's history of what a run was.
	spec.Mode = recipe.ModeReview
	spec.Mutate = mutate
	if err := spec.Validate(); err != nil {
		return userErr("%v", err)
	}
	if err := checkReviewLaunch(ctx, env, repo, slug, o.pr, spec, cmd.ErrOrStderr()); err != nil {
		return err
	}
	res, err := finishLaunch(ctx, env, o2, slug, spec, false, cmd.ErrOrStderr(), false)
	if err != nil {
		return err
	}
	// The job comes from the launch record and not from a second resolution:
	// the record is what the platform reported for THIS execution, so a name
	// this command recomputed could not disagree with the one that ran.
	l, err := runstore.Open(env.bucket.Bucket, slug, spec.RunID).ReadLaunch(ctx)
	if err != nil {
		return remote(err)
	}
	return printReviewLaunch(cmd.OutOrStdout(), res, l.Job, reviewAccount(spec), o.asJSON)
}

// parseMutate turns --mutate's <file:test> into the task's own ask. The split
// is at the LAST colon: a Go test name holds none (task's mutateTestRE), while
// a path may — and both halves are then shape-checked by task.Validate, which
// is the rule the runner applies to a hand-written task.json too, so a launcher
// and a bucket edit are held to one list rather than two.
func parseMutate(flags []string) ([]task.Mutate, error) {
	switch {
	case len(flags) == 0:
		return nil, nil
	case len(flags) > 1:
		return nil, userErr("--mutate asks for one mutation check, and one mutant is all a review runs; %d were given", len(flags))
	}
	ask := flags[0]
	i := strings.LastIndex(ask, ":")
	if i < 0 {
		return nil, userErr("--mutate %s: give the file and the test as <file:test>, such as internal/runner/reviewmode.go:TestReviewModeNeverPushes", oneLineCLI(ask))
	}
	return []task.Mutate{{File: strings.TrimSpace(ask[:i]), Test: strings.TrimSpace(ask[i+1:])}}, nil
}

// reviewAccount is the account a review launch resolved, as a document spells
// it: the task carries the unsuffixed default as no field at all, the way every
// other launch writes it (config.DefaultClaudeAccount).
func reviewAccount(spec *task.Spec) string {
	if spec.OAuthToken == "" {
		return config.DefaultClaudeAccount
	}
	return spec.OAuthToken
}

// checkReviewLaunch is every refusal a review makes before anything of it is
// written, in the order the operator would act on them: the credentials service
// the job fetches its credentials from (E7's first half, D893 (c)), the class
// that service admits (E7's second half — the binary's here, the installation's
// in checkReviewAdmission), the named account's own state (D882), and the pull
// request's coding run (spec §5).
//
// The pull request is the command's own --pr, which is checked positive before
// anything else happens, and not spec.ReviewPRs[0]: the task's own list is the
// same number (reviewSpec writes it from that flag) and is not read for its
// length here, so no caller can index an empty slice.
func checkReviewLaunch(ctx context.Context, env *cloudEnv, repo, slug string, pr int, spec *task.Spec, warn io.Writer) error {
	if err := checkReviewService(env); err != nil {
		return err
	}
	if err := checkReviewJobAdmitted(); err != nil {
		return err
	}
	if err := checkReviewAccountState(ctx, env, spec); err != nil {
		return err
	}
	return checkReviewHeadIsStill(ctx, env, repo, slug, pr, warn)
}

// checkReviewJobAdmitted is E7's second half — the service admitting a review
// caller of a project-scoped job — and it is a constant rather than a question
// (reviewJobAdmitted). Since 0.21.0 the answer is yes, so a launch goes on to
// the account's state and the pull request's head; the sentence below is what a
// binary older than that release answers, and it names the binary as the thing
// that is out of date rather than the installation: the admission is compiled
// in, so no installation state moves it, and the remedy is one upgrade — a
// newer fugaro, then the `fugaro update` that is the whole upgrade and that
// deploys the service's review class (DON1).
func checkReviewJobAdmitted() error {
	if reviewJobAdmitted {
		return nil
	}
	return userErr("this fugaro predates the release in which the credentials service admits the review job's own caller: the review's account belongs to a project-scoped job, and whether the service admits that class is not a question this launcher can ask it (E7's second half) — it is a fact the binary carries, and this one was built while the answer was no, so it refuses the launch whatever the installation in front of it admits. Upgrade Fugaro (`brew upgrade dimipaun/tap/fugaro`), then run `fugaro update` with the newer binary: that is the whole upgrade, and the command that deploys the service's review class beside the review-only job variant. Until then, review a pull request in the run's own job with fugaro run --pr N --recipe " + reviewOnlyRecipe)
}

// reviewJobFor is the job a review launch runs on: CSR7's review-only variant
// of the shared project job env.launchProjectJobFor resolved — the second job
// per (profile, provider, account), with its own service account, which mounts
// no secret at all — and never the shared job itself, which mounts the App key
// a review must not be able to read (D883). The variant is resolved from the
// coding job's own name through runner.ProjectJobOfPolicy, the computation the
// runner matches CLOUD_RUN_JOB against, so this launcher never parses a job name
// back (R2-5) and can never disagree with the policy about which profile,
// provider and account the job belongs to.
//
// A launch that resolved no shared project job has no variant to run on: a
// baked workflow keeps its per-repository job, a project that published no
// profiles has no project jobs at all, and a repository the profile does not
// admit is refused long before this. In each case there is nothing deployed
// beside the coding job to launch, and starting the coding job instead is the
// one thing this command exists not to do.
func (e *cloudEnv) reviewJobFor(ctx context.Context, coding *launchProjectJob) (*launchProjectJob, error) {
	if coding == nil {
		return nil, userErr("a review runs on the review-only variant of a shared project job, and this repository has none: its workflow runs on a per-repository job (a baked workflow, or a project that has published no profiles), beside which no variant is deployed. Read the review in the run's own job instead, with fugaro run --pr N --recipe " + reviewOnlyRecipe)
	}
	p, found, err := e.policy.read(ctx, e.lc)
	if err != nil {
		return nil, policyReadErr(err)
	}
	if !found || p == nil {
		return nil, userErr("a review runs on the review-only variant of %s, and this project has published no policy to derive it from", pluginwire.Printable(coding.Name))
	}
	profile, provider, account, ok := runner.ProjectJobOfPolicy(p, coding.Name)
	if !ok {
		return nil, userErr("the review-only variant of %s cannot be resolved: the published policy defines no such job, so there is nothing beside it to launch", pluginwire.Printable(coding.Name))
	}
	job := projectJobReviewFor(profile, provider, account, coding.Timeout)
	return &job, nil
}

// checkReviewService is E7's first half, and the half an owner can supply. A
// review runs on a container that mounts no secret at all (CSR7, O1): its
// read-only installation token and its account's model calls are both fetched
// from the installation's credentials service at the moment they are needed, so
// an installation that has not opted into one has nothing for the job to fetch
// anything from. The launch refuses rather than starting it, because the job's
// own failure would be the service's answer (or a missing variable) instead of
// the sentence that says what to do.
func checkReviewService(env *cloudEnv) error {
	if env.lc != nil && env.lc.CredentialsService != nil && env.lc.CredentialsService.URL != "" {
		return nil
	}
	return userErr("a review runs on the review-only job, which mounts no credential of its own: its read-only installation token and its account's model calls both come from the installation's credentials service, and this installation has none (credentials_service is not published in this project's config). An ordinary `fugaro update` deploys the service on every installation that can carry one, which needs the installation's Firebase backend (`fugaro update --firebase-project`) and at least one repository job; then `fugaro creds set github-app-key` writes the App the review's read-only token is issued for")
}

// checkReviewAccountState is D882's refusal: a review is never launched on an
// account whose mark holds it. The mark is the shared state the credentials
// service keeps (creds/marks/<name>) and the launcher reads it as any other
// state reader does — the same database, the same client the budget pre-check
// uses — so an exhausted account stops the launch here, with the reset time the
// upstream reported, rather than starting a job whose reviewer is refused a
// minute later and whose run is then retried by hand.
func checkReviewAccountState(ctx context.Context, env *cloudEnv, spec *task.Spec) error {
	name := reviewAccount(spec)
	lc, db, err := budgetDBFor(ctx, env.lc)
	if err != nil {
		return err
	}
	if err := checkDBProject(ctx, db, lc); err != nil {
		return remote(err)
	}
	marks, err := (&creds.RTDBStore{DB: db}).Marks(ctx)
	if err != nil {
		return remote(err)
	}
	state := accountStateOf(marks[name])
	if state.State == string(creds.StateAvailable) {
		return nil
	}
	return userErr("the Claude account %s is %s, so no review is started on it: a review is never retried for the account's own state, and a job launched now would be refused by the credentials service the moment its reviewer called. Pick another account with --token, or wait for this one", pluginwire.Printable(name), stateText(state))
}

// checkReviewHeadIsStill refuses a review of a pull request whose own coding run
// is still going (spec §5): the artifact records the head it read, and a head
// that moves under the review would leave a verdict about a commit that is no
// longer there. It reads the runs on the pull request exactly as a follow-up
// resolves them (resolvePR: the same window, the same bucket, no cache), and
// refuses on the same three states; a run whose objects cannot be read refuses
// too, because whether the head is moving is then unknown rather than false.
func checkReviewHeadIsStill(ctx context.Context, env *cloudEnv, repo, slug string, pr int, warn io.Writer) error {
	if warn == nil {
		warn = io.Discard
	}
	now := time.Now()
	rows, err := loadRows(ctx, env, lsFilter{slugs: []string{slug}, since: now.Add(-followUpLookback), pr: pr, warn: warn, noCache: true}, now)
	if err != nil {
		return err
	}
	for _, r := range rows {
		switch r.Status {
		case runview.StatusError:
			return userErr("run %s on PR #%d can't be read (%s), so whether the head is still moving is unknown; see fugaro diagnose %s", r.Run, pr, oneLine(r.Reason), r.Run)
		case runview.StatusLaunching, runview.StatusPending, string(runstore.StatusRunning):
			return userErr("run %s on PR #%d is still %s; a review reads a head that is not moving, so wait for it to finish (fugaro runs ls --pr %d --repo %s) or cancel it", r.Run, pr, r.Status, pr, repo)
		case runview.StatusUnlaunched:
			fmt.Fprintf(warn, "warning: run %s on PR #%d never launched; leaving it out\n", oneLine(r.Run), pr)
		}
	}
	return nil
}

// reviewLaunchJSON is `fugaro review --json`'s document: the launch's own
// result, the shape every command's --json shares, beside the two facts this
// command adds about it — the job the execution landed on (the review-only
// variant, from its launch.json) and the account it runs as.
type reviewLaunchJSON struct {
	launchResult
	Job     string `json:"job"`
	Account string `json:"account"`
}

// printReviewLaunch is printLaunch for a review: the same line and the same
// --json document every other launch prints, plus the job and the account —
// which job a review landed on is the whole point of the command, and it is not
// a field the shared shape carries.
func printReviewLaunch(w io.Writer, res launchResult, job, account string, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(reviewLaunchJSON{launchResult: res, Job: job, Account: account})
	}
	if err := printLaunch(w, res, false); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "  job %s\n  account %s\n", oneLine(job), pluginwire.Printable(account))
	return err
}

// reviewShowOptions are `fugaro review show`'s flags: the config selection every
// cloud command shares, and the one output shape.
type reviewShowOptions struct {
	cloud  cloudOptions
	asJSON bool
}

func newReviewShowCmd() *cobra.Command {
	o := &reviewShowOptions{}
	cmd := &cobra.Command{
		Use:   "show RUN",
		Short: "Print a review run's verdict artifact and its markdown",
		Long: "Print what a review run wrote: its verdict artifact (review.json), and the\n" +
			"same review rendered as markdown (review.md). RUN is <repo-slug>/<run-id>.\n\n" +
			"--json prints the artifact's own JSON alone, byte for byte as the review job\n" +
			"wrote it, so the bytes are the ones the service signed and a reader that\n" +
			"checks the signature (the sidecar object review.json.sig) is checking those\n" +
			"bytes.\n\n" +
			"A run that is still going has no artifact yet; the command says so rather than\n" +
			"printing an empty review.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runReviewShow(cmd, o, args[0]) },
	}
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "print the artifact's own JSON alone, exactly as the review job wrote it")
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

func runReviewShow(cmd *cobra.Command, o *reviewShowOptions, run string) error {
	ctx := cmd.Context()
	slug, id, err := parseRunRef(run)
	if err != nil {
		return err
	}
	env, err := openCloud(ctx, o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()
	s := runstore.Open(env.bucket.Bucket, slug, id)
	data, err := s.ReadFile(ctx, runstore.ArtifactPrefix+runner.ReviewArtifactName)
	if err != nil {
		return reviewArtifactUnreadable(run, err)
	}
	out := cmd.OutOrStdout()
	if o.asJSON {
		return writeObject(out, data)
	}
	md, err := s.ReadFile(ctx, runstore.ArtifactPrefix+runner.ReviewMarkdownName)
	if err != nil {
		return reviewArtifactUnreadable(run, err)
	}
	if err := writeObject(out, data); err != nil {
		return err
	}
	return writeObject(out, md)
}

// reviewArtifactUnreadable words a failed read of a review artifact. The one
// case worth telling apart from a bucket that refused the read is the run that
// has no artifact: a review still going (the job writes all three objects
// together, at the end), a run that is not a review at all, and a review whose
// sign call failed — which writes nothing (CSR3) — all land here, and the run's
// own record is what tells them apart.
func reviewArtifactUnreadable(run string, err error) error {
	if errors.Is(err, runstore.ErrNotFound) {
		return userErr("%s has no review artifact: the review job writes review.json when it finishes, so this is a run that is still going, a review whose signature could not be made (it then writes nothing), or a run that is not a review. See fugaro diagnose %s", oneLineCLI(run), oneLineCLI(run))
	}
	return remote(err)
}

// reviewFindings is the findings section a fix round's brief opens with: the
// issues of review run reviewRun (CSR5, D887), rendered by runner.FixFindings
// — bounded, quoted, nonce-delimited, and naming the review run — for the
// launcher to put in front of the follow-up's own instructions.
//
// reviewRun is the reference the launcher passed: <repo-slug>/<run-id>, or a
// bare run id, read from the repository this fix round is for. A reference
// that names another repository is refused, and so is a reference whose path
// and whose artifact disagree: the artifact is read from exactly where fugaro
// review show reads it, and its own claims are checked against this launch
// before any of its text is used, so a brief cannot quote a review of
// somewhere else nor name a run nobody read. The section that is rendered
// names the reference this launcher read, never the artifact's claim about
// itself.
//
// A verdict is a verdict only once its signature is checked (CSR10, D892(a)),
// so this reader is a VERIFYING one: the sidecar beside the artifact is
// verified by the installation's credentials service over the artifact's exact
// stored bytes before the verdict is read at all, and it FAILS CLOSED (spec
// §6): a read or a decode that fails, and every way the service's answer can
// be other than yes, refuses the launch — never "nothing to check" — because a
// brief whose findings nobody verified is worse than no run at all. Every
// refusal names the review run and quotes no field of the review's own prose
// (spec §7).
//
// The verdict is acted on only when it is the one that asks for a fix: a
// `fix first` becomes the brief, while a `ship` or a `reject` refuses and
// points at fugaro review show for the findings (Open item 2's answer,
// 2026-11-05) — a brief built from a verdict that asked for no fix is a fix
// round nobody asked for.
func reviewFindings(ctx context.Context, env *cloudEnv, repo, reviewRun string, pr int) (string, error) {
	slug, id := "", reviewRun
	if strings.Contains(reviewRun, "/") {
		var err error
		if slug, id, err = parseRunRef(reviewRun); err != nil {
			return "", err
		}
	} else if !task.ValidRunID(reviewRun) {
		return "", userErr("--review-run %q must look like 20260926-221530-a1b2, or <repo-slug>/<run-id>", oneLineCLI(reviewRun))
	}
	own, err := env.repoSlug(repo, checkoutOf(ctx, repo))
	if err != nil {
		return "", err
	}
	if slug == "" {
		slug = own
	} else if slug != own {
		return "", userErr("--review-run %s is a review of %s, and this fix round is for %s: a brief carries the findings of its own repository", oneLineCLI(reviewRun), oneLineCLI(slug), oneLineCLI(own))
	}
	ref := slug + "/" + id
	store := runstore.Open(env.bucket.Bucket, slug, id)
	data, err := store.ReadFile(ctx, runstore.ArtifactPrefix+runner.ReviewArtifactName)
	if err != nil {
		if errors.Is(err, runstore.ErrNotFound) {
			return "", reviewArtifactUnreadable(ref, err)
		}
		return "", remote(err)
	}
	art, err := runner.DecodeReviewArtifact(data)
	if err != nil {
		return "", userErr("%s has no readable review artifact: %s. A fix round carries a review's findings into its brief, so a verdict this launcher cannot read refuses the launch rather than starting a run that fixes nothing. See fugaro review show %s", oneLineCLI(ref), safetext.Field(err.Error()), oneLineCLI(ref))
	}
	switch {
	case art.ReviewRun != id:
		return "", userErr("--review-run %s holds the artifact of review run %s: a brief carries the findings of the review it names", oneLineCLI(ref), oneLineCLI(art.ReviewRun))
	case art.Repository != slug:
		return "", userErr("--review-run %s holds the artifact of a review of %s: a brief carries the findings of its own repository", oneLineCLI(ref), oneLineCLI(art.Repository))
	case art.PR != pr:
		return "", userErr("--review-run %s holds the artifact of a review of pull request #%d, and this fix round is for #%d: a brief carries the findings of the review it names", oneLineCLI(ref), art.PR, pr)
	}
	// The sidecar, read from the same prefix the artifact was read from: the
	// digest the service is asked about is the SHA-256 of the bytes above, so
	// the signature and the object are the same read.
	sig, err := store.ReadFile(ctx, runstore.ArtifactPrefix+runner.ReviewSignName)
	if err != nil {
		if errors.Is(err, runstore.ErrNotFound) {
			return "", userErr("%s has no signature beside its review artifact (%s is absent), and an unverified verdict is not one to act on: a fix round is launched only on a review the installation's credentials service signed. See fugaro review show %s", oneLineCLI(ref), runner.ReviewSignName, oneLineCLI(ref))
		}
		return "", remote(err)
	}
	// The account the verify call names has ONE source: the account of the
	// review job this launcher resolved, which the review run's own task
	// carries (the axis's empty account is spelled as no field, so the mapping
	// is the launch's own rule). The artifact has no account field by design —
	// an artifact that could name the key that verifies it would choose its own
	// verifier — and the run's record is deliberately not a source (its
	// oauth_account is informational and written by the job itself).
	reviewSpec, err := store.ReadTask(ctx)
	if err != nil {
		return "", userErr("%s's own task cannot be read (%s), and the account a verdict is verified under is the review run's own: without it there is no key to ask about, so the brief is refused rather than built on a guess. See fugaro diagnose %s", oneLineCLI(ref), safetext.FieldErr(err), oneLineCLI(ref))
	}
	// The head the call demands, from the forge rather than from the artifact:
	// a validly signed verdict for an earlier head of the same pull request is
	// a replay, and the artifact's own head_sha is the claim being checked.
	head, err := reviewHead(ctx, env, repo, pr)
	if err != nil {
		return "", err
	}
	verifier, err := reviewVerifier(ctx, env, reviewAccount(reviewSpec), id, slug)
	if err != nil {
		return "", err
	}
	want := runner.ReviewExpectation{ReviewRun: id, Repository: slug, PR: pr, HeadSHA: head}
	if _, err := runner.VerifyReviewArtifact(ctx, data, string(sig), want, verifier); err != nil {
		return "", userErr("%s: %s. A fix round carries a review's verdict into its brief only when the installation's credentials service verifies the signature over the artifact's own bytes, so this launch is refused rather than started on a verdict nobody checked. See fugaro review show %s", oneLineCLI(ref), safetext.Field(err.Error()), oneLineCLI(ref))
	}
	// The signature said the artifact is the one this launch is for; the
	// verdict says whether it asks for a fix at all. Only `fix first` does, and
	// the other two words are refused rather than carried: the findings of a
	// ship or a reject are one fugaro review show away, while a brief built
	// from them is a fix round nobody asked for.
	if art.Verdict != reviewFixFirst {
		return "", userErr("--review-run %s holds a review whose verdict is %q, and only a review that asks for a fix becomes a fix round's brief: read its findings with fugaro review show %s", oneLineCLI(ref), safetext.Field(art.Verdict), oneLineCLI(ref))
	}
	return runner.FixFindings(ref, art.Issues), nil
}

// reviewFixFirst is the one verdict word that asks for a fix round: the
// artifact's own closed set is ship, fix first and reject (runner's
// reviewVerdicts, held by DecodeReviewArtifact), and the launcher acts on the
// one of them that asks for the work this launch does.
const reviewFixFirst = "fix first"

// writeObject writes one object's bytes and a closing newline, so two objects
// printed one after the other stay two: review.json is stored without a
// trailing newline, and the end of the artifact and the start of the markdown
// would otherwise be one line.
func writeObject(w io.Writer, data []byte) error {
	if _, err := w.Write(data); err != nil {
		return err
	}
	if len(data) > 0 && data[len(data)-1] == '\n' {
		return nil
	}
	_, err := io.WriteString(w, "\n")
	return err
}
