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
	"github.com/dimipaun/fugaro/internal/pluginwire"
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
	// publishes — a member, a repository, a higher ceiling, or the ceiling
	// itself when the record states none. It is the confirmation's input and not
	// the write's: a widen is what the owner's own typed word is reserved for,
	// and a first publish has nothing to widen.
	widens []string
	// removals names what this write takes OUT of a group the workspace already
	// publishes: a member the record no longer declares, a repository of one it
	// still declares, and the cap when the record states none. The node is
	// replaced whole, so these are real changes to the document and the diff has
	// to state them: the operator confirms a diff, and a member the node stops
	// listing stops being admitted through the workspace the moment it lands.
	removals []string
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

// workspaceNoMemberReason is the skip for a config that declares a workspace and
// lists no member in it. The state is one the owner's own commands reach —
// `fugaro workspace cap set` writes the cap alone on a project that has no
// table, and `fugaro workspace member rm` of the last member leaves the table
// empty — so it is a config this program must be able to read, and the sentence
// names the local edit that fixes it rather than leaving the owner to work it
// out from a node that was not written.
//
// It is a skip and never an error, and that is the HARD RULE and not politeness:
// a Check that answers an error stops the run (initflow's loop marks the stage
// and blocks every stage after it), so a cap-only config would leave the owner
// with a `fugaro update` that fails after `brew upgrade` and a release whose
// later stages — the layer, the checkout, and everything the run registers after
// this one — never ran. Nothing is published either way: no member is a record
// that admits nobody.
func workspaceNoMemberReason(lc *localcfg.Config) string {
	return fmt.Sprintf("the workspace %s declares no member, and a record that lists none is a workspace that admits nobody: nothing is published; declare the projects the workspace groups with 'fugaro workspace member add %s <project>' in this project's config, then run %s",
		lc.Name, lc.Name, selfCommand()+" update")
}

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
// A record that lists no member is not published, and that is W0's own note: a
// published node listing none is a workspace that admits nobody, and it is
// published into a database the workspace's own service rolls up and admits
// against — so the state is spelled out to the owner with the local edit that
// fixes it, rather than written and discovered from the other side. It is the
// skip workspaceNoMemberReason gives and not an error of this function's: the
// state is this config's own file, reachable from `fugaro workspace cap set` and
// from `fugaro workspace member rm` of the last member, and a stage of `fugaro
// update` that failed the run on it would leave every stage after it blocked.
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
		return nil, &workspacePublishNotOurs{reason: workspaceNoMemberReason(lc)}
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

// workspaceConfigState is the config's own half of the read: the record this
// installation declares (nil for one that publishes none), and the skips the
// config alone decides — a record that lists no member (workspaceNoMemberReason)
// and a record with no database named to publish it in (workspaceNoDBReason).
// Both are skips and not errors, so neither can stop the run.
//
// The record's own state is asked FIRST and the database second, which is the
// order the owner's remedy is in: a member to declare is an edit of this file,
// while the database a run with --firebase-project is about to name is one this
// same run writes.
//
// It is a function of its own because the stage asks it EVERY time and caches
// only what is below it (workspaceStage.plan): the run writes this file under
// its own stages' feet, and the run that first names a budget backend
// (--firebase-project) is the run that first has a database for the record. A
// cached "nowhere to publish it" outliving that write is a sentence the config
// in force contradicts.
func workspaceConfigState(lc *localcfg.Config) (*workspace.Record, error) {
	rec, err := workspaceRecordOf(lc)
	if err != nil || rec == nil {
		return nil, err
	}
	if policyNodeUnreachable(lc) {
		return nil, &workspacePublishNotOurs{reason: workspaceNoDBReason}
	}
	return rec, nil
}

// workspacePublicationRead is the read past the config's own answers
// (workspaceConfigState): the database, the ownership gate and the node. This is
// the half the stage caches, because it is the half that reaches the cloud, and
// the half a re-pointed or newly named backend invalidates whole — which is what
// forget is for.
func workspacePublicationRead(ctx context.Context, lc *localcfg.Config, rec *workspace.Record) (*workspacePublication, error) {
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
		// about them (a node may hold anything under those keys). The names
		// themselves are the node's own, so they are printed through
		// unknownKeysText like every name of that provenance in this package: a
		// key carrying a bidi override or a separator is a legal JSON member
		// name, and printed raw it would rewrite the line of a refusal.
		return nil, userErr("%s carries %d key(s) this Fugaro does not know (%s), and publishing the record from here would write a document without them: nothing was written; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run %s again",
			p.path, len(node.UnknownKeys), unknownKeysText(node.UnknownKeys), selfCommand()+" update")
	}
	written, err := projectpolicy.WorkspaceNode(rec)
	if err != nil {
		return nil, userErr("building the workspace record for %s: %v", rec.Name, err)
	}
	p.body = written
	p.widens = workspaceWidening(rec, node, found)
	p.removals = workspaceRemovals(rec, node)
	return p, nil
}

