package runner

// The runner's own fetch of a workflow secret its job does not mount (B3.4, S3,
// D491, D1267, D2073-D2075, F4).
//
// A workflow's `secrets:` are mounted into the job today, and S3's flip (B3.8)
// takes that mount away for a repository the credentials service serves: from
// then on the job holds no value for them, and the runner asks the service for
// one here, at bootstrap, before the agent starts. What the fetch reads is what
// the mount read -- the same container, named by the service's own record
// (internal/credsvc's workflow-secrets route) -- so no value moves and no name
// changes (F33, D1267).
//
// Four rules shape everything below.
//
//   - A mounted value always wins. A declared variable the job's environment
//     already carries is never fetched, so the flip is a no-op either way: a run
//     whose job still mounts its secrets behaves exactly as it did.
//   - The service decides. The call names one environment variable and nothing
//     else (credsvc.WorkflowSecretsRequest): which repository, which workflow
//     and which container are the service's own answer from the VERIFIED job
//     account, so this code has nothing to get wrong about them and a caller
//     cannot name another workflow's secret (G19).
//   - No value anywhere but the environment it belongs in and the redactor
//     (G18). A fetched value is registered with the redactor before it is
//     written anywhere, and it appears in no log line, no error reason and no
//     run record. A refusal is this file's sentence plus the contract's own
//     code, never the service's message beside it -- that text is written for
//     an operator's log and not for a run (reviewartifact.go's
//     ServiceSigner.Sign drops it for the same reason).
//   - It fails closed. An installation with no credentials service, a run with
//     no identity to present, a service that cannot be reached, a refusal and an
//     answer that is not this contract's all stop the run where it stands,
//     before any agent call (D143). The alternative -- carrying on without the
//     value -- is the run agent.BuildEnv refuses a moment later anyway, with a
//     sentence that would name the mount instead of the fetch.
//
// A review run fetches like any other, and the value it fetches never reaches
// its agent: bootstrap's review arm registers the values for redaction and hands
// agent.BuildEnv an empty secret list, and it reads the same environment this
// file writes (runner.go's secretEnvs). The service's own class gate is what
// makes that moot against a real service -- a review-class job names no one
// workflow's job, so the route refuses it (G19) -- and the run then stops
// exactly where it stopped before this fetch existed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
)

// workflowSecretsBudget bounds the whole fetch, every variable of the workflow
// together, the way slipClaimBudget bounds the slip claim: the service is one
// the run's start depends on, and a fetch that has not finished in this long is
// a run that does not start. One call per variable, so a workflow with several
// unmounted secrets shares this budget rather than spending it each.
const workflowSecretsBudget = 90 * time.Second

