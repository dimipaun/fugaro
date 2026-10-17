// The managed apply: the one path that applies a saved plan in a managed
// project, and the three things that replace the coding-agent refusal D55
// removes there (docs/design/managed-projects.md, D55 and D76).
//
// What it is: the agent-authored root is copied into this command's own
// workdir, the backend is set by the command itself (D77's state bucket, never
// one the configuration chose), the root is planned into a saved file, the
// file is gated (Tasks 1.0-1.3) with the addresses the owner's own object
// approves (Task 1.4, D72/D74), and the saved file is applied as the D75
// deployer account (Task 1.5) rather than as the operator.
//
// What it is not, stated rather than implied, because this is the change the
// design's §6 item 4 is about: none of the three replacements is a boundary
// against an agent that goes around the program. The agent shares the
// container and the user with the runner, reads the runner's environment
// through /proc and the job's service account through the metadata server
// (SECURITY.md), so it can apply directly, write the marker with the
// operator's own credentials, or unset its marker and run this command
// itself. What the gate, the delete list and the marker stop is the accident:
// a session that goes through this program applies only what the owner's
// rules admit.
//
// And it is inert until phase 4 lands: every path below is behind
// Request.Enable, whose zero value is closed (enable.go), because the
// whole-project inventory and the budget are not in this release yet. Task 4.4
// is the task that opens it.
package managed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The refusals this file's own rules make. They are values as well as
// messages, so a caller can match on them without reading English.
var (
	// ErrNotManaged is the creation fact's answer: a project the marker read
	// did not say is managed is applied by nobody's session, and this command
	// applies nothing for it.
	ErrNotManaged = errors.New("the project is not managed")
	// ErrSessionAddresses is an address the applying command was given on its
	// own command line: refused by name rather than honoured, whatever the
	// owner's object says (D72/D74).
	ErrSessionAddresses = errors.New("the applying command named addresses of its own")
	// ErrRefused is the gate's refusal: the plan is not applied, and the only
	// action a false verdict permits is a confirmation this session cannot
	// give.
	ErrRefused = errors.New("the managed gate refused the plan")
	// ErrPreflight is the root's own configuration: a file this command
	// cannot read as HCL, or a block whose whole point is to act outside the
	// plan (a backend, a cloud block, provider_meta, an ephemeral or an
	// action block).
	ErrPreflight = errors.New("the root's configuration is not one a managed apply runs")
	// ErrProviderSetting is a provider setting that reached terraform's
	// environment. tf.Env's allowlist drops every one of them, and this is the
	// assertion that says so at run time rather than only in a test.
	ErrProviderSetting = errors.New("a provider setting is in terraform's environment")
	// ErrPlanChanged is the saved plan file not being the bytes the gate
	// admitted (Verdict's own contract): the gate's verdict is about a plan,
	// not about a file name, so the bytes are hashed at gate time and again
	// before the apply.
	ErrPlanChanged = errors.New("the saved plan changed between the gate and the apply")
	// ErrRequest is a Request this command cannot run at all: no workdir, no
	// terraform, no gate inputs.
	ErrRequest = errors.New("the managed apply has nothing to run")
)

// Fact is the creation fact's read, as infra/managed.IsManaged answered it:
// whether the project is managed, the one line it gave for its answer, and the
// error when the read could not be made at all.
//
// It is passed in rather than read here because the read needs the storage and
// Resource Manager clients, which internal/cli holds and this package does not
// (the same split statebucket.go's StateStore follows). The zero value is "not
// managed", which is the direction every mistake has to fall in.
type Fact struct {
	// Managed is IsManaged's first result: true only with a nil error.
	Managed bool
	// Reason is IsManaged's one-line reason, empty when Managed.
	Reason string
	// Err is IsManaged's error, when the read could not be made.
	Err error
}

