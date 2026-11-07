package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The workspace record's publish, as a stage of `fugaro update`: the workspace
// OWNER's installation writes `workspaces/<name>` in the workspace's own
// database from the record its own config declares (docs/workspaces/spec.md §2,
// D1292), migrating a node an older release wrote in place rather than asking
// the owner to publish again (the owner's rule of 2026-10-30: `fugaro update`
// is the whole upgrade, so a release must not ship an operator step that is
// another command, and the reconciliation's D1292 refused a `fugaro workspace
// publish` on exactly that ground).
//
// It is the policy stage's pattern (init_policy_migrate.go) in another
// database, and the parallel is deliberate rather than incidental: one read of
// the published node per run, the write conditional on the ETag of the node the
// operator read the diff against, the shared ownership predicate
// (budgetDBUnadopted) asked BEFORE the read so a database the installation
// never adopted is skipped rather than migrated into, and a migration that is
// refused rather than a delete and a republish.
//
// Three things about this stage are its own, and they are the whole of what the
// membership design says:
//
//   - **It is the workspace OWNER's, and a member's run of the same command
//     does nothing here.** A member holds no grant on the workspace's project
//     at all (spec §5), so its `fugaro update` must not even read there — the
//     stage decides that from the config alone, before any database is opened,
//     and a member's run makes zero requests to any database for it
//     (TestUpdateOnAMemberNeverWritesTheWorkspaceNode). The record a run
//     publishes is the one its OWN config declares: `workspace_members` and
//     `workspace_cap`, which are the keys of the project the workspace is named
//     after and never a member's (`workspace:` and `workspace_pin:` are the
//     member's half, W1).
//   - **Membership needs both sides, and neither half wins.** The record is
//     built from the owner's table exactly as the table states it, and the
//     half-done memberships this machine can see are REPORTED beside the diff
//     rather than resolved (workspaceRow.problems, W1's own sentences: this
//     file words no second copy of them). A run that admitted a member on the
//     config's word alone would be a run that published a claim about a machine
//     the owner does not control.
//   - **The node carries the member list and the cap and nothing else** (spec
//     §2, SEC): no issuer, no assertion key, no service account. Every one of
//     those is the member's own pin in the member's own config, because a node
//     that could supply one would be a node that could aim every member's
//     exchange and every member's grant at a party of its choosing — the one
//     direction rule 1 does not survive. The node's shape enforces it
//     (projectpolicy.WorkspaceNode) and the test holds it, key by key.

// workspacePublication is one read of the published node and the node it would
// be written as: the plan the operator is shown, and the write itself.
// Everything the write needs is read once, here, because the ETag the write is
// conditional on is the ETag of the node the operator read the diff against.
type workspacePublication struct {
	lc     *localcfg.Config
	db     *rtdb.Client
	path   string
	record *workspace.Record

	etag  string // the published node's ETag, as read; the write is conditional on it
	found bool   // whether a node is published at all

	// from is the published node's version, read off the document: 0 for a node
	// in the record's own file shape (projectpolicy.migrateWorkspaceV0ToV1) and
	// for the first publish, where there is nothing to name.
	from int
	// node is the published node as it stands, decoded: nil when there is none.
	// It is what the diff is a diff OF, and what the widening check reads.
	node *projectpolicy.Workspace
	// changes are the migration steps this run would take, empty for a node
	// already at this Fugaro's version.
	changes []projectpolicy.WorkspaceChange
	// body is the document this run writes.
	body []byte
	// done is a node that already carries what this installation declares, at
	// this Fugaro's version: there is nothing to do, and nothing is written.
	done bool
	// widens names what this write adds to a group the workspace ALREADY
	// publishes — a member, a repository, a ceiling where there was none or a
	// higher one. It is the confirmation's input and not the write's: a widen is
	// what the owner's own typed word is reserved for, and a first publish has
	// nothing to widen.
	widens []string
	// drift are the half-done memberships this machine can see (W1's own
	// sentences). They are reported and never resolved: neither half of a
	// membership is this stage's to complete.
	drift []string
}

