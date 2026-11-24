package runner

// The runner's own fetch of a workflow secret its job does not mount (B3.4, S3,
// D491, D1267, D2073-D2075, F4, and by name D3324/D3328/D3333).
//
// A workflow's `secrets:` are mounted into the job today, and S3's flip (B3.8)
// takes that mount away for a repository the credentials service serves: from
// then on the job holds no value for them, and the runner asks the service for
// one here, at bootstrap, before the agent starts. What the fetch reads is what
// the mount read -- the same container, named by the service's own record
// (internal/credsvc's workflow-secrets route) -- so no value moves (F33,
// D1267). What the call NAMES is the difference this file carries: version 2
// asks by the secret's logical name (config.Secret.Name), which is the unit
// the project record's allow-list is written in (D3333), and version 1 asks by
// the variable, which is the unit the flip's own record is written in.
//
// Five rules shape everything below.
//
//   - A mounted value always wins. A declared variable the job's environment
//     already carries is never fetched, so the flip is a no-op either way: a
//     run whose job still mounts its secrets behaves exactly as it did.
//   - The service decides. The call names one variable or one logical secret
//     and nothing else (credsvc.WorkflowSecretsRequest): which repository,
//     which workflow and which container are the service's own answer from the
//     VERIFIED job account, so this code has nothing to get wrong about them
//     and a caller cannot name another workflow's secret (G19, G1).
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
//   - One refusal is not final. Version 2's "this name is not in the project
//     record" (credsvc.ErrorWorkflowSecretNotAllowed) is also what a record
//     that does not hold the name YET answers, so that one answer is followed
//     by the same question in version 1 -- the record S3 wrote, variable by
//     variable -- and a repository already served by S3 keeps working until
//     its name is copied (D3325, D3328). It is the ONLY answer with a
//     fallback: every other refusal is the service's decision and asking again
//     under another vocabulary cannot answer differently (contract.md).
//
// G9 is why the version is not simply 2: version 2 rests on D2313 (a
// repository's own copy is the one no run reads), so while
// config.RepoSecretsOff is false -- the door D2313's ruling keeps, which
// reverses that -- the fetch asks as it always has and version 2 is never sent
// (F24).
//
// A CODING run's `file: true` secret is exposed as a file from here
// (secretfile.go, D3327): the value this file leaves in the runner's
// environment is the one a mount would have left, and the exposure writes it
// out and points the variable at the path. A review run fetches like any other
// and gets no file: the value it fetches never reaches its agent -- bootstrap's
// review arm registers the values for redaction and hands agent.BuildEnv an
// empty secret list, and it reads the same environment this file writes
// (runner.go's secretEnvs) -- so a file an untrusted checkout could read is
// the one thing a review run must not leave behind (F19, R5). The service's
// own class gate is what makes the fetch itself moot against a real service --
// a review-class job names no one workflow's job, so the route refuses it (G19,
// G5) -- and the run then stops exactly where it stopped before this fetch
// existed.

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
// declares whose variable the job's own environment does not carry (B3.4), and
// then exposes every `file: true` one of them as a file (secretfile.go).
//
// It is called from bootstrap before the agent's environment is built, and it
// leaves the values where a mount would have left them: in the runner's own
// environment, which is where agent.BuildEnv reads a declared secret and where
// the review arm's registration reads it too. Nothing else about the run
// changes: the variable name, the workflow that declares it and the secret it
// names in the repository's file are the same whether the value arrived in the
// job's environment or through this call.
func (r *run) fetchWorkflowSecrets(ctx context.Context, wf config.Workflow) error {
	declared := r.declaredSecrets(wf)
	var missing []config.Secret
	// A repository-level secret the job does not mount has no value anywhere
	// (D2313): asking the service for it would refuse every run of a
	// repository the ruling brings up.
	for _, s := range declared {
		if envLookup(r.d.Env, s.Env) == "" {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		if err := r.fetchMissingWorkflowSecrets(ctx, missing); err != nil {
			return err
		}
	}
	// After the fetch and before anything reads the variables: a file secret's
	// variable is the path its value was written to, whether the value came
	// from the job's mount or from the service (D3327).
	return r.exposeSecretFiles(declared)
}

// fetchMissingWorkflowSecrets fetches the declared secrets the job does not
// carry, one call each.
func (r *run) fetchMissingWorkflowSecrets(ctx context.Context, missing []config.Secret) error {
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
	for _, s := range missing {
		if err := r.fetchWorkflowSecret(cctx, svc, url, s); err != nil {
			return err
		}
	}
	return nil
}

// fetchWorkflowSecret fetches one declared secret's value and puts it where a
// mount would have put it: the runner's environment, and the redactor.
//
// Version 2 is the call, unless the door D2313 keeps is open (G9, F24); one
// answer falls back to version 1 (the file's header). The value is registered
// with the redactor BEFORE it is written to the environment, and nothing
// between the answer and that registration can print anything: from the moment
// the answer is read, every reason this run publishes is redacted of it
// (runner.go's addSecret).
func (r *run) fetchWorkflowSecret(ctx context.Context, svc *gateway.Service, url string, s config.Secret) error {
	if config.RepoSecretsOff {
		a, err := r.askWorkflowSecret(ctx, svc, url, credsvc.WorkflowSecretsRequest{
			Name: s.Name, Version: credsvc.WorkflowSecretsVersion2}, s.Env)
		if err != nil {
			return err
		}
		switch a.code {
		case "":
			r.keepWorkflowSecret(s.Env, a.value)
			return nil
		case credsvc.ErrorWorkflowSecretNotAllowed:
			// The record's answer for a name no repository is allowed is the
			// same one it gives a name it does not hold yet, and the second is
			// the state every repository S3 already serves is in until update
			// copies its names (D3325, D3328): the question is asked again in
			// the vocabulary that record is written in.
		default:
			return refusedWorkflowSecret(s.Env, a.status, a.code)
		}
	}
	a, err := r.askWorkflowSecret(ctx, svc, url, credsvc.WorkflowSecretsRequest{Env: s.Env}, s.Env)
	if err != nil {
		return err
	}
	if a.code != "" {
		return refusedWorkflowSecret(s.Env, a.status, a.code)
	}
	r.keepWorkflowSecret(s.Env, a.value)
	return nil
}

// workflowSecretAnswer is what one call came back with: the value, or the
// contract's refusal code and the status that carried it. A refused answer is
// not an error here -- one of them is what fetchWorkflowSecret falls back on --
// while everything that stops the run before an answer can be read is.
type workflowSecretAnswer struct {
	value  string
	status int
	code   string
}

// askWorkflowSecret sends one call and reads its answer.
//
// name is what a sentence names the secret by: the variable, which is what the
// repository's own file and every other message about it speak.
func (r *run) askWorkflowSecret(ctx context.Context, svc *gateway.Service, url string, req credsvc.WorkflowSecretsRequest, name string) (workflowSecretAnswer, error) {
	if err := req.Validate(); err != nil {
		// The contract's own rule, read by the runner as well as by the
		// service: a body this release built is one this contract carries
		// before it reaches the wire (credsvc.WorkflowSecretsRequest.Validate).
		return workflowSecretAnswer{}, fmt.Errorf("fetching workflow secret %s: %w", name, err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return workflowSecretAnswer{}, fmt.Errorf("fetching workflow secret %s: the call could not be built", name)
	}
	status, answer, err := svc.Post(ctx, url, credsvc.WorkflowSecretsPath, body)
	if err != nil {
		// Post writes its own half of the sentence and never the body it sent,
		// and the answer does not exist yet: nothing of the value can be in
		// this reason.
		return workflowSecretAnswer{}, fmt.Errorf("fetching workflow secret %s: %w", name, err)
	}
	if status != http.StatusOK {
		code := workflowSecretRefusal(status, answer)
		if code == "" {
			// A refusal whose body is not this contract's error is still a
			// refusal, and there is no code to act on: read as an answer it
			// would put an EMPTY value under the variable, the run would then
			// stop in agent.BuildEnv naming the mount rather than the service,
			// and the version-1 fallback -- which one refusal deserves -- would
			// never be asked. Which is why an answer's empty code means served
			// and nothing else.
			return workflowSecretAnswer{}, refusedWorkflowSecret(name, status, "")
		}
		return workflowSecretAnswer{status: status, code: code}, nil
	}
	var ans credsvc.WorkflowSecretsResponse
	if err := decodeWorkflowSecretAnswer(answer, &ans); err != nil {
		return workflowSecretAnswer{}, fmt.Errorf("fetching workflow secret %s: %w", name, err)
	}
	// The contract echoes what the call named so an answer can never be read as
	// another secret's (credsvc.WorkflowSecretsResponse). An answer that names
	// another one is a body this route did not write, and putting its value
	// under the variable that was asked for would be mounting a secret the call
	// never named.
	if req.VersionOf() == credsvc.WorkflowSecretsVersion2 {
		if ans.Name != req.Name {
			return workflowSecretAnswer{}, fmt.Errorf("fetching workflow secret %s: the credentials service's answer is for another secret", name)
		}
	} else if ans.Env != req.Env {
		return workflowSecretAnswer{}, fmt.Errorf("fetching workflow secret %s: the credentials service's answer is for another variable", name)
	}
	if len(ans.Value) < minSecretLen {
		// The floor the mounted path holds too (minSecretLen): a value this
		// short cannot be redacted without redacting text that is not a secret,
		// and agent.BuildEnv refuses a declared secret under the same figure.
		// Refused here rather than registered: a two-byte value in the redactor
		// would rewrite unrelated text everywhere this run publishes.
		return workflowSecretAnswer{}, fmt.Errorf("fetching workflow secret %s: the credentials service answered a value shorter than %d bytes, which cannot be redacted safely", name, minSecretLen)
	}
	return workflowSecretAnswer{value: ans.Value}, nil
}

// keepWorkflowSecret leaves a fetched value where a mount would have left it,
// and registers it with the redactor first: the environment write is the first
// place it exists outside this call.
func (r *run) keepWorkflowSecret(name, value string) {
	r.addSecret(value)
	r.d.Env = append(r.d.Env, name+"="+value)
}

// refusedWorkflowSecret is the whole sentence for an answer that refused the
// call: this file's words, the status, and the contract's own code.
//
// Only the code is read and the message beside it is dropped: it is the
// service's text, written for an operator's log, and a run's reason is not the
// place for another writer's sentence (reviewartifact.go's reviewRefusalCode,
// which reads the sign route's refusals the same way).
func refusedWorkflowSecret(name string, status int, code string) error {
	s := fmt.Sprintf("fetching workflow secret %s: the installation's credentials service answered %d", name, status)
	if code != "" {
		s += ", refused: " + code
	}
	return errors.New(s)
}

// workflowSecretRefusal is the contract's own code for a refused answer, or ""
// when the body carries none this contract could have written. The call is a
// non-2xx answer, which is what credsvc.ReadWireError reads.
func workflowSecretRefusal(status int, answer []byte) string {
	e, err := credsvc.ReadWireError(status, answer)
	if err != nil || !reviewServiceErrorCode.MatchString(e.Error) {
		return ""
	}
	return e.Error
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