// Request is everything a managed apply is told. Every field is set by the
// caller that holds the cloud clients (internal/cli's fugaro managed apply);
// nothing here is read from a flag the applying session could choose.
type Request struct {
	// Enable is the config gate of enable.go, and the FIRST thing Apply reads.
	Enable Enable
	// Project is the managed Fugaro project's name, GCPProject its GCP
	// project. The state bucket is derived from GCPProject and from nothing
	// else (managed.ManagedStateBucketName).
	Project, GCPProject string
	// Fact is the marker read's answer.
	Fact Fact
	// Workdir is the workdir terraform runs in: Dir holds terraform's CLI
	// config, Root the copy of the agent-authored root. It is this command's
	// own (infra.ManagedWorkdir), never the checkout.
	Workdir *infra.Workdir
	// TF runs terraform in Workdir.Root, with the environment TerraformEnv
	// built (tf.Env's allowlist, and the assertion over it).
	TF *tf.TF
	// Cover is the managed instantiation of the scope-and-attribute gate: the
	// managed project's own allowlist (Task 1.1), with the projects, region
	// and buckets of this installation.
	Cover Cover
	// AllowDelete are the addresses the owner's own object approves, read
	// fail-closed by the caller (infra/managed.GuardAllowList). An empty list
	// allows no delete and no replace at all, which is what the gate then
	// refuses.
	AllowDelete []string
	// Session is what the applying command's OWN command line named as
	// addresses it may delete or replace. fugaro managed apply registers no
	// such flag, so the production value is nil, and internal/cli asserts that
	// it is (a command-line flag of this command is refused rather than
	// ignored) — the field exists because the rule is enforced by the one
	// function that runs an apply rather than left to each caller, and
	// anything here is refused by name whatever the owner's object says.
	Session []string
	// Imports are the import blocks the root's discovery wrote: an import is
	// admitted only when its address and ID are exactly one of them.
	Imports []tf.ImportKey
	// Access and Impersonator are D75's resolution: whether the deployer
	// account is there, whether this caller may assume it, and the assumption
	// itself. A nil one is refused rather than treated as nothing to check.
	Access       Access
	Impersonator Impersonator
	// Out receives terraform's progress output and this command's own lines.
	// Nil discards them.
	Out io.Writer
}

// Result is what an apply did, for the caller that prints it. Changed is false
// when the plan was empty, in which case nothing was gated and nothing was
// applied.
type Result struct {
	Changed  bool
	PlanPath string
	PlanHash string
	Verdict  Verdict
}

// RefusedError is the gate's refusal, carried out of Apply with the verdict
// that made it: every refusal, each with the gate that made it, the address it
// is about and the rule's own words. Nothing is applied when this is returned.
type RefusedError struct {
	Verdict Verdict
}

func (e *RefusedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, so nothing is applied (%d refusal(s))", ErrRefused, len(e.Verdict.Refusals))
	for _, r := range e.Verdict.Refusals {
		b.WriteString("\n  ")
		b.WriteString(r.Gate)
		b.WriteString(": ")
		if r.Address != "" {
			b.WriteString(r.Address)
			b.WriteString(": ")
		}
		b.WriteString(r.Why)
	}
	if len(e.Verdict.Refusals) == 0 {
		b.WriteString(": the verdict refused and listed nothing, which is a refusal all the same")
	}
	return b.String()
}

func (e *RefusedError) Unwrap() error { return ErrRefused }

