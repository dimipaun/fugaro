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
	"github.com/dimipaun/fugaro/internal/safetext"
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
	// creds is the second document this stage publishes, in the same shape: the
	// published workspace credentials node and the one this run would write. It
	// is nil only where this function returns before the read, and every line of
	// the stage that names the write names both documents.
	creds *workspaceCredentialsPublication
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
// table, and `fugaro workspace project rm` of the last member leaves the table
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
	return fmt.Sprintf("the workspace %s declares no member, and a record that lists none is a workspace that admits nobody: nothing is published; declare the projects the workspace groups with 'fugaro workspace project add <project> --workspace %s' on this project's machine, then run %s",
		lc.Name, lc.Name, selfCommand()+" update")
}

// workspaceDeclared is whether THIS installation is a workspace's own: its
// config carries the OWNER's half of the membership (workspace_members or
// workspace_cap) and NOT the member's (workspace / workspace_pin), which is the
// predicate workspaceRecordOf is built on. It is one function because the
// question is one question, and three places ask it: the record's own read, the
// workspace's own project's root (W3, workspace_create.go) and the Identity
// Platform carve-out (init_firebase.go's refusePublicSignUp) — a workspace's own
// project is the one Firebase project whose public sign-up paths the owner
// enables (docs/workspaces/spec.md §3).
//
// A config that declares the cap and lists NO member is still this project's
// workspace: `fugaro workspace cap set` writes the cap alone, and `workspace
// project rm` of the last member leaves the table empty (workspaceNoMemberReason
// is that state's own sentence, and it is a skip and never a "not ours").
//
// The MEMBER's half decides where both are written, and the narrow reading is
// deliberate. The two halves are contradictory claims about one project: a
// workspace's own project is the one Firebase whose public sign-up paths are
// enabled, a member's the one where they are refused (spec §3); a workspace's
// own project holds the workspace's service account and its one grant, a
// member's holds neither, because the workspace holds nothing on a member's
// project and a member nothing on the workspace's (spec §5). Read as a
// workspace's own, such a config would enable the carve-out on a member's
// Firebase and create the workspace's account — with the grant on the member's
// own database — on the member's project, which is the widening rule 1 forbids;
// read as a member's, it publishes no record and converges no root, and the
// owner's remedy is to delete the half they did not mean. A config reaches the
// state through this machine's own commands, which name the config they write
// by the project and not by the half: `fugaro workspace cap set --workspace
// <a member>` and `fugaro workspace project add <a member> --workspace <name>`
// both write the owner's half into a member's file.
func workspaceDeclared(lc *localcfg.Config) bool {
	if workspaceMember(lc) {
		return false
	}
	return lc != nil && (lc.WorkspaceMembers != nil || lc.WorkspaceCap != nil)
}

// workspaceMember is whether a config carries the MEMBER's half of the
// membership: the workspace it is in and the pin it trusts, which localcfg
// requires together (validateWorkspace) and W1 reads as this project's
// membership. The two keys are asked as one, because a pin without a workspace
// is a trust nothing acts on and a workspace without a pin is a membership the
// loader refuses — neither is a state a valid config reaches, and a config this
// function is handed has not always been through that loader.
func workspaceMember(lc *localcfg.Config) bool {
	return lc != nil && (lc.Workspace != "" || !lc.WorkspacePin.IsZero())
}

// workspaceOwnFirebase is whether a read of Firebase project fp is a read of a
// workspace's own project: the config running the read declares the owner's
// half (workspaceDeclared) AND fp is the installation's own GCP project, which
// is the project a workspace's own is named after.
//
// The second half is the project the check is made against, and the carve-out
// that reads this (refusePublicSignUp, on the Identity Platform step) is about
// that project and not about the config: the pin admits the workspace's own
// sign-in there, so a read landing anywhere else has to be refused as any other
// installation's would be. The state where the two come apart is a split
// installation — budget.firebase_project naming another project than
// gcp_project, which workspaceSplitFirebase refuses a stage after this step, and
// which the Firebase step itself refuses before it when the flag names that
// other project — but the step asks it here rather than leaving it to them, so
// what it accepts does not depend on a check made elsewhere.
func workspaceOwnFirebase(lc *localcfg.Config, fp string) bool {
	return lc != nil && lc.GCPProject != "" && fp == lc.GCPProject && workspaceDeclared(lc)
}

