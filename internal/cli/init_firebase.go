package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/place"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runindex"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// printVarsUngatedFirebase is update --firebase-project --print-vars's warning.
const printVarsUngatedFirebase = "--print-vars is ungated and makes no cloud call, so the Firebase root's admins list holds only --budget-admin and the local config's terraform.budget_admins: the GCP project's owners and editors are read from its IAM policy when the converge runs, and the Firebase project is not checked"

// databaseRegionNote is what --print-vars says about the budget's Realtime
// Database (LP2, D3089, D3091): where a NEW instance goes, from the
// installation's region, and the one thing a printed document cannot know —
// whether an instance already exists, and in which of the three locations.
// --print-vars runs no discovery by design, so the line stands in for the read
// and says as much; a converge finds the instance itself and, when its location
// is not what the mapping says, adds the discovery's own note saying which it
// is (DiscoverFirebase).
func databaseRegionNote(region string) string {
	loc := place.For(region).RealtimeDatabase
	note := "the budget's Realtime Database instance goes in " + loc.Where
	if loc.Reason != "" {
		note += ", because " + loc.Reason
	}
	return note + "; an instance that already exists keeps the location it was created in, whatever this document says"
}

// firebaseVars is the installation's and the Firebase root's tfvars as one
// JSON object, for --print-vars.
//
// It carries the workspace pin's account for the same reason the apply does: a
// member whose config names a workspace has the read bound to that account, and
// a printed document that dropped the key would disagree with the installation
// it stands for — the reading of it an owner cannot trust. The pin is in the
// local config, so it needs no cloud call (what --print-vars by design makes
// none of), unlike the accounts that come from an apply's outputs — the
// history, coordinator and issuer accounts — which are therefore absent here.
func (r *initRun) firebaseVars(inst []byte, spec infra.InstallationSpec, lc *localcfg.Config) ([]byte, error) {
	fs, err := infra.Firebase(spec, infra.FirebaseInputs{
		FP:                      r.o.firebaseProject(),
		BudgetAdmins:            r.budgetAdmins(lc),
		WorkspaceServiceAccount: lc.WorkspacePin.ServiceAccount,
	})
	if err != nil {
		return nil, initErr(err)
	}
	fb, err := infra.FirebaseVars(fs)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(map[string]json.RawMessage{"installation": inst, "firebase": fb}, "", "  ")
}

// budgetAdmins are the further budget admins: the flags', else the local
// config's.
func (r *initRun) budgetAdmins(lc *localcfg.Config) []string {
	if r.o.budgetAdminsChanged {
		return slices.Clone(r.o.budgetAdmins)
	}
	return slices.Clone(lc.Terraform.BudgetAdmins)
}

// historyJob completes the installation spec's history job: it is deployed
// only when the Firebase root's outputs are known (they go into its
// environment) and its image exists. Otherwise the installation keeps the
// history account alone, and a job that is already there is kept by the next
// run that has both.
func (r *initRun) historyJob(ctx context.Context, c *infra.Clients, lc *localcfg.Config, spec infra.InstallationSpec) (infra.InstallationSpec, error) {
	if spec.History == nil {
		return spec, nil
	}
	h := *spec.History
	if r.fb != nil {
		h.FirebaseProject, h.RTDBURL = r.fb.FirebaseProject, r.fb.RTDBURL
	}
	h.DeployJob = false
	if h.FirebaseProject != "" && h.RTDBURL != "" {
		ok, err := infra.HistoryImageExists(ctx, c, lc, h)
		if err != nil {
			return spec, initErr(err)
		}
		h.DeployJob = ok
		if !ok && !r.historyNoted {
			r.historyNoted = true
			r.warn(fmt.Sprintf("the history job (the sweeper of crashed runs) is not deployed, so crashed runs are not cleaned up and old run users are not deleted: its image %s does not exist, and nothing builds it for you (like the dev base image until M7). From a checkout of this repository run\n  sh images/build-base.sh history %s\n  docker push %s\nthen run fugaro update --firebase-project %s again (gcloud auth configure-docker %s lets docker push there)", h.Image, h.Image, h.Image, firebaseProjectIDWord(lc.GCPProject), strings.SplitN(h.Image, "/", 2)[0]))
		}
	}
	spec.History = &h
	return spec, nil
}

// coordinatorJob completes the installation spec's aggregation job: it is
// deployed only when the Firebase root's outputs are known (they go into its
// environment), its image exists and the credential its repositories need is
// recorded. Which credential that is depends on what it reconciles (F24): its
// GitHub path mints the coordinator's own read-only installation token from
// the App, so a job reconciling a GitHub repository with no App ID would fail
// every tick it ran, while its Bitbucket path mints a read token at the
// credentials service (BB5) and asks for no App at all. Otherwise the
// installation keeps the coordinator's account alone, which is what the
// Firebase root's grant needs, and a job that is already there is kept by the
// next run that has everything it needs.
//
// A missing image is not said here: it is the history job's own image
// (infra.Coordinator's spec takes HistoryImage), and historyJob's warning
// names it once per run.
func (r *initRun) coordinatorJob(ctx context.Context, c *infra.Clients, lc *localcfg.Config, spec infra.InstallationSpec) (infra.InstallationSpec, error) {
	if spec.Coordinator == nil {
		return spec, nil
	}
	cs := *spec.Coordinator
	if r.fb != nil {
		cs.FirebaseProject, cs.RTDBURL = r.fb.FirebaseProject, r.fb.RTDBURL
	}
	cs.DeployJob = false
	switch {
	case cs.FirebaseProject == "" || cs.RTDBURL == "" || len(cs.Repos) == 0:
		// Nothing to reconcile, or nowhere to write it: the account alone.
	case cs.GitHubAppID == "" && coordinatorReadsGitHub(cs.Repos):
		if !r.coordinatorNoted {
			r.coordinatorNoted = true
			r.warn(fmt.Sprintf("the coordinator's aggregation job is not deployed: the local config records no GitHub App ID, and the sweep reads its GitHub repositories through a token minted from one. Onboarding a GitHub repository records it (fugaro update --repo <owner/name> --github-app-id <id>), and the next fugaro update --firebase-project %s deploys the job.", firebaseProjectIDWord(lc.GCPProject)))
		}
	default:
		ok, err := infra.CoordinatorImageExists(ctx, c, lc, cs)
		if err != nil {
			return spec, initErr(err)
		}
		cs.DeployJob = ok
	}
	spec.Coordinator = &cs
	return spec, nil
}

// coordinatorReadsGitHub reports whether the sweep reads a GitHub repository,
// which is the whole of whether its job needs the App credential (F24). It is
// asked of the repositories and not of the config, the way the module's own
// coordinator_github is: a repository the config records no provider for is
// not in the spec's list at all (infra.CoordinatorRepos), so "is there a
// github entry" is exactly "does the sweep read GitHub".
func coordinatorReadsGitHub(repos []infra.CoordinatorRepo) bool {
	for _, r := range repos {
		if r.Provider == gitprov.KindGitHub {
			return true
		}
	}
	return false
}