// Apply is the managed apply: the gate's verdict is the whole of the decision,
// and every refusal below returns before anything is applied.
//
// The order is the plan's own, and each step is before the one that trusts it:
//
//  1. the config gate (enable.go) — closed refuses before anything is read;
//  2. the applying command's own command line — an address there is refused;
//  3. the creation fact — an unreadable marker is not an apply;
//  4. the root's own files — a backend, cloud, provider_meta, ephemeral or
//     action block is refused BEFORE terraform init (the plan cannot show
//     them: PR #483's required residual);
//  5. the backend and the init — D77's bucket, written by this command;
//  6. the plan, into a saved file;
//  7. the plan's bytes hashed, then the gate;
//  8. the deployer assumed (D75), the hash re-checked, and the saved file
//     applied. Nothing runs outside that assumption.
func Apply(ctx context.Context, r Request) (Result, error) {
	if err := r.Enable.Refuse(); err != nil {
		return Result{}, err
	}
	if len(r.Session) > 0 {
		return Result{}, r.refuseSession()
	}
	if err := r.fact(); err != nil {
		return Result{}, err
	}
	if err := r.ready(); err != nil {
		return Result{}, err
	}
	if err := r.preflight(); err != nil {
		return Result{}, err
	}
	// The backend is this command's, not the configuration's: WriteBackend
	// writes backend.hcl for anyone running terraform here by hand and returns
	// the same settings for init, and WriteBackendBlock writes the one backend
	// block terraform reads. Both are refused by the preflight above if the
	// root carries one of its own, so there is exactly one.
	settings, err := r.Workdir.WriteBackend(r.stateBucket(), infra.StatePrefixManaged)
	if err != nil {
		return Result{}, fmt.Errorf("the state backend of %s: %w", r.what(), err)
	}
	if err := r.Workdir.WriteBackendBlock(); err != nil {
		return Result{}, fmt.Errorf("the state backend of %s: %w", r.what(), err)
	}
	if err := r.writeVars(); err != nil {
		return Result{}, fmt.Errorf("the variables file of %s: %w", r.what(), err)
	}
	if err := r.TF.Init(ctx, settings); err != nil {
		return Result{}, fmt.Errorf("initializing the root of %s: %w", r.what(), err)
	}
	planPath := filepath.Join(r.Workdir.Root, infra.PlanFile)
	changed, err := r.TF.Plan(ctx, planPath)
	if err != nil {
		return Result{}, fmt.Errorf("planning the root of %s: %w", r.what(), err)
	}
	if !changed {
		// Nothing to change: there is no plan to gate and none to apply, and
		// an empty plan is not a verdict.
		return Result{Changed: false}, nil
	}
	return r.gateAndApply(ctx, planPath)
}

// refuseSession is D72/D74's refusal, in the words infra/managed.GuardAllowList
// uses: it names the addresses and the owner's object, and it names no flag at
// all — a refusal that named one would be read by the session it refuses as a
// thing to pass.
func (r Request) refuseSession() error {
	named := slices.Clone(r.Session)
	slices.Sort(named)
	return fmt.Errorf("%w: refusing the addresses this command's own command line named (%s): in a managed project the addresses a run may delete or replace come from the owner's own list at gs://%s/%s, written with the operator's credentials by fugaro allow-delete, never from the arguments of the command that applies the plan; ask the owner to approve %s",
		ErrSessionAddresses, strings.Join(named, ", "), r.runsBucket(), "fugaro/allow-delete.json", strings.Join(named, ", "))
}

// fact is the creation fact's own rule: this command applies only for a
// project the read answered managed with no error, and the refusal always
// carries a line a person can read. A read that answered nothing — no reason
// and no managed — is refused rather than read as an answer, and an error is
// never a reason to proceed (Global Constraint 3, fail closed).
func (r Request) fact() error {
	switch {
	case r.Fact.Err != nil:
		return fmt.Errorf("%w: reading the creation fact of %s: %w", ErrNotManaged, r.what(), r.Fact.Err)
	case r.Fact.Managed:
		return nil
	case oneLine(r.Fact.Reason) == "":
		return fmt.Errorf("%w: %s is not managed, and the read gave no reason a person could read, so nothing is applied: a read that answered nothing is not an answer", ErrNotManaged, r.what())
	default:
		return fmt.Errorf("%w: %s (%s), so nothing is applied here: the coding agent's refusal is removed only where the creation fact says the project is managed", ErrNotManaged, r.what(), oneLine(r.Fact.Reason))
	}
}

// ready is what an apply needs before it can read anything: a workdir of this
// command's own and a terraform running in it.
func (r Request) ready() error {
	switch {
	case r.Workdir == nil || r.Workdir.Dir == "" || r.Workdir.Root == "":
		return fmt.Errorf("%w: no workdir, so there is no root to plan and nothing to apply", ErrRequest)
	case r.TF == nil:
		return fmt.Errorf("%w: no terraform, so the root cannot be planned", ErrRequest)
	case r.Cover == nil:
		return fmt.Errorf("%w: no cover, and a managed plan is admitted by the managed cover of %s or by nothing", ErrRequest, r.what())
	case r.Access == nil || r.Impersonator == nil:
		return fmt.Errorf("%w: nothing resolves the deployer account of %s, and an apply that cannot run as it is refused rather than run as the operator", ErrRequest, r.what())
	}
	return nil
}