// workspacePublishNotOurs is the answer the shared ownership predicate gives for
// a database the installation never adopted: there is nothing to publish HERE,
// and the stage is skipped with the predicate's own sentence rather than
// failing the run (the policy stage's policyMigrationNotOurs, for the same
// reason: the rest of the run has nothing to do with that database).
type workspacePublishNotOurs struct{ reason string }

func (e *workspacePublishNotOurs) Error() string { return e.reason }

// workspaceNoDBReason is the skip for a config that declares a workspace and
// names no budget backend: the record lives in a Realtime Database, so there is
// nowhere to publish it. It is a skip and not a refusal, like the policy
// stage's own (policyNodeUnreachable): a config written before the budget
// backend is a state `fugaro update` must not stop on.
const workspaceNoDBReason = "the project's config names no Firebase budget backend, so there is no database to publish the workspace record in"

// workspaceRecordOf builds the record this installation publishes. It answers
// nil for a config that is not a workspace's own — one that declares no member
// table and no cap, which is every member's config and every project in no
// workspace — and that answer is what keeps a member's run from reaching for a
// database it holds no grant on at all.
//
// The two lists are SORTED here rather than required to be sorted in the file:
// yamledit appends where `fugaro workspace member add` writes, so the file's
// order is the order the owner's edits happened in and not an order anyone
// chose (internal/localcfg says so beside the table). The record that is
// published is sorted, which is what makes two spellings of one member list one
// node rather than two documents that compare unequal.
//
// A record that lists no member is refused, and that is W0's own note made a
// refusal: a published node listing none is a workspace that admits nobody, and
// it is published into a database the workspace's own service rolls up and
// admits against — so the state is spelled out to the owner with the local edit
// that fixes it, rather than written and discovered from the other side.
func workspaceRecordOf(lc *localcfg.Config) (*workspace.Record, error) {
	if lc == nil || (lc.WorkspaceMembers == nil && lc.WorkspaceCap == nil) {
		return nil, nil
	}
	table := workspaceTable(lc)
	r := &workspace.Record{Name: lc.Name, Members: make([]workspace.Member, 0, len(table))}
	for _, project := range slices.Sorted(maps.Keys(table)) {
		repos := slices.Clone(table[project].Repos)
		slices.Sort(repos)
		r.Members = append(r.Members, workspace.Member{Project: project, Repos: repos})
	}
	if lc.WorkspaceCap != nil {
		monthly, err := pricing.FromUSD(lc.WorkspaceCap.Monthly)
		if err != nil {
			return nil, userErr("workspace_cap.monthly: %v", err)
		}
		r.Cap = workspace.Cap{Monthly: monthly, Set: true}
	}
	if len(r.Members) == 0 {
		return nil, userErr("the workspace %s declares no member, and a record that lists none is a workspace that admits nobody: nothing is published; declare the projects the workspace groups with 'fugaro workspace member add %s <project>' in this project's config, then run %s",
			lc.Name, lc.Name, selfCommand()+" update")
	}
	if err := r.Validate(); err != nil {
		return nil, userErr("the workspace record this installation declares could not be published: %v", err)
	}
	return r, nil
}

// readWorkspaceNode reads one workspace's node with its ETag, the precondition
// the write is made under. It is readPolicyNode for another path: the node is
// the workspace's own, at `workspaces/<name>` in the workspace's own database,
// and the ETag is what makes the write conditional on the document the operator
// read.
func readWorkspaceNode(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, name string) (etag string, found bool, raw json.RawMessage, err error) {
	etag, found, err = db.GetETag(ctx, projectpolicy.PathWorkspace(name), &raw)
	if err != nil {
		return "", false, nil, dbErr(lc, err, false)
	}
	return etag, found, raw, nil
}

