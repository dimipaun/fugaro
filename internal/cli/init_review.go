package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The run's one confirmation (design m11 §9, "one confirmation per run").
//
// In a project Fugaro owns, the ordinary steps (the Terraform applies of the
// installation, the Firebase root, the history job and the repository, the
// state bucket, the Viewer-read removal, the Firestore marks and rules, image
// copies that only add tags, the local config) share ONE typed project name,
// asked with a review screen the first time one of them has something to
// change (so a rerun with nothing to do asks nothing). Each step still prints
// its own exact plan and banner, and says it was covered.
//
// It never covers what is money, permanent, a secret or someone else's:
// project creation, billing, the Firestore database's creation, a Cloud Build,
// replacing a registry tag, an unlisted repository (confirmRepo), secrets.
// Those never call anything here: TestRunConfirmationCallSites lists the only
// files that may. And a step whose plan destroys, replaces or removes an IAM
// grant is not covered either (tf.Cover): it asks its own typed confirmation.
//
// Fugaro-owned means: the project was created by this run (init
// --create-project), or the runs bucket's fugaro/project.json names this
// project and this GCP project (read-only). A project that is neither (an
// adopted one, an unmarked one, a failed read) keeps the per-step typed
// confirmations as they were. The confirmation is per run, in memory only.

type reviewState int

const (
	reviewUnasked reviewState = iota
	reviewTaken
	reviewDeclined
	reviewNotOwned
)

// runReview is the run's one confirmation. Only the converge sets it up
// (initEngine.converge); every other mode of init leaves initRun.review nil,
// and so asks per step.
type runReview struct {
	// cover says which plans the run's one confirmation may cover (the
	// allowlist of tf.Cover, with the members the review screen lists).
	cover func() tf.Cover
	// firebase says whether the Firebase project is verified as Fugaro's.
	firebase func(context.Context) (bool, string)
	owner    func(ctx context.Context) (why string, owned bool)
	screen   func(ctx context.Context, why string)
	state    reviewState
}

// ctx is the command's context, or a background one in a test with none.
func (r *initRun) ctx() context.Context {
	if r.cmd != nil && r.cmd.Context() != nil {
		return r.cmd.Context()
	}
	return context.Background()
}

// runCovers reports whether the run's one confirmation covers an ordinary
// step now, asking for it the first time. It is true only for a person at a
// real terminal (initflow.CanConfirmRun: never --yes, --non-interactive,
// --json, a pipe, --plan-only or a coding agent) who typed the project's
// name, in a project Fugaro owns. A name that is not the project's is a
// needs-you for this step and every one after it.
func (r *initRun) runCovers() (bool, error) {
	rv := r.review
	if rv == nil || r.o.planOnly || agentMarker(os.Getenv) != "" || !initflow.CanConfirmRun(r.conditions()) {
		return false, nil
	}
	switch rv.state {
	case reviewTaken:
		return true, nil
	case reviewNotOwned:
		return false, nil
	case reviewDeclined:
		return false, r.declinedRun()
	}
	ctx := r.ctx()
	why, owned := rv.owner(ctx)
	if !owned {
		rv.state = reviewNotOwned
		return false, nil
	}
	rv.screen(ctx, why)
	ok, err := r.typed("Type " + r.projectName + " to apply all of the above: ")
	if err != nil {
		return false, err
	}
	if !ok {
		rv.state = reviewDeclined
		return false, r.declinedRun()
	}
	rv.state = reviewTaken
	return true, nil
}

// declinedRun is the needs-you of a run whose one confirmation was not typed:
// nothing is applied, here or in a later stage.
func (r *initRun) declinedRun() error {
	args := []string{"init"}
	if r.o.firebase != "" {
		args = append(args, "--firebase", quoteWord(r.o.firebase))
	}
	fmt.Fprintln(r.w, "not confirmed (the project's name was not typed at the review); nothing was applied")
	return &initflow.NeedsYouError{Left: promptLeft(initflow.Installation, "type the project's name at the review to apply all of its steps", args...)}
}