// stateBucket is D77's bucket: a function of the GCP project alone, so no
// argument, flag or configuration can point the managed state at another
// bucket (statebucket.go's rule).
func (r Request) stateBucket() string { return ManagedStateBucketName(r.GCPProject) }

// writeVars makes sure the root has a terraform.tfvars.json, because the plan
// this apply runs passes -var-file=terraform.tfvars.json (tf.Plan's own
// sequence) and terraform fails when the file is not there. A root that wrote
// its own variables file keeps it byte for byte — it is the agent's
// configuration, and what its values do is in the plan the gate reads — and one
// that wrote none gets the empty object, which is what an agent-authored root
// with no variables needs.
func (r Request) writeVars() error {
	path := filepath.Join(r.Workdir.Root, infra.VarsFile)
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return os.WriteFile(path, []byte("{}\n"), 0o600)
}

// runsBucket is the name the marker and the delete list live under, the one
// infra/managed reads (its runsBucketName): the default, never the config's
// runs_bucket, because a managed object in another bucket is read by nobody.
func (r Request) runsBucket() string { return "fugaro-runs-" + r.GCPProject }

// what names the project in this file's refusals.
func (r Request) what() string {
	switch {
	case r.Project != "" && r.GCPProject != "":
		return r.Project + " (" + r.GCPProject + ")"
	case r.Project != "":
		return r.Project
	case r.GCPProject != "":
		return r.GCPProject
	}
	return "this project"
}

// spec is the gate's own input: the managed cover, the owner's addresses, the
// discovery's imports and the terraform the saved plan is read with.
func (r Request) spec() Spec {
	return Spec{Project: r.Project, GCPProject: r.GCPProject, Cover: r.Cover,
		AllowDelete: r.AllowDelete, Imports: r.Imports, TF: r.TF}
}

// gateAndApply is the second half of Apply: the hash, the gate, the deployer
// and the apply, in that order.
func (r Request) gateAndApply(ctx context.Context, planPath string) (Result, error) {
	before, err := planHash(planPath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: the saved plan of %s: %w", ErrRequest, r.what(), err)
	}
	v, err := Gate(ctx, planPath, r.spec())
	if err != nil {
		// A plan the gate cannot read is an error and never a verdict, and the
		// zero Verdict it returns is not OK either.
		return Result{}, fmt.Errorf("gating the plan of %s: %w", r.what(), err)
	}
	res := Result{Changed: true, PlanPath: planPath, PlanHash: before, Verdict: v}
	if !v.OK {
		// The refusal is a hard return: there is no "apply anyway" here, and
		// the only action a false verdict permits is a confirmation, which in
		// the session a managed apply runs in means the step does not proceed.
		return res, &RefusedError{Verdict: v}
	}
	err = Deployer(ctx, r.GCPProject, r.Access, r.Impersonator, func(ctx context.Context, cred Credential) error {
		// The verdict is about a plan and not about a file name (Verdict's own
		// contract), so the bytes are hashed again here, inside the assumption
		// and immediately before the apply.
		now, err := planHash(planPath)
		if err != nil {
			return fmt.Errorf("%w: the saved plan of %s: %w", ErrPlanChanged, r.what(), err)
		}
		if now != before {
			return fmt.Errorf("%w: the file the gate admitted is not the file that would be applied (%s then, %s now), so nothing is applied: gate and apply must be the same bytes",
				ErrPlanChanged, before, now)
		}
		if r.Out != nil {
			fmt.Fprintf(r.Out, "applying the saved plan of %s as %s\n", r.what(), cred.Account())
		}
		return r.TF.Apply(ctx, planPath)
	})
	if err != nil {
		return res, err
	}
	return res, nil
}

// planHash is the saved plan's own bytes, hex-encoded: what the gate admitted
// and what the apply must find again.
func planHash(planPath string) (string, error) {
	f, err := os.Open(planPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// oneLine is a reason read as one line: the reason comes from an API answer or
// from an object someone else may have written, so a newline or a control
// character in it must not become a line of this program's own output.
// (infra/managed's read already does this; this is the boundary for any caller
// that hands Fact over directly.)
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)), " ")
}