// workspacePublicationFor reads the state and works out what this run would
// write. A nil publication with a nil error is a config that publishes no
// workspace record at all, which is a skip and not a failure.
//
// The order is the policy stage's, and it is the order that matters: the record
// is built from the config FIRST (so a workspace with no member is refused
// before any database is opened), then the ownership gate, then the read. A
// stage that read the node first would reach into a database the installation
// never adopted just to find out it was not allowed to write there.
func workspacePublicationFor(ctx context.Context, lc *localcfg.Config) (*workspacePublication, error) {
	rec, err := workspaceRecordOf(lc)
	if err != nil || rec == nil {
		return nil, err
	}
	if lc.Budget == nil || lc.Budget.RTDBURL == "" {
		return nil, &workspacePublishNotOurs{reason: workspaceNoDBReason}
	}
	lc, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, err
	}
	if why := budgetDBUnadopted(ctx, lc, infra.NewDB(db, lc.Name, lc.GCPProject, ""), "this stage publishes no workspace record there", selfCommand()+" update"); why != "" {
		return nil, &workspacePublishNotOurs{reason: why}
	}
	return workspacePublicationIn(ctx, lc, db, rec)
}

// workspacePublicationIn is workspacePublicationFor's second half, past the
// gate: the read, the migration and the document this run would write.
func workspacePublicationIn(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, rec *workspace.Record) (*workspacePublication, error) {
	p := &workspacePublication{lc: lc, db: db, path: projectpolicy.PathWorkspace(rec.Name), record: rec}
	etag, found, raw, err := readWorkspaceNode(ctx, db, lc, rec.Name)
	if err != nil {
		return nil, err
	}
	p.etag, p.found = etag, found
	// MigrateWorkspace is asked only of a node that is there: an absent one is
	// the first publish, and a migration of nothing is not a state this Fugaro
	// has an answer for (MigrateWorkspace refuses an empty document, which is
	// its contract for a caller that hands it one).
	body := raw
	if found {
		body, p.changes, err = projectpolicy.MigrateWorkspace(raw)
		if err != nil {
			return nil, workspaceRefusal(p.path, err)
		}
	}
	// The version the diff names is read off the document rather than off the
	// migration: a run that only publishes has no change to read it from, and a
	// node in the record's own file shape has no version key at all and is
	// version 0.
	var head struct {
		Version int `json:"version"`
	}
	_ = json.Unmarshal(raw, &head)
	p.from = head.Version
	// The decode is the tolerant one (UnknownKeys instead of a refusal), and the
	// refusal is made below: a node carrying top-level keys this Fugaro does not
	// model is read here and refused at the write, by the one sentence that
	// names them, rather than by a sentence the migration half never reaches.
	node, _, err := projectpolicy.DecodeWorkspace(body)
	if err != nil {
		return nil, workspaceRefusal(p.path, err)
	}
	p.node = node
	p.drift, err = workspaceDrift(rec.Name)
	if err != nil {
		return nil, err
	}
	if found && len(p.changes) == 0 && workspaceNodeMatches(node, rec) {
		// The node already carries what this installation declares, at this
		// Fugaro's version. Nothing is written and nothing is asked: a stage of
		// `fugaro update` that rewrote it every run would be a write per run
		// against a document that is already right.
		p.done = true
		p.body = nil
		return p, nil
	}
	if found && len(node.UnknownKeys) > 0 {
		// P8 at this stage's write, and the same reading the policy stage takes:
		// this is a WRITER, and rewriting a node from the record would drop the
		// keys it cannot read. Tolerating them in the reader is what keeps a
		// newer node readable; it is not a licence to write one this binary does
		// not understand, and the count is the whole of what the refusal says
		// about them (a node may hold anything under those keys).
		return nil, userErr("%s carries %d key(s) this Fugaro does not know (%s), and publishing the record from here would write a document without them: nothing was written; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run %s again",
			p.path, len(node.UnknownKeys), strings.Join(node.UnknownKeys, ", "), selfCommand()+" update")
	}
	written, err := projectpolicy.WorkspaceNode(rec)
	if err != nil {
		return nil, userErr("building the workspace record for %s: %v", rec.Name, err)
	}
	p.body, p.widens = written, workspaceWidening(rec, node, found)
	return p, nil
}