// applyRoot plans, shows, guards and (once confirmed) applies the root t
// runs, which is not the installation (installRoot does that). what says
// where; stop means --plan-only ended the run.
//
// imports are the import blocks the root's discovery wrote: the run's one
// confirmation covers an import only when it is one of them.
//
// The plan, the gate and the apply are one cycle, run again by
// propagationRetry exactly as the installation's, the repository's and the
// compute root's are, and for the same reason: the Firebase root creates the
// token signer and the bindings that name it in one apply
// (modules/firebase/iam.tf), so an IAM front end that has not caught up with
// the account fails the bindings -- and a fresh project's first
// `fugaro update --firebase-project` would otherwise stop there and ask for a
// re-run. A retry re-plans and re-judges the new plan; it never applies the
// saved plan of the failed attempt.
func (r *initRun) applyRoot(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, root, what string, imports []tf.ImportKey) (applied, stop bool, err error) {
	cycle := func(ctx context.Context) (*tf.Plan, error) {
		changed, err := t.Plan(ctx, infra.PlanFile)
		if err != nil {
			return nil, remote(err)
		}
		if !changed {
			fmt.Fprintf(r.stageDetail(), "No changes: the %s root matches the plan.\n", root)
			stop = r.o.planOnly
			return nil, nil
		}
		plan, err := t.Show(ctx, infra.PlanFile)
		if err != nil {
			return nil, remote(err)
		}
		r.planBlock(plan)
		counts := infra.CountPlan(plan)
		// The managed gate first, and the guard and the confirmation only when it
		// does not admit this plan (D1016, update_managed.go).
		gated, err := r.gateStep(ctx, t, imports, r.coverOfFirebase(), nil, nil, nil)
		if err != nil {
			return plan, err
		}
		if !gated {
			if err := r.guard(ctx, c, plan, r.o.allowDelete); err != nil {
				return plan, err
			}
			if r.o.planOnly {
				fmt.Fprintln(r.w, r.planOnlyLine(filepath.Join(wd.Root, infra.PlanFile)))
				stop = true
				return plan, nil
			}
			if err := r.confirmPlanned("applies "+counts.String()+" "+what, "nothing was applied", plan, r.notCoveredFirebase(plan, imports)); err != nil {
				return plan, err
			}
		}
		if err := r.applyAndRecord(ctx, t); err != nil {
			return plan, err
		}
		applied = true
		return plan, nil
	}
	if err := propagationRetry(ctx, r.w, "the Firebase step", cycle); err != nil {
		return false, false, err
	}
	return applied, stop, nil
}