// workspaceRecordOf builds the record this installation publishes. It answers
// nil for a config that is not a workspace's own — one that declares no member
// table and no cap, which is every member's config and every project in no
// workspace — and that answer is what keeps a member's run from reaching for a
// database it holds no grant on at all.
//
// The two lists are SORTED here rather than required to be sorted in the file:
// yamledit appends where `fugaro workspace project add` writes, so the file's
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
// from `fugaro workspace project rm` of the last member, and a stage of `fugaro
// update` that failed the run on it would leave every stage after it blocked.
func workspaceRecordOf(lc *localcfg.Config) (*workspace.Record, error) {
	if !workspaceDeclared(lc) {
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
	m, err := workspaceReadMachine()
	if err != nil {
		return nil, err
	}
	p.drift, err = workspaceDrift(m, rec.Name)
	if err != nil {
		return nil, err
	}
	// The record's own half of the answer, and it is asked before the write: the
	// node already carries what this installation declares, at this Fugaro's
	// version. Nothing is written for it and nothing is asked, because a stage of
	// `fugaro update` that rewrote it every run would be a write per run against a
	// document that is already right.
	recDone := found && len(p.changes) == 0 && workspaceNodeMatches(node, rec)
	if found && !recDone && len(node.UnknownKeys) > 0 {
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
	if !recDone {
		written, err := projectpolicy.WorkspaceNode(rec)
		if err != nil {
			return nil, userErr("building the workspace record for %s: %v", rec.Name, err)
		}
		p.body = written
		p.widens = workspaceWidening(rec, node, found)
		p.removals = workspaceRemovals(rec, node)
	}
	// The second document of this stage, read and built whether or not the record
	// needs a write: the two halves are one declaration, and a run whose record is
	// already right still has a credentials node to publish the first time or to
	// migrate. Only the write is conditional (write), not the read.
	creds, err := workspaceCredentialsIn(ctx, lc, db, rec, m)
	if err != nil {
		return nil, err
	}
	p.creds = creds
	p.done = recDone && creds.done
	return p, nil
}

// allWidens is what the confirmation's class rule reads: the record's widening
// list, and the pool the credentials node puts in — because one run's write
// admits both at once, and the two are the whole of what this write puts in the
// group that was not in it. The credentials node contributes its pool and not
// its member rows: every row it writes is a member the record already carries,
// whose admission is the record's own list above, and what the node adds beyond
// the record is the credentials a caller may be served.
func (p *workspacePublication) allWidens() []string {
	out := slices.Clone(p.widens)
	if p.creds != nil {
		out = append(out, p.creds.pool...)
	}
	return out
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
// That is what keeps the group's first record out of a coding agent's reach: it
// is money or permanent, and D1026 admits no plan gate's answer for one. A
// hand edit of `workspace_members` in the YAML — past the fenced `fugaro
// workspace project add` — is a document this stage would otherwise publish
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
// (`fugaro workspace project rm`), which is their own word about their own group
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

// The credentials node is the stage's second document
// (`workspace_credentials/<name>`, docs/workspace-service/contract-node.md). The
// record says who is in the group and what the group may spend; this one says
// how the workspace's own service resolves a caller of each member and which of
// the workspace's credentials each pool entry may serve, and one stage publishes
// both because they are one declaration read by two readers: the record is what
// the members roll up and admit against, and this node is what a
// shared-credential call is decided by.
//
// It is written the stage's own way — one read, a write conditional on the ETag
// of the document the operator read the diff against, a value comparison for
// "nothing to do" — with two differences the document itself brings:
//
//   - **The repositories are published canonically.** A node's `job_account_repos`
//     and a forge entry's `covers` are concrete repositories in the one spelling
//     this tree writes them in (`projectpolicy.Canonical`, R6/F42), because the
//     service holds the slug a run's verified identity carries to them. The local
//     table writes `owner/name`, so the provider — the other half of the
//     canonical name — is resolved from this machine's own project configs
//     (workspaceRepoProvider) and a repository whose provider nothing here states
//     is REFUSED rather than published in a spelling the node's reader refuses: a
//     node that named the repository the way the file does is a node the service
//     cannot compare anything to, and dropping it would leave the member's runs
//     refused by a sentence that tells them to ask for the update that was just
//     run (R19).
//   - **`all_members` is fenced by the cap** (D2977): the entry is offered to
//     every member, including the ones that join later, and the workspace's own
//     ceiling is what bounds it — so a workspace that states no cap while such an
//     entry exists, in the node or in the value this run would write, is refused.

// workspaceCredentialsPublication is the second document this stage publishes:
// one read of the published node and the node this run would write.
type workspaceCredentialsPublication struct {
	lc     *localcfg.Config
	db     *rtdb.Client
	path   string
	record *workspace.Record

	etag  string // the published node's ETag, as read; the write is conditional on it
	found bool   // whether a node is published at all
	// from is the published node's version, read off the document: 0 for a node
	// with no version at all, which no release wrote (the migration refuses it).
	from int
	// node is the published node as it stands, decoded and migrated: nil when
	// there is none. It is what the diff is a diff of.
	node *projectpolicy.WorkspaceCredentials
	// declared is the value this run writes, built from the member table and the
	// shared list of this installation's config.
	declared *projectpolicy.WorkspaceCredentials
	// changes are the migration steps this run would take.
	changes []projectpolicy.WorkspaceCredentialsChange
	// body is the document this run writes.
	body []byte
	// done is a node that already carries what this installation declares, at
	// this Fugaro's version: there is nothing to do, and nothing is written.
	done bool
	// added and removals are this document's diff, in the record's own two
	// directions: what the write states that the published node did not, and what
	// it takes away. The node is replaced whole, so both are what the operator
	// reads and confirms.
	added    []string
	removals []string
	// pool is what this write puts in the workspace's pool that the published
	// node did not: a credential offered to a caller of a member, offered to more
	// of them, or covering more repositories. It is the whole of this document's
	// answer to the confirmation's class rule (allWidens), and it is deliberately
	// not the diff: the rows this node writes are the record's own membership,
	// which the RECORD's widening list is the owner's word for, and what the node
	// adds beyond the record is what the workspace's money is spent on.
	pool []string
}

// readWorkspaceCredentialsNode reads the credentials node with its ETag, the
// precondition the write is made under. It is readWorkspaceNode for the other
// path of the same database.
func readWorkspaceCredentialsNode(ctx context.Context, db *rtdb.Client, lc *localcfg.Config, name string) (etag string, found bool, raw json.RawMessage, err error) {
	etag, found, err = db.GetETag(ctx, projectpolicy.PathWorkspaceCredentials(name), &raw)
	if err != nil {
		return "", false, nil, dbErr(lc, err, false)
	}
	return etag, found, raw, nil
}

// workspaceCredentialsRefusal is MigrateWorkspaceCredentials' refusal with the
// remedy that fits this side, and it is workspaceRefusal's twin: a node written
// by a newer Fugaro is refused with the sentence that names the move, and
// everything else is the migration's own sentence, which names what it could not
// read.
//
// The version class is read off the error's numbers and worded here rather than
// printed from it: WorkspaceVersionError's own sentence names the workspace
// RECORD, which is the other document of this stage, and an operator sent to the
// record by a refusal about the credentials node would look in the wrong place.
func workspaceCredentialsRefusal(path string, err error) error {
	var ve *projectpolicy.WorkspaceVersionError
	if errors.As(err, &ve) {
		return userErr("the workspace credentials node at %s is version %d and this Fugaro writes version %d: it was written by a newer Fugaro, so nothing was written; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run %s again",
			path, ve.Found, ve.Understood, selfCommand()+" update")
	}
	return userErr("the workspace credentials node at %s could not be published: %v; nothing was written", path, err)
}

// workspaceCredentialsIn is the read, the migration and the document this run
// would write for the credentials node. It is workspacePublicationIn's second
// half and returns its own reading, so a refusal of this document is worded for
// it.
func workspaceCredentialsIn(ctx context.Context, lc *localcfg.Config, db *rtdb.Client, rec *workspace.Record, m *workspaceMachine) (*workspaceCredentialsPublication, error) {
	c := &workspaceCredentialsPublication{lc: lc, db: db, path: projectpolicy.PathWorkspaceCredentials(rec.Name), record: rec}
	etag, found, raw, err := readWorkspaceCredentialsNode(ctx, db, lc, rec.Name)
	if err != nil {
		return nil, err
	}
	c.etag, c.found = etag, found
	var head struct {
		Version int `json:"version"`
	}
	_ = json.Unmarshal(raw, &head)
	c.from = head.Version
	// The keys this Fugaro does not model are refused BEFORE the migration, and
	// that order is the point: a migration re-encodes the document, so an unknown
	// key is gone from the bytes by the time a check made afterwards could read
	// it, and the operator would be told nothing while the key disappeared.
	standing := raw
	if found {
		decoded, _, derr := projectpolicy.DecodeWorkspaceCredentials(raw)
		if derr != nil {
			return nil, workspaceCredentialsRefusal(c.path, derr)
		}
		if len(decoded.UnknownKeys) > 0 {
			return nil, userErr("%s carries %d key(s) this Fugaro does not know (%s), and publishing this installation's declaration would write a document without them: nothing was written; upgrade Fugaro (brew upgrade dimipaun/tap/fugaro), then run %s again",
				c.path, len(decoded.UnknownKeys), unknownKeysText(decoded.UnknownKeys), selfCommand()+" update")
		}
		standing, c.changes, err = projectpolicy.MigrateWorkspaceCredentials(raw)
		if err != nil {
			return nil, workspaceCredentialsRefusal(c.path, err)
		}
	}
	node, _, err := projectpolicy.DecodeWorkspaceCredentials(standing)
	if err != nil {
		return nil, workspaceCredentialsRefusal(c.path, err)
	}
	c.node = node
	declared, err := workspaceCredentialsDeclared(ctx, lc, rec, m)
	if err != nil {
		return nil, err
	}
	c.declared = declared
	// The fence is asked on EVERY path, the unchanged one included, and that is
	// the path it exists for: the credentials node carries no cap, so an owner
	// who removes `workspace_cap` from their own file while an all_members entry
	// stands leaves the node saying exactly what the installation declares. The
	// run that skipped the fence there would report nothing to publish and leave
	// a pool offered to every member of a workspace whose ceiling is gone —
	// existing members and the ones that join later, bounded by nothing.
	if err := workspaceCredentialsAllMembersFence(rec, declared, node); err != nil {
		return nil, err
	}
	if found && len(c.changes) == 0 && workspaceCredentialsMatch(node, declared) {
		c.done = true
		return c, nil
	}
	written, err := projectpolicy.WorkspaceCredentialsNode(declared)
	if err != nil {
		return nil, userErr("building the workspace credentials node for %s: %v", rec.Name, err)
	}
	c.body = written
	c.added, c.removals, c.pool = workspaceCredentialsChanges(declared, node, found)
	return c, nil
}

// workspaceCredentialsDeclared builds the credentials node this installation
// publishes, from the same two local declarations the record comes from: the
// member table, whose rows carry what the workspace's service resolves a caller
// by and the repositories each job account is a job of, and the shared list of
// what the workspace offers its members.
//
// The rows are built in the RECORD's order and from the record's members, so the
// two documents of one run can never name different member lists: the record is
// where the membership is decided (workspaceRecordOf) and this value is that
// decision read for the other reader.
func workspaceCredentialsDeclared(ctx context.Context, lc *localcfg.Config, rec *workspace.Record, m *workspaceMachine) (*projectpolicy.WorkspaceCredentials, error) {
	table := workspaceTable(lc)
	w := &projectpolicy.WorkspaceCredentials{
		Version: projectpolicy.WorkspaceCredentialsVersion,
		Name:    rec.Name,
		Members: make([]projectpolicy.WorkspaceCredentialMember, 0, len(rec.Members)),
	}
	for _, mem := range rec.Members {
		row := table[mem.Project]
		// Both lists are held to ONE declaration (contract rule 1): the entry at
		// position i names the account job_accounts[i] names, so an entry is
		// written for every account the row carries, in the row's own order.
		accounts := make([]string, 0, len(row.JobAccounts))
		repos := make([]projectpolicy.WorkspaceJobAccount, 0, len(row.JobAccounts))
		for _, a := range row.JobAccounts {
			accounts = append(accounts, a.Account)
			canonical, err := workspaceCanonicalRepos(ctx, lc, m, mem.Project, a.Account, a.Repos)
			if err != nil {
				return nil, err
			}
			repos = append(repos, projectpolicy.WorkspaceJobAccount{Account: a.Account, Repos: canonical})
		}
		w.Members = append(w.Members, projectpolicy.WorkspaceCredentialMember{
			Project:         mem.Project,
			FirebaseProject: row.FirebaseProject,
			DatabaseRegion:  row.DatabaseRegion,
			FP:              row.FP,
			JobAccounts:     accounts,
			JobAccountRepos: repos,
			ExchangeURL:     row.ExchangeURL,
		})
	}
	shared := workspaceShared(lc)
	for _, name := range slices.Sorted(maps.Keys(shared)) {
		entry, err := workspaceSharedEntry(rec, name, shared[name])
		if err != nil {
			return nil, err
		}
		w.Shared = append(w.Shared, entry)
	}
	if err := w.Validate(); err != nil {
		return nil, userErr("the workspace credentials node this installation declares could not be published: %v", err)
	}
	return w, nil
}

// workspaceSharedEntry is one pool entry of the node, built from the local list's
// entry of the same name.
//
// The name and the kind are the local list's, and the three kind spellings are
// the same strings on both sides (R4: the route record's own spelling) — they
// are mapped here rather than passed through so that a kind either side grows
// alone is a refusal in this run instead of a document the service cannot route
// by. The projects are the entry's own, in the CREDITOR's name grammar, and each
// is held to the workspace's member table first: a shared entry naming a project
// the table does not declare is an entry the service would resolve a caller
// against and find no member for (F13), and the table is the whole of who the
// workspace reaches. An entry offered to every member names no project here —
// the node carries `all_members` instead, and the service offers it to each of
// the node's members, the intersection with the member's own policy unchanged
// (D2977, R5). The covers are the entry's repositories, canonical, with the
// entry's own kind as the provider segment (rule 4, U2).
func workspaceSharedEntry(rec *workspace.Record, name string, entry localcfg.WorkspaceShared) (projectpolicy.WorkspaceShared, error) {
	var kind string
	switch entry.Kind {
	case localcfg.WorkspaceSharedModel:
		kind = projectpolicy.SharedKindModel
	case localcfg.WorkspaceSharedBitbucket, localcfg.WorkspaceSharedGitHub:
		kind = entry.Kind
	default:
		return projectpolicy.WorkspaceShared{}, userErr("workspace_shared.%s: kind %q is not one this Fugaro publishes for a workspace's pool", name, entry.Kind)
	}
	// Who the entry may be served to is the node's one-of-two column (contract
	// C3's rule 5): an entry offered to the members it names carries the projects
	// and no all_members at all, and one offered to every member carries
	// all_members and no projects. The local list's own loader refuses both at
	// once, and this writes the one the entry declared rather than passing both
	// through, so a value built past that loader is still a node the service
	// reads.
	if entry.AllMembers && len(entry.Projects) > 0 {
		return projectpolicy.WorkspaceShared{}, userErr("workspace_shared.%s carries both all_members and projects, which the node's own reader refuses: an entry offered to every member of the workspace names no project, and one offered to the members it names carries projects and no all_members; nothing was written — write the one the entry means ('fugaro workspace share %s --workspace %s --all-members', or --projects <member>)",
			name, name, rec.Name)
	}
	out := projectpolicy.WorkspaceShared{Name: name, Kind: kind, AllMembers: entry.AllMembers}
	if !entry.AllMembers {
		out.Projects = slices.Clone(entry.Projects)
		slices.Sort(out.Projects)
	}
	// The local list's name is the map key and the node's is a field, so an empty
	// key is the one entry the two cannot be built from; the loader refuses it, and
	// a config this function is handed has not always been through that loader.
	if out.Name == "" {
		return projectpolicy.WorkspaceShared{}, userErr("a workspace_shared entry has no name: the key is the credential's name as the workspace's bundle holds it")
	}
	for _, p := range out.Projects {
		if !slices.Contains(memberProjects(rec), p) {
			return projectpolicy.WorkspaceShared{}, userErr("workspace_shared.%s offers the workspace's credential to %s, which the workspace's member table does not declare: the workspace's own service would be asked to reach a member the workspace does not have (F13); nothing was written — declare the member ('fugaro workspace project add %s --workspace %s'), or offer the entry to the members it is for ('fugaro workspace share %s --workspace %s --projects <member>'), then run %s again",
				out.Name, p, p, rec.Name, out.Name, rec.Name, selfCommand()+" update")
		}
	}
	if kind != projectpolicy.SharedKindModel {
		covers, err := workspaceCanonicalCovers(kind, out.Name, entry.Covers)
		if err != nil {
			return projectpolicy.WorkspaceShared{}, err
		}
		out.Covers = covers
	}
	return out, nil
}

// workspaceCanonicalCovers canonicalizes a forge entry's coverage with the
// entry's own kind as the provider segment: the kind IS the provider of every
// repository the entry reaches (contract rule 4), so no lookup is needed here
// and a cover whose own provider disagrees would be a node whose two columns
// contradict each other.
func workspaceCanonicalCovers(kind, name string, covers []string) ([]string, error) {
	out := make([]string, 0, len(covers))
	for _, cover := range covers {
		canonical, err := projectpolicy.Canonical(kind, cover)
		if err != nil {
			return nil, userErr("workspace_shared.%s: covers %s: %v; the entry's kind is the provider every repository it covers is on", name, pluginwire.Printable(cover), err)
		}
		out = append(out, canonical)
	}
	slices.Sort(out)
	return out, nil
}

// workspaceCanonicalRepos is the repositories of one job service account, in the
// spelling the node carries them in: `provider/owner/name`, because the service
// holds the slug a run's verified identity carries to this list (R6, D2992).
//
// The provider is the half the local table does not carry — every repository in
// it is written `owner/name` — so it is read from this machine's own configs
// (workspaceRepoProvider) and a repository nothing here states is a refusal and
// not a guess: an invented provider would hold a run of another forge's
// repository to this account, and a node that named the repository the way the
// file does is one `projectpolicy.Canonical` refuses at the reader.
func workspaceCanonicalRepos(ctx context.Context, lc *localcfg.Config, m *workspaceMachine, member, account string, repos []string) ([]string, error) {
	out := make([]string, 0, len(repos))
	for _, repo := range repos {
		provider := workspaceRepoProvider(ctx, lc, m, member, repo)
		if provider == "" {
			return nil, userErr("workspace_members.%s: the job service account %s is a job of %s, and no project config on this machine states which forge that repository is on: the workspace's credentials node carries a job account's repositories canonically (<provider>/<owner>/<name>) because the service compares a run's verified repository to them, so the provider cannot be left out or guessed; nothing was written — write the provider into the repository's entry in the member's own project config, or in this project's ('repos.owner/name.provider'), then run %s again",
				member, pluginwire.Printable(account), pluginwire.Printable(repo), selfCommand()+" update")
		}
		canonical, err := projectpolicy.Canonical(provider, repo)
		if err != nil {
			return nil, userErr("workspace_members.%s: the job service account %s is a job of %s: %v", member, pluginwire.Printable(account), pluginwire.Printable(repo), err)
		}
		out = append(out, canonical)
	}
	slices.Sort(out)
	return out, nil
}

// workspaceRepoProvider is the provider kind a repository of the member table is
// published under, resolved from this machine's own files and never guessed:
// the member's own project config first — the member's repository is the
// member's statement, and its config is the one this machine holds when the two
// projects are on one machine — then this installation's own config and the
// repository's checkout, which is the order repoProvider already resolves a
// launch's provider in. An empty answer is a repository nothing here can spell
// canonically, and the caller refuses.
func workspaceRepoProvider(ctx context.Context, lc *localcfg.Config, m *workspaceMachine, member, repo string) string {
	if mc, ok := m.held[member]; ok && mc != nil {
		if p := (&cloudEnv{lc: mc}).localRepoProvider(repo); p != "" {
			return p
		}
	}
	return (&cloudEnv{lc: lc}).repoProvider(repo, checkoutConfig(ctx, repo))
}

// memberProjects is a record's member list as the projects a shared entry may be
// offered to.
func memberProjects(rec *workspace.Record) []string {
	out := make([]string, 0, len(rec.Members))
	for _, m := range rec.Members {
		out = append(out, m.Project)
	}
	return out
}

// workspaceCredentialsAllMembersFence is D2977's rule, and it is the writer's
// because the node carries no cap: a pool entry offered to every member — the
// members that join after it is written included — is bounded by the workspace's
// ceiling alone, and the ceiling is the owner's fence over their own money. A
// workspace that states no cap while such an entry exists is refused, and both
// sides are asked: the entry the published node carries (the cap the owner
// removed from their own file) and the entry this run would write (the cap a
// release dropped, or a node a newer Fugaro wrote, read on the value going out).
func workspaceCredentialsAllMembersFence(rec *workspace.Record, decl, node *projectpolicy.WorkspaceCredentials) error {
	if rec.Cap.Set {
		return nil
	}
	var names []string
	for _, list := range [][]projectpolicy.WorkspaceShared{decl.Shared, nodeShared(node)} {
		for _, s := range list {
			if s.AllMembers && !slices.Contains(names, s.Name) {
				names = append(names, s.Name)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	return userErr("the workspace %s states no cap, and %s offered to every member of the workspace: an entry offered to every member is bounded by the workspace's ceiling alone, so a member that joins later would reach it with nothing bounding what it spends, and this write publishes the pool without that entry's key; nothing was written — state the ceiling again ('fugaro workspace cap set --workspace %s --monthly <dollars>'), then run %s again",
		rec.Name, quotedList(names), rec.Name, selfCommand()+" update")
}

// nodeShared is a published node's pool, whatever is there.
func nodeShared(node *projectpolicy.WorkspaceCredentials) []projectpolicy.WorkspaceShared {
	if node == nil {
		return nil
	}
	return node.Shared
}

// quotedList names a set of entries in a sentence.
func quotedList(names []string) string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, pluginwire.Printable(n))
	}
	return strings.Join(out, ", ")
}

// workspaceCredentialsMatch reports whether a published node carries what this
// installation declares, value by value and in order. It is
// workspaceNodeMatches' twin for this document, and for the same reason: the
// database round-trips the document through its own encoding, so a comparison of
// bytes would rewrite a node that says exactly what the table says, every run.
func workspaceCredentialsMatch(node, decl *projectpolicy.WorkspaceCredentials) bool {
	if node == nil || decl == nil || node.Version != decl.Version || node.Name != decl.Name {
		return false
	}
	if len(node.Members) != len(decl.Members) || len(node.Shared) != len(decl.Shared) {
		return false
	}
	for i, m := range decl.Members {
		got := node.Members[i]
		if got.Project != m.Project || got.FirebaseProject != m.FirebaseProject || got.DatabaseRegion != m.DatabaseRegion ||
			got.FP != m.FP || got.ExchangeURL != m.ExchangeURL ||
			!slices.Equal(got.JobAccounts, m.JobAccounts) || len(got.JobAccountRepos) != len(m.JobAccountRepos) {
			return false
		}
		for j, a := range m.JobAccountRepos {
			if got.JobAccountRepos[j].Account != a.Account || !slices.Equal(got.JobAccountRepos[j].Repos, a.Repos) {
				return false
			}
		}
	}
	for i, s := range decl.Shared {
		got := node.Shared[i]
		if got.Name != s.Name || got.Kind != s.Kind || got.AllMembers != s.AllMembers ||
			!slices.Equal(got.Projects, s.Projects) || !slices.Equal(got.Covers, s.Covers) {
			return false
		}
	}
	return true
}

// workspaceCredentialsChanges walks the published node and the value this run
// would write and reports both directions at once: what the write states that
// the node did not (the diff's added lines), what it takes away (the diff's
// removed lines), and, apart from both, the pool it puts in — which is the only
// part of this document the confirmation's class rule reads.
//
// The rule is the record's own (workspaceWidening, workspaceRemovals) read on
// this document, with one difference that comes from what the document is: the
// ROWS are not a widening, and the POOL is. Every row this node writes is
// resolved from the member table of the record being published in the same run
// (workspaceCredentialsDeclared) — the members, their accounts and the
// repositories each account is a job of — so the value that admits a member's
// runs at all is the record's membership, and the record's own widening list is
// where the owner's word for it is taken. What the node adds beyond what the
// record already admits is the pool: a credential of the workspace offered to a
// caller that was not offered it before, which is the workspace's money being
// spent, and that is what a run may not put in behind a mark.
//
// A value that changes is in both directions for that reason: the member's row
// names a different Firebase project, and that is one change with two readers.
func workspaceCredentialsChanges(decl, node *projectpolicy.WorkspaceCredentials, found bool) (added, removals, pool []string) {
	if !found || node == nil {
		// The first node is stated whole by the diff's own line (print), which
		// names the members it resolves and what its pool holds. What it puts in
		// the pool is the class rule's question, and it is asked of the entries
		// themselves: a first node whose pool is empty offers nobody a credential,
		// and a first node that carries one is the workspace offering its own
		// credentials to a caller for the first time.
		for _, s := range decl.Shared {
			line := fmt.Sprintf("the pool entry %s (%s) offered to %s", pluginwire.Printable(s.Name), s.Kind, offerText(s))
			added = append(added, line)
			pool = append(pool, line)
		}
		return added, nil, pool
	}
	published := map[string]projectpolicy.WorkspaceCredentialMember{}
	for _, m := range node.Members {
		published[m.Project] = m
	}
	for _, m := range decl.Members {
		old, listed := published[m.Project]
		if !listed {
			added = append(added, fmt.Sprintf("the member %s, as the workspace's service resolves its callers (firebase %s, accounts %s)", m.Project, pluginwire.Printable(m.FirebaseProject), quotedList(m.JobAccounts)))
			continue
		}
		for _, a := range m.JobAccounts {
			before, had := jobReposOf(old, a)
			now, _ := jobReposOf(m, a)
			if !had {
				added = append(added, fmt.Sprintf("the account %s as a job of %s", pluginwire.Printable(a), m.Project))
				continue
			}
			for _, repo := range now {
				if !slices.Contains(before, repo) {
					added = append(added, fmt.Sprintf("the account %s as a job of %s", pluginwire.Printable(a), pluginwire.Printable(repo)))
				}
			}
		}
		for _, field := range []struct{ what, was, now string }{
			{"firebase_project", old.FirebaseProject, m.FirebaseProject},
			{"fp", old.FP, m.FP},
			{"database_region", old.DatabaseRegion, m.DatabaseRegion},
			{"exchange_url", old.ExchangeURL, m.ExchangeURL},
		} {
			if field.was == field.now {
				continue
			}
			added = append(added, fmt.Sprintf("the %s of the member %s, now %s", field.what, m.Project, pluginwire.Printable(field.now)))
			removals = append(removals, fmt.Sprintf("the %s of the member %s, %s", field.what, m.Project, pluginwire.Printable(field.was)))
		}
	}
	// offer states a pool line once and files it under both readers of it: the
	// operator reads it as what this write adds, and the confirmation's class
	// rule reads it as what this run puts in the workspace's pool.
	offer := func(line string) {
		added = append(added, line)
		pool = append(pool, line)
	}
	publishedShared := map[string]projectpolicy.WorkspaceShared{}
	for _, s := range node.Shared {
		publishedShared[s.Name] = s
	}
	for _, s := range decl.Shared {
		old, listed := publishedShared[s.Name]
		if !listed {
			offer(fmt.Sprintf("the pool entry %s (%s) offered to %s", pluginwire.Printable(s.Name), s.Kind, offerText(s)))
			continue
		}
		if old.Kind != s.Kind {
			offer(fmt.Sprintf("the pool entry %s, served as %s instead of %s", pluginwire.Printable(s.Name), s.Kind, old.Kind))
		}
		if s.AllMembers && !old.AllMembers {
			offer(fmt.Sprintf("the pool entry %s offered to every member of the workspace, the ones that join later included", pluginwire.Printable(s.Name)))
		}
		for _, p := range s.Projects {
			if !slices.Contains(old.Projects, p) {
				offer(fmt.Sprintf("the pool entry %s offered to %s", pluginwire.Printable(s.Name), p))
			}
		}
		for _, cover := range s.Covers {
			if !slices.Contains(old.Covers, cover) {
				offer(fmt.Sprintf("the pool entry %s covering %s", pluginwire.Printable(s.Name), pluginwire.Printable(cover)))
			}
		}
	}
	declared := map[string]projectpolicy.WorkspaceCredentialMember{}
	for _, m := range decl.Members {
		declared[m.Project] = m
	}
	for _, m := range node.Members {
		kept, still := declared[m.Project]
		if !still {
			removals = append(removals, fmt.Sprintf("the member %s", pluginwire.Printable(m.Project)))
			continue
		}
		for _, a := range m.JobAccounts {
			repos, _ := jobReposOf(m, a)
			now, still := jobReposOf(kept, a)
			if !still {
				removals = append(removals, fmt.Sprintf("the account %s of %s", pluginwire.Printable(a), pluginwire.Printable(m.Project)))
				continue
			}
			for _, repo := range repos {
				if !slices.Contains(now, repo) {
					removals = append(removals, fmt.Sprintf("the account %s as a job of %s", pluginwire.Printable(a), pluginwire.Printable(repo)))
				}
			}
		}
	}
	declaredShared := map[string]projectpolicy.WorkspaceShared{}
	for _, s := range decl.Shared {
		declaredShared[s.Name] = s
	}
	for _, s := range node.Shared {
		kept, still := declaredShared[s.Name]
		if !still {
			removals = append(removals, fmt.Sprintf("the pool entry %s", pluginwire.Printable(s.Name)))
			continue
		}
		if s.AllMembers && !kept.AllMembers {
			removals = append(removals, fmt.Sprintf("the pool entry %s offered to every member of the workspace", pluginwire.Printable(s.Name)))
		}
		for _, p := range s.Projects {
			if !slices.Contains(kept.Projects, p) {
				removals = append(removals, fmt.Sprintf("the pool entry %s from %s", pluginwire.Printable(s.Name), p))
			}
		}
		for _, cover := range s.Covers {
			if !slices.Contains(kept.Covers, cover) {
				removals = append(removals, fmt.Sprintf("%s from the coverage of %s", pluginwire.Printable(cover), pluginwire.Printable(s.Name)))
			}
		}
	}
	return added, removals, pool
}

// jobReposOf is one account's repositories in a decoded row.
func jobReposOf(m projectpolicy.WorkspaceCredentialMember, account string) ([]string, bool) {
	for _, a := range m.JobAccountRepos {
		if a.Account == account {
			return a.Repos, true
		}
	}
	return nil, false
}

// offerText names whom a pool entry is offered to.
func offerText(s projectpolicy.WorkspaceShared) string {
	if s.AllMembers {
		return "every member of the workspace"
	}
	return strings.Join(s.Projects, ", ")
}

// credentialsMembersText names the members a value carries.
func credentialsMembersText(w *projectpolicy.WorkspaceCredentials) string {
	if w == nil || len(w.Members) == 0 {
		return "no member"
	}
	out := make([]string, 0, len(w.Members))
	for _, m := range w.Members {
		out = append(out, m.Project)
	}
	return "members " + strings.Join(out, ", ")
}

// credentialsPoolText names the pool a value carries, or says that there is none.
func credentialsPoolText(w *projectpolicy.WorkspaceCredentials) string {
	if w == nil || len(w.Shared) == 0 {
		return "the pool is empty"
	}
	out := make([]string, 0, len(w.Shared))
	for _, s := range w.Shared {
		out = append(out, fmt.Sprintf("%s (%s, %s)", pluginwire.Printable(s.Name), s.Kind, offerText(s)))
	}
	return "pool " + strings.Join(out, "; ")
}

// workspaceDrift is the two-sided membership check as far as this machine's own
// files can make it: the members the workspace's table declares whose own
// config here names no workspace, and the configs here that name this workspace
// whose table does not declare them (workspaceRow.problems, W1). The sentences
// are W1's, verbatim and not a second wording of them: `fugaro workspace show`
// reports the same state, and two wordings of one state is two things an
// operator has to read as one.
//
// The machine is passed in rather than read here because the same read answers
// the credentials node's two questions: which project configs this machine holds
// (the member's own, for a repository's provider) and which of them names this
// workspace.
func workspaceDrift(m *workspaceMachine, name string) ([]string, error) {
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
// money or permanent (D846, M3), and D1026 admits no plan gate's answer for one
// — so a member is still admitted by the owner's own word and never by a mark
// (SEC, rule 1). It is not in runCovered, for the
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
//
// The skip workspaceSkip answers with — no database named to publish in, a
// database this installation never adopted, a workspace that lists no member —
// is this stage's work left undone and never a state with nothing to do: not one
// of the three publishes anything, so a step run for this stage stays owed
// (initflow.Status.Undone, the policy stage's own rule for its parallel skips).
// The other skip below, p == nil, is nothing to do: a config that declares no
// workspace publishes no record anywhere, and Check, Plan and Apply all say so.
func (s *workspaceStage) Check(ctx context.Context) (initflow.Status, error) {
	p, err := s.plan(ctx)
	if reason, skip := workspaceSkip(err); skip {
		return initflow.Status{State: initflow.Skipped, Detail: reason, Undone: true}, nil
	}
	if err != nil {
		return initflow.Status{}, err
	}
	if p == nil {
		return initflow.Status{State: initflow.Skipped, Detail: workspaceNoRecordReason(s.e.lc)}, nil
	}
	switch {
	case p.done:
		return initflow.Status{State: initflow.Done, Detail: fmt.Sprintf("the published workspace record at %s and the workspace credentials node at %s are version %d and carry what this installation declares",
			p.path, projectpolicy.PathWorkspaceCredentials(p.record.Name), projectpolicy.WorkspaceVersion)}, nil
	case !p.found:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("no workspace record is published at %s: this run publishes the one this installation declares (%s), after your confirmation%s", p.path, p.recordSummary(), p.credentialsClause())}, nil
	case len(p.changes) > 0:
		return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the published workspace record at %s is version %d and this Fugaro writes version %d%s", p.path, p.from, projectpolicy.WorkspaceVersion, p.credentialsClause())}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: fmt.Sprintf("the local config states what the published workspace record at %s does not carry: this run publishes it, after your confirmation%s", p.path, p.credentialsClause())}, nil
}

// credentialsClause is the half of a status or plan line that names the second
// document this stage publishes, and it is empty when that node already carries
// what this installation declares: the line then has nothing of its to say. It
// is a clause rather than a second line because the two documents are one
// declaration of one run: an operator reads one step, and the step writes both.
func (p *workspacePublication) credentialsClause() string {
	c := p.creds
	if c == nil || c.done {
		return ""
	}
	switch {
	case !c.found:
		return fmt.Sprintf("; no workspace credentials node is published at %s yet, and the same run writes the one this installation declares", c.path)
	case len(c.changes) > 0:
		return fmt.Sprintf("; the workspace credentials node at %s is version %d and the same run migrates it to version %d in place", c.path, c.from, projectpolicy.WorkspaceCredentialsVersion)
	}
	return fmt.Sprintf("; the workspace credentials node at %s does not carry what this installation declares, and the same run rewrites it", c.path)
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
		return initflow.Plan{NothingToDo: true, Detail: fmt.Sprintf("the published workspace record at %s and the workspace credentials node at %s carry what this installation declares",
			p.path, projectpolicy.PathWorkspaceCredentials(p.record.Name))}, nil
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
		return fmt.Sprintf("publish the workspace record this installation declares at %s, after your confirmation%s", p.path, p.credentialsClause())
	case len(p.changes) > 0:
		return fmt.Sprintf("migrate the published workspace record at %s from version %d to %d in place, after your confirmation%s", p.path, p.from, projectpolicy.WorkspaceVersion, p.credentialsClause())
	}
	return fmt.Sprintf("publish the workspace record this installation declares at %s, after your confirmation%s", p.path, p.credentialsClause())
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
	// run's own writer, and what the confirmation asks goes through a shared
	// door that holds the command rather than the run (confirmStageWrite,
	// confirmWidening): the whole call is put to the person with the line in
	// flight down (initRun.asking), so neither the diff nor the CONFIRM block is
	// drawn over (G1) — the policy stage's own reading of the same rule, for
	// this command's second published document.
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

// confirm is the confirmation this write is made behind. A write that ADMITS
// something to the group — the whole membership, where the workspace has no
// published record yet, and any later write that adds a member, a repository or
// a ceiling — is the owner's own step (D1555, D1552's fence): a person at a real
// terminal is told what it admits and what it writes and it is written, a
// session that is not one keeps the refusal naming the terminal, and --ask puts
// the question back for whoever asked for it. The typed project name it used to
// take (confirmTypedStrict) is gone with D1540 — no command waits on stdin — and
// the coding agent's session was refused before this call: Apply's own marker
// check where the gate is not underway, and gateStep's class sentence on a
// managed project. --yes does not stand in for a widening (the stage is not a
// step the gate admits, and who is in the group is the owner's own word, SEC
// rule 1), which is the one arm of confirmStageWrite the widening does not take.
//
// Every other write of this stage — a plain rewrite, a migration, a removal —
// is the ordinary step, confirmed by confirmStageWrite: nothing is asked at the
// owner's terminal either, --yes stands in where it reaches the stage, --ask
// puts the question back. A managed project's gate that admitted the node
// replaces all of it (`admitted`), and the diff is printed either way.
func (p *workspacePublication) confirm(cmd *cobra.Command, yes bool, w io.Writer, admitted, managed bool, c initflow.Conditions) error {
	p.print(w)
	if admitted {
		return nil
	}
	what := p.what()
	// --ask's own read (restoreAsk): the flag puts a question back only where
	// the gate replaced one, so a project the marks did not answer "managed"
	// for asks nothing and keeps the person at its terminal (PersonAtTheTerminal
	// reads Ask).
	c = restoreAsk(c, managed && c.Ask)
	if managed && initflow.PersonAtTheTerminal(c) {
		fmt.Fprintf(w, "note: %s\n", what)
		return nil
	}
	if len(p.allWidens()) == 0 {
		return confirmStageWrite(cmd, p.lc, c, yes, what)
	}
	if stdinIsTerminal(cmd.InOrStdin()) {
		fmt.Fprintf(w, "%s\n", p.wideningWhy())
	}
	return confirmWidening(cmd, p.lc, c, p.wideningWhy(), what)
}

// confirmWidening is confirmStageWrite where the write is the group's own
// membership (D1555): the banner and what this program does with the document
// are the same, and the question a session with no terminal leaves behind is
// not. What answers for a widening is the owner's own session — a person at a
// real terminal confirms it by being there, and nothing is typed at them
// (D1540) — so the arms are --ask's [y/N], the person themselves, and the
// refusal. --yes is not among them: a flag is not who is in the group.
//
// why is what this write admits, and it is the refusal's own sentence; the
// caller prints it above this banner where there is a terminal to print it to,
// so a refusal never says it twice.
func confirmWidening(cmd *cobra.Command, lc *localcfg.Config, c initflow.Conditions, why, what string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "⚠ CONFIRM (project %s): %s\n", safetext.Field(lc.Name), what)
	switch {
	case c.Ask:
		// The owner asked for this step's question back: askConfirm asks it
		// below and refuses in its own sentence where nobody can answer one.
	case initflow.PersonAtTheTerminal(c):
		return nil
	default:
		return userErr("%s: %s", initflow.NeedsTerminal("the confirmation of this step"), why)
	}
	ok, err := askConfirm(cmd, what+"?")
	if err != nil {
		return err
	}
	if !ok {
		return userErr("not confirmed (the answer was no); nothing was written")
	}
	return nil
}

// wideningWhy is what this write admits, and what it takes to admit it: the
// sentence this stage prints above the banner, and the sentence a refusal
// carries, said once either way.
//
// The first publish has no published record for the write to be wider than, so
// it says what it decides instead: the record written now is the one every run
// of every member is admitted by, and there is nothing before it to be compared
// against, which is also why it is the one arm that names the command to run
// (selfCommand). Neither sentence names a value out of the node — a node's
// repositories are the one string of it nothing holds to a grammar — and both
// name `--yes` as the thing that does not stand in, because the operator reading
// this from a script has just passed it and would otherwise take it for the
// answer.
func (p *workspacePublication) wideningWhy() string {
	if !p.found {
		return fmt.Sprintf("the workspace %s has no published record at %s, and this writes the one every run of its members reads — %s. It is the owner's own step: no flag stands in for it and nothing is typed at you, so run it yourself at your own terminal: %s",
			p.record.Name, p.path, strings.Join(p.allWidens(), "; "), selfCommand()+" update")
	}
	if p.body == nil && p.creds != nil && !p.creds.found {
		// The record is already published and right; what is new is the other
		// document of the same declaration, and this arm is reached for exactly one
		// reason: the pool it publishes offers the workspace's own credentials
		// where nobody could be served any (allWidens reads the pool and not the
		// rows, the rows being the record's own membership, whose widening was the
		// record's). That is money, so it is the owner's own step.
		return fmt.Sprintf("the workspace %s has no published credentials node at %s, and the one this writes offers its own credentials where nobody could be served them — %s. It is the owner's own step: no flag stands in for it and nothing is typed at you, so run it yourself at your own terminal: %s",
			p.record.Name, p.creds.path, strings.Join(p.allWidens(), "; "), selfCommand()+" update")
	}
	return fmt.Sprintf("this admits more to the workspace %s than the published record does: %s; every run of a member project reads the record, and what it admits is paid for by the workspace's own credentials", p.record.Name, strings.Join(p.allWidens(), "; "))
}

// what names this write as the confirmation reads it. The three arms are the
// three documents a run of this stage can put out: both, the record alone (the
// node is already right), and the node alone (the record is already right) —
// which is why the sentence is built from p.body rather than from the node's
// own state, the node's arm being the one that is not always there.
func (p *workspacePublication) what() string {
	credsPath := projectpolicy.PathWorkspaceCredentials(p.record.Name)
	if p.body == nil {
		return fmt.Sprintf("this publishes the workspace credentials node at %s, from what this installation declares: the published workspace record already carries what this installation declares and is left as it is, and the workspace's own service reads the node to resolve a caller of each member and to decide which of the workspace's credentials that caller may be served", credsPath)
	}
	if len(p.changes) > 0 {
		return fmt.Sprintf("this migrates the published workspace record at %s from version %d to %d in place, and publishes the workspace credentials node at %s, from what this installation declares: the workspace's own service reads the first for the member list it rolls up and admits against, and the second to resolve a caller of a member and decide which of the workspace's credentials it may be served",
			p.path, p.from, projectpolicy.WorkspaceVersion, credsPath)
	}
	return fmt.Sprintf("this publishes the workspace record at %s and the workspace credentials node at %s from what this installation declares: the workspace's own service reads the first for the member list it rolls up and admits against, and the second to resolve a caller of a member and decide which of the workspace's credentials it may be served",
		p.path, credsPath)
}

// doneDetail is the line the run reports the write with, and it names the
// documents this run actually wrote: a run whose record was already right
// publishes the credentials node alone, and a line that claimed both would say
// the record moved when nothing touched it.
func (p *workspacePublication) doneDetail() string {
	credsPath := projectpolicy.PathWorkspaceCredentials(p.record.Name)
	creds := p.creds != nil && p.creds.body != nil
	record := "published the workspace record this installation declares at " + p.path
	if len(p.changes) > 0 {
		record = fmt.Sprintf("migrated the published workspace record at %s from version %d to %d in place", p.path, p.from, projectpolicy.WorkspaceVersion)
	}
	switch {
	case p.body == nil:
		return fmt.Sprintf("published the workspace credentials node this installation declares at %s", credsPath)
	case !creds:
		return record
	case len(p.changes) > 0:
		return record + fmt.Sprintf(", and published the workspace credentials node at %s", credsPath)
	}
	return record + fmt.Sprintf(" and the workspace credentials node this installation declares at %s", credsPath)
}

// print is the one confirmation diff: what the record states, what it changes
// against the node that is published now, what the migration moved, and the
// half-done memberships this machine can see.
func (p *workspacePublication) print(w io.Writer) {
	switch {
	case p.body == nil:
		// The record already carries what this installation declares: this run
		// writes the credentials node beside it, and a diff of the record would be
		// a diff of a document nothing is written to.
		fmt.Fprintf(w, "%s: the published record already carries what this installation declares; this run writes nothing to it\n", p.path)
	case !p.found:
		fmt.Fprintf(w, "%s: no record is published yet; this writes the first one for the workspace %s (%s)\n", p.path, p.record.Name, p.recordSummary())
	default:
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
	if c := p.creds; c != nil && !c.done {
		c.print(w)
	}
	for _, d := range p.drift {
		fmt.Fprintf(w, "  %s\n", d)
	}
}

// print is the second document's half of the confirmation diff: the same lines
// the record's have, for the same reason — the write replaces the document, so
// what it changes, what it adds and what it drops are all what the operator is
// confirming.
func (c *workspaceCredentialsPublication) print(w io.Writer) {
	if !c.found {
		fmt.Fprintf(w, "%s: no credentials node is published yet; this writes the first one for the workspace %s (%s; %s)\n",
			c.path, c.record.Name, credentialsMembersText(c.declared), credentialsPoolText(c.declared))
	} else {
		fmt.Fprintf(w, "%s: published (version %d) and rewritten in place as version %d\n", c.path, c.from, projectpolicy.WorkspaceCredentialsVersion)
		for _, ch := range c.changes {
			fmt.Fprintf(w, "  %d to %d: %s\n", ch.From, ch.To, ch.Changed)
			if len(ch.Kept) > 0 {
				fmt.Fprintf(w, "    kept as it is: %s\n", strings.Join(ch.Kept, ", "))
			}
		}
		fmt.Fprintf(w, "  this installation declares: %s; %s\n", credentialsMembersText(c.declared), credentialsPoolText(c.declared))
		if len(c.removals) == 0 {
			fmt.Fprintf(w, "  nothing is removed\n")
		}
		for _, rm := range c.removals {
			fmt.Fprintf(w, "  removed: %s\n", rm)
		}
		for _, add := range c.added {
			fmt.Fprintf(w, "  added: %s\n", add)
		}
	}
}

// write puts the record back with the ETag it was read at. A concurrent writer —
// another `fugaro update` on the owner's other machine, a hand edit through the
// console — makes the precondition fail, and that is a refusal and not a retry:
// the operator confirmed a diff of the node this write replaces, and a write
// that suppressed a change nobody read would be exactly the unasked rewrite the
// one command must not make.
func (p *workspacePublication) write(ctx context.Context) error {
	if p.body != nil {
		if err := p.db.PutIfMatch(ctx, p.path, p.etag, json.RawMessage(p.body)); err != nil {
			if errors.Is(err, rtdb.ErrPrecondition) {
				return userErr("the workspace record at %s changed while this publish was being confirmed (another machine published it meanwhile), so nothing was written: run %s again to read what it now holds",
					p.path, selfCommand()+" update")
			}
			return dbErr(p.lc, err, true)
		}
	}
	// The second document, under its own ETag: the two are one declaration but
	// two writes, so a precondition that fails here is a refusal naming this
	// node and not the record the operator read the diff of.
	if c := p.creds; c != nil && c.body != nil {
		if err := c.db.PutIfMatch(ctx, c.path, c.etag, json.RawMessage(c.body)); err != nil {
			if errors.Is(err, rtdb.ErrPrecondition) {
				return userErr("the workspace credentials node at %s changed while this publish was being confirmed (another machine published it meanwhile), so nothing was written there: run %s again to read what it now holds",
					c.path, selfCommand()+" update")
			}
			return dbErr(p.lc, err, true)
		}
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
	if s.p.body != nil {
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
	}
	if c := s.p.creds; c != nil && c.body != nil {
		_, found, raw, err := readWorkspaceCredentialsNode(ctx, c.db, c.lc, c.record.Name)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("the published workspace credentials node is gone")
		}
		node, _, err := projectpolicy.DecodeWorkspaceCredentials(raw)
		if err != nil {
			return fmt.Errorf("the workspace credentials node was written but cannot be read back: %w", err)
		}
		if !workspaceCredentialsMatch(node, c.declared) {
			return fmt.Errorf("the workspace credentials node was written but %s does not carry what this installation declares", c.path)
		}
	}
	return nil
}