// notCovered is why a plan is not covered by the run's one confirmation, ""
// when it is: only a plan that is entirely creates and in-place updates of the
// installation's own resource types, granting only the roles the modules use
// to people the review listed or to Fugaro's own service accounts, is covered
// (tf.Cover). Anything else, a plan that could not be read included, asks its
// own typed confirmation. imports are the import blocks the root's discovery
// wrote: an import that is not one of them is not covered.
func (r *initRun) notCovered(p *tf.Plan, imports []tf.ImportKey) string {
	if r.review == nil || r.review.cover == nil {
		return ""
	}
	b := r.review.cover().NotCovered(p, imports)
	if len(b) == 0 {
		return ""
	}
	return "the plan is outside what the review announced (" + strings.Join(b, "; ") + ")"
}

// askOrdinary is ask for an ordinary step: covered by the run's one
// confirmation when it applies, else the step's own. notCovered, when set, is
// why this step is not covered (shown), so it asks its own typed
// confirmation.
func (r *initRun) askOrdinary(what, notCovered string) (bool, error) {
	if m := agentMarker(os.Getenv); m != "" {
		return false, &initflow.AgentError{Marker: m}
	}
	if notCovered == "" {
		covered, err := r.runCovers()
		if err != nil {
			return false, err
		}
		if covered {
			fmt.Fprintf(r.w, "⚠ CONFIRM (project %s, GCP project %s): %s\n", r.projectName, r.gcpProject, what)
			fmt.Fprintln(r.w, "  covered by the run confirmation (the project's name typed at the review)")
			return true, nil
		}
	} else if r.review != nil && r.review.state == reviewTaken {
		fmt.Fprintf(r.w, "note: this step is not covered by the run confirmation: %s; it asks for its own\n", notCovered)
	}
	return r.ask(what)
}

// confirmOrdinary is confirm for an ordinary step (see askOrdinary).
func (r *initRun) confirmOrdinary(what, undone, notCovered string) error {
	ok, err := r.askOrdinary(what, notCovered)
	if err != nil {
		return err
	}
	if !ok {
		return r.notConfirmed(undone)
	}
	return nil
}

// owner says why the project is Fugaro's own, or not: created by this run, or
// its runs bucket (which must have the default name, so a custom or squatted
// bucket never counts) belongs to this project (its own project number) and
// carries the mark naming this project. Read-only; any failure is "not owned".
func (e *initEngine) owner(ctx context.Context) (string, bool) {
	r := e.r
	if r.res.Created != "" && r.res.Created == r.gcpProject {
		return "created by this run", true
	}
	if e.spec.RunsBucket != "fugaro-runs-"+r.gcpProject {
		return "", false
	}
	c := e.c
	if c == nil {
		var err error
		if c, err = newInitClients(ctx, e.lc); err != nil {
			return "", false
		}
	}
	num, err := infra.ProjectNumber(ctx, c, r.gcpProject)
	if err != nil {
		return "", false
	}
	m, err := infra.ReadProjectMarker(ctx, c, e.spec.RunsBucket, num)
	if err != nil || m == nil || m.Name != r.projectName || m.GCPProject != r.gcpProject {
		return "", false
	}
	return "its runs bucket, which is this project's, carries Fugaro's mark for project " + pluginwire.Printable(m.Name), true
}

// access is one list of IAM members the review prints, and where it came from.
type access struct {
	what    string
	members []string
	source  string
	note    string
}

// accessLists are the effective launchers, operators, budget admins and
// viewers, and the alert email, each with its source (a flag, the local
// config, or the default for a new config). The launchers can mint run tokens
// and launch runs, and the operators onboard repositories: they are the only
// people the run's one confirmation lets a plan grant roles to.
func (e *initEngine) accessLists(ctx context.Context) []access {
	o := e.r.o
	src := func(changed bool) string {
		switch {
		case changed && e.defaulted != "":
			return "default: you"
		case changed:
			return "from a flag"
		}
		return "from the local config"
	}
	out := []access{
		{what: "launchers", members: e.spec.Launchers, source: src(o.launchersChanged), note: "launch runs; also mint run tokens when --firebase's token signer is applied"},
		{what: "operators", members: e.spec.Operators, source: src(o.operatorsChanged), note: "onboard repositories and submit builds"},
	}
	if o.firebase != "" || e.lc.Terraform.BudgetBackend {
		bs := "from the local config"
		if o.budgetAdminsChanged {
			bs = "from a flag"
		}
		out = append(out, access{what: "budget admins", members: e.r.budgetAdmins(e.lc), source: bs, note: "plus the GCP project's owners and editors who are users or groups, read now"})
		for _, a := range e.projectAdmins(ctx) {
			out[len(out)-1].members = append(out[len(out)-1].members, a)
		}
		out = append(out, access{what: "budget viewers", members: slices.Concat(e.spec.Launchers, e.spec.Operators), source: "the launchers and operators"})
	}
	return out
}

