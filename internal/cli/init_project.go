package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"

	"github.com/dimipaun/fugaro/internal/billing"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
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
//   - print the account a project is linked to: the project's own billing is
//     read (infra.ReadProject), and the account that read names is
//     deliberately not carried out of it (infra.ProjectInfo has no field for
//     one), so no mode of this stage can print it. The wizard's billing
//     question does list the operator's open accounts, and only there and
//     only where canType() says yes (billing_prompt.go, W4.3; the reversal
//     and its bounds are written down in internal/billing's package doc).
//     This stage lists nothing itself, in any mode.
//
// Without --link-billing the stage prints the one gcloud line that links
// billing and stops needs-you. The calls are the user's own
// (infra.ProjectClients; no service account); the quota project is the one
// in the user's credentials, and the confirmation says so. See
// infra/projectcreate.go for what is unverified against the real services.

func (o *initOptions) checkCreateProject() error {
	if !o.createProject {
		switch {
		case o.managed:
			return userErr("--managed goes with --create-project: it is what a project this run creates is, and only a creation can be one — an existing project is made managed by the owner's own command instead (never by update --firebase-project, which adds Firebase to a project someone else made). To create a managed project: fugaro setup --create-project --gcp-project <id> --managed --budget <amount> --budget-currency <code> --billing-account <id>; to flag one that already exists: fugaro managed flag <id> --budget <amount> --budget-currency <code>, before a person, who types its ID")
		case o.linkBilling != "":
			return userErr("--link-billing goes with --create-project (both flags, explicitly: fugaro setup links billing only to a project it was asked to create or adopt). To link billing yourself: gcloud billing projects link <project> --billing-account=<account>")
		case o.parent != "" || o.displayName != "":
			return userErr("--parent and --display-name are for --create-project")
		}
		return nil
	}
	switch {
	// A managed creation has no amount to fall back on (D64, Task 4.2): the
	// monthly budget is required, and the refusal says what to pass and what
	// a value to start from looks like. There is deliberately **no constant
	// here and none anywhere else** — the 50 below is a sentence in a
	// refusal, not a value any code path can use, because a silent default
	// is a number the owner did not choose that then reads as a promise.
	// This case is why it comes before the ordinary trio rule in check():
	// the same missing flags are a different answer for a managed creation,
	// and this is the one that says why the amount is being asked for.
	case o.managed && (o.budget <= 0 || o.budgetCurrency == "" || o.billingAccount == ""):
		return userErr("--managed creates a project Fugaro manages, and a managed project's monthly budget amount is required at creation — with no default, because an amount you did not choose reads as a promise and one that is too low is discovered by a bill. Pass all three: --budget <amount> --budget-currency <code> --billing-account <id>. For a first installation --budget 50 --budget-currency USD is a reasonable start (the alerts fire at 50%%, 90%% and 100%% of it); raise it once you have seen a month of spend. The amount and the currency are recorded in the project's creation fact, so a later run and a restore can read what was chosen")
	// The figures a managed creation records are held to the rule the
	// marker's write and read share (managed.Budget.Check: a whole amount
	// from 1 to 10,000,000 units of the currency, and a three-letter
	// uppercase ISO 4217 code), here, before anything is created: a value the
	// marker would refuse refuses the run that would write it, rather than
	// creating a project whose creation fact cannot then be recorded.
	case o.managed && (managed.Budget{Amount: o.budget, Currency: o.budgetCurrency}).Check() != nil:
		return userErr("--managed: %v. Pass --budget as a whole amount from %d to %d units of the currency and --budget-currency as the billing account's three-letter ISO 4217 code in upper case, such as USD", (managed.Budget{Amount: o.budget, Currency: o.budgetCurrency}).Check(), managed.MinBudgetAmount, managed.MaxBudgetAmount)
	case o.forget || o.configOnly || o.printVars:
		return userErr("--create-project creates a project; it excludes --forget, --config-only and --print-vars")
	case o.cloud.gcpProject == "":
		return userErr("--create-project needs --gcp-project <id>: the ID to create (fugaro setup never uses the gcloud default project)")
	}
	// The pairing rule, read through the default (firebaseProject, D7): a
	// project this run creates is its own Firebase project, which is the
	// default path, and the only way it is another one is --firebase-project
	// saying so — refused, because fugaro setup creates one project and only
	// the project that holds the installation.
	if fp := o.firebaseProject(); fp != o.cloud.gcpProject {
		return userErr("--create-project creates --gcp-project %s, and --firebase-project %s is another project: fugaro setup creates only one, and only the project that holds the installation; create %s yourself, or pass --firebase-project %s", o.cloud.gcpProject, fp, fp, o.cloud.gcpProject)
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

// answer is what this run said about the project: create one, or adopt one that
// already exists. It is the wizard's first question's answer (setup.go's
// askProjectAnswer) or --create-project, which is the same field, because it is
// the same question — a scripted run answers it with the flag instead of at a
// terminal. Nothing here reads the flag: the stage acts on the answer, so that
// a run whose answer was adoption creates nothing however it came to run.
//
// adoptDetail is what the stage says when the answer was adoption. It is a
// skip and not a check: whether the project is there is the installation's own
// question, and it is asked by the run that applies into it (a GCP project
// that is missing fails there, with the message that has always said so).
const adoptDetail = "not creating: this run's answer was to adopt an existing GCP project"

func (s *projectStage) answer() bool { return s.o().createProject }

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
// account, and no account is read. It is billing.Guided's own words (W4.2),
// which is also what the wizard's billing question (billing_prompt.go, W4.3)
// prints when it has no account to offer: one line printed in two places is
// one line, and a rewording of it cannot leave the two disagreeing.
func (s *projectStage) guided() string { return billing.Guided(s.id) }

func billingHelp() string {
	return "find the account's ID with gcloud billing accounts list, or in https://console.cloud.google.com/billing; then run the command, or rerun " + selfCommand() + " setup with --create-project --link-billing BILLING_ACCOUNT_ID to type its confirmation here"
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
	// The fact markCreated's --managed warning needs, recorded where it is
	// read: whether the project this run installs into was already there.
	// r.res.Created alone cannot tell a run that adopted an existing project
	// from one that never reached the creation at all (see
	// initRun.projectExisted).
	s.e.r.projectExisted = info.Exists
	// fugaro restore's child: a project that is already there is refused,
	// never adopted (an adopted project gets no creation fact, and the
	// installation would be applied into someone else's or a stale project).
	// The project this run created itself is the one exception: the stage
	// reads it again after the creation.
	if info.Exists && s.o().strictCreate && s.e.r.res.Created != s.id {
		return userErr("GCP project %s already exists, and this creation must make a new one (fugaro restore never adopts a project): nothing was created or applied", s.id)
	}
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
			s.guide, s.whyGuide = true, "the project's billing cannot be read, so --link-billing is not applied (fugaro setup never replaces a link it cannot see)"
		case given:
			s.guide, s.whyGuide = true, "the project is linked to a billing account that is not enabled (suspended or closed), and fugaro setup never replaces an existing link: --link-billing is not applied"
		default:
			s.guide = true
		}
	}
	return nil
}

