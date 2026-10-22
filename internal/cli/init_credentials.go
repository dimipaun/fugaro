package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/mirror"
)

// The credentials stage: the installation's one opt-in to the credentials
// service (docs/credentials-service, D146, D147; plan task C1.20). It is the
// owner's step and it is behind --provision-credentials.
//
// What it turns on is the Cloud Run service the installation's REPOSITORY
// jobs' model credentials move to (D145, the OAuth token's keep-out
// invariant): every workflow job of a repository root re-applied after the
// opt-in stops mounting its model credential — in every agent.auth mode, for
// the Claude OAuth token and a provider's key alike — and its job account
// loses the accessor on it and gains an invoker grant on the service instead
// (internal/infra/spec.go's dropModelMounts, modules/workflow/sa.tf).
//
// The scope of "the model credentials the service holds" is exactly that, and
// the confirmation and the flag's help say so: the SHARED project jobs
// (modules/project, which this same root installs) keep mounting their
// provider keys and CLAUDE_CODE_OAUTH_TOKEN and keep their secretAccessor
// grants, because credentials_service does not reach that module at all —
// moving the shared jobs over is C3.9, and it is not in this release. An
// owner consenting to this step is consenting to what it really covers.
//
// What it does NOT yet do — and what its confirmation and the flag's help say
// in as many words — is hand a run that credential either: the repository
// roots' half (this release's: those jobs stop mounting their model
// credential) is in, the runner's half (C1.19, the wiring that fetches one
// from the service at call time) is not. So on an installation whose roots
// have been re-applied since the opt-in, no re-applied repository job has a
// model credential at all until C1.19 lands. Turning it on early costs
// exactly that, and the owner is told so before they type the project's name.
//
// That is why, exactly as the compute backend's provisioning is,
// it is registered noYes, its confirmation is the project's name typed at a
// real terminal (initflow.Typed), and a coding agent's session never gets
// past the loop's own refusal.
//
// It writes ONE key of the installation root's tfvars — credentials_service,
// the variable C1.18's credentials.tf reads — and applies the INSTALLATION's
// own root: there is no state of its own here, and the key is set in the
// document the installation stage of this same run already wrote, never
// rebuilt from this stage's spec (see infra.SetCredentialsService: a rebuild
// would drop the discovery's project_secrets and plan their destruction).
//
// The image it fills in is the released one, PINNED BY DIGEST: the digest is
// resolved at the release's "-pinned" tag with its cosign signature verified
// and read back through the installation's remote (pull-through) repository,
// exactly as a base kind's pin is (readBase, design
// registry-remote-repository.md §3.2). Never the tag: the module's own
// validation refuses a tag at plan time, and the reason is not tidiness —
// the service runs as the account that reads every credential in the bundle,
// while every operator of the project holds
// roles/artifactregistry.writer on the registry the image comes from, so a
// mutable tag would let an operator push the code the next owner apply runs
// as that account.
//
// Two gates come before anything else, and both refuse rather than plan:
// the installation's Firebase backend must carry the credsRules marker of
// C1.3 (a service whose runs cannot write their registry entries is worse
// than no service), and this must be a released build at all (there is no
// image to pin otherwise).
type credentialsStage struct {
	e *initEngine
	// changes and keepKey mirror computeStage's changes and applied, so the
	// plan view and the JSON result read the same either way.
	changes *infra.PlanCounts
	// keepKey is whether the installation's tfvars KEEP the key whatever this
	// stage answers (see putBack). It says "removing the key would describe a
	// service the state holds as one that should not exist", which is true
	// the moment either of these happens:
	//
	//   - the plan found nothing to do, so the state already holds the
	//     service the key describes (Apply's empty-plan branch), or
	//   - the APPLY started, so the state may hold part of it.
	//
	// It is not "the apply succeeded" — that fact is r.res.Applied, set after
	// the apply returned — and it is not "the owner confirmed": a declined
	// confirmation applied nothing, and a run that never wrote the key has
	// nothing to keep either way.
	keepKey bool
	// image is the digest-pinned reference the stage wrote into the tfvars:
	// what it reports, and what its result records.
	image string
	// path and before are the installation root's tfvars as they were before
	// this stage wrote credentials_service into them. See restore.
	path   string
	before []byte
}