// projectAdmins are the GCP project's owners and editors who are users or
// groups, which the Firebase root makes budget admins; read once, read-only,
// and empty when it cannot be read (then a plan granting them is not covered).
func (e *initEngine) projectAdmins(ctx context.Context) []string {
	if e.adminsRead || (e.r.o.firebase == "" && !e.lc.Terraform.BudgetBackend) {
		return e.admins
	}
	e.adminsRead = true
	c := e.c
	if c == nil {
		var err error
		if c, err = newInitClients(ctx, e.lc); err != nil {
			return nil
		}
	}
	e.admins, _, _, _ = infra.ProjectAdmins(ctx, c, e.r.gcpProject)
	return e.admins
}

// cover is what the run's one confirmation may cover: grants to the listed
// members and to Fugaro's own service accounts of the installation's project
// and the Firebase project.
func (e *initEngine) cover() tf.Cover {
	c := tf.Cover{Projects: []string{e.r.gcpProject}, Region: e.lc.Region, Buckets: []string{e.spec.RunsBucket}}
	if host, err := infra.RegistryHost(e.lc); err == nil {
		c.Registry, _, _ = strings.Cut(host, "/")
	}
	if b := e.r.o.billingAccount; b != "" {
		c.Billing = []string{b}
	}
	if fp := e.r.o.firebase; fp != "" && fp != e.r.gcpProject {
		if ok, _ := e.firebaseVerified(e.r.ctx()); ok {
			c.Projects = append(c.Projects, fp)
		}
	}
	for _, a := range e.accessLists(e.r.ctx()) {
		c.Listed = append(c.Listed, a.members...)
	}
	return c
}

// todo is the preview's stages the run's one confirmation covers that have
// something to do, in order.
func (e *initEngine) todo() map[string]bool {
	out := map[string]bool{}
	for _, s := range e.previewed {
		if s.State == initflow.Todo && initflow.RunCovers(s.Name) {
			out[s.Name] = true
		}
	}
	return out
}

func (e *initEngine) previewState(name string) initflow.State {
	for _, s := range e.previewed {
		if s.Name == name {
			return s.State
		}
	}
	return ""
}

