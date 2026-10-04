package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// printVarsUngatedFirebase is init --firebase --print-vars's warning.
const printVarsUngatedFirebase = "--print-vars is ungated and makes no cloud call, so the Firebase root's admins list holds only --budget-admin and the local config's terraform.budget_admins: the GCP project's owners and editors are read from its IAM policy when init runs, and the Firebase project is not checked"

// firebaseVars is the installation's and the Firebase root's tfvars as one
// JSON object, for --print-vars.
func (r *initRun) firebaseVars(inst []byte, spec infra.InstallationSpec, lc *localcfg.Config) ([]byte, error) {
	fs, err := infra.Firebase(spec, infra.FirebaseInputs{FP: r.o.firebase, BudgetAdmins: r.budgetAdmins(lc)})
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
			r.warn(fmt.Sprintf("the history job (the sweeper of crashed runs) is not deployed, so crashed runs are not cleaned up and old run users are not deleted: its image %s does not exist, and nothing builds it for you (like the dev base image until M7). From a checkout of this repository run\n  sh images/build-base.sh history %s\n  docker push %s\nthen run fugaro init --firebase again (gcloud auth configure-docker %s lets docker push there)", h.Image, h.Image, h.Image, strings.SplitN(h.Image, "/", 2)[0]))
		}
	}
	spec.History = &h
	return spec, nil
}

// applyRoot plans, shows, guards and (once confirmed) applies the root t
// runs, which is not the installation (installRoot does that). what says
// where; stop means --plan-only ended the run.
func (r *initRun) applyRoot(ctx context.Context, t *tf.TF, wd *infra.Workdir, root, what string) (applied, stop bool, err error) {
	changed, err := t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return false, false, remote(err)
	}
	if !changed {
		fmt.Fprintf(r.w, "No changes: the %s root matches the plan.\n", root)
		return false, r.o.planOnly, nil
	}
	plan, err := t.Show(ctx, infra.PlanFile)
	if err != nil {
		return false, false, remote(err)
	}
	fmt.Fprint(r.w, tf.Summary(plan))
	if err := guard(plan, r.o.allowDelete); err != nil {
		return false, false, err
	}
	counts := infra.CountPlan(plan)
	if r.o.planOnly {
		fmt.Fprintf(r.w, "--plan-only: nothing applied; the plan is %s\n", filepath.Join(wd.Root, infra.PlanFile))
		return false, true, nil
	}
	if err := r.confirm("applies "+counts.String()+" "+what, "nothing was applied"); err != nil {
		return false, false, err
	}
	if err := t.Apply(ctx, infra.PlanFile); err != nil {
		return false, false, remote(err)
	}
	return true, false, nil
}