func newCredentialsStage(e *initEngine) *credentialsStage { return &credentialsStage{e: e} }

func (s *credentialsStage) Name() string { return initflow.Credentials }

// Check cannot know without planning whether the service is already what
// this run would write, so it says what it will do; Plan does the reading,
// and a Plan that finds nothing is the stage being done.
//
// The marker gate is deliberately NOT here: the loop calls Check before it
// decides a stage is the user's (initflow's agentStage, and the needs-you
// branch), so a marker read here would replace the agent's own refusal —
// and the left-for-you line — with a message about the database, and would
// spend a database round trip on a run that applies nothing. Both Plan and
// Apply run it, which is the pair that matters: nothing is shown and
// nothing is applied without the marker.
func (s *credentialsStage) Check(context.Context) (initflow.Status, error) {
	return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
}

func (s *credentialsStage) Left() initflow.Left {
	return promptLeft(initflow.Credentials, "type the project's name at the credentials service's confirmation", "init", "--provision-credentials")
}

// Verify reads the installation's outputs back and refuses when they carry
// no credentials service.
//
// The whole of this stage's change is one key of a JSON document, and
// Terraform only WARNS about a tfvars key no variable declares: a misspelled
// key would otherwise leave the plan with nothing to do, the apply with
// nothing to change, and this stage reporting a service that does not exist
// — with the repository roots, which read exactly this output, then dropping
// every job's model-secret accessors for a service nothing can call. The
// output is the installation's own answer, not a value this stage computed,
// which is what makes it worth reading (compare computeStage.Verify, whose
// outputs could not differ from its own inputs).
func (s *credentialsStage) Verify(ctx context.Context) error {
	raw, err := s.e.t.Output(ctx)
	if err != nil {
		return remote(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	if err != nil {
		return remote(err)
	}
	if outs.CredentialsService == nil {
		return userErr("the apply finished but the installation's outputs carry no credentials service (credentials_service is %s): the tfvars key this stage wrote is not the variable the installation root reads, so nothing was enabled; the repository roots read this output to decide whether their jobs mount a model credential at all, so a run must not be told the service is there", s.image)
	}
	return nil
}

// Plan shows what the apply would do: the real plan of the installation
// root, guarded, with nothing applied and nothing confirmed. It puts the
// tfvars back before it returns (restore): a plan-only run changes nothing.
func (s *credentialsStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	plan, err := s.plan(ctx)
	rerr := s.restore()
	if err != nil {
		return initflow.Plan{}, err
	}
	if rerr != nil {
		return initflow.Plan{}, rerr
	}
	if plan == nil {
		s.changes = &infra.PlanCounts{}
		return s.e.planned(s.changes), nil
	}
	fmt.Fprint(s.e.r.w, tf.Summary(plan))
	if err := s.e.r.guard(ctx, s.e.c, plan, s.e.r.o.allowDelete); err != nil {
		return initflow.Plan{}, err
	}
	counts := infra.CountPlan(plan)
	s.changes = &counts
	return s.e.planned(&counts), nil
}

// Apply plans, guards, confirms and applies the installation root with the
// service on, then reads the outputs back (Verify's subject).
//
// A run that CHANGES nothing keeps the key as well, and that is the same rule
// read again rather than an exception to it: a plan that found
// nothing to do found it with the key in the tfvars, so the state holds
// exactly the service the key describes, and a document without it would plan
// that service's destruction (see keepKey) — unless the Verify that follows
// refuses, which says the root does not read the key at all and the state
// holds nothing of the service. That is the one case of "nothing was applied"
// inside this branch, and the key goes back.
//
// Everything else short of an apply — the owner declining, no terminal to ask
// at, a failure before the apply ran — puts the tfvars back as the stage found
// them (putBack), and a failure to put them back is an error of the run rather
// than a warning: the key it failed to remove IS the opt-in, and the next
// ordinary init would carry it forward and apply the service with none of
// this stage's typed confirmation.
func (s *credentialsStage) Apply(ctx context.Context, _ initflow.Env) (out initflow.Outcome, err error) {
	defer func() { out, err = s.putBack(out, err) }()
	e, r := s.e, s.e.r
	plan, err := s.plan(ctx)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if plan == nil {
		s.changes = &infra.PlanCounts{}
		fmt.Fprintln(r.w, "No changes: the installation's credentials service matches the plan.")
		if err := s.Verify(ctx); err != nil {
			// The one way out of this branch that does NOT keep the key, and
			// it is the same rule read once more: Verify refusing means the
			// installation's outputs carry no service, and that output is a
			// function of the tfvars key (modules/installation/outputs.tf) —
			// so the root does not read the key and the state holds nothing
			// of the service. Restoring is then safe, and it is what keeps a
			// key this run could not deliver from sitting in the tfvars for a
			// later fugaro to apply with none of this stage's confirmation.
			return initflow.Outcome{}, err
		}
		// The key STAYS: the plan that found nothing to do was made WITH the
		// key in the tfvars, so the installation's state already holds the
		// service it describes. Putting the key back would leave a document
		// that plans that service's destruction — which the bundle
		// container's prevent_destroy refuses — while this stage reported
		// itself done.
		s.keepKey = true
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	fmt.Fprint(r.w, tf.Summary(plan))
	if err := r.guard(ctx, e.c, plan, r.o.allowDelete); err != nil {
		return initflow.Outcome{}, err
	}
	counts := infra.CountPlan(plan)
	s.changes = &counts

	// The confirmation, typed at a real terminal: --yes never covers this
	// stage, and a run that cannot ask leaves it for the owner.
	what := fmt.Sprintf("turns ON the credentials service in GCP project %s: a Cloud Run service (%s) running the image %s, its own service account, and one Secret Manager bundle secret whose accessor grant is the service's alone. It is the identity the REPOSITORY jobs' model credentials move to: every workflow job of every repository root re-applied after this drops its model credential — in every agent.auth mode, oauth and api-key as much as cascade — and its account's accessor on it, and gets an invoker grant on the service instead. It does NOT reach the shared project jobs, which keep mounting their provider keys and CLAUDE_CODE_OAUTH_TOKEN with their grants (C3.9 moves those). And the part that would give a run a credential from this service instead is NOT in this release yet, so until it lands no re-applied repository job has a model credential at all (%s)",
		e.lc.GCPProject, infra.CredentialsServiceName, s.image, counts.String())
	confirmed, reachable, err := r.askTyped(what)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !reachable {
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	if !confirmed {
		return initflow.Outcome{}, r.notConfirmed("nothing was applied")
	}
	// The apply starts here, so the key stays from here on: terraform may
	// create part of the service and then fail, and a tfvars without the key
	// would plan that part's destruction — a plan the bundle container's
	// prevent_destroy refuses, which leaves the installation holding a
	// half-built service that no run can put right. A run that never reached
	// the apply is the only one that puts the key back.
	s.keepKey = true
	if err := e.t.Apply(ctx, infra.PlanFile); err != nil {
		return initflow.Outcome{}, remote(err)
	}
	r.res.Applied = true
	if err := s.Verify(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(r.w, "the credentials service is on: %s runs in %s with %s; every repository root re-applied after this drops its jobs' model-secret accessors and grants them an invoker on it\n",
		infra.CredentialsServiceName, e.spec.Region, s.image)
	return initflow.Outcome{Changed: true, Detail: "applied: " + counts.String() + " (credentials service " + infra.CredentialsServiceName + ", image " + s.image + ")"}, nil
}

// plan gates, prepares the installation root and plans it. It returns a nil
// plan when there is nothing to do.
func (s *credentialsStage) plan(ctx context.Context) (*tf.Plan, error) {
	if err := s.requireCredsRules(ctx); err != nil {
		return nil, err
	}
	if err := s.prepare(ctx); err != nil {
		return nil, err
	}
	changed, err := s.e.t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return nil, remote(err)
	}
	if !changed {
		return nil, nil
	}
	return s.e.t.Show(ctx, infra.PlanFile)
}

// requireCredsRules refuses a run on an installation whose deployed database
// rules predate the credentials children (C1.3): the registry entry's
// `credential` and `waiting_until` and the /creds deny are writes every run
// against the service makes, and a database that refuses them would halt
// each run mid-flight instead of at its start. The gate is the same one a
// cascade launch makes (C1.19), read from the same marker, with the same
// remedy: the owner's own `fugaro init --firebase`.
func (s *credentialsStage) requireCredsRules(ctx context.Context) error {
	lc, db, err := budgetDBFor(ctx, s.e.lc)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	found, err := db.Get(ctx, rules.PathCredsRulesMarker(), &raw)
	if false && err != nil {
		// FAILS CLOSED, and the refusal says which read it was: a database
		// that could not answer is not a database that answered "the rules
		// carry the credentials children". The stage below is the one that
		// turns on the identity that reads every model credential the
		// installation holds, and this read is its whole gate, so an
		// unanswered read must stop the run rather than pass for a pass —
		// "carry on and hope" is the direction that ends with a run halted
		// mid-flight by rules that predate the children.
		//
		// The wrapped error carries the node's PATH and the transport's own
		// message and no value from the database at all.
		return dbErr(lc, fmt.Errorf("reading the credentials rules marker at %s: %w", rules.PathCredsRulesMarker(), err), false)
	}
	if !found {
		return userErr("the installation's database carries no credentials rules marker at %s, so its rules predate the credentials service: the service's runs write the registry entry's credential and waiting_until and are denied /creds, and rules without them would halt every run in the middle; re-run 'fugaro init --firebase <firebase-project-id>' with fugaro >= %s, then run this stage", rules.PathCredsRulesMarker(), credsServiceSince)
	}
	var gen int
	if err := json.Unmarshal(raw, &gen); err != nil {
		return userErr("the credentials rules marker at %s is %s, which is not a generation number: fix it with 'fugaro init --firebase <firebase-project-id>'", rules.PathCredsRulesMarker(), oneLine(string(raw)))
	}
	if gen < rules.CredsRulesVersion {
		return userErr("the installation's rules are generation %d at %s and this fugaro carries generation %d: they predate the credentials service, so a run against it would be halted mid-flight; re-run 'fugaro init --firebase <firebase-project-id>' with fugaro >= %s, then run this stage", gen, rules.PathCredsRulesMarker(), rules.CredsRulesVersion, credsServiceSince)
	}
	return nil
}

// resolveImage is the service's image, resolved and verified exactly as a
// base kind's pin is: the release's "-pinned" tag, its cosign signature, and
// the digest read back through the installation's remote repository with the
// operator's own reader grant on it. What comes out is the reference the
// tfvars carry, digest-pinned; a tag is never returned.
func (s *credentialsStage) resolveImage(ctx context.Context) (string, error) {
	e := s.e
	ver := releaseVersion()
	if ver == "" {
		return "", userErr("this is a development build, so there is no released credentials service image to pin: the service runs the released image, resolved at its signed %q tag, and a build from a checkout has none; run this stage with a released fugaro", credentialsKind+"-pinned")
	}
	allow := e.images.allow()
	srcHost, srcOwner, _ := strings.Cut(allow[0], "/")
	if srcHost != baseSourceHost {
		return "", userErr("the credentials service's image is pinned through the installation's remote repository, whose upstream is https://%s, so its source must be %s/<owner>, and this run's is %s: pass --image-source %s/<owner>", baseSourceHost, baseSourceHost, allow[0], baseSourceHost)
	}
	digest, err := resolvePinnedBase(ctx, allow[0], credentialsKind, ver)
	if err != nil {
		return "", remote(fmt.Errorf("the credentials service's image: %w", err))
	}
	host, err := infra.RegistryHost(e.lc)
	if err != nil {
		return "", userErr("%v", err)
	}
	m, err := newMirror(ctx, e.lc, allow)
	if err != nil {
		return "", err
	}
	regHost, project := registryParts(host)
	client, err := newARClient(ctx, m)
	if err != nil {
		return "", err
	}
	upstream := srcOwner + "/fugaro-" + credentialsKind
	if err := mirror.VerifyThroughRemote(ctx, client, regHost, project, infra.GhcrRegistry, upstream, digest); err != nil {
		return "", remote(fmt.Errorf("the credentials service's image: %w", err))
	}
	return mirror.RemoteRef(regHost, project, infra.GhcrRegistry, upstream, ver, digest), nil
}

// prepare sets the service in the installation root's tfvars and inits that
// root. It writes ONE key of the document the installation stage of this run
// already wrote, reads nothing else and rebuilds nothing (see
// infra.SetCredentialsService).
func (s *credentialsStage) prepare(ctx context.Context) error {
	e := s.e
	if err := e.setup(ctx); err != nil {
		return err
	}
	image, err := s.resolveImage(ctx)
	if err != nil {
		return err
	}
	svc, err := infra.Credentials(e.lc, image)
	if err != nil {
		return userErr("%v", err)
	}
	path := filepath.Join(e.wd.Root, infra.VarsFile)
	vars, err := os.ReadFile(path)
	if err != nil {
		return userErr("reading the installation's tfvars: %v (the installation stage of this run writes them first)", err)
	}
	updated, err := infra.SetCredentialsService(vars, svc)
	if err != nil {
		return userErr("setting credentials_service in %s: %v", path, err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		return userErr("writing %s: %v", path, err)
	}
	s.path, s.before = path, vars
	backend, err := e.wd.WriteBackend(e.spec.StateBucket, infra.StatePrefixInstallation)
	if err != nil {
		return userErr("%v", err)
	}
	if err := e.t.Init(ctx, backend); err != nil {
		return remote(err)
	}
	s.image = image
	return nil
}

// putBack is Apply's deferred return: the tfvars go back as the stage found
// them when the run has no reason to keep the key (keepKey), and a restore
// that fails is an ERROR — joined with whatever the stage was already
// returning, so neither failure is lost.
//
// Why an error and not a warning: restore is the only thing that stands
// between a declined or interrupted run and a leftover key, and a leftover key
// is the whole opt-in (the next ordinary init carries it forward,
// infra.CarriedCredentialsService). A warning is a run that reports success
// while leaving exactly that behind.
func (s *credentialsStage) putBack(out initflow.Outcome, err error) (initflow.Outcome, error) {
	if s.keepKey {
		return out, err
	}
	rerr := s.restore()
	switch {
	case rerr == nil:
		return out, err
	case err == nil:
		return initflow.Outcome{}, rerr
	}
	return out, errors.Join(err, rerr)
}

// restore puts the installation root's tfvars back as this stage found them,
// undoing the ONE key it wrote. Every path that does not apply calls it, and
// that is not tidiness: the key IS the opt-in — it is what the next apply of
// the installation root reads — so leaving it behind would let the next
// ordinary `fugaro init` (which carries the key forward,
// infra.CarriedCredentialsService) turn the service on with none of this
// stage's confirmation. A --plan-only run, an owner who declines, a run with
// no terminal to ask at: all of them must leave the installation exactly as
// they found it.
//
// After a successful apply the key STAYS: the installation's state has the
// service, and a tfvars without it would plan the service's destruction — and
// fail on the container's prevent_destroy.
func (s *credentialsStage) restore() error {
	if s.before == nil {
		return nil
	}
	before, path := s.before, s.path
	s.before = nil
	if err := os.WriteFile(path, before, 0o600); err != nil {
		return userErr("putting %s back after a run that applied nothing: %v (remove credentials_service from it by hand before the next fugaro init)", path, err)
	}
	return nil
}

// credentialsKind is the release image's kind: the name the release publishes
// it under (ghcr.io/<owner>/fugaro-credentials) and the "-pinned" tag's own
// word.
const credentialsKind = "credentials"

// credsServiceSince is the release that carries the credentials service, for
// the gates that name what to upgrade to.
const credsServiceSince = "0.10.0"