// reviewScreen prints the one review for what is left of the run: a line for
// each ordinary stage with something to do, the IAM changes it knows of, and
// what still asks separately.
func (e *initEngine) reviewScreen(ctx context.Context, why string) {
	r, o := e.r, e.r.o
	w := r.w
	todo := e.todo()
	fmt.Fprintf(w, "\nReview: project %s (GCP %s) is Fugaro's own (%s), so its ordinary steps share ONE confirmation.\n",
		pluginwire.Printable(r.projectName), pluginwire.Printable(r.gcpProject), why)
	fmt.Fprintln(w, "Will do, each step printing its own exact plan first:")
	if todo[initflow.Installation] {
		fmt.Fprintln(w, "  installation:   Terraform apply of the installation root (planned when applied)")
	}
	if todo[initflow.Firebase] {
		_, fpWhy := e.firebaseVerified(ctx)
		fmt.Fprintf(w, "  firebase:       Terraform applies of the installation root (GCP project %s) and of the Firebase root in %s, then the budget database's marks and rules and Identity Platform (planned when applied)\n", pluginwire.Printable(r.gcpProject), pluginwire.Printable(o.firebase))
		fmt.Fprintf(w, "                  the Firebase project %s: %s\n", pluginwire.Printable(o.firebase), fpWhy)
	}
	if todo[initflow.Images] {
		fmt.Fprintln(w, "  "+e.images.reviewLine(ctx))
		fmt.Fprintln(w, "                  "+e.images.sourceLine(ctx))
	}
	if len(e.lc.BaseImages) > 0 {
		kinds := slices.Sorted(maps.Keys(e.lc.BaseImages))
		prefix := e.cover().Registry + "/" + r.gcpProject + "/"
		for _, k := range kinds {
			ref := e.lc.BaseImages[k]
			note := ""
			if !strings.HasPrefix(ref, prefix) {
				note = " (OUTSIDE this project's registry " + pluginwire.Printable(prefix) + ": a job built on it is not covered)"
			}
			fmt.Fprintf(w, "  base image %s: %s%s\n", pluginwire.Printable(k), pluginwire.Printable(ref), note)
		}
	}
	if todo[initflow.Installation2] {
		fmt.Fprintln(w, "  installation-2: Terraform apply of the installation root again, deploying the history job (planned when applied)")
	}
	if todo[initflow.Repository] {
		repo := "the checkout's repository"
		if e.repo != nil && e.repo.tg != nil {
			repo = pluginwire.Printable(e.repo.tg.repo)
		}
		fmt.Fprintf(w, "  repository:     Terraform apply onboarding %s (planned when applied)\n", repo)
	}
	fmt.Fprintln(w, "Fixed, announced and free, so they need no plan: the Terraform state bucket (cents a month) and the Cloud Resource Manager API when missing, the local config (its diff is shown first).")
	fmt.Fprintf(w, "IAM changes announced: removes project Viewers' read access to the runs bucket gs://%s and the state bucket gs://%s when they have it.\n", e.spec.RunsBucket, e.spec.StateBucket)
	fmt.Fprintln(w, "Who gets access (the effective lists; a plan that grants anyone else stops and asks its own typed confirmation):")
	for _, a := range e.accessLists(ctx) {
		list := "nobody"
		if len(a.members) > 0 {
			ps := make([]string, len(a.members))
			for i, m := range a.members {
				ps[i] = pluginwire.Printable(m)
			}
			list = strings.Join(ps, ", ")
		}
		line := fmt.Sprintf("  %-15s %s (%s)", a.what+":", list, a.source)
		if a.note != "" {
			line += "; " + a.note
		}
		fmt.Fprintln(w, line)
	}
	for _, a := range e.accessLists(ctx) {
		for _, m := range a.members {
			if strings.HasPrefix(m, "domain:") || m == "allUsers" || m == "allAuthenticatedUsers" {
				fmt.Fprintf(w, "WARNING: %s lists %s, which is everyone in a domain or on the internet: a plan granting it is NOT covered and asks its own typed confirmation.\n", a.what, pluginwire.Printable(m))
			}
		}
	}
	alert, asrc := e.alertEmail()
	fmt.Fprintf(w, "  %-15s %s (%s)\n", "alert email:", pluginwire.Printable(alert), asrc)
	if o.billingAccount != "" {
		fmt.Fprintf(w, "  %-15s %s (from --billing-account; the only account a budget may name)\n", "billing account:", pluginwire.Printable(o.billingAccount))
	}
	fmt.Fprintln(w, "Every Terraform plan is shown as it runs. It is covered only if it consists of creates and in-place updates of the installation's own resource kinds, its attributes are the modules' (images in this project's registry, jobs of this project called by Fugaro's accounts, logs to this project, private buckets, this project's names), and it grants only the modules' roles to the members above or to Fugaro's own service accounts. Anything else (a destroy or replace, a binding or policy, a key, another project, an unlisted member) stops that step, which asks its own typed confirmation.")
	fmt.Fprintln(w, "Still asks separately, never covered by this confirmation:")
	var sep []string
	if o.firebase != "" {
		sep = append(sep, "the Firestore database's creation (its location is permanent)")
	}
	if len(o.replaceImages) > 0 {
		sep = append(sep, "replacing a tag in your registry (--replace-image)")
	}
	if todo[initflow.Repository] {
		sep = append(sep, "each first Cloud Build of an image (billable)")
		if e.repo != nil && e.repo.tg != nil {
			if a, err := e.authState(e.repo.tg.origin); err == nil && a != authOK {
				sep = append(sep, "onboarding "+pluginwire.Printable(e.repo.tg.repo)+", which is not one of the project's yet (type owner/name)")
			}
		}
	}
	if e.previewState(initflow.Secrets) == initflow.Todo {
		sep = append(sep, "the secrets (hidden prompts)")
	}
	if e.previewState(initflow.Plugin) == initflow.Todo {
		sep = append(sep, "the plugin wiring (its own y/N)")
	}
	if len(sep) == 0 {
		sep = append(sep, "nothing else is waiting in this run")
	}
	for _, s := range sep {
		fmt.Fprintln(w, "  - "+s)
	}
}