// TerraformEnv is the environment a managed apply runs terraform with: tf.Env's
// allowlist, and the assertion over it that the plan's own provider settings
// are not in the environment either.
//
// Why the assertion exists as well as the allowlist (PR #483's required
// residual (c)): the applier's shell is the operator's, and a GOOGLE_* variable
// in it would decide who applies, who is billed or where the requests go —
// exactly the settings the managed cover refuses in a provider block
// (managedProviderAllowed). tf.Env drops everything not on its allowlist, so
// this cannot fire today; it is here so that the day the allowlist grows one,
// the apply refuses rather than runs with it. The names are the cover's own
// list: the credential variables, the billing project, the project/region/zone
// defaults a provider would otherwise fall back to, the impersonation, and
// every *_CUSTOM_ENDPOINT (which would send the applier's bearer token to
// another host).
func TerraformEnv(parent []string, workdir, cache string) ([]string, error) {
	env, err := tf.Env(parent, workdir, cache)
	if err != nil {
		return nil, err
	}
	if k := providerSettingIn(env); k != "" {
		return nil, fmt.Errorf("%w: %s is set in terraform's environment, and a managed apply runs with the applier's own ambient credentials alone: unset it", ErrProviderSetting, k)
	}
	return env, nil
}

// providerSettings are the variables TerraformEnv refuses by name, beside
// every *_CUSTOM_ENDPOINT.
var providerSettings = []string{
	"GOOGLE_CREDENTIALS", "GOOGLE_OAUTH_ACCESS_TOKEN", "GOOGLE_BILLING_PROJECT",
	"GOOGLE_IMPERSONATE_SERVICE_ACCOUNT", "GOOGLE_SERVICE_ACCOUNT", "GOOGLE_SERVICE_ACCOUNT_IMPERSONATION_URL",
	"GOOGLE_PROJECT", "GOOGLE_REGION", "GOOGLE_ZONE",
}

// providerSettingIn is the first provider setting env carries, "" when it
// carries none.
func providerSettingIn(env []string) string {
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasSuffix(k, "_CUSTOM_ENDPOINT") || slices.Contains(providerSettings, k) {
			return k
		}
	}
	return ""
}

// preflight is the root's own files, read before terraform init: the one part
// of a managed plan the plan JSON cannot show, and so the one part the cover
// cannot refuse (PR #483's required residual (b), and the design's §6 item 4).
//
// What it refuses, and why each is invisible to the gate:
//
//   - a `backend` block: terraform show -json does not carry the backend at
//     all, so a `backend "http"` (or "s3", or any other) pointing at an
//     outside address would send the managed project's state there and bypass
//     D77's dedicated bucket. This command sets the backend itself.
//   - a `cloud` block: the same thing one layer up — Terraform Cloud runs the
//     apply where the plan says, with an identity the plan chooses.
//   - a `provider_meta` block: it is not in the plan's configuration either,
//     and it is how a provider is handed settings outside its own block.
//   - an `ephemeral` block: an ephemeral resource is opened at apply with the
//     applier's credentials, and terraform leaves the block itself out of the
//     plan's configuration. The cover reads an ephemeral from an expression's
//     references, so one that nothing references — or that only a `local` or a
//     `depends_on` names — is invisible to it. Refusing the block covers all
//     three.
//   - an `action` block: the plan carries an action_invocations key the cover
//     refuses, but the block is refused here too, before anything runs.
//
// It is a hand-written reader and not an HCL parser (this repository has no
// HCL dependency), so it reads structure only: comments, quoted strings and
// heredocs are skipped, block nesting is tracked, and a file it cannot read as
// HCL at all is refused rather than skipped (fail closed). The two directions
// of a mistake are not symmetric: a file it cannot make sense of stops an
// apply that a person can then fix, while a file it read too leniently would
// apply a root the gate never saw. The one place its leniency shows is a
// quoted string holding a nested-quoted interpolation ("${"a"}"), which it
// reads as two strings; that can only make it refuse.
//
// Every local module the files call is read too (a `source` of "./x" or
// "../x"), because a module's own files are not in the root's directory and
// are loaded by terraform init. A module with a remote source — a registry or
// a git address — is NOT read here: its files are not in the working directory
// before init, and what the plan shows of its resources is the managed cover's
// (managedConfigResources walks the plan's own module tree). That boundary is
// stated rather than implied: a remote module's backend, provider_meta and
// unreferenced ephemeral blocks are outside this refusal.
func (r Request) preflight() error {
	root := r.Workdir.Root
	bad, err := scanRoot(root)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPreflight, err)
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: %s: %s", ErrPreflight, r.what(), strings.Join(bad, "; "))
	}
	return nil
}

