package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// writeWorkflowSecrets records, for the repository this run applied, the
// workflow secrets each of its workflows' jobs mounts and the container each
// one is read from (creds.WorkflowSecrets, D2073). It is the answer the
// credentials service cannot compute for itself: which container a name
// resolves to is Readiness's (the repository's own copy when it has an enabled
// version, the project-wide one when the operator's local allow-list admits
// this repository to one), and the service holds no local config to read it
// from (F33, D1262). The service serves a workflow-secrets call from this
// record (B3.2) and a build call's build secrets from it (D2077 (a), B4.3).
//
// It is written where the mounts are decided and not at the end of the
// converge, and that is the whole reason this step is reached from the repo
// engine rather than from the converge's outcome hook beside writeAdminList:
// `ready` is the spec Readiness produced, the one the tfvars the plan was made
// from were written out of, and RepoSpec.MountSecretID — the ONE place that
// answers "which container does this repository's job mount for this logical
// name" — reads that decision back rather than taking it again. A step that
// resolved the names itself from live state (infra.ResolveSecretID) would be a
// second spelling of the precedence the Terraform merge spells, and could point
// the service at a container the plan never mounted. Both doors that apply a
// repository's root reach it: `fugaro update --repo` and the converge's
// repository stage.
//
// Nothing here stops an upgrade, and nothing here fails one. Every refusal is a
// warning and the run goes on: a repository whose record was not written is
// served nothing by the service, which is the fail-closed direction G19 asks
// for, and never a reason to leave an installation half converged (CLAUDE.md's
// HARD RULE). An installation that ran the release behind has no record at all
// — the node is new with this one — so the first `fugaro update` after it
// writes one, with no other step and nothing asked of the person.
//
// A coding agent's session needs no gate of its own here, and what keeps this
// step out of one is the plan-only gate above rather than a second reading of
// the environment: a session applies no engine at all (initflow's loop plans
// an engine stage read-only and leaves what it found to the person), so the
// repository stage reaches this engine with planOnly set, and a plan changes
// nothing outside this machine — which is the whole of what `--yes`'s help
// promises there. The managed project's gate is the one case in which a
// session IS the run that converges the installation (D1016): the loop applies
// the stage, planOnly is clear, and the record is part of what it converges.
//
// What the record holds is why leaving it to that is enough rather than
// lucky: every container it names is one the repository's job already mounts
// and the run already reads from its own environment (F4), so publishing it
// widens nobody's reach — the route that serves it is bound to the verified
// job account of the repository AND the workflow (G19) — and every ID in it is
// derived from the repository's own config and the operator's allow-list
// rather than chosen by the caller.
func (r *initRun) writeWorkflowSecrets(ctx context.Context, lc *localcfg.Config, ready infra.RepoSpec, cfg *config.Config) {
	// The door first: this is the upgrade's step, and `fugaro setup --repo`
	// onboards a repository without converging it. --plan-only changes nothing
	// outside this machine and so writes nothing, exactly as its help says.
	if r.o.doorName() != updateDoor.name || r.o.planOnly {
		return
	}
	// The record is a node of the installation's budget database, reached over
	// IAM: a run that never opened the cloud, or an installation with no shared
	// backend at all, has none to write to. The service that reads it needs the
	// same database, so there is nothing to record for such an installation —
	// the line about it is doctor's (policy-published), and `fugaro update
	// --firebase-project`, the command that gives it a backend, is where this
	// step next runs. A spec with no slug would build no path (creds refuses an
	// empty segment), and a spec always has one: task.Slug answered it earlier
	// in this engine.
	if lc == nil || lc.Budget == nil || lc.Budget.RTDBURL == "" || ready.Slug == "" {
		return
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		r.warn(fmt.Sprintf("the workflow secrets record was not written: the database the service reads it from could not be opened (%s), so the service serves this repository's workflows nothing", safetext.FieldErr(err)))
		return
	}
	// The one ownership question every reader and writer of that database asks
	// (budgetDBUnadopted), before anything is written into it: a database that
	// is not this installation's is not where this installation's service
	// reads, and a record written there would name this project's containers to
	// another installation's service — the one write this step must not make.
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "the workflow secrets record was not written", selfCommand()+" update"); why != "" {
		r.warn(why)
		return
	}
	rec := workflowSecretsRecord(ready, cfg)
	if err := creds.PutWorkflowSecrets(ctx, db, ready.Slug, rec); err != nil {
		r.warn(fmt.Sprintf("the workflow secrets record was not written: %s, so the service serves this repository's workflows nothing", safetext.FieldErr(err)))
		return
	}
	n := 0
	for _, rows := range rec.Workflows {
		n += len(rows)
	}
	fmt.Fprintf(r.w, "workflow secrets: %d recorded for %s at %s\n", n, ready.Name, creds.PathWorkflowSecrets(ready.Slug))
}

// workflowSecretsRecord is the record for the mounts ready holds: every
// workflow of cfg, its own `secrets:` (config.Workflow.Secrets), and for each
// one the container the job mounts for that logical name.
//
// The names are the workflow's own and not every mount the job carries, which
// is the set S3 is about (D491, D1267): the git credential is the build
// route's own item (B4.3, R5) and a model credential is the cascade's, served
// on the route that already exists for it (B2.x) rather than as a workflow
// secret. The workflow's build secrets ARE these entries (D2077 (a)):
// WorkflowSpec.BuildSecrets is the git credential plus exactly this list, so
// the build caller reads the same record for them.
//
// A name the spec mounts no container for is left out rather than recorded
// with an empty ID: the job mounts nothing for it, so there is nothing for the
// service to read (F33 — the fetch reads the container the mount reads). The
// workflow stays out of the map when nothing is left, so a repository with no
// workflow secrets at all is recorded as the empty record and not as a
// workflow with no secrets.
func workflowSecretsRecord(ready infra.RepoSpec, cfg *config.Config) creds.WorkflowSecrets {
	rec := creds.WorkflowSecrets{Version: creds.WorkflowSecretsVersion}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		var rows []creds.WorkflowSecret
		for _, s := range cfg.Workflows[name].Secrets {
			id := ready.MountSecretID(s.Name)
			if id == "" {
				continue
			}
			rows = append(rows, creds.WorkflowSecret{Env: s.Env, ID: id})
		}
		if len(rows) > 0 {
			if rec.Workflows == nil {
				rec.Workflows = map[string][]creds.WorkflowSecret{}
			}
			rec.Workflows[name] = rows
		}
	}
	return rec
}
