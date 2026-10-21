package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/infra"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// fugaro managed apply (docs/design/managed-projects.md, D76) applies the
// agent-authored Terraform root of a managed project: the root in the
// repository checkout, copied into a workdir of this command's own, planned
// into a saved file, gated by the deterministic gate (Tasks 1.0-1.3) with the
// addresses the OWNER's object approves (Task 1.4, D72/D74), and applied as
// the deployer account of D75 (Task 1.5) rather than as the operator.
//
// Why this command exists at all: D55 removes the coding agent's refusal in a
// managed project, and the root it removes it for is the agent-authored one.
// Fugaro's own embedded roots keep their confirmations — their plans carry
// project-level IAM the deployer may not make, so the managed gate refuses them
// and a refused plan falls back to a confirmation (D75 (ii)) — which leaves
// exactly one root with nothing to apply it, and this is that command.
//
// What it is NOT, stated here rather than implied, because this is the change
// the design's §6 item 4 is about: nothing here is a boundary against an agent
// that goes around the program. The agent shares the container, the user and
// the metadata server with the runner (SECURITY.md), so it can run terraform or
// gcloud directly, write the marker and the delete list with the operator's own
// credentials, and impersonate the deployer the operator may impersonate. What
// the gate, the marker, the owner's list and the deployer account stop is the
// accident: a session that goes through this program applies only what the
// owner's own rules admit, and applies it as an identity that holds no
// project-level IAM permission and no write path to the runs bucket.
//
// And the gate it runs behind is the apply-time budget read (Task 4.4), not a
// release switch: managedmode's Enable is opened by one thing and that thing is
// the read of the project's creation fact through internal/infra/managed's
// ReadBudget, which must answer with an amount above 0 and a currency. No
// constant in the build opens it any more, and every other answer — no marker
// at all, a version-1 marker with no amount recorded, a read that failed, a
// malformed, oversized or trailing-data object, a marker naming another
// project, an amount or a currency the marker's own reader refuses — is a hard
// refusal.
//
// Where in this command's order that read happens, stated because it is not
// first and was: the flags, the checkout and the cloud clients come first (the
// command cannot read anything about the project before it knows which one),
// then the runs bucket is pinned to its default name and the project number is
// read, and only then the apply-time budget read and the gate it opens — after
// which come the owner's delete list, the workdir and terraform. A read that
// does not answer therefore stops the command before a plan exists, before the
// root is copied, before terraform is looked for and before the deployer is
// assumed. managed.Apply refuses the same value again as its own first step, so
// neither check can be left out.

// newManagedCmd is `fugaro managed`: the noun group D76 answers with, which
// leaves room for the plan's Task 6.1 flagging command under the same noun. It
// is not `fugaro project` (D68's noun, untouched): that one is about the
// project's own configuration, and this one is about applying a root inside a
// managed project.
func newManagedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "managed",
		Short: "Act on a managed project (a project created by fugaro with the coding agent's refusal removed)",
		Long: `Act on a managed project: one Fugaro created with the coding agent's
confirmation refusal removed, where what may be applied is decided by a
deterministic gate over the saved plan rather than by a prompt.

This is not fugaro project (the project's own configuration) and not fugaro
init (which creates and re-installs an installation): a managed project's
agent-authored root is applied by fugaro managed apply.`,
	}
	cmd.AddCommand(newManagedApplyCmd(), newManagedFlagCmd())
	return cmd
}

