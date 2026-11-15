package credsvc

// The workflow-secrets route (B3.2, G18, G19): how one of a repository's
// workflow secrets reaches the job that declares it when the job does not mount
// it.
//
// # What it is for
//
// A workflow's `secrets:` are stored once and mounted by the job, and S3's flip
// (B3.8) takes the mount away for a repository the switch serves: from then on
// the job holds no value, and the runner asks for it here, at bootstrap, before
// the agent starts (B3.4). No value moves and no name changes (F33, D1267): this
// route reads the very container the job's own mount read, which is the answer
// `fugaro update` computed and wrote down as a record (B3.1,
// creds/workflowsecrets/<slug>) — the repository's own copy when it has an
// enabled version, the project-wide copy when the operator's allow-list admits
// this repository to one, a choice the service cannot make for itself because
// it cannot read an operator's local config (D2073).
//
// # Who it serves, and who it refuses
//
// The call is a per-repository job's, and the repository, the workflow and the
// container are all decided by the service:
//
//   - the REPOSITORY is the calling job account's own binding. The record is
//     keyed by the slug (JobAccount.Slug, creds.PathWorkflowSecrets) and the
//     pairing in ServeHTTP has already held that slug equal to the verified run
//     identity's; JobAccount.Repo is the same repository in the spelling the
//     published policy speaks, and neither is a field of the call.
//   - the WORKFLOW is JobAccount.Workflow, the workflow whose job this account
//     is (JobAccountsFor built it from the same pair the deployer derived the
//     account's name from), never X-Fugaro-Run-Job, which is the caller's claim
//     and decides nothing anywhere in this service — one workflow's job is never
//     served another's secrets, which is what G19 is.
//   - the CONTAINER is the record's entry for the variable the call names, and
//     nothing a caller sends can reach a container the record does not hold.
//
// Everything else is refused, and refused by DEFAULT: a shared project job and
// a review job are turned away here (their accounts name no one workflow's job,
// report.go's synthesised rows), and a launcher and a person never arrive —
// neither holds a run identity, so RequestFromHTTP refuses them before this file
// is reached. An absent record, a record this release does not know the version
// of, a workflow the record does not hold, a variable the record does not hold
// under it, and a container that cannot be read are each a typed refusal with no
// fallback of any kind. A fallback here would be the whole point of the route
// inverted: the service would answer a value the job did not ask for, or one the
// operator's own answer for this repository never named.
//
// # No value anywhere but the answer
//
// The value this route serves is a credential, and the answer is the only place
// it may appear (G18). It is read out of the container with the service's own
// account at call time, exactly as the bundle's values are (perSecrets: the one
// Secret Manager client and project the deployment already reads through), and
// it is never put in a log line, an audit line or an error: every refusal
// sentence below is written here, names the field that was wrong and not what it
// held, and the Secret Manager failure keeps its own class as the cause of a
// refusal whose message is this file's. The answer itself is written no-store
// (writeReadAnswer), because an intermediary that cached a workflow secret would
// be handing the next caller a credential this service did not serve it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
)

// ErrWorkflowSecretsBadRequest is a call whose body is not this contract's: not
// one JSON object of one field, or an env that is not a name a job could mount a
// secret under. It is the sentinel the reader wraps, so a test can tell this
// refusal from another without reading a sentence.
var ErrWorkflowSecretsBadRequest = errors.New("credsvc: the workflow-secrets call is not one this service reads")

// readWorkflowSecretsRequest reads a workflow-secrets call's body. It is bounded
// and decoded strictly (Convention 6: a field this code does not know is
// refused, not ignored), and no error here repeats what it read: the body is the
// caller's text, and a decoder's own message names the byte it choked on — so
// every sentence this returns is one of this file's own (github.go's
// readMintRequest, the reader this one is shaped after).
func readWorkflowSecretsRequest(r *http.Request) (WorkflowSecretsRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWorkflowSecretsRequestBytes+1))
	if err != nil {
		return WorkflowSecretsRequest{}, fmt.Errorf("%w: the body could not be read", ErrWorkflowSecretsBadRequest)
	}
	if len(body) > maxWorkflowSecretsRequestBytes {
		return WorkflowSecretsRequest{}, fmt.Errorf("%w: the body is larger than the %d bytes this service accepts", ErrWorkflowSecretsBadRequest, maxWorkflowSecretsRequestBytes)
	}
	var req WorkflowSecretsRequest
	if err := decodeStrict(body, &req); err != nil {
		return WorkflowSecretsRequest{}, fmt.Errorf("%w: the body is not one JSON object of one field", ErrWorkflowSecretsBadRequest)
	}
	if !config.EnvNameRE.MatchString(req.Env) {
		// The shape rule and not the record's: a name no workflow secret could
		// be recorded under is refused before the record is read, and the
		// refusal says which field was wrong and never what it held.
		return WorkflowSecretsRequest{}, fmt.Errorf("%w: the call names no environment variable", ErrWorkflowSecretsBadRequest)
	}
	return req, nil
}

