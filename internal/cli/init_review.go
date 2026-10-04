package cli

import (
	"context"
	"fmt"
	"os"
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
// grant (tf.Beyond) is not covered either: it asks its own typed confirmation.
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
	owner  func(ctx context.Context) (why string, owned bool)
	screen func(ctx context.Context, why string)
	state  reviewState
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

// planNotCovered is why a plan is not covered by the run's one confirmation,
// "" when it is.
func planNotCovered(p *tf.Plan) string {
	b := tf.Beyond(p)
	if len(b) == 0 {
		return ""
	}
	return "the plan destroys, replaces or removes IAM access (" + strings.Join(b, "; ") + ")"
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
// carrying its mark (read-only; any failure to read is "not owned").
func (e *initEngine) owner(ctx context.Context) (string, bool) {
	r := e.r
	if r.res.Created != "" && r.res.Created == r.gcpProject {
		return "created by this run", true
	}
	c := e.c
	if c == nil {
		var err error
		if c, err = newInitClients(ctx, e.lc); err != nil {
			return "", false
		}
	}
	m, err := infra.ReadProjectMarker(ctx, c, e.spec.RunsBucket)
	if err != nil || m == nil || m.Name != r.projectName || m.GCPProject != r.gcpProject {
		return "", false
	}
	return "its runs bucket carries Fugaro's mark for project " + pluginwire.Printable(m.Name), true
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
		fmt.Fprintf(w, "  firebase:       Terraform applies of the installation root and of the Firebase root in %s, then the budget database's marks and rules and Identity Platform (planned when applied)\n", pluginwire.Printable(o.firebase))
	}
	if todo[initflow.Images] {
		fmt.Fprintln(w, "  "+e.images.reviewLine(ctx))
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
	if e.defaulted != "" {
		fmt.Fprintf(w, "  launchers: %s (you); operators: %s (you); change with --launcher/--operator\n", e.defaulted, e.defaulted)
	}
	fmt.Fprintln(w, "  also, where missing or changed: the Terraform state bucket (cents a month), the Cloud Resource Manager API, the local config (its diff is shown first)")
	fmt.Fprintf(w, "IAM changes announced: removes project Viewers' read access to the runs bucket gs://%s and the state bucket gs://%s when they have it; each Terraform plan lists the service accounts and roles it adds.\n", e.spec.RunsBucket, e.spec.StateBucket)
	fmt.Fprintln(w, "A plan that destroys or replaces anything, or removes any other IAM grant, is not covered: that step stops and asks for its own typed confirmation.")
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