// newManagedApplyCmd is `fugaro managed apply ROOT`.
func newManagedApplyCmd() *cobra.Command {
	var o cloudOptions
	cmd := &cobra.Command{
		Use:   "apply ROOT",
		Short: "Apply the agent-authored Terraform root of a managed project, as the deployer account",
		Long: `Apply the agent-authored Terraform root ROOT of a managed project.

ROOT is a directory in this repository's checkout (the checkout whose
fugaro.yaml names the project). Its files are copied into this command's own
workdir — never run in place, so terraform's plan file, lock file and provider
directory are not written beside the configuration under review — the state
backend is set to the project's own managed state bucket, the root is planned
into a saved plan file, the plan is gated against the project's managed
allowlist and against the addresses the project's OWNER approved, and the saved
plan file is applied as the project's deployer service account.

An address this command may delete or replace is never one of this command's
arguments: an approval lives in the owner's object at
gs://<runs bucket>/fugaro/allow-delete.json, written with the operator's own
credentials by fugaro allow-delete. This command registers no flag for one, so
there is no way to pass one.

The refusal this command's apply does not carry is the coding agent's: in a
managed project a coding agent's session applies cloud changes with no human
prompt, and what replaces the prompt is the gate (a refused plan is not
applied), the marker (an unreadable one is not an apply), the owner's list (an
address the owner did not approve is refused) and the deployer account (which
holds no project-level IAM permission and cannot write the runs bucket). None
of the four is a boundary against a session that goes around this program.

The prompt comes back whenever the whole-project inventory does not clear the
project: before anything is planned, this command reads every resource of the
GCP project (Cloud Asset, with the operator's own credential) and compares it
with what the installation's Terraform states account for. A resource nothing
accounts for, or an inventory that cannot be read, keeps this run's
confirmation: a coding agent's session is refused and a person is asked, with
the reason printed first. Nothing is halted and nothing is deleted.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return managedApply(cmd, o, args[0])
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// managedApply is newManagedApplyCmd's body. The order is the whole of the
// design: the local checks, then the project and its number, then the
// apply-time budget read and the gate it opens (Task 4.4), then the reads the
// apply itself trusts, then the apply — and every refusal before the apply is a
// hard return.
func managedApply(cmd *cobra.Command, o cloudOptions, rootArg string) error {
	ctx := cmd.Context()
	// No deployer to apply as in this release (managedDeployerShipped):
	// refused before the project is read or the cloud opened.
	if err := refuseManagedNotShipped(); err != nil {
		return err
	}
	// A credential leak first, before anything opens the cloud: GODEBUG is
	// read before main runs, so it cannot be unset in time (refuseHTTP2Debug).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", safetext.Field(w))
	}
	root, err := managedRoot(ctx, lc, rootArg)
	if err != nil {
		return err
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return err
	}
	// The runs bucket's default name and no other, the rule the managed
	// objects already live by (allowDelete): the marker and the delete list
	// are read from fugaro-runs-<gcp_project>, so a config that names another
	// bucket would make this command read objects nobody wrote for it.
	bucket := "fugaro-runs-" + lc.GCPProject
	if got := lc.RunsBucketName(); got != bucket {
		return userErr("project %s names the runs bucket %q, and a managed project's marker and delete list live in %s: a managed apply reads the objects of the project's own runs bucket, so check runs_bucket in the project config", safetext.Field(lc.Name), safetext.Field(got), safetext.Field(bucket))
	}
	// The project number is what says a bucket name is this project's (a
	// bucket name is global), and both reads below prove it with.
	number, err := infra.ProjectNumber(ctx, c, lc.GCPProject)
	if err != nil {
		// The API's own text can carry a body: one sanitised line.
		return remote(fmt.Errorf("reading the number of GCP project %s: %s", safetext.Field(lc.GCPProject), safetext.Field(err.Error())))
	}
	// The apply-time budget read, and the gate it opens (Task 4.4). Both
	// happen here, after the project and its number are known and before
	// anything is assumed or planned, and a refusal ends the command: a gate
	// that did not open runs no terraform at all.
	gate, fact, err := managedBudgetGate(ctx, c, lc)
	if err != nil {
		return userErr("nothing was applied: %v", err)
	}
	// The addresses the owner approved, read fail-closed from the owner's own
	// object. The applying command names none — this command registers no
	// --allow-delete and no address argument — so the guard's first half (an
	// address from the command line is refused rather than honoured) cannot
	// fire here, and it is passed nil rather than left out so that the one
	// function that enforces the rule is still the one that runs it.
	allowed, err := managed.GuardAllowList(ctx, c.Storage, bucket, lc.GCPProject, number, nil)
	if err != nil {
		return managedListErr(lc, bucket, err)
	}
	// The whole-project inventory (Task 4.5, D59), read here because this is
	// the last moment the operator's own credential is the one in hand: the
	// Cloud Asset read role is granted to the operators, the deployer holds
	// neither it nor the installation's state bucket, and everything after
	// the impersonation below runs as the deployer. It cannot fail the
	// command — every way it can go wrong is a fallback that keeps the run's
	// confirmations (managedInventory's own comment) — and the answer is
	// handed to the apply, which asks D55's rule with it.
	inventory := managedInventory(ctx, lc, c, number)
	// Who is running, and with which credential: the impersonation grant is
	// on people (deployer.tf grants it to var.operators), so this is the
	// operator's own identity and the credential the IAM Credentials calls
	// are made with.
	self, ts, err := managedCaller(ctx, lc)
	if err != nil {
		return err
	}
	// The workdir is this command's own, never the checkout (D76): terraform
	// writes a plan file, a lock file and a provider directory, and the
	// checkout belongs to the agent and to the user.
	dir, err := infra.ManagedWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return userErr("%v", err)
	}
	wd, err := infra.CopyRoot(dir, root)
	if err != nil {
		return userErr("the root %s: %v", safetext.Field(rootArg), safetext.FieldErr(err))
	}
	cache, err := infra.PluginCache(os.Getenv)
	if err != nil {
		return userErr("%v", err)
	}
	bin, err := exec.LookPath("terraform")
	if err != nil {
		return userErr("fugaro managed apply needs terraform (>= 1.9, < 2) on PATH: %v", err)
	}
	imp := deployerImpersonator{self: self, ts: ts, endpoint: impersonationEndpoint, hc: impersonationHTTPClient()}
	res, err := managedmode.Apply(ctx, managedmode.Request{
		// The gate the budget read above opened, handed over rather than
		// rebuilt: managed.Apply refuses a closed one as its own first step,
		// so the value is checked twice and neither check can be left out.
		Enable:  gate,
		Project: lc.Name, GCPProject: lc.GCPProject,
		Fact: fact,
		// The whole-project inventory read above: D59's third fact of the
		// confirmation rule, and the reason this run keeps its confirmations
		// when the read found a resource the state does not account for or
		// could not be made at all (Task 4.5).
		Inventory: inventory,
		Workdir:   wd,
		// Terraform is built inside the deployer's assumption, around the
		// credential it was handed: the plan is read from this root's own
		// state bucket (D77), which the deployer holds and the operator's
		// identity need not, so init, plan, the show the gate reads and the
		// apply are one identity's.
		Terraform: func(cred managedmode.Credential) (*tf.TF, func(), error) {
			return managedTerraform(cmd, lc, wd, bin, cache, cred)
		},
		Cover: managedCover(lc, bucket),
		// The addresses the owner approved and nothing else.
		AllowDelete: allowed,
		// What this session says about who is there: the marker its
		// environment carries and whether its own terminal is one. It is the
		// input of D55's confirmation rule that only the caller has, and the
		// apply asks the rule once the gate has a verdict (managed.Request's
		// own comment).
		Conditions: managedConditions(cmd),
		// And the person's own answer, when the session is a person's: the
		// project's name typed at the terminal, after a question that names
		// the saved plan's hash and what it changes. A coding agent's session
		// is never asked (D55), and a session with no terminal never reaches
		// this (the rule refuses it first).
		Ask: managedAsk(cmd, lc),
		// Session is nil, and it is written out rather than left to the zero
		// value because the field is the assertion: this command registers no
		// flag and no argument that names an address, so there is no way to
		// put one here — and managed.Apply refuses a non-empty one by name
		// whatever the owner's list says (Task 1.4's rule, D72/D74).
		Session: nil,
		// No imports: the gate admits an import only when its address and ID
		// are exactly one the root's own discovery wrote (tf.Cover's rule),
		// and this command does no discovery — so every import block in a
		// managed root is refused rather than adopted unchecked. That is the
		// fail-closed direction, and a task that discovers a managed root's
		// imports is what would pass them here.
		Imports: nil,
		Access:  deployerAccess{svc: c.IAM, project: lc.GCPProject},
		// The impersonator is the one above, whose Self is the operator's
		// own member: the account is assumed by the identity the grant is
		// on, and an assumption that cannot be made applies nothing.
		Impersonator: imp,
		Out:          cmd.ErrOrStderr(),
	})
	if err != nil {
		return managedApplyErr(lc, rootArg, res, err)
	}
	w := cmd.OutOrStdout()
	if !res.Changed {
		fmt.Fprintf(w, "no changes: the plan of %s is empty, so nothing was gated and nothing was applied\n", safetext.Field(rootArg))
		return nil
	}
	fmt.Fprintf(w, "applied the saved plan of %s as %s: the gate admitted every change in it, and the owner's list approved every delete and replacement it carries\n",
		safetext.Field(rootArg), safetext.Field(managedmode.DeployerAccount(lc.GCPProject)))
	return nil
}

// managedConditions is what this session says about who is there, and it is
// the same pair internal/cli already reads for its other applying steps
// (image.go's conds): a coding agent's marker in the environment, which is the
// only thing that reaches D55's managed case (initflow.Managed.Removes), and
// whether this command's own stdin is a terminal. It decides nothing here — it
// is not read until the plan has been gated, and managed.Apply asks the rule
// once (managed.Request's own comment).
//
// The command registers no --yes and no --non-interactive, so neither appears
// here: a person at a terminal is ASKED (managedAsk: the project's name typed
// after a question naming the plan's hash and counts), and a session with no
// marker and no terminal has no confirmation to give. That is the fail-closed
// direction, and the refusal a person then reads is initflow's own
// NoTerminalAdvice.
func managedConditions(cmd *cobra.Command) initflow.Conditions {
	return initflow.Conditions{Terminal: stdinIsTerminal(cmd.InOrStdin()), Agent: agentMarker(os.Getenv)}
}

// managedAsk is the question a person at a terminal answers before a managed
// apply: the apply's own words (the project, the saved plan's hash and its
// counts), and the project's name typed back. Anything else is a no.
func managedAsk(cmd *cobra.Command, lc *localcfg.Config) func(string) (bool, error) {
	return func(question string) (bool, error) {
		out := cmd.ErrOrStderr()
		fmt.Fprintf(out, "⚠ CONFIRM (project %s): %s\nType %s to confirm: ", safetext.Field(lc.Name), question, safetext.Field(lc.Name))
		line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		return strings.TrimSpace(line) == lc.Name, nil
	}
}

// managedRoot is ROOT as the apply reads it: an absolute directory inside the
// checkout of the project this command acts on.
//
// Why the checkout is checked and not only the directory: D75 (ii) says the
// deployer applies the agent-authored root IN THE PROJECT REPOSITORY, and a
// root outside the checkout it belongs to is a root nobody reviewed — an agent
// that can point this command at any directory on the machine can have it apply
// a tree it never committed, which is the whole of the property the pull
// request is supposed to carry. A root that is in no checkout, or in another
// project's, is refused.
func managedRoot(ctx context.Context, lc *localcfg.Config, rootArg string) (string, error) {
	abs, err := filepath.Abs(rootArg)
	if err != nil {
		return "", userErr("the root %s: %v", safetext.Field(rootArg), safetext.FieldErr(err))
	}
	st, err := os.Stat(abs)
	switch {
	case err != nil:
		return "", userErr("the root %s: %v", safetext.Field(rootArg), safetext.FieldErr(err))
	case !st.IsDir():
		return "", userErr("the root %s is not a directory", safetext.Field(rootArg))
	}
	co, err := checkoutProject(ctx, abs)
	if err != nil {
		return "", err
	}
	if co == nil {
		return "", userErr("the root %s is in no checkout with a fugaro.yaml, and a managed apply runs the root of the project repository (D75): run it on a directory of the checkout whose fugaro.yaml names %s", safetext.Field(rootArg), safetext.Field(lc.Name))
	}
	if co.Project != lc.Name {
		return "", userErr("the root %s is in the checkout of project %s, and this command is acting on %s: a managed apply runs the root of the project it applies for", safetext.Field(rootArg), safetext.Field(co.Project), safetext.Field(lc.Name))
	}
	// co.Root is abs's own git toplevel (checkoutProject resolved it), so abs
	// is inside the checkout by construction and nothing further is checked
	// here: what a symlink out of the tree would mean is CopyRoot's question,
	// and it refuses one.
	return abs, nil
}

// managedCover is the managed instantiation of the gate for this installation:
// Task 1.1's managed allowlist, with tf.Cover's own inputs taken from the
// project's config.
//
// Two of them are deliberately empty, and the direction of each is a refusal
// rather than a permission:
//
//   - Listed is the members a plan may grant a role to. A managed apply has no
//     review screen, so the members it lists are the config's own — the
//     launchers, the operators and the budget admins (terraform.budget_admins),
//     the people this installation's config says may act on the project. It is
//     the config's side of what init's review prints (init_review's
//     accessLists) and deliberately not all of it: the review's list also
//     carries the GCP project's owners and editors, which it reads from IAM
//     live, and this command reads no IAM — so a plan granting a role to one of
//     them is refused here (the deployer's credential holds no project-level
//     IAM permission at all, D75 (iii)). The review's budget viewers are the
//     launchers and operators again, so they widen nothing. A member outside
//     the config's own lists is refused.
//
// The budget admins are the half that was missing, and the direction of the
// bug was the wrong one: a plan init's own gate admits — a role for a
// configured budget admin — was refused by this command, forever, with a
// sentence about a review screen it does not have.
//   - Billing is the billing account a resource may name. The config records
//     none, and a managed plan that creates a billing budget is refused by the
//     managed type list anyway (D75 (iv)), so an empty list refuses every
//     resource that names an account rather than admitting one this command
//     could not read.
func managedCover(lc *localcfg.Config, bucket string) tf.ManagedCover {
	c := tf.Cover{
		Projects: []string{lc.GCPProject},
		Region:   lc.Region,
		Buckets:  []string{bucket},
		Ghcr:     infra.GhcrRegistry,
		Owner:    baseOwner(lc),
	}
	if host, err := infra.RegistryHost(lc); err == nil {
		c.Registry, _, _ = strings.Cut(host, "/")
	}
	c.Listed = slices.Concat(lc.Terraform.Launchers, lc.Terraform.Operators, lc.Terraform.BudgetAdmins)
	return tf.ManagedCover{Cover: c}
}

// managedTerraform builds the terraform the apply runs, around the credential
// the deployer's assumption produced, and returns the cleanup that removes
// that credential's file — which the apply runs once every terraform it built
// has finished (managed.Request.Terraform's own contract). The file has to
// outlive this function: init, plan, show and apply all run later, each with
// GOOGLE_APPLICATION_CREDENTIALS naming it.
//
// The environment is managedmode.TerraformEnv's (tf.Env's allowlist, and the
// assertion that no provider setting of the applier's shell is in it), with the
// ONE variable that names a credential file replaced by this command's own:
// terraform has no other channel for "run as the deployer" — tf.Env refuses
// GOOGLE_IMPERSONATE_SERVICE_ACCOUNT, and TerraformEnv refuses
// GOOGLE_OAUTH_ACCESS_TOKEN and GOOGLE_CREDENTIALS by name — and the file is
// the shape gcloud's own --impersonate-service-account writes. The replacement
// happens BEFORE TerraformEnv is called, so the assertion runs over exactly the
// environment terraform will run with.
//
// The file is the credential itself, not a copy of a claim about it: what it
// holds is the impersonation URL the credential was minted through
// (deployerImpersonationURL), and the account that URL names is checked again
// here against the deployer account and against cred.Account() before a byte is
// written. It lives in a directory of its own (credentialDir), never in the
// workdir — the root's parent, which an agent-written configuration names as
// ".." — and the managed preflight refuses every function that could read a
// file anyway.
func managedTerraform(cmd *cobra.Command, lc *localcfg.Config, wd *infra.Workdir, bin, cache string, cred managedmode.Credential) (*tf.TF, func(), error) {
	none := func() {}
	want := managedmode.DeployerAccount(lc.GCPProject)
	ic, ok := cred.(impersonatedCredential)
	if !ok {
		return nil, none, fmt.Errorf("the credential for %s does not name the impersonation it was minted through, and terraform's credential file is that impersonation or nothing", safetext.Field(cred.Account()))
	}
	u := ic.ImpersonationURL()
	account, err := impersonationAccount(u)
	switch {
	case err != nil:
		return nil, none, err
	case account != want || cred.Account() != want:
		// The credential is checked against the deployer account by
		// managed.Deployer before this runs; this is the second check, in the
		// one place that would otherwise write another account's credential
		// file — and it reads the account out of the file's own URL.
		return nil, none, fmt.Errorf("the credential is for %s (its impersonation names %s), not for the deployer account of %s", safetext.Field(cred.Account()), safetext.Field(account), safetext.Field(lc.GCPProject))
	}
	file, cleanup, err := writeImpersonationFile(u)
	if err != nil {
		return nil, none, err
	}
	parent := replaceEnv(os.Environ(), "GOOGLE_APPLICATION_CREDENTIALS", file)
	env, err := managedmode.TerraformEnv(parent, wd.Dir, cache)
	if err != nil {
		cleanup()
		return nil, none, userErr("%v", err)
	}
	t, err := tf.New(bin, wd.Root, env)
	if err != nil {
		cleanup()
		var ee *tf.ExitError
		if errors.As(err, &ee) {
			return nil, none, remote(safetext.FieldErr(err))
		}
		return nil, none, userErr("%v", safetext.FieldErr(err))
	}
	t.Out = cmd.ErrOrStderr()
	return t, cleanup, nil
}

// impersonatedCredential is a credential that names the impersonation it was
// minted through: the URL terraform's credential file carries.
type impersonatedCredential interface {
	ImpersonationURL() string
}

// impersonationFile is the file terraform's credential variable names: the ADC
// library's own "impersonated_service_account" form, which is what
// `gcloud auth application-default login --impersonate-service-account` writes.
// SourceCredentials is the operator's own credential, verbatim, and the URL is
// the one call that turns it into the deployer's.
type impersonationFile struct {
	Type                           string          `json:"type"`
	ServiceAccountImpersonationURL string          `json:"service_account_impersonation_url"`
	SourceCredentials              json.RawMessage `json:"source_credentials"`
}

// credentialDir is where the credential file's own directory is made: the
// system's temporary directory, and a variable so tests make it under theirs.
// It is never the workdir: the workdir is the root's parent.
var credentialDir = ""

// writeImpersonationFile writes that file into a directory of its own (mode
// 0700, the file 0600) and returns its path and the function that removes
// both.
//
// What it refuses: an operator whose credential is not a person's. The
// impersonation grant of D75 is on var.operators, which are user members, and
// the identity this command reports (managedCaller) is a person's — so a
// service account's credential, or no credential at all, is a refusal here
// rather than a file whose source is something else.
func writeImpersonationFile(impersonationURL string) (string, func(), error) {
	none := func() {}
	path, err := adcFile()
	if err != nil {
		// A user error (exit 1), as main's own copy of this lookup was: where
		// the credentials are and whether they exist is the operator's
		// environment, not a cloud failure. Without the wrap, this error
		// reaches managedApplyErr's default arm, which reports exit 2 and the
		// wrong reason (a terraform failure) for a machine that merely has no
		// ADC on it.
		return "", none, userErr("%v", safetext.FieldErr(err))
	}
	src, err := readBounded(path)
	if err != nil {
		return "", none, userErr("reading your Application Default Credentials: %v (run gcloud auth application-default login)", safetext.FieldErr(err))
	}
	var kind struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(src, &kind) != nil || kind.Type == "" {
		return "", none, userErr("your Application Default Credentials (%s) are not a credential file this command reads: run gcloud auth application-default login", safetext.Field(path))
	}
	if kind.Type != "authorized_user" {
		return "", none, userErr("your Application Default Credentials are a %s credential, and a managed apply is run by a person: the deployer account's impersonation grant is on the project's operators (people), so run it with your own credentials", safetext.Field(kind.Type))
	}
	body, err := json.Marshal(impersonationFile{
		Type:                           "impersonated_service_account",
		ServiceAccountImpersonationURL: impersonationURL,
		SourceCredentials:              json.RawMessage(src),
	})
	if err != nil {
		return "", none, err
	}
	// The modes below are the POSIX half of what protects this file, and 0600
	// and 0700 are deliberately not claimed to protect it anywhere else: on
	// Windows there are no POSIX modes, Go's os.Chmod there sets the read-only
	// attribute and nothing more, so what keeps another local user out of this
	// file is the ACL %TEMP% carries and the fact that it is removed when the
	// apply ends. That is weaker than the Linux answer, and it is not a
	// weakening this branch may decide: **D12 (file modes) is open** in
	// docs/windows-cli/reconciliation.md, the Windows CLI gets its release in
	// **Slice 5**, and docs/windows-cli/README.md records that a Windows
	// release must not ship without that decision. The file is the operator's
	// refresh token wrapped for the deployer — the one secret this command
	// writes — and no code path may grow a second copy of it on Windows before
	// D12 is answered.
	dir, err := os.MkdirTemp(credentialDir, "fugaro-deployer-")
	if err != nil {
		return "", none, userErr("making the credential file's directory: %v", safetext.FieldErr(err))
	}
	remove := func() {
		// The file is removed whether or not the apply succeeded: what it
		// holds is the operator's refresh token, and the apply is the only
		// thing that needed it. A file that cannot be removed is reported
		// rather than ignored, because the operator is the only one who can
		// delete it.
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s could not be removed (%v): it holds your own credentials, wrapped for the deployer account, and nothing needs it again\n", safetext.Field(dir), safetext.FieldErr(err))
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		remove()
		return "", none, userErr("%v", safetext.FieldErr(err))
	}
	out := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(out, body, 0o600); err != nil {
		remove()
		return "", none, userErr("writing %s: %v", safetext.Field(out), safetext.FieldErr(err))
	}
	return out, remove, nil
}

// replaceEnv is parent with key set to value: the last one wins, as in exec,
// and every other entry is kept.
func replaceEnv(parent []string, key, value string) []string {
	out := make([]string, 0, len(parent)+1)
	for _, kv := range parent {
		if k, _, _ := strings.Cut(kv, "="); k == key {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+value)
}

// managedCaller is who is running this command, and the credential the IAM
// Credentials calls are made with: the operator's own, which is the identity
// the deployer account's impersonation grant is on.
//
// It is a package variable so tests never reach the network (authenticatedUser's
// own shape). The member is user:<email> because the grant D75 (i) creates is
// on var.operators, whose members are people.
var managedCaller = func(ctx context.Context, lc *localcfg.Config) (string, oauth2.TokenSource, error) {
	email, err := authenticatedUser(ctx, lc)
	if err != nil {
		if errors.Is(err, errServiceAccount) {
			return "", nil, userErr("a managed apply runs as a person: the deployer account's impersonation grant is on the project's operators, and this run's credentials are a service account's")
		}
		return "", nil, userErr("fugaro managed apply cannot tell who you are: %v", safetext.FieldErr(err))
	}
	ts, err := google.DefaultTokenSource(ctx, cloudPlatformScope)
	if err != nil {
		return "", nil, userErr("no Google credentials to assume the deployer account with: run gcloud auth application-default login (%v)", safetext.FieldErr(err))
	}
	return "user:" + email, ts, nil
}

// cloudPlatformScope is the scope the impersonation calls are made with.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// impersonationEndpoint is the IAM Credentials API's, and a variable so tests
// point it at their own server (budgetTransport's own shape).
var impersonationEndpoint = "https://iamcredentials.googleapis.com"

// impersonationTransport, when set, is the transport the impersonation calls
// go through (tests).
var impersonationTransport http.RoundTripper

// impersonationHTTPClient is the client the impersonation calls are made with:
// a timeout, and the tests' transport when one is set.
func impersonationHTTPClient() *http.Client {
	if impersonationTransport != nil {
		return &http.Client{Timeout: 30 * time.Second, Transport: impersonationTransport}
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// deployerImpersonator is the production Impersonator: it assumes the deployer
// account through the IAM Credentials API with the operator's own credential.
type deployerImpersonator struct {
	self     string
	ts       oauth2.TokenSource
	endpoint string
	hc       *http.Client
}

// Self is the ambient identity of this run, as the member IAM spells: the
// identity the deployer's impersonation grant has to name.
func (i deployerImpersonator) Self() string { return i.self }

// Impersonate mints the deployer's credential: the impersonation terraform's
// credential file names, an access token minted through it, and the account
// both are for.
//
// Where the account comes from, and what is checked: the credential terraform
// actually runs with is the impersonation file, and what decides whose tokens
// that file yields is the URL in it — Google's IAM Credentials API mints for
// the account that URL names, or refuses. So Account is read out of that URL
// (impersonationAccount), and two independent answers have to agree with it
// before a credential is returned at all: the account this call was asked for,
// and the email claim of an ID token minted through the same grant. Any
// disagreement — a proxy, a misconfigured endpoint, a bug that minted for
// another account — is a refusal, never a credential.
//
// What remains trusted, stated rather than implied: the ID token's signature
// is not verified (that needs Google's signing keys, which are fetched online;
// this command makes no call it does not need), so its claim is a cross-check
// of the endpoint's answer and not a proof; the proof is that the IAM
// Credentials API, reached over TLS at impersonationEndpoint, mints only for
// the account a URL names and only for a caller holding the grant on it. The
// access token is opaque and is not inspected: it is minted to prove the grant
// works before terraform runs, and nothing in this command hands it to a
// client (terraform mints its own through the file).
func (i deployerImpersonator) Impersonate(ctx context.Context, account string) (managedmode.Credential, error) {
	u := deployerImpersonationURL(i.endpoint, account)
	named, err := impersonationAccount(u)
	if err != nil {
		return nil, err
	}
	if named != account {
		return nil, fmt.Errorf("the impersonation URL names %s, not the deployer account %s", safetext.Field(named), safetext.Field(account))
	}
	first, err := i.accessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	// identity refuses an ID token whose email is not account, which is the
	// account the URL names (checked above): the three agree or there is no
	// credential.
	if _, err := i.identity(ctx, account); err != nil {
		return nil, err
	}
	return deployerCredential{url: u, ctx: ctx, first: first,
		mint: func(ctx context.Context) (*oauth2.Token, error) { return i.accessToken(ctx, account) }}, nil
}

// deployerImpersonationURL is the generateAccessToken URL of account at
// endpoint: the URL terraform's credential file carries.
func deployerImpersonationURL(endpoint, account string) string {
	return strings.TrimRight(endpoint, "/") + "/v1/projects/-/serviceAccounts/" + url.PathEscape(account) + ":generateAccessToken"
}

// impersonationAccount is the account an impersonation URL names: the one
// generateAccessToken mints for. A URL that is not one is an error, never an
// empty account.
func impersonationAccount(u string) (string, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("the impersonation URL %s: %w", safetext.Field(u), safetext.FieldErr(err))
	}
	const prefix = "/v1/projects/-/serviceAccounts/"
	rest, ok := strings.CutPrefix(parsed.EscapedPath(), strings.TrimRight(parsedPathPrefix(parsed), "/")+prefix)
	if !ok || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("the impersonation URL %s is not a generateAccessToken URL", safetext.Field(u))
	}
	escaped, ok := strings.CutSuffix(rest, ":generateAccessToken")
	if !ok || escaped == "" || strings.Contains(escaped, "/") {
		return "", fmt.Errorf("the impersonation URL %s is not a generateAccessToken URL", safetext.Field(u))
	}
	account, err := url.PathUnescape(escaped)
	if err != nil || account == "" || strings.ContainsAny(account, "/:") {
		return "", fmt.Errorf("the impersonation URL %s names no account", safetext.Field(u))
	}
	return account, nil
}

// parsedPathPrefix is the path the endpoint itself carries before the API's
// own path (a test server's is empty, and so is Google's).
func parsedPathPrefix(u *url.URL) string {
	p := u.EscapedPath()
	if i := strings.Index(p, "/v1/projects/-/serviceAccounts/"); i > 0 {
		return p[:i]
	}
	return ""
}

// deployerCredential is an assumed credential: the impersonation it was minted
// through, its first token, and the call that mints the next one.
type deployerCredential struct {
	url   string
	ctx   context.Context
	first *oauth2.Token
	mint  func(context.Context) (*oauth2.Token, error)
}

// Account is the account the credential's impersonation names: the account
// Google mints for through it. Impersonate refused a credential whose URL, the
// requested account and the ID token's claim did not all agree, so a URL that
// cannot be read here is not one it returned; it is read again rather than
// stored, so Account cannot drift from the file terraform is given.
func (c deployerCredential) Account() string {
	a, err := impersonationAccount(c.url)
	if err != nil {
		return ""
	}
	return a
}

// ImpersonationURL is the URL terraform's credential file carries.
func (c deployerCredential) ImpersonationURL() string { return c.url }

// Source re-mints on expiry: an impersonated access token lives at most an
// hour, and a token source handed to a client that outlives it has to be able
// to ask again rather than fail (or, worse, hand back a stale one).
func (c deployerCredential) Source() oauth2.TokenSource {
	return oauth2.ReuseTokenSource(c.first, tokenSourceFunc(func() (*oauth2.Token, error) { return c.mint(c.ctx) }))
}

// tokenSourceFunc is an oauth2.TokenSource from a function.
type tokenSourceFunc func() (*oauth2.Token, error)

func (f tokenSourceFunc) Token() (*oauth2.Token, error) { return f() }

// accessToken mints an access token for the account, with the scope terraform
// and fugaro's own clients need.
func (i deployerImpersonator) accessToken(ctx context.Context, account string) (*oauth2.Token, error) {
	body, err := json.Marshal(map[string]any{"scope": []string{cloudPlatformScope}, "lifetime": "3600s"})
	if err != nil {
		return nil, err
	}
	out, err := i.call(ctx, account, "generateAccessToken", body)
	if err != nil {
		return nil, err
	}
	var tok struct {
		AccessToken string `json:"accessToken"`
		ExpireTime  string `json:"expireTime"`
	}
	if json.Unmarshal(out, &tok) != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("assuming the deployer account %s: generateAccessToken answered without a token", safetext.Field(account))
	}
	exp, err := time.Parse(time.RFC3339, tok.ExpireTime)
	if err != nil {
		// An expiry this command cannot read is not a reason to treat the
		// token as one that never expires: it is used now and re-minted
		// before this hour is out.
		exp = time.Now().Add(time.Hour)
	}
	return &oauth2.Token{AccessToken: tok.AccessToken, Expiry: exp, TokenType: "Bearer"}, nil
}

// identity is the email the minted ID token names: the account the token was
// minted for, read from the token itself.
func (i deployerImpersonator) identity(ctx context.Context, account string) (string, error) {
	body, err := json.Marshal(map[string]any{"audience": "fugaro-managed-apply", "includeEmail": true})
	if err != nil {
		return "", err
	}
	out, err := i.call(ctx, account, "generateIdToken", body)
	if err != nil {
		return "", err
	}
	var tok struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(out, &tok) != nil || tok.Token == "" {
		return "", fmt.Errorf("assuming the deployer account %s: generateIdToken answered without a token", safetext.Field(account))
	}
	email, err := jwtEmail(tok.Token)
	if err != nil {
		return "", fmt.Errorf("assuming the deployer account %s: %w", safetext.Field(account), safetext.FieldErr(err))
	}
	if email != account {
		return "", fmt.Errorf("the credential is for %s, not for the deployer account %s: the token this assumption produced names another account", safetext.Field(email), safetext.Field(account))
	}
	return email, nil
}

// jwtEmail is the email claim of a JWT's payload, and nothing else: the
// signature is not verified here (Google signed it, and this is the account the
// token names rather than a claim anyone can make), and a payload this command
// cannot read is an error rather than an empty account.
func jwtEmail(jwt string) (string, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return "", errors.New("the identity token is not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("the identity token's payload is not base64url")
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Email == "" {
		return "", errors.New("the identity token names no email")
	}
	return claims.Email, nil
}

// call is one IAM Credentials call for the account, with the operator's own
// credential, and the JSON body it answered.
func (i deployerImpersonator) call(ctx context.Context, account, method string, body []byte) ([]byte, error) {
	if i.ts == nil {
		return nil, errors.New("assuming the deployer account: no credential to make the call with")
	}
	tok, err := i.ts.Token()
	if err != nil {
		return nil, fmt.Errorf("assuming the deployer account: fetching your own credential failed (%v): run gcloud auth application-default login", safetext.FieldErr(err))
	}
	u := strings.TrimRight(i.endpoint, "/") + "/v1/projects/-/serviceAccounts/" + url.PathEscape(account) + ":" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := i.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("assuming the deployer account %s: %w", safetext.Field(account), safetext.FieldErr(err))
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("assuming the deployer account %s: %w", safetext.Field(account), safetext.FieldErr(err))
	}
	// resp.Status is the status line the far end answered with — the reason
	// phrase included, which is the other program's own text and not a code —
	// so it is one bounded field like the body under it. The account this
	// command asked about is one as well, here as everywhere it is printed.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w: %s refused this run's credential (%s): the owner-run creating apply grants roles/iam.serviceAccountTokenCreator on %s to var.operators, so ask the owner to add you, or run this from the terminal you are an operator in",
			managedmode.ErrNoImpersonationGrant, safetext.Field(account), safetext.Field(resp.Status), safetext.Field(account))
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("assuming the deployer account %s: %s: %s", safetext.Field(account), safetext.Field(resp.Status), safetext.Field(string(out)))
	}
	return out, nil
}

// deployerAccess is the production Access: the deployer account's existence
// and its IAM policy, read from the project the marker named (D75's one-time
// setup, deployer.tf's first two steps).
type deployerAccess struct {
	svc     *iam.Service
	project string
}

func (a deployerAccess) name(account string) string {
	return "projects/" + a.project + "/serviceAccounts/" + account
}

// Exists reports whether the account is in the project. A read that fails is an
// error and never a "no": managed.Deployer turns both into a refusal, and this
// one keeps the distinction so the refusal can say which read failed.
func (a deployerAccess) Exists(ctx context.Context, account string) (bool, error) {
	_, err := a.svc.Projects.ServiceAccounts.Get(a.name(account)).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// TokenCreator reports whether member holds roles/iam.serviceAccountTokenCreator
// on account — the one grant that lets a person assume it.
//
// A binding that carries an IAM condition is NOT counted: a conditional grant
// may exclude this caller (the condition is on the resource's name, and a
// policy read cannot evaluate it here), and the direction of that mistake has
// to be "the grant is missing" rather than "the grant is there".
func (a deployerAccess) TokenCreator(ctx context.Context, account, member string) (bool, error) {
	p, err := a.svc.Projects.ServiceAccounts.GetIamPolicy(a.name(account)).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	if err != nil {
		return false, err
	}
	for _, b := range p.Bindings {
		if b.Role != tf.DeployerTokenCreatorRole || b.Condition != nil {
			continue
		}
		if slices.Contains(b.Members, member) {
			return true, nil
		}
	}
	return false, nil
}

// managedBudgetGate makes the apply-time budget read and turns it into the gate
// the apply runs behind (Task 4.4). It is the one place the managed apply — and
// a restore, which applies through this command's own path (Task 5.2) — opens
// that gate, and every way of not answering with an amount leaves it closed.
//
// The read is internal/infra/managed.ReadBudget and never IsManaged alone: what
// the gate needs is the amount the creation fact records, and ReadBudget is the
// read that proves the bucket is the project's own before it answers with one.
// It is made here rather than in internal/managed because the storage and
// Resource Manager clients live on this side of the boundary, which is the
// split Fact already follows.
//
// The second read below is the one thing ReadBudget cannot answer on its own.
// When it answers with no amount and no error, the object is either a managed
// creation that recorded none (the marker's version 1) or not a managed
// creation at all, and its own one-line reason does not say which; IsManaged is
// the same read over the same object asking the other question, and it is made
// only in that case, and only so the refusal names the right one of the two.
// Nothing is applied either way.
func managedBudgetGate(ctx context.Context, c *infra.Clients, lc *localcfg.Config) (managedmode.Enable, managedmode.Fact, error) {
	var s *storage.Service
	var r *crm.Service
	if c != nil {
		s, r = c.Storage, c.CRM
	}
	return managedBudgetGateOf(ctx, s, r, lc.Name, lc.GCPProject)
}

// managedBudgetGateOf is managedBudgetGate for a named GCP project rather than
// the selected config's: `fugaro restore` asks it of the NEW project right
// after the creation, so a creation that wrote no valid version-2 marker stops
// before the checkout line moves. One function, so the restore's check and
// the apply's gate are the same read and the same decision.
func managedBudgetGateOf(ctx context.Context, s *storage.Service, r *crm.Service, name, gcpProject string) (managedmode.Enable, managedmode.Fact, error) {
	answer, err := managed.ReadBudgetAnswer(ctx, s, r, name, gcpProject)
	read := managedmode.BudgetRead{
		Project: name, Amount: answer.Budget.Amount, Currency: answer.Budget.Currency,
		OK: answer.OK, Reason: answer.Reason, Err: err, Invalid: answer.Invalid,
	}
	if !answer.OK && !answer.Invalid && err == nil {
		isManaged, managedReason, managedErr := managed.IsManaged(ctx, s, r, name, gcpProject)
		read.Managed, read.ManagedReason = isManaged, managedReason
		if managedErr != nil {
			// A read that answered once and failed the second time is believed
			// neither way; the refusal then says the read could not be made.
			read.Err = managedErr
		}
	}
	gate, err := managedmode.ApplyGate(read)
	if err != nil {
		// The fact is not passed on: the command ends here.
		return managedmode.Enable{}, managedmode.Fact{}, err
	}
	// The gate is open, so the creation fact was read and believed — ReadBudget
	// answers OK only for a marker readFact accepted — and the confirmation
	// rule of D55 gets that fact rather than a second read of it.
	return gate, managedmode.Fact{Managed: true}, nil
}

// managedListErr is the owner's object that could not be read, in the words the
// owner can act on: the read is fail-closed, so an object this command cannot
// read is no approval at all and every delete is refused.
func managedListErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was applied: the delete list of %s could not be read, and an unreadable list approves nothing: %s", safetext.Field(lc.Name), safetext.Field(err.Error()))
	case isAccessDenied(err):
		return userErr("nothing was applied: gs://%s/%s could not be read with this run's credentials: %s", safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(err.Error()))
	default:
		return remote(fmt.Errorf("reading gs://%s/%s for %s: %w", safetext.Field(bucket), managed.AllowDeleteObject, safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}

// managedApplyErr maps the apply's own refusals to what the reader can do about
// them: everything the gate, the marker, the owner's list and the deployer
// account refuse is a user error (exit 1), and a cloud or terraform failure is
// remote (exit 2).
func managedApplyErr(lc *localcfg.Config, root string, res managedmode.Result, err error) error {
	var refused *managedmode.RefusedError
	var ee *tf.ExitError
	switch {
	case errors.Is(err, managedmode.ErrNotEnabled):
		// The gate, again: the check at the top of this command is the same
		// value, and this is the apply's own refusal of it.
		return userErr("nothing was applied: %v", err)
	case errors.As(err, &refused):
		return userErr("nothing was applied: the gate refused the plan of %s, and a refused plan is not applied — the plan carries a delete or a replacement the owner has not approved, or a resource outside the managed project's allowlist:\n%v", safetext.Field(root), err)
	case errors.Is(err, managedmode.ErrNotManaged):
		return userErr("nothing was applied: %v", err)
	case errors.Is(err, managedmode.ErrSessionAddresses):
		return userErr("nothing was applied: %v", err)
	case errors.Is(err, managedmode.ErrPreflight):
		return userErr("nothing was applied: %v", err)
	case errors.Is(err, managedmode.ErrPlanChanged):
		return userErr("nothing was applied: %v", err)
	case errors.Is(err, managedmode.ErrNotConfirmed):
		// D55's rule, refusing the session rather than the plan: exit 1,
		// because what to do about it is the person's (a terminal) and not a
		// remote failure's.
		return userErr("nothing was applied: %v", err)
	case errors.Is(err, managedmode.ErrProviderSetting):
		return userErr("nothing was applied: %v", err)
	case errors.Is(err, managedmode.ErrNoDeployerAccount):
		return userErr("nothing was applied: %v", err)
	case errors.Is(err, managedmode.ErrNoImpersonationGrant):
		return userErr("nothing was applied: %v", err)
	case errors.Is(err, managedmode.ErrImpersonationFailed):
		return remote(err)
	case errors.Is(err, managedmode.ErrRequest):
		return userErr("nothing was applied: %v", err)
	case errors.As(err, &ee):
		return remote(err)
	default:
		return remote(fmt.Errorf("applying the root of %s for %s: %w", safetext.Field(root), safetext.Field(lc.Name), safetext.FieldErr(err)))
	}
}
