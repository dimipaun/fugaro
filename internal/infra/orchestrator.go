package infra

import (
	"encoding/json"
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// OrchestratorAccountID is the resident orchestrator's own service account
// (mobile plan task P5.1b; docs/mobile/design/always-on-orchestrator.md
// section 4.3), created by the installation module for an installation whose
// tfvars carry the block `fugaro update` writes.
//
// It is a fixed account ID and not a per-installation name, like the
// coordinator's writer and the front door's: `fugaro doctor` reads the account
// back by this name, and the run's own plan review reads a member of the
// installation's project as this account's kind by it (internal/infra/tf's
// OrchestratorAccountID, the same literal — TestCoverNamesAreTheCodes is the
// pin). Because it is one account per installation, its ID is deliberately NOT
// a member of tf.FugaroAccounts: every grant rule that admits that set admits
// it for every job account the modules create, and this is the account that
// holds iam.serviceAccounts.signJwt on the budget signer.
//
// The block that turns it on is written by `fugaro update` (internal/cli's
// ensureOrchestratorBlock, in the credentials-service stage), composed by
// Orchestrator below, and carried forward by every later run of the door
// (CarriedOrchestrator): the account and its bindings are state, and no run
// writes the block a second time.
const OrchestratorAccountID = "fugaro-orch"

// OrchestratorPairingSecretID is the Secret Manager container holding the
// installation's copy of every pairing's MAC key (mobile plan task P5.1e; the
// design's section 4.2 and its section 4.7 step 2b): ONE container per
// installation, one version per pairing inside it.
//
// It is a fixed ID and not a per-installation name for the reason
// OrchestratorAccountID is one: the container is created by the installation
// module (deploy/terraform/gcp/modules/installation/orchestrator.tf, whose
// literal this constant is pinned to by the plan-level test), and the CLI that
// writes a version into it — `fugaro pair`, and `fugaro pair revoke` when it
// destroys one — computes no secret name of its own. A name derived from the
// project the way gcp.ProjectSecretID derives a repository's would be a second
// spelling Terraform cannot reproduce.
//
// The container carries `fugaro=managed` and the installation's name and
// deliberately NOT fugaro_secret: that label is what `fugaro update`'s
// discovery reads to decide which logical secret of the project's set a
// container holds, and this one is not a member of that set.
const OrchestratorPairingSecretID = "fugaro-orch-pairing"

// OrchestratorServiceName and OrchestratorSchedulerJob are the resident
// orchestrator's other two names (mobile plan task P5.1c; the design's section
// 4.1): the Cloud Run service the account runs and the Cloud Scheduler job
// whose tick wakes it.
//
// They are the release's own defaults rather than per-installation choices,
// because nothing but the block below names them: the service is a subcommand
// of the released binary (`fugaro orchestrator serve`), its invoker grant is
// made on the service name in the same apply, and the tick's URI is read off
// the service's own assigned URL — so an installation that wrote another name
// would be one whose tick the module still points at it. They are spelled the
// Scheduler names' way rather than the jobs' (HistoryJob's note: `ls` and
// max_parallel count every fugaro-* JOB as a workflow's) because neither is a
// Cloud Run job: one is a service and the other a Scheduler job, which no
// listing counts.
const (
	OrchestratorServiceName  = "fugaro-orchestrator"
	OrchestratorSchedulerJob = "fugaro-orchestrator-tick"
)

// OrchestratorSpec is the installation root's var.orchestrator (mobile plan
// tasks P5.1b and P5.1c; the design's sections 4.1 and 4.3): the account the
// resident orchestrator runs as, the token-minter row's two names, the service
// and the tick that wake it, and the environment its one process is configured
// by. It is the module's own object, field for field.
//
// Every member is DERIVED and none is supplied: the account, the service and
// the tick are the constants above, the signer is the Firebase root's own
// account ID (SignerAccountID — the same literal the Firebase module creates it
// under, which is what makes the address computable without an output), the
// database is the installation's budget block, and the image is the digest pin
// `fugaro update` already resolved for the credentials service — one released
// binary serves both, exactly as it serves the intake and the voice hop
// (IntakeSpec's "one release, one pin, two services"). Nothing here is a flag
// value, and no operator types a digest.
//
// It is not composed on every run. The block is the installation's own RECORD
// that the update wrote it — the account, its five bindings, the container and the
// service are state — so the writer writes it once, the first update that finds
// the installation able to carry one, and every later run carries it (CarriedOrchestrator) and moves only the image
// inside it (SetOrchestratorImage). A run that recomposed the block would be
// re-deciding the grants, and a service or tick name taken from a default over
// an installation that holds another one is a force-new replacement plan.
type OrchestratorSpec struct {
	AccountID string `json:"account_id"`
	// FirebaseProject and SignerAccountID are the token-minter row's two
	// names: that binding is made on the Firebase root's signer account, in
	// the Firebase root's project (the module's own comment says why they are
	// fields of this variable rather than derived from the coordinator's).
	FirebaseProject string `json:"firebase_project"`
	SignerAccountID string `json:"signer_account_id"`
	// Service, Image and SchedulerJob are the service and the tick: what runs
	// and what wakes it.
	Service      string `json:"service"`
	Image        string `json:"image"`
	SchedulerJob string `json:"scheduler_job"`
	// RTDBURL is the database the process reads this installation's node out
	// of, and SchedulerRegion is where the tick lives — a field of its own
	// because Cloud Scheduler is not offered in every Cloud Run region
	// (HistorySpec's rule).
	RTDBURL         string `json:"rtdb_url"`
	SchedulerRegion string `json:"scheduler_region"`
}

// Orchestrator is the resident orchestrator's block for lc, running image.
//
// The image is the credentials service's own resolved pin and this writer
// resolves nothing: `fugaro orchestrator serve` is a subcommand of the released
// binary the credentials service, the intake and the voice hop all run, so the
// digest `fugaro update` already read back through the installation's remote
// repository is this block's image too. It must be pinned by digest
// (credsImageRE) — the module's variable states the same rule — so a tag handed
// in here is refused rather than written into a document the plan refuses.
//
// The Firebase facts come from the local config's budget block, which
// `fugaro update --firebase-project` wrote from the Firebase root's outputs:
// the process reads this installation's own node, and an installation whose
// config names no database cannot be configured at all (the module's own
// tftest would fail the apply). Both halves are refused here by name rather
// than written empty.
//
// The scheduler region is the local config's, resolved the way the history
// job's and the coordinator's are: gcp.SchedulerRegion's nearest served
// location when the config names none.
func Orchestrator(lc *localcfg.Config, image string) (OrchestratorSpec, error) {
	if !credsImageRE.MatchString(image) {
		return OrchestratorSpec{}, userErr("the resident orchestrator's image %q must be pinned by digest (ending in @sha256: and 64 lower-case hex digits): it is the released binary's `orchestrator serve` subcommand, pinned exactly as the credentials service's and the intake's are, and a tag is mutable", image)
	}
	b, err := firebaseBackend(lc, "the resident orchestrator")
	if err != nil {
		return OrchestratorSpec{}, err
	}
	region := lc.SchedulerRegion
	if region == "" {
		var err error
		if region, err = gcp.SchedulerRegion(lc.Region); err != nil {
			return OrchestratorSpec{}, userErr("%v", err)
		}
	}
	return OrchestratorSpec{
		AccountID:       OrchestratorAccountID,
		FirebaseProject: b.FirebaseProject,
		SignerAccountID: SignerAccountID,
		Service:         OrchestratorServiceName,
		Image:           image,
		SchedulerJob:    OrchestratorSchedulerJob,
		RTDBURL:         b.RTDBURL,
		SchedulerRegion: region,
	}, nil
}

// SetOrchestrator returns the installation root's tfvars vars with orchestrator
// set to spec, and every other entry byte-for-byte what it was. It is
// SetIntake's read-modify-write of the ONE key and for the same reason: the
// document on disk carries the run's discovery — the live project secrets it
// found, its legacy registry, its imports — and rebuilding it from a spec would
// plan that discovery away.
//
// The writer asks this only for an installation that holds no block
// (OrchestratorRecorded, internal/cli's ensureOrchestratorBlock): the block is
// the installation's record, and a second write would recompose an account and
// five bindings that are state.
func SetOrchestrator(vars []byte, spec OrchestratorSpec) ([]byte, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	return setVarsKey(vars, OrchestratorKey, raw)
}

// UnsetOrchestrator returns the installation root's tfvars vars without the
// resident orchestrator's block, and every other entry byte-for-byte what it
// was.
//
// It is SetOrchestrator's undo, and it exists for the window in which a block
// can be written without becoming a fact about the installation:
// ensureOrchestratorBlock (internal/cli) writes it BEFORE the plan that carries
// it, because the plan reads the document on disk, and everything between that
// write and the apply — the guard, the owner's own answer, a plan that cannot be
// read, the stop --plan-only makes — can end the run with nothing created. A
// block left behind by such a run is not the record of an apply, it IS the
// switch: var.orchestrator turns the account on, so the next `fugaro update
// --yes`, or a managed project's run, which asks nobody, would apply the
// account, its five bindings, the pairing container and the service from a
// document the failed run left, past the plan and the confirmation the block
// was written for. The write is what the plan needs; the apply it was written
// for is the fact that keeps it.
//
// It deletes the ONE key, so the document it hands back is what the run's own
// writers left — the discovery, the images the stages before moved, this
// release's history job — with the entry the run decided not to make taken out
// of it, exactly as SetOrchestrator's write kept everything it found. A
// document that carries no block is returned as it was FOUND and not
// re-serialized: a caller asking twice, or asking for a write that never
// happened, writes nothing back.
func UnsetOrchestrator(vars []byte) ([]byte, error) {
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, ok := doc[OrchestratorKey]
	if !ok || block == nil {
		return vars, nil
	}
	delete(doc, OrchestratorKey)
	return sortedJSON(doc)
}

// OrchestratorOf returns the block the installation root's tfvars carry for the
// resident orchestrator, and false when they carry none (the variable is off)
// or an empty one. It is IntakeOf's reader, on the key that decides whether an
// installation has a resident orchestrator at all — a question the run's own
// config cannot answer, because the block is the record of what an earlier
// apply created.
//
// The account is read WHOLE, every member the module's variable requires: a
// block that names no account or no image is not one that could be carried into
// a plan or re-pinned, and the callers that ask are the ones whose next step is
// a write into it.
func OrchestratorOf(vars []byte) (OrchestratorSpec, bool, error) {
	if vars == nil {
		return OrchestratorSpec{}, false, nil
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return OrchestratorSpec{}, false, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, _ := doc[OrchestratorKey].(map[string]any)
	if block == nil {
		return OrchestratorSpec{}, false, nil
	}
	spec := OrchestratorSpec{}
	spec.AccountID, _ = block["account_id"].(string)
	spec.FirebaseProject, _ = block["firebase_project"].(string)
	spec.SignerAccountID, _ = block["signer_account_id"].(string)
	spec.Service, _ = block["service"].(string)
	spec.Image, _ = block["image"].(string)
	spec.SchedulerJob, _ = block["scheduler_job"].(string)
	spec.RTDBURL, _ = block["rtdb_url"].(string)
	spec.SchedulerRegion, _ = block["scheduler_region"].(string)
	if spec.AccountID == "" || spec.Service == "" || spec.Image == "" {
		return OrchestratorSpec{}, false, nil
	}
	return spec, true, nil
}

// SetOrchestratorImage returns vars with the resident orchestrator's image
// replaced by image, and every other entry byte-for-byte what it was —
// SetIntakeImage's re-pin, on the block that runs the same reference.
//
// It is how `fugaro update`'s credentials-service stage moves the resident
// orchestrator onto this release with the three services it shares the pin
// with: one released binary serves all four, and a run that moved the service
// and left the orchestrator behind would run the release behind's process
// against the state this run writes.
//
// A document with no orchestrator block is refused rather than given one, for
// SetIntakeURL's reason exactly: writing one here would turn the account, its
// five bindings, the pairing container and the service on for an installation
// whose owner never asked for a resident orchestrator.
func SetOrchestratorImage(vars []byte, image string) ([]byte, error) {
	if !credsImageRE.MatchString(image) {
		return nil, fmt.Errorf("the resident orchestrator's image %q must be pinned by digest (ending in @sha256: and 64 lower-case hex digits)", image)
	}
	doc, err := varsDoc(vars)
	if err != nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json is not readable: %w", err)
	}
	block, _ := doc[OrchestratorKey].(map[string]any)
	if block == nil {
		return nil, fmt.Errorf("the installation's terraform.tfvars.json carries no orchestrator to set the image of")
	}
	block["image"] = image
	doc[OrchestratorKey] = block
	return sortedJSON(doc)
}