// workspaceWidening names what this write adds to a group the workspace already
// publishes. It is empty for a first publish, which has nothing to widen: the
// record is this installation's own declaration and the operator's `--yes` or
// typed word is the confirmation every other write of the run takes. What is
// reserved for the typed name is a write that puts MORE in a group that is
// already there — a project that can now spend against the workspace's
// credentials, a repository it now covers, a ceiling higher than the one the
// owner set — which is the policy stage's loosening rule (D846) read on this
// node.
//
// The comparison is against the published node and not the local file: a member
// whose own config still lists a repository the node does not carry is a
// widening this stage would make, whatever the file's history was.
func workspaceWidening(rec *workspace.Record, node *projectpolicy.Workspace, found bool) []string {
	if !found || node == nil {
		return nil
	}
	published := map[string][]string{}
	for _, m := range node.Members {
		published[m.Project] = m.Repos
	}
	var out []string
	for _, m := range rec.Members {
		repos, listed := published[m.Project]
		if !listed {
			out = append(out, fmt.Sprintf("%s is admitted to the workspace", m.Project))
			continue
		}
		for _, repo := range m.Repos {
			if !slices.Contains(repos, repo) {
				out = append(out, fmt.Sprintf("%s contributes %s", m.Project, repo))
			}
		}
	}
	if rec.Cap.Set {
		switch {
		case node.Cap == nil:
			out = append(out, fmt.Sprintf("the workspace's own ceiling becomes %s a month", money(rec.Cap.Monthly)))
		case rec.Cap.Monthly > node.Cap.MonthlyMicros:
			out = append(out, fmt.Sprintf("the workspace's own ceiling rises from %s to %s a month", money(node.Cap.MonthlyMicros), money(rec.Cap.Monthly)))
		}
	}
	return out
}

// workspaceNodeMatches reports whether a published node carries what a record
// states: the version, the name, the member list and its repositories in order,
// and the cap. It is a comparison of VALUES and not of bytes, because the
// database round-trips the document through its own encoding — a byte
// comparison would report a difference on every run for a node that says
// exactly what the record says, and the stage would rewrite it forever.
func workspaceNodeMatches(node *projectpolicy.Workspace, rec *workspace.Record) bool {
	if node == nil || node.Version != projectpolicy.WorkspaceVersion || node.Name != rec.Name {
		return false
	}
	if len(node.Members) != len(rec.Members) {
		return false
	}
	for i, m := range rec.Members {
		got := node.Members[i]
		if got.Project != m.Project || !slices.Equal(got.Repos, m.Repos) {
			return false
		}
	}
	switch {
	case !rec.Cap.Set:
		return node.Cap == nil
	case node.Cap == nil:
		return false
	}
	return node.Cap.MonthlyMicros == rec.Cap.Monthly
}

// workspaceDrift is the two-sided membership check as far as this machine's own
// files can make it: the members the workspace's table declares whose own
// config here names no workspace, and the configs here that name this workspace
// whose table does not declare them (workspaceRow.problems, W1). The sentences
// are W1's, verbatim and not a second wording of them: `fugaro workspace show`
// reports the same state, and two wordings of one state is two things an
// operator has to read as one.
func workspaceDrift(name string) ([]string, error) {
	m, err := workspaceReadMachine()
	if err != nil {
		return nil, err
	}
	row, ok := m.known(name)
	if !ok {
		return nil, nil
	}
	return row.problems(m), nil
}

// workspaceRefusal is MigrateWorkspace's own refusal with the remedy that fits
// this side: a node written by a newer Fugaro is refused with the sentence that
// names the move (upgrade the binary, then the one command), and everything
// else — a document that is not a record, a top-level key written twice, a
// member that is not a project name — is the migration's own sentence, which
// names what it could not read.
//
// Neither names a second command: the node is the owner's own document in a
// database only this installation can write, so the move is always the one
// command, run again from a Fugaro that understands it.
func workspaceRefusal(path string, err error) error {
	var ve *projectpolicy.WorkspaceVersionError
	if errors.As(err, &ve) {
		return userErr("the workspace record at %s is version %d and this Fugaro writes version %d: it was written by a newer Fugaro, so nothing was written; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run %s again",
			path, ve.Found, ve.Understood, selfCommand()+" update")
	}
	return userErr("the workspace record at %s could not be published: %v; nothing was written", path, err)
}