func (s *projectStage) Check(ctx context.Context) (initflow.Status, error) {
	if !s.answer() {
		// Adoption: nothing to create, and nothing to read either. The answer
		// is the stage's whole input, so a run that answered "adopt" reaches no
		// project API at all.
		return initflow.Status{State: initflow.Skipped, Detail: adoptDetail}, nil
	}
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
		note = "; billing is already linked, and fugaro setup never changes an existing link (--link-billing is not applied)"
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
		parts = append(parts, "billing is not linked by fugaro setup without --link-billing: "+s.guided())
	}
	return "will " + strings.Join(parts, ", ")
}

func (s *projectStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	if !s.answer() {
		return initflow.Plan{NothingToDo: true, Detail: adoptDetail}, nil
	}
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
		args := []string{s.o().doorName(), "--create-project", "--gcp-project", quoteWord(s.id)}
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
//
// It is one predicate in one place, asked of the run, because two things ask
// it: this stage's typed confirmations, and the wizard's billing question
// (billing_prompt.go, W4.3), whose whole reason to exist is bounded by it —
// listing the operator's billing accounts is a reversal internal/billing's
// package doc limits to exactly these four conditions, and two copies of them
// would be two places to widen by accident.
func (r *initRun) canType() bool {
	o := r.o
	return !o.nonInteractive && !o.asJSON && !agentEnv(os.Getenv) && stdinIsTerminal(r.cmd.InOrStdin())
}

// canType is this stage's typed confirmation, asked of the run it is in.
func (s *projectStage) canType() bool { return s.e.r.canType() }

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
	return "adopting an EXISTING project " + id + " (not created by this run; fugaro setup cannot tell whether it is yours or empty)"
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
	path, err := adcFilePath(os.Getenv, runtime.GOOS)
	if err != nil {
		return "", ""
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
		Text: "not confirmed (" + what + " was not typed): rerun " + selfCommand() + " " + s.e.r.o.doorName() + " in your own terminal window and type it, nothing further was done"}}
}