// initFirebase is fugaro init --firebase <fp>: it adopts the project's own
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
//  4. the installation root again, which deploys the history job with the
//     Firebase outputs once its image exists.
//
// Each apply has its own plan and confirmation, and so does the database
// step. Nothing creates the Firebase project or links billing.
func (r *initRun) initFirebase(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, bin string, lc *localcfg.Config, spec infra.InstallationSpec, path string, old []byte) error {
	fp := r.o.firebase
	mode := r.o.budgetMode
	if mode == "enforce" && (lc.Budget == nil || lc.Budget.PerRunUSD <= 0) {
		return userErr("--budget-mode enforce needs budget.per_run_usd in the project config (more than 0): it is the per-run cap the jobs enforce")
	}
	if lc.Budget != nil && lc.Budget.FirebaseProject != "" && lc.Budget.FirebaseProject != fp {
		return userErr("project %s already uses Firebase project %s (budget.firebase_project); one Fugaro project has one Firebase project (design D3, which may be the installation's own GCP project), so --firebase %s is refused", lc.Name, lc.Budget.FirebaseProject, fp)
	}

	// 0. Discovery.
	if err := infra.CheckFirebaseProject(ctx, c, fp); err != nil {
		return initErr(err)
	}
	admins, skipped, err := infra.ProjectAdmins(ctx, c, lc.GCPProject)
	if err != nil {
		return initErr(err)
	}
	for _, s := range skipped {
		r.warn("not a budget admin (owners and editors who are users or groups are; a service account can be added with --budget-admin): " + s)
	}
	if len(admins) == 0 && len(r.budgetAdmins(lc)) == 0 {
		return userErr("project %s has no user or group with roles/owner or roles/editor, and no --budget-admin: nobody could change a cap or a kill switch", lc.GCPProject)
	}
	// Refused here, before the first apply, whatever the installation module
	// accepts.
	if _, err := infra.Firebase(spec, infra.FirebaseInputs{FP: fp, Admins: admins, BudgetAdmins: r.budgetAdmins(lc)}); err != nil {
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
		fmt.Fprintf(r.w, "Firestore step of the Firebase project %s (only after you confirm, never in --plan-only):\n", fp)
		for _, l := range lines {
			fmt.Fprintf(r.w, "  %s\n", l)
		}
	}

	// 1. The installation root.
	outs, stop, err := r.installRoot(ctx, c, t, wd, lc, spec)
	if err != nil || stop {
		return err
	}
	if outs.HistoryServiceAccount == "" {
		return remote(errors.New("the installation has no history account in its outputs; the first apply did not create it"))
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
	fspec, err := infra.Firebase(spec, infra.FirebaseInputs{FP: fp, Admins: admins, BudgetAdmins: r.budgetAdmins(lc), HistoryAccount: outs.HistoryServiceAccount})
	if err != nil {
		return initErr(err)
	}
	vars, err := infra.FirebaseVars(fspec)
	if err != nil {
		return err
	}
	if err := fwd.WriteVars(vars); err != nil {
		return userErr("writing the Firebase tfvars: %v", err)
	}
	backend, err := fwd.WriteBackend(spec.StateBucket, infra.StatePrefixFirebase)
	if err != nil {
		return userErr("%v", err)
	}
	if err := ft.Init(ctx, backend); err != nil {
		return remote(err)
	}
	applied, stop, err := r.applyRoot(ctx, ft, fwd, "firebase", fmt.Sprintf("to the Firebase project %s (the Realtime Database, a restricted web API key, the token signer and the grants on it)", fp))
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
	if mode == "off" {
		r.warn("--budget-mode off still deployed the database (its rules, mark and project name): jobs just don't use it, and the mode stored in it is unchanged by off")
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
	if err := r.writeConfig(lc, spec, outs, path, old, confirmed); err != nil {
		return err
	}
	if lc.BudgetMode() == localcfg.BudgetOff && mode == "" {
		r.warn("budget.mode is off in the project config, so jobs don't use the backend yet: fugaro init --firebase " + fp + " --budget-mode observe turns it on (then fugaro init --repo for each repository)")
	}

	// 4. The installation root again: the history job.
	r.installLabel = " (the history job and its sweep schedule)"
	_, stop, err = r.installRoot(ctx, c, t, wd, lc, spec)
	if err != nil || stop {
		return err
	}
	fmt.Fprintf(r.w, "The budget backend of project %s is in Firebase project %s. Each repository needs fugaro init --repo to pick up the jobs' environment.\n", lc.Name, fp)
	return nil
}

// openDatabase is the admin client of the database at url (the person's
// credentials).
func (r *initRun) openDatabase(ctx context.Context, lc *localcfg.Config, url, mode string) (*infra.DB, error) {
	cl, err := newBudgetClient(ctx, url, lc.Endpoints.NoAuth)
	if err != nil {
		return nil, err
	}
	m := mode
	if m == "off" {
		m = ""
	}
	return infra.NewDB(cl, lc.Name, lc.GCPProject, m), nil
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

// deployDatabase shows what it would write into the database (the mark, the
// project's name, the mode, the largest lease, the rules) and writes it once
// confirmed; a database that is already as wanted is left alone, with no
// confirmation. confirmed reports that a confirmation was given, which also
// covers the local config's diff.
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
		what += fmt.Sprintf("; it also creates the Firestore database in %s, whose location is permanent and can never be changed", infra.FirestoreLocation)
	}
	if err := r.confirm(what, "the database was not written"); err != nil {
		return false, err
	}
	if fs.CreatesDatabase() {
		if err := r.confirmLocation(); err != nil {
			return false, err
		}
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
	if fs.Pending() {
		if err := fs.Apply(ctx); err != nil {
			return false, initErr(err)
		}
		fmt.Fprintln(r.w, "ensured the Firestore database, its deny-all rules and its mark (read back)")
	}
	return true, nil
}

// confirmLocation is the Firestore database's own confirmation: its location
// is permanent, so the person types the location itself (--yes confirms it, as
// it does every step).
func (r *initRun) confirmLocation() error {
	fmt.Fprintf(r.w, "⚠ CONFIRM (project %s): the Firestore database is created in %s, and a database's location can NEVER be changed.\n", r.projectName, infra.FirestoreLocation)
	if r.o.yes {
		fmt.Fprintln(r.w, "  confirmed by --yes")
		return nil
	}
	fmt.Fprintf(r.w, "Type %s to create it there: ", infra.FirestoreLocation)
	line, err := r.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return userErr("reading the confirmation: %v", err)
	}
	if strings.TrimSpace(line) != infra.FirestoreLocation {
		return userErr("the location %s was not typed; the Firestore database was not created (nothing was written)", infra.FirestoreLocation)
	}
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
	fs, err := firestore.New(lc.Endpoints.Firestore, fp, ts, firestore.WithHTTPClient(hc))
	if err != nil {
		return nil, userErr("%v", err)
	}
	wait := time.Second
	if lc.Endpoints.NoAuth {
		wait = 10 * time.Millisecond
	}
	return &infra.FirestoreStep{FS: fs, Rules: &infra.RulesClient{Endpoint: lc.Endpoints.FirebaseRules, Project: fp, HTTP: hc},
		FP: fp, Project: lc.Name, GCPProject: lc.GCPProject, Version: Version, Wait: wait}, nil
}

// idpStep is the Identity Platform step: read before the confirmation, run
// after it.
type idpStep struct {
	c       *infra.IdentityPlatform
	missing bool
}

// identityPlatform reads the Firebase project's Identity Platform
// configuration (read-only). One that exists is never changed, and is refused
// when it enables a public sign-up path: the web API key is public, so only
// custom tokens signed by our signer may work.
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
	s := &idpStep{c: &infra.IdentityPlatform{Endpoint: lc.Endpoints.IdentityToolkit, Project: fp, HTTP: hc}}
	raw, exists, err := s.c.Config(ctx)
	if err != nil {
		return nil, remote(err)
	}
	s.missing = !exists
	if exists {
		if err := refusePublicSignUp(raw, fp); err != nil {
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
	return refusePublicSignUp(raw, s.c.Project)
}

func refusePublicSignUp(raw []byte, fp string) error {
	public, err := infra.PublicSignUp(raw)
	if err != nil {
		return remote(err)
	}
	if len(public) == 0 {
		return nil
	}
	return userErr("Identity Platform in Firebase project %s has public sign-up enabled (%s). The web API key is public, so only custom tokens signed by Fugaro's signer may work: disable these in the Firebase console (Authentication, Sign-in method) or with the Identity Toolkit config API, then rerun. fugaro init never changes an existing configuration", fp, strings.Join(public, "; "))
}