// refusedBlocks are the block types no managed root may carry, and where they
// are refused: `backend`, `cloud` and `provider_meta` at the top level and
// inside a top-level `terraform` block (which is where terraform reads them),
// and `ephemeral` and `action` at the top level, where terraform's own
// top-level block types live.
var refusedBlocks = map[string]bool{
	"backend": true, "cloud": true, "provider_meta": true, "ephemeral": true, "action": true,
}

// moduleBlock is the top-level block a local module is called from.
const moduleBlock = "module"

// maxModuleDepth bounds the walk of local modules: a module tree deeper than
// this is refused rather than followed, so a symlink or a very long chain
// cannot make the scan unbounded.
const maxModuleDepth = 32

// scanRoot reads every .tf and .tf.json file of root and of every local module
// they call, and returns a one-line reason per refused block, or an error when
// a file cannot be read as HCL at all.
func scanRoot(root string) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("the root %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("the root %s is not a directory", root)
	}
	seen := map[string]bool{}
	var bad []string
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if depth > maxModuleDepth {
			return fmt.Errorf("the modules under %s nest deeper than %d, which is a cycle or a tree no one reads", root, maxModuleDepth)
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return fmt.Errorf("the module %s: %w", dir, err)
		}
		if seen[real] {
			return nil
		}
		seen[real] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("the module %s: %w", dir, err)
		}
		var modules []string
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".tf") && !strings.HasSuffix(name, ".tf.json") {
				continue
			}
			file := filepath.Join(dir, name)
			if e.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%s is a symlink, and a managed apply reads the root's own files alone", file)
			}
			src, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			var (
				fileBad []string
				fileMod []string
			)
			if strings.HasSuffix(name, ".tf.json") {
				fileBad, fileMod, err = scanJSON(file, src)
			} else {
				fileBad, fileMod, err = scanHCL(file, src)
			}
			if err != nil {
				return fmt.Errorf("refusing, because %s cannot be read as HCL: %w", file, err)
			}
			bad = append(bad, fileBad...)
			for _, m := range fileMod {
				mod, err := localModule(abs, dir, m)
				if err != nil {
					return err
				}
				modules = append(modules, mod)
			}
		}
		for _, m := range modules {
			if err := walk(m, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(abs, 0); err != nil {
		return nil, err
	}
	return bad, nil
}

// localModule resolves a module block's source against the directory the block
// is in: the module's directory, or an error when the source is not a local
// path, is a symlink, or leaves the root.
func localModule(root, dir, source string) (string, error) {
	src := strings.TrimSpace(source)
	// path.Clean would drop the "./" a local source is written with, so the
	// prefix is read from the source as the file writes it.
	if !strings.HasPrefix(src, "./") && !strings.HasPrefix(src, "../") && src != "." && src != ".." {
		return "", fmt.Errorf("the module source %q in %s is not a local path, and this scan reads local modules alone", source, dir)
	}
	mod := filepath.Join(dir, filepath.FromSlash(src))
	rel, err := filepath.Rel(root, mod)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the module source %q in %s is outside the root %s, and a managed apply reads the root's own tree alone", source, dir, root)
	}
	if st, err := os.Lstat(mod); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("the module source %q in %s is a symlink, and a managed apply reads the root's own tree alone", source, dir)
	}
	return mod, nil
}

// tokKind is what one token of a .tf file is, as far as this scan cares.
type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokString
	tokOpen
	tokClose
	tokEq
	tokOther
)