func (s *projectStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if s.started {
		// The one-project-per-run invariant first: it holds whatever the
		// answer was, and it is the stage's own, not the run's.
		return initflow.Outcome{}, errors.New("fugaro setup creates at most one project per run: it has already acted in this run")
	}
	if !s.answer() {
		// Adoption creates nothing, and asks nothing: a run that answered
		// "adopt" has no typed confirmation to leave for anyone, whatever its
		// terminal is.
		return initflow.Outcome{Detail: adoptDetail}, nil
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
			return initflow.Outcome{}, s.failCreate(err)
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
			return initflow.Outcome{}, s.failLink(err, acct)
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

// The owner's ruling of 2026-10-23: the only supported path is a project this
// run creates and links, so the two calls that make one -- Cloud Resource
// Manager's create and Cloud Billing's link -- are the two a person may be
// refused. Where they are, the API's own answer is not an answer: it names a
// permission or a constraint and neither what to ask for nor whom to ask.
// refusedCreate and refusedLink say that; infra.ProjectError.Refused says when
// a failure is that case and nothing else does. Every other failure -- a
// quota, an ID someone else holds, an API that is off -- keeps the message it
// always had (fail), and the message here carries the service's own text under
// the helpful line, so nothing the API said is hidden.

// refusedCreate is what a refused creation says: what happened, and what to
// ask the organization for.
func (s *projectStage) refusedCreate() string {
	return "Fugaro could not create the project " + s.id + ": your Google account is not allowed to create projects in your organization. " +
		"Ask your organization's admin (IT) for the Project Creator role on the organization or a folder, or ask them to create the project and make you its Owner. " +
		"See docs/gcp-setup.md#not-an-owner."
}

// refusedLink is what a refused billing link says. The link needs a role on
// the billing account *and* one on the project (infra.LinkBilling's hint), and
// the 403 that comes back names neither, so both are here: naming only the
// billing one would send a reader who holds it -- the one adopting an existing
// project they are not an Owner of -- to the billing administrator for a role
// they already have, and the loop keeps only this first line for the stage
// (initflow's oneLine), so the hint under it is not a place to put them. The
// two non-role causes the same 403 can have (a closed account, an account at
// its limit of linked projects) are here too, or a reader holding both roles
// would be told to ask for roles they have. The one line that links it, and
// the door the reader is at (setup creates a project, update re-converges
// one), are the last of it: whoever acts for them runs the line, and the
// reader runs the door again.
func (s *projectStage) refusedLink(account string) string {
	return "Fugaro could not link billing account " + account + " to project " + s.id + ": your account is missing a role the link needs. " +
		"Ask whoever administers " + account + " for the Billing Account User role on it, and whoever owns " + s.id + " for the Owner or Project Billing Manager role on it; " +
		"either of them can link it for you instead, with the one line `gcloud billing projects link " + s.id + " --billing-account=" + account + "`. " +
		"Then run `" + selfCommand() + " " + s.o().doorName() + "` again. If you hold both roles already, the billing account may be closed, or at its limit of linked projects."
}

// failCreate and failLink are fail for those two calls: a recognised refusal
// is a user error (exit 1, the reader's to act on) whose message is the
// helpful line with the service's own text under it.
func (s *projectStage) failCreate(err error) error { return s.failRefused(err, s.refusedCreate()) }

func (s *projectStage) failLink(err error, account string) error {
	return s.failRefused(err, s.refusedLink(account))
}

func (s *projectStage) failRefused(err error, help string) error {
	var pe *infra.ProjectError
	if errors.As(err, &pe) && pe.Refused {
		return &initflow.StageError{Err: userErr("%s\n%s", help, err), Fix: pe.Fix}
	}
	return s.fail(err)
}

// Verify re-reads the project: Apply verified each step it took; this is
// the closing check that nothing is left half done.
func (s *projectStage) Verify(ctx context.Context) error {
	if !s.answer() {
		return nil // an adopted project is not this stage's to check
	}
	if err := s.read(ctx); err != nil {
		return s.fail(err)
	}
	if !s.info.Exists || !s.info.Firebase {
		return fmt.Errorf("project %s is not ready after the apply (exists: %v, Firebase: %v)", s.id, s.info.Exists, s.info.Firebase)
	}
	return nil
}
