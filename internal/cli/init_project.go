package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The project stage (stage 1, M11 task 16): init may create the GCP
// project, add Firebase to it and link billing, only when asked.
//
// What it never does:
//   - create a project, add Firebase or link billing under --yes,
//     --non-interactive or --json, with stdin not a terminal, or in a coding
//     agent's session (agentEnv): the loop does not even call Apply for a
//     stage --yes does not cover when it cannot prompt, and Apply refuses
//     again on its own. --yes is never read here;
//   - take the project's ID from anywhere but --gcp-project (never the gcloud
//     default, never GOOGLE_CLOUD_PROJECT or the local config alone);
//   - create more than one project in a run;
//   - link billing without --link-billing and its own typed confirmation (the
//     account's ID), or to a project that already has billing (an existing
//     link is never changed), or without --create-project (a project this
//     run did not ask to create or adopt is never billed);
//   - list the user's billing accounts, or print the account a project is
//     linked to.
//
// Without --link-billing the stage prints the one gcloud line that links
// billing and stops needs-you. The calls are the user's own
// (infra.ProjectClients; no service account); the quota project is the one
// in the user's credentials, and the confirmation says so. See
// infra/projectcreate.go for what is unverified against the real services.

func (o *initOptions) checkCreateProject() error {
	if !o.createProject {
		switch {
		case o.linkBilling != "":
			return userErr("--link-billing goes with --create-project (both flags, explicitly: init links billing only to a project it was asked to create or adopt). To link billing yourself: gcloud billing projects link <project> --billing-account=<account>")
		case o.parent != "" || o.displayName != "":
			return userErr("--parent and --display-name are for --create-project")
		}
		return nil
	}
	switch {
	case o.forget || o.configOnly || o.printVars:
		return userErr("--create-project creates a project; it excludes --forget, --config-only and --print-vars")
	case o.cloud.gcpProject == "":
		return userErr("--create-project needs --gcp-project <id>: the ID to create (init never uses the gcloud default project)")
	}
	if err := infra.ValidateNewProjectID(o.cloud.gcpProject); err != nil {
		return userErr("--gcp-project: %v", err)
	}
	if o.parent != "" {
		if err := infra.ValidateParent(o.parent); err != nil {
			return userErr("%v", err)
		}
	}
	if o.displayName != "" {
		if err := infra.ValidateDisplayName(o.displayName); err != nil {
			return userErr("%v", err)
		}
	}
	if o.linkBilling != "" {
		if err := infra.ValidateBillingAccount(o.linkBilling); err != nil {
			return userErr("%v", err)
		}
	}
	return nil
}

// projectStage is stage 1 with --create-project.
type projectStage struct {
	e  *initEngine
	id string

	pc   *infra.ProjectClients
	info infra.ProjectInfo
	// todo is what the last check found to do.
	create, addFirebase, link bool
	guide                     bool // billing is missing and --link-billing is not given
	// started is set when Apply begins: one project per run, and one Apply.
	started bool
}

func newProjectStage(e *initEngine) *projectStage {
	return &projectStage{e: e, id: e.r.o.cloud.gcpProject}
}

func (s *projectStage) Name() string { return initflow.Project }

func (s *projectStage) o() *initOptions { return s.e.r.o }

func (s *projectStage) clients(ctx context.Context) error {
	if s.pc != nil {
		return nil
	}
	ep := s.e.lc.Endpoints
	pc, err := infra.NewProjectClients(ctx, infra.ProjectEndpoints{ResourceManager: ep.ResourceManager, Firebase: ep.FirebaseManagement, Billing: ep.CloudBilling}, ep.NoAuth, nil)
	if err != nil {
		return remote(err)
	}
	s.pc = pc
	return nil
}

// guided is the one line that links billing: nothing is guessed about the
// account, and no account is read.
func (s *projectStage) guided() string {
	return "gcloud billing projects link " + s.id + " --billing-account=BILLING_ACCOUNT_ID"
}

const billingHelp = "find the account's ID with gcloud billing accounts list, or in https://console.cloud.google.com/billing; then run the command, or rerun fugaro init with --create-project --link-billing BILLING_ACCOUNT_ID to type its confirmation here"

// apiProblem maps a refusal by the project APIs to a stage failure with its
// one-line fix, or to the user's to do (an API off on the quota project).
func (s *projectStage) problem(err error) (initflow.Status, error) {
	var pe *infra.ProjectError
	if errors.As(err, &pe) {
		if pe.Kind == infra.ProjectAPIOff && pe.Fix != "" {
			lf := initflow.Left{Stage: initflow.Project, Kind: initflow.LeftCommand, Text: pe.Fix}
			return initflow.Status{State: initflow.NeedsYou, Detail: pe.Error(), Left: &lf}, nil
		}
		return initflow.Status{}, &initflow.StageError{Err: remote(err), Fix: pe.Fix}
	}
	return initflow.Status{}, remote(err)
}

