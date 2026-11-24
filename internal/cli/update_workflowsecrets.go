package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/secretname"
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
// the service at a container the plan never mounted. Every door that applies a
// repository's root reaches it: `fugaro update --repo` and `fugaro setup
// --repo` are the same engine (runInitRepo → repoEngine) and the converge's
// repository stage runs that one, and which door it was decides nothing here
// — a repository whose root was applied has a deployed job mounting the
// containers this record has to name, whichever command applied it, and a
// repository onboarded by `fugaro setup --repo` alone is served by the service
// exactly as one onboarded by the converge.
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
// step out of one is the plan-only gate alone rather than a second reading of
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
	// The plan, and only the plan: this step is reached after planRepo, so
	// `ready` is an applied root — except under --plan-only, where the same
	// function planned and applied nothing and the mounts `ready` names are
	// ones no job has. --plan-only changes nothing outside this machine and so
	// writes nothing, exactly as its help says. Which door ran it is not asked
	// here: `fugaro setup --repo` applies this repository's root and deploys
	// its job, so a repository onboarded that way needs the record as much as
	// one the converge applied (the file's header).
	if r.o.planOnly {
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
//
// A repository-level name is one of those (D2313, which this record is the
// second half of — the jobs are the first): the repository's own container is
// not something a shared job mounts, so a row pointing the service at it would
// be a record of a value no fetch can read, and the ROW is what a repository
// declaring `secrets:` gets instead of the value. The name is not dropped from
// the repository's config and its container is not touched: `fugaro validate`
// and `update` say it is disabled for now (config.RepoSecretsWarnings), and the
// project-wide container an operator has opened to this repository is still
// recorded, because that one is mounted.
func workflowSecretsRecord(ready infra.RepoSpec, cfg *config.Config) creds.WorkflowSecrets {
	rec := creds.WorkflowSecrets{Version: creds.WorkflowSecretsVersion}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		var rows []creds.WorkflowSecret
		for _, s := range cfg.Workflows[name].Secrets {
			id := workflowSecretMountID(ready, s.Name)
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

// workflowSecretMountID is the container the record names for a workflow secret:
// infra.RepoSpec.MountSecretID's answer, except that a repository-level name is
// answered with the project's copy alone under D2313.
//
// Why the repository's own copy is dropped there and only there: that container
// is not one a shared job mounts, and this record is what the credentials
// service reads to fetch a value for a run — a row naming it would be a promise
// of a value no mount carries, which is the same fail-closed direction the rest
// of this step takes (G19). The project's copy is still recorded for such a
// name, because that one IS mounted: the operator's allow-list decides whether
// this repository may read it (ProjectSecretCandidate), and a name it admits is
// the project's copy and not the repository's own, whatever the workflow wrote
// under `secrets:`.
//
// The door is read here rather than at workflowSecretsRecord's loop so the two
// halves of the ruling — no mount for a repository-level name, and a warning
// about it (config.RepoSecretsWarnings) — are asked in one place each, and the
// machinery this ruling keeps stays exercised: with the door off the answer is
// MountSecretID's, exactly as it was before D2313.
func workflowSecretMountID(ready infra.RepoSpec, logical string) string {
	if config.RepoSecretsOff && config.RepoLevelSecret(logical) {
		return ready.ProjectSecrets[logical]
	}
	return ready.MountSecretID(logical)
}

// copyWorkflowSecretsToGrammar is D3325's migration: every workflow secret the
// installation holds a project copy of becomes a `fugaro_p_<name>` container,
// as the operator, so a run reads the one copy from the grammar the intake
// writes and the credentials service serves (D3323, D3324).
//
// # What it reads and what it writes
//
// The source is `gcp.ProjectSecretID`'s container alone. Under D2313 that is
// the only copy a run reads, so it is the only copy there is anything to move
// (F16), and the repository-level `creds/workflowsecrets/<slug>` record is left
// exactly as it is: version 1 still reads it until the follow-up removes the
// route, and rewriting it would change what a run of the release behind fetches
// in the middle of its own `fugaro update` (G4).
//
// The newest ENABLED version is the one copied and not the `latest` alias: an
// operator's disable moves those two apart, and the version a run can read is
// the enabled one.
//
// A name whose `fugaro_p_` container is ALREADY there with the intake's shape
// AND holds an enabled version gets no version from this step. Two states reach
// that branch and both are the same answer: a previous update copied it, or the
// owner stored the name with `fugaro secrets set` since, which is newer than
// anything the project copy holds (D3323 made that store the intake's). Which
// of the two it is does not matter — the container's own version is the one a
// run reads from now on. A container of that shape with no enabled version is
// filled, which is the one state the step writes into a container it did not
// make itself.
//
// # Never a value
//
// The value is read into memory, written, and dropped; it is redacted out of
// the one error that could carry it back (a server that echoes the payload it
// refused), and no line this step prints names it (G18). The refusal a foreign
// container gets names the container and the command that fixes it, never a
// value.
//
// # The record, and why it is asked for before anything is copied
//
// A name is recorded in the project record only once its value IS in the
// grammar, and the record is what IS6 reads to decide which mounts, grants and
// project copies go (D3325). So an installation with no database to write that
// record into copies nothing: the two halves of the migration move together or
// not at all, and a value copied with no record of it is a container nothing
// would ever retire and whose mount nothing would drop.
//
// The entry a name gets is seeded from the operator's own `workflow_secrets`
// `allow_data_to` and only when the record holds no entry for the name: an
// entry the owner wrote with `fugaro secrets allow` is left exactly as it is,
// which is the direction that can never widen a list a `deny` narrowed (F22,
// F23, G8). A repository's own `secrets:` is never read here — a declaration a
// repository controls is not a grant (F22).
//
// # Nothing here fails an upgrade
//
// Every state that leaves the installation as it is is a warning: a name whose
// container is another writer's, a value that could not be written, a record
// that could not be updated. The fail-closed direction is the one the migration
// keeps — a name that was not copied is a name no record entry names, so IS6
// leaves its mount, its grants and its project copy alone, and the next update
// copies it (CLAUDE.md's HARD RULE, G4).
//
// The one state that STOPS the step is an operator with no read on a project
// copy that is there: the roles Fugaro grants do not carry
// secretmanager.versions.access — the project's Owner does
// (docs/secret-readers.md E12) — and every remaining name would be refused the
// same way. It is one line naming the fix, after the names copied before it are
// recorded; the converge's own stages are what the owner came for, and an
// upgrade that stopped over a permission the owner can grant in one line would
// be the operator step the HARD RULE forbids. A name whose project copy is gone
// — the state after IS6 retires one — is not this state: there is nothing to
// copy for it and no read missing.
//
// A coding agent's session is refused like every other cloud write
// (agentMarker): the step writes Secret Manager containers, so it changes
// something outside this machine, and what a session gets instead is the one
// command to run at a person's own terminal.
func (e *initEngine) copyWorkflowSecretsToGrammar(ctx context.Context) {
	r, o := e.r, e.r.o
	// The door first, as in writeAdminList: this is the upgrade's step, and a
	// project `fugaro setup` just created has no copies and no operator config.
	if o.doorName() != updateDoor.name || o.planOnly {
		return
	}
	lc := e.lc
	names := workflowSecretCopyNames(lc)
	if len(names) == 0 {
		return
	}
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" && !r.gateUnderway() {
		r.warn(fmt.Sprintf("workflow secrets were not copied: a coding agent's session makes no cloud writes. Run `%s update` in your own terminal", selfCommand()))
		return
	}
	// The record first: a value copied with no record of it is a container
	// nothing retires and a mount nothing drops (the header).
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return
	}
	db, err := newBudgetClient(ctx, lc.Budget.RTDBURL, lc.Endpoints.NoAuth)
	if err != nil {
		r.warn(fmt.Sprintf("workflow secrets were not copied: the database the record goes in could not be opened (%s)", safetext.FieldErr(err)))
		return
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "the workflow secrets were not copied", selfCommand()+" update"); why != "" {
		r.warn(why)
		return
	}
	// Who made the container, which is the annotation the intake writes for a
	// project-shared one (R10) and the shape check requires. A credential that
	// cannot say who its holder is cannot make a container the intake accepts,
	// and the fix is the terminal the operator owns.
	who, err := authenticatedUser(ctx, lc)
	if err != nil {
		r.warn(fmt.Sprintf("workflow secrets were not copied: recording who made each container needs your own Google account (%s). Run `%s update` at your own terminal",
			safetext.FieldErr(err), selfCommand()))
		return
	}
	svc, err := gcp.NewSecrets(ctx, gcpOptions(lc))
	if err != nil {
		r.warn(fmt.Sprintf("workflow secrets were not copied: %s", safetext.FieldErr(err)))
		return
	}

	var copied []string
	already, skipped, refused := 0, 0, false
	for _, name := range names {
		copiedName, state, cause := copyOneWorkflowSecret(ctx, svc, lc.Name, name, who)
		if state == copyRefusedNoRead {
			// An operator with no read on the project copy at all: every
			// remaining name would be refused the same way. The loop stops, and
			// what it copied first is still recorded below — a copied value
			// with no record is a container nothing retires (D3325).
			refused = true
			break
		}
		switch state {
		case copyDone:
			copied = append(copied, copiedName)
		case copyAlready:
			already++
			copied = append(copied, copiedName)
		case copyNothing:
		default:
			skipped++
			r.warn(copySkipLine(state, name, cause))
		}
	}
	if len(copied) > 0 {
		if err := recordCopiedWorkflowSecrets(ctx, db, lc, copied); err != nil {
			r.warn(fmt.Sprintf("workflow secrets were copied but not recorded (%s), so their mounts and project copies stay until the next update", safetext.FieldErr(err)))
			return
		}
	}
	if refused {
		r.warn(workflowCopyNoReadLine(selfCommand()))
		return
	}
	fmt.Fprintf(r.w, "workflow secrets: %d copied into the %s grammar (%d already there, %d skipped)\n",
		len(copied)-already, secretname.ProjectPrefix, already, skipped)
}

// The states one name's copy ends in. copyRefusedNoRead is the one that stops
// the step, and the rest warn and go on.
type copyState int

const (
	copyFailed copyState = iota
	copyDone
	copyAlready
	copyRefusedNoRead
	// copyNothing is a name with nothing to move — no project copy at all, or
	// one with no enabled version: there is no line to print, the name is not
	// recorded, and IS6 keeps what the name still has.
	copyNothing
	// copyForeign is a name whose container is another writer's: a shape the
	// intake does not write, or no marker on it. It is the one skip that gets
	// the fix rather than the summary line.
	copyForeign
)

// copyOneWorkflowSecret moves one name's project copy into the grammar and
// answers what it did, with the cause of a failure for the one line a skipped
// name gets. The value is read, written and dropped here: nothing it returns
// carries one, and a failure's message is redacted of the bytes before it is
// handed back (a server that echoes the payload it refused).
func copyOneWorkflowSecret(ctx context.Context, svc *gcp.Secrets, fugaroProject, name, who string) (string, copyState, error) {
	src := gcp.ProjectSecretID(fugaroProject, name)
	ok, err := svc.CanReadValues(ctx, src)
	if err != nil {
		return name, copyFailed, err
	}
	if !ok {
		// CanReadValues answers false, nil for a container that is not there as
		// well as for one the operator may not read: only the second is a
		// refusal that stops the step. No container at all is the routine state
		// after IS6 retires a project copy, and a name in it has nothing to
		// copy and is not one to stop the rest for.
		_, found, err := svc.Container(ctx, src)
		if err != nil {
			return name, copyFailed, err
		}
		if !found {
			return name, copyNothing, nil
		}
		return name, copyRefusedNoRead, nil
	}
	version, err := svc.LatestEnabled(ctx, src)
	if err != nil {
		return name, copyFailed, err
	}
	if version == "" {
		return name, copyNothing, nil
	}
	dst, err := secretname.Project(name)
	if err != nil {
		return name, copyFailed, err
	}
	info, found, err := svc.Container(ctx, dst)
	if err != nil {
		return name, copyFailed, err
	}
	if found {
		// Another writer's container, or a marked one of a shape the intake
		// does not write: the step writes no version into it (G2). The check
		// is the intake's own, asked through its own function, so a container
		// this step made can never be one the next store refuses.
		if err := intake.AcceptsWorkflowWrite(dst, intakeSecretOf(info)); err != nil {
			return name, copyForeign, err
		}
		if v, err := svc.LatestEnabled(ctx, dst); err != nil {
			return name, copyFailed, err
		} else if v != "" {
			return name, copyAlready, nil
		}
	} else if err := svc.Create(ctx, dst, intakeSharedLabels(), map[string]string{intake.AnnotationCreatedBy: who}); err != nil {
		return name, copyFailed, err
	}
	value, err := svc.ReadVersion(ctx, src, version)
	if err != nil {
		return name, copyFailed, redactValue(err, value)
	}
	if _, err := svc.AddVersion(ctx, dst, value); err != nil {
		err = redactValue(err, value)
		clear(value)
		return name, copyFailed, err
	}
	clear(value)
	return name, copyDone, nil
}

// redactValue takes the copy's bytes out of a failure before it can reach a
// line a person reads: the refusal names the fact, never the value (the
// credentials-service rule). It is the one place a value and an error meet.
func redactValue(err error, value []byte) error {
	if err == nil || len(value) == 0 {
		return err
	}
	return fmt.Errorf("%s", agent.Redact(err.Error(), []string{string(value), base64.StdEncoding.EncodeToString(value)}))
}

// intakeSecretOf is one of our containers as the intake's own shape check reads
// it, so that the check the copy asks is the check the write path asks.
func intakeSecretOf(c gcp.ContainerInfo) intake.Secret {
	return intake.Secret{
		Labels: c.Labels, Annotations: c.Annotations,
		Shape: intake.Shape{TTL: c.TTL, ExpireTime: c.ExpireTime, Topics: c.Topics, CMEK: c.CMEK, Rotation: c.Rotation},
	}
}

// intakeSharedLabels is the label set the intake writes for a project-shared
// container, plus the marker that says the class of value in it is a workflow's
// own (F18, G2). It is the same set `fugaro secrets set` writes through the
// intake, spelled from the intake's own constants so the two ends can never
// drift.
func intakeSharedLabels() map[string]string {
	return map[string]string{
		intake.LabelScope:          string(intake.ScopeShared),
		intake.LabelWorkflowSecret: intake.WorkflowSecretMarker,
	}
}

// workflowSecretCopyNames is the workflow-secret names an installation holds a
// project copy of, in order: the operator's `workflow_secrets` entries that
// allow the value to somebody, which is exactly what the installation root
// plans a container for (internal/infra's projectSecretContainers). A name no
// entry admits has no container to copy and no list to seed.
func workflowSecretCopyNames(lc *localcfg.Config) []string {
	if lc == nil {
		return nil
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(lc.WorkflowSecrets)) {
		if len(lc.WorkflowSecrets[name].AllowDataTo) > 0 {
			out = append(out, name)
		}
	}
	return out
}

// copySkipLine is the one line a skipped name gets: why it was left alone and,
// for a container another writer made, the command that clears it. It never
// carries a value.
func copySkipLine(state copyState, name string, cause error) string {
	if state == copyForeign {
		return fmt.Sprintf("%s was not copied: %s%s is not a container the intake made. Remove it with `%s secrets shared rm %s`",
			name, secretname.ProjectPrefix, name, selfCommand(), name)
	}
	return fmt.Sprintf("workflow secret %s was not copied into the %s grammar (%s), so its mount and its project copy stay until the next update",
		name, secretname.ProjectPrefix, safetext.FieldErr(cause))
}

// workflowCopyNoReadLine is the refusal an operator without a read on the
// project copy is stopped with: the permission, and the one thing that gives
// it.
func workflowCopyNoReadLine(program string) string {
	return fmt.Sprintf("workflow secrets were not copied: the project copy needs secretmanager.versions.access — run `%s update` as the project's Owner", program)
}

// recordCopiedWorkflowSecrets writes the names whose value is now in the
// grammar into the project record, seeding each one's list from the operator's
// own allow_data_to and leaving every entry the record already holds exactly as
// it is (F23, G8).
func recordCopiedWorkflowSecrets(ctx context.Context, db creds.DB, lc *localcfg.Config, names []string) error {
	return creds.WriteProjectSecrets(ctx, db, func(cur creds.ProjectSecrets, found bool) (creds.ProjectSecrets, error) {
		next := cur
		for _, name := range names {
			if _, held := next.Rule(name); held {
				continue
			}
			next = next.WithRule(name, creds.ProjectSecretRule{Repos: slices.Clone(lc.WorkflowSecrets[name].AllowDataTo)})
		}
		return next, nil
	})
}