// workspaceStage is the stage itself. It is SelfConfirming, like the policy and
// layer stages: it shows its own diff, takes its own confirmation and refuses a
// coding agent's session.
//
// It is NOT a stage of the managed gate (gatedStages, update_managed.go), and
// that is a decision rather than an omission: what this write decides is who is
// in the group — the member list the workspace's own service rolls up and
// admits against — and that is the owner's own word about their own group. A
// mark's admission standing in for it would let a document nobody read admit a
// member, which is the one thing rule 1 forbids (SEC). It is not in runCovered
// either, for the policy stage's reason: it takes its own confirmation.
type workspaceStage struct {
	e       *initEngine
	planned bool
	p       *workspacePublication
	err     error
}

func newWorkspaceStage(e *initEngine) *workspaceStage { return &workspaceStage{e: e} }

func (workspaceStage) Name() string    { return initflow.Workspace }
func (workspaceStage) SelfConfirming() {}

func (workspaceStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Workspace, Kind: initflow.LeftCommand, Text: selfCommand() + " update"}
}

// plan reads the node once per run: the check, the plan and the apply all ask
// the same question, and the answer is what the confirmation is made against.
func (s *workspaceStage) plan(ctx context.Context) (*workspacePublication, error) {
	if !s.planned {
		s.planned = true
		s.p, s.err = workspacePublicationFor(ctx, s.e.lc)
	}
	return s.p, s.err
}

// notOurs is the skip both Check and Plan answer with: the state the stage is
// not this run's to act on, in the sentence that names why.
func workspaceSkip(err error) (string, bool) {
	var notOurs *workspacePublishNotOurs
	if errors.As(err, &notOurs) {
		return notOurs.reason, true
	}
	return "", false
}

// Check reports what the published node is. A refusal here is the run's: a
// document this Fugaro cannot read is not something the operator finishes by
// hand, and the message says which of the two it is.
func (s *workspaceStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := s.plan(ctx)
	if reason, skip := workspaceSkip(err); skip {
		return initflow.Status{State: initflow.Skipped, Detail: reason}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	if p == nil {
		return initflow.Status{State: initflow.Skipped, Detail: workspaceNoRecordReason(s.e.lc)}, nil
	}
	switch {
	case p.done:
		return initflow.Status{State: initflow.Done, Detail: fmt.Sprintf("the published workspace record at %s is version %d and carries what this installation declares", p.path, projectpolicy.WorkspaceVersion)}, nil
	case !p.found:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("no workspace record is published at %s: this run publishes the one this installation declares (%s), after your confirmation", p.path, p.recordSummary())}, nil
	case len(p.changes) > 0:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published workspace record at %s is version %d and this Fugaro writes version %d", p.path, p.from, projectpolicy.WorkspaceVersion)}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the local config states what the published workspace record at %s does not carry: this run publishes it, after your confirmation", p.path)}, nil
}

// workspaceNoRecordReason is the skip for a project that declares no workspace:
// not a member, not a workspace's own, and not a state to act on.
func workspaceNoRecordReason(lc *localcfg.Config) string {
	project := "this project"
	if lc != nil {
		project = "the project " + lc.Name
	}
	return fmt.Sprintf("%s declares no workspace: it has no workspace_members table and no workspace_cap, so it publishes no workspace record — a member's config names the workspace it joined (workspace: and workspace_pin:) and never publishes one", project)
}

// recordSummary names what the record this run would publish holds, for the
// check line: the members and the cap, in the words the node carries them in.
func (p *workspacePublication) recordSummary() string {
	names := make([]string, 0, len(p.record.Members))
	for _, m := range p.record.Members {
		names = append(names, m.Project)
	}
	s := "members " + strings.Join(names, ", ")
	if p.record.Cap.Set {
		s += ", cap " + money(p.record.Cap.Monthly) + " a month"
	} else {
		s += ", no cap of its own"
	}
	return s
}