// initFirebase is fugaro update --firebase-project <gcp-project-id>: it adopts the project's own
// Firebase project and builds the budget backend in it.
//
//  0. checks, before anything is applied: the Firebase project exists, is
//     active and has billing; the members who get roles on it are users,
//     groups or plain service accounts (never one of ours); the GCP
//     project's owners and editors are read; a database that already holds
//     data must carry our mark.
//  1. the installation root, which creates the history account, so it
//     exists before the Firebase root grants it anything;
//  2. the Firebase root (its own state, fugaro/firebase);
//  3. Identity Platform (initialized over REST when missing, never changed
//     when there, refused when it enables public sign-up) and the database: the rules, the mark, the project name, the mode and the
//     largest lease, and the local config;
//  4. (firebaseHistory, the installation-2 stage) the installation root
//     again, which deploys the history job with the Firebase outputs once
//     its image exists.
//
// Each apply has its own plan and confirmation, and so does the database
// step. Nothing creates the Firebase project or links billing.
// specPtr is the ENGINE's spec, and every field this stage sets on it is set
// there and not on the copy below: the switch the installation root's
// `enable_budget` is outlives this stage (see the comment where it is set).
func (r *initRun) initFirebase(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, bin string, lc *localcfg.Config, specPtr *infra.InstallationSpec, path string, old []byte) error {
	spec := *specPtr
	// The project this stage works in. With the flag it is the flag's value,
	// which checkFirebaseProject has already held to the installation's own
	// project; with no flag (AB7, R2) it is the installation's own project,
	// which is the only one there is: Fugaro builds the budget backend in the
	// project `fugaro setup` created for the installation and nowhere else, so
	// the stage that runs with no flag has one candidate and not a choice.
	fp := cmp.Or(r.o.firebaseProject(), lc.GCPProject)
	if fp == "" {
		return userErr("no Firebase project: the project config names no gcp_project and no --firebase-project was given")
	}
	// The no-flag arm: adopt the database the installation already has, or
	// create one, or change nothing and say in one line why (R2). It runs
	// BEFORE this stage's first apply, so a run that may not create a database
	// leaves the installation exactly as it found it.
	if r.o.firebase == "" {
		// RI2: a converged installation is still this stage's while the Firebase
		// root's state does not carry the run index's composite indexes, which
		// nothing but this stage's apply creates (F10) — and while it does not
		// carry the workspace's read the pin names (WS1, D2988). Those are the
		// cases the stage's check lets through with the database recorded
		// (init_stages.go's firebaseSkip, and this call is the same question
		// asked again at the arm it decides). There is nothing to adopt or
		// create then, because the database is this machine's, so the arm below
		// is left out and the flag's own flow runs: the same discovery, the same
		// apply of the root, the same database steps.
		rootOwed := false
		if lc.Budget != nil && lc.Budget.RTDBURL != "" {
			rootOwed, _ = r.firebaseRootWait(ctx, c, bin, spec.StateBucket, fp, lc)
		}
		done := false
		if !rootOwed {
			var err error
			done, err = r.noFlagBudgetDatabase(ctx, c, t, wd, bin, lc, spec, path, old, fp)
			if err != nil {
				return err
			}
		}
		// r.declined is the whole of what a run that changed nothing has to say
		// (R2), and its spec stays the engine's own: it has no database here, so
		// the installation root creates no history account for the Firebase
		// root's grant to name. Every other answer means the database is this
		// run's — the state's own, which the local config now records, or one
		// this run is about to create — and the installation root it is about to
		// apply is what creates the history account, its custom role and the
		// grant that binds them (F28: the `enable_budget` variable is kept and
		// set once the database exists). Without it the first apply would answer
		// no history account in its outputs, and the Firebase root's grant would
		// have no member to name.
		if r.declined == "" {
			h, err := infra.History(lc)
			if err != nil {
				return initErr(err)
			}
			spec.EnableBudget, spec.History = true, &h
			// The ENGINE's spec and not this function's copy: the stages after
			// this one plan the same root — the credentials stage's apply, and
			// the second installation stage's, which is the history job's — and
			// a spec without the switch rebuilds the tfvars without the key and
			// plans the history account and its custom role away in the very run
			// that gave the installation its database.
			*specPtr = spec
		}
		if done {
			return nil
		}
	}
	mode := r.o.budgetMode
	if mode == "enforce" && (lc.Budget == nil || lc.Budget.PerRunUSD <= 0) {
		return userErr("--budget-mode enforce needs budget.per_run_usd in the project config (more than 0): it is the per-run cap the jobs enforce")
	}
	if lc.Budget != nil && lc.Budget.FirebaseProject != "" && lc.Budget.FirebaseProject != fp {
		return userErr("project %s already uses Firebase project %s (budget.firebase_project); one Fugaro project has one Firebase project (design D3, which may be the installation's own GCP project), so --firebase-project %s is refused", lc.Name, lc.Budget.FirebaseProject, fp)
	}

	// 0. Discovery.
	if err := infra.CheckFirebaseProject(ctx, c, fp); err != nil {
		return initErr(err)
	}
	admins, skipped, risks, err := infra.ProjectAdmins(ctx, c, lc.GCPProject)
	if err != nil {
		return initErr(err)
	}
	for _, s := range skipped {
		r.warn("not a budget admin (owners and editors who are users or groups are; a service account can be added with --budget-admin): " + s)
	}
	if fp == lc.GCPProject && len(risks) > 0 {
		r.warn(fmt.Sprintf("the Firebase project is the installation's own project, and its default service account(s) hold a primitive role: %s. Anything that runs as one of them (a Cloud Build build submitted without a serviceAccount, a job or function deployed without one) can reach the database, Firestore and Identity Toolkit, which nothing else in the project should reach. Remove the role (gcloud projects remove-iam-policy-binding %s --member=serviceAccount:<account> --role=roles/editor) and set the organization policy iam.automaticIamGrantsForDefaultServiceAccounts. The converge changes nothing here", strings.Join(risks, ", "), lc.GCPProject))
	}
	if len(admins) == 0 && len(r.budgetAdmins(lc)) == 0 {
		return userErr("project %s has no user or group with roles/owner or roles/editor, and no --budget-admin: nobody could change a cap or a kill switch", lc.GCPProject)
	}
	// Refused here, before the first apply, whatever the installation module
	// accepts.
	// Every input the apply below passes and this point can know is passed here
	// too, the workspace pin's account included: this call is the gate that runs
	// before the installation root is applied, so a value the Firebase root
	// would refuse has to be refused here rather than after an apply has already
	// touched the state. The accounts that apply's own outputs carry — the
	// history, coordinator and issuer accounts — are the ones it cannot pass:
	// none of them exists until that root has been applied, which is the apply
	// below, and the issuer's member is read back from it rather than composed
	// here for the same reason (U9).
	if _, err := infra.Firebase(spec, infra.FirebaseInputs{FP: fp, Admins: admins, BudgetAdmins: r.budgetAdmins(lc), WorkspaceServiceAccount: lc.WorkspacePin.ServiceAccount}); err != nil {
		return initErr(err)
	}

	// The Firebase project's databases are checked now, before anything is
	// applied or granted on it: a database that holds unmarked data, or the
	// mark of another project or installation, is refused on a first run too.
	urls, err := infra.DatabaseURLs(ctx, c, fp)
	if err != nil {
		return initErr(err)
	}
	for _, u := range urls {
		if err := r.checkDatabase(ctx, lc, u, mode); err != nil {
			return err
		}
	}
	if mode == "enforce" && len(urls) == 0 {
		return initErr(infra.NoCapsForEnforce())
	}
	// Firestore is read now too (read-only), so a database in another
	// location, rules or a mark that are not ours are refused before the
	// first apply, and --plan-only shows the step. Before the Firebase root's
	// first apply the API may not be enabled yet: that is not a refusal, the
	// step is planned again after the apply.
	fsStep, err := r.firestoreStep(ctx, lc, fp)
	if err != nil {
		return err
	}
	if lines, _, err := fsStep.Plan(ctx); err != nil {
		if !errors.Is(err, infra.ErrFirestoreUnreadable) {
			return initErr(err)
		}
		if r.o.planOnly {
			fmt.Fprintf(r.w, "Firestore step of the Firebase project %s: not planned, it cannot be read yet (%v)\n", fp, err)
		}
	} else if r.o.planOnly {
		fmt.Fprintf(r.w, "Firestore step of the Firebase project %s (applied by the run that applies, never in --plan-only):\n", fp)
		for _, l := range lines {
			fmt.Fprintf(r.w, "  %s\n", l)
		}
	}
	// Whether the FP has its Firestore database, for the Firebase root's tfvars
	// (RI2, FirestoreStep.DatabaseExists). It is read from the plan made BEFORE
	// the apply below, which is the only one that says anything about the
	// database the apply will meet: the root's composite indexes are refused
	// under a database that is not there, and this stage creates the database
	// LAST, long after that apply (D3027). The step is planned again after the
	// apply and that second plan is the one deployFirestore acts on.
	//
	// This is the answer of a read that answered. One that did not — the API not
	// enabled yet, or a permission or Rules-API failure on the reads after the
	// database's own — answers false here, and false is the last thing the tfvars
	// may say for an installation that already has the indexes: an empty for_each
	// is a plan to destroy them. That case is decided from the Firebase state,
	// further down (runIndexIndexesManaged).
	dbExists := fsStep.DatabaseExists()

	// 1. The installation root.
	outs, stop, err := r.installRoot(ctx, c, t, wd, lc, spec)
	if err != nil || stop {
		return err
	}
	if outs.HistoryServiceAccount == "" {
		return remote(errors.New("the installation has no history account in its outputs; the first apply did not create it"))
	}
	// The coordinator's account is created by that same apply on an
	// installation whose owner opted in (P5), and the Firebase root below
	// grants it the FP's database admin: with no output to name it, that
	// grant would be planned for an account that may not exist, and IAM
	// refuses a member it cannot find. Without the opt-in there is no account
	// and no grant, and nothing to check.
	if spec.Coordinator != nil && outs.CoordinatorServiceAccount == "" {
		return remote(errors.New("the installation has no coordinator account in its outputs; the first apply did not create it"))
	}
	// The credentials service's own account, handed to the Firebase root so
	// that it plans the issuer's row on the runs' signer (LM13, D3121). It
	// comes from that same apply's outputs and never from a name composed
	// here, which is the whole of U9: the row's member has to be an account
	// that EXISTS, and the installation root creates it only where its tfvars
	// carry the service's block — a fresh setup's first apply carries none
	// (the credentials stage writes it later in the run), so the root
	// publishes no account and this root is handed none, and the plan names
	// nobody. An installation that does carry the service has the account by
	// this point: the installation root is applied above, and the block's
	// services and accounts are that apply's own resources.
	issuer := ""
	if cs := outs.CredentialsService; cs != nil {
		issuer = cs.Account
	}

	// 2. The Firebase root.
	fdir, err := infra.FirebaseWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return userErr("%v", err)
	}
	fwd, ft, err := r.terraform(bin, fdir, "firebase")
	if err != nil {
		return err
	}
	fspec, err := infra.Firebase(spec, infra.FirebaseInputs{FP: fp, Admins: admins, BudgetAdmins: r.budgetAdmins(lc), HistoryAccount: outs.HistoryServiceAccount, CoordinatorAccount: outs.CoordinatorServiceAccount, WorkspaceServiceAccount: lc.WorkspacePin.ServiceAccount, IssuerAccount: issuer, FirestoreDatabaseExists: dbExists})
	if err != nil {
		return initErr(err)
	}
	backend, err := fwd.WriteBackend(spec.StateBucket, infra.StatePrefixFirebase)
	if err != nil {
		return userErr("%v", err)
	}
	// No imports.tf.json is here yet: PrepareWorkdir (r.terraform) rebuilt
	// the root's tree, so only this run's discovery writes it, below, and a
	// refusal leaves none.
	if err := ft.Init(ctx, backend); err != nil {
		return remote(err)
	}
	// The state is read before discovery. terraform init and show read the
	// installation's state bucket, and on a first run Terraform's GCS
	// backend may create an EMPTY state object there (taking and releasing
	// its lock); nothing is written to the Firebase project. What the state
	// manages is Terraform's already, so discovery neither vets nor imports
	// it, and the plan, the guard and the confirmation handle it as they
	// always have. A first run has no state ({"format_version":"1.0"}, no
	// values), which manages nothing; a state that cannot be read stops the
	// run.
	st, err := ft.ShowState(ctx)
	if err != nil {
		return remote(err)
	}
	managed := map[string]bool{}
	for _, a := range st.Addresses() {
		managed[a] = true
	}
	// The run index's gate (RI2), decided here because this is where the state is
	// known — and the tfvars are written here, and not before the state was read,
	// for that reason: nothing in between reads the file (terraform init and show
	// take no variables) and the plan below does. The plan above answers only when
	// its read of the database answered at all; when it did not, the tfvars must
	// not say there is no database, because the module's for_each would then plan
	// away the composite indexes an installation already has. A state that manages
	// one is an installation that has them, and it keeps them. A state that
	// manages none keeps the plan's false, which is the direction that costs a
	// read and never a failed apply.
	if !fsStep.DatabaseKnown() {
		fspec.FirestoreDatabaseExists = runIndexIndexesManaged(managed)
	}
	vars, err := infra.FirebaseVars(fspec)
	if err != nil {
		return err
	}
	if err := fwd.WriteVars(vars); err != nil {
		return userErr("writing the Firebase tfvars: %v", err)
	}
	// Read-only discovery of the Firebase project's own singletons that the
	// state does not manage: a look-alike is refused before any plan or
	// apply and before imports.tf.json is written. The Firebase project
	// pays for these reads, as the root's provider does.
	dc, err := r.discoveryClients(ctx, c, lc, fp)
	if err != nil {
		return err
	}
	im, err := infra.DiscoverFirebase(ctx, dc, fspec, managed)
	if err != nil {
		return initErr(err)
	}
	for _, n := range im.Notes {
		r.warn(n)
	}
	// The file is written on every run, empty when there is nothing to
	// import.
	if err := infra.WriteImports(fwd.Root, im); err != nil {
		return userErr("writing the Firebase imports: %v", err)
	}
	what := fmt.Sprintf("to the Firebase project %s (the Realtime Database, a restricted web API key, the token signer and the grants on it)", fp)
	if names := adoptedNames(im); names != "" {
		what = fmt.Sprintf("to the Firebase project %s (adopting the existing %s; the Realtime Database, a restricted web API key, the token signer and the grants on it)", fp, names)
	}
	applied, stop, err := r.applyRoot(ctx, c, ft, fwd, "firebase", what, im.Keys())
	if err != nil || stop {
		return err
	}
	r.res.Applied = r.res.Applied || applied
	raw, err := ft.Output(ctx)
	if err != nil {
		return remote(err)
	}
	fo, err := infra.DecodeFirebaseOutputs(raw)
	if err != nil {
		return remote(err)
	}
	if fo.FirebaseProject != fp {
		return remote(fmt.Errorf("the Firebase root's firebase_project output is %s, not %s", fo.FirebaseProject, fp))
	}
	r.fb = &fo
	r.res.Firebase, r.res.RTDBURL = fp, fo.RTDBURL

	// 3. The database, then the local config.
	db, err := r.openDatabase(ctx, lc, fo.RTDBURL, mode)
	if err != nil {
		return err
	}
	idp, err := r.identityPlatform(ctx, lc, fp)
	if err != nil {
		return err
	}
	fsLines, fsWarnings, err := fsStep.Plan(ctx)
	if err != nil {
		return initErr(err)
	}
	for _, w := range fsWarnings {
		r.warn(w)
	}
	confirmed, err := r.deployDatabase(ctx, db, fp, idp, fsStep, fsLines)
	if err != nil {
		return err
	}
	r.mutate = func(next *localcfg.Config) {
		b := localcfg.Budget{}
		if next.Budget != nil {
			b = *next.Budget
		}
		b.FirebaseProject, b.RTDBURL, b.FirebaseAPIKey, b.TokenSigner = fo.FirebaseProject, fo.RTDBURL, fo.FirebaseAPIKey, fo.TokenSigner
		if mode != "" {
			b.Mode = mode
		}
		next.Budget = &b
		if r.o.budgetAdminsChanged {
			next.Terraform.BudgetAdmins = slices.Clone(r.budgetAdmins(lc))
		}
	}
	// The same backfill install() makes, and for the same reason: this door
	// applies the installation root itself (installRoot above), and
	// `fugaro update --firebase-project` runs no installation stage at all —
	// so an installation whose first post-upgrade command is this flag would
	// keep no per-repository record, and this release's launcher withholds the
	// service's address from the cascade jobs of every root the earlier apply
	// already stripped of its model mounts (D143). The record is the switch's
	// second condition, and the upgrade owes it on every door that writes the
	// local config.
	r.backfillCredentialsServiceReady(ctx, c, spec.StateBucket, lc, outs)
	if err := r.writeConfig(ctx, lc, spec, outs, path, old, confirmed); err != nil {
		return err
	}
	// D3027: the budget database and the local config that records it are
	// written, and only now is the run history reached. Nothing here waits on
	// a person: deployFirestore prints the line saying where the database goes
	// and creates it there with nothing asked, in every session this stage may
	// run in (D3090).
	return r.deployFirestore(ctx, fsStep)
}

