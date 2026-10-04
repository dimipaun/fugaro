package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	if o.firebase != "" && o.firebase != o.cloud.gcpProject {
		return userErr("--create-project creates --gcp-project %s, and --firebase %s is another project: init creates only one, and only the project that holds the installation; create %s yourself, or pass --firebase %s", o.cloud.gcpProject, o.firebase, o.firebase, o.cloud.gcpProject)
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
	guide                     bool   // billing is missing and is not linked by this run
	whyGuide                  string // why --link-billing was not applied, if it was given
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
	return "gcloud billing projects link " + quoteWord(s.id) + " --billing-account=BILLING_ACCOUNT_ID"
}

func billingHelp() string {
	return "find the account's ID with gcloud billing accounts list, or in https://console.cloud.google.com/billing; then run the command, or rerun " + selfCommand() + " init with --create-project --link-billing BILLING_ACCOUNT_ID to type its confirmation here"
}

// problem maps a refusal by the project APIs to a stage failure with its
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
		// Billing is linked to a project that already existed only when it
		// has no link at all and the read says so: an unreadable state, or a
		// link that is merely not enabled (suspended, closed), is never
		// replaced.
		given := s.o().linkBilling != ""
		switch {
		case given && info.BillingKnown && !info.BillingLinked:
			s.link = true
		case given && !info.BillingKnown:
			s.guide, s.whyGuide = true, "the project's billing cannot be read, so --link-billing is not applied (init never replaces a link it cannot see)"
		case given:
			s.guide, s.whyGuide = true, "the project is linked to a billing account that is not enabled (suspended or closed), and init never replaces an existing link: --link-billing is not applied"
		default:
			s.guide = true
		}
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
		why := ""
		if s.whyGuide != "" {
			why = s.whyGuide + ". "
		}
		return initflow.Status{State: initflow.NeedsYou, Detail: "project " + s.id + " has no billing (or it cannot be read): " + why + billingHelp(), Left: &lf}, nil
	}
	return initflow.Status{State: initflow.Done, Detail: "project " + s.id + " exists, has Firebase and billing" + note}, nil
}

// describe is the one line of what Apply would do.
func (s *projectStage) describe() string {
	var parts []string
	if !s.create && s.info.Exists {
		parts = append(parts, existingNote(s.id)+":")
	}
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
		args := []string{"init", "--create-project", "--gcp-project", quoteWord(s.id)}
		if p := s.o().parent; p != "" {
			args = append(args, "--parent", quoteWord(p))
		}
		if s.o().linkBilling != "" {
			args = append(args, "--link-billing", quoteWord(s.o().linkBilling))
		}
		return promptLeft(initflow.Project, "type the ID(s) it asks for", args...)
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

// existingNote is what every confirmation and the plan say about a project
// that was there before this run: it was not created by it, and it may not
// be the user's own.
func existingNote(id string) string {
	return "adopting an EXISTING project " + id + " (not created by this run; init cannot tell whether it is yours or empty)"
}

var safeIDRE = regexp.MustCompile(`[^a-zA-Z0-9._:-]`)

// sanitized is s cut and stripped to what a project ID or number holds, so
// nothing from the environment can inject text or control characters.
func sanitized(s string) string {
	s = safeIDRE.ReplaceAllString(s, "?")
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return s
}

// adcInfo is what the Application Default Credentials' file says: its type
// and quota project. It reads the one file ADC would (GOOGLE_APPLICATION_CREDENTIALS,
// else gcloud's well-known one) and never keeps or prints a path or a key.
func adcInfo() (kind, quota string) {
	path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if path == "" {
		dir := os.Getenv("CLOUDSDK_CONFIG")
		if dir == "" {
			if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
				dir = filepath.Join(x, "gcloud")
			} else if h, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(h, ".config", "gcloud")
			}
		}
		if dir != "" {
			path = filepath.Join(dir, "application_default_credentials.json")
		}
	}
	b, err := readBounded(path)
	if err != nil {
		return "", ""
	}
	var f struct {
		Type  string `json:"type"`
		Quota string `json:"quota_project_id"`
	}
	if json.Unmarshal(b, &f) != nil {
		return "", ""
	}
	return f.Type, f.Quota
}