// fetchWorkflowSecrets fetches the value of every workflow secret this run
// declares whose variable the job's own environment does not carry (B3.4).
//
// It is called from bootstrap before the agent's environment is built, and it
// leaves the values where a mount would have left them: in the runner's own
// environment, which is where agent.BuildEnv reads a declared secret and where
// the review arm's registration reads it too. Nothing else about the run
// changes: the variable name, the workflow that declares it and the secret it
// names in the repository's file are the same whether the value arrived in the
// job's environment or through this call.
func (r *run) fetchWorkflowSecrets(ctx context.Context, wf config.Workflow) error {
	var missing []string
	for _, s := range wf.Secrets {
		if envLookup(r.d.Env, s.Env) == "" {
			missing = append(missing, s.Env)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	// A local run never fetches (D149): `fugaro exec` runs on the developer's
	// own credentials, holds no installation identity and reaches no private
	// service, so a value its environment lacks is the "secret %s is not set"
	// agent.BuildEnv has always answered -- a sentence that names the variable
	// and the fix, where a refusal to reach a service this run has none of would
	// name neither.
	url := envLookup(r.d.Env, credentialsServiceURLEnv)
	if r.d.Local || url == "" {
		// The job carries no URL, so the installation has not deployed the
		// service for it (D147) or has not switched this repository onto it:
		// there is nothing to ask, and the mounted value -- or BuildEnv's own
		// refusal -- is the whole answer, exactly as before this fetch existed.
		return nil
	}
	job, err := r.jobName()
	if err != nil {
		return fmt.Errorf("fetching the workflow's secrets: %w", errors.New(r.redact(err.Error())))
	}
	if job == "" {
		// jobName's contract means this cannot fire for a non-local run (the
		// only run it answers "" for is a local one, returned above), and the
		// service refuses a call that cannot report the job it is in.
		return errors.New("fetching the workflow's secrets: this run has no Cloud Run job name to report, and a call that cannot report the job it is in is not one the credentials service reads")
	}
	r.mu.Lock()
	sess := r.sess
	r.mu.Unlock()
	if sess == nil {
		// The run's own token is what the service verifies the run by (C1.10),
		// and a run on this path is always one the budget backend admitted
		// (C1.19), so this is a fact about how the run was started rather than
		// about the service.
		return errors.New("fetching the workflow's secrets: this run has no run identity, which is what the credentials service verifies a call by, so no workflow secret can be fetched for it")
	}
	svc := r.serviceIdentities(url, job, sess.IDToken())
	cctx, cancel := context.WithTimeout(ctx, workflowSecretsBudget)
	defer cancel()
	for _, name := range missing {
		if err := r.fetchWorkflowSecret(cctx, svc, url, name); err != nil {
			return err
		}
	}
	return nil
}

// fetchWorkflowSecret fetches one declared variable's value and puts it where a
// mount would have put it: the runner's environment, and the redactor.
//
// The value is registered with the redactor BEFORE it is written to the
// environment, and nothing between the answer and that registration can print
// anything: from the moment the answer is read, every reason this run publishes
// is redacted of it (runner.go's addSecret).
func (r *run) fetchWorkflowSecret(ctx context.Context, svc *gateway.Service, url, name string) error {
	body, err := json.Marshal(credsvc.WorkflowSecretsRequest{Env: name})
	if err != nil {
		return fmt.Errorf("fetching workflow secret %s: the call could not be built", name)
	}
	status, answer, err := svc.Post(ctx, url, credsvc.WorkflowSecretsPath, body)
	if err != nil {
		// Post writes its own half of the sentence and never the body it sent,
		// and the answer does not exist yet: nothing of the value can be in
		// this reason.
		return fmt.Errorf("fetching workflow secret %s: %w", name, err)
	}
	if status != http.StatusOK {
		// The refusal is the service's decision and asking it again would
		// answer the same thing, so the run stops here rather than retrying
		// (D143). Only the contract's own code is relayed: the message beside
		// it is the service's text.
		return fmt.Errorf("fetching workflow secret %s: the installation's credentials service answered %d%s", name, status, workflowSecretRefusalCode(status, answer))
	}
	var ans credsvc.WorkflowSecretsResponse
	if err := decodeWorkflowSecretAnswer(answer, &ans); err != nil {
		return fmt.Errorf("fetching workflow secret %s: %w", name, err)
	}
	if ans.Env != name {
		// The contract echoes the variable so an answer can never be read as
		// another variable's (credsvc.WorkflowSecretsResponse). An answer that
		// names another one is a body this route did not write, and putting its
		// value under the variable that was asked for would be mounting a
		// secret the call never named.
		return fmt.Errorf("fetching workflow secret %s: the credentials service's answer is for another variable", name)
	}
	if len(ans.Value) < minSecretLen {
		// The floor the mounted path holds too (minSecretLen): a value this
		// short cannot be redacted without redacting text that is not a secret,
		// and agent.BuildEnv refuses a declared secret under the same figure.
		// Refused here rather than registered: a two-byte value in the redactor
		// would rewrite unrelated text everywhere this run publishes.
		return fmt.Errorf("fetching workflow secret %s: the credentials service answered a value shorter than %d bytes, which cannot be redacted safely", name, minSecretLen)
	}
	r.addSecret(ans.Value)
	r.d.Env = append(r.d.Env, name+"="+ans.Value)
	return nil
}

// workflowSecretRefusalCode is the contract's own code for a refused answer, as
// ", refused: code", or "" when the body carries none this contract could have
// written. The call is a non-2xx answer, which is what credsvc.ReadWireError
// reads.
//
// Only the code is read, and the message beside it is dropped: it is the
// service's text, written for an operator's log, and a run's reason is not the
// place for another writer's sentence (reviewartifact.go's reviewRefusalCode,
// which reads the sign route's refusals the same way).
func workflowSecretRefusalCode(status int, answer []byte) string {
	e, err := credsvc.ReadWireError(status, answer)
	if err != nil || !reviewServiceErrorCode.MatchString(e.Error) {
		return ""
	}
	return ", refused: " + e.Error
}

// decodeWorkflowSecretAnswer reads the route's answer: exactly one JSON object
// of its two fields, and nothing after it. A field this release does not know is
// refused rather than ignored (Convention 6, credsvc's own decodeStrict), because
// a later service's answer read generously is a value read out of a document
// this release does not understand.
//
// No error here repeats what it read: the answer is a credential's, and a
// decoder's own message names the byte it choked on.
func decodeWorkflowSecretAnswer(raw []byte, out *credsvc.WorkflowSecretsResponse) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return errors.New("the credentials service's answer is not a workflow secret's")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("the credentials service's answer carries more than one document")
	}
	return nil
}