// runIndexIndexesManaged reports whether the Firebase root's state manages any
// of the run index's composite indexes (RI2). It is the fallback for the gate
// the root's tfvars carry (FirestoreStep.DatabaseKnown), and it asks about any
// of them and not all: a run whose Firestore read did not answer must not plan
// away the indexes an installation already has — the module's for_each would be
// empty and the apply would destroy them — so one address in the state is
// enough to say they are there. Which of them are there is
// runIndexIndexesMissing's question, and it is the one the plain update's skip
// is decided by.
//
// firebaseIndexesInState reads the addresses the Firebase root's state manages,
// and whether that could be read at all (RI2). It is the read the plain
// update's Firebase stage is decided by — in front of the stage
// (init_stages.go's firebaseSkip) and again in its no-flag arm, so that a
// converged installation whose state does not carry them gets the apply that
// creates them.
//
// Every way it can fail answers "could not read", and the caller then leaves
// the installation exactly as it is. That direction is the whole of the safety:
// the update of a converged installation must not fail over a read the stage
// did not have to make, and it must not hand the root a state nobody could read
// either, because what a root plans under a state it never saw is the resources
// that are already there. The indexes such an update does not create are
// created by the update that can read.
func (r *initRun) firebaseIndexesInState(ctx context.Context, c *infra.Clients, bin, stateBucket, gcpProject string) (addresses map[string]bool, ok bool) {
	has, err := infra.StateExists(ctx, c, stateBucket, infra.StatePrefixFirebase)
	if err != nil || !has {
		return nil, false
	}
	fdir, err := infra.FirebaseWorkdir(os.Getenv, gcpProject)
	if err != nil {
		return nil, false
	}
	fwd, ft, err := r.terraform(bin, fdir, "firebase")
	if err != nil {
		return nil, false
	}
	backend, err := fwd.WriteBackend(stateBucket, infra.StatePrefixFirebase)
	if err != nil {
		return nil, false
	}
	if err := ft.Init(ctx, backend); err != nil {
		return nil, false
	}
	st, err := ft.ShowState(ctx)
	if err != nil {
		return nil, false
	}
	addresses = make(map[string]bool)
	for _, a := range st.Addresses() {
		addresses[a] = true
	}
	return addresses, true
}

// runIndexIndexesMissing lists the run index's composite indexes the Firebase
// root's state does not manage (RI2): the module's declared set
// (runindex.Indexes) minus the addresses the state holds. It is the whole of
// the answer to "does this installation carry the indexes yet" — all of them,
// not any of them — because a contract row added by a later release reaches a
// converged installation only through an apply of that root, and a state that
// carries the rows of the release before is exactly what such an installation
// has. The update that finds a row missing runs the stage; the one that finds
// none does not.
func runIndexIndexesMissing(managed map[string]bool) []string {
	var missing []string
	for _, key := range runindex.Indexes {
		if !managed[runindex.IndexAddress(key)] {
			missing = append(missing, key)
		}
	}
	return missing
}