// token is one lexical token: its kind, its text (an identifier's name or a
// string's contents) and the line it started on.
type token struct {
	kind tokKind
	text string
	line int
}

// scanHCL reads one .tf file: the refused blocks it declares and the local
// module sources it names, or an error when it cannot be read as HCL.
func scanHCL(file string, src []byte) ([]string, []string, error) {
	toks, err := lex(file, src)
	if err != nil {
		return nil, nil, err
	}
	var bad, modules []string
	// stack is the names of the open blocks; "" is a brace that is not a block
	// (an object value), which still has to be counted so the nesting stays
	// right.
	var stack []string
	pending := ""
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case tokEOF:
			if len(stack) != 0 {
				return nil, nil, fmt.Errorf("%s: %d block(s) are never closed", file, len(stack))
			}
			return bad, modules, nil
		case tokOpen:
			stack = append(stack, pending)
			pending = ""
		case tokClose:
			if len(stack) == 0 {
				return nil, nil, fmt.Errorf("%s:%d: a } closes no block", file, t.line)
			}
			stack = stack[:len(stack)-1]
			pending = ""
		case tokIdent:
			// Labels (strings or identifiers) may follow a block's name; the
			// block is the name if a { follows them, and an attribute or an
			// expression otherwise.
			j := i + 1
			for j < len(toks) && (toks[j].kind == tokString || toks[j].kind == tokIdent) {
				j++
			}
			if j < len(toks) && toks[j].kind == tokOpen {
				if refusedBlock(t.text, stack) {
					bad = append(bad, fmt.Sprintf("%s:%d: a %s block, which a managed apply refuses before init", file, t.line, t.text))
				}
				if t.text == moduleBlock && len(stack) == 0 {
					if src, ok := moduleSource(toks, j); ok {
						modules = append(modules, src)
					}
				}
				pending = t.text
				i = j - 1
				continue
			}
			pending = ""
			i = j - 1
		default:
			pending = ""
		}
	}
	return bad, modules, nil
}

// refusedBlock says whether a block named name, opened with the stack of
// enclosing blocks, is one no managed root may carry.
func refusedBlock(name string, stack []string) bool {
	if !refusedBlocks[name] {
		return false
	}
	switch name {
	case "backend", "cloud", "provider_meta":
		// Where terraform reads them: at the top level (invalid HCL, and
		// refused anyway) and inside a top-level terraform block.
		return len(stack) == 0 || (len(stack) == 1 && stack[0] == "terraform")
	default:
		// ephemeral and action are top-level block types of their own.
		return len(stack) == 0
	}
}

// moduleSource is the `source` attribute of the module block whose body opens
// at index open, "" when it has none or it is not a literal string.
func moduleSource(toks []token, open int) (string, bool) {
	depth := 0
	for i := open; i < len(toks); i++ {
		switch toks[i].kind {
		case tokOpen:
			depth++
		case tokClose:
			depth--
			if depth == 0 {
				return "", false
			}
		case tokIdent:
			if toks[i].text != "source" || depth != 1 {
				continue
			}
			if i+2 < len(toks) && toks[i+1].kind == tokEq && toks[i+2].kind == tokString {
				return toks[i+2].text, true
			}
		}
	}
	return "", false
}