// planOnlyReview shows what an applying run would ask once, for a project
// that is, or by --create-project would be, Fugaro's own. It applies nothing
// and never takes --yes or the confirmation.
func (e *initEngine) planOnlyReview(ctx context.Context, stages []initflow.StageResult) {
	e.previewed = stages
	why, owned := e.owner(ctx)
	if !owned && e.r.o.createProject && e.previewState(initflow.Project) == initflow.Todo {
		why, owned = "created by the run", true
	}
	if !owned || len(e.todo()) == 0 {
		return
	}
	e.reviewScreen(ctx, why)
	fmt.Fprintf(e.r.w, "A run without --plan-only asks once at a terminal: Type %s to apply all of the above. --plan-only never takes --yes or it.\n", e.r.projectName)
}

// alertEmail is the alert email in effect and where it comes from.
func (e *initEngine) alertEmail() (string, string) {
	switch o := e.r.o; {
	case o.alertEmail != "":
		return o.alertEmail, "from a flag"
	case e.lc.Terraform.AlertEmail != "":
		return e.lc.Terraform.AlertEmail, "from the local config"
	}
	return "none", "not set"
}

// firebaseVerified says whether the Firebase project is Fugaro's, and how
// that was found: the installation's own project, or another one whose own
// runs bucket carries this project's mark (the same check as the installation
// project's). A Firebase project that is neither is covered by nothing: its
// root and its database writes take their own typed names.
func (e *initEngine) firebaseVerified(ctx context.Context) (bool, string) {
	r := e.r
	fp := r.o.firebase
	switch {
	case fp == "":
		return false, ""
	case fp == r.gcpProject:
		return true, "the installation's own project, verified as above"
	}
	if e.fpChecked == "" {
		e.fpChecked = "no"
		if c := e.c; c != nil || e.lc != nil {
			if c == nil {
				c, _ = newInitClients(ctx, e.lc)
			}
			if c != nil {
				if num, err := infra.ProjectNumber(ctx, c, fp); err == nil {
					if m, err := infra.ReadProjectMarker(ctx, c, "fugaro-runs-"+fp, num); err == nil && m != nil && m.Name == r.projectName && m.GCPProject == fp {
						e.fpChecked = "yes"
					}
				}
			}
		}
	}
	if e.fpChecked == "yes" {
		return true, "verified as Fugaro's (its own runs bucket carries this project's mark)"
	}
	return false, "NOT verified as Fugaro's (another project than " + pluginwire.Printable(r.gcpProject) + ", with no Fugaro mark): its Terraform apply and database writes each ask their own typed name"
}

// firebaseReason is why a step on the Firebase project is not covered, "" when
// it is (or there is no review).
func (r *initRun) firebaseReason() string {
	if r.review == nil || r.review.firebase == nil {
		return ""
	}
	if ok, why := r.review.firebase(r.ctx()); !ok {
		return "the Firebase project is " + why
	}
	return ""
}

// notCoveredFirebase is notCovered for a plan of the Firebase root, which is
// also not covered when the Firebase project is not verified as Fugaro's.
func (r *initRun) notCoveredFirebase(p *tf.Plan, imports []tf.ImportKey) string {
	if why := r.firebaseReason(); why != "" {
		return why
	}
	return r.notCovered(p, imports)
}