// readBounded reads a regular file of at most 1 MiB: a FIFO or a device
// named by the environment is never opened for reading, so it cannot block.
func readBounded(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// credentialNote says whose credentials make the calls, and which project
// pays for them, without asserting more than it knows.
func (s *projectStage) credentialNote() string {
	kind, adcQuota := adcInfo()
	who := "as the account of your Application Default Credentials"
	switch kind {
	case "authorized_user":
		who = "as you (your own user credentials), not a service account"
	case "service_account":
		who = "as the SERVICE ACCOUNT in your credentials, not as you personally (GOOGLE_APPLICATION_CREDENTIALS names a service-account key): check that is the account you mean to own this project"
	}
	switch {
	case os.Getenv("GOOGLE_CLOUD_QUOTA_PROJECT") != "":
		return who + "; calls are billed to quota project " + sanitized(os.Getenv("GOOGLE_CLOUD_QUOTA_PROJECT")) + " (GOOGLE_CLOUD_QUOTA_PROJECT), not to the new project"
	case adcQuota != "":
		return who + "; calls are billed to quota project " + sanitized(adcQuota) + " (from your credentials), not to the new project"
	}
	return who + "; your credentials name no quota project, so calls may be refused: set one with gcloud auth application-default set-quota-project <a project of yours> (the new project cannot be its own quota project)"
}

func (s *projectStage) declined(what string) error {
	return &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Project, Kind: initflow.LeftPrompt,
		Text: "not confirmed (" + what + " was not typed): rerun " + selfCommand() + " init in your own terminal window and type it, nothing further was done"}}
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
	created := false
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
		ok, err := s.typed(fmt.Sprintf("this CREATES the Google Cloud project %q (display name %q) with %s, and adds Firebase to it, %s. "+
			"The ID is global and permanent: nobody can reuse it, even after the project is deleted. It will be used for everything Fugaro runs. "+
			"Billing is linked separately: this confirmation does not link it.",
			s.id, name, parent, s.credentialNote()), s.id, "to create it")
		if err != nil {
			return initflow.Outcome{}, err
		}
		if !ok {
			return initflow.Outcome{}, s.declined("the project's ID")
		}
		spec := infra.CreateSpec{ID: s.id, DisplayName: name, Parent: o.parent, OnOperation: func(op string) {
			fmt.Fprintf(r.w, "creating project %s (operation %s): if this run stops before it is readable, wait a few minutes and rerun with the same ID\n", s.id, op)
		}}
		if err := infra.CreateProject(ctx, s.pc, spec); err != nil {
			return initflow.Outcome{}, s.fail(err)
		}
		if err := infra.WaitReadable(ctx, s.pc, s.id); err != nil {
			return initflow.Outcome{}, s.fail(err)
		}
		r.res.Created = s.id
		created = true
		did = append(did, "created project "+s.id)
		fmt.Fprintf(r.w, "created project %s\n", s.id)
		s.addFirebase = true
	} else if s.addFirebase {
		ok, err := s.typed(fmt.Sprintf("%s: it has no Firebase, and this ADDS Firebase to it (free; Firebase is how Fugaro keeps its budget and dashboard data), %s. Nothing else about the project changes.", existingNote(s.id), s.credentialNote()), s.id, "to add Firebase to the existing project")
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
		lead := ""
		if !created {
			lead = existingNote(s.id) + ": "
		}
		ok, err := s.typed(fmt.Sprintf("%sthis LINKS billing account %s to project %s. Everything Fugaro runs in the project (Cloud Run jobs, builds, storage, Firestore, Vertex AI calls) may incur charges on that account. "+
			"It is not undone by deleting resources; unlink it with gcloud billing projects unlink %s.", lead, acct, s.id, quoteWord(s.id)), acct, "to link it")
		if err != nil {
			return initflow.Outcome{}, err
		}
		if !ok {
			return initflow.Outcome{}, s.declined("the billing account's ID")
		}
		if err := infra.LinkBilling(ctx, s.pc, s.id, acct, !created); err != nil {
			var pe *infra.ProjectError
			if errors.As(err, &pe) && pe.Kind == infra.ProjectLinkGuard {
				fmt.Fprintln(r.w, pe.Error())
				return initflow.Outcome{}, &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Project, Kind: initflow.LeftCommand, Text: s.guided()}}
			}
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