// lex turns a .tf file into tokens, skipping comments (// and /* */), quoted
// strings and heredocs, and refusing a file it cannot read to the end.
func lex(file string, src []byte) ([]token, error) {
	var toks []token
	line := 1
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#' || (c == '/' && i+1 < len(src) && src[i+1] == '/'):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return nil, fmt.Errorf("%s:%d: a /* comment is never closed", file, line)
			}
			line += bytes.Count(src[i:i+2+end], []byte("\n"))
			i += 2 + end + 2
		case c == '"':
			start := line
			i++
			var b strings.Builder
			for {
				if i >= len(src) {
					return nil, fmt.Errorf("%s:%d: a string is never closed", file, start)
				}
				if src[i] == '\n' {
					return nil, fmt.Errorf("%s:%d: a string runs past the end of its line", file, start)
				}
				if src[i] == '\\' && i+1 < len(src) {
					b.WriteByte(src[i+1])
					i += 2
					continue
				}
				if src[i] == '"' {
					i++
					break
				}
				b.WriteByte(src[i])
				i++
			}
			toks = append(toks, token{kind: tokString, text: b.String(), line: start})
		case c == '<' && i+1 < len(src) && (src[i+1] == '<' || (src[i+1] == '-' && i+2 < len(src) && src[i+2] == '<')):
			i += 2
			if src[i] == '-' {
				i++
			}
			start := i
			for i < len(src) && (isIdentByte(src[i]) || src[i] == '-') {
				i++
			}
			tag := string(src[start:i])
			if tag == "" {
				return nil, fmt.Errorf("%s:%d: a heredoc has no tag", file, line)
			}
			for i < len(src) && src[i] != '\n' {
				i++
			}
			// The body ends at a line whose first non-space content is the tag.
			for {
				if i >= len(src) {
					return nil, fmt.Errorf("%s:%d: the heredoc <<%s is never closed", file, line, tag)
				}
				i++ // the newline
				line++
				body := src[i:]
				nl := bytes.IndexByte(body, '\n')
				var l []byte
				if nl < 0 {
					l = body
				} else {
					l = body[:nl]
				}
				if strings.TrimSpace(string(l)) == tag {
					i += len(l)
					break
				}
				if nl < 0 {
					return nil, fmt.Errorf("%s: the heredoc <<%s is never closed", file, tag)
				}
				i += nl
			}
		case isIdentStart(c):
			start := i
			for i < len(src) && isIdentByte(src[i]) {
				i++
			}
			toks = append(toks, token{kind: tokIdent, text: string(src[start:i]), line: line})
		case c == '{':
			toks = append(toks, token{kind: tokOpen, line: line})
			i++
		case c == '}':
			toks = append(toks, token{kind: tokClose, line: line})
			i++
		case c == '=':
			toks = append(toks, token{kind: tokEq, line: line})
			i++
		default:
			toks = append(toks, token{kind: tokOther, line: line})
			i++
		}
	}
	toks = append(toks, token{kind: tokEOF, line: line})
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentByte(c byte) bool {
	return isIdentStart(c) || c == '-' || (c >= '0' && c <= '9')
}

// scanJSON reads one .tf.json file: the refused blocks it declares and the
// local module sources it names, or an error when it is not JSON this scan can
// read. A key may hold one block body or a list of them (the HCL JSON syntax
// allows both), and anything else where a block body belongs is refused rather
// than skipped.
func scanJSON(file string, src []byte) ([]string, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	var doc map[string]json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nil, fmt.Errorf("%s: it holds more than one JSON value", file)
	}
	var bad, modules []string
	bodies := func(raw json.RawMessage) ([]map[string]json.RawMessage, error) {
		var one map[string]json.RawMessage
		if err := json.Unmarshal(raw, &one); err == nil {
			return []map[string]json.RawMessage{one}, nil
		}
		var many []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &many); err != nil {
			return nil, fmt.Errorf("%s: a block that is neither an object nor a list of objects: %w", file, err)
		}
		return many, nil
	}
	for key, raw := range doc {
		switch key {
		case "ephemeral", "action", "backend", "cloud":
			bad = append(bad, fmt.Sprintf("%s: a %s block, which a managed apply refuses before init", file, key))
		case "terraform":
			bs, err := bodies(raw)
			if err != nil {
				return nil, nil, err
			}
			for _, b := range bs {
				for _, k := range []string{"backend", "cloud", "provider_meta"} {
					if _, ok := b[k]; ok {
						bad = append(bad, fmt.Sprintf("%s: a %s block, which a managed apply refuses before init", file, k))
					}
				}
			}
		case moduleBlock:
			bs, err := bodies(raw)
			if err != nil {
				return nil, nil, err
			}
			for _, b := range bs {
				for _, body := range b {
					var m map[string]json.RawMessage
					if json.Unmarshal(body, &m) != nil {
						continue
					}
					var source string
					if json.Unmarshal(m["source"], &source) == nil && source != "" {
						modules = append(modules, source)
					}
				}
			}
		}
	}
	return bad, modules, nil
}