// workspaceReadMissing reports whether the Firebase root's state leaves out the
// workspace's read of this project's database (WS1, D2988): the binding, and
// the one-permission custom role it carries, that the root declares for the
// workspace service account a member's own pin names
// (modules/firebase/workspace_access.tf). It is asked only of a config that
// pins one: an installation in no workspace passes "", the root's count is 0
// and the question is never asked of it, so nothing else's update moves.
//
// The pin is what makes the state's answer stale, and it is stale the moment a
// member joins: `fugaro workspace add` writes the pin, and no root has been
// applied with it since. The state is then the whole of the read — a state that
// carries the binding is one an update has already given it, whatever accidental
// state the project's IAM is in — which is what keeps a converged member's
// update converging.
func workspaceReadMissing(managed map[string]bool, lc *localcfg.Config) bool {
	if lc == nil || lc.WorkspacePin.ServiceAccount == "" {
		return false
	}
	for a := range managed {
		if strings.HasPrefix(a, "module.firebase.google_project_iam_member.workspace_read") {
			return false
		}
	}
	return true
}

// firebaseRootWait is RI2's and WS1's question at the plain update's own door,
// and the reason it is answered no. Two things are the Firebase root's and only
// an apply of that root makes them: the eleven composite indexes the run
// index's reads are served by (F10, RI2), and the workspace's read of a member's
// database (WS1, D2988). So a converged installation — F34's skip, and its
// caller has already read that this machine's config records the database —
// owes that apply while its root's state is readable, leaves out either of them
// (runIndexIndexesMissing: a row a later release added is a row such an
// installation has not been given yet; workspaceReadMissing: the pin a member
// wrote when it joined), and this credential may make the project's IAM changes.
//
// The last of those is F34's own reason for the skip, and it is the run
// that skip was written for: an operator's update, whose credential may not
// make the project's IAM changes an apply of that root makes, would fail there
// instead of converging. So such a run keeps the skip — with the reason, which
// firebaseSkip prints as its line — and a project owner's update creates what is
// missing. A member's own update is a project owner's for this purpose: the
// binding is handed to its database by the member's owner at their terminal,
// which is the same credential the pin was written with.
//
// Every way it can fail answers no, and every such run is left exactly as it
// was: an update that cannot read the state, or cannot ask about the
// credential, does not fail over it — what it does not create is created by the
// update that can. A pin whose state could not be read is one such run, and the
// direction is not a preference: the read that failed is also the only thing
// that could tell the root what it must not plan away, and a root handed a state
// nobody saw plans the resources that are already there away.
func (r *initRun) firebaseRootWait(ctx context.Context, c *infra.Clients, bin, stateBucket, fp string, lc *localcfg.Config) (wait bool, why string) {
	managed, ok := r.firebaseIndexesInState(ctx, c, bin, stateBucket, fp)
	if !ok {
		return false, " (the Firebase root's state could not be read, so the run index's composite indexes and the workspace's read wait for the update that can)"
	}
	indexes := len(runIndexIndexesMissing(managed)) > 0
	read := workspaceReadMissing(managed, lc)
	if !indexes && !read {
		return false, " (and the state already carries the run index's composite indexes and the workspace's read)"
	}
	if may, err := infra.CanMakeIAMChanges(ctx, c, fp); err != nil || !may {
		what := "the run index's composite indexes"
		switch {
		case read && !indexes:
			what = "the workspace's read of this project's database"
		case read && indexes:
			what = "the run index's composite indexes and the workspace's read of this project's database"
		}
		return false, " (and " + what + " wait for an update that may make project " + fp + "'s IAM changes, which an apply of the Firebase root does)"
	}
	return true, ""
}

func runIndexIndexesManaged(managed map[string]bool) bool {
	for a := range managed {
		if strings.HasPrefix(a, "module.firebase.google_firestore_index.") {
			return true
		}
	}
	return false
}

// discoveryClients are the clients that read the Firebase project's
// resources: the Firebase project is their quota project, not the
// installation's, when the two differ. The Resource Manager client stays
// the installation's (c.CRM), as CheckFirebaseProject's reads of the
// Firebase project are: a fresh Firebase project may not have the Resource
// Manager API enabled, and its project number is read before anything is
// applied there.
func (r *initRun) discoveryClients(ctx context.Context, c *infra.Clients, lc *localcfg.Config, fp string) (*infra.Clients, error) {
	if fp == lc.GCPProject {
		return c, nil
	}
	dc, err := newInitClientsFor(ctx, lc, fp)
	if err != nil {
		return nil, err
	}
	dc.CRM = c.CRM
	return dc, nil
}