func (s *projectStage) read(ctx context.Context) error {
	if err := s.clients(ctx); err != nil {
		return err
	}
	info, err := infra.ReadProject(ctx, s.pc, s.id)
	if err != nil {
		return err
	}
	s.info = info
	s.create, s.addFirebase, s.link, s.guide = false, false, false, false
	if !info.Exists {
		s.create = true
		s.link = s.o().linkBilling != ""
		return nil
	}
	if info.State != "" && info.State != "ACTIVE" {
		return nil
	}
	s.addFirebase = !info.Firebase
	if !(info.BillingKnown && info.BillingEnabled) {
		s.link = s.o().linkBilling != ""
		s.guide = !s.link
	}
	return nil
}

func (s *projectStage) Check(ctx context.Context) (initflow.Status, error) {
	if err := s.read(ctx); err != nil {
		return s.problem(err)
	}
	if st := s.info.State; s.info.Exists && st != "" && st != "ACTIVE" {
		return initflow.Status{}, &initflow.StageError{
			Err: userErr("project %s is %s: it is being deleted and cannot be used", s.id, st),
			Fix: "restore it with gcloud projects undelete " + s.id + " (within 30 days of the deletion), or choose another --gcp-project"}
	}
	note := ""
	if lb := s.o().linkBilling; lb != "" && s.info.Exists && s.info.BillingKnown && s.info.BillingEnabled {
		note = "; billing is already linked, and init never changes an existing link (--link-billing is not applied)"
	}
	if s.create || s.addFirebase || s.link {
		return initflow.Status{State: initflow.Todo, Detail: s.describe() + note}, nil
	}
	if s.guide {
		lf := initflow.Left{Stage: initflow.Project, Kind: initflow.LeftCommand, Text: s.guided()}
		return initflow.Status{State: initflow.NeedsYou, Detail: "project " + s.id + " has no billing (or it cannot be read): " + billingHelp, Left: &lf}, nil
	}
	return initflow.Status{State: initflow.Done, Detail: "project " + s.id + " exists, has Firebase and billing" + note}, nil
}

// describe is the one line of what Apply would do.
func (s *projectStage) describe() string {
	var parts []string
	if s.create {
		p := "create project " + s.id
		if par := s.o().parent; par != "" {
			p += " under " + par
		} else {
			p += " with no parent"
		}
		parts = append(parts, p+" (typed confirmation)")
	}
	if s.create || s.addFirebase {
		parts = append(parts, "add Firebase (free)")
	}
	if s.link {
		parts = append(parts, "link billing account "+s.o().linkBilling+" (its own typed confirmation; may incur charges)")
	} else if s.create || s.guide {
		parts = append(parts, "billing is not linked by init without --link-billing: "+s.guided())
	}
	return "will " + strings.Join(parts, ", ")
}

func (s *projectStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	if err := s.read(ctx); err != nil {
		return initflow.Plan{}, err
	}
	if !(s.create || s.addFirebase || s.link) {
		return initflow.Plan{NothingToDo: !s.guide, Detail: "nothing to create"}, nil
	}
	return initflow.Plan{Detail: s.describe()}, nil
}

func (s *projectStage) Left() initflow.Left {
	if s.create || s.addFirebase || s.link {
		text := "run fugaro init --create-project --gcp-project " + s.id
		if p := s.o().parent; p != "" {
			text += " --parent " + p
		}
		if s.o().linkBilling != "" {
			text += " --link-billing " + s.o().linkBilling
		}
		text += " in your own terminal and type the ID(s) it asks for"
		return initflow.Left{Stage: initflow.Project, Kind: initflow.LeftPrompt, Text: text}
	}
	return initflow.Left{Stage: initflow.Project, Kind: initflow.LeftCommand, Text: s.guided()}
}

// canType is whether this run may show a typed confirmation: a terminal on
// stdin, none of --non-interactive and --json (a run no person watches),
// and no coding agent's session. --yes is not consulted: it confirms
// nothing here, and a person at a terminal who also passed it still types.
func (s *projectStage) canType() bool {
	o, cmd := s.o(), s.e.r.cmd
	return !o.nonInteractive && !o.asJSON && !agentEnv(os.Getenv) && stdinIsTerminal(cmd.InOrStdin())
}

// typed shows banner and the prompt, and reports whether the line typed is
// exactly want.
func (s *projectStage) typed(banner, want, prompt string) (bool, error) {
	r := s.e.r
	fmt.Fprintf(r.w, "⚠ CONFIRM: %s\n", banner)
	fmt.Fprintf(r.w, "Type %s %s: ", want, prompt)
	line, err := r.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, userErr("reading the confirmation: %v", err)
	}
	fmt.Fprintln(r.w)
	return strings.TrimSpace(line) == want, nil
}

func quotaNote() string {
	if q := os.Getenv("GOOGLE_CLOUD_QUOTA_PROJECT"); q != "" {
		return "quota project " + q + " (GOOGLE_CLOUD_QUOTA_PROJECT)"
	}
	return "the quota project in your Application Default Credentials (gcloud auth application-default set-quota-project changes it)"
}

func (s *projectStage) declined(what string) error {
	return &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Project, Kind: initflow.LeftPrompt,
		Text: "not confirmed (" + what + " was not typed): rerun fugaro init in your own terminal and type it, nothing further was done"}}
}