// secretIDRE is Secret Manager's own rule for a secret ID: at most 255 of
// [A-Za-z0-9_-] (internal/backend/gcp/names.go states it beside the derivations
// that build these IDs).
//
// It is checked here rather than trusted, because the ID this route reads is the
// record's text and the record is a document the service reads rather than code
// it holds: a record whose entry were not a secret ID would otherwise be
// concatenated into a Secret Manager resource path, and a service that built a
// resource name out of a document is one an operator's mistake — or a writer's —
// could point at something else. A record that fails this is a record this
// release does not serve, which is the fail-closed answer.
var secretIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,255}$`)

// The refusals of this route, as sentences written here. Each one names the
// field that was wrong and never what the call sent, what the record holds or
// which container was read: a caller learns that it was refused, and an agent
// holding the run identity learns nothing it could enumerate with — which is why
// the variable it asked for is never echoed back either.
const (
	// workflowSecretNotThisJob answers a caller class this route does not serve
	// (G19). It is a verified caller refused at a route, and it is recorded once
	// like every other one.
	workflowSecretNotThisJob = "a workflow secret is served only to the per-repository job of the workflow it is recorded for"
	// workflowSecretNoReader answers a service with no reader for the record at
	// all: the route refuses rather than serving nothing, exactly as the sign
	// route does with no mark store.
	workflowSecretNoReader = "this service has no reader for the workflow secrets record, so it serves no workflow secret"
	// workflowSecretNoRecord answers a repository `fugaro update` has not
	// recorded: an installation that has not run the release that writes these,
	// or one whose write has not reached this repository yet. It is not an empty
	// answer (G19): a call served nothing would leave the runner without the
	// variable it needs and with no reason it could act on.
	workflowSecretNoRecord = "no workflow secrets record is written for the calling job's repository, so this service serves no workflow secret for it"
	// workflowSecretUnreadable answers a record that could not be read, or one
	// whose entry is not a container this service may read: a record half
	// understood could name a container the repository never mounted.
	workflowSecretUnreadable = "the workflow secrets record could not be read"
	// workflowSecretOtherWorkflow answers a record that holds nothing for the
	// calling job's workflow. The sentence says no more than that: which
	// workflows the record DOES hold, and which variables, are the repository's
	// own business, and one workflow's job is not told them (G19).
	workflowSecretOtherWorkflow = "the calling job's workflow is not one this repository's record holds a secret for"
	// workflowSecretNoSuchEnv answers a variable the calling job's workflow is
	// not recorded under. It is the fail-closed half of the record's being the
	// authority: the service serves what the record says the job mounts, and
	// never a name the caller supplied that the record does not hold (G19).
	workflowSecretNoSuchEnv = "the calling job's workflow holds no secret under the variable this call names"
	// workflowSecretUnreadableValue answers a container that could not be read,
	// is no longer there, or holds a version with no value. There is no fallback
	// from it: a service that answered the bundle's value, or an empty one,
	// would be serving a credential the repository's own record does not name
	// (D143, G19).
	workflowSecretUnreadableValue = "the workflow secret could not be read"
	// workflowSecretUnaudited answers a service whose audit line could not be
	// written: the act is not recorded, so it is not made — the rule the mint's
	// answer keeps for the same reason.
	workflowSecretUnaudited = "the workflow secret could not be audited, so it was not served"
	// workflowSecretAnswerFailed answers a service that could not build its own
	// answer. The branch is unreachable through a real call — the answer is two
	// strings — and it is kept rather than dropped for the mint's own reason:
	// the alternative to answering is a panic inside the one act that hands out
	// a credential, and a call that reached it must be refused.
	workflowSecretAnswerFailed = "the service could not build the workflow secret's answer"
)

// WorkflowSecretRecords reads the record `fugaro update` writes at
// creds/workflowsecrets/<slug> (B3.1). It is an interface so a test can serve
// one without a database, and RTDBWorkflowSecrets is the only implementation the
// service deploys.
//
// found=false is a repository no update has recorded, which is not an error: the
// caller decides what an absent record means, and for this route that is the
// refusal above (fail closed, G19).
type WorkflowSecretRecords interface {
	Read(ctx context.Context, slug string) (creds.WorkflowSecrets, bool, error)
}

// RTDBWorkflowSecrets reads the record as the service's own account, over the
// database client the rest of the service reads its nodes with — the rules deny
// every run identity creds/ (O1, D136), so the node's two readers are the
// operator's CLI, which writes it, and this service.
//
// It reads and never writes: creds.DB is the shape creds.ReadWorkflowSecrets
// takes, and this type holds no method that writes a record. The record is
// `fugaro update`'s to write and nobody else's (B3.1).
type RTDBWorkflowSecrets struct {
	// DB is the database; *rtdb.Client is one.
	DB creds.DB
}

// Read reads one repository's record. A nil DB answers as a failed read rather
// than as an absent record: an installation whose database is not wired is a
// deployment that is wrong, and the two are one refusal to a run
// (state_unavailable) and two different incidents to an operator.
func (r RTDBWorkflowSecrets) Read(ctx context.Context, slug string) (creds.WorkflowSecrets, bool, error) {
	if r.DB == nil {
		return creds.WorkflowSecrets{}, false, errors.New("credsvc: the service has no database client to read a workflow secrets record with")
	}
	return creds.ReadWorkflowSecrets(ctx, r.DB, slug)
}

// workflowSecrets answers one workflow-secrets call.
//
// Everything before this is the call's admission and nothing here repeats it:
// the two identities have been verified, the pairing has bound the calling job
// account to the run identity's repository, the operator's stop switches have
// been read, and the slip the call presents — if it presents one — has been
// verified against the run. What is left is this route's own gate (the class),
// the record, and the one container the call asks for.
//
// The order is the fail-closed order, and each step is answered before the next
// can be reached: the class first, because a class this route does not serve is
// refused before any record is read; then the body, before the record; then the
// record, before any container; then the container, before any value. Nothing on
// this path serves a value that a step above it did not authorise.
func (s *Server) workflowSecrets(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity, ja JobAccount) {
	if id.JobClass != ClassRepository {
		// A shared project job's account and a review job's name no one
		// workflow's job (the pairing synthesises their rows), and what a
		// WORKFLOW job is served is its own workflow's secrets: a caller whose
		// account cannot say which workflow that is is refused whole rather
		// than served the repository's union (G19). The refusal is the
		// pairing's own vocabulary — the run identity is not one this route
		// serves — so no runner needs a new case.
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnverified, ErrorUnverifiedRunIdentity, workflowSecretNotThisJob)
		return
	}
	req, err := readWorkflowSecretsRequest(r)
	if err != nil {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if s.cfg.WorkflowSecrets == nil {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretNoReader)
		return
	}
	rec, found, err := s.cfg.WorkflowSecrets.Read(ctx, ja.Slug)
	if err != nil {
		s.refuseCallCause(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadable, err)
		return
	}
	if !found {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretNoRecord)
		return
	}
	rows, ok := rec.Workflows[ja.Workflow]
	if !ok || len(rows) == 0 {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretOtherWorkflow)
		return
	}
	container := ""
	for _, row := range rows {
		if row.Env == req.Env {
			container = row.ID
			break
		}
	}
	if container == "" {
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusRequestRefused, ErrorRequestRefused, requestRefused+workflowSecretNoSuchEnv)
		return
	}
	if !secretIDRE.MatchString(container) {
		// The refusal names the record and not the entry: what the node holds
		// is the operator's business and not a caller's, and a resource path is
		// never built from a document this service has not read as a container
		// ID.
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, "", "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadable)
		return
	}
	source := s.perSecrets()
	if source == nil || source.Source == nil {
		// No Secret Manager client and no project: the same refusal an
		// unreadable container gets, because the two are one state to a run —
		// nothing was read — and neither is a fallback.
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue)
		return
	}
	value, found, err := source.Source.Value(ctx, container)
	if err != nil {
		// The reader's error is the cause and never the message: it is another
		// package's sentence and it reaches this service's own log line
		// (refuseCallCause), where an operator reads it.
		s.refuseCallCause(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue, err)
		return
	}
	if !found {
		// A container, or a version of one, that is not there. There is no
		// fallback of any kind: the record said this is where this variable's
		// value is, and a service that answered nothing — or the bundle's entry
		// under the same name — would be serving a credential this repository's
		// own record does not name (D143, G19).
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnreadableValue)
		return
	}
	s.answerWorkflowSecret(ctx, w, id, container, WorkflowSecretsResponse{Env: req.Env, Value: value})
}

// answerWorkflowSecret builds the answer, audits the act and writes it, in the
// order and for the reasons answerGitHubRead has: the answer is built first, so
// a body the service cannot build is a refusal with its own line and no issuance
// line at all; then the audit line, written BEFORE the value is handed over,
// because an act this service cannot record is not made (C1.13, D143) and a
// served value cannot be unserved — the runner holds it from the moment the
// bytes leave.
//
// The line names the CONTAINER the value was read from (Identity.Audit fills in
// the run, its repository, the job the run reported and the verified account).
// It is a name of the record's own — the thing the operator's `update` mounted,
// which is the one fact that says where a run's value came from — and it is one
// name with no tier, because a workflow secret is not a model credential of the
// cascade and was selected at no tier (audit.go's credentialWithoutTier).
func (s *Server) answerWorkflowSecret(ctx context.Context, w http.ResponseWriter, id Identity, container string, ans WorkflowSecretsResponse) {
	body, err := json.Marshal(ans)
	if err != nil {
		s.log.Error("credsvc: the workflow secret's answer could not be built", "error", err)
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretAnswerFailed)
		return
	}
	if err := s.auditCall(ctx, id, PurposeWorkflowSecret, container, "", StatusOK); err != nil {
		s.log.Error("credsvc: the workflow secret's audit line was not written", "error", err)
		s.refuseCall(ctx, w, id, PurposeWorkflowSecret, container, "", StatusUnavailable, ErrorStateUnavailable, workflowSecretUnaudited)
		return
	}
	writeReadAnswer(w, body)
}