// Plan is what the run says it is about to do. The diff and the drift are
// printed where a person can read them — a terminal, or --yes, which is a
// person's word that they have read them — because the confirmation this stage
// takes is made against both.
func (s *workspaceStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	p, err := s.plan(ctx)
	if reason, skip := workspaceSkip(err); skip {
		return initflow.Plan{NothingToDo: true, Detail: reason}, nil
	}
	if err != nil {
		return initflow.Plan{}, err
	}
	if p == nil {
		return initflow.Plan{NothingToDo: true, Detail: workspaceNoRecordReason(s.e.lc)}, nil
	}
	if p.done {
		return initflow.Plan{NothingToDo: true, Detail: fmt.Sprintf("the published workspace record at %s carries what this installation declares", p.path)}, nil
	}
	if env.Interactive || env.Yes {
		p.print(s.e.r.cmd.OutOrStdout())
	}
	return initflow.Plan{Detail: p.planDetail()}, nil
}

// planDetail is what the stage says it is about to do, in the wording that fits
// this run's plan.
func (p *workspacePublication) planDetail() string {
	switch {
	case !p.found:
		return fmt.Sprintf("publish the workspace record this installation declares at %s, after your confirmation", p.path)
	case len(p.changes) > 0:
		return fmt.Sprintf("migrate the published workspace record at %s from version %d to %d in place, after your confirmation", p.path, p.from, projectpolicy.WorkspaceVersion)
	}
	return fmt.Sprintf("publish the workspace record this installation declares at %s, after your confirmation", p.path)
}