// workspaceWidening names what this write adds to a group. What is reserved for
// the typed name is a write that puts MORE in the group — a project that can now
// spend against the workspace's credentials, a repository it now covers, a
// ceiling higher than the one the owner set — which is the policy stage's
// loosening rule (D846) read on this node, and a FIRST publish is the whole of
// that rule at once, exactly as the policy stage reads its own first node (M3):
// the document is chosen where none was, so the member list, the repositories
// each member contributes and the ceiling the workspace's own credentials are
// bounded by are all decided here for the first time, and every run of every
// member reads the record from the moment it lands.
//
// The list is empty exactly when the write adds nothing to the group: an
// ordinary rewrite of a node that already carries what the record states, a
// migration that only moves the version, and a removal — which takes something
// out of the group rather than putting something in it, and is confirmed by the
// ordinary y/N (workspaceRemovals, and the branch confirm takes at the empty
// list).
//
// That is what keeps the group's first record out of a coding agent's reach: the
// class is Typed, and D1026 admits no plan gate's answer for a Typed step. A
// hand edit of `workspace_members` in the YAML — past the fenced `fugaro
// workspace member add` — is a document this stage would otherwise publish
// behind a mark, and the node is the group's whole membership.
//
// The comparison is against the published node and not the local file: a member
// whose own config still lists a repository the node does not carry is a
// widening this stage would make, whatever the file's history was.
func workspaceWidening(rec *workspace.Record, node *projectpolicy.Workspace, found bool) []string {
	if !found || node == nil {
		return []string{"the whole group: the member list, the repositories each member contributes and the workspace's own ceiling are chosen here for the first time"}
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
	switch {
	case rec.Cap.Set && node.Cap != nil && rec.Cap.Monthly > node.Cap.MonthlyMicros:
		out = append(out, fmt.Sprintf("the workspace's own ceiling rises from %s to %s a month", money(node.Cap.MonthlyMicros), money(rec.Cap.Monthly)))
	case !rec.Cap.Set && node.Cap != nil:
		// The whole ceiling gone is the largest widening this node has: what the
		// group spends through the workspace's own credentials stops being
		// bounded at all. A ceiling where there was none is the other direction
		// and is not here — it BOUNDS the group, which is a tightening, and the
		// policy stage's own rule reads D846 the same way ("a publish that raises
		// a ceiling, removes one or widens the model list").
		out = append(out, "spend through the workspace's own credentials becomes unbounded")
	}
	return out
}

// workspaceRemovals names what this write takes OUT of a group the workspace
// already publishes. The node is replaced whole, so a member the record no
// longer declares, a repository of a member it still declares, and the cap a
// record that states none drops are all real changes to the document — and the
// diff the operator confirms is where they are said. Nothing computes them
// anywhere else: a publish that dropped a member under a diff reading "nothing
// is removed" would be the silent removal this stage must not make.
//
// A removal is not a widening — it takes away rather than admits — and it does
// not take the typed name: the owner asked for it by editing their own table
// (`fugaro workspace member rm`), which is their own word about their own group
// already. One removal is both a removal and a widening, and it is in both
// lists: the cap. It is stated here as what the node loses and in
// workspaceWidening as what the group gains, because it is one change with two
// readers — the person reading the diff, and the class rule that decides
// whether a mark may stand in for the write.
func workspaceRemovals(rec *workspace.Record, node *projectpolicy.Workspace) []string {
	if node == nil {
		return nil
	}
	declared := make(map[string][]string, len(rec.Members))
	for _, m := range rec.Members {
		declared[m.Project] = m.Repos
	}
	var out []string
	for _, m := range node.Members {
		repos, still := declared[m.Project]
		if !still {
			out = append(out, fmt.Sprintf("member %s", m.Project))
			continue
		}
		for _, repo := range m.Repos {
			if !slices.Contains(repos, repo) {
				// The repository is the node's own value and the only string in
				// this document nothing holds to a grammar: a project name is a
				// project name (config.ProjectNameRE), the cap is a number, and a
				// repository the local table contributes is held to owner/name
				// where the config is read — but a node's repositories are
				// checked only for being non-empty (DecodeWorkspace). Printed
				// raw, a repository carrying an escape or a bidi override would
				// rewrite the diff line the operator is confirming, so it goes
				// through pluginwire.Printable where every other
				// document-derived name in this package does.
				out = append(out, fmt.Sprintf("%s from %s", pluginwire.Printable(repo), m.Project))
			}
		}
	}
	if node.Cap != nil && !rec.Cap.Set {
		out = append(out, fmt.Sprintf("the workspace's own ceiling of %s a month is removed", money(node.Cap.MonthlyMicros)))
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
// It is a stage of the managed gate (gatedStages, update_managed.go), because
// the workspaces design says so: spec §6 has W2's publish of workspaces/<name>
// running "under the gate update already opens", so a managed project's coding
// agent converges it like the policy node beside it rather than stopping here
// with a step for the owner. What the gate may NOT admit are the writes this
// stage is watched for: a publish that admits something the node does not carry
// — and the workspace's first record, which admits the whole membership — is
// initflow.Typed (D846, M3), and D1026 admits no plan gate's answer for a Typed
// step — so a member is still admitted by the owner's own word and never by a
// mark (SEC, rule 1). It is not in runCovered, for the
// policy stage's reason: it takes its own confirmation where the gate does not
// stand in for one.
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

// plan is what the check, the plan and the apply all answer with: the config's
// own state and the node this run would write.
//
// Only the node is remembered. The config's half is asked every time, for the
// reason workspaceConfigState gives — the run's own local-config write changes
// it, and the stage that reads a cached skip there publishes nothing on the run
// that just named the backend. That is also why the cached half is dropped
// rather than kept when the file moves: a database this run has just been
// pointed at is not the database the old read was made in, and the client in
// that read is not the one the node lives in (forget).
func (s *workspaceStage) plan(ctx context.Context) (*workspacePublication, error) {
	rec, err := workspaceConfigState(s.e.lc)
	if err != nil || rec == nil {
		return nil, err
	}
	if !s.planned {
		s.planned = true
		s.p, s.err = workspacePublicationRead(ctx, s.e.lc, rec)
	}
	return s.p, s.err
}

// forget drops the read the check, the plan and the apply share for the rest of
// the run, so the next of them reads the config and the node again. It exists
// because a run changes under this stage's feet: the Firebase stage writes
// budget.firebase_project and budget.rtdb_url into the local config and into
// the engine's view of it (initRun.writeConfig), which is past the read-only
// preview pass where this stage's check was made — so without this the run
// prints "there is no database to publish the workspace record in" over the
// config it has just written, and the record lands on the next update instead.
// The policy stage is dropped from the same write, for the same reason and its
// own half of the config (its members and the verifier account).
func (s *workspaceStage) forget() {
	s.planned, s.p, s.err = false, nil, nil
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

// Apply is the write. A managed project's gate is asked FIRST (spec §6: W2's
// publish "runs under the gate update already opens"), and only this door has
// one: in a coding agent's session on a managed project the step that publishes
// the record is judged before it writes, and an admitted one proceeds. The
// session's own refusal is kept for every other case, which is why it moves
// below the plan rather than going away: the plan is read-only, so a session
// that is going to refuse refuses after the same reads it has always made, and
// the sentence it reads is the one it always read.
func (s *workspaceStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	gate := s.e.r.gateUnderway()
	if !gate {
		if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
			return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
		}
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
	admitted, err := s.gateStep(ctx, p)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !admitted {
		if marker := agentMarker(os.Getenv, s.e.r.stdinTerminal()); marker != "" {
			return initflow.Outcome{}, &initflow.AgentError{Marker: marker}
		}
	}
	// The diff, the confirmation and every line of the write go through the
	// run's own writer, and the question this stage asks is one of the shared
	// ones (confirmRemoval, confirmTypedStrict), which hold the command rather
	// than the run: the whole call is put to the person with the line in flight
	// down (initRun.asking), so neither the diff nor the CONFIRM block nor the
	// project's name typed under it is drawn over (G1) — the policy stage's own
	// reading of the same rule, for this command's second published document.
	cmd := s.e.r.cmd
	if err := s.e.r.asking(func() error {
		return p.confirm(cmd, env.Yes, s.e.r.w, admitted, gate, s.e.r.conditions())
	}); err != nil {
		return initflow.Outcome{}, err
	}
	if err := p.write(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	if admitted {
		// The record follows the write (D1014) and not the admission: what an
		// owner reads is that this node was published, and a step that failed
		// inside the write has published nothing to record.
		if err := s.e.r.gateApplied(ctx); err != nil {
			return initflow.Outcome{}, err
		}
	}
	return initflow.Outcome{Changed: true, Detail: p.doneDetail()}, nil
}

// confirm is the confirmation this write is made behind: the typed project name
// when the write puts more in the group — the group's first record, which
// chooses the whole of it, and any later write that admits something the node
// does not carry — and the ordinary y/N (or --yes) otherwise. A managed
// project's gate that admitted the node replaces both, and the diff is printed
// either way; of the sessions the class rule leaves a question to, a coding
// agent's was refused before this call (gateStep) and a person at a real
// terminal is answered by the note below.
//
// The typed name is the policy stage's loosening rule (confirmTypedStrict, D846)
// read on this node: what it reserves is a write that puts MORE in the group — a
// project that can now spend against the workspace's credentials, a repository
// it now covers, a ceiling above the one the owner set, the ceiling the node
// states DROPPED by the record (which leaves the group's spend unbounded), or
// the whole membership when there is no published record yet — and --yes does
// not stand in for it, which is what the sentence says to the operator whose
// script has just passed --yes and been refused. What it does NOT reserve is a
// removal — a member the record no longer declares shrinks the group, and the
// diff says so — or a ceiling the record ADDS where the node states none, which
// bounds the group rather than widening it (workspaceWidening).
//
// A person at a real terminal on a managed project is not asked for the typed
// name: the owner's ruling of 2026-11-06 keeps the typed prompt for data loss
// and secret values alone, and rewriting this node from the owner's own local
// record is neither — the person is told what is written (the diff above, whose
// added and removed lines both name what changes) and it is written. That is the
// policy stage's own reading of the ruling (policyMigrationConfirm), for this
// command's second published document. Every other session keeps what the
// branches below ask — a pipe, --json and --non-interactive by the typed name's
// own refusal, and a coding agent's was refused before this call (gateStep) —
// and the gate's own admission is untouched: where the gate judged and admitted
// the write, there was nothing left to ask.
func (p *workspacePublication) confirm(cmd *cobra.Command, yes bool, w io.Writer, admitted, managed bool, c initflow.Conditions) error {
	p.print(w)
	if admitted {
		return nil
	}
	what := p.what()
	if managed && initflow.PersonAtTheTerminal(c) {
		fmt.Fprintf(w, "note: %s\n", what)
		return nil
	}
	if len(p.widens) == 0 {
		return confirmRemoval(cmd, p.lc, yes, what)
	}
	why := p.wideningWhy()
	// The sentence is said once: a session with no terminal is refused with it
	// inside the error (confirmTypedStrict), and a door that printed it beside
	// that error would say the same thing twice.
	if stdinIsTerminal(cmd.InOrStdin()) {
		fmt.Fprintf(w, "%s\n", why)
	}
	return confirmTypedStrict(cmd, p.lc, why, what)
}

// wideningWhy is the sentence the typed confirmation shows and the refusal
// gives: what this write admits, and what it takes to admit it.
//
// The first publish has no published record for the write to be wider than, so
// it says what it decides instead: the record written now is the one every run
// of every member is admitted by, and there is nothing before it to be compared
// against. Neither sentence names a value out of the node — a node's
// repositories are the one string of it nothing holds to a grammar — and both
// name `--yes` as the thing that does not stand in, because the operator reading
// this from a script has just passed it and would otherwise take it for the
// answer.
func (p *workspacePublication) wideningWhy() string {
	if !p.found {
		return fmt.Sprintf("the workspace %s has no published record at %s, and this writes the one every run of its members reads — %s. %s is what confirms it and --yes does not stand in; run this yourself at your own terminal: %s",
			p.record.Name, p.path, strings.Join(p.widens, "; "), p.record.Name, selfCommand()+" update")
	}
	return fmt.Sprintf("this admits more to the workspace %s than the published record does: %s; every run of a member project reads the record, and what it admits is paid for by the workspace's own credentials", p.record.Name, strings.Join(p.widens, "; "))
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
		fmt.Fprintf(w, "%s: published (version %d) and rewritten in place as version %d\n", p.path, p.from, projectpolicy.WorkspaceVersion)
		for _, ch := range p.changes {
			fmt.Fprintf(w, "  %d to %d: %s\n", ch.From, ch.To, ch.Changed)
			if len(ch.Kept) > 0 {
				fmt.Fprintf(w, "    kept as it is: %s\n", strings.Join(ch.Kept, ", "))
			}
		}
		fmt.Fprintf(w, "  this installation declares: %s\n", p.recordSummary())
		// Both directions of the diff, and neither is left to be worked out by
		// eye against the node above: the write replaces the document, so what
		// it adds AND what it drops are what the operator is confirming.
		if len(p.removals) == 0 {
			fmt.Fprintf(w, "  nothing is removed\n")
		}
		for _, rm := range p.removals {
			fmt.Fprintf(w, "  removed: %s\n", rm)
		}
		for _, add := range p.widens {
			fmt.Fprintf(w, "  added: %s\n", add)
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
