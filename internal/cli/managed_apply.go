package cli

import (
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
	iam "google.golang.org/api/iam/v1"

	"github.com/dimipaun/fugaro/internal/infra"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
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
// And it is INERT in this release: managedmode.Shipped is closed, and the gate
// is the first thing this command reads — before a project is selected, before
// a bucket is read and before terraform runs — so what this release ships is a
// command that refuses and does nothing else. Task 4.4 is the task that opens
// it, in internal/managed/enable.go and nowhere else.

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
	cmd.AddCommand(newManagedApplyCmd())
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
of the four is a boundary against a session that goes around this program.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return managedApply(cmd, o, args[0])
		},
	}
	addCloudFlags(cmd, &o)
	return cmd
}

// managedApply is newManagedApplyCmd's body. The order is the whole of the
// design: the config gate, then the local checks, then the reads, then the
// apply — and every refusal before the apply is a hard return.
func managedApply(cmd *cobra.Command, o cloudOptions, rootArg string) error {
	ctx := cmd.Context()
	// A credential leak first, before anything opens the cloud: GODEBUG is
	// read before main runs, so it cannot be unset in time (refuseHTTP2Debug).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	// And then the config gate, before the project is selected and before a
	// single cloud call: in this release the managed apply is refused by
	// config, and a refused command must not read a bucket, assume an account
	// or run terraform on its way to the refusal. The same value is handed to
	// the apply below, which refuses again (managed.Apply's own first step):
	// two checks of one value, so neither can be left out.
	if err := managedmode.Shipped().Refuse(); err != nil {
		return userErr("nothing was applied: %v", err)
	}
	_, lc, err := selectProject(ctx, o)
	if err != nil {
		return err
	}
	for _, w := range lc.Warnings() {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro: warning: %s\n", w)
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
		return userErr("project %s names the runs bucket %q, and a managed project's marker and delete list live in %s: a managed apply reads the objects of the project's own runs bucket, so check runs_bucket in the project config", lc.Name, got, bucket)
	}
	// The project number is what says a bucket name is this project's (a
	// bucket name is global), and both reads below prove it with.
	number, err := infra.ProjectNumber(ctx, c, lc.GCPProject)
	if err != nil {
		return remote(err)
	}
	// The creation fact: whether this project is managed at all, and the one
	// line that says why when it is not. The read is fail-closed, and the
	// apply refuses an unreadable marker or a reason nobody can read.
	isManaged, reason, err := managed.IsManaged(ctx, c.Storage, c.CRM, lc.Name, lc.GCPProject)
	fact := managedmode.Fact{Managed: isManaged, Reason: reason, Err: err}
	if fact.Err != nil {
		return userErr("nothing was applied: the creation fact of %s could not be read, and an unreadable marker is not an apply: %v", lc.Name, fact.Err)
	}
	if !fact.Managed {
		return userErr("nothing was applied: %s is not managed (%s), so its plans keep the coding agent's refusal and are applied by fugaro init's own path with its confirmation", lc.Name, reason)
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
		return userErr("the root %s: %v", rootArg, err)
	}
	cache, err := infra.PluginCache(os.Getenv)
	if err != nil {
		return userErr("%v", err)
	}
	bin, err := exec.LookPath("terraform")
	if err != nil {
		return userErr("fugaro managed apply needs terraform (>= 1.7, < 2) on PATH: %v", err)
	}
	imp := deployerImpersonator{self: self, ts: ts, endpoint: impersonationEndpoint, hc: impersonationHTTPClient()}
	res, err := managedmode.Apply(ctx, managedmode.Request{
		// The gate as this release ships it: closed. The check above is the
		// same value, and this one is what makes the apply itself refuse
		// however it is called.
		Enable:  managedmode.Shipped(),
		Project: lc.Name, GCPProject: lc.GCPProject,
		Fact:    fact,
		Workdir: wd,
		// Terraform is built inside the deployer's assumption, around the
		// credential it was handed: the plan is read from this root's own
		// state bucket (D77), which the deployer holds and the operator's
		// identity need not, so init, plan, the show the gate reads and the
		// apply are one identity's.
		Terraform: func(cred managedmode.Credential) (*tf.TF, error) {
			return managedTerraform(cmd, lc, wd, bin, cache, imp, cred)
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
		fmt.Fprintf(w, "no changes: the plan of %s is empty, so nothing was gated and nothing was applied\n", rootArg)
		return nil
	}
	fmt.Fprintf(w, "applied the saved plan of %s as %s: the gate admitted every change in it, and the owner's list approved every delete and replacement it carries\n",
		rootArg, managedmode.DeployerAccount(lc.GCPProject))
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
// here: a person at a terminal confirms a YesOrTyped step by having one, and a
// session with no marker and no terminal has no confirmation to give. That is
// the fail-closed direction, and the refusal a person then reads is
// initflow's own NoTerminalAdvice.
func managedConditions(cmd *cobra.Command) initflow.Conditions {
	return initflow.Conditions{Terminal: stdinIsTerminal(cmd.InOrStdin()), Agent: agentMarker(os.Getenv)}
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
		return "", userErr("the root %s: %v", rootArg, err)
	}
	st, err := os.Stat(abs)
	switch {
	case err != nil:
		return "", userErr("the root %s: %v", rootArg, err)
	case !st.IsDir():
		return "", userErr("the root %s is not a directory", rootArg)
	}
	co, err := checkoutProject(ctx, abs)
	if err != nil {
		return "", err
	}
	if co == nil {
		return "", userErr("the root %s is in no checkout with a fugaro.yaml, and a managed apply runs the root of the project repository (D75): run it on a directory of the checkout whose fugaro.yaml names %s", rootArg, lc.Name)
	}
	if co.Project != lc.Name {
		return "", userErr("the root %s is in the checkout of project %s, and this command is acting on %s: a managed apply runs the root of the project it applies for", rootArg, co.Project, lc.Name)
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
//     review screen, so the members it lists are the config's own launchers and
//     operators — the people this installation's config says may act on the
//     project, and the same list the review screen shows (init_review's
//     accessLists). A member outside it is refused.
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
	c.Listed = slices.Concat(lc.Terraform.Launchers, lc.Terraform.Operators)
	return tf.ManagedCover{Cover: c}
}

// managedTerraform builds the terraform the apply runs, around the credential
// the deployer's assumption produced.
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
// The file is removed when the apply returns: it holds the operator's own
// refresh token (wrapped, not copied: it is the source_credentials of an
// impersonated credential, and the impersonation URL names the deployer), and
// it has no business outliving the apply that needed it.
func managedTerraform(cmd *cobra.Command, lc *localcfg.Config, wd *infra.Workdir, bin, cache string, imp deployerImpersonator, cred managedmode.Credential) (*tf.TF, error) {
	account := cred.Account()
	if account != managedmode.DeployerAccount(lc.GCPProject) {
		// The credential is checked against the deployer account by
		// managed.Deployer before this runs; this is the second check, in the
		// one place that would otherwise write another account's credential
		// file.
		return nil, fmt.Errorf("the credential is for %s, not for the deployer account of %s", account, lc.GCPProject)
	}
	file, cleanup, err := writeImpersonationFile(wd.Dir, account, imp.endpoint)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	parent := replaceEnv(os.Environ(), "GOOGLE_APPLICATION_CREDENTIALS", file)
	env, err := managedmode.TerraformEnv(parent, wd.Dir, cache)
	if err != nil {
		return nil, userErr("%v", err)
	}
	t, err := tf.New(bin, wd.Root, env)
	if err != nil {
		var ee *tf.ExitError
		if errors.As(err, &ee) {
			return nil, remote(err)
		}
		return nil, userErr("%v", err)
	}
	t.Out = cmd.ErrOrStderr()
	return t, nil
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

// writeImpersonationFile writes that file into dir (this command's workdir,
// mode 0600) and returns its path and the function that removes it.
//
// What it refuses: an operator whose credential is not a person's. The
// impersonation grant of D75 is on var.operators, which are user members, and
// the identity this command reports (managedCaller) is a person's — so a
// service account's credential, or no credential at all, is a refusal here
// rather than a file whose source is something else.
func writeImpersonationFile(dir, account, endpoint string) (string, func(), error) {
	none := func() {}
	path, err := adcFile()
	if err != nil {
		return "", none, err
	}
	src, err := readBounded(path)
	if err != nil {
		return "", none, userErr("reading your Application Default Credentials: %v (run gcloud auth application-default login)", err)
	}
	var kind struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(src, &kind) != nil || kind.Type == "" {
		return "", none, userErr("your Application Default Credentials (%s) are not a credential file this command reads: run gcloud auth application-default login", path)
	}
	if kind.Type != "authorized_user" {
		return "", none, userErr("your Application Default Credentials are a %s credential, and a managed apply is run by a person: the deployer account's impersonation grant is on the project's operators (people), so run it with your own credentials", kind.Type)
	}
	body, err := json.Marshal(impersonationFile{
		Type:                           "impersonated_service_account",
		ServiceAccountImpersonationURL: strings.TrimRight(endpoint, "/") + "/v1/projects/-/serviceAccounts/" + url.PathEscape(account) + ":generateAccessToken",
		SourceCredentials:              json.RawMessage(src),
	})
	if err != nil {
		return "", none, err
	}
	// A directory of this command's own, and a name of its own inside it: the
	// workdir already holds terraform's CLI config and data directory.
	out := filepath.Join(dir, "fugaro_deployer_credentials.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", none, userErr("%v", err)
	}
	if err := os.WriteFile(out, body, 0o600); err != nil {
		return "", none, userErr("writing %s: %v", out, err)
	}
	// The file is removed whether or not the apply succeeded: what it holds is
	// the operator's refresh token, and the apply is the only thing that
	// needed it. A file that cannot be removed is reported rather than
	// ignored, because the operator is the only one who can delete it.
	return out, func() {
		if err := os.Remove(out); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: %s could not be removed (%v): it holds your own credentials, wrapped for the deployer account, and nothing needs it again\n", out, err)
		}
	}, nil
}

// adcFile is the file Application Default Credentials come from: what
// GOOGLE_APPLICATION_CREDENTIALS names, else gcloud's well-known one.
// (init_project.go's adcInfo reads the same file for its type; this is the
// path, which that one does not return.)
func adcFile() (string, error) {
	if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		return p, nil
	}
	dir := os.Getenv("CLOUDSDK_CONFIG")
	if dir == "" {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			dir = filepath.Join(x, "gcloud")
		} else if h, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(h, ".config", "gcloud")
		}
	}
	if dir == "" {
		return "", userErr("no Application Default Credentials: GOOGLE_APPLICATION_CREDENTIALS is unset and there is no home directory to look in (run gcloud auth application-default login)")
	}
	return filepath.Join(dir, "application_default_credentials.json"), nil
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
		return "", nil, userErr("fugaro managed apply cannot tell who you are: %v", err)
	}
	ts, err := google.DefaultTokenSource(ctx, cloudPlatformScope)
	if err != nil {
		return "", nil, userErr("no Google credentials to assume the deployer account with: run gcloud auth application-default login (%v)", err)
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

// Impersonate mints the deployer's credential: an access token for the apply's
// own API clients, and the identity that token is for.
//
// Why an ID token is minted as well, and why Account is not simply the account
// this call was made for: the access token is opaque, so an Account taken from
// the request would be this code asserting its own identity, and
// managed.Deployer's check — that the credential really is the deployer's —
// could not fail. The ID token is a JWT Google signs for the account the token
// was minted FOR, and its email claim is what Account returns: the account is
// read out of a token rather than out of this function's own argument, so a
// proxy, a misconfigured endpoint or a bug that minted for another account is
// refused by Deployer rather than applied with. Both calls are the same grant
// (roles/iam.serviceAccountTokenCreator includes iam.serviceAccounts.getOpenIdToken).
func (i deployerImpersonator) Impersonate(ctx context.Context, account string) (managedmode.Credential, error) {
	first, err := i.accessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	email, err := i.identity(ctx, account)
	if err != nil {
		return nil, err
	}
	return deployerCredential{account: email, ctx: ctx, first: first,
		mint: func(ctx context.Context) (*oauth2.Token, error) { return i.accessToken(ctx, account) }}, nil
}

// deployerCredential is an assumed credential: the account a token names, the
// token itself, and the call that mints the next one.
type deployerCredential struct {
	account string
	ctx     context.Context
	first   *oauth2.Token
	mint    func(context.Context) (*oauth2.Token, error)
}

func (c deployerCredential) Account() string { return c.account }

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
		return nil, fmt.Errorf("assuming the deployer account %s: generateAccessToken answered without a token", account)
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
		return "", fmt.Errorf("assuming the deployer account %s: generateIdToken answered without a token", account)
	}
	email, err := jwtEmail(tok.Token)
	if err != nil {
		return "", fmt.Errorf("assuming the deployer account %s: %w", account, err)
	}
	if email != account {
		return "", fmt.Errorf("the credential is for %s, not for the deployer account %s: the token this assumption produced names another account", email, account)
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
		return nil, fmt.Errorf("assuming the deployer account: fetching your own credential failed (%v): run gcloud auth application-default login", err)
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
		return nil, fmt.Errorf("assuming the deployer account %s: %w", account, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("assuming the deployer account %s: %w", account, err)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w: %s refused this run's credential (%s): the owner-run creating apply grants roles/iam.serviceAccountTokenCreator on %s to var.operators, so ask the owner to add you, or run this from the terminal you are an operator in",
			managedmode.ErrNoImpersonationGrant, account, resp.Status, account)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("assuming the deployer account %s: %s: %s", account, resp.Status, oneLineCLI(string(out)))
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

// managedListErr is the owner's object that could not be read, in the words the
// owner can act on: the read is fail-closed, so an object this command cannot
// read is no approval at all and every delete is refused.
func managedListErr(lc *localcfg.Config, bucket string, err error) error {
	switch {
	case errors.Is(err, managed.ErrRefused):
		return userErr("nothing was applied: the delete list of %s could not be read, and an unreadable list approves nothing: %v", lc.Name, err)
	case isAccessDenied(err):
		return userErr("nothing was applied: gs://%s/%s could not be read with this run's credentials: %v", bucket, managed.AllowDeleteObject, err)
	default:
		return remote(fmt.Errorf("reading gs://%s/%s for %s: %w", bucket, managed.AllowDeleteObject, lc.Name, err))
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
		return userErr("nothing was applied: the gate refused the plan of %s, and a refused plan is not applied — the plan carries a delete or a replacement the owner has not approved, or a resource outside the managed project's allowlist:\n%v", root, err)
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
		return remote(fmt.Errorf("applying the root of %s for %s: %w", root, lc.Name, err))
	}
}