// adoptedNames names the resources im imports, for the confirmation: the
// database and the key by their names, the signer, and the role by its ID.
func adoptedNames(im infra.Imports) string {
	last := func(id string) string { return id[strings.LastIndex(id, "/")+1:] }
	var names []string
	for _, i := range im.List {
		switch {
		case strings.HasSuffix(i.To, "google_firebase_database_instance.this"):
			names = append(names, "Realtime Database "+last(i.ID))
		case strings.HasSuffix(i.To, "google_apikeys_key.web"):
			names = append(names, "web API key "+last(i.ID))
		case strings.HasSuffix(i.To, "google_service_account.signer"):
			names = append(names, "token signer")
		case strings.HasSuffix(i.To, "google_project_iam_custom_role.token_minter"):
			names = append(names, last(i.ID)+" role")
		default:
			names = append(names, i.To)
		}
	}
	slices.SortStableFunc(names, func(a, b string) int { return cmp.Compare(adoptOrder(a), adoptOrder(b)) })
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// adoptOrder puts the database first, then the key, the signer and the role.
func adoptOrder(name string) int {
	switch {
	case strings.HasPrefix(name, "Realtime Database"):
		return 0
	case strings.HasPrefix(name, "web API key"):
		return 1
	case name == "token signer":
		return 2
	}
	return 3
}

// firebaseHistory is update --firebase-project's last step, the installation root
// again: it deploys the history job with the Firebase outputs once the
// history image exists (the images stage, before it, mirrors that image).
// It is the installation-2 stage's engine, split out of initFirebase so the
// images stage can run between them; run straight after initFirebase it is
// what initFirebase did.
func (r *initRun) firebaseHistory(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec) error {
	if r.fb == nil {
		return errors.New("the Firebase root's outputs are not known: the firebase stage did not run before the history job")
	}
	r.installLabel = " (the history job and its sweep schedule)"
	_, stop, err := r.installRoot(ctx, c, t, wd, lc, spec)
	if err != nil || stop {
		return err
	}
	fmt.Fprintf(r.w, "The budget backend of project %s is in Firebase project %s. Each repository needs fugaro update --repo to pick up the jobs' environment.\n", lc.Name, cmp.Or(r.o.firebaseProject(), lc.GCPProject))
	return nil
}

// openDatabase is the admin client of the database at url (the person's
// credentials).
func (r *initRun) openDatabase(ctx context.Context, lc *localcfg.Config, url, mode string) (*infra.DB, error) {
	cl, err := newBudgetClient(ctx, url, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, err
	}
	return infra.NewDB(cl, lc.Name, lc.GCPProject, mode), nil
}

// checkDatabase refuses a database of the Firebase project that holds
// unmarked data or another project's or installation's mark, and enforce on
// one without global caps, before the first apply.
func (r *initRun) checkDatabase(ctx context.Context, lc *localcfg.Config, url, mode string) error {
	db, err := r.openDatabase(ctx, lc, url, mode)
	if err != nil {
		return err
	}
	if _, err := db.Check(ctx); err != nil {
		return initErr(err)
	}
	return initErr(db.RequireCapsForEnforce(ctx))
}

// noFlagBudgetDatabase is the Firebase stage's own decision when the run names
// no project (AB7, R2), and it is made before this stage's first apply: the
// installation adopts the budget database it already has, or this run may not
// create one and says so in one line (R2), or the run goes on to create one.
//
// done reports that the stage is finished — the database was adopted and
// recorded, or r.declined says in one line why this run did nothing. done is
// false only where this run may create the database, and the `enable_budget`
// the caller sets for the applies below is what the false means (F28: the
// variable stays, and it is set once the database exists).
//
// The questions are asked in this order, and every answer but the last ends the
// stage without changing anything anywhere, so a run that may not create a
// database leaves the cloud exactly as it found it:
//
//  1. Is there a Firebase root state (F23)? A state whose outputs are this
//     project's IS the database: the run adopts it, records it in the local
//     config and applies nothing. (An installation whose database this machine
//     already records never reaches this call at all: the stage is skipped,
//     F34.)
//  2. May this credential make the project's IAM changes? Creating the database
//     needs the custom role, the account that wears it and the grant that binds
//     them (R2, and U1 is the question itself).
//  3. Can the project hold a database at all? One that does not exist, is not
//     ACTIVE or has no billing account linked gets the line that names it.
//
// What is left is the creation, and it ends where the flag's own path ends: the
// same discovery, the same two confirmed applies, the same database steps. A run
// whose ordinary confirmation nothing would answer never gets here at all: its
// stage is skipped at the check, with the same one line and nothing applied
// (F4, R2 — cannotCreateTheDatabase).
func (r *initRun) noFlagBudgetDatabase(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, bin string, lc *localcfg.Config, spec infra.InstallationSpec, path string, old []byte, fp string) (done bool, err error) {
	// F23: the probe is the Firebase STATE's and never the history account's.
	has, err := infra.StateExists(ctx, c, spec.StateBucket, infra.StatePrefixFirebase)
	if err != nil {
		return false, initErr(err)
	}
	if has {
		return r.adoptBudgetDatabase(ctx, c, t, wd, bin, lc, spec, path, old, fp)
	}
	// R2: whether the person can make the project's IAM changes.
	if ok, err := infra.CanMakeIAMChanges(ctx, c, fp); err != nil {
		return false, initErr(err)
	} else if !ok {
		r.declined = fmt.Sprintf("no budget database was created: this credential may not make project %s's IAM changes, which creating one needs (resourcemanager.projects.setIamPolicy, iam.roles.create and iam.serviceAccounts.create; roles/owner holds them); a project owner runs %s once", fp, selfCommand()+" "+r.o.doorName())
		return true, nil
	}
	// R2: a project that cannot hold a database is not this run's to fix. The
	// line names what the project needs, because the fix is a person's.
	if err := infra.CheckFirebaseProject(ctx, c, fp); err != nil {
		r.declined = oneLineCLI("no budget database was created: " + err.Error() + "; a project owner runs " + selfCommand() + " " + r.o.doorName() + " once the project can hold one (it needs a linked billing account, Blaze)")
		return true, nil
	}
	// F32: the state can be deleted while the database stays, and a create over
	// one that exists would be a second database. The project's databases are
	// listed before this run creates anything; one that is there is adopted by
	// the Firebase root's own discovery (the same apply, the same outputs), and
	// the run says which database it found instead of creating one beside it.
	urls, err := infra.DatabaseURLs(ctx, c, fp)
	if err != nil {
		return false, initErr(err)
	}
	if len(urls) > 0 {
		r.warn(fmt.Sprintf("project %s already has a Realtime Database (%s) and this machine records none: the Firebase root adopts what is there rather than creating a second one", fp, strings.Join(urls, ", ")))
	}
	// F4/R2's run never reaches this call: its stage is skipped at the check
	// (cannotCreateTheDatabase, newFirebaseStage's skip), which is where the
	// answer can be given before anything is applied. What is left here is the
	// creation, and every run that reaches it has an answer to give.
	return false, nil
}

// cannotCreateTheDatabase is F4/R2's answer, and whether it is this run's: a
// run whose ordinary confirmation nothing would answer — a coding agent's
// session, --json, a pipe, --non-interactive, and a run with no terminal —
// creates no database, and says in one line who must. The rest of the run
// carries on, which is the whole point of the line (R2).
//
// The managed gate is asked first and answers for the run: on a managed project
// this stage performs under it with no confirmation at all, a coding agent's
// session included (D3028, R11), so a run the gate stands in for is not a run
// that has nothing to answer with.
//
// It is asked at the stage's own check and not in the arm below, because a run
// it applies to never reaches the arm: the loop leaves a stage whose steps
// nothing can confirm to the user, which is the stop this line replaces.
func (r *initRun) cannotCreateTheDatabase() (string, bool) {
	if r.gateStandsIn() || r.stepConfirmation(true) != stepLeave {
		return "", false
	}
	return "no budget database was created: nothing answers this run's confirmation (no terminal, --json, a pipe and a coding agent never do); a project owner runs " + selfCommand() + " " + r.o.doorName() + " once", true
}

// adoptBudgetDatabase is the no-flag stage's adoption (R2): the installation's
// Firebase root state names a database that is this project's, so the run
// records it in the local config and applies nothing — no plan, no apply, no
// project read beyond the state's own outputs. It is the update door's half of
// budgetDatabaseFromState, which a teammate's new machine reaches through its
// own stage.
//
// A state that exists and whose outputs are not readable, not this project's,
// or not valid does NOT fall through to a creation: the database the state is
// about may be right there, and a second one is the thing this whole path
// exists to avoid (F32). The stage says its one line instead.
//
// The adoption needs no confirmation — it reads the state and writes this
// machine's config — so it is the one half of the no-flag stage a run nothing
// can confirm for would reach. It does not reach it: such a run's stage is
// skipped at the check (cannotCreateTheDatabase), and a plan-only run, which
// does reach it, writes nothing here.
func (r *initRun) adoptBudgetDatabase(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, bin string, lc *localcfg.Config, spec infra.InstallationSpec, path string, old []byte, fp string) (done bool, err error) {
	outs, err := r.readOutputs(ctx, c, t, wd, lc, spec)
	if err != nil {
		return false, initErr(err)
	}
	next := *lc
	if ok, why := r.budgetDatabaseFromState(ctx, c, bin, spec.StateBucket, fp, &next); !ok {
		r.declined = oneLineCLI("no budget database was recorded: " + why + "; this machine creates none over a Firebase root this installation already has, so a project owner runs " + selfCommand() + " " + r.o.doorName() + " once")
		return true, nil
	}
	// --plan-only writes no config, as it writes nothing else (newMachineConfig
	// is the same guard on the same write, one stage earlier): the reads above
	// are read-only and the line says what the run that applies would record.
	if r.o.planOnly {
		fmt.Fprintf(r.w, "--plan-only: the budget database of project %s is the one this installation already has (Firebase project %s, %s); the run that applies records it in the local config and applies nothing\n", lc.Name, pluginwire.Printable(next.Budget.FirebaseProject), pluginwire.Printable(next.Budget.RTDBURL))
		return true, nil
	}
	r.mutate = func(n *localcfg.Config) { n.Budget = next.Budget }
	r.backfillCredentialsServiceReady(ctx, c, spec.StateBucket, lc, outs)
	if err := r.writeConfig(ctx, lc, spec, outs, path, old, true); err != nil {
		return false, err
	}
	r.res.Firebase, r.res.RTDBURL = next.Budget.FirebaseProject, next.Budget.RTDBURL
	fmt.Fprintf(r.w, "the budget database of project %s is the one this installation already has (Firebase project %s, %s): the local config records it and nothing was applied\n", lc.Name, pluginwire.Printable(next.Budget.FirebaseProject), pluginwire.Printable(next.Budget.RTDBURL))
	return true, nil
}

// deployDatabase shows what it would write into the database (the mark, the
// project's name, the mode, the largest lease, the rules) and writes it once
// confirmed; a database that is already as wanted is left alone, with no
// confirmation. confirmed reports that a confirmation was given, which also
// covers the local config's diff.
//
// The budget database is written BEFORE the Firestore database is reached
// (D3027): the two are different databases, and the run history's location is
// permanent and is printed rather than asked for (locationLine, D3090), so
// nothing holds the budget database the installation needs to launch anything
// at all. This function therefore stops at the budget database: the caller
// writes the local config that records it and only then reaches the run
// history (deployFirestore).
func (r *initRun) deployDatabase(ctx context.Context, db *infra.DB, fp string, idp *idpStep, fs *infra.FirestoreStep, fsLines []string) (confirmed bool, err error) {
	acts, warnings, err := db.Plan(ctx)
	if err != nil {
		return false, initErr(err)
	}
	for _, w := range warnings {
		r.warn(w)
	}
	if len(acts) == 0 && !idp.missing && !fs.Pending() {
		fmt.Fprintln(r.w, "The database already has the rules, mark, project name and mode.")
		return false, nil
	}
	fmt.Fprintf(r.w, "The Firebase project %s:\n", fp)
	for _, a := range acts {
		fmt.Fprintf(r.w, "  %s: %s\n", a.Path, a.Text)
	}
	if idp.missing {
		fmt.Fprintf(r.w, "  Identity Platform: %s\n", infra.IdentityPlatformStep)
	} else {
		fmt.Fprintln(r.w, "  Identity Platform: already initialized, left as it is")
	}
	for _, l := range fsLines {
		fmt.Fprintf(r.w, "  %s\n", l)
	}
	what := "writes the database as listed, as you (project owner, editor or budget admin), and then the local config"
	if idp.missing {
		what = "writes the database as listed and " + infra.IdentityPlatformStep + ", as you (project owner, editor or budget admin), and then the local config"
	}
	if fs.CreatesDatabase() {
		what += fmt.Sprintf("; this does NOT create the Firestore database: the run creates it after the local config, in %s, printing where and creating it with nothing asked (its location is permanent)", fs.Location)
	}
	if err := r.confirmDatabase(what); err != nil {
		return false, err
	}
	if len(acts) > 0 {
		if err := db.Apply(ctx); err != nil {
			return false, initErr(err)
		}
		fmt.Fprintln(r.w, "wrote the database")
	}
	if idp.missing {
		if err := idp.initialize(ctx); err != nil {
			return false, err
		}
		fmt.Fprintln(r.w, "initialized Identity Platform (no sign-in providers)")
	}
	return true, nil
}

// confirmDatabase is the budget database's own confirmation: the stage's
// ordinary one, or the managed gate's sentence where the gate stands in for the
// stage's confirmations (D3028, R11 — on a managed project the database is
// created with nobody confirming, and from a coding agent's session too, which
// is the owner's own choice of the two answers O6 puts side by side).
//
// The gate is what answers here and not the run's review: the review is not
// asked on a managed project at all (runCovers' gateStandsIn arm), so a step
// that waited for it would wait for a confirmation nobody is shown. What is
// left to a person beyond this step is nothing of the Firestore database's: its
// location is permanent, and D3090 prints the line and creates it rather than
// asking anyone for it (locationLine).
func (r *initRun) confirmDatabase(what string) error {
	if r.gateStandsIn() {
		fmt.Fprintf(r.w, "  %s\n", r.gateProceeded())
		return nil
	}
	return r.confirmOrdinary(what, "the database was not written", r.firebaseReason())
}

// locationLine is the whole of what a person reads about the Firestore
// database's location: where it is created, and that a database's location can
// never be changed. No step confirms it (D3090): the line is printed and the
// database is created, in every session this stage may run in — a person's
// terminal, --json, a pipe and a coding agent's session alike.
//
// What keeps an agent out of a write is unchanged and is not this stage's: the
// refusal of cloud writes in an agent's session (D3028) and the managed gate.
// loc is where the database goes, which is the step's own location —
// place.For's answer for the installation's region (D3085) — and never the
// region itself: the two are different settings, and the location is the one
// that is permanent.
func (r *initRun) locationLine(loc string) {
	fmt.Fprintf(r.w, "the Firestore database is created in %s, and a database's location can NEVER be changed.\n", loc)
}

// deployFirestore is the run history's own database, and it is the LAST step of
// the stage — after the budget database has been written and the local config
// that records it has been written too (D3027). The two are different
// databases, and the run history is what waits on the local config that
// records the budget database the installation needs to launch anything at
// all.
//
// The line saying where the database goes comes first where there is no
// database at all (its location is permanent), and only then is anything
// written: the deny-all rules, the mark and — where the database was just
// created — the read-back that proves it is there.
func (r *initRun) deployFirestore(ctx context.Context, fs *infra.FirestoreStep) error {
	if !fs.Pending() {
		return nil
	}
	if fs.CreatesDatabase() {
		r.locationLine(fs.Location)
	}
	if err := fs.Apply(ctx); err != nil {
		return initErr(err)
	}
	// A database that appeared between the plan and the write, in the location
	// the plan named, is adopted and not created — and the run says where it
	// is, because nobody chose it in this run and its location is permanent.
	if loc := fs.AdoptedLocation(); loc != "" {
		fmt.Fprintf(r.w, "Cloud Firestore database: created by someone else after the plan, in %s, adopted as it is\n", loc)
	}
	fmt.Fprintln(r.w, "ensured the Firestore database, its deny-all rules and its mark (read back)")
	return nil
}

// firestoreStep builds the Firestore ensure step for the Firebase project fp
// (the person's credentials). With no_auth it needs both endpoints, so a
// fake run can never reach the real Google.
func (r *initRun) firestoreStep(ctx context.Context, lc *localcfg.Config, fp string) (*infra.FirestoreStep, error) {
	hc := &http.Client{Timeout: 90 * time.Second}
	var ts oauth2.TokenSource
	if lc.Endpoints.NoAuth {
		if lc.Endpoints.Firestore == "" || lc.Endpoints.FirebaseRules == "" {
			return nil, userErr("endpoints: no_auth is set but the firestore and firebase_rules endpoints are not")
		}
		ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "no-auth"})
	} else {
		var err error
		ts, err = google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, userErr("no Google credentials for Firestore: run gcloud auth application-default login (%v)", err)
		}
		hc = oauth2.NewClient(ctx, ts)
	}
	wait := time.Second
	if lc.Endpoints.NoAuth {
		wait = 10 * time.Millisecond
	}
	// The poll interval also paces the read-back after the database's
	// creation, which retries while the API's enablement propagates.
	fs, err := firestore.New(lc.Endpoints.Firestore, fp, ts, firestore.WithHTTPClient(hc), firestore.WithPollInterval(2*wait))
	if err != nil {
		return nil, userErr("%v", err)
	}
	m := place.For(lc.Region)
	return &infra.FirestoreStep{FS: fs, Rules: &infra.RulesClient{Endpoint: lc.Endpoints.FirebaseRules, Project: fp, HTTP: hc},
		FP: fp, Project: lc.Name, GCPProject: lc.GCPProject, Version: Version, Wait: wait,
		Location: m.Firestore.Where, Place: m.Place}, nil
}