// Apply is the write. A coding agent's session never reaches it: the record
// decides who is in the group, and that is a cloud write like any other.
func (s *workspaceStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
		return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
	}
	p, err := s.plan(ctx)
	if reason, skip := workspaceSkip(err); skip {
		return initflow.Outcome{Detail: reason}, nil
	}
	if err != nil {
		return initflow.Outcome{}, err
	}
	if p == nil {
		return initflow.Outcome{Detail: workspaceNoRecordReason(s.e.lc)}, nil
	}
	if p.done {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	cmd := s.e.r.cmd
	if err := p.confirm(cmd, env.Yes, cmd.OutOrStdout()); err != nil {
		return initflow.Outcome{}, err
	}
	if err := p.write(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	return initflow.Outcome{Changed: true, Detail: p.doneDetail()}, nil
}

// confirm is the confirmation this write is made behind: the typed project name
// when the write puts more in a group the workspace already publishes, and the
// ordinary y/N (or --yes) otherwise.
//
// The typed name is the policy stage's loosening rule (confirmTypedStrict, D846)
// read on this node: what it reserves is a write that ADMITS something — a
// project that can now spend against the workspace's credentials, a repository
// it now covers, a ceiling above the one the owner set — and --yes does not
// stand in for it, which is what the sentence says to the operator whose script
// has just passed --yes and been refused.
func (p *workspacePublication) confirm(cmd *cobra.Command, yes bool, w io.Writer) error {
	p.print(w)
	what := p.what()
	if len(p.widens) == 0 {
		return confirmRemoval(cmd, p.lc, yes, what)
	}
	why := fmt.Sprintf("this admits more to the workspace %s than the published record does: %s; every run of a member project reads the record, and what it admits is paid for by the workspace's own credentials", p.record.Name, strings.Join(p.widens, "; "))
	// The sentence is said once: a session with no terminal is refused with it
	// inside the error (confirmTypedStrict), and a door that printed it beside
	// that error would say the same thing twice.
	if stdinIsTerminal(cmd.InOrStdin()) {
		fmt.Fprintf(w, "%s\n", why)
	}
	return confirmTypedStrict(cmd, p.lc, why, what)
}

// what names this write as the confirmation reads it.
func (p *workspacePublication) what() string {
	if len(p.changes) > 0 {
		return fmt.Sprintf("this migrates the published workspace record at %s from version %d to %d in place, from the record this installation declares: the workspace's own service reads it for the member list it rolls up and admits against", p.path, p.from, projectpolicy.WorkspaceVersion)
	}
	return fmt.Sprintf("this publishes the workspace record at %s from the record this installation declares: the workspace's own service reads it for the member list it rolls up and admits against", p.path)
}

// doneDetail is the line the run reports the write with.
func (p *workspacePublication) doneDetail() string {
	if len(p.changes) > 0 {
		return fmt.Sprintf("migrated the published workspace record at %s from version %d to %d in place", p.path, p.from, projectpolicy.WorkspaceVersion)
	}
	return fmt.Sprintf("published the workspace record this installation declares at %s", p.path)
}

// print is the one confirmation diff: what the record states, what it changes
// against the node that is published now, what the migration moved, and the
// half-done memberships this machine can see.
func (p *workspacePublication) print(w io.Writer) {
	if !p.found {
		fmt.Fprintf(w, "%s: no record is published yet; this writes the first one for the workspace %s (%s)\n", p.path, p.record.Name, p.recordSummary())
	} else {
		fmt.Fprintf(w, "%s: published (version %d) and rewritten in place as version %d; nothing is removed\n", p.path, p.from, projectpolicy.WorkspaceVersion)
		for _, ch := range p.changes {
			fmt.Fprintf(w, "  %d to %d: %s\n", ch.From, ch.To, ch.Changed)
			if len(ch.Kept) > 0 {
				fmt.Fprintf(w, "    kept as it is: %s\n", strings.Join(ch.Kept, ", "))
			}
		}
		fmt.Fprintf(w, "  this installation declares: %s\n", p.recordSummary())
		if !p.done {
			// The member list is the whole of what this write ADMITS, so the
			// members it adds are named rather than left to be diffed by eye
			// against the node above.
			for _, add := range p.widens {
				fmt.Fprintf(w, "  added: %s\n", add)
			}
		}
	}
	for _, d := range p.drift {
		fmt.Fprintf(w, "  %s\n", d)
	}
}

// write puts the record back with the ETag it was read at. A concurrent writer —
// another `fugaro update` on the owner's other machine, a hand edit through the
// console — makes the precondition fail, and that is a refusal and not a retry:
// the operator confirmed a diff of the node this write replaces, and a write
// that suppressed a change nobody read would be exactly the unasked rewrite the
// one command must not make.
func (p *workspacePublication) write(ctx context.Context) error {
	if err := p.db.PutIfMatch(ctx, p.path, p.etag, json.RawMessage(p.body)); err != nil {
		if errors.Is(err, rtdb.ErrPrecondition) {
			return userErr("the workspace record at %s changed while this publish was being confirmed (another machine published it meanwhile), so nothing was written: run %s again to read what it now holds",
				p.path, selfCommand()+" update")
		}
		return dbErr(p.lc, err, true)
	}
	return nil
}

// Verify reads the node back: a write that claims a record it did not land is a
// node this Fugaro would keep republishing on every run.
//
// The comparison is the same value comparison the plan's "nothing to do" is
// (workspaceNodeMatches), and that is the point: the next run's idle answer and
// this run's success are one question about one document, so a landed write
// cannot be reported as done here and as a change there.
func (s *workspaceStage) Verify(ctx context.Context) error {
	if s.p == nil || s.p.done {
		// The run published nothing: either this installation declares no
		// workspace, or the node already carries what it declares. There is
		// nothing of this run's to read back.
		return nil
	}
	_, found, raw, err := readWorkspaceNode(ctx, s.p.db, s.p.lc, s.p.record.Name)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("the published workspace record is gone")
	}
	node, _, err := projectpolicy.DecodeWorkspace(raw)
	if err != nil {
		return fmt.Errorf("the workspace record was written but cannot be read back: %w", err)
	}
	if !workspaceNodeMatches(node, s.p.record) {
		return fmt.Errorf("the workspace record was written but %s does not carry what this installation declares", s.p.path)
	}
	return nil
}