func (s *projectStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if s.started {
		return initflow.Outcome{}, errors.New("init creates at most one project per run: it has already acted in this run")
	}
	if !env.Interactive || !s.canType() {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	s.started = true
	if err := s.read(ctx); err != nil {
		return initflow.Outcome{}, s.fail(err)
	}
	if st := s.info.State; s.info.Exists && st != "" && st != "ACTIVE" {
		return initflow.Outcome{}, userErr("project %s is %s: it is being deleted and cannot be used", s.id, st)
	}
	r, o := s.e.r, s.o()
	var did []string
	if s.create {
		parent := "no parent (it is not in any organization or folder)"
		if o.parent != "" {
			parent = "parent " + o.parent
		}
		name := o.displayName
		if name == "" {
			name = s.id
		}
		ok, err := s.typed(fmt.Sprintf("this CREATES the Google Cloud project %q (display name %q) with %s, and adds Firebase to it, as you (your own credentials, no service account). "+
			"The ID is global and permanent: nobody can reuse it, even after the project is deleted. It will be used for everything Fugaro runs. "+
			"Calls are billed to %s, not to the new project. Billing is linked separately: this confirmation does not link it.",
			s.id, name, parent, quotaNote()), s.id, "to create it")
		if err != nil {
			return initflow.Outcome{}, err
		}
		if !ok {
			return initflow.Outcome{}, s.declined("the project's ID")
		}
		if err := infra.CreateProject(ctx, s.pc, infra.CreateSpec{ID: s.id, DisplayName: name, Parent: o.parent}); err != nil {
			return initflow.Outcome{}, s.fail(err)
		}
		if err := infra.WaitReadable(ctx, s.pc, s.id); err != nil {
			return initflow.Outcome{}, s.fail(err)
		}
		r.res.Created = s.id
		did = append(did, "created project "+s.id)
		fmt.Fprintf(r.w, "created project %s\n", s.id)
		s.addFirebase = true
	} else if s.addFirebase {
		ok, err := s.typed(fmt.Sprintf("project %s exists and you can read it, but it has no Firebase: this ADDS Firebase to it (free; Firebase is how Fugaro keeps its budget and dashboard data), as you. Nothing else about the project changes.", s.id), s.id, "to add Firebase")
		if err != nil {
			return initflow.Outcome{}, err
		}
		if !ok {
			return initflow.Outcome{}, s.declined("the project's ID")
		}
	}
	if s.addFirebase {
		if err := infra.AddFirebase(ctx, s.pc, s.id); err != nil {
			return initflow.Outcome{}, s.fail(err)
		}
		if err := infra.VerifyFirebase(ctx, s.pc, s.id); err != nil {
			return initflow.Outcome{}, s.fail(err)
		}
		did = append(did, "added Firebase")
		fmt.Fprintf(r.w, "added Firebase to project %s\n", s.id)
	}
	switch {
	case s.link:
		acct := o.linkBilling
		ok, err := s.typed(fmt.Sprintf("this LINKS billing account %s to project %s. Everything Fugaro runs in the project (Cloud Run jobs, builds, storage, Firestore, Vertex AI calls) may incur charges on that account. "+
			"It is not undone by deleting resources; unlink it with gcloud billing projects unlink %s.", acct, s.id, s.id), acct, "to link it")
		if err != nil {
			return initflow.Outcome{}, err
		}
		if !ok {
			return initflow.Outcome{}, s.declined("the billing account's ID")
		}
		if err := infra.LinkBilling(ctx, s.pc, s.id, acct); err != nil {
			return initflow.Outcome{}, s.fail(err)
		}
		if err := infra.VerifyBilling(ctx, s.pc, s.id); err != nil {
			return initflow.Outcome{}, s.fail(err)
		}
		did = append(did, "linked billing")
		fmt.Fprintf(r.w, "linked billing account %s to project %s\n", acct, s.id)
	case s.guide || s.create:
		// What was done is on the account above; billing is the user's.
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Project, Kind: initflow.LeftCommand, Text: s.guided()}}
	}
	return initflow.Outcome{Changed: len(did) > 0, Detail: strings.Join(did, ", ")}, nil
}

// fail keeps the services' own text (verbatim) and the fix, and exit codes.
func (s *projectStage) fail(err error) error {
	var pe *infra.ProjectError
	if errors.As(err, &pe) {
		if pe.Kind == infra.ProjectTaken || pe.Kind == infra.ProjectDeleted {
			return &initflow.StageError{Err: userErr("%v", err), Fix: pe.Fix}
		}
		return &initflow.StageError{Err: remote(err), Fix: pe.Fix}
	}
	return remote(err)
}

// Verify re-reads the project: Apply verified each step it took; this is
// the closing check that nothing is left half done.
func (s *projectStage) Verify(ctx context.Context) error {
	if err := s.read(ctx); err != nil {
		return s.fail(err)
	}
	if !s.info.Exists || !s.info.Firebase {
		return fmt.Errorf("project %s is not ready after the apply (exists: %v, Firebase: %v)", s.id, s.info.Exists, s.info.Firebase)
	}
	return nil
}