// idpStep is the Identity Platform step: read before the confirmation, run
// after it.
type idpStep struct {
	c       *infra.IdentityPlatform
	missing bool
	// workspace is whether the project THIS step reads is a workspace's own,
	// which is the one project whose sign-in paths the owner enables: the
	// pinned ones pass and an off-pin one is refused (refusePublicSignUp).
	// A config declaring a workspace of its own is not enough for it: the read
	// has to be of the installation's own project as well, which is what
	// workspaceOwnFirebase reads, because the carve-out is about the project
	// the check is made against and not about the config.
	workspace bool
}

// identityPlatform reads the Firebase project's Identity Platform
// configuration (read-only). One that exists is never changed, and is refused
// when it enables a public sign-up path — unless the project read here is a
// workspace's own and the path is one the workspace's pin carries
// (refusePublicSignUp, and workspaceSignInPath for the pinned entries).
func (r *initRun) identityPlatform(ctx context.Context, lc *localcfg.Config, fp string) (*idpStep, error) {
	hc := &http.Client{Timeout: 90 * time.Second}
	if !lc.Endpoints.NoAuth {
		ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, userErr("no Google credentials for Identity Toolkit: run gcloud auth application-default login (%v)", err)
		}
		hc = oauth2.NewClient(ctx, ts)
	} else if lc.Endpoints.IdentityToolkit == "" {
		return nil, userErr("endpoints: no_auth is set but the identity_toolkit endpoint is not")
	}
	s := &idpStep{c: &infra.IdentityPlatform{Endpoint: lc.Endpoints.IdentityToolkit, Project: fp, HTTP: hc},
		workspace: workspaceOwnFirebase(lc, fp)}
	raw, exists, err := s.c.Config(ctx)
	if err != nil {
		return nil, remote(err)
	}
	s.missing = !exists
	if exists {
		if err := refusePublicSignUp(raw, fp, s.workspace); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// initialize initializes Identity Platform, then checks the configuration it
// finds (a race may have left one with sign-in enabled).
func (s *idpStep) initialize(ctx context.Context) error {
	if err := s.c.Initialize(ctx); err != nil {
		return remote(err)
	}
	raw, exists, err := s.c.Config(ctx)
	switch {
	case err != nil:
		return remote(err)
	case !exists:
		return remote(fmt.Errorf("Identity Platform was initialized in %s but its configuration is still not found", s.c.Project))
	}
	return refusePublicSignUp(raw, s.c.Project, s.workspace)
}

// refusePublicSignUp refuses an existing Identity Platform configuration that
// enables a public sign-up path: the web API key is public, so only custom
// tokens signed by our signer may work.
//
// A workspace's OWN project is the one exception to that refusal, and what
// marks it is the read being of that project (workspaceOwnFirebase): the
// installation's own config declaring the owner's half, and fp being the
// project the workspace's own is named after. A config that carries the
// member's half as well is a member's, because the two halves contradict each
// other and the narrow one decides. The workspace's sign-in is
// the owner's own Firebase project's, and the workspace's spec pins WHICH paths
// that project has on — email/password and google.com in, every other one out
// (docs/workspaces/spec.md §3) — so the owner enables providers here on purpose,
// and the pinned ones are admitted rather than refused. Refusing THEM would make
// `fugaro update` fail on exactly the project W3's root converges — the HARD
// RULE — and it would refuse the workspace's own sign-in, which is the point of
// that project; what this never does is REQUIRE them: a workspace whose
// providers the owner has not enabled yet (or has not enabled with the pinned
// pair) converges as usual, and the live read is where that is named
// (scripts/live-checks/check361.sh).
//
// A path outside the pin is a different matter, and the workspace's own project
// is not exempt from it. That project is also an installation, and its MCP
// sign-in reads a token's claim and not the provider that signed the user in
// (ee/coordinator/mcpauth/firebase.go's VerifyIDToken), so an off-pin provider
// that asserts a verified address would be admitted silently: an anonymous
// sign-in proves no address at all, and a provider an owner did not mean to
// carry — a second IdP, phone number — is an address this project accepts that
// nobody decided on. So here the workspace's own project is held to the pin in
// the one direction that costs no operator step: the pinned entries pass
// (workspaceSignInPath) and everything else is refused by name.
func refusePublicSignUp(raw []byte, fp string, workspace bool) error {
	public, err := infra.PublicSignUp(raw)
	if err != nil {
		return remote(err)
	}
	if len(public) == 0 {
		return nil
	}
	if workspace {
		var offPin []string
		for _, p := range public {
			if !workspaceSignInPath(p) {
				offPin = append(offPin, p)
			}
		}
		if len(offPin) == 0 {
			return nil
		}
		return userErr("Identity Platform in the workspace's own Firebase project %s enables sign-in paths that are not the workspace's pin (%s). The workspace's own project is the one Firebase in this design where a person signs in with a real provider, and the workspace's spec pins which: email/password and google.com, and nothing else (docs/workspaces/spec.md §3) — an anonymous sign-in proves no address at all, and any other provider is an address this project accepts that nobody decided on, admitted by this installation's own sign-in as well. Disable these in the Firebase console (Authentication, Sign-in method) or with the Identity Toolkit config API, then rerun. The converge never changes an existing configuration", fp, strings.Join(offPin, "; "))
	}
	return userErr("Identity Platform in Firebase project %s has public sign-up enabled (%s). The web API key is public, so only custom tokens signed by Fugaro's signer may work: disable these in the Firebase console (Authentication, Sign-in method) or with the Identity Toolkit config API, then rerun. The converge never changes an existing configuration", fp, strings.Join(public, "; "))
}

// workspaceSignInPath is whether one entry of PublicSignUp's list is a path spec
// §3's pin carries for the workspace's own project: email/password sign-in,
// google.com (either of the two lists the API reports an IdP under — the
// default-supported config and the OAuth one, both of which the workspace's
// console step writes), and the sign-up setting itself, which the pin does not
// constrain at all: the workspace's Firebase is the one that does not refuse
// public sign-up, which is what lets a person who does not already hold a
// password create one and reach the workspace (spec §3, and check 361's own
// reading). Everything else — anonymous, phone number, any other IdP — is off
// the pin and refused.
//
// An IdP entry names its provider by RESOURCE PATH and not by the bare id:
// PublicSignUp writes the API's own `name` field, which is
// projects/<project>/defaultSupportedIdpConfigs/google.com — the same shape
// scripts/live-checks/check361.sh reads, and the reason the two IdP entries
// below are compared by their LAST path segment rather than as whole strings.
// Compared whole, the pin's own provider would be refused on the one project
// the pin is about — the owner has enabled google.com there, which is the state
// spec §3 asks for — and `fugaro update` would fail on exactly the project W3
// converges (the HARD RULE).
func workspaceSignInPath(entry string) bool {
	switch entry {
	case "signIn.email.enabled",
		"client.permissions.disabledUserSignup is false":
		return true
	}
	for _, field := range []string{"defaultSupportedIdpConfigs ", "oauthIdpConfigs "} {
		rest, ok := strings.CutPrefix(entry, field)
		if !ok {
			continue
		}
		name, ok := strings.CutSuffix(rest, " enabled")
		if !ok {
			continue
		}
		// The provider is the last path segment: projects/<p>/<field>/<id>.
		if last := name[strings.LastIndex(name, "/")+1:]; last == "google.com" {
			return true
		}
	}
	return false
}
